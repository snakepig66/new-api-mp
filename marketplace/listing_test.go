package marketplace

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// listingTestKey is the plaintext API key used across this file. Every error
// assertion also checks the message never contains it (§2.3).
const listingTestKey = "sk-test-secret-key-12345"

// newListingTestDB builds the full schema a listing needs: the marketplace
// tables plus the upstream channels table, and a configured MP_KEY_SECRET.
// The secret is a package global, so these tests must not run in parallel.
func newListingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	preserveAnonSecret(t)
	SetAnonSecret("test-secret")
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	return db
}

func createTestListing(t *testing.T, db *gorm.DB, sellerID int64, key string) Listing {
	t.Helper()
	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        sellerID,
		VendorID:        "deepseek",
		APIKey:          key,
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	return listing
}

// assertNoKeyLeak is the §2.3 guard: no error text may carry the key
// plaintext or the full fingerprint (the 8-char prefix is allowed).
func assertNoKeyLeak(t *testing.T, err error, keys ...string) {
	t.Helper()
	require.Error(t, err)
	for _, k := range keys {
		assert.NotContains(t, err.Error(), k)
	}
}

func TestCreateListingSuccess(t *testing.T) {
	db := newListingTestDB(t)

	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        "deepseek",
		APIKey:          listingTestKey,
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		MultiplierMilli: 300,
		Concurrency:     0, // default
		RPM:             60,
		DailyCapMicro:   5_000_000,
		Hours:           `[{"start":"09:00","end":"18:00"}]`,
	})
	require.NoError(t, err)

	// Listing row.
	assert.Positive(t, listing.ID)
	assert.Equal(t, int64(10023), listing.SellerID)
	assert.Equal(t, "deepseek", listing.Vendor)
	assert.Equal(t, []string{"deepseek-flash", "deepseek-v4-pro"}, listing.Models)
	assert.Equal(t, int64(300), listing.MultiplierMilli)
	assert.Equal(t, defaultListingConcurrency, listing.ConcurrencyLimit, "unset concurrency defaults to 5 (§3.5)")
	assert.Equal(t, 60, listing.RPMLimit)
	assert.Equal(t, int64(5_000_000), listing.DailyCapMicro)
	assert.Equal(t, `[{"start":"09:00","end":"18:00"}]`, listing.Hours)
	assert.Equal(t, ListingDraft, listing.Status)
	assert.Equal(t, int64(1), listing.Version)

	// Key material: ciphertext stored, plaintext nowhere.
	require.NotEmpty(t, listing.KeyCiphertext)
	assert.NotContains(t, string(listing.KeyCiphertext), listingTestKey)
	fp, err := ListingKeyFingerprint(listingTestKey)
	require.NoError(t, err)
	assert.Equal(t, fp, listing.KeyFingerprint)
	back, err := DecryptListingKey(listing.KeyCiphertext)
	require.NoError(t, err)
	assert.Equal(t, listingTestKey, back, "the stored blob must decrypt back to the key")

	// Upstream channel row: created disabled, holding only the placeholder.
	var ch model.Channel
	require.NoError(t, db.First(&ch, listing.ChannelID).Error)
	assert.Equal(t, constant.ChannelTypeDeepSeek, ch.Type)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, ch.Status, "a draft listing's channel stays disabled")
	assert.Equal(t, "mp-managed:"+fp[:8], ch.Key)
	assert.NotContains(t, ch.Key, listingTestKey, "the channel row must not hold the real key")
	assert.Equal(t, "mp:deepseek:10023", ch.Name)
	require.NotNil(t, ch.BaseURL)
	assert.Equal(t, "https://api.deepseek.com", *ch.BaseURL)
	assert.Equal(t, "deepseek-flash,deepseek-v4-pro", ch.Models, "channel Models mirror the listing's models")
	assert.Equal(t, "default", ch.Group)
}

func TestCreateListingDefaultsAndDedup(t *testing.T) {
	db := newListingTestDB(t)

	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        "deepseek",
		APIKey:          listingTestKey,
		Models:          []string{"deepseek-flash", "deepseek-flash"},
		MultiplierMilli: 1000,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"deepseek-flash"}, listing.Models, "duplicate models collapse")
	assert.Equal(t, defaultListingConcurrency, listing.ConcurrencyLimit)
	assert.Equal(t, 0, listing.RPMLimit, "0 rpm = unlimited")
	assert.Equal(t, int64(0), listing.DailyCapMicro, "0 cap = unlimited")
	assert.Empty(t, listing.Hours, "empty hours = all day")
}

