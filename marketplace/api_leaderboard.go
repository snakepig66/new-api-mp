package marketplace

// api_leaderboard.go — 模型市场排行榜 (P6): public, read-only aggregates.
//
//	GET /marketplace/leaderboard/models?range=24h|7d&limit=20
//	GET /marketplace/leaderboard/sellers?range=24h|7d&limit=20
//	GET /marketplace/leaderboard/trend?days=30&model=<optional>
//
// Privacy contract (§3.1/§3.5): the public leaderboards carry aggregates only.
// No listing id, buyer id/token id, key material or seller id leaves this
// file. The seller board names a seller only by an anonymous alias derived
// from MP_KEY_SECRET with domain separation, never by their real id.
//
// Ranges are SLIDING windows (now-24h / now-7d), not Asia/Shanghai calendar
// days; the response spells that out in "window". The trend endpoint buckets
// by Asia/Shanghai calendar day in Go, because SQLite/MySQL/PostgreSQL date
// functions are not portable.
//
// Every endpoint is cached for 60 seconds (same approach as /stats/public):
// one process-wide, keyed snapshot per query signature.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// RegisterLeaderboardRoutes mounts the public leaderboard endpoints on g.
// g is the public /api/marketplace group: no login is required, and every
// response is an aggregate over mp_requests.
func RegisterLeaderboardRoutes(g *gin.RouterGroup) {
	g.GET("/leaderboard/models", getModelLeaderboard)
	g.GET("/leaderboard/sellers", getSellerLeaderboard)
	g.GET("/leaderboard/trend", getLeaderboardTrend)
}

// Leaderboard parameters. The window is sliding; limit is capped to keep the
// public payload bounded.
const (
	leaderboardRange24h     = "24h"
	leaderboardRange7d      = "7d"
	leaderboardLimitDefault = 20
	leaderboardLimitMax     = 100
	leaderboardDaysDefault  = 30
	leaderboardDaysMax      = 90
	// leaderboardCacheTTL mirrors publicStatsTTL (FRONTEND-REBUILD §5).
	leaderboardCacheTTL = time.Minute
)

// parseLeaderboardRange normalizes ?range=. Anything other than "7d" is the
// 24h default, so a bad query never turns into an error page.
func parseLeaderboardRange(raw string) (string, time.Duration) {
	if strings.EqualFold(strings.TrimSpace(raw), leaderboardRange7d) {
		return leaderboardRange7d, 7 * 24 * time.Hour
	}
	return leaderboardRange24h, 24 * time.Hour
}

// parseLeaderboardLimit reads ?limit=: default 20, capped at 100.
func parseLeaderboardLimit(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return leaderboardLimitDefault
	}
	if n > leaderboardLimitMax {
		n = leaderboardLimitMax
	}
	return n
}

// parseLeaderboardDays reads ?days= for the trend endpoint: default 30,
// capped at 90 (the same horizon as the buyer usage trend).
func parseLeaderboardDays(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return leaderboardDaysDefault
	}
	if n > leaderboardDaysMax {
		n = leaderboardDaysMax
	}
	return n
}

// ---------------------------------------------------------------------------
// cache
// ---------------------------------------------------------------------------

type leaderboardCacheEntry struct {
	at    time.Time
	value any
}

// leaderboardCache is keyed by endpoint + query signature so different
// ranges/limits/model filters never share a slot.
var leaderboardCache = struct {
	mu      sync.Mutex
	entries map[string]leaderboardCacheEntry
}{entries: make(map[string]leaderboardCacheEntry)}

// resetLeaderboardCache drops every cached snapshot (tests and any future
// admin "refresh now" path).
func resetLeaderboardCache() {
	leaderboardCache.mu.Lock()
	leaderboardCache.entries = make(map[string]leaderboardCacheEntry)
	leaderboardCache.mu.Unlock()
}

// cachedLeaderboard returns the snapshot for key when it is younger than
// leaderboardCacheTTL, otherwise computes and stores a fresh one.
func cachedLeaderboard(key string, now time.Time, compute func() (any, error)) (any, error) {
	leaderboardCache.mu.Lock()
	defer leaderboardCache.mu.Unlock()
	if entry, ok := leaderboardCache.entries[key]; ok {
		age := now.Sub(entry.at)
		if age >= 0 && age < leaderboardCacheTTL {
			return entry.value, nil
		}
	}
	value, err := compute()
	if err != nil {
		return nil, err
	}
	leaderboardCache.entries[key] = leaderboardCacheEntry{at: now, value: value}
	return value, nil
}

