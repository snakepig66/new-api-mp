package marketplace

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// ErrInsufficientBalance is returned when the buyer's users.quota cannot cover
// a pre-freeze or a reserve top-up (§3.7: 余额不足报错).
var ErrInsufficientBalance = errors.New("marketplace: insufficient balance")

// DefaultToolPreFreezeCap is the default per-request upper bound of
// Search/Fetch calls folded into the pre-freeze amount (§3.4: 预冻结额外按每
// 请求最多 N 次，默认 10，可配).
const DefaultToolPreFreezeCap = 10

// ToolPreFreezeCap returns the configured Search/Fetch pre-freeze cap: the
// MP_TOOL_PREFREEZE_CAP environment variable when it holds a positive
// integer, DefaultToolPreFreezeCap otherwise.
func ToolPreFreezeCap() int {
	raw := strings.TrimSpace(os.Getenv("MP_TOOL_PREFREEZE_CAP"))
	if raw == "" {
		return DefaultToolPreFreezeCap
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return DefaultToolPreFreezeCap
	}
	return n
}

// BillingSession is the marketplace pre-freeze/settle session for one buyer
// request (§3.7). It implements the upstream relaycommon.BillingSettler seam
// so the relay layer can drive marketplace billing exactly like upstream
// billing.
//
// Lifecycle: NewBillingSession atomically pre-freezes the estimated amount
// out of users.quota; Settle reconciles against the actual debit computed by
// the single Quote function (§3.7 展示价/扣费/记录共用同一算价函数); Refund
// returns the whole pre-freeze on the failure path (§3.7 失败不扣费). All
// quota movement goes through the model package's atomic helpers
// (model.TryReserveUserQuota / model.IncreaseUserQuota) so the Redis quota
// cache stays consistent and users.quota can never go negative.
//
// A marketplace 微元 (Micro) is numerically identical to one upstream quota
// unit (§4.5: QUOTA_PER_UNIT=1000000), so amounts cross the seam by direct
// integer conversion.
//
// Concurrency: all methods are safe for concurrent use (streaming settle
// racing a timeout refund); the mutex makes Settle/Refund mutually exclusive
// and idempotent.
type BillingSession struct {
	db           *gorm.DB
	buyerUserID  int
	preConsumed  Micro
	settled      bool
	refunded     bool
	settledMicro Micro // amount actually charged by the winning Settle/SettleAtomic
	mu           sync.Mutex
}

// Compile-time proof that BillingSession satisfies the upstream seam.
var _ relaycommon.BillingSettler = (*BillingSession)(nil)

// NewBillingSession atomically pre-freezes preConsume 微元 from the buyer's
// users.quota and returns the session. It returns ErrInsufficientBalance when
// the balance cannot cover the freeze (the atomic reserve leaves the balance
// untouched in that case), ErrMoneyNegative for a negative amount, and a
// range error when an identifier or amount does not fit int.
//
// db is retained on the session for marketplace-side records written by later
// phases (§3.8/§3.9); quota movement itself goes through the model package,
// which owns the primary handle and the quota cache.
func NewBillingSession(db *gorm.DB, buyerUserID int64, preConsume Micro) (*BillingSession, error) {
	if preConsume < 0 {
		return nil, fmt.Errorf("%w: pre-freeze amount must be non-negative", ErrMoneyNegative)
	}
	if buyerUserID < 0 || buyerUserID > int64(math.MaxInt) {
		return nil, fmt.Errorf("marketplace: buyer user id %d out of int range", buyerUserID)
	}
	if int64(preConsume) > int64(math.MaxInt) {
		return nil, fmt.Errorf("marketplace: pre-freeze amount %d out of int range", int64(preConsume))
	}
	uid := int(buyerUserID)
	if preConsume > 0 {
		ok, err := model.TryReserveUserQuota(uid, int(preConsume))
		if err != nil {
			return nil, fmt.Errorf("marketplace: pre-freeze reserve: %w", err)
		}
		if !ok {
			return nil, ErrInsufficientBalance
		}
	}
	return &BillingSession{db: db, buyerUserID: uid, preConsumed: preConsume}, nil
}

// Settle reconciles the pre-freeze against the actual debit actualQuota
// (微元, computed by Quote and passed in through the seam):
//
//   - actual < preConsumed: the difference is refunded to users.quota;
//   - actual > preConsumed: the difference is topped up. When the balance
//     cannot cover the top-up the session settles for what could actually be
//     deducted (the balance is drained to exactly zero, never negative) and
//     the shortfall is logged via common.SysError;
//   - actual == preConsumed: no quota movement.
//
// Settle is idempotent: once settled or refunded it returns nil without
// moving quota again. It wraps SettleAtomic in its own transaction, so the
// quota movement is atomic and never negative.
func (s *BillingSession) Settle(actualQuota int) error {
	if actualQuota < 0 {
		return fmt.Errorf("%w: settle amount must be non-negative", ErrMoneyNegative)
	}
	db := s.quotaDB()
	if db == nil {
		return errors.New("marketplace: settle: no database handle")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		_, err := s.SettleAtomic(tx, Micro(actualQuota))
		return err
	})
}

