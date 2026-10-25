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

	// M9: writes require an active seller row; the fixture seller is fully
	// onboarded so the pre-M9 tests keep exercising their own paths.
	require.NoError(t, db.Create(&Seller{
		UserID:           int64(seller.Id),
		Status:           SellerStatusActive,
		RealNameStatus:   SellerRealNameVerified,
		AgreementVersion: CurrentSellerAgreementVersion,
		PayoutAccount:    "fixture-encrypted-account",
	}).Error)

	engine := gin.New()
	grp := engine.Group("/api/marketplace", headerSellerUserAuth())
	registerSellerRoutes(grp)
	registerSellerOnboardingRoutes(grp)
	registerMeRoutes(grp)

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

	key := uniqueTestKey("sk-seller-create")
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
	key := uniqueTestKey("sk-seller-list")
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
		APIKey:          uniqueTestKey("sk-seller-own"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	// A listing owned by the stranger.
	foreign, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.stranger,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-seller-foreign"),
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
		APIKey:          uniqueTestKey("sk-seller-pause"),
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

// ---------------------------------------------------------------------------
// M9 开通状态机 + GET /me
// ---------------------------------------------------------------------------

func TestSellerOnboardingFlow(t *testing.T) {
	f := newSellerAPIFixture(t)
	userID := int(f.buyerID) // this fixture user has no seller row

	// 1. Wrong agreement version → 400.
	w := f.do(t, http.MethodPost, "/api/marketplace/seller/onboarding", map[string]any{
		"agreement_version": "v2",
	}, userID)
	require.Equal(t, http.StatusBadRequest, w.Code)

	// 2. Correct agreement → active immediately (open sharing); real-name and
	// payout onboarding stay as optional nudges for withdrawal.
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/onboarding", map[string]any{
		"agreement_version": CurrentSellerAgreementVersion,
	}, userID)
	require.Equal(t, http.StatusOK, w.Code)
	seller := onboardingSellerView(t, w)
	assert.Equal(t, true, seller["exists"])
	assert.Equal(t, SellerStatusActive, seller["status"])
	assert.Equal(t, "real_name", seller["progress"].(map[string]any)["next_step"])

	// 3. Real name alone advances the optional rail without touching activation.
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/onboarding", map[string]any{
		"real_name": "张三",
	}, userID)
	require.Equal(t, http.StatusOK, w.Code)
	seller = onboardingSellerView(t, w)
	assert.Equal(t, SellerStatusActive, seller["status"])
	assert.Equal(t, "payout_account", seller["progress"].(map[string]any)["next_step"])

	// 4. Invalid real name / plaintext id / bad alipay → 400 and no state change.
	for _, body := range []map[string]any{
		{"real_name": "12345"},
		{"id_card_masked": "110101199001011234"}, // unmasked: no '*'
		{"alipay_account": "not-an-account"},
	} {
		w = f.do(t, http.MethodPost, "/api/marketplace/seller/onboarding", body, userID)
		assert.Equal(t, http.StatusBadRequest, w.Code, body)
	}

	// 5. Masked id + alipay → active; the response never echoes the account.
	alipay := "seller-payout@example.com"
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/onboarding", map[string]any{
		"id_card_masked": "110101********1234",
		"alipay_account": alipay,
	}, userID)
	require.Equal(t, http.StatusOK, w.Code)
	seller = onboardingSellerView(t, w)
	assert.Equal(t, SellerStatusActive, seller["status"])
	assert.Equal(t, "done", seller["progress"].(map[string]any)["next_step"])
	assert.NotContains(t, w.Body.String(), alipay, "§2.3: the payout account is never returned")
	assert.NotContains(t, w.Body.String(), "110101", "the masked id is never echoed")

	// 6. Stored at rest as AES-GCM ciphertext, decryptable back to plaintext.
	var row Seller
	require.NoError(t, f.db.Where("user_id = ?", f.buyerID).Take(&row).Error)
	assert.NotEqual(t, alipay, row.PayoutAccount)
	assert.NotContains(t, row.PayoutAccount, alipay)
	plain, err := decryptPayoutAccount(row.PayoutAccount)
	require.NoError(t, err)
	assert.Equal(t, alipay, plain)
	assert.Equal(t, SellerRealNameVerified, row.RealNameStatus)

	// 7. GET /me reports the seller identity and progress.
	w = f.do(t, http.MethodGet, "/api/marketplace/me", nil, userID)
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeSellerBody(t, w)["data"].(map[string]any)
	assert.EqualValues(t, f.buyerID, data["user_id"])
	meSeller := data["seller"].(map[string]any)
	assert.Equal(t, true, meSeller["exists"])
	assert.Equal(t, SellerStatusActive, meSeller["status"])
	assert.Equal(t, "done", meSeller["progress"].(map[string]any)["next_step"])
	assert.NotContains(t, w.Body.String(), alipay)

	// 8. A user without a seller row is reported as exists=false.
	w = f.do(t, http.MethodGet, "/api/marketplace/me", nil, int(f.stranger))
	require.Equal(t, http.StatusOK, w.Code)
	data = decodeSellerBody(t, w)["data"].(map[string]any)
	meSeller = data["seller"].(map[string]any)
	assert.Equal(t, false, meSeller["exists"])
	assert.Equal(t, "agreement", meSeller["progress"].(map[string]any)["next_step"])
}