// ---------------------------------------------------------------------------
// model board
// ---------------------------------------------------------------------------

// Leaderboard anti-gaming parameters (P5-B). A request only counts toward a
// board when its input + output tokens exceed leaderboardMinTokens; repeated
// requests from one buyer to one seller count once (the "unrelated paid buyer"
// metric); and one buyer's contribution to one seller (or one model) is capped
// at leaderboardBuyerCapPercent of the pre-cap settlement total.
const (
	leaderboardMinTokens       int64 = 1000
	leaderboardBuyerCapPercent int64 = 10
)

// modelLeaderboardItem is one model's aggregate over the requested window.
// Buyers is the deduplicated paid-buyer count the board ranks by; Requests is
// kept for compatibility and carries the same deduplicated count. IncomeMicro
// is the settlement amount after the per-buyer cap.
type modelLeaderboardItem struct {
	Rank             int    `json:"rank"`
	Model            string `json:"model"`
	Buyers           int64  `json:"buyers"`
	Requests         int64  `json:"requests"`
	SuccessRateMilli int64  `json:"success_rate_milli"`
	AvgLatencyMs     int64  `json:"avg_latency_ms"`
	Tokens           int64  `json:"tokens"` // input + output
	IncomeMicro      int64  `json:"income_micro"`
}

// modelLeaderboard is GET /leaderboard/models's payload.
type modelLeaderboard struct {
	Range       string                 `json:"range"`        // 24h / 7d
	Window      string                 `json:"window"`       // "sliding"
	WindowStart time.Time              `json:"window_start"` // now-range
	WindowEnd   time.Time              `json:"window_end"`   // now
	GeneratedAt time.Time              `json:"generated_at"`
	Items       []modelLeaderboardItem `json:"items"`
}

// leaderboardBuyerRow is one (grouping key × buyer) aggregate. Grouping by
// buyer before the Go-side rollup is how duplicate traffic from one buyer to
// one seller collapses to a single contribution.
type leaderboardBuyerRow struct {
	Model        string
	SellerID     int64
	BuyerUserID  int64
	ReqCount     int64
	SuccessCount int64
	LatencySum   int64
	LatencyCount int64
	InputTokens  int64
	OutputTokens int64
	IncomeMicro  int64
}

// cappedBuyerIncome caps one buyer's contribution at 10% of the pre-cap total.
// The cap is computed from the pre-cap total so the order of buyers does not
// matter; integer 微元 arithmetic keeps the leaderboard deterministic.
func cappedBuyerIncome(byBuyer map[int64]int64) int64 {
	var total int64
	for _, income := range byBuyer {
		total += income
	}
	capMicro := total * leaderboardBuyerCapPercent / 100
	var capped int64
	for _, income := range byBuyer {
		capped += min(income, capMicro)
	}
	return capped
}

// modelBoardAggregate is the per-model rollup across (seller, buyer) pairs.
// The cap is applied per seller before the seller's capped income is summed
// into the model, matching the "one buyer's contribution to one seller" rule.
type modelBoardAggregate struct {
	reqCount     int64
	successCount int64
	latencySum   int64
	latencyCount int64
	tokens       int64
	bySeller     map[int64]map[int64]int64
}

