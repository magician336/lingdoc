package generation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// actionFixture 造一个动作路径用的生成夹具。
//
// 它与 generationFixture 的差别只有一处：这一份带一份章节正文，动作路径的
// 范围与上下文选择要有东西可挑。传入的资料就是这次执行被允许读的资料。
func actionFixture(assets ...string) (*Service, *testRepository) {
	if len(assets) == 0 {
		assets = []string{"asset-1"}
	}
	actor := Actor{TenantID: 1, UserID: "user-1"}
	chapterVersion := "chapter-v1"
	versions := make([]candidateadoption.AssetVersion, 0, len(assets))
	sources := make([]Source, 0, len(assets))
	for i, asset := range assets {
		versions = append(versions, candidateadoption.AssetVersion{AssetID: asset, AssetRevision: i + 1})
		sources = append(sources, Source{ID: "source-" + asset, ProjectID: "project-1", AssetID: asset,
			AssetRevision: i + 1, Status: "available"})
	}
	basis := candidateadoption.Basis{SpecRevision: 3, ChapterVersionID: &chapterVersion, AssetVersions: versions}
	input := Input{Actor: actor, Basis: basis,
		Workspace: candidateadoption.GenerationContext{ProjectID: "project-1", ChapterID: "chapter-1", SpecRevision: 3,
			ChapterVersionID: &chapterVersion, Basis: basis,
			Chapter: candidateadoption.Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "section-1",
				Title: "研究问题", BodyMarkdown: "已有正文"}},
		Sources: sources}
	repo := &testRepository{input: input, run: Run{ID: "run-1", ProjectID: "project-1", ChapterID: "chapter-1", Status: StatusQueued}}
	svc := NewService(testAuthorizer{}, testInputs{input: input}, testSources{}, testCurrentness{current: true},
		testModel{draft: Draft{BodyMarkdown: "Text [[source:source-asset-1]]", Sources: []Source{{ID: "source-asset-1"}}}}, repo, &testEnqueuer{})
	svc.NewID = func() string { return "candidate-1" }
	return svc, repo
}

// actionRequest 造一个按动作发起的生成请求。
func actionRequest() Request {
	action := writingaction.ActionGenerate
	scope := writingaction.ScopeChapter
	params := writingaction.OutputParams{Length: writingaction.LengthMedium, Tone: writingaction.ToneFormal}
	return Request{
		ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "补充说明",
		ExpectedSpecRevision: 3, ExpectedChapterVersionID: stringPtr("chapter-v1"),
		WritingAction: &action, Scope: &scope, OutputParams: &params,
	}
}

// TestGenerationActionPathAcceptsBothScopes 覆盖「整章与选区两种目标范围都能
// 被声明」。
//
// 两种范围都要真的跑起来，并且写给模型的指令都来自执行计划。
func TestGenerationActionPathAcceptsBothScopes(t *testing.T) {
	for _, scope := range []writingaction.Scope{writingaction.ScopeChapter, writingaction.ScopeSection} {
		t.Run(string(scope), func(t *testing.T) {
			svc, repo := actionFixture()
			request := actionRequest()
			request.Scope = &scope

			if _, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
				"project-1", "action-scope-"+string(scope), request); err != nil {
				t.Fatalf("Start() error = %v, want the scope to be accepted", err)
			}
			if repo.createRunCalls != 1 {
				t.Fatalf("create run calls = %d, want 1", repo.createRunCalls)
			}
			instruction := repo.input.Instruction
			if instruction == "" || instruction == request.Instruction {
				t.Fatalf("model instruction = %q, want the rendered action instruction", instruction)
			}
			if repo.input.SupplementaryNote != request.Instruction {
				t.Fatalf("supplementary note = %q, want the client instruction", repo.input.SupplementaryNote)
			}
		})
	}
}

