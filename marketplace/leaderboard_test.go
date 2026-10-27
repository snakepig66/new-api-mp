package marketplace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// leaderboardFixture serves the public leaderboard routes against an isolated
// database and a configured MP_KEY_SECRET.
type leaderboardFixture struct {
	db     *gorm.DB
	engine *gin.Engine
}

func newLeaderboardFixture(t *testing.T) *leaderboardFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	migrateAll(t, db)

	oldSecret := anonSecret
	SetAnonSecret("leaderboard-test-secret")
	relayStateMu.Lock()
	oldRelayDB := relayDB
	relayDB = db
	relayStateMu.Unlock()
	resetLeaderboardCache()
	t.Cleanup(func() {
		resetLeaderboardCache()
		anonSecret = oldSecret
		relayStateMu.Lock()
		relayDB = oldRelayDB
		relayStateMu.Unlock()
	})

	engine := gin.New()
	RegisterLeaderboardRoutes(engine.Group("/api/marketplace"))
	return &leaderboardFixture{db: db, engine: engine}
}

func (f *leaderboardFixture) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	f.engine.ServeHTTP(w, req)
	return w
}

// leaderboardEnvelope decodes the common.ApiSuccess wrapper into T.
type leaderboardEnvelope[T any] struct {
	Success bool `json:"success"`
	Data    T    `json:"data"`
}

func decodeLeaderboardData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var env leaderboardEnvelope[T]
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env), w.Body.String())
	require.True(t, env.Success)
	return env.Data
}

// seedBoardRequest writes one mp_requests row; only the columns the
// leaderboard aggregates read are meaningful.
func seedBoardRequest(t *testing.T, db *gorm.DB, requestID string, buyerID, sellerID int64,
	model, status string, latencyMs, inputTokens, outputTokens, sellerCredit int64, at time.Time) {
	t.Helper()
	require.NoError(t, db.Create(&Request{
		RequestID:         requestID,
		BuyerUserID:       buyerID,
		SellerID:          sellerID,
		Model:             model,
		Tier:              TierFlat,
		PriceVersion:      1,
		MultiplierMilli:   1000,
		InputTokens:       inputTokens,
		OutputTokens:      outputTokens,
		TotalDebitMicro:   sellerCredit,
		SellerCreditMicro: sellerCredit,
		Status:            status,
		LatencyMs:         latencyMs,
		CreatedAt:         at,
	}).Error)
}

// TestLeaderboardModelDedupesBuyerTraffic verifies the "unrelated paid buyer"
// metric: a buyer's repeated requests to one model collapse to one
// contribution, and the settlement contribution is capped at 10%.
func TestLeaderboardModelDedupesBuyerTraffic(t *testing.T) {
	f := newLeaderboardFixture(t)
	now := time.Now()
	// Buyer 100 sends three requests for model-a; buyer 101 one.
	for i := range 3 {
		seedBoardRequest(t, f.db, "dup-"+time.Duration(i).String(), 100, 10011, "model-a",
			RequestSuccess, 100, 1200, 800, 100, now.Add(-time.Hour))
	}
	seedBoardRequest(t, f.db, "dup-other", 101, 10011, "model-a",
		RequestSuccess, 100, 1200, 800, 100, now.Add(-time.Hour))

	data := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h"))
	require.Len(t, data.Items, 1)
	item := data.Items[0]
	assert.Equal(t, "model-a", item.Model)
	assert.Equal(t, int64(2), item.Buyers, "two distinct buyers, not four requests")
	assert.Equal(t, int64(2), item.Requests, "Requests mirrors the deduplicated buyer count")
	assert.Equal(t, int64(1000), item.SuccessRateMilli)
	assert.Equal(t, int64(8000), item.Tokens, "all qualifying tokens are still reported")
	// Pre-cap settlement 400; 10% cap is 40 per buyer → 80 total.
	assert.Equal(t, int64(80), item.IncomeMicro)
}

