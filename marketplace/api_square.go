package marketplace

// api_square.go — 模型广场 API (§5 阶段 5): a public read-only catalog of
// every model that currently has live/canary supply, with the buyer-facing
// price of both 高峰/空闲 tiers and the next tier-switch instant.
//
// Pricing contract (§3.2/§3.7): the displayed price IS the charged price, so
// every number here comes out of the single Quote function — a 1e6-token
// usage benchmark per price dimension, at the lowest currently available
// multiplier. There is no second pricing algorithm.
//
// Privacy contract (§3.1/§3.5): the square exposes only aggregates (listing
// count, multiplier range); no seller id, no listing id, no key material.
//
// These are public web-console APIs (no login required): they answer in the
// upstream console envelope (common.ApiSuccess / common.ApiErrorStatus).

import (
	"cmp"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// RegisterSquareRoutes mounts the model-square endpoints on g. The square is
// public read-only (§5 阶段 5): g is expected to be the public /api group,
// without UserAuth.
func RegisterSquareRoutes(g *gin.RouterGroup) {
	registerSquareRoutes(g)
}

func registerSquareRoutes(g *gin.RouterGroup) {
	g.GET("/square/models", listSquareModels)
	g.GET("/square/models/:model", getSquareModel)
}

// squareTierPrices is the buyer price of one tier in 微元/百万 tokens
// (§3.2: 官方价目 × 最低可用倍率, each dimension rounded up exactly once by
// Quote — the same rounding the debit path applies).
type squareTierPrices struct {
	InputMicroPerMTok      int64 `json:"input_micro_per_mtok"`
	OutputMicroPerMTok     int64 `json:"output_micro_per_mtok"`
	CacheReadMicroPerMTok  int64 `json:"cache_read_micro_per_mtok"`
	CacheWriteMicroPerMTok int64 `json:"cache_write_micro_per_mtok"`
}

// squareModelEntry is one model of the square listing (§5 阶段 5). Prices is
// keyed by tier ("peak" / "offpeak"): the entry at Tier was priced at
// GeneratedAt, the other at NextSwitch. NextSwitch is null when the calendar
// shows no flip within the NextSwitch horizon.
type squareModelEntry struct {
	Model        string                      `json:"model"`
	Vendor       string                      `json:"vendor"`
	Tier         string                      `json:"tier"`
	TierLabel    string                      `json:"tier_label"`
	Prices       map[string]squareTierPrices `json:"prices"`
	NextSwitch   *time.Time                  `json:"next_switch"`
	ListingCount int                         `json:"listing_count"`
	GeneratedAt  time.Time                   `json:"generated_at"`
}

// squareOfficialPrices is one tier's raw official price-book row (no flat
// fallback), shown on the model detail view.
type squareOfficialPrices struct {
	squareTierPrices
	Version int64 `json:"version"`
}

// squareModelDetail is the single-model square view: the list entry plus the
// official per-tier price books and the multiplier range of the available
// supply (§5 阶段 5 各时段官方价与倍率区间).
type squareModelDetail struct {
	squareModelEntry
	MultiplierMinMilli int64                           `json:"multiplier_min_milli"`
	MultiplierMaxMilli int64                           `json:"multiplier_max_milli"`
	OfficialPrices     map[string]squareOfficialPrices `json:"official_prices"`
}

// squareSupply is the aggregate of the live/canary listings serving one
// (model, vendor): how many listings and the multiplier band. 可用倍率下限
// takes canary listings into account because canary supply does serve buyer
// traffic (§3.5 灰度 10%).
type squareSupply struct {
	count   int
	minMult int64
	maxMult int64
}

// loadSquareSupply groups the current live/canary supply by (model, vendor).
// The models column is JSON text, so the model filter runs in Go; the live
// listing set is small in v1 (same trade-off as hasLiveListingForModel).
func loadSquareSupply(db *gorm.DB) (map[[2]string]*squareSupply, error) {
	var rows []Listing
	if err := db.Where("status IN ?", []string{ListingLive, ListingCanary}).
		Select("id", "vendor", "models", "multiplier_milli").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	supply := make(map[[2]string]*squareSupply)
	for i := range rows {
		l := &rows[i]
		for _, m := range l.Models {
			k := [2]string{m, l.Vendor}
			s := supply[k]
			if s == nil {
				s = &squareSupply{minMult: l.MultiplierMilli, maxMult: l.MultiplierMilli}
				supply[k] = s
			}
			s.count++
			s.minMult = min(s.minMult, l.MultiplierMilli)
			s.maxMult = max(s.maxMult, l.MultiplierMilli)
		}
	}
	return supply, nil
}

// quoteTierPrices prices one tier through the single Quote function (§3.7):
// each of the four price dimensions is quoted separately with exactly one
// million tokens, so the result is the per-million-token buyer price with the
// same ceil rounding the debit path applies. Tool prices are not part of the
// square display (counts are per-request), so Search/Fetch stay zero.
func quoteTierPrices(db *gorm.DB, cal *Calendar, model, vendor string, multMilli int64, at time.Time) (squareTierPrices, error) {
	var out squareTierPrices
	quoteDim := func(setTokens func(*QuoteInput)) (int64, error) {
		in := QuoteInput{Model: model, Vendor: vendor, MultiplierMilli: multMilli, At: at}
		setTokens(&in)
		q, err := Quote(db, cal, in)
		if err != nil {
			return 0, err
		}
		return int64(q.TotalDebit), nil
	}
	var err error
	if out.InputMicroPerMTok, err = quoteDim(func(in *QuoteInput) { in.InputTokens = MicroPerMillionTokens }); err != nil {
		return squareTierPrices{}, err
	}
	if out.OutputMicroPerMTok, err = quoteDim(func(in *QuoteInput) { in.OutputTokens = MicroPerMillionTokens }); err != nil {
		return squareTierPrices{}, err
	}
	if out.CacheReadMicroPerMTok, err = quoteDim(func(in *QuoteInput) { in.CacheReadTokens = MicroPerMillionTokens }); err != nil {
		return squareTierPrices{}, err
	}
	if out.CacheWriteMicroPerMTok, err = quoteDim(func(in *QuoteInput) { in.CacheWriteTokens = MicroPerMillionTokens }); err != nil {
		return squareTierPrices{}, err
	}
	return out, nil
}

// buildSquareEntry assembles one model's square entry at instant now: the
// current tier, both tiers' buyer prices at the lowest available multiplier
// (the current tier priced at now, the other at NextSwitch(now) — by
// construction the first instant of the other tier), and the switch instant.
// A model whose CURRENT tier cannot be priced is reported ok=false: a listing
// the buyer could not be charged for must not be advertised (§3.2 买家看到
// 的就是最终价). The other tier is simply omitted when it has no covering
// price book.
func buildSquareEntry(db *gorm.DB, cal *Calendar, model, vendor string, s *squareSupply, now time.Time) (squareModelEntry, bool) {
	tier := cal.TierAt(now)
	entry := squareModelEntry{
		Model:        model,
		Vendor:       vendor,
		Tier:         tier,
		TierLabel:    tierLabel(tier),
		Prices:       make(map[string]squareTierPrices, 2),
		ListingCount: s.count,
		GeneratedAt:  now,
	}
	next := cal.NextSwitch(now)
	otherAt := now
	if !next.IsZero() {
		entry.NextSwitch = &next
		otherAt = next
	}
	current, err := quoteTierPrices(db, cal, model, vendor, s.minMult, now)
	if err != nil {
		return squareModelEntry{}, false
	}
	entry.Prices[tier] = current
	if other, err := quoteTierPrices(db, cal, model, vendor, s.minMult, otherAt); err == nil {
		entry.Prices[cal.TierAt(otherAt)] = other
	}
	return entry, true
}

// squareState returns the marketplace database and calendar or answers 500.
func squareState(c *gin.Context) (*gorm.DB, *Calendar) {
	db, cal := relayState()
	if db == nil || cal == nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
		return nil, nil
	}
	return db, cal
}

