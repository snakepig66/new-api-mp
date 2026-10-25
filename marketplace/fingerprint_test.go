package marketplace

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// ---------------------------------------------------------------------------
// fingerprint comparison
// ---------------------------------------------------------------------------

// fingerprintChatMock is a minimal OpenAI-compatible upstream for
// CompareFingerprint: it records the last request body/auth and answers with a
// fixed usage.prompt_tokens.
type fingerprintChatMock struct {
	promptTokens int64
	status       int
	noUsage      bool
	delay        time.Duration

	calls atomic.Int32
	mu    sync.Mutex
	body  map[string]any
	auth  string
}

func (h *fingerprintChatMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls.Add(1)
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = common.Unmarshal(raw, &body)
	h.mu.Lock()
	h.body = body
	h.auth = r.Header.Get("Authorization")
	h.mu.Unlock()

	if h.delay > 0 {
		select {
		case <-time.After(h.delay):
		case <-r.Context().Done():
			return
		}
	}
	if r.URL.Path != "/chat/completions" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if h.status != 0 && h.status != http.StatusOK {
		w.WriteHeader(h.status)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream secret body must not be echoed"}}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	if h.noUsage {
		_, _ = w.Write([]byte(`{"choices":[]}`))
		return
	}
	_, _ = fmt.Fprintf(w,
		`{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":%d,"completion_tokens":1,"total_tokens":%d}}`,
		h.promptTokens, h.promptTokens+1)
}

func (h *fingerprintChatMock) lastRequest() (map[string]any, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.body, h.auth
}

func newFingerprintVendor(t *testing.T, srv *httptest.Server) Vendor {
	t.Helper()
	return Vendor{ID: "mock", DisplayName: "Mock", BaseURL: srv.URL}
}

func TestFingerprintCompareTolerance(t *testing.T) {
	tests := []struct {
		name            string
		officialTokens  int64
		candidateTokens int64
		candidateNoUse  bool
		wantRatio       float64
		wantWithin      bool
		wantFail        string
	}{
		{name: "identical tokenizer", officialTokens: 10, candidateTokens: 10,
			wantRatio: 1, wantWithin: true},
		{name: "three percent drift", officialTokens: 100, candidateTokens: 103,
			wantRatio: 1.03, wantWithin: false},
		{name: "candidate reports zero", officialTokens: 10, candidateTokens: 0,
			wantFail: "missing prompt token usage"},
		{name: "candidate omits usage", officialTokens: 10, candidateNoUse: true,
			wantFail: "missing prompt token usage"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			officialMock := &fingerprintChatMock{promptTokens: tc.officialTokens}
			candidateMock := &fingerprintChatMock{promptTokens: tc.candidateTokens, noUsage: tc.candidateNoUse}
			officialSrv := httptest.NewServer(officialMock)
			candidateSrv := httptest.NewServer(candidateMock)
			t.Cleanup(officialSrv.Close)
			t.Cleanup(candidateSrv.Close)

			const prompt = "tokenizer fingerprint prompt"
			res := CompareFingerprint(context.Background(), officialSrv.Client(),
				newFingerprintVendor(t, officialSrv), "sk-official-secret", "official-upstream",
				newFingerprintVendor(t, candidateSrv), "sk-candidate-secret", "candidate-upstream",
				prompt)

			if tc.wantFail != "" {
				assert.Equal(t, tc.wantFail, res.FailReason)
				assert.False(t, res.WithinTolerance)
				return
			}
			require.Empty(t, res.FailReason)
			assert.Equal(t, tc.officialTokens, res.PromptTokensOfficial)
			assert.Equal(t, tc.candidateTokens, res.PromptTokensCandidate)
			assert.InDelta(t, tc.wantRatio, res.Ratio, 0.00005)
			assert.Equal(t, tc.wantWithin, res.WithinTolerance)

			// Both requests carry the minimal paid settings and the bearer key,
			// but the result never exposes the key.
			for _, mock := range []*fingerprintChatMock{officialMock, candidateMock} {
				body, auth := mock.lastRequest()
				require.NotNil(t, body)
				assert.EqualValues(t, 1, body["max_tokens"])
				assert.EqualValues(t, 0, body["temperature"])
				assert.Equal(t, prompt, body["messages"].([]any)[0].(map[string]any)["content"])
				assert.True(t, strings.HasPrefix(auth, "Bearer sk-"))
			}
			assert.NotContains(t, res.FailReason, "sk-official-secret")
			assert.NotContains(t, res.FailReason, "sk-candidate-secret")
		})
	}
}

