package marketplace

// api_square.go — 模型广场 API (§5 阶段 5 / MODEL-CHANNEL-POOL P5).
//
// The square aggregates by STANDARD MODEL (mp_models.name), not by
// (model, vendor): one public row per active standard model whose origin
// vendor has an official price book. A standard model with no listing at all
// is still advertised at the official price (buyer_price_source="official");
// as soon as at least one live/canary listing serves it, the row carries the
// cheapest buyer price across the listed channels and
// buyer_price_source="market".
//
// Pricing contract (§3.2/§3.7): the displayed price IS the charged price, so
// every number here comes out of the single Quote function — a 1e6-token
// usage benchmark per price dimension, at the cheapest effective factor
// (channel coefficient × listing multiplier). There is no second pricing
// algorithm. The official price book is keyed by the standard model's origin
// vendor (MODEL-CHANNEL-POOL §3.5), so Quote receives OriginVendor and the
// channel's CoefficientMilli.
//
// Privacy contract (§3.1/§3.5): the square exposes only aggregates (listing
// count, channel count, multiplier range); no seller id, no listing id, no
// key material.
//
// These are public web-console APIs (no login required): they answer in the
// upstream console envelope (common.ApiSuccess / common.ApiErrorStatus).

import (
	"errors"
	"net/http"
	"strings"
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

// Buyer price sources of a square row.
const (
	BuyerPriceSourceMarket   = "market"
	BuyerPriceSourceOfficial = "official"
)

// squareTierPrices is the buyer price of one tier in 微元/百万 tokens
// (§3.2: 官方价目 × 渠道系数 × 最低倍率, each dimension rounded up exactly once
// by Quote — the same rounding the debit path applies).
type squareTierPrices struct {
	InputMicroPerMTok      int64 `json:"input_micro_per_mtok"`
	OutputMicroPerMTok     int64 `json:"output_micro_per_mtok"`
	CacheReadMicroPerMTok  int64 `json:"cache_read_micro_per_mtok"`
	CacheWriteMicroPerMTok int64 `json:"cache_write_micro_per_mtok"`
}

// squareModelEntry is one standard model of the square listing. Prices is
// keyed by tier ("peak" / "offpeak"): the entry at Tier was priced at
// GeneratedAt, the other at NextSwitch. NextSwitch is null when the calendar
// shows no flip within the NextSwitch horizon.
//
// Vendor is retained as an alias of OriginVendor so existing frontends keep
// working; new clients should read origin_vendor.
type squareModelEntry struct {
	Model        string                      `json:"model"`
	Aliases      []string                    `json:"aliases"`
	OriginVendor string                      `json:"origin_vendor"`
	Vendor       string                      `json:"vendor"`
	Tier         string                      `json:"tier"`
	TierLabel    string                      `json:"tier_label"`
	Prices       map[string]squareTierPrices `json:"prices"`
	NextSwitch   *time.Time                  `json:"next_switch"`
	ListingCount int                         `json:"listing_count"`
	ChannelCount int                         `json:"channel_count"`
	// MultiplierMinMilli is the lowest multiplier among live/canary supply;
	// 0 when nothing is on sale right now.
	MultiplierMinMilli int64 `json:"multiplier_min_milli"`
	// OfficialAvailable reports whether at least one live/canary listing is
	// served by an official-kind channel with a verified, active qualification
	// for this model. It is what the key detail page uses to warn that
	// official-only routing cannot reach a model.
	OfficialAvailable bool      `json:"official_available"`
	BuyerPriceSource  string    `json:"buyer_price_source"`
	GeneratedAt       time.Time `json:"generated_at"`
	// Public model metadata (mp_models): capability tags and context window.
	Capabilities  []string `json:"capabilities,omitempty"`
	ContextLength int      `json:"context_length,omitempty"`
	// Metrics is the 1h/24h/7d reliability summary derived from mp_requests
	// (see square_metrics.go). Nil when the model has no request history at
	// all — the UI then hides the reliability columns instead of showing 0.
	Metrics *squareMetrics `json:"metrics,omitempty"`
}

// squareOfficialPrices is one tier's raw official price-book row (no flat
// fallback), shown on the model detail view.
type squareOfficialPrices struct {
	squareTierPrices
	Version int64 `json:"version"`
}

// squareChannelView is one sellable channel of a standard model on the detail
// page (§5: 详情页按渠道列挂单, badges 官方/套餐/已验真). A channel with a
// verified qualification is listed even when it has no live supply.
type squareChannelView struct {
	ChannelSlug        string         `json:"channel_slug"`
	Name               string         `json:"name"`
	Kind               string         `json:"kind"`
	Verified           bool           `json:"verified"`
	CoefficientMilli   int64          `json:"coefficient_milli"`
	Differences        map[string]any `json:"differences"`
	ListingCount       int            `json:"listing_count"`
	MinMultiplierMilli int64          `json:"min_multiplier_milli"`
	MaxMultiplierMilli int64          `json:"max_multiplier_milli"`
	// Metrics is the channel's 24h success rate/latency; nil when it served
	// no marketplace request in the window.
	Metrics *squareChannelMetrics `json:"metrics,omitempty"`
}

// squareModelDetail is the single-model square view: the list entry plus the
// official per-tier price books, the multiplier range of the available supply
// and the per-channel sellable qualifications (§5 阶段 5).
type squareModelDetail struct {
	squareModelEntry
	MultiplierMinMilli int64                           `json:"multiplier_min_milli"`
	MultiplierMaxMilli int64                           `json:"multiplier_max_milli"`
	OfficialPrices     map[string]squareOfficialPrices `json:"official_prices"`
	Channels           []squareChannelView             `json:"channels"`
}

// squareChannelSupply is the live/canary listing aggregate of one channel
// serving a standard model.
type squareChannelSupply struct {
	count   int
	minMult int64
	maxMult int64
}

// squareSupply is the aggregate of the live/canary listings serving one
// standard model: how many listings, how many channels, the multiplier band
// and the listing that yields the cheapest buyer price. 可用倍率下限 takes
// canary listings into account because canary supply does serve buyer traffic
// (§3.5 灰度 10%).
type squareSupply struct {
	count         int
	officialCount int
	channels      map[string]struct{}
	minMult       int64
	maxMult       int64
	byChannel     map[string]*squareChannelSupply
	haveBest      bool
	bestSlug      string
	bestMult      int64
	bestCoeff     int64
	bestFactor    int64
}

// modelPoolIndex resolves a listing's model spelling to a standard model
// name. Name matching is exact (case-insensitive); aliases are accepted so a
// legacy listing written with an operator spelling still rolls up under the
// standard row — the square itself always emits the standard name.
type modelPoolIndex struct {
	byName  map[string]string
	byAlias map[string]string
}

// newModelPoolIndex builds the lookup from the active pool rows.
func newModelPoolIndex(pool []ModelPool) modelPoolIndex {
	idx := modelPoolIndex{
		byName:  make(map[string]string, len(pool)),
		byAlias: make(map[string]string),
	}
	for _, m := range pool {
		if name := strings.TrimSpace(m.Name); name != "" {
			idx.byName[strings.ToLower(name)] = m.Name
		}
	}
	for _, m := range pool {
		for _, alias := range parseStringList(m.Aliases) {
			key := strings.ToLower(strings.TrimSpace(alias))
			if key == "" {
				continue
			}
			if _, exists := idx.byAlias[key]; !exists {
				idx.byAlias[key] = m.Name
			}
		}
	}
	return idx
}

// resolve maps a model spelling to the standard model name.
func (idx modelPoolIndex) resolve(model string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(model))
	if key == "" {
		return "", false
	}
	if name, ok := idx.byName[key]; ok {
		return name, true
	}
	if name, ok := idx.byAlias[key]; ok {
		return name, true
	}
	return "", false
}

