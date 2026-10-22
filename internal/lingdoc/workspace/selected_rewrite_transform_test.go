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

// 转写夹具的正文三段。
//
// 转写与其它选段动作共用选区与上下文，差别在**产出参数**：转写要长度与一个子
// 参数（目标语言或读者层次）。夹具因此保留前文与后文，让「转写只替换选区」这件
// 事有东西可断言。
const (
	transformBefore   = "前文第一段。"
	transformSelected = "第二段被选中 [[source:existing]]"
	transformAfter    = "第三段。"
)

// transformRewriteModel 回一段带来源标记的转写正文。
type transformRewriteModel struct {
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

func (*transformRewriteModel) RunMode() string { return "mock" }

func (m *transformRewriteModel) RewriteSelected(ctx context.Context, _ Actor, p SelectedRewritePrompt) (SelectedRewriteOutput, error) {
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
	return SelectedRewriteOutput{ReplacementMarkdown: "转写出来的内容 [[source:existing]]",
		SourceIDs: []string{"existing"}, ReviewItems: m.reviewItems, RunMode: "mock"}, nil
}

// transformWorkspace 记下工作副本的写入。
//
// 它实现 ApplyRewriteResult，让「确认载荷齐全才写得进去」这条断言看得见：
// 写入的次数与写入的正文都在这里。
type transformWorkspace struct {
	inheritedRewriteWorkspace
	writes []core.SaveWorkingCopyInput
}

func (w *transformWorkspace) ApplyRewriteResult(_ context.Context, _ Actor, _, _, _ string,
	input core.SaveWorkingCopyInput, _ []core.ReviewItem) (WorkingCopyMutationResult, error) {
	w.writes = append(w.writes, input)
	w.copy.BodyMarkdown = input.BodyMarkdown
	w.copy.WorkingCopyRevision++
	return WorkingCopyMutationResult{WorkingCopy: w.copy}, nil
}

// transformFixture 造一个转写夹具。
//
// 它自带夹具，不复用压缩或扩展的夹具：每张动作票的切片各自闭环（开发计划
// D20），下张票不依赖这张票的测试文件。
func transformFixture(t *testing.T) (*SelectedRewriteService, *transformWorkspace, *transformRewriteModel, SelectedRewriteRequest) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rewrite.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	body := transformBefore + transformSelected + transformAfter
	workspace := &transformWorkspace{inheritedRewriteWorkspace: inheritedRewriteWorkspace{
		copy: WorkingCopy{ProjectID: "project", ChapterID: "chapter", SpecRevision: 1,
			WorkingCopyRevision: 1, BodyMarkdown: body, SourceIDs: []string{"existing"}}}}
	sources := &inheritedRewriteSources{}
	model := &transformRewriteModel{}
	service := NewSelectedRewriteService(db, workspace, sources, model)
	require.NoError(t, service.store.AutoMigrate(context.Background()))
	input := SelectedRewriteRequest{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
		Selection: RewriteSelection{
			StartUTF16:   len(utf16.Encode([]rune(transformBefore))),
			EndUTF16:     len(utf16.Encode([]rune(transformBefore + transformSelected))),
			SelectedText: transformSelected},
		Instruction: "polish", SourceIDs: []string{}}
	return service, workspace, model, input
}

// transformRequest 给一份走动作路径的转写请求。
//
// 默认是翻译子模式：翻译带目标语言，解释带读者层次，两者互斥。
func transformRequest(base SelectedRewriteRequest) SelectedRewriteRequest {
	base.WritingAction = actionPointer(writingaction.ActionTransform)
	base.OutputParams = &writingaction.OutputParams{
		Length: writingaction.LengthShort,
		Transform: &writingaction.TransformParams{Mode: writingaction.TransformTranslate,
			TargetLanguage: actionPointer(writingaction.LangZH)},
	}
	base.Scope = actionPointer(writingaction.ScopeSection)
	return base
}

// transformExplainRequest 给一份走解释子模式的转写请求。
func transformExplainRequest(base SelectedRewriteRequest) SelectedRewriteRequest {
	base.WritingAction = actionPointer(writingaction.ActionTransform)
	base.OutputParams = &writingaction.OutputParams{
		Length: writingaction.LengthShort,
		Transform: &writingaction.TransformParams{Mode: writingaction.TransformExplain,
			Audience: actionPointer(writingaction.AudienceNovice)},
	}
	base.Scope = actionPointer(writingaction.ScopeSection)
	return base
}

