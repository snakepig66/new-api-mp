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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// Stable error codes the seller console switches on. The message stays
// human-readable English; the code is what drives the duplicate-key
// distinction and the plan-terms prompt in the UI.
const (
	// CodeDuplicateKeyOwn: the key is already listed by the caller. The
	// response also carries listing_id so the console can link to it.
	CodeDuplicateKeyOwn = "DUPLICATE_KEY_OWN"
	// CodeDuplicateKeyOther: the key is listed by another account. The
	// response never carries that seller's identity (§3.5/§2.3).
	CodeDuplicateKeyOther = "DUPLICATE_KEY_OTHER"
	// CodePlanTermsRequired: a plan channel was submitted without
	// plan_terms_accepted=true.
	CodePlanTermsRequired = "PLAN_TERMS_REQUIRED"
)

// sellerVerifyTimeout bounds one background verification. The UI shows a
// "verifying" state for at most this long, so it must stay <= 60s.
const sellerVerifyTimeout = 60 * time.Second

// handleSellerDuplicateKey writes the discriminated duplicate-key response
// and reports whether err was one. The caller's own listing id is exposed so
// the console can deep-link; another seller's id/name never is, but operators
// get an alert line with the fingerprint prefix and both seller ids.
func handleSellerDuplicateKey(c *gin.Context, err error) bool {
	var dup *DuplicateKeyError
	if !errors.As(err, &dup) {
		return false
	}
	if dup.Own {
		common.ApiErrorStatus(c, http.StatusConflict,
			errors.New("this api key is already listed by you"),
			gin.H{"code": CodeDuplicateKeyOwn, "listing_id": dup.ListingID})
		return true
	}
	cur := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	common.SysError(common.LogText(
		"marketplace: duplicate key attempt by seller %d on a key already listed by seller %d (listing %d, fp %s)",
		cur, dup.SellerID, dup.ListingID, dup.FingerprintPrefix))
	common.ApiErrorStatus(c, http.StatusConflict,
		errors.New("this api key is already listed by another account"),
		gin.H{"code": CodeDuplicateKeyOther})
	return true
}

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
	g.POST("/seller/listings/:id/verify", verifySellerListing)
	g.POST("/seller/listings/:id/pause", pauseSellerListing)
	g.POST("/seller/listings/:id/resume", resumeSellerListing)
	g.GET("/seller/balance", getSellerBalance)
	g.POST("/seller/payouts", postSellerPayout)
}

// RegisterSellerOnboardingRoutes is the exported seam for the M9 onboarding
// endpoints and the FRONTEND-REBUILD §5 seller-side console APIs. The router
// seam should mount it on the same UserAuth group as RegisterSellerRoutes.
//
// It covers:
//
//	POST /seller/onboarding       协议（必填）→ 实名 / 支付宝（提现前）
//	GET  /me                      current user + seller progress
//	POST /seller/listings/probe   dry-run key check (no DB write)
//	GET  /seller/listings/:id     detail + recent probes + pause reason
//	GET  /seller/stats            pending/available/income/fee/30-day series
//	GET  /seller/payouts          paginated withdraw history
func RegisterSellerOnboardingRoutes(g *gin.RouterGroup) {
	registerSellerOnboardingRoutes(g)
	registerMeRoutes(g)
}