func TestCreateListingDuplicateKey(t *testing.T) {
	db := newListingTestDB(t)

	first := createTestListing(t, db, 10023, listingTestKey)
	require.Positive(t, first.ID)

	// Same seller, same key.
	_, err := CreateListing(db, CreateListingInput{
		SellerID: 10023, VendorID: "deepseek", APIKey: listingTestKey,
		Models: []string{"deepseek-flash"}, MultiplierMilli: 300,
	})
	assert.ErrorIs(t, err, ErrDuplicateKey, "one key can only be listed once platform-wide (§3.5)")
	assertNoKeyLeak(t, err, listingTestKey, first.KeyFingerprint)

	// A different seller with the same key is rejected too.
	_, err = CreateListing(db, CreateListingInput{
		SellerID: 10024, VendorID: "deepseek", APIKey: listingTestKey,
		Models: []string{"deepseek-flash"}, MultiplierMilli: 300,
	})
	assert.ErrorIs(t, err, ErrDuplicateKey)
	assertNoKeyLeak(t, err, listingTestKey, first.KeyFingerprint)

	// A genuinely different key still goes through.
	second := createTestListing(t, db, 10024, "sk-test-other-key-67890")
	assert.NotEqual(t, first.KeyFingerprint, second.KeyFingerprint)
}

func TestCreateListingValidation(t *testing.T) {
	db := newListingTestDB(t)

	base := CreateListingInput{
		SellerID: 10023, VendorID: "deepseek", APIKey: listingTestKey,
		Models: []string{"deepseek-flash"}, MultiplierMilli: 300,
	}

	in := base
	in.VendorID = "openai"
	_, err := CreateListing(db, in)
	assert.ErrorIs(t, err, ErrVendorNotWhitelisted)

	in = base
	in.Models = []string{"deepseek-flash", "gpt-4"}
	_, err = CreateListing(db, in)
	require.Error(t, err, "models must stay within the vendor's list")
	assert.Contains(t, err.Error(), "gpt-4")
	assertNoKeyLeak(t, err, listingTestKey)

	in = base
	in.Models = nil
	_, err = CreateListing(db, in)
	assert.Error(t, err, "at least one model is required")

	for _, milli := range []int64{0, -1, 1001} {
		in = base
		in.MultiplierMilli = milli
		_, err = CreateListing(db, in)
		assert.Errorf(t, err, "multiplier %d is outside 1..1000", milli)
		assertNoKeyLeak(t, err, listingTestKey)
	}

	in = base
	in.Hours = `[{"start":"25:00","end":"26:00"}]`
	_, err = CreateListing(db, in)
	assert.Error(t, err, "invalid hours are rejected before anything is written")

	in = base
	in.APIKey = "  "
	_, err = CreateListing(db, in)
	assert.Error(t, err, "an empty key is rejected")

	in = base
	in.SellerID = 0
	_, err = CreateListing(db, in)
	assert.Error(t, err)

	// Nothing may have been written by the failing attempts.
	var cnt int64
	require.NoError(t, db.Model(&Listing{}).Count(&cnt).Error)
	assert.Zero(t, cnt)
	require.NoError(t, db.Model(&model.Channel{}).Count(&cnt).Error)
	assert.Zero(t, cnt, "failed creates must roll back the channel row too")
}

func TestListingStateMachine(t *testing.T) {
	db := newListingTestDB(t)
	listing := createTestListing(t, db, 10023, listingTestKey)

	status := func() string {
		row, err := GetListing(db, listing.ID)
		require.NoError(t, err)
		return row.Status
	}
	require.Equal(t, ListingDraft, status())

	// The full legal chain: draft → verifying → canary → live ⇄ paused → banned.
	require.NoError(t, StartVerification(db, listing.ID))
	assert.Equal(t, ListingVerifying, status())
	require.NoError(t, PassVerification(db, listing.ID))
	assert.Equal(t, ListingCanary, status())
	require.NoError(t, GoLive(db, listing.ID))
	assert.Equal(t, ListingLive, status())
	require.NoError(t, PauseListing(db, listing.ID))
	assert.Equal(t, ListingPaused, status())
	require.NoError(t, ResumeListing(db, listing.ID))
	assert.Equal(t, ListingLive, status())
	require.NoError(t, BanListing(db, listing.ID))
	assert.Equal(t, ListingBanned, status())
}

