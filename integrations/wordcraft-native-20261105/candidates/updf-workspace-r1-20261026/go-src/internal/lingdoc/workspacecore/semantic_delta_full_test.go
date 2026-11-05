package workspacecore

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"strconv"
	"strings"
	"testing"
	"time"
)

func semanticDraft(t *testing.T, s *Service, a Actor, p Project, key string, deltas ...SemanticDelta) ChangeSet {
	t.Helper()
	raw, _, _, err := s.CreateChangeDraft(context.Background(), a, p.ID, key, ExtendedChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "明确人工提交的合成语义变更", Fields: map[string]ChangeFieldInput{}, SemanticDeltas: deltas})
	return g2Decode[ChangeSet](t, raw, err)
}
func semanticApply(t *testing.T, s *Service, a Actor, p Project, c ChangeSet, key string) ChangeSet {
	t.Helper()
	if _, _, _, err := s.AssessChangeSet(context.Background(), a, p.ID, c.ID, key+"-assess"); err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := s.ApplyChangeSet(context.Background(), a, p.ID, c.ID, key+"-apply")
	return g2Decode[ChangeSet](t, raw, err)
}
func TestSemanticClaimUsesVersionsConsentAndInverse(t *testing.T) {
	f := newG2Fixture(t, false)
	claim := f.claim(t)
	delta := SemanticDelta{Kind: "ResearchClaim", TargetID: claim.ID, ExpectedVersion: claim.Version, BeforeHash: "forged", ProposedClaim: &SaveResearchClaimInput{Text: "用户修改的合成主张", Scope: "新的明确范围", Status: "active", Confirmed: false}}
	if _, _, _, err := f.svc.CreateChangeDraft(context.Background(), f.owner, f.project.ID, "claim-no-consent", ExtendedChangeSetInput{ExpectedContextRevision: f.project.CurrentContextRevision, Reason: "用户尚未确认", SemanticDeltas: []SemanticDelta{delta}}); !errors.Is(err, ErrG2ConsentRequired) {
		t.Fatalf("implicit claim consent: %v", err)
	}
	delta.ProposedClaim.Confirmed = true
	c := semanticDraft(t, f.svc, f.owner, f.project, "claim-semantic-draft", delta)
	if len(c.SemanticDeltas) != 1 || c.SemanticDeltas[0].BeforeHash == "forged" {
		t.Fatal("caller before hash trusted or semantics missing")
	}
	var before ResearchClaim
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var e error
		before, _, e = g2Load[ResearchClaim](tx, f.project.ID, G2ClaimKind, claim.ID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if before.Version != 1 || before.Text != claim.Text {
		t.Fatal("draft changed formal claim")
	}
	semanticApply(t, f.svc, f.owner, f.project, c, "claim-semantic")
	p, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.SpecRevision != f.project.SpecRevision || p.CurrentContextRevision != f.project.CurrentContextRevision+1 {
		t.Fatal("localized claim edit rewrote all chapter spec bases")
	}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		now, _, e := g2Load[ResearchClaim](tx, p.ID, G2ClaimKind, claim.ID)
		if e != nil {
			return e
		}
		if now.Version != 2 || now.Text != delta.ProposedClaim.Text || now.Basis.ContextRevision != p.CurrentContextRevision {
			t.Fatal("formal claim version did not advance")
		}
		state, e := tx.(currentEvidenceObjectTransaction).CurrentEvidenceObject(p.ID, "ResearchClaim", claim.ID)
		if e != nil {
			return e
		}
		if state.Confirmed {
			t.Fatal("Owner edit became scientific confirmation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := f.svc.ReverseChangeSet(context.Background(), f.owner, p.ID, c.ID, "claim-inverse-draft")
	inverse := g2Decode[ChangeSet](t, raw, err)
	if inverse.Status != "draft" || inverse.SemanticDeltas[0].ExpectedVersion != 2 {
		t.Fatal("inverse is not a current new proposal")
	}
	semanticApply(t, f.svc, f.owner, p, inverse, "claim-inverse")
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		now, _, e := g2Load[ResearchClaim](tx, p.ID, G2ClaimKind, claim.ID)
		if e == nil && (now.Version != 3 || now.Text != claim.Text) {
			t.Fatal("inverse rewound pointer instead of append")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
func TestSemanticApplyRollsBackEverythingTogether(t *testing.T) {
	f := newG2Fixture(t, false)
	claim := f.claim(t)
	c := semanticDraft(t, f.svc, f.owner, f.project, "atomic-semantic-draft", SemanticDelta{Kind: "ResearchClaim", TargetID: claim.ID, ExpectedVersion: claim.Version, ProposedClaim: &SaveResearchClaimInput{Text: "需要原子写入的主张", Scope: "合成范围", Status: "active", Confirmed: true}})
	if _, _, _, err := f.svc.AssessChangeSet(context.Background(), f.owner, f.project.ID, c.ID, "atomic-semantic-assess"); err != nil {
		t.Fatal(err)
	}
	before, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.testDB().Exec("CREATE TRIGGER semantic_task_fail BEFORE INSERT ON lingdoc_g3_records WHEN NEW.kind = 'task' BEGIN SELECT RAISE(ABORT, 'semantic task unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	_, _, _, err = f.svc.ApplyChangeSet(context.Background(), f.owner, f.project.ID, c.ID, "atomic-semantic-apply")
	if err == nil || !strings.Contains(err.Error(), "semantic task unavailable") {
		t.Fatalf("did not reach intended failure: %v", err)
	}
	after, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ProjectVersion != before.ProjectVersion || after.CurrentContextRevision != before.CurrentContextRevision {
		t.Fatal("partial formal project")
	}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		now, _, e := g2Load[ResearchClaim](tx, f.project.ID, G2ClaimKind, claim.ID)
		if e == nil && now.Version != claim.Version {
			t.Fatal("partial formal claim")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	var audits int64
	if err := f.svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", f.project.ID, "semantic.apply").Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 0 {
		t.Fatal("partial semantic audit")
	}
}
func TestAssetScopeRechecksSourcePolicy(t *testing.T) {
	f := newG2Fixture(t, false)
	g2SeedNativeEvidence(t, f)
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return g2Store(tx, G2StoredObject{}, f.project.ID, G2AssetPolicyKind, "asset-1", 1, ProjectAssetPolicy{ID: "asset-1", ProjectID: f.project.ID, AssetID: "asset-1", Version: 1, AssetRevision: 1, PolicyVersion: 1, Scope: "project", Purpose: "原始人工用途", CreatedBy: f.owner.UserID})
	}); err != nil {
		t.Fatal(err)
	}
	c := semanticDraft(t, f.svc, f.owner, f.project, "asset-scope-draft", SemanticDelta{Kind: "ProjectAssetScope", TargetID: "asset-1", ExpectedVersion: 1, ProposedAssetPolicy: &ProjectAssetPolicy{Scope: "personal", Purpose: "仅本人复核", ModelUseAllowed: false}})
	if _, _, _, err := f.svc.AssessChangeSet(context.Background(), f.owner, f.project.ID, c.ID, "asset-scope-assess"); err != nil {
		t.Fatal(err)
	}
	f.policy.verdict = ErrSourceUnavailable
	if _, _, _, err := f.svc.ApplyChangeSet(context.Background(), f.owner, f.project.ID, c.ID, "asset-scope-denied"); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("revoked source applied: %v", err)
	}
	f.policy.verdict = nil
	if _, _, _, err := f.svc.ApplyChangeSet(context.Background(), f.owner, f.project.ID, c.ID, "asset-scope-apply"); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		policy, _, _, e := ReadProjectAssetPolicy(tx, p, "asset-1")
		if e == nil && (policy.Version != 2 || policy.AssetRevision != 2 || policy.Scope != "personal") {
			t.Fatal("scope update did not advance native revision")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := f.svc.ReverseChangeSet(context.Background(), f.owner, p.ID, c.ID, "asset-scope-inverse")
	inverse := g2Decode[ChangeSet](t, raw, err)
	semanticApply(t, f.svc, f.owner, p, inverse, "asset-inverse")
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		policy, _, _, e := ReadProjectAssetPolicy(tx, p, "asset-1")
		if e == nil && (policy.Version != 3 || policy.Scope != "project") {
			t.Fatal("asset inverse rewound pointer")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
func TestRouteUsesCurrentDependenciesAndRequiredGate(t *testing.T) {
	f := newG2Fixture(t, false)
	claim := f.claim(t)
	c := semanticDraft(t, f.svc, f.owner, f.project, "route-draft", SemanticDelta{Kind: "RouteNode", TargetID: "analysis-route", ExpectedVersion: 0, ProposedRoute: &RouteNode{Name: "合成技术路线", Required: true, Status: "ready", Inputs: []ObjectVersionRef{{Kind: "ResearchClaim", ID: claim.ID, Version: "1"}}}})
	semanticApply(t, f.svc, f.owner, f.project, c, "route-change")
	p, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		blockers, e := RouteStabilizationBlockers(tx, p)
		if e == nil && len(blockers) != 0 {
			t.Fatal("human route readiness asserted science confirmation or missed current inputs")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	// Changing the real referenced claim makes the required route invalid.
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		old, row, e := g2Load[ResearchClaim](tx, p.ID, G2ClaimKind, claim.ID)
		if e != nil {
			return e
		}
		old.Version++
		return g2Store(tx, row, p.ID, G2ClaimKind, old.ID, old.Version, old)
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		blockers, e := RouteStabilizationBlockers(tx, p)
		if e == nil && len(blockers) == 0 {
			t.Fatal("stale route inputs passed required gate")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
func TestRecomputeRejectsOldUnknownAndStaleReceipt(t *testing.T) {
	s, a, p := extendedImpactStore(t)
	task := ImpactTask{ID: "recompute-task", ProjectID: p.ID, Target: ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: strconv.FormatInt(p.SpecRevision, 10)}, TargetRevision: p.CurrentContextRevision, Action: "RECOMPUTE"}
	if err := s.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		if err := verifyImpactActionResult(tx, p, task, []ObjectVersionRef{task.Target}); !errors.Is(err, ErrInvalidState) {
			t.Fatal("old ref with reason is a recompute result")
		}
		basis, err := ImpactComputationBasisHash(tx, p)
		if err != nil {
			return err
		}
		sourceHash, e := tx.(impactComputationSourceTransaction).ImpactSourceDigest(p.ID, nil)
		if e != nil {
			return e
		}
		receipt := ImpactComputationReceipt{SourceBasisHash: sourceHash, ID: "actual-check", Version: 1, ProjectID: p.ID, Status: "blocked", ProjectVersion: p.ProjectVersion, ContextRevision: p.CurrentContextRevision, TemplateCopyVersion: p.TemplateCopyVersion, RulesetHash: p.TemplateCopy.RulesetHash, InputHash: "synthetic-evaluator-input", ComputationBasisHash: basis, CheckedAt: time.Now().UTC(), SourceIDs: []string{}, Evaluations: []template.Evaluation{{RuleID: "required-fields", TargetRef: p.ID, TargetVersion: "1", RulesetHash: p.TemplateCopy.RulesetHash, EvaluatorVersion: template.EvaluatorVersion, Status: template.EvaluationUnknown}}}
		store := tx.(runtimeTransaction)
		if err := store.G6Insert(a.TenantID, p.ID, "preflight_check", receipt.ID, 1, receipt); err != nil {
			return err
		}
		ref := ObjectVersionRef{Kind: "ComputationReceipt", ID: receipt.ID, Version: "1"}
		if err := verifyImpactActionResult(tx, p, task, []ObjectVersionRef{ref}); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("unknown was a completed recompute: %v", err)
		}
		receipt.Evaluations[0].Status = template.EvaluationIssue
		receipt.Version = 2
		if err := store.G6CAS(a.TenantID, p.ID, "preflight_check", receipt.ID, 1, receipt); err != nil {
			return err
		}
		ref.Version = "2"
		if err := verifyImpactActionResult(tx, p, task, []ObjectVersionRef{ref}); err != nil {
			return err
		}
		stale := p
		stale.Spec = map[string]string{"changed": "different"}
		if err := verifyImpactActionResult(tx, stale, task, []ObjectVersionRef{ref}); !errors.Is(err, ErrVersionConflict) {
			t.Fatal("changed current basis accepted old receipt")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type semanticCatalogFixture struct{ value CatalogTemplateDefinition }

func (f semanticCatalogFixture) Resolve(context.Context, Transaction, Actor, string, string) (CatalogTemplateDefinition, error) {
	return f.value, nil
}
func TestBindingDerivesImmutableCopyAndInverse(t *testing.T) {
	s, a, p := extendedImpactStore(t)
	set, err := template.PublishRuleSet(template.RuleSet{ID: "semantic-rules", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{
		{RuleID: "mandatory-presence", DefinitionID: "presence", DefinitionVersion: "1", Origin: "builtin", TargetKind: "field", TargetID: "research_goal", Severity: "blocking", TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: "synthetic-template-owner"},
		{RuleID: "optional-pattern", DefinitionID: "pattern", DefinitionVersion: "1", Origin: "project", TargetKind: "field", TargetID: "research_goal", Parameters: map[string]any{"pattern": "."}, Severity: "warning", TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: "synthetic-template-owner"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	definition := template.Clone(p.TemplateCopy.Definition)
	definition.Rules, err = template.CompileRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	definition, err = template.WithHashes(definition)
	if err != nil {
		t.Fatal(err)
	}
	policies := []governance.BindingPolicy{{RuleID: "optional-pattern", MayDisable: true, AllowedParameterKeys: []string{"pattern"}, AllowedSeverities: []string{"warning", "info"}}}
	policyHash, err := governance.HashBindingPolicies(policies)
	if err != nil {
		t.Fatal(err)
	}
	value := CatalogTemplateDefinition{Definition: definition, RuleSetSnapshot: &set, SourceRuleSetID: set.ID, SourceRuleSetVersion: set.Version, SourceRuleSetHash: set.RulesetHash, BindingPolicies: policies, SourcePolicyHash: policyHash}
	s.SetTemplateCatalogResolver(semanticCatalogFixture{value})
	if err := s.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		next := p
		copy, e := makeCatalogTemplateCopy(a, p.ID, p.TemplateCopyVersion+1, value, TemplateCopyBound)
		if e != nil {
			return e
		}
		next.TemplateCopy = copy
		next.TemplateCopyVersion = copy.Version
		next.ProjectVersion++
		return tx.UpdateProject(a.TenantID, p, next)
	}); err != nil {
		t.Fatal(err)
	}
	p, err = s.GetProject(context.Background(), a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, _, _, err := s.CreateChangeDraft(context.Background(), a, p.ID, "mandatory-disable", ExtendedChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "非法降级反例", SemanticDeltas: []SemanticDelta{{Kind: "RuleBinding", TargetID: "mandatory-presence", ExpectedVersion: p.TemplateCopyVersion, ProposedBinding: &governance.BindingOverride{RuleID: "mandatory-presence", Enabled: &disabled}}}}); err == nil {
		t.Fatal("mandatory binding disabled")
	}
	c := semanticDraft(t, s, a, p, "optional-binding-draft", SemanticDelta{Kind: "RuleBinding", TargetID: "optional-pattern", ExpectedVersion: p.TemplateCopyVersion, ProposedBinding: &governance.BindingOverride{RuleID: "optional-pattern", Severity: "info"}})
	semanticApply(t, s, a, p, c, "optional-binding")
	current, err := s.GetProject(context.Background(), a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.TemplateCopyVersion != p.TemplateCopyVersion+1 || current.TemplateCopy.SourcePolicyHash != policyHash || current.TemplateCopy.RuleSetSnapshot.Bindings[1].Severity != "info" {
		t.Fatal("binding did not persist effective immutable copy and reviewed policy")
	}
	if current.TemplateCopy.SourceRuleSetHash != set.RulesetHash {
		t.Fatal("project override changed catalog source rules")
	}
	raw, _, _, err := s.ReverseChangeSet(context.Background(), a, p.ID, c.ID, "binding-inverse-draft")
	inverse := g2Decode[ChangeSet](t, raw, err)
	semanticApply(t, s, a, current, inverse, "binding-inverse")
	restored, err := s.GetProject(context.Background(), a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.TemplateCopyVersion != current.TemplateCopyVersion+1 || restored.TemplateCopy.RuleSetSnapshot.Bindings[1].Severity != "warning" {
		t.Fatal("binding inverse did not append reviewed source severity")
	}
}
func TestRetrieveRequiresApprovedRunScope(t *testing.T) {
	f := newG2Fixture(t, false)
	g2SeedNativeEvidence(t, f)
	task := ImpactTask{ID: "retrieve-task", ProjectID: f.project.ID, TargetRevision: f.project.CurrentContextRevision, Action: "RETRIEVE_CANDIDATES", Target: ObjectVersionRef{Kind: "ProjectSpec", ID: f.project.ID, Version: "1"}}
	raw, _, _, err := f.svc.SaveResearchTask(context.Background(), f.owner, f.project.ID, "impact-research-task", SaveResearchTaskInput{G2Expectation: f.expectation(0), Goal: "合成重检索", Scope: "impact:" + task.ID, Queries: []string{"定义", "限制"}, ProviderID: "synthetic-provider", AssetIDs: []string{"asset-1"}})
	research := g2Decode[ResearchTask](t, raw, err)
	raw, _, _, err = f.svc.ApproveResearchTask(context.Background(), f.owner, f.project.ID, research.ID, "impact-research-approve", G2ConfirmedInput{G2Expectation: f.expectation(research.Version), Confirmed: true, Reason: "真实用户确认合成范围"})
	research = g2Decode[ResearchTask](t, raw, err)
	raw, _, _, err = f.svc.RunResearchTask(context.Background(), f.owner, f.project.ID, research.ID, "impact-research-run", RunResearchTaskInput{G2Expectation: f.expectation(research.Version)})
	run := g2Decode[ResearchRun](t, raw, err)
	ref := ObjectVersionRef{Kind: "ResearchRun", ID: run.ID, Version: strconv.FormatInt(run.Version, 10)}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		if err := verifyImpactActionResult(tx, f.project, task, []ObjectVersionRef{task.Target}); !errors.Is(err, ErrInvalidState) {
			t.Fatal("old target ref is retrieval receipt")
		}
		if err := verifyImpactActionResult(tx, f.project, task, []ObjectVersionRef{ref}); err != nil {
			return err
		}
		unrelated := task
		unrelated.ID = "another-impact-task"
		if err := verifyImpactActionResult(tx, f.project, unrelated, []ObjectVersionRef{ref}); !errors.Is(err, ErrInvalidState) {
			t.Fatal("unrelated run closed another task")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.testDB().Exec("UPDATE lingdoc_project_assets SET asset_revision = 2 WHERE id = ?", "asset-1").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if err := verifyImpactActionResult(tx, f.project, task, []ObjectVersionRef{ref}); err == nil {
			t.Fatal("changed real asset reused old run")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
