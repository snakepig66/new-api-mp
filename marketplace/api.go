package marketplace

// api.go — marketplace console/status API surface (§4.4 H). v1 exposes only
// the status endpoint; buyer/seller/admin APIs arrive with phases 5 and 6.

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
)

// RegisterRoutes mounts the public marketplace API routes on the /api group
// (status + model square; both are safe without auth). Buyer-key and record
// endpoints require login and are wired by the router seam onto a UserAuth
// group. The caller gates registration on Enabled().
func RegisterRoutes(g *gin.RouterGroup) {
	g.GET("/marketplace/status", getMarketplaceStatus)
	RegisterSquareRoutes(g.Group("/marketplace"))
}

// getMarketplaceStatus reports whether the marketplace module is active; it
// carries no sensitive data and needs no auth beyond the group's middleware.
func getMarketplaceStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"enabled": Enabled(),
		"version": common.Version,
	})
}
