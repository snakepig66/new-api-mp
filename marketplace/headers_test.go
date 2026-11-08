package marketplace

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestValidateSellerHeaders(t *testing.T) {
	valid, err := validateSellerHeaders(map[string]string{
		"X-Route":  "blue-route-1234",
		"X-Tenant": "acme",
	})
	require.NoError(t, err)
	assert.Equal(t, "blue-route-1234", valid["x-route"])
	assert.Equal(t, "acme", valid["x-tenant"])

	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"authorization", map[string]string{"Authorization": "Bearer x"}},
		{"content type", map[string]string{"Content-Type": "application/json"}},
		{"forwarded", map[string]string{"X-Forwarded-For": "1.2.3.4"}},
		{"anthropic", map[string]string{"anthropic-version": "2023-06-01"}},
		{"openai beta", map[string]string{"openai-beta": "assistants=v2"}},
		{"token suffix", map[string]string{"X-Session-Token": "abc"}},
		{"placeholder", map[string]string{"X-Route": "{api_key}"}},
		{"wildcard", map[string]string{"X-Route": "prod-*"}},
		{"regex", map[string]string{"X-Route": "regex:prod"}},
		{"newline", map[string]string{"X-Route": "a\r\nX-Injected: b"}},
		{"control", map[string]string{"X-Route": "a\x01b"}},
		{"bad name", map[string]string{"x route": "secret-value"}},
		{"long value", map[string]string{"x-route": strings.Repeat("a", maxSellerHeaderValueBytes+1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateSellerHeaders(tc.headers)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidHeaders)
			for _, value := range tc.headers {
				assert.NotContains(t, err.Error(), value, "the error never echoes a header value")
			}
		})
	}
}

func TestMaskHeaderValue(t *testing.T) {
	assert.Equal(t, "", maskHeaderValue(""))
	assert.Equal(t, "****", maskHeaderValue("abc"))
	assert.Equal(t, "****cdef", maskHeaderValue("abcdef"))
}

// newCustomHeaderListing provisions a seller-defined channel and makes it live
// so a header candidate has a real listing to verify against.
func newCustomHeaderListing(t *testing.T, key string) (*gorm.DB, Listing) {
	t.Helper()
	db := newListingTestDB(t)
	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		CustomBaseURL:   "https://api.example.com",
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	require.True(t, isCustomChannelSlug(listing.Vendor))
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", listing.ID).
		Update("status", ListingLive).Error)
	return db, listing
}

