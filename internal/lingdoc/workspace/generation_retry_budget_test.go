package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The retry budget of a queued generation run.
//
// The same task type (types.TypeLingDocGeneration) reaches the same queue
// (types.QueueSummary) through two enqueue paths:
//
//   - the normal path: generation.EnqueueTask
//   - the delayed reschedule path: EnqueueGenerationAfter, used when the
//     run's lease is still live and the worker asks for another attempt
//     later (generation.TaskHandler.Handle -> Service.Reschedule).
//
// Only the normal path stated a retry budget. The delayed path inherited
// asynq's default of 25 retries (asynq v0.26.0, "defaultMaxRetry = 25"),
// so one throttled run could spend up to 25 real provider calls, burn the
// acceptance budget, and block every later node of the round.
//
// The tests are offline: no provider is called, no Redis is used, and no
// request leaves the process.
// ---------------------------------------------------------------------------

// capturingEnqueuer records the task and the options it was enqueued with.
type capturingEnqueuer struct {
	task *asynq.Task
	opts []asynq.Option
}

func (e *capturingEnqueuer) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	e.task = task
	e.opts = opts
	return &asynq.TaskInfo{Type: task.Type(), Queue: types.QueueSummary}, nil
}

// statedMaxRetry reads the retry budget the queue would apply, and reports
// whether the enqueue stated one at all.
func statedMaxRetry(opts []asynq.Option) (int, bool) {
	for _, opt := range opts {
		if opt.Type() == asynq.MaxRetryOpt {
			return opt.Value().(int), true
		}
	}
	return 0, false
}

// statedQueue reads the queue the task was sent to.
func statedQueue(opts []asynq.Option) string {
	for _, opt := range opts {
		if opt.Type() == asynq.QueueOpt {
			return opt.Value().(string)
		}
	}
	return ""
}

// statedProcessIn reads the delay the task was enqueued with.
func statedProcessIn(opts []asynq.Option) (time.Duration, bool) {
	for _, opt := range opts {
		if opt.Type() == asynq.ProcessInOpt {
			return opt.Value().(time.Duration), true
		}
	}
	return 0, false
}

// enqueueThroughBothPaths enqueues the same run through the normal path and
// through the delayed reschedule path, and returns what each of them stated.
func enqueueThroughBothPaths(t *testing.T, delay time.Duration) (normal, delayed *capturingEnqueuer) {
	t.Helper()
	normal = &capturingEnqueuer{}
	require.NoError(t, generation.EnqueueTask(context.Background(), normal, 1, "run-1"))
	delayed = &capturingEnqueuer{}
	require.NoError(t, generationTaskEnqueuer{tasks: delayed}.EnqueueGenerationAfter(
		context.Background(), 1, "run-1", delay))
	return normal, delayed
}

func TestBothEnqueuePathsStateTheSameRetryBudget(t *testing.T) {
	normal, delayed := enqueueThroughBothPaths(t, 30*time.Second)

	normalBudget, normalStated := statedMaxRetry(normal.opts)
	delayedBudget, delayedStated := statedMaxRetry(delayed.opts)

	require.True(t, normalStated, "the normal path must state a retry budget")
	require.True(t, delayedStated,
		"the delayed reschedule path must state a retry budget too; without it asynq's default of 25 is in force")
	assert.Equal(t, normalBudget, delayedBudget,
		"both enqueue paths must spend the same retry budget")
	assert.Equal(t, generation.GenerationTaskMaxRetry, delayedBudget,
		"the delayed path must use the one budget the generation package defines")
	assert.NotEqual(t, 25, delayedBudget, "asynq's default budget of 25 must not be in force")

	// One attempt plus its retries is how many real provider calls a
	// throttled run can spend on that path.
	assert.LessOrEqual(t, normalBudget+1, 4,
		"a retryable failure must not spend more than 4 attempts on the normal path")
	assert.LessOrEqual(t, delayedBudget+1, 4,
		"a retryable failure must not spend more than 4 attempts on the delayed reschedule path")
}