func registerSellerOnboardingRoutes(g *gin.RouterGroup) {
	g.POST("/seller/onboarding", postSellerOnboarding)
	g.POST("/seller/listings/probe", postSellerListingProbe)
	g.GET("/seller/listings/:id", getSellerListingDetail)
	g.GET("/seller/stats", getSellerStats)
	g.GET("/seller/payouts", listSellerPayouts)
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
	VendorID        string `json:"vendor_id"`
	APIKey          string `json:"api_key"`
	Models          []string `json:"models"`
	MultiplierMilli int64  `json:"multiplier_milli"`
	Concurrency     int    `json:"concurrency"`
	RPM             int    `json:"rpm"`
	DailyCapMicro   int64  `json:"daily_cap_micro"`
	Hours           string `json:"hours"`
	// PlanTermsAccepted is required (true) for plan channels; it is the
	// seller's explicit confirmation that the plan terms allow sharing.
	PlanTermsAccepted bool `json:"plan_terms_accepted"`
	// CustomBaseURL opens a seller-defined third-party channel; VendorID is
	// ignored when set (custom_channel.go).
	CustomBaseURL string `json:"custom_base_url"`
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
	// PauseReason is the stored system pause reason (plan quota, patrol,
	// migration). "manual" is the fallback for a seller-initiated pause.
	PauseReason string `json:"pause_reason,omitempty"`
	// ResumeAt is set for time-boxed pauses (plan quota windows).
	ResumeAt *time.Time `json:"resume_at,omitempty"`
	// Verification is the per-model outcome of the latest verify probe.
	Verification *sellerVerificationView `json:"verification,omitempty"`
}

// sellerVerificationView surfaces the latest verify probe's per-model results
// so both the list and the detail page can show which models failed and why
// (§5). It carries no key material.
type sellerVerificationView struct {
	OK           bool                `json:"ok"`
	FailReason   string              `json:"fail_reason"`
	PromptCache  bool                `json:"prompt_cache_support"`
	LatencyMs    int64               `json:"latency_ms"`
	ModelResults []ModelVerifyResult `json:"model_results"`
	CheckedAt    time.Time           `json:"checked_at"`
}

// probeVerificationView decodes a verify probe's detail. A malformed detail
// still yields the probe's own ok/latency rather than dropping the result.
func probeVerificationView(p Probe) *sellerVerificationView {
	var detail struct {
		FailReason   string              `json:"fail_reason"`
		PromptCache  bool                `json:"prompt_cache"`
		ModelResults []ModelVerifyResult `json:"model_results"`
	}
	_ = common.Unmarshal([]byte(p.Detail), &detail)
	results := detail.ModelResults
	if results == nil {
		results = []ModelVerifyResult{}
	}
	return &sellerVerificationView{
		OK:           p.OK,
		FailReason:   detail.FailReason,
		PromptCache:  detail.PromptCache,
		LatencyMs:    p.LatencyMs,
		ModelResults: results,
		CheckedAt:    p.CreatedAt,
	}
}

// latestVerifyProbe returns the newest verify probe of one listing, if any.
func latestVerifyProbe(db *gorm.DB, listingID int64) *Probe {
	var p Probe
	err := db.Where("listing_id = ? AND type = ?", listingID, ProbeTypeVerify).
		Order("id DESC").Take(&p).Error
	if err != nil {
		return nil
	}
	return &p
}

