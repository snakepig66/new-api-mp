package marketplace

import (
	"fmt"

	"gorm.io/gorm"
)

// Multiplier bounds are the single source shared by the backend validators and
// the seller console. 0.001x (the old floor) let a seller dump below cost and
// manipulate the ranking, so the floor now depends on the channel kind:
// official supply keeps 0.3x, plan 0.3x, third-party 0.2x, and the absolute
// floor is 0.1x.
const (
	MultiplierAbsoluteMinMilli   = 100
	MultiplierAbsoluteMaxMilli   = 1000
	MultiplierOfficialMinMilli   = 300
	MultiplierPlanMinMilli       = 300
	MultiplierThirdPartyMinMilli = 200
)

// MultiplierBoundsForTier returns the inclusive [min, max] milli bounds of one
// channel kind. Unknown kinds use the third-party floor.
func MultiplierBoundsForTier(tier string) (int64, int64) {
	switch tier {
	case ChannelKindOfficial:
		return MultiplierOfficialMinMilli, MultiplierAbsoluteMaxMilli
	case ChannelKindPlan:
		return MultiplierPlanMinMilli, MultiplierAbsoluteMaxMilli
	default:
		return MultiplierThirdPartyMinMilli, MultiplierAbsoluteMaxMilli
	}
}

// multiplierBoundsForVendor resolves a listing vendor slug to its channel kind
// and returns the bounds. A missing channel row (custom or legacy fixture) uses
// the third-party floor.
func multiplierBoundsForVendor(db *gorm.DB, vendor string) (int64, int64) {
	if db == nil {
		return MultiplierThirdPartyMinMilli, MultiplierAbsoluteMaxMilli
	}
	var ch ChannelPool
	if err := db.Select("kind").Where("slug = ?", vendor).Take(&ch).Error; err == nil {
		return MultiplierBoundsForTier(ch.Kind)
	}
	return MultiplierThirdPartyMinMilli, MultiplierAbsoluteMaxMilli
}

// validateMultiplierForVendor enforces the kind-specific range. It is called
// inside the write transaction, after the listing/vendor is known.
func validateMultiplierForVendor(db *gorm.DB, vendor string, milli int64) error {
	min, max := multiplierBoundsForVendor(db, vendor)
	if milli < min || milli > max {
		return fmt.Errorf("marketplace: multiplier %d out of range %d..%d for this channel", milli, min, max)
	}
	return nil
}
