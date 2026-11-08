package marketplace

import (
	"time"

	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/model"
)

func TestMigrateCreatesTables(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	// The model/channel pool is migrated by its own Init step; the table
	// assertion below covers all three pool tables.
	require.NoError(t, MigrateModelChannelPool(db))

	want := []string{
		"mp_sellers",
		"mp_price_books",
		"mp_per_call_prices",
		"mp_holidays",
		"mp_listings",
		"mp_listing_groups",
		"mp_listing_models",
		"mp_probes",
		"mp_key_prefs",
		"mp_key_models",
		"mp_requests",
		"mp_response_bindings",
		"mp_affinity_bindings",
		"mp_ledger",
		"mp_payouts",
		"mp_channel_model_map",
		"mp_models",
		"mp_channels",
		"mp_channel_models",
		"mp_notifications",
		"mp_trust_pool",
		"mp_trust_pool_audit",
		"mp_trust_pool_approvals",
		"mp_ops_metrics",
		"mp_seller_channels",
		"mp_channel_endpoints",
		"mp_channel_keys",
		"mp_seller_channel_models",
		"mp_multiplier_changes",
		"mp_listing_audit",
	}
	var tables []string
	switch db.Dialector.Name() {
	case "mysql":
		require.NoError(t, db.Raw("SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name LIKE 'mp\\_%' ORDER BY table_name").Scan(&tables).Error)
	case "postgres":
		require.NoError(t, db.Raw("SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = 'public' AND tablename LIKE 'mp\\_%' ORDER BY tablename").Scan(&tables).Error)
	default:
		require.NoError(t, db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'mp_%' ORDER BY name").Scan(&tables).Error)
	}
	got := make(map[string]bool, len(tables))
	for _, name := range tables {
		got[name] = true
	}
	for _, name := range want {
		assert.True(t, got[name], "missing marketplace table %s", name)
	}
	assert.Len(t, tables, len(want), "exactly the %d marketplace tables of §4.2", len(want))

	// The (request_id, entry_type) unique index on mp_ledger must exist. The
	// index DDL is dialect-specific; assert the index exists and is unique via
	// the dialect's catalog instead of parsing CREATE INDEX text.
	switch db.Dialector.Name() {
	case "mysql":
		// information_schema.statistics has one row per index COLUMN, and
		// uk_mp_ledger_req_type spans two columns — count distinct index names.
		var n int64
		require.NoError(t, db.Raw("SELECT COUNT(DISTINCT index_name) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'mp_ledger' AND index_name = 'uk_mp_ledger_req_type' AND non_unique = 0").Scan(&n).Error)
		assert.EqualValues(t, 1, n, "mp_ledger must have the (request_id, entry_type) unique index")
	case "postgres":
		var n int64
		require.NoError(t, db.Raw("SELECT COUNT(*) FROM pg_catalog.pg_indexes WHERE schemaname = 'public' AND tablename = 'mp_ledger' AND indexname = 'uk_mp_ledger_req_type'").Scan(&n).Error)
		assert.EqualValues(t, 1, n, "mp_ledger must have the (request_id, entry_type) unique index")
	default:
		var indexSQL string
		require.NoError(t, db.Raw("SELECT COALESCE(sql, '') FROM sqlite_master WHERE type='index' AND name='uk_mp_ledger_req_type'").Scan(&indexSQL).Error)
		require.NotEmpty(t, indexSQL, "mp_ledger must have the (request_id, entry_type) unique index")
		assert.Contains(t, indexSQL, "UNIQUE")
		assert.Contains(t, indexSQL, "request_id")
		assert.Contains(t, indexSQL, "entry_type")
	}
}

func TestMigrateRequestTelemetryColumns(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	// The usage-record page reads first_token_ms / is_stream / token_group off
	// mp_requests, so the migration must create them with the documented
	// defaults on all three dialects (0 / false / '').
	require.NoError(t, db.Create(&Request{
		RequestID: "req-migrate-telemetry", BuyerUserID: 1, BuyerTokenID: 1,
		SellerID: 1, ListingID: 1, Model: "m", Tier: TierPeak, Status: RequestSuccess,
	}).Error)
	var row Request
	require.NoError(t, db.Where("request_id = ?", "req-migrate-telemetry").Take(&row).Error)
	assert.Zero(t, row.FirstTokenMs)
	assert.False(t, row.IsStream)
	assert.Empty(t, row.TokenGroup)

	// Existing columns keep their own meaning: a row may carry all three.
	require.NoError(t, db.Model(&Request{}).
		Where("request_id = ?", "req-migrate-telemetry").
		Updates(map[string]any{"first_token_ms": 812, "is_stream": true, "token_group": "vip"}).Error)
	require.NoError(t, db.Where("request_id = ?", "req-migrate-telemetry").Take(&row).Error)
	assert.EqualValues(t, 812, row.FirstTokenMs)
	assert.True(t, row.IsStream)
	assert.Equal(t, "vip", row.TokenGroup)
}

func TestUserIDWatermark(t *testing.T) {
	db := newTestDB(t)

	// Simulate upstream creating the users table.
	require.NoError(t, db.AutoMigrate(&model.User{}))

	// Probe: ensureUserIDStart's sqlite branch relies on the users DDL
	// containing AUTOINCREMENT; without it the watermark cannot persist. This
	// probe is sqlite-only — MySQL/PostgreSQL persist the watermark through
	// AUTO_INCREMENT / sequence respectively and are asserted by the id
	// checks below.
	if db.Dialector.Name() == "sqlite" {
		var ddl string
		require.NoError(t, db.Raw("SELECT COALESCE(sql, '') FROM sqlite_master WHERE type='table' AND name='users'").Scan(&ddl).Error)
		require.Contains(t, strings.ToUpper(ddl), "AUTOINCREMENT",
			"glebarez sqlite must emit AUTOINCREMENT for users.id, otherwise the id watermark premise of migrate.go does not hold")
	}

	require.NoError(t, Migrate(db))

	first := model.User{Username: "wm-first", Password: "placeholder-pass", AffCode: "wm-first"}
	require.NoError(t, db.Create(&first).Error)
	if db.Dialector.Name() == "sqlite" {
		assert.Equal(t, FirstUserID, int64(first.Id), "first user after migration gets the public numbering start")
	} else {
		assert.GreaterOrEqual(t, int64(first.Id), FirstUserID,
			"first user after migration starts at or above the public numbering start")
	}

	second := model.User{Username: "wm-second", Password: "placeholder-pass", AffCode: "wm-second"}
	require.NoError(t, db.Create(&second).Error)
	if db.Dialector.Name() == "sqlite" {
		assert.Equal(t, FirstUserID+1, int64(second.Id))
	} else {
		assert.Greater(t, int64(second.Id), int64(first.Id), "user ids must keep increasing")
	}

	require.NoError(t, Migrate(db), "Migrate must be idempotent over an existing watermark")
}

func TestUserIDWatermarkWithExistingUser(t *testing.T) {
	db := newTestDB(t)

	require.NoError(t, db.AutoMigrate(&model.User{}))
	legacy := model.User{Username: "wm-legacy", Password: "placeholder-pass", AffCode: "wm-legacy"}
	require.NoError(t, db.Create(&legacy).Error)
	require.Equal(t, 1, legacy.Id, "before migration the auto-increment starts at 1")

	require.NoError(t, Migrate(db))

	next := model.User{Username: "wm-after", Password: "placeholder-pass", AffCode: "wm-after"}
	require.NoError(t, db.Create(&next).Error)
	assert.Equal(t, FirstUserID, int64(next.Id), "existing low ids stay untouched; new ids jump to the watermark")
}

func TestMigrateIdempotent(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, Migrate(db))
	require.NoError(t, Migrate(db))
}

