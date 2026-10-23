package marketplace

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// preserveEgressDomains snapshots the package-level egress whitelist and
// restores it after the test. Tests in this file mutate that global, so they
// must not call t.Parallel().
func preserveEgressDomains(t *testing.T) {
	t.Helper()
	egressMu.RLock()
	orig := egressDomains
	egressMu.RUnlock()
	t.Cleanup(func() {
		egressMu.Lock()
		egressDomains = orig
		egressMu.Unlock()
	})
}

func TestCheckEgressHost(t *testing.T) {
	preserveEgressDomains(t)

	cases := []struct {
		name    string
		domains []string
		host    string
		allow   bool
	}{
		{"empty whitelist denies everything", nil, "api.deepseek.com", false},
		{"exact match", []string{"api.deepseek.com"}, "api.deepseek.com", true},
		{"subdomain suffix match", []string{"deepseek.com"}, "api.deepseek.com", true},
		{"deep subdomain suffix match", []string{"deepseek.com"}, "a.b.deepseek.com", true},
		{"parent not implied by child entry", []string{"api.deepseek.com"}, "deepseek.com", false},
		{"lookalike domain denied", []string{"deepseek.com"}, "evil-deepseek.com", false},
		{"whitelist entry as suffix of foreign domain denied", []string{"deepseek.com"}, "deepseek.com.evil.com", false},
		{"port stripped before matching", []string{"deepseek.com"}, "api.deepseek.com:443", true},
		{"host case normalized", []string{"deepseek.com"}, "API.DeepSeek.COM", true},
		{"fqdn trailing dot normalized", []string{"deepseek.com"}, "api.deepseek.com.", true},
		{"ipv4 literal denied even when listed", []string{"8.8.8.8"}, "8.8.8.8", false},
		{"ipv4 literal with port denied", []string{"deepseek.com"}, "127.0.0.1:8080", false},
		{"ipv6 literal denied", []string{"deepseek.com"}, "::1", false},
		{"bracketed ipv6 literal denied", []string{"deepseek.com"}, "[::1]:443", false},
		{"empty host denied", []string{"deepseek.com"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			SetEgressDomains(tc.domains)
			err := CheckEgressHost(tc.host)
			if tc.allow {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, ErrEgressDenied)
			}
		})
	}
}

func TestValidateEgressIP(t *testing.T) {
	// Every non-public range from §3.11 must be refused.
	denied := []string{
		"127.0.0.1", "127.1.2.3", "::1", // loopback
		"10.0.0.1", "172.16.5.5", "172.31.255.255", "192.168.0.1", // RFC 1918
		"fc00::1", "fd12::1", // RFC 4193 unique-local
		"169.254.0.1", "fe80::1", // link-local unicast
		"0.0.0.0", "::", // unspecified
		"100.64.0.1", "100.127.255.255", // CGNAT 100.64.0.0/10
		"198.18.0.1", "198.19.255.255", // benchmarking 198.18.0.0/15
		"224.0.0.1", "224.0.0.2", "ff02::1", // multicast / link-local multicast
		"::ffff:127.0.0.1", // IPv4-mapped loopback: must be judged as 127.0.0.1
	}
	for _, s := range denied {
		t.Run("deny "+s, func(t *testing.T) {
			ip := net.ParseIP(s)
			require.NotNil(t, ip, "test fixture must parse")
			assert.ErrorIs(t, ValidateEgressIP(ip), ErrEgressDenied, "ip %s must be refused", s)
		})
	}

	allowed := []string{"8.8.8.8", "1.1.1.1", "2400:3200::1", "2606:4700:4700::1111"}
	for _, s := range allowed {
		t.Run("allow "+s, func(t *testing.T) {
			ip := net.ParseIP(s)
			require.NotNil(t, ip, "test fixture must parse")
			assert.NoError(t, ValidateEgressIP(ip), "public ip %s must pass", s)
		})
	}

	t.Run("deny nil ip", func(t *testing.T) {
		assert.ErrorIs(t, ValidateEgressIP(nil), ErrEgressDenied)
	})
}

