package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// replaySourceValidator counts the source validations the replay seam asks
// for and answers with a fixed error.
type replaySourceValidator struct {
	err   error
	calls int
}

func (v *replaySourceValidator) ValidateGenerationSources(context.Context, Actor, string, []Source) error {
	v.calls++
	return v.err
}

// replayEvidenceValidator answers the evidence check and remembers which
// evidence cards it was asked about.
//
// It rejects ONE card id on purpose. The request's card and the stored run's
// card are different, so a test can tell the check on the freshly resolved
// request apart from the check on the STORED input — the layer the replay
// seam owns.
type replayEvidenceValidator struct {
	rejectCardID string
	seenCardIDs  []string
}

func (v *replayEvidenceValidator) ValidateGenerationEvidence(_ context.Context, _ Actor, _, _ string, refs []EvidenceCardRef, _ []Source) error {
	for _, ref := range refs {
		v.seenCardIDs = append(v.seenCardIDs, ref.ID)
		if ref.ID == v.rejectCardID {
			return ErrSourceAccessDenied
		}
	}
	return nil
}

func (v *replayEvidenceValidator) ValidateGenerationEvidenceConstraints(_ context.Context, _ Actor, _, _ string, refs []EvidenceCardRef, values []EvidenceConstraint) error {
	if len(values) != len(refs) {
		return ErrSourceAccessDenied
	}
	for i, value := range values {
		if value.CardRef != refs[i] {
			return ErrSourceAccessDenied
		}
	}
	return nil
}

// replayEvidenceInput builds the input of a run that was started against a
// formal evidence card.
func replayEvidenceInput(actor Actor, projectID, chapterID, cardID string) Input {
	input := replayTestInput(actor, projectID, chapterID)
	ref := EvidenceCardRef{ID: cardID, Version: 2}
	input.EvidenceCardRefs = []EvidenceCardRef{ref}
	input.Request.EvidenceCardRefs = []EvidenceCardRef{ref}
	input.EvidenceConstraints = []EvidenceConstraint{{CardRef: ref, SupportType: "qualifies",
		Scope: "仅合成样本", Limitations: "不证明因果", SourceIDs: []string{"source-1"}}}
	return input
}

// replayEvidenceRequest builds a request that names a formal evidence card.
func replayEvidenceRequest(chapterID, cardID string) Request {
	request := replayTestRequest(chapterID)
	request.EvidenceCardRefs = []EvidenceCardRef{{ID: cardID, Version: 2}}
	return request
}

// The terminal set is the whole of the contract: a run that has already run
// (succeeded, failed, interrupted) is read back without asking whether its
// inputs are still current, because the currentness check guards a
// generation that is ABOUT TO RUN and a terminal run can never run again.
//
// Every terminal state is pinned here: shrinking the set to {succeeded} must
// turn the failed and the interrupted case red.
func TestReplayOfTerminalRunsSkipsTheCurrentnessCheck(t *testing.T) {
	for _, status := range []Status{StatusSucceeded, StatusFailed, StatusInterrupted} {
		t.Run(string(status), func(t *testing.T) {
			actor := Actor{TenantID: 1, UserID: "user-1"}
			request := replayTestRequest("chapter-1")
			input := replayTestInput(actor, "project-1", "chapter-1")
			repo := &replayRepository{storedInput: input}
			repo.run = Run{ID: "run-1", ProjectID: "project-1", ChapterID: "chapter-1", Status: status}
			repo.idempotencyKey = "f01-generate-0001"
			repo.requestHash = replayRequestHash(t, request)
			// The workspace moved on: the captured inputs are stale.
			policy := &replayPolicyStub{allowed: true, current: false}
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
			if policy.currentCalls != 0 {
				t.Fatalf("currentness calls = %d, want a terminal run to skip the currentness check", policy.currentCalls)
			}
			if repo.createRunCalls != 0 {
				t.Fatalf("createRunCalls = %d, want no second run", repo.createRunCalls)
			}
			if model.calls != 0 {
				t.Fatalf("model calls = %d, want the replay to reach no model", model.calls)
			}
			if enqueuer.calls != 0 {
				t.Fatalf("enqueuer calls = %d, want a terminal run to be re-armed zero times", enqueuer.calls)
			}
		})
	}
}

