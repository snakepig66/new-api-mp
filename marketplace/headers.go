package marketplace

// headers.go — seller-supplied upstream request headers for custom channels.
//
// A header policy is never applied on submit. It is stored encrypted as a
// pending candidate, exercised with the platform's own minimal probes, and
// only a candidate that keeps every enabled model callable replaces
// channels.header_override. Failures keep the old policy, which makes the
// channel no worse than before the edit (fail-closed). Values never appear in
// an API response or an audit entry: the console reads masked values only.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	// ProbeTypeHeadersCheck marks a candidate-header verification run.
	ProbeTypeHeadersCheck = "headers_check"

	maxSellerHeaders          = 20
	maxSellerHeaderTotalBytes = 8 << 10
	maxSellerHeaderValueBytes = 1024
)

// ErrHeadersUnsupported marks a header submission for a channel the platform
// configures itself (official, plan, or catalogued third-party).
var ErrHeadersUnsupported = errors.New("marketplace: request headers are only available for seller-defined channels")

// ErrInvalidHeaders wraps every rejected header name or value. The message
// names the offending header but never echoes its value.
var ErrInvalidHeaders = errors.New("marketplace: invalid request header")

var headerNamePattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// exactHeaderDenylist names headers the relay or the HTTP stack owns. Letting
// a seller set any of them would let a listing impersonate the platform,
// rewrite framing, or leak the platform key.
var exactHeaderDenylist = map[string]struct{}{
	"host":                {},
	"authorization":       {},
	"x-api-key":           {},
	"api-key":             {},
	"cookie":              {},
	"set-cookie":          {},
	"content-length":      {},
	"content-type":        {},
	"content-encoding":    {},
	"accept-encoding":     {},
	"connection":          {},
	"transfer-encoding":   {},
	"upgrade":             {},
	"keep-alive":          {},
	"proxy-authorization": {},
	"proxy-connection":    {},
	"te":                  {},
	"trailer":             {},
	"expect":              {},
	"via":                 {},
	"forwarded":           {},
	"x-real-ip":           {},
	"x-goog-api-key":      {},
}

var deniedHeaderPrefixes = []string{
	"content-",
	"x-forwarded-",
	"anthropic-",
	"openai-",
	"proxy-",
}

// validateSellerHeaders normalizes and validates one header policy. It returns
// a copy with lower-case names; input order is irrelevant.
func validateSellerHeaders(raw map[string]string) (map[string]string, error) {
	if len(raw) > maxSellerHeaders {
		return nil, fmt.Errorf("%w: at most %d headers", ErrInvalidHeaders, maxSellerHeaders)
	}
	out := make(map[string]string, len(raw))
	total := 0
	for name, value := range raw {
		lower := strings.ToLower(strings.TrimSpace(name))
		if !headerNamePattern.MatchString(lower) {
			return nil, fmt.Errorf("%w: %q is not a valid header name", ErrInvalidHeaders, name)
		}
		if _, dup := out[lower]; dup {
			return nil, fmt.Errorf("%w: duplicate header %q", ErrInvalidHeaders, lower)
		}
		if headerNameDenied(lower) {
			return nil, fmt.Errorf("%w: %q is reserved", ErrInvalidHeaders, lower)
		}
		if err := validateHeaderValue(lower, value); err != nil {
			return nil, err
		}
		if len(lower)+len(value) > maxSellerHeaderTotalBytes {
			return nil, fmt.Errorf("%w: header policy is too large", ErrInvalidHeaders)
		}
		total += len(lower) + len(value)
		if total > maxSellerHeaderTotalBytes {
			return nil, fmt.Errorf("%w: header policy is too large", ErrInvalidHeaders)
		}
		out[lower] = value
	}
	return out, nil
}

func headerNameDenied(lower string) bool {
	if _, denied := exactHeaderDenylist[lower]; denied {
		return true
	}
	for _, prefix := range deniedHeaderPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	// Authentication-bearing custom headers are refused by shape: a seller can
	// set their own credential through the channel key, never through a header
	// the console cannot mask reliably.
	return strings.HasSuffix(lower, "-token")
}