// TestFlattenPeakPrices: every (model, vendor) with an off-peak row gets one
// flat row at the off-peak price; peak/off-peak rows stay; a re-run adds
// nothing.
func TestFlattenPeakPrices(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	eff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&PriceBook{Model: "m", Vendor: "v", Tier: TierPeak, Version: 1,
		InputMicroPerMTok: 2, OutputMicroPerMTok: 8, EffectiveAt: eff}).Error)
	require.NoError(t, db.Create(&PriceBook{Model: "m", Vendor: "v", Tier: TierOffpeak, Version: 1,
		InputMicroPerMTok: 1, OutputMicroPerMTok: 4, CacheReadMicroPerMTok: 3, CacheWriteMicroPerMTok: 5, EffectiveAt: eff}).Error)

	now := time.Date(2026, 10, 28, 2, 0, 0, 0, time.UTC)
	require.NoError(t, flattenPeakPrices(db, now))
	require.NoError(t, flattenPeakPrices(db, now.Add(time.Minute)))

	var flats []PriceBook
	require.NoError(t, db.Where("model = ? AND vendor = ? AND tier = ?", "m", "v", TierFlat).Find(&flats).Error)
	require.Len(t, flats, 1, "idempotent")
	assert.EqualValues(t, 1, flats[0].InputMicroPerMTok)
	assert.EqualValues(t, 4, flats[0].OutputMicroPerMTok)
	assert.EqualValues(t, 3, flats[0].CacheReadMicroPerMTok)
	assert.EqualValues(t, 5, flats[0].CacheWriteMicroPerMTok)
	assert.True(t, flats[0].EffectiveAt.Equal(now))

	var total int64
	require.NoError(t, db.Model(&PriceBook{}).Where("model = ?", "m").Count(&total).Error)
	assert.EqualValues(t, 3, total, "peak/off-peak history rows are kept")
}