func TestSetEgressDomains(t *testing.T) {
	preserveEgressDomains(t)

	SetEgressDomains([]string{" API.DeepSeek.com ", "api.deepseek.com:443", "", "   ", "deepseek.com.", "deepseek.com"})
	assert.NoError(t, CheckEgressHost("api.deepseek.com"),
		"case/space/port variants must normalize onto the same entry")
	assert.NoError(t, CheckEgressHost("API.DEEPSEEK.COM"))
	assert.NoError(t, CheckEgressHost("www.deepseek.com"))

	// Resetting to nil returns the guard to fail-closed.
	SetEgressDomains(nil)
	assert.ErrorIs(t, CheckEgressHost("api.deepseek.com"), ErrEgressDenied)
	assert.ErrorIs(t, CheckEgressHost("www.deepseek.com"), ErrEgressDenied)
}

func TestEgressHTTPClientRejectsLoopbackURL(t *testing.T) {
	preserveEgressDomains(t)
	SetEgressDomains([]string{"deepseek.com"})

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(srv.Close)

	// srv.URL is http://127.0.0.1:<port>/ — an IP literal, refused before any
	// connection attempt (SSRF, §3.11).
	_, err := EgressHTTPClient().Get(srv.URL)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEgressDenied)
	assert.Equal(t, int32(0), hits.Load(), "no request may reach the server")
}

func TestEgressHTTPClientRejectsNonWhitelistedDomain(t *testing.T) {
	preserveEgressDomains(t)
	SetEgressDomains([]string{"api.deepseek.com"})

	// The whitelist check runs before DNS, so this is deterministic offline.
	_, err := EgressHTTPClient().Get("http://evil-deepseek.com/")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEgressDenied)
}

func TestEgressHTTPClientRejectsWhitelistedNameResolvingToPrivateIP(t *testing.T) {
	preserveEgressDomains(t)
	SetEgressDomains([]string{"localhost"})

	ips, err := net.DefaultResolver.LookupIP(context.Background(), "ip", "localhost")
	if err != nil || len(ips) == 0 {
		t.Skipf("localhost does not resolve on this host: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	require.NoError(t, err)

	// localhost is whitelisted but resolves to loopback: the per-IP check
	// must still refuse the dial (DNS-based SSRF, §3.11).
	_, err = EgressHTTPClient().Get("http://localhost:" + port + "/")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEgressDenied)
}

func TestEgressHTTPClientTimeouts(t *testing.T) {
	client := EgressHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "transport must be *http.Transport")
	assert.Equal(t, 10*time.Second, transport.TLSHandshakeTimeout)
	assert.Equal(t, 30*time.Second, transport.ResponseHeaderTimeout)
	assert.NotNil(t, transport.DialContext, "every dial must go through the egress guard")
	assert.Nil(t, transport.Proxy, "proxies stay disabled: a proxy would bypass IP validation")
}

// pipeDialer returns a dial func that records every address it is asked to
// dial into dialed and answers with an in-memory pipe connection (or failWith
// when set). Tests only inspect the dialed addresses; no I/O happens on the
// connection.
func pipeDialer(dialed *[]string, failWith error) func(context.Context, string, string) (net.Conn, error) {
	return func(_ context.Context, _, address string) (net.Conn, error) {
		*dialed = append(*dialed, address)
		if failWith != nil {
			return nil, failWith
		}
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	}
}

func TestEgressDialCheckedRejectsAnyBadIP(t *testing.T) {
	preserveEgressDomains(t)
	SetEgressDomains([]string{"deepseek.com"})

	lookup := func(context.Context, string) ([]net.IP, error) {
		// One good IP must never cover a bad one (fail-closed, §3.11).
		return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("127.0.0.1")}, nil
	}
	var dialed []string
	conn, err := dialChecked(context.Background(), "tcp", "api.deepseek.com:443",
		lookup, pipeDialer(&dialed, nil))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEgressDenied)
	assert.Nil(t, conn)
	assert.Empty(t, dialed, "no dial may happen once validation fails")
}

