package marketplace

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/relaykit/types"
)

func TestClassifyModelUnavailable(t *testing.T) {
	cases := []struct {
		name string
		err  *types.NewAPIError
		want relayErrorClass
	}{
		{"404 without a code", types.WithOpenAIError(types.OpenAIError{Message: "not found"}, http.StatusNotFound), relayErrorClassModelUnavailable},
		{"explicit model_not_found", types.WithOpenAIError(types.OpenAIError{Message: "model not found", Code: "model_not_found"}, http.StatusBadRequest), relayErrorClassModelUnavailable},
		{"dashscope model.accessdenied", types.WithOpenAIError(types.OpenAIError{Message: "access denied", Code: "Model.AccessDenied"}, http.StatusForbidden), relayErrorClassModelUnavailable},
		{"zhipu 1211", types.WithOpenAIError(types.OpenAIError{Message: "model not exist", Code: "1211"}, http.StatusBadRequest), relayErrorClassModelUnavailable},
		{"buyer tools 400 is not a model failure", types.WithOpenAIError(types.OpenAIError{
			Message: "This model does not support tools", Type: "invalid_request_error", Code: "invalid_request_error",
		}, http.StatusBadRequest), relayErrorClassContent},
		{"buyer image 400 is not a model failure", types.WithOpenAIError(types.OpenAIError{
			Message: "This model does not support image input", Type: "invalid_request_error", Code: "invalid_request_error",
		}, http.StatusBadRequest), relayErrorClassContent},
		{"403 insufficient balance stays balance", types.WithOpenAIError(types.OpenAIError{
			Message: "insufficient balance", Code: "insufficient_quota",
		}, http.StatusForbidden), relayErrorClassBalance},
		{"401 stays auth", types.WithOpenAIError(types.OpenAIError{
			Message: "invalid api key", Type: "authentication_error", Code: "invalid_api_key",
		}, http.StatusUnauthorized), relayErrorClassAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyRelayError(tc.err))
		})
	}
}

func TestProbeIndicatesModelUnavailable(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		message string
		want    bool
	}{
		{"404 always", http.StatusNotFound, "", true},
		{"model not found text", http.StatusBadRequest, "model not found", true},
		{"model not activated", http.StatusBadRequest, "模型未开通", true},
		{"tools unsupported text is buyer-parameter wording", http.StatusBadRequest, "model does not support tools", false},
		{"balance is not a model failure", http.StatusForbidden, "insufficient balance", false},
		{"rate limit is not a model failure", http.StatusTooManyRequests, "rate limit exceeded", false},
		{"auth is not a model failure", http.StatusUnauthorized, "invalid api key", false},
		{"unrelated 500", http.StatusInternalServerError, "internal error", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, probeIndicatesModelUnavailable(tc.status, tc.message))
		})
	}
}

func TestSanitizeUpstreamMessage(t *testing.T) {
	raw := `{"error":{"message":"model not found for sk-abcdefgh12345678\n"}}`
	got := sanitizeUpstreamMessage(raw, "sk-abcdefgh12345678")
	assert.Equal(t, "model not found for sk-***", got)
	assert.NotContains(t, got, "\n")
	assert.NotContains(t, got, "sk-abcdefgh")
}

