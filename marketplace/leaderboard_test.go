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
func seedBoardRequest(t *testing.T, db *gorm.DB, requestID, model string, sellerID int64,
	status string, latencyMs, inputTokens, outputTokens, sellerCredit int64, at time.Time) {
	t.Helper()
	require.NoError(t, db.Create(&Request{
		RequestID:         requestID,
		SellerID:          sellerID,
		Model:             model,
		Tier:              TierFlat,
		PriceVersion:      1,
		MultiplierMilli:   1000,
		InputTokens:       inputTokens,
		OutputTokens:      outputTokens,
		SellerCreditMicro: sellerCredit,
		Status:            status,
		LatencyMs:         latencyMs,
		CreatedAt:         at,
	}).Error)
}

func TestLeaderboardModelsAggregation(t *testing.T) {
	f := newLeaderboardFixture(t)
	now := time.Now()

	// model-a: 2 successes + 1 failure, latency 100/200/300, tokens 120+60+30.
	seedBoardRequest(t, f.db, "a-1", "model-a", 10011, RequestSuccess, 100, 100, 20, 0, now.Add(-2*time.Hour))
	seedBoardRequest(t, f.db, "a-2", "model-a", 10011, RequestSuccess, 200, 50, 10, 0, now.Add(-1*time.Hour))
	seedBoardRequest(t, f.db, "a-3", "model-a", 10012, RequestFailed, 300, 30, 0, 0, now.Add(-30*time.Minute))
	// model-b: 2 successes, latency 500/400.
	seedBoardRequest(t, f.db, "b-1", "model-b", 10012, RequestSuccess, 500, 60, 40, 0, now.Add(-3*time.Hour))
	seedBoardRequest(t, f.db, "b-2", "model-b", 10012, RequestSuccess, 400, 10, 5, 0, now.Add(-4*time.Hour))
	// model-c is outside the 24h window but inside 7d.
	seedBoardRequest(t, f.db, "c-1", "model-c", 10011, RequestSuccess, 10, 1, 1, 0, now.Add(-26*time.Hour))

	data := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h"))
	assert.Equal(t, leaderboardRange24h, data.Range)
	assert.Equal(t, "sliding", data.Window)
	assert.True(t, data.WindowEnd.After(data.WindowStart))
	require.Len(t, data.Items, 2)

	assert.Equal(t, 1, data.Items[0].Rank)
	assert.Equal(t, "model-a", data.Items[0].Model)
	assert.Equal(t, int64(3), data.Items[0].Requests)
	assert.Equal(t, int64(666), data.Items[0].SuccessRateMilli)
	assert.Equal(t, int64(200), data.Items[0].AvgLatencyMs)
	assert.Equal(t, int64(210), data.Items[0].Tokens, "tokens = input + output")

	assert.Equal(t, 2, data.Items[1].Rank)
	assert.Equal(t, "model-b", data.Items[1].Model)
	assert.Equal(t, int64(2), data.Items[1].Requests)
	assert.Equal(t, int64(1000), data.Items[1].SuccessRateMilli)
	assert.Equal(t, int64(450), data.Items[1].AvgLatencyMs)
	assert.Equal(t, int64(115), data.Items[1].Tokens)

	// The 7d sliding window picks up model-c.
	week := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=7d"))
	assert.Equal(t, leaderboardRange7d, week.Range)
	require.Len(t, week.Items, 3)
	got := map[string]int64{}
	for _, item := range week.Items {
		got[item.Model] = item.Requests
	}
	assert.Equal(t, map[string]int64{"model-a": 3, "model-b": 2, "model-c": 1}, got)
}

func TestLeaderboardLimit(t *testing.T) {
	f := newLeaderboardFixture(t)
	now := time.Now()
	for i, model := range []string{"m-1", "m-2", "m-3"} {
		for n := 0; n <= i; n++ {
			seedBoardRequest(t, f.db, model+"-"+time.Duration(n).String(), model, 10011,
				RequestSuccess, 10, 1, 1, 0, now.Add(-time.Hour))
		}
	}
	data := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?limit=2"))
	require.Len(t, data.Items, 2)
	assert.Equal(t, "m-3", data.Items[0].Model, "ordered by request count")
	assert.Equal(t, "m-2", data.Items[1].Model)

	all := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?limit=100"))
	assert.Len(t, all.Items, 3)

	// An out-of-range limit falls back to the default, never an error.
	fallback := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?limit=abc"))
	assert.Len(t, fallback.Items, 3)
}

