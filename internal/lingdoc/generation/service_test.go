package generation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	lingdocaccess "github.com/Tencent/WeKnora/internal/lingdoc/access"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

type testAuthorizer struct{ err error }

func (a testAuthorizer) AuthorizeGeneration(context.Context, Actor, string) error { return a.err }

type testInputs struct{ input Input }

func (r testInputs) ResolveGenerationInput(_ context.Context, actor Actor, project string, req Request) (Input, error) {
	in := r.input
	in.Actor, in.Request = actor, req
	in.Workspace.ProjectID, in.Workspace.ChapterID = project, req.ChapterID
	return in, nil
}

type countingInputs struct{ calls int }

func (r *countingInputs) ResolveGenerationInput(context.Context, Actor, string, Request) (Input, error) {
	r.calls++
	return Input{}, nil
}

type testSources struct{ err error }

func (v testSources) ValidateGenerationSources(context.Context, Actor, string, []Source) error {
	return v.err
}

func (v testSources) ValidateGenerationSourcesWithEvidence(context.Context, Actor, string, string, []EvidenceCardRef, []Source) error {
	return v.err
}

type testCurrentness struct {
	current bool
	err     error
}

type sharedGenerationPolicyStub struct {
	allowed bool
	current bool
}

func (p *sharedGenerationPolicyStub) Authorize(context.Context, lingdocaccess.LingDocAccessContext, string) error {
	if !p.allowed {
		return ErrForbidden
	}
	return nil
}

func (p *sharedGenerationPolicyStub) Current(context.Context, lingdocaccess.LingDocAccessContext, lingdocaccess.CurrentnessInput) (bool, error) {
	return p.current, nil
}

func (c testCurrentness) GenerationInputIsCurrent(context.Context, Actor, string, string, candidateadoption.Basis) (bool, error) {
	return c.current, c.err
}

type testModel struct {
	draft Draft
	err   error
}

// RunMode 报 mock：测试替身没有 provider，不得冒充真实证据。
func (m testModel) RunMode() string { return "mock" }

func (m testModel) Generate(context.Context, Input) (Draft, error) { return m.draft, m.err }

type deadlineTestModel struct{}

func (deadlineTestModel) RunMode() string { return "mock" }

func (deadlineTestModel) Generate(ctx context.Context, _ Input) (Draft, error) {
	<-ctx.Done()
	return Draft{}, ctx.Err()
}

type countingModel struct {
	calls int
	draft Draft
}

func (m *countingModel) RunMode() string { return "mock" }

func (m *countingModel) Generate(context.Context, Input) (Draft, error) {
	m.calls++
	return m.draft, nil
}

type testEnqueuer struct{ calls int }

func (e *testEnqueuer) EnqueueGeneration(context.Context, uint64, string) error {
	e.calls++
	return nil
}

type delayedTestEnqueuer struct {
	calls int
	delay time.Duration
}

func (e *delayedTestEnqueuer) EnqueueGeneration(context.Context, uint64, string) error { return nil }
func (e *delayedTestEnqueuer) EnqueueGenerationAfter(_ context.Context, _ uint64, _ string, delay time.Duration) error {
	e.calls++
	e.delay = delay
	return nil
}

type testRepository struct {
	findReplayCalls int
	createRunCalls  int
	run             Run
	input           Input
	idempotencyKey  string
	requestHash     string
	claimed         bool
	candidate       candidateadoption.Candidate
	failure         *RunError
}

