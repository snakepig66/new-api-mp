package marketplace

// modelpool_test.go — 模型池 / 渠道池 P1: migrations, seeds, legacy migration,
// alias resolution and the bare accessors (the admin HTTP surface is covered in
// api_admin_test.go).

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedPool migrates and seeds the three pool tables on an already migrated
// database.
func seedPool(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, MigrateModelChannelPool(db))
	require.NoError(t, SeedModelChannelPool(db))
}

func countRows(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(model).Count(&n).Error)
	return n
}

// TestModelPoolMigrateAndSeedIdempotent checks the three seed sets and that a
// second startup changes nothing.
func TestModelPoolMigrateAndSeedIdempotent(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	seedPool(t, db)

	assert.EqualValues(t, len(modelPoolSeeds), countRows(t, db, &ModelPool{}))
	assert.EqualValues(t, len(channelPoolSeeds), countRows(t, db, &ChannelPool{}))
	assert.EqualValues(t, len(poolChannelModelSeeds), countRows(t, db, &ChannelModel{}))

	// Seeded model content.

	// Seeded model content.
	glm, ok := ModelPoolByName(db, "glm-5.3")
	require.True(t, ok)
	assert.Equal(t, "zhipu", glm.OriginVendor)
	assert.EqualValues(t, 200000, glm.ContextLength)
	assert.ElementsMatch(t, []string{"text", "tools", "cache"}, parseStringList(glm.Capabilities))
	assert.ElementsMatch(t, []string{"glm5.3", "GLM-5.3", "zhipu/glm-5.3"}, parseStringList(glm.Aliases))
	assert.Equal(t, PoolStatusActive, glm.Status)
	assert.NotEmpty(t, glm.VerifyPrompt)

	// Seeded channel content.
	ark, ok := ChannelPoolBySlug(db, "volcengine-ark")
	require.True(t, ok)
	assert.Equal(t, ChannelKindOfficial, ark.Kind)
	assert.True(t, ark.StripV1Prefix)
	assert.Contains(t, parseStringList(ark.DenyPaths), "/api/coding")
	assert.Equal(t, "openai", ark.Adaptor)

	plan, ok := ChannelPoolBySlug(db, "aliyun-bailian-coding-plan")
	require.True(t, ok)
	assert.Equal(t, ChannelKindPlan, plan.Kind)
	assert.Equal(t, `^sk-(sp|ws)-`, plan.KeyPattern)
	assert.NotEmpty(t, plan.QuotaWindow)
	assert.Nil(t, plan.TermsCheckedAt)

	// A second seed is a no-op.
	require.NoError(t, SeedModelChannelPool(db))
	assert.EqualValues(t, len(modelPoolSeeds), countRows(t, db, &ModelPool{}))
	assert.EqualValues(t, len(channelPoolSeeds), countRows(t, db, &ChannelPool{}))
	assert.EqualValues(t, len(poolChannelModelSeeds), countRows(t, db, &ChannelModel{}))
}

// TestModelPoolResolveAlias covers case-insensitive Name and Aliases matching.
func TestModelPoolResolveAlias(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	seedPool(t, db)

	for _, input := range []string{"glm5.3", "GLM-5.3", "zhipu/glm-5.3", "glm-5.3", "  GLM5.3  "} {
		got, ok := ModelPoolResolveAlias(db, input)
		assert.True(t, ok, input)
		assert.Equal(t, "glm-5.3", got, input)
	}
	got, ok := ModelPoolResolveAlias(db, "DeepSeek-Flash")
	assert.True(t, ok)
	assert.Equal(t, "deepseek-flash", got)

	_, ok = ModelPoolResolveAlias(db, "unknown-model")
	assert.False(t, ok)
	_, ok = ModelPoolResolveAlias(db, "")
	assert.False(t, ok)
	_, ok = ModelPoolResolveAlias(nil, "glm-5.3")
	assert.False(t, ok)
}

