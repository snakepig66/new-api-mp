package marketplace

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
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
// hold egressMu for the read. egressPatterns holds vendor-declared strict
// subdomain patterns (Bailian workspace domains); a host passes when it
// matches an exact/suffix entry OR one anchored pattern.
var (
	egressMu       sync.RWMutex
	egressDomains  []string
	egressPatterns []*regexp.Regexp
)

// SetEgressPatterns replaces the strict host-pattern whitelist. Patterns are
// compiled with both anchors added, so a pattern can never match a substring;
// an invalid pattern is an error so Init fails instead of running open.
func SetEgressPatterns(patterns []string) error {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		re, err := regexp.Compile("^(?:" + p + ")$")
		if err != nil {
			return fmt.Errorf("marketplace: invalid egress host pattern %q: %w", p, err)
		}
		compiled = append(compiled, re)
	}
	egressMu.Lock()
	egressPatterns = compiled
	egressMu.Unlock()
	return nil
}

// Non-public ranges that netip.Addr's classification helpers do not cover on
// their own (§3.11, review L2). Checked with netip.Prefix.Contains, which is
// family-aware and therefore safe to run against both IPv4 and IPv6:
//
//   - 0.0.0.0/8             "this network" (only 0.0.0.0 itself is unspecified)
//   - 100.64.0.0/10         carrier-grade NAT (RFC 6598)
//   - 192.0.0.0/24          IETF protocol assignments
//   - 192.0.2.0/24          TEST-NET-1 documentation range
//   - 198.18.0.0/15         benchmarking (RFC 2544)
//   - 240.0.0.0/4           reserved + limited broadcast (includes 255.255.255.255)
//   - 64:ff9b::/96          NAT64 well-known prefix
//   - 2002::/16             6to4, which embeds an arbitrary IPv4 address
//
// 2002::/16 is denied wholesale: a 6to4 address can smuggle a private IPv4
// destination past every IPv4 check.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("2002::/16"),
}

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
	patterns := egressPatterns
	egressMu.RUnlock()
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return nil
		}
	}
	for _, re := range patterns {
		if re.MatchString(host) {
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
// multicast, plus every explicitly listed non-public prefix in
// nonPublicPrefixes (CGNAT, benchmarking, "this network", IETF protocol
// assignments, TEST-NET-1, reserved/broadcast, NAT64, 6to4).
//
// IPv4-mapped IPv6 addresses are unmapped first, so ::ffff:127.0.0.1 is
// judged as 127.0.0.1 and ::ffff:192.0.2.1 as 192.0.2.1.
func ValidateEgressIP(ip net.IP) error {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return fmt.Errorf("%w: invalid ip %v", ErrEgressDenied, ip)
	}
	// Unmap first: an IPv4-mapped IPv6 address (::ffff:127.0.0.1) is judged
	// as the IPv4 address it embeds (127.0.0.1).
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() {
		return fmt.Errorf("%w: non-public ip %s", ErrEgressDenied, addr)
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return fmt.Errorf("%w: non-public ip %s", ErrEgressDenied, addr)
		}
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
