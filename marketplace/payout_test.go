package marketplace

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// payoutTestNow is the fixed instant used by all payout tests; deterministic
// input, deterministic expectations (AGENTS.md backend test quality).
var payoutTestNow = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// newPayoutSeller creates a seller row and seeds its withdrawable balance by
// inserting an available seller_credit ledger row (fixture for §5 阶段 6).
func newPayoutSeller(t *testing.T, db *gorm.DB, sellerID, balanceMicro int64) {
	t.Helper()
	require.NoError(t, db.Create(&Seller{
		UserID:           sellerID,
		Status:           "active",
		RealNameStatus:   "verified",
		AgreementVersion: "v1",
		CreditScore:      100,
	}).Error)
	require.NoError(t, db.Create(&LedgerEntry{
		RequestID:   fmt.Sprintf("payout-seed-%d", sellerID),
		EntryType:   EntrySellerCredit,
		Account:     SellerAccount(sellerID),
		AmountMicro: balanceMicro,
		Status:      LedgerAvailable,
		CreatedAt:   payoutTestNow,
	}).Error)
}

// countPayouts returns the number of payout rows for a seller.
func countPayouts(t *testing.T, db *gorm.DB, sellerID int64) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&Payout{}).Where("seller_id = ?", sellerID).Count(&n).Error)
	return n
}

// TestAvailableBalanceExcludesBuyerSpend pins the H5 withdrawable rule: the
// balance comes from seller:<id> only, so a seller's own marketplace purchases
// never reduce what they can withdraw.
func TestAvailableBalanceExcludesBuyerSpend(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	const seller = int64(10012)
	income := baseSettlement()
	income.BuyerUserID = 10011
	income.SellerUserID = seller
	require.NoError(t, PostSettlement(db, income))
	_, err := PromoteAvailable(db, income.Now.Add(DefaultMaturity))
	require.NoError(t, err)

	before, err := AvailableBalance(db, seller)
	require.NoError(t, err)
	assert.Equal(t, Micro(wantCredit), before)

	spend := baseSettlement()
	spend.RequestID = "available-spend"
	spend.BuyerUserID = seller
	spend.SellerUserID = 10013
	spend.TokenDebit = 30_000_000
	spend.ToolDebit = 0
	require.NoError(t, PostSettlement(db, spend))

	after, err := AvailableBalance(db, seller)
	require.NoError(t, err)
	assert.Equal(t, before, after)

	sellerAvail, _, err := AccountBalance(db, SellerAccount(seller))
	require.NoError(t, err)
	assert.Equal(t, Micro(wantCredit), sellerAvail, "buyer spending is booked to buyer:<id> only")
}

func TestPayoutRequestBasic(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	const seller = 10011
	newPayoutSeller(t, db, seller, 100_000_000) // 100 元

	p, err := RequestPayout(db, seller, 60_000_000, "basic-1", payoutTestNow) // 60 元
	require.NoError(t, err)
	assert.Equal(t, PayoutPending, p.Status)
	assert.Equal(t, int64(60_000_000), p.AmountMicro)
	assert.Equal(t, int64(seller), p.SellerID)

	bal, err := AvailableBalance(db, seller)
	require.NoError(t, err)
	assert.Equal(t, Micro(40_000_000), bal, "100元 − 60元 active = 40元 withdrawable")
}

func TestPayoutBelowMinimum(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	const seller = 10011
	newPayoutSeller(t, db, seller, 100_000_000)

	_, err := RequestPayout(db, seller, 49_999_999, "below-1", payoutTestNow) // 49.999999 元 < ¥50 (§3.9)
	assert.ErrorIs(t, err, ErrPayoutBelowMinimum)

	_, err = RequestPayout(db, seller, 0, "zero", payoutTestNow)
	assert.ErrorIs(t, err, ErrPayoutBelowMinimum, "amountMicro <= 0 must be rejected")

	_, err = RequestPayout(db, seller, -5, "neg", payoutTestNow)
	assert.ErrorIs(t, err, ErrPayoutBelowMinimum)

	assert.Equal(t, int64(0), countPayouts(t, db, seller), "no payout row may be created on rejection")
}

