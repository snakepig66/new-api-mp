package marketplace

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"
)

// selectorTestT0 is a fixed instant the pure in-memory tests build on.
var selectorTestT0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func shanghaiLocation(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(shanghaiTZ)
	require.NoError(t, err)
	return loc
}

func TestSlotManager(t *testing.T) {
	t0 := selectorTestT0

	t.Run("acquire up to the limit then refuse without queueing", func(t *testing.T) {
		m := NewSlotManager()
		assert.True(t, m.Acquire(1, 2, t0))
		assert.True(t, m.Acquire(1, 2, t0))
		assert.Equal(t, 2, m.InFlight(1))
		// 满并发跳过这家，不排队 (§3.5): the third acquire fails immediately.
		assert.False(t, m.Acquire(1, 2, t0))
		assert.Equal(t, 2, m.InFlight(1))
		// A different listing is unaffected.
		assert.True(t, m.Acquire(2, 2, t0))
	})

	t.Run("release frees a slot", func(t *testing.T) {
		m := NewSlotManager()
		require.True(t, m.Acquire(1, 1, t0))
		assert.False(t, m.Acquire(1, 1, t0))
		m.Release(1)
		assert.Equal(t, 0, m.InFlight(1))
		assert.True(t, m.Acquire(1, 1, t0))
		// Releasing with nothing held is a no-op, never negative.
		m.Release(99)
		assert.Equal(t, 0, m.InFlight(99))
	})

	t.Run("slots expire after SlotTimeout", func(t *testing.T) {
		m := NewSlotManager()
		require.True(t, m.Acquire(1, 1, t0))
		assert.False(t, m.Acquire(1, 1, t0.Add(SlotTimeout-time.Second)),
			"a slot younger than ten minutes still counts")
		assert.True(t, m.Acquire(1, 1, t0.Add(SlotTimeout)),
			"at exactly SlotTimeout the stale slot is swept (§3.5 超时 10 分钟释放)")
		assert.Equal(t, 1, m.InFlight(1))
	})

	t.Run("renew extends a long-running slot past SlotTimeout", func(t *testing.T) {
		m := NewSlotManager()
		require.True(t, m.Acquire(1, 1, t0))
		renewed := t0.Add(9 * time.Minute)
		m.Renew(1, renewed)
		assert.Equal(t, 1, m.InFlight(1))
		assert.False(t, m.Acquire(1, 1, t0.Add(SlotTimeout)),
			"the renewed slot is younger than SlotTimeout and still counts")
		assert.True(t, m.Acquire(1, 1, renewed.Add(SlotTimeout)),
			"at SlotTimeout after the renewal the stale slot is swept")
		// Renewing a listing with nothing held is a no-op.
		m.Renew(99, t0)
		assert.Equal(t, 0, m.InFlight(99))
	})

	t.Run("limit zero falls back to the §3.5 default of five", func(t *testing.T) {
		m := NewSlotManager()
		for range 5 {
			require.True(t, m.Acquire(1, 0, t0))
		}
		assert.False(t, m.Acquire(1, 0, t0))
	})

	t.Run("stats snapshot counts slot lifecycle events", func(t *testing.T) {
		m := NewSlotManager()
		require.True(t, m.Acquire(1, 1, t0))
		assert.False(t, m.Acquire(1, 1, t0), "a refused acquire is not counted")
		require.True(t, m.Acquire(2, 1, t0))
		m.Renew(1, t0.Add(time.Second))
		m.Renew(3, t0) // nothing held: renew failure
		m.Release(1)
		m.Release(3) // nothing held: not a release

		assert.Equal(t, map[string]int64{
			"acquire":       2,
			"renew":         1,
			"renew_failure": 1,
			"release":       1,
			"in_flight":     1,
		}, m.Stats())
	})
}

func TestCircuitBreaker(t *testing.T) {
	t0 := selectorTestT0

	t.Run("a 429 breaks the listing for sixty seconds", func(t *testing.T) {
		b := NewCircuitBreaker()
		assert.True(t, b.Allow(1, t0))
		b.Record429(1, t0)
		assert.False(t, b.Allow(1, t0))
		assert.False(t, b.Allow(1, t0.Add(CircuitBreakDuration-time.Second)))
		// Other listings are unaffected — the break is per listing (§3.5).
		assert.True(t, b.Allow(2, t0))
	})

	t.Run("past the break a single half-open probe is admitted", func(t *testing.T) {
		b := NewCircuitBreaker()
		b.Record429(1, t0)
		t1 := t0.Add(CircuitBreakDuration)
		assert.True(t, b.Allow(1, t1), "the first probe after 60s is admitted")
		assert.False(t, b.Allow(1, t1), "a concurrent second probe is refused")
	})

	t.Run("a successful half-open probe closes the circuit", func(t *testing.T) {
		b := NewCircuitBreaker()
		b.Record429(1, t0)
		t1 := t0.Add(CircuitBreakDuration)
		require.True(t, b.Allow(1, t1))
		b.RecordSuccess(1)
		assert.True(t, b.Allow(1, t1))
		assert.True(t, b.Allow(1, t1), "a closed circuit has no probe limit")
	})

	t.Run("a 429 during half-open re-breaks for a full duration", func(t *testing.T) {
		b := NewCircuitBreaker()
		b.Record429(1, t0)
		t1 := t0.Add(CircuitBreakDuration)
		require.True(t, b.Allow(1, t1))
		b.Record429(1, t1)
		assert.False(t, b.Allow(1, t1.Add(CircuitBreakDuration-time.Second)))
		assert.True(t, b.Allow(1, t1.Add(CircuitBreakDuration)))
	})
}