func TestBothEnqueuePathsEnqueueTheSameTask(t *testing.T) {
	normal, delayed := enqueueThroughBothPaths(t, 30*time.Second)

	assert.Equal(t, types.TypeLingDocGeneration, normal.task.Type())
	assert.Equal(t, normal.task.Type(), delayed.task.Type(),
		"both paths must enqueue the same task type so the same handler classifies their failures")
	assert.Equal(t, normal.task.Payload(), delayed.task.Payload(),
		"both paths must carry the same payload so the same failure classification applies")
	assert.Equal(t, statedQueue(normal.opts), statedQueue(delayed.opts))
	assert.Equal(t, types.QueueSummary, statedQueue(delayed.opts))
}

func TestDelayedReschedulePathKeepsItsDelay(t *testing.T) {
	_, delayed := enqueueThroughBothPaths(t, 45*time.Second)

	delay, stated := statedProcessIn(delayed.opts)
	require.True(t, stated, "the delayed path must keep its delay")
	assert.Equal(t, 45*time.Second, delay)
}

// budgetStubApplication answers Execute with a fixed error.
type budgetStubApplication struct {
	executeErr error
}

func (s *budgetStubApplication) Start(context.Context, generation.Actor, string, string, generation.Request) (generation.Run, error) {
	return generation.Run{}, nil
}

func (s *budgetStubApplication) Get(context.Context, generation.Actor, string, string) (generation.Run, error) {
	return generation.Run{}, nil
}

func (s *budgetStubApplication) Cancel(context.Context, generation.Actor, string, string) (generation.Run, error) {
	return generation.Run{}, nil
}

func (s *budgetStubApplication) GetCandidate(context.Context, generation.Actor, string, string) (candidateadoption.Candidate, error) {
	return candidateadoption.Candidate{}, nil
}

func (s *budgetStubApplication) ListCandidates(context.Context, generation.Actor, string, string) ([]candidateadoption.CandidateSummary, error) {
	return nil, nil
}

func (s *budgetStubApplication) Execute(context.Context, string) (generation.Run, error) {
	return generation.Run{}, s.executeErr
}

func (s *budgetStubApplication) Reschedule(context.Context, uint64, string, time.Duration) error {
	return nil
}

// TestDelayedRescheduleTaskIsClassifiedLikeTheNormalPath drives the task the
// delayed path actually enqueued through the shared handler. The delayed path
// has no failure classification of its own: it enqueues the same task type
// with the same payload, so the same handler classifies it.
func TestDelayedRescheduleTaskIsClassifiedLikeTheNormalPath(t *testing.T) {
	_, delayed := enqueueThroughBothPaths(t, 30*time.Second)

	cases := []struct {
		name      string
		err       error
		retryable bool
	}{
		{
			name:      "permanent 400 is tried once",
			err:       errors.New("API request failed with status 400: bad request"),
			retryable: false,
		},
		{
			name:      "permanent 404 is tried once",
			err:       errors.New("API request failed with status 404: model does not exist"),
			retryable: false,
		},
		{
			name:      "rate limited 429 stays retryable",
			err:       errors.New("API request failed with status 429: slow down"),
			retryable: true,
		},
		{
			name:      "server error 503 stays retryable",
			err:       errors.New("API request failed with status 503: service unavailable"),
			retryable: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handler := generation.NewTaskHandler(&budgetStubApplication{executeErr: c.err})

			err := handler.Handle(context.Background(), delayed.task)

			require.Error(t, err)
			if c.retryable {
				assert.NotErrorIs(t, err, asynq.SkipRetry,
					"a transient failure must stay retryable, bounded by the stated budget")
				return
			}
			assert.ErrorIs(t, err, asynq.SkipRetry,
				"a permanent failure must be tried once instead of spending the retry budget")
		})
	}
}
