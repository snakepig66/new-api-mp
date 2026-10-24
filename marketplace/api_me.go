package marketplace

// api_me.go — GET /api/marketplace/me (§5 FRONTEND-REBUILD first row): the
// logged-in user's id plus whether they have a 分享者 identity and how far the
// onboarding has progressed. It never carries key material, the real name, the
// ID number or the payout account.

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
)

// registerMeRoutes mounts GET /me on a group assumed to carry UserAuth.
func registerMeRoutes(g *gin.RouterGroup) {
	g.GET("/me", getMe)
}

// RegisterMeRoutes is the exported seam for callers that want /me mounted
// separately; RegisterSellerOnboardingRoutes already mounts it.
func RegisterMeRoutes(g *gin.RouterGroup) {
	registerMeRoutes(g)
}

// getMe serves GET /me → {"user_id":…, "seller":{"exists":…,"status":…,"progress":…}}.
func getMe(c *gin.Context) {
	userID, ok := sellerUserID(c)
	if !ok {
		return
	}
	db := sellerDB(c)
	if db == nil {
		return
	}
	seller, err := GetSeller(db, userID)
	if err != nil {
		if errors.Is(err, ErrSellerNotFound) {
			common.ApiSuccess(c, gin.H{
				"user_id": userID,
				"seller":  sellerIdentityViewOf(Seller{}, false),
			})
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New("failed to query seller"))
		return
	}
	common.ApiSuccess(c, gin.H{
		"user_id": userID,
		"seller":  sellerIdentityViewOf(seller, true),
	})
}