func validateHeaderValue(name, value string) error {
	if len(value) > maxSellerHeaderValueBytes {
		return fmt.Errorf("%w: value for %q is too long", ErrInvalidHeaders, name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: value for %q is not valid UTF-8", ErrInvalidHeaders, name)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: value for %q contains a control character", ErrInvalidHeaders, name)
		}
	}
	if strings.ContainsAny(value, "{}") {
		return fmt.Errorf("%w: value for %q may not contain placeholders", ErrInvalidHeaders, name)
	}
	if strings.Contains(value, "*") {
		return fmt.Errorf("%w: value for %q may not contain a wildcard", ErrInvalidHeaders, name)
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "re:") || strings.Contains(lower, "regex:") {
		return fmt.Errorf("%w: value for %q may not contain a regular expression", ErrInvalidHeaders, name)
	}
	return nil
}

// maskHeaderValue keeps only the last four characters. Short values are fully
// masked so a tiny secret cannot be reconstructed from its suffix.
func maskHeaderValue(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 4 {
		return "****"
	}
	return "****" + string(runes[len(runes)-4:])
}

// sellerHeaderEntry is the only representation of a header value the console
// ever receives.
type sellerHeaderEntry struct {
	Name        string `json:"name"`
	ValueMasked string `json:"value_masked"`
}

