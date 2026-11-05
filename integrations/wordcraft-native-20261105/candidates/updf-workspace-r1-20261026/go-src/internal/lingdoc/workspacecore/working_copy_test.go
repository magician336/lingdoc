package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

type workingCopyMutationApplication interface {
	SaveWorkingCopyResult(context.Context, Actor, string, string, string, SaveWorkingCopyInput) (WorkingCopyMutationResult, error)
	ApplyRewriteResult(context.Context, Actor, string, string, string, SaveWorkingCopyInput, []ReviewItem) (WorkingCopyMutationResult, error)
	CommitWorkingCopyResult(context.Context, Actor, string, string, string, CommitWorkingCopyInput) (CommittedChapterMutationResult, error)
	RestoreWorkingCopyResult(context.Context, Actor, string, string, string, RestoreWorkingCopyInput) (WorkingCopyMutationResult, error)
}

func TestTypedWorkingCopyMutationsPreserveFieldsReplayAndErrors(t *testing.T) {
	ctx := context.Background()
	repository := fakeWorkspace()
	var application workingCopyMutationApplication = NewService(repository)
	actor := Actor{TenantID: 1, UserID: "owner"}

	saveInput := SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1, BodyMarkdown: "first draft", SourceIDs: []string{}}
	saved, err := application.SaveWorkingCopyResult(ctx, actor, "p", "c", "typed-save", saveInput)
	if err != nil {
		t.Fatalf("typed save: %v", err)
	}
	if saved.Replayed || saved.WorkingCopy.WorkingCopyRevision != 2 || saved.WorkingCopy.BodyMarkdown != saveInput.BodyMarkdown || len(saved.WorkingCopy.ReviewItems) != 1 {
		t.Fatalf("typed save = %+v, want updated working copy with server review items", saved)
	}
	savedReplay, err := application.SaveWorkingCopyResult(ctx, actor, "p", "c", "typed-save", saveInput)
	if err != nil || !savedReplay.Replayed || savedReplay.WorkingCopy.WorkingCopyRevision != saved.WorkingCopy.WorkingCopyRevision {
		t.Fatalf("typed save replay = %+v, err=%v", savedReplay, err)
	}

	rewriteInput := SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 2, BodyMarkdown: "rewritten draft", SourceIDs: []string{}}
	review := []ReviewItem{{ID: "rewrite-review", Statement: "verify rewrite", OriginCandidateID: "candidate-1"}}
	applied, err := application.ApplyRewriteResult(ctx, actor, "p", "c", "typed-apply", rewriteInput, review)
	if err != nil {
		t.Fatalf("typed apply rewrite: %v", err)
	}
	if applied.Replayed || applied.WorkingCopy.WorkingCopyRevision != 3 || applied.WorkingCopy.BodyMarkdown != rewriteInput.BodyMarkdown || len(applied.WorkingCopy.ReviewItems) != 2 || applied.WorkingCopy.ReviewItems[1].ID != review[0].ID {
		t.Fatalf("typed apply rewrite = %+v, want merged server review items", applied)
	}
	appliedReplay, err := application.ApplyRewriteResult(ctx, actor, "p", "c", "typed-apply", rewriteInput, review)
	if err != nil || !appliedReplay.Replayed || appliedReplay.WorkingCopy.WorkingCopyRevision != applied.WorkingCopy.WorkingCopyRevision {
		t.Fatalf("typed apply replay = %+v, err=%v", appliedReplay, err)
	}

	commitInput := CommitWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 3}
	committed, err := application.CommitWorkingCopyResult(ctx, actor, "p", "c", "typed-commit", commitInput)
	if err != nil {
		t.Fatalf("typed commit: %v", err)
	}
	if committed.Replayed || committed.Version.ChapterVersionID == "" || committed.Version.ParentChapterVersionID != nil || committed.Version.CommittedWorkingCopyRevision != 3 || committed.Version.NextWorkingCopyRevision != 4 || committed.Version.BodyMarkdown != rewriteInput.BodyMarkdown || len(committed.Version.ReviewItems) != 2 {
		t.Fatalf("typed commit = %+v, want committed chapter lineage and review items", committed)
	}
	committedReplay, err := application.CommitWorkingCopyResult(ctx, actor, "p", "c", "typed-commit", commitInput)
	if err != nil || !committedReplay.Replayed || committedReplay.Version.ChapterVersionID != committed.Version.ChapterVersionID {
		t.Fatalf("typed commit replay = %+v, err=%v", committedReplay, err)
	}

	versionID := committed.Version.ChapterVersionID
	restoreInput := RestoreWorkingCopyInput{ChapterVersionID: versionID, ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 4, ExpectedChapterVersionID: &versionID}
	restored, err := application.RestoreWorkingCopyResult(ctx, actor, "p", "c", "typed-restore", restoreInput)
	if err != nil {
		t.Fatalf("typed restore: %v", err)
	}
	if restored.Replayed || restored.WorkingCopy.WorkingCopyRevision != 5 || restored.WorkingCopy.BodyMarkdown != rewriteInput.BodyMarkdown || restored.WorkingCopy.BaseChapterVersionID == nil || *restored.WorkingCopy.BaseChapterVersionID != versionID || len(restored.WorkingCopy.ReviewItems) != 2 {
		t.Fatalf("typed restore = %+v, want restored working copy and lineage", restored)
	}
	restoredReplay, err := application.RestoreWorkingCopyResult(ctx, actor, "p", "c", "typed-restore", restoreInput)
	if err != nil || !restoredReplay.Replayed || restoredReplay.WorkingCopy.WorkingCopyRevision != restored.WorkingCopy.WorkingCopyRevision {
		t.Fatalf("typed restore replay = %+v, err=%v", restoredReplay, err)
	}

	if _, err := application.SaveWorkingCopyResult(ctx, actor, "p", "c", "typed-save-stale", saveInput); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("typed stale save = %v, want ErrVersionConflict", err)
	}
	if _, err := application.ApplyRewriteResult(ctx, actor, "p", "c", "typed-apply-stale", rewriteInput, review); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("typed stale apply = %v, want ErrVersionConflict", err)
	}
}

