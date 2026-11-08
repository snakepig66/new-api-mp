package marketplace

// seller_overview.go — seller console overview aggregation (FRONTEND-REBUILD §5).
//
// GET /seller/overview answers the seller console's overview page:
//
//	cards   current month to date (Asia/Shanghai): calls, tokens, income,
//	        pending settlement
//	range   the 30-day chart window (inclusive start/end, Asia/Shanghai)
//	by_model / daily_by_model
//	        last 30 days, tokens and income per model; daily_by_model keeps
//	        the top 8 models by tokens and folds the rest into "other"
//	by_channel
//	        last 30 days, label + calls/tokens/income per own channel
//
// Income always comes from mp_ledger, never from mp_requests: seller_credit
// minus reversal, with reversed rows excluded, exactly like the payout page.
// Calls and tokens come from mp_requests. All money is 微元 (int64).
//
// The endpoint is also merged into GET /seller/stats so the existing earnings
// page receives the new fields without a second round trip.
//
// Aggregation happens in Go rather than through SQL date functions so SQLite,
// MySQL and PostgreSQL return identical buckets (the same trade-off as
// aggregateUsageDaily).

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

const (
	// sellerOverviewDays is the chart horizon: today plus the previous 29
	// Asia/Shanghai calendar days.
	sellerOverviewDays = 30
	// sellerOverviewTopModels is the number of model series kept in
	// daily_by_model before the remainder is folded into "other".
	sellerOverviewTopModels = 8
	// sellerOverviewOtherModel is the bucket that absorbs every model outside
	// the top 8. It always sorts last.
	sellerOverviewOtherModel = "other"
)

// sellerOverviewRange describes the chart window so the console can print an
// explicit title instead of inferring it from the first/last data point.
type sellerOverviewRange struct {
	Days  int    `json:"days"`
	Start string `json:"start"` // YYYY-MM-DD, Asia/Shanghai, inclusive
	End   string `json:"end"`   // YYYY-MM-DD, Asia/Shanghai, inclusive (today)
}

// sellerOverviewModelAgg is one row of by_model: the whole 30-day window
// aggregated per model, ordered by tokens descending.
type sellerOverviewModelAgg struct {
	Model       string `json:"model"`
	Calls       int64  `json:"calls"`
	Tokens      int64  `json:"tokens"`
	IncomeMicro int64  `json:"income_micro"`
}

// sellerOverviewDayKey identifies one (model, Asia/Shanghai calendar day) cell
// while the raw rows are folded in Go.
type sellerOverviewDayKey struct {
	Model string
	Date  string
}

// sellerOverviewDay is one day of one model's series. Every series is dense:
// dates without traffic carry zeros.
type sellerOverviewDay struct {
	Date        string `json:"date"` // YYYY-MM-DD, Asia/Shanghai
	Tokens      int64  `json:"tokens"`
	IncomeMicro int64  `json:"income_micro"`
}

// sellerOverviewModelSeries is one model's dense daily series.
type sellerOverviewModelSeries struct {
	Model string              `json:"model"`
	Days  []sellerOverviewDay `json:"days"`
}

// sellerOverviewChannel is one row of by_channel. Label is the seller's
// listing label, falling back to "厂商 #<listing id>".
type sellerOverviewChannel struct {
	ListingID   int64  `json:"listing_id"`
	Label       string `json:"label"`
	Calls       int64  `json:"calls"`
	Tokens      int64  `json:"tokens"`
	IncomeMicro int64  `json:"income_micro"`
}

// sellerOverviewData is the chart payload computed from the database. The
// month card income/pending values are computed by the handler through the
// ledger helpers so they stay identical to the payout page.
type sellerOverviewData struct {
	MonthCalls   int64
	MonthTokens  int64
	Range        sellerOverviewRange
	ByModel      []sellerOverviewModelAgg
	DailyByModel []sellerOverviewModelSeries
	ByChannel    []sellerOverviewChannel
}

