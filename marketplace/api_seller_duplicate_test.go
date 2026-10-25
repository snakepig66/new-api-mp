package marketplace

import (
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingTransport makes background verification fail immediately without
// touching the network. The tests below only assert the create response, but
// the async goroutine must still exit deterministically.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("test: offline")
}

func requireSellerActive(t *testing.T, f *sellerAPIFixture, userID int64) {
	t.Helper()
	var count int64
	require.NoError(t, f.db.Model(&Seller{}).Where("user_id = ?", userID).Count(&count).Error)
	if count > 0 {
		return
	}
	require.NoError(t, f.db.Create(&Seller{
		UserID:           userID,
		Status:           SellerStatusActive,
		RealNameStatus:   SellerRealNameVerified,
		AgreementVersion: CurrentSellerAgreementVersion,
	}).Error)
}

// TestSellerDuplicateKeyDistinction covers the two duplicate-key outcomes:
// the caller's own listing returns DUPLICATE_KEY_OWN + listing_id, while
// another account's key returns only DUPLICATE_KEY_OTHER (never the foreign
// listing id or seller id).
func TestSellerDuplicateKeyDistinction(t *testing.T) {
	f := newSellerAPIFixture(t)
	requireSellerActive(t, f, f.stranger)
	restore := setSellerVerifyClientForTest(&http.Client{Transport: failingTransport{}})
	defer restore()

	key := uniqueTestKey("sk-dup-distinct")
	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings", map[string]any{
		"vendor_id": "deepseek", "api_key": key,
		"models": []string{"deepseek-flash"}, "multiplier_milli": 500,
	}, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	id := int64(decodeSellerBody(t, w)["data"].(map[string]any)["id"].(float64))

	// Same seller, same key → own duplicate, with a linkable id.
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/listings", map[string]any{
		"vendor_id": "deepseek", "api_key": key,
		"models": []string{"deepseek-flash"}, "multiplier_milli": 500,
	}, int(f.sellerID))
	require.Equal(t, http.StatusConflict, w.Code)
	body := decodeSellerBody(t, w)
	assert.Equal(t, CodeDuplicateKeyOwn, body["code"])
	assert.EqualValues(t, id, body["listing_id"])

	// Another seller, same key → distinct code, no foreign identifiers.
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/listings", map[string]any{
		"vendor_id": "deepseek", "api_key": key,
		"models": []string{"deepseek-flash"}, "multiplier_milli": 500,
	}, int(f.stranger))
	require.Equal(t, http.StatusConflict, w.Code)
	raw := w.Body.String()
	body = decodeSellerBody(t, w)
	assert.Equal(t, CodeDuplicateKeyOther, body["code"])
	_, hasListingID := body["listing_id"]
	assert.False(t, hasListingID)
	assert.NotContains(t, raw, strconv.FormatInt(id, 10))
}

// TestCreateListingDuplicateKeyDistinctionIsInternal covers the library path:
// both outcomes still satisfy errors.Is(err, ErrDuplicateKey), and the own flag
// is what the API uses.
func TestCreateListingDuplicateKeyDistinctionIsInternal(t *testing.T) {
	db := newListingTestDB(t)
	_, err := CreateListing(db, CreateListingInput{
		SellerID:        10001,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          "sk-dup-lib-same-plaintext",
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 400,
	})
	require.NoError(t, err)
	_, err = CreateListing(db, CreateListingInput{
		SellerID:        10001,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          "sk-dup-lib-same-plaintext",
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 400,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDuplicateKey)
	var dup *DuplicateKeyError
	require.ErrorAs(t, err, &dup)
	assert.True(t, dup.Own)
	assert.NotZero(t, dup.ListingID)

	_, err = CreateListing(db, CreateListingInput{
		SellerID:        10002,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          "sk-dup-lib-same-plaintext",
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 400,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDuplicateKey)
	require.ErrorAs(t, err, &dup)
	assert.False(t, dup.Own)
}

// TestPlanTermsRequired covers the plan-channel gate at both layers: the
// library rejects without the flag, the API returns PLAN_TERMS_REQUIRED, and
// an explicit true passes validation once the qualification is sellable.
func TestPlanTermsRequired(t *testing.T) {
	f := newSellerAPIFixture(t)
	restore := setSellerVerifyClientForTest(&http.Client{Transport: failingTransport{}})
	defer restore()

	// Make the seeded plan qualification sellable; its model is the standard
	// deepseek-v4-pro, whose price book lives under the origin vendor.
	require.NoError(t, f.db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", testPlanChannelSlug, "deepseek-v4-pro").
		Update("verified", true).Error)

	key := uniqueTestKey("sk-plan-terms")
	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings", map[string]any{
		"vendor_id":        testPlanChannelSlug,
		"api_key":          key,
		"models":           []string{"deepseek-v4-pro"},
		"multiplier_milli": 500,
	}, int(f.sellerID))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, CodePlanTermsRequired, decodeSellerBody(t, w)["code"])

	w = f.do(t, http.MethodPost, "/api/marketplace/seller/listings", map[string]any{
		"vendor_id":           testPlanChannelSlug,
		"api_key":             key,
		"models":              []string{"deepseek-v4-pro"},
		"multiplier_milli":    500,
		"plan_terms_accepted": true,
	}, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// The library gate is the same check.
	_, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        testPlanChannelSlug,
		APIKey:          uniqueTestKey("sk-plan-lib"),
		Models:          []string{"deepseek-v4-pro"},
		MultiplierMilli: 500,
	})
	require.ErrorIs(t, err, ErrPlanTermsRequired)
}

