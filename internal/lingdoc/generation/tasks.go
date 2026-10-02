package generation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

type generationTaskPayload struct {
	TenantID uint64 `json:"tenant_id"`
	RunID    string `json:"run_id"`
}

// EnqueueTask schedules a durable generation-run ID, never prompt text,
// credentials, or source content. The immutable input is read from the run
// repository by the worker.
func EnqueueTask(_ context.Context, enqueuer interfaces.TaskEnqueuer, tenantID uint64, runID string) error {
	if enqueuer == nil || tenantID == 0 || runID == "" {
		return ErrDependencyUnavailable
	}
	encoded, err := json.Marshal(generationTaskPayload{TenantID: tenantID, RunID: runID})
	if err != nil {
		return err
	}
	_, err = enqueuer.Enqueue(asynq.NewTask(types.TypeLingDocGeneration, encoded), asynq.Queue(types.QueueSummary))
	return err
}

// TaskHandler adapts the LingDoc worker to WeKnora's shared Asynq/Lite task
// execution path. Repository claim semantics make duplicate queue deliveries
// harmless; the task payload itself contains no user-provided generation data.
type TaskHandler struct{ Service *Service }

func NewTaskHandler(service *Service) *TaskHandler { return &TaskHandler{Service: service} }

func (h *TaskHandler) Handle(ctx context.Context, task *asynq.Task) error {
	if h == nil || h.Service == nil || task == nil {
		return ErrDependencyUnavailable
	}
	var payload generationTaskPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil || payload.TenantID == 0 || payload.RunID == "" {
		return asynq.SkipRetry
	}
	ctx = types.WithExecutionTenant(ctx, payload.TenantID)
	run, err := h.Service.Execute(ctx, payload.RunID)
	if err != nil {
		if errors.Is(err, ErrRunUnavailable) {
			if delayed, ok := h.Service.Enqueuer.(DelayedEnqueuer); ok {
				if enqueueErr := delayed.EnqueueGenerationAfter(ctx, payload.TenantID, payload.RunID, claimLeaseDuration+time.Second); enqueueErr == nil {
					return nil
				}
			}
			// Do not ACK a run that may have lost its only worker.
			return ErrRunUnavailable
		}
		return err
	}
	if run.Status == StatusRunning {
		return ErrRunUnavailable
	}
	return nil
}