// The other half of the same boundary: a run that can still turn into a
// provider call keeps the currentness check. A queued run is re-armed and
// then runs, so stale inputs must be refused before anything is enqueued.
func TestReplayOfNonTerminalRunsStillChecksCurrentness(t *testing.T) {
	for _, status := range []Status{StatusQueued, StatusRunning} {
		t.Run(string(status), func(t *testing.T) {
			actor := Actor{TenantID: 1, UserID: "user-1"}
			request := replayTestRequest("chapter-1")
			input := replayTestInput(actor, "project-1", "chapter-1")
			repo := &replayRepository{storedInput: input}
			repo.run = Run{ID: "run-1", ProjectID: "project-1", ChapterID: "chapter-1", Status: status}
			repo.idempotencyKey = "f01-generate-0001"
			repo.requestHash = replayRequestHash(t, request)
			policy := &replayPolicyStub{allowed: true, current: false}
			model := &countingModel{}
			enqueuer := &testEnqueuer{}
			svc := replayService(policy, repo, input, model, enqueuer)

			if _, err := svc.Start(context.Background(), actor, "project-1", "f01-generate-0001", request); !errors.Is(err, ErrStaleInput) {
				t.Fatalf("Start replay error = %v, want the stale input refusal", err)
			}
			if policy.currentCalls != 1 {
				t.Fatalf("currentness calls = %d, want the shared policy to answer once", policy.currentCalls)
			}
			if enqueuer.calls != 0 {
				t.Fatalf("enqueuer calls = %d, want a stale run to be re-armed zero times", enqueuer.calls)
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

// The source check belongs to the replay seam and runs unconditionally.
//
// Start does NOT validate the sources of a replayed run — only Execute does,
// and Execute is not on the replay path — so this is the only source check
// the replay has. Dropping it leaves the replay with no source line of
// defence at all, and every status must turn red without it.
func TestReplayStillValidatesTheSources(t *testing.T) {
	for _, status := range []Status{StatusSucceeded, StatusFailed, StatusInterrupted, StatusQueued} {
		t.Run(string(status), func(t *testing.T) {
			actor := Actor{TenantID: 1, UserID: "user-1"}
			request := replayTestRequest("chapter-1")
			input := replayTestInput(actor, "project-1", "chapter-1")
			repo := &replayRepository{storedInput: input}
			repo.run = Run{ID: "run-1", ProjectID: "project-1", ChapterID: "chapter-1", Status: status}
			repo.idempotencyKey = "f01-generate-0001"
			repo.requestHash = replayRequestHash(t, request)
			policy := &replayPolicyStub{allowed: true, current: true}
			sources := &replaySourceValidator{err: ErrSourceAccessDenied}
			model := &countingModel{}
			enqueuer := &testEnqueuer{}
			svc := replayService(policy, repo, input, model, enqueuer)
			svc.Sources = sources

			if _, err := svc.Start(context.Background(), actor, "project-1", "f01-generate-0001", request); !errors.Is(err, ErrSourceAccessDenied) {
				t.Fatalf("Start replay error = %v, want the source refusal", err)
			}
			if sources.calls != 1 {
				t.Fatalf("source validations = %d, want the stored sources validated once", sources.calls)
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

// The evidence check on the STORED input is the replay seam's own check.
//
// Start validates the evidence of the freshly resolved request, but that says
// nothing about the evidence the stored run was actually started against. The
// request's card and the stored run's card are different on purpose: the
// check on the request passes, so the only thing that can refuse this replay
// is the check on the stored input.
func TestReplayStillValidatesTheStoredEvidence(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "user-1"}
	request := replayEvidenceRequest("chapter-1", "card-request")
	resolved := replayEvidenceInput(actor, "project-1", "chapter-1", "card-request")
	stored := replayEvidenceInput(actor, "project-1", "chapter-1", "card-stored")
	repo := &replayRepository{storedInput: stored}
	repo.run = Run{ID: "run-1", ProjectID: "project-1", ChapterID: "chapter-1", Status: StatusSucceeded}
	repo.idempotencyKey = "f01-generate-0001"
	repo.requestHash = replayRequestHash(t, request)
	policy := &replayPolicyStub{allowed: true, current: true}
	evidence := &replayEvidenceValidator{rejectCardID: "card-stored"}
	model := &countingModel{}
	enqueuer := &testEnqueuer{}
	svc := replayService(policy, repo, resolved, model, enqueuer)
	svc.Evidence = evidence

	if _, err := svc.Start(context.Background(), actor, "project-1", "f01-generate-0001", request); !errors.Is(err, ErrSourceAccessDenied) {
		t.Fatalf("Start replay error = %v, want the stored evidence refusal", err)
	}
	if len(evidence.seenCardIDs) != 2 || evidence.seenCardIDs[0] != "card-request" || evidence.seenCardIDs[1] != "card-stored" {
		t.Fatalf("evidence cards checked = %v, want the request's card and the stored card", evidence.seenCardIDs)
	}
	if repo.createRunCalls != 0 {
		t.Fatalf("createRunCalls = %d, want no second run", repo.createRunCalls)
	}
	if model.calls != 0 {
		t.Fatalf("model calls = %d, want the refused replay to reach no model", model.calls)
	}
}

// The HTTP seam is where the contract is promised: the same idempotency key
// with the same body answers 202 the second time, marked as a replay, with
// the same run and no second run and no model call.
func TestHTTPReplayOfTheSameIdempotencyKeyReturnsTheSameRun(t *testing.T) {
	gin.SetMode(gin.TestMode)
	actor := Actor{TenantID: 1, UserID: "user-1"}
	request := replayTestRequest("chapter-1")
	input := replayTestInput(actor, "project-1", "chapter-1")
	repo := &replayRepository{storedInput: input}
	policy := &replayPolicyStub{allowed: true, current: true}
	model := &countingModel{}
	enqueuer := &testEnqueuer{}
	svc := replayService(policy, repo, input, model, enqueuer)
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1/lingdoc"), NewHandler(svc, func(*gin.Context) (Actor, bool) {
		return actor, true
	}))
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	first := httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/project-1/generations", bytes.NewReader(body))
	first.Header.Set("Idempotency-Key", "f01-generate-0001")
	firstResponse := httptest.NewRecorder()
	router.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusAccepted {
		t.Fatalf("first request status = %d, want %d; body=%s", firstResponse.Code, http.StatusAccepted, firstResponse.Body.String())
	}
	firstEnvelope := decodeGenerationEnvelope(t, firstResponse)
	if firstEnvelope.Meta.Replayed {
		t.Fatal("the first request is not a replay")
	}

	second := httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/project-1/generations", bytes.NewReader(body))
	second.Header.Set("Idempotency-Key", "f01-generate-0001")
	secondResponse := httptest.NewRecorder()
	router.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusAccepted {
		t.Fatalf("replayed request status = %d, want %d; body=%s", secondResponse.Code, http.StatusAccepted, secondResponse.Body.String())
	}
	secondEnvelope := decodeGenerationEnvelope(t, secondResponse)
	if !secondEnvelope.Meta.Replayed {
		t.Fatal("the replayed request is not marked as a replay")
	}
	if secondEnvelope.Data.ID != firstEnvelope.Data.ID {
		t.Fatalf("replayed run id = %q, want the same run %q", secondEnvelope.Data.ID, firstEnvelope.Data.ID)
	}
	if repo.createRunCalls != 1 {
		t.Fatalf("createRunCalls = %d, want one run for two requests", repo.createRunCalls)
	}
	if model.calls != 0 {
		t.Fatalf("model calls = %d, want the replay to reach no model", model.calls)
	}
	if enqueuer.calls != 2 {
		t.Fatalf("enqueuer calls = %d, want the queued run re-armed once and not duplicated", enqueuer.calls)
	}
	if repo.candidate.ID != "" {
		t.Fatalf("candidate id = %q, want the replay to write no candidate", repo.candidate.ID)
	}
}

// The replay reads an existing result back; it never writes a second one.
//
// The run is executed first, so a candidate exists: the replay must leave
// that candidate exactly as it was, and must not call the model again.
func TestReplayWritesNoCandidate(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "user-1"}
	request := replayTestRequest("chapter-1")
	input := replayTestInput(actor, "project-1", "chapter-1")
	repo := &replayRepository{storedInput: input}
	policy := &replayPolicyStub{allowed: true, current: true}
	model := &countingModel{draft: Draft{BodyMarkdown: "Text [[source:source-1]]", Sources: []Source{{ID: "source-1"}}}}
	enqueuer := &testEnqueuer{}
	svc := replayService(policy, repo, input, model, enqueuer)
	candidates := 0
	svc.NewID = func() string {
		candidates++
		return fmt.Sprintf("candidate-%d", candidates)
	}

	run, err := svc.Start(context.Background(), actor, "project-1", "f01-generate-0001", request)
	if err != nil {
		t.Fatalf("Start error = %v", err)
	}
	executed, err := svc.Execute(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if executed.Status != StatusSucceeded {
		t.Fatalf("executed run = %+v, want it succeeded", executed)
	}
	written := repo.candidate.ID
	if written == "" {
		t.Fatal("the executed run wrote no candidate")
	}

	replayed, err := svc.Start(context.Background(), actor, "project-1", "f01-generate-0001", request)
	if err != nil {
		t.Fatalf("Start replay error = %v", err)
	}
	if !replayed.Replayed || replayed.ID != run.ID {
		t.Fatalf("replayed run = %+v, want the same run marked as a replay", replayed)
	}
	if repo.candidate.ID != written {
		t.Fatalf("candidate id = %q, want the replay to keep the candidate %q", repo.candidate.ID, written)
	}
	if candidates != 1 {
		t.Fatalf("candidates written = %d, want the replay to write none", candidates)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want only the call of the original execution", model.calls)
	}
}

// generationEnvelope is the shape the generation HTTP seam answers with.
type generationEnvelope struct {
	Data Run `json:"data"`
	Meta struct {
		Replayed        bool `json:"replayed"`
		RefreshRequired bool `json:"refresh_required"`
	} `json:"meta"`
}

func decodeGenerationEnvelope(t *testing.T, response *httptest.ResponseRecorder) generationEnvelope {
	t.Helper()
	var envelope generationEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, response.Body.String())
	}
	return envelope
}
