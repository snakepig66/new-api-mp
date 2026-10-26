package marketplace

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// legacyListingFixture writes one pre-P3 listing together with its upstream
// placeholder channel: exactly the shape BackfillSellerChannels must split.
func legacyListingFixture(t *testing.T, db *gorm.DB, sellerID int64, models []string, status string, multiplierMilli int64) (Listing, model.Channel) {
	t.Helper()
	base := "https://api.deepseek.com"
	channel := model.Channel{
		Type:        constant.ChannelTypeDeepSeek,
		Key:         "mp-managed:legacy01",
		Name:        fmt.Sprintf("legacy:%d", sellerID),
		Status:      common.ChannelStatusManuallyDisabled,
		BaseURL:     &base,
		Models:      strings.Join(models, ","),
		Group:       "default",
		CreatedTime: time.Now().Unix(),
	}
	mapping := `{"deepseek-flash":"deepseek-chat","deepseek-v4-pro":"deepseek-reasoner"}`
	channel.ModelMapping = &mapping
	require.NoError(t, db.Create(&channel).Error)

	key := uniqueTestKey("sk-backfill")
	ciphertext, err := EncryptListingKey(key)
	require.NoError(t, err)
	fingerprint, err := ListingKeyFingerprint(key)
	require.NoError(t, err)
	listing := Listing{
		SellerID:         sellerID,
		ChannelID:        int64(channel.Id),
		Vendor:           "deepseek",
		Models:           models,
		MultiplierMilli:  multiplierMilli,
		ConcurrencyLimit: 7,
		RPMLimit:         30,
		DailyCapMicro:    1234,
		Hours:            `[{"start":"09:00","end":"18:00"}]`,
		Status:           status,
		KeyFingerprint:   fingerprint,
		KeyCiphertext:    ciphertext,
		Version:          3,
	}
	require.NoError(t, db.Create(&listing).Error)
	return listing, channel
}

