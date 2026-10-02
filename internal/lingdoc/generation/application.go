package generation

import (
	"context"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"time"
)

type Application interface {
	Start(context.Context, Actor, string, string, Request) (Run, error)
	Get(context.Context, Actor, string, string) (Run, error)
	Cancel(context.Context, Actor, string, string) (Run, error)
	GetCandidate(context.Context, Actor, string, string) (candidateadoption.Candidate, error)
	ListCandidates(context.Context, Actor, string, string) ([]candidateadoption.CandidateSummary, error)
	Execute(context.Context, string) (Run, error)
	Reschedule(context.Context, uint64, string, time.Duration) error
}

func (s *Service) Reschedule(ctx context.Context, tenantID uint64, runID string, delay time.Duration) error {
	if delayed, ok := s.Enqueuer.(DelayedEnqueuer); ok {
		return delayed.EnqueueGenerationAfter(ctx, tenantID, runID, delay)
	}
	return ErrRunUnavailable
}

var _ Application = (*Service)(nil)
