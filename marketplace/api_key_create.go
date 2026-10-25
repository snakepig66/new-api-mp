package marketplace

// api_key_create.go — one-shot buyer key creation (POST /key).
//
// The console dialog expands a routing preset on the client; this endpoint
// receives only the resulting concrete fields and writes the upstream token,
// the marketplace preference row and the selected-model rows in ONE
// transaction. A bad model or preference creates nothing, so there is no
// "wizard.partial" state where the key exists without its routing setup.
//
// The full key is returned exactly once in the create response, like
// POST /token/:id/key. It is never logged and never returned by any other
// endpoint in this file.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// keyCreateModel is one selected model in the create payload.
type keyCreateModel struct {
	Model         string `json:"model"`
	PriceCapMilli int64  `json:"price_cap_milli"`
}

// keyCreateRequest is the one-shot POST /key body. Presets are a frontend
// concept: the client sends the expanded fields, so the backend stores only
// the existing token/preference/model concepts plus model_scope.
type keyCreateRequest struct {
	Name            string  `json:"name"`
	Strategy        string  `json:"strategy"`
	ModelScope      string  `json:"model_scope"`
	PriceCapMilli   int64   `json:"price_cap_milli"`
	SpendCapMicro   *int64  `json:"spend_cap_micro"` // null = unlimited
	RPM             int     `json:"rpm"`
	PreferSellerIDs []int64 `json:"prefer_seller_ids"`
	BlockSellerIDs  []int64 `json:"block_seller_ids"`
	OnlySellerID    int64   `json:"only_seller_id"`
	AllowFallback   bool    `json:"allow_fallback"`
	OfficialOnly    bool    `json:"official_only"`
	// ExpiredTime is a Unix second timestamp; -1 or 0 means never.
	ExpiredTime int64            `json:"expired_time"`
	AllowIps    *string          `json:"allow_ips"`
	Models      []keyCreateModel `json:"models"`
}

