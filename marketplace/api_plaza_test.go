package marketplace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

	// The price book is the plaza's only price source, so the listable model
	// needs a covering price; an unpriced model is hidden.
	require.NoError(t, db.Create(&PriceBook{
		Model: "plaza-model-1", Vendor: "deepseek", Tier: TierFlat, Version: 1,
		InputMicroPerMTok: 1_000_000, OutputMicroPerMTok: 4_000_000,
		CacheReadMicroPerMTok: 20_000, CacheWriteMicroPerMTok: 0,
		EffectiveAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}).Error)

	// Add test models to mp_models
	m1 := ModelPool{Name: "plaza-model-1", OriginVendor: "deepseek", Status: PoolStatusActive, EnablePlaza: true}
	m2 := ModelPool{Name: "plaza-model-2", OriginVendor: "deepseek", Status: PoolStatusActive, EnablePlaza: false}
	m3 := ModelPool{Name: "plaza-model-retired", OriginVendor: "deepseek", Status: PoolStatusRetired, EnablePlaza: true}
	require.NoError(t, db.Create(&m1).Error)
	require.NoError(t, db.Create(&m2).Error)
	require.NoError(t, db.Create(&m3).Error)
	m4 := ModelPool{Name: "plaza-model-nochan", OriginVendor: "deepseek", Status: PoolStatusActive, EnablePlaza: true}
	m5 := ModelPool{Name: "plaza-model-mponly", OriginVendor: "deepseek", Status: PoolStatusActive, EnablePlaza: true}
	require.NoError(t, db.Create(&m4).Error)
	require.NoError(t, db.Create(&m5).Error)

	// Native channel 1 serves plaza-model-1; seller channel 2 (mp:) serves
	// plaza-model-mponly; disabled native channel 3 serves plaza-model-nochan.
	require.NoError(t, db.Exec("CREATE TABLE IF NOT EXISTS channels (id INTEGER PRIMARY KEY, name TEXT, status INTEGER)").Error)
	require.NoError(t, db.Exec("CREATE TABLE IF NOT EXISTS abilities (\"group\" TEXT, model TEXT, channel_id INTEGER, enabled NUMERIC)").Error)
	require.NoError(t, db.Exec("INSERT INTO channels (id, name, status) VALUES (1, 'native', 1), (2, 'mp:seller:1', 1), (3, 'dead', 2)").Error)
	require.NoError(t, db.Exec("INSERT INTO abilities (\"group\", model, channel_id, enabled) VALUES ('default', 'plaza-model-1', 1, 1), ('default', 'plaza-model-mponly', 2, 1), ('default', 'plaza-model-nochan', 3, 1)").Error)

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
			// Base price equals the flat price book (multiplier 1).
			require.NotNil(t, it.BasePrices)
			assert.EqualValues(t, 1_000_000, it.BasePrices.InputMicroPerMTok)
			assert.EqualValues(t, 4_000_000, it.BasePrices.OutputMicroPerMTok)
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
	for _, it := range resp.Data.Items {
		assert.NotEqual(t, "plaza-model-nochan", it.Model, "model whose only native channel is disabled must not be listed")
		assert.NotEqual(t, "plaza-model-mponly", it.Model, "model served only by seller channels must not be listed")
	}

	// Also test IsPlazaModel helper
	assert.True(t, IsPlazaModel(db, "plaza-model-1"))
	assert.False(t, IsPlazaModel(db, "plaza-model-2"))
	assert.False(t, IsPlazaModel(db, "plaza-model-retired"))
	assert.False(t, IsPlazaModel(db, "non-existent"))
}
