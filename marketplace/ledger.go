package marketplace

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/model"
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
	// ErrReversalConflict is returned when the seller credit changed state
	// between the reversal's read and its conditional update (for example a
	// concurrent T+7 promotion), so the reversal must roll back (L3).
	ErrReversalConflict = errors.New("marketplace: request changed state during reversal")
)

// PlatformAccount is the ledger account holding the platform's 7% fees.
func PlatformAccount() string { return "platform" }

// BuyerAccount is the ledger account of a user's marketplace spending
// (§3.9). It is informational only: the real buyer balance is users.quota,
// and this account is never part of a seller's withdrawable balance.
func BuyerAccount(userID int64) string { return "buyer:" + strconv.FormatInt(userID, 10) }

// SellerAccount is the ledger account of a user's marketplace income (§3.9).
// Only this account feeds the withdrawable balance.
func SellerAccount(userID int64) string { return "seller:" + strconv.FormatInt(userID, 10) }

// UserAccount is the pre-split combined account ("user:<id>") that used to
// hold both buyer spending and seller income. It is retained so historical
// rows stay readable; new rows always use BuyerAccount or SellerAccount.
//
// Deprecated: use BuyerAccount or SellerAccount.
func UserAccount(userID int64) string { return "user:" + strconv.FormatInt(userID, 10) }

// accountUserID extracts the user id from a "buyer:<id>", "seller:<id>" or
// legacy "user:<id>" ledger account. It reports false for "platform" and for
// malformed values.
func accountUserID(account string) (int64, bool) {
	for _, prefix := range []string{"buyer:", "seller:", "user:"} {
		if !strings.HasPrefix(account, prefix) {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(account, prefix), 10, 64)
		if err != nil || id <= 0 {
			return 0, false
		}
		return id, true
	}
	return 0, false
}

