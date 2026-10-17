package workspacecore

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/types"
)

var ErrHumanDecisionRequired = errors.New("human_decision_required")

// This is an identity check, never a client-supplied confirmed flag. Existing
// trusted domain fixtures without a HTTP Principal remain explicit fixtures.
func RequireHumanDecision(ctx context.Context) error {
	if _, machine := types.TenantAPIKeyScopeFromContext(ctx); machine {
		return ErrHumanDecisionRequired
	}
	if p, ok := types.PrincipalFromContext(ctx); ok {
		switch p.Type {
		case types.PrincipalAPITenant, types.PrincipalAPIPlatform, types.PrincipalAPIExternalUser, types.PrincipalEmbedChannel, types.PrincipalEmbedSession, types.PrincipalEmbedVisitor:
			return ErrHumanDecisionRequired
		}
	}
	return nil
}

func requiresHumanOperation(op string) bool {
	switch op {
	case "discardProject", "restoreProject", "activateProject", "applyChangeSet", "stabilizeRevision", "transferOwner", "requestOwnerTransfer", "acceptOwnerTransfer", "rejectOwnerTransfer", "inviteMember", "acceptMemberInvitation", "memberAssignment", "memberLifecycle", "apiKeyProjectGrant", "chapterReview", "chapterComment":
		return true
	}
	return false
}

var ErrProjectInvitationRequired = errors.New("project_invitation_required")