// TestGenerationActionPathRendersTheActionInstruction 覆盖「产出参数执行前可见」。
//
// 长度与语气都写进指令，客户端的说明不与动作指令混成一段。
func TestGenerationActionPathRendersTheActionInstruction(t *testing.T) {
	svc, repo := actionFixture()
	request := actionRequest()
	if _, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "action-instruction-0001", request); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	instruction := repo.input.Instruction
	for _, want := range []string{"medium", "formal", "产出长度", "产出语气"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("model instruction = %q, want it to carry %q", instruction, want)
		}
	}
	if strings.Contains(instruction, request.Instruction) {
		t.Fatalf("model instruction = %q, want the client note kept apart from the action instruction", instruction)
	}
}

// TestGenerationActionPathRejectsInvalidDeclarations 覆盖动作声明的闭合校验。
//
// 非法声明不产生 run：产出参数与目标范围在模型调用之前就被拒。
func TestGenerationActionPathRejectsInvalidDeclarations(t *testing.T) {
	unknown := writingaction.Action("unknown")
	review := writingaction.ActionReview
	paragraph := writingaction.Scope("paragraph")
	badLength := writingaction.Length("huge")
	ratio := 50
	cases := []struct {
		name    string
		mutate  func(*Request)
		wantErr error
	}{
		{name: "unknown action", mutate: func(r *Request) { r.WritingAction = &unknown }, wantErr: writingaction.ErrUnknownAction},
		{name: "missing action", mutate: func(r *Request) { r.WritingAction = nil }, wantErr: ErrInvalidRequest},
		{name: "scope not allowed", mutate: func(r *Request) { r.Scope = &paragraph }, wantErr: writingaction.ErrScopeNotAllowed},
		{name: "length out of range", mutate: func(r *Request) {
			params := writingaction.OutputParams{Length: badLength, Tone: writingaction.ToneFormal}
			r.OutputParams = &params
		}, wantErr: writingaction.ErrInvalidOutputParams},
		{name: "length missing", mutate: func(r *Request) {
			params := writingaction.OutputParams{Tone: writingaction.ToneFormal}
			r.OutputParams = &params
		}, wantErr: writingaction.ErrInvalidOutputParams},
		{name: "tone missing", mutate: func(r *Request) {
			params := writingaction.OutputParams{Length: writingaction.LengthMedium}
			r.OutputParams = &params
		}, wantErr: writingaction.ErrInvalidOutputParams},
		{name: "unused parameter", mutate: func(r *Request) {
			params := writingaction.OutputParams{Length: writingaction.LengthMedium, Tone: writingaction.ToneFormal, Ratio: ratio}
			r.OutputParams = &params
		}, wantErr: writingaction.ErrInvalidOutputParams},
		// 样本是「评审」：它允许整章与选段两种范围，PlanFor 不会先把它拒掉，
		// 所以请求真的走到了管道归属判定那一支；而它不在任何管道里，于是被拒。
		// 错误期望一字未动——换的只是样本。
		{name: "another action on this endpoint", mutate: func(r *Request) {
			r.WritingAction = &review
			r.OutputParams = nil
		}, wantErr: ErrInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := actionFixture()
			request := actionRequest()
			tc.mutate(&request)
			_, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
				"project-1", "action-invalid-0001", request)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Start() error = %v, want %v", err, tc.wantErr)
			}
			if repo.createRunCalls != 0 {
				t.Fatalf("create run calls = %d, want 0: an invalid declaration must not produce a run", repo.createRunCalls)
			}
		})
	}
}

