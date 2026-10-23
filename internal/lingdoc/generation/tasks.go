package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

type generationTaskPayload struct {
	TenantID uint64 `json:"tenant_id"`
	RunID    string `json:"run_id"`
}

// GenerationTaskMaxRetry is the retry budget one queued generation run gets.
//
// The budget is a delivery concern, not a business rule. A generation run
// that failed can be issued again by the user, so the queue must not spend 25
// attempts on it — 25 is asynq's default when no budget is stated (asynq
// v0.26.0, client.go: "defaultMaxRetry = 25"). 3 retries (4 attempts in
// total) is the same budget the knowledge-base summary tasks already use
// (internal/application/service/knowledge_summary_refresh.go:131,
// knowledge_post_process.go:582).
//
// The budget is exported because two enqueue paths must state it: the normal
// path (EnqueueTask) and the delayed reschedule path
// (workspace.generationTaskEnqueuer.EnqueueGenerationAfter) put the same
// task type on the same queue. One definition, two callers, one number — a
// path that states its own budget is a second number waiting to drift.
const GenerationTaskMaxRetry = 3

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
	_, err = enqueuer.Enqueue(
		asynq.NewTask(types.TypeLingDocGeneration, encoded),
		asynq.Queue(types.QueueSummary),
		asynq.MaxRetry(GenerationTaskMaxRetry),
	)
	return err
}

// upstreamStatusPattern finds the HTTP status an upstream provider answered
// with.
//
// The chat clients report a rejected call as
// "API request failed with status 400: ..." (internal/models/chat/remote_api.go:253,
// internal/models/chat/anthropic.go:194). The status is not carried in a typed
// error, so the message is the only place it can be read.
var upstreamStatusPattern = regexp.MustCompile(`status (\d{3})`)

// taskFailureRetryable reports whether a failed generation task deserves
// another attempt from the queue.
//
// The verdict has two sources, in this order:
//
//  1. The domain's own declaration. A clienterror form already says whether
//     repeating the request could succeed (clienterror.Form.Retryable), and a
//     form that says "no" is final: repeating it repeats the same rejection.
//  2. The upstream HTTP status, when the failure carries one. A 4xx is a
//     permanent rejection — the provider refuses the same request the same
//     way every time. 408 (request timeout) and 429 (rate limited) are the
//     two 4xx worth repeating.
//
// Everything else stays retryable: the budget is bounded
// (GenerationTaskMaxRetry), so an unknown failure costs a few attempts and
// then lands in the dead-letter queue where an operator can read it.
func taskFailureRetryable(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, asynq.SkipRetry) {
		return false
	}
	if form, ok := clienterror.FormOf(err); ok {
		return form.Retryable
	}
	if errors.Is(err, ErrRunUnavailable) {
		// The lease of this run is still live: another worker may finish it.
		return true
	}
	if status, ok := upstreamStatus(err); ok {
		return status == http.StatusRequestTimeout ||
			status == http.StatusTooManyRequests ||
			status >= http.StatusInternalServerError
	}
	return true
}

// upstreamStatus reads the HTTP status an upstream provider answered with.
func upstreamStatus(err error) (int, bool) {
	matches := upstreamStatusPattern.FindStringSubmatch(err.Error())
	if len(matches) != 2 {
		return 0, false
	}
	status, convErr := strconv.Atoi(matches[1])
	if convErr != nil {
		return 0, false
	}
	return status, true
}

// TaskHandler adapts the LingDoc worker to WeKnora's shared Asynq/Lite task
// execution path. Repository claim semantics make duplicate queue deliveries
// harmless; the task payload itself contains no user-provided generation data.
type TaskHandler struct{ Service Application }

func NewTaskHandler(service Application) *TaskHandler { return &TaskHandler{Service: service} }

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
			if enqueueErr := h.Service.Reschedule(ctx, payload.TenantID, payload.RunID, claimLeaseDuration+time.Second); enqueueErr == nil {
				return nil
			}
			// Do not ACK a run that may have lost its only worker.
			return ErrRunUnavailable
		}
		if !taskFailureRetryable(err) {
			// A permanent failure is archived now instead of spending the
			// retry budget on a request the provider refuses the same way
			// every time. The reason travels with the SkipRetry so the
			// archived task still says why it stopped.
			return fmt.Errorf("permanent generation failure: %w: %v", asynq.SkipRetry, err)
		}
		return err
	}
	if run.Status == StatusRunning {
		return ErrRunUnavailable
	}
	return nil
}
