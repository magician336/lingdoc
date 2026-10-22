package generation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// outlineSkeleton 是提纲这一次执行的产出：章节骨架，不是展开的正文。
//
// 它带一个来源标记：候选正文里的来源标记必须与候选声明的来源一一对应，否则
// 候选被拒。
const outlineSkeleton = "## 1. 研究背景\n- 背景要点 [[source:source-asset-1]]\n" +
	"## 2. 研究方法\n- 方法要点\n"

// outlineFixture 造一个提纲动作用的生成夹具。
//
// 它自带夹具，不复用生成动作的夹具：提纲有自己的产出形状（章节骨架）与自己的
// 采纳许可位（只有「工作副本·整段」），复用别的动作的夹具会让这张票的切片依赖
// 上一张票的测试文件（开发计划 D20）。
//
// 章节正文是「用户已经写好的正文」：提纲采纳只写工作副本，正式版本里的这份
// 正文不能被提纲吃掉——这正是本票要守住的那件事。
func outlineFixture(t *testing.T) (*Service, *testRepository) {
	t.Helper()
	actor := Actor{TenantID: 1, UserID: "user-1"}
	chapterVersion := "chapter-v1"
	basis := candidateadoption.Basis{SpecRevision: 3, ChapterVersionID: &chapterVersion,
		AssetVersions: []candidateadoption.AssetVersion{{AssetID: "asset-1", AssetRevision: 1}}}
	input := Input{Actor: actor, Basis: basis,
		Workspace: candidateadoption.GenerationContext{ProjectID: "project-1", ChapterID: "chapter-1",
			SpecRevision: 3, ChapterVersionID: &chapterVersion, Basis: basis,
			Chapter: candidateadoption.Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "section-1",
				Title: "研究问题", BodyMarkdown: "用户已经写好的正文"}},
		Sources: []Source{{ID: "source-asset-1", ProjectID: "project-1", AssetID: "asset-1", AssetRevision: 1,
			Status: "available"}}}
	repo := &testRepository{input: input, run: Run{ID: "run-1", ProjectID: "project-1",
		ChapterID: "chapter-1", Status: StatusQueued}}
	svc := NewService(testAuthorizer{}, testInputs{input: input}, testSources{}, testCurrentness{current: true},
		testModel{draft: Draft{BodyMarkdown: outlineSkeleton, Sources: []Source{{ID: "source-asset-1"}}}},
		repo, &testEnqueuer{})
	svc.NewID = func() string { return "candidate-outline-1" }
	return svc, repo
}

// outlineRequest 造一份按提纲动作发起的生成请求。
//
// 提纲**没有产出参数**：请求里不带 output_params。带了就是「用不上的参数必须
// 没给」——服务端按这个规则拒。
func outlineRequest(scope writingaction.Scope) Request {
	action := writingaction.ActionOutline
	return Request{
		ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "补充说明",
		ExpectedSpecRevision: 3, ExpectedChapterVersionID: stringPtr("chapter-v1"),
		WritingAction: &action, Scope: &scope,
	}
}

// TestGenerationOutlineRunsOnTheGenerationPipeline 覆盖「提纲在生成管道上跑得
// 起来」。
//
// 提纲只在生成管道里做得出（它只允许整章，选段管道不做它）。动作路径下写给
// 模型的指令来自执行计划，客户端的补充说明另放一个字段。
func TestGenerationOutlineRunsOnTheGenerationPipeline(t *testing.T) {
	svc, repo := outlineFixture(t)
	request := outlineRequest(writingaction.ScopeChapter)

	_, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "outline-scope-chapter", request)
	require.NoError(t, err, "提纲在生成管道上必须跑得起来")
	require.Equal(t, 1, repo.createRunCalls, "合法声明要产生一条运行")

	plan, err := writingaction.PlanFor(writingaction.ActionOutline, writingaction.ScopeChapter,
		writingaction.OutputParams{})
	require.NoError(t, err)
	instruction := repo.input.Instruction
	require.Contains(t, instruction, string(plan.Instruction),
		"写给模型的指令必须带着词汇包声明的动作指令")
	require.NotEqual(t, request.Instruction, instruction,
		"写给模型的指令必须来自执行计划，不是客户端的补充说明")
	require.Equal(t, request.Instruction, repo.input.SupplementaryNote,
		"客户端的说明单独放一个字段，不与动作指令混成一段")
	require.False(t, strings.Contains(instruction, "产出长度"),
		"提纲没有产出参数，指令里不该出现产出参数")
}

