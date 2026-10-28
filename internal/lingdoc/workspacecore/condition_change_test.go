package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

// The wording-commit condition gate (#753): a registered condition value that
// vanished from the chapter text is a suspicion; the commit is refused behind
// an explicit, audited dismissal that must name the same suspicions.

const conditionFixtureValue = "旧研究主题值"
const absentConditionValue = "从未在正文出现过的目标条件值"

func conditionFixture(t *testing.T) (*Service, Actor, Project, string, WorkingCopy) {
	t.Helper()
	ctx := context.Background()
	svc := testStore(t, filepath.Join(t.TempDir(), "condition.db"))
	actor := Actor{TenantID: 321, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	raw, _, _, err := svc.CreateProject(ctx, actor, "condition-create", CreateProjectInput{Name: "条件检测课题", TemplateID: "template-demo"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	project := asProject(t, raw)
	_, _, _, err = svc.SaveSpec(ctx, actor, project.ID, "condition-spec", SaveSpecInput{
		ExpectedSpecRevision: 0,
		Fields: map[string]string{
			"research_subject": conditionFixtureValue,
			"research_goal":    absentConditionValue, // registered but never in the text
		},
	})
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	raw, _, _, err = svc.ActivateProject(ctx, actor, project.ID, "condition-activate", ActivateProjectInput{ExpectedSpecRevision: 1, ExpectedProjectVersion: 2, ReviewedProjectVersion: 2})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	project = asProject(t, raw)
	chapters, err := svc.ListChapters(ctx, actor, project.ID)
	if err != nil || len(chapters) < 1 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	chapterID := chapters[0].ID
	copy, err := svc.GetWorkingCopy(ctx, actor, project.ID, chapterID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _, err = svc.SaveWorkingCopy(ctx, actor, project.ID, chapterID, "condition-seed-body", SaveWorkingCopyInput{
		BaseChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
		BodyMarkdown: "本课题围绕" + conditionFixtureValue + "展开，方法与结论不变。", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatalf("seed body: %v", err)
	}
	if err := json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	// Land the seeded text as the FORMAL version: the gate compares against
	// the current chapter version, so the value has to live there first.
	if _, _, _, err = svc.CommitWorkingCopy(ctx, actor, project.ID, chapterID, "condition-seed-commit", CommitWorkingCopyInput{
		ExpectedChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
	}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	if copy, err = svc.GetWorkingCopy(ctx, actor, project.ID, chapterID); err != nil {
		t.Fatal(err)
	}
	return svc, actor, project, chapterID, copy
}

func TestWordingCommitFlagsAVanishedConditionValue(t *testing.T) {
	svc, actor, project, chapterID, copy := conditionFixture(t)
	ctx := context.Background()
	// Rephrase the text so the registered value no longer occurs in it.
	raw, _, _, err := svc.SaveWorkingCopy(ctx, actor, project.ID, chapterID, "condition-drop-value", SaveWorkingCopyInput{
		BaseChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
		BodyMarkdown: "本课题围绕新的表述展开，方法与结论不变。", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = svc.CommitWorkingCopy(ctx, actor, project.ID, chapterID, "condition-commit-refused", CommitWorkingCopyInput{
		ExpectedChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
	})
	var refused conditionChangeConfirmationRequiredError
	if !errors.As(err, &refused) {
		t.Fatalf("a vanished condition value must refuse the wording commit, got %v", err)
	}
	form := refused.ClientForm()
	if form.Status != 422 || form.Code != "condition_change_confirmation_required" {
		t.Fatalf("form = %+v", form)
	}
	details, _ := form.Details.(map[string]any)
	suspected, _ := details["suspected_condition_changes"].([]SuspectedConditionChange)
	if len(suspected) != 1 || suspected[0].FieldID != "research_subject" || suspected[0].Value != conditionFixtureValue {
		t.Fatalf("suspected = %+v details = %+v", suspected, details)
	}
	// The refusal left nothing behind: the chapter still has its old version.
	chapters, listErr := svc.ListChapters(ctx, actor, project.ID)
	if listErr != nil || chapters[0].CurrentVersionID == nil || *chapters[0].CurrentVersionID != *copy.BaseChapterVersionID {
		t.Fatalf("a refused commit changed the chapter: %+v %v", chapters[0], listErr)
	}
}

func TestWordingCommitAcceptsAnExplicitDismissalAndAuditsIt(t *testing.T) {
	svc, actor, project, chapterID, copy := conditionFixture(t)
	ctx := context.Background()
	raw, _, _, err := svc.SaveWorkingCopy(ctx, actor, project.ID, chapterID, "condition-drop-value", SaveWorkingCopyInput{
		BaseChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
		BodyMarkdown: "本课题围绕新的表述展开，方法与结论不变。", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	committed, status, _, err := svc.CommitWorkingCopy(ctx, actor, project.ID, chapterID, "condition-commit-dismissed", CommitWorkingCopyInput{
		ExpectedChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
		ConditionChange: &ConditionChangeDismissal{
			DismissedFields: []string{"research_subject"}, Reason: "仅改写表述，条件未变",
		},
	})
	if err != nil || status != 201 {
		t.Fatalf("dismissed commit: status=%d err=%v", status, err)
	}
	if len(committed) == 0 {
		t.Fatal("empty committed payload")
	}
	events, err := svc.ListAuditEvents(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Action != "chapter.commit.condition_dismissed" || event.Target != chapterID {
			continue
		}
		found = true
		fields, _ := event.Details["fields"].([]any)
		if len(fields) != 1 || fields[0] != "research_subject" {
			t.Fatalf("audit fields = %+v", event.Details)
		}
	}
	if !found {
		t.Fatal("the dismissal was not audited")
	}
}

func TestWordingCommitDismissalMustNameTheCurrentSuspicions(t *testing.T) {
	svc, actor, project, chapterID, copy := conditionFixture(t)
	ctx := context.Background()
	raw, _, _, err := svc.SaveWorkingCopy(ctx, actor, project.ID, chapterID, "condition-drop-value", SaveWorkingCopyInput{
		BaseChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
		BodyMarkdown: "本课题围绕新的表述展开，方法与结论不变。", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = svc.CommitWorkingCopy(ctx, actor, project.ID, chapterID, "condition-commit-stale", CommitWorkingCopyInput{
		ExpectedChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
		ConditionChange: &ConditionChangeDismissal{DismissedFields: []string{"never_mentioned"}, Reason: "确认的清单不是看到的清单"},
	})
	if !errors.Is(err, ErrConditionChangeDismissalStale) {
		t.Fatalf("a dismissal naming other fields must be refused, got %v", err)
	}
}

func TestWordingCommitDismissalRequiresAReason(t *testing.T) {
	svc, actor, project, chapterID, copy := conditionFixture(t)
	ctx := context.Background()
	raw, _, _, err := svc.SaveWorkingCopy(ctx, actor, project.ID, chapterID, "condition-drop-value", SaveWorkingCopyInput{
		BaseChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
		BodyMarkdown: "本课题围绕新的表述展开，方法与结论不变。", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = svc.CommitWorkingCopy(ctx, actor, project.ID, chapterID, "condition-commit-no-reason", CommitWorkingCopyInput{
		ExpectedChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
		ConditionChange: &ConditionChangeDismissal{DismissedFields: []string{"research_subject"}},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("a reasonless dismissal must be refused, got %v", err)
	}
}

// The pure boundaries: the gate only looks at values long enough to be
// unambiguous and only at values the old text actually carried.
func TestSuspectedConditionChangeBoundaries(t *testing.T) {
	spec := map[string]string{
		"gated":     "旧研究主题值",
		"too_short": "是",
		"absent":    absentConditionValue,
	}
	old := "正文包含旧研究主题值，也包含是。"
	if got := suspectedConditionChanges(spec, old, "正文只剩是。"); len(got) != 1 || got[0].FieldID != "gated" {
		t.Fatalf("suspected = %+v", got)
	}
	if got := suspectedConditionChanges(spec, old, "正文保留旧研究主题值，也包含是。"); len(got) != 0 {
		t.Fatalf("a kept value must not gate: %+v", got)
	}
	if got := suspectedConditionChanges(spec, "正文不包含任何登记值。", "正文不包含任何登记值。"); len(got) != 0 {
		t.Fatalf("values absent from the old text never gate: %+v", got)
	}
}

func TestWordingCommitIgnoresShortAndAbsentValues(t *testing.T) {
	svc, actor, project, chapterID, copy := conditionFixture(t)
	ctx := context.Background()
	// The registered long value STAYS; only the short flag's occurrence is
	// gone and the never-mentioned value remains absent — neither gates.
	raw, status, _, err := svc.CommitWorkingCopy(ctx, actor, project.ID, chapterID, "condition-commit-clean", CommitWorkingCopyInput{
		ExpectedChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
	})
	if err != nil || status != 201 {
		t.Fatalf("a wording commit that keeps every gating value must pass: status=%d err=%v", status, err)
	}
	if len(raw) == 0 {
		t.Fatal("empty committed payload")
	}
}

func TestWordingCommitWithUnrelatedRephraseKeepsTheValue(t *testing.T) {
	svc, actor, project, chapterID, copy := conditionFixture(t)
	ctx := context.Background()
	raw, _, _, err := svc.SaveWorkingCopy(ctx, actor, project.ID, chapterID, "condition-rephrase", SaveWorkingCopyInput{
		BaseChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
		BodyMarkdown: "本课题自始至终围绕" + conditionFixtureValue + "展开（改写后的表述）。", SourceIDs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	if _, status, _, err := svc.CommitWorkingCopy(ctx, actor, project.ID, chapterID, "condition-commit-rephrased", CommitWorkingCopyInput{
		ExpectedChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
	}); err != nil || status != 201 {
		t.Fatalf("a rephrase that keeps the value must pass: status err=%v", err)
	}
}
