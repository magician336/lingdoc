package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 压缩夹具的正文三段。
//
// 压缩与改写、续写的差别在**产出参数**：压缩的目标是比例，不是长度。夹具因此
// 保留前文与后文，让「压缩只替换选区」这件事有东西可断言。
const (
	compressBefore   = "前文第一段。"
	compressSelected = "第二段被选中 [[source:existing]]"
	compressAfter    = "第三段。"
)

// compressRewriteModel 回一段带来源标记的压缩正文。
type compressRewriteModel struct {
	calls  int
	prompt SelectedRewritePrompt
	// onCall 在模型返回结果之前执行：它让用例能把撤权插在模型调用与写入之间。
	onCall func()
	// failing 让这次运行失败：它让用例能看住「失败留痕」。
	failing bool
	// cancelAware 让模型像真的适配器那样：上下文被取消时它交回上下文的错误。
	cancelAware bool
	// reviewItems 是模型交回的待核事项：它与正文分列，不混进候选正文。
	reviewItems []string
}

func (*compressRewriteModel) RunMode() string { return "mock" }

func (m *compressRewriteModel) RewriteSelected(ctx context.Context, _ Actor, p SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	m.calls++
	m.prompt = p
	if m.onCall != nil {
		m.onCall()
	}
	if m.cancelAware && ctx.Err() != nil {
		return SelectedRewriteOutput{}, ctx.Err()
	}
	if m.failing {
		return SelectedRewriteOutput{}, errors.New("模型不可用")
	}
	return SelectedRewriteOutput{ReplacementMarkdown: "压缩出来的内容 [[source:existing]]",
		SourceIDs: []string{"existing"}, ReviewItems: m.reviewItems, RunMode: "mock"}, nil
}

// compressWorkspace 记下工作副本的写入。
//
// 它实现 ApplyRewriteResult，让「确认载荷齐全才写得进去」这条断言看得见：
// 写入的次数与写入的正文都在这里。
type compressWorkspace struct {
	inheritedRewriteWorkspace
	writes []core.SaveWorkingCopyInput
}

func (w *compressWorkspace) ApplyRewriteResult(_ context.Context, _ Actor, _, _, _ string,
	input core.SaveWorkingCopyInput, _ []core.ReviewItem) (WorkingCopyMutationResult, error) {
	w.writes = append(w.writes, input)
	w.copy.BodyMarkdown = input.BodyMarkdown
	w.copy.WorkingCopyRevision++
	return WorkingCopyMutationResult{WorkingCopy: w.copy}, nil
}

// compressFixture 造一个压缩夹具。
func compressFixture(t *testing.T) (*SelectedRewriteService, *compressWorkspace, *compressRewriteModel, SelectedRewriteRequest) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rewrite.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	body := compressBefore + compressSelected + compressAfter
	workspace := &compressWorkspace{inheritedRewriteWorkspace: inheritedRewriteWorkspace{
		copy: WorkingCopy{ProjectID: "project", ChapterID: "chapter", SpecRevision: 1,
			WorkingCopyRevision: 1, BodyMarkdown: body, SourceIDs: []string{"existing"}}}}
	sources := &inheritedRewriteSources{}
	model := &compressRewriteModel{}
	service := NewSelectedRewriteService(db, workspace, sources, model)
	require.NoError(t, service.store.AutoMigrate(context.Background()))
	input := SelectedRewriteRequest{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
		Selection: RewriteSelection{
			StartUTF16:   len(utf16.Encode([]rune(compressBefore))),
			EndUTF16:     len(utf16.Encode([]rune(compressBefore + compressSelected))),
			SelectedText: compressSelected},
		Instruction: "polish", SourceIDs: []string{}}
	return service, workspace, model, input
}

// compressRequest 给一份走动作路径的压缩请求。
func compressRequest(base SelectedRewriteRequest) SelectedRewriteRequest {
	base.WritingAction = actionPointer(writingaction.ActionCompress)
	base.OutputParams = &writingaction.OutputParams{
		Ratio: 40,
		Tone:  writingaction.ToneFormal,
	}
	base.Scope = actionPointer(writingaction.ScopeSection)
	return base
}