// computeModelLeaderboard aggregates mp_requests by model over [now-window, now].
// Only requests above the token threshold participate. Each buyer counts once
// per model, and their settlement contribution is capped at 10% of the model's
// pre-cap total. Rows are ranked by buyer count, then capped settlement.
func computeModelLeaderboard(db *gorm.DB, now time.Time, windowName string, window time.Duration, limit int) (modelLeaderboard, error) {
	since := now.Add(-window)
	var rows []leaderboardBuyerRow
	err := db.Model(&Request{}).
		Select(`model,
			seller_id,
			buyer_user_id,
			COUNT(*) AS req_count,
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS success_count,
			SUM(latency_ms) AS latency_sum,
			SUM(CASE WHEN latency_ms > 0 THEN 1 ELSE 0 END) AS latency_count,
			SUM(input_tokens) AS input_tokens,
			SUM(output_tokens) AS output_tokens,
			SUM(CASE WHEN status = ? THEN seller_credit_micro ELSE 0 END) AS income_micro`,
			RequestSuccess, RequestSuccess).
		Where("created_at >= ? AND created_at <= ? AND (input_tokens + output_tokens) > ?",
			since, now, leaderboardMinTokens).
		Group("model, seller_id, buyer_user_id").
		Scan(&rows).Error
	if err != nil {
		return modelLeaderboard{}, err
	}
	aggregates := make(map[string]*modelBoardAggregate, len(rows))
	for i := range rows {
		row := &rows[i]
		agg := aggregates[row.Model]
		if agg == nil {
			agg = &modelBoardAggregate{bySeller: make(map[int64]map[int64]int64)}
			aggregates[row.Model] = agg
		}
		agg.reqCount += row.ReqCount
		agg.successCount += row.SuccessCount
		agg.latencySum += row.LatencySum
		agg.latencyCount += row.LatencyCount
		agg.tokens += row.InputTokens + row.OutputTokens
		if row.SuccessCount > 0 && row.IncomeMicro > 0 {
			byBuyer := agg.bySeller[row.SellerID]
			if byBuyer == nil {
				byBuyer = make(map[int64]int64)
				agg.bySeller[row.SellerID] = byBuyer
			}
			byBuyer[row.BuyerUserID] += row.IncomeMicro
		}
	}

	type modelScore struct {
		model  string
		agg    *modelBoardAggregate
		buyers int64
		income int64
	}
	scores := make([]modelScore, 0, len(aggregates))
	for model, agg := range aggregates {
		buyerSet := make(map[int64]struct{})
		var income int64
		for _, byBuyer := range agg.bySeller {
			income += cappedBuyerIncome(byBuyer)
			for buyerID := range byBuyer {
				buyerSet[buyerID] = struct{}{}
			}
		}
		scores = append(scores, modelScore{
			model:  model,
			agg:    agg,
			buyers: int64(len(buyerSet)),
			income: income,
		})
	}
	slices.SortFunc(scores, func(a, b modelScore) int {
		if a.buyers != b.buyers {
			return compareInt64Desc(a.buyers, b.buyers)
		}
		if a.income != b.income {
			return compareInt64Desc(a.income, b.income)
		}
		return strings.Compare(a.model, b.model)
	})
	if len(scores) > limit {
		scores = scores[:limit]
	}
	items := make([]modelLeaderboardItem, 0, len(scores))
	for i, score := range scores {
		item := modelLeaderboardItem{
			Rank:             i + 1,
			Model:            score.model,
			Buyers:           score.buyers,
			Requests:         score.buyers,
			SuccessRateMilli: rateMilli(score.agg.reqCount, score.agg.successCount),
			Tokens:           score.agg.tokens,
			IncomeMicro:      score.income,
		}
		if score.agg.latencyCount > 0 {
			item.AvgLatencyMs = score.agg.latencySum / score.agg.latencyCount
		}
		items = append(items, item)
	}
	return modelLeaderboard{
		Range:       windowName,
		Window:      "sliding",
		WindowStart: since,
		WindowEnd:   now,
		GeneratedAt: now,
		Items:       items,
	}, nil
}

// compareInt64Desc orders a before b for a descending sort.
func compareInt64Desc(a, b int64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	default:
		return 0
	}
}

// getModelLeaderboard serves GET /leaderboard/models (no login).
func getModelLeaderboard(c *gin.Context) {
	db := consoleDB(c)
	if db == nil {
		return
	}
	rangeName, window := parseLeaderboardRange(c.Query("range"))
	limit := parseLeaderboardLimit(c.Query("limit"))
	now := time.Now()
	key := fmt.Sprintf("models|%s|%d", rangeName, limit)
	value, err := cachedLeaderboard(key, now, func() (any, error) {
		return computeModelLeaderboard(db, now, rangeName, window, limit)
	})
	if err != nil {
		common.SysError(common.LogText("marketplace leaderboard models: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query leaderboard"))
		return
	}
	common.ApiSuccess(c, value)
}

// ---------------------------------------------------------------------------
// seller board
// ---------------------------------------------------------------------------

// sellerLeaderboardItem is one anonymized seller board row. SellerID is
// deliberately absent: the seller appears only as Alias. Buyers is the
// deduplicated paid-buyer count; Requests mirrors it for compatibility.
type sellerLeaderboardItem struct {
	Rank             int    `json:"rank"`
	Alias            string `json:"alias"`
	Buyers           int64  `json:"buyers"`
	Requests         int64  `json:"requests"`
	SuccessRateMilli int64  `json:"success_rate_milli"`
	IncomeMicro      int64  `json:"income_micro"`
}

