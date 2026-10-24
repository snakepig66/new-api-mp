package marketplace

// api_stats.go — usage trends and homepage public stats (FRONTEND-REBUILD §5).
//
//   GET /records/usage/stats?days=7  buyer-only daily aggregation of
//                                    mp_requests (requests, tokens, 实付, 省了)
//   GET /stats/public                public homepage strip: 在售模型数, 最低倍率,
//                                    今日调用量, 平台模型数 — cached for 60s
//
// The public endpoint carries aggregates only: no seller id, no listing id, no
// buyer id and no key material (§3.1/§3.5), so it is safe without login.
//
// Route wiring is central. Because one router group cannot be both public and
// UserAuth-gated, two registration functions are exported. Both expect a
// group already scoped to /marketplace (like RegisterRecordRoutes):
//
//	RegisterStatsRoutes(apiRouter.Group("/marketplace", middleware.UserAuth()))
//	RegisterPublicStatsRoutes(apiRouter.Group("/marketplace"))
//
// Both answer in the upstream console envelope (common.ApiSuccess /
// common.ApiErrorStatus).

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// RegisterStatsRoutes mounts the buyer usage-trend endpoint on g. g MUST
// already carry middleware.UserAuth().
func RegisterStatsRoutes(g *gin.RouterGroup) {
	g.GET("/records/usage/stats", getUsageStats)
}

// RegisterPublicStatsRoutes mounts the public homepage-stats endpoint on g.
// g is the public /api group (no login required).
func RegisterPublicStatsRoutes(g *gin.RouterGroup) {
	g.GET("/stats/public", getPublicStats)
}

// usageDaysDefault / usageDaysMax bound the ?days= parameter of the trend
// endpoint.
const (
	usageDaysDefault = 7
	usageDaysMax     = 90
)

