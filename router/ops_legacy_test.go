package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpsLegacyTicket_IssueAndRedeem(t *testing.T) {
	token := "admin-jwt-token-123456"
	ticket, err := issueOpsTicket(token)
	require.NoError(t, err)
	require.Len(t, ticket, 64)

	// First redemption: success
	redeemed, ok := redeemOpsTicket(ticket)
	require.True(t, ok)
	assert.Equal(t, token, redeemed)

	// Second redemption: fails (single use)
	_, ok = redeemOpsTicket(ticket)
	assert.False(t, ok)
}

func TestOpsLegacyTicket_Expiry(t *testing.T) {
	token := "admin-jwt-token-expired"
	opsTicketsMu.Lock()
	expiredTicket := "expired-test-ticket-hex-value"
	opsTickets[expiredTicket] = opsTicketEntry{
		token:     token,
		expiresAt: time.Now().Add(-1 * time.Second),
	}
	opsTicketsMu.Unlock()

	// Redeeming expired ticket must fail
	_, ok := redeemOpsTicket(expiredTicket)
	assert.False(t, ok)

	// Verify it was deleted even when expired
	opsTicketsMu.Lock()
	_, exists := opsTickets[expiredTicket]
	opsTicketsMu.Unlock()
	assert.False(t, exists)
}

func TestOpsLegacy_TicketEndpointRoleEnforcement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		role       int
		authHeader string
		wantStatus int
	}{
		{
			name:       "no auth header",
			role:       common.RoleAdminUser,
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "regular user rejected",
			role:       common.RoleCommonUser,
			authHeader: "Bearer user-token",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "admin user allowed",
			role:       common.RoleAdminUser,
			authHeader: "Bearer admin-token",
			wantStatus: http.StatusOK,
		},
		{
			name:       "root user allowed",
			role:       common.RoleRootUser,
			authHeader: "Bearer root-token",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/ops-legacy/ticket", nil)
			if tt.authHeader != "" {
				c.Request.Header.Set("Authorization", tt.authHeader)
			}
			c.Set("role", tt.role)

			handleCreateOpsTicket(c)

			assert.Equal(t, tt.wantStatus, recorder.Code)
			if tt.wantStatus == http.StatusOK {
				assert.Contains(t, recorder.Body.String(), `"ticket":`)
			}
		})
	}
}

func TestOpsLegacy_TicketRedemptionCookieFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	assets := WebAssets{
		IndexPage: []byte("<html><head></head><body>ops legacy</body></html>"),
	}
	SetOpsLegacyRouter(engine, assets)

	// 1. Issue a valid ticket
	adminToken := "valid-session-jwt-admin-token"
	ticket, err := issueOpsTicket(adminToken)
	require.NoError(t, err)

	// 2. Request /ops-legacy?ticket=...
	req := httptest.NewRequest(http.MethodGet, "/ops-legacy?ticket="+ticket, nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	// 3. Must return 302 Found redirecting to /ops-legacy
	assert.Equal(t, http.StatusFound, recorder.Code)
	assert.Equal(t, "/ops-legacy", recorder.Header().Get("Location"))

	// 4. Must set the HttpOnly cookie with Path=/ops-legacy
	setCookie := recorder.Header().Get("Set-Cookie")
	require.NotEmpty(t, setCookie)
	assert.Contains(t, setCookie, "ops_legacy_session="+adminToken)
	assert.Contains(t, setCookie, "Path=/ops-legacy")
	assert.Contains(t, setCookie, "HttpOnly")
	assert.Contains(t, setCookie, "SameSite=Strict")

	// 5. Replaying the same ticket immediately must fail (single-use)
	req2 := httptest.NewRequest(http.MethodGet, "/ops-legacy?ticket="+ticket, nil)
	recorder2 := httptest.NewRecorder()
	engine.ServeHTTP(recorder2, req2)
	assert.Equal(t, http.StatusUnauthorized, recorder2.Code)
}

func TestOpsLegacy_RejectsQueryToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	assets := WebAssets{
		IndexPage: []byte("<html><head></head><body>ops legacy</body></html>"),
	}
	SetOpsLegacyRouter(engine, assets)

	// Request /ops-legacy?token=... with HTML accept
	// The ?token= query parameter is no longer supported and must be ignored;
	// unauthenticated HTML requests must be redirected to /login.
	req := httptest.NewRequest(http.MethodGet, "/ops-legacy?token=some-token", nil)
	req.Header.Set("Accept", "text/html")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusFound, recorder.Code)
	assert.Equal(t, "/login", recorder.Header().Get("Location"))
}

func TestOpsLegacy_UnauthenticatedRedirectsToLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	assets := WebAssets{
		IndexPage: []byte("<html><head></head><body>ops legacy</body></html>"),
	}
	SetOpsLegacyRouter(engine, assets)

	req := httptest.NewRequest(http.MethodGet, "/ops-legacy", nil)
	req.Header.Set("Accept", "text/html")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusFound, recorder.Code)
	assert.Equal(t, "/login", recorder.Header().Get("Location"))
}
