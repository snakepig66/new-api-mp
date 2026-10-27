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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// RegisterAdminRootRoutes is the exported seam for the marketplace admin API.
// The sensitive actions (提现审批 / 冲正 / 代号反查) require Root privileges
// (L5): the caller must mount this group with middleware.RootAuth(). The
// handlers additionally re-check the role from the context as defense in depth,
// so accidentally wiring AdminAuth still rejects non-root administrators.
func RegisterAdminRootRoutes(g *gin.RouterGroup) { registerAdminRootRoutes(g) }

// RegisterAdminRoutes is the legacy name kept so the current router seam keeps
// compiling during the parallel refactor. New wiring should call
// RegisterAdminRootRoutes on a RootAuth group.
//
// Deprecated: use RegisterAdminRootRoutes.
func RegisterAdminRoutes(g *gin.RouterGroup) { registerAdminRootRoutes(g) }

// registerAdminRootRoutes mounts the §5 阶段 6 admin endpoints on g. Route
// wiring into RegisterRoutes is done centrally in api.go.
func registerAdminRootRoutes(g *gin.RouterGroup) {
	g.POST("/admin/prices", adminPostPrice)
	g.GET("/admin/prices", adminListPrices)
	g.POST("/admin/per-call-prices", adminPostPerCallPrice)
	g.GET("/admin/per-call-prices", adminListPerCallPrices)
	g.POST("/admin/holidays", adminPostHolidays)
	g.GET("/admin/holidays", adminListHolidays)
	g.GET("/admin/listings", adminListListings)
	// 待审队列 (P2) + 通过/拒绝.
	g.GET("/admin/listings/pending", adminListPendingListings)
	g.POST("/admin/listings/:id/approve", adminApproveListing)
	g.POST("/admin/listings/:id/reject", adminRejectListing)
	g.POST("/admin/listings/:id/ban", adminBanListing)
	g.POST("/admin/listings/:id/unban", adminUnbanListing)
	// 全链路排查 (P5): one request id → request, retry chain, ledger, probes.
	g.GET("/admin/requests/:request_id", adminRequestTrace)
	g.GET("/admin/payouts", adminListPayouts)
	g.POST("/admin/payouts/:id/approve", adminApprovePayout)
	g.POST("/admin/payouts/:id/reject", adminRejectPayout)
	g.POST("/admin/payouts/:id/mark-paid", adminMarkPayoutPaid)
	g.POST("/admin/reverse", adminReverse)
	g.GET("/admin/deanon", adminDeanon)
	// 渠道模型映射表: 平台标准模型 ID → 上游模型名. Editing this table controls
	// which托管模型 can be listed, so it shares the same root-only gate.
	g.GET("/admin/channel-models", adminListChannelModels)
	g.PUT("/admin/channel-models", adminPutChannelModel)
	g.DELETE("/admin/channel-models", adminDeleteChannelModel)
	// 模型池 / 渠道池 (P1): mp_models / mp_channels / mp_channel_models.
	g.GET("/admin/pool/models", adminPoolListModels)
	g.PUT("/admin/pool/models", adminPoolPutModel)
	g.DELETE("/admin/pool/models", adminPoolDeleteModel)
	g.GET("/admin/pool/channels", adminPoolListChannels)
	g.PUT("/admin/pool/channels", adminPoolPutChannel)
	g.DELETE("/admin/pool/channels", adminPoolDeleteChannel)
	g.GET("/admin/pool/channel-models", adminPoolListChannelModels)
	g.PUT("/admin/pool/channel-models", adminPoolPutChannelModel)
	g.DELETE("/admin/pool/channel-models", adminPoolDeleteChannelModel)
	// 认证池 (P5): operator CRUD over mp_trust_pool plus the governance gate,
	// the append-only audit trail and the P5-B two-person confirmation queue.
	g.GET("/admin/trust-pool", adminTrustPoolList)
	g.GET("/admin/trust-pool/approvals", adminTrustPoolApprovalList)
	g.POST("/admin/trust-pool/approvals/:id/approve", adminTrustPoolApprovalApprove)
	g.POST("/admin/trust-pool/approvals/:id/reject", adminTrustPoolApprovalReject)
	g.GET("/admin/trust-pool/:id", adminTrustPoolGet)
	g.POST("/admin/trust-pool", adminTrustPoolCreate)
	g.PUT("/admin/trust-pool/:id", adminTrustPoolUpdate)
	g.DELETE("/admin/trust-pool/:id", adminTrustPoolDelete)
	// 派生指标 (P5): mp_ops_metrics with metric_name / since / dimensions
	// filters.
	g.GET("/admin/metrics", adminListOpsMetrics)
}

// adminRequireRoot is the L5 defense-in-depth gate for 提现审批 / 冲正 /
// 代号反查. RootAuth already enforces it at the group level; this check keeps
// the endpoints safe if they are ever mounted on an AdminAuth group.
func adminRequireRoot(c *gin.Context) bool {
	if common.GetContextKeyInt(c, constant.ContextKeyUserId) <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return false
	}
	if c.GetInt("role") < common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, errors.New("root privileges required"))
		return false
	}
	return true
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
	// M11: the tier is an enum; an unknown value would silently price nothing.
	if req.Tier != TierPeak && req.Tier != TierOffpeak && req.Tier != TierFlat {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("tier must be peak, offpeak or flat"))
		return
	}
	if req.InputMicroPerMTok < 0 || req.OutputMicroPerMTok < 0 ||
		req.CacheReadMicroPerMTok < 0 || req.CacheWriteMicroPerMTok < 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("prices must be >= 0"))
		return
	}
	now := time.Now()
	effectiveAt := now
	if req.EffectiveAt != "" {
		t, err := time.Parse(time.RFC3339, req.EffectiveAt)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("effective_at must be RFC3339"))
			return
		}
		// M11/§3.3: a backdated version would split one request's pre-freeze
		// and settlement across two price versions, so it is rejected.
		if t.Before(now) {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("effective_at must not be earlier than now"))
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

// adminListPrices serves GET /admin/prices?model=&vendor=&tier= — the
// price-book rows, newest version first. Without filters it lists every row.
func adminListPrices(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	q := db.Model(&PriceBook{})
	if model := c.Query("model"); model != "" {
		q = q.Where("model = ?", model)
	}
	if vendor := c.Query("vendor"); vendor != "" {
		q = q.Where("vendor = ?", vendor)
	}
	if tier := c.Query("tier"); tier != "" {
		q = q.Where("tier = ?", tier)
	}
	var rows []PriceBook
	if err := q.Order("model ASC").Order("vendor ASC").Order("tier ASC").Order("version DESC").Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query price book"))
		return
	}
	common.ApiSuccess(c, rows)
}

// ---------------------------------------------------------------------------
// 按次价目 (§3.4): 后台维护 Search / Fetch 的官方单价, 同样只增不改.
// ---------------------------------------------------------------------------

// adminPerCallPriceRequest is the POST /admin/per-call-prices body. Prices
// are 微元/次; version is optional and auto-incremented like token prices.
type adminPerCallPriceRequest struct {
	Vendor         string `json:"vendor"`
	Item           string `json:"item"` // search / fetch
	UnitPriceMicro int64  `json:"unit_price_micro"`
	Version        int64  `json:"version"`
	EffectiveAt    string `json:"effective_at"`
}

func nextPerCallVersion(db *gorm.DB, vendor, item string) (int64, error) {
	var maxV *int64
	if err := db.Model(&PerCallPrice{}).
		Where("vendor = ? AND item = ?", vendor, item).
		Select("COALESCE(MAX(version), 0)").Scan(&maxV).Error; err != nil {
		return 0, err
	}
	if maxV == nil {
		return 1, nil
	}
	return *maxV + 1, nil
}

// adminPostPerCallPrice serves POST /admin/per-call-prices (§3.4/§12 M11).
func adminPostPerCallPrice(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	var req adminPerCallPriceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if req.Vendor == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("vendor is required"))
		return
	}
	if req.Item != ToolItemSearch && req.Item != ToolItemFetch {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("item must be search or fetch"))
		return
	}
	if req.UnitPriceMicro < 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("unit_price_micro must be >= 0"))
		return
	}
	now := time.Now()
	effectiveAt := now
	if req.EffectiveAt != "" {
		t, err := time.Parse(time.RFC3339, req.EffectiveAt)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("effective_at must be RFC3339"))
			return
		}
		if t.Before(now) {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("effective_at must not be earlier than now"))
			return
		}
		effectiveAt = t
	}
	version := req.Version
	if version <= 0 {
		next, err := nextPerCallVersion(db, req.Vendor, req.Item)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to assign per-call price version"))
			return
		}
		version = next
	}
	var clash int64
	if err := db.Model(&PerCallPrice{}).
		Where("vendor = ? AND item = ? AND version = ?", req.Vendor, req.Item, version).
		Count(&clash).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query per-call prices"))
		return
	}
	if clash > 0 {
		common.ApiErrorStatus(c, http.StatusConflict, errors.New("per-call price version already exists for this vendor/item"))
		return
	}
	row := PerCallPrice{
		Vendor:         req.Vendor,
		Item:           req.Item,
		Version:        version,
		UnitPriceMicro: req.UnitPriceMicro,
		EffectiveAt:    effectiveAt,
	}
	if err := db.Create(&row).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to create per-call price row"))
		return
	}
	common.ApiSuccess(c, row)
}

// adminListPerCallPrices serves GET /admin/per-call-prices?vendor=&item=.
func adminListPerCallPrices(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	q := db.Model(&PerCallPrice{})
	if vendor := c.Query("vendor"); vendor != "" {
		q = q.Where("vendor = ?", vendor)
	}
	if item := c.Query("item"); item != "" {
		q = q.Where("item = ?", item)
	}
	var rows []PerCallPrice
	if err := q.Order("vendor ASC").Order("item ASC").Order("version DESC").Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query per-call prices"))
		return
	}
	common.ApiSuccess(c, rows)
}

// ---------------------------------------------------------------------------
// 节假日 (§3.3): 后台按年导入 / 列表.
// ---------------------------------------------------------------------------

