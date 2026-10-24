package marketplace

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- KeyPref upsert / read ---------------------------------------------------

func TestKeyPrefUpsertRoundTrip(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	// Create: an empty strategy defaults to balanced (§3.6).
	require.NoError(t, UpsertKeyPref(db, 77, KeyPrefInput{PriceCapMilli: 500, RPM: 30}))
	pref, err := GetKeyPref(db, 77)
	require.NoError(t, err)
	assert.Equal(t, StrategyBalanced, pref.Strategy)
	assert.Equal(t, int64(500), pref.PriceCapMilli)
	assert.Equal(t, 30, pref.RPM)

	// Update: the full field set round-trips through the row.
	require.NoError(t, UpsertKeyPref(db, 77, KeyPrefInput{
		Strategy:        StrategyCheapest,
		PriceCapMilli:   800,
		PreferSellerIDs: []int64{11, 22},
		BlockSellerIDs:  []int64{33},
		RPM:             60,
	}))
	pref, err = GetKeyPref(db, 77)
	require.NoError(t, err)
	assert.Equal(t, StrategyCheapest, pref.Strategy)
	assert.Equal(t, int64(800), pref.PriceCapMilli)
	assert.Equal(t, []int64{11, 22}, pref.PreferSellerIDs)
	assert.Equal(t, []int64{33}, pref.BlockSellerIDs)
	assert.Equal(t, 60, pref.RPM)

	// 只用某一家 + 允许回落 round-trips.
	require.NoError(t, UpsertKeyPref(db, 77, KeyPrefInput{Strategy: StrategyStable, OnlySellerID: 11, AllowFallback: true}))
	pref, err = GetKeyPref(db, 77)
	require.NoError(t, err)
	assert.Equal(t, int64(11), pref.OnlySellerID)
	assert.True(t, pref.AllowFallback)

	// A token that never saved a preference → ErrKeyPrefNotFound.
	_, err = GetKeyPref(db, 999)
	assert.ErrorIs(t, err, ErrKeyPrefNotFound)
}

func TestKeyPrefUpsertValidation(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	assert.Error(t, UpsertKeyPref(db, 77, KeyPrefInput{Strategy: "fastest"}), "strategy outside the §3.6 enum")
	assert.Error(t, UpsertKeyPref(db, 77, KeyPrefInput{PriceCapMilli: -1}), "negative price cap")
	assert.Error(t, UpsertKeyPref(db, 77, KeyPrefInput{RPM: -5}), "negative rpm")
	assert.Error(t, UpsertKeyPref(db, 77, KeyPrefInput{OnlySellerID: 11, PreferSellerIDs: []int64{11}}), "only conflicts with prefer")
	assert.Error(t, UpsertKeyPref(db, 77, KeyPrefInput{OnlySellerID: 11, BlockSellerIDs: []int64{22}}), "only conflicts with block")
	assert.Error(t, UpsertKeyPref(db, 0, KeyPrefInput{}), "non-positive token id")

	// None of the rejected writes left a row behind.
	_, err := GetKeyPref(db, 77)
	assert.ErrorIs(t, err, ErrKeyPrefNotFound)
}

// --- KeyModel management -------------------------------------------------------

func TestKeyModelAddRemove(t *testing.T) {
	f := newRelayFixture(t, 0)

	// The model has no live/canary listing yet → ErrNoListingAvailable.
	err := AddKeyModel(f.db, 77, "deepseek-flash", 0)
	assert.ErrorIs(t, err, ErrNoListingAvailable)

	f.addListing(t, 500)
	require.NoError(t, AddKeyModel(f.db, 77, "deepseek-flash", 300))

	// Duplicate on the same key → ErrModelAlreadyAdded; another key is
	// independent.
	assert.ErrorIs(t, AddKeyModel(f.db, 77, "deepseek-flash", 100), ErrModelAlreadyAdded)
	require.NoError(t, AddKeyModel(f.db, 88, "deepseek-flash", 0))

	rows, err := ListKeyModels(f.db, 77)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "deepseek-flash", rows[0].Model)
	assert.Equal(t, int64(300), rows[0].PriceCapMilli)

	require.NoError(t, RemoveKeyModel(f.db, 77, "deepseek-flash"))
	assert.ErrorIs(t, RemoveKeyModel(f.db, 77, "deepseek-flash"), ErrModelNotAdded)

	rows, err = ListKeyModels(f.db, 77)
	require.NoError(t, err)
	assert.Empty(t, rows)

	// Field validation.
	assert.Error(t, AddKeyModel(f.db, 77, "  ", 0), "blank model name")
	assert.Error(t, AddKeyModel(f.db, 77, "deepseek-flash", -1), "negative cap")
	assert.Error(t, AddKeyModel(f.db, 0, "deepseek-flash", 0), "non-positive token id")
}

// --- buyer RPM (§3.6) ------------------------------------------------------------

// swapBuyerRPMWindow isolates the process-global buyer RPM window for one
// test.
func swapBuyerRPMWindow(t *testing.T) {
	t.Helper()
	old := buyerRPMWindow
	buyerRPMWindow = NewRPMWindow()
	t.Cleanup(func() { buyerRPMWindow = old })
}

func TestBuyerRPMAllow(t *testing.T) {
	f := newRelayFixture(t, 0)
	swapBuyerRPMWindow(t)
	now := time.Now()

	// No preference row → unlimited.
	for range 5 {
		assert.True(t, BuyerRPMAllow(77, now))
	}

	// RPM = 2: the third attempt inside the 60s window is refused.
	require.NoError(t, UpsertKeyPref(f.db, 77, KeyPrefInput{RPM: 2}))
	assert.True(t, BuyerRPMAllow(77, now))
	assert.True(t, BuyerRPMAllow(77, now.Add(time.Second)))
	assert.False(t, BuyerRPMAllow(77, now.Add(2*time.Second)))
	// The window slides: a minute later the oldest hit falls out.
	assert.True(t, BuyerRPMAllow(77, now.Add(61*time.Second)))

	// RPM = 0 → unlimited.
	require.NoError(t, UpsertKeyPref(f.db, 88, KeyPrefInput{RPM: 0}))
	for i := range 5 {
		assert.True(t, BuyerRPMAllow(88, now.Add(time.Duration(i)*time.Second)))
	}
}

// TestBeginDistributeBuyerRPM covers the §3.6 buyer-RPM wiring in
// BeginDistribute: the second request inside the window is refused before
// listing selection, with the §3.10-conformant 400 (no 429 in the outward
// error list; §10 待确认).
func TestBeginDistributeBuyerRPM(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	require.NoError(t, UpsertKeyPref(f.db, 77, KeyPrefInput{RPM: 1}))
	swapBuyerRPMWindow(t)

	// First request passes the RPM check and gets a listing.
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)

	// Second request in the same minute exceeds RPM = 1 → 400, and no relay
	// session is opened for it.
	c2 := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))
	_, handled2, status, err := BeginDistribute(c2, "deepseek-flash")
	require.Error(t, err)
	assert.True(t, handled2)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, err.Error(), "rate limited")
	assert.Nil(t, CurrentRelaySession(c2))
}