func (r *testRepository) FindReplay(_ context.Context, _ Actor, _ string, key, hash string) (Run, bool, error) {
	r.findReplayCalls++
	if r.run.ID == "" || key != r.idempotencyKey {
		return Run{}, false, nil
	}
	if hash != r.requestHash {
		return Run{}, false, ErrIdempotencyConflict
	}
	run := r.run
	run.Replayed = true
	return run, true, nil
}
func (r *testRepository) CreateOrReplay(_ context.Context, _ Actor, project, key, hash string, input Input) (Run, error) {
	r.createRunCalls++
	r.run = Run{ID: "run-1", ProjectID: project, ChapterID: input.Request.ChapterID, Status: StatusQueued}
	r.input, r.idempotencyKey, r.requestHash = input, key, hash
	return r.run, nil
}
func (r *testRepository) Claim(context.Context, string) (Run, Input, bool, error) {
	if r.claimed {
		return r.run, r.input, false, nil
	}
	r.claimed = true
	r.run.Status, r.run.ClaimToken = StatusRunning, "test-claim-token"
	return r.run, r.input, true, nil
}
func (r *testRepository) Touch(context.Context, string, string) error { return nil }
func (r *testRepository) Cancel(_ context.Context, _ Actor, _, _ string) (Run, error) {
	r.run.Status = StatusInterrupted
	r.failure = &RunError{Code: "generation_cancelled", Message: "生成任务已取消。"}
	r.run.Error = r.failure
	return r.run, nil
}
func (r *testRepository) ListCandidates(context.Context, string, string) ([]candidateadoption.CandidateSummary, error) {
	return nil, nil
}
func (r *testRepository) GetCandidate(context.Context, string, string) (candidateadoption.Candidate, Input, error) {
	return r.candidate, r.input, nil
}
func (r *testRepository) Complete(_ context.Context, _, _ string, c candidateadoption.Candidate) (Run, error) {
	r.candidate = c
	r.run.Status, r.run.CandidateID = StatusSucceeded, &c.ID
	return r.run, nil
}
func (r *testRepository) Fail(_ context.Context, _, _ string, status Status, failure RunError) (Run, error) {
	r.run.Status, r.failure = status, &failure
	r.run.Error = &failure
	return r.run, nil
}
func (r *testRepository) Get(context.Context, Actor, string, string) (Run, error) { return r.run, nil }

func generationFixture() (*Service, *testRepository) {
	actor := Actor{TenantID: 1, UserID: "user-1"}
	chapterVersion := "chapter-v1"
	basis := candidateadoption.Basis{SpecRevision: 3, ChapterVersionID: &chapterVersion,
		AssetVersions: []candidateadoption.AssetVersion{{AssetID: "asset-1", AssetRevision: 2}}}
	input := Input{Actor: actor, Basis: basis,
		Workspace: candidateadoption.GenerationContext{ProjectID: "project-1", ChapterID: "chapter-1", SpecRevision: 3, ChapterVersionID: &chapterVersion, Basis: basis},
		Sources:   []Source{{ID: "source-1", ProjectID: "project-1", AssetID: "asset-1", AssetRevision: 2, Status: "available"}}}
	repo := &testRepository{input: input, run: Run{ID: "run-1", ProjectID: "project-1", ChapterID: "chapter-1", Status: StatusQueued}}
	svc := NewService(testAuthorizer{}, testInputs{input: input}, testSources{}, testCurrentness{current: true},
		testModel{draft: Draft{BodyMarkdown: "Text [[source:source-1]]", Sources: []Source{{ID: "source-1"}}}}, repo, &testEnqueuer{})
	svc.NewID = func() string { return "candidate-1" }
	return svc, repo
}

func TestStartEmptyAssetScopeHasNoDownstreamEffects(t *testing.T) {
	inputs := &countingInputs{}
	repo := &testRepository{}
	enqueuer := &testEnqueuer{}
	model := &countingModel{}
	service := NewService(testAuthorizer{}, inputs, testSources{}, testCurrentness{}, model, repo, enqueuer)
	request := Request{
		ChapterID: "chapter-1", AssetIDs: []string{}, Instruction: "Draft",
		ExpectedSpecRevision: 1, ExpectedChapterVersionID: nil,
	}

	_, err := service.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "f02-empty-assets-0001", request)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Start() error = %v, want invalid request", err)
	}
	observation := map[string]int{
		"input_resolver_calls":         inputs.calls,
		"repository_find_replay_calls": repo.findReplayCalls,
		"repository_create_run_calls":  repo.createRunCalls,
		"enqueue_calls":                enqueuer.calls,
		"model_generate_calls":         model.calls,
	}
	encoded, _ := json.Marshal(observation)
	t.Logf("WHITE_BOX_OBSERVATION %s", encoded)
	for point, calls := range observation {
		if calls != 0 {
			t.Errorf("%s = %d, want 0", point, calls)
		}
	}
}

func TestStartChecksVersionsAndAssetSet(t *testing.T) {
	svc, repo := generationFixture()
	req := Request{ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "Draft", ExpectedSpecRevision: 3, ExpectedChapterVersionID: stringPtr("chapter-v1")}
	first, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "idempotency-1", req)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	changedInput := repo.input
	changedInput.Workspace.SpecRevision++
	svc.Inputs = testInputs{input: changedInput}
	replayed, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "idempotency-1", req)
	if err != nil || replayed.ID != first.ID || !replayed.Replayed {
		t.Fatalf("Start() retry = (%+v, %v), want original run replay despite current workspace drift", replayed, err)
	}
	svc.Inputs = testInputs{input: repo.input}
	req.AssetIDs = []string{"other-asset"}
	if _, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "idempotency-2", req); !errors.Is(err, ErrSourceAccessDenied) {
		t.Fatalf("Start() error = %v, want source access denied", err)
	}
}

