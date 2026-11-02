package marketplace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlazaModelsListAndFiltering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, SeedPrices(db))
	cal, err := LoadCalendar(db)
	require.NoError(t, err)

	relayStateMu.Lock()
	oldDB, oldCal := relayDB, defaultCalendar
	relayDB, defaultCalendar = db, cal
	relayStateMu.Unlock()
	t.Cleanup(func() {
		relayStateMu.Lock()
		relayDB, defaultCalendar = oldDB, oldCal
		relayStateMu.Unlock()
	})

	// Add test models to mp_models
	m1 := ModelPool{Name: "plaza-model-1", OriginVendor: "deepseek", Status: PoolStatusActive, EnablePlaza: true}
	m2 := ModelPool{Name: "plaza-model-2", OriginVendor: "deepseek", Status: PoolStatusActive, EnablePlaza: false}
	m3 := ModelPool{Name: "plaza-model-retired", OriginVendor: "deepseek", Status: PoolStatusRetired, EnablePlaza: true}
	require.NoError(t, db.Create(&m1).Error)
	require.NoError(t, db.Create(&m2).Error)
	require.NoError(t, db.Create(&m3).Error)

	engine := gin.New()
	RegisterPlazaRoutes(engine.Group("/api/marketplace"))

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/plaza/models", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Items []PlazaModel `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)

	// Check items
	found1 := false
	found2 := false
	found3 := false
	for _, it := range resp.Data.Items {
		if it.Model == "plaza-model-1" {
			found1 = true
			assert.True(t, it.EnablePlaza)
		}
		if it.Model == "plaza-model-2" {
			found2 = true
		}
		if it.Model == "plaza-model-retired" {
			found3 = true
		}
	}
	assert.True(t, found1, "active plaza model must be listed")
	assert.False(t, found2, "plaza model with enable_plaza=false must not be listed")
	assert.False(t, found3, "retired model must not be listed")

	// Also test IsPlazaModel helper
	assert.True(t, IsPlazaModel(db, "plaza-model-1"))
	assert.False(t, IsPlazaModel(db, "plaza-model-2"))
	assert.False(t, IsPlazaModel(db, "plaza-model-retired"))
	assert.False(t, IsPlazaModel(db, "non-existent"))
}
