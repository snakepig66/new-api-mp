package marketplace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// mustInsertBook writes one price-book row into the fixture database.
func mustInsertBook(t *testing.T, db *gorm.DB, pb PriceBook) {
	t.Helper()
	require.NoError(t, db.Create(&pb).Error)
}

func TestPriceAtVersionSwitch(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	// Two versions of the same (model, vendor, tier): v1 effective
	// 2026-01-01, v2 effective 2026-06-01 (§3.3 价目表带版本号).
	mustInsertBook(t, db, PriceBook{Model: "m1", Vendor: "v1", Tier: TierPeak, Version: 1,
		InputMicroPerMTok: 100, EffectiveAt: shTime(2026, time.January, 1, 0, 0, 0)})
	mustInsertBook(t, db, PriceBook{Model: "m1", Vendor: "v1", Tier: TierPeak, Version: 2,
		InputMicroPerMTok: 200, EffectiveAt: shTime(2026, time.June, 1, 0, 0, 0)})

	pb, err := PriceAt(db, "m1", "v1", TierPeak, shTime(2026, time.March, 1, 0, 0, 0))
	require.NoError(t, err)
	assert.Equal(t, int64(1), pb.Version, "before v2 takes effect the older version serves")
	assert.Equal(t, int64(100), pb.InputMicroPerMTok)

	pb, err = PriceAt(db, "m1", "v1", TierPeak, shTime(2026, time.July, 1, 0, 0, 0))
	require.NoError(t, err)
	assert.Equal(t, int64(2), pb.Version, "after v2 takes effect it wins")
	assert.Equal(t, int64(200), pb.InputMicroPerMTok)

	// Same effective_at: the higher version breaks the tie.
	mustInsertBook(t, db, PriceBook{Model: "m2", Vendor: "v1", Tier: TierPeak, Version: 1,
		InputMicroPerMTok: 100, EffectiveAt: shTime(2026, time.January, 1, 0, 0, 0)})
	mustInsertBook(t, db, PriceBook{Model: "m2", Vendor: "v1", Tier: TierPeak, Version: 2,
		InputMicroPerMTok: 222, EffectiveAt: shTime(2026, time.January, 1, 0, 0, 0)})

	pb, err = PriceAt(db, "m2", "v1", TierPeak, shTime(2026, time.February, 1, 0, 0, 0))
	require.NoError(t, err)
	assert.Equal(t, int64(2), pb.Version, "same effective_at: the higher version wins")
	assert.Equal(t, int64(222), pb.InputMicroPerMTok)
}

func TestPriceAtEffectiveAtInclusive(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	eff := shTime(2026, time.June, 1, 0, 0, 0)
	mustInsertBook(t, db, PriceBook{Model: "m1", Vendor: "v1", Tier: TierPeak, Version: 1,
		InputMicroPerMTok: 100, EffectiveAt: eff})

	_, err := PriceAt(db, "m1", "v1", TierPeak, eff.Add(-time.Second))
	assert.ErrorIs(t, err, ErrNoPrice, "one second before effective_at the row does not cover")

	pb, err := PriceAt(db, "m1", "v1", TierPeak, eff)
	require.NoError(t, err, "effective_at <= at: the exact switch-over instant is covered")
	assert.Equal(t, int64(1), pb.Version)
}