// latestVerifyProbes maps each listing id to its newest verify probe in one
// query, so the list endpoint stays O(1) queries.
func latestVerifyProbes(db *gorm.DB, listingIDs []int64) map[int64]Probe {
	out := make(map[int64]Probe, len(listingIDs))
	if len(listingIDs) == 0 {
		return out
	}
	var rows []Probe
	if err := db.Where("listing_id IN ? AND type = ?", listingIDs, ProbeTypeVerify).
		Order("id DESC").Find(&rows).Error; err != nil {
		return out
	}
	for _, p := range rows {
		if _, seen := out[p.ListingID]; !seen {
			out[p.ListingID] = p
		}
	}
	return out
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
		PauseReason:      l.PauseReason,
		ResumeAt:         l.ResumeAt,
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
	// 分享对每个账号开放：签完协议（active）即可开挂单，实名与收款账户
	// 不再是挂单前置条件。
	if err := RequireActiveSeller(db, sellerID); err != nil {
		status, msg := sellerGateStatus(err)
		common.ApiErrorStatus(c, status, errors.New(msg))
		return
	}
	var req sellerListingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	in := CreateListingInput{
		SellerID:          sellerID,
		VendorID:          req.VendorID,
		APIKey:            req.APIKey,
		Models:            req.Models,
		MultiplierMilli:   req.MultiplierMilli,
		Concurrency:       req.Concurrency,
		RPM:               req.RPM,
		DailyCapMicro:     req.DailyCapMicro,
		Hours:             req.Hours,
		PlanTermsAccepted: req.PlanTermsAccepted,
		CustomBaseURL:     req.CustomBaseURL,
	}
	listing, err := CreateListing(db, in)
	if err != nil {
		if handleSellerDuplicateKey(c, err) {
			return
		}
		if errors.Is(err, ErrPlanTermsRequired) {
			common.ApiErrorStatus(c, http.StatusBadRequest,
				errors.New("plan channel requires accepting the plan terms"),
				gin.H{"code": CodePlanTermsRequired})
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
	ctx, cancel := context.WithTimeout(context.Background(), sellerVerifyTimeout)
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
	ids := make([]int64, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	latest := latestVerifyProbes(db, ids)
	out := make([]sellerListingView, 0, len(rows))
	for i := range rows {
		view := toSellerListingView(&rows[i])
		if p, ok := latest[rows[i].ID]; ok {
			view.Verification = probeVerificationView(p)
		}
		view.PauseReason = listingPauseReasonForList(db, rows[i])
		out = append(out, view)
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
		if handleSellerDuplicateKey(c, err) {
			return
		}
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		return
	}
	go verifyListingAsync(db, id)
	common.ApiSuccess(c, gin.H{"id": id, "status": ListingVerifying})
}

// verifySellerListing serves POST /seller/listings/:id/verify — re-run the key
// verification for a draft (verification failed) listing. It is the same
// background verifier the create/replace paths use, so the UI can offer
// 「重新检测」 without a state-changing key swap. Only draft listings qualify;
// a listing already verifying returns 409 so a double click cannot queue two
// probes.
func verifySellerListing(c *gin.Context) {
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
	listing, ok := requireOwnListing(c, db, id, sellerID)
	if !ok {
		return
	}
	switch listing.Status {
	case ListingDraft:
		// allowed
	case ListingVerifying:
		common.ApiErrorStatus(c, http.StatusConflict, errors.New("verification is already running"))
		return
	default:
		common.ApiErrorStatus(c, http.StatusBadRequest,
			errors.New("only a draft listing can be re-verified"))
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
	if err := ResumeSellerListing(db, id); err != nil {
		if errors.Is(err, ErrListingSystemPaused) {
			common.ApiErrorStatus(c, http.StatusConflict, ErrListingSystemPaused)
			return
		}
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
	_, pending, err := AccountBalance(db, SellerAccount(sellerID))
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
	// Payouts require an active seller; the payout account itself is checked
	// inside RequestPayout so a seller who has not bound one gets a clear 400.
	if err := RequireActiveSeller(db, sellerID); err != nil {
		status, msg := sellerGateStatus(err)
		common.ApiErrorStatus(c, status, errors.New(msg))
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
		case errors.Is(err, ErrPayoutAccountMissing):
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("payout account is not bound"))
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

// ---------------------------------------------------------------------------
// 开通 (M9) 与 /me (§5 FRONTEND-REBUILD)
// ---------------------------------------------------------------------------

type sellerOnboardingRequest struct {
	AgreementVersion string `json:"agreement_version"`
	RealName         string `json:"real_name"`
	IDCardMasked     string `json:"id_card_masked"`
	AlipayAccount    string `json:"alipay_account"`
}

// sellerIdentityView is the seller block of GET /me and the onboarding
// response. It never carries the alipay account, the ciphertext, the real name
// or the ID number (§2.3/§3.1).
type sellerIdentityView struct {
	Exists   bool           `json:"exists"`
	Status   string         `json:"status"`
	Progress SellerProgress `json:"progress"`
}

func sellerIdentityViewOf(s Seller, exists bool) sellerIdentityView {
	if !exists {
		return sellerIdentityView{
			Exists:   false,
			Progress: SellerProgress{NextStep: "agreement"},
		}
	}
	return sellerIdentityView{Exists: true, Status: s.Status, Progress: s.progress()}
}

// postSellerOnboarding serves POST /seller/onboarding — the §3.1 state machine
// 协议 → 实名 → 支付宝. A partial body advances only the provided steps; the
// response carries the progress and the next step.
func postSellerOnboarding(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	var req sellerOnboardingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	seller, progress, err := OnboardSeller(db, sellerID, SellerOnboardingInput{
		AgreementVersion: req.AgreementVersion,
		RealName:         req.RealName,
		IDCardMasked:     req.IDCardMasked,
		AlipayAccount:    req.AlipayAccount,
	}, time.Now())
	if err != nil {
		status, msg := sellerOnboardingStatus(err)
		common.ApiErrorStatus(c, status, errors.New(msg))
		return
	}
	view := sellerIdentityView{Exists: seller.UserID > 0, Status: seller.Status, Progress: progress}
	common.ApiSuccess(c, gin.H{"seller": view})
}

// ---------------------------------------------------------------------------
// 挂单检测 probe (§5): dry-run, no DB writes.
// ---------------------------------------------------------------------------

type sellerProbeRequest struct {
	VendorID string `json:"vendor_id"`
	APIKey   string `json:"api_key"`
	Models   []string `json:"models"`
	// CustomBaseURL probes a seller-defined upstream instead of a registered
	// channel; VendorID is ignored when set (custom_channel.go).
	CustomBaseURL string `json:"custom_base_url"`
}

// sellerVerifyResources returns the async-verification client and limiter
// under the same lock as setSellerVerifyClientForTest, so probe and background
// verification share one independent §3.5 rate-limit bucket.
func sellerVerifyResources() (*http.Client, *VerifyLimiter) {
	sellerVerifyMu.RLock()
	defer sellerVerifyMu.RUnlock()
	return sellerVerifyClient, sellerVerifyLimiter
}

// postSellerListingProbe serves POST /seller/listings/probe — one dry-run
// verification identical to the async listing check (§3.5) plus the §4
// certified-model discovery: key validity, balance, /models latency,
// prompt-cache support, and the standard model ids this key may actually
// sell.
//
// The authority is the certification intersection, never the request body:
// GET the channel's /models with this key, keep the upstream names it
// exposes, and return the standard model id of every mp_channel_models row
// with verified=true and status=active for the channel. Request models stay
// an optional compatibility filter — a requested model outside that set is
// reported in missing_models instead of being accepted. The paid prompt-cache
// probe names the first certified qualification's upstream model. No
// mp_listings / mp_probes row is written and the key is never echoed.
func postSellerListingProbe(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	var req sellerProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("api_key is required"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	client, limiter := sellerVerifyResources()
	if err := limiter.Acquire(ctx); err != nil {
		common.ApiErrorStatus(c, http.StatusTooManyRequests, errors.New("verification is busy, try again later"))
		return
	}
	defer limiter.Release()

	// Seller-defined upstream: register the channel row (the host must pass
	// the §3.11 egress guard on these dials), then probe the raw URL and
	// intersect the upstream's model names with the platform catalogue.
	if strings.TrimSpace(req.CustomBaseURL) != "" {
		if _, err := ensureCustomChannel(db, sellerID, req.CustomBaseURL, nil); err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, err)
			return
		}
		detected, keyRes, sellable := probeCustomChannelKey(ctx, client, db, req.CustomBaseURL, apiKey)
		coefficients := make(map[string]int64, len(sellable))
		for _, m := range sellable {
			coefficients[m] = 1000
		}
		var balance *int64
		promptCache := false
		if keyRes.KeyValid && len(sellable) > 0 && detected != "" {
			balance = fetchBalanceMicro(ctx, client, detected, apiKey)
			promptCache = probePromptCacheSupport(ctx, client, detected, sellable[0], apiKey)
		}
		missing := make([]string, 0)
		for _, m := range req.Models {
			if !containsString(sellable, m) && !containsString(missing, m) {
				missing = append(missing, m)
			}
		}
		probeOK := keyRes.KeyValid && len(sellable) > 0 && len(missing) == 0
		failReason := stripKey(keyRes.FailReason, apiKey)
		if keyRes.KeyValid && len(sellable) == 0 {
			failReason = "上游没有平台在售的模型（模型名需要与平台一致）"
		}
		common.ApiSuccess(c, gin.H{
			"ok":              probeOK,
			"models":          sellable,
			"verified_models": sellable,
			"coefficients":    coefficients,
			"missing_models":  missing,
			"latency_ms":      keyRes.LatencyMs,
			"prompt_cache":    promptCache,
			"balance_micro":   balance,
			"key_valid":       keyRes.KeyValid,
			"fail_reason":     failReason,
		})
		return
	}
	vendor, ok := lookupVendorForDB(db, req.VendorID)
	if !ok {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("vendor is not whitelisted"))
		return
	}
	// A custom channel is private to its owner; probing someone else's slug
	// would leak their upstream address.
	if isCustomChannelSlug(req.VendorID) {
		if owner, owned := customChannelOwner(req.VendorID); !owned || owner != sellerID {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("vendor is not whitelisted"))
			return
		}
	}

	// §4 channel path: which upstream names can this key see on the channel?
	keyRes := VerifyChannelKey(ctx, client, vendor, apiKey)

	// §4 model path: intersect the channel's verified+active qualifications
	// with the upstream names the key exposes. The result is standard model
	// ids, because the seller console only knows the platform catalogue.
	certified := certifiedChannelModelsFor(db, vendor.ID)
	available := make([]string, 0, len(certified))
	// coefficients carries the channel's price coefficient per standard model
	// (1000 = official price). The wizard multiplies it with the seller's
	// multiplier for an accurate live buyer-price preview.
	coefficients := make(map[string]int64, len(certified))
	probeModel := ""
	for _, cm := range certified {
		if !slices.Contains(keyRes.Models, cm.UpstreamModel) {
			continue
		}
		if !slices.Contains(available, cm.Model) {
			available = append(available, cm.Model)
			coefficient := cm.CoefficientMilli
			if coefficient <= 0 {
				coefficient = 1000
			}
			coefficients[cm.Model] = coefficient
		}
		if probeModel == "" {
			probeModel = cm.UpstreamModel
		}
	}

	// Balance and prompt-cache support stay informational. They are only
	// requested once the key listed at least one model: an invalid/empty key
	// cannot turn OK, so spending its paid probe would be waste.
	var balance *int64
	promptCache := false
	if keyRes.OK {
		base := VendorOpenAIBase(vendor)
		balance = fetchBalanceMicro(ctx, client, base, apiKey)
		if probeModel != "" {
			promptCache = probePromptCacheSupport(ctx, client, base, probeModel, apiKey)
		}
	}

	missing := make([]string, 0)
	for _, m := range req.Models {
		if !slices.Contains(available, m) && !slices.Contains(missing, m) {
			missing = append(missing, m)
		}
	}
	probeOK := keyRes.OK && len(available) > 0 && len(missing) == 0
	failReason := stripKey(keyRes.FailReason, apiKey)
	if keyRes.KeyValid && len(available) == 0 {
		failReason = "no certified models available for this key"
	}

	common.ApiSuccess(c, gin.H{
		"ok":              probeOK,
		"models":          available,
		"verified_models": available, // explicit alias for the seller wizard
		"coefficients":    coefficients,
		"missing_models":  missing,
		"latency_ms":      keyRes.LatencyMs,
		"prompt_cache":    promptCache,
		"balance_micro":   balance, // null when the vendor has no balance endpoint
		"key_valid":       keyRes.KeyValid,
		"fail_reason":     failReason,
	})
}

// certifiedChannelModelsFor lists a channel's verified+active qualifications
// ordered by upstream name. It is the authority for what a seller's key may
// list; the request body never grants a model by itself.
func certifiedChannelModelsFor(db *gorm.DB, channelSlug string) []ChannelModel {
	out := make([]ChannelModel, 0)
	if db == nil {
		return out
	}
	if err := db.Where("channel_slug = ? AND verified = ? AND status = ?",
		strings.TrimSpace(channelSlug), true, PoolStatusActive).
		Order("upstream_model ASC, id ASC").Find(&out).Error; err != nil {
		return []ChannelModel{}
	}
	return out
}

// ---------------------------------------------------------------------------
// 挂单详情 (§5): detail + last 10 probes + pause reason.
// ---------------------------------------------------------------------------

type sellerProbeView struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	OK        bool      `json:"ok"`
	LatencyMs int64     `json:"latency_ms"`
	Detail    any       `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

type sellerListingDetail struct {
	sellerListingView
	Probes []sellerProbeView `json:"probes"`
}

// probeDetailValue renders a probe's JSON detail to the console; a malformed
// detail is returned as a plain string rather than dropping it.
func probeDetailValue(detail string) any {
	if detail == "" {
		return nil
	}
	var v any
	if err := common.Unmarshal([]byte(detail), &v); err != nil {
		return detail
	}
	return v
}

// probeFailReason extracts fail_reason (verify/patrol) or reason (auto-pause)
// from a probe detail JSON string.
func probeFailReason(detail string) string {
	var m map[string]any
	if err := common.Unmarshal([]byte(detail), &m); err != nil {
		return ""
	}
	if s, ok := m["fail_reason"].(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	if s, ok := m["reason"].(string); ok {
		return s
	}
	return ""
}

// listingPauseReason derives the paused listing's reason from the stored pause
// reason first (plan quota / patrol / migration), then its most recent failed
// probe, falling back to "manual" (卖家手动暂停).
func listingPauseReason(listing Listing, probes []Probe) string {
	if listing.Status != ListingPaused {
		return ""
	}
	if reason := strings.TrimSpace(listing.PauseReason); reason != "" {
		return reason
	}
	for _, p := range probes {
		if !p.OK {
			if reason := probeFailReason(p.Detail); reason != "" {
				return reason
			}
		}
	}
	return "manual"
}

// listingPauseReasonForList derives the paused reason for the list response.
// Unlike the detail path it queries the latest failed probe itself, because
// the list endpoint does not load probes.
func listingPauseReasonForList(db *gorm.DB, listing Listing) string {
	if listing.Status != ListingPaused {
		return ""
	}
	if reason := strings.TrimSpace(listing.PauseReason); reason != "" {
		return reason
	}
	var p Probe
	if err := db.Where("listing_id = ? AND ok = ?", listing.ID, false).
		Order("id DESC").Take(&p).Error; err == nil {
		if reason := probeFailReason(p.Detail); reason != "" {
			return reason
		}
	}
	return "manual"
}

// getSellerListingDetail serves GET /seller/listings/:id — own listing detail,
// the last 10 probes and the pause reason (§5 挂单详情).
func getSellerListingDetail(c *gin.Context) {
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
	listing, ok := requireOwnListing(c, db, id, sellerID)
	if !ok {
		return
	}
	var probes []Probe
	if err := db.Where("listing_id = ?", id).Order("id DESC").Limit(10).Find(&probes).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query probes"))
		return
	}
	views := make([]sellerProbeView, 0, len(probes))
	for _, p := range probes {
		views = append(views, sellerProbeView{
			ID:        p.ID,
			Type:      p.Type,
			OK:        p.OK,
			LatencyMs: p.LatencyMs,
			Detail:    probeDetailValue(p.Detail),
			CreatedAt: p.CreatedAt,
		})
	}
	view := toSellerListingView(&listing)
	view.PauseReason = listingPauseReason(listing, probes)
	// Look the newest verify probe up independently of the recent-probe
	// window: a run of patrol probes must not hide the last per-model verify
	// result the seller needs to fix a failed draft.
	if latest := latestVerifyProbe(db, id); latest != nil {
		view.Verification = probeVerificationView(*latest)
	}
	common.ApiSuccess(c, sellerListingDetail{
		sellerListingView: view,
		Probes:            views,
	})
}

// ---------------------------------------------------------------------------
// 收益统计 (§5): 待结算 / 可提现 / 本月收入 / 平台费 / 近 30 天按日收入.
// ---------------------------------------------------------------------------

// sellerDailyIncome is one day of the 30-day series.
type sellerDailyIncome struct {
	Date        string `json:"date"` // YYYY-MM-DD Asia/Shanghai
	IncomeMicro int64  `json:"income_micro"`
}

// sumSellerLedger sums one seller's ledger entries attributed through
// mp_requests. Attribution by request (not by account string) keeps the stats
// correct regardless of the buyer/seller account split of §3.9.
func sumSellerLedger(db *gorm.DB, sellerID int64, entryTypes []string, extra string, args ...any) (int64, error) {
	where := "r.seller_id = ? AND l.entry_type IN ?"
	qargs := []any{sellerID, entryTypes}
	if extra != "" {
		where += " AND " + extra
		qargs = append(qargs, args...)
	}
	var sum int64
	err := db.Table("mp_ledger AS l").
		Joins("JOIN mp_requests AS r ON r.request_id = l.request_id").
		Where(where, qargs...).
		Select("COALESCE(SUM(l.amount_micro), 0)").
		Scan(&sum).Error
	return sum, err
}

// getSellerStats serves GET /seller/stats (§5 分享者概览).
func getSellerStats(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	now := time.Now()
	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to load timezone"))
		return
	}
	localNow := now.In(loc)
	monthStart := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, loc)
	seriesStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -29)

	pending, err := sumSellerLedger(db, sellerID, []string{EntrySellerCredit}, "l.status = ?", LedgerPending)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query pending balance"))
		return
	}
	available, err := AvailableBalance(db, sellerID)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query available balance"))
		return
	}
	monthIncome, err := sumSellerLedger(db, sellerID, []string{EntrySellerCredit},
		"l.status <> ? AND l.created_at >= ?", LedgerReversed, monthStart)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query monthly income"))
		return
	}
	fees, err := sumSellerLedger(db, sellerID, []string{EntryPlatformFee, EntryFeeReversal}, "")
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query platform fees"))
		return
	}

	type dailyRow struct {
		CreatedAt   time.Time
		AmountMicro int64
	}
	var rows []dailyRow
	if err := db.Table("mp_ledger AS l").
		Select("l.created_at AS created_at, l.amount_micro AS amount_micro").
		Joins("JOIN mp_requests AS r ON r.request_id = l.request_id").
		Where("r.seller_id = ? AND l.entry_type = ? AND l.status <> ? AND l.created_at >= ?",
			sellerID, EntrySellerCredit, LedgerReversed, seriesStart).
		Scan(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query daily income"))
		return
	}
	byDay := make(map[string]int64, 30)
	for _, r := range rows {
		byDay[r.CreatedAt.In(loc).Format("2006-01-02")] += r.AmountMicro
	}
	series := make([]sellerDailyIncome, 0, 30)
	for i := 0; i < 30; i++ {
		day := seriesStart.AddDate(0, 0, i).Format("2006-01-02")
		series = append(series, sellerDailyIncome{Date: day, IncomeMicro: byDay[day]})
	}

	common.ApiSuccess(c, gin.H{
		"pending_micro":            pending,
		"available_micro":          int64(available),
		"month_income_micro":       monthIncome,
		"platform_fee_total_micro": fees,
		"daily":                    series,
		"as_of":                    now,
	})
}

// ---------------------------------------------------------------------------
// 提现历史 (§5)
// ---------------------------------------------------------------------------

// listSellerPayouts serves GET /seller/payouts?page=&page_size= — the caller's
// withdrawal history, newest first, in the console PageInfo envelope.
func listSellerPayouts(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	page, pageSize := parseRecordPageQuery(c)
	var total int64
	if err := db.Model(&Payout{}).Where("seller_id = ?", sellerID).Count(&total).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query payouts"))
		return
	}
	var rows []Payout
	if err := db.Where("seller_id = ?", sellerID).
		Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query payouts"))
		return
	}
	pageInfo := &common.PageInfo{Page: page, PageSize: pageSize}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(rows)
	common.ApiSuccess(c, pageInfo)
}
