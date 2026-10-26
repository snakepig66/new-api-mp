package marketplace

// custom_channel_test.go — 卖家自定义第三方渠道 (custom_channel.go) 的
// 单测：URL 校验、渠道开通幂等与上限、CreateListing 走自定义渠道、
// /models 形态探测与 base 归一化、目录可见性（仅本人可见）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func TestValidateCustomBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr string
		want    string
	}{
		{name: "https root", raw: "https://api.example.com", want: "https://api.example.com"},
		{name: "https with version path", raw: "https://gw.example.com/v1/", want: "https://gw.example.com/v1"},
		{name: "trailing slash trimmed", raw: "https://api.example.com/", want: "https://api.example.com"},
		{name: "port kept", raw: "https://api.example.com:8443/v1", want: "https://api.example.com:8443/v1"},
		{name: "loopback test upstream", raw: "http://127.0.0.1:9911", want: "http://127.0.0.1:9911"},
		{name: "empty", raw: "   ", wantErr: "address is required"},
		{name: "relative", raw: "/v1", wantErr: "absolute"},
		{name: "http public host refused", raw: "http://api.example.com", wantErr: "https"},
		{name: "public ip refused", raw: "https://8.8.8.8", wantErr: "domain"},
		{name: "internal suffix refused", raw: "https://db.corp.internal", wantErr: "internal"},
		{name: "userinfo refused", raw: "https://user:pass@api.example.com", wantErr: "userinfo"},
		{name: "query refused", raw: "https://api.example.com/v1?x=1", wantErr: "query"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, _, err := validateCustomBaseURL(tc.raw)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrCustomBaseURL)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, base)
		})
	}
}

func newCustomChannelTestDB(t *testing.T) *testing.T {
	t.Helper()
	return t
}

func TestEnsureCustomChannelIdempotentAndCap(t *testing.T) {
	db := newListingTestDB(t)

	first, err := ensureCustomChannel(db, 10023, "https://api.example.com", []string{"deepseek-flash"})
	require.NoError(t, err)
	assert.True(t, isCustomChannelSlug(first))
	owner, ok := customChannelOwner(first)
	require.True(t, ok)
	assert.Equal(t, int64(10023), owner)

	// Same (seller, URL) maps to the same slug; the model rows are upserts.
	second, err := ensureCustomChannel(db, 10023, "https://api.example.com/", []string{"deepseek-flash"})
	require.NoError(t, err)
	assert.Equal(t, first, second)

	// A different seller with the same URL gets a different slug.
	other, err := ensureCustomChannel(db, 10024, "https://api.example.com", []string{"deepseek-flash"})
	require.NoError(t, err)
	assert.NotEqual(t, first, other)

	// The identity qualification exists and is verified.
	cm, found := ChannelModelFor(db, first, "deepseek-flash")
	require.True(t, found)
	assert.Equal(t, "deepseek-flash", cm.UpstreamModel)
	assert.True(t, cm.Verified)

	// Cap: the 11th distinct URL for one seller is refused.
	for i := 0; i < maxCustomChannelsPerSeller; i++ {
		_, err := ensureCustomChannel(db, 10025, fmt.Sprintf("https://host%d.example.com", i), nil)
		require.NoError(t, err)
	}
	_, err = ensureCustomChannel(db, 10025, "https://over-cap.example.com", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "custom channels per account")

	// Invalid URLs never reach the pool.
	_, err = ensureCustomChannel(db, 10023, "http://public.example.com", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCustomBaseURL)
}

func TestCreateListingWithCustomBaseURL(t *testing.T) {
	db := newListingTestDB(t)

	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		APIKey:         listingTestKey,
		CustomBaseURL:  "https://api.example.com",
		Models:         []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	assert.True(t, isCustomChannelSlug(listing.Vendor), "vendor id derives from the custom channel")
	assert.Equal(t, []string{"deepseek-flash"}, listing.Models)

	// The placeholder relay channel points at the seller's base and maps the
	// platform model to the identical upstream name.
	var ch model.Channel
	require.NoError(t, db.Where("id = ?", listing.ChannelID).Take(&ch).Error)
	require.NotNil(t, ch.BaseURL)
	assert.Equal(t, "https://api.example.com", *ch.BaseURL)
	mapping := map[string]string{}
	require.NoError(t, json.Unmarshal([]byte(ch.GetModelMapping()), &mapping))
	assert.Equal(t, "deepseek-flash", mapping["deepseek-flash"])

	// A second listing on the same URL reuses the channel row.
	again, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		APIKey:         "sk-other-key-abcdef",
		CustomBaseURL:  "https://api.example.com",
		Models:         []string{"deepseek-flash"},
		MultiplierMilli: 400,
	})
	require.NoError(t, err)
	assert.Equal(t, listing.Vendor, again.Vendor)

	// A model outside the platform catalogue is refused.
	_, err = CreateListing(db, CreateListingInput{
		SellerID:        10023,
		APIKey:         "sk-third-key-abcdef",
		CustomBaseURL:  "https://api2.example.com",
		Models:         []string{"not-in-catalogue"},
		MultiplierMilli: 400,
	})
	require.Error(t, err)
}