// adminPostHolidays serves POST /admin/holidays — the body is an array of
// {"date":"YYYY-MM-DD","kind":"holiday|workday"} upserted via ImportHolidays.
// M11: the in-process calendar is reloaded immediately so the new dates take
// effect on the next request instead of waiting for the hourly job.
func adminPostHolidays(c *gin.Context) {
	db, cal := relayState()
	if db == nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
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
	if cal == nil {
		// relayState loads lazily; this branch is defensive.
		loaded, err := LoadCalendar(db)
		if err != nil {
			common.SysError(common.LogText("marketplace: reload calendar after import: %s", err.Error()))
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("holidays imported but calendar reload failed"))
			return
		}
		cal = loaded
	}
	if err := cal.Reload(db); err != nil {
		common.SysError(common.LogText("marketplace: reload calendar after import: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("holidays imported but calendar reload failed"))
		return
	}
	common.ApiSuccess(c, gin.H{"imported": len(entries)})
}

// nextCalendarYear returns the next year in Asia/Shanghai, the calendar the
// holiday table is keyed on.
func nextCalendarYear(now time.Time) int {
	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		return now.Year() + 1
	}
	return now.In(loc).Year() + 1
}

// holidayYearImported reports whether at least one date of the given year is
// in mp_holidays.
func holidayYearImported(db *gorm.DB, year int) (bool, error) {
	var count int64
	prefix := strconv.Itoa(year) + "%"
	if err := db.Model(&Holiday{}).Where("date LIKE ?", prefix).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// adminListHolidays serves GET /admin/holidays?year= — the holiday calendar,
// optionally filtered to one year (YYYY), plus the M11 明年日历导入告警 flag.
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
	nextYear := nextCalendarYear(time.Now())
	nextImported, err := holidayYearImported(db, nextYear)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query next year's holidays"))
		return
	}
	common.ApiSuccess(c, gin.H{
		"holidays":           rows,
		"year":               c.Query("year"),
		"next_year":          nextYear,
		"next_year_imported": nextImported,
	})
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
	notifySeller(db, id, NotificationKindReview, "渠道已下架",
		"渠道因审核未通过被下架。如有疑问请联系运营。", "/console/listings")
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
	notifySeller(db, id, NotificationKindReview, "渠道已恢复",
		"渠道已恢复为暂停状态，请重新检测后恢复接单。", "/console/listings")
	common.ApiSuccess(c, gin.H{"id": id, "status": ListingPaused})
}

// ---------------------------------------------------------------------------
// 运营审核 (P2): 待审队列 + 通过/拒绝.
//
// A seller-defined upstream the trust pool does not trust is created in
// pending_review (listing.go); only an operator may release it into the
// automated verification flow or return it to the seller. The decision is
// recorded as an mp_probes row of type "review" so the review trail sits next
// to verification and patrol evidence.
// ---------------------------------------------------------------------------

// ProbeTypeReview marks an operator review decision in mp_probes.
const ProbeTypeReview = "review"

// adminPendingListing is one row of the review queue. custom_base_url is set
// only for a seller-defined channel; platform channels already have a fixed
// address. No key material is exposed (§2.3).
type adminPendingListing struct {
	ID                   int64     `json:"id"`
	SellerID             int64     `json:"seller_id"`
	Vendor               string    `json:"vendor"`
	CustomBaseURL        string    `json:"custom_base_url"`
	Models               []string  `json:"models"`
	CreatedAt            time.Time `json:"created_at"`
	SellerRealNameStatus string    `json:"seller_real_name_status"`
}

// adminPendingHostGroup is one host bucket of the review queue. Host is the
// hostname of the seller-supplied upstream, or "" for a platform channel that
// has no seller-defined address. Listings keep the queue's newest-first order.
type adminPendingHostGroup struct {
	Host     string                `json:"host"`
	Count    int                   `json:"count"`
	Listings []adminPendingListing `json:"listings"`
}

// pendingListingHost extracts the hostname used to group the review queue from
// a stored custom_base_url. A malformed or empty URL groups under "".
func pendingListingHost(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// adminListPendingListings serves GET /admin/listings/pending — every
// pending_review listing, grouped by the host of its seller-defined upstream,
// newest first inside each group. The real-name status of each seller is
// joined in so the operator can see whether the account is verified; seller
// relation/overlap ranking is out of scope for this phase.
func adminListPendingListings(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	var rows []Listing
	if err := db.Select("id", "seller_id", "vendor", "models", "created_at").
		Where("status = ?", ListingPendingReview).
		Order("created_at DESC").Order("id DESC").
		Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query pending listings"))
		return
	}
	groups := make([]adminPendingHostGroup, 0)
	if len(rows) == 0 {
		common.ApiSuccess(c, groups)
		return
	}
	sellerIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		sellerIDs = append(sellerIDs, row.SellerID)
	}
	var sellers []Seller
	if err := db.Select("user_id", "real_name_status").
		Where("user_id IN ?", sellerIDs).Find(&sellers).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query sellers"))
		return
	}
	realName := make(map[int64]string, len(sellers))
	for _, s := range sellers {
		realName[s.UserID] = s.RealNameStatus
	}
	groupAt := make(map[string]int, len(rows))
	for i := range rows {
		item := adminPendingListing{
			ID:                   rows[i].ID,
			SellerID:             rows[i].SellerID,
			Vendor:               rows[i].Vendor,
			Models:               rows[i].Models,
			CreatedAt:            rows[i].CreatedAt,
			SellerRealNameStatus: realName[rows[i].SellerID],
		}
		host := ""
		if isCustomChannelSlug(rows[i].Vendor) {
			if vendor, ok := lookupVendorForDB(db, rows[i].Vendor); ok {
				item.CustomBaseURL = VendorOpenAIBase(vendor)
				host = pendingListingHost(item.CustomBaseURL)
			}
		}
		pos, ok := groupAt[host]
		if !ok {
			groups = append(groups, adminPendingHostGroup{Host: host, Listings: make([]adminPendingListing, 0)})
			pos = len(groups) - 1
			groupAt[host] = pos
		}
		groups[pos].Listings = append(groups[pos].Listings, item)
		groups[pos].Count++
	}
	// Busiest host first so the operator sees the pile that needs attention;
	// host asc breaks a tie deterministically.
	slices.SortStableFunc(groups, func(a, b adminPendingHostGroup) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Host, b.Host)
	})
	common.ApiSuccess(c, groups)
}

// recordListingReview writes one operator decision to mp_probes. A failure is
// logged but never undoes or reports failure for the state transition that has
// already committed: the audit write is a side effect, like a notification.
func recordListingReview(db *gorm.DB, listingID, reviewerID int64, action, notes, previous, next string) {
	detail, err := common.Marshal(map[string]any{
		"reviewer_id":     reviewerID,
		"action":          action,
		"notes":           notes,
		"previous_status": previous,
		"new_status":      next,
	})
	if err != nil {
		common.SysError(common.LogText("marketplace: record review for listing %d: %s", listingID, err.Error()))
		return
	}
	probe := Probe{
		ListingID: listingID,
		Type:      ProbeTypeReview,
		OK:        action == "approve",
		Detail:    string(detail),
		CreatedAt: time.Now(),
	}
	if err := db.Create(&probe).Error; err != nil {
		common.SysError(common.LogText("marketplace: record review for listing %d: %s", listingID, err.Error()))
	}
}

// adminListingReviewError maps a state-machine error to the review API status.
func adminListingReviewError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrListingNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, errors.New("listing not found"))
	case errors.Is(err, ErrInvalidTransition):
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("listing is not pending review"))
	default:
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to update listing"))
	}
}

// adminApproveListing serves POST /admin/listings/:id/approve —
// pending_review → verifying, then the standard verification runs in the
// background. An optional {"notes": "..."} body is stored with the decision.
func adminApproveListing(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	id, ok := adminIDParam(c)
	if !ok {
		return
	}
	var req struct {
		Notes string `json:"notes"`
	}
	if c.Request != nil && c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
			return
		}
	}
	if err := StartVerification(db, id); err != nil {
		adminListingReviewError(c, err)
		return
	}
	reviewerID := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	recordListingReview(db, id, reviewerID, "approve", req.Notes, ListingPendingReview, ListingVerifying)
	go verifyApprovedListingAsync(db, id)
	common.ApiSuccess(c, gin.H{"id": id, "status": ListingVerifying})
}

// adminRejectListing serves POST /admin/listings/:id/reject with
// {"action": "draft" | "ban", "notes": "..."}. draft returns the listing to the
// seller for correction; ban is the terminal edge for a severe violation.
func adminRejectListing(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	id, ok := adminIDParam(c)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
		Notes  string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	var next string
	switch req.Action {
	case "draft":
		next = ListingDraft
	case "ban":
		next = ListingBanned
	default:
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New(`action must be "draft" or "ban"`))
		return
	}
	var err error
	if next == ListingBanned {
		err = BanListing(db, id)
	} else {
		err = RejectListing(db, id)
	}
	if err != nil {
		adminListingReviewError(c, err)
		return
	}
	reviewerID := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	recordListingReview(db, id, reviewerID, "reject", req.Notes, ListingPendingReview, next)
	common.ApiSuccess(c, gin.H{"id": id, "status": next})
}

// ---------------------------------------------------------------------------
// 全链路排查 (P5): 给定 request_id, 返回请求 → 选择/每一跳 → 结算 → 验真/巡检
// 的完整视图. Root-only: the trace crosses buyer, seller and ledger accounts,
// so it is an operator (L5) view, not a customer record API.
// ---------------------------------------------------------------------------

// adminTraceProbeLimit caps the per-listing verify/patrol/review history shown
// with one request. A busy listing can accumulate thousands of probes; the
// trace only needs the recent evidence around the request.
const adminTraceProbeLimit = 20

