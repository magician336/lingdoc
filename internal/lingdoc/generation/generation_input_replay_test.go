package generation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	lingdocaccess "github.com/Tencent/WeKnora/internal/lingdoc/access"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
)

// replayPolicyStub is the shape the production assembly hands the service:
// the shared access policy, with NO legacy currentness port behind it.
//
// The stub counts how often the currentness capability was consulted, so a
// test can tell "the check said no" from "the check was never asked".
type replayPolicyStub struct {
	allowed      bool
	current      bool
	currentCalls int
}

func (p *replayPolicyStub) Authorize(context.Context, lingdocaccess.LingDocAccessContext, string) error {
	if !p.allowed {
		return ErrForbidden
	}
	return nil
}

func (p *replayPolicyStub) Current(context.Context, lingdocaccess.LingDocAccessContext, lingdocaccess.CurrentnessInput) (bool, error) {
	p.currentCalls++
	return p.current, nil
}

// AuthorizeHumanGeneration mirrors the production policy: a human initiation
// is required before any generation, replayed or not.
func (p *replayPolicyStub) AuthorizeHumanGeneration(context.Context) error { return nil }

// replayRepository answers the replay questions and reads the stored input
// back.
//
// The stored input comes back whichever actor asks for it, so the ownership
// check under test is the service's own check and not the store's query.
type replayRepository struct {
	testRepository
	storedInput Input
	loadCalls   int
}

func (r *replayRepository) LoadGenerationInput(context.Context, Actor, string, string) (Input, error) {
	r.loadCalls++
	return r.storedInput, nil
}

// budgetGatedModel mirrors the production model adapter's budget gate: the
// reservation is taken BEFORE the provider is called, and a refused
// reservation never reaches a provider.
type budgetGatedModel struct {
	budget        *acceptancebudget.Budget
	providerCalls int
}

func (m *budgetGatedModel) RunMode() string { return "mock" }

func (m *budgetGatedModel) Generate(ctx context.Context, _ Input) (Draft, error) {
	reservation, err := m.budget.Reserve(ctx, acceptancebudget.QwenChatModel, 2048, 4096)
	if err != nil {
		if errors.Is(err, acceptancebudget.ErrCallsSpent) || errors.Is(err, acceptancebudget.ErrTokenBudgetExhausted) {
			return Draft{}, ErrAcceptanceBudgetExceeded
		}
		return Draft{}, ErrAcceptanceBudgetUnavailable
	}
	m.providerCalls++
	if err := reservation.Complete("response_received", 1024, 512, "stop", false); err != nil {
		return Draft{}, err
	}
	return Draft{BodyMarkdown: "Text [[source:source-1]]", Sources: []Source{{ID: "source-1"}}}, nil
}

func replayTestInput(actor Actor, projectID, chapterID string) Input {
	chapterVersion := "chapter-v1"
	basis := candidateadoption.Basis{SpecRevision: 3, ChapterVersionID: &chapterVersion,
		AssetVersions: []candidateadoption.AssetVersion{{AssetID: "asset-1", AssetRevision: 2}}}
	return Input{
		Actor:          actor,
		HumanInitiated: true,
		Basis:          basis,
		Workspace: candidateadoption.GenerationContext{ProjectID: projectID, ChapterID: chapterID,
			SpecRevision: 3, ChapterVersionID: &chapterVersion, Basis: basis},
		Sources: []Source{{ID: "source-1", ProjectID: projectID, AssetID: "asset-1", AssetRevision: 2, Status: "available"}},
		Request: replayTestRequest(chapterID),
	}
}

func replayTestRequest(chapterID string) Request {
	chapterVersion := "chapter-v1"
	return Request{ChapterID: chapterID, AssetIDs: []string{"asset-1"},
		Instruction: "写这一章。", ExpectedSpecRevision: 3, ExpectedChapterVersionID: &chapterVersion}
}

func replayRequestHash(t *testing.T, request Request) string {
	t.Helper()
	hash, err := operationcore.RequestHash(request)
	if err != nil {
		t.Fatalf("request hash error = %v", err)
	}
	return hash
}

