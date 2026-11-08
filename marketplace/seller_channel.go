package marketplace

// seller_channel.go — P3a extension stage: the seller-channel shadow schema.
//
// A legacy listing (mp_listings) is one seller + one key + one model. P3 splits
// it into five tables:
//
//	mp_seller_channels        one seller channel (id == legacy listing id)
//	mp_channel_endpoints      one row per protocol endpoint the channel serves
//	mp_channel_keys           one row per upstream credential
//	mp_seller_channel_models  one row per platform model the channel sells
//	mp_multiplier_changes     append-only multiplier edit history
//
// During this stage the legacy tables stay authoritative: CreateListing writes
// both schemas, the existing bulk data is backfilled once, and the selector /
// relay paths keep reading mp_listings. Nothing here may become a read path.

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
)

// sellerChannelBackfillBatch bounds the listings loaded per query so an upgrade
// of a large table never holds one long transaction.
const sellerChannelBackfillBatch = 100

// channelEndpointSpec is the derived definition of one protocol endpoint.
type channelEndpointSpec struct {
	Protocol      string
	Base          string
	FixedPath     string
	AuthMethod    string
	StreamEndRule bool
}

// channelEndpointSpecs derives the endpoints a channel serves from its
// protocol bases. A chat base implies both OpenAI endpoints (the responses
// endpoint falls back to the chat base), while anthropic_messages is only
// present when the channel declares an Anthropic base. fallbackBase is used
// when the pool has no protocol data at all (a legacy or custom channel row).
func channelEndpointSpecs(bases ChannelEndpointBases, fallbackBase string) []channelEndpointSpec {
	chat := strings.TrimSuffix(strings.TrimSpace(bases.ChatBase()), "/")
	if chat == "" {
		chat = strings.TrimSuffix(strings.TrimSpace(fallbackBase), "/")
	}
	specs := make([]channelEndpointSpec, 0, 3)
	if chat != "" {
		responses := strings.TrimSuffix(strings.TrimSpace(bases.ResponsesBase()), "/")
		if responses == "" {
			responses = chat
		}
		specs = append(specs,
			channelEndpointSpec{
				Protocol:      string(relayconstant.EndpointOpenAIChat),
				Base:          chat,
				FixedPath:     relayconstant.EndpointPath(relayconstant.EndpointOpenAIChat),
				AuthMethod:    EndpointAuthBearer,
				StreamEndRule: true,
			},
			channelEndpointSpec{
				Protocol:   string(relayconstant.EndpointOpenAIResponses),
				Base:       responses,
				FixedPath:  relayconstant.EndpointPath(relayconstant.EndpointOpenAIResponses),
				AuthMethod: EndpointAuthBearer,
			},
		)
	}
	if messages := strings.TrimSuffix(strings.TrimSpace(bases.MessagesBase()), "/"); messages != "" {
		specs = append(specs, channelEndpointSpec{
			Protocol:   string(relayconstant.EndpointAnthropicMessages),
			Base:       messages,
			FixedPath:  relayconstant.EndpointPath(relayconstant.EndpointAnthropicMessages),
			AuthMethod: EndpointAuthXAPIKey,
		})
	}
	return specs
}

// sellerChannelBase is the channel-level normalized base: the chat base when
// configured, otherwise the placeholder upstream channel's base.
func sellerChannelBase(bases ChannelEndpointBases, fallbackBase string) string {
	if base := strings.TrimSuffix(strings.TrimSpace(bases.ChatBase()), "/"); base != "" {
		return base
	}
	return strings.TrimSuffix(strings.TrimSpace(fallbackBase), "/")
}

// sellerChannelStatusVerified maps a listing status to whether its endpoints
// and models already passed verification. live/canary are the only served
// states; everything else is unverified.
func sellerChannelStatusVerified(status string) bool {
	return status == ListingLive || status == ListingCanary
}

// matchTrustPoolID returns the trust-pool row id matching one base URL, or 0
// when nothing matches. It never fails the caller: the shadow row's trust link
// is descriptive, not a routing decision.
func matchTrustPoolID(db *gorm.DB, base string) int64 {
	if db == nil || strings.TrimSpace(base) == "" {
		return 0
	}
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Host == "" {
		return 0
	}
	pool, err := MatchTrustPoolDB(db, u.Host, u.Path, "")
	if err != nil || pool == nil {
		return 0
	}
	return pool.ID
}

// sellerChannelModelSpec is one platform-model row to write.
type sellerChannelModelSpec struct {
	Model         string
	UpstreamModel string
}

// sellerChannelModelSpecs resolves the upstream name of every listed model. An
// explicit mapping (the channel's model_mapping, already decoded) wins; then
// the pool qualification; the platform model id is the identity fallback.
func sellerChannelModelSpecs(db *gorm.DB, models []string, mapping map[string]string, vendor string) []sellerChannelModelSpec {
	specs := make([]sellerChannelModelSpec, 0, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		upstream := strings.TrimSpace(mapping[m])
		if upstream == "" {
			if cm, ok := ChannelModelFor(db, vendor, m); ok {
				upstream = strings.TrimSpace(cm.UpstreamModel)
			}
		}
		if upstream == "" {
			upstream = m
		}
		specs = append(specs, sellerChannelModelSpec{Model: m, UpstreamModel: upstream})
	}
	return specs
}

