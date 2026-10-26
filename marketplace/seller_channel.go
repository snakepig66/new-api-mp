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