func TestTransitionIllegal(t *testing.T) {
	db := newListingTestDB(t)

	// draft → live skips verification and canary.
	draft := createTestListing(t, db, 10023, listingTestKey)
	err := GoLive(db, draft.ID)
	assert.ErrorIs(t, err, ErrInvalidTransition)

	// paused → verifying is not an edge.
	paused := createTestListing(t, db, 10023, "sk-test-key-2")
	require.NoError(t, StartVerification(db, paused.ID))
	require.NoError(t, PassVerification(db, paused.ID))
	require.NoError(t, GoLive(db, paused.ID))
	require.NoError(t, PauseListing(db, paused.ID))
	err = StartVerification(db, paused.ID)
	assert.ErrorIs(t, err, ErrInvalidTransition)

	// banned is terminal: nothing leaves it.
	banned := createTestListing(t, db, 10023, "sk-test-key-3")
	require.NoError(t, StartVerification(db, banned.ID))
	require.NoError(t, BanListing(db, banned.ID)) // verifying → banned is legal
	err = ResumeListing(db, banned.ID)
	assert.ErrorIs(t, err, ErrInvalidTransition)
	err = BanListing(db, banned.ID)
	assert.ErrorIs(t, err, ErrInvalidTransition, "banning twice is not a legal edge")

	// canary → banned is legal (§3.5 lists canary among ban sources).
	canary := createTestListing(t, db, 10023, "sk-test-key-4")
	require.NoError(t, StartVerification(db, canary.ID))
	require.NoError(t, PassVerification(db, canary.ID))
	require.NoError(t, BanListing(db, canary.ID))

	// Unknown id.
	err = StartVerification(db, 999999)
	assert.ErrorIs(t, err, ErrListingNotFound)
}

func TestUpdateMultiplier(t *testing.T) {
	db := newListingTestDB(t)
	listing := createTestListing(t, db, 10023, listingTestKey)
	t0 := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

	// First change always allowed (MultiplierUpdatedAt is nil); no re-verify.
	require.NoError(t, UpdateMultiplier(db, listing.ID, 500, t0))
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(500), row.MultiplierMilli)
	require.NotNil(t, row.MultiplierUpdatedAt)
	assert.WithinDuration(t, t0, *row.MultiplierUpdatedAt, time.Second)
	assert.Equal(t, int64(2), row.Version, "multiplier change bumps the optimistic-lock version")
	assert.Equal(t, ListingDraft, row.Status, "changing the multiplier never re-verifies (§3.5)")

	// Inside the 10-minute cooldown.
	err = UpdateMultiplier(db, listing.ID, 600, t0.Add(5*time.Minute))
	assert.ErrorIs(t, err, ErrMultiplierTooSoon)

	// At the boundary it succeeds again.
	require.NoError(t, UpdateMultiplier(db, listing.ID, 600, t0.Add(10*time.Minute)))
	row, err = GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(600), row.MultiplierMilli)
	assert.WithinDuration(t, t0.Add(10*time.Minute), *row.MultiplierUpdatedAt, time.Second)
	assert.Equal(t, int64(3), row.Version)

	// Range check and missing listing.
	assert.Error(t, UpdateMultiplier(db, listing.ID, 0, t0.Add(time.Hour)))
	assert.Error(t, UpdateMultiplier(db, listing.ID, 1001, t0.Add(time.Hour)))
	assert.ErrorIs(t, UpdateMultiplier(db, 999999, 500, t0.Add(time.Hour)), ErrListingNotFound)
}

func TestReplaceKey(t *testing.T) {
	db := newListingTestDB(t)
	const newKey = "sk-test-replacement-key-99999"

	listing := createTestListing(t, db, 10023, listingTestKey)
	oldCiphertext := append([]byte{}, listing.KeyCiphertext...)

	require.NoError(t, ReplaceKey(db, listing.ID, newKey))
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingVerifying, row.Status, "a key change must re-trigger verification (§3.5)")
	assert.NotEqual(t, oldCiphertext, row.KeyCiphertext)
	assert.NotEqual(t, listing.KeyFingerprint, row.KeyFingerprint)
	assert.Equal(t, int64(2), row.Version)
	back, err := DecryptListingKey(row.KeyCiphertext)
	require.NoError(t, err)
	assert.Equal(t, newKey, back)

	// Swapping to a key another seller already listed is rejected, and the
	// error leaks neither the key nor the full fingerprint.
	other := createTestListing(t, db, 10024, "sk-test-other-seller-key")
	err = ReplaceKey(db, listing.ID, "sk-test-other-seller-key")
	assert.ErrorIs(t, err, ErrDuplicateKey)
	assertNoKeyLeak(t, err, "sk-test-other-seller-key", other.KeyFingerprint)
	row, err = GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, newKey, mustDecrypt(t, row), "a rejected replacement leaves the old key untouched")

	// Banned listings are terminal: no key swap brings them back.
	banned := createTestListing(t, db, 10023, "sk-test-key-to-ban")
	require.NoError(t, StartVerification(db, banned.ID))
	require.NoError(t, BanListing(db, banned.ID))
	err = ReplaceKey(db, banned.ID, "sk-yet-another-key-1")
	assert.ErrorIs(t, err, ErrInvalidTransition)

	assert.Error(t, ReplaceKey(db, listing.ID, "  "), "empty replacement key")
	assert.ErrorIs(t, ReplaceKey(db, 999999, newKey), ErrListingNotFound)
}

