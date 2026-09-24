
package delivery

import (
	"reflect"
	"testing"
	"time"
)

func validDeliveryInput() DeliveryInput {
	template := DemoTemplate()
	version := "chapter-question-v1"
	return DeliveryInput{
		ProjectID: "project-1", ProjectName: "内部演示", ProjectVersion: 3, SpecRevision: 2,
		Spec: map[string]string{"research_subject": "LingDoc", "research_goal": "验证"}, Template: template,
		Chapters: []SnapshotChapter{{ChapterID: "chapter-question", ChapterVersionID: &version, SectionID: "question", Title: "研究问题", BodyMarkdown: "结论 [[source:source-1]]", SourceIDs: []string{"source-1"}, ReviewItems: []ReviewItem{{ID: "review-1", Statement: "待核", OriginCandidateID: "candidate-1"}}, Confirmation: &Confirmation{ChapterVersionID: version, SpecRevision: 2, TemplateVersion: template.Version, Decisions: []ReviewDecision{{ReviewItemID: "review-1", Disposition: "resolved", Reason: "人工核验"}}}},
			{ChapterID: "chapter-method", ChapterVersionID: stringPtr("chapter-method-v1"), SectionID: "method", Title: "研究方案", BodyMarkdown: "方案正文", SourceIDs: []string{}, Confirmation: &Confirmation{ChapterVersionID: "chapter-method-v1", SpecRevision: 2, TemplateVersion: template.Version}}},
		Sources:       []FrozenSource{{ID: "source-1", ProjectID: "project-1", AssetID: "asset-1", AssetRevision: 4, Locator: "第 1 段", QuotedText: "原文", QuotedTextHash: sha256Text("原文"), DisplayTitle: "演示资料"}},
		AssetVersions: []AssetVersion{{AssetID: "asset-1", Revision: 4}}, PolicyAssetIDs: []string{"asset-1"}, DeliveryKind: "internal_demo",
	}
}
func stringPtr(value string) *string { return &value }

func TestPrepareFreezesInputAndDigest(t *testing.T) {
	store := NewMemorySnapshotStore()
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
	if !reflect.DeepEqual(sortedIssueCodes(result.Issues), []string{"citation_mismatch", "source_invalid"}) {
		t.Fatalf("issues = %+v", result.Issues)
	}
}

