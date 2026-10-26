package marketplace

// notifications.go — 通知中心 (P8).
//
// One row per user-facing notification in mp_notifications. The table is
// scoped by user_id; every read and every mark-read query carries the
// logged-in user id, so one account can never read or mutate another
// account's rows.
//
//	GET  /marketplace/notifications?page=&page_size=   paginated list + unread_count
//	POST /marketplace/notifications/:id/read            mark one as read
//	POST /marketplace/notifications/read-all            mark every unread row
//
// CreateNotification is the write seam for the rest of the package: review,
// listing_degraded, balance_low, topup, withdrawal, key_expiring and system
// events call it. This change does not wire those call sites yet.
//
// The routes expect a group that already carries middleware.UserAuth(); the
// handlers defensively answer 401 when the context user id is missing.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// Notification kinds (P8).
const (
	NotificationKindReview          = "review"
	NotificationKindListingDegraded = "listing_degraded"
	NotificationKindBalanceLow      = "balance_low"
	NotificationKindTopup           = "topup"
	NotificationKindWithdrawal      = "withdrawal"
	NotificationKindKeyExpiring     = "key_expiring"
	NotificationKindSystem          = "system"
)

// notificationKinds is the accepted set; an unknown kind falls back to
// "system" so a typo cannot poison the table with an unrenderable row.
var notificationKinds = map[string]struct{}{
	NotificationKindReview:          {},
	NotificationKindListingDegraded: {},
	NotificationKindBalanceLow:      {},
	NotificationKindTopup:           {},
	NotificationKindWithdrawal:      {},
	NotificationKindKeyExpiring:     {},
	NotificationKindSystem:          {},
}

// Notification is one mp_notifications row (P8). ReadAt is NULL until the
// user reads it. The composite index (user_id, read_at, created_at) serves
// both the paginated list (user + created_at order) and the unread count
// (user + read_at IS NULL).
type Notification struct {
	ID        int64      `gorm:"primaryKey"`
	UserID    int64      `gorm:"not null;index:idx_mp_notifications_user_read_created,priority:1"`
	Kind      string     `gorm:"size:32;not null"`
	Title     string     `gorm:"size:255;not null"`
	Body      string     `gorm:"type:text"`
	Link      string     `gorm:"type:text"`
	ReadAt    *time.Time `gorm:"index:idx_mp_notifications_user_read_created,priority:2"`
	CreatedAt time.Time  `gorm:"index:idx_mp_notifications_user_read_created,priority:3"`
}

func (Notification) TableName() string { return "mp_notifications" }

// CreateNotification writes one notification row. userID must be positive
// (there are no broadcast rows); an empty/unknown kind is stored as "system".
func CreateNotification(db *gorm.DB, userID int64, kind, title, body, link string) error {
	if db == nil {
		return errors.New("marketplace: notification database is not initialized")
	}
	if userID <= 0 {
		return errors.New("marketplace: notification user id must be positive")
	}
	kind = strings.TrimSpace(kind)
	if _, ok := notificationKinds[kind]; !ok {
		kind = NotificationKindSystem
	}
	return db.Create(&Notification{
		UserID: userID,
		Kind:   kind,
		Title:  title,
		Body:   body,
		Link:   link,
	}).Error
}

// RegisterNotificationRoutes mounts the notification center endpoints on g.
// g MUST already carry middleware.UserAuth() (this package cannot import
// middleware without an import cycle).
func RegisterNotificationRoutes(g *gin.RouterGroup) {
	g.GET("/notifications", listNotifications)
	g.POST("/notifications/read-all", markAllNotificationsRead)
	g.POST("/notifications/:id/read", markNotificationRead)
}

// Notification pagination follows the other console lists: page starts at 1,
// page_size defaults to 20 and is capped at 100.
const (
	notificationPageSizeDefault = 20
	notificationPageSizeMax     = 100
)

func parseNotificationPageQuery(c *gin.Context) (page, pageSize int) {
	page, err := strconv.Atoi(c.Query("page"))
	if err != nil || page < 1 {
		page = 1
	}
	pageSize, err = strconv.Atoi(c.Query("page_size"))
	if err != nil || pageSize <= 0 {
		pageSize = notificationPageSizeDefault
	}
	if pageSize > notificationPageSizeMax {
		pageSize = notificationPageSizeMax
	}
	return page, pageSize
}

