package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
)

// Sharing is the only external boundary in this fixture. The existing native
// permission service authorizes same-tenant reads; foreign sharing grants are
// unavailable, rather than supplied by a blanket allow stub.
type adapterSharesStub struct{ interfaces.KBShareService }

func (adapterSharesStub) CheckTenantKBPermission(context.Context, string, uint64, types.TenantRole) (types.OrgMemberRole, bool, error) {
	return "", false, nil
}

type adapterChapterAuthorizerStub struct{ projects *core.Service }

func (a adapterChapterAuthorizerStub) Authorize(ctx context.Context, userID, projectID, capability string) error {
	if err := core.RequireHumanDecision(ctx); err != nil {
		return err
	}
	caller := types.CallerFromContext(ctx)
	if caller.UserID != userID {
		return core.ErrNotFound
	}
	return a.projects.Authorize(ctx, core.Actor{TenantID: caller.TenantID, UserID: caller.UserID, Role: caller.Role}, projectID, capability)
}

func governanceAdapterFixture(t *testing.T) (*TemplateGovernanceRuntime, *gorm.DB, core.Actor, core.Project, context.Context) {
	t.Helper()
	runtime, _, db, actor, p, ctx := governanceAdapterFixtureWithProjects(t)
	return runtime, db, actor, p, ctx
}

// governanceAdapterFixtureWithProjects also hands back the aggregate the fixture
// assembled. The governance runtime deliberately no longer holds the aggregate,
// so the helpers that drive the human baseline lifecycle receive it explicitly
// instead of reaching through the runtime.
func governanceAdapterFixtureWithProjects(t *testing.T) (*TemplateGovernanceRuntime, *core.Service, *gorm.DB, core.Actor, core.Project, context.Context) {
	t.Helper()
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: filepath.Join(t.TempDir(), "governance.db") + "?_pragma=busy_timeout(5000)"}, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	for _, ddl := range []string{"CREATE TABLE tenants(id INTEGER PRIMARY KEY);INSERT INTO tenants VALUES(7)", "CREATE TABLE tenant_members(tenant_id INTEGER,user_id TEXT,role TEXT,status TEXT,deleted_at DATETIME,PRIMARY KEY(tenant_id,user_id))", `CREATE TABLE audit_logs(id INTEGER PRIMARY KEY,tenant_id INTEGER,actor_user_id TEXT,actor_role TEXT,action TEXT,target_type TEXT,target_id TEXT,outcome TEXT,details TEXT,scope_type TEXT,scope_id TEXT,created_at DATETIME)`} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Apply the same production LingDoc chain as the current service. A cutoff
	// fixture omitted canonical RuleSet reference/audit columns after main sync.
	migrations, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "sqlite", "*_lingdoc_*.up.sql"))
	if err != nil || len(migrations) == 0 {
		t.Fatalf("find production LingDoc migrations: count=%d error=%v", len(migrations), err)
	}
	for _, migration := range migrations {
		raw, err := os.ReadFile(migration)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(string(raw)).Error; err != nil {
			t.Fatalf("%s: %v", filepath.Base(migration), err)
		}
	}
	for _, ddl := range []string{`CREATE TABLE knowledge_bases(id TEXT PRIMARY KEY,tenant_id INTEGER,chunking_config TEXT,deleted_at DATETIME)`, `CREATE TABLE knowledges(id TEXT PRIMARY KEY,tenant_id INTEGER,knowledge_base_id TEXT,type TEXT,title TEXT,file_type TEXT,file_size INTEGER,file_hash TEXT,file_path TEXT,parse_status TEXT,processed_at DATETIME,deleted_at DATETIME)`, `CREATE TABLE chunks(id TEXT PRIMARY KEY,tenant_id INTEGER,knowledge_id TEXT,knowledge_base_id TEXT,content TEXT,content_revision INTEGER DEFAULT 0,chunk_index INTEGER,chunk_type TEXT DEFAULT 'text',is_enabled INTEGER DEFAULT 1,start_at INTEGER,end_at INTEGER,deleted_at DATETIME)`} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	actor := core.Actor{TenantID: 7, UserID: "curator", Role: types.TenantRoleOwner}
	if err := db.Exec("INSERT INTO tenant_members VALUES(7,'curator','owner','active',NULL)").Error; err != nil {
		t.Fatal(err)
	}
	projects := core.NewService(core.NewGORMRepository(db))
	ctx := governanceCallerContext(context.Background(), actor)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	t.Cleanup(cancel)
	raw, _, _, err := projects.CreateProject(ctx, actor, "runtime-project", core.CreateProjectInput{Name: "合成规则验收", TemplateID: template.DemoTemplateID})
	if err != nil {
		t.Fatal(err)
	}
	var p core.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	shares := adapterSharesStub{}
	sourceRuntime := NewGORMSourceRuntime(db, ProjectPorts{Authorizer: projects, Projects: projects, Context: projects}, shares, nil, delivery.NewFixedTemplateReader())
	connectAggregateCapabilities(sourceRuntime, projects)
	sourceRuntime.EnableFullWorkflowPolicy()
	research := NewResearchAdapter(db, projects, sourceRuntime, shares, nil, nil)
	projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: research})
	runtime, err := NewTemplateGovernanceRuntime(db, projects, sourceRuntime, nil)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, projects, db, actor, p, ctx
}

