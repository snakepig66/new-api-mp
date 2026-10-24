package marketplace

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// Listing write-path errors (§3.5). No error message from this file ever
// contains an API key, a key ciphertext or a full key fingerprint (§2.3);
// the fingerprint's first 8 hex chars may identify a listing's key.
var (
	ErrDuplicateKey         = errors.New("marketplace: this api key is already listed")
	ErrInvalidTransition    = errors.New("marketplace: invalid listing status transition")
	ErrMultiplierTooSoon    = errors.New("marketplace: multiplier was changed less than 10 minutes ago")
	ErrListingNotFound      = errors.New("marketplace: listing not found")
	ErrVendorNotWhitelisted = errors.New("marketplace: vendor is not whitelisted")
)

const (
	// defaultListingConcurrency is the §3.5 default concurrent-slot count
	// applied when the seller does not set one.
	defaultListingConcurrency = 5
	// multiplierMinInterval is the §3.5 cooldown between two multiplier
	// changes; a change only affects new requests and never re-verifies.
	multiplierMinInterval = 10 * time.Minute
)

// CreateListingInput carries everything needed to open one listing (§3.5).
// APIKey appears here only: it is encrypted and fingerprinted, and the
// plaintext is never stored, logged or returned.
type CreateListingInput struct {
	SellerID        int64
	VendorID        string
	APIKey          string   // plaintext; input only
	Models          []string // must be a subset of the vendor's Models
	MultiplierMilli int64    // 1..1000 (§3.2)
	Concurrency     int      // <= 0 → defaultListingConcurrency
	RPM             int      // 0 = unlimited
	DailyCapMicro   int64    // 0 = unlimited
	Hours           string   // JSON windows; empty = all day
}

// validate checks every field that does not need the database and returns
// the whitelisted vendor. Errors name the offending field but never echo the
// API key.
func (in CreateListingInput) validate() (Vendor, error) {
	vendor, ok := VendorByID(in.VendorID)
	if !ok {
		return Vendor{}, fmt.Errorf("%w: %q", ErrVendorNotWhitelisted, in.VendorID)
	}
	if in.SellerID <= 0 {
		return Vendor{}, errors.New("marketplace: seller id is required")
	}
	if _, err := normalizeListingKey(in.APIKey); err != nil {
		// Empty after trimming, or whitespace/control characters inside the
		// key. Prefixing/suffixing spaces must not produce a second listing
		// for the same upstream credential (M5); the message never echoes it.
		return Vendor{}, err
	}
	if len(in.Models) == 0 {
		return Vendor{}, errors.New("marketplace: at least one model is required")
	}
	for _, m := range in.Models {
		if !slices.Contains(vendor.Models, m) {
			return Vendor{}, fmt.Errorf("marketplace: model %q is not served by vendor %q", m, vendor.ID)
		}
	}
	if in.MultiplierMilli < 1 || in.MultiplierMilli > 1000 {
		return Vendor{}, fmt.Errorf("marketplace: multiplier must be 1..1000 milli, got %d", in.MultiplierMilli)
	}
	if in.RPM < 0 || in.DailyCapMicro < 0 {
		return Vendor{}, errors.New("marketplace: rpm and daily cap must be >= 0")
	}
	if err := ValidateHours(in.Hours); err != nil {
		return Vendor{}, err
	}
	return vendor, nil
}

// dedupModels returns the input models without duplicates, order preserved.
func dedupModels(models []string) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// CreateListing opens one listing in draft status (§3.5): it validates the
// request, encrypts the key, computes its fingerprint, rejects a key already
// listed anywhere on the platform, creates the disabled upstream channel row
// and finally the listing itself, all in one transaction. The channel's Key
// column holds only the "mp-managed:<fp8>" placeholder — the real key lives
// solely in Listing.KeyCiphertext.
func CreateListing(db *gorm.DB, in CreateListingInput) (Listing, error) {
	vendor, err := in.validate()
	if err != nil {
		return Listing{}, err
	}
	ciphertext, err := EncryptListingKey(in.APIKey)
	if err != nil {
		return Listing{}, err
	}
	fingerprint, err := ListingKeyFingerprint(in.APIKey)
	if err != nil {
		return Listing{}, err
	}
	models := dedupModels(in.Models)

	var listing Listing
	err = db.Transaction(func(tx *gorm.DB) error {
		var existing int64
		if err := tx.Model(&Listing{}).
			Where("key_fingerprint = ?", fingerprint).
			Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			// §2.3: identify the key by the fingerprint prefix only.
			return fmt.Errorf("%w (fp %s)", ErrDuplicateKey, fingerprint[:8])
		}

		// The v1 whitelist holds only DeepSeek, so the upstream channel type
		// is fixed; a second vendor adds its own type mapping here.
		baseURL := vendor.BaseURL
		channel := model.Channel{
			Type:        constant.ChannelTypeDeepSeek,
			Key:         "mp-managed:" + fingerprint[:8],
			Name:        fmt.Sprintf("mp:%s:%d", vendor.ID, in.SellerID),
			Status:      common.ChannelStatusManuallyDisabled,
			BaseURL:     &baseURL,
			Models:      strings.Join(models, ","),
			Group:       "default",
			CreatedTime: time.Now().Unix(),
		}
		// Multi-protocol vendors relay the buyer's raw body (native OpenAI +
		// Anthropic) instead of the adaptor's conversion (§3.10).
		if vendor.PassthroughBody {
			setting := `{"pass_through_body_enabled":true}`
			channel.Setting = &setting
		}
		if err := tx.Create(&channel).Error; err != nil {
			return fmt.Errorf("marketplace: create upstream channel: %w", err)
		}

		concurrency := in.Concurrency
		if concurrency <= 0 {
			concurrency = defaultListingConcurrency
		}
		listing = Listing{
			SellerID:         in.SellerID,
			ChannelID:        int64(channel.Id),
			Vendor:           vendor.ID,
			Models:           models,
			MultiplierMilli:  in.MultiplierMilli,
			ConcurrencyLimit: concurrency,
			RPMLimit:         in.RPM,
			DailyCapMicro:    in.DailyCapMicro,
			Hours:            strings.TrimSpace(in.Hours),
			Status:           ListingDraft,
			KeyFingerprint:   fingerprint,
			KeyCiphertext:    ciphertext,
			Version:          1,
		}
		if err := tx.Create(&listing).Error; err != nil {
			// The unique index on key_fingerprint is the concurrency
			// backstop for the count check above.
			if isDuplicateKeyError(err) {
				return fmt.Errorf("%w (fp %s)", ErrDuplicateKey, fingerprint[:8])
			}
			return err
		}
		return nil
	})
	if err != nil {
		return Listing{}, err
	}
	return listing, nil
}

