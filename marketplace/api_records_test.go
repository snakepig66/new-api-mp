package marketplace

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// ---------------------------------------------------------------------------
// shared test plumbing for the console API tests (records + square)
// ---------------------------------------------------------------------------

// recordFixture is an isolated marketplace environment with a gin engine
// serving the record routes behind a simulated login (§5 阶段 5 接口测试 +
// 权限测试).
type recordFixture struct {
	db     *gorm.DB
	engine *gin.Engine
}

func newRecordFixture(t *testing.T) *recordFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	migrateAll(t, db)

	oldSecret := anonSecret
	SetAnonSecret("records-test-secret")
	relayStateMu.Lock()
	oldRelayDB := relayDB
	relayDB = db
	relayStateMu.Unlock()
	t.Cleanup(func() {
		anonSecret = oldSecret
		relayStateMu.Lock()
		relayDB = oldRelayDB
		relayStateMu.Unlock()
	})

	engine := gin.New()
	g := engine.Group("/api/marketplace")
	// 模拟登录: registerRecordRoutes assumes the group already carries
	// middleware.UserAuth(), which sets the "id" context key from the session
	// (middleware.setDashboardAuthContext). Tests pass the user id through the
	// X-Test-User-Id header instead.
	g.Use(func(c *gin.Context) {
		id, _ := strconv.Atoi(c.GetHeader("X-Test-User-Id"))
		c.Set("id", id)
		c.Next()
	})
	registerRecordRoutes(g)
	return &recordFixture{db: db, engine: engine}
}

func (f *recordFixture) get(t *testing.T, path string, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Test-User-Id", strconv.FormatInt(userID, 10))
	f.engine.ServeHTTP(w, req)
	return w
}

// seedRequest writes one mp_requests row directly — the API tests fixture the
// records themselves, not the relay path that produces them.
func seedRequest(t *testing.T, db *gorm.DB, rec Request) {
	t.Helper()
	if rec.Status == "" {
		rec.Status = RequestSuccess
	}
	require.NoError(t, db.Create(&rec).Error)
}

// decodeAPIBody parses the upstream console envelope
// {"success":...,"message":...,"data":...} (common.ApiSuccess/ApiErrorStatus).
func decodeAPIBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
	return body
}

// apiData unwraps the envelope's data object.
func apiData(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	require.True(t, ok, "data must be an object, body: %v", body)
	return data
}

// apiNum reads a numeric JSON field (decoded as float64) as int64.
func apiNum(t *testing.T, m map[string]any, key string) int64 {
	t.Helper()
	v, ok := m[key].(float64)
	require.True(t, ok, "field %q must be a number, map: %v", key, m)
	return int64(v)
}

// apiPageItems unwraps a PageInfo data object's items array.
func apiPageItems(t *testing.T, data map[string]any) []any {
	t.Helper()
	items, ok := data["items"].([]any)
	require.True(t, ok, "items must be an array, data: %v", data)
	return items
}

// findByRequestID picks the item with the given request_id out of an items
// array; it fails the test when absent.
func findByRequestID(t *testing.T, items []any, requestID string) map[string]any {
	t.Helper()
	for _, it := range items {
		m, ok := it.(map[string]any)
		require.True(t, ok)
		if m["request_id"] == requestID {
			return m
		}
	}
	require.FailNow(t, "request_id not found in items", "request_id=%s items=%v", requestID, items)
	return nil
}