// postCreateKey creates a buyer key, its routing preference and its selected
// models atomically.
func postCreateKey(c *gin.Context) {
	userID, ok := keyAPIUserID(c)
	if !ok {
		return
	}
	db := keyAPIDB(c)
	if db == nil {
		return
	}
	var req keyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeKeyAPIBadRequest(c, "bad_request_body", "invalid request body: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if len([]rune(name)) > 50 {
		writeKeyAPIBadRequest(c, "name_too_long", "key name must be at most 50 characters")
		return
	}
	maxTokens := operation_setting.GetMaxUserTokens()
	if maxTokens > 0 {
		count, err := model.CountUserTokens(userID)
		if err != nil {
			writeKeyAPIInternal(c, "count user tokens: "+err.Error())
			return
		}
		if int(count) >= maxTokens {
			writeKeyAPIBadRequest(c, "token_limit_reached",
				fmt.Sprintf("maximum number of keys reached (%d)", maxTokens))
			return
		}
	}
	if name == "" {
		var err error
		name, err = nextKeyName(db, userID)
		if err != nil {
			writeKeyAPIInternal(c, "default key name: "+err.Error())
			return
		}
	}

	scope := strings.TrimSpace(req.ModelScope)
	if scope == "" {
		if len(req.Models) > 0 {
			scope = ModelScopeSelected
		} else {
			scope = ModelScopeAll
		}
	}
	if scope != ModelScopeAll && scope != ModelScopeSelected {
		writeKeyAPIBadRequest(c, "invalid_model_scope", ErrKeyModelScope.Error())
		return
	}

	models := make([]KeyModel, 0, len(req.Models))
	if scope == ModelScopeSelected {
		if len(req.Models) == 0 {
			writeKeyAPIBadRequest(c, "models_required",
				"select at least one model or use the all-models scope")
			return
		}
		seen := make(map[string]struct{}, len(req.Models))
		for _, m := range req.Models {
			modelName := strings.TrimSpace(m.Model)
			if modelName == "" {
				writeKeyAPIBadRequest(c, "invalid_key_model", "model must not be empty")
				return
			}
			if _, dup := seen[modelName]; dup {
				continue
			}
			seen[modelName] = struct{}{}
			poolModel, found := ModelPoolByName(db, modelName)
			if !found || poolModel.Status != PoolStatusActive {
				writeKeyAPIBadRequest(c, "unknown_model",
					fmt.Sprintf("model %q is not in the marketplace catalog", modelName))
				return
			}
			if m.PriceCapMilli < 0 || m.PriceCapMilli > 1000 {
				writeKeyAPIBadRequest(c, "invalid_price_cap",
					"per-model price cap must be between 0 and 1000 milli")
				return
			}
			models = append(models, KeyModel{Model: modelName, PriceCapMilli: m.PriceCapMilli})
		}
	}

	in := KeyPrefInput{
		Strategy:        req.Strategy,
		ModelScope:      scope,
		PriceCapMilli:   req.PriceCapMilli,
		PreferSellerIDs: req.PreferSellerIDs,
		BlockSellerIDs:  req.BlockSellerIDs,
		OnlySellerID:    req.OnlySellerID,
		AllowFallback:   req.AllowFallback,
		RPM:             req.RPM,
		OfficialOnly:    req.OfficialOnly,
	}
	if in.Strategy == "" {
		in.Strategy = StrategyBalanced
	}
	if err := in.validate(); err != nil {
		writeKeyAPIBadRequest(c, "invalid_preference", err.Error())
		return
	}

	spendCap := int64(0)
	unlimited := true
	if req.SpendCapMicro != nil && *req.SpendCapMicro > 0 {
		spendCap = *req.SpendCapMicro
		unlimited = false
	}
	if spendCap > int64(int(^uint(0)>>1)) {
		writeKeyAPIBadRequest(c, "invalid_spend_cap", "spend cap is out of range")
		return
	}
	expiredTime, err := normalizeKeyExpiry(req.ExpiredTime)
	if err != nil {
		writeKeyAPIBadRequest(c, "invalid_expired_time", err.Error())
		return
	}
	allowIPs, err := normalizeKeyAllowIPs(keyAllowIPsString(req.AllowIps))
	if err != nil {
		writeKeyAPIBadRequest(c, "invalid_allow_ips", err.Error())
		return
	}
	key, err := common.GenerateKey()
	if err != nil {
		writeKeyAPIInternal(c, "generate token key: "+err.Error())
		return
	}
	token := model.Token{
		UserId:              userID,
		Name:                name,
		Key:                 key,
		Status:              common.TokenStatusEnabled,
		CreatedTime:         common.GetTimestamp(),
		AccessedTime:        common.GetTimestamp(),
		ExpiredTime:         expiredTime,
		AllowIps:            allowIPs,
		RemainQuota:         int(spendCap),
		UnlimitedQuota:      unlimited,
		AllowUserQuotaQuery: true,
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		pref := KeyPref{
			TokenID:         int64(token.Id),
			Strategy:        in.Strategy,
			ModelScope:      scope,
			PriceCapMilli:   in.PriceCapMilli,
			PreferSellerIDs: in.PreferSellerIDs,
			BlockSellerIDs:  in.BlockSellerIDs,
			OnlySellerID:    in.OnlySellerID,
			AllowFallback:   in.AllowFallback,
			RPM:             in.RPM,
			OfficialOnly:    in.OfficialOnly,
		}
		if err := tx.Create(&pref).Error; err != nil {
			return err
		}
		for i := range models {
			models[i].TokenID = int64(token.Id)
			if err := tx.Create(&models[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		var dupErr error
		if isDuplicateKeyError(err) {
			dupErr = errors.New("could not allocate a unique key; retry")
		}
		msg := "create key transaction: " + err.Error()
		if dupErr != nil {
			msg = dupErr.Error()
		}
		writeKeyAPIInternal(c, msg)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":              token.Id,
		"name":            token.Name,
		"key":             key,
		"model_scope":     scope,
		"models":          toKeyModelJSON(models),
		"strategy":        in.Strategy,
		"price_cap_milli": in.PriceCapMilli,
		"spend_cap_micro": spendCap,
		"unlimited_quota": unlimited,
	})
}

// nextKeyName returns the first unused "Key N" for this user (the name is
// optional in the create dialog). The search is bounded so an unusual token
// list cannot cause an unbounded loop.
func nextKeyName(db *gorm.DB, userID int) (string, error) {
	var existing []string
	if err := db.Model(&model.Token{}).Where("user_id = ?", userID).Pluck("name", &existing).Error; err != nil {
		return "", err
	}
	used := make(map[string]struct{}, len(existing))
	for _, name := range existing {
		used[name] = struct{}{}
	}
	for i := 1; i <= len(existing)+100; i++ {
		candidate := fmt.Sprintf("Key %d", i)
		if _, ok := used[candidate]; !ok {
			return candidate, nil
		}
	}
	return fmt.Sprintf("Key %d", len(existing)+1), nil
}
