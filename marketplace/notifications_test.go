package marketplace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/model"
)

// notificationFixture serves the notification routes behind a simulated
// login (headerUserAuth from api_key_test.go).
type notificationFixture struct {
	db      *gorm.DB
	engine  *gin.Engine
	userID  int64
	otherID int64
}

func newNotificationFixture(t *testing.T) *notificationFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}))

	relayStateMu.Lock()
	oldRelayDB := relayDB
	relayDB = db
	relayStateMu.Unlock()
	t.Cleanup(func() {
		relayStateMu.Lock()
		relayDB = oldRelayDB
		relayStateMu.Unlock()
	})

	engine := gin.New()
	RegisterNotificationRoutes(engine.Group("/api/marketplace", headerUserAuth()))

	return &notificationFixture{
		db:      db,
		engine:  engine,
		userID:  insertTestUser(t, db, "notif-user"),
		otherID: insertTestUser(t, db, "notif-other"),
	}
}

// do issues one request as userID; 0 means no login header.
func (f *notificationFixture) do(t *testing.T, method, path string, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	if userID > 0 {
		req.Header.Set("X-Test-User-Id", strconv.FormatInt(userID, 10))
	}
	f.engine.ServeHTTP(w, req)
	return w
}

type notificationEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

func decodeNotificationData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var env notificationEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env), w.Body.String())
	require.True(t, env.Success)
	var out T
	require.NoError(t, json.Unmarshal(env.Data, &out), string(env.Data))
	return out
}

func seedNotification(t *testing.T, db *gorm.DB, userID int64, kind, title string) Notification {
	t.Helper()
	require.NoError(t, CreateNotification(db, userID, kind, title, "body-"+title, "/console"))
	var row Notification
	require.NoError(t, db.Where("user_id = ? AND title = ?", userID, title).Take(&row).Error)
	return row
}

func TestNotificationPaginationAndUnread(t *testing.T) {
	f := newNotificationFixture(t)
	for i := 0; i < 25; i++ {
		seedNotification(t, f.db, f.userID, NotificationKindSystem, fmt.Sprintf("n-%02d", i))
	}

	// Unauthenticated requests are rejected.
	assert.Equal(t, http.StatusUnauthorized, f.do(t, http.MethodGet, "/api/marketplace/notifications", 0).Code)

	page := decodeNotificationData[notificationPage](t,
		f.do(t, http.MethodGet, "/api/marketplace/notifications?page=2&page_size=10", f.userID))
	assert.Equal(t, 2, page.Page)
	assert.Equal(t, 10, page.PageSize)
	assert.Equal(t, 25, page.Total)
	assert.Equal(t, int64(25), page.UnreadCount)
	require.Len(t, page.Items, 10)
	// Newest first; ids 25..16 on page 1, so page 2 starts at 15.
	assert.Equal(t, int64(15), page.Items[0].ID)
	assert.Equal(t, int64(6), page.Items[9].ID)
	assert.False(t, page.Items[0].Read)
	assert.Nil(t, page.Items[0].ReadAt)

	// Defaults: page 1, page_size 20.
	first := decodeNotificationData[notificationPage](t,
		f.do(t, http.MethodGet, "/api/marketplace/notifications", f.userID))
	assert.Equal(t, 1, first.Page)
	assert.Equal(t, 20, first.PageSize)
	assert.Len(t, first.Items, 20)

	// Another user sees an empty page, not user A's rows.
	other := decodeNotificationData[notificationPage](t,
		f.do(t, http.MethodGet, "/api/marketplace/notifications", f.otherID))
	assert.Equal(t, 0, other.Total)
	assert.Zero(t, other.UnreadCount)
	assert.NotNil(t, other.Items)
	assert.Len(t, other.Items, 0)
}

