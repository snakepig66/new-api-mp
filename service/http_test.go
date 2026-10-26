package service

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

// Only identity-safe upstream headers may cross the marketplace proxy; every
// header that reveals the upstream provider is dropped.
func TestShouldCopyUpstreamHeaderWhitelist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	cases := []struct {
		name string
		want bool
	}{
		{"Content-Type", true},
		{"content-type", true},
		{"Cache-Control", true},
		{"ETag", true},
		{"Last-Modified", true},
		{"X-Reasoning-Included", true},
		{"X-Codex-Turn-State", true},

		{"Content-Length", false},
		{"OpenAI-Organization", false},
		{"organization", false},
		{"x-ratelimit-remaining-requests", false},
		{"ratelimit-limit", false},
		{"Set-Cookie", false},
		{"Server", false},
		{"Via", false},
		{"X-Powered-By", false},
		{"X-Oneapi-Request-Id", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, ShouldCopyUpstreamHeader(c, tc.name, []string{"v"}), tc.name)
	}
}

// The upstream request id is never forwarded (the local instance supplies its
// own), but it is kept for logging.
func TestShouldCopyUpstreamHeaderCapturesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	assert.False(t, ShouldCopyUpstreamHeader(c, common.RequestIdKey, []string{"upstream-req-1"}))
	assert.Equal(t, "upstream-req-1", c.GetString(common.UpstreamRequestIdKey))
}

func TestIOCopyBytesGracefullyCopiesWhitelistOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	upstream := httptest.NewRecorder()
	upstream.Header().Set("Content-Type", "application/json")
	upstream.Header().Set("Cache-Control", "no-store")
	upstream.Header().Set("OpenAI-Organization", "org-upstream")
	upstream.Header().Set("x-ratelimit-limit", "100")
	upstream.Header().Set("Set-Cookie", "session=upstream")
	upstream.Header().Set("Server", "upstream-vendor")
	upstream.Header().Set("Via", "1.1 upstream")
	upstream.WriteHeader(http.StatusOK)
	src := upstream.Result()

	data := []byte(`{"ok":true}`)
	IOCopyBytesGracefully(c, src, data)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, strconv.Itoa(len(data)), rec.Header().Get("Content-Length"))
	assert.Empty(t, rec.Header().Get("Openai-Organization"))
	assert.Empty(t, rec.Header().Get("X-Ratelimit-Limit"))
	assert.Empty(t, rec.Header().Get("Set-Cookie"))
	assert.Empty(t, rec.Header().Get("Server"))
	assert.Empty(t, rec.Header().Get("Via"))
	assert.Equal(t, string(data), rec.Body.String())
}

// A nil upstream response still writes the locally generated payload.
func TestIOCopyBytesGracefullyNilSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	data := []byte(`{"ok":true}`)
	IOCopyBytesGracefully(c, nil, data)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, strconv.Itoa(len(data)), rec.Header().Get("Content-Length"))
	assert.Equal(t, string(data), rec.Body.String())
}
