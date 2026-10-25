package marketplace

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Payout lifecycle (§3.9/§3.11): pending → approved → paid, or pending →
// rejected. A rejected payout is not part of the active-withdrawal set, so its
// amount automatically returns to the withdrawable balance.
//
// Withdrawable balance (可提余额) is defined as:
//
//	AccountBalance(available) − sum(amount of active payouts)
//
// where "active" is every payout in status pending, approved or paid for the
// seller. The subtraction prevents the same money from being withdrawn twice:
// once a payout is requested, that amount is reserved until it is rejected
// (which removes it from the active set) or paid out.
const (
	// MinPayoutMicro is the minimum withdrawal amount: ¥50 = 50,000,000 微元 (§3.9).
	MinPayoutMicro int64 = 50_000_000
	// PayoutWeeklyWindow is the rolling window for the once-per-week rule (§3.9).
	PayoutWeeklyWindow = 7 * 24 * time.Hour
)

var (
	// ErrPayoutBelowMinimum is returned when the requested amount is below ¥50.
	ErrPayoutBelowMinimum = errors.New("marketplace: payout amount below minimum")
	// ErrPayoutTooFrequent is returned when a non-rejected payout already exists
	// inside the rolling 7-day window.
	ErrPayoutTooFrequent = errors.New("marketplace: payout too frequent")
	// ErrPayoutInsufficient is returned when the withdrawable balance is not
	// enough to cover the requested amount.
	ErrPayoutInsufficient = errors.New("marketplace: insufficient available balance")
	// ErrPayoutNotFound is returned when the seller or payout does not exist.
	ErrPayoutNotFound = errors.New("marketplace: payout not found")
	// ErrPayoutNotPending is returned when an approve/reject/mark-paid targets a
	// payout whose status is no longer pending (or approved for mark-paid).
	ErrPayoutNotPending = errors.New("marketplace: payout is not in the expected state")
	// ErrPayoutDuplicate is returned when an idempotency key is reused with
	// different parameters.
	ErrPayoutDuplicate = errors.New("marketplace: duplicate idempotency key")
	// ErrPayoutAccountMissing is returned when the seller has not bound a
	// payout account yet. Sharing is open to everyone, so this is checked at
	// withdrawal time instead of onboarding time.
	ErrPayoutAccountMissing = errors.New("marketplace: payout account is not bound")
)

// activePayoutStatuses are the statuses that reserve withdrawable balance.
func activePayoutStatuses() []string {
	return []string{PayoutPending, PayoutApproved, PayoutPaid}
}

// activePayoutSum returns the total amount of a seller's active payouts.
func activePayoutSum(db *gorm.DB, sellerID int64) (int64, error) {
	var sum int64
	err := db.Model(&Payout{}).
		Where("seller_id = ? AND status IN ?", sellerID, activePayoutStatuses()).
		Select("COALESCE(SUM(amount_micro), 0)").
		Scan(&sum).Error
	return sum, err
}

// AvailableBalance computes the seller's current withdrawable balance (§3.9):
// the seller account's available total minus the sum of active
// (pending/approved/paid) payouts. Buyer spending and refunds live in
// buyer:<id> and never enter this calculation (H5). It is shared by the API
// display and RequestPayout's internal check.
func AvailableBalance(db *gorm.DB, sellerID int64) (Micro, error) {
	available, _, err := AccountBalance(db, SellerAccount(sellerID))
	if err != nil {
		return 0, err
	}
	active, err := activePayoutSum(db, sellerID)
	if err != nil {
		return 0, err
	}
	bal := int64(available) - active
	if bal < 0 {
		bal = 0
	}
	return Micro(bal), nil
}