// TestChannelPoolAsVendor checks the Vendor field mapping used by probe/relay
// call sites.
func TestChannelPoolAsVendor(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	seedPool(t, db)

	v, ok := ChannelPoolAsVendor(db, "zhipu")
	require.True(t, ok)
	assert.Equal(t, "zhipu", v.ID)
	assert.Equal(t, "智谱 BigModel", v.DisplayName)
	assert.Equal(t, VendorTierOfficial, v.Tier)
	assert.Equal(t, "https://open.bigmodel.cn/api/paas/v4", v.Protocols.OpenAI)
	assert.Equal(t, "https://open.bigmodel.cn/api/anthropic", v.Protocols.Anthropic)
	assert.Equal(t, "openai", v.Adaptor)
	assert.True(t, v.StripV1Prefix)
	assert.False(t, v.PassthroughBody)
	assert.Equal(t, []string{"/api/coding/paas/v4"}, v.DenyPaths)
	assert.Contains(t, v.Models, "glm-5.3")
	assert.Contains(t, v.Models, "glm-4.6")

	plan, ok := ChannelPoolAsVendor(db, "volcengine-ark-coding-plan")
	require.True(t, ok)
	assert.Equal(t, ChannelKindPlan, plan.Tier)
	assert.Equal(t, "https://ark.cn-beijing.volces.com/api/coding/v3", plan.BaseURL)
	assert.Empty(t, plan.DenyPaths)

	bailian, ok := ChannelPoolAsVendor(db, "aliyun-bailian")
	require.True(t, ok)
	assert.Equal(t, `^[a-z0-9-]+\.cn-beijing\.maas\.aliyuncs\.com$`, bailian.AllowHostPattern)
	assert.Equal(t, []string{"sk-sp-", "sk-ws-"}, bailian.DenyKeyPrefixes)

	_, ok = ChannelPoolAsVendor(db, "no-such-channel")
	assert.False(t, ok)
}

