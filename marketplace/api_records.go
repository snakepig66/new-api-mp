package marketplace

// api_records.go — 记录 API (§3.8, §5 阶段 5): the buyer's 使用记录 and the
// seller's 分享记录, both read from mp_requests and paired one-to-one by
// request_id.
//
// Privacy contract (§3.1/§3.8):
//   - the buyer view never carries seller-side money fields (平台费/分享者收入),
//     the listing id, the multiplier or any key material; the only seller-side
//     datum is the 分享者编号 (SellerID as a plain number, §3.1);
//   - the seller view never carries the buyer's real user/token id — the buyer
//     appears only as the per-seller 8-hex-digit anonymous code
//     AnonBuyerCode(sellerID, buyerUserID) — and never any key material;
//   - detail endpoints answer 404 for anyone but the owner, so the existence
//     of a request id is not leaked.
//
// These are web-console APIs: they answer in the upstream console envelope
// (common.ApiSuccess / common.ApiErrorStatus), not the §3.10 OpenAI error
// format, which is reserved for the buyer-facing relay path.

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

// recordPageSizeDefault / recordPageSizeMax are the §5 阶段 5 pagination
// rules: page starts at 1, page_size defaults to 20 and is capped at 100.
const (
	recordPageSizeDefault = 20
	recordPageSizeMax     = 100
)

// RegisterRecordRoutes mounts the record endpoints on g. g MUST already carry
// middleware.UserAuth() (wired by the router seam; this package cannot import
// middleware without an import cycle, §4.4).
func RegisterRecordRoutes(g *gin.RouterGroup) {
	registerRecordRoutes(g)
}

func registerRecordRoutes(g *gin.RouterGroup) {
	g.GET("/records/usage", listUsageRecords)
	g.GET("/records/usage/:request_id", getUsageRecord)
	g.GET("/records/shares", listShareRecords)
	g.GET("/records/shares/:request_id", getShareRecord)
}

// usageRecord is one row of the buyer's 使用记录 (§3.8). Amounts are 微元.
type usageRecord struct {
	RequestID         string    `json:"request_id"`
	Model             string    `json:"model"`
	SellerID          int64     `json:"seller_id"` // 分享者编号 (§3.1)
	Tier              string    `json:"tier"`
	TierLabel         string    `json:"tier_label"` // 高峰 / 空闲 / 标准
	InputTokens       int64     `json:"input_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	CacheReadTokens   int64     `json:"cache_read_tokens"`
	CacheWriteTokens  int64     `json:"cache_write_tokens"`
	CacheHitTokens    int64     `json:"cache_hit_tokens"` // 缓存命中 (= cache_read_tokens)
	SearchCount       int64     `json:"search_count"`
	FetchCount        int64     `json:"fetch_count"`
	ToolDebitMicro    int64     `json:"tool_debit_micro"` // 搜索/抓取费用
	OfficialCostMicro int64     `json:"official_cost_micro"`
	TotalDebitMicro   int64     `json:"total_debit_micro"`
	SavedMicro        int64     `json:"saved_micro"` // 省了多少 = 官方价 − 实付
	UsageEstimated    bool      `json:"usage_estimated"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
}

// usageRecordDetail adds the retry chain to the buyer's detail view (§3.8:
// 重试链原样返回, JSON 字符串).
type usageRecordDetail struct {
	usageRecord
	RetryChain string `json:"retry_chain"`
}

