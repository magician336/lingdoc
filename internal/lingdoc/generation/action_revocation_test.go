package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// revocationSourceValidator 让用例在指定的一次来源复核上撤掉授权。
//
// 撤权发生在两次数据库往返之间，测试里没有别的办法把两个时刻分开：计数就是
// 那个时刻。
type revocationSourceValidator struct {
	calls   int
	revoked bool
}

func (v *revocationSourceValidator) ValidateGenerationSources(context.Context, Actor, string, []Source) error {
	v.calls++
	if v.revoked {
		return ErrSourceAccessDenied
	}
	return nil
}

// revocationGenerationModel 在模型返回之前撤掉授权。
type revocationGenerationModel struct {
	draft  Draft
	onCall func()
}

func (m *revocationGenerationModel) RunMode() string { return "mock" }

func (m *revocationGenerationModel) Generate(context.Context, Input) (Draft, error) {
	if m.onCall != nil {
		m.onCall()
	}
	return m.draft, nil
}

// generationRevocationFixture 造一个带来源复核的生成夹具。
func generationRevocationFixture(t *testing.T) (*Service, *testRepository, *revocationSourceValidator) {
	t.Helper()
	svc, repo := actionFixture()
	validator := &revocationSourceValidator{}
	svc.Sources = validator
	return svc, repo, validator
}

// TestGenerationRevocationBeforeModelCallBlocksTheRun 覆盖撤权的第一个时机：
// 模型调用之前。
//
// 授权没了 ⇒ 拒绝、模型不被调用、一条候选都不留。
func TestGenerationRevocationBeforeModelCallBlocksTheRun(t *testing.T) {
	svc, repo, validator := generationRevocationFixture(t)
	model := &countingModel{draft: Draft{BodyMarkdown: "Text [[source:source-asset-1]]", Sources: []Source{{ID: "source-asset-1"}}}}
	svc.Model = model
	validator.revoked = true

	run, err := svc.Execute(context.Background(), repo.run.ID)
	require.NoError(t, err, "撤权在模型调用前 ⇒ 运行失败，失败原因在运行上")
	require.Equal(t, StatusFailed, run.Status)
	require.NotNil(t, run.Error)
	require.Equal(t, "source_access_denied", run.Error.Code)
	require.Zero(t, model.calls, "撤权后模型不得被调用")
	require.Empty(t, repo.candidate.ID, "撤权后不得留下候选")
}

// TestGenerationRevocationDuringRunLeavesNoUsableCandidate 覆盖撤权的第二个
// 时机：模型已经返回、写入之前。
//
// 候选不发布、运行状态与原因说得清是授权没了。
func TestGenerationRevocationDuringRunLeavesNoUsableCandidate(t *testing.T) {
	svc, repo, validator := generationRevocationFixture(t)
	model := &revocationGenerationModel{
		draft:  Draft{BodyMarkdown: "Text [[source:source-asset-1]]", Sources: []Source{{ID: "source-asset-1"}}},
		onCall: func() { validator.revoked = true },
	}
	svc.Model = model

	run, err := svc.Execute(context.Background(), repo.run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, run.Status)
	require.NotNil(t, repo.failure)
	require.Equal(t, "source_access_denied", repo.failure.Code)
	require.Empty(t, repo.candidate.ID, "撤权后不得发布候选")
}

// TestGenerationRevocationBeforeCandidateReadIsDenied 覆盖撤权的第三个时机：
// 候选读取之前。
//
// 候选还在，但它依据的授权没了 ⇒ 读取被拒。
func TestGenerationRevocationBeforeCandidateReadIsDenied(t *testing.T) {
	svc, repo, validator := generationRevocationFixture(t)
	run, err := svc.Execute(context.Background(), repo.run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, run.Status)

	validator.revoked = true
	_, err = svc.GetCandidate(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, "project-1", repo.run.ID)
	require.ErrorIs(t, err, ErrSourceAccessDenied)
}

