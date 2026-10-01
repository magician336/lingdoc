package workspacecore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// The API fixture executes original migration scripts as batches, not a naive
// semicolon split (comments also contain semicolons and can trail the script).
func TestSQLiteProductionMigrationBatch(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migrations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, file := range []string{"000018_lingdoc_workspace.up.sql", "000019_lingdoc_evidence_assets.up.sql"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	for _, table := range []string{"lingdoc_projects", "lingdoc_members", "lingdoc_chapters", "lingdoc_chapter_versions", "lingdoc_operations", "lingdoc_project_assets", "lingdoc_asset_revisions"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("migration did not create %s: count=%d error=%v", table, count, err)
		}
	}
}