func TestRPMWindow(t *testing.T) {
	t0 := selectorTestT0

	t.Run("the rpm+1-th attempt inside the window is refused", func(t *testing.T) {
		w := NewRPMWindow()
		assert.True(t, w.Allow(1, 2, t0))
		assert.True(t, w.Allow(1, 2, t0.Add(30*time.Second)))
		assert.False(t, w.Allow(1, 2, t0.Add(59*time.Second)))
	})

	t.Run("attempts slide out of the window", func(t *testing.T) {
		w := NewRPMWindow()
		require.True(t, w.Allow(1, 1, t0))
		assert.False(t, w.Allow(1, 1, t0.Add(59*time.Second)))
		assert.True(t, w.Allow(1, 1, t0.Add(61*time.Second)),
			"once the oldest attempt is 60s+ old the window has room again")
	})

	t.Run("zero rpm means unlimited", func(t *testing.T) {
		w := NewRPMWindow()
		for range 100 {
			assert.True(t, w.Allow(1, 0, t0))
		}
	})
}

func TestDailySpendMicro(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	loc := shanghaiLocation(t)
	// All timestamps stay in Asia/Shanghai so every dialect (and the sqlite
	// test driver's offset-bearing text format) compares them consistently.
	dayStart := time.Date(2026, 10, 23, 0, 0, 0, 0, loc)

	addLedger := func(requestID, entryType string, listingID int64, amount int64, at time.Time) {
		t.Helper()
		require.NoError(t, db.Create(&LedgerEntry{
			RequestID:   requestID,
			EntryType:   entryType,
			Account:     UserAccount(10011),
			ListingID:   listingID,
			AmountMicro: amount,
			Status:      LedgerAvailable,
			CreatedAt:   at,
		}).Error)
	}

	// Today (Asia/Shanghai): the 00:01 buyer_debit and the 23:59 tool_debit
	// count, by absolute value.
	addLedger("r1", EntryBuyerDebit, 7, -100, dayStart.Add(time.Minute))
	addLedger("r2", EntryToolDebit, 7, -50, dayStart.Add(23*time.Hour+59*time.Minute))
	// Yesterday 23:59 and tomorrow 00:00 do not.
	addLedger("r3", EntryBuyerDebit, 7, -999, dayStart.Add(-time.Minute))
	addLedger("r4", EntryBuyerDebit, 7, -888, dayStart.AddDate(0, 0, 1))
	// Other listings and other entry types do not.
	addLedger("r5", EntryBuyerDebit, 8, -777, dayStart.Add(2*time.Hour))
	addLedger("r6", EntrySellerCredit, 7, 666, dayStart.Add(3*time.Hour))

	spent, err := ListingDailySpendMicro(db, 7, dayStart.Add(12*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(150), spent,
		"only today's buyer_debit + tool_debit absolute values count (§3.5 daily_cap_micro)")

	// 23:59:59 still belongs to today; one minute past midnight the window
	// has rolled over to tomorrow.
	spent, err = ListingDailySpendMicro(db, 7, dayStart.AddDate(0, 0, 1).Add(-time.Second))
	require.NoError(t, err)
	assert.Equal(t, int64(150), spent)
	spent, err = ListingDailySpendMicro(db, 7, dayStart.AddDate(0, 0, 1).Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(888), spent)
}

func TestWithinHours(t *testing.T) {
	loc := shanghaiLocation(t)
	at := func(hour, minute int) time.Time {
		return time.Date(2026, 10, 23, hour, minute, 0, 0, loc)
	}

	t.Run("empty hours means all day", func(t *testing.T) {
		assert.True(t, WithinHours("", at(0, 0)))
		assert.True(t, WithinHours("   ", at(23, 59)))
	})

	t.Run("inside and outside a single window", func(t *testing.T) {
		hours := `[{"start":"09:00","end":"18:00"}]`
		assert.False(t, WithinHours(hours, at(8, 59)))
		assert.True(t, WithinHours(hours, at(9, 0)), "the window start is inclusive")
		assert.True(t, WithinHours(hours, at(17, 59)))
		assert.False(t, WithinHours(hours, at(18, 0)), "the window end is exclusive")
	})

	t.Run("any window matches", func(t *testing.T) {
		hours := `[{"start":"09:00","end":"12:00"},{"start":"14:00","end":"18:00"}]`
		assert.False(t, WithinHours(hours, at(13, 0)))
		assert.True(t, WithinHours(hours, at(14, 0)))
	})

	t.Run("cross-midnight windows are not supported", func(t *testing.T) {
		// ValidateHours rejects start >= end at write time (listing.go), so a
		// 22:00-02:00 window can never be stored; WithinHours also fails
		// closed on it. This test locks that semantic.
		hours := `[{"start":"22:00","end":"02:00"}]`
		require.Error(t, ValidateHours(hours))
		assert.False(t, WithinHours(hours, at(23, 0)))
		assert.False(t, WithinHours(hours, at(1, 0)))
	})

	t.Run("unparseable hours fail closed", func(t *testing.T) {
		assert.False(t, WithinHours("not-json", at(12, 0)))
		assert.False(t, WithinHours(`[{"start":"9:00","end":"18:00"}]`, at(12, 0)))
	})

	t.Run("windows are read in Asia/Shanghai", func(t *testing.T) {
		hours := `[{"start":"09:00","end":"18:00"}]`
		// 2026-10-23 08:00 UTC is 16:00 in Shanghai — inside.
		assert.True(t, WithinHours(hours, time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)))
		// 2026-10-22 23:00 UTC is 2026-10-23 07:00 in Shanghai — outside.
		assert.False(t, WithinHours(hours, time.Date(2026, 10, 22, 23, 0, 0, 0, time.UTC)))
	})
}

