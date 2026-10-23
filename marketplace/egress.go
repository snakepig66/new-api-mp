package marketplace

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrEgressDenied marks every refusal of the egress guard (§3.11): a host
// outside the whitelist, an IP literal used as a host, or a resolved IP in a
// non-public range. Callers match it with errors.Is.
var ErrEgressDenied = errors.New("marketplace: egress denied")

// egressDomains is the egress whitelist configured by SetEgressDomains. Each
// call replaces the slice wholesale (it is never mutated in place); readers
// hold egressMu for the read.
var (
	egressMu      sync.RWMutex
	egressDomains []string
)

// Non-public ranges that netip does not classify on its own (§3.11):
// carrier-grade NAT (RFC 6598) and benchmarking (RFC 2544).
var (
	cgnatPrefix        = netip.MustParsePrefix("100.64.0.0/10")
	benchmarkingPrefix = netip.MustParsePrefix("198.18.0.0/15")
)

// SetEgressDomains replaces the egress whitelist. Entries are lowercased and
// stripped of surrounding spaces, ports, brackets and trailing dots; empties
// are dropped and duplicates removed. An empty result denies everything
// (fail-closed).
func SetEgressDomains(domains []string) {
	normalized := make([]string, 0, len(domains))
	for _, d := range domains {
		d = normalizeEgressHost(d)
		if d == "" || slices.Contains(normalized, d) {
			continue
		}
		normalized = append(normalized, d)
	}
	egressMu.Lock()
	egressDomains = normalized
	egressMu.Unlock()
}

// CheckEgressHost validates host against the egress whitelist (§3.11: 出网只
// 允许白名单域名). An empty whitelist denies everything. Matching is exact or
// on a dot boundary: whitelisting example.com allows api.example.com but
// never evil-example.com. A port suffix is stripped first. IP literals are
// not domains and are always refused here; ValidateEgressIP judges IPs.
func CheckEgressHost(host string) error {
	host = normalizeEgressHost(host)
	if host == "" {
		return fmt.Errorf("%w: empty host", ErrEgressDenied)
	}
	if net.ParseIP(host) != nil {
		return fmt.Errorf("%w: ip literal %q is not a whitelisted domain", ErrEgressDenied, host)
	}
	egressMu.RLock()
	domains := egressDomains
	egressMu.RUnlock()
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return nil
		}
	}
	return fmt.Errorf("%w: host %q not in whitelist", ErrEgressDenied, host)
}

// normalizeEgressHost lowercases and trims space, strips an optional port and
// the brackets of an IPv6 literal, and drops the trailing dot of FQDN form.
func normalizeEgressHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return strings.TrimSuffix(host, ".")
}

// ValidateEgressIP refuses every non-public destination (§3.11): unspecified,
// loopback, private (RFC 1918 + fc00::/7), link-local unicast and multicast,
// multicast, CGNAT (100.64.0.0/10) and benchmarking (198.18.0.0/15) ranges.
// IPv4-mapped IPv6 addresses are unmapped first, so ::ffff:127.0.0.1 is
// judged as 127.0.0.1.
func ValidateEgressIP(ip net.IP) error {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return fmt.Errorf("%w: invalid ip %v", ErrEgressDenied, ip)
	}
	// Unmap first: an IPv4-mapped IPv6 address (::ffff:127.0.0.1) is judged
	// as the IPv4 address it embeds (127.0.0.1).
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() ||
		cgnatPrefix.Contains(addr) || benchmarkingPrefix.Contains(addr) {
		return fmt.Errorf("%w: non-public ip %s", ErrEgressDenied, addr)
	}
	return nil
}

// EgressHTTPClient returns an http.Client whose every dial passes the egress
// guard: the target host must be whitelisted and every IP it resolves to must
// be public (see dialChecked). Timeouts: dial 10s, TLS handshake 10s,
// response header 30s. No proxy is honored — a proxy would receive the raw
// target host and resolve it upstream, bypassing ValidateEgressIP entirely.
func EgressHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           egressDial,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// egressDial is the transport DialContext behind EgressHTTPClient.
func egressDial(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return dialChecked(ctx, network, addr,
		func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		},
		dialer.DialContext,
	)
}

// dialChecked resolves host once through lookup, refuses unless EVERY resolved
// IP passes ValidateEgressIP — fail-closed, so one good IP can never cover a
// bad one — then dials the validated IPs directly. No second resolution ever
// happens between validation and dialing; that TOCTOU gap is where DNS
// rebinding attacks live, so the dialed IP is always a validated one. The
// transport still runs TLS against the request URL's host name, so
// certificate verification is unaffected.
func dialChecked(ctx context.Context, network, addr string,
	lookup func(context.Context, string) ([]net.IP, error),
	dial func(context.Context, string, string) (net.Conn, error),
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed address %q", ErrEgressDenied, addr)
	}
	if err := CheckEgressHost(host); err != nil {
		return nil, err
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("marketplace: resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: %q resolved to zero ips", ErrEgressDenied, host)
	}
	for _, ip := range ips {
		if err := ValidateEgressIP(ip); err != nil {
			return nil, fmt.Errorf("marketplace: %q: %w", host, err)
		}
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("marketplace: dial %q: %w", host, lastErr)
}