// Exercise the actual human baseline lifecycle. A draft correctly has no
// formal chapters and cannot be read as a delivery input.
func activateProjectFixture(t *testing.T, projects *core.Service, actor core.Actor, p core.Project, ctx context.Context) core.Project {
	t.Helper()
	projects.RequireActivationRead(true)
	raw, _, _, err := projects.SaveSpec(ctx, actor, p.ID, "g4-human-spec", core.SaveSpecInput{ExpectedSpecRevision: p.SpecRevision, Fields: map[string]string{"research_subject": "合成软件验收对象", "research_goal": "合成软件验收目标"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	read, err := projects.RecordActivationRead(ctx, actor, p.ID, p.ProjectVersion)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _, err = projects.ActivateProject(ctx, actor, p.ID, "g4-human-activate", core.ActivateProjectInput{ExpectedSpecRevision: p.SpecRevision, ExpectedProjectVersion: p.ProjectVersion, ReviewedProjectVersion: p.ProjectVersion, ActivationReadID: read.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Status != "active" || p.BaselineConfirmationID == "" {
		t.Fatal("actual human activation did not establish the baseline")
	}
	return p
}

// These are synthetic software inputs. A scoped human confirmation is recorded
// through its real service; it is not a real research trial or scientific proof.
func writeConfirmedChaptersFixture(t *testing.T, projects *core.Service, db *gorm.DB, actor core.Actor, p core.Project, ctx context.Context) []core.Chapter {
	t.Helper()
	chapters, err := projects.ListChapters(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != len(p.TemplateCopy.Definition.Sections) || len(chapters) == 0 {
		t.Fatal("actual activation did not create the required formal chapters")
	}
	store := candidateadoption.NewSQLiteCandidateAdoptionStore(db)
	confirmations := candidateadoption.NewConfirmationService(store, nil, adapterChapterAuthorizerStub{projects})
	for _, chapter := range chapters {
		raw, _, _, err := projects.SaveChapter(ctx, actor, p.ID, chapter.ID, "g4-body-"+chapter.ID, core.SaveChapterInput{ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: p.SpecRevision, BodyMarkdown: "合成正文仅检验软件流程，不构成研究结论。", SourceIDs: []string{}})
		if err != nil {
			t.Fatal(err)
		}
		var saved core.Chapter
		if err := json.Unmarshal(raw, &saved); err != nil || saved.CurrentVersionID == nil {
			t.Fatalf("actual chapter save: %s %v", raw, err)
		}
		confirmed, replayed, err := confirmations.ConfirmChapter(ctx, candidateadoption.ConfirmChapterInput{ProjectID: p.ID, ChapterID: chapter.ID, ActorID: actor.UserID, IdempotencyKey: "g4-confirm-" + chapter.ID, ExpectedChapterVersionID: *saved.CurrentVersionID, ExpectedSpecRevision: int(p.SpecRevision), Decisions: []candidateadoption.ReviewDecision{}})
		if err != nil || replayed || !confirmed.Valid || confirmed.ActorUserID != actor.UserID || confirmed.ChapterVersionID != *saved.CurrentVersionID {
			t.Fatalf("actual scoped human confirmation: %+v replay=%v %v", confirmed, replayed, err)
		}
	}
	chapters, err = projects.ListChapters(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return chapters
}

func adapterSourceFixture(t *testing.T, r *TemplateGovernanceRuntime, db *gorm.DB, p core.Project, actor core.Actor) (evidence.Asset, governance.SourceRef) {
	t.Helper()
	processed := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	quote := "合成已授权来源，仅检验软件行为。"
	originPath := filepath.Join(t.TempDir(), "actual-origin.txt")
	if err := os.WriteFile(originPath, []byte(quote), 0600); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		SQL  string
		Args []any
	}{{"INSERT INTO knowledge_bases(id,tenant_id) VALUES('kb-runtime',7)", nil}, {"INSERT INTO knowledges VALUES('knowledge-runtime',7,'kb-runtime','file','合成来源','txt',?,'known-file-hash',?,'completed',?,NULL)", []any{len([]byte(quote)), originPath, processed}}, {"INSERT INTO chunks(id,tenant_id,knowledge_id,knowledge_base_id,content,chunk_index,start_at,end_at) VALUES('source-runtime',7,'knowledge-runtime','kb-runtime',?,0,0,?)", []any{quote, len([]rune(quote))}}} {
		if err := db.Exec(query.SQL, query.Args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	asset, err := evidence.NewBindings(db).Bind(context.Background(), evidence.BindInput{TenantID: 7, ProjectID: p.ID, KnowledgeID: "knowledge-runtime", KnowledgeBaseID: "kb-runtime", Title: "合成来源", CreatedBy: actor.UserID, Signal: evidence.KnowledgeSignal{KnowledgeID: "knowledge-runtime", ParseStatus: types.ParseStatusCompleted, FileHash: "known-file-hash", FileSize: int64(len([]byte(quote))), ProcessedAt: processed}})
	if err != nil {
		t.Fatal(err)
	}
	importRecord := core.ImportCandidate{ID: "consent-runtime", Version: 1, ProjectID: p.ID, AssetID: asset.ID, AssetRevision: asset.AssetRevision, KnowledgeID: asset.KnowledgeID, Scope: "project", Purpose: "仅检验合成软件输入的规则建议", CreatedBy: actor.UserID, Status: "imported", ModelUseAllowed: true, CreatedAt: processed}
	raw, _ := json.Marshal(importRecord)
	if err := db.Exec("INSERT INTO lingdoc_g2_objects(project_id,kind,id,version,payload_json) VALUES(?,?,?,?,?)", p.ID, core.ImportCandidateKind, importRecord.ID, 1, string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	return asset, governance.SourceRef{ID: "source-runtime", Version: "1"}
}
func TestTemplateGovernanceRuntimeCurrentRoleAndParentRollback(t *testing.T) {
	r, db, actor, _, ctx := governanceAdapterFixture(t)
	govActor := governanceActor(actor)
	rollback := errors.New("rollback-fixture")
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.AuthorizeTemplate(ctx, tx, govActor, governance.ActionPublish, ""); err != nil {
			t.Fatal(err)
		}
		if err := tx.Exec("UPDATE tenant_members SET role='viewer' WHERE user_id='curator'").Error; err != nil {
			t.Fatal(err)
		}
		if err := r.AuthorizeTemplate(ctx, tx, govActor, governance.ActionPublish, ""); err == nil {
			t.Fatal("stale owner token restored publish permission")
		}
		if err := r.AuthorizeTemplate(ctx, tx, govActor, governance.ActionRead, ""); err != nil {
			t.Fatal(err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, err := r.Service().CreateProfile(ctx, govActor, "after-role-rollback", governance.CreateProfileInput{ID: "rollback-profile", Name: "rollback recovery", Reason: "actual transaction rolled back"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE tenant_members SET status='suspended'").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := r.Service().CreateProfile(ctx, govActor, "after-role-rollback", governance.CreateProfileInput{ID: "rollback-profile", Name: "rollback recovery", Reason: "actual transaction rolled back"}); err == nil {
		t.Fatal("suspension permitted old operation replay")
	}
}
func TestTemplateGovernanceRuntimeSourceConsentRevisionAndRevocationInOnePool(t *testing.T) {
	r, db, actor, p, ctx := governanceAdapterFixture(t)
	_, ref := adapterSourceFixture(t, r, db, p, actor)
	if err := r.VerifyTemplateSources(ctx, governanceActor(actor), []governance.SourceRef{ref}); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback-source")
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := r.sourceInTransaction(ctx, tx, actor, p.ID, ref, true); err != nil {
			t.Fatal(err)
		}
		var row struct{ PayloadJSON string }
		if err := tx.Table("lingdoc_g2_objects").Where("id='consent-runtime'").Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		var consent core.ImportCandidate
		_ = json.Unmarshal([]byte(row.PayloadJSON), &consent)
		consent.ModelUseAllowed = false
		raw, _ := json.Marshal(consent)
		if err := tx.Table("lingdoc_g2_objects").Where("id='consent-runtime'").Update("payload_json", string(raw)).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := r.sourceInTransaction(ctx, tx, actor, p.ID, ref, true); err == nil {
			t.Fatal("client flag overrode current model consent")
		}
		if _, err := r.sourceInTransaction(ctx, tx, actor, p.ID, ref, false); err != nil {
			t.Fatal(err)
		}
		if err := tx.Exec("UPDATE knowledges SET file_hash='reparsed' WHERE id='knowledge-runtime'").Error; err != nil {
			t.Fatal(err)
		}
		if err := r.VerifyTemplateSourcesInTransaction(ctx, tx, governanceActor(actor), []governance.SourceRef{ref}); err == nil {
			t.Fatal("reparse retained stale pinned asset fingerprint")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id='kb-runtime'").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.VerifyTemplateSourcesInTransaction(ctx, tx, governanceActor(actor), []governance.SourceRef{ref})
	}); err == nil {
		t.Fatal("deleted KB retained source permission")
	}
}

func TestTemplateGovernanceRuntimeUsesCurrentHumanAssetScopeInParentTransaction(t *testing.T) {
	for _, restricted := range []struct {
		name, scope, createdBy string
		allowedUserIDs         []string
	}{{"personal", "personal", "another-author", []string{}}, {"explicit-users", "project", "curator", []string{"another-author"}}} {
		t.Run(restricted.name, func(t *testing.T) {
			r, db, actor, p, ctx := governanceAdapterFixture(t)
			asset, ref := adapterSourceFixture(t, r, db, p, actor)
			if err := r.VerifyTemplateSources(ctx, governanceActor(actor), []governance.SourceRef{ref}); err != nil {
				t.Fatal(err)
			}
			err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				policy := core.ProjectAssetPolicy{ID: asset.ID, ProjectID: p.ID, AssetID: asset.ID, Version: int64(asset.AssetRevision), AssetRevision: int64(asset.AssetRevision), PolicyVersion: 1, Scope: restricted.scope, AllowedUserIDs: restricted.allowedUserIDs, CreatedBy: restricted.createdBy, Purpose: "合成范围收窄反例", ModelUseAllowed: true}
				raw, err := json.Marshal(policy)
				if err != nil {
					return err
				}
				if err := core.NewTransactionAdapter(tx).(core.ObjectStore).InsertObject(core.StoredObject{ProjectID: p.ID, Kind: core.AssetPolicyKind, ID: asset.ID, Version: 1, Payload: raw}); err != nil {
					return err
				}
				// This row is uncommitted, so a main-pool read would miss it or
				// deadlock this single-connection fixture.
				if _, err := r.sourceInTransaction(ctx, tx, actor, p.ID, ref, false); err == nil {
					t.Fatal("native KB access bypassed current formal human scope")
				} else if errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("scope recheck performed nested main-pool IO")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.VerifyTemplateSources(ctx, governanceActor(actor), []governance.SourceRef{ref}); err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("public native source policy ignored restricted scope: %v", err)
			}
		})
	}
}

func TestTemplateGovernanceRuntimeNativeSourceKeepsTenantAndAPIKeyBoundary(t *testing.T) {
	r, db, actor, p, ctx := governanceAdapterFixture(t)
	_, ref := adapterSourceFixture(t, r, db, p, actor)
	rollback := errors.New("rollback-tenant-fixture")
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE knowledges SET tenant_id=8 WHERE id='knowledge-runtime'").Error; err != nil {
			return err
		}
		if _, err := r.sourceInTransaction(ctx, tx, actor, p.ID, ref, false); !errors.Is(err, ErrCrossTenantSourceUnsupported) {
			t.Fatalf("foreign native source admitted without a transaction-safe grant: %v", err)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	blocked := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 91, KnowledgeBaseIDs: types.StringArray{"kb-excluded"}, Capabilities: types.StringArray{"retrieve"}})
	if err := db.WithContext(blocked).Transaction(func(tx *gorm.DB) error {
		allowed, err := (tenantKBReader{db: tx, actor: actor}).CanReadKB(blocked, evidenceActor(actor), "kb-runtime", actor.TenantID)
		if allowed || err == nil {
			t.Fatal("matching tenant caller erased the original API key KB scope")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func runtimeDemoVersionFixture(t *testing.T) governance.TemplateVersion {
	t.Helper()
	set, err := TemplateGovernanceDemoRuleSet()
	if err != nil {
		t.Fatal(err)
	}
	def := template.DemoTemplate()
	def.Rules, err = template.CompileRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	def, err = template.WithHashes(def)
	if err != nil {
		t.Fatal(err)
	}
	policyHash, err := governance.HashBindingPolicies(nil)
	if err != nil {
		t.Fatal(err)
	}
	return governance.TemplateVersion{TemplateID: def.ID, Version: def.Version, Revision: 1, State: template.RuleSetDraft, Definition: def, RuleSet: governance.RuleSetRef{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}, RuleSetSnapshot: set, ContentHash: def.ContentHash, RulesetHash: set.RulesetHash, PolicyHash: policyHash}
}
func TestTemplateGovernanceRuntimeReviewedFixturesExecuteBothPolarities(t *testing.T) {
	r, _, actor, _, ctx := governanceAdapterFixture(t)
	version := runtimeDemoVersionFixture(t)
	ids := []string{}
	for _, record := range BuiltinTemplateGovernanceFixtures() {
		ids = append(ids, record.Fixture.ID)
	}
	if err := r.Verify(ctx, governanceActor(actor), version, ids); err != nil {
		t.Fatal(err)
	}
	if err := r.Verify(ctx, governanceActor(actor), version, append(ids, "client-pass")); err == nil {
		t.Fatal("client fixture PASS was accepted")
	}
	tampered := version
	tampered.Definition.Fields = append(append([]template.Field{}, version.Definition.Fields...), template.Field{ID: "unreviewed", Label: "new mandatory field", Type: "string", Required: true})
	tampered.Definition.RequiredFields = append(append([]string{}, version.Definition.RequiredFields...), "unreviewed")
	tampered.Definition, _ = template.WithHashes(tampered.Definition)
	tampered.ContentHash = tampered.Definition.ContentHash
	if err := r.Verify(ctx, governanceActor(actor), tampered, ids); err == nil {
		t.Fatal("fixture accepted unrelated template content")
	}
	record := r.fixtures[ids[1]]
	record.Fixture.Input.Fields["research_goal"] = "now positive"
	r.fixtures[ids[1]] = record
	if err := r.Verify(ctx, governanceActor(actor), version, ids); err == nil {
		t.Fatal("incorrect server fixture expected outcome escaped actual evaluation")
	}
}
func TestTemplateGovernanceRuntimeRuleLifecycleCatalogUsageAndRetry(t *testing.T) {
	r, _, actor, p, ctx := governanceAdapterFixture(t)
	svc := r.Service()
	govActor := governanceActor(actor)
	version := runtimeDemoVersionFixture(t)
	draft := version.RuleSetSnapshot
	draft.State = template.RuleSetDraft
	draft.RulesetHash = ""
	input := governance.RuleDraftInput{Draft: draft, Reason: "server-reviewed fixture seed"}
	if _, err := svc.CreateRuleSetDraft(ctx, govActor, "create-demo-rules", input); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateRuleSetDraft(ctx, govActor, "create-demo-rules", input); err != nil {
		t.Fatal("lost response retry", err)
	}
	fixtures := []string{}
	for _, record := range BuiltinTemplateGovernanceFixtures() {
		fixtures = append(fixtures, record.Fixture.ID)
	}
	publish := governance.RuleGovernanceInput{ExpectedState: template.RuleSetDraft, Reason: "real positive and negative replay", ReplayFixtureIDs: fixtures, TemplateID: template.DemoTemplateID, TemplateVersion: "1"}
	published, err := svc.PublishRuleSet(ctx, govActor, draft.ID, draft.Version, "publish-demo-rules", publish)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishRuleSet(ctx, govActor, draft.ID, draft.Version, "publish-demo-rules", publish); err != nil {
		t.Fatal("publish retry", err)
	}
	if _, err := svc.CreateProfile(ctx, govActor, "create-demo-profile", governance.CreateProfileInput{ID: template.DemoTemplateID, Name: "内置审阅目录", Reason: "actual catalog registration"}); err != nil {
		t.Fatal(err)
	}
	def := template.DemoTemplate()
	def.Rules = nil
	def.ContentHash = ""
	def.RulesetHash = ""
	saved, err := svc.SaveDraft(ctx, govActor, "save-demo-template", governance.SaveDraftInput{TemplateID: def.ID, Version: def.Version, Definition: def, RuleSet: governance.RuleSetRef{ID: published.ID, Version: published.Version, Hash: published.RulesetHash}, SourceType: "lingdoc_builtin", Scope: "internal-demo", Curator: actor.UserID, Reason: "actual immutable copy"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err = svc.Publish(ctx, govActor, "publish-demo-template", governance.PublishInput{TemplateID: saved.TemplateID, Version: saved.Version, ExpectedRevision: saved.Revision, ReplayFixtureIDs: fixtures, Reason: "actual replay"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		resolved, err := r.Resolve(ctx, core.NewTransactionAdapter(tx), actor, saved.TemplateID, saved.Version)
		if err != nil {
			return err
		}
		if resolved.RuleSetSnapshot == nil || resolved.SourceRuleSetHash != saved.RulesetHash || resolved.SourcePolicyHash != saved.PolicyHash {
			t.Fatal("exact snapshot identity lost")
		}
		ids, err := r.AffectedProjects(ctx, tx, govActor, saved.TemplateID, saved.Version)
		if err != nil {
			return err
		}
		if len(ids) != 1 || ids[0] != p.ID {
			t.Fatalf("actual usage = %v", ids)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	recalled, err := svc.Recall(ctx, govActor, "recall-demo-template", governance.RecallInput{GovernanceInput: governance.GovernanceInput{TemplateID: saved.TemplateID, Version: saved.Version, ExpectedRevision: saved.Revision, Reason: "retain audit and current copies"}, MigrationAdvice: "human review exact successor"})
	if err != nil {
		t.Fatal(err)
	}
	if recalled.Recall == nil || len(recalled.Recall.AffectedProjectIDs) != 1 {
		t.Fatal("actual current usage not recorded")
	}
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := r.Resolve(ctx, core.NewTransactionAdapter(tx), actor, saved.TemplateID, saved.Version)
		return err
	}); err == nil {
		t.Fatal("recalled catalog could bind a new project")
	}
}

// seedDemoRuleSetFixture performs the first legal RuleSet publication: draft ->
// real positive/negative replay -> published. It returns the published set and
// the exact server-registered fixture IDs so callers never invent IDs.
func seedDemoRuleSetFixture(t *testing.T, r *TemplateGovernanceRuntime, actor core.Actor, ctx context.Context) (template.RuleSet, []string) {
	t.Helper()
	svc := r.Service()
	govActor := governanceActor(actor)
	version := runtimeDemoVersionFixture(t)
	draft := version.RuleSetSnapshot
	draft.State = template.RuleSetDraft
	draft.RulesetHash = ""
	input := governance.RuleDraftInput{Draft: draft, Reason: "server-reviewed fixture seed"}
	if _, err := svc.CreateRuleSetDraft(ctx, govActor, "create-demo-rules", input); err != nil {
		t.Fatal(err)
	}
	fixtures := []string{}
	for _, record := range BuiltinTemplateGovernanceFixtures() {
		fixtures = append(fixtures, record.Fixture.ID)
	}
	publish := governance.RuleGovernanceInput{ExpectedState: template.RuleSetDraft, Reason: "real positive and negative replay", ReplayFixtureIDs: fixtures, TemplateID: template.DemoTemplateID, TemplateVersion: "1"}
	published, err := svc.PublishRuleSet(ctx, govActor, draft.ID, draft.Version, "publish-demo-rules", publish)
	if err != nil {
		t.Fatal(err)
	}
	return published, fixtures
}

// saveDemoTemplateDraftFixture saves the template-demo/1 catalog draft bound to the
// replayed demo RuleSet. The returned version is still a draft.
func saveDemoTemplateDraftFixture(t *testing.T, r *TemplateGovernanceRuntime, actor core.Actor, set template.RuleSet, ctx context.Context) governance.TemplateVersion {
	t.Helper()
	def := template.DemoTemplate()
	def.Rules = nil
	def.ContentHash = ""
	def.RulesetHash = ""
	saved, err := r.Service().SaveDraft(ctx, governanceActor(actor), "save-demo-template", governance.SaveDraftInput{TemplateID: def.ID, Version: def.Version, Definition: def, RuleSet: governance.RuleSetRef{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}, SourceType: "lingdoc_builtin", Scope: "internal-demo", Curator: actor.UserID, Reason: "actual immutable copy"})
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

// publishDemoCatalogFixture performs the full legal first publication: replayed
// RuleSet -> profile -> template draft -> replay publish. Each test runs
// against its own SQLite database, so the fixed idempotency keys never collide.
func publishDemoCatalogFixture(t *testing.T, r *TemplateGovernanceRuntime, actor core.Actor, ctx context.Context) (governance.TemplateVersion, []string) {
	t.Helper()
	published, fixtures := seedDemoRuleSetFixture(t, r, actor, ctx)
	if _, err := r.Service().CreateProfile(ctx, governanceActor(actor), "create-demo-profile", governance.CreateProfileInput{ID: template.DemoTemplateID, Name: "内置审阅目录", Reason: "actual catalog registration"}); err != nil {
		t.Fatal(err)
	}
	saved := saveDemoTemplateDraftFixture(t, r, actor, published, ctx)
	saved, err := r.Service().Publish(ctx, governanceActor(actor), "publish-demo-template", governance.PublishInput{TemplateID: saved.TemplateID, Version: saved.Version, ExpectedRevision: saved.Revision, ReplayFixtureIDs: fixtures, Reason: "actual replay"})
	if err != nil {
		t.Fatal(err)
	}
	return saved, fixtures
}

func TestTemplateGovernanceRuntimeWithdrawClosesNewCopiesButKeepsHistory(t *testing.T) {
	r, db, actor, p, ctx := governanceAdapterFixture(t)
	svc := r.Service()
	govActor := governanceActor(actor)
	saved, _ := publishDemoCatalogFixture(t, r, actor, ctx)
	if p.TemplateCopy == nil {
		t.Fatal("fixture project has no template copy to preserve")
	}
	withdrawn, err := svc.Withdraw(ctx, govActor, "withdraw-demo-template", governance.GovernanceInput{TemplateID: saved.TemplateID, Version: saved.Version, ExpectedRevision: saved.Revision, Reason: "stop new copies, keep history"})
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.State != template.RuleSetWithdrawn {
		t.Fatalf("state = %s", withdrawn.State)
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := r.Resolve(ctx, core.NewTransactionAdapter(tx), actor, saved.TemplateID, saved.Version)
		return err
	})
	if !errors.Is(err, governance.ErrInvalidState) {
		t.Fatalf("withdrawn catalog still bound a new project: %v", err)
	}
	if _, err := svc.Withdraw(ctx, govActor, "withdraw-demo-template-again", governance.GovernanceInput{TemplateID: saved.TemplateID, Version: saved.Version, ExpectedRevision: withdrawn.Revision, Reason: "repeat withdrawal"}); !errors.Is(err, governance.ErrInvalidState) {
		t.Fatalf("repeat withdrawal accepted: %v", err)
	}
	def := template.DemoTemplate()
	def.Rules = nil
	def.ContentHash = ""
	def.RulesetHash = ""
	if _, err := svc.SaveDraft(ctx, govActor, "save-after-withdraw", governance.SaveDraftInput{TemplateID: def.ID, Version: def.Version, ExpectedRevision: withdrawn.Revision, Definition: def, RuleSet: saved.RuleSet, SourceType: "lingdoc_builtin", Scope: "internal-demo", Curator: actor.UserID, Reason: "reuse withdrawn version"}); !errors.Is(err, governance.ErrInvalidState) {
		t.Fatalf("draft overwrote a withdrawn version: %v", err)
	}
	history, err := svc.GetVersion(ctx, govActor, saved.TemplateID, saved.Version)
	if err != nil {
		t.Fatal(err)
	}
	if history.State != template.RuleSetWithdrawn || history.ContentHash != saved.ContentHash || history.RulesetHash != saved.RulesetHash || len(history.Definition.Sections) == 0 {
		t.Fatal("withdrawal mutated the historical record")
	}
	var copies int64
	if err := db.Table("lingdoc_project_template_copies").Where("project_id=?", p.ID).Count(&copies).Error; err != nil {
		t.Fatal(err)
	}
	if copies == 0 {
		t.Fatal("withdrawal deleted existing project copies")
	}
}

func TestTemplateGovernanceRuntimeRejectsUnknownIncompleteReplayAndAdvicelessRecall(t *testing.T) {
	r, _, actor, _, ctx := governanceAdapterFixture(t)
	svc := r.Service()
	govActor := governanceActor(actor)
	published, fixtures := seedDemoRuleSetFixture(t, r, actor, ctx)
	if _, err := svc.CreateProfile(ctx, govActor, "create-demo-profile", governance.CreateProfileInput{ID: template.DemoTemplateID, Name: "内置审阅目录", Reason: "actual catalog registration"}); err != nil {
		t.Fatal(err)
	}
	draft := saveDemoTemplateDraftFixture(t, r, actor, published, ctx)
	stillDraft := func() {
		t.Helper()
		current, err := svc.GetVersion(ctx, govActor, draft.TemplateID, draft.Version)
		if err != nil {
			t.Fatal(err)
		}
		if current.State != template.RuleSetDraft {
			t.Fatalf("rejected publish changed state to %s", current.State)
		}
	}
	unknown := append(append([]string{}, fixtures...), "demo-no-such-fixture")
	if _, err := svc.Publish(ctx, govActor, "publish-unknown-fixture", governance.PublishInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, ReplayFixtureIDs: unknown, Reason: "unknown id must fail closed"}); !errors.Is(err, governance.ErrReplayRequired) {
		t.Fatalf("unknown fixture id accepted: %v", err)
	}
	stillDraft()
	positives := []string{}
	for _, record := range BuiltinTemplateGovernanceFixtures() {
		if record.Fixture.Polarity == "positive" {
			positives = append(positives, record.Fixture.ID)
		}
	}
	if _, err := svc.Publish(ctx, govActor, "publish-positive-only", governance.PublishInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, ReplayFixtureIDs: positives, Reason: "incomplete coverage must fail closed"}); !errors.Is(err, governance.ErrReplayRequired) {
		t.Fatalf("positive-only replay accepted: %v", err)
	}
	stillDraft()
	saved, err := svc.Publish(ctx, govActor, "publish-demo-template", governance.PublishInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, ReplayFixtureIDs: fixtures, Reason: "actual replay"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Recall(ctx, govActor, "recall-no-advice", governance.RecallInput{GovernanceInput: governance.GovernanceInput{TemplateID: saved.TemplateID, Version: saved.Version, ExpectedRevision: saved.Revision, Reason: "advice missing"}}); !errors.Is(err, governance.ErrInvalidRequest) {
		t.Fatalf("recall without migration advice accepted: %v", err)
	}
}

type bridgeModelStub struct {
	calls   int
	request core.ModelRequest
}

func (m *bridgeModelStub) Reply(_ context.Context, _ core.Actor, request core.ModelRequest) (core.ModelReply, error) {
	m.calls++
	m.request = request
	return core.ModelReply{StructuredOutput: json.RawMessage(`{"proposals":[],"explanation":"synthetic fixture only"}`), ModelID: "synthetic-fixture", RunMode: "real_api_fake_model", InputTokens: 12, OutputTokens: 5}, nil
}
func TestTemplateGovernanceRuntimeModelUsesActualAgentContextOnce(t *testing.T) {
	r, db, actor, p, ctx := governanceAdapterFixture(t)
	_, ref := adapterSourceFixture(t, r, db, p, actor)
	var basis governance.VersionBasis
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		basis, err = r.CurrentProjectBasis(ctx, tx, governanceActor(actor), p.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	scope := governance.SuggestionScope{ProjectID: p.ID, Basis: basis, TemplateID: p.TemplateID, TemplateVersion: p.TemplateVersion, RuleSet: basis.RuleSet, Sources: []governance.SourceRef{ref}, TargetKind: "field", TargetID: "research_goal"}
	authorized, err := r.Build(ctx, governanceActor(actor), scope)
	if err != nil {
		t.Fatal(err)
	}
	model := &bridgeModelStub{}
	r.model = model
	output, err := r.Suggest(ctx, governanceActor(actor), governance.RuleSuggestionRequest{Scope: scope, Template: p.TemplateCopy.Definition, Context: authorized, CorrelationID: "one-paid-boundary"})
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 || model.request.Purpose != "rule_suggestion" || model.request.Context.SourceRefs[0].QuotedTextHash != governanceRuntimeHash(model.request.Context.SourceRefs[0].QuotedText) || output.RunMode != "real_api_fake_model" {
		t.Fatal("shared agent bridge identity/once-only contract broken")
	}
	authorized.Sources[0].QuotedText = "client fake text"
	authorized.Sources[0].QuotedTextHash = governanceRuntimeHash(authorized.Sources[0].QuotedText)
	if _, err := r.Suggest(ctx, governanceActor(actor), governance.RuleSuggestionRequest{Scope: scope, Context: authorized}); err == nil || model.calls != 1 {
		t.Fatal("client-authorized quote reached model")
	}
}
func TestTemplateGovernanceRuntimeCheckerUsesPersistedReviewDecisions(t *testing.T) {
	r, projects, db, actor, p, ctx := governanceAdapterFixtureWithProjects(t)
	p = activateProjectFixture(t, projects, actor, p, ctx)
	chapters := writeConfirmedChaptersFixture(t, projects, db, actor, p, ctx)
	check := func() []core.ImpactBlocker {
		t.Helper()
		var blockers []core.ImpactBlocker
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			p, err := r.review.AuthorizeInTransaction(ctx, core.NewTransactionAdapter(tx), actor, p.ID, "read")
			if err != nil {
				return err
			}
			blockers, err = r.Check(core.NewTransactionAdapter(tx), p)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return blockers
	}
	if blockers := check(); len(blockers) != 0 {
		t.Fatalf("actual complete input blocked: %+v", blockers)
	}
	if err := db.Exec("UPDATE lingdoc_chapter_versions SET review_items_json=? WHERE chapter_id=?", `[{"id":"new-review","statement":"待核项不能自证通过","origin_candidate_id":"fixture"}]`, chapters[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	blockers := check()
	found := false
	for _, b := range blockers {
		if b.Code == "rule:"+template.RuleReviewItems {
			found = true
		}
	}
	if !found {
		t.Fatalf("confirmation boolean replaced actual decisions: %+v", blockers)
	}
}