// isDuplicateKeyError reports whether err is a unique-constraint violation on
// any of the three supported dialects (SQLite / MySQL / PostgreSQL). It is
// the fallback for handles without GORM's TranslateError, which would
// otherwise surface as gorm.ErrDuplicatedKey.
func isDuplicateKeyError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || // sqlite
		strings.Contains(msg, "duplicate entry") || // mysql 1062
		strings.Contains(msg, "duplicate key value") // postgres 23505
}

// transitionListingFrom moves one listing from any of the given statuses to
// `to`. It locks the row inside a transaction and relies on the conditional
// UPDATE's affected-row count: anything but exactly one row means the listing
// is not in an allowed from-state (ErrInvalidTransition). A missing listing
// yields ErrListingNotFound.
func transitionListingFrom(db *gorm.DB, id int64, froms []string, to string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var row Listing
		if err := lockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrListingNotFound
			}
			return err
		}
		res := tx.Model(&Listing{}).
			Where("id = ? AND status IN ?", id, froms).
			Update("status", to)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("%w: %s -> %s (current %s)", ErrInvalidTransition, strings.Join(froms, "|"), to, row.Status)
		}
		return nil
	})
}

// TransitionListing moves one listing from status `from` to status `to`; the
// named wrappers below encode the legal edges of the §3.5 state machine
// (draft → verifying → canary → live ⇄ paused → banned).
func TransitionListing(db *gorm.DB, id int64, from, to string) error {
	return transitionListingFrom(db, id, []string{from}, to)
}

// StartVerification moves draft → verifying (verification requested).
func StartVerification(db *gorm.DB, id int64) error {
	return TransitionListing(db, id, ListingDraft, ListingVerifying)
}

// PassVerification moves verifying → canary (key verified, 10% gray traffic).
func PassVerification(db *gorm.DB, id int64) error {
	return TransitionListing(db, id, ListingVerifying, ListingCanary)
}

// GoLive moves canary → live (§3.5: ≥98% success over 24h, decided by jobs).
func GoLive(db *gorm.DB, id int64) error {
	return TransitionListing(db, id, ListingCanary, ListingLive)
}

// PauseListing moves live → paused.
func PauseListing(db *gorm.DB, id int64) error {
	return TransitionListing(db, id, ListingLive, ListingPaused)
}

// ResumeListing moves paused → live.
func ResumeListing(db *gorm.DB, id int64) error {
	return TransitionListing(db, id, ListingPaused, ListingLive)
}

// BanListing moves live/paused/canary/verifying → banned. Banned is terminal.
func BanListing(db *gorm.DB, id int64) error {
	return transitionListingFrom(db, id,
		[]string{ListingLive, ListingPaused, ListingCanary, ListingVerifying}, ListingBanned)
}

// UpdateMultiplier changes the listing's 倍率 (§3.5): it never re-verifies
// the key, two changes must be at least 10 minutes apart (measured on
// MultiplierUpdatedAt), and the new value affects only new requests. The
// version field is the optimistic-lock backstop behind the row lock.
func UpdateMultiplier(db *gorm.DB, id int64, milli int64, now time.Time) error {
	if milli < 1 || milli > 1000 {
		return fmt.Errorf("marketplace: multiplier must be 1..1000 milli, got %d", milli)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var row Listing
		if err := lockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrListingNotFound
			}
			return err
		}
		if row.MultiplierUpdatedAt != nil && now.Sub(*row.MultiplierUpdatedAt) < multiplierMinInterval {
			return ErrMultiplierTooSoon
		}
		res := tx.Model(&Listing{}).
			Where("id = ? AND version = ?", id, row.Version).
			Updates(map[string]any{
				"multiplier_milli":      milli,
				"multiplier_updated_at": now,
				"version":               row.Version + 1,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("marketplace: listing %d was modified concurrently", id)
		}
		return nil
	})
}

