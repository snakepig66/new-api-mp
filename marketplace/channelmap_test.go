package marketplace

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newChannelMapTestDB opens an isolated database and creates only the channel
// model map table; these tests do not need the rest of the marketplace schema.
func newChannelMapTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	require.NoError(t, MigrateChannelModelMap(db))
	// AutoMigrate is documented as idempotent; run it a second time so the
	// migration test above doubles as a regression guard.
	require.NoError(t, MigrateChannelModelMap(db))
	return db
}

func channelMapCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&ChannelModelMap{}).Count(&count).Error)
	return count
}

// TestChannelModelMapMigrateAndSeed covers migration + seed idempotency: a
// second run inserts nothing, and operator edits are not overwritten by the
// seed.
func TestChannelModelMapMigrateAndSeed(t *testing.T) {
	db := newChannelMapTestDB(t)

	require.NoError(t, SeedChannelModelMap(db))
	seeded := channelMapCount(t, db)
	require.EqualValues(t, len(channelModelSeeds), seeded)

	// Seed is keyed on (vendor, model), so a second run must be a no-op.
	require.NoError(t, SeedChannelModelMap(db))
	assert.EqualValues(t, seeded, channelMapCount(t, db))

	// Spot-check the documented upstream names.
	cases := []struct {
		vendor, model, upstream string
	}{
		{"deepseek", "deepseek-flash", "deepseek-flash"},
		{"deepseek", "deepseek-v4-pro", "deepseek-v4-pro"},
		{"volcengine-ark", "deepseek-flash", "deepseek-v3-2-flash"},
		{"volcengine-ark", "deepseek-v4-pro", "deepseek-v3-2"},
		{"aliyun-bailian", "deepseek-flash", "deepseek-v3.2-flash"},
		{"aliyun-bailian", "deepseek-v4-pro", "deepseek-v3.2"},
		{"moonshot", "kimi-k2", "kimi-k2"},
		{"zhipu", "glm-4.6", "glm-4.6"},
		{"minimax", "minimax-m2", "minimax-m2"},
	}
	for _, c := range cases {
		got, ok := ChannelModelResolve(db, c.vendor, c.model)
		require.True(t, ok, "%s/%s must be seeded", c.vendor, c.model)
		assert.Equal(t, c.upstream, got, "%s/%s", c.vendor, c.model)
	}

	// An administrator's edit survives the next startup seed.
	require.NoError(t, UpsertChannelModel(db, "deepseek", "deepseek-flash", "deepseek-flash-custom"))
	require.NoError(t, SeedChannelModelMap(db))
	assert.EqualValues(t, seeded, channelMapCount(t, db))
	got, ok := ChannelModelResolve(db, "deepseek", "deepseek-flash")
	require.True(t, ok)
	assert.Equal(t, "deepseek-flash-custom", got)
}

// TestChannelModelUpsert covers create, update, trimming, validation and the
// per-vendor upstream uniqueness rule.
func TestChannelModelUpsert(t *testing.T) {
	db := newChannelMapTestDB(t)

	// Create. Leading/trailing spaces are trimmed before storage.
	require.NoError(t, UpsertChannelModel(db, " deepseek ", " deepseek-flash ", " upstream-a "))
	assert.EqualValues(t, 1, channelMapCount(t, db))
	got, ok := ChannelModelResolve(db, "deepseek", "deepseek-flash")
	require.True(t, ok)
	assert.Equal(t, "upstream-a", got)

	// Update the upstream of an existing (vendor, model).
	require.NoError(t, UpsertChannelModel(db, "deepseek", "deepseek-flash", "upstream-b"))
	assert.EqualValues(t, 1, channelMapCount(t, db), "update must not insert a new row")
	got, _ = ChannelModelResolve(db, "deepseek", "deepseek-flash")
	assert.Equal(t, "upstream-b", got)

	// Re-writing the same value is a no-op success.
	require.NoError(t, UpsertChannelModel(db, "deepseek", "deepseek-flash", "upstream-b"))
	assert.EqualValues(t, 1, channelMapCount(t, db))

	// A different platform model may not reuse the same upstream under the
	// same vendor: pricing/probing would become ambiguous.
	err := UpsertChannelModel(db, "deepseek", "another-model", "upstream-b")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrChannelModelUpstreamTaken))
	_, ok = ChannelModelResolve(db, "deepseek", "another-model")
	assert.False(t, ok, "the conflicting row must not be written")
	assert.EqualValues(t, 1, channelMapCount(t, db))

	// A different vendor may reuse the same upstream name.
	require.NoError(t, UpsertChannelModel(db, "other-vendor", "another-model", "upstream-b"))
	assert.EqualValues(t, 2, channelMapCount(t, db))

	// Empty / whitespace-only fields are rejected.
	for _, tc := range []struct{ vendor, model, upstream string }{
		{"", "m", "u"},
		{"v", "", "u"},
		{"v", "m", ""},
		{"  ", "m", "u"},
		{"v", "  ", "u"},
		{"v", "m", "  "},
	} {
		err := UpsertChannelModel(db, tc.vendor, tc.model, tc.upstream)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrChannelModelEmpty), "%q/%q/%q", tc.vendor, tc.model, tc.upstream)
	}
	assert.EqualValues(t, 2, channelMapCount(t, db), "validation failures must not write rows")
}