func TestExecuteCreatesCandidateOnlyForVerifiedSources(t *testing.T) {
	svc, repo := generationFixture()
	run, err := svc.Execute(context.Background(), "run-1")
	if err != nil || run.Status != StatusSucceeded {
		t.Fatalf("Execute() = (%+v, %v), want succeeded", run, err)
	}
	if repo.candidate.ID != "candidate-1" || len(repo.candidate.SourceIDs) != 1 || repo.candidate.SourceIDs[0] != "source-1" {
		t.Fatalf("candidate source result = %+v", repo.candidate)
	}
	// 候选必须记下自己是怎么来的：采纳是一次显式确认，确认载荷要拿它和候选
	// 对上。这个生成端点产出的就是「生成」这个动作的结果，正文全部来自模型。
	if repo.candidate.WritingAction != writingaction.ActionGenerate {
		t.Fatalf("candidate writing action = %q, want %q", repo.candidate.WritingAction, writingaction.ActionGenerate)
	}
	if repo.candidate.Origin != writingaction.OriginGenerated {
		t.Fatalf("candidate origin = %q, want %q", repo.candidate.Origin, writingaction.OriginGenerated)
	}
	candidate, err := svc.GetCandidate(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "run-1")
	if err != nil || candidate.Validity != "fresh" {
		t.Fatalf("GetCandidate() = (%+v, %v), want a fresh candidate", candidate, err)
	}
	svc.Currentness = testCurrentness{current: false}
	candidate, err = svc.GetCandidate(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "run-1")
	if err != nil || candidate.Validity != "stale" {
		t.Fatalf("GetCandidate() stale = (%+v, %v), want stale", candidate, err)
	}

	svc, repo = generationFixture()
	svc.Model = testModel{draft: Draft{BodyMarkdown: "forged [[source:other]]", Sources: []Source{{ID: "other"}}}}
	run, err = svc.Execute(context.Background(), "run-1")
	if err != nil || run.Status != StatusFailed || repo.failure == nil || repo.failure.Code != "invalid_source_reference" {
		t.Fatalf("forged source Execute() = (%+v, %v), failure=%+v", run, err, repo.failure)
	}
}

func TestExecuteAllowsRepeatedCitationOfOneSource(t *testing.T) {
	svc, repo := generationFixture()
	svc.Model = testModel{draft: Draft{
		BodyMarkdown: "First claim [[source:source-1]]. Another claim [[source:source-1]].",
		Sources:      []Source{{ID: "source-1"}},
	}}

	run, err := svc.Execute(context.Background(), "run-1")
	if err != nil || run.Status != StatusSucceeded {
		t.Fatalf("Execute() = (%+v, %v), want succeeded", run, err)
	}
	if len(repo.candidate.SourceIDs) != 1 || repo.candidate.SourceIDs[0] != "source-1" {
		t.Fatalf("candidate source IDs = %v, want one unique source ID", repo.candidate.SourceIDs)
	}
}

func TestExecuteInterruptsWhenInputIsStale(t *testing.T) {
	svc, repo := generationFixture()
	svc.Currentness = testCurrentness{current: false}
	run, err := svc.Execute(context.Background(), "run-1")
	if err != nil || run.Status != StatusInterrupted || repo.failure == nil || repo.failure.Code != "stale_input" {
		t.Fatalf("stale Execute() = (%+v, %v), failure=%+v", run, err, repo.failure)
	}
}

func TestSharedPolicyGatesGenerationPermissionAndStaleInput(t *testing.T) {
	legacy, repo := generationFixture()
	policy := &sharedGenerationPolicyStub{allowed: false, current: true}
	service := NewServiceWithAccessPolicy(policy, legacy.Inputs, legacy.Sources, legacy.Model, legacy.Repository, legacy.Enqueuer)
	request := Request{ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "Draft", ExpectedSpecRevision: 3, ExpectedChapterVersionID: stringPtr("chapter-v1")}
	if _, err := service.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "policy-gate-1", request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("shared policy denied Start() = %v, want %v", err, ErrForbidden)
	}

	policy.allowed = true
	if _, err := service.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", "policy-gate-2", request); err != nil {
		t.Fatalf("shared policy allowed Start() = %v", err)
	}
	policy.current = false
	repo.claimed = false
	repo.run.Status = StatusQueued
	run, err := service.Execute(context.Background(), repo.run.ID)
	if err != nil || run.Status != StatusInterrupted || repo.failure == nil || repo.failure.Code != "stale_input" {
		t.Fatalf("shared policy stale Execute() = (%+v, %v), failure=%+v", run, err, repo.failure)
	}
}