func TestFingerprintCompareFailurePaths(t *testing.T) {
	run := func(t *testing.T, officialStatus, candidateStatus int, ctx context.Context, officialDelay time.Duration) FingerprintResult {
		t.Helper()
		officialMock := &fingerprintChatMock{promptTokens: 10, status: officialStatus, delay: officialDelay}
		candidateMock := &fingerprintChatMock{promptTokens: 10, status: candidateStatus}
		officialSrv := httptest.NewServer(officialMock)
		candidateSrv := httptest.NewServer(candidateMock)
		t.Cleanup(officialSrv.Close)
		t.Cleanup(candidateSrv.Close)
		return CompareFingerprint(ctx, officialSrv.Client(),
			newFingerprintVendor(t, officialSrv), "sk-official-secret", "official-upstream",
			newFingerprintVendor(t, candidateSrv), "sk-candidate-secret", "candidate-upstream",
			"prompt")
	}

	t.Run("official non-200", func(t *testing.T) {
		res := run(t, http.StatusInternalServerError, 0, context.Background(), 0)
		require.NotEmpty(t, res.FailReason)
		assert.Contains(t, res.FailReason, "official")
		assert.NotContains(t, res.FailReason, "sk-official-secret")
		assert.NotContains(t, res.FailReason, "upstream secret body")
	})

	t.Run("candidate non-200", func(t *testing.T) {
		res := run(t, 0, http.StatusUnauthorized, context.Background(), 0)
		require.NotEmpty(t, res.FailReason)
		assert.Contains(t, res.FailReason, "candidate")
		assert.NotContains(t, res.FailReason, "sk-candidate-secret")
		assert.NotContains(t, res.FailReason, "upstream secret body")
	})

	t.Run("timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		res := run(t, 0, 0, ctx, 200*time.Millisecond)
		require.NotEmpty(t, res.FailReason)
		assert.NotContains(t, res.FailReason, "sk-official-secret")
	})
}

func TestFingerprintHTTPClientOverride(t *testing.T) {
	officialMock := &fingerprintChatMock{promptTokens: 10}
	candidateMock := &fingerprintChatMock{promptTokens: 10}
	officialSrv := httptest.NewServer(officialMock)
	candidateSrv := httptest.NewServer(candidateMock)
	t.Cleanup(officialSrv.Close)
	t.Cleanup(candidateSrv.Close)

	old := fingerprintHTTPClient
	fingerprintHTTPClient = candidateSrv.Client()
	t.Cleanup(func() { fingerprintHTTPClient = old })

	// nil client would otherwise use http.DefaultClient; the package override
	// must win and make both requests reachable.
	res := CompareFingerprint(context.Background(), nil,
		newFingerprintVendor(t, officialSrv), "sk-official", "official-upstream",
		newFingerprintVendor(t, candidateSrv), "sk-candidate", "candidate-upstream",
		"prompt")
	assert.True(t, res.WithinTolerance)
	assert.Empty(t, res.FailReason)
}

func TestFingerprintPlatformKeys(t *testing.T) {
	t.Setenv(PlatformKeysEnv, `{"deepseek":"sk-platform-deepseek","zhipu":"sk-platform-zhipu"}`)

	key, ok := PlatformKeyForVendor("deepseek")
	assert.True(t, ok)
	assert.Equal(t, "sk-platform-deepseek", key)

	key, ok = PlatformKeyForVendor("DeepSeek")
	assert.True(t, ok, "vendor lookup is case-insensitive")
	assert.Equal(t, "sk-platform-deepseek", key)

	_, ok = PlatformKeyForVendor("moonshot")
	assert.False(t, ok)

	t.Setenv(PlatformKeysEnv, `not json`)
	_, ok = PlatformKeyForVendor("deepseek")
	assert.False(t, ok, "malformed JSON degrades to no key")

	t.Setenv(PlatformKeysEnv, ``)
	_, ok = PlatformKeyForVendor("deepseek")
	assert.False(t, ok)
}

