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
	"fmt"
	"net/http"
	"net/url"
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
	// CodeKeyBannedDeleted: the key belonged to a listing that was banned and
	// later deleted. The ban follows the fingerprint.
	CodeKeyBannedDeleted = "KEY_BANNED_DELETED"
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
	// Common addresses come from the trust pool's trusted rows so the wizard can
	// prefill an address the operator already certified. The handler lives in
	// api_vendors.go next to the catalogue it describes.
	g.GET("/seller/common-addresses", listSellerCommonAddresses)
	// Static route, registered before the :id parameter route on purpose: gin
	// matches static segments first, so /listings/health never parses "health"
	// as a listing id.
	g.GET("/seller/listings/health", getSellerListingsHealth)
	g.GET("/seller/multiplier-bounds", getSellerMultiplierBounds)
	// Static batch path before the :id parameter routes.
	g.POST("/seller/listings/batch-models", batchSellerListingModels)
	g.POST("/seller/listings/:id/multiplier", updateSellerMultiplier)
	// P4: per-model multiplier on one listing (PUT keeps the create/update
	// distinction), group listing and group management.
	g.PUT("/seller/listings/:id/models/:model/multiplier", setSellerModelMultiplier)
	g.GET("/seller/listings/:id/models", getSellerListingModels)
	g.PUT("/seller/listings/:id/models", batchSellerListingModelState)
	g.PUT("/seller/listings/:id/models/:model", setSellerModelMultiplier)
	g.POST("/seller/listings/:id/models/restore", restoreSellerListingModels)
	g.GET("/seller/groups", listSellerListingGroups)
	g.PUT("/seller/groups/:id", updateSellerListingGroup)
	g.POST("/seller/listings/:id/key", replaceSellerKey)
	g.POST("/seller/listings/:id/verify", verifySellerListing)
	g.POST("/seller/listings/:id/pause", pauseSellerListing)
	g.POST("/seller/listings/:id/resume", resumeSellerListing)
	g.DELETE("/seller/listings/:id", deleteSellerListing)
	g.PUT("/seller/listings/:id", updateSellerListingSettings)
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
	g.GET("/seller/vendors", listSellerVendors)
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
	VendorID        string   `json:"vendor_id"`
	APIKey          string   `json:"api_key"`
	Models          []string `json:"models"`
	MultiplierMilli int64    `json:"multiplier_milli"`
	Concurrency     int      `json:"concurrency"`
	RPM             int      `json:"rpm"`
	DailyCapMicro   int64    `json:"daily_cap_micro"`
	Hours           string   `json:"hours"`
	Label           string   `json:"label"`
	// PlanTermsAccepted is required (true) for plan channels; it is the
	// seller's explicit confirmation that the plan terms allow sharing.
	PlanTermsAccepted bool `json:"plan_terms_accepted"`
	// CustomBaseURL opens a seller-defined third-party channel; VendorID is
	// ignored when set (custom_channel.go).
	CustomBaseURL string `json:"custom_base_url"`
	// P4 group settings, used only when this request creates the group.
	KeyStrategy string `json:"key_strategy"`
	Affinity    *bool  `json:"affinity_enabled"`
	Weight      int    `json:"weight"`
}

// sellerListingView is one row of the seller's own listing list. It carries
// only the fingerprint's first 8 hex chars — never the ciphertext or the full
// fingerprint (§2.3).
type sellerListingView struct {
	ID                     int64     `json:"id"`
	Vendor                 string    `json:"vendor"`
	Label                  string    `json:"label"`
	Models                 []string  `json:"models"`
	ModelCount             int       `json:"model_count"`
	EnabledModelCount      int       `json:"enabled_model_count"`
	AutoDisabledModelCount int       `json:"auto_disabled_model_count"`
	MultiplierMinMilli     int64     `json:"multiplier_min_milli"`
	MultiplierMaxMilli     int64     `json:"multiplier_max_milli"`
	MultiplierMilli        int64     `json:"multiplier_milli"`
	ConcurrencyLimit       int       `json:"concurrency_limit"`
	RPMLimit               int       `json:"rpm_limit"`
	DailyCapMicro          int64     `json:"daily_cap_micro"`
	Hours                  string    `json:"hours"`
	Status                 string    `json:"status"`
	KeyFingerprint8        string    `json:"key_fingerprint8"`
	CreatedAt              time.Time `json:"created_at"`
	// P4: the group this listing belongs to and the key's relative weight.
	GroupID int64 `json:"group_id"`
	Weight  int   `json:"weight"`
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

// applyListingAggregates fills the list-row summary fields (model counts and
// effective multiplier band) from the batch-loaded overrides.
func applyListingAggregates(view *sellerListingView, listing Listing, overrides listingModelOverrides) {
	enabled, autoDisabled, minMilli, maxMilli := overrides.aggregate(listing)
	view.ModelCount = len(listing.Models)
	view.EnabledModelCount = enabled
	view.AutoDisabledModelCount = autoDisabled
	view.MultiplierMinMilli = minMilli
	view.MultiplierMaxMilli = maxMilli
}

func toSellerListingView(l *Listing) sellerListingView {
	fp8 := ""
	if len(l.KeyFingerprint) >= 8 {
		fp8 = l.KeyFingerprint[:8]
	}
	return sellerListingView{
		ID:               l.ID,
		Vendor:           l.Vendor,
		Label:            l.Label,
		Models:           l.Models,
		MultiplierMilli:  l.MultiplierMilli,
		ConcurrencyLimit: l.ConcurrencyLimit,
		RPMLimit:         l.RPMLimit,
		DailyCapMicro:    l.DailyCapMicro,
		Hours:            l.Hours,
		Status:           l.Status,
		KeyFingerprint8:  fp8,
		CreatedAt:        l.CreatedAt,
		GroupID:          l.GroupID,
		Weight:           l.Weight,
		PauseReason:      l.PauseReason,
		ResumeAt:         l.ResumeAt,
	}
}

// postSellerListing serves POST /seller/listings — create a listing (draft,
// or pending_review for an untrusted seller-defined upstream) and, for a
// draft, asynchronously trigger verification (§3.5 挂单向导). The response
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
		Label:             req.Label,
		PlanTermsAccepted: req.PlanTermsAccepted,
		CustomBaseURL:     req.CustomBaseURL,
		KeyStrategy:       req.KeyStrategy,
		Affinity:          req.Affinity,
		Weight:            req.Weight,
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
		if errors.Is(err, ErrKeyBannedDeleted) {
			common.ApiErrorStatus(c, http.StatusForbidden, err,
				gin.H{"code": CodeKeyBannedDeleted})
			return
		}
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		return
	}
	// Asynchronous verification: the seller gets the draft id back at once and
	// verification flips it to verifying → canary (or back to draft) in the
	// background. The goroutine is recovered and logged so a panic never takes
	// the request path down.
	//
	// A seller-defined upstream the trust pool does not trust is parked in
	// pending_review instead of draft: VerifyListing refuses any other start
	// state, so starting it here would only write an ErrInvalidTransition log
	// line. It stays untouched until an operator approves it (api_admin.go).
	if listing.Status == ListingDraft {
		go verifyListingAsync(db, listing.ID)
	}
	common.ApiSuccess(c, gin.H{"id": listing.ID, "status": listing.Status})
}

