package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type catalogSwitchRuleSetReader struct{ store *rulesetstore.Store }

func (r catalogSwitchRuleSetReader) GetVersion(ctx context.Context, actor core.Actor, id, version string) (template.RuleSet, error) {
	return r.store.GetVersion(ctx, governanceActor(actor), id, version)
}
func (r catalogSwitchRuleSetReader) GetVersionInTransaction(ctx context.Context, tx core.Transaction, actor core.Actor, id, version string) (template.RuleSet, error) {
	db, err := core.TransactionDatabase(tx)
	if err != nil {
		return template.RuleSet{}, err
	}
	return r.store.GetVersionForBinding(ctx, db, governanceActor(actor), id, version)
}

// Publish a non-demo catalog version using registered, executed positive and
// negative rule fixtures. No published catalog row is seeded directly.
func newCatalogSwitchHTTPHarness(t *testing.T) (*migrationRiskHarness, *TemplateGovernanceRuntime, governance.TemplateVersion, template.RuleSet) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	runtime, projects, db, actor, project, ctx := governanceAdapterFixtureWithProjects(t)
	store := rulesetstore.NewStore(db, runtime)
	set, err := TemplateGovernanceDemoRuleSet()
	if err != nil {
		t.Fatal(err)
	}
	set.ID, set.State, set.RulesetHash = "catalog-switch-rules", template.RuleSetDraft, ""
	for i := range set.Bindings {
		set.Bindings[i].PositiveExampleRefs = nil
		set.Bindings[i].NegativeExampleRefs = nil
	}
	if err := store.CreateDraft(ctx, governanceActor(actor), set); err != nil {
		t.Fatal(err)
	}
	set, err = store.Publish(ctx, governanceActor(actor), set.ID, set.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Service().CreateProfile(ctx, governanceActor(actor), "switch-profile", governance.CreateProfileInput{ID: "catalog-custom", Name: "Custom catalog target", Reason: "reviewed test target"}); err != nil {
		t.Fatal(err)
	}
	definition := template.DemoTemplate()
	definition.ID = "catalog-custom"
	definition.Rules, definition.ContentHash, definition.RulesetHash = nil, "", ""
	draft, err := runtime.Service().SaveDraft(ctx, governanceActor(actor), "switch-draft", governance.SaveDraftInput{
		TemplateID: definition.ID, Version: definition.Version, Definition: definition,
		RuleSet:    governance.RuleSetRef{ID: set.ID, Version: set.Version, Hash: set.RulesetHash},
		SourceType: "lingdoc_builtin", Scope: "internal-demo", Curator: actor.UserID, Reason: "reviewed test target",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewed := BuiltinTemplateGovernanceFixtures()
	fixtureIDs := make([]string, 0, len(reviewed))
	for i := range reviewed {
		reviewed[i].Fixture.ID = "custom-" + reviewed[i].Fixture.ID
		reviewed[i].Fixture.RuleSet = draft.RuleSet
		reviewed[i].TemplateID, reviewed[i].TemplateVersion = draft.TemplateID, draft.Version
		reviewed[i].TemplateContentHash, reviewed[i].PolicyHash = draft.ContentHash, draft.PolicyHash
		fixtureIDs = append(fixtureIDs, reviewed[i].Fixture.ID)
	}
	runtime, err = NewTemplateGovernanceRuntime(db, projects, nil, nil, WithReviewedTemplateFixtures(reviewed))
	if err != nil {
		t.Fatal(err)
	}
	published, err := runtime.Service().Publish(ctx, governanceActor(actor), "switch-publish", governance.PublishInput{
		TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, ReplayFixtureIDs: fixtureIDs, Reason: "all fixture evaluations passed",
	})
	if err != nil {
		t.Fatal(err)
	}
	projects.SetTemplateCatalogResolver(runtime)
	projects.WithRuleSetSnapshotReader(catalogSwitchRuleSetReader{store: rulesetstore.NewStore(db, runtime)})
	handler, err := NewHandler(HandlerDependencies{Projects: projects, Members: newMembershipService(projects, core.NewGORMRepository(db)), Chapters: projects,
		TemplateApp: projects, ChangeSets: projects, Sources: emptySourcesStub{}})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	handler.Register(RouteGroups{Read: router.Group("/api/v1/lingdoc"), Write: router.Group("/api/v1/lingdoc")})
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	return &migrationRiskHarness{t: t, db: db, router: router, ctx: ctx, actor: actor, project: project}, runtime, published, set
}

func switchCount(t *testing.T, db *gorm.DB, outcome string) int64 {
	t.Helper()
	var n int64
	if err := db.Table("audit_logs").Where("action = ? AND outcome = ?", "lingdoc.template.switch", outcome).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPublishedCatalogSwitchHTTPRiskAtomicAuditAndRecall(t *testing.T) {
	h, runtime, published, set := newCatalogSwitchHTTPHarness(t)
	createBody, _ := json.Marshal(core.CreateProjectInput{Name: "new catalog project", TemplateID: published.TemplateID, TemplateVersion: published.Version})
	createResponse := governanceHTTPRequest(h.router, h.ctx, http.MethodPost, "/api/v1/lingdoc/projects", string(createBody), "catalog-create-project")
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("published catalog project creation: %d %s", createResponse.Code, createResponse.Body.String())
	}
	var createEnvelope struct{ Data core.Project }
	if err := json.Unmarshal(createResponse.Body.Bytes(), &createEnvelope); err != nil {
		t.Fatal(err)
	}
	created := createEnvelope.Data
	if created.TemplateID != published.TemplateID || created.TemplateCopy == nil || created.TemplateCopy.SourceRuleSetHash != set.RulesetHash || len(created.Spec) != 0 {
		t.Fatalf("new empty catalog project lost its bound source: %+v", created)
	}
	var copySuccess int64
	if err := h.db.Table("audit_logs").Where("action=? AND outcome=?", "lingdoc.template.copy", "success").Count(&copySuccess).Error; err != nil || copySuccess != 1 {
		t.Fatalf("new catalog project did not retain its true copy receipt: count=%d err=%v", copySuccess, err)
	}
	input := core.TemplateMigrationInput{ExpectedProjectVersion: h.project.ProjectVersion, TemplateID: published.TemplateID, TemplateVersion: published.Version}
	var preview core.TemplateMigrationPreview
	h.call(h.ctx, http.MethodPost, "/template-migration/preview", "", input, http.StatusOK, &preview)
	if !slices.Contains(preview.MissingRequired, "research_goal") || preview.TargetRulesetHash != set.RulesetHash || preview.TargetContentHash != published.ContentHash {
		t.Fatalf("preview lost risk or catalog snapshot identity: %+v", preview)
	}
	before, copies, audits := h.snapshot("project_template.change")
	h.call(h.ctx, http.MethodPost, "/template-migration", "catalog-risk", input, http.StatusBadRequest, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.change", "catalog-risk")
	if switchCount(t, h.db, "success") != 0 || switchCount(t, h.db, "failure") != 1 {
		t.Fatal("risk refusal did not produce exactly one failure receipt")
	}
	// Use a separate published rule with a distinct ID; binding the demo
	// projection itself would be rejected earlier for duplicate rule IDs and
	// would not prove that field risk caused this refusal.
	bindStore := rulesetstore.NewStore(h.db, runtime)
	bindSet := template.RuleSet{ID: "binding-risk-rules", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{{
		RuleID: "additional-goal", DefinitionID: "presence", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal",
		Severity: "warning", TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "office",
	}}}
	if err := bindStore.CreateDraft(h.ctx, governanceActor(h.actor), bindSet); err != nil {
		t.Fatal(err)
	}
	bindSet, err := bindStore.Publish(h.ctx, governanceActor(h.actor), bindSet.ID, bindSet.Version)
	if err != nil {
		t.Fatal(err)
	}
	bind := core.BindTemplateCopyRuleSetInput{ExpectedProjectVersion: h.project.ProjectVersion, ExpectedTemplateCopyVersion: h.project.TemplateCopyVersion, RuleSetID: bindSet.ID, RuleSetVersion: bindSet.Version}
	before, copies, audits = h.snapshot("project_template.bind_ruleset")
	h.call(h.ctx, http.MethodPut, "/template-copy/rule-set", "catalog-bind-risk", bind, http.StatusBadRequest, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.bind_ruleset", "catalog-bind-risk")
	h.saveSpec("catalog-fill", map[string]string{"research_subject": "subject", "research_goal": "goal"})
	bind.ExpectedProjectVersion, bind.ExpectedTemplateCopyVersion = h.project.ProjectVersion, h.project.TemplateCopyVersion
	if h.call(h.ctx, http.MethodPut, "/template-copy/rule-set", "catalog-bind-risk", bind, http.StatusOK, &h.project) {
		t.Fatal("corrected RuleSet binding unexpectedly replayed")
	}
	if !h.call(h.ctx, http.MethodPut, "/template-copy/rule-set", "catalog-bind-risk", bind, http.StatusOK, nil) {
		t.Fatal("successful RuleSet binding retry was not replayed")
	}
	input.ExpectedProjectVersion = h.project.ProjectVersion
	stale := input
	stale.ExpectedProjectVersion--
	before, copies, audits = h.snapshot("project_template.change")
	h.call(h.ctx, http.MethodPost, "/template-migration", "catalog-stale", stale, http.StatusConflict, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.change", "catalog-stale")
	h.call(h.outsider(), http.MethodPost, "/template-migration", "catalog-denied", input, http.StatusNotFound, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.change", "catalog-denied")
	h.call(h.ctx, http.MethodPost, "/template-migration/preview", "", input, http.StatusOK, &preview)
	if len(preview.MissingRequired)+len(preview.Orphaned)+len(preview.Incompatible) != 0 {
		t.Fatalf("resolved fields still block: %+v", preview)
	}
	if err := h.db.Exec(`CREATE TRIGGER fail_switch_success BEFORE INSERT ON audit_logs WHEN NEW.action = 'lingdoc.template.switch' AND NEW.outcome = 'success' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`).Error; err != nil {
		t.Fatal(err)
	}
	h.call(h.ctx, http.MethodPost, "/template-migration", "catalog-switch", input, http.StatusInternalServerError, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.change", "catalog-switch")
	if switchCount(t, h.db, "success") != 0 || switchCount(t, h.db, "failure") != 2 {
		t.Fatal("audit failure did not roll back success and persist a failure receipt")
	}
	if err := h.db.Exec("DROP TRIGGER fail_switch_success").Error; err != nil {
		t.Fatal(err)
	}
	if h.call(h.ctx, http.MethodPost, "/template-migration", "catalog-switch", input, http.StatusOK, &h.project) {
		t.Fatal("corrected request unexpectedly replayed")
	}
	if h.project.TemplateID != published.TemplateID || h.project.TemplateCopy.RulesetHash != preview.TargetRulesetHash || h.project.TemplateCopy.ContentHash != preview.TargetContentHash || h.project.TemplateCopy.SourceRuleSetHash != set.RulesetHash || h.project.TemplateCopy.SourcePolicyHash != published.PolicyHash {
		t.Fatalf("published source identity or preview hash did not persist: %+v", h.project.TemplateCopy)
	}
	if !h.call(h.ctx, http.MethodPost, "/template-migration", "catalog-switch", input, http.StatusOK, nil) || switchCount(t, h.db, "success") != 1 {
		t.Fatal("idempotent retry did not return the single successful switch")
	}
	var details string
	if err := h.db.Table("audit_logs").Select("details").Where("action=? AND outcome=?", "lingdoc.template.switch", "success").Scan(&details).Error; err != nil {
		t.Fatal(err)
	}
	var receipt map[string]any
	if err := json.Unmarshal([]byte(details), &receipt); err != nil || receipt["reason"] != "existing project switched to this exact published template version" {
		t.Fatalf("wrong switch receipt: %s, %v", details, err)
	}
	historyResponse := governanceHTTPRequest(h.router, h.ctx, http.MethodGet, "/api/v1/lingdoc/template-catalog/profiles/"+published.TemplateID+"/audit-events?limit=100", "", "")
	if historyResponse.Code != http.StatusOK {
		t.Fatalf("catalog audit history: %d %s", historyResponse.Code, historyResponse.Body.String())
	}
	var history struct{ Data governance.AuditHistoryPage }
	if err := json.Unmarshal(historyResponse.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	var visibleSuccess, visibleFailure int
	for _, event := range history.Data.Events {
		if event.Action == "lingdoc.template.switch" && event.Outcome == "success" {
			visibleSuccess++
		}
		if event.Action == "lingdoc.template.switch" && event.Outcome == "failure" {
			visibleFailure++
		}
	}
	if visibleSuccess != 1 || visibleFailure != 2 {
		t.Fatalf("catalog history omitted switch receipts: success=%d failure=%d", visibleSuccess, visibleFailure)
	}
	recalled, err := runtime.Service().Recall(h.ctx, governanceActor(h.actor), "catalog-recall", governance.RecallInput{GovernanceInput: governance.GovernanceInput{
		TemplateID: published.TemplateID, Version: published.Version, ExpectedRevision: published.Revision, Reason: "recall test"}, MigrationAdvice: "choose an available version"})
	if err != nil || recalled.Recall == nil || !slices.Contains(recalled.Recall.AffectedProjectIDs, h.project.ID) || !slices.Contains(recalled.Recall.AffectedProjectIDs, created.ID) {
		t.Fatalf("recall lost switched project: %+v, %v", recalled.Recall, err)
	}
	createAfterRecall := governanceHTTPRequest(h.router, h.ctx, http.MethodPost, "/api/v1/lingdoc/projects", string(createBody), "catalog-create-recalled")
	if createAfterRecall.Code != http.StatusUnprocessableEntity {
		t.Fatalf("recalled catalog version was copied: %d %s", createAfterRecall.Code, createAfterRecall.Body.String())
	}
	var createdCount int64
	if err := h.db.Table("lingdoc_projects").Where("name=?", "new catalog project").Count(&createdCount).Error; err != nil || createdCount != 1 {
		t.Fatalf("recalled creation left a project: count=%d err=%v", createdCount, err)
	}
	input.ExpectedProjectVersion = h.project.ProjectVersion
	before, copies, audits = h.snapshot("project_template.change")
	h.call(h.ctx, http.MethodPost, "/template-migration/preview", "", input, http.StatusUnprocessableEntity, nil)
	h.call(h.ctx, http.MethodPost, "/template-migration", "catalog-recalled", input, http.StatusUnprocessableEntity, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.change", "catalog-recalled")
}

func TestPublishedCatalogWithdrawAndDemoFallbackHTTP(t *testing.T) {
	h, runtime, published, _ := newCatalogSwitchHTTPHarness(t)
	demoBody, _ := json.Marshal(core.CreateProjectInput{Name: "fixed demo project", TemplateID: template.DemoTemplateID})
	demoResponse := governanceHTTPRequest(h.router, h.ctx, http.MethodPost, "/api/v1/lingdoc/projects", string(demoBody), "catalog-create-demo")
	if demoResponse.Code != http.StatusCreated {
		t.Fatalf("fixed demo project creation: %d %s", demoResponse.Code, demoResponse.Body.String())
	}
	var demoCopySuccess int64
	if err := h.db.Table("audit_logs").Where("action=? AND outcome=?", "lingdoc.template.copy", "success").Count(&demoCopySuccess).Error; err != nil || demoCopySuccess != 0 {
		t.Fatalf("fixed demo creation was represented as a published catalog copy: count=%d err=%v", demoCopySuccess, err)
	}
	h.saveSpec("withdraw-fill", map[string]string{"research_subject": "subject", "research_goal": "goal"})
	_, err := runtime.Service().Withdraw(h.ctx, governanceActor(h.actor), "catalog-withdraw", governance.GovernanceInput{TemplateID: published.TemplateID, Version: published.Version, ExpectedRevision: published.Revision, Reason: "withdraw test"})
	if err != nil {
		t.Fatal(err)
	}
	input := core.TemplateMigrationInput{ExpectedProjectVersion: h.project.ProjectVersion, TemplateID: published.TemplateID, TemplateVersion: published.Version}
	before, copies, audits := h.snapshot("project_template.change")
	h.call(h.ctx, http.MethodPost, "/template-migration/preview", "", input, http.StatusUnprocessableEntity, nil)
	h.call(h.ctx, http.MethodPost, "/template-migration", "catalog-withdrawn", input, http.StatusUnprocessableEntity, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.change", "catalog-withdrawn")
	input.TemplateID, input.TemplateVersion = template.DemoTemplateID, ""
	var preview core.TemplateMigrationPreview
	h.call(h.ctx, http.MethodPost, "/template-migration/preview", "", input, http.StatusOK, &preview)
	h.call(h.ctx, http.MethodPost, "/template-migration", "catalog-demo-fallback", input, http.StatusOK, &h.project)
	if h.project.TemplateID != template.DemoTemplateID || switchCount(t, h.db, "success") != 0 {
		t.Fatal("fixed demo fallback was misrepresented as a published catalog switch")
	}
}

func TestPublishedCatalogSwitchRejectsWithdrawnRuleSetHTTP(t *testing.T) {
	h, runtime, published, set := newCatalogSwitchHTTPHarness(t)
	h.saveSpec("ruleset-withdraw-fill", map[string]string{"research_subject": "subject", "research_goal": "goal"})
	if _, err := rulesetstore.NewStore(h.db, runtime).Withdraw(h.ctx, governanceActor(h.actor), set.ID, set.Version); err != nil {
		t.Fatal(err)
	}
	input := core.TemplateMigrationInput{ExpectedProjectVersion: h.project.ProjectVersion, TemplateID: published.TemplateID, TemplateVersion: published.Version}
	before, copies, audits := h.snapshot("project_template.change")
	h.call(h.ctx, http.MethodPost, "/template-migration/preview", "", input, http.StatusConflict, nil)
	h.call(h.ctx, http.MethodPost, "/template-migration", "catalog-withdrawn-rule", input, http.StatusConflict, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.change", "catalog-withdrawn-rule")
	body, _ := json.Marshal(core.CreateProjectInput{Name: "withdrawn rule set project", TemplateID: published.TemplateID, TemplateVersion: published.Version})
	response := governanceHTTPRequest(h.router, h.ctx, http.MethodPost, "/api/v1/lingdoc/projects", string(body), "catalog-create-withdrawn-rule")
	if response.Code != http.StatusConflict {
		t.Fatalf("new project accepted withdrawn RuleSet: %d %s", response.Code, response.Body.String())
	}
	var count int64
	if err := h.db.Table("lingdoc_projects").Where("name=?", "withdrawn rule set project").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("withdrawn RuleSet creation wrote a project: count=%d err=%v", count, err)
	}
}

func TestSaveSpecOmittedValuesAndMetadataOnlyHTTP(t *testing.T) {
	h := newMigrationRiskHarness(t, nil)
	h.saveSpec("spec-preserve-base", map[string]string{"research_subject": "subject", "research_goal": "goal"})
	metadata := h.project.SpecFields
	metadata["legacy_only"] = core.SpecField{Value: "historical metadata value", Origin: "human", Status: "draft", ModifiedBy: "legacy"}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec("UPDATE lingdoc_projects SET spec_metadata_json=? WHERE id=?", string(raw), h.project.ID).Error; err != nil {
		t.Fatal(err)
	}
	var preview core.TemplateMigrationPreview
	h.call(h.ctx, http.MethodPost, "/template-migration/preview", "", core.TemplateMigrationInput{ExpectedProjectVersion: h.project.ProjectVersion, TemplateID: template.DemoTemplateID}, http.StatusOK, &preview)
	if !slices.Contains(preview.Orphaned, "legacy_only") {
		t.Fatalf("metadata-only orphan was not exposed for archive: %+v", preview)
	}
	before, copies, audits := h.snapshot("project_spec.save")
	h.call(h.ctx, http.MethodPut, "/spec", "spec-omit-value", core.SaveSpecInput{ExpectedSpecRevision: h.project.SpecRevision, Fields: map[string]string{"research_subject": "subject"}}, http.StatusBadRequest, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_spec.save", "spec-omit-value")
	h.saveSpec("spec-preserve-full", map[string]string{"research_subject": "updated subject", "research_goal": "goal"})
	if _, present := h.project.Spec["legacy_only"]; present || !reflect.DeepEqual(h.project.SpecFields["legacy_only"], metadata["legacy_only"]) {
		t.Fatalf("save lost or materialized metadata-only field: %+v", h.project)
	}
}

func TestRevokedMetadataOnlyFieldIsRedactedWithoutMaterializingHTTP(t *testing.T) {
	f := newNativeProvenanceFixture(t)
	input := f.specInput("secret sourced legacy value")
	raw, _, _, err := f.projects.SaveSpec(f.ctx, f.actor, f.project.ID, "spec-source-seed", input)
	if err != nil {
		t.Fatal(err)
	}
	var project core.Project
	if err := json.Unmarshal(raw, &project); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(HandlerDependencies{Projects: f.projects, Members: newMembershipService(f.projects, core.NewGORMRepository(f.db)), Chapters: f.projects,
		TemplateApp: f.projects, ChangeSets: f.projects, Sources: emptySourcesStub{}})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	handler.Register(RouteGroups{Read: router.Group("/api/v1/lingdoc"), Write: router.Group("/api/v1/lingdoc")})
	h := &migrationRiskHarness{t: t, db: f.db, router: router, ctx: f.ctx, actor: f.actor, project: project}
	before, copies, audits := h.snapshot("project_spec.save")
	h.call(h.ctx, http.MethodPut, "/spec", "spec-omit-sourced", core.SaveSpecInput{ExpectedSpecRevision: project.SpecRevision, Fields: map[string]string{"research_goal": input.Fields["research_goal"]}}, http.StatusBadRequest, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_spec.save", "spec-omit-sourced")
	var storedBefore struct{ SpecJSON, SpecMetadataJSON string }
	if err := f.db.Table("lingdoc_projects").Select("spec_json,spec_metadata_json").Where("id=?", project.ID).Take(&storedBefore).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(storedBefore.SpecJSON, input.Fields["research_subject"]) || !strings.Contains(storedBefore.SpecMetadataJSON, input.Fields["research_subject"]) {
		t.Fatal("rejected omission erased the sourced value or its metadata")
	}
	// Model a historical row with metadata but no value, then actually revoke
	// the source. The stored metadata and server provenance pin remain intact.
	if err := f.db.Exec("UPDATE lingdoc_projects SET spec_json=? WHERE id=?", `{"research_goal":"human goal"}`, project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id=?", "kb-runtime").Error; err != nil {
		t.Fatal(err)
	}
	var visible core.Project
	h.call(h.ctx, http.MethodGet, "", "", nil, http.StatusOK, &visible)
	field := visible.SpecFields["research_subject"]
	if _, present := visible.Spec["research_subject"]; present || field.Value != "" || field.Provenance == nil || field.Provenance.Accessible || field.Provenance.Locator != "" || field.Provenance.Purpose != "" {
		t.Fatalf("revoked metadata-only source leaked or materialized: %+v", visible)
	}
	if encoded, _ := json.Marshal(visible); strings.Contains(string(encoded), input.Fields["research_subject"]) {
		t.Fatal("HTTP read exposed revoked field content")
	}
	h.call(h.ctx, http.MethodPut, "/spec", "spec-save-other-after-revoke", core.SaveSpecInput{ExpectedSpecRevision: project.SpecRevision, Fields: map[string]string{"research_goal": "updated human goal"}}, http.StatusOK, &visible)
	if _, present := visible.Spec["research_subject"]; present || visible.SpecFields["research_subject"].Value != "" {
		t.Fatalf("HTTP save materialized or exposed the revoked field: %+v", visible)
	}
	var storedAfter struct{ SpecJSON, SpecMetadataJSON string }
	if err := f.db.Table("lingdoc_projects").Select("spec_json,spec_metadata_json").Where("id=?", project.ID).Take(&storedAfter).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedAfter.SpecJSON, "research_subject") || !strings.Contains(storedAfter.SpecMetadataJSON, input.Fields["research_subject"]) {
		t.Fatalf("save did not preserve the raw metadata without materializing the value: %+v", storedAfter)
	}
}