// compressConfirmation 造一份「采纳到工作副本选段」的确认载荷。
func compressConfirmation(actor string) *candidateadoption.AdoptionConfirmation {
	return &candidateadoption.AdoptionConfirmation{
		WritingAction: writingaction.ActionCompress,
		Target:        writingaction.TargetWorkingCopyPartial,
		Scope: &candidateadoption.AdoptionScope{
			StartUTF16: len(utf16.Encode([]rune(compressBefore))),
			EndUTF16:   len(utf16.Encode([]rune(compressBefore + compressSelected))),
		},
		ConfirmedAt: time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC),
		ConfirmedBy: actor,
		Origin:      writingaction.OriginGenerated,
	}
}

// TestSelectedRewriteCompressRunsAndRecordsTheActionOriginAndRunMode 断言压缩
// 产出的候选带着动作、来源与运行模式。
func TestSelectedRewriteCompressRunsAndRecordsTheActionOriginAndRunMode(t *testing.T) {
	service, _, model, base := compressFixture(t)
	input := compressRequest(base)
	input.Instruction = ""
	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "compress-key", input)
	require.NoError(t, err)
	require.Equal(t, writingaction.ActionCompress, candidate.WritingAction)
	require.Equal(t, writingaction.OriginGenerated, candidate.Origin)
	require.Equal(t, candidateadoption.RunModeMock, candidate.RunMode)
	require.Equal(t, "ready", candidate.Status)
	require.Equal(t, 1, model.calls)
}

// TestSelectedRewriteCompressRendersTheRatioAndTheToneIntoTheModelRequest 断言
// 压缩的产出参数（比例与语气）在模型请求里看得见，而且长度不在其中。
//
// 比例与语气由词汇包渲染进动作指令：执行方不解释产出参数。比例取 10..90 的
// 整数百分比，所以指令里看得见的是「目标比例：40%」。
func TestSelectedRewriteCompressRendersTheRatioAndTheToneIntoTheModelRequest(t *testing.T) {
	service, _, model, base := compressFixture(t)
	_, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "compress-key", compressRequest(base))
	require.NoError(t, err)
	require.Contains(t, model.prompt.Instruction, "把选中的片段压缩到指定比例")
	require.Contains(t, model.prompt.Instruction, "目标比例：40%")
	require.Contains(t, model.prompt.Instruction, "产出语气：正式（formal）")
	require.NotContains(t, model.prompt.Instruction, "产出长度",
		"压缩用比例不用长度，模型请求里不该出现长度")
}

// TestSelectedRewriteCompressRefusesIllegalDeclarations 覆盖压缩的声明闭合：
// 整章范围、越界或缺失的比例、缺失的语气、这个动作用不上的参数、这个管道
// 不做的动作。
//
// 每一种都在**模型调用之前**被拒，且一条候选都不产生。
func TestSelectedRewriteCompressRefusesIllegalDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*SelectedRewriteRequest)
		want   error
	}{
		{"整章范围", func(r *SelectedRewriteRequest) {
			r.Scope = actionPointer(writingaction.ScopeChapter)
		}, writingaction.ErrScopeNotAllowed},
		{"比例低于下限", func(r *SelectedRewriteRequest) {
			r.OutputParams = &writingaction.OutputParams{Ratio: 9, Tone: writingaction.ToneFormal}
		}, writingaction.ErrInvalidOutputParams},
		{"比例高于上限", func(r *SelectedRewriteRequest) {
			r.OutputParams = &writingaction.OutputParams{Ratio: 91, Tone: writingaction.ToneFormal}
		}, writingaction.ErrInvalidOutputParams},
		{"比例没给", func(r *SelectedRewriteRequest) {
			r.OutputParams = &writingaction.OutputParams{Tone: writingaction.ToneFormal}
		}, writingaction.ErrInvalidOutputParams},
		{"语气没给", func(r *SelectedRewriteRequest) {
			r.OutputParams = &writingaction.OutputParams{Ratio: 40}
		}, writingaction.ErrInvalidOutputParams},
		{"动作用不上的长度参数被带上", func(r *SelectedRewriteRequest) {
			r.OutputParams = &writingaction.OutputParams{Ratio: 40, Tone: writingaction.ToneFormal,
				Length: writingaction.LengthShort}
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
			service, _, model, base := compressFixture(t)
			input := compressRequest(base)
			tc.mutate(&input)
			_, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
				"project", "chapter", "compress-key", input)
			require.ErrorIs(t, err, tc.want)
			require.Zero(t, model.calls, "非法声明不得触达模型")
			var rows int64
			require.NoError(t, service.store.db.Model(&selectedRewriteRow{}).Count(&rows).Error)
			require.Zero(t, rows, "非法声明不得留下候选行")
		})
	}
}