// sellerChannelModelRows builds the per-model rows. allowed_endpoints is the
// JSON array of protocols the channel serves; the three verified flags follow
// the channel status.
func sellerChannelModelRows(channelID int64, specs []sellerChannelModelSpec, endpoints []channelEndpointSpec, multiplierMilli int64, verified bool) ([]SellerChannelModel, error) {
	allowed := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		allowed = append(allowed, ep.Protocol)
	}
	encoded, err := common.Marshal(allowed)
	if err != nil {
		return nil, fmt.Errorf("marketplace: encode allowed endpoints: %w", err)
	}
	rows := make([]SellerChannelModel, 0, len(specs))
	for _, spec := range specs {
		rows = append(rows, SellerChannelModel{
			ChannelID:                 channelID,
			Model:                     spec.Model,
			UpstreamModel:             spec.UpstreamModel,
			AllowedEndpoints:          string(encoded),
			MultiplierMilli:           multiplierMilli,
			Enabled:                   true,
			VerifiedOpenAIChat:        verified,
			VerifiedOpenAIResponses:   verified,
			VerifiedAnthropicMessages: verified,
		})
	}
	return rows, nil
}

// sellerChannelEndpointRows builds the endpoint rows of one channel.
func sellerChannelEndpointRows(channelID int64, specs []channelEndpointSpec, newAPIChannelID int64, verified bool, now time.Time) []ChannelEndpoint {
	rows := make([]ChannelEndpoint, 0, len(specs))
	for _, spec := range specs {
		row := ChannelEndpoint{
			ChannelID:       channelID,
			Protocol:        spec.Protocol,
			Base:            spec.Base,
			FixedPath:       spec.FixedPath,
			AuthMethod:      spec.AuthMethod,
			StreamEndRule:   spec.StreamEndRule,
			Verified:        verified,
			NewAPIChannelID: newAPIChannelID,
		}
		if verified {
			verifiedAt := now
			row.VerifiedAt = &verifiedAt
		}
		rows = append(rows, row)
	}
	return rows
}

// createSellerChannelShadow writes the P3 shadow rows of one freshly created
// listing inside the caller's transaction. The listing has already been
// inserted, so its id is final and the seller channel reuses it.
func createSellerChannelShadow(tx *gorm.DB, listing Listing, base string, bases ChannelEndpointBases, mapping map[string]string) error {
	now := time.Now()
	verified := sellerChannelStatusVerified(listing.Status)
	endpoints := channelEndpointSpecs(bases, base)
	channel := SellerChannel{
		ID:               listing.ID,
		SellerID:         listing.SellerID,
		TrustPoolID:      matchTrustPoolID(tx, base),
		Base:             sellerChannelBase(bases, base),
		KeyStrategy:      KeyStrategyWeightedRoundRobin,
		ConcurrencyLimit: listing.ConcurrencyLimit,
		RPMLimit:         listing.RPMLimit,
		DailyCapMicro:    listing.DailyCapMicro,
		Hours:            strings.TrimSpace(listing.Hours),
		Status:           listing.Status,
		Version:          listing.Version,
	}
	if err := tx.Create(&channel).Error; err != nil {
		return fmt.Errorf("marketplace: create seller channel: %w", err)
	}
	key := ChannelKey{
		ChannelID:        listing.ID,
		KeyCiphertext:    listing.KeyCiphertext,
		KeyFingerprint:   listing.KeyFingerprint,
		Weight:           100,
		ConcurrencyLimit: listing.ConcurrencyLimit,
		RPMLimit:         listing.RPMLimit,
		Status:           ChannelKeyNormal,
	}
	// A soft-deleted shadow key keeps its unique fingerprint even though
	// GORM's unique index is not partial. Free it with a tombstone before
	// creating the new key, so re-listing a normally deleted key works even if
	// a previous mirror deletion ran first.
	var stale ChannelKey
	staleErr := tx.Unscoped().Where("key_fingerprint = ? AND deleted_at IS NOT NULL", key.KeyFingerprint).
		Take(&stale).Error
	switch {
	case staleErr == nil:
		tombstone := fmt.Sprintf("deleted:%d:%d", stale.ChannelID, stale.ID)
		if uerr := tx.Unscoped().Model(&ChannelKey{}).Where("id = ?", stale.ID).
			Updates(map[string]any{
				"deleted_fingerprint": key.KeyFingerprint,
				"key_fingerprint":     tombstone,
			}).Error; uerr != nil {
			return fmt.Errorf("marketplace: release soft-deleted channel key %d: %w", stale.ID, uerr)
		}
	case !errors.Is(staleErr, gorm.ErrRecordNotFound):
		return fmt.Errorf("marketplace: check soft-deleted channel key: %w", staleErr)
	}
	if err := tx.Create(&key).Error; err != nil {
		return fmt.Errorf("marketplace: create channel key: %w", err)
	}
	models, err := sellerChannelModelRows(listing.ID,
		sellerChannelModelSpecs(tx, listing.Models, mapping, listing.Vendor),
		endpoints, listing.MultiplierMilli, verified)
	if err != nil {
		return err
	}
	if len(models) > 0 {
		if err := tx.Create(&models).Error; err != nil {
			return fmt.Errorf("marketplace: create seller channel models: %w", err)
		}
	}
	rows := sellerChannelEndpointRows(listing.ID, endpoints, listing.ChannelID, verified, now)
	if len(rows) > 0 {
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("marketplace: create channel endpoints: %w", err)
		}
	}
	return nil
}

