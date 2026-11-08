package marketplace

// square_metrics_test.go — 广场可靠性指标聚合的单测：窗口分桶、状态色、
// 延迟/缓存命中/TPS 推导、以及无样本时返回 -1（前端隐藏）。

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func insertRequest(t *testing.T, db *gorm.DB, model string, listingID int64, status string, at time.Time, latencyMs, input, output, cacheRead int64) {
	t.Helper()
	rec := Request{
		RequestID:       uniqueTestKey("req-" + model + "-" + at.Format("150405.000000000")),
		BuyerUserID:     1,
		BuyerTokenID:    1,
		SellerID:        9,
		ListingID:       listingID,
		Model:           model,
		Tier:            TierFlat,
		Status:          status,
		LatencyMs:       latencyMs,
		InputTokens:     input,
		OutputTokens:    output,
		CacheReadTokens: cacheRead,
		CreatedAt:       at,
	}
	require.NoError(t, db.Create(&rec).Error)
}

func TestSquareMetrics(t *testing.T) {
	db := newListingTestDB(t)

	now := time.Now()
	// model-a: 3 requests in the last hour (2 ok, 1 fail), one older success
	// inside 24h, one success 3 days ago. Latency 100/200/300 → avg 200.
	insertRequest(t, db, "model-a", 1, "success", now.Add(-10*time.Minute), 100, 100, 20, 50)
	insertRequest(t, db, "model-a", 1, "success", now.Add(-20*time.Minute), 200, 100, 20, 50)
	insertRequest(t, db, "model-a", 1, "failed", now.Add(-30*time.Minute), 300, 0, 0, 0)
	insertRequest(t, db, "model-a", 1, "success", now.Add(-2*time.Hour), 200, 100, 20, 50)
	insertRequest(t, db, "model-a", 1, "success", now.Add(-72*time.Hour), 0, 100, 20, 0)
	// model-b: only one failure in 24h.
	insertRequest(t, db, "model-b", 2, "failed", now.Add(-5*time.Minute), 0, 0, 0, 0)

	metrics, err := loadSquareModelMetrics(db, now)
	require.NoError(t, err)

	a := metrics["model-a"]
	require.NotNil(t, a)
	assert.Equal(t, int64(666), a.SuccessRate1hMilli, "2/3 in the last hour")
	assert.Equal(t, int64(750), a.SuccessRate24hMilli, "3/4 in 24h")
	assert.Equal(t, int64(800), a.SuccessRate7dMilli, "4/5 in 7d")
	assert.Equal(t, int64(4), a.Requests24h)
	assert.Equal(t, int64(200), a.LatencyMs, "avg of the three measured 24h rows")
	// 24h measured rows: latency 100+200+300=600, output 20+20+0=40 → 40*1000/600.
	assert.Equal(t, int64(75), a.TpsPermille)
	// cache read 150 / (input 300 + cache 150) → 333.
	assert.Equal(t, int64(333), a.CacheHitPermille)
	assert.Equal(t, SquareStatusOffline, a.Status, "80% success falls below the 90% degraded floor")
	require.NotNil(t, a.LastSuccessAt)

	b := metrics["model-b"]
	require.NotNil(t, b)
	assert.Equal(t, int64(0), b.SuccessRate1hMilli)
	assert.Equal(t, int64(0), b.SuccessRate24hMilli)
	assert.Equal(t, SquareStatusOffline, b.Status)
	assert.Nil(t, b.LastSuccessAt)
}

func TestSquareMetricsEmptyBucketsAreMinusOne(t *testing.T) {
	db := newListingTestDB(t)
	now := time.Now()
	// A request 8 days old is outside every window: no metrics row at all.
	insertRequest(t, db, "model-old", 1, "success", now.Add(-8*24*time.Hour), 100, 10, 10, 0)
	metrics, err := loadSquareModelMetrics(db, now)
	require.NoError(t, err)
	assert.Nil(t, metrics["model-old"], "outside the 7d window means no entry, not a fake zero")
	assert.Equal(t, int64(-1), rateMilli(0, 0))
}

func TestSquareChannelMetrics(t *testing.T) {
	db := newListingTestDB(t)
	now := time.Now()

	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-metrics-channel"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)

	insertRequest(t, db, "deepseek-flash", listing.ID, "success", now.Add(-10*time.Minute), 120, 10, 5, 0)
	insertRequest(t, db, "deepseek-flash", listing.ID, "failed", now.Add(-20*time.Minute), 0, 0, 0, 0)

	metrics, err := loadSquareChannelMetrics(db, "deepseek-flash", now)
	require.NoError(t, err)
	m := metrics[SeedVendorDeepSeek]
	require.NotNil(t, m)
	assert.Equal(t, int64(500), m.SuccessRate24hMilli)
	assert.Equal(t, int64(2), m.Requests24h)
	assert.Equal(t, int64(120), m.LatencyMs)
	assert.Equal(t, SquareStatusOffline, m.Status, "50% success is offline")
	assert.NotNil(t, m.LastSuccessAt)
}