// TestGenerationOutlineRefusesASectionScope 覆盖「提纲的目标范围只能是整章」。
//
// 声明选区被拒：提纲的产出是整章的章节骨架，对着一段选区做不出它。这个拒绝
// 发生在**模型调用之前**，也不留下候选行。
func TestGenerationOutlineRefusesASectionScope(t *testing.T) {
	svc, repo := outlineFixture(t)
	request := outlineRequest(writingaction.ScopeSection)

	_, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "outline-scope-section", request)
	require.ErrorIs(t, err, writingaction.ErrScopeNotAllowed,
		"提纲声明选区必须被拒")
	require.Zero(t, repo.createRunCalls, "被拒的声明不得产生运行")
	require.Empty(t, repo.candidate.ID, "被拒的声明不得留下候选")
}

// TestGenerationOutlineRefusesOutputParams 覆盖「提纲没有产出参数」。
//
// 提纲的产出形状是章节骨架，不带长度、语气或比例。用不上的参数必须没给：请求
// 带上产出参数，服务端按这个规则拒，而不是悄悄忽略。
func TestGenerationOutlineRefusesOutputParams(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params writingaction.OutputParams
	}{
		{name: "length", params: writingaction.OutputParams{Length: writingaction.LengthMedium}},
		{name: "tone", params: writingaction.OutputParams{Tone: writingaction.ToneFormal}},
		{name: "ratio", params: writingaction.OutputParams{Ratio: 50}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := outlineFixture(t)
			request := outlineRequest(writingaction.ScopeChapter)
			params := tc.params
			request.OutputParams = &params

			_, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
				"project-1", "outline-params-"+tc.name, request)
			require.ErrorIs(t, err, writingaction.ErrInvalidOutputParams,
				"提纲带上产出参数必须被拒")
			require.Zero(t, repo.createRunCalls, "被拒的声明不得产生运行")
		})
	}
}

// TestGenerationOutlineCandidateCarriesActionOriginAndSkeleton 覆盖「提纲产出
// 章节骨架，候选带动作与来源」。
//
// 候选说的是「这是一次提纲的结果」：动作是提纲，来源是模型产出，正文是章节
// 骨架。骨架**不是**评审 findings——提纲不放进待核事项，那是评审报告的事。
func TestGenerationOutlineCandidateCarriesActionOriginAndSkeleton(t *testing.T) {
	svc, repo := outlineFixture(t)
	_, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "outline-candidate-0001", outlineRequest(writingaction.ScopeChapter))
	require.NoError(t, err)

	run, err := svc.Execute(context.Background(), repo.run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, run.Status)

	candidate := repo.candidate
	require.Equal(t, writingaction.ActionOutline, candidate.WritingAction,
		"候选必须记下自己是提纲的结果")
	require.Equal(t, writingaction.OriginGenerated, candidate.Origin,
		"提纲的正文全部来自模型")
	require.Equal(t, candidateadoption.RunModeMock, candidate.RunMode)
	require.Equal(t, outlineSkeleton, candidate.BodyMarkdown,
		"候选正文就是章节骨架")
	require.Empty(t, candidate.ReviewItems,
		"提纲是骨架，不是评审意见：待核事项必须空着")
}