func TestEgressDialCheckedDialsOnlyValidatedIPs(t *testing.T) {
	preserveEgressDomains(t)
	SetEgressDomains([]string{"deepseek.com"})

	lookupCalls := 0
	lookup := func(context.Context, string) ([]net.IP, error) {
		lookupCalls++
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	}
	var dialed []string
	conn, err := dialChecked(context.Background(), "tcp", "api.deepseek.com:443",
		lookup, pipeDialer(&dialed, nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	assert.Equal(t, 1, lookupCalls, "the host is resolved exactly once per dial")
	assert.Equal(t, []string{"8.8.8.8:443"}, dialed,
		"the dialed IP must be the validated one — no re-resolution between validation and dialing (DNS rebinding defense, §3.11)")
}

func TestEgressDialCheckedRevalidatesEachAttempt(t *testing.T) {
	preserveEgressDomains(t)
	SetEgressDomains([]string{"deepseek.com"})

	// DNS rebinding script: the same host answers differently per lookup.
	answers := [][]net.IP{
		{net.ParseIP("127.0.0.1")},
		{net.ParseIP("8.8.8.8")},
	}
	lookupCalls := 0
	lookup := func(context.Context, string) ([]net.IP, error) {
		ips := answers[lookupCalls]
		lookupCalls++
		return ips, nil
	}
	var dialed []string
	dial := pipeDialer(&dialed, nil)

	conn, err := dialChecked(context.Background(), "tcp", "api.deepseek.com:443", lookup, dial)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEgressDenied, "the first answer is loopback: the attempt must be refused")
	assert.Nil(t, conn)

	conn, err = dialChecked(context.Background(), "tcp", "api.deepseek.com:443", lookup, dial)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	assert.Equal(t, 2, lookupCalls, "every dial resolves and validates independently")
	assert.Equal(t, []string{"8.8.8.8:443"}, dialed, "only the validated second answer is ever dialed")
}

func TestEgressDialCheckedRejectsZeroIPs(t *testing.T) {
	preserveEgressDomains(t)
	SetEgressDomains([]string{"deepseek.com"})

	lookup := func(context.Context, string) ([]net.IP, error) { return nil, nil }
	var dialed []string
	conn, err := dialChecked(context.Background(), "tcp", "api.deepseek.com:443",
		lookup, pipeDialer(&dialed, nil))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEgressDenied, "zero resolved ips is a refusal, not a dial")
	assert.Nil(t, conn)
	assert.Empty(t, dialed)
}

func TestEgressDialCheckedPropagatesResolveFailure(t *testing.T) {
	preserveEgressDomains(t)
	SetEgressDomains([]string{"deepseek.com"})

	resolveErr := errors.New("no such host")
	lookup := func(context.Context, string) ([]net.IP, error) { return nil, resolveErr }
	var dialed []string
	_, err := dialChecked(context.Background(), "tcp", "api.deepseek.com:443",
		lookup, pipeDialer(&dialed, nil))
	require.Error(t, err)
	assert.ErrorIs(t, err, resolveErr)
	assert.NotErrorIs(t, err, ErrEgressDenied, "a resolver failure is not a policy refusal")
	assert.Empty(t, dialed)
}

func TestEgressDialCheckedPropagatesDialFailure(t *testing.T) {
	preserveEgressDomains(t)
	SetEgressDomains([]string{"deepseek.com"})

	dialErr := errors.New("connection refused")
	lookup := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	}
	var dialed []string
	conn, err := dialChecked(context.Background(), "tcp", "api.deepseek.com:443",
		lookup, pipeDialer(&dialed, dialErr))
	require.Error(t, err)
	assert.ErrorIs(t, err, dialErr)
	assert.NotErrorIs(t, err, ErrEgressDenied, "a transport failure is not a policy refusal")
	assert.Nil(t, conn)
	assert.Equal(t, []string{"8.8.8.8:443"}, dialed)
}
