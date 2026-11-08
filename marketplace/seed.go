package marketplace

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// SeedVendorDeepSeek is the vendor id of the DeepSeek official channel, the
// first whitelisted vendor (§3.5).
const SeedVendorDeepSeek = "deepseek"

// seedVendorTipig is the optional E2E gateway vendor id. It is NOT in the
// production whitelist (H8); its seed rows are written only when the vendor
// has been injected through MP_EXTRA_VENDORS.
const seedVendorTipig = "tipig"

// SeedPrices idempotently writes the DeepSeek seed price books (§3.3/§3.4):
// peak and off-peak tiers for deepseek-flash and deepseek-v4-pro, plus the
// Search/Fetch per-call prices. Rows that already exist — same
// (model, vendor, tier, version) or (vendor, item, version) — are skipped, so
// Init can call this on every startup.
//
// Source: https://api-docs.deepseek.com/zh-cn/quick_start/pricing
// (fetched 2026-10-23). The page lists exactly these two models; no name
// mapping was needed. Raw official prices (元/百万 tokens):
//
//	deepseek-flash (DeepSeek-V4.1-Flash-1017)
//	  空闲: input(cache miss) 1,  cache hit 0.02, output 4
//	  高峰: input(cache miss) 2,  cache hit 0.04, output 8
//	deepseek-v4-pro (DeepSeek-V4-Pro-0813)
//	  空闲: input(cache miss) 4.5, cache hit 0.15, output 13.5
//	  高峰: input(cache miss) 9,   cache hit 0.30, output 27
//
// 高峰 = 空闲 × 2 matches §3.3. DeepSeek meters no separate cache write:
// cache-miss input is billed at the standard input rate, so CacheWrite is
// seeded at the input (cache-miss) rate and Input is that same standard rate.
// 元/百万tokens × 1e6 = 微元/百万tokens. Search/Fetch: ¥5 per 1000 successful
// calls = 5,000 微元/次, effective 2026-11-01 00:00 Asia/Shanghai (§3.4).
func SeedPrices(db *gorm.DB) error {
	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		return fmt.Errorf("marketplace: seed prices: %w", err)
	}
	tokenEffective := time.Date(2026, 1, 1, 0, 0, 0, 0, loc) // already in effect
	toolEffective := time.Date(2026, 11, 1, 0, 0, 0, 0, loc) // §3.4 switch-on

	books := []PriceBook{
		{Model: "deepseek-flash", Vendor: SeedVendorDeepSeek, Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 1_000_000, OutputMicroPerMTok: 4_000_000,
			CacheReadMicroPerMTok: 20_000, CacheWriteMicroPerMTok: 1_000_000,
			EffectiveAt: tokenEffective},
		{Model: "deepseek-v4-pro", Vendor: SeedVendorDeepSeek, Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 4_500_000, OutputMicroPerMTok: 13_500_000,
			CacheReadMicroPerMTok: 150_000, CacheWriteMicroPerMTok: 4_500_000,
			EffectiveAt: tokenEffective},
	}
	// Catalog prices for the other official channels, fetched 2026-10-25 from
	// each vendor's public price page (元/百万 tokens → 微元/百万 tokens).
	// They all publish a single price tier, so they use TierFlat; DeepSeek
	// is flat too since peak/off-peak pricing was retired (off-peak rate).
	//
	// Sources:
	//   https://platform.kimi.com/docs/pricing/chat
	//   https://docs.bigmodel.cn/cn/guide/models/text/glm-5
	//   https://platform.minimax.cn/docs/guides/pricing
	//   https://help.aliyun.com/zh/model-studio/qwen3-8-max
	//   https://help.aliyun.com/zh/model-studio/qwen3-7-plus
	//   https://help.aliyun.com/zh/model-studio/qwen3-8-flash
	//   https://www.volcengine.com/pricing?product=ark
	//
	// A missing per-token cache-write price (Zhipu free cache storage, Doubao
	// hourly cache storage) is seeded as 0 so storage is never double-billed.
	// Qwen publishes no cache discount, so cache reads are billed at the input
	// rate. qwen3.7-plus and minimax-m3 publish context-tiered prices; the pool
	// advertises the base tier (≤256k / ≤512k) and bills that row, so neither
	// model can silently undercharge long-context requests.
	catalogEffective := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	books = append(books,
		PriceBook{Model: "kimi-k3.1-pro", Vendor: "moonshot", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 13_000_000, OutputMicroPerMTok: 68_000_000,
			CacheReadMicroPerMTok: 1_300_000, CacheWriteMicroPerMTok: 13_000_000,
			EffectiveAt: catalogEffective},
		PriceBook{Model: "kimi-k3", Vendor: "moonshot", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 20_000_000, OutputMicroPerMTok: 100_000_000,
			CacheReadMicroPerMTok: 2_000_000, CacheWriteMicroPerMTok: 20_000_000,
			EffectiveAt: catalogEffective},
		PriceBook{Model: "kimi-k2.7-code", Vendor: "moonshot", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 6_500_000, OutputMicroPerMTok: 27_000_000,
			CacheReadMicroPerMTok: 1_300_000, CacheWriteMicroPerMTok: 6_500_000,
			EffectiveAt: catalogEffective},
		PriceBook{Model: "kimi-k2.6", Vendor: "moonshot", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 6_500_000, OutputMicroPerMTok: 27_000_000,
			CacheReadMicroPerMTok: 1_100_000, CacheWriteMicroPerMTok: 6_500_000,
			EffectiveAt: catalogEffective},

		PriceBook{Model: "glm-5.3", Vendor: "zhipu", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 8_000_000, OutputMicroPerMTok: 28_000_000,
			CacheReadMicroPerMTok: 2_000_000, CacheWriteMicroPerMTok: 0,
			EffectiveAt: catalogEffective},
		PriceBook{Model: "glm-5.3-flash", Vendor: "zhipu", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 800_000, OutputMicroPerMTok: 2_800_000,
			CacheReadMicroPerMTok: 230_000, CacheWriteMicroPerMTok: 0,
			EffectiveAt: catalogEffective},
		PriceBook{Model: "glm-5.3-flashx", Vendor: "zhipu", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 2_000_000, OutputMicroPerMTok: 7_000_000,
			CacheReadMicroPerMTok: 570_000, CacheWriteMicroPerMTok: 0,
			EffectiveAt: catalogEffective},
		PriceBook{Model: "glm-5.2", Vendor: "zhipu", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 8_000_000, OutputMicroPerMTok: 28_000_000,
			CacheReadMicroPerMTok: 2_000_000, CacheWriteMicroPerMTok: 0,
			EffectiveAt: catalogEffective},
		PriceBook{Model: "glm-4.7-flash", Vendor: "zhipu", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 0, OutputMicroPerMTok: 0,
			CacheReadMicroPerMTok: 0, CacheWriteMicroPerMTok: 0,
			EffectiveAt: catalogEffective},

		PriceBook{Model: "minimax-m3", Vendor: "minimax", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 2_100_000, OutputMicroPerMTok: 8_400_000,
			CacheReadMicroPerMTok: 420_000, CacheWriteMicroPerMTok: 2_100_000,
			EffectiveAt: catalogEffective},
		PriceBook{Model: "minimax-m3.1-flash", Vendor: "minimax", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 1_000_000, OutputMicroPerMTok: 4_000_000,
			CacheReadMicroPerMTok: 100_000, CacheWriteMicroPerMTok: 1_000_000,
			EffectiveAt: catalogEffective},

		PriceBook{Model: "qwen3.8-max", Vendor: "aliyun-bailian", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 12_000_000, OutputMicroPerMTok: 36_000_000,
			CacheReadMicroPerMTok: 12_000_000, CacheWriteMicroPerMTok: 12_000_000,
			EffectiveAt: catalogEffective},
		PriceBook{Model: "qwen3.7-plus", Vendor: "aliyun-bailian", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 2_000_000, OutputMicroPerMTok: 8_000_000,
			CacheReadMicroPerMTok: 2_000_000, CacheWriteMicroPerMTok: 2_000_000,
			EffectiveAt: catalogEffective},
		PriceBook{Model: "qwen3.8-flash", Vendor: "aliyun-bailian", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 800_000, OutputMicroPerMTok: 2_700_000,
			CacheReadMicroPerMTok: 800_000, CacheWriteMicroPerMTok: 800_000,
			EffectiveAt: catalogEffective},

		PriceBook{Model: "doubao-seed-evolving", Vendor: "volcengine-ark", Tier: TierFlat, Version: 1,
			InputMicroPerMTok: 6_000_000, OutputMicroPerMTok: 30_000_000,
			CacheReadMicroPerMTok: 1_200_000, CacheWriteMicroPerMTok: 0,
			EffectiveAt: catalogEffective},
	)
	// E2E-only vendor (H8): its prices are seeded only when the vendor was
	// injected through MP_EXTRA_VENDORS. The production whitelist does not
	// contain tipig, so a default deployment writes exactly the four DeepSeek
	// rows and no unofficial model ever gets the official price list.
	if _, ok := VendorByID(seedVendorTipig); ok {
		// tipig gateway serves the same DeepSeek V4.1/V4 Pro models under their
		// upstream model ids; the official price list applies (§3.2/§7 E2E).
		books = append(books,
			PriceBook{Model: "deepseek-v4.1-flash-1017", Vendor: seedVendorTipig, Tier: TierFlat, Version: 1,
				InputMicroPerMTok: 1_000_000, OutputMicroPerMTok: 4_000_000,
				CacheReadMicroPerMTok: 20_000, CacheWriteMicroPerMTok: 1_000_000,
				EffectiveAt: tokenEffective},
			PriceBook{Model: "deepseek-v4-pro-0813", Vendor: seedVendorTipig, Tier: TierFlat, Version: 1,
				InputMicroPerMTok: 4_500_000, OutputMicroPerMTok: 13_500_000,
				CacheReadMicroPerMTok: 150_000, CacheWriteMicroPerMTok: 4_500_000,
				EffectiveAt: tokenEffective},
		)
	}
	// OFFICIAL-CHANNELS §3: cloud platforms that host the same model are
	// priced at the model's original vendor price, so the square never shows
	// two different baselines for one model id. Copy the DeepSeek books under
	// each official cloud channel instead of inventing platform prices.
	officialBooks := make([]PriceBook, 0, 4)
	for _, b := range books {
		if b.Vendor == SeedVendorDeepSeek {
			officialBooks = append(officialBooks, b)
		}
	}
	for _, vendorID := range []string{"volcengine-ark", "aliyun-bailian"} {
		if _, ok := VendorByID(vendorID); !ok {
			continue
		}
		for _, b := range officialBooks {
			copied := b
			copied.Vendor = vendorID
			books = append(books, copied)
		}
	}
	for i := range books {
		b := &books[i]
		var cnt int64
		if err := db.Model(&PriceBook{}).
			Where("model = ? AND vendor = ? AND tier = ? AND version = ?", b.Model, b.Vendor, b.Tier, b.Version).
			Count(&cnt).Error; err != nil {
			return fmt.Errorf("marketplace: seed prices: %w", err)
		}
		if cnt > 0 {
			continue
		}
		if err := db.Create(b).Error; err != nil {
			return fmt.Errorf("marketplace: seed prices: %w", err)
		}
	}

	perCall := []PerCallPrice{
		{Vendor: SeedVendorDeepSeek, Item: ToolItemSearch, Version: 1,
			UnitPriceMicro: 5_000, EffectiveAt: toolEffective},
		{Vendor: SeedVendorDeepSeek, Item: ToolItemFetch, Version: 1,
			UnitPriceMicro: 5_000, EffectiveAt: toolEffective},
	}
	for i := range perCall {
		p := &perCall[i]
		var cnt int64
		if err := db.Model(&PerCallPrice{}).
			Where("vendor = ? AND item = ? AND version = ?", p.Vendor, p.Item, p.Version).
			Count(&cnt).Error; err != nil {
			return fmt.Errorf("marketplace: seed prices: %w", err)
		}
		if cnt > 0 {
			continue
		}
		if err := db.Create(p).Error; err != nil {
			return fmt.Errorf("marketplace: seed prices: %w", err)
		}
	}
	return nil
}
