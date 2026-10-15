package candidateadoption

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdoptionAdvancesCleanWorkingCopyAndReplaysWithoutRevisionBump(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
	candidate := Candidate{ID: "candidate", ProjectID: "project", ChapterID: "chapter", BodyMarkdown: "new [[source:source]]", SourceIDs: []string{"source"}, ReviewItems: []ReviewItem{{ID: "review", Statement: "check"}}}
	seedCandidateAdoptionWorkspace(t, store, Chapter{ID: "chapter", ProjectID: "project"}, candidate, 2)
	require.NoError(t, store.DB().Create(&adoptionWorkingCopyRow{ProjectID: "project", ChapterID: "chapter", SpecRevision: 2, WorkingCopyRevision: 4, SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]"}).Error)
	input := AcceptCandidateInput{ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor", IdempotencyKey: "adoption-working-copy", ExpectedSpecRevision: 2}
	result, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.NoError(t, err)
	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, result.Chapter.CurrentVersionID, copy.BaseChapterVersionID)
	require.Equal(t, int64(5), copy.WorkingCopyRevision)
	require.Equal(t, int64(2), copy.SpecRevision)
	require.Equal(t, candidate.BodyMarkdown, copy.BodyMarkdown)
	require.True(t, equalAdoptionJSON(copy.SourceIDsJSON, candidate.SourceIDs))
	require.True(t, equalAdoptionJSON(copy.ReviewItemsJSON, candidate.ReviewItems))
	require.JSONEq(t, `[{"source_id":"source"}]`, copy.CitationUsagesJSON)
	replay, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, int64(5), copy.WorkingCopyRevision)
}

func TestAdoptionRejectsDirtyOrStaleWorkingCopyAtomically(t *testing.T) {
	for _, field := range []string{"body_markdown", "source_ids_json", "review_items_json", "citation_usages_json", "base_chapter_version_id"} {
		t.Run(field, func(t *testing.T) {
			store := newCandidateAdoptionStore(t)
			require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
			old := "old"
			candidate := Candidate{ID: "candidate", ProjectID: "project", ChapterID: "chapter", BodyMarkdown: "new"}
			seedCandidateAdoptionWorkspace(t, store, Chapter{ID: "chapter", ProjectID: "project", CurrentVersionID: &old}, candidate, 2)
			require.NoError(t, store.DB().Create(&chapterVersionRow{ID: old, ProjectID: "project", ChapterID: "chapter", BodyMarkdown: "old body", SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]"}).Error)
			require.NoError(t, store.DB().Create(&adoptionWorkingCopyRow{ProjectID: "project", ChapterID: "chapter", BaseChapterVersionID: &old, SpecRevision: 2, WorkingCopyRevision: 4, BodyMarkdown: "old body", SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]"}).Error)
			value := `[{"id":"unsaved"}]`
			if field == "body_markdown" || field == "base_chapter_version_id" {
				value = "unsaved"
			}
			require.NoError(t, store.DB().Model(&adoptionWorkingCopyRow{}).Where("chapter_id = ?", "chapter").Update(field, value).Error)
			_, err := store.AcceptCandidate(context.Background(), AcceptCandidateInput{ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor", IdempotencyKey: "dirty", ExpectedSpecRevision: 2, ExpectedChapterVersionID: &old, ReplaceExisting: true}, candidate)
			require.ErrorIs(t, err, ErrVersionConflict)
			var count int64
			require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&count).Error)
			require.Equal(t, int64(1), count)
			require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&count).Error)
			require.Zero(t, count)
			var chapter chapterRow
			require.NoError(t, store.DB().First(&chapter).Error)
			require.Equal(t, &old, chapter.CurrentVersionID)
			var copy adoptionWorkingCopyRow
			require.NoError(t, store.DB().First(&copy).Error)
			require.Equal(t, int64(4), copy.WorkingCopyRevision)
		})
	}
}

func TestProductionAdoptionFailsWhenWorkingCopySchemaOrRowMissing(t *testing.T) {
	for _, tableExists := range []bool{false, true} {
		store := newCandidateAdoptionStore(t)
		if tableExists {
			require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
		}
		candidate := Candidate{ID: "candidate", ProjectID: "project", ChapterID: "chapter", BodyMarkdown: "new"}
		seedCandidateAdoptionWorkspace(t, store, Chapter{ID: "chapter", ProjectID: "project"}, candidate, 1)
		productionStore := NewSQLiteCandidateAdoptionStore(store.DB())
		_, err := productionStore.AcceptCandidate(context.Background(), AcceptCandidateInput{ProjectID: "project", ChapterID: "chapter", ExpectedSpecRevision: 1}, candidate)
		require.Error(t, err)
		var count int64
		require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestAdoptionInitializesUnknownUsagesAndPreservesPriorVersion(t *testing.T) {
	for _, withSources := range []bool{true, false} {
		t.Run(map[bool]string{true: "cited", false: "uncited"}[withSources], func(t *testing.T) {
			store := newCandidateAdoptionStore(t)
			require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
			oldID := "old"
			oldBody := "old [[source:retained]] [[source:removed]]"
			oldSources := `["removed","retained"]`
			oldUsages := `[{"source_id":"removed","purpose":"old purpose"},{"source_id":"retained","purpose":"old interpretation","limitation":"old limitation"}]`
			candidate := Candidate{ID: "candidate", ProjectID: "project", ChapterID: "chapter", BodyMarkdown: "new", SourceIDs: []string{}, ReviewItems: []ReviewItem{}}
			expected := []CitationUsage{}
			if withSources {
				candidate.BodyMarkdown = "new [[source:retained]] [[source:new]] [[source:retained]]"
				candidate.SourceIDs = []string{"retained", "new", "retained"}
				expected = []CitationUsage{{SourceID: "new"}, {SourceID: "retained"}}
			}
			seedCandidateAdoptionWorkspace(t, store, Chapter{ID: "chapter", ProjectID: "project", CurrentVersionID: &oldID}, candidate, 2)
			require.NoError(t, store.DB().Create(&chapterVersionRow{ID: oldID, ProjectID: "project", ChapterID: "chapter", BodyMarkdown: oldBody, SourceIDsJSON: oldSources, CitationUsagesJSON: oldUsages, ReviewItemsJSON: "[]"}).Error)
			require.NoError(t, store.DB().Create(&adoptionWorkingCopyRow{ProjectID: "project", ChapterID: "chapter", BaseChapterVersionID: &oldID, SpecRevision: 2, WorkingCopyRevision: 4, BodyMarkdown: oldBody, SourceIDsJSON: oldSources, CitationUsagesJSON: oldUsages, ReviewItemsJSON: "[]"}).Error)
			input := AcceptCandidateInput{ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor", IdempotencyKey: "citation-usages", ExpectedSpecRevision: 2, ExpectedChapterVersionID: &oldID, ReplaceExisting: true}
			result, err := store.AcceptCandidate(context.Background(), input, candidate)
			require.NoError(t, err)
			require.Equal(t, expected, result.Chapter.CitationUsages)
			require.Equal(t, candidate.BodyMarkdown, result.Chapter.BodyMarkdown)
			persisted, err := store.GenerationContext(context.Background(), "project", "chapter")
			require.NoError(t, err)
			require.Equal(t, expected, persisted.Chapter.CitationUsages)
			var copy adoptionWorkingCopyRow
			require.NoError(t, store.DB().First(&copy).Error)
			require.True(t, equalAdoptionJSON(copy.CitationUsagesJSON, expected))
			require.Equal(t, int64(5), copy.WorkingCopyRevision)
			var prior chapterVersionRow
			require.NoError(t, store.DB().Where("id = ?", oldID).First(&prior).Error)
			require.JSONEq(t, oldUsages, prior.CitationUsagesJSON)
			replay, err := store.AcceptCandidate(context.Background(), input, candidate)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, result.Chapter, replay.Chapter)
			require.NoError(t, store.DB().First(&copy).Error)
			require.Equal(t, int64(5), copy.WorkingCopyRevision)
		})
	}
}
