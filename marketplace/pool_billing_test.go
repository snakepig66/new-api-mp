package marketplace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPoolAliasRouting covers the acceptance case: glm5.3, GLM-5.3 and
// zhipu/glm-5.3 all resolve to the standard id glm-5.3.
func TestPoolAliasRouting(t *testing.T) {
	db := newListingTestDB(t)
	for _, input := range []string{"glm5.3", "GLM-5.3", "zhipu/glm-5.3", "glm-5.3", "  glm-5.3  "} {
		standard, ok := ModelPoolResolveAlias(db, input)
		require.Truef(t, ok, "%q must resolve", input)
		assert.Equalf(t, "glm-5.3", standard, "%q must normalize to the standard id", input)
	}
	_, ok := ModelPoolResolveAlias(db, "no-such-model")
	assert.False(t, ok)
}

// TestQuoteCoefficient: a plan channel priced at 0.5× official charges half,
// while the official cost (used for "saved") stays at the official price.
func TestQuoteCoefficient(t *testing.T) {
	db := newListingTestDB(t)
	cal, err := LoadCalendar(db)
	require.NoError(t, err)
	at := shTime(2026, time.January, 5, 10, 0, 0) // Monday 10:00 = peak

	base, err := Quote(db, cal, QuoteInput{
		Model: "deepseek-flash", Vendor: "deepseek", OriginVendor: "deepseek",
		CoefficientMilli: 1000, MultiplierMilli: 1000, At: at,
		InputTokens: 1_000_000,
	})
	require.NoError(t, err)

	half, err := Quote(db, cal, QuoteInput{
		Model: "deepseek-flash", Vendor: "volcengine-ark-coding-plan", OriginVendor: "deepseek",
		CoefficientMilli: 500, MultiplierMilli: 1000, At: at,
		InputTokens: 1_000_000,
	})
	require.NoError(t, err)

	// deepseek-flash peak input = 2,000,000 微元 / 1M tokens.
	assert.Equal(t, Micro(2_000_000), base.TokenDebit)
	assert.Equal(t, Micro(1_000_000), half.TokenDebit, "0.5 coefficient halves the debit")
	assert.Equal(t, base.OfficialCost, half.OfficialCost, "official cost ignores the coefficient")
}

// TestSelectorRequiresVerifiedChannelModel: an unverified qualification never
// gets traffic, and verifying it makes the listing selectable.
func TestSelectorRequiresVerifiedChannelModel(t *testing.T) {
	db := newListingTestDB(t)
	listing := createTestListing(t, db, 10023, uniqueTestKey("sk-selector-verified"))
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", listing.ID).Update("status", ListingLive).Error)
	require.NoError(t, db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", "deepseek", "deepseek-flash").
		Update("verified", false).Error)

	selector := NewSelector()
	_, _, err := selector.SelectWithProbe(db, SelectInput{Model: "deepseek-flash"})
	assert.ErrorIs(t, err, ErrNoListingAvailable, "unverified channel model must not be selected")

	require.NoError(t, db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", "deepseek", "deepseek-flash").
		Update("verified", true).Error)
	got, _, err := NewSelector().SelectWithProbe(db, SelectInput{Model: "deepseek-flash"})
	require.NoError(t, err)
	assert.Equal(t, listing.ID, got.ID)
}

// TestSelectorOfficialOnly: a verified plan-channel listing is excluded when
// the buyer key offers only official channels.
func TestSelectorOfficialOnly(t *testing.T) {
	db := newListingTestDB(t)
	// Verify the plan qualifications used by the test listings.
	require.NoError(t, db.Model(&ChannelModel{}).
		Where("channel_slug = ?", "volcengine-ark-coding-plan").
		Update("verified", true).Error)

	official := createTestListing(t, db, 10023, uniqueTestKey("sk-selector-official"))
	plan, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        "volcengine-ark-coding-plan",
		APIKey:          uniqueTestKey("sk-selector-plan"),
		Models:          []string{"deepseek-v4-pro"},
		MultiplierMilli: 1, // cheapest, would win without the switch
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&Listing{}).Where("id IN ?", []int64{official.ID, plan.ID}).
		Update("status", ListingLive).Error)

	all, _, err := NewSelector().SelectWithProbe(db, SelectInput{Model: "deepseek-flash"})
	require.NoError(t, err)
	assert.Equal(t, official.ID, all.ID)

	// The plan listing serves deepseek-v4-pro only; use that model for the
	// official-only assertion with a second official listing.
	officialPro, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        "deepseek",
		APIKey:          uniqueTestKey("sk-selector-official-pro"),
		Models:          []string{"deepseek-v4-pro"},
		MultiplierMilli: 10,
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", officialPro.ID).Update("status", ListingLive).Error)

	got, _, err := NewSelector().SelectWithProbe(db, SelectInput{Model: "deepseek-v4-pro", OfficialOnly: true})
	require.NoError(t, err)
	assert.Equal(t, officialPro.ID, got.ID, "official-only must skip the cheaper plan listing")

	_, _, err = NewSelector().SelectWithProbe(db, SelectInput{
		Model:             "deepseek-v4-pro",
		OfficialOnly:      true,
		ExcludeListingIDs: []int64{official.ID, officialPro.ID},
	})
	assert.ErrorIs(t, err, ErrNoListingAvailable, "no official supply left after excluding them")
}
