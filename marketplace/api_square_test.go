package marketplace

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// ---------------------------------------------------------------------------
// square fixture
// ---------------------------------------------------------------------------

type squareFixture struct {
	db     *gorm.DB
	cal    *Calendar
	engine *gin.Engine
	fpSeq  int
}

func newSquareFixture(t *testing.T) *squareFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, SeedPrices(db))
	cal, err := LoadCalendar(db)
	require.NoError(t, err)

	relayStateMu.Lock()
	oldDB, oldCal := relayDB, defaultCalendar
	relayDB, defaultCalendar = db, cal
	relayStateMu.Unlock()
	t.Cleanup(func() {
		relayStateMu.Lock()
		relayDB, defaultCalendar = oldDB, oldCal
		relayStateMu.Unlock()
	})

	engine := gin.New()
	registerSquareRoutes(engine.Group("/api/marketplace"))
	return &squareFixture{db: db, cal: cal, engine: engine}
}

// addListing writes one listing row directly on the DeepSeek channel. The
// square only reads status/models/multiplier, so the key material fields are
// inert placeholders — and the assertions below double-check they never
// surface.
func (f *squareFixture) addListing(t *testing.T, sellerID int64, models []string, mult int64, status string) {
	t.Helper()
	f.addListingOnChannel(t, sellerID, SeedVendorDeepSeek, models, mult, status)
}

// addListingOnChannel is addListing with an explicit channel slug.
func (f *squareFixture) addListingOnChannel(t *testing.T, sellerID int64, vendor string, models []string, mult int64, status string) {
	t.Helper()
	f.fpSeq++
	require.NoError(t, f.db.Create(&Listing{
		SellerID:         sellerID,
		ChannelID:        1,
		Vendor:           vendor,
		Models:           models,
		MultiplierMilli:  mult,
		ConcurrencyLimit: 5,
		Status:           status,
		KeyFingerprint:   fmt.Sprintf("fp-square-%s-%d", t.Name(), f.fpSeq),
		KeyCiphertext:    []byte("square-test-ciphertext"),
	}).Error)
}

// setChannelCoefficient updates one pool qualification's price coefficient.
func (f *squareFixture) setChannelCoefficient(t *testing.T, channel, model string, milli int64) {
	t.Helper()
	res := f.db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", channel, model).
		Update("coefficient_milli", milli)
	require.NoError(t, res.Error)
	require.Equal(t, int64(1), res.RowsAffected, "qualification %s/%s must exist", channel, model)
}

func findSquareChannel(t *testing.T, channels []any, slug string) map[string]any {
	t.Helper()
	for _, c := range channels {
		m, ok := c.(map[string]any)
		require.True(t, ok)
		if m["channel_slug"] == slug {
			return m
		}
	}
	require.FailNow(t, "channel not found in detail", "channel=%s channels=%v", slug, channels)
	return nil
}

func (f *squareFixture) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	f.engine.ServeHTTP(w, req)
	return w
}

// pricedModelCount is the number of active standard models that have an
// origin-vendor price book, i.e. exactly the rows the square advertises.
func pricedModelCount(t *testing.T, db *gorm.DB) int {
	t.Helper()
	var models []ModelPool
	require.NoError(t, db.Where("status = ?", PoolStatusActive).Find(&models).Error)
	n := 0
	for _, m := range models {
		var cnt int64
		require.NoError(t, db.Model(&PriceBook{}).
			Where("model = ? AND vendor = ?", m.Name, m.OriginVendor).
			Count(&cnt).Error)
		if cnt > 0 {
			n++
		}
	}
	return n
}

func findSquareItem(t *testing.T, items []any, model string) map[string]any {
	t.Helper()
	for _, it := range items {
		m, ok := it.(map[string]any)
		require.True(t, ok)
		if m["model"] == model {
			return m
		}
	}
	require.FailNow(t, "model not found in square items", "model=%s items=%v", model, items)
	return nil
}

// ---------------------------------------------------------------------------
// 1. square list: models with live/canary supply appear with the current
//    tier, both tiers' buyer prices (official × min multiplier, via Quote),
//    and a next_switch that actually flips the tier
// ---------------------------------------------------------------------------

