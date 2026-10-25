package marketplace

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// keyAPIFixture stands up the §3.6 buyer-key console API against an isolated
// database: upstream users/tokens/channels tables, marketplace tables, a
// header-driven stand-in for middleware.UserAuth, and the package-level
// relay state pointed at the test handle.
type keyAPIFixture struct {
	db            *gorm.DB
	engine        *gin.Engine
	buyerID       int
	strangerID    int
	sellerID      int64
	tokenID       int
	strangerTokID int
}

// headerUserAuth replaces middleware.UserAuth in tests: the X-Test-User-Id
// header plays the dashboard session, anything else is a 401 in the same
// OpenAI error shape the handlers use.
func headerUserAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("X-Test-User-Id"))
		id, err := strconv.Atoi(raw)
		if raw == "" || err != nil || id <= 0 {
			writeKeyAPIError(c, http.StatusUnauthorized, "authentication_error", "unauthorized", "authentication required")
			return
		}
		common.SetContextKey(c, constant.ContextKeyUserId, id)
		c.Next()
	}
}

func newKeyAPIFixture(t *testing.T) *keyAPIFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	require.NoError(t, SeedPrices(db))

	oldDB := model.DB
	model.DB = db
	oldEnabled := enabled
	enabled = true
	SetAnonSecret("api-key-test-secret")

	relayStateMu.Lock()
	oldRelayDB, oldSelector, oldCal := relayDB, defaultSelector, defaultCalendar
	relayDB = db
	relayStateMu.Unlock()
	t.Cleanup(func() {
		model.DB = oldDB
		enabled = oldEnabled
		relayStateMu.Lock()
		relayDB, defaultSelector, defaultCalendar = oldRelayDB, oldSelector, oldCal
		relayStateMu.Unlock()
	})

	buyer := model.User{Username: "buyer", Password: "placeholder-pass", AffCode: "buyer"}
	require.NoError(t, db.Create(&buyer).Error)
	stranger := model.User{Username: "stranger", Password: "placeholder-pass", AffCode: "stranger"}
	require.NoError(t, db.Create(&stranger).Error)
	seller := model.User{Username: "seller", Password: "placeholder-pass", AffCode: "seller"}
	require.NoError(t, db.Create(&seller).Error)

	buyerToken := model.Token{UserId: buyer.Id, Key: "sk-api-key-test-buyer", Name: "buyer-main", Status: 1}
	require.NoError(t, db.Create(&buyerToken).Error)
	strangerToken := model.Token{UserId: stranger.Id, Key: "sk-api-key-test-stranger", Name: "stranger-main", Status: 1}
	require.NoError(t, db.Create(&strangerToken).Error)

	engine := gin.New()
	registerKeyRoutes(engine.Group("/api/marketplace", headerUserAuth()))

	return &keyAPIFixture{
		db:            db,
		engine:        engine,
		buyerID:       buyer.Id,
		strangerID:    stranger.Id,
		sellerID:      int64(seller.Id),
		tokenID:       buyerToken.Id,
		strangerTokID: strangerToken.Id,
	}
}

// addListing opens one live deepseek-flash listing so AddKeyModel has a
// supplier to accept the model (§3.6).
func (f *keyAPIFixture) addListing(t *testing.T) {
	t.Helper()
	key := uniqueTestKey("sk-api-key-test-listing")
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).Update("status", ListingLive).Error)
}

// do issues one request against the fixture engine; userID <= 0 sends no
// auth header (the unauthenticated case).
func (f *keyAPIFixture) do(t *testing.T, method, path string, body any, userID int) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := common.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if userID > 0 {
		req.Header.Set("X-Test-User-Id", strconv.Itoa(userID))
	}
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

func decodeKeyAPIBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &v))
	return v
}

