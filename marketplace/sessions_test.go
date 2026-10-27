package marketplace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionRegistryLifecycle covers the P5-C active-freeze registry: listing,
// filtering, live freeze refresh after a retry adjustment, CreatedAt
// preservation, and idempotent removal.
func TestSessionRegistryLifecycle(t *testing.T) {
	registry := NewSessionRegistry()
	created := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	billing := &BillingSession{buyerUserID: 7, preConsumed: 100}
	registry.Register(SessionEntry{
		RequestID:   "req-a",
		ListingID:   1,
		BuyerID:     7,
		FrozenMicro: 100,
		CreatedAt:   created,
		billing:     billing,
	})
	registry.Register(SessionEntry{
		RequestID:   "req-b",
		ListingID:   2,
		BuyerID:     8,
		FrozenMicro: 50,
		CreatedAt:   created.Add(time.Minute),
	})

	all := registry.List(0)
	require.Len(t, all, 2)
	assert.Equal(t, "req-a", all[0].RequestID, "oldest first")
	assert.Equal(t, int64(1), all[0].ListingID)
	assert.Equal(t, 7, all[0].BuyerID)

	stats := registry.Stats()
	assert.Equal(t, 2, stats.Total)
	assert.Equal(t, int64(150), stats.FrozenMicro)
	assert.Equal(t, map[int64]int{1: 1, 2: 1}, stats.ByListing)

	onlyListing1 := registry.List(1)
	require.Len(t, onlyListing1, 1)
	assert.Equal(t, "req-a", onlyListing1[0].RequestID)

	// A retry reserves more under the same request id: the snapshot must report
	// the live freeze, and re-registering under a new listing keeps CreatedAt.
	billing.preConsumed = 250
	assert.Equal(t, int64(250), registry.List(1)[0].FrozenMicro)
	assert.Equal(t, int64(300), registry.Stats().FrozenMicro)

	registry.Register(SessionEntry{
		RequestID: "req-a",
		ListingID: 3,
		BuyerID:   7,
		billing:   billing,
	})
	assert.Empty(t, registry.List(1), "the request moved to listing 3")
	moved := registry.List(3)
	require.Len(t, moved, 1)
	assert.Equal(t, int64(250), moved[0].FrozenMicro)
	assert.Equal(t, created, moved[0].CreatedAt, "a retry keeps the original registration time")

	registry.Unregister("req-a")
	registry.Unregister("req-a") // idempotent
	registry.Unregister("unknown")
	remaining := registry.List(0)
	require.Len(t, remaining, 1)
	assert.Equal(t, "req-b", remaining[0].RequestID)
	registry.Unregister("req-b")
	assert.Empty(t, registry.List(0))
	assert.Equal(t, 0, registry.Stats().Total)
}
