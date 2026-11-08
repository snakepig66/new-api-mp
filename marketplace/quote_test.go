package marketplace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// newQuoteFixture migrates an isolated database, seeds the DeepSeek price
// books (§3.3/§3.4) and loads an empty-holiday calendar.
func newQuoteFixture(t *testing.T) (*gorm.DB, *Calendar) {
	t.Helper()
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, SeedPrices(db))
	cal, err := LoadCalendar(db)
	require.NoError(t, err)
	return db, cal
}

// flashUsage is the canonical deepseek-flash usage snapshot: 1M input +
// 0.5M output tokens priced at instant at with multiplier mult.
func flashUsage(mult int64, at time.Time) QuoteInput {
	return QuoteInput{
		Model: "deepseek-flash", Vendor: SeedVendorDeepSeek,
		MultiplierMilli: mult, At: at,
		InputTokens: 1_000_000, OutputTokens: 500_000,
	}
}

func TestQuoteFlat(t *testing.T) {
	db, cal := newQuoteFixture(t)

	// 20:00 is off-peak: input 1 元/M = 1e6 微元/M, output 4 元/M = 4e6 微元/M.
	// Σ = 1e6×1e6 + 0.5e6×4e6 = 3e12
	// TokenDebit   = ceil(3e12 × 300 / 1e9)  = 900_000 微元
	// OfficialCost = ceil(3e12 × 1000 / 1e9) = 3_000_000 微元
	res, err := Quote(db, cal, flashUsage(300, shTime(2026, time.October, 21, 20, 0, 0)))
	require.NoError(t, err)

	assert.Equal(t, TierFlat, res.Tier)
	assert.Equal(t, int64(1), res.PriceVersion)
	assert.Equal(t, Micro(900_000), res.TokenDebit)
	assert.Equal(t, Micro(0), res.ToolDebit)
	assert.Equal(t, Micro(900_000), res.TotalDebit)
	assert.Equal(t, Micro(3_000_000), res.OfficialCost)
	assert.Equal(t, res.TokenDebit+res.ToolDebit, res.TotalDebit)
}

func TestQuoteNoSplitAcrossTiers(t *testing.T) {
	db, cal := newQuoteFixture(t)

	// §3.3: the whole request (including the retry chain) prices at the
	// pre-freeze instant; a request is never split across tiers. With
	// input 1e6 + output 0.5e6 at mult=1000 both buckets must follow the
	// single tier of At: peak Σ = 6e12 → 6_000_000, off-peak Σ = 3e12 → 3_000_000.
	cases := []struct {
		at        time.Time
		wantTier  string
		wantDebit Micro
	}{
		// Peak/off-peak is retired: every instant bills the flat (former
		// off-peak) price, Σ = 3e12 → 3_000_000.
		{shTime(2026, time.October, 21, 11, 59, 59), TierFlat, 3_000_000},
		{shTime(2026, time.October, 21, 12, 0, 0), TierFlat, 3_000_000},
		{shTime(2026, time.October, 21, 14, 0, 0), TierFlat, 3_000_000},
		{shTime(2026, time.October, 21, 18, 0, 0), TierFlat, 3_000_000},
	}
	for _, tc := range cases {
		res, err := Quote(db, cal, flashUsage(1000, tc.at))
		require.NoError(t, err, "at %s", tc.at.Format("15:04:05"))
		assert.Equal(t, tc.wantTier, res.Tier, "at %s", tc.at.Format("15:04:05"))
		assert.Equal(t, tc.wantDebit, res.TokenDebit,
			"at %s the whole request bills at the single tier of the freeze instant", tc.at.Format("15:04:05"))
	}
}

