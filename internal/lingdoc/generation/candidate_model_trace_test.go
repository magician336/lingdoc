package generation

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 产物 → 模型：候选行自己记下产出它的模型。
//
// 事实的来源是**模型调用账本**：那行账本是这个 run 唯一一次真实 provider
// 调用的记录，它的 model_id 就是产出这个候选的模型。读产物的人不必再经
// run → 账本绕一圈才推得出「这次候选是用哪个模型产出的」。
//
// 守护的方向是反的：只要账本里有这一行，候选行就**必须**带上模型；
// 去掉这次填充，本用例如必失败。
func TestTheCandidateRowCarriesTheModelThatProducedIt(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	// 账本表用生产迁移链建出来（000055 建表、000058 加 cost_basis 列）：
	// 候选行的 model_id 就是从这张表读的，账本写入带上 cost_basis（#527），
	// 只应用 000055 会让写入在缺列的表上失败。
	for _, name := range []string{
		"000055_lingdoc_model_call_ledger.up.sql",
		"000058_lingdoc_model_call_ledger_cost_basis.up.sql",
	} {
		migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
		require.NoError(t, err)
		require.NoError(t, db.Exec(string(migration)).Error)
	}

	repo := NewSQLiteRepository(db)
	candidateStore := candidateadoption.NewSQLiteCandidateAdoptionStore(db)
	require.NoError(t, candidateStore.AutoMigrate(ctx))
	require.NoError(t, repo.AutoMigrate(ctx))

	actor := Actor{TenantID: 7, UserID: "member-1"}
	input := Input{Actor: actor, Request: Request{ChapterID: "chapter-1"},
		Workspace: candidateadoption.GenerationContext{ProjectID: "project-1", ChapterID: "chapter-1"}}
	run, err := repo.CreateOrReplay(ctx, actor, "project-1", "request-key-1", "hash-1", input)
	require.NoError(t, err)
	claimed, _, ok, err := repo.Claim(ctx, run.ID)
	require.NoError(t, err)
	require.True(t, ok)

	// 一次真实模型调用 ⇒ 账本一行。
	ledger := workspacecore.NewGORMModelCallLedger(db)
	require.NotNil(t, ledger)
	require.NoError(t, ledger.RecordModelCall(ctx, workspacecore.ModelCallLedgerEntry{
		TenantID: 7, ProjectID: "project-1", Provider: "aliyun",
		ModelID: "lingdoc-chat-qwen3.8-flash-iso", RunMode: "real",
		ParametersJSON: `{"temperature":0.2,"max_completion_tokens":4096}`,
		InputTokens:    2051, OutputTokens: 519,
		Status:     workspacecore.ModelCallLedgerStatusSucceeded,
		SourceKind: workspacecore.ModelCallLedgerSourceGenerationRun, SourceID: run.ID,
	}))

	candidate := candidateadoption.Candidate{ID: "candidate-1", ProjectID: "project-1",
		ChapterID: "chapter-1", RunID: run.ID,
		BodyMarkdown: "body [[source:source-1]]", SourceIDs: []string{"source-1"},
		Validity: "fresh", Basis: candidateadoption.Basis{SpecRevision: 4}}
	_, err = repo.Complete(ctx, run.ID, claimed.ClaimToken, candidate)
	require.NoError(t, err)

	stored, err := candidateStore.GetCandidate(ctx, "project-1", "candidate-1")
	require.NoError(t, err)
	require.Equal(t, "lingdoc-chat-qwen3.8-flash-iso", stored.ModelID,
		"a candidate produced by a real model call must say which model produced it")
}

// 没有 provider 调用 ⇒ 候选行也没有模型。两个字段成对：账本有行则候选必有
// 模型，账本无行则候选必无模型。一行「空模型」若出现在有真实调用的产物上，
// 那就是记录口径漏了，不是「这次没调模型」。
func TestACandidateWithoutAModelCallCarriesNoModel(t *testing.T) {
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
	run, err := repo.CreateOrReplay(ctx, actor, "project-1", "request-key-1", "hash-1", input)
	require.NoError(t, err)
	claimed, _, ok, err := repo.Claim(ctx, run.ID)
	require.NoError(t, err)
	require.True(t, ok)

	candidate := candidateadoption.Candidate{ID: "candidate-1", ProjectID: "project-1",
		ChapterID: "chapter-1", RunID: run.ID,
		BodyMarkdown: "body [[source:source-1]]", SourceIDs: []string{"source-1"},
		Validity: "fresh", Basis: candidateadoption.Basis{SpecRevision: 4}}
	_, err = repo.Complete(ctx, run.ID, claimed.ClaimToken, candidate)
	require.NoError(t, err)

	stored, err := candidateStore.GetCandidate(ctx, "project-1", "candidate-1")
	require.NoError(t, err)
	require.Empty(t, stored.ModelID,
		"a candidate with no provider call behind it must not claim a model")
}
