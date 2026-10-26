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
	paidOnly     bool
	settled      bool
	refunded     bool
	settledMicro Micro // amount actually charged by the winning Settle/SettleAtomic
	settledGift  Micro // part of settledMicro paid with gifted balance
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
	return NewBillingSessionWithPolicy(db, buyerUserID, preConsume, false)
}

// NewBillingSessionWithPolicy opens a pre-freeze session under an explicit
// funding policy. paidOnly=true (a seller channel) freezes only the paid part
// of users.quota, so gifted balance can never be spent there; paidOnly=false
// (an official channel) may use the whole balance including gifts.
func NewBillingSessionWithPolicy(db *gorm.DB, buyerUserID int64, preConsume Micro, paidOnly bool) (*BillingSession, error) {
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
		ok, err := reserveBuyerQuota(uid, int(preConsume), paidOnly)
		if err != nil {
			return nil, fmt.Errorf("marketplace: pre-freeze reserve: %w", err)
		}
		if !ok {
			return nil, ErrInsufficientBalance
		}
	}
	return &BillingSession{db: db, buyerUserID: uid, preConsumed: preConsume, paidOnly: paidOnly}, nil
}

// reserveBuyerQuota performs the atomic freeze for the session's funding
// policy: paid-only on seller channels, the whole wallet on official channels.
func reserveBuyerQuota(uid, amount int, paidOnly bool) (bool, error) {
	if paidOnly {
		return model.TryReserveUserPaidQuota(uid, amount)
	}
	return model.TryReserveUserQuota(uid, amount)
}

// Settle reconciles the pre-freeze against the actual debit actualQuota
// (微元, computed by Quote and passed in through the seam):
//
//   - actual < preConsumed: the difference is refunded to users.quota;
//   - actual >= preConsumed: the charge is capped at preConsumed. A request
//     never costs more than the amount frozen before it started; the excess is
//     borne by the platform (it is recorded by SettleRelay as CappedMicro);
//   - the gifted part of the charge is deducted from users.gift_quota, the
//     paid part is the remainder (settleInTx).
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
//   - actual > preConsumed: charged == preConsumed (the settlement cap); the
//     excess is never collected. A defensive log records such a call.
//
// All quota movement is a direct conditional UPDATE inside tx, so it is atomic
// with the caller's ledger writes and never crosses the transaction boundary.
// Once settled (or refunded) the session replays its recorded charge and moves
// no money. SettledGift reports the gifted part of the winning charge.
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
	var giftUsed Micro
	charged, giftUsed, err = s.settleInTx(tx, actual)
	if err != nil {
		return 0, err
	}
	s.settled = true
	s.settledMicro = charged
	s.settledGift = giftUsed
	return charged, nil
}

// SettledGift returns the gifted part of the amount recorded by the winning
// Settle/SettleAtomic (0 before settlement or after a refund). It is read
// while the caller holds no session lock and is therefore safe to call right
// after SettleAtomic returns.
func (s *BillingSession) SettledGift() Micro {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settledGift
}

// PaidOnly reports whether this session may only spend the paid part of the
// wallet (seller channel).
func (s *BillingSession) PaidOnly() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paidOnly
}

// settleInTx performs the quota reconciliation against tx. The caller holds
// s.mu. The settled amount is capped at the pre-freeze: the platform absorbs
// any excess rather than charging the buyer beyond what was frozen.
func (s *BillingSession) settleInTx(tx *gorm.DB, actual Micro) (charged, giftUsed Micro, err error) {
	if actual > s.preConsumed {
		common.SysError(common.LogText(
			"marketplace: settle amount %d exceeds pre-freeze %d (userId=%d); charging pre-freeze only",
			int64(actual), int64(s.preConsumed), s.buyerUserID))
		actual = s.preConsumed
	}
	// Gift is not reserved at freeze time (P0: a concurrent oversell is a
	// platform loss, never a cash-out path), so the split is decided here from
	// the gift balance actually present. Decrement it before the refund so the
	// users.quota / gift_quota invariant is evaluated against the final balance.
	giftUsed, err = s.deductGift(tx, actual)
	if err != nil {
		return 0, 0, err
	}
	refund := s.preConsumed - actual
	if refund > 0 {
		res := tx.Model(&model.User{}).
			Where("id = ?", s.buyerUserID).
			Update("quota", gorm.Expr("quota + ?", int64(refund)))
		if res.Error != nil {
			return 0, 0, fmt.Errorf("marketplace: settle refund difference: %w", res.Error)
		}
		if res.RowsAffected != 1 {
			return 0, 0, fmt.Errorf("marketplace: settle refund difference: user %d not found", s.buyerUserID)
		}
	}
	return actual, giftUsed, nil
}

