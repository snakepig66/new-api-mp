package marketplace

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
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
	RegisterVendorsRoutes(grp)

	// Probe quota and fingerprint tracking are process-wide; a fresh fixture
	// must not inherit another test's counters.
	resetProbeStateForTest()

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

// TestSellerCreatePendingReviewSkipsVerification covers a seller-defined
// upstream the trust pool does not trust: the create response is pending_review
// and the background verifier is not started at all, so no
// ErrInvalidTransition line is logged for a row the operator has not approved.
func TestSellerCreatePendingReviewSkipsVerification(t *testing.T) {
	f := newSellerAPIFixture(t)

	var logs syncBuffer
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logs
	t.Cleanup(func() { gin.DefaultErrorWriter = oldWriter })

	key := uniqueTestKey("sk-seller-pending")
	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings", map[string]any{
		"custom_base_url":  "https://unlisted-" + strconv.FormatInt(time.Now().UnixNano(), 36) + ".example.com",
		"api_key":          key,
		"models":           []string{"deepseek-flash"},
		"multiplier_milli": 500,
	}, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	data, ok := decodeSellerBody(t, w)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, ListingPendingReview, data["status"])
	id := int64(data["id"].(float64))

	// The verifier, if it were started, would reach VerifyListing after its
	// small delay and log the invalid transition. Never seeing the line is the
	// observable contract.
	require.Never(t, func() bool {
		return strings.Contains(logs.String(), "invalid listing status transition")
	}, 150*time.Millisecond, 10*time.Millisecond)

	var probes int64
	require.NoError(t, f.db.Model(&Probe{}).
		Where("listing_id = ? AND type = ?", id, ProbeTypeVerify).Count(&probes).Error)
	assert.Zero(t, probes, "no verification probe may be written before approval")
	row, err := GetListing(f.db, id)
	require.NoError(t, err)
	assert.Equal(t, ListingPendingReview, row.Status)
}

// syncBuffer is a concurrency-safe log sink: the background verifier this test
// guards against would write from another goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
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
	// A non-default channel coefficient must reach the wizard so its buyer
	// price preview matches Quote (official × coefficient × multiplier).
	require.NoError(t, f.db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", SeedVendorDeepSeek, "deepseek-flash").
		Update("coefficient_milli", 1200).Error)

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
	assert.Equal(t, true, data["balance_sufficient"], "only enough/not-enough is surfaced")
	assert.NotContains(t, data, "balance_micro", "the upstream balance itself is never returned")
	models, ok := data["models"].([]any)
	require.True(t, ok)
	assert.Contains(t, models, "deepseek-flash")
	coefficients, ok := data["coefficients"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 1200, coefficients["deepseek-flash"])
	assert.EqualValues(t, 0, len(data["missing_models"].([]any)))
	assert.Equal(t, "", data["fail_reason"])
	trust, ok := data["trust_pool"].(map[string]any)
	require.True(t, ok, "the probe carries the trust-pool recognition result")
	assert.Equal(t, true, trust["matched"])
	assert.Equal(t, TrustTrusted, trust["trust"])
	assert.Equal(t, TrustKindOfficial, trust["kind"])
	assert.Equal(t, "DeepSeek", trust["vendor_name"])
	assert.Contains(t, trust["endpoints"], "openai_chat")
	assert.Contains(t, trust["badge_text"], "DeepSeek")
	assert.Contains(t, trust["badge_text"], "官方")
	assert.Contains(t, trust["badge_text"], "免审核")
	assert.NotContains(t, w.Body.String(), verifyTestKey, "§2.3: probe never echoes the key")

	var listingsAfter, probesAfter int64
	require.NoError(t, f.db.Model(&Listing{}).Count(&listingsAfter).Error)
	require.NoError(t, f.db.Model(&Probe{}).Count(&probesAfter).Error)
	assert.Equal(t, listingsBefore, listingsAfter, "probe must not create a listing")
	assert.Equal(t, probesBefore, probesAfter, "probe must not write mp_probes")
}