// TestBackfillSellerChannelsFieldMapping covers the full legacy → shadow
// split: status/limits/base mapping, the key row, per-model upstream names and
// verified flags, one endpoint row per protocol, and the request/ledger
// channel-id backfill.
func TestBackfillSellerChannelsFieldMapping(t *testing.T) {
	db := newListingTestDB(t)
	listing, _ := legacyListingFixture(t, db, 42,
		[]string{"deepseek-flash", "deepseek-v4-pro"}, ListingLive, 400)

	// Legacy records written before P3 carry no channel_id.
	require.NoError(t, db.Create(&Request{
		RequestID: "legacy-req", BuyerUserID: 1, BuyerTokenID: 1,
		SellerID: listing.SellerID, ListingID: listing.ID, Model: "deepseek-flash",
		Status: RequestSuccess,
	}).Error)
	require.NoError(t, db.Create(&LedgerEntry{
		RequestID: "legacy-req", EntryType: EntryBuyerDebit, Account: BuyerAccount(1),
		ListingID: listing.ID, AmountMicro: -1, Status: LedgerAvailable,
	}).Error)

	require.NoError(t, BackfillSellerChannels(db))

	var channel SellerChannel
	require.NoError(t, db.Where("id = ?", listing.ID).Take(&channel).Error)
	assert.Equal(t, listing.ID, channel.ID, "the seller channel reuses the listing id")
	assert.Equal(t, listing.SellerID, channel.SellerID)
	assert.Positive(t, channel.TrustPoolID, "api.deepseek.com matches a seeded trust-pool row")
	assert.Equal(t, "https://api.deepseek.com", channel.Base)
	assert.Equal(t, KeyStrategyWeightedRoundRobin, channel.KeyStrategy)
	assert.Equal(t, 7, channel.ConcurrencyLimit)
	assert.Equal(t, 30, channel.RPMLimit)
	assert.Equal(t, int64(1234), channel.DailyCapMicro)
	assert.Equal(t, listing.Hours, channel.Hours)
	assert.Equal(t, ListingLive, channel.Status)
	assert.Equal(t, int64(3), channel.Version)

	var key ChannelKey
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Take(&key).Error)
	assert.Equal(t, listing.KeyFingerprint, key.KeyFingerprint)
	assert.Equal(t, listing.KeyCiphertext, key.KeyCiphertext)
	assert.Equal(t, ChannelKeyNormal, key.Status)
	assert.Equal(t, 100, key.Weight)
	assert.Equal(t, 7, key.ConcurrencyLimit)
	assert.Equal(t, 30, key.RPMLimit)

	var models []SellerChannelModel
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Order("model ASC").Find(&models).Error)
	require.Len(t, models, 2)
	assert.Equal(t, "deepseek-flash", models[0].Model)
	assert.Equal(t, "deepseek-chat", models[0].UpstreamModel, "upstream name comes from channel.model_mapping")
	assert.Equal(t, "deepseek-v4-pro", models[1].Model)
	assert.Equal(t, "deepseek-reasoner", models[1].UpstreamModel)
	for _, row := range models {
		assert.Equal(t, int64(400), row.MultiplierMilli)
		assert.True(t, row.Enabled)
		assert.True(t, row.VerifiedOpenAIChat)
		assert.True(t, row.VerifiedOpenAIResponses)
		assert.True(t, row.VerifiedAnthropicMessages)
		var allowed []string
		require.NoError(t, common.UnmarshalJsonStr(row.AllowedEndpoints, &allowed))
		assert.ElementsMatch(t, []string{
			EndpointProtocolOpenAIChat, EndpointProtocolOpenAIResponses, EndpointProtocolAnthropicMessages,
		}, allowed)
	}

	var endpoints []ChannelEndpoint
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Find(&endpoints).Error)
	require.Len(t, endpoints, 3, "the seeded deepseek channel serves chat, responses and messages")
	byProtocol := make(map[string]ChannelEndpoint, len(endpoints))
	for _, ep := range endpoints {
		byProtocol[ep.Protocol] = ep
		assert.Equal(t, listing.ChannelID, ep.NewAPIChannelID)
		assert.True(t, ep.Verified)
		require.NotNil(t, ep.VerifiedAt)
	}
	assert.Equal(t, "https://api.deepseek.com", byProtocol[EndpointProtocolOpenAIChat].Base)
	assert.Equal(t, "/v1/chat/completions", byProtocol[EndpointProtocolOpenAIChat].FixedPath)
	assert.Equal(t, EndpointAuthBearer, byProtocol[EndpointProtocolOpenAIChat].AuthMethod)
	assert.True(t, byProtocol[EndpointProtocolOpenAIChat].StreamEndRule)

	assert.Equal(t, "https://api.deepseek.com", byProtocol[EndpointProtocolOpenAIResponses].Base)
	assert.Equal(t, "/v1/responses", byProtocol[EndpointProtocolOpenAIResponses].FixedPath)
	assert.False(t, byProtocol[EndpointProtocolOpenAIResponses].StreamEndRule)

	assert.Equal(t, "https://api.deepseek.com/anthropic", byProtocol[EndpointProtocolAnthropicMessages].Base)
	assert.Equal(t, "/v1/messages", byProtocol[EndpointProtocolAnthropicMessages].FixedPath)
	assert.Equal(t, EndpointAuthXAPIKey, byProtocol[EndpointProtocolAnthropicMessages].AuthMethod)

	var request Request
	require.NoError(t, db.Where("request_id = ?", "legacy-req").Take(&request).Error)
	assert.Equal(t, listing.ID, request.ChannelID)
	var entry LedgerEntry
	require.NoError(t, db.Where("request_id = ?", "legacy-req").Take(&entry).Error)
	assert.Equal(t, listing.ID, entry.ChannelID)
}

// TestBackfillSellerChannelsIdempotentAndBatched proves the batch loop covers
// more than one batch and that a second run writes nothing new.
func TestBackfillSellerChannelsIdempotentAndBatched(t *testing.T) {
	db := newListingTestDB(t)
	base := "https://api.deepseek.com"
	channel := model.Channel{
		Type: constant.ChannelTypeDeepSeek, Key: "mp-managed:batch",
		Name: "legacy:batch", BaseURL: &base, Group: "default",
		CreatedTime: time.Now().Unix(),
	}
	mapping := `{"deepseek-flash":"deepseek-chat"}`
	channel.ModelMapping = &mapping
	require.NoError(t, db.Create(&channel).Error)

	total := sellerChannelBackfillBatch + 1
	for i := range total {
		key := uniqueTestKey("sk-batch")
		ciphertext, err := EncryptListingKey(key)
		require.NoError(t, err)
		fingerprint, err := ListingKeyFingerprint(key)
		require.NoError(t, err)
		require.NoError(t, db.Create(&Listing{
			SellerID:         int64(100 + i),
			ChannelID:        int64(channel.Id),
			Vendor:           "deepseek",
			Models:           []string{"deepseek-flash"},
			MultiplierMilli:  500,
			ConcurrencyLimit: 1,
			Status:           ListingDraft,
			KeyFingerprint:   fingerprint,
			KeyCiphertext:    ciphertext,
			Version:          1,
		}).Error)
	}

	require.NoError(t, BackfillSellerChannels(db))
	countRows := func(dest any) int64 {
		var n int64
		require.NoError(t, db.Model(dest).Count(&n).Error)
		return n
	}
	require.Equal(t, int64(total), countRows(&SellerChannel{}))
	require.Equal(t, int64(total), countRows(&ChannelKey{}))
	require.Equal(t, int64(total), countRows(&SellerChannelModel{}))
	require.Equal(t, int64(total*3), countRows(&ChannelEndpoint{}))

	require.NoError(t, BackfillSellerChannels(db), "a second run must be a no-op")
	assert.Equal(t, int64(total), countRows(&SellerChannel{}))
	assert.Equal(t, int64(total), countRows(&ChannelKey{}))
	assert.Equal(t, int64(total), countRows(&SellerChannelModel{}))
	assert.Equal(t, int64(total*3), countRows(&ChannelEndpoint{}))
}

