package workspace

import (
	"context"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// revocationRewriteModel 允许用例在模型调用**返回之前**撤掉授权。
//
// 撤权发生在模型调用之后、写入之前的那个时机，只能这样注入：生产链路上
// 授权是在两次数据库往返之间变的，测试里没有别的办法把两个时刻分开。
type revocationRewriteModel struct {
	inheritedRewriteModel
	// onCall 在模型返回结果之前执行。
	onCall func()
}

func (m *revocationRewriteModel) RewriteSelected(_ context.Context, _ Actor, p SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	m.calls++
	m.prompt = p
	if m.onCall != nil {
		m.onCall()
	}
	return SelectedRewriteOutput{ReplacementMarkdown: "revised [[source:existing]]", SourceIDs: []string{"existing"}, RunMode: "mock"}, nil
}

// revocationRewriteFixture 造一个可以换模型的改写夹具。
func revocationRewriteFixture(t *testing.T, model SelectedRewriteModel) (*SelectedRewriteService, *inheritedRewriteWorkspace, *inheritedRewriteSources, SelectedRewriteRequest) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rewrite.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	selected := "original [[source:existing]]"
	workspace := &inheritedRewriteWorkspace{copy: WorkingCopy{ProjectID: "project", ChapterID: "chapter", SpecRevision: 1,
		WorkingCopyRevision: 1, BodyMarkdown: selected + " context [[source:outside]]", SourceIDs: []string{"existing", "outside"}}}
	sources := &inheritedRewriteSources{}
	service := NewSelectedRewriteService(db, workspace, sources, model)
	require.NoError(t, service.store.AutoMigrate(context.Background()))
	input := SelectedRewriteRequest{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
		Selection:   RewriteSelection{EndUTF16: len(utf16.Encode([]rune(selected))), SelectedText: selected},
		Instruction: "polish", SourceIDs: []string{}}
	return service, workspace, sources, input
}

// TestSelectedRewriteRevocationBeforeModelCallBlocksTheRun 覆盖撤权的第一个
// 时机：模型调用之前。
//
// 授权没了 ⇒ 拒绝、模型不被调用、一条候选行都不留。
func TestSelectedRewriteRevocationBeforeModelCallBlocksTheRun(t *testing.T) {
	model := &revocationRewriteModel{}
	service, workspace, sources, input := revocationRewriteFixture(t, model)
	sources.denied = true

	_, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "revoked-before-model", input)
	require.ErrorIs(t, err, ErrRewriteSourceDenied)
	require.Zero(t, model.calls, "撤权后模型不得被调用")
	var rows int64
	require.NoError(t, service.store.db.Model(&selectedRewriteRow{}).Count(&rows).Error)
	require.Zero(t, rows, "撤权后不得留下候选行")
	require.Equal(t, int64(1), workspace.copy.WorkingCopyRevision, "工作副本不得被改动")
}

// TestSelectedRewriteRevocationDuringRunLeavesNoUsableCandidate 覆盖撤权的
// 第二个时机：模型已经返回、写入之前。
//
// 候选转 stale、正文清空、错误码说明是授权没了；这样的候选不能被采纳。
func TestSelectedRewriteRevocationDuringRunLeavesNoUsableCandidate(t *testing.T) {
	model := &revocationRewriteModel{}
	service, workspace, sources, input := revocationRewriteFixture(t, model)
	model.onCall = func() { sources.denied = true }

	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "revoked-during-run", input)
	require.NoError(t, err)
	require.Equal(t, 1, model.calls)
	require.Equal(t, "stale", candidate.Status)
	require.Equal(t, "source_access_denied", candidate.ErrorCode)
	require.Empty(t, candidate.ReplacementMarkdown, "撤权后候选正文必须清空")
	require.Equal(t, int64(1), workspace.copy.WorkingCopyRevision, "工作副本不得被改动")

	_, _, err = service.Apply(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", candidate.CandidateID, "apply-revoked", ApplySelectedRewriteInput{
			ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
		})
	// 采纳被拒，而且理由说的是「授权没了」而不是「输入变了」：两种失效要能分开说。
	require.ErrorIs(t, err, ErrRewriteSourceDenied, "撤权后的候选不得被采纳")
}

// TestSelectedRewriteRevocationBeforeCandidateReadIsDenied 覆盖撤权的第三个
// 时机：候选读取之前。
//
// 候选还在，但它依据的授权没了 ⇒ 读取被拒（403 source_access_denied）。
func TestSelectedRewriteRevocationBeforeCandidateReadIsDenied(t *testing.T) {
	model := &revocationRewriteModel{}
	service, _, sources, input := revocationRewriteFixture(t, model)

	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "revoked-before-read", input)
	require.NoError(t, err)
	require.Equal(t, "ready", candidate.Status)

	sources.denied = true
	_, err = service.Get(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", candidate.CandidateID)
	require.ErrorIs(t, err, ErrRewriteSourceDenied)
}