// TestUpsertChannelModelPoolConflictsAndValidation covers both unique-key
// conflicts and input validation.
func TestUpsertChannelModelPoolConflictsAndValidation(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	seedPool(t, db)

	// Same channel + upstream, different model → conflict.
	_, err := UpsertChannelModelPool(db, ChannelModel{
		ChannelSlug: "deepseek", UpstreamModel: "deepseek-flash", Model: "glm-5.3",
		CoefficientMilli: 1000, Status: PoolStatusActive,
	})
	assert.ErrorIs(t, err, ErrChannelModelConflict)

	// Same channel + model, different upstream → conflict.
	_, err = UpsertChannelModelPool(db, ChannelModel{
		ChannelSlug: "volcengine-ark", UpstreamModel: "deepseek-v3-2-flash-v2", Model: "deepseek-flash",
		CoefficientMilli: 1000, Status: PoolStatusActive,
	})
	assert.ErrorIs(t, err, ErrChannelModelConflict)

	// Coefficient range.
	_, err = UpsertChannelModelPool(db, ChannelModel{
		ChannelSlug: "moonshot", UpstreamModel: "glm-5.3-ms", Model: "glm-5.3",
		CoefficientMilli: 0, Status: PoolStatusActive,
	})
	assert.ErrorIs(t, err, ErrChannelModelCoefficient)
	_, err = UpsertChannelModelPool(db, ChannelModel{
		ChannelSlug: "moonshot", UpstreamModel: "glm-5.3-ms", Model: "glm-5.3",
		CoefficientMilli: 10001, Status: PoolStatusActive,
	})
	assert.ErrorIs(t, err, ErrChannelModelCoefficient)

	// Unknown channel/model and bad JSON/status.
	_, err = UpsertChannelModelPool(db, ChannelModel{
		ChannelSlug: "no-such", UpstreamModel: "x", Model: "glm-5.3",
		CoefficientMilli: 1000, Status: PoolStatusActive,
	})
	assert.ErrorIs(t, err, ErrChannelPoolNotFound)
	_, err = UpsertChannelModelPool(db, ChannelModel{
		ChannelSlug: "moonshot", UpstreamModel: "x", Model: "no-such-model",
		CoefficientMilli: 1000, Status: PoolStatusActive,
	})
	assert.ErrorIs(t, err, ErrModelPoolNotFound)
	_, err = UpsertChannelModelPool(db, ChannelModel{
		ChannelSlug: "moonshot", UpstreamModel: "glm-5.3-ms", Model: "glm-5.3",
		CoefficientMilli: 1000, Differences: "not-json", Status: PoolStatusActive,
	})
	assert.ErrorIs(t, err, ErrChannelModelDifferences)
	_, err = UpsertChannelModelPool(db, ChannelModel{
		ChannelSlug: "moonshot", UpstreamModel: "glm-5.3-ms", Model: "glm-5.3",
		CoefficientMilli: 1000, Status: "bogus",
	})
	assert.ErrorIs(t, err, ErrChannelModelStatusInvalid)

	// A valid new qualification.
	row, err := UpsertChannelModelPool(db, ChannelModel{
		ChannelSlug: "moonshot", UpstreamModel: "glm-5.3-ms", Model: "glm-5.3",
		CoefficientMilli: 1200, Differences: `{"no_tools":true}`, Status: PoolStatusActive,
	})
	require.NoError(t, err)
	assert.NotZero(t, row.ID)
	assert.EqualValues(t, 1200, row.CoefficientMilli)

	// Updating the same (channel, upstream) row updates in place.
	row, err = UpsertChannelModelPool(db, ChannelModel{
		ChannelSlug: "moonshot", UpstreamModel: "glm-5.3-ms", Model: "glm-5.3",
		CoefficientMilli: 800, Status: PoolStatusActive,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 800, row.CoefficientMilli)
	var n int64
	require.NoError(t, db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND upstream_model = ?", "moonshot", "glm-5.3-ms").Count(&n).Error)
	assert.EqualValues(t, 1, n)
}

// TestSetChannelModelVerification checks verified/ratio/verified_at updates.
func TestSetChannelModelVerification(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	seedPool(t, db)

	require.NoError(t, SetChannelModelVerification(db, "deepseek", "deepseek-flash", false, 0.5))
	row, ok := ChannelModelFor(db, "deepseek", "deepseek-flash")
	require.True(t, ok)
	assert.False(t, row.Verified)
	assert.InDelta(t, 0.5, row.FingerprintRatio, 1e-9)
	assert.Nil(t, row.VerifiedAt)
	_, ok = VerifiedChannelModelFor(db, "deepseek", "deepseek-flash")
	assert.False(t, ok)

	require.NoError(t, SetChannelModelVerification(db, "deepseek", "deepseek-flash", true, 0.99))
	row, ok = ChannelModelFor(db, "deepseek", "deepseek-flash")
	require.True(t, ok)
	assert.True(t, row.Verified)
	assert.InDelta(t, 0.99, row.FingerprintRatio, 1e-9)
	require.NotNil(t, row.VerifiedAt)
	_, ok = VerifiedChannelModelFor(db, "deepseek", "deepseek-flash")
	assert.True(t, ok)

	err := SetChannelModelVerification(db, "deepseek", "no-such-upstream", true, 1)
	assert.ErrorIs(t, err, ErrChannelModelNotFound)
}

// TestLegacyChannelMapMigrationIdempotent pre-seeds the legacy
// mp_channel_model_map with an operator row and checks it lands exactly once in
// mp_channel_models.
func TestLegacyChannelMapMigrationIdempotent(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db) // creates and seeds mp_channel_model_map

	legacy := ChannelModelMap{Vendor: "legacy-vendor", Model: "legacy-model", UpstreamModel: "legacy-upstream"}
	require.NoError(t, db.Create(&legacy).Error)

	seedPool(t, db)
	require.NoError(t, SeedModelChannelPool(db))

	var rows []ChannelModel
	require.NoError(t, db.Where("channel_slug = ?", "legacy-vendor").Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, "legacy-model", rows[0].Model)
	assert.Equal(t, "legacy-upstream", rows[0].UpstreamModel)
	assert.EqualValues(t, 1000, rows[0].CoefficientMilli)
	assert.True(t, rows[0].Verified)
	assert.Equal(t, PoolStatusActive, rows[0].Status)

	// The legacy table is retained.
	assert.EqualValues(t, len(channelModelSeeds)+1, countRows(t, db, &ChannelModelMap{}))
}

// TestSeedModelChannelPoolPriceDedupe removes identical non-origin price rows
// and keeps genuinely different ones.
func TestSeedModelChannelPoolPriceDedupe(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db) // SeedPrices writes identical ark/bailian copies
	seedPool(t, db)

	var copies int64
	require.NoError(t, db.Model(&PriceBook{}).
		Where("model = ? AND vendor IN ?", "deepseek-flash", []string{"volcengine-ark", "aliyun-bailian"}).
		Count(&copies).Error)
	assert.Zero(t, copies, "identical copies of the origin price are removed")

	var origin int64
	require.NoError(t, db.Model(&PriceBook{}).
		Where("model = ? AND vendor = ?", "deepseek-flash", "deepseek").Count(&origin).Error)
	assert.EqualValues(t, 2, origin, "origin vendor price rows are kept")

	// A deliberately different price is kept and only logged.
	custom := PriceBook{
		Model: "deepseek-flash", Vendor: "custom-cloud", Tier: TierFlat, Version: 1,
		InputMicroPerMTok: 7, OutputMicroPerMTok: 8,
		CacheReadMicroPerMTok: 1, CacheWriteMicroPerMTok: 2,
		EffectiveAt: time.Now(),
	}
	require.NoError(t, db.Create(&custom).Error)
	require.NoError(t, SeedModelChannelPool(db))
	var kept int64
	require.NoError(t, db.Model(&PriceBook{}).Where("id = ?", custom.ID).Count(&kept).Error)
	assert.EqualValues(t, 1, kept)
}