// RequestPayout applies for a withdrawal (§3.11). Inside one transaction it
// locks the seller row, recomputes the withdrawable balance and deducts the
// requested amount by inserting a pending payout row.
//
// Idempotency: the (idempotency_key) column is unique. A retry with the same
// key and the same seller+amount returns the existing payout with a nil error
// (idempotent success); the same key with different parameters returns
// ErrPayoutDuplicate. The unique index is the concurrency backstop.
func RequestPayout(db *gorm.DB, sellerID, amountMicro int64, idempotencyKey string, now time.Time) (Payout, error) {
	if amountMicro <= 0 {
		return Payout{}, ErrPayoutBelowMinimum
	}
	if amountMicro < MinPayoutMicro {
		return Payout{}, ErrPayoutBelowMinimum
	}
	if idempotencyKey == "" {
		return Payout{}, fmt.Errorf("%w: empty idempotency key", ErrPayoutDuplicate)
	}

	var existing Payout
	err := db.Where("idempotency_key = ?", idempotencyKey).First(&existing).Error
	if err == nil {
		if existing.SellerID == sellerID && existing.AmountMicro == amountMicro {
			return existing, nil // idempotent replay
		}
		return Payout{}, ErrPayoutDuplicate
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Payout{}, err
	}

	var created Payout
	err = db.Transaction(func(tx *gorm.DB) error {
		// Lock the seller row so concurrent requests for the same seller
		// serialize their balance recomputation (§3.11).
		var seller Seller
		if err := lockForUpdate(tx).First(&seller, "user_id = ?", sellerID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPayoutNotFound
			}
			return err
		}
		// Sharing is open to every account; the payout account is what money
		// can actually be sent to, so it is required here rather than at
		// onboarding (§3.1 open sharing).
		if strings.TrimSpace(seller.PayoutAccount) == "" {
			return ErrPayoutAccountMissing
		}

		// Weekly frequency: no non-rejected payout may already exist in the
		// rolling 7-day window. "already exist" means created strictly before
		// this request's instant (created_at < now), so a payout created by a
		// concurrent request at the very same instant is not counted against
		// this one — the balance check below is then the binding constraint.
		windowStart := now.Add(-PayoutWeeklyWindow)
		var freqCount int64
		if err := tx.Model(&Payout{}).
			Where("seller_id = ? AND status <> ? AND created_at >= ? AND created_at < ?",
				sellerID, PayoutRejected, windowStart, now).
			Count(&freqCount).Error; err != nil {
			return err
		}
		if freqCount > 0 {
			return ErrPayoutTooFrequent
		}

		// Recompute the withdrawable balance inside the locked transaction and
		// verify it can cover the request.
		available, _, err := AccountBalance(tx, SellerAccount(sellerID))
		if err != nil {
			return err
		}
		active, err := activePayoutSum(tx, sellerID)
		if err != nil {
			return err
		}
		if int64(available)-active < amountMicro {
			return ErrPayoutInsufficient
		}

		p := Payout{
			SellerID:       sellerID,
			AmountMicro:    amountMicro,
			Status:         PayoutPending,
			IdempotencyKey: idempotencyKey,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(&p).Error; err != nil {
			// Unique-violation race: another request won the idempotency key.
			var again Payout
			if err2 := tx.Where("idempotency_key = ?", idempotencyKey).First(&again).Error; err2 == nil {
				if again.SellerID == sellerID && again.AmountMicro == amountMicro {
					created = again
					return nil
				}
				return ErrPayoutDuplicate
			}
			return err
		}
		created = p
		return nil
	})
	if err != nil {
		return Payout{}, err
	}
	return created, nil
}

// approveOrReject flips a payout from pending to the target status using a
// conditional UPDATE ... WHERE status='pending'. It returns ErrPayoutNotFound
// when the payout does not exist and ErrPayoutNotPending when the affected row
// count is not exactly one (the status already changed). Rejecting never
// refunds twice: the conditional update is the guarantee.
func approveOrReject(db *gorm.DB, payoutID, reviewerID int64, now time.Time, target string) error {
	res := db.Model(&Payout{}).
		Where("id = ? AND status = ?", payoutID, PayoutPending).
		Updates(map[string]any{
			"status":      target,
			"reviewer_id": reviewerID,
			"reviewed_at": now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// Distinguish "not found" from "not pending".
		var count int64
		if err := db.Model(&Payout{}).Where("id = ?", payoutID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return ErrPayoutNotFound
		}
		return ErrPayoutNotPending
	}
	return nil
}

// ApprovePayout approves a pending payout (§3.11). The amount stays reserved in
// the active-withdrawal set until MarkPayoutPaid.
func ApprovePayout(db *gorm.DB, payoutID, reviewerID int64, now time.Time) error {
	return approveOrReject(db, payoutID, reviewerID, now, PayoutApproved)
}

// RejectPayout rejects a pending payout (§3.11). The rejected payout drops out
// of the active-withdrawal set, so its amount returns to the withdrawable
// balance automatically. A second rejection hits the conditional update and
// returns ErrPayoutNotPending without refunding twice.
func RejectPayout(db *gorm.DB, payoutID, reviewerID int64, now time.Time) error {
	return approveOrReject(db, payoutID, reviewerID, now, PayoutRejected)
}

// MarkPayoutPaid confirms an approved payout was paid out (后台确认打款), moving
// approved → paid via a conditional update. The amount remains reserved (paid
// is still in the active set), so it cannot be withdrawn again.
func MarkPayoutPaid(db *gorm.DB, payoutID int64, now time.Time) error {
	res := db.Model(&Payout{}).
		Where("id = ? AND status = ?", payoutID, PayoutApproved).
		Updates(map[string]any{
			"status": PayoutPaid,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var count int64
		if err := db.Model(&Payout{}).Where("id = ?", payoutID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return ErrPayoutNotFound
		}
		return ErrPayoutNotPending
	}
	return nil
}
