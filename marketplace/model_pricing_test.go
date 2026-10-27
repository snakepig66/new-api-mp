package marketplace

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func pricingFixture(t *testing.T, baseMilli int64) (*gorm.DB, Listing) {
	t.Helper()
	db := newListingTestDB(t)
	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-pricing"),
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		MultiplierMilli: baseMilli,
	})
	require.NoError(t, err)
	return db, listing
}

func int64Ptr(v int64) *int64 { return &v }
func boolPtr(v bool) *bool    { return &v }

func TestMultiplierBoundsRejectBelowCost(t *testing.T) {
	db := newListingTestDB(t)
	require.Error(t, validateMultiplierForVendor(db, SeedVendorDeepSeek, MultiplierOfficialMinMilli-1))
	require.NoError(t, validateMultiplierForVendor(db, SeedVendorDeepSeek, MultiplierOfficialMinMilli))
	min, max := MultiplierBoundsForTier(ChannelKindThirdParty)
	assert.EqualValues(t, MultiplierThirdPartyMinMilli, min)
	assert.EqualValues(t, MultiplierAbsoluteMaxMilli, max)
	require.Error(t, validateMultiplierForVendor(db, SeedVendorDeepSeek, MultiplierAbsoluteMinMilli-1))
}

func TestModelPriceDecreaseIsImmediate(t *testing.T) {
	db, listing := pricingFixture(t, 600)
	rows, err := SetListingModelsState(db, listing.ID, listing.SellerID,
		[]string{"deepseek-flash"}, ModelStateInput{MultiplierMilli: int64Ptr(400)}, time.Now())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(400), rows[0].MultiplierMilli)
	assert.Zero(t, rows[0].PendingMultiplierMilli)
}