// addSelectorListing inserts one listing row directly: the selector only
// reads mp_listings, so no channel row or key material is needed. fp must be
// unique within the test database (key_fingerprint has a unique index).
func addSelectorListing(t *testing.T, db *gorm.DB, fp string, mutate func(*Listing)) Listing {
	t.Helper()
	l := Listing{
		SellerID:         10023,
		ChannelID:        1,
		Vendor:           SeedVendorDeepSeek,
		Models:           []string{"deepseek-flash"},
		MultiplierMilli:  300,
		ConcurrencyLimit: 5,
		Status:           ListingLive,
		KeyFingerprint:   fp,
		Version:          1,
	}
	if mutate != nil {
		mutate(&l)
	}
	require.NoError(t, db.Create(&l).Error)
	return l
}

func TestSelect(t *testing.T) {
	loc := shanghaiLocation(t)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, loc)

	// newSelector builds a Selector with a deterministic canary dice.
	newSelector := func() *Selector {
		s := NewSelector()
		s.Rand = rand.New(rand.NewSource(1))
		return s
	}

	t.Run("a single live listing is picked and holds its slot", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		a := addSelectorListing(t, db, "fp-a", nil)
		s := newSelector()
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, a.ID, got.ID)
		assert.Equal(t, 1, s.Slots.InFlight(a.ID),
			"the winner holds its concurrency slot until the caller releases it (§3.5)")
	})

	t.Run("listings not serving the model are filtered", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		addSelectorListing(t, db, "fp-a", nil)
		s := newSelector()
		_, err := s.Select(db, SelectInput{Model: "deepseek-v4-pro", Now: now})
		assert.ErrorIs(t, err, ErrNoListingAvailable)
	})

	t.Run("the exclude list switches to the next listing", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		a := addSelectorListing(t, db, "fp-a", func(l *Listing) { l.MultiplierMilli = 200 })
		b := addSelectorListing(t, db, "fp-b", func(l *Listing) { l.MultiplierMilli = 400 })
		s := newSelector()
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now, ExcludeListingIDs: []int64{a.ID}})
		require.NoError(t, err)
		assert.Equal(t, b.ID, got.ID, "重试换家排除已试过的 (§3.7)")
		assert.Equal(t, 0, s.Slots.InFlight(a.ID))
	})

	t.Run("a buyer never gets their own listing", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		own := addSelectorListing(t, db, "fp-a", func(l *Listing) {
			l.SellerID = 42
			l.MultiplierMilli = 100 // cheapest, so only the buyer check can skip it
		})
		other := addSelectorListing(t, db, "fp-b", func(l *Listing) {
			l.SellerID = 43
			l.MultiplierMilli = 400
		})
		s := newSelector()
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now, BuyerID: 42})
		require.NoError(t, err)
		assert.Equal(t, other.ID, got.ID, "buying your own supply would launder gifted balance into withdrawals")
		assert.Equal(t, 0, s.Slots.InFlight(own.ID))

		_, err = s.Select(db, SelectInput{Model: "deepseek-flash", Now: now, BuyerID: 42,
			ExcludeListingIDs: []int64{other.ID}})
		assert.ErrorIs(t, err, ErrNoListingAvailable)
	})

	t.Run("excluding everything yields ErrNoListingAvailable", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		a := addSelectorListing(t, db, "fp-a", nil)
		s := newSelector()
		_, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now, ExcludeListingIDs: []int64{a.ID}})
		assert.ErrorIs(t, err, ErrNoListingAvailable)
	})

	t.Run("canary is never admitted at fraction zero", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		addSelectorListing(t, db, "fp-a", func(l *Listing) { l.Status = ListingCanary })
		s := newSelector()
		s.CanaryFraction = 0
		_, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		assert.ErrorIs(t, err, ErrNoListingAvailable)
	})

	t.Run("canary is admitted at fraction one", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		c := addSelectorListing(t, db, "fp-a", func(l *Listing) { l.Status = ListingCanary })
		s := newSelector()
		s.CanaryFraction = 1
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, c.ID, got.ID)
	})

	t.Run("listings above the buyer price cap are skipped", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		addSelectorListing(t, db, "fp-a", func(l *Listing) { l.MultiplierMilli = 500 })
		s := newSelector()
		_, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now, MaxMultiplierMilli: 300})
		assert.ErrorIs(t, err, ErrNoListingAvailable,
			"倍率 500 exceeds the 300 cap (§3.6 价格上限约束兜底换家)")
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now, MaxMultiplierMilli: 500})
		require.NoError(t, err)
		assert.Equal(t, int64(500), got.MultiplierMilli, "a cap equal to the multiplier admits it")
	})

	t.Run("the buyer cap uses multiplier × channel coefficient", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		// Coefficient 0.5 halves the effective price: 900 × 500 / 1000 = 450.
		require.NoError(t, db.Model(&ChannelModel{}).
			Where("channel_slug = ? AND model = ?", SeedVendorDeepSeek, "deepseek-flash").
			Update("coefficient_milli", 500).Error)
		a := addSelectorListing(t, db, "fp-a", func(l *Listing) { l.MultiplierMilli = 900 })
		s := newSelector()
		got, err := s.Select(db, SelectInput{
			Model: "deepseek-flash", Now: now, MaxMultiplierMilli: 600,
		})
		require.NoError(t, err, "effective 450 is below the 600 cap")
		assert.Equal(t, a.ID, got.ID)

		// The same effective price is enforced by ReselectChannel, which goes
		// through SelectWithProbe; a cap below 450 excludes it.
		_, _, err = s.SelectWithProbe(db, SelectInput{
			Model: "deepseek-flash", Now: now, MaxMultiplierMilli: 449,
		})
		assert.ErrorIs(t, err, ErrNoListingAvailable)
	})

	t.Run("a coefficient above 1000 is rejected", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		require.NoError(t, db.Model(&ChannelModel{}).
			Where("channel_slug = ? AND model = ?", SeedVendorDeepSeek, "deepseek-flash").
			Update("coefficient_milli", 2000).Error)
		addSelectorListing(t, db, "fp-a", func(l *Listing) { l.MultiplierMilli = 100 })
		s := newSelector()
		_, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		assert.ErrorIs(t, err, ErrNoListingAvailable,
			"a mark-up above official price must never be selectable")
	})

	t.Run("a coefficient of 1000 is the default official price", func(t *testing.T) {
		assert.Equal(t, int64(900), effectiveMultiplierMilli(900, 1000))
		assert.Equal(t, int64(450), effectiveMultiplierMilli(900, 500))
		assert.Equal(t, int64(901), effectiveMultiplierMilli(900, 1001),
			"ceil rounds fractional milli up (1001 here just exercises the math)")
		assert.Equal(t, int64(900), effectiveMultiplierMilli(900, 0),
			"a missing/zero coefficient means 1000")
	})

	t.Run("the lower multiplier wins", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		a := addSelectorListing(t, db, "fp-a", func(l *Listing) { l.MultiplierMilli = 500 })
		b := addSelectorListing(t, db, "fp-b", func(l *Listing) { l.MultiplierMilli = 200 })
		s := newSelector()
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, b.ID, got.ID)
		assert.Equal(t, 0, s.Slots.InFlight(a.ID), "the loser hands its slot back")
		assert.Equal(t, 1, s.Slots.InFlight(b.ID))
	})

	t.Run("equal multiplier: the lowest latest probe latency wins", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		a := addSelectorListing(t, db, "fp-a", nil)
		b := addSelectorListing(t, db, "fp-b", nil)
		// A has an old fast probe and a newer slow one; only the latest counts.
		require.NoError(t, db.Create(&Probe{ListingID: a.ID, Type: ProbeTypeVerify, OK: true, LatencyMs: 10, CreatedAt: now.Add(-time.Hour)}).Error)
		require.NoError(t, db.Create(&Probe{ListingID: a.ID, Type: ProbeTypePatrol, OK: true, LatencyMs: 100, CreatedAt: now}).Error)
		require.NoError(t, db.Create(&Probe{ListingID: b.ID, Type: ProbeTypeVerify, OK: true, LatencyMs: 50, CreatedAt: now.Add(-time.Hour)}).Error)
		s := newSelector()
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, b.ID, got.ID, "A's latest probe (100ms) loses to B's (50ms)")
	})

	t.Run("equal multiplier: a probed listing outranks an unprobed one", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		addSelectorListing(t, db, "fp-a", nil) // no probe; the lower id would win the tie-break
		b := addSelectorListing(t, db, "fp-b", nil)
		require.NoError(t, db.Create(&Probe{ListingID: b.ID, Type: ProbeTypeVerify, OK: true, LatencyMs: 900, CreatedAt: now}).Error)
		s := newSelector()
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, b.ID, got.ID, "no probe ranks last, even behind a slow probe")
	})

	t.Run("a listing with full concurrency is skipped", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		a := addSelectorListing(t, db, "fp-a", func(l *Listing) {
			l.MultiplierMilli = 100 // cheapest — would win if it had a free slot
			l.ConcurrencyLimit = 1
		})
		b := addSelectorListing(t, db, "fp-b", func(l *Listing) { l.MultiplierMilli = 400 })
		s := newSelector()
		require.True(t, s.Slots.Acquire(a.ID, a.ConcurrencyLimit, now), "occupy A's only slot")
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, b.ID, got.ID)
	})

	t.Run("a broken circuit is skipped", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		a := addSelectorListing(t, db, "fp-a", func(l *Listing) { l.MultiplierMilli = 100 })
		b := addSelectorListing(t, db, "fp-b", func(l *Listing) { l.MultiplierMilli = 400 })
		s := newSelector()
		s.Breaker.Record429(a.ID, now)
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, b.ID, got.ID)
		s.Breaker.Record429(b.ID, now)
		_, err = s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		assert.ErrorIs(t, err, ErrNoListingAvailable, "with every candidate broken the request fails")
	})

	t.Run("a listing outside its hours is skipped", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		a := addSelectorListing(t, db, "fp-a", func(l *Listing) {
			l.MultiplierMilli = 100
			l.Hours = `[{"start":"09:00","end":"18:00"}]`
		})
		b := addSelectorListing(t, db, "fp-b", func(l *Listing) { l.MultiplierMilli = 400 })
		s := newSelector()
		evening := time.Date(2026, 10, 23, 20, 0, 0, 0, loc)
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: evening})
		require.NoError(t, err)
		assert.Equal(t, b.ID, got.ID)
		// Inside the window A wins again.
		s2 := newSelector()
		got, err = s2.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, a.ID, got.ID)
	})

	t.Run("a listing at its daily cap is skipped", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		a := addSelectorListing(t, db, "fp-a", func(l *Listing) {
			l.MultiplierMilli = 100
			l.DailyCapMicro = 1000
		})
		b := addSelectorListing(t, db, "fp-b", func(l *Listing) { l.MultiplierMilli = 400 })
		dayStart := time.Date(2026, 10, 23, 0, 0, 0, 0, loc)
		require.NoError(t, db.Create(&LedgerEntry{
			RequestID: "cap-1", EntryType: EntryBuyerDebit, Account: UserAccount(10011),
			ListingID: a.ID, AmountMicro: -600, Status: LedgerAvailable, CreatedAt: dayStart.Add(time.Hour),
		}).Error)
		require.NoError(t, db.Create(&LedgerEntry{
			RequestID: "cap-2", EntryType: EntryToolDebit, Account: UserAccount(10011),
			ListingID: a.ID, AmountMicro: -400, Status: LedgerAvailable, CreatedAt: dayStart.Add(2 * time.Hour),
		}).Error)
		s := newSelector()
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, b.ID, got.ID, "600 + 400 reaches A's 1000 daily cap, so A is skipped")
	})

	t.Run("a listing out of rpm budget is skipped", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		a := addSelectorListing(t, db, "fp-a", func(l *Listing) {
			l.MultiplierMilli = 100
			l.RPMLimit = 1
		})
		b := addSelectorListing(t, db, "fp-b", func(l *Listing) { l.MultiplierMilli = 400 })
		s := newSelector()
		require.True(t, s.RPM.Allow(a.ID, a.RPMLimit, now), "spend A's single rpm slot")
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, b.ID, got.ID)
	})

	t.Run("only the winner consumes rpm budget", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		a := addSelectorListing(t, db, "fp-a", func(l *Listing) {
			l.MultiplierMilli = 100
			l.RPMLimit = 1
		})
		b := addSelectorListing(t, db, "fp-b", func(l *Listing) {
			l.MultiplierMilli = 400
			l.RPMLimit = 1
		})
		s := newSelector()
		first, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, a.ID, first.ID)
		s.Slots.Release(first.ID)
		// A's only rpm slot is spent; B's must still be intact because B lost the
		// first round without serving it (M7).
		second, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err, "the loser must not have consumed its rpm budget")
		assert.Equal(t, b.ID, second.ID)
		s.Slots.Release(second.ID)
	})

	t.Run("draft and paused listings never serve traffic", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		addSelectorListing(t, db, "fp-a", func(l *Listing) { l.Status = ListingDraft })
		addSelectorListing(t, db, "fp-b", func(l *Listing) { l.Status = ListingPaused })
		s := newSelector()
		_, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		assert.ErrorIs(t, err, ErrNoListingAvailable)
	})
}

