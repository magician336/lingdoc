package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// firstOrganizationRuleSet is the bootstrap rule set of Spec #768 D3: two
// presence bindings on the demo template's research_subject and research_goal
// fields, both trigger scenes, no declared example refs (registered fixtures
// alone can satisfy publication).
func firstOrganizationRuleSet(ownerID string) template.RuleSet {
	return template.RuleSet{ID: "org-rules", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{
		{RuleID: "goal-present", DefinitionID: "presence", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal", Severity: template.SeverityWarning, TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: ownerID},
		{RuleID: "subject-present", DefinitionID: "presence", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_subject", Severity: template.SeverityWarning, TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: ownerID},
	}}
}

// projectedFixtureInputs covers every enabled binding × trigger scene ×
// polarity of the bootstrap rule set with inputs actually evaluated against
// the replay base projection.
func projectedFixtureInputs(prefix string) []governance.ReplayFixtureInput {
	inputs := []governance.ReplayFixtureInput{}
	for _, rule := range []struct{ id, field string }{{"goal-present", "research_goal"}, {"subject-present", "research_subject"}} {
		for _, scene := range []string{"write_check", "preflight"} {
			for _, polarity := range []string{"positive", "negative"} {
				fields := map[string]string{}
				status := template.EvaluationIssue
				if polarity == "positive" {
					fields[rule.field] = "R-6 自定义规则目标"
					status = template.EvaluationPass
				}
				inputs = append(inputs, governance.ReplayFixtureInput{
					FixtureID:       prefix + rule.id + "-" + scene + "-" + polarity,
					RuleID:          rule.id,
					Scene:           scene,
					Polarity:        polarity,
					TargetRef:       "field/" + rule.field,
					Input:           template.EvaluationInput{ProjectID: "ruleset-registration", TargetVersion: "1", Fields: fields},
					ExpectedStatus:  status,
					ReviewReference: "review:g4-771-ruleset-registration",
				})
			}
		}
	}
	return inputs
}

func registerRuleSetFixturesHTTP(t *testing.T, router *gin.Engine, ctx context.Context, rulesetID, version, baseTemplateID, baseVersion, key string, fixtures []governance.ReplayFixtureInput) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(governance.RegisterRuleSetFixturesInput{RuleSetID: rulesetID, RuleSetVersion: version, TemplateID: baseTemplateID, TemplateVersion: baseVersion, Fixtures: fixtures, Reason: "g4 ruleset registration acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/lingdoc/template-catalog/rulesets/" + rulesetID + "/versions/" + version + "/replay-fixtures"
	return governanceHTTPRequest(router, ctx, http.MethodPost, path, string(body), key)
}

func publishRuleSetHTTP(t *testing.T, router *gin.Engine, ctx context.Context, rulesetID, version, baseTemplateID, baseVersion, key string, fixtureIDs []string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(governance.RuleGovernanceInput{ExpectedState: template.RuleSetDraft, ExpectedRuleSetHash: "", ReplayFixtureIDs: fixtureIDs, TemplateID: baseTemplateID, TemplateVersion: baseVersion, Reason: "published on projected fixtures"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/lingdoc/template-catalog/rulesets/" + rulesetID + "/versions/" + version + "/publish"
	return governanceHTTPRequest(router, ctx, http.MethodPost, path, string(body), key)
}

// The bootstrap tracer bullet (Spec #768 D3): the first organization rule set
// goes draft -> fixture registration against the builtin demo replay base ->
// publish 200, entirely over HTTP. The registration receipt, the coverage
// readback (ruleset projection query parameters) and publication Verify answer
// the same shared coverage matrix, and the registration receipt is readable in
// the replay base's admin audit history with both references.
func TestRuleSetProjectionFixtureRegistrationBootstrapChainHTTP(t *testing.T) {
	runtime, _, _, owner, _, ctx := governanceAdapterFixtureWithProjects(t)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)

	draftBody, err := json.Marshal(governance.RuleDraftInput{Draft: firstOrganizationRuleSet(owner.UserID), Reason: "first organization rule set"})
	if err != nil {
		t.Fatal(err)
	}
	draftResponse := governanceHTTPRequest(router, ctx, http.MethodPost, "/api/v1/lingdoc/template-catalog/rulesets/org-rules/versions", string(draftBody), "http-create-org-rules")
	if draftResponse.Code != http.StatusCreated {
		t.Fatalf("org ruleset draft = %d %s", draftResponse.Code, draftResponse.Body.String())
	}

	registered := registerRuleSetFixturesHTTP(t, router, ctx, "org-rules", "1", template.DemoTemplateID, template.DemoTemplateVersion, "http-register-org-rules", projectedFixtureInputs("org-rules-reg-"))
	var receipt governance.RegisterFixturesReceipt
	if replayed := decodeGovernanceResponse(t, registered, http.StatusCreated, &receipt); replayed {
		t.Fatal("first ruleset registration reported replayed")
	}
	if receipt.TemplateID != template.DemoTemplateID || receipt.Version != template.DemoTemplateVersion {
		t.Fatalf("receipt must name the replay base version: %+v", receipt)
	}
	if receipt.Projection == nil || receipt.Projection.RuleSet.ID != "org-rules" || receipt.Projection.RuleSet.Version != "1" || receipt.Projection.RuleSet.Hash == "" {
		t.Fatalf("receipt must carry the projected ruleset reference: %+v", receipt.Projection)
	}
	if receipt.Projection.ReplayBase.TemplateID != template.DemoTemplateID || receipt.Projection.ReplayBase.Version != template.DemoTemplateVersion {
		t.Fatalf("receipt must carry the replay base reference: %+v", receipt.Projection)
	}
	if len(receipt.Registered) != 8 || !receipt.Coverage.Complete {
		t.Fatalf("receipt = %d registered, coverage complete=%v", len(receipt.Registered), receipt.Coverage.Complete)
	}
	registeredIDs := make([]string, 0, 8)
	for _, summary := range receipt.Registered {
		if summary.RuleSet.ID != "org-rules" || summary.RuleSet.Hash != receipt.Projection.RuleSet.Hash {
			t.Fatalf("registered summary must pin the projected ruleset: %+v", summary)
		}
		registeredIDs = append(registeredIDs, summary.FixtureID)
	}

	// Idempotency two-state: same key same request replays the same receipt;
	// same key different request conflicts.
	if replayed := decodeGovernanceResponse(t, registerRuleSetFixturesHTTP(t, router, ctx, "org-rules", "1", template.DemoTemplateID, template.DemoTemplateVersion, "http-register-org-rules", projectedFixtureInputs("org-rules-reg-")), http.StatusCreated, nil); !replayed {
		t.Fatal("same-key ruleset replay did not report replayed")
	}
	conflicting := registerRuleSetFixturesHTTP(t, router, ctx, "org-rules", "1", template.DemoTemplateID, template.DemoTemplateVersion, "http-register-org-rules", projectedFixtureInputs("org-rules-other-"))
	if conflicting.Code != http.StatusConflict || !strings.Contains(conflicting.Body.String(), "idempotency_conflict") {
		t.Fatalf("same key different ruleset request = %d %s", conflicting.Code, conflicting.Body.String())
	}

	// The replay base becomes a readable catalog version so the coverage
	// readback and the admin audit history have their production seam.
	if _, err := runtime.Service().CreateProfile(ctx, governanceActor(owner), "create-demo-profile-771", governance.CreateProfileInput{ID: template.DemoTemplateID, Name: "内置审阅目录", Reason: "coverage readback seam"}); err != nil {
		t.Fatal(err)
	}
	demoSet, _ := seedDemoRuleSetFixture(t, runtime, owner, ctx)
	demoDraft := saveDemoTemplateDraftFixture(t, runtime, owner, demoSet, ctx)

	coverageBase := "/api/v1/lingdoc/template-catalog/profiles/" + template.DemoTemplateID + "/versions/" + template.DemoTemplateVersion + "/replay-coverage"
	var projected governance.ReplayCoverageView
	decodeGovernanceResponse(t, governanceHTTPRequest(router, ctx, http.MethodGet, coverageBase+"?ruleset_id=org-rules&ruleset_version=1", "", ""), http.StatusOK, &projected)
	if !projected.Complete || projected.ContentHash == "" || projected.ContentHash == demoDraft.ContentHash {
		t.Fatalf("projected coverage = complete:%v content:%s (demo draft %s)", projected.Complete, projected.ContentHash, demoDraft.ContentHash)
	}
	if projected.Projection == nil || projected.Projection.RuleSet.ID != "org-rules" || projected.Projection.ReplayBase.TemplateID != template.DemoTemplateID {
		t.Fatalf("projected coverage view must name the projection: %+v", projected.Projection)
	}
	// Three-way agreement (registration receipt == coverage endpoint).
	endpointCoverage, err := json.Marshal(projected.ReplayCoverageReport)
	if err != nil {
		t.Fatal(err)
	}
	receiptCoverage, err := json.Marshal(receipt.Coverage)
	if err != nil {
		t.Fatal(err)
	}
	if string(endpointCoverage) != string(receiptCoverage) {
		t.Fatalf("coverage answers diverge:\nendpoint %s\nreceipt %s", endpointCoverage, receiptCoverage)
	}
	// The demo base's own coverage (no query parameters) still answers from the
	// builtin set: the org projection rows must not bleed across pins.
	var demoCoverage governance.ReplayCoverageView
	decodeGovernanceResponse(t, governanceHTTPRequest(router, ctx, http.MethodGet, coverageBase, "", ""), http.StatusOK, &demoCoverage)
	if !demoCoverage.Complete || demoCoverage.ContentHash != demoDraft.ContentHash || demoCoverage.Projection != nil {
		t.Fatalf("demo draft coverage = complete:%v content:%s projection:%+v", demoCoverage.Complete, demoCoverage.ContentHash, demoCoverage.Projection)
	}

	// Publication Verify agrees: fewer than the full matrix is 422 with the
	// replay evidence, the complete set publishes 200.
	partial := publishRuleSetHTTP(t, router, ctx, "org-rules", "1", template.DemoTemplateID, template.DemoTemplateVersion, "http-publish-org-rules-partial", registeredIDs[:7])
	if partial.Code != http.StatusUnprocessableEntity || !strings.Contains(partial.Body.String(), "template_replay_required") {
		t.Fatalf("partial publish = %d %s", partial.Code, partial.Body.String())
	}
	published := publishRuleSetHTTP(t, router, ctx, "org-rules", "1", template.DemoTemplateID, template.DemoTemplateVersion, "http-publish-org-rules", registeredIDs)
	var publishedSet template.RuleSet
	if published.Code != http.StatusOK {
		t.Fatalf("ruleset publish on projected fixtures = %d %s", published.Code, published.Body.String())
	}
	decodeGovernanceResponse(t, published, http.StatusOK, &publishedSet)
	if publishedSet.State != template.RuleSetPublished || publishedSet.RulesetHash != receipt.Projection.RuleSet.Hash {
		t.Fatalf("published set = %+v, want the registered projection hash", publishedSet)
	}

	// The ruleset-path receipt is readable in the replay base's admin audit
	// history, with both the ruleset reference and the replay base reference.
	auditPath := "/api/v1/lingdoc/template-catalog/profiles/" + template.DemoTemplateID + "/audit-events"
	auditResponse := governanceHTTPRequest(router, ctx, http.MethodGet, auditPath, "", "")
	var history struct {
		Data struct {
			Events []governance.AuditEvent `json:"events"`
		} `json:"data"`
	}
	decodeGovernanceResponse(t, auditResponse, http.StatusOK, &history.Data)
	found := false
	for _, event := range history.Data.Events {
		if event.Action != "lingdoc.template.register_fixture" || event.Outcome != "success" {
			continue
		}
		raw, err := json.Marshal(event.Details)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "org-rules") && strings.Contains(string(raw), template.DemoTemplateID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("ruleset-path registration receipt missing from the base template's audit history: %s", auditResponse.Body.String())
	}
}

// The refusal matrix of the template registration endpoint holds equally on
// the ruleset spelling: machine principals 403 human_decision_required,
// non-managers 404 with a permission_denied failure receipt, cross-tenant
// admins 404, builtin fixture ids reserved, oversized batches 400, a trial
// that contradicts the real evaluation 422 with evidence, zero writes on every
// refusal.
func TestRuleSetFixtureRegistrationRefusalMatrixHTTP(t *testing.T) {
	runtime, db, owner, _, ctx := governanceAdapterFixture(t)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	svc := runtime.Service()
	if _, err := svc.CreateRuleSetDraft(ctx, governanceActor(owner), "create-org-rules-refusal", governance.RuleDraftInput{Draft: firstOrganizationRuleSet(owner.UserID), Reason: "refusal matrix acceptance"}); err != nil {
		t.Fatal(err)
	}

	machine := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 12, FullAccess: true})
	machineRegister := registerRuleSetFixturesHTTP(t, router, machine, "org-rules", "1", template.DemoTemplateID, template.DemoTemplateVersion, "http-register-machine", projectedFixtureInputs("org-m-"))
	if machineRegister.Code != http.StatusForbidden || !strings.Contains(machineRegister.Body.String(), "human_decision_required") {
		t.Fatalf("machine ruleset registration = %d %s", machineRegister.Code, machineRegister.Body.String())
	}

	reader := core.Actor{TenantID: owner.TenantID, UserID: "catalog-viewer", Role: types.TenantRoleViewer}
	if err := db.Exec("INSERT INTO tenant_members VALUES(7,'catalog-viewer','viewer','active',NULL)").Error; err != nil {
		t.Fatal(err)
	}
	readerCtx := governanceCallerContext(context.Background(), reader)
	readerRegister := registerRuleSetFixturesHTTP(t, router, readerCtx, "org-rules", "1", template.DemoTemplateID, template.DemoTemplateVersion, "http-register-viewer", projectedFixtureInputs("org-v-"))
	if readerRegister.Code != http.StatusNotFound {
		t.Fatalf("viewer ruleset registration = %d %s, want anti-enumerating 404", readerRegister.Code, readerRegister.Body.String())
	}
	if err := db.Exec("INSERT INTO tenants(id) VALUES(8)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tenant_members VALUES(8,'other-admin','admin','active',NULL)").Error; err != nil {
		t.Fatal(err)
	}
	other := core.Actor{TenantID: 8, UserID: "other-admin", Role: types.TenantRoleAdmin}
	otherCtx := governanceCallerContext(context.Background(), other)
	otherRegister := registerRuleSetFixturesHTTP(t, router, otherCtx, "org-rules", "1", template.DemoTemplateID, template.DemoTemplateVersion, "http-register-other", projectedFixtureInputs("org-x-"))
	if otherRegister.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant ruleset registration = %d %s", otherRegister.Code, otherRegister.Body.String())
	}
	var receipts []struct{ Details string }
	if err := db.Table("audit_logs").Select("details").Where("action=? AND actor_user_id=? AND outcome=?", "lingdoc.template.register_fixture", reader.UserID, "failure").Find(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || !strings.Contains(receipts[0].Details, `"error_code":"permission_denied"`) {
		t.Fatalf("viewer ruleset denial receipts = %+v", receipts)
	}

	// Builtin fixture ids are reserved: exact conflict is 400 with zero writes.
	builtin := BuiltinTemplateGovernanceFixtures()
	conflict := projectedFixtureInputs("org-c-")
	conflict[0] = governance.ReplayFixtureInput{FixtureID: builtin[0].Fixture.ID, RuleID: "goal-present", Scene: "write_check", Polarity: "positive", TargetRef: "field/research_goal", Input: conflict[0].Input, ExpectedStatus: template.EvaluationPass, ReviewReference: "review:conflict"}
	builtinConflict := registerRuleSetFixturesHTTP(t, router, ctx, "org-rules", "1", template.DemoTemplateID, template.DemoTemplateVersion, "http-register-builtin-conflict", conflict)
	if builtinConflict.Code != http.StatusBadRequest || !strings.Contains(builtinConflict.Body.String(), "invalid_request") {
		t.Fatalf("builtin id conflict = %d %s", builtinConflict.Code, builtinConflict.Body.String())
	}

	// A trial the real evaluation contradicts is the 422 replay evidence
	// family, not a shape error: the declared target is never exercised, which
	// only the evaluator can decide.
	wrongEvidence := projectedFixtureInputs("org-w-")
	wrongEvidence[0].TargetRef = "field/never_declared"
	wrongTrial := registerRuleSetFixturesHTTP(t, router, ctx, "org-rules", "1", template.DemoTemplateID, template.DemoTemplateVersion, "http-register-wrong-trial", wrongEvidence)
	if wrongTrial.Code != http.StatusUnprocessableEntity || !strings.Contains(wrongTrial.Body.String(), "template_replay_required") {
		t.Fatalf("wrong trial = %d %s", wrongTrial.Code, wrongTrial.Body.String())
	}

	oversized := projectedFixtureInputs("org-over-")
	for len(oversized) <= governance.MaxReplayFixturesPerBatch {
		oversized = append(oversized, oversized[0])
	}
	oversizedResponse := registerRuleSetFixturesHTTP(t, router, ctx, "org-rules", "1", template.DemoTemplateID, template.DemoTemplateVersion, "http-register-oversized", oversized)
	if oversizedResponse.Code != http.StatusBadRequest || !strings.Contains(oversizedResponse.Body.String(), "invalid_request") {
		t.Fatalf("oversized ruleset batch = %d %s", oversizedResponse.Code, oversizedResponse.Body.String())
	}

	var rows int64
	if err := db.Table("lingdoc_template_replay_fixtures").Count(&rows).Error; err != nil || rows != 0 {
		t.Fatalf("refusals wrote rows: %d %v", rows, err)
	}
}

// The second chain (Spec #768 D3): after the bootstrap, an organization draft
// works as a replay base. Its projection pin follows the base draft's content:
// a re-saved base (new content hash) deactivates the old pin, the publication
// answers 422 naming re-registration (the ErrStaleReplayPin shape), the old
// rows survive append-only, and re-registration on the current pin publishes.
func TestRuleSetProjectionOldPinDeactivatesHTTP(t *testing.T) {
	runtime, _, db, owner, _, ctx := governanceAdapterFixtureWithProjects(t)
	router := gin.New()
	RegisterTemplateGovernanceRoutes(router.Group("/api/v1/lingdoc"), runtime)
	base := seedOrganizationTemplateFixture(t, runtime, db, owner, ctx)

	// The second organization rule set version, registered against the
	// organization template draft as its replay base.
	second := firstOrganizationRuleSet(owner.UserID)
	second.Version = "2"
	if _, err := runtime.Service().CreateRuleSetDraft(ctx, governanceActor(owner), "create-org-rules-2", governance.RuleDraftInput{Draft: second, Reason: "second chain acceptance"}); err != nil {
		t.Fatal(err)
	}
	fixtures := projectedFixtureInputs("org-rules2-reg-")
	registered := registerRuleSetFixturesHTTP(t, router, ctx, "org-rules", "2", base.TemplateID, base.Version, "http-register-org-rules-2", fixtures)
	var receipt governance.RegisterFixturesReceipt
	decodeGovernanceResponse(t, registered, http.StatusCreated, &receipt)
	if receipt.Projection == nil || receipt.Projection.ReplayBase.TemplateID != base.TemplateID {
		t.Fatalf("second-chain receipt = %+v", receipt.Projection)
	}
	if receipt.ContentHash == "" || receipt.ContentHash == base.ContentHash {
		// The projection adds the v2 binding set to the base definition, so its
		// content digest differs from the base draft's own pin.
		t.Fatalf("projection kept the base's own content hash: %s", receipt.ContentHash)
	}
	ids := make([]string, 0, len(fixtures))
	for _, summary := range receipt.Registered {
		ids = append(ids, summary.FixtureID)
	}

	// The base draft is re-saved: new content hash, the old projection pin dies.
	def := template.Clone(base.Definition)
	def.Rules = nil
	def.ContentHash = ""
	def.RulesetHash = ""
	def.Fields[0].Label = "重存后的底版字段名"
	resaved, err := runtime.Service().SaveDraft(ctx, governanceActor(owner), "resave-org-base-771", governance.SaveDraftInput{TemplateID: base.TemplateID, Version: base.Version, ExpectedRevision: base.Revision, Definition: def, RuleSet: base.RuleSet, SourceType: "lingdoc_builtin", Scope: "organization", Curator: owner.UserID, Reason: "base content changed"})
	if err != nil {
		t.Fatal(err)
	}
	if resaved.ContentHash == base.ContentHash {
		t.Fatal("re-saved base kept the old content hash")
	}

	stale := publishRuleSetHTTP(t, router, ctx, "org-rules", "2", base.TemplateID, base.Version, "http-publish-org-rules-2-stale", ids)
	if stale.Code != http.StatusUnprocessableEntity || !strings.Contains(stale.Body.String(), "template_replay_required") {
		t.Fatalf("stale projection pin publish = %d %s", stale.Code, stale.Body.String())
	}
	if !strings.Contains(stale.Body.String(), "重新登记") {
		t.Fatalf("stale pin receipt must name re-registration: %s", stale.Body.String())
	}
	var rows int64
	if err := db.Table("lingdoc_template_replay_fixtures").Where("fixture_id LIKE 'org-rules2-reg-%'").Count(&rows).Error; err != nil || rows != 8 {
		t.Fatalf("old projection rows must survive append-only: %d %v", rows, err)
	}

	// Re-registration fixes the current projection pin; publication then passes.
	reregistered := registerRuleSetFixturesHTTP(t, router, ctx, "org-rules", "2", base.TemplateID, base.Version, "http-register-org-rules-2b", fixtures)
	var receipt2 governance.RegisterFixturesReceipt
	decodeGovernanceResponse(t, reregistered, http.StatusCreated, &receipt2)
	if receipt2.ContentHash == receipt.ContentHash {
		t.Fatalf("re-registration must follow the re-saved base to a new projection: still %s", receipt2.ContentHash)
	}
	if receipt2.ContentHash == resaved.ContentHash {
		t.Fatalf("projection must not equal the base's own pin: %s", receipt2.ContentHash)
	}
	published := publishRuleSetHTTP(t, router, ctx, "org-rules", "2", base.TemplateID, base.Version, "http-publish-org-rules-2", ids)
	if published.Code != http.StatusOK {
		t.Fatalf("publish after re-registration = %d %s", published.Code, published.Body.String())
	}
}