// Settlement is the priced outcome of one successful request (§3.7/§3.9).
type Settlement struct {
	RequestID    string
	BuyerUserID  int64
	SellerUserID int64
	ListingID    int64
	// P3 shadow identity carried onto every ledger row (§4.2). ChannelID equals
	// ListingID (mp_seller_channels.id), KeyID is 0 until multi-key supply, and
	// ModelMultiplierMilli is the listing multiplier used for pricing.
	ChannelID            int64
	KeyID                int64
	Endpoint             string
	ModelMultiplierMilli int64
	PriceVersion         int64
	MultiplierMilli      int64
	TokenDebit           Micro // token charges, buyer-rounded up
	ToolDebit            Micro // Search/Fetch per-call charges (§3.4)
	Now                  time.Time
	Maturity             time.Duration // 0 → DefaultMaturity
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
		RequestID:            s.RequestID,
		ListingID:            s.ListingID,
		ChannelID:            s.ChannelID,
		KeyID:                s.KeyID,
		Endpoint:             s.Endpoint,
		ModelMultiplierMilli: s.ModelMultiplierMilli,
		PriceVersion:         s.PriceVersion,
		MultiplierMilli:      s.MultiplierMilli,
		CreatedAt:            s.Now,
	}
	rows := []LedgerEntry{
		func() LedgerEntry {
			r := base
			r.EntryType = EntryBuyerDebit
			// Buyer spending lives in its own account so it never reduces a
			// seller's withdrawable income (H5).
			r.Account = BuyerAccount(s.BuyerUserID)
			r.AmountMicro = -int64(s.TokenDebit)
			r.Status = LedgerAvailable
			return r
		}(),
		func() LedgerEntry {
			r := base
			r.EntryType = EntrySellerCredit
			r.Account = SellerAccount(s.SellerUserID)
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
		tool.Account = BuyerAccount(s.BuyerUserID)
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
//
// The refund is real money: in the same transaction the buyer's users.quota is
// credited with the full debit (H5), and the refund ledger row is booked to
// buyer:<id>, which is never withdrawable. The seller-credit state flip checks
// RowsAffected so a concurrent T+7 promotion cannot be overwritten (L3).
func Reverse(db *gorm.DB, requestID string, now time.Time) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var rows []LedgerEntry
		if err := lockForUpdate(tx).Where("request_id = ?", requestID).Find(&rows).Error; err != nil {
			return err
		}
		var creditRow *LedgerEntry
		var total int64
		var buyerID int64
		for i := range rows {
			r := &rows[i]
			switch r.EntryType {
			case EntryRefund, EntryReversal, EntryFeeReversal:
				return ErrAlreadyReversed
			case EntryBuyerDebit:
				total += -r.AmountMicro
				if id, ok := accountUserID(r.Account); ok {
					buyerID = id
				}
			case EntryToolDebit:
				total += -r.AmountMicro
				if buyerID == 0 {
					if id, ok := accountUserID(r.Account); ok {
						buyerID = id
					}
				}
			case EntrySellerCredit:
				creditRow = r
			}
		}
		if creditRow == nil || creditRow.Status != LedgerPending ||
			creditRow.AvailableAt == nil || !creditRow.AvailableAt.After(now) {
			return ErrNotReversible
		}
		res := tx.Model(&LedgerEntry{}).
			Where("id = ? AND status = ?", creditRow.ID, LedgerPending).
			Update("status", LedgerReversed)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			// Another writer (T+7 promotion or a competing reversal) changed
			// the row after our read: roll the whole reversal back.
			return ErrReversalConflict
		}
		// Return the money to the buyer's real wallet in this same
		// transaction; the ledger refund alone would leave the wallet short.
		// A gift-funded part goes back to gift_quota so the original funding
		// split is restored (both columns increase, so gift <= quota holds).
		if total > 0 {
			if buyerID == 0 {
				return fmt.Errorf("marketplace: reverse request %s: cannot resolve buyer account", requestID)
			}
			var giftUsed int64
			if err := tx.Model(&Request{}).
				Select("gift_debit_micro").
				Where("request_id = ?", requestID).
				Scan(&giftUsed).Error; err != nil {
				return err
			}
			giftUsed = max(min(giftUsed, total), 0)
			updates := map[string]any{"quota": gorm.Expr("quota + ?", total)}
			if giftUsed > 0 {
				updates["gift_quota"] = gorm.Expr("gift_quota + ?", giftUsed)
			}
			res := tx.Model(&model.User{}).
				Where("id = ?", buyerID).
				Updates(updates)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return fmt.Errorf("marketplace: reverse request %s: buyer %d not found", requestID, buyerID)
			}
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
		// Refunds always land in the buyer account, even when the original
		// settlement predates the split (legacy "user:<id>" rows).
		refundAccount := BuyerAccount(buyerID)
		if buyerID == 0 {
			refundAccount = ""
			for i := range rows {
				if rows[i].EntryType == EntryBuyerDebit {
					refundAccount = rows[i].Account
				}
			}
		}
		compensation := []LedgerEntry{
			{
				RequestID: requestID, EntryType: EntryRefund,
				Account: refundAccount, AmountMicro: total,
				ListingID: creditRow.ListingID, PriceVersion: creditRow.PriceVersion,
				ChannelID: creditRow.ChannelID, KeyID: creditRow.KeyID,
				Endpoint: creditRow.Endpoint, ModelMultiplierMilli: creditRow.ModelMultiplierMilli,
				MultiplierMilli: creditRow.MultiplierMilli,
				Status:          LedgerAvailable, CreatedAt: now,
			},
			{
				RequestID: requestID, EntryType: EntryReversal,
				Account: creditRow.Account, AmountMicro: -int64(credit),
				ListingID: creditRow.ListingID, PriceVersion: creditRow.PriceVersion,
				ChannelID: creditRow.ChannelID, KeyID: creditRow.KeyID,
				Endpoint: creditRow.Endpoint, ModelMultiplierMilli: creditRow.ModelMultiplierMilli,
				MultiplierMilli: creditRow.MultiplierMilli,
				Status:          LedgerReversed, CreatedAt: now,
			},
			{
				RequestID: requestID, EntryType: EntryFeeReversal,
				Account: PlatformAccount(), AmountMicro: -int64(fee),
				ListingID: creditRow.ListingID, PriceVersion: creditRow.PriceVersion,
				ChannelID: creditRow.ChannelID, KeyID: creditRow.KeyID,
				Endpoint: creditRow.Endpoint, ModelMultiplierMilli: creditRow.ModelMultiplierMilli,
				MultiplierMilli: creditRow.MultiplierMilli,
				Status:          LedgerAvailable, CreatedAt: now,
			},
		}
		return tx.Create(&compensation).Error
	})
}

// AccountBalance returns an account's available and pending totals in 微元.
// Reversed rows are excluded from both. Accounts are "seller:<id>" (income,
// the only withdrawable account), "buyer:<id>" (spending, informational only),
// "platform", and legacy "user:<id>" rows from before the split. The buyer's
// real balance is users.quota (§4.5: quota == 微元).
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