// TestLeaderboardBuyerContributionCap verifies a buyer contributing more than
// 10% of a seller's settlement is capped, and the ranking uses buyer count
// before settlement amount.
func TestLeaderboardBuyerContributionCap(t *testing.T) {
	f := newLeaderboardFixture(t)
	now := time.Now()
	// Seller 200: buyer 1 contributes 900, buyer 2 contributes 100.
	seedBoardRequest(t, f.db, "cap-1", 1, 200, "model-a", RequestSuccess, 100, 2000, 0, 900, now.Add(-time.Hour))
	seedBoardRequest(t, f.db, "cap-2", 2, 200, "model-a", RequestSuccess, 100, 2000, 0, 100, now.Add(-time.Hour))
	// Seller 201: a single buyer contributes 500; the same 10% rule applies.
	seedBoardRequest(t, f.db, "cap-3", 3, 201, "model-b", RequestSuccess, 100, 2000, 0, 500, now.Add(-time.Hour))

	models := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h"))
	require.Len(t, models.Items, 2)
	assert.Equal(t, "model-a", models.Items[0].Model, "buyer count ranks first")
	assert.Equal(t, int64(2), models.Items[0].Buyers)
	assert.Equal(t, int64(200), models.Items[0].IncomeMicro, "900→100 and 100→100 after the 10% cap")
	assert.Equal(t, "model-b", models.Items[1].Model)
	assert.Equal(t, int64(1), models.Items[1].Buyers)
	assert.Equal(t, int64(50), models.Items[1].IncomeMicro, "a sole buyer is capped to 10% of the total")

	sellers := decodeLeaderboardData[sellerLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/sellers?range=24h"))
	require.Len(t, sellers.Items, 2)
	assert.Equal(t, int64(2), sellers.Items[0].Buyers)
	assert.Equal(t, int64(200), sellers.Items[0].IncomeMicro)
	assert.Equal(t, int64(1), sellers.Items[1].Buyers)
	assert.Equal(t, int64(50), sellers.Items[1].IncomeMicro)
}

// TestLeaderboardMinimumTokenThreshold verifies requests with exactly 1000
// tokens do not count; only totals above the threshold do.
// TestLeaderboardModelCapIsPerSeller verifies the model board applies the 10%
// cap to each buyer's contribution to each seller before summing sellers, not
// to the model's global total.
func TestLeaderboardModelCapIsPerSeller(t *testing.T) {
	f := newLeaderboardFixture(t)
	now := time.Now()
	// Seller 300: buyer 1 contributes 900, buyer 2 contributes 100 → 200.
	seedBoardRequest(t, f.db, "per-seller-1", 1, 300, "model-c", RequestSuccess, 100, 2000, 0, 900, now.Add(-time.Hour))
	seedBoardRequest(t, f.db, "per-seller-2", 2, 300, "model-c", RequestSuccess, 100, 2000, 0, 100, now.Add(-time.Hour))
	// Seller 301: buyer 3 contributes 450, buyer 4 contributes 50 → 100.
	seedBoardRequest(t, f.db, "per-seller-3", 3, 301, "model-c", RequestSuccess, 100, 2000, 0, 450, now.Add(-time.Hour))
	seedBoardRequest(t, f.db, "per-seller-4", 4, 301, "model-c", RequestSuccess, 100, 2000, 0, 50, now.Add(-time.Hour))

	data := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h"))
	require.Len(t, data.Items, 1)
	assert.Equal(t, int64(4), data.Items[0].Buyers)
	assert.Equal(t, int64(300), data.Items[0].IncomeMicro, "200 from seller 300 + 100 from seller 301")
}

func TestLeaderboardMinimumTokenThreshold(t *testing.T) {
	f := newLeaderboardFixture(t)
	now := time.Now()
	seedBoardRequest(t, f.db, "threshold-low", 1, 300, "model-low", RequestSuccess,
		100, 600, 400, 100, now.Add(-time.Hour)) // exactly 1000 tokens
	seedBoardRequest(t, f.db, "threshold-high", 2, 300, "model-high", RequestSuccess,
		100, 600, 401, 100, now.Add(-time.Hour)) // 1001 tokens

	data := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h"))
	require.Len(t, data.Items, 1)
	assert.Equal(t, "model-high", data.Items[0].Model)
	assert.Equal(t, int64(1), data.Items[0].Buyers)
	assert.Equal(t, int64(1001), data.Items[0].Tokens)
	assert.Equal(t, int64(10), data.Items[0].IncomeMicro, "sole buyer capped to 10%")
}

func TestLeaderboardLimit(t *testing.T) {
	f := newLeaderboardFixture(t)
	now := time.Now()
	// m-1: 1 buyer, m-2: 2 buyers, m-3: 3 buyers. Each request qualifies.
	for i, model := range []string{"m-1", "m-2", "m-3"} {
		for n := 0; n <= i; n++ {
			seedBoardRequest(t, f.db, model+"-"+time.Duration(n).String(), int64(100+n), 10011,
				model, RequestSuccess, 10, 2000, 0, 100, now.Add(-time.Hour))
		}
	}
	data := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?limit=2"))
	require.Len(t, data.Items, 2)
	assert.Equal(t, "m-3", data.Items[0].Model, "ordered by distinct buyers")
	assert.Equal(t, int64(3), data.Items[0].Buyers)
	assert.Equal(t, "m-2", data.Items[1].Model)
	assert.Equal(t, int64(2), data.Items[1].Buyers)

	all := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?limit=100"))
	assert.Len(t, all.Items, 3)

	// An out-of-range limit falls back to the default, never an error.
	fallback := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?limit=abc"))
	assert.Len(t, fallback.Items, 3)
}

func TestLeaderboardSellersAnonymized(t *testing.T) {
	f := newLeaderboardFixture(t)
	now := time.Now()
	// Seller 10011: buyer 100 twice (deduplicated) plus buyer 101; one failed
	// qualifying request from buyer 102 hurts the success rate but adds no
	// buyer or income.
	seedBoardRequest(t, f.db, "s-1", 100, 10011, "model-a", RequestSuccess, 10, 2000, 0, 100, now.Add(-time.Hour))
	seedBoardRequest(t, f.db, "s-2", 100, 10011, "model-a", RequestSuccess, 10, 2000, 0, 100, now.Add(-time.Hour))
	seedBoardRequest(t, f.db, "s-3", 101, 10011, "model-a", RequestSuccess, 10, 2000, 0, 100, now.Add(-time.Hour))
	seedBoardRequest(t, f.db, "s-4", 102, 10011, "model-a", RequestFailed, 10, 2000, 0, 0, now.Add(-time.Hour))
	// Seller 10012: a single buyer's 50 is capped to 5.
	seedBoardRequest(t, f.db, "s-5", 103, 10012, "model-b", RequestSuccess, 10, 2000, 0, 50, now.Add(-time.Hour))

	w := f.get(t, "/api/marketplace/leaderboard/sellers?range=24h")
	data := decodeLeaderboardData[sellerLeaderboard](t, w)
	require.Len(t, data.Items, 2)

	assert.Equal(t, 1, data.Items[0].Rank)
	assert.Regexp(t, regexp.MustCompile(`^分享者 #[0-9a-f]{4}$`), data.Items[0].Alias)
	assert.Equal(t, int64(2), data.Items[0].Buyers, "buyer 100 counts once")
	assert.Equal(t, int64(2), data.Items[0].Requests)
	assert.Equal(t, int64(750), data.Items[0].SuccessRateMilli, "3 successes of 4 qualifying requests")
	assert.Equal(t, int64(60), data.Items[0].IncomeMicro, "buyer 100's 200 and buyer 101's 100 capped at 30 each")
	assert.Equal(t, 2, data.Items[1].Rank)
	assert.Equal(t, int64(1), data.Items[1].Buyers)
	assert.Equal(t, int64(5), data.Items[1].IncomeMicro)
	assert.NotEqual(t, data.Items[0].Alias, data.Items[1].Alias)

	// The raw payload must not contain seller ids or the internal field.
	body := w.Body.String()
	assert.False(t, strings.Contains(body, `"seller_id"`), "seller_id must not be returned")
	assert.False(t, strings.Contains(body, `"listing_id"`), "listing_id must not be returned")
	assert.False(t, strings.Contains(body, `"buyer"`), "buyer fields must not be returned")

	// Aliases are stable, distinct and never contain the raw id.
	aliasA := sellerAlias(10011)
	aliasB := sellerAlias(10012)
	assert.Equal(t, aliasA, sellerAlias(10011))
	assert.NotEqual(t, aliasA, aliasB)
	assert.NotContains(t, aliasA, "10011")
	assert.NotContains(t, aliasB, "10012")
}

func TestLeaderboardCacheDoesNotMixQueries(t *testing.T) {
	f := newLeaderboardFixture(t)
	now := time.Now()
	seedBoardRequest(t, f.db, "cache-1", 100, 10011, "model-a", RequestSuccess, 10, 2000, 0, 100, now)

	first := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h&limit=20"))
	require.Len(t, first.Items, 1)
	assert.Equal(t, int64(1), first.Items[0].Buyers)

	// A write after the first snapshot is invisible for 60s on the same key...
	seedBoardRequest(t, f.db, "cache-2", 101, 10011, "model-a", RequestSuccess, 10, 2000, 0, 100, now)
	cached := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h&limit=20"))
	require.Len(t, cached.Items, 1)
	assert.Equal(t, int64(1), cached.Items[0].Buyers, "same key must hit the 60s cache")

	// ... but a different query signature has its own slot.
	otherLimit := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h&limit=5"))
	require.Len(t, otherLimit.Items, 1)
	assert.Equal(t, int64(2), otherLimit.Items[0].Buyers)
	week := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=7d&limit=20"))
	assert.Equal(t, int64(2), week.Items[0].Buyers)
	trend := decodeLeaderboardData[leaderboardTrend](t, f.get(t, "/api/marketplace/leaderboard/trend?days=1"))
	assert.Equal(t, int64(2), trend.Items[0].Requests)

	// After an explicit reset the same key recomputes.
	resetLeaderboardCache()
	fresh := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h&limit=20"))
	assert.Equal(t, int64(2), fresh.Items[0].Buyers)

	// A seller-board query has a distinct key and must not reuse the model
	// board's cached snapshot.
	sellers := decodeLeaderboardData[sellerLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/sellers?range=24h"))
	require.Len(t, sellers.Items, 1)
	assert.Equal(t, int64(2), sellers.Items[0].Buyers)
}

func TestLeaderboardEmptySamples(t *testing.T) {
	f := newLeaderboardFixture(t)

	models := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models"))
	assert.NotNil(t, models.Items)
	assert.Len(t, models.Items, 0)

	sellers := decodeLeaderboardData[sellerLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/sellers"))
	assert.NotNil(t, sellers.Items)
	assert.Len(t, sellers.Items, 0)

	trend := decodeLeaderboardData[leaderboardTrend](t, f.get(t, "/api/marketplace/leaderboard/trend?days=3"))
	require.Len(t, trend.Items, 3)
	for _, day := range trend.Items {
		assert.Zero(t, day.Requests)
		assert.Zero(t, day.Tokens)
	}
}

func TestLeaderboardTrendBucketsByShanghaiDay(t *testing.T) {
	f := newLeaderboardFixture(t)
	loc, err := time.LoadLocation(shanghaiTZ)
	require.NoError(t, err)
	now := time.Now()
	nowLocal := now.In(loc)
	today := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)
	yesterdayNoon := today.AddDate(0, 0, -1).Add(12 * time.Hour)

	// Today: model-a twice, model-b once.
	seedBoardRequest(t, f.db, "t-1", 100, 10011, "model-a", RequestSuccess, 10, 10, 5, 0, now)
	seedBoardRequest(t, f.db, "t-2", 100, 10011, "model-a", RequestSuccess, 10, 20, 5, 0, now)
	seedBoardRequest(t, f.db, "t-3", 101, 10012, "model-b", RequestSuccess, 10, 10, 5, 0, now)
	// Yesterday: model-a once.
	seedBoardRequest(t, f.db, "t-4", 100, 10011, "model-a", RequestSuccess, 10, 7, 3, 0, yesterdayNoon)
	// Outside the 2-day horizon.
	seedBoardRequest(t, f.db, "t-5", 100, 10011, "model-a", RequestSuccess, 10, 999, 999, 0, today.AddDate(0, 0, -5))

	data := decodeLeaderboardData[leaderboardTrend](t, f.get(t, "/api/marketplace/leaderboard/trend?days=2"))
	require.Len(t, data.Items, 2)
	assert.Equal(t, today.AddDate(0, 0, -1).Format("2006-01-02"), data.Items[0].Date)
	assert.Equal(t, int64(1), data.Items[0].Requests)
	assert.Equal(t, int64(10), data.Items[0].Tokens)
	assert.Equal(t, today.Format("2006-01-02"), data.Items[1].Date)
	assert.Equal(t, int64(3), data.Items[1].Requests)
	assert.Equal(t, int64(55), data.Items[1].Tokens)

	filtered := decodeLeaderboardData[leaderboardTrend](t, f.get(t, "/api/marketplace/leaderboard/trend?days=2&model=model-a"))
	assert.Equal(t, "model-a", filtered.Model)
	require.Len(t, filtered.Items, 2)
	assert.Equal(t, int64(1), filtered.Items[0].Requests)
	assert.Equal(t, int64(2), filtered.Items[1].Requests)
	assert.Equal(t, int64(40), filtered.Items[1].Tokens)
}