func TestDisableAndRestoreModel(t *testing.T) {
	db := newListingTestDB(t)
	key := uniqueTestKey("sk-model-health")
	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", listing.ID).
		Update("status", ListingLive).Error)

	now := time.Now()
	require.NoError(t, DisableListingModel(db, listing.ID, "deepseek-flash", "model_not_found",
		"not found", http.StatusNotFound, "model not found upstream", now))

	var row ListingModel
	require.NoError(t, db.Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").Take(&row).Error)
	assert.False(t, row.Enabled)
	assert.True(t, row.AutoDisabled)
	assert.Equal(t, "model_not_found", row.DisabledReason)
	assert.Equal(t, http.StatusNotFound, row.DisabledHTTPStatus)
	assert.Equal(t, "model not found upstream", row.DisabledUpstreamMessage)

	// The model is gone from the buyer-facing supply.
	overrides := loadListingModelOverrides(db, []int64{listing.ID}, now)
	assert.False(t, overrides.modelEnabled(listing.ID, "deepseek-flash"))

	// Restore runs a platform probe; the mock answers 200.
	h := allGreenHandler()
	h.wantAuth = "Bearer " + key
	srv, _ := newVendorServer(t, h)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	restore := setSellerVerifyClientForTest(&http.Client{Transport: rewriteTransport{target: target}})
	t.Cleanup(restore)

	result, err := RestoreAutoDisabledModels(db, listing.ID, listing.SellerID, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 1, result.Restored)
	assert.Empty(t, result.StillFailing)
	require.NoError(t, db.Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").Take(&row).Error)
	assert.True(t, row.Enabled)
	assert.False(t, row.AutoDisabled)

	// A failing probe keeps the model disabled and reports why.
	require.NoError(t, DisableListingModel(db, listing.ID, "deepseek-flash", "model_not_found",
		"not found", http.StatusNotFound, "model not found upstream", time.Now()))
	h.chatStatus = http.StatusNotFound
	h.chatBody = `{"error":{"message":"model not found"}}`
	result, err = RestoreAutoDisabledModels(db, listing.ID, listing.SellerID, time.Now())
	require.NoError(t, err)
	assert.Zero(t, result.Restored)
	require.Len(t, result.StillFailing, 1)
	assert.Equal(t, "deepseek-flash", result.StillFailing[0].Model)
	require.NoError(t, db.Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").Take(&row).Error)
	assert.False(t, row.Enabled)
	assert.Equal(t, "restore_failed", row.DisabledReason)
}

func TestVerifyModelAvailabilityDisablesOnProbeFailure(t *testing.T) {
	db := newListingTestDB(t)
	key := uniqueTestKey("sk-model-probe")
	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", listing.ID).
		Update("status", ListingLive).Error)

	h := allGreenHandler()
	h.wantAuth = "Bearer " + key
	h.chatStatus = http.StatusNotFound
	h.chatBody = `{"error":{"message":"model not found"}}`
	srv, _ := newVendorServer(t, h)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	restore := setSellerVerifyClientForTest(&http.Client{Transport: rewriteTransport{target: target}})
	t.Cleanup(restore)

	verifyModelAvailabilityAsync(db, listing.ID, "deepseek-flash")
	var row ListingModel
	require.NoError(t, db.Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").Take(&row).Error)
	assert.False(t, row.Enabled)
	assert.True(t, row.AutoDisabled)

	// A second run within ten minutes is deduplicated by the probe record.
	var before int64
	require.NoError(t, db.Model(&Probe{}).Where("listing_id = ? AND type = ?", listing.ID, ProbeTypeModelCheck).Count(&before).Error)
	verifyModelAvailabilityAsync(db, listing.ID, "deepseek-flash")
	var after int64
	require.NoError(t, db.Model(&Probe{}).Where("listing_id = ? AND type = ?", listing.ID, ProbeTypeModelCheck).Count(&after).Error)
	assert.Equal(t, before, after)
}

func TestPatrolDisablesModelAfterTwoFailures(t *testing.T) {
	db := newListingTestDB(t)
	key := uniqueTestKey("sk-patrol-model")
	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", listing.ID).
		Update("status", ListingLive).Error)

	h := allGreenHandler()
	h.wantAuth = "Bearer " + key
	h.chatStatus = http.StatusNotFound
	h.chatBody = `{"error":{"message":"模型不存在"}}`
	srv, _ := newVendorServer(t, h)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := &http.Client{Transport: rewriteTransport{target: target}}

	// First failure is not enough: the listing stays live and the model stays on.
	require.NoError(t, patrolLiveListingsWithClient(db, client, time.Now()))
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, row.Status)
	var modelRow ListingModel
	require.NoError(t, db.Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").Take(&modelRow).Error)
	assert.True(t, modelRow.Enabled)

	// Second consecutive failure disables only the model; the listing stays live.
	require.NoError(t, patrolLiveListingsWithClient(db, client, time.Now()))
	row, err = GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, row.Status)
	require.NoError(t, db.Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").Take(&modelRow).Error)
	assert.False(t, modelRow.Enabled)
	assert.True(t, modelRow.AutoDisabled)
	assert.Equal(t, "patrol_failed", modelRow.DisabledReason)
}

func TestVerifyModelAvailabilityKeepsModelOnProbeSuccess(t *testing.T) {
	db := newListingTestDB(t)
	key := uniqueTestKey("sk-model-probe-ok")
	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", listing.ID).
		Update("status", ListingLive).Error)

	h := allGreenHandler()
	h.wantAuth = "Bearer " + key
	srv, _ := newVendorServer(t, h)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	restore := setSellerVerifyClientForTest(&http.Client{Transport: rewriteTransport{target: target}})
	t.Cleanup(restore)

	verifyModelAvailabilityAsync(db, listing.ID, "deepseek-flash")
	var row ListingModel
	require.NoError(t, db.Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").Take(&row).Error)
	assert.True(t, row.Enabled, "a successful platform probe must not disable the model")
	assert.False(t, row.AutoDisabled)
}
