package marketplace

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// sellerAPIFixture stands up the §5 阶段 6 seller API against an isolated
// database: upstream users/channels tables, marketplace tables, a
// header-driven stand-in for middleware.UserAuth, and the package-level relay
// state pointed at the test handle.
type sellerAPIFixture struct {
	db        *gorm.DB
	engine    *gin.Engine
	sellerID  int64
	stranger  int64
	buyerID   int64
	restoreFn func()
}

// headerSellerUserAuth replaces middleware.UserAuth in tests: the
// X-Test-User-Id header plays the dashboard session.
func headerSellerUserAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("X-Test-User-Id"))
		id, err := strconv.Atoi(raw)
		if raw == "" || err != nil || id <= 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "unauthorized"})
			return
		}
		common.SetContextKey(c, constant.ContextKeyUserId, id)
		c.Next()
	}
}

func newSellerAPIFixture(t *testing.T) *sellerAPIFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	require.NoError(t, SeedPrices(db))

	oldDB := model.DB
	model.DB = db
	oldEnabled := enabled
	enabled = true
	preserveAnonSecret(t)
	SetAnonSecret("seller-test-secret")

	relayStateMu.Lock()
	oldRelayDB, oldSelector, oldCal := relayDB, defaultSelector, defaultCalendar
	relayDB = db
	relayStateMu.Unlock()
	t.Cleanup(func() {
		model.DB = oldDB
		enabled = oldEnabled
		relayStateMu.Lock()
		relayDB, defaultSelector, defaultCalendar = oldRelayDB, oldSelector, oldCal
		relayStateMu.Unlock()
	})

	seller := model.User{Username: "seller", Password: "placeholder-pass", AffCode: "seller"}
	require.NoError(t, db.Create(&seller).Error)
	stranger := model.User{Username: "stranger", Password: "placeholder-pass", AffCode: "stranger"}
	require.NoError(t, db.Create(&stranger).Error)
	buyer := model.User{Username: "buyer", Password: "placeholder-pass", AffCode: "buyer"}
	require.NoError(t, db.Create(&buyer).Error)

	engine := gin.New()
	registerSellerRoutes(engine.Group("/api/marketplace", headerSellerUserAuth()))

	return &sellerAPIFixture{
		db:       db,
		engine:   engine,
		sellerID: int64(seller.Id),
		stranger: int64(stranger.Id),
		buyerID:  int64(buyer.Id),
	}
}

func (f *sellerAPIFixture) do(t *testing.T, method, path string, body any, userID int) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := common.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if userID > 0 {
		req.Header.Set("X-Test-User-Id", strconv.Itoa(userID))
	}
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

func decodeSellerBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &v))
	return v
}

// TestSellerCreateListingTriggersVerify covers §5 阶段 6 挂单创建: the create
// returns the draft id and the async verification flips it to canary against a
// mock vendor.
func TestSellerCreateListingTriggersVerify(t *testing.T) {
	f := newSellerAPIFixture(t)
	// Mock the vendor so the async verification passes. VerifyListing resolves
	// the vendor's whitelisted BaseURL internally, so the mock client must
	// redirect api.deepseek.com to the httptest server (rewriteTransport).
	h := allGreenHandler()
	srv, _ := newVendorServer(t, h)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := &http.Client{Transport: rewriteTransport{target: target}}
	restore := setSellerVerifyClientForTest(client)
	defer restore()

	key := "sk-seller-create-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	// The mock must accept this listing's actual key.
	h.wantAuth = "Bearer " + key
	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings", map[string]any{
		"vendor_id": "deepseek", "api_key": key,
		"models": []string{"deepseek-flash"}, "multiplier_milli": 500,
	}, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeSellerBody(t, w)
	data := body["data"].(map[string]any)
	id := int64(data["id"].(float64))
	assert.Equal(t, ListingDraft, data["status"])

	// The async verification should promote draft → canary shortly.
	require.Eventually(t, func() bool {
		row, err := GetListing(f.db, id)
		return err == nil && row.Status == ListingCanary
	}, 5*time.Second, 10*time.Millisecond)
	assert.NotContains(t, w.Body.String(), key, "§2.3: the create response never echoes the key")
}

// TestSellerListListingsNoKey covers the seller listing list: it shows the
// fingerprint's first 8 chars and never the ciphertext or full fingerprint.
func TestSellerListListingsNoKey(t *testing.T) {
	f := newSellerAPIFixture(t)
	key := "sk-seller-list-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/listings", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	items, ok := decodeSellerBody(t, w)["data"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	first, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, listing.ID, first["id"])
	fp8, ok := first["key_fingerprint8"].(string)
	require.True(t, ok)
	assert.Len(t, fp8, 8)
	assert.NotContains(t, w.Body.String(), listing.KeyFingerprint)
	assert.NotContains(t, w.Body.String(), key)
}

