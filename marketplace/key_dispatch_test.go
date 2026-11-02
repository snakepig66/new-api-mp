package marketplace_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/marketplace"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// doChatWithKey sends a minimal chat completion request using a specified API token.
func doChatWithKey(t *testing.T, client *http.Client, serverURL, tokenKey, modelName string) chatResult {
	t.Helper()
	payload := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, modelName)
	req, err := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions", strings.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-"+tokenKey)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return chatResult{
		status:    resp.StatusCode,
		body:      body,
		requestID: resp.Header.Get(common.RequestIdKey),
	}
}

// TestKeyDispatch_StandardKeyCallsPlazaModel_Success verifies that a standard key
// calling a plaza-enabled model dispatches through new-api native channels and succeeds.
func TestKeyDispatch_StandardKeyCallsPlazaModel_Success(t *testing.T) {
	_ = i18n.Init()
	ratio_setting.InitRatioSettings()
	h := newMPHarness(t, itBuyerQuota)

	plazaModel := "gpt-4o-mini"

	// 1. Configure the model in mp_model_pools as a plaza model
	var pool marketplace.ModelPool
	if err := h.db.Where("name = ?", plazaModel).First(&pool).Error; err != nil {
		require.NoError(t, h.db.Create(&marketplace.ModelPool{
			Name:         plazaModel,
			OriginVendor: "openai",
			EnablePlaza:  true,
			Status:       "active",
		}).Error)
	} else {
		require.NoError(t, h.db.Model(&pool).Updates(map[string]any{
			"enable_plaza": true,
			"status":       "active",
		}).Error)
	}

	// 2. Configure a native channel serving this plaza model
	mock := newMockUpstream(t, writeChatSuccessFor(plazaModel, "hello from plaza", chatUsageJSON{PromptTokens: 10, CompletionTokens: 5}))
	mockURL := mock.url()
	nativeChannel := model.Channel{
		Type:    constant.ChannelTypeOpenAI,
		Name:    "native-plaza-channel",
		Key:     "native-key-123",
		BaseURL: &mockURL,
		Status:  common.ChannelStatusEnabled,
		Models:  plazaModel,
		Group:   "default",
	}
	require.NoError(t, h.db.Create(&nativeChannel).Error)
	require.NoError(t, nativeChannel.AddAbilities(h.db))

	// 3. Create a standard key (no hyphens to preserve full key in auth parser)
	stdKey := "stdtoken12345"
	token := model.Token{
		UserId:         h.buyerID,
		Key:            stdKey,
		Name:           "standard-key",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		KeyType:        model.KeyTypeStandard,
	}
	require.NoError(t, h.db.Create(&token).Error)

	// 4. Send request with standard key calling plaza model
	res := doChatWithKey(t, h.client, h.server.URL, stdKey, plazaModel)
	assert.Equal(t, http.StatusOK, res.status, "standard key calling plaza model must succeed")
	assert.Contains(t, string(res.body), "hello from plaza")
	assert.Equal(t, 1, mock.callCount(), "native mock upstream must have received the request")
}

// TestKeyDispatch_StandardKeyCallsMarketModel_Rejected verifies that a standard key
// calling a market-only model (not enabled on plaza) is rejected with the exact error message.
func TestKeyDispatch_StandardKeyCallsMarketModel_Rejected(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)

	marketModel := "deepseek-flash"

	// Ensure marketModel is not a plaza model
	h.db.Model(&marketplace.ModelPool{}).Where("name = ?", marketModel).Update("enable_plaza", false)

	// Set up a live marketplace listing for marketModel
	mock := newMockUpstream(t, writeChatSuccessFor(marketModel, "hello from market", chatUsageJSON{PromptTokens: 10, CompletionTokens: 5}))
	h.addListing(mock.url(), "sk-seller-mock-key-123", 500)

	// Create a standard key
	stdKey := "stdtokenmarketreject"
	token := model.Token{
		UserId:         h.buyerID,
		Key:            stdKey,
		Name:           "standard-key",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		KeyType:        model.KeyTypeStandard,
	}
	require.NoError(t, h.db.Create(&token).Error)

	// Call market model with standard key
	res := doChatWithKey(t, h.client, h.server.URL, stdKey, marketModel)
	assert.Equal(t, http.StatusBadRequest, res.status, "standard key calling market model must be rejected with 400")
	assert.Contains(t, string(res.body), "该模型不在此密钥可用范围内（普通密钥只能用模型广场模型 / 共享密钥请先在模型市场把该模型加入此密钥）")
	assert.Equal(t, 0, mock.callCount(), "upstream must not be called when request is rejected")
}

