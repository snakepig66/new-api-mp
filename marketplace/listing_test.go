package marketplace

import (
	"context"
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

// TestCreateListingWritesShadowTables proves CreateListing double-writes the
// P3 split tables in the same transaction: the seller channel keeps the
// listing id and every field is copied, and a failure anywhere would have
// rolled the whole create back (the model is the constraint enforced by the
// shadow insert).
func TestCreateListingWritesShadowTables(t *testing.T) {
	db := newListingTestDB(t)

	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        "deepseek",
		APIKey:          uniqueTestKey("sk-shadow"),
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		MultiplierMilli: 300,
		Concurrency:     9,
		RPM:             40,
		DailyCapMicro:   6_000_000,
		Hours:           `[{"start":"08:00","end":"20:00"}]`,
	})
	require.NoError(t, err)

	var channel SellerChannel
	require.NoError(t, db.Where("id = ?", listing.ID).Take(&channel).Error)
	assert.Equal(t, listing.ID, channel.ID, "shadow channel id equals the listing id")
	assert.Equal(t, listing.SellerID, channel.SellerID)
	assert.Equal(t, "https://api.deepseek.com", channel.Base)
	assert.Equal(t, KeyStrategyWeightedRoundRobin, channel.KeyStrategy)
	assert.Equal(t, listing.ConcurrencyLimit, channel.ConcurrencyLimit)
	assert.Equal(t, listing.RPMLimit, channel.RPMLimit)
	assert.Equal(t, listing.DailyCapMicro, channel.DailyCapMicro)
	assert.Equal(t, listing.Hours, channel.Hours)
	assert.Equal(t, listing.Status, channel.Status)
	assert.Equal(t, listing.Version, channel.Version)

	var key ChannelKey
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Take(&key).Error)
	assert.Equal(t, listing.KeyFingerprint, key.KeyFingerprint)
	assert.Equal(t, listing.KeyCiphertext, key.KeyCiphertext)
	assert.Equal(t, ChannelKeyNormal, key.Status)
	assert.Equal(t, 100, key.Weight)

	var models []SellerChannelModel
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Order("model ASC").Find(&models).Error)
	require.Len(t, models, 2)
	for _, row := range models {
		assert.Equal(t, row.Model, row.UpstreamModel, "deepseek maps the platform id onto itself")
		assert.Equal(t, int64(300), row.MultiplierMilli)
		assert.True(t, row.Enabled)
		assert.False(t, row.VerifiedOpenAIChat, "a draft listing is unverified")
		assert.False(t, row.VerifiedOpenAIResponses)
		assert.False(t, row.VerifiedAnthropicMessages)
	}

	var endpoints []ChannelEndpoint
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Find(&endpoints).Error)
	require.Len(t, endpoints, 3)
	for _, ep := range endpoints {
		assert.Equal(t, listing.ChannelID, ep.NewAPIChannelID)
		assert.False(t, ep.Verified)
		assert.Nil(t, ep.VerifiedAt)
	}
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

// M5 anti-regression: a key with surrounding whitespace normalizes onto the
// already-listed key and must be reported as ErrDuplicateKey; interior
// whitespace/control characters are rejected as a parameter error before
// anything is written.
func TestCreateListingNormalizesKeyWhitespace(t *testing.T) {
	db := newListingTestDB(t)

	first := createTestListing(t, db, 10023, "sk-abc")

	_, err := CreateListing(db, CreateListingInput{
		SellerID: 10024, VendorID: "deepseek", APIKey: "sk-abc ",
		Models: []string{"deepseek-flash"}, MultiplierMilli: 300,
	})
	assert.ErrorIs(t, err, ErrDuplicateKey,
		`"sk-abc " must not open a second listing for "sk-abc"`)
	assertNoKeyLeak(t, err, "sk-abc", first.KeyFingerprint)

	_, err = CreateListing(db, CreateListingInput{
		SellerID: 10024, VendorID: "deepseek", APIKey: "sk-abc def",
		Models: []string{"deepseek-flash"}, MultiplierMilli: 300,
	})
	assert.ErrorIs(t, err, ErrInvalidListingKey, "interior whitespace is rejected")
	assertNoKeyLeak(t, err, "sk-abc def")

	// A rejected create writes nothing.
	var cnt int64
	require.NoError(t, db.Model(&Listing{}).Count(&cnt).Error)
	assert.Equal(t, int64(1), cnt)
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
	passVerification := func() {
		t.Helper()
		require.NoError(t, db.Create(&Probe{
			ListingID: listing.ID, Type: ProbeTypeVerify, OK: true,
		}).Error)
		require.NoError(t, PassVerification(db, listing.ID))
	}
	require.Equal(t, ListingDraft, status())

	// The full legal chain: draft → verifying → canary → live ⇄ paused →
	// draft (a resume re-verifies) → verifying → canary → live → banned.
	require.NoError(t, StartVerification(db, listing.ID))
	assert.Equal(t, ListingVerifying, status())
	passVerification()
	assert.Equal(t, ListingCanary, status())
	require.NoError(t, GoLive(db, listing.ID))
	assert.Equal(t, ListingLive, status())
	require.NoError(t, PauseListing(db, listing.ID))
	assert.Equal(t, ListingPaused, status())
	require.NoError(t, ResumeListing(db, listing.ID))
	assert.Equal(t, ListingDraft, status(), "a platform resume must re-run full verification")
	require.NoError(t, StartVerification(db, listing.ID))
	passVerification()
	assert.Equal(t, ListingCanary, status())
	require.NoError(t, GoLive(db, listing.ID))
	assert.Equal(t, ListingLive, status())
	require.NoError(t, BanListing(db, listing.ID))
	assert.Equal(t, ListingBanned, status())
}