// TestMirrorListingUpdateTracksListingWrites drives the legacy write paths
// (multiplier, key replacement, state transitions) and asserts the shadow
// tables follow: status/version/multiplier/key material/verified flags. It also
// re-runs the mirror to prove the writes are idempotent (no new rows).
func TestMirrorListingUpdateTracksListingWrites(t *testing.T) {
	db := newListingTestDB(t)
	listing := createTestListing(t, db, 10023, uniqueTestKey("sk-mirror"))

	shadow := func() (SellerChannel, ChannelKey, []SellerChannelModel, []ChannelEndpoint) {
		t.Helper()
		var channel SellerChannel
		require.NoError(t, db.Where("id = ?", listing.ID).Take(&channel).Error)
		var key ChannelKey
		require.NoError(t, db.Where("channel_id = ?", listing.ID).Take(&key).Error)
		var models []SellerChannelModel
		require.NoError(t, db.Where("channel_id = ?", listing.ID).Order("model ASC").Find(&models).Error)
		var endpoints []ChannelEndpoint
		require.NoError(t, db.Where("channel_id = ?", listing.ID).Find(&endpoints).Error)
		return channel, key, models, endpoints
	}

	channel, key, models, endpoints := shadow()
	assert.Equal(t, ListingDraft, channel.Status)
	assert.Equal(t, ChannelKeyNormal, key.Status)
	require.Len(t, models, 2)
	require.Len(t, endpoints, 3)
	for _, row := range models {
		assert.False(t, row.VerifiedOpenAIChat)
	}
	originalKeyID := key.ID

	// Multiplier edit: the per-model rows follow and the version is copied.
	require.NoError(t, UpdateMultiplier(db, listing.ID, 450, time.Now()))
	channel, key, models, _ = shadow()
	assert.Equal(t, int64(2), channel.Version)
	for _, row := range models {
		assert.Equal(t, int64(450), row.MultiplierMilli)
	}
	assert.Equal(t, ChannelKeyNormal, key.Status)

	// Key replacement: the shadow key is updated in place, not duplicated.
	replacement := uniqueTestKey("sk-mirror-replaced")
	require.NoError(t, ReplaceKey(db, listing.ID, replacement))
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	channel, key, _, _ = shadow()
	assert.Equal(t, ListingVerifying, channel.Status)
	assert.Equal(t, originalKeyID, key.ID, "the shadow key keeps its id across a key swap")
	assert.Equal(t, row.KeyFingerprint, key.KeyFingerprint)
	assert.Equal(t, row.KeyCiphertext, key.KeyCiphertext)

	// A passing verification promotes to canary: every verified flag follows.
	require.NoError(t, db.Create(&Probe{ListingID: listing.ID, Type: ProbeTypeVerify, OK: true}).Error)
	require.NoError(t, PassVerification(db, listing.ID))
	channel, key, models, endpoints = shadow()
	assert.Equal(t, ListingCanary, channel.Status)
	assert.Equal(t, ChannelKeyNormal, key.Status)
	for _, m := range models {
		assert.True(t, m.Enabled)
		assert.True(t, m.VerifiedOpenAIChat)
		assert.True(t, m.VerifiedOpenAIResponses)
		assert.True(t, m.VerifiedAnthropicMessages)
	}
	for _, ep := range endpoints {
		assert.True(t, ep.Verified)
		require.NotNil(t, ep.VerifiedAt)
	}

	require.NoError(t, GoLive(db, listing.ID))
	channel, _, _, _ = shadow()
	assert.Equal(t, ListingLive, channel.Status)

	// A pause cools the key down; a ban fails it and clears every verified flag.
	require.NoError(t, PauseListing(db, listing.ID))
	channel, key, _, _ = shadow()
	assert.Equal(t, ListingPaused, channel.Status)
	assert.Equal(t, ChannelKeyCooling, key.Status)

	require.NoError(t, BanListing(db, listing.ID))
	channel, key, models, endpoints = shadow()
	assert.Equal(t, ListingBanned, channel.Status)
	assert.Equal(t, ChannelKeyFailed, key.Status)
	for _, m := range models {
		assert.False(t, m.VerifiedOpenAIChat)
		assert.False(t, m.VerifiedOpenAIResponses)
		assert.False(t, m.VerifiedAnthropicMessages)
	}
	for _, ep := range endpoints {
		assert.False(t, ep.Verified)
		assert.Nil(t, ep.VerifiedAt)
	}

	// Idempotency: a repeated mirror writes the same state and appends nothing.
	require.NoError(t, MirrorListingUpdate(db, listing.ID))
	require.NoError(t, MirrorListingUpdate(db, listing.ID))
	var keyCount, modelCount, endpointCount int64
	require.NoError(t, db.Model(&ChannelKey{}).Where("channel_id = ?", listing.ID).Count(&keyCount).Error)
	require.NoError(t, db.Model(&SellerChannelModel{}).Where("channel_id = ?", listing.ID).Count(&modelCount).Error)
	require.NoError(t, db.Model(&ChannelEndpoint{}).Where("channel_id = ?", listing.ID).Count(&endpointCount).Error)
	assert.EqualValues(t, 1, keyCount)
	assert.EqualValues(t, 2, modelCount)
	assert.EqualValues(t, 3, endpointCount)
}