var recordTestBase = time.Date(2026, time.October, 21, 10, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------------------
// 1. buyer usage list: only own rows, full fields, 省了多少 = 官方价 − 实付
// ---------------------------------------------------------------------------

func TestApiRecordUsageList(t *testing.T) {
	f := newRecordFixture(t)
	const buyerA, buyerB, seller = int64(7001), int64(7002), int64(8001)

	// A live listing on the seeded official deepseek channel gives the buyer
	// view a 渠道类型 without exposing the listing id itself.
	require.NoError(t, f.db.Create(&Listing{
		ID: 901, SellerID: seller, ChannelID: 1, Vendor: "deepseek",
		Models: []string{"deepseek-flash"}, MultiplierMilli: 300,
		Status: ListingLive, KeyFingerprint: "fp-records-901",
	}).Error)

	seedRequest(t, f.db, Request{
		RequestID: "req-a1", BuyerUserID: buyerA, BuyerTokenID: 501, SellerID: seller, ListingID: 901,
		Model: "deepseek-flash", Tier: TierPeak, PriceVersion: 1, MultiplierMilli: 300,
		InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 200, CacheWriteTokens: 100,
		SearchCount: 1, FetchCount: 2, TokenDebitMicro: 5000, ToolDebitMicro: 15000,
		TotalDebitMicro: 20000, PlatformFeeMicro: 1400, SellerCreditMicro: 18600,
		OfficialCostMicro: 66000, RetryChain: `[{"seq":1,"event":"select"}]`,
		CreatedAt: recordTestBase,
	})
	seedRequest(t, f.db, Request{
		RequestID: "req-a2", BuyerUserID: buyerA, BuyerTokenID: 501, SellerID: seller, ListingID: 901,
		Model: "deepseek-flash", Tier: TierOffpeak, PriceVersion: 1, MultiplierMilli: 300,
		InputTokens: 2000, OutputTokens: 800, TotalDebitMicro: 9000, OfficialCostMicro: 30000,
		UsageEstimated: true, RetryChain: `[]`,
		CreatedAt: recordTestBase.Add(time.Minute),
	})
	seedRequest(t, f.db, Request{
		RequestID: "req-b1", BuyerUserID: buyerB, BuyerTokenID: 502, SellerID: seller, ListingID: 901,
		Model: "deepseek-flash", Tier: TierPeak, PriceVersion: 1, MultiplierMilli: 300,
		TotalDebitMicro: 1000, OfficialCostMicro: 3000, RetryChain: `[]`,
		CreatedAt: recordTestBase.Add(2 * time.Minute),
	})

	w := f.get(t, "/api/marketplace/records/usage", buyerA)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	body := decodeAPIBody(t, w)
	require.Equal(t, true, body["success"])
	data := apiData(t, body)
	assert.Equal(t, int64(2), apiNum(t, data, "total"))
	assert.Equal(t, int64(1), apiNum(t, data, "page"))
	assert.Equal(t, int64(20), apiNum(t, data, "page_size"), "page_size default is 20 (§5 阶段 5)")
	items := apiPageItems(t, data)
	require.Len(t, items, 2)

	// Newest first.
	first := items[0].(map[string]any)
	assert.Equal(t, "req-a2", first["request_id"])

	rec := findByRequestID(t, items, "req-a1")
	assert.Equal(t, "deepseek-flash", rec["model"])
	assert.Equal(t, int64(seller), apiNum(t, rec, "seller_id"), "§3.1: 买家能看到分享者编号")
	assert.Equal(t, "official", rec["channel_type"], "官方渠道性质来自 listing 的 vendor")
	assert.Equal(t, "peak", rec["tier"])
	assert.Equal(t, "高峰", rec["tier_label"], "时段文案化")
	assert.Equal(t, int64(1000), apiNum(t, rec, "input_tokens"))
	assert.Equal(t, int64(500), apiNum(t, rec, "output_tokens"))
	assert.Equal(t, int64(200), apiNum(t, rec, "cache_read_tokens"))
	assert.Equal(t, int64(100), apiNum(t, rec, "cache_write_tokens"))
	assert.Equal(t, int64(200), apiNum(t, rec, "cache_hit_tokens"), "缓存命中 = cache read tokens")
	assert.Equal(t, int64(1), apiNum(t, rec, "search_count"))
	assert.Equal(t, int64(2), apiNum(t, rec, "fetch_count"))
	assert.Equal(t, int64(15000), apiNum(t, rec, "tool_debit_micro"), "搜索/抓取费用")
	assert.Equal(t, int64(66000), apiNum(t, rec, "official_cost_micro"), "官方价")
	assert.Equal(t, int64(20000), apiNum(t, rec, "total_debit_micro"), "实付")
	assert.Equal(t, int64(46000), apiNum(t, rec, "saved_micro"), "省了多少 = OfficialCost − TotalDebit")
	assert.Equal(t, int64(300), apiNum(t, rec, "multiplier_milli"), "费用明细的计价比例")
	assert.Equal(t, int64(1400), apiNum(t, rec, "platform_fee_micro"), "平台服务费")
	assert.Equal(t, int64(18600), apiNum(t, rec, "seller_credit_micro"), "分享者到手")
	assert.Equal(t, "success", rec["status"])
	assert.Equal(t, false, rec["usage_estimated"])
	assert.NotContains(t, rec, "retry_chain", "the list view carries no retry chain (detail only)")

	// The buyer view must not carry internal routing/key fields. The
	// cost-breakdown fields themselves are intentionally part of 费用明细.
	raw := w.Body.String()
	for _, absent := range []string{
		"listing_id", "price_version", "buyer_token_id",
		"key_fingerprint", "key_ciphertext",
	} {
		assert.NotContains(t, raw, absent, "buyer view leaks %s", absent)
	}

	// The other buyer sees only their own row.
	w2 := f.get(t, "/api/marketplace/records/usage", buyerB)
	require.Equal(t, http.StatusOK, w2.Code)
	data2 := apiData(t, decodeAPIBody(t, w2))
	assert.Equal(t, int64(1), apiNum(t, data2, "total"))
	items2 := apiPageItems(t, data2)
	require.Len(t, items2, 1)
	assert.Equal(t, "req-b1", items2[0].(map[string]any)["request_id"])
	assert.NotContains(t, w2.Body.String(), "req-a1", "another buyer's record must not leak")
}

// ---------------------------------------------------------------------------
// 2. seller shares list: only own supply, buyer shown as anonymous code,
//    response carries neither the buyer's real id nor any key material
// ---------------------------------------------------------------------------

func TestApiRecordSharesAnonymity(t *testing.T) {
	f := newRecordFixture(t)
	// Distinctive buyer id: the full-response absence check below relies on it
	// not colliding with any amount in the fixtures.
	const buyer = int64(918273645)
	const sellerX, sellerY = int64(8001), int64(8002)

	seedRequest(t, f.db, Request{
		RequestID: "req-s1", BuyerUserID: buyer, BuyerTokenID: 601, SellerID: sellerX, ListingID: 901,
		Model: "deepseek-flash", Tier: TierPeak, PriceVersion: 1, MultiplierMilli: 300,
		InputTokens: 1000, OutputTokens: 500, TotalDebitMicro: 20000,
		PlatformFeeMicro: 1400, SellerCreditMicro: 18600, OfficialCostMicro: 66000,
		RetryChain: `[]`, CreatedAt: recordTestBase,
	})
	seedRequest(t, f.db, Request{
		RequestID: "req-s2", BuyerUserID: buyer, BuyerTokenID: 601, SellerID: sellerY, ListingID: 902,
		Model: "deepseek-flash", Tier: TierOffpeak, PriceVersion: 1, MultiplierMilli: 500,
		InputTokens: 3000, OutputTokens: 900, TotalDebitMicro: 30000,
		PlatformFeeMicro: 2100, SellerCreditMicro: 27900, OfficialCostMicro: 60000,
		RetryChain: `[]`, CreatedAt: recordTestBase.Add(time.Minute),
	})

	// Seller X sees exactly their own supply.
	wX := f.get(t, "/api/marketplace/records/shares", sellerX)
	require.Equal(t, http.StatusOK, wX.Code, "body: %s", wX.Body.String())
	dataX := apiData(t, decodeAPIBody(t, wX))
	assert.Equal(t, int64(1), apiNum(t, dataX, "total"))
	itemsX := apiPageItems(t, dataX)
	require.Len(t, itemsX, 1)
	recX := itemsX[0].(map[string]any)
	assert.Equal(t, "req-s1", recX["request_id"])
	assert.Equal(t, int64(18600), apiNum(t, recX, "seller_credit_micro"), "分享者收入")
	assert.Equal(t, int64(1400), apiNum(t, recX, "platform_fee_micro"), "平台费")

	// The buyer appears as the per-seller anonymous code (§3.1): 8 hex digits,
	// and a different seller sees a different code for the SAME buyer.
	wantX, err := AnonBuyerCode(sellerX, buyer)
	require.NoError(t, err)
	assert.Equal(t, wantX, recX["buyer_code"])
	assert.Len(t, wantX, 8)

	wY := f.get(t, "/api/marketplace/records/shares", sellerY)
	require.Equal(t, http.StatusOK, wY.Code)
	itemsY := apiPageItems(t, apiData(t, decodeAPIBody(t, wY)))
	require.Len(t, itemsY, 1)
	wantY, err := AnonBuyerCode(sellerY, buyer)
	require.NoError(t, err)
	assert.Equal(t, wantY, itemsY[0].(map[string]any)["buyer_code"])
	assert.NotEqual(t, wantX, wantY, "§3.1: 每个分享者看到的代号不同")

	// The seller's 渠道 filter narrows to one listing.
	wFiltered := f.get(t, "/api/marketplace/records/shares?listing_id=902", sellerX)
	require.Equal(t, http.StatusOK, wFiltered.Code)
	assert.Equal(t, int64(0), apiNum(t, apiData(t, decodeAPIBody(t, wFiltered)), "total"))

	// The full response must not leak the buyer's real id or token id, nor
	// any key material field name (§3.1/§2.3). key_fingerprint4 (the last 4
	// chars) is the deliberate display tail, so the absence check targets the
	// full fingerprint value and the ciphertext instead.
	rawX := wX.Body.String()
	assert.NotContains(t, rawX, "918273645", "seller view leaks the buyer's real user id")
	for _, absent := range []string{
		"buyer_user_id", "buyer_token_id", "key_ciphertext", "api_key",
	} {
		assert.NotContains(t, rawX, absent, "seller view leaks %s", absent)
	}
	assert.NotContains(t, rawX, "fp-records-901", "seller view leaks the full key fingerprint")
}

// ---------------------------------------------------------------------------
// 3. detail permissions: only the owner sees the record; anyone else gets
//    404 so existence is not leaked (§3.1)
// ---------------------------------------------------------------------------

func TestApiRecordDetailPermissions(t *testing.T) {
	f := newRecordFixture(t)
	const buyer, otherBuyer = int64(7001), int64(7002)
	const seller, otherSeller = int64(8001), int64(8002)
	const chain = `[{"seq":1,"event":"select","listing_id":901},{"seq":2,"event":"switch","listing_id":903}]`

	seedRequest(t, f.db, Request{
		RequestID: "req-d1", BuyerUserID: buyer, BuyerTokenID: 501, SellerID: seller, ListingID: 901,
		Model: "deepseek-flash", Tier: TierPeak, PriceVersion: 1, MultiplierMilli: 300,
		InputTokens: 1000, OutputTokens: 500, TotalDebitMicro: 20000,
		PlatformFeeMicro: 1400, SellerCreditMicro: 18600, OfficialCostMicro: 66000,
		RetryChain: chain, CreatedAt: recordTestBase,
	})

	// Buyer detail: the owner gets the full row including retry_chain (§3.8
	// 原样返回, JSON 字符串).
	w := f.get(t, "/api/marketplace/records/usage/req-d1", buyer)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	detail := apiData(t, decodeAPIBody(t, w))
	assert.Equal(t, "req-d1", detail["request_id"])
	assert.Equal(t, chain, detail["retry_chain"])
	assert.Equal(t, int64(46000), apiNum(t, detail, "saved_micro"))
	assert.Equal(t, int64(300), apiNum(t, detail, "multiplier_milli"))
	assert.Equal(t, int64(1400), apiNum(t, detail, "platform_fee_micro"))
	assert.Equal(t, int64(18600), apiNum(t, detail, "seller_credit_micro"))

	// Anyone else on the buyer view: 404, not 403 — existence must not leak.
	assert.Equal(t, http.StatusNotFound, f.get(t, "/api/marketplace/records/usage/req-d1", otherBuyer).Code,
		"another buyer must get 404")
	assert.Equal(t, http.StatusNotFound, f.get(t, "/api/marketplace/records/usage/req-d1", seller).Code,
		"the seller of the record is still not its buyer")

	// Seller detail: the owning seller gets the row with the anonymous buyer
	// code; the retry chain is redacted (L4): no listing ids or other sellers'
	// identifiers may leak into the seller view.
	wS := f.get(t, "/api/marketplace/records/shares/req-d1", seller)
	require.Equal(t, http.StatusOK, wS.Code, "body: %s", wS.Body.String())
	detailS := apiData(t, decodeAPIBody(t, wS))
	assert.Equal(t, "req-d1", detailS["request_id"])
	assert.Equal(t, `[{"seq":1,"event":"select","at":""},{"seq":2,"event":"switch","at":""}]`, detailS["retry_chain"],
		"seller-view retry chain must be redacted")
	wantCode, err := AnonBuyerCode(seller, buyer)
	require.NoError(t, err)
	assert.Equal(t, wantCode, detailS["buyer_code"])
	assert.NotContains(t, wS.Body.String(), strconv.FormatInt(buyer, 10))

	assert.Equal(t, http.StatusNotFound, f.get(t, "/api/marketplace/records/shares/req-d1", otherSeller).Code,
		"another seller must get 404")
	assert.Equal(t, http.StatusNotFound, f.get(t, "/api/marketplace/records/shares/req-d1", buyer).Code,
		"the buyer of the record is still not its seller")

	// A nonexistent id is indistinguishable from someone else's record.
	assert.Equal(t, http.StatusNotFound, f.get(t, "/api/marketplace/records/usage/req-nope", buyer).Code)
	assert.Equal(t, http.StatusNotFound, f.get(t, "/api/marketplace/records/shares/req-nope", seller).Code)
}

// ---------------------------------------------------------------------------
// 4. pagination: page starts at 1, page_size default 20, capped at 100,
//    total returned (§5 阶段 5)
// ---------------------------------------------------------------------------

func TestApiRecordPagination(t *testing.T) {
	f := newRecordFixture(t)
	const buyer = int64(7001)
	for i := 1; i <= 26; i++ {
		seedRequest(t, f.db, Request{
			RequestID: fmt.Sprintf("req-p-%02d", i), BuyerUserID: buyer, BuyerTokenID: 501,
			SellerID: 8001, ListingID: 901, Model: "deepseek-flash", Tier: TierPeak,
			PriceVersion: 1, MultiplierMilli: 300, TotalDebitMicro: int64(i),
			OfficialCostMicro: int64(i * 3), RetryChain: `[]`,
			CreatedAt: recordTestBase.Add(time.Duration(i) * time.Minute),
		})
	}

	// Defaults: page 1, page_size 20, total 26, newest first.
	w := f.get(t, "/api/marketplace/records/usage", buyer)
	require.Equal(t, http.StatusOK, w.Code)
	data := apiData(t, decodeAPIBody(t, w))
	assert.Equal(t, int64(26), apiNum(t, data, "total"))
	assert.Equal(t, int64(1), apiNum(t, data, "page"))
	assert.Equal(t, int64(20), apiNum(t, data, "page_size"))
	items := apiPageItems(t, data)
	require.Len(t, items, 20)
	assert.Equal(t, "req-p-26", items[0].(map[string]any)["request_id"], "newest first")
	assert.Equal(t, "req-p-07", items[19].(map[string]any)["request_id"])

	// Page 2 holds the remaining 6 rows.
	w2 := f.get(t, "/api/marketplace/records/usage?page=2&page_size=20", buyer)
	require.Equal(t, http.StatusOK, w2.Code)
	data2 := apiData(t, decodeAPIBody(t, w2))
	assert.Equal(t, int64(26), apiNum(t, data2, "total"))
	assert.Equal(t, int64(2), apiNum(t, data2, "page"))
	items2 := apiPageItems(t, data2)
	require.Len(t, items2, 6)
	assert.Equal(t, "req-p-06", items2[0].(map[string]any)["request_id"])
	assert.Equal(t, "req-p-01", items2[5].(map[string]any)["request_id"])

	// page_size=999 is capped at 100: all 26 rows fit on one page.
	w3 := f.get(t, "/api/marketplace/records/usage?page=1&page_size=999", buyer)
	require.Equal(t, http.StatusOK, w3.Code)
	data3 := apiData(t, decodeAPIBody(t, w3))
	assert.Equal(t, int64(100), apiNum(t, data3, "page_size"), "page_size cap is 100")
	assert.Len(t, apiPageItems(t, data3), 26)
}

func TestApiRecordActiveSessionInProgress(t *testing.T) {
	f := newRecordFixture(t)
	buyer := int64(1001)
	seller := int64(2002)
	listingID := int64(999)

	require.NoError(t, f.db.Create(&Listing{
		ID: listingID, SellerID: seller, ChannelID: 1, Vendor: "deepseek",
		Models: []string{"deepseek-flash"}, MultiplierMilli: 300,
		Status: ListingLive, KeyFingerprint: "fp-active-999",
	}).Error)

	// Seed one completed request
	seedRequest(t, f.db, Request{
		RequestID:        "req-done",
		BuyerUserID:      buyer,
		SellerID:         seller,
		ListingID:        listingID,
		Model:            "deepseek-flash",
		InputTokens:      100,
		OutputTokens:     50,
		TotalDebitMicro:  1000,
		Status:           "success",
		CreatedAt:        time.Now().Add(-time.Minute),
	})

	// Register an active in-flight session
	activeReqID := "req-in-flight-1"
	DefaultSessionRegistry().Register(SessionEntry{
		RequestID:   activeReqID,
		ListingID:   listingID,
		BuyerID:     int(buyer),
		Model:       "deepseek-flash",
		IsStream:    true,
		FrozenMicro: 5000,
		CreatedAt:   time.Now(),
	})
	t.Cleanup(func() {
		DefaultSessionRegistry().Unregister(activeReqID)
	})

	// Buyer list usage records should return the active session prepended
	w := f.get(t, "/api/marketplace/records/usage", buyer)
	require.Equal(t, http.StatusOK, w.Code)
	data := apiData(t, decodeAPIBody(t, w))
	items := apiPageItems(t, data)
	require.GreaterOrEqual(t, len(items), 2)
	activeRec := items[0].(map[string]any)
	assert.Equal(t, activeReqID, activeRec["request_id"])
	assert.Equal(t, "in_progress", activeRec["status"])
	assert.Equal(t, "deepseek-flash", activeRec["model"])
	assert.Equal(t, int64(0), apiNum(t, activeRec, "total_debit_micro"))

	// Buyer get usage detail of in-flight session
	wDetail := f.get(t, "/api/marketplace/records/usage/"+activeReqID, buyer)
	require.Equal(t, http.StatusOK, wDetail.Code)
	detailData := apiData(t, decodeAPIBody(t, wDetail))
	assert.Equal(t, activeReqID, detailData["request_id"])
	assert.Equal(t, "in_progress", detailData["status"])

	// Seller list shares should also include the active session
	wShares := f.get(t, "/api/marketplace/records/shares", seller)
	require.Equal(t, http.StatusOK, wShares.Code)
	dataShares := apiData(t, decodeAPIBody(t, wShares))
	shareItems := apiPageItems(t, dataShares)
	require.GreaterOrEqual(t, len(shareItems), 2)
	activeShare := shareItems[0].(map[string]any)
	assert.Equal(t, activeReqID, activeShare["request_id"])
	assert.Equal(t, "in_progress", activeShare["status"])
}
