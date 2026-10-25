package marketplace

// api_vendors.go — 公开的官方渠道目录 (OFFICIAL-CHANNELS §4.4 /
// MODEL-CHANNEL-POOL P5).
//
// The listing wizard and the model square need to know which channels exist,
// their kind, their protocol bases and which standard models each one can
// actually list right now. Both the channel list and the per-channel models
// come from the pool (mp_channels / mp_channel_models): a model is listable
// only when the channel-model qualification is verified AND active AND the
// standard model's origin vendor has an official price book, so the UI can
// never offer a model the backend would reject.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// vendorView is the public shape of one channel. No key material, no secret
// paths: protocol bases are public endpoints and keys are never involved.
// Kind mirrors tier for new clients ("official" / "plan" / "third_party")
// while tier stays in place for existing frontends.
type vendorView struct {
	ID              string          `json:"id"`
	DisplayName     string          `json:"display_name"`
	Tier            string          `json:"tier"`
	Kind            string          `json:"kind"`
	LogoURL         string          `json:"logo_url,omitempty"`
	Protocols       VendorProtocols `json:"protocols"`
	Models          []string        `json:"models"`
	PassthroughBody bool            `json:"passthrough_body"`
}

// vendorLogoURL is a static allow-list from channel slug to the copy shipped
// in site/public/vendors. Keeping it out of the database means no migration
// and no admin-editable URL that could point anywhere. Plan channels reuse
// their parent brand's mark.
func vendorLogoURL(slug string) string {
	switch slug {
	case "deepseek":
		return "/vendors/deepseek.svg"
	case "zhipu":
		return "/vendors/zhipu.svg"
	case "aliyun-bailian", "aliyun-bailian-coding-plan":
		return "/vendors/qwen.svg"
	case "moonshot":
		return "/vendors/kimi.svg"
	case "minimax":
		return "/vendors/minimax.svg"
	case "volcengine-ark", "volcengine-ark-coding-plan":
		return "/vendors/doubao.svg"
	default:
		return ""
	}
}

// RegisterVendorsRoutes mounts GET /marketplace/vendors on the public group.
func RegisterVendorsRoutes(g *gin.RouterGroup) {
	g.GET("/vendors", listVendors)
}

// listVendors serves the channel pool (status=active) with the currently
// listable models. The database handle comes from the relay state; before
// Init it answers 500 like every other marketplace endpoint.
func listVendors(c *gin.Context) {
	db, _ := relayState()
	if db == nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
		return
	}
	channels := ChannelPoolAll(db)
	views := make([]vendorView, 0, len(channels))
	for _, ch := range channels {
		if ch.Status != PoolStatusActive {
			continue
		}
		vendor, ok := ChannelPoolAsVendor(db, ch.Slug)
		if !ok {
			continue
		}
		views = append(views, vendorView{
			ID:              vendor.ID,
			DisplayName:     vendor.DisplayName,
			Tier:            vendor.Tier,
			Kind:            vendor.Tier,
			LogoURL:         vendorLogoURL(vendor.ID),
			Protocols:       vendor.Protocols,
			Models:          listableModels(db, vendor),
			PassthroughBody: vendor.PassthroughBody,
		})
	}
	common.ApiSuccess(c, views)
}

// listableModels returns the standard model ids a channel can list right now:
// a verified, active mp_channel_models row for the channel AND an official
// price book under the standard model's origin vendor. Stable model order,
// de-duplicated. The origin vendor defaults to the channel itself when the
// model has no pool row.
func listableModels(db *gorm.DB, vendor Vendor) []string {
	var rows []ChannelModel
	if err := db.Where("channel_slug = ? AND verified = ? AND status = ?",
		vendor.ID, true, PoolStatusActive).
		Order("model ASC").
		Find(&rows).Error; err != nil {
		return []string{}
	}
	out := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for i := range rows {
		m := strings.TrimSpace(rows[i].Model)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		origin := vendor.ID
		if model, ok := ModelPoolByName(db, m); ok {
			if o := strings.TrimSpace(model.OriginVendor); o != "" {
				origin = o
			}
		}
		var priced int64
		if err := db.Model(&PriceBook{}).Where("model = ? AND vendor = ?", m, origin).Count(&priced).Error; err != nil {
			continue
		}
		if priced == 0 {
			continue
		}
		out = append(out, m)
	}
	return out
}
