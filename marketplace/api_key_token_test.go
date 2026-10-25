package marketplace

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func TestCreateKeyStoresExpiryAndIPWhitelist(t *testing.T) {
	f := newKeyAPIFixture(t)
	future := common.GetTimestamp() + 86_400
	w := f.do(t, http.MethodPost, "/api/marketplace/key", map[string]any{
		"name":         "limited",
		"model_scope":  "all",
		"expired_time": future,
		"allow_ips":    "203.0.113.10\n198.51.100.0/24, 203.0.113.10",
	}, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	id := int64(apiNum(t, decodeKeyAPIBody(t, w), "id"))

	var token model.Token
	require.NoError(t, f.db.Where("id = ?", id).Take(&token).Error)
	assert.Equal(t, future, token.ExpiredTime)
	require.NotNil(t, token.AllowIps)
	assert.Equal(t, "203.0.113.10\n198.51.100.0/24", *token.AllowIps,
		"duplicates and inline commas are normalized away")
}

func TestCreateKeyRejectsInvalidLimitsAtomically(t *testing.T) {
	f := newKeyAPIFixture(t)
	before := countRows(t, f.db, &model.Token{})

	w := f.do(t, http.MethodPost, "/api/marketplace/key", map[string]any{
		"expired_time": 1,
	}, f.buyerID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_expired_time")

	w = f.do(t, http.MethodPost, "/api/marketplace/key", map[string]any{
		"allow_ips": "definitely-not-an-ip",
	}, f.buyerID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_allow_ips")

	assert.EqualValues(t, before, countRows(t, f.db, &model.Token{}),
		"a rejected limit must not leave a token behind")
}

func TestUpdateKeyTokenLimits(t *testing.T) {
	f := newKeyAPIFixture(t)
	w := f.do(t, http.MethodPost, "/api/marketplace/key", map[string]any{
		"name": "editable",
	}, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	id := int64(apiNum(t, decodeKeyAPIBody(t, w), "id"))

	future := common.GetTimestamp() + 3_600
	w = f.do(t, http.MethodPut, "/api/marketplace/key/token", map[string]any{
		"token_id":     id,
		"expired_time": future,
		"allow_ips":    "10.0.0.1\n10.0.0.0/8",
	}, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	data, ok := decodeKeyAPIBody(t, w)["data"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, future, data["expired_time"])
	assert.Equal(t, "10.0.0.1\n10.0.0.0/8", data["allow_ips"])

	var token model.Token
	require.NoError(t, f.db.Where("id = ?", id).Take(&token).Error)
	assert.Equal(t, future, token.ExpiredTime)
	require.NotNil(t, token.AllowIps)

	// Clearing the whitelist and setting never-expire.
	w = f.do(t, http.MethodPut, "/api/marketplace/key/token", map[string]any{
		"token_id":     id,
		"expired_time": -1,
		"allow_ips":    "",
	}, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, f.db.Where("id = ?", id).Take(&token).Error)
	assert.Equal(t, int64(-1), token.ExpiredTime)
	assert.Nil(t, token.AllowIps)

	// Another user's token is not visible through this endpoint.
	w = f.do(t, http.MethodPut, "/api/marketplace/key/token", map[string]any{
		"token_id":     f.strangerTokID,
		"expired_time": -1,
	}, f.buyerID)
	assert.Equal(t, http.StatusNotFound, w.Code)

	w = f.do(t, http.MethodPut, "/api/marketplace/key/token", map[string]any{
		"token_id":  id,
		"allow_ips": "bad-ip",
	}, f.buyerID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_allow_ips")
}

func TestUpdateKeyExtendingExpiredKeyReenablesIt(t *testing.T) {
	f := newKeyAPIFixture(t)
	expired := model.Token{
		UserId:      f.buyerID,
		Key:         "sk-api-key-test-expired",
		Name:        "expired",
		Status:      common.TokenStatusExpired,
		ExpiredTime: common.GetTimestamp() - 60,
	}
	require.NoError(t, f.db.Create(&expired).Error)

	future := common.GetTimestamp() + 600
	w := f.do(t, http.MethodPut, "/api/marketplace/key/token", map[string]any{
		"token_id":     expired.Id,
		"expired_time": future,
	}, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var stored model.Token
	require.NoError(t, f.db.Where("id = ?", expired.Id).Take(&stored).Error)
	assert.Equal(t, common.TokenStatusEnabled, stored.Status)
	assert.Equal(t, future, stored.ExpiredTime)
}
