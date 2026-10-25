package marketplace

// modelpool.go — 模型池 / 渠道池 (P1).
//
// Three tables describe what the marketplace can sell independently of who
// happens to be listed right now:
//
//	mp_models          standard models (one row per platform model id)
//	mp_channels        suppliers / channel definitions
//	mp_channel_models  one row = one sellable qualification: a standard model
//	                   served by a channel under an upstream model name
//
// The legacy tables mp_channel_model_map (vendor → upstream name) and
// mp_price_books stay untouched; SeedModelChannelPool copies their rows into
// the new pool once, idempotently.
//
// JSON columns (capabilities, aliases, protocols, deny_*, quota_window,
// differences) are stored as text. Helpers below parse them defensively: bad
// data yields an empty value rather than a panic, and no API response ever
// carries a key.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ModelPool is a standard model of the pool. Name is the platform model id
// buyers see (e.g. glm-5.3); aliases accept operator/upstream spellings.
type ModelPool struct {
	ID            int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	Name          string `gorm:"size:128;not null;uniqueIndex" json:"name"`
	OriginVendor  string `gorm:"size:64;not null;index" json:"origin_vendor"`
	ContextLength int    `gorm:"not null;default:0" json:"context_length"`
	Capabilities  string `gorm:"type:text;not null;default:''" json:"capabilities"`
	Aliases       string `gorm:"type:text;not null;default:''" json:"aliases"`
	VerifyPrompt  string `gorm:"type:text;not null;default:''" json:"verify_prompt"`
	Status        string `gorm:"size:32;not null;default:'active'" json:"status"` // active / retired

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (ModelPool) TableName() string { return "mp_models" }

// ChannelPool is a supplier definition. It mirrors the subset of Vendor the
// probe/relay paths need, so ChannelPoolAsVendor can hand a pool row to code
// written against the static vendor whitelist.
type ChannelPool struct {
	ID                  int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	Slug                string     `gorm:"size:64;not null;uniqueIndex" json:"slug"`
	Name                string     `gorm:"size:128;not null" json:"name"`
	Kind                string     `gorm:"size:32;not null;index" json:"kind"` // official / plan / third_party
	Protocols           string     `gorm:"type:text;not null;default:''" json:"protocols"`
	AllowHostPattern    string     `gorm:"size:256;not null;default:''" json:"allow_host_pattern"`
	KeyPattern          string     `gorm:"size:256;not null;default:''" json:"key_pattern"`
	DenyPaths           string     `gorm:"type:text;not null;default:''" json:"deny_paths"`
	DenyKeyPrefixes     string     `gorm:"type:text;not null;default:''" json:"deny_key_prefixes"`
	Adaptor             string     `gorm:"size:32;not null;default:'openai'" json:"adaptor"`
	StripV1Prefix       bool       `json:"strip_v1_prefix"`
	PassthroughBody     bool       `json:"passthrough_body"`
	QuotaWindow         string     `gorm:"type:text;not null;default:''" json:"quota_window"`
	ExhaustionSignature string     `gorm:"type:text;not null;default:''" json:"exhaustion_signature"`
	TermsURL            string     `gorm:"size:512;not null;default:''" json:"terms_url"`
	TermsCheckedAt      *time.Time `json:"terms_checked_at"`
	Status              string     `gorm:"size:32;not null;default:'active'" json:"status"` // active / disabled

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (ChannelPool) TableName() string { return "mp_channels" }

// ChannelModel is one sellable qualification: standard model Model is served
// by channel ChannelSlug under the upstream name UpstreamModel.
type ChannelModel struct {
	ID               int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	ChannelSlug      string     `gorm:"size:64;not null;uniqueIndex:idx_mp_cm_channel_model;uniqueIndex:idx_mp_cm_channel_upstream" json:"channel_slug"`
	UpstreamModel    string     `gorm:"size:128;not null;uniqueIndex:idx_mp_cm_channel_upstream" json:"upstream_model"`
	Model            string     `gorm:"size:128;not null;uniqueIndex:idx_mp_cm_channel_model;index" json:"model"`
	CoefficientMilli int64      `gorm:"not null;default:1000" json:"coefficient_milli"` // 1000 = 官方价 ×1.0
	Differences      string     `gorm:"type:text;not null;default:''" json:"differences"`
	Verified         bool       `gorm:"not null;default:false;index" json:"verified"`
	VerifiedAt       *time.Time `json:"verified_at"`
	FingerprintRatio float64    `gorm:"not null;default:0" json:"fingerprint_ratio"`
	Status           string     `gorm:"size:32;not null;default:'active'" json:"status"` // active / paused / banned

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (ChannelModel) TableName() string { return "mp_channel_models" }

// Pool status values.
const (
	PoolStatusActive   = "active"
	PoolStatusRetired  = "retired"
	PoolStatusDisabled = "disabled"
	PoolStatusPaused   = "paused"
	PoolStatusBanned   = "banned"
)

// Channel kinds.
const (
	ChannelKindOfficial   = "official"
	ChannelKindPlan       = "plan"
	ChannelKindThirdParty = "third_party"
)

// Pool-only semantic errors. Their text is short English and never echoes
// anything sensitive; the admin API forwards them verbatim.
var (
	ErrModelPoolNameRequired        = errors.New("model name is required")
	ErrModelPoolStatusInvalid       = errors.New("status must be active or retired")
	ErrModelPoolInUse               = errors.New("model is referenced by a channel model")
	ErrModelPoolNotFound            = errors.New("model not found")
	ErrChannelPoolSlugRequired      = errors.New("channel slug is required")
	ErrChannelPoolKindInvalid       = errors.New("kind must be official, plan or third_party")
	ErrChannelPoolKeyPatternInvalid = errors.New("key_pattern is not a valid regular expression")
	ErrChannelPoolStatusInvalid     = errors.New("status must be active or disabled")
	ErrChannelPoolInUse             = errors.New("channel is referenced by a listing")
	ErrChannelPoolNotFound          = errors.New("channel not found")
	ErrChannelModelFieldsRequired   = errors.New("channel_slug, upstream_model and model are required")
	ErrChannelModelCoefficient      = errors.New("coefficient_milli must be between 1 and 10000")
	ErrChannelModelStatusInvalid    = errors.New("status must be active, paused or banned")
	ErrChannelModelDifferences      = errors.New("differences must be a JSON object")
	ErrChannelModelConflict         = errors.New("channel model mapping conflicts with an existing row")
	ErrChannelModelNotFound         = errors.New("channel model not found")
)

// MigrateModelChannelPool creates or upgrades the three pool tables. It is
// idempotent and safe to call on every startup.
func MigrateModelChannelPool(db *gorm.DB) error {
	if db == nil {
		return errors.New("marketplace: migrate model channel pool: nil database")
	}
	if err := db.AutoMigrate(&ModelPool{}, &ChannelPool{}, &ChannelModel{}); err != nil {
		return fmt.Errorf("marketplace: migrate model channel pool: %w", err)
	}
	return nil
}

// poolVerifyPrompt is the shared fingerprint-verification prompt. The exact
// wording matters for cross-channel comparison, so every seeded model uses the
// same fixed text (Chinese + English, ~200 chars).
const poolVerifyPrompt = "请逐字复述下面这段文字，不要改写、不要翻译、不要省略标点：The quick brown fox jumps over the lazy dog, " +
	"and 0123456789 stays exactly as written. 这段话同时包含英文常用字母与中文标点，用于比对不同渠道的分词与输出指纹。" +
	"请保持原有空格与换行，并在结尾追加一行：模型池 model pool / 渠道池 channel pool 稳定性校验结束。"

// modelPoolSeeds is the initial standard-model pool.
var modelPoolSeeds = []ModelPool{
	{Name: "deepseek-flash", OriginVendor: "deepseek", ContextLength: 131072,
		Capabilities: `["text","tools","cache"]`, Aliases: `["DeepSeek-Flash"]`,
		VerifyPrompt: poolVerifyPrompt, Status: PoolStatusActive},
	{Name: "deepseek-v4-pro", OriginVendor: "deepseek", ContextLength: 131072,
		Capabilities: `["text","tools","cache"]`, Aliases: `[]`,
		VerifyPrompt: poolVerifyPrompt, Status: PoolStatusActive},
	{Name: "kimi-k2", OriginVendor: "moonshot", ContextLength: 262144,
		Capabilities: `["text","tools"]`, Aliases: `[]`,
		VerifyPrompt: poolVerifyPrompt, Status: PoolStatusActive},
	{Name: "glm-5.3", OriginVendor: "zhipu", ContextLength: 131072,
		Capabilities: `["text","tools","cache"]`, Aliases: `["glm5.3","GLM-5.3","zhipu/glm-5.3"]`,
		VerifyPrompt: poolVerifyPrompt, Status: PoolStatusActive},
	{Name: "glm-4.6", OriginVendor: "zhipu", ContextLength: 131072,
		Capabilities: `["text","tools"]`, Aliases: `[]`,
		VerifyPrompt: poolVerifyPrompt, Status: PoolStatusActive},
	{Name: "minimax-m2", OriginVendor: "minimax", ContextLength: 204800,
		Capabilities: `["text","tools"]`, Aliases: `[]`,
		VerifyPrompt: poolVerifyPrompt, Status: PoolStatusActive},
}

// poolQuotaWindow is the shared plan quota window (5 hours, reset in Shanghai).
const poolQuotaWindow = `{"period_seconds":18000,"reset_timezone":"Asia/Shanghai"}`

// channelPoolSeeds is the initial channel pool: official channels plus the two
// coding plans whose upstream model names are already known but whose
// exhaustion behaviour is still unverified (P6 待实测).
var channelPoolSeeds = []ChannelPool{
	{
		Slug: "deepseek", Name: "DeepSeek", Kind: ChannelKindOfficial,
		Protocols:       `{"openai":"https://api.deepseek.com","anthropic":"https://api.deepseek.com/anthropic"}`,
		Adaptor:         VendorAdaptorDeepSeek,
		PassthroughBody: true,
		Status:          PoolStatusActive,
	},
	{
		Slug: "volcengine-ark", Name: "火山方舟", Kind: ChannelKindOfficial,
		Protocols:     `{"openai":"https://ark.cn-beijing.volces.com/api/v3","anthropic":"https://ark.cn-beijing.volces.com/api/compatible"}`,
		DenyPaths:     `["/api/coding","/api/plan"]`,
		Adaptor:       VendorAdaptorOpenAI,
		StripV1Prefix: true,
		Status:        PoolStatusActive,
	},
	{
		Slug: "aliyun-bailian", Name: "阿里云百炼", Kind: ChannelKindOfficial,
		Protocols:        `{"openai":"https://dashscope.aliyuncs.com/compatible-mode/v1","anthropic":"https://dashscope.aliyuncs.com/apps/anthropic"}`,
		AllowHostPattern: `^[a-z0-9-]+\.cn-beijing\.maas\.aliyuncs\.com$`,
		DenyKeyPrefixes:  `["sk-sp-","sk-ws-"]`,
		Adaptor:          VendorAdaptorOpenAI,
		StripV1Prefix:    true,
		Status:           PoolStatusActive,
	},
	{
		Slug: "moonshot", Name: "Kimi（月之暗面）", Kind: ChannelKindOfficial,
		Protocols:     `{"openai":"https://api.moonshot.cn/v1","anthropic":"https://api.moonshot.cn/anthropic"}`,
		Adaptor:       VendorAdaptorOpenAI,
		StripV1Prefix: true,
		Status:        PoolStatusActive,
	},
	{
		Slug: "zhipu", Name: "智谱 BigModel", Kind: ChannelKindOfficial,
		Protocols:     `{"openai":"https://open.bigmodel.cn/api/paas/v4","anthropic":"https://open.bigmodel.cn/api/anthropic"}`,
		DenyPaths:     `["/api/coding/paas/v4"]`,
		Adaptor:       VendorAdaptorOpenAI,
		StripV1Prefix: true,
		Status:        PoolStatusActive,
	},
	{
		Slug: "minimax", Name: "MiniMax", Kind: ChannelKindOfficial,
		Protocols:     `{"openai":"https://api.minimaxi.com/v1","anthropic":"https://api.minimaxi.com/anthropic"}`,
		Adaptor:       VendorAdaptorOpenAI,
		StripV1Prefix: true,
		Status:        PoolStatusActive,
	},
	{
		Slug: "volcengine-ark-coding-plan", Name: "火山方舟 Coding Plan", Kind: ChannelKindPlan,
		Protocols:           `{"openai":"https://ark.cn-beijing.volces.com/api/coding/v3"}`,
		KeyPattern:          `^.+$`,
		QuotaWindow:         poolQuotaWindow,
		ExhaustionSignature: "coding plan",
		Adaptor:             VendorAdaptorOpenAI,
		Status:              PoolStatusActive,
	},
	{
		Slug: "aliyun-bailian-coding-plan", Name: "百炼 Coding Plan", Kind: ChannelKindPlan,
		Protocols:           `{"openai":"https://coding.dashscope.aliyuncs.com/v1"}`,
		KeyPattern:          `^sk-(sp|ws)-`,
		QuotaWindow:         poolQuotaWindow,
		ExhaustionSignature: "token plan",
		Adaptor:             VendorAdaptorOpenAI,
		Status:              PoolStatusActive,
	},
}

// poolChannelModelSeeds is the initial sellable-qualification seed.
var poolChannelModelSeeds = []ChannelModel{
	{ChannelSlug: "deepseek", UpstreamModel: "deepseek-flash", Model: "deepseek-flash",
		CoefficientMilli: 1000, Verified: true, FingerprintRatio: 1.0, Status: PoolStatusActive},
	{ChannelSlug: "deepseek", UpstreamModel: "deepseek-v4-pro", Model: "deepseek-v4-pro",
		CoefficientMilli: 1000, Verified: true, FingerprintRatio: 1.0, Status: PoolStatusActive},

	{ChannelSlug: "volcengine-ark", UpstreamModel: "deepseek-v3-2-flash", Model: "deepseek-flash",
		CoefficientMilli: 1000, Differences: `{"host":"volcengine-ark"}`, Verified: true, FingerprintRatio: 1.0, Status: PoolStatusActive},
	{ChannelSlug: "volcengine-ark", UpstreamModel: "deepseek-v3-2", Model: "deepseek-v4-pro",
		CoefficientMilli: 1000, Differences: `{"host":"volcengine-ark"}`, Verified: true, FingerprintRatio: 1.0, Status: PoolStatusActive},

	{ChannelSlug: "aliyun-bailian", UpstreamModel: "deepseek-v3.2-flash", Model: "deepseek-flash",
		CoefficientMilli: 1000, Verified: true, FingerprintRatio: 1.0, Status: PoolStatusActive},
	{ChannelSlug: "aliyun-bailian", UpstreamModel: "deepseek-v3.2", Model: "deepseek-v4-pro",
		CoefficientMilli: 1000, Verified: true, FingerprintRatio: 1.0, Status: PoolStatusActive},

	{ChannelSlug: "moonshot", UpstreamModel: "kimi-k2", Model: "kimi-k2",
		CoefficientMilli: 1000, Verified: true, FingerprintRatio: 1.0, Status: PoolStatusActive},

	{ChannelSlug: "zhipu", UpstreamModel: "glm-5.3", Model: "glm-5.3",
		CoefficientMilli: 1000, Verified: true, FingerprintRatio: 1.0, Status: PoolStatusActive},
	{ChannelSlug: "zhipu", UpstreamModel: "glm-4.6", Model: "glm-4.6",
		CoefficientMilli: 1000, Verified: true, FingerprintRatio: 1.0, Status: PoolStatusActive},

	{ChannelSlug: "minimax", UpstreamModel: "minimax-m2", Model: "minimax-m2",
		CoefficientMilli: 1000, Verified: true, FingerprintRatio: 1.0, Status: PoolStatusActive},

	{ChannelSlug: "volcengine-ark-coding-plan", UpstreamModel: "deepseek-v3-2", Model: "deepseek-v4-pro",
		CoefficientMilli: 500, Differences: `{"plan_only":true,"context_length":64000}`,
		Verified: false, Status: PoolStatusActive},
	{ChannelSlug: "aliyun-bailian-coding-plan", UpstreamModel: "deepseek-v3.2", Model: "deepseek-v4-pro",
		CoefficientMilli: 500, Differences: `{"plan_only":true,"context_length":64000}`,
		Verified: false, Status: PoolStatusActive},
}

// SeedModelChannelPool idempotently writes the pool seeds and migrates legacy
// data. Every step is safe to repeat:
//
//  1. seed models by Name, channels by Slug, qualifications by
//     (ChannelSlug, UpstreamModel);
//  2. copy mp_channel_model_map rows into mp_channel_models;
//  3. drop price-book duplicates under non-origin vendors when the price
//     4-tuple is identical to the origin vendor row;
//  4. pause listings whose channel/model mapping is missing after migration and
//     record one type="migration" probe per listing.
func SeedModelChannelPool(db *gorm.DB) error {
	if db == nil {
		return errors.New("marketplace: seed model channel pool: nil database")
	}
	for i := range modelPoolSeeds {
		seed := modelPoolSeeds[i]
		var cnt int64
		if err := db.Model(&ModelPool{}).Where("name = ?", seed.Name).Count(&cnt).Error; err != nil {
			return fmt.Errorf("marketplace: seed model pool: %w", err)
		}
		if cnt > 0 {
			continue
		}
		row := seed
		if err := db.Create(&row).Error; err != nil {
			return fmt.Errorf("marketplace: seed model pool: %w", err)
		}
	}

	for i := range channelPoolSeeds {
		seed := channelPoolSeeds[i]
		var cnt int64
		if err := db.Model(&ChannelPool{}).Where("slug = ?", seed.Slug).Count(&cnt).Error; err != nil {
			return fmt.Errorf("marketplace: seed channel pool: %w", err)
		}
		if cnt > 0 {
			continue
		}
		row := seed
		if err := db.Create(&row).Error; err != nil {
			return fmt.Errorf("marketplace: seed channel pool: %w", err)
		}
	}

	for i := range poolChannelModelSeeds {
		seed := poolChannelModelSeeds[i]
		var cnt int64
		// Either unique key being present means this slot is already taken.
		if err := db.Model(&ChannelModel{}).
			Where("(channel_slug = ? AND upstream_model = ?) OR (channel_slug = ? AND model = ?)",
				seed.ChannelSlug, seed.UpstreamModel, seed.ChannelSlug, seed.Model).
			Count(&cnt).Error; err != nil {
			return fmt.Errorf("marketplace: seed channel model pool: %w", err)
		}
		if cnt > 0 {
			continue
		}
		row := seed
		if err := db.Create(&row).Error; err != nil {
			return fmt.Errorf("marketplace: seed channel model pool: %w", err)
		}
	}

	if err := migrateLegacyChannelModelMap(db); err != nil {
		return err
	}
	if err := migratePoolPriceDuplicates(db); err != nil {
		return err
	}
	return migratePoolListings(db)
}

// migrateLegacyChannelModelMap copies mp_channel_model_map into
// mp_channel_models. The legacy table is kept for rollback. Existing rows are
// skipped, so a second startup is a no-op.
func migrateLegacyChannelModelMap(db *gorm.DB) error {
	if !db.Migrator().HasTable("mp_channel_model_map") {
		return nil
	}
	var rows []ChannelModelMap
	if err := db.Table("mp_channel_model_map").Find(&rows).Error; err != nil {
		return fmt.Errorf("marketplace: migrate legacy channel model map: %w", err)
	}
	for _, old := range rows {
		slug := strings.TrimSpace(old.Vendor)
		model := strings.TrimSpace(old.Model)
		upstream := strings.TrimSpace(old.UpstreamModel)
		if slug == "" || model == "" || upstream == "" {
			continue
		}
		var cnt int64
		if err := db.Model(&ChannelModel{}).
			Where("(channel_slug = ? AND upstream_model = ?) OR (channel_slug = ? AND model = ?)",
				slug, upstream, slug, model).
			Count(&cnt).Error; err != nil {
			return fmt.Errorf("marketplace: migrate legacy channel model map: %w", err)
		}
		if cnt > 0 {
			continue
		}
		row := ChannelModel{
			ChannelSlug:      slug,
			UpstreamModel:    upstream,
			Model:            model,
			CoefficientMilli: 1000,
			Verified:         true,
			FingerprintRatio: 1.0,
			Status:           PoolStatusActive,
		}
		if err := db.Create(&row).Error; err != nil {
			return fmt.Errorf("marketplace: migrate legacy channel model map: %w", err)
		}
	}
	return nil
}

// migratePoolPriceDuplicates removes price-book rows of a standard model under
// vendors other than its origin vendor when the row is identical (tier,
// version and the four prices) to the origin vendor row. Non-identical rows are
// kept and logged: a deliberate price difference must not be lost silently.
func migratePoolPriceDuplicates(db *gorm.DB) error {
	if !db.Migrator().HasTable(&PriceBook{}) {
		return nil
	}
	for _, pool := range ModelPoolAll(db) {
		origin := strings.TrimSpace(pool.OriginVendor)
		if origin == "" {
			continue
		}
		var refs []PriceBook
		if err := db.Where("model = ? AND vendor = ?", pool.Name, origin).Find(&refs).Error; err != nil {
			return fmt.Errorf("marketplace: migrate price duplicates: %w", err)
		}
		if len(refs) == 0 {
			continue
		}
		var others []PriceBook
		if err := db.Where("model = ? AND vendor <> ?", pool.Name, origin).Find(&others).Error; err != nil {
			return fmt.Errorf("marketplace: migrate price duplicates: %w", err)
		}
		for _, other := range others {
			duplicate := false
			for _, ref := range refs {
				if ref.Tier == other.Tier && ref.Version == other.Version &&
					ref.InputMicroPerMTok == other.InputMicroPerMTok &&
					ref.OutputMicroPerMTok == other.OutputMicroPerMTok &&
					ref.CacheReadMicroPerMTok == other.CacheReadMicroPerMTok &&
					ref.CacheWriteMicroPerMTok == other.CacheWriteMicroPerMTok {
					duplicate = true
					break
				}
			}
			if duplicate {
				if err := db.Delete(&PriceBook{}, other.ID).Error; err != nil {
					return fmt.Errorf("marketplace: migrate price duplicates: %w", err)
				}
				continue
			}
			common.SysLog(fmt.Sprintf(
				"marketplace: price row for model %s vendor %s differs from origin vendor %s; keeping it",
				pool.Name, other.Vendor, origin))
		}
	}
	return nil
}

// poolMigrationFailReason is the probe detail written when a legacy listing
// loses its mapping during the pool migration.
const poolMigrationFailReason = "channel model mapping missing after pool migration; re-verify required"

// migratePoolListings pauses listings that cannot be served from the pool and
// records one migration probe per affected listing. Banned is terminal and is
// left untouched. Probe write failures never abort startup.
func migratePoolListings(db *gorm.DB) error {
	if !db.Migrator().HasTable(&Listing{}) {
		return nil
	}
	var listings []Listing
	if err := db.Select("id", "vendor", "models", "status").Find(&listings).Error; err != nil {
		return fmt.Errorf("marketplace: migrate pool listings: %w", err)
	}
	hasProbeTable := db.Migrator().HasTable(&Probe{})
	for i := range listings {
		listing := listings[i]
		if listing.Status == ListingBanned {
			continue
		}
		mapped := true
		if _, ok := ChannelPoolBySlug(db, listing.Vendor); !ok {
			mapped = false
		}
		if mapped {
			for _, model := range listing.Models {
				if _, ok := ChannelModelFor(db, listing.Vendor, model); !ok {
					mapped = false
					break
				}
			}
		}
		if mapped {
			continue
		}
		if err := db.Model(&Listing{}).Where("id = ?", listing.ID).
			Update("status", ListingPaused).Error; err != nil {
			return fmt.Errorf("marketplace: pause unmapped listing %d: %w", listing.ID, err)
		}
		if !hasProbeTable {
			common.SysLog(fmt.Sprintf(
				"marketplace: listing %d paused after pool migration; probe table unavailable", listing.ID))
			continue
		}
		var probes int64
		if err := db.Model(&Probe{}).
			Where("listing_id = ? AND type = ?", listing.ID, "migration").
			Count(&probes).Error; err != nil {
			common.SysLog(fmt.Sprintf(
				"marketplace: listing %d paused after pool migration; probe lookup failed", listing.ID))
			continue
		}
		if probes > 0 {
			continue
		}
		probe := Probe{
			ListingID: listing.ID,
			Type:      "migration",
			OK:        false,
			Detail:    poolMigrationFailReason,
			CreatedAt: time.Now(),
		}
		if err := db.Create(&probe).Error; err != nil {
			common.SysLog(fmt.Sprintf(
				"marketplace: listing %d paused after pool migration; probe write failed", listing.ID))
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Model pool accessors
// ---------------------------------------------------------------------------

// ModelPoolByName returns the standard model row for name (exact, trimmed).
func ModelPoolByName(db *gorm.DB, name string) (ModelPool, bool) {
	if db == nil {
		return ModelPool{}, false
	}
	var row ModelPool
	if err := db.Where("name = ?", strings.TrimSpace(name)).Take(&row).Error; err != nil {
		return ModelPool{}, false
	}
	return row, true
}

// ModelPoolAll returns every standard model ordered by name.
func ModelPoolAll(db *gorm.DB) []ModelPool {
	out := make([]ModelPool, 0)
	if db == nil {
		return out
	}
	if err := db.Order("name ASC").Find(&out).Error; err != nil {
		return []ModelPool{}
	}
	return out
}

// ModelPoolResolveAlias resolves input to a standard model name. Matching is
// case-insensitive across Name and the Aliases JSON array; an unknown input
// yields ("", false).
func ModelPoolResolveAlias(db *gorm.DB, input string) (string, bool) {
	if db == nil {
		return "", false
	}
	needle := strings.ToLower(strings.TrimSpace(input))
	if needle == "" {
		return "", false
	}
	rows := ModelPoolAll(db)
	for _, row := range rows {
		if strings.ToLower(strings.TrimSpace(row.Name)) == needle {
			return row.Name, true
		}
	}
	for _, row := range rows {
		for _, alias := range parseStringList(row.Aliases) {
			if strings.ToLower(strings.TrimSpace(alias)) == needle {
				return row.Name, true
			}
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Channel pool accessors
// ---------------------------------------------------------------------------

// ChannelPoolBySlug returns the channel row for slug (exact, trimmed).
func ChannelPoolBySlug(db *gorm.DB, slug string) (ChannelPool, bool) {
	if db == nil {
		return ChannelPool{}, false
	}
	var row ChannelPool
	if err := db.Where("slug = ?", strings.TrimSpace(slug)).Take(&row).Error; err != nil {
		return ChannelPool{}, false
	}
	return row, true
}

// ChannelPoolAll returns every channel ordered by slug.
func ChannelPoolAll(db *gorm.DB) []ChannelPool {
	out := make([]ChannelPool, 0)
	if db == nil {
		return out
	}
	if err := db.Order("slug ASC").Find(&out).Error; err != nil {
		return []ChannelPool{}
	}
	return out
}

// ChannelPoolCreateOrUpdate validates ch and upserts it by slug. Validation:
// slug is required, kind is one of official/plan/third_party, an optional
// key_pattern must compile, and status is active/disabled.
func ChannelPoolCreateOrUpdate(db *gorm.DB, ch ChannelPool) (ChannelPool, error) {
	if db == nil {
		return ChannelPool{}, errors.New("marketplace: nil database")
	}
	ch.Slug = strings.TrimSpace(ch.Slug)
	ch.Name = strings.TrimSpace(ch.Name)
	ch.Kind = strings.TrimSpace(ch.Kind)
	ch.Status = strings.TrimSpace(ch.Status)
	ch.Adaptor = strings.TrimSpace(ch.Adaptor)
	if ch.Slug == "" {
		return ChannelPool{}, ErrChannelPoolSlugRequired
	}
	switch ch.Kind {
	case ChannelKindOfficial, ChannelKindPlan, ChannelKindThirdParty:
	default:
		return ChannelPool{}, ErrChannelPoolKindInvalid
	}
	if ch.KeyPattern != "" {
		if _, err := regexp.Compile(ch.KeyPattern); err != nil {
			return ChannelPool{}, ErrChannelPoolKeyPatternInvalid
		}
	}
	if ch.Status == "" {
		ch.Status = PoolStatusActive
	}
	if ch.Status != PoolStatusActive && ch.Status != PoolStatusDisabled {
		return ChannelPool{}, ErrChannelPoolStatusInvalid
	}
	if ch.Adaptor == "" {
		ch.Adaptor = VendorAdaptorOpenAI
	}

	existing, found := ChannelPoolBySlug(db, ch.Slug)
	if !found {
		ch.ID = 0
		ch.CreatedAt = time.Time{}
		ch.UpdatedAt = time.Time{}
		if err := db.Create(&ch).Error; err != nil {
			return ChannelPool{}, fmt.Errorf("marketplace: create channel pool: %w", err)
		}
		return reloadChannelPool(db, ch.Slug)
	}
	updates := map[string]any{
		"name":                 ch.Name,
		"kind":                 ch.Kind,
		"protocols":            ch.Protocols,
		"allow_host_pattern":   ch.AllowHostPattern,
		"key_pattern":          ch.KeyPattern,
		"deny_paths":           ch.DenyPaths,
		"deny_key_prefixes":    ch.DenyKeyPrefixes,
		"adaptor":              ch.Adaptor,
		"strip_v1_prefix":      ch.StripV1Prefix,
		"passthrough_body":     ch.PassthroughBody,
		"quota_window":         ch.QuotaWindow,
		"exhaustion_signature": ch.ExhaustionSignature,
		"terms_url":            ch.TermsURL,
		"terms_checked_at":     ch.TermsCheckedAt,
		"status":               ch.Status,
		"updated_at":           time.Now(),
	}
	if err := db.Model(&ChannelPool{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
		return ChannelPool{}, fmt.Errorf("marketplace: update channel pool: %w", err)
	}
	return reloadChannelPool(db, ch.Slug)
}

// reloadChannelPool re-reads a just-written row, surfacing a missing row as an
// error instead of a zero value.
func reloadChannelPool(db *gorm.DB, slug string) (ChannelPool, error) {
	row, ok := ChannelPoolBySlug(db, slug)
	if !ok {
		return ChannelPool{}, errors.New("marketplace: channel pool row not found after write")
	}
	return row, nil
}

// ChannelPoolDelete removes the channel and its channel-model rows. A channel
// referenced by a listing is refused with ErrChannelPoolInUse so a live listing
// never loses its supplier definition by accident.
func ChannelPoolDelete(db *gorm.DB, slug string) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ErrChannelPoolSlugRequired
	}
	if db.Migrator().HasTable(&Listing{}) {
		var used int64
		if err := db.Model(&Listing{}).Where("vendor = ?", slug).Count(&used).Error; err != nil {
			return fmt.Errorf("marketplace: delete channel pool: %w", err)
		}
		if used > 0 {
			return ErrChannelPoolInUse
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("channel_slug = ?", slug).Delete(&ChannelModel{}).Error; err != nil {
			return fmt.Errorf("marketplace: delete channel pool: %w", err)
		}
		if err := tx.Where("slug = ?", slug).Delete(&ChannelPool{}).Error; err != nil {
			return fmt.Errorf("marketplace: delete channel pool: %w", err)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Channel model accessors
// ---------------------------------------------------------------------------

// ChannelModelFor returns the (channel, model) qualification in any status.
func ChannelModelFor(db *gorm.DB, channelSlug, model string) (ChannelModel, bool) {
	if db == nil {
		return ChannelModel{}, false
	}
	var row ChannelModel
	err := db.Where("channel_slug = ? AND model = ?",
		strings.TrimSpace(channelSlug), strings.TrimSpace(model)).Take(&row).Error
	if err != nil {
		return ChannelModel{}, false
	}
	return row, true
}

// VerifiedChannelModelFor returns the qualification only when it is verified
// and active.
func VerifiedChannelModelFor(db *gorm.DB, channelSlug, model string) (ChannelModel, bool) {
	row, ok := ChannelModelFor(db, channelSlug, model)
	if !ok || !row.Verified || row.Status != PoolStatusActive {
		return ChannelModel{}, false
	}
	return row, true
}

// ChannelModelsForChannel returns the channel's qualifications ordered by
// upstream model name.
func ChannelModelsForChannel(db *gorm.DB, channelSlug string) []ChannelModel {
	out := make([]ChannelModel, 0)
	if db == nil {
		return out
	}
	if err := db.Where("channel_slug = ?", strings.TrimSpace(channelSlug)).
		Order("upstream_model ASC").Find(&out).Error; err != nil {
		return []ChannelModel{}
	}
	return out
}

// UpsertChannelModelPool creates or updates one qualification.
//
//	NOTE: the contract name UpsertChannelModel is already taken by the legacy
//	mp_channel_model_map helper in channelmap.go, so the pool variant carries
//	the Pool suffix.
//
// The row identity is (channel_slug, upstream_model) — the pair the DELETE
// endpoint takes. Both unique keys are checked before writing: the same
// channel may map an upstream to exactly one model and a model to exactly one
// upstream, so either kind of mismatch is ErrChannelModelConflict.
//
// Validation: channel_slug, upstream_model and model are required; the channel
// must exist in mp_channels and the model in mp_models; coefficient_milli is
// 1..10000; differences, when present, must be a JSON object.
func UpsertChannelModelPool(db *gorm.DB, cm ChannelModel) (ChannelModel, error) {
	if db == nil {
		return ChannelModel{}, errors.New("marketplace: nil database")
	}
	cm.ChannelSlug = strings.TrimSpace(cm.ChannelSlug)
	cm.UpstreamModel = strings.TrimSpace(cm.UpstreamModel)
	cm.Model = strings.TrimSpace(cm.Model)
	cm.Status = strings.TrimSpace(cm.Status)
	cm.Differences = strings.TrimSpace(cm.Differences)
	if cm.ChannelSlug == "" || cm.UpstreamModel == "" || cm.Model == "" {
		return ChannelModel{}, ErrChannelModelFieldsRequired
	}
	if _, ok := ChannelPoolBySlug(db, cm.ChannelSlug); !ok {
		return ChannelModel{}, ErrChannelPoolNotFound
	}
	if _, ok := ModelPoolByName(db, cm.Model); !ok {
		return ChannelModel{}, ErrModelPoolNotFound
	}
	if cm.CoefficientMilli < 1 || cm.CoefficientMilli > 10000 {
		return ChannelModel{}, ErrChannelModelCoefficient
	}
	if cm.Differences != "" && !isJSONObject(cm.Differences) {
		return ChannelModel{}, ErrChannelModelDifferences
	}
	if cm.Status == "" {
		cm.Status = PoolStatusActive
	}
	switch cm.Status {
	case PoolStatusActive, PoolStatusPaused, PoolStatusBanned:
	default:
		return ChannelModel{}, ErrChannelModelStatusInvalid
	}

	byUpstream, upstreamFound := channelModelByUpstream(db, cm.ChannelSlug, cm.UpstreamModel)
	byModel, modelFound := ChannelModelFor(db, cm.ChannelSlug, cm.Model)
	if upstreamFound && byUpstream.Model != cm.Model {
		return ChannelModel{}, ErrChannelModelConflict
	}
	if modelFound && byModel.UpstreamModel != cm.UpstreamModel {
		return ChannelModel{}, ErrChannelModelConflict
	}

	if upstreamFound {
		now := time.Now()
		verifiedAt := byUpstream.VerifiedAt
		if cm.Verified {
			if verifiedAt == nil {
				verifiedAt = &now
			}
		} else {
			verifiedAt = nil
		}
		ratio := cm.FingerprintRatio
		if ratio == 0 {
			ratio = byUpstream.FingerprintRatio
		}
		updates := map[string]any{
			"coefficient_milli": cm.CoefficientMilli,
			"differences":       cm.Differences,
			"verified":          cm.Verified,
			"verified_at":       verifiedAt,
			"fingerprint_ratio": ratio,
			"status":            cm.Status,
			"updated_at":        now,
		}
		if err := db.Model(&ChannelModel{}).Where("id = ?", byUpstream.ID).Updates(updates).Error; err != nil {
			return ChannelModel{}, fmt.Errorf("marketplace: update channel model pool: %w", err)
		}
		row, _ := channelModelByUpstream(db, cm.ChannelSlug, cm.UpstreamModel)
		return row, nil
	}

	now := time.Now()
	row := ChannelModel{
		ChannelSlug:      cm.ChannelSlug,
		UpstreamModel:    cm.UpstreamModel,
		Model:            cm.Model,
		CoefficientMilli: cm.CoefficientMilli,
		Differences:      cm.Differences,
		Verified:         cm.Verified,
		FingerprintRatio: cm.FingerprintRatio,
		Status:           cm.Status,
	}
	if cm.Verified {
		row.VerifiedAt = &now
	}
	if err := db.Create(&row).Error; err != nil {
		return ChannelModel{}, fmt.Errorf("marketplace: create channel model pool: %w", err)
	}
	return row, nil
}

// DeleteChannelModelPool removes the (channel_slug, upstream_model) row. An
// absent row is a no-op so repeated deletes succeed.
func DeleteChannelModelPool(db *gorm.DB, channelSlug, upstream string) error {
	channelSlug = strings.TrimSpace(channelSlug)
	upstream = strings.TrimSpace(upstream)
	if channelSlug == "" || upstream == "" {
		return ErrChannelModelFieldsRequired
	}
	if err := db.Where("channel_slug = ? AND upstream_model = ?", channelSlug, upstream).
		Delete(&ChannelModel{}).Error; err != nil {
		return fmt.Errorf("marketplace: delete channel model pool: %w", err)
	}
	return nil
}

// SetChannelModelVerification records the fingerprint outcome of a channel
// model: verified/flipped and the tokenizer ratio. VerifiedAt is stamped on
// verification and cleared when the flag is removed.
func SetChannelModelVerification(db *gorm.DB, channelSlug, upstream string, verified bool, ratio float64) error {
	channelSlug = strings.TrimSpace(channelSlug)
	upstream = strings.TrimSpace(upstream)
	if channelSlug == "" || upstream == "" {
		return ErrChannelModelFieldsRequired
	}
	if db == nil {
		return errors.New("marketplace: nil database")
	}
	row, ok := channelModelByUpstream(db, channelSlug, upstream)
	if !ok {
		return ErrChannelModelNotFound
	}
	updates := map[string]any{
		"verified":          verified,
		"fingerprint_ratio": ratio,
		"updated_at":        time.Now(),
	}
	if verified {
		now := time.Now()
		updates["verified_at"] = now
	} else {
		updates["verified_at"] = nil
	}
	if err := db.Model(&ChannelModel{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
		return fmt.Errorf("marketplace: set channel model verification: %w", err)
	}
	return nil
}

// lookupVendorForDB resolves a channel for DB-backed write paths. The pool is
// authoritative; test-only injected vendors (MP_ENV=test) remain available
// through the registry fallback.
func lookupVendorForDB(db *gorm.DB, slug string) (Vendor, bool) {
	if db != nil {
		if v, ok := ChannelPoolAsVendor(db, slug); ok {
			return v, true
		}
	}
	return VendorByID(slug)
}

// ChannelPoolAsVendor adapts a pool row to the existing Vendor structure so
// probe/relay code written against the static whitelist can consume pool
// channels. Tier carries Kind.
func ChannelPoolAsVendor(db *gorm.DB, slug string) (Vendor, bool) {
	ch, ok := ChannelPoolBySlug(db, slug)
	if !ok {
		return Vendor{}, false
	}
	var protocols VendorProtocols
	if strings.TrimSpace(ch.Protocols) != "" {
		_ = json.Unmarshal([]byte(ch.Protocols), &protocols)
	}
	adaptor := strings.TrimSpace(ch.Adaptor)
	if adaptor == "" {
		adaptor = VendorAdaptorOpenAI
	}
	models := make([]string, 0)
	for _, cm := range ChannelModelsForChannel(db, ch.Slug) {
		if !poolContainsString(models, cm.Model) {
			models = append(models, cm.Model)
		}
	}
	sort.Strings(models)
	return Vendor{
		ID:               ch.Slug,
		DisplayName:      ch.Name,
		Tier:             ch.Kind,
		BaseURL:          protocols.OpenAI,
		Protocols:        protocols,
		Models:           models,
		PassthroughBody:  ch.PassthroughBody,
		DenyPaths:        parseStringList(ch.DenyPaths),
		DenyKeyPrefixes:  parseStringList(ch.DenyKeyPrefixes),
		AllowHostPattern: ch.AllowHostPattern,
		Adaptor:          adaptor,
		StripV1Prefix:    ch.StripV1Prefix,
	}, true
}

// channelModelByUpstream is the identity lookup of a qualification.
func channelModelByUpstream(db *gorm.DB, channelSlug, upstream string) (ChannelModel, bool) {
	var row ChannelModel
	err := db.Where("channel_slug = ? AND upstream_model = ?", channelSlug, upstream).Take(&row).Error
	if err != nil {
		return ChannelModel{}, false
	}
	return row, true
}

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

// parseStringList decodes a JSON string array. Empty or malformed data yields
// an empty (non-nil) slice so callers can range over it safely.
func parseStringList(raw string) []string {
	out := make([]string, 0)
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

// parseJSONObject decodes a JSON object; malformed data yields an empty map.
func parseJSONObject(raw string) map[string]any {
	out := make(map[string]any)
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

// isJSONObject reports whether raw is a valid JSON object.
func isJSONObject(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed[0] != '{' {
		return false
	}
	return json.Valid([]byte(trimmed))
}

func poolContainsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
