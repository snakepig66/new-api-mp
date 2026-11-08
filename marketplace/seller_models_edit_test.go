package marketplace

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Adding a model mid-life: it is listed at once but stays off (pending) until
// its verification passes; the seller cannot switch it on early; a failure
// keeps it off with the reason.
func TestAddListingModelsVerifiedBeforeTraffic(t *testing.T) {
	db := newListingTestDB(t)
	l := createTestListing(t, db, 7001, "sk-add-models-0000000000000001")

	added, err := AddListingModels(db, l.ID, 7001, []sellerModelConfig{
		{Model: "kimi-k2.6", UpstreamModel: "moonshot/kimi-k2.6", MultiplierMilli: 400},
		{Model: "deepseek-flash"}, // already listed: ignored
	}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, []string{"kimi-k2.6"}, added)

	got, err := GetListing(db, l.ID)
	require.NoError(t, err)
	assert.Contains(t, got.Models, "kimi-k2.6")

	var row ListingModel
	require.NoError(t, db.Where("listing_id = ? AND model = ?", l.ID, "kimi-k2.6").Take(&row).Error)
	assert.False(t, row.Enabled, "not routable before verification")
	assert.Equal(t, ListingModelVerifyPending, row.VerifyState)
	assert.EqualValues(t, 400, row.MultiplierMilli)
	assert.Equal(t, "moonshot/kimi-k2.6", ListingUpstreamModel(db, l.ID, l.Vendor, "kimi-k2.6"))
	assert.True(t, listingModelDisabled(db, l.ID, "kimi-k2.6"), "selector skips it")

	on := true
	_, err = SetListingModelsState(db, l.ID, 7001, []string{"kimi-k2.6"}, ModelStateInput{Enabled: &on}, time.Now())
	var batch *BatchModelChangeError
	require.True(t, errors.As(err, &batch), "enabling a pending model is refused: %v", err)

	markListingModelVerified(db, l.ID, "kimi-k2.6", true, "")
	require.NoError(t, db.Where("listing_id = ? AND model = ?", l.ID, "kimi-k2.6").Take(&row).Error)
	assert.True(t, row.Enabled)
	assert.Empty(t, row.VerifyState)
	assert.False(t, listingModelDisabled(db, l.ID, "kimi-k2.6"))

	// A model outside the priced pool is refused.
	_, err = AddListingModels(db, l.ID, 7001, []sellerModelConfig{{Model: "made-up-model"}}, time.Now())
	require.Error(t, err)
}

func TestAddListingModelFailedVerificationStaysOff(t *testing.T) {
	db := newListingTestDB(t)
	l := createTestListing(t, db, 7002, "sk-add-models-0000000000000002")
	_, err := AddListingModels(db, l.ID, 7002, []sellerModelConfig{{Model: "kimi-k2.6"}}, time.Now())
	require.NoError(t, err)
	markListingModelVerified(db, l.ID, "kimi-k2.6", false, "这个 Key 无权调用该模型（HTTP 403）")
	var row ListingModel
	require.NoError(t, db.Where("listing_id = ? AND model = ?", l.ID, "kimi-k2.6").Take(&row).Error)
	assert.False(t, row.Enabled)
	assert.Equal(t, ListingModelVerifyFailed, row.VerifyState)
	assert.Contains(t, row.DisabledDetail, "403")
}

// Removing a model only affects new requests: it leaves Listing.Models and its
// row at once; the last model cannot be removed.
func TestRemoveListingModel(t *testing.T) {
	db := newListingTestDB(t)
	l := createTestListing(t, db, 7003, "sk-add-models-0000000000000003")

	require.NoError(t, RemoveListingModel(db, l.ID, 7003, "deepseek-v4-pro"))
	got, err := GetListing(db, l.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"deepseek-flash"}, []string(got.Models))
	var n int64
	require.NoError(t, db.Model(&ListingModel{}).Where("listing_id = ? AND model = ?", l.ID, "deepseek-v4-pro").Count(&n).Error)
	assert.Zero(t, n)

	assert.ErrorIs(t, RemoveListingModel(db, l.ID, 7003, "deepseek-flash"), ErrLastListingModel)
	assert.ErrorIs(t, RemoveListingModel(db, l.ID, 7003, "nope"), ErrListingNotFound)

	// Re-adding a removed model goes through verification again.
	added, err := AddListingModels(db, l.ID, 7003, []sellerModelConfig{{Model: "deepseek-v4-pro"}}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, []string{"deepseek-v4-pro"}, added)
	assert.True(t, listingModelDisabled(db, l.ID, "deepseek-v4-pro"))
}

// Price is locked at pre-freeze: a multiplier change after the freeze does not
// change the frozen quote's multiplier.
func TestPriceLockedAtFreeze(t *testing.T) {
	db := newListingTestDB(t)
	cal, err := LoadCalendar(db)
	require.NoError(t, err)
	l := createTestListing(t, db, 7004, "sk-add-models-0000000000000004")
	at := time.Now()
	frozen, err := PreFreezeQuoteForListing(db, cal, l, "deepseek-flash", 1000, 0, 0, 0, at)
	require.NoError(t, err)
	require.NoError(t, db.Model(&ListingModel{}).Where("listing_id = ? AND model = ?", l.ID, "deepseek-flash").
		Update("multiplier_milli", 900).Error)
	after, err := PreFreezeQuoteForListing(db, cal, l, "deepseek-flash", 1000, 0, 0, 0, at)
	require.NoError(t, err)
	assert.EqualValues(t, 300, frozen.ModelMultiplierMilli, "the in-flight request keeps the frozen multiplier")
	assert.EqualValues(t, 900, after.ModelMultiplierMilli, "a new request sees the new multiplier")
}
