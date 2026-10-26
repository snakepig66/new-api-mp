package marketplace

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// walletFixture is an isolated marketplace environment serving the wallet
// routes behind a simulated login (headerUserAuth from api_key_test.go).
type walletFixture struct {
	db      *gorm.DB
	engine  *gin.Engine
	buyerID int64
	otherID int64
}

// insertTestUser inserts one users row via SQL so the fixture does not depend
// on the current Go field name of the affiliate code column (which is being
// renamed upstream); aff_code is unique, so every fixture user needs its own.
func insertTestUser(t *testing.T, db *gorm.DB, username string) int64 {
	t.Helper()
	require.NoError(t, db.Exec(
		"INSERT INTO users (username, password, aff_code, role, status) VALUES (?, ?, ?, 1, 1)",
		username, "placeholder-pass", username+"-aff",
	).Error)
	var id int64
	require.NoError(t, db.Table("users").Select("id").Where("username = ?", username).Scan(&id).Error)
	require.NotZero(t, id)
	return id
}

func newWalletFixture(t *testing.T) *walletFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	migrateAll(t, db)
	// Top-ups belong to the upstream schema; the unified wallet stream reads
	// them next to mp_ledger, so the fixture creates the same table production
	// GORM owns.
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}))

	relayStateMu.Lock()
	oldRelayDB := relayDB
	relayDB = db
	relayStateMu.Unlock()
	t.Cleanup(func() {
		relayStateMu.Lock()
		relayDB = oldRelayDB
		relayStateMu.Unlock()
	})

	buyerID := insertTestUser(t, db, "wallet-buyer")
	otherID := insertTestUser(t, db, "wallet-other")

	engine := gin.New()
	registerWalletRoutes(engine.Group("/api/marketplace", headerUserAuth()))

	return &walletFixture{
		db:      db,
		engine:  engine,
		buyerID: buyerID,
		otherID: otherID,
	}
}

func (f *walletFixture) get(t *testing.T, path string, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if userID > 0 {
		req.Header.Set("X-Test-User-Id", strconv.FormatInt(userID, 10))
	}
	f.engine.ServeHTTP(w, req)
	return w
}

// seedWalletLedger writes one ledger row directly; the wallet APIs read the
// ledger, they never create it.
func seedWalletLedger(t *testing.T, db *gorm.DB, row LedgerEntry) {
	t.Helper()
	if row.Status == "" {
		row.Status = LedgerAvailable
	}
	require.NoError(t, db.Create(&row).Error)
}

// seedWalletTopUp writes one upstream top_ups row. create_time is Unix
// seconds, unlike the ledger's time.Time column.
func seedWalletTopUp(t *testing.T, db *gorm.DB, row model.TopUp) {
	t.Helper()
	if row.Status == "" {
		row.Status = common.TopUpStatusSuccess
	}
	require.NoError(t, db.Create(&row).Error)
}

func apiStr(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	value, ok := m[key].(string)
	require.True(t, ok, "field %q must be a string, map: %v", key, m)
	return value
}

func seedWalletRequest(t *testing.T, db *gorm.DB, row Request) {
	t.Helper()
	if row.Status == "" {
		row.Status = RequestSuccess
	}
	require.NoError(t, db.Create(&row).Error)
}

