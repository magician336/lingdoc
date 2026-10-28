package templategovernance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// PostgreSQL parity for replay fixture registration (Spec #768, ticket #769
// acceptance row 2): registration, pin deactivation and the shared coverage
// computation must reach the same conclusions on the versioned PostgreSQL
// chain as on SQLite. This optional contract test uses a disposable schema and
// never touches the application's tables. An unset DSN is explicitly NOT RUN,
// not a parity pass.
// openPostgresGovernanceFixture opens one disposable PostgreSQL schema with
// the tenants/audit_logs stubs, the versioned rule_sets migration and the
// package DDL applied — the shared substrate of the fixture-registration
// parity tests. An unset DSN is explicitly NOT RUN, not a parity pass.
func openPostgresGovernanceFixture(t *testing.T) *gorm.DB {
	t.Helper()
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
	t.Cleanup(func() {
		if err := rootSQL.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	schema := fmt.Sprintf("template_replay_fixtures_%d", time.Now().UnixNano())
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	})
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
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
	return db
}

func TestPostgresReplayFixtureRegistrationPinDeactivationAndCoverage(t *testing.T) {
	db := openPostgresGovernanceFixture(t)
	auth := &testAuth{allow: true}
	actor := Actor{TenantID: 1, UserID: "owner", Role: string(types.TenantRoleAdmin)}
	set := template.RuleSet{ID: "grant-rules", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{{RuleID: "goal-present", DefinitionID: "presence", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal", Severity: template.SeverityWarning, TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: "rule-admin"}}}
	store := rulesetstore.NewStore(db, auth)
	if err := store.CreateDraft(context.Background(), actor, set); err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(context.Background(), actor, set.ID, set.Version)
	if err != nil {
		t.Fatal(err)
	}
	fixtureStore := NewReplayFixtureStore(db, []ReplayFixtureRecord{{Fixture: ReplayFixture{ID: "reserved-builtin", RuleID: "goal-present"}, TemplateID: "builtin", TemplateVersion: "1", TemplateContentHash: "builtin-hash", PolicyHash: "builtin-policy", ReviewedBy: "builtin", ReviewReference: "repository:builtin", ReviewedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}})
	s := NewService(db, Ports{Authorizer: auth, RuleSets: auth, Replay: ExampleReplayVerifier{Reader: fixtureStore}, Usage: testUsage{}, Sources: sourceVerifier{allow: true}, Fixtures: fixtureStore})
	if _, err := s.CreateProfile(context.Background(), actor, "postgres-profile", CreateProfileInput{ID: "grant-template", Name: "Internal template", Reason: "reviewed template"}); err != nil {
		t.Fatal(err)
	}
	draft, err := s.SaveDraft(context.Background(), actor, "postgres-draft-1", draftInput(RuleSetRef{ID: published.ID, Version: published.Version, Hash: published.RulesetHash}, "1", 0))
	if err != nil {
		t.Fatal(err)
	}

	// Registration on the versioned chain fixes the same server-computed pin
	// and answers the same coverage matrix.
	receipt, err := s.RegisterFixtures(context.Background(), actor, "postgres-register-1", RegisterFixturesInput{TemplateID: draft.TemplateID, Version: draft.Version, Fixtures: []ReplayFixtureInput{
		{FixtureID: "pg-write_check-positive", RuleID: "goal-present", Scene: "write_check", Polarity: "positive", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg", TargetVersion: "1", Fields: map[string]string{"research_goal": "observable objective"}}, ExpectedStatus: template.EvaluationPass, ReviewReference: "review:pg-1"},
		{FixtureID: "pg-write_check-negative", RuleID: "goal-present", Scene: "write_check", Polarity: "negative", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg", TargetVersion: "1", Fields: map[string]string{}}, ExpectedStatus: template.EvaluationIssue, ReviewReference: "review:pg-1"},
		{FixtureID: "pg-preflight-positive", RuleID: "goal-present", Scene: "preflight", Polarity: "positive", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg", TargetVersion: "1", Fields: map[string]string{"research_goal": "observable objective"}}, ExpectedStatus: template.EvaluationPass, ReviewReference: "review:pg-1"},
		{FixtureID: "pg-preflight-negative", RuleID: "goal-present", Scene: "preflight", Polarity: "negative", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg", TargetVersion: "1", Fields: map[string]string{}}, ExpectedStatus: template.EvaluationIssue, ReviewReference: "review:pg-1"},
	}, Reason: "postgres parity"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ContentHash != draft.ContentHash || receipt.PolicyHash != draft.PolicyHash || !receipt.Coverage.Complete || len(receipt.Registered) != 4 {
		t.Fatalf("postgres registration receipt = %+v", receipt)
	}
	record, err := fixtureStore.GetFixture(context.Background(), actor.TenantID, "pg-write_check-negative")
	if err != nil || record.TemplateContentHash != draft.ContentHash || record.Fixture.ExpectedStatus != template.EvaluationIssue {
		t.Fatalf("postgres stored record = %+v %v", record, err)
	}
	if !fixtureStore.ConflictsBuiltin("reserved-builtin") {
		t.Fatal("postgres builtin conflict answer wrong")
	}
	if err := (ExampleReplayVerifier{Reader: fixtureStore}).Verify(context.Background(), actor, draft, []string{"pg-write_check-positive", "pg-write_check-negative", "pg-preflight-positive", "pg-preflight-negative"}); err != nil {
		t.Fatalf("postgres verify with registered fixtures: %v", err)
	}
	if _, err := s.RegisterFixtures(context.Background(), actor, "postgres-register-builtin", RegisterFixturesInput{TemplateID: draft.TemplateID, Version: draft.Version, Fixtures: []ReplayFixtureInput{{FixtureID: "reserved-builtin", RuleID: "goal-present", Scene: "write_check", Polarity: "positive", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg", TargetVersion: "1", Fields: map[string]string{"research_goal": "x"}}, ExpectedStatus: template.EvaluationPass, ReviewReference: "review:pg-2"}}, Reason: "postgres builtin conflict"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("postgres builtin id conflict = %v", err)
	}
	view, err := s.ReplayCoverage(context.Background(), actor, draft.TemplateID, draft.Version, nil)
	if err != nil || !view.Complete || view.ContentHash != draft.ContentHash {
		t.Fatalf("postgres coverage view = %+v %v", view, err)
	}

	// Pin deactivation: the re-saved draft stops answering the old rows, which
	// survive append-only, and the coverage view follows the current pin.
	def := template.Clone(draft.Definition)
	def.Rules = nil
	def.ContentHash = ""
	def.RulesetHash = ""
	def.Fields[0].Label = "renamed goal"
	if _, err := s.SaveDraft(context.Background(), actor, "postgres-resave-1", SaveDraftInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, Definition: def, RuleSet: draft.RuleSet, SourceType: "lingdoc_builtin", Scope: "internal-demo", Curator: actor.UserID, Reason: "content changed"}); err != nil {
		t.Fatal(err)
	}
	resaved, err := s.GetVersion(context.Background(), actor, draft.TemplateID, draft.Version)
	if err != nil || resaved.ContentHash == draft.ContentHash {
		t.Fatalf("postgres re-saved draft = %v %v", resaved.ContentHash, err)
	}
	err = (ExampleReplayVerifier{Reader: fixtureStore}).Verify(context.Background(), actor, resaved, []string{"pg-write_check-positive", "pg-write_check-negative", "pg-preflight-positive", "pg-preflight-negative"})
	if !errors.Is(err, ErrReplayRequired) {
		t.Fatalf("postgres stale pin verify = %v, want the replay evidence family", err)
	}
	if _, err := s.RegisterFixtures(context.Background(), actor, "postgres-register-after-resave", RegisterFixturesInput{TemplateID: draft.TemplateID, Version: draft.Version, Fixtures: postgresRefixtures(), Reason: "postgres re-registration"}); err != nil {
		t.Fatal(err)
	}
	var rows int64
	if err := db.Table("lingdoc_template_replay_fixtures").Where("tenant_id=?", actor.TenantID).Count(&rows).Error; err != nil || rows != 8 {
		t.Fatalf("postgres append-only rows = %d, %v", rows, err)
	}
	staleView, err := s.ReplayCoverage(context.Background(), actor, draft.TemplateID, draft.Version, nil)
	if err != nil || !staleView.Complete || staleView.ContentHash != resaved.ContentHash {
		t.Fatalf("postgres current-pin coverage = complete:%v content:%s %v", staleView.Complete, staleView.ContentHash, err)
	}
}

func postgresRefixtures() []ReplayFixtureInput {
	return []ReplayFixtureInput{
		{FixtureID: "pg2-write_check-positive", RuleID: "goal-present", Scene: "write_check", Polarity: "positive", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg", TargetVersion: "2", Fields: map[string]string{"research_goal": "observable objective"}}, ExpectedStatus: template.EvaluationPass, ReviewReference: "review:pg-3"},
		{FixtureID: "pg2-write_check-negative", RuleID: "goal-present", Scene: "write_check", Polarity: "negative", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg", TargetVersion: "2", Fields: map[string]string{}}, ExpectedStatus: template.EvaluationIssue, ReviewReference: "review:pg-3"},
		{FixtureID: "pg2-preflight-positive", RuleID: "goal-present", Scene: "preflight", Polarity: "positive", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg", TargetVersion: "2", Fields: map[string]string{"research_goal": "observable objective"}}, ExpectedStatus: template.EvaluationPass, ReviewReference: "review:pg-3"},
		{FixtureID: "pg2-preflight-negative", RuleID: "goal-present", Scene: "preflight", Polarity: "negative", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg", TargetVersion: "2", Fields: map[string]string{}}, ExpectedStatus: template.EvaluationIssue, ReviewReference: "review:pg-3"},
	}
}

func postgresProjectionFixtures(prefix string) []ReplayFixtureInput {
	return []ReplayFixtureInput{
		{FixtureID: prefix + "write_check-positive", RuleID: "goal-present", Scene: "write_check", Polarity: "positive", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg-projection", TargetVersion: "1", Fields: map[string]string{"research_goal": "observable objective"}}, ExpectedStatus: template.EvaluationPass, ReviewReference: "review:pg-projection"},
		{FixtureID: prefix + "write_check-negative", RuleID: "goal-present", Scene: "write_check", Polarity: "negative", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg-projection", TargetVersion: "1", Fields: map[string]string{}}, ExpectedStatus: template.EvaluationIssue, ReviewReference: "review:pg-projection"},
		{FixtureID: prefix + "preflight-positive", RuleID: "goal-present", Scene: "preflight", Polarity: "positive", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg-projection", TargetVersion: "1", Fields: map[string]string{"research_goal": "observable objective"}}, ExpectedStatus: template.EvaluationPass, ReviewReference: "review:pg-projection"},
		{FixtureID: prefix + "preflight-negative", RuleID: "goal-present", Scene: "preflight", Polarity: "negative", TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "pg-projection", TargetVersion: "1", Fields: map[string]string{}}, ExpectedStatus: template.EvaluationIssue, ReviewReference: "review:pg-projection"},
	}
}

// PostgreSQL parity for the ruleset projection registration (Spec #768,
// ticket #771): the projection pin, the projected coverage readback, the
// bootstrap demo base and the base re-save's pin semantics must reach the
// same conclusions on the versioned chain as on SQLite.
func TestPostgresRuleSetProjectionRegistrationCoverageAndPinParity(t *testing.T) {
	db := openPostgresGovernanceFixture(t)
	ctx := context.Background()
	auth := &testAuth{allow: true}
	actor := Actor{TenantID: 1, UserID: "owner", Role: string(types.TenantRoleAdmin)}
	binding := template.RuleBinding{RuleID: "goal-present", DefinitionID: "presence", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal", Severity: template.SeverityWarning, TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: "rule-admin"}

	// The replay base: a published rule set, a profile and the draft template
	// bound to it — the second chain's organization draft.
	baseSet := template.RuleSet{ID: "base-rules", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{binding}}
	store := rulesetstore.NewStore(db, auth)
	if err := store.CreateDraft(ctx, actor, baseSet); err != nil {
		t.Fatal(err)
	}
	publishedBase, err := store.Publish(ctx, actor, baseSet.ID, baseSet.Version)
	if err != nil {
		t.Fatal(err)
	}
	fixtureStore := NewReplayFixtureStore(db, nil)
	s := NewService(db, Ports{Authorizer: auth, RuleSets: auth, Replay: ExampleReplayVerifier{Reader: fixtureStore}, Usage: testUsage{}, Sources: sourceVerifier{allow: true}, Fixtures: fixtureStore})
	if _, err := s.CreateProfile(ctx, actor, "postgres-projection-profile", CreateProfileInput{ID: "grant-template", Name: "Internal template", Reason: "reviewed template"}); err != nil {
		t.Fatal(err)
	}
	base, err := s.SaveDraft(ctx, actor, "postgres-projection-draft-1", draftInput(RuleSetRef{ID: publishedBase.ID, Version: publishedBase.Version, Hash: publishedBase.RulesetHash}, "1", 0))
	if err != nil {
		t.Fatal(err)
	}

	// The second rule set draft whose publication the projection unlocks.
	if err := store.CreateDraft(ctx, actor, template.RuleSet{ID: "next-rules", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{binding}}); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.RegisterRuleSetFixtures(ctx, actor, "pg-ruleset-register-1", RegisterRuleSetFixturesInput{RuleSetID: "next-rules", RuleSetVersion: "1", TemplateID: base.TemplateID, TemplateVersion: base.Version, Fixtures: postgresProjectionFixtures("pgp-"), Reason: "postgres projection parity"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TemplateID != base.TemplateID || receipt.Version != base.Version || receipt.ContentHash == base.ContentHash {
		t.Fatalf("postgres projection receipt pin = %+v", receipt)
	}
	if receipt.Projection == nil || receipt.Projection.RuleSet.ID != "next-rules" || receipt.Projection.RuleSet.Hash == "" || receipt.Projection.ReplayBase.TemplateID != base.TemplateID {
		t.Fatalf("postgres projection reference = %+v", receipt.Projection)
	}
	if !receipt.Coverage.Complete || len(receipt.Registered) != 4 {
		t.Fatalf("postgres projection receipt = %d registered, complete=%v", len(receipt.Registered), receipt.Coverage.Complete)
	}

	// The readback seam answers the same matrix over the same pin.
	view, err := s.ReplayCoverage(ctx, actor, base.TemplateID, base.Version, &RuleSetCoverageQuery{RuleSetID: "next-rules", RuleSetVersion: "1"})
	if err != nil || !view.Complete || view.ContentHash != receipt.ContentHash || view.Projection == nil || view.Projection.RuleSet.ID != "next-rules" {
		t.Fatalf("postgres projected coverage view = complete:%v content:%s projection:%+v %v", view.Complete, view.ContentHash, view.Projection, err)
	}

	// The bootstrap path: the builtin demo base needs no stored version.
	demoReceipt, err := s.RegisterRuleSetFixtures(ctx, actor, "pg-ruleset-register-demo", RegisterRuleSetFixturesInput{RuleSetID: "next-rules", RuleSetVersion: "1", TemplateID: template.DemoTemplateID, TemplateVersion: template.DemoTemplateVersion, Fixtures: postgresProjectionFixtures("pgd-"), Reason: "postgres bootstrap parity"})
	if err != nil {
		t.Fatal(err)
	}
	if demoReceipt.TemplateID != template.DemoTemplateID || demoReceipt.Version != template.DemoTemplateVersion || demoReceipt.Projection == nil || demoReceipt.Projection.ReplayBase.TemplateID != template.DemoTemplateID {
		t.Fatalf("postgres demo bootstrap receipt = %+v", demoReceipt)
	}

	// The success audit carries both references and the PG JSONB filter of the
	// admin audit history reads it.
	var receipts int64
	if err := db.Table("audit_logs").Where("action=? AND outcome=? AND details -> 'details' -> 'replay_base' ->> 'template_id' = ?", "lingdoc.template.register_fixture", "success", base.TemplateID).Count(&receipts).Error; err != nil || receipts != 1 {
		t.Fatalf("postgres audit receipts = %d, %v", receipts, err)
	}

	// Pin semantics: the re-saved base moves the projection, the stored
	// receipt no longer answers (idempotent replay conflict), and a fresh
	// registration fixes the new pin while the old rows survive append-only.
	def := template.Clone(base.Definition)
	def.Rules = nil
	def.ContentHash = ""
	def.RulesetHash = ""
	def.Fields[0].Label = "renamed goal"
	if _, err := s.SaveDraft(ctx, actor, "postgres-projection-resave-1", SaveDraftInput{TemplateID: base.TemplateID, Version: base.Version, ExpectedRevision: base.Revision, Definition: def, RuleSet: base.RuleSet, SourceType: "lingdoc_builtin", Scope: "internal-demo", Curator: actor.UserID, Reason: "content changed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterRuleSetFixtures(ctx, actor, "pg-ruleset-register-1", RegisterRuleSetFixturesInput{RuleSetID: "next-rules", RuleSetVersion: "1", TemplateID: base.TemplateID, TemplateVersion: base.Version, Fixtures: postgresProjectionFixtures("pgp-"), Reason: "postgres projection parity"}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("postgres stale receipt replay = %v, want ErrVersionConflict", err)
	}
	reregistered, err := s.RegisterRuleSetFixtures(ctx, actor, "pg-ruleset-register-2", RegisterRuleSetFixturesInput{RuleSetID: "next-rules", RuleSetVersion: "1", TemplateID: base.TemplateID, TemplateVersion: base.Version, Fixtures: postgresProjectionFixtures("pgp-"), Reason: "postgres re-registration"})
	if err != nil {
		t.Fatal(err)
	}
	if reregistered.ContentHash == receipt.ContentHash {
		t.Fatal("postgres re-registration kept the old projection pin")
	}
	var rows int64
	if err := db.Table("lingdoc_template_replay_fixtures").Where("tenant_id=?", actor.TenantID).Count(&rows).Error; err != nil || rows != 12 {
		t.Fatalf("postgres append-only projection rows = %d, %v", rows, err)
	}
}
