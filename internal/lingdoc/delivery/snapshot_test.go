package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
)

type snapshotRuleSetUseAuditRecorder struct {
	events []lingdoctemplate.RuleSetUseAuditEvent
}

func (r *snapshotRuleSetUseAuditRecorder) RecordRuleSetUse(_ context.Context, event lingdoctemplate.RuleSetUseAuditEvent) error {
	r.events = append(r.events, event)
	return nil
}

func validDeliveryInput() DeliveryInput {
	template := DemoTemplate()
	version := "chapter-question-v1"
	confirmedAt := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	return DeliveryInput{
		ProjectID: "project-1", ProjectName: "内部演示", ProjectVersion: 3, SpecRevision: 2,
		Spec: map[string]string{"research_subject": "LingDoc", "research_goal": "验证"}, Template: template,
		Chapters: []SnapshotChapter{{ChapterID: "chapter-question", ChapterVersionID: &version, SectionID: "question", Title: "研究问题", BodyMarkdown: "结论 [[source:source-1]]", SourceIDs: []string{"source-1"}, ReviewItems: []ReviewItem{{ID: "review-1", Statement: "待核", OriginCandidateID: "candidate-1"}}, Confirmation: &Confirmation{ID: "confirmation-question-v1", ChapterID: "chapter-question", ChapterVersionID: version, SpecRevision: 2, AssetVersions: []AssetVersion{{AssetID: "asset-1", Revision: 4}}, TemplateVersion: template.Version, ActorUserID: "owner", CreatedAt: confirmedAt, Decisions: []ReviewDecision{{ReviewItemID: "review-1", Disposition: "resolved", Reason: "人工核验"}}}},
			{ChapterID: "chapter-method", ChapterVersionID: stringPtr("chapter-method-v1"), SectionID: "method", Title: "研究方案", BodyMarkdown: "方案正文", SourceIDs: []string{}, Confirmation: &Confirmation{ID: "confirmation-method-v1", ChapterID: "chapter-method", ChapterVersionID: "chapter-method-v1", SpecRevision: 2, AssetVersions: []AssetVersion{}, TemplateVersion: template.Version, ActorUserID: "owner", CreatedAt: confirmedAt}}},
		Sources:       []FrozenSource{{ID: "source-1", ProjectID: "project-1", AssetID: "asset-1", AssetRevision: 4, Locator: "第 1 段", QuotedText: "原文", QuotedTextHash: sha256Text("原文"), DisplayTitle: "演示资料"}},
		AssetVersions: []AssetVersion{{AssetID: "asset-1", Revision: 4}}, PolicyAssetIDs: []string{"asset-1"}, DeliveryKind: "internal_demo",
	}
}
func stringPtr(value string) *string { return &value }

func TestPrepareFreezesInputAndDigest(t *testing.T) {
	forEachSnapshotStore(t, testPrepareFreezesInputAndDigest)
}