// TestMirrorListingUpdateCreatesMissingShadow proves mirroring repairs a
// legacy listing that was never backfilled: the first run creates the seller
// channel, key, models and endpoints.
func TestMirrorListingUpdateCreatesMissingShadow(t *testing.T) {
	db := newListingTestDB(t)
	listing, _ := legacyListingFixture(t, db, 42,
		[]string{"deepseek-flash", "deepseek-v4-pro"}, ListingLive, 400)

	require.NoError(t, MirrorListingUpdate(db, listing.ID))

	var channel SellerChannel
	require.NoError(t, db.Where("id = ?", listing.ID).Take(&channel).Error)
	assert.Equal(t, ListingLive, channel.Status)
	assert.Equal(t, listing.Version, channel.Version)
	var key ChannelKey
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Take(&key).Error)
	assert.Equal(t, listing.KeyFingerprint, key.KeyFingerprint)
	var models []SellerChannelModel
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Find(&models).Error)
	require.Len(t, models, 2)
	for _, row := range models {
		assert.Equal(t, int64(400), row.MultiplierMilli)
		assert.True(t, row.Enabled)
		assert.True(t, row.VerifiedOpenAIChat)
	}
	var endpoints int64
	require.NoError(t, db.Model(&ChannelEndpoint{}).Where("channel_id = ?", listing.ID).Count(&endpoints).Error)
	assert.EqualValues(t, 3, endpoints)
}

// TestMirrorListingDeleteSoftDeletesKey proves removal keeps historical key ids
// resolvable while disabling the channel and its models, and that a repeat run
// is a no-op.
func TestMirrorListingDeleteSoftDeletesKey(t *testing.T) {
	db := newListingTestDB(t)
	listing := createTestListing(t, db, 10023, uniqueTestKey("sk-mirror-delete"))

	require.NoError(t, MirrorListingDelete(db, listing.ID))
	require.NoError(t, MirrorListingDelete(db, listing.ID), "a repeated delete must be idempotent")

	var active int64
	require.NoError(t, db.Model(&ChannelKey{}).Where("channel_id = ?", listing.ID).Count(&active).Error)
	assert.Zero(t, active, "the active key list is empty")
	var deleted ChannelKey
	require.NoError(t, db.Unscoped().Where("channel_id = ?", listing.ID).Take(&deleted).Error)
	assert.True(t, deleted.DeletedAt.Valid, "the key row stays as a soft-deleted historical record")

	var models []SellerChannelModel
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Find(&models).Error)
	require.NotEmpty(t, models)
	for _, row := range models {
		assert.False(t, row.Enabled)
	}
	var channel SellerChannel
	require.NoError(t, db.Where("id = ?", listing.ID).Take(&channel).Error)
	assert.Equal(t, sellerChannelStatusDeleted, channel.Status)

	// The legacy listing still exists, so a later mirror brings the shadow back
	// rather than creating a duplicate key row.
	require.NoError(t, MirrorListingUpdate(db, listing.ID))
	require.NoError(t, db.Model(&ChannelKey{}).Where("channel_id = ?", listing.ID).Count(&active).Error)
	assert.EqualValues(t, 1, active, "the soft-deleted key is revived in place")
	var revived ChannelKey
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Take(&revived).Error)
	assert.Equal(t, deleted.ID, revived.ID)
	require.NoError(t, db.Where("id = ?", listing.ID).Take(&channel).Error)
	assert.Equal(t, ListingDraft, channel.Status, "the shadow status follows the live legacy row again")
}

