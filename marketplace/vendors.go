package marketplace

import (
	"net/url"
	"slices"
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
}

// vendors is the platform's supplier whitelist (§3.5). v1 lists only the
// DeepSeek official channel; the model ids mirror the SeedPrices seed rows
// (seed.go) so a listing can always be priced.
//
// tipig is the platform's own relay gateway (api.tipig.cloud, an upstream
// new-api instance) serving the same DeepSeek V4.1/V4 Pro models; it is the
// E2E test upstream until the official-key onboarding lands (§7 阶段 7).
var vendors = []Vendor{
	{
		ID:          SeedVendorDeepSeek,
		DisplayName: "DeepSeek",
		BaseURL:     "https://api.deepseek.com",
		Models:      []string{"deepseek-flash", "deepseek-v4-pro"},
	},
	{
		ID:          "tipig",
		DisplayName: "tipig gateway",
		BaseURL:     "https://api.tipig.cloud/v1",
		Models:      []string{"deepseek-v4.1-flash-1017", "deepseek-v4-pro-0813"},
	},
}

// VendorByID returns the whitelisted vendor with the given id.
func VendorByID(id string) (Vendor, bool) {
	for _, v := range vendors {
		if v.ID == id {
			// Copy the model list so callers cannot mutate the whitelist.
			v.Models = slices.Clone(v.Models)
			return v, true
		}
	}
	return Vendor{}, false
}

// VendorIDs returns the ids of all whitelisted vendors, in whitelist order.
func VendorIDs() []string {
	ids := make([]string, len(vendors))
	for i, v := range vendors {
		ids[i] = v.ID
	}
	return ids
}

// VendorBaseURLHosts returns the host names of every whitelisted vendor's
// fixed BaseURL; they seed the egress whitelist (§3.11/§4.5) when
// MP_EGRESS_DOMAINS is not set.
func VendorBaseURLHosts() []string {
	hosts := make([]string, 0, len(vendors))
	for _, v := range vendors {
		if u, err := url.Parse(v.BaseURL); err == nil && u.Host != "" {
			hosts = append(hosts, u.Host)
		}
	}
	return hosts
}