// headerProbeHandler rejects requests whose x-route header is not "blue" and
// otherwise answers the same shape the platform's probes expect.
func headerProbeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Route") != "blue-route-1234" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown route"}}`))
			return
		}
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-flash"}]}`))
		case "/chat/completions":
			_, _ = w.Write([]byte(`{"model":"deepseek-flash","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func useHeaderProbeServer(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(headerProbeHandler())
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	restore := setSellerVerifyClientForTest(&http.Client{Transport: rewriteTransport{target: target}})
	t.Cleanup(restore)
}

func TestSubmitHeadersStagesEncryptedCandidate(t *testing.T) {
	db, listing := newCustomHeaderListing(t, "sk-header-stage-1234")
	require.NoError(t, db.Table("channels").Where("id = ?", listing.ChannelID).
		Update("header_override", `{"x-old":"keep"}`).Error)
	view, err := SellerHeadersViewFor(db, listing)
	require.NoError(t, err)
	assert.True(t, view.CustomChannel)
	assert.Len(t, view.Headers, 1)

	result, err := SubmitListingHeaders(db, listing.ID, listing.SellerID, map[string]string{
		"X-Route": "blue-route-1234",
	}, time.Now())
	require.NoError(t, err)
	assert.True(t, result.Pending)
	assert.Positive(t, result.Version)
	require.Len(t, result.Headers, 1)
	assert.Equal(t, "****1234", result.Headers[0].ValueMasked)
	assert.NotContains(t, result.Headers[0].ValueMasked, "blue")

	var stored Listing
	require.NoError(t, db.Where("id = ?", listing.ID).Take(&stored).Error)
	assert.NotEmpty(t, stored.PendingHeadersCiphertext)
	assert.NotContains(t, string(stored.PendingHeadersCiphertext), "blue-route", "the candidate is stored encrypted")
	assert.Equal(t, result.Version, stored.PendingHeadersVersion)

	view, err = SellerHeadersViewFor(db, stored)
	require.NoError(t, err)
	require.NotNil(t, view.Pending)
	assert.Equal(t, "****1234", view.Pending.Headers[0].ValueMasked)
}

func TestVerifyHeadersAppliesOnlyAfterEveryModelPasses(t *testing.T) {
	db, listing := newCustomHeaderListing(t, "sk-header-apply-1234")
	useHeaderProbeServer(t)

	result, err := SubmitListingHeaders(db, listing.ID, listing.SellerID, map[string]string{
		"X-Route": "blue-route-1234",
	}, time.Now())
	require.NoError(t, err)
	verifyPendingHeaders(db, listing.ID, result.Version)

	var raw *string
	require.NoError(t, db.Table("channels").Select("header_override").
		Where("id = ?", listing.ChannelID).Scan(&raw).Error)
	require.NotNil(t, raw)
	assert.JSONEq(t, `{"x-route":"blue-route-1234"}`, *raw)

	var stored Listing
	require.NoError(t, db.Where("id = ?", listing.ID).Take(&stored).Error)
	assert.Empty(t, stored.PendingHeadersCiphertext)
	assert.Greater(t, stored.PendingHeadersVersion, result.Version)

	var applied int64
	require.NoError(t, db.Model(&ListingAudit{}).
		Where("listing_id = ? AND action = ?", listing.ID, ListingAuditHeadersApply).
		Count(&applied).Error)
	assert.EqualValues(t, 1, applied)

	// Re-submitting the live policy is a no-op and cancels stale candidates.
	again, err := SubmitListingHeaders(db, listing.ID, listing.SellerID, map[string]string{
		"x-route": "blue-route-1234",
	}, time.Now())
	require.NoError(t, err)
	assert.True(t, again.Unchanged)
	assert.False(t, again.Pending)
}

func TestVerifyHeadersKeepsOldPolicyOnFailure(t *testing.T) {
	db, listing := newCustomHeaderListing(t, "sk-header-fail-1234")
	useHeaderProbeServer(t)
	require.NoError(t, db.Table("channels").Where("id = ?", listing.ChannelID).
		Update("header_override", `{"x-old":"keep"}`).Error)

	result, err := SubmitListingHeaders(db, listing.ID, listing.SellerID, map[string]string{
		"X-Route": "wrong-route",
	}, time.Now())
	require.NoError(t, err)
	verifyPendingHeaders(db, listing.ID, result.Version)

	var raw *string
	require.NoError(t, db.Table("channels").Select("header_override").
		Where("id = ?", listing.ChannelID).Scan(&raw).Error)
	require.NotNil(t, raw)
	assert.JSONEq(t, `{"x-old":"keep"}`, *raw, "a failed candidate never replaces the live policy")

	var stored Listing
	require.NoError(t, db.Where("id = ?", listing.ID).Take(&stored).Error)
	assert.Empty(t, stored.PendingHeadersCiphertext, "a failed candidate is discarded")
	assert.Greater(t, stored.PendingHeadersVersion, result.Version)

	var probes []Probe
	require.NoError(t, db.Where("listing_id = ? AND type = ?", listing.ID, ProbeTypeHeadersCheck).
		Find(&probes).Error)
	require.NotEmpty(t, probes)
	assert.False(t, probes[len(probes)-1].OK)
	assert.NotContains(t, probes[len(probes)-1].Detail, "wrong-route", "header values never reach a probe")

	var rejected int64
	require.NoError(t, db.Model(&ListingAudit{}).
		Where("listing_id = ? AND action = ?", listing.ID, ListingAuditHeadersReject).
		Count(&rejected).Error)
	assert.EqualValues(t, 1, rejected)
}

func TestVerifyHeadersDiscardsSupersededVersion(t *testing.T) {
	db, listing := newCustomHeaderListing(t, "sk-header-supersede-1234")
	useHeaderProbeServer(t)

	result, err := SubmitListingHeaders(db, listing.ID, listing.SellerID, map[string]string{
		"X-Route": "blue-route-1234",
	}, time.Now())
	require.NoError(t, err)
	require.NoError(t, discardPendingHeaders(db, listing.ID, 0))
	verifyPendingHeaders(db, listing.ID, result.Version)

	var raw *string
	require.NoError(t, db.Table("channels").Select("header_override").
		Where("id = ?", listing.ChannelID).Scan(&raw).Error)
	assert.True(t, raw == nil || *raw == "", "a cancelled candidate must not be applied")
}

func TestClearListingHeaders(t *testing.T) {
	db, listing := newCustomHeaderListing(t, "sk-header-clear-1234")
	require.NoError(t, db.Table("channels").Where("id = ?", listing.ChannelID).
		Update("header_override", `{"x-old":"keep"}`).Error)

	require.NoError(t, ClearListingHeaders(db, listing.ID, listing.SellerID, time.Now()))
	var raw *string
	require.NoError(t, db.Table("channels").Select("header_override").
		Where("id = ?", listing.ChannelID).Scan(&raw).Error)
	assert.True(t, raw == nil || *raw == "")

	view, err := SellerHeadersViewFor(db, listing)
	require.NoError(t, err)
	assert.Empty(t, view.Headers)
}

func TestSubmitHeadersRejectsTrustedChannel(t *testing.T) {
	db := newListingTestDB(t)
	listing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-header-trusted"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	_, err = SubmitListingHeaders(db, listing.ID, listing.SellerID, map[string]string{
		"X-Route": "blue",
	}, time.Now())
	assert.ErrorIs(t, err, ErrHeadersUnsupported)
}
