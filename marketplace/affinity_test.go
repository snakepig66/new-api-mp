package marketplace

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setAffinityTestSecret installs a known HMAC secret and restores the previous
// one after the test, since anonSecret is a package global.
func setAffinityTestSecret(t *testing.T) {
	t.Helper()
	old := anonSecret
	SetAnonSecret("affinity-test-secret")
	t.Cleanup(func() { anonSecret = old })
}

func newAffinityContext(t *testing.T, body string, header map[string]string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(body)))
	for k, v := range header {
		c.Request.Header.Set(k, v)
	}
	return c
}

func TestAffinityKey(t *testing.T) {
	setAffinityTestSecret(t)

	base := AffinityKey(1, AffinitySourceUserID, "session-a")
	require.NotEmpty(t, base)
	assert.Equal(t, base, AffinityKey(1, AffinitySourceUserID, "session-a"),
		"the same buyer/source/value derives the same key")
	assert.NotEqual(t, base, AffinityKey(2, AffinitySourceUserID, "session-a"),
		"bindings are isolated per buyer")
	assert.NotEqual(t, base, AffinityKey(1, AffinitySourcePromptCacheKey, "session-a"),
		"the source is part of the HMAC input")
	assert.NotEqual(t, base, AffinityKey(1, AffinitySourceUserID, "session-b"))
	assert.Empty(t, AffinityKey(0, AffinitySourceUserID, "session-a"),
		"an unknown buyer cannot key a binding")

	old := anonSecret
	SetAnonSecret("")
	t.Cleanup(func() { anonSecret = old })
	assert.Empty(t, AffinityKey(1, AffinitySourceUserID, "session-a"),
		"without MP_KEY_SECRET affinity is disabled")
}

func TestExtractAffinityIdentifier(t *testing.T) {
	t.Run("metadata.user_id has the highest priority", func(t *testing.T) {
		body := `{"metadata":{"user_id":"user-1"},"prompt_cache_key":"key-1","messages":[{"role":"user","content":"hi"}]}`
		c := newAffinityContext(t, body, map[string]string{"X-Session-Id": "header-1"})
		source, value, ok := ExtractAffinityIdentifier(c, []byte(body))
		require.True(t, ok)
		assert.Equal(t, AffinitySourceUserID, source)
		assert.Equal(t, "user-1", value)
	})

	t.Run("prompt_cache_key precedes the session header", func(t *testing.T) {
		body := `{"prompt_cache_key":"cache-1"}`
		c := newAffinityContext(t, body, map[string]string{"X-Session-Id": "header-1"})
		source, value, ok := ExtractAffinityIdentifier(c, []byte(body))
		require.True(t, ok)
		assert.Equal(t, AffinitySourcePromptCacheKey, source)
		assert.Equal(t, "cache-1", value)
	})

	t.Run("session header is used when the body has no identifier", func(t *testing.T) {
		body := `{"messages":[{"role":"user","content":"hi"}]}`
		c := newAffinityContext(t, body, map[string]string{"X-Session-Id": "header-1"})
		source, value, ok := ExtractAffinityIdentifier(c, []byte(body))
		require.True(t, ok)
		assert.Equal(t, AffinitySourceSessionHeader, source)
		assert.Equal(t, "header-1", value)
	})

	t.Run("cache_control enables a short prompt hash", func(t *testing.T) {
		body := `{"system":"you are helpful","messages":[{"role":"user","content":[{"type":"text","text":"first","cache_control":{"type":"ephemeral"}}]}]}`
		c := newAffinityContext(t, body, nil)
		source, value, ok := ExtractAffinityIdentifier(c, []byte(body))
		require.True(t, ok)
		assert.Equal(t, AffinitySourcePromptHash, source)
		assert.NotEmpty(t, value)

		// The hash is deterministic and follows the conversation.
		_, again, ok := ExtractAffinityIdentifier(c, []byte(body))
		require.True(t, ok)
		assert.Equal(t, value, again)

		other := `{"system":"you are helpful","messages":[{"role":"user","content":[{"type":"text","text":"second","cache_control":{"type":"ephemeral"}}]}]}`
		_, otherValue, ok := ExtractAffinityIdentifier(c, []byte(other))
		require.True(t, ok)
		assert.NotEqual(t, value, otherValue, "a different leading message changes the hash")
	})

	t.Run("a long prompt is hashed without cache_control", func(t *testing.T) {
		long := strings.Repeat("a", 5000)
		body := `{"system":"` + long + `","messages":[{"role":"user","content":"hi"}]}`
		_, value, ok := ExtractAffinityIdentifier(nil, []byte(body))
		require.True(t, ok)
		assert.NotEmpty(t, value)
	})

	t.Run("a short prompt without cache_control gets no affinity", func(t *testing.T) {
		body := `{"system":"short","messages":[{"role":"user","content":"hi"}]}`
		_, _, ok := ExtractAffinityIdentifier(nil, []byte(body))
		assert.False(t, ok)
	})

	t.Run("an empty request has no identifier", func(t *testing.T) {
		_, _, ok := ExtractAffinityIdentifier(nil, nil)
		assert.False(t, ok)
	})
}

