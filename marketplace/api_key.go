package marketplace

// api_key.go — 买家 Key 控制台 API (§3.6, 错误格式 §3.10): key preference
// and key-model management. The route group these handlers hang on is
// expected to carry middleware.UserAuth() (wired in api.go's RegisterRoutes
// by the router seam); every handler additionally verifies the target token
// belongs to the logged-in user, answering a uniform 404 for foreign or
// missing token ids so ownership can never be probed. No response from this
// file ever carries key material (§2.3) — mp_key_prefs/mp_key_models hold
// none and the upstream token row is never echoed back.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// RegisterKeyRoutes is the exported one-line wrapper the router seam calls;
// the marketplace package cannot import middleware (import cycle), so the
// UserAuth group is built by the caller and passed in.
func RegisterKeyRoutes(g *gin.RouterGroup) { registerKeyRoutes(g) }

// registerKeyRoutes mounts the §3.6 buyer-key endpoints on g. g is assumed
// to already carry UserAuth (and to be gated on Enabled() by the caller).
// Route wiring into RegisterRoutes is done centrally in api.go.
func registerKeyRoutes(g *gin.RouterGroup) {
	// POST /key creates the token, its preference row and its model list in a
	// single transaction (no partial key). GET/PUT /key/pref handle later
	// edits.
	g.POST("/key", postCreateKey)
	g.PUT("/key/token", putKeyTokenLimits)
	g.GET("/key/pref", getKeyPref)
	g.PUT("/key/pref", putKeyPref)
	g.POST("/key/models", postKeyModel)
	g.DELETE("/key/models", deleteKeyModel)
	g.GET("/key/models", listKeyModels)
}

// OpenAI error-format writer (§3.10): {"error":{"message","type","code"}}.
// type follows the OpenAI convention; code is a stable snake_case machine
// string for the console frontend.
func writeKeyAPIError(c *gin.Context, status int, errType, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"message": message,
		"type":    errType,
		"code":    code,
	}})
}

func writeKeyAPIBadRequest(c *gin.Context, code, message string) {
	writeKeyAPIError(c, http.StatusBadRequest, "invalid_request_error", code, message)
}

func writeKeyAPINotFound(c *gin.Context, code, message string) {
	writeKeyAPIError(c, http.StatusNotFound, "invalid_request_error", code, message)
}

func writeKeyAPIInternal(c *gin.Context, logMsg string) {
	common.SysError(common.LogText("marketplace key api: %s", logMsg))
	writeKeyAPIError(c, http.StatusInternalServerError, "internal_error", "internal_error", "internal error")
}

// keyAPIDB returns the marketplace primary database handle, answering 500
// when the module state is not initialized (Enabled() gating makes this
// unreachable in production).
func keyAPIDB(c *gin.Context) *gorm.DB {
	db, _ := relayState()
	if db == nil {
		writeKeyAPIInternal(c, "marketplace is not initialized")
		return nil
	}
	return db
}

// keyAPIUserID is the defense-in-depth login check behind UserAuth: a
// missing or non-positive user id is answered 401.
func keyAPIUserID(c *gin.Context) (int, bool) {
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	if userID <= 0 {
		writeKeyAPIError(c, http.StatusUnauthorized, "authentication_error", "unauthorized", "authentication required")
		return 0, false
	}
	return userID, true
}

// requireOwnToken loads the upstream token row and confirms it belongs to
// the caller. A foreign id and an unknown id get the identical 404 so the
// response reveals nothing about other users' tokens; the token row itself
// is never written to the response.
func requireOwnToken(c *gin.Context, userID int, tokenID int64) bool {
	if tokenID <= 0 {
		writeKeyAPIBadRequest(c, "invalid_token_id", "token_id must be a positive integer")
		return false
	}
	_, err := model.GetTokenByIds(int(tokenID), userID)
	if err == nil {
		return true
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeKeyAPINotFound(c, "token_not_found", "token not found")
		return false
	}
	writeKeyAPIInternal(c, "query token ownership: "+err.Error())
	return false
}

// parseTokenIDQuery reads the token_id query parameter of the GET handlers.
func parseTokenIDQuery(c *gin.Context) (int64, bool) {
	raw := c.Query("token_id")
	tokenID, err := strconv.ParseInt(raw, 10, 64)
	if raw == "" || err != nil {
		writeKeyAPIBadRequest(c, "invalid_token_id", "token_id query parameter must be a positive integer")
		return 0, false
	}
	return tokenID, true
}

