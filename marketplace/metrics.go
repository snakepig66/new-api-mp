package marketplace

// metrics.go — 派生运营指标 (§5 P5): 断流率、503 原因、指纹异常、订阅信号、
// 槽位快照和预占偏差. Request-derived metrics are aggregated from mp_requests
// into hour buckets of mp_ops_metrics; the process-local slot counters are
// snapshotted separately because they never reach the database otherwise.
//
// The aggregation is idempotent: every bucket it recomputes has its previous
// rows replaced in the same transaction, so a retried or repeated hourly run
// never doubles a value.

import (
	"maps"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// opsMetricBucket is the width of one mp_ops_metrics bucket. Buckets are
// anchored at the process-local hour, matching how mp_requests.created_at is
// written, so a bucket boundary never disagrees with the stored timestamps.
const opsMetricBucket = time.Hour

// opsAggregatedMetricNames are the request-derived metric names one bucket
// recomputation owns. Deleting exactly these before inserting keeps a re-run
// idempotent without touching the slot snapshots.
var opsAggregatedMetricNames = []string{
	MetricStreamCutRate,
	Metric503Reason,
	MetricFingerprintAnomaly,
	MetricSubscriptionSignal,
	MetricPreFreezeDeviation,
}

// opsMetricFailureEvents are the retry-chain events that describe why a failed
// request ended. Routing mechanics (select/switch/inplace_retry/strip_tools)
// are excluded: they also appear on successful requests and are not failure
// reasons.
var opsMetricFailureEvents = []string{
	HopEventError,
	HopEventBreaker429,
	HopEventAutoPause,
	HopEventPlanQuotaExhausted,
	HopEventZeroOutput,
	HopEventStreamCut,
	HopEventEarlyExit,
	HopEventSettleShortfall,
	HopEventMaxOutputSkipped,
}

// opsMetricHop is the subset of a retry-chain hop the aggregation reads.
type opsMetricHop struct {
	Event string `json:"event"`
}

// newOpsMetric builds one metric row. A dimensions object that cannot be
// marshaled degrades to an empty object rather than dropping the row.
func newOpsMetric(name string, value float64, dimensions map[string]any, bucket time.Time) OpsMetric {
	raw, err := common.Marshal(dimensions)
	if err != nil {
		raw = []byte("{}")
	}
	return OpsMetric{
		MetricName:  name,
		MetricValue: value,
		Dimensions:  string(raw),
		BucketTime:  bucket,
	}
}

// AggregateOpsMetrics recomputes the derived metrics of every hour bucket in
// [since, now). It is safe to call repeatedly: each bucket is replaced, not
// appended to.
func AggregateOpsMetrics(db *gorm.DB, since time.Time) error {
	if db == nil {
		return nil
	}
	now := time.Now()
	if since.IsZero() {
		since = now.Add(-opsMetricBucket)
	}
	start := since.Truncate(opsMetricBucket)
	for bucket := start; bucket.Before(now); bucket = bucket.Add(opsMetricBucket) {
		if err := aggregateOpsBucket(db, bucket, bucket.Add(opsMetricBucket)); err != nil {
			return err
		}
	}
	return nil
}

// aggregateOpsBucket computes one bucket's request-derived metrics from
// mp_requests and replaces the bucket's rows.
func aggregateOpsBucket(db *gorm.DB, bucketStart, bucketEnd time.Time) error {
	var rows []Request
	if err := db.Select("status", "retry_chain", "model_fingerprint_anomaly",
		"capped_micro", "total_debit_micro").
		Where("created_at >= ? AND created_at < ?", bucketStart, bucketEnd).
		Find(&rows).Error; err != nil {
		return err
	}

	var total, streamCut, fingerprintAnomaly, subscriptionSignal int64
	var cappedRequests, cappedMicro, debitMicro int64
	reasons := make(map[string]int64)
	for i := range rows {
		row := &rows[i]
		total++
		if row.ModelFingerprintAnomaly {
			fingerprintAnomaly++
		}
		if row.CappedMicro > 0 {
			cappedRequests++
			cappedMicro += row.CappedMicro
		}
		debitMicro += row.TotalDebitMicro

		events := make(map[string]struct{})
		if row.RetryChain != "" {
			var hops []opsMetricHop
			if err := common.UnmarshalJsonStr(row.RetryChain, &hops); err == nil {
				for _, hop := range hops {
					if hop.Event != "" {
						events[hop.Event] = struct{}{}
					}
				}
			}
		}
		if _, ok := events[HopEventStreamCut]; ok {
			streamCut++
		}
		if _, ok := events[HopEventPlanQuotaExhausted]; ok {
			subscriptionSignal++
		}
		if row.Status == RequestFailed {
			for event := range events {
				if slices.Contains(opsMetricFailureEvents, event) {
					reasons[event]++
				}
			}
		}
	}

	metrics := make([]OpsMetric, 0, len(opsAggregatedMetricNames)+len(reasons))
	appendMetric := func(name string, value float64, dimensions map[string]any) {
		metrics = append(metrics, newOpsMetric(name, value, dimensions, bucketStart))
	}

	streamCutRate := 0.0
	if total > 0 {
		streamCutRate = float64(streamCut) / float64(total)
	}
	appendMetric(MetricStreamCutRate, streamCutRate, map[string]any{
		"total": total,
		"hits":  streamCut,
	})
	appendMetric(MetricFingerprintAnomaly, float64(fingerprintAnomaly), map[string]any{
		"total": total,
	})
	appendMetric(MetricSubscriptionSignal, float64(subscriptionSignal), map[string]any{
		"total": total,
	})
	// P0 caps the settlement at the pre-freeze, so the deviation (actual minus
	// frozen) is never positive; what the platform absorbed is capped_micro.
	// The ratio is measured against the uncapped total (capped + charged).
	deviationRatio := 0.0
	if cappedMicro+debitMicro > 0 {
		deviationRatio = float64(cappedMicro) / float64(cappedMicro+debitMicro)
	}
	appendMetric(MetricPreFreezeDeviation, float64(cappedMicro), map[string]any{
		"total":           total,
		"capped_requests": cappedRequests,
		"ratio":           deviationRatio,
	})
	for _, reason := range slices.Sorted(maps.Keys(reasons)) {
		appendMetric(Metric503Reason, float64(reasons[reason]), map[string]any{
			"reason": reason,
		})
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("bucket_time = ? AND metric_name IN ?", bucketStart,
			opsAggregatedMetricNames).Delete(&OpsMetric{}).Error; err != nil {
			return err
		}
		return tx.Create(&metrics).Error
	})
}