// ---------------------------------------------------------------------------
// VerifyChannelKey (channel path, platform key)
// ---------------------------------------------------------------------------

func TestFingerprintVerifyChannelKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"up-a"},{"id":"up-b"},{"id":"up-a"}]}`))
	}))
	t.Cleanup(srv.Close)

	res := VerifyChannelKey(context.Background(), srv.Client(), newFingerprintVendor(t, srv), "sk-platform-key")
	assert.True(t, res.OK)
	assert.True(t, res.KeyValid)
	assert.Equal(t, []string{"up-a", "up-b"}, res.Models)
	assert.GreaterOrEqual(t, res.LatencyMs, int64(0))
	assert.Empty(t, res.FailReason)
	assert.Equal(t, "Bearer sk-platform-key", gotAuth)
}

func TestFingerprintVerifyChannelKeyUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream secret body must not be echoed"}}`))
	}))
	t.Cleanup(srv.Close)

	const key = "sk-platform-secret"
	res := VerifyChannelKey(context.Background(), srv.Client(), newFingerprintVendor(t, srv), key)
	assert.False(t, res.OK)
	assert.False(t, res.KeyValid)
	require.NotEmpty(t, res.FailReason)
	assert.Contains(t, res.FailReason, "key rejected")
	assert.NotContains(t, res.FailReason, key)
	assert.NotContains(t, res.FailReason, "upstream secret body")
}

// ---------------------------------------------------------------------------
// seller probe: certified-model intersection
// ---------------------------------------------------------------------------

// probeVendorMock is an OpenAI-compatible upstream for the seller probe: it
// lists the given upstream names and answers the balance and paid prompt-cache
// probes.
type probeVendorMock struct {
	models []string
}

func (h *probeVendorMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/models":
		data := make([]map[string]string, 0, len(h.models))
		for _, m := range h.models {
			data = append(data, map[string]string{"id": m})
		}
		raw, _ := common.Marshal(map[string]any{"object": "list", "data": data})
		_, _ = w.Write(raw)
	case "/user/balance":
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"5.00"}]}`))
	case "/chat/completions":
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}],"usage":{"prompt_cache_hit_tokens":4,"prompt_cache_miss_tokens":1,"prompt_tokens":5}}`))
	default:
		http.NotFound(w, r)
	}
}

// seedProbeChannel inserts one pool channel pointing at baseURL plus its
// channel-model rows. Rows keep the verified/status flags the test sets.
func seedProbeChannel(t *testing.T, db *gorm.DB, slug, baseURL string, rows []ChannelModel) {
	t.Helper()
	require.NoError(t, db.Create(&ChannelPool{
		Slug:      slug,
		Name:      slug,
		Kind:      ChannelKindOfficial,
		Protocols: fmt.Sprintf(`{"openai":%q}`, baseURL),
		Adaptor:   VendorAdaptorOpenAI,
		Status:    PoolStatusActive,
	}).Error)
	for i := range rows {
		row := rows[i]
		row.ChannelSlug = slug
		if row.Status == "" {
			row.Status = PoolStatusActive
		}
		require.NoError(t, db.Create(&row).Error)
	}
}

// probeData calls POST /seller/listings/probe and returns its data envelope.
func probeData(t *testing.T, f *sellerAPIFixture, vendorID, apiKey string, models []string) map[string]any {
	t.Helper()
	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings/probe", map[string]any{
		"vendor_id": vendorID,
		"api_key":   apiKey,
		"models":    models,
	}, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), apiKey, "§2.3: probe never echoes the key")
	return decodeSellerBody(t, w)["data"].(map[string]any)
}

