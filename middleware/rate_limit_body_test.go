package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// P2 regression: a 429 must carry a JSON body so the frontend can show the
// back-off message instead of a generic failure (BUG-REPORT-2026-11-08 P2-2).
func TestWriteRateLimitedJSONBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func(path string) (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, path, nil)
		return c, w
	}

	// API path: envelope with success=false and a readable message.
	c, w := newCtx("/api/user/login")
	writeRateLimited(c, 1200)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "1200", w.Header().Get("Retry-After"))
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.False(t, env.Success)
	assert.Contains(t, env.Message, "1200")

	// Relay path: OpenAI error format.
	c, w = newCtx("/v1/chat/completions")
	writeRateLimited(c, 30)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	var openai struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &openai))
	assert.Equal(t, "rate_limit_error", openai.Error.Type)
	assert.NotEmpty(t, openai.Error.Message)

	// Missing TTL still yields a usable generic message.
	c, w = newCtx("/api/user/register")
	writeRateLimited(c, 0)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	var env2 struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env2))
	assert.NotEmpty(t, env2.Message)
}
