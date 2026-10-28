package marketplace

// modelcatalog.go — 卖家可选模型目录 (ShareLLM-style channel editor).
//
// What a seller may list is decided by the standard model pool: a model is
// sellable when mp_models has an active row for it AND a flat official price
// exists at the moment of the request. Admin-maintained channel-model rows are
// no longer a requirement; they are only read to pick a per-channel upstream
// name and price coefficient when one happens to exist.
//
// The seller console asks for the two halves of the picker in one payload:
//
//	pool_models      every sellable standard model with its official reference
//	                 price (the four flat unit prices)
//	upstream_models  the names the seller's key actually exposes at /models,
//	                 each with the pool model it was automatically matched to
//	                 (null when unsure, so the seller picks one manually) and
//	                 whether that suggestion has a price the platform can bill

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// OfficialFlatPrice is one standard model's official flat reference price
// (微元 per million tokens). All four unit prices come from the same price-book
// row so the console can preview buyer prices without drifting from billing.
type OfficialFlatPrice struct {
	InputMicroPerMTok      int64 `json:"input_micro_per_mtok"`
	OutputMicroPerMTok     int64 `json:"output_micro_per_mtok"`
	CacheReadMicroPerMTok  int64 `json:"cache_read_micro_per_mtok"`
	CacheWriteMicroPerMTok int64 `json:"cache_write_micro_per_mtok"`
}

// poolModelView is one selectable standard model in the pool picker.
type poolModelView struct {
	Model        string            `json:"model"`
	Vendor       string            `json:"vendor"`
	DisplayName  string            `json:"display_name,omitempty"`
	ContextWidth int               `json:"context_length,omitempty"`
	Price        OfficialFlatPrice `json:"price"`
}

// upstreamModelView is one upstream model name returned by GET /models.
// SuggestedModel is nil when no confident match exists; the seller then picks
// a pool model manually. Priced is true only when the suggestion has an
// official flat price, i.e. the row can be ticked and listed.
type upstreamModelView struct {
	Upstream       string  `json:"upstream"`
	SuggestedModel *string `json:"suggested_model"`
	Priced         bool    `json:"priced"`
}

// sellerModelCatalog is the shared payload of POST /seller/listings/probe and
// POST /seller/listings/:id/models/upstream.
type sellerModelCatalog struct {
	UpstreamModels []upstreamModelView `json:"upstream_models"`
	PoolModels     []poolModelView     `json:"pool_models"`
}

// modelDateSuffix matches the snapshot suffixes vendors append to a model id:
// -20250101, -2025-01-01, -202501, -0101, -01-01, _20250101.
var modelDateSuffix = regexp.MustCompile(`[-_](?:\d{4}[-_]?\d{2}[-_]?\d{2}|\d{6}|\d{4})$`)

// modelNameCandidates expands an upstream model id into the spellings worth
// matching against the pool, in confidence order:
//
//	deepseek-flash-20250101  → deepseek-flash-20250101, deepseek-flash
//	deepseek/deepseek-flash  → deepseek/deepseek-flash, deepseek-flash
//	accounts/x/models/glm-5.3 → …, glm-5.3
//
// A name is only accepted when a candidate equals a pool model name or one of
// its aliases (case-insensitive), so a guess never invents a model.
func modelNameCandidates(upstream string) []string {
	base := strings.TrimSpace(upstream)
	if base == "" {
		return nil
	}
	out := make([]string, 0, 6)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, existing := range out {
			if strings.EqualFold(existing, s) {
				return
			}
		}
		out = append(out, s)
	}
	add(base)
	trimmed := stripModelDateSuffix(base)
	add(trimmed)
	leaf := trimmed
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		leaf = trimmed[i+1:]
		add(leaf)
		add(stripModelDateSuffix(leaf))
	}
	return out
}

// stripModelDateSuffix removes one trailing snapshot date suffix. A name that
// is nothing but a date is returned unchanged.
func stripModelDateSuffix(name string) string {
	stripped := modelDateSuffix.ReplaceAllString(name, "")
	if strings.TrimSpace(stripped) == "" {
		return name
	}
	return stripped
}

// matchPoolModelName resolves an upstream model id to a standard model name
// using the same rules the console shows: exact match, then a stripped date
// suffix, then a stripped vendor/account path prefix, each compared
// case-insensitively against names and aliases. Unknown names yield ("", false)
// so the seller picks manually instead of the platform guessing.
func matchPoolModelName(pool []ModelPool, upstream string) (string, bool) {
	index := make(map[string]string, len(pool)*2)
	for _, row := range pool {
		if row.Status != PoolStatusActive {
			continue
		}
		index[strings.ToLower(strings.TrimSpace(row.Name))] = row.Name
		for _, alias := range parseStringList(row.Aliases) {
			key := strings.ToLower(strings.TrimSpace(alias))
			if key != "" {
				if _, taken := index[key]; !taken {
					index[key] = row.Name
				}
			}
		}
	}
	for _, candidate := range modelNameCandidates(upstream) {
		if name, ok := index[strings.ToLower(candidate)]; ok {
			return name, true
		}
	}
	return "", false
}

