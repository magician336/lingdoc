package workspacecore

import (
	"context"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
)

var ErrHumanDecisionRequired = clienterror.New(clienterror.Form{Status: 403, Code: "human_decision_required", Message: "此操作需要实际成员本人确认。", Retryable: false}, "human_decision_required")

// This is an identity check, never a client-supplied confirmed flag. Existing
// trusted domain fixtures without a HTTP Principal remain explicit fixtures.
//
// The check itself belongs to the shared pipeline: the same check everywhere,
// so a call that reaches the gate through the pipeline and a call that reaches
// it directly cannot disagree.
//
// Which operations need the gate is this domain's policy, so the list lives
// here rather than in the pipeline: the research domain decides for its own
// operations, and this list decides for the workspace's.
func RequireHumanDecision(ctx context.Context) error {
	return operationcore.RequireHumanDecision(ctx, ErrHumanDecisionRequired)
}

// workspaceHumanDecisionOperations are the workspace's operations that change
// what a project is, and therefore may not be performed by a machine identity
// or on behalf of someone else.
//
// The rule is an identity check on the caller, never a client-supplied
// "confirmed" flag. The list is the workspace's, because what counts as
// changing the project is the workspace's judgement.
func workspaceHumanDecisionOperations() map[string]bool {
	return map[string]bool{
		"discardProject":              true,
		"restoreProject":              true,
		"archiveProject":              true,
		"removeArchivedTemplateField": true,
		"activateProject":             true,
		"applyChangeSet":              true,
		"reverseChangeSet":            true,
		"stabilizeRevision":           true,
		"transferOwner":               true,
		"requestOwnerTransfer":        true,
		"acceptOwnerTransfer":         true,
		"rejectOwnerTransfer":         true,
		"inviteMember":                true,
		"acceptMemberInvitation":      true,
		"memberAssignment":            true,
		"memberLifecycle":             true,
		"apiKeyProjectGrant":          true,
		"chapterReview":               true,
		"chapterComment":              true,
	}
}

var ErrProjectInvitationRequired = clienterror.New(clienterror.Form{Status: 422, Code: "project_invitation_required", Message: "请使用邀请接受与逐成员分配流程；批量直接加入已停用。", Retryable: false}, "project_invitation_required")