// deductGift subtracts min(actual, gift_quota) from users.gift_quota and
// returns the amount actually taken. The conditional UPDATE is the race guard:
// if a concurrent settlement already consumed the gift, no gift is booked for
// this request (the charge stays paid, which can only reduce cash-out risk).
func (s *BillingSession) deductGift(tx *gorm.DB, actual Micro) (Micro, error) {
	if actual <= 0 || s.paidOnly {
		// A seller channel settles entirely against paid balance; gift was never
		// part of its freeze and must stay untouched.
		return 0, nil
	}
	var gift int64
	if err := tx.Model(&model.User{}).Where("id = ?", s.buyerUserID).
		Select("gift_quota").Scan(&gift).Error; err != nil {
		return 0, fmt.Errorf("marketplace: settle read gift quota: %w", err)
	}
	giftUsed := Micro(min(int64(actual), max(gift, 0)))
	if giftUsed <= 0 {
		return 0, nil
	}
	res := tx.Model(&model.User{}).
		Where("id = ? AND gift_quota >= ?", s.buyerUserID, int64(giftUsed)).
		Update("gift_quota", gorm.Expr("gift_quota - ?", int64(giftUsed)))
	if res.Error != nil {
		return 0, fmt.Errorf("marketplace: settle deduct gift quota: %w", res.Error)
	}
	if res.RowsAffected != 1 {
		common.SysError(common.LogText(
			"marketplace: settle gift race (userId=%d, wanted=%d); booking the charge as paid",
			s.buyerUserID, int64(giftUsed)))
		return 0, nil
	}
	return giftUsed, nil
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
	_, _, err := s.settleInTx(tx, 0)
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
	ok, err := reserveBuyerQuota(s.buyerUserID, int(delta), s.paidOnly)
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
	origin, coefficient := pricingContext(db, vendor, modelName)
	return Quote(db, cal, QuoteInput{
		Model:            modelName,
		Vendor:           vendor,
		OriginVendor:     origin,
		CoefficientMilli: coefficient,
		MultiplierMilli:  multiplierMilli,
		At:               at,
		InputTokens:      estInputTokens,
		OutputTokens:     maxOutputTokens,
		SearchCount:      searchCap,
		FetchCount:       fetchCap,
	})
}

// channelAllowsGift reports whether a listing's channel is an official
// (platform-registered) channel, where gifted balance (redemption codes,
// invitation transfers, admin grants) may be spent. Plan and seller-defined
// third-party channels are paid-only. An unknown channel fails closed.
func channelAllowsGift(db *gorm.DB, vendor string) bool {
	if db == nil {
		return false
	}
	ch, ok := ChannelPoolBySlug(db, vendor)
	return ok && ch.Kind == ChannelKindOfficial
}

// pricingContext resolves the official price owner and the channel price
// coefficient for one (channel slug, standard model) pair
// (MODEL-CHANNEL-POOL §3.5). Missing pool rows fall back to the channel slug as
// the price vendor and 1.0, which keeps legacy callers and tests working.
func pricingContext(db *gorm.DB, channelSlug, modelName string) (originVendor string, coefficientMilli int64) {
	originVendor = channelSlug
	coefficientMilli = 1000
	if db == nil {
		return originVendor, coefficientMilli
	}
	if model, ok := ModelPoolByName(db, modelName); ok && strings.TrimSpace(model.OriginVendor) != "" {
		originVendor = model.OriginVendor
	}
	if cm, ok := ChannelModelFor(db, channelSlug, modelName); ok && cm.CoefficientMilli > 0 {
		coefficientMilli = cm.CoefficientMilli
	}
	return originVendor, coefficientMilli
}
