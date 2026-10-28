package marketplace

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
)

// adminAPIFixture stands up the §5 阶段 6 admin API against an isolated
// database: upstream users/channels tables, marketplace tables, a
// header-driven stand-in for middleware.AdminAuth, and the package-level relay
// state pointed at the test handle.
type adminAPIFixture struct {
	db       *gorm.DB
	engine   *gin.Engine
	adminID  int
	sellerID int64
	buyerID  int64
}

// headerAdminAuth replaces middleware.AdminAuth in tests: the X-Test-User-Id
// header plays the admin dashboard session; anything else is a 401.
func headerAdminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("X-Test-User-Id"))
		id, err := strconv.Atoi(raw)
		if raw == "" || err != nil || id <= 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "unauthorized"})
			return
		}
		common.SetContextKey(c, constant.ContextKeyUserId, id)
		// The marketplace admin handlers re-check the role (L5). Tests default
		// to root; X-Test-Role lowers it to exercise the non-root rejection.
		role := common.RoleRootUser
		if rawRole := strings.TrimSpace(c.GetHeader("X-Test-Role")); rawRole != "" {
			if parsed, err := strconv.Atoi(rawRole); err == nil {
				role = parsed
			}
		}
		c.Set("role", role)
		c.Next()
	}
}

// trustPoolTestGrants impersonates a per-user trust_pool authorization for one
// test user: key is the user id, value is the set of granted actions.
var trustPoolTestGrants = map[int]map[string]bool{}

// trustPoolTestGuard mirrors RequirePermission(trust_pool, action): root is a
// superuser; every other role needs the explicit per-user grant.
func trustPoolTestGuard(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetInt("role") >= common.RoleRootUser {
			c.Next()
			return
		}
		userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
		if trustPoolTestGrants[userID][action] {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "Unauthorized, insufficient privileges"})
	}
}

func newAdminAPIFixture(t *testing.T) *adminAPIFixture {
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
	SetAnonSecret("admin-test-secret")

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

	admin := model.User{Username: "admin", Password: "placeholder-pass", AffCode: "admin", Role: common.RoleAdminUser}
	require.NoError(t, db.Create(&admin).Error)
	seller := model.User{Username: "seller", Password: "placeholder-pass", AffCode: "seller"}
	require.NoError(t, db.Create(&seller).Error)
	buyer := model.User{Username: "buyer", Password: "placeholder-pass", AffCode: "buyer"}
	require.NoError(t, db.Create(&buyer).Error)

	engine := gin.New()
	group := engine.Group("/api/marketplace", headerAdminAuth())
	registerAdminRootRoutes(group)
	// P5-C: the trust-pool routes live on a permission-guarded group in the
	// real router. Tests install the same action guard locally (middleware
	// cannot be imported here: it imports marketplace).
	for _, route := range TrustPoolRoutes() {
		group.Handle(route.Method, route.Path, trustPoolTestGuard(route.Action), route.Handler)
	}

	return &adminAPIFixture{
		db:       db,
		engine:   engine,
		adminID:  admin.Id,
		sellerID: int64(seller.Id),
		buyerID:  int64(buyer.Id),
	}
}

func (f *adminAPIFixture) do(t *testing.T, method, path string, body any, userID int) *httptest.ResponseRecorder {
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

// doRole is do with an explicit role header (L5 tests).
func (f *adminAPIFixture) doRole(t *testing.T, method, path string, body any, userID, role int) *httptest.ResponseRecorder {
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
	req.Header.Set("X-Test-Role", strconv.Itoa(role))
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

func decodeAdminBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &v))
	return v
}

// addAdminListing opens one listing for the fixture seller and returns it.
// The key carries a counter because Windows clocks can return the same
// UnixNano value for two calls microseconds apart, which would trip the
// one-key-per-platform rule inside a single test.
var adminTestKeySeq atomic.Int64

func (f *adminAPIFixture) addAdminListing(t *testing.T, status string) Listing {
	t.Helper()
	key := "sk-admin-test-" + strconv.FormatInt(adminTestKeySeq.Add(1), 10) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).Update("status", status).Error)
	return listing
}

// TestAdminPriceBookAddVersionAndList covers §5 阶段 6 价目表: adding a new
// version row and listing it; editing an existing row is rejected.
func TestAdminPriceBookAddVersionAndList(t *testing.T) {
	f := newAdminAPIFixture(t)

	// POST a new version row for deepseek-flash/flat.
	w := f.do(t, http.MethodPost, "/api/marketplace/admin/prices", map[string]any{
		"model": "deepseek-flash", "vendor": "deepseek", "tier": "flat",
		"input_micro_per_mtok": 2_000_000, "output_micro_per_mtok": 5_000_000,
		"cache_read_micro_per_mtok": 40_000, "cache_write_micro_per_mtok": 2_000_000,
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeAdminBody(t, w)
	var row PriceBook
	raw, err := common.Marshal(body["data"])
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(raw, &row))
	// Auto-assigned version: the seed row is version 1, so this is 2.
	assert.EqualValues(t, 2, row.Version)
	assert.EqualValues(t, 2_000_000, row.InputMicroPerMTok)

	// Listing with ?model=&vendor=&tier= returns both versions, newest first.
	w = f.do(t, http.MethodGet, "/api/marketplace/admin/prices?model=deepseek-flash&vendor=deepseek&tier=flat", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	body = decodeAdminBody(t, w)
	items, ok := body["data"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, items)
	raw, err = common.Marshal(items[0])
	require.NoError(t, err)
	var first PriceBook
	require.NoError(t, common.Unmarshal(raw, &first))
	assert.EqualValues(t, 2, first.Version)

	// Re-posting the same version for the same key is rejected (价目只增不改).
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/prices", map[string]any{
		"model": "deepseek-flash", "vendor": "deepseek", "tier": "flat",
		"input_micro_per_mtok": 9_000_000, "output_micro_per_mtok": 9_000_000,
		"cache_read_micro_per_mtok": 9_000_000, "cache_write_micro_per_mtok": 9_000_000,
		"version": 2,
	}, f.adminID)
	assert.Equal(t, http.StatusConflict, w.Code)

	// The original version-1 row is untouched.
	var v1 PriceBook
	require.NoError(t, f.db.Where("model = ? AND vendor = ? AND tier = ? AND version = ?",
		"deepseek-flash", "deepseek", "flat", 1).Take(&v1).Error)
	assert.EqualValues(t, 1_000_000, v1.InputMicroPerMTok)
}