func maskedHeaderEntries(headers map[string]string) []sellerHeaderEntry {
	out := make([]sellerHeaderEntry, 0, len(headers))
	for name, value := range headers {
		out = append(out, sellerHeaderEntry{Name: name, ValueMasked: maskHeaderValue(value)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func headerNames(headers map[string]string) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sellerPendingHeaders describes a candidate that is still being verified.
type sellerPendingHeaders struct {
	Version     int                 `json:"version"`
	SubmittedAt *time.Time          `json:"submitted_at,omitempty"`
	Headers     []sellerHeaderEntry `json:"headers"`
}

// sellerHeadersView is GET /seller/listings/:id/headers.
type sellerHeadersView struct {
	CustomChannel bool                  `json:"custom_channel"`
	Headers       []sellerHeaderEntry   `json:"headers"`
	Pending       *sellerPendingHeaders `json:"pending,omitempty"`
	CanEdit       bool                  `json:"can_edit"`
}

// loadAppliedHeaders reads the relay channel's current header_override. A
// malformed stored value is reported as empty for display purposes only; the
// relay's own parser is the authority on what it sends.
func loadAppliedHeaders(db *gorm.DB, channelID int64) (map[string]string, error) {
	out := map[string]string{}
	if db == nil || channelID <= 0 {
		return out, nil
	}
	var raw *string
	if err := db.Table("channels").Select("header_override").Where("id = ?", channelID).
		Scan(&raw).Error; err != nil {
		return nil, err
	}
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return out, nil
	}
	var parsed map[string]string
	if err := common.Unmarshal([]byte(*raw), &parsed); err != nil {
		return out, nil
	}
	return parsed, nil
}

// SellerHeadersViewFor builds the console view for one listing. Pending
// values are decrypted and immediately masked; they are never returned raw.
func SellerHeadersViewFor(db *gorm.DB, listing Listing) (sellerHeadersView, error) {
	view := sellerHeadersView{
		CustomChannel: isCustomChannelSlug(listing.Vendor),
		Headers:       []sellerHeaderEntry{},
		CanEdit:       viewEditable(listing.Status),
	}
	applied, err := loadAppliedHeaders(db, listing.ChannelID)
	if err != nil {
		return view, err
	}
	view.Headers = maskedHeaderEntries(applied)
	if listing.PendingHeadersVersion > 0 && len(listing.PendingHeadersCiphertext) > 0 {
		pending := &sellerPendingHeaders{
			Version:     listing.PendingHeadersVersion,
			SubmittedAt: listing.PendingHeadersAt,
			Headers:     []sellerHeaderEntry{},
		}
		if plain, derr := DecryptProtectedBlob(listing.PendingHeadersCiphertext); derr == nil {
			var candidate map[string]string
			if common.Unmarshal(plain, &candidate) == nil {
				pending.Headers = maskedHeaderEntries(candidate)
			}
		}
		view.Pending = pending
	}
	return view, nil
}

func viewEditable(status string) bool {
	switch status {
	case ListingLive, ListingCanary, ListingVerifying, ListingDraft:
		return true
	default:
		return false
	}
}

// SellerHeadersSubmitResult reports whether a candidate was accepted for
// verification.
type SellerHeadersSubmitResult struct {
	Pending   bool                `json:"pending"`
	Unchanged bool                `json:"unchanged"`
	Version   int                 `json:"version"`
	Headers   []sellerHeaderEntry `json:"headers"`
}

// SubmitListingHeaders validates a policy, stores it encrypted as the new
// pending version, and writes an audit entry with header names only. The
// caller starts verification (the API handler does it in the background).
func SubmitListingHeaders(db *gorm.DB, listingID, sellerID int64, headers map[string]string, now time.Time) (SellerHeadersSubmitResult, error) {
	var result SellerHeadersSubmitResult
	if db == nil {
		return result, errors.New("marketplace: nil database")
	}
	var listing Listing
	if err := db.Where("id = ?", listingID).Take(&listing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, ErrListingNotFound
		}
		return result, err
	}
	if sellerID > 0 && listing.SellerID != sellerID {
		return result, ErrListingNotFound
	}
	switch listing.Status {
	case ListingDeleted:
		return result, ErrListingNotFound
	case ListingBanned:
		return result, fmt.Errorf("%w: a banned listing cannot be edited", ErrInvalidTransition)
	}
	if !isCustomChannelSlug(listing.Vendor) {
		return result, ErrHeadersUnsupported
	}
	normalized, err := validateSellerHeaders(headers)
	if err != nil {
		return result, err
	}
	if len(normalized) == 0 {
		return result, fmt.Errorf("%w: an empty policy means clear the headers", ErrInvalidHeaders)
	}
	result.Headers = maskedHeaderEntries(normalized)

	current, err := loadAppliedHeaders(db, listing.ChannelID)
	if err != nil {
		return result, err
	}
	if maps.Equal(current, normalized) {
		// The candidate is already live; cancel any in-flight candidate so an
		// older version cannot apply after this no-op.
		if derr := discardPendingHeaders(db, listing.ID, 0); derr != nil {
			return result, derr
		}
		result.Unchanged = true
		return result, nil
	}

	blob, err := common.Marshal(normalized)
	if err != nil {
		return result, err
	}
	ciphertext, err := EncryptProtectedBlob(blob)
	if err != nil {
		return result, err
	}
	var version int
	err = db.Transaction(func(tx *gorm.DB) error {
		if uerr := tx.Model(&Listing{}).Where("id = ?", listing.ID).Updates(map[string]any{
			"pending_headers_ciphertext": ciphertext,
			"pending_headers_version":    gorm.Expr("pending_headers_version + 1"),
			"pending_headers_at":         now,
		}).Error; uerr != nil {
			return uerr
		}
		return tx.Model(&Listing{}).Select("pending_headers_version").
			Where("id = ?", listing.ID).Scan(&version).Error
	})
	if err != nil {
		return result, err
	}
	if version <= 0 {
		return result, errors.New("marketplace: could not stage the header policy")
	}
	writeListingAuditLogged(db, listing.ID, sellerID, ListingAuditHeadersSubmit, map[string]any{
		"headers": headerNames(normalized),
		"version": version,
	})
	result.Pending = true
	result.Version = version
	return result, nil
}

// ClearListingHeaders removes the live policy and cancels any pending
// candidate. Clearing is immediate: restoring the upstream default cannot
// leave the relay in a state it has never served.
func ClearListingHeaders(db *gorm.DB, listingID, sellerID int64, now time.Time) error {
	if db == nil {
		return errors.New("marketplace: nil database")
	}
	var listing Listing
	if err := db.Where("id = ?", listingID).Take(&listing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrListingNotFound
		}
		return err
	}
	if sellerID > 0 && listing.SellerID != sellerID {
		return ErrListingNotFound
	}
	switch listing.Status {
	case ListingDeleted:
		return ErrListingNotFound
	case ListingBanned:
		return fmt.Errorf("%w: a banned listing cannot be edited", ErrInvalidTransition)
	}
	if !isCustomChannelSlug(listing.Vendor) {
		return ErrHeadersUnsupported
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if uerr := tx.Model(&Listing{}).Where("id = ?", listing.ID).Updates(map[string]any{
			"pending_headers_ciphertext": nil,
			"pending_headers_version":    gorm.Expr("pending_headers_version + 1"),
			"pending_headers_at":         nil,
		}).Error; uerr != nil {
			return uerr
		}
		if uerr := tx.Table("channels").Where("id = ?", listing.ChannelID).
			Update("header_override", "").Error; uerr != nil {
			return uerr
		}
		return writeListingAuditTx(tx, listing.ID, sellerID, ListingAuditHeadersApply, map[string]any{
			"headers": []string{},
			"cleared": true,
			"at":      now,
		})
	})
	if err != nil {
		return err
	}
	refreshChannelCache(db, listing.ChannelID)
	return nil
}