func TestApiSquareModels(t *testing.T) {
	f := newSquareFixture(t)
	f.addListing(t, 9001, []string{"deepseek-flash"}, 300, ListingLive)
	f.addListingOnChannel(t, 9002, "volcengine-ark", []string{"deepseek-flash"}, 500, ListingLive)
	// canary supply counts as available (§3.5 灰度接单); paused does not.
	f.addListing(t, 9003, []string{"deepseek-v4-pro"}, 800, ListingCanary)
	f.addListing(t, 9004, []string{"deepseek-v4-pro"}, 900, ListingPaused)

	w := f.get(t, "/api/marketplace/square/models")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	body := decodeAPIBody(t, w)
	require.Equal(t, true, body["success"])
	items, ok := body["data"].([]any)
	require.True(t, ok, "data must be an array, body: %v", body)
	// One row per active standard model with an origin price book.
	require.Len(t, items, pricedModelCount(t, f.db), "one row per priced standard model")

	flash := findSquareItem(t, items, "deepseek-flash")
	assert.Contains(t, flash["aliases"], "DeepSeek-Flash", "the key picker searches aliases")
	assert.Equal(t, "deepseek", flash["origin_vendor"])
	assert.Equal(t, "deepseek", flash["vendor"], "vendor stays as an origin_vendor alias for old frontends")
	assert.Equal(t, int64(2), apiNum(t, flash, "listing_count"), "two live listings")
	assert.Equal(t, int64(2), apiNum(t, flash, "channel_count"), "two channels carry supply")
	assert.Equal(t, BuyerPriceSourceMarket, flash["buyer_price_source"])

	// generated_at pins the instant the entry was computed at; every
	// time-derived assertion goes through it, so the test cannot straddle a
	// tier boundary.
	genAt, err := time.Parse(time.RFC3339Nano, flash["generated_at"].(string))
	require.NoError(t, err)
	tier := f.cal.TierAt(genAt)
	assert.Equal(t, tier, flash["tier"])
	assert.Equal(t, tierLabel(tier), flash["tier_label"])

	// next_switch is in the future and actually flips the tier.
	nsStr, ok := flash["next_switch"].(string)
	require.True(t, ok && nsStr != "", "next_switch must be set, entry: %v", flash)
	next, err := time.Parse(time.RFC3339Nano, nsStr)
	require.NoError(t, err)
	assert.True(t, next.After(genAt), "next_switch %s must be after generated_at %s", next, genAt)
	assert.NotEqual(t, tier, f.cal.TierAt(next), "next_switch must flip the tier")
	assert.Equal(t, f.cal.NextSwitch(genAt).UTC(), next.UTC(), "next_switch is Calendar.NextSwitch")

	// Buyer prices: official price book × min multiplier 300 (§3.2), per
	// dimension, with Quote's ceil rounding.
	prices, ok := flash["prices"].(map[string]any)
	require.True(t, ok, "prices must be an object, entry: %v", flash)
	peak, ok := prices[TierPeak].(map[string]any)
	require.True(t, ok, "peak prices must be present, prices: %v", prices)
	offpeak, ok := prices[TierOffpeak].(map[string]any)
	require.True(t, ok, "offpeak prices must be present, prices: %v", prices)

	// deepseek-flash seed (§3.3): peak input 2 元/M, output 8 元/M,
	// cache read 0.04 元/M, cache write 2 元/M; off-peak is half.
	assert.Equal(t, int64(600_000), apiNum(t, peak, "input_micro_per_mtok"), "2e6 × 0.3")
	assert.Equal(t, int64(2_400_000), apiNum(t, peak, "output_micro_per_mtok"), "8e6 × 0.3")
	assert.Equal(t, int64(12_000), apiNum(t, peak, "cache_read_micro_per_mtok"), "4e4 × 0.3")
	assert.Equal(t, int64(600_000), apiNum(t, peak, "cache_write_micro_per_mtok"), "2e6 × 0.3")
	assert.Equal(t, int64(300_000), apiNum(t, offpeak, "input_micro_per_mtok"), "1e6 × 0.3")
	assert.Equal(t, int64(1_200_000), apiNum(t, offpeak, "output_micro_per_mtok"), "4e6 × 0.3")
	assert.Equal(t, int64(6_000), apiNum(t, offpeak, "cache_read_micro_per_mtok"), "2e4 × 0.3")
	assert.Equal(t, int64(300_000), apiNum(t, offpeak, "cache_write_micro_per_mtok"), "1e6 × 0.3")
	assert.Equal(t, apiNum(t, offpeak, "input_micro_per_mtok")*2, apiNum(t, peak, "input_micro_per_mtok"),
		"§3.3: 高峰 = 空闲 × 2")

	// The displayed price must come out of the single Quote function (§3.7):
	// re-pricing the same dimension at the entry's instant reproduces it.
	q, err := Quote(f.db, f.cal, QuoteInput{
		Model: "deepseek-flash", Vendor: SeedVendorDeepSeek, OriginVendor: SeedVendorDeepSeek,
		CoefficientMilli: 1000, MultiplierMilli: 300, At: genAt, InputTokens: MicroPerMillionTokens,
	})
	require.NoError(t, err)
	currentPrices, ok := prices[tier].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, int64(q.TotalDebit), apiNum(t, currentPrices, "input_micro_per_mtok"),
		"display price == Quote price at the same instant")

	// The canary-only model appears with its canary multiplier as the floor;
	// the paused listing at 0.9x is not supply.
	pro := findSquareItem(t, items, "deepseek-v4-pro")
	assert.Equal(t, int64(1), apiNum(t, pro, "listing_count"))
	proPrices := pro["prices"].(map[string]any)
	proOff := proPrices[TierOffpeak].(map[string]any)
	assert.Equal(t, int64(3_600_000), apiNum(t, proOff, "input_micro_per_mtok"), "4.5e6 × 0.8 (canary)")

	// Aggregate-only output: no seller/listing identifiers anywhere (§3.1).
	for _, absent := range []string{"seller_id", "listing_id", "key_fingerprint", "key_ciphertext", "9001", "9002", "9003"} {
		assert.NotContains(t, w.Body.String(), absent, "square leaks %s", absent)
	}
}

