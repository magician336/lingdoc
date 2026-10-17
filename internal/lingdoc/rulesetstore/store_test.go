package rulesetstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
)

type testAuthorizer struct {
	allow bool
}

func (a testAuthorizer) AuthorizeRuleSet(_ context.Context, _ *gorm.DB, actor Actor, _ Action, _ *template.RuleSet) error {
	if !a.allow || actor.Role != "tenant_admin" {
		return errors.New("permission denied")
	}
	return nil
}

func TestStorePersistsImmutableVersionsTransitionsAndAudit(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "admin-1", Role: "tenant_admin"}
	draft := testDraft("v1")

	if err := store.CreateDraft(ctx, actor, draft); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDraft(ctx, actor, draft); !errors.Is(err, ErrRuleSetVersionExists) {
		t.Fatalf("duplicate draft error = %v, want ErrRuleSetVersionExists", err)
	}

	published, err := store.Publish(ctx, actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if published.State != template.RuleSetPublished || published.RulesetHash == "" {
		t.Fatalf("published ruleset = %+v", published)
	}
	if _, err := store.Publish(ctx, actor, draft.ID, draft.Version); !errors.Is(err, template.ErrRuleSetNotDraft) {
		t.Fatalf("publishing an immutable version again = %v, want ErrRuleSetNotDraft", err)
	}

	withdrawn, err := store.Withdraw(ctx, actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.State != template.RuleSetWithdrawn || withdrawn.RulesetHash != published.RulesetHash {
		t.Fatalf("withdrawal changed historical binding hash: %+v", withdrawn)
	}
	if _, err := store.Publish(ctx, actor, draft.ID, draft.Version); !errors.Is(err, template.ErrRuleSetNotDraft) {
		t.Fatalf("withdrawn version was mutable: %v", err)
	}

	stored, err := store.GetVersion(ctx, actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != template.RuleSetWithdrawn || stored.RulesetHash != published.RulesetHash || len(stored.Bindings) != len(draft.Bindings) {
		t.Fatalf("historical read-back = %+v", stored)
	}

	var audits int64
	if err := db.Table("audit_logs").Where("tenant_id = ? AND scope_type = ? AND scope_id = ?", actor.TenantID, "lingdoc_ruleset", draft.ID).Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 3 {
		t.Fatalf("audit records = %d, want create, publish, withdraw", audits)
	}
}

func TestRuleSetUseAuditPersistsAndReadsExactIdentity(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "admin-1", Role: "tenant_admin"}
	draft := testDraft("v-use-audit")
	if err := store.CreateDraft(ctx, actor, draft); err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(ctx, actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_projects (id) VALUES (?)", "project-audit").Error; err != nil {
		t.Fatal(err)
	}
	event := template.RuleSetUseAuditEvent{
		TenantID: actor.TenantID, ActorID: actor.UserID, ActorRole: actor.Role,
		ProjectID: "project-audit", Action: "preflight", RuleSetID: published.ID,
		RuleSetVersion: published.Version, RuleSetHash: published.RulesetHash,
		EffectiveRulesetHash: strings.Repeat("a", 64), TemplateID: "template-demo", TemplateVersion: "1",
		TemplateCopyID: "copy-1", TemplateCopyVersion: 2, ProjectVersion: 7, ResourceID: "snapshot-1", Outcome: "blocked",
		CreatedAt: time.Now().UTC()}
	if err := store.RecordRuleSetUse(ctx, event); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListRuleSetUseAudits(ctx, actor, event.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].RuleSetID != published.ID || events[0].RuleSetVersion != published.Version ||
		events[0].RuleSetHash != published.RulesetHash || events[0].EffectiveRulesetHash != event.EffectiveRulesetHash ||
		events[0].TemplateCopyVersion != 2 || events[0].ResourceID != "snapshot-1" || events[0].Outcome != "blocked" {
		t.Fatalf("use audit readback lost its exact identity: %+v", events)
	}
	bad := event
	bad.RuleSetHash = "client-fabricated"
	if err := store.RecordRuleSetUse(ctx, bad); !errors.Is(err, ErrInvalidRuleSetUseAudit) {
		t.Fatalf("hash mismatch error = %v, want ErrInvalidRuleSetUseAudit", err)
	}
	events, err = store.ListRuleSetUseAudits(ctx, actor, event.ProjectID)
	if err != nil || len(events) != 1 {
		t.Fatalf("failed audit must not leave a partial record: len=%d err=%v", len(events), err)
	}
}

func TestGetVersionForBindingReadsPublishedVersionOnCallerTransaction(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "admin-1", Role: "tenant_admin"}
	draft := testDraft("v1")
	if err := store.CreateDraft(ctx, actor, draft); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(ctx, actor, draft.ID, draft.Version); err != nil {
		t.Fatal(err)
	}
	var bound template.RuleSet
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		bound, err = store.GetVersionForBinding(ctx, tx, actor, draft.ID, draft.Version)
		return err
	}); err != nil {
		t.Fatalf("GetVersionForBinding in caller transaction: %v", err)
	}
	if bound.State != template.RuleSetPublished || bound.RulesetHash == "" {
		t.Fatalf("transactional binding snapshot = %+v, want published immutable version", bound)
	}
	if _, err := store.Withdraw(ctx, actor, draft.ID, draft.Version); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		current, err := store.GetVersionForBinding(ctx, tx, actor, draft.ID, draft.Version)
		if err != nil {
			return err
		}
		if current.State != template.RuleSetWithdrawn {
			t.Fatalf("transactional read after withdrawal = %s, want withdrawn", current.State)
		}
		return nil
	}); err != nil {
		t.Fatalf("GetVersionForBinding after withdrawal: %v", err)
	}
}