// TestChannelModelResolve covers hit and miss paths, including the vendor
// scoping.
func TestChannelModelResolve(t *testing.T) {
	db := newChannelMapTestDB(t)

	_, ok := ChannelModelResolve(db, "deepseek", "deepseek-flash")
	assert.False(t, ok, "empty table misses")
	_, ok = ChannelModelResolve(db, "missing", "missing")
	assert.False(t, ok)

	require.NoError(t, UpsertChannelModel(db, "deepseek", "deepseek-flash", "upstream-a"))

	upstream, ok := ChannelModelResolve(db, "deepseek", "deepseek-flash")
	require.True(t, ok)
	assert.Equal(t, "upstream-a", upstream)

	// Same model id under another vendor must not resolve.
	_, ok = ChannelModelResolve(db, "other", "deepseek-flash")
	assert.False(t, ok)

	// ChannelUpstreamModel is the same lookup.
	upstream, ok = ChannelUpstreamModel(db, "deepseek", "deepseek-flash")
	require.True(t, ok)
	assert.Equal(t, "upstream-a", upstream)
}

// TestChannelModelsFor covers deduplication and stable ordering.
func TestChannelModelsFor(t *testing.T) {
	db := newChannelMapTestDB(t)
	require.NoError(t, UpsertChannelModel(db, "deepseek", "model-c", "up-c"))
	require.NoError(t, UpsertChannelModel(db, "deepseek", "model-a", "up-a"))
	require.NoError(t, UpsertChannelModel(db, "deepseek", "model-b", "up-b"))
	// Another vendor's rows never leak into the list.
	require.NoError(t, UpsertChannelModel(db, "other", "model-a", "up-a"))

	got := ChannelModelsFor(db, "deepseek")
	assert.Equal(t, []string{"model-a", "model-b", "model-c"}, got)

	// The result is stable across calls.
	assert.Equal(t, got, ChannelModelsFor(db, " deepseek "))
	assert.Equal(t, got, ChannelModelsFor(db, "deepseek"))

	// No duplicates even if the query were to return them.
	seen := make(map[string]bool, len(got))
	for _, m := range got {
		assert.False(t, seen[m], "duplicate model %q", m)
		seen[m] = true
	}

	assert.Empty(t, ChannelModelsFor(db, "unknown"))
	assert.Equal(t, []string{"model-a"}, ChannelModelsFor(db, "other"))
}

// TestDeleteChannelModel covers removal and idempotent re-deletion.
func TestDeleteChannelModel(t *testing.T) {
	db := newChannelMapTestDB(t)
	require.NoError(t, UpsertChannelModel(db, "deepseek", "deepseek-flash", "upstream-a"))
	require.NoError(t, UpsertChannelModel(db, "other", "deepseek-flash", "upstream-a"))

	require.NoError(t, DeleteChannelModel(db, "deepseek", "deepseek-flash"))
	_, ok := ChannelModelResolve(db, "deepseek", "deepseek-flash")
	assert.False(t, ok)
	assert.EqualValues(t, 1, channelMapCount(t, db))

	// The same model id under another vendor is untouched.
	_, ok = ChannelModelResolve(db, "other", "deepseek-flash")
	assert.True(t, ok)

	// Deleting an absent row is a no-op, so admin DELETE is idempotent.
	require.NoError(t, DeleteChannelModel(db, "deepseek", "deepseek-flash"))
	assert.EqualValues(t, 1, channelMapCount(t, db))

	// Blank identifiers are rejected.
	err := DeleteChannelModel(db, " ", "deepseek-flash")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrChannelModelEmpty))
}