// adminRequestTraceView is the full mp_requests row in snake_case for the
// console. The retry_chain JSON text is returned separately as parsed hops.
type adminRequestTraceView struct {
	ID                      int64     `json:"id"`
	RequestID               string    `json:"request_id"`
	BuyerUserID             int64     `json:"buyer_user_id"`
	BuyerTokenID            int64     `json:"buyer_token_id"`
	SellerID                int64     `json:"seller_id"`
	ListingID               int64     `json:"listing_id"`
	Model                   string    `json:"model"`
	UpstreamModel           string    `json:"upstream_model"`
	ModelFingerprintAnomaly bool      `json:"model_fingerprint_anomaly"`
	Tier                    string    `json:"tier"`
	PriceVersion            int64     `json:"price_version"`
	MultiplierMilli         int64     `json:"multiplier_milli"`
	InputTokens             int64     `json:"input_tokens"`
	OutputTokens            int64     `json:"output_tokens"`
	CacheReadTokens         int64     `json:"cache_read_tokens"`
	CacheWriteTokens        int64     `json:"cache_write_tokens"`
	SearchCount             int64     `json:"search_count"`
	FetchCount              int64     `json:"fetch_count"`
	TokenDebitMicro         int64     `json:"token_debit_micro"`
	ToolDebitMicro          int64     `json:"tool_debit_micro"`
	TotalDebitMicro         int64     `json:"total_debit_micro"`
	GiftDebitMicro          int64     `json:"gift_debit_micro"`
	CappedMicro             int64     `json:"capped_micro"`
	PlatformFeeMicro        int64     `json:"platform_fee_micro"`
	SellerCreditMicro       int64     `json:"seller_credit_micro"`
	OfficialCostMicro       int64     `json:"official_cost_micro"`
	UsageEstimated          bool      `json:"usage_estimated"`
	Status                  string    `json:"status"`
	LatencyMs               int64     `json:"latency_ms"`
	CreatedAt               time.Time `json:"created_at"`
}

// adminLedgerTraceView is one mp_ledger row for the trace table.
type adminLedgerTraceView struct {
	ID              int64      `json:"id"`
	RequestID       string     `json:"request_id"`
	EntryType       string     `json:"entry_type"`
	Account         string     `json:"account"`
	ListingID       int64      `json:"listing_id"`
	AmountMicro     int64      `json:"amount_micro"`
	PriceVersion    int64      `json:"price_version"`
	MultiplierMilli int64      `json:"multiplier_milli"`
	Status          string     `json:"status"`
	AvailableAt     *time.Time `json:"available_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

// adminProbeTraceView is one verification/patrol/review evidence row.
type adminProbeTraceView struct {
	ID        int64     `json:"id"`
	ListingID int64     `json:"listing_id"`
	Type      string    `json:"type"`
	OK        bool      `json:"ok"`
	LatencyMs int64     `json:"latency_ms"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// adminRequestTrace is the GET /admin/requests/:request_id response.
type adminRequestTraceResponse struct {
	Request    adminRequestTraceView  `json:"request"`
	RetryChain []RelayHop             `json:"retry_chain"`
	Ledger     []adminLedgerTraceView `json:"ledger"`
	Probes     []adminProbeTraceView  `json:"probes"`
}

func toAdminRequestTraceView(r *Request) adminRequestTraceView {
	return adminRequestTraceView{
		ID:                      r.ID,
		RequestID:               r.RequestID,
		BuyerUserID:             r.BuyerUserID,
		BuyerTokenID:            r.BuyerTokenID,
		SellerID:                r.SellerID,
		ListingID:               r.ListingID,
		Model:                   r.Model,
		UpstreamModel:           r.UpstreamModel,
		ModelFingerprintAnomaly: r.ModelFingerprintAnomaly,
		Tier:                    r.Tier,
		PriceVersion:            r.PriceVersion,
		MultiplierMilli:         r.MultiplierMilli,
		InputTokens:             r.InputTokens,
		OutputTokens:            r.OutputTokens,
		CacheReadTokens:         r.CacheReadTokens,
		CacheWriteTokens:        r.CacheWriteTokens,
		SearchCount:             r.SearchCount,
		FetchCount:              r.FetchCount,
		TokenDebitMicro:         r.TokenDebitMicro,
		ToolDebitMicro:          r.ToolDebitMicro,
		TotalDebitMicro:         r.TotalDebitMicro,
		GiftDebitMicro:          r.GiftDebitMicro,
		CappedMicro:             r.CappedMicro,
		PlatformFeeMicro:        r.PlatformFeeMicro,
		SellerCreditMicro:       r.SellerCreditMicro,
		OfficialCostMicro:       r.OfficialCostMicro,
		UsageEstimated:          r.UsageEstimated,
		Status:                  r.Status,
		LatencyMs:               r.LatencyMs,
		CreatedAt:               r.CreatedAt,
	}
}

// adminRequestTrace serves GET /admin/requests/:request_id.
func adminRequestTrace(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	requestID := strings.TrimSpace(c.Param("request_id"))
	if requestID == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("request_id is required"))
		return
	}
	var row Request
	if err := db.Where("request_id = ?", requestID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("request not found"))
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query request"))
		return
	}
	hops := make([]RelayHop, 0)
	if strings.TrimSpace(row.RetryChain) != "" {
		if err := common.Unmarshal([]byte(row.RetryChain), &hops); err != nil {
			// A corrupt retry chain must not hide the rest of the trace.
			hops = make([]RelayHop, 0)
		}
	}
	var ledgerRows []LedgerEntry
	if err := db.Where("request_id = ?", requestID).Order("id ASC").Find(&ledgerRows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query ledger"))
		return
	}
	ledger := make([]adminLedgerTraceView, 0, len(ledgerRows))
	for i := range ledgerRows {
		ledger = append(ledger, adminLedgerTraceView{
			ID:              ledgerRows[i].ID,
			RequestID:       ledgerRows[i].RequestID,
			EntryType:       ledgerRows[i].EntryType,
			Account:         ledgerRows[i].Account,
			ListingID:       ledgerRows[i].ListingID,
			AmountMicro:     ledgerRows[i].AmountMicro,
			PriceVersion:    ledgerRows[i].PriceVersion,
			MultiplierMilli: ledgerRows[i].MultiplierMilli,
			Status:          ledgerRows[i].Status,
			AvailableAt:     ledgerRows[i].AvailableAt,
			CreatedAt:       ledgerRows[i].CreatedAt,
		})
	}
	probes := make([]adminProbeTraceView, 0)
	if row.ListingID > 0 {
		var probeRows []Probe
		if err := db.Where("listing_id = ? AND type IN ?", row.ListingID,
			[]string{ProbeTypeVerify, ProbeTypePatrol, ProbeTypeReview}).
			Order("created_at DESC").Order("id DESC").Limit(adminTraceProbeLimit).
			Find(&probeRows).Error; err != nil {
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query probes"))
			return
		}
		for i := range probeRows {
			probes = append(probes, adminProbeTraceView{
				ID:        probeRows[i].ID,
				ListingID: probeRows[i].ListingID,
				Type:      probeRows[i].Type,
				OK:        probeRows[i].OK,
				LatencyMs: probeRows[i].LatencyMs,
				Detail:    probeRows[i].Detail,
				CreatedAt: probeRows[i].CreatedAt,
			})
		}
	}
	common.ApiSuccess(c, adminRequestTraceResponse{
		Request:    toAdminRequestTraceView(&row),
		RetryChain: hops,
		Ledger:     ledger,
		Probes:     probes,
	})
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

// adminPayoutSellerID loads the payout's owner for the L5 self-review check.
func adminPayoutSellerID(c *gin.Context, db *gorm.DB, id int64) (int64, bool) {
	var p Payout
	if err := db.Select("id", "seller_id").Where("id = ?", id).Take(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("payout not found"))
			return 0, false
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query payout"))
		return 0, false
	}
	return p.SellerID, true
}

// adminPayoutAction runs one payout state transition with the reviewer id
// taken from the admin session. L5: root only, and an administrator may never
// approve/reject their own payout.
func adminPayoutAction(c *gin.Context, fn func(db *gorm.DB, payoutID, reviewerID int64, now time.Time) error) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	id, ok := adminIDParam(c)
	if !ok {
		return
	}
	reviewerID := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	sellerID, ok := adminPayoutSellerID(c, db, id)
	if !ok {
		return
	}
	if sellerID == reviewerID {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("administrators cannot review their own payout"))
		return
	}
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
	var payout Payout
	if err := db.Select("status").Where("id = ?", id).Take(&payout).Error; err == nil {
		switch payout.Status {
		case PayoutApproved:
			notifyUser(db, sellerID, NotificationKindWithdrawal, "提现已通过",
				"提现申请已通过审核，等待打款。", "/console/earnings")
		case PayoutRejected:
			notifyUser(db, sellerID, NotificationKindWithdrawal, "提现未通过",
				"提现申请未通过审核，金额已回到可提现余额。", "/console/earnings")
		}
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
	if !adminRequireRoot(c) {
		return
	}
	id, ok := adminIDParam(c)
	if !ok {
		return
	}
	reviewerID := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	sellerID, ok := adminPayoutSellerID(c, db, id)
	if !ok {
		return
	}
	if sellerID == reviewerID {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("administrators cannot operate on their own payout"))
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
	notifyUser(db, sellerID, NotificationKindWithdrawal, "提现已打款",
		"提现金额已打款，请留意收款账户。", "/console/earnings")
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
	if !adminRequireRoot(c) {
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
	if !adminRequireRoot(c) {
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

// ---------------------------------------------------------------------------
// 渠道模型映射表: 后台维护 平台标准模型 ID → 上游模型名. 只有映射表里的模型
// 才允许上架; 同一 vendor 下的 upstream 唯一 (见 channelmap.go 的说明).
// ---------------------------------------------------------------------------

// adminChannelModelRequest is the PUT /admin/channel-models body.
type adminChannelModelRequest struct {
	Vendor        string `json:"vendor"`
	Model         string `json:"model"`
	UpstreamModel string `json:"upstream_model"`
}

// adminListChannelModels serves GET /admin/channel-models, ordered by
// (vendor, model) so the console renders a stable table.
func adminListChannelModels(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	rows := make([]ChannelModelMap, 0)
	if err := db.Order("vendor ASC").Order("model ASC").Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query channel model map"))
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows})
}

// adminPutChannelModel serves PUT /admin/channel-models — create or update one
// mapping and return the stored row. A duplicate upstream under the same vendor
// is a 409.
func adminPutChannelModel(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	var req adminChannelModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if err := UpsertChannelModel(db, req.Vendor, req.Model, req.UpstreamModel); err != nil {
		switch {
		case errors.Is(err, ErrChannelModelEmpty):
			common.ApiErrorStatus(c, http.StatusBadRequest, ErrChannelModelEmpty)
		case errors.Is(err, ErrChannelModelUpstreamTaken):
			common.ApiErrorStatus(c, http.StatusConflict, ErrChannelModelUpstreamTaken)
		default:
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to save channel model mapping"))
		}
		return
	}
	var row ChannelModelMap
	if err := db.Where("vendor = ? AND model = ?",
		strings.TrimSpace(req.Vendor), strings.TrimSpace(req.Model)).Take(&row).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to load channel model mapping"))
		return
	}
	common.ApiSuccess(c, row)
}