// transformConfirmation 造一份「采纳到工作副本选段」的确认载荷。
func transformConfirmation(actor string) *candidateadoption.AdoptionConfirmation {
	return &candidateadoption.AdoptionConfirmation{
		WritingAction: writingaction.ActionTransform,
		Target:        writingaction.TargetWorkingCopyPartial,
		Scope: &candidateadoption.AdoptionScope{
			StartUTF16: len(utf16.Encode([]rune(transformBefore))),
			EndUTF16:   len(utf16.Encode([]rune(transformBefore + transformSelected))),
		},
		ConfirmedAt: time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC),
		ConfirmedBy: actor,
		Origin:      writingaction.OriginGenerated,
	}
}

// TestSelectedRewriteTransformRunsAndRecordsTheActionOriginAndRunMode 断言转写
// 产出的候选带着动作、来源与运行模式。
func TestSelectedRewriteTransformRunsAndRecordsTheActionOriginAndRunMode(t *testing.T) {
	service, _, model, base := transformFixture(t)
	input := transformRequest(base)
	input.Instruction = ""
	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "transform-key", input)
	require.NoError(t, err)
	require.Equal(t, writingaction.ActionTransform, candidate.WritingAction)
	require.Equal(t, writingaction.OriginGenerated, candidate.Origin)
	require.Equal(t, candidateadoption.RunModeMock, candidate.RunMode)
	require.Equal(t, "ready", candidate.Status)
	require.Equal(t, 1, model.calls)
}

// TestSelectedRewriteTransformRunsBothSubModes 断言两个子模式都能在这条管道上
// 跑通。
//
// 子模式之间的差别只在子参数：翻译带目标语言，解释带读者层次。执行方不解释它们
// ——它把产出参数原样交给执行计划。
func TestSelectedRewriteTransformRunsBothSubModes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request func(SelectedRewriteRequest) SelectedRewriteRequest
	}{
		{"翻译", transformRequest},
		{"解释", transformExplainRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _, model, base := transformFixture(t)
			candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
				"project", "chapter", "transform-key", tc.request(base))
			require.NoError(t, err)
			require.Equal(t, writingaction.ActionTransform, candidate.WritingAction)
			require.Equal(t, "ready", candidate.Status)
			require.Equal(t, 1, model.calls)
		})
	}
}

// TestSelectedRewriteTransformRendersTheLengthAndTheSubModeIntoTheModelRequest
// 断言转写的产出参数（长度与子参数）在模型请求里看得见，而且语气与比例不在其中。
//
// 产出参数由词汇包渲染进动作指令：执行方不解释产出参数。翻译看得见目标语言，
// 解释看得见读者层次。
func TestSelectedRewriteTransformRendersTheLengthAndTheSubModeIntoTheModelRequest(t *testing.T) {
	t.Run("翻译", func(t *testing.T) {
		service, _, model, base := transformFixture(t)
		_, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
			"project", "chapter", "transform-key", transformRequest(base))
		require.NoError(t, err)
		require.Contains(t, model.prompt.Instruction, "按指定的转写模式改写目标内容")
		require.Contains(t, model.prompt.Instruction, "产出长度")
		require.Contains(t, model.prompt.Instruction, "转写模式：译成 zh")
		require.NotContains(t, model.prompt.Instruction, "产出语气",
			"转写不用语气，模型请求里不该出现语气")
		require.NotContains(t, model.prompt.Instruction, "目标比例",
			"转写不用比例，模型请求里不该出现比例")
	})
	t.Run("解释", func(t *testing.T) {
		service, _, model, base := transformFixture(t)
		_, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
			"project", "chapter", "transform-key", transformExplainRequest(base))
		require.NoError(t, err)
		require.Contains(t, model.prompt.Instruction, "按指定的转写模式改写目标内容")
		require.Contains(t, model.prompt.Instruction, "转写模式：面向 novice 解释")
	})
}

