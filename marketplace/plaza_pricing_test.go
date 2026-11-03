package marketplace

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// capturePlazaPricingState snapshots the persisted pricing options so a test
// can restore them afterwards. SyncPlazaModelRatios writes through
// model.UpdateModelPricingOptions, which mutates both the options table and the
// in-memory ratio maps.
func capturePlazaPricingState(t *testing.T, db *gorm.DB) {
	t.Helper()
	snapshot := map[string]string{
		"ModelRatio":                   mustJSON(ratio_setting.GetModelRatioCopy()),
		"CompletionRatio":              mustJSON(ratio_setting.GetCompletionRatioCopy()),
		"CacheRatio":                   mustJSON(ratio_setting.GetCacheRatioCopy()),
		"billing_setting.billing_mode": mustJSON(billing_setting.GetBillingModeCopy()),
		"billing_setting.billing_expr": mustJSON(billing_setting.GetBillingExprCopy()),
	}
	t.Cleanup(func() {
		require.NoError(t, model.UpdateModelPricingOptions(snapshot))
	})
}

// newPlazaFileDB opens a file-backed SQLite database with more than one
// connection. model.UpdateModelPricingOptions validates pricing inside a
// transaction and resolves task model aliases with a second query, so the
// single-connection in-memory test database used by most marketplace tests
// would deadlock on itself.
func newPlazaFileDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "plaza_pricing.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func usePlazaTestModelDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	old := model.DB
	model.DB = db
	switch db.Dialector.Name() {
	case "mysql":
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	case "postgres":
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	default:
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	}
	common.SetLogDatabaseType(common.MainDatabaseType())
	model.InitCommonColumns()
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	oldOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		common.OptionMap = oldOptions
		model.DB = old
	})
}

func usePlazaTestRelayState(t *testing.T, db *gorm.DB) *Calendar {
	t.Helper()
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
	return cal
}

func setPlazaTestPrice(t *testing.T, db *gorm.DB, modelName string, input, output, cacheRead int64) {
	t.Helper()
	next, err := nextPriceVersion(db, modelName, "zhipu", TierFlat)
	require.NoError(t, err)
	require.NoError(t, db.Create(&PriceBook{
		Model:                 modelName,
		Vendor:                "zhipu",
		Tier:                  TierFlat,
		InputMicroPerMTok:     input,
		OutputMicroPerMTok:    output,
		CacheReadMicroPerMTok: cacheRead,
		Version:               next,
		EffectiveAt:           time.Now().Add(-time.Minute),
	}).Error)
}

func TestPlazaModelRatioConversionMatchesKnownPrices(t *testing.T) {
	old := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = old })

	// Production: QUOTA_PER_UNIT=1000000, 1 quota = 1 微元.
	common.QuotaPerUnit = 1_000_000
	assert.InDelta(t, 1.0, plazaModelRatio(1_000_000), 1e-12, "deepseek-v4.1-flash ¥1.0/M")
	assert.InDelta(t, 8.0, plazaModelRatio(8_000_000), 1e-12, "glm-5.3 ¥8/M")
	// Upstream default: ¥1 = 500000 quota.
	common.QuotaPerUnit = 500_000
	assert.InDelta(t, 0.5, plazaModelRatio(1_000_000), 1e-12)
	// Round trip: quota debited for 1M input tokens equals price × QuotaPerUnit.
	common.QuotaPerUnit = 1_000_000
	assert.InDelta(t, 1.2*common.QuotaPerUnit, plazaModelRatio(1_200_000)*1e6, 1e-6, "hy3 ¥1.2/M")
}