// SettleAtomic reconciles the pre-freeze against actual inside the caller's
// transaction (H3): the ledger rows, the request record and users.quota must
// commit or roll back together. It returns the amount actually charged, which
// is what the caller must book as the buyer debit:
//
//   - actual <= preConsumed: the difference is credited back to users.quota;
//     charged == actual.
//   - actual > preConsumed: the difference is deducted from users.quota. When
//     the balance cannot cover it, the remaining balance is drained to exactly
//     zero and charged == preConsumed + drained (< actual); the shortfall is
//     logged and the caller books only the money actually received.
//
// All quota movement is a direct conditional UPDATE inside tx, so it is atomic
// with the caller's ledger writes and never crosses the transaction boundary.
// Once settled (or refunded) the session replays its recorded charge and moves
// no money.
func (s *BillingSession) SettleAtomic(tx *gorm.DB, actual Micro) (charged Micro, err error) {
	if tx == nil {
		return 0, errors.New("marketplace: settle requires a transaction")
	}
	if actual < 0 {
		return 0, fmt.Errorf("%w: settle amount must be non-negative", ErrMoneyNegative)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refunded {
		return 0, nil
	}
	if s.settled {
		return s.settledMicro, nil
	}
	charged, err = s.settleInTx(tx, actual)
	if err != nil {
		return 0, err
	}
	s.settled = true
	s.settledMicro = charged
	return charged, nil
}

// settleInTx performs the quota reconciliation against tx. The caller holds
// s.mu.
func (s *BillingSession) settleInTx(tx *gorm.DB, actual Micro) (Micro, error) {
	if actual <= s.preConsumed {
		refund := s.preConsumed - actual
		if refund > 0 {
			res := tx.Model(&model.User{}).
				Where("id = ?", s.buyerUserID).
				Update("quota", gorm.Expr("quota + ?", int64(refund)))
			if res.Error != nil {
				return 0, fmt.Errorf("marketplace: settle refund difference: %w", res.Error)
			}
			if res.RowsAffected != 1 {
				return 0, fmt.Errorf("marketplace: settle refund difference: user %d not found", s.buyerUserID)
			}
		}
		return actual, nil
	}

	delta := actual - s.preConsumed
	res := tx.Model(&model.User{}).
		Where("id = ? AND quota >= ?", s.buyerUserID, int64(delta)).
		Update("quota", gorm.Expr("quota - ?", int64(delta)))
	if res.Error != nil {
		return 0, fmt.Errorf("marketplace: settle top-up: %w", res.Error)
	}
	if res.RowsAffected == 1 {
		return actual, nil
	}

	// Insufficient balance: lock the row, read what is left, drain it to
	// exactly zero, and report the charge actually collected.
	var user model.User
	if err := lockForUpdate(tx).Select("id", "quota").
		Where("id = ?", s.buyerUserID).First(&user).Error; err != nil {
		return 0, fmt.Errorf("marketplace: settle shortfall: %w", err)
	}
	drained := 0
	if user.Quota > 0 {
		res := tx.Model(&model.User{}).
			Where("id = ? AND quota >= ?", s.buyerUserID, user.Quota).
			Update("quota", gorm.Expr("quota - ?", user.Quota))
		if res.Error != nil {
			return 0, fmt.Errorf("marketplace: settle shortfall drain: %w", res.Error)
		}
		if res.RowsAffected == 1 {
			drained = user.Quota
		}
	}
	charged := s.preConsumed + Micro(drained)
	common.SysError(common.LogText(
		"marketplace: settle top-up shortfall (userId=%d, need=%d, drained=%d)",
		s.buyerUserID, int64(delta), drained))
	return charged, nil
}

// Refund returns the whole pre-freeze to the buyer's users.quota on the
// request-failure path (§3.7 失败不扣费). It is idempotent and a no-op once
// the session has settled. The *gin.Context is taken per the
// relaycommon.BillingSettler signature but unused: refunding is done
// synchronously so callers can rely on the balance being restored when
// Refund returns.
//
// The refund reuses settleInTx (actual == 0) so it follows the same atomic
// conditional UPDATE as SettleAtomic.
func (s *BillingSession) Refund(c *gin.Context) {
	db := s.quotaDB()
	if db == nil {
		common.SysError(common.LogText(
			"marketplace: refund pre-freeze failed (userId=%d): no database handle", s.buyerUserID))
		return
	}
	// Acquire the connection (Begin) before the session mutex, matching
	// Settle/SettleAtomic's connection-then-mutex order. sqlite pools a single
	// connection; the opposite order would deadlock a racing Settle.
	tx := db.Begin()
	if tx.Error != nil {
		common.SysError(common.LogText(
			"marketplace: refund pre-freeze failed (userId=%d): %s", s.buyerUserID, tx.Error.Error()))
		return
	}
	s.mu.Lock()
	if s.settled || s.refunded {
		s.mu.Unlock()
		_ = tx.Rollback().Error
		return
	}
	_, err := s.settleInTx(tx, 0)
	if err == nil {
		err = tx.Commit().Error
	} else {
		_ = tx.Rollback().Error
	}
	if err != nil {
		s.mu.Unlock()
		common.SysError(common.LogText(
			"marketplace: refund pre-freeze failed (userId=%d, amount=%d): %s",
			s.buyerUserID, int64(s.preConsumed), err.Error()))
		return
	}
	s.refunded = true
	s.mu.Unlock()
}

// quotaDB returns the marketplace handle, falling back to the model package's
// global handle for sessions created without one (tests and legacy callers).
func (s *BillingSession) quotaDB() *gorm.DB {
	if s.db != nil {
		return s.db
	}
	return model.DB
}

// NeedsRefund reports whether the session still holds a pre-freeze that
// neither settled nor refunded (§3.7 failure cleanup check).
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.settled && !s.refunded && s.preConsumed > 0
}

