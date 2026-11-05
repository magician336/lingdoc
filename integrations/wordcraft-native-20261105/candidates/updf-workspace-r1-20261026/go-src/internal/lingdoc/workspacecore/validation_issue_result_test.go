package workspacecore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidationIssueDispositionTypedResultsPreserveFoundAndReplay(t *testing.T) {
	ctx := context.Background()
	service := testStore(t, filepath.Join(t.TempDir(), "validation-issue-result.db"))
	actor := Actor{TenantID: 501, UserID: "owner"}
	project := seedActiveImpactProject(t, service, actor)
	binding := issueBinding(project.ProjectVersion, "warning")
	input := ValidationIssueDispositionInput{
		ExpectedProjectVersion: project.ProjectVersion,
		ValidationIssueBinding: binding,
		Action:                 "dismiss",
		Reason:                 "该规则不适用于当前章节",
	}

	first, err := service.RecordValidationIssueDispositionResult(ctx, actor, project.ID, "validation-result-1", input)
	if err != nil {
		t.Fatalf("record disposition: %v", err)
	}
	if first.Replayed || first.Disposition.IssueID != binding.IssueID || first.Disposition.Action != input.Action {
		t.Fatalf("first result = %+v, want typed value with replay=false", first)
	}

	recordedReplay, err := service.RecordValidationIssueDispositionResult(ctx, actor, project.ID, "validation-result-1", input)
	if err != nil {
		t.Fatalf("record replay: %v", err)
	}
	if !recordedReplay.Replayed || recordedReplay.Disposition.IssueID != first.Disposition.IssueID {
		t.Fatalf("record replay = %+v, want same typed value with replay=true", recordedReplay)
	}

	missing, err := service.ReplayValidationIssueDispositionResult(ctx, actor, project.ID, binding.IssueID, "validation-result-missing", input.Action, input.Reason, project.ProjectVersion)
	if err != nil {
		t.Fatalf("missing replay: %v", err)
	}
	if missing.Found || missing.Replayed || missing.Disposition.IssueID != "" {
		t.Fatalf("missing replay = %+v, want found=false and replay=false", missing)
	}

	replayed, err := service.ReplayValidationIssueDispositionResult(ctx, actor, project.ID, binding.IssueID, "validation-result-1", input.Action, input.Reason, project.ProjectVersion)
	if err != nil {
		t.Fatalf("found replay: %v", err)
	}
	if !replayed.Found || !replayed.Replayed || replayed.Disposition.IssueID != first.Disposition.IssueID || replayed.Disposition.Reason != first.Disposition.Reason {
		t.Fatalf("found replay = %+v, want found=true, replay=true and same value", replayed)
	}
}

func TestValidationIssueDispositionTypedResultsPreserveDomainDecodeErrors(t *testing.T) {
	ctx := context.Background()
	service := testStore(t, filepath.Join(t.TempDir(), "validation-issue-malformed.db"))
	actor := Actor{TenantID: 502, UserID: "owner"}
	project := seedActiveImpactProject(t, service, actor)
	binding := issueBinding(project.ProjectVersion, "warning")
	input := ValidationIssueDispositionInput{
		ExpectedProjectVersion: project.ProjectVersion,
		ValidationIssueBinding: binding,
		Action:                 "dismiss",
		Reason:                 "该规则不适用于当前章节",
	}

	if _, err := service.RecordValidationIssueDispositionResult(ctx, actor, project.ID, "validation-malformed-record", input); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	corruptOperationResult(t, service, actor, project.ID+"/"+binding.IssueID, "validation-malformed-record")
	recordResult, err := service.RecordValidationIssueDispositionResult(ctx, actor, project.ID, "validation-malformed-record", input)
	if err == nil || !strings.Contains(err.Error(), "decode issue disposition result") {
		t.Fatalf("malformed record error = %v, want domain decode label", err)
	}
	if !recordResult.Replayed {
		t.Fatalf("malformed record result = %+v, want replay=true propagated with decode error", recordResult)
	}

	replayBinding := binding
	replayBinding.IssueID = "issue-2"
	replayInput := input
	replayInput.ValidationIssueBinding = replayBinding
	if _, err := service.RecordValidationIssueDispositionResult(ctx, actor, project.ID, "validation-malformed-replay", replayInput); err != nil {
		t.Fatalf("seed replay record: %v", err)
	}
	corruptOperationResult(t, service, actor, project.ID+"/"+replayBinding.IssueID, "validation-malformed-replay")
	replayResult, err := service.ReplayValidationIssueDispositionResult(ctx, actor, project.ID, replayBinding.IssueID, "validation-malformed-replay", replayInput.Action, replayInput.Reason, project.ProjectVersion)
	if err == nil || !strings.Contains(err.Error(), "decode replayed issue disposition") {
		t.Fatalf("malformed replay error = %v, want domain decode label", err)
	}
	if !replayResult.Found || replayResult.Replayed {
		t.Fatalf("malformed replay result = %+v, want found=true and replay=false on failed decode", replayResult)
	}
}

func TestValidationIssueDispositionTypedResultsPreserveIdempotencyErrors(t *testing.T) {
	ctx := context.Background()
	service := testStore(t, filepath.Join(t.TempDir(), "validation-issue-errors.db"))
	actor := Actor{TenantID: 503, UserID: "owner"}
	project := seedActiveImpactProject(t, service, actor)
	binding := issueBinding(project.ProjectVersion, "warning")
	input := ValidationIssueDispositionInput{
		ExpectedProjectVersion: project.ProjectVersion,
		ValidationIssueBinding: binding,
		Action:                 "dismiss",
		Reason:                 "该规则不适用于当前章节",
	}
	if _, err := service.RecordValidationIssueDispositionResult(ctx, actor, project.ID, "validation-conflict", input); err != nil {
		t.Fatalf("seed disposition: %v", err)
	}

	conflictingInput := input
	conflictingInput.Reason = "不同的处置理由"
	if _, err := service.RecordValidationIssueDispositionResult(ctx, actor, project.ID, "validation-conflict", conflictingInput); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("record idempotency error = %v, want ErrIdempotencyConflict", err)
	}
	if _, err := service.ReplayValidationIssueDispositionResult(ctx, actor, project.ID, binding.IssueID, "validation-conflict", "resolve", input.Reason, project.ProjectVersion); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("replay idempotency error = %v, want ErrIdempotencyConflict", err)
	}
}

func corruptOperationResult(t *testing.T, service *Service, actor Actor, target, key string) {
	t.Helper()
	err := service.testDB().Table("lingdoc_operations").Where(
		"tenant_id = ? AND user_id = ? AND operation = ? AND target = ? AND key = ?",
		actor.TenantID, actor.UserID, "validationIssueDisposition", target, key,
	).Update("response_json", "{").Error
	if err != nil {
		t.Fatalf("corrupt operation result: %v", err)
	}
}
