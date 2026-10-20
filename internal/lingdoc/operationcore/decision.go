package operationcore

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
)

// The human decision gate.
//
// An operation that changes what a project *is* may not be performed by a
// machine identity, and may not be performed on behalf of someone else. The
// rule is an identity check on the caller, never a client-supplied
// "confirmed" flag.
//
// The error is handed in rather than declared here: the workspace owns how a
// failure reads outward, and the pipeline raises the very same error value
// the workspace raised before, so a caller's error check does not change.

// RequiresHumanDecision reports whether an operation needs the human decision
// gate.
//
// The list of operations used to live apart from the pipeline that applied it,
// so nothing recorded that the two agreed.
func RequiresHumanDecision(operation string) bool {
	switch operation {
	case "discardProject", "restoreProject", "activateProject", "applyChangeSet", "reverseChangeSet", "stabilizeRevision", "transferOwner", "requestOwnerTransfer", "acceptOwnerTransfer", "rejectOwnerTransfer", "inviteMember", "acceptMemberInvitation", "memberAssignment", "memberLifecycle", "apiKeyProjectGrant", "chapterReview", "chapterComment":
		return true
	}
	return false
}

// RequireHumanDecision refuses the operation when the caller may not act for
// themselves.
//
// It returns required when the caller is not allowed to decide alone, and nil
// when the caller may.
func RequireHumanDecision(ctx context.Context, required error) error {
	if _, machine := types.TenantAPIKeyScopeFromContext(ctx); machine {
		return required
	}
	if p, ok := types.PrincipalFromContext(ctx); ok {
		switch p.Type {
		case types.PrincipalAPITenant, types.PrincipalAPIPlatform, types.PrincipalAPIExternalUser, types.PrincipalEmbedChannel, types.PrincipalEmbedSession, types.PrincipalEmbedVisitor:
			return required
		}
	}
	return nil
}

// ReplayPayloadDecodeError marks a failure that happened while decoding a
// stored replay payload.
//
// It is not an ordinary failure: the operation did not fail, its *replay* could
// not be read. The caller must be told the attempt was a replay, so the
// transport does not claim a fresh mutation.
type ReplayPayloadDecodeError struct{ err error }

func (e ReplayPayloadDecodeError) Error() string { return e.err.Error() }

func (e ReplayPayloadDecodeError) Unwrap() error { return e.err }

// MarkReplayPayloadDecodeError marks err as a replay payload decode failure.
func MarkReplayPayloadDecodeError(err error) error {
	if err == nil {
		return nil
	}
	return ReplayPayloadDecodeError{err: err}
}

// IsReplayPayloadDecodeError reports whether err is a replay payload decode
// failure.
func IsReplayPayloadDecodeError(err error) bool {
	var target ReplayPayloadDecodeError
	return errors.As(err, &target)
}
