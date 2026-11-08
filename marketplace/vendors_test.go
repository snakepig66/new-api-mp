package marketplace

import (
	"net/url"
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
	origEnv, hadEnv := os.LookupEnv("MP_ENV")
	require.NoError(t, os.Setenv(ExtraVendorsEnv, raw))
	// MP_EXTRA_VENDORS is a test-only escape hatch (MODEL-CHANNEL-POOL §2).
	require.NoError(t, os.Setenv("MP_ENV", "test"))
	require.NoError(t, LoadExtraVendorsFromEnv())
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(ExtraVendorsEnv, orig)
		} else {
			_ = os.Unsetenv(ExtraVendorsEnv)
		}
		if hadEnv {
			_ = os.Setenv("MP_ENV", origEnv)
		} else {
			_ = os.Unsetenv("MP_ENV")
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
	resetVendorRegistryForTest()
	t.Cleanup(resetVendorRegistryForTest)
	ids := VendorIDs()
	require.Equal(t, []string{
		"deepseek", "volcengine-ark", "aliyun-bailian", "moonshot", "zhipu", "minimax",
	}, ids, "the production whitelist holds the OFFICIAL-CHANNELS §1 channels only (H8: tipig is not built in)")
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
	resetVendorRegistryForTest()
	t.Cleanup(resetVendorRegistryForTest)
	withExtraVendors(t, tipigExtraVendors)

	v, ok := VendorByID("tipig")
	require.True(t, ok, "the injected vendor must join the whitelist")
	assert.Equal(t, "tipig", v.ID)
	assert.Equal(t, "tipig gateway", v.DisplayName)
	assert.Equal(t, "https://api.tipig.cloud", v.BaseURL)
	assert.Equal(t, []string{"deepseek-v4.1-flash-1017", "deepseek-v4-pro-0813"}, v.Models)
	assert.True(t, v.PassthroughBody)

	ids := VendorIDs()
	assert.Equal(t, []string{
		"deepseek", "volcengine-ark", "aliyun-bailian", "moonshot", "zhipu", "minimax", "tipig",
	}, ids)
	assert.Contains(t, VendorHosts(), "api.tipig.cloud",
		"injected base URLs seed the egress whitelist")

	// Idempotent: reloading the same environment yields the same whitelist.
	require.NoError(t, LoadExtraVendorsFromEnv())
	require.NoError(t, LoadExtraVendorsFromEnv())
	assert.Len(t, VendorIDs(), 7)

	// Clearing the variable removes the injected vendor again.
	require.NoError(t, os.Unsetenv(ExtraVendorsEnv))
	require.NoError(t, LoadExtraVendorsFromEnv())
	_, ok = VendorByID("tipig")
	assert.False(t, ok, "an unset MP_EXTRA_VENDORS clears the injected vendors")
	assert.Len(t, VendorIDs(), 6)
}

func TestVendorExtraInvalidEnv(t *testing.T) {
	for name, raw := range map[string]string{
		"invalid json":     `{"id":"tipig"}`,
		"missing id":       `[{"base_url":"https://api.tipig.cloud"}]`,
		"missing base":     `[{"id":"tipig"}]`,
		"relative base":    `[{"id":"tipig","base_url":"/v1"}]`,
		"duplicate id":     `[{"id":"tipig","base_url":"https://a.example"},{"id":"tipig","base_url":"https://b.example"}]`,
		"collides builtin": `[{"id":"deepseek","base_url":"https://evil.example"}]`,
		"bad adaptor":      `[{"id":"tipig","base_url":"https://a.example","adaptor":"claude"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			orig, had := os.LookupEnv(ExtraVendorsEnv)
			origEnv, hadEnv := os.LookupEnv("MP_ENV")
			require.NoError(t, os.Setenv(ExtraVendorsEnv, raw))
			require.NoError(t, os.Setenv("MP_ENV", "test"))
			t.Cleanup(func() {
				if had {
					_ = os.Setenv(ExtraVendorsEnv, orig)
				} else {
					_ = os.Unsetenv(ExtraVendorsEnv)
				}
				if hadEnv {
					_ = os.Setenv("MP_ENV", origEnv)
				} else {
					_ = os.Unsetenv("MP_ENV")
				}
				require.NoError(t, LoadExtraVendorsFromEnv())
			})
			assert.Error(t, LoadExtraVendorsFromEnv(), "invalid MP_EXTRA_VENDORS must be rejected")
		})
	}
}

// OFFICIAL-CHANNELS §1: every built-in channel carries both protocol bases and
// the right tier, and the workspace-domain pattern is anchored.
func TestOfficialVendorProtocols(t *testing.T) {
	for _, id := range []string{"deepseek", "volcengine-ark", "aliyun-bailian", "moonshot", "zhipu", "minimax"} {
		v, ok := VendorByID(id)
		require.Truef(t, ok, "%s must be whitelisted", id)
		assert.Equal(t, VendorTierOfficial, v.Tier, id)
		assert.Equal(t, "https", mustScheme(t, VendorOpenAIBase(v)), id)
		assert.Contains(t, VendorAnthropicMessagesURL(v), "/v1/messages",
			"%s must expose a native Messages endpoint", id)
		assert.NotEmpty(t, v.DisplayName)
	}

	hosts := VendorHosts()
	for _, host := range []string{
		"api.deepseek.com",
		"ark.cn-beijing.volces.com",
		"dashscope.aliyuncs.com",
		"api.moonshot.cn",
		"open.bigmodel.cn",
		"api.minimaxi.com",
	} {
		assert.Contains(t, hosts, host)
	}

	// The Bailian workspace pattern is anchored by SetEgressPatterns: only the
	// documented subdomain shape passes.
	require.NoError(t, SetEgressPatterns(VendorHostPatterns()))
	t.Cleanup(func() { _ = SetEgressPatterns(nil) })
	assert.NoError(t, CheckEgressHost("ws-1.cn-beijing.maas.aliyuncs.com"))
	assert.Error(t, CheckEgressHost("evil.cn-beijing.maas.aliyuncs.com.example.com"))
	assert.Error(t, CheckEgressHost("cn-beijing.maas.aliyuncs.com"))
}

func TestVendorDenyRules(t *testing.T) {
	bailian, ok := VendorByID("aliyun-bailian")
	require.True(t, ok)
	assert.True(t, VendorKeyDenied(bailian, "sk-sp-123"), "Token Plan key must be refused")
	assert.True(t, VendorKeyDenied(bailian, "SK-WS-123"), "prefix match is case-insensitive")
	assert.False(t, VendorKeyDenied(bailian, "sk-123"))

	ark, ok := VendorByID("volcengine-ark")
	require.True(t, ok)
	assert.True(t, VendorPathDenied(ark, "/api/coding/v3"))
	assert.True(t, VendorPathDenied(ark, "/api/plan"))
	assert.False(t, VendorPathDenied(ark, "/api/coder"), "segment boundary only")

	// Built-in protocol bases never hit their own deny list.
	for _, id := range VendorIDs() {
		v, _ := VendorByID(id)
		for _, u := range []string{v.Protocols.OpenAI, v.Protocols.Anthropic} {
			parsed, err := url.Parse(u)
			require.NoError(t, err)
			assert.Falsef(t, VendorPathDenied(v, parsed.Path), "%s protocol base %s hits deny list", id, u)
		}
	}
}

func mustScheme(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Scheme
}