// TestSelectedRewriteTransformRefusesIllegalDeclarations 覆盖转写的声明闭合：
// 整章范围、缺子参数、混用子参数、未知子模式、动作用不上的参数、缺长度。
//
// 每一种都在**模型调用之前**被拒，且一条候选都不产生。
func TestSelectedRewriteTransformRefusesIllegalDeclarations(t *testing.T) {
	unknownMode := writingaction.TransformMode("summarize")
	unknownLanguage := writingaction.TargetLanguage("klingon")
	unknownAudience := writingaction.Audience("aliens")
	for _, tc := range []struct {
		name   string
		mutate func(*SelectedRewriteRequest)
		want   error
	}{
		{"整章范围", func(r *SelectedRewriteRequest) {
			// 这条管道只写选段：整章转写归生成管道。
			r.Scope = actionPointer(writingaction.ScopeChapter)
		}, writingaction.ErrScopeNotAllowed},
		{"翻译没给目标语言", func(r *SelectedRewriteRequest) {
			r.OutputParams.Transform = &writingaction.TransformParams{Mode: writingaction.TransformTranslate}
		}, writingaction.ErrInvalidOutputParams},
		{"解释没给读者层次", func(r *SelectedRewriteRequest) {
			r.OutputParams.Transform = &writingaction.TransformParams{Mode: writingaction.TransformExplain}
		}, writingaction.ErrInvalidOutputParams},
		{"两个子参数混用", func(r *SelectedRewriteRequest) {
			language := writingaction.LangEN
			audience := writingaction.AudiencePeer
			r.OutputParams.Transform = &writingaction.TransformParams{Mode: writingaction.TransformTranslate,
				TargetLanguage: &language, Audience: &audience}
		}, writingaction.ErrInvalidOutputParams},
		{"子模式不认识", func(r *SelectedRewriteRequest) {
			r.OutputParams.Transform = &writingaction.TransformParams{Mode: unknownMode}
		}, writingaction.ErrInvalidOutputParams},
		{"目标语言不在枚举里", func(r *SelectedRewriteRequest) {
			r.OutputParams.Transform = &writingaction.TransformParams{Mode: writingaction.TransformTranslate,
				TargetLanguage: &unknownLanguage}
		}, writingaction.ErrInvalidOutputParams},
		{"读者层次不在枚举里", func(r *SelectedRewriteRequest) {
			r.OutputParams.Transform = &writingaction.TransformParams{Mode: writingaction.TransformExplain,
				Audience: &unknownAudience}
		}, writingaction.ErrInvalidOutputParams},
		{"转写没给子参数", func(r *SelectedRewriteRequest) {
			r.OutputParams.Transform = nil
		}, writingaction.ErrInvalidOutputParams},
		{"长度没给", func(r *SelectedRewriteRequest) {
			r.OutputParams.Length = ""
		}, writingaction.ErrInvalidOutputParams},
		{"动作用不上的语气被带上", func(r *SelectedRewriteRequest) {
			r.OutputParams.Tone = writingaction.ToneFormal
		}, writingaction.ErrInvalidOutputParams},
		{"动作用不上的比例被带上", func(r *SelectedRewriteRequest) {
			r.OutputParams.Ratio = 40
		}, writingaction.ErrInvalidOutputParams},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _, model, base := transformFixture(t)
			input := transformRequest(base)
			tc.mutate(&input)
			_, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
				"project", "chapter", "transform-key", input)
			require.ErrorIs(t, err, tc.want)
			require.Zero(t, model.calls, "非法声明不得触达模型")
			var rows int64
			require.NoError(t, service.store.db.Model(&selectedRewriteRow{}).Count(&rows).Error)
			require.Zero(t, rows, "非法声明不得留下候选行")
		})
	}
}

