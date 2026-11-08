package marketplace

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSellerListingsHealth covers GET /seller/listings/health: one row per own
// listing, the newest probe (any type) with fail_reason/model_results, today's
// request count and success rate (milli), and today's seller credit.
func TestSellerListingsHealth(t *testing.T) {
	f := newSellerAPIFixture(t)
	now := time.Now()

	listingA, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-health-a"),
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	listingB, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-health-b"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	// A foreign listing must never show up on this seller's health cards.
	_, err = CreateListing(f.db, CreateListingInput{
		SellerID:        f.stranger,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-health-foreign"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)

	// Two probes on A: the newer failed patrol is the one health must report.
	require.NoError(t, f.db.Create(&Probe{
		ListingID: listingA.ID, Type: ProbeTypePatrol, OK: true, LatencyMs: 20,
		Detail: `{"ok":true}`,
	}).Error)
	require.NoError(t, f.db.Create(&Probe{
		ListingID: listingA.ID, Type: ProbeTypePatrol, OK: false, LatencyMs: 55,
		Detail: `{"fail_reason":"upstream 500",` +
			`"model_results":[{"model":"deepseek-flash","upstream_model":"deepseek-flash",` +
			`"ok":false,"latency_ms":55,"fail_reason":"upstream 500"}]}`,
	}).Error)

	// Today: 2 success + 1 failed on A. Yesterday's row must be excluded.
	makeRequest := func(id string, listingID int64, status string, at time.Time) {
		require.NoError(t, f.db.Create(&Request{
			RequestID: id, BuyerUserID: f.buyerID, SellerID: f.sellerID,
			ListingID: listingID, Model: "deepseek-flash", Status: status, CreatedAt: at,
		}).Error)
	}
	makeRequest("health-a-1", listingA.ID, RequestSuccess, now)
	makeRequest("health-a-2", listingA.ID, RequestSuccess, now)
	makeRequest("health-a-3", listingA.ID, RequestFailed, now)
	makeRequest("health-a-old", listingA.ID, RequestSuccess, now.AddDate(0, 0, -1))

	credits := []LedgerEntry{
		{RequestID: "health-a-1", EntryType: EntrySellerCredit, Account: SellerAccount(f.sellerID),
			AmountMicro: 1_500_000, Status: LedgerAvailable, CreatedAt: now},
		{RequestID: "health-a-2", EntryType: EntrySellerCredit, Account: SellerAccount(f.sellerID),
			AmountMicro: 2_000_000, Status: LedgerAvailable, CreatedAt: now},
		// A reversed credit and yesterday's credit must not count.
		{RequestID: "health-a-3", EntryType: EntrySellerCredit, Account: SellerAccount(f.sellerID),
			AmountMicro: 700_000, Status: LedgerReversed, CreatedAt: now},
		{RequestID: "health-a-old", EntryType: EntrySellerCredit, Account: SellerAccount(f.sellerID),
			AmountMicro: 900_000, Status: LedgerAvailable, CreatedAt: now.AddDate(0, 0, -1)},
	}
	require.NoError(t, f.db.Create(&credits).Error)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/listings/health", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	items, ok := decodeSellerBody(t, w)["data"].([]any)
	require.True(t, ok)
	require.Len(t, items, 2, "only the seller's two listings")

	byID := make(map[int64]map[string]any, len(items))
	for _, item := range items {
		row := item.(map[string]any)
		byID[int64(row["listing_id"].(float64))] = row
	}

	a := byID[listingA.ID]
	require.NotNil(t, a)
	assert.EqualValues(t, 3, a["today_requests"])
	assert.EqualValues(t, 666, a["today_success_rate_milli"], "2/3 success → 666 milli")
	assert.EqualValues(t, 3_500_000, a["today_income_micro"])

	probe, ok := a["latest_probe"].(map[string]any)
	require.True(t, ok, "listing A reports its newest probe")
	assert.Equal(t, ProbeTypePatrol, probe["type"])
	assert.Equal(t, false, probe["ok"])
	assert.EqualValues(t, 55, probe["latency_ms"])
	assert.Equal(t, "upstream 500", probe["fail_reason"])
	results, ok := probe["model_results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	assert.Equal(t, "deepseek-flash", results[0].(map[string]any)["model"])

	b := byID[listingB.ID]
	require.NotNil(t, b)
	assert.EqualValues(t, 0, b["today_requests"])
	assert.EqualValues(t, 0, b["today_success_rate_milli"])
	assert.EqualValues(t, 0, b["today_income_micro"])
	assert.Nil(t, b["latest_probe"], "no probe yet → null")

	assert.NotContains(t, w.Body.String(), "sk-health-")
}

// TestSellerPaidTotals covers the new paid_micro field: only paid payouts count
// toward 已提现, pending/approved ones do not.
func TestSellerPaidTotals(t *testing.T) {
	f := newSellerAPIFixture(t)
	payouts := []Payout{
		{SellerID: f.sellerID, AmountMicro: 20_000_000, Status: PayoutPaid,
			IdempotencyKey: "health-paid-1", CreatedAt: time.Now()},
		{SellerID: f.sellerID, AmountMicro: 30_000_000, Status: PayoutPending,
			IdempotencyKey: "health-paid-2", CreatedAt: time.Now()},
		{SellerID: f.sellerID, AmountMicro: 40_000_000, Status: PayoutApproved,
			IdempotencyKey: "health-paid-3", CreatedAt: time.Now()},
	}
	require.NoError(t, f.db.Create(&payouts).Error)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/stats", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	stats := decodeSellerBody(t, w)["data"].(map[string]any)
	assert.EqualValues(t, 20_000_000, stats["paid_micro"])

	w = f.do(t, http.MethodGet, "/api/marketplace/seller/balance", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	balance := decodeSellerBody(t, w)["data"].(map[string]any)
	assert.EqualValues(t, 20_000_000, balance["paid_micro"])

	// The health endpoint stays scoped to the caller: no auth → 401.
	w = f.do(t, http.MethodGet, "/api/marketplace/seller/listings/health", nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// A foreign seller's health sees an empty list, never this seller's rows.
	w = f.do(t, http.MethodGet, "/api/marketplace/seller/listings/health", nil, int(f.stranger))
	require.Equal(t, http.StatusOK, w.Code)
	items, ok := decodeSellerBody(t, w)["data"].([]any)
	require.True(t, ok)
	assert.Len(t, items, 0)
}
