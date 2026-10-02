package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"
)

type sourcePolicyStub struct {
	err   error
	calls int
}

func (s *sourcePolicyStub) Validate(context.Context, string, string, []string) error {
	s.calls++
	return s.err
}
func TestServiceSourceRecheckBeforeReplayWithNonSQLStorage(t *testing.T) {
	r := fakeWorkspace()
	policy := &sourcePolicyStub{}
	s := NewServiceWithSources(r, ContractDemoTemplate{}, policy)
	actor := Actor{TenantID: 1, UserID: "owner"}
	in := SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "draft [[source:s1]]", SourceIDs: []string{"s1"}}
	body, _, replayed, err := s.SaveChapter(context.Background(), actor, "p", "c", "source-save", in)
	if err != nil || replayed || r.writes != 1 || policy.calls != 1 {
		t.Fatalf("initial save: %v %v", replayed, err)
	}
	policy.err = errors.New("source changed or revoked")
	if _, _, _, err := s.SaveChapter(context.Background(), actor, "p", "c", "source-save", in); !errors.Is(err, policy.err) {
		t.Fatalf("source-invalid replay returned saved content: %v", err)
	}
	if r.writes != 1 || policy.calls != 2 {
		t.Fatal("failed recheck mutated state or skipped policy")
	}
	policy.err = nil
	again, _, replayed, err := s.SaveChapter(context.Background(), actor, "p", "c", "source-save", in)
	if err != nil || !replayed || string(again) != string(body) || r.writes != 1 {
		t.Fatalf("restored replay: %v %v", replayed, err)
	}
	r.active = false
	previousCalls := policy.calls
	if _, _, _, err := s.SaveChapter(context.Background(), actor, "p", "c", "source-save", in); !errors.Is(err, ErrNotFound) || policy.calls != previousCalls {
		t.Fatalf("project authorization did not precede source access: %v", err)
	}
}

// A stateful non-SQL adapter. The SAME Service owns every rule, and rollback
// discards both domain writes and replay records on any callback error.
type stateRepository struct {
	project    Project
	chapter    Chapter
	active     bool
	operations map[OperationIdentity]OperationResult
	writes     int
	failCommit bool
}

