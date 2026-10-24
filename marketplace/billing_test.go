package marketplace

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// TestMain pins common.RedisEnabled to false for the whole test binary. The
// variable defaults to true and is only cleared by InitRedisClient, which
// tests never run; without this the quota reserve path would take the Redis
// branch and dereference the nil client. It is set exactly once here rather
// than restored per test because model.IncreaseUserQuota syncs the quota
// cache from a gopool goroutine that reads the flag asynchronously — a
// per-test restore would race with that read.
func TestMain(m *testing.M) {
	common.RedisEnabled = false
	os.Exit(m.Run())
}

// newBillingFixture builds an isolated database with the full upstream users
// schema (migrateAll only creates the id column; AutoMigrate adds quota and
// the rest), points the model package's global handle at it so the atomic
// quota helpers (model.TryReserveUserQuota / model.IncreaseUserQuota) operate
// on this database, and creates one buyer row with startQuota.
func newBillingFixture(t *testing.T, startQuota int) (*gorm.DB, int64) {
	t.Helper()
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}))

	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })

	buyer := model.User{Username: "buyer", Password: "placeholder-pass", Quota: startQuota}
	require.NoError(t, db.Create(&buyer).Error)
	return db, int64(buyer.Id)
}

func buyerQuota(t *testing.T, buyerID int64) int {
	t.Helper()
	quota, err := model.GetUserQuota(int(buyerID), true)
	require.NoError(t, err)
	return quota
}

// 1. Pre-freeze deducts the estimated amount atomically.
func TestBillingPreFreeze(t *testing.T) {
	db, buyerID := newBillingFixture(t, 1000)

	s, err := NewBillingSession(db, buyerID, 300)
	require.NoError(t, err)
	assert.Equal(t, 700, buyerQuota(t, buyerID))
	assert.Equal(t, 300, s.GetPreConsumedQuota())
	assert.True(t, s.NeedsRefund())
}

// 2. Pre-freeze beyond the balance fails with ErrInsufficientBalance and
// leaves the balance untouched.
func TestBillingPreFreezeInsufficient(t *testing.T) {
	_, buyerID := newBillingFixture(t, 100)

	s, err := NewBillingSession(nil, buyerID, 1000)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInsufficientBalance))
	assert.Nil(t, s)
	assert.Equal(t, 100, buyerQuota(t, buyerID))
}

// 3. Settle with actual < preConsumed refunds the difference.
func TestBillingSettleRefundsDifference(t *testing.T) {
	_, buyerID := newBillingFixture(t, 1000)

	s, err := NewBillingSession(nil, buyerID, 1000)
	require.NoError(t, err)
	assert.Equal(t, 0, buyerQuota(t, buyerID))

	require.NoError(t, s.Settle(300))
	assert.Equal(t, 700, buyerQuota(t, buyerID), "1000 frozen, 300 actual -> 700 refunded")
	assert.False(t, s.NeedsRefund())

	require.NoError(t, s.Settle(300), "settle is idempotent")
	assert.Equal(t, 700, buyerQuota(t, buyerID))
}

// 4. Settle with actual > preConsumed tops up; a short balance is drained to
// exactly zero, never negative, and the shortfall is logged (SysError).
func TestBillingSettleTopUp(t *testing.T) {
	t.Run("covered", func(t *testing.T) {
		_, buyerID := newBillingFixture(t, 1000)
		s, err := NewBillingSession(nil, buyerID, 300)
		require.NoError(t, err)
		assert.Equal(t, 700, buyerQuota(t, buyerID))

		require.NoError(t, s.Settle(500))
		assert.Equal(t, 500, buyerQuota(t, buyerID), "300 frozen, 500 actual -> 200 more deducted")
	})

	t.Run("shortfall drains to zero", func(t *testing.T) {
		_, buyerID := newBillingFixture(t, 350)
		s, err := NewBillingSession(nil, buyerID, 300)
		require.NoError(t, err)
		assert.Equal(t, 50, buyerQuota(t, buyerID))

		require.NoError(t, s.Settle(500))
		final := buyerQuota(t, buyerID)
		assert.GreaterOrEqual(t, final, 0, "balance must never go negative")
		assert.Equal(t, 0, final, "only the remaining 50 could be deducted of the 200 delta")
		assert.False(t, s.NeedsRefund(), "session is settled for what could be deducted")
	})
}

// settleAtomic runs one SettleAtomic inside its own transaction, mirroring how
// the relay's settlement transaction will call it.
func settleAtomic(t *testing.T, db *gorm.DB, s *BillingSession, actual Micro) Micro {
	t.Helper()
	var charged Micro
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		charged, err = s.SettleAtomic(tx, actual)
		return err
	}))
	return charged
}