func TestAutoPauseListing(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	listing := addSelectorListing(t, db, "fp-a", nil)

	require.NoError(t, AutoPauseListing(db, listing.ID, "upstream reported insufficient balance"))

	got, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPaused, got.Status, "auto-pause moves live -> paused (§3.5)")

	var probes []Probe
	require.NoError(t, db.Where("listing_id = ?", listing.ID).Find(&probes).Error)
	require.Len(t, probes, 1)
	assert.Equal(t, ProbeTypePatrol, probes[0].Type)
	assert.False(t, probes[0].OK)
	assert.Contains(t, probes[0].Detail, "insufficient balance",
		"the probe detail records the semantic reason")

	err = AutoPauseListing(db, listing.ID, "upstream rejected the key")
	assert.ErrorIs(t, err, ErrInvalidTransition, "an already paused listing cannot be auto-paused again")

	assert.ErrorIs(t, AutoPauseListing(db, 99999, "upstream rejected the key"), ErrListingNotFound)
}

// addEndpointChannel inserts one active channel pool row and its verified
// deepseek-flash qualification, so a listing on that vendor is selectable.
func addEndpointChannel(t *testing.T, db *gorm.DB, slug, protocols string) {
	t.Helper()
	require.NoError(t, db.Create(&ChannelPool{
		Slug: slug, Name: slug, Kind: ChannelKindOfficial,
		Protocols: protocols, Status: PoolStatusActive,
	}).Error)
	require.NoError(t, db.Create(&ChannelModel{
		ChannelSlug: slug, UpstreamModel: "deepseek-flash", Model: "deepseek-flash",
		CoefficientMilli: 1000, Verified: true, Status: PoolStatusActive,
	}).Error)
}

