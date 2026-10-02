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

type confirmationAuthorizerFunc func(context.Context, string, string, string) error

func (f confirmationAuthorizerFunc) Authorize(ctx context.Context, actorID, projectID, capability string) error {
	return f(ctx, actorID, projectID, capability)
}

func allowConfirmation(context.Context, string, string, string) error { return nil }

type confirmationSourceStub struct {
	calls         int
	err           error
	assetVersions []AssetVersion
}

func (s *confirmationSourceStub) ValidateCurrent(context.Context, string, string, []string) ([]AssetVersion, error) {
	s.calls++
	return s.assetVersions, s.err
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
	for name, test := range map[string]struct {
		decisions []ReviewDecision
		wantError error
	}{
		"duplicate":    {decisions: []ReviewDecision{valid[0], valid[0]}, wantError: ErrInvalidRequest},
		"missing":      {decisions: []ReviewDecision{valid[0]}, wantError: ErrInvalidState},
		"empty":        {decisions: nil, wantError: ErrInvalidState},
		"unknown":      {decisions: []ReviewDecision{{ReviewItemID: "other", Disposition: "resolved", Reason: "ok"}, valid[1]}, wantError: ErrInvalidRequest},
		"empty reason": {decisions: []ReviewDecision{{ReviewItemID: "r1", Disposition: "resolved"}, valid[1]}, wantError: ErrInvalidRequest},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateReviewDecisions(items, test.decisions); !errors.Is(err, test.wantError) {
				t.Fatalf("got %v, want %v", err, test.wantError)
			}
		})
	}
	if err := validateReviewDecisions(nil, nil); err != nil {
		t.Fatalf("empty chapter decisions rejected: %v", err)
	}
}

func TestConfirmationServiceFailsClosedWithoutCurrentSourcePolicy(t *testing.T) {
	version := "version-1"
	workspace := GenerationContext{ProjectID: "p1", ChapterID: "c1", SpecRevision: 3, ChapterVersionID: &version,
		Basis:   Basis{SpecRevision: 3, ChapterVersionID: &version, TemplateID: "demo", TemplateVersion: "1"},
		Chapter: Chapter{ID: "c1", ProjectID: "p1", CurrentVersionID: &version, SourceIDs: []string{"s1"}}}
	writer := &confirmationWriterStub{}
	service := ConfirmationService{Workspace: confirmationWorkspaceStub{value: workspace}, Authorizer: confirmationAuthorizerFunc(allowConfirmation), Writer: writer}
	_, _, err := service.ConfirmChapter(context.Background(), ConfirmChapterInput{ProjectID: "p1", ChapterID: "c1", ActorID: "u1",
		IdempotencyKey: "request-123", ExpectedChapterVersionID: version, ExpectedSpecRevision: 3})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("got %v, want invalid state", err)
	}
	if writer.calls != 0 {
		t.Fatal("writer called while source-currentness policy was missing")
	}
}

func TestConfirmationServiceAuthorizesWriteCapability(t *testing.T) {
	version := "version-1"
	workspace := GenerationContext{ProjectID: "p1", ChapterID: "c1", SpecRevision: 3, ChapterVersionID: &version,
		Basis:   Basis{SpecRevision: 3, ChapterVersionID: &version},
		Chapter: Chapter{ID: "c1", ProjectID: "p1", CurrentVersionID: &version}}
	var capability string
	writer := &confirmationWriterStub{}
	service := ConfirmationService{Workspace: confirmationWorkspaceStub{value: workspace},
		Authorizer: confirmationAuthorizerFunc(func(_ context.Context, _, _, got string) error {
			capability = got
			return nil
		}), Writer: writer}
	_, _, err := service.ConfirmChapter(context.Background(), ConfirmChapterInput{ProjectID: "p1", ChapterID: "c1", ActorID: "u1",
		IdempotencyKey: "request-123", ExpectedChapterVersionID: version, ExpectedSpecRevision: 3})
	if err != nil {
		t.Fatal(err)
	}
	if capability != "write" {
		t.Fatalf("authorization capability = %q, want write", capability)
	}
}

func TestConfirmationServiceRejectsStaleVersionBeforeWrite(t *testing.T) {
	version := "version-2"
	workspace := GenerationContext{ProjectID: "p1", ChapterID: "c1", SpecRevision: 3, ChapterVersionID: &version,
		Chapter: Chapter{ID: "c1", ProjectID: "p1", CurrentVersionID: &version}}
	writer := &confirmationWriterStub{}
	service := ConfirmationService{Workspace: confirmationWorkspaceStub{value: workspace}, Authorizer: confirmationAuthorizerFunc(allowConfirmation), Writer: writer}
	_, _, err := service.ConfirmChapter(context.Background(), ConfirmChapterInput{ProjectID: "p1", ChapterID: "c1", ActorID: "u1",
		IdempotencyKey: "request-123", ExpectedChapterVersionID: "version-1", ExpectedSpecRevision: 3})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("got %v, want version conflict", err)
	}
	if writer.calls != 0 {
		t.Fatal("writer called for stale version")
	}
}