// sellerBoardAggregate is the per-seller rollup across buyers.
type sellerBoardAggregate struct {
	reqCount     int64
	successCount int64
	byBuyer      map[int64]int64
}

// sellerLeaderboard is GET /leaderboard/sellers's payload.
type sellerLeaderboard struct {
	Range       string                  `json:"range"`
	Window      string                  `json:"window"`
	WindowStart time.Time               `json:"window_start"`
	WindowEnd   time.Time               `json:"window_end"`
	GeneratedAt time.Time               `json:"generated_at"`
	Items       []sellerLeaderboardItem `json:"items"`
}

// sellerAliasDomain is the domain-separation label that derives the alias
// subkey from MP_KEY_SECRET. Changing it rotates every public alias.
const sellerAliasDomain = "marketplace/leaderboard/seller-alias/v1"

// sellerAliasKey derives the alias subkey: HMAC-SHA256(MP_KEY_SECRET, domain).
// Domain separation keeps this HMAC independent of the buyer-code and
// key-fingerprint subkeys derived from the same secret.
func sellerAliasKey() []byte {
	mac := hmac.New(sha256.New, anonSecret)
	mac.Write([]byte(sellerAliasDomain))
	return mac.Sum(nil)
}

// sellerAlias renders "分享者 #a1b2": the first four hex chars of
// HMAC-SHA256(aliasKey, sellerID). It never exposes the seller id.
func sellerAlias(sellerID int64) string {
	mac := hmac.New(sha256.New, sellerAliasKey())
	fmt.Fprintf(mac, "%d", sellerID)
	return "分享者 #" + hex.EncodeToString(mac.Sum(nil))[:4]
}

// computeSellerLeaderboard aggregates mp_requests by seller over the window and
// anonymizes every row. It applies the same anti-gaming rules as the model
// board: token threshold, one contribution per buyer, and a 10% per-buyer
// settlement cap. Buyers are ranked before settlement so a seller cannot buy
// their way up the board with one high-value account.
func computeSellerLeaderboard(db *gorm.DB, now time.Time, windowName string, window time.Duration, limit int) (sellerLeaderboard, error) {
	since := now.Add(-window)
	var rows []leaderboardBuyerRow
	err := db.Model(&Request{}).
		Select(`seller_id,
			buyer_user_id,
			COUNT(*) AS req_count,
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS success_count,
			SUM(CASE WHEN status = ? THEN seller_credit_micro ELSE 0 END) AS income_micro`,
			RequestSuccess, RequestSuccess).
		Where("created_at >= ? AND created_at <= ? AND (input_tokens + output_tokens) > ?",
			since, now, leaderboardMinTokens).
		Group("seller_id, buyer_user_id").
		Scan(&rows).Error
	if err != nil {
		return sellerLeaderboard{}, err
	}
	aggregates := make(map[int64]*sellerBoardAggregate, len(rows))
	for i := range rows {
		row := &rows[i]
		agg := aggregates[row.SellerID]
		if agg == nil {
			agg = &sellerBoardAggregate{byBuyer: make(map[int64]int64)}
			aggregates[row.SellerID] = agg
		}
		agg.reqCount += row.ReqCount
		agg.successCount += row.SuccessCount
		if row.SuccessCount > 0 && row.IncomeMicro > 0 {
			agg.byBuyer[row.BuyerUserID] += row.IncomeMicro
		}
	}

	type sellerScore struct {
		sellerID int64
		agg      *sellerBoardAggregate
		buyers   int64
		income   int64
	}
	scores := make([]sellerScore, 0, len(aggregates))
	for sellerID, agg := range aggregates {
		scores = append(scores, sellerScore{
			sellerID: sellerID,
			agg:      agg,
			buyers:   int64(len(agg.byBuyer)),
			income:   cappedBuyerIncome(agg.byBuyer),
		})
	}
	slices.SortFunc(scores, func(a, b sellerScore) int {
		if a.buyers != b.buyers {
			return compareInt64Desc(a.buyers, b.buyers)
		}
		if a.income != b.income {
			return compareInt64Desc(a.income, b.income)
		}
		return compareInt64Desc(b.sellerID, a.sellerID)
	})
	if len(scores) > limit {
		scores = scores[:limit]
	}
	items := make([]sellerLeaderboardItem, 0, len(scores))
	for i, score := range scores {
		items = append(items, sellerLeaderboardItem{
			Rank:             i + 1,
			Alias:            sellerAlias(score.sellerID),
			Buyers:           score.buyers,
			Requests:         score.buyers,
			SuccessRateMilli: rateMilli(score.agg.reqCount, score.agg.successCount),
			IncomeMicro:      score.income,
		})
	}
	return sellerLeaderboard{
		Range:       windowName,
		Window:      "sliding",
		WindowStart: since,
		WindowEnd:   now,
		GeneratedAt: now,
		Items:       items,
	}, nil
}