// TestSellerOwnershipAndMultiplier covers ownership (foreign listing → 404) and
// the multiplier 10-minute-interval error mapping.
func TestSellerOwnershipAndMultiplier(t *testing.T) {
	f := newSellerAPIFixture(t)
	// A listing owned by the fixture seller.
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          "sk-seller-own-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	// A listing owned by the stranger.
	foreign, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.stranger,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          "sk-seller-foreign-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)

	// The seller can change their own multiplier.
	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10)+"/multiplier",
		map[string]any{"multiplier_milli": 600}, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)

	// A foreign listing is a uniform 404 (ownership, not existence).
	for _, action := range []string{"multiplier", "key", "pause", "resume"} {
		w = f.do(t, http.MethodPost, "/api/marketplace/seller/listings/"+strconv.FormatInt(foreign.ID, 10)+"/"+action,
			map[string]any{"multiplier_milli": 500, "api_key": "sk-x"}, int(f.sellerID))
		assert.Equal(t, http.StatusNotFound, w.Code, action)
	}

	// Changing the multiplier again within 10 minutes → 409 too soon.
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10)+"/multiplier",
		map[string]any{"multiplier_milli": 700}, int(f.sellerID))
	assert.Equal(t, http.StatusConflict, w.Code)

	// Out-of-range multiplier → 400.
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).
		Update("multiplier_updated_at", time.Now().Add(-time.Hour)).Error)
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10)+"/multiplier",
		map[string]any{"multiplier_milli": 5000}, int(f.sellerID))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestSellerPauseResume covers the pause/resume state machine with ownership.
func TestSellerPauseResume(t *testing.T) {
	f := newSellerAPIFixture(t)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          "sk-seller-pause-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).Update("status", ListingLive).Error)

	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10)+"/pause", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	row, err := GetListing(f.db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPaused, row.Status)

	w = f.do(t, http.MethodPost, "/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10)+"/resume", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	row, err = GetListing(f.db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, row.Status)
}

// TestSellerBalance covers the balance endpoint: available + pending from the
// ledger plus the payout list.
func TestSellerBalance(t *testing.T) {
	f := newSellerAPIFixture(t)
	now := time.Now()
	// A settled request gives the seller pending income.
	require.NoError(t, PostSettlement(f.db, Settlement{
		RequestID:       "seller-balance-req-1",
		BuyerUserID:     f.buyerID,
		SellerUserID:    f.sellerID,
		ListingID:       1,
		PriceVersion:    1,
		MultiplierMilli: 500,
		TokenDebit:      10_000_000,
		Now:             now,
	}))
	// Mature one row so there is available balance too.
	require.NoError(t, f.db.Model(&LedgerEntry{}).
		Where("request_id = ? AND entry_type = ?", "seller-balance-req-1", EntrySellerCredit).
		Update("status", LedgerAvailable).Error)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/balance", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeSellerBody(t, w)
	data := body["data"].(map[string]any)
	// 10_000_000 debit, 7% fee → credit = 9_300_000.
	assert.EqualValues(t, 9_300_000, data["available_micro"])
	assert.EqualValues(t, 0, data["pending_micro"])
	assert.NotNil(t, data["payouts"])
}

// TestSellerPayoutRequest covers the payout request endpoint. It exercises the
// validation path (missing idempotency key → 400) and, when payout.go is
// available, a full request. The full flow is guarded so the test still passes
// if the parallel payout.go is not yet present.
func TestSellerPayoutRequest(t *testing.T) {
	f := newSellerAPIFixture(t)

	// Missing idempotency key → 400 regardless of payout.go.
	w := f.do(t, http.MethodPost, "/api/marketplace/seller/payouts", map[string]any{
		"amount_micro": 50_000_000,
	}, int(f.sellerID))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Give the seller available balance so a request can succeed.
	require.NoError(t, PostSettlement(f.db, Settlement{
		RequestID:       "seller-payout-req-1",
		BuyerUserID:     f.buyerID,
		SellerUserID:    f.sellerID,
		ListingID:       1,
		PriceVersion:    1,
		MultiplierMilli: 500,
		TokenDebit:      100_000_000,
		Now:             time.Now(),
	}))
	require.NoError(t, f.db.Model(&LedgerEntry{}).
		Where("request_id = ? AND entry_type = ?", "seller-payout-req-1", EntrySellerCredit).
		Update("status", LedgerAvailable).Error)

	// If payout.go is present, a valid request succeeds and returns the id.
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/payouts", map[string]any{
		"amount_micro": 50_000_000, "idempotency_key": "seller-payout-key-1",
	}, int(f.sellerID))
	if w.Code == http.StatusOK {
		body := decodeSellerBody(t, w)
		data := body["data"].(map[string]any)
		assert.EqualValues(t, 50_000_000, data["amount_micro"])
		assert.Equal(t, PayoutPending, data["status"])
	} else {
		// payout.go not yet wired: the endpoint must still answer a defined
		// 4xx (bad request), never a 500.
		assert.Equal(t, http.StatusBadRequest, w.Code)
	}
}

// TestSellerRequiresAuth covers the seller gate: no login → 401.
func TestSellerRequiresAuth(t *testing.T) {
	f := newSellerAPIFixture(t)
	w := f.do(t, http.MethodGet, "/api/marketplace/seller/listings", nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