// getSellerOverview serves GET /seller/overview. listing_id is an optional
// drill-down filter; a foreign or unknown id answers 404 exactly like the
// other seller listing endpoints (§3.5).
func getSellerOverview(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	listingID, ok := sellerOverviewListingFilter(c, db, sellerID)
	if !ok {
		return
	}

	now := time.Now()
	data, err := computeSellerOverview(db, sellerID, listingID, now)
	if err != nil {
		common.SysError(common.LogText("marketplace seller overview: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query seller overview"))
		return
	}

	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to load timezone"))
		return
	}
	localNow := now.In(loc)
	monthStart := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, loc)
	nextMonthStart := monthStart.AddDate(0, 1, 0)

	pending, err := sellerOverviewPending(db, sellerID, listingID)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query pending balance"))
		return
	}
	monthIncome, err := sellerOverviewMonthIncome(db, sellerID, listingID, monthStart, nextMonthStart)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query monthly income"))
		return
	}

	common.ApiSuccess(c, gin.H{
		"cards": gin.H{
			"calls":         data.MonthCalls,
			"tokens":        data.MonthTokens,
			"income_micro":  monthIncome,
			"pending_micro": pending,
		},
		"range":          data.Range,
		"by_model":       data.ByModel,
		"daily_by_model": data.DailyByModel,
		"by_channel":     data.ByChannel,
		"as_of":          now,
	})
}

// sellerOverviewListingFilter parses the optional ?listing_id= drill-down
// filter and enforces ownership. The zero value means "all own listings".
func sellerOverviewListingFilter(c *gin.Context, db *gorm.DB, sellerID int64) (int64, bool) {
	raw := strings.TrimSpace(c.Query("listing_id"))
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid listing id"))
		return 0, false
	}
	if _, ok := requireOwnListing(c, db, id, sellerID); !ok {
		return 0, false
	}
	return id, true
}

