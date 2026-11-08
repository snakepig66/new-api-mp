package marketplace

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// overviewSeedRequest writes one mp_requests row for the overview tests.
func overviewSeedRequest(
	t *testing.T,
	f *sellerAPIFixture,
	requestID string,
	sellerID, listingID int64,
	model string,
	input, output, cacheRead, cacheWrite int64,
	createdAt time.Time,
) {
	t.Helper()
	require.NoError(t, f.db.Create(&Request{
		RequestID:        requestID,
		BuyerUserID:      f.buyerID,
		SellerID:         sellerID,
		ListingID:        listingID,
		Model:            model,
		InputTokens:      input,
		OutputTokens:     output,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
		Status:           RequestSuccess,
		CreatedAt:        createdAt,
	}).Error)
}

// overviewSeedCredit writes one seller ledger row for the overview tests.
func overviewSeedCredit(
	t *testing.T,
	f *sellerAPIFixture,
	requestID, entryType string,
	amount int64,
	status string,
	availableAt *time.Time,
	createdAt time.Time,
) {
	t.Helper()
	require.NoError(t, f.db.Create(&LedgerEntry{
		RequestID:   requestID,
		EntryType:   entryType,
		Account:     SellerAccount(f.sellerID),
		AmountMicro: amount,
		Status:      status,
		AvailableAt: availableAt,
		CreatedAt:   createdAt,
	}).Error)
}

// TestSellerOverviewIncomeLedger verifies the income definition: seller_credit
// minus reversal, excluding reversed rows, while pending settlement only counts
// pending seller_credit.
func TestSellerOverviewIncomeLedger(t *testing.T) {
	f := newSellerAPIFixture(t)
	now := time.Now()

	// Ordinary settled credit: counts in full.
	overviewSeedRequest(t, f, "ov-good", f.sellerID, 0, "m-good", 10, 20, 0, 0, now)
	overviewSeedCredit(t, f, "ov-good", EntrySellerCredit, 1000, LedgerAvailable, nil, now)

	// Fully reversed request: both rows are reversed and must contribute 0.
	overviewSeedRequest(t, f, "ov-reversed", f.sellerID, 0, "m-rev", 5, 5, 0, 0, now)
	overviewSeedCredit(t, f, "ov-reversed", EntrySellerCredit, 500, LedgerReversed, nil, now)
	overviewSeedCredit(t, f, "ov-reversed", EntryReversal, -500, LedgerReversed, nil, now)

	// A non-reversed reversal subtracts from its credit (the payout page's
	// ledger sum would be 600 as well).
	overviewSeedRequest(t, f, "ov-sub", f.sellerID, 0, "m-sub", 1, 1, 0, 0, now)
	overviewSeedCredit(t, f, "ov-sub", EntrySellerCredit, 1000, LedgerAvailable, nil, now)
	overviewSeedCredit(t, f, "ov-sub", EntryReversal, -400, LedgerAvailable, nil, now)

	// Pending credit counts toward income but not toward available balance.
	maturity := now.Add(24 * time.Hour)
	overviewSeedRequest(t, f, "ov-pending", f.sellerID, 0, "m-pend", 2, 2, 0, 0, now)
	overviewSeedCredit(t, f, "ov-pending", EntrySellerCredit, 300, LedgerPending, &maturity, now)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/overview", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeSellerBody(t, w)["data"].(map[string]any)
	cards := data["cards"].(map[string]any)

	assert.EqualValues(t, 4, cards["calls"])
	assert.EqualValues(t, 10+20+5+5+1+1+2+2, cards["tokens"])
	// 1000 (good) + 0 (reversed pair) + 600 (credit − reversal) + 300 (pending).
	assert.EqualValues(t, 1900, cards["income_micro"])
	assert.EqualValues(t, 300, cards["pending_micro"])

	byModel := indexOverviewModels(t, data["by_model"])
	assert.EqualValues(t, 1000, byModel["m-good"]["income_micro"])
	assert.EqualValues(t, 0, byModel["m-rev"]["income_micro"])
	assert.EqualValues(t, 600, byModel["m-sub"]["income_micro"])
	assert.EqualValues(t, 300, byModel["m-pend"]["income_micro"])
}