// sellerChannelStatusDeleted marks a shadow seller channel whose legacy listing
// was removed. It is not a legacy listing status: mp_listings has no delete
// path, so this only records the shadow side of MirrorListingDelete.
const sellerChannelStatusDeleted = "deleted"

// channelKeyStatusForListing maps a legacy listing status onto the lifecycle
// state of its single shadow key. The legacy table remains authoritative; the
// mapping only keeps mp_channel_keys meaningful for a later read-path switch:
// a banned credential is failed, a paused one cools down, and every other
// state (including draft/verifying, which are gated by the channel status) is
// normal.
func channelKeyStatusForListing(status string) string {
	switch status {
	case ListingBanned:
		return ChannelKeyFailed
	case ListingPaused:
		return ChannelKeyCooling
	default:
		return ChannelKeyNormal
	}
}

// mirrorListingUpdateLogged mirrors one listing into the P3 shadow tables after
// the authoritative write has committed. The legacy tables stay in charge, so a
// mirror failure is logged and swallowed: it must never fail or roll back the
// seller-facing operation.
func mirrorListingUpdateLogged(db *gorm.DB, listingID int64) {
	if err := MirrorListingUpdate(db, listingID); err != nil {
		common.SysError(common.LogText("marketplace: mirror listing %d update: %s", listingID, err.Error()))
	}
}

// listingUpstreamChannel returns the model mapping and base URL of the
// placeholder upstream channel a listing points at. A missing channel (bogus
// id in older fixtures, or a channel table not present in a partial deployment)
// yields empty values, never an error: the mapping falls back to the channel
// pool and the platform model id.
func listingUpstreamChannel(db *gorm.DB, channelID int64) (map[string]string, string) {
	mapping := map[string]string{}
	if db == nil || channelID <= 0 || !db.Migrator().HasTable(&model.Channel{}) {
		return mapping, ""
	}
	var ch model.Channel
	if err := db.Select("id", "base_url", "model_mapping").Where("id = ?", channelID).Take(&ch).Error; err != nil {
		return mapping, ""
	}
	if raw := strings.TrimSpace(ch.GetModelMapping()); raw != "" {
		_ = common.Unmarshal([]byte(raw), &mapping)
	}
	return mapping, ch.GetBaseURL()
}

// MirrorListingUpdate copies the latest authoritative mp_listings row into the
// P3 shadow tables (seller channel, key, per-model rows, endpoint verification).
// It is the write-path counterpart of createSellerChannelShadow/Backfill: every
// update that changes a listing calls it after the legacy transaction commits.
//
// The call is idempotent. Every value is assigned (not accumulated), missing
// shadow rows are created, and rows for models the listing no longer carries are
// only disabled. A repeat run therefore writes the same state and creates
// nothing. It is never a read path.
func MirrorListingUpdate(db *gorm.DB, listingID int64) error {
	if db == nil {
		return errors.New("marketplace: mirror listing update: nil database")
	}
	if listingID <= 0 {
		return errors.New("marketplace: mirror listing update: invalid listing id")
	}
	if !db.Migrator().HasTable(&Listing{}) || !db.Migrator().HasTable(&SellerChannel{}) {
		return nil
	}
	var listing Listing
	if err := db.Where("id = ?", listingID).Take(&listing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrListingNotFound
		}
		return fmt.Errorf("marketplace: mirror listing %d: %w", listingID, err)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return mirrorListingUpdateTx(tx, listing)
	})
}