// TestGenerationActionPathRejectsContextBeyondPolicy 覆盖上下文选择的策略校验。
//
// 纳入一个这个动作不读的类别、剔除一个这个动作不读的类别、把一个必须用的类别
// 整个拿掉，都被拒。
func TestGenerationActionPathRejectsContextBeyondPolicy(t *testing.T) {
	cases := []struct {
		name    string
		context GenerationContextSelection
		wantErr error
	}{
		{name: "include a category the action does not read", context: GenerationContextSelection{
			Include: map[writingaction.ContextItem][]GenerationContextItem{
				writingaction.ContextTemplate: {{ID: "template-1"}},
			}}, wantErr: ErrContextNotAllowed},
		{name: "exclude a category the action does not read", context: GenerationContextSelection{
			Exclude: map[writingaction.ContextItem][]GenerationContextItem{
				writingaction.ContextTemplate: {{ID: "template-1"}},
			}}, wantErr: ErrContextNotAllowed},
		{name: "exclude the last entry of a required category", context: GenerationContextSelection{
			Exclude: map[writingaction.ContextItem][]GenerationContextItem{
				writingaction.ContextAssets: {{ID: "asset-1"}},
			}}, wantErr: ErrContextRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := actionFixture()
			request := actionRequest()
			request.Context = &tc.context
			_, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
				"project-1", "action-context-0001", request)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Start() error = %v, want %v", err, tc.wantErr)
			}
			if repo.createRunCalls != 0 {
				t.Fatalf("create run calls = %d, want 0", repo.createRunCalls)
			}
		})
	}
}

// TestGenerationActionPathKeepsExcludedContextOutOfTheModelRequest 覆盖「用户剔除
// 的条目不进模型请求」。
//
// 剔除掉的资料不进这次执行的资料集合，而且被点名：丢弃是可见的。
func TestGenerationActionPathKeepsExcludedContextOutOfTheModelRequest(t *testing.T) {
	svc, repo := actionFixture("asset-1")
	request := actionRequest()
	request.AssetIDs = []string{"asset-1", "asset-2"}
	request.Context = &GenerationContextSelection{
		Exclude: map[writingaction.ContextItem][]GenerationContextItem{
			writingaction.ContextAssets: {{ID: "asset-2"}},
		}}

	run, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "action-exclude-0001", request)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(repo.input.Request.AssetIDs) != 1 || repo.input.Request.AssetIDs[0] != "asset-1" {
		t.Fatalf("model request assets = %v, want only asset-1", repo.input.Request.AssetIDs)
	}
	if len(run.DroppedContext) != 1 || run.DroppedContext[0].ID != "asset-2" ||
		run.DroppedContext[0].Category != writingaction.ContextAssets ||
		run.DroppedContext[0].Reason != contextDroppedExcluded {
		t.Fatalf("dropped context = %+v, want the excluded asset named with a reason", run.DroppedContext)
	}
}

// TestGenerationActionPathIncludesSelectedContext 覆盖「用户点名的条目进入这次
// 执行」。
func TestGenerationActionPathIncludesSelectedContext(t *testing.T) {
	svc, repo := actionFixture("asset-1", "asset-2")
	request := actionRequest()
	request.Context = &GenerationContextSelection{
		Include: map[writingaction.ContextItem][]GenerationContextItem{
			writingaction.ContextAssets: {{ID: "asset-2"}},
		}}

	if _, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "action-include-0001", request); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	assets := repo.input.Request.AssetIDs
	if len(assets) != 2 || assets[0] != "asset-1" || assets[1] != "asset-2" {
		t.Fatalf("model request assets = %v, want asset-1 and asset-2", assets)
	}
}

// TestLegacyGenerationRequestKeepsItsBytes 覆盖「旧请求的 JSON 与摘要逐字不变」。
//
// 请求摘要 = 整个请求结构的哈希。这四个新字段必须是**指针**加 omitempty：非
// 指针的结构体字段会让旧请求多出一个键，历史幂等键全部失配。
func TestLegacyGenerationRequestKeepsItsBytes(t *testing.T) {
	request := Request{ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "Draft",
		ExpectedSpecRevision: 3, ExpectedChapterVersionID: stringPtr("chapter-v1")}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	want := `{"chapter_id":"chapter-1","asset_ids":["asset-1"],"instruction":"Draft","expected_spec_revision":3,"expected_chapter_version_id":"chapter-v1"}`
	if string(encoded) != want {
		t.Fatalf("legacy request JSON = %s, want %s", encoded, want)
	}
}

