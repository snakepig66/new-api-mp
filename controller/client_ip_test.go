package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetClientIPTrustsConfiguredProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	require.NoError(t, engine.SetTrustedProxies([]string{"127.0.0.1"}))
	engine.GET("/api/user/ip", GetClientIP)

	request := httptest.NewRequest(http.MethodGet, "/api/user/ip", nil)
	request.RemoteAddr = "127.0.0.1:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			IP string `json:"ip"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.True(t, body.Success)
	assert.Equal(t, "203.0.113.7", body.Data.IP)
}

func TestGetClientIPIgnoresForwardedForFromUntrustedPeer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	require.NoError(t, engine.SetTrustedProxies(nil))
	engine.GET("/api/user/ip", GetClientIP)

	request := httptest.NewRequest(http.MethodGet, "/api/user/ip", nil)
	request.RemoteAddr = "198.51.100.9:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	var body struct {
		Data struct {
			IP string `json:"ip"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, "198.51.100.9", body.Data.IP)
}
