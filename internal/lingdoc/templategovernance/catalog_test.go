package templategovernance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
)

type testAuth struct {
	allow bool
	basis VersionBasis
}

func (a *testAuth) AuthorizeTemplate(_ context.Context, _ *gorm.DB, actor Actor, _ Action, _ string) error {
	if !a.allow || actor.Role != "tenant_admin" {
		return errors.New("denied")
	}
	return nil
}
func (a *testAuth) AuthorizeSuggestion(_ context.Context, _ *gorm.DB, actor Actor, _ Action, _ SuggestionScope) error {
	if !a.allow || actor.Role != "tenant_admin" {
		return errors.New("denied")
	}
	return nil
}
func (a *testAuth) CurrentProjectBasis(_ context.Context, _ *gorm.DB, _ Actor, _ string) (VersionBasis, error) {
	return a.basis, nil
}
func (a *testAuth) AuthorizeRuleSet(_ context.Context, _ *gorm.DB, actor Actor, _ rulesetstore.Action, _ *template.RuleSet) error {
	if !a.allow || actor.Role != "tenant_admin" {
		return errors.New("denied")
	}
	return nil
}

type fixtureReader map[string]ReplayFixture

func (r fixtureReader) GetReplayFixture(_ context.Context, _ Actor, id string) (ReplayFixture, error) {
	f, ok := r[id]
	if !ok {
		return f, ErrNotFound
	}
	return f, nil
}

type testUsage struct{}

func (testUsage) AffectedProjects(_ context.Context, _ *gorm.DB, _ Actor, _, _ string) ([]string, error) {
	return []string{"affected-project"}, nil
}

type sourceVerifier struct{ allow bool }

func (s sourceVerifier) VerifyTemplateSources(_ context.Context, _ Actor, _ []SourceRef) error {
	if !s.allow {
		return ErrSourceUnavailable
	}
	return nil
}
func (s sourceVerifier) VerifyTemplateSourcesInTransaction(_ context.Context, _ *gorm.DB, _ Actor, _ []SourceRef) error {
	if !s.allow {
		return ErrSourceUnavailable
	}
	return nil
}

func openTestDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "templates.db")
	db := reopenDB(t, path)
	for _, ddl := range []string{
		"CREATE TABLE tenants (id INTEGER PRIMARY KEY); INSERT INTO tenants(id) VALUES(1),(2)",
		`CREATE TABLE audit_logs(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id INTEGER NOT NULL,actor_user_id TEXT NOT NULL,actor_role TEXT NOT NULL,action TEXT NOT NULL,target_type TEXT NOT NULL,target_id TEXT NOT NULL,outcome TEXT NOT NULL,details TEXT NOT NULL,scope_type TEXT NOT NULL,scope_id TEXT NOT NULL,created_at DATETIME NOT NULL)`,
		SQLiteDDL,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000036_lingdoc_rule_sets.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(string(migration)).Error; err != nil {
		t.Fatal(err)
	}
	return db, path
}
func reopenDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"}, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	return db
}
func seedService(t *testing.T) (*Service, *gorm.DB, *testAuth, Actor, TemplateVersion, fixtureReader) {
	t.Helper()
	db, _ := openTestDB(t)
	auth := &testAuth{allow: true}
	actor := Actor{TenantID: 1, UserID: "owner", Role: "tenant_admin"}
	set := template.RuleSet{ID: "grant-rules", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{{RuleID: "goal-present", DefinitionID: "presence", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal", Severity: template.SeverityWarning, TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: "rule-admin"}}}
	store := rulesetstore.NewStore(db, auth)
	if err := store.CreateDraft(context.Background(), actor, set); err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(context.Background(), actor, set.ID, set.Version)
	if err != nil {
		t.Fatal(err)
	}
	ref := RuleSetRef{ID: published.ID, Version: published.Version, Hash: published.RulesetHash}
	fixtures := fixtureReader{}
	for _, scene := range []string{"write_check", "preflight"} {
		for _, polarity := range []string{"positive", "negative"} {
			id := scene + "-" + polarity
			fields := map[string]string{}
			status := template.EvaluationIssue
			if polarity == "positive" {
				fields["research_goal"] = "observable objective"
				status = template.EvaluationPass
			}
			fixtures[id] = ReplayFixture{ID: id, RuleSet: ref, RuleID: "goal-present", Scene: scene, Polarity: polarity, TargetRef: "field/research_goal", Input: template.EvaluationInput{ProjectID: "fixture", TargetVersion: "fixture/1", Fields: fields}, ExpectedStatus: status}
		}
	}
	s := NewService(db, Ports{Authorizer: auth, RuleSets: auth, Replay: ExampleReplayVerifier{Reader: fixtures}, Usage: testUsage{}, Sources: sourceVerifier{allow: true}})
	if _, err := s.CreateProfile(context.Background(), actor, "create-profile-1", CreateProfileInput{ID: "grant-template", Name: "Internal working template", Reason: "create reviewed internal structure"}); err != nil {
		t.Fatal(err)
	}
	input := draftInput(ref, "1", 0)
	draft, err := s.SaveDraft(context.Background(), actor, "save-template-1", input)
	if err != nil {
		t.Fatal(err)
	}
	auth.basis = VersionBasis{ProjectVersion: 3, SpecRevision: 2, ContextRevision: 1, TemplateCopyVersion: 1, TemplateID: draft.TemplateID, TemplateVersion: draft.Version, RuleSet: ref, CurrentSnapshotID: "release-1"}
	return s, db, auth, actor, draft, fixtures
}
func draftInput(ref RuleSetRef, version string, revision int64) SaveDraftInput {
	return SaveDraftInput{TemplateID: "grant-template", Version: version, ExpectedRevision: revision, RuleSet: ref, SourceType: "lingdoc_builtin", Scope: "internal-demo", Curator: "rule-admin", Reason: "reviewed structural change", Definition: template.Template{Fields: []template.Field{{ID: "research_goal", Label: "Goal", Type: "string", Required: true}}, RequiredFields: []string{"research_goal"}, Sections: []template.Section{{ID: "method", Title: "Method", Required: true, Order: 1}}}}
}
func publishTestVersion(t *testing.T, s *Service, actor Actor, draft TemplateVersion) TemplateVersion {
	t.Helper()
	v, err := s.Publish(context.Background(), actor, "publish-template-"+draft.Version, PublishInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, ReplayFixtureIDs: []string{"write_check-positive", "write_check-negative", "preflight-positive", "preflight-negative"}, Reason: "all declared examples passed"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func countRows(t *testing.T, db *gorm.DB, table, condition string, args ...any) int64 {
	t.Helper()
	var count int64
	q := db.Table(table)
	if condition != "" {
		q = q.Where(condition, args...)
	}
	if err := q.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}
func TestCatalogLifecycleReplayRestartAndCopyGates(t *testing.T) {
	s, db, auth, actor, draft, _ := seedService(t)
	published := publishTestVersion(t, s, actor, draft)
	if published.ContentHash == "" || published.RulesetHash == "" || published.RulesetHash == published.Definition.RulesetHash {
		t.Fatal("missing independent version hashes")
	}
	again := publishTestVersion(t, s, actor, draft)
	if again.Revision != published.Revision {
		t.Fatal("retry created a second publication")
	}
	list, err := s.ListProfiles(context.Background(), actor, CatalogFilter{Scope: "internal-demo", State: template.RuleSetPublished})
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v %v", list, err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		v, err := s.ResolveForCopy(context.Background(), tx, actor, published.TemplateID, published.Version)
		if err != nil {
			return err
		}
		if v.RulesetHash != published.RulesetHash {
			t.Fatal("copy lost RuleSet identity")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	path := poolPath(db, t)
	pool, _ := db.DB()
	_ = pool.Close()
	db2 := reopenDB(t, path)
	s2 := NewService(db2, s.ports)
	read, err := s2.GetVersion(context.Background(), actor, published.TemplateID, published.Version)
	if err != nil || read.ContentHash != published.ContentHash {
		t.Fatalf("restart = %v %v", read, err)
	}
	read.Definition.Fields[0].Label = "caller-only mutation"
	read2, err := s2.GetVersion(context.Background(), actor, published.TemplateID, published.Version)
	if err != nil || read2.Definition.Fields[0].Label == "caller-only mutation" {
		t.Fatal("returned definition aliased storage")
	}
	withdrawn, err := s2.Withdraw(context.Background(), actor, "withdraw-version-1", GovernanceInput{TemplateID: published.TemplateID, Version: published.Version, ExpectedRevision: published.Revision, Reason: "known wrong requirement"})
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.ContentHash != published.ContentHash || withdrawn.RulesetHash != published.RulesetHash {
		t.Fatal("withdraw altered historical content")
	}
	if err := db2.Transaction(func(tx *gorm.DB) error {
		_, err := s2.ResolveForCopy(context.Background(), tx, actor, published.TemplateID, published.Version)
		return err
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("withdrawn copy = %v", err)
	}
	other := actor
	other.TenantID = 2
	if _, err := s2.GetVersion(context.Background(), other, published.TemplateID, published.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant read = %v", err)
	}
	auth.allow = false
	if _, err := s2.Withdraw(context.Background(), actor, "withdraw-version-1", GovernanceInput{TemplateID: published.TemplateID, Version: published.Version, ExpectedRevision: published.Revision, Reason: "known wrong requirement"}); err == nil {
		t.Fatal("revoked actor replayed cached result")
	}
}
func poolPath(db *gorm.DB, t *testing.T) string {
	t.Helper()
	var rows []struct {
		Seq  int
		Name string
		File string
	}
	if err := db.Raw("PRAGMA database_list").Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Name == "main" {
			return row.File
		}
	}
	t.Fatal("database path missing")
	return ""
}
func TestCatalogRejectsInvalidReplayHashesStatesAndStaleWrites(t *testing.T) {
	s, _, _, actor, draft, fixtures := seedService(t)
	bad := draftInput(draft.RuleSet, "2", 0)
	bad.Definition.RulesetHash = "client-forged"
	if _, err := s.SaveDraft(context.Background(), actor, "bad-client-hash", bad); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("forged = %v", err)
	}
	bad = draftInput(draft.RuleSet, "2", 0)
	bad.Definition.Fields[0].Type = "script"
	if _, err := s.SaveDraft(context.Background(), actor, "bad-client-field", bad); err == nil {
		t.Fatal("invalid structure saved")
	}
	input := PublishInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, ReplayFixtureIDs: []string{"write_check-positive"}, Reason: "insufficient coverage"}
	if _, err := s.Publish(context.Background(), actor, "missing-fixtures", input); !errors.Is(err, ErrReplayRequired) {
		t.Fatalf("incomplete examples = %v", err)
	}
	f := fixtures["preflight-negative"]
	f.Input.Fields["research_goal"] = "unexpectedly present"
	fixtures["preflight-negative"] = f
	input.ReplayFixtureIDs = []string{"write_check-positive", "write_check-negative", "preflight-positive", "preflight-negative"}
	if _, err := s.Publish(context.Background(), actor, "false-negative-1", input); !errors.Is(err, ErrReplayRequired) {
		t.Fatalf("wrong expected result = %v", err)
	}
	current, err := s.GetVersion(context.Background(), actor, draft.TemplateID, draft.Version)
	if err != nil || current.State != template.RuleSetDraft {
		t.Fatal("replay failure partially published")
	}
	edit := draftInput(draft.RuleSet, "1", draft.Revision)
	edit.Definition.Fields[0].Label = "new label"
	if _, err := s.SaveDraft(context.Background(), actor, "second-draft-save", edit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDraft(context.Background(), actor, "save-template-1", draftInput(draft.RuleSet, "1", 0)); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("old key replay = %v", err)
	}
}
func TestCatalogAuditFailureIsAtomicAndRecallKeepsHistory(t *testing.T) {
	s, db, _, actor, draft, _ := seedService(t)
	if err := db.Exec(`CREATE TRIGGER deny_save_audit BEFORE INSERT ON audit_logs WHEN NEW.action='lingdoc.template.save_draft' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDraft(context.Background(), actor, "new-draft-fails", draftInput(draft.RuleSet, "2", 0)); err == nil {
		t.Fatal("audit failure succeeded")
	}
	if n := countRows(t, db, "lingdoc_template_versions", "version=?", "2"); n != 0 {
		t.Fatal("business row survived audit rollback")
	}
	if n := countRows(t, db, "lingdoc_template_governance_operations", "idempotency_key=?", "new-draft-fails"); n != 0 {
		t.Fatal("operation survived audit rollback")
	}
	published := publishTestVersion(t, s, actor, draft)
	recalled, err := s.Recall(context.Background(), actor, "recall-template-1", RecallInput{GovernanceInput: GovernanceInput{TemplateID: published.TemplateID, Version: published.Version, ExpectedRevision: published.Revision, Reason: "serious template error"}, MigrationAdvice: "owner must assess a template_upgrade ChangeSet"})
	if err != nil {
		t.Fatal(err)
	}
	if recalled.State != template.RuleSetRecalled || recalled.Recall == nil || len(recalled.Recall.AffectedProjectIDs) != 1 || recalled.ContentHash != published.ContentHash {
		t.Fatal("recall record/history missing")
	}
}
func TestCatalogConcurrentConditionalEditAndRuleSetWithdrawal(t *testing.T) {
	s, db, _, actor, draft, _ := seedService(t)
	input := draftInput(draft.RuleSet, "1", draft.Revision)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, key := range []string{"concurrent-save-a", "concurrent-save-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, err := s.SaveDraft(context.Background(), actor, key, input)
			errs <- err
		}(key)
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrVersionConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success/conflict %d/%d", success, conflict)
	}
	if _, err := rulesetstore.NewStore(db, s.ports.RuleSets).Withdraw(context.Background(), actor, draft.RuleSet.ID, draft.RuleSet.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDraft(context.Background(), actor, "withdrawn-set-copy", draftInput(draft.RuleSet, "2", 0)); !errors.Is(err, template.ErrRuleSetNotPublished) {
		t.Fatalf("withdrawn RuleSet bound = %v", err)
	}
}
