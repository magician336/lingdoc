package generation

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// generationAdoptionFixture 造一个「生成侧产出候选 → 采纳端采纳」的真实链路。
//
// 它用真的存储与真的采纳服务：候选由生成侧落库，采纳由采纳端读同一张表。
func generationAdoptionFixture(t *testing.T) (*candidateadoption.SQLiteCandidateAdoptionStore, string) {
	t.Helper()
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "adoption.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	store := candidateadoption.NewSQLiteCandidateAdoptionStore(db)
	require.NoError(t, store.AutoMigrate(ctx))
	repo := NewSQLiteRepository(db)
	require.NoError(t, repo.AutoMigrate(ctx))
	require.NoError(t, store.UpsertProject(ctx, "project-1", 0))
	require.NoError(t, store.UpsertChapter(ctx, candidateadoption.Chapter{
		ID: "chapter-1", ProjectID: "project-1", SectionID: "section-1", Title: "研究问题"}, 3))
	// 资料版本是当前基线的一部分：候选的基线要能在这个基线上被复核。
	require.NoError(t, db.Exec(
		"INSERT INTO lingdoc_project_assets (id, project_id, asset_revision) VALUES ('asset-1', 'project-1', 1)").Error)

	basis := candidateadoption.Basis{SpecRevision: 3, TemplateID: "template-demo", TemplateVersion: "1",
		AssetVersions: []candidateadoption.AssetVersion{{AssetID: "asset-1", AssetRevision: 1}}}
	input := Input{Actor: Actor{TenantID: 1, UserID: "user-1"}, Basis: basis,
		Workspace: candidateadoption.GenerationContext{ProjectID: "project-1", ChapterID: "chapter-1",
			SpecRevision: 3, Basis: basis,
			Chapter: candidateadoption.Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "section-1",
				Title: "研究问题"}},
		Sources: []Source{{ID: "source-asset-1", ProjectID: "project-1", AssetID: "asset-1", AssetRevision: 1,
			Status: "available"}}}
	service := NewService(testAuthorizer{}, testInputs{input: input}, testSources{}, testCurrentness{current: true},
		testModel{draft: Draft{BodyMarkdown: "Text [[source:source-asset-1]]", Sources: []Source{{ID: "source-asset-1"}}}},
		repo, &testEnqueuer{})
	service.NewID = func() string { return "candidate-1" }
	request := Request{ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "Draft", ExpectedSpecRevision: 3}
	run, err := service.Start(ctx, Actor{TenantID: 1, UserID: "user-1"}, "project-1", "adoption-key-0001", request)
	require.NoError(t, err)
	completed, err := service.Execute(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, completed.Status)
	require.NotNil(t, completed.CandidateID)
	return store, *completed.CandidateID
}

// TestGenerationCandidateGoesThroughTheAdoptionConfirmation 覆盖「生成侧产出的
// 候选能走通显式确认采纳」。
//
// 采纳必须携带动作、采纳目标与来源的确认载荷；载荷缺了被拒，带了才写得进去。
func TestGenerationCandidateGoesThroughTheAdoptionConfirmation(t *testing.T) {
	ctx := context.Background()
	store, candidateID := generationAdoptionFixture(t)
	allow := adoptionSourcePolicyFunc(func(context.Context, string, string, []string) error { return nil })
	adoption := candidateadoption.NewCandidateAdoptionService(store, allow, adoptionAuthorizerFunc(nil))

	// 缺确认载荷 ⇒ 拒。
	_, err := adoption.AcceptCandidate(ctx, candidateadoption.AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: candidateID, ActorID: "user-1",
		IdempotencyKey: "adoption-no-confirmation-0001", ExpectedSpecRevision: 3,
		ExpectedWorkingCopyRevision: 0, ExpectedBodyHash: workingCopyBodyDigest(""),
	})
	require.ErrorIs(t, err, candidateadoption.ErrInvalidRequest)

	// 确认载荷的动作与候选对不上 ⇒ 拒。
	_, err = adoption.AcceptCandidate(ctx, candidateadoption.AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: candidateID, ActorID: "user-1",
		IdempotencyKey: "adoption-wrong-action-0001", ExpectedSpecRevision: 3,
		ExpectedWorkingCopyRevision: 0, ExpectedBodyHash: workingCopyBodyDigest(""),
		Confirmation: &candidateadoption.AdoptionConfirmation{
			WritingAction: writingaction.ActionRewrite,
			Target:        writingaction.TargetFormalVersionWhole,
			ConfirmedAt:   time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC),
			ConfirmedBy:   "user-1",
			Origin:        writingaction.OriginGenerated,
		},
	})
	require.Error(t, err)

	// 确认载荷齐全 ⇒ 采纳成立：候选带的动作与来源正好是确认载荷确认的那两件事。
	result, err := adoption.AcceptCandidate(ctx, candidateadoption.AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: candidateID, ActorID: "user-1",
		IdempotencyKey: "adoption-confirmed-0001", ExpectedSpecRevision: 3,
		ExpectedWorkingCopyRevision: 0, ExpectedBodyHash: workingCopyBodyDigest(""),
		Confirmation: &candidateadoption.AdoptionConfirmation{
			WritingAction: writingaction.ActionGenerate,
			Target:        writingaction.TargetFormalVersionWhole,
			ConfirmedAt:   time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC),
			ConfirmedBy:   "user-1",
			Origin:        writingaction.OriginGenerated,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Chapter.CurrentVersionID)
}