func mustDecrypt(t *testing.T, row Listing) string {
	t.Helper()
	plain, err := DecryptListingKey(row.KeyCiphertext)
	require.NoError(t, err)
	return plain
}

func TestHoursValidation(t *testing.T) {
	assert.NoError(t, ValidateHours(""), "empty = all day")
	assert.NoError(t, ValidateHours("  "))
	assert.NoError(t, ValidateHours(`[{"start":"09:00","end":"18:00"}]`))
	assert.NoError(t, ValidateHours(`[{"start":"00:00","end":"06:30"},{"start":"12:00","end":"23:59"}]`))

	assert.Error(t, ValidateHours("not json"))
	assert.Error(t, ValidateHours(`{"start":"09:00","end":"18:00"}`), "must be an array")
	assert.Error(t, ValidateHours(`[]`), "an explicit empty array is not all day")
	assert.Error(t, ValidateHours(`[{"start":"9:00","end":"18:00"}]`), "HH:MM must be zero-padded")
	assert.Error(t, ValidateHours(`[{"start":"25:00","end":"26:00"}]`), "24h clock")
	assert.Error(t, ValidateHours(`[{"start":"18:00","end":"09:00"}]`), "start must precede end")
	assert.Error(t, ValidateHours(`[{"start":"09:00","end":"09:00"}]`))
	assert.Error(t, ValidateHours(`[{"start":"09:00"}]`), "end is required")
}

func TestListAndGetListings(t *testing.T) {
	db := newListingTestDB(t)

	a := createTestListing(t, db, 10023, listingTestKey)
	b := createTestListing(t, db, 10023, "sk-test-key-b")
	createTestListing(t, db, 10024, "sk-test-key-c")

	rows, err := ListListingsBySeller(db, 10023)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, a.ID, rows[0].ID)
	assert.Equal(t, b.ID, rows[1].ID)

	rows, err = ListListingsBySeller(db, 99999)
	require.NoError(t, err)
	assert.Empty(t, rows)

	got, err := GetListing(db, a.ID)
	require.NoError(t, err)
	assert.Equal(t, a.KeyFingerprint, got.KeyFingerprint)
	_, err = GetListing(db, 999999)
	assert.ErrorIs(t, err, ErrListingNotFound)

	// Ownership check: another seller's listing is indistinguishable from
	// a missing one.
	got, err = GetListingForSeller(db, a.ID, 10023)
	require.NoError(t, err)
	assert.Equal(t, a.ID, got.ID)
	_, err = GetListingForSeller(db, a.ID, 10024)
	assert.ErrorIs(t, err, ErrListingNotFound)
}

func TestListingErrorsNeverContainKeyMaterial(t *testing.T) {
	db := newListingTestDB(t)
	listing := createTestListing(t, db, 10023, listingTestKey)

	_, err := CreateListing(db, CreateListingInput{
		SellerID: 10024, VendorID: "deepseek", APIKey: listingTestKey,
		Models: []string{"deepseek-flash"}, MultiplierMilli: 300,
	})
	require.ErrorIs(t, err, ErrDuplicateKey)
	assertNoKeyLeak(t, err, listingTestKey, listing.KeyFingerprint)

	err = ReplaceKey(db, listing.ID, listingTestKey+"x")
	require.NoError(t, err)
	_, err = CreateListing(db, CreateListingInput{
		SellerID: 10024, VendorID: "deepseek", APIKey: listingTestKey + "x",
		Models: []string{"deepseek-flash"}, MultiplierMilli: 300,
	})
	require.ErrorIs(t, err, ErrDuplicateKey)
	assert.False(t, strings.Contains(err.Error(), listingTestKey+"x"), "§2.3: no key material in errors")
}