// ---------------------------------------------------------------------------
// request / response shapes (snake_case; never any key material)
// ---------------------------------------------------------------------------

type keyPrefUpdateRequest struct {
	TokenID         int64   `json:"token_id"`
	Strategy        string  `json:"strategy"`
	ModelScope      string  `json:"model_scope"`
	PriceCapMilli   int64   `json:"price_cap_milli"`
	PreferSellerIDs []int64 `json:"prefer_seller_ids"`
	BlockSellerIDs  []int64 `json:"block_seller_ids"`
	OnlySellerID    int64   `json:"only_seller_id"`
	AllowFallback   bool    `json:"allow_fallback"`
	RPM             int     `json:"rpm"`
	OfficialOnly    bool    `json:"official_only"`
}

type keyModelRequest struct {
	TokenID       int64  `json:"token_id"`
	Model         string `json:"model"`
	PriceCapMilli int64  `json:"price_cap_milli"`
}

type keyModelJSON struct {
	Model         string `json:"model"`
	PriceCapMilli int64  `json:"price_cap_milli"`
	AddedAt       int64  `json:"added_at"` // unix seconds
}

type keyPrefJSON struct {
	TokenID         int64          `json:"token_id"`
	Strategy        string         `json:"strategy"`
	ModelScope      string         `json:"model_scope"`
	PriceCapMilli   int64          `json:"price_cap_milli"`
	PreferSellerIDs []int64        `json:"prefer_seller_ids"`
	BlockSellerIDs  []int64        `json:"block_seller_ids"`
	OnlySellerID    int64          `json:"only_seller_id"`
	AllowFallback   bool           `json:"allow_fallback"`
	RPM             int            `json:"rpm"`
	OfficialOnly    bool           `json:"official_only"`
	Models          []keyModelJSON `json:"models"`
}

func toKeyModelJSON(rows []KeyModel) []keyModelJSON {
	out := make([]keyModelJSON, 0, len(rows))
	for _, r := range rows {
		out = append(out, keyModelJSON{
			Model:         r.Model,
			PriceCapMilli: r.PriceCapMilli,
			AddedAt:       r.CreatedAt.Unix(),
		})
	}
	return out
}

func toKeyPrefJSON(pref KeyPref, models []KeyModel) keyPrefJSON {
	scope := pref.ModelScope
	if scope == "" {
		scope = ModelScopeSelected
	}
	return keyPrefJSON{
		TokenID:         pref.TokenID,
		Strategy:        pref.Strategy,
		ModelScope:      scope,
		PriceCapMilli:   pref.PriceCapMilli,
		PreferSellerIDs: pref.PreferSellerIDs,
		BlockSellerIDs:  pref.BlockSellerIDs,
		OnlySellerID:    pref.OnlySellerID,
		AllowFallback:   pref.AllowFallback,
		RPM:             pref.RPM,
		OfficialOnly:    pref.OfficialOnly,
		Models:          toKeyModelJSON(models),
	}
}

// ---------------------------------------------------------------------------
// handlers
// ---------------------------------------------------------------------------

