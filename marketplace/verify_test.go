package marketplace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// verifyTestKey is the plaintext seller key used across this file. Every
// error and probe-detail assertion also checks it never leaks (§2.3).
const verifyTestKey = "sk-verify-test-key-abcde"

// vendorHandler mocks a whitelisted vendor's three verification endpoints
// (§3.5). wantAuth, when set, makes the handler answer 401 unless the
// request carries exactly that Authorization header — the all-green path
// thereby also proves the key is sent as a bearer token.
type vendorHandler struct {
	wantAuth      string
	modelsStatus  int
	modelsBody    string
	balanceStatus int
	balanceBody   string
	chatStatus    int
	chatBody      string

	balanceCalls atomic.Int32
	chatCalls    atomic.Int32
}

func (h *vendorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.wantAuth != "" && r.Header.Get("Authorization") != h.wantAuth {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/models":
		w.WriteHeader(h.modelsStatus)
		_, _ = w.Write([]byte(h.modelsBody))
	case "/user/balance":
		h.balanceCalls.Add(1)
		w.WriteHeader(h.balanceStatus)
		_, _ = w.Write([]byte(h.balanceBody))
	case "/chat/completions":
		h.chatCalls.Add(1)
		w.WriteHeader(h.chatStatus)
		_, _ = w.Write([]byte(h.chatBody))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// newVendorServer starts the mock vendor and returns its server plus a
// Vendor whose BaseURL points at it (VerifyListingKey takes the Vendor
// explicitly, so no URL rewriting is needed here).
func newVendorServer(t *testing.T, h http.Handler) (*httptest.Server, Vendor) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	vendor := Vendor{
		ID:          "testvendor",
		DisplayName: "Test Vendor",
		BaseURL:     srv.URL,
		Models:      []string{"deepseek-flash"},
	}
	return srv, vendor
}

// allGreenHandler answers every endpoint the §3.5 wizard check needs.
func allGreenHandler() *vendorHandler {
	return &vendorHandler{
		wantAuth:      "Bearer " + verifyTestKey,
		modelsStatus:  http.StatusOK,
		modelsBody:    `{"object":"list","data":[{"id":"deepseek-flash"},{"id":"deepseek-v4-pro"}]}`,
		balanceStatus: http.StatusOK,
		balanceBody:   `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"12.34"}]}`,
		chatStatus:    http.StatusOK,
		chatBody:      `{"choices":[{"message":{"content":"pong"}}],"usage":{"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":2,"total_tokens":10}}`,
	}
}

func TestVerifyListingKeyAllGreen(t *testing.T) {
	h := allGreenHandler()
	srv, vendor := newVendorServer(t, h)

	res := VerifyListingKey(context.Background(), srv.Client(), vendor, verifyTestKey)

	assert.True(t, res.OK)
	assert.True(t, res.KeyValid)
	require.NotNil(t, res.BalanceMicro)
	assert.Equal(t, int64(12_340_000), *res.BalanceMicro, "CNY 12.34 yuan = 12,340,000 微元, parsed exactly (no float)")
	assert.Equal(t, []string{"deepseek-flash", "deepseek-v4-pro"}, res.Models)
	assert.GreaterOrEqual(t, res.LatencyMs, int64(0))
	assert.True(t, res.PromptCacheSupport, "usage carries prompt_cache_hit_tokens (§3.5 auto-probe)")
	assert.Empty(t, res.FailReason)
	assert.Equal(t, int32(1), h.balanceCalls.Load())
	assert.Equal(t, int32(1), h.chatCalls.Load())
}

func TestVerifyListingKeyInvalidKey(t *testing.T) {
	h := allGreenHandler()
	h.modelsStatus = http.StatusUnauthorized
	h.modelsBody = `{"error":{"message":"bad key body must not be echoed"}}`
	srv, vendor := newVendorServer(t, h)

	res := VerifyListingKey(context.Background(), srv.Client(), vendor, verifyTestKey)

	assert.False(t, res.OK)
	assert.False(t, res.KeyValid)
	require.NotEmpty(t, res.FailReason)
	assert.NotContains(t, res.FailReason, verifyTestKey, "§2.3: no key in failure text")
	assert.NotContains(t, res.FailReason, "bad key body", "§3.11: upstream body is never echoed")
	assert.Zero(t, h.balanceCalls.Load(), "an invalid key short-circuits the balance lookup")
	assert.Zero(t, h.chatCalls.Load(), "an invalid key short-circuits the paid cache probe")
}

func TestVerifyListingKeyBalanceMissing(t *testing.T) {
	h := allGreenHandler()
	h.balanceStatus = http.StatusNotFound
	h.balanceBody = `{"error":"no such endpoint"}`
	srv, vendor := newVendorServer(t, h)

	res := VerifyListingKey(context.Background(), srv.Client(), vendor, verifyTestKey)

	assert.Nil(t, res.BalanceMicro, "404 on /user/balance is not a verification failure")
	assert.True(t, res.OK, "an unreadable balance never gates OK (§3.5)")
	assert.True(t, res.KeyValid)
}

func TestVerifyListingKeyPromptCacheUnsupported(t *testing.T) {
	run := func(t *testing.T, chatStatus int, chatBody string) VerifyResult {
		h := allGreenHandler()
		h.chatStatus = chatStatus
		h.chatBody = chatBody
		srv, vendor := newVendorServer(t, h)
		return VerifyListingKey(context.Background(), srv.Client(), vendor, verifyTestKey)
	}

	t.Run("usage lacks cache fields", func(t *testing.T) {
		res := run(t, http.StatusOK, `{"usage":{"prompt_tokens":5,"total_tokens":6}}`)
		assert.False(t, res.PromptCacheSupport)
		assert.True(t, res.OK, "cache support is informational and never gates OK")
	})
	t.Run("usage missing entirely", func(t *testing.T) {
		res := run(t, http.StatusOK, `{"choices":[]}`)
		assert.False(t, res.PromptCacheSupport)
		assert.True(t, res.OK)
	})
	t.Run("probe request fails", func(t *testing.T) {
		res := run(t, http.StatusInternalServerError, `{"error":"boom"}`)
		assert.False(t, res.PromptCacheSupport)
		assert.True(t, res.OK)
	})
}

func TestVerifyListingKeyNoModels(t *testing.T) {
	t.Run("empty model list", func(t *testing.T) {
		h := allGreenHandler()
		h.modelsBody = `{"object":"list","data":[]}`
		srv, vendor := newVendorServer(t, h)
		res := VerifyListingKey(context.Background(), srv.Client(), vendor, verifyTestKey)
		assert.True(t, res.KeyValid, "a 200 means the key itself works")
		assert.False(t, res.OK, "OK requires at least one model")
		assert.NotEmpty(t, res.FailReason)
		assert.Zero(t, h.chatCalls.Load(), "zero models cannot turn OK, so the paid probe is skipped")
	})
	t.Run("malformed json", func(t *testing.T) {
		h := allGreenHandler()
		h.modelsBody = `not json at all`
		srv, vendor := newVendorServer(t, h)
		res := VerifyListingKey(context.Background(), srv.Client(), vendor, verifyTestKey)
		assert.True(t, res.KeyValid)
		assert.False(t, res.OK)
		assert.NotEmpty(t, res.FailReason)
		assert.NotContains(t, res.FailReason, verifyTestKey)
	})
}

// TestVerifyYuanStringToMicro pins the exact decimal → 微元 conversion: no
// float anywhere, sub-micro dust truncates, garbage yields nil without a
// panic.
func TestVerifyYuanStringToMicro(t *testing.T) {
	microPtr := func(v int64) *int64 { return &v }
	cases := []struct {
		in   string
		want *int64
	}{
		{"12.34", microPtr(12_340_000)},
		{"0", microPtr(0)},
		{"0.000001", microPtr(1)},
		{"100", microPtr(100_000_000)},
		{"-1.5", microPtr(-1_500_000)},
		{"12.34567890", microPtr(12_345_678)}, // sub-micro fraction truncates toward zero
		{"abc", nil},
		{"", nil},
		{"   ", nil},
		{"99999999999999999999999999", nil}, // int64 overflow → nil, not a wrap
	}
	for _, tc := range cases {
		got := yuanStringToMicro(tc.in)
		if tc.want == nil {
			assert.Nil(t, got, "input %q", tc.in)
		} else if assert.NotNil(t, got, "input %q", tc.in) {
			assert.Equal(t, *tc.want, *got, "input %q", tc.in)
		}
	}
}

func TestVerifyLimiterConcurrency(t *testing.T) {
	limiter := NewVerifyLimiter(2, 6000) // ample rate tokens; only the semaphore gates
	ctx := context.Background()
	require.NoError(t, limiter.Acquire(ctx))
	require.NoError(t, limiter.Acquire(ctx))

	acquired := make(chan struct{})
	go func() {
		if err := limiter.Acquire(ctx); err == nil {
			close(acquired)
		}
	}()

	// Both slots are held: the third Acquire must stay blocked.
	select {
	case <-acquired:
		t.Fatal("third Acquire succeeded while both slots were held")
	case <-time.After(100 * time.Millisecond):
	}

	limiter.Release()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("third Acquire did not proceed after Release")
	}
	limiter.Release()
	limiter.Release()
}

