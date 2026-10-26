package marketplace

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// TestJobsPromoteAvailable covers the T+7 job: pending seller credits whose
// available_at has passed are promoted to available; future ones are not.
func TestJobsPromoteAvailable(t *testing.T) {
	db := newListingTestDB(t)
	now := time.Now()
	require.NoError(t, PostSettlement(db, Settlement{
		RequestID:       "jobs-promote-1",
		BuyerUserID:     1,
		SellerUserID:    10023,
		ListingID:       1,
		PriceVersion:    1,
		MultiplierMilli: 500,
		TokenDebit:      10_000_000,
		Now:             now.Add(-8 * 24 * time.Hour), // matured
	}))
	require.NoError(t, PostSettlement(db, Settlement{
		RequestID:       "jobs-promote-2",
		BuyerUserID:     1,
		SellerUserID:    10023,
		ListingID:       1,
		PriceVersion:    1,
		MultiplierMilli: 500,
		TokenDebit:      10_000_000,
		Now:             now, // not yet matured
	}))

	promoted, err := PromoteAvailable(db, now)
	require.NoError(t, err)
	assert.EqualValues(t, 1, promoted)

	var matured, future LedgerEntry
	require.NoError(t, db.Where("request_id = ? AND entry_type = ?", "jobs-promote-1", EntrySellerCredit).Take(&matured).Error)
	require.NoError(t, db.Where("request_id = ? AND entry_type = ?", "jobs-promote-2", EntrySellerCredit).Take(&future).Error)
	assert.Equal(t, LedgerAvailable, matured.Status)
	assert.Equal(t, LedgerPending, future.Status)
}

// TestJobsCanaryPromote covers the canary → live job: a listing that has been
// in canary for 24h with >= 98% success over >= 20 samples goes live; a
// low-sample or low-rate listing stays canary.
func TestJobsCanaryPromote(t *testing.T) {
	db := newListingTestDB(t)
	now := time.Now()

	mkCanary := func(t *testing.T, created time.Time) Listing {
		t.Helper()
		listing, err := CreateListing(db, CreateListingInput{
			SellerID:        10023,
			VendorID:        SeedVendorDeepSeek,
			APIKey:          uniqueTestKey("sk-canary"),
			Models:          []string{"deepseek-flash"},
			MultiplierMilli: 500,
		})
		require.NoError(t, err)
		require.NoError(t, db.Model(&Listing{}).Where("id = ?", listing.ID).
			Update("status", ListingCanary).Error)
		require.NoError(t, db.Model(&Listing{}).Where("id = ?", listing.ID).
			Update("created_at", created).Error)
		return listing
	}
	seedRequests := func(t *testing.T, listingID int64, total, success int64) {
		t.Helper()
		for i := int64(0); i < total; i++ {
			status := RequestSuccess
			if i >= success {
				status = RequestFailed
			}
			require.NoError(t, db.Create(&Request{
				RequestID:       "canary-" + strconv.FormatInt(listingID, 10) + "-" + strconv.FormatInt(i, 10),
				BuyerUserID:     1,
				SellerID:        10023,
				ListingID:       listingID,
				Model:           "deepseek-flash",
				Status:          status,
				TotalDebitMicro: 100,
				CreatedAt:       now.Add(-time.Hour),
			}).Error)
		}
	}

	// Mature, high-rate, enough samples → promoted.
	good := mkCanary(t, now.Add(-48*time.Hour))
	seedRequests(t, good.ID, 100, 99) // 99% >= 98%
	// Mature but too few samples → stays canary.
	lowSample := mkCanary(t, now.Add(-48*time.Hour))
	seedRequests(t, lowSample.ID, 10, 10) // 100% but < 20 samples
	// Mature, enough samples but low rate → stays canary.
	lowRate := mkCanary(t, now.Add(-48*time.Hour))
	seedRequests(t, lowRate.ID, 50, 40) // 80% < 98%
	// Not yet mature → stays canary regardless of rate.
	young := mkCanary(t, now.Add(-time.Hour))
	seedRequests(t, young.ID, 50, 50)

	require.NoError(t, promoteCanaryListings(db, now))

	row, err := GetListing(db, good.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, row.Status, "mature + >=98% + >=20 samples promotes")

	for _, l := range []Listing{lowSample, lowRate, young} {
		row, err := GetListing(db, l.ID)
		require.NoError(t, err)
		assert.Equal(t, ListingCanary, row.Status, "listing %d must stay canary", l.ID)
	}
}