func TestConcurrentRuleSetWithdrawWaitsForBindingTransaction(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "admin-1", Role: "tenant_admin"}
	draft := testDraft("v1")
	if err := store.CreateDraft(ctx, actor, draft); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(ctx, actor, draft.ID, draft.Version); err != nil {
		t.Fatal(err)
	}

	bindTx := db.Begin()
	if bindTx.Error != nil {
		t.Fatal(bindTx.Error)
	}
	defer func() {
		if bindTx.Statement != nil && bindTx.Statement.ConnPool != nil {
			_ = bindTx.Rollback().Error
		}
	}()
	set, err := store.GetVersionForBinding(ctx, bindTx, actor, draft.ID, draft.Version)
	if err != nil || set.State != template.RuleSetPublished {
		t.Fatalf("locked binding snapshot = %+v, error = %v", set, err)
	}

	started := make(chan struct{})
	withdrawn := make(chan error, 1)
	go func() {
		close(started)
		_, err := store.Withdraw(ctx, actor, draft.ID, draft.Version)
		withdrawn <- err
	}()
	<-started
	var earlyErr error
	select {
	case err := <-withdrawn:
		if err == nil {
			t.Fatal("withdraw committed before the binding transaction ended")
		}
		earlyErr = err // SQLite may fail fast with SQLITE_BUSY instead of waiting.
	case <-time.After(150 * time.Millisecond):
	}
	if err := bindTx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if earlyErr != nil {
		if _, err := store.Withdraw(ctx, actor, draft.ID, draft.Version); err != nil {
			t.Fatalf("retry withdraw after binding transaction: %v (initial lock result: %v)", err, earlyErr)
		}
	} else {
		select {
		case err := <-withdrawn:
			if err != nil {
				t.Fatalf("withdraw after binding transaction: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("withdraw did not resume after the binding transaction committed")
		}
	}
	current, err := store.GetVersion(ctx, actor, draft.ID, draft.Version)
	if err != nil || current.State != template.RuleSetWithdrawn {
		t.Fatalf("RuleSet final state = %+v, error = %v; want withdrawn after the serialized bind", current, err)
	}
}

func TestStoreIsTenantScopedAndAuthorizationFailsClosed(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "admin-1", Role: "tenant_admin"}
	draft := testDraft("v1")
	if err := store.CreateDraft(ctx, actor, draft); err != nil {
		t.Fatal(err)
	}

	otherTenant := Actor{TenantID: 2, UserID: "admin-2", Role: "tenant_admin"}
	if _, err := store.GetVersion(ctx, otherTenant, draft.ID, draft.Version); !errors.Is(err, ErrRuleSetNotFound) {
		t.Fatalf("cross-tenant read error = %v, want ErrRuleSetNotFound", err)
	}

	denied := NewStore(db, testAuthorizer{allow: false})
	if _, err := denied.GetVersion(ctx, actor, draft.ID, draft.Version); err == nil {
		t.Fatal("unauthorized read succeeded")
	}
	if err := NewStore(db, nil).CreateDraft(ctx, actor, testDraft("v2")); !errors.Is(err, ErrAuthorizerUnavailable) {
		t.Fatalf("nil authorizer error = %v, want ErrAuthorizerUnavailable", err)
	}
}

