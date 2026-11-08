package marketplace

// affinity.go — P4-A 亲和: pin one buyer session to the listing (endpoint +
// key) that served it, so a multi-turn conversation keeps hitting the same
// upstream account and its prompt cache. A binding is only a preference: the
// selector still applies every filter to the pinned listing, and a listing
// whose concurrency slots are full may be replaced for one request without
// rewriting the recorded relation.
//
// Affinity never leaves one channel: in the marketplace model a listing id is
// exactly one endpoint + key pair, so the binding stores the listing id.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/QuantumNous/new-api/common"
)

// Affinity identifier sources, highest priority first. They are stored on the
// binding only as part of the HMAC input; the raw value never leaves the
// process.
const (
	AffinitySourceUserID         = "user_id"
	AffinitySourcePromptCacheKey = "prompt_cache_key"
	AffinitySourceSessionHeader  = "session_header"
	AffinitySourcePromptHash     = "prompt_hash"
)

const (
	// AffinityTTL is the sliding lifetime of a binding: every hit extends it.
	AffinityTTL = time.Hour
	// AffinityMaxTTL is the absolute upper bound of one binding, measured from
	// its first creation. A hit near the end shortens ExpiresAt to this limit
	// instead of extending past it.
	AffinityMaxTTL = 24 * time.Hour

	// affinityPromptMessageLimit is how many leading messages join the system
	// prompt in the prompt hash.
	affinityPromptMessageLimit = 3
	// affinityPromptMinTokens is the prompt length above which a request
	// without cache_control still gets a prompt-hash affinity. The estimate is
	// deliberately rough (about four bytes per token).
	affinityPromptMinTokens = 1024
)

// ExtractAffinityIdentifier picks the highest-priority session identifier the
// request carries (P4-A):
//
//  1. metadata.user_id (Anthropic)
//  2. prompt_cache_key (OpenAI)
//  3. X-Session-Id header
//  4. a hash of the system prompt + the first few messages, only when the
//     request marks cache_control or the prompt is at least 1024 tokens
//
// It returns the source name and the raw value; AffinityKey hashes them. A
// request with none of these signals reports ok=false.
func ExtractAffinityIdentifier(c *gin.Context, body []byte) (source string, value string, ok bool) {
	if v := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String()); v != "" {
		return AffinitySourceUserID, v, true
	}
	if v := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); v != "" {
		return AffinitySourcePromptCacheKey, v, true
	}
	if c != nil && c.Request != nil {
		if v := strings.TrimSpace(c.Request.Header.Get("X-Session-Id")); v != "" {
			return AffinitySourceSessionHeader, v, true
		}
	}
	system, messages := affinityPromptParts(body)
	signature := strings.Join(system, "\x00")
	if len(messages) > 0 {
		signature += "\x01" + strings.Join(messages, "\x00")
	}
	if signature == "" {
		return "", "", false
	}
	// cache_control marks the prompt as cache-relevant regardless of length;
	// without it only a long prompt is worth pinning (a short one rotates its
	// hash on nearly every turn and would spread traffic for no benefit).
	if !bytes.Contains(body, []byte(`"cache_control"`)) &&
		(len(signature)+3)/4 < affinityPromptMinTokens {
		return "", "", false
	}
	sum := sha256.Sum256([]byte(signature))
	return AffinitySourcePromptHash, hex.EncodeToString(sum[:]), true
}

// affinityPromptParts returns the system texts and the first few non-system
// messages (role + text) of the inbound body. It understands the chat
// `messages` array, the Anthropic top-level `system`, and the Responses
// `instructions` field.
func affinityPromptParts(body []byte) (system, messages []string) {
	if sys := gjson.GetBytes(body, "system"); sys.Exists() {
		if text := affinityContentText(sys); text != "" {
			system = append(system, text)
		}
	}
	if instructions := gjson.GetBytes(body, "instructions"); instructions.Exists() {
		if text := affinityContentText(instructions); text != "" {
			system = append(system, text)
		}
	}
	all := gjson.GetBytes(body, "messages")
	if !all.IsArray() {
		return system, messages
	}
	for _, message := range all.Array() {
		role := strings.TrimSpace(message.Get("role").String())
		if role == "system" || role == "developer" {
			if text := affinityContentText(message.Get("content")); text != "" {
				system = append(system, text)
			}
			continue
		}
		if len(messages) >= affinityPromptMessageLimit {
			continue
		}
		messages = append(messages, role+"\x00"+affinityContentText(message.Get("content")))
	}
	return system, messages
}

// affinityContentText flattens a message content value: a plain string, or an
// array of text blocks as used by the Anthropic and OpenAI content-part
// dialects.
func affinityContentText(value gjson.Result) string {
	if !value.Exists() {
		return ""
	}
	if value.IsArray() {
		var b strings.Builder
		for _, part := range value.Array() {
			if part.Type == gjson.String {
				b.WriteString(part.String())
				continue
			}
			if part.Get("text").Exists() {
				b.WriteString(part.Get("text").String())
			}
		}
		return b.String()
	}
	if value.Type == gjson.String {
		return value.String()
	}
	return value.Raw
}

