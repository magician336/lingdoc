package templategovernance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This optional contract test uses a disposable schema and never touches the
// application's tables. An unset DSN is explicitly NOT RUN, not a parity pass.
func TestPostgresCatalogSchemaLifecycleAndAuditRollback(t *testing.T) {
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
	schema := fmt.Sprintf("template_governance_%d", time.Now().UnixNano())
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
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	}()
	if err := db.Exec("CREATE TABLE tenants(id BIGINT PRIMARY KEY);INSERT INTO tenants(id) VALUES(1)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE audit_logs(id BIGSERIAL PRIMARY KEY,tenant_id BIGINT NOT NULL,actor_user_id TEXT NOT NULL,actor_role TEXT NOT NULL,action TEXT NOT NULL,target_type TEXT NOT NULL,target_id TEXT NOT NULL,outcome TEXT NOT NULL,details JSONB NOT NULL,scope_type TEXT NOT NULL,scope_id TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", "000115_lingdoc_rule_sets.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(PostgreSQLDDL).Error; err != nil {
		t.Fatal(err)
	}
	auth := &testAuth{allow: true}
	actor := Actor{TenantID: 1, UserID: "owner", Role: string(types.TenantRoleAdmin)}
	store := rulesetstore.NewStore(db, auth)
	set := template.RuleSet{ID: "grant-rules", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{{RuleID: "goal-present", DefinitionID: "presence", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal", Severity: "warning", TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: "rules-admin"}}}
	if err := store.CreateDraft(context.Background(), actor, set); err != nil {
		t.Fatal(err)
	}
	set, err = store.Publish(context.Background(), actor, set.ID, set.Version)
	if err != nil {
		t.Fatal(err)
	}
	ref := RuleSetRef{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}
	fixtures := fixtureReader{}
	for _, scene := range []string{"write_check", "preflight"} {
		for _, polarity := range []string{"positive", "negative"} {
			fields := map[string]string{}
			status := template.EvaluationIssue
			if polarity == "positive" {
				fields["research_goal"] = "observable"
				status = template.EvaluationPass
			}
			id := scene + "-" + polarity
			fixtures[id] = ReplayFixture{ID: id, RuleSet: ref, RuleID: "goal-present", Scene: scene, Polarity: polarity, TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "fixture", TargetVersion: "fixture/1", Fields: fields}, ExpectedStatus: status}
		}
	}
	s := NewService(db, Ports{Authorizer: auth, RuleSets: auth, Replay: ExampleReplayVerifier{Reader: fixtures}})
	if _, err := s.CreateProfile(context.Background(), actor, "postgres-profile", CreateProfileInput{ID: "grant-template", Name: "Internal template", Reason: "reviewed template"}); err != nil {
		t.Fatal(err)
	}
	draft, err := s.SaveDraft(context.Background(), actor, "postgres-draft-1", draftInput(ref, "1", 0))
	if err != nil {
		t.Fatal(err)
	}
	// This PostgreSQL contract isolates the SQL public projection under the
	// package's testAuth. Production role normalization/freshActor behavior is
	// covered on PostgreSQL by
	// workspace.TestPostgresTemplateCatalogVisibilityUsesProductionMembership.
	reader := Actor{TenantID: actor.TenantID, UserID: "catalog-reader", Role: "viewer"}
	if profiles, err := s.ListProfiles(context.Background(), reader, CatalogFilter{}); err != nil || len(profiles) != 0 {
		t.Fatalf("PostgreSQL draft-only profile leaked: %v, %v", profiles, err)
	}
	published := publishTestVersion(t, s, actor, draft)
	if profiles, err := s.ListProfiles(context.Background(), reader, CatalogFilter{State: template.RuleSetPublished}); err != nil || len(profiles) != 1 {
		t.Fatalf("PostgreSQL published catalog read: %v, %v", profiles, err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, err := s.ResolveForCopy(context.Background(), tx, actor, published.TemplateID, published.Version)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(context.Background(), actor, "postgres-stale-publish", PublishInput{TemplateID: published.TemplateID, Version: published.Version, ExpectedRevision: published.Revision + 1, ReplayFixtureIDs: []string{"write_check-positive", "write_check-negative", "preflight-positive", "preflight-negative"}, Reason: "stale version must be audited"}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale PostgreSQL publish = %v", err)
	}
	if countRows(t, db, "audit_logs", "action=? AND outcome=?", "lingdoc.template.publish", "failure") != 1 {
		t.Fatal("PostgreSQL rejected publish lost its failure audit")
	}
	// A fresh pool and service must recover the exact version, audit trail and
	// idempotency record without relying on the first service's memory.
	reopened, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	reopenedPool, err := reopened.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopenedPool.Close(); err != nil {
			t.Errorf("close reopened test database: %v", err)
		}
	}()
	restarted := NewService(reopened, Ports{Authorizer: auth, RuleSets: auth, Replay: ExampleReplayVerifier{Reader: fixtures}})
	recovered, err := restarted.GetVersion(context.Background(), actor, published.TemplateID, published.Version)
	if err != nil || recovered.Revision != published.Revision || recovered.State != published.State || recovered.ContentHash != published.ContentHash || recovered.RulesetHash != published.RulesetHash {
		t.Fatalf("PostgreSQL restart lost exact published version: %+v, %v", recovered, err)
	}
	if _, err := restarted.CreateProfile(context.Background(), actor, "postgres-profile", CreateProfileInput{ID: "grant-template", Name: "Internal template", Reason: "reviewed template"}); err != nil {
		t.Fatalf("PostgreSQL restart lost idempotency receipt: %v", err)
	}
	var recoveredAudit struct{ Details string }
	if err := reopened.Table("audit_logs").Select("details").Where("action=? AND outcome=?", "lingdoc.template.publish", "success").Take(&recoveredAudit).Error; err != nil || !strings.Contains(recoveredAudit.Details, published.ContentHash) {
		t.Fatalf("PostgreSQL restart lost publication audit/hash: %s, %v", recoveredAudit.Details, err)
	}
	raceDraft, err := restarted.SaveDraft(context.Background(), actor, "postgres-racing-draft", draftInput(ref, "race", 0))
	if err != nil {
		t.Fatal(err)
	}
	raceInput := PublishInput{TemplateID: raceDraft.TemplateID, Version: raceDraft.Version, ExpectedRevision: raceDraft.Revision,
		ReplayFixtureIDs: []string{"write_check-positive", "write_check-negative", "preflight-positive", "preflight-negative"}, Reason: "concurrent publication must commit once"}
	type publication struct {
		key string
		err error
	}
	start := make(chan struct{})
	results := make(chan publication, 2)
	for _, key := range []string{"postgres-publish-race-a", "postgres-publish-race-b"} {
		go func(key string) {
			<-start
			_, err := restarted.Publish(context.Background(), actor, key, raceInput)
			results <- publication{key: key, err: err}
		}(key)
	}
	close(start)
	winner := ""
	for range 2 {
		result := <-results
		if result.err == nil {
			if winner != "" {
				t.Fatal("concurrent PostgreSQL publications both succeeded")
			}
			winner = result.key
		} else if !errors.Is(result.err, ErrVersionConflict) && !errors.Is(result.err, ErrInvalidState) {
			t.Fatalf("concurrent PostgreSQL publication returned an unclassified failure: %v", result.err)
		}
	}
	if winner == "" || countRows(t, reopened, "audit_logs", "action=? AND outcome=? AND target_id=?", "lingdoc.template.publish", "success", raceDraft.TemplateID+"/"+raceDraft.Version) != 1 {
		t.Fatal("concurrent PostgreSQL publication lost its unique success/audit")
	}
	if replayed, err := restarted.Publish(context.Background(), actor, winner, raceInput); err != nil || replayed.Revision != raceDraft.Revision+1 {
		t.Fatalf("PostgreSQL publication retry was not idempotent: %+v, %v", replayed, err)
	}
	if err := db.Exec(`CREATE FUNCTION deny_template_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='lingdoc.template.save_draft' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END$$; CREATE TRIGGER deny_template_audit BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION deny_template_audit()`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDraft(context.Background(), actor, "postgres-failed-draft", draftInput(ref, "2", 0)); err == nil {
		t.Fatal("audit failure succeeded")
	}
	if countRows(t, db, "lingdoc_template_versions", "version=?", "2") != 0 || countRows(t, db, "lingdoc_template_governance_operations", "idempotency_key=?", "postgres-failed-draft") != 0 {
		t.Fatal("PostgreSQL rollback left partial writes")
	}
}