// adminDeleteChannelModel serves DELETE /admin/channel-models?vendor=&model=.
// Deleting an absent mapping still succeeds (idempotent).
func adminDeleteChannelModel(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	vendor := strings.TrimSpace(c.Query("vendor"))
	model := strings.TrimSpace(c.Query("model"))
	if vendor == "" || model == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("vendor and model are required"))
		return
	}
	if err := DeleteChannelModel(db, vendor, model); err != nil {
		if errors.Is(err, ErrChannelModelEmpty) {
			common.ApiErrorStatus(c, http.StatusBadRequest, ErrChannelModelEmpty)
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to delete channel model mapping"))
		return
	}
	common.ApiSuccess(c, gin.H{"vendor": vendor, "model": model})
}

// ---------------------------------------------------------------------------
// 模型池 / 渠道池 (P1) admin API.
//
// The DB stores JSON columns as text; these views parse them into real JSON
// arrays/objects so the console never has to double-decode. No field here ever
// carries key material.
// ---------------------------------------------------------------------------

// flexJSON accepts either a JSON value or a string containing that JSON value
// (the console may send arrays/objects either way). Set records presence so a
// PUT can distinguish "omitted" from "empty".
type flexJSON struct {
	Set bool
	Raw []byte
}

func (f *flexJSON) UnmarshalJSON(b []byte) error {
	f.Set = true
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		f.Raw = nil
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" || s == "null" {
			f.Raw = nil
			return nil
		}
		f.Raw = []byte(s)
		return nil
	}
	f.Raw = append([]byte(nil), trimmed...)
	return nil
}

// flexObjectString validates a flexJSON value as an object (or empty) and
// returns the text stored in the database.
func flexObjectString(f flexJSON) (string, error) {
	if !f.Set || len(f.Raw) == 0 {
		return "", nil
	}
	if !isJSONObject(string(f.Raw)) {
		return "", errors.New("must be a JSON object")
	}
	return string(f.Raw), nil
}

// flexArrayString validates a flexJSON value as an array (or empty) and
// returns the text stored in the database.
func flexArrayString(f flexJSON) (string, error) {
	if !f.Set || len(f.Raw) == 0 {
		return "", nil
	}
	trimmed := bytes.TrimSpace(f.Raw)
	if len(trimmed) == 0 || trimmed[0] != '[' || !json.Valid(trimmed) {
		return "", errors.New("must be a JSON array")
	}
	return string(trimmed), nil
}

// --- views -----------------------------------------------------------------

type adminPoolModelView struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	OriginVendor  string    `json:"origin_vendor"`
	ContextLength int       `json:"context_length"`
	Capabilities  []string  `json:"capabilities"`
	Aliases       []string  `json:"aliases"`
	VerifyPrompt  string    `json:"verify_prompt"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toAdminPoolModelView(row ModelPool) adminPoolModelView {
	return adminPoolModelView{
		ID:            row.ID,
		Name:          row.Name,
		OriginVendor:  row.OriginVendor,
		ContextLength: row.ContextLength,
		Capabilities:  parseStringList(row.Capabilities),
		Aliases:       parseStringList(row.Aliases),
		VerifyPrompt:  row.VerifyPrompt,
		Status:        row.Status,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}

type adminPoolChannelView struct {
	ID                  int64           `json:"id"`
	Slug                string          `json:"slug"`
	Name                string          `json:"name"`
	Kind                string          `json:"kind"`
	Protocols           VendorProtocols `json:"protocols"`
	AllowHostPattern    string          `json:"allow_host_pattern"`
	KeyPattern          string          `json:"key_pattern"`
	DenyPaths           []string        `json:"deny_paths"`
	DenyKeyPrefixes     []string        `json:"deny_key_prefixes"`
	Adaptor             string          `json:"adaptor"`
	StripV1Prefix       bool            `json:"strip_v1_prefix"`
	PassthroughBody     bool            `json:"passthrough_body"`
	QuotaWindow         map[string]any  `json:"quota_window"`
	ExhaustionSignature string          `json:"exhaustion_signature"`
	TermsURL            string          `json:"terms_url"`
	TermsCheckedAt      *time.Time      `json:"terms_checked_at"`
	Status              string          `json:"status"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