// computeSellerOverview builds the chart payload. listingID == 0 aggregates
// every listing of the seller; a positive id scopes the whole payload.
func computeSellerOverview(db *gorm.DB, sellerID, listingID int64, now time.Time) (sellerOverviewData, error) {
	out := sellerOverviewData{
		ByModel:      []sellerOverviewModelAgg{},
		DailyByModel: []sellerOverviewModelSeries{},
		ByChannel:    []sellerOverviewChannel{},
	}
	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		return out, err
	}
	localNow := now.In(loc)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	seriesStart := today.AddDate(0, 0, -(sellerOverviewDays - 1))
	endExclusive := today.AddDate(0, 0, 1)
	monthStart := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, loc)
	nextMonthStart := monthStart.AddDate(0, 1, 0)

	out.Range = sellerOverviewRange{
		Days:  sellerOverviewDays,
		Start: seriesStart.Format("2006-01-02"),
		End:   today.Format("2006-01-02"),
	}

	// Month-to-date calls and tokens. SUM over the four token columns keeps
	// the definition identical to the buyer usage stats endpoint.
	var monthAgg struct {
		Calls  int64
		Tokens int64
	}
	monthQuery := db.Table("mp_requests").
		Where("seller_id = ? AND created_at >= ? AND created_at < ?", sellerID, monthStart, nextMonthStart)
	if listingID > 0 {
		monthQuery = monthQuery.Where("listing_id = ?", listingID)
	}
	if err := monthQuery.
		Select("COUNT(*) AS calls, COALESCE(SUM(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens), 0) AS tokens").
		Scan(&monthAgg).Error; err != nil {
		return out, err
	}
	out.MonthCalls = monthAgg.Calls
	out.MonthTokens = monthAgg.Tokens

	type overviewRequestRow struct {
		CreatedAt        time.Time
		ListingID        int64
		Model            string
		InputTokens      int64
		OutputTokens     int64
		CacheReadTokens  int64
		CacheWriteTokens int64
	}
	requestQuery := db.Table("mp_requests").
		Select("created_at, listing_id, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens").
		Where("seller_id = ? AND created_at >= ? AND created_at < ?", sellerID, seriesStart, endExclusive)
	if listingID > 0 {
		requestQuery = requestQuery.Where("listing_id = ?", listingID)
	}
	var requests []overviewRequestRow
	if err := requestQuery.Scan(&requests).Error; err != nil {
		return out, err
	}

	type overviewIncomeRow struct {
		CreatedAt   time.Time
		ListingID   int64
		Model       string
		AmountMicro int64
	}
	incomeQuery := db.Table("mp_ledger AS l").
		Joins("JOIN mp_requests AS r ON r.request_id = l.request_id").
		Select("l.created_at AS created_at, r.listing_id AS listing_id, r.model AS model, l.amount_micro AS amount_micro").
		Where("r.seller_id = ? AND l.entry_type IN ? AND l.status <> ? AND l.created_at >= ? AND l.created_at < ?",
			sellerID, []string{EntrySellerCredit, EntryReversal}, LedgerReversed, seriesStart, endExclusive)
	if listingID > 0 {
		incomeQuery = incomeQuery.Where("r.listing_id = ?", listingID)
	}
	var incomes []overviewIncomeRow
	if err := incomeQuery.Scan(&incomes).Error; err != nil {
		return out, err
	}

	dailyTokens := make(map[sellerOverviewDayKey]int64)
	dailyIncome := make(map[sellerOverviewDayKey]int64)
	modelCalls := make(map[string]int64)
	modelTokens := make(map[string]int64)
	modelIncome := make(map[string]int64)
	channelCalls := make(map[int64]int64)
	channelTokens := make(map[int64]int64)
	channelIncome := make(map[int64]int64)

	for i := range requests {
		row := &requests[i]
		date := row.CreatedAt.In(loc).Format("2006-01-02")
		tokens := row.InputTokens + row.OutputTokens + row.CacheReadTokens + row.CacheWriteTokens
		key := sellerOverviewDayKey{Model: row.Model, Date: date}
		dailyTokens[key] += tokens
		modelCalls[row.Model]++
		modelTokens[row.Model] += tokens
		if row.ListingID > 0 {
			channelCalls[row.ListingID]++
			channelTokens[row.ListingID] += tokens
		}
	}
	for i := range incomes {
		row := &incomes[i]
		date := row.CreatedAt.In(loc).Format("2006-01-02")
		key := sellerOverviewDayKey{Model: row.Model, Date: date}
		dailyIncome[key] += row.AmountMicro
		modelIncome[row.Model] += row.AmountMicro
		if row.ListingID > 0 {
			channelIncome[row.ListingID] += row.AmountMicro
		}
	}

	dates := make([]string, sellerOverviewDays)
	for i := 0; i < sellerOverviewDays; i++ {
		dates[i] = seriesStart.AddDate(0, 0, i).Format("2006-01-02")
	}

	// A model can appear in only one of the two maps (for example a ledger row
	// dated inside the window whose request is older), so union both.
	modelSet := make(map[string]struct{}, len(modelTokens)+len(modelIncome))
	for model := range modelTokens {
		modelSet[model] = struct{}{}
	}
	for model := range modelIncome {
		modelSet[model] = struct{}{}
	}
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	slices.SortFunc(models, func(a, b string) int {
		if modelTokens[a] != modelTokens[b] {
			return compareInt64Desc(modelTokens[a], modelTokens[b])
		}
		return strings.Compare(a, b)
	})

	for _, model := range models {
		out.ByModel = append(out.ByModel, sellerOverviewModelAgg{
			Model:       model,
			Calls:       modelCalls[model],
			Tokens:      modelTokens[model],
			IncomeMicro: modelIncome[model],
		})
	}

	top := min(len(models), sellerOverviewTopModels)
	for _, model := range models[:top] {
		out.DailyByModel = append(out.DailyByModel, sellerOverviewModelSeries{
			Model: model,
			Days:  sellerOverviewDaysFor(dates, model, dailyTokens, dailyIncome),
		})
	}
	if len(models) > sellerOverviewTopModels {
		other := make([]sellerOverviewDay, 0, sellerOverviewDays)
		for _, date := range dates {
			var tokens, income int64
			for _, model := range models[top:] {
				key := sellerOverviewDayKey{Model: model, Date: date}
				tokens += dailyTokens[key]
				income += dailyIncome[key]
			}
			other = append(other, sellerOverviewDay{Date: date, Tokens: tokens, IncomeMicro: income})
		}
		out.DailyByModel = append(out.DailyByModel, sellerOverviewModelSeries{
			Model: sellerOverviewOtherModel,
			Days:  other,
		})
	}

	channelIDs := make([]int64, 0, len(channelCalls)+len(channelIncome))
	seenChannel := make(map[int64]struct{}, len(channelCalls)+len(channelIncome))
	for id := range channelCalls {
		if id > 0 {
			seenChannel[id] = struct{}{}
		}
	}
	for id := range channelTokens {
		if id > 0 {
			seenChannel[id] = struct{}{}
		}
	}
	for id := range channelIncome {
		if id > 0 {
			seenChannel[id] = struct{}{}
		}
	}
	for id := range seenChannel {
		channelIDs = append(channelIDs, id)
	}

	if len(channelIDs) > 0 {
		type overviewListingRow struct {
			ID     int64
			Label  string
			Status string
		}
		var listings []overviewListingRow
		if err := db.Table("mp_listings").
			Select("id, label, status").
			Where("seller_id = ? AND id IN ?", sellerID, channelIDs).
			Scan(&listings).Error; err != nil {
			return out, err
		}
		labels := make(map[int64]string, len(listings))
		deleted := make(map[int64]struct{}, len(listings))
		for i := range listings {
			row := &listings[i]
			// Soft-deleted listings disappear from the channel breakdown; their
			// historical requests and income still count in the cards and the
			// model charts so the seller's totals remain complete.
			if row.Status == ListingDeleted {
				deleted[row.ID] = struct{}{}
				continue
			}
			label := strings.TrimSpace(row.Label)
			if label == "" {
				label = fmt.Sprintf("厂商 #%d", row.ID)
			}
			labels[row.ID] = label
		}
		for _, id := range channelIDs {
			if _, gone := deleted[id]; gone {
				continue
			}
			label, ok := labels[id]
			if !ok {
				label = fmt.Sprintf("厂商 #%d", id)
			}
			out.ByChannel = append(out.ByChannel, sellerOverviewChannel{
				ListingID:   id,
				Label:       label,
				Calls:       channelCalls[id],
				Tokens:      channelTokens[id],
				IncomeMicro: channelIncome[id],
			})
		}
		slices.SortFunc(out.ByChannel, func(a, b sellerOverviewChannel) int {
			if a.IncomeMicro != b.IncomeMicro {
				return compareInt64Desc(a.IncomeMicro, b.IncomeMicro)
			}
			if a.Calls != b.Calls {
				return compareInt64Desc(a.Calls, b.Calls)
			}
			return compareInt64Desc(b.ListingID, a.ListingID)
		})
	}

	return out, nil
}

