package marketplace

// api_seller.go — 分享者自助 API (§5 阶段 6): 挂单创建/列表/改倍率/换 key/
// 暂停恢复, 余额与提现. The route group these handlers hang on is expected to
// carry middleware.UserAuth() (wired by the router seam, §4.4); every handler
// additionally verifies the listing belongs to the logged-in seller, answering
// a uniform 404 for foreign or missing listing ids so ownership can never be
// probed (§3.5). No response from this file ever carries key material (§2.3) —
// listings show only the fingerprint's first 8 hex chars.
//
// These are web-console APIs: they answer in the upstream console envelope
// (common.ApiSuccess / common.ApiErrorStatus), like api_records.go.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// RegisterSellerRoutes is the exported one-line wrapper the router seam calls;
// the marketplace package cannot import middleware (import cycle), so the
// UserAuth group is built by the caller and passed in.
func RegisterSellerRoutes(g *gin.RouterGroup) { registerSellerRoutes(g) }

// registerSellerRoutes mounts the §5 阶段 6 seller endpoints on g. g is assumed
// to already carry middleware.UserAuth() (and to be gated on Enabled() by the
// caller). Route wiring into RegisterRoutes is done centrally in api.go.
func registerSellerRoutes(g *gin.RouterGroup) {
	g.POST("/seller/listings", postSellerListing)
	g.GET("/seller/listings", listSellerListings)
	g.POST("/seller/listings/:id/multiplier", updateSellerMultiplier)
	g.POST("/seller/listings/:id/key", replaceSellerKey)
	g.POST("/seller/listings/:id/pause", pauseSellerListing)
	g.POST("/seller/listings/:id/resume", resumeSellerListing)
	g.GET("/seller/balance", getSellerBalance)
	g.POST("/seller/payouts", postSellerPayout)
}

// sellerDB returns the marketplace primary database handle, answering 500 when
// the module state is not initialized.
func sellerDB(c *gin.Context) *gorm.DB {
	db, _ := relayState()
	if db == nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
		return nil
	}
	return db
}

// sellerUserID returns the logged-in seller's user id (UserAuth has already
// run on the group); <= 0 is answered 401 defensively.
func sellerUserID(c *gin.Context) (int64, bool) {
	userID := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return 0, false
	}
	return userID, true
}

// sellerIDParam parses the :id path parameter as a positive int64.
func sellerIDParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid listing id"))
		return 0, false
	}
	return id, true
}

// requireOwnListing loads the listing and confirms it belongs to the caller.
// A foreign id and an unknown id get the identical 404 so the response reveals
// nothing about other sellers' listings (§3.5 ownership 校验失败一律 404).
func requireOwnListing(c *gin.Context, db *gorm.DB, id, sellerID int64) (Listing, bool) {
	listing, err := GetListingForSeller(db, id, sellerID)
	if err != nil {
		if errors.Is(err, ErrListingNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("listing not found"))
			return Listing{}, false
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query listing"))
		return Listing{}, false
	}
	return listing, true
}

// ---------------------------------------------------------------------------
// 挂单 (§3.5)
// ---------------------------------------------------------------------------

// sellerListingRequest is the POST /seller/listings body — the seller-facing
// subset of CreateListingInput (seller_id is taken from the session).
type sellerListingRequest struct {
	VendorID        string   `json:"vendor_id"`
	APIKey          string   `json:"api_key"`
	Models          []string `json:"models"`
	MultiplierMilli int64    `json:"multiplier_milli"`
	Concurrency     int      `json:"concurrency"`
	RPM             int      `json:"rpm"`
	DailyCapMicro   int64    `json:"daily_cap_micro"`
	Hours           string   `json:"hours"`
}

// sellerListingView is one row of the seller's own listing list. It carries
// only the fingerprint's first 8 hex chars — never the ciphertext or the full
// fingerprint (§2.3).
type sellerListingView struct {
	ID               int64     `json:"id"`
	Vendor           string    `json:"vendor"`
	Models           []string  `json:"models"`
	MultiplierMilli  int64     `json:"multiplier_milli"`
	ConcurrencyLimit int       `json:"concurrency_limit"`
	RPMLimit         int       `json:"rpm_limit"`
	DailyCapMicro    int64     `json:"daily_cap_micro"`
	Hours            string    `json:"hours"`
	Status           string    `json:"status"`
	KeyFingerprint8  string    `json:"key_fingerprint8"`
	CreatedAt        time.Time `json:"created_at"`
}

