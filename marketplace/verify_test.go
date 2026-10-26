package marketplace

import (
	"context"
	"fmt"
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
		chatBody:      `{"model":"deepseek-flash","choices":[{"message":{"content":"pong"}}],"usage":{"prompt_tokens":20,"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":2,"total_tokens":30}}`,
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
		res := run(t, http.StatusOK, `{"model":"deepseek-flash","choices":[{"message":{"content":"pong"}}],"usage":{"prompt_tokens":5,"total_tokens":6}}`)
		assert.False(t, res.PromptCacheSupport)
		assert.True(t, res.OK, "cache support is informational and never gates OK")
	})
	t.Run("usage missing entirely is not callable", func(t *testing.T) {
		res := run(t, http.StatusOK, `{"model":"deepseek-flash","choices":[{"message":{"content":"pong"}}]}`)
		assert.False(t, res.PromptCacheSupport)
		assert.False(t, res.OK, "a chat completion without usage is not a valid served answer")
		assert.Contains(t, res.FailReason, "usage")
	})
	t.Run("malformed body is not callable", func(t *testing.T) {
		res := run(t, http.StatusOK, `not json at all`)
		assert.False(t, res.OK)
		assert.Contains(t, res.FailReason, "解析")
	})
	t.Run("missing model id is not callable", func(t *testing.T) {
		res := run(t, http.StatusOK, `{"choices":[{"message":{"content":"pong"}}],"usage":{"prompt_tokens":5}}`)
		assert.False(t, res.OK, "the returned model id is a prerequisite for fingerprinting")
		assert.Contains(t, res.FailReason, "model")
	})
	t.Run("probe request fails", func(t *testing.T) {
		res := run(t, http.StatusInternalServerError, `{"error":"boom"}`)
		assert.False(t, res.PromptCacheSupport)
		assert.False(t, res.OK, "a model that cannot be called is not verified")
		assert.Contains(t, res.FailReason, "上游服务异常")
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
	// The official-token cache is process-wide; a previous test's cached count
	// must never short-circuit this test's official call.
	resetFingerprintCacheForTest()
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

func TestKeyExposesModelOnlyAcceptsDateSuffixes(t *testing.T) {
	cases := []struct {
		available []string
		upstream  string
		want      bool
	}{
		{[]string{"glm-5"}, "glm-5", true},
		{[]string{"GLM-5"}, "glm-5", true},
		{[]string{"glm-5-20251001"}, "glm-5", true},
		{[]string{"glm-5-251001"}, "glm-5", true},
		{[]string{"glm-5.3"}, "glm-5", false},
		{[]string{"glm-5-flash"}, "glm-5", false},
		{[]string{"glm-5@beta"}, "glm-5", false},
		{[]string{"glm-5_latest"}, "glm-5", false},
		{[]string{"glm-5-2025-10-01"}, "glm-5", false},
		{[]string{"glm-5-2025101"}, "glm-5", false},
		{[]string{"glm-5-preview"}, "glm-5", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, keyExposesModel(tc.available, tc.upstream),
			"available=%v upstream=%q", tc.available, tc.upstream)
	}
}

// rejectingModelHandler fails the paid probe for one upstream model while
// leaving its /models listing intact.
type rejectingModelHandler struct {
	inner     http.Handler
	badModel  string
	badStatus int
}

func (h rejectingModelHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/chat/completions" {
		var body struct {
			Model string `json:"model"`
		}
		_ = common.DecodeJson(r.Body, &body)
		if body.Model == h.badModel {
			w.WriteHeader(h.badStatus)
			_, _ = w.Write([]byte(`{"error":"model not activated"}`))
			return
		}
	}
	h.inner.ServeHTTP(w, r)
}

func TestVerifyListingOnlyListsCallableModels(t *testing.T) {
	h := rejectingModelHandler{inner: allGreenHandler(), badModel: "deepseek-v4-pro", badStatus: http.StatusNotFound}
	db, listing, client := newVerifyListingFixture(t, h)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err)
	assert.True(t, res.OK, "one callable model is enough to list")
	require.Len(t, res.ModelResults, 2)
	assert.True(t, res.ModelResults[0].OK)
	assert.False(t, res.ModelResults[1].OK)
	assert.Contains(t, res.ModelResults[1].FailReason, "模型不存在")

	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingCanary, row.Status)
	assert.Equal(t, []string{"deepseek-flash"}, row.Models,
		"only models whose 1-token call succeeded stay on the listing")

	probes := listVerifyProbes(t, db, listing.ID)
	require.Len(t, probes, 1)
	var detail map[string]any
	require.NoError(t, common.Unmarshal([]byte(probes[0].Detail), &detail))
	results, ok := detail["model_results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 2)
}

