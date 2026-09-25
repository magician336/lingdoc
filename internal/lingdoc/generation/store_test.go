package generation

import (
	"context"
	"testing"
	"time"

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

	claimedRun, _, claimed, err := repo.Claim(ctx, first.ID)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NotEmpty(t, claimedRun.ClaimToken)
	oldClaimToken := claimedRun.ClaimToken
	require.NoError(t, repo.Touch(ctx, first.ID, oldClaimToken))
	_, _, claimed, err = repo.Claim(ctx, first.ID)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, db.Model(&generationRow{}).Where("id = ?", first.ID).
		Update("updated_at", time.Now().UTC().Add(-claimLeaseDuration-time.Second)).Error)
	claimedRun, _, claimed, err = repo.Claim(ctx, first.ID)
	require.NoError(t, err)
	require.True(t, claimed, "an expired worker lease should be reclaimable")
	require.NotEqual(t, oldClaimToken, claimedRun.ClaimToken)
	require.ErrorIs(t, repo.Touch(ctx, first.ID, oldClaimToken), ErrRunUnavailable)

	candidate := candidateadoption.Candidate{ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: first.ID,
		BodyMarkdown: "body [[source:source-1]]", SourceIDs: []string{"source-1"}, Validity: "fresh",
		Basis:       candidateadoption.Basis{SpecRevision: 4},
		ReviewItems: []candidateadoption.ReviewItem{{ID: "review-1", Statement: "核实数字", OriginCandidateID: "candidate-1"}}}
	_, err = repo.Complete(ctx, first.ID, oldClaimToken, candidate)
	require.ErrorIs(t, err, ErrRunUnavailable, "an expired worker cannot persist its candidate")
	completed, err := repo.Complete(ctx, first.ID, claimedRun.ClaimToken, candidate)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, completed.Status)
	stored, err := candidateStore.GetCandidate(ctx, "project-1", "candidate-1")
	require.NoError(t, err)
	require.Equal(t, candidate.SourceIDs, stored.SourceIDs)
	require.Equal(t, candidate.ReviewItems, stored.ReviewItems)
	listed, err := candidateStore.ListCandidates(ctx, "project-1", "chapter-1")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, candidate.ID, listed[0].ID)
}

func TestGenerationServicePersistsReadableCandidate(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := NewSQLiteRepository(db)
	candidateReader := candidateadoption.NewSQLiteCandidateAdoptionStore(db)
	require.NoError(t, candidateReader.AutoMigrate(ctx))
	require.NoError(t, repo.AutoMigrate(ctx))

	actor := Actor{TenantID: 7, UserID: "member-1"}
	chapterVersion := "chapter-v1"
	basis := candidateadoption.Basis{SpecRevision: 3, ChapterVersionID: &chapterVersion,
		AssetVersions: []candidateadoption.AssetVersion{{AssetID: "asset-1", AssetRevision: 2}}}
	input := Input{Actor: actor, Basis: basis,
		Workspace: candidateadoption.GenerationContext{ProjectID: "project-1", ChapterID: "chapter-1", SpecRevision: 3,
			ChapterVersionID: &chapterVersion, Basis: basis},
		Sources: []Source{{ID: "source-1", ProjectID: "project-1", AssetID: "asset-1", AssetRevision: 2, Status: "available"}}}
	draft := Draft{BodyMarkdown: "Verified text [[source:source-1]]", Sources: []Source{{ID: "source-1"}},
		ReviewItems: []candidateadoption.ReviewItem{{ID: "review-1", Statement: "复核统计口径"}}}
	service := NewService(testAuthorizer{}, testInputs{input: input}, testSources{}, testCurrentness{current: true},
		testModel{draft: draft}, repo)
	service.Enqueuer = &testEnqueuer{}
	request := Request{ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "Draft this chapter",
		ExpectedSpecRevision: 3, ExpectedChapterVersionID: &chapterVersion}
	run, err := service.Start(ctx, actor, "project-1", "generation-key-1", request)
	require.NoError(t, err)
	require.Equal(t, StatusQueued, run.Status)

	completed, err := service.Execute(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, completed.Status)
	require.NotNil(t, completed.CandidateID)
	candidate, storedInput, err := repo.GetCandidate(ctx, "project-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, *completed.CandidateID, candidate.ID)
	require.Len(t, storedInput.Sources, 1)
	require.Equal(t, "source-1", storedInput.Sources[0].ID)
	stored, err := candidateReader.GetCandidate(ctx, "project-1", *completed.CandidateID)
	require.NoError(t, err)
	require.Equal(t, run.ID, stored.RunID)
	require.Equal(t, draft.BodyMarkdown, stored.BodyMarkdown)
	require.Equal(t, []string{"source-1"}, stored.SourceIDs)
	require.Len(t, stored.ReviewItems, 1)
	require.Equal(t, *completed.CandidateID, stored.ReviewItems[0].OriginCandidateID)
	listed, err := repo.ListCandidates(ctx, "project-1", "chapter-1")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, stored.ID, listed[0].ID)
}

