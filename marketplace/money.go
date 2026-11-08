package marketplace

import (
	"errors"
	"math"
	"math/bits"
)

// Micro is an integer amount of money in 微元: 1 元 = 1,000,000 微元.
// All marketplace accounting uses Micro; floating-point money is forbidden
// (MARKETPLACE-DEV.md §2.1).
type Micro int64

const (
	// MicroPerYuan is the number of 微元 in one yuan.
	MicroPerYuan int64 = 1_000_000
	// MicroPerMillionTokens is the divisor converting (tokens × 微元/百万tokens)
	// into 微元.
	MicroPerMillionTokens int64 = 1_000_000
	// PlatformFeePercent is the platform's cut of the buyer debit (§3.2).
	PlatformFeePercent int64 = 7
)

// ErrMoneyOverflow is returned when a money computation would exceed int64.
// Callers must treat it as a hard failure; a saturated or wrapped amount must
// never reach the ledger.
var ErrMoneyOverflow = errors.New("marketplace: money arithmetic overflow")

// ErrMoneyNegative is returned when a money operand that must be non-negative
// is negative.
var ErrMoneyNegative = errors.New("marketplace: negative money operand")

// mulDivCeil returns ceil(a·b / den) for non-negative a, b and positive den,
// using 128-bit intermediate arithmetic. This is the buyer-side 向上取整 rule
// (§3.2). It returns ErrMoneyOverflow when the exact result exceeds int64.
func mulDivCeil(a, b, den int64) (Micro, error) {
	if a < 0 || b < 0 {
		return 0, ErrMoneyNegative
	}
	if den <= 0 {
		return 0, ErrMoneyOverflow
	}
	if a == 0 || b == 0 {
		return 0, nil
	}
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi >= uint64(den) {
		// Quotient would not fit in uint64, let alone int64.
		return 0, ErrMoneyOverflow
	}
	q, r := bits.Div64(hi, lo, uint64(den))
	if r != 0 {
		q++
	}
	if q > math.MaxInt64 {
		return 0, ErrMoneyOverflow
	}
	return Micro(q), nil
}

// TokenCostMicro converts a token count at a 微元/百万tokens unit price into a
// cost in 微元, rounding up (buyer rule, §3.2).
func TokenCostMicro(tokens int64, priceMicroPerMTok int64) (Micro, error) {
	return mulDivCeil(tokens, priceMicroPerMTok, MicroPerMillionTokens)
}

// ToolCostMicro converts a per-call count at a 微元/次 unit price into a cost
// in 微元 (§3.4). Per-call prices are exact integers, so no rounding applies.
func ToolCostMicro(count int64, unitPriceMicro int64) (Micro, error) {
	if count < 0 || unitPriceMicro < 0 {
		return 0, ErrMoneyNegative
	}
	hi, lo := bits.Mul64(uint64(count), uint64(unitPriceMicro))
	if hi != 0 || lo > math.MaxInt64 {
		return 0, ErrMoneyOverflow
	}
	return Micro(lo), nil
}

// SplitPlatformFee splits a buyer debit into the platform fee
// (round-half-up of debit × 7%, §3.2) and the seller credit (debit − fee).
// fee + credit == debit always holds.
func SplitPlatformFee(debit Micro) (fee Micro, credit Micro, err error) {
	if debit < 0 {
		return 0, 0, ErrMoneyNegative
	}
	if int64(debit) > (math.MaxInt64-50)/PlatformFeePercent {
		return 0, 0, ErrMoneyOverflow
	}
	fee = Micro((int64(debit)*PlatformFeePercent + 50) / 100)
	return fee, debit - fee, nil
}
