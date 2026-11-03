package marketplace

// plaza_pricing.go — 广场价目 → new-api ratio_setting 同步.
//
// 广场 (plaza) 请求走 new-api 原生渠道，实际扣费完全由 new-api 的
// ratio_setting（ModelRatio / CompletionRatio / CacheRatio）决定。展价位却来自
// 市场价目 (price book)，两套数字必须一致，因此这里把每个上架广场的模型用
// quoteTierPrices 的买家价换算成 new-api 倍率并写入 options 表，使其成为广场
// 唯一的计价来源。

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

// ErrPlazaPriceRequired is returned when an operator tries to put a model on
// the plaza while its price book has no input and no output price. The message
// is shown verbatim in the admin console.
var ErrPlazaPriceRequired = errors.New("请先配置模型池基价再上架广场")

// quoteOneMillionTokens returns quoteTierPrices' overall debit at
// multiplier 1000 (official price), which is the 微元/百万 tokens base price
// the plaza charges at.
func plazaBasePrices(db *gorm.DB, cal *Calendar, row ModelPool, at time.Time) (squareTierPrices, error) {
	vendor := row.OriginVendor
	if vendor == "" {
		vendor = originVendorOf(row)
	}
	return quoteTierPrices(db, cal, row.Name, vendor, vendor, 1000, 1000, at)
}

// plazaModelRatio converts the input base price (微元/百万 tokens) into a
// new-api ModelRatio. new-api bills one input token as
// ModelRatio × QuotaPerUnit / 500000 quota, which the marketplace treats as
// 微元 1:1 (billing.go). Solving for the per-million-token price:
//
//	ModelRatio = inputMicro / 1e6 × 500000 / QuotaPerUnit
//
// With the default QuotaPerUnit (500000) this is inputMicro / 1e6, so
// ¥1.0/M (1_000_000 micro) → 1.0 and ¥8/M (8_000_000 micro) → 8.
func plazaModelRatio(inputMicroPerMTok int64) float64 {
	return float64(inputMicroPerMTok) / 1_000_000 * 500_000 / common.QuotaPerUnit
}

// plazaRatioSet is the new-api ratio tuple one plaza model is charged with.
type plazaRatioSet struct {
	ModelRatio      float64
	CompletionRatio float64
	CacheRatio      float64
}

func buildPlazaRatioSet(prices squareTierPrices) plazaRatioSet {
	modelRatio := plazaModelRatio(prices.InputMicroPerMTok)
	set := plazaRatioSet{
		ModelRatio:      modelRatio,
		CompletionRatio: 1,
		CacheRatio:      ratio_setting.DefaultCacheRatio,
	}
	if prices.InputMicroPerMTok > 0 {
		set.CompletionRatio = float64(prices.OutputMicroPerMTok) / float64(prices.InputMicroPerMTok)
		set.CacheRatio = float64(prices.CacheReadMicroPerMTok) / float64(prices.InputMicroPerMTok)
	}
	return set
}

// plazaPriceable reports whether a price book covers the model with a
// non-zero input or output price. A model without one cannot be listed on the
// plaza.
func plazaPriceable(prices squareTierPrices) bool {
	return prices.InputMicroPerMTok > 0 || prices.OutputMicroPerMTok > 0
}

// plazaRatiosFor computes the ratio tuple of one plaza model right now.
// ErrNoPrice is returned when the price book has no covering row.
func plazaRatiosFor(db *gorm.DB, cal *Calendar, row ModelPool, at time.Time) (plazaRatioSet, bool, error) {
	prices, err := plazaBasePrices(db, cal, row, at)
	if err != nil {
		return plazaRatioSet{}, false, err
	}
	if !plazaPriceable(prices) {
		return plazaRatioSet{}, false, nil
	}
	return buildPlazaRatioSet(prices), true, nil
}

