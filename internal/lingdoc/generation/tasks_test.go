package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The generation task's retry budget and its failure classification.
//
// Two separate facts are pinned here:
//
//   - The budget is stated, not inherited. Without asynq.MaxRetry the task
//     silently gets asynq's default of 25 retries (asynq v0.26.0,
//     "defaultMaxRetry = 25").
//   - A permanent failure does not spend the budget. A 4xx from the provider
//     is a refusal, not a hiccup: repeating it repeats the same refusal, so
//     the task is archived instead of being retried.
//
// The tests are offline: no provider is called and no request leaves the
// process.
// ---------------------------------------------------------------------------

// capturingEnqueuer records the options the task was enqueued with.
type capturingEnqueuer struct {
	task *asynq.Task
	opts []asynq.Option
}

func (e *capturingEnqueuer) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	e.task = task
	e.opts = opts
	return &asynq.TaskInfo{Type: task.Type(), Queue: types.QueueSummary}, nil
}

// stubApplication answers Execute with a fixed error.
type stubApplication struct {
	executeErr error
	reschedule int
}

func (s *stubApplication) Start(context.Context, Actor, string, string, Request) (Run, error) {
	return Run{}, nil
}

func (s *stubApplication) Get(context.Context, Actor, string, string) (Run, error) {
	return Run{}, nil
}

func (s *stubApplication) Cancel(context.Context, Actor, string, string) (Run, error) {
	return Run{}, nil
}

func (s *stubApplication) GetCandidate(context.Context, Actor, string, string) (candidateadoption.Candidate, error) {
	return candidateadoption.Candidate{}, nil
}

func (s *stubApplication) ListCandidates(context.Context, Actor, string, string) ([]candidateadoption.CandidateSummary, error) {
	return nil, nil
}

func (s *stubApplication) Execute(context.Context, string) (Run, error) {
	return Run{}, s.executeErr
}

func (s *stubApplication) Reschedule(context.Context, uint64, string, time.Duration) error {
	s.reschedule++
	return nil
}

func generationTask(t *testing.T, tenantID uint64, runID string) *asynq.Task {
	t.Helper()
	encoded, err := json.Marshal(generationTaskPayload{TenantID: tenantID, RunID: runID})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeLingDocGeneration, encoded)
}

func TestEnqueueTaskStatesTheRetryBudgetExplicitly(t *testing.T) {
	enqueuer := &capturingEnqueuer{}

	require.NoError(t, EnqueueTask(context.Background(), enqueuer, 1, "run-1"))

	// The options are read the same way the Lite executor reads them
	// (internal/router/sync_task.go).
	maxRetry := 0
	queue := ""
	for _, opt := range enqueuer.opts {
		switch opt.Type() {
		case asynq.MaxRetryOpt:
			maxRetry = opt.Value().(int)
		case asynq.QueueOpt:
			queue = opt.Value().(string)
		}
	}
	assert.Equal(t, generationTaskMaxRetry, maxRetry,
		"the task must state its retry budget instead of inheriting asynq's default of 25")
	assert.NotEqual(t, 25, maxRetry, "asynq's default budget must not be in force")
	assert.Equal(t, types.QueueSummary, queue)
	assert.Equal(t, types.TypeLingDocGeneration, enqueuer.task.Type())
}

func TestEnqueueTaskRejectsIncompleteWork(t *testing.T) {
	enqueuer := &capturingEnqueuer{}

	require.ErrorIs(t, EnqueueTask(context.Background(), nil, 1, "run-1"), ErrDependencyUnavailable)
	require.ErrorIs(t, EnqueueTask(context.Background(), enqueuer, 0, "run-1"), ErrDependencyUnavailable)
	require.ErrorIs(t, EnqueueTask(context.Background(), enqueuer, 1, ""), ErrDependencyUnavailable)
	assert.Empty(t, enqueuer.opts, "nothing may be enqueued for incomplete work")
}

func TestTaskFailureRetryableRejectsPermanentFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{
			name: "upstream 400 parameter error",
			err:  errors.New(`API request failed with status 400: {"error":{"message":"Field required: input.contents"}}`),
		},
		{
			name: "upstream 401 unauthorized",
			err:  errors.New("API request failed with status 401: invalid api key"),
		},
		{
			name: "upstream 404 model not found",
			err:  errors.New("API request failed with status 404: model does not exist"),
		},
		{
			name: "upstream 422 unprocessable",
			err:  errors.New("API request failed with status 422: unsupported parameter"),
		},
		{
			name: "domain form that declares itself non-retryable",
			err:  clienterror.New(clienterror.InvalidRequest, "invalid request"),
		},
		{
			name: "skip retry marker",
			err:  asynq.SkipRetry,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.False(t, taskFailureRetryable(c.err), "a permanent failure must not be retried")
		})
	}
}

func TestTaskFailureRetryableAcceptsTransientFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{
			name: "upstream 500",
			err:  errors.New("API request failed with status 500: internal error"),
		},
		{
			name: "upstream 503",
			err:  errors.New("API request failed with status 503: service unavailable"),
		},
		{
			name: "upstream 429 rate limited",
			err:  errors.New("API request failed with status 429: slow down"),
		},
		{
			name: "upstream 408 request timeout",
			err:  errors.New("API request failed with status 408: request timeout"),
		},
		{
			name: "network error",
			err:  fmt.Errorf("send request: %w", &net.OpError{Op: "dial", Err: errors.New("connection reset")}),
		},
		{
			name: "dependency unavailable",
			err:  ErrDependencyUnavailable,
		},
		{
			name: "lease still live",
			err:  ErrRunUnavailable,
		},
		{
			name: "unknown failure",
			err:  errors.New("something nobody has classified"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.True(t, taskFailureRetryable(c.err), "a transient failure must stay retryable")
		})
	}
}

func TestTaskHandlerArchivesPermanentFailureWithoutRetrying(t *testing.T) {
	service := &stubApplication{executeErr: errors.New(
		`API request failed with status 400: {"error":{"message":"Field required: input.contents"}}`)}
	handler := NewTaskHandler(service)

	err := handler.Handle(context.Background(), generationTask(t, 1, "run-1"))

	require.Error(t, err)
	assert.ErrorIs(t, err, asynq.SkipRetry, "a permanent failure must not spend the retry budget")
	assert.Contains(t, err.Error(), "status 400", "the archived task must still say why it stopped")
}

func TestTaskHandlerKeepsTransientFailureRetryable(t *testing.T) {
	service := &stubApplication{executeErr: errors.New("API request failed with status 503: service unavailable")}
	handler := NewTaskHandler(service)

	err := handler.Handle(context.Background(), generationTask(t, 1, "run-1"))

	require.Error(t, err)
	assert.NotErrorIs(t, err, asynq.SkipRetry, "a transient failure must be handed back to the queue")
	assert.Contains(t, err.Error(), "status 503")
}

func TestTaskHandlerKeepsDomainFailureReasons(t *testing.T) {
	service := &stubApplication{executeErr: clienterror.New(clienterror.InvalidRequest, "invalid request")}
	handler := NewTaskHandler(service)

	err := handler.Handle(context.Background(), generationTask(t, 1, "run-1"))

	require.Error(t, err)
	assert.ErrorIs(t, err, asynq.SkipRetry)
	assert.Contains(t, err.Error(), "invalid request", "the archived task must keep the domain's own reason")
}
