package marketplace

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// DefaultMaturity is the T+7 freeze on seller income (§3.9): a seller credit
// becomes withdrawable seven days after the request settles.
const DefaultMaturity = 7 * 24 * time.Hour

var (
	// ErrSettlementConflict is returned when a request id is settled again
	// with different figures; an identical replay is a no-op instead.
	ErrSettlementConflict = errors.New("marketplace: conflicting settlement for request id")
	// ErrNotReversible is returned when a request is outside the reversal
	// window (seller credit already matured) or does not exist.
	ErrNotReversible = errors.New("marketplace: request is not reversible")
	// ErrAlreadyReversed is returned on a second reversal of the same request.
	ErrAlreadyReversed = errors.New("marketplace: request already reversed")
	// ErrInvalidSettlement is returned for structurally invalid input.
	ErrInvalidSettlement = errors.New("marketplace: invalid settlement")
)

// PlatformAccount is the ledger account holding the platform's 7% fees.
func PlatformAccount() string { return "platform" }

// UserAccount is the ledger account of a buyer or seller.
func UserAccount(userID int64) string { return "user:" + strconv.FormatInt(userID, 10) }

// Settlement is the priced outcome of one successful request (§3.7/§3.9).
type Settlement struct {
	RequestID       string
	BuyerUserID     int64
	SellerUserID    int64
	ListingID       int64
	PriceVersion    int64
	MultiplierMilli int64
	TokenDebit      Micro // token charges, buyer-rounded up
	ToolDebit       Micro // Search/Fetch per-call charges (§3.4)
	Now             time.Time
	Maturity        time.Duration // 0 → DefaultMaturity
}

func (s Settlement) validate() error {
	if s.RequestID == "" || s.BuyerUserID <= 0 || s.SellerUserID <= 0 || s.ListingID <= 0 {
		return fmt.Errorf("%w: request id, buyer, seller and listing are required", ErrInvalidSettlement)
	}
	if s.TokenDebit < 0 || s.ToolDebit < 0 {
		return fmt.Errorf("%w: negative debit", ErrInvalidSettlement)
	}
	if int64(s.TokenDebit) > (1<<63-1)-int64(s.ToolDebit) {
		return fmt.Errorf("%w: debit overflow", ErrInvalidSettlement)
	}
	return nil
}

// TotalDebit is the full buyer charge: token debit + per-call tool debit.
func (s Settlement) TotalDebit() Micro { return s.TokenDebit + s.ToolDebit }

// rows builds the ledger rows of the settlement. Row set:
//
//	buyer_debit   buyer    −TokenDebit          available
//	tool_debit    buyer    −ToolDebit           available (only when ToolDebit > 0)
//	seller_credit seller   +(total − fee)       pending, available_at = Now + Maturity
//	platform_fee  platform +fee                 available
//
// Signed amounts per request sum to zero; buyer_debit + tool_debit =
// seller_credit + platform_fee (§3.9 identity).
func (s Settlement) rows() ([]LedgerEntry, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	maturity := s.Maturity
	if maturity == 0 {
		maturity = DefaultMaturity
	}
	total := s.TotalDebit()
	fee, credit, err := SplitPlatformFee(total)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSettlement, err)
	}
	availableAt := s.Now.Add(maturity)
	base := LedgerEntry{
		RequestID:       s.RequestID,
		ListingID:       s.ListingID,
		PriceVersion:    s.PriceVersion,
		MultiplierMilli: s.MultiplierMilli,
		CreatedAt:       s.Now,
	}
	rows := []LedgerEntry{
		func() LedgerEntry {
			r := base
			r.EntryType = EntryBuyerDebit
			r.Account = UserAccount(s.BuyerUserID)
			r.AmountMicro = -int64(s.TokenDebit)
			r.Status = LedgerAvailable
			return r
		}(),
		func() LedgerEntry {
			r := base
			r.EntryType = EntrySellerCredit
			r.Account = UserAccount(s.SellerUserID)
			r.AmountMicro = int64(credit)
			r.Status = LedgerPending
			r.AvailableAt = &availableAt
			return r
		}(),
		func() LedgerEntry {
			r := base
			r.EntryType = EntryPlatformFee
			r.Account = PlatformAccount()
			r.AmountMicro = int64(fee)
			r.Status = LedgerAvailable
			return r
		}(),
	}
	if s.ToolDebit > 0 {
		tool := base
		tool.EntryType = EntryToolDebit
		tool.Account = UserAccount(s.BuyerUserID)
		tool.AmountMicro = -int64(s.ToolDebit)
		tool.Status = LedgerAvailable
		rows = append(rows, tool)
	}
	return rows, nil
}

// sameRows reports whether the stored rows are exactly the settlement's rows
// (same multiset of type/account/amount/status), making a replay idempotent.
func sameRows(stored []LedgerEntry, built []LedgerEntry) bool {
	if len(stored) != len(built) {
		return false
	}
	type key struct {
		typ, account, status string
		amount               int64
	}
	counts := make(map[key]int, len(stored))
	for _, r := range stored {
		counts[key{r.EntryType, r.Account, r.Status, r.AmountMicro}]++
	}
	for _, r := range built {
		k := key{r.EntryType, r.Account, r.Status, r.AmountMicro}
		counts[k]--
		if counts[k] < 0 {
			return false
		}
	}
	return true
}