func TestQuoteCacheBuckets(t *testing.T) {
	db, cal := newQuoteFixture(t)

	// Cache reads bill at the cache-read price, cache writes at the
	// cache-write price (seeded = the standard input price), each from its
	// own bucket (§3.2). mult=1000, 1M tokens per bucket:
	//   空闲: cr 1e6 × 0.02 元/M = 20_000; cw 1e6 × 1 元/M = 1_000_000 → 1_020_000
	//   高峰: cr 1e6 × 0.04 元/M = 40_000; cw 1e6 × 2 元/M = 2_000_000 → 2_040_000
	cases := []struct {
		name string
		at   time.Time
		want Micro
	}{
		{"offpeak", shTime(2026, time.October, 21, 20, 0, 0), 1_020_000},
		{"former peak hour", shTime(2026, time.October, 21, 10, 0, 0), 1_020_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Quote(db, cal, QuoteInput{
				Model: "deepseek-flash", Vendor: SeedVendorDeepSeek,
				MultiplierMilli: 1000, At: tc.at,
				CacheReadTokens: 1_000_000, CacheWriteTokens: 1_000_000,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, res.TokenDebit)
			assert.Equal(t, tc.want, res.TotalDebit, "no tools involved")
		})
	}
}

func TestQuoteMultiplierBounds(t *testing.T) {
	db, cal := newQuoteFixture(t)
	at := shTime(2026, time.October, 21, 20, 0, 0) // off-peak

	// input 1e6 off-peak → Σ = 1e12:
	//   mult=1    → ceil(1e12 × 1 / 1e9)    = 1_000
	//   mult=1000 → ceil(1e12 × 1000 / 1e9) = 1_000_000
	cases := []struct {
		mult int64
		want Micro
	}{
		{1, 1_000},
		{1000, 1_000_000},
	}
	for _, tc := range cases {
		res, err := Quote(db, cal, QuoteInput{Model: "deepseek-flash", Vendor: SeedVendorDeepSeek,
			MultiplierMilli: tc.mult, At: at, InputTokens: 1_000_000})
		require.NoError(t, err, "mult=%d", tc.mult)
		assert.Equal(t, tc.want, res.TokenDebit, "mult=%d", tc.mult)
		assert.Equal(t, tc.mult, res.MultiplierMilli)
	}

	// §3.2: the multiplier is an integer thousandth in 1..1000.
	for _, mult := range []int64{0, 1001, -3} {
		_, err := Quote(db, cal, QuoteInput{Model: "deepseek-flash", Vendor: SeedVendorDeepSeek,
			MultiplierMilli: mult, At: at, InputTokens: 1_000_000})
		assert.Error(t, err, "mult=%d is out of range 1..1000", mult)
		assert.ErrorContains(t, err, "multiplier")
	}
}

func TestQuoteRoundsUpExactlyOnce(t *testing.T) {
	db, cal := newQuoteFixture(t)
	at := shTime(2026, time.October, 21, 20, 0, 0) // off-peak
	eff := shTime(2026, time.January, 1, 0, 0, 0)

	// A 1 微元/M price book: the smallest possible positive charge.
	mustInsertBook(t, db, PriceBook{Model: "tiny-one", Vendor: "test", Tier: TierFlat, Version: 1,
		InputMicroPerMTok: 1, EffectiveAt: eff})
	// A 3 微元/M price book on both the input and the output bucket.
	mustInsertBook(t, db, PriceBook{Model: "tiny-three", Vendor: "test", Tier: TierFlat, Version: 1,
		InputMicroPerMTok: 3, OutputMicroPerMTok: 3, EffectiveAt: eff})

	// 1 token × 1 微元/M × mult=1 → ceil(1/1e9) = 1 微元, never 0 (§3.2 向上取整).
	res, err := Quote(db, cal, QuoteInput{Model: "tiny-one", Vendor: "test",
		MultiplierMilli: 1, At: at, InputTokens: 1})
	require.NoError(t, err)
	assert.Equal(t, Micro(1), res.TokenDebit)

	// 1 token × 3 微元/M × mult=1 → ceil(3/1e9) = 1.
	res, err = Quote(db, cal, QuoteInput{Model: "tiny-three", Vendor: "test",
		MultiplierMilli: 1, At: at, InputTokens: 1})
	require.NoError(t, err)
	assert.Equal(t, Micro(1), res.TokenDebit)

	// 1 input + 1 output token at 3 微元/M each: the single final ceil gives
	// ceil(6/1e9) = 1. Rounding each bucket up first would give 1+1 = 2.
	res, err = Quote(db, cal, QuoteInput{Model: "tiny-three", Vendor: "test",
		MultiplierMilli: 1, At: at, InputTokens: 1, OutputTokens: 1})
	require.NoError(t, err)
	assert.Equal(t, Micro(1), res.TokenDebit,
		"the bucket sum is rounded up exactly once; per-bucket rounding would give 2")
}

