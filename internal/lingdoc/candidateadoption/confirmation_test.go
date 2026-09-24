package candidateadoption

import (
	"context"
	"errors"
	"testing"
)

type confirmationWorkspaceStub struct {
	value GenerationContext
	err   error
}

func (s confirmationWorkspaceStub) GenerationContext(context.Context, string, string) (GenerationContext, error) {
	return s.value, s.err
}

type confirmationSourceStub struct {
	calls int
	err   error
}

func (s *confirmationSourceStub) ValidateCurrent(context.Context, string, string, []string) error {
	s.calls++
	return s.err
}

type confirmationWriterStub struct{ calls int }

func (s *confirmationWriterStub) ConfirmChapter(_ context.Context, in ConfirmChapterInput, workspace GenerationContext) (Confirmation, bool, error) {
	s.calls++
	return newConfirmation(in, workspace), false, nil
}

type confirmationReplayStub struct {
	value *Confirmation
	calls int
}

func (s *confirmationReplayStub) ReplayConfirmation(context.Context, ConfirmChapterInput) (*Confirmation, error) {
	s.calls++
	return s.value, nil
}

func TestValidateReviewDecisionsRequiresEveryUniqueItem(t *testing.T) {
	items := []ReviewItem{{ID: "r1"}, {ID: "r2"}}
	valid := []ReviewDecision{
		{ReviewItemID: "r1", Disposition: "resolved", Reason: "已核对原文"},
		{ReviewItemID: "r2", Disposition: "retained_warning", Reason: "保留提示"},
	}
	if err := validateReviewDecisions(items, valid); err != nil {
		t.Fatalf("valid decisions rejected: %v", err)
	}
	for name, decisions := range map[string][]ReviewDecision{
		"duplicate":    {valid[0], valid[0]},
		"missing":      {valid[0]},
		"unknown":      {{ReviewItemID: "other", Disposition: "resolved", Reason: "ok"}, valid[1]},
		"empty reason": {{ReviewItemID: "r1", Disposition: "resolved"}, valid[1]},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateReviewDecisions(items, decisions); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("got %v, want invalid request", err)
			}
		})
	}
}

func TestConfirmationServiceFailsClosedWithoutCurrentSourcePolicy(t *testing.T) {
	version := "version-1"
	workspace := GenerationContext{ProjectID: "p1", ChapterID: "c1", SpecRevision: 3, ChapterVersionID: &version,
		Basis:   Basis{SpecRevision: 3, ChapterVersionID: &version, TemplateID: "demo", TemplateVersion: "1"},
		Chapter: Chapter{ID: "c1", ProjectID: "p1", CurrentVersionID: &version, SourceIDs: []string{"s1"}}}
	writer := &confirmationWriterStub{}
	service := ConfirmationService{Workspace: confirmationWorkspaceStub{value: workspace}, Writer: writer}
	_, _, err := service.ConfirmChapter(context.Background(), ConfirmChapterInput{ProjectID: "p1", ChapterID: "c1", ActorID: "u1",
		IdempotencyKey: "request-123", ExpectedChapterVersionID: version, ExpectedSpecRevision: 3})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("got %v, want invalid state", err)
	}
	if writer.calls != 0 {
		t.Fatal("writer called while source-currentness policy was missing")
	}
}

func TestConfirmationServiceRejectsStaleVersionBeforeWrite(t *testing.T) {
	version := "version-2"
	workspace := GenerationContext{ProjectID: "p1", ChapterID: "c1", SpecRevision: 3, ChapterVersionID: &version,
		Chapter: Chapter{ID: "c1", ProjectID: "p1", CurrentVersionID: &version}}
	writer := &confirmationWriterStub{}
	service := ConfirmationService{Workspace: confirmationWorkspaceStub{value: workspace}, Writer: writer}
	_, _, err := service.ConfirmChapter(context.Background(), ConfirmChapterInput{ProjectID: "p1", ChapterID: "c1", ActorID: "u1",
		IdempotencyKey: "request-123", ExpectedChapterVersionID: "version-1", ExpectedSpecRevision: 3})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("got %v, want version conflict", err)
	}
	if writer.calls != 0 {
		t.Fatal("writer called for stale version")
	}
}

func TestConfirmationServiceReplaysBeforeReadingMutableWorkspace(t *testing.T) {
	existing := &Confirmation{ID: "saved-confirmation", Valid: true}
	replay := &confirmationReplayStub{value: existing}
	service := ConfirmationService{Idempotency: replay}
	got, wasReplay, err := service.ConfirmChapter(context.Background(), ConfirmChapterInput{ProjectID: "p1", ChapterID: "c1", ActorID: "u1",
		IdempotencyKey: "request-123", ExpectedChapterVersionID: "version-1", ExpectedSpecRevision: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !wasReplay || got.ID != existing.ID || replay.calls != 1 {
		t.Fatalf("unexpected replay: %#v, replay=%v, calls=%d", got, wasReplay, replay.calls)
	}
}