// A standard model with no live/canary listing still appears, priced at the
// official price (buyer_price_source=official). Paused/draft listings are not
// supply, so listing_count is zero but the model is not hidden.
func TestApiSquareUnlistedModelsUseOfficialPrice(t *testing.T) {
	f := newSquareFixture(t)
	f.addListing(t, 9001, []string{"deepseek-flash"}, 300, ListingPaused) // paused ≠ supply
	f.addListing(t, 9002, []string{"deepseek-flash"}, 300, ListingDraft)

	w := f.get(t, "/api/marketplace/square/models")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	items, ok := decodeAPIBody(t, w)["data"].([]any)
	require.True(t, ok)
	require.Len(t, items, pricedModelCount(t, f.db), "unlisted standard models are still advertised")

	flash := findSquareItem(t, items, "deepseek-flash")
	assert.Equal(t, int64(0), apiNum(t, flash, "listing_count"))
	assert.Equal(t, int64(0), apiNum(t, flash, "channel_count"))
	assert.Equal(t, BuyerPriceSourceOfficial, flash["buyer_price_source"])
	assert.Equal(t, "deepseek", flash["origin_vendor"])
	assert.Equal(t, "deepseek", flash["vendor"])

	prices := flash["prices"].(map[string]any)
	peak := prices[TierPeak].(map[string]any)
	offpeak := prices[TierOffpeak].(map[string]any)
	assert.Equal(t, int64(2_000_000), apiNum(t, peak, "input_micro_per_mtok"), "official peak input, ×1.0")
	assert.Equal(t, int64(1_000_000), apiNum(t, offpeak, "input_micro_per_mtok"), "official off-peak input, ×1.0")

	// The detail page is reachable too, with the neutral multiplier band.
	w = f.get(t, "/api/marketplace/square/models/deepseek-flash")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	detail := apiData(t, decodeAPIBody(t, w))
	assert.Equal(t, BuyerPriceSourceOfficial, detail["buyer_price_source"])
	assert.Equal(t, int64(1000), apiNum(t, detail, "multiplier_min_milli"))
	assert.Equal(t, int64(1000), apiNum(t, detail, "multiplier_max_milli"))
	assert.Equal(t, int64(0), apiNum(t, detail, "listing_count"))
	channels, ok := detail["channels"].([]any)
	require.True(t, ok)
	deepseekCh := findSquareChannel(t, channels, "deepseek")
	assert.Equal(t, int64(0), apiNum(t, deepseekCh, "listing_count"))
	assert.Equal(t, int64(0), apiNum(t, deepseekCh, "min_multiplier_milli"))

	// Unknown / non-standard names are still 404.
	assert.Equal(t, http.StatusNotFound, f.get(t, "/api/marketplace/square/models/no-such-model").Code)
	// Retired pool models are hidden from the square.
	require.NoError(t, f.db.Model(&ModelPool{}).Where("name = ?", "deepseek-v4-pro").
		Update("status", PoolStatusRetired).Error)
	w = f.get(t, "/api/marketplace/square/models")
	require.Equal(t, http.StatusOK, w.Code)
	items = decodeAPIBody(t, w)["data"].([]any)
	require.Len(t, items, pricedModelCount(t, f.db), "retiring one model removes exactly its row")
	assert.Equal(t, "deepseek-flash", items[0].(map[string]any)["model"])
	assert.Equal(t, http.StatusNotFound, f.get(t, "/api/marketplace/square/models/deepseek-v4-pro").Code)
}

