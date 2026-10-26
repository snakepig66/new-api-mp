package marketplace

import (
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Vendor describes one whitelisted API supplier (§3.5, OFFICIAL-CHANNELS §1).
// Base URLs are fixed by the platform — sellers submit only their key, never
// an address — so egress stays on the §3.11 whitelist and seller-controlled
// upstreams cannot become an SSRF vector.
//
// Each vendor carries the two protocol bases the first wave supports:
// OpenAI-compatible (chat/completions) and native Anthropic (messages). The
// marketplace channel created for a listing points at the OpenAI base and
// stores the Messages URL in its channel settings, so a buyer may speak either
// protocol against the same listing (§3.10).
type Vendor struct {
	ID          string
	DisplayName string
	Tier        string // VendorTierOfficial / VendorTierThirdParty
	// BaseURL is the OpenAI-compatible base. It mirrors Protocols.OpenAI and
	// is kept for the MP_EXTRA_VENDORS syntax and older call sites.
	BaseURL   string
	Protocols VendorProtocols
	// Models lists the platform model ids the vendor may serve. The actual
	// listable set is further filtered by the channel_model_map and the price
	// books (§3 of OFFICIAL-CHANNELS), so a vendor may advertise a model here
	// that has no mapping yet.
	Models []string
	// PassthroughBody forwards the buyer's raw request body (multi-protocol
	// vendors, §3.10) instead of converting it.
	PassthroughBody bool
	// DenyPaths are upstream path prefixes that belong to personal coding
	// plans and must never be used for resale (§2 of OFFICIAL-CHANNELS).
	DenyPaths []string
	// DenyKeyPrefixes reject subscription/plan keys before they are stored
	// (§2: Bailian sk-sp- Token Plan, sk-ws- Coding Plan).
	DenyKeyPrefixes []string
	// AllowHostPattern is an optional strict regex for vendor-owned workspace
	// subdomains (Bailian {ws}.cn-beijing.maas.aliyuncs.com). It never allows
	// arbitrary hosts.
	AllowHostPattern string
	// Adaptor names the upstream relay adaptor: "deepseek" or "openai".
	Adaptor string
	// StripV1Prefix drops the buyer's leading /v1 from the upstream path for
	// vendors whose base URL already ends with a version segment (Ark /api/v3,
	// Bailian /compatible-mode/v1, Moonshot /v1, ...).
	StripV1Prefix bool
}

// VendorProtocols holds the fixed protocol bases for one vendor.
type VendorProtocols struct {
	OpenAI    string `json:"openai"`
	Anthropic string `json:"anthropic"`
}

// Vendor tiers (OFFICIAL-CHANNELS §4.4): third-party sources arrive separately
// through the custom-URL scheme.
const (
	VendorTierOfficial   = "official"
	VendorTierThirdParty = "third_party"
)

// Adaptor names used by CreateListing to choose the upstream channel type.
const (
	VendorAdaptorOpenAI   = "openai"
	VendorAdaptorDeepSeek = "deepseek"
)

// ExtraVendorsEnv is the environment variable that injects additional
// whitelisted vendors at Init time (H8). It carries a JSON array; see
// LoadExtraVendorsFromEnv for the schema. Production leaves it unset: the
// built-in whitelist contains official channels only. Injected entries belong
// to the third-party tier.
const ExtraVendorsEnv = "MP_EXTRA_VENDORS"

// extraVendorConfig is the MP_EXTRA_VENDORS element schema:
//
//	{
//	  "id":               "tipig",                     // required, unique
//	  "display_name":     "tipig gateway",             // optional, defaults to id
//	  "base_url":         "https://api.tipig.cloud",   // required, absolute URL
//	  "anthropic_base":   "https://api.tipig.cloud",   // optional, defaults to base_url
//	  "models":           ["deepseek-v4.1-flash-1017"],// optional
//	  "passthrough_body": true,                        // optional (default false)
//	  "adaptor":          "deepseek",                  // optional: deepseek (default) | openai
//	  "strip_v1_prefix":  false                        // optional
//	}
type extraVendorConfig struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"display_name"`
	BaseURL         string   `json:"base_url"`
	AnthropicBase   string   `json:"anthropic_base"`
	Models          []string `json:"models"`
	PassthroughBody bool     `json:"passthrough_body"`
	Adaptor         string   `json:"adaptor"`
	StripV1Prefix   bool     `json:"strip_v1_prefix"`
}

// vendors is the platform's production supplier whitelist (§3.5): the official
// channels of OFFICIAL-CHANNELS §1. Test/E2E upstreams must NOT be added here
// (H8): inject them through MP_EXTRA_VENDORS, which keeps the production
// whitelist closed by default.
//
// Deny paths are taken from OFFICIAL-CHANNELS §2; they belong to per-seat
// coding plans and reselling them violates the vendor's terms. Sellers cannot
// submit URLs, so these lists also serve as an invariant guard for future
// custom-URL listings.
// vendors is the seed-time whitelist snapshot. The runtime source is the
// mp_channels pool (MODEL-CHANNEL-POOL): Init reloads it into vendorRegistry,
// and VendorByID refreshes it at most every registryTTL so admin edits take
// effect without a restart. This slice remains only as a fallback for code
// paths that run without a database (unit tests, early startup), so the seed
// data lives in exactly one place for tests.
var vendors = []Vendor{
	{
		ID:          SeedVendorDeepSeek,
		DisplayName: "DeepSeek",
		Tier:        VendorTierOfficial,
		BaseURL:     "https://api.deepseek.com",
		Protocols: VendorProtocols{
			OpenAI:    "https://api.deepseek.com",
			Anthropic: "https://api.deepseek.com/anthropic",
		},
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		PassthroughBody: true,
		Adaptor:         VendorAdaptorDeepSeek,
	},
	{
		ID:          "volcengine-ark",
		DisplayName: "火山方舟",
		Tier:        VendorTierOfficial,
		BaseURL:     "https://ark.cn-beijing.volces.com/api/v3",
		Protocols: VendorProtocols{
			OpenAI:    "https://ark.cn-beijing.volces.com/api/v3",
			Anthropic: "https://ark.cn-beijing.volces.com/api/compatible",
		},
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		PassthroughBody: true,
		Adaptor:         VendorAdaptorOpenAI,
		StripV1Prefix:   true,
		DenyPaths:       []string{"/api/coding", "/api/plan"},
	},
	{
		ID:          "aliyun-bailian",
		DisplayName: "阿里云百炼",
		Tier:        VendorTierOfficial,
		BaseURL:     "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Protocols: VendorProtocols{
			OpenAI:    "https://dashscope.aliyuncs.com/compatible-mode/v1",
			Anthropic: "https://dashscope.aliyuncs.com/apps/anthropic",
		},
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		PassthroughBody: true,
		Adaptor:         VendorAdaptorOpenAI,
		StripV1Prefix:   true,
		DenyPaths:       []string{"/apps/coding"},
		DenyKeyPrefixes: []string{"sk-sp-", "sk-ws-"},
		// 业务空间专属域名：只有 {ws}.cn-beijing.maas.aliyuncs.com 这层
		// 子域被允许，ws 仅限小写字母/数字/连字符，不做通配。
		AllowHostPattern: `^[a-z0-9-]+\.cn-beijing\.maas\.aliyuncs\.com$`,
	},
	{
		ID:          "moonshot",
		DisplayName: "Kimi（月之暗面）",
		Tier:        VendorTierOfficial,
		BaseURL:     "https://api.moonshot.cn/v1",
		Protocols: VendorProtocols{
			OpenAI:    "https://api.moonshot.cn/v1",
			Anthropic: "https://api.moonshot.cn/anthropic",
		},
		Models:          []string{"kimi-k2"},
		PassthroughBody: true,
		Adaptor:         VendorAdaptorOpenAI,
		StripV1Prefix:   true,
	},
	{
		ID:          "zhipu",
		DisplayName: "智谱 BigModel",
		Tier:        VendorTierOfficial,
		BaseURL:     "https://open.bigmodel.cn/api/paas/v4",
		Protocols: VendorProtocols{
			OpenAI:    "https://open.bigmodel.cn/api/paas/v4",
			Anthropic: "https://open.bigmodel.cn/api/anthropic",
		},
		Models:          []string{"glm-4.6"},
		PassthroughBody: true,
		Adaptor:         VendorAdaptorOpenAI,
		StripV1Prefix:   true,
		DenyPaths:       []string{"/api/coding/paas/v4"},
	},
	{
		ID:          "minimax",
		DisplayName: "MiniMax",
		Tier:        VendorTierOfficial,
		BaseURL:     "https://api.minimaxi.com/v1",
		Protocols: VendorProtocols{
			OpenAI:    "https://api.minimaxi.com/v1",
			Anthropic: "https://api.minimaxi.com/anthropic",
		},
		Models:          []string{"minimax-m2"},
		PassthroughBody: true,
		Adaptor:         VendorAdaptorOpenAI,
		StripV1Prefix:   true,
	},
}

// extraVendors holds the vendors injected through MP_EXTRA_VENDORS. It is
// replaced atomically by LoadExtraVendorsFromEnv; readers hold vendorsMu.
// Extras are a test-only escape hatch (MP_ENV=test, MODEL-CHANNEL-POOL §2).
var (
	vendorsMu    sync.RWMutex
	extraVendors []Vendor

	vendorRegistryMu sync.RWMutex
	vendorRegistry   map[string]Vendor
	vendorRegistryDB *gorm.DB
	vendorRegistryAt time.Time
)

// vendorRegistryTTL bounds how stale an admin channel edit can be before the
// next lookup reloads the pool.
const vendorRegistryTTL = 30 * time.Second

// extraVendorsEnabled reports whether this process may load test vendors.
func extraVendorsEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("MP_ENV")), "test")
}

// refreshVendorRegistry reloads the pool channels into the lookup registry.
// It is a no-op when the database is not initialized (early startup, tests
// without relayState); callers then fall back to the seed snapshot.
func refreshVendorRegistry(force bool) {
	db, _ := relayState()
	if db == nil {
		return
	}
	vendorRegistryMu.RLock()
	fresh := !force && vendorRegistryDB == db && len(vendorRegistry) > 0 && time.Since(vendorRegistryAt) < vendorRegistryTTL
	vendorRegistryMu.RUnlock()
	if fresh {
		return
	}
	channels := ChannelPoolAll(db)
	reg := make(map[string]Vendor, len(channels))
	hosts := make([]string, 0, len(channels))
	patterns := make([]string, 0, len(channels))
	for _, ch := range channels {
		vendor, ok := ChannelPoolAsVendor(db, ch.Slug)
		if !ok {
			continue
		}
		reg[vendor.ID] = vendor
		for _, raw := range []string{vendor.BaseURL, vendor.Protocols.OpenAI, vendor.Protocols.Anthropic} {
			if u, err := url.Parse(raw); err == nil && u.Host != "" && !slices.Contains(hosts, u.Hostname()) {
				hosts = append(hosts, u.Hostname())
			}
		}
		if vendor.AllowHostPattern != "" && !slices.Contains(patterns, vendor.AllowHostPattern) {
			patterns = append(patterns, vendor.AllowHostPattern)
		}
	}
	vendorRegistryMu.Lock()
	vendorRegistry = reg
	vendorRegistryDB = db
	vendorRegistryAt = time.Now()
	vendorRegistryMu.Unlock()
	// Keep egress in step with the pool. MP_EGRESS_DOMAINS adds deployment
	// hosts on top of the pool (it no longer replaces the pool set, because
	// seller-defined custom channels extend the pool at runtime); patterns
	// always come from the pool.
	if extra := strings.TrimSpace(os.Getenv("MP_EGRESS_DOMAINS")); extra != "" {
		for _, d := range strings.Split(extra, ",") {
			if d = strings.TrimSpace(d); d != "" && !slices.Contains(hosts, d) {
				hosts = append(hosts, d)
			}
		}
	}
	SetEgressDomains(hosts)
	if err := SetEgressPatterns(patterns); err != nil {
		common.SysError(common.LogText("marketplace: vendor registry egress patterns: %s", err.Error()))
	}
}

// registryVendors returns the DB-backed channels, or nil when the registry has
// never loaded.
func registryVendors() []Vendor {
	vendorRegistryMu.RLock()
	defer vendorRegistryMu.RUnlock()
	if len(vendorRegistry) == 0 {
		return nil
	}
	out := make([]Vendor, 0, len(vendorRegistry))
	// Stable order: registry map iteration would make VendorIDs random.
	for _, id := range sortedVendorIDs(vendorRegistry) {
		out = append(out, vendorRegistry[id])
	}
	return out
}

func sortedVendorIDs(reg map[string]Vendor) []string {
	ids := make([]string, 0, len(reg))
	for id := range reg {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// LoadExtraVendorsFromEnv parses MP_EXTRA_VENDORS and replaces the injected
// vendor list. It is idempotent: calling it repeatedly with the same
// environment yields the same whitelist, and an unset/empty variable clears
// the injected entries. Invalid JSON, a missing id/base_url, a relative
// base_url or a duplicate id (including one colliding with a built-in vendor)
// is an error — Init surfaces it instead of silently running with a partial
// whitelist.
//
// Init must call this BEFORE the egress whitelist is derived from the vendor
// base URLs, so the injected hosts pass the §3.11 guard.
func LoadExtraVendorsFromEnv() error {
	raw := strings.TrimSpace(os.Getenv(ExtraVendorsEnv))
	if !extraVendorsEnabled() {
		vendorsMu.Lock()
		extraVendors = nil
		vendorsMu.Unlock()
		if raw != "" {
			common.SysLog("marketplace: MP_EXTRA_VENDORS is ignored unless MP_ENV=test (MODEL-CHANNEL-POOL §2)")
		}
		return nil
	}
	if raw == "" {
		vendorsMu.Lock()
		extraVendors = nil
		vendorsMu.Unlock()
		return nil
	}
	var cfgs []extraVendorConfig
	if err := common.Unmarshal([]byte(raw), &cfgs); err != nil {
		return fmt.Errorf("marketplace: %s must be a JSON array: %w", ExtraVendorsEnv, err)
	}
	loaded := make([]Vendor, 0, len(cfgs))
	seen := make(map[string]bool, len(cfgs))
	for i, cfg := range cfgs {
		id := strings.TrimSpace(cfg.ID)
		if id == "" {
			return fmt.Errorf("marketplace: %s[%d]: id is required", ExtraVendorsEnv, i)
		}
		if collidesWithNonExtraChannel(id) {
			return fmt.Errorf("marketplace: %s[%d]: id %q collides with an existing channel", ExtraVendorsEnv, i, id)
		}
		if seen[id] {
			return fmt.Errorf("marketplace: %s[%d]: duplicate id %q", ExtraVendorsEnv, i, id)
		}
		seen[id] = true

		baseURL := strings.TrimSpace(cfg.BaseURL)
		u, uerr := url.Parse(baseURL)
		if baseURL == "" || uerr != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("marketplace: %s[%d]: base_url %q is not an absolute URL", ExtraVendorsEnv, i, baseURL)
		}
		anthropicBase := strings.TrimSpace(cfg.AnthropicBase)
		if anthropicBase == "" {
			anthropicBase = baseURL
		} else if au, aerr := url.Parse(anthropicBase); aerr != nil || au.Scheme == "" || au.Host == "" {
			return fmt.Errorf("marketplace: %s[%d]: anthropic_base %q is not an absolute URL", ExtraVendorsEnv, i, anthropicBase)
		}
		adaptor := strings.TrimSpace(cfg.Adaptor)
		switch adaptor {
		case "":
			// Injected vendors keep the pre-existing DeepSeek-adaptor behavior
			// so existing E2E gateways (tipig) continue to work unchanged.
			adaptor = VendorAdaptorDeepSeek
		case VendorAdaptorOpenAI, VendorAdaptorDeepSeek:
		default:
			return fmt.Errorf("marketplace: %s[%d]: unknown adaptor %q", ExtraVendorsEnv, i, adaptor)
		}
		displayName := strings.TrimSpace(cfg.DisplayName)
		if displayName == "" {
			displayName = id
		}
		models := make([]string, 0, len(cfg.Models))
		for _, m := range cfg.Models {
			m = strings.TrimSpace(m)
			if m != "" && !slices.Contains(models, m) {
				models = append(models, m)
			}
		}
		loaded = append(loaded, Vendor{
			ID:              id,
			DisplayName:     displayName,
			Tier:            VendorTierThirdParty,
			BaseURL:         baseURL,
			Protocols:       VendorProtocols{OpenAI: baseURL, Anthropic: anthropicBase},
			Models:          models,
			PassthroughBody: cfg.PassthroughBody,
			Adaptor:         adaptor,
			StripV1Prefix:   cfg.StripV1Prefix,
		})
	}
	vendorsMu.Lock()
	extraVendors = loaded
	vendorsMu.Unlock()
	return nil
}

// extraVendorList returns a copy of the test-only extras.
func extraVendorList() []Vendor {
	vendorsMu.RLock()
	defer vendorsMu.RUnlock()
	return slices.Clone(extraVendors)
}

// collidesWithNonExtraChannel reports whether id already belongs to the seed
// snapshot or the DB pool. Extras themselves are excluded, so reloading the
// same MP_EXTRA_VENDORS payload stays idempotent.
func collidesWithNonExtraChannel(id string) bool {
	if builtinVendorByID(id) {
		return true
	}
	refreshVendorRegistry(false)
	for _, v := range registryVendors() {
		if v.ID == id {
			return true
		}
	}
	return false
}

// resetVendorRegistryForTest clears the registry so tests that swap databases
// start from the seed snapshot; it is not used by production code.
func resetVendorRegistryForTest() {
	vendorRegistryMu.Lock()
	vendorRegistry = nil
	vendorRegistryDB = nil
	vendorRegistryAt = time.Time{}
	vendorRegistryMu.Unlock()
}

// builtinVendorByID reports whether id belongs to the immutable production
// whitelist. Injected vendors may not shadow it.
func builtinVendorByID(id string) bool {
	for _, v := range vendors {
		if v.ID == id {
			return true
		}
	}
	return false
}

// vendorSnapshot returns the runtime channel list: the DB pool when it has
// loaded, plus test-only extras; the seed snapshot only when no database is
// available (unit tests or very early startup).
func vendorSnapshot() []Vendor {
	refreshVendorRegistry(false)
	vendorsMu.RLock()
	extras := slices.Clone(extraVendors)
	vendorsMu.RUnlock()
	if list := registryVendors(); len(list) > 0 {
		return append(list, extras...)
	}
	return append(slices.Clone(vendors), extras...)
}

// VendorByID returns the whitelisted channel with the given id from the DB
// pool (or the seed snapshot before Init), then the test-only extras.
func VendorByID(id string) (Vendor, bool) {
	for _, v := range vendorSnapshot() {
		if v.ID == id {
			// Copy the slices so callers cannot mutate the registry.
			v.Models = slices.Clone(v.Models)
			v.DenyPaths = slices.Clone(v.DenyPaths)
			v.DenyKeyPrefixes = slices.Clone(v.DenyKeyPrefixes)
			return v, true
		}
	}
	return Vendor{}, false
}

// VendorIDs returns the ids of all channels: pool order, then the extras.
func VendorIDs() []string {
	list := vendorSnapshot()
	ids := make([]string, 0, len(list))
	for _, v := range list {
		ids = append(ids, v.ID)
	}
	return ids
}

// VendorOpenAIBase returns the OpenAI-compatible base URL (no trailing slash).
func VendorOpenAIBase(v Vendor) string {
	base := v.Protocols.OpenAI
	if base == "" {
		base = v.BaseURL
	}
	return strings.TrimSuffix(base, "/")
}

// VendorAnthropicMessagesURL returns the vendor-native Messages endpoint, or
// "" when the vendor has no Anthropic-compatible API. Pass-through channels use
// it so a Claude-format buyer request reaches the vendor without conversion.
func VendorAnthropicMessagesURL(v Vendor) string {
	base := strings.TrimSuffix(strings.TrimSpace(v.Protocols.Anthropic), "/")
	if base == "" {
		return ""
	}
	return base + "/v1/messages"
}

// VendorKeyDenied reports whether the key belongs to a personal plan prefix
// the platform refuses to resell (§2). The vendor's DenyKeyPrefixes are
// compared case-insensitively; the key itself is never echoed.
func VendorKeyDenied(v Vendor, apiKey string) bool {
	lower := strings.ToLower(apiKey)
	for _, prefix := range v.DenyKeyPrefixes {
		if prefix != "" && strings.HasPrefix(lower, strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

// VendorPathDenied reports whether an upstream URL path belongs to a denied
// personal-plan endpoint. Path matching is on a segment boundary so
// "/api/coding" denies "/api/coding/v3" but never "/api/coder".
func VendorPathDenied(v Vendor, path string) bool {
	for _, deny := range v.DenyPaths {
		if deny == "" {
			continue
		}
		if path == deny || strings.HasPrefix(path, deny+"/") {
			return true
		}
	}
	return false
}

// VendorHosts returns the host names of every whitelisted vendor's protocol
// URLs; they seed the egress whitelist (§3.11/§4.5) when MP_EGRESS_DOMAINS is
// not set. Extras injected through MP_EXTRA_VENDORS are included, which is why
// Init loads them first.
func VendorHosts() []string {
	list := vendorSnapshot()
	hosts := make([]string, 0, len(list)*2)
	appendHost := func(raw string) {
		if u, err := url.Parse(raw); err == nil && u.Host != "" && !slices.Contains(hosts, u.Hostname()) {
			hosts = append(hosts, u.Hostname())
		}
	}
	for _, v := range list {
		appendHost(v.BaseURL)
		appendHost(v.Protocols.OpenAI)
		appendHost(v.Protocols.Anthropic)
	}
	return hosts
}

// VendorHostPatterns returns the strict subdomain patterns channels declare
// (Bailian workspace domains). Egress consults them after the exact host list.
func VendorHostPatterns() []string {
	list := vendorSnapshot()
	patterns := make([]string, 0, len(list))
	for _, v := range list {
		if v.AllowHostPattern != "" && !slices.Contains(patterns, v.AllowHostPattern) {
			patterns = append(patterns, v.AllowHostPattern)
		}
	}
	return patterns
}