// poolModelPrice resolves the official flat price of a standard model.
// Only a currently effective flat row counts: a model without one cannot be
// priced for buyers, so it is not sellable.
func poolModelPrice(db *gorm.DB, model ModelPool, at time.Time) (PriceBook, bool) {
	origin := strings.TrimSpace(model.OriginVendor)
	if origin == "" {
		return PriceBook{}, false
	}
	pb, err := PriceAt(db, model.Name, origin, TierFlat, at)
	if err != nil {
		return PriceBook{}, false
	}
	return pb, true
}

// sellablePoolModels lists the active standard models that have a current
// official flat price, ordered by name. It is the authority for what can be
// listed: nothing outside this list is accepted by the seller model endpoints.
func sellablePoolModels(db *gorm.DB, at time.Time) []poolModelView {
	models := activeModelPool(db)
	out := make([]poolModelView, 0, len(models))
	for _, model := range models {
		pb, ok := poolModelPrice(db, model, at)
		if !ok {
			continue
		}
		out = append(out, poolModelView{
			Model:        model.Name,
			Vendor:       strings.TrimSpace(model.OriginVendor),
			ContextWidth: model.ContextLength,
			Price: OfficialFlatPrice{
				InputMicroPerMTok:      pb.InputMicroPerMTok,
				OutputMicroPerMTok:     pb.OutputMicroPerMTok,
				CacheReadMicroPerMTok:  pb.CacheReadMicroPerMTok,
				CacheWriteMicroPerMTok: pb.CacheWriteMicroPerMTok,
			},
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

// sellerModelCatalogFor builds the picker payload from the upstream model
// names and the pool. Duplicate upstream names collapse; the upstream order
// from the provider is preserved so the console stays stable between probes.
func sellerModelCatalogFor(db *gorm.DB, upstreams []string, at time.Time) sellerModelCatalog {
	pool := activeModelPool(db)
	views := make([]upstreamModelView, 0, len(upstreams))
	seen := make(map[string]bool, len(upstreams))
	for _, raw := range upstreams {
		name := strings.TrimSpace(raw)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		view := upstreamModelView{Upstream: name}
		if matched, ok := matchPoolModelName(pool, name); ok {
			suggestion := matched
			view.SuggestedModel = &suggestion
			for _, model := range pool {
				if model.Name != matched {
					continue
				}
				if _, priced := poolModelPrice(db, model, at); priced {
					view.Priced = true
				}
				break
			}
		}
		views = append(views, view)
	}
	return sellerModelCatalog{UpstreamModels: views, PoolModels: sellablePoolModels(db, at)}
}

// poolCoefficients maps every pool model to the channel's price coefficient
// (1000 = official price). The seller console multiplies it with the chosen
// multiplier for its buyer-price preview; a channel without an admin-maintained
// row simply uses 1000.
func poolCoefficients(db *gorm.DB, vendor string) map[string]int64 {
	out := make(map[string]int64)
	if db == nil {
		return out
	}
	var rows []ChannelModel
	if err := db.Select("model", "coefficient_milli").
		Where("channel_slug = ?", strings.TrimSpace(vendor)).Find(&rows).Error; err != nil {
		return out
	}
	for _, row := range rows {
		coefficient := row.CoefficientMilli
		if coefficient <= 0 {
			coefficient = 1000
		}
		out[row.Model] = coefficient
	}
	return out
}

// upstreamProbeModel picks the upstream name for the informational
// prompt-cache probe: the first upstream model that maps to a priced pool
// model, else the first name. Empty when the key exposed nothing.
func upstreamProbeModel(db *gorm.DB, upstreams []string, at time.Time) string {
	pool := activeModelPool(db)
	for _, raw := range upstreams {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if matched, ok := matchPoolModelName(pool, name); ok {
			for _, model := range pool {
				if model.Name == matched {
					if _, priced := poolModelPrice(db, model, at); priced {
						return name
					}
					break
				}
			}
		}
	}
	if len(upstreams) > 0 {
		return strings.TrimSpace(upstreams[0])
	}
	return ""
}

// ListingUpstreamModel resolves the model name forwarded upstream for one
// standard model of a listing: the seller's alias (ListingModel.UpstreamModel)
// wins, then a legacy channel mapping when one exists, else the standard id
// itself. It never fails: an unmapped model is forwarded under its own name.
func ListingUpstreamModel(db *gorm.DB, listingID int64, vendor, model string) string {
	model = strings.TrimSpace(model)
	if db == nil || model == "" {
		return model
	}
	if listingID > 0 {
		var row ListingModel
		if err := db.Select("upstream_model").
			Where("listing_id = ? AND model = ?", listingID, model).Take(&row).Error; err == nil {
			if up := strings.TrimSpace(row.UpstreamModel); up != "" {
				return up
			}
		}
	}
	if cm, ok := ChannelModelFor(db, vendor, model); ok && strings.TrimSpace(cm.UpstreamModel) != "" {
		return strings.TrimSpace(cm.UpstreamModel)
	}
	return model
}

// sellableModelPrice reports whether a standard model may be listed: it must
// be an active pool model with an official flat price in effect at `at`.
func sellableModelPrice(db *gorm.DB, model string, at time.Time) (ModelPool, bool) {
	mp, found := ModelPoolByName(db, model)
	if !found {
		return ModelPool{}, false
	}
	if _, priced := poolModelPrice(db, mp, at); !priced {
		return ModelPool{}, false
	}
	return mp, true
}