// A channel coefficient below 1000 halves the buyer price: the square picks
// the listing with the smallest coefficient × multiplier factor and passes
// both values to Quote.
func TestApiSquarePriceAppliesChannelCoefficient(t *testing.T) {
	f := newSquareFixture(t)
	f.setChannelCoefficient(t, "volcengine-ark", "deepseek-flash", 500)
	f.addListing(t, 9001, []string{"deepseek-flash"}, 500, ListingLive)                            // 1000 × 500
	f.addListingOnChannel(t, 9002, "volcengine-ark", []string{"deepseek-flash"}, 500, ListingLive) // 500 × 500 = half

	w := f.get(t, "/api/marketplace/square/models")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	items := decodeAPIBody(t, w)["data"].([]any)
	flash := findSquareItem(t, items, "deepseek-flash")
	assert.Equal(t, BuyerPriceSourceMarket, flash["buyer_price_source"])
	assert.Equal(t, int64(2), apiNum(t, flash, "listing_count"))
	assert.Equal(t, int64(2), apiNum(t, flash, "channel_count"))

	prices := flash["prices"].(map[string]any)
	peak := prices[TierPeak].(map[string]any)
	offpeak := prices[TierOffpeak].(map[string]any)
	// official peak input 2e6 × 0.5 (coefficient) × 0.5 (multiplier).
	assert.Equal(t, int64(500_000), apiNum(t, peak, "input_micro_per_mtok"))
	assert.Equal(t, int64(250_000), apiNum(t, offpeak, "input_micro_per_mtok"))
	assert.Equal(t, int64(2_000_000), apiNum(t, peak, "output_micro_per_mtok"))

	// The same factor comes out of Quote.
	q, err := Quote(f.db, f.cal, QuoteInput{
		Model: "deepseek-flash", Vendor: "volcengine-ark", OriginVendor: "deepseek",
		CoefficientMilli: 500, MultiplierMilli: 500, At: flashGeneratedAt(t, flash),
		InputTokens: MicroPerMillionTokens,
	})
	require.NoError(t, err)
	currentTierPrices := prices[flash["tier"].(string)].(map[string]any)
	assert.Equal(t, int64(q.TotalDebit), apiNum(t, currentTierPrices, "input_micro_per_mtok"))
}

// flashGeneratedAt decodes an entry's generated_at instant.
func flashGeneratedAt(t *testing.T, entry map[string]any) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, entry["generated_at"].(string))
	require.NoError(t, err)
	return at
}

// Aliases roll up under the standard model row; no alias ever becomes its own
// square entry.
func TestApiSquareUsesStandardModelNames(t *testing.T) {
	f := newSquareFixture(t)
	f.addListing(t, 9001, []string{"DeepSeek-Flash"}, 300, ListingLive) // pool alias of deepseek-flash

	w := f.get(t, "/api/marketplace/square/models")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	items, ok := decodeAPIBody(t, w)["data"].([]any)
	require.True(t, ok)
	require.Len(t, items, pricedModelCount(t, f.db), "an alias must not create a second row")
	for _, it := range items {
		assert.NotEqual(t, "DeepSeek-Flash", it.(map[string]any)["model"])
	}
	flash := findSquareItem(t, items, "deepseek-flash")
	assert.Equal(t, int64(1), apiNum(t, flash, "listing_count"), "alias listing rolls up to the standard model")
}

