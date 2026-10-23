package marketplace

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// lockForUpdate makes the next query emit SELECT ... FOR UPDATE so the matched
// rows stay locked until the surrounding transaction ends. SQLite has no FOR
// UPDATE syntax, so the clause is skipped there; SQLite's single-writer model
// makes one of two conflicting transactions fail instead.
//
// This mirrors model.lockForUpdate, which is unexported; branching here keys
// off the connection's dialect instead of the global database type so tests
// can use their own handle.
func lockForUpdate(tx *gorm.DB) *gorm.DB {
	if tx.Dialector.Name() == "sqlite" {
		return tx
	}
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}
