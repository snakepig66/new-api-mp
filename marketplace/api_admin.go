package marketplace

// api_admin.go — 后台 API (§5 阶段 6): 价目表、节假日、挂单审核、提现审批、冲
// 正、代号反查。Every endpoint here is admin-only; the route group these handlers
// hang on is expected to carry middleware.AdminAuth() (wired by the router seam,
// §4.4 — this package cannot import middleware without an import cycle, so the
// AdminAuth group is built by the caller and passed in).
//
// Privacy contract (§2.3/§3.1): no response from this file ever carries a key,
// a key ciphertext or a full fingerprint. Listings expose only the fingerprint's
// first 8 hex chars; the 代号反查 endpoint returns the buyer's real user id to a
// logged-in admin only (后台可反查完整对应关系).
//
// These are web-console APIs: they answer in the upstream console envelope
// (common.ApiSuccess / common.ApiErrorStatus), like api_records.go.

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// RegisterAdminRoutes is the exported one-line wrapper the router seam calls;
// the marketplace package cannot import middleware (import cycle), so the
// AdminAuth group is built by the caller and passed in.
func RegisterAdminRoutes(g *gin.RouterGroup) { registerAdminRoutes(g) }

// registerAdminRoutes mounts the §5 阶段 6 admin endpoints on g. g is assumed
// to already carry middleware.AdminAuth() (and to be gated on Enabled() by the
// caller). Route wiring into RegisterRoutes is done centrally in api.go.
func registerAdminRoutes(g *gin.RouterGroup) {
	g.POST("/admin/prices", adminPostPrice)
	g.GET("/admin/prices", adminListPrices)
	g.POST("/admin/holidays", adminPostHolidays)
	g.GET("/admin/holidays", adminListHolidays)
	g.GET("/admin/listings", adminListListings)
	g.POST("/admin/listings/:id/ban", adminBanListing)
	g.POST("/admin/listings/:id/unban", adminUnbanListing)
	g.GET("/admin/payouts", adminListPayouts)
	g.POST("/admin/payouts/:id/approve", adminApprovePayout)
	g.POST("/admin/payouts/:id/reject", adminRejectPayout)
	g.POST("/admin/payouts/:id/mark-paid", adminMarkPayoutPaid)
	g.POST("/admin/reverse", adminReverse)
	g.GET("/admin/deanon", adminDeanon)
}

// adminDB returns the marketplace primary database handle, answering 500 when
// the module state is not initialized (Enabled() gating makes this unreachable
// in production).
func adminDB(c *gin.Context) *gorm.DB {
	db, _ := relayState()
	if db == nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
		return nil
	}
	return db
}

// adminIDParam parses the :id path parameter as a positive int64.
func adminIDParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid id"))
		return 0, false
	}
	return id, true
}

// ---------------------------------------------------------------------------
// 价目表 (§3.3): 价目只增不改 — every POST inserts a new version row; existing
// rows are never modified, so history is retained.
// ---------------------------------------------------------------------------

// adminPriceRequest is the POST /admin/prices body. Version is optional: when
// omitted (or <= the current max for the (model,vendor,tier) key) the next
// version is auto-assigned. Prices are 微元 per million tokens.
type adminPriceRequest struct {
	Model                  string `json:"model"`
	Vendor                 string `json:"vendor"`
	Tier                   string `json:"tier"`
	InputMicroPerMTok      int64  `json:"input_micro_per_mtok"`
	OutputMicroPerMTok     int64  `json:"output_micro_per_mtok"`
	CacheReadMicroPerMTok  int64  `json:"cache_read_micro_per_mtok"`
	CacheWriteMicroPerMTok int64  `json:"cache_write_micro_per_mtok"`
	Version                int64  `json:"version"`
	EffectiveAt            string `json:"effective_at"` // RFC3339; empty = now
}

// nextPriceVersion returns max(version)+1 for the (model,vendor,tier) key, so
// a new row always supersedes the current one without touching it.
func nextPriceVersion(db *gorm.DB, model, vendor, tier string) (int64, error) {
	var maxV *int64
	if err := db.Model(&PriceBook{}).
		Where("model = ? AND vendor = ? AND tier = ?", model, vendor, tier).
		Select("COALESCE(MAX(version), 0)").Scan(&maxV).Error; err != nil {
		return 0, err
	}
	if maxV == nil {
		return 1, nil
	}
	return *maxV + 1, nil
}

