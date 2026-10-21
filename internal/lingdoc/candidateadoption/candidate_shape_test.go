package candidateadoption

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// TestCandidateCarriesActionOriginRunModeAndModel pins the new candidate
// shape: a candidate records the writing action that produced it, who the
// text is from, how the run reached the model and which model it was.
func TestCandidateCarriesActionOriginRunModeAndModel(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	ctx := context.Background()
	require.NoError(t, store.UpsertProject(ctx, "project-1", 0))
	require.NoError(t, store.UpsertChapter(ctx, Chapter{
		ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题",
	}, 1))
	require.NoError(t, store.UpsertCandidate(ctx, Candidate{
		ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
		BodyMarkdown: "候选正文 [[source:source-1]]", SourceIDs: []string{"source-1"},
		Basis: Basis{SpecRevision: 1}, Validity: ValidityFresh,
		WritingAction: writingaction.ActionRewrite,
		Origin:        writingaction.OriginGenerated,
		RunMode:       RunModeRealAPIFakeModel,
		ModelID:       "model-rewrite-1",
	}))

	stored, err := store.GetCandidate(ctx, "project-1", "candidate-1")
	require.NoError(t, err)
	require.Equal(t, writingaction.ActionRewrite, stored.WritingAction)
	require.Equal(t, writingaction.OriginGenerated, stored.Origin)
	require.Equal(t, RunModeRealAPIFakeModel, stored.RunMode)
	require.Equal(t, "model-rewrite-1", stored.ModelID)
}

// TestCandidateWithoutTheNewFieldsKeepsThemEmpty pins that this ticket adds
// the shape without filling it: a candidate that says nothing about its
// action, origin, run mode or model still reads back with nothing in those
// fields, so the running production path behaves exactly as before.
func TestCandidateWithoutTheNewFieldsKeepsThemEmpty(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	ctx := context.Background()
	require.NoError(t, store.UpsertProject(ctx, "project-1", 0))
	require.NoError(t, store.UpsertChapter(ctx, Chapter{
		ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题",
	}, 1))
	require.NoError(t, store.UpsertCandidate(ctx, Candidate{
		ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
		BodyMarkdown: "候选正文 [[source:source-1]]", SourceIDs: []string{"source-1"},
		Basis: Basis{SpecRevision: 1}, Validity: ValidityFresh,
	}))

	stored, err := store.GetCandidate(ctx, "project-1", "candidate-1")
	require.NoError(t, err)
	require.Empty(t, stored.WritingAction)
	require.Empty(t, stored.Origin)
	require.Empty(t, stored.RunMode)
	require.Empty(t, stored.ModelID)
}

// TestValidityTellsStaleFromNotAuthorized pins the second non-adoptable
// reason.
//
// "The inputs changed" and "the authorization is gone" both make a candidate
// unusable, but they are different situations: the first means run the
// action again, the second means restore the authorization and run it again.
// A caller has to be able to tell them apart, so the reason has to survive
// the round trip through storage.
func TestValidityTellsStaleFromNotAuthorized(t *testing.T) {
	require.Equal(t, "fresh", string(ValidityFresh))
	require.Equal(t, "stale", string(ValidityStale))
	require.Equal(t, "not_authorized", string(ValidityNotAuthorized))
	require.NotEqual(t, ValidityStale, ValidityNotAuthorized)

	store := newCandidateAdoptionStore(t)
	ctx := context.Background()
	require.NoError(t, store.UpsertProject(ctx, "project-1", 0))
	require.NoError(t, store.UpsertChapter(ctx, Chapter{
		ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题",
	}, 1))
	for _, validity := range []Validity{ValidityStale, ValidityNotAuthorized} {
		id := "candidate-" + string(validity)
		require.NoError(t, store.UpsertCandidate(ctx, Candidate{
			ID: id, ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-" + string(validity),
			BodyMarkdown: "候选正文 [[source:source-1]]", SourceIDs: []string{"source-1"},
			Basis: Basis{SpecRevision: 1}, Validity: validity,
		}))
		stored, err := store.GetCandidate(ctx, "project-1", id)
		require.NoError(t, err)
		require.Equal(t, validity, stored.Validity)
	}
}