// TestWritingActionsEndpointServesTransform 覆盖动作表端点上的转写。
//
// 转写与其它动作一样从服务端取：允许范围、可用产出参数、产出形状与允许的采纳
// 目标都由词汇包给出，前端不写第二份动作清单。子参数带**适用子模式**：目标语言
// 只在翻译下适用，读者层次只在解释下适用。
func TestWritingActionsEndpointServesTransform(t *testing.T) {
	service, _, _, _ := transformFixture(t)
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
		if envelope.Data[index].Action == writingaction.ActionTransform {
			described = &envelope.Data[index]
		}
	}
	require.NotNil(t, described, "动作表里必须有转写")
	require.Equal(t, []writingaction.Scope{writingaction.ScopeChapter, writingaction.ScopeSection},
		described.Scopes)
	require.Equal(t, writingaction.ProduceProse, described.Produce)
	require.Len(t, described.Targets, 3)
	require.Len(t, described.OutputParams, 4)

	var length, mode, language, audience *writingaction.ParamOption
	for index := range described.OutputParams {
		switch described.OutputParams[index].Name {
		case "length":
			length = &described.OutputParams[index]
		case "transform_mode":
			mode = &described.OutputParams[index]
		case "target_language":
			language = &described.OutputParams[index]
		case "audience":
			audience = &described.OutputParams[index]
		}
	}
	require.NotNil(t, length, "转写的产出参数里有长度")
	require.Equal(t, []string{"short", "medium", "long"}, length.Values)
	require.NotNil(t, mode, "转写的产出参数里有子模式")
	require.Equal(t, []string{"translate", "explain"}, mode.Values)
	require.NotNil(t, language, "转写的产出参数里有目标语言")
	require.Equal(t, []string{"zh", "en", "ja", "de", "fr"}, language.Values)
	require.Equal(t, string(writingaction.TransformTranslate), language.AppliesTo)
	require.NotNil(t, audience, "转写的产出参数里有读者层次")
	require.Equal(t, []string{"peer", "reviewer", "novice"}, audience.Values)
	require.Equal(t, string(writingaction.TransformExplain), audience.AppliesTo)
}