func toSellerListingView(l *Listing) sellerListingView {
	fp8 := ""
	if len(l.KeyFingerprint) >= 8 {
		fp8 = l.KeyFingerprint[:8]
	}
	return sellerListingView{
		ID:               l.ID,
		Vendor:           l.Vendor,
		Models:           l.Models,
		MultiplierMilli:  l.MultiplierMilli,
		ConcurrencyLimit: l.ConcurrencyLimit,
		RPMLimit:         l.RPMLimit,
		DailyCapMicro:    l.DailyCapMicro,
		Hours:            l.Hours,
		Status:           l.Status,
		KeyFingerprint8:  fp8,
		CreatedAt:        l.CreatedAt,
	}
}

// postSellerListing serves POST /seller/listings — create a listing (draft)
// and asynchronously trigger verification (§3.5 挂单向导). The response
// returns the listing id and status immediately; verification runs in the
// background through the independent VerifyLimiter.
func postSellerListing(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	var req sellerListingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	in := CreateListingInput{
		SellerID:        sellerID,
		VendorID:        req.VendorID,
		APIKey:          req.APIKey,
		Models:          req.Models,
		MultiplierMilli: req.MultiplierMilli,
		Concurrency:     req.Concurrency,
		RPM:             req.RPM,
		DailyCapMicro:   req.DailyCapMicro,
		Hours:           req.Hours,
	}
	listing, err := CreateListing(db, in)
	if err != nil {
		if errors.Is(err, ErrDuplicateKey) {
			common.ApiErrorStatus(c, http.StatusConflict, errors.New("this api key is already listed"))
			return
		}
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		return
	}
	// Asynchronous verification: the seller gets the draft id back at once and
	// verification flips it to verifying → canary (or back to draft) in the
	// background. The goroutine is recovered and logged so a panic never takes
	// the request path down.
	go verifyListingAsync(db, listing.ID)
	common.ApiSuccess(c, gin.H{"id": listing.ID, "status": listing.Status})
}

// sellerVerifyClient is the http.Client used for the async verification
// triggered by listing create / key replace. It defaults to the egress-checked
// client; tests swap it for an httptest mock via setSellerVerifyClientForTest.
var (
	sellerVerifyMu       sync.RWMutex
	sellerVerifyClient   = EgressHTTPClient()
	sellerVerifyLimiter  = NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)
	sellerVerifyInterval = 5 * time.Millisecond
)

// setSellerVerifyClientForTest swaps the async-verification client and limiter
// and returns a restore function. It exists ONLY for tests: their mock vendors
// are httptest servers on 127.0.0.1, which the §3.11 egress guard must keep
// refusing in production. Never call from non-test code.
func setSellerVerifyClientForTest(client *http.Client) (restore func()) {
	sellerVerifyMu.Lock()
	oldClient, oldLimiter, oldInterval := sellerVerifyClient, sellerVerifyLimiter, sellerVerifyInterval
	sellerVerifyClient = client
	sellerVerifyLimiter = NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)
	sellerVerifyInterval = 5 * time.Millisecond
	sellerVerifyMu.Unlock()
	return func() {
		sellerVerifyMu.Lock()
		sellerVerifyClient, sellerVerifyLimiter, sellerVerifyInterval = oldClient, oldLimiter, oldInterval
		sellerVerifyMu.Unlock()
	}
}

// verifyListingAsync runs VerifyListing in the background with a 60s bound and
// the egress-checked client, recovering panics and logging failures (§3.5).
func verifyListingAsync(db *gorm.DB, listingID int64) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(common.LogText("marketplace: verify listing %d panicked: %v", listingID, r))
		}
	}()
	sellerVerifyMu.RLock()
	client, limiter, interval := sellerVerifyClient, sellerVerifyLimiter, sellerVerifyInterval
	sellerVerifyMu.RUnlock()
	// Small delay so the create request returns before the goroutine flips the
	// status; the response already carries the draft id and status.
	time.Sleep(interval)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := VerifyListing(ctx, db, client, limiter, listingID)
	if err != nil {
		common.SysError(common.LogText("marketplace: verify listing %d: %s", listingID, err.Error()))
		return
	}
	if !res.OK {
		common.SysLog(common.LogText("marketplace: listing %d verification failed: %s", listingID, res.FailReason))
	}
}

// listSellerListings serves GET /seller/listings — the seller's own listings,
// without ciphertext or the full fingerprint.
func listSellerListings(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	rows, err := ListListingsBySeller(db, sellerID)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query listings"))
		return
	}
	out := make([]sellerListingView, 0, len(rows))
	for i := range rows {
		out = append(out, toSellerListingView(&rows[i]))
	}
	common.ApiSuccess(c, out)
}