// TestVerifyListingMirrorsReducedModels proves verification's model narrowing
// is mirrored: the callable model stays enabled and verified in the shadow
// table, the dropped one is disabled, and the channel lands in canary.
func TestVerifyListingMirrorsReducedModels(t *testing.T) {
	h := rejectingModelHandler{inner: allGreenHandler(), badModel: "deepseek-v4-pro", badStatus: http.StatusNotFound}
	db, listing, client := newVerifyListingFixture(t, h)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err)
	require.True(t, res.OK)

	var channel SellerChannel
	require.NoError(t, db.Where("id = ?", listing.ID).Take(&channel).Error)
	assert.Equal(t, ListingCanary, channel.Status)
	var key ChannelKey
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Take(&key).Error)
	assert.Equal(t, ChannelKeyNormal, key.Status)

	var models []SellerChannelModel
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Order("model ASC").Find(&models).Error)
	require.Len(t, models, 2, "both original models keep a historical shadow row")
	byModel := make(map[string]SellerChannelModel, len(models))
	for _, row := range models {
		byModel[row.Model] = row
	}
	assert.True(t, byModel["deepseek-flash"].Enabled)
	assert.True(t, byModel["deepseek-flash"].VerifiedOpenAIChat)
	assert.False(t, byModel["deepseek-v4-pro"].Enabled, "a dropped model is disabled, not deleted")
	assert.False(t, byModel["deepseek-v4-pro"].VerifiedOpenAIChat)
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

// verifyOfficialKey is the platform-owned DeepSeek key used by the
// fail-closed fingerprint tests.
const verifyOfficialKey = "sk-verify-platform-official"

// fingerprintVerifyHandler serves one mock upstream for both the seller key
// and the platform official key. Token counts differ by bearer token so the
// same URL exercises match, mismatch and official-failure paths, and
// officialFailModel can fail one model's official comparison only.
type fingerprintVerifyHandler struct {
	sellerKey         string
	officialKey       string
	sellerTokens      int64
	officialTokens    int64
	officialStatus    int
	officialFailModel string
}

