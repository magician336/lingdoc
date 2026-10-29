package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// demoFixtureInputs derives a registration batch for the demo draft from the
// builtin records' already-correct semantic fields, under fresh fixture ids.
// The ids deliberately differ from the builtin set (D4): a tenant batch can
// never claim a builtin identity, but it can carry the same real evaluated
// inputs against the same pin.
func demoFixtureInputs(prefix string) []governance.ReplayFixtureInput {
	inputs := []governance.ReplayFixtureInput{}
	for _, record := range BuiltinTemplateGovernanceFixtures() {
		inputs = append(inputs, governance.ReplayFixtureInput{
			FixtureID:       prefix + record.Fixture.ID,
			RuleID:          record.Fixture.RuleID,
			Scene:           record.Fixture.Scene,
			Polarity:        record.Fixture.Polarity,
			TargetRef:       record.Fixture.TargetRef,
			Input:           record.Fixture.Input,
			ExpectedStatus:  record.Fixture.ExpectedStatus,
			ReviewReference: "review:g4-769-registration",
		})
	}
	return inputs
}

// orgFixtureInputs covers the tracer's single presence binding (both trigger
// scenes × both polarities) with actually evaluated inputs.
func orgFixtureInputs(prefix string) []governance.ReplayFixtureInput {
	inputs := []governance.ReplayFixtureInput{}
	for _, scene := range []string{"write_check", "preflight"} {
		for _, polarity := range []string{"positive", "negative"} {
			fields := map[string]string{}
			status := template.EvaluationIssue
			if polarity == "positive" {
				fields["research_goal"] = "R-6 tracer objective"
				status = template.EvaluationPass
			}
			inputs = append(inputs, governance.ReplayFixtureInput{
				FixtureID:       prefix + scene + "-" + polarity,
				RuleID:          "goal-present",
				Scene:           scene,
				Polarity:        polarity,
				TargetRef:       "field/research_goal",
				Input:           template.EvaluationInput{ProjectID: "registration", TargetVersion: "1", Fields: fields},
				ExpectedStatus:  status,
				ReviewReference: "review:g4-769-registration",
			})
		}
	}
	return inputs
}