// indexOverviewModels maps by_model rows by model name for assertions.
func indexOverviewModels(t *testing.T, raw any) map[string]map[string]any {
	t.Helper()
	rows, ok := raw.([]any)
	require.True(t, ok)
	out := make(map[string]map[string]any, len(rows))
	for _, item := range rows {
		row := item.(map[string]any)
		out[row["model"].(string)] = row
	}
	return out
}

// TestSellerOverviewMonthBoundary verifies that the month cards use the
// Asia/Shanghai month boundary while the 30-day chart still includes the
// previous month's traffic inside the window.
func TestSellerOverviewMonthBoundary(t *testing.T) {
	f := newSellerAPIFixture(t)
	loc, err := time.LoadLocation(shanghaiTZ)
	require.NoError(t, err)

	now := time.Date(2026, 3, 15, 12, 0, 0, 0, loc)
	feb := time.Date(2026, 2, 20, 10, 0, 0, 0, loc)
	mar := time.Date(2026, 3, 5, 10, 0, 0, 0, loc)

	overviewSeedRequest(t, f, "ov-feb", f.sellerID, 0, "m-feb", 7, 3, 0, 0, feb)
	overviewSeedCredit(t, f, "ov-feb", EntrySellerCredit, 1000, LedgerAvailable, nil, feb)
	overviewSeedRequest(t, f, "ov-mar", f.sellerID, 0, "m-mar", 20, 30, 0, 0, mar)
	overviewSeedCredit(t, f, "ov-mar", EntrySellerCredit, 2000, LedgerAvailable, nil, mar)

	data, err := computeSellerOverview(f.db, f.sellerID, 0, now)
	require.NoError(t, err)

	// Cards: only the March request counts.
	assert.EqualValues(t, 1, data.MonthCalls)
	assert.EqualValues(t, 50, data.MonthTokens)

	monthStart := time.Date(2026, 3, 1, 0, 0, 0, 0, loc)
	monthIncome, err := sellerOverviewMonthIncome(f.db, f.sellerID, 0, monthStart, monthStart.AddDate(0, 1, 0))
	require.NoError(t, err)
	assert.EqualValues(t, 2000, monthIncome)

	// Charts: both days fall inside the 30-day window (Feb 14 .. Mar 15).
	byModel := indexOverviewModels(t, overviewModelsJSON(t, data.ByModel))
	assert.EqualValues(t, 10, byModel["m-feb"]["tokens"])
	assert.EqualValues(t, 1000, byModel["m-feb"]["income_micro"])
	assert.EqualValues(t, 50, byModel["m-mar"]["tokens"])
	assert.EqualValues(t, 2000, byModel["m-mar"]["income_micro"])

	febSeries := findOverviewSeries(t, data.DailyByModel, "m-feb")
	assert.Equal(t, "2026-02-20", febSeries.Days[6].Date)
	assert.EqualValues(t, 10, febSeries.Days[6].Tokens)
	assert.EqualValues(t, 1000, febSeries.Days[6].IncomeMicro)
	marSeries := findOverviewSeries(t, data.DailyByModel, "m-mar")
	assert.Equal(t, "2026-03-05", marSeries.Days[19].Date)
	assert.EqualValues(t, 50, marSeries.Days[19].Tokens)
	assert.EqualValues(t, 2000, marSeries.Days[19].IncomeMicro)
}

// overviewModelsJSON converts by_model rows to the generic JSON shape so the
// same indexer serves direct and HTTP assertions.
func overviewModelsJSON(t *testing.T, rows []sellerOverviewModelAgg) any {
	t.Helper()
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"model":        row.Model,
			"calls":        float64(row.Calls),
			"tokens":       float64(row.Tokens),
			"income_micro": float64(row.IncomeMicro),
		})
	}
	return out
}

// findOverviewSeries returns one model's series or fails the test.
func findOverviewSeries(t *testing.T, series []sellerOverviewModelSeries, model string) sellerOverviewModelSeries {
	t.Helper()
	for _, s := range series {
		if s.Model == model {
			return s
		}
	}
	t.Fatalf("model series %q not found", model)
	return sellerOverviewModelSeries{}
}

