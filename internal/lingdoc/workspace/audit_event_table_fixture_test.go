package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

// The shape of lingdoc_project_audits belongs to the production migration
// chain, not to this package's fixtures.
//
// The fixtures used to hand-write the column list. Every column added to that
// table afterwards broke them, and it broke them quietly: the audit write
// failed with "no such column", and the HTTP layer turned that into an opaque
// 500 instead of a schema error (#327). The helper below creates the base
// table and then applies the production migration that brings it to the
// current shape, so the fixture and production agree by construction.

func ensureAuditEventTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS lingdoc_project_audits (
		id TEXT PRIMARY KEY, project_id TEXT NOT NULL, actor_id TEXT NOT NULL,
		action TEXT NOT NULL, target TEXT NOT NULL, details_json TEXT NOT NULL DEFAULT '{}',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`).Error; err != nil {
		t.Fatalf("prepare the audit table: %v", err)
	}
	if auditEventTableHasColumn(t, db, "target_version") {
		return
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite",
		"000052_lingdoc_audit_version_fields.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatalf("apply the audit version fields migration: %v", err)
	}
}

// auditEventTableHasColumn reports whether the audit table already carries the
// column, so a fixture that prepares the same table twice does not apply the
// migration twice.
func auditEventTableHasColumn(t *testing.T, db *gorm.DB, column string) bool {
	t.Helper()
	var columns []struct {
		Name string
	}
	if err := db.Raw("PRAGMA table_info(lingdoc_project_audits)").Scan(&columns).Error; err != nil {
		t.Fatalf("read the audit table columns: %v", err)
	}
	for _, candidate := range columns {
		if candidate.Name == column {
			return true
		}
	}
	return false
}