// TestWalletSummary checks the three wallet numbers against hand-inserted
// data: balance from users.quota, month spend from buyer debit rows only, and
// month savings from 官方价 − 实付.
func TestWalletSummary(t *testing.T) {
	f := newWalletFixture(t)
	const quota = 123_456_789
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.buyerID).Update("quota", quota).Error)

	now := time.Now()
	monthStart, _, err := shanghaiMonthRange(now)
	require.NoError(t, err)
	inMonth := monthStart.Add(2 * time.Hour)
	lastMonth := monthStart.Add(-time.Hour)

	buyer := strconv.FormatInt(f.buyerID, 10)
	other := strconv.FormatInt(f.otherID, 10)

	// Debits this month: 5000 + 2000 under the current "user:" convention and
	// 500 under the split "buyer:" account (CODE-REVIEW H5), all counted.
	seedWalletLedger(t, f.db, LedgerEntry{
		RequestID: "w-buyer-1", EntryType: EntryBuyerDebit, Account: "user:" + buyer,
		AmountMicro: -5000, Status: LedgerAvailable, CreatedAt: inMonth,
	})
	seedWalletLedger(t, f.db, LedgerEntry{
		RequestID: "w-buyer-1", EntryType: EntryToolDebit, Account: "user:" + buyer,
		AmountMicro: -2000, Status: LedgerAvailable, CreatedAt: inMonth,
	})
	seedWalletLedger(t, f.db, LedgerEntry{
		RequestID: "w-buyer-2", EntryType: EntryBuyerDebit, Account: "buyer:" + buyer,
		AmountMicro: -500, Status: LedgerAvailable, CreatedAt: inMonth,
	})
	// A refund does not reduce the "被扣合计" figure (the spec defines it as
	// the sum of the debit rows).
	seedWalletLedger(t, f.db, LedgerEntry{
		RequestID: "w-buyer-1", EntryType: EntryRefund, Account: "user:" + buyer,
		AmountMicro: 1000, Status: LedgerAvailable, CreatedAt: inMonth,
	})
	// Seller income of the same user must not leak into buyer spend.
	seedWalletLedger(t, f.db, LedgerEntry{
		RequestID: "w-seller-1", EntryType: EntrySellerCredit, Account: "user:" + buyer,
		AmountMicro: 10000, Status: LedgerPending, CreatedAt: inMonth,
	})
	// Another buyer and a previous month are excluded.
	seedWalletLedger(t, f.db, LedgerEntry{
		RequestID: "w-other-1", EntryType: EntryBuyerDebit, Account: "user:" + other,
		AmountMicro: -9999, Status: LedgerAvailable, CreatedAt: inMonth,
	})
	seedWalletLedger(t, f.db, LedgerEntry{
		RequestID: "w-last-month", EntryType: EntryBuyerDebit, Account: "user:" + buyer,
		AmountMicro: -700, Status: LedgerAvailable, CreatedAt: lastMonth,
	})

	// Month savings: (10000-4000) + (5000-5000) = 6000. The other buyer and
	// last month are excluded.
	seedWalletRequest(t, f.db, Request{
		RequestID: "w-req-1", BuyerUserID: f.buyerID, BuyerTokenID: 1, SellerID: 9, ListingID: 9,
		Model: "deepseek-flash", Tier: TierPeak, CreatedAt: inMonth,
		OfficialCostMicro: 10000, TotalDebitMicro: 4000,
	})
	seedWalletRequest(t, f.db, Request{
		RequestID: "w-req-2", BuyerUserID: f.buyerID, BuyerTokenID: 1, SellerID: 9, ListingID: 9,
		Model: "deepseek-flash", Tier: TierOffpeak, CreatedAt: inMonth,
		OfficialCostMicro: 5000, TotalDebitMicro: 5000,
	})
	seedWalletRequest(t, f.db, Request{
		RequestID: "w-req-other", BuyerUserID: f.otherID, BuyerTokenID: 2, SellerID: 9, ListingID: 9,
		Model: "deepseek-flash", Tier: TierPeak, CreatedAt: inMonth,
		OfficialCostMicro: 9000, TotalDebitMicro: 1000,
	})
	seedWalletRequest(t, f.db, Request{
		RequestID: "w-req-last-month", BuyerUserID: f.buyerID, BuyerTokenID: 1, SellerID: 9, ListingID: 9,
		Model: "deepseek-flash", Tier: TierPeak, CreatedAt: lastMonth,
		OfficialCostMicro: 7000, TotalDebitMicro: 1000,
	})

	w := f.get(t, "/api/marketplace/wallet", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	body := decodeAPIBody(t, w)
	require.Equal(t, true, body["success"])
	data := apiData(t, body)
	assert.Equal(t, int64(quota), apiNum(t, data, "balance_micro"))
	assert.Equal(t, int64(7500), apiNum(t, data, "month_spend_micro"))
	assert.Equal(t, int64(6000), apiNum(t, data, "month_saved_micro"))

	// No login → 401.
	w = f.get(t, "/api/marketplace/wallet", 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestWalletLedgerPaginationAndOwnership checks the flow rows, newest-first
// pagination and the ownership boundary: another buyer's rows are invisible.
func TestWalletLedgerPaginationAndOwnership(t *testing.T) {
	f := newWalletFixture(t)
	now := time.Now()
	monthStart, _, err := shanghaiMonthRange(now)
	require.NoError(t, err)
	base := monthStart.Add(2 * time.Hour)

	buyer := strconv.FormatInt(f.buyerID, 10)
	other := strconv.FormatInt(f.otherID, 10)

	ledgerRows := []LedgerEntry{
		{RequestID: "w-oldest", EntryType: EntryBuyerDebit, Account: "user:" + buyer, AmountMicro: -100, CreatedAt: base},
		{RequestID: "w-tool", EntryType: EntryToolDebit, Account: "user:" + buyer, AmountMicro: -200, CreatedAt: base.Add(time.Minute)},
		{RequestID: "w-refund", EntryType: EntryRefund, Account: "user:" + buyer, AmountMicro: 300, CreatedAt: base.Add(2 * time.Minute)},
		{RequestID: "w-newest", EntryType: EntryBuyerDebit, Account: "buyer:" + buyer, AmountMicro: -400, CreatedAt: base.Add(3 * time.Minute)},
		{RequestID: "w-second-newest", EntryType: EntryBuyerDebit, Account: "user:" + buyer, AmountMicro: -500, CreatedAt: base.Add(4 * time.Minute)},
		// Seller income and another buyer's debit are not part of this flow.
		{RequestID: "w-seller", EntryType: EntrySellerCredit, Account: "user:" + buyer, AmountMicro: 1000, Status: LedgerPending, CreatedAt: base.Add(5 * time.Minute)},
		{RequestID: "w-other", EntryType: EntryBuyerDebit, Account: "user:" + other, AmountMicro: -900, CreatedAt: base.Add(6 * time.Minute)},
	}
	for _, row := range ledgerRows {
		seedWalletLedger(t, f.db, row)
	}

	// Page 1 of 2: the two newest buyer rows, newest first, seller/other
	// rows skipped.
	w := f.get(t, "/api/marketplace/wallet/ledger?page=1&page_size=2", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	data := apiData(t, decodeAPIBody(t, w))
	assert.Equal(t, int64(5), apiNum(t, data, "total"))
	assert.Equal(t, int64(1), apiNum(t, data, "page"))
	assert.Equal(t, int64(2), apiNum(t, data, "page_size"))
	items := apiPageItems(t, data)
	require.Len(t, items, 2)
	first := items[0].(map[string]any)
	assert.Equal(t, "w-second-newest", first["request_id"])
	assert.Equal(t, EntryBuyerDebit, first["entry_type"])
	assert.Equal(t, int64(-500), apiNum(t, first, "amount_micro"))
	assert.Equal(t, LedgerAvailable, first["status"])
	assert.NotEmpty(t, first["created_at"])
	second := items[1].(map[string]any)
	assert.Equal(t, "w-newest", second["request_id"])

	// Page 3 holds the last row.
	w = f.get(t, "/api/marketplace/wallet/ledger?page=3&page_size=2", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	data = apiData(t, decodeAPIBody(t, w))
	items = apiPageItems(t, data)
	require.Len(t, items, 1)
	assert.Equal(t, "w-oldest", items[0].(map[string]any)["request_id"])

	// page_size is capped at 100.
	w = f.get(t, "/api/marketplace/wallet/ledger?page=1&page_size=1000", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(100), apiNum(t, apiData(t, decodeAPIBody(t, w)), "page_size"))

	// The other buyer sees only their own row, never the buyer's five.
	w = f.get(t, "/api/marketplace/wallet/ledger", f.otherID)
	require.Equal(t, http.StatusOK, w.Code)
	data = apiData(t, decodeAPIBody(t, w))
	assert.Equal(t, int64(1), apiNum(t, data, "total"))
	items = apiPageItems(t, data)
	require.Len(t, items, 1)
	assert.Equal(t, "w-other", items[0].(map[string]any)["request_id"])

	// No login → 401.
	w = f.get(t, "/api/marketplace/wallet/ledger", 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestWalletTransactionsMergeOrderingAndPagination covers the unified stream:
// ledger debits/refunds and top-up orders interleave by created_at, pagination
// crosses the source boundary, and only buyer-side rows of the logged-in user
// are visible.
func TestWalletTransactionsMergeOrderingAndPagination(t *testing.T) {
	f := newWalletFixture(t)
	previousUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previousUnit })

	base := time.Unix(1_700_000_000, 0)
	buyer := strconv.FormatInt(f.buyerID, 10)
	other := strconv.FormatInt(f.otherID, 10)

	ledgerRows := []LedgerEntry{
		{RequestID: "tx-legacy", EntryType: EntryBuyerDebit, Account: "user:" + buyer, AmountMicro: -300_000, CreatedAt: base.Add(30 * time.Second)},
		{RequestID: "tx-debit", EntryType: EntryBuyerDebit, Account: "buyer:" + buyer, AmountMicro: -1_000_000, CreatedAt: base.Add(120 * time.Second)},
		{RequestID: "tx-tool", EntryType: EntryToolDebit, Account: "user:" + buyer, AmountMicro: -500_000, CreatedAt: base.Add(180 * time.Second)},
		{RequestID: "tx-refund", EntryType: EntryRefund, Account: "user:" + buyer, AmountMicro: 200_000, CreatedAt: base.Add(240 * time.Second)},
		// Another buyer and the same user's seller income never enter the
		// buyer's wallet stream.
		{RequestID: "tx-other", EntryType: EntryBuyerDebit, Account: "user:" + other, AmountMicro: -9_000_000, CreatedAt: base.Add(360 * time.Second)},
		{RequestID: "tx-seller", EntryType: EntrySellerCredit, Account: "seller:" + buyer, AmountMicro: 5_000_000, Status: LedgerPending, CreatedAt: base.Add(400 * time.Second)},
	}
	for _, row := range ledgerRows {
		seedWalletLedger(t, f.db, row)
	}
	seedWalletTopUp(t, f.db, model.TopUp{
		UserId: int(f.buyerID), Amount: 10, Money: 10, TradeNo: "trade-buyer-1",
		CreateTime: base.Add(60 * time.Second).Unix(), Status: common.TopUpStatusSuccess,
	})
	seedWalletTopUp(t, f.db, model.TopUp{
		UserId: int(f.otherID), Amount: 99, Money: 99, TradeNo: "trade-other-1",
		CreateTime: base.Add(300 * time.Second).Unix(), Status: common.TopUpStatusSuccess,
	})

	// Page 1 of 3: the same user's newest rows across every source. The
	// seller credit is the user's own income, so it belongs in the wallet.
	w := f.get(t, "/api/marketplace/wallet/transactions?p=1&page_size=2", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	data := apiData(t, decodeAPIBody(t, w))
	assert.Equal(t, int64(6), apiNum(t, data, "total"))
	assert.Equal(t, int64(1), apiNum(t, data, "page"))
	assert.Equal(t, int64(2), apiNum(t, data, "page_size"))

	items := apiPageItems(t, data)
	require.Len(t, items, 2)
	first := items[0].(map[string]any)
	assert.Equal(t, "tx-seller", apiStr(t, first, "reference"))
	assert.Equal(t, walletTxKindEarning, apiStr(t, first, "kind"))
	assert.Equal(t, int64(5_000_000), apiNum(t, first, "amount_micro"))
	assert.Equal(t, LedgerPending, apiStr(t, first, "status"))
	assert.Contains(t, apiStr(t, first, "id"), "ledger:")
	_, hasMoney := first["money"]
	assert.False(t, hasMoney, "ledger rows must not carry money")

	second := items[1].(map[string]any)
	assert.Equal(t, "tx-refund", apiStr(t, second, "reference"))
	assert.Equal(t, walletTxKindRefund, apiStr(t, second, "kind"))
	assert.Equal(t, int64(200_000), apiNum(t, second, "amount_micro"))

	// Page 2 stays on the buyer's debit side.
	w = f.get(t, "/api/marketplace/wallet/transactions?p=2&page_size=2", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	data = apiData(t, decodeAPIBody(t, w))
	items = apiPageItems(t, data)
	require.Len(t, items, 2)
	assert.Equal(t, "tx-tool", apiStr(t, items[0].(map[string]any), "reference"))
	assert.Equal(t, walletTxKindUsage, apiStr(t, items[0].(map[string]any), "kind"))
	assert.Equal(t, "tx-debit", apiStr(t, items[1].(map[string]any), "reference"))

	// Page 3 crosses the source boundary: the top-up then the oldest ledger row.
	w = f.get(t, "/api/marketplace/wallet/transactions?p=3&page_size=2", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	data = apiData(t, decodeAPIBody(t, w))
	items = apiPageItems(t, data)
	require.Len(t, items, 2)
	topup := items[0].(map[string]any)
	assert.Equal(t, walletTxKindTopup, apiStr(t, topup, "kind"))
	assert.Equal(t, "trade-buyer-1", apiStr(t, topup, "reference"))
	assert.Equal(t, int64(10*500_000), apiNum(t, topup, "amount_micro"), "amount is CNY × QuotaPerUnit")
	assert.Equal(t, int64(10), apiNum(t, topup, "money"))
	assert.Equal(t, common.TopUpStatusSuccess, apiStr(t, topup, "status"))
	assert.Contains(t, apiStr(t, topup, "id"), "topup:")
	createdAt, err := time.Parse(time.RFC3339Nano, apiStr(t, topup, "created_at"))
	require.NoError(t, err)
	assert.Equal(t, base.Add(60*time.Second).Unix(), createdAt.Unix(), "create_time is Unix seconds")
	assert.Equal(t, "tx-legacy", apiStr(t, items[1].(map[string]any), "reference"))

	// Page 4 is empty but keeps total.
	w = f.get(t, "/api/marketplace/wallet/transactions?p=4&page_size=2", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	data = apiData(t, decodeAPIBody(t, w))
	assert.Equal(t, int64(6), apiNum(t, data, "total"))
	assert.Empty(t, apiPageItems(t, data))

	// The other buyer sees only their own top-up and debit.
	w = f.get(t, "/api/marketplace/wallet/transactions?p=1&page_size=20", f.otherID)
	require.Equal(t, http.StatusOK, w.Code)
	data = apiData(t, decodeAPIBody(t, w))
	assert.Equal(t, int64(2), apiNum(t, data, "total"))
	for _, raw := range apiPageItems(t, data) {
		item := raw.(map[string]any)
		ref := apiStr(t, item, "reference")
		assert.True(t, ref == "tx-other" || ref == "trade-other-1", "unexpected row %q", ref)
	}

	// No login → 401.
	w = f.get(t, "/api/marketplace/wallet/transactions", 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestWalletTransactionsKindFilter checks the five filters: usage is only the
// buyer's debit/tool rows, refund is its own kind, earning is seller credit,
// withdrawal is a payout, and topup is the order table.
func TestWalletTransactionsKindFilter(t *testing.T) {
	f := newWalletFixture(t)
	base := time.Unix(1_700_100_000, 0)
	buyer := strconv.FormatInt(f.buyerID, 10)

	seedWalletLedger(t, f.db, LedgerEntry{
		RequestID: "kf-debit", EntryType: EntryBuyerDebit, Account: "user:" + buyer,
		AmountMicro: -100, CreatedAt: base,
	})
	seedWalletLedger(t, f.db, LedgerEntry{
		RequestID: "kf-refund", EntryType: EntryRefund, Account: "user:" + buyer,
		AmountMicro: 50, CreatedAt: base.Add(time.Second),
	})
	seedWalletLedger(t, f.db, LedgerEntry{
		RequestID: "kf-earning", EntryType: EntrySellerCredit, Account: "seller:" + buyer,
		AmountMicro: 500, Status: LedgerPending, CreatedAt: base.Add(2 * time.Second),
	})
	require.NoError(t, f.db.Create(&Payout{
		SellerID: f.buyerID, AmountMicro: 50_000_000, Status: PayoutPending,
		IdempotencyKey: "kf-payout-1", CreatedAt: base.Add(3 * time.Second),
	}).Error)
	seedWalletTopUp(t, f.db, model.TopUp{
		UserId: int(f.buyerID), Amount: 1, Money: 1, TradeNo: "kf-trade",
		CreateTime: base.Add(4 * time.Second).Unix(),
	})

	cases := []struct {
		kind string
		want int64
	}{
		{walletTxKindAll, 5},
		{walletTxKindUsage, 1},
		{walletTxKindRefund, 1},
		{walletTxKindEarning, 1},
		{walletTxKindWithdrawal, 1},
		{walletTxKindTopup, 1},
	}
	for _, tc := range cases {
		w := f.get(t, "/api/marketplace/wallet/transactions?p=1&page_size=20&kind="+tc.kind, f.buyerID)
		require.Equal(t, http.StatusOK, w.Code, "kind=%s body: %s", tc.kind, w.Body.String())
		data := apiData(t, decodeAPIBody(t, w))
		assert.Equal(t, tc.want, apiNum(t, data, "total"), "kind=%s total", tc.kind)
		items := apiPageItems(t, data)
		require.Len(t, items, int(tc.want), "kind=%s rows", tc.kind)
		for _, raw := range items {
			item := raw.(map[string]any)
			if tc.kind != walletTxKindAll {
				assert.Equal(t, tc.kind, apiStr(t, item, "kind"), "kind=%s row kind", tc.kind)
			}
		}
	}

	// An unknown kind behaves like all.
	w := f.get(t, "/api/marketplace/wallet/transactions?p=1&page_size=20&kind=bogus", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(5), apiNum(t, apiData(t, decodeAPIBody(t, w)), "total"))
}

// TestWalletTransactionsExport checks the CSV export: UTF-8 BOM, the required
// Chinese header, kind filtering, yuan amounts and — most importantly — that
// the export is scoped to the logged-in user.
func TestWalletTransactionsExport(t *testing.T) {
	f := newWalletFixture(t)
	previousUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previousUnit })

	base := time.Unix(1_700_200_000, 0)
	buyer := strconv.FormatInt(f.buyerID, 10)
	other := strconv.FormatInt(f.otherID, 10)
	for _, row := range []LedgerEntry{
		{RequestID: "exp-debit", EntryType: EntryBuyerDebit, Account: "user:" + buyer, AmountMicro: -1_250_000, CreatedAt: base},
		{RequestID: "exp-refund", EntryType: EntryRefund, Account: "user:" + buyer, AmountMicro: 500_000, CreatedAt: base.Add(time.Second)},
		{RequestID: "exp-earning", EntryType: EntrySellerCredit, Account: "seller:" + buyer, AmountMicro: 3_000_000, Status: LedgerPending, CreatedAt: base.Add(2 * time.Second)},
		{RequestID: "exp-other", EntryType: EntryBuyerDebit, Account: "user:" + other, AmountMicro: -9_000_000, CreatedAt: base.Add(3 * time.Second)},
	} {
		seedWalletLedger(t, f.db, row)
	}
	seedWalletTopUp(t, f.db, model.TopUp{
		UserId: int(f.buyerID), Amount: 10, Money: 10, TradeNo: "exp-trade",
		CreateTime: base.Add(4 * time.Second).Unix(), Status: common.TopUpStatusSuccess,
	})
	seedWalletTopUp(t, f.db, model.TopUp{
		UserId: int(f.otherID), Amount: 99, Money: 99, TradeNo: "exp-other-trade",
		CreateTime: base.Add(5 * time.Second).Unix(), Status: common.TopUpStatusSuccess,
	})
	require.NoError(t, f.db.Create(&Payout{
		SellerID: f.buyerID, AmountMicro: 60_000_000, Status: PayoutApproved,
		IdempotencyKey: "exp-payout-1", CreatedAt: base.Add(6 * time.Second),
	}).Error)

	w := f.get(t, "/api/marketplace/wallet/transactions/export?kind=all", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv"), "content type: %s", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	raw := w.Body.String()
	require.True(t, strings.HasPrefix(raw, "\ufeff"), "export must start with a UTF-8 BOM")
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, "\ufeff"))).ReadAll()
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	assert.Equal(t, []string{"时间", "类型", "说明", "金额元", "状态", "单号"}, rows[0])
	assert.Len(t, rows, 6, "header + buyer's five flow rows")

	body := strings.TrimPrefix(raw, "\ufeff")
	assert.Contains(t, body, "-1.25", "debit amount in yuan")
	assert.Contains(t, body, "0.50", "refund amount in yuan")
	assert.Contains(t, body, "exp-trade")
	assert.Contains(t, body, "exp-payout-1")
	assert.Contains(t, body, "消费")
	assert.Contains(t, body, "退款")
	assert.Contains(t, body, "收益")
	assert.Contains(t, body, "提现")
	assert.Contains(t, body, "充值")
	assert.NotContains(t, body, "exp-other", "another user's ledger row must not be exported")
	assert.NotContains(t, body, "exp-other-trade", "another user's top-up must not be exported")

	// kind=refund exports just the refund row.
	w = f.get(t, "/api/marketplace/wallet/transactions/export?kind=refund", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	body = strings.TrimPrefix(w.Body.String(), "\ufeff")
	assert.Contains(t, body, "exp-refund")
	assert.NotContains(t, body, "exp-debit")
	assert.NotContains(t, body, "exp-trade")
	assert.NotContains(t, body, "exp-payout-1")

	// kind=withdrawal exports just the payout.
	w = f.get(t, "/api/marketplace/wallet/transactions/export?kind=withdrawal", f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	body = strings.TrimPrefix(w.Body.String(), "\ufeff")
	assert.Contains(t, body, "exp-payout-1")
	assert.Contains(t, body, "提现")
	assert.NotContains(t, body, "exp-debit")

	// No login → 401.
	w = f.get(t, "/api/marketplace/wallet/transactions/export", 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestWalletTransactionsExportOtherUser proves the ownership boundary even
// when the other user calls the export endpoint directly: they get only their
// own rows, never the first buyer's.
func TestWalletTransactionsExportOtherUser(t *testing.T) {
	f := newWalletFixture(t)
	base := time.Unix(1_700_300_000, 0)
	buyer := strconv.FormatInt(f.buyerID, 10)
	seedWalletLedger(t, f.db, LedgerEntry{
		RequestID: "iso-buyer-only", EntryType: EntryBuyerDebit,
		Account: "user:" + buyer, AmountMicro: -100, CreatedAt: base,
	})
	w := f.get(t, "/api/marketplace/wallet/transactions/export?kind=usage", f.otherID)
	require.Equal(t, http.StatusOK, w.Code)
	body := strings.TrimPrefix(w.Body.String(), "\ufeff")
	assert.NotContains(t, body, "iso-buyer-only")
	// Only the header remains.
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestWalletTopUpAmountMicro(t *testing.T) {
	previousUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previousUnit })

	assert.Equal(t, int64(500_000), topUpAmountMicro(1))
	assert.Equal(t, int64(5_000_000), topUpAmountMicro(10))
	assert.NotEqual(t, int64(10), topUpAmountMicro(10))
	assert.Equal(t, int64(0), topUpAmountMicro(0))
	assert.Equal(t, int64(0), topUpAmountMicro(-5))
}
