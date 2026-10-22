package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// actionPointer 造一个取址的动作取值，供需要区分「没给」与「给了零值」的用例使用。
func actionPointer[T any](value T) *T { return &value }

// actionRequest 给一份走动作路径的改写请求。
func actionRequest(base SelectedRewriteRequest) SelectedRewriteRequest {
	base.WritingAction = actionPointer(writingaction.ActionRewrite)
	base.OutputParams = &writingaction.OutputParams{
		Length: writingaction.LengthShort,
		Tone:   writingaction.ToneFormal,
	}
	base.Scope = actionPointer(writingaction.ScopeSection)
	return base
}

// TestSelectedRewriteActionPathRefusesIllegalDeclarations 覆盖动作封闭与产出
// 参数的闭合取值集。
//
// 缺动作、未知动作、整章范围、越界或缺失的产出参数、本端点未实现的动作——
// 五种都在**模型调用之前**被拒，且一条候选都不产生。
func TestSelectedRewriteActionPathRefusesIllegalDeclarations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*SelectedRewriteRequest)
		want   error
	}{
		{"缺动作", func(r *SelectedRewriteRequest) { r.WritingAction = nil }, ErrRewriteInvalidRequest},
		{"未知动作", func(r *SelectedRewriteRequest) {
			r.WritingAction = actionPointer(writingaction.Action("polish"))
		}, writingaction.ErrUnknownAction},
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
			r.OutputParams = &writingaction.OutputParams{
				Length: writingaction.LengthShort, Tone: writingaction.ToneFormal, Ratio: 50,
			}
		}, writingaction.ErrInvalidOutputParams},
		{"本端点未实现的动作", func(r *SelectedRewriteRequest) {
			r.WritingAction = actionPointer(writingaction.ActionGenerate)
			r.Scope = actionPointer(writingaction.ScopeSection)
		}, ErrRewriteInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, _, _, model, base := inheritedRewriteFixture(t)
			input := actionRequest(base)
			tc.mutate(&input)
			_, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
				"project", "chapter", "action-key", input)
			require.ErrorIs(t, err, tc.want)
			require.Zero(t, model.calls, "非法声明不得触达模型")
			var rows int64
			require.NoError(t, service.store.db.Model(&selectedRewriteRow{}).Count(&rows).Error)
			require.Zero(t, rows, "非法声明不得留下候选行")
		})
	}
}

// TestSelectedRewriteActionPathRecordsTheActionTheOriginAndTheRunMode 断言
// 动作路径产出的候选带着动作、来源与运行模式。
func TestSelectedRewriteActionPathRecordsTheActionTheOriginAndTheRunMode(t *testing.T) {
	service, _, _, _, base := inheritedRewriteFixture(t)
	input := actionRequest(base)
	input.Instruction = ""
	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "action-key", input)
	require.NoError(t, err)
	require.Equal(t, writingaction.ActionRewrite, candidate.WritingAction)
	require.Equal(t, writingaction.OriginGenerated, candidate.Origin)
	require.Equal(t, candidateadoption.RunModeMock, candidate.RunMode)
	require.Equal(t, candidateadoption.ValidityFresh, candidate.Validity)
}

// TestSelectedRewriteActionPathSeparatesTheActionInstructionFromTheSupplementaryNote
// 断言客户端的补充说明不参与动作判定，也不与动作指令混成一段。
func TestSelectedRewriteActionPathSeparatesTheActionInstructionFromTheSupplementaryNote(t *testing.T) {
	service, _, _, model, base := inheritedRewriteFixture(t)
	input := actionRequest(base)
	input.Instruction = "别动第二段"
	_, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "action-key", input)
	require.NoError(t, err)
	require.Equal(t, "别动第二段", model.prompt.SupplementaryNote)
	require.NotContains(t, model.prompt.Instruction, "别动第二段")
	require.Contains(t, model.prompt.Instruction, "只改写选中的片段")
}

