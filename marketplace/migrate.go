package marketplace

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

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
		&ListingGroup{},
		&ListingModel{},
		&Probe{},
		&KeyPref{},
		&KeyModel{},
		&Request{},
		&ResponseBinding{},
		&AffinityBinding{},
		&LedgerEntry{},
		&Payout{},
		&Notification{},
		&TrustPool{},
		&TrustPoolAudit{},
		&TrustPoolApproval{},
		&OpsMetric{},
		&SellerChannel{},
		&ChannelEndpoint{},
		&ChannelKey{},
		&SellerChannelModel{},
		&MultiplierChange{},
		&ListingAudit{},
	); err != nil {
		return fmt.Errorf("marketplace migrate: %w", err)
	}
	// The seed rows of the trust pool are part of the initial schema contract:
	// the official vendor addresses must classify as trusted before the first
	// listing is created. SeedTrustPool is idempotent.
	if err := SeedTrustPool(db); err != nil {
		return fmt.Errorf("marketplace seed trust pool: %w", err)
	}
	// P3a: split legacy listings into the shadow tables. Idempotent, batched,
	// and a no-op on a fresh database.
	if err := BackfillSellerChannels(db); err != nil {
		return fmt.Errorf("marketplace backfill seller channels: %w", err)
	}
	if err := flattenPeakPrices(db, time.Now()); err != nil {
		return fmt.Errorf("marketplace flatten peak prices: %w", err)
	}
	if err := ensureUserIDStart(db, FirstUserID); err != nil {
		return fmt.Errorf("marketplace user id watermark: %w", err)
	}
	if err := MigrateKeyTypes(db); err != nil {
		return fmt.Errorf("marketplace migrate key types: %w", err)
	}
	return nil
}

// MigrateKeyTypes migrates existing marketplace tokens to key_type = 'shared',
// and expands any model_scope = 'all' preferences to explicit mp_key_models rows.
func MigrateKeyTypes(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if !db.Migrator().HasTable("tokens") || !db.Migrator().HasTable("mp_key_prefs") {
		return nil
	}
	// 1. Mark existing tokens that have mp_key_prefs or mp_key_models as shared.
	if err := db.Exec("UPDATE tokens SET key_type = 'shared' WHERE id IN (SELECT token_id FROM mp_key_prefs UNION SELECT token_id FROM mp_key_models)").Error; err != nil {
		return fmt.Errorf("migrate key types: %w", err)
	}

	// 2. Query any key prefs that still have model_scope = 'all'.
	var allPrefs []KeyPref
	if err := db.Where("model_scope = ?", ModelScopeAll).Find(&allPrefs).Error; err != nil {
		return fmt.Errorf("find all-scope key prefs: %w", err)
	}
	if len(allPrefs) == 0 {
		return nil
	}

	if !db.Migrator().HasTable("mp_models") {
		return nil
	}

	// Active catalog models from mp_models.
	var activeModels []ModelPool
	if err := db.Where("status = ?", PoolStatusActive).Find(&activeModels).Error; err != nil {
		return fmt.Errorf("query active models for scope expansion: %w", err)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, pref := range allPrefs {
			for _, m := range activeModels {
				var count int64
				if err := tx.Model(&KeyModel{}).
					Where("token_id = ? AND model = ?", pref.TokenID, m.Name).
					Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					km := KeyModel{
						TokenID:       pref.TokenID,
						Model:         m.Name,
						PriceCapMilli: 1000,
					}
					if err := tx.Create(&km).Error; err != nil {
						return err
					}
				}
			}
			if err := tx.Model(&KeyPref{}).
				Where("token_id = ?", pref.TokenID).
				Update("model_scope", ModelScopeSelected).Error; err != nil {
				return err
			}
		}
		return nil
	})
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

// flattenPeakPrices retires peak/off-peak pricing (2026-10-28): for every
// (model, vendor) that has an off-peak row but no flat row in effect at now,
// it inserts a flat row carrying the current off-peak unit prices, effective
// now. Old peak/off-peak rows stay as history. Idempotent.
func flattenPeakPrices(db *gorm.DB, now time.Time) error {
	type pair struct{ Model, Vendor string }
	var pairs []pair
	if err := db.Model(&PriceBook{}).Where("tier = ?", TierOffpeak).
		Distinct("model", "vendor").Find(&pairs).Error; err != nil {
		return err
	}
	for _, p := range pairs {
		if _, err := priceAt(db, p.Model, p.Vendor, TierFlat, now); err == nil {
			continue
		}
		off, err := priceAt(db, p.Model, p.Vendor, TierOffpeak, now)
		if err != nil {
			continue // no off-peak row in effect yet; nothing to copy
		}
		var maxVersion int64
		if err := db.Model(&PriceBook{}).
			Where("model = ? AND vendor = ? AND tier = ?", p.Model, p.Vendor, TierFlat).
			Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
			return err
		}
		row := PriceBook{
			Model: p.Model, Vendor: p.Vendor, Tier: TierFlat, Version: maxVersion + 1,
			InputMicroPerMTok:      off.InputMicroPerMTok,
			OutputMicroPerMTok:     off.OutputMicroPerMTok,
			CacheReadMicroPerMTok:  off.CacheReadMicroPerMTok,
			CacheWriteMicroPerMTok: off.CacheWriteMicroPerMTok,
			EffectiveAt:            now,
		}
		if err := db.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}
