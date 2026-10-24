package marketplace

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// statsFixture serves the usage-trend routes behind a simulated login and the
// public stats route without auth.
type statsFixture struct {
	db           *gorm.DB
	userEngine   *gin.Engine
	publicEngine *gin.Engine
	buyerID      int64
	otherID      int64
}

func newStatsFixture(t *testing.T) *statsFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}))

	relayStateMu.Lock()
	oldRelayDB := relayDB
	relayDB = db
	relayStateMu.Unlock()
	resetPublicStatsCache()
	t.Cleanup(func() {
		resetPublicStatsCache()
		relayStateMu.Lock()
		relayDB = oldRelayDB
		relayStateMu.Unlock()
	})

	buyerID := insertTestUser(t, db, "stats-buyer")
	otherID := insertTestUser(t, db, "stats-other")

	userEngine := gin.New()
	RegisterStatsRoutes(userEngine.Group("/api/marketplace", headerUserAuth()))
	publicEngine := gin.New()
	RegisterPublicStatsRoutes(publicEngine.Group("/api/marketplace"))

	return &statsFixture{
		db:           db,
		userEngine:   userEngine,
		publicEngine: publicEngine,
		buyerID:      buyerID,
		otherID:      otherID,
	}
}

func (f *statsFixture) get(t *testing.T, engine *gin.Engine, path string, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if userID > 0 {
		req.Header.Set("X-Test-User-Id", strconv.FormatInt(userID, 10))
	}
	engine.ServeHTTP(w, req)
	return w
}

// seedStatsRequest writes one mp_requests row for the trend/public tests.
func seedStatsRequest(t *testing.T, db *gorm.DB, row Request) {
	t.Helper()
	if row.Status == "" {
		row.Status = RequestSuccess
	}
	require.NoError(t, db.Create(&row).Error)
}

// seedStatsListing writes one listing row directly; only the fields the stats
// aggregate reads are meaningful.
func seedStatsListing(t *testing.T, db *gorm.DB, vendor string, models []string, mult int64, status, fingerprint string) {
	t.Helper()
	require.NoError(t, db.Create(&Listing{
		SellerID:        1000,
		ChannelID:       1,
		Vendor:          vendor,
		Models:          models,
		MultiplierMilli: mult,
		Status:          status,
		KeyFingerprint:  fingerprint,
	}).Error)
}

// TestUsageStatsDailyAggregation pins the Asia/Shanghai day boundaries and
// the per-day sums.
func TestUsageStatsDailyAggregation(t *testing.T) {
	f := newStatsFixture(t)
	loc, err := time.LoadLocation(shanghaiTZ)
	require.NoError(t, err)

	// Three-day horizon ending Tuesday 2026-10-22.
	now := time.Date(2026, time.October, 22, 10, 0, 0, 0, loc)
	day20 := time.Date(2026, time.October, 20, 0, 0, 0, 0, loc)
	day21 := time.Date(2026, time.October, 21, 0, 0, 0, 0, loc)
	day22 := time.Date(2026, time.October, 22, 0, 0, 0, 0, loc)

	rows := []Request{
		// Exactly at the start of the horizon → day 20.
		{RequestID: "u-1", BuyerUserID: f.buyerID, InputTokens: 100, OutputTokens: 10,
			CacheReadTokens: 5, TotalDebitMicro: 1000, OfficialCostMicro: 3000, CreatedAt: day20},
		// Last instant of day 20 → still day 20.
		{RequestID: "u-2", BuyerUserID: f.buyerID, InputTokens: 200, OutputTokens: 20,
			TotalDebitMicro: 2000, OfficialCostMicro: 4000, CreatedAt: day20.AddDate(0, 0, 1).Add(-time.Nanosecond)},
		// First instant of day 21 → day 21, not day 20.
		{RequestID: "u-3", BuyerUserID: f.buyerID, InputTokens: 300, OutputTokens: 30,
			TotalDebitMicro: 3000, OfficialCostMicro: 6000, CreatedAt: day21},
		{RequestID: "u-4", BuyerUserID: f.buyerID, InputTokens: 400, OutputTokens: 40,
			TotalDebitMicro: 4000, OfficialCostMicro: 8000, CreatedAt: day22.Add(9 * time.Hour)},
		// Just before the horizon → excluded.
		{RequestID: "u-before", BuyerUserID: f.buyerID, InputTokens: 900, TotalDebitMicro: 9000,
			OfficialCostMicro: 9000, CreatedAt: day20.Add(-time.Nanosecond)},
		// Another buyer → excluded.
		{RequestID: "u-other", BuyerUserID: f.otherID, InputTokens: 999, TotalDebitMicro: 9999,
			OfficialCostMicro: 9999, CreatedAt: day21},
		// A failed row still counts as a request, with zero money.
		{RequestID: "u-failed", BuyerUserID: f.buyerID, Status: RequestFailed, CreatedAt: day21.Add(time.Hour)},
	}
	for _, row := range rows {
		seedStatsRequest(t, f.db, row)
	}

	stats, err := aggregateUsageDaily(f.db, f.buyerID, 3, now)
	require.NoError(t, err)
	assert.Equal(t, 3, stats.Days)
	assert.Equal(t, "2026-10-20", stats.Start)
	assert.Equal(t, "2026-10-22", stats.End)
	require.Len(t, stats.Items, 3)

	// Day 20: u-1 + u-2.
	assert.Equal(t, "2026-10-20", stats.Items[0].Date)
	assert.Equal(t, int64(2), stats.Items[0].Requests)
	assert.Equal(t, int64(300), stats.Items[0].InputTokens)
	assert.Equal(t, int64(30), stats.Items[0].OutputTokens)
	assert.Equal(t, int64(5), stats.Items[0].CacheReadTokens)
	assert.Equal(t, int64(335), stats.Items[0].Tokens, "input+output+cache read+cache write")
	assert.Equal(t, int64(3000), stats.Items[0].TotalDebitMicro)
	assert.Equal(t, int64(4000), stats.Items[0].SavedMicro)

	// Day 21: u-3 + u-failed (a failed request is still a request).
	assert.Equal(t, "2026-10-21", stats.Items[1].Date)
	assert.Equal(t, int64(2), stats.Items[1].Requests)
	assert.Equal(t, int64(300), stats.Items[1].InputTokens)
	assert.Equal(t, int64(330), stats.Items[1].Tokens)
	assert.Equal(t, int64(3000), stats.Items[1].TotalDebitMicro)
	assert.Equal(t, int64(3000), stats.Items[1].SavedMicro)

	// Day 22: u-4.
	assert.Equal(t, "2026-10-22", stats.Items[2].Date)
	assert.Equal(t, int64(1), stats.Items[2].Requests)
	assert.Equal(t, int64(440), stats.Items[2].Tokens)
	assert.Equal(t, int64(4000), stats.Items[2].SavedMicro)

	// A one-day horizon only reports today.
	oneDay, err := aggregateUsageDaily(f.db, f.buyerID, 1, now)
	require.NoError(t, err)
	require.Len(t, oneDay.Items, 1)
	assert.Equal(t, "2026-10-22", oneDay.Items[0].Date)
}