// AffinityKey returns HMAC-SHA256(secret, buyer_id ‖ source ‖ value) as hex,
// isolating bindings per buyer. It returns "" when the deployment has no
// MP_KEY_SECRET or the buyer is unknown, which disables affinity.
func AffinityKey(buyerID int, source, value string) string {
	if len(anonSecret) == 0 || buyerID <= 0 {
		return ""
	}
	mac := hmac.New(sha256.New, anonSecret)
	fmt.Fprintf(mac, "%d|%s|%s", buyerID, strings.TrimSpace(source), strings.TrimSpace(value))
	return hex.EncodeToString(mac.Sum(nil))
}

// affinityBindingExpired reports whether a binding is past its sliding TTL or
// the 24-hour absolute bound.
func affinityBindingExpired(binding AffinityBinding, now time.Time) bool {
	if !binding.ExpiresAt.IsZero() && !binding.ExpiresAt.After(now) {
		return true
	}
	if !binding.CreatedAt.IsZero() && now.Sub(binding.CreatedAt) >= AffinityMaxTTL {
		return true
	}
	return false
}

// LookupAffinity returns the live binding for one buyer session on one
// endpoint, or nil when none exists. An expired row is deleted lazily, so no
// background sweep is required.
func LookupAffinity(db *gorm.DB, buyerID int, source, value, endpoint string) (*AffinityBinding, error) {
	key := AffinityKey(buyerID, source, value)
	if db == nil || key == "" {
		return nil, nil
	}
	var binding AffinityBinding
	err := db.Where("affinity_key = ? AND endpoint = ? AND buyer_id = ?", key, endpoint, int64(buyerID)).
		Take(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if affinityBindingExpired(binding, time.Now()) {
		if derr := db.Where("id = ?", binding.ID).Delete(&AffinityBinding{}).Error; derr != nil {
			common.SysError(common.LogText("marketplace: delete expired affinity binding %d: %s", binding.ID, derr.Error()))
		}
		return nil, nil
	}
	return &binding, nil
}

// RecordAffinity records that listingID served a buyer session and extends the
// binding's sliding TTL. A binding that already exists keeps its listing id:
// a request dispatched elsewhere (for example because the preferred listing's
// concurrency slots were full) must not rewrite an established relation. The
// 24-hour absolute bound is measured from the binding's first creation.
func RecordAffinity(db *gorm.DB, buyerID int, source, value, endpoint string, listingID int64) error {
	key := AffinityKey(buyerID, source, value)
	if db == nil || key == "" || listingID <= 0 {
		return nil
	}
	now := time.Now()
	binding := AffinityBinding{
		AffinityKey: key,
		BuyerID:     int64(buyerID),
		ListingID:   listingID,
		Endpoint:    endpoint,
		CreatedAt:   now,
		ExpiresAt:   now.Add(AffinityTTL),
		LastHitAt:   now,
	}
	var existing AffinityBinding
	err := db.Where("affinity_key = ? AND endpoint = ? AND buyer_id = ?", key, endpoint, int64(buyerID)).
		Take(&existing).Error
	switch {
	case err == nil && affinityBindingExpired(existing, now):
		// Past the 24-hour bound (or the TTL): the relation is over and a new
		// binding may pin a different listing.
		if derr := db.Where("id = ?", existing.ID).Delete(&AffinityBinding{}).Error; derr != nil {
			return derr
		}
		existing = AffinityBinding{}
	case err == nil:
		// Keep the first creation instant and the pinned listing; only the
		// sliding expiry moves.
		binding.CreatedAt = existing.CreatedAt
		binding.ListingID = existing.ListingID
		if limit := existing.CreatedAt.Add(AffinityMaxTTL); binding.ExpiresAt.After(limit) {
			binding.ExpiresAt = limit
		}
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return err
	}
	// The upsert is race-safe across the three dialects: the unique
	// (affinity_key, endpoint) index makes a concurrent insert converge on the
	// same row, and the assignment set never touches listing_id or created_at.
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "affinity_key"}, {Name: "endpoint"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_hit_at": binding.LastHitAt,
			"expires_at":  binding.ExpiresAt,
		}),
	}).Create(&binding).Error
}

// recordAffinity persists the request's affinity after a successful settle.
// It is a no-op for requests that carried no session identifier (or whose
// selection never opened a session).
func (s *RelaySession) recordAffinity(db *gorm.DB) {
	if s == nil || db == nil {
		return
	}
	s.mu.Lock()
	source, value := s.affinitySource, s.affinityValue
	listingID := s.listing.ID
	buyerID := s.buyerUserID
	endpoint := string(s.endpointKind)
	settledMicro := s.settledMicro
	s.mu.Unlock()
	if source == "" || value == "" || listingID == 0 {
		return
	}
	// A zero settled amount means the request produced nothing and was recorded
	// as failed (zero output / truncated stream); it must not pin the session.
	if settledMicro <= 0 {
		return
	}
	if err := RecordAffinity(db, buyerID, source, value, endpoint, listingID); err != nil {
		common.SysError(common.LogText("marketplace: record affinity binding for listing %d: %s", listingID, err.Error()))
	}
}
