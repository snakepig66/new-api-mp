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