// TestGenerationOutlineConflictsWithAnotherActionOnTheSameIdempotencyKey 断言
// 同一个幂等键上的不同动作被判成冲突。
//
// 动作在请求摘要里 ⇒ 摘要天然不同 ⇒ 同键不同动作是冲突，不是重放。生成管道
// 现在做三个动作（生成、转写与提纲），这一条对提纲同样成立。
func TestGenerationOutlineConflictsWithAnotherActionOnTheSameIdempotencyKey(t *testing.T) {
	svc, repo := outlineFixture(t)
	_, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "outline-shared-key", outlineRequest(writingaction.ScopeChapter))
	require.NoError(t, err)
	require.Equal(t, 1, repo.createRunCalls)

	generate := outlineRequest(writingaction.ScopeChapter)
	generateAction := writingaction.ActionGenerate
	generate.WritingAction = &generateAction
	generate.OutputParams = &writingaction.OutputParams{Length: writingaction.LengthMedium,
		Tone: writingaction.ToneFormal}
	_, err = svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "outline-shared-key", generate)
	require.ErrorIs(t, err, ErrIdempotencyConflict)
	require.Equal(t, 1, repo.createRunCalls, "冲突的请求不得再建一条运行")
}

// outlineGenerationRouter 把真实的生成服务挂到 HTTP 边上。
//
// 用例要看的正是「提纲的声明在 HTTP 这一层怎么被拒」，所以这里走真的服务层。
func outlineGenerationRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	service, _ := outlineFixture(t)
	router := gin.New()
	RegisterRoutes(router.Group("/lingdoc"), NewHandler(service, func(*gin.Context) (Actor, bool) {
		return Actor{TenantID: 1, UserID: "user-1"}, true
	}))
	return router
}

// postOutlineGeneration 发一次提纲生成请求。
func postOutlineGeneration(t *testing.T, router *gin.Engine, declaration string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"chapter_id":"chapter-1","asset_ids":["asset-1"],"instruction":"补充说明",` +
		`"expected_spec_revision":3,"expected_chapter_version_id":"chapter-v1",` + declaration + `}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/lingdoc/projects/project-1/generations",
		strings.NewReader(body))
	request.Header.Set("Idempotency-Key", "outline-http-key-0001")
	router.ServeHTTP(recorder, request)
	return recorder
}

// TestGenerationOutlineRunInputSummaryReportsTheAction 覆盖「失败与取消保留 run
// 输入」。
//
// 输入摘要要能读回「这次是按什么发起的」：提纲路径说得出动作与目标范围。失败
// 与取消的运行读回来时，靠的就是这份摘要与运行上的状态、原因。
func TestGenerationOutlineRunInputSummaryReportsTheAction(t *testing.T) {
	input := Input{Request: outlineRequest(writingaction.ScopeChapter)}
	summary := inputSummaryOf(input)
	require.Equal(t, writingaction.ActionOutline, summary.WritingAction,
		"摘要要能读回这次是提纲")
	require.Equal(t, writingaction.ScopeChapter, summary.Scope,
		"摘要要能读回这次的目标范围")
}

// TestGenerationOutlineHTTP 层：提纲的声明在 HTTP 边上被接受或被拒。
//
// 合法声明被接受（202）；声明选区、带上产出参数、动作不认识，都被拒（400）。
func TestGenerationOutlineHTTP(t *testing.T) {
	t.Run("accepted at chapter scope", func(t *testing.T) {
		router := outlineGenerationRouter(t)
		recorder := postOutlineGeneration(t, router,
			`"writing_action":"outline","scope":"chapter"`)
		require.Equal(t, http.StatusAccepted, recorder.Code, recorder.Body.String())
	})

	for _, tc := range []struct {
		name        string
		declaration string
	}{
		{name: "section scope", declaration: `"writing_action":"outline","scope":"section"`},
		{name: "output params", declaration: `"writing_action":"outline","scope":"chapter",` +
			`"output_params":{"length":"medium"}`},
		{name: "unknown action", declaration: `"writing_action":"unknown_action","scope":"chapter"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := outlineGenerationRouter(t)
			recorder := postOutlineGeneration(t, router, tc.declaration)
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), `"code":"invalid_request"`)
		})
	}
}