func TestExecuteRechecksAuthorizationAndSourcesBeforeModel(t *testing.T) {
	for name, configure := range map[string]func(*Service){
		"authorization revoked": func(svc *Service) { svc.Authorizer = testAuthorizer{err: ErrForbidden} },
		"source revoked":        func(svc *Service) { svc.Sources = testSources{err: ErrSourceAccessDenied} },
	} {
		t.Run(name, func(t *testing.T) {
			svc, repo := generationFixture()
			model := &countingModel{draft: Draft{BodyMarkdown: "Text [[source:source-1]]", Sources: []Source{{ID: "source-1"}}}}
			svc.Model = model
			configure(svc)
			run, err := svc.Execute(context.Background(), repo.run.ID)
			if err != nil || run.Status != StatusFailed || model.calls != 0 {
				t.Fatalf("Execute() = (%+v, %v), model calls=%d; want blocked before model", run, err, model.calls)
			}
		})
	}
}

func TestTaskHandlerDelaysRetryWhenLeaseIsStillLive(t *testing.T) {
	svc, repo := generationFixture()
	repo.claimed = true
	repo.run.Status = StatusRunning
	delayed := &delayedTestEnqueuer{}
	svc.Enqueuer = delayed
	handler := NewTaskHandler(svc)
	task, err := json.Marshal(generationTaskPayload{TenantID: 1, RunID: repo.run.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.Handle(context.Background(), asynq.NewTask(types.TypeLingDocGeneration, task)); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if delayed.calls != 1 || delayed.delay <= claimLeaseDuration {
		t.Fatalf("delayed retry = calls %d, delay %s; want one lease-delayed retry", delayed.calls, delayed.delay)
	}
}

func TestExecuteStoresAcceptanceStopsWithCorrectRetryability(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		status    Status
		code      string
		retryable bool
	}{
		{name: "truncated output", err: ErrGenerationIncomplete, status: StatusFailed, code: "generation_incomplete"},
		{name: "exhausted budget", err: ErrAcceptanceBudgetExceeded, status: StatusFailed, code: "generation_budget_exceeded"},
		{name: "uncertain provider outcome", err: ErrAcceptanceOutcomeUnknown, status: StatusFailed, code: "generation_outcome_unknown"},
		{name: "cancelled provider with uncertain outcome", err: errors.Join(context.Canceled, ErrAcceptanceOutcomeUnknown), status: StatusInterrupted, code: "generation_interrupted", retryable: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := generationFixture()
			svc.Model = testModel{err: tc.err}
			run, err := svc.Execute(context.Background(), repo.run.ID)
			if err != nil || run.Status != tc.status || run.Error == nil || run.Error.Code != tc.code || run.Error.Retryable != tc.retryable {
				t.Fatalf("Execute() = (%+v, %v), want status=%s code=%s retryable=%v", run, err, tc.status, tc.code, tc.retryable)
			}
		})
	}
}