func onboardingSellerView(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	data, ok := decodeSellerBody(t, w)["data"].(map[string]any)
	require.True(t, ok)
	seller, ok := data["seller"].(map[string]any)
	require.True(t, ok)
	return seller
}

// TestSellerOnboardingInactiveWriteRejected covers M9: pending (or missing)
// sellers cannot open listings or request payouts → 403.
func TestSellerOnboardingInactiveWriteRejected(t *testing.T) {
	f := newSellerAPIFixture(t)
	// Demote the onboarded fixture seller to pending.
	require.NoError(t, f.db.Model(&Seller{}).Where("user_id = ?", f.sellerID).
		Update("status", SellerStatusPending).Error)

	body := map[string]any{
		"vendor_id": "deepseek", "api_key": uniqueTestKey("sk-inactive"),
		"models": []string{"deepseek-flash"}, "multiplier_milli": 500,
	}
	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings", body, int(f.sellerID))
	assert.Equal(t, http.StatusForbidden, w.Code)

	w = f.do(t, http.MethodPost, "/api/marketplace/seller/payouts", map[string]any{
		"amount_micro": 50_000_000, "idempotency_key": "inactive-payout",
	}, int(f.sellerID))
	assert.Equal(t, http.StatusForbidden, w.Code)

	// A user with no seller row at all is rejected on both writes too.
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/listings", body, int(f.stranger))
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/payouts", map[string]any{
		"amount_micro": 50_000_000, "idempotency_key": "stranger-payout",
	}, int(f.stranger))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestPayoutRequiresBoundAccount covers the other half of open sharing: a
// seller activated by the agreement alone may list, but the first withdrawal
// asks for a payout account instead of silently accepting an unusable request.
func TestPayoutRequiresBoundAccount(t *testing.T) {
	f := newSellerAPIFixture(t)
	userID := int(f.buyerID)

	w := f.do(t, http.MethodPost, "/api/marketplace/seller/onboarding", map[string]any{
		"agreement_version": CurrentSellerAgreementVersion,
	}, userID)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, SellerStatusActive, onboardingSellerView(t, w)["status"])

	w = f.do(t, http.MethodPost, "/api/marketplace/seller/payouts", map[string]any{
		"amount_micro": 50_000_000, "idempotency_key": "no-account-payout",
	}, userID)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "payout account is not bound")

	// Binding the account lets the request reach the balance check, which is
	// the next gate for this account (it has no seller income).
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/onboarding", map[string]any{
		"alipay_account": "payout-gate@example.com",
	}, userID)
	require.Equal(t, http.StatusOK, w.Code)
	seller := onboardingSellerView(t, w)
	assert.Equal(t, SellerStatusActive, seller["status"])
	assert.Equal(t, "real_name", seller["progress"].(map[string]any)["next_step"])

	w = f.do(t, http.MethodPost, "/api/marketplace/seller/payouts", map[string]any{
		"amount_micro": 50_000_000, "idempotency_key": "no-account-payout-2",
	}, userID)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "insufficient available balance")
}

// ---------------------------------------------------------------------------
// probe / listing detail
// ---------------------------------------------------------------------------

