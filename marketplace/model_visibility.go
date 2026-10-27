package marketplace

import (
	"sync"
	"time"

	"gorm.io/gorm"
)

// model_visibility.go — what /v1/models may advertise.
//
// A model is "owned" by the marketplace when at least one live/canary listing
// mentions it. An owned model is "available" when at least one of those
// listings is on a verified, active channel AND has not disabled the model.
//
// The distinction matters: a model the seller turned off must disappear from
// /v1/models instead of being advertised and then failing with "no supplier".
// Models the marketplace does not own at all (regular upstream channels) are
// left untouched.

const buyerModelVisibilityTTL = 30 * time.Second

type modelVisibilityState struct {
	owned     map[string]bool
	available map[string]bool
}

var buyerModelVisibilityCache struct {
	mu    sync.Mutex
	at    time.Time
	value modelVisibilityState
	valid bool
}

// resetBuyerModelVisibilityCache drops the snapshot. Model enable/disable,
// listing status changes and deletion call it so the next /v1/models request
// sees the new state immediately.
func resetBuyerModelVisibilityCache() {
	buyerModelVisibilityCache.mu.Lock()
	buyerModelVisibilityCache.valid = false
	buyerModelVisibilityCache.at = time.Time{}
	buyerModelVisibilityCache.value = modelVisibilityState{}
	buyerModelVisibilityCache.mu.Unlock()
}

// FilterBuyerModels removes owned-but-unavailable models from a /v1/models
// response. A nil database or a disabled marketplace returns the input
// unchanged, so non-marketplace deployments keep their model list.
func FilterBuyerModels(models []string) []string {
	if len(models) == 0 || !Enabled() {
		return models
	}
	db, _ := relayState()
	if db == nil {
		return models
	}
	state := cachedModelVisibilityState(db, time.Now())
	out := make([]string, 0, len(models))
	for _, model := range models {
		if !state.owned[model] {
			out = append(out, model)
			continue
		}
		if state.available[model] {
			out = append(out, model)
		}
	}
	return out
}

func cachedModelVisibilityState(db *gorm.DB, now time.Time) modelVisibilityState {
	buyerModelVisibilityCache.mu.Lock()
	defer buyerModelVisibilityCache.mu.Unlock()
	if buyerModelVisibilityCache.valid {
		age := now.Sub(buyerModelVisibilityCache.at)
		if age >= 0 && age < buyerModelVisibilityTTL {
			return buyerModelVisibilityCache.value
		}
	}
	state := loadModelVisibilityState(db)
	buyerModelVisibilityCache.value = state
	buyerModelVisibilityCache.at = now
	buyerModelVisibilityCache.valid = true
	return state
}

func loadModelVisibilityState(db *gorm.DB) modelVisibilityState {
	state := modelVisibilityState{
		owned:     make(map[string]bool),
		available: make(map[string]bool),
	}
	var rows []Listing
	if err := db.Select("id", "vendor", "models").
		Where("status IN ?", []string{ListingLive, ListingCanary}).
		Find(&rows).Error; err != nil {
		return state
	}
	ids := make([]int64, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	overrides := loadListingModelOverrides(db, ids)
	verified := loadVerifiedChannelModels(db)
	for i := range rows {
		listing := &rows[i]
		for _, model := range listing.Models {
			state.owned[model] = true
			if !overrides.modelEnabled(listing.ID, model) {
				continue
			}
			if verified[model][listing.Vendor] {
				state.available[model] = true
			}
		}
	}
	return state
}

// loadVerifiedChannelModels returns model -> channel slug for every active,
// verified qualification on an active channel. It is the batched form of
// loadVerifiedChannels used by the list endpoint, which has no endpoint kind.
func loadVerifiedChannelModels(db *gorm.DB) map[string]map[string]bool {
	out := make(map[string]map[string]bool)
	var channels []ChannelPool
	if err := db.Select("slug").Where("status = ?", PoolStatusActive).Find(&channels).Error; err != nil {
		return out
	}
	if len(channels) == 0 {
		return out
	}
	active := make(map[string]struct{}, len(channels))
	for i := range channels {
		active[channels[i].Slug] = struct{}{}
	}
	var rows []ChannelModel
	if err := db.Select("channel_slug", "model").
		Where("verified = ? AND status = ?", true, PoolStatusActive).
		Find(&rows).Error; err != nil {
		return out
	}
	for i := range rows {
		slug := rows[i].ChannelSlug
		if _, ok := active[slug]; !ok {
			continue
		}
		byChannel := out[rows[i].Model]
		if byChannel == nil {
			byChannel = make(map[string]bool)
			out[rows[i].Model] = byChannel
		}
		byChannel[slug] = true
	}
	return out
}
