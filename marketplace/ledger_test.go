package marketplace

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ledgerTestNow is the fixed settlement instant used by all ledger tests;
// deterministic input, deterministic expectations (AGENTS.md backend test
// quality). UTC round-trips exactly through the glebarez driver.
var ledgerTestNow = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// baseSettlement is the canonical valid settlement: total debit
// 1,234,567 + 5,000 = 1,239,567 微元, platform fee
// round-half-up(1239567*7%) = (1239567*7+50)/100 = 86,770,
// seller credit 1,152,797.
func baseSettlement() Settlement {
	return Settlement{
		RequestID:       "r1",
		BuyerUserID:     10011,
		SellerUserID:    10012,
		ListingID:       1,
		PriceVersion:    1,
		MultiplierMilli: 300,
		TokenDebit:      1_234_567,
		ToolDebit:       5_000,
		Now:             ledgerTestNow,
	}
}

const (
	wantTotal  = int64(1_239_567)
	wantFee    = int64(86_770)
	wantCredit = wantTotal - wantFee // 1,152,797
)

func rowsOfRequest(t *testing.T, db *gorm.DB, requestID string) []LedgerEntry {
	t.Helper()
	var rows []LedgerEntry
	require.NoError(t, db.Where("request_id = ?", requestID).Order("id").Find(&rows).Error)
	return rows
}

func rowByType(t *testing.T, rows []LedgerEntry, entryType string) LedgerEntry {
	t.Helper()
	for _, r := range rows {
		if r.EntryType == entryType {
			return r
		}
	}
	require.FailNow(t, "missing ledger row", "request has no row of type %s", entryType)
	return LedgerEntry{}
}

func signedSum(rows []LedgerEntry) int64 {
	var sum int64
	for _, r := range rows {
		sum += r.AmountMicro
	}
	return sum
}

func activeSum(rows []LedgerEntry) int64 {
	var sum int64
	for _, r := range rows {
		if r.Status != LedgerReversed {
			sum += r.AmountMicro
		}
	}
	return sum
}

func TestPostSettlementIdentity(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	s := baseSettlement()
	require.NoError(t, PostSettlement(db, s))

	rows := rowsOfRequest(t, db, s.RequestID)
	require.Len(t, rows, 4, "buyer_debit + tool_debit + seller_credit + platform_fee")

	buyer := rowByType(t, rows, EntryBuyerDebit)
	assert.Equal(t, UserAccount(10011), buyer.Account)
	assert.Equal(t, int64(-1_234_567), buyer.AmountMicro)
	assert.Equal(t, LedgerAvailable, buyer.Status)

	tool := rowByType(t, rows, EntryToolDebit)
	assert.Equal(t, UserAccount(10011), tool.Account)
	assert.Equal(t, int64(-5_000), tool.AmountMicro)
	assert.Equal(t, LedgerAvailable, tool.Status)

	seller := rowByType(t, rows, EntrySellerCredit)
	assert.Equal(t, UserAccount(10012), seller.Account)
	assert.Equal(t, wantCredit, seller.AmountMicro)
	assert.Equal(t, LedgerPending, seller.Status, "seller income freezes as pending (T+7, §3.9)")
	require.NotNil(t, seller.AvailableAt)
	assert.True(t, seller.AvailableAt.Equal(s.Now.Add(DefaultMaturity)),
		"available_at must equal Now + 7*24h, got %s want %s", seller.AvailableAt, s.Now.Add(DefaultMaturity))

	fee := rowByType(t, rows, EntryPlatformFee)
	assert.Equal(t, PlatformAccount(), fee.Account)
	assert.Equal(t, wantFee, fee.AmountMicro, "round-half-up(1239567*7%%) = (1239567*7+50)/100 = 86770")
	assert.Equal(t, LedgerAvailable, fee.Status)

	assert.Zero(t, signedSum(rows), "signed amounts of one request must sum to zero")
	assert.Equal(t, -buyer.AmountMicro+(-tool.AmountMicro), seller.AmountMicro+fee.AmountMicro,
		"|buyer_debit| + |tool_debit| == seller_credit + platform_fee")
}