// TestLegacyGenerationPathKeepsTheClientInstruction 覆盖「旧路径发给模型的 JSON
// 逐字节不变」。
//
// 旧路径下写给模型的指令就是客户端的自由文本，补充说明恒为空——模型输入 JSON
// 里那个带 omitempty 的新字段整个不出现。
func TestLegacyGenerationPathKeepsTheClientInstruction(t *testing.T) {
	svc, repo := generationFixture()
	request := Request{ChapterID: "chapter-1", AssetIDs: []string{"asset-1"}, Instruction: "Draft",
		ExpectedSpecRevision: 3, ExpectedChapterVersionID: stringPtr("chapter-v1")}
	if _, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "legacy-instruction-0001", request); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if repo.input.Instruction != request.Instruction {
		t.Fatalf("model instruction = %q, want the client instruction %q", repo.input.Instruction, request.Instruction)
	}
	if repo.input.SupplementaryNote != "" {
		t.Fatalf("supplementary note = %q, want it empty on the legacy path", repo.input.SupplementaryNote)
	}
}

// TestGenerationRunInputSummaryReportsTheAction 覆盖「失败与取消保留 run 输入」。
//
// 输入摘要要能读回「这次是按什么发起的」：动作路径说得出目标范围；旧路径没有
// 声明过范围，摘要里也不出现这一项。动作本身两条路径都是「生成」——候选带的
// 就是它，摘要与候选说的是同一件事。
func TestGenerationRunInputSummaryReportsTheAction(t *testing.T) {
	action := actionRequest()
	input := Input{Request: action}
	summary := inputSummaryOf(input)
	if summary.WritingAction != writingaction.ActionGenerate {
		t.Fatalf("summary writing action = %q, want %q", summary.WritingAction, writingaction.ActionGenerate)
	}
	if summary.Scope != writingaction.ScopeChapter {
		t.Fatalf("summary scope = %q, want %q", summary.Scope, writingaction.ScopeChapter)
	}

	legacy := Input{Request: Request{ChapterID: "chapter-1", Instruction: "Draft"}}
	legacySummary := inputSummaryOf(legacy)
	if legacySummary.Scope != "" {
		t.Fatalf("legacy summary scope = %q, want no scope: the legacy path declares none", legacySummary.Scope)
	}
}

// TestGenerationActionPathCandidateCarriesActionOriginAndRunMode 覆盖「候选带
// 动作、来源与运行模式」。
func TestGenerationActionPathCandidateCarriesActionOriginAndRunMode(t *testing.T) {
	svc, repo := actionFixture()
	if _, err := svc.Start(context.Background(), Actor{TenantID: 1, UserID: "user-1"},
		"project-1", "action-candidate-0001", actionRequest()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	run, err := svc.Execute(context.Background(), repo.run.ID)
	if err != nil || run.Status != StatusSucceeded {
		t.Fatalf("Execute() = (%+v, %v), want succeeded", run, err)
	}
	candidate := repo.candidate
	if candidate.WritingAction != writingaction.ActionGenerate {
		t.Fatalf("candidate writing action = %q, want %q", candidate.WritingAction, writingaction.ActionGenerate)
	}
	if candidate.Origin != writingaction.OriginGenerated {
		t.Fatalf("candidate origin = %q, want %q", candidate.Origin, writingaction.OriginGenerated)
	}
	if candidate.RunMode != candidateadoption.RunModeMock {
		t.Fatalf("candidate run mode = %q, want %q", candidate.RunMode, candidateadoption.RunModeMock)
	}
	if len(candidate.ReviewItems) != 0 {
		t.Fatalf("candidate review items = %+v, want the body and the review items kept apart", candidate.ReviewItems)
	}
}
