package marketplace

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSellerModelsEndpoint(t *testing.T) {
	f := newSellerAPIFixture(t)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-models-endpoint"),
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		MultiplierMilli: 400,
	})
	require.NoError(t, err)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/listings/"+itoa(listing.ID)+"/models", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	data, ok := decodeSellerBody(t, w)["data"].(map[string]any)
	require.True(t, ok)
	items, ok := data["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 2)
	row := items[0].(map[string]any)
	assert.EqualValues(t, MultiplierOfficialMinMilli, row["multiplier_min_milli"])
	assert.EqualValues(t, MultiplierAbsoluteMaxMilli, row["multiplier_max_milli"])
	official, ok := row["official_prices"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, official)
	buyer, ok := row["buyer_prices"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, buyer)
	assert.EqualValues(t, PlatformFeePercent, data["platform_fee_percent"])
}

func TestSellerMultiplierBoundsEndpoint(t *testing.T) {
	f := newSellerAPIFixture(t)
	w := f.do(t, http.MethodGet, "/api/marketplace/seller/multiplier-bounds", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	data, ok := decodeSellerBody(t, w)["data"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, MultiplierAbsoluteMinMilli, data["absolute_min_milli"])
	assert.EqualValues(t, MultiplierAbsoluteMaxMilli, data["absolute_max_milli"])
	kinds, ok := data["kinds"].(map[string]any)
	require.True(t, ok)
	official, ok := kinds[ChannelKindOfficial].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, MultiplierOfficialMinMilli, official["min_milli"])
}

func TestSellerModelStateEndpoints(t *testing.T) {
	f := newSellerAPIFixture(t)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-model-state-api"),
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		MultiplierMilli: 600,
	})
	require.NoError(t, err)
	base := "/api/marketplace/seller/listings/" + itoa(listing.ID)

	// A decrease is immediate.
	w := f.do(t, http.MethodPut, base+"/models/deepseek-flash",
		map[string]any{"multiplier_milli": 500}, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)

	// An increase is staged.
	w = f.do(t, http.MethodPut, base+"/models/deepseek-flash",
		map[string]any{"multiplier_milli": 600}, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	row := decodeSellerBody(t, w)["data"].(map[string]any)
	assert.EqualValues(t, 500, row["multiplier_milli"])
	assert.EqualValues(t, 600, row["pending_multiplier_milli"])

	// A second increase while one is pending is a 409 with per-row detail.
	w = f.do(t, http.MethodPut, base+"/models/deepseek-flash",
		map[string]any{"multiplier_milli": 700}, int(f.sellerID))
	require.Equal(t, http.StatusConflict, w.Code)

	// Batch over both models: one is disabled (no price change) and the other
	// is re-enabled in the same call.
	w = f.do(t, http.MethodPut, base+"/models",
		map[string]any{"models": []string{"deepseek-v4-pro"}, "enabled": false}, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestSellerBatchModelsEndpoint(t *testing.T) {
	f := newSellerAPIFixture(t)
	first, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-batch-a"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 400,
	})
	require.NoError(t, err)
	second, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-batch-b"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 400,
	})
	require.NoError(t, err)

	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings/batch-models", map[string]any{
		"listing_ids":      []int64{first.ID, second.ID},
		"multiplier_milli": 500,
	}, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)

	// An increase on top of the staged one is rejected for the whole batch.
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/listings/batch-models", map[string]any{
		"listing_ids":      []int64{first.ID, second.ID},
		"multiplier_milli": 600,
	}, int(f.sellerID))
	require.Equal(t, http.StatusConflict, w.Code)
	body := decodeSellerBody(t, w)
	items, ok := body["items"].([]any)
	require.True(t, ok)
	assert.Len(t, items, 2)
}

func TestCreateListingRejectsBelowKindFloor(t *testing.T) {
	f := newSellerAPIFixture(t)
	_, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-below-floor"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: MultiplierOfficialMinMilli - 1,
	})
	require.Error(t, err)
}