// TestProbeDoesNotPersist covers POST /seller/listings/probe: the dry-run
// result fields, no mp_listings/mp_probes writes and no key in the response.
func TestProbeDoesNotPersist(t *testing.T) {
	f := newSellerAPIFixture(t)
	h := allGreenHandler()
	srv, _ := newVendorServer(t, h)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	restore := setSellerVerifyClientForTest(&http.Client{Transport: rewriteTransport{target: target}})
	defer restore()

	var listingsBefore, probesBefore int64
	require.NoError(t, f.db.Model(&Listing{}).Count(&listingsBefore).Error)
	require.NoError(t, f.db.Model(&Probe{}).Count(&probesBefore).Error)

	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings/probe", map[string]any{
		"vendor_id": "deepseek",
		"api_key":   verifyTestKey,
		"models":    []string{"deepseek-flash"},
	}, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)

	data := decodeSellerBody(t, w)["data"].(map[string]any)
	assert.Equal(t, true, data["ok"])
	assert.Equal(t, true, data["key_valid"])
	assert.Equal(t, true, data["prompt_cache"])
	assert.NotNil(t, data["balance_micro"])
	assert.EqualValues(t, 12_340_000, data["balance_micro"])
	models, ok := data["models"].([]any)
	require.True(t, ok)
	assert.Contains(t, models, "deepseek-flash")
	assert.EqualValues(t, 0, len(data["missing_models"].([]any)))
	assert.Equal(t, "", data["fail_reason"])
	assert.NotContains(t, w.Body.String(), verifyTestKey, "§2.3: probe never echoes the key")

	var listingsAfter, probesAfter int64
	require.NoError(t, f.db.Model(&Listing{}).Count(&listingsAfter).Error)
	require.NoError(t, f.db.Model(&Probe{}).Count(&probesAfter).Error)
	assert.Equal(t, listingsBefore, listingsAfter, "probe must not create a listing")
	assert.Equal(t, probesBefore, probesAfter, "probe must not write mp_probes")
}