// usageDayStat is one day (Asia/Shanghai calendar day) of a buyer's usage.
// All amounts are 微元; Tokens counts every token reported by upstream
// (input + output + cache read + cache write).
type usageDayStat struct {
	Date             string `json:"date"` // YYYY-MM-DD, Asia/Shanghai
	Requests         int64  `json:"requests"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	Tokens           int64  `json:"tokens"`
	TotalDebitMicro  int64  `json:"total_debit_micro"`
	SavedMicro       int64  `json:"saved_micro"` // 官方价 − 实付
}

// buyerUsageStats is GET /records/usage/stats's payload. Items is dense:
// every day in [start, end] appears, with zeros for days without traffic.
type buyerUsageStats struct {
	Days  int            `json:"days"`
	Start string         `json:"start"` // YYYY-MM-DD, inclusive
	End   string         `json:"end"`   // YYYY-MM-DD, inclusive (today)
	Items []usageDayStat `json:"items"`
}

// parseUsageDays reads ?days=; invalid or non-positive values fall back to the
// 7-day default and the horizon is capped at 90 days.
func parseUsageDays(c *gin.Context) int {
	raw := c.Query("days")
	if raw == "" {
		return usageDaysDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return usageDaysDefault
	}
	if n > usageDaysMax {
		n = usageDaysMax
	}
	return n
}

// aggregateUsageDaily buckets one buyer's mp_requests rows into Asia/Shanghai
// calendar days over [today-(days-1), today]. Aggregation runs in Go rather
// than SQL date functions so SQLite/MySQL/PostgreSQL behave identically.
func aggregateUsageDaily(db *gorm.DB, userID int64, days int, now time.Time) (buyerUsageStats, error) {
	if days < 1 {
		days = 1
	}
	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		return buyerUsageStats{}, err
	}
	lt := now.In(loc)
	today := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	startDay := today.AddDate(0, 0, -(days - 1))
	endExclusive := today.AddDate(0, 0, 1)

	items := make([]usageDayStat, days)
	index := make(map[string]int, days)
	for i := 0; i < days; i++ {
		date := startDay.AddDate(0, 0, i).Format("2006-01-02")
		items[i] = usageDayStat{Date: date}
		index[date] = i
	}

	var rows []Request
	if err := db.Model(&Request{}).
		Select("created_at", "input_tokens", "output_tokens", "cache_read_tokens",
			"cache_write_tokens", "total_debit_micro", "official_cost_micro").
		Where("buyer_user_id = ? AND created_at >= ? AND created_at < ?", userID, startDay, endExclusive).
		Find(&rows).Error; err != nil {
		return buyerUsageStats{}, err
	}
	for i := range rows {
		row := &rows[i]
		idx, ok := index[row.CreatedAt.In(loc).Format("2006-01-02")]
		if !ok {
			// Outside the requested horizon (can only happen on a timezone
			// mismatch); do not silently fold it into a neighbouring day.
			continue
		}
		day := &items[idx]
		day.Requests++
		day.InputTokens += row.InputTokens
		day.OutputTokens += row.OutputTokens
		day.CacheReadTokens += row.CacheReadTokens
		day.CacheWriteTokens += row.CacheWriteTokens
		day.Tokens += row.InputTokens + row.OutputTokens + row.CacheReadTokens + row.CacheWriteTokens
		day.TotalDebitMicro += row.TotalDebitMicro
		day.SavedMicro += row.OfficialCostMicro - row.TotalDebitMicro
	}
	return buyerUsageStats{
		Days:  days,
		Start: startDay.Format("2006-01-02"),
		End:   today.Format("2006-01-02"),
		Items: items,
	}, nil
}

// getUsageStats serves GET /records/usage/stats for the logged-in buyer only.
func getUsageStats(c *gin.Context) {
	userID := consoleUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	db := consoleDB(c)
	if db == nil {
		return
	}
	stats, err := aggregateUsageDaily(db, userID, parseUsageDays(c), time.Now())
	if err != nil {
		common.SysError(common.LogText("marketplace usage stats: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query usage stats"))
		return
	}
	common.ApiSuccess(c, stats)
}

// publicStats is GET /stats/public's payload. It deliberately contains only
// platform-level aggregates; every field is safe to show to anonymous
// visitors (§3.1/§3.5).
type publicStats struct {
	// ModelsOnSale is the number of distinct (model, vendor) pairs with at
	// least one live or canary listing.
	ModelsOnSale int64 `json:"models_on_sale"`
	// MinMultiplierMilli is the lowest multiplier_milli among live listings
	// (canary supply does not set the advertised floor); 0 when none is live.
	MinMultiplierMilli int64 `json:"min_multiplier_milli"`
	// TodayRequests is the number of mp_requests rows dated today
	// (Asia/Shanghai), including failed requests.
	TodayRequests int64 `json:"today_requests"`
	// PlatformModels is the number of distinct models in the price book.
	PlatformModels int64     `json:"platform_models"`
	GeneratedAt    time.Time `json:"generated_at"`
}

// publicStatsTTL is the FRONTEND-REBUILD §5 cache horizon: the homepage strip
// is refreshed at most once per minute.
const publicStatsTTL = time.Minute

// publicStatsCache is a single process-wide cache slot. The data is
// identical for every caller, so no per-user or per-query key is needed.
var publicStatsCache struct {
	mu    sync.Mutex
	at    time.Time
	value publicStats
	valid bool
}

// resetPublicStatsCache drops the cached snapshot (tests, and any future
// admin "refresh now" path).
func resetPublicStatsCache() {
	publicStatsCache.mu.Lock()
	publicStatsCache.valid = false
	publicStatsCache.at = time.Time{}
	publicStatsCache.value = publicStats{}
	publicStatsCache.mu.Unlock()
}

// cachedPublicStats returns the cached snapshot when it is younger than
// publicStatsTTL, otherwise recomputes and refreshes it. Cache misses are the
// only path that touches the database.
func cachedPublicStats(db *gorm.DB, now time.Time) (publicStats, error) {
	publicStatsCache.mu.Lock()
	defer publicStatsCache.mu.Unlock()
	if publicStatsCache.valid {
		age := now.Sub(publicStatsCache.at)
		if age >= 0 && age < publicStatsTTL {
			return publicStatsCache.value, nil
		}
	}
	stats, err := computePublicStats(db, now)
	if err != nil {
		return publicStats{}, err
	}
	publicStatsCache.value = stats
	publicStatsCache.at = now
	publicStatsCache.valid = true
	return stats, nil
}

// computePublicStats runs the four aggregate queries behind /stats/public.
func computePublicStats(db *gorm.DB, now time.Time) (publicStats, error) {
	stats := publicStats{GeneratedAt: now}

	// Distinct (model, vendor) with live/canary supply and the live-only
	// multiplier floor. The models column is JSON text, so grouping happens
	// in Go (same trade-off as hasLiveListingForModel / loadSquareSupply).
	var listings []Listing
	if err := db.Model(&Listing{}).
		Select("vendor", "models", "multiplier_milli", "status").
		Where("status IN ?", []string{ListingLive, ListingCanary}).
		Find(&listings).Error; err != nil {
		return publicStats{}, err
	}
	supply := make(map[[2]string]struct{})
	haveLive := false
	for i := range listings {
		l := &listings[i]
		for _, model := range l.Models {
			supply[[2]string{model, l.Vendor}] = struct{}{}
		}
		if l.Status == ListingLive {
			if !haveLive || l.MultiplierMilli < stats.MinMultiplierMilli {
				stats.MinMultiplierMilli = l.MultiplierMilli
				haveLive = true
			}
		}
	}
	stats.ModelsOnSale = int64(len(supply))
	if !haveLive {
		stats.MinMultiplierMilli = 0
	}

	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		return publicStats{}, err
	}
	lt := now.In(loc)
	dayStart := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)
	if err := db.Model(&Request{}).
		Where("created_at >= ? AND created_at < ?", dayStart, dayEnd).
		Count(&stats.TodayRequests).Error; err != nil {
		return publicStats{}, err
	}

	if err := db.Model(&PriceBook{}).Distinct("model").Count(&stats.PlatformModels).Error; err != nil {
		return publicStats{}, err
	}
	return stats, nil
}

// getPublicStats serves GET /stats/public (no login).
func getPublicStats(c *gin.Context) {
	db := consoleDB(c)
	if db == nil {
		return
	}
	stats, err := cachedPublicStats(db, time.Now())
	if err != nil {
		common.SysError(common.LogText("marketplace public stats: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query public stats"))
		return
	}
	common.ApiSuccess(c, stats)
}
