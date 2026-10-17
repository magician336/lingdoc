package workspacecore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Both database fixtures discover the production LingDoc migration chain.
// A copied cutoff list previously missed migration 116/37 and built a schema
// that could not serve the current repository contract.
func productionLingDocMigrationNames(t *testing.T, flavor string) []string {
	t.Helper()
	if flavor != "sqlite" && flavor != "versioned" {
		t.Fatalf("unsupported migration fixture flavor %q", flavor)
	}
	matches, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", flavor, "*_lingdoc_*.up.sql"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("find %s production migrations: count=%d error=%v", flavor, len(matches), err)
	}
	sort.Strings(matches)
	names := make([]string, len(matches))
	for i, file := range matches {
		names[i] = filepath.Base(file)
	}
	t.Logf("%s fixture applies %d production migrations through %s", flavor, len(names), names[len(names)-1])
	return names
}

// The API fixture executes original migration scripts as batches, not a naive
// semicolon split (comments also contain semicolons and can trail the script).
func TestSQLiteProductionMigrationBatch(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migrations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, file := range productionLingDocMigrationNames(t, "sqlite") {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	for _, table := range []string{"lingdoc_projects", "lingdoc_members", "lingdoc_member_permissions", "lingdoc_chapters", "lingdoc_chapter_versions", "lingdoc_working_copies", "lingdoc_selected_rewrites", "lingdoc_operations", "lingdoc_project_assets", "lingdoc_asset_revisions", "lingdoc_candidates", "lingdoc_chapter_confirmations", "lingdoc_generation_runs", "lingdoc_release_snapshots", "lingdoc_export_artifacts", "lingdoc_change_sets", "lingdoc_project_template_copies", "lingdoc_rule_sets", "lingdoc_ruleset_use_audits", "lingdoc_template_profiles", "lingdoc_template_versions", "lingdoc_rule_suggestion_runs", "lingdoc_template_governance_operations", "lingdoc_g2_objects", "lingdoc_g2_object_versions", "lingdoc_g3_records", "lingdoc_g5_records", "lingdoc_g6_records"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("migration did not create %s: count=%d error=%v", table, count, err)
		}
	}
}