// TestSeedModelChannelPoolPausesUnmappedListings checks the legacy-listing
// migration: unmapped listings are paused with exactly one migration probe,
// mapped listings are untouched and banned stays terminal.
func TestSeedModelChannelPoolPausesUnmappedListings(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	seedPool(t, db)

	live := Listing{
		SellerID: 1, ChannelID: 1, Vendor: "deepseek", Models: []string{"deepseek-flash"},
		MultiplierMilli: 500, Status: ListingLive, KeyFingerprint: "fp-live", Version: 1,
	}
	ghost := Listing{
		SellerID: 1, ChannelID: 2, Vendor: "ghost-vendor", Models: []string{"deepseek-flash"},
		MultiplierMilli: 500, Status: ListingLive, KeyFingerprint: "fp-ghost", Version: 1,
	}
	unmapped := Listing{
		SellerID: 1, ChannelID: 3, Vendor: "deepseek", Models: []string{"deepseek-flash", "bogus-model"},
		MultiplierMilli: 500, Status: ListingVerifying, KeyFingerprint: "fp-unmapped", Version: 1,
	}
	banned := Listing{
		SellerID: 1, ChannelID: 4, Vendor: "ghost-vendor", Models: []string{"deepseek-flash"},
		MultiplierMilli: 500, Status: ListingBanned, KeyFingerprint: "fp-banned", Version: 1,
	}
	for _, row := range []*Listing{&live, &ghost, &unmapped, &banned} {
		require.NoError(t, db.Create(row).Error)
	}

	require.NoError(t, SeedModelChannelPool(db))
	require.NoError(t, SeedModelChannelPool(db))

	reload := func(id int64) Listing {
		var row Listing
		require.NoError(t, db.Where("id = ?", id).Take(&row).Error)
		return row
	}
	assert.Equal(t, ListingLive, reload(live.ID).Status, "fully mapped listing is untouched")
	assert.Equal(t, ListingPaused, reload(ghost.ID).Status, "listing with an unknown channel is paused")
	assert.Equal(t, ListingPaused, reload(unmapped.ID).Status, "listing with an unmapped model is paused")
	assert.Equal(t, ListingBanned, reload(banned.ID).Status, "banned is terminal")

	var probes []Probe
	require.NoError(t, db.Where("type = ?", "migration").Order("listing_id ASC").Find(&probes).Error)
	require.Len(t, probes, 2, "one migration probe per paused listing, no duplicates on restart")
	assert.EqualValues(t, ghost.ID, probes[0].ListingID)
	assert.EqualValues(t, unmapped.ID, probes[1].ListingID)
	for _, probe := range probes {
		assert.False(t, probe.OK)
		assert.Equal(t, poolMigrationFailReason, probe.Detail)
	}
}