func TestPayoutFrequency(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	const seller = 10011
	// Enough balance for every request in this test.
	newPayoutSeller(t, db, seller, 1_000_000_000)

	// First payout at t0 succeeds.
	_, err := RequestPayout(db, seller, 50_000_000, "freq-1", payoutTestNow)
	require.NoError(t, err)

	// A second payout inside the 7-day window is too frequent.
	_, err = RequestPayout(db, seller, 50_000_000, "freq-2", payoutTestNow.Add(6*24*time.Hour))
	assert.ErrorIs(t, err, ErrPayoutTooFrequent)

	// Exactly at 7 days the window is still closed (rolling window: within 7 days).
	_, err = RequestPayout(db, seller, 50_000_000, "freq-3", payoutTestNow.Add(7*24*time.Hour))
	assert.ErrorIs(t, err, ErrPayoutTooFrequent)

	// 8 days later the window has rolled over and a new payout succeeds.
	p, err := RequestPayout(db, seller, 50_000_000, "freq-4", payoutTestNow.Add(8*24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, PayoutPending, p.Status)

	// A rejected payout does not block an immediate re-request.
	require.NoError(t, RejectPayout(db, p.ID, 1, payoutTestNow.Add(8*24*time.Hour)))
	_, err = RequestPayout(db, seller, 50_000_000, "freq-5", payoutTestNow.Add(8*24*time.Hour+time.Minute))
	require.NoError(t, err, "a rejected payout is not in the active set, so it does not count toward frequency")
}

func TestPayoutIdempotency(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	const seller = 10011
	newPayoutSeller(t, db, seller, 100_000_000)

	first, err := RequestPayout(db, seller, 60_000_000, "idem-1", payoutTestNow)
	require.NoError(t, err)
	assert.Equal(t, PayoutPending, first.Status)

	// Same key, same params → same payout, nil error, still one row.
	again, err := RequestPayout(db, seller, 60_000_000, "idem-1", payoutTestNow.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, first.ID, again.ID, "idempotent replay returns the existing payout")
	assert.Equal(t, int64(1), countPayouts(t, db, seller), "replay must not add a row")

	// Same key, different amount → duplicate.
	_, err = RequestPayout(db, seller, 50_000_000, "idem-1", payoutTestNow.Add(2*time.Minute))
	assert.ErrorIs(t, err, ErrPayoutDuplicate)

	// Same key, different seller → duplicate.
	const otherSeller = 10012
	newPayoutSeller(t, db, otherSeller, 100_000_000)
	_, err = RequestPayout(db, otherSeller, 60_000_000, "idem-1", payoutTestNow.Add(3*time.Minute))
	assert.ErrorIs(t, err, ErrPayoutDuplicate)
}

// runConcurrentPayouts fires n goroutines each requesting amountMicro with a
// unique idempotency key at the same instant, and returns the number that
// succeeded.
func runConcurrentPayouts(t *testing.T, db *gorm.DB, sellerID, amountMicro int64, n int) int {
	t.Helper()
	success := make([]bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := RequestPayout(db, sellerID, amountMicro, fmt.Sprintf("conc-%d", i), payoutTestNow)
			success[i] = err == nil
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, s := range success {
		if s {
			ok++
		}
	}
	return ok
}

func TestPayoutConcurrent(t *testing.T) {
	t.Run("fifty yuan each", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		const seller = 10011
		newPayoutSeller(t, db, seller, 500_000_000) // 500 元

		ok := runConcurrentPayouts(t, db, seller, 50_000_000, 50) // 50 × 50 元 (¥50 最低额, §3.9)
		assert.Equal(t, 10, ok, "500元 / 50元 → exactly 10 succeed")

		assert.True(t, int64(ok)*50_000_000 <= 500_000_000, "success × amount must not exceed balance")

		bal, err := AvailableBalance(db, seller)
		require.NoError(t, err)
		assert.True(t, bal >= 0, "available balance must never go negative, got %d", bal)

		assert.Equal(t, int64(ok), countPayouts(t, db, seller), "mp_payouts rows == success count")
	})

	t.Run("sixty yuan each", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		const seller = 10012
		newPayoutSeller(t, db, seller, 100_000_000) // 100 元

		ok := runConcurrentPayouts(t, db, seller, 60_000_000, 50) // 50 × 60 元
		assert.Equal(t, 1, ok, "100元 / 60元 → exactly 1 succeeds")

		bal, err := AvailableBalance(db, seller)
		require.NoError(t, err)
		assert.True(t, bal >= 0, "available balance must never go negative, got %d", bal)

		assert.Equal(t, int64(ok), countPayouts(t, db, seller), "mp_payouts rows == success count")
	})
}

func TestPayoutApprovalFlow(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	const seller = 10011
	newPayoutSeller(t, db, seller, 100_000_000)

	p, err := RequestPayout(db, seller, 60_000_000, "approve-1", payoutTestNow)
	require.NoError(t, err)

	// pending → approved (conditional update).
	require.NoError(t, ApprovePayout(db, p.ID, 1, payoutTestNow.Add(time.Minute)))
	var stored Payout
	require.NoError(t, db.First(&stored, p.ID).Error)
	assert.Equal(t, PayoutApproved, stored.Status)
	assert.Equal(t, int64(1), stored.ReviewerID)
	require.NotNil(t, stored.ReviewedAt)

	// Approving again → not pending.
	assert.ErrorIs(t, ApprovePayout(db, p.ID, 1, payoutTestNow.Add(2*time.Minute)), ErrPayoutNotPending)
}