// ---------------------------------------------------------------------------
// 2. single-model detail: list fields + official per-tier prices + the
//    supply's multiplier band
// ---------------------------------------------------------------------------

func TestApiSquareModelDetail(t *testing.T) {
	f := newSquareFixture(t)
	f.addListing(t, 9001, []string{"deepseek-flash"}, 300, ListingLive)
	f.addListing(t, 9002, []string{"deepseek-flash"}, 500, ListingLive)
	// Hosted cloud rows ship unverified (the seed cannot prove the upstream
	// mapping). Simulate the administrator verifying them here.
	require.NoError(t, f.db.Model(&ChannelModel{}).
		Where("channel_slug IN ? AND model = ?", []string{"volcengine-ark", "aliyun-bailian"}, "deepseek-flash").
		Updates(map[string]any{"verified": true, "fingerprint_ratio": 1.0}).Error)

	w := f.get(t, "/api/marketplace/square/models/deepseek-flash")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	detail := apiData(t, decodeAPIBody(t, w))

	assert.Equal(t, "deepseek-flash", detail["model"])
	assert.Equal(t, int64(300), apiNum(t, detail, "multiplier_min_milli"))
	assert.Equal(t, int64(500), apiNum(t, detail, "multiplier_max_milli"))

	official, ok := detail["official_prices"].(map[string]any)
	require.True(t, ok, "official_prices must be an object, detail: %v", detail)
	peak, ok := official[TierPeak].(map[string]any)
	require.True(t, ok, "peak official price must be present")
	assert.Equal(t, int64(2_000_000), apiNum(t, peak, "input_micro_per_mtok"))
	assert.Equal(t, int64(8_000_000), apiNum(t, peak, "output_micro_per_mtok"))
	assert.Equal(t, int64(1), apiNum(t, peak, "version"))
	offpeak, ok := official[TierOffpeak].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, int64(1_000_000), apiNum(t, offpeak, "input_micro_per_mtok"))
	_, hasFlat := official[TierFlat]
	assert.False(t, hasFlat, "no flat price book is seeded for DeepSeek (§3.3)")

	// The detail view carries the same buyer prices as the list view.
	prices, ok := detail["prices"].(map[string]any)
	require.True(t, ok)
	peakBuyer, ok := prices[TierPeak].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, int64(600_000), apiNum(t, peakBuyer, "input_micro_per_mtok"))

	// channels: every verified+active qualification with an active channel is
	// listed — including channels with no listing right now (badges/detail).
	channels, ok := detail["channels"].([]any)
	require.True(t, ok, "channels must be an array, detail: %v", detail)
	require.Len(t, channels, 3, "deepseek-flash is sellable on three verified official channels")

	deepseekCh := findSquareChannel(t, channels, "deepseek")
	assert.Equal(t, "DeepSeek", deepseekCh["name"])
	assert.Equal(t, ChannelKindOfficial, deepseekCh["kind"])
	assert.Equal(t, true, deepseekCh["verified"], "已验真 badge")
	assert.Equal(t, int64(1000), apiNum(t, deepseekCh, "coefficient_milli"))
	assert.Equal(t, int64(2), apiNum(t, deepseekCh, "listing_count"))
	assert.Equal(t, int64(300), apiNum(t, deepseekCh, "min_multiplier_milli"))
	assert.Equal(t, int64(500), apiNum(t, deepseekCh, "max_multiplier_milli"))
	_, hasDifferences := deepseekCh["differences"].(map[string]any)
	assert.True(t, hasDifferences, "differences is always an object")

	arkCh := findSquareChannel(t, channels, "volcengine-ark")
	assert.Equal(t, int64(0), apiNum(t, arkCh, "listing_count"), "channel is listed even without supply")
	arkDiff, ok := arkCh["differences"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "volcengine-ark", arkDiff["host"], "differences passes through as an object")

	// Unverified plan qualifications (deepseek-v4-pro coding plans) never show.
	w = f.get(t, "/api/marketplace/square/models/deepseek-v4-pro")
	require.Equal(t, http.StatusOK, w.Code)
	proChannels, ok := apiData(t, decodeAPIBody(t, w))["channels"].([]any)
	require.True(t, ok)
	for _, c := range proChannels {
		assert.NotEqual(t, "volcengine-ark-coding-plan", c.(map[string]any)["channel_slug"])
		assert.NotEqual(t, "aliyun-bailian-coding-plan", c.(map[string]any)["channel_slug"])
	}
}

