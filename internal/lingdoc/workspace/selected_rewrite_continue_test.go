package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 续写夹具的正文三段。
//
// 续写与改写的差别在**上下文需求**：续写要接得上，所以选区前面必须有前文。
// 夹具因此把正文写成「前文 / 选区 / 后文」三段，选区落在中间——前文与后文
// 都真的存在，断言「前文进入模型请求」才有东西可断言。
const (
	continueBefore   = "前文第一段。"
	continueSelected = "第二段被选中 [[source:existing]]"
	continueAfter    = "第三段。"
)

// continueRewriteModel 回一段带来源标记的续写正文。
type continueRewriteModel struct {
	calls  int
	prompt SelectedRewritePrompt
	// onCall 在模型返回结果之前执行：它让用例能把撤权插在模型调用与写入之间。
	onCall func()
	// failing 让这次运行失败：它让用例能看住「失败留痕」。
	failing bool
}

func (*continueRewriteModel) RunMode() string { return "mock" }

func (m *continueRewriteModel) RewriteSelected(_ context.Context, _ Actor, p SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	m.calls++
	m.prompt = p
	if m.onCall != nil {
		m.onCall()
	}
	if m.failing {
		return SelectedRewriteOutput{}, errors.New("模型不可用")
	}
	return SelectedRewriteOutput{ReplacementMarkdown: "续写出来的内容 [[source:existing]]",
		SourceIDs: []string{"existing"}, RunMode: "mock"}, nil
}

// continueWorkspace 记下工作副本的写入。
//
// 它实现 ApplyRewriteResult，让「确认载荷齐全才写得进去」这条断言看得见：
// 写入的次数与写入的正文都在这里。
type continueWorkspace struct {
	inheritedRewriteWorkspace
	writes []core.SaveWorkingCopyInput
}

func (w *continueWorkspace) ApplyRewriteResult(_ context.Context, _ Actor, _, _, _ string,
	input core.SaveWorkingCopyInput, _ []core.ReviewItem) (WorkingCopyMutationResult, error) {
	w.writes = append(w.writes, input)
	w.copy.BodyMarkdown = input.BodyMarkdown
	w.copy.WorkingCopyRevision++
	return WorkingCopyMutationResult{WorkingCopy: w.copy}, nil
}

// continueFixture 造一个「选区前面有前文」的续写夹具。
func continueFixture(t *testing.T) (*SelectedRewriteService, *continueWorkspace, *continueRewriteModel, SelectedRewriteRequest) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rewrite.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	body := continueBefore + continueSelected + continueAfter
	workspace := &continueWorkspace{inheritedRewriteWorkspace: inheritedRewriteWorkspace{
		copy: WorkingCopy{ProjectID: "project", ChapterID: "chapter", SpecRevision: 1,
			WorkingCopyRevision: 1, BodyMarkdown: body, SourceIDs: []string{"existing"}}}}
	sources := &inheritedRewriteSources{}
	model := &continueRewriteModel{}
	service := NewSelectedRewriteService(db, workspace, sources, model)
	require.NoError(t, service.store.AutoMigrate(context.Background()))
	input := SelectedRewriteRequest{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
		Selection: RewriteSelection{
			StartUTF16:   len(utf16.Encode([]rune(continueBefore))),
			EndUTF16:     len(utf16.Encode([]rune(continueBefore + continueSelected))),
			SelectedText: continueSelected},
		Instruction: "polish", SourceIDs: []string{}}
	return service, workspace, model, input
}

// continueRequest 给一份走动作路径的续写请求。
func continueRequest(base SelectedRewriteRequest) SelectedRewriteRequest {
	base.WritingAction = actionPointer(writingaction.ActionContinue)
	base.OutputParams = &writingaction.OutputParams{
		Length: writingaction.LengthShort,
		Tone:   writingaction.ToneFormal,
	}
	base.Scope = actionPointer(writingaction.ScopeSection)
	return base
}

// TestSelectedRewriteContinueRunsAndRecordsTheActionOriginAndRunMode 断言续写
// 产出的候选带着动作、来源与运行模式。
func TestSelectedRewriteContinueRunsAndRecordsTheActionOriginAndRunMode(t *testing.T) {
	service, _, model, base := continueFixture(t)
	input := continueRequest(base)
	input.Instruction = ""
	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "continue-key", input)
	require.NoError(t, err)
	require.Equal(t, writingaction.ActionContinue, candidate.WritingAction)
	require.Equal(t, writingaction.OriginGenerated, candidate.Origin)
	require.Equal(t, candidateadoption.RunModeMock, candidate.RunMode)
	require.Equal(t, candidateadoption.ValidityFresh, candidate.Validity)
	require.Equal(t, 1, model.calls)
}

