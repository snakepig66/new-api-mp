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
	registerAdminRootRoutes(engine.Group("/api/marketplace", headerAdminAuth()))

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

	// POST a new version row for deepseek-flash/offpeak.
	w := f.do(t, http.MethodPost, "/api/marketplace/admin/prices", map[string]any{
		"model": "deepseek-flash", "vendor": "deepseek", "tier": "offpeak",
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
	w = f.do(t, http.MethodGet, "/api/marketplace/admin/prices?model=deepseek-flash&vendor=deepseek&tier=offpeak", nil, f.adminID)
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
		"model": "deepseek-flash", "vendor": "deepseek", "tier": "offpeak",
		"input_micro_per_mtok": 9_000_000, "output_micro_per_mtok": 9_000_000,
		"cache_read_micro_per_mtok": 9_000_000, "cache_write_micro_per_mtok": 9_000_000,
		"version": 2,
	}, f.adminID)
	assert.Equal(t, http.StatusConflict, w.Code)

	// The original version-1 row is untouched.
	var v1 PriceBook
	require.NoError(t, f.db.Where("model = ? AND vendor = ? AND tier = ? AND version = ?",
		"deepseek-flash", "deepseek", "offpeak", 1).Take(&v1).Error)
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
		"model": "deepseek-flash", "vendor": "deepseek", "tier": "offpeak",
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
	assert.Equal(t, http.StatusBadRequest, w.Code, "tier must be peak/offpeak/flat")

	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	w = f.do(t, http.MethodPost, "/api/marketplace/admin/prices", map[string]any{
		"model": "deepseek-flash", "vendor": "deepseek", "tier": "offpeak",
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
	require.Equal(t, TierPeak, cal.TierAt(at), "before import the weekday is peak")

	// next_year_imported starts false (seed contains no next-year rows).
	w := f.do(t, http.MethodGet, "/api/marketplace/admin/holidays", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, false, data["next_year_imported"])

	w = f.do(t, http.MethodPost, "/api/marketplace/admin/holidays", []map[string]any{
		{"date": date, "kind": "holiday"},
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)

	// No waiting for the hourly job: the same instant is off-peak now.
	assert.Equal(t, TierOffpeak, cal.TierAt(at), "import must reload the calendar immediately")

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

// TestAdminChannelModelsAPI covers GET/PUT/DELETE of the channel-model mapping
// table, its (vendor, model) ordering, conflict handling and the root-only
// gate.
func TestAdminChannelModelsAPI(t *testing.T) {
	f := newAdminAPIFixture(t)
	// The regular marketplace migration does not own this table (it is wired by
	// the channel-map module), so create and seed it explicitly.
	require.NoError(t, MigrateChannelModelMap(f.db))
	require.NoError(t, SeedChannelModelMap(f.db))

	const path = "/api/marketplace/admin/channel-models"

	// GET lists the seed, ordered by (vendor, model).
	w := f.do(t, http.MethodGet, path, nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data, ok := decodeAdminBody(t, w)["data"].(map[string]any)
	require.True(t, ok)
	items, ok := data["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, len(channelModelSeeds))
	prevVendor, prevModel := "", ""
	for i, item := range items {
		row, ok := item.(map[string]any)
		require.True(t, ok)
		vendor, _ := row["vendor"].(string)
		model, _ := row["model"].(string)
		require.NotEmpty(t, vendor)
		require.NotEmpty(t, model)
		assert.Contains(t, row, "upstream_model")
		assert.Contains(t, row, "id")
		if i > 0 {
			assert.True(t, vendor > prevVendor || (vendor == prevVendor && model >= prevModel),
				"items must be ordered by (vendor, model): %s/%s after %s/%s",
				vendor, model, prevVendor, prevModel)
		}
		prevVendor, prevModel = vendor, model
	}

	// PUT creates a new mapping and returns the stored row.
	w = f.do(t, http.MethodPut, path, map[string]any{
		"vendor": "test-vendor", "model": "std-a", "upstream_model": "up-a",
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	row, ok := decodeAdminBody(t, w)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test-vendor", row["vendor"])
	assert.Equal(t, "std-a", row["model"])
	assert.Equal(t, "up-a", row["upstream_model"])
	assert.NotZero(t, row["id"])

	upstream, ok := ChannelModelResolve(f.db, "test-vendor", "std-a")
	require.True(t, ok)
	assert.Equal(t, "up-a", upstream)

	// PUT for the same (vendor, model) updates upstream_model in place.
	w = f.do(t, http.MethodPut, path, map[string]any{
		"vendor": "test-vendor", "model": "std-a", "upstream_model": "up-b",
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	row, ok = decodeAdminBody(t, w)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "up-b", row["upstream_model"])
	var cnt int64
	require.NoError(t, f.db.Model(&ChannelModelMap{}).
		Where("vendor = ? AND model = ?", "test-vendor", "std-a").Count(&cnt).Error)
	assert.EqualValues(t, 1, cnt, "update must not insert a duplicate row")

	// A second model in the same vendor may not reuse the upstream → 409.
	w = f.do(t, http.MethodPut, path, map[string]any{
		"vendor": "test-vendor", "model": "std-b", "upstream_model": "up-b",
	}, f.adminID)
	assert.Equal(t, http.StatusConflict, w.Code)
	_, ok = ChannelModelResolve(f.db, "test-vendor", "std-b")
	assert.False(t, ok)

	// Blank fields → 400.
	w = f.do(t, http.MethodPut, path, map[string]any{
		"vendor": "  ", "model": "std-b", "upstream_model": "up-x",
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Non-root administrators are rejected (defense in depth behind RootAuth),
	// and the rejected DELETE leaves the row untouched.
	nonRoot := common.RoleAdminUser
	w = f.doRole(t, http.MethodGet, path, nil, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.doRole(t, http.MethodPut, path, map[string]any{
		"vendor": "test-vendor", "model": "std-a", "upstream_model": "up-x",
	}, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.doRole(t, http.MethodDelete, path+"?vendor=test-vendor&model=std-a", nil, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	upstream, ok = ChannelModelResolve(f.db, "test-vendor", "std-a")
	require.True(t, ok)
	assert.Equal(t, "up-b", upstream, "forbidden requests must not modify the table")

	// No session → 401 from the auth middleware stand-in.
	w = f.do(t, http.MethodGet, path, nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// DELETE removes the mapping; a repeated DELETE is still 200.
	w = f.do(t, http.MethodDelete, path+"?vendor=test-vendor&model=std-a", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	_, ok = ChannelModelResolve(f.db, "test-vendor", "std-a")
	assert.False(t, ok)
	w = f.do(t, http.MethodDelete, path+"?vendor=test-vendor&model=std-a", nil, f.adminID)
	assert.Equal(t, http.StatusOK, w.Code)

	// Missing query parameters → 400.
	w = f.do(t, http.MethodDelete, path, nil, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = f.do(t, http.MethodDelete, path+"?vendor=test-vendor", nil, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

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

// TestAdminPoolChannelsAPI covers GET/PUT/DELETE of /admin/pool/channels.
func TestAdminPoolChannelsAPI(t *testing.T) {
	f := newAdminAPIFixture(t)
	requirePoolSeed(t, f)
	const path = "/api/marketplace/admin/pool/channels"

	w := f.do(t, http.MethodGet, path, nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	items, ok := decodeAdminBody(t, w)["data"].(map[string]any)["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, len(channelPoolSeeds))

	// Create with an object protocols/quota_window and JSON-string deny lists.
	w = f.do(t, http.MethodPut, path, map[string]any{
		"slug": "pool-test-channel", "name": "Pool Test", "kind": "third_party",
		"protocols":        map[string]any{"openai": "https://example.com/v1", "anthropic": "https://example.com/anthropic"},
		"deny_paths":       `["/a","/b"]`,
		"key_pattern":      "^sk-",
		"strip_v1_prefix":  true,
		"passthrough_body": true,
		"quota_window":     map[string]any{"period_seconds": 3600},
		"status":           "active",
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	row := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, "pool-test-channel", row["slug"])
	protocols := row["protocols"].(map[string]any)
	assert.Equal(t, "https://example.com/v1", protocols["openai"])
	assert.Equal(t, []any{"/a", "/b"}, row["deny_paths"])
	assert.Equal(t, true, row["strip_v1_prefix"])
	assert.EqualValues(t, 3600, row["quota_window"].(map[string]any)["period_seconds"])

	// Update in place; protocols may be a JSON string.
	w = f.do(t, http.MethodPut, path, map[string]any{
		"slug": "pool-test-channel", "name": "Pool Test 2", "kind": "official",
		"protocols": `{"openai":"https://example.org/v1"}`,
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	row = decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, "official", row["kind"])
	assert.Equal(t, "https://example.org/v1", row["protocols"].(map[string]any)["openai"])
	var cnt int64
	require.NoError(t, f.db.Model(&ChannelPool{}).Where("slug = ?", "pool-test-channel").Count(&cnt).Error)
	assert.EqualValues(t, 1, cnt)

	// Validation failures.
	w = f.do(t, http.MethodPut, path, map[string]any{"slug": "x", "kind": "bogus"}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = f.do(t, http.MethodPut, path, map[string]any{"slug": "x", "kind": "official", "key_pattern": "["}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = f.do(t, http.MethodPut, path, map[string]any{
		"slug": "x", "kind": "official", "deny_paths": map[string]any{"a": 1},
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = f.do(t, http.MethodPut, path, map[string]any{"name": "no slug", "kind": "official"}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// A channel referenced by a listing cannot be deleted.
	_ = f.addAdminListing(t, ListingLive) // vendor deepseek
	w = f.do(t, http.MethodDelete, path+"?slug=deepseek", nil, f.adminID)
	assert.Equal(t, http.StatusConflict, w.Code)
	_, ok = ChannelPoolBySlug(f.db, "deepseek")
	assert.True(t, ok)

	// Unreferenced channel deletion succeeds.
	w = f.do(t, http.MethodDelete, path+"?slug=pool-test-channel", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	_, ok = ChannelPoolBySlug(f.db, "pool-test-channel")
	assert.False(t, ok)

	w = f.do(t, http.MethodDelete, path, nil, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Root-only + auth.
	nonRoot := common.RoleAdminUser
	w = f.doRole(t, http.MethodPut, path, map[string]any{"slug": "x", "kind": "official"}, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.doRole(t, http.MethodGet, path, nil, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.do(t, http.MethodGet, path, nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAdminPoolChannelModelsAPI covers GET/PUT/DELETE of
// /admin/pool/channel-models.
func TestAdminPoolChannelModelsAPI(t *testing.T) {
	f := newAdminAPIFixture(t)
	requirePoolSeed(t, f)
	const path = "/api/marketplace/admin/pool/channel-models"

	w := f.do(t, http.MethodGet, path, nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	items, ok := decodeAdminBody(t, w)["data"].(map[string]any)["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, len(poolChannelModelSeeds))

	// Update an existing qualification; omitted verified/differences survive.
	w = f.do(t, http.MethodPut, path, map[string]any{
		"channel_slug": "deepseek", "upstream_model": "deepseek-flash",
		"model": "deepseek-flash", "coefficient_milli": 900,
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	row := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.EqualValues(t, 900, row["coefficient_milli"])
	assert.Equal(t, true, row["verified"], "omitted verified is preserved")

	// Both conflict directions.
	w = f.do(t, http.MethodPut, path, map[string]any{
		"channel_slug": "deepseek", "upstream_model": "deepseek-flash",
		"model": "glm-5.3", "coefficient_milli": 1000,
	}, f.adminID)
	assert.Equal(t, http.StatusConflict, w.Code)
	w = f.do(t, http.MethodPut, path, map[string]any{
		"channel_slug": "deepseek", "upstream_model": "deepseek-flash-v2",
		"model": "deepseek-flash", "coefficient_milli": 1000,
	}, f.adminID)
	assert.Equal(t, http.StatusConflict, w.Code)

	// Validation failures.
	for _, coefficient := range []int64{0, 10001} {
		w = f.do(t, http.MethodPut, path, map[string]any{
			"channel_slug": "deepseek", "upstream_model": "glm-5.3-preview",
			"model": "glm-5.3", "coefficient_milli": coefficient,
		}, f.adminID)
		assert.Equal(t, http.StatusBadRequest, w.Code, "coefficient %d", coefficient)
	}
	w = f.do(t, http.MethodPut, path, map[string]any{
		"channel_slug": "no-such", "upstream_model": "up", "model": "glm-5.3",
		"coefficient_milli": 1000,
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = f.do(t, http.MethodPut, path, map[string]any{
		"channel_slug": "deepseek", "upstream_model": "glm-5.3-preview", "model": "no-such-model",
		"coefficient_milli": 1000,
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = f.do(t, http.MethodPut, path, map[string]any{
		"channel_slug": "deepseek", "upstream_model": "glm-5.3-preview", "model": "glm-5.3",
		"coefficient_milli": 1000, "differences": []string{"not", "an", "object"},
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Valid create, then delete.
	w = f.do(t, http.MethodPut, path, map[string]any{
		"channel_slug": "deepseek", "upstream_model": "glm-5.3-preview", "model": "glm-5.3",
		"coefficient_milli": 1000, "verified": true, "differences": map[string]any{"context_length": 64000},
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	row = decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, true, row["verified"])
	assert.EqualValues(t, 64000, row["differences"].(map[string]any)["context_length"])
	require.EqualValues(t, len(poolChannelModelSeeds)+1, countRows(t, f.db, &ChannelModel{}))

	w = f.do(t, http.MethodDelete, path+"?channel_slug=deepseek&upstream_model=glm-5.3-preview", nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	_, ok = channelModelByUpstream(f.db, "deepseek", "glm-5.3-preview")
	assert.False(t, ok)
	// Repeating the delete is fine (idempotent).
	w = f.do(t, http.MethodDelete, path+"?channel_slug=deepseek&upstream_model=glm-5.3-preview", nil, f.adminID)
	assert.Equal(t, http.StatusOK, w.Code)
	w = f.do(t, http.MethodDelete, path+"?channel_slug=deepseek", nil, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Root-only + auth.
	nonRoot := common.RoleAdminUser
	w = f.doRole(t, http.MethodPut, path, map[string]any{
		"channel_slug": "deepseek", "upstream_model": "deepseek-flash",
		"model": "deepseek-flash", "coefficient_milli": 1000,
	}, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.doRole(t, http.MethodDelete, path+"?channel_slug=deepseek&upstream_model=deepseek-flash", nil, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.do(t, http.MethodGet, path, nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAdminTrustPoolCRUDAndGovernance covers the P5 authentication-pool CRUD,
// the governance gate and the append-only audit trail in one fixture: the
// routes share a single state machine, so splitting them would only repeat the
// seed setup.
func TestAdminTrustPoolCRUDAndGovernance(t *testing.T) {
	f := newAdminAPIFixture(t)
	const path = "/api/marketplace/admin/trust-pool"

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

	// Create a manual watch row; source defaults to manual.
	w = f.do(t, http.MethodPost, path, map[string]any{
		"host": "api.manual.example", "kind": "third_party", "trust": "watch",
		"endpoints": []string{"openai_chat"}, "deny_paths": []string{"/admin"},
		"notes": "manual test row",
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	created := decodeAdminBody(t, w)["data"].(map[string]any)
	createdID := int64(created["id"].(float64))
	assert.Equal(t, "manual", created["source"])
	assert.Equal(t, "api.manual.example", created["host"])
	assert.Equal(t, []any{"openai_chat"}, created["endpoints"])
	assert.Equal(t, []any{"/admin"}, created["deny_paths"])

	// The audit row was written in the same transaction.
	var audits []TrustPoolAudit
	require.NoError(t, f.db.Where("trust_pool_id = ?", createdID).Order("id ASC").Find(&audits).Error)
	require.Len(t, audits, 1)
	assert.Equal(t, TrustPoolAuditCreate, audits[0].Action)
	assert.EqualValues(t, f.adminID, audits[0].ActorID)
	assert.Empty(t, audits[0].Before)
	assert.Contains(t, audits[0].After, "api.manual.example")
	assert.Equal(t, "manual test row", audits[0].Notes)

	// Governance and shape validation.
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
		w = f.do(t, http.MethodPost, path, body, f.adminID)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%v", body)
	}
	w = f.do(t, http.MethodPost, path, map[string]any{
		"host": "api.expiring.example", "kind": "third_party", "trust": "trusted",
		"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	expiringID := int64(decodeAdminBody(t, w)["data"].(map[string]any)["id"].(float64))

	// Update keeps the source and records the before/after snapshots.
	w = f.do(t, http.MethodPut, path+"/"+strconv.FormatInt(createdID, 10), map[string]any{
		"host": "api.manual.example", "kind": "third_party", "trust": "blocked",
		"notes": "resold plan account", "deny_paths": []string{"/api/coding"},
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	updated := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, "blocked", updated["trust"])
	assert.Equal(t, "manual", updated["source"])
	require.NoError(t, f.db.Where("trust_pool_id = ?", createdID).Order("id ASC").Find(&audits).Error)
	require.Len(t, audits, 2)
	assert.Equal(t, TrustPoolAuditUpdate, audits[1].Action)
	assert.Contains(t, audits[1].Before, `"trust":"watch"`)
	assert.Contains(t, audits[1].After, `"trust":"blocked"`)
	assert.Equal(t, "resold plan account", audits[1].Notes)

	// A manual row cannot become official, and source is immutable.
	w = f.do(t, http.MethodPut, path+"/"+strconv.FormatInt(createdID, 10), map[string]any{
		"host": "api.manual.example", "kind": "official", "trust": "watch",
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = f.do(t, http.MethodPut, path+"/"+strconv.FormatInt(createdID, 10), map[string]any{
		"host": "api.manual.example", "kind": "third_party", "trust": "watch", "source": "probe",
	}, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// A seeded official row stays editable (source stays seed).
	w = f.do(t, http.MethodPut, path+"/"+strconv.FormatInt(seedID, 10), map[string]any{
		"host": "api.deepseek.com", "kind": "official", "trust": "trusted",
		"endpoints":  []string{"openai_chat", "anthropic_messages"},
		"expires_at": time.Now().Add(24 * time.Hour).Format(time.RFC3339),
		"notes":      "reviewed",
	}, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	seedUpdated := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.Equal(t, "seed", seedUpdated["source"])
	assert.Equal(t, "official", seedUpdated["kind"])
	assert.Equal(t, "reviewed", seedUpdated["notes"])

	// Hard delete keeps the audit trail; seeded rows are code-owned (409).
	w = f.do(t, http.MethodDelete, path+"/"+strconv.FormatInt(expiringID, 10), nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	assert.EqualValues(t, len(trustPoolSeeds)+1, countRows(t, f.db, &TrustPool{}), "the seed rows and the manual row remain")
	var deleteAudits []TrustPoolAudit
	require.NoError(t, f.db.Where("trust_pool_id = ? AND action = ?", expiringID, TrustPoolAuditDelete).Find(&deleteAudits).Error)
	require.Len(t, deleteAudits, 1)
	assert.Contains(t, deleteAudits[0].Before, "api.expiring.example")
	assert.Empty(t, deleteAudits[0].After)

	w = f.do(t, http.MethodDelete, path+"/"+strconv.FormatInt(seedID, 10), nil, f.adminID)
	assert.Equal(t, http.StatusConflict, w.Code)
	w = f.do(t, http.MethodDelete, path+"/999999", nil, f.adminID)
	assert.Equal(t, http.StatusNotFound, w.Code)
	w = f.do(t, http.MethodGet, path+"/999999", nil, f.adminID)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Auth: missing session is 401; non-root is 403.
	w = f.do(t, http.MethodGet, path, nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	nonRoot := common.RoleAdminUser
	w = f.doRole(t, http.MethodGet, path, nil, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = f.doRole(t, http.MethodPost, path, map[string]any{"host": "x.example"}, f.adminID, nonRoot)
	assert.Equal(t, http.StatusForbidden, w.Code)
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
