package workspacecore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A blocking rule issue must be dismissible on PostgreSQL. The disposition
// write and its authorization audit row share one transaction, and the audit
// row's action column holds 64 characters on PostgreSQL. Writing the
// composite capability ("authorization.issue-dismiss-blocking:<uuid>", 73
// characters) overflowed that column; PostgreSQL rejected the write with
// SQLSTATE 22001 and the whole operation failed, which reached the transport
// as an unmapped 500. SQLite never caught it because its migration declares
// the column as unbounded TEXT.
//
// Requires an isolated TEST server, never the application's live database.
// One generated schema per run; production migration is executed unchanged.
func TestPostgresBlockingIssueDismissSurvivesAuditColumnWidth(t *testing.T) {
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
	schema := "g3gap_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	for _, file := range productionLingDocMigrationNames(t, "versioned") {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	if err := db.Exec("CREATE TABLE tenant_members ( role TEXT NOT NULL DEFAULT 'owner',tenant_id BIGINT, user_id TEXT, status TEXT, deleted_at TIMESTAMPTZ)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tenant_members (tenant_id,user_id,status) VALUES (1,'owner','active')").Error; err != nil {
		t.Fatal(err)
	}
	reader := &ruleSetSnapshotReaderStub{set: publishedWorkspaceRuleSet(t)}
	// The audit sink must be present: without it the service skips the audit
	// write entirely, which is why a sink-less reproduction of this defect
	// could not see it.
	service := NewServiceWithAudit(NewGORMRepository(db), nil, nil, nil, pgRegressionAuditSink{}).WithRuleSetSnapshotReader(reader)
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "owner"}

	created, code, _, err := service.CreateProject(ctx, actor, "pg-gap-create", CreateProjectInput{Name: "Postgres synthetic", TemplateID: "template-demo"})
	if err != nil || code != 201 {
		t.Fatalf("create: %d %v", code, err)
	}
	project := asProject(t, created)
	if _, _, _, err := service.SaveSpec(ctx, actor, project.ID, "pg-gap-spec", SaveSpecInput{Fields: map[string]string{"research_subject": "synthetic", "research_goal": "persistence"}}); err != nil {
		t.Fatal(err)
	}
	current, err := service.GetProject(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := service.ActivateProject(ctx, actor, project.ID, "pg-gap-activate", ActivateProjectInput{ExpectedSpecRevision: current.SpecRevision, ExpectedProjectVersion: current.ProjectVersion, ReviewedProjectVersion: current.ProjectVersion}); err != nil {
		t.Fatal(err)
	}
	after, err := service.GetProject(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}

	target := "5013efc8-24f7-4983-b853-3e8b53329acd"
	targetVersion := strconv.FormatInt(after.TemplateCopyVersion, 10)
	binding := ValidationIssueBinding{IssueID: "vi-pg-blocking-001", RuleID: "chapter-confirmed", RulesetHash: after.TemplateCopy.RulesetHash,
		Severity: "blocking", TargetID: target, TargetVersion: &targetVersion, ProjectVersion: after.ProjectVersion}
	raw, status, replayed, err := service.RecordValidationIssueDisposition(ctx, actor, after.ID, "pg-gap-dismiss-blocking", ValidationIssueDispositionInput{
		ExpectedProjectVersion: after.ProjectVersion, ValidationIssueBinding: binding, Action: "dismiss", Reason: "误报删除：阻断级规则问题"})
	if err != nil || status != 201 || replayed {
		t.Fatalf("blocking dismiss: status=%d replayed=%v err=%v", status, replayed, err)
	}
	var stored ValidationIssueDisposition
	if err := decodeStoredDisposition(raw, &stored); err != nil {
		t.Fatalf("decode disposition: %v", err)
	}
	if stored.Action != "dismiss" || stored.Severity != "blocking" || stored.IssueID != binding.IssueID {
		t.Fatalf("stored disposition = %+v", stored)
	}

	// The audit row keeps the category in the bounded column and the full
	// capability in its details.
	var rows []auditEventRow
	if err := db.Where("project_id = ? AND action LIKE ?", after.ID, "authorization.issue-dismiss%").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one blocking-dismiss authorization audit row, got %d", len(rows))
	}
	if len(rows[0].Action) > 64 {
		t.Fatalf("audit action %q is %d characters, column holds 64", rows[0].Action, len(rows[0].Action))
	}
	if rows[0].Action != "authorization.issue-dismiss-blocking" {
		t.Fatalf("audit action = %q, want the category only", rows[0].Action)
	}
	if !strings.Contains(rows[0].DetailsJSON, target) {
		t.Fatalf("audit details lost the resource: %s", rows[0].DetailsJSON)
	}
}

// pgRegressionAuditSink satisfies AuditSink without swallowing errors: the
// point of the regression is that an audit write failure must not be able to
// pass unnoticed.
type pgRegressionAuditSink struct{}

func (pgRegressionAuditSink) Record(context.Context, AuditEvent) error { return nil }

func decodeStoredDisposition(raw []byte, target *ValidationIssueDisposition) error {
	return json.Unmarshal(raw, target)
}