func TestModelPriceIncreaseIsStagedAndPromoted(t *testing.T) {
	db, listing := pricingFixture(t, 400)
	now := time.Now()
	rows, err := SetListingModelsState(db, listing.ID, listing.SellerID,
		[]string{"deepseek-flash"}, ModelStateInput{MultiplierMilli: int64Ptr(600)}, now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(400), rows[0].MultiplierMilli, "the current price must not change yet")
	assert.Equal(t, int64(600), rows[0].PendingMultiplierMilli)
	require.NotNil(t, rows[0].PendingEffectiveAt)

	// Another increase while one is pending is rejected.
	_, err = SetListingModelsState(db, listing.ID, listing.SellerID,
		[]string{"deepseek-flash"}, ModelStateInput{MultiplierMilli: int64Ptr(700)}, now)
	var rejected *BatchModelChangeError
	require.ErrorAs(t, err, &rejected)
	assert.Equal(t, "PRICE_PENDING", rejected.Items[0].Code)

	// A due pending value is honoured by the read path before the job runs.
	before := loadListingModelOverrides(db, []int64{listing.ID}, now)
	assert.EqualValues(t, 400, before.multiplierFor(listing.ID, "deepseek-flash", listing.MultiplierMilli))
	after := loadListingModelOverrides(db, []int64{listing.ID}, now.Add(2*time.Hour))
	assert.EqualValues(t, 600, after.multiplierFor(listing.ID, "deepseek-flash", listing.MultiplierMilli))

	n, err := PromotePendingModelMultipliers(db, now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	var row ListingModel
	require.NoError(t, db.Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").Take(&row).Error)
	assert.Equal(t, int64(600), row.MultiplierMilli)
	assert.Zero(t, row.PendingMultiplierMilli)

	// The same day cannot increase again.
	_, err = SetListingModelsState(db, listing.ID, listing.SellerID,
		[]string{"deepseek-flash"}, ModelStateInput{MultiplierMilli: int64Ptr(700)}, now.Add(3*time.Hour))
	require.ErrorAs(t, err, &rejected)
	assert.Equal(t, "PRICE_DAILY_LIMIT", rejected.Items[0].Code)
	require.NotNil(t, rejected.Items[0].EarliestChangeAt)
}

func TestModelPriceSameValueCancelsPending(t *testing.T) {
	db, listing := pricingFixture(t, 600)
	now := time.Now()
	_, err := SetListingModelsState(db, listing.ID, listing.SellerID,
		[]string{"deepseek-flash"}, ModelStateInput{MultiplierMilli: int64Ptr(700)}, now)
	require.NoError(t, err)

	rows, err := SetListingModelsState(db, listing.ID, listing.SellerID,
		[]string{"deepseek-flash"}, ModelStateInput{MultiplierMilli: int64Ptr(600)}, now)
	require.NoError(t, err)
	assert.Zero(t, rows[0].PendingMultiplierMilli)
	assert.Equal(t, int64(600), rows[0].MultiplierMilli)
}

func TestModelPriceDecreaseClearsPending(t *testing.T) {
	db, listing := pricingFixture(t, 600)
	now := time.Now()
	_, err := SetListingModelsState(db, listing.ID, listing.SellerID,
		[]string{"deepseek-flash"}, ModelStateInput{MultiplierMilli: int64Ptr(800)}, now)
	require.NoError(t, err)

	rows, err := SetListingModelsState(db, listing.ID, listing.SellerID,
		[]string{"deepseek-flash"}, ModelStateInput{MultiplierMilli: int64Ptr(500)}, now)
	require.NoError(t, err)
	assert.Equal(t, int64(500), rows[0].MultiplierMilli)
	assert.Zero(t, rows[0].PendingMultiplierMilli)
}

func TestModelPriceJumpIsRejected(t *testing.T) {
	db, listing := pricingFixture(t, 300)
	_, err := SetListingModelsState(db, listing.ID, listing.SellerID,
		[]string{"deepseek-flash"}, ModelStateInput{MultiplierMilli: int64Ptr(500)}, time.Now())
	var rejected *BatchModelChangeError
	require.ErrorAs(t, err, &rejected)
	assert.Equal(t, "PRICE_JUMP", rejected.Items[0].Code)
}

func TestModelPriceConcurrentIncreaseOnlyOneWins(t *testing.T) {
	db, listing := pricingFixture(t, 300)
	now := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, value := range []int64{400, 450} {
		wg.Add(1)
		go func(v int64) {
			defer wg.Done()
			_, err := SetListingModelsState(db, listing.ID, listing.SellerID,
				[]string{"deepseek-flash"}, ModelStateInput{MultiplierMilli: &v}, now)
			errs <- err
		}(value)
	}
	wg.Wait()
	close(errs)

	var successes, failures int
	for err := range errs {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	assert.Equal(t, 1, successes, "only one concurrent increase may be staged")
	assert.Equal(t, 1, failures)
}

func TestBatchModelChangeIsAllOrNothing(t *testing.T) {
	db, listing := pricingFixture(t, 300)
	now := time.Now()
	today := shanghaiDate(now)
	// Mark one model as already increased today.
	require.NoError(t, db.Model(&ListingModel{}).
		Where("listing_id = ? AND model = ?", listing.ID, "deepseek-v4-pro").
		Updates(map[string]any{"last_changed_date": today}).Error)

	_, err := SetListingModelsState(db, listing.ID, listing.SellerID, nil,
		ModelStateInput{MultiplierMilli: int64Ptr(400)}, now)
	var rejected *BatchModelChangeError
	require.ErrorAs(t, err, &rejected)
	assert.Len(t, rejected.Items, 1)

	var flash ListingModel
	require.NoError(t, db.Where("listing_id = ? AND model = ?", listing.ID, "deepseek-flash").Take(&flash).Error)
	assert.Equal(t, int64(300), flash.MultiplierMilli, "a rejected batch writes nothing")
	assert.Zero(t, flash.PendingMultiplierMilli)
}

func TestModelDisableClearsPendingAndHidesModel(t *testing.T) {
	db, listing := pricingFixture(t, 600)
	now := time.Now()
	_, err := SetListingModelsState(db, listing.ID, listing.SellerID,
		[]string{"deepseek-flash"}, ModelStateInput{MultiplierMilli: int64Ptr(700)}, now)
	require.NoError(t, err)

	rows, err := SetListingModelsState(db, listing.ID, listing.SellerID,
		[]string{"deepseek-flash"}, ModelStateInput{Enabled: boolPtr(false)}, now)
	require.NoError(t, err)
	assert.False(t, rows[0].Enabled)
	assert.Zero(t, rows[0].PendingMultiplierMilli)
	assert.Equal(t, "manual", rows[0].DisabledReason)

	overrides := loadListingModelOverrides(db, []int64{listing.ID}, now)
	assert.False(t, overrides.modelEnabled(listing.ID, "deepseek-flash"))
}