func TestPayoutRejectRefunds(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	const seller = 10011
	newPayoutSeller(t, db, seller, 100_000_000)

	p, err := RequestPayout(db, seller, 60_000_000, "reject-1", payoutTestNow)
	require.NoError(t, err)

	balBefore, err := AvailableBalance(db, seller)
	require.NoError(t, err)
	assert.Equal(t, Micro(40_000_000), balBefore, "60元 reserved while pending")

	// Reject → rejected; the amount returns to the withdrawable balance.
	require.NoError(t, RejectPayout(db, p.ID, 1, payoutTestNow.Add(time.Minute)))
	balAfter, err := AvailableBalance(db, seller)
	require.NoError(t, err)
	assert.Equal(t, Micro(100_000_000), balAfter, "rejected payout is not active, so balance recovers")

	// Rejecting again → not pending, and no double refund.
	assert.ErrorIs(t, RejectPayout(db, p.ID, 1, payoutTestNow.Add(2*time.Minute)), ErrPayoutNotPending)
	balFinal, err := AvailableBalance(db, seller)
	require.NoError(t, err)
	assert.Equal(t, Micro(100_000_000), balFinal, "a second reject must not refund twice")
}

func TestPayoutMarkPaid(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	const seller = 10011
	newPayoutSeller(t, db, seller, 100_000_000)

	p, err := RequestPayout(db, seller, 60_000_000, "paid-1", payoutTestNow)
	require.NoError(t, err)
	require.NoError(t, ApprovePayout(db, p.ID, 1, payoutTestNow.Add(time.Minute)))

	// approved → paid.
	require.NoError(t, MarkPayoutPaid(db, p.ID, payoutTestNow.Add(2*time.Minute)))
	var stored Payout
	require.NoError(t, db.First(&stored, p.ID).Error)
	assert.Equal(t, PayoutPaid, stored.Status)

	// A paid payout stays in the active set: it is still reserved.
	bal, err := AvailableBalance(db, seller)
	require.NoError(t, err)
	assert.Equal(t, Micro(40_000_000), bal, "paid payout remains active, so 60元 stays reserved")

	// Marking paid again → not pending.
	assert.ErrorIs(t, MarkPayoutPaid(db, p.ID, payoutTestNow.Add(3*time.Minute)), ErrPayoutNotPending)
}

func TestPayoutNotFound(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	const seller = 10011
	newPayoutSeller(t, db, seller, 100_000_000)

	// No seller row for this id.
	_, err := RequestPayout(db, 99999, 60_000_000, "nope-1", payoutTestNow)
	assert.ErrorIs(t, err, ErrPayoutNotFound)

	// Approve/reject/mark-paid on a nonexistent payout.
	assert.ErrorIs(t, ApprovePayout(db, 424242, 1, payoutTestNow), ErrPayoutNotFound)
	assert.ErrorIs(t, RejectPayout(db, 424242, 1, payoutTestNow), ErrPayoutNotFound)
	assert.ErrorIs(t, MarkPayoutPaid(db, 424242, payoutTestNow), ErrPayoutNotFound)
}

func TestPayoutConcurrentApproval(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	const seller = 10011
	newPayoutSeller(t, db, seller, 100_000_000)

	p, err := RequestPayout(db, seller, 60_000_000, "conc-approve", payoutTestNow)
	require.NoError(t, err)

	// 20 goroutines mix approve and reject on the same pending payout; exactly
	// one conditional UPDATE may win.
	const n = 20
	success := make([]bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				err = ApprovePayout(db, p.ID, int64(i+1), payoutTestNow.Add(time.Minute))
			} else {
				err = RejectPayout(db, p.ID, int64(i+1), payoutTestNow.Add(time.Minute))
			}
			// Only a nil (won the conditional update) or ErrPayoutNotPending
			// (lost the race) is acceptable.
			success[i] = err == nil
			if err != nil && !errors.Is(err, ErrPayoutNotPending) {
				t.Errorf("goroutine %d: unexpected error %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	wins := 0
	for _, s := range success {
		if s {
			wins++
		}
	}
	assert.Equal(t, 1, wins, "exactly one of the concurrent approve/reject calls may win")

	var stored Payout
	require.NoError(t, db.First(&stored, p.ID).Error)
	assert.True(t, stored.Status == PayoutApproved || stored.Status == PayoutRejected,
		"the payout must end in exactly one terminal state, got %s", stored.Status)
}
