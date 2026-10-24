package router

// MP-HOOK: legacy upstream admin UI.
//
// The marketplace frontend hides the upstream dashboard, but operators still
// need channels and system settings in v1. /ops-legacy serves web/dist for the
// root user only. It is registered solely when MP_FRONTEND=site, so legacy
// deployments are unaffected.
//
// Access is checked by the existing RootAuth middleware. A bearer token may be
// passed in the Authorization header or, for a quick browser/curl handoff, in
// the ?token= query (remove once F4 ships a first-party ops login).

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

// legacyPathShim runs before the deferred legacy bundles. The upstream SPA
// has no base path support, so it must see "/" as the current location; the
// address bar is rewritten accordingly for the duration of the ops session.
var legacyPathShim = []byte(`<script>(function(){try{if(location.pathname.indexOf("/ops-legacy")===0){history.replaceState(null,"","/");}}catch(e){}})();</script>`)

func SetOpsLegacyRouter(router *gin.Engine, assets WebAssets) {
	legacyFS := common.EmbedFolder(assets.BuildFS, "web/dist")
	index := withLegacyPathShim(assets.IndexPage)
	auth := opsLegacyAuth()

	serveIndex := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	}
	serveAsset := func(c *gin.Context) {
		name := c.Param("filepath")
		if name == "" || name == "/" {
			serveIndex(c)
			return
		}
		file, err := legacyFS.Open(name)
		if err != nil {
			serveIndex(c)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.IsDir() {
			serveIndex(c)
			return
		}
		http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), file)
	}

	router.GET("/ops-legacy", auth, serveIndex)
	// The wildcard also answers "/ops-legacy/" (filepath == "/").
	router.GET("/ops-legacy/*filepath", auth, serveAsset)

	// The legacy build loads its chunks from /static/* at runtime. Those are
	// immutable public assets (no data), so they are served without the page
	// gate; every API call keeps its own authentication.
	router.GET("/static/*filepath", serveLegacyStatic(legacyFS))
}

func opsLegacyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.TrimSpace(c.GetHeader("Authorization")) == "" {
			if token := strings.TrimSpace(c.Query("token")); token != "" {
				c.Request.Header.Set("Authorization", "Bearer "+token)
			}
		}
		middleware.RootAuth()(c)
	}
}

func serveLegacyStatic(frontendFS static.ServeFileSystem) gin.HandlerFunc {
	fileServer := http.FileServer(frontendFS)
	return func(c *gin.Context) {
		if !frontendFS.Exists("/", c.Request.URL.Path) {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "max-age=604800")
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}

func withLegacyPathShim(index []byte) []byte {
	if !bytes.Contains(index, []byte("</head>")) {
		return index
	}
	replacement := make([]byte, 0, len(legacyPathShim)+len("</head>"))
	replacement = append(replacement, legacyPathShim...)
	replacement = append(replacement, []byte("</head>")...)
	return bytes.Replace(index, []byte("</head>"), replacement, 1)
}
