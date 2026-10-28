package candidateadoption

import (
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/database/fixturemigrations"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The audit table's shape belongs to the production migration chain (#416).
//
// These tests used to spell out the seven columns themselves, so the next
// column added by a migration made the audit insert fail with "no such column"
// inside a fixture that never claimed to own that table. The helper declares
// the table and applies every production SQLite migration that shapes or writes
// it, discovered by table name.
//
// The focused fixture (AutoMigrate over the local row projections) deliberately
// does not build the audit table: only the tests that assert an audit row exists
// ask for it, and they now get the production shape rather than a copy of it.
func ensureProjectAuditFixtureTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db.Migrator().HasTable("lingdoc_project_audits") {
		return
	}
	sqlDB, err := db.DB()
	require.NoError(t, err)
	root, err := fixturemigrations.RepoRoot()
	require.NoError(t, err)
	selection, err := fixturemigrations.ApplySQLite(sqlDB,
		filepath.Join(root, "migrations", "sqlite"), []string{"lingdoc_project_audits"})
	require.NoError(t, err, "give the fixture the production audit table shape")
	t.Logf("audit table fixture applies %d production migrations through %s",
		len(selection.Names), selection.Names[len(selection.Names)-1])
}
