package workspacecore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestStandardMutationResultFamiliesPreserveTypedValueAndReplay(t *testing.T) {
	ctx := context.Background()

	t.Run("chapter", func(t *testing.T) {
		svc := testStore(t, filepath.Join(t.TempDir(), "chapter.db"))
		actor := Actor{TenantID: 401, UserID: "owner"}
		project := seedActiveG3Project(t, svc, actor)
		chapters, err := svc.ListChapters(ctx, actor, project.ID)
		if err != nil || len(chapters) == 0 {
			t.Fatalf("chapters: %v %+v", err, chapters)
		}
		input := SaveChapterInput{ExpectedSpecRevision: project.SpecRevision, BodyMarkdown: "typed chapter", SourceIDs: []string{}}
		first, err := svc.SaveChapterResult(ctx, actor, project.ID, chapters[0].ID, "typed-chapter", input)
		if err != nil {
			t.Fatalf("typed chapter: %v", err)
		}
		if first.Replayed || first.Chapter.ID != chapters[0].ID || first.Chapter.BodyMarkdown != input.BodyMarkdown {
			t.Fatalf("typed chapter result = %+v, want first typed value", first)
		}
		replayed, err := svc.SaveChapterResult(ctx, actor, project.ID, chapters[0].ID, "typed-chapter", input)
		if err != nil {
			t.Fatalf("typed chapter replay: %v", err)
		}
		if !replayed.Replayed || replayed.Chapter.ID != first.Chapter.ID || replayed.Chapter.BodyMarkdown != first.Chapter.BodyMarkdown {
			t.Fatalf("typed chapter replay = %+v, want same value with replay=true", replayed)
		}
	})

	t.Run("owner transfer", func(t *testing.T) {
		svc := testStore(t, filepath.Join(t.TempDir(), "owner-transfer.db"))
		actor := Actor{TenantID: 402, UserID: "owner"}
		project := seedActiveG3Project(t, svc, actor)
		recipient := Actor{TenantID: actor.TenantID, UserID: "recipient"}
		seedTenantMember(t, svc, recipient)
		input := OwnerTransferInput{ExpectedProjectVersion: project.ProjectVersion, ToUserID: recipient.UserID}
		first, err := svc.RequestOwnerTransferResult(ctx, actor, project.ID, "typed-owner-transfer", input)
		if err != nil {
			t.Fatalf("typed owner transfer: %v", err)
		}
		if first.Replayed || first.Transfer.ProjectID != project.ID || first.Transfer.ToUserID != recipient.UserID || first.Transfer.Status != "pending" {
			t.Fatalf("typed owner transfer result = %+v, want pending transfer", first)
		}
		replayed, err := svc.RequestOwnerTransferResult(ctx, actor, project.ID, "typed-owner-transfer", input)
		if err != nil {
			t.Fatalf("typed owner transfer replay: %v", err)
		}
		if !replayed.Replayed || replayed.Transfer.ID != first.Transfer.ID || replayed.Transfer.Status != first.Transfer.Status {
			t.Fatalf("typed owner transfer replay = %+v, want same value with replay=true", replayed)
		}
	})

	t.Run("change set", func(t *testing.T) {
		svc := testStore(t, filepath.Join(t.TempDir(), "change-set.db"))
		actor := Actor{TenantID: 403, UserID: "owner"}
		project := seedActiveG3Project(t, svc, actor)
		chapters, err := svc.ListChapters(ctx, actor, project.ID)
		if err != nil || len(chapters) == 0 {
			t.Fatalf("chapters: %v %+v", err, chapters)
		}
		input := CreateChangeSetInput{
			ExpectedContextRevision: project.CurrentContextRevision,
			Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
			AffectedChapterIDs:      []string{chapters[0].ID},
			Reason:                  "typed change set",
		}
		first, err := svc.CreateChangeSetResult(ctx, actor, project.ID, "typed-change-set", input)
		if err != nil {
			t.Fatalf("typed change set: %v", err)
		}
		if first.Replayed || first.ChangeSet.Status != "assessed" || first.ChangeSet.ProjectID != project.ID {
			t.Fatalf("typed change set result = %+v, want assessed change set", first)
		}
		replayed, err := svc.CreateChangeSetResult(ctx, actor, project.ID, "typed-change-set", input)
		if err != nil {
			t.Fatalf("typed change set replay: %v", err)
		}
		if !replayed.Replayed || replayed.ChangeSet.ID != first.ChangeSet.ID || replayed.ChangeSet.Status != first.ChangeSet.Status {
			t.Fatalf("typed change set replay = %+v, want same value with replay=true", replayed)
		}

		applied, err := svc.ApplyChangeSetResult(ctx, actor, project.ID, first.ChangeSet.ID, "typed-change-set-apply")
		if err != nil {
			t.Fatalf("typed apply change set: %v", err)
		}
		if applied.Replayed || applied.ChangeSet.Status != "applied" {
			t.Fatalf("typed apply result = %+v, want applied change set", applied)
		}
		appliedReplay, err := svc.ApplyChangeSetResult(ctx, actor, project.ID, first.ChangeSet.ID, "typed-change-set-apply")
		if err != nil {
			t.Fatalf("typed apply replay: %v", err)
		}
		if !appliedReplay.Replayed || appliedReplay.ChangeSet.ID != applied.ChangeSet.ID || appliedReplay.ChangeSet.Status != "applied" {
			t.Fatalf("typed apply replay = %+v, want same value with replay=true", appliedReplay)
		}
	})

	t.Run("draft candidate", func(t *testing.T) {
		svc := testStore(t, filepath.Join(t.TempDir(), "draft-candidate.db"))
		actor := Actor{TenantID: 404, UserID: "owner"}
		seedTenantMember(t, svc, actor)
		raw, _, _, err := svc.CreateProject(ctx, actor, "typed-candidate-project", CreateProjectInput{Name: "候选项目", TemplateID: "template-demo"})
		if err != nil {
			t.Fatalf("create draft project: %v", err)
		}
		project := asProject(t, raw)
		input := DraftCandidateInput{Kind: "retrieval", Title: "typed candidate", Content: "candidate body", Level: "background"}
		first, err := svc.CreateDraftCandidateResult(ctx, actor, project.ID, "typed-draft-candidate", input)
		if err != nil {
			t.Fatalf("typed draft candidate: %v", err)
		}
		if first.Replayed || first.Candidate.ProjectID != project.ID || first.Candidate.Title != input.Title || first.Candidate.Content != input.Content {
			t.Fatalf("typed draft candidate result = %+v, want typed candidate", first)
		}
		replayed, err := svc.CreateDraftCandidateResult(ctx, actor, project.ID, "typed-draft-candidate", input)
		if err != nil {
			t.Fatalf("typed draft candidate replay: %v", err)
		}
		if !replayed.Replayed || replayed.Candidate.ID != first.Candidate.ID || replayed.Candidate.Content != first.Candidate.Content {
			t.Fatalf("typed draft candidate replay = %+v, want same value with replay=true", replayed)
		}
	})
}