func TestVerifyLimiterContextCancel(t *testing.T) {
	limiter := NewVerifyLimiter(1, 6000)
	require.NoError(t, limiter.Acquire(context.Background()))
	defer limiter.Release()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- limiter.Acquire(ctx) }()
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled, "a cancelled Acquire returns immediately")
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled Acquire did not return")
	}
}

func TestVerifyLimiterRateBlocks(t *testing.T) {
	limiter := NewVerifyLimiter(1, 1) // one token per minute, bucket starts full
	require.NoError(t, limiter.Acquire(context.Background()), "the first token is available instantly")
	limiter.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := limiter.Acquire(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "an exhausted bucket blocks until refill")
}

// rewriteTransport points every request at the httptest server. VerifyListing
// resolves the vendor internally from the whitelist (BaseURL
// api.deepseek.com), so the orchestration tests redirect at the transport
// layer instead of touching the whitelist.
type rewriteTransport struct{ target *url.URL }

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

// newVerifyListingFixture builds the DB schema, a draft listing holding
// verifyTestKey, a mock vendor server and a client that redirects the
// whitelisted vendor's BaseURL to it.
func newVerifyListingFixture(t *testing.T, h http.Handler) (*gorm.DB, Listing, *http.Client) {
	t.Helper()
	db := newListingTestDB(t)
	listing := createTestListing(t, db, 10023, verifyTestKey)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return db, listing, &http.Client{Transport: rewriteTransport{target: target}}
}

