package marketplace

// api_wallet.go — 买家钱包 API (§3.9, FRONTEND-REBUILD §5): balance, the
// current month's spend and savings, and the buyer's own ledger flow.
//
// Money is integer 微元 everywhere; nothing here does float arithmetic. The
// balance is users.quota verbatim (quota == 微元, §4.5); month spend comes
// from this buyer's mp_ledger debit rows, month savings from the mp_requests
// 官方价 − 实付 difference (§3.8).
//
// Ownership: every query is scoped to the logged-in user id. The handler
// never accepts a user id from the request, so one buyer can never read
// another buyer's wallet or ledger. These are console APIs and answer in the
// upstream envelope (common.ApiSuccess / common.ApiErrorStatus), like
// api_records.go.
//
// Route wiring is central (api.go / router/api-router.go): g is expected to be
// scoped to /marketplace and MUST already carry middleware.UserAuth(), e.g.
// apiRouter.Group("/marketplace", middleware.UserAuth()).

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

// RegisterWalletRoutes mounts the buyer wallet endpoints on g. g MUST already
// carry middleware.UserAuth() (this package cannot import middleware without
// an import cycle, §4.4).
func RegisterWalletRoutes(g *gin.RouterGroup) {
	registerWalletRoutes(g)
}

func registerWalletRoutes(g *gin.RouterGroup) {
	g.GET("/wallet", getWallet)
	g.GET("/wallet/ledger", listWalletLedger)
}

// Wallet pagination follows the other console lists: page starts at 1,
// page_size defaults to 20 and is capped at 100.
const (
	walletPageSizeDefault = 20
	walletPageSizeMax     = 100
)

// walletSummary is GET /wallet's payload. All amounts are 微元.
type walletSummary struct {
	// BalanceMicro is the buyer's users.quota (quota 1:1 with 微元).
	BalanceMicro int64 `json:"balance_micro"`
	// MonthSpendMicro is what the buyer was charged this month (Asia/Shanghai
	// month): the absolute buyer_debit + tool_debit of the buyer's ledger rows.
	MonthSpendMicro int64 `json:"month_spend_micro"`
	// MonthSavedMicro is 官方价 − 实付 over this month's mp_requests (§3.8).
	MonthSavedMicro int64 `json:"month_saved_micro"`
}