// adminPostPrice serves POST /admin/prices — insert a new version row.
func adminPostPrice(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	var req adminPriceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if req.Model == "" || req.Vendor == "" || req.Tier == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("model, vendor and tier are required"))
		return
	}
	if req.InputMicroPerMTok < 0 || req.OutputMicroPerMTok < 0 ||
		req.CacheReadMicroPerMTok < 0 || req.CacheWriteMicroPerMTok < 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("prices must be >= 0"))
		return
	}
	effectiveAt := time.Now()
	if req.EffectiveAt != "" {
		t, err := time.Parse(time.RFC3339, req.EffectiveAt)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("effective_at must be RFC3339"))
			return
		}
		effectiveAt = t
	}
	version := req.Version
	if version <= 0 {
		next, err := nextPriceVersion(db, req.Model, req.Vendor, req.Tier)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to assign price version"))
			return
		}
		version = next
	}
	// 价目只增不改: reject a version that would collide with or rewrite an
	// existing row for the same key.
	var clash int64
	if err := db.Model(&PriceBook{}).
		Where("model = ? AND vendor = ? AND tier = ? AND version = ?", req.Model, req.Vendor, req.Tier, version).
		Count(&clash).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query price book"))
		return
	}
	if clash > 0 {
		common.ApiErrorStatus(c, http.StatusConflict, errors.New("price version already exists for this model/vendor/tier"))
		return
	}
	row := PriceBook{
		Model:                  req.Model,
		Vendor:                 req.Vendor,
		Tier:                   req.Tier,
		InputMicroPerMTok:      req.InputMicroPerMTok,
		OutputMicroPerMTok:     req.OutputMicroPerMTok,
		CacheReadMicroPerMTok:  req.CacheReadMicroPerMTok,
		CacheWriteMicroPerMTok: req.CacheWriteMicroPerMTok,
		Version:                version,
		EffectiveAt:            effectiveAt,
	}
	if err := db.Create(&row).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to create price row"))
		return
	}
	common.ApiSuccess(c, row)
}

// adminListPrices serves GET /admin/prices?model= — the price-book rows,
// newest version first. Without model it lists every row.
func adminListPrices(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	q := db.Model(&PriceBook{})
	if model := c.Query("model"); model != "" {
		q = q.Where("model = ?", model)
	}
	var rows []PriceBook
	if err := q.Order("model ASC").Order("vendor ASC").Order("tier ASC").Order("version DESC").Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query price book"))
		return
	}
	common.ApiSuccess(c, rows)
}

// ---------------------------------------------------------------------------
// 节假日 (§3.3): 后台按年导入 / 列表.
// ---------------------------------------------------------------------------

// adminPostHolidays serves POST /admin/holidays — the body is an array of
// {"date":"YYYY-MM-DD","kind":"holiday|workday"} upserted via ImportHolidays.
func adminPostHolidays(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	var entries []Holiday
	if err := c.ShouldBindJSON(&entries); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body: expected an array of holidays"))
		return
	}
	if err := ImportHolidays(db, entries); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		return
	}
	common.ApiSuccess(c, gin.H{"imported": len(entries)})
}

// adminListHolidays serves GET /admin/holidays?year= — the holiday calendar,
// optionally filtered to one year (YYYY).
func adminListHolidays(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	q := db.Model(&Holiday{})
	if year := c.Query("year"); year != "" {
		if len(year) != 4 {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("year must be YYYY"))
			return
		}
		q = q.Where("date LIKE ?", year+"%")
	}
	var rows []Holiday
	if err := q.Order("date ASC").Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query holidays"))
		return
	}
	common.ApiSuccess(c, rows)
}

// ---------------------------------------------------------------------------
// 挂单审核 (§3.5): 列表（不含密文/全指纹）+ ban/unban 状态机.
// ---------------------------------------------------------------------------