// seedOrganizationTemplateFixture builds the R-6 tracer's organization
// template: a published organization rule set (bindings declare no example
// refs, so registered fixtures alone can satisfy publication), a catalog
// profile, and the bound draft version.
func seedOrganizationTemplateFixture(t *testing.T, runtime *TemplateGovernanceRuntime, db *gorm.DB, owner core.Actor, ctx context.Context) governance.TemplateVersion {
	t.Helper()
	set := template.RuleSet{ID: "org-rules", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{{RuleID: "goal-present", DefinitionID: "presence", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal", Severity: template.SeverityWarning, TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: owner.UserID}}}
	store := rulesetstore.NewStore(db, runtime)
	if err := store.CreateDraft(ctx, governanceActor(owner), set); err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(ctx, governanceActor(owner), set.ID, set.Version)
	if err != nil {
		t.Fatal(err)
	}
	svc := runtime.Service()
	if _, err := svc.CreateProfile(ctx, governanceActor(owner), "create-org-template-profile", governance.CreateProfileInput{ID: "org-template", Name: "组织模板", Reason: "R-6 tracer bullet"}); err != nil {
		t.Fatal(err)
	}
	def := template.Template{Fields: []template.Field{{ID: "research_goal", Label: "Goal", Type: "string", Required: true}}, RequiredFields: []string{"research_goal"}, Sections: []template.Section{{ID: "method", Title: "Method", Required: true, Order: 1}}}
	saved, err := svc.SaveDraft(ctx, governanceActor(owner), "save-org-template-draft", governance.SaveDraftInput{TemplateID: "org-template", Version: "1", Definition: def, RuleSet: governance.RuleSetRef{ID: published.ID, Version: published.Version, Hash: published.RulesetHash}, SourceType: "lingdoc_builtin", Scope: "organization", Curator: owner.UserID, Reason: "R-6 tracer bullet"})
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func registerFixturesHTTP(t *testing.T, router *gin.Engine, ctx context.Context, draft governance.TemplateVersion, key string, fixtures []governance.ReplayFixtureInput) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(governance.RegisterFixturesInput{TemplateID: draft.TemplateID, Version: draft.Version, Fixtures: fixtures, Reason: "g4 registration acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/lingdoc/template-catalog/profiles/" + draft.TemplateID + "/versions/" + draft.Version + "/replay-fixtures"
	return governanceHTTPRequest(router, ctx, http.MethodPost, path, string(body), key)
}

// decodeGovernanceResponse asserts the status and decodes the data envelope;
// it answers whether the response carried the operation-replayed meta flag.
func decodeGovernanceResponse(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, target any) bool {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d: %s", response.Code, wantStatus, response.Body.String())
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if target == nil {
		return envelope.Meta.Replayed
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		t.Fatal(err)
	}
	return envelope.Meta.Replayed
}

// The tracer bullet (R-6): an organization template registers its own replay
// fixtures over HTTP, reads the coverage matrix, publishes with those
// registered fixtures only — from a second runtime instance over the same
// database, proving persistence rather than process memory — and the published
// template copies into a new project.
func TestTemplateVersionReplayFixtureRegistrationHTTP(t *testing.T) {
	runtime, projects, db, owner, _, ctx := governanceAdapterFixtureWithProjects(t)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	draft := seedOrganizationTemplateFixture(t, runtime, db, owner, ctx)

	registered := registerFixturesHTTP(t, router, ctx, draft, "http-register-1", orgFixtureInputs("org-reg-"))
	var receipt governance.RegisterFixturesReceipt
	if replayed := decodeGovernanceResponse(t, registered, http.StatusCreated, &receipt); replayed {
		t.Fatal("first registration reported replayed")
	}
	if receipt.ContentHash != draft.ContentHash || receipt.PolicyHash != draft.PolicyHash {
		t.Fatalf("server-fixed pin missing: %+v", receipt)
	}
	if len(receipt.Registered) != 4 || !receipt.Coverage.Complete {
		t.Fatalf("receipt = %d registered, complete=%v", len(receipt.Registered), receipt.Coverage.Complete)
	}
	registeredIDs := make([]string, 0, 4)
	for _, summary := range receipt.Registered {
		if summary.RuleSet.ID != "org-rules" || summary.RuleSet.Hash != draft.RulesetHash {
			t.Fatalf("client-influenced ruleset pin: %+v", summary.RuleSet)
		}
		registeredIDs = append(registeredIDs, summary.FixtureID)
	}

	// The same key with the same request replays the same receipt.
	if replayed := decodeGovernanceResponse(t, registerFixturesHTTP(t, router, ctx, draft, "http-register-1", orgFixtureInputs("org-reg-")), http.StatusCreated, nil); !replayed {
		t.Fatal("same-key replay did not report replayed")
	}
	// The same key with a different request is a conflict.
	conflicting := registerFixturesHTTP(t, router, ctx, draft, "http-register-1", orgFixtureInputs("org-other-"))
	if conflicting.Code != http.StatusConflict || !strings.Contains(conflicting.Body.String(), "idempotency_conflict") {
		t.Fatalf("same key different request = %d %s", conflicting.Code, conflicting.Body.String())
	}

	coveragePath := "/api/v1/lingdoc/template-catalog/profiles/" + draft.TemplateID + "/versions/" + draft.Version + "/replay-coverage"
	var coverage governance.ReplayCoverageView
	decodeGovernanceResponse(t, governanceHTTPRequest(router, ctx, http.MethodGet, coveragePath, "", ""), http.StatusOK, &coverage)
	if !coverage.Complete || coverage.ContentHash != draft.ContentHash {
		t.Fatalf("coverage view = complete:%v content:%s", coverage.Complete, coverage.ContentHash)
	}

	// A second runtime instance over the same database answers from stored
	// rows and carries the publication with the registered fixtures only.
	runtime2, err := NewTemplateGovernanceRuntime(db, projects, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	projects.SetTemplateCatalogResolver(runtime2)
	router2 := gin.New()
	RegisterTemplateGovernanceRoutes(router2.Group("/api/v1/lingdoc"), runtime2)
	h, err := NewHandler(HandlerDependencies{Projects: projects, Members: newMembershipService(projects, core.NewGORMRepository(db)), Chapters: projects,
		TemplateApp: projects, ChangeSets: projects, Sources: emptySourcesStub{}})
	if err != nil {
		t.Fatal(err)
	}
	router2.Group("/api/v1/lingdoc").POST("/projects", h.createProject)
	catalogResponse := governanceHTTPRequest(router2, ctx, http.MethodGet, "/api/v1/lingdoc/template-catalog/replay-fixtures", "", "")
	var catalog struct {
		Data []ReplayFixtureSummary `json:"data"`
	}
	if err := json.Unmarshal(catalogResponse.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalogResponse.Code != http.StatusOK || len(catalog.Data) != 24 {
		t.Fatalf("merged fixture catalog = %d %s, want builtin 20 + registered 4", len(catalog.Data), catalogResponse.Body.String())
	}
	previous := ""
	for _, summary := range catalog.Data {
		if summary.FixtureID <= previous {
			t.Fatalf("catalog not in stable order: %q after %q", summary.FixtureID, previous)
		}
		previous = summary.FixtureID
	}

	publishBody, err := json.Marshal(governance.PublishInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, ReplayFixtureIDs: registeredIDs, Reason: "published on registered fixtures only"})
	if err != nil {
		t.Fatal(err)
	}
	publishResponse := governanceHTTPRequest(router2, ctx, http.MethodPost, "/api/v1/lingdoc/template-catalog/profiles/"+draft.TemplateID+"/versions/"+draft.Version+"/publish", string(publishBody), "http-publish-registered")
	if publishResponse.Code != http.StatusOK {
		t.Fatalf("publish on registered fixtures = %d %s", publishResponse.Code, publishResponse.Body.String())
	}

	// R-6 closes the loop: a new project copies the freshly published
	// organization template.
	copyResponse := governanceHTTPRequest(router2, ctx, http.MethodPost, "/api/v1/lingdoc/projects", `{"name":"R-6 注册夹具项目","template_id":"org-template","template_version":"1"}`, "http-copy-org-template")
	if copyResponse.Code != http.StatusCreated {
		t.Fatalf("project copy of the registered-fixture publication = %d %s", copyResponse.Code, copyResponse.Body.String())
	}
}

func TestTemplateVersionReplayFixturePermissionMatrixHTTP(t *testing.T) {
	runtime, db, owner, _, ctx := governanceAdapterFixture(t)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	draft := seedOrganizationDraftFixture(t, runtime, owner, ctx)
	decodeGovernanceResponse(t, registerFixturesHTTP(t, router, ctx, draft, "http-register-perm", demoFixtureInputs("org-")), http.StatusCreated, nil)

	machine := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 12, FullAccess: true})
	machineRegister := registerFixturesHTTP(t, router, machine, draft, "http-register-machine", demoFixtureInputs("org-m-"))
	if machineRegister.Code != http.StatusForbidden || !strings.Contains(machineRegister.Body.String(), "human_decision_required") {
		t.Fatalf("machine registration = %d %s", machineRegister.Code, machineRegister.Body.String())
	}
	coveragePath := "/api/v1/lingdoc/template-catalog/profiles/" + draft.TemplateID + "/versions/" + draft.Version + "/replay-coverage"
	machineCoverage := governanceHTTPRequest(router, machine, http.MethodGet, coveragePath, "", "")
	if machineCoverage.Code != http.StatusForbidden || !strings.Contains(machineCoverage.Body.String(), "human_decision_required") {
		t.Fatalf("machine coverage read = %d %s", machineCoverage.Code, machineCoverage.Body.String())
	}

	reader := core.Actor{TenantID: owner.TenantID, UserID: "catalog-viewer", Role: types.TenantRoleViewer}
	if err := db.Exec("INSERT INTO tenant_members VALUES(7,'catalog-viewer','viewer','active',NULL)").Error; err != nil {
		t.Fatal(err)
	}
	readerCtx := governanceCallerContext(context.Background(), reader)
	readerRegister := registerFixturesHTTP(t, router, readerCtx, draft, "http-register-viewer", demoFixtureInputs("org-v-"))
	if readerRegister.Code != http.StatusNotFound {
		t.Fatalf("viewer registration = %d %s, want anti-enumerating 404", readerRegister.Code, readerRegister.Body.String())
	}
	readerCoverage := governanceHTTPRequest(router, readerCtx, http.MethodGet, coveragePath, "", "")
	if readerCoverage.Code != http.StatusNotFound {
		t.Fatalf("viewer coverage read = %d %s, want anti-enumerating 404", readerCoverage.Code, readerCoverage.Body.String())
	}
	var receipts []struct{ Details string }
	if err := db.Table("audit_logs").Select("details").Where("action=? AND actor_user_id=? AND outcome=?", "lingdoc.template.register_fixture", reader.UserID, "failure").Find(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || !strings.Contains(receipts[0].Details, `"error_code":"permission_denied"`) {
		t.Fatalf("viewer denial receipts = %+v", receipts)
	}

	if err := db.Exec("INSERT INTO tenants(id) VALUES(8)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tenant_members VALUES(8,'other-admin','admin','active',NULL)").Error; err != nil {
		t.Fatal(err)
	}
	other := core.Actor{TenantID: 8, UserID: "other-admin", Role: types.TenantRoleAdmin}
	otherCtx := governanceCallerContext(context.Background(), other)
	otherRegister := registerFixturesHTTP(t, router, otherCtx, draft, "http-register-other", demoFixtureInputs("org-x-"))
	if otherRegister.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant registration = %d %s", otherRegister.Code, otherRegister.Body.String())
	}
	if otherCoverage := governanceHTTPRequest(router, otherCtx, http.MethodGet, coveragePath, "", ""); otherCoverage.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant coverage read = %d %s", otherCoverage.Code, otherCoverage.Body.String())
	}
	var rows int64
	if err := db.Table("lingdoc_template_replay_fixtures").Where("tenant_id=7").Count(&rows).Error; err != nil || rows != 20 {
		t.Fatalf("tenant rows = %d, %v", rows, err)
	}

	// The fixture listing stays readable for humans and machines, metadata
	// only, and every tenant sees the builtin set plus exactly its own rows.
	viewerCatalog := governanceHTTPRequest(router, readerCtx, http.MethodGet, "/api/v1/lingdoc/template-catalog/replay-fixtures", "", "")
	var viewerData struct {
		Data []ReplayFixtureSummary `json:"data"`
	}
	if err := json.Unmarshal(viewerCatalog.Body.Bytes(), &viewerData); err != nil || viewerCatalog.Code != http.StatusOK || len(viewerData.Data) != 40 {
		t.Fatalf("viewer merged catalog = %d %s %v", len(viewerData.Data), viewerCatalog.Body.String(), err)
	}
	otherCatalog := governanceHTTPRequest(router, otherCtx, http.MethodGet, "/api/v1/lingdoc/template-catalog/replay-fixtures", "", "")
	var otherData struct {
		Data []ReplayFixtureSummary `json:"data"`
	}
	if err := json.Unmarshal(otherCatalog.Body.Bytes(), &otherData); err != nil || otherCatalog.Code != http.StatusOK || len(otherData.Data) != 20 {
		t.Fatalf("cross-tenant catalog must see builtin only = %d %s %v", len(otherData.Data), otherCatalog.Body.String(), err)
	}
	for _, summary := range otherData.Data {
		if strings.HasPrefix(summary.FixtureID, "org-") {
			t.Fatalf("cross-tenant registered row leaked: %s", summary.FixtureID)
		}
	}
}

// seedOrganizationDraftFixture prepares the demo-chain draft (pin shared with
// the builtin set) used by the permission and stale-pin tests.
func seedOrganizationDraftFixture(t *testing.T, runtime *TemplateGovernanceRuntime, owner core.Actor, ctx context.Context) governance.TemplateVersion {
	t.Helper()
	set, _ := seedDemoRuleSetFixture(t, runtime, owner, ctx)
	if _, err := runtime.Service().CreateProfile(ctx, governanceActor(owner), "create-org-profile", governance.CreateProfileInput{ID: template.DemoTemplateID, Name: "组织目录", Reason: "fixture registration acceptance"}); err != nil {
		t.Fatal(err)
	}
	return saveDemoTemplateDraftFixture(t, runtime, owner, set, ctx)
}

// A re-saved draft carries a new content hash; the old pin stops answering.
// Publishing with the old fixtures fails 422, the failure receipt classifies
// the stale pin, and the old rows survive append-only.
func TestTemplateVersionReplayFixtureOldPinDeactivatesHTTP(t *testing.T) {
	runtime, db, owner, _, ctx := governanceAdapterFixture(t)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	draft := seedOrganizationDraftFixture(t, runtime, owner, ctx)
	decodeGovernanceResponse(t, registerFixturesHTTP(t, router, ctx, draft, "http-register-oldpin", demoFixtureInputs("org-")), http.StatusCreated, nil)

	def := template.Clone(draft.Definition)
	def.Rules = nil
	def.ContentHash = ""
	def.RulesetHash = ""
	def.Fields[0].Label = "重存后的字段名"
	resaved, err := runtime.Service().SaveDraft(ctx, governanceActor(owner), "resave-oldpin-draft", governance.SaveDraftInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, Definition: def, RuleSet: draft.RuleSet, SourceType: "lingdoc_builtin", Scope: "internal-demo", Curator: owner.UserID, Reason: "content changed"})
	if err != nil {
		t.Fatal(err)
	}
	if resaved.ContentHash == draft.ContentHash {
		t.Fatal("re-saved draft kept the old content hash")
	}

	oldIDs := make([]string, 0, 20)
	for _, input := range demoFixtureInputs("org-") {
		oldIDs = append(oldIDs, input.FixtureID)
	}
	publishBody, err := json.Marshal(governance.PublishInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: resaved.Revision, ReplayFixtureIDs: oldIDs, Reason: "stale pin publish attempt"})
	if err != nil {
		t.Fatal(err)
	}
	publishResponse := governanceHTTPRequest(router, ctx, http.MethodPost, "/api/v1/lingdoc/template-catalog/profiles/"+draft.TemplateID+"/versions/"+draft.Version+"/publish", string(publishBody), "http-publish-oldpin")
	if publishResponse.Code != http.StatusUnprocessableEntity || !strings.Contains(publishResponse.Body.String(), "template_replay_required") {
		t.Fatalf("stale pin publish = %d %s", publishResponse.Code, publishResponse.Body.String())
	}
	if !strings.Contains(publishResponse.Body.String(), "重新登记") {
		t.Fatalf("stale pin receipt must name re-registration: %s", publishResponse.Body.String())
	}
	var receipts []struct{ Details string }
	if err := db.Table("audit_logs").Select("details").Where("action=? AND outcome=?", "lingdoc.template.publish", "failure").Find(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if len(receipts) == 0 || !strings.Contains(receipts[0].Details, `"error_code":"replay_required"`) {
		t.Fatalf("publish failure receipts = %+v", receipts)
	}
	var rows int64
	if err := db.Table("lingdoc_template_replay_fixtures").Count(&rows).Error; err != nil || rows != 20 {
		t.Fatalf("old rows must survive append-only: %d %v", rows, err)
	}
	// The coverage readback of the current pin sees the stale rows no more.
	coveragePath := "/api/v1/lingdoc/template-catalog/profiles/" + draft.TemplateID + "/versions/" + draft.Version + "/replay-coverage"
	var coverage governance.ReplayCoverageView
	decodeGovernanceResponse(t, governanceHTTPRequest(router, ctx, http.MethodGet, coveragePath, "", ""), http.StatusOK, &coverage)
	if coverage.Complete || coverage.ContentHash != resaved.ContentHash {
		t.Fatalf("current-pin coverage = complete:%v content:%s", coverage.Complete, coverage.ContentHash)
	}
}

// Spec D6 boundary pins: publish input decidable without the evaluator — an
// empty fixture selection and a duplicated fixture id — must answer the 400
// invalid_request family, never the 422 replay family, and leave zero
// successful writes behind: no success receipt in the audit ledger, the two
// denial receipts classify invalid_request, and the version stays draft at its
// original revision.
func TestTemplateVersionPublishInvalidFixtureSelectionIs400HTTP(t *testing.T) {
	runtime, db, owner, _, ctx := governanceAdapterFixture(t)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	draft := seedOrganizationDraftFixture(t, runtime, owner, ctx)
	inputs := demoFixtureInputs("org-d6-")
	registeredIDs := make([]string, 0, len(inputs))
	for _, input := range inputs {
		registeredIDs = append(registeredIDs, input.FixtureID)
	}
	decodeGovernanceResponse(t, registerFixturesHTTP(t, router, ctx, draft, "http-register-d6", inputs), http.StatusCreated, nil)

	publishPath := "/api/v1/lingdoc/template-catalog/profiles/" + draft.TemplateID + "/versions/" + draft.Version + "/publish"
	publish := func(ids []string, key string) *httptest.ResponseRecorder {
		body, err := json.Marshal(governance.PublishInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, ReplayFixtureIDs: ids, Reason: "D6 boundary pin: shape rejection without evaluation"})
		if err != nil {
			t.Fatal(err)
		}
		return governanceHTTPRequest(router, ctx, http.MethodPost, publishPath, string(body), key)
	}
	for _, attempt := range []struct {
		name string
		ids  []string
		key  string
	}{
		{name: "empty replay_fixture_ids", ids: []string{}, key: "http-publish-empty-ids"},
		{name: "duplicate fixture id", ids: []string{registeredIDs[0], registeredIDs[0]}, key: "http-publish-duplicate-id"},
	} {
		response := publish(attempt.ids, attempt.key)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_request") {
			t.Fatalf("%s = %d %s, want 400 invalid_request", attempt.name, response.Code, response.Body.String())
		}
	}

	var successes int64
	if err := db.Table("audit_logs").Where("action=? AND outcome=?", "lingdoc.template.publish", "success").Count(&successes).Error; err != nil || successes != 0 {
		t.Fatalf("rejected publishes must not write success receipts: %d %v", successes, err)
	}
	var failures []struct{ Details string }
	if err := db.Table("audit_logs").Select("details").Where("action=? AND outcome=?", "lingdoc.template.publish", "failure").Find(&failures).Error; err != nil {
		t.Fatal(err)
	}
	if len(failures) != 2 {
		t.Fatalf("failure receipts = %d, want one denial receipt per rejected publish", len(failures))
	}
	for _, receipt := range failures {
		if !strings.Contains(receipt.Details, `"error_code":"invalid_request"`) {
			t.Fatalf("D6 denial receipts must classify invalid_request: %+v", failures)
		}
	}
	versionPath := "/api/v1/lingdoc/template-catalog/profiles/" + draft.TemplateID + "/versions/" + draft.Version
	var version governance.TemplateVersion
	decodeGovernanceResponse(t, governanceHTTPRequest(router, ctx, http.MethodGet, versionPath, "", ""), http.StatusOK, &version)
	if version.State != template.RuleSetDraft || version.Revision != draft.Revision {
		t.Fatalf("rejected publishes must not advance the version: state=%s revision=%d", version.State, version.Revision)
	}
}

