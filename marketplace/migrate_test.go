package marketplace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/model"
)

func TestMigrateCreatesTables(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	want := []string{
		"mp_sellers",
		"mp_price_books",
		"mp_per_call_prices",
		"mp_holidays",
		"mp_listings",
		"mp_probes",
		"mp_key_prefs",
		"mp_key_models",
		"mp_requests",
		"mp_ledger",
		"mp_payouts",
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
		var n int64
		require.NoError(t, db.Raw("SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'mp_ledger' AND index_name = 'uk_mp_ledger_req_type' AND non_unique = 0").Scan(&n).Error)
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
