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

// transformRequest 造一份按转写动作发起的生成请求。
//
// 它自带夹具，不复用生成动作的请求：转写有自己的一组子参数，两个子模式的
// 取值也各不相同。复用别的动作的夹具会让这张票的切片依赖上一张票的测试文件。
func transformRequest(scope writingaction.Scope, mode writingaction.TransformMode) Request {
	action := writingaction.ActionTransform
	params := writingaction.OutputParams{Length: writingaction.LengthMedium}
	switch mode {
	case writingaction.TransformTranslate:
		language := writingaction.LangZH
		params.Transform = &writingaction.TransformParams{Mode: mode, TargetLanguage: &language}
	case writingaction.TransformExplain:
		audience := writingaction.AudienceNovice
		params.Transform = &writingaction.TransformParams{Mode: mode, Audience: &audience}
	}
	return Request{
		ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "补充说明",
		ExpectedSpecRevision: 3, ExpectedChapterVersionID: stringPtr("chapter-v1"),
		WritingAction: &action, Scope: &scope, OutputParams: &params,
	}
}

// TestGenerationTransformRunsAtBothScopes 覆盖「转写的目标范围可以是整章，也可以
// 是选段」。
//
// 转写是第一个在两条管道上都做得出的动作：选段管道转写选段，生成管道转写整章。
// 两种范围都要真的跑起来，而且写给模型的指令都来自执行计划。
func TestGenerationTransformRunsAtBothScopes(t *testing.T) {
	for _, scope := range []writingaction.Scope{writingaction.ScopeChapter, writingaction.ScopeSection} {
		t.Run(string(scope), func(t *testing.T) {
			svc, repo := actionFixture()
			request := transformRequest(scope, writingaction.TransformTranslate)
			_, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
				"project-1", "transform-scope-"+string(scope), request)
			require.NoError(t, err)
			require.Equal(t, 1, repo.createRunCalls, "合法声明要产生一条运行")
			instruction := repo.input.Instruction
			require.NotEmpty(t, instruction)
			require.NotEqual(t, request.Instruction, instruction,
				"写给模型的指令必须来自执行计划，不是客户端的补充说明")
			require.Equal(t, request.Instruction, repo.input.SupplementaryNote,
				"客户端的说明单独放一个字段，不与动作指令混成一段")
		})
	}
}

// TestGenerationTransformRendersTheSubModeIntoTheModelRequest 覆盖「两个子模式
// 的子参数在执行前可见」。
//
// 子参数由词汇包渲染进动作指令：执行方不解释产出参数。translate 看得见目标语言，
// explain 看得见读者层次。动作指令本身说的是两个模式各自是什么意思（那是词汇包
// 的文本），所以断言落在**渲染出来的子参数那一句**上。
func TestGenerationTransformRendersTheSubModeIntoTheModelRequest(t *testing.T) {
	for _, tc := range []struct {
		mode   writingaction.TransformMode
		want   string
		absent string
	}{
		{mode: writingaction.TransformTranslate,
			want:   "转写模式：译成 zh。",
			absent: "转写模式：面向"},
		{mode: writingaction.TransformExplain,
			want:   "转写模式：面向 novice 解释。",
			absent: "转写模式：译成"},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			svc, repo := actionFixture()
			_, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
				"project-1", "transform-render-"+string(tc.mode),
				transformRequest(writingaction.ScopeChapter, tc.mode))
			require.NoError(t, err)
			instruction := repo.input.Instruction
			require.Contains(t, instruction, tc.want,
				"动作指令 %q 里要有渲染出来的子参数", instruction)
			require.NotContains(t, instruction, tc.absent,
				"动作指令 %q 里不该有另一个子模式的渲染结果", instruction)
			require.Contains(t, instruction, "产出长度",
				"长度是转写声明的产出参数，执行前要看得见")
		})
	}
}