// sellerVerifyClient is the http.Client used for the async verification
// triggered by listing create / key replace. It defaults to the verify egress
// client: dial-time public-IP validation always runs, but whitelist membership
// does not, so a seller-submitted host can be verified before its listing is
// approved into the relay whitelist (§3). Tests swap it for an httptest mock
// via setSellerVerifyClientForTest.
var (
	sellerVerifyMu       sync.RWMutex
	sellerVerifyClient   = EgressVerifyHTTPClient()
	sellerVerifyLimiter  = NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)
	sellerVerifyInterval = 5 * time.Millisecond
)

// setSellerVerifyClientForTest swaps the async-verification client and limiter
// and returns a restore function. It exists ONLY for tests: their mock vendors
// are httptest servers on 127.0.0.1, which the §3.11 dial guard must keep
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
	// A verified listing becomes canary, which is what admits a seller-defined
	// channel's host to the relay whitelist (vendors.go). Reload now instead of
	// waiting out the registry TTL; the reload is in-memory only.
	refreshVendorRegistry(true)
	resetBuyerModelVisibilityCache()
	if !res.OK {
		common.SysLog(common.LogText("marketplace: listing %d verification failed: %s", listingID, res.FailReason))
	}
}

// verifyApprovedListingAsync runs the standard verification for a listing the
// operator just approved. The approval edge records pending_review → verifying
// for the console and the audit trail, but VerifyListing owns the
// draft→verifying edge itself and refuses any other start state (fail-closed),
// so this steps the row back to draft and lets the single verifier drive it.
// A row another operator action has already moved on (banned, paused) is left
// alone.
func verifyApprovedListingAsync(db *gorm.DB, listingID int64) {
	row, err := GetListing(db, listingID)
	if err != nil {
		common.SysError(common.LogText("marketplace: verify approved listing %d: %s", listingID, err.Error()))
		return
	}
	if row.Status != ListingVerifying {
		return
	}
	if err := revertVerificationToDraft(db, listingID); err != nil {
		common.SysError(common.LogText("marketplace: verify approved listing %d: %s", listingID, err.Error()))
		return
	}
	verifyListingAsync(db, listingID)
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
	overrides := loadListingModelOverrides(db, ids, time.Now())
	out := make([]sellerListingView, 0, len(rows))
	for i := range rows {
		view := toSellerListingView(&rows[i])
		applyListingAggregates(&view, rows[i], overrides)
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
		if errors.Is(err, ErrInvalidTransition) {
			common.ApiErrorStatus(c, http.StatusConflict, errors.New(err.Error()))
			return
		}
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		return
	}
	common.ApiSuccess(c, gin.H{"id": id, "multiplier_milli": req.MultiplierMilli})
}

// sellerModelStateRequest is shared by the single-model, per-listing batch and
// cross-listing batch endpoints. Nil pointers mean "leave unchanged".
type sellerModelStateRequest struct {
	Enabled         *bool    `json:"enabled"`
	MultiplierMilli *int64   `json:"multiplier_milli"`
	Models          []string `json:"models"`
	ListingIDs      []int64  `json:"listing_ids"`
}

func (r sellerModelStateRequest) input() ModelStateInput {
	return ModelStateInput{Enabled: r.Enabled, MultiplierMilli: r.MultiplierMilli}
}

func (r sellerModelStateRequest) hasChange() bool {
	return r.Enabled != nil || r.MultiplierMilli != nil
}

func listingModelSummary(row ListingModel) gin.H {
	return gin.H{
		"model":                    row.Model,
		"enabled":                  row.Enabled,
		"multiplier_milli":         row.MultiplierMilli,
		"pending_multiplier_milli": row.PendingMultiplierMilli,
		"pending_effective_at":     row.PendingEffectiveAt,
		"auto_disabled":            row.AutoDisabled,
		"disabled_reason":          row.DisabledReason,
	}
}

// writeModelChangeError maps a rejected model change to the console codes. A
// batch rejection is all-or-nothing: the response carries every row's reason.
func writeModelChangeError(c *gin.Context, err error) {
	var batch *BatchModelChangeError
	if errors.As(err, &batch) {
		status := http.StatusConflict
		for _, item := range batch.Items {
			if item.IsBadRequest() {
				status = http.StatusBadRequest
				break
			}
		}
		common.ApiErrorStatus(c, status, err, gin.H{
			"code":  "MODEL_CHANGE_REJECTED",
			"items": batch.Items,
		})
		return
	}
	switch {
	case errors.Is(err, ErrListingNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, errors.New("listing not found"))
	case errors.Is(err, ErrInvalidTransition):
		common.ApiErrorStatus(c, http.StatusConflict, errors.New(err.Error()))
	case errors.Is(err, ErrModelPriceConflict):
		common.ApiErrorStatus(c, http.StatusConflict, errors.New(err.Error()), gin.H{"code": "PRICE_CONFLICT"})
	default:
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
	}
}

// setSellerModelMultiplier serves PUT /seller/listings/:id/models/:model —
// enable/disable or reprice one model. A decrease applies immediately; an
// increase is staged for one hour.
func setSellerModelMultiplier(c *gin.Context) {
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
	model := c.Param("model")
	var req sellerModelStateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if !req.hasChange() {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("enabled or multiplier_milli is required"))
		return
	}
	rows, err := SetListingModelsState(db, id, sellerID, []string{model}, req.input(), time.Now())
	if err != nil {
		writeModelChangeError(c, err)
		return
	}
	common.ApiSuccess(c, listingModelSummary(rows[0]))
}