// 4b. SettleAtomic (H3): refund of the difference, covered top-up, shortfall
// capped at what was actually collectable, and idempotent replay.
func TestBillingSettleAtomic(t *testing.T) {
	t.Run("refund difference", func(t *testing.T) {
		db, buyerID := newBillingFixture(t, 1000)
		s, err := NewBillingSession(db, buyerID, 1000)
		require.NoError(t, err)
		assert.Equal(t, 0, buyerQuota(t, buyerID))

		assert.Equal(t, Micro(300), settleAtomic(t, db, s, 300))
		assert.Equal(t, 700, buyerQuota(t, buyerID))

		// Idempotent replay in a fresh transaction: same charge, no movement.
		assert.Equal(t, Micro(300), settleAtomic(t, db, s, 300))
		assert.Equal(t, 700, buyerQuota(t, buyerID))
	})

	t.Run("covered top-up", func(t *testing.T) {
		db, buyerID := newBillingFixture(t, 1000)
		s, err := NewBillingSession(db, buyerID, 300)
		require.NoError(t, err)
		assert.Equal(t, 700, buyerQuota(t, buyerID))

		assert.Equal(t, Micro(500), settleAtomic(t, db, s, 500))
		assert.Equal(t, 500, buyerQuota(t, buyerID), "300 frozen + 200 collected")
	})

	t.Run("shortfall charges only what was collectable", func(t *testing.T) {
		db, buyerID := newBillingFixture(t, 350)
		s, err := NewBillingSession(db, buyerID, 300)
		require.NoError(t, err)
		assert.Equal(t, 50, buyerQuota(t, buyerID))

		charged := settleAtomic(t, db, s, 500)
		assert.Equal(t, Micro(350), charged, "300 frozen + 50 drained; never more than actual")
		assert.Less(t, charged, Micro(500))
		assert.Equal(t, 0, buyerQuota(t, buyerID), "balance is drained to exactly zero, never negative")

		// Replay must not charge the remaining shortfall again.
		assert.Equal(t, Micro(350), settleAtomic(t, db, s, 500))
		assert.Equal(t, 0, buyerQuota(t, buyerID))
	})

	t.Run("refund-first wins", func(t *testing.T) {
		db, buyerID := newBillingFixture(t, 1000)
		s, err := NewBillingSession(db, buyerID, 400)
		require.NoError(t, err)
		s.Refund(nil)
		assert.Equal(t, 1000, buyerQuota(t, buyerID))

		assert.Equal(t, Micro(0), settleAtomic(t, db, s, 250),
			"after a refund the session is closed and charges nothing")
		assert.Equal(t, 1000, buyerQuota(t, buyerID))
	})

	t.Run("nil transaction and negative amount", func(t *testing.T) {
		db, buyerID := newBillingFixture(t, 1000)
		s, err := NewBillingSession(db, buyerID, 100)
		require.NoError(t, err)

		_, err = s.SettleAtomic(nil, 100)
		assert.Error(t, err)

		_, err = s.SettleAtomic(db, -1)
		assert.ErrorIs(t, err, ErrMoneyNegative)
	})
}

// 4c. SettleAtomic with 50 goroutines mixing Settle and Refund converges on
// exactly one of the two legal outcomes (run under -race).
func TestBillingSettleAtomicConcurrent(t *testing.T) {
	_, buyerID := newBillingFixture(t, 1000)

	s, err := NewBillingSession(nil, buyerID, 400)
	require.NoError(t, err)
	assert.Equal(t, 600, buyerQuota(t, buyerID))

	const goroutines = 50
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				assert.NoError(t, s.Settle(250))
			} else {
				s.Refund(nil)
			}
		}(i)
	}
	wg.Wait()

	final := buyerQuota(t, buyerID)
	assert.Contains(t, []int{750, 1000}, final,
		"settle-first refunds 150 (750); refund-first restores the pre-freeze (1000)")
	assert.False(t, s.NeedsRefund())
}

// 5. Refund returns the whole pre-freeze, is idempotent, and does not fire
// after Settle.
func TestBillingRefund(t *testing.T) {
	_, buyerID := newBillingFixture(t, 1000)

	s, err := NewBillingSession(nil, buyerID, 400)
	require.NoError(t, err)
	assert.Equal(t, 600, buyerQuota(t, buyerID))

	s.Refund(nil)
	assert.Equal(t, 1000, buyerQuota(t, buyerID))
	s.Refund(nil)
	assert.Equal(t, 1000, buyerQuota(t, buyerID), "second refund is a no-op")
	assert.False(t, s.NeedsRefund())

	s2, err := NewBillingSession(nil, buyerID, 400)
	require.NoError(t, err)
	require.NoError(t, s2.Settle(400))
	s2.Refund(nil)
	assert.Equal(t, 600, buyerQuota(t, buyerID), "refund after settle returns nothing")
}

// 6. Concurrent Settle and Refund on one session: exactly one of them takes
// effect, the rest are no-ops; the final balance is exactly one of the two
// legal outcomes. Run under -race.
func TestBillingSettleRefundConcurrent(t *testing.T) {
	_, buyerID := newBillingFixture(t, 1000)

	s, err := NewBillingSession(nil, buyerID, 400)
	require.NoError(t, err)
	assert.Equal(t, 600, buyerQuota(t, buyerID))

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { assert.NoError(t, s.Settle(250)) })
		wg.Go(func() { s.Refund(nil) })
	}
	wg.Wait()

	final := buyerQuota(t, buyerID)
	assert.Contains(t, []int{750, 1000}, final,
		"settle-first refunds 150 (final 750); refund-first returns 400 (final 1000); no other outcome is legal")
	assert.False(t, s.NeedsRefund())

	// Late calls keep converging on the same outcome.
	require.NoError(t, s.Settle(250))
	s.Refund(nil)
	assert.Equal(t, final, buyerQuota(t, buyerID))
}