// SnapshotSlotMetrics writes one hour bucket of the process-local slot
// counters: slot_usage carries the currently held slots, slot_renew_failure
// the cumulative renew failures. Counters are process-lifetime totals for this
// process (see SlotManager), so a multi-node deployment publishes one row per
// node and the console shows the per-process values.
func SnapshotSlotMetrics(db *gorm.DB, at time.Time, stats map[string]int64) error {
	if db == nil {
		return nil
	}
	bucket := at.Truncate(opsMetricBucket)
	rows := make([]OpsMetric, 0, 2)
	rows = append(rows, newOpsMetric(MetricSlotUsage, float64(stats["in_flight"]), map[string]any{
		"in_flight": stats["in_flight"],
		"acquire":   stats["acquire"],
		"renew":     stats["renew"],
		"release":   stats["release"],
	}, bucket))
	rows = append(rows, newOpsMetric(MetricSlotRenewFailure, float64(stats["renew_failure"]), map[string]any{
		"renew":         stats["renew"],
		"renew_failure": stats["renew_failure"],
	}, bucket))
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("bucket_time = ? AND metric_name IN ?", bucket,
			[]string{MetricSlotUsage, MetricSlotRenewFailure}).Delete(&OpsMetric{}).Error; err != nil {
			return err
		}
		return tx.Create(&rows).Error
	})
}