// discardPendingHeaders clears a candidate. version <= 0 discards whatever is
// pending; a positive version only clears that exact candidate, so a newer
// submission is never lost to an old goroutine.
func discardPendingHeaders(db *gorm.DB, listingID int64, version int) error {
	query := db.Model(&Listing{}).Where("id = ?", listingID)
	if version > 0 {
		query = query.Where("pending_headers_version = ?", version)
	}
	return query.Updates(map[string]any{
		"pending_headers_ciphertext": nil,
		"pending_headers_version":    gorm.Expr("pending_headers_version + 1"),
		"pending_headers_at":         nil,
	}).Error
}

// verifyPendingHeaders is the fail-closed verification run for one candidate
// version. It is synchronous; callers hand it to a goroutine.
func verifyPendingHeaders(db *gorm.DB, listingID int64, version int) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(common.LogText("marketplace: header verification %d/%d panicked: %v", listingID, version, r))
		}
	}()
	if db == nil || listingID <= 0 || version <= 0 {
		return
	}
	var listing Listing
	if err := db.Where("id = ?", listingID).Take(&listing).Error; err != nil {
		return
	}
	if listing.PendingHeadersVersion != version || len(listing.PendingHeadersCiphertext) == 0 {
		return
	}
	switch listing.Status {
	case ListingDeleted, ListingBanned, ListingPaused:
		// The listing left service while the candidate was in flight. Keep the
		// old policy and forget the candidate.
		_ = discardPendingHeaders(db, listingID, version)
		return
	}

	plain, err := DecryptProtectedBlob(listing.PendingHeadersCiphertext)
	if err != nil {
		rejectPendingHeaders(db, listingID, version, "header policy cannot be decrypted", 0, "")
		return
	}
	var candidate map[string]string
	if err := common.Unmarshal(plain, &candidate); err != nil {
		rejectPendingHeaders(db, listingID, version, "header policy is malformed", 0, "")
		return
	}
	normalized, err := validateSellerHeaders(candidate)
	if err != nil || len(normalized) == 0 {
		rejectPendingHeaders(db, listingID, version, "header policy is not valid", 0, "")
		return
	}
	apiKey, err := DecryptListingKey(listing.KeyCiphertext)
	if err != nil {
		rejectPendingHeaders(db, listingID, version, "listing key cannot be decrypted", 0, "")
		return
	}
	vendor, ok := lookupVendorForDB(db, listing.Vendor)
	if !ok {
		rejectPendingHeaders(db, listingID, version, "channel is not available", 0, "")
		return
	}
	sellerVerifyMu.RLock()
	client, limiter := sellerVerifyClient, sellerVerifyLimiter
	sellerVerifyMu.RUnlock()
	if client == nil || limiter == nil {
		rejectPendingHeaders(db, listingID, version, "verification is not configured", 0, "")
		return
	}

	models := enabledListingModels(db, listing)
	if len(models) == 0 {
		rejectPendingHeaders(db, listingID, version, "channel has no enabled model to verify", 0, "")
		return
	}
	base := VendorOpenAIBase(vendor)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := limiter.Acquire(ctx); err != nil {
		rejectPendingHeaders(db, listingID, version, "verification is busy; try again later", 0, "")
		return
	}
	defer limiter.Release()

	status, message := modelsRespondWithHeaders(ctx, client, base, apiKey, normalized)
	if status != http.StatusOK {
		rejectPendingHeaders(db, listingID, version, "the channel rejected the new headers", status, message)
		return
	}
	for _, model := range models {
		upstreamModel := ListingUpstreamModel(db, listing.ID, listing.Vendor, model)
		out := probeModelCallOutcomeWithHeaders(ctx, client, base, upstreamModel, apiKey, normalized)
		if out.OK {
			continue
		}
		rejectPendingHeaders(db, listingID, version,
			"model "+model+" failed with the new headers", out.StatusCode, out.UpstreamMessage)
		return
	}
	applyPendingHeaders(db, listing, version, normalized)
}

// enabledListingModels returns the models a header candidate must keep
// callable. Listings created before per-model rows existed have no rows and
// fall back to the listing's model list; a listing whose rows are all disabled
// has nothing to verify and is rejected by the caller.
func enabledListingModels(db *gorm.DB, listing Listing) []string {
	var rows []ListingModel
	if err := db.Where("listing_id = ?", listing.ID).Order("model ASC").Find(&rows).Error; err != nil {
		return nil
	}
	if len(rows) == 0 {
		return append([]string{}, listing.Models...)
	}
	models := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Enabled {
			models = append(models, row.Model)
		}
	}
	return models
}

