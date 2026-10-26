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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
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
	g.GET("/wallet/transactions", listWalletTransactions)
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
// console lists. Upstream console lists spell the page as "p"; the legacy
// wallet endpoints used "page", so both are accepted.
func parseWalletPageQuery(c *gin.Context) (page, pageSize int) {
	rawPage := c.Query("p")
	if rawPage == "" {
		rawPage = c.Query("page")
	}
	page, err := strconv.Atoi(rawPage)
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

// Unified wallet flow kinds. "usage" and "refund" are ledger rows, "topup"
// comes from the upstream top_ups table.
const (
	walletTxKindAll    = "all"
	walletTxKindUsage  = "usage"
	walletTxKindRefund = "refund"
	walletTxKindTopup  = "topup"
)

// walletTransaction is one row of GET /wallet/transactions, merging the
// marketplace ledger with the upstream top-up orders:
//
//	id            "ledger:<mp_ledger.id>" or "topup:<top_ups.id>"
//	kind          usage | refund | topup
//	amount_micro  signed ledger amount, or the credited quota of a top-up
//	status        ledger status (available/pending/reversed) or top-up status
//	reference     ledger request id or top-up trade number
//	money         what the buyer actually paid for a top-up, omitted for ledger
//	created_at    row time (top_ups.create_time is Unix seconds)
type walletTransaction struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	AmountMicro int64     `json:"amount_micro"`
	Status      string    `json:"status"`
	Reference   string    `json:"reference"`
	Money       float64   `json:"money,omitempty"`
	CreatedAt   time.Time `json:"created_at"`

	// sourceID is the raw mp_ledger.id / top_ups.id, used only to break ties
	// between two rows with the same second. It is never serialized.
	sourceID int64
}

// topUpAmountMicro converts top_ups.amount into 微元.
//
// top_ups.amount is the display (currency) amount, not an already-micro
// value: in this deployment it is CNY, and the credited quota is
// amount × QuotaPerUnit — exactly the conversion the recharge paths perform
// (model.RechargeEpay/RechargeWaffo) and getTopUpQuota uses for a currency
// display type. The conversion is intentionally decimal-backed so it matches
// the credit path instead of accumulating float error.
func topUpAmountMicro(amount int64) int64 {
	if amount <= 0 {
		return 0
	}
	quota, err := common.WalletQuotaFromDecimalStrict(
		decimal.NewFromInt(amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)),
	)
	if err != nil || quota <= 0 {
		return 0
	}
	return int64(quota)
}

// listWalletTransactions serves GET /wallet/transactions?p=&page_size=&kind=
// with one merged, newest-first stream of the buyer's ledger rows and top-up
// orders.
//
// Pagination is correct across both sources without an in-database union: each
// source is read up to page*page_size rows (already sorted), the two slices
// are merged in memory by created_at DESC then source id DESC, and the
// requested page is cut out of the result. total is the sum of both source
// counts, so the UI can size the paginator. kind=usage/topup restricts the
// stream to one source; refund rows count as usage-source rows.
func listWalletTransactions(c *gin.Context) {
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
	kind := strings.ToLower(strings.TrimSpace(c.Query("kind")))
	switch kind {
	case "":
		kind = walletTxKindAll
	case walletTxKindAll, walletTxKindUsage, walletTxKindTopup:
	default:
		// Unknown filters are ignored; the console only sends the three
		// documented values, and "all" is the safe default.
		kind = walletTxKindAll
	}

	// Rows needed from each source before merging: the requested page ends at
	// page*page_size, so no source can contribute a later row to an earlier
	// page. Each source read is capped at its own row count, which keeps a
	// deep page from asking the database for rows that do not exist. A
	// multiplication overflow degrades to "read all rows" rather than a
	// negative limit.
	limit := page * pageSize
	if limit < pageSize {
		limit = 0
	}
	items := make([]walletTransaction, 0)
	var total int64

	if kind == walletTxKindAll || kind == walletTxKindUsage {
		accounts := buyerLedgerAccounts(userID)
		types := buyerLedgerEntryTypes()
		var ledgerTotal int64
		if err := db.Model(&LedgerEntry{}).
			Where("account IN ? AND entry_type IN ?", accounts, types).
			Count(&ledgerTotal).Error; err != nil {
			common.SysError(common.LogText("marketplace wallet: count transactions ledger: %s", err.Error()))
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query wallet transactions"))
			return
		}
		total += ledgerTotal
		if ledgerTotal > 0 {
			fetchLimit := limit
			if fetchLimit <= 0 || int64(fetchLimit) > ledgerTotal {
				fetchLimit = int(ledgerTotal)
			}
			var rows []LedgerEntry
			if err := db.Where("account IN ? AND entry_type IN ?", accounts, types).
				Order("created_at DESC").Order("id DESC").
				Limit(fetchLimit).
				Find(&rows).Error; err != nil {
				common.SysError(common.LogText("marketplace wallet: query transactions ledger: %s", err.Error()))
				common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query wallet transactions"))
				return
			}
			for i := range rows {
				r := &rows[i]
				entryKind := walletTxKindUsage
				if r.EntryType == EntryRefund {
					entryKind = walletTxKindRefund
				}
				items = append(items, walletTransaction{
					ID:          "ledger:" + strconv.FormatInt(r.ID, 10),
					Kind:        entryKind,
					AmountMicro: r.AmountMicro,
					Status:      r.Status,
					Reference:   r.RequestID,
					CreatedAt:   r.CreatedAt,
					sourceID:    r.ID,
				})
			}
		}
	}

	if kind == walletTxKindAll || kind == walletTxKindTopup {
		var topupTotal int64
		if err := db.Model(&model.TopUp{}).
			Where("user_id = ?", userID).
			Count(&topupTotal).Error; err != nil {
			common.SysError(common.LogText("marketplace wallet: count transactions topups: %s", err.Error()))
			common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query wallet transactions"))
			return
		}
		total += topupTotal
		if topupTotal > 0 {
			fetchLimit := limit
			if fetchLimit <= 0 || int64(fetchLimit) > topupTotal {
				fetchLimit = int(topupTotal)
			}
			var rows []model.TopUp
			if err := db.Where("user_id = ?", userID).
				Order("create_time DESC").Order("id DESC").
				Limit(fetchLimit).
				Find(&rows).Error; err != nil {
				common.SysError(common.LogText("marketplace wallet: query transactions topups: %s", err.Error()))
				common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query wallet transactions"))
				return
			}
			for i := range rows {
				r := &rows[i]
				items = append(items, walletTransaction{
					ID:          "topup:" + strconv.Itoa(r.Id),
					Kind:        walletTxKindTopup,
					AmountMicro: topUpAmountMicro(r.Amount),
					Status:      r.Status,
					Reference:   r.TradeNo,
					Money:       r.Money,
					CreatedAt:   time.Unix(r.CreateTime, 0),
					sourceID:    int64(r.Id),
				})
			}
		}
	}

	// Newest first; equal timestamps fall back to the higher source row id so
	// the order is stable across requests. The final id comparison only makes
	// the ordering total when both sources happen to share an id.
	sort.Slice(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		if items[i].sourceID != items[j].sourceID {
			return items[i].sourceID > items[j].sourceID
		}
		return items[i].ID > items[j].ID
	})

	start := (page - 1) * pageSize
	if start < 0 || start >= len(items) {
		items = items[:0]
	} else {
		end := start + pageSize
		if end > len(items) {
			end = len(items)
		}
		items = items[start:end]
	}

	pageInfo := &common.PageInfo{Page: page, PageSize: pageSize}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}