// replayService wires the service the way the production assembly wires it:
// the shared access policy, no legacy currentness port.
func replayService(policy *replayPolicyStub, repo *replayRepository, input Input, model ModelAdapter, enqueuer Enqueuer) *Service {
	svc := NewServiceWithAccessPolicy(policy, testInputs{input: input}, testSources{}, model, repo, enqueuer)
	svc.RequireHumanInitiation = true
	return svc
}

// ledgerRequestCount reads how many rows the acceptance ledger holds.
func ledgerRequestCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read acceptance ledger: %v", err)
	}
	var ledger struct {
		Requests []json.RawMessage `json:"requests"`
	}
	if err := json.Unmarshal(raw, &ledger); err != nil {
		t.Fatalf("decode acceptance ledger: %v", err)
	}
	return len(ledger.Requests)
}

// A succeeded run is a fact. Replaying it reads that fact back: the same run
// comes back, no new run is created, and nothing reaches the model.
//
// The captured inputs are stale here (the workspace moved on after the run),
// and that does not stop the replay: the check that guards an imminent
// generation has nothing left to protect once the run is over.
func TestReplayOfSucceededRunReusesTheSameRunWithoutCallingTheModel(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "user-1"}
	request := replayTestRequest("chapter-1")
	input := replayTestInput(actor, "project-1", "chapter-1")
	repo := &replayRepository{storedInput: input}
	repo.run = Run{ID: "run-1", ProjectID: "project-1", ChapterID: "chapter-1", Status: StatusSucceeded}
	repo.idempotencyKey = "f01-generate-0001"
	repo.requestHash = replayRequestHash(t, request)
	policy := &replayPolicyStub{allowed: true, current: false}
	model := &countingModel{}
	enqueuer := &testEnqueuer{}
	svc := replayService(policy, repo, input, model, enqueuer)

	run, err := svc.Start(context.Background(), actor, "project-1", "f01-generate-0001", request)
	if err != nil {
		t.Fatalf("Start replay error = %v, want the existing run back", err)
	}
	if run.ID != "run-1" {
		t.Fatalf("replayed run id = %q, want the same run %q", run.ID, "run-1")
	}
	if !run.Replayed {
		t.Fatal("replayed run is not marked as a replay")
	}
	if repo.createRunCalls != 0 {
		t.Fatalf("createRunCalls = %d, want no second run", repo.createRunCalls)
	}
	if model.calls != 0 {
		t.Fatalf("model calls = %d, want the replay to reach no model", model.calls)
	}
	if enqueuer.calls != 0 {
		t.Fatalf("enqueuer calls = %d, want a finished run to be re-armed zero times", enqueuer.calls)
	}
	if policy.currentCalls != 0 {
		t.Fatalf("currentness calls = %d, want a terminal run to skip the currentness check", policy.currentCalls)
	}
}

// The production assembly carries the shared access policy and no legacy
// currentness port. A replay under that assembly must read the currentness
// capability through the policy, exactly like Execute does: a queued run with
// CURRENT inputs is re-armed and returned, not refused.
func TestReplayOfQueuedRunUnderTheProductionAssemblyReusesTheSameRun(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "user-1"}
	request := replayTestRequest("chapter-1")
	input := replayTestInput(actor, "project-1", "chapter-1")
	repo := &replayRepository{storedInput: input}
	repo.run = Run{ID: "run-1", ProjectID: "project-1", ChapterID: "chapter-1", Status: StatusQueued}
	repo.idempotencyKey = "f01-generate-0001"
	repo.requestHash = replayRequestHash(t, request)
	policy := &replayPolicyStub{allowed: true, current: true}
	model := &countingModel{}
	enqueuer := &testEnqueuer{}
	svc := replayService(policy, repo, input, model, enqueuer)

	run, err := svc.Start(context.Background(), actor, "project-1", "f01-generate-0001", request)
	if err != nil {
		t.Fatalf("Start replay error = %v, want the existing run back", err)
	}
	if run.ID != "run-1" || !run.Replayed {
		t.Fatalf("replayed run = %+v, want the same run marked as a replay", run)
	}
	if repo.createRunCalls != 0 {
		t.Fatalf("createRunCalls = %d, want no second run", repo.createRunCalls)
	}
	if model.calls != 0 {
		t.Fatalf("model calls = %d, want the replay to reach no model", model.calls)
	}
	if enqueuer.calls != 1 {
		t.Fatalf("enqueuer calls = %d, want the queued run to be re-armed once", enqueuer.calls)
	}
	if policy.currentCalls != 1 {
		t.Fatalf("currentness calls = %d, want the shared policy to answer once", policy.currentCalls)
	}
}