// TestAPIKeyPrefAndModelsFlow walks the whole §3.6 console flow: save a
// preference, read it back, update it, add a model, list it, remove it.
func TestAPIKeyPrefAndModelsFlow(t *testing.T) {
	f := newKeyAPIFixture(t)
	f.addListing(t)
	tid := strconv.Itoa(f.tokenID)

	// PUT creates the preference.
	w := f.do(t, http.MethodPut, "/api/marketplace/key/pref", map[string]any{
		"token_id": f.tokenID, "strategy": "cheapest", "price_cap_milli": 500, "rpm": 30,
	}, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeKeyAPIBody(t, w)
	assert.Equal(t, "cheapest", body["strategy"])
	assert.EqualValues(t, 500, body["price_cap_milli"])
	assert.EqualValues(t, 30, body["rpm"])
	assert.Empty(t, body["models"])

	// GET reads the same values back.
	w = f.do(t, http.MethodGet, "/api/marketplace/key/pref?token_id="+tid, nil, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	body = decodeKeyAPIBody(t, w)
	assert.Equal(t, "cheapest", body["strategy"])
	assert.EqualValues(t, f.tokenID, body["token_id"])

	// A second PUT updates the row in place.
	w = f.do(t, http.MethodPut, "/api/marketplace/key/pref", map[string]any{
		"token_id": f.tokenID, "strategy": "stable",
	}, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "stable", decodeKeyAPIBody(t, w)["strategy"])

	// POST adds a model with a per-model price cap.
	w = f.do(t, http.MethodPost, "/api/marketplace/key/models", map[string]any{
		"token_id": f.tokenID, "model": "deepseek-flash", "price_cap_milli": 300,
	}, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	models, ok := decodeKeyAPIBody(t, w)["models"].([]any)
	require.True(t, ok)
	require.Len(t, models, 1)
	first, ok := models[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "deepseek-flash", first["model"])
	assert.EqualValues(t, 300, first["price_cap_milli"])

	// GET models lists the new row.
	w = f.do(t, http.MethodGet, "/api/marketplace/key/models?token_id="+tid, nil, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, decodeKeyAPIBody(t, w)["models"], 1)

	// DELETE removes it.
	w = f.do(t, http.MethodDelete, "/api/marketplace/key/models", map[string]any{
		"token_id": f.tokenID, "model": "deepseek-flash",
	}, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, true, decodeKeyAPIBody(t, w)["deleted"])

	// The list is empty again, in both the list endpoint and the pref read.
	w = f.do(t, http.MethodGet, "/api/marketplace/key/models?token_id="+tid, nil, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeKeyAPIBody(t, w)["models"])
	w = f.do(t, http.MethodGet, "/api/marketplace/key/pref?token_id="+tid, nil, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeKeyAPIBody(t, w)["models"])

	// §2.3: no response ever carries key material.
	assert.NotContains(t, w.Body.String(), "sk-api-key-test")
}

// TestAPIKeyOwnershipAndAuth covers the permission rules: a foreign token id
// is a uniform 404 that reveals nothing, and a missing login is a 401.
func TestAPIKeyOwnershipAndAuth(t *testing.T) {
	f := newKeyAPIFixture(t)
	f.addListing(t)

	// Give the buyer a preference so a leak would have content to show.
	w := f.do(t, http.MethodPut, "/api/marketplace/key/pref", map[string]any{
		"token_id": f.tokenID, "strategy": "cheapest", "rpm": 10,
	}, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)

	// A stranger operating the buyer's token id gets 404 on every endpoint,
	// and the body carries nothing about the token or its preferences.
	tid := strconv.Itoa(f.tokenID)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/marketplace/key/pref?token_id=" + tid, nil},
		{http.MethodPut, "/api/marketplace/key/pref", map[string]any{"token_id": f.tokenID, "strategy": "stable"}},
		{http.MethodPost, "/api/marketplace/key/models", map[string]any{"token_id": f.tokenID, "model": "deepseek-flash"}},
		{http.MethodDelete, "/api/marketplace/key/models", map[string]any{"token_id": f.tokenID, "model": "deepseek-flash"}},
		{http.MethodGet, "/api/marketplace/key/models?token_id=" + tid, nil},
	} {
		w := f.do(t, tc.method, tc.path, tc.body, f.strangerID)
		assert.Equal(t, http.StatusNotFound, w.Code, "%s %s", tc.method, tc.path)
		assert.NotContains(t, w.Body.String(), "cheapest")
		assert.NotContains(t, w.Body.String(), "sk-api-key-test")
	}

	// The stranger's own token works — the 404 was ownership, not existence.
	w = f.do(t, http.MethodPut, "/api/marketplace/key/pref", map[string]any{
		"token_id": f.strangerTokID, "strategy": "stable",
	}, f.strangerID)
	assert.Equal(t, http.StatusOK, w.Code)

	// No login → 401.
	w = f.do(t, http.MethodGet, "/api/marketplace/key/pref?token_id="+tid, nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	w = f.do(t, http.MethodPut, "/api/marketplace/key/pref", map[string]any{"token_id": f.tokenID}, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAPIKeyBadRequest covers the §3.10 error shape: every client error is a
// JSON {"error":{"message","type","code"}} with a 4xx status.
func TestAPIKeyBadRequest(t *testing.T) {
	f := newKeyAPIFixture(t)
	f.addListing(t)
	tid := strconv.Itoa(f.tokenID)

	// Malformed JSON body → 400 with the OpenAI error shape.
	req := httptest.NewRequest(http.MethodPut, "/api/marketplace/key/pref", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User-Id", strconv.Itoa(f.buyerID))
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	errObj, ok := decodeKeyAPIBody(t, w)["error"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, errObj["message"])
	assert.NotEmpty(t, errObj["type"])
	assert.NotEmpty(t, errObj["code"])

	// Strategy outside the §3.6 enum → 400.
	w = f.do(t, http.MethodPut, "/api/marketplace/key/pref", map[string]any{
		"token_id": f.tokenID, "strategy": "fastest",
	}, f.buyerID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 只用某一家 conflicting with the prefer list → 400.
	w = f.do(t, http.MethodPut, "/api/marketplace/key/pref", map[string]any{
		"token_id": f.tokenID, "only_seller_id": 11, "prefer_seller_ids": []int64{11},
	}, f.buyerID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Missing token_id query parameter → 400.
	w = f.do(t, http.MethodGet, "/api/marketplace/key/pref", nil, f.buyerID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// No preference saved yet → 404.
	w = f.do(t, http.MethodGet, "/api/marketplace/key/pref?token_id="+tid, nil, f.buyerID)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "key_pref_not_found")

	// Adding a model nobody sells → 400.
	w = f.do(t, http.MethodPost, "/api/marketplace/key/models", map[string]any{
		"token_id": f.tokenID, "model": "no-such-model",
	}, f.buyerID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "no_listing_available")

	// Adding the same model twice → 400 model_already_added.
	w = f.do(t, http.MethodPost, "/api/marketplace/key/models", map[string]any{
		"token_id": f.tokenID, "model": "deepseek-flash",
	}, f.buyerID)
	require.Equal(t, http.StatusOK, w.Code)
	w = f.do(t, http.MethodPost, "/api/marketplace/key/models", map[string]any{
		"token_id": f.tokenID, "model": "deepseek-flash",
	}, f.buyerID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "model_already_added")

	// Removing a model the key never added → 404.
	w = f.do(t, http.MethodDelete, "/api/marketplace/key/models", map[string]any{
		"token_id": f.tokenID, "model": "deepseek-v4-pro",
	}, f.buyerID)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "model_not_added")
}