func TestSelectEndpointKind(t *testing.T) {
	loc := shanghaiLocation(t)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, loc)

	t.Run("chat never picks a messages-only channel", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		addEndpointChannel(t, db, "messages-only", `{"anthropic":"https://api.example.com/anthropic"}`)
		chat := addSelectorListing(t, db, "fp-chat", func(l *Listing) { l.MultiplierMilli = 400 })
		messages := addSelectorListing(t, db, "fp-messages", func(l *Listing) {
			l.Vendor = "messages-only"
			l.MultiplierMilli = 100 // cheapest, so only the protocol filter keeps it out
		})

		got, err := NewSelector().Select(db, SelectInput{
			Model: "deepseek-flash", Now: now, EndpointKind: relayconstant.EndpointOpenAIChat,
		})
		require.NoError(t, err)
		assert.Equal(t, chat.ID, got.ID, "a chat request must not land on a messages-only channel")

		got, err = NewSelector().Select(db, SelectInput{
			Model: "deepseek-flash", Now: now, EndpointKind: relayconstant.EndpointAnthropicMessages,
		})
		require.NoError(t, err)
		assert.Equal(t, messages.ID, got.ID)
	})

	t.Run("legacy openai-only channel serves chat and responses but not messages", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		addEndpointChannel(t, db, "legacy-openai", `{"openai":"https://api.example.com"}`)
		l := addSelectorListing(t, db, "fp-legacy", func(l *Listing) { l.Vendor = "legacy-openai" })

		for _, kind := range []relayconstant.EndpointKind{
			relayconstant.EndpointOpenAIChat,
			relayconstant.EndpointOpenAIResponses,
		} {
			got, err := NewSelector().Select(db, SelectInput{
				Model: "deepseek-flash", Now: now, EndpointKind: kind,
			})
			require.NoError(t, err, "the legacy openai base covers %s", kind)
			assert.Equal(t, l.ID, got.ID)
		}
		_, err := NewSelector().Select(db, SelectInput{
			Model: "deepseek-flash", Now: now, EndpointKind: relayconstant.EndpointAnthropicMessages,
		})
		assert.ErrorIs(t, err, ErrNoListingAvailable)
	})

	t.Run("a channel with no endpoint data stays selectable", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		addEndpointChannel(t, db, "legacy-empty", `{}`)
		l := addSelectorListing(t, db, "fp-empty", func(l *Listing) { l.Vendor = "legacy-empty" })
		got, err := NewSelector().Select(db, SelectInput{
			Model: "deepseek-flash", Now: now, EndpointKind: relayconstant.EndpointAnthropicMessages,
		})
		require.NoError(t, err, "rows written before the endpoint split are not silently withdrawn")
		assert.Equal(t, l.ID, got.ID)
	})
}

