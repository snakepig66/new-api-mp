package router

import (
	"embed"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

// WebAssets holds the embedded dashboard frontend assets.
type WebAssets struct {
	BuildFS   embed.FS
	IndexPage []byte
	// MP-HOOK: standalone marketplace frontend (site/dist). Served only when
	// MP_FRONTEND=site; the legacy web/dist assets stay embedded for /ops-legacy.
	SiteBuildFS   embed.FS
	SiteIndexPage []byte
}

func SetWebRouter(router *gin.Engine, assets WebAssets, pluginDispatcher gin.HandlerFunc) {
	frontendFS := common.EmbedFolder(assets.BuildFS, "web/dist")
	compress := gzip.Gzip(gzip.DefaultCompression)

	router.NoRoute(
		pluginDispatcher,
		middleware.RouteTag("web"),
		func(c *gin.Context) {
			// ServeFrontendFiles sends build files already compressed.
			if !frontendFS.Exists("/", c.Request.URL.Path) {
				compress(c)
			}
		},
		middleware.AccessTokenAudit(),
		middleware.GlobalWebRateLimit(frontendFS),
		middleware.Cache(),
		middleware.ServeFrontendFiles(frontendFS),
		func(c *gin.Context) {
			if strings.HasPrefix(c.Request.RequestURI, "/v1") || strings.HasPrefix(c.Request.RequestURI, "/api") || strings.HasPrefix(c.Request.RequestURI, "/assets") {
				controller.RelayNotFound(c)
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", assets.IndexPage)
		},
	)
}

// MP-HOOK: standalone marketplace frontend.
//
// SetSiteWebRouter is the MP_FRONTEND=site counterpart of SetWebRouter. All
// non-API paths fall back to site/dist/index.html, while real files under
// site/dist (Vite emits them in /assets) are served by the same middleware
// stack. API, relay and missing asset paths keep answering 404 as before.
func SetSiteWebRouter(router *gin.Engine, assets WebAssets, pluginDispatcher gin.HandlerFunc) {
	siteFS := common.EmbedFolder(assets.SiteBuildFS, "site/dist")
	compress := gzip.Gzip(gzip.DefaultCompression)

	router.NoRoute(
		pluginDispatcher,
		middleware.RouteTag("web"),
		func(c *gin.Context) {
			if !siteFS.Exists("/", c.Request.URL.Path) {
				compress(c)
			}
		},
		middleware.AccessTokenAudit(),
		middleware.GlobalWebRateLimit(siteFS),
		middleware.Cache(),
		siteFrontendCacheHeaders(),
		middleware.ServeFrontendFiles(siteFS),
		func(c *gin.Context) {
			if strings.HasPrefix(c.Request.RequestURI, "/v1") || strings.HasPrefix(c.Request.RequestURI, "/api") || strings.HasPrefix(c.Request.RequestURI, "/assets") {
				controller.RelayNotFound(c)
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", assets.SiteIndexPage)
		},
	)
}

// MP-HOOK: cache policy for the standalone frontend (site/dist).
//
// index.html must be revalidated on every load: it is what points the browser
// at the current asset hashes, and a stale copy is what made /models/:id white
// screen after a deploy. Vite's /assets filenames are content-hashed, so they
// can be cached forever.
func siteFrontendCacheHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		switch {
		case strings.HasPrefix(path, "/assets/"):
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		case path == "/" || path == "/index.html":
			c.Header("Cache-Control", "no-cache")
		}
		c.Next()
	}
}
