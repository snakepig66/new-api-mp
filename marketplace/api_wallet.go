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
	"encoding/csv"
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
	g.GET("/wallet/transactions/export", exportWalletTransactions)
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
	// GiftQuotaMicro is the gifted part of the balance (redemption codes,
	// invitation transfers, admin grants). It can only be spent on official
	// channels.
	GiftQuotaMicro int64 `json:"gift_quota_micro"`
	// PaidQuotaMicro is BalanceMicro - GiftQuotaMicro; the part spendable
	// everywhere, including seller channels.
	PaidQuotaMicro int64 `json:"paid_quota_micro"`
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
	var balances struct {
		Quota int64
		Gift  int64
	}
	if err := db.Table("users").
		Select("COALESCE(quota, 0) AS quota, COALESCE(gift_quota, 0) AS gift").
		Where("id = ?", userID).Scan(&balances).Error; err != nil {
		return walletSummary{}, err
	}
	summary.BalanceMicro = balances.Quota
	// Gift is never above the total; clamp defensively so a legacy row with an
	// inconsistent split cannot report a negative paid balance.
	summary.GiftQuotaMicro = min(max(balances.Gift, 0), max(balances.Quota, 0))
	summary.PaidQuotaMicro = summary.BalanceMicro - summary.GiftQuotaMicro
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

// Unified wallet flow kinds. The wallet is scoped to the logged-in user, so a
// user who both buys and sells sees both sides of their own flow. Ledger entry
// types map to a kind as follows:
//
//	buyer_debit / tool_debit          → usage
//	refund                            → refund
//	seller_credit / reversal          → earning
//	withdraw / withdrawal / payout    → withdrawal (reserved; payouts are the
//	                                    live source, see mp_payouts)
//	top_ups                           → topup
const (
	walletTxKindAll        = "all"
	walletTxKindUsage      = "usage"
	walletTxKindRefund     = "refund"
	walletTxKindTopup      = "topup"
	walletTxKindEarning    = "earning"
	walletTxKindWithdrawal = "withdrawal"
)

// walletTransaction is one row of GET /wallet/transactions, merging the
// marketplace ledger, upstream top-up orders and seller payouts:
//
//	id            "ledger:<mp_ledger.id>" | "topup:<top_ups.id>" | "payout:<mp_payouts.id>"
//	kind          usage | refund | topup | earning | withdrawal
//	amount_micro  signed ledger amount, credited quota of a top-up, or the
//	              negative amount of a payout
//	status        ledger status, top-up status or payout status
//	reference     ledger request id, top-up trade number or payout idempotency key
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

	// sourceID is the raw mp_ledger.id / top_ups.id / mp_payouts.id, used only
	// to break ties between two rows with the same second. It is never
	// serialized.
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

// normalizeWalletTxKind maps the query value to a supported kind; unknown or
// empty values behave like "all".
func normalizeWalletTxKind(raw string) string {
	switch kind := strings.ToLower(strings.TrimSpace(raw)); kind {
	case walletTxKindUsage, walletTxKindRefund, walletTxKindTopup,
		walletTxKindEarning, walletTxKindWithdrawal:
		return kind
	default:
		return walletTxKindAll
	}
}

// sellerWalletAccounts lists the ledger accounts a seller's income and
// withdrawal rows may use. "seller:<id>" is the split convention and
// "user:<id>" covers legacy rows written before the split.
func sellerWalletAccounts(userID int64) []string {
	id := strconv.FormatInt(userID, 10)
	return []string{"seller:" + id, "user:" + id}
}

// earningLedgerEntryTypes are the seller-side income rows: the credit itself
// and its reversal compensation.
func earningLedgerEntryTypes() []string {
	return []string{EntrySellerCredit, EntryReversal}
}

