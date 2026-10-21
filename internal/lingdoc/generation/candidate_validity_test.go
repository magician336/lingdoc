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

// TestListCandidatesReportsNotAuthorizedWhenTheAuthorizationIsGone 覆盖 A38
// 在生成侧的另一半：候选列表要能说清「授权没了」，而不是一律说「输入变了」。
//
// 两者要分开，是因为下一步不同：授权没了要先恢复授权，输入变了直接重跑。
func TestListCandidatesReportsNotAuthorizedWhenTheAuthorizationIsGone(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := NewSQLiteRepository(db)
	require.NoError(t, candidateadoption.NewSQLiteCandidateAdoptionStore(db).AutoMigrate(ctx))
	require.NoError(t, repo.AutoMigrate(ctx))

	actor := Actor{TenantID: 7, UserID: "member-1"}
	chapterVersion := "chapter-v1"
	basis := candidateadoption.Basis{SpecRevision: 3, ChapterVersionID: &chapterVersion,
		AssetVersions: []candidateadoption.AssetVersion{{AssetID: "asset-1", AssetRevision: 2}}}
	input := Input{Actor: actor, Basis: basis,
		Workspace: candidateadoption.GenerationContext{ProjectID: "project-1", ChapterID: "chapter-1", SpecRevision: 3,
			ChapterVersionID: &chapterVersion, Basis: basis},
		Sources: []Source{{ID: "source-1", ProjectID: "project-1", AssetID: "asset-1", AssetRevision: 2, Status: "available"}}}
	draft := Draft{BodyMarkdown: "Verified [[source:source-1]]", Sources: []Source{{ID: "source-1"}}}
	service := NewService(testAuthorizer{}, testInputs{input: input}, testSources{}, testCurrentness{current: true},
		testModel{draft: draft}, repo, &testEnqueuer{})
	service.NewID = func() string { return "candidate-1" }
	request := Request{ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "Draft this chapter",
		ExpectedSpecRevision: 3, ExpectedChapterVersionID: &chapterVersion}

	run, err := service.Start(ctx, actor, "project-1", "generation-validity-1", request)
	require.NoError(t, err)
	completed, err := service.Execute(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, completed.Status)

	listed, err := service.ListCandidates(ctx, actor, "project-1", "chapter-1")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, candidateadoption.ValidityFresh, listed[0].Validity)

	// 授权没了：候选还在，但它依据的授权不在了。
	revoked := NewService(testAuthorizer{}, testInputs{input: input}, testSources{err: ErrSourceAccessDenied},
		testCurrentness{current: true}, testModel{draft: draft}, repo, &testEnqueuer{})
	revoked.NewID = func() string { return "candidate-1" }
	listed, err = revoked.ListCandidates(ctx, actor, "project-1", "chapter-1")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, candidateadoption.ValidityNotAuthorized, listed[0].Validity)
	require.NotEqual(t, candidateadoption.ValidityStale, listed[0].Validity,
		"授权没了不能算成输入变了：两者的下一步不同")
}