// TestGenerationRevocationBeforeAdoptionIsRefused 覆盖撤权的第四个时机：采纳之前。
//
// 生成侧产出的候选在授权没了之后不得被采纳，而且零写入：没有正式版本。
func TestGenerationRevocationBeforeAdoptionIsRefused(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "revocation.db")), &gorm.Config{})
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

	input := Input{Actor: Actor{TenantID: 1, UserID: "user-1"},
		Basis: candidateadoption.Basis{SpecRevision: 3,
			AssetVersions: []candidateadoption.AssetVersion{{AssetID: "asset-1", AssetRevision: 1}}},
		Workspace: candidateadoption.GenerationContext{ProjectID: "project-1", ChapterID: "chapter-1", SpecRevision: 3,
			Basis: candidateadoption.Basis{SpecRevision: 3,
				AssetVersions: []candidateadoption.AssetVersion{{AssetID: "asset-1", AssetRevision: 1}}},
			Chapter: candidateadoption.Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "section-1",
				Title: "研究问题"}},
		Sources: []Source{{ID: "source-asset-1", ProjectID: "project-1", AssetID: "asset-1", AssetRevision: 1,
			Status: "available"}}}
	svc := NewService(testAuthorizer{}, testInputs{input: input}, testSources{}, testCurrentness{current: true},
		testModel{draft: Draft{BodyMarkdown: "Text [[source:source-asset-1]]", Sources: []Source{{ID: "source-asset-1"}}}},
		repo, &testEnqueuer{})
	svc.NewID = func() string { return "candidate-1" }
	request := Request{ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "Draft", ExpectedSpecRevision: 3}
	run, err := svc.Start(ctx, Actor{TenantID: 1, UserID: "user-1"}, "project-1", "revocation-adopt-0001", request)
	require.NoError(t, err)
	completed, err := svc.Execute(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, completed.Status)
	require.NotNil(t, completed.CandidateID)

	// 授权没了：采纳端按当前授权复核候选依据的来源 ⇒ 拒绝。
	revoked := adoptionSourcePolicyFunc(func(context.Context, string, string, []string) error {
		return candidateadoption.ErrSourceAccessDenied
	})
	adoption := candidateadoption.NewCandidateAdoptionService(store, revoked, adoptionAuthorizerFunc(nil))
	_, err = adoption.AcceptCandidate(ctx, candidateadoption.AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: *completed.CandidateID, ActorID: "user-1",
		IdempotencyKey: "revocation-adoption-0001", ExpectedSpecRevision: 3,
		ExpectedWorkingCopyRevision: 0, ExpectedBodyHash: workingCopyBodyDigest(""),
		Confirmation: &candidateadoption.AdoptionConfirmation{
			WritingAction: writingaction.ActionGenerate,
			Target:        writingaction.TargetFormalVersionWhole,
			ConfirmedAt:   time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC),
			ConfirmedBy:   "user-1",
			Origin:        writingaction.OriginGenerated,
		},
	})
	require.ErrorIs(t, err, candidateadoption.ErrSourceAccessDenied)

	var versionCount int64
	require.NoError(t, db.Table("lingdoc_chapter_versions").Count(&versionCount).Error)
	require.Zero(t, versionCount, "撤权后的采纳不得建正式版本")
}

// adoptionSourcePolicyFunc 是一个采纳端的来源策略替身。
type adoptionSourcePolicyFunc func(context.Context, string, string, []string) error

func (f adoptionSourcePolicyFunc) Validate(ctx context.Context, projectID, actorID string, sourceIDs []string) error {
	return f(ctx, projectID, actorID, sourceIDs)
}

// adoptionAuthorizerFunc 是一个采纳端的授权替身。
type adoptionAuthorizerFunc func(context.Context, string, string, string) error

func (f adoptionAuthorizerFunc) Authorize(ctx context.Context, projectID, actorID, capability string) error {
	if f == nil {
		return nil
	}
	return f(ctx, projectID, actorID, capability)
}

// workingCopyBodyDigest 是工作副本正文的摘要。
//
// 它与服务端用的是同一条规则：hex(sha256(正文 UTF-8 字节))。
func workingCopyBodyDigest(body string) string {
	digest := sha256.Sum256([]byte(body))
	return hex.EncodeToString(digest[:])
}