func TestQuoteToolBilling(t *testing.T) {
	db, cal := newQuoteFixture(t)

	// 2026-12-01 20:00 CST (Tuesday, off-peak) is past the 2026-11-01 tool
	// price switch-on (§3.4). mult=500 (0.5x), search=2, fetch=1:
	//   TokenDebit   = ceil(1e6×1e6 × 500 / 1e9)            = 500_000
	//   ToolDebit    = ceil((2×5000 + 1×5000) × 500 / 1000) = 7_500
	//   TotalDebit   = 507_500
	//   OfficialCost = 1_000_000 + 15_000                   = 1_015_000
	res, err := Quote(db, cal, QuoteInput{
		Model: "deepseek-flash", Vendor: SeedVendorDeepSeek,
		MultiplierMilli: 500, At: shTime(2026, time.December, 1, 20, 0, 0),
		InputTokens: 1_000_000,
		SearchCount: 2, FetchCount: 1,
	})
	require.NoError(t, err)

	assert.Equal(t, Micro(500_000), res.TokenDebit)
	assert.Equal(t, Micro(7_500), res.ToolDebit)
	assert.Equal(t, Micro(507_500), res.TotalDebit)
	assert.Equal(t, res.TokenDebit+res.ToolDebit, res.TotalDebit)
	assert.Equal(t, Micro(1_015_000), res.OfficialCost)
	assert.Equal(t, map[string]int64{ToolItemSearch: 1, ToolItemFetch: 1}, res.ToolPriceVersions,
		"every billed tool item pins its price-book version")
}

func TestQuoteToolPriceGate(t *testing.T) {
	db, cal := newQuoteFixture(t)

	// 2026-10-01 is before the tool price effective instant 2026-11-01 00:00
	// CST (§3.4): a positive count of an unpriceable tool fails the whole
	// quote; the caller strips the tool before relaying.
	at := shTime(2026, time.October, 1, 20, 0, 0)
	base := QuoteInput{Model: "deepseek-flash", Vendor: SeedVendorDeepSeek,
		MultiplierMilli: 1000, At: at, InputTokens: 1_000_000}

	withSearch := base
	withSearch.SearchCount = 1
	_, err := Quote(db, cal, withSearch)
	assert.ErrorIs(t, err, ErrNoPrice, "search has no covering price row before 2026-11-01")

	withFetch := base
	withFetch.FetchCount = 1
	_, err = Quote(db, cal, withFetch)
	assert.ErrorIs(t, err, ErrNoPrice, "fetch has no covering price row before 2026-11-01")

	// Zero counts are ignored entirely: the quote succeeds without tools.
	// TokenDebit = ceil(1e12 × 1000 / 1e9) = 1_000_000.
	res, err := Quote(db, cal, base)
	require.NoError(t, err)
	assert.Equal(t, Micro(1_000_000), res.TokenDebit)
	assert.Equal(t, Micro(0), res.ToolDebit)
	assert.Equal(t, Micro(1_000_000), res.TotalDebit)
	assert.Empty(t, res.ToolPriceVersions)
}

func TestQuoteOfficialCostEqualsFullPriceQuote(t *testing.T) {
	db, cal := newQuoteFixture(t)

	// OfficialCost is the 省了多少 baseline (§3.8): the same usage priced at
	// mult=1000. Off-peak, input 1e6 + output 0.5e6 + search 1:
	//   Σ_tokens = 3e12, Σ_tools = 5_000
	//   mult=300:  TokenDebit 900_000, ToolDebit ceil(5000×300/1000) = 1_500,
	//              TotalDebit = 901_500
	//   mult=1000: TotalDebit = 3_000_000 + 5_000 = 3_005_000 = OfficialCost
	at := shTime(2026, time.December, 1, 20, 0, 0)
	in := QuoteInput{
		Model: "deepseek-flash", Vendor: SeedVendorDeepSeek,
		At: at, InputTokens: 1_000_000, OutputTokens: 500_000, SearchCount: 1,
	}

	in.MultiplierMilli = 300
	discounted, err := Quote(db, cal, in)
	require.NoError(t, err)
	assert.Equal(t, Micro(900_000), discounted.TokenDebit)
	assert.Equal(t, Micro(1_500), discounted.ToolDebit)
	assert.Equal(t, Micro(901_500), discounted.TotalDebit)
	assert.Equal(t, Micro(3_005_000), discounted.OfficialCost)

	in.MultiplierMilli = 1000
	full, err := Quote(db, cal, in)
	require.NoError(t, err)
	assert.Equal(t, full.TotalDebit, discounted.OfficialCost,
		"OfficialCost at 0.3x must equal the full-price TotalDebit of the same usage")
}

