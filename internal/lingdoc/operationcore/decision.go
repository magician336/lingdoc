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

// HumanGate says whether an operation needs the human decision gate.
//
// The gate is a policy of the domain that owns the operation, not a rule of
// the pipeline: one domain's operation may need a person's confirmation while
// another's does not, and one may need it only for some of its calls. So the
// caller declares it, and the pipeline applies what was declared.
//
// There are three values, not two, and not a bool. A bool's zero value would
// silently mean "no gate" - the direction that quietly weakens a check
// rather than strengthening it. An enum's zero value would have the same
// problem, so the zero value here means "not declared": a caller that forgot
// to say is refused at run time rather than let through.
type HumanGate int

const (
	// HumanGateUndeclared means the caller did not say. The pipeline refuses
	// the operation rather than guessing which way the check should fall.
	HumanGateUndeclared HumanGate = iota
	// HumanGateRequired refuses the operation unless the caller may act for
	// themselves.
	HumanGateRequired
	// HumanGateNotRequired lets the caller act for themselves.
	HumanGateNotRequired
)

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