// batchSellerListingModelState serves PUT /seller/listings/:id/models — one
// request over several models of one listing. Any rejected row rejects all.
func batchSellerListingModelState(c *gin.Context) {
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
	var req sellerModelStateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if !req.hasChange() {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("enabled or multiplier_milli is required"))
		return
	}
	rows, err := SetListingModelsState(db, id, sellerID, req.Models, req.input(), time.Now())
	if err != nil {
		writeModelChangeError(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, listingModelSummary(row))
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

// batchSellerListingModels serves POST /seller/listings/batch-models — the
// same change across several listings (for example "set every selected
// channel to 0.6x"). The whole batch is one transaction.
func batchSellerListingModels(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	var req sellerModelStateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if len(req.ListingIDs) == 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("listing_ids is required"))
		return
	}
	if !req.hasChange() {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("enabled or multiplier_milli is required"))
		return
	}
	if err := BatchSetListingModelsState(db, sellerID, req.ListingIDs, req.Models, req.input(), time.Now()); err != nil {
		writeModelChangeError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"ok": true})
}

// getSellerMultiplierBounds serves GET /seller/multiplier-bounds — the single
// source the console uses for input min/max and validation copy.
func getSellerMultiplierBounds(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"absolute_min_milli": MultiplierAbsoluteMinMilli,
		"absolute_max_milli": MultiplierAbsoluteMaxMilli,
		"kinds": gin.H{
			ChannelKindOfficial: gin.H{
				"min_milli": MultiplierOfficialMinMilli,
				"max_milli": MultiplierAbsoluteMaxMilli,
			},
			ChannelKindPlan: gin.H{
				"min_milli": MultiplierPlanMinMilli,
				"max_milli": MultiplierAbsoluteMaxMilli,
			},
			ChannelKindThirdParty: gin.H{
				"min_milli": MultiplierThirdPartyMinMilli,
				"max_milli": MultiplierAbsoluteMaxMilli,
			},
		},
	})
}

// restoreSellerListingModels serves POST /seller/listings/:id/models/restore —
// probe each auto-disabled model and enable only the ones that pass.
func restoreSellerListingModels(c *gin.Context) {
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
	result, err := RestoreAutoDisabledModels(db, id, sellerID, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, ErrListingNotFound):
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("listing not found"))
		case errors.Is(err, ErrInvalidTransition):
			common.ApiErrorStatus(c, http.StatusConflict, errors.New(err.Error()))
		default:
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		}
		return
	}
	common.ApiSuccess(c, result)
}

// sellerModelRowView is one row of the console's 模型与价格 table. Buyer prices
// are computed by the same Quote path the debit uses, so the preview cannot
// drift from billing; the console only formats them.
type sellerModelRowView struct {
	Model                    string                      `json:"model"`
	UpstreamModel            string                      `json:"upstream_model"`
	Enabled                  bool                        `json:"enabled"`
	AutoDisabled             bool                        `json:"auto_disabled"`
	DisabledReason           string                      `json:"disabled_reason"`
	DisabledDetail           string                      `json:"disabled_detail"`
	DisabledUpstreamStatus   int                         `json:"disabled_upstream_status"`
	DisabledUpstreamMessage  string                      `json:"disabled_upstream_message"`
	DisabledAt               *time.Time                  `json:"disabled_at,omitempty"`
	MultiplierMilli          int64                       `json:"multiplier_milli"`
	EffectiveMultiplierMilli int64                       `json:"effective_multiplier_milli"`
	PendingMultiplierMilli   int64                       `json:"pending_multiplier_milli"`
	PendingEffectiveAt       *time.Time                  `json:"pending_effective_at,omitempty"`
	CoefficientMilli         int64                       `json:"coefficient_milli"`
	OriginVendor             string                      `json:"origin_vendor"`
	MinMultiplierMilli       int64                       `json:"multiplier_min_milli"`
	MaxMultiplierMilli       int64                       `json:"multiplier_max_milli"`
	OfficialPrices           map[string]squareTierPrices `json:"official_prices"`
	BuyerPrices              map[string]squareTierPrices `json:"buyer_prices"`
}

// getSellerListingModels serves GET /seller/listings/:id/models — every model
// of the listing with enabled state, price, official reference and the buyer
// price computed by Quote.
func getSellerListingModels(c *gin.Context) {
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
	var modelRows []ListingModel
	if err := db.Where("listing_id = ?", id).Find(&modelRows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query model rows"))
		return
	}
	rowByModel := make(map[string]ListingModel, len(modelRows))
	for _, row := range modelRows {
		rowByModel[row.Model] = row
	}
	_, cal := relayState()
	now := time.Now()
	minMilli, maxMilli := multiplierBoundsForVendor(db, listing.Vendor)
	items := make([]sellerModelRowView, 0, len(listing.Models))
	for _, model := range listing.Models {
		row, exists := rowByModel[model]
		if !exists {
			row = ListingModel{
				ListingID:       listing.ID,
				Model:           model,
				MultiplierMilli: listing.MultiplierMilli,
				Enabled:         true,
			}
		}
		origin := listing.Vendor
		if mp, found := ModelPoolByName(db, model); found && strings.TrimSpace(mp.OriginVendor) != "" {
			origin = mp.OriginVendor
		}
		coefficient := int64(1000)
		if c, found := loadVerifiedCoefficients(db, model)[listing.Vendor]; found && c > 0 {
			coefficient = c
		}
		effective := effectiveListingModelMultiplier(row, now)
		upstream := model
		if cm, found := ChannelModelFor(db, listing.Vendor, model); found && cm.UpstreamModel != "" {
			upstream = cm.UpstreamModel
		}
		official := make(map[string]squareTierPrices, 3)
		buyer := make(map[string]squareTierPrices, 3)
		for _, tier := range []string{TierPeak, TierOffpeak, TierFlat} {
			pb, err := PriceAt(db, model, origin, tier, now)
			if err != nil {
				continue
			}
			official[tier] = squareTierPrices{
				InputMicroPerMTok:      pb.InputMicroPerMTok,
				OutputMicroPerMTok:     pb.OutputMicroPerMTok,
				CacheReadMicroPerMTok:  pb.CacheReadMicroPerMTok,
				CacheWriteMicroPerMTok: pb.CacheWriteMicroPerMTok,
			}
			if cal != nil {
				if prices, err := quoteTierPrices(db, cal, model, listing.Vendor, origin, effective, coefficient, now); err == nil {
					buyer[tier] = prices
				}
			}
		}
		items = append(items, sellerModelRowView{
			Model:                    model,
			UpstreamModel:            upstream,
			Enabled:                  row.Enabled,
			AutoDisabled:             row.AutoDisabled,
			DisabledReason:           row.DisabledReason,
			DisabledDetail:           row.DisabledDetail,
			DisabledUpstreamStatus:   row.DisabledHTTPStatus,
			DisabledUpstreamMessage:  row.DisabledUpstreamMessage,
			DisabledAt:               row.DisabledAt,
			MultiplierMilli:          row.MultiplierMilli,
			EffectiveMultiplierMilli: effectiveMultiplierMilli(effective, coefficient),
			PendingMultiplierMilli:   row.PendingMultiplierMilli,
			PendingEffectiveAt:       row.PendingEffectiveAt,
			CoefficientMilli:         coefficient,
			OriginVendor:             origin,
			MinMultiplierMilli:       minMilli,
			MaxMultiplierMilli:       maxMilli,
			OfficialPrices:           official,
			BuyerPrices:              buyer,
		})
	}
	tier := ""
	if cal != nil {
		tier = cal.TierAt(now)
	}
	common.ApiSuccess(c, gin.H{
		"items":                items,
		"platform_fee_percent": PlatformFeePercent,
		"tier":                 tier,
		"tier_label":           tierLabel(tier),
		"multiplier_min_milli": minMilli,
		"multiplier_max_milli": maxMilli,
	})
}