// withdrawalLedgerEntryTypes are the reserved ledger spellings for a payout.
// Payouts live in mp_payouts today; accepting these names keeps the export
// stable if the ledger starts recording withdrawals directly.
func withdrawalLedgerEntryTypes() []string {
	return []string{"withdraw", "withdrawal", "payout"}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// walletLedgerScope returns the ledger account and entry-type filters for one
// kind. "all" takes the union of both sides of the user's own flow.
func walletLedgerScope(userID int64, kind string) (accounts, types []string) {
	switch kind {
	case walletTxKindUsage:
		return buyerLedgerAccounts(userID), []string{EntryBuyerDebit, EntryToolDebit}
	case walletTxKindRefund:
		return buyerLedgerAccounts(userID), []string{EntryRefund}
	case walletTxKindEarning:
		return sellerWalletAccounts(userID), earningLedgerEntryTypes()
	case walletTxKindWithdrawal:
		return sellerWalletAccounts(userID), withdrawalLedgerEntryTypes()
	default:
		accounts = uniqueStrings(append(buyerLedgerAccounts(userID), sellerWalletAccounts(userID)...))
		types = uniqueStrings(append(
			append(buyerLedgerEntryTypes(), earningLedgerEntryTypes()...),
			withdrawalLedgerEntryTypes()...,
		))
		return accounts, types
	}
}

// walletKindForLedgerEntry maps an mp_ledger entry_type to its wallet kind.
func walletKindForLedgerEntry(entryType string) string {
	switch entryType {
	case EntryBuyerDebit, EntryToolDebit:
		return walletTxKindUsage
	case EntryRefund:
		return walletTxKindRefund
	case EntrySellerCredit, EntryReversal:
		return walletTxKindEarning
	case "withdraw", "withdrawal", "payout":
		return walletTxKindWithdrawal
	default:
		return walletTxKindUsage
	}
}

// walletFetchLimit is the number of rows to read per source: the requested
// page ends at page*page_size, so no source can contribute a later row to an
// earlier page. A non-positive value means "read every row" (the CSV export).
func walletFetchLimit(page, pageSize int) int {
	limit := page * pageSize
	if limit < pageSize {
		return 0
	}
	return limit
}

func limitRows(total int64, limit int) int {
	if total <= 0 {
		return 0
	}
	if limit <= 0 || int64(limit) > total {
		return int(total)
	}
	return limit
}

func loadWalletLedgerRows(db *gorm.DB, userID int64, kind string, limit int) ([]walletTransaction, int64, error) {
	accounts, types := walletLedgerScope(userID, kind)
	var total int64
	if err := db.Model(&LedgerEntry{}).
		Where("account IN ? AND entry_type IN ?", accounts, types).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}
	query := db.Where("account IN ? AND entry_type IN ?", accounts, types).
		Order("created_at DESC").Order("id DESC")
	if fetch := limitRows(total, limit); fetch < int(total) {
		query = query.Limit(fetch)
	}
	var rows []LedgerEntry
	if err := query.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]walletTransaction, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		items = append(items, walletTransaction{
			ID:          "ledger:" + strconv.FormatInt(r.ID, 10),
			Kind:        walletKindForLedgerEntry(r.EntryType),
			AmountMicro: r.AmountMicro,
			Status:      r.Status,
			Reference:   r.RequestID,
			CreatedAt:   r.CreatedAt,
			sourceID:    r.ID,
		})
	}
	return items, total, nil
}

func loadWalletTopUpRows(db *gorm.DB, userID int64, limit int) ([]walletTransaction, int64, error) {
	var total int64
	if err := db.Model(&model.TopUp{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}
	query := db.Where("user_id = ?", userID).Order("create_time DESC").Order("id DESC")
	if fetch := limitRows(total, limit); fetch < int(total) {
		query = query.Limit(fetch)
	}
	var rows []model.TopUp
	if err := query.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]walletTransaction, 0, len(rows))
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
	return items, total, nil
}

func loadWalletPayoutRows(db *gorm.DB, userID int64, limit int) ([]walletTransaction, int64, error) {
	var total int64
	if err := db.Model(&Payout{}).Where("seller_id = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}
	query := db.Where("seller_id = ?", userID).Order("created_at DESC").Order("id DESC")
	if fetch := limitRows(total, limit); fetch < int(total) {
		query = query.Limit(fetch)
	}
	var rows []Payout
	if err := query.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]walletTransaction, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		items = append(items, walletTransaction{
			ID:          "payout:" + strconv.FormatInt(r.ID, 10),
			Kind:        walletTxKindWithdrawal,
			AmountMicro: -r.AmountMicro,
			Status:      r.Status,
			Reference:   r.IdempotencyKey,
			CreatedAt:   r.CreatedAt,
			sourceID:    r.ID,
		})
	}
	return items, total, nil
}

// loadWalletTransactions reads the sources selected by kind. limit <= 0 means
// "every row" (export); otherwise each source is capped at page*page_size.
// The result is merged newest-first.
func loadWalletTransactions(db *gorm.DB, userID int64, kind string, limit int) ([]walletTransaction, int64, error) {
	items := make([]walletTransaction, 0)
	var total int64

	ledgerKind := kind == walletTxKindAll || kind == walletTxKindUsage ||
		kind == walletTxKindRefund || kind == walletTxKindEarning ||
		kind == walletTxKindWithdrawal
	if ledgerKind {
		rows, count, err := loadWalletLedgerRows(db, userID, kind, limit)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, rows...)
		total += count
	}
	if kind == walletTxKindAll || kind == walletTxKindTopup {
		rows, count, err := loadWalletTopUpRows(db, userID, limit)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, rows...)
		total += count
	}
	if kind == walletTxKindAll || kind == walletTxKindWithdrawal {
		rows, count, err := loadWalletPayoutRows(db, userID, limit)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, rows...)
		total += count
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
	return items, total, nil
}

