package marketplace

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
)

// model_pricing.go — per-model price changes and enabled toggles.
//
// Contract (seller console):
//   - a decrease applies immediately and cancels any staged increase;
//   - an increase is staged for one hour, at most one increase per model per
//     Asia/Shanghai day, and at most +50% in one step;
//   - a staged increase blocks another increase (no stacking);
//   - sending the current value cancels a staged increase;
//   - disabling a model cancels its staged increase.
//
// Reads apply a due pending value themselves (effectiveListingModelMultiplier)
// so a lagging promotion job never charges the old price.

const (
	modelPriceIncreaseDelay = time.Hour
	modelPriceIncreasePct   = 150 // new <= current * 1.5
)

var (
	ErrModelPricePending    = errors.New("marketplace: a price increase is already pending for this model")
	ErrModelPriceDailyLimit = errors.New("marketplace: this model's price was already increased today")
	ErrModelPriceJump       = errors.New("marketplace: a single increase cannot exceed 50%")
	ErrModelPriceConflict   = errors.New("marketplace: the model price changed concurrently; retry")
)

// ModelStateInput is one model's requested state. Nil fields are unchanged.
type ModelStateInput struct {
	Enabled         *bool
	MultiplierMilli *int64
}

// ModelChangeError is one rejected row of a batch.
type ModelChangeError struct {
	ListingID        int64      `json:"listing_id"`
	Model            string     `json:"model"`
	Code             string     `json:"code"`
	Message          string     `json:"message"`
	EarliestChangeAt *time.Time `json:"earliest_change_at,omitempty"`
}

// BatchModelChangeError is all-or-nothing: when any row is rejected, no row is
// written.
type BatchModelChangeError struct {
	Items []ModelChangeError `json:"items"`
}

func (e *BatchModelChangeError) Error() string {
	if len(e.Items) == 0 {
		return "marketplace: model change rejected"
	}
	return fmt.Sprintf("marketplace: %d model change(s) rejected; first: %s",
		len(e.Items), e.Items[0].Message)
}

// IsBadRequest reports whether the row is a validation failure rather than a
// state conflict. The API maps these to 400 instead of 409.
func (e ModelChangeError) IsBadRequest() bool {
	return e.Code == "PRICE_RANGE" || e.Code == "MODEL_NOT_FOUND" || e.Code == "PRICE_JUMP"
}

type plannedModelChange struct {
	ListingID          int64
	Model              string
	RowID              int64
	Enabled            *bool
	Current            int64
	New                int64
	Mode               string // noop / enabled_only / immediate / pending / cancel
	PendingEffectiveAt time.Time
	Today              string
	ClearPending       bool
}

func shanghaiDate(now time.Time) string {
	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		return now.UTC().Format("2006-01-02")
	}
	return now.In(loc).Format("2006-01-02")
}

func nextShanghaiDay(now time.Time) time.Time {
	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		return now.Add(24 * time.Hour)
	}
	lt := now.In(loc)
	return time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
}

// ensureListingModelRow returns the per-model row, creating the default
// (base multiplier, enabled) row for a legacy listing that predates P4.
func ensureListingModelRow(tx *gorm.DB, listing Listing, model string) (ListingModel, error) {
	var row ListingModel
	err := tx.Where("listing_id = ? AND model = ?", listing.ID, model).Take(&row).Error
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return ListingModel{}, err
	}
	row = ListingModel{
		ListingID:       listing.ID,
		Model:           model,
		MultiplierMilli: listing.MultiplierMilli,
		Enabled:         true,
	}
	if err := tx.Create(&row).Error; err != nil {
		return ListingModel{}, err
	}
	return row, nil
}

func changeError(listingID int64, model, code, message string) *ModelChangeError {
	return &ModelChangeError{ListingID: listingID, Model: model, Code: code, Message: message}
}

