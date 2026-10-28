package marketplace

// seller_models_edit.go — ShareLLM-style model editing on an existing channel.
//
//	POST   /seller/listings/:id/models/upstream  fetch the key's upstream models + priced pool
//	POST   /seller/listings/:id/models           add models (verified before they take traffic)
//	DELETE /seller/listings/:id/models/:model    remove a model (new requests only)
//
// Mid-flight semantics:
//   - an added model is stored disabled with VerifyState=pending; a real call
//     verifies it asynchronously and only a pass enables it, so the selector
//     never routes to an unverified model;
//   - a removed model leaves Listing.Models and its ListingModel row at once,
//     so the selector stops picking it for new requests, while requests already
//     in flight keep their RelaySession snapshot and settle normally;
//   - price (multiplier, coefficient, price-book instant) is locked at the
//     pre-freeze, so a later edit never changes an in-flight request's charge.

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Verify states of a ListingModel row.
const (
	ListingModelVerifyPending = "pending"
	ListingModelVerifyFailed  = "failed"
)

// ErrLastListingModel refuses removing the only model of a listing.
var ErrLastListingModel = errors.New("marketplace: a listing must keep at least one model")

func registerSellerModelEditRoutes(g *gin.RouterGroup) {
	g.POST("/seller/listings/:id/models/upstream", postSellerListingUpstreamModels)
	g.POST("/seller/listings/:id/models", postSellerListingModels)
	g.DELETE("/seller/listings/:id/models/:model", deleteSellerListingModel)
}

// listingKeyAndVendor loads the listing's decrypted key and vendor.
func listingKeyAndVendor(db *gorm.DB, listingID int64) (Listing, Vendor, string, error) {
	var l Listing
	if err := db.Where("id = ?", listingID).Take(&l).Error; err != nil {
		return Listing{}, Vendor{}, "", err
	}
	vendor, ok := lookupVendorForDB(db, l.Vendor)
	if !ok {
		return Listing{}, Vendor{}, "", ErrVendorNotWhitelisted
	}
	apiKey, err := DecryptListingKey(l.KeyCiphertext)
	if err != nil {
		return Listing{}, Vendor{}, "", err
	}
	return l, vendor, apiKey, nil
}

// postSellerListingUpstreamModels serves POST /seller/listings/:id/models/upstream.
func postSellerListingUpstreamModels(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	id, ok := sellerIDParam(c)
	if !ok {
		return
	}
	if _, ok := requireOwnListing(c, db, id, sellerID); !ok {
		return
	}
	_, vendor, apiKey, err := listingKeyAndVendor(db, id)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("listing key or channel is unavailable"))
		return
	}
	client, limiter := sellerVerifyResources()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if limiter != nil {
		if err := limiter.Acquire(ctx); err != nil {
			common.ApiErrorStatus(c, http.StatusTooManyRequests, errors.New("verification is busy, try again later"))
			return
		}
		defer limiter.Release()
	}
	keyRes := VerifyChannelKey(ctx, client, vendor, apiKey)
	catalog := sellerModelCatalogFor(db, keyRes.Models, time.Now())
	common.ApiSuccess(c, gin.H{
		"key_valid":       keyRes.KeyValid,
		"fail_reason":     stripKey(keyRes.FailReason, apiKey),
		"upstream_models": catalog.UpstreamModels,
		"pool_models":     catalog.PoolModels,
		"coefficients":    poolCoefficients(db, vendor.ID),
	})
}

type sellerAddModelsRequest struct {
	Models []sellerModelConfig `json:"models"`
}

