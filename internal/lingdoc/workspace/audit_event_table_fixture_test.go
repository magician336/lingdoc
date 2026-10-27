package workspace

import (
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/database/fixturemigrations"
	"gorm.io/gorm"
)

// The shape of lingdoc_project_audits belongs to the production migration
// chain, not to this package's fixtures.
//
// The fixtures used to hand-write the column list. Every column added to that
// table afterwards broke them, and it broke them quietly: the audit write
// failed with "no such column", and the HTTP layer turned that into an opaque
// 500 instead of a schema error (#327, #416). The helper below therefore
// declares the table it needs and applies **every** production SQLite
// migration that shapes or writes that table, discovered by table name — there
// is no column list and no migration number here to fall behind.
//
// The same declaration is exercised by
// internal/database/audit_event_columns_test.go, which builds the table this
// way and compares it with the row model, so the fixture path cannot drift from
// the model without a red test.

func ensureAuditEventTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db.Migrator().HasTable("lingdoc_project_audits") {
		// A fixture that prepared the table already owns a production shape.
		return
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open the fixture database handle: %v", err)
	}
	root, err := fixturemigrations.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	selection, err := fixturemigrations.ApplySQLite(sqlDB,
		filepath.Join(root, "migrations", "sqlite"), []string{"lingdoc_project_audits"})
	if err != nil {
		t.Fatalf("give the fixture the production audit table shape: %v", err)
	}
	t.Logf("audit table fixture applies %d production migrations through %s",
		len(selection.Names), selection.Names[len(selection.Names)-1])
}