func TestHasLiveListingForEndpoint(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	addEndpointChannel(t, db, "messages-only", `{"anthropic":"https://api.example.com/anthropic"}`)
	addSelectorListing(t, db, "fp-live", func(l *Listing) { l.Vendor = "messages-only" })

	has, err := hasLiveListingForModel(db, "deepseek-flash")
	require.NoError(t, err)
	assert.True(t, has, "without an endpoint restriction any live qualification counts")

	has, err = hasLiveListingForModel(db, "deepseek-flash", relayconstant.EndpointOpenAIChat)
	require.NoError(t, err)
	assert.False(t, has, "a messages-only channel cannot serve a chat request")

	has, err = hasLiveListingForModel(db, "deepseek-flash", relayconstant.EndpointAnthropicMessages)
	require.NoError(t, err)
	assert.True(t, has)
}

// TestSelectAffinityPreference locks P4-A's promise: the bound listing ranks
// first, but only after it passes every filter, and a full concurrency limit
// falls through to another supplier.
func TestSelectAffinityPreference(t *testing.T) {
	loc := shanghaiLocation(t)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, loc)
	newSelector := func() *Selector {
		s := NewSelector()
		s.Rand = rand.New(rand.NewSource(1))
		return s
	}

	t.Run("the preferred listing outranks a cheaper one", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		cheap := addSelectorListing(t, db, "fp-cheap", func(l *Listing) { l.MultiplierMilli = 200 })
		preferred := addSelectorListing(t, db, "fp-preferred", func(l *Listing) { l.MultiplierMilli = 800 })
		s := newSelector()
		got, err := s.Select(db, SelectInput{
			Model: "deepseek-flash", Now: now, PreferredListingID: preferred.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, preferred.ID, got.ID)
		assert.Equal(t, 0, s.Slots.InFlight(cheap.ID), "the cheaper loser hands its slot back")
	})

	t.Run("the price cap still filters the preferred listing", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		cheap := addSelectorListing(t, db, "fp-cheap", func(l *Listing) { l.MultiplierMilli = 200 })
		preferred := addSelectorListing(t, db, "fp-preferred", func(l *Listing) { l.MultiplierMilli = 800 })
		got, err := newSelector().Select(db, SelectInput{
			Model: "deepseek-flash", Now: now, MaxMultiplierMilli: 500,
			PreferredListingID: preferred.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, cheap.ID, got.ID, "an over-cap preferred listing is not selectable")
	})

	t.Run("the preferred listing never overrides the self-purchase block", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		other := addSelectorListing(t, db, "fp-other", func(l *Listing) {
			l.SellerID = 43
			l.MultiplierMilli = 400
		})
		own := addSelectorListing(t, db, "fp-own", func(l *Listing) {
			l.SellerID = 42
			l.MultiplierMilli = 100
		})
		got, err := newSelector().Select(db, SelectInput{
			Model: "deepseek-flash", Now: now, BuyerID: 42, PreferredListingID: own.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, other.ID, got.ID)
	})

	t.Run("the preferred listing still obeys its business hours", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		available := addSelectorListing(t, db, "fp-available", func(l *Listing) { l.MultiplierMilli = 400 })
		preferred := addSelectorListing(t, db, "fp-preferred", func(l *Listing) {
			l.MultiplierMilli = 100
			l.Hours = `[{"start":"09:00","end":"10:00"}]`
		})
		got, err := newSelector().Select(db, SelectInput{
			Model: "deepseek-flash", Now: now, PreferredListingID: preferred.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, available.ID, got.ID, "a closed listing is skipped even when preferred")
	})

	t.Run("the preferred listing still needs a compatible endpoint", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		addEndpointChannel(t, db, "affinity-messages-only", `{"anthropic":"https://api.example.com/anthropic"}`)
		chat := addSelectorListing(t, db, "fp-chat", func(l *Listing) { l.MultiplierMilli = 400 })
		preferred := addSelectorListing(t, db, "fp-messages", func(l *Listing) {
			l.Vendor = "affinity-messages-only"
			l.MultiplierMilli = 100
		})
		got, err := newSelector().Select(db, SelectInput{
			Model: "deepseek-flash", Now: now, EndpointKind: relayconstant.EndpointOpenAIChat,
			PreferredListingID: preferred.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, chat.ID, got.ID, "a messages-only preferred listing cannot serve chat")
	})

	t.Run("official-only routing still filters the preferred listing", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		official := addSelectorListing(t, db, "fp-official", func(l *Listing) { l.MultiplierMilli = 400 })
		require.NoError(t, db.Create(&ChannelPool{
			Slug: "affinity-plan", Name: "affinity-plan", Kind: ChannelKindPlan,
			Protocols: `{"openai":"https://plan.example.com"}`, Status: PoolStatusActive,
		}).Error)
		require.NoError(t, db.Create(&ChannelModel{
			ChannelSlug: "affinity-plan", UpstreamModel: "deepseek-flash", Model: "deepseek-flash",
			CoefficientMilli: 1000, Verified: true, Status: PoolStatusActive,
		}).Error)
		preferred := addSelectorListing(t, db, "fp-plan", func(l *Listing) {
			l.Vendor = "affinity-plan"
			l.MultiplierMilli = 100
		})
		got, err := newSelector().Select(db, SelectInput{
			Model: "deepseek-flash", Now: now, OfficialOnly: true,
			PreferredListingID: preferred.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, official.ID, got.ID, "只用官方 never lands on a plan channel")
	})

	t.Run("full concurrency dispatches elsewhere without failing", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		preferred := addSelectorListing(t, db, "fp-preferred", func(l *Listing) {
			l.MultiplierMilli = 100
			l.ConcurrencyLimit = 1
		})
		other := addSelectorListing(t, db, "fp-other", func(l *Listing) { l.MultiplierMilli = 400 })
		s := newSelector()
		require.True(t, s.Slots.Acquire(preferred.ID, preferred.ConcurrencyLimit, now))
		got, err := s.Select(db, SelectInput{
			Model: "deepseek-flash", Now: now, PreferredListingID: preferred.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, other.ID, got.ID, "a full preferred listing is replaced for this request")
	})
}

