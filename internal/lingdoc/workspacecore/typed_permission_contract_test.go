package workspacecore

import (
	"context"
	"errors"
	"testing"
)

// TestTypedMutationContractMatrix is a characterization test at the
// highest workspace application seam. It deliberately uses the typed result
// method rather than the legacy raw JSON/status method so future port changes
// cannot move transport concerns back into callers.
func TestTypedMutationContractMatrix(t *testing.T) {
	repository := fakeWorkspace()
	service := NewService(repository)
	actor := Actor{TenantID: 1, UserID: "owner"}
	input := SaveChapterInput{
		ExpectedSpecRevision: 1,
		BodyMarkdown:         "G5-C1 baseline",
		SourceIDs:            []string{},
	}

	first, err := service.SaveChapterResult(context.Background(), actor, "p", "c", "g5-c1-save", input)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	if first.Replayed || first.Chapter.BodyMarkdown != input.BodyMarkdown || repository.writes != 1 {
		t.Fatalf("first save = %+v, writes=%d; want a new result and one write", first, repository.writes)
	}

	replayed, err := service.SaveChapterResult(context.Background(), actor, "p", "c", "g5-c1-save", input)
	if err != nil {
		t.Fatalf("same-key replay: %v", err)
	}
	if !replayed.Replayed || replayed.Chapter.ID != first.Chapter.ID || replayed.Chapter.BodyMarkdown != first.Chapter.BodyMarkdown || repository.writes != 1 {
		t.Fatalf("same-key replay = %+v, writes=%d; want original value, replay=true and no second write", replayed, repository.writes)
	}

	if _, err := service.SaveChapterResult(context.Background(), actor, "p", "c", "g5-c1-stale", input); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version = %v, want ErrVersionConflict", err)
	}

	changed := input
	changed.BodyMarkdown = "different request body"
	if _, err := service.SaveChapterResult(context.Background(), actor, "p", "c", "g5-c1-save", changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same-key changed body = %v, want ErrIdempotencyConflict", err)
	}
	if repository.writes != 1 {
		t.Fatalf("conflicting replay changed write count to %d, want 1", repository.writes)
	}

	// Authorization is checked before replay. Losing membership must not leak
	// the previously persisted chapter through a retry.
	repository.active = false
	if _, err := service.SaveChapterResult(context.Background(), actor, "p", "c", "g5-c1-save", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked replay = %v, want ErrNotFound", err)
	}
	if repository.writes != 1 {
		t.Fatalf("revoked replay changed write count to %d, want 1", repository.writes)
	}
}