func mirrorListingUpdateTx(tx *gorm.DB, listing Listing) error {
	mapping, fallbackBase := listingUpstreamChannel(tx, listing.ChannelID)
	bases, _ := ChannelPoolEndpointBases(tx, listing.Vendor)
	base := sellerChannelBase(bases, fallbackBase)
	endpoints := channelEndpointSpecs(bases, fallbackBase)
	verified := sellerChannelStatusVerified(listing.Status)
	now := time.Now()

	// mp_seller_channels: identity, lifecycle and channel-level limits.
	var channel SellerChannel
	err := tx.Where("id = ?", listing.ID).Take(&channel).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		channel = SellerChannel{
			ID:               listing.ID,
			SellerID:         listing.SellerID,
			TrustPoolID:      matchTrustPoolID(tx, base),
			Base:             base,
			KeyStrategy:      KeyStrategyWeightedRoundRobin,
			ConcurrencyLimit: listing.ConcurrencyLimit,
			RPMLimit:         listing.RPMLimit,
			DailyCapMicro:    listing.DailyCapMicro,
			Hours:            strings.TrimSpace(listing.Hours),
			Status:           listing.Status,
			PauseReason:      listing.PauseReason,
			ResumeAt:         listing.ResumeAt,
			Version:          listing.Version,
			CreatedAt:        listing.CreatedAt,
		}
		if err := tx.Create(&channel).Error; err != nil {
			return fmt.Errorf("marketplace: mirror seller channel %d: %w", listing.ID, err)
		}
	case err != nil:
		return fmt.Errorf("marketplace: mirror seller channel %d: %w", listing.ID, err)
	default:
		updates := SellerChannel{
			SellerID:         listing.SellerID,
			ConcurrencyLimit: listing.ConcurrencyLimit,
			RPMLimit:         listing.RPMLimit,
			DailyCapMicro:    listing.DailyCapMicro,
			Hours:            strings.TrimSpace(listing.Hours),
			Status:           listing.Status,
			PauseReason:      listing.PauseReason,
			ResumeAt:         listing.ResumeAt,
			Version:          listing.Version,
		}
		if err := tx.Model(&SellerChannel{}).Where("id = ?", listing.ID).
			Select("SellerID", "ConcurrencyLimit", "RPMLimit", "DailyCapMicro", "Hours",
				"Status", "PauseReason", "ResumeAt", "Version").
			Updates(updates).Error; err != nil {
			return fmt.Errorf("marketplace: mirror seller channel %d: %w", listing.ID, err)
		}
	}

	// mp_channel_keys: the legacy listing carries exactly one credential, so
	// the shadow row is updated in place (keeping its id stable for historical
	// requests) and created only when absent. A soft-deleted row left by
	// MirrorListingDelete is revived instead of creating a second row with the
	// same fingerprint.
	keyStatus := channelKeyStatusForListing(listing.Status)
	keyUpdates := ChannelKey{
		KeyCiphertext:    listing.KeyCiphertext,
		KeyFingerprint:   listing.KeyFingerprint,
		ConcurrencyLimit: listing.ConcurrencyLimit,
		RPMLimit:         listing.RPMLimit,
		Status:           keyStatus,
	}
	var key ChannelKey
	err = tx.Where("channel_id = ?", listing.ID).Order("id ASC").Take(&key).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		var deletedKey ChannelKey
		recoverErr := tx.Unscoped().Where("channel_id = ?", listing.ID).Order("id ASC").Take(&deletedKey).Error
		switch {
		case recoverErr == nil:
			if err := tx.Unscoped().Model(&ChannelKey{}).Where("id = ?", deletedKey.ID).
				Select("KeyCiphertext", "KeyFingerprint", "ConcurrencyLimit", "RPMLimit", "Status", "DeletedAt").
				Updates(keyUpdates).Error; err != nil {
				return fmt.Errorf("marketplace: mirror channel key %d: %w", listing.ID, err)
			}
		case errors.Is(recoverErr, gorm.ErrRecordNotFound):
			key = keyUpdates
			key.ChannelID = listing.ID
			key.Weight = 100
			if err := tx.Create(&key).Error; err != nil {
				return fmt.Errorf("marketplace: mirror channel key %d: %w", listing.ID, err)
			}
		default:
			return fmt.Errorf("marketplace: mirror channel key %d: %w", listing.ID, recoverErr)
		}
	case err != nil:
		return fmt.Errorf("marketplace: mirror channel key %d: %w", listing.ID, err)
	default:
		if err := tx.Model(&ChannelKey{}).Where("id = ?", key.ID).
			Select("KeyCiphertext", "KeyFingerprint", "ConcurrencyLimit", "RPMLimit", "Status").
			Updates(keyUpdates).Error; err != nil {
			return fmt.Errorf("marketplace: mirror channel key %d: %w", listing.ID, err)
		}
	}

	// mp_channel_endpoints: derived from the channel pool, so a mirror run after
	// a pool/base edit also repairs the rows. Verification follows the listing
	// status; VerifiedAt is only rewritten when the state actually changes.
	verifiedAt := now
	for _, spec := range endpoints {
		var endpoint ChannelEndpoint
		err := tx.Where("channel_id = ? AND protocol = ?", listing.ID, spec.Protocol).Take(&endpoint).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			rows := sellerChannelEndpointRows(listing.ID, []channelEndpointSpec{spec}, listing.ChannelID, verified, now)
			if err := tx.Create(&rows[0]).Error; err != nil {
				return fmt.Errorf("marketplace: mirror channel endpoint %d: %w", listing.ID, err)
			}
		case err != nil:
			return fmt.Errorf("marketplace: mirror channel endpoint %d: %w", listing.ID, err)
		default:
			updates := ChannelEndpoint{
				Base:            spec.Base,
				FixedPath:       spec.FixedPath,
				AuthMethod:      spec.AuthMethod,
				StreamEndRule:   spec.StreamEndRule,
				NewAPIChannelID: listing.ChannelID,
			}
			fields := []string{"Base", "FixedPath", "AuthMethod", "StreamEndRule", "NewAPIChannelID"}
			if endpoint.Verified != verified {
				updates.Verified = verified
				if verified {
					updates.VerifiedAt = &verifiedAt
				}
				fields = append(fields, "Verified", "VerifiedAt")
			}
			if err := tx.Model(&ChannelEndpoint{}).Where("id = ?", endpoint.ID).
				Select(fields).Updates(updates).Error; err != nil {
				return fmt.Errorf("marketplace: mirror channel endpoint %d: %w", listing.ID, err)
			}
		}
	}

	// mp_seller_channel_models: one enabled row per listed model, disabled for a
	// model the listing dropped (verification narrows the list in place).
	allowedEndpoints := ""
	if len(endpoints) > 0 {
		allowed := make([]string, 0, len(endpoints))
		for _, ep := range endpoints {
			allowed = append(allowed, ep.Protocol)
		}
		encoded, err := common.Marshal(allowed)
		if err != nil {
			return fmt.Errorf("marketplace: mirror seller channel %d: encode endpoints: %w", listing.ID, err)
		}
		allowedEndpoints = string(encoded)
	}
	listed := make(map[string]bool, len(listing.Models))
	overrides := loadListingModelOverrides(tx, []int64{listing.ID}, time.Now())
	for _, spec := range sellerChannelModelSpecs(tx, listing.Models, mapping, listing.Vendor) {
		listed[spec.Model] = true
		modelEnabled := overrides.modelEnabled(listing.ID, spec.Model)
		modelMultiplier := overrides.multiplierFor(listing.ID, spec.Model, listing.MultiplierMilli)
		var row SellerChannelModel
		err := tx.Where("channel_id = ? AND model = ?", listing.ID, spec.Model).Take(&row).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			rows, rowsErr := sellerChannelModelRows(listing.ID,
				[]sellerChannelModelSpec{spec}, endpoints, modelMultiplier, verified)
			if rowsErr != nil {
				return rowsErr
			}
			rows[0].Enabled = modelEnabled
			if err := tx.Create(&rows[0]).Error; err != nil {
				return fmt.Errorf("marketplace: mirror seller channel model %d: %w", listing.ID, err)
			}
		case err != nil:
			return fmt.Errorf("marketplace: mirror seller channel model %d: %w", listing.ID, err)
		default:
			updates := SellerChannelModel{
				UpstreamModel:             spec.UpstreamModel,
				MultiplierMilli:           modelMultiplier,
				Enabled:                   modelEnabled,
				VerifiedOpenAIChat:        verified,
				VerifiedOpenAIResponses:   verified,
				VerifiedAnthropicMessages: verified,
			}
			fields := []string{"UpstreamModel", "MultiplierMilli", "Enabled",
				"VerifiedOpenAIChat", "VerifiedOpenAIResponses", "VerifiedAnthropicMessages"}
			if allowedEndpoints != "" {
				updates.AllowedEndpoints = allowedEndpoints
				fields = append(fields, "AllowedEndpoints")
			}
			if err := tx.Model(&SellerChannelModel{}).Where("id = ?", row.ID).
				Select(fields).Updates(updates).Error; err != nil {
				return fmt.Errorf("marketplace: mirror seller channel model %d: %w", listing.ID, err)
			}
		}
	}
	var stale []SellerChannelModel
	if err := tx.Where("channel_id = ? AND enabled = ?", listing.ID, true).Find(&stale).Error; err != nil {
		return fmt.Errorf("marketplace: mirror seller channel models %d: %w", listing.ID, err)
	}
	for i := range stale {
		if listed[stale[i].Model] {
			continue
		}
		if err := tx.Model(&SellerChannelModel{}).Where("id = ?", stale[i].ID).
			Select("Enabled", "VerifiedOpenAIChat", "VerifiedOpenAIResponses", "VerifiedAnthropicMessages").
			Updates(SellerChannelModel{Enabled: false}).Error; err != nil {
			return fmt.Errorf("marketplace: disable seller channel model %d: %w", listing.ID, err)
		}
	}
	return nil
}