func TestSQLiteRepositoryCancelsQueuedAndRunningRuns(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := NewSQLiteRepository(db)
	require.NoError(t, repo.AutoMigrate(ctx))
	actor := Actor{TenantID: 7, UserID: "member-1"}
	input := Input{Actor: actor, Request: Request{ChapterID: "chapter-1"}}
	queued, err := repo.CreateOrReplay(ctx, actor, "project-1", "queued-key", "hash-1", input)
	require.NoError(t, err)
	cancelled, err := repo.Cancel(ctx, actor, "project-1", queued.ID)
	require.NoError(t, err)
	require.Equal(t, StatusInterrupted, cancelled.Status)
	require.Equal(t, "generation_cancelled", cancelled.Error.Code)
	_, _, claimed, err := repo.Claim(ctx, queued.ID)
	require.NoError(t, err)
	require.False(t, claimed)

	running, err := repo.CreateOrReplay(ctx, actor, "project-1", "running-key", "hash-2", input)
	require.NoError(t, err)
	claimedRun, _, claimed, err := repo.Claim(ctx, running.ID)
	require.NoError(t, err)
	require.True(t, claimed)
	cancelled, err = repo.Cancel(ctx, actor, "project-1", running.ID)
	require.NoError(t, err)
	require.Equal(t, StatusRunning, cancelled.Status, "an active worker finishes cancellation through its lease")
	require.ErrorIs(t, repo.Touch(ctx, running.ID, claimedRun.ClaimToken), ErrGenerationCancelled)
	cancelled, err = repo.Fail(ctx, running.ID, claimedRun.ClaimToken, StatusInterrupted,
		RunError{Code: "generation_interrupted", Message: "worker stopped", Retryable: true})
	require.NoError(t, err)
	require.Equal(t, StatusInterrupted, cancelled.Status)
	require.Equal(t, "generation_cancelled", cancelled.Error.Code)
	var stored generationRow
	require.NoError(t, db.Where("id = ?", running.ID).First(&stored).Error)
	require.Empty(t, stored.ClaimToken)

	abandoned, err := repo.CreateOrReplay(ctx, actor, "project-1", "abandoned-key", "hash-3", input)
	require.NoError(t, err)
	abandonedRun, _, claimed, err := repo.Claim(ctx, abandoned.ID)
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = repo.Cancel(ctx, actor, "project-1", abandoned.ID)
	require.NoError(t, err)
	require.NoError(t, db.Model(&generationRow{}).Where("id = ?", abandoned.ID).
		Update("updated_at", time.Now().UTC().Add(-claimLeaseDuration-time.Second)).Error)
	terminal, _, claimed, err := repo.Claim(ctx, abandoned.ID)
	require.NoError(t, err)
	require.False(t, claimed, "an expired cancelled run must not be reclaimed")
	require.Equal(t, StatusInterrupted, terminal.Status)
	require.Equal(t, "generation_cancelled", terminal.Error.Code)
	require.ErrorIs(t, repo.Touch(ctx, abandoned.ID, abandonedRun.ClaimToken), ErrRunUnavailable)
}
