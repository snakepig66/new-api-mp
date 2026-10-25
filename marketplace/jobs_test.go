package marketplace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

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
		chatBody:      `{"usage":{"prompt_cache_hit_tokens":1,"prompt_cache_miss_tokens":1}}`,
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

// patrolOneListingWithClient is patrolOneListing with the client injected.
func patrolOneListingWithClient(db *gorm.DB, limiter *VerifyLimiter, client *http.Client, l *Listing) error {
	apiKey, err := DecryptListingKey(l.KeyCiphertext)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := limiter.Acquire(ctx); err != nil {
		return err
	}
	vendor, ok := VendorByID(l.Vendor)
	if !ok {
		limiter.Release()
		return ErrVendorNotWhitelisted
	}
	res := VerifyListingKey(ctx, client, vendor, apiKey)
	limiter.Release()

	detail, err := common.Marshal(map[string]any{
		"key_valid":    res.KeyValid,
		"models":       len(res.Models),
		"has_balance":  res.BalanceMicro != nil,
		"prompt_cache": res.PromptCacheSupport,
		"fail_reason":  res.FailReason,
	})
	if err != nil {
		detail = []byte("{}")
	}
	probe := Probe{
		ListingID: l.ID,
		Type:      ProbeTypePatrol,
		OK:        res.OK,
		LatencyMs: res.LatencyMs,
		Detail:    string(detail),
	}
	if err := db.Create(&probe).Error; err != nil {
		return err
	}
	if !res.OK {
		return AutoPauseListing(db, l.ID, "patrol failed: "+res.FailReason)
	}
	return nil
}