// AddListingModels appends models to a listing. Each must be a priced pool
// model not already listed; rows start disabled and pending verification.
// Returns the added standard model ids.
func AddListingModels(db *gorm.DB, listingID, sellerID int64, configs []sellerModelConfig, now time.Time) ([]string, error) {
	listing, err := GetListingForSeller(db, listingID, sellerID)
	if err != nil {
		return nil, err
	}
	if listing.Status == ListingBanned {
		return nil, ErrInvalidTransition
	}
	added := make([]string, 0, len(configs))
	rows := make([]ListingModel, 0, len(configs))
	mappingAdds := make(map[string]string, len(configs))
	for _, mc := range configs {
		name := strings.TrimSpace(mc.Model)
		if name == "" || slices.Contains(listing.Models, name) || slices.Contains(added, name) {
			continue
		}
		if _, ok := sellableModelPrice(db, name, now); !ok {
			return nil, errors.New("模型 " + name + " 不在平台模型池或没有官方价格")
		}
		milli := mc.MultiplierMilli
		if milli <= 0 {
			milli = listing.MultiplierMilli
		}
		if err := validateMultiplierForVendor(db, listing.Vendor, milli); err != nil {
			return nil, err
		}
		upstream := strings.TrimSpace(mc.UpstreamModel)
		if upstream == "" {
			upstream = ListingUpstreamModel(db, 0, listing.Vendor, name)
		}
		added = append(added, name)
		mappingAdds[name] = upstream
		rows = append(rows, ListingModel{
			ListingID:       listingID,
			Model:           name,
			MultiplierMilli: milli,
			Enabled:         false,
			UpstreamModel:   upstream,
			VerifyState:     ListingModelVerifyPending,
			DisabledReason:  "verifying",
			DisabledDetail:  "新增模型验证中，通过后开始接流量",
		})
	}
	if len(added) == 0 {
		return added, nil
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		// A row left by an earlier remove would collide on the unique index.
		if err := tx.Where("listing_id = ? AND model IN ?", listingID, added).Delete(&ListingModel{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&rows).Error; err != nil {
			return err
		}
		models := append(slices.Clone(listing.Models), added...)
		if err := tx.Model(&Listing{}).Where("id = ?", listingID).Select("Models").Updates(&Listing{Models: models}).Error; err != nil {
			return err
		}
		return updateListingChannelModels(tx, listing.ChannelID, models, mappingAdds, nil)
	})
	if err != nil {
		return nil, err
	}
	writeListingAuditLogged(db, listingID, sellerID, "models_added", map[string]any{"models": added})
	afterListingModelsChanged(db, listingID)
	return added, nil
}

// RemoveListingModel drops one model from a listing for new requests.
func RemoveListingModel(db *gorm.DB, listingID, sellerID int64, modelName string) error {
	listing, err := GetListingForSeller(db, listingID, sellerID)
	if err != nil {
		return err
	}
	modelName = strings.TrimSpace(modelName)
	if !slices.Contains(listing.Models, modelName) {
		return ErrListingNotFound
	}
	if len(listing.Models) <= 1 {
		return ErrLastListingModel
	}
	models := make([]string, 0, len(listing.Models)-1)
	for _, m := range listing.Models {
		if m != modelName {
			models = append(models, m)
		}
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Listing{}).Where("id = ?", listingID).Select("Models").Updates(&Listing{Models: models}).Error; err != nil {
			return err
		}
		if err := tx.Where("listing_id = ? AND model = ?", listingID, modelName).Delete(&ListingModel{}).Error; err != nil {
			return err
		}
		return updateListingChannelModels(tx, listing.ChannelID, models, nil, []string{modelName})
	})
	if err != nil {
		return err
	}
	writeListingAuditLogged(db, listingID, sellerID, "model_removed", map[string]any{"model": modelName})
	afterListingModelsChanged(db, listingID)
	return nil
}

// updateListingChannelModels keeps the listing's new-api channel row (models
// list and model_mapping) in step with Listing.Models.
func updateListingChannelModels(tx *gorm.DB, channelID int64, models []string, add map[string]string, remove []string) error {
	if channelID <= 0 {
		return nil
	}
	var ch model.Channel
	if err := tx.Select("id", "model_mapping").Where("id = ?", channelID).Take(&ch).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	mapping := map[string]string{}
	if raw := strings.TrimSpace(ch.GetModelMapping()); raw != "" {
		_ = common.Unmarshal([]byte(raw), &mapping)
	}
	for k, v := range add {
		mapping[k] = v
	}
	for _, k := range remove {
		delete(mapping, k)
	}
	encoded, err := common.Marshal(mapping)
	if err != nil {
		return err
	}
	return tx.Model(&model.Channel{}).Where("id = ?", channelID).Updates(map[string]any{
		"models":        strings.Join(models, ","),
		"model_mapping": string(encoded),
	}).Error
}

