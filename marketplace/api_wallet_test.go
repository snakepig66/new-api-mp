package marketplace

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

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
	require.NoError(t, db.AutoMigrate(&model.User{}))

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