// GetPreConsumedQuota returns the amount currently frozen by this session.
func (s *BillingSession) GetPreConsumedQuota() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int(s.preConsumed)
}

// Reserve raises the frozen amount to targetQuota 微元, top-ping up the
// difference with the same atomic balance check as the initial freeze
// (retry-hop re-freeze, §3.7: 换一家就重新计价、重新冻结). It is a no-op when
// the target does not exceed the current freeze or the session is closed.
func (s *BillingSession) Reserve(targetQuota int) error {
	return s.reserve(targetQuota)
}

// ReserveBeforeSubmit is Reserve with the same semantics; the upstream
// interface splits the two because its wallet path admits debt on the
// settle-time reserve but demands available balance before submission. The
// marketplace wallet never admits debt — every reserve is the strict atomic
// check — so both entry points share one implementation.
func (s *BillingSession) ReserveBeforeSubmit(targetQuota int) error {
	return s.reserve(targetQuota)
}

func (s *BillingSession) reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled || s.refunded {
		return nil
	}
	if targetQuota < 0 {
		return fmt.Errorf("%w: reserve target must be non-negative", ErrMoneyNegative)
	}
	delta := Micro(targetQuota) - s.preConsumed
	if delta <= 0 {
		return nil
	}
	if int64(delta) > int64(math.MaxInt) {
		return fmt.Errorf("marketplace: reserve delta %d out of int range", int64(delta))
	}
	ok, err := model.TryReserveUserQuota(s.buyerUserID, int(delta))
	if err != nil {
		return fmt.Errorf("marketplace: reserve top-up: %w", err)
	}
	if !ok {
		return ErrInsufficientBalance
	}
	s.preConsumed = Micro(targetQuota)
	return nil
}

// PreFreezeQuote computes the pre-freeze amount (§3.7): estimated input
// tokens plus maxTokens × output price plus the Search/Fetch caps
// (searchCap/fetchCap calls each, §3.4), priced at instant at through the
// single Quote function — the same function that prices the actual debit and
// the request record.
//
// maxOutputTokens <= 0 is treated as 0: substituting the model's maximum
// output when the request omits max_tokens (§3.7) — and clamping that
// estimate to the buyer's balance — is the caller's responsibility; this
// function does not guess model limits. Negative caps and token counts are
// clamped to 0.
func PreFreezeQuote(db *gorm.DB, cal *Calendar, vendor, modelName string, multiplierMilli int64, estInputTokens, maxOutputTokens, searchCap, fetchCap int64, at time.Time) (QuoteResult, error) {
	if estInputTokens < 0 {
		estInputTokens = 0
	}
	if maxOutputTokens < 0 {
		maxOutputTokens = 0
	}
	if searchCap < 0 {
		searchCap = 0
	}
	if fetchCap < 0 {
		fetchCap = 0
	}
	return Quote(db, cal, QuoteInput{
		Model:           modelName,
		Vendor:          vendor,
		MultiplierMilli: multiplierMilli,
		At:              at,
		InputTokens:     estInputTokens,
		OutputTokens:    maxOutputTokens,
		SearchCount:     searchCap,
		FetchCount:      fetchCap,
	})
}