// TestSelectedRewriteTransformOverHTTP 覆盖转写端点的 HTTP 接缝。
//
// 缺动作、未知动作、整章范围、缺子参数、混用子参数、越界的长度都在这里被拒；
// 合法的转写请求返回带动作、来源与运行模式的候选。
func TestSelectedRewriteTransformOverHTTP(t *testing.T) {
	service, _, _, base := transformFixture(t)
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
		{"缺动作", "http-transform-missing", func() map[string]any {
			body := baseBody()
			body["output_params"] = map[string]any{"length": "short",
				"transform": map[string]any{"mode": "translate", "target_language": "zh"}}
			return body
		}()},
		{"未知动作", "http-transform-unknown", func() map[string]any {
			body := baseBody()
			body["writing_action"] = "polish"
			body["output_params"] = map[string]any{"length": "short",
				"transform": map[string]any{"mode": "translate", "target_language": "zh"}}
			return body
		}()},
		{"整章范围", "http-transform-chapter", func() map[string]any {
			body := baseBody()
			body["writing_action"] = "transform"
			body["scope"] = "chapter"
			body["output_params"] = map[string]any{"length": "short",
				"transform": map[string]any{"mode": "translate", "target_language": "zh"}}
			return body
		}()},
		{"翻译缺目标语言", "http-transform-missing-language", func() map[string]any {
			body := baseBody()
			body["writing_action"] = "transform"
			body["scope"] = "section"
			body["output_params"] = map[string]any{"length": "short",
				"transform": map[string]any{"mode": "translate"}}
			return body
		}()},
		{"解释缺读者层次", "http-transform-missing-audience", func() map[string]any {
			body := baseBody()
			body["writing_action"] = "transform"
			body["scope"] = "section"
			body["output_params"] = map[string]any{"length": "short",
				"transform": map[string]any{"mode": "explain"}}
			return body
		}()},
		{"两个子参数混用", "http-transform-mixed", func() map[string]any {
			body := baseBody()
			body["writing_action"] = "transform"
			body["scope"] = "section"
			body["output_params"] = map[string]any{"length": "short",
				"transform": map[string]any{"mode": "translate", "target_language": "zh", "audience": "peer"}}
			return body
		}()},
		{"长度越界", "http-transform-length", func() map[string]any {
			body := baseBody()
			body["writing_action"] = "transform"
			body["scope"] = "section"
			body["output_params"] = map[string]any{"length": "huge",
				"transform": map[string]any{"mode": "translate", "target_language": "zh"}}
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
	accepted["writing_action"] = "transform"
	accepted["scope"] = "section"
	accepted["output_params"] = map[string]any{"length": "short",
		"transform": map[string]any{"mode": "translate", "target_language": "zh"}}
	response := post(t, "http-transform-accepted", accepted)
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
	require.Equal(t, "transform", envelope.Data.WritingAction)
	require.Equal(t, "ai_generated", envelope.Data.Origin)
	require.Equal(t, "mock", envelope.Data.RunMode)
	require.Equal(t, "ready", envelope.Data.Status)
}

// TestSelectedRewriteTransformCandidateShowsItsContextAndSeparatesReviewItems
// 断言候选带着项目、章节、上下文修订号与授权来源，并且待核事项与正文分列。
//
// 这两行是矩阵里的 A6 与 A7：上下文要看得见，三类内容不能混成一段。
func TestSelectedRewriteTransformCandidateShowsItsContextAndSeparatesReviewItems(t *testing.T) {
	service, _, model, base := transformFixture(t)
	model.reviewItems = []string{"转写后的事实需要核对"}
	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "transform-key", transformRequest(base))
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
	require.Equal(t, "转写后的事实需要核对", candidate.ReviewItems[0].Statement)
	require.Equal(t, candidate.CandidateID, candidate.ReviewItems[0].OriginCandidateID)
	require.Equal(t, "转写出来的内容 [[source:existing]]", candidate.ReplacementMarkdown)
	require.NotContains(t, candidate.ReplacementMarkdown, "待核")
}

// TestSelectedRewriteTransformNeedsAnExplicitAdoptionConfirmation 断言转写候选
// 的采纳必须带显式确认载荷。
//
// 缺载荷被拒；确认的动作、来源、确认人、目标范围对不上被拒；载荷齐全才写得
// 进去。
func TestSelectedRewriteTransformNeedsAnExplicitAdoptionConfirmation(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, workspace, _, base := transformFixture(t)
	candidate, err := service.Create(context.Background(), actor, "project", "chapter", "transform-key",
		transformRequest(base))
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
	wrongAction := transformConfirmation(actor.UserID)
	wrongAction.WritingAction = writingaction.ActionRewrite
	require.ErrorIs(t, apply(t, "apply-wrong-action", wrongAction), ErrRewriteInvalidRequest)

	// 确认的来源与候选对不上 ⇒ 拒。
	wrongOrigin := transformConfirmation(actor.UserID)
	wrongOrigin.Origin = writingaction.OriginHuman
	require.ErrorIs(t, apply(t, "apply-wrong-origin", wrongOrigin), ErrRewriteInvalidRequest)

	// 确认人不是这次请求的认证身份 ⇒ 拒。
	otherActor := transformConfirmation("someone-else")
	require.ErrorIs(t, apply(t, "apply-other-actor", otherActor), ErrRewriteConfirmationForbidden)

	// 确认的目标范围与候选的选区对不上 ⇒ 拒。
	wrongScope := transformConfirmation(actor.UserID)
	wrongScope.Scope = &candidateadoption.AdoptionScope{StartUTF16: 0, EndUTF16: 3}
	require.ErrorIs(t, apply(t, "apply-wrong-scope", wrongScope), ErrRewriteInvalidRequest)

	// 确认的采纳目标不在端点能力内 ⇒ 拒。
	wrongTarget := transformConfirmation(actor.UserID)
	wrongTarget.Target = writingaction.TargetFormalVersionWhole
	require.ErrorIs(t, apply(t, "apply-wrong-target", wrongTarget), ErrRewriteInvalidRequest)

	// 载荷齐全 ⇒ 采纳成立：工作副本被写了，写进去的是候选正文，且只替换选区。
	require.NoError(t, apply(t, "apply-confirmed", transformConfirmation(actor.UserID)))
	require.Len(t, workspace.writes, 1)
	require.Equal(t, transformBefore+"转写出来的内容 [[source:existing]]"+transformAfter,
		workspace.writes[0].BodyMarkdown)
}

// TestSelectedRewriteTransformRevocationBlocksTheFourTimings 覆盖撤权的四个
// 时机对转写同样成立：模型调用之前、运行中、候选读取之前、采纳之前。
func TestSelectedRewriteTransformRevocationBlocksTheFourTimings(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	t.Run("模型调用之前", func(t *testing.T) {
		service, workspace, model, base := transformFixture(t)
		service.sources.(*inheritedRewriteSources).denied = true
		_, err := service.Create(context.Background(), actor, "project", "chapter",
			"revoked-before-model", transformRequest(base))
		require.ErrorIs(t, err, ErrRewriteSourceDenied)
		require.Zero(t, model.calls)
		require.Equal(t, int64(1), workspace.copy.WorkingCopyRevision)
	})
	t.Run("运行中", func(t *testing.T) {
		service, workspace, model, base := transformFixture(t)
		sources := service.sources.(*inheritedRewriteSources)
		model.onCall = func() { sources.denied = true }
		candidate, err := service.Create(context.Background(), actor, "project", "chapter",
			"revoked-during-run", transformRequest(base))
		require.NoError(t, err)
		require.Equal(t, 1, model.calls)
		require.Equal(t, "stale", candidate.Status)
		require.Equal(t, "source_access_denied", candidate.ErrorCode)
		require.Empty(t, candidate.ReplacementMarkdown)
		require.Equal(t, int64(1), workspace.copy.WorkingCopyRevision)
	})
	t.Run("候选读取之前", func(t *testing.T) {
		service, _, _, base := transformFixture(t)
		candidate, err := service.Create(context.Background(), actor, "project", "chapter",
			"revoked-before-read", transformRequest(base))
		require.NoError(t, err)
		require.Equal(t, "ready", candidate.Status)
		service.sources.(*inheritedRewriteSources).denied = true
		_, err = service.Get(context.Background(), actor, "project", candidate.CandidateID)
		require.ErrorIs(t, err, ErrRewriteSourceDenied)
	})
	t.Run("采纳之前", func(t *testing.T) {
		service, workspace, _, base := transformFixture(t)
		candidate, err := service.Create(context.Background(), actor, "project", "chapter",
			"revoked-before-apply", transformRequest(base))
		require.NoError(t, err)
		require.Equal(t, "ready", candidate.Status)
		service.sources.(*inheritedRewriteSources).denied = true
		_, _, err = service.Apply(context.Background(), actor, "project", "chapter", candidate.CandidateID,
			"apply-revoked", ApplySelectedRewriteInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
				Confirmation: transformConfirmation(actor.UserID)})
		require.ErrorIs(t, err, ErrRewriteSourceDenied)
		require.Empty(t, workspace.writes)
	})
}

