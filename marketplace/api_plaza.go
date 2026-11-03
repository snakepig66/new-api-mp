package marketplace

import (
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type PlazaModel struct {
	Model         string           `json:"model"`
	Aliases       []string         `json:"aliases"`
	OriginVendor  string           `json:"origin_vendor"`
	Vendor        string           `json:"vendor"`
	Capabilities  []string         `json:"capabilities"`
	ContextLength int              `json:"context_length"`
	Status        string           `json:"status"` // active / retired
	EnablePlaza   bool             `json:"enable_plaza"`
	Prices        squareTierPrices `json:"prices"`
}

func RegisterPlazaRoutes(g *gin.RouterGroup) {
	g.GET("/plaza/models", listPlazaModels)
}

func listPlazaModels(c *gin.Context) {
	db, cal := squareState(c)
	if db == nil {
		return
	}

	var rows []ModelPool
	if err := db.Where("status = ? AND enable_plaza = ?", PoolStatusActive, true).
		Order("name ASC").Find(&rows).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, err)
		return
	}

	served := plazaServedModels(db)

	now := time.Now()
	items := make([]PlazaModel, 0, len(rows))

	for _, row := range rows {
		// Only list models that at least one enabled platform (non-marketplace)
		// channel can serve right now, so deleting or disabling the channels in
		// the native admin takes the model off the plaza immediately.
		if !served[row.Name] {
			continue
		}
		vendor := row.OriginVendor
		if vendor == "" {
			vendor = originVendorOf(row)
		}
		// The price book is the plaza's only price source: a model without a
		// covering price is not listed.
		tierPrices, err := quoteTierPrices(db, cal, row.Name, vendor, vendor, 1000, 1000, now)
		if err != nil || !plazaPriceable(tierPrices) {
			continue
		}

		items = append(items, PlazaModel{
			Model:         row.Name,
			Aliases:       parseStringList(row.Aliases),
			OriginVendor:  vendor,
			Vendor:        vendor,
			Capabilities:  parseStringList(row.Capabilities),
			ContextLength: row.ContextLength,
			Status:        row.Status,
			EnablePlaza:   row.EnablePlaza,
			Prices:        tierPrices,
		})
	}

	common.ApiSuccess(c, gin.H{"items": items})
}

// plazaServedModels returns the set of model names served by at least one
// enabled ability on an enabled native channel. Marketplace seller channels
// are named "mp:..." and never back the plaza.
func plazaServedModels(db *gorm.DB) map[string]bool {
	out := map[string]bool{}
	var names []string
	err := db.Table("abilities").
		Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where("abilities.enabled = ? AND channels.status = ? AND channels.name NOT LIKE ?", true, 1, "mp:%").
		Distinct().Pluck("abilities.model", &names).Error
	if err != nil {
		return out
	}
	for _, n := range names {
		out[n] = true
	}
	return out
}
