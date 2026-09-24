package generation

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteRepositoryReplaysAndPersistsCandidateAtomically(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := NewSQLiteRepository(db)
	candidateStore := candidateadoption.NewSQLiteCandidateAdoptionStore(db)
	require.NoError(t, candidateStore.AutoMigrate(ctx))
	require.NoError(t, repo.AutoMigrate(ctx))

	actor := Actor{TenantID: 7, UserID: "member-1"}
	input := Input{Actor: actor, Request: Request{ChapterID: "chapter-1"},
		Workspace: candidateadoption.GenerationContext{ProjectID: "project-1", ChapterID: "chapter-1"}}
	first, err := repo.CreateOrReplay(ctx, actor, "project-1", "request-key-1", "hash-1", input)
	require.NoError(t, err)
	require.Equal(t, StatusQueued, first.Status)
	second, err := repo.CreateOrReplay(ctx, actor, "project-1", "request-key-1", "hash-1", input)
	require.NoError(t, err)
	require.True(t, second.Replayed)
	require.Equal(t, first.ID, second.ID)
	_, err = repo.CreateOrReplay(ctx, actor, "project-1", "request-key-1", "different-hash", input)
	require.ErrorIs(t, err, ErrIdempotencyConflict)

	_, _, claimed, err := repo.Claim(ctx, first.ID)
	require.NoError(t, err)
	require.True(t, claimed)
	_, _, claimed, err = repo.Claim(ctx, first.ID)
	require.NoError(t, err)
	require.False(t, claimed)

	candidate := candidateadoption.Candidate{ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: first.ID,
		BodyMarkdown: "body [[source:source-1]]", SourceIDs: []string{"source-1"}, Validity: "fresh",
		Basis:       candidateadoption.Basis{SpecRevision: 4},
		ReviewItems: []candidateadoption.ReviewItem{{ID: "review-1", Statement: "核实数字", OriginCandidateID: "candidate-1"}}}
	completed, err := repo.Complete(ctx, first.ID, candidate)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, completed.Status)
	stored, err := candidateStore.GetCandidate(ctx, "project-1", "candidate-1")
	require.NoError(t, err)
	require.Equal(t, candidate.SourceIDs, stored.SourceIDs)
	require.Equal(t, candidate.ReviewItems, stored.ReviewItems)
}