func testPrepareFreezesInputAndDigest(t *testing.T, store snapshotStoreUnderTest) {
	service := NewReleaseService(store)
	service.now = func() time.Time { return time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC) }
	input := validDeliveryInput()
	snapshot, err := service.Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Check.Status != CheckPassed || snapshot.SnapshotDigest == "" || !snapshot.IsCurrent {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	input.Spec["research_goal"] = "篡改"
	input.Chapters[0].SourceIDs[0] = "other"
	stored, err := store.Get("project-1", snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.FrozenInput.Spec["research_goal"] != "验证" || stored.FrozenInput.Chapters[0].SourceIDs[0] != "source-1" {
		t.Fatalf("snapshot was mutable: %+v", stored.FrozenInput)
	}
	if digest, _ := digestFrozenInput(stored.FrozenInput); digest != stored.SnapshotDigest {
		t.Fatalf("digest = %s, want %s", digest, stored.SnapshotDigest)
	}
}

func TestPreparePersistsBlockedSnapshotWithActionableIssues(t *testing.T) {
	service := NewReleaseService(NewMemorySnapshotStore())
	input := validDeliveryInput()
	input.Chapters[0].ChapterVersionID = nil
	input.Chapters[0].Confirmation = nil
	snapshot, err := service.Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Check.Status != CheckBlocked {
		t.Fatalf("status = %s", snapshot.Check.Status)
	}
	if !reflect.DeepEqual(sortedIssueCodes(snapshot.Check.Issues), []string{"chapter_version_missing"}) {
		t.Fatalf("issues = %+v", snapshot.Check.Issues)
	}
}

func TestEvaluateRejectsCitationAndSourceDrift(t *testing.T) {
	input := validDeliveryInput()
	input.Chapters[0].BodyMarkdown = "没有来源标记"
	input.Sources[0].AssetRevision = 5
	result := Evaluate(input)
	if result.Status != CheckBlocked {
		t.Fatalf("status = %s", result.Status)
	}
	if !reflect.DeepEqual(sortedIssueCodes(result.Issues), []string{"citation_mismatch", "confirmation_asset_versions_mismatch", "source_invalid"}) {
		t.Fatalf("issues = %+v", result.Issues)
	}
}

func TestEvaluateSurfacesUnsupportedMarkdownBeforeExport(t *testing.T) {
	input := validDeliveryInput()
	input.Chapters[0].BodyMarkdown = "- 列表项 [[source:source-1]]"

	result := Evaluate(input)
	if result.Status != CheckPassed {
		t.Fatalf("status = %s, want passed with warning", result.Status)
	}
	if !reflect.DeepEqual(sortedIssueCodes(result.Issues), []string{"unsupported_markdown"}) {
		t.Fatalf("issues = %+v, want unsupported_markdown", result.Issues)
	}
	issue := result.Issues[0]
	if issue.RuleID != RuleChapterNonempty || issue.ChapterID != "chapter-question" || issue.Severity != SeverityWarning {
		t.Fatalf("unsupported markdown issue = %+v", issue)
	}
}

func TestReleaseCurrentnessIsCheckedWhenReadAndPrepared(t *testing.T) {
	forEachSnapshotStore(t, testReleaseCurrentnessIsCheckedWhenReadAndPrepared)
}

func testReleaseCurrentnessIsCheckedWhenReadAndPrepared(t *testing.T, store snapshotStoreUnderTest) {
	current := true
	service := NewReleaseService(store, CurrentnessFunc(func(DeliveryInput) (bool, error) { return current, nil }))
	snapshot, err := service.Prepare(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	current = false
	stored, err := service.Get(snapshot.ProjectID, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.IsCurrent {
		t.Fatal("stored snapshot should become non-current after the source changes")
	}
	if _, err := service.Prepare(validDeliveryInput()); !errors.Is(err, ErrSnapshotStaleInput) {
		t.Fatalf("Prepare error = %v, want %v", err, ErrSnapshotStaleInput)
	}
}

func TestReleaseSnapshotIDsSurviveServiceRestart(t *testing.T) {
	forEachSnapshotStore(t, testReleaseSnapshotIDsSurviveServiceRestart)
}

func testReleaseSnapshotIDsSurviveServiceRestart(t *testing.T, store snapshotStoreUnderTest) {
	first, err := NewReleaseService(store).Prepare(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewReleaseService(store).Prepare(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("snapshot IDs collided: %q", first.ID)
	}
	if _, err := store.Get(first.ProjectID, first.ID); err != nil {
		t.Fatalf("first snapshot was overwritten: %v", err)
	}
}

func TestEvaluateRejectsConfirmationAssetVersionDrift(t *testing.T) {
	input := validDeliveryInput()
	input.Chapters[0].Confirmation.AssetVersions[0].Revision = 5
	result := Evaluate(input)
	if !reflect.DeepEqual(sortedIssueCodes(result.Issues), []string{"confirmation_asset_versions_mismatch"}) {
		t.Fatalf("issues = %+v", result.Issues)
	}
}

func TestFrozenInputJSONUsesStablePublicNames(t *testing.T) {
	raw, err := json.Marshal(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	chapters := decoded["chapters"].([]any)
	if _, exists := chapters[0].(map[string]any)["section_id"]; exists {
		t.Fatalf("section_id must not be part of the frozen public JSON: %s", raw)
	}
	assets := decoded["asset_versions"].([]any)
	asset := assets[0].(map[string]any)
	if _, exists := asset["revision"]; exists || asset["asset_revision"] != float64(4) {
		t.Fatalf("asset version JSON = %#v", asset)
	}
}

func TestTemplateCheckAndPreflightShareDeterministicRuleEvaluation(t *testing.T) {
	input := validDeliveryInput()
	input.Template.Rules = append(input.Template.Rules, lingdoctemplate.Rule{
		ID: "project-goal-pattern", Kind: "pattern", Severity: SeverityWarning,
		Parameters: map[string]any{"target_kind": "field", "target_id": "research_goal", "pattern": "^approved:"},
	})
	definition, err := lingdoctemplate.WithHashes(input.Template)
	if err != nil {
		t.Fatal(err)
	}
	input.Template = definition
	input.TemplateCopyID, input.TemplateCopyVersion, input.TemplateCopyContentHash = "copy-1", 1, definition.ContentHash

	first, err := EvaluateTemplate(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EvaluateTemplate(input)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("template evaluation is not deterministic: first=%+v second=%+v err=%v", first, second, err)
	}
	var ruleEvaluation *lingdoctemplate.Evaluation
	for i := range first.Evaluations {
		if first.Evaluations[i].RuleID == "project-goal-pattern" {
			ruleEvaluation = &first.Evaluations[i]
			break
		}
	}
	if ruleEvaluation == nil || ruleEvaluation.Status != lingdoctemplate.EvaluationIssue || ruleEvaluation.Severity != SeverityWarning || ruleEvaluation.TargetVersion == "" || ruleEvaluation.RulesetHash != definition.RulesetHash || ruleEvaluation.EvaluatorVersion != lingdoctemplate.EvaluatorVersion {
		t.Fatalf("missing versioned, explainable custom-rule result: %+v", first)
	}

	check, err := EvaluateTemplate(input)
	if err != nil || check.Status != TemplateCheckPassed || check.TemplateCopyID != "copy-1" || check.TemplateCopyVersion != 1 || check.ContentHash != definition.ContentHash {
		t.Fatalf("template check result = %+v err=%v", check, err)
	}
	preflight := Evaluate(input)
	var finding *CheckIssue
	for i := range preflight.Issues {
		if preflight.Issues[i].RuleID == "project-goal-pattern" {
			finding = &preflight.Issues[i]
			break
		}
	}
	if finding == nil || finding.Severity != SeverityWarning || finding.Code != "template_rule_issue" {
		t.Fatalf("preflight did not apply the same custom-rule result: %+v", preflight)
	}
	repeated := Evaluate(input)
	for _, issue := range repeated.Issues {
		if issue.RuleID == "project-goal-pattern" && issue.ID != finding.ID {
			t.Fatalf("custom finding identity changed: %s != %s", issue.ID, finding.ID)
		}
	}
}

func TestBlockingUnknownRuleTargetBlocksTemplateCheckAndPreflight(t *testing.T) {
	input := validDeliveryInput()
	set, err := lingdoctemplate.PublishRuleSet(lingdoctemplate.RuleSet{
		ID: "unavailable-target-rules", Version: "v1", State: lingdoctemplate.RuleSetDraft,
		Bindings: []lingdoctemplate.RuleBinding{{
			RuleID: "missing-field-pattern", DefinitionID: "pattern", DefinitionVersion: "1",
			Origin: "organization", TargetKind: "field", TargetID: "removed_field",
			Parameters: map[string]any{"pattern": "^approved:"}, Severity: SeverityBlocking,
			TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "research-office",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Template.RuleSetSnapshot = &set
	definition, err := lingdoctemplate.WithHashes(input.Template)
	if err != nil {
		t.Fatal(err)
	}
	input.Template = definition

	check, err := EvaluateTemplate(input)
	if err != nil || check.Status != TemplateCheckBlocked {
		t.Fatalf("blocking unknown target must not pass Template Check: status=%s evaluations=%+v err=%v", check.Status, check.Evaluations, err)
	}
	foundUnknown := false
	for _, evaluation := range check.Evaluations {
		if evaluation.RuleID == "missing-field-pattern" {
			foundUnknown = evaluation.Status == lingdoctemplate.EvaluationUnknown && evaluation.Severity == SeverityBlocking
		}
	}
	if !foundUnknown {
		t.Fatalf("Template Check did not preserve the blocking UNKNOWN evaluation: %+v", check.Evaluations)
	}

	preflight := Evaluate(input)
	if preflight.Status != CheckBlocked {
		t.Fatalf("blocking unknown target must block Preflight: %+v", preflight)
	}
	foundIssue := false
	for _, issue := range preflight.Issues {
		if issue.RuleID == "missing-field-pattern" {
			foundIssue = issue.Severity == SeverityBlocking && issue.Code == "template_rule_unavailable"
		}
	}
	if !foundIssue {
		t.Fatalf("Preflight did not record the unavailable blocking rule as a ValidationIssue: %+v", preflight.Issues)
	}

	snapshotStore := NewMemorySnapshotStore()
	release, err := NewReleaseService(snapshotStore).Prepare(input)
	if err != nil || release.Check.Status != CheckBlocked || release.Check.RulesetHash != definition.RulesetHash {
		t.Fatalf("ReleaseSnapshot did not freeze the blocked rule evaluation: release=%+v err=%v", release, err)
	}
	auditor := &snapshotRuleSetUseAuditRecorder{}
	exportService := NewExportService(snapshotStore, NewMemoryExportStore(),
		frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("must-not-render"), nil }),
		FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport)).WithRuleSetUseAuditor(auditor)
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if _, _, err := exportService.StartAs(context.Background(), actor, release.ProjectID, release.ID, "blocking-unknown-export"); !errors.Is(err, ErrExportPreflightBlocked) {
		t.Fatalf("export must reject a frozen blocking UNKNOWN evaluation: %v", err)
	}
	if len(auditor.events) != 1 || auditor.events[0].Action != "export" || auditor.events[0].Outcome != "blocked" || auditor.events[0].RuleSetID != set.ID || auditor.events[0].RuleSetVersion != set.Version || auditor.events[0].RuleSetHash != set.RulesetHash || auditor.events[0].EffectiveRulesetHash != definition.RulesetHash {
		t.Fatalf("blocked export use must be audited against the exact RuleSet snapshot: %+v", auditor.events)
	}
}

func TestBoundRuleSetSnapshotFlowsThroughTemplateCheckAndPreflight(t *testing.T) {
	input := validDeliveryInput()
	set, err := lingdoctemplate.PublishRuleSet(lingdoctemplate.RuleSet{
		ID: "delivery-rules", Version: "v1", State: lingdoctemplate.RuleSetDraft,
		Bindings: []lingdoctemplate.RuleBinding{{
			RuleID: "goal-prefix", DefinitionID: "pattern", DefinitionVersion: "1",
			Origin: "organization", TargetKind: "field", TargetID: "research_goal",
			Parameters: map[string]any{"pattern": "^approved:"}, Severity: SeverityBlocking,
			TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "research-office",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Template.RuleSetSnapshot = &set
	definition, err := lingdoctemplate.WithHashes(input.Template)
	if err != nil {
		t.Fatal(err)
	}
	input.Template = definition

	check, err := EvaluateTemplate(input)
	if err != nil {
		t.Fatal(err)
	}
	if check.Status != TemplateCheckBlocked || check.RulesetHash != definition.RulesetHash {
		t.Fatalf("bound blocking rule was not included in template check: %+v", check)
	}
	found := false
	for _, evaluation := range check.Evaluations {
		if evaluation.RuleID == "goal-prefix" {
			found = evaluation.Status == lingdoctemplate.EvaluationIssue && evaluation.RulesetHash == definition.RulesetHash
		}
	}
	if !found {
		t.Fatalf("template check did not emit the pinned RuleSet evaluation: %+v", check.Evaluations)
	}

	preflight := Evaluate(input)
	if preflight.Status != CheckBlocked || preflight.RulesetHash != definition.RulesetHash {
		t.Fatalf("Preflight did not consume the same bound RuleSet snapshot: %+v", preflight)
	}
	found = false
	for _, issue := range preflight.Issues {
		if issue.RuleID == "goal-prefix" {
			found = issue.Severity == SeverityBlocking && issue.RulesetHash == definition.RulesetHash
		}
	}
	if !found {
		t.Fatalf("Preflight did not retain the bound RuleSet finding: %+v", preflight.Issues)
	}

	snapshotStore := NewMemorySnapshotStore()
	release, err := NewReleaseService(snapshotStore).Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	if release.Check.RulesetHash != definition.RulesetHash || release.FrozenInput.Template.RuleSetSnapshot == nil || release.FrozenInput.Template.RuleSetSnapshot.RulesetHash != set.RulesetHash {
		t.Fatalf("ReleaseSnapshot did not freeze the evaluated RuleSet identity and hash: %+v", release)
	}
	set.Bindings[0].Parameters["pattern"] = "^tampered$"
	if release.FrozenInput.Template.RuleSetSnapshot.Bindings[0].Parameters["pattern"] != "^approved:" {
		t.Fatal("frozen ReleaseSnapshot shared mutable RuleSet data with its input")
	}
	exports := NewMemoryExportStore()
	auditor := &snapshotRuleSetUseAuditRecorder{}
	exportService := NewExportService(snapshotStore, exports,
		frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("must-not-render"), nil }),
		FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport)).WithRuleSetUseAuditor(auditor)
	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if _, _, err := exportService.StartAs(context.Background(), actor, release.ProjectID, release.ID, "ruleset-blocked-export"); !errors.Is(err, ErrExportPreflightBlocked) {
		t.Fatalf("export must honor the frozen RuleSet Preflight result: %v", err)
	}
	if len(auditor.events) != 1 || auditor.events[0].Action != "export" || auditor.events[0].Outcome != "blocked" || auditor.events[0].RuleSetID != set.ID || auditor.events[0].RuleSetVersion != set.Version || auditor.events[0].RuleSetHash != set.RulesetHash || auditor.events[0].EffectiveRulesetHash != definition.RulesetHash {
		t.Fatalf("blocked export use must be audited against the exact RuleSet snapshot: %+v", auditor.events)
	}
}
