package workspacecore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A failed audit write on an ordinary read has to become visible in the
// runtime observation stream, not only in a log line (#327).
//
// The alert is published on its own transaction, so it survives the failure
// it reports, and it never changes the authorization decision.

// installAuditWriteFailureTrigger makes every audit insert fail.
//
// ABORT undoes the failing statement and leaves the transaction usable,
// which is the fail-open shape an ordinary read has on SQLite: the read
// continues while the audit row is lost.
func installAuditWriteFailureTrigger(t *testing.T, svc *Service) {
	t.Helper()
	statement := `CREATE TRIGGER audit_write_failure BEFORE INSERT ON lingdoc_project_audits
BEGIN SELECT RAISE(ABORT, 'synthetic audit write failure'); END`
	if err := svc.testDB().Exec(statement).Error; err != nil {
		t.Fatal(err)
	}
}

func TestAuditFailurePublishesRuntimeEventAndAlert(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	// The audit sink has to be present: without it the service skips the
	// audit write entirely and there is no failure to report.
	svc.audit = &auditSinkStub{}
	ctx := context.Background()
	if _, _, _, err := svc.SaveRuntimeAlertRule(ctx, actor, p.ID, "audit-failure-rule", RuntimeAlertRule{
		Signal: "audit/failed", Severity: "P2", Action: "notify", ResponsibleID: actor.UserID,
	}); err != nil {
		t.Fatalf("save the audit failure rule: %v", err)
	}
	installAuditWriteFailureTrigger(t, svc)

	if err := svc.Authorize(ctx, actor, p.ID, "read"); err != nil {
		t.Fatalf("the audit failure stopped the read: %v", err)
	}

	events, err := svc.ListRuntimeEvents(ctx, actor, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Category != "audit" {
			continue
		}
		found = true
		if event.Status != "failed" {
			t.Fatalf("the audit failure event carries another status: %+v", event)
		}
		if event.ErrorCode != auditFailureErrorCode {
			t.Fatalf("the audit failure event carries another error code: %+v", event)
		}
		// The event reports the failure of an observability sink. It must not
		// carry what the audit was about: no capability, no details, no
		// research content.
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"authorization", "details", "spec", "body"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("the audit failure event carries %q: %s", forbidden, encoded)
			}
		}
	}
	if !found {
		t.Fatalf("the audit failure produced no runtime event: %+v", events)
	}

	alerts, err := svc.ListRuntimeAlerts(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 {
		t.Fatalf("the audit failure produced %d alerts, want 1: %+v", len(alerts), alerts)
	}
	if alerts[0].Severity != "P2" || alerts[0].Action != "notify" {
		t.Fatalf("the alert does not carry the rule's disposition: %+v", alerts[0])
	}
}

func TestAuditFailureDoesNotChangeTheAuthorizationDecision(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	svc.audit = &auditSinkStub{}
	ctx := context.Background()
	installAuditWriteFailureTrigger(t, svc)

	// A caller without access is still refused while the audit sink is down.
	if err := svc.Authorize(ctx, Actor{TenantID: actor.TenantID, UserID: "stranger"}, p.ID, "read"); err == nil {
		t.Fatal("the audit failure turned a denial into an allow")
	}
}

func TestAlertWriteFailureDoesNotBlockTheRead(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	svc.audit = &auditSinkStub{}
	ctx := context.Background()
	installAuditWriteFailureTrigger(t, svc)
	// The runtime record write fails too, so the alert cannot be published
	// either. Losing the alert may not cost the read.
	statement := `CREATE TRIGGER runtime_record_failure BEFORE INSERT ON lingdoc_g6_records
BEGIN SELECT RAISE(ABORT, 'synthetic runtime write failure'); END`
	if err := svc.testDB().Exec(statement).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.Authorize(ctx, actor, p.ID, "read"); err != nil {
		t.Fatalf("a failed alert stopped the read: %v", err)
	}
}