// tombstoneChannelKeysTx soft-deletes a listing's shadow keys and frees the
// unique fingerprint with a per-row tombstone. The original identity moves to
// deleted_fingerprint so a later re-listing of a normally deleted key cannot
// collide with the unique index.
func tombstoneChannelKeysTx(tx *gorm.DB, listingID int64, now time.Time) error {
	var keys []ChannelKey
	if err := tx.Unscoped().Where("channel_id = ?", listingID).Find(&keys).Error; err != nil {
		return fmt.Errorf("marketplace: load channel keys %d: %w", listingID, err)
	}
	for i := range keys {
		tombstone := fmt.Sprintf("deleted:%d:%d", listingID, keys[i].ID)
		if err := tx.Unscoped().Model(&ChannelKey{}).Where("id = ?", keys[i].ID).
			Updates(map[string]any{
				"deleted_fingerprint": keys[i].KeyFingerprint,
				"key_fingerprint":     tombstone,
				"deleted_at":          now,
			}).Error; err != nil {
			return fmt.Errorf("marketplace: tombstone channel key %d: %w", keys[i].ID, err)
		}
	}
	return nil
}

// MirrorListingDelete applies the shadow side of a removed listing: the
// channel's credentials are soft-deleted (historical requests keep resolvable
// key ids), its models are disabled, and the channel is marked deleted. The
// call is idempotent: a repeat run finds nothing new to change.
func MirrorListingDelete(db *gorm.DB, listingID int64) error {
	if db == nil {
		return errors.New("marketplace: mirror listing delete: nil database")
	}
	if listingID <= 0 {
		return errors.New("marketplace: mirror listing delete: invalid listing id")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if tx.Migrator().HasTable(&ChannelKey{}) {
			if err := tombstoneChannelKeysTx(tx, listingID, time.Now()); err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&SellerChannelModel{}) {
			if err := tx.Model(&SellerChannelModel{}).Where("channel_id = ?", listingID).
				Select("Enabled", "VerifiedOpenAIChat", "VerifiedOpenAIResponses", "VerifiedAnthropicMessages").
				Updates(SellerChannelModel{Enabled: false}).Error; err != nil {
				return fmt.Errorf("marketplace: mirror delete seller channel models %d: %w", listingID, err)
			}
		}
		if tx.Migrator().HasTable(&SellerChannel{}) {
			if err := tx.Model(&SellerChannel{}).Where("id = ?", listingID).
				Select("Status").Updates(SellerChannel{Status: sellerChannelStatusDeleted}).Error; err != nil {
				return fmt.Errorf("marketplace: mirror delete seller channel %d: %w", listingID, err)
			}
		}
		return nil
	})
}

