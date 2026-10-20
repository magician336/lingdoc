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
// The rule itself belongs to the shared pipeline: the same check and the same
// list of operations that need it, so a call that reaches the gate through
// the pipeline and a call that reaches it directly cannot disagree.
func RequireHumanDecision(ctx context.Context) error {
	return operationcore.RequireHumanDecision(ctx, ErrHumanDecisionRequired)
}

var ErrProjectInvitationRequired = clienterror.New(clienterror.Form{Status: 422, Code: "project_invitation_required", Message: "请使用邀请接受与逐成员分配流程；批量直接加入已停用。", Retryable: false}, "project_invitation_required")