// TestSellerOverviewTopModelsAndOther verifies the top-8 cut and the folded
// "other" bucket, including stable token-descending ordering.
func TestSellerOverviewTopModelsAndOther(t *testing.T) {
	f := newSellerAPIFixture(t)
	loc, err := time.LoadLocation(shanghaiTZ)
	require.NoError(t, err)
	now := time.Date(2026, 4, 10, 12, 0, 0, 0, loc)

	for i := 0; i < 10; i++ {
		model := fmt.Sprintf("model-%02d", i)
		tokens := int64((i + 1) * 100) // model-00 = 100 … model-09 = 1000
		overviewSeedRequest(t, f, fmt.Sprintf("top-%d", i), f.sellerID, 0, model, tokens, 0, 0, 0, now)
		overviewSeedCredit(t, f, fmt.Sprintf("top-%d", i), EntrySellerCredit, int64(i+1)*10, LedgerAvailable, nil, now)
	}

	data, err := computeSellerOverview(f.db, f.sellerID, 0, now)
	require.NoError(t, err)

	require.Len(t, data.ByModel, 10)
	assert.Equal(t, "model-09", data.ByModel[0].Model)
	assert.EqualValues(t, 1000, data.ByModel[0].Tokens)
	assert.Equal(t, "model-00", data.ByModel[9].Model)
	assert.EqualValues(t, 100, data.ByModel[9].Tokens)

	// Eight explicit models plus the "other" bucket.
	require.Len(t, data.DailyByModel, 9)
	assert.Equal(t, "model-09", data.DailyByModel[0].Model)
	assert.Equal(t, "model-02", data.DailyByModel[7].Model)
	assert.Equal(t, sellerOverviewOtherModel, data.DailyByModel[8].Model)
	for _, series := range data.DailyByModel {
		require.Len(t, series.Days, sellerOverviewDays)
	}

	other := data.DailyByModel[8]
	today := other.Days[len(other.Days)-1]
	assert.Equal(t, now.In(loc).Format("2006-01-02"), today.Date)
	// model-01 (200) + model-00 (100).
	assert.EqualValues(t, 300, today.Tokens)
	// 20 + 10.
	assert.EqualValues(t, 30, today.IncomeMicro)
}

// TestSellerOverviewByChannelLabels covers per-channel labels (explicit and
// "厂商 #id" fallback), calls/tokens/income, and soft-deleted exclusion.
func TestSellerOverviewByChannelLabels(t *testing.T) {
	f := newSellerAPIFixture(t)
	now := time.Now()

	labelled, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-overview-label"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
		Label:           "香港一号",
	})
	require.NoError(t, err)
	unlabelled, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-overview-nolabel"),
		Models:          []string{"deepseek-v4-pro"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	deleted, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-overview-deleted"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)

	overviewSeedRequest(t, f, "ch-a", f.sellerID, labelled.ID, "deepseek-flash", 10, 20, 0, 0, now)
	overviewSeedCredit(t, f, "ch-a", EntrySellerCredit, 1000, LedgerAvailable, nil, now)
	overviewSeedRequest(t, f, "ch-b", f.sellerID, unlabelled.ID, "deepseek-v4-pro", 5, 5, 0, 0, now)
	overviewSeedCredit(t, f, "ch-b", EntrySellerCredit, 2000, LedgerAvailable, nil, now)
	overviewSeedRequest(t, f, "ch-del", f.sellerID, deleted.ID, "deepseek-flash", 1, 1, 0, 0, now)
	overviewSeedCredit(t, f, "ch-del", EntrySellerCredit, 500, LedgerAvailable, nil, now)
	_, err = DeleteListingForSeller(f.db, deleted.ID, f.sellerID, now)
	require.NoError(t, err)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/overview", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeSellerBody(t, w)["data"].(map[string]any)

	channels, ok := data["by_channel"].([]any)
	require.True(t, ok)
	require.Len(t, channels, 2)

	// Income-descending: the 2000 channel comes first.
	first := channels[0].(map[string]any)
	assert.EqualValues(t, unlabelled.ID, first["listing_id"])
	assert.Equal(t, fmt.Sprintf("厂商 #%d", unlabelled.ID), first["label"])
	assert.EqualValues(t, 1, first["calls"])
	assert.EqualValues(t, 10, first["tokens"])
	assert.EqualValues(t, 2000, first["income_micro"])

	second := channels[1].(map[string]any)
	assert.EqualValues(t, labelled.ID, second["listing_id"])
	assert.Equal(t, "香港一号", second["label"])
	assert.EqualValues(t, 1, second["calls"])
	assert.EqualValues(t, 30, second["tokens"])
	assert.EqualValues(t, 1000, second["income_micro"])

	// The deleted listing is hidden from by_channel, but its traffic still
	// counts in the seller's cards.
	cards := data["cards"].(map[string]any)
	assert.EqualValues(t, 3500, cards["income_micro"])
	assert.EqualValues(t, 42, cards["tokens"])

	for _, item := range channels {
		row := item.(map[string]any)
		assert.NotEqual(t, float64(deleted.ID), row["listing_id"])
	}
}