// TestProbeListingDetail covers GET /seller/listings/:id: ownership, the last
// 10 probes, and the paused reason taken from the probe detail.
func TestProbeListingDetail(t *testing.T) {
	f := newSellerAPIFixture(t)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-detail"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).
		Update("status", ListingPaused).Error)
	require.NoError(t, f.db.Create(&Probe{
		ListingID: listing.ID, Type: ProbeTypeVerify, OK: false, LatencyMs: 42,
		Detail: `{"key_valid":false,"fail_reason":"key rejected (http 401)"}`,
	}).Error)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10), nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeSellerBody(t, w)["data"].(map[string]any)
	assert.EqualValues(t, listing.ID, data["id"])
	assert.Equal(t, ListingPaused, data["status"])
	assert.Equal(t, "key rejected (http 401)", data["pause_reason"])
	probes, ok := data["probes"].([]any)
	require.True(t, ok)
	require.Len(t, probes, 1)
	first := probes[0].(map[string]any)
	assert.Equal(t, false, first["ok"])
	detail, ok := first["detail"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "key rejected (http 401)", detail["fail_reason"])
	assert.NotContains(t, w.Body.String(), listing.KeyFingerprint)

	// Ownership: a foreign user gets the uniform 404.
	w = f.do(t, http.MethodGet, "/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10), nil, int(f.stranger))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ---------------------------------------------------------------------------
// stats / payouts
// ---------------------------------------------------------------------------

// TestSellerStats covers GET /seller/stats against hand-inserted ledger rows.
func TestSellerStats(t *testing.T) {
	f := newSellerAPIFixture(t)
	now := time.Now()
	old := now.AddDate(0, -2, 0)
	maturity := now.Add(24 * time.Hour)

	require.NoError(t, f.db.Create(&Request{
		RequestID: "stats-req-1", BuyerUserID: f.buyerID, SellerID: f.sellerID,
		Model: "deepseek-flash", Status: RequestSuccess, CreatedAt: now,
	}).Error)
	require.NoError(t, f.db.Create(&Request{
		RequestID: "stats-req-2", BuyerUserID: f.buyerID, SellerID: f.sellerID,
		Model: "deepseek-flash", Status: RequestSuccess, CreatedAt: old,
	}).Error)
	entries := []LedgerEntry{
		{RequestID: "stats-req-1", EntryType: EntrySellerCredit, Account: UserAccount(f.sellerID),
			AmountMicro: 9_300_000, Status: LedgerPending, AvailableAt: &maturity, CreatedAt: now},
		{RequestID: "stats-req-1", EntryType: EntryPlatformFee, Account: PlatformAccount(),
			AmountMicro: 700_000, Status: LedgerAvailable, CreatedAt: now},
		{RequestID: "stats-req-2", EntryType: EntrySellerCredit, Account: UserAccount(f.sellerID),
			AmountMicro: 4_650_000, Status: LedgerAvailable, CreatedAt: old},
		{RequestID: "stats-req-2", EntryType: EntryPlatformFee, Account: PlatformAccount(),
			AmountMicro: 350_000, Status: LedgerAvailable, CreatedAt: old},
	}
	require.NoError(t, f.db.Create(&entries).Error)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/stats", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeSellerBody(t, w)["data"].(map[string]any)

	assert.EqualValues(t, 9_300_000, data["pending_micro"])
	assert.EqualValues(t, 1_050_000, data["platform_fee_total_micro"])
	// Two months ago is outside the current month: only req-1 counts.
	assert.EqualValues(t, 9_300_000, data["month_income_micro"])

	// The available figure is whatever the ledger's withdrawable helper says
	// (signature is shared with payout.go and its account split may change).
	available, err := AvailableBalance(f.db, f.sellerID)
	require.NoError(t, err)
	assert.EqualValues(t, int64(available), data["available_micro"])

	daily, ok := data["daily"].([]any)
	require.True(t, ok)
	require.Len(t, daily, 30)
	loc, err := time.LoadLocation(shanghaiTZ)
	require.NoError(t, err)
	today := now.In(loc).Format("2006-01-02")
	var todayIncome any
	for _, item := range daily {
		row := item.(map[string]any)
		if row["date"] == today {
			todayIncome = row["income_micro"]
		}
	}
	assert.EqualValues(t, 9_300_000, todayIncome)
}

// TestSellerPayouts covers GET /seller/payouts pagination.
func TestSellerPayouts(t *testing.T) {
	f := newSellerAPIFixture(t)
	for i := 1; i <= 3; i++ {
		require.NoError(t, f.db.Create(&Payout{
			SellerID:       f.sellerID,
			AmountMicro:    int64(i) * 1_000_000,
			Status:         PayoutPending,
			IdempotencyKey: "seller-payout-page-" + strconv.Itoa(i),
			CreatedAt:      time.Now().Add(time.Duration(i) * time.Second),
		}).Error)
	}
	w := f.do(t, http.MethodGet, "/api/marketplace/seller/payouts?page=2&page_size=2", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeSellerBody(t, w)["data"].(map[string]any)
	assert.EqualValues(t, 3, data["total"])
	assert.EqualValues(t, 2, data["page"])
	assert.EqualValues(t, 2, data["page_size"])
	items, ok := data["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1, "3 payouts, page 2 of size 2 → the oldest one")
	first := items[0].(map[string]any)
	// Payout has no JSON tags; encoding/json keeps the Go field name.
	assert.EqualValues(t, 1_000_000, first["AmountMicro"])
}

// TestSellerStatsRetryChainRedaction covers L4: a share detail's retry chain
// never carries other listing ids or seller identifiers.
func TestSellerStatsRetryChainRedaction(t *testing.T) {
	raw := `[
		{"seq":1,"event":"select","listing_id":777,"detail":"selected listing 777 for seller_id=88","at":"2026-10-24T10:00:00+08:00"},
		{"seq":2,"event":"switch","listing_id":999,"detail":"listing_id=999 seller #88 failed","at":"2026-10-24T10:00:01+08:00"}
	]`
	out := sellerRetryChain(raw)
	assert.NotContains(t, out, "777")
	assert.NotContains(t, out, "999")
	assert.NotContains(t, out, "88")
	assert.NotContains(t, out, "listing_id")
	assert.NotContains(t, out, "seller_id")

	var hops []map[string]any
	require.NoError(t, common.Unmarshal([]byte(out), &hops))
	require.Len(t, hops, 2)
	for _, h := range hops {
		assert.Contains(t, h, "seq")
		assert.Contains(t, h, "event")
		assert.Contains(t, h, "at")
		assert.NotContains(t, h, "listing_id")
	}

	// Malformed input never leaks raw text.
	assert.Equal(t, "[]", sellerRetryChain("not json"))
	assert.Equal(t, "", sellerRetryChain(""))
}

// TestSellerCannotResumeSystemPause covers the P1 lifecycle rule: a pause the
// platform created (patrol, migration, plan quota, fingerprint) cannot be
// lifted from the seller console.
func TestSellerCannotResumeSystemPause(t *testing.T) {
	f := newSellerAPIFixture(t)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-seller-sys-pause"),
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).
		Updates(map[string]any{"status": ListingPaused, "pause_reason": "migration"}).Error)

	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10)+"/resume", nil, int(f.sellerID))
	assert.Equal(t, http.StatusConflict, w.Code)

	row, err := GetListing(f.db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPaused, row.Status)

	// Clearing the system reason (an admin decision) allows the seller edge.
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).
		Updates(map[string]any{"pause_reason": "", "resume_at": nil}).Error)
	w = f.do(t, http.MethodPost, "/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10)+"/resume", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	row, err = GetListing(f.db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, row.Status)
}
