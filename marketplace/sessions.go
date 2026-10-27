package marketplace

// sessions.go — in-process registry of active marketplace requests (P5-C).
//
// A request's BillingSession already lives in the request goroutine, reachable
// only through the gin context; nothing enumerates the freezes that are
// outstanding right now. The operator escape hatch for a wedged request needs
// exactly that view, so every request registers its current listing and freeze
// after PrepareBilling and unregisters when EndRelay closes it.
//
// Like SlotManager this state is process-local: a multi-node deployment needs
// the same move to shared storage. Until then force release only affects the
// node the operator happens to hit.

import (
	"cmp"
	"slices"
	"sync"
	"time"
)

// SessionEntry is one active request's freeze bookkeeping. billing is the
// live session, used to read the current freeze after a retry adjusts it and
// to refund it safely (idempotently) on force release. It is unexported so no
// API response can leak the session object.
type SessionEntry struct {
	RequestID   string    `json:"request_id"`
	ListingID   int64     `json:"listing_id"`
	BuyerID     int       `json:"buyer_id"`
	FrozenMicro int64     `json:"frozen_micro"`
	CreatedAt   time.Time `json:"created_at"`

	billing *BillingSession
}

// SessionStats is a point-in-time summary of the registry.
type SessionStats struct {
	Total       int           `json:"total"`
	FrozenMicro int64         `json:"frozen_micro"`
	ByListing   map[int64]int `json:"by_listing"`
}

// SessionRegistry stores the active requests of this process.
type SessionRegistry struct {
	mu      sync.Mutex
	entries map[string]*SessionEntry
}

// NewSessionRegistry builds an empty registry.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{entries: make(map[string]*SessionEntry)}
}

// DefaultSessionRegistry is the process-wide registry used by the relay hooks.
func DefaultSessionRegistry() *SessionRegistry {
	return defaultSessionRegistry
}

var defaultSessionRegistry = NewSessionRegistry()

// Register adds or replaces the entry for entry.RequestID. A retry hop updates
// the listing and the current freeze under the same request id, so the record
// keeps its original CreatedAt instead of restarting the clock.
func (r *SessionRegistry) Register(entry SessionEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	if existing, ok := r.entries[entry.RequestID]; ok && !existing.CreatedAt.IsZero() {
		entry.CreatedAt = existing.CreatedAt
	}
	stored := entry
	r.entries[entry.RequestID] = &stored
}

// Unregister removes the request's entry. Removing an unknown request is a
// no-op, so EndRelay can call it unconditionally.
func (r *SessionRegistry) Unregister(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, requestID)
}

// List returns the active entries of one listing (listingID <= 0 means every
// listing), oldest first. FrozenMicro is refreshed from the live BillingSession
// because a retry may have reserved or refunded the difference.
func (r *SessionRegistry) List(listingID int64) []SessionEntry {
	r.mu.Lock()
	entries := make([]*SessionEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		if listingID > 0 && entry.ListingID != listingID {
			continue
		}
		entries = append(entries, entry)
	}
	r.mu.Unlock()

	result := make([]SessionEntry, 0, len(entries))
	for _, entry := range entries {
		snapshot := *entry
		if entry.billing != nil {
			snapshot.FrozenMicro = int64(entry.billing.GetPreConsumedQuota())
		}
		result = append(result, snapshot)
	}
	slices.SortFunc(result, func(a, b SessionEntry) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.RequestID, b.RequestID)
	})
	return result
}

// Stats summarizes the registry: request count, total frozen 微元, and a
// request count per listing.
func (r *SessionRegistry) Stats() SessionStats {
	stats := SessionStats{ByListing: make(map[int64]int)}
	r.mu.Lock()
	entries := make([]*SessionEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		entries = append(entries, entry)
	}
	r.mu.Unlock()

	stats.Total = len(entries)
	for _, entry := range entries {
		frozen := entry.FrozenMicro
		if entry.billing != nil {
			frozen = int64(entry.billing.GetPreConsumedQuota())
		}
		stats.FrozenMicro += frozen
		stats.ByListing[entry.ListingID]++
	}
	return stats
}