// ---------------------------------------------------------------------------
// 3. Calendar.NextSwitch
// ---------------------------------------------------------------------------

func TestNextSwitch(t *testing.T) {
	// Fixture calendar: zero holiday rows. 2026-10-21 is a Wednesday (pinned
	// by the TierAt tests), so 10-23 is Friday, 10-24/25 the weekend and
	// 10-26 Monday.
	_, cal := newTestCalendar(t)

	cases := []struct {
		name string
		at   time.Time
		want time.Time
	}{
		{"weekday peak → 12:00", shTime(2026, time.October, 21, 10, 0, 0), shTime(2026, time.October, 21, 12, 0, 0)},
		{"lunch break → 14:00", shTime(2026, time.October, 21, 12, 30, 0), shTime(2026, time.October, 21, 14, 0, 0)},
		{"17:59 → 18:00", shTime(2026, time.October, 21, 17, 59, 0), shTime(2026, time.October, 21, 18, 0, 0)},
		{"morning off-peak → 09:00", shTime(2026, time.October, 21, 8, 0, 0), shTime(2026, time.October, 21, 9, 0, 0)},
		{"evening → next weekday 09:00", shTime(2026, time.October, 21, 20, 0, 0), shTime(2026, time.October, 22, 9, 0, 0)},
		{"exact boundary 12:00:00 → 14:00", shTime(2026, time.October, 21, 12, 0, 0), shTime(2026, time.October, 21, 14, 0, 0)},
		// §3.3's peak window is 14:00–18:00, so Friday 17:00 is still peak and
		// flips at 18:00 the same day; the weekend crossing starts at 18:00.
		{"friday 17:00 (still peak) → 18:00", shTime(2026, time.October, 23, 17, 0, 0), shTime(2026, time.October, 23, 18, 0, 0)},
		{"friday 18:00 → monday 09:00", shTime(2026, time.October, 23, 18, 0, 0), shTime(2026, time.October, 26, 9, 0, 0)},
		{"friday evening → monday 09:00", shTime(2026, time.October, 23, 20, 0, 0), shTime(2026, time.October, 26, 9, 0, 0)},
		{"saturday → monday 09:00", shTime(2026, time.October, 24, 10, 0, 0), shTime(2026, time.October, 26, 9, 0, 0)},
		{"sunday night → monday 09:00", shTime(2026, time.October, 25, 23, 30, 0), shTime(2026, time.October, 26, 9, 0, 0)},
		{"sub-second input truncates to the second",
			time.Date(2026, time.October, 21, 11, 59, 59, 500_000_000, shanghaiTestLoc),
			shTime(2026, time.October, 21, 12, 0, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cal.NextSwitch(tc.at)
			assert.Equal(t, tc.want, got)
			if !tc.want.IsZero() {
				assert.True(t, got.After(tc.at.Truncate(time.Second)), "NextSwitch is strictly after t")
				assert.NotEqual(t, cal.TierAt(tc.at), cal.TierAt(got), "NextSwitch flips the tier")
			}
		})
	}
}

func TestNextSwitchHoliday(t *testing.T) {
	db, cal := newTestCalendar(t)

	// 法定节假日 (Wednesday): off-peak all day, so from mid-morning the next
	// flip is the following ordinary day's rules — Thursday 09:00.
	require.NoError(t, ImportHolidays(db, []Holiday{{Date: "2026-10-21", Kind: HolidayKindHoliday}}))
	require.NoError(t, cal.Reload(db))
	assert.Equal(t, shTime(2026, time.October, 22, 9, 0, 0),
		cal.NextSwitch(shTime(2026, time.October, 21, 10, 0, 0)),
		"holiday all day off-peak → next day follows the normal rules")

	// 调休上班 Saturday: the weekday windows apply, so 10:00 is peak and the
	// next flip is the same day's 12:00 lunch break.
	require.NoError(t, ImportHolidays(db, []Holiday{{Date: "2026-10-24", Kind: HolidayKindWorkday}}))
	require.NoError(t, cal.Reload(db))
	assert.Equal(t, shTime(2026, time.October, 24, 12, 0, 0),
		cal.NextSwitch(shTime(2026, time.October, 24, 10, 0, 0)),
		"调休周六 10:00 高峰 → 当天 12:00")
}

