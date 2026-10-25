package marketplace

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDriver is the dialect the current test binary runs against. It is
// decided once per process from MP_TEST_MYSQL_DSN / MP_TEST_POSTGRES_DSN and
// defaults to sqlite, so the default `go test ./marketplace/...` run is
// byte-for-byte the pre-阶段-7 behavior.
type testDriver string

const (
	driverSQLite   testDriver = "sqlite"
	driverMySQL    testDriver = "mysql"
	driverPostgres testDriver = "postgres"
)

// mpTestMySQLDSN and mpTestPostgresDSN are the management connection strings
// used to create per-test databases. They are read once at process start; the
// values are stable for the whole binary.
var (
	mpTestMySQLDSN    = os.Getenv("MP_TEST_MYSQL_DSN")
	mpTestPostgresDSN = os.Getenv("MP_TEST_POSTGRES_DSN")
)

func currentTestDriver() testDriver {
	if mpTestMySQLDSN != "" {
		return driverMySQL
	}
	if mpTestPostgresDSN != "" {
		return driverPostgres
	}
	return driverSQLite
}

// newTestDB opens an isolated database for one test.
//
//   - sqlite (default): an in-memory database. The DSN carries a unique name
//     per call so tests never share state; cache=shared keeps the database
//     visible to every pooled connection of the handle.
//
//   - mysql / postgres: a fresh random database (mp_test_<nano>) is created
//     from the management DSN, the handle is pointed at it, and t.Cleanup
//     drops the database. Every test therefore gets library-level isolation
//     exactly like the sqlite path.
//
// The sqlite pool is capped at one connection: with cache=shared, table-lock
// conflicts between concurrent connections surface as SQLITE_LOCKED and the
// busy handler is never invoked (deadlock avoidance), so a busy_timeout
// pragma cannot serialize writers — a single pooled connection does. The
// concurrency test still runs its goroutines through the race detector and
// asserts convergence on the idempotent-replay path. MySQL/PostgreSQL keep a
// real pool: lockForUpdate then emits a true SELECT ... FOR UPDATE, which is
// the expected behavior under real engines.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	switch currentTestDriver() {
	case driverMySQL:
		return newMySQLTestDB(t)
	case driverPostgres:
		return newPostgresTestDB(t)
	default:
		return newSQLiteTestDB(t)
	}
}

func newSQLiteTestDB(t *testing.T) *gorm.DB {
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

// newMySQLTestDB creates a random database from the management DSN and opens a
// handle to it. The DSN is parsed with go-sql-driver so the database name is
// replaced while every other parameter (parseTime, charset, TLS, ...) is
// preserved verbatim.
// newMySQLDSN creates a random per-test database from the management DSN and
// returns the DSN pointing at it. The DSN is parsed with go-sql-driver so the
// database name is replaced while every other parameter (parseTime, charset,
// TLS, ...) is preserved verbatim. t.Cleanup drops the database.
func newMySQLDSN(t *testing.T) string {
	t.Helper()
	cfg, err := gomysql.ParseDSN(mpTestMySQLDSN)
	require.NoError(t, err, "MP_TEST_MYSQL_DSN must be a valid go-sql-driver DSN")
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	require.NoError(t, err)

	name := fmt.Sprintf("mp_test_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err, "create per-test mysql database")
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name)
		_ = admin.Close()
	})

	cfg.DBName = name
	return cfg.FormatDSN()
}

func newMySQLTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(mysql.Open(newMySQLDSN(t)), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// newPostgresDSN creates a random per-test database from the management DSN
// and returns the DSN pointing at it. The DSN is parsed with pgx so the
// database name is replaced while every other parameter (ssl, timezone,
// search_path, ...) is preserved. t.Cleanup drops the database.
//
// Two traps this function deliberately avoids: ConnConfig.ConnString()
// returns the ORIGINAL parsed string and ignores the mutated Database field
// (so the DSN is rebuilt from fields instead), and the admin pool must stay
// open until the cleanup's DROP DATABASE has run.
func newPostgresDSN(t *testing.T) string {
	t.Helper()
	cfg, err := pgx.ParseConfig(mpTestPostgresDSN)
	require.NoError(t, err, "MP_TEST_POSTGRES_DSN must be a valid pgx DSN")

	admin := stdlib.OpenDB(*cfg)

	name := fmt.Sprintf("mp_test_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err, "create per-test postgres database")
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name)
		_ = admin.Close()
	})

	cfg.Database = name
	// Rebuild the DSN from fields: ConnString() would return the pre-mutation
	// string (a pgx quirk), silently pointing every test at the management DB.
	q := url.Values{}
	for k, v := range cfg.RuntimeParams {
		q.Set(k, v)
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))),
		Path:     "/" + name,
		RawQuery: q.Encode(),
	}
	return u.String()
}

func newPostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  newPostgresDSN(t),
		PreferSimpleProtocol: true,
	}), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// NewIntegrationDSN returns the SQL_DSN value that points model.InitDB at an
// isolated database for the model.InitDB-based integration tests (used from
// the external marketplace_test package). The sqlite path keeps the
// file-backed behavior (SQL_DSN=local + SQLitePath); the mysql/postgres paths
// create a fresh random database and return its DSN. The second return value
// reports whether the caller must configure common.SQLitePath (sqlite only).
func NewIntegrationDSN(t *testing.T) (sqlDSN string, isSQLite bool) {
	t.Helper()
	switch currentTestDriver() {
	case driverMySQL:
		return newMySQLDSN(t), false
	case driverPostgres:
		return newPostgresDSN(t), false
	default:
		return "local", true
	}
}

// migrateAll prepares the upstream-owned users table and runs the marketplace
// migrations. Migrate's watermark step (ensureUserIDStart) requires users to
// exist with an auto-increment primary key, which mirrors what upstream GORM
// emits for model.User on each dialect.
func migrateAll(t *testing.T, db *gorm.DB) {
	t.Helper()
	switch db.Dialector.Name() {
	case "mysql":
		require.NoError(t, db.Exec("CREATE TABLE IF NOT EXISTS users (id BIGINT AUTO_INCREMENT PRIMARY KEY)").Error)
	case "postgres":
		require.NoError(t, db.Exec("CREATE TABLE IF NOT EXISTS users (id BIGSERIAL PRIMARY KEY)").Error)
	default:
		require.NoError(t, db.Exec("CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY AUTOINCREMENT)").Error)
	}
	require.NoError(t, Migrate(db))
	// The channel_model_map is part of the production schema and every
	// listing now requires a mapping row (OFFICIAL-CHANNELS §3), so fixtures
	// carry the same seed a real deployment writes at Init. The seeded price
	// books are what make those mappings listable.
	require.NoError(t, MigrateChannelModelMap(db))
	require.NoError(t, SeedChannelModelMap(db))
	require.NoError(t, SeedPrices(db))
}