// 7. Reserve tops the freeze up to the target; insufficient balance fails
// without moving quota.
func TestBillingReserve(t *testing.T) {
	_, buyerID := newBillingFixture(t, 1000)

	s, err := NewBillingSession(nil, buyerID, 100)
	require.NoError(t, err)
	assert.Equal(t, 900, buyerQuota(t, buyerID))

	require.NoError(t, s.Reserve(500))
	assert.Equal(t, 500, buyerQuota(t, buyerID))
	assert.Equal(t, 500, s.GetPreConsumedQuota())

	require.NoError(t, s.Reserve(300), "reserve below the current freeze is a no-op")
	assert.Equal(t, 500, buyerQuota(t, buyerID))

	err = s.Reserve(2000)
	assert.True(t, errors.Is(err, ErrInsufficientBalance))
	assert.Equal(t, 500, buyerQuota(t, buyerID), "failed reserve moves nothing")
	assert.Equal(t, 500, s.GetPreConsumedQuota())

	require.NoError(t, s.ReserveBeforeSubmit(600))
	assert.Equal(t, 400, buyerQuota(t, buyerID))
	assert.Equal(t, 600, s.GetPreConsumedQuota())
}

// 8. PreFreezeQuote = est input + maxTokens x output price + tool caps,
// through the same Quote function as the actual debit.
func TestPreFreezeQuote(t *testing.T) {
	db, cal := newQuoteFixture(t)

	// 2026-11-02 20:00 CST is a Monday outside the peak windows and after the
	// 2026-11-01 Search/Fetch switch-on (§3.4). deepseek-flash 空闲:
	// input 1 元/M = 1e6 微元/M, output 4 元/M = 4e6 微元/M; tools 5000 微元/次.
	at := shTime(2026, time.November, 2, 20, 0, 0)
	res, err := PreFreezeQuote(db, cal, SeedVendorDeepSeek, "deepseek-flash",
		300, 1_000_000, 500_000, 10, 10, at)
	require.NoError(t, err)

	// Σ tokens = 1e6×1e6 + 0.5e6×4e6 = 3e12
	// TokenDebit = ceil(3e12 × 300 / 1e9) = 900_000
	// ToolDebit  = ceil((10×5000 + 10×5000) × 300 / 1000) = 30_000
	assert.Equal(t, Micro(900_000), res.TokenDebit)
	assert.Equal(t, Micro(30_000), res.ToolDebit)
	assert.Equal(t, Micro(930_000), res.TotalDebit)
	assert.Equal(t, Micro(3_100_000), res.OfficialCost)
	assert.Equal(t, int64(1), res.ToolPriceVersions[ToolItemSearch])
	assert.Equal(t, int64(1), res.ToolPriceVersions[ToolItemFetch])

	// maxTokens == 0 contributes no output estimate (caller's duty to
	// substitute the model maximum); zero tool caps price no tools.
	res, err = PreFreezeQuote(db, cal, SeedVendorDeepSeek, "deepseek-flash",
		300, 1_000_000, 0, 0, 0, at)
	require.NoError(t, err)
	assert.Equal(t, Micro(300_000), res.TokenDebit)
	assert.Equal(t, Micro(0), res.ToolDebit)

	// Before the 2026-11-01 switch-on a positive tool cap has no price row:
	// the quote must fail with ErrNoPrice rather than bill a guess (§3.4).
	_, err = PreFreezeQuote(db, cal, SeedVendorDeepSeek, "deepseek-flash",
		300, 1_000, 1_000, 1, 0, shTime(2026, time.October, 21, 20, 0, 0))
	assert.True(t, errors.Is(err, ErrNoPrice))
}

// 9. ToolPreFreezeCap: default 10, MP_TOOL_PREFREEZE_CAP overrides when it is
// a positive integer.
func TestToolPreFreezeCap(t *testing.T) {
	assert.Equal(t, DefaultToolPreFreezeCap, ToolPreFreezeCap())
	assert.Equal(t, 10, DefaultToolPreFreezeCap)

	t.Setenv("MP_TOOL_PREFREEZE_CAP", "25")
	assert.Equal(t, 25, ToolPreFreezeCap())

	t.Setenv("MP_TOOL_PREFREEZE_CAP", "0")
	assert.Equal(t, DefaultToolPreFreezeCap, ToolPreFreezeCap())

	t.Setenv("MP_TOOL_PREFREEZE_CAP", "-3")
	assert.Equal(t, DefaultToolPreFreezeCap, ToolPreFreezeCap())

	t.Setenv("MP_TOOL_PREFREEZE_CAP", "abc")
	assert.Equal(t, DefaultToolPreFreezeCap, ToolPreFreezeCap())
}