func toAdminPoolChannelView(row ChannelPool) adminPoolChannelView {
	protocols := VendorProtocols{}
	if strings.TrimSpace(row.Protocols) != "" {
		_ = json.Unmarshal([]byte(row.Protocols), &protocols)
	}
	return adminPoolChannelView{
		ID:                  row.ID,
		Slug:                row.Slug,
		Name:                row.Name,
		Kind:                row.Kind,
		Protocols:           protocols,
		AllowHostPattern:    row.AllowHostPattern,
		KeyPattern:          row.KeyPattern,
		DenyPaths:           parseStringList(row.DenyPaths),
		DenyKeyPrefixes:     parseStringList(row.DenyKeyPrefixes),
		Adaptor:             row.Adaptor,
		StripV1Prefix:       row.StripV1Prefix,
		PassthroughBody:     row.PassthroughBody,
		QuotaWindow:         parseJSONObject(row.QuotaWindow),
		ExhaustionSignature: row.ExhaustionSignature,
		TermsURL:            row.TermsURL,
		TermsCheckedAt:      row.TermsCheckedAt,
		Status:              row.Status,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
}

type adminPoolChannelModelView struct {
	ID               int64          `json:"id"`
	ChannelSlug      string         `json:"channel_slug"`
	UpstreamModel    string         `json:"upstream_model"`
	Model            string         `json:"model"`
	CoefficientMilli int64          `json:"coefficient_milli"`
	Differences      map[string]any `json:"differences"`
	Verified         bool           `json:"verified"`
	VerifiedAt       *time.Time     `json:"verified_at"`
	FingerprintRatio float64        `json:"fingerprint_ratio"`
	Status           string         `json:"status"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

func toAdminPoolChannelModelView(row ChannelModel) adminPoolChannelModelView {
	return adminPoolChannelModelView{
		ID:               row.ID,
		ChannelSlug:      row.ChannelSlug,
		UpstreamModel:    row.UpstreamModel,
		Model:            row.Model,
		CoefficientMilli: row.CoefficientMilli,
		Differences:      parseJSONObject(row.Differences),
		Verified:         row.Verified,
		VerifiedAt:       row.VerifiedAt,
		FingerprintRatio: row.FingerprintRatio,
		Status:           row.Status,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

// --- model pool ------------------------------------------------------------

// adminPoolModelRequest is the PUT /admin/pool/models body. Name is required;
// the other fields are optional, and omitted optional fields are preserved on
// update.
type adminPoolModelRequest struct {
	Name          string   `json:"name"`
	OriginVendor  string   `json:"origin_vendor"`
	ContextLength *int     `json:"context_length"`
	Capabilities  flexJSON `json:"capabilities"`
	Aliases       flexJSON `json:"aliases"`
	VerifyPrompt  *string  `json:"verify_prompt"`
	Status        string   `json:"status"`
}

// adminPoolListModels serves GET /admin/pool/models.
func adminPoolListModels(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	rows := ModelPoolAll(db)
	items := make([]adminPoolModelView, 0, len(rows))
	for _, row := range rows {
		items = append(items, toAdminPoolModelView(row))
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

// adminPoolPutModel serves PUT /admin/pool/models — upsert one standard model.
func adminPoolPutModel(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	var req adminPoolModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, ErrModelPoolNameRequired)
		return
	}
	status := strings.TrimSpace(req.Status)
	if status != "" && status != PoolStatusActive && status != PoolStatusRetired {
		common.ApiErrorStatus(c, http.StatusBadRequest, ErrModelPoolStatusInvalid)
		return
	}
	capabilities := ""
	if req.Capabilities.Set {
		parsed, err := flexArrayString(req.Capabilities)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("capabilities must be a JSON array"))
			return
		}
		capabilities = parsed
	}
	aliases := ""
	if req.Aliases.Set {
		parsed, err := flexArrayString(req.Aliases)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("aliases must be a JSON array"))
			return
		}
		aliases = parsed
	}

	row, found := ModelPoolByName(db, name)
	if !found {
		row = ModelPool{Name: name, Status: PoolStatusActive}
	}
	if req.OriginVendor != "" || !found {
		row.OriginVendor = strings.TrimSpace(req.OriginVendor)
	}
	if req.ContextLength != nil {
		row.ContextLength = *req.ContextLength
	}
	if req.Capabilities.Set {
		row.Capabilities = capabilities
	}
	if req.Aliases.Set {
		row.Aliases = aliases
	}
	if req.VerifyPrompt != nil {
		row.VerifyPrompt = *req.VerifyPrompt
	}
	if status != "" {
		row.Status = status
	}
	if row.Status == "" {
		row.Status = PoolStatusActive
	}

	if found {
		updates := map[string]any{
			"origin_vendor":  row.OriginVendor,
			"context_length": row.ContextLength,
			"capabilities":   row.Capabilities,
			"aliases":        row.Aliases,
			"verify_prompt":  row.VerifyPrompt,
			"status":         row.Status,
			"updated_at":     time.Now(),
		}
		if err := db.Model(&ModelPool{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to save model"))
			return
		}
		row, _ = ModelPoolByName(db, name)
	} else if err := db.Create(&row).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to save model"))
		return
	}
	common.ApiSuccess(c, toAdminPoolModelView(row))
}

// adminPoolDeleteModel serves DELETE /admin/pool/models?name=.
func adminPoolDeleteModel(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	name := strings.TrimSpace(c.Query("name"))
	if name == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, ErrModelPoolNameRequired)
		return
	}
	if db.Migrator().HasTable(&ChannelModel{}) {
		var used int64
		if err := db.Model(&ChannelModel{}).Where("model = ?", name).Count(&used).Error; err != nil {
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query channel models"))
			return
		}
		if used > 0 {
			common.ApiErrorStatus(c, http.StatusConflict, ErrModelPoolInUse)
			return
		}
	}
	if err := db.Where("name = ?", name).Delete(&ModelPool{}).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to delete model"))
		return
	}
	common.ApiSuccess(c, gin.H{"name": name})
}

// --- channel pool ----------------------------------------------------------

// adminPoolChannelRequest is the PUT /admin/pool/channels body (full replace).
type adminPoolChannelRequest struct {
	Slug                string     `json:"slug"`
	Name                string     `json:"name"`
	Kind                string     `json:"kind"`
	Protocols           flexJSON   `json:"protocols"`
	AllowHostPattern    string     `json:"allow_host_pattern"`
	KeyPattern          string     `json:"key_pattern"`
	DenyPaths           flexJSON   `json:"deny_paths"`
	DenyKeyPrefixes     flexJSON   `json:"deny_key_prefixes"`
	Adaptor             string     `json:"adaptor"`
	StripV1Prefix       bool       `json:"strip_v1_prefix"`
	PassthroughBody     bool       `json:"passthrough_body"`
	QuotaWindow         flexJSON   `json:"quota_window"`
	ExhaustionSignature string     `json:"exhaustion_signature"`
	TermsURL            string     `json:"terms_url"`
	TermsCheckedAt      *time.Time `json:"terms_checked_at"`
	Status              string     `json:"status"`
}

// adminPoolListChannels serves GET /admin/pool/channels.
func adminPoolListChannels(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	rows := ChannelPoolAll(db)
	items := make([]adminPoolChannelView, 0, len(rows))
	for _, row := range rows {
		items = append(items, toAdminPoolChannelView(row))
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

// adminPoolPutChannel serves PUT /admin/pool/channels.
func adminPoolPutChannel(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	var req adminPoolChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	protocols, err := flexObjectString(req.Protocols)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("protocols must be a JSON object"))
		return
	}
	denyPaths, err := flexArrayString(req.DenyPaths)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("deny_paths must be a JSON array"))
		return
	}
	denyKeyPrefixes, err := flexArrayString(req.DenyKeyPrefixes)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("deny_key_prefixes must be a JSON array"))
		return
	}
	quotaWindow, err := flexObjectString(req.QuotaWindow)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("quota_window must be a JSON object"))
		return
	}
	row, err := ChannelPoolCreateOrUpdate(db, ChannelPool{
		Slug:                req.Slug,
		Name:                req.Name,
		Kind:                req.Kind,
		Protocols:           protocols,
		AllowHostPattern:    req.AllowHostPattern,
		KeyPattern:          req.KeyPattern,
		DenyPaths:           denyPaths,
		DenyKeyPrefixes:     denyKeyPrefixes,
		Adaptor:             req.Adaptor,
		StripV1Prefix:       req.StripV1Prefix,
		PassthroughBody:     req.PassthroughBody,
		QuotaWindow:         quotaWindow,
		ExhaustionSignature: req.ExhaustionSignature,
		TermsURL:            req.TermsURL,
		TermsCheckedAt:      req.TermsCheckedAt,
		Status:              req.Status,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrChannelPoolSlugRequired),
			errors.Is(err, ErrChannelPoolKindInvalid),
			errors.Is(err, ErrChannelPoolKeyPatternInvalid),
			errors.Is(err, ErrChannelPoolStatusInvalid):
			common.ApiErrorStatus(c, http.StatusBadRequest, err)
		default:
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to save channel"))
		}
		return
	}
	common.ApiSuccess(c, toAdminPoolChannelView(row))
}

// adminPoolDeleteChannel serves DELETE /admin/pool/channels?slug=.
func adminPoolDeleteChannel(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	slug := strings.TrimSpace(c.Query("slug"))
	if slug == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, ErrChannelPoolSlugRequired)
		return
	}
	if err := ChannelPoolDelete(db, slug); err != nil {
		switch {
		case errors.Is(err, ErrChannelPoolInUse):
			common.ApiErrorStatus(c, http.StatusConflict, ErrChannelPoolInUse)
		case errors.Is(err, ErrChannelPoolSlugRequired):
			common.ApiErrorStatus(c, http.StatusBadRequest, ErrChannelPoolSlugRequired)
		default:
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to delete channel"))
		}
		return
	}
	common.ApiSuccess(c, gin.H{"slug": slug})
}

// --- channel models --------------------------------------------------------

// adminPoolChannelModelRequest is the PUT /admin/pool/channel-models body.
type adminPoolChannelModelRequest struct {
	ChannelSlug      string   `json:"channel_slug"`
	UpstreamModel    string   `json:"upstream_model"`
	Model            string   `json:"model"`
	CoefficientMilli int64    `json:"coefficient_milli"`
	Differences      flexJSON `json:"differences"`
	Verified         *bool    `json:"verified"`
	Status           string   `json:"status"`
}

// adminPoolListChannelModels serves GET /admin/pool/channel-models.
func adminPoolListChannelModels(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	rows := make([]ChannelModel, 0)
	if err := db.Order("channel_slug ASC").Order("upstream_model ASC").Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query channel models"))
		return
	}
	items := make([]adminPoolChannelModelView, 0, len(rows))
	for _, row := range rows {
		items = append(items, toAdminPoolChannelModelView(row))
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

// adminPoolPutChannelModel serves PUT /admin/pool/channel-models.
func adminPoolPutChannelModel(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	var req adminPoolChannelModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	differences := ""
	if req.Differences.Set {
		parsed, err := flexObjectString(req.Differences)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, ErrChannelModelDifferences)
			return
		}
		differences = parsed
	}
	slug := strings.TrimSpace(req.ChannelSlug)
	upstream := strings.TrimSpace(req.UpstreamModel)
	model := strings.TrimSpace(req.Model)

	// Preserve omitted optional fields from the current row.
	existing, found := channelModelByUpstream(db, slug, upstream)
	if !found {
		existing, found = ChannelModelFor(db, slug, model)
	}
	cm := ChannelModel{
		ChannelSlug:      slug,
		UpstreamModel:    upstream,
		Model:            model,
		CoefficientMilli: req.CoefficientMilli,
		Differences:      differences,
		Status:           req.Status,
	}
	if req.Verified != nil {
		cm.Verified = *req.Verified
	} else if found {
		cm.Verified = existing.Verified
	}
	if !req.Differences.Set && found {
		cm.Differences = existing.Differences
	}
	row, err := UpsertChannelModelPool(db, cm)
	if err != nil {
		switch {
		case errors.Is(err, ErrChannelModelConflict):
			common.ApiErrorStatus(c, http.StatusConflict, ErrChannelModelConflict)
		case errors.Is(err, ErrChannelModelFieldsRequired),
			errors.Is(err, ErrChannelModelCoefficient),
			errors.Is(err, ErrChannelModelStatusInvalid),
			errors.Is(err, ErrChannelModelDifferences),
			errors.Is(err, ErrChannelPoolNotFound),
			errors.Is(err, ErrModelPoolNotFound):
			common.ApiErrorStatus(c, http.StatusBadRequest, err)
		default:
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to save channel model"))
		}
		return
	}
	if err := RefreshListingChannelMapping(db, row.ChannelSlug, row.Model, row.UpstreamModel); err != nil {
		common.SysError(common.LogText("marketplace: refresh listing mapping for %s/%s: %s", row.ChannelSlug, row.Model, err.Error()))
	}
	common.ApiSuccess(c, toAdminPoolChannelModelView(row))
}

// adminPoolDeleteChannelModel serves DELETE /admin/pool/channel-models.
func adminPoolDeleteChannelModel(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	slug := strings.TrimSpace(c.Query("channel_slug"))
	upstream := strings.TrimSpace(c.Query("upstream_model"))
	if slug == "" || upstream == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, ErrChannelModelFieldsRequired)
		return
	}
	existing, existed := channelModelByUpstream(db, slug, upstream)
	if err := DeleteChannelModelPool(db, slug, upstream); err != nil {
		if errors.Is(err, ErrChannelModelFieldsRequired) {
			common.ApiErrorStatus(c, http.StatusBadRequest, ErrChannelModelFieldsRequired)
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to delete channel model"))
		return
	}
	if existed {
		if err := RefreshListingChannelMapping(db, slug, existing.Model, ""); err != nil {
			common.SysError(common.LogText("marketplace: drop listing mapping for %s/%s: %s", slug, existing.Model, err.Error()))
		}
	}
	common.ApiSuccess(c, gin.H{"channel_slug": slug, "upstream_model": upstream})
}

// ---------------------------------------------------------------------------
// 认证池 (P5): CRUD + 治理守卫 + 只追加审计 + 双人确认.
//
// The operator console may only add manual/probe rows: source="seed" is
// reserved for SeedTrustPool, so an official row can never be minted through
// the API. P5-B changes the write path: a create/update/delete is recorded as
// a pending TrustPoolApproval instead of taking effect immediately. A second
// operator approves it, and only then is the stored after snapshot applied and
// the append-only audit row written. The submitter may never approve their own
// change (防自审); independent RBAC roles are deferred to P5-C, so the routes
// keep the existing RootAuth gate.
// ---------------------------------------------------------------------------

// trustPoolApprovalPayload is the JSON stored in TrustPoolApproval.Payload.
// The after snapshot is exactly the row the second operator reviews and the
// row applied on approval. Deletion stores only before.
type trustPoolApprovalPayload struct {
	Before *TrustPool `json:"before"`
	After  *TrustPool `json:"after"`
}

// adminTrustPoolApprovalView is the console shape of one approval row.
type adminTrustPoolApprovalView struct {
	ID          int64      `json:"id"`
	TrustPoolID int64      `json:"trust_pool_id"`
	Action      string     `json:"action"`
	Target      string     `json:"target"`
	Payload     any        `json:"payload"`
	SubmitterID int64      `json:"submitter_id"`
	ApproverID  int64      `json:"approver_id"`
	Status      string     `json:"status"`
	Notes       string     `json:"notes"`
	CreatedAt   time.Time  `json:"created_at"`
	ApprovedAt  *time.Time `json:"approved_at"`
}

// decodeTrustPoolApprovalPayload parses a stored payload. Callers treat an
// error as a corrupt row, not as a client error.
func decodeTrustPoolApprovalPayload(raw string) (trustPoolApprovalPayload, error) {
	var payload trustPoolApprovalPayload
	if strings.TrimSpace(raw) == "" {
		return payload, nil
	}
	if err := common.UnmarshalJsonStr(raw, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

// trustPoolApprovalTarget names the affected address for a list row.
func trustPoolApprovalTarget(payload trustPoolApprovalPayload) string {
	row := payload.After
	if row == nil {
		row = payload.Before
	}
	if row == nil {
		return ""
	}
	if row.Host != "" {
		return row.Host
	}
	return row.HostPattern
}

// toAdminTrustPoolApprovalView decodes the payload for display. A corrupt
// stored payload is returned verbatim so an operator can see what is stored.
func toAdminTrustPoolApprovalView(row TrustPoolApproval) adminTrustPoolApprovalView {
	payload, err := decodeTrustPoolApprovalPayload(row.Payload)
	view := adminTrustPoolApprovalView{
		ID:          row.ID,
		TrustPoolID: row.TrustPoolID,
		Action:      row.Action,
		Target:      trustPoolApprovalTarget(payload),
		SubmitterID: row.SubmitterID,
		ApproverID:  row.ApproverID,
		Status:      row.Status,
		Notes:       row.Notes,
		CreatedAt:   row.CreatedAt,
		ApprovedAt:  row.ApprovedAt,
	}
	if err != nil {
		view.Payload = row.Payload
	} else {
		view.Payload = payload
	}
	return view
}

// submitTrustPoolApproval inserts the pending change. The caller has already
// validated the after row with trustPoolFromAdminRequest / the governance
// guard, so a malformed change never reaches the approval queue.
func submitTrustPoolApproval(db *gorm.DB, submitterID, trustPoolID int64, action string,
	before, after *TrustPool) (*TrustPoolApproval, error) {
	raw, err := common.Marshal(trustPoolApprovalPayload{Before: before, After: after})
	if err != nil {
		return nil, err
	}
	notes := ""
	switch {
	case after != nil:
		notes = after.Notes
	case before != nil:
		notes = before.Notes
	}
	row := TrustPoolApproval{
		TrustPoolID: trustPoolID,
		Action:      action,
		Payload:     string(raw),
		SubmitterID: submitterID,
		Status:      TrustPoolApprovalPending,
		Notes:       notes,
		CreatedAt:   time.Now(),
	}
	if err := db.Create(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// adminTrustPoolCreate serves POST /admin/trust-pool — validate the row and
// queue a create for a second operator's approval.
func adminTrustPoolCreate(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	var req adminTrustPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	row, err := trustPoolFromAdminRequest(req, nil)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body: "+err.Error()))
		return
	}
	actorID := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	approval, err := submitTrustPoolApproval(db, actorID, 0, TrustPoolApprovalCreate, nil, &row)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to submit trust pool change"))
		return
	}
	common.ApiSuccess(c, toAdminTrustPoolApprovalView(*approval))
}

// adminTrustPoolUpdate serves PUT /admin/trust-pool/:id. The source of an
// existing row is immutable; seeded rows keep their official identity. The
// validated row is queued for a second operator instead of saved.
func adminTrustPoolUpdate(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	id, ok := adminIDParam(c)
	if !ok {
		return
	}
	var req adminTrustPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	var before TrustPool
	if err := db.Where("id = ?", id).Take(&before).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("trust pool row not found"))
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query trust pool row"))
		return
	}
	after, err := trustPoolFromAdminRequest(req, &before)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid request body: "+err.Error()))
		return
	}
	action := TrustPoolApprovalUpdate
	if before.Trust != after.Trust {
		action = TrustPoolApprovalChangeTrust
	}
	actorID := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	approval, err := submitTrustPoolApproval(db, actorID, id, action, &before, &after)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to submit trust pool change"))
		return
	}
	common.ApiSuccess(c, toAdminTrustPoolApprovalView(*approval))
}

// adminTrustPoolDelete serves DELETE /admin/trust-pool/:id — queue a hard
// delete for a second operator's approval. Seeded rows are code-owned and
// cannot be deleted; setting trust=blocked is the supported way to stop
// trusting one.
func adminTrustPoolDelete(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	id, ok := adminIDParam(c)
	if !ok {
		return
	}
	var before TrustPool
	if err := db.Where("id = ?", id).Take(&before).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("trust pool row not found"))
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query trust pool row"))
		return
	}
	if before.Source == TrustSourceSeed {
		common.ApiErrorStatus(c, http.StatusConflict, ErrTrustPoolSeedLocked)
		return
	}
	actorID := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	approval, err := submitTrustPoolApproval(db, actorID, id, TrustPoolApprovalDelete, &before, nil)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to submit trust pool change"))
		return
	}
	common.ApiSuccess(c, toAdminTrustPoolApprovalView(*approval))
}

// adminTrustPoolApprovalList serves GET /admin/trust-pool/approvals?status=
// (default pending). Rows are newest first.
func adminTrustPoolApprovalList(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	status := strings.TrimSpace(c.Query("status"))
	if status == "" {
		status = TrustPoolApprovalPending
	}
	switch status {
	case TrustPoolApprovalPending, TrustPoolApprovalApproved, TrustPoolApprovalRejected:
	default:
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("status must be pending, approved or rejected"))
		return
	}
	limit := 200
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("limit must be between 1 and 500"))
			return
		}
		limit = parsed
	}
	var rows []TrustPoolApproval
	if err := db.Where("status = ?", status).Order("created_at DESC").Order("id DESC").
		Limit(limit).Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query trust pool approvals"))
		return
	}
	items := make([]adminTrustPoolApprovalView, 0, len(rows))
	for i := range rows {
		items = append(items, toAdminTrustPoolApprovalView(rows[i]))
	}
	common.ApiSuccess(c, items)
}

// adminTrustPoolApprovalDecision loads a pending approval and runs the 双人确认
// guard before fn claims and applies it. The guard is checked both here and
// inside the claim transaction so a concurrent second approval cannot slip
// through.
func loadTrustPoolApprovalForReview(db *gorm.DB, c *gin.Context) (*TrustPoolApproval, int64, bool) {
	id, ok := adminIDParam(c)
	if !ok {
		return nil, 0, false
	}
	var approval TrustPoolApproval
	if err := db.Where("id = ?", id).Take(&approval).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("trust pool approval not found"))
			return nil, 0, false
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query trust pool approval"))
		return nil, 0, false
	}
	reviewerID := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	if err := ValidateTrustPoolApprovalDecision(approval, reviewerID); err != nil {
		if errors.Is(err, ErrTrustPoolApprovalSelfReview) {
			common.ApiErrorStatus(c, http.StatusForbidden, err)
			return nil, 0, false
		}
		common.ApiErrorStatus(c, http.StatusConflict, err)
		return nil, 0, false
	}
	return &approval, reviewerID, true
}

// adminTrustPoolApprovalApprove serves POST /admin/trust-pool/approvals/:id/approve.
// It claims the pending row, applies the reviewed snapshot and writes the
// audit row in one transaction.
func adminTrustPoolApprovalApprove(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	approval, reviewerID, ok := loadTrustPoolApprovalForReview(db, c)
	if !ok {
		return
	}
	payload, err := decodeTrustPoolApprovalPayload(approval.Payload)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("trust pool approval payload is corrupt"))
		return
	}
	now := time.Now()
	applied := TrustPoolApproval{}
	err = db.Transaction(func(tx *gorm.DB) error {
		// Claim the approval first: the conditional status update makes a
		// second concurrent reviewer fail instead of applying the change twice.
		res := tx.Model(&TrustPoolApproval{}).
			Where("id = ? AND status = ?", approval.ID, TrustPoolApprovalPending).
			Updates(map[string]any{
				"approver_id": reviewerID,
				"status":      TrustPoolApprovalApproved,
				"approved_at": now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrTrustPoolApprovalNotPending
		}
		var auditAction string
		switch approval.Action {
		case TrustPoolApprovalCreate:
			if payload.After == nil {
				return errors.New("create approval has no after snapshot")
			}
			row := *payload.After
			row.ID = 0
			row.CreatedAt = now
			row.UpdatedAt = now
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			applied.TrustPoolID = row.ID
			auditAction = TrustPoolAuditCreate
			if err := recordTrustPoolAudit(tx, reviewerID, auditAction, nil, &row); err != nil {
				return err
			}
		case TrustPoolApprovalUpdate, TrustPoolApprovalChangeTrust:
			if payload.After == nil {
				return errors.New("update approval has no after snapshot")
			}
			var current TrustPool
			if err := tx.Where("id = ?", approval.TrustPoolID).Take(&current).Error; err != nil {
				return err
			}
			row := *payload.After
			row.ID = approval.TrustPoolID
			row.CreatedAt = current.CreatedAt
			row.UpdatedAt = now
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			applied.TrustPoolID = row.ID
			if err := recordTrustPoolAudit(tx, reviewerID, TrustPoolAuditUpdate, &current, &row); err != nil {
				return err
			}
		case TrustPoolApprovalDelete:
			var current TrustPool
			if err := tx.Where("id = ?", approval.TrustPoolID).Take(&current).Error; err != nil {
				return err
			}
			if current.Source == TrustSourceSeed {
				return ErrTrustPoolSeedLocked
			}
			if err := tx.Delete(&TrustPool{}, approval.TrustPoolID).Error; err != nil {
				return err
			}
			applied.TrustPoolID = approval.TrustPoolID
			if err := recordTrustPoolAudit(tx, reviewerID, TrustPoolAuditDelete, &current, nil); err != nil {
				return err
			}
		default:
			return errors.New("unknown trust pool approval action")
		}
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrTrustPoolApprovalNotPending):
			common.ApiErrorStatus(c, http.StatusConflict, err)
		case errors.Is(err, ErrTrustPoolSeedLocked):
			common.ApiErrorStatus(c, http.StatusConflict, err)
		case errors.Is(err, gorm.ErrRecordNotFound):
			common.ApiErrorStatus(c, http.StatusConflict, errors.New("the trust pool row no longer exists"))
		default:
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to apply trust pool change"))
		}
		return
	}
	approval.Status = TrustPoolApprovalApproved
	approval.ApproverID = reviewerID
	approval.ApprovedAt = &now
	view := toAdminTrustPoolApprovalView(*approval)
	view.TrustPoolID = applied.TrustPoolID
	common.ApiSuccess(c, view)
}

// adminTrustPoolApprovalReject serves POST /admin/trust-pool/approvals/:id/reject.
// The change is not applied and the row stays unchanged; the approval row
// records who rejected it.
func adminTrustPoolApprovalReject(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	approval, reviewerID, ok := loadTrustPoolApprovalForReview(db, c)
	if !ok {
		return
	}
	now := time.Now()
	res := db.Model(&TrustPoolApproval{}).
		Where("id = ? AND status = ?", approval.ID, TrustPoolApprovalPending).
		Updates(map[string]any{
			"approver_id": reviewerID,
			"status":      TrustPoolApprovalRejected,
			"approved_at": now,
		})
	if res.Error != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to reject trust pool change"))
		return
	}
	if res.RowsAffected != 1 {
		common.ApiErrorStatus(c, http.StatusConflict, ErrTrustPoolApprovalNotPending)
		return
	}
	approval.Status = TrustPoolApprovalRejected
	approval.ApproverID = reviewerID
	approval.ApprovedAt = &now
	common.ApiSuccess(c, toAdminTrustPoolApprovalView(*approval))
}

// trustPoolEndpointValues is the closed set of endpoint kinds a trust-pool row
// may declare.
var trustPoolEndpointValues = []string{"openai_chat", "openai_responses", "anthropic_messages"}

// adminTrustPoolView is the console shape of one trust-pool row. JSON text
// columns are decoded into real values; malformed data is returned verbatim so
// an operator can see what is stored. No field carries key material.
type adminTrustPoolView struct {
	ID                      int64      `json:"id"`
	Host                    string     `json:"host"`
	HostPattern             string     `json:"host_pattern"`
	PathPrefix              string     `json:"path_prefix"`
	KeyPrefix               string     `json:"key_prefix"`
	VendorSlug              string     `json:"vendor_slug"`
	Kind                    string     `json:"kind"`
	Trust                   string     `json:"trust"`
	Endpoints               []string   `json:"endpoints"`
	AllowedModels           any        `json:"allowed_models"`
	OfficialPriceBookID     int64      `json:"official_price_book_id"`
	QuotaWindowTemplate     any        `json:"quota_window_template"`
	DenyKeyPrefixes         []string   `json:"deny_key_prefixes"`
	DenyPaths               []string   `json:"deny_paths"`
	ErrorCodeTable          any        `json:"error_code_table"`
	DefaultHeaders          any        `json:"default_headers"`
	AccountFingerprintRules any        `json:"account_fingerprint_rules"`
	Source                  string     `json:"source"`
	Notes                   string     `json:"notes"`
	ExpiresAt               *time.Time `json:"expires_at"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

// parseTrustPoolJSON decodes one stored JSON text column. Empty stays nil;
// malformed data is returned as its original text instead of being silently
// dropped.
func parseTrustPoolJSON(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var value any
	if err := common.Unmarshal([]byte(trimmed), &value); err != nil {
		return raw
	}
	return value
}

func toAdminTrustPoolView(row TrustPool) adminTrustPoolView {
	return adminTrustPoolView{
		ID:                      row.ID,
		Host:                    row.Host,
		HostPattern:             row.HostPattern,
		PathPrefix:              row.PathPrefix,
		KeyPrefix:               row.KeyPrefix,
		VendorSlug:              row.VendorSlug,
		Kind:                    row.Kind,
		Trust:                   row.Trust,
		Endpoints:               parseStringList(row.Endpoints),
		AllowedModels:           parseTrustPoolJSON(row.AllowedModels),
		OfficialPriceBookID:     row.OfficialPriceBookID,
		QuotaWindowTemplate:     parseTrustPoolJSON(row.QuotaWindowTemplate),
		DenyKeyPrefixes:         parseStringList(row.DenyKeyPrefixes),
		DenyPaths:               parseStringList(row.DenyPaths),
		ErrorCodeTable:          parseTrustPoolJSON(row.ErrorCodeTable),
		DefaultHeaders:          parseTrustPoolJSON(row.DefaultHeaders),
		AccountFingerprintRules: parseTrustPoolJSON(row.AccountFingerprintRules),
		Source:                  row.Source,
		Notes:                   row.Notes,
		ExpiresAt:               row.ExpiresAt,
		CreatedAt:               row.CreatedAt,
		UpdatedAt:               row.UpdatedAt,
	}
}

// adminTrustPoolRequest is the POST/PUT body. Optional JSON columns are
// flexJSON so an update that omits them preserves the stored value. expires_at
// is a pointer: nil preserves on update, "" clears, RFC3339 sets.
type adminTrustPoolRequest struct {
	Host                    string   `json:"host"`
	HostPattern             string   `json:"host_pattern"`
	PathPrefix              string   `json:"path_prefix"`
	KeyPrefix               string   `json:"key_prefix"`
	VendorSlug              string   `json:"vendor_slug"`
	Kind                    string   `json:"kind"`
	Trust                   string   `json:"trust"`
	Endpoints               flexJSON `json:"endpoints"`
	AllowedModels           flexJSON `json:"allowed_models"`
	OfficialPriceBookID     *int64   `json:"official_price_book_id"`
	QuotaWindowTemplate     flexJSON `json:"quota_window_template"`
	DenyKeyPrefixes         flexJSON `json:"deny_key_prefixes"`
	DenyPaths               flexJSON `json:"deny_paths"`
	ErrorCodeTable          flexJSON `json:"error_code_table"`
	DefaultHeaders          flexJSON `json:"default_headers"`
	AccountFingerprintRules flexJSON `json:"account_fingerprint_rules"`
	Source                  string   `json:"source"`
	Notes                   string   `json:"notes"`
	ExpiresAt               *string  `json:"expires_at"`
}

// flexTrustPoolStrings converts a request JSON array into a normalized list.
// nil means "not provided" (update keeps the stored value); an empty slice
// means "clear it".
func flexTrustPoolStrings(f flexJSON) ([]string, error) {
	if !f.Set {
		return nil, nil
	}
	list := make([]string, 0)
	if len(f.Raw) == 0 {
		return list, nil
	}
	var raw []string
	if err := common.Unmarshal(f.Raw, &raw); err != nil {
		return nil, errors.New("must be a JSON array of strings")
	}
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" || slices.Contains(list, item) {
			continue
		}
		list = append(list, item)
	}
	return list, nil
}

