package marketplace

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSellerListingListAggregates(t *testing.T) {
	f := newSellerAPIFixture(t)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-list-aggregates"),
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)

	// Disable one model manually; the other gets a 0.6x override.
	require.NoError(t, f.db.Model(&ListingModel{}).
		Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").
		Update("enabled", false).Error)
	require.NoError(t, f.db.Model(&ListingModel{}).
		Where("listing_id = ? AND model = ?", listing.ID, "deepseek-v4-pro").
		Update("multiplier_milli", 600).Error)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/listings", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	items, ok := decodeSellerBody(t, w)["data"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	row := items[0].(map[string]any)

	assert.EqualValues(t, 2, row["model_count"])
	assert.EqualValues(t, 1, row["enabled_model_count"])
	assert.EqualValues(t, 0, row["auto_disabled_model_count"])
	assert.EqualValues(t, 600, row["multiplier_min_milli"])
	assert.EqualValues(t, 600, row["multiplier_max_milli"])
}

func TestSellerListingHealthWindows(t *testing.T) {
	f := newSellerAPIFixture(t)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-health-windows"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)

	now := time.Now()
	seed := func(id string, status string, ago time.Duration) {
		require.NoError(t, f.db.Create(&Request{
			RequestID: id, BuyerUserID: f.buyerID, SellerID: f.sellerID,
			ListingID: listing.ID, Model: "deepseek-flash",
			Status: status, CreatedAt: now.Add(-ago),
		}).Error)
	}
	seed("w-1h-a", RequestSuccess, 30*time.Minute)
	seed("w-1h-b", RequestSuccess, 40*time.Minute)
	seed("w-24h-fail", RequestFailed, 2*time.Hour)
	seed("w-7d", RequestSuccess, 2*24*time.Hour)
	seed("w-old", RequestSuccess, 8*24*time.Hour)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/listings/health", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	items, ok := decodeSellerBody(t, w)["data"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	row := items[0].(map[string]any)

	assert.EqualValues(t, 2, row["requests_1h"])
	assert.EqualValues(t, 1000, row["success_rate_1h_milli"])
	assert.EqualValues(t, 3, row["requests_24h"])
	assert.EqualValues(t, 666, row["success_rate_24h_milli"])
	assert.EqualValues(t, 4, row["requests_7d"])
	assert.EqualValues(t, 750, row["success_rate_7d_milli"])
}
