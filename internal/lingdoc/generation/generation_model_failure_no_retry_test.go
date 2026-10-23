package generation

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// A failed model call is a failed RUN, not a queue error.
//
// Execute records the failure on the run and answers with no error, so the
// task handler sees nothing to retry: the task is acknowledged and the
// provider was asked exactly once. A retry would be a second external
// request for a request the provider already refused, which is the one thing
// a controlled execution may not do.
//
// The tests are offline: the model is a stub, no provider is called and no
// request leaves the process.

// failingCountingModel fails every call and counts them.
type failingCountingModel struct {
	calls int
}

func (m *failingCountingModel) RunMode() string { return "mock" }

func (m *failingCountingModel) Generate(context.Context, Input) (Draft, error) {
	m.calls++
	return Draft{}, errors.New("provider refused the request")
}

// failedRunApplication answers Execute with a run that already failed and no
// error, the way the service answers after a failed model call.
type failedRunApplication struct {
	stubApplication
	run Run
}

func (s *failedRunApplication) Execute(context.Context, string) (Run, error) { return s.run, nil }

func TestFailedModelCallIsRecordedOnTheRunAndNotRetried(t *testing.T) {
	svc, repo := generationFixture()
	model := &failingCountingModel{}
	svc.Model = model

	run, err := svc.Execute(context.Background(), repo.run.ID)

	require.NoError(t, err, "the failure belongs on the run, not on the queue")
	require.Equal(t, StatusFailed, run.Status)
	require.NotNil(t, repo.failure, "the failed run must carry its reason")
	require.Equal(t, 1, model.calls, "the provider must be asked exactly once")
}

func TestTaskHandlerAcknowledgesAFailedRunWithoutRetrying(t *testing.T) {
	svc, repo := generationFixture()
	svc.Model = &failingCountingModel{}
	run, err := svc.Execute(context.Background(), repo.run.ID)
	require.NoError(t, err)

	handler := NewTaskHandler(&failedRunApplication{run: run})

	require.NoError(t, handler.Handle(context.Background(), generationTask(t, 1, "run-1")),
		"a failed run is acknowledged: the queue must not spend a retry on it")
}