// TestKeyDispatch_SharedKeyCallsAddedMarketModel_Success verifies that a shared key
// calling a market model explicitly added to this key routes via marketplace distribution.
func TestKeyDispatch_SharedKeyCallsAddedMarketModel_Success(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)

	marketModel := "deepseek-flash"

	// Set up live marketplace listing
	mock := newMockUpstream(t, writeChatSuccessFor(marketModel, "hello from shared market", chatUsageJSON{PromptTokens: 10, CompletionTokens: 5}))
	h.addListing(mock.url(), "sk-seller-mock-key-456", 500)

	// Create a shared key
	sharedKey := "sharedtokenpass"
	token := model.Token{
		UserId:         h.buyerID,
		Key:            sharedKey,
		Name:           "shared-key",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		KeyType:        model.KeyTypeShared,
	}
	require.NoError(t, h.db.Create(&token).Error)

	// Add marketModel to this key
	require.NoError(t, h.db.Create(&marketplace.KeyModel{
		TokenID: int64(token.Id),
		Model:   marketModel,
	}).Error)

	// Call market model with shared key
	res := doChatWithKey(t, h.client, h.server.URL, sharedKey, marketModel)
	assert.Equal(t, http.StatusOK, res.status, "shared key calling added market model must succeed")
	assert.Contains(t, string(res.body), "hello from shared market")
	assert.Equal(t, 1, mock.callCount(), "market listing mock upstream must have received the request")
}

// TestKeyDispatch_SharedKeyCallsUnaddedModel_Rejected verifies that a shared key
// calling a model NOT added to this key is rejected with the exact error message.
func TestKeyDispatch_SharedKeyCallsUnaddedModel_Rejected(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)

	marketModel := "deepseek-flash"

	// Set up live marketplace listing
	mock := newMockUpstream(t, writeChatSuccessFor(marketModel, "hello", chatUsageJSON{PromptTokens: 10, CompletionTokens: 5}))
	h.addListing(mock.url(), "sk-seller-mock-key-789", 500)

	// Create a shared key WITHOUT adding marketModel to KeyModel
	sharedKey := "sharedtokenunadded"
	token := model.Token{
		UserId:         h.buyerID,
		Key:            sharedKey,
		Name:           "shared-key",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		KeyType:        model.KeyTypeShared,
	}
	require.NoError(t, h.db.Create(&token).Error)

	// Call market model with shared key without permission
	res := doChatWithKey(t, h.client, h.server.URL, sharedKey, marketModel)
	assert.Equal(t, http.StatusBadRequest, res.status, "shared key calling unadded model must be rejected with 400")
	assert.Contains(t, string(res.body), "该模型不在此密钥可用范围内（普通密钥只能用模型广场模型 / 共享密钥请先在模型市场把该模型加入此密钥）")
	assert.Equal(t, 0, mock.callCount(), "upstream must not be called when request is rejected")
}