// TestSelectedRewriteLegacyPathKeepsItsOwnSemantics 断言旧路径逐字不变：
// 客户端的自由文本说明原样作为指令，候选不带动作与来源。
func TestSelectedRewriteLegacyPathKeepsItsOwnSemantics(t *testing.T) {
	service, _, _, model, input := inheritedRewriteFixture(t)
	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "legacy-key", input)
	require.NoError(t, err)
	require.Equal(t, writingaction.Action(""), candidate.WritingAction)
	require.Equal(t, writingaction.Origin(""), candidate.Origin)
	require.Equal(t, "polish", model.prompt.Instruction)
	require.Equal(t, "", model.prompt.SupplementaryNote)
}

// TestSelectedRewriteLegacyRequestDigestIsUnchanged 断言旧形状请求的摘要值
// 逐字不变。
//
// 这个字面量是在基线提交 454d66a1 上、本票改动之前量出来的。请求摘要变了，
// 历史幂等键就从「重放」变成「冲突」——用户重试一次旧请求就会拿到一个假冲突。
func TestSelectedRewriteLegacyRequestDigestIsUnchanged(t *testing.T) {
	base := "chap-base"
	input := SelectedRewriteRequest{
		BaseChapterVersionID:        &base,
		ExpectedSpecRevision:        7,
		ExpectedWorkingCopyRevision: 3,
		Selection:                   RewriteSelection{StartUTF16: 10, EndUTF16: 20, SelectedText: "选中"},
		Instruction:                 "润色这一段",
		SourceIDs:                   []string{"src-a"},
	}
	digest, err := selectedRewriteRequestHash(input)
	require.NoError(t, err)
	require.Equal(t, "dd84021d56b6e4e024e7e795199815af844ce4eaed0e9c0533c91ab2f0695ea3", digest)
}

// rewriteModeModel 报告一个指定的运行模式。
type rewriteModeModel struct {
	inheritedRewriteModel
	mode string
}

func (m *rewriteModeModel) RunMode() string { return m.mode }

func (m *rewriteModeModel) RewriteSelected(_ context.Context, _ Actor, _ SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	m.calls++
	return SelectedRewriteOutput{ReplacementMarkdown: "revised [[source:existing]]", SourceIDs: []string{"existing"}, RunMode: m.mode}, nil
}

// TestSelectedRewriteRecordsEachRunMode 覆盖三档运行模式。
//
// 候选带的运行模式是这次运行怎么走到模型的：真模型、没有提供方、真实提供方
// 回了假模型。三档都记在候选上，mock 不冒充真实证据。
func TestSelectedRewriteRecordsEachRunMode(t *testing.T) {
	for _, mode := range []candidateadoption.RunMode{
		candidateadoption.RunModeMock,
		candidateadoption.RunModeRealAPIFakeModel,
		candidateadoption.RunModeReal,
	} {
		t.Run(string(mode), func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rewrite.db")), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			selected := "original [[source:existing]]"
			workspace := &inheritedRewriteWorkspace{copy: WorkingCopy{
				ProjectID: "project", ChapterID: "chapter", SpecRevision: 1, WorkingCopyRevision: 1,
				BodyMarkdown: selected + " context", SourceIDs: []string{"existing"},
			}}
			model := &rewriteModeModel{mode: string(mode)}
			service := NewSelectedRewriteService(db, workspace, nil, &inheritedRewriteSources{}, model)
			require.NoError(t, service.store.AutoMigrate(context.Background()))
			input := SelectedRewriteRequest{
				ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
				Selection:   RewriteSelection{EndUTF16: len(utf16.Encode([]rune(selected))), SelectedText: selected},
				Instruction: "polish", SourceIDs: []string{},
			}
			candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
				"project", "chapter", "mode-key", input)
			require.NoError(t, err)
			require.Equal(t, mode, candidate.RunMode)
			require.Equal(t, candidateadoption.ValidityFresh, candidate.Validity)
		})
	}
}