func (h *fingerprintVerifyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/models":
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-flash"},{"id":"deepseek-v4-pro"}]}`))
	case "/user/balance":
		_, _ = w.Write([]byte(`{"balance_infos":[{"currency":"CNY","total_balance":"5.00"}]}`))
	case "/chat/completions":
		var body struct {
			Model string `json:"model"`
		}
		_ = common.DecodeJson(r.Body, &body)
		official := r.Header.Get("Authorization") == "Bearer "+h.officialKey
		if official {
			fail := h.officialStatus != 0 && h.officialStatus != http.StatusOK
			if h.officialFailModel != "" && body.Model == h.officialFailModel {
				fail = true
			}
			if fail {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"official upstream unavailable"}`))
				return
			}
		}
		tokens := h.sellerTokens
		if official {
			tokens = h.officialTokens
		}
		_, _ = fmt.Fprintf(w,
			`{"model":%q,"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":%d,"prompt_cache_hit_tokens":1,"prompt_cache_miss_tokens":1}}`,
			body.Model, tokens)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// TestVerifyListingTransportErrorFails proves a probe that never reaches the
// vendor cannot pass verification (fail-closed, not unchecked=pass).
func TestVerifyListingTransportErrorFails(t *testing.T) {
	db := newListingTestDB(t)
	listing := createTestListing(t, db, 10023, verifyTestKey)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	res, err := VerifyListing(context.Background(), db, &http.Client{Transport: failingTransport{}}, limiter, listing.ID)
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.NotEmpty(t, res.FailReason)
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingDraft, row.Status)
}

// TestVerifyListingMissingUsageFails proves an HTTP 200 chat body without
// usage is not a served model.
func TestVerifyListingMissingUsageFails(t *testing.T) {
	h := allGreenHandler()
	h.chatBody = `{"model":"deepseek-flash","choices":[{"message":{"content":"pong"}}]}`
	db, listing, client := newVerifyListingFixture(t, h)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.NotEmpty(t, res.FailReason)
	for _, mr := range res.ModelResults {
		assert.False(t, mr.OK)
	}
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingDraft, row.Status)
}

// TestVerifyListingMissingPlatformKeyFails proves official fingerprinting is
// fail-closed: once any platform key is configured, a model whose origin
// vendor has no platform key cannot be certified.
func TestVerifyListingMissingPlatformKeyFails(t *testing.T) {
	db, listing, client := newVerifyListingFixture(t, allGreenHandler())
	t.Setenv(PlatformKeysEnv, `{"zhipu":"sk-only-zhipu"}`)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err)
	assert.False(t, res.OK, "no official reference must never pass verification")
	assert.Contains(t, res.FailReason, "fingerprint")
	assert.Contains(t, res.FailReason, "deepseek")
	require.Len(t, res.Fingerprints, 2)
	for _, fr := range res.Fingerprints {
		assert.False(t, fr.Checked)
		assert.False(t, fr.Matched)
		assert.NotEmpty(t, fr.FailReason)
	}
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingDraft, row.Status)

	probes := listVerifyProbes(t, db, listing.ID)
	require.Len(t, probes, 1)
	assert.False(t, probes[0].OK)
	var detail map[string]any
	require.NoError(t, common.Unmarshal([]byte(probes[0].Detail), &detail))
	assert.Equal(t, true, detail["fingerprint_required"])
	fingerprints, ok := detail["fingerprints"].([]any)
	require.True(t, ok)
	require.Len(t, fingerprints, 2)
}

// TestVerifyListingEmptyPlatformKeysFails covers a defined-but-empty
// MP_PLATFORM_KEYS: the deployment declares official credentials but supplies
// none, so there is no official reference and verification fails closed.
func TestVerifyListingEmptyPlatformKeysFails(t *testing.T) {
	db, listing, client := newVerifyListingFixture(t, allGreenHandler())
	t.Setenv(PlatformKeysEnv, "")
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, res.FailReason, "fingerprint")
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingDraft, row.Status)
}

// TestVerifyListingFingerprintMismatchFails covers a callable model whose
// tokenizer does not match the official one: callability alone is not enough.
func TestVerifyListingFingerprintMismatchFails(t *testing.T) {
	h := &fingerprintVerifyHandler{
		sellerKey: verifyTestKey, officialKey: verifyOfficialKey,
		sellerTokens: 500, officialTokens: 100,
	}
	db, listing, client := newVerifyListingFixture(t, h)
	t.Setenv(PlatformKeysEnv, `{"deepseek":"`+verifyOfficialKey+`"}`)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err)
	assert.False(t, res.OK, "a fingerprint mismatch must not pass")
	assert.Contains(t, res.FailReason, "fingerprint")
	require.Len(t, res.Fingerprints, 2)
	for _, fr := range res.Fingerprints {
		assert.True(t, fr.Checked)
		assert.False(t, fr.Matched)
		assert.NotEmpty(t, fr.FailReason)
	}
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingDraft, row.Status)
}

// TestVerifyListingFingerprintIncompleteFails covers a failed official call:
// without a completed comparison the listing stays in draft.
func TestVerifyListingFingerprintIncompleteFails(t *testing.T) {
	h := &fingerprintVerifyHandler{
		sellerKey: verifyTestKey, officialKey: verifyOfficialKey,
		sellerTokens: 100, officialTokens: 100, officialStatus: http.StatusInternalServerError,
	}
	db, listing, client := newVerifyListingFixture(t, h)
	t.Setenv(PlatformKeysEnv, `{"deepseek":"`+verifyOfficialKey+`"}`)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, res.FailReason, "fingerprint")
	for _, fr := range res.Fingerprints {
		assert.False(t, fr.Checked)
		assert.False(t, fr.Matched)
	}
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingDraft, row.Status)
}

// TestVerifyListingFingerprintOneModelFails proves a single uncertified model
// blocks the whole listing: every participating key×model needs BOTH a
// successful call and a matching fingerprint.
func TestVerifyListingFingerprintOneModelFails(t *testing.T) {
	h := &fingerprintVerifyHandler{
		sellerKey: verifyTestKey, officialKey: verifyOfficialKey,
		sellerTokens: 100, officialTokens: 100, officialFailModel: "deepseek-v4-pro",
	}
	db, listing, client := newVerifyListingFixture(t, h)
	t.Setenv(PlatformKeysEnv, `{"deepseek":"`+verifyOfficialKey+`"}`)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err)
	assert.False(t, res.OK)
	require.Len(t, res.Fingerprints, 2)
	assert.True(t, res.Fingerprints[0].Matched, "the first model's comparison passed")
	assert.False(t, res.Fingerprints[1].Matched, "one failing model blocks the listing")
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingDraft, row.Status)
}

// TestVerifyListingFingerprintMatchPasses is the all-green fail-closed path:
// both models are callable and both tokenizer comparisons match.
func TestVerifyListingFingerprintMatchPasses(t *testing.T) {
	h := &fingerprintVerifyHandler{
		sellerKey: verifyTestKey, officialKey: verifyOfficialKey,
		sellerTokens: 100, officialTokens: 100,
	}
	db, listing, client := newVerifyListingFixture(t, h)
	t.Setenv(PlatformKeysEnv, `{"deepseek":"`+verifyOfficialKey+`"}`)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err)
	require.True(t, res.OK, "fail reason: %s", res.FailReason)
	require.Len(t, res.Fingerprints, 2)
	for _, fr := range res.Fingerprints {
		assert.True(t, fr.Checked)
		assert.True(t, fr.Matched)
	}
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingCanary, row.Status, "verification promotes draft → verifying → canary")
}