func TestSyncPlazaModelRatiosWritesEnabledPlazaPrices(t *testing.T) {
	db := newPlazaFileDB(t)
	migrateAll(t, db)
	usePlazaTestModelDB(t, db)
	cal := usePlazaTestRelayState(t, db)
	capturePlazaPricingState(t, db)

	setPlazaTestPrice(t, db, "plaza-sync-one", 1_000_000, 4_000_000, 20_000)
	setPlazaTestPrice(t, db, "plaza-sync-off", 2_000_000, 8_000_000, 400_000)

	require.NoError(t, db.Create(&ModelPool{Name: "plaza-sync-one", OriginVendor: "zhipu", Status: PoolStatusActive, EnablePlaza: true}).Error)
	require.NoError(t, db.Create(&ModelPool{Name: "plaza-sync-off", OriginVendor: "zhipu", Status: PoolStatusActive, EnablePlaza: false}).Error)
	require.NoError(t, db.Create(&ModelPool{Name: "plaza-sync-retired", OriginVendor: "zhipu", Status: PoolStatusRetired, EnablePlaza: true}).Error)

	require.NoError(t, model.UpdateModelPricingOptions(map[string]string{
		"ModelRatio":      `{"plaza-sync-off": 9}`,
		"CompletionRatio": `{"plaza-sync-off": 2}`,
		"CacheRatio":      `{"plaza-sync-off": 0.5}`,
	}))

	require.NoError(t, SyncPlazaModelRatios(db, cal))

	ratio, ok, _ := ratio_setting.GetModelRatio("plaza-sync-one")
	require.True(t, ok)
	assert.InDelta(t, plazaModelRatio(1_000_000), ratio, 1e-12)
	assert.InDelta(t, 4.0, ratio_setting.GetCompletionRatio("plaza-sync-one"), 1e-9)
	cacheRatio, ok := ratio_setting.GetCacheRatio("plaza-sync-one")
	require.True(t, ok)
	assert.InDelta(t, 0.02, cacheRatio, 1e-9)

	offRatio, ok, _ := ratio_setting.GetModelRatio("plaza-sync-off")
	require.True(t, ok)
	assert.InDelta(t, 9, offRatio, 1e-9, "a model kept off the plaza keeps the operator's ratio")
}

func TestSyncPlazaModelRatiosClearsTieredExprForEnabledModel(t *testing.T) {
	db := newPlazaFileDB(t)
	migrateAll(t, db)
	usePlazaTestModelDB(t, db)
	cal := usePlazaTestRelayState(t, db)
	capturePlazaPricingState(t, db)

	setPlazaTestPrice(t, db, "plaza-tiered-model", 10_000_000, 40_000_000, 1_000_000)
	require.NoError(t, db.Create(&ModelPool{Name: "plaza-tiered-model", OriginVendor: "zhipu", Status: PoolStatusActive, EnablePlaza: true}).Error)
	require.NoError(t, model.UpdateModelPricingOptions(map[string]string{
		"billing_setting.billing_mode": `{"plaza-tiered-model": "tiered_expr"}`,
		"billing_setting.billing_expr": `{"plaza-tiered-model": "tier(\"base\", p * 1)"}`,
	}))

	require.NoError(t, SyncPlazaModelRatios(db, cal))

	_, hasExpr := billing_setting.GetBillingExpr("plaza-tiered-model")
	assert.False(t, hasExpr, "plaza model's tiered expression must be cleared")
	assert.Equal(t, billing_setting.BillingModeRatio, billing_setting.GetBillingMode("plaza-tiered-model"))
}

func TestEnsurePlazaPriceableRejectsUnpricedModel(t *testing.T) {
	db := newPlazaFileDB(t)
	migrateAll(t, db)
	usePlazaTestModelDB(t, db)
	usePlazaTestRelayState(t, db)

	err := ensurePlazaPriceable(db, ModelPool{Name: "no-price-model", OriginVendor: "zhipu"})
	require.ErrorIs(t, err, ErrPlazaPriceRequired)

	setPlazaTestPrice(t, db, "priced-model", 1_000_000, 4_000_000, 20_000)
	require.NoError(t, ensurePlazaPriceable(db, ModelPool{Name: "priced-model", OriginVendor: "zhipu"}))
}

func TestPlazaLogFirstTokenMsReadsStreamFRT(t *testing.T) {
	other := model.NewLogOther()
	require.True(t, other.SetPublic("frt", float64(812)))
	stream := &model.Log{IsStream: true, Other: other.JSONString()}
	assert.EqualValues(t, 812, plazaLogFirstTokenMs(stream))

	nonStream := &model.Log{IsStream: false, Other: other.JSONString()}
	assert.Zero(t, plazaLogFirstTokenMs(nonStream))

	assert.Zero(t, plazaLogFirstTokenMs(nil))
	assert.Zero(t, plazaLogFirstTokenMs(&model.Log{IsStream: true, Other: ""}))
	assert.Zero(t, plazaLogFirstTokenMs(&model.Log{IsStream: true, Other: "not json"}))

	zeroFRT := model.NewLogOther()
	require.True(t, zeroFRT.SetPublic("frt", float64(0)))
	assert.Zero(t, plazaLogFirstTokenMs(&model.Log{IsStream: true, Other: zeroFRT.JSONString()}))
}
