package marketplace

import (
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// Vendor describes one whitelisted API supplier (§3.5). BaseURL is fixed by
// the platform — sellers submit only their key, never an address — so egress
// stays on the §3.11 whitelist domains and seller-controlled upstreams
// cannot become an SSRF vector.
//
// The struct deliberately leaves room for the §8 second-wave sources
// (non-official vendors with custom base URLs, fidelity levels L0–L3,
// subscription/OAuth credentials): those arrive as additional fields and
// additional whitelist entries without changing the lookup API.
type Vendor struct {
	ID          string
	DisplayName string
	BaseURL     string   // fixed by the platform; sellers never supply it
	Models      []string // model ids this vendor can serve
	// PassthroughBody marks vendors that natively speak multiple protocol
	// formats (OpenAI + Anthropic); the channel forwards the buyer's raw body
	// instead of converting, which keeps /v1/messages native (§3.10).
	PassthroughBody bool
}

// ExtraVendorsEnv is the environment variable that injects additional
// whitelisted vendors at Init time (H8). It carries a JSON array; see
// LoadExtraVendorsFromEnv for the schema. Production leaves it unset: the
// built-in whitelist contains official channels only.
const ExtraVendorsEnv = "MP_EXTRA_VENDORS"

// extraVendorConfig is the MP_EXTRA_VENDORS element schema:
//
//	{
//	  "id":               "tipig",                     // required, unique
//	  "display_name":     "tipig gateway",             // optional, defaults to id
//	  "base_url":         "https://api.tipig.cloud",   // required, absolute URL
//	  "models":           ["deepseek-v4.1-flash-1017"],// optional
//	  "passthrough_body": true                         // optional (default false)
//	}
type extraVendorConfig struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"display_name"`
	BaseURL         string   `json:"base_url"`
	Models          []string `json:"models"`
	PassthroughBody bool     `json:"passthrough_body"`
}

// vendors is the platform's production supplier whitelist (§3.5): official
// channels only, with model ids mirroring the SeedPrices seed rows (seed.go)
// so a listing can always be priced.
//
// Test/E2E upstreams must NOT be added here (H8): inject them through
// MP_EXTRA_VENDORS, which keeps the production whitelist closed by default.
var vendors = []Vendor{
	{
		ID:          SeedVendorDeepSeek,
		DisplayName: "DeepSeek",
		BaseURL:     "https://api.deepseek.com",
		Models:      []string{"deepseek-flash", "deepseek-v4-pro"},
	},
}

// extraVendors holds the vendors injected through MP_EXTRA_VENDORS. It is
// replaced atomically by LoadExtraVendorsFromEnv; readers hold vendorsMu.
var (
	vendorsMu    sync.RWMutex
	extraVendors []Vendor
)

// LoadExtraVendorsFromEnv parses MP_EXTRA_VENDORS and replaces the injected
// vendor list. It is idempotent: calling it repeatedly with the same
// environment yields the same whitelist, and an unset/empty variable clears
// the injected entries. Invalid JSON, a missing id/base_url, a relative
// base_url or a duplicate id (including one colliding with a built-in vendor)
// is an error — Init surfaces it instead of silently running with a partial
// whitelist.
//
// Init must call this BEFORE the egress whitelist is derived from
// VendorBaseURLHosts, so the injected base URLs pass the §3.11 guard.
func LoadExtraVendorsFromEnv() error {
	raw := strings.TrimSpace(os.Getenv(ExtraVendorsEnv))
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
		if builtinVendorByID(id) {
			return fmt.Errorf("marketplace: %s[%d]: id %q collides with a built-in vendor", ExtraVendorsEnv, i, id)
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
			BaseURL:         baseURL,
			Models:          models,
			PassthroughBody: cfg.PassthroughBody,
		})
	}
	vendorsMu.Lock()
	extraVendors = loaded
	vendorsMu.Unlock()
	return nil
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

// VendorByID returns the whitelisted vendor with the given id, first from the
// production whitelist and then from the MP_EXTRA_VENDORS injection.
func VendorByID(id string) (Vendor, bool) {
	vendorsMu.RLock()
	defer vendorsMu.RUnlock()
	for _, list := range [][]Vendor{vendors, extraVendors} {
		for _, v := range list {
			if v.ID == id {
				// Copy the model list so callers cannot mutate the whitelist.
				v.Models = slices.Clone(v.Models)
				return v, true
			}
		}
	}
	return Vendor{}, false
}

// VendorIDs returns the ids of all whitelisted vendors: built-ins first (in
// whitelist order), then the injected extras.
func VendorIDs() []string {
	vendorsMu.RLock()
	defer vendorsMu.RUnlock()
	ids := make([]string, 0, len(vendors)+len(extraVendors))
	for _, v := range vendors {
		ids = append(ids, v.ID)
	}
	for _, v := range extraVendors {
		ids = append(ids, v.ID)
	}
	return ids
}

// VendorBaseURLHosts returns the host names of every whitelisted vendor's
// fixed BaseURL; they seed the egress whitelist (§3.11/§4.5) when
// MP_EGRESS_DOMAINS is not set. Extras injected through MP_EXTRA_VENDORS are
// included, which is why Init loads them first.
func VendorBaseURLHosts() []string {
	vendorsMu.RLock()
	defer vendorsMu.RUnlock()
	hosts := make([]string, 0, len(vendors)+len(extraVendors))
	for _, list := range [][]Vendor{vendors, extraVendors} {
		for _, v := range list {
			if u, err := url.Parse(v.BaseURL); err == nil && u.Host != "" {
				hosts = append(hosts, u.Host)
			}
		}
	}
	return hosts
}
