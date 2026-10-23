package marketplace

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// ErrNoPrice is returned when no price-book row covers the requested
// model/vendor/tier (or vendor/item) at the requested instant.
var ErrNoPrice = errors.New("marketplace: no price book")

// PriceAt returns the (model, vendor, tier) price book in effect at instant
// at: among rows with effective_at <= at it takes the one with the latest
// effective_at, breaking ties by the higher version. A non-flat tier that has
// no covering row falls back to the flat tier of the same model and vendor;
// when neither exists the error is ErrNoPrice.
func PriceAt(db *gorm.DB, model, vendor, tier string, at time.Time) (PriceBook, error) {
	pb, err := priceAt(db, model, vendor, tier, at)
	if err == nil {
		return pb, nil
	}
	if !errors.Is(err, ErrNoPrice) || tier == TierFlat {
		return PriceBook{}, err
	}
	return priceAt(db, model, vendor, TierFlat, at)
}

// priceAt is the single-tier lookup behind PriceAt.
func priceAt(db *gorm.DB, model, vendor, tier string, at time.Time) (PriceBook, error) {
	var pb PriceBook
	err := db.Where("model = ? AND vendor = ? AND tier = ? AND effective_at <= ?", model, vendor, tier, at).
		Order("effective_at DESC").
		Order("version DESC").
		Take(&pb).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PriceBook{}, ErrNoPrice
		}
		return PriceBook{}, err
	}
	return pb, nil
}

// PerCallPriceAt returns the (vendor, item) per-call unit price in effect at
// instant at (§3.4), using the same effective_at DESC, version DESC rule as
// PriceAt. The version is returned alongside so records can pin the exact
// price book a charge came from. ErrNoPrice when no row covers at.
func PerCallPriceAt(db *gorm.DB, vendor, item string, at time.Time) (unitMicro int64, version int64, err error) {
	var p PerCallPrice
	err = db.Where("vendor = ? AND item = ? AND effective_at <= ?", vendor, item, at).
		Order("effective_at DESC").
		Order("version DESC").
		Take(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, 0, ErrNoPrice
		}
		return 0, 0, err
	}
	return p.UnitPriceMicro, p.Version, nil
}