// SyncPlazaModelRatios rewrites the new-api ratio maps so every active plaza
// model is charged exactly its price-book price, and models no longer on the
// plaza lose the override. Only plaza models are touched. The result is
// persisted through model.UpdateModelPricingOptions, which keeps the in-memory
// ratio maps and the options table consistent.
func SyncPlazaModelRatios(db *gorm.DB, cal *Calendar) error {
	// The ratio maps are persisted through model.UpdateModelPricingOptions,
	// which writes to the process-wide model.DB. Production sets model.DB (via
	// InitDB) before marketplace.Init runs; when it is nil there is nothing to
	// persist to, so skip instead of dereferencing a nil handle.
	if db == nil || model.DB == nil {
		return nil
	}
	if cal == nil {
		_, cal = relayState()
	}
	var rows []ModelPool
	if err := db.Where("status = ? AND enable_plaza = ?", PoolStatusActive, true).Find(&rows).Error; err != nil {
		return err
	}

	now := time.Now()
	targets := make(map[string]plazaRatioSet, len(rows))
	candidates := make([]string, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, row.Name)
		set, ok, err := plazaRatiosFor(db, cal, row, now)
		if err != nil || !ok {
			// A plaza model without a price cannot be listed; leave its ratios
			// alone so a pricing mistake never silently zeroes a charge.
			continue
		}
		targets[row.Name] = set
	}

	return model.UpdateModelPricingOptions(plazaRatioOptions(targets, candidates))
}

// plazaRatioOptions builds the option-map overrides that overlay the plaza
// ratio tuples on the current maps. Non-plaza entries are preserved verbatim;
// plaza candidates without a usable price are removed so a model taken off the
// plaza falls back to the operator's own configuration. Models charged with a
// tiered expression cannot be charged by ratio, so their tiered mode is
// cleared to make the synced ratio effective.
func plazaRatioOptions(targets map[string]plazaRatioSet, plazaModels []string) map[string]string {
	modelRatio := ratio_setting.GetModelRatioCopy()
	completionRatio := ratio_setting.GetCompletionRatioCopy()
	cacheRatio := ratio_setting.GetCacheRatioCopy()
	billingMode := billing_setting.GetBillingModeCopy()
	billingExpr := billing_setting.GetBillingExprCopy()

	for _, name := range plazaModels {
		if _, ok := targets[name]; ok {
			continue
		}
		delete(modelRatio, name)
		delete(completionRatio, name)
		delete(cacheRatio, name)
	}
	for name, set := range targets {
		modelRatio[name] = set.ModelRatio
		completionRatio[name] = set.CompletionRatio
		cacheRatio[name] = set.CacheRatio
		billingMode[name] = billing_setting.BillingModeRatio
		delete(billingExpr, name)
	}

	return map[string]string{
		"ModelRatio":                   mustJSON(modelRatio),
		"CompletionRatio":              mustJSON(completionRatio),
		"CacheRatio":                   mustJSON(cacheRatio),
		"billing_setting.billing_mode": mustJSON(billingMode),
		"billing_setting.billing_expr": mustJSON(billingExpr),
	}
}

func mustJSON[T any](values map[string]T) string {
	encoded, err := common.Marshal(values)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// syncPlazaModelRatiosFor re-syncs a single plaza model after an admin edit
// (price change, plaza toggle, pool status change). A model that is not (or no
// longer) an active plaza model is re-synced together with every other plaza
// model so its stale override is dropped.
func syncPlazaModelRatiosFor(db *gorm.DB, modelName string) {
	if db == nil {
		return
	}
	var row ModelPool
	err := db.Where("name = ?", strings.TrimSpace(modelName)).Take(&row).Error
	if err == nil && row.Status == PoolStatusActive && row.EnablePlaza {
		_, cal := relayState()
		set, ok, qerr := plazaRatiosFor(db, cal, row, time.Now())
		if qerr == nil && ok {
			if err := model.UpdateModelPricingOptions(plazaRatioOptions(map[string]plazaRatioSet{row.Name: set}, []string{row.Name})); err != nil {
				common.SysError(common.LogText("marketplace: sync plaza ratio for %s: %s", row.Name, err.Error()))
			}
			return
		}
	}
	// Not (or no longer) a priceable plaza model: refresh every plaza model so
	// this one's stale override is dropped.
	_, cal := relayState()
	if err := SyncPlazaModelRatios(db, cal); err != nil {
		common.SysError(common.LogText("marketplace: sync plaza ratios: %s", err.Error()))
	}
}

// ensurePlazaPriceable rejects enabling the plaza for a model whose price book
// has neither an input nor an output price.
func ensurePlazaPriceable(db *gorm.DB, row ModelPool) error {
	_, cal := relayState()
	prices, err := plazaBasePrices(db, cal, row, time.Now())
	if err != nil {
		if errors.Is(err, ErrNoPrice) {
			return ErrPlazaPriceRequired
		}
		return fmt.Errorf("failed to read model price: %w", err)
	}
	if !plazaPriceable(prices) || math.IsNaN(plazaModelRatio(prices.InputMicroPerMTok)) {
		return ErrPlazaPriceRequired
	}
	return nil
}