func TestWorkingCopySaveCommitHistoryRestoreAndReplay(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "working-copy.db")
	service := testStore(t, path)
	actor := Actor{TenantID: 29, UserID: "writer"}
	seedTenantMember(t, service, actor)
	raw, _, _, err := service.CreateProject(ctx, actor, "create-workcopy", CreateProjectInput{Name: "工作副本测试", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, raw)
	if _, _, _, err := service.SaveSpec(ctx, actor, project.ID, "spec-workcopy", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "synthetic", "research_goal": "test"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := service.ActivateProject(ctx, actor, project.ID, "activate-workcopy", ActivateProjectInput{ExpectedSpecRevision: 1, ExpectedProjectVersion: 2, ReviewedProjectVersion: 2}); err != nil {
		t.Fatal(err)
	}
	chapters, err := service.ListChapters(ctx, actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %d %v", len(chapters), err)
	}
	chapterID := chapters[0].ID
	workingCopy, err := service.GetWorkingCopy(ctx, actor, project.ID, chapterID)
	if err != nil || workingCopy.WorkingCopyRevision != 1 || workingCopy.SpecRevision != 1 || workingCopy.SourceIDs == nil || workingCopy.ReviewItems == nil {
		t.Fatalf("initial working copy: %+v %v", workingCopy, err)
	}

	save := SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
		BodyMarkdown: "第一稿 [[source:source-one]]", SourceIDs: []string{"source-one"}}
	savedRaw, status, replayed, err := service.SaveWorkingCopy(ctx, actor, project.ID, chapterID, "save-workcopy-001", save)
	if err != nil || status != 200 || replayed {
		t.Fatalf("save working copy: status=%d replay=%v err=%v", status, replayed, err)
	}
	var saved WorkingCopy
	if err := json.Unmarshal(savedRaw, &saved); err != nil || saved.WorkingCopyRevision != 2 || saved.BodyMarkdown != save.BodyMarkdown {
		t.Fatalf("saved working copy: %+v %v", saved, err)
	}
	replayedRaw, _, replayed, err := service.SaveWorkingCopy(ctx, actor, project.ID, chapterID, "save-workcopy-001", save)
	if err != nil || !replayed || string(replayedRaw) != string(savedRaw) {
		t.Fatalf("lost-response replay: replay=%v err=%v", replayed, err)
	}
	if _, _, _, err := service.SaveWorkingCopy(ctx, actor, project.ID, chapterID, "save-workcopy-stale", save); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale working-copy revision accepted: %v", err)
	}
	var formalCount int64
	if err := service.repository.(*GORMRepository).db.Model(&chapterVersionRow{}).Where("chapter_id = ?", chapterID).Count(&formalCount).Error; err != nil || formalCount != 0 {
		t.Fatalf("autosave created formal version: count=%d err=%v", formalCount, err)
	}

	commit := CommitWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 2}
	committedRaw, status, replayed, err := service.CommitWorkingCopy(ctx, actor, project.ID, chapterID, "commit-workcopy-001", commit)
	if err != nil || status != 201 || replayed {
		t.Fatalf("commit: status=%d replay=%v err=%v", status, replayed, err)
	}
	var first CommittedChapterVersion
	if err := json.Unmarshal(committedRaw, &first); err != nil || first.ChapterVersionID == "" || first.ParentChapterVersionID != nil || first.CommittedWorkingCopyRevision != 2 || first.NextWorkingCopyRevision != 3 {
		t.Fatalf("commit response: %+v %v", first, err)
	}
	commit.ExpectedChapterVersionID = &first.ChapterVersionID
	commit.ExpectedWorkingCopyRevision = 3
	if _, _, _, err := service.SaveWorkingCopy(ctx, actor, project.ID, chapterID, "save-workcopy-002", SaveWorkingCopyInput{
		BaseChapterVersionID: &first.ChapterVersionID, ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 3,
		BodyMarkdown: "第二稿", SourceIDs: []string{},
	}); err != nil {
		t.Fatalf("second draft save: %v", err)
	}
	commit.ExpectedWorkingCopyRevision = 4
	secondRaw, _, _, err := service.CommitWorkingCopy(ctx, actor, project.ID, chapterID, "commit-workcopy-002", commit)
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}
	var second CommittedChapterVersion
	if err := json.Unmarshal(secondRaw, &second); err != nil || second.ParentChapterVersionID == nil || *second.ParentChapterVersionID != first.ChapterVersionID {
		t.Fatalf("second version lineage: %+v %v", second, err)
	}

	versions, err := service.ListChapterVersions(ctx, actor, project.ID, chapterID)
	if err != nil || len(versions) != 2 || versions[0].ID != second.ChapterVersionID || versions[1].ID != first.ChapterVersionID {
		t.Fatalf("version history: %+v %v", versions, err)
	}
	workingCopy, err = service.GetWorkingCopy(ctx, actor, project.ID, chapterID)
	if err != nil || workingCopy.WorkingCopyRevision != 5 || workingCopy.BaseChapterVersionID == nil || *workingCopy.BaseChapterVersionID != second.ChapterVersionID {
		t.Fatalf("current copy after commit: %+v %v", workingCopy, err)
	}
	restore := RestoreWorkingCopyInput{ChapterVersionID: first.ChapterVersionID, ExpectedSpecRevision: 1,
		ExpectedWorkingCopyRevision: 5, ExpectedChapterVersionID: &second.ChapterVersionID}
	restoredRaw, status, replayed, err := service.RestoreWorkingCopy(ctx, actor, project.ID, chapterID, "restore-workcopy-001", restore)
	if err != nil || status != 200 || replayed {
		t.Fatalf("restore historical draft: status=%d replay=%v err=%v", status, replayed, err)
	}
	var restored WorkingCopy
	if err := json.Unmarshal(restoredRaw, &restored); err != nil || restored.WorkingCopyRevision != 6 || restored.BodyMarkdown != first.BodyMarkdown || restored.BaseChapterVersionID == nil || *restored.BaseChapterVersionID != second.ChapterVersionID {
		t.Fatalf("restored working copy: %+v %v", restored, err)
	}
	if err := service.repository.(*GORMRepository).db.Model(&chapterVersionRow{}).Where("chapter_id = ?", chapterID).Count(&formalCount).Error; err != nil || formalCount != 2 {
		t.Fatalf("history restore mutated formal versions: count=%d err=%v", formalCount, err)
	}
}
