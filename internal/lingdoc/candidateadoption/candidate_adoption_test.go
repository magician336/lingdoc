package candidateadoption

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newCandidateAdoptionStore(t *testing.T) *SQLiteCandidateAdoptionStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	store := NewSQLiteCandidateAdoptionStore(db)
	require.NoError(t, store.AutoMigrate(context.Background()))
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return store
}

func seedCandidateAdoptionWorkspace(t *testing.T, store *SQLiteCandidateAdoptionStore, chapter Chapter, candidate Candidate, specRevision int) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, store.UpsertProject(ctx, chapter.ProjectID, 0))
	require.NoError(t, store.UpsertChapter(ctx, chapter, specRevision))
	require.NoError(t, store.UpsertCandidate(ctx, candidate))
}

func TestAcceptCandidateCreatesVersionPreservesReviewItemsAndReplays(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	item := ReviewItem{ID: "review-1", Statement: "需要人工核查", OriginCandidateID: "candidate-1"}
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题"},
		Candidate{
			ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
			BodyMarkdown: "候选正文 [[source:source-1]]", SourceIDs: []string{"source-1"},
			Basis: Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1"}, Validity: "fresh", ReviewItems: []ReviewItem{item},
		}, 1)

	service := NewCandidateAdoptionService(store, nil, nil)
	input := AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: "candidate-1", ActorID: "user-1",
		IdempotencyKey: "candidate-adoption-key-0001", ExpectedSpecRevision: 1,
	}
	result, err := service.AcceptCandidate(context.Background(), input)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	require.NotNil(t, result.Chapter.CurrentVersionID)
	require.Equal(t, item, result.Chapter.ReviewItems[0])
	require.False(t, result.Chapter.ConfirmationValid)

	replayed, err := service.AcceptCandidate(context.Background(), input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, result.Chapter.CurrentVersionID, replayed.Chapter.CurrentVersionID)

	var versionCount int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Where("chapter_id = ?", "chapter-1").Count(&versionCount).Error)
	require.Equal(t, int64(1), versionCount)
}

func TestAcceptCandidateRequiresExplicitReplacementAndKeepsOldVersion(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	first := "old-version"
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题", CurrentVersionID: &first, BodyMarkdown: "旧正文 [[source:source-1]]", SourceIDs: []string{"source-1"}},
		Candidate{ID: "candidate-2", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-2", BodyMarkdown: "新正文 [[source:source-2]]", SourceIDs: []string{"source-2"}, Basis: Basis{SpecRevision: 1, ChapterVersionID: &first, TemplateID: "template-demo", TemplateVersion: "1"}, Validity: "fresh"}, 1)
	require.NoError(t, store.DB().Create(&chapterVersionRow{ID: first, ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: "candidate-2", BodyMarkdown: "旧正文 [[source:source-1]]", SourceIDsJSON: `["source-1"]`, ReviewItemsJSON: "[]", SpecRevision: 1, CreatedBy: "user-old"}).Error)

	service := NewCandidateAdoptionService(store, nil, nil)
	input := AcceptCandidateInput{ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: "candidate-2", ActorID: "user-1", IdempotencyKey: "candidate-adoption-key-0002", ExpectedChapterVersionID: &first, ExpectedSpecRevision: 1}
	_, err := service.AcceptCandidate(context.Background(), input)
	require.ErrorIs(t, err, ErrInvalidState)

	input.ReplaceExisting = true
	result, err := service.AcceptCandidate(context.Background(), input)
	require.NoError(t, err)
	require.NotEqual(t, first, *result.Chapter.CurrentVersionID)

	var versions []chapterVersionRow
	require.NoError(t, store.DB().Where("chapter_id = ?", "chapter-1").Find(&versions).Error)
	require.Len(t, versions, 2)
	var replacement *chapterVersionRow
	for i := range versions {
		if versions[i].ID != first {
			replacement = &versions[i]
		}
	}
	require.NotNil(t, replacement)
	require.Equal(t, first, *replacement.ParentVersionID)
	require.Equal(t, "candidate-2", replacement.CandidateID)
}

func TestAcceptCandidateRejectsStaleAndMismatchedRequests(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题"},
		Candidate{ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1", BodyMarkdown: "正文 [[source:source-1]]", SourceIDs: []string{"source-1"}, Basis: Basis{SpecRevision: 1}, Validity: "stale"}, 1)
	service := NewCandidateAdoptionService(store, nil, nil)
	base := AcceptCandidateInput{ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: "candidate-1", ActorID: "user-1", IdempotencyKey: "candidate-adoption-key-0003", ExpectedSpecRevision: 1}
	_, err := service.AcceptCandidate(context.Background(), base)
	require.ErrorIs(t, err, ErrStaleInput)

	require.NoError(t, store.DB().Model(&candidateRow{}).Where("id = ?", "candidate-1").Update("validity", "fresh").Error)
	base.IdempotencyKey = "candidate-adoption-key-0004"
	base.ExpectedSpecRevision = 2
	_, err = service.AcceptCandidate(context.Background(), base)
	require.ErrorIs(t, err, ErrVersionConflict)

	var adoptionCount int64
	require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&adoptionCount).Error)
	require.Equal(t, int64(0), adoptionCount)
}

func TestConfirmationIdempotencyKeyIsUniqueInAutoMigratedStore(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	first := confirmationIdempotencyRow{
		ID: "request-1", ProjectID: "project-1", ChapterID: "chapter-1", ActorID: "user-1",
		Key: "confirm-key-0001", RequestHash: "hash-1", ResponseJSON: `{}`,
	}
	require.NoError(t, store.DB().Create(&first).Error)
	duplicate := confirmationIdempotencyRow{
		ID: "request-2", ProjectID: first.ProjectID, ChapterID: first.ChapterID, ActorID: first.ActorID,
		Key: first.Key, RequestHash: "hash-2", ResponseJSON: `{}`,
	}
	require.Error(t, store.DB().Create(&duplicate).Error, "the focused SQLite schema must enforce the same composite idempotency key as production migrations")
}