// listWalletTransactions serves GET /wallet/transactions?p=&page_size=&kind=
// with one merged, newest-first stream of the buyer's ledger rows, top-up
// orders and (for a seller) payout rows.
//
// kind=all|usage|topup|earning|withdrawal|refund selects one slice; refund is
// its own kind now that 消费 and 退款 are separate filters.
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
	kind := normalizeWalletTxKind(c.Query("kind"))

	items, total, err := loadWalletTransactions(db, userID, kind, walletFetchLimit(page, pageSize))
	if err != nil {
		common.SysError(common.LogText("marketplace wallet: query transactions: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query wallet transactions"))
		return
	}

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

// Wallet flow labels used by the CSV export. They mirror the console's
// Chinese wording; the raw status/kind values stay untouched in JSON APIs.
func walletCSVKindLabel(kind string) string {
	switch kind {
	case walletTxKindUsage:
		return "消费"
	case walletTxKindRefund:
		return "退款"
	case walletTxKindTopup:
		return "充值"
	case walletTxKindEarning:
		return "收益"
	case walletTxKindWithdrawal:
		return "提现"
	default:
		return "其他"
	}
}

func walletCSVDescription(kind string) string {
	switch kind {
	case walletTxKindUsage:
		return "模型调用扣费"
	case walletTxKindRefund:
		return "退款入账"
	case walletTxKindTopup:
		return "在线充值"
	case walletTxKindEarning:
		return "分享收益"
	case walletTxKindWithdrawal:
		return "提现"
	default:
		return "其他"
	}
}

func walletCSVStatus(kind, status string) string {
	switch kind {
	case walletTxKindTopup:
		switch status {
		case "success":
			return "已到账"
		case "pending":
			return "待支付"
		case "failed", "expired":
			return "已关闭"
		}
	case walletTxKindWithdrawal:
		switch status {
		case PayoutPending:
			return "审核中"
		case PayoutApproved:
			return "已通过"
		case PayoutPaid:
			return "已打款"
		case PayoutRejected:
			return "已驳回"
		}
	default:
		switch status {
		case LedgerAvailable:
			return "已入账"
		case LedgerPending:
			return "待结算"
		case LedgerReversed:
			return "已冲正"
		}
	}
	return "未知"
}

// csvYuan renders 微元 as a yuan string with at least two and at most six
// decimals, trailing zeros trimmed. It never rounds to a cent, so sub-cent
// ledger rows survive the export.
func csvYuan(micro int64) string {
	value := decimal.NewFromInt(micro).
		Div(decimal.NewFromInt(1_000_000)).
		StringFixed(6)
	value = strings.TrimRight(value, "0")
	if strings.HasSuffix(value, ".") {
		value += "00"
	} else if dot := strings.IndexByte(value, '.'); dot >= 0 && len(value)-dot-1 == 1 {
		value += "0"
	}
	if value == "-0.00" {
		value = "0.00"
	}
	return value
}

// exportWalletTransactions serves GET /wallet/transactions/export?kind=... as
// a UTF-8 CSV (with BOM so Excel opens Chinese correctly). It exports every
// row of the current user's filtered flow — never another user's — and reuses
// the same ownership-scoped loader as the paginated endpoint.
func exportWalletTransactions(c *gin.Context) {
	userID := consoleUserID(c)
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	db := consoleDB(c)
	if db == nil {
		return
	}
	kind := normalizeWalletTxKind(c.Query("kind"))
	items, _, err := loadWalletTransactions(db, userID, kind, 0)
	if err != nil {
		common.SysError(common.LogText("marketplace wallet: export transactions: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to export wallet transactions"))
		return
	}

	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		loc = time.Local
	}
	filename := "wallet-" + kind + "-" + time.Now().In(loc).Format("20060102-150405") + ".csv"
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Status(http.StatusOK)
	// BOM: Excel on Windows needs it to detect UTF-8 for Chinese headers.
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{"时间", "类型", "说明", "金额元", "状态", "单号"})
	for i := range items {
		item := &items[i]
		_ = writer.Write([]string{
			item.CreatedAt.In(loc).Format("2006-01-02 15:04:05"),
			walletCSVKindLabel(item.Kind),
			walletCSVDescription(item.Kind),
			csvYuan(item.AmountMicro),
			walletCSVStatus(item.Kind, item.Status),
			item.Reference,
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		common.SysError(common.LogText("marketplace wallet: write export csv: %s", err.Error()))
	}
}