// adminListing is one row of the admin listing review view. It carries the
// seller id and only the first 8 hex chars of the key fingerprint — never the
// ciphertext or the full fingerprint (§2.3).
type adminListing struct {
	ID               int64     `json:"id"`
	SellerID         int64     `json:"seller_id"`
	ChannelID        int64     `json:"channel_id"`
	Vendor           string    `json:"vendor"`
	Models           []string  `json:"models"`
	MultiplierMilli  int64     `json:"multiplier_milli"`
	ConcurrencyLimit int       `json:"concurrency_limit"`
	RPMLimit         int       `json:"rpm_limit"`
	DailyCapMicro    int64     `json:"daily_cap_micro"`
	Status           string    `json:"status"`
	KeyFingerprint8  string    `json:"key_fingerprint8"`
	CreatedAt        time.Time `json:"created_at"`
}

func toAdminListing(l *Listing) adminListing {
	fp8 := ""
	if len(l.KeyFingerprint) >= 8 {
		fp8 = l.KeyFingerprint[:8]
	}
	return adminListing{
		ID:               l.ID,
		SellerID:         l.SellerID,
		ChannelID:        l.ChannelID,
		Vendor:           l.Vendor,
		Models:           l.Models,
		MultiplierMilli:  l.MultiplierMilli,
		ConcurrencyLimit: l.ConcurrencyLimit,
		RPMLimit:         l.RPMLimit,
		DailyCapMicro:    l.DailyCapMicro,
		Status:           l.Status,
		KeyFingerprint8:  fp8,
		CreatedAt:        l.CreatedAt,
	}
}

// adminListListings serves GET /admin/listings?status= — all listings, with
// the fingerprint truncated to 8 chars and the ciphertext never selected.
func adminListListings(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	q := db.Model(&Listing{}).Select("id", "seller_id", "channel_id", "vendor", "models",
		"multiplier_milli", "concurrency_limit", "rpm_limit", "daily_cap_micro",
		"status", "key_fingerprint", "created_at")
	if status := c.Query("status"); status != "" {
		q = q.Where("status = ?", status)
	}
	var rows []Listing
	if err := q.Order("id DESC").Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query listings"))
		return
	}
	out := make([]adminListing, 0, len(rows))
	for i := range rows {
		out = append(out, toAdminListing(&rows[i]))
	}
	common.ApiSuccess(c, out)
}

// adminBanListing serves POST /admin/listings/:id/ban — live/paused/canary/
// verifying → banned (terminal, §3.5).
func adminBanListing(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	id, ok := adminIDParam(c)
	if !ok {
		return
	}
	if err := BanListing(db, id); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		return
	}
	common.ApiSuccess(c, gin.H{"id": id, "status": ListingBanned})
}

// adminUnbanListing serves POST /admin/listings/:id/unban — banned → paused
// (人工恢复; the listing must be re-verified before serving traffic again).
func adminUnbanListing(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	id, ok := adminIDParam(c)
	if !ok {
		return
	}
	if err := TransitionListing(db, id, ListingBanned, ListingPaused); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		return
	}
	common.ApiSuccess(c, gin.H{"id": id, "status": ListingPaused})
}

// ---------------------------------------------------------------------------
// 提现审批 (§3.9): 列表 + approve / reject / mark-paid, 全部走 payout.go.
// ---------------------------------------------------------------------------

// adminListPayouts serves GET /admin/payouts?status= — the withdrawal queue.
func adminListPayouts(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	q := db.Model(&Payout{})
	if status := c.Query("status"); status != "" {
		q = q.Where("status = ?", status)
	}
	var rows []Payout
	if err := q.Order("id DESC").Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query payouts"))
		return
	}
	common.ApiSuccess(c, rows)
}

// adminPayoutAction runs one payout state transition with the reviewer id
// taken from the admin session.
func adminPayoutAction(c *gin.Context, fn func(db *gorm.DB, payoutID, reviewerID int64, now time.Time) error) {
	db := adminDB(c)
	if db == nil {
		return
	}
	id, ok := adminIDParam(c)
	if !ok {
		return
	}
	reviewerID := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	if err := fn(db, id, reviewerID, time.Now()); err != nil {
		switch {
		case errors.Is(err, ErrPayoutNotFound):
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("payout not found"))
		case errors.Is(err, ErrPayoutNotPending):
			common.ApiErrorStatus(c, http.StatusConflict, errors.New("payout is not in a pending state"))
		default:
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		}
		return
	}
	common.ApiSuccess(c, gin.H{"id": id})
}