// installPostgresAuditWriteFailureTrigger makes every audit insert fail on
// PostgreSQL.
//
// This is not the SQLite shape. SQLite's RAISE(ABORT) undoes only the failing
// statement and leaves the transaction usable; PostgreSQL aborts the whole
// transaction, so the read's own transaction dies with the audit write. The
// alert is published on a separate transaction afterwards, which is exactly
// the property this test pins.
func installPostgresAuditWriteFailureTrigger(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`CREATE OR REPLACE FUNCTION synthetic_audit_write_failure() RETURNS trigger AS $$
BEGIN
	RAISE EXCEPTION 'synthetic audit write failure';
END;
$$ LANGUAGE plpgsql`,
		`CREATE TRIGGER audit_write_failure BEFORE INSERT ON lingdoc_project_audits
FOR EACH ROW EXECUTE FUNCTION synthetic_audit_write_failure()`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// TestAuditFailurePublishesRuntimeEventAndAlertOnPostgres runs the alert
// contract on the production dialect.
//
// On PostgreSQL the failed audit write aborts the read's transaction, so the
// read itself fails; that is a separate gap (#387) and not what this ticket
// delivers. What this ticket delivers is that the audit failure still becomes
// a runtime event and still matches the rule, so the read's outcome is
// recorded here and the alert is what the test asserts.
func TestAuditFailurePublishesRuntimeEventAndAlertOnPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset; requires an isolated disposable PostgreSQL server")
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
	schema := "pf3audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	}()
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
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
	// The production migration chain, unchanged.
	for _, file := range productionLingDocMigrationNames(t, "versioned") {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	members := "CREATE TABLE tenant_members ( role TEXT NOT NULL DEFAULT 'owner'," +
		"tenant_id BIGINT, user_id TEXT, status TEXT, deleted_at TIMESTAMPTZ)"
	if err := db.Exec(members).Error; err != nil {
		t.Fatal(err)
	}
	owner := "INSERT INTO tenant_members (tenant_id,user_id,status) VALUES (1,'owner','active')"
	if err := db.Exec(owner).Error; err != nil {
		t.Fatal(err)
	}
	reader := &ruleSetSnapshotReaderStub{set: publishedWorkspaceRuleSet(t)}
	svc := NewService(NewGORMRepository(db)).WithRuleSetSnapshotReader(reader)
	// The audit sink has to be present: without it the service skips the
	// audit write entirely and there is no failure to report.
	svc.audit = &auditSinkStub{}
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "owner"}
	created, code, _, err := svc.CreateProject(ctx, actor, "pg-audit-create-key", CreateProjectInput{
		Name: "PostgreSQL audit sink", TemplateID: "template-demo",
	})
	if err != nil || code != 201 {
		t.Fatalf("create: %d %v", code, err)
	}
	p := asProject(t, created)
	if _, _, _, err := svc.SaveRuntimeAlertRule(ctx, actor, p.ID, "pg-audit-failure-rule", RuntimeAlertRule{
		Signal: "audit/failed", Severity: "P2", Action: "notify", ResponsibleID: actor.UserID,
	}); err != nil {
		t.Fatalf("save the audit failure rule: %v", err)
	}
	installPostgresAuditWriteFailureTrigger(t, db)

	readErr := svc.Authorize(ctx, actor, p.ID, "read")
	t.Logf("the read on PostgreSQL after the audit write failed: %v", readErr)

	events, err := svc.ListRuntimeEvents(ctx, actor, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Category != "audit" {
			continue
		}
		found = true
		if event.Status != "failed" {
			t.Fatalf("the audit failure event carries another status: %+v", event)
		}
		if event.ErrorCode != auditFailureErrorCode {
			t.Fatalf("the audit failure event carries another error code: %+v", event)
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"authorization", "details", "spec", "body"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("the audit failure event carries %q: %s", forbidden, encoded)
			}
		}
	}
	if !found {
		t.Fatalf("the audit failure produced no runtime event on PostgreSQL: %+v", events)
	}

	alerts, err := svc.ListRuntimeAlerts(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 {
		t.Fatalf("the audit failure produced %d alerts on PostgreSQL, want 1: %+v", len(alerts), alerts)
	}
	if alerts[0].Severity != "P2" || alerts[0].Action != "notify" {
		t.Fatalf("the alert does not carry the rule's disposition: %+v", alerts[0])
	}
}