// loadActiveModelPool returns the active standard models ordered by name.
func loadActiveModelPool(db *gorm.DB) ([]ModelPool, error) {
	var rows []ModelPool
	if err := db.Where("status = ?", PoolStatusActive).Order("name ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// loadChannelModelCoefficients returns model -> channel_slug -> coefficient.
// Missing or malformed rows are simply absent; callers default to 1000.
func loadChannelModelCoefficients(db *gorm.DB) map[string]map[string]int64 {
	out := make(map[string]map[string]int64)
	var rows []ChannelModel
	if err := db.Select("channel_slug", "model", "coefficient_milli").Find(&rows).Error; err != nil {
		return out
	}
	for i := range rows {
		row := &rows[i]
		model := strings.TrimSpace(row.Model)
		slug := strings.TrimSpace(row.ChannelSlug)
		if model == "" || slug == "" {
			continue
		}
		byChannel := out[model]
		if byChannel == nil {
			byChannel = make(map[string]int64)
			out[model] = byChannel
		}
		byChannel[slug] = row.CoefficientMilli
	}
	return out
}

// coefficientFor returns the channel's price coefficient for a standard
// model, defaulting to 1000 (= official price ×1.0) when no usable
// qualification row exists.
func coefficientFor(coeffs map[string]map[string]int64, model, channel string) int64 {
	if byChannel := coeffs[model]; byChannel != nil {
		if milli := byChannel[channel]; milli > 0 {
			return milli
		}
	}
	return 1000
}

// add folds one live/canary listing into the aggregate. factor is the buyer
// price factor in thousandths of the official price:
// coefficient_milli × multiplier_milli (1000 × 1000 = official price).
func (s *squareSupply) add(channel string, multiplierMilli, coefficientMilli int64, official bool) {
	if coefficientMilli <= 0 {
		coefficientMilli = 1000
	}
	if official {
		s.officialCount++
	}
	if s.channels == nil {
		s.channels = make(map[string]struct{})
	}
	if s.byChannel == nil {
		s.byChannel = make(map[string]*squareChannelSupply)
	}
	if s.count == 0 {
		s.minMult, s.maxMult = multiplierMilli, multiplierMilli
	} else {
		s.minMult = min(s.minMult, multiplierMilli)
		s.maxMult = max(s.maxMult, multiplierMilli)
	}
	s.count++
	s.channels[channel] = struct{}{}
	cs := s.byChannel[channel]
	if cs == nil {
		cs = &squareChannelSupply{count: 0, minMult: multiplierMilli, maxMult: multiplierMilli}
		s.byChannel[channel] = cs
	}
	if cs.count == 0 {
		cs.minMult, cs.maxMult = multiplierMilli, multiplierMilli
	} else {
		cs.minMult = min(cs.minMult, multiplierMilli)
		cs.maxMult = max(cs.maxMult, multiplierMilli)
	}
	cs.count++

	// 最低到手价 = official × coefficient × multiplier: pick the listing with
	// the smallest product. Deterministic tie-break keeps the page stable.
	factor := multiplierMilli * coefficientMilli
	if !s.haveBest || factor < s.bestFactor ||
		(factor == s.bestFactor && (coefficientMilli < s.bestCoeff ||
			(coefficientMilli == s.bestCoeff && channel < s.bestSlug))) {
		s.haveBest = true
		s.bestSlug = channel
		s.bestMult = multiplierMilli
		s.bestCoeff = coefficientMilli
		s.bestFactor = factor
	}
}

// loadOfficialChannelModels returns model -> channel slugs that can serve the
// model through an official-kind channel: an active channel whose
// qualification for that model is verified and active. Two queries for the
// whole pool keep the square free of per-model lookups (the same eligibility
// rule as loadVerifiedChannels, batched).
func loadOfficialChannelModels(db *gorm.DB) map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{})
	var channels []ChannelPool
	if err := db.Select("slug").
		Where("kind = ? AND status = ?", ChannelKindOfficial, PoolStatusActive).
		Find(&channels).Error; err != nil {
		return out
	}
	if len(channels) == 0 {
		return out
	}
	officialSlugs := make(map[string]struct{}, len(channels))
	for i := range channels {
		officialSlugs[strings.TrimSpace(channels[i].Slug)] = struct{}{}
	}
	var qualifications []ChannelModel
	if err := db.Select("channel_slug", "model").
		Where("verified = ? AND status = ?", true, PoolStatusActive).
		Find(&qualifications).Error; err != nil {
		return out
	}
	for i := range qualifications {
		slug := strings.TrimSpace(qualifications[i].ChannelSlug)
		model := strings.TrimSpace(qualifications[i].Model)
		if slug == "" || model == "" {
			continue
		}
		if _, ok := officialSlugs[slug]; !ok {
			continue
		}
		byChannel := out[model]
		if byChannel == nil {
			byChannel = make(map[string]struct{})
			out[model] = byChannel
		}
		byChannel[slug] = struct{}{}
	}
	return out
}