func TestProbeReturnsCertifiedIntersection(t *testing.T) {
	f := newSellerAPIFixture(t)
	resetVendorRegistryForTest()
	t.Cleanup(resetVendorRegistryForTest)
	// The fixture loads the pool seeds; the custom channels below must not leak
	// into other tests' vendor registry.
	defer resetVendorRegistryForTest()

	// refreshVendorRegistry also rewrites the egress whitelist from the test
	// channels (127.0.0.1 mocks). Restore it so later tests see the production
	// hosts rather than a test-only list.
	egressMu.RLock()
	oldDomains := append([]string(nil), egressDomains...)
	oldPatterns := append([]*regexp.Regexp(nil), egressPatterns...)
	egressMu.RUnlock()
	t.Cleanup(func() {
		egressMu.Lock()
		egressDomains, egressPatterns = oldDomains, oldPatterns
		egressMu.Unlock()
	})

	mockA := httptest.NewServer(&probeVendorMock{models: []string{"up-a1", "up-a2", "up-a3", "foreign-up"}})
	t.Cleanup(mockA.Close)
	mockB := httptest.NewServer(&probeVendorMock{models: []string{"up-b1"}})
	t.Cleanup(mockB.Close)

	// Channel A: only up-a1 is verified+active AND exposed by /models.
	seedProbeChannel(t, f.db, "mock-a", mockA.URL, []ChannelModel{
		{UpstreamModel: "up-a1", Model: "deepseek-flash", Verified: true, Status: PoolStatusActive},
		{UpstreamModel: "up-a2", Model: "kimi-k2", Verified: false, Status: PoolStatusActive},
		{UpstreamModel: "up-a3", Model: "glm-4.6", Verified: true, Status: PoolStatusPaused},
		{UpstreamModel: "up-a4", Model: "deepseek-v4-pro", Verified: true, Status: PoolStatusActive},
	})
	// Channel B is a second mock with its own certified model.
	seedProbeChannel(t, f.db, "mock-b", mockB.URL, []ChannelModel{
		{UpstreamModel: "up-b1", Model: "minimax-m2", Verified: true, Status: PoolStatusActive},
	})
	// Channel C reuses B's /models but its only certified upstream is not
	// exposed, so the certification intersection is empty.
	seedProbeChannel(t, f.db, "mock-c", mockB.URL, []ChannelModel{
		{UpstreamModel: "up-c1", Model: "glm-4.6", Verified: true, Status: PoolStatusActive},
	})
	resetVendorRegistryForTest()

	restore := setSellerVerifyClientForTest(mockA.Client())
	defer restore()

	// A: request may name a standard id, but the server returns the whole
	// certified intersection. up-a2 (unverified), up-a3 (paused) and up-a4
	// (not exposed by /models) are excluded.
	data := probeData(t, f, "mock-a", verifyTestKey, []string{"deepseek-flash"})
	assert.Equal(t, true, data["ok"])
	assert.Equal(t, true, data["key_valid"])
	assert.Equal(t, true, data["prompt_cache"])
	assert.EqualValues(t, 5_000_000, data["balance_micro"])
	assert.Equal(t, []any{"deepseek-flash"}, data["models"])
	assert.Equal(t, []any{"deepseek-flash"}, data["verified_models"])
	assert.Equal(t, []any{}, data["missing_models"])
	assert.Equal(t, "", data["fail_reason"])
	assert.NotContains(t, data["models"], "kimi-k2")
	assert.NotContains(t, data["models"], "glm-4.6")
	assert.NotContains(t, data["models"], "minimax-m2", "another channel's qualification must not leak")

	// Gate: a requested model outside the certified set is reported missing,
	// never granted, and flips ok to false.
	data = probeData(t, f, "mock-a", verifyTestKey, []string{"deepseek-flash", "kimi-k2"})
	assert.Equal(t, false, data["ok"])
	assert.Equal(t, []any{"deepseek-flash"}, data["models"])
	assert.Equal(t, []any{"kimi-k2"}, data["missing_models"])

	// B: the second mock's channel returns only its own certified standard id.
	data = probeData(t, f, "mock-b", verifyTestKey, []string{"minimax-m2"})
	assert.Equal(t, true, data["ok"])
	assert.Equal(t, []any{"minimax-m2"}, data["models"])
	assert.NotContains(t, data["models"], "deepseek-flash")

	// C: valid key, but no verified/active/exposed upstream => explicit reason.
	data = probeData(t, f, "mock-c", verifyTestKey, nil)
	assert.Equal(t, false, data["ok"])
	assert.Equal(t, true, data["key_valid"])
	assert.Equal(t, []any{}, data["models"])
	assert.Equal(t, "no certified models available for this key", data["fail_reason"])
}
