package candidateadoption

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/clienterror"
)

// The candidate adoption surface's own wording.
//
// The workspace main route answers the same domain failures with another
// wording, and the workspace surface already declares those sentinels. The
// form is decided by the situation, so this surface declares its own scenario
// values and wraps the sentinels: a sentinel keeps its own form for every
// other surface, and errors.Is still finds it.
var (
	// ErrAdoptionInvalidState is a candidate adoption request the current
	// stage does not allow, as this surface words it.
	ErrAdoptionInvalidState = clienterror.Wrap(clienterror.StageNotAllowed, "candidate adoption: the current stage does not allow this action", ErrInvalidState)
	// ErrAdoptionStaleInput is a candidate or snapshot that was built on an
	// input that has changed since, as this surface words it.
	ErrAdoptionStaleInput = clienterror.Wrap(clienterror.CandidateStaleInput, "candidate adoption: the candidate or snapshot is based on an old input", ErrStaleInput)
	// ErrAdoptionIdempotencyConflict is one idempotency key used by two
	// different requests, as this surface words it.
	ErrAdoptionIdempotencyConflict = clienterror.Wrap(clienterror.IdempotencyKeyConflict, "candidate adoption: the same idempotency key carries a different request", ErrIdempotencyConflict)
	// ErrAdoptionDependencyUnavailable is a candidate adoption dependency
	// that is not ready, as this surface words it.
	ErrAdoptionDependencyUnavailable = clienterror.Wrap(clienterror.Form{Status: 503, Code: "dependency_unavailable", Message: "候选采纳依赖尚未就绪，请稍后重试。", Retryable: true}, "candidate adoption: the dependency is not ready yet", ErrDependencyUnavailable)
)

// The failures this surface raises itself, before any application call.
var (
	// ErrUnauthenticated is a request without a usable actor.
	ErrUnauthenticated = clienterror.New(clienterror.Unauthenticated, "unauthenticated")
	// ErrAdoptionServiceUnavailable is the answer when the candidate adoption
	// service is not configured. It keeps the shape this surface has always
	// answered with.
	ErrAdoptionServiceUnavailable = clienterror.New(clienterror.Form{Status: 503, Code: "invalid_state", Message: "当前阶段不能执行该动作。", Retryable: false}, "candidate adoption service is unavailable")
)

// surfaceError presents a domain failure the way the candidate adoption
// surface words it.
//
// The candidates and the workspace read the same domain sentinels, and the two
// surfaces word four of them differently. The presentation belongs to the
// surface, so it is applied at the surface's own application seam.
func surfaceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalidState):
		return ErrAdoptionInvalidState
	case errors.Is(err, ErrStaleInput):
		return ErrAdoptionStaleInput
	case errors.Is(err, ErrIdempotencyConflict):
		return ErrAdoptionIdempotencyConflict
	case errors.Is(err, ErrDependencyUnavailable):
		return ErrAdoptionDependencyUnavailable
	}
	return err
}

// surfaceApplication presents the candidate adoption failures the way this
// surface words them.
type surfaceApplication struct{ inner Application }

func newSurfaceApplication(inner Application) Application {
	if inner == nil {
		return nil
	}
	return &surfaceApplication{inner: inner}
}

func (s *surfaceApplication) AcceptCandidate(ctx context.Context, in AcceptCandidateInput) (AcceptResult, error) {
	result, err := s.inner.AcceptCandidate(ctx, in)
	return result, surfaceError(err)
}

func (s *surfaceApplication) ReadCandidate(ctx context.Context, actor, projectID, candidateID string) (Candidate, error) {
	candidate, err := s.inner.ReadCandidate(ctx, actor, projectID, candidateID)
	return candidate, surfaceError(err)
}

func (s *surfaceApplication) ReadChapters(ctx context.Context, actor, projectID string) ([]Chapter, error) {
	chapters, err := s.inner.ReadChapters(ctx, actor, projectID)
	return chapters, surfaceError(err)
}

// surfaceConfirmationApplication presents the confirmation failures the way
// this surface words them.
type surfaceConfirmationApplication struct{ inner ConfirmationApplication }

func newSurfaceConfirmationApplication(inner ConfirmationApplication) ConfirmationApplication {
	if inner == nil {
		return nil
	}
	return &surfaceConfirmationApplication{inner: inner}
}

func (s *surfaceConfirmationApplication) ConfirmChapter(ctx context.Context, in ConfirmChapterInput) (Confirmation, bool, error) {
	confirmation, replayed, err := s.inner.ConfirmChapter(ctx, in)
	return confirmation, replayed, surfaceError(err)
}