// getSellerLeaderboard serves GET /leaderboard/sellers (no login).
func getSellerLeaderboard(c *gin.Context) {
	if len(anonSecret) == 0 {
		// Init refuses to start without MP_KEY_SECRET, so this is a
		// misconfiguration; failing loudly is better than leaking ids.
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
		return
	}
	db := consoleDB(c)
	if db == nil {
		return
	}
	rangeName, window := parseLeaderboardRange(c.Query("range"))
	limit := parseLeaderboardLimit(c.Query("limit"))
	now := time.Now()
	key := fmt.Sprintf("sellers|%s|%d", rangeName, limit)
	value, err := cachedLeaderboard(key, now, func() (any, error) {
		return computeSellerLeaderboard(db, now, rangeName, window, limit)
	})
	if err != nil {
		common.SysError(common.LogText("marketplace leaderboard sellers: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query leaderboard"))
		return
	}
	common.ApiSuccess(c, value)
}

// ---------------------------------------------------------------------------
// trend
// ---------------------------------------------------------------------------

// trendDay is one Asia/Shanghai calendar day of platform-wide traffic.
type trendDay struct {
	Date     string `json:"date"` // YYYY-MM-DD
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"` // input + output
}

// leaderboardTrend is GET /leaderboard/trend's payload. Items are dense over
// [start, end] (days without traffic carry zeros).
type leaderboardTrend struct {
	Days  int        `json:"days"`
	Start string     `json:"start"`
	End   string     `json:"end"`
	Model string     `json:"model,omitempty"`
	Items []trendDay `json:"items"`
}

// computeLeaderboardTrend buckets mp_requests into Asia/Shanghai calendar
// days. Bucketing happens in Go after fetching created_at, which keeps the
// three supported SQL engines byte-for-byte identical (the same trade-off as
// aggregateUsageDaily).
func computeLeaderboardTrend(db *gorm.DB, now time.Time, days int, model string) (leaderboardTrend, error) {
	if days < 1 {
		days = 1
	}
	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		return leaderboardTrend{}, err
	}
	lt := now.In(loc)
	today := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	startDay := today.AddDate(0, 0, -(days - 1))
	endExclusive := today.AddDate(0, 0, 1)

	items := make([]trendDay, days)
	index := make(map[string]int, days)
	for i := 0; i < days; i++ {
		date := startDay.AddDate(0, 0, i).Format("2006-01-02")
		items[i] = trendDay{Date: date}
		index[date] = i
	}

	query := db.Model(&Request{}).
		Select("created_at", "input_tokens", "output_tokens").
		Where("created_at >= ? AND created_at < ?", startDay, endExclusive)
	if model != "" {
		query = query.Where("model = ?", model)
	}
	var rows []Request
	if err := query.Find(&rows).Error; err != nil {
		return leaderboardTrend{}, err
	}
	for i := range rows {
		row := &rows[i]
		idx, ok := index[row.CreatedAt.In(loc).Format("2006-01-02")]
		if !ok {
			continue
		}
		items[idx].Requests++
		items[idx].Tokens += row.InputTokens + row.OutputTokens
	}
	return leaderboardTrend{
		Days:  days,
		Start: startDay.Format("2006-01-02"),
		End:   today.Format("2006-01-02"),
		Model: model,
		Items: items,
	}, nil
}

// getLeaderboardTrend serves GET /leaderboard/trend (no login).
func getLeaderboardTrend(c *gin.Context) {
	db := consoleDB(c)
	if db == nil {
		return
	}
	days := parseLeaderboardDays(c.Query("days"))
	model := strings.TrimSpace(c.Query("model"))
	now := time.Now()
	key := fmt.Sprintf("trend|%d|%s", days, model)
	value, err := cachedLeaderboard(key, now, func() (any, error) {
		return computeLeaderboardTrend(db, now, days, model)
	})
	if err != nil {
		common.SysError(common.LogText("marketplace leaderboard trend: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query leaderboard"))
		return
	}
	common.ApiSuccess(c, value)
}
