package marketplace

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// SeedVendorDeepSeek is the vendor id of the DeepSeek official channel, the
// first whitelisted vendor (§3.5).
const SeedVendorDeepSeek = "deepseek"

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
		{Model: "deepseek-flash", Vendor: SeedVendorDeepSeek, Tier: TierOffpeak, Version: 1,
			InputMicroPerMTok: 1_000_000, OutputMicroPerMTok: 4_000_000,
			CacheReadMicroPerMTok: 20_000, CacheWriteMicroPerMTok: 1_000_000,
			EffectiveAt: tokenEffective},
		{Model: "deepseek-flash", Vendor: SeedVendorDeepSeek, Tier: TierPeak, Version: 1,
			InputMicroPerMTok: 2_000_000, OutputMicroPerMTok: 8_000_000,
			CacheReadMicroPerMTok: 40_000, CacheWriteMicroPerMTok: 2_000_000,
			EffectiveAt: tokenEffective},
		{Model: "deepseek-v4-pro", Vendor: SeedVendorDeepSeek, Tier: TierOffpeak, Version: 1,
			InputMicroPerMTok: 4_500_000, OutputMicroPerMTok: 13_500_000,
			CacheReadMicroPerMTok: 150_000, CacheWriteMicroPerMTok: 4_500_000,
			EffectiveAt: tokenEffective},
		{Model: "deepseek-v4-pro", Vendor: SeedVendorDeepSeek, Tier: TierPeak, Version: 1,
			InputMicroPerMTok: 9_000_000, OutputMicroPerMTok: 27_000_000,
			CacheReadMicroPerMTok: 300_000, CacheWriteMicroPerMTok: 9_000_000,
			EffectiveAt: tokenEffective},
		// tipig gateway serves the same DeepSeek V4.1/V4 Pro models under their
		// upstream model ids; the official price list applies (§3.2/§7 E2E).
		{Model: "deepseek-v4.1-flash-1017", Vendor: "tipig", Tier: TierOffpeak, Version: 1,
			InputMicroPerMTok: 1_000_000, OutputMicroPerMTok: 4_000_000,
			CacheReadMicroPerMTok: 20_000, CacheWriteMicroPerMTok: 1_000_000,
			EffectiveAt: tokenEffective},
		{Model: "deepseek-v4.1-flash-1017", Vendor: "tipig", Tier: TierPeak, Version: 1,
			InputMicroPerMTok: 2_000_000, OutputMicroPerMTok: 8_000_000,
			CacheReadMicroPerMTok: 40_000, CacheWriteMicroPerMTok: 2_000_000,
			EffectiveAt: tokenEffective},
		{Model: "deepseek-v4-pro-0813", Vendor: "tipig", Tier: TierOffpeak, Version: 1,
			InputMicroPerMTok: 4_500_000, OutputMicroPerMTok: 13_500_000,
			CacheReadMicroPerMTok: 150_000, CacheWriteMicroPerMTok: 4_500_000,
			EffectiveAt: tokenEffective},
		{Model: "deepseek-v4-pro-0813", Vendor: "tipig", Tier: TierPeak, Version: 1,
			InputMicroPerMTok: 9_000_000, OutputMicroPerMTok: 27_000_000,
			CacheReadMicroPerMTok: 300_000, CacheWriteMicroPerMTok: 9_000_000,
			EffectiveAt: tokenEffective},
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
