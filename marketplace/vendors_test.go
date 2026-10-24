package marketplace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVendorWhitelistHit(t *testing.T) {
	v, ok := VendorByID("deepseek")
	require.True(t, ok, "deepseek must be whitelisted (§3.5)")
	assert.Equal(t, "deepseek", v.ID)
	assert.NotEmpty(t, v.DisplayName)
	// The base URL is fixed by the platform; sellers can never supply one.
	assert.Equal(t, "https://api.deepseek.com", v.BaseURL)
	// The whitelist models mirror the SeedPrices seed rows so every
	// listable model is priced.
	assert.Equal(t, []string{"deepseek-flash", "deepseek-v4-pro"}, v.Models)
}

func TestVendorWhitelistMiss(t *testing.T) {
	for _, id := range []string{"", "DeepSeek", "openai", "kimi", "localhost"} {
		_, ok := VendorByID(id)
		assert.Falsef(t, ok, "vendor %q must not be whitelisted", id)
	}
}

func TestVendorIDs(t *testing.T) {
	ids := VendorIDs()
	require.Len(t, ids, 2, "whitelist holds deepseek + tipig (§7 E2E upstream)")
	assert.Equal(t, "deepseek", ids[0])
	assert.Equal(t, "tipig", ids[1])
}

func TestVendorModelsAreCopied(t *testing.T) {
	v, ok := VendorByID("deepseek")
	require.True(t, ok)
	v.Models[0] = "mutated"
	again, ok := VendorByID("deepseek")
	require.True(t, ok)
	assert.Equal(t, "deepseek-flash", again.Models[0], "callers must not mutate the whitelist")
}