// TestWritingActionsEndpointServesCompress 覆盖动作表端点上的压缩。
//
// 压缩与其它动作一样从服务端取：允许范围、可用产出参数、产出形状与允许的
// 采纳目标都由词汇包给出，前端不写第二份动作清单。比例是**连续区间型**维度：
// 取值列表为空，边界在 min / max 上。
func TestWritingActionsEndpointServesCompress(t *testing.T) {
	service, _, _, _ := compressFixture(t)
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
		if envelope.Data[index].Action == writingaction.ActionCompress {
			described = &envelope.Data[index]
		}
	}
	require.NotNil(t, described, "动作表里必须有压缩")
	require.Equal(t, []writingaction.Scope{writingaction.ScopeSection}, described.Scopes)
	require.Equal(t, writingaction.ProduceProse, described.Produce)
	require.Len(t, described.Targets, 3)
	require.Len(t, described.OutputParams, 2)

	var ratio, tone *writingaction.ParamOption
	for index := range described.OutputParams {
		switch described.OutputParams[index].Name {
		case "ratio":
			ratio = &described.OutputParams[index]
		case "tone":
			tone = &described.OutputParams[index]
		}
	}
	require.NotNil(t, ratio, "压缩的产出参数里有比例")
	require.Empty(t, ratio.Values, "比例是连续区间型维度，没有取值列表")
	require.Equal(t, 10, ratio.Min)
	require.Equal(t, 90, ratio.Max)
	require.NotNil(t, tone, "压缩的产出参数里有语气")
	require.Equal(t, []string{"neutral", "formal", "academic", "plain"}, tone.Values)
}