// ---------------------------------------------------------------------------
// 4. §3.10 outward error codes
// ---------------------------------------------------------------------------

// apiErrorObject decodes the OpenAI error envelope and asserts it carries
// exactly {message, type, code}.
func apiErrorObject(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
	errObj, ok := body["error"].(map[string]any)
	require.True(t, ok, "body must be an OpenAI error object: %s", w.Body.String())
	assert.Len(t, errObj, 3, "the error object carries exactly message/type/code")
	return errObj
}

func TestAPIErrorShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Default wording per status (§3.10 固定文案表).
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	WriteAPIError(c, http.StatusUnauthorized, "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	errObj := apiErrorObject(t, w)
	assert.Equal(t, "Invalid API key", errObj["message"])
	assert.Equal(t, "authentication_error", errObj["type"])
	assert.Equal(t, "invalid_api_key", errObj["code"])

	// A caller-supplied message overrides the default wording.
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	WriteAPIError(c2, http.StatusPaymentRequired, "top up first")
	assert.Equal(t, http.StatusPaymentRequired, w2.Code)
	errObj2 := apiErrorObject(t, w2)
	assert.Equal(t, "top up first", errObj2["message"])
	assert.Equal(t, "insufficient_quota", errObj2["type"])
	assert.Equal(t, "insufficient_balance", errObj2["code"])

	// A status outside the §3.10 table collapses to 503 — no other status may
	// leak outward.
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	WriteAPIError(c3, http.StatusInternalServerError, "")
	assert.Equal(t, http.StatusServiceUnavailable, w3.Code)
	assert.Equal(t, "no_supplier_available", apiErrorObject(t, w3)["code"])
}

func TestWriteMarketplaceErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantType   string
		wantCode   string
	}{
		{"insufficient balance → 402", ErrInsufficientBalance,
			http.StatusPaymentRequired, "insufficient_quota", "insufficient_balance"},
		{"wrapped balance → 402", fmt.Errorf("pre-freeze: %w", ErrInsufficientBalance),
			http.StatusPaymentRequired, "insufficient_quota", "insufficient_balance"},
		{"model not added to key → 400", ErrModelNotAddedToKey,
			http.StatusBadRequest, "invalid_request_error", "invalid_request"},
		{"no listing → 503", ErrNoListingAvailable,
			http.StatusServiceUnavailable, "server_error", "no_supplier_available"},
		{"no price book → 503", ErrNoPrice,
			http.StatusServiceUnavailable, "server_error", "no_supplier_available"},
		{"wrapped no-listing → 503", fmt.Errorf("select: %w", ErrNoListingAvailable),
			http.StatusServiceUnavailable, "server_error", "no_supplier_available"},
		{"unknown internal → 503 fixed wording", errors.New("db connection lost"),
			http.StatusServiceUnavailable, "server_error", "no_supplier_available"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			WriteMarketplaceError(c, tc.err)
			assert.Equal(t, tc.wantStatus, w.Code, "body: %s", w.Body.String())
			errObj := apiErrorObject(t, w)
			assert.Equal(t, tc.wantType, errObj["type"])
			assert.Equal(t, tc.wantCode, errObj["code"])
			msg, _ := errObj["message"].(string)
			assert.NotEmpty(t, msg)
			if errors.Is(tc.err, ErrModelNotAddedToKey) {
				assert.Contains(t, msg, "add it to the key", "§3.6: 提示去添加")
			}
			if tc.name == "unknown internal → 503 fixed wording" {
				assert.NotContains(t, msg, "db connection", "internal detail must not leak (§2.3)")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 5. GET /marketplace/vendors: channels come from the pool (status=active)
//    and models are verified+active qualifications with an origin price book
// ---------------------------------------------------------------------------

func newVendorsFixture(t *testing.T) (*gorm.DB, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	migrateAll(t, db)

	relayStateMu.Lock()
	oldDB := relayDB
	relayDB = db
	relayStateMu.Unlock()
	t.Cleanup(func() {
		relayStateMu.Lock()
		relayDB = oldDB
		relayStateMu.Unlock()
	})

	engine := gin.New()
	RegisterVendorsRoutes(engine.Group("/api/marketplace"))
	return db, engine
}

func getVendors(t *testing.T, engine *gin.Engine) []any {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/vendors", nil)
	engine.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	items, ok := decodeAPIBody(t, w)["data"].([]any)
	require.True(t, ok)
	return items
}

func findVendorItem(t *testing.T, items []any, id string) map[string]any {
	t.Helper()
	for _, it := range items {
		m, ok := it.(map[string]any)
		require.True(t, ok)
		if m["id"] == id {
			return m
		}
	}
	require.FailNow(t, "vendor not found", "id=%s items=%v", id, items)
	return nil
}

func vendorModels(t *testing.T, vendor map[string]any) []string {
	t.Helper()
	raw, ok := vendor["models"].([]any)
	require.True(t, ok, "models must be an array: %v", vendor)
	out := make([]string, 0, len(raw))
	for _, m := range raw {
		out = append(out, m.(string))
	}
	return out
}

func TestApiVendorsPoolModels(t *testing.T) {
	db, engine := newVendorsFixture(t)

	items := getVendors(t, engine)
	require.NotEmpty(t, items)

	// kind is adds alongside tier without changing the existing fields.
	for _, it := range items {
		v := it.(map[string]any)
		assert.Equal(t, v["tier"], v["kind"], "kind mirrors tier")
		assert.NotEmpty(t, v["id"])
		assert.NotEmpty(t, v["display_name"])
		assert.Contains(t, v, "protocols")
		assert.Contains(t, v, "passthrough_body")
	}

	deepseek := findVendorItem(t, items, "deepseek")
	assert.Equal(t, "DeepSeek", deepseek["display_name"])
	assert.Equal(t, VendorTierOfficial, deepseek["tier"])
	assert.Equal(t, ChannelKindOfficial, deepseek["kind"])
	assert.Equal(t, []string{"deepseek-flash", "deepseek-v4-pro"}, vendorModels(t, deepseek))

	// The pool channel set is the source: every seeded active channel appears.
	findVendorItem(t, items, "volcengine-ark")
	findVendorItem(t, items, "aliyun-bailian")
	findVendorItem(t, items, "moonshot")

	// Unverified plan qualifications are not listable, even though the plan
	// channel itself exists.
	arkPlan := findVendorItem(t, items, "volcengine-ark-coding-plan")
	assert.Equal(t, ChannelKindPlan, arkPlan["kind"])
	assert.Empty(t, vendorModels(t, arkPlan))

	// glm-4.6 still has no catalog price, so its verified qualification is not
	// listable even though priced GLM models are.
	zhipu := findVendorItem(t, items, "zhipu")
	assert.NotContains(t, vendorModels(t, zhipu), "glm-4.6")
	assert.Contains(t, vendorModels(t, zhipu), "glm-5.3")

	// Adding the origin price book makes the verified qualification listable.
	require.NoError(t, db.Create(&PriceBook{
		Model: "glm-4.6", Vendor: "zhipu", Tier: TierFlat, Version: 1,
		InputMicroPerMTok: 1_000_000, OutputMicroPerMTok: 2_000_000,
		EffectiveAt: time.Now().Add(-time.Hour),
	}).Error)
	zhipu = findVendorItem(t, getVendors(t, engine), "zhipu")
	assert.Contains(t, vendorModels(t, zhipu), "glm-4.6")

	// An unverified qualification drops out.
	require.NoError(t, db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", "zhipu", "glm-4.6").
		Update("verified", false).Error)
	zhipu = findVendorItem(t, getVendors(t, engine), "zhipu")
	assert.NotContains(t, vendorModels(t, zhipu), "glm-4.6")
	assert.Contains(t, vendorModels(t, zhipu), "glm-5.3")

	// Disabled channels are omitted from the directory entirely.
	require.NoError(t, db.Model(&ChannelPool{}).
		Where("slug = ?", "minimax").
		Update("status", PoolStatusDisabled).Error)
	for _, it := range getVendors(t, engine) {
		assert.NotEqual(t, "minimax", it.(map[string]any)["id"])
	}
}
