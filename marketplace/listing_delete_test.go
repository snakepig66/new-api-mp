package marketplace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteListingSoftAndReadd(t *testing.T) {
	db := newListingTestDB(t)
	key := uniqueTestKey("sk-soft-delete")
	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	require.NotEmpty(t, listing.KeyCiphertext)

	now := time.Now()
	deleted, err := DeleteListingForSeller(db, listing.ID, listing.SellerID, now)
	require.NoError(t, err)
	assert.Equal(t, ListingDeleted, deleted.Status)
	assert.Equal(t, listing.KeyFingerprint, deleted.DeletedFingerprint,
		"the original identity moves to the deleted column")
	assert.Equal(t, "deleted:"+itoa(listing.ID), deleted.KeyFingerprint,
		"the unique column is freed with a per-row tombstone")
	assert.False(t, deleted.DeletedBanned)
	assert.NotEmpty(t, deleted.KeyCiphertext, "the key survives the grace period")
	require.NotNil(t, deleted.KeyWipeAt)

	rows, err := ListListingsBySeller(db, listing.SellerID)
	require.NoError(t, err)
	for _, row := range rows {
		assert.NotEqual(t, listing.ID, row.ID, "a deleted listing is not listed")
	}

	// A normal delete releases the key: the same key may be listed again.
	readded, err := CreateListing(db, CreateListingInput{
		SellerID:        listing.SellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	assert.NotEqual(t, listing.ID, readded.ID)

	// The delayed job wipes the ciphertext only after the grace period.
	n, err := WipeDeletedListingKeys(db, now.Add(10*time.Minute))
	require.NoError(t, err)
	assert.Zero(t, n, "nothing is wiped before key_wipe_at")
	n, err = WipeDeletedListingKeys(db, now.Add(16*time.Minute))
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	old, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Empty(t, old.KeyCiphertext)

	// Deleting again is idempotent.
	again, err := DeleteListingForSeller(db, listing.ID, listing.SellerID, now.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, ListingDeleted, again.Status)
}

func TestDeleteBannedKeyCannotBeReadded(t *testing.T) {
	db := newListingTestDB(t)
	key := uniqueTestKey("sk-banned-delete")
	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", listing.ID).
		Update("status", ListingLive).Error)
	require.NoError(t, BanListing(db, listing.ID))

	deleted, err := DeleteListingForSeller(db, listing.ID, listing.SellerID, time.Now())
	require.NoError(t, err)
	assert.True(t, deleted.DeletedBanned, "the ban follows the fingerprint")

	_, err = CreateListing(db, CreateListingInput{
		SellerID:        listing.SellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.ErrorIs(t, err, ErrKeyBannedDeleted)
}

func TestUpdateListingSettings(t *testing.T) {
	db := newListingTestDB(t)
	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-settings"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)

	label := "主渠道"
	concurrency := 9
	rpm := 120
	capMicro := int64(5_000_000)
	hours := `[{"start":"09:00","end":"18:00"}]`
	updated, err := UpdateListingSettings(db, listing.ID, listing.SellerID, UpdateListingSettingsInput{
		Label:            &label,
		ConcurrencyLimit: &concurrency,
		RPMLimit:         &rpm,
		DailyCapMicro:    &capMicro,
		Hours:            &hours,
	}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, label, updated.Label)
	assert.Equal(t, concurrency, updated.ConcurrencyLimit)
	assert.Equal(t, rpm, updated.RPMLimit)
	assert.Equal(t, capMicro, updated.DailyCapMicro)
	assert.Equal(t, hours, updated.Hours)

	// The seller list view carries the label.
	view := toSellerListingView(&updated)
	assert.Equal(t, label, view.Label)

	// Validation is fail-closed.
	badHours := `[{"start":"18:00","end":"09:00"}]`
	_, err = UpdateListingSettings(db, listing.ID, listing.SellerID, UpdateListingSettingsInput{Hours: &badHours}, time.Now())
	require.Error(t, err)
	badConcurrency := 0
	_, err = UpdateListingSettings(db, listing.ID, listing.SellerID, UpdateListingSettingsInput{ConcurrencyLimit: &badConcurrency}, time.Now())
	require.Error(t, err)

	// A foreign seller gets the same 404 as an unknown id.
	_, err = UpdateListingSettings(db, listing.ID, 99999, UpdateListingSettingsInput{Label: &label}, time.Now())
	require.ErrorIs(t, err, ErrListingNotFound)

	var audits int64
	require.NoError(t, db.Model(&ListingAudit{}).Where("listing_id = ?", listing.ID).Count(&audits).Error)
	assert.GreaterOrEqual(t, audits, int64(1))
}

// itoa is a tiny dependency-free int-to-decimal helper for the tombstone
// assertion; strconv would be an extra import in this file only.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