func TestStoreRollsBackRuleSetWhenAuditWriteFails(t *testing.T) {
	db := openStoreTestDB(t, false)
	store := NewStore(db, testAuthorizer{allow: true})
	actor := Actor{TenantID: 1, UserID: "admin-1", Role: "tenant_admin"}
	if err := store.CreateDraft(context.Background(), actor, testDraft("v1")); err == nil {
		t.Fatal("CreateDraft succeeded without the required audit table")
	}
	var count int64
	if err := db.Table("lingdoc_rule_sets").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("ruleset rows = %d after audit failure, want rollback to 0", count)
	}
}

func TestStoreBoundsAuditTargetForMaximumRuleSetIdentity(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	actor := Actor{TenantID: 1, UserID: "admin-1", Role: "tenant_admin"}
	draft := testDraft(strings.Repeat("v", 64))
	draft.ID = strings.Repeat("r", 64)

	if err := store.CreateDraft(context.Background(), actor, draft); err != nil {
		t.Fatalf("CreateDraft(max identity) error = %v", err)
	}
	if _, err := store.Publish(context.Background(), actor, draft.ID, draft.Version); err != nil {
		t.Fatalf("Publish(max identity) error = %v", err)
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
		t.Fatalf("audit target_id = %q (len %d), want deterministic 64-character reference", audit.TargetID, len(audit.TargetID))
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(audit.Details), &details); err != nil {
		t.Fatal(err)
	}
	if details["ruleset_id"] != draft.ID || details["version"] != draft.Version {
		t.Fatalf("audit details do not retain the original identity: %+v", details)
	}

	overlong := testDraft("v1")
	overlong.ID = strings.Repeat("r", 65)
	if err := store.CreateDraft(context.Background(), actor, overlong); !errors.Is(err, template.ErrInvalidRuleSet) {
		t.Fatalf("CreateDraft(overlong identity) error = %v, want ErrInvalidRuleSet", err)
	}
}

func TestStoreListsVersionsNewestFirstAndKeepsDraftHashServerOwned(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "admin-1", Role: "tenant_admin"}

	older := testDraft("v1")
	if err := store.CreateDraft(ctx, actor, older); err != nil {
		t.Fatal(err)
	}
	newer := testDraft("v2")
	if err := store.CreateDraft(ctx, actor, newer); err != nil {
		t.Fatal(err)
	}
	versions, err := store.ListVersions(ctx, actor, newer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != "v2" || versions[1].Version != "v1" {
		t.Fatalf("versions = %+v, want v2 then v1", versions)
	}

	withClientHash := testDraft("v3")
	withClientHash.RulesetHash = "client-controlled"
	if err := store.CreateDraft(ctx, actor, withClientHash); !errors.Is(err, ErrDraftHashMustBeEmpty) {
		t.Fatalf("client hash error = %v, want ErrDraftHashMustBeEmpty", err)
	}
}

func openStoreTestDB(t *testing.T, withAudit bool) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rulesets.db")
	db, err := gorm.Open(sqlite.Dialector{
		DriverName: "sqlite",
		DSN:        path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)",
	}, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("CREATE TABLE tenants (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tenants (id) VALUES (1), (2)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE lingdoc_projects (id TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000036_lingdoc_rule_sets.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatal(err)
	}
	usageMigration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000040_lingdoc_ruleset_use_audits.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(usageMigration)).Error; err != nil {
		t.Fatal(err)
	}
	if withAudit {
		if err := db.Exec(`CREATE TABLE audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL,
			actor_user_id VARCHAR(36) NOT NULL DEFAULT '',
			actor_role VARCHAR(32) NOT NULL DEFAULT '',
			action VARCHAR(64) NOT NULL,
			target_type VARCHAR(32) NOT NULL DEFAULT '',
			target_id VARCHAR(64) NOT NULL DEFAULT '',
			target_user_id VARCHAR(36) NOT NULL DEFAULT '',
			request_path VARCHAR(512) NOT NULL DEFAULT '',
			request_method VARCHAR(16) NOT NULL DEFAULT '',
			outcome VARCHAR(16) NOT NULL DEFAULT 'success',
			details TEXT NOT NULL DEFAULT '{}',
			scope_type VARCHAR(32) NOT NULL DEFAULT '',
			scope_id VARCHAR(64) NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func testDraft(version string) template.RuleSet {
	return template.RuleSet{
		ID: "proposal-rules", Version: version, State: template.RuleSetDraft,
		Bindings: []template.RuleBinding{{
			RuleID: "research-goal-required", DefinitionID: "presence", DefinitionVersion: "1",
			Origin: "organization", TargetKind: "field", TargetID: "research_goal",
			Severity: "warning", Message: "Research goal is required", TriggerScenes: []string{"preflight"},
			Enabled: true, Owner: "tenant-1",
		}},
	}
}