// trustPoolStringList serializes a list into its text column; an empty list is
// stored as an empty string, matching the schema default.
func trustPoolStringList(list []string) string {
	if len(list) == 0 {
		return ""
	}
	raw, err := common.Marshal(list)
	if err != nil {
		return ""
	}
	return string(raw)
}

// flexTrustPoolJSON validates any JSON value and returns its compact text.
func flexTrustPoolJSON(f flexJSON) (string, error) {
	trimmed := bytes.TrimSpace(f.Raw)
	if len(trimmed) == 0 {
		return "", nil
	}
	var value any
	if err := common.Unmarshal(trimmed, &value); err != nil {
		return "", errors.New("must be valid JSON")
	}
	raw, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// parseTrustPoolExpiresAt accepts an RFC3339 timestamp; empty clears it.
func parseTrustPoolExpiresAt(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, errors.New("expires_at must be RFC3339")
	}
	return &parsed, nil
}

// trustPoolFromAdminRequest validates one create/update request and returns
// the row to store. existing is nil for create. Fields the console does not
// edit are preserved on update.
func trustPoolFromAdminRequest(req adminTrustPoolRequest, existing *TrustPool) (TrustPool, error) {
	row := TrustPool{}
	if existing != nil {
		row = *existing
	}
	host, hostPattern, err := NormalizeTrustPoolAddress(req.Host, req.HostPattern)
	if err != nil {
		return row, err
	}
	row.Host, row.HostPattern = host, hostPattern
	row.PathPrefix = normalizeTrustPath(req.PathPrefix)
	if len(row.PathPrefix) > 512 {
		return row, errors.New("path_prefix is too long")
	}
	row.KeyPrefix = strings.TrimSpace(req.KeyPrefix)
	if len(row.KeyPrefix) > 128 {
		return row, errors.New("key_prefix is too long")
	}
	row.VendorSlug = strings.TrimSpace(req.VendorSlug)
	if len(row.VendorSlug) > 64 {
		return row, errors.New("vendor_slug is too long")
	}
	row.Kind = strings.TrimSpace(req.Kind)
	row.Trust = strings.TrimSpace(req.Trust)
	row.Notes = strings.TrimSpace(req.Notes)

	if req.Endpoints.Set {
		endpoints, err := flexTrustPoolStrings(req.Endpoints)
		if err != nil {
			return row, errors.New("endpoints must be a JSON array of strings")
		}
		for _, endpoint := range endpoints {
			if !slices.Contains(trustPoolEndpointValues, endpoint) {
				return row, errors.New("endpoints must be openai_chat, openai_responses or anthropic_messages")
			}
		}
		row.Endpoints = trustPoolStringList(endpoints)
	}
	if req.DenyKeyPrefixes.Set {
		list, err := flexTrustPoolStrings(req.DenyKeyPrefixes)
		if err != nil {
			return row, errors.New("deny_key_prefixes must be a JSON array of strings")
		}
		row.DenyKeyPrefixes = trustPoolStringList(list)
	}
	if req.DenyPaths.Set {
		list, err := flexTrustPoolStrings(req.DenyPaths)
		if err != nil {
			return row, errors.New("deny_paths must be a JSON array of strings")
		}
		normalized := make([]string, 0, len(list))
		for _, item := range list {
			item = normalizeTrustPath(item)
			if item == "" || slices.Contains(normalized, item) {
				continue
			}
			normalized = append(normalized, item)
		}
		row.DenyPaths = trustPoolStringList(normalized)
	}
	if req.OfficialPriceBookID != nil {
		if *req.OfficialPriceBookID < 0 {
			return row, errors.New("official_price_book_id must be >= 0")
		}
		row.OfficialPriceBookID = *req.OfficialPriceBookID
	}
	for _, field := range []struct {
		name string
		in   flexJSON
		out  *string
	}{
		{name: "allowed_models", in: req.AllowedModels, out: &row.AllowedModels},
		{name: "quota_window_template", in: req.QuotaWindowTemplate, out: &row.QuotaWindowTemplate},
		{name: "error_code_table", in: req.ErrorCodeTable, out: &row.ErrorCodeTable},
		{name: "default_headers", in: req.DefaultHeaders, out: &row.DefaultHeaders},
		{name: "account_fingerprint_rules", in: req.AccountFingerprintRules, out: &row.AccountFingerprintRules},
	} {
		if !field.in.Set {
			continue
		}
		value, err := flexTrustPoolJSON(field.in)
		if err != nil {
			return row, fmt.Errorf("%s must be valid JSON", field.name)
		}
		*field.out = value
	}
	if req.ExpiresAt != nil {
		expiresAt, err := parseTrustPoolExpiresAt(*req.ExpiresAt)
		if err != nil {
			return row, err
		}
		row.ExpiresAt = expiresAt
	}
	source := strings.TrimSpace(req.Source)
	if existing == nil {
		if source == "" {
			source = TrustSourceManual
		}
		switch source {
		case TrustSourceManual, TrustSourceProbe:
			row.Source = source
		case TrustSourceSeed:
			return row, ErrTrustPoolSourceReserved
		default:
			return row, errors.New("source must be manual or probe")
		}
	} else if source != "" && source != existing.Source {
		return row, ErrTrustPoolSourceImmutable
	}
	if row.Source == "" {
		row.Source = TrustSourceManual
	}
	if err := ValidateTrustPoolGovernance(row); err != nil {
		return row, err
	}
	return row, nil
}

