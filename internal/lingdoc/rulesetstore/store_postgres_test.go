package rulesetstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/database/fixturemigrations"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// postgresFixtureTables declares the production tables this fixture depends on.
//
// The migration version numbers are deliberately absent: they are resolved from
// migrations/versioned by table name (issue #386). The previous hand-copied list
// of five files was a subset that nothing kept honest, so the next migration to
// add a column to these tables would have left this fixture building a schema
// the server no longer has.
var postgresFixtureTables = []string{
	"lingdoc_projects",
	"lingdoc_members",
	"lingdoc_export_artifacts",
	"lingdoc_release_snapshots",
	"lingdoc_project_template_copies",
	"lingdoc_rule_sets",
	"lingdoc_ruleset_use_audits",
	"lingdoc_ruleset_metrics",
}

// postgresFixtureStubs are platform tables the fixture builds itself. The
// LingDoc migration chain only references them (tenants via foreign keys) or
// does not touch them at all (audit_logs), so applying the selected migrations
// never collides with these stubs.
var postgresFixtureStubs = []string{"tenants", "audit_logs"}

// This contract test uses a disposable schema and applies the production
// migration unchanged. It never targets the application database.
func TestPostgresRuleSetMigrationAndLifecycle(t *testing.T) {
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
	defer func() {
		if err := rootSQL.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	}()

	schema := fmt.Sprintf("ruleset_store_%d", time.Now().UnixNano())
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
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	}()
	// These platform tables are not owned by the LingDoc migration chain: it
	// either only references them or never touches them. The fixture builds
	// them as stubs, and every LingDoc-owned table below comes from the
	// production migrations selected by postgresFixtureTables.
	for _, statement := range []string{
		`CREATE TABLE tenants (id BIGINT PRIMARY KEY)`,
		`INSERT INTO tenants (id) VALUES (1)`,
		`CREATE TABLE audit_logs (
		id BIGSERIAL PRIMARY KEY,
		tenant_id BIGINT NOT NULL,
		actor_user_id VARCHAR(36) NOT NULL DEFAULT '',
		actor_role VARCHAR(32) NOT NULL DEFAULT '',
		action VARCHAR(64) NOT NULL,
		target_type VARCHAR(32) NOT NULL DEFAULT '',
		target_id VARCHAR(64) NOT NULL DEFAULT '',
		target_user_id VARCHAR(36) NOT NULL DEFAULT '',
		request_path VARCHAR(512) NOT NULL DEFAULT '',
		request_method VARCHAR(16) NOT NULL DEFAULT '',
		outcome VARCHAR(16) NOT NULL DEFAULT 'success',
		details JSONB NOT NULL DEFAULT '{}'::JSONB,
		scope_type VARCHAR(32) NOT NULL DEFAULT '',
		scope_id VARCHAR(64) NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	migrations, err := fixturemigrations.Select(filepath.Join("..", "..", "..", "migrations", "versioned"), postgresFixtureTables)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PostgreSQL fixture applies %d production LingDoc migrations through %s",
		len(migrations.Names), migrations.Names[len(migrations.Names)-1])
	statements, err := migrations.SQL(filepath.Join("..", "..", "..", "migrations", "versioned"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range statements {
		if err := db.Exec(statement.SQL).Error; err != nil {
			t.Fatalf("apply PostgreSQL migration %s: %v", statement.Name, err)
		}
	}

	store := NewStore(db, testAuthorizer{allow: true})
	actor := Actor{TenantID: 1, UserID: "pg-admin", Role: string(types.TenantRoleAdmin)}
	draft := template.RuleSet{
		ID: strings.Repeat("r", 64), Version: strings.Repeat("v", 64), State: template.RuleSetDraft,
		Bindings: []template.RuleBinding{{
			RuleID: "goal-required", DefinitionID: "presence", DefinitionVersion: "1",
			Origin: "organization", TargetKind: "field", TargetID: "research_goal",
			Severity: "warning", TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "tenant-1",
		}},
	}
	if err := store.CreateDraft(context.Background(), actor, draft); err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(context.Background(), actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if published.State != template.RuleSetPublished || published.RulesetHash == "" {
		t.Fatalf("published ruleset = %+v", published)
	}
	stored, err := store.GetVersion(context.Background(), actor, draft.ID, draft.Version)
	if err != nil || stored.RulesetHash != published.RulesetHash {
		t.Fatalf("read-back = %+v, error = %v", stored, err)
	}
	var auditCount int64
	if err := db.Table("audit_logs").Where("tenant_id = ? AND scope_id = ?", actor.TenantID, draft.ID).Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("audit rows = %d, want 2", auditCount)
	}
	var audit struct {
		TargetID string
		Details  string
	}
	if err := db.Table("audit_logs").Select("target_id, details").
		Where("tenant_id = ? AND scope_id = ? AND action = ?", actor.TenantID, draft.ID, "lingdoc.ruleset.publish").
		Take(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if len(audit.TargetID) != 64 || audit.TargetID != auditTargetID(draft.ID, draft.Version) {
		t.Fatalf("PostgreSQL audit target_id = %q (len %d), want deterministic 64-character reference", audit.TargetID, len(audit.TargetID))
	}
	if !strings.Contains(audit.Details, draft.ID) || !strings.Contains(audit.Details, draft.Version) {
		t.Fatalf("PostgreSQL audit details lost the full identity: %s", audit.Details)
	}
	if err := db.Exec(`INSERT INTO lingdoc_projects
		(id, tenant_id, name, status, project_version, spec_revision, template_id, template_version)
		VALUES (?, ?, ?, 'active', 1, 0, 'template-pg-ruleset', 'v1')`,
		"project-pg-ruleset", actor.TenantID, "PostgreSQL ruleset fixture").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)", "project-pg-ruleset", actor.UserID, "owner").Error; err != nil {
		t.Fatal(err)
	}
	verifiedTemplate, verifiedBinding := verifiedAuditEvidence(t, published)
	event := template.RuleSetUseAuditEvent{
		TenantID: actor.TenantID, ActorID: actor.UserID, ActorRole: actor.Role,
		ProjectID: "project-pg-ruleset", Action: "preflight", RuleSetID: published.ID,
		RuleSetVersion: published.Version, RuleSetHash: published.RulesetHash,
		EffectiveRulesetHash: verifiedBinding.EffectiveHash, TemplateID: verifiedTemplate.ID, TemplateVersion: verifiedTemplate.Version,
		TemplateCopyID: "copy-pg-ruleset", TemplateCopyVersion: 2, ProjectVersion: 7,
		ResourceID: "snapshot-pg-ruleset", Outcome: "passed", CreatedAt: time.Now().UTC(), VerifiedTemplate: &verifiedTemplate, VerifiedBinding: &verifiedBinding,
	}
	if err := store.RecordRuleSetUse(context.Background(), event); err != nil {
		t.Fatalf("record PostgreSQL RuleSet-use audit: %v", err)
	}
	readBack, err := store.ListRuleSetUseAudits(context.Background(), actor, event.ProjectID)
	if err != nil || len(readBack) != 1 || readBack[0].RuleSetID != published.ID ||
		readBack[0].RuleSetVersion != published.Version || readBack[0].RuleSetHash != published.RulesetHash ||
		readBack[0].EffectiveRulesetHash != event.EffectiveRulesetHash || readBack[0].ResourceID != event.ResourceID {
		t.Fatalf("PostgreSQL RuleSet-use audit readback = %+v, err=%v", readBack, err)
	}
	metrics, err := store.ReadRuleSetMetrics(context.Background(), actor, event.ProjectID, RuleSetMetricsQuery{})
	if err != nil || metrics.AuditQueryCount == 0 || metrics.AuditPageSizeCount == 0 || metrics.AuditQueryP95Ms <= 0 || metrics.AuditQueryP99Ms < metrics.AuditQueryP95Ms {
		t.Fatalf("PostgreSQL RuleSet metrics readback = %+v, err=%v", metrics, err)
	}
	for _, tableColumn := range [][2]string{
		{"lingdoc_export_artifacts", "ruleset_ref_json"},
		{"lingdoc_project_template_copies", "ruleset_ref_json"},
		{"lingdoc_ruleset_use_audits", "ruleset_hash"},
	} {
		var count int64
		if err := db.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?", schema, tableColumn[0], tableColumn[1]).Scan(&count).Error; err != nil || count != 1 {
			t.Fatalf("PostgreSQL migration column %s.%s count=%d err=%v", tableColumn[0], tableColumn[1], count, err)
		}
	}
}
