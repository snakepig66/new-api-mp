package marketplace

// square_metrics.go — 模型广场的公开可靠性指标.
//
// Every number here is DERIVED, never guessed:
//
//   - success rates: mp_requests.status ("success" vs anything else) bucketed
//     over the last 1h / 24h / 7d;
//   - latency: the 24h average of mp_requests.latency_ms (wall-clock relay
//     duration recorded by RelaySession.insertRecord; rows before the field
//     existed carry 0 and are excluded);
//   - TPS: output tokens per second of relay time, Σoutput / Σlatency;
//   - cache hit: cache_read / (input + cache_read) over 24h, in permille
//     (1000 = every prompt token was a cache read);
//   - last success: MAX(created_at) of successful requests.
//
// A bucket with no sample is reported as -1 so the UI can hide the column
// instead of printing a fake 0 (P0: 没数据就隐藏). The API is public and
// aggregate-only: no listing id, seller id or key material leaves this file.

import (
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Square status values (P0 three-state palette).
const (
	SquareStatusAvailable = "available"
	SquareStatusDegraded  = "degraded"
	SquareStatusOffline   = "offline"
)

// Square degradation thresholds: 98% and 90% success over 24h.
const (
	squareAvailablePermille = 980
	squareDegradedPermille  = 900
)

// squareMetrics is the public reliability summary of one standard model.
type squareMetrics struct {
	Status              string     `json:"status"`                 // available / degraded / offline
	SuccessRate1hMilli  int64      `json:"success_rate_1h_milli"`  // -1 = no sample
	SuccessRate24hMilli int64      `json:"success_rate_24h_milli"` // -1 = no sample
	SuccessRate7dMilli  int64      `json:"success_rate_7d_milli"`  // -1 = no sample
	Requests24h         int64      `json:"requests_24h"`
	LatencyMs           int64      `json:"latency_ms"`         // 24h avg, 0 = no sample
	TpsPermille         int64      `json:"tps_permille"`       // output tok/s ×1000, 0 = no sample
	CacheHitPermille    int64      `json:"cache_hit_permille"` // -1 = no sample
	LastSuccessAt       *time.Time `json:"last_success_at"`
	SampledAt           time.Time  `json:"sampled_at"`
}

// squareChannelMetrics is the per-channel reliability of one model.
type squareChannelMetrics struct {
	Status              string     `json:"status"`
	SuccessRate24hMilli int64      `json:"success_rate_24h_milli"`
	Requests24h         int64      `json:"requests_24h"`
	LatencyMs           int64      `json:"latency_ms"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
}

// rateMilli converts total/success counts into a 0..1000 rate; an empty
// sample is -1 (the UI hides it).
func rateMilli(total, success int64) int64 {
	if total <= 0 {
		return -1
	}
	return success * 1000 / total
}

// squareStatusOf derives the three-state dot from the 24h window. No sample
// at all means offline; a degraded rate or a very low sample size with
// failures means degraded.
func squareStatusOf(total, success int64) string {
	if total <= 0 {
		return SquareStatusOffline
	}
	rate := rateMilli(total, success)
	switch {
	case rate >= squareAvailablePermille:
		return SquareStatusAvailable
	case rate >= squareDegradedPermille:
		return SquareStatusDegraded
	default:
		return SquareStatusOffline
	}
}

// modelMetricsRow is one grouped mp_requests aggregate. LastSuccess is a
// string because SQLite's MAX(datetime) comes back as text while MySQL/Postgres
// return a time; parseDBTime normalizes all three.
type modelMetricsRow struct {
	Model        string
	Total7d      int64
	Success7d    int64
	Total24h     int64
	Success24h   int64
	Total1h      int64
	Success1h    int64
	LatencySum   int64
	LatencyCount int64
	OutputTokens int64
	CacheRead    int64
	InputTokens  int64
	LastSuccess  *string
}

// parseDBTime parses the datetime text SQLite emits, plus the common MySQL and
// Postgres layouts. An unparseable value yields nil (the UI hides it).
func parseDBTime(raw *string) *time.Time {
	if raw == nil {
		return nil
	}
	text := strings.TrimSpace(*raw)
	if text == "" {
		return nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999999Z07:00",
	} {
		if t, err := time.Parse(layout, text); err == nil {
			return &t
		}
	}
	return nil
}

const metricsWindow7d = 7 * 24 * time.Hour

// loadSquareModelMetrics aggregates the whole square in one pass over the 7d
// window. SQL stays portable (CASE WHEN, AVG over NULL) across SQLite, MySQL
// and Postgres, the three drivers the test matrix runs.
func loadSquareModelMetrics(db *gorm.DB, now time.Time) (map[string]*squareMetrics, error) {
	since7d := now.Add(-metricsWindow7d)
	since24h := now.Add(-24 * time.Hour)
	since1h := now.Add(-time.Hour)

	var rows []modelMetricsRow
	err := db.Model(&Request{}).
		Select(`model,
			COUNT(*) AS total7d,
			SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS success7d,
			SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END) AS total24h,
			SUM(CASE WHEN created_at >= ? AND status = 'success' THEN 1 ELSE 0 END) AS success24h,
			SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END) AS total1h,
			SUM(CASE WHEN created_at >= ? AND status = 'success' THEN 1 ELSE 0 END) AS success1h,
			SUM(CASE WHEN created_at >= ? THEN latency_ms ELSE 0 END) AS latency_sum,
			SUM(CASE WHEN created_at >= ? AND latency_ms > 0 THEN 1 ELSE 0 END) AS latency_count,
			SUM(CASE WHEN created_at >= ? THEN output_tokens ELSE 0 END) AS output_tokens,
			SUM(CASE WHEN created_at >= ? THEN cache_read_tokens ELSE 0 END) AS cache_read,
			SUM(CASE WHEN created_at >= ? THEN input_tokens ELSE 0 END) AS input_tokens,
			MAX(CASE WHEN status = 'success' THEN created_at END) AS last_success`,
			since24h, since24h, since1h, since1h, since24h, since24h,
			since24h, since24h, since24h).
		Where("created_at >= ?", since7d).
		Group("model").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]*squareMetrics, len(rows))
	for i := range rows {
		row := &rows[i]
		m := &squareMetrics{
			Status:              squareStatusOf(row.Total24h, row.Success24h),
			SuccessRate1hMilli:  rateMilli(row.Total1h, row.Success1h),
			SuccessRate24hMilli: rateMilli(row.Total24h, row.Success24h),
			SuccessRate7dMilli:  rateMilli(row.Total7d, row.Success7d),
			Requests24h:         row.Total24h,
			CacheHitPermille:    -1,
			LastSuccessAt:       parseDBTime(row.LastSuccess),
			SampledAt:           now,
		}
		if row.LatencyCount > 0 {
			m.LatencyMs = row.LatencySum / row.LatencyCount
		}
		if row.LatencySum > 0 && row.OutputTokens > 0 {
			// tokens × 1000 / milliseconds = tokens per second ×1000.
			m.TpsPermille = row.OutputTokens * 1000 / row.LatencySum
		}
		if prompt := row.InputTokens + row.CacheRead; prompt > 0 {
			m.CacheHitPermille = row.CacheRead * 1000 / prompt
		}
		out[row.Model] = m
	}
	return out, nil
}

// channelMetricsRow is one (model, channel) 24h aggregate.
type channelMetricsRow struct {
	Channel      string
	Total        int64
	Success      int64
	LatencySum   int64
	LatencyCount int64
	LastSuccess  *string
}

// loadSquareChannelMetrics aggregates one model's 24h reliability per seller
// channel. The channel is the listing's vendor slug; a custom channel shows
// under its host-derived slug (custom-…), which the detail page renders as
// 第三方 rather than leaking the seller.
func loadSquareChannelMetrics(db *gorm.DB, model string, now time.Time) (map[string]*squareChannelMetrics, error) {
	since24h := now.Add(-24 * time.Hour)
	var rows []channelMetricsRow
	err := db.Table("mp_requests AS r").
		Select(`l.vendor AS channel,
			COUNT(*) AS total,
			SUM(CASE WHEN r.status = 'success' THEN 1 ELSE 0 END) AS success,
			SUM(r.latency_ms) AS latency_sum,
			SUM(CASE WHEN r.latency_ms > 0 THEN 1 ELSE 0 END) AS latency_count,
			MAX(CASE WHEN r.status = 'success' THEN r.created_at END) AS last_success`).
		Joins("JOIN mp_listings AS l ON l.id = r.listing_id").
		Where("r.model = ? AND r.created_at >= ?", model, since24h).
		Group("l.vendor").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]*squareChannelMetrics, len(rows))
	for i := range rows {
		row := &rows[i]
		m := &squareChannelMetrics{
			Status:              squareStatusOf(row.Total, row.Success),
			SuccessRate24hMilli: rateMilli(row.Total, row.Success),
			Requests24h:         row.Total,
			LastSuccessAt:       parseDBTime(row.LastSuccess),
		}
		if row.LatencyCount > 0 {
			m.LatencyMs = row.LatencySum / row.LatencyCount
		}
		out[row.Channel] = m
	}
	return out, nil
}

// squareMetricsTTL bounds how often the public square recomputes the
// aggregate scan over mp_requests. The numbers are a 1h/24h/7d summary, so a
// minute of staleness is invisible to buyers and keeps the public page cheap.
const squareMetricsTTL = time.Minute

var squareMetricsCache struct {
	mu    sync.Mutex
	at    time.Time
	value map[string]*squareMetrics
	valid bool
}

// cachedSquareModelMetrics returns the process-wide metrics snapshot, older
// than squareMetricsTTL at most. Cache misses are the only DB path.
func cachedSquareModelMetrics(db *gorm.DB, now time.Time) (map[string]*squareMetrics, error) {
	squareMetricsCache.mu.Lock()
	defer squareMetricsCache.mu.Unlock()
	if squareMetricsCache.valid {
		age := now.Sub(squareMetricsCache.at)
		if age >= 0 && age < squareMetricsTTL {
			return squareMetricsCache.value, nil
		}
	}
	metrics, err := loadSquareModelMetrics(db, now)
	if err != nil {
		return nil, err
	}
	squareMetricsCache.value = metrics
	squareMetricsCache.at = now
	squareMetricsCache.valid = true
	return metrics, nil
}

// resetSquareMetricsCache drops the snapshot (tests).
func resetSquareMetricsCache() {
	squareMetricsCache.mu.Lock()
	squareMetricsCache.valid = false
	squareMetricsCache.at = time.Time{}
	squareMetricsCache.value = nil
	squareMetricsCache.mu.Unlock()
}