// planModelChange validates one model's request without writing. The caller
// must have created the row first.
func planModelChange(listing Listing, row ListingModel, input ModelStateInput, today string, now time.Time) (*plannedModelChange, *ModelChangeError) {
	planned := &plannedModelChange{
		ListingID: listing.ID,
		Model:     row.Model,
		RowID:     row.ID,
		Enabled:   input.Enabled,
		Current:   row.MultiplierMilli,
		New:       row.MultiplierMilli,
		Mode:      "noop",
		Today:     today,
	}
	if input.Enabled != nil && !*input.Enabled {
		planned.ClearPending = true
	}
	if input.MultiplierMilli == nil {
		if input.Enabled != nil {
			planned.Mode = "enabled_only"
		}
		return planned, nil
	}
	requested := *input.MultiplierMilli
	if requested < MultiplierAbsoluteMinMilli || requested > MultiplierAbsoluteMaxMilli {
		return nil, changeError(listing.ID, row.Model, "PRICE_RANGE",
			fmt.Sprintf("倍率必须在 %.3fx 到 %.3fx 之间", float64(MultiplierAbsoluteMinMilli)/1000, float64(MultiplierAbsoluteMaxMilli)/1000))
	}
	min, max := multiplierBoundsForVendor(nil, listing.Vendor)
	if requested < min || requested > max {
		// Resolve the kind through the caller's DB when available; this branch
		// is only reached for the absolute check above in tests.
		return nil, changeError(listing.ID, row.Model, "PRICE_RANGE",
			fmt.Sprintf("该渠道倍率必须在 %.3fx 到 %.3fx 之间", float64(min)/1000, float64(max)/1000))
	}
	switch {
	case requested == row.MultiplierMilli:
		if row.PendingMultiplierMilli > 0 {
			planned.Mode = "cancel"
			planned.ClearPending = true
		}
	case requested < row.MultiplierMilli:
		planned.Mode = "immediate"
		planned.New = requested
		planned.ClearPending = true
	default:
		if row.PendingMultiplierMilli > 0 {
			return nil, changeError(listing.ID, row.Model, "PRICE_PENDING", ErrModelPricePending.Error())
		}
		if row.LastChangedDate == today {
			e := changeError(listing.ID, row.Model, "PRICE_DAILY_LIMIT", ErrModelPriceDailyLimit.Error())
			next := nextShanghaiDay(now)
			e.EarliestChangeAt = &next
			return nil, e
		}
		if requested*100 > row.MultiplierMilli*modelPriceIncreasePct {
			return nil, changeError(listing.ID, row.Model, "PRICE_JUMP", ErrModelPriceJump.Error())
		}
		planned.Mode = "pending"
		planned.New = requested
		planned.PendingEffectiveAt = now.Add(modelPriceIncreaseDelay)
	}
	return planned, nil
}

// planModelChangeWithDB is planModelChange with the vendor kind resolved from
// the database.
func planModelChangeWithDB(tx *gorm.DB, listing Listing, row ListingModel, input ModelStateInput, today string, now time.Time) (*plannedModelChange, *ModelChangeError) {
	if input.MultiplierMilli != nil {
		requested := *input.MultiplierMilli
		if err := validateMultiplierForVendor(tx, listing.Vendor, requested); err != nil {
			return nil, changeError(listing.ID, row.Model, "PRICE_RANGE", err.Error())
		}
	}
	return planModelChange(listing, row, input, today, now)
}