// TestGenerationTransformRefusesIllegalDeclarations 覆盖转写子参数的闭合校验。
//
// 缺子参数、混用子参数、未知子模式、动作用不上的参数、缺长度——每一种都在
// **模型调用之前**被拒，一条运行都不产生。
func TestGenerationTransformRefusesIllegalDeclarations(t *testing.T) {
	unknownMode := writingaction.TransformMode("summarize")
	unknownLanguage := writingaction.TargetLanguage("klingon")
	unknownAudience := writingaction.Audience("aliens")
	for _, tc := range []struct {
		name   string
		mutate func(*Request)
	}{
		{"翻译没给目标语言", func(r *Request) {
			r.OutputParams.Transform = &writingaction.TransformParams{Mode: writingaction.TransformTranslate}
		}},
		{"解释没给读者层次", func(r *Request) {
			r.OutputParams.Transform = &writingaction.TransformParams{Mode: writingaction.TransformExplain}
		}},
		{"两个子参数混用", func(r *Request) {
			language := writingaction.LangEN
			audience := writingaction.AudiencePeer
			r.OutputParams.Transform = &writingaction.TransformParams{Mode: writingaction.TransformTranslate,
				TargetLanguage: &language, Audience: &audience}
		}},
		{"子模式不认识", func(r *Request) {
			r.OutputParams.Transform = &writingaction.TransformParams{Mode: unknownMode}
		}},
		{"目标语言不在枚举里", func(r *Request) {
			r.OutputParams.Transform = &writingaction.TransformParams{Mode: writingaction.TransformTranslate,
				TargetLanguage: &unknownLanguage}
		}},
		{"读者层次不在枚举里", func(r *Request) {
			r.OutputParams.Transform = &writingaction.TransformParams{Mode: writingaction.TransformExplain,
				Audience: &unknownAudience}
		}},
		{"转写没给子参数", func(r *Request) {
			r.OutputParams.Transform = nil
		}},
		{"转写没给长度", func(r *Request) {
			r.OutputParams.Length = ""
		}},
		{"动作用不上的比例被带上", func(r *Request) {
			r.OutputParams.Ratio = 40
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := actionFixture()
			request := transformRequest(writingaction.ScopeChapter, writingaction.TransformTranslate)
			tc.mutate(&request)
			_, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
				"project-1", "transform-invalid-0001", request)
			require.ErrorIs(t, err, writingaction.ErrInvalidOutputParams)
			require.Zero(t, repo.createRunCalls, "非法声明不得产生运行")
		})
	}
}

// TestGenerationTransformCandidateCarriesActionOriginAndRunMode 覆盖「候选带
// 动作、来源与运行模式」。
//
// 转写的候选说的是「这是一次转写的结果」：动作是转写，来源是模型产出，运行模式
// 是它真的跑到的那一档。
func TestGenerationTransformCandidateCarriesActionOriginAndRunMode(t *testing.T) {
	svc, repo := actionFixture()
	_, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "transform-candidate-0001",
		transformRequest(writingaction.ScopeChapter, writingaction.TransformTranslate))
	require.NoError(t, err)
	run, err := svc.Execute(context.Background(), repo.run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, run.Status)
	require.Equal(t, writingaction.ActionTransform, repo.candidate.WritingAction)
	require.Equal(t, writingaction.OriginGenerated, repo.candidate.Origin)
	require.Equal(t, candidateadoption.RunModeMock, repo.candidate.RunMode)
}

// TestGenerationTransformAndGenerateConflictOnTheSameIdempotencyKey 断言同一个
// 幂等键上的不同动作被判成冲突。
//
// 生成管道现在做两个动作（生成与转写）：动作在请求摘要里 ⇒ 摘要天然不同 ⇒
// 同键不同动作是冲突，不是重放。
func TestGenerationTransformAndGenerateConflictOnTheSameIdempotencyKey(t *testing.T) {
	svc, repo := actionFixture()
	_, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "transform-shared-key",
		transformRequest(writingaction.ScopeChapter, writingaction.TransformTranslate))
	require.NoError(t, err)
	require.Equal(t, 1, repo.createRunCalls)

	generate := transformRequest(writingaction.ScopeChapter, writingaction.TransformTranslate)
	generateAction := writingaction.ActionGenerate
	generate.WritingAction = &generateAction
	generate.OutputParams = &writingaction.OutputParams{Length: writingaction.LengthMedium,
		Tone: writingaction.ToneFormal}
	_, err = svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "transform-shared-key", generate)
	require.ErrorIs(t, err, ErrIdempotencyConflict)
	require.Equal(t, 1, repo.createRunCalls, "冲突的请求不得再建一条运行")
}

