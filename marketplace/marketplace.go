// Package marketplace implements the LLM API marketplace on top of new-api.
//
// All marketplace logic lives in this package; upstream code is only touched
// at seam points marked with "// MP-HOOK". The whole module is inert unless
// the environment variable MP_ENABLED is set to "true", so a binary built
// from this tree behaves exactly like upstream when the marketplace is off.
package marketplace

import (
	"os"

	"gorm.io/gorm"
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
	return nil
}