// TestCheckSellerChannelConsistencyDetectsAndRepairs proves the P5
// reconciliation reports the listing id plus each mismatch (status, multiplier,
// key fingerprint) and that a mirror run repairs them; an orphan shadow channel
// is reported too.
func TestCheckSellerChannelConsistencyDetectsAndRepairs(t *testing.T) {
	db := newListingTestDB(t)
	listing := createTestListing(t, db, 10023, uniqueTestKey("sk-consistency"))
	other := createTestListing(t, db, 10024, uniqueTestKey("sk-consistency-other"))

	issues, err := CheckSellerChannelConsistency(db)
	require.NoError(t, err)
	assert.Empty(t, issues)

	require.NoError(t, db.Model(&SellerChannel{}).Where("id = ?", listing.ID).
		Update("status", ListingLive).Error)
	require.NoError(t, db.Model(&SellerChannelModel{}).
		Where("channel_id = ? AND model = ?", listing.ID, "deepseek-flash").
		Update("multiplier_milli", 999).Error)
	require.NoError(t, db.Model(&ChannelKey{}).Where("channel_id = ?", listing.ID).
		Update("key_fingerprint", "tampered-fingerprint").Error)

	issues, err = CheckSellerChannelConsistency(db)
	require.NoError(t, err)
	report := strings.Join(issues, "\n")
	assert.Contains(t, report, fmt.Sprintf("listing %d: status mismatch", listing.ID))
	assert.Contains(t, report, fmt.Sprintf("listing %d: model \"deepseek-flash\" multiplier mismatch", listing.ID))
	assert.Contains(t, report, fmt.Sprintf("listing %d: key fingerprint mismatch", listing.ID))
	assert.NotContains(t, report, fmt.Sprintf("listing %d:", other.ID), "a consistent listing is not reported")

	require.NoError(t, MirrorListingUpdate(db, listing.ID))
	issues, err = CheckSellerChannelConsistency(db)
	require.NoError(t, err)
	assert.Empty(t, issues, "mirroring repairs the shadow mismatch")

	// An orphan shadow channel has no authoritative listing.
	require.NoError(t, db.Create(&SellerChannel{
		ID:          99999,
		SellerID:    7,
		KeyStrategy: KeyStrategyWeightedRoundRobin,
		Status:      ListingDraft,
	}).Error)
	issues, err = CheckSellerChannelConsistency(db)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], "seller channel 99999: no legacy listing")
}

// TestBackfillSellerChannelsEmptyModels keeps an empty model list from
// producing model rows, and proves a non-served status leaves the verified
// flags cleared.
func TestBackfillSellerChannelsEmptyModels(t *testing.T) {
	db := newListingTestDB(t)
	listing, _ := legacyListingFixture(t, db, 7, nil, ListingDraft, 250)

	require.NoError(t, BackfillSellerChannels(db))

	var models int64
	require.NoError(t, db.Model(&SellerChannelModel{}).Where("channel_id = ?", listing.ID).Count(&models).Error)
	assert.Zero(t, models)

	var channel SellerChannel
	require.NoError(t, db.Where("id = ?", listing.ID).Take(&channel).Error)
	assert.Equal(t, ListingDraft, channel.Status)

	var endpoints []ChannelEndpoint
	require.NoError(t, db.Where("channel_id = ?", listing.ID).Find(&endpoints).Error)
	require.Len(t, endpoints, 3)
	for _, ep := range endpoints {
		assert.False(t, ep.Verified, "a draft listing is unverified")
		assert.Nil(t, ep.VerifiedAt)
	}
}