// ReplaceKey swaps in a new API key (§3.5): the new key is encrypted and
// fingerprinted, must not be listed anywhere else on the platform
// (ErrDuplicateKey), and the listing drops back to verifying — a key change
// always re-triggers verification. Banned listings are terminal and reject
// key replacement with ErrInvalidTransition.
func ReplaceKey(db *gorm.DB, id int64, newAPIKey string) error {
	if _, err := normalizeListingKey(newAPIKey); err != nil {
		// Same normalization as CreateListing: a padded key maps onto the
		// existing fingerprint; interior whitespace/control characters are
		// rejected before anything is encrypted.
		return err
	}
	ciphertext, err := EncryptListingKey(newAPIKey)
	if err != nil {
		return err
	}
	fingerprint, err := ListingKeyFingerprint(newAPIKey)
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var row Listing
		if err := lockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrListingNotFound
			}
			return err
		}
		if row.Status == ListingBanned {
			return fmt.Errorf("%w: banned -> verifying", ErrInvalidTransition)
		}
		var clash int64
		if err := tx.Model(&Listing{}).
			Where("key_fingerprint = ? AND id <> ?", fingerprint, id).
			Count(&clash).Error; err != nil {
			return err
		}
		if clash > 0 {
			return fmt.Errorf("%w (fp %s)", ErrDuplicateKey, fingerprint[:8])
		}
		res := tx.Model(&Listing{}).
			Where("id = ? AND version = ?", id, row.Version).
			Updates(map[string]any{
				"key_ciphertext":  ciphertext,
				"key_fingerprint": fingerprint,
				"status":          ListingVerifying,
				"version":         row.Version + 1,
			})
		if res.Error != nil {
			if isDuplicateKeyError(res.Error) {
				return fmt.Errorf("%w (fp %s)", ErrDuplicateKey, fingerprint[:8])
			}
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("marketplace: listing %d was modified concurrently", id)
		}
		return nil
	})
}

// hourWindow is one 可接单时段 entry: {"start":"09:00","end":"18:00"}.
type hourWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// ValidateHours checks the listing's 可接单时段 (§3.5): an empty string means
// all day; otherwise it must be a JSON array of {"start","end"} windows in
// zero-padded 24h HH:MM form with start strictly before end.
func ValidateHours(hours string) error {
	if strings.TrimSpace(hours) == "" {
		return nil
	}
	var windows []hourWindow
	if err := common.UnmarshalJsonStr(hours, &windows); err != nil {
		return fmt.Errorf("marketplace: hours must be a JSON array of time windows: %v", err)
	}
	if len(windows) == 0 {
		return errors.New("marketplace: hours must contain at least one window or be empty")
	}
	for _, w := range windows {
		start, err := parseHourMinute(w.Start)
		if err != nil {
			return fmt.Errorf("marketplace: hours start %q is not HH:MM 24h", w.Start)
		}
		end, err := parseHourMinute(w.End)
		if err != nil {
			return fmt.Errorf("marketplace: hours end %q is not HH:MM 24h", w.End)
		}
		if start >= end {
			return fmt.Errorf("marketplace: hours window %q-%q must have start before end", w.Start, w.End)
		}
	}
	return nil
}

// parseHourMinute parses a strict zero-padded 24h "HH:MM" into minutes since
// midnight. time.Parse("15:04", …) alone is too lenient — it accepts "9:00" —
// so the shape is checked character by character.
func parseHourMinute(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, errors.New("not HH:MM")
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return 0, errors.New("not HH:MM")
		}
	}
	hour := int(s[0]-'0')*10 + int(s[1]-'0')
	minute := int(s[3]-'0')*10 + int(s[4]-'0')
	if hour > 23 || minute > 59 {
		return 0, errors.New("not 24h")
	}
	return hour*60 + minute, nil
}

// GetListing returns one listing by id, or ErrListingNotFound.
func GetListing(db *gorm.DB, id int64) (Listing, error) {
	var row Listing
	if err := db.Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Listing{}, ErrListingNotFound
		}
		return Listing{}, err
	}
	return row, nil
}

// GetListingForSeller returns one listing owned by the given seller; any
// other owner's listing is indistinguishable from a missing one
// (ErrListingNotFound), so sellers cannot probe foreign listing ids.
func GetListingForSeller(db *gorm.DB, id, sellerID int64) (Listing, error) {
	var row Listing
	if err := db.Where("id = ? AND seller_id = ?", id, sellerID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Listing{}, ErrListingNotFound
		}
		return Listing{}, err
	}
	return row, nil
}

// ListListingsBySeller returns all of one seller's listings, oldest first.
func ListListingsBySeller(db *gorm.DB, sellerID int64) ([]Listing, error) {
	var rows []Listing
	if err := db.Where("seller_id = ?", sellerID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