func modelsRespondWithHeaders(ctx context.Context, client *http.Client, base, apiKey string, headers map[string]string) (int, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/models", nil)
	if err != nil {
		return 0, "upstream address is invalid"
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "cannot reach the channel"
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, sanitizeUpstreamMessage(string(raw), apiKey)
	}
	return http.StatusOK, ""
}

// rejectPendingHeaders records a failed candidate, keeps the live policy and
// notifies the seller. Values are never logged or stored; the sanitized
// upstream message is the only evidence.
func rejectPendingHeaders(db *gorm.DB, listingID int64, version int, reason string, status int, upstreamMessage string) {
	res := db.Model(&Listing{}).Where("id = ? AND pending_headers_version = ?", listingID, version).
		Updates(map[string]any{
			"pending_headers_ciphertext": nil,
			"pending_headers_version":    version + 1,
			"pending_headers_at":         nil,
		})
	if res.Error != nil {
		common.SysError(common.LogText("marketplace: discard rejected header policy %d: %s", listingID, res.Error.Error()))
		return
	}
	if res.RowsAffected == 0 {
		return // a newer submission superseded this one
	}
	_ = db.Create(&Probe{
		ListingID: listingID,
		Type:      ProbeTypeHeadersCheck,
		OK:        false,
		Detail: mustMarshal(map[string]any{
			"ok":               false,
			"reason":           reason,
			"status_code":      status,
			"upstream_message": upstreamMessage,
			"trigger":          "headers",
		}),
	}).Error
	writeListingAuditLogged(db, listingID, 0, ListingAuditHeadersReject, map[string]any{
		"reason":      reason,
		"status_code": status,
		"version":     version,
	})
	notifySeller(db, listingID, NotificationKindListingDegraded,
		"请求头未生效",
		reason+"；原配置保持不变。",
		"/console/listings?edit="+fmt.Sprint(listingID))
	common.SysLog(common.LogText("marketplace: listing %d header policy rejected: %s (status=%d)",
		listingID, reason, status))
}

// applyPendingHeaders swaps the verified policy into the relay channel. The
// conditional update makes a late goroutine a safe no-op after a newer
// submission or a clear.
func applyPendingHeaders(db *gorm.DB, listing Listing, version int, headers map[string]string) {
	blob, err := common.Marshal(headers)
	if err != nil {
		rejectPendingHeaders(db, listing.ID, version, "header policy cannot be encoded", 0, "")
		return
	}
	applied := false
	err = db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&Listing{}).Where("id = ? AND pending_headers_version = ?", listing.ID, version).
			Updates(map[string]any{
				"pending_headers_ciphertext": nil,
				"pending_headers_version":    version + 1,
				"pending_headers_at":         nil,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		if uerr := tx.Table("channels").Where("id = ?", listing.ChannelID).
			Update("header_override", string(blob)).Error; uerr != nil {
			return uerr
		}
		if uerr := writeListingAuditTx(tx, listing.ID, 0, ListingAuditHeadersApply, map[string]any{
			"headers": headerNames(headers),
			"version": version,
		}); uerr != nil {
			return uerr
		}
		if uerr := tx.Create(&Probe{
			ListingID: listing.ID,
			Type:      ProbeTypeHeadersCheck,
			OK:        true,
			Detail: mustMarshal(map[string]any{
				"ok":      true,
				"headers": headerNames(headers),
				"trigger": "headers",
			}),
		}).Error; uerr != nil {
			return uerr
		}
		applied = true
		return nil
	})
	if err != nil {
		common.SysError(common.LogText("marketplace: apply header policy %d: %s", listing.ID, err.Error()))
		return
	}
	if !applied {
		return
	}
	refreshChannelCache(db, listing.ChannelID)
	common.SysLog(common.LogText("marketplace: listing %d header policy applied (%d headers)",
		listing.ID, len(headers)))
}

func mustMarshal(value any) string {
	out, err := common.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(out)
}

// refreshChannelCache publishes the new header_override to the relay without
// waiting for the periodic cache reload. When the in-memory cache is disabled
// the relay reads the row on its next query anyway.
func refreshChannelCache(db *gorm.DB, channelID int64) {
	if db == nil || channelID <= 0 {
		return
	}
	var channel model.Channel
	if err := db.Where("id = ?", channelID).Take(&channel).Error; err != nil {
		return
	}
	model.CacheUpdateChannel(&channel)
}