// TestSelectedRewriteTransformFailureKeepsItsInputStatusAndReason 断言失败的
// 那次运行留下输入、状态与原因，重试用新键不写工作副本。
//
// 选段管道是同步的：它没有单独的「取消」动作，失败就是这条管道上「这次运行
// 没成」的形态。运行输入与原因都留在候选上，读回来还在。
func TestSelectedRewriteTransformFailureKeepsItsInputStatusAndReason(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, workspace, model, base := transformFixture(t)
	model.failing = true
	failed, err := service.Create(context.Background(), actor, "project", "chapter",
		"transform-failed", transformRequest(base))
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
	require.Equal(t, transformSelected, readBack.Selection.SelectedText)
	require.Equal(t, []string{"existing"}, readBack.SourceIDs)

	// 重试用新幂等键：另起一次运行，工作副本仍然没被写。
	model.failing = false
	retried, err := service.Create(context.Background(), actor, "project", "chapter",
		"transform-retry", transformRequest(base))
	require.NoError(t, err)
	require.Equal(t, "ready", retried.Status)
	require.NotEqual(t, failed.CandidateID, retried.CandidateID)
	require.Empty(t, workspace.writes, "重试不得写工作副本")
}

// TestSelectedRewriteTransformCancellationKeepsItsStatusAndReason 断言被取消的
// 那次运行也留痕。
//
// 选段管道是同步的：请求被取消就是模型调用交回上下文的错误。留痕走的是
// context.WithoutCancel，所以取消之后状态与原因仍然写得进候选行。
func TestSelectedRewriteTransformCancellationKeepsItsStatusAndReason(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, workspace, model, base := transformFixture(t)
	model.cancelAware = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model.onCall = func() { cancel() }
	candidate, err := service.Create(ctx, actor, "project", "chapter", "transform-cancelled",
		transformRequest(base))
	require.NoError(t, err)
	require.Equal(t, "failed", candidate.Status)
	require.Equal(t, "rewrite_failed", candidate.ErrorCode)
	require.Empty(t, candidate.ReplacementMarkdown)
	require.Empty(t, workspace.writes, "被取消的运行不得写工作副本")
	require.Equal(t, transformSelected, candidate.Selection.SelectedText)
}