// notificationItem is one row of the list response. ReadAt is null while
// unread; Read is the convenience boolean the console binds to.
type notificationItem struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Link      string     `json:"link"`
	Read      bool       `json:"read"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

func newNotificationItem(n *Notification) notificationItem {
	return notificationItem{
		ID:        n.ID,
		Kind:      n.Kind,
		Title:     n.Title,
		Body:      n.Body,
		Link:      n.Link,
		Read:      n.ReadAt != nil,
		ReadAt:    n.ReadAt,
		CreatedAt: n.CreatedAt,
	}
}

// notificationPage is GET /notifications's payload. UnreadCount is the
// user-wide unread total, independent of the current page.
type notificationPage struct {
	Page        int                `json:"page"`
	PageSize    int                `json:"page_size"`
	Total       int                `json:"total"`
	UnreadCount int64              `json:"unread_count"`
	Items       []notificationItem `json:"items"`
}

// listNotifications serves GET /notifications for the logged-in user only.
func listNotifications(c *gin.Context) {
	userID := consoleUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	db := consoleDB(c)
	if db == nil {
		return
	}
	page, pageSize := parseNotificationPageQuery(c)

	var total int64
	if err := db.Model(&Notification{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
		common.SysError(common.LogText("marketplace notifications count: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query notifications"))
		return
	}
	var unread int64
	if err := db.Model(&Notification{}).
		Where("user_id = ? AND read_at IS NULL", userID).
		Count(&unread).Error; err != nil {
		common.SysError(common.LogText("marketplace notifications unread: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query notifications"))
		return
	}
	var rows []Notification
	if err := db.Where("user_id = ?", userID).
		Order("created_at DESC").Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&rows).Error; err != nil {
		common.SysError(common.LogText("marketplace notifications list: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query notifications"))
		return
	}
	items := make([]notificationItem, 0, len(rows))
	for i := range rows {
		items = append(items, newNotificationItem(&rows[i]))
	}
	common.ApiSuccess(c, notificationPage{
		Page:        page,
		PageSize:    pageSize,
		Total:       int(total),
		UnreadCount: unread,
		Items:       items,
	})
}

// markNotificationRead serves POST /notifications/:id/read. A row owned by
// another user (or a nonexistent id) answers 404 — ownership is never leaked.
// Marking an already-read row is idempotent.
func markNotificationRead(c *gin.Context) {
	userID := consoleUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid notification id"))
		return
	}
	db := consoleDB(c)
	if db == nil {
		return
	}
	var row Notification
	err = db.Where("id = ? AND user_id = ?", id, userID).Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusNotFound, errors.New("notification not found"))
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query notification"))
		return
	}
	if row.ReadAt == nil {
		now := time.Now()
		if err := db.Model(&Notification{}).
			Where("id = ? AND user_id = ? AND read_at IS NULL", id, userID).
			Update("read_at", now).Error; err != nil {
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to mark notification read"))
			return
		}
		row.ReadAt = &now
	}
	common.ApiSuccess(c, newNotificationItem(&row))
}

// markAllNotificationsRead serves POST /notifications/read-all, scoped to the
// logged-in user. It is idempotent and reports how many rows it changed.
func markAllNotificationsRead(c *gin.Context) {
	userID := consoleUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	db := consoleDB(c)
	if db == nil {
		return
	}
	res := db.Model(&Notification{}).
		Where("user_id = ? AND read_at IS NULL", userID).
		Update("read_at", time.Now())
	if res.Error != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to mark notifications read"))
		return
	}
	common.ApiSuccess(c, gin.H{"updated": res.RowsAffected})
}

// notifySeller writes a notification to the seller that owns listingID. A
// failed lookup or insert is logged, never returned: notifying is a side
// effect of the business action, not part of its contract.
func notifySeller(db *gorm.DB, listingID int64, kind, title, body, link string) {
	if db == nil || listingID <= 0 {
		return
	}
	var sellerID int64
	if err := db.Model(&Listing{}).Select("seller_id").Where("id = ?", listingID).
		Scan(&sellerID).Error; err != nil || sellerID <= 0 {
		return
	}
	if err := CreateNotification(db, sellerID, kind, title, body, link); err != nil {
		common.SysError(common.LogText("marketplace: notification for listing %d: %s", listingID, err.Error()))
	}
}

// notifyUser writes one notification for a known user id (payouts are
// seller-level, so there is no listing to resolve). Failures are logged only.
func notifyUser(db *gorm.DB, userID int64, kind, title, body, link string) {
	if db == nil || userID <= 0 {
		return
	}
	if err := CreateNotification(db, userID, kind, title, body, link); err != nil {
		common.SysError(common.LogText("marketplace: notification for user %d: %s", userID, err.Error()))
	}
}