// TestJobsPatrol covers the 巡检 job: a live listing whose key fails
// verification is auto-paused and a patrol probe row is written; a healthy one
// stays live with an OK probe.
func TestJobsPatrol(t *testing.T) {
	db := newListingTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))

	// A healthy listing and a failing listing.
	healthy, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-patrol-healthy"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	failing, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-patrol-failing"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&Listing{}).Where("id IN ?", []int64{healthy.ID, failing.ID}).
		Update("status", ListingLive).Error)

	// Mock vendor: healthy key passes, failing key is rejected.
	healthyKey, err := DecryptListingKey(healthy.KeyCiphertext)
	require.NoError(t, err)
	h := &vendorHandler{
		wantAuth:      "Bearer " + healthyKey,
		modelsStatus:  http.StatusOK,
		modelsBody:    `{"object":"list","data":[{"id":"deepseek-flash"}]}`,
		balanceStatus: http.StatusOK,
		balanceBody:   `{"balance_infos":[{"currency":"CNY","total_balance":"5.00"}]}`,
		chatStatus:    http.StatusOK,
		chatBody:      `{"model":"deepseek-flash","choices":[{"message":{"content":"pong"}}],"usage":{"prompt_tokens":5,"prompt_cache_hit_tokens":1,"prompt_cache_miss_tokens":1}}`,
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := &http.Client{Transport: rewriteTransport{target: target}}

	// Run the patrol against both listings with the mock client.
	require.NoError(t, patrolLiveListingsWithClient(db, client, time.Now()))

	// Healthy listing stays live with an OK patrol probe.
	row, err := GetListing(db, healthy.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, row.Status)
	probes := listVerifyProbes(t, db, healthy.ID)
	require.Len(t, probes, 1)
	assert.Equal(t, ProbeTypePatrol, probes[0].Type)
	assert.True(t, probes[0].OK)

	// Failing listing is auto-paused. Two patrol probes are written: the
	// verification check itself and the AutoPauseListing record; both are
	// type "patrol" and neither carries key material.
	row, err = GetListing(db, failing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPaused, row.Status)
	probes = listVerifyProbes(t, db, failing.ID)
	require.Len(t, probes, 2)
	for _, p := range probes {
		assert.Equal(t, ProbeTypePatrol, p.Type)
		assert.False(t, p.OK)
		assert.NotContains(t, p.Detail, "sk-patrol", "§2.3: probe detail carries no key")
	}
}

// patrolLiveListingsWithClient runs one patrol pass with an injected client so
// tests can point the egress-checked path at an httptest mock.
func patrolLiveListingsWithClient(db *gorm.DB, client *http.Client, now time.Time) error {
	var rows []Listing
	if err := db.Where("status = ?", ListingLive).Find(&rows).Error; err != nil {
		return err
	}
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)
	for i := range rows {
		l := &rows[i]
		if err := patrolOneListingWithClient(db, limiter, client, l); err != nil {
			return err
		}
	}
	return nil
}

// fingerprintPatrolHandler mocks one upstream serving both the listing key and
// the platform key. The chat prompt_tokens depend on which bearer token is
// used, so the same URL can act as both the official and candidate endpoint.
type fingerprintPatrolHandler struct {
	officialKey      string
	officialTokens   int64
	candidateTokens  int64
	candidateNoUsage bool
}

func (h *fingerprintPatrolHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/models":
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-flash"}]}`))
	case "/user/balance":
		_, _ = w.Write([]byte(`{"balance_infos":[{"currency":"CNY","total_balance":"5.00"}]}`))
	case "/chat/completions":
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = common.DecodeJson(r.Body, &body)
		// The callability probe sends "ping"; the fingerprint sends the long
		// verify prompt. candidateNoUsage must only break the fingerprint, so
		// the probe still sees a valid chat completion.
		probe := len(body.Messages) > 0 && body.Messages[0].Content == "ping"
		tokens := h.candidateTokens
		official := r.Header.Get("Authorization") == "Bearer "+h.officialKey
		if official {
			tokens = h.officialTokens
		}
		if !probe && !official && h.candidateNoUsage {
			// Valid JSON with non-empty usage but no prompt_tokens: callable,
			// yet the fingerprint comparison cannot run.
			_, _ = w.Write([]byte(`{"model":"deepseek-flash","choices":[{"message":{"content":"x"}}],"usage":{"completion_tokens":1}}`))
			return
		}
		_, _ = fmt.Fprintf(w,
			`{"model":"deepseek-flash","choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":%d,"prompt_cache_hit_tokens":1,"prompt_cache_miss_tokens":1}}`,
			tokens)
	default:
		http.NotFound(w, r)
	}
}