func TestConfirmationServiceCapturesCurrentSourceAssetVersions(t *testing.T) {
	version := "version-1"
	workspace := GenerationContext{ProjectID: "p1", ChapterID: "c1", SpecRevision: 3, ChapterVersionID: &version,
		Basis:   Basis{SpecRevision: 3, ChapterVersionID: &version, TemplateVersion: "demo-v2"},
		Chapter: Chapter{ID: "c1", ProjectID: "p1", CurrentVersionID: &version, SourceIDs: []string{"s1"}}}
	sourcePolicy := &confirmationSourceStub{assetVersions: []AssetVersion{{AssetID: "a1", AssetRevision: 7}}}
	writer := &confirmationWriterStub{}
	service := ConfirmationService{Workspace: confirmationWorkspaceStub{value: workspace}, Sources: sourcePolicy,
		Authorizer: confirmationAuthorizerFunc(allowConfirmation), Writer: writer}
	got, replayed, err := service.ConfirmChapter(context.Background(), ConfirmChapterInput{ProjectID: "p1", ChapterID: "c1", ActorID: "u1",
		IdempotencyKey: "request-123", ExpectedChapterVersionID: version, ExpectedSpecRevision: 3})
	if err != nil {
		t.Fatal(err)
	}
	if replayed || sourcePolicy.calls != 1 || writer.calls != 1 {
		t.Fatalf("unexpected calls/replay: %d/%d replay=%v", sourcePolicy.calls, writer.calls, replayed)
	}
	if len(got.AssetVersions) != 1 || got.AssetVersions[0] != (AssetVersion{AssetID: "a1", AssetRevision: 7}) {
		t.Fatalf("confirmation omitted current source asset revision: %#v", got.AssetVersions)
	}
}

func TestConfirmationServiceOmitsProjectAssetsWhenChapterHasNoSources(t *testing.T) {
	version := "version-1"
	workspace := GenerationContext{ProjectID: "p1", ChapterID: "c1", SpecRevision: 3, ChapterVersionID: &version,
		Basis:   Basis{SpecRevision: 3, ChapterVersionID: &version, TemplateVersion: "demo-v2", AssetVersions: []AssetVersion{{AssetID: "project-asset", AssetRevision: 4}}},
		Chapter: Chapter{ID: "c1", ProjectID: "p1", CurrentVersionID: &version}}
	writer := &confirmationWriterStub{}
	service := ConfirmationService{Workspace: confirmationWorkspaceStub{value: workspace},
		Authorizer: confirmationAuthorizerFunc(allowConfirmation), Writer: writer}
	got, replayed, err := service.ConfirmChapter(context.Background(), ConfirmChapterInput{ProjectID: "p1", ChapterID: "c1", ActorID: "u1",
		IdempotencyKey: "request-123", ExpectedChapterVersionID: version, ExpectedSpecRevision: 3})
	if err != nil {
		t.Fatal(err)
	}
	if replayed || writer.calls != 1 {
		t.Fatalf("unexpected calls/replay: writer=%d replay=%v", writer.calls, replayed)
	}
	if len(got.AssetVersions) != 0 {
		t.Fatalf("confirmation captured assets for a chapter with no sources: %#v", got.AssetVersions)
	}
}

func TestConfirmationServiceReplaysBeforeReadingMutableWorkspace(t *testing.T) {
	existing := &Confirmation{ID: "saved-confirmation", Valid: true}
	replay := &confirmationReplayStub{value: existing}
	service := ConfirmationService{Idempotency: replay, Authorizer: confirmationAuthorizerFunc(allowConfirmation)}
	got, wasReplay, err := service.ConfirmChapter(context.Background(), ConfirmChapterInput{ProjectID: "p1", ChapterID: "c1", ActorID: "u1",
		IdempotencyKey: "request-123", ExpectedChapterVersionID: "version-1", ExpectedSpecRevision: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !wasReplay || got.ID != existing.ID || replay.calls != 1 {
		t.Fatalf("unexpected replay: %#v, replay=%v, calls=%d", got, wasReplay, replay.calls)
	}
}

func TestConfirmationServiceFailsClosedBeforeIdempotencyReplayWithoutAuthorizer(t *testing.T) {
	replay := &confirmationReplayStub{value: &Confirmation{ID: "must-not-leak", Valid: true}}
	service := ConfirmationService{Idempotency: replay}
	_, _, err := service.ConfirmChapter(context.Background(), ConfirmChapterInput{ProjectID: "p1", ChapterID: "c1", ActorID: "u1",
		IdempotencyKey: "request-123", ExpectedChapterVersionID: "version-1", ExpectedSpecRevision: 3})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("got %v, want invalid state", err)
	}
	if replay.calls != 0 {
		t.Fatal("confirmation replay queried without project authorization")
	}
}