// TestSelectedRewriteContinuePutsThePrecedingTextIntoTheModelRequest 断言续写
// 的上下文需求包含选区及其前文，且前文真的进了模型请求。
//
// 前文进入模型请求是**通用行为**：选区管道本来就送前文。续写与改写的差别
// 只在动作指令与产出参数，执行方不按动作名决定任何事。
func TestSelectedRewriteContinuePutsThePrecedingTextIntoTheModelRequest(t *testing.T) {
	service, _, model, base := continueFixture(t)
	input := continueRequest(base)
	_, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "continue-key", input)
	require.NoError(t, err)
	require.Equal(t, continueBefore, model.prompt.ContextBefore, "前文必须进模型请求")
	require.Equal(t, continueAfter, model.prompt.ContextAfter)
	require.Contains(t, model.prompt.Instruction, "接着选区及其前文继续写")
	// 产出参数写进动作指令：长度与语气都看得见。
	require.Contains(t, model.prompt.Instruction, "产出长度")
	require.Contains(t, model.prompt.Instruction, "产出语气")
}

// TestSelectedRewriteContinueRefusesIllegalDeclarations 覆盖续写的声明闭合：
// 整章范围、越界或缺失的产出参数、这个管道不做的动作。
//
// 四种都在**模型调用之前**被拒，且一条候选都不产生。
func TestSelectedRewriteContinueRefusesIllegalDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*SelectedRewriteRequest)
		want   error
	}{
		{"整章范围", func(r *SelectedRewriteRequest) {
			r.Scope = actionPointer(writingaction.ScopeChapter)
		}, writingaction.ErrScopeNotAllowed},
		{"长度取值越界", func(r *SelectedRewriteRequest) {
			r.OutputParams = &writingaction.OutputParams{Length: writingaction.Length("huge"), Tone: writingaction.ToneFormal}
		}, writingaction.ErrInvalidOutputParams},
		{"产出参数没给齐", func(r *SelectedRewriteRequest) {
			r.OutputParams = &writingaction.OutputParams{Length: writingaction.LengthShort}
		}, writingaction.ErrInvalidOutputParams},
		{"动作用不上的参数被带上", func(r *SelectedRewriteRequest) {
			r.OutputParams = &writingaction.OutputParams{Length: writingaction.LengthShort,
				Tone: writingaction.ToneFormal, Ratio: 50}
		}, writingaction.ErrInvalidOutputParams},
		{"这个管道不做的动作", func(r *SelectedRewriteRequest) {
			// 评审是这条管道还没做的动作（#348）：拿它当「管道不做这个动作」
			// 的样本。转写曾经也当过这个样本——它现在是这条管道做的动作之一。
			// 评审允许选段范围，所以请求到得了管道归属判定那一支；它没有产出
			// 参数，所以请求里不带产出参数。
			r.WritingAction = actionPointer(writingaction.ActionReview)
			r.OutputParams = &writingaction.OutputParams{}
		}, ErrRewriteInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _, model, base := continueFixture(t)
			input := continueRequest(base)
			tc.mutate(&input)
			_, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
				"project", "chapter", "continue-key", input)
			require.ErrorIs(t, err, tc.want)
			require.Zero(t, model.calls, "非法声明不得触达模型")
			var rows int64
			require.NoError(t, service.store.db.Model(&selectedRewriteRow{}).Count(&rows).Error)
			require.Zero(t, rows, "非法声明不得留下候选行")
		})
	}
}

// TestSelectedRewriteContinueFailureKeepsItsStatusAndReason 断言失败的那次
// 运行留下状态与原因，重试用新键不写工作副本。
func TestSelectedRewriteContinueFailureKeepsItsStatusAndReason(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, workspace, model, base := continueFixture(t)
	model.failing = true
	failed, err := service.Create(context.Background(), actor, "project", "chapter", "continue-failed", continueRequest(base))
	require.NoError(t, err)
	require.Empty(t, failed.Validity)
	require.Equal(t, "rewrite_failed", failed.ErrorCode)
	require.Empty(t, failed.ReplacementMarkdown)
	require.Empty(t, workspace.writes, "失败的运行不得写工作副本")

	// 重试用新幂等键：另起一次运行，工作副本仍然没被写。
	model.failing = false
	retried, err := service.Create(context.Background(), actor, "project", "chapter", "continue-retry", continueRequest(base))
	require.NoError(t, err)
	require.Equal(t, candidateadoption.ValidityFresh, retried.Validity)
	require.NotEqual(t, failed.CandidateID, retried.CandidateID)
	require.Empty(t, workspace.writes, "重试不得写工作副本")
}