// TestSeedCatalogCoversCommonModels pins the 2026-10-25 catalog import: every
// newly seeded common model must have an origin price book, a verified
// qualification on its origin channel, and an identity upstream mapping, so
// the square can advertise it and sellers can list it without admin edits.
func TestSeedCatalogCoversCommonModels(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	seedPool(t, db)

	type want struct {
		model   string
		vendor  string
		input   int64
		output  int64
		cache   int64
		context int64
	}
	cases := []want{
		{"kimi-k3.1-pro", "moonshot", 13_000_000, 68_000_000, 1_300_000, 1048576},
		{"kimi-k3", "moonshot", 20_000_000, 100_000_000, 2_000_000, 1048576},
		{"kimi-k2.7-code", "moonshot", 6_500_000, 27_000_000, 1_300_000, 262144},
		{"kimi-k2.6", "moonshot", 6_500_000, 27_000_000, 1_100_000, 262144},
		{"glm-5.3", "zhipu", 8_000_000, 28_000_000, 2_000_000, 200000},
		{"glm-5.3-flash", "zhipu", 800_000, 2_800_000, 230_000, 200000},
		{"glm-5.3-flashx", "zhipu", 2_000_000, 7_000_000, 570_000, 200000},
		{"glm-5.2", "zhipu", 8_000_000, 28_000_000, 2_000_000, 200000},
		{"glm-4.7-flash", "zhipu", 0, 0, 0, 200000},
		{"minimax-m3", "minimax", 2_100_000, 8_400_000, 420_000, 512000},
		{"minimax-m3.1-flash", "minimax", 1_000_000, 4_000_000, 100_000, 512000},
		{"qwen3.8-max", "aliyun-bailian", 12_000_000, 36_000_000, 12_000_000, 1000000},
		{"qwen3.7-plus", "aliyun-bailian", 2_000_000, 8_000_000, 2_000_000, 256000},
		{"qwen3.8-flash", "aliyun-bailian", 800_000, 2_700_000, 800_000, 1000000},
		{"doubao-seed-evolving", "volcengine-ark", 6_000_000, 30_000_000, 1_200_000, 256000},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			m, ok := ModelPoolByName(db, tc.model)
			require.True(t, ok, "model must be seeded")
			assert.Equal(t, tc.vendor, m.OriginVendor)
			assert.EqualValues(t, tc.context, m.ContextLength)

			pb, err := PriceAt(db, tc.model, tc.vendor, TierFlat, time.Now())
			require.NoError(t, err, "origin price book must exist")
			assert.EqualValues(t, tc.input, pb.InputMicroPerMTok)
			assert.EqualValues(t, tc.output, pb.OutputMicroPerMTok)
			assert.EqualValues(t, tc.cache, pb.CacheReadMicroPerMTok)

			cm, ok := VerifiedChannelModelFor(db, tc.vendor, tc.model)
			require.True(t, ok, "verified qualification must exist")
			assert.EqualValues(t, 1000, cm.CoefficientMilli)
			assert.Equal(t, tc.model, cm.UpstreamModel, "official upstream id is the standard name")

			qualified, mapped := ChannelModelFor(db, tc.vendor, tc.model)
			assert.True(t, mapped)
			assert.Equal(t, tc.model, qualified.UpstreamModel)
		})
	}
}