// newFingerprintPatrolListing creates a live listing on the seeded deepseek
// channel (whose model pool entry originates from the same vendor) and returns
// it plus an httptest client that redirects every probe to h.
func newFingerprintPatrolListing(t *testing.T, db *gorm.DB, h *fingerprintPatrolHandler) (Listing, *http.Client) {
	t.Helper()
	// The official-token cache is process-wide and keyed by vendor + model +
	// prompt; keep one test's official count from leaking into the next.
	resetFingerprintCacheForTest()
	l, err := CreateListing(db, CreateListingInput{
		SellerID:        10023,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-fingerprint-patrol"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", l.ID).
		Update("status", ListingLive).Error)
	l, err = GetListing(db, l.ID)
	require.NoError(t, err)

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return l, &http.Client{Transport: rewriteTransport{target: target}}
}

func lastPatrolProbe(t *testing.T, db *gorm.DB, listingID int64) Probe {
	t.Helper()
	var p Probe
	require.NoError(t, db.Where("listing_id = ? AND type = ?", listingID, ProbeTypePatrol).
		Order("id DESC").Take(&p).Error)
	return p
}

// lastFingerprintPatrolProbe skips AutoPauseListing annotation rows, which are
// also type=patrol but carry no fingerprint verdict.
func lastFingerprintPatrolProbe(t *testing.T, db *gorm.DB, listingID int64) Probe {
	t.Helper()
	var p Probe
	require.NoError(t, db.Where("listing_id = ? AND type = ? AND detail LIKE ?",
		listingID, ProbeTypePatrol, `%"fingerprint_checked"%`).
		Order("id DESC").Take(&p).Error)
	return p
}

// An incomplete comparison (missing usage, billing error, 429, network
// failure) is not health evidence: the patrol must not write an OK probe and,
// because the listing cannot be certified, it is paused. It is also not a
// mismatch, so it never extends the mismatch streak.
func TestJobsFingerprintUncheckedDoesNotCount(t *testing.T) {
	db := newListingTestDB(t)
	const officialKey = "sk-platform-fingerprint"
	t.Setenv(PlatformKeysEnv, `{"deepseek":"`+officialKey+`"}`)
	oldClient := fingerprintHTTPClient
	fingerprintHTTPClient = nil
	t.Cleanup(func() { fingerprintHTTPClient = oldClient })

	h := &fingerprintPatrolHandler{
		officialKey:      officialKey,
		officialTokens:   100,
		candidateTokens:  500,
		candidateNoUsage: true,
	}
	listing, client := newFingerprintPatrolListing(t, db, h)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	require.NoError(t, patrolOneListingWithClient(db, limiter, client, &listing))
	probe := lastFingerprintPatrolProbe(t, db, listing.ID)
	assert.False(t, probe.OK, "an incomplete comparison must not be marked healthy")
	assert.Contains(t, probe.Detail, `"fingerprint_checked":false`)
	assert.NotContains(t, probe.Detail, `"fingerprint_ok"`,
		"fingerprint_ok is written only for a fully checked comparison")
	assert.Contains(t, probe.Detail, `"fingerprints"`)
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPaused, row.Status, "fail-closed patrol pauses an uncertified listing")
	consecutive, err := consecutiveFingerprintMismatches(db, listing.ID)
	require.NoError(t, err)
	assert.Zero(t, consecutive, "the failed comparison is not a mismatch")

	// A real mismatch now, on a live listing: it is written as checked+failed
	// and counted, so the streak is exactly one and the listing stays live.
	h.candidateNoUsage = false
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", listing.ID).
		Updates(map[string]any{"status": ListingLive, "pause_reason": "", "resume_at": nil}).Error)
	require.NoError(t, patrolOneListingWithClient(db, limiter, client, &listing))
	probe = lastPatrolProbe(t, db, listing.ID)
	assert.False(t, probe.OK)
	assert.Contains(t, probe.Detail, `"fingerprint_checked":true`)
	assert.Contains(t, probe.Detail, `"fingerprint_ok":false`)
	consecutive, err = consecutiveFingerprintMismatches(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, consecutive, "the failed comparison was skipped, not counted")
	row, err = GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, row.Status, "one real mismatch does not pause")
}