func (r *stateRepository) Transaction(ctx context.Context, _ TransactionOptions, fn func(Transaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx := *r
	tx.project.Spec = maps.Clone(r.project.Spec)
	tx.operations = maps.Clone(r.operations)
	if err := fn(&stateTransaction{state: &tx}); err != nil {
		return err
	}
	if r.failCommit {
		return ErrRequestInProgress
	}
	*r = tx
	return nil
}

type stateTransaction struct {
	Transaction
	state *stateRepository
}

func (t *stateTransaction) ActiveMember(Actor) (bool, error) { return t.state.active, nil }
func (t *stateTransaction) Project(tenant uint64, id string) (Project, error) {
	if tenant != 1 || t.state.project.ID != id {
		return Project{}, ErrNotFound
	}
	return t.state.project, nil
}
func (t *stateTransaction) Chapter(project, id string) (Chapter, error) {
	if project != t.state.chapter.ProjectID || id != t.state.chapter.ID {
		return Chapter{}, ErrNotFound
	}
	return t.state.chapter, nil
}
func (t *stateTransaction) UpdateProject(_ uint64, previous, next Project) error {
	if t.state.project.ProjectVersion != previous.ProjectVersion {
		return ErrVersionConflict
	}
	t.state.project = next
	return nil
}
func (t *stateTransaction) AppendChapter(previous, next Chapter, _ int64) error {
	if !sameVersion(previous.CurrentVersionID, t.state.chapter.CurrentVersionID) {
		return ErrVersionConflict
	}
	t.state.chapter = next
	t.state.writes++
	return nil
}
func (t *stateTransaction) Operation(id OperationIdentity) (OperationResult, bool, error) {
	result, found := t.state.operations[id]
	return result, found, nil
}
func (t *stateTransaction) SaveOperation(id OperationIdentity, result OperationResult) error {
	t.state.operations[id] = result
	return nil
}
func fakeWorkspace() *stateRepository {
	return &stateRepository{active: true, operations: map[OperationIdentity]OperationResult{}, project: Project{ID: "p", Status: "active", ProjectVersion: 1, SpecRevision: 1, Spec: map[string]string{}, Members: []Member{{UserID: "owner", Role: "owner"}}}, chapter: Chapter{ID: "c", ProjectID: "p", SourceIDs: []string{}, ReviewItems: []ReviewItem{{ID: "review", Statement: "check me", OriginCandidateID: "candidate"}}}}
}
func TestServiceRulesWithNonSQLStorage(t *testing.T) {
	r := fakeWorkspace()
	s := NewService(r)
	actor := Actor{TenantID: 1, UserID: "owner"}
	ctx := context.Background()
	input := SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "first draft", SourceIDs: []string{}}
	raw, code, replay, err := s.SaveChapter(ctx, actor, "p", "c", "chapter-save", input)
	if err != nil || code != 201 || replay || r.writes != 1 {
		t.Fatalf("save: %s %d %v %v", raw, code, replay, err)
	}
	var chapter Chapter
	if err := json.Unmarshal(raw, &chapter); err != nil {
		t.Fatal(err)
	}
	if chapter.CurrentVersionID == nil || len(chapter.ReviewItems) != 1 || chapter.ConfirmationValid {
		t.Fatalf("immutable version/review lost: %+v", chapter)
	}
	second, _, replay, err := s.SaveChapter(ctx, actor, "p", "c", "chapter-save", input)
	if err != nil || !replay || string(raw) != string(second) || r.writes != 1 {
		t.Fatalf("lost-response retry duplicated: %v %v", replay, err)
	}
	if _, _, _, err := s.SaveChapter(ctx, actor, "p", "c", "different-key", input); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version: %v", err)
	}
	changed := input
	changed.BodyMarkdown = "different body"
	if _, _, _, err := s.SaveChapter(ctx, actor, "p", "c", "chapter-save", changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("key conflict: %v", err)
	}
	r.active = false
	if _, _, _, err := s.SaveChapter(ctx, actor, "p", "c", "chapter-save", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked replay leaked: %v", err)
	}
	r.active = true
	r.project.Members = nil
	if _, err := s.GetProject(ctx, actor, "p"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nonmember read: %v", err)
	}
}
func TestServiceRollbackAndSourceBoundaryWithNonSQLStorage(t *testing.T) {
	r := fakeWorkspace()
	s := NewService(r)
	actor := Actor{TenantID: 1, UserID: "owner"}
	ctx := context.Background()
	input := SaveChapterInput{ExpectedSpecRevision: 1, BodyMarkdown: "draft", SourceIDs: []string{}}
	r.failCommit = true
	if _, _, _, err := s.SaveChapter(ctx, actor, "p", "c", "chapter-save", input); !errors.Is(err, ErrRequestInProgress) {
		t.Fatalf("commit failure: %v", err)
	}
	if r.writes != 0 || r.project.ProjectVersion != 1 || len(r.operations) != 0 {
		t.Fatal("failed transaction leaked mutations")
	}
	r.failCommit = false
	if _, _, replay, err := s.SaveChapter(ctx, actor, "p", "c", "chapter-save", input); err != nil || replay {
		t.Fatalf("retry: %v %v", replay, err)
	}
	r.chapter.SourceIDs = []string{"changed-source"}
	input.ExpectedChapterVersionID = r.chapter.CurrentVersionID
	input.SourceIDs = []string{"changed-source"}
	input.BodyMarkdown = "[[source:changed-source]]"
	if _, _, _, err := s.SaveChapter(ctx, actor, "p", "c", "source-check", input); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("changed existing source escaped policy: %v", err)
	}
	input.BodyMarkdown = "[[source:malformed/id]]"
	if _, _, _, err := s.SaveChapter(ctx, actor, "p", "c", "source-check", input); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("malformed marker: %v", err)
	}
}