// TestSelectedRewriteTransformRecordsTheSelectionAndRefusesABrokenOne 断言
// 候选带着选区（以 UTF-16 偏移记录），坏的选区被拒。
//
// 两种坏的选区有两种失败：形状不合法（结束在开始之前）按请求错误拒；偏移越出
// 正文按「选区已变」拒——那是 409 stale_input，不是 400。后者是既有行为，本票
// 不改。
func TestSelectedRewriteTransformRecordsTheSelectionAndRefusesABrokenOne(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, _, model, base := transformFixture(t)
	candidate, err := service.Create(context.Background(), actor, "project", "chapter",
		"transform-key", transformRequest(base))
	require.NoError(t, err)
	require.Equal(t, transformSelected, candidate.Selection.SelectedText)
	require.Equal(t, len(utf16.Encode([]rune(transformBefore))), candidate.Selection.StartUTF16)
	require.Equal(t, len(utf16.Encode([]rune(transformBefore+transformSelected))), candidate.Selection.EndUTF16)

	// 形状不合法的选区 ⇒ 请求错误。
	malformed := transformRequest(base)
	malformed.Selection = RewriteSelection{StartUTF16: 10, EndUTF16: 5, SelectedText: transformSelected}
	_, err = service.Create(context.Background(), actor, "project", "chapter", "transform-malformed", malformed)
	require.ErrorIs(t, err, ErrRewriteInvalidRequest)

	// 偏移越出正文的选区 ⇒ 「选区已变」。
	outOfRange := transformRequest(base)
	outOfRange.Selection = RewriteSelection{StartUTF16: 0, EndUTF16: 9999, SelectedText: transformSelected}
	_, err = service.Create(context.Background(), actor, "project", "chapter", "transform-out-of-range", outOfRange)
	require.ErrorIs(t, err, ErrRewriteStale)
	require.Equal(t, 1, model.calls, "被拒的选区不得触达模型")
}

// TestSelectedRewriteTransformAndRewriteConflictOnTheSameIdempotencyKey 断言
// 同一个幂等键上的不同动作被判成冲突。
//
// 动作在请求摘要里 ⇒ 摘要天然不同 ⇒ 同键不同动作是冲突，不是重放。
func TestSelectedRewriteTransformAndRewriteConflictOnTheSameIdempotencyKey(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, _, model, base := transformFixture(t)
	_, err := service.Create(context.Background(), actor, "project", "chapter", "shared-key", transformRequest(base))
	require.NoError(t, err)
	require.Equal(t, 1, model.calls)

	_, err = service.Create(context.Background(), actor, "project", "chapter", "shared-key", actionRequest(base))
	require.ErrorIs(t, err, ErrRewriteKeyConflict)
	require.Equal(t, 1, model.calls, "冲突的请求不得再触达模型")
}

// TestSelectedRewriteTransformRetryWithANewKeyDoesNotWriteTwice 断言重试用新的
// 幂等键不重复写工作副本。
//
// 同一个键重放是同一次操作：工作副本不再被写。换一个键是另一次尝试：它被拒
// （工作副本已经动过了，候选的依据不再成立），工作副本仍然只被写过一次。
func TestSelectedRewriteTransformRetryWithANewKeyDoesNotWriteTwice(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, workspace, _, base := transformFixture(t)
	candidate, err := service.Create(context.Background(), actor, "project", "chapter",
		"transform-key", transformRequest(base))
	require.NoError(t, err)

	apply := func(t *testing.T, key string) (core.WorkingCopy, bool, error) {
		t.Helper()
		copy, replayed, err := service.Apply(context.Background(), actor, "project", "chapter",
			candidate.CandidateID, key, ApplySelectedRewriteInput{
				ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
				Confirmation: transformConfirmation(actor.UserID)})
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
