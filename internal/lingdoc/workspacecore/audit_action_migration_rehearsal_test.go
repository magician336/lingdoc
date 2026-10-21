package workspacecore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A migration that adds a bound to a column that already holds rows has to
// deal with those rows. #276 adds the audit category bound, so this test
// rehearses the migration on a database that really holds a legacy row.
//
// The legacy row is the shape the pre-#253 writer produced: the category with
// the resource it applied to. At 73 characters it does not fit the bound.
//
//   - On SQLite the old column was unbounded TEXT, so such a row could exist.
//     The migration normalizes it in place (the resource stays in the
//     details) and the bound then holds.
//   - On PostgreSQL the column was varchar(64), so such a row could never
//     exist — the rejection is exactly the #253 defect. The migration adds the
//     explicit constraint next to the type bound.

func TestSQLiteAuditBoundMigrationNormalizesLegacyRows(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy-audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	files := productionLingDocMigrationNames(t, "sqlite")
	if len(files) == 0 {
		t.Fatal("no production SQLite migrations found")
	}
	last := files[len(files)-1]
	if !strings.Contains(last, "audit_action_bounds") {
		t.Fatalf("last production SQLite migration = %q, want the audit bounds one", last)
	}
	// Everything before the bound migration, so the database is a real
	// pre-migration database.
	for _, file := range files[:len(files)-1] {
		raw, readErr := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", file))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := db.ExecContext(context.Background(), string(raw)); execErr != nil {
			t.Fatalf("%s: %v", file, execErr)
		}
	}

	const legacyAction = "authorization.issue-dismiss-blocking:5013efc8-24f7-4983-b853-3e8b53329acd"
	const capability = "issue-dismiss-blocking:5013efc8-24f7-4983-b853-3e8b53329acd"
	if _, err := db.Exec(
		"INSERT INTO lingdoc_project_audits (id, project_id, actor_id, action, target, details_json, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?)",
		"legacy-row", "project-legacy", "owner", legacyAction, "project-legacy",
		`{"capability":"`+capability+`"}`, "2026-01-01 00:00:00",
	); err != nil {
		t.Fatalf("the legacy shape must be writable before the bound migration: %v", err)
	}

	// Now the bound migration runs.
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", last))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), string(raw)); err != nil {
		t.Fatalf("%s: %v", last, err)
	}

	var action, details string
	if err := db.QueryRow(
		"SELECT action, details_json FROM lingdoc_project_audits WHERE id = ?", "legacy-row").Scan(&action, &details); err != nil {
		t.Fatal(err)
	}
	if action != "authorization.issue-dismiss-blocking" {
		t.Fatalf("legacy action = %q, want the normalized category", action)
	}
	if len(action) > auditCategoryWidth {
		t.Fatalf("normalized action %q is %d characters, the column holds %d", action, len(action), auditCategoryWidth)
	}
	if !strings.Contains(details, capability) {
		t.Fatalf("the resource was lost from the details: %q", details)
	}

	// The bound now holds for new writes too.
	if _, err := db.Exec(
		"INSERT INTO lingdoc_project_audits (id, project_id, actor_id, action, target, details_json, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?)",
		"over-long-row", "project-legacy", "owner", strings.Repeat("x", auditCategoryWidth+1),
		"project-legacy", "{}", "2026-01-01 00:00:00",
	); err == nil {
		t.Fatal("the bound migration did not bound the column")
	}

	// The down migration widens the column again, and the up migration puts
	// the bound back: the pair is a round trip.
	down, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite",
		strings.TrimSuffix(last, ".up.sql")+".down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), string(down)); err != nil {
		t.Fatalf("%s (down): %v", last, err)
	}
	if _, err := db.Exec(
		"INSERT INTO lingdoc_project_audits (id, project_id, actor_id, action, target, details_json, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?)",
		"over-long-row", "project-legacy", "owner", strings.Repeat("x", auditCategoryWidth+1),
		"project-legacy", "{}", "2026-01-01 00:00:00",
	); err != nil {
		t.Fatalf("the down migration did not widen the column: %v", err)
	}
	// The operator's action before re-applying the bound: the over-long row
	// has to go (or be normalized) first — that is the whole reason #276 asks
	// for the inventory to run before the constraint is added.
	if _, err := db.Exec("DELETE FROM lingdoc_project_audits WHERE id = ?", "over-long-row"); err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", last))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), string(up)); err != nil {
		t.Fatalf("%s (up again): %v", last, err)
	}
	if _, err := db.Exec(
		"INSERT INTO lingdoc_project_audits (id, project_id, actor_id, action, target, details_json, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?)",
		"over-long-row", "project-legacy", "owner", strings.Repeat("x", auditCategoryWidth+1),
		"project-legacy", "{}", "2026-01-01 00:00:00",
	); err == nil {
		t.Fatal("the up migration did not restore the bound")
	}
}

