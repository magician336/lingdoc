package delivery

import (
	"encoding/json"
	"testing"
)

// The published OpenAPI schema fixes rule_id, severity, target_id and
// target_version for every ValidationIssue. These tests fail if the evaluator
// drifts back to emitting internal diagnostic codes or a hardcoded severity.

func publishedSeverities() map[string]string {
	out := map[string]string{}
	for _, rule := range DemoTemplate().Rules {
		out[rule.ID] = rule.Severity
	}
	return out
}

func issuesByCode(result CheckResult) map[string]CheckIssue {
	out := make(map[string]CheckIssue, len(result.Issues))
	for _, issue := range result.Issues {
		out[issue.Code] = issue
	}
	return out
}

func TestIssuesNamePublishedRulesAndDeclaredSeverity(t *testing.T) {
	input := validDeliveryInput()
	input.Spec["research_goal"] = ""
	input.Chapters[0].BodyMarkdown = ""
	input.Chapters[1].ChapterVersionID = nil
	input.Chapters[1].Confirmation = nil
	result := Evaluate(input)
	if result.Status != CheckBlocked {
		t.Fatalf("status = %s, want blocked", result.Status)
	}
	if len(result.Issues) == 0 {
		t.Fatal("a blocked result carried no issue")
	}
	published := publishedSeverities()
	for _, issue := range result.Issues {
		severity, declared := published[issue.RuleID]
		if !declared {
			t.Fatalf("issue %q names rule %q, which the frozen template does not declare", issue.Code, issue.RuleID)
		}
		if issue.Severity != severity {
			t.Fatalf("issue %q severity = %q, want the template's %q", issue.Code, issue.Severity, severity)
		}
		if issue.RulesetHash != DemoRulesetHash {
			t.Fatalf("issue %q ruleset_hash = %q", issue.Code, issue.RulesetHash)
		}
		if issue.TargetID == "" {
			t.Fatalf("issue %q has an empty target_id", issue.Code)
		}
		if issue.Severity != SeverityBlocking && issue.Severity != SeverityWarning && issue.Severity != SeverityInfo {
			t.Fatalf("issue %q severity = %q, outside the published enum", issue.Code, issue.Severity)
		}
	}
	missing, ok := issuesByCode(result)["required_field_missing"]
	if !ok {
		t.Fatalf("issues = %+v", result.Issues)
	}
	if missing.RuleID != RuleRequiredFields || missing.TargetID != "project-1" || missing.TargetVersion != nil {
		t.Fatalf("project-scoped issue = %+v", missing)
	}
	if missing.ChapterID != "" {
		t.Fatalf("project-scoped issue leaked a chapter: %+v", missing)
	}
}

func TestChapterIssuesPinTheReviewedVersion(t *testing.T) {
	input := validDeliveryInput()
	input.Chapters[0].BodyMarkdown = ""      // a reviewed version exists, so it must be cited
	input.Chapters[1].ChapterVersionID = nil // never written: nothing to cite
	input.Chapters[1].Confirmation = nil
	issues := issuesByCode(Evaluate(input))

	empty, ok := issues["chapter_empty"]
	if !ok {
		t.Fatalf("issues = %+v", issues)
	}
	if empty.TargetID != "chapter-question" || empty.TargetVersion == nil || *empty.TargetVersion != "chapter-question-v1" {
		t.Fatalf("chapter_empty = %+v", empty)
	}
	unwritten, ok := issues["chapter_version_missing"]
	if !ok {
		t.Fatalf("issues = %+v", issues)
	}
	if unwritten.TargetID != "chapter-method" || unwritten.TargetVersion != nil {
		t.Fatalf("chapter_version_missing = %+v", unwritten)
	}
}

func TestRetainedReviewItemIsAWarningAndStillReleases(t *testing.T) {
	input := validDeliveryInput()
	input.Chapters[0].Confirmation.Decisions = []ReviewDecision{{ReviewItemID: "review-1", Disposition: DispositionRetainedWarning, Reason: "负责人选择保留该待核项"}}
	store := NewMemorySnapshotStore()
	snapshot, err := NewReleaseService(store).Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Check.Status != CheckPassed {
		t.Fatalf("status = %s, want passed", snapshot.Check.Status)
	}
	if len(snapshot.Check.Issues) != 1 {
		t.Fatalf("issues = %+v", snapshot.Check.Issues)
	}
	warning := snapshot.Check.Issues[0]
	if warning.Code != "retained_review_item" || warning.RuleID != RuleReviewItemsDecided || warning.Severity != SeverityWarning {
		t.Fatalf("retained warning = %+v", warning)
	}
	if warning.TargetID != "chapter-question" || warning.TargetVersion == nil || *warning.TargetVersion != "chapter-question-v1" {
		t.Fatalf("retained warning = %+v", warning)
	}

	// A retained item is carried into the file, not turned into a fake download
	// failure: the release still has to succeed.
	exports := NewMemoryExportStore()
	service := NewExportService(store, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) {
		return []byte("PK\x03\x04retained appendix"), nil
	}), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))
	artifact, err := service.Start("owner", snapshot.ProjectID, snapshot.ID)
	if err != nil || artifact.Status != ExportVerified {
		t.Fatalf("retained warning blocked the release: %+v, %v", artifact, err)
	}
}

func TestEvaluateWithoutRulesetIsNotEvaluated(t *testing.T) {
	input := validDeliveryInput()
	input.Template = Template{ID: DemoTemplateID, Version: DemoTemplateVersion}
	result := Evaluate(input)
	if result.Status != CheckNotEvaluated || len(result.Issues) != 0 || result.Issues == nil {
		t.Fatalf("result = %+v", result)
	}
}

func TestCheckResultJSONMatchesThePublishedSchema(t *testing.T) {
	passing := Evaluate(validDeliveryInput())
	if passing.Status != CheckPassed {
		t.Fatalf("fixture status = %s", passing.Status)
	}
	raw, err := json.Marshal(passing)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	// CheckResult is published with additionalProperties: false, and its issues
	// array must serialize as an array even when nothing was found.
	for _, key := range []string{"project_version", "ruleset_hash", "status", "issues"} {
		if _, exists := decoded[key]; !exists {
			t.Fatalf("CheckResult JSON is missing %q: %s", key, raw)
		}
	}
	if len(decoded) != 4 {
		t.Fatalf("CheckResult JSON carries unpublished keys: %s", raw)
	}
	if string(decoded["issues"]) != "[]" {
		t.Fatalf("issues = %s, want an empty array", decoded["issues"])
	}

	blocked := validDeliveryInput()
	blocked.Chapters[0].Confirmation = nil
	raw, err = json.Marshal(Evaluate(blocked))
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Issues []map[string]json.RawMessage `json:"issues"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Issues) == 0 {
		t.Fatalf("blocked result carried no issue: %s", raw)
	}
	// ValidationIssue is published with additionalProperties: false too, and
	// target_version stays required even when it is null.
	expected := []string{"id", "rule_id", "ruleset_hash", "severity", "target_id", "target_version", "message"}
	for _, issue := range envelope.Issues {
		if len(issue) != len(expected) {
			t.Fatalf("ValidationIssue JSON carries unpublished keys: %s", raw)
		}
		for _, key := range expected {
			if _, exists := issue[key]; !exists {
				t.Fatalf("ValidationIssue JSON is missing %q: %s", key, raw)
			}
		}
	}
}