func TestSelectTieBreakSpreadsLoad(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	a := addSelectorListing(t, db, "fp-a", nil)
	b := addSelectorListing(t, db, "fp-b", nil)

	s := NewSelector()
	s.Rand = rand.New(rand.NewSource(7))
	counts := map[int64]int{}
	for range 200 {
		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: selectorTestT0})
		require.NoError(t, err)
		counts[got.ID]++
		s.Slots.Release(got.ID)
	}
	assert.Greater(t, counts[a.ID], 40, "an equally priced listing must receive a fair share")
	assert.Greater(t, counts[b.ID], 40, "the final tie is a draw, not lowest-id-wins")
}

// addSelectorGroup inserts one P4 listing group for the selector fixtures.
func addSelectorGroup(t *testing.T, db *gorm.DB, mutate func(*ListingGroup)) ListingGroup {
	t.Helper()
	g := ListingGroup{
		SellerID:        10023,
		VendorSlug:      SeedVendorDeepSeek,
		KeyStrategy:     KeyStrategyWeightedRoundRobin,
		AffinityEnabled: true,
	}
	if mutate != nil {
		mutate(&g)
	}
	require.NoError(t, db.Create(&g).Error)
	return g
}

// TestSelectGroupRotation covers the in-group key choice: weighted round robin
// alternates the keys, primary_backup keeps the lowest id first.
func TestSelectGroupRotation(t *testing.T) {
	loc := shanghaiLocation(t)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, loc)

	t.Run("weighted round robin alternates two equal keys", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		group := addSelectorGroup(t, db, nil)
		a := addSelectorListing(t, db, "fp-g-a", func(l *Listing) { l.GroupID = group.ID })
		b := addSelectorListing(t, db, "fp-g-b", func(l *Listing) { l.GroupID = group.ID })
		s := NewSelector()
		s.Rand = rand.New(rand.NewSource(1))

		first, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		s.Slots.Release(first.ID)
		second, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		s.Slots.Release(second.ID)

		assert.ElementsMatch(t, []int64{a.ID, b.ID}, []int64{first.ID, second.ID})
		assert.NotEqual(t, first.ID, second.ID, "the second request rotates to the other key")
	})

	t.Run("primary backup prefers the lowest listing id", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		group := addSelectorGroup(t, db, func(g *ListingGroup) { g.KeyStrategy = KeyStrategyPrimaryBackup })
		a := addSelectorListing(t, db, "fp-g-pa", func(l *Listing) { l.GroupID = group.ID })
		b := addSelectorListing(t, db, "fp-g-pb", func(l *Listing) { l.GroupID = group.ID })
		s := NewSelector()
		s.Rand = rand.New(rand.NewSource(1))

		got, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, a.ID, got.ID)
		s.Slots.Release(got.ID)

		// Excluding the primary falls through to the backup.
		got, err = s.Select(db, SelectInput{Model: "deepseek-flash", Now: now, ExcludeListingIDs: []int64{a.ID}})
		require.NoError(t, err)
		assert.Equal(t, b.ID, got.ID)
		s.Slots.Release(got.ID)
	})
}

