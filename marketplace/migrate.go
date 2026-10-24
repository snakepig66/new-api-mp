package marketplace

import (
	"database/sql"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// FirstUserID is the start of the public user numbering (§3.1): the first
// registered account is #10011.
const FirstUserID int64 = 10011

// Migrate creates or upgrades all marketplace tables and raises the users
// auto-increment watermark to FirstUserID. It is idempotent and safe to run on
// every startup, on SQLite, MySQL and PostgreSQL.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&Seller{},
		&PriceBook{},
		&PerCallPrice{},
		&Holiday{},
		&Listing{},
		&Probe{},
		&KeyPref{},
		&KeyModel{},
		&Request{},
		&LedgerEntry{},
		&Payout{},
	); err != nil {
		return fmt.Errorf("marketplace migrate: %w", err)
	}
	if err := ensureUserIDStart(db, FirstUserID); err != nil {
		return fmt.Errorf("marketplace user id watermark: %w", err)
	}
	return nil
}

// ensureUserIDStart raises the users table auto-increment watermark to start,
// leaving existing ids untouched. It is a no-op when any user id is already
// ≥ start.
func ensureUserIDStart(db *gorm.DB, start int64) error {
	var maxID sql.NullInt64
	if err := db.Raw("SELECT MAX(id) FROM users").Scan(&maxID).Error; err != nil {
		return err
	}
	if maxID.Valid && maxID.Int64 >= start {
		return nil
	}
	switch db.Dialector.Name() {
	case "mysql":
		// InnoDB ignores an AUTO_INCREMENT below the current maximum.
		return db.Exec(fmt.Sprintf("ALTER TABLE users AUTO_INCREMENT = %d", start)).Error
	case "postgres":
		// setval with is_called=false makes the very next nextval return the
		// given value itself.
		return db.Exec("SELECT setval(pg_get_serial_sequence('users','id'), ?, false)", start).Error
	case "sqlite":
		// The users table is created by upstream GORM with INTEGER PRIMARY KEY
		// AUTOINCREMENT, whose high-water mark lives in sqlite_sequence and
		// survives deletes. Without AUTOINCREMENT there is no persistent
		// watermark on an empty table; fail loudly instead of silently
		// handing out low ids.
		var ddl string
		if err := db.Raw("SELECT COALESCE(sql, '') FROM sqlite_master WHERE type='table' AND name='users'").Scan(&ddl).Error; err != nil {
			return err
		}
		if !strings.Contains(strings.ToUpper(ddl), "AUTOINCREMENT") {
			return fmt.Errorf("users table lacks AUTOINCREMENT, cannot persist id watermark %d", start)
		}
		// sqlite_sequence.name has no unique index and may not be indexed, so
		// an upsert cannot work: UPDATE the existing row, INSERT when the
		// fresh AUTOINCREMENT table has no watermark row yet. A row already at
		// or above the watermark is left alone.
		res := db.Exec("UPDATE sqlite_sequence SET seq = ? WHERE name = 'users' AND seq < ?", start-1, start-1)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			return nil
		}
		var cnt int64
		if err := db.Raw("SELECT COUNT(*) FROM sqlite_sequence WHERE name = 'users'").Scan(&cnt).Error; err != nil {
			return err
		}
		if cnt > 0 {
			return nil
		}
		return db.Exec("INSERT INTO sqlite_sequence (name, seq) VALUES ('users', ?)", start-1).Error
	default:
		return fmt.Errorf("unsupported dialect %q", db.Dialector.Name())
	}
}
