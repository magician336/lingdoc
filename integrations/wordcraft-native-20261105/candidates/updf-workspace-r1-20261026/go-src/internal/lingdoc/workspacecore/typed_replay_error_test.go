package workspacecore

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTypedWorkingCopyMutationsPreserveReplayMetadataOnSourcePayloadDecodeErrors(t *testing.T) {
	t.Run("apply rewrite", func(t *testing.T) {
		service, actor, projectID, chapterID, _ := operationSourceFixture(t)
		input := SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1, BodyMarkdown: "rewritten", SourceIDs: []string{}}
		reviews := []ReviewItem{{ID: "review-replay", Statement: "verify", OriginCandidateID: "candidate"}}
		if _, err := service.ApplyRewriteResult(context.Background(), actor, projectID, chapterID, "apply-replay-error", input, reviews); err != nil {
			t.Fatalf("seed apply rewrite: %v", err)
		}
		corruptTypedMutationReplay(t, service, actor, "applySelectedRewrite", projectID+"/"+chapterID, "apply-replay-error")
		result, err := service.ApplyRewriteResult(context.Background(), actor, projectID, chapterID, "apply-replay-error", input, reviews)
		assertReplayPayloadDecodeError(t, result.Replayed, err, "working copy mutation result")
	})

	t.Run("commit", func(t *testing.T) {
		service, actor, projectID, chapterID, _ := operationSourceFixture(t)
		input := CommitWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1}
		if _, err := service.CommitWorkingCopyResult(context.Background(), actor, projectID, chapterID, "commit-replay-error", input); err != nil {
			t.Fatalf("seed commit: %v", err)
		}
		corruptTypedMutationReplay(t, service, actor, "commitWorkingCopy", projectID+"/"+chapterID, "commit-replay-error")
		result, err := service.CommitWorkingCopyResult(context.Background(), actor, projectID, chapterID, "commit-replay-error", input)
		assertReplayPayloadDecodeError(t, result.Replayed, err, "committed chapter result")
	})

	t.Run("restore", func(t *testing.T) {
		service, actor, projectID, chapterID, _ := operationSourceFixture(t)
		commitInput := CommitWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1}
		committed, err := service.CommitWorkingCopyResult(context.Background(), actor, projectID, chapterID, "restore-seed-commit", commitInput)
		if err != nil {
			t.Fatalf("seed restore commit: %v", err)
		}
		versionID := committed.Version.ChapterVersionID
		restoreInput := RestoreWorkingCopyInput{ChapterVersionID: versionID, ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 2, ExpectedChapterVersionID: &versionID}
		if _, err := service.RestoreWorkingCopyResult(context.Background(), actor, projectID, chapterID, "restore-replay-error", restoreInput); err != nil {
			t.Fatalf("seed restore: %v", err)
		}
		corruptTypedMutationReplay(t, service, actor, "restoreWorkingCopy", projectID+"/"+chapterID, "restore-replay-error")
		result, err := service.RestoreWorkingCopyResult(context.Background(), actor, projectID, chapterID, "restore-replay-error", restoreInput)
		assertReplayPayloadDecodeError(t, result.Replayed, err, "working copy mutation result")
	})
}

func TestTypedWorkingCopyMutationOrdinaryErrorsDoNotClaimReplay(t *testing.T) {
	service, actor, projectID, chapterID, _ := operationSourceFixture(t)
	input := SaveWorkingCopyInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 2, BodyMarkdown: "stale", SourceIDs: []string{}}
	result, err := service.SaveWorkingCopyResult(context.Background(), actor, projectID, chapterID, "ordinary-version-error", input)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("ordinary source-sensitive error = %v, want ErrVersionConflict", err)
	}
	if result.Replayed {
		t.Fatalf("ordinary source-sensitive error result = %+v, want replay=false", result)
	}
}

func corruptTypedMutationReplay(t *testing.T, service *Service, actor Actor, operation, target, key string) {
	t.Helper()
	if err := service.testDB().Table("lingdoc_operations").Where(
		"tenant_id = ? AND user_id = ? AND operation = ? AND target = ? AND key = ?",
		actor.TenantID, actor.UserID, operation, target, key,
	).Update("response_json", "{").Error; err != nil {
		t.Fatalf("corrupt %s replay: %v", operation, err)
	}
}

func assertReplayPayloadDecodeError(t *testing.T, replayed bool, err error, label string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "decode "+label) {
		t.Fatalf("replay error = %v, want decode %s", err, label)
	}
	if !replayed {
		t.Fatalf("replay metadata = false for decode %s error, want true", label)
	}
}