// TestSelectedRewriteCompressOverHTTP 覆盖压缩端点的 HTTP 接缝。
//
// 缺动作、未知动作、整章范围、越界的比例、非整数的比例都在这里被拒；合法的
// 压缩请求返回带动作、来源与运行模式的候选。
func TestSelectedRewriteCompressOverHTTP(t *testing.T) {
	service, _, _, base := compressFixture(t)
	engine := rewriteHTTPRouter(t, service)

	post := func(t *testing.T, key string, payload map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(payload)
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodPost,
			"/api/lingdoc/projects/project/chapters/chapter/rewrite-candidates", strings.NewReader(string(body)))
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, rewriteHTTPContext(request))
		return response
	}

	baseBody := func() map[string]any {
		return map[string]any{
			"base_chapter_version_id":        nil,
			"expected_spec_revision":         1,
			"expected_working_copy_revision": 1,
			"selection":                      base.Selection,
			"instruction":                    "",
			"source_ids":                     []string{},
		}
	}

	for _, tc := range []struct {
		name string
		key  string
		body map[string]any
	}{
		{"缺动作", "http-compress-missing", func() map[string]any {
			body := baseBody()
			body["output_params"] = map[string]any{"ratio": 40, "tone": "formal"}
			return body
		}()},
		{"未知动作", "http-compress-unknown", func() map[string]any {
			body := baseBody()
			body["writing_action"] = "polish"
			body["output_params"] = map[string]any{"ratio": 40, "tone": "formal"}
			return body
		}()},
		{"整章范围", "http-compress-chapter", func() map[string]any {
			body := baseBody()
			body["writing_action"] = "compress"
			body["scope"] = "chapter"
			body["output_params"] = map[string]any{"ratio": 40, "tone": "formal"}
			return body
		}()},
		{"比例越界", "http-compress-ratio", func() map[string]any {
			body := baseBody()
			body["writing_action"] = "compress"
			body["scope"] = "section"
			body["output_params"] = map[string]any{"ratio": 95, "tone": "formal"}
			return body
		}()},
		{"比例不是整数", "http-compress-fraction", func() map[string]any {
			body := baseBody()
			body["writing_action"] = "compress"
			body["scope"] = "section"
			body["output_params"] = map[string]any{"ratio": 45.5, "tone": "formal"}
			return body
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := post(t, tc.key, tc.body)
			require.Equal(t, http.StatusBadRequest, response.Code)
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.Equal(t, "invalid_request", envelope.Error.Code)
		})
	}

	accepted := baseBody()
	accepted["writing_action"] = "compress"
	accepted["scope"] = "section"
	accepted["output_params"] = map[string]any{"ratio": 40, "tone": "formal"}
	response := post(t, "http-compress-accepted", accepted)
	require.Equal(t, http.StatusCreated, response.Code)
	var envelope struct {
		Data struct {
			CandidateID   string `json:"candidate_id"`
			WritingAction string `json:"writing_action"`
			Origin        string `json:"origin"`
			RunMode       string `json:"run_mode"`
			Status        string `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.NotEmpty(t, envelope.Data.CandidateID)
	require.Equal(t, "compress", envelope.Data.WritingAction)
	require.Equal(t, "ai_generated", envelope.Data.Origin)
	require.Equal(t, "mock", envelope.Data.RunMode)
	require.Equal(t, "ready", envelope.Data.Status)
}

// TestSelectedRewriteCompressCandidateShowsItsContextAndSeparatesReviewItems
// 断言候选带着项目、章节、上下文修订号与授权来源，并且待核事项与正文分列。
//
// 这两行是矩阵里的 A6 与 A7：上下文要看得见，三类内容不能混成一段。
func TestSelectedRewriteCompressCandidateShowsItsContextAndSeparatesReviewItems(t *testing.T) {
	service, _, model, base := compressFixture(t)
	model.reviewItems = []string{"压缩后的事实需要核对"}
	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "compress-key", compressRequest(base))
	require.NoError(t, err)

	// A6：上下文看得见。
	require.Equal(t, "project", candidate.ProjectID)
	require.Equal(t, "chapter", candidate.ChapterID)
	require.Equal(t, int64(1), candidate.SpecRevision)
	require.Equal(t, int64(1), candidate.WorkingCopyRevision)
	require.Len(t, candidate.AuthorizedSources, 1)
	require.Equal(t, "existing", candidate.AuthorizedSources[0].SourceID)
	require.False(t, candidate.AuthorizedSources[0].AuthorizedAt.IsZero())

	// A7：待核事项与正文分列。
	require.Len(t, candidate.ReviewItems, 1)
	require.Equal(t, "压缩后的事实需要核对", candidate.ReviewItems[0].Statement)
	require.Equal(t, candidate.CandidateID, candidate.ReviewItems[0].OriginCandidateID)
	require.Equal(t, "压缩出来的内容 [[source:existing]]", candidate.ReplacementMarkdown)
	require.NotContains(t, candidate.ReplacementMarkdown, "待核")
}

// TestSelectedRewriteCompressNeedsAnExplicitAdoptionConfirmation 断言压缩候选
// 的采纳必须带显式确认载荷。
//
// 缺载荷被拒；确认的动作、来源、确认人、目标范围对不上被拒；载荷齐全才写得
// 进去。
func TestSelectedRewriteCompressNeedsAnExplicitAdoptionConfirmation(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, workspace, _, base := compressFixture(t)
	candidate, err := service.Create(context.Background(), actor, "project", "chapter", "compress-key",
		compressRequest(base))
	require.NoError(t, err)
	require.Equal(t, "ready", candidate.Status)

	apply := func(t *testing.T, key string, confirmation *candidateadoption.AdoptionConfirmation) error {
		t.Helper()
		_, _, err := service.Apply(context.Background(), actor, "project", "chapter",
			candidate.CandidateID, key, ApplySelectedRewriteInput{
				ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1, Confirmation: confirmation})
		return err
	}

	// 缺确认载荷 ⇒ 拒。
	require.ErrorIs(t, apply(t, "apply-missing", nil), ErrRewriteConfirmationRequired)
	require.Empty(t, workspace.writes, "缺确认载荷不得写工作副本")

	// 确认的动作与候选对不上 ⇒ 拒。
	wrongAction := compressConfirmation(actor.UserID)
	wrongAction.WritingAction = writingaction.ActionRewrite
	require.ErrorIs(t, apply(t, "apply-wrong-action", wrongAction), ErrRewriteInvalidRequest)

	// 确认的来源与候选对不上 ⇒ 拒。
	wrongOrigin := compressConfirmation(actor.UserID)
	wrongOrigin.Origin = writingaction.OriginHuman
	require.ErrorIs(t, apply(t, "apply-wrong-origin", wrongOrigin), ErrRewriteInvalidRequest)

	// 确认人不是这次请求的认证身份 ⇒ 拒。
	otherActor := compressConfirmation("someone-else")
	require.ErrorIs(t, apply(t, "apply-other-actor", otherActor), ErrRewriteConfirmationForbidden)

	// 确认的目标范围与候选的选区对不上 ⇒ 拒。
	wrongScope := compressConfirmation(actor.UserID)
	wrongScope.Scope = &candidateadoption.AdoptionScope{StartUTF16: 0, EndUTF16: 3}
	require.ErrorIs(t, apply(t, "apply-wrong-scope", wrongScope), ErrRewriteInvalidRequest)

	// 确认的采纳目标不在端点能力内 ⇒ 拒。
	wrongTarget := compressConfirmation(actor.UserID)
	wrongTarget.Target = writingaction.TargetFormalVersionWhole
	require.ErrorIs(t, apply(t, "apply-wrong-target", wrongTarget), ErrRewriteInvalidRequest)

	// 载荷齐全 ⇒ 采纳成立：工作副本被写了，写进去的是候选正文，且只替换选区。
	require.NoError(t, apply(t, "apply-confirmed", compressConfirmation(actor.UserID)))
	require.Len(t, workspace.writes, 1)
	require.Equal(t, compressBefore+"压缩出来的内容 [[source:existing]]"+compressAfter,
		workspace.writes[0].BodyMarkdown)
}

// TestSelectedRewriteCompressRevocationBlocksTheFourTimings 覆盖撤权的四个
// 时机对压缩同样成立：模型调用之前、运行中、候选读取之前、采纳之前。
func TestSelectedRewriteCompressRevocationBlocksTheFourTimings(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	t.Run("模型调用之前", func(t *testing.T) {
		service, workspace, model, base := compressFixture(t)
		service.sources.(*inheritedRewriteSources).denied = true
		_, err := service.Create(context.Background(), actor, "project", "chapter",
			"revoked-before-model", compressRequest(base))
		require.ErrorIs(t, err, ErrRewriteSourceDenied)
		require.Zero(t, model.calls)
		require.Equal(t, int64(1), workspace.copy.WorkingCopyRevision)
	})
	t.Run("运行中", func(t *testing.T) {
		service, workspace, model, base := compressFixture(t)
		sources := service.sources.(*inheritedRewriteSources)
		model.onCall = func() { sources.denied = true }
		candidate, err := service.Create(context.Background(), actor, "project", "chapter",
			"revoked-during-run", compressRequest(base))
		require.NoError(t, err)
		require.Equal(t, 1, model.calls)
		require.Equal(t, "stale", candidate.Status)
		require.Equal(t, "source_access_denied", candidate.ErrorCode)
		require.Empty(t, candidate.ReplacementMarkdown)
		require.Equal(t, int64(1), workspace.copy.WorkingCopyRevision)
	})
	t.Run("候选读取之前", func(t *testing.T) {
		service, _, _, base := compressFixture(t)
		candidate, err := service.Create(context.Background(), actor, "project", "chapter",
			"revoked-before-read", compressRequest(base))
		require.NoError(t, err)
		require.Equal(t, "ready", candidate.Status)
		service.sources.(*inheritedRewriteSources).denied = true
		_, err = service.Get(context.Background(), actor, "project", candidate.CandidateID)
		require.ErrorIs(t, err, ErrRewriteSourceDenied)
	})
	t.Run("采纳之前", func(t *testing.T) {
		service, workspace, _, base := compressFixture(t)
		candidate, err := service.Create(context.Background(), actor, "project", "chapter",
			"revoked-before-apply", compressRequest(base))
		require.NoError(t, err)
		require.Equal(t, "ready", candidate.Status)
		service.sources.(*inheritedRewriteSources).denied = true
		_, _, err = service.Apply(context.Background(), actor, "project", "chapter", candidate.CandidateID,
			"apply-revoked", ApplySelectedRewriteInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
				Confirmation: compressConfirmation(actor.UserID)})
		require.ErrorIs(t, err, ErrRewriteSourceDenied)
		require.Empty(t, workspace.writes)
	})
}

// TestSelectedRewriteCompressFailureKeepsItsInputStatusAndReason 断言失败的
// 那次运行留下输入、状态与原因，重试用新键不写工作副本。
//
// 选段管道是同步的：它没有单独的「取消」动作，失败就是这条管道上「这次运行
// 没成」的形态。运行输入与原因都留在候选上，读回来还在。
func TestSelectedRewriteCompressFailureKeepsItsInputStatusAndReason(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, workspace, model, base := compressFixture(t)
	model.failing = true
	failed, err := service.Create(context.Background(), actor, "project", "chapter",
		"compress-failed", compressRequest(base))
	require.NoError(t, err)
	require.Equal(t, "failed", failed.Status)
	require.Equal(t, "rewrite_failed", failed.ErrorCode)
	require.Empty(t, failed.ReplacementMarkdown)
	require.Empty(t, workspace.writes, "失败的运行不得写工作副本")

	// 失败留下的输入与原因读回来还在。
	readBack, err := service.Get(context.Background(), actor, "project", failed.CandidateID)
	require.NoError(t, err)
	require.Equal(t, "failed", readBack.Status)
	require.Equal(t, "rewrite_failed", readBack.ErrorCode)
	require.Equal(t, compressSelected, readBack.Selection.SelectedText)
	require.Equal(t, []string{"existing"}, readBack.SourceIDs)

	// 重试用新幂等键：另起一次运行，工作副本仍然没被写。
	model.failing = false
	retried, err := service.Create(context.Background(), actor, "project", "chapter",
		"compress-retry", compressRequest(base))
	require.NoError(t, err)
	require.Equal(t, "ready", retried.Status)
	require.NotEqual(t, failed.CandidateID, retried.CandidateID)
	require.Empty(t, workspace.writes, "重试不得写工作副本")
}

// TestSelectedRewriteCompressCancellationKeepsItsStatusAndReason 断言被取消的
// 那次运行也留痕。
//
// 选段管道是同步的：请求被取消就是模型调用交回上下文的错误。留痕走的是
// context.WithoutCancel，所以取消之后状态与原因仍然写得进候选行。
func TestSelectedRewriteCompressCancellationKeepsItsStatusAndReason(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, workspace, model, base := compressFixture(t)
	model.cancelAware = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model.onCall = func() { cancel() }
	candidate, err := service.Create(ctx, actor, "project", "chapter", "compress-cancelled",
		compressRequest(base))
	require.NoError(t, err)
	require.Equal(t, "failed", candidate.Status)
	require.Equal(t, "rewrite_failed", candidate.ErrorCode)
	require.Empty(t, candidate.ReplacementMarkdown)
	require.Empty(t, workspace.writes, "被取消的运行不得写工作副本")
	require.Equal(t, compressSelected, candidate.Selection.SelectedText)
}

// TestSelectedRewriteCompressRecordsTheSelectionAndRefusesABrokenOne 断言
// 候选带着选区（以 UTF-16 偏移记录），坏的选区被拒。
//
// 两种坏的选区有两种失败：形状不合法（结束在开始之前）按请求错误拒；偏移越出
// 正文按「选区已变」拒——那是 409 stale_input，不是 400。后者是既有行为，本票
// 不改。
func TestSelectedRewriteCompressRecordsTheSelectionAndRefusesABrokenOne(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, _, model, base := compressFixture(t)
	candidate, err := service.Create(context.Background(), actor, "project", "chapter",
		"compress-key", compressRequest(base))
	require.NoError(t, err)
	require.Equal(t, compressSelected, candidate.Selection.SelectedText)
	require.Equal(t, len(utf16.Encode([]rune(compressBefore))), candidate.Selection.StartUTF16)
	require.Equal(t, len(utf16.Encode([]rune(compressBefore+compressSelected))), candidate.Selection.EndUTF16)

	// 形状不合法的选区 ⇒ 请求错误。
	malformed := compressRequest(base)
	malformed.Selection = RewriteSelection{StartUTF16: 10, EndUTF16: 5, SelectedText: compressSelected}
	_, err = service.Create(context.Background(), actor, "project", "chapter", "compress-malformed", malformed)
	require.ErrorIs(t, err, ErrRewriteInvalidRequest)

	// 偏移越出正文的选区 ⇒ 「选区已变」。
	outOfRange := compressRequest(base)
	outOfRange.Selection = RewriteSelection{StartUTF16: 0, EndUTF16: 9999, SelectedText: compressSelected}
	_, err = service.Create(context.Background(), actor, "project", "chapter", "compress-out-of-range", outOfRange)
	require.ErrorIs(t, err, ErrRewriteStale)
	require.Equal(t, 1, model.calls, "被拒的选区不得触达模型")
}

// TestSelectedRewriteCompressAndRewriteConflictOnTheSameIdempotencyKey 断言
// 同一个幂等键上的不同动作被判成冲突。
//
// 动作在请求摘要里 ⇒ 摘要天然不同 ⇒ 同键不同动作是冲突，不是重放。
func TestSelectedRewriteCompressAndRewriteConflictOnTheSameIdempotencyKey(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, _, model, base := compressFixture(t)
	_, err := service.Create(context.Background(), actor, "project", "chapter", "shared-key", compressRequest(base))
	require.NoError(t, err)
	require.Equal(t, 1, model.calls)

	_, err = service.Create(context.Background(), actor, "project", "chapter", "shared-key", actionRequest(base))
	require.ErrorIs(t, err, ErrRewriteKeyConflict)
	require.Equal(t, 1, model.calls, "冲突的请求不得再触达模型")
}

// TestSelectedRewriteCompressRetryWithANewKeyDoesNotWriteTwice 断言重试用新的
// 幂等键不重复写工作副本。
//
// 同一个键重放是同一次操作：工作副本不再被写。换一个键是另一次尝试：它被拒
// （工作副本已经动过了，候选的依据不再成立），工作副本仍然只被写过一次。
func TestSelectedRewriteCompressRetryWithANewKeyDoesNotWriteTwice(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, workspace, _, base := compressFixture(t)
	candidate, err := service.Create(context.Background(), actor, "project", "chapter",
		"compress-key", compressRequest(base))
	require.NoError(t, err)

	apply := func(t *testing.T, key string) (core.WorkingCopy, bool, error) {
		t.Helper()
		copy, replayed, err := service.Apply(context.Background(), actor, "project", "chapter",
			candidate.CandidateID, key, ApplySelectedRewriteInput{
				ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
				Confirmation: compressConfirmation(actor.UserID)})
		return copy, replayed, err
	}

	_, _, err = apply(t, "apply-first")
	require.NoError(t, err)
	require.Len(t, workspace.writes, 1)

	// 同一个键重放：同一次操作，不再写工作副本。
	_, replayed, err := apply(t, "apply-first")
	require.NoError(t, err)
	require.True(t, replayed)
	require.Len(t, workspace.writes, 1)

	// 换一个键重试：候选的依据已经不成立，被拒；工作副本仍然只写过一次。
	_, _, err = apply(t, "apply-second")
	require.ErrorIs(t, err, ErrRewriteStale)
	require.Len(t, workspace.writes, 1, "重试不得重复写工作副本")
}