func TestTransitionIllegal(t *testing.T) {
	db := newListingTestDB(t)
	seedPassingProbe := func(id int64) {
		t.Helper()
		require.NoError(t, db.Create(&Probe{ListingID: id, Type: ProbeTypeVerify, OK: true}).Error)
	}

	// draft → live skips verification and canary.
	draft := createTestListing(t, db, 10023, listingTestKey)
	err := GoLive(db, draft.ID)
	assert.ErrorIs(t, err, ErrInvalidTransition)

	// paused → verifying is not an edge, and nothing may jump paused → live.
	paused := createTestListing(t, db, 10023, "sk-test-key-2")
	require.NoError(t, StartVerification(db, paused.ID))
	seedPassingProbe(paused.ID)
	require.NoError(t, PassVerification(db, paused.ID))
	require.NoError(t, GoLive(db, paused.ID))
	require.NoError(t, PauseListing(db, paused.ID))
	err = StartVerification(db, paused.ID)
	assert.ErrorIs(t, err, ErrInvalidTransition)
	err = TransitionListing(db, paused.ID, ListingPaused, ListingLive)
	assert.ErrorIs(t, err, ErrInvalidTransition, "live is reachable only from canary")
	require.NoError(t, ResumeListing(db, paused.ID))
	require.NoError(t, StartVerification(db, paused.ID), "resume lands in draft so verification is mandatory")

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
	seedPassingProbe(canary.ID)
	require.NoError(t, PassVerification(db, canary.ID))
	require.NoError(t, BanListing(db, canary.ID))

	// Unknown id.
	err = StartVerification(db, 999999)
	assert.ErrorIs(t, err, ErrListingNotFound)
}

// TestPendingReviewTransitions covers the operator-review edges: approval
// releases pending_review into verification, rejection returns it to draft, and
// a severe violation bans it. No other promotion is legal from pending_review.
func TestPendingReviewTransitions(t *testing.T) {
	db := newListingTestDB(t)
	pending := func(key string) Listing {
		t.Helper()
		listing := createTestListing(t, db, 10023, key)
		require.NoError(t, db.Model(&Listing{}).Where("id = ?", listing.ID).
			Update("status", ListingPendingReview).Error)
		return listing
	}
	status := func(id int64) string {
		t.Helper()
		row, err := GetListing(db, id)
		require.NoError(t, err)
		return row.Status
	}

	approved := pending(uniqueTestKey("sk-pending-approve"))
	require.NoError(t, StartVerification(db, approved.ID))
	assert.Equal(t, ListingVerifying, status(approved.ID))

	returned := pending(uniqueTestKey("sk-pending-draft"))
	require.NoError(t, RejectListing(db, returned.ID))
	assert.Equal(t, ListingDraft, status(returned.ID))

	banned := pending(uniqueTestKey("sk-pending-ban"))
	require.NoError(t, BanListing(db, banned.ID))
	assert.Equal(t, ListingBanned, status(banned.ID))

	// pending_review cannot skip review: no live, no pass without verifying,
	// no seller-side pause/live edges.
	stuck := pending(uniqueTestKey("sk-pending-illegal"))
	assert.ErrorIs(t, TransitionListing(db, stuck.ID, ListingPendingReview, ListingLive), ErrInvalidTransition)
	assert.ErrorIs(t, PassVerification(db, stuck.ID), ErrInvalidTransition)
	assert.ErrorIs(t, GoLive(db, stuck.ID), ErrInvalidTransition)
	assert.ErrorIs(t, PauseListing(db, stuck.ID), ErrInvalidTransition)
	// RejectListing only accepts pending_review (approved is verifying now).
	assert.ErrorIs(t, RejectListing(db, approved.ID), ErrInvalidTransition)
	assert.ErrorIs(t, RejectListing(db, 999999), ErrListingNotFound)
}

