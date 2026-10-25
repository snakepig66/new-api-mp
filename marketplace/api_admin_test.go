package marketplace

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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