// sellerOverviewDaysFor builds one model's dense series over dates.
func sellerOverviewDaysFor(
	dates []string,
	model string,
	dailyTokens map[sellerOverviewDayKey]int64,
	dailyIncome map[sellerOverviewDayKey]int64,
) []sellerOverviewDay {
	days := make([]sellerOverviewDay, 0, len(dates))
	for _, date := range dates {
		key := sellerOverviewDayKey{Model: model, Date: date}
		days = append(days, sellerOverviewDay{
			Date:        date,
			Tokens:      dailyTokens[key],
			IncomeMicro: dailyIncome[key],
		})
	}
	return days
}

// sellerOverviewPending returns the seller's pending settlement (T+7) total.
// Only seller_credit rows can be pending; reversal rows are always booked
// reversed and therefore excluded.
func sellerOverviewPending(db *gorm.DB, sellerID, listingID int64) (int64, error) {
	extra := "l.status = ?"
	args := []any{LedgerPending}
	if listingID > 0 {
		extra += " AND r.listing_id = ?"
		args = append(args, listingID)
	}
	return sumSellerLedger(db, sellerID, []string{EntrySellerCredit}, extra, args...)
}

// sellerOverviewMonthIncome sums the month's seller_credit minus reversal,
// excluding reversed rows. This mirrors the payout page's ledger definition.
func sellerOverviewMonthIncome(db *gorm.DB, sellerID, listingID int64, start, end time.Time) (int64, error) {
	extra := "l.status <> ? AND l.created_at >= ? AND l.created_at < ?"
	args := []any{LedgerReversed, start, end}
	if listingID > 0 {
		extra += " AND r.listing_id = ?"
		args = append(args, listingID)
	}
	return sumSellerLedger(db, sellerID, []string{EntrySellerCredit, EntryReversal}, extra, args...)
}