func TestPriceAtFlatFallback(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	at := shTime(2026, time.March, 1, 0, 0, 0)
	eff := shTime(2026, time.January, 1, 0, 0, 0)

	// A vendor that does not split peak/off-peak publishes only a flat tier
	// (§3.3 不分时的厂商只配一档).
	mustInsertBook(t, db, PriceBook{Model: "flat-only", Vendor: "v1", Tier: TierFlat, Version: 1,
		InputMicroPerMTok: 100, EffectiveAt: eff})

	pb, err := PriceAt(db, "flat-only", "v1", TierPeak, at)
	require.NoError(t, err, "a peak query falls back to the flat tier")
	assert.Equal(t, TierFlat, pb.Tier, "the returned row is the flat one actually priced")
	assert.Equal(t, int64(100), pb.InputMicroPerMTok)

	pb, err = PriceAt(db, "flat-only", "v1", TierOffpeak, at)
	require.NoError(t, err, "an off-peak query falls back to the flat tier too")
	assert.Equal(t, TierFlat, pb.Tier)

	// A model with a real peak tier must NOT fall back to flat.
	mustInsertBook(t, db, PriceBook{Model: "two-tier", Vendor: "v1", Tier: TierFlat, Version: 1,
		InputMicroPerMTok: 100, EffectiveAt: eff})
	mustInsertBook(t, db, PriceBook{Model: "two-tier", Vendor: "v1", Tier: TierPeak, Version: 1,
		InputMicroPerMTok: 200, EffectiveAt: eff})

	pb, err = PriceAt(db, "two-tier", "v1", TierPeak, at)
	require.NoError(t, err)
	assert.Equal(t, TierPeak, pb.Tier, "an exact peak row beats the flat fallback")
	assert.Equal(t, int64(200), pb.InputMicroPerMTok)

	// A model with only a peak row has nothing an off-peak query can fall
	// back to.
	mustInsertBook(t, db, PriceBook{Model: "peak-only", Vendor: "v1", Tier: TierPeak, Version: 1,
		InputMicroPerMTok: 200, EffectiveAt: eff})
	_, err = PriceAt(db, "peak-only", "v1", TierOffpeak, at)
	assert.ErrorIs(t, err, ErrNoPrice, "no off-peak row and no flat row to fall back to")
}

func TestPriceAtNotFound(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	_, err := PriceAt(db, "no-such-model", "v1", TierPeak, shTime(2026, time.March, 1, 0, 0, 0))
	assert.ErrorIs(t, err, ErrNoPrice)
}

func TestPerCallPriceAtSeedBoundary(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, SeedPrices(db))

	// §3.4: Search/Fetch ¥5 per 1000 successful calls = 5,000 微元/次,
	// effective 2026-11-01 00:00 Asia/Shanghai.
	boundary := shTime(2026, time.November, 1, 0, 0, 0)
	for _, item := range []string{ToolItemSearch, ToolItemFetch} {
		_, _, err := PerCallPriceAt(db, SeedVendorDeepSeek, item, boundary.Add(-time.Second))
		assert.ErrorIs(t, err, ErrNoPrice, "%s: one second before the switch-on", item)

		unit, version, err := PerCallPriceAt(db, SeedVendorDeepSeek, item, boundary)
		require.NoError(t, err, "%s: at exactly the switch-on instant", item)
		assert.Equal(t, int64(5_000), unit, "%s unit price", item)
		assert.Equal(t, int64(1), version, "%s version", item)

		unit, version, err = PerCallPriceAt(db, SeedVendorDeepSeek, item, shTime(2026, time.December, 1, 12, 0, 0))
		require.NoError(t, err, "%s: after the switch-on", item)
		assert.Equal(t, int64(5_000), unit, "%s unit price", item)
		assert.Equal(t, int64(1), version, "%s version", item)
	}

	_, _, err := PerCallPriceAt(db, SeedVendorDeepSeek, "no-such-item", shTime(2026, time.December, 1, 12, 0, 0))
	assert.ErrorIs(t, err, ErrNoPrice)
}