func TestTemplateVersionReplayFixturePayloadBoundedHTTP(t *testing.T) {
	runtime, db, owner, _, ctx := governanceAdapterFixture(t)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	draft := seedOrganizationDraftFixture(t, runtime, owner, ctx)

	oversized := demoFixtureInputs("org-over-")
	for len(oversized) <= governance.MaxReplayFixturesPerBatch {
		oversized = append(oversized, oversized[0])
	}
	oversizedResponse := registerFixturesHTTP(t, router, ctx, draft, "http-register-oversized", oversized)
	if oversizedResponse.Code != http.StatusBadRequest || !strings.Contains(oversizedResponse.Body.String(), "invalid_request") {
		t.Fatalf("oversized batch = %d %s", oversizedResponse.Code, oversizedResponse.Body.String())
	}

	huge := demoFixtureInputs("org-huge-")
	huge[0].Input = template.EvaluationInput{ProjectID: "bloat", TargetVersion: "1", Fields: map[string]string{"payload": strings.Repeat("a", 2<<20)}}
	hugeResponse := registerFixturesHTTP(t, router, ctx, draft, "http-register-huge", huge)
	if hugeResponse.Code != http.StatusBadRequest {
		t.Fatalf("oversized body = %d %s", hugeResponse.Code, hugeResponse.Body.String())
	}
	var rows int64
	if err := db.Table("lingdoc_template_replay_fixtures").Count(&rows).Error; err != nil || rows != 0 {
		t.Fatalf("bounded rejections wrote rows: %d %v", rows, err)
	}
}

// fail-closed (D7): with fixture storage unavailable, Verify must answer the
// explicit storage failure — never a silent builtin-only replay verdict and
// never a missing-fixture 422 that would read as a registration problem.
func TestTemplateGovernanceVerifyFailsClosedWhenFixtureStorageUnavailable(t *testing.T) {
	runtime, db, owner, _, ctx := governanceAdapterFixture(t)
	draft := seedOrganizationDraftFixture(t, runtime, owner, ctx)
	receipt, err := runtime.Service().RegisterFixtures(ctx, governanceActor(owner), "failclosed-register", governance.RegisterFixturesInput{TemplateID: draft.TemplateID, Version: draft.Version, Fixtures: demoFixtureInputs("org-"), Reason: "fail-closed acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(receipt.Registered))
	for _, summary := range receipt.Registered {
		ids = append(ids, summary.FixtureID)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	err = runtime.Verify(ctx, governanceActor(owner), draft, ids)
	if !errors.Is(err, governance.ErrUnavailable) {
		t.Fatalf("verify with storage down = %v, want fail-closed ErrUnavailable", err)
	}
	if errors.Is(err, governance.ErrReplayRequired) {
		t.Fatal("storage failure must not read as a replay requirement")
	}
}
