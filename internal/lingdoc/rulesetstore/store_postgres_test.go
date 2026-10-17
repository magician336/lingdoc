package rulesetstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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
	if err := db.Exec("CREATE TABLE tenants (id BIGINT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tenants (id) VALUES (1)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE audit_logs (
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
	)`).Error; err != nil {
		t.Fatal(err)
	}
	// These three existing tables are extended by the production migrations
	// under test. Keep only the columns needed by their ALTER statements and FK.
	for _, statement := range []string{
		`CREATE TABLE lingdoc_projects (id VARCHAR(64) PRIMARY KEY, tenant_id BIGINT NOT NULL)`,
		`CREATE TABLE lingdoc_project_template_copies (id VARCHAR(64) PRIMARY KEY)`,
		`CREATE TABLE lingdoc_export_artifacts (id VARCHAR(64) PRIMARY KEY, project_id VARCHAR(64) NOT NULL, snapshot_id VARCHAR(64) NOT NULL)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{
		"000115_lingdoc_rule_sets.up.sql",
		"000118_lingdoc_ruleset_export_reference.up.sql",
		"000119_lingdoc_ruleset_use_audits.up.sql",
		"000120_lingdoc_template_version_ruleset_ref.up.sql",
	} {
		migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(string(migration)).Error; err != nil {
			t.Fatalf("apply PostgreSQL migration %s: %v", name, err)
		}
	}

	store := NewStore(db, testAuthorizer{allow: true})
	actor := Actor{TenantID: 1, UserID: "pg-admin", Role: "tenant_admin"}
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
	if err := db.Exec("INSERT INTO lingdoc_projects (id, tenant_id) VALUES (?, ?)", "project-pg-ruleset", actor.TenantID).Error; err != nil {
		t.Fatal(err)
	}
	event := template.RuleSetUseAuditEvent{
		TenantID: actor.TenantID, ActorID: actor.UserID, ActorRole: actor.Role,
		ProjectID: "project-pg-ruleset", Action: "preflight", RuleSetID: published.ID,
		RuleSetVersion: published.Version, RuleSetHash: published.RulesetHash,
		EffectiveRulesetHash: strings.Repeat("a", 64), TemplateID: "template-demo", TemplateVersion: "1",
		TemplateCopyID: "copy-pg-ruleset", TemplateCopyVersion: 2, ProjectVersion: 7,
		ResourceID: "snapshot-pg-ruleset", Outcome: "passed", CreatedAt: time.Now().UTC(),
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