// loadSquarePoolSupply groups the current live/canary supply by standard
// model name. The models column is JSON text, so the model filter runs in Go;
// the live listing set is small in v1 (same trade-off as hasLiveListingForModel).
func loadSquarePoolSupply(db *gorm.DB, idx modelPoolIndex, coeffs map[string]map[string]int64) (map[string]*squareSupply, error) {
	var rows []Listing
	if err := db.Where("status IN ?", []string{ListingLive, ListingCanary}).
		Select("id", "vendor", "models", "multiplier_milli").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]int64, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	overrides := loadListingModelOverrides(db, ids, time.Now())
	official := loadOfficialChannelModels(db)
	supply := make(map[string]*squareSupply)
	for i := range rows {
		l := &rows[i]
		channel := strings.TrimSpace(l.Vendor)
		for _, raw := range l.Models {
			if !overrides.modelEnabled(l.ID, raw) {
				// A disabled model is not supply: its listing must not contribute
				// to the count, multiplier band or the advertised cheapest price.
				continue
			}
			model, ok := idx.resolve(raw)
			if !ok {
				continue
			}
			s := supply[model]
			if s == nil {
				s = &squareSupply{}
				supply[model] = s
			}
			_, isOfficial := official[model][channel]
			s.add(channel, overrides.multiplierFor(l.ID, raw, l.MultiplierMilli),
				coefficientFor(coeffs, model, channel), isOfficial)
		}
	}
	return supply, nil
}