func TestRecordAndLookupAffinity(t *testing.T) {
	setAffinityTestSecret(t)
	db := newTestDB(t)
	migrateAll(t, db)

	const (
		buyerID  = 10011
		listingA = int64(101)
		listingB = int64(102)
	)
	endpoint := "openai_chat"

	require.NoError(t, RecordAffinity(db, buyerID, AffinitySourceSessionHeader, "sess-1", endpoint, listingA))
	binding, err := LookupAffinity(db, buyerID, AffinitySourceSessionHeader, "sess-1", endpoint)
	require.NoError(t, err)
	require.NotNil(t, binding)
	assert.Equal(t, listingA, binding.ListingID)
	assert.Equal(t, int64(buyerID), binding.BuyerID)
	assert.WithinDuration(t, time.Now().Add(AffinityTTL), binding.ExpiresAt, time.Minute)
	assert.WithinDuration(t, time.Now(), binding.LastHitAt, time.Minute)

	// A later request served by another listing (for example a concurrency
	// fallback) must not rewrite the established relation.
	require.NoError(t, RecordAffinity(db, buyerID, AffinitySourceSessionHeader, "sess-1", endpoint, listingB))
	binding, err = LookupAffinity(db, buyerID, AffinitySourceSessionHeader, "sess-1", endpoint)
	require.NoError(t, err)
	require.NotNil(t, binding)
	assert.Equal(t, listingA, binding.ListingID, "an existing binding keeps its listing")
	assert.WithinDuration(t, time.Now(), binding.LastHitAt, time.Minute, "the hit updates last_hit_at")

	// Bindings are isolated per buyer and per endpoint.
	other, err := LookupAffinity(db, buyerID+1, AffinitySourceSessionHeader, "sess-1", endpoint)
	require.NoError(t, err)
	assert.Nil(t, other, "another buyer never sees this binding")
	other, err = LookupAffinity(db, buyerID, AffinitySourceSessionHeader, "sess-1", "anthropic_messages")
	require.NoError(t, err)
	assert.Nil(t, other, "another endpoint never sees this binding")

	// An expired binding is ignored and deleted lazily.
	now := time.Now()
	require.NoError(t, db.Create(&AffinityBinding{
		AffinityKey: AffinityKey(buyerID, AffinitySourceSessionHeader, "sess-expired"),
		BuyerID:     buyerID, ListingID: listingA, Endpoint: endpoint,
		CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Minute), LastHitAt: now.Add(-2 * time.Hour),
	}).Error)
	expired, err := LookupAffinity(db, buyerID, AffinitySourceSessionHeader, "sess-expired", endpoint)
	require.NoError(t, err)
	assert.Nil(t, expired)
	var count int64
	require.NoError(t, db.Model(&AffinityBinding{}).
		Where("affinity_key = ?", AffinityKey(buyerID, AffinitySourceSessionHeader, "sess-expired")).
		Count(&count).Error)
	assert.Zero(t, count, "the expired row is deleted on lookup")
}

func TestAffinityAbsoluteTTL(t *testing.T) {
	setAffinityTestSecret(t)
	db := newTestDB(t)
	migrateAll(t, db)

	const (
		buyerID  = 10011
		listingA = int64(201)
		listingB = int64(202)
	)
	now := time.Now()
	createdAt := now.Add(-23*time.Hour - 50*time.Minute)

	// A hit near the 24-hour bound shortens the sliding TTL to the absolute
	// limit instead of extending past it.
	require.NoError(t, db.Create(&AffinityBinding{
		AffinityKey: AffinityKey(buyerID, AffinitySourceUserID, "long-session"),
		BuyerID:     buyerID, ListingID: listingA, Endpoint: "openai_chat",
		CreatedAt: createdAt, ExpiresAt: now.Add(time.Minute), LastHitAt: now.Add(-time.Hour),
	}).Error)
	require.NoError(t, RecordAffinity(db, buyerID, AffinitySourceUserID, "long-session", "openai_chat", listingB))

	var binding AffinityBinding
	require.NoError(t, db.Where("affinity_key = ?", AffinityKey(buyerID, AffinitySourceUserID, "long-session")).
		Take(&binding).Error)
	assert.Equal(t, listingA, binding.ListingID)
	assert.WithinDuration(t, createdAt.Add(AffinityMaxTTL), binding.ExpiresAt, time.Minute,
		"expires_at is capped at created_at+24h")

	// Past the absolute bound the row is stale even with a future expires_at.
	require.NoError(t, db.Create(&AffinityBinding{
		AffinityKey: AffinityKey(buyerID, AffinitySourceUserID, "too-old"),
		BuyerID:     buyerID, ListingID: listingA, Endpoint: "openai_chat",
		CreatedAt: now.Add(-AffinityMaxTTL - time.Minute), ExpiresAt: now.Add(time.Hour), LastHitAt: now,
	}).Error)
	stale, err := LookupAffinity(db, buyerID, AffinitySourceUserID, "too-old", "openai_chat")
	require.NoError(t, err)
	assert.Nil(t, stale, "the 24-hour bound invalidates a binding regardless of expires_at")

	// A request after the bound starts a fresh relation and may pin another
	// listing, because the old relation no longer exists.
	require.NoError(t, db.Create(&AffinityBinding{
		AffinityKey: AffinityKey(buyerID, AffinitySourceUserID, "renewed"),
		BuyerID:     buyerID, ListingID: listingA, Endpoint: "openai_chat",
		CreatedAt: now.Add(-AffinityMaxTTL - time.Minute), ExpiresAt: now.Add(time.Hour), LastHitAt: now,
	}).Error)
	require.NoError(t, RecordAffinity(db, buyerID, AffinitySourceUserID, "renewed", "openai_chat", listingB))
	renewed, err := LookupAffinity(db, buyerID, AffinitySourceUserID, "renewed", "openai_chat")
	require.NoError(t, err)
	require.NotNil(t, renewed)
	assert.Equal(t, listingB, renewed.ListingID)
	assert.WithinDuration(t, time.Now(), renewed.CreatedAt, time.Minute,
		"the replacement binding restarts the 24-hour clock")
}
