package workspacecore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The target version and the project revision are first-class columns of the
// audit event (#327). A reader must get them back without parsing Details,
// and Details must not carry a second copy of them: two places to hold one
// fact is how the shape of the audit store drifts.
//
// The migration must also leave the rows that were written before it alone.
// Audit is append-only, and a row that predates the columns means "this
// record carries no such concept", not "version empty" and "revision zero".

// auditVersionMigrationIndex locates the audit version field migration inside
// the production chain. The migration number is a shared serial resource and
// other faces keep adding migrations after it, so the rehearsal locates it by
// name instead of by position.
func auditVersionMigrationIndex(tb testing.TB, files []string) int {
	tb.Helper()
	for i, file := range files {
		if strings.Contains(file, "audit_version_fields") {
			return i
		}
	}
	tb.Fatalf("the audit version fields migration is not in the production chain: %v", files)
	return -1
}

func TestAuditEventVersionFieldsRoundTrip(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "audit-version.db"))
	actor := Actor{TenantID: 91, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	svc.audit = &auditSinkStub{}
	raw, _, _, err := svc.CreateProject(context.Background(), actor, "audit-version-project", CreateProjectInput{
		Name: "audit version", TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, raw).ID

	event := AuditEvent{
		ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID,
		Action: "project.change_set.apply", Target: "target-1",
		TargetVersion: "v7", ProjectRevision: 12,
		Decision: "allow", CreatedAt: time.Now().UTC(),
	}
	if err := (gormTransaction{svc.testDB()}).RecordAudit(event); err != nil {
		t.Fatalf("record the audit event: %v", err)
	}

	events, err := (gormTransaction{svc.testDB()}).AuditEvents(projectID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range events {
		if item.ID != event.ID {
			continue
		}
		found = true
		if item.TargetVersion != "v7" || item.ProjectRevision != 12 {
			t.Fatalf("the audit event lost its version facts: %+v", item)
		}
		for _, key := range []string{"target_version", "project_revision"} {
			if _, duplicated := item.Details[key]; duplicated {
				t.Fatalf("the version facts were duplicated into details under %q: %+v", key, item.Details)
			}
		}
	}
	if !found {
		t.Fatalf("the written audit event did not come back: %+v", events)
	}

	// The facts live in columns of their own, not inside the JSON details.
	var row auditEventRow
	if err := svc.testDB().Where("id = ?", event.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.TargetVersion != "v7" || row.ProjectRevision != 12 {
		t.Fatalf("the audit row does not carry the version facts in columns: %+v", row)
	}
}

func TestAuditEventVersionFieldsDefaultWhenAbsent(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "audit-version-default.db"))
	actor := Actor{TenantID: 91, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	svc.audit = &auditSinkStub{}
	raw, _, _, err := svc.CreateProject(context.Background(), actor, "audit-version-default-project",
		CreateProjectInput{Name: "audit version default", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, raw).ID

	event := AuditEvent{
		ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID,
		Action: "authorization.read", Target: projectID,
		Decision: "allow", CreatedAt: time.Now().UTC(),
	}
	if err := (gormTransaction{svc.testDB()}).RecordAudit(event); err != nil {
		t.Fatalf("record the audit event: %v", err)
	}

	events, err := (gormTransaction{svc.testDB()}).AuditEvents(projectID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range events {
		if item.ID != event.ID {
			continue
		}
		found = true
		if item.TargetVersion != "" || item.ProjectRevision != 0 {
			t.Fatalf("an absent version fact came back as a value: %+v", item)
		}
	}
	if !found {
		t.Fatalf("the written audit event did not come back: %+v", events)
	}

	// The columns hold their default, not NULL: a missing fact has one
	// readable shape instead of two.
	var row auditEventRow
	if err := svc.testDB().Where("id = ?", event.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.TargetVersion != "" || row.ProjectRevision != 0 {
		t.Fatalf("the audit row did not keep the column defaults: %+v", row)
	}
}

func TestSQLiteAuditVersionMigrationLeavesHistoricRowsUntouched(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "audit-version-migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	files := productionLingDocMigrationNames(t, "sqlite")
	if len(files) == 0 {
		t.Fatal("no production SQLite migrations found")
	}
	index := auditVersionMigrationIndex(t, files)
	up := files[index]
	for _, file := range files[:index] {
		raw, readErr := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", file))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := db.ExecContext(context.Background(), string(raw)); execErr != nil {
			t.Fatalf("%s: %v", file, execErr)
		}
	}

	// A row written before the migration existed.
	if err := insertAuditRow(t, db, "historic-row", "project-historic", "owner", "authorization.read",
		"project-historic", `{"capability":"read"}`, "2026-01-01 00:00:00"); err != nil {
		t.Fatalf("the historic row must be writable before the migration: %v", err)
	}

	body, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", up))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), string(body)); err != nil {
		t.Fatalf("%s: %v", up, err)
	}

	// The historic row keeps every fact it had, and its new columns hold
	// their default rather than being back-filled.
	var targetVersion string
	var projectRevision int64
	var action, target, details string
	const historicRow = "SELECT target_version, project_revision, action, target, details_json " +
		"FROM lingdoc_project_audits WHERE id = ?"
	if err := db.QueryRow(historicRow, "historic-row").
		Scan(&targetVersion, &projectRevision, &action, &target, &details); err != nil {
		t.Fatal(err)
	}
	if targetVersion != "" || projectRevision != 0 {
		t.Fatalf("the migration back-filled a historic row: target_version=%q project_revision=%d",
			targetVersion, projectRevision)
	}
	if action != "authorization.read" || target != "project-historic" || !strings.Contains(details, "read") {
		t.Fatalf("the migration rewrote a historic row: action=%q target=%q details=%q", action, target, details)
	}

	// The rollback removes the columns and keeps the row.
	down := strings.Replace(up, ".up.sql", ".down.sql", 1)
	downBody, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", down))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), string(downBody)); err != nil {
		t.Fatalf("%s: %v", down, err)
	}
	var schema string
	const tableSchema = "SELECT sql FROM sqlite_master WHERE type='table' AND name='lingdoc_project_audits'"
	if err := db.QueryRow(tableSchema).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(schema, "target_version") || strings.Contains(schema, "project_revision") {
		t.Fatalf("the rollback left the version columns behind: %s", schema)
	}
	var remaining int
	const countHistoric = "SELECT count(*) FROM lingdoc_project_audits WHERE id = ?"
	if err := db.QueryRow(countHistoric, "historic-row").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("the rollback dropped a historic row: %d rows left", remaining)
	}
}

// The two dialects must express the same rule under the same column names.
// A column that exists on one side only is a schema that answers different
// questions depending on where the database runs.
func TestAuditVersionMigrationDeclaresTheSameColumnsInBothDialects(t *testing.T) {
	addColumn := regexp.MustCompile(`ADD\s+COLUMN\s+([a-z_]+)`)
	columns := func(tb testing.TB, path string) []string {
		tb.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			tb.Fatal(err)
		}
		found := map[string]bool{}
		for _, match := range addColumn.FindAllStringSubmatch(string(body), -1) {
			found[match[1]] = true
		}
		if len(found) == 0 {
			tb.Fatalf("no ADD COLUMN found in %s", path)
		}
		names := make([]string, 0, len(found))
		for name := range found {
			names = append(names, name)
		}
		return names
	}
	sqlitePath := filepath.Join("..", "..", "..", "migrations", "sqlite",
		"000052_lingdoc_audit_version_fields.up.sql")
	postgresPath := filepath.Join("..", "..", "..", "migrations", "versioned",
		"000131_lingdoc_audit_version_fields.up.sql")
	sqlite := columns(t, sqlitePath)
	postgres := columns(t, postgresPath)
	// The set of columns has to match; the order two files declare them in
	// does not, because both dialects append the new columns at the end.
	slices.Sort(sqlite)
	slices.Sort(postgres)
	if !slices.Equal(sqlite, postgres) {
		t.Fatalf("the dialects declare different columns: sqlite=%v postgres=%v", sqlite, postgres)
	}
	for _, name := range []string{"target_version", "project_revision"} {
		if !slices.Contains(sqlite, name) {
			t.Fatalf("the migration does not declare %q: %v", name, sqlite)
		}
	}
}
