package rulesetstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	defer rootSQL.Close()

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
	defer sqlDB.Close()
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
	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", "000115_lingdoc_rule_sets.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatal(err)
	}

	store := NewStore(db, testAuthorizer{allow: true})
	actor := Actor{TenantID: 1, UserID: "pg-admin", Role: "tenant_admin"}
	draft := template.RuleSet{
		ID: "pg-rules", Version: "v1", State: template.RuleSetDraft,
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
}
