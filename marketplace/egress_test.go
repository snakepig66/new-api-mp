package marketplace

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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
	origDomains := slices.Clone(egressDomains)
	origSellerHosts := slices.Clone(egressSellerHosts)
	origPatterns := slices.Clone(egressPatterns)
	egressMu.RUnlock()
	t.Cleanup(func() {
		egressMu.Lock()
		egressDomains = origDomains
		egressSellerHosts = origSellerHosts
		egressPatterns = origPatterns
		egressMu.Unlock()
	})
}

func TestCheckEgressHost(t *testing.T) {
	preserveEgressDomains(t)
	SetEgressSellerHosts(nil)

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

// TestCheckEgressHostSellerExactMatch locks the two-level whitelist: seller
// custom-channel hosts match exactly, platform-configured domains keep the
// dot-boundary suffix rule.
func TestCheckEgressHostSellerExactMatch(t *testing.T) {
	preserveEgressDomains(t)

	SetEgressDomains(nil)
	SetEgressSellerHosts([]string{" Seller-Upstream.Example.com "})
	assert.NoError(t, CheckEgressHost("seller-upstream.example.com"))
	assert.ErrorIs(t, CheckEgressHost("api.seller-upstream.example.com"), ErrEgressDenied,
		"a seller host must never allow its subdomains")
	assert.ErrorIs(t, CheckEgressHost("seller-upstream.example.com.evil.com"), ErrEgressDenied)

	// Official/platform domains still allow documented subdomains.
	SetEgressDomains([]string{"deepseek.com"})
	assert.NoError(t, CheckEgressHost("api.deepseek.com"))
	assert.NoError(t, CheckEgressHost("a.b.deepseek.com"))
	assert.ErrorIs(t, CheckEgressHost("evil-deepseek.com"), ErrEgressDenied)
}

// TestSellerHostJoinsRelayWhitelistOnlyWhenListingLive drives the registry
// refresh against a real pool row: a draft custom channel stays out of the
// relay whitelist, canary/live admits its exact host, and a pause removes it.
func TestSellerHostJoinsRelayWhitelistOnlyWhenListingLive(t *testing.T) {
	preserveEgressDomains(t)
	f := newSellerAPIFixture(t)
	resetVendorRegistryForTest()
	t.Cleanup(resetVendorRegistryForTest)

	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		APIKey:          uniqueTestKey("sk-egress-whitelist"),
		CustomBaseURL:   "https://seller-upstream.example.com",
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)

	refreshVendorRegistry(true)
	assert.ErrorIs(t, CheckEgressHost("seller-upstream.example.com"), ErrEgressDenied,
		"a draft listing must not admit the seller host")

	for _, status := range []string{ListingCanary, ListingLive} {
		require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).
			Update("status", status).Error)
		refreshVendorRegistry(true)
		assert.NoError(t, CheckEgressHost("seller-upstream.example.com"), status)
		assert.ErrorIs(t, CheckEgressHost("api.seller-upstream.example.com"), ErrEgressDenied,
			"the admitted seller host is exact, not a suffix")
	}

	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).
		Update("status", ListingPaused).Error)
	refreshVendorRegistry(true)
	assert.ErrorIs(t, CheckEgressHost("seller-upstream.example.com"), ErrEgressDenied,
		"a paused listing removes the host from the relay whitelist")
}

func TestCheckEgressHostSellerExactMatchRejectsIPLiteral(t *testing.T) {
	preserveEgressDomains(t)
	SetEgressDomains(nil)
	SetEgressSellerHosts([]string{"127.0.0.1"})
	assert.ErrorIs(t, CheckEgressHost("127.0.0.1"), ErrEgressDenied,
		"an IP literal is never a whitelisted domain, even in the seller list")
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

		// L2 additions: ranges netip does not classify by itself.
		"0.1.2.3", "0.255.255.255", // 0.0.0.0/8 "this network"
		"192.0.0.1",          // 192.0.0.0/24 IETF protocol assignments
		"192.0.2.1",          // 192.0.2.0/24 TEST-NET-1 documentation
		"192.0.2.255",        // TEST-NET-1 upper edge
		"240.1.1.1",          // 240.0.0.0/4 reserved
		"255.255.255.255",    // limited broadcast, inside 240.0.0.0/4
		"64:ff9b::8.8.8.8",   // NAT64 64:ff9b::/96 embeds a public IPv4
		"64:ff9b:1::8.8.8.8", // local-use NAT64 RFC 8215
		"fec0::1",            // deprecated IPv6 site-local
		"::8.8.8.8",          // deprecated IPv4-compatible form
		"2002::1",            // 6to4 2002::/16
		"2002:c0a8:0101::",   // 6to4 embedding 192.168.1.1

		// IPv4-mapped forms must be judged as the embedded IPv4 address.
		"::ffff:192.0.2.1",
		"::ffff:240.1.1.1",
		"::ffff:0.0.0.0",
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

// redirectTransport answers every request with a 301 to a different host and
// counts the requests it served, so a test can prove that no redirect was
// followed without any network access.
type redirectTransport struct{ hits *atomic.Int32 }

func (rt redirectTransport) RoundTrip(*http.Request) (*http.Response, error) {
	rt.hits.Add(1)
	return &http.Response{
		StatusCode: http.StatusMovedPermanently,
		Header:     http.Header{"Location": []string{"https://elsewhere.example.com/x"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

func TestEgressClientsDoNotFollowRedirects(t *testing.T) {
	for name, client := range map[string]*http.Client{
		"egress client": EgressHTTPClient(),
		"verify client": EgressVerifyHTTPClient(),
	} {
		t.Run(name, func(t *testing.T) {
			var hits atomic.Int32
			client.Transport = redirectTransport{hits: &hits}
			resp, err := client.Get("https://api.deepseek.com/probe")
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })
			assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode,
				"the 3xx must be returned to the caller, not followed")
			assert.Equal(t, int32(1), hits.Load(), "exactly one request — no redirect follow")
		})
	}
}

func TestEgressVerifyDialSkipsWhitelistButValidatesIPs(t *testing.T) {
	preserveEgressDomains(t)
	SetEgressDomains(nil) // fail-closed whitelist: nothing is a member

	// The verify client reaches hosts that are not whitelisted (§3: 审核通过
	// 前只有验证客户端可以访问它), but every resolved IP must still be public.
	lookup := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	}
	var dialed []string
	conn, err := dialCheckedPolicy(context.Background(), "tcp", "seller-submitted.example:443",
		lookup, pipeDialer(&dialed, nil), false)
	require.NoError(t, err)
	require.NotNil(t, conn)
	t.Cleanup(func() { _ = conn.Close() })
	assert.Equal(t, []string{"8.8.8.8:443"}, dialed)

	// A non-public IP is refused even without the whitelist membership check.
	badLookup := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.0.0.5")}, nil
	}
	_, err = dialCheckedPolicy(context.Background(), "tcp", "seller-submitted.example:443",
		badLookup, pipeDialer(&dialed, nil), false)
	assert.ErrorIs(t, err, ErrEgressDenied)
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