// CheckSellerChannelConsistency compares the authoritative mp_listings rows
// with the P3 shadow tables and returns one human-readable issue per mismatch
// (listing id plus reason), ordered by listing id. It is the P5 operation
// reconciliation entry point; nothing schedules it yet.
func CheckSellerChannelConsistency(db *gorm.DB) ([]string, error) {
	if db == nil {
		return nil, errors.New("marketplace: check seller channel consistency: nil database")
	}
	for _, table := range []any{&Listing{}, &SellerChannel{}, &ChannelKey{}, &SellerChannelModel{}} {
		if !db.Migrator().HasTable(table) {
			return nil, fmt.Errorf("marketplace: check seller channel consistency: table for %T is missing", table)
		}
	}
	var listings []Listing
	if err := db.Order("id ASC").Find(&listings).Error; err != nil {
		return nil, fmt.Errorf("marketplace: check seller channel consistency: %w", err)
	}
	var channels []SellerChannel
	if err := db.Order("id ASC").Find(&channels).Error; err != nil {
		return nil, fmt.Errorf("marketplace: check seller channel consistency: %w", err)
	}
	channelByID := make(map[int64]SellerChannel, len(channels))
	for i := range channels {
		channelByID[channels[i].ID] = channels[i]
	}
	var keys []ChannelKey
	if err := db.Order("channel_id ASC").Order("id ASC").Find(&keys).Error; err != nil {
		return nil, fmt.Errorf("marketplace: check seller channel consistency: %w", err)
	}
	keysByChannel := make(map[int64][]ChannelKey, len(keys))
	for i := range keys {
		keysByChannel[keys[i].ChannelID] = append(keysByChannel[keys[i].ChannelID], keys[i])
	}
	var modelRows []SellerChannelModel
	if err := db.Order("channel_id ASC").Order("model ASC").Find(&modelRows).Error; err != nil {
		return nil, fmt.Errorf("marketplace: check seller channel consistency: %w", err)
	}
	modelsByChannel := make(map[int64][]SellerChannelModel, len(modelRows))
	for i := range modelRows {
		modelsByChannel[modelRows[i].ChannelID] = append(modelsByChannel[modelRows[i].ChannelID], modelRows[i])
	}

	issues := make([]string, 0)
	for i := range listings {
		l := &listings[i]
		channel, ok := channelByID[l.ID]
		if !ok {
			issues = append(issues, fmt.Sprintf("listing %d: missing shadow seller channel", l.ID))
			continue
		}
		if channel.SellerID != l.SellerID {
			issues = append(issues, fmt.Sprintf("listing %d: seller id mismatch", l.ID))
		}
		if channel.Status != l.Status {
			issues = append(issues, fmt.Sprintf("listing %d: status mismatch (listing=%s shadow=%s)", l.ID, l.Status, channel.Status))
		}
		if channel.Version != l.Version {
			issues = append(issues, fmt.Sprintf("listing %d: version mismatch (listing=%d shadow=%d)", l.ID, l.Version, channel.Version))
		}
		if channel.PauseReason != l.PauseReason {
			issues = append(issues, fmt.Sprintf("listing %d: pause reason mismatch (listing=%q shadow=%q)", l.ID, l.PauseReason, channel.PauseReason))
		}
		resumeAtMatches := (channel.ResumeAt == nil && l.ResumeAt == nil) ||
			(channel.ResumeAt != nil && l.ResumeAt != nil && channel.ResumeAt.Equal(*l.ResumeAt))
		if !resumeAtMatches {
			issues = append(issues, fmt.Sprintf("listing %d: resume time mismatch", l.ID))
		}
		activeKeys := keysByChannel[l.ID]
		switch {
		case len(activeKeys) == 0:
			issues = append(issues, fmt.Sprintf("listing %d: missing shadow channel key", l.ID))
		case len(activeKeys) > 1:
			issues = append(issues, fmt.Sprintf("listing %d: %d active shadow keys, want 1", l.ID, len(activeKeys)))
		default:
			key := activeKeys[0]
			if key.KeyFingerprint != l.KeyFingerprint {
				issues = append(issues, fmt.Sprintf("listing %d: key fingerprint mismatch", l.ID))
			}
			if !bytes.Equal(key.KeyCiphertext, l.KeyCiphertext) {
				issues = append(issues, fmt.Sprintf("listing %d: key ciphertext mismatch", l.ID))
			}
			if want := channelKeyStatusForListing(l.Status); key.Status != want {
				issues = append(issues, fmt.Sprintf("listing %d: key status mismatch (want=%s shadow=%s)", l.ID, want, key.Status))
			}
		}
		listed := make(map[string]bool, len(l.Models))
		for _, m := range l.Models {
			listed[m] = true
		}
		seen := make(map[string]bool, len(l.Models))
		wantVerified := sellerChannelStatusVerified(l.Status)
		for _, row := range modelsByChannel[l.ID] {
			if !listed[row.Model] {
				if row.Enabled {
					issues = append(issues, fmt.Sprintf("listing %d: model %q is enabled but not listed", l.ID, row.Model))
				}
				continue
			}
			seen[row.Model] = true
			if !row.Enabled {
				issues = append(issues, fmt.Sprintf("listing %d: model %q is disabled in shadow", l.ID, row.Model))
			}
			if row.MultiplierMilli != l.MultiplierMilli {
				issues = append(issues, fmt.Sprintf("listing %d: model %q multiplier mismatch (listing=%d shadow=%d)",
					l.ID, row.Model, l.MultiplierMilli, row.MultiplierMilli))
			}
			if row.VerifiedOpenAIChat != wantVerified ||
				row.VerifiedOpenAIResponses != wantVerified ||
				row.VerifiedAnthropicMessages != wantVerified {
				issues = append(issues, fmt.Sprintf("listing %d: model %q verified flags do not match status %s", l.ID, row.Model, l.Status))
			}
		}
		for _, m := range l.Models {
			if !seen[m] {
				issues = append(issues, fmt.Sprintf("listing %d: model %q has no shadow row", l.ID, m))
			}
		}
	}
	listingByID := make(map[int64]struct{}, len(listings))
	for i := range listings {
		listingByID[listings[i].ID] = struct{}{}
	}
	for i := range channels {
		if _, ok := listingByID[channels[i].ID]; !ok {
			issues = append(issues, fmt.Sprintf("seller channel %d: no legacy listing", channels[i].ID))
		}
	}
	return issues, nil
}

