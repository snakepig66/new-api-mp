package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
)

// GetClientIP returns the caller's source IP as resolved by Gin's
// trusted-proxy configuration (common.SetTrustedProxies). The key forms use it
// to fill the IP whitelist without asking the buyer to look it up.
func GetClientIP(c *gin.Context) {
	common.ApiSuccess(c, gin.H{"ip": c.ClientIP()})
}
