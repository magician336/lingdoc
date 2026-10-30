package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

type agentAttemptKey struct{}
type agentAttemptEvent struct {
	request AuthorizationRequest
	err     error
	shadow  bool
}
type agentAttemptObservations struct {
	mu     sync.Mutex
	events []agentAttemptEvent
}

func bufferAgentAttempt(ctx context.Context, request AuthorizationRequest, err error, shadow bool) bool {
	buffer, ok := ctx.Value(agentAttemptKey{}).(*agentAttemptObservations)
	if !ok {
		return false
	}
	buffer.mu.Lock()
	buffer.events = append(buffer.events, agentAttemptEvent{request: request, err: err, shadow: shadow})
	buffer.mu.Unlock()
	return true
}

// Retry the complete authorized request, including source IO, but only after
// a SQLite callback error and a confirmed successful database rollback.
func (s *Service) retryAgentAfterVerifiedRollback(ctx context.Context, run func(context.Context) (json.RawMessage, int, bool, error)) (json.RawMessage, int, bool, error) {
	repository, ok := s.repository.(*GORMRepository)
	if !ok || repository.db.Name() != "sqlite" {
		return run(ctx)
	}
	return retryVerifiedSQLiteOperation(ctx, func(attemptCtx context.Context) (json.RawMessage, int, bool, error) {
		buffer := &agentAttemptObservations{}
		attemptCtx = context.WithValue(attemptCtx, agentAttemptKey{}, buffer)
		raw, status, replay, err := run(attemptCtx)
		var rollback verifiedSQLiteRollback
		if errors.As(err, &rollback) && isSQLiteLockError(err) {
			return raw, status, replay, err
		}
		buffer.mu.Lock()
		events := append([]agentAttemptEvent(nil), buffer.events...)
		buffer.mu.Unlock()
		for _, event := range events {
			if event.shadow {
				_ = s.recordAudit(ctx, event.request.Actor, event.request.ProjectID, "shadow:"+event.request.capability(), event.err)
			} else {
				noteAuthorizationDenial(ctx, event.request, event.err)
			}
		}
		return raw, status, replay, err
	})
}