// recordTrustPoolAudit appends one audit row. It runs in the caller's
// transaction so a committed change always has its trail.
func recordTrustPoolAudit(tx *gorm.DB, actorID int64, action string, before, after *TrustPool) error {
	snapshot := func(row *TrustPool) (string, error) {
		if row == nil {
			return "", nil
		}
		raw, err := common.Marshal(row)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	beforeJSON, err := snapshot(before)
	if err != nil {
		return err
	}
	afterJSON, err := snapshot(after)
	if err != nil {
		return err
	}
	notes := ""
	poolID := int64(0)
	switch {
	case after != nil:
		notes = after.Notes
		poolID = after.ID
	case before != nil:
		notes = before.Notes
		poolID = before.ID
	}
	return tx.Create(&TrustPoolAudit{
		TrustPoolID: poolID,
		ActorID:     actorID,
		Action:      action,
		Before:      beforeJSON,
		After:       afterJSON,
		Notes:       notes,
		CreatedAt:   time.Now(),
	}).Error
}

// adminTrustPoolPaging parses ?page=&page_size= (default 1 / 50, max 200).
func adminTrustPoolPaging(c *gin.Context) (int, int, bool) {
	page := 1
	pageSize := 50
	if raw := strings.TrimSpace(c.Query("page")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("page must be a positive integer"))
			return 0, 0, false
		}
		page = parsed
	}
	if raw := strings.TrimSpace(c.Query("page_size")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("page_size must be between 1 and 200"))
			return 0, 0, false
		}
		pageSize = parsed
	}
	return page, pageSize, true
}