// TestSellerReVerify covers POST /seller/listings/:id/verify: draft is
// accepted, verifying conflicts, and a live listing is rejected.
func TestSellerReVerify(t *testing.T) {
	f := newSellerAPIFixture(t)
	restore := setSellerVerifyClientForTest(&http.Client{Transport: failingTransport{}})
	defer restore()

	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-reverify"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)

	path := "/api/marketplace/seller/listings/" + strconv.FormatInt(listing.ID, 10) + "/verify"
	w := f.do(t, http.MethodPost, path, nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)

	// Verification fails against the offline transport and returns the row to
	// draft; then the same call is accepted again.
	require.Eventually(t, func() bool {
		row, err := GetListing(f.db, listing.ID)
		return err == nil && row.Status == ListingDraft
	}, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).
		Update("status", ListingLive).Error)
	w = f.do(t, http.MethodPost, path, nil, int(f.sellerID))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Ownership: another user gets the uniform 404.
	w = f.do(t, http.MethodPost, path, nil, int(f.stranger))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestSellerListVerificationAndPlanPause covers the richer seller list:
// per-model verification results, pause_reason and resume_at.
func TestSellerListVerificationAndPlanPause(t *testing.T) {
	f := newSellerAPIFixture(t)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-list-verification"),
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)

	resumeAt := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).Updates(map[string]any{
		"status":       ListingPaused,
		"pause_reason": PauseReasonPlanQuotaExhausted,
		"resume_at":    resumeAt,
	}).Error)
	detail := `{"key_valid":true,"fail_reason":"one model failed",` +
		`"model_results":[` +
		`{"model":"deepseek-flash","upstream_model":"deepseek-flash","ok":true,"prompt_cache_support":true,"latency_ms":40},` +
		`{"model":"deepseek-v4-pro","upstream_model":"deepseek-v4-pro","ok":false,"latency_ms":80,"fail_reason":"该模型不在这个 Key 的可用模型列表中"}]}`
	require.NoError(t, f.db.Create(&Probe{
		ListingID: listing.ID, Type: ProbeTypeVerify, OK: false, LatencyMs: 80, Detail: detail,
	}).Error)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/listings", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	items := decodeSellerBody(t, w)["data"].([]any)
	require.Len(t, items, 1)
	row := items[0].(map[string]any)
	assert.Equal(t, PauseReasonPlanQuotaExhausted, row["pause_reason"])
	resume, ok := row["resume_at"].(string)
	require.True(t, ok)
	parsed, err := time.Parse(time.RFC3339, resume)
	require.NoError(t, err)
	assert.True(t, parsed.Equal(resumeAt))

	verification, ok := row["verification"].(map[string]any)
	require.True(t, ok)
	results, ok := verification["model_results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 2)
	first := results[0].(map[string]any)
	assert.Equal(t, "deepseek-flash", first["model"])
	assert.Equal(t, true, first["ok"])
	second := results[1].(map[string]any)
	assert.Equal(t, false, second["ok"])
	assert.Contains(t, second["fail_reason"], "可用模型列表")

	// Detail carries the same fields.
	w = f.do(t, http.MethodGet, "/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10), nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	detailBody := decodeSellerBody(t, w)["data"].(map[string]any)
	assert.Equal(t, PauseReasonPlanQuotaExhausted, detailBody["pause_reason"])
	assert.Contains(t, detailBody, "resume_at")
	assert.Contains(t, detailBody, "verification")
}

// TestSellerVendorsExposeQuotaWindow covers the plan-window data the wizard
// renders: the parsed quota_window and the plan terms URL.
func TestSellerVendorsExposeQuotaWindow(t *testing.T) {
	f := newSellerAPIFixture(t)
	w := f.do(t, http.MethodGet, "/api/marketplace/vendors", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	items := decodeSellerBody(t, w)["data"].([]any)
	var plan map[string]any
	for _, item := range items {
		row := item.(map[string]any)
		if row["id"] == testPlanChannelSlug {
			plan = row
			break
		}
	}
	require.NotNil(t, plan, "seeded plan channel must be listed")
	assert.Equal(t, "plan", plan["kind"])
	quota, ok := plan["quota_window"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 18000, quota["period_seconds"])
	assert.Equal(t, "Asia/Shanghai", quota["reset_timezone"])
}