// A queued run can still turn into a provider call, so the currentness check
// still governs its replay: stale inputs are refused, and nothing is enqueued.
func TestReplayOfQueuedRunWithStaleInputsIsRefused(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "user-1"}
	request := replayTestRequest("chapter-1")
	input := replayTestInput(actor, "project-1", "chapter-1")
	repo := &replayRepository{storedInput: input}
	repo.run = Run{ID: "run-1", ProjectID: "project-1", ChapterID: "chapter-1", Status: StatusQueued}
	repo.idempotencyKey = "f01-generate-0001"
	repo.requestHash = replayRequestHash(t, request)
	policy := &replayPolicyStub{allowed: true, current: false}
	model := &countingModel{}
	enqueuer := &testEnqueuer{}
	svc := replayService(policy, repo, input, model, enqueuer)

	if _, err := svc.Start(context.Background(), actor, "project-1", "f01-generate-0001", request); !errors.Is(err, ErrStaleInput) {
		t.Fatalf("Start replay error = %v, want the stale input refusal", err)
	}
	if enqueuer.calls != 0 {
		t.Fatalf("enqueuer calls = %d, want a stale queued run to be re-armed zero times", enqueuer.calls)
	}
	if repo.createRunCalls != 0 {
		t.Fatalf("createRunCalls = %d, want no second run", repo.createRunCalls)
	}
	if model.calls != 0 {
		t.Fatalf("model calls = %d, want the refused replay to reach no model", model.calls)
	}
}

// A run belongs to the actor that started it. Another tenant, another user,
// another project or another chapter may not replay it.
func TestReplayOfRunOwnedByAnotherActorIsForbidden(t *testing.T) {
	cases := []struct {
		name          string
		actor         Actor
		projectID     string
		storedChapter string
	}{
		{name: "another tenant", actor: Actor{TenantID: 2, UserID: "user-1"}, projectID: "project-1", storedChapter: "chapter-1"},
		{name: "another user", actor: Actor{TenantID: 1, UserID: "user-2"}, projectID: "project-1", storedChapter: "chapter-1"},
		{name: "another project", actor: Actor{TenantID: 1, UserID: "user-1"}, projectID: "project-2", storedChapter: "chapter-1"},
		{name: "another chapter", actor: Actor{TenantID: 1, UserID: "user-1"}, projectID: "project-1", storedChapter: "chapter-2"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			owner := Actor{TenantID: 1, UserID: "user-1"}
			request := replayTestRequest("chapter-1")
			input := replayTestInput(owner, "project-1", testCase.storedChapter)
			repo := &replayRepository{storedInput: input}
			repo.run = Run{ID: "run-1", ProjectID: "project-1", ChapterID: "chapter-1", Status: StatusSucceeded}
			repo.idempotencyKey = "f01-generate-0001"
			repo.requestHash = replayRequestHash(t, request)
			policy := &replayPolicyStub{allowed: true, current: true}
			model := &countingModel{}
			enqueuer := &testEnqueuer{}
			svc := replayService(policy, repo, input, model, enqueuer)

			if _, err := svc.Start(context.Background(), testCase.actor, testCase.projectID, "f01-generate-0001", request); !errors.Is(err, ErrForbidden) {
				t.Fatalf("Start replay error = %v, want the ownership refusal", err)
			}
			if repo.createRunCalls != 0 {
				t.Fatalf("createRunCalls = %d, want no second run", repo.createRunCalls)
			}
			if model.calls != 0 {
				t.Fatalf("model calls = %d, want the refused replay to reach no model", model.calls)
			}
		})
	}
}