// TestPassVerificationRequiresPassingProbe proves the verifying → canary edge
// cannot be taken without the verdict VerifyListing writes: no probe, or a
// failed probe, leaves the listing in verifying.
func TestPassVerificationRequiresPassingProbe(t *testing.T) {
	db := newListingTestDB(t)

	noProbe := createTestListing(t, db, 10023, listingTestKey)
	require.NoError(t, StartVerification(db, noProbe.ID))
	err := PassVerification(db, noProbe.ID)
	assert.ErrorIs(t, err, ErrVerificationRequired)
	row, err := GetListing(db, noProbe.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingVerifying, row.Status)

	failed := createTestListing(t, db, 10023, "sk-test-key-probe-fail")
	require.NoError(t, StartVerification(db, failed.ID))
	require.NoError(t, db.Create(&Probe{
		ListingID: failed.ID, Type: ProbeTypeVerify, OK: false,
		Detail: `{"fail_reason":"fingerprint verification failed"}`,
	}).Error)
	err = PassVerification(db, failed.ID)
	assert.ErrorIs(t, err, ErrVerificationRequired)
	row, err = GetListing(db, failed.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingVerifying, row.Status)

	passed := createTestListing(t, db, 10023, "sk-test-key-probe-ok")
	require.NoError(t, StartVerification(db, passed.ID))
	require.NoError(t, db.Create(&Probe{
		ListingID: passed.ID, Type: ProbeTypeVerify, OK: true,
	}).Error)
	require.NoError(t, PassVerification(db, passed.ID))
	row, err = GetListing(db, passed.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingCanary, row.Status)
}

// TestSellerResumeRefusesFailedVerification proves a seller pause-lift cannot
// return a listing whose last verification failed to live.
func TestSellerResumeRefusesFailedVerification(t *testing.T) {
	db := newListingTestDB(t)
	listing := createTestListing(t, db, 10023, listingTestKey)
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", listing.ID).
		Update("status", ListingPaused).Error)
	require.NoError(t, db.Create(&Probe{
		ListingID: listing.ID, Type: ProbeTypeVerify, OK: false,
	}).Error)

	err := ResumeSellerListing(db, listing.ID)
	assert.ErrorIs(t, err, ErrVerificationRequired)
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPaused, row.Status)

	// A passing verdict allows the seller pause-lift again.
	require.NoError(t, db.Create(&Probe{
		ListingID: listing.ID, Type: ProbeTypeVerify, OK: true,
	}).Error)
	require.NoError(t, ResumeSellerListing(db, listing.ID))
	row, err = GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, row.Status)
}

// TestResumeListingRequiresReverification proves a platform resume never
// restores live directly: it lands in draft, so the full verify path runs
// again.
func TestResumeListingRequiresReverification(t *testing.T) {
	db := newListingTestDB(t)
	listing := createTestListing(t, db, 10023, listingTestKey)
	require.NoError(t, StartVerification(db, listing.ID))
	require.NoError(t, db.Create(&Probe{ListingID: listing.ID, Type: ProbeTypeVerify, OK: true}).Error)
	require.NoError(t, PassVerification(db, listing.ID))
	require.NoError(t, GoLive(db, listing.ID))
	require.NoError(t, PauseListing(db, listing.ID))

	require.NoError(t, ResumeListing(db, listing.ID))
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingDraft, row.Status)
	assert.Empty(t, row.PauseReason)
	assert.Nil(t, row.ResumeAt)
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

// TestReplaceKeyValidatesChannelPattern makes sure a key swap cannot move a
// plan key into an official channel (or vice versa) without re-checking the
// channel's key_pattern.
func TestReplaceKeyValidatesChannelPattern(t *testing.T) {
	db := newListingTestDB(t)
	listing := Listing{
		SellerID: 1, ChannelID: 1, Vendor: "aliyun-bailian-coding-plan",
		Models: []string{"deepseek-v4-pro"}, MultiplierMilli: 500, Status: ListingLive,
		KeyFingerprint: "fp-replace-pattern", KeyCiphertext: []byte("old"), Version: 1,
	}
	require.NoError(t, db.Create(&listing).Error)

	err := ReplaceKey(db, listing.ID, "sk-official-looking-key")
	assert.ErrorIs(t, err, ErrVendorKeyPattern)

	require.NoError(t, ReplaceKey(db, listing.ID, "sk-sp-plan-key-123"))
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingVerifying, row.Status, "a key swap always re-verifies")
}