// rewriteHTTPRouter 装一个带改写端点的真实路由。
func rewriteHTTPRouter(t *testing.T, service SelectedRewriteApplication) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	deps := contextPorts(&applicationServiceStub{}, emptySourcesStub{})
	deps.WithSelectedRewrites(service)
	handler, err := NewHandler(deps)
	require.NoError(t, err)
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api/lingdoc"), Write: engine.Group("/api/lingdoc")})
	return engine
}

// rewriteHTTPContext 造一个带身份上下文的请求。
func rewriteHTTPContext(request *http.Request) *http.Request {
	ctx := context.WithValue(request.Context(), types.UserIDContextKey, "owner")
	return request.WithContext(context.WithValue(ctx, types.TenantIDContextKey, uint64(1)))
}

// TestSelectedRewriteActionPathOverHTTP 覆盖改写端点的 HTTP 接缝。
//
// 缺动作、未知动作、整章范围、越界的产出参数都在这里被拒；合法的动作化
// 请求返回带动作与来源的候选。
func TestSelectedRewriteActionPathOverHTTP(t *testing.T) {
	service, _, _, _, input := inheritedRewriteFixture(t)
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

	base := func() map[string]any {
		return map[string]any{
			"base_chapter_version_id":        nil,
			"expected_spec_revision":         1,
			"expected_working_copy_revision": 1,
			"selection":                      input.Selection,
			"instruction":                    "",
			"source_ids":                     []string{},
		}
	}

	for _, tc := range []struct {
		name string
		key  string
		body map[string]any
	}{
		{"缺动作", "http-missing", func() map[string]any {
			body := base()
			body["output_params"] = map[string]any{"length": "short", "tone": "formal"}
			return body
		}()},
		{"未知动作", "http-unknown", func() map[string]any {
			body := base()
			body["writing_action"] = "polish"
			body["output_params"] = map[string]any{"length": "short", "tone": "formal"}
			return body
		}()},
		{"整章范围", "http-chapter", func() map[string]any {
			body := base()
			body["writing_action"] = "rewrite"
			body["scope"] = "chapter"
			body["output_params"] = map[string]any{"length": "short", "tone": "formal"}
			return body
		}()},
		{"产出参数越界", "http-params", func() map[string]any {
			body := base()
			body["writing_action"] = "rewrite"
			body["output_params"] = map[string]any{"length": "huge", "tone": "formal"}
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

	accepted := base()
	accepted["writing_action"] = "rewrite"
	accepted["scope"] = "section"
	accepted["output_params"] = map[string]any{"length": "short", "tone": "formal"}
	response := post(t, "http-accepted", accepted)
	require.Equal(t, http.StatusCreated, response.Code)
	var envelope struct {
		Data struct {
			CandidateID   string `json:"candidate_id"`
			WritingAction string `json:"writing_action"`
			Origin        string `json:"origin"`
			RunMode       string `json:"run_mode"`
			Validity      string `json:"validity"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.NotEmpty(t, envelope.Data.CandidateID)
	require.Equal(t, "rewrite", envelope.Data.WritingAction)
	require.Equal(t, "ai_generated", envelope.Data.Origin)
	require.Equal(t, "mock", envelope.Data.RunMode)
	require.Equal(t, string(candidateadoption.ValidityFresh), envelope.Data.Validity)
}

// TestWritingActionsEndpointServesTheVocabulary 覆盖动作表端点。
//
// 前端不写第二份动作枚举：动作、允许范围、可用产出参数、产出形状与允许的
// 采纳目标都从服务端取。
func TestWritingActionsEndpointServesTheVocabulary(t *testing.T) {
	service, _, _, _, _ := inheritedRewriteFixture(t)
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
	var rewrite *writingaction.Description
	for index := range envelope.Data {
		if envelope.Data[index].Action == writingaction.ActionRewrite {
			rewrite = &envelope.Data[index]
		}
	}
	require.NotNil(t, rewrite, "动作表里必须有改写")
	require.Equal(t, []writingaction.Scope{writingaction.ScopeSection}, rewrite.Scopes)
	require.Equal(t, writingaction.ProduceProse, rewrite.Produce)
	require.Len(t, rewrite.Targets, 3)
	require.Len(t, rewrite.OutputParams, 2)
}