// adminTrustPoolList serves GET /admin/trust-pool?trust=&kind=&source=&page=&page_size=.
// An unknown filter value is a 400: silently ignoring it would show the
// operator a set they did not ask for.
func adminTrustPoolList(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	trustFilter := strings.TrimSpace(c.Query("trust"))
	kindFilter := strings.TrimSpace(c.Query("kind"))
	sourceFilter := strings.TrimSpace(c.Query("source"))
	if trustFilter != "" && trustFilter != TrustTrusted && trustFilter != TrustWatch && trustFilter != TrustBlocked {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("trust must be trusted, watch or blocked"))
		return
	}
	if kindFilter != "" && kindFilter != TrustKindOfficial && kindFilter != TrustKindThirdParty && kindFilter != TrustKindPlan {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("kind must be official, third_party or plan"))
		return
	}
	if sourceFilter != "" && sourceFilter != TrustSourceSeed && sourceFilter != TrustSourceManual && sourceFilter != TrustSourceProbe {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("source must be seed, manual or probe"))
		return
	}
	page, pageSize, ok := adminTrustPoolPaging(c)
	if !ok {
		return
	}
	filtered := func() *gorm.DB {
		q := db.Model(&TrustPool{})
		if trustFilter != "" {
			q = q.Where("trust = ?", trustFilter)
		}
		if kindFilter != "" {
			q = q.Where("kind = ?", kindFilter)
		}
		if sourceFilter != "" {
			q = q.Where("source = ?", sourceFilter)
		}
		return q
	}
	var total int64
	if err := filtered().Count(&total).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query trust pool"))
		return
	}
	var rows []TrustPool
	if err := filtered().Order("id ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query trust pool"))
		return
	}
	items := make([]adminTrustPoolView, 0, len(rows))
	for i := range rows {
		items = append(items, toAdminTrustPoolView(rows[i]))
	}
	common.ApiSuccess(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// adminTrustPoolGet serves GET /admin/trust-pool/:id.
func adminTrustPoolGet(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	id, ok := adminIDParam(c)
	if !ok {
		return
	}
	var row TrustPool
	if err := db.Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("trust pool row not found"))
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query trust pool"))
		return
	}
	common.ApiSuccess(c, toAdminTrustPoolView(row))
}

// ---------------------------------------------------------------------------
// 派生指标 (P5): 读取 mp_ops_metrics.
//
// The hourly job writes one row per metric per hour bucket. This endpoint is a
// read-only operator view: metric_name and since narrow the query, dimensions
// filters the result by exact key/value pairs of the row's JSON object.
// ---------------------------------------------------------------------------

// adminOpsMetricView is the console shape of one metric row. Dimensions is
// decoded from its JSON text so the console does not double-decode.
type adminOpsMetricView struct {
	ID          int64     `json:"id"`
	MetricName  string    `json:"metric_name"`
	MetricValue float64   `json:"metric_value"`
	Dimensions  any       `json:"dimensions"`
	BucketTime  time.Time `json:"bucket_time"`
	CreatedAt   time.Time `json:"created_at"`
}

// adminOpsMetricDimensions decodes a row's dimensions; an empty or malformed
// value is returned verbatim instead of being silently dropped.
func adminOpsMetricDimensions(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}
	}
	var value any
	if err := common.UnmarshalJsonStr(trimmed, &value); err != nil {
		return raw
	}
	return value
}

// adminOpsMetricDimensionsMatch reports whether a row's dimensions contain
// every filter key with the same (stringified) value. A number filter therefore
// matches both JSON numbers and numeric strings.
func adminOpsMetricDimensionsMatch(raw string, filter map[string]any) bool {
	if len(filter) == 0 {
		return true
	}
	var dimensions map[string]any
	if err := common.UnmarshalJsonStr(strings.TrimSpace(raw), &dimensions); err != nil {
		return false
	}
	for key, want := range filter {
		got, ok := dimensions[key]
		if !ok || fmt.Sprint(got) != fmt.Sprint(want) {
			return false
		}
	}
	return true
}

// adminListOpsMetrics serves GET /admin/metrics?metric_name=&since=&dimensions=&limit=.
// since is RFC3339 (default: the last 24 hours). dimensions is a JSON object of
// exact key/value pairs. Rows come back newest bucket first.
func adminListOpsMetrics(c *gin.Context) {
	db := adminDB(c)
	if db == nil {
		return
	}
	if !adminRequireRoot(c) {
		return
	}
	since := time.Now().Add(-24 * time.Hour)
	if raw := strings.TrimSpace(c.Query("since")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("since must be RFC3339"))
			return
		}
		since = parsed
	}
	limit := 500
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 2000 {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("limit must be between 1 and 2000"))
			return
		}
		limit = parsed
	}
	var dimensionFilter map[string]any
	if raw := strings.TrimSpace(c.Query("dimensions")); raw != "" {
		if err := common.UnmarshalJsonStr(raw, &dimensionFilter); err != nil || len(dimensionFilter) == 0 {
			common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("dimensions must be a JSON object"))
			return
		}
	}
	query := db.Model(&OpsMetric{}).Where("bucket_time >= ?", since)
	if metricName := strings.TrimSpace(c.Query("metric_name")); metricName != "" {
		query = query.Where("metric_name = ?", metricName)
	}
	var rows []OpsMetric
	if err := query.Order("bucket_time DESC").Order("metric_name ASC").
		Limit(limit).Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query metrics"))
		return
	}
	items := make([]adminOpsMetricView, 0, len(rows))
	for i := range rows {
		if !adminOpsMetricDimensionsMatch(rows[i].Dimensions, dimensionFilter) {
			continue
		}
		items = append(items, adminOpsMetricView{
			ID:          rows[i].ID,
			MetricName:  rows[i].MetricName,
			MetricValue: rows[i].MetricValue,
			Dimensions:  adminOpsMetricDimensions(rows[i].Dimensions),
			BucketTime:  rows[i].BucketTime,
			CreatedAt:   rows[i].CreatedAt,
		})
	}
	common.ApiSuccess(c, items)
}