func TestPostSettlementNoTool(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	s := baseSettlement()
	s.ToolDebit = 0
	require.NoError(t, PostSettlement(db, s))

	rows := rowsOfRequest(t, db, s.RequestID)
	require.Len(t, rows, 3, "no tool_debit row when ToolDebit == 0")
	for _, r := range rows {
		assert.NotEqual(t, EntryToolDebit, r.EntryType, "tool_debit row must not exist")
	}

	buyer := rowByType(t, rows, EntryBuyerDebit)
	seller := rowByType(t, rows, EntrySellerCredit)
	fee := rowByType(t, rows, EntryPlatformFee)
	assert.Zero(t, signedSum(rows))
	assert.Equal(t, -buyer.AmountMicro, seller.AmountMicro+fee.AmountMicro)
}

func TestPostSettlementZeroAmount(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	s := baseSettlement()
	s.TokenDebit = 0
	s.ToolDebit = 0
	require.NoError(t, PostSettlement(db, s))

	rows := rowsOfRequest(t, db, s.RequestID)
	require.Len(t, rows, 3)
	for _, r := range rows {
		assert.Zero(t, r.AmountMicro, "zero settlement writes zero rows, type %s", r.EntryType)
	}

	require.NoError(t, PostSettlement(db, s), "idempotent replay of a zero settlement")
	assert.Len(t, rowsOfRequest(t, db, s.RequestID), 3)
}

func TestPostSettlementIdempotentReplay(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	s := baseSettlement()
	require.NoError(t, PostSettlement(db, s))
	rowsBefore := rowsOfRequest(t, db, s.RequestID)
	sellerAvailBefore, sellerPendingBefore, err := AccountBalance(db, UserAccount(s.SellerUserID))
	require.NoError(t, err)

	require.NoError(t, PostSettlement(db, s), "replaying the identical settlement is a no-op")

	rowsAfter := rowsOfRequest(t, db, s.RequestID)
	assert.Len(t, rowsAfter, len(rowsBefore), "replay must not add rows")
	sellerAvailAfter, sellerPendingAfter, err := AccountBalance(db, UserAccount(s.SellerUserID))
	require.NoError(t, err)
	assert.Equal(t, sellerAvailBefore, sellerAvailAfter)
	assert.Equal(t, sellerPendingBefore, sellerPendingAfter)
	assert.Equal(t, Micro(0), sellerAvailAfter)
	assert.Equal(t, Micro(wantCredit), sellerPendingAfter)
}

func TestPostSettlementConflict(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	s := baseSettlement()
	require.NoError(t, PostSettlement(db, s))

	conflicting := s
	conflicting.TokenDebit++
	assert.ErrorIs(t, PostSettlement(db, conflicting), ErrSettlementConflict,
		"same request id with different figures must conflict")

	invalid := []struct {
		name string
		mut  func(*Settlement)
	}{
		{"empty request id", func(s *Settlement) { s.RequestID = "" }},
		{"negative token debit", func(s *Settlement) { s.RequestID = "bad-neg"; s.TokenDebit = -1 }},
		{"zero buyer", func(s *Settlement) { s.RequestID = "bad-buyer"; s.BuyerUserID = 0 }},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			bad := baseSettlement()
			tc.mut(&bad)
			assert.ErrorIs(t, PostSettlement(db, bad), ErrInvalidSettlement)
		})
	}
}

func TestPostSettlementConcurrent(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	s := baseSettlement()
	s.RequestID = "r-concurrent"

	const goroutines = 20
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = PostSettlement(db, s)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.Truef(t, err == nil || errors.Is(err, ErrSettlementConflict),
			"goroutine %d: only nil (idempotent replay) or a declared conflict are acceptable, got %v", i, err)
	}

	rows := rowsOfRequest(t, db, s.RequestID)
	require.Len(t, rows, 4, "concurrent identical submissions must converge to the single-submission row set")
	assert.Zero(t, signedSum(rows), "identity must hold after concurrent submission")
	buyer := rowByType(t, rows, EntryBuyerDebit)
	seller := rowByType(t, rows, EntrySellerCredit)
	fee := rowByType(t, rows, EntryPlatformFee)
	tool := rowByType(t, rows, EntryToolDebit)
	assert.Equal(t, -buyer.AmountMicro+(-tool.AmountMicro), seller.AmountMicro+fee.AmountMicro)
}