func TestUsageStatsEndpoint(t *testing.T) {
	f := newStatsFixture(t)
	now := time.Now()

	seedStatsRequest(t, f.db, Request{
		RequestID: "e-1", BuyerUserID: f.buyerID, InputTokens: 1000, OutputTokens: 100,
		TotalDebitMicro: 500, OfficialCostMicro: 1500, CreatedAt: now,
	})
	seedStatsRequest(t, f.db, Request{
		RequestID: "e-2", BuyerUserID: f.otherID, InputTokens: 5000, TotalDebitMicro: 100,
		OfficialCostMicro: 100, CreatedAt: now,
	})

	w := f.get(t, f.userEngine, "/api/marketplace/records/usage/stats?days=7", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	data := apiData(t, decodeAPIBody(t, w))
	assert.Equal(t, int64(7), apiNum(t, data, "days"))
	items := apiPageItems(t, data)
	require.Len(t, items, 7, "dense daily series")

	today := items[6].(map[string]any)
	assert.Equal(t, time.Now().In(mustShanghai(t)).Format("2006-01-02"), today["date"])
	assert.Equal(t, int64(1), apiNum(t, today, "requests"), "only the caller's own rows")
	assert.Equal(t, int64(1100), apiNum(t, today, "tokens"))
	assert.Equal(t, int64(500), apiNum(t, today, "total_debit_micro"))
	assert.Equal(t, int64(1000), apiNum(t, today, "saved_micro"))

	// Invalid days fall back to the 7-day default; huge values are capped.
	w = f.get(t, f.userEngine, "/api/marketplace/records/usage/stats?days=abc", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(7), apiNum(t, apiData(t, decodeAPIBody(t, w)), "days"))
	w = f.get(t, f.userEngine, "/api/marketplace/records/usage/stats?days=1000", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(usageDaysMax), apiNum(t, apiData(t, decodeAPIBody(t, w)), "days"))

	// No login → 401.
	w = f.get(t, f.userEngine, "/api/marketplace/records/usage/stats", 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func mustShanghai(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(shanghaiTZ)
	require.NoError(t, err)
	return loc
}

// TestPublicStats covers the four aggregates, the sensitive-field contract
// and the 60s cache.
func TestPublicStats(t *testing.T) {
	f := newStatsFixture(t)
	now := time.Now()
	loc := mustShanghai(t)
	today := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 12, 0, 0, 0, loc)

	// Supply: alpha and beta are live, gamma only canary. The advertised
	// multiplier floor is live-only (300), not the canary's 100.
	seedStatsListing(t, f.db, "deepseek", []string{"alpha", "beta"}, 300, ListingLive, "fp-live-1")
	seedStatsListing(t, f.db, "deepseek", []string{"alpha"}, 350, ListingLive, "fp-live-2")
	seedStatsListing(t, f.db, "deepseek", []string{"gamma"}, 100, ListingCanary, "fp-canary")

	// Two distinct models in the price book.
	for _, modelName := range []string{"alpha", "beta"} {
		require.NoError(t, f.db.Create(&PriceBook{
			Model: modelName, Vendor: "deepseek", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 1000, OutputMicroPerMTok: 2000, EffectiveAt: now,
		}).Error)
	}

	// Today: three rows regardless of buyer; yesterday's row is excluded.
	seedStatsRequest(t, f.db, Request{RequestID: "p-1", BuyerUserID: f.buyerID, CreatedAt: today})
	seedStatsRequest(t, f.db, Request{RequestID: "p-2", BuyerUserID: f.otherID, CreatedAt: today.Add(time.Minute)})
	seedStatsRequest(t, f.db, Request{RequestID: "p-3", BuyerUserID: f.buyerID, CreatedAt: today.Add(time.Hour)})
	seedStatsRequest(t, f.db, Request{RequestID: "p-yesterday", BuyerUserID: f.buyerID, CreatedAt: today.AddDate(0, 0, -1)})

	stats, err := computePublicStats(f.db, now)
	require.NoError(t, err)
	assert.Equal(t, int64(3), stats.ModelsOnSale, "distinct (model, vendor) pairs")
	assert.Equal(t, int64(300), stats.MinMultiplierMilli, "live only, canary does not set the floor")
	assert.Equal(t, int64(3), stats.TodayRequests)
	assert.Equal(t, int64(2), stats.PlatformModels)

	// The payload carries no seller/buyer/listing identifiers or key material
	// (the struct has only aggregates; this guards future field additions).
	raw, err := common.Marshal(stats)
	require.NoError(t, err)
	body := string(raw)
	for _, forbidden := range []string{"seller_id", "buyer_user_id", "buyer_id", "listing_id", "fingerprint", "ciphertext"} {
		assert.NotContains(t, body, forbidden)
	}

	// Anonymous HTTP access works and exposes exactly the documented fields.
	w := f.get(t, f.publicEngine, "/api/marketplace/stats/public", 0)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	data := apiData(t, decodeAPIBody(t, w))
	assert.Equal(t, int64(3), apiNum(t, data, "models_on_sale"))
	assert.Equal(t, int64(300), apiNum(t, data, "min_multiplier_milli"))
	assert.Equal(t, int64(3), apiNum(t, data, "today_requests"))
	assert.Equal(t, int64(2), apiNum(t, data, "platform_models"))
	assert.NotEmpty(t, data["generated_at"])
	assert.Len(t, data, 5)
	assert.NotContains(t, w.Body.String(), "seller_id")
	assert.NotContains(t, w.Body.String(), "listing_id")

	// Cache: a data change inside the 60s window is not visible.
	seedStatsRequest(t, f.db, Request{RequestID: "p-4", BuyerUserID: f.buyerID, CreatedAt: today.Add(2 * time.Hour)})
	w = f.get(t, f.publicEngine, "/api/marketplace/stats/public", 0)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(3), apiNum(t, apiData(t, decodeAPIBody(t, w)), "today_requests"),
		"second call inside 60s must be served from the process cache")

	// Dropping the cache makes the next call hit the database and see p-4.
	resetPublicStatsCache()
	w = f.get(t, f.publicEngine, "/api/marketplace/stats/public", 0)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(4), apiNum(t, apiData(t, decodeAPIBody(t, w)), "today_requests"))

	// TTL logic directly: 30s later is a hit, 61s later is a refresh.
	resetPublicStatsCache()
	v1, err := cachedPublicStats(f.db, now)
	require.NoError(t, err)
	assert.Equal(t, now, v1.GeneratedAt)
	seedStatsRequest(t, f.db, Request{RequestID: "p-5", BuyerUserID: f.buyerID, CreatedAt: today.Add(3 * time.Hour)})
	v2, err := cachedPublicStats(f.db, now.Add(30*time.Second))
	require.NoError(t, err)
	assert.Equal(t, now, v2.GeneratedAt, "cache hit keeps the first generated_at")
	assert.Equal(t, v1.TodayRequests, v2.TodayRequests, "cache hit does not re-query")
	v3, err := cachedPublicStats(f.db, now.Add(61*time.Second))
	require.NoError(t, err)
	assert.Equal(t, now.Add(61*time.Second), v3.GeneratedAt, "TTL expiry recomputes")
	assert.Equal(t, v1.TodayRequests+1, v3.TodayRequests)
}
