package marketplace

// api_records.go — 记录 API (§3.8, §5 阶段 5): the buyer's 使用记录 and the
// seller's 分享记录, both read from mp_requests and paired one-to-one by
// request_id.
//
// Privacy contract (§3.1/§3.8):
//   - the buyer view carries the per-request cost breakdown the buyer is shown
//     in 费用明细 (official price, paid amount, platform fee, seller credit and
//     the seller's price ratio), but never the listing id or any key material;
//     the only seller-side identity is the 分享者编号 (SellerID, §3.1);
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
	"strings"
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
	g.GET("/records/usage/summary", usageSummary)
	g.GET("/records/usage/:request_id", getUsageRecord)
	g.GET("/records/shares", listShareRecords)
	g.GET("/records/shares/summary", shareSummary)
	g.GET("/records/shares/:request_id", getShareRecord)
}

// ---------------------------------------------------------------------------
// List-record response shapes
// ---------------------------------------------------------------------------

// usageRecord is one row of the buyer's 使用记录 (§3.8). Amounts are 微元.
// The telemetry and token fields are additive JSON additions — console clients
// without them keep working.
type usageRecord struct {
	RequestID        string `json:"request_id"`
	Model            string `json:"model"`
	SellerID         int64  `json:"seller_id"`    // 分享者编号 (§3.1)
	ChannelType      string `json:"channel_type"` // official / plan / third_party
	Tier             string `json:"tier"`
	TierLabel        string `json:"tier_label"` // 高峰 / 空闲 / 标准
	TokenGroup       string `json:"token_group"` // 令牌分组快照
	TokenName        string `json:"token_name"`  // 令牌名；令牌已删除时为空（前端显示"已删除"）
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	CacheHitTokens   int64  `json:"cache_hit_tokens"` // 缓存命中 (= cache_read_tokens)
	SearchCount      int64  `json:"search_count"`
	FetchCount       int64  `json:"fetch_count"`
	ToolDebitMicro   int64  `json:"tool_debit_micro"` // 搜索/抓取费用

	MultiplierMilli   int64 `json:"multiplier_milli"`    // 分享者设定的计价比例 (1000 = 100%)
	PlatformFeeMicro  int64 `json:"platform_fee_micro"`  // 平台服务费
	SellerCreditMicro int64 `json:"seller_credit_micro"` // 分享者到手
	OfficialCostMicro int64 `json:"official_cost_micro"`
	TotalDebitMicro   int64 `json:"total_debit_micro"`
	SavedMicro        int64 `json:"saved_micro"`     // 省了多少 = 官方价 − 实付
	SavedPermille     int64 `json:"saved_permille"`  // 省了千分之几 = saved / official × 1000; 0 when official = 0

	FirstTokenMs int64  `json:"first_token_ms"` // 首字耗时 ms; 0 = 未记录
	LatencyMs    int64  `json:"latency_ms"`     // 总耗时 ms
	IsStream     bool   `json:"is_stream"`      // 流式请求
	Endpoint     string `json:"endpoint"`       // 买方协议 (openai-chat / anthropic-messages / …)
	Converted    bool   `json:"converted"`      // 协议转换

	UsageEstimated bool      `json:"usage_estimated"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
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
	ChannelGroup      string    `json:"channel_group"`    // 渠道分组 (mp_listing_groups 的 vendor slug)
	ListingID         int64     `json:"listing_id"`       // 渠道 id
	KeyFingerprint4   string    `json:"key_fingerprint4"` // key 指纹后 4 位
	InputTokens       int64     `json:"input_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	CacheReadTokens   int64     `json:"cache_read_tokens"`
	CacheWriteTokens  int64     `json:"cache_write_tokens"`
	SearchCount       int64     `json:"search_count"`
	FetchCount        int64     `json:"fetch_count"`
	ToolDebitMicro    int64     `json:"tool_debit_micro"`
	SellerCreditMicro int64     `json:"seller_credit_micro"` // 卖家收入
	BuyerPaidMicro    int64     `json:"buyer_paid_micro"`    // 买家实付
	PlatformFeeMicro  int64     `json:"platform_fee_micro"`  // 平台费

	FirstTokenMs int64  `json:"first_token_ms"`
	LatencyMs    int64  `json:"latency_ms"`
	IsStream     bool   `json:"is_stream"`
	Endpoint     string `json:"endpoint"`
	Converted    bool   `json:"converted"`

	UsageEstimated bool      `json:"usage_estimated"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// shareRecordDetail adds the retry chain to the seller's detail view (§3.8).
type shareRecordDetail struct {
	shareRecord
	RetryChain string `json:"retry_chain"`
}

// ---------------------------------------------------------------------------
// Summary response shapes
// ---------------------------------------------------------------------------

// usageSummary is GET /records/usage/summary's payload for the buyer.
type usageSummary struct {
	RequestCount     int64 `json:"request_count"`
	SuccessCount     int64 `json:"success_count"`
	SuccessRateMilli int64 `json:"success_rate_milli"` // -1 = no sample
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	TotalDebitMicro  int64 `json:"total_debit_micro"`  // 实付合计
	SavedMicro       int64 `json:"saved_micro"`        // 比官方节省合计
	AvgFirstTokenMs  int64 `json:"avg_first_token_ms"` // 首字平均 ms; 0 = 无数据
	AvgLatencyMs     int64 `json:"avg_latency_ms"`     // 总耗时平均 ms
}

// shareSummary is GET /records/shares/summary's payload for the seller.
type shareSummary struct {
	RequestCount      int64 `json:"request_count"`
	SuccessCount      int64 `json:"success_count"`
	SuccessRateMilli  int64 `json:"success_rate_milli"` // -1 = no sample
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	CacheReadTokens   int64 `json:"cache_read_tokens"`
	CacheWriteTokens  int64 `json:"cache_write_tokens"`
	SellerCreditMicro int64 `json:"seller_credit_micro"` // 收益合计
	AvgFirstTokenMs   int64 `json:"avg_first_token_ms"`
	AvgLatencyMs      int64 `json:"avg_latency_ms"`
}

// ---------------------------------------------------------------------------
// Row context: per-row extras resolved in batched reads
// ---------------------------------------------------------------------------

// recordRowContext carries the per-row values the list handlers resolve in
// batched reads (channel kind, token name, listing group, key fingerprint
// tail). Each view reads the fields it shows; the others stay empty.
type recordRowContext struct {
	channelType    string
	tokenName      string
	channelGroup   string
	keyFingerprint4 string
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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

func newUsageRecord(r *Request, ctx recordRowContext) usageRecord {
	savedMicro := r.OfficialCostMicro - r.TotalDebitMicro
	var savedPermille int64
	if r.OfficialCostMicro > 0 {
		savedPermille = savedMicro * 1000 / r.OfficialCostMicro
	}
	return usageRecord{
		RequestID:         r.RequestID,
		Model:             r.Model,
		SellerID:          r.SellerID,
		ChannelType:       ctx.channelType,
		Tier:              r.Tier,
		TierLabel:         tierLabel(r.Tier),
		TokenGroup:        r.TokenGroup,
		TokenName:         ctx.tokenName,
		InputTokens:       r.InputTokens,
		OutputTokens:      r.OutputTokens,
		CacheReadTokens:   r.CacheReadTokens,
		CacheWriteTokens:  r.CacheWriteTokens,
		CacheHitTokens:    r.CacheReadTokens,
		SearchCount:       r.SearchCount,
		FetchCount:        r.FetchCount,
		ToolDebitMicro:    r.ToolDebitMicro,
		MultiplierMilli:   r.MultiplierMilli,
		PlatformFeeMicro:  r.PlatformFeeMicro,
		SellerCreditMicro: r.SellerCreditMicro,
		OfficialCostMicro: r.OfficialCostMicro,
		TotalDebitMicro:   r.TotalDebitMicro,
		SavedMicro:        savedMicro,
		SavedPermille:     savedPermille,
		FirstTokenMs:      r.FirstTokenMs,
		LatencyMs:         r.LatencyMs,
		IsStream:          r.IsStream,
		Endpoint:          r.Endpoint,
		Converted:         r.Converted,
		UsageEstimated:    r.UsageEstimated,
		Status:            r.Status,
		CreatedAt:         r.CreatedAt,
	}
}

func newShareRecord(r *Request, buyerCode string, ctx recordRowContext) shareRecord {
	return shareRecord{
		RequestID:         r.RequestID,
		Model:             r.Model,
		BuyerCode:         buyerCode,
		Tier:              r.Tier,
		TierLabel:         tierLabel(r.Tier),
		ChannelGroup:      ctx.channelGroup,
		ListingID:         r.ListingID,
		KeyFingerprint4:   ctx.keyFingerprint4,
		InputTokens:       r.InputTokens,
		OutputTokens:      r.OutputTokens,
		CacheReadTokens:   r.CacheReadTokens,
		CacheWriteTokens:  r.CacheWriteTokens,
		SearchCount:       r.SearchCount,
		FetchCount:        r.FetchCount,
		ToolDebitMicro:    r.ToolDebitMicro,
		SellerCreditMicro: r.SellerCreditMicro,
		BuyerPaidMicro:    r.TotalDebitMicro,
		PlatformFeeMicro:  r.PlatformFeeMicro,
		FirstTokenMs:      r.FirstTokenMs,
		LatencyMs:         r.LatencyMs,
		IsStream:          r.IsStream,
		Endpoint:          r.Endpoint,
		Converted:         r.Converted,
		UsageEstimated:    r.UsageEstimated,
		Status:            r.Status,
		CreatedAt:         r.CreatedAt,
	}
}

// tokenNames loads the name of each buyer token in ids. A token that no longer
// exists (deleted) is absent from the map; the buyer view renders "已删除".
func tokenNames(db *gorm.DB, tokenIDs []int64) map[int64]string {
	names := make(map[int64]string, len(tokenIDs))
	ids := make([]int64, 0, len(tokenIDs))
	seen := make(map[int64]struct{}, len(tokenIDs))
	for _, id := range tokenIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return names
	}
	var tokens []struct {
		ID   int64
		Name string
	}
	if err := db.Table("tokens").Select("id", "name").Where("id IN ?", ids).Find(&tokens).Error; err != nil {
		return names
	}
	for _, t := range tokens {
		names[t.ID] = t.Name
	}
	return names
}

// listingGroupNames resolves the channel-group (mp_listing_groups) of each
// listing in ids, keyed by listing id. An ungrouped or missing listing gets "".
func listingGroupNames(db *gorm.DB, listingIDs []int64) map[int64]string {
	groupOfListing := make(map[int64]int64, len(listingIDs))
	groupIDs := make([]int64, 0, len(listingIDs))
	seenGroup := make(map[int64]struct{}, len(listingIDs))
	for _, lid := range listingIDs {
		if lid <= 0 {
			continue
		}
		var l Listing
		if err := db.Select("id", "group_id").Where("id = ?", lid).Take(&l).Error; err != nil || l.GroupID <= 0 {
			continue
		}
		groupOfListing[lid] = l.GroupID
		if _, ok := seenGroup[l.GroupID]; !ok {
			seenGroup[l.GroupID] = struct{}{}
			groupIDs = append(groupIDs, l.GroupID)
		}
	}
	groupNames := make(map[int64]string, len(groupIDs))
	if len(groupIDs) > 0 {
		var groups []ListingGroup
		if err := db.Select("id", "vendor_slug").Where("id IN ?", groupIDs).Find(&groups).Error; err == nil {
			for i := range groups {
				groupNames[groups[i].ID] = groups[i].VendorSlug
			}
		}
	}
	result := make(map[int64]string, len(listingIDs))
	for lid, gid := range groupOfListing {
		result[lid] = groupNames[gid]
	}
	return result
}

// listingFingerprints4 loads the last 4 chars of each listing's key
// fingerprint, keyed by listing id. A missing listing gets "".
func listingFingerprints4(db *gorm.DB, listingIDs []int64) map[int64]string {
	result := make(map[int64]string, len(listingIDs))
	ids := make([]int64, 0, len(listingIDs))
	seen := make(map[int64]struct{}, len(listingIDs))
	for _, id := range listingIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return result
	}
	var listings []Listing
	if err := db.Select("id", "key_fingerprint").Where("id IN ?", ids).Find(&listings).Error; err != nil {
		return result
	}
	for i := range listings {
		fp := listings[i].KeyFingerprint
		if len(fp) >= 4 {
			result[listings[i].ID] = fp[len(fp)-4:]
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Filter building
// ---------------------------------------------------------------------------

// recordFilters holds the parsed query parameters for a list-records call.
// The owner filter (buyer_user_id or seller_id) is always applied.
type recordFilters struct {
	OwnerID    int64
	OwnerField string // "buyer_user_id" or "seller_id"

	Start   time.Time
	End     time.Time
	Model   string
	TokenID int64
	// Group is the buyer's token-group snapshot (token_group column).
	Group string
	// GroupID is the seller's channel-group id (mp_listings.group_id); a
	// seller filter matches the request's listing's group.
	GroupID int64
	Stream  *bool // nil = no filter
	Status  string
}

// parseRecordFilters reads ?start=&end=&model=&token_id=&group=&stream=&status=
// from c. start/end are unix seconds; a group value that parses as a positive
// integer is treated as a seller channel-group id, otherwise as a token-group
// name.
func parseRecordFilters(c *gin.Context, ownerField string, ownerID int64) recordFilters {
	f := recordFilters{OwnerID: ownerID, OwnerField: ownerField}
	if s := c.Query("start"); s != "" {
		if t, err := strconv.ParseInt(s, 10, 64); err == nil {
			f.Start = time.Unix(t, 0)
		}
	}
	if s := c.Query("end"); s != "" {
		if t, err := strconv.ParseInt(s, 10, 64); err == nil {
			f.End = time.Unix(t, 0)
		}
	}
	f.Model = strings.TrimSpace(c.Query("model"))
	if s := c.Query("token_id"); s != "" {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil && id > 0 {
			f.TokenID = id
		}
	}
	if s := c.Query("group"); s != "" {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil && id > 0 {
			f.GroupID = id
		} else {
			f.Group = strings.TrimSpace(s)
		}
	}
	if s := c.Query("stream"); s != "" {
		v := strings.ToLower(s)
		b := v == "true" || v == "1"
		if b || v == "false" || v == "0" {
			f.Stream = &b
		}
	}
	f.Status = strings.TrimSpace(c.Query("status"))
	return f
}

// apply adds every parsed filter as WHERE clauses to query, always including
// the owner filter.
func (f recordFilters) apply(query *gorm.DB) *gorm.DB {
	query = query.Where(f.OwnerField+" = ?", f.OwnerID)
	if !f.Start.IsZero() {
		query = query.Where("created_at >= ?", f.Start)
	}
	if !f.End.IsZero() {
		query = query.Where("created_at <= ?", f.End)
	}
	if f.Model != "" {
		query = query.Where("model = ?", f.Model)
	}
	if f.TokenID > 0 {
		query = query.Where("buyer_token_id = ?", f.TokenID)
	}
	if f.GroupID > 0 {
		// Seller view: the channel group lives on the listing, not the request.
		query = query.Where("listing_id IN (SELECT id FROM mp_listings WHERE group_id = ?)", f.GroupID)
	} else if f.Group != "" {
		query = query.Where("token_group = ?", f.Group)
	}
	if f.Stream != nil {
		query = query.Where("is_stream = ?", *f.Stream)
	}
	if f.Status != "" {
		query = query.Where("status = ?", f.Status)
	}
	return query
}

// ---------------------------------------------------------------------------
// Pagination / auth / db helpers
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Channel kind helpers
// ---------------------------------------------------------------------------

// usageChannelKinds resolves the supplier kind (official / plan / third_party)
// of each request's listing in two batched reads, keyed by listing id. A
// request whose listing is gone (or whose vendor is unknown) gets "" and the
// console renders the generic 分享渠道 label instead of guessing.
func usageChannelKinds(db *gorm.DB, rows []Request) map[int64]string {
	kinds := make(map[int64]string)
	ids := make([]int64, 0, len(rows))
	seen := make(map[int64]struct{}, len(rows))
	for i := range rows {
		id := rows[i].ListingID
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return kinds
	}
	var listings []Listing
	if err := db.Select("id", "vendor").Where("id IN ?", ids).Find(&listings).Error; err != nil {
		return kinds
	}
	vendorByListing := make(map[int64]string, len(listings))
	slugs := make([]string, 0, len(listings))
	seenSlug := make(map[string]struct{}, len(listings))
	for i := range listings {
		vendor := listings[i].Vendor
		if vendor == "" {
			continue
		}
		vendorByListing[listings[i].ID] = vendor
		if _, ok := seenSlug[vendor]; ok {
			continue
		}
		seenSlug[vendor] = struct{}{}
		slugs = append(slugs, vendor)
	}
	kindBySlug := make(map[string]string, len(slugs))
	if len(slugs) > 0 {
		var pools []ChannelPool
		if err := db.Select("slug", "kind").Where("slug IN ?", slugs).Find(&pools).Error; err == nil {
			for i := range pools {
				kindBySlug[pools[i].Slug] = pools[i].Kind
			}
		}
	}
	for listingID, vendor := range vendorByListing {
		kind := kindBySlug[vendor]
		if kind == "" {
			// Static vendors (MP_EXTRA_VENDORS and the built-in table) are not
			// rows in mp_channels; Vendor.Tier carries the same kind value.
			if v, ok := lookupVendorForDB(db, vendor); ok {
				kind = v.Tier
			}
		}
		kinds[listingID] = kind
	}
	return kinds
}

func usageChannelKind(db *gorm.DB, listingID int64) string {
	if listingID <= 0 {
		return ""
	}
	return usageChannelKinds(db, []Request{{ListingID: listingID}})[listingID]
}

// ---------------------------------------------------------------------------
// List and get
// ---------------------------------------------------------------------------

// listRecords runs one paginated mp_requests query (newest first, id as the
// tie-break so pages are stable) and answers with the upstream console
// PageInfo envelope. build receives the resolved per-row context (channel
// kind, token name, channel group, key fingerprint tail) so the views render
// without an extra query per row.
func listRecords(c *gin.Context, filters recordFilters, build func(*Request, recordRowContext) any) {
	page, pageSize := parseRecordPageQuery(c)
	db := recordDB(c)
	if db == nil {
		return
	}
	var total int64
	if err := filters.apply(db.Model(&Request{})).Count(&total).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query records"))
		return
	}
	var rows []Request
	if err := filters.apply(db).
		Order("created_at DESC").Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query records"))
		return
	}

	channelKinds := usageChannelKinds(db, rows)
	tokenIDs := make([]int64, 0, len(rows))
	listingIDs := make([]int64, 0, len(rows))
	for i := range rows {
		tokenIDs = append(tokenIDs, rows[i].BuyerTokenID)
		listingIDs = append(listingIDs, rows[i].ListingID)
	}
	tokenNamesByID := tokenNames(db, tokenIDs)
	groupsByListing := listingGroupNames(db, listingIDs)
	fpsByListing := listingFingerprints4(db, listingIDs)

	items := make([]any, 0, len(rows))
	for i := range rows {
		ctx := recordRowContext{
			channelType:     channelKinds[rows[i].ListingID],
			tokenName:       tokenNamesByID[rows[i].BuyerTokenID],
			channelGroup:    groupsByListing[rows[i].ListingID],
			keyFingerprint4: fpsByListing[rows[i].ListingID],
		}
		if item := build(&rows[i], ctx); item != nil {
			items = append(items, item)
		}
	}
	pageInfo := &common.PageInfo{Page: page, PageSize: pageSize}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// ---------------------------------------------------------------------------
// Buyer usage records
// ---------------------------------------------------------------------------

// listUsageRecords serves GET /records/usage — the buyer's own 使用记录.
func listUsageRecords(c *gin.Context) {
	userID := recordUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	filters := parseRecordFilters(c, "buyer_user_id", userID)
	listRecords(c, filters, func(r *Request, ctx recordRowContext) any {
		return newUsageRecord(r, ctx)
	})
}

// getUsageRecord serves GET /records/usage/:request_id — buyer only.
func getUsageRecord(c *gin.Context) {
	userID := recordUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	db := recordDB(c)
	if db == nil {
		return
	}
	var row Request
	err := db.Where("request_id = ? AND buyer_user_id = ?", c.Param("request_id"), userID).Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("record not found"))
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query record"))
		return
	}
	ctx := recordRowContext{channelType: usageChannelKind(db, row.ListingID)}
	if row.BuyerTokenID > 0 {
		ctx.tokenName = tokenNames(db, []int64{row.BuyerTokenID})[row.BuyerTokenID]
	}
	common.ApiSuccess(c, usageRecordDetail{
		usageRecord: newUsageRecord(&row, ctx),
		RetryChain:  row.RetryChain,
	})
}

// ---------------------------------------------------------------------------
// Seller share records
// ---------------------------------------------------------------------------

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
	filters := parseRecordFilters(c, "seller_id", userID)
	listRecords(c, filters, func(r *Request, ctx recordRowContext) any {
		code, err := AnonBuyerCode(r.SellerID, r.BuyerUserID)
		if err != nil {
			return nil
		}
		return newShareRecord(r, code, ctx)
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
	db := recordDB(c)
	if db == nil {
		return
	}
	var row Request
	err := db.Where("request_id = ? AND seller_id = ?", c.Param("request_id"), userID).Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("record not found"))
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query record"))
		return
	}
	code, err := AnonBuyerCode(row.SellerID, row.BuyerUserID)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to generate buyer code"))
		return
	}
	ctx := recordRowContext{
		channelType:     usageChannelKind(db, row.ListingID),
		channelGroup:    listingGroupNames(db, []int64{row.ListingID})[row.ListingID],
		keyFingerprint4: listingFingerprints4(db, []int64{row.ListingID})[row.ListingID],
	}
	common.ApiSuccess(c, shareRecordDetail{
		shareRecord: newShareRecord(&row, code, ctx),
		RetryChain:  sellerRetryChain(row.RetryChain),
	})
}

// ---------------------------------------------------------------------------
// Summary endpoints
// ---------------------------------------------------------------------------

// usageSummary serves GET /records/usage/summary — buyer aggregate stats under
// the same filters as the list.
func usageSummary(c *gin.Context) {
	userID := recordUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	db := recordDB(c)
	if db == nil {
		return
	}
	filters := parseRecordFilters(c, "buyer_user_id", userID)

	var a struct {
		RequestCount     int64
		SuccessCount     int64
		InputTokens      int64
		OutputTokens     int64
		CacheReadTokens  int64
		CacheWriteTokens int64
		TotalDebitMicro  int64
		OfficialCostMicro int64
		SumFirstTokenMs  int64
		HasFirstToken    int64
		SumLatencyMs     int64
		HasLatency       int64
	}
	err := filters.apply(db.Model(&Request{}).Select(`
		COUNT(*) AS request_count,
		SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS success_count,
		SUM(input_tokens) AS input_tokens,
		SUM(output_tokens) AS output_tokens,
		SUM(cache_read_tokens) AS cache_read_tokens,
		SUM(cache_write_tokens) AS cache_write_tokens,
		SUM(total_debit_micro) AS total_debit_micro,
		SUM(official_cost_micro) AS official_cost_micro,
		SUM(first_token_ms) AS sum_first_token_ms,
		SUM(CASE WHEN first_token_ms > 0 THEN 1 ELSE 0 END) AS has_first_token,
		SUM(latency_ms) AS sum_latency_ms,
		SUM(CASE WHEN latency_ms > 0 THEN 1 ELSE 0 END) AS has_latency`)).
		Scan(&a).Error
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to compute summary"))
		return
	}
	var rate int64 = -1
	if a.RequestCount > 0 {
		rate = a.SuccessCount * 1000 / a.RequestCount
	}
	avgFirst, avgLatency := int64(0), int64(0)
	if a.HasFirstToken > 0 {
		avgFirst = a.SumFirstTokenMs / a.HasFirstToken
	}
	if a.HasLatency > 0 {
		avgLatency = a.SumLatencyMs / a.HasLatency
	}
	common.ApiSuccess(c, usageSummary{
		RequestCount:     a.RequestCount,
		SuccessCount:     a.SuccessCount,
		SuccessRateMilli: rate,
		InputTokens:      a.InputTokens,
		OutputTokens:     a.OutputTokens,
		CacheReadTokens:  a.CacheReadTokens,
		CacheWriteTokens: a.CacheWriteTokens,
		TotalDebitMicro:  a.TotalDebitMicro,
		SavedMicro:       a.OfficialCostMicro - a.TotalDebitMicro,
		AvgFirstTokenMs:  avgFirst,
		AvgLatencyMs:     avgLatency,
	})
}

// shareSummary serves GET /records/shares/summary — seller aggregate stats
// under the same filters as the list.
func shareSummary(c *gin.Context) {
	userID := recordUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	if !shareCodesReady() {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
		return
	}
	db := recordDB(c)
	if db == nil {
		return
	}
	filters := parseRecordFilters(c, "seller_id", userID)

	var a struct {
		RequestCount      int64
		SuccessCount      int64
		InputTokens       int64
		OutputTokens      int64
		CacheReadTokens   int64
		CacheWriteTokens  int64
		SellerCreditMicro int64
		SumFirstTokenMs   int64
		HasFirstToken     int64
		SumLatencyMs      int64
		HasLatency        int64
	}
	err := filters.apply(db.Model(&Request{}).Select(`
		COUNT(*) AS request_count,
		SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS success_count,
		SUM(input_tokens) AS input_tokens,
		SUM(output_tokens) AS output_tokens,
		SUM(cache_read_tokens) AS cache_read_tokens,
		SUM(cache_write_tokens) AS cache_write_tokens,
		SUM(seller_credit_micro) AS seller_credit_micro,
		SUM(first_token_ms) AS sum_first_token_ms,
		SUM(CASE WHEN first_token_ms > 0 THEN 1 ELSE 0 END) AS has_first_token,
		SUM(latency_ms) AS sum_latency_ms,
		SUM(CASE WHEN latency_ms > 0 THEN 1 ELSE 0 END) AS has_latency`)).
		Scan(&a).Error
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to compute summary"))
		return
	}
	var rate int64 = -1
	if a.RequestCount > 0 {
		rate = a.SuccessCount * 1000 / a.RequestCount
	}
	avgFirst, avgLatency := int64(0), int64(0)
	if a.HasFirstToken > 0 {
		avgFirst = a.SumFirstTokenMs / a.HasFirstToken
	}
	if a.HasLatency > 0 {
		avgLatency = a.SumLatencyMs / a.HasLatency
	}
	common.ApiSuccess(c, shareSummary{
		RequestCount:      a.RequestCount,
		SuccessCount:      a.SuccessCount,
		SuccessRateMilli:  rate,
		InputTokens:       a.InputTokens,
		OutputTokens:      a.OutputTokens,
		CacheReadTokens:   a.CacheReadTokens,
		CacheWriteTokens:  a.CacheWriteTokens,
		SellerCreditMicro: a.SellerCreditMicro,
		AvgFirstTokenMs:   avgFirst,
		AvgLatencyMs:      avgLatency,
	})
}