// TestSelectGroupLimits covers the aggregate channel limits: concurrency, RPM,
// daily cap and cumulative cap. A limit is shared by every key of the group.
func TestSelectGroupLimits(t *testing.T) {
	loc := shanghaiLocation(t)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, loc)

	t.Run("group concurrency is shared by the keys", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		group := addSelectorGroup(t, db, func(g *ListingGroup) { g.ChannelConcurrencyLimit = 1 })
		addSelectorListing(t, db, "fp-gc-a", func(l *Listing) { l.GroupID = group.ID })
		addSelectorListing(t, db, "fp-gc-b", func(l *Listing) { l.GroupID = group.ID })
		s := NewSelector()
		s.Rand = rand.New(rand.NewSource(1))

		first, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		assert.Equal(t, 1, s.GroupSlots.InFlight(group.ID))
		_, err = s.Select(db, SelectInput{Model: "deepseek-flash", Now: now, ExcludeListingIDs: []int64{first.ID}})
		assert.ErrorIs(t, err, ErrNoListingAvailable, "the other key shares the full group slot budget")
		s.Slots.Release(first.ID)
		s.GroupSlots.Release(group.ID)

		_, err = s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
	})

	t.Run("group rpm is shared by the keys", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		group := addSelectorGroup(t, db, func(g *ListingGroup) { g.ChannelRPMLimit = 1 })
		addSelectorListing(t, db, "fp-gr-a", func(l *Listing) { l.GroupID = group.ID })
		addSelectorListing(t, db, "fp-gr-b", func(l *Listing) { l.GroupID = group.ID })
		s := NewSelector()
		s.Rand = rand.New(rand.NewSource(1))

		first, err := s.Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		require.NoError(t, err)
		s.Slots.Release(first.ID)

		_, err = s.Select(db, SelectInput{Model: "deepseek-flash", Now: now, ExcludeListingIDs: []int64{first.ID}})
		assert.ErrorIs(t, err, ErrNoListingAvailable)
		_, err = s.Select(db, SelectInput{Model: "deepseek-flash", Now: now.Add(61 * time.Second)})
		assert.NoError(t, err, "the RPM window rolls over after a minute")
	})

	t.Run("group daily cap skips every key", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		group := addSelectorGroup(t, db, func(g *ListingGroup) { g.ChannelDailyCapMicro = 500 })
		a := addSelectorListing(t, db, "fp-gd-a", func(l *Listing) { l.GroupID = group.ID })
		require.NoError(t, db.Create(&LedgerEntry{
			RequestID: "req-group-daily", EntryType: EntryBuyerDebit,
			Account: BuyerAccount(1), ListingID: a.ID, AmountMicro: -600,
			Status: LedgerAvailable, CreatedAt: now,
		}).Error)

		_, err := NewSelector().Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		assert.ErrorIs(t, err, ErrNoListingAvailable)
	})

	t.Run("group cumulative cap counts older ledger rows", func(t *testing.T) {
		db := newTestDB(t)
		migrateAll(t, db)
		group := addSelectorGroup(t, db, func(g *ListingGroup) { g.ChannelCumulativeCapMicro = 500 })
		a := addSelectorListing(t, db, "fp-gt-a", func(l *Listing) { l.GroupID = group.ID })
		require.NoError(t, db.Create(&LedgerEntry{
			RequestID: "req-group-total", EntryType: EntryBuyerDebit,
			Account: BuyerAccount(1), ListingID: a.ID, AmountMicro: -600,
			Status: LedgerAvailable, CreatedAt: now.AddDate(0, 0, -10),
		}).Error)

		_, err := NewSelector().Select(db, SelectInput{Model: "deepseek-flash", Now: now})
		assert.ErrorIs(t, err, ErrNoListingAvailable)
	})
}