// BackfillSellerChannels copies every legacy listing into the P3 shadow
// tables. It is idempotent: a listing whose mp_seller_channels row already
// exists is skipped, and the request/ledger channel ids are only filled where
// they are still 0. Listings are read in batches of sellerChannelBackfillBatch
// and each listing is written in its own transaction, so a large upgrade never
// holds one long transaction.
func BackfillSellerChannels(db *gorm.DB) error {
	if db == nil {
		return errors.New("marketplace: backfill seller channels: nil database")
	}
	if !db.Migrator().HasTable(&Listing{}) || !db.Migrator().HasTable(&SellerChannel{}) {
		return nil
	}
	hasUpstreamChannels := db.Migrator().HasTable(&model.Channel{})
	var lastID int64
	for {
		var listings []Listing
		if err := db.Where("id > ?", lastID).Order("id ASC").
			Limit(sellerChannelBackfillBatch).Find(&listings).Error; err != nil {
			return fmt.Errorf("marketplace: backfill seller channels: %w", err)
		}
		if len(listings) == 0 {
			break
		}
		for i := range listings {
			lastID = listings[i].ID
			if err := backfillSellerChannel(db, listings[i], hasUpstreamChannels); err != nil {
				return err
			}
		}
	}
	if err := backfillRequestLedgerChannelIDs(db); err != nil {
		return err
	}
	return alignSellerChannelIDSequence(db)
}

