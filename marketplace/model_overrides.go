package marketplace

import (
	"gorm.io/gorm"
)

// listingModelOverrides is the per-listing, per-model state of a batch of
// listings. Multiplier holds only usable enabled overrides; disabled marks the
// models a seller (or an automatic rule) turned off.
//
// The distinction matters: before this type existed, a disabled row was simply
// ignored and the listing fell back to its base multiplier, so a "disabled"
// model stayed sellable. Every read path that decides whether a listing serves
// a model must consult this state.
type listingModelOverrides struct {
	multiplier   map[int64]map[string]int64
	disabled     map[int64]map[string]bool
	autoDisabled map[int64]map[string]bool
}

// loadListingModelOverrides loads the override rows of every given listing in
// one query. A deployment without the table (partial schema or an early test
// fixture) yields an empty state, which keeps every model at its base
// multiplier and enabled — the historical behaviour.
func loadListingModelOverrides(db *gorm.DB, listingIDs []int64) listingModelOverrides {
	out := listingModelOverrides{
		multiplier:   make(map[int64]map[string]int64),
		disabled:     make(map[int64]map[string]bool),
		autoDisabled: make(map[int64]map[string]bool),
	}
	if db == nil || len(listingIDs) == 0 || !db.Migrator().HasTable(&ListingModel{}) {
		return out
	}
	var rows []ListingModel
	if err := db.Select("listing_id", "model", "multiplier_milli", "enabled", "auto_disabled").
		Where("listing_id IN ?", listingIDs).Find(&rows).Error; err != nil {
		return out
	}
	for _, row := range rows {
		if !row.Enabled {
			byModel := out.disabled[row.ListingID]
			if byModel == nil {
				byModel = make(map[string]bool)
				out.disabled[row.ListingID] = byModel
			}
			byModel[row.Model] = true
			if row.AutoDisabled {
				auto := out.autoDisabled[row.ListingID]
				if auto == nil {
					auto = make(map[string]bool)
					out.autoDisabled[row.ListingID] = auto
				}
				auto[row.Model] = true
			}
			continue
		}
		// A corrupt value must never make pricing reject every request, so it is
		// treated as "no override" and the base multiplier applies.
		if row.MultiplierMilli < 1 || row.MultiplierMilli > 1000 {
			continue
		}
		byModel := out.multiplier[row.ListingID]
		if byModel == nil {
			byModel = make(map[string]int64)
			out.multiplier[row.ListingID] = byModel
		}
		byModel[row.Model] = row.MultiplierMilli
	}
	return out
}

// modelEnabled reports whether the listing may serve the model. A listing with
// no row for the model is enabled.
func (o listingModelOverrides) modelEnabled(listingID int64, model string) bool {
	if byModel := o.disabled[listingID]; byModel != nil && byModel[model] {
		return false
	}
	return true
}

// multiplierFor returns the enabled per-model override, or the listing base
// multiplier when the model has no usable row.
func (o listingModelOverrides) multiplierFor(listingID int64, model string, base int64) int64 {
	if byModel := o.multiplier[listingID]; byModel != nil {
		if milli, ok := byModel[model]; ok {
			return milli
		}
	}
	return base
}

// aggregate summarizes one listing's models for the list row: enabled count,
// auto-disabled count, and the effective multiplier band of the enabled set.
// A listing with every model disabled returns enabled=0 and a zero band.
func (o listingModelOverrides) aggregate(listing Listing) (enabled int, autoDisabled int, minMilli, maxMilli int64) {
	for _, model := range listing.Models {
		if !o.modelEnabled(listing.ID, model) {
			if byModel := o.autoDisabled[listing.ID]; byModel != nil && byModel[model] {
				autoDisabled++
			}
			continue
		}
		enabled++
		milli := o.multiplierFor(listing.ID, model, listing.MultiplierMilli)
		if minMilli == 0 || milli < minMilli {
			minMilli = milli
		}
		if milli > maxMilli {
			maxMilli = milli
		}
	}
	return enabled, autoDisabled, minMilli, maxMilli
}