// Protocol choice never changes the price: OpenAI-style (inclusive) and
// Anthropic-style (exclusive) usage describing the same consumption extract to
// the same token buckets, so Quote charges the same amount (§3.2).
func TestQuoteSameConsumptionAcrossEndpoints(t *testing.T) {
	db, cal := newQuoteFixture(t)
	at := shTime(2026, time.October, 21, 20, 0, 0) // off-peak

	chat := &dto.Usage{PromptTokens: 1_000_000, CompletionTokens: 500_000}
	chat.PromptTokensDetails.CachedTokens = 200_000

	responses := &dto.Usage{InputTokens: 1_000_000, OutputTokens: 500_000}
	responses.InputTokensDetails = &dto.InputTokenDetails{CachedTokens: 200_000}

	messages := &dto.Usage{
		PromptTokens: 800_000, InputTokens: 1_000_000, CompletionTokens: 500_000,
		UsageSemantic: dto.BillingUsageSemanticAnthropic,
	}
	messages.PromptTokensDetails.CachedTokens = 200_000

	cases := []struct {
		name  string
		usage *dto.Usage
		kind  relayconstant.EndpointKind
	}{
		{"openai chat", chat, relayconstant.EndpointOpenAIChat},
		{"openai responses", responses, relayconstant.EndpointOpenAIResponses},
		{"anthropic messages", messages, relayconstant.EndpointAnthropicMessages},
	}
	var want Micro
	for i, tc := range cases {
		u := extractMarketplaceUsage(nil, tc.usage, tc.kind)
		res, err := Quote(db, cal, QuoteInput{
			Model: "deepseek-flash", Vendor: SeedVendorDeepSeek,
			MultiplierMilli: 1000, At: at,
			InputTokens:      u.inputTokens,
			OutputTokens:     u.outputTokens,
			CacheReadTokens:  u.cacheRead,
			CacheWriteTokens: u.cacheWrite,
		})
		require.NoError(t, err, tc.name)
		assert.Equal(t, int64(800_000), u.inputTokens, tc.name)
		if i == 0 {
			want = res.TotalDebit
			continue
		}
		assert.Equal(t, want, res.TotalDebit, "%s prices the same consumption identically", tc.name)
	}
}

func TestQuoteRejectsNegativeUsage(t *testing.T) {
	db, cal := newQuoteFixture(t)

	cases := []struct {
		name string
		mut  func(*QuoteInput)
	}{
		{"input tokens", func(in *QuoteInput) { in.InputTokens = -1 }},
		{"output tokens", func(in *QuoteInput) { in.OutputTokens = -1 }},
		{"cache read tokens", func(in *QuoteInput) { in.CacheReadTokens = -1 }},
		{"cache write tokens", func(in *QuoteInput) { in.CacheWriteTokens = -1 }},
		{"search count", func(in *QuoteInput) { in.SearchCount = -1 }},
		{"fetch count", func(in *QuoteInput) { in.FetchCount = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := QuoteInput{Model: "deepseek-flash", Vendor: SeedVendorDeepSeek,
				MultiplierMilli: 300, At: shTime(2026, time.October, 21, 20, 0, 0),
				InputTokens: 1}
			tc.mut(&in)
			_, err := Quote(db, cal, in)
			assert.ErrorIs(t, err, ErrMoneyNegative)
		})
	}
}