// backfillSellerChannel writes the shadow rows of one legacy listing unless the
// seller channel already exists.
func backfillSellerChannel(db *gorm.DB, listing Listing, hasUpstreamChannels bool) error {
	var existing int64
	if err := db.Model(&SellerChannel{}).Where("id = ?", listing.ID).Count(&existing).Error; err != nil {
		return fmt.Errorf("marketplace: backfill seller channel %d: %w", listing.ID, err)
	}
	if existing > 0 {
		return nil
	}
	var upstream *model.Channel
	if hasUpstreamChannels {
		var channel model.Channel
		err := db.Where("id = ?", listing.ChannelID).Take(&channel).Error
		if err == nil {
			upstream = &channel
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("marketplace: backfill seller channel %d: read upstream channel: %w", listing.ID, err)
		}
	}
	mapping := map[string]string{}
	if upstream != nil {
		if raw := strings.TrimSpace(upstream.GetModelMapping()); raw != "" {
			_ = common.Unmarshal([]byte(raw), &mapping)
		}
	}
	fallbackBase := ""
	if upstream != nil {
		fallbackBase = upstream.GetBaseURL()
	}
	bases, _ := ChannelPoolEndpointBases(db, listing.Vendor)
	base := sellerChannelBase(bases, fallbackBase)
	endpoints := channelEndpointSpecs(bases, fallbackBase)
	verified := sellerChannelStatusVerified(listing.Status)

	return db.Transaction(func(tx *gorm.DB) error {
		channel := SellerChannel{
			ID:               listing.ID,
			SellerID:         listing.SellerID,
			TrustPoolID:      matchTrustPoolID(tx, base),
			Base:             base,
			KeyStrategy:      KeyStrategyWeightedRoundRobin,
			ConcurrencyLimit: listing.ConcurrencyLimit,
			RPMLimit:         listing.RPMLimit,
			DailyCapMicro:    listing.DailyCapMicro,
			Hours:            strings.TrimSpace(listing.Hours),
			Status:           listing.Status,
			PauseReason:      listing.PauseReason,
			ResumeAt:         listing.ResumeAt,
			Version:          listing.Version,
			CreatedAt:        listing.CreatedAt,
			UpdatedAt:        listing.UpdatedAt,
		}
		if err := tx.Create(&channel).Error; err != nil {
			return fmt.Errorf("marketplace: backfill seller channel %d: %w", listing.ID, err)
		}
		key := ChannelKey{
			ChannelID:        listing.ID,
			KeyCiphertext:    listing.KeyCiphertext,
			KeyFingerprint:   listing.KeyFingerprint,
			Weight:           100,
			ConcurrencyLimit: listing.ConcurrencyLimit,
			RPMLimit:         listing.RPMLimit,
			Status:           ChannelKeyNormal,
		}
		if err := tx.Create(&key).Error; err != nil {
			return fmt.Errorf("marketplace: backfill channel key %d: %w", listing.ID, err)
		}
		rows, err := sellerChannelModelRows(listing.ID,
			sellerChannelModelSpecs(tx, listing.Models, mapping, listing.Vendor),
			endpoints, listing.MultiplierMilli, verified)
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return fmt.Errorf("marketplace: backfill seller channel models %d: %w", listing.ID, err)
			}
		}
		endpointRows := sellerChannelEndpointRows(listing.ID, endpoints, listing.ChannelID, verified, time.Now())
		if len(endpointRows) > 0 {
			if err := tx.Create(&endpointRows).Error; err != nil {
				return fmt.Errorf("marketplace: backfill channel endpoints %d: %w", listing.ID, err)
			}
		}
		return nil
	})
}

// backfillRequestLedgerChannelIDs fills the shadow channel id of records
// written before P3. mp_seller_channels.id equals the listing id, so the
// mapping is a column copy; key_id stays 0 until multi-key supply exists.
func backfillRequestLedgerChannelIDs(db *gorm.DB) error {
	if db.Migrator().HasTable(&Request{}) {
		if err := db.Model(&Request{}).
			Where("channel_id = 0 AND listing_id > 0").
			Update("channel_id", gorm.Expr("listing_id")).Error; err != nil {
			return fmt.Errorf("marketplace: backfill request channel ids: %w", err)
		}
	}
	if db.Migrator().HasTable(&LedgerEntry{}) {
		if err := db.Model(&LedgerEntry{}).
			Where("channel_id = 0 AND listing_id > 0").
			Update("channel_id", gorm.Expr("listing_id")).Error; err != nil {
			return fmt.Errorf("marketplace: backfill ledger channel ids: %w", err)
		}
	}
	return nil
}

// alignSellerChannelIDSequence moves a PostgreSQL sequence past the explicit
// ids copied from mp_listings. Explicit inserts do not advance a sequence on
// PostgreSQL, so without this a later auto-generated id could collide; SQLite
// and MySQL update their high-water marks on an explicit insert.
func alignSellerChannelIDSequence(db *gorm.DB) error {
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	if err := db.Exec("SELECT setval(pg_get_serial_sequence('mp_seller_channels','id'), COALESCE(MAX(id), 1), MAX(id) IS NOT NULL) FROM mp_seller_channels").Error; err != nil {
		return fmt.Errorf("marketplace: align seller channel id sequence: %w", err)
	}
	return nil
}