func adminApprovePayout(c *gin.Context) {
	adminPayoutAction(c, ApprovePayout)
}

func adminRejectPayout(c *gin.Context) {
	adminPayoutAction(c, RejectPayout)
}

func adminMarkPayoutPaid(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	id, ok := adminIDParam(c)
	if !ok {
		return
	}
	if err := MarkPayoutPaid(db, id, time.Now()); err != nil {
		switch {
		case errors.Is(err, ErrPayoutNotFound):
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("payout not found"))
		case errors.Is(err, ErrPayoutNotPending):
			common.ApiErrorStatus(c, http.StatusConflict, errors.New("payout is not in a pending state"))
		default:
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(err.Error()))
		}
		return
	}
	common.ApiSuccess(c, gin.H{"id": id})
}

// ---------------------------------------------------------------------------
// 冲正 (§3.9): 冻结期内冲正一笔已结算请求, 返回冲正后各账户余额.
// ---------------------------------------------------------------------------

// adminReverse serves POST /admin/reverse {request_id} → ledger.Reverse. On
// success it returns the post-reversal balances of the buyer, seller and
// platform accounts involved. ErrNotReversible / ErrAlreadyReversed → 400.
func adminReverse(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	var req struct {
		RequestID string `json:"request_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if req.RequestID == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("request_id is required"))
		return
	}
	if err := Reverse(db, req.RequestID, time.Now()); err != nil {
		switch {
		case errors.Is(err, ErrNotReversible):
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("request is not reversible (matured or unknown)"))
		case errors.Is(err, ErrAlreadyReversed):
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("request already reversed"))
		default:
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to reverse request"))
		}
		return
	}
	// Return the post-reversal balances of every account touched by the
	// request, so the admin can verify the reversal netted to zero.
	var rows []LedgerEntry
	if err := db.Where("request_id = ?", req.RequestID).Find(&rows).Error; err != nil {
		common.ApiSuccess(c, gin.H{"reversed": true})
		return
	}
	accounts := make(map[string]Micro, 3)
	for _, r := range rows {
		if _, ok := accounts[r.Account]; !ok {
			avail, pending, err := AccountBalance(db, r.Account)
			if err != nil {
				continue
			}
			accounts[r.Account] = Micro(int64(avail) + int64(pending))
		}
	}
	common.ApiSuccess(c, gin.H{"reversed": true, "balances": accounts})
}

// ---------------------------------------------------------------------------
// 代号反查 (§3.1): 后台可反查完整对应关系 — 给定 seller_id 与匿名代号, 返回该
// 分享者视角下对应买家的真实 user id. 找不到 → 404.
// ---------------------------------------------------------------------------

// adminDeanon serves GET /admin/deanon?seller_id=&code=. It iterates the
// distinct buyers who have requests against that seller and returns the buyer
// whose AnonBuyerCode(seller_id, buyer) matches code.
func adminDeanon(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	sellerID, err := strconv.ParseInt(c.Query("seller_id"), 10, 64)
	if err != nil || sellerID <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("seller_id must be a positive integer"))
		return
	}
	code := c.Query("code")
	if code == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("code is required"))
		return
	}
	var buyers []int64
	if err := db.Model(&Request{}).
		Where("seller_id = ?", sellerID).
		Distinct("buyer_user_id").
		Pluck("buyer_user_id", &buyers).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query requests"))
		return
	}
	for _, buyer := range buyers {
		got, err := AnonBuyerCode(sellerID, buyer)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
			return
		}
		if got == code {
			common.ApiSuccess(c, gin.H{"seller_id": sellerID, "buyer_user_id": buyer, "code": code})
			return
		}
	}
	common.ApiErrorStatus(c, http.StatusNotFound, errors.New("no buyer matches this code for the seller"))
}