// The PostgreSQL side of the same rehearsal: the legacy shape never fitted
// the column, so the rejection happens before the migration runs. The
// migration only adds the explicit constraint next to the type bound.
func TestPostgresAuditBoundMigrationAddsTheExplicitConstraint(t *testing.T) {
	dsn := os.Getenv("LINGDOC_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset")
	}
	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	rootSQL, err := root.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rootSQL.Close() }()
	schema := "auditmigration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	}()
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := db.Exec("CREATE TABLE tenants (id BIGINT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}

	files := productionLingDocMigrationNames(t, "versioned")
	if len(files) == 0 {
		t.Fatal("no production versioned migrations found")
	}
	last := files[len(files)-1]
	if !strings.Contains(last, "audit_action_bounds") {
		t.Fatalf("last production versioned migration = %q, want the audit bounds one", last)
	}
	for _, file := range files[:len(files)-1] {
		raw, readErr := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", file))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := sqlDB.ExecContext(context.Background(), string(raw)); execErr != nil {
			t.Fatalf("%s: %v", file, execErr)
		}
	}

	// The legacy shape does not fit varchar(64): PostgreSQL rejected it, and
	// that rejection is the #253 defect.
	const legacyAction = "authorization.issue-dismiss-blocking:5013efc8-24f7-4983-b853-3e8b53329acd"
	if err := db.Exec(
		"INSERT INTO lingdoc_project_audits (id, project_id, actor_id, action, target, details_json, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?)",
		"legacy-row", "project-legacy", "owner", legacyAction, "project-legacy",
		`{"capability":"issue-dismiss-blocking:5013efc8-24f7-4983-b853-3e8b53329acd"}`, time.Now().UTC(),
	); err == nil {
		t.Fatal("PostgreSQL accepted the legacy shape; the varchar(64) bound is gone")
	}

	// The migration adds the explicit constraint.
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", last))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(context.Background(), string(raw)); err != nil {
		t.Fatalf("%s: %v", last, err)
	}
	var definition string
	if err := db.Raw(
		"SELECT pg_get_constraintdef(oid) FROM pg_constraint "+
			"WHERE conrelid = 'lingdoc_project_audits'::regclass AND conname = ?",
		"ck_lingdoc_project_audits_action_len").Scan(&definition).Error; err != nil {
		t.Fatal(err)
	}
	if definition == "" {
		t.Fatal("the explicit audit category constraint is missing after the migration")
	}

	// The down migration drops the explicit constraint. The column type
	// varchar(64) stays, so PostgreSQL still refuses an over-long category:
	// the type bound was there before this ticket and does not depend on it.
	down, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned",
		strings.TrimSuffix(last, ".up.sql")+".down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(context.Background(), string(down)); err != nil {
		t.Fatalf("%s (down): %v", last, err)
	}
	var afterDown string
	if err := db.Raw(
		"SELECT coalesce(max(pg_get_constraintdef(oid)), '') FROM pg_constraint "+
			"WHERE conrelid = 'lingdoc_project_audits'::regclass AND conname = ?",
		"ck_lingdoc_project_audits_action_len").Scan(&afterDown).Error; err != nil {
		t.Fatal(err)
	}
	if afterDown != "" {
		t.Fatalf("the down migration left the explicit constraint behind: %q", afterDown)
	}
	if err := db.Exec(
		"INSERT INTO lingdoc_project_audits (id, project_id, actor_id, action, target, details_json, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?)",
		"over-long-row", "project-legacy", "owner", strings.Repeat("x", auditCategoryWidth+1),
		"project-legacy", "{}", time.Now().UTC(),
	); err == nil {
		t.Fatal("PostgreSQL accepted an over-long category after the down migration; the varchar(64) bound is gone")
	}
}

// An over-long row that the operator did not handle makes the bound
// migration fail loudly. It must not be truncated silently: the audit history
// keeps its values, and the operator gets an error that names the rule.
//
// The production runner applies each migration inside a transaction, so a
// failed migration leaves the database on the previous version. This test
// replays the script the way the fixture does, which is why it builds its own
// database.
func TestSQLiteAuditBoundMigrationRefusesUnhandledOverLongRows(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "unhandled-audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	files := productionLingDocMigrationNames(t, "sqlite")
	last := files[len(files)-1]
	for _, file := range files[:len(files)-1] {
		raw, readErr := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", file))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := db.ExecContext(context.Background(), string(raw)); execErr != nil {
			t.Fatalf("%s: %v", file, execErr)
		}
	}

	// A row the migration cannot normalize: not the old authorization shape.
	if _, err := db.Exec(
		"INSERT INTO lingdoc_project_audits (id, project_id, actor_id, action, target, details_json, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?)",
		"odd-row", "project-legacy", "owner", strings.Repeat("y", auditCategoryWidth+1),
		"project-legacy", "{}", "2026-01-01 00:00:00",
	); err != nil {
		t.Fatalf("the unbounded column must accept the row first: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", last))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), string(raw)); err == nil {
		t.Fatal("the bound migration accepted an over-long row it could not normalize")
	} else if !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("the bound migration failed for the wrong reason: %v", err)
	}
}