// fakeCustomUpstream serves an OpenAI-compatible /v1/models plus
// /v1/chat/completions, mounted under /v1 only — the form that forces the
// detection in normalizeCustomChannel to rewrite the base.
func fakeCustomUpstream(t *testing.T, key string, models ...string) *httptest.Server {
	t.Helper()
	seen := map[string]bool{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		data := make([]map[string]any, 0, len(models))
		for _, m := range models {
			data = append(data, map[string]any{"id": m})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen[fmt.Sprint(body["model"])] = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "pong"}}},
			"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestVerifyListingCustomChannelNormalizesBase(t *testing.T) {
	db := newListingTestDB(t)

	const key = "sk-custom-upstream-key"
	server := fakeCustomUpstream(t, key, "deepseek-flash")

	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		APIKey:         key,
		CustomBaseURL:  server.URL, // no /v1 — detection must add it
		Models:         []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)

	client := server.Client()
	limiter := NewVerifyLimiter(1, 600)
	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err)
	require.True(t, res.OK, "fail reason: %s", res.FailReason)

	// The pool row and the relay placeholder both carry the /v1 base and the
	// strip flag, so the relay dials the exact path that was verified.
	pool, found := ChannelPoolBySlug(db, listing.Vendor)
	require.True(t, found)
	assert.True(t, pool.StripV1Prefix)
	assert.Contains(t, pool.Protocols, server.URL+"/v1")

	var ch model.Channel
	require.NoError(t, db.Where("id = ?", listing.ChannelID).Take(&ch).Error)
	require.NotNil(t, ch.BaseURL)
	assert.Equal(t, server.URL+"/v1", *ch.BaseURL)
	require.NotNil(t, ch.Setting)
	assert.Contains(t, *ch.Setting, `"mp_strip_v1_prefix":true`)

	// The listing left draft (canary after verification).
	fresh, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingCanary, fresh.Status)
}

func TestVerifyListingCustomChannelBadKeyStaysDraft(t *testing.T) {
	db := newListingTestDB(t)

	server := fakeCustomUpstream(t, "sk-the-right-key", "deepseek-flash")

	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		APIKey:         "sk-the-wrong-key",
		CustomBaseURL:  server.URL,
		Models:         []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)

	client := server.Client()
	limiter := NewVerifyLimiter(1, 600)
	res, err := VerifyListing(context.Background(), db, client, limiter, listing.ID)
	require.NoError(t, err)
	assert.False(t, res.OK)

	fresh, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingDraft, fresh.Status)
}

func TestListVendorsHidesCustomChannelsFromOthers(t *testing.T) {
	fixture := newSellerAPIFixture(t)

	slug, err := ensureCustomChannel(fixture.db, fixture.sellerID, "https://private-upstream.example.com", []string{"deepseek-flash"})
	require.NoError(t, err)

	// The owner sees their custom channel on the seller catalogue.
	w := fixture.do(t, http.MethodGet, "/api/marketplace/seller/vendors", nil, int(fixture.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	var payload struct {
		Data []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"data"`
	}
	require.NoError(t, common.DecodeJson(w.Body, &payload))
	found := false
	for _, v := range payload.Data {
		if v.ID == slug {
			found = true
			assert.Equal(t, ChannelKindThirdParty, v.Kind)
		}
	}
	assert.True(t, found, "the owner must see their own custom channel")

	// A stranger does not.
	w = fixture.do(t, http.MethodGet, "/api/marketplace/seller/vendors", nil, int(fixture.stranger))
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, common.DecodeJson(w.Body, &payload))
	for _, v := range payload.Data {
		assert.NotEqual(t, slug, v.ID, "custom channels must stay private to their owner")
	}

	// The public catalogue never carries custom channels.
	w = fixture.do(t, http.MethodGet, "/api/marketplace/vendors", nil, int(fixture.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, common.DecodeJson(w.Body, &payload))
	for _, v := range payload.Data {
		assert.NotEqual(t, slug, v.ID, "the public catalogue must never carry custom channels")
	}
}

func TestProbeCustomChannelKeyDetectsV1(t *testing.T) {
	db := newListingTestDB(t)

	const key = "sk-probe-key"
	server := fakeCustomUpstream(t, key, "deepseek-flash", "some-unknown-model")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	detected, res, sellable := probeCustomChannelKey(ctx, server.Client(), db, server.URL, key)
	require.True(t, res.KeyValid, "fail reason: %s", res.FailReason)
	assert.Equal(t, server.URL+"/v1", detected, "detection adds the /v1 the upstream mounts")
	assert.Equal(t, []string{"deepseek-flash"}, sellable, "only platform catalogue names are sellable")
	assert.False(t, containsString(sellable, "some-unknown-model"))

	// A bad URL never dials.
	_, res, sellable = probeCustomChannelKey(ctx, server.Client(), db, "http://public.example.com", key)
	assert.False(t, res.KeyValid)
	assert.Empty(t, sellable)
	assert.True(t, strings.Contains(res.FailReason, "custom base url"))
}