func TestNotificationReadIsolation(t *testing.T) {
	f := newNotificationFixture(t)
	a1 := seedNotification(t, f.db, f.userID, NotificationKindReview, "a-1")
	a2 := seedNotification(t, f.db, f.userID, NotificationKindTopup, "a-2")
	b1 := seedNotification(t, f.db, f.otherID, NotificationKindWithdrawal, "b-1")

	// User A cannot mark user B's row read, and the row stays unread.
	cross := f.do(t, http.MethodPost, "/api/marketplace/notifications/"+strconv.FormatInt(b1.ID, 10)+"/read", f.userID)
	assert.Equal(t, http.StatusNotFound, cross.Code)
	var reloaded Notification
	require.NoError(t, f.db.Where("id = ?", b1.ID).Take(&reloaded).Error)
	assert.Nil(t, reloaded.ReadAt, "cross-user mark must not touch the row")

	// Marking one's own row works and is idempotent.
	w := f.do(t, http.MethodPost, "/api/marketplace/notifications/"+strconv.FormatInt(a1.ID, 10)+"/read", f.userID)
	item := decodeNotificationData[notificationItem](t, w)
	assert.Equal(t, a1.ID, item.ID)
	assert.True(t, item.Read)
	require.NotNil(t, item.ReadAt)
	reloaded = Notification{}
	require.NoError(t, f.db.Where("id = ?", a1.ID).Take(&reloaded).Error)
	require.NotNil(t, reloaded.ReadAt)
	firstReadAt := *reloaded.ReadAt
	assert.Equal(t, http.StatusOK, f.do(t, http.MethodPost,
		"/api/marketplace/notifications/"+strconv.FormatInt(a1.ID, 10)+"/read", f.userID).Code)
	reloaded = Notification{}
	require.NoError(t, f.db.Where("id = ?", a1.ID).Take(&reloaded).Error)
	require.NotNil(t, reloaded.ReadAt)
	assert.WithinDuration(t, firstReadAt, *reloaded.ReadAt, 0, "already-read rows keep their timestamp")

	// A's remaining unread is 1.
	aPage := decodeNotificationData[notificationPage](t,
		f.do(t, http.MethodGet, "/api/marketplace/notifications", f.userID))
	assert.Equal(t, int64(1), aPage.UnreadCount)
	assert.Equal(t, 2, aPage.Total)
	require.Len(t, aPage.Items, 2)
	assert.Equal(t, a2.ID, aPage.Items[0].ID, "newest first")
	assert.False(t, aPage.Items[0].Read)
	assert.True(t, aPage.Items[1].Read)

	// read-all is user-scoped: B marks only B's row.
	res := decodeNotificationData[map[string]int64](t,
		f.do(t, http.MethodPost, "/api/marketplace/notifications/read-all", f.otherID))
	assert.Equal(t, int64(1), res["updated"])
	bPage := decodeNotificationData[notificationPage](t,
		f.do(t, http.MethodGet, "/api/marketplace/notifications", f.otherID))
	assert.Zero(t, bPage.UnreadCount)
	aPage = decodeNotificationData[notificationPage](t,
		f.do(t, http.MethodGet, "/api/marketplace/notifications", f.userID))
	assert.Equal(t, int64(1), aPage.UnreadCount, "B's read-all must not touch A's rows")

	// A's read-all clears the rest, and is idempotent.
	res = decodeNotificationData[map[string]int64](t,
		f.do(t, http.MethodPost, "/api/marketplace/notifications/read-all", f.userID))
	assert.Equal(t, int64(1), res["updated"])
	aPage = decodeNotificationData[notificationPage](t,
		f.do(t, http.MethodGet, "/api/marketplace/notifications", f.userID))
	assert.Zero(t, aPage.UnreadCount)
	for _, item := range aPage.Items {
		assert.True(t, item.Read)
	}
	again := decodeNotificationData[map[string]int64](t,
		f.do(t, http.MethodPost, "/api/marketplace/notifications/read-all", f.userID))
	assert.Zero(t, again["updated"])

	// Invalid ids are a client error, not a 404 leak.
	assert.Equal(t, http.StatusBadRequest, f.do(t, http.MethodPost,
		"/api/marketplace/notifications/abc/read", f.userID).Code)
}

func TestCreateNotificationValidation(t *testing.T) {
	f := newNotificationFixture(t)

	assert.Error(t, CreateNotification(nil, f.userID, NotificationKindSystem, "t", "", ""))
	assert.Error(t, CreateNotification(f.db, 0, NotificationKindSystem, "t", "", ""))

	// Every documented kind is accepted; an unknown kind degrades to system.
	for _, kind := range []string{
		NotificationKindReview,
		NotificationKindListingDegraded,
		NotificationKindBalanceLow,
		NotificationKindTopup,
		NotificationKindWithdrawal,
		NotificationKindKeyExpiring,
		NotificationKindSystem,
	} {
		require.NoError(t, CreateNotification(f.db, f.userID, kind, "kind-"+kind, "", ""))
	}
	require.NoError(t, CreateNotification(f.db, f.userID, "bogus", "kind-bogus", "", ""))

	var bogus Notification
	require.NoError(t, f.db.Where("user_id = ? AND title = ?", f.userID, "kind-bogus").Take(&bogus).Error)
	assert.Equal(t, NotificationKindSystem, bogus.Kind)

	var count int64
	require.NoError(t, f.db.Model(&Notification{}).Where("user_id = ?", f.userID).Count(&count).Error)
	assert.Equal(t, int64(8), count)
}