// TestCreateListingPassthroughPerModel checks that raw-body pass-through is no
// longer a channel-wide "all models are identity-mapped" switch: the channel
// keeps capability on when the vendor forwards raw bodies, and the relay
// decides per requested model whether the body is passed through (and rewrites
// the model id it carries).
func TestCreateListingPassthroughPerModel(t *testing.T) {
	db := newListingTestDB(t)
	// A renamed mapping must not disable the channel capability any more.
	require.NoError(t, db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", "deepseek", "deepseek-flash").
		Update("upstream_model", "deepseek-flash-renamed").Error)

	renamed, err := CreateListing(db, CreateListingInput{
		SellerID: 1, VendorID: "deepseek", APIKey: uniqueTestKey("sk-ds-rename"),
		Models: []string{"deepseek-flash"}, MultiplierMilli: 500,
	})
	require.NoError(t, err)
	var ch model.Channel
	require.NoError(t, db.Where("id = ?", renamed.ChannelID).Take(&ch).Error)
	setting := ""
	if ch.Setting != nil {
		setting = *ch.Setting
	}
	assert.Contains(t, setting, "pass_through_body_enabled", "the vendor supports raw bodies; the per-model decision happens at relay time")
	assert.Contains(t, setting, "mp_anthropic_messages_url", "the native messages endpoint is always stored")
	assert.Equal(t, "https://api.deepseek.com", ch.GetBaseURL())

	// A vendor that does not forward raw bodies never gets the capability, but
	// still stores its native messages endpoint for /v1/messages.
	require.NoError(t, db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", "volcengine-ark", "deepseek-flash").
		Updates(map[string]any{"verified": true, "fingerprint_ratio": 1.0}).Error)
	converted, err := CreateListing(db, CreateListingInput{
		SellerID: 2, VendorID: "volcengine-ark", APIKey: uniqueTestKey("sk-ark-rename"),
		Models: []string{"deepseek-flash"}, MultiplierMilli: 500,
	})
	require.NoError(t, err)
	var arkChannel model.Channel
	require.NoError(t, db.Where("id = ?", converted.ChannelID).Take(&arkChannel).Error)
	setting = ""
	if arkChannel.Setting != nil {
		setting = *arkChannel.Setting
	}
	assert.NotContains(t, setting, "pass_through_body_enabled", "this vendor converts bodies")
	assert.Contains(t, setting, "mp_anthropic_messages_url")
	assert.Contains(t, setting, "mp_strip_v1_prefix")
}

// TestCreateListingUsesEndpointBases checks that per-endpoint protocol bases
// from mp_channels.protocols reach the channel row instead of the legacy
// single openai base.
func TestCreateListingUsesEndpointBases(t *testing.T) {
	db := newListingTestDB(t)
	ch, ok := ChannelPoolBySlug(db, "deepseek")
	require.True(t, ok)
	require.NoError(t, db.Model(&ChannelPool{}).Where("id = ?", ch.ID).Update("protocols",
		`{"openai":"https://legacy.example.com","openai_chat":"https://chat.example.com/v1",`+
			`"openai_responses":"https://responses.example.com","anthropic_messages":"https://messages.example.com"}`).Error)

	listing, err := CreateListing(db, CreateListingInput{
		SellerID: 1, VendorID: "deepseek", APIKey: uniqueTestKey("sk-endpoints"),
		Models: []string{"deepseek-flash"}, MultiplierMilli: 500,
	})
	require.NoError(t, err)
	var channel model.Channel
	require.NoError(t, db.Where("id = ?", listing.ChannelID).Take(&channel).Error)
	assert.Equal(t, "https://chat.example.com/v1", channel.GetBaseURL())
	setting := ""
	if channel.Setting != nil {
		setting = *channel.Setting
	}
	assert.Contains(t, setting, `"mp_anthropic_messages_url":"https://messages.example.com/v1/messages"`)
}