func listVerifyProbes(t *testing.T, db *gorm.DB, listingID int64) []Probe {
	t.Helper()
	var probes []Probe
	require.NoError(t, db.Where("listing_id = ?", listingID).Order("id ASC").Find(&probes).Error)
	return probes
}

func TestVerifyListingSuccess(t *testing.T) {
	db, listing, client := newVerifyListingFixture(t, allGreenHandler())
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err)
	assert.True(t, res.OK)
	assert.True(t, res.PromptCacheSupport)

	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingCanary, row.Status, "a passed verification moves draft → canary (§3.5)")

	probes := listVerifyProbes(t, db, listing.ID)
	require.Len(t, probes, 1)
	assert.Equal(t, ProbeTypeVerify, probes[0].Type)
	assert.True(t, probes[0].OK)
	assert.GreaterOrEqual(t, probes[0].LatencyMs, int64(0))
	assert.NotContains(t, probes[0].Detail, verifyTestKey, "§2.3: probe detail carries no key")

	var detail map[string]any
	require.NoError(t, common.Unmarshal([]byte(probes[0].Detail), &detail))
	assert.Equal(t, true, detail["key_valid"])
	assert.Equal(t, float64(2), detail["models"])
	assert.Equal(t, true, detail["has_balance"])
	assert.Equal(t, true, detail["prompt_cache"])
	assert.Equal(t, "", detail["fail_reason"])

	// A canary listing cannot be re-verified through this entry point.
	_, err = VerifyListing(context.Background(), db, client, limiter, listing.ID)
	assert.ErrorIs(t, err, ErrInvalidTransition)
}

func TestVerifyListingFailureRollsBackToDraft(t *testing.T) {
	h := allGreenHandler()
	h.modelsStatus = http.StatusUnauthorized
	h.modelsBody = `{"error":{"message":"invalid authentication credentials"}}`
	db, listing, client := newVerifyListingFixture(t, h)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err, "a failed verification is a result, not an error")
	assert.False(t, res.OK)
	assert.False(t, res.KeyValid)
	assert.NotEmpty(t, res.FailReason)
	assert.NotContains(t, res.FailReason, verifyTestKey)

	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingDraft, row.Status, "a failed verification rolls verifying back to draft")

	probes := listVerifyProbes(t, db, listing.ID)
	require.Len(t, probes, 1)
	assert.Equal(t, ProbeTypeVerify, probes[0].Type)
	assert.False(t, probes[0].OK)
	assert.NotContains(t, probes[0].Detail, verifyTestKey)
	var detail map[string]any
	require.NoError(t, common.Unmarshal([]byte(probes[0].Detail), &detail))
	assert.Equal(t, false, detail["key_valid"])
	assert.NotEmpty(t, detail["fail_reason"])

	// Back in draft, the seller can fix the key and verify again.
	require.NoError(t, StartVerification(db, listing.ID), "draft accepts a fresh verification attempt")
}

func TestVerifyListingRejects(t *testing.T) {
	db, listing, client := newVerifyListingFixture(t, allGreenHandler())
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	// Non-draft listings are refused before anything is probed or written.
	require.NoError(t, StartVerification(db, listing.ID))
	_, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	assert.ErrorIs(t, err, ErrInvalidTransition)
	assert.Empty(t, listVerifyProbes(t, db, listing.ID), "a refused run writes no probe")

	// Unknown listing id.
	_, err = VerifyListing(context.Background(), db, client, limiter, 999999)
	assert.ErrorIs(t, err, ErrListingNotFound)
}
