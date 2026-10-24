package marketplace

// keyprefs.go — 买家 Key 偏好与模型管理 (§3.6): per-token strategy, price
// cap, prefer/block/only seller lists, the buyer-managed model list with
// per-model price caps, and the buyer-side RPM limit enforced on the relay
// path (BeginDistribute).

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// Key preference / model management errors (§3.6). Messages never carry key
// material (§2.3).
var (
	ErrKeyPrefNotFound   = errors.New("marketplace: key preference not found")
	ErrModelAlreadyAdded = errors.New("marketplace: model already added to this key")
	ErrModelNotAdded     = errors.New("marketplace: model not added to this key")
)

// KeyPrefInput is one write of a buyer key's marketplace routing preferences
// (§3.6).
type KeyPrefInput struct {
	Strategy        string  // cheapest / balanced / stable; empty = balanced
	PriceCapMilli   int64   // 0 = 不限
	PreferSellerIDs []int64 // 优选名单
	BlockSellerIDs  []int64 // 拉黑名单
	OnlySellerID    int64   // >0 = 只用某一家
	AllowFallback   bool    // 只用某一家不可用时是否允许回落 (§3.6)
	RPM             int     // 买家 RPM 限制, 0 = 不限
}

// validate checks the fields that need no database; the strategy default
// (balanced) is applied by UpsertKeyPref before validate runs.
func (in KeyPrefInput) validate() error {
	switch in.Strategy {
	case StrategyCheapest, StrategyBalanced, StrategyStable:
	default:
		return fmt.Errorf("marketplace: invalid strategy %q", in.Strategy)
	}
	if in.PriceCapMilli < 0 {
		return errors.New("marketplace: price cap must be >= 0")
	}
	if in.RPM < 0 {
		return errors.New("marketplace: rpm must be >= 0")
	}
	// §3.6: 「只用某一家」与优选/拉黑互斥 — an only-seller key has no list
	// left to prefer or block.
	if in.OnlySellerID > 0 && (len(in.PreferSellerIDs) > 0 || len(in.BlockSellerIDs) > 0) {
		return errors.New("marketplace: only-seller conflicts with prefer/block lists")
	}
	return nil
}

// UpsertKeyPref creates or replaces the preference row of one buyer token.
// The token_id unique index on mp_key_prefs makes the row 1:1 with the token.
func UpsertKeyPref(db *gorm.DB, tokenID int64, in KeyPrefInput) error {
	if tokenID <= 0 {
		return errors.New("marketplace: token id must be positive")
	}
	if in.Strategy == "" {
		in.Strategy = StrategyBalanced
	}
	if err := in.validate(); err != nil {
		return err
	}
	var pref KeyPref
	err := db.Where("token_id = ?", tokenID).Take(&pref).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		pref = KeyPref{TokenID: tokenID}
	case err != nil:
		return err
	}
	pref.Strategy = in.Strategy
	pref.PriceCapMilli = in.PriceCapMilli
	pref.PreferSellerIDs = in.PreferSellerIDs
	pref.BlockSellerIDs = in.BlockSellerIDs
	pref.OnlySellerID = in.OnlySellerID
	pref.AllowFallback = in.AllowFallback
	pref.RPM = in.RPM
	return db.Save(&pref).Error
}

// GetKeyPref returns the preference row of one buyer token, or
// ErrKeyPrefNotFound when the buyer never saved one.
func GetKeyPref(db *gorm.DB, tokenID int64) (KeyPref, error) {
	var pref KeyPref
	err := db.Where("token_id = ?", tokenID).Take(&pref).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return KeyPref{}, ErrKeyPrefNotFound
	}
	return pref, err
}

// AddKeyModel adds one model to a buyer key with an optional per-model price
// cap (§3.6: 模型由买家自己加到 Key 上，可设该模型的价格上限; cap 0 inherits
// the key-level cap at request time). The model must have at least one
// live/canary listing — adding a model nobody sells would only fail later on
// the relay path with less context.
func AddKeyModel(db *gorm.DB, tokenID int64, model string, priceCapMilli int64) error {
	if tokenID <= 0 {
		return errors.New("marketplace: token id must be positive")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("marketplace: model must not be empty")
	}
	if priceCapMilli < 0 {
		return errors.New("marketplace: price cap must be >= 0")
	}
	var existing int64
	if err := db.Model(&KeyModel{}).Where("token_id = ? AND model = ?", tokenID, model).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return ErrModelAlreadyAdded
	}
	has, err := hasLiveListingForModel(db, model)
	if err != nil {
		return err
	}
	if !has {
		return ErrNoListingAvailable
	}
	return db.Create(&KeyModel{TokenID: tokenID, Model: model, PriceCapMilli: priceCapMilli}).Error
}

// RemoveKeyModel drops one model from a buyer key; a model the key never
// added yields ErrModelNotAdded.
func RemoveKeyModel(db *gorm.DB, tokenID int64, model string) error {
	res := db.Where("token_id = ? AND model = ?", tokenID, model).Delete(&KeyModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrModelNotAdded
	}
	return nil
}

// ListKeyModels returns every model the buyer added to the key, oldest
// addition first.
func ListKeyModels(db *gorm.DB, tokenID int64) ([]KeyModel, error) {
	var rows []KeyModel
	if err := db.Where("token_id = ?", tokenID).Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// buyerRPMWindow is the process-local state of the §3.6 buyer RPM limit,
// keyed by token id. Like the listing-side Selector state (slots, breaker,
// listing RPM) it lives in this process only: a multi-node deployment must
// move it to Redis or equivalent shared storage (second-version item, §8).
var buyerRPMWindow = NewRPMWindow()

// BuyerRPMAllow records one buyer request against the key's RPM budget and
// reports whether it fits (§3.6: 每个买家有自己的 RPM 限制). A key without a
// saved preference, or with RPM = 0, is unlimited. The window is
// best-effort: when the preference cannot be read the request is admitted —
// the relay path queries the same database immediately afterwards and fails
// there with the real error.
func BuyerRPMAllow(tokenID int64, now time.Time) bool {
	db, _ := relayState()
	if db == nil {
		return true
	}
	pref, err := GetKeyPref(db, tokenID)
	if err != nil {
		if !errors.Is(err, ErrKeyPrefNotFound) {
			common.SysError(common.LogText("marketplace: read key pref for buyer rpm: %s", err.Error()))
		}
		return true
	}
	return buyerRPMWindow.Allow(tokenID, pref.RPM, now)
}
