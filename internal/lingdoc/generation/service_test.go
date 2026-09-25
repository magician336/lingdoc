package generation

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
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

type testSources struct{ err error }

func (v testSources) ValidateGenerationSources(context.Context, Actor, string, []Source) error {
	return v.err
}

type testCurrentness struct {
	current bool
	err     error
}

func (c testCurrentness) GenerationInputIsCurrent(context.Context, Actor, string, string, candidateadoption.Basis) (bool, error) {
	return c.current, c.err
}

type testModel struct {
	draft Draft
	err   error
}

func (m testModel) Generate(context.Context, Input) (Draft, error) { return m.draft, m.err }

type testEnqueuer struct{ calls int }

func (e *testEnqueuer) EnqueueGeneration(context.Context, uint64, string) error {
	e.calls++
	return nil
}

type testRepository struct {
	run            Run
	input          Input
	idempotencyKey string
	requestHash    string
	claimed        bool
	candidate      candidateadoption.Candidate
	failure        *RunError
}

func (r *testRepository) FindReplay(_ context.Context, _ Actor, _ string, key, hash string) (Run, bool, error) {
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
		testModel{draft: Draft{BodyMarkdown: "Text [[source:source-1]]", Sources: []Source{{ID: "source-1"}}}}, repo)
	svc.Enqueuer = &testEnqueuer{}
	svc.NewID = func() string { return "candidate-1" }
	return svc, repo
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

func stringPtr(s string) *string { return &s }