// markListingModelVerified records the outcome of a pending model's real
// verification call: a pass enables it, a failure keeps it off with the reason.
// Rows no longer pending (removed or re-added meanwhile) are left alone.
func markListingModelVerified(db *gorm.DB, listingID int64, m string, ok bool, reason string) {
	q := db.Model(&ListingModel{}).Where("listing_id = ? AND model = ? AND verify_state = ?",
		listingID, m, ListingModelVerifyPending)
	if !ok {
		_ = q.Updates(map[string]any{
			"verify_state":    ListingModelVerifyFailed,
			"disabled_reason": "verify_failed",
			"disabled_detail": reason,
			"disabled_at":     time.Now(),
		}).Error
		return
	}
	_ = q.Updates(map[string]any{
		"enabled":         true,
		"verify_state":    "",
		"auto_disabled":   false,
		"disabled_reason": "",
		"disabled_detail": "",
		"disabled_at":     nil,
	}).Error
}

// verifyAddedListingModels runs one real call per pending model and enables
// the ones that pass. Failures stay disabled with the reason.
func verifyAddedListingModels(db *gorm.DB, listingID int64, models []string) {
	defer afterListingModelsChanged(db, listingID)
	listing, vendor, apiKey, err := listingKeyAndVendor(db, listingID)
	if err != nil {
		for _, m := range models {
			markListingModelVerified(db, listingID, m, false, "无法读取渠道 Key，未能验证")
		}
		return
	}
	client, limiter := sellerVerifyResources()
	base := VendorOpenAIBase(vendor)
	for _, m := range models {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		if limiter != nil {
			if err := limiter.Acquire(ctx); err != nil {
				cancel()
				markListingModelVerified(db, listingID, m, false, "验证队列繁忙，请稍后重新添加")
				continue
			}
		}
		upstream := ListingUpstreamModel(db, listing.ID, listing.Vendor, m)
		ok, _, _, reason := probeModelCall(ctx, client, base, upstream, apiKey)
		if limiter != nil {
			limiter.Release()
		}
		cancel()
		markListingModelVerified(db, listingID, m, ok, stripKey(reason, apiKey))
	}
}

// afterListingModelsChanged refreshes the shadow tables and the caches that
// decide which models buyers see.
func afterListingModelsChanged(db *gorm.DB, listingID int64) {
	mirrorListingUpdateLogged(db, listingID)
	resetBuyerModelVisibilityCache()
	resetPublicStatsCache()
}

// postSellerListingModels serves POST /seller/listings/:id/models.
func postSellerListingModels(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	id, ok := sellerIDParam(c)
	if !ok {
		return
	}
	if _, ok := requireOwnListing(c, db, id, sellerID); !ok {
		return
	}
	var req sellerAddModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Models) == 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("models is required"))
		return
	}
	added, err := AddListingModels(db, id, sellerID, req.Models, time.Now())
	if err != nil {
		if errors.Is(err, ErrInvalidTransition) {
			common.ApiErrorStatus(c, http.StatusConflict, errors.New("listing cannot be edited"))
			return
		}
		common.ApiErrorStatus(c, http.StatusBadRequest, err)
		return
	}
	common.ApiSuccess(c, gin.H{"added": added, "verifying": added})
	if len(added) > 0 && sellerAsyncVerify {
		go verifyAddedListingModels(db, id, added)
	}
}

// sellerAsyncVerify lets tests run verification synchronously.
var sellerAsyncVerify = true

// deleteSellerListingModel serves DELETE /seller/listings/:id/models/:model.
func deleteSellerListingModel(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	id, ok := sellerIDParam(c)
	if !ok {
		return
	}
	if _, ok := requireOwnListing(c, db, id, sellerID); !ok {
		return
	}
	if err := RemoveListingModel(db, id, sellerID, c.Param("model")); err != nil {
		switch {
		case errors.Is(err, ErrLastListingModel):
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("至少保留一个模型；要下架整个渠道请暂停或删除渠道"))
		case errors.Is(err, ErrListingNotFound):
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("model not found on this listing"))
		default:
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to remove model"))
		}
		return
	}
	common.ApiSuccess(c, gin.H{"removed": c.Param("model")})
}
