package marketplace

// api_vendors.go — 公开的官方渠道目录 (OFFICIAL-CHANNELS §4.4).
//
// The listing wizard and the model square need to know which channels exist,
// their tier, their protocol bases and which platform models each one can
// actually list. The listable set is the intersection of the vendor's static
// catalogue, the admin-editable channel_model_map and the official price
// books, so the UI can never offer a model the backend would reject.

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// vendorView is the public shape of one channel. No key material, no secret
// paths: protocol bases are public endpoints and keys are never involved.
type vendorView struct {
	ID              string          `json:"id"`
	DisplayName     string          `json:"display_name"`
	Tier            string          `json:"tier"`
	Protocols       VendorProtocols `json:"protocols"`
	Models          []string        `json:"models"`
	PassthroughBody bool            `json:"passthrough_body"`
}

// RegisterVendorsRoutes mounts GET /marketplace/vendors on the public group.
func RegisterVendorsRoutes(g *gin.RouterGroup) {
	g.GET("/vendors", listVendors)
}

// listVendors serves the whitelist with the currently listable models. The
// database handle comes from the relay state; before Init it answers 500 like
// every other marketplace endpoint.
func listVendors(c *gin.Context) {
	db, _ := relayState()
	if db == nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("marketplace is not initialized"))
		return
	}
	views := make([]vendorView, 0, len(VendorIDs()))
	for _, id := range VendorIDs() {
		vendor, ok := VendorByID(id)
		if !ok {
			continue
		}
		views = append(views, vendorView{
			ID:              vendor.ID,
			DisplayName:     vendor.DisplayName,
			Tier:            vendor.Tier,
			Protocols:       vendor.Protocols,
			Models:          listableModels(db, vendor),
			PassthroughBody: vendor.PassthroughBody,
		})
	}
	common.ApiSuccess(c, views)
}

// listableModels returns the platform model ids a vendor can list right now:
// mapped in channel_model_map AND covered by an effective price book for the
// (model, vendor) pair. Stable whitelist order.
func listableModels(db *gorm.DB, vendor Vendor) []string {
	out := make([]string, 0, len(vendor.Models))
	seen := make(map[string]bool, len(vendor.Models))
	for _, m := range vendor.Models {
		if seen[m] {
			continue
		}
		seen[m] = true
		if _, mapped := ChannelModelResolve(db, vendor.ID, m); !mapped {
			continue
		}
		var priced int64
		if err := db.Model(&PriceBook{}).Where("model = ? AND vendor = ?", m, vendor.ID).Count(&priced).Error; err != nil {
			continue
		}
		if priced == 0 {
			continue
		}
		out = append(out, m)
	}
	return out
}
