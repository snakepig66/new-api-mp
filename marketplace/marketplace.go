// Package marketplace implements the LLM API marketplace on top of new-api.
//
// All marketplace logic lives in this package; upstream code is only touched
// at seam points marked with "// MP-HOOK". The whole module is inert unless
// the environment variable MP_ENABLED is set to "true", so a binary built
// from this tree behaves exactly like upstream when the marketplace is off.
package marketplace

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

var enabled bool

// Enabled reports whether the marketplace module is active.
func Enabled() bool {
	return enabled
}

// Init bootstraps the marketplace module: configuration, migrations and
// background jobs (added in later phases). It is a no-op unless
// MP_ENABLED=true. db is the primary database handle owned by model.InitDB;
// Init must not close or reconfigure it.
func Init(db *gorm.DB) error {
	enabled = os.Getenv("MP_ENABLED") == "true"
	if !enabled {
		return nil
	}
	// MP_KEY_SECRET is mandatory (§4.5): without it anonymous buyer codes
	// cannot be derived. Never include the secret itself in any error or log.
	secret := os.Getenv("MP_KEY_SECRET")
	if secret == "" {
		return ErrNoAnonSecret
	}
	SetAnonSecret(secret)
	// Extra vendors (e.g. test gateways) join the whitelist only via
	// MP_EXTRA_VENDORS; the built-in production whitelist stays official.
	if err := LoadExtraVendorsFromEnv(); err != nil {
		return fmt.Errorf("marketplace init: %w", err)
	}
	// Egress whitelist (§3.11/§4.5): MP_EGRESS_DOMAINS adds deployment hosts
	// on top of the pool-derived list — seller-defined custom channels extend
	// the pool at runtime, so the env no longer replaces the pool set; the
	// vendor-declared subdomain patterns (Bailian workspace domains) always
	// apply and are anchored by SetEgressPatterns.
	if domains := os.Getenv("MP_EGRESS_DOMAINS"); domains != "" {
		merged := VendorHosts()
		for _, d := range strings.Split(domains, ",") {
			if d = strings.TrimSpace(d); d != "" && !slices.Contains(merged, d) {
				merged = append(merged, d)
			}
		}
		SetEgressDomains(merged)
	} else {
		SetEgressDomains(VendorHosts())
	}
	if err := SetEgressPatterns(VendorHostPatterns()); err != nil {
		return fmt.Errorf("marketplace init: %w", err)
	}
	if err := Migrate(db); err != nil {
		return fmt.Errorf("marketplace init: %w", err)
	}
	if err := SeedPrices(db); err != nil {
		return fmt.Errorf("marketplace seed: %w", err)
	}
	// 官方渠道模型映射（OFFICIAL-CHANNELS §3）：平台标准模型 ID ↔ 各渠道
	// 上游模型名，后台可编辑；挂单只接受映射表里有且价目覆盖的组合。
	if err := MigrateChannelModelMap(db); err != nil {
		return fmt.Errorf("marketplace init: %w", err)
	}
	if err := SeedChannelModelMap(db); err != nil {
		return fmt.Errorf("marketplace seed: %w", err)
	}
	// 模型池 / 渠道池 (P1): mp_models / mp_channels / mp_channel_models, their
	// seed rows, and the legacy channel-map / price / listing migration.
	if err := MigrateModelChannelPool(db); err != nil {
		return fmt.Errorf("marketplace init: %w", err)
	}
	if err := SeedModelChannelPool(db); err != nil {
		return fmt.Errorf("marketplace seed: %w", err)
	}
	// §4.5: the §3.7 retry semantics (最多换 2 家) need at least two upstream
	// retries; only ever raise the global value (只升不降).
	if common.RetryTimes < 2 {
		common.RetryTimes = 2
	}
	// Start the package-level relay singletons (Selector, holiday calendar)
	// used by the §4.4 seam hooks.
	initRelayState(db)
	// Background jobs (T+7 maturity, canary promotion, patrol, calendar reload).
	// The stop channel is never closed: the jobs exit with the process.
	StartJobs(db, make(chan struct{}))
	common.SysLog("marketplace enabled: migrations applied")
	return nil
}
