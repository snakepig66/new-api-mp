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
	require.NoError(t, db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'mp_%' ORDER BY name").Scan(&tables).Error)
	got := make(map[string]bool, len(tables))
	for _, name := range tables {
		got[name] = true
	}
	for _, name := range want {
		assert.True(t, got[name], "missing marketplace table %s", name)
	}
	assert.Len(t, tables, len(want), "exactly the %d marketplace tables of §4.2", len(want))

	var indexSQL string
	require.NoError(t, db.Raw("SELECT COALESCE(sql, '') FROM sqlite_master WHERE type='index' AND name='uk_mp_ledger_req_type'").Scan(&indexSQL).Error)
	require.NotEmpty(t, indexSQL, "mp_ledger must have the (request_id, entry_type) unique index")
	assert.Contains(t, indexSQL, "UNIQUE")
	assert.Contains(t, indexSQL, "request_id")
	assert.Contains(t, indexSQL, "entry_type")
}

func TestUserIDWatermark(t *testing.T) {
	db := newTestDB(t)

	// Simulate upstream creating the users table.
	require.NoError(t, db.AutoMigrate(&model.User{}))

	// Probe: ensureUserIDStart's sqlite branch relies on the users DDL
	// containing AUTOINCREMENT; without it the watermark cannot persist.
	var ddl string
	require.NoError(t, db.Raw("SELECT COALESCE(sql, '') FROM sqlite_master WHERE type='table' AND name='users'").Scan(&ddl).Error)
	require.Contains(t, strings.ToUpper(ddl), "AUTOINCREMENT",
		"glebarez sqlite must emit AUTOINCREMENT for users.id, otherwise the id watermark premise of migrate.go does not hold")

	require.NoError(t, Migrate(db))

	first := model.User{Username: "wm-first", Password: "placeholder-pass", AffCode: "wm-first"}
	require.NoError(t, db.Create(&first).Error)
	assert.Equal(t, FirstUserID, int64(first.Id), "first user after migration gets the public numbering start")

	second := model.User{Username: "wm-second", Password: "placeholder-pass", AffCode: "wm-second"}
	require.NoError(t, db.Create(&second).Error)
	assert.Equal(t, FirstUserID+1, int64(second.Id))

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