// getKeyPref answers GET /key/pref?token_id= with the preference row plus
// the key's model list (§3.6). A token without a saved preference is a 404.
func getKeyPref(c *gin.Context) {
	userID, ok := keyAPIUserID(c)
	if !ok {
		return
	}
	tokenID, ok := parseTokenIDQuery(c)
	if !ok {
		return
	}
	if !requireOwnToken(c, userID, tokenID) {
		return
	}
	db := keyAPIDB(c)
	if db == nil {
		return
	}
	pref, err := GetKeyPref(db, tokenID)
	if errors.Is(err, ErrKeyPrefNotFound) {
		writeKeyAPINotFound(c, "key_pref_not_found", "no preference saved for this token")
		return
	}
	if err != nil {
		writeKeyAPIInternal(c, "get key pref: "+err.Error())
		return
	}
	models, err := ListKeyModels(db, tokenID)
	if err != nil {
		writeKeyAPIInternal(c, "list key models: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, toKeyPrefJSON(pref, models))
}

// putKeyPref answers PUT /key/pref with an upsert of the token's preference
// (§3.6); the body is KeyPrefInput plus token_id.
func putKeyPref(c *gin.Context) {
	userID, ok := keyAPIUserID(c)
	if !ok {
		return
	}
	var req keyPrefUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeKeyAPIBadRequest(c, "bad_request_body", "invalid request body: "+err.Error())
		return
	}
	if !requireOwnToken(c, userID, req.TokenID) {
		return
	}
	db := keyAPIDB(c)
	if db == nil {
		return
	}
	in := KeyPrefInput{
		Strategy:        req.Strategy,
		ModelScope:      req.ModelScope,
		PriceCapMilli:   req.PriceCapMilli,
		PreferSellerIDs: req.PreferSellerIDs,
		BlockSellerIDs:  req.BlockSellerIDs,
		OnlySellerID:    req.OnlySellerID,
		AllowFallback:   req.AllowFallback,
		RPM:             req.RPM,
		OfficialOnly:    req.OfficialOnly,
	}
	if err := UpsertKeyPref(db, req.TokenID, in); err != nil {
		writeKeyAPIBadRequest(c, "invalid_preference", err.Error())
		return
	}
	pref, err := GetKeyPref(db, req.TokenID)
	if err != nil {
		writeKeyAPIInternal(c, "reload key pref: "+err.Error())
		return
	}
	models, err := ListKeyModels(db, req.TokenID)
	if err != nil {
		writeKeyAPIInternal(c, "list key models: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, toKeyPrefJSON(pref, models))
}

// postKeyModel answers POST /key/models by adding one model to the key
// (§3.6), optionally with a per-model price cap.
func postKeyModel(c *gin.Context) {
	userID, ok := keyAPIUserID(c)
	if !ok {
		return
	}
	var req keyModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeKeyAPIBadRequest(c, "bad_request_body", "invalid request body: "+err.Error())
		return
	}
	if !requireOwnToken(c, userID, req.TokenID) {
		return
	}
	db := keyAPIDB(c)
	if db == nil {
		return
	}
	err := AddKeyModel(db, req.TokenID, req.Model, req.PriceCapMilli)
	switch {
	case err == nil:
	case errors.Is(err, ErrModelAlreadyAdded):
		writeKeyAPIBadRequest(c, "model_already_added", err.Error())
		return
	case errors.Is(err, ErrNoListingAvailable):
		writeKeyAPIBadRequest(c, "no_listing_available", "this model has no supplier on the marketplace yet")
		return
	default:
		writeKeyAPIBadRequest(c, "invalid_key_model", err.Error())
		return
	}
	models, err := ListKeyModels(db, req.TokenID)
	if err != nil {
		writeKeyAPIInternal(c, "list key models: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": toKeyModelJSON(models)})
}

// deleteKeyModel answers DELETE /key/models with a JSON body
// {token_id, model}; removing a model the key never added is a 404.
func deleteKeyModel(c *gin.Context) {
	userID, ok := keyAPIUserID(c)
	if !ok {
		return
	}
	var req keyModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeKeyAPIBadRequest(c, "bad_request_body", "invalid request body: "+err.Error())
		return
	}
	if !requireOwnToken(c, userID, req.TokenID) {
		return
	}
	db := keyAPIDB(c)
	if db == nil {
		return
	}
	err := RemoveKeyModel(db, req.TokenID, req.Model)
	if errors.Is(err, ErrModelNotAdded) {
		writeKeyAPINotFound(c, "model_not_added", err.Error())
		return
	}
	if err != nil {
		writeKeyAPIInternal(c, "remove key model: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// listKeyModels answers GET /key/models?token_id= with the key's model list.
func listKeyModels(c *gin.Context) {
	userID, ok := keyAPIUserID(c)
	if !ok {
		return
	}
	tokenID, ok := parseTokenIDQuery(c)
	if !ok {
		return
	}
	if !requireOwnToken(c, userID, tokenID) {
		return
	}
	db := keyAPIDB(c)
	if db == nil {
		return
	}
	models, err := ListKeyModels(db, tokenID)
	if err != nil {
		writeKeyAPIInternal(c, "list key models: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": toKeyModelJSON(models)})
}
