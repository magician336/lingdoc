package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// #774 tier B: the manual selection anchor (impact_selection_reference.go).
// The seams are the service API (verbatim validation, dedup, CAS, verdict
// untouched) and the chapter-advance re-verification.

func TestAddImpactSelectionReferenceAnchorsVerbatimSelection(t *testing.T) {
	body := "首段 unrelated。\n\n旧研究主题全文见于次段，样本选择也需要复核。\n\n末段含登记目标值为定位而设。"
	svc, actor, project, chapters := impactLocationSeed(t, body, 361)
	project = impactLocationApplyChange(t, svc, actor, project, chapters, "impact-location-b1", "research_goal", ChangeFieldInput{OldValue: "旧目标", NewValue: "登记目标值为定位而设"})
	chapters, err := svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := impactLocationRunWatchedChange(t, svc, actor, project, chapters, "impact-location-run-b1", "研究对象变化,选段引用")
	raw, status, replay, err := svc.AddImpactSelectionReference(context.Background(), actor, project.ID, task.ID, "impact-selection-b1", ImpactSelectionReferenceInput{
		ExpectedVersion: task.Version, Quote: "样本选择也需要复核", Reason: "人工圈定该句需复核",
	})
	if err != nil || status != 200 || replay {
		t.Fatalf("add selection: status=%d replay=%v err=%v", status, replay, err)
	}
	var added ImpactTask
	if err := json.Unmarshal(raw, &added); err != nil {
		t.Fatal(err)
	}
	anchored := 0
	for _, hint := range added.LocationHints {
		if hint.Source != ImpactLocationSourceSelectionReference {
			continue
		}
		anchored++
		if hint.Value != "样本选择也需要复核" || hint.ParagraphIndex != 2 || hint.StartOffset <= 0 {
			t.Fatalf("anchored hint = %+v", hint)
		}
		if len(hint.SelectionHash) != 64 {
			t.Fatalf("selection hash = %q, want 64 hex chars (QuotedTextHash shape)", hint.SelectionHash)
		}
	}
	if anchored != 1 {
		t.Fatalf("anchored hints = %d, want 1", anchored)
	}
	// Duplicate selection on the same task is refused.
	if _, _, _, err := svc.AddImpactSelectionReference(context.Background(), actor, project.ID, task.ID, "impact-selection-b1-dup", ImpactSelectionReferenceInput{
		ExpectedVersion: added.Version, Quote: "样本选择也需要复核", Reason: "重复圈定应被拒",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("duplicate error = %v, want ErrInvalidRequest", err)
	}
	// A quote the body does not carry is refused without guessing.
	if _, _, _, err := svc.AddImpactSelectionReference(context.Background(), actor, project.ID, task.ID, "impact-selection-b1-miss", ImpactSelectionReferenceInput{
		ExpectedVersion: added.Version, Quote: "这句不在正文里", Reason: "幻觉选段应被拒",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing quote error = %v, want ErrInvalidRequest", err)
	}
	// Stale task version is refused by CAS.
	if _, _, _, err := svc.AddImpactSelectionReference(context.Background(), actor, project.ID, task.ID, "impact-selection-b1-cas", ImpactSelectionReferenceInput{
		ExpectedVersion: 1, Quote: "样本选择也需要复核", Reason: "旧版本应 409",
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale CAS error = %v, want ErrVersionConflict", err)
	}
}

func TestChapterAdvanceCoversSelectionReferences(t *testing.T) {
	body := "首段 unrelated。\n\n旧研究主题全文见于次段，样本选择也需要复核。"
	svc, actor, project, chapters := impactLocationSeed(t, body, 362)
	task := impactLocationRunWatchedChange(t, svc, actor, project, chapters, "impact-location-run-b2", "研究对象变化,选段引用")
	if _, _, _, err := svc.AddImpactSelectionReference(context.Background(), actor, project.ID, task.ID, "impact-selection-b2", ImpactSelectionReferenceInput{
		ExpectedVersion: task.Version, Quote: "样本选择也需要复核", Reason: "人工圈定",
	}); err != nil {
		t.Fatalf("anchor: %v", err)
	}
	project, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	chapters, err = svc.ListChapters(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[0].ID, "impact-location-advance-b2", SaveChapterInput{
		ExpectedChapterVersionID: chapters[0].CurrentVersionID,
		ExpectedSpecRevision:     project.SpecRevision,
		BodyMarkdown:             "改写后的正文，选段已不在。",
		SourceIDs:                []string{},
	}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	tasks, err := svc.ListImpactTasks(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored := impactLocationTaskByID(t, tasks, task.ID)
	selectionRelocated := false
	for _, hint := range stored.LocationHints {
		if hint.Source == ImpactLocationSourceSelectionReference && hint.Relocate {
			selectionRelocated = true
		}
	}
	if !selectionRelocated {
		t.Fatalf("selection reference not relocated: %+v", stored.LocationHints)
	}
}