// listSellerListingGroups serves GET /seller/groups — the seller's P4 groups,
// each with its sibling keys, so the create wizard can offer joining an
// existing channel.
func listSellerListingGroups(c *gin.Context) {
	sellerID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	var groups []ListingGroup
	if err := db.Where("seller_id = ?", sellerID).Order("id ASC").Find(&groups).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query groups"))
		return
	}
	out := make([]sellerListingGroupView, 0, len(groups))
	for _, group := range groups {
		view, err := sellerListingGroupViewFor(db, group)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query group listings"))
			return
		}
		out = append(out, view)
	}
	common.ApiSuccess(c, out)
}

// updateSellerListingGroup serves PUT /seller/groups/:id — change the group's
// routing strategy, affinity switch and aggregate channel limits
// (ownership-checked). Limits are non-negative; 0 means unlimited.
func updateSellerListingGroup(c *gin.Context) {
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
	var group ListingGroup
	if err := db.Where("id = ? AND seller_id = ?", id, sellerID).Take(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("group not found"))
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query group"))
		return
	}
	var req struct {
		KeyStrategy               string `json:"key_strategy"`
		AffinityEnabled           bool   `json:"affinity_enabled"`
		ChannelConcurrencyLimit   int    `json:"channel_concurrency_limit"`
		ChannelRPMLimit           int    `json:"channel_rpm_limit"`
		ChannelDailyCapMicro      int64  `json:"channel_daily_cap_micro"`
		ChannelCumulativeCapMicro int64  `json:"channel_cumulative_cap_micro"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if err := UpdateListingGroup(db, group.ID, req.KeyStrategy, req.AffinityEnabled,
		req.ChannelConcurrencyLimit, req.ChannelRPMLimit,
		req.ChannelDailyCapMicro, req.ChannelCumulativeCapMicro, time.Now()); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		return
	}
	updated, err := loadListingGroup(db, group.ID)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query group"))
		return
	}
	view, err := sellerListingGroupViewFor(db, updated)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query group listings"))
		return
	}
	common.ApiSuccess(c, view)
}

// loadListingGroup returns one group by id.
func loadListingGroup(db *gorm.DB, id int64) (ListingGroup, error) {
	var group ListingGroup
	if err := db.Where("id = ?", id).Take(&group).Error; err != nil {
		return ListingGroup{}, err
	}
	return group, nil
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
//
// A seller-defined upstream the trust pool does not trust (watch/unlisted) is
// refused here: such a listing reaches draft only through an operator's reject
// decision (pending_review → draft), and letting the seller re-run verification
// on it would bypass the review the trust pool requires. Only an operator
// approve may release it into the automated flow (api_admin.go). Platform
// channels are governed by mp_channels/operators and keep the automated flow.
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
	// Review-bypass guard. CreateListing parks a custom channel the trust pool
	// classifies as watch/unlisted in pending_review; an operator reject moves
	// it back to draft. Without this check the seller could then drive
	// draft → verifying → canary on their own, skipping the review. Re-classify
	// with the full stored credential (key-specific trust rows) and fail closed.
	if isCustomChannelSlug(listing.Vendor) {
		vendor, found := lookupVendorForDB(db, listing.Vendor)
		if !found {
			common.ApiErrorStatus(c, http.StatusForbidden, errors.New("该渠道需要运营审核"))
			return
		}
		apiKey, err := DecryptListingKey(listing.KeyCiphertext)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to load listing key"))
			return
		}
		host, path := sellerTrustAddress(vendor)
		trust, _, err := CheckTrustPoolDB(db, host, path, apiKey)
		if err != nil {
			if errors.Is(err, ErrTrustPoolBlocked) {
				common.ApiErrorStatus(c, http.StatusForbidden, errors.New("该渠道已被运营屏蔽"))
				return
			}
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to check channel trust"))
			return
		}
		if trust != TrustTrusted {
			common.ApiErrorStatus(c, http.StatusForbidden, errors.New("该渠道需要运营审核"))
			return
		}
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
	// A live listing was the channel's relay-whitelist membership; drop it on
	// the next registry reload.
	refreshVendorRegistry(true)
	resetBuyerModelVisibilityCache()
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
	// The listing is live again, so its custom channel may rejoin the relay
	// whitelist immediately.
	refreshVendorRegistry(true)
	resetBuyerModelVisibilityCache()
	common.ApiSuccess(c, gin.H{"id": id, "status": ListingLive})
}

// deleteSellerListing serves DELETE /seller/listings/:id — soft delete. The
// response is idempotent: a second call returns the same status.
func deleteSellerListing(c *gin.Context) {
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
	if _, err := DeleteListingForSeller(db, id, sellerID, time.Now()); err != nil {
		if errors.Is(err, ErrListingNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("listing not found"))
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to delete listing"))
		return
	}
	if err := MirrorListingDelete(db, id); err != nil {
		common.SysError(common.LogText("marketplace: mirror deleted listing %d: %s", id, err.Error()))
	}
	refreshVendorRegistry(true)
	resetBuyerModelVisibilityCache()
	common.ApiSuccess(c, gin.H{"id": id, "status": ListingDeleted})
}

// sellerListingSettingsRequest is the PUT /seller/listings/:id body. Nil
// fields are left unchanged so the drawer can submit only dirty fields.
type sellerListingSettingsRequest struct {
	Label            *string `json:"label"`
	ConcurrencyLimit *int    `json:"concurrency_limit"`
	RPMLimit         *int    `json:"rpm_limit"`
	DailyCapMicro    *int64  `json:"daily_cap_micro"`
	Hours            *string `json:"hours"`
}

// updateSellerListingSettings serves PUT /seller/listings/:id — name, limits
// and business hours.
func updateSellerListingSettings(c *gin.Context) {
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
	var req sellerListingSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	updated, err := UpdateListingSettings(db, id, sellerID, UpdateListingSettingsInput{
		Label:            req.Label,
		ConcurrencyLimit: req.ConcurrencyLimit,
		RPMLimit:         req.RPMLimit,
		DailyCapMicro:    req.DailyCapMicro,
		Hours:            req.Hours,
	}, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, ErrListingNotFound):
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("listing not found"))
		case errors.Is(err, ErrInvalidTransition):
			common.ApiErrorStatus(c, http.StatusConflict, errors.New(err.Error()))
		default:
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		}
		return
	}
	view := toSellerListingView(&updated)
	common.ApiSuccess(c, view)
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
	var paid int64
	if err := db.Model(&Payout{}).
		Where("seller_id = ? AND status = ?", sellerID, PayoutPaid).
		Select("COALESCE(SUM(amount_micro), 0)").Scan(&paid).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query paid payouts"))
		return
	}
	common.ApiSuccess(c, gin.H{
		"available_micro": int64(available),
		"pending_micro":   int64(pending),
		"paid_micro":      paid,
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
	VendorID string   `json:"vendor_id"`
	APIKey   string   `json:"api_key"`
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

// Probe rate limits (§5): dry-run probes spend upstream credit and can be
// used to enumerate a key, so they are bounded per seller and per client IP in
// process memory. A fixed one-minute window is enough — this is abuse
// protection, not billing.
const (
	probeSellerPerMinute  = 10
	probeIPPerMinute      = 30
	probeRateWindowLength = time.Minute
	// maxProbeRateWindows bounds the in-memory counter map; expired windows are
	// purged when it is exceeded.
	maxProbeRateWindows = 4096
	// maxProbeFingerprintOwners bounds the cross-account probe tracker.
	maxProbeFingerprintOwners = 4096
)

// ErrProbeRateLimited marks a probe refused by the per-seller or per-IP
// fixed window. Callers map it to 429.
var ErrProbeRateLimited = errors.New("marketplace: probe rate limit exceeded")

// probeRateWindow is one fixed-window counter.
type probeRateWindow struct {
	start time.Time
	count int
}

var probeRateState = struct {
	mu      sync.Mutex
	windows map[string]*probeRateWindow
}{windows: make(map[string]*probeRateWindow)}

// allowProbeRequest charges one probe to both the seller and the client IP,
// returning ErrProbeRateLimited when either window is exhausted. The maps are
// keyed with a prefix so the two dimensions never collide.
func allowProbeRequest(sellerID int64, clientIP string, now time.Time) error {
	probeRateState.mu.Lock()
	defer probeRateState.mu.Unlock()
	if len(probeRateState.windows) > maxProbeRateWindows {
		for key, w := range probeRateState.windows {
			if now.Sub(w.start) >= probeRateWindowLength {
				delete(probeRateState.windows, key)
			}
		}
	}
	allow := func(key string, limit int) bool {
		w, ok := probeRateState.windows[key]
		if !ok || now.Sub(w.start) >= probeRateWindowLength {
			probeRateState.windows[key] = &probeRateWindow{start: now, count: 1}
			return true
		}
		if w.count >= limit {
			return false
		}
		w.count++
		return true
	}
	if !allow("seller:"+strconv.FormatInt(sellerID, 10), probeSellerPerMinute) {
		return fmt.Errorf("%w: too many probes from this account", ErrProbeRateLimited)
	}
	if ip := strings.TrimSpace(clientIP); ip != "" && !allow("ip:"+ip, probeIPPerMinute) {
		return fmt.Errorf("%w: too many probes from this address", ErrProbeRateLimited)
	}
	return nil
}

// probeFingerprintOwners remembers the first seller that probed each key
// fingerprint. A second account probing the same key is an operator alert: it
// suggests a leaked/shared credential. Nothing is persisted and only the
// fingerprint prefix is logged (§2.3).
var probeFingerprintOwners = struct {
	mu     sync.Mutex
	owners map[string]int64
}{owners: make(map[string]int64)}

// noteProbeFingerprint records one probe's key fingerprint and logs when a
// different account probes the same key.
func noteProbeFingerprint(fingerprint string, sellerID int64) {
	prefix := fingerprint
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	probeFingerprintOwners.mu.Lock()
	owner, seen := probeFingerprintOwners.owners[fingerprint]
	if !seen {
		if len(probeFingerprintOwners.owners) >= maxProbeFingerprintOwners {
			probeFingerprintOwners.owners = make(map[string]int64)
		}
		probeFingerprintOwners.owners[fingerprint] = sellerID
	}
	probeFingerprintOwners.mu.Unlock()
	if seen && owner != sellerID {
		common.SysError(common.LogText(
			"marketplace: api key fp %s probed by seller %d, earlier probed by seller %d",
			prefix, sellerID, owner))
	}
}

// resetProbeStateForTest clears the process-local probe trackers so one test
// cannot consume another test's quota.
func resetProbeStateForTest() {
	probeRateState.mu.Lock()
	probeRateState.windows = make(map[string]*probeRateWindow)
	probeRateState.mu.Unlock()
	probeFingerprintOwners.mu.Lock()
	probeFingerprintOwners.owners = make(map[string]int64)
	probeFingerprintOwners.mu.Unlock()
}

// sellerTrustPoolView is the recognition result the wizard shows as a badge
// while the seller types an upstream address. It is metadata only: the probe
// still runs the normal per-model verification, and listing creation re-checks
// the trust pool independently.
type sellerTrustPoolView struct {
	Matched    bool     `json:"matched"`
	Trust      string   `json:"trust"`
	Kind       string   `json:"kind"`
	VendorName string   `json:"vendor_name"`
	Endpoints  []string `json:"endpoints"`
	BadgeText  string   `json:"badge_text"`
}

// trustEndpointLabel is the short protocol name used in the badge.
func trustEndpointLabel(endpoint string) string {
	switch endpoint {
	case "openai_chat":
		return "OpenAI"
	case "openai_responses":
		return "OpenAI Responses"
	case "anthropic_messages":
		return "Anthropic"
	default:
		return endpoint
	}
}

// trustKindLabel is the channel kind shown in the badge.
func trustKindLabel(kind string) string {
	switch kind {
	case TrustKindOfficial:
		return "官方"
	case TrustKindPlan:
		return "套餐"
	case TrustKindThirdParty:
		return "第三方"
	default:
		return ""
	}
}

// trustStateLabel is the review state shown in the badge.
func trustStateLabel(trust string) string {
	switch trust {
	case TrustTrusted:
		return "免审核"
	case TrustWatch:
		return "人工审核"
	case TrustBlocked:
		return "已屏蔽"
	default:
		return "未收录 · 需人工审核"
	}
}

// sellerTrustPoolViewFor classifies (host, path, key) and renders the badge.
// It never carries the key and ignores the blocked error (the trust state is
// what the console needs; the create/probe gates still fail closed).
func sellerTrustPoolViewFor(db *gorm.DB, host, path, key string) sellerTrustPoolView {
	trust, pool, _ := CheckTrustPool(host, path, key)
	if trust == "" {
		trust = TrustUnlisted
	}
	view := sellerTrustPoolView{Matched: pool != nil, Trust: trust, Endpoints: []string{}}
	if pool != nil {
		view.Kind = strings.TrimSpace(pool.Kind)
		if view.Kind == "" {
			view.Kind = TrustKindThirdParty
		}
		if slug := strings.TrimSpace(pool.VendorSlug); slug != "" {
			view.VendorName = slug
			if vendor, ok := lookupVendorForDB(db, slug); ok && strings.TrimSpace(vendor.DisplayName) != "" {
				view.VendorName = vendor.DisplayName
			}
		}
		view.Endpoints = append(view.Endpoints, parseStringList(pool.Endpoints)...)
	}
	parts := make([]string, 0, 4)
	if view.VendorName != "" {
		parts = append(parts, view.VendorName)
	}
	if label := trustKindLabel(view.Kind); label != "" {
		parts = append(parts, label)
	}
	if len(view.Endpoints) > 0 {
		labels := make([]string, 0, len(view.Endpoints))
		for _, endpoint := range view.Endpoints {
			labels = append(labels, trustEndpointLabel(endpoint))
		}
		parts = append(parts, strings.Join(labels, " + "))
	}
	parts = append(parts, trustStateLabel(view.Trust))
	view.BadgeText = strings.Join(parts, " · ")
	return view
}

// sellerTrustAddress returns the (host, path) of a vendor's OpenAI-compatible
// base, which is the address the trust pool classifies for a registered
// channel. A malformed base yields empty values and therefore "unlisted".
func sellerTrustAddress(vendor Vendor) (host, path string) {
	parsed, err := url.Parse(VendorOpenAIBase(vendor))
	if err != nil {
		return "", ""
	}
	return parsed.Hostname(), parsed.Path
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
	// A probe spends upstream credit and reads a seller's key; keep it behind
	// the same active-seller gate as listing creation.
	if err := RequireActiveSeller(db, sellerID); err != nil {
		status, msg := sellerGateStatus(err)
		common.ApiErrorStatus(c, status, errors.New(msg))
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
	// Two fixed windows: the account cannot fan out key checks, and one client
	// address cannot sweep accounts. Both answer 429.
	if err := allowProbeRequest(sellerID, c.ClientIP(), time.Now()); err != nil {
		common.ApiErrorStatus(c, http.StatusTooManyRequests, err)
		return
	}
	// Same key probed by a different account is an operator alert (memory only:
	// the probe path never writes a row).
	if fingerprint, ferr := ListingKeyFingerprint(apiKey); ferr == nil {
		noteProbeFingerprint(fingerprint, sellerID)
	}

	// Seller-defined upstream: validation and the probe are read-only. The
	// channel row (and any relay-whitelist membership) is provisioned by
	// CreateListing when the seller submits, never by a dry run.
	if strings.TrimSpace(req.CustomBaseURL) != "" {
		base, host, err := validateCustomBaseURL(req.CustomBaseURL)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, err)
			return
		}
		// Recognition runs before the paid probe so the console can show the
		// trust badge even when the upstream call fails.
		trustView := sellerTrustPoolViewFor(db, host, customBasePath(base), apiKey)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		client, limiter := sellerVerifyResources()
		if err := limiter.Acquire(ctx); err != nil {
			common.ApiErrorStatus(c, http.StatusTooManyRequests, errors.New("verification is busy, try again later"))
			return
		}
		defer limiter.Release()
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
			"ok":                 probeOK,
			"models":             sellable,
			"verified_models":    sellable,
			"coefficients":       coefficients,
			"missing_models":     missing,
			"latency_ms":         keyRes.LatencyMs,
			"prompt_cache":       promptCache,
			"balance_sufficient": balance != nil && *balance > 0,
			"key_valid":          keyRes.KeyValid,
			"fail_reason":        failReason,
			"trust_pool":         trustView,
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
	trustHost, trustPath := sellerTrustAddress(vendor)
	trustView := sellerTrustPoolViewFor(db, trustHost, trustPath, apiKey)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	client, limiter := sellerVerifyResources()
	if err := limiter.Acquire(ctx); err != nil {
		common.ApiErrorStatus(c, http.StatusTooManyRequests, errors.New("verification is busy, try again later"))
		return
	}
	defer limiter.Release()

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
	// cannot turn OK, so spending its paid probe would be waste. Only key
	// sufficiency is surfaced — never the upstream balance itself.
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
		"ok":                 probeOK,
		"models":             available,
		"verified_models":    available, // explicit alias for the seller wizard
		"coefficients":       coefficients,
		"missing_models":     missing,
		"latency_ms":         keyRes.LatencyMs,
		"prompt_cache":       promptCache,
		"balance_sufficient": balance != nil && *balance > 0,
		"key_valid":          keyRes.KeyValid,
		"fail_reason":        failReason,
		"trust_pool":         trustView,
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
	// P4: the listing's group (with its sibling keys) and the per-model
	// multiplier table shown in the detail page.
	Group            *sellerListingGroupView `json:"group,omitempty"`
	ModelMultipliers []sellerModelMultiplier `json:"model_multipliers"`
}

// sellerModelMultiplier is one row of a listing's per-model multiplier table.
type sellerModelMultiplier struct {
	Model                  string     `json:"model"`
	MultiplierMilli        int64      `json:"multiplier_milli"`
	Enabled                bool       `json:"enabled"`
	PendingMultiplierMilli int64      `json:"pending_multiplier_milli"`
	PendingEffectiveAt     *time.Time `json:"pending_effective_at,omitempty"`
	AutoDisabled           bool       `json:"auto_disabled"`
	DisabledReason         string     `json:"disabled_reason"`
}

// sellerGroupSibling is one key (listing) of a group, shown next to the key
// being viewed. It carries only the fingerprint prefix, like every seller view.
type sellerGroupSibling struct {
	ID              int64    `json:"id"`
	Status          string   `json:"status"`
	Models          []string `json:"models"`
	MultiplierMilli int64    `json:"multiplier_milli"`
	Weight          int      `json:"weight"`
	KeyFingerprint8 string   `json:"key_fingerprint8"`
}

// sellerListingGroupView is the seller-facing group block: the routing
// strategy, the aggregate channel limits and the sibling keys.
type sellerListingGroupView struct {
	ID                        int64                `json:"id"`
	VendorSlug                string               `json:"vendor_slug"`
	KeyStrategy               string               `json:"key_strategy"`
	AffinityEnabled           bool                 `json:"affinity_enabled"`
	ChannelConcurrencyLimit   int                  `json:"channel_concurrency_limit"`
	ChannelRPMLimit           int                  `json:"channel_rpm_limit"`
	ChannelDailyCapMicro      int64                `json:"channel_daily_cap_micro"`
	ChannelCumulativeCapMicro int64                `json:"channel_cumulative_cap_micro"`
	KeyCount                  int                  `json:"key_count"`
	Listings                  []sellerGroupSibling `json:"listings"`
}

// sellerListingGroupViewFor builds the group view for one seller-owned group,
// including every sibling listing. The caller must have verified ownership.
func sellerListingGroupViewFor(db *gorm.DB, group ListingGroup) (sellerListingGroupView, error) {
	var listings []Listing
	if err := db.Select("id", "status", "models", "multiplier_milli", "weight", "key_fingerprint").
		Where("group_id = ?", group.ID).Order("id ASC").Find(&listings).Error; err != nil {
		return sellerListingGroupView{}, err
	}
	siblings := make([]sellerGroupSibling, 0, len(listings))
	for i := range listings {
		fp8 := ""
		if len(listings[i].KeyFingerprint) >= 8 {
			fp8 = listings[i].KeyFingerprint[:8]
		}
		siblings = append(siblings, sellerGroupSibling{
			ID:              listings[i].ID,
			Status:          listings[i].Status,
			Models:          listings[i].Models,
			MultiplierMilli: listings[i].MultiplierMilli,
			Weight:          listings[i].Weight,
			KeyFingerprint8: fp8,
		})
	}
	return sellerListingGroupView{
		ID:                        group.ID,
		VendorSlug:                group.VendorSlug,
		KeyStrategy:               group.KeyStrategy,
		AffinityEnabled:           group.AffinityEnabled,
		ChannelConcurrencyLimit:   group.ChannelConcurrencyLimit,
		ChannelRPMLimit:           group.ChannelRPMLimit,
		ChannelDailyCapMicro:      group.ChannelDailyCapMicro,
		ChannelCumulativeCapMicro: group.ChannelCumulativeCapMicro,
		KeyCount:                  len(siblings),
		Listings:                  siblings,
	}, nil
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
	applyListingAggregates(&view, listing, loadListingModelOverrides(db, []int64{id}, time.Now()))
	view.PauseReason = listingPauseReason(listing, probes)
	// Look the newest verify probe up independently of the recent-probe
	// window: a run of patrol probes must not hide the last per-model verify
	// result the seller needs to fix a failed draft.
	if latest := latestVerifyProbe(db, id); latest != nil {
		view.Verification = probeVerificationView(*latest)
	}
	// P4: the group block (with sibling keys) and the per-model multiplier
	// table. A listing created before groups existed has GroupID 0 and simply
	// renders no group block.
	var groupView *sellerListingGroupView
	if listing.GroupID > 0 {
		if group, gerr := loadListingGroup(db, listing.GroupID); gerr == nil {
			if v, verr := sellerListingGroupViewFor(db, group); verr == nil {
				groupView = &v
			}
		}
	}
	var modelRows []ListingModel
	if err := db.Where("listing_id = ?", id).Order("model ASC").Find(&modelRows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query model multipliers"))
		return
	}
	modelMultipliers := make([]sellerModelMultiplier, 0, len(modelRows))
	for _, row := range modelRows {
		modelMultipliers = append(modelMultipliers, sellerModelMultiplier{
			Model:                  row.Model,
			MultiplierMilli:        row.MultiplierMilli,
			Enabled:                row.Enabled,
			PendingMultiplierMilli: row.PendingMultiplierMilli,
			PendingEffectiveAt:     row.PendingEffectiveAt,
			AutoDisabled:           row.AutoDisabled,
			DisabledReason:         row.DisabledReason,
		})
	}
	common.ApiSuccess(c, sellerListingDetail{
		sellerListingView: view,
		Probes:            views,
		Group:             groupView,
		ModelMultipliers:  modelMultipliers,
	})
}

// ---------------------------------------------------------------------------
// 渠道健康聚合 (§5): 每个渠道的最近一次巡检 + 今日请求/成功率/收入.
// ---------------------------------------------------------------------------

// sellerHealthProbe is the latest probe summary on a seller health card. It
// carries the probe's own ok/latency plus fail_reason and per-model results
// decoded from detail; no key material is ever present in a probe.
type sellerHealthProbe struct {
	Type         string              `json:"type"`
	OK           bool                `json:"ok"`
	LatencyMs    int64               `json:"latency_ms"`
	FailReason   string              `json:"fail_reason"`
	ModelResults []ModelVerifyResult `json:"model_results"`
	CreatedAt    time.Time           `json:"created_at"`
}

// sellerListingHealth is one row of GET /seller/listings/health. Today is the
// Asia/Shanghai calendar day, matching seller stats.
type sellerListingHealth struct {
	ListingID             int64              `json:"listing_id"`
	LatestProbe           *sellerHealthProbe `json:"latest_probe"`
	TodayRequests         int64              `json:"today_requests"`
	TodaySuccessRateMilli int64              `json:"today_success_rate_milli"`
	TodayIncomeMicro      int64              `json:"today_income_micro"`
	// Three rolling windows for the table's success-rate cell. A window with
	// no sample is -1; the console applies its own <5-sample display threshold
	// using the paired request count.
	Requests1h          int64 `json:"requests_1h"`
	SuccessRate1hMilli  int64 `json:"success_rate_1h_milli"`
	Requests24h         int64 `json:"requests_24h"`
	SuccessRate24hMilli int64 `json:"success_rate_24h_milli"`
	Requests7d          int64 `json:"requests_7d"`
	SuccessRate7dMilli  int64 `json:"success_rate_7d_milli"`
}

// windowRateMilli converts a window's counts into a 0..1000 rate; an empty
// window is -1 so the UI can hide it instead of printing a fake 0.
func windowRateMilli(total, success int64) int64 {
	if total <= 0 {
		return -1
	}
	return success * 1000 / total
}

// latestProbesByListing maps each listing to its newest probe of any type in
// two queries (max id per listing, then the rows), so health cards never load
// the full probe history.
func latestProbesByListing(db *gorm.DB, listingIDs []int64) map[int64]Probe {
	out := make(map[int64]Probe, len(listingIDs))
	if len(listingIDs) == 0 {
		return out
	}
	var probeIDs []int64
	if err := db.Model(&Probe{}).
		Where("listing_id IN ?", listingIDs).
		Group("listing_id").Pluck("MAX(id)", &probeIDs).Error; err != nil || len(probeIDs) == 0 {
		return out
	}
	var rows []Probe
	if err := db.Where("id IN ?", probeIDs).Find(&rows).Error; err != nil {
		return out
	}
	for _, p := range rows {
		out[p.ListingID] = p
	}
	return out
}

// sellerHealthProbeView decodes a probe's detail into the health-card shape.
func sellerHealthProbeView(p Probe) *sellerHealthProbe {
	var detail struct {
		FailReason   string              `json:"fail_reason"`
		ModelResults []ModelVerifyResult `json:"model_results"`
	}
	_ = common.Unmarshal([]byte(p.Detail), &detail)
	results := detail.ModelResults
	if results == nil {
		results = []ModelVerifyResult{}
	}
	return &sellerHealthProbe{
		Type:         p.Type,
		OK:           p.OK,
		LatencyMs:    p.LatencyMs,
		FailReason:   detail.FailReason,
		ModelResults: results,
		CreatedAt:    p.CreatedAt,
	}
}

// getSellerListingsHealth serves GET /seller/listings/health — one health row
// per own listing: latest probe, today's request count and success rate, and
// today's seller credit. Queries are grouped by listing so the endpoint keeps
// a constant query count regardless of listing count.
func getSellerListingsHealth(c *gin.Context) {
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
	latest := latestProbesByListing(db, ids)

	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to load timezone"))
		return
	}
	now := time.Now().In(loc)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	type requestAgg struct {
		ListingID int64
		Total     int64
		Success   int64
	}
	reqByListing := make(map[int64]requestAgg, len(ids))
	if len(ids) > 0 {
		var aggs []requestAgg
		if err := db.Table("mp_requests").
			Select("listing_id, COUNT(*) AS total, SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS success", RequestSuccess).
			Where("seller_id = ? AND listing_id IN ? AND created_at >= ?", sellerID, ids, todayStart).
			Group("listing_id").Scan(&aggs).Error; err != nil {
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query today's requests"))
			return
		}
		for _, agg := range aggs {
			reqByListing[agg.ListingID] = agg
		}
	}

	type windowAgg struct {
		ListingID  int64
		Total7d    int64
		Success7d  int64
		Total24h   int64
		Success24h int64
		Total1h    int64
		Success1h  int64
	}
	windowsByListing := make(map[int64]windowAgg, len(ids))
	if len(ids) > 0 {
		since7d := now.Add(-7 * 24 * time.Hour)
		since24h := now.Add(-24 * time.Hour)
		since1h := now.Add(-time.Hour)
		var aggs []windowAgg
		if err := db.Table("mp_requests").
			Select(`listing_id,
				COUNT(*) AS total7d,
				SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS success7d,
				SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END) AS total24h,
				SUM(CASE WHEN created_at >= ? AND status = ? THEN 1 ELSE 0 END) AS success24h,
				SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END) AS total1h,
				SUM(CASE WHEN created_at >= ? AND status = ? THEN 1 ELSE 0 END) AS success1h`,
				RequestSuccess, since24h, since24h, RequestSuccess, since1h, since1h, RequestSuccess).
			Where("seller_id = ? AND listing_id IN ? AND created_at >= ?", sellerID, ids, since7d).
			Group("listing_id").Scan(&aggs).Error; err != nil {
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query request windows"))
			return
		}
		for _, agg := range aggs {
			windowsByListing[agg.ListingID] = agg
		}
	}

	type incomeAgg struct {
		ListingID   int64
		IncomeMicro int64
	}
	incomeByListing := make(map[int64]int64, len(ids))
	if len(ids) > 0 {
		var aggs []incomeAgg
		if err := db.Table("mp_ledger AS l").
			Select("r.listing_id AS listing_id, COALESCE(SUM(l.amount_micro), 0) AS income_micro").
			Joins("JOIN mp_requests AS r ON r.request_id = l.request_id").
			Where("r.seller_id = ? AND r.listing_id IN ? AND l.entry_type = ? AND l.status <> ? AND l.created_at >= ?",
				sellerID, ids, EntrySellerCredit, LedgerReversed, todayStart).
			Group("r.listing_id").Scan(&aggs).Error; err != nil {
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query today's income"))
			return
		}
		for _, agg := range aggs {
			incomeByListing[agg.ListingID] = agg.IncomeMicro
		}
	}

	out := make([]sellerListingHealth, 0, len(rows))
	for _, listing := range rows {
		row := sellerListingHealth{
			ListingID:        listing.ID,
			TodayIncomeMicro: incomeByListing[listing.ID],
		}
		if agg, ok := reqByListing[listing.ID]; ok {
			row.TodayRequests = agg.Total
			if agg.Total > 0 {
				row.TodaySuccessRateMilli = agg.Success * 1000 / agg.Total
			}
		}
		if agg, ok := windowsByListing[listing.ID]; ok {
			row.Requests1h = agg.Total1h
			row.SuccessRate1hMilli = windowRateMilli(agg.Total1h, agg.Success1h)
			row.Requests24h = agg.Total24h
			row.SuccessRate24hMilli = windowRateMilli(agg.Total24h, agg.Success24h)
			row.Requests7d = agg.Total7d
			row.SuccessRate7dMilli = windowRateMilli(agg.Total7d, agg.Success7d)
		} else {
			row.SuccessRate1hMilli = -1
			row.SuccessRate24hMilli = -1
			row.SuccessRate7dMilli = -1
		}
		if p, ok := latest[listing.ID]; ok {
			row.LatestProbe = sellerHealthProbeView(p)
		}
		out = append(out, row)
	}
	common.ApiSuccess(c, out)
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
	// paid is the cumulative amount already disbursed (only the paid status
	// counts; pending/approved requests are still reserved, see balance).
	var paid int64
	if err := db.Model(&Payout{}).
		Where("seller_id = ? AND status = ?", sellerID, PayoutPaid).
		Select("COALESCE(SUM(amount_micro), 0)").Scan(&paid).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query paid payouts"))
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
		"paid_micro":               paid,
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