func TestSeedPrices(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, SeedPrices(db))

	t.Run("contents", func(t *testing.T) {
		tokenEffective := shTime(2026, time.January, 1, 0, 0, 0)
		// §3.3 seed (元/百万tokens × 1e6 = 微元/百万tokens):
		//   deepseek-flash  空闲 1 / 0.02 / 4,   高峰 2 / 0.04 / 8
		//   deepseek-v4-pro 空闲 4.5 / 0.15 / 13.5, 高峰 9 / 0.30 / 27
		//   (input / cache hit / output); 缓存写 = 标准输入价.
		wants := []struct {
			model, tier     string
			in, out, cr, cw int64
		}{
			{"deepseek-flash", TierOffpeak, 1_000_000, 4_000_000, 20_000, 1_000_000},
			{"deepseek-flash", TierPeak, 2_000_000, 8_000_000, 40_000, 2_000_000},
			{"deepseek-v4-pro", TierOffpeak, 4_500_000, 13_500_000, 150_000, 4_500_000},
			{"deepseek-v4-pro", TierPeak, 9_000_000, 27_000_000, 300_000, 9_000_000},
		}
		for _, w := range wants {
			var pb PriceBook
			require.NoError(t, db.Where("model = ? AND tier = ?", w.model, w.tier).Take(&pb).Error,
				"seeded row %s/%s", w.model, w.tier)
			assert.Equal(t, SeedVendorDeepSeek, pb.Vendor, "%s/%s vendor", w.model, w.tier)
			assert.Equal(t, int64(1), pb.Version, "%s/%s version", w.model, w.tier)
			assert.Equal(t, w.in, pb.InputMicroPerMTok, "%s/%s input", w.model, w.tier)
			assert.Equal(t, w.out, pb.OutputMicroPerMTok, "%s/%s output", w.model, w.tier)
			assert.Equal(t, w.cr, pb.CacheReadMicroPerMTok, "%s/%s cache read", w.model, w.tier)
			assert.Equal(t, w.cw, pb.CacheWriteMicroPerMTok, "%s/%s cache write = standard input price", w.model, w.tier)
			assert.True(t, pb.EffectiveAt.Equal(tokenEffective),
				"%s/%s effective_at %s, want %s", w.model, w.tier, pb.EffectiveAt, tokenEffective)
		}

		toolEffective := shTime(2026, time.November, 1, 0, 0, 0)
		for _, item := range []string{ToolItemSearch, ToolItemFetch} {
			var p PerCallPrice
			require.NoError(t, db.Where("vendor = ? AND item = ?", SeedVendorDeepSeek, item).Take(&p).Error)
			assert.Equal(t, int64(5_000), p.UnitPriceMicro, "%s unit price (§3.4)", item)
			assert.Equal(t, int64(1), p.Version, "%s version", item)
			assert.True(t, p.EffectiveAt.Equal(toolEffective),
				"%s effective_at %s, want %s", item, p.EffectiveAt, toolEffective)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		countRows := func() (books int64, perCall int64) {
			require.NoError(t, db.Model(&PriceBook{}).Count(&books).Error)
			require.NoError(t, db.Model(&PerCallPrice{}).Count(&perCall).Error)
			return books, perCall
		}
		booksBefore, perCallBefore := countRows()
		assert.Equal(t, int64(4), booksBefore, "default whitelist: 2 deepseek models × 2 tiers (H8: tipig not seeded)")
		assert.Equal(t, int64(2), perCallBefore, "search + fetch")

		require.NoError(t, SeedPrices(db), "a second run on the same database must succeed")

		booksAfter, perCallAfter := countRows()
		assert.Equal(t, booksBefore, booksAfter, "SeedPrices must skip rows that already exist")
		assert.Equal(t, perCallBefore, perCallAfter, "SeedPrices must skip rows that already exist")
	})
}

// H8: the four tipig seed rows exist only when the vendor was injected through
// MP_EXTRA_VENDORS; the production whitelist stays free of the E2E gateway.
func TestSeedPricesTipigInjected(t *testing.T) {
	withExtraVendors(t, tipigExtraVendors)

	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, SeedPrices(db))

	var books int64
	require.NoError(t, db.Model(&PriceBook{}).Count(&books).Error)
	assert.Equal(t, int64(8), books, "4 deepseek + 4 injected tipig rows")

	for _, model := range []string{"deepseek-v4.1-flash-1017", "deepseek-v4-pro-0813"} {
		for _, tier := range []string{TierOffpeak, TierPeak} {
			var pb PriceBook
			require.NoError(t, db.Where("model = ? AND vendor = ? AND tier = ?", model, seedVendorTipig, tier).Take(&pb).Error,
				"seeded row %s/%s", model, tier)
			assert.Equal(t, int64(1), pb.Version)
		}
	}

	var perCall int64
	require.NoError(t, db.Model(&PerCallPrice{}).Count(&perCall).Error)
	assert.Equal(t, int64(2), perCall, "per-call prices stay DeepSeek-only")
}