// TestSellerOverviewOwnership404 verifies the optional listing drill-down
// refuses foreign and unknown listing ids with 404, and accepts an own row.
func TestSellerOverviewOwnership404(t *testing.T) {
	f := newSellerAPIFixture(t)

	foreign, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.stranger,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-overview-foreign"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	own, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-overview-own"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)

	for _, path := range []string{
		fmt.Sprintf("/api/marketplace/seller/overview?listing_id=%d", foreign.ID),
		"/api/marketplace/seller/overview?listing_id=999999",
	} {
		w := f.do(t, http.MethodGet, path, nil, int(f.sellerID))
		assert.Equal(t, http.StatusNotFound, w.Code, path)
	}

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/overview?listing_id=abc", nil, int(f.sellerID))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = f.do(t, http.MethodGet,
		fmt.Sprintf("/api/marketplace/seller/overview?listing_id=%d", own.ID), nil, int(f.sellerID))
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestSellerOverviewEmptyZeroes verifies the empty-data shape: zero cards and
// non-null empty arrays.
func TestSellerOverviewEmptyZeroes(t *testing.T) {
	f := newSellerAPIFixture(t)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/overview", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeSellerBody(t, w)["data"].(map[string]any)

	cards := data["cards"].(map[string]any)
	assert.EqualValues(t, 0, cards["calls"])
	assert.EqualValues(t, 0, cards["tokens"])
	assert.EqualValues(t, 0, cards["income_micro"])
	assert.EqualValues(t, 0, cards["pending_micro"])

	window := data["range"].(map[string]any)
	assert.EqualValues(t, sellerOverviewDays, window["days"])
	assert.NotEmpty(t, window["start"])
	assert.NotEmpty(t, window["end"])

	assert.Empty(t, data["by_model"].([]any))
	assert.Empty(t, data["daily_by_model"].([]any))
	assert.Empty(t, data["by_channel"].([]any))
}

// TestSellerStatsIncludesOverviewFields verifies the existing /seller/stats
// endpoint also carries the new month calls/tokens and chart fields.
func TestSellerStatsIncludesOverviewFields(t *testing.T) {
	f := newSellerAPIFixture(t)
	now := time.Now()

	overviewSeedRequest(t, f, "stats-ov", f.sellerID, 0, "m-stats", 7, 3, 0, 0, now)
	overviewSeedCredit(t, f, "stats-ov", EntrySellerCredit, 100, LedgerAvailable, nil, now)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/stats", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeSellerBody(t, w)["data"].(map[string]any)

	assert.EqualValues(t, 1, data["month_calls"])
	assert.EqualValues(t, 10, data["month_tokens"])
	assert.EqualValues(t, 100, data["month_income_micro"])
	require.Len(t, data["by_model"].([]any), 1)
	require.Len(t, data["daily_by_model"].([]any), 1)
	assert.Empty(t, data["by_channel"].([]any))
}