// shareRecord is one row of the seller's 分享记录 (§3.8). Amounts are 微元.
type shareRecord struct {
	RequestID         string    `json:"request_id"`
	Model             string    `json:"model"`
	BuyerCode         string    `json:"buyer_code"` // 匿名代号 (§3.1), 8 hex digits
	Tier              string    `json:"tier"`
	TierLabel         string    `json:"tier_label"`
	InputTokens       int64     `json:"input_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	CacheReadTokens   int64     `json:"cache_read_tokens"`
	CacheWriteTokens  int64     `json:"cache_write_tokens"`
	SearchCount       int64     `json:"search_count"`
	FetchCount        int64     `json:"fetch_count"`
	ToolDebitMicro    int64     `json:"tool_debit_micro"`
	SellerCreditMicro int64     `json:"seller_credit_micro"` // 收入
	PlatformFeeMicro  int64     `json:"platform_fee_micro"`  // 平台费
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
}

// shareRecordDetail adds the retry chain to the seller's detail view (§3.8).
type shareRecordDetail struct {
	shareRecord
	RetryChain string `json:"retry_chain"`
}

// tierLabel renders the §3.8 计价时段 in buyer/seller-facing wording.
func tierLabel(tier string) string {
	switch tier {
	case TierPeak:
		return "高峰"
	case TierOffpeak:
		return "空闲"
	case TierFlat:
		return "标准"
	default:
		return tier
	}
}

func newUsageRecord(r *Request) usageRecord {
	return usageRecord{
		RequestID:         r.RequestID,
		Model:             r.Model,
		SellerID:          r.SellerID,
		Tier:              r.Tier,
		TierLabel:         tierLabel(r.Tier),
		InputTokens:       r.InputTokens,
		OutputTokens:      r.OutputTokens,
		CacheReadTokens:   r.CacheReadTokens,
		CacheWriteTokens:  r.CacheWriteTokens,
		CacheHitTokens:    r.CacheReadTokens,
		SearchCount:       r.SearchCount,
		FetchCount:        r.FetchCount,
		ToolDebitMicro:    r.ToolDebitMicro,
		OfficialCostMicro: r.OfficialCostMicro,
		TotalDebitMicro:   r.TotalDebitMicro,
		SavedMicro:        r.OfficialCostMicro - r.TotalDebitMicro,
		UsageEstimated:    r.UsageEstimated,
		Status:            r.Status,
		CreatedAt:         r.CreatedAt,
	}
}

func newShareRecord(r *Request, buyerCode string) shareRecord {
	return shareRecord{
		RequestID:         r.RequestID,
		Model:             r.Model,
		BuyerCode:         buyerCode,
		Tier:              r.Tier,
		TierLabel:         tierLabel(r.Tier),
		InputTokens:       r.InputTokens,
		OutputTokens:      r.OutputTokens,
		CacheReadTokens:   r.CacheReadTokens,
		CacheWriteTokens:  r.CacheWriteTokens,
		SearchCount:       r.SearchCount,
		FetchCount:        r.FetchCount,
		ToolDebitMicro:    r.ToolDebitMicro,
		SellerCreditMicro: r.SellerCreditMicro,
		PlatformFeeMicro:  r.PlatformFeeMicro,
		Status:            r.Status,
		CreatedAt:         r.CreatedAt,
	}
}

// parseRecordPageQuery reads the §5 阶段 5 pagination parameters: page starts
// at 1, page_size defaults to 20 and is capped at 100.
func parseRecordPageQuery(c *gin.Context) (page, pageSize int) {
	page, err := strconv.Atoi(c.Query("page"))
	if err != nil || page < 1 {
		page = 1
	}
	pageSize, err = strconv.Atoi(c.Query("page_size"))
	if err != nil || pageSize <= 0 {
		pageSize = recordPageSizeDefault
	}
	if pageSize > recordPageSizeMax {
		pageSize = recordPageSizeMax
	}
	return page, pageSize
}

// recordUserID returns the logged-in user's id (UserAuth has already run on
// the group, §4.4); <= 0 is answered with 401 by the caller, defensively.
func recordUserID(c *gin.Context) int64 {
	return int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
}

// recordDB returns the marketplace database or answers 500. The marketplace
// handle is the one Init received from model.InitDB (relayhook.go).
func recordDB(c *gin.Context) *gorm.DB {
	db, _ := relayState()
	if db == nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
		return nil
	}
	return db
}

// listRecords runs one paginated mp_requests query (newest first, id as the
// tie-break so pages are stable) and answers with the upstream console
// PageInfo envelope.
func listRecords(c *gin.Context, filter string, arg int64, build func(*Request) any) {
	page, pageSize := parseRecordPageQuery(c)
	db := recordDB(c)
	if db == nil {
		return
	}
	var total int64
	if err := db.Model(&Request{}).Where(filter, arg).Count(&total).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query records"))
		return
	}
	var rows []Request
	if err := db.Where(filter, arg).
		Order("created_at DESC").Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query records"))
		return
	}
	items := make([]any, 0, len(rows))
	for i := range rows {
		if item := build(&rows[i]); item != nil {
			items = append(items, item)
		}
	}
	pageInfo := &common.PageInfo{Page: page, PageSize: pageSize}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// getRecord loads one mp_requests row scoped to the owner; any other row —
// including a nonexistent id — answers 404 so existence is not leaked (§3.1).
func getRecord(c *gin.Context, filter string, arg int64, build func(*Request) any) {
	db := recordDB(c)
	if db == nil {
		return
	}
	var row Request
	err := db.Where("request_id = ? AND "+filter, c.Param("request_id"), arg).Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("record not found"))
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query record"))
		return
	}
	item := build(&row)
	if item == nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to render record"))
		return
	}
	common.ApiSuccess(c, item)
}

// listUsageRecords serves GET /records/usage — the buyer's own 使用记录.
func listUsageRecords(c *gin.Context) {
	userID := recordUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	listRecords(c, "buyer_user_id = ?", userID, func(r *Request) any {
		rec := newUsageRecord(r)
		return rec
	})
}

// getUsageRecord serves GET /records/usage/:request_id — buyer only.
func getUsageRecord(c *gin.Context) {
	userID := recordUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	getRecord(c, "buyer_user_id = ?", userID, func(r *Request) any {
		return usageRecordDetail{usageRecord: newUsageRecord(r), RetryChain: r.RetryChain}
	})
}

// shareCodesReady reports whether anonymous buyer codes can be derived at all
// (Init refuses to start without MP_KEY_SECRET, so a false here is a
// misconfiguration, answered 500 — never degraded into leaking the real id).
func shareCodesReady() bool {
	return len(anonSecret) > 0
}

// listShareRecords serves GET /records/shares — the seller's own 分享记录,
// with the buyer anonymized per §3.1.
func listShareRecords(c *gin.Context) {
	userID := recordUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	if !shareCodesReady() {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
		return
	}
	listRecords(c, "seller_id = ?", userID, func(r *Request) any {
		code, err := AnonBuyerCode(r.SellerID, r.BuyerUserID)
		if err != nil {
			return nil
		}
		rec := newShareRecord(r, code)
		return rec
	})
}

// getShareRecord serves GET /records/shares/:request_id — seller only.
func getShareRecord(c *gin.Context) {
	userID := recordUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	if !shareCodesReady() {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
		return
	}
	getRecord(c, "seller_id = ?", userID, func(r *Request) any {
		code, err := AnonBuyerCode(r.SellerID, r.BuyerUserID)
		if err != nil {
			return nil
		}
		return shareRecordDetail{shareRecord: newShareRecord(r, code), RetryChain: r.RetryChain}
	})
}