// updateSellerMultiplier serves POST /seller/listings/:id/multiplier
// {multiplier_milli} — change the 倍率 (ownership-checked). UpdateMultiplier
// enforces the 10-minute interval and the 1..1000 range; errors map to 400
// with the semantic message.
func updateSellerMultiplier(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	id, ok := sellerIDParam(c)
	if !ok {
		return
	}
	if _, ok := requireOwnListing(c, db, id, sellerID); !ok {
		return
	}
	var req struct {
		MultiplierMilli int64 `json:"multiplier_milli"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if err := UpdateMultiplier(db, id, req.MultiplierMilli, time.Now()); err != nil {
		if errors.Is(err, ErrMultiplierTooSoon) {
			common.ApiErrorStatus(c, http.StatusConflict, errors.New("multiplier was changed less than 10 minutes ago"))
			return
		}
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		return
	}
	common.ApiSuccess(c, gin.H{"id": id, "multiplier_milli": req.MultiplierMilli})
}

// replaceSellerKey serves POST /seller/listings/:id/key {api_key} — swap in a
// new key (ownership-checked). ReplaceKey re-verifies: the listing drops back
// to verifying, and verification is re-triggered in the background.
func replaceSellerKey(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	id, ok := sellerIDParam(c)
	if !ok {
		return
	}
	if _, ok := requireOwnListing(c, db, id, sellerID); !ok {
		return
	}
	var req struct {
		APIKey string `json:"api_key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if err := ReplaceKey(db, id, req.APIKey); err != nil {
		if errors.Is(err, ErrDuplicateKey) {
			common.ApiErrorStatus(c, http.StatusConflict, errors.New("this api key is already listed"))
			return
		}
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		return
	}
	go verifyListingAsync(db, id)
	common.ApiSuccess(c, gin.H{"id": id, "status": ListingVerifying})
}

// pauseSellerListing serves POST /seller/listings/:id/pause — live → paused
// (ownership-checked).
func pauseSellerListing(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	id, ok := sellerIDParam(c)
	if !ok {
		return
	}
	if _, ok := requireOwnListing(c, db, id, sellerID); !ok {
		return
	}
	if err := PauseListing(db, id); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		return
	}
	common.ApiSuccess(c, gin.H{"id": id, "status": ListingPaused})
}

// resumeSellerListing serves POST /seller/listings/:id/resume — paused → live
// (ownership-checked).
func resumeSellerListing(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	id, ok := sellerIDParam(c)
	if !ok {
		return
	}
	if _, ok := requireOwnListing(c, db, id, sellerID); !ok {
		return
	}
	if err := ResumeListing(db, id); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		return
	}
	common.ApiSuccess(c, gin.H{"id": id, "status": ListingLive})
}

// ---------------------------------------------------------------------------
// 余额与提现 (§3.9)
// ---------------------------------------------------------------------------

// getSellerBalance serves GET /seller/balance — the seller's available and
// pending balances (from the ledger) plus the seller's own payout list.
func getSellerBalance(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	available, err := AvailableBalance(db, sellerID)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query available balance"))
		return
	}
	_, pending, err := AccountBalance(db, UserAccount(sellerID))
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query balance"))
		return
	}
	var payouts []Payout
	if err := db.Where("seller_id = ?", sellerID).Order("id DESC").Find(&payouts).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query payouts"))
		return
	}
	common.ApiSuccess(c, gin.H{
		"available_micro": int64(available),
		"pending_micro":   int64(pending),
		"payouts":         payouts,
	})
}

// postSellerPayout serves POST /seller/payouts {amount_micro, idempotency_key}
// — request a withdrawal (§3.9). Errors map to 400/409 with semantic messages.
func postSellerPayout(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	var req struct {
		AmountMicro    int64  `json:"amount_micro"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if req.IdempotencyKey == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("idempotency_key is required"))
		return
	}
	payout, err := RequestPayout(db, sellerID, req.AmountMicro, req.IdempotencyKey, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, ErrPayoutBelowMinimum):
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("payout amount is below the minimum"))
		case errors.Is(err, ErrPayoutTooFrequent):
			common.ApiErrorStatus(c, http.StatusConflict, errors.New("payouts are limited to once per week"))
		case errors.Is(err, ErrPayoutInsufficient):
			common.ApiErrorStatus(c, http.StatusConflict, errors.New("insufficient available balance"))
		case errors.Is(err, ErrPayoutDuplicate):
			common.ApiErrorStatus(c, http.StatusConflict, errors.New("a payout with this idempotency key already exists"))
		default:
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		}
		return
	}
	common.ApiSuccess(c, gin.H{"id": payout.ID, "status": payout.Status, "amount_micro": payout.AmountMicro})
}