func TestPromoteAvailable(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	s := baseSettlement()
	require.NoError(t, PostSettlement(db, s))
	sellerAccount := UserAccount(s.SellerUserID)

	n, err := PromoteAvailable(db, s.Now.Add(6*24*time.Hour))
	require.NoError(t, err)
	assert.Zero(t, n, "one day before maturity nothing may be promoted")

	avail, pending, err := AccountBalance(db, sellerAccount)
	require.NoError(t, err)
	assert.Equal(t, Micro(0), avail)
	assert.Equal(t, Micro(wantCredit), pending)

	n, err = PromoteAvailable(db, s.Now.Add(7*24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "at exactly Now+7d the credit matures")

	seller := rowByType(t, rowsOfRequest(t, db, s.RequestID), EntrySellerCredit)
	assert.Equal(t, LedgerAvailable, seller.Status)

	avail, pending, err = AccountBalance(db, sellerAccount)
	require.NoError(t, err)
	assert.Equal(t, Micro(wantCredit), avail)
	assert.Equal(t, Micro(0), pending)
}

func TestReverseHappy(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	s := baseSettlement()
	require.NoError(t, PostSettlement(db, s))
	require.NoError(t, Reverse(db, s.RequestID, s.Now.Add(time.Hour)))

	rows := rowsOfRequest(t, db, s.RequestID)
	require.Len(t, rows, 7, "4 settlement rows + refund + reversal + fee_reversal")

	seller := rowByType(t, rows, EntrySellerCredit)
	assert.Equal(t, LedgerReversed, seller.Status, "the frozen credit is flipped to reversed")

	refund := rowByType(t, rows, EntryRefund)
	assert.Equal(t, UserAccount(s.BuyerUserID), refund.Account)
	assert.Equal(t, wantTotal, refund.AmountMicro)
	assert.Equal(t, LedgerAvailable, refund.Status)

	reversal := rowByType(t, rows, EntryReversal)
	assert.Equal(t, UserAccount(s.SellerUserID), reversal.Account)
	assert.Equal(t, -wantCredit, reversal.AmountMicro)
	assert.Equal(t, LedgerReversed, reversal.Status)

	feeReversal := rowByType(t, rows, EntryFeeReversal)
	assert.Equal(t, PlatformAccount(), feeReversal.Account)
	assert.Equal(t, -wantFee, feeReversal.AmountMicro)
	assert.Equal(t, LedgerAvailable, feeReversal.Status)

	assert.Zero(t, signedSum(rows), "all rows including reversed ones must still sum to zero")
	assert.Zero(t, activeSum(rows), "active (non-reversed) rows must sum to zero")

	buyerAvail, buyerPending, err := AccountBalance(db, UserAccount(s.BuyerUserID))
	require.NoError(t, err)
	assert.Equal(t, Micro(0), buyerAvail)
	assert.Equal(t, Micro(0), buyerPending)

	sellerAvail, sellerPending, err := AccountBalance(db, UserAccount(s.SellerUserID))
	require.NoError(t, err)
	assert.Equal(t, Micro(0), sellerAvail, "reversed rows are excluded from balances")
	assert.Equal(t, Micro(0), sellerPending)

	platformAvail, platformPending, err := AccountBalance(db, PlatformAccount())
	require.NoError(t, err)
	assert.Equal(t, Micro(0), platformAvail)
	assert.Equal(t, Micro(0), platformPending)
}

func TestReverseGuards(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	assert.ErrorIs(t, Reverse(db, "no-such-request", ledgerTestNow), ErrNotReversible,
		"reversing an unknown request id")

	s := baseSettlement()
	require.NoError(t, PostSettlement(db, s))
	require.NoError(t, Reverse(db, s.RequestID, s.Now.Add(time.Hour)))
	assert.ErrorIs(t, Reverse(db, s.RequestID, s.Now.Add(2*time.Hour)), ErrAlreadyReversed,
		"a second reversal of the same request")

	matured := baseSettlement()
	matured.RequestID = "r-matured"
	require.NoError(t, PostSettlement(db, matured))
	promoted, err := PromoteAvailable(db, matured.Now.Add(8*24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(1), promoted)
	assert.ErrorIs(t, Reverse(db, matured.RequestID, matured.Now.Add(8*24*time.Hour)), ErrNotReversible,
		"a matured (T+7) credit is outside the reversal window")
}

func TestAccountBalanceEmpty(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	avail, pending, err := AccountBalance(db, UserAccount(42424))
	require.NoError(t, err)
	assert.Equal(t, Micro(0), avail)
	assert.Equal(t, Micro(0), pending)
}