// quoteTierPrices prices one tier through the single Quote function (§3.7):
// each of the four price dimensions is quoted separately with exactly one
// million tokens, so the result is the per-million-token buyer price with the
// same ceil rounding the debit path applies. The official price book is keyed
// by originVendor; coefficientMilli is the channel's price coefficient
// (1000 = official). Tool prices are not part of the square display (counts
// are per-request), so Search/Fetch stay zero.
func quoteTierPrices(db *gorm.DB, cal *Calendar, model, vendor, originVendor string, multMilli, coefficientMilli int64, at time.Time) (squareTierPrices, error) {
	var out squareTierPrices
	quoteDim := func(setTokens func(*QuoteInput)) (int64, error) {
		in := QuoteInput{
			Model:            model,
			Vendor:           vendor,
			OriginVendor:     originVendor,
			CoefficientMilli: coefficientMilli,
			MultiplierMilli:  multMilli,
			At:               at,
		}
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

// originVendorOf returns the standard model's official price owner. An empty
// origin vendor falls back to the model name so legacy rows still resolve.
func originVendorOf(model ModelPool) string {
	if origin := strings.TrimSpace(model.OriginVendor); origin != "" {
		return origin
	}
	return strings.TrimSpace(model.Name)
}

// buildSquareEntry assembles one standard model's square entry at instant now:
// the current tier, both tiers' buyer prices at the cheapest available factor
// (the current tier priced at now, the other at NextSwitch(now) — by
// construction the first instant of the other tier), and the switch instant.
//
// With live/canary supply the factor is the listing with the smallest
// coefficient × multiplier and buyer_price_source is "market". Without any
// listing the factor is 1000 × 1000 (official price ×1.0) and the source is
// "official" — an unlisted standard model is still shown.
//
// A model whose CURRENT tier cannot be priced is reported ok=false: a model
// the buyer could not be charged for must not be advertised (§3.2 买家看到的
// 就是最终价). The other tier is simply omitted when it has no covering price
// book.
func buildSquareEntry(db *gorm.DB, cal *Calendar, model ModelPool, s *squareSupply, now time.Time) (squareModelEntry, bool) {
	origin := originVendorOf(model)
	tier := cal.TierAt(now)
	entry := squareModelEntry{
		Model:            model.Name,
		Aliases:          parseStringList(model.Aliases),
		OriginVendor:     origin,
		Vendor:           origin,
		Tier:             tier,
		TierLabel:        tierLabel(tier),
		Prices:           make(map[string]squareTierPrices, 2),
		BuyerPriceSource: BuyerPriceSourceOfficial,
		GeneratedAt:      now,
		Capabilities:     parseStringList(model.Capabilities),
		ContextLength:    model.ContextLength,
	}
	mult, coeff, vendor := int64(1000), int64(1000), origin
	if s != nil {
		entry.ListingCount = s.count
		entry.ChannelCount = len(s.channels)
		entry.MultiplierMinMilli = s.minMult
		entry.OfficialAvailable = s.officialCount > 0
		if s.haveBest && s.count > 0 {
			entry.BuyerPriceSource = BuyerPriceSourceMarket
			mult, coeff, vendor = s.bestMult, s.bestCoeff, s.bestSlug
		}
	}
	next := cal.NextSwitch(now)
	otherAt := now
	if !next.IsZero() {
		entry.NextSwitch = &next
		otherAt = next
	}
	current, err := quoteTierPrices(db, cal, model.Name, vendor, origin, mult, coeff, now)
	if err != nil {
		return squareModelEntry{}, false
	}
	entry.Prices[tier] = current
	if other, err := quoteTierPrices(db, cal, model.Name, vendor, origin, mult, coeff, otherAt); err == nil {
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

// listSquareModels serves GET /square/models — one row per active standard
// model (ordered by standard name) that has an official price. Models with no
// listing still appear at the official price.
func listSquareModels(c *gin.Context) {
	db, cal := squareState(c)
	if db == nil {
		return
	}
	pool, err := loadActiveModelPool(db)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query models"))
		return
	}
	idx := newModelPoolIndex(pool)
	coeffs := loadChannelModelCoefficients(db)
	supply, err := loadSquarePoolSupply(db, idx, coeffs)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query listings"))
		return
	}
	now := time.Now()
	metrics, err := cachedSquareModelMetrics(db, now)
	if err != nil {
		// Reliability is decorative: a metrics failure must not take the
		// price list down. The columns are simply absent.
		common.SysError(common.LogText("marketplace: square metrics: %s", err.Error()))
		metrics = nil
	}
	items := make([]squareModelEntry, 0, len(pool))
	for _, model := range pool {
		entry, ok := buildSquareEntry(db, cal, model, supply[model.Name], now)
		if !ok {
			// Missing/unpriceable origin book: skip this model instead of failing
			// the whole page (§3.2).
			common.SysError(common.LogText("marketplace: square: no covering price for %s, hidden from square", model.Name))
			continue
		}
		if metrics != nil {
			entry.Metrics = metrics[model.Name]
		}
		items = append(items, entry)
	}
	common.ApiSuccess(c, items)
}

// getSquareModel serves GET /square/models/:model — the list entry plus the
// official per-tier price books, the supply's multiplier range and every
// channel that may sell the model (even without a listing). The model must be
// an active standard model with an official price; otherwise 404.
func getSquareModel(c *gin.Context) {
	db, cal := squareState(c)
	if db == nil {
		return
	}
	modelName := strings.TrimSpace(c.Param("model"))
	model, ok := ModelPoolByName(db, modelName)
	if !ok || model.Status != PoolStatusActive {
		common.ApiErrorStatus(c, http.StatusNotFound, errors.New("model not found"))
		return
	}
	pool := []ModelPool{model}
	idx := newModelPoolIndex(pool)
	coeffs := loadChannelModelCoefficients(db)
	supply, err := loadSquarePoolSupply(db, idx, coeffs)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query listings"))
		return
	}
	s := supply[model.Name]
	entry, ok := buildSquareEntry(db, cal, model, s, time.Now())
	if !ok {
		common.ApiErrorStatus(c, http.StatusNotFound, errors.New("model not found"))
		return
	}
	detail := squareModelDetail{
		squareModelEntry:   entry,
		MultiplierMinMilli: 1000,
		MultiplierMaxMilli: 1000,
		OfficialPrices:     make(map[string]squareOfficialPrices, 3),
		Channels:           loadSquareChannels(db, model.Name, s),
	}
	now := time.Now()
	detail.Channels = attachChannelMetrics(db, model.Name, detail.Channels, now)
	if all, err := loadSquareModelMetrics(db, now); err == nil {
		detail.Metrics = all[model.Name]
	} else {
		common.SysError(common.LogText("marketplace: square metrics: %s", err.Error()))
	}
	if s != nil && s.count > 0 {
		detail.MultiplierMinMilli = s.minMult
		detail.MultiplierMaxMilli = s.maxMult
	}
	// 各时段官方价: raw price-book rows per tier, no flat fallback — a
	// flat-only vendor shows its single flat row, not three copies of it.
	origin := originVendorOf(model)
	for _, tier := range []string{TierPeak, TierOffpeak, TierFlat} {
		pb, err := priceAt(db, model.Name, origin, tier, entry.GeneratedAt)
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

// loadSquareChannels lists every channel with a verified, active
// qualification for the standard model and an active channel definition.
// Channels are returned in stable slug order; listing aggregates come from
// live/canary supply (zero when the channel has no listing right now).
func loadSquareChannels(db *gorm.DB, model string, s *squareSupply) []squareChannelView {	var rows []ChannelModel
	if err := db.Where("model = ? AND verified = ? AND status = ?", model, true, PoolStatusActive).
		Order("channel_slug ASC").
		Find(&rows).Error; err != nil {
		return []squareChannelView{}
	}
	active := make(map[string]ChannelPool)
	for _, ch := range ChannelPoolAll(db) {
		if ch.Status == PoolStatusActive {
			active[ch.Slug] = ch
		}
	}
	out := make([]squareChannelView, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		ch, ok := active[strings.TrimSpace(row.ChannelSlug)]
		if !ok {
			// Channel disabled/deleted: degrade by skipping the row, never 500.
			continue
		}
		coefficient := row.CoefficientMilli
		if coefficient <= 0 {
			coefficient = 1000
		}
		view := squareChannelView{
			ChannelSlug:      ch.Slug,
			Name:             ch.Name,
			Kind:             ch.Kind,
			Verified:         row.Verified,
			CoefficientMilli: coefficient,
			Differences:      parseJSONObject(row.Differences),
		}
		if s != nil && s.byChannel != nil {
			if cs := s.byChannel[ch.Slug]; cs != nil {
				view.ListingCount = cs.count
				view.MinMultiplierMilli = cs.minMult
				view.MaxMultiplierMilli = cs.maxMult
			}
		}
		out = append(out, view)
	}
	return out
}

// attachChannelMetrics merges the 24h per-channel reliability into the channel
// views. A query failure leaves Metrics nil (the columns hide) instead of
// failing the detail page.
func attachChannelMetrics(db *gorm.DB, model string, channels []squareChannelView, now time.Time) []squareChannelView {
	metrics, err := loadSquareChannelMetrics(db, model, now)
	if err != nil {
		common.SysError(common.LogText("marketplace: square channel metrics: %s", err.Error()))
		return channels
	}
	for i := range channels {
		channels[i].Metrics = metrics[channels[i].ChannelSlug]
	}
	return channels
}
