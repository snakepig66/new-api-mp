package marketplace

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

	t.Run("limit zero falls back to the §3.5 default of five", func(t *testing.T) {
		m := NewSlotManager()
		for range 5 {
			require.True(t, m.Acquire(1, 0, t0))
		}
		assert.False(t, m.Acquire(1, 0, t0))
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