// listSquareModels serves GET /square/models — every model with live/canary
// supply, sorted by (model, vendor) for a stable page.
func listSquareModels(c *gin.Context) {
	db, cal := squareState(c)
	if db == nil {
		return
	}
	supply, err := loadSquareSupply(db)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query listings"))
		return
	}
	keys := make([][2]string, 0, len(supply))
	for k := range supply {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b [2]string) int {
		if c := cmp.Compare(a[0], b[0]); c != 0 {
			return c
		}
		return cmp.Compare(a[1], b[1])
	})
	now := time.Now()
	items := make([]squareModelEntry, 0, len(keys))
	for _, k := range keys {
		entry, ok := buildSquareEntry(db, cal, k[0], k[1], supply[k], now)
		if !ok {
			common.SysError(common.LogText("marketplace: square: no covering price for %s/%s, hidden from square", k[0], k[1]))
			continue
		}
		items = append(items, entry)
	}
	common.ApiSuccess(c, items)
}

// getSquareModel serves GET /square/models/:model — the list entry plus the
// official per-tier price books and the supply's multiplier range. A model
// with no live/canary listing answers 404.
func getSquareModel(c *gin.Context) {
	db, cal := squareState(c)
	if db == nil {
		return
	}
	modelName := c.Param("model")
	supply, err := loadSquareSupply(db)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query listings"))
		return
	}
	// v1 has exactly one vendor per model. Should several (model, vendor)
	// supply groups ever exist for the same model, the detail view presents
	// the group whose minimum multiplier is lowest — the price the square
	// list advertises for this model — with the vendor id as the deterministic
	// tie-break.
	var pickedKey [2]string
	var picked *squareSupply
	for k, s := range supply {
		if k[0] != modelName {
			continue
		}
		if picked == nil || s.minMult < picked.minMult ||
			(s.minMult == picked.minMult && (k[1] < pickedKey[1])) {
			pickedKey, picked = k, s
		}
	}
	if picked == nil {
		common.ApiErrorStatus(c, http.StatusNotFound, errors.New("model not found"))
		return
	}
	entry, ok := buildSquareEntry(db, cal, pickedKey[0], pickedKey[1], picked, time.Now())
	if !ok {
		common.ApiErrorStatus(c, http.StatusNotFound, errors.New("model not found"))
		return
	}
	detail := squareModelDetail{
		squareModelEntry:   entry,
		MultiplierMinMilli: picked.minMult,
		MultiplierMaxMilli: picked.maxMult,
		OfficialPrices:     make(map[string]squareOfficialPrices, 3),
	}
	// 各时段官方价: raw price-book rows per tier, no flat fallback — a
	// flat-only vendor shows its single flat row, not three copies of it.
	for _, tier := range []string{TierPeak, TierOffpeak, TierFlat} {
		pb, err := priceAt(db, pickedKey[0], pickedKey[1], tier, entry.GeneratedAt)
		if err != nil {
			continue
		}
		detail.OfficialPrices[tier] = squareOfficialPrices{
			squareTierPrices: squareTierPrices{
				InputMicroPerMTok:      pb.InputMicroPerMTok,
				OutputMicroPerMTok:     pb.OutputMicroPerMTok,
				CacheReadMicroPerMTok:  pb.CacheReadMicroPerMTok,
				CacheWriteMicroPerMTok: pb.CacheWriteMicroPerMTok,
			},
			Version: pb.Version,
		}
	}
	common.ApiSuccess(c, detail)
}