func applyModelChange(tx *gorm.DB, planned *plannedModelChange, actorID int64, now time.Time) error {
	updates := map[string]any{}
	if planned.Enabled != nil {
		if *planned.Enabled {
			updates["enabled"] = true
			updates["auto_disabled"] = false
			updates["disabled_reason"] = ""
			updates["disabled_detail"] = ""
			updates["disabled_status_code"] = 0
			updates["disabled_upstream_message"] = ""
			updates["disabled_at"] = nil
		} else {
			updates["enabled"] = false
			updates["auto_disabled"] = false
			updates["disabled_reason"] = "manual"
			updates["disabled_detail"] = ""
			updates["disabled_status_code"] = 0
			updates["disabled_upstream_message"] = ""
			updates["disabled_at"] = now
		}
	}
	if planned.ClearPending {
		updates["pending_multiplier_milli"] = 0
		updates["pending_effective_at"] = nil
	}
	switch planned.Mode {
	case "immediate":
		updates["multiplier_milli"] = planned.New
		updates["last_changed_at"] = now
	case "pending":
		// The condition is the concurrency guard: both the daily limit and an
		// existing pending value are re-checked atomically, not read-then-write.
		res := tx.Model(&ListingModel{}).
			Where("id = ? AND (pending_multiplier_milli = 0 OR pending_multiplier_milli IS NULL) AND (last_changed_date = '' OR last_changed_date <> ?)",
				planned.RowID, planned.Today).
			Updates(map[string]any{
				"pending_multiplier_milli": planned.New,
				"pending_effective_at":     planned.PendingEffectiveAt,
				"last_changed_date":        planned.Today,
				"last_changed_at":          now,
				"updated_at":               now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrModelPriceConflict
		}
	}
	if len(updates) > 0 {
		if err := tx.Model(&ListingModel{}).Where("id = ?", planned.RowID).
			Updates(updates).Error; err != nil {
			return err
		}
	}
	if planned.Mode == "immediate" || planned.Mode == "pending" || planned.Mode == "cancel" {
		notes := "mode=" + planned.Mode
		if planned.Mode == "pending" {
			notes += "; effective_at=" + planned.PendingEffectiveAt.Format(time.RFC3339)
		}
		if err := tx.Create(&MultiplierChange{
			ChannelID:          planned.ListingID,
			Model:              planned.Model,
			OldMultiplierMilli: planned.Current,
			NewMultiplierMilli: planned.New,
			ActorID:            actorID,
			Notes:              notes,
		}).Error; err != nil {
			return err
		}
	}
	if planned.Enabled != nil {
		action := ListingAuditModelEnable
		if !*planned.Enabled {
			action = ListingAuditModelDisable
		}
		if err := writeListingAuditTx(tx, planned.ListingID, actorID, action, map[string]any{
			"model": planned.Model,
		}); err != nil {
			return err
		}
	}
	return nil
}

// SetListingModelsState applies one model-state request to one listing. An
// empty models slice means every model of the listing.
func SetListingModelsState(db *gorm.DB, listingID, sellerID int64, models []string, input ModelStateInput, now time.Time) ([]ListingModel, error) {
	var out []ListingModel
	err := db.Transaction(func(tx *gorm.DB) error {
		var listing Listing
		if err := lockForUpdate(tx).Where("id = ?", listingID).First(&listing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrListingNotFound
			}
			return err
		}
		if listing.SellerID != sellerID && sellerID > 0 {
			return ErrListingNotFound
		}
		if listing.Status == ListingDeleted {
			return ErrListingNotFound
		}
		if listing.Status == ListingBanned {
			return fmt.Errorf("%w: banned listing cannot be edited", ErrInvalidTransition)
		}
		targets := normalizeModelTargets(models, listing.Models)
		if len(targets) == 0 {
			return errors.New("marketplace: at least one model is required")
		}
		var rejected []ModelChangeError
		for _, model := range targets {
			if !slices.Contains(listing.Models, model) {
				rejected = append(rejected, *changeError(listing.ID, model, "MODEL_NOT_FOUND",
					"model is not listed on this listing"))
			}
		}
		if len(rejected) > 0 {
			return &BatchModelChangeError{Items: rejected}
		}
		today := shanghaiDate(now)
		rows := make([]ListingModel, 0, len(targets))
		for _, model := range targets {
			row, err := ensureListingModelRow(tx, listing, model)
			if err != nil {
				return err
			}
			rows = append(rows, row)
		}
		planned := make([]*plannedModelChange, 0, len(rows))
		for _, row := range rows {
			// A model added after creation only takes traffic once its real
			// verification call passed; the seller cannot switch it on early.
			if input.Enabled != nil && *input.Enabled && row.VerifyState != "" {
				rejected = append(rejected, *changeError(listing.ID, row.Model, "MODEL_VERIFYING",
					"model is still being verified or failed verification"))
				continue
			}
			change, cerr := planModelChangeWithDB(tx, listing, row, input, today, now)
			if cerr != nil {
				rejected = append(rejected, *cerr)
				continue
			}
			planned = append(planned, change)
		}
		if len(rejected) > 0 {
			return &BatchModelChangeError{Items: rejected}
		}
		for _, change := range planned {
			if err := applyModelChange(tx, change, sellerID, now); err != nil {
				return err
			}
		}
		out = make([]ListingModel, 0, len(rows))
		for _, row := range rows {
			var current ListingModel
			if err := tx.Where("id = ?", row.ID).First(&current).Error; err != nil {
				return err
			}
			out = append(out, current)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	mirrorListingUpdateLogged(db, listingID)
	resetBuyerModelVisibilityCache()
	resetPublicStatsCache()
	return out, nil
}

// BatchSetListingModelsState applies the same request to several listings in
// one transaction. Any rejected row rejects the whole batch.
func BatchSetListingModelsState(db *gorm.DB, sellerID int64, listingIDs []int64, models []string, input ModelStateInput, now time.Time) error {
	if len(listingIDs) == 0 {
		return errors.New("marketplace: at least one listing is required")
	}
	var affected []int64
	err := db.Transaction(func(tx *gorm.DB) error {
		var listings []Listing
		if err := lockForUpdate(tx).Where("id IN ? AND seller_id = ? AND status <> ?",
			listingIDs, sellerID, ListingDeleted).Find(&listings).Error; err != nil {
			return err
		}
		if len(listings) != len(listingIDs) {
			return ErrListingNotFound
		}
		today := shanghaiDate(now)
		var planned []*plannedModelChange
		var rejected []ModelChangeError
		for i := range listings {
			listing := listings[i]
			if listing.Status == ListingBanned {
				rejected = append(rejected, *changeError(listing.ID, "", "LISTING_BANNED", "banned listing cannot be edited"))
				continue
			}
			targets := normalizeModelTargets(models, listing.Models)
			for _, model := range targets {
				if !slices.Contains(listing.Models, model) {
					rejected = append(rejected, *changeError(listing.ID, model, "MODEL_NOT_FOUND",
						"model is not listed on this listing"))
					continue
				}
				row, err := ensureListingModelRow(tx, listing, model)
				if err != nil {
					return err
				}
				change, cerr := planModelChangeWithDB(tx, listing, row, input, today, now)
				if cerr != nil {
					rejected = append(rejected, *cerr)
					continue
				}
				planned = append(planned, change)
			}
		}
		if len(rejected) > 0 {
			return &BatchModelChangeError{Items: rejected}
		}
		for _, change := range planned {
			if err := applyModelChange(tx, change, sellerID, now); err != nil {
				return err
			}
			if !containsInt64(affected, change.ListingID) {
				affected = append(affected, change.ListingID)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, id := range affected {
		mirrorListingUpdateLogged(db, id)
	}
	resetBuyerModelVisibilityCache()
	resetPublicStatsCache()
	return nil
}

func normalizeModelTargets(models []string, fallback []string) []string {
	src := models
	if len(src) == 0 {
		src = fallback
	}
	seen := make(map[string]bool, len(src))
	out := make([]string, 0, len(src))
	for _, model := range src {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		out = append(out, model)
	}
	return out
}

func containsInt64(values []int64, needle int64) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// PromotePendingModelMultipliers materializes staged increases whose effective
// time has passed. The read paths already apply a due pending value, so this
// job only keeps the stored row (and its audit trail) in sync; a lagging run is
// never visible to buyers.
func PromotePendingModelMultipliers(db *gorm.DB, now time.Time) (int64, error) {
	if db == nil {
		return 0, nil
	}
	var rows []ListingModel
	if err := db.Where("pending_multiplier_milli > 0 AND pending_effective_at IS NOT NULL AND pending_effective_at <= ?", now).
		Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	affected := make(map[int64]bool, len(rows))
	var promoted int64
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, row := range rows {
			res := tx.Model(&ListingModel{}).
				Where("id = ? AND pending_multiplier_milli = ?", row.ID, row.PendingMultiplierMilli).
				Updates(map[string]any{
					"multiplier_milli":         row.PendingMultiplierMilli,
					"pending_multiplier_milli": 0,
					"pending_effective_at":     nil,
					"updated_at":               now,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				// A newer decrease or cancellation superseded this row.
				continue
			}
			if err := tx.Create(&MultiplierChange{
				ChannelID:          row.ListingID,
				Model:              row.Model,
				OldMultiplierMilli: row.MultiplierMilli,
				NewMultiplierMilli: row.PendingMultiplierMilli,
				ActorID:            0,
				Notes:              "mode=applied; effective_at=" + row.PendingEffectiveAt.Format(time.RFC3339),
			}).Error; err != nil {
				return err
			}
			affected[row.ListingID] = true
			promoted++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for id := range affected {
		mirrorListingUpdateLogged(db, id)
	}
	resetBuyerModelVisibilityCache()
	resetPublicStatsCache()
	return promoted, nil
}
