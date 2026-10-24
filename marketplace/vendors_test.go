package marketplace

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tipigExtraVendors is the E2E-only vendor injected through MP_EXTRA_VENDORS
// (H8). It is deliberately absent from the built-in production whitelist.
const tipigExtraVendors = `[{
	"id": "tipig",
	"display_name": "tipig gateway",
	"base_url": "https://api.tipig.cloud",
	"models": ["deepseek-v4.1-flash-1017", "deepseek-v4-pro-0813"],
	"passthrough_body": true
}]`

// withExtraVendors sets MP_EXTRA_VENDORS for one test, reloads the injected
// whitelist and restores both the environment and the loaded vendors when the
// test ends. Tests in this package do not call t.Parallel, so the global
// mutation is safe.
func withExtraVendors(t *testing.T, raw string) {
	t.Helper()
	orig, had := os.LookupEnv(ExtraVendorsEnv)
	require.NoError(t, os.Setenv(ExtraVendorsEnv, raw))
	require.NoError(t, LoadExtraVendorsFromEnv())
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(ExtraVendorsEnv, orig)
		} else {
			_ = os.Unsetenv(ExtraVendorsEnv)
		}
		require.NoError(t, LoadExtraVendorsFromEnv(), "restore injected vendors")
	})
}

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
	for _, id := range []string{"", "DeepSeek", "openai", "kimi", "localhost", "tipig"} {
		_, ok := VendorByID(id)
		assert.Falsef(t, ok, "vendor %q must not be whitelisted by default", id)
	}
}

func TestVendorIDs(t *testing.T) {
	ids := VendorIDs()
	require.Len(t, ids, 1, "the production whitelist holds official channels only (H8: tipig is not built in)")
	assert.Equal(t, "deepseek", ids[0])
}

func TestVendorModelsAreCopied(t *testing.T) {
	v, ok := VendorByID("deepseek")
	require.True(t, ok)
	v.Models[0] = "mutated"
	again, ok := VendorByID("deepseek")
	require.True(t, ok)
	assert.Equal(t, "deepseek-flash", again.Models[0], "callers must not mutate the whitelist")
}

// H8: MP_EXTRA_VENDORS injects non-production vendors at Init time.
func TestVendorExtraFromEnv(t *testing.T) {
	withExtraVendors(t, tipigExtraVendors)

	v, ok := VendorByID("tipig")
	require.True(t, ok, "the injected vendor must join the whitelist")
	assert.Equal(t, "tipig", v.ID)
	assert.Equal(t, "tipig gateway", v.DisplayName)
	assert.Equal(t, "https://api.tipig.cloud", v.BaseURL)
	assert.Equal(t, []string{"deepseek-v4.1-flash-1017", "deepseek-v4-pro-0813"}, v.Models)
	assert.True(t, v.PassthroughBody)

	ids := VendorIDs()
	assert.Equal(t, []string{"deepseek", "tipig"}, ids)
	assert.Contains(t, VendorBaseURLHosts(), "api.tipig.cloud",
		"injected base URLs seed the egress whitelist")

	// Idempotent: reloading the same environment yields the same whitelist.
	require.NoError(t, LoadExtraVendorsFromEnv())
	require.NoError(t, LoadExtraVendorsFromEnv())
	assert.Equal(t, []string{"deepseek", "tipig"}, VendorIDs())

	// Clearing the variable removes the injected vendor again.
	require.NoError(t, os.Unsetenv(ExtraVendorsEnv))
	require.NoError(t, LoadExtraVendorsFromEnv())
	_, ok = VendorByID("tipig")
	assert.False(t, ok, "an unset MP_EXTRA_VENDORS clears the injected vendors")
	assert.Equal(t, []string{"deepseek"}, VendorIDs())
}

func TestVendorExtraInvalidEnv(t *testing.T) {
	for name, raw := range map[string]string{
		"invalid json":     `{"id":"tipig"}`,
		"missing id":       `[{"base_url":"https://api.tipig.cloud"}]`,
		"missing base":     `[{"id":"tipig"}]`,
		"relative base":    `[{"id":"tipig","base_url":"/v1"}]`,
		"duplicate id":     `[{"id":"tipig","base_url":"https://a.example"},{"id":"tipig","base_url":"https://b.example"}]`,
		"collides builtin": `[{"id":"deepseek","base_url":"https://evil.example"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			orig, had := os.LookupEnv(ExtraVendorsEnv)
			require.NoError(t, os.Setenv(ExtraVendorsEnv, raw))
			t.Cleanup(func() {
				if had {
					_ = os.Setenv(ExtraVendorsEnv, orig)
				} else {
					_ = os.Unsetenv(ExtraVendorsEnv)
				}
				require.NoError(t, LoadExtraVendorsFromEnv())
			})
			assert.Error(t, LoadExtraVendorsFromEnv(), "invalid MP_EXTRA_VENDORS must be rejected")
		})
	}
}