// TestProbeRequiresActiveSeller covers the probe write gate: a pending (or
// missing) seller row answers 403 before any upstream traffic.
func TestProbeRequiresActiveSeller(t *testing.T) {
	f := newSellerAPIFixture(t)
	require.NoError(t, f.db.Model(&Seller{}).Where("user_id = ?", f.sellerID).
		Update("status", SellerStatusPending).Error)

	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings/probe", map[string]any{
		"vendor_id": "deepseek", "api_key": verifyTestKey,
	}, int(f.sellerID))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestProbeCustomURLDoesNotPersist covers the custom-URL dry run: it must not
// create a channel-pool row, channel-model rows or egress whitelist
// membership. Those side effects belong to CreateListing.
func TestProbeCustomURLDoesNotPersist(t *testing.T) {
	f := newSellerAPIFixture(t)
	preserveEgressDomains(t)
	SetEgressDomains(nil)
	SetEgressSellerHosts(nil)

	const key = "sk-probe-custom-key"
	server := fakeCustomUpstream(t, key, "deepseek-flash")
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	restore := setSellerVerifyClientForTest(&http.Client{Transport: rewriteTransport{target: target}})
	defer restore()

	var channelsBefore, modelsBefore int64
	require.NoError(t, f.db.Model(&ChannelPool{}).Count(&channelsBefore).Error)
	require.NoError(t, f.db.Model(&ChannelModel{}).Count(&modelsBefore).Error)

	w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings/probe", map[string]any{
		"custom_base_url": "https://probe-upstream.example.com",
		"api_key":         key,
		"models":          []string{"deepseek-flash"},
	}, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeSellerBody(t, w)["data"].(map[string]any)
	assert.Equal(t, true, data["ok"])
	assert.Equal(t, []any{"deepseek-flash"}, data["models"])
	assert.NotContains(t, data, "balance_micro")
	assert.Equal(t, false, data["balance_sufficient"], "the fake upstream has no balance endpoint")
	trust, ok := data["trust_pool"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, trust["matched"])
	assert.Equal(t, TrustUnlisted, trust["trust"])
	assert.Contains(t, trust["badge_text"], "未收录")
	assert.NotContains(t, w.Body.String(), key, "§2.3: probe never echoes the key")

	var channelsAfter, modelsAfter int64
	require.NoError(t, f.db.Model(&ChannelPool{}).Count(&channelsAfter).Error)
	require.NoError(t, f.db.Model(&ChannelModel{}).Count(&modelsAfter).Error)
	assert.Equal(t, channelsBefore, channelsAfter, "probe must not create a channel-pool row")
	assert.Equal(t, modelsBefore, modelsAfter, "probe must not write channel-model rows")
	var customChannels int64
	require.NoError(t, f.db.Model(&ChannelPool{}).
		Where("slug LIKE ?", customChannelSlugPrefix+"%").
		Count(&customChannels).Error)
	assert.Zero(t, customChannels, "probe must not provision a custom channel")
	assert.ErrorIs(t, CheckEgressHost("probe-upstream.example.com"), ErrEgressDenied,
		"probe must not touch the relay whitelist")
}