// TestAdminHolidaysImportAndList covers §5 阶段 6 节假日: importing an array and
// listing by year.
func TestAdminHolidaysImportAndList(t *testing.T) {
	f := newAdminAPIFixture(t)

	w := f.do(t, http.MethodPost, "/api/marketplace/admin/holidays", []map[string]any{
		{"date": "2026-10-01", "kind": "holiday"},
		{"date": "2026-10-02", "kind": "holiday"},
		{"date": "2026-10-10", "kind": "workday"},
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.EqualValues(t, 3, decodeAdminBody(t, w)["data"].(map[string]any)["imported"])

	w = f.do(t, http.MethodGet, "/api/marketplace/admin/holidays?year=2026", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeAdminBody(t, w)["data"].(map[string]any)
	items, ok := data["holidays"].([]any)
	require.True(t, ok)
	assert.Len(t, items, 3)
	assert.Contains(t, data, "next_year_imported")
	assert.Contains(t, data, "next_year")

	// Re-importing a date upserts its kind (ImportHolidays semantics).
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/holidays", []map[string]any{
		{"date": "2026-10-01", "kind": "workday"},
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	var h Holiday
	require.NoError(t, f.db.Where("date = ?", "2026-10-01").Take(&h).Error)
	assert.Equal(t, HolidayKindWorkday, h.Kind)

	// Invalid kind → 400.
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/holidays", []map[string]any{
		{"date": "2026-10-03", "kind": "bogus"},
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestAdminListingReview covers §5 阶段 6 挂单审核: ban/unban state machine and
// that the list response never contains the ciphertext or full fingerprint.
func TestAdminListingReview(t *testing.T) {
	f := newAdminAPIFixture(t)
	listing := f.addAdminListing(t, ListingLive)

	// The list carries seller_id and only the fingerprint's first 8 chars.
	w := f.do(t, http.MethodGet, "/api/marketplace/admin/listings?status=live", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeAdminBody(t, w)
	items, ok := body["data"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	first, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, f.sellerID, first["seller_id"])
	assert.Equal(t, "live", first["status"])
	fp8, ok := first["key_fingerprint8"].(string)
	require.True(t, ok)
	assert.Len(t, fp8, 8)
	// §2.3: no ciphertext, no full fingerprint, no key anywhere in the JSON.
	assert.NotContains(t, w.Body.String(), listing.KeyFingerprint)
	assert.NotContains(t, w.Body.String(), "sk-admin-test")

	// ban: live → banned.
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/listings/"+strconv.FormatInt(listing.ID, 10)+"/ban", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	row, err := GetListing(f.db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingBanned, row.Status)

	// unban: banned → paused (人工恢复).
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/listings/"+strconv.FormatInt(listing.ID, 10)+"/unban", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	row, err = GetListing(f.db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPaused, row.Status)

	// Banning a paused listing works; banning a draft listing is rejected.
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/listings/"+strconv.FormatInt(listing.ID, 10)+"/ban", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	draft := f.addAdminListing(t, ListingDraft)
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/listings/"+strconv.FormatInt(draft.ID, 10)+"/ban", nil, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ---------------------------------------------------------------------------
// P2 运营审核: 待审队列 + 通过/拒绝
// ---------------------------------------------------------------------------

// latestReviewProbe returns the newest operator decision recorded for a listing.
func latestReviewProbe(t *testing.T, db *gorm.DB, listingID int64) Probe {
	t.Helper()
	var probe Probe
	require.NoError(t, db.Where("listing_id = ? AND type = ?", listingID, ProbeTypeReview).
		Order("id DESC").Take(&probe).Error)
	return probe
}

// TestAdminPendingListingQueue covers the review queue: empty, then only
// pending_review rows, newest first, with the seller's real-name status.
func TestAdminPendingListingQueue(t *testing.T) {
	f := newAdminAPIFixture(t)
	w := f.do(t, http.MethodGet, "/api/marketplace/admin/listings/pending", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	items, ok := decodeAdminBody(t, w)["data"].([]any)
	require.True(t, ok)
	assert.Empty(t, items)

	require.NoError(t, f.db.Create(&Seller{
		UserID:           f.sellerID,
		Status:           SellerStatusActive,
		RealNameStatus:   SellerRealNameVerified,
		AgreementVersion: CurrentSellerAgreementVersion,
	}).Error)
	older := f.addAdminListing(t, ListingPendingReview)
	newer := f.addAdminListing(t, ListingPendingReview)
	live := f.addAdminListing(t, ListingLive)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", older.ID).
		Update("created_at", time.Now().Add(-time.Hour)).Error)

	w = f.do(t, http.MethodGet, "/api/marketplace/admin/listings/pending", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	groups, ok := decodeAdminBody(t, w)["data"].([]any)
	require.True(t, ok)
	require.Len(t, groups, 1, "platform listings without a custom URL share the empty host bucket")
	group, ok := groups[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "", group["host"])
	assert.EqualValues(t, 2, group["count"])
	items, ok = group["listings"].([]any)
	require.True(t, ok)
	require.Len(t, items, 2, "only pending_review listings are queued")
	for _, item := range items {
		assert.NotEqualValues(t, live.ID, item.(map[string]any)["id"])
	}

	first, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, newer.ID, first["id"])
	assert.EqualValues(t, f.sellerID, first["seller_id"])
	assert.Equal(t, SeedVendorDeepSeek, first["vendor"])
	assert.Equal(t, "", first["custom_base_url"], "platform channels have no seller-supplied address")
	assert.Equal(t, []any{"deepseek-flash"}, first["models"])
	assert.Equal(t, SellerRealNameVerified, first["seller_real_name_status"])

	second, ok := items[1].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, older.ID, second["id"], "newest first")
}

// addCustomPendingListing points a pending listing at a seller-defined channel
// with the given base URL and returns the updated listing. The channel row is
// written directly: the review queue only reads the vendor slug and its base.
func (f *adminAPIFixture) addCustomPendingListing(t *testing.T, baseURL string) Listing {
	t.Helper()
	listing := f.addAdminListing(t, ListingPendingReview)
	slug := fmt.Sprintf("custom-%d-%x", f.sellerID, len(baseURL)+int(listing.ID))
	protocols, err := common.Marshal(map[string]string{"openai": baseURL})
	require.NoError(t, err)
	require.NoError(t, f.db.Create(&ChannelPool{
		Slug:      slug,
		Name:      baseURL,
		Kind:      ChannelKindThirdParty,
		Protocols: string(protocols),
		Adaptor:   VendorAdaptorOpenAI,
		Status:    PoolStatusActive,
	}).Error)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).Update("vendor", slug).Error)
	return listing
}

// TestAdminPendingListingQueueGroupsByHost covers the P5 host aggregation: one
// bucket per seller-defined upstream host, busiest first, newest first inside
// each bucket, and the custom address surfaced for the operator.
func TestAdminPendingListingQueueGroupsByHost(t *testing.T) {
	f := newAdminAPIFixture(t)
	require.NoError(t, f.db.Create(&Seller{
		UserID:           f.sellerID,
		Status:           SellerStatusActive,
		RealNameStatus:   SellerRealNameVerified,
		AgreementVersion: CurrentSellerAgreementVersion,
	}).Error)

	alphaOne := f.addCustomPendingListing(t, "https://api.alpha.example/v1")
	alphaTwo := f.addCustomPendingListing(t, "https://api.alpha.example/other")
	beta := f.addCustomPendingListing(t, "https://beta.example/v1")
	f.addAdminListing(t, ListingPendingReview) // platform channel, empty host
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", alphaOne.ID).
		Update("created_at", time.Now().Add(-time.Hour)).Error)

	w := f.do(t, http.MethodGet, "/api/marketplace/admin/listings/pending", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	groups, ok := decodeAdminBody(t, w)["data"].([]any)
	require.True(t, ok)
	require.Len(t, groups, 3)

	first := groups[0].(map[string]any)
	assert.Equal(t, "api.alpha.example", first["host"])
	assert.EqualValues(t, 2, first["count"])
	alphaItems := first["listings"].([]any)
	require.Len(t, alphaItems, 2)
	assert.EqualValues(t, alphaTwo.ID, alphaItems[0].(map[string]any)["id"], "newest first")
	assert.EqualValues(t, alphaOne.ID, alphaItems[1].(map[string]any)["id"])
	for _, item := range alphaItems {
		assert.Equal(t, "api.alpha.example", pendingListingHost(item.(map[string]any)["custom_base_url"].(string)))
	}

	second := groups[1].(map[string]any)
	assert.Equal(t, "", second["host"], "the platform bucket ties on count and sorts first by host asc")
	assert.EqualValues(t, 1, second["count"])

	third := groups[2].(map[string]any)
	assert.Equal(t, "beta.example", third["host"])
	assert.EqualValues(t, beta.ID, third["listings"].([]any)[0].(map[string]any)["id"])
}

// TestAdminApproveListing covers approval: pending_review → verifying, the
// audit probe, and the background verification that promotes it to canary.
func TestAdminApproveListing(t *testing.T) {
	f := newAdminAPIFixture(t)
	h := allGreenHandler()
	srv, _ := newVendorServer(t, h)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	restore := setSellerVerifyClientForTest(&http.Client{Transport: rewriteTransport{target: target}})
	defer restore()

	key := uniqueTestKey("sk-admin-approve")
	h.wantAuth = "Bearer " + key
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: 500,
	})
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).
		Update("status", ListingPendingReview).Error)
	idText := strconv.FormatInt(listing.ID, 10)

	w := f.do(t, http.MethodPost, "/api/marketplace/admin/listings/"+idText+"/approve",
		map[string]any{"notes": "upstream certified by ops"}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data, ok := decodeAdminBody(t, w)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, ListingVerifying, data["status"])

	probe := latestReviewProbe(t, f.db, listing.ID)
	assert.True(t, probe.OK)
	var detail map[string]any
	require.NoError(t, common.Unmarshal([]byte(probe.Detail), &detail))
	assert.EqualValues(t, f.adminID, detail["reviewer_id"])
	assert.Equal(t, "approve", detail["action"])
	assert.Equal(t, "upstream certified by ops", detail["notes"])
	assert.Equal(t, ListingPendingReview, detail["previous_status"])
	assert.Equal(t, ListingVerifying, detail["new_status"])

	// Approval hands the row to the ordinary verifier, which promotes it.
	require.Eventually(t, func() bool {
		row, err := GetListing(f.db, listing.ID)
		return err == nil && row.Status == ListingCanary
	}, 5*time.Second, 10*time.Millisecond)
	var verifyProbes int64
	require.NoError(t, f.db.Model(&Probe{}).
		Where("listing_id = ? AND type = ?", listing.ID, ProbeTypeVerify).Count(&verifyProbes).Error)
	assert.EqualValues(t, 1, verifyProbes)

	// Approving again is not a pending_review edge any more.
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/listings/"+idText+"/approve", nil, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = f.do(t, http.MethodPost, "/api/marketplace/admin/listings/999999/approve", nil, f.adminID)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAdminRejectListing covers both rejection outcomes and the guards: an
// unknown action, a listing that is no longer pending, and an unknown id.
func TestAdminRejectListing(t *testing.T) {
	f := newAdminAPIFixture(t)
	reject := func(id int64, action string) *httptest.ResponseRecorder {
		return f.do(t, http.MethodPost,
			"/api/marketplace/admin/listings/"+strconv.FormatInt(id, 10)+"/reject",
			map[string]any{"action": action, "notes": "not certified"}, f.adminID)
	}

	toDraft := f.addAdminListing(t, ListingPendingReview)
	w := reject(toDraft.ID, "draft")
	require.Equal(t, http.StatusOK, w.Code)
	row, err := GetListing(f.db, toDraft.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingDraft, row.Status, "the seller can fix and resubmit")
	probe := latestReviewProbe(t, f.db, toDraft.ID)
	assert.False(t, probe.OK)
	var detail map[string]any
	require.NoError(t, common.Unmarshal([]byte(probe.Detail), &detail))
	assert.Equal(t, "reject", detail["action"])
	assert.Equal(t, "not certified", detail["notes"])
	assert.Equal(t, ListingPendingReview, detail["previous_status"])
	assert.Equal(t, ListingDraft, detail["new_status"])

	toBan := f.addAdminListing(t, ListingPendingReview)
	w = reject(toBan.ID, "ban")
	require.Equal(t, http.StatusOK, w.Code)
	row, err = GetListing(f.db, toBan.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingBanned, row.Status)
	detail = nil
	require.NoError(t, common.Unmarshal([]byte(latestReviewProbe(t, f.db, toBan.ID).Detail), &detail))
	assert.Equal(t, ListingBanned, detail["new_status"])

	// An unknown action changes nothing.
	bad := f.addAdminListing(t, ListingPendingReview)
	w = reject(bad.ID, "maybe")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	row, err = GetListing(f.db, bad.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPendingReview, row.Status)

	// A listing that already left pending_review cannot be reviewed again.
	w = reject(toDraft.ID, "draft")
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = reject(999999, "draft")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAdminListingReviewRootOnly covers the L5 gate on the P2 review API: the
// group is RootAuth and the handlers re-check the role themselves.
func TestAdminListingReviewRootOnly(t *testing.T) {
	f := newAdminAPIFixture(t)
	listing := f.addAdminListing(t, ListingPendingReview)
	idText := strconv.FormatInt(listing.ID, 10)
	nonRoot := common.RoleAdminUser

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/marketplace/admin/listings/pending", nil},
		{http.MethodPost, "/api/marketplace/admin/listings/" + idText + "/approve", nil},
		{http.MethodPost, "/api/marketplace/admin/listings/" + idText + "/reject",
			map[string]any{"action": "draft"}},
	}
	for _, tc := range cases {
		w := f.doRole(t, tc.method, tc.path, tc.body, f.adminID, nonRoot)
		assert.Equal(t, http.StatusForbidden, w.Code, tc.path)
	}
	row, err := GetListing(f.db, listing.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPendingReview, row.Status, "a refused review must not change the listing")
}

