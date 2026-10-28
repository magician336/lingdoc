package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type stubLocationModel struct {
	calls  int
	result ImpactLocationSuggestionResult
	err    error
}

func (m *stubLocationModel) Suggest(ctx context.Context, actor Actor, request ImpactLocationSuggestionRequest) (ImpactLocationSuggestionResult, error) {
	m.calls++
	return m.result, m.err
}

func suggestionModel(proposals ...ModelProposedImpactLocation) *stubLocationModel {
	return &stubLocationModel{result: ImpactLocationSuggestionResult{
		Proposals: proposals, ModelID: "deepseek-test", RunMode: "real", ModelRunID: "loc-stub-1",
	}}
}

func suggestionOutcome(t *testing.T, raw json.RawMessage) ImpactLocationSuggestionOutcome {
	t.Helper()
	var outcome ImpactLocationSuggestionOutcome
	if err := json.Unmarshal(raw, &outcome); err != nil {
		t.Fatal(err)
	}
	return outcome
}

func TestSuggestImpactLocationsVerifiesQuotesAndDropsHallucinations(t *testing.T) {
	// Recall honesty: the chapter body carries two real sentences the model
	// may point at, plus one hallucinated quote the server must refuse.
	body := "首段 unrelated。\n\n旧研究主题全文见于次段，样本选择也需要复核。\n\n末段含登记目标值为定位而设。"
	svc, actor, project, chapters := impactLocationSeed(t, body, 351)
	project = impactLocationApplyChange(t, svc, actor, project, chapters, "impact-location-c2", "research_goal", ChangeFieldInput{OldValue: "旧目标", NewValue: "登记目标值为定位而设"})
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := impactLocationRunWatchedChange(t, svc, actor, project, chapters, "impact-location-run-c2", "研究对象变化,句级定位")
	model := suggestionModel(
		ModelProposedImpactLocation{Quote: "样本选择也需要复核", ParagraphHint: 2, ImpactReason: "样本表述与变更冲突", Confidence: 0.8, SuggestedAction: "核对样本数"},
		ModelProposedImpactLocation{Quote: "末段含登记目标值", ParagraphHint: 3, ImpactReason: "登记值出现在末段", Confidence: 0.6},
		ModelProposedImpactLocation{Quote: "这句话不在正文里", ParagraphHint: 9, ImpactReason: "幻觉定位", Confidence: 0.9},
	)
	svc.SetImpactLocationSuggestionModel(model)
	raw, status, replay, err := svc.SuggestImpactLocations(context.Background(), actor, project.ID, task.ID, "impact-location-suggest-c2", ImpactLocationSuggestionInput{
		ExpectedVersion: task.Version, Reason: "AI 句级定位",
	})
	if err != nil || status != 200 || replay {
		t.Fatalf("suggest: status=%d replay=%v err=%v", status, replay, err)
	}
	outcome := suggestionOutcome(t, raw)
	if len(outcome.Suggestions) != 2 || len(outcome.Dropped) != 1 || outcome.Dropped[0].Reason != "quote_not_found" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.Suggestions[0].Status != ImpactLocationSuggestionPending || outcome.Suggestions[0].ParagraphIndex != 2 || outcome.Suggestions[0].StartOffset <= 0 {
		t.Fatalf("suggestion 0 = %+v", outcome.Suggestions[0])
	}
	if !strings.Contains(outcome.Suggestions[0].Excerpt, "样本选择也需要复核") {
		t.Fatalf("excerpt lost the quote: %q", outcome.Suggestions[0].Excerpt)
	}
	tasks, err := svc.ListImpactTasks(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored := impactLocationTaskByID(t, tasks, outcomeTaskID(t, raw))
	if len(stored.LocationSuggestions) != 2 || stored.SuggestionBatchKey != "impact-location-suggest-c2" {
		t.Fatalf("stored task = %+v", stored)
	}
	if len(stored.LocationHints) == 0 {
		t.Fatalf("tier-A hints vanished: %+v", stored)
	}
	// A replayed key must not cost a second model call.
	raw, status, replay, err = svc.SuggestImpactLocations(context.Background(), actor, project.ID, task.ID, "impact-location-suggest-c2", ImpactLocationSuggestionInput{
		ExpectedVersion: stored.Version, Reason: "AI 句级定位",
	})
	if err != nil || status != 200 || !replay {
		t.Fatalf("replay: status=%d replay=%v err=%v", status, replay, err)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}
}

func outcomeTaskID(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var outcome ImpactLocationSuggestionOutcome
	if err := json.Unmarshal(raw, &outcome); err != nil {
		t.Fatal(err)
	}
	if len(outcome.Task) == 0 {
		t.Fatal("outcome task missing")
	}
	var task ImpactTask
	if err := json.Unmarshal(outcome.Task, &task); err != nil {
		t.Fatal(err)
	}
	return task.ID
}

func impactLocationTaskByID(t *testing.T, tasks []ImpactTask, id string) ImpactTask {
	t.Helper()
	for _, task := range tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("no task %s in %+v", id, tasks)
	return ImpactTask{}
}

func TestDecideImpactLocationSuggestionAdoptsIntoHintsAndRejects(t *testing.T) {
	body := "首段 unrelated。\n\n旧研究主题全文见于次段，样本选择也需要复核。\n\n末段含登记目标值为定位而设。"
	svc, actor, project, chapters := impactLocationSeed(t, body, 352)
	project = impactLocationApplyChange(t, svc, actor, project, chapters, "impact-location-c3", "research_goal", ChangeFieldInput{OldValue: "旧目标", NewValue: "登记目标值为定位而设"})
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := impactLocationRunWatchedChange(t, svc, actor, project, chapters, "impact-location-run-c3", "研究对象变化,句级定位")
	model := suggestionModel(
		ModelProposedImpactLocation{Quote: "样本选择也需要复核", ParagraphHint: 2, ImpactReason: "样本表述与变更冲突", Confidence: 0.8},
		ModelProposedImpactLocation{Quote: "末段含登记目标值", ParagraphHint: 3, ImpactReason: "登记值出现在末段", Confidence: 0.6},
	)
	svc.SetImpactLocationSuggestionModel(model)
	raw, _, _, err := svc.SuggestImpactLocations(context.Background(), actor, project.ID, task.ID, "impact-location-suggest-c3", ImpactLocationSuggestionInput{
		ExpectedVersion: task.Version, Reason: "AI 句级定位",
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome := suggestionOutcome(t, raw)
	verdictBefore := struct {
		status, action, severity string
	}{outcomeTaskVerdict(t, raw).status, outcomeTaskVerdict(t, raw).action, outcomeTaskVerdict(t, raw).severity}

	adopted := outcome.Suggestions[0]
	afterAdopt, _, _, err := svc.DecideImpactLocationSuggestion(context.Background(), actor, project.ID, outcomeTaskID(t, raw), adopted.ID, "impact-location-adopt-c3", ImpactLocationSuggestionDecisionInput{
		ExpectedVersion: outcomeTaskVerdict(t, raw).version, Decision: "adopt", Reason: "采纳该句级定位",
	})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	var adoptedTask ImpactTask
	if err := json.Unmarshal(afterAdopt, &adoptedTask); err != nil {
		t.Fatal(err)
	}
	aiHints := 0
	for _, hint := range adoptedTask.LocationHints {
		if hint.Source == ImpactLocationSourceAISentence {
			aiHints++
			if hint.Value != adopted.Quote || hint.StartOffset != adopted.StartOffset {
				t.Fatalf("adopted hint = %+v", hint)
			}
		}
	}
	if aiHints != 1 {
		t.Fatalf("ai hints = %d, want 1", aiHints)
	}
	if adoptedTask.LocationSuggestions[0].Status != ImpactLocationSuggestionAdopted {
		t.Fatalf("suggestion status = %s", adoptedTask.LocationSuggestions[0].Status)
	}
	// Adoption is an attachment: the verdict fields never moved.
	if adoptedTask.Status != verdictBefore.status || adoptedTask.Action != verdictBefore.action || adoptedTask.Severity != verdictBefore.severity {
		t.Fatalf("verdict moved: %+v", adoptedTask)
	}

	rejected := outcome.Suggestions[1]
	afterReject, _, _, err := svc.DecideImpactLocationSuggestion(context.Background(), actor, project.ID, adoptedTask.ID, rejected.ID, "impact-location-reject-c3", ImpactLocationSuggestionDecisionInput{
		ExpectedVersion: adoptedTask.Version, Decision: "reject", Reason: "该句与本次变更无关",
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	var rejectedTask ImpactTask
	if err := json.Unmarshal(afterReject, &rejectedTask); err != nil {
		t.Fatal(err)
	}
	if rejectedTask.LocationSuggestions[1].Status != ImpactLocationSuggestionRejected {
		t.Fatalf("rejected status = %s", rejectedTask.LocationSuggestions[1].Status)
	}
	if _, _, _, err := svc.DecideImpactLocationSuggestion(context.Background(), actor, project.ID, adoptedTask.ID, rejected.ID, "impact-location-again-c3", ImpactLocationSuggestionDecisionInput{
		ExpectedVersion: rejectedTask.Version, Decision: "adopt", Reason: "重复决定应被拒",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("second decision error = %v, want ErrInvalidState", err)
	}
	if _, _, _, err := svc.DecideImpactLocationSuggestion(context.Background(), actor, project.ID, adoptedTask.ID, "no-such-suggestion", "impact-location-missing-c3", ImpactLocationSuggestionDecisionInput{
		ExpectedVersion: rejectedTask.Version, Decision: "adopt", Reason: "未知建议应 404",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown suggestion error = %v, want ErrNotFound", err)
	}
	if _, _, _, err := svc.DecideImpactLocationSuggestion(context.Background(), actor, project.ID, adoptedTask.ID, adopted.ID, "impact-location-stale-c3", ImpactLocationSuggestionDecisionInput{
		ExpectedVersion: 1, Decision: "adopt", Reason: "旧版本应 409",
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version error = %v, want ErrVersionConflict", err)
	}
}

type suggestionTaskVerdict struct {
	status, action, severity string
	version                  int64
}

func outcomeTaskVerdict(t *testing.T, raw json.RawMessage) suggestionTaskVerdict {
	t.Helper()
	var outcome ImpactLocationSuggestionOutcome
	if err := json.Unmarshal(raw, &outcome); err != nil {
		t.Fatal(err)
	}
	var task ImpactTask
	if err := json.Unmarshal(outcome.Task, &task); err != nil {
		t.Fatal(err)
	}
	return suggestionTaskVerdict{status: task.Status, action: task.Action, severity: task.Severity, version: task.Version}
}

func TestChapterAdvanceMarksMissingLocationsForRelocation(t *testing.T) {
	body := "首段 unrelated。\n\n旧研究主题全文见于次段，样本选择也需要复核。\n\n末段含登记目标值为定位而设。"
	svc, actor, project, chapters := impactLocationSeed(t, body, 353)
	project = impactLocationApplyChange(t, svc, actor, project, chapters, "impact-location-c4", "research_goal", ChangeFieldInput{OldValue: "旧目标", NewValue: "登记目标值为定位而设"})
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := impactLocationRunWatchedChange(t, svc, actor, project, chapters, "impact-location-run-c4", "研究对象变化,句级定位")
	model := suggestionModel(ModelProposedImpactLocation{Quote: "样本选择也需要复核", ParagraphHint: 2, ImpactReason: "样本表述与变更冲突", Confidence: 0.8})
	svc.SetImpactLocationSuggestionModel(model)
	raw, _, _, err := svc.SuggestImpactLocations(context.Background(), actor, project.ID, task.ID, "impact-location-suggest-c4", ImpactLocationSuggestionInput{
		ExpectedVersion: task.Version, Reason: "AI 句级定位",
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome := suggestionOutcome(t, raw)
	adoptedID := outcomeTaskID(t, raw)
	_, _, _, err = svc.DecideImpactLocationSuggestion(context.Background(), actor, project.ID, adoptedID, outcome.Suggestions[0].ID, "impact-location-adopt-c4", ImpactLocationSuggestionDecisionInput{
		ExpectedVersion: outcomeTaskVerdict(t, raw).version, Decision: "adopt", Reason: "采纳",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The chapter advances to a body that no longer carries the adopted
	// sentence: the hint and the remaining suggestion flip to relocate.
	project, err = svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	chapters, err = svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[0].ID, "impact-location-advance-c4", SaveChapterInput{
		ExpectedChapterVersionID: chapters[0].CurrentVersionID,
		ExpectedSpecRevision:     project.SpecRevision,
		BodyMarkdown:             "改写后的全新正文，不再含有原来的句子。",
		SourceIDs:                []string{},
	}); err != nil {
		t.Fatalf("advance chapter: %v", err)
	}
	tasks, err := svc.ListImpactTasks(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored := impactLocationTaskByID(t, tasks, task.ID)
	relocatedHints, relocatedSuggestions := 0, 0
	for _, hint := range stored.LocationHints {
		if hint.Relocate {
			relocatedHints++
		}
	}
	for _, suggestion := range stored.LocationSuggestions {
		if suggestion.Relocate {
			relocatedSuggestions++
		}
	}
	if relocatedHints == 0 || relocatedSuggestions == 0 {
		t.Fatalf("relocation not marked: hints=%+v suggestions=%+v", stored.LocationHints, stored.LocationSuggestions)
	}
	// A relocated suggestion cannot be adopted.
	if _, _, _, err := svc.DecideImpactLocationSuggestion(context.Background(), actor, project.ID, stored.ID, stored.LocationSuggestions[0].ID, "impact-location-adopt-relocated-c4", ImpactLocationSuggestionDecisionInput{
		ExpectedVersion: stored.Version, Decision: "adopt", Reason: "失配建议不能采纳",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("adopt relocated error = %v, want ErrInvalidState", err)
	}
	// And the body coming back clears the flags again — the recheck is a
	// recomputation, not a one-way latch.
	project, err = svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	chapters, err = svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[0].ID, "impact-location-restore-c4", SaveChapterInput{
		ExpectedChapterVersionID: chapters[0].CurrentVersionID,
		ExpectedSpecRevision:     project.SpecRevision,
		BodyMarkdown:             body,
		SourceIDs:                []string{},
	}); err != nil {
		t.Fatalf("restore chapter: %v", err)
	}
	tasks, err = svc.ListImpactTasks(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored = impactLocationTaskByID(t, tasks, task.ID)
	for _, hint := range stored.LocationHints {
		if hint.Relocate {
			t.Fatalf("hint still relocated after restore: %+v", hint)
		}
	}
}

func TestSuggestImpactLocationsFailsClosedWithoutModel(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "impact-location-nomodel.db"))
	actor := Actor{TenantID: 354, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	if _, _, _, err := svc.SuggestImpactLocations(context.Background(), actor, project.ID, "task-x", "impact-location-nomodel", ImpactLocationSuggestionInput{
		ExpectedVersion: 1, Reason: "无模型注入应失败关闭",
	}); !errors.Is(err, ErrImpactLocationSuggestionUnavailable) {
		t.Fatalf("error = %v, want ErrImpactLocationSuggestionUnavailable", err)
	}
}