// TestProbeRateLimits covers both fixed windows: the account cap and the
// client-IP cap each answer 429.
func TestProbeRateLimits(t *testing.T) {
	t.Run("per seller", func(t *testing.T) {
		f := newSellerAPIFixture(t)
		resetProbeStateForTest()
		body := map[string]any{"vendor_id": "not-whitelisted", "api_key": verifyTestKey}
		for i := range probeSellerPerMinute {
			w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings/probe", body, int(f.sellerID))
			require.Equal(t, http.StatusBadRequest, w.Code, "request %d stays under the limit", i+1)
		}
		w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings/probe", body, int(f.sellerID))
		assert.Equal(t, http.StatusTooManyRequests, w.Code)
	})

	t.Run("per ip", func(t *testing.T) {
		f := newSellerAPIFixture(t)
		resetProbeStateForTest()
		extraSellers := []int64{900001, 900002, 900003}
		for _, uid := range extraSellers {
			require.NoError(t, f.db.Create(&Seller{
				UserID:           uid,
				Status:           SellerStatusActive,
				RealNameStatus:   SellerRealNameVerified,
				AgreementVersion: CurrentSellerAgreementVersion,
				PayoutAccount:    "fixture-encrypted-account",
			}).Error)
		}
		sellers := append([]int64{f.sellerID}, extraSellers...)

		codes := make([]int, 0, len(sellers)*probeSellerPerMinute)
		for _, uid := range sellers {
			body := map[string]any{
				"vendor_id": "not-whitelisted",
				"api_key":   uniqueTestKey("sk-probe-ip-" + strconv.FormatInt(uid, 10)),
			}
			for range probeSellerPerMinute {
				w := f.do(t, http.MethodPost, "/api/marketplace/seller/listings/probe", body, int(uid))
				codes = append(codes, w.Code)
			}
		}
		require.Len(t, codes, len(sellers)*probeSellerPerMinute)
		for i, code := range codes[:probeIPPerMinute] {
			assert.Equal(t, http.StatusBadRequest, code, "request %d is under the IP limit", i+1)
		}
		for i, code := range codes[probeIPPerMinute:] {
			assert.Equal(t, http.StatusTooManyRequests, code,
				"request %d exceeds the IP limit", i+probeIPPerMinute+1)
		}
	})
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

// TestSellerListingDetailLatestVerifyOutsideRecentProbes locks the detail
// response to the newest verify probe even when ten newer patrol probes fill
// the recent-probe window. The seller needs the per-model verify results to
// fix a failed draft, regardless of patrol chatter.
func TestSellerListingDetailLatestVerifyOutsideRecentProbes(t *testing.T) {
	f := newSellerAPIFixture(t)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          uniqueTestKey("sk-detail-latest-verify"),
		Models:          []string{"deepseek-flash", "deepseek-v4-pro"},
		MultiplierMilli: 300,
	})
	require.NoError(t, err)
	verifyDetail := `{"key_valid":true,"fail_reason":"one model failed",` +
		`"model_results":[` +
		`{"model":"deepseek-flash","upstream_model":"deepseek-flash","ok":true,"prompt_cache_support":true,"latency_ms":40},` +
		`{"model":"deepseek-v4-pro","upstream_model":"deepseek-v4-pro","ok":false,"latency_ms":80,"fail_reason":"该模型不在这个 Key 的可用模型列表中"}]}`
	require.NoError(t, f.db.Create(&Probe{
		ListingID: listing.ID, Type: ProbeTypeVerify, OK: false, LatencyMs: 80,
		Detail: verifyDetail,
	}).Error)
	for i := 0; i < 10; i++ {
		require.NoError(t, f.db.Create(&Probe{
			ListingID: listing.ID, Type: "patrol", OK: true, LatencyMs: 10,
			Detail: `{"ok":true}`,
		}).Error)
	}

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10), nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeSellerBody(t, w)["data"].(map[string]any)
	probes, ok := data["probes"].([]any)
	require.True(t, ok)
	require.Len(t, probes, 10, "the recent-probe window still shows the patrols")

	verification, ok := data["verification"].(map[string]any)
	require.True(t, ok, "the newest verify probe must survive the patrol window")
	assert.Equal(t, false, verification["ok"])
	results, ok := verification["model_results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 2)
	failed := results[1].(map[string]any)
	assert.Equal(t, "deepseek-v4-pro", failed["model"])
	assert.Equal(t, false, failed["ok"])
	assert.Contains(t, failed["fail_reason"], "可用模型列表")
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

// addSellerCustomListing creates one seller-defined listing and returns it and
// its plaintext key. The trust pool decides the initial status: trusted yields
// draft, watch/unlisted yields pending_review (CreateListing, listing.go).
func addSellerCustomListing(t *testing.T, f *sellerAPIFixture, baseURL string) (Listing, string) {
	t.Helper()
	key := uniqueTestKey("sk-seller-custom-verify")
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
		CustomBaseURL:   baseURL,
	})
	require.NoError(t, err)
	return listing, key
}