// The two acceptance messages have to describe the profile that was in force
// for THAT call.
//
// A controlled window allows exactly one call, and the most likely way to
// fail is "no model row matches the authorized profile".  The message the
// operator reads then decides what they do next: a message that names a model
// from another profile sends them after a row the profile will refuse, and
// they spend the window doing it.
func TestExecuteAcceptanceMessagesFollowTheArmedProfile(t *testing.T) {
	cases := []struct {
		name        string
		profile     string
		err         error
		code        string
		wantModel   string
		wantLimit   string
		mustNotName string
	}{
		{
			name:      "qwen profile names the qwen model",
			profile:   acceptancebudget.QwenProfile,
			err:       ErrAcceptanceModelMismatch,
			code:      "generation_model_mismatch",
			wantModel: acceptancebudget.QwenChatModel,
			// The other profile's model must not appear at all: the
			// operator must not go looking for a row that the profile
			// will refuse.  The whole word is checked, not just the
			// model name.
			mustNotName: "deepseek",
		},
		{
			name:      "qwen profile names the qwen ceiling",
			profile:   acceptancebudget.QwenProfile,
			err:       ErrGenerationIncomplete,
			code:      "generation_incomplete",
			wantLimit: "4,096",
			// The DeepSeek ceiling (6,000) is a number the operator
			// would try to fit the input into.
			mustNotName: "6,000",
		},
		{
			name:        "deepseek profile still names the deepseek model",
			profile:     acceptancebudget.Profile,
			err:         ErrAcceptanceModelMismatch,
			code:        "generation_model_mismatch",
			wantModel:   acceptancebudget.DeepSeekModel,
			mustNotName: "qwen",
		},
		{
			name:      "deepseek profile keeps its ceiling",
			profile:   acceptancebudget.Profile,
			err:       ErrGenerationIncomplete,
			code:      "generation_incomplete",
			wantLimit: "6,000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(acceptancebudget.ProfileEnv, tc.profile)
			svc, repo := generationFixture()
			svc.Model = testModel{err: tc.err}
			run, err := svc.Execute(context.Background(), repo.run.ID)
			if err != nil || run.Status != StatusFailed || run.Error == nil {
				t.Fatalf("Execute() = (%+v, %v), want a failed run carrying an error", run, err)
			}
			if run.Error.Code != tc.code {
				t.Fatalf("error code = %q, want %q", run.Error.Code, tc.code)
			}
			if run.Error.Retryable {
				t.Fatalf("error is retryable; the acceptance stops are terminal")
			}
			if tc.wantModel != "" && !strings.Contains(run.Error.Message, tc.wantModel) {
				t.Fatalf("message %q does not name the profile's model %q", run.Error.Message, tc.wantModel)
			}
			if tc.wantLimit != "" && !strings.Contains(run.Error.Message, tc.wantLimit) {
				t.Fatalf("message %q does not name the profile's ceiling %q", run.Error.Message, tc.wantLimit)
			}
			if tc.mustNotName != "" && strings.Contains(run.Error.Message, tc.mustNotName) {
				t.Fatalf("message %q names %q, which belongs to another profile", run.Error.Message, tc.mustNotName)
			}
		})
	}
}

// With no profile armed the process is not in a controlled mode, and the
// messages have to stay exactly what they were: the two failures cannot even
// happen without a profile (the adapter only checks the model and the
// ceiling when one is armed), and a process that arms one later must not
// find its wording changed underneath it.
func TestExecuteAcceptanceMessagesWithoutAProfileKeepTheirPreviousWording(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
		want string
	}{
		{
			name: "model mismatch",
			err:  ErrAcceptanceModelMismatch,
			code: "generation_model_mismatch",
			want: "验收模式未找到唯一且符合条件的 deepseek-flash 配置；未调用模型。",
		},
		{
			name: "incomplete output",
			err:  ErrGenerationIncomplete,
			code: "generation_incomplete",
			want: "本轮输出达到 6,000 token 上限，结果不完整；系统不会自动续写。缩小输入后重新发起会占用下一次调用额度。",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(acceptancebudget.ProfileEnv, "")
			svc, repo := generationFixture()
			svc.Model = testModel{err: tc.err}
			run, err := svc.Execute(context.Background(), repo.run.ID)
			if err != nil || run.Status != StatusFailed || run.Error == nil {
				t.Fatalf("Execute() = (%+v, %v), want a failed run carrying an error", run, err)
			}
			if run.Error.Code != tc.code {
				t.Fatalf("error code = %q, want %q", run.Error.Code, tc.code)
			}
			if run.Error.Message != tc.want {
				t.Fatalf("message = %q, want the previous wording %q", run.Error.Message, tc.want)
			}
		})
	}
}

func TestFormatTokenCountWritesThousandsSeparators(t *testing.T) {
	cases := map[int]string{0: "0", 999: "999", 4096: "4,096", 6000: "6,000", 16000: "16,000"}
	for count, want := range cases {
		if got := formatTokenCount(count); got != want {
			t.Fatalf("formatTokenCount(%d) = %q, want %q", count, got, want)
		}
	}
}

func TestExecuteStoresProviderCancellationAsInterrupted(t *testing.T) {
	svc, repo := generationFixture()
	svc.Model = testModel{err: context.Canceled}

	run, err := svc.Execute(context.Background(), repo.run.ID)
	if err != nil || run.Status != StatusInterrupted || run.Error == nil || run.Error.Code != "generation_interrupted" || !run.Error.Retryable {
		t.Fatalf("Execute() = (%+v, %v), want retryable interrupted result", run, err)
	}
}

func TestExecuteStoresProviderTimeoutAsInterrupted(t *testing.T) {
	svc, repo := generationFixture()
	svc.Model = deadlineTestModel{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	run, err := svc.Execute(ctx, repo.run.ID)
	if err != nil || run.Status != StatusInterrupted || run.Error == nil || run.Error.Code != "generation_interrupted" || !run.Error.Retryable {
		t.Fatalf("Execute() = (%+v, %v), want retryable interrupted result after provider deadline", run, err)
	}
}

func stringPtr(s string) *string { return &s }