// TestSelectedRewriteContinueAndRewriteConflictOnTheSameIdempotencyKey 断言同一
// 个幂等键上的不同动作被判成冲突。
//
// 动作在请求摘要里 ⇒ 摘要天然不同 ⇒ 同键不同动作是冲突，不是重放。
func TestSelectedRewriteContinueAndRewriteConflictOnTheSameIdempotencyKey(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, _, model, base := continueFixture(t)
	_, err := service.Create(context.Background(), actor, "project", "chapter", "shared-key", continueRequest(base))
	require.NoError(t, err)
	require.Equal(t, 1, model.calls)

	_, err = service.Create(context.Background(), actor, "project", "chapter", "shared-key", actionRequest(base))
	require.ErrorIs(t, err, ErrRewriteKeyConflict)
	require.Equal(t, 1, model.calls, "冲突的请求不得再触达模型")
}

// TestSelectedRewriteContinueRevocationBlocksTheRunTimings 覆盖撤权的前三个
// 时机对续写同样成立：模型调用之前、运行中、候选读取之前。
//
// 第四个时机（采纳之前）由采纳确认那组用例覆盖：那里要带上确认载荷。
func TestSelectedRewriteContinueRevocationBlocksTheRunTimings(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	t.Run("模型调用之前", func(t *testing.T) {
		service, workspace, model, base := continueFixture(t)
		service.sources.(*inheritedRewriteSources).denied = true
		_, err := service.Create(context.Background(), actor, "project", "chapter", "revoked-before-model", continueRequest(base))
		require.ErrorIs(t, err, ErrRewriteSourceDenied)
		require.Zero(t, model.calls)
		require.Equal(t, int64(1), workspace.copy.WorkingCopyRevision)
	})
	t.Run("运行中", func(t *testing.T) {
		service, workspace, model, base := continueFixture(t)
		sources := service.sources.(*inheritedRewriteSources)
		model.onCall = func() { sources.denied = true }
		candidate, err := service.Create(context.Background(), actor, "project", "chapter", "revoked-during-run", continueRequest(base))
		require.NoError(t, err)
		require.Equal(t, 1, model.calls)
		require.Equal(t, candidateadoption.ValidityNotAuthorized, candidate.Validity)
		require.Equal(t, "source_access_denied", candidate.ErrorCode)
		require.Empty(t, candidate.ReplacementMarkdown)
		require.Equal(t, int64(1), workspace.copy.WorkingCopyRevision)
	})
	t.Run("候选读取之前", func(t *testing.T) {
		service, _, _, base := continueFixture(t)
		candidate, err := service.Create(context.Background(), actor, "project", "chapter", "revoked-before-read", continueRequest(base))
		require.NoError(t, err)
		require.Equal(t, candidateadoption.ValidityFresh, candidate.Validity)
		service.sources.(*inheritedRewriteSources).denied = true
		_, err = service.Get(context.Background(), actor, "project", candidate.CandidateID)
		require.ErrorIs(t, err, ErrRewriteSourceDenied)
	})
}

// TestWritingActionsEndpointServesContinue 覆盖动作表端点上的续写。
//
// 续写与其它动作一样从服务端取：允许范围、可用产出参数、产出形状与允许的
// 采纳目标都由词汇包给出，前端不写第二份动作清单。
func TestWritingActionsEndpointServesContinue(t *testing.T) {
	service, _, _, _ := continueFixture(t)
	engine := rewriteHTTPRouter(t, service)
	request := httptest.NewRequest(http.MethodGet,
		"/api/lingdoc/projects/project/writing-actions?scope=section", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, rewriteHTTPContext(request))
	require.Equal(t, http.StatusOK, response.Code)

	var envelope struct {
		Data []writingaction.Description `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	var described *writingaction.Description
	for index := range envelope.Data {
		if envelope.Data[index].Action == writingaction.ActionContinue {
			described = &envelope.Data[index]
		}
	}
	require.NotNil(t, described, "动作表里必须有续写")
	require.Equal(t, []writingaction.Scope{writingaction.ScopeSection}, described.Scopes)
	require.Equal(t, writingaction.ProduceProse, described.Produce)
	require.Len(t, described.Targets, 3)
	require.Len(t, described.OutputParams, 2)
}