func TestLeaderboardSellersAnonymized(t *testing.T) {
	f := newLeaderboardFixture(t)
	now := time.Now()
	seedBoardRequest(t, f.db, "s-1", "model-a", 10011, RequestSuccess, 10, 1, 1, 100, now.Add(-time.Hour))
	seedBoardRequest(t, f.db, "s-2", "model-a", 10011, RequestSuccess, 10, 1, 1, 200, now.Add(-time.Hour))
	seedBoardRequest(t, f.db, "s-3", "model-a", 10011, RequestFailed, 10, 1, 1, 0, now.Add(-time.Hour))
	seedBoardRequest(t, f.db, "s-4", "model-b", 10012, RequestSuccess, 10, 1, 1, 50, now.Add(-time.Hour))

	w := f.get(t, "/api/marketplace/leaderboard/sellers?range=24h")
	data := decodeLeaderboardData[sellerLeaderboard](t, w)
	require.Len(t, data.Items, 2)

	assert.Equal(t, 1, data.Items[0].Rank)
	assert.Regexp(t, regexp.MustCompile(`^分享者 #[0-9a-f]{4}$`), data.Items[0].Alias)
	assert.Equal(t, int64(3), data.Items[0].Requests)
	assert.Equal(t, int64(666), data.Items[0].SuccessRateMilli)
	assert.Equal(t, int64(300), data.Items[0].IncomeMicro)
	assert.Equal(t, 2, data.Items[1].Rank)
	assert.Equal(t, int64(1), data.Items[1].Requests)
	assert.Equal(t, int64(50), data.Items[1].IncomeMicro)
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
	seedBoardRequest(t, f.db, "cache-1", "model-a", 10011, RequestSuccess, 10, 1, 1, 0, now)

	first := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h&limit=20"))
	require.Len(t, first.Items, 1)
	assert.Equal(t, int64(1), first.Items[0].Requests)

	// A write after the first snapshot is invisible for 60s on the same key...
	seedBoardRequest(t, f.db, "cache-2", "model-a", 10011, RequestSuccess, 10, 1, 1, 0, now)
	cached := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h&limit=20"))
	require.Len(t, cached.Items, 1)
	assert.Equal(t, int64(1), cached.Items[0].Requests, "same key must hit the 60s cache")

	// ... but a different query signature has its own slot.
	otherLimit := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h&limit=5"))
	require.Len(t, otherLimit.Items, 1)
	assert.Equal(t, int64(2), otherLimit.Items[0].Requests)
	week := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=7d&limit=20"))
	assert.Equal(t, int64(2), week.Items[0].Requests)
	trend := decodeLeaderboardData[leaderboardTrend](t, f.get(t, "/api/marketplace/leaderboard/trend?days=1"))
	assert.Equal(t, int64(2), trend.Items[0].Requests)

	// After an explicit reset the same key recomputes.
	resetLeaderboardCache()
	fresh := decodeLeaderboardData[modelLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/models?range=24h&limit=20"))
	assert.Equal(t, int64(2), fresh.Items[0].Requests)

	// A seller-board query has a distinct key and must not reuse the model
	// board's cached snapshot.
	sellers := decodeLeaderboardData[sellerLeaderboard](t, f.get(t, "/api/marketplace/leaderboard/sellers?range=24h"))
	require.Len(t, sellers.Items, 1)
	assert.Equal(t, int64(2), sellers.Items[0].Requests)
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
	seedBoardRequest(t, f.db, "t-1", "model-a", 10011, RequestSuccess, 10, 10, 5, 0, now)
	seedBoardRequest(t, f.db, "t-2", "model-a", 10011, RequestSuccess, 10, 20, 5, 0, now)
	seedBoardRequest(t, f.db, "t-3", "model-b", 10012, RequestSuccess, 10, 10, 5, 0, now)
	// Yesterday: model-a once.
	seedBoardRequest(t, f.db, "t-4", "model-a", 10011, RequestSuccess, 10, 7, 3, 0, yesterdayNoon)
	// Outside the 2-day horizon.
	seedBoardRequest(t, f.db, "t-5", "model-a", 10011, RequestSuccess, 10, 999, 999, 0, today.AddDate(0, 0, -5))

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