func TestApplyChangeSetResultPreservesStaleVersionConflict(t *testing.T) {
	ctx := context.Background()
	svc := testStore(t, filepath.Join(t.TempDir(), "stale-change-set.db"))
	actor := Actor{TenantID: 405, UserID: "owner"}
	project := seedActiveG3Project(t, svc, actor)
	chapters, err := svc.ListChapters(ctx, actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	makeInput := func(value string) CreateChangeSetInput {
		return CreateChangeSetInput{
			ExpectedContextRevision: project.CurrentContextRevision,
			Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: value}},
			AffectedChapterIDs:      []string{chapters[0].ID},
			Reason:                  "stale typed change set",
		}
	}
	first, err := svc.CreateChangeSetResult(ctx, actor, project.ID, "typed-stale-first", makeInput("目标一"))
	if err != nil {
		t.Fatalf("first typed change set: %v", err)
	}
	second, err := svc.CreateChangeSetResult(ctx, actor, project.ID, "typed-stale-second", makeInput("目标二"))
	if err != nil {
		t.Fatalf("second typed change set: %v", err)
	}
	if _, err := svc.ApplyChangeSetResult(ctx, actor, project.ID, first.ChangeSet.ID, "typed-stale-apply-first"); err != nil {
		t.Fatalf("first typed apply: %v", err)
	}
	if _, err := svc.ApplyChangeSetResult(ctx, actor, project.ID, second.ChangeSet.ID, "typed-stale-apply-second"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale typed apply error = %v, want ErrVersionConflict", err)
	}
	if _, err := svc.ApplyChangeSetResult(ctx, actor, project.ID, second.ChangeSet.ID, "typed-stale-apply-second"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale typed replay error = %v, want ErrVersionConflict", err)
	}
}
