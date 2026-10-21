package workspacecore

import (
	"context"
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

// The audit table's `action` column is a **category** column: it names what
// happened, not which object it happened to. PostgreSQL declared it
// varchar(64), so an over-long category was rejected there with SQLSTATE
// 22001 — that rejection is what made the #253 defect ("a legal request
// answered with an unmapped HTTP 500") visible. SQLite declared the same
// column as an unbounded TEXT and silently accepted the write, so the defect
// stayed invisible on the development machine.
//
// These tests prove the two dialects now reject the same write, and that the
// ordinary write paths still land.

const auditCategoryWidth = 64

// The category boundary is inclusive: a category of exactly the width is
// accepted, one character more is rejected. The pair pins the failure to the
// boundary itself — a foreign key or a missing column would reject both.
func TestSQLiteAuditCategoryColumnRejectsOverLongAction(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "audit-bounds.db"))
	actor := Actor{TenantID: 91, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	raw, _, _, err := svc.CreateProject(context.Background(), actor, "audit-bounds-project", CreateProjectInput{
		Name: "audit bounds", TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, raw).ID

	// The ordinary write path still lands: an authorization audit row carries a
	// category, and the column now has a bound.
	svc.audit = &auditSinkStub{}
	if err := svc.Authorize(context.Background(), actor, projectID, "read"); err != nil {
		t.Fatal(err)
	}
	var landed []auditEventRow
	if err := svc.testDB().Where("project_id = ? AND action = ?", projectID, "authorization.read").Find(&landed).Error; err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 {
		t.Fatalf("authorization audit rows = %d, want 1", len(landed))
	}

	// A category of exactly the width is accepted.
	atLimit := strings.Repeat("x", auditCategoryWidth)
	if err := insertAuditCategory(t, svc.testDB(), projectID, actor.UserID, atLimit); err != nil {
		t.Fatalf("SQLite rejected a category of exactly %d characters: %v", auditCategoryWidth, err)
	}

	// One character more is rejected, not truncated and not silently accepted.
	overLimit := strings.Repeat("x", auditCategoryWidth+1)
	if err := insertAuditCategory(t, svc.testDB(), projectID, actor.UserID, overLimit); err == nil {
		t.Fatal("SQLite accepted an over-long audit category")
	}
	var overLong int64
	if err := svc.testDB().Table("lingdoc_project_audits").
		Where("project_id = ? AND length(action) > ?", projectID, auditCategoryWidth).Count(&overLong).Error; err != nil {
		t.Fatal(err)
	}
	if overLong != 0 {
		t.Fatalf("SQLite stored %d audit categories longer than %d characters", overLong, auditCategoryWidth)
	}
}

// The same boundary on PostgreSQL, on the production versioned migration
// chain. The column type already rejected over-long categories; the explicit
// constraint (#276) puts the rule under the same name on both dialects.
//
// Requires an isolated TEST server, never the application's live database.
func TestPostgresAuditCategoryColumnRejectsOverLongAction(t *testing.T) {
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
	schema := "auditbounds_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	// The LingDoc migration chain expects the WeKnora tenant table to exist.
	if err := db.Exec("CREATE TABLE tenants (id BIGINT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	for _, file := range productionLingDocMigrationNames(t, "versioned") {
		raw, readErr := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", file))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := sqlDB.ExecContext(context.Background(), string(raw)); execErr != nil {
			t.Fatalf("%s: %v", file, execErr)
		}
	}

	// The explicit constraint is there, under the name the SQLite side uses.
	// PostgreSQL re-parses a varchar column as (action)::text inside the
	// definition, so the definition is checked for the bound, not for the
	// exact expression text.
	var definition string
	if err := db.Raw(
		"SELECT pg_get_constraintdef(oid) FROM pg_constraint " +
			"WHERE conrelid = 'lingdoc_project_audits'::regclass AND conname = ?",
		"ck_lingdoc_project_audits_action_len").Scan(&definition).Error; err != nil {
		t.Fatal(err)
	}
	if definition == "" {
		t.Fatal("the explicit audit category constraint is missing on PostgreSQL")
	}
	if !strings.Contains(definition, "length") || !strings.Contains(definition, "64") {
		t.Fatalf("audit category constraint = %q, want a length(action) <= 64 bound", definition)
	}

	if err := db.Exec("CREATE TABLE tenant_members ( role TEXT NOT NULL DEFAULT 'owner',tenant_id BIGINT, user_id TEXT, status TEXT, deleted_at TIMESTAMPTZ)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tenant_members (tenant_id,user_id,status) VALUES (1,'owner','active')").Error; err != nil {
		t.Fatal(err)
	}
	reader := &ruleSetSnapshotReaderStub{set: publishedWorkspaceRuleSet(t)}
	service := NewServiceWithAudit(NewGORMRepository(db), nil, nil, nil, pgRegressionAuditSink{}).WithRuleSetSnapshotReader(reader)
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "owner"}

	created, code, _, err := service.CreateProject(ctx, actor, "pg-audit-bounds", CreateProjectInput{Name: "Postgres synthetic", TemplateID: "template-demo"})
	if err != nil || code != 201 {
		t.Fatalf("create: %d %v", code, err)
	}
	projectID := asProject(t, created).ID

	// The ordinary write path still lands on PostgreSQL.
	if err := service.Authorize(ctx, actor, projectID, "read"); err != nil {
		t.Fatal(err)
	}
	var landed []auditEventRow
	if err := db.Where("project_id = ? AND action = ?", projectID, "authorization.read").Find(&landed).Error; err != nil {
		t.Fatal(err)
	}
	if len(landed) == 0 {
		t.Fatal("no authorization audit row landed on PostgreSQL")
	}
	for _, row := range landed {
		if row.Action != "authorization.read" || row.ActorID != actor.UserID || row.Target != projectID {
			t.Fatalf("incomplete audit row: %+v", row)
		}
	}

	// A category of exactly the width is accepted; one character more is not.
	atLimit := strings.Repeat("x", auditCategoryWidth)
	if err := insertAuditCategory(t, db, projectID, actor.UserID, atLimit); err != nil {
		t.Fatalf("PostgreSQL rejected a category of exactly %d characters: %v", auditCategoryWidth, err)
	}
	overLimit := strings.Repeat("x", auditCategoryWidth+1)
	if err := insertAuditCategory(t, db, projectID, actor.UserID, overLimit); err == nil {
		t.Fatal("PostgreSQL accepted an over-long audit category")
	}
	var overLong int64
	if err := db.Table("lingdoc_project_audits").
		Where("project_id = ? AND length(action) > ?", projectID, auditCategoryWidth).Count(&overLong).Error; err != nil {
		t.Fatal(err)
	}
	if overLong != 0 {
		t.Fatalf("PostgreSQL stored %d audit categories longer than %d characters", overLong, auditCategoryWidth)
	}
}

// insertAuditCategory writes one audit row straight through SQL, so the
// category bound is what decides the outcome. The project foreign key is
// satisfied by the caller.
func insertAuditCategory(t *testing.T, db *gorm.DB, projectID, actorID, action string) error {
	t.Helper()
	return db.Exec(
		"INSERT INTO lingdoc_project_audits (id, project_id, actor_id, action, target, details_json, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?)",
		uuid.NewString(), projectID, actorID, action, projectID, "{}", time.Now().UTC(),
	).Error
}