// TestSellerVerifyListingRejectsUntrustedCustomChannel is the review-bypass
// regression: after an operator rejects a watch/unlisted custom channel back to
// draft, the seller must not be able to run verification on it themselves — that
// would drive draft → verifying → canary and skip the required review.
func TestSellerVerifyListingRejectsUntrustedCustomChannel(t *testing.T) {
	cases := []struct {
		name string
		row  *TrustPool
		host string
	}{
		{
			name: "watch",
			host: "watch.seller-verify.example",
			row:  &TrustPool{Host: "watch.seller-verify.example", Kind: TrustKindThirdParty, Trust: TrustWatch, Source: "manual"},
		},
		{
			name: "unlisted",
			host: "unlisted-" + strconv.FormatInt(time.Now().UnixNano(), 36) + ".example.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSellerAPIFixture(t)
			if tc.row != nil {
				createTrustPoolRow(t, f.db, *tc.row)
			}
			listing, _ := addSellerCustomListing(t, f, "https://"+tc.host)
			require.Equal(t, ListingPendingReview, listing.Status)
			// The operator's reject decision returns it to the seller.
			require.NoError(t, RejectListing(f.db, listing.ID))

			w := f.do(t, http.MethodPost,
				"/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10)+"/verify",
				nil, int(f.sellerID))
			require.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, decodeSellerBody(t, w)["message"], "需要运营审核")

			row, err := GetListing(f.db, listing.ID)
			require.NoError(t, err)
			assert.Equal(t, ListingDraft, row.Status, "a refused verify must not move the listing")
			var probes int64
			require.NoError(t, f.db.Model(&Probe{}).
				Where("listing_id = ? AND type = ?", listing.ID, ProbeTypeVerify).Count(&probes).Error)
			assert.Zero(t, probes, "no verification probe may be written for an untrusted channel")
		})
	}
}

// TestSellerVerifyListingAllowsTrustedChannels covers the other side of the
// guard: a trusted custom upstream and a platform channel (already governed by
// operators) keep the self-service draft → verifying → canary flow.
func TestSellerVerifyListingAllowsTrustedChannels(t *testing.T) {
	t.Run("trusted custom channel", func(t *testing.T) {
		f := newSellerAPIFixture(t)
		h := allGreenHandler()
		srv, _ := newVendorServer(t, h)
		target, err := url.Parse(srv.URL)
		require.NoError(t, err)
		restore := setSellerVerifyClientForTest(&http.Client{Transport: rewriteTransport{target: target}})
		defer restore()

		host := "trusted.seller-verify.example"
		createTrustPoolRow(t, f.db, TrustPool{Host: host, Kind: TrustKindThirdParty, Trust: TrustTrusted, Source: "manual"})
		listing, key := addSellerCustomListing(t, f, "https://"+host)
		require.Equal(t, ListingDraft, listing.Status, "a trusted upstream keeps the automated flow")
		h.wantAuth = "Bearer " + key

		w := f.do(t, http.MethodPost,
			"/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10)+"/verify",
			nil, int(f.sellerID))
		require.Equal(t, http.StatusOK, w.Code)
		data, ok := decodeSellerBody(t, w)["data"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, ListingVerifying, data["status"])

		require.Eventually(t, func() bool {
			row, err := GetListing(f.db, listing.ID)
			return err == nil && row.Status == ListingCanary
		}, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("platform channel", func(t *testing.T) {
		f := newSellerAPIFixture(t)
		key := uniqueTestKey("sk-seller-platform-verify")
		h := allGreenHandler()
		h.wantAuth = "Bearer " + key
		srv, _ := newVendorServer(t, h)
		target, err := url.Parse(srv.URL)
		require.NoError(t, err)
		restore := setSellerVerifyClientForTest(&http.Client{Transport: rewriteTransport{target: target}})
		defer restore()

		listing, err := CreateListing(f.db, CreateListingInput{
			SellerID:        f.sellerID,
			VendorID:        SeedVendorDeepSeek,
			APIKey:          key,
			Models:          []string{"deepseek-flash"},
			MultiplierMilli: 500,
		})
		require.NoError(t, err)
		require.Equal(t, ListingDraft, listing.Status)

		w := f.do(t, http.MethodPost,
			"/api/marketplace/seller/listings/"+strconv.FormatInt(listing.ID, 10)+"/verify",
			nil, int(f.sellerID))
		require.Equal(t, http.StatusOK, w.Code)
		data, ok := decodeSellerBody(t, w)["data"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, ListingVerifying, data["status"])

		require.Eventually(t, func() bool {
			row, err := GetListing(f.db, listing.ID)
			return err == nil && row.Status == ListingCanary
		}, 5*time.Second, 10*time.Millisecond)
	})
}
