package workspacecore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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
	for _, file := range []string{
		"000018_lingdoc_workspace.up.sql", "000019_lingdoc_evidence_assets.up.sql",
		"000020_lingdoc_candidate_adoption.up.sql", "000021_lingdoc_generation_runs.up.sql",
		"000022_lingdoc_generation_claim_token.up.sql", "000023_lingdoc_chapter_confirmation_requests.up.sql",
		"000024_lingdoc_delivery_stores.up.sql", "000025_lingdoc_member_permissions.up.sql",
		"000026_lingdoc_project_context_revision.up.sql", "000027_lingdoc_draft_provenance.up.sql",
		"000028_lingdoc_owner_transfers.up.sql", "000029_lingdoc_project_discard.up.sql",
		"000030_lingdoc_project_baseline.up.sql", "000031_lingdoc_citation_usages.up.sql",
		"000032_lingdoc_change_sets.up.sql",
		"000033_lingdoc_working_copies.up.sql",
		"000034_lingdoc_selected_rewrites.up.sql",
		"000035_lingdoc_project_template_copies.up.sql",
		"000038_lingdoc_satisfied_change_sets.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	for _, table := range []string{"lingdoc_projects", "lingdoc_members", "lingdoc_member_permissions", "lingdoc_chapters", "lingdoc_chapter_versions", "lingdoc_working_copies", "lingdoc_selected_rewrites", "lingdoc_operations", "lingdoc_project_assets", "lingdoc_asset_revisions", "lingdoc_candidates", "lingdoc_chapter_confirmations", "lingdoc_generation_runs", "lingdoc_release_snapshots", "lingdoc_export_artifacts", "lingdoc_change_sets", "lingdoc_project_template_copies"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("migration did not create %s: count=%d error=%v", table, count, err)
		}
	}
}

func TestSQLiteSatisfiedChangeSetMigrationUpgradesExistingRows(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "satisfied-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("CREATE TABLE lingdoc_projects (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO lingdoc_projects (id) VALUES ('project-1')"); err != nil {
		t.Fatal(err)
	}
	oldMigration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000032_lingdoc_change_sets.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	fullWorkflowSchema := strings.Replace(string(oldMigration),
		"CHECK (status IN ('assessed', 'applied', 'rejected', 'stale'))",
		"CHECK (status IN ('draft', 'assessed', 'applied', 'rejected', 'stale', 'stabilized'))", 1)
	if _, err := db.ExecContext(context.Background(), fullWorkflowSchema); err != nil {
		t.Fatalf("create pre-upgrade schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO lingdoc_change_sets
		(id, project_id, created_by, reason, status, base_context_revision, base_spec_revision, fields_json, impacts_json)
		VALUES ('stale-1', 'project-1', 'owner', 'legacy proposal', 'stale', 4, 3, '[{"fields":[]}]', '[]')`); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"draft", "stabilized"} {
		if _, err := db.Exec(`INSERT INTO lingdoc_change_sets
			(id, project_id, created_by, reason, status, base_context_revision, base_spec_revision)
			VALUES (?, 'project-1', 'owner', 'full workflow history', ?, 4, 3)`, status+"-1", status); err != nil {
			t.Fatalf("insert %s status: %v", status, err)
		}
	}
	upgrade, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000038_lingdoc_satisfied_change_sets.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), string(upgrade)); err != nil {
		t.Fatalf("apply satisfied status migration: %v", err)
	}
	var status, reason string
	if err := db.QueryRow("SELECT status, reason FROM lingdoc_change_sets WHERE id = 'stale-1'").Scan(&status, &reason); err != nil || status != "stale" || reason != "legacy proposal" {
		t.Fatalf("legacy row read-back: status=%q reason=%q err=%v", status, reason, err)
	}
	var fullWorkflowCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM lingdoc_change_sets WHERE status IN ('draft', 'stale', 'stabilized')").Scan(&fullWorkflowCount); err != nil || fullWorkflowCount != 3 {
		t.Fatalf("full workflow statuses were not preserved: count=%d err=%v", fullWorkflowCount, err)
	}
	if _, err := db.Exec(`INSERT INTO lingdoc_change_sets
		(id, project_id, created_by, reason, status, base_context_revision, base_spec_revision)
		VALUES ('satisfied-1', 'project-1', 'owner', 'already satisfied', 'satisfied', 4, 3)`); err != nil {
		t.Fatalf("insert satisfied status after upgrade: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO lingdoc_change_sets
		(id, project_id, created_by, reason, status, base_context_revision, base_spec_revision)
		VALUES ('invalid-1', 'project-1', 'owner', 'invalid', 'unknown', 4, 3)`); err == nil {
		t.Fatal("upgrade migration stopped enforcing the status constraint")
	}
	downgrade, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000038_lingdoc_satisfied_change_sets.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), string(downgrade)); err == nil {
		t.Fatal("downgrade silently discarded the satisfied terminal status")
	}
	var satisfiedCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM lingdoc_change_sets WHERE status = 'satisfied'").Scan(&satisfiedCount); err != nil || satisfiedCount != 1 {
		t.Fatalf("failed downgrade changed current schema/data: satisfied=%d err=%v", satisfiedCount, err)
	}
}
