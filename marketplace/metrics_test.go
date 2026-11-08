package marketplace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// newOpsMetricsTestDB creates just the two tables the aggregation reads and
// writes; the full Migrate contract is covered by migrate_test.go.
func newOpsMetricsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	require.NoError(t, db.AutoMigrate(&Request{}, &OpsMetric{}))
	return db
}

// seedOpsRequest inserts one mp_requests row with the given retry-chain events.
func seedOpsRequest(t *testing.T, db *gorm.DB, requestID, status string, anomaly bool, capped, debit int64, at time.Time, events ...string) {
	t.Helper()
	hops := make([]RelayHop, 0, len(events))
	for i, event := range events {
		hops = append(hops, RelayHop{Seq: i + 1, Event: event})
	}
	raw, err := common.Marshal(hops)
	require.NoError(t, err)
	require.NoError(t, db.Create(&Request{
		RequestID:               requestID,
		BuyerUserID:             1,
		BuyerTokenID:            2,
		SellerID:                3,
		ListingID:               4,
		Model:                   "deepseek-flash",
		ModelFingerprintAnomaly: anomaly,
		Tier:                    TierFlat,
		Status:                  status,
		RetryChain:              string(raw),
		TotalDebitMicro:         debit,
		CappedMicro:             capped,
		CreatedAt:               at,
	}).Error)
}

func opsMetricsAt(t *testing.T, db *gorm.DB, name string, bucket time.Time) []OpsMetric {
	t.Helper()
	var rows []OpsMetric
	require.NoError(t, db.Where("metric_name = ? AND bucket_time = ?", name, bucket).
		Order("id ASC").Find(&rows).Error)
	return rows
}

func decodeOpsDimensions(t *testing.T, row OpsMetric) map[string]any {
	t.Helper()
	var dimensions map[string]any
	require.NoError(t, common.UnmarshalJsonStr(row.Dimensions, &dimensions))
	return dimensions
}

// TestAggregateOpsMetrics covers the request-derived metrics: stream-cut rate,
// 503 reasons, fingerprint anomalies, subscription signals and the pre-freeze
// deviation total. A repeated run must not duplicate a bucket's rows.
func TestAggregateOpsMetrics(t *testing.T) {
	db := newOpsMetricsTestDB(t)
	now := time.Now()
	at := now.Add(-30 * time.Minute)
	bucket := at.Truncate(opsMetricBucket)

	// 4 requests in one bucket: one stream cut (success status), one failed plan
	// quota signal, one fingerprint anomaly, one failed with an error + 429 and
	// a capped settle.
	seedOpsRequest(t, db, "ops-1", RequestSuccess, false, 0, 100, at,
		HopEventSelect, HopEventStreamCut)
	seedOpsRequest(t, db, "ops-2", RequestFailed, false, 0, 0, at,
		HopEventSelect, HopEventPlanQuotaExhausted)
	seedOpsRequest(t, db, "ops-3", RequestSuccess, true, 0, 200, at,
		HopEventSelect)
	seedOpsRequest(t, db, "ops-4", RequestFailed, false, 1000, 100, at,
		HopEventSelect, HopEventError, HopEventBreaker429)

	require.NoError(t, AggregateOpsMetrics(db, now.Add(-14*time.Hour)))

	rateRows := opsMetricsAt(t, db, MetricStreamCutRate, bucket)
	require.Len(t, rateRows, 1)
	assert.InDelta(t, 0.25, rateRows[0].MetricValue, 1e-9)
	rateDims := decodeOpsDimensions(t, rateRows[0])
	assert.EqualValues(t, 4, rateDims["total"])
	assert.EqualValues(t, 1, rateDims["hits"])

	anomalyRows := opsMetricsAt(t, db, MetricFingerprintAnomaly, bucket)
	require.Len(t, anomalyRows, 1)
	assert.EqualValues(t, 1, anomalyRows[0].MetricValue)

	signalRows := opsMetricsAt(t, db, MetricSubscriptionSignal, bucket)
	require.Len(t, signalRows, 1)
	assert.EqualValues(t, 1, signalRows[0].MetricValue)

	// 503 reasons are grouped by event type; routing mechanics such as "select"
	// are not reasons and are excluded.
	reasonRows := opsMetricsAt(t, db, Metric503Reason, bucket)
	require.Len(t, reasonRows, 3)
	reasons := make(map[string]float64, len(reasonRows))
	for _, row := range reasonRows {
		reasons[decodeOpsDimensions(t, row)["reason"].(string)] = row.MetricValue
	}
	assert.Equal(t, map[string]float64{
		HopEventPlanQuotaExhausted: 1,
		HopEventError:              1,
		HopEventBreaker429:         1,
	}, reasons)

	deviationRows := opsMetricsAt(t, db, MetricPreFreezeDeviation, bucket)
	require.Len(t, deviationRows, 1)
	assert.EqualValues(t, 1000, deviationRows[0].MetricValue)
	deviationDims := decodeOpsDimensions(t, deviationRows[0])
	assert.EqualValues(t, 1, deviationDims["capped_requests"])
	// Capped 1000 against the remaining charged debit (100 + 200 + 100).
	assert.InDelta(t, 1000.0/1400.0, deviationDims["ratio"], 1e-9)

	// Idempotent: rewriting the same buckets keeps the row count.
	var before int64
	require.NoError(t, db.Model(&OpsMetric{}).Count(&before).Error)
	require.NoError(t, AggregateOpsMetrics(db, now.Add(-14*time.Hour)))
	var after int64
	require.NoError(t, db.Model(&OpsMetric{}).Count(&after).Error)
	assert.Equal(t, before, after)
}

// TestAggregateOpsMetricsEmptyBucket keeps a zero row for the continuous
// metrics so a chart has no gaps; 503 reasons stay absent.
func TestAggregateOpsMetricsEmptyBucket(t *testing.T) {
	db := newOpsMetricsTestDB(t)
	bucket := time.Now().Truncate(opsMetricBucket)
	require.NoError(t, AggregateOpsMetrics(db, time.Now()))

	rateRows := opsMetricsAt(t, db, MetricStreamCutRate, bucket)
	require.Len(t, rateRows, 1)
	assert.Zero(t, rateRows[0].MetricValue)
	assert.EqualValues(t, 0, decodeOpsDimensions(t, rateRows[0])["total"])
	assert.Empty(t, opsMetricsAt(t, db, Metric503Reason, bucket))
}

// TestSnapshotSlotMetrics covers the process-local slot snapshot and its
// per-bucket idempotency.
func TestSnapshotSlotMetrics(t *testing.T) {
	db := newOpsMetricsTestDB(t)
	now := time.Now()
	stats := map[string]int64{
		"acquire":       10,
		"renew":         4,
		"renew_failure": 2,
		"release":       8,
		"in_flight":     2,
	}
	require.NoError(t, SnapshotSlotMetrics(db, now, stats))
	require.NoError(t, SnapshotSlotMetrics(db, now, stats))
	bucket := now.Truncate(opsMetricBucket)

	usageRows := opsMetricsAt(t, db, MetricSlotUsage, bucket)
	require.Len(t, usageRows, 1)
	assert.EqualValues(t, 2, usageRows[0].MetricValue)
	assert.EqualValues(t, 10, decodeOpsDimensions(t, usageRows[0])["acquire"])

	failureRows := opsMetricsAt(t, db, MetricSlotRenewFailure, bucket)
	require.Len(t, failureRows, 1)
	assert.EqualValues(t, 2, failureRows[0].MetricValue)
}
