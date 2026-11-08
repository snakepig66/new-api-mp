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
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
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
	// retries; only ever raise the global value (只升不降). The request-policy
	// snapshot is rebuilt here because options were loaded before this point,
	// so /api/channel/ops must not keep reporting the pre-floor value.
	if err := model.EnsureMinimumRetryTimes(2); err != nil {
		common.SysError(common.LogText("marketplace: retry floor: %s", err.Error()))
	}
	// Start the package-level relay singletons (Selector, holiday calendar)
	// used by the §4.4 seam hooks.
	initRelayState(db)
	// 广场价目 is the plaza's only price source: rewrite new-api's ratio maps
	// from the price book at startup so display and debit agree (§3.7).
	if err := SyncPlazaModelRatios(db, defaultCalendar); err != nil {
		common.SysError(common.LogText("marketplace: sync plaza ratios: %s", err.Error()))
	}
	// Background jobs (T+7 maturity, canary promotion, patrol, calendar reload).
	// The stop channel is never closed: the jobs exit with the process.
	StartJobs(db, make(chan struct{}))

	// Register hook to sync native Plaza requests into mp_requests
	model.RegisterConsumeLogHook(func(c *gin.Context, log *model.Log, params model.RecordConsumeLogParams) {
		if c == nil || c.GetString("mp_relay_session") != "" || log == nil || log.RequestId == "" {
			return
		}
		rdb, _ := relayState()
		if rdb == nil {
			return
		}
		cacheRead, cacheWrite := plazaLogCacheTokens(log.Other)
		input := max(int64(log.PromptTokens)-cacheRead-cacheWrite, 0)
		reqRow := Request{
			RequestID:        log.RequestId,
			BuyerUserID:      int64(log.UserId),
			BuyerTokenID:     int64(log.TokenId),
			SellerID:         0,
			ListingID:        0,
			ChannelID:        int64(log.ChannelId),
			Model:            log.ModelName,
			UpstreamModel:    "",
			Source:           RequestSourcePlaza,
			Tier:             "standard",
			Status:           RequestSuccess,
			InputTokens:      input,
			OutputTokens:     int64(log.CompletionTokens),
			CacheReadTokens:  cacheRead,
			CacheWriteTokens: cacheWrite,
			TotalDebitMicro:  int64(log.Quota),
			TokenDebitMicro:  int64(log.Quota),
			LatencyMs:        int64(log.UseTime * 1000),
			FirstTokenMs:     plazaLogFirstTokenMs(log),
			IsStream:         log.IsStream,
			TokenGroup:       log.Group,
			CreatedAt:        time.Unix(log.CreatedAt, 0),
		}
		_ = rdb.Create(&reqRow).Error
	})

	common.SysLog("marketplace enabled: migrations applied")
	return nil
}

// plazaLogFirstTokenMs reads the first-response latency new-api stores as
// "frt" (milliseconds) in a consume log's Other JSON. It is only meaningful
// for streamed requests; a non-streaming request keeps 0, matching the
// marketplace convention for mp_requests.first_token_ms.
func plazaLogFirstTokenMs(log *model.Log) int64 {
	if log == nil || !log.IsStream || log.Other == "" {
		return 0
	}
	var m map[string]any
	if err := common.UnmarshalJsonStr(log.Other, &m); err != nil {
		return 0
	}
	switch v := m["frt"].(type) {
	case float64:
		if v > 0 {
			return int64(v)
		}
	case int64:
		if v > 0 {
			return v
		}
	case int:
		if v > 0 {
			return int64(v)
		}
	}
	return 0
}

// plazaLogCacheTokens reads the cache counters new-api stores in a consume
// log's Other JSON (cache_tokens / cache_creation_tokens). new-api's
// PromptTokens already includes them, while mp_requests.input_tokens follows
// the marketplace convention of excluding cache (see record.go), so the hook
// subtracts them back out.
func plazaLogCacheTokens(other string) (cacheRead, cacheWrite int64) {
	if other == "" {
		return 0, 0
	}
	var m map[string]any
	if err := common.UnmarshalJsonStr(other, &m); err != nil {
		return 0, 0
	}
	num := func(k string) int64 {
		switch v := m[k].(type) {
		case float64:
			if v > 0 {
				return int64(v)
			}
		case int64:
			if v > 0 {
				return v
			}
		case int:
			if v > 0 {
				return int64(v)
			}
		}
		return 0
	}
	return num("cache_tokens"), num("cache_creation_tokens")
}