// TestCreateListingTrustPoolBlockedRejected proves a blocked upstream leaves
// neither a channel nor a listing behind.
func TestCreateListingTrustPoolBlockedRejected(t *testing.T) {
	db := newListingTestDB(t)
	createTrustPoolRow(t, db, TrustPool{
		Host: "blocked-listing.example.com", Kind: TrustKindThirdParty,
		Trust: TrustBlocked, Source: "manual",
	})

	_, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		APIKey:          uniqueTestKey("sk-trust-blocked"),
		CustomBaseURL:   "https://blocked-listing.example.com",
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	assert.ErrorIs(t, err, ErrTrustPoolBlocked)

	var listings, channels int64
	require.NoError(t, db.Model(&Listing{}).Count(&listings).Error)
	require.NoError(t, db.Model(&model.Channel{}).Count(&channels).Error)
	assert.Zero(t, listings)
	assert.Zero(t, channels, "a blocked address must not provision an upstream channel")
}

// TestCreateListingUntrustedUpstreamsWaitForReview covers watch and unlisted:
// both create a listing, but it stays out of draft so the asynchronous
// verification cannot promote it.
func TestCreateListingUntrustedUpstreamsWaitForReview(t *testing.T) {
	db := newListingTestDB(t)
	createTrustPoolRow(t, db, TrustPool{
		Host: "watch-listing.example.com", Kind: TrustKindThirdParty,
		Trust: TrustWatch, Source: "probe",
	})

	cases := []struct {
		name string
		host string
	}{
		{name: "watch", host: "watch-listing.example.com"},
		{name: "unlisted", host: "unlisted-listing.example.com"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			listing, err := CreateListing(db, CreateListingInput{
				SellerID:        10023,
				APIKey:          uniqueTestKey("sk-review-" + tc.name),
				CustomBaseURL:   "https://" + tc.host,
				Models:          []string{"deepseek-flash"},
				MultiplierMilli: 500,
			})
			require.NoError(t, err)
			require.Equal(t, ListingPendingReview, listing.Status)

			// The automated path refuses a pending_review row, so it can never
			// reach canary before an operator approves it. No network is touched
			// because the status check runs first.
			_, err = VerifyListing(context.Background(), db, nil, nil, listing.ID)
			assert.ErrorIs(t, err, ErrInvalidTransition, "case %d", i)
			row, err := GetListing(db, listing.ID)
			require.NoError(t, err)
			assert.Equal(t, ListingPendingReview, row.Status)
		})
	}
}

// TestCreateListingTrustedCustomChannelAutoVerifies covers the trusted branch
// end to end: the listing starts in draft and the ordinary verification may
// promote it to canary.
func TestCreateListingTrustedCustomChannelAutoVerifies(t *testing.T) {
	t.Setenv(MPAllowLoopbackBaseURLEnv, "1")
	db := newListingTestDB(t)
	createTrustPoolRow(t, db, TrustPool{
		Host: "127.0.0.1", Kind: TrustKindThirdParty, Trust: TrustTrusted, Source: "manual",
	})

	const key = "sk-trusted-upstream-key"
	server := fakeCustomUpstream(t, key, "deepseek-flash")

	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		APIKey:          key,
		CustomBaseURL:   server.URL,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	assert.Equal(t, ListingDraft, listing.Status, "trusted means no manual review")

	res, err := VerifyListing(context.Background(), db, server.Client(), NewVerifyLimiter(1, 600), listing.ID)
	require.NoError(t, err)
	require.True(t, res.OK, "fail reason: %s", res.FailReason)

	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingCanary, row.Status)
}

// TestRefreshListingChannelMapping updates the pinned upstream name on every
// listing channel for a (channel, model) pair, and removes it on deletion.
func TestRefreshListingChannelMapping(t *testing.T) {
	db := newListingTestDB(t)
	listing := createTestListing(t, db, 1, uniqueTestKey("sk-refresh-map"))

	require.NoError(t, RefreshListingChannelMapping(db, "deepseek", "deepseek-flash", "deepseek-flash-dated"))
	var ch model.Channel
	require.NoError(t, db.Where("id = ?", listing.ChannelID).Take(&ch).Error)
	var mapping map[string]string
	require.NoError(t, common.Unmarshal([]byte(ch.GetModelMapping()), &mapping))
	assert.Equal(t, "deepseek-flash-dated", mapping["deepseek-flash"])

	require.NoError(t, RefreshListingChannelMapping(db, "deepseek", "deepseek-flash", ""))
	require.NoError(t, db.Where("id = ?", listing.ChannelID).Take(&ch).Error)
	mapping = nil
	require.NoError(t, common.Unmarshal([]byte(ch.GetModelMapping()), &mapping))
	_, exists := mapping["deepseek-flash"]
	assert.False(t, exists)
}