// TestKeyDispatch_LegacyKeyMigration_BehaviorPreserved verifies that existing legacy keys
// (with empty KeyType) are migrated to shared keys, expanding 'all' scope to active models,
// and preserving access to catalog models while rejecting unpermitted ones.
func TestKeyDispatch_LegacyKeyMigration_BehaviorPreserved(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)

	catalogModel := "deepseek-flash"
	unaddedModel := "unlisted-secret-model"

	// 1. Ensure catalogModel is in mp_model_pools with status='active'
	var catPool marketplace.ModelPool
	if err := h.db.Where("name = ?", catalogModel).First(&catPool).Error; err != nil {
		require.NoError(t, h.db.Create(&marketplace.ModelPool{
			Name:         catalogModel,
			OriginVendor: "deepseek",
			EnablePlaza:  false,
			Status:       "active",
		}).Error)
	} else {
		require.NoError(t, h.db.Model(&catPool).Updates(map[string]any{
			"enable_plaza": false,
			"status":       "active",
		}).Error)
	}

	// 2. Create a legacy token with empty KeyType and model_scope='all'
	legacyKey := "legacykeyall"
	legacyToken := model.Token{
		UserId:         h.buyerID,
		Key:            legacyKey,
		Name:           "legacy-key",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		KeyType:        "", // Legacy key
	}
	require.NoError(t, h.db.Create(&legacyToken).Error)
	require.NoError(t, marketplace.UpsertKeyPref(h.db, int64(legacyToken.Id), marketplace.KeyPrefInput{
		ModelScope: marketplace.ModelScopeAll,
	}))

	// 3. Set up marketplace listing for catalogModel
	mock := newMockUpstream(t, writeChatSuccessFor(catalogModel, "hello from legacy", chatUsageJSON{PromptTokens: 10, CompletionTokens: 5}))
	h.addListing(mock.url(), "sk-seller-mock-key-legacy", 500)

	// 4. Run migration
	require.NoError(t, marketplace.MigrateKeyTypes(h.db))

	// Legacy keys default to standard, never shared (an old rule flipped them
	// to market-only on every boot).
	var updatedToken model.Token
	require.NoError(t, h.db.First(&updatedToken, legacyToken.Id).Error)
	assert.Equal(t, model.KeyTypeStandard, updatedToken.KeyType, "legacy token must default to standard")

	// Re-running the migration on a key the user made shared, or made
	// standard, must leave it alone.
	require.NoError(t, h.db.Model(&model.Token{}).Where("id = ?", legacyToken.Id).Update("key_type", model.KeyTypeShared).Error)
	require.NoError(t, marketplace.MigrateKeyTypes(h.db))
	require.NoError(t, h.db.First(&updatedToken, legacyToken.Id).Error)
	assert.Equal(t, model.KeyTypeShared, updatedToken.KeyType, "migration must not change an explicit shared key")
	require.NoError(t, h.db.Model(&model.Token{}).Where("id = ?", legacyToken.Id).Update("key_type", model.KeyTypeStandard).Error)
	require.NoError(t, marketplace.MigrateKeyTypes(h.db))
	require.NoError(t, h.db.First(&updatedToken, legacyToken.Id).Error)
	assert.Equal(t, model.KeyTypeStandard, updatedToken.KeyType, "migration must not turn a standard key with prefs into shared")
	require.NoError(t, h.db.Model(&model.Token{}).Where("id = ?", legacyToken.Id).Update("key_type", model.KeyTypeShared).Error)

	// Verify catalogModel was expanded into mp_key_models for this key
	allowed, err := marketplace.KeyPrefAllowsModel(h.db, int64(legacyToken.Id), catalogModel)
	require.NoError(t, err)
	assert.True(t, allowed, "catalog model must be expanded and allowed after migration")

	// 5. Test calling catalogModel succeeds
	res := doChatWithKey(t, h.client, h.server.URL, legacyKey, catalogModel)
	assert.Equal(t, http.StatusOK, res.status, "migrated shared key calling catalog model must succeed")
	assert.Contains(t, string(res.body), "hello from legacy")
	assert.Equal(t, 1, mock.callCount())

	// 6. Test calling unaddedModel is rejected
	resUnadded := doChatWithKey(t, h.client, h.server.URL, legacyKey, unaddedModel)
	assert.Equal(t, http.StatusBadRequest, resUnadded.status, "migrated shared key calling unadded model must be rejected")
	assert.Contains(t, string(resUnadded.body), "该模型不在此密钥可用范围内（普通密钥只能用模型广场模型 / 共享密钥请先在模型市场把该模型加入此密钥）")
}