// The budget gate is not touched by the replay fix: a NEW key still has to
// pass the budget before anything reaches a provider, and a refused
// reservation leaves no row in the ledger.
func TestNewGenerationRequestIsRefusedWhenTheAcceptanceBudgetIsSpent(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "calls.json")
	budget := acceptancebudget.New(acceptancebudget.QwenProfile, ledgerPath)
	reservation, err := budget.Reserve(context.Background(), acceptancebudget.QwenChatModel, 2048, 4096)
	if err != nil {
		t.Fatalf("first reserve error = %v", err)
	}
	if err := reservation.Complete("response_received", 1024, 512, "stop", false); err != nil {
		t.Fatalf("complete error = %v", err)
	}
	if rows := ledgerRequestCount(t, ledgerPath); rows != 1 {
		t.Fatalf("ledger rows = %d, want the spent call to be recorded once", rows)
	}

	actor := Actor{TenantID: 1, UserID: "user-1"}
	request := replayTestRequest("chapter-1")
	input := replayTestInput(actor, "project-1", "chapter-1")
	repo := &replayRepository{storedInput: input}
	repo.idempotencyKey = "f01-generate-0002"
	repo.requestHash = replayRequestHash(t, request)
	policy := &replayPolicyStub{allowed: true, current: true}
	model := &budgetGatedModel{budget: budget}
	enqueuer := &testEnqueuer{}
	svc := replayService(policy, repo, input, model, enqueuer)

	run, err := svc.Start(context.Background(), actor, "project-1", "f01-generate-0002", request)
	if err != nil {
		t.Fatalf("Start error = %v, want the new run to be queued", err)
	}
	if run.Status != StatusQueued {
		t.Fatalf("new run status = %q, want it queued", run.Status)
	}

	failed, err := svc.Execute(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Execute error = %v, want the run to fail on the budget refusal", err)
	}
	if failed.Status != StatusFailed {
		t.Fatalf("run status = %q, want the budget refusal to fail the run", failed.Status)
	}
	if failed.Error == nil || failed.Error.Code != "generation_budget_exceeded" {
		t.Fatalf("run error = %+v, want the budget refusal", failed.Error)
	}
	if model.providerCalls != 0 {
		t.Fatalf("provider calls = %d, want the spent budget to stop the provider call", model.providerCalls)
	}
	if rows := ledgerRequestCount(t, ledgerPath); rows != 1 {
		t.Fatalf("ledger rows = %d, want the refused reservation to record no row", rows)
	}
}

// The replay is a read of an existing fact: it reaches no provider and spends
// no budget, so the ledger stays exactly as it was.
func TestReplayOfSucceededRunSpendsNoAcceptanceBudget(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "calls.json")
	budget := acceptancebudget.New(acceptancebudget.QwenProfile, ledgerPath)
	reservation, err := budget.Reserve(context.Background(), acceptancebudget.QwenChatModel, 2048, 4096)
	if err != nil {
		t.Fatalf("first reserve error = %v", err)
	}
	if err := reservation.Complete("response_received", 1024, 512, "stop", false); err != nil {
		t.Fatalf("complete error = %v", err)
	}

	actor := Actor{TenantID: 1, UserID: "user-1"}
	request := replayTestRequest("chapter-1")
	input := replayTestInput(actor, "project-1", "chapter-1")
	repo := &replayRepository{storedInput: input}
	repo.run = Run{ID: "run-1", ProjectID: "project-1", ChapterID: "chapter-1", Status: StatusSucceeded}
	repo.idempotencyKey = "f01-generate-0001"
	repo.requestHash = replayRequestHash(t, request)
	policy := &replayPolicyStub{allowed: true, current: false}
	model := &budgetGatedModel{budget: budget}
	enqueuer := &testEnqueuer{}
	svc := replayService(policy, repo, input, model, enqueuer)

	run, err := svc.Start(context.Background(), actor, "project-1", "f01-generate-0001", request)
	if err != nil {
		t.Fatalf("Start replay error = %v, want the existing run back", err)
	}
	if run.ID != "run-1" || !run.Replayed {
		t.Fatalf("replayed run = %+v, want the same run marked as a replay", run)
	}
	if model.providerCalls != 0 {
		t.Fatalf("provider calls = %d, want the replay to reach no provider", model.providerCalls)
	}
	if rows := ledgerRequestCount(t, ledgerPath); rows != 1 {
		t.Fatalf("ledger rows = %d, want the replay to spend no budget", rows)
	}
}