// TestGenerationTransformCandidateGoesThroughTheAdoptionConfirmation 覆盖生成侧
// 的转写候选也走显式确认采纳。
//
// 缺确认载荷被拒；确认的动作与候选对不上被拒；载荷齐全才写得进去——确认的
// 动作正是转写。
func TestGenerationTransformCandidateGoesThroughTheAdoptionConfirmation(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "transform-adoption.db")), &gorm.Config{})
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
	svc := NewService(testAuthorizer{}, testInputs{input: input}, testSources{}, testCurrentness{current: true},
		testModel{draft: Draft{BodyMarkdown: "Text [[source:source-asset-1]]", Sources: []Source{{ID: "source-asset-1"}}}},
		repo, &testEnqueuer{})
	svc.NewID = func() string { return "candidate-1" }
	request := transformRequest(writingaction.ScopeChapter, writingaction.TransformTranslate)
	request.ExpectedChapterVersionID = nil
	run, err := svc.Start(ctx, Actor{TenantID: 1, UserID: "user-1"}, "project-1", "transform-adopt-0001", request)
	require.NoError(t, err)
	completed, err := svc.Execute(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, completed.Status)
	require.NotNil(t, completed.CandidateID)

	allow := adoptionSourcePolicyFunc(func(context.Context, string, string, []string) error { return nil })
	adoption := candidateadoption.NewCandidateAdoptionService(store, allow, adoptionAuthorizerFunc(nil))

	// 缺确认载荷 ⇒ 拒。
	_, err = adoption.AcceptCandidate(ctx, candidateadoption.AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: *completed.CandidateID, ActorID: "user-1",
		IdempotencyKey: "transform-adoption-no-confirmation-0001", ExpectedSpecRevision: 3,
		ExpectedWorkingCopyRevision: 0, ExpectedBodyHash: workingCopyBodyDigest(""),
	})
	require.ErrorIs(t, err, candidateadoption.ErrInvalidRequest)

	// 确认载荷的动作与候选对不上 ⇒ 拒。
	_, err = adoption.AcceptCandidate(ctx, candidateadoption.AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: *completed.CandidateID, ActorID: "user-1",
		IdempotencyKey: "transform-adoption-wrong-action-0001", ExpectedSpecRevision: 3,
		ExpectedWorkingCopyRevision: 0, ExpectedBodyHash: workingCopyBodyDigest(""),
		Confirmation: &candidateadoption.AdoptionConfirmation{
			WritingAction: writingaction.ActionGenerate,
			Target:        writingaction.TargetFormalVersionWhole,
			ConfirmedAt:   time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC),
			ConfirmedBy:   "user-1",
			Origin:        writingaction.OriginGenerated,
		},
	})
	require.Error(t, err)

	// 确认载荷齐全 ⇒ 采纳成立：确认的动作正是转写。
	result, err := adoption.AcceptCandidate(ctx, candidateadoption.AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: *completed.CandidateID, ActorID: "user-1",
		IdempotencyKey: "transform-adoption-confirmed-0001", ExpectedSpecRevision: 3,
		ExpectedWorkingCopyRevision: 0, ExpectedBodyHash: workingCopyBodyDigest(""),
		Confirmation: &candidateadoption.AdoptionConfirmation{
			WritingAction: writingaction.ActionTransform,
			Target:        writingaction.TargetFormalVersionWhole,
			ConfirmedAt:   time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC),
			ConfirmedBy:   "user-1",
			Origin:        writingaction.OriginGenerated,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Chapter.CurrentVersionID)
}
