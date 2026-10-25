package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The retired boolean switch only distinguishes currency from tokens. When
// the site is set to CNY it must not silently flip the choice back to USD.
func TestDisplayInCurrencyKeepsCNY(t *testing.T) {
	cfg := config.GlobalConfig.Get("general_setting")
	require.NotNil(t, cfg)

	original := operation_setting.GetQuotaDisplayType()
	originalMap := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		require.NoError(t, config.UpdateConfigFromMap(cfg, map[string]string{"quota_display_type": original}))
		common.OptionMap = originalMap
	})

	require.NoError(t, config.UpdateConfigFromMap(cfg, map[string]string{"quota_display_type": operation_setting.QuotaDisplayTypeCNY}))

	require.NoError(t, updateOptionMap("DisplayInCurrencyEnabled", "true"))
	assert.Equal(t, operation_setting.QuotaDisplayTypeCNY, operation_setting.GetQuotaDisplayType())

	require.NoError(t, updateOptionMap("DisplayInCurrencyEnabled", "false"))
	assert.Equal(t, operation_setting.QuotaDisplayTypeTokens, operation_setting.GetQuotaDisplayType())
}

// With the default USD choice the legacy switch keeps its historical meaning.
func TestDisplayInCurrencyDefaultsToUSD(t *testing.T) {
	cfg := config.GlobalConfig.Get("general_setting")
	require.NotNil(t, cfg)

	original := operation_setting.GetQuotaDisplayType()
	originalMap := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		require.NoError(t, config.UpdateConfigFromMap(cfg, map[string]string{"quota_display_type": original}))
		common.OptionMap = originalMap
	})

	require.NoError(t, config.UpdateConfigFromMap(cfg, map[string]string{"quota_display_type": operation_setting.QuotaDisplayTypeUSD}))

	require.NoError(t, updateOptionMap("DisplayInCurrencyEnabled", "true"))
	assert.Equal(t, operation_setting.QuotaDisplayTypeUSD, operation_setting.GetQuotaDisplayType())
}