// ---------------------------------------------------------------------------
// 全链路排查 (P5): request_id → 请求 / 重试链 / 账本 / 验真巡检
// ---------------------------------------------------------------------------

// TestAdminRequestTrace covers GET /admin/requests/:request_id: the full
// request row, the parsed retry chain, every ledger row and recent
// verify/patrol/review probes, plus the root-only gate and 404.
func TestAdminRequestTrace(t *testing.T) {
	f := newAdminAPIFixture(t)
	listing := f.addAdminListing(t, ListingLive)

	chain, err := common.Marshal([]RelayHop{
		{Seq: 1, Event: "select", ListingID: listing.ID, Detail: "cheapest", At: "2026-10-01T00:00:00Z"},
		{Seq: 2, Event: "success", ListingID: listing.ID, Detail: "200", At: "2026-10-01T00:00:01Z"},
	})
	require.NoError(t, err)
	require.NoError(t, f.db.Create(&Request{
		RequestID:       "trace-req-1",
		BuyerUserID:     f.buyerID,
		BuyerTokenID:    7,
		SellerID:        f.sellerID,
		ListingID:       listing.ID,
		Model:           "deepseek-flash",
		UpstreamModel:   "deepseek-flash-0901",
		Tier:            TierOffpeak,
		PriceVersion:    1,
		MultiplierMilli: 500,
		InputTokens:     10,
		OutputTokens:    20,
		TotalDebitMicro: 1234,
		Status:          RequestSuccess,
		RetryChain:      string(chain),
		LatencyMs:       321,
		CreatedAt:       time.Now(),
	}).Error)
	require.NoError(t, f.db.Create(&LedgerEntry{
		RequestID: "trace-req-1", EntryType: EntryBuyerDebit, Account: "buyer:1",
		ListingID: listing.ID, AmountMicro: -1234, Status: LedgerAvailable, CreatedAt: time.Now(),
	}).Error)
	require.NoError(t, f.db.Create(&LedgerEntry{
		RequestID: "trace-req-1", EntryType: EntrySellerCredit, Account: "seller:2",
		ListingID: listing.ID, AmountMicro: 1000, Status: LedgerPending, CreatedAt: time.Now(),
	}).Error)
	for _, probeType := range []string{ProbeTypeVerify, ProbeTypePatrol, ProbeTypeReview, ProbeTypeReport} {
		require.NoError(t, f.db.Create(&Probe{
			ListingID: listing.ID, Type: probeType, OK: probeType != ProbeTypeReport,
			Detail: "evidence " + probeType, CreatedAt: time.Now(),
		}).Error)
	}

	w := f.do(t, http.MethodGet, "/api/marketplace/admin/requests/trace-req-1", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data, ok := decodeAdminBody(t, w)["data"].(map[string]any)
	require.True(t, ok)

	req, ok := data["request"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "trace-req-1", req["request_id"])
	assert.EqualValues(t, f.buyerID, req["buyer_user_id"])
	assert.EqualValues(t, f.sellerID, req["seller_id"])
	assert.EqualValues(t, listing.ID, req["listing_id"])
	assert.Equal(t, "deepseek-flash", req["model"])
	assert.Equal(t, "deepseek-flash-0901", req["upstream_model"])
	assert.Equal(t, RequestSuccess, req["status"])
	assert.EqualValues(t, 321, req["latency_ms"])
	assert.EqualValues(t, 1234, req["total_debit_micro"])

	hops, ok := data["retry_chain"].([]any)
	require.True(t, ok)
	require.Len(t, hops, 2)
	firstHop := hops[0].(map[string]any)
	assert.EqualValues(t, 1, firstHop["seq"])
	assert.Equal(t, "select", firstHop["event"])
	assert.EqualValues(t, listing.ID, firstHop["listing_id"])

	ledger, ok := data["ledger"].([]any)
	require.True(t, ok)
	require.Len(t, ledger, 2)
	assert.Equal(t, EntryBuyerDebit, ledger[0].(map[string]any)["entry_type"])
	assert.EqualValues(t, -1234, ledger[0].(map[string]any)["amount_micro"])
	assert.Equal(t, EntrySellerCredit, ledger[1].(map[string]any)["entry_type"])
	assert.Equal(t, LedgerPending, ledger[1].(map[string]any)["status"])

	probes, ok := data["probes"].([]any)
	require.True(t, ok)
	require.Len(t, probes, 3, "report probes are not part of verification evidence")
	for _, item := range probes {
		assert.NotEqual(t, ProbeTypeReport, item.(map[string]any)["type"])
	}

	// A corrupt retry chain does not hide the rest of the trace.
	require.NoError(t, f.db.Create(&Request{
		RequestID: "trace-req-bad-chain", BuyerUserID: f.buyerID, SellerID: f.sellerID,
		ListingID: listing.ID, Status: RequestFailed, RetryChain: "not-json", CreatedAt: time.Now(),
	}).Error)
	w = f.do(t, http.MethodGet, "/api/marketplace/admin/requests/trace-req-bad-chain", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data = decodeAdminBody(t, w)["data"].(map[string]any)
	hops, ok = data["retry_chain"].([]any)
	require.True(t, ok)
	assert.Empty(t, hops)

	// Unknown request → 404; non-root → 403; no session → 401.
	w = f.do(t, http.MethodGet, "/api/marketplace/admin/requests/does-not-exist", nil, f.adminID)
	assert.Equal(t, http.StatusNotFound, w.Code)
	w = f.doRole(t, http.MethodGet, "/api/marketplace/admin/requests/trace-req-1", nil, f.adminID, common.RoleAdminUser)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.do(t, http.MethodGet, "/api/marketplace/admin/requests/trace-req-1", nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAdminPayoutListAndNotFound covers the payout queue: the list endpoint
// (which does not need payout.go) and the 404 path of the approve/reject/
// mark-paid actions for an unknown id.
func TestAdminPayoutListAndNotFound(t *testing.T) {
	f := newAdminAPIFixture(t)
	// Seed a payout row directly so the list has content.
	payout := Payout{
		SellerID:       f.sellerID,
		AmountMicro:    50_000_000,
		Status:         PayoutPending,
		IdempotencyKey: "admin-payout-list-test",
		CreatedAt:      time.Now(),
	}
	require.NoError(t, f.db.Create(&payout).Error)

	w := f.do(t, http.MethodGet, "/api/marketplace/admin/payouts?status=pending", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	items, ok := decodeAdminBody(t, w)["data"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)

	// Unknown payout id → 404 on every action.
	for _, action := range []string{"approve", "reject", "mark-paid"} {
		w = f.do(t, http.MethodPost, "/api/marketplace/admin/payouts/999999/"+action, nil, f.adminID)
		assert.Equal(t, http.StatusNotFound, w.Code, action)
	}
}

// TestAdminReverse covers §5 阶段 6 冲正: a settled request is reversed, balances
// returned, and a second reversal is a 400.
func TestAdminReverse(t *testing.T) {
	f := newAdminAPIFixture(t)
	// Settle one request so there is a reversible ledger.
	now := time.Now()
	s := Settlement{
		RequestID:       "admin-reverse-req-1",
		BuyerUserID:     f.buyerID,
		SellerUserID:    f.sellerID,
		ListingID:       1,
		PriceVersion:    1,
		MultiplierMilli: 500,
		TokenDebit:      10_000_000,
		Now:             now,
	}
	require.NoError(t, PostSettlement(f.db, s))

	w := f.do(t, http.MethodPost, "/api/marketplace/admin/reverse", map[string]any{
		"request_id": "admin-reverse-req-1",
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeAdminBody(t, w)
	assert.Equal(t, true, body["data"].(map[string]any)["reversed"])

	// The seller credit is now reversed; the buyer account nets to zero.
	var credit LedgerEntry
	require.NoError(t, f.db.Where("request_id = ? AND entry_type = ?", "admin-reverse-req-1", EntrySellerCredit).Take(&credit).Error)
	assert.Equal(t, LedgerReversed, credit.Status)

	// Second reversal → 400 already reversed.
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/reverse", map[string]any{
		"request_id": "admin-reverse-req-1",
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Unknown request → 400 not reversible.
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/reverse", map[string]any{
		"request_id": "admin-reverse-unknown",
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestAdminDeanon covers §5 阶段 6 代号反查: two buyers' requests against one
// seller, and the code resolves back to the correct buyer; a wrong code is 404.
func TestAdminDeanon(t *testing.T) {
	f := newAdminAPIFixture(t)
	// Two distinct buyers with requests against the fixture seller.
	otherBuyer := model.User{Username: "buyer2", Password: "placeholder-pass", AffCode: "buyer2"}
	require.NoError(t, f.db.Create(&otherBuyer).Error)

	now := time.Now()
	for i, buyer := range []int64{f.buyerID, int64(otherBuyer.Id)} {
		require.NoError(t, f.db.Create(&Request{
			RequestID:       "admin-deanon-" + strconv.Itoa(i),
			BuyerUserID:     buyer,
			SellerID:        f.sellerID,
			Model:           "deepseek-flash",
			Status:          RequestSuccess,
			TotalDebitMicro: 100,
			CreatedAt:       now,
		}).Error)
	}

	code1, err := AnonBuyerCode(f.sellerID, f.buyerID)
	require.NoError(t, err)
	code2, err := AnonBuyerCode(f.sellerID, int64(otherBuyer.Id))
	require.NoError(t, err)
	assert.NotEqual(t, code1, code2, "each seller sees a different code per buyer")

	// Reverse lookup of buyer 1's code.
	w := f.do(t, http.MethodGet, "/api/marketplace/admin/deanon?seller_id="+strconv.FormatInt(f.sellerID, 10)+"&code="+code1, nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeAdminBody(t, w)
	assert.EqualValues(t, f.buyerID, body["data"].(map[string]any)["buyer_user_id"])

	// Reverse lookup of buyer 2's code.
	w = f.do(t, http.MethodGet, "/api/marketplace/admin/deanon?seller_id="+strconv.FormatInt(f.sellerID, 10)+"&code="+code2, nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.EqualValues(t, otherBuyer.Id, decodeAdminBody(t, w)["data"].(map[string]any)["buyer_user_id"])

	// A wrong code → 404.
	w = f.do(t, http.MethodGet, "/api/marketplace/admin/deanon?seller_id="+strconv.FormatInt(f.sellerID, 10)+"&code=deadbeef", nil, f.adminID)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAdminRequiresAuth covers the admin gate: no login → 401.
func TestAdminRequiresAuth(t *testing.T) {
	f := newAdminAPIFixture(t)
	w := f.do(t, http.MethodGet, "/api/marketplace/admin/prices", nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ---------------------------------------------------------------------------
// M11 价目 / 按次价目 / 日历
// ---------------------------------------------------------------------------

// TestAdminPriceValidation covers M11: backdated effective_at and unknown tier
// are rejected; a future effective_at is accepted.
func TestAdminPriceValidation(t *testing.T) {
	f := newAdminAPIFixture(t)

	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	w := f.do(t, http.MethodPost, "/api/marketplace/admin/prices", map[string]any{
		"model": "deepseek-flash", "vendor": "deepseek", "tier": "flat",
		"input_micro_per_mtok": 1_000_000, "output_micro_per_mtok": 4_000_000,
		"cache_read_micro_per_mtok": 20_000, "cache_write_micro_per_mtok": 1_000_000,
		"effective_at": past,
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code, "backdated price version")

	w = f.do(t, http.MethodPost, "/api/marketplace/admin/prices", map[string]any{
		"model": "deepseek-flash", "vendor": "deepseek", "tier": "bogus",
		"input_micro_per_mtok": 1, "output_micro_per_mtok": 1,
		"cache_read_micro_per_mtok": 1, "cache_write_micro_per_mtok": 1,
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code, "tier must be flat")

	// Peak/off-peak pricing is retired: those tiers are rejected too.
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/prices", map[string]any{
		"model": "deepseek-flash", "vendor": "deepseek", "tier": "peak",
		"input_micro_per_mtok": 1, "output_micro_per_mtok": 1,
		"cache_read_micro_per_mtok": 1, "cache_write_micro_per_mtok": 1,
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code, "peak is retired")

	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/prices", map[string]any{
		"model": "deepseek-flash", "vendor": "deepseek", "tier": "flat",
		"input_micro_per_mtok": 1_100_000, "output_micro_per_mtok": 4_100_000,
		"cache_read_micro_per_mtok": 21_000, "cache_write_micro_per_mtok": 1_100_000,
		"effective_at": future,
	}, f.adminID)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestPerCallPrices covers the §3.4/§12 M11 per-call price admin CRUD.
func TestPerCallPrices(t *testing.T) {
	f := newAdminAPIFixture(t)

	w := f.do(t, http.MethodPost, "/api/marketplace/admin/per-call-prices", map[string]any{
		"vendor": "deepseek", "item": "search", "unit_price_micro": 6_000,
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	row := decodeAdminBody(t, w)["data"].(map[string]any)
	// PerCallPrice has no JSON tags; encoding/json keeps the Go field names.
	assert.EqualValues(t, 2, row["Version"], "seed search row is version 1")
	assert.EqualValues(t, 6_000, row["UnitPriceMicro"])

	w = f.do(t, http.MethodGet, "/api/marketplace/admin/per-call-prices?vendor=deepseek&item=search", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	items, ok := decodeAdminBody(t, w)["data"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, items)
	assert.EqualValues(t, 2, items[0].(map[string]any)["Version"])

	// Unknown item and backdated effective_at are rejected.
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/per-call-prices", map[string]any{
		"vendor": "deepseek", "item": "bogus", "unit_price_micro": 6_000,
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = f.do(t, http.MethodPost, "/api/marketplace/admin/per-call-prices", map[string]any{
		"vendor": "deepseek", "item": "fetch", "unit_price_micro": 6_000,
		"effective_at": time.Now().Add(-time.Hour).Format(time.RFC3339),
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHolidaysCalendarReload covers M11: an import reloads the in-process
// calendar immediately (TierAt assertion) and GET reports next_year_imported.
func TestHolidaysCalendarReload(t *testing.T) {
	f := newAdminAPIFixture(t)
	_, cal := relayState()
	require.NotNil(t, cal)

	// Pick a weekday in next calendar year so (a) the calendar first reports
	// peak at 10:00 and (b) the import flips the next_year_imported flag.
	nextYear := nextCalendarYear(time.Now())
	var date string
	for day := 1; day <= 10; day++ {
		d := time.Date(nextYear, 1, day, 10, 0, 0, 0, time.UTC)
		wd := d.Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			date = d.Format("2006-01-02")
			break
		}
	}
	require.NotEmpty(t, date, "a weekday in early January must exist")
	at, err := time.ParseInLocation("2006-01-02 15:04", date+" 10:00", cal.loc)
	require.NoError(t, err)
	require.Equal(t, TierFlat, cal.TierAt(at), "billing is flat since peak/off-peak was retired")

	// next_year_imported starts false (seed contains no next-year rows).
	w := f.do(t, http.MethodGet, "/api/marketplace/admin/holidays", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, false, data["next_year_imported"])

	w = f.do(t, http.MethodPost, "/api/marketplace/admin/holidays", []map[string]any{
		{"date": date, "kind": "holiday"},
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)

	// Holidays no longer change the billing tier.
	assert.Equal(t, TierFlat, cal.TierAt(at), "holidays do not affect the flat tier")

	w = f.do(t, http.MethodGet, "/api/marketplace/admin/holidays", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data = decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, true, data["next_year_imported"])

	// And the endpoint lists the imported date.
	items, ok := data["holidays"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	assert.Equal(t, date, items[0].(map[string]any)["Date"])
}

// ---------------------------------------------------------------------------
// L5: root-only sensitive actions + no self-review
// ---------------------------------------------------------------------------

// TestAdminRootOnlySensitiveActions covers L5: 提现审批 / 冲正 / 代号反查
// reject non-root administrators with 403.
func TestAdminRootOnlySensitiveActions(t *testing.T) {
	f := newAdminAPIFixture(t)
	payout := Payout{
		SellerID: f.sellerID, AmountMicro: 50_000_000, Status: PayoutPending,
		IdempotencyKey: "root-only-payout", CreatedAt: time.Now(),
	}
	require.NoError(t, f.db.Create(&payout).Error)

	nonRoot := common.RoleAdminUser
	w := f.doRole(t, http.MethodPost,
		"/api/marketplace/admin/payouts/"+strconv.FormatInt(payout.ID, 10)+"/approve", nil, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)

	w = f.doRole(t, http.MethodPost, "/api/marketplace/admin/reverse",
		map[string]any{"request_id": "whatever"}, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)

	w = f.doRole(t, http.MethodGet,
		"/api/marketplace/admin/deanon?seller_id="+strconv.FormatInt(f.sellerID, 10)+"&code=deadbeef", nil, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Root can still act (here: approve another seller's payout).
	w = f.doRole(t, http.MethodPost,
		"/api/marketplace/admin/payouts/"+strconv.FormatInt(payout.ID, 10)+"/approve", nil, f.adminID, common.RoleRootUser)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestAdminSelfPayoutForbidden covers L5: an administrator cannot approve or
// mark paid their own withdrawal.
func TestAdminSelfPayoutForbidden(t *testing.T) {
	f := newAdminAPIFixture(t)
	payout := Payout{
		SellerID: int64(f.adminID), AmountMicro: 50_000_000, Status: PayoutPending,
		IdempotencyKey: "self-payout", CreatedAt: time.Now(),
	}
	require.NoError(t, f.db.Create(&payout).Error)
	path := "/api/marketplace/admin/payouts/" + strconv.FormatInt(payout.ID, 10)

	w := f.do(t, http.MethodPost, path+"/approve", nil, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = f.do(t, http.MethodPost, path+"/reject", nil, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = f.do(t, http.MethodPost, path+"/mark-paid", nil, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// The payout is untouched.
	var row Payout
	require.NoError(t, f.db.Where("id = ?", payout.ID).Take(&row).Error)
	assert.Equal(t, PayoutPending, row.Status)
}

// ---------------------------------------------------------------------------
// 渠道模型映射表 admin API
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 模型池 / 渠道池 admin API (P1)
// ---------------------------------------------------------------------------

// requirePoolSeed migrates and seeds the pool tables on an admin fixture. The
// shared fixture intentionally does not own this step (it is an Init concern),
// so each pool test asks for it explicitly.
func requirePoolSeed(t *testing.T, f *adminAPIFixture) {
	t.Helper()
	require.NoError(t, MigrateModelChannelPool(f.db))
	require.NoError(t, SeedModelChannelPool(f.db))
}

// TestAdminPoolModelsAPI covers GET/PUT/DELETE of /admin/pool/models.
func TestAdminPoolModelsAPI(t *testing.T) {
	f := newAdminAPIFixture(t)
	requirePoolSeed(t, f)
	const path = "/api/marketplace/admin/pool/models"

	w := f.do(t, http.MethodGet, path, nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	items, ok := decodeAdminBody(t, w)["data"].(map[string]any)["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, len(modelPoolSeeds))
	first := items[0].(map[string]any)
	assert.Contains(t, first, "capabilities")
	assert.Contains(t, first, "aliases")
	assert.IsType(t, []any{}, first["capabilities"], "arrays are parsed for the console")

	// Create a new model.
	w = f.do(t, http.MethodPut, path, map[string]any{
		"name": "pool-test-model", "origin_vendor": "custom",
		"context_length": 4096, "capabilities": []string{"text"},
		"aliases": []string{"pool-test-alias"},
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	row := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, "pool-test-model", row["name"])
	assert.Equal(t, "custom", row["origin_vendor"])
	assert.EqualValues(t, 4096, row["context_length"])
	assert.Equal(t, []any{"text"}, row["capabilities"])
	assert.Equal(t, []any{"pool-test-alias"}, row["aliases"])

	// Update: omitted fields (capabilities) are preserved, aliases may be
	// provided as a JSON string.
	w = f.do(t, http.MethodPut, path, map[string]any{
		"name": "pool-test-model", "context_length": 8192,
		"aliases": `["renamed-alias"]`, "status": "retired",
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	row = decodeAdminBody(t, w)["data"].(map[string]any)
	assert.EqualValues(t, 8192, row["context_length"])
	assert.Equal(t, "custom", row["origin_vendor"], "omitted origin_vendor is preserved")
	assert.Equal(t, []any{"text"}, row["capabilities"], "omitted capabilities are preserved")
	assert.Equal(t, []any{"renamed-alias"}, row["aliases"], "JSON-string arrays are accepted")
	assert.Equal(t, "retired", row["status"])

	// Validation failures.
	w = f.do(t, http.MethodPut, path, map[string]any{"origin_vendor": "x"}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = f.do(t, http.MethodPut, path, map[string]any{"name": "pool-test-model", "status": "bogus"}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = f.do(t, http.MethodPut, path, map[string]any{
		"name": "pool-test-model", "capabilities": map[string]any{"x": 1},
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// A referenced model cannot be deleted.
	w = f.do(t, http.MethodDelete, path+"?name=glm-5.3", nil, f.adminID)
	assert.Equal(t, http.StatusConflict, w.Code)
	_, ok = ModelPoolByName(f.db, "glm-5.3")
	assert.True(t, ok)

	// Unreferenced model deletion succeeds; a missing name is a 400.
	w = f.do(t, http.MethodDelete, path+"?name=pool-test-model", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	_, ok = ModelPoolByName(f.db, "pool-test-model")
	assert.False(t, ok)
	w = f.do(t, http.MethodDelete, path, nil, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Root-only + auth.
	nonRoot := common.RoleAdminUser
	w = f.doRole(t, http.MethodGet, path, nil, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.doRole(t, http.MethodPut, path, map[string]any{"name": "x"}, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.do(t, http.MethodGet, path, nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAdminTrustPoolCRUDAndGovernance covers the P5 authentication-pool CRUD,
// the governance gate, the append-only audit trail and the P5-B two-person
// confirmation flow in one fixture: they share a single state machine, so
// splitting them would only repeat the seed setup.
func TestAdminTrustPoolCRUDAndGovernance(t *testing.T) {
	f := newAdminAPIFixture(t)
	const path = "/api/marketplace/admin/trust-pool"
	submitter := f.adminID
	reviewer := int(f.buyerID)

	// The seed rows are listed with a parsed view and an expiry (P5 requires
	// every trusted row to carry one).
	w := f.do(t, http.MethodGet, path, nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeAdminBody(t, w)["data"].(map[string]any)
	items, ok := data["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, len(trustPoolSeeds))
	assert.EqualValues(t, len(trustPoolSeeds), data["total"])
	assert.EqualValues(t, 1, data["page"])
	assert.EqualValues(t, 50, data["page_size"])
	first := items[0].(map[string]any)
	assert.Equal(t, "seed", first["source"])
	assert.Equal(t, "official", first["kind"])
	assert.IsType(t, []any{}, first["endpoints"])
	assert.NotNil(t, first["expires_at"], "a seeded trusted row carries an expiry")

	// Filters and pagination.
	w = f.do(t, http.MethodGet, path+"?trust=trusted&kind=official&source=seed", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data = decodeAdminBody(t, w)["data"].(map[string]any)
	assert.EqualValues(t, len(trustPoolSeeds), data["total"])
	for _, query := range []string{
		"?trust=bogus", "?kind=bogus", "?source=bogus", "?page=0", "?page_size=201",
	} {
		w = f.do(t, http.MethodGet, path+query, nil, f.adminID)
		assert.Equal(t, http.StatusBadRequest, w.Code, query)
	}
	w = f.do(t, http.MethodGet, path+"?page=2&page_size=3", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data = decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Len(t, data["items"], 3)
	assert.EqualValues(t, 2, data["page"])

	// Detail of the first seed row.
	seedID := int64(first["id"].(float64))
	w = f.do(t, http.MethodGet, path+"/"+strconv.FormatInt(seedID, 10), nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	seed := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, "api.deepseek.com", seed["host"])
	assert.Equal(t, "seed", seed["source"])

	// Create a manual watch row: it is queued as a pending approval and does
	// not touch mp_trust_pool yet.
	w = f.do(t, http.MethodPost, path, map[string]any{
		"host": "api.manual.example", "kind": "third_party", "trust": "watch",
		"endpoints": []string{"openai_chat"}, "deny_paths": []string{"/admin"},
		"notes": "manual test row",
	}, submitter)
	require.Equal(t, http.StatusOK, w.Code)
	createApproval := decodeAdminBody(t, w)["data"].(map[string]any)
	createApprovalID := int64(createApproval["id"].(float64))
	assert.Equal(t, TrustPoolApprovalPending, createApproval["status"])
	assert.Equal(t, TrustPoolApprovalCreate, createApproval["action"])
	assert.Equal(t, "api.manual.example", createApproval["target"])
	assert.EqualValues(t, submitter, createApproval["submitter_id"])
	assert.EqualValues(t, 0, createApproval["approver_id"])
	createPayload := createApproval["payload"].(map[string]any)
	assert.Nil(t, createPayload["before"], "a create has no before snapshot")
	assert.Equal(t, "api.manual.example", createPayload["after"].(map[string]any)["host"])
	assert.EqualValues(t, len(trustPoolSeeds), countRows(t, f.db, &TrustPool{}), "a pending change is not applied")

	// The pending queue lists it; another status filter does not.
	w = f.do(t, http.MethodGet, path+"/approvals", nil, submitter)
	require.Equal(t, http.StatusOK, w.Code)
	pendingApprovals, ok := decodeAdminBody(t, w)["data"].([]any)
	require.True(t, ok)
	require.Len(t, pendingApprovals, 1)
	assert.EqualValues(t, createApprovalID, pendingApprovals[0].(map[string]any)["id"])
	w = f.do(t, http.MethodGet, path+"/approvals?status=approved", nil, submitter)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeAdminBody(t, w)["data"])
	w = f.do(t, http.MethodGet, path+"/approvals?status=bogus", nil, submitter)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 防自审: the submitter cannot approve their own change.
	w = f.do(t, http.MethodPost, path+"/approvals/"+strconv.FormatInt(createApprovalID, 10)+"/approve", nil, submitter)
	assert.Equal(t, http.StatusForbidden, w.Code)
	var stillPending TrustPoolApproval
	require.NoError(t, f.db.Where("id = ?", createApprovalID).Take(&stillPending).Error)
	assert.Equal(t, TrustPoolApprovalPending, stillPending.Status)

	// A second operator approves; only now is the row created and audited.
	w = f.do(t, http.MethodPost, path+"/approvals/"+strconv.FormatInt(createApprovalID, 10)+"/approve", nil, reviewer)
	require.Equal(t, http.StatusOK, w.Code)
	approvedCreate := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, TrustPoolApprovalApproved, approvedCreate["status"])
	assert.EqualValues(t, reviewer, approvedCreate["approver_id"])
	createdID := int64(approvedCreate["trust_pool_id"].(float64))
	require.NotZero(t, createdID)
	var createdRow TrustPool
	require.NoError(t, f.db.Where("id = ?", createdID).Take(&createdRow).Error)
	assert.Equal(t, "api.manual.example", createdRow.Host)
	assert.Equal(t, "manual", createdRow.Source)

	var audits []TrustPoolAudit
	require.NoError(t, f.db.Where("trust_pool_id = ?", createdID).Order("id ASC").Find(&audits).Error)
	require.Len(t, audits, 1)
	assert.Equal(t, TrustPoolAuditCreate, audits[0].Action)
	assert.EqualValues(t, reviewer, audits[0].ActorID)
	assert.Empty(t, audits[0].Before)
	assert.Contains(t, audits[0].After, "api.manual.example")
	assert.Equal(t, "manual test row", audits[0].Notes)

	// The queue is empty again, and a decided change cannot be decided twice.
	w = f.do(t, http.MethodGet, path+"/approvals", nil, submitter)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeAdminBody(t, w)["data"])
	w = f.do(t, http.MethodPost, path+"/approvals/"+strconv.FormatInt(createApprovalID, 10)+"/approve", nil, reviewer)
	assert.Equal(t, http.StatusConflict, w.Code)

	// Governance and shape validation still runs at submission time.
	for _, body := range []map[string]any{
		{"kind": "third_party", "trust": "watch"},                                              // address required
		{"host": "a.example", "host_pattern": `^a\.`, "kind": "third_party", "trust": "watch"}, // both
		{"host_pattern": "[", "kind": "third_party", "trust": "watch"},                         // invalid regex
		{"host": "api.manual.example", "kind": "bogus", "trust": "watch"},
		{"host": "api.manual.example", "kind": "third_party", "trust": "bogus"},
		{"host": "api.manual.example", "kind": "third_party", "trust": "trusted"}, // expiry required
		{"host": "api.manual.example", "kind": "third_party", "trust": "blocked"}, // notes required
		{"host": "api.manual.example", "kind": "official", "trust": "watch"},      // official is seed-only
		{"host": "api.manual.example", "kind": "third_party", "trust": "watch", "source": "seed"},
		{"host": "api.manual.example", "kind": "third_party", "trust": "watch", "endpoints": []string{"bad"}},
		{"host": "api.manual.example", "kind": "third_party", "trust": "watch", "path_prefix": "/" + strings.Repeat("a", 513)},
	} {
		w = f.do(t, http.MethodPost, path, body, submitter)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%v", body)
	}
	w = f.do(t, http.MethodPost, path, map[string]any{
		"host": "api.expiring.example", "kind": "third_party", "trust": "trusted",
		"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
	}, submitter)
	require.Equal(t, http.StatusOK, w.Code)
	expiringApprovalID := int64(decodeAdminBody(t, w)["data"].(map[string]any)["id"].(float64))
	w = f.do(t, http.MethodPost, path+"/approvals/"+strconv.FormatInt(expiringApprovalID, 10)+"/approve", nil, reviewer)
	require.Equal(t, http.StatusOK, w.Code)
	expiringID := int64(decodeAdminBody(t, w)["data"].(map[string]any)["trust_pool_id"].(float64))

	// An update is queued (tagged change_trust when trust flips), leaves the
	// row untouched, and a rejection leaves it untouched for good.
	w = f.do(t, http.MethodPut, path+"/"+strconv.FormatInt(createdID, 10), map[string]any{
		"host": "api.manual.example", "kind": "third_party", "trust": "blocked",
		"notes": "resold plan account", "deny_paths": []string{"/api/coding"},
	}, submitter)
	require.Equal(t, http.StatusOK, w.Code)
	updateApproval := decodeAdminBody(t, w)["data"].(map[string]any)
	updateApprovalID := int64(updateApproval["id"].(float64))
	assert.Equal(t, TrustPoolApprovalChangeTrust, updateApproval["action"])
	require.NoError(t, f.db.Where("id = ?", createdID).Take(&createdRow).Error)
	assert.Equal(t, TrustWatch, createdRow.Trust, "a pending update is not applied")

	w = f.do(t, http.MethodPost, path+"/approvals/"+strconv.FormatInt(updateApprovalID, 10)+"/reject", nil, submitter)
	assert.Equal(t, http.StatusForbidden, w.Code, "the submitter cannot reject their own change")
	w = f.do(t, http.MethodPost, path+"/approvals/"+strconv.FormatInt(updateApprovalID, 10)+"/reject", nil, reviewer)
	require.Equal(t, http.StatusOK, w.Code)
	rejected := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, TrustPoolApprovalRejected, rejected["status"])
	require.NoError(t, f.db.Where("id = ?", createdID).Take(&createdRow).Error)
	assert.Equal(t, TrustWatch, createdRow.Trust, "a rejected change is not applied")
	w = f.do(t, http.MethodPost, path+"/approvals/"+strconv.FormatInt(updateApprovalID, 10)+"/reject", nil, reviewer)
	assert.Equal(t, http.StatusConflict, w.Code, "only a pending change can be decided")

	// Resubmit and approve the same update.
	w = f.do(t, http.MethodPut, path+"/"+strconv.FormatInt(createdID, 10), map[string]any{
		"host": "api.manual.example", "kind": "third_party", "trust": "blocked",
		"notes": "resold plan account", "deny_paths": []string{"/api/coding"},
	}, submitter)
	require.Equal(t, http.StatusOK, w.Code)
	updateApprovalID = int64(decodeAdminBody(t, w)["data"].(map[string]any)["id"].(float64))
	w = f.do(t, http.MethodPost, path+"/approvals/"+strconv.FormatInt(updateApprovalID, 10)+"/approve", nil, reviewer)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, f.db.Where("id = ?", createdID).Take(&createdRow).Error)
	assert.Equal(t, TrustBlocked, createdRow.Trust)
	assert.Equal(t, "manual", createdRow.Source)
	require.NoError(t, f.db.Where("trust_pool_id = ?", createdID).Order("id ASC").Find(&audits).Error)
	require.Len(t, audits, 2)
	assert.Equal(t, TrustPoolAuditUpdate, audits[1].Action)
	assert.EqualValues(t, reviewer, audits[1].ActorID)
	assert.Contains(t, audits[1].Before, `"trust":"watch"`)
	assert.Contains(t, audits[1].After, `"trust":"blocked"`)
	assert.Equal(t, "resold plan account", audits[1].Notes)

	// A manual row cannot become official, and source is immutable.
	w = f.do(t, http.MethodPut, path+"/"+strconv.FormatInt(createdID, 10), map[string]any{
		"host": "api.manual.example", "kind": "official", "trust": "watch",
	}, submitter)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = f.do(t, http.MethodPut, path+"/"+strconv.FormatInt(createdID, 10), map[string]any{
		"host": "api.manual.example", "kind": "third_party", "trust": "watch", "source": "probe",
	}, submitter)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// A seeded official row stays editable (source stays seed).
	w = f.do(t, http.MethodPut, path+"/"+strconv.FormatInt(seedID, 10), map[string]any{
		"host": "api.deepseek.com", "kind": "official", "trust": "trusted",
		"endpoints":  []string{"openai_chat", "anthropic_messages"},
		"expires_at": time.Now().Add(24 * time.Hour).Format(time.RFC3339),
		"notes":      "reviewed",
	}, submitter)
	require.Equal(t, http.StatusOK, w.Code)
	seedApprovalID := int64(decodeAdminBody(t, w)["data"].(map[string]any)["id"].(float64))
	w = f.do(t, http.MethodPost, path+"/approvals/"+strconv.FormatInt(seedApprovalID, 10)+"/approve", nil, reviewer)
	require.Equal(t, http.StatusOK, w.Code)
	var seedUpdated TrustPool
	require.NoError(t, f.db.Where("id = ?", seedID).Take(&seedUpdated).Error)
	assert.Equal(t, "seed", seedUpdated.Source)
	assert.Equal(t, "official", seedUpdated.Kind)
	assert.Equal(t, "reviewed", seedUpdated.Notes)

	// A delete is queued too; the row survives until the second operator
	// approves, then the hard delete and its audit row commit together.
	w = f.do(t, http.MethodDelete, path+"/"+strconv.FormatInt(expiringID, 10), nil, submitter)
	require.Equal(t, http.StatusOK, w.Code)
	deleteApproval := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, TrustPoolApprovalDelete, deleteApproval["action"])
	deleteApprovalID := int64(deleteApproval["id"].(float64))
	assert.EqualValues(t, len(trustPoolSeeds)+2, countRows(t, f.db, &TrustPool{}), "the pending delete has not been applied")
	w = f.do(t, http.MethodPost, path+"/approvals/"+strconv.FormatInt(deleteApprovalID, 10)+"/approve", nil, reviewer)
	require.Equal(t, http.StatusOK, w.Code)
	var gone int64
	require.NoError(t, f.db.Model(&TrustPool{}).Where("id = ?", expiringID).Count(&gone).Error)
	assert.Zero(t, gone)
	var deleteAudits []TrustPoolAudit
	require.NoError(t, f.db.Where("trust_pool_id = ? AND action = ?", expiringID, TrustPoolAuditDelete).Find(&deleteAudits).Error)
	require.Len(t, deleteAudits, 1)
	assert.Contains(t, deleteAudits[0].Before, "api.expiring.example")
	assert.Empty(t, deleteAudits[0].After)

	// Seed rows cannot be deleted even through the approval queue.
	w = f.do(t, http.MethodDelete, path+"/"+strconv.FormatInt(seedID, 10), nil, submitter)
	assert.Equal(t, http.StatusConflict, w.Code)
	w = f.do(t, http.MethodDelete, path+"/999999", nil, submitter)
	assert.Equal(t, http.StatusNotFound, w.Code)
	w = f.do(t, http.MethodGet, path+"/999999", nil, submitter)
	assert.Equal(t, http.StatusNotFound, w.Code)
	w = f.do(t, http.MethodPost, path+"/approvals/999999/approve", nil, reviewer)
	assert.Equal(t, http.StatusNotFound, w.Code)
	w = f.do(t, http.MethodPost, path+"/approvals/999999/reject", nil, reviewer)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Auth: missing session is 401; non-root is 403.
	w = f.do(t, http.MethodGet, path, nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	nonRoot := common.RoleAdminUser
	w = f.doRole(t, http.MethodGet, path, nil, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.doRole(t, http.MethodPost, path, map[string]any{"host": "x.example"}, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.doRole(t, http.MethodGet, path+"/approvals", nil, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.doRole(t, http.MethodPost, path+"/approvals/1/approve", nil, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// P5-C seam: a delegated operator granted only trust_pool:list reaches the
	// list endpoint without gaining another trust-pool action.
	trustPoolTestGrants[f.adminID] = map[string]bool{authz.ActionList: true}
	t.Cleanup(func() { delete(trustPoolTestGrants, f.adminID) })
	w = f.doRole(t, http.MethodGet, path, nil, f.adminID, nonRoot)
	assert.Equal(t, http.StatusOK, w.Code)
	w = f.doRole(t, http.MethodPost, path, map[string]any{"host": "grant.example"}, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code, "a list-only grant must not allow create")
}

// TestAdminOpsMetricsList covers the P5 derived-metrics read API: filters by
// metric_name, since and dimensions, plus the root-only gate.
func TestAdminOpsMetricsList(t *testing.T) {
	f := newAdminAPIFixture(t)
	now := time.Now()
	rows := []OpsMetric{
		{MetricName: MetricStreamCutRate, MetricValue: 0.25, Dimensions: `{"total":4,"hits":1}`, BucketTime: now.Add(-2 * time.Hour), CreatedAt: now},
		{MetricName: Metric503Reason, MetricValue: 2, Dimensions: `{"reason":"plan_quota_exhausted"}`, BucketTime: now.Add(-3 * time.Hour), CreatedAt: now},
		{MetricName: MetricPreFreezeDeviation, MetricValue: 1000, Dimensions: `{"capped_requests":1,"total":4}`, BucketTime: now.Add(-time.Hour), CreatedAt: now},
	}
	require.NoError(t, f.db.Create(&rows).Error)

	path := "/api/marketplace/admin/metrics"
	since := url.QueryEscape(now.Add(-24 * time.Hour).Format(time.RFC3339))

	w := f.do(t, http.MethodGet, path+"?since="+since, nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	items, ok := decodeAdminBody(t, w)["data"].([]any)
	require.True(t, ok)
	require.Len(t, items, 3)
	first := items[0].(map[string]any)
	assert.Equal(t, MetricPreFreezeDeviation, first["metric_name"], "newest bucket first")
	assert.EqualValues(t, 1000, first["metric_value"])
	assert.NotEmpty(t, first["bucket_time"])
	dimensions, ok := first["dimensions"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 1, dimensions["capped_requests"])

	// metric_name narrows the list.
	w = f.do(t, http.MethodGet, path+"?since="+since+"&metric_name="+MetricStreamCutRate, nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	items = decodeAdminBody(t, w)["data"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, MetricStreamCutRate, items[0].(map[string]any)["metric_name"])
	assert.InDelta(t, 0.25, items[0].(map[string]any)["metric_value"], 1e-9)

	// dimensions filters by exact key/value pairs.
	dimFilter := url.QueryEscape(`{"reason":"plan_quota_exhausted"}`)
	w = f.do(t, http.MethodGet, path+"?since="+since+"&dimensions="+dimFilter, nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	items = decodeAdminBody(t, w)["data"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, Metric503Reason, items[0].(map[string]any)["metric_name"])

	// Invalid inputs are 400; the root gate rejects missing and non-root
	// sessions.
	for _, query := range []string{
		"?since=yesterday",
		"?dimensions=not-json",
		"?dimensions={}",
		"?limit=0",
		"?limit=5000",
	} {
		w = f.do(t, http.MethodGet, path+query, nil, f.adminID)
		assert.Equal(t, http.StatusBadRequest, w.Code, query)
	}
	w = f.do(t, http.MethodGet, path, nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	w = f.doRole(t, http.MethodGet, path, nil, f.adminID, common.RoleAdminUser)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestAdminActiveSessionsAndForceRelease covers the P5-C operator escape hatch:
// list the active freezes of a listing, refund them, drop the slots, and prove
// that a later settle cannot move quota a second time.
func TestAdminActiveSessionsAndForceRelease(t *testing.T) {
	f := newAdminAPIFixture(t)
	const listingID = int64(987654321)

	oldRegistry := defaultSessionRegistry
	defaultSessionRegistry = NewSessionRegistry()
	t.Cleanup(func() { defaultSessionRegistry = oldRegistry })

	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.buyerID).Update("quota", 5_000_000).Error)
	session, err := NewBillingSession(f.db, f.buyerID, 2_000_000)
	require.NoError(t, err)
	DefaultSessionRegistry().Register(SessionEntry{
		RequestID:   "req-force-1",
		ListingID:   listingID,
		BuyerID:     int(f.buyerID),
		FrozenMicro: int64(session.GetPreConsumedQuota()),
		billing:     session,
	})
	now := time.Now()
	require.True(t, DefaultSelector().Slots.Acquire(listingID, 5, now))
	require.True(t, DefaultSelector().Slots.Acquire(listingID, 5, now))

	w := f.do(t, http.MethodGet, "/api/marketplace/admin/sessions?listing_id="+strconv.FormatInt(listingID, 10), nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.EqualValues(t, 1, data["total"])
	assert.EqualValues(t, 2_000_000, data["frozen_micro"])
	items := data["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, "req-force-1", items[0].(map[string]any)["request_id"])

	w = f.do(t, http.MethodGet, "/api/marketplace/admin/sessions?listing_id=0", nil, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/sessions/force-release", map[string]any{}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = f.do(t, http.MethodPost, "/api/marketplace/admin/sessions/force-release", map[string]any{"listing_id": listingID}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	result := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.EqualValues(t, 1, result["sessions"])
	assert.EqualValues(t, 2_000_000, result["frozen_micro"])
	assert.EqualValues(t, 2, result["slots_released"])
	assert.EqualValues(t, 0, result["refund_failures"])

	var quota int64
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.buyerID).Select("quota").Scan(&quota).Error)
	assert.Equal(t, int64(5_000_000), quota, "the force release restores the full freeze")
	assert.False(t, session.NeedsRefund())
	assert.Empty(t, DefaultSessionRegistry().List(listingID))
	assert.Zero(t, DefaultSelector().Slots.InFlight(listingID))

	// A settle that races the forced release must not refund or charge again.
	require.NoError(t, session.Settle(1_500_000))
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.buyerID).Select("quota").Scan(&quota).Error)
	assert.Equal(t, int64(5_000_000), quota)

	w = f.do(t, http.MethodPost, "/api/marketplace/admin/sessions/force-release", map[string]any{"listing_id": listingID}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.EqualValues(t, 0, decodeAdminBody(t, w)["data"].(map[string]any)["sessions"])
}
