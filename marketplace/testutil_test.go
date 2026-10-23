package marketplace

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newTestDB opens an isolated in-memory sqlite database for one test. The DSN
// carries a unique name per call so tests never share state; cache=shared
// keeps the database visible to every pooled connection of the handle.
//
// The pool is capped at one connection: with cache=shared, table-lock
// conflicts between concurrent connections surface as SQLITE_LOCKED and the
// busy handler is never invoked (deadlock avoidance), so a busy_timeout
// pragma cannot serialize writers — a single pooled connection does. The
// concurrency test still runs its goroutines through the race detector and
// asserts convergence on the idempotent-replay path.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:mp_test_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// migrateAll prepares the upstream-owned users table and runs the marketplace
// migrations. Migrate's watermark step (ensureUserIDStart) requires users to
// exist with an AUTOINCREMENT primary key, which mirrors what upstream GORM
// emits for model.User on sqlite.
func migrateAll(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec("CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY AUTOINCREMENT)").Error)
	require.NoError(t, Migrate(db))
}