// walletLedgerEntry is one row of GET /wallet/ledger. AmountMicro is signed:
// debits are negative, refunds positive.
type walletLedgerEntry struct {
	RequestID   string    `json:"request_id"`
	EntryType   string    `json:"entry_type"`
	AmountMicro int64     `json:"amount_micro"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// consoleDB returns the marketplace database handle without loading the
// holiday calendar: the wallet/stats reads do not price anything, and tests
// can point relayDB at an isolated database without extra state.
func consoleDB(c *gin.Context) *gorm.DB {
	relayStateMu.Lock()
	db := relayDB
	relayStateMu.Unlock()
	if db == nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
		return nil
	}
	return db
}

// consoleUserID returns the logged-in user id (UserAuth has already run on the
// group). <= 0 is answered with 401 by the caller, defensively.
func consoleUserID(c *gin.Context) int64 {
	return int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
}

// parseWalletPageQuery reads page/page_size with the same rules as the other
// console lists.
func parseWalletPageQuery(c *gin.Context) (page, pageSize int) {
	page, err := strconv.Atoi(c.Query("page"))
	if err != nil || page < 1 {
		page = 1
	}
	pageSize, err = strconv.Atoi(c.Query("page_size"))
	if err != nil || pageSize <= 0 {
		pageSize = walletPageSizeDefault
	}
	if pageSize > walletPageSizeMax {
		pageSize = walletPageSizeMax
	}
	return page, pageSize
}

// shanghaiMonthRange returns [start, end) of the Asia/Shanghai month
// containing now.
func shanghaiMonthRange(now time.Time) (start, end time.Time, err error) {
	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	lt := now.In(loc)
	start = time.Date(lt.Year(), lt.Month(), 1, 0, 0, 0, 0, loc)
	end = start.AddDate(0, 1, 0)
	return start, end, nil
}

// buyerLedgerAccounts lists the ledger account spellings a buyer's debit rows
// may use. "user:<id>" is the current convention; "buyer:<id>" is accepted so
// the wallet keeps working if buyer/seller accounts get split (CODE-REVIEW
// H5). No other account name can match, so seller credits of a user who is
// also a seller never leak into the buyer view.
func buyerLedgerAccounts(userID int64) []string {
	id := strconv.FormatInt(userID, 10)
	return []string{"user:" + id, "buyer:" + id}
}

// buyerLedgerEntryTypes are the buyer-side flow rows of §3.9: token debit,
// per-call tool debit and refund.
func buyerLedgerEntryTypes() []string {
	return []string{EntryBuyerDebit, EntryToolDebit, EntryRefund}
}

// walletSummaryAt computes the wallet payload for one buyer at instant now.
// It is separated from the handler so tests can pin the month boundary.
func walletSummaryAt(db *gorm.DB, userID int64, now time.Time) (walletSummary, error) {
	var summary walletSummary
	if err := db.Table("users").Select("COALESCE(quota, 0)").
		Where("id = ?", userID).Scan(&summary.BalanceMicro).Error; err != nil {
		return walletSummary{}, err
	}
	start, end, err := shanghaiMonthRange(now)
	if err != nil {
		return walletSummary{}, err
	}
	// 本月被扣合计: the ledger's buyer debit rows, as an absolute sum.
	if err := db.Model(&LedgerEntry{}).
		Select("COALESCE(SUM(ABS(amount_micro)), 0)").
		Where("account IN ? AND entry_type IN ? AND created_at >= ? AND created_at < ?",
			buyerLedgerAccounts(userID),
			[]string{EntryBuyerDebit, EntryToolDebit},
			start, end).
		Scan(&summary.MonthSpendMicro).Error; err != nil {
		return walletSummary{}, err
	}
	// 本月省了多少: the same 官方价 − 实付 difference the records list shows.
	if err := db.Model(&Request{}).
		Select("COALESCE(SUM(official_cost_micro - total_debit_micro), 0)").
		Where("buyer_user_id = ? AND created_at >= ? AND created_at < ?", userID, start, end).
		Scan(&summary.MonthSavedMicro).Error; err != nil {
		return walletSummary{}, err
	}
	return summary, nil
}

// getWallet serves GET /wallet for the logged-in buyer.
func getWallet(c *gin.Context) {
	userID := consoleUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	db := consoleDB(c)
	if db == nil {
		return
	}
	summary, err := walletSummaryAt(db, userID, time.Now())
	if err != nil {
		common.SysError(common.LogText("marketplace wallet: query summary: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query wallet"))
		return
	}
	common.ApiSuccess(c, summary)
}

// listWalletLedger serves GET /wallet/ledger?page=&page_size= with this
// buyer's debit/tool-debit/refund rows, newest first.
func listWalletLedger(c *gin.Context) {
	userID := consoleUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	db := consoleDB(c)
	if db == nil {
		return
	}
	page, pageSize := parseWalletPageQuery(c)
	accounts := buyerLedgerAccounts(userID)
	types := buyerLedgerEntryTypes()

	var total int64
	if err := db.Model(&LedgerEntry{}).
		Where("account IN ? AND entry_type IN ?", accounts, types).
		Count(&total).Error; err != nil {
		common.SysError(common.LogText("marketplace wallet: count ledger: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query wallet ledger"))
		return
	}
	var rows []LedgerEntry
	if err := db.Where("account IN ? AND entry_type IN ?", accounts, types).
		Order("created_at DESC").Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&rows).Error; err != nil {
		common.SysError(common.LogText("marketplace wallet: query ledger: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query wallet ledger"))
		return
	}
	items := make([]walletLedgerEntry, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		items = append(items, walletLedgerEntry{
			RequestID:   r.RequestID,
			EntryType:   r.EntryType,
			AmountMicro: r.AmountMicro,
			Status:      r.Status,
			CreatedAt:   r.CreatedAt,
		})
	}
	pageInfo := &common.PageInfo{Page: page, PageSize: pageSize}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}