// A 429/402/403 during verification (or a billing failure) is a patrol
// failure, never a fingerprint mismatch: the listing may be paused for the key
// failure, but the fingerprint streak stays empty.
func TestJobsFingerprintBillingAnd429DoNotCount(t *testing.T) {
	db := newListingTestDB(t)
	const officialKey = "sk-platform-fingerprint-paid"
	t.Setenv(PlatformKeysEnv, `{"deepseek":"`+officialKey+`"}`)
	oldClient := fingerprintHTTPClient
	fingerprintHTTPClient = nil
	t.Cleanup(func() { fingerprintHTTPClient = oldClient })

	h := &fingerprintPatrolHandler{officialKey: officialKey, officialTokens: 100, candidateTokens: 500}
	listing, _ := newFingerprintPatrolListing(t, db, h)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	// Seed one real mismatch so a wrongly counted status failure would reach
	// two strikes and pause the listing.
	require.NoError(t, db.Create(&Probe{
		ListingID: listing.ID, Type: ProbeTypePatrol, OK: false,
		Detail: `{"fingerprint_checked":true,"fingerprint_ok":false}`,
	}).Error)

	// The candidate now answers 429 on /models, so VerifyListingKey fails and
	// the fingerprint is never attempted.
	srv429 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"insufficient balance"}}`))
	}))
	t.Cleanup(srv429.Close)
	target, err := url.Parse(srv429.URL)
	require.NoError(t, err)
	client429 := &http.Client{Transport: rewriteTransport{target: target}}

	require.NoError(t, patrolOneListingWithClient(db, limiter, client429, &listing))
	probe := lastFingerprintPatrolProbe(t, db, listing.ID)
	assert.Contains(t, probe.Detail, `"fingerprint_checked":false`)
	assert.NotContains(t, probe.Detail, `"fingerprint_ok":false`,
		"a 429 must not be written as a fingerprint mismatch")

	consecutive, err := consecutiveFingerprintMismatches(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, consecutive, "only the pre-seeded real comparison counts")
}

// Three real mismatches pause the listing and raise one SysError alert, but
// the channel-model qualification must stay active: banning is a manual
// operator action.
func TestJobsFingerprintThreeStrikesNeverBans(t *testing.T) {
	db := newListingTestDB(t)
	const officialKey = "sk-platform-fingerprint-ban"
	t.Setenv(PlatformKeysEnv, `{"deepseek":"`+officialKey+`"}`)
	oldClient := fingerprintHTTPClient
	fingerprintHTTPClient = nil
	t.Cleanup(func() { fingerprintHTTPClient = oldClient })

	h := &fingerprintPatrolHandler{officialKey: officialKey, officialTokens: 100, candidateTokens: 500}
	listing, client := newFingerprintPatrolListing(t, db, h)
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)

	var alerts bytes.Buffer
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &alerts
	t.Cleanup(func() { gin.DefaultErrorWriter = oldWriter })

	// Strike one: still live.
	require.NoError(t, patrolOneListingWithClient(db, limiter, client, &listing))
	row, err := GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, row.Status)

	// Strike two: paused (current listing only).
	require.NoError(t, patrolOneListingWithClient(db, limiter, client, &listing))
	row, err = GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPaused, row.Status)
	pauseProbe := lastPatrolProbe(t, db, listing.ID)
	assert.Contains(t, pauseProbe.Detail, "fingerprint mismatch (2 consecutive)")

	// Strike three: the direct call still records the third mismatch and must
	// escalate to an alert without banning anything.
	require.NoError(t, patrolOneListingWithClient(db, limiter, client, &listing))
	row, err = GetListing(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPaused, row.Status)

	cm, ok := ChannelModelFor(db, SeedVendorDeepSeek, "deepseek-flash")
	require.True(t, ok)
	assert.Equal(t, PoolStatusActive, cm.Status,
		"the channel model is never banned automatically (manual review only)")
	assert.Contains(t, alerts.String(), "fingerprint mismatch (3 consecutive)")
	assert.Contains(t, alerts.String(), "listing")
	assert.Contains(t, alerts.String(), SeedVendorDeepSeek)
	assert.Contains(t, alerts.String(), "deepseek-flash")
}

// The streak walker skips unchecked records (without breaking) and stops at
// the newest real match.
func TestJobsFingerprintStreakSkipsUnchecked(t *testing.T) {
	db := newListingTestDB(t)
	listing := addSelectorListing(t, db, "fp-streak", nil)
	details := []string{
		`{"fingerprint_checked":true,"fingerprint_ok":true}`,
		`{"fingerprint_checked":false,"fingerprint_reason":"prompt too short to compare"}`,
		`{"fingerprint_checked":true,"fingerprint_ok":false}`,
		`{"auto_paused":true,"reason":"patrol failed: vendor /models returned 429"}`,
		`{"fingerprint_checked":true,"fingerprint_ok":false}`,
	}
	for _, detail := range details {
		require.NoError(t, db.Create(&Probe{
			ListingID: listing.ID, Type: ProbeTypePatrol, Detail: detail,
		}).Error)
	}
	consecutive, err := consecutiveFingerprintMismatches(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, consecutive, "unchecked rows are skipped, the match stops the walk")

	// An even newer unchecked row must not reset the streak either.
	require.NoError(t, db.Create(&Probe{
		ListingID: listing.ID, Type: ProbeTypePatrol,
		Detail: `{"fingerprint_checked":false}`,
	}).Error)
	consecutive, err = consecutiveFingerprintMismatches(db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, consecutive)
}