func TestQuoteNoPriceBook(t *testing.T) {
	db, cal := newQuoteFixture(t)
	at := shTime(2026, time.October, 21, 20, 0, 0)

	_, err := Quote(db, cal, QuoteInput{Model: "no-such-model", Vendor: SeedVendorDeepSeek,
		MultiplierMilli: 1000, At: at, InputTokens: 1})
	assert.ErrorIs(t, err, ErrNoPrice)

	_, err = Quote(db, cal, QuoteInput{Model: "deepseek-flash", Vendor: "no-such-vendor",
		MultiplierMilli: 1000, At: at, InputTokens: 1})
	assert.ErrorIs(t, err, ErrNoPrice)
}

// A hosting channel (volcengine-ark) has no price book of its own: the
// official book lives under the standard model's original vendor (deepseek).
// pricingContext resolves both that owner and the channel coefficient, and
// Quote must price through them (§3.5).
func TestQuoteChannelOriginAndCoefficient(t *testing.T) {
	db, cal := newQuoteFixture(t)
	at := shTime(2026, time.October, 21, 20, 0, 0) // deepseek off-peak

	// Remove the hosted-channel copies so only the original vendor's book
	// remains, and set the channel coefficient to 0.5.
	require.NoError(t, db.Where("vendor = ? AND model = ?", "volcengine-ark", "deepseek-flash").
		Delete(&PriceBook{}).Error)
	require.NoError(t, db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", "volcengine-ark", "deepseek-flash").
		Update("coefficient_milli", 500).Error)

	origin, coefficient := pricingContext(db, "volcengine-ark", "deepseek-flash")
	require.Equal(t, SeedVendorDeepSeek, origin)
	require.Equal(t, int64(500), coefficient)

	// Without the origin resolution the hosted channel has no price book.
	_, err := Quote(db, cal, QuoteInput{Model: "deepseek-flash", Vendor: "volcengine-ark",
		MultiplierMilli: 1000, At: at, InputTokens: 1_000_000})
	assert.ErrorIs(t, err, ErrNoPrice, "the hosted channel slug alone has no price book")

	// Off-peak input is 1 元/M = 1_000_000 微元/M officially; coefficient 0.5
	// halves it.
	res, err := Quote(db, cal, QuoteInput{
		Model: "deepseek-flash", Vendor: "volcengine-ark",
		OriginVendor: origin, CoefficientMilli: coefficient,
		MultiplierMilli: 1000, At: at, InputTokens: 1_000_000,
	})
	require.NoError(t, err)
	assert.Equal(t, Micro(500_000), res.TokenDebit)
	assert.Equal(t, int64(500), res.CoefficientMilli, "QuoteResult reports the effective coefficient")
	assert.Equal(t, Micro(1_000_000), res.OfficialCost, "official cost ignores the channel coefficient")

	// A zero coefficient falls back to 1000 and is reported as such.
	res, err = Quote(db, cal, QuoteInput{
		Model: "deepseek-flash", Vendor: SeedVendorDeepSeek,
		MultiplierMilli: 1000, At: at, InputTokens: 1_000_000,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1000), res.CoefficientMilli)
}

// TestQuoteUsesModelMultiplier proves the per-model override wins over the
// listing-level multiplier and is reported back as the effective multiplier.
func TestQuoteUsesModelMultiplier(t *testing.T) {
	db, cal := newQuoteFixture(t)
	at := shTime(2026, time.October, 21, 20, 0, 0)

	in := flashUsage(300, at)
	in.ModelMultiplierMilli = 600
	res, err := Quote(db, cal, in)
	require.NoError(t, err)
	// Σ = 3e12; ceil(3e12 × 600 / 1e9) = 1_800_000.
	assert.Equal(t, Micro(1_800_000), res.TokenDebit)
	assert.Equal(t, int64(600), res.MultiplierMilli)
	assert.Equal(t, int64(600), res.ModelMultiplierMilli)

	// A zero override falls back to the listing multiplier.
	in.ModelMultiplierMilli = 0
	res, err = Quote(db, cal, in)
	require.NoError(t, err)
	assert.Equal(t, int64(300), res.MultiplierMilli)
	assert.Equal(t, Micro(900_000), res.TokenDebit)

	in.ModelMultiplierMilli = 1001
	_, err = Quote(db, cal, in)
	assert.Error(t, err, "an out-of-range override is rejected")
}
