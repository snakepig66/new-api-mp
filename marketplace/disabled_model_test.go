package marketplace

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// disableListingModel inserts an explicit disabled override. Step 0a's whole
// point is that such a row must remove the listing from the model, not fall
// back to the base multiplier.
func disableListingModel(t *testing.T, db *gorm.DB, listingID int64, model string, multiplier int64) {
	t.Helper()
	require.NoError(t, db.Create(&ListingModel{
		ListingID:       listingID,
		Model:           model,
		MultiplierMilli: multiplier,
		Enabled:         false,
	}).Error)
}

func TestSelectSkipsDisabledModel(t *testing.T) {
	loc := shanghaiLocation(t)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, loc)
	db := newTestDB(t)
	migrateAll(t, db)

	listing := addSelectorListing(t, db, "fp-disabled-select", nil)
	disableListingModel(t, db, listing.ID, "deepseek-flash", 600)

	s := NewSelector()
	s.Rand = rand.New(rand.NewSource(1))

	_, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
	require.ErrorIs(t, err, ErrNoListingAvailable,
		"a disabled model must not fall back to the base multiplier")

	has, err := hasLiveListingForModel(db, "deepseek-flash")
	require.NoError(t, err)
	assert.False(t, has, "has-live must ignore a listing that disabled the model")
	anyListing, err := hasAnyListingForModel(db, "deepseek-flash")
	require.NoError(t, err)
	assert.True(t, anyListing,
		"the marketplace still owns the model; disabling one listing's model must not fall through to regular channels")

	require.NoError(t, db.Model(&ListingModel{}).
		Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").
		Update("enabled", true).Error)

	got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
	require.NoError(t, err)
	assert.Equal(t, listing.ID, got.ID)
	s.Slots.Release(got.ID)
}

func TestPreFreezeQuoteForListingRejectsDisabledModel(t *testing.T) {
	db, cal := newQuoteFixture(t)
	at := shTime(2026, time.November, 2, 20, 0, 0)
	listing := Listing{
		SellerID:        10023,
		ChannelID:       1,
		Vendor:          SeedVendorDeepSeek,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
		Status:          ListingLive,
		KeyFingerprint:  "fp-disabled-quote",
		Version:         1,
	}
	require.NoError(t, db.Create(&listing).Error)
	disableListingModel(t, db, listing.ID, "deepseek-flash", 600)

	_, err := PreFreezeQuoteForListing(db, cal, listing, "deepseek-flash",
		1_000_000, 500_000, 0, 0, at)
	require.ErrorIs(t, err, ErrModelDisabled,
		"an in-flight selection that reaches a disabled model must not be priced at the base multiplier")
}

func TestPublicStatsSkipDisabledModel(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	listing := addSelectorListing(t, db, "fp-stats-disabled", nil)
	disableListingModel(t, db, listing.ID, "deepseek-flash", 600)

	stats, err := computePublicStats(db, time.Now())
	require.NoError(t, err)
	assert.EqualValues(t, 0, stats.ModelsOnSale, "a disabled model is not on sale")
	assert.EqualValues(t, 0, stats.MinMultiplierMilli)

	require.NoError(t, db.Model(&ListingModel{}).
		Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").
		Update("enabled", true).Error)
	stats, err = computePublicStats(db, time.Now())
	require.NoError(t, err)
	assert.EqualValues(t, 1, stats.ModelsOnSale)
	assert.EqualValues(t, 600, stats.MinMultiplierMilli,
		"the public floor must use the enabled per-model multiplier, not the listing base")
}

func TestBuyerModelVisibilitySkipsDisabledModel(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	listing := addSelectorListing(t, db, "fp-visibility-disabled", nil)
	disableListingModel(t, db, listing.ID, "deepseek-flash", 600)

	state := loadModelVisibilityState(db)
	assert.True(t, state.owned["deepseek-flash"])
	assert.False(t, state.available["deepseek-flash"],
		"/v1/models must hide a model whose only supply disabled it")

	require.NoError(t, db.Model(&ListingModel{}).
		Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").
		Update("enabled", true).Error)
	resetBuyerModelVisibilityCache()
	state = loadModelVisibilityState(db)
	assert.True(t, state.available["deepseek-flash"])
}

func TestFilterBuyerModels(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	listing := addSelectorListing(t, db, "fp-filter-buyer", nil)
	disableListingModel(t, db, listing.ID, "deepseek-flash", 600)

	relayStateMu.Lock()
	oldDB, oldEnabled := relayDB, enabled
	relayDB, enabled = db, true
	relayStateMu.Unlock()
	t.Cleanup(func() {
		relayStateMu.Lock()
		relayDB, enabled = oldDB, oldEnabled
		relayStateMu.Unlock()
		resetBuyerModelVisibilityCache()
	})
	resetBuyerModelVisibilityCache()

	got := FilterBuyerModels([]string{"deepseek-flash", "not-a-marketplace-model"})
	assert.Equal(t, []string{"not-a-marketplace-model"}, got,
		"an owned-but-disabled model disappears; a model the marketplace does not own is left alone")

	require.NoError(t, db.Model(&ListingModel{}).
		Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").
		Update("enabled", true).Error)
	resetBuyerModelVisibilityCache()
	got = FilterBuyerModels([]string{"deepseek-flash", "not-a-marketplace-model"})
	assert.ElementsMatch(t, []string{"deepseek-flash", "not-a-marketplace-model"}, got)
}
