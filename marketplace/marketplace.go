// Package marketplace implements the LLM API marketplace on top of new-api.
//
// All marketplace logic lives in this package; upstream code is only touched
// at seam points marked with "// MP-HOOK". The whole module is inert unless
// the environment variable MP_ENABLED is set to "true", so a binary built
// from this tree behaves exactly like upstream when the marketplace is off.
package marketplace

import (
	"fmt"
	"os"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

var enabled bool

// Enabled reports whether the marketplace module is active.
func Enabled() bool {
	return enabled
}

// Init bootstraps the marketplace module: configuration, migrations and
// background jobs (added in later phases). It is a no-op unless
// MP_ENABLED=true. db is the primary database handle owned by model.InitDB;
// Init must not close or reconfigure it.
func Init(db *gorm.DB) error {
	enabled = os.Getenv("MP_ENABLED") == "true"
	if !enabled {
		return nil
	}
	// MP_KEY_SECRET is mandatory (§4.5): without it anonymous buyer codes
	// cannot be derived. Never include the secret itself in any error or log.
	secret := os.Getenv("MP_KEY_SECRET")
	if secret == "" {
		return ErrNoAnonSecret
	}
	SetAnonSecret(secret)
	if err := Migrate(db); err != nil {
		return fmt.Errorf("marketplace init: %w", err)
	}
	if err := SeedPrices(db); err != nil {
		return fmt.Errorf("marketplace seed: %w", err)
	}
	common.SysLog("marketplace enabled: migrations applied")
	return nil
}
