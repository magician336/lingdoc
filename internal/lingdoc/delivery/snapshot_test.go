package delivery

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

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
