package marketplace

import (
	"fmt"
	"math"
	"math/big"
	"time"

	"gorm.io/gorm"
)

// QuoteInput is one request's usage snapshot for unified pricing (§3.2/§3.7).
// At is the pricing instant — the pre-freeze moment — and decides both the
// peak/off-peak tier and the price-book versions for the whole request,
// including every retry hop (§3.3). Reasoning tokens are folded into
// OutputTokens by the caller (§3.2); Quote does not distinguish them.
type QuoteInput struct {
	Model, Vendor string
	// OriginVendor is the standard model's original vendor; the official
	// price book is keyed by it (MODEL-CHANNEL-POOL §3.5). Empty falls back to
	// Vendor for pre-pool callers.
	OriginVendor string
	// CoefficientMilli is the channel-model price coefficient (1000 = official
	// price). <= 0 is treated as 1000.
	CoefficientMilli int64
	MultiplierMilli  int64 // 倍率千分值, must be 1..1000 (§3.2)
	// ModelMultiplierMilli is the per-model multiplier override (P4). When > 0
	// it replaces MultiplierMilli for this quote; 0 falls back to the listing
	// multiplier.
	ModelMultiplierMilli                                         int64
	At                                                           time.Time // pricing instant = pre-freeze moment (§3.3)
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int64
	SearchCount, FetchCount                                      int64
}

// QuoteResult is the single priced outcome shared by the displayed price, the
// actual debit and the request record (§3.7) — there is no second pricing
// function.
type QuoteResult struct {
	Model, Vendor, Tier string           // Tier is the tier actually priced (flat after fallback)
	OriginVendor        string           // price-book owner used for this quote
	PriceVersion        int64            // token price-book version
	ToolPriceVersions   map[string]int64 // item -> version, only for items with count > 0
	MultiplierMilli     int64
	// ModelMultiplierMilli is the multiplier actually used: the per-model
	// override when one was supplied, otherwise the listing multiplier.
	ModelMultiplierMilli              int64
	CoefficientMilli                  int64
	TokenDebit, ToolDebit, TotalDebit Micro
	OfficialCost                      Micro // same usage at multiplier 1000, for 省了多少 (§3.8)
}

// Quote prices one request at instant in.At (§3.2–§3.4):
//
//	TokenDebit = ceil((in·P_in + out·P_out + cr·P_cr + cw·P_cw) · mult / (1e6 · 1000))
//	ToolDebit  = ceil((search·U_search + fetch·U_fetch) · mult / 1000)
//
// where P_* are the official 微元/百万tokens prices and U_* the official
// 微元/次 unit prices. Each component is rounded up exactly once; the
// intermediate products exceed int64 (tokens × price × mult reaches ~1e20),
// so the arithmetic is exact math/big. OfficialCost is the same usage priced
// at mult = 1000. A tool item with count > 0 but no covering price row fails
// with ErrNoPrice — the caller strips unpriceable tools before relaying
// (§3.4); a zero count is ignored entirely.
func Quote(db *gorm.DB, cal *Calendar, in QuoteInput) (QuoteResult, error) {
	multiplier := in.MultiplierMilli
	if in.ModelMultiplierMilli > 0 {
		multiplier = in.ModelMultiplierMilli
	}
	if multiplier < 1 || multiplier > 1000 {
		return QuoteResult{}, fmt.Errorf("marketplace: multiplier milli %d out of range 1..1000", multiplier)
	}
	if in.InputTokens < 0 || in.OutputTokens < 0 || in.CacheReadTokens < 0 || in.CacheWriteTokens < 0 ||
		in.SearchCount < 0 || in.FetchCount < 0 {
		return QuoteResult{}, fmt.Errorf("%w: quote input counts must be non-negative", ErrMoneyNegative)
	}
	origin := in.OriginVendor
	if origin == "" {
		origin = in.Vendor
	}
	coefficient := in.CoefficientMilli
	if coefficient <= 0 {
		coefficient = 1000
	}
	pb, err := PriceAt(db, in.Model, origin, cal.TierAt(in.At), in.At)
	if err != nil {
		return QuoteResult{}, err
	}

	toolVersions := make(map[string]int64, 2)
	var searchUnit, fetchUnit int64
	if in.SearchCount > 0 {
		unit, version, err := PerCallPriceAt(db, origin, ToolItemSearch, in.At)
		if err != nil {
			return QuoteResult{}, err
		}
		searchUnit = unit
		toolVersions[ToolItemSearch] = version
	}
	if in.FetchCount > 0 {
		unit, version, err := PerCallPriceAt(db, origin, ToolItemFetch, in.At)
		if err != nil {
			return QuoteResult{}, err
		}
		fetchUnit = unit
		toolVersions[ToolItemFetch] = version
	}

	tokenParts := [][2]int64{
		{in.InputTokens, pb.InputMicroPerMTok},
		{in.OutputTokens, pb.OutputMicroPerMTok},
		{in.CacheReadTokens, pb.CacheReadMicroPerMTok},
		{in.CacheWriteTokens, pb.CacheWriteMicroPerMTok},
	}
	toolParts := [][2]int64{
		{in.SearchCount, searchUnit},
		{in.FetchCount, fetchUnit},
	}
	tokenDebit, err := ceilScaledSum(tokenParts, multiplier*coefficient, MicroPerMillionTokens*1000*1000)
	if err != nil {
		return QuoteResult{}, err
	}
	toolDebit, err := ceilScaledSum(toolParts, multiplier*coefficient, 1000*1000)
	if err != nil {
		return QuoteResult{}, err
	}
	officialToken, err := ceilScaledSum(tokenParts, 1000, MicroPerMillionTokens*1000)
	if err != nil {
		return QuoteResult{}, err
	}
	officialTool, err := ceilScaledSum(toolParts, 1000, 1000)
	if err != nil {
		return QuoteResult{}, err
	}
	if int64(tokenDebit) > math.MaxInt64-int64(toolDebit) {
		return QuoteResult{}, ErrMoneyOverflow
	}
	if int64(officialToken) > math.MaxInt64-int64(officialTool) {
		return QuoteResult{}, ErrMoneyOverflow
	}
	return QuoteResult{
		Model:                in.Model,
		Vendor:               in.Vendor,
		OriginVendor:         origin,
		Tier:                 pb.Tier,
		PriceVersion:         pb.Version,
		ToolPriceVersions:    toolVersions,
		MultiplierMilli:      multiplier,
		ModelMultiplierMilli: multiplier,
		CoefficientMilli:     coefficient,
		TokenDebit:           tokenDebit,
		ToolDebit:            toolDebit,
		TotalDebit:           tokenDebit + toolDebit,
		OfficialCost:         officialToken + officialTool,
	}, nil
}

// ceilScaledSum returns ceil((Σ countᵢ·priceᵢ) · mult / den) for non-negative
// counts, prices and mult and a positive den, rounding up exactly once. Exact
// big-integer arithmetic keeps the ~1e20 intermediate products lossless; it
// runs only a handful of times per request, so the cost is irrelevant.
func ceilScaledSum(parts [][2]int64, mult int64, den int64) (Micro, error) {
	sum := new(big.Int)
	for _, p := range parts {
		sum.Add(sum, new(big.Int).Mul(big.NewInt(p[0]), big.NewInt(p[1])))
	}
	sum.Mul(sum, big.NewInt(mult))
	q, r := new(big.Int).QuoRem(sum, big.NewInt(den), new(big.Int))
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, ErrMoneyOverflow
	}
	return Micro(q.Int64()), nil
}