// PostSettlement writes one request's ledger rows in a single transaction.
// It is idempotent: replaying the same request id with the same figures is a
// no-op; replaying with different figures returns ErrSettlementConflict. The
// (request_id, entry_type) unique index is the concurrency backstop.
func PostSettlement(db *gorm.DB, s Settlement) error {
	built, err := s.rows()
	if err != nil {
		return err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		var stored []LedgerEntry
		if err := tx.Where("request_id = ?", s.RequestID).Find(&stored).Error; err != nil {
			return err
		}
		if len(stored) > 0 {
			if sameRows(stored, built) {
				return nil
			}
			return ErrSettlementConflict
		}
		return tx.Create(&built).Error
	})
	if err == nil {
		return nil
	}
	// Unique-violation race: under REPEATABLE READ the aborted transaction's
	// snapshot no longer sees the other transaction's committed rows, so the
	// replay check must re-read in a fresh transaction/connection. (SQLite's
	// single-writer model makes the in-transaction recheck sufficient there.)
	if isDuplicateKeyError(err) {
		var again []LedgerEntry
		if err2 := db.Where("request_id = ?", s.RequestID).Find(&again).Error; err2 == nil && len(again) > 0 {
			if sameRows(again, built) {
				return nil
			}
			return ErrSettlementConflict
		}
	}
	return err
}

// PromoteAvailable matures pending seller credits whose available_at has
// passed (T+7 job, §3.9). It returns the number of rows promoted.
func PromoteAvailable(db *gorm.DB, now time.Time) (int64, error) {
	res := db.Model(&LedgerEntry{}).
		Where("status = ? AND available_at IS NOT NULL AND available_at <= ?", LedgerPending, now).
		Update("status", LedgerAvailable)
	return res.RowsAffected, res.Error
}

// Reverse cancels a settled request inside its freeze window (§3.9 冲正):
//
//	seller_credit → reversed (never matures)
//	refund        buyer    +total      available
//	reversal      seller   −credit     reversed (paired exclusion with the credit)
//	fee_reversal  platform −fee        available
//
// After a reversal every account nets to zero and the active rows of the
// request still sum to zero. Reversing a matured or unknown request returns
// ErrNotReversible; reversing twice returns ErrAlreadyReversed.
func Reverse(db *gorm.DB, requestID string, now time.Time) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var rows []LedgerEntry
		if err := lockForUpdate(tx).Where("request_id = ?", requestID).Find(&rows).Error; err != nil {
			return err
		}
		var creditRow *LedgerEntry
		var total int64
		for i := range rows {
			r := &rows[i]
			switch r.EntryType {
			case EntryRefund, EntryReversal, EntryFeeReversal:
				return ErrAlreadyReversed
			case EntryBuyerDebit:
				total += -r.AmountMicro
			case EntryToolDebit:
				total += -r.AmountMicro
			case EntrySellerCredit:
				creditRow = r
			}
		}
		if creditRow == nil || creditRow.Status != LedgerPending ||
			creditRow.AvailableAt == nil || !creditRow.AvailableAt.After(now) {
			return ErrNotReversible
		}
		if err := tx.Model(&LedgerEntry{}).
			Where("id = ? AND status = ?", creditRow.ID, LedgerPending).
			Update("status", LedgerReversed).Error; err != nil {
			return err
		}
		var fee Micro
		var credit Micro
		for i := range rows {
			switch rows[i].EntryType {
			case EntryPlatformFee:
				fee = Micro(rows[i].AmountMicro)
			case EntrySellerCredit:
				credit = Micro(rows[i].AmountMicro)
			}
		}
		buyerAccount := ""
		for i := range rows {
			if rows[i].EntryType == EntryBuyerDebit {
				buyerAccount = rows[i].Account
			}
		}
		compensation := []LedgerEntry{
			{
				RequestID: requestID, EntryType: EntryRefund,
				Account: buyerAccount, AmountMicro: total,
				ListingID: creditRow.ListingID, PriceVersion: creditRow.PriceVersion,
				MultiplierMilli: creditRow.MultiplierMilli,
				Status:          LedgerAvailable, CreatedAt: now,
			},
			{
				RequestID: requestID, EntryType: EntryReversal,
				Account: creditRow.Account, AmountMicro: -int64(credit),
				ListingID: creditRow.ListingID, PriceVersion: creditRow.PriceVersion,
				MultiplierMilli: creditRow.MultiplierMilli,
				Status:          LedgerReversed, CreatedAt: now,
			},
			{
				RequestID: requestID, EntryType: EntryFeeReversal,
				Account: PlatformAccount(), AmountMicro: -int64(fee),
				ListingID: creditRow.ListingID, PriceVersion: creditRow.PriceVersion,
				MultiplierMilli: creditRow.MultiplierMilli,
				Status:          LedgerAvailable, CreatedAt: now,
			},
		}
		return tx.Create(&compensation).Error
	})
}

// AccountBalance returns an account's available and pending totals in 微元.
// Reversed rows are excluded from both. Buyer balances are informational only;
// the buyer's real balance is users.quota (§4.5: quota == 微元).
func AccountBalance(db *gorm.DB, account string) (available Micro, pending Micro, err error) {
	type sumRow struct {
		Status string
		Total  int64
	}
	var sums []sumRow
	if err = db.Model(&LedgerEntry{}).
		Select("status, COALESCE(SUM(amount_micro), 0) AS total").
		Where("account = ? AND status <> ?", account, LedgerReversed).
		Group("status").Scan(&sums).Error; err != nil {
		return 0, 0, err
	}
	for _, s := range sums {
		switch s.Status {
		case LedgerAvailable:
			available = Micro(s.Total)
		case LedgerPending:
			pending = Micro(s.Total)
		}
	}
	return available, pending, nil
}
