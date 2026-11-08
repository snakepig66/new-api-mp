package router

// MP-HOOK: legacy upstream admin UI.
//
// The marketplace frontend hides the upstream dashboard, but operators still
// need channels and system settings in v1. /ops-legacy serves web/dist for
// root/admin operators only. It is registered solely when MP_FRONTEND=site, so legacy
// deployments are unaffected.
//
// Access is protected via short-lived (30s) single-use tickets:
// 1. Authenticated root/admin operators call POST /api/ops-legacy/ticket to acquire a ticket.
// 2. The browser navigates to /ops-legacy?ticket=<ticket>.
// 3. /ops-legacy exchanges the ticket for an HttpOnly, Secure, SameSite=Strict, Path=/ops-legacy
//    session cookie and issues an HTTP 302 redirect back to /ops-legacy (stripping query parameters).
// 4. Subscriptions and assets are authenticated via the session cookie under AdminAuth.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

// legacyPathShim runs before the deferred legacy bundles. The upstream SPA
// has no base path support, so it must see "/" as the current location; the
// address bar is rewritten accordingly for the duration of the ops session.
var legacyPathShim = []byte(`<script>(function(){try{if(location.pathname.indexOf("/ops-legacy")===0){history.replaceState(null,"","/");}}catch(e){}})();</script>`)

type opsTicketEntry struct {
	token     string
	expiresAt time.Time
}

var (
	opsTicketsMu sync.Mutex
	opsTickets   = make(map[string]opsTicketEntry)
)

func issueOpsTicket(token string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	ticket := hex.EncodeToString(buf)

	opsTicketsMu.Lock()
	defer opsTicketsMu.Unlock()

	now := time.Now()
	if len(opsTickets) > 100 {
		for k, v := range opsTickets {
			if now.After(v.expiresAt) {
				delete(opsTickets, k)
			}
		}
	}

	opsTickets[ticket] = opsTicketEntry{
		token:     token,
		expiresAt: now.Add(30 * time.Second),
	}
	return ticket, nil
}

func redeemOpsTicket(ticket string) (string, bool) {
	if ticket == "" {
		return "", false
	}
	opsTicketsMu.Lock()
	defer opsTicketsMu.Unlock()

	entry, ok := opsTickets[ticket]
	if !ok {
		return "", false
	}
	delete(opsTickets, ticket) // single-use guarantee

	if time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.token, true
}

func handleCreateOpsTicket(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	token := ""
	if strings.HasPrefix(authHeader, "Bearer ") {
		token = strings.TrimPrefix(authHeader, "Bearer ")
	}
	if token == "" {
		if cookie, err := c.Cookie("ops_legacy_session"); err == nil && strings.TrimSpace(cookie) != "" {
			token = strings.TrimSpace(cookie)
		}
	}
	if token == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "unauthorized: no active token",
		})
		return
	}

	role := c.GetInt("role")
	if role < common.RoleAdminUser {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "insufficient privileges: admin required",
		})
		return
	}

	ticket, err := issueOpsTicket(token)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to generate ticket",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"ticket": ticket,
		},
	})
}

func SetOpsLegacyRouter(router *gin.Engine, assets WebAssets) {
	legacyFS := common.EmbedFolder(assets.BuildFS, "web/dist")
	index := withLegacyPathShim(assets.IndexPage)

	router.POST("/api/ops-legacy/ticket", middleware.RouteTag("api"), middleware.AccessTokenAudit(), middleware.AdminAuth(), handleCreateOpsTicket)

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
		// 1. Single-use ticket redemption
		if ticket := strings.TrimSpace(c.Query("ticket")); ticket != "" {
			token, ok := redeemOpsTicket(ticket)
			if !ok {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": "invalid or expired ops-legacy ticket",
				})
				return
			}

			secure := common.SessionCookieSecure || c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
			http.SetCookie(c.Writer, &http.Cookie{
				Name:     "ops_legacy_session",
				Value:    token,
				Path:     "/ops-legacy",
				MaxAge:   86400,
				HttpOnly: true,
				Secure:   secure,
				SameSite: http.SameSiteStrictMode,
			})
			c.Redirect(http.StatusFound, "/ops-legacy")
			c.Abort()
			return
		}

		// 2. Cookie or Bearer auth
		if strings.TrimSpace(c.GetHeader("Authorization")) == "" {
			if cookie, err := c.Cookie("ops_legacy_session"); err == nil && strings.TrimSpace(cookie) != "" {
				c.Request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cookie))
			} else if strings.Contains(c.GetHeader("Accept"), "text/html") {
				c.Redirect(http.StatusFound, "/login")
				c.Abort()
				return
			}
		}

		middleware.AdminAuth()(c)
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
