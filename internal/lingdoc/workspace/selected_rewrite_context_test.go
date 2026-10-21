package workspace

import (
	"context"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// contextRewriteSources 按一张表回答「这条来源现在能不能用」。
type contextRewriteSources struct {
	SourceApplicationService
	available map[string]evidence.Source
}

func (s *contextRewriteSources) GetSource(_ context.Context, _ Actor, projectID, id string) (evidence.Source, error) {
	source, ok := s.available[id]
	if !ok {
		return evidence.Source{}, ErrRewriteSourceDenied
	}
	source.ProjectID = projectID
	return source, nil
}

// contextRewriteModel 按这次真正拿到的来源作答。
//
// 它不能替实现说话：模型回什么就等于模型能看到什么，所以「被剔除的来源有
// 没有进模型请求」只能这样看。
type contextRewriteModel struct {
	calls  int
	prompt SelectedRewritePrompt
}

func (*contextRewriteModel) RunMode() string { return "mock" }

func (m *contextRewriteModel) RewriteSelected(_ context.Context, _ Actor, p SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	m.calls++
	m.prompt = p
	ids := make([]string, 0, len(p.Sources))
	for _, source := range p.Sources {
		ids = append(ids, source.ID)
	}
	body := "revised without any source"
	if len(ids) > 0 {
		body = "revised [[source:" + ids[0] + "]]"
	}
	return SelectedRewriteOutput{ReplacementMarkdown: body, SourceIDs: ids, RunMode: "mock"}, nil
}

// contextRewriteFixture 造一个带两条来源的改写夹具。
func contextRewriteFixture(t *testing.T) (*SelectedRewriteService, *contextRewriteSources, *contextRewriteModel, SelectedRewriteRequest) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rewrite.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	selected := "original [[source:existing]]"
	workspace := &inheritedRewriteWorkspace{copy: WorkingCopy{ProjectID: "project", ChapterID: "chapter", SpecRevision: 1,
		WorkingCopyRevision: 1, BodyMarkdown: selected + " context", SourceIDs: []string{"existing"}}}
	sources := &contextRewriteSources{available: map[string]evidence.Source{
		"existing": {ID: "existing", Status: evidence.SourceAvailable, AssetID: "asset-1", AssetRevision: 1,
			Locator: "page/1", QuotedTextHash: "hash-1", QuotedText: "supported fact"},
		"extra": {ID: "extra", Status: evidence.SourceAvailable, AssetID: "asset-2", AssetRevision: 1,
			Locator: "page/2", QuotedTextHash: "hash-2", QuotedText: "another fact"},
	}}
	model := &contextRewriteModel{}
	service := NewSelectedRewriteService(db, workspace, sources, model)
	require.NoError(t, service.store.AutoMigrate(context.Background()))
	input := SelectedRewriteRequest{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
		Selection:   RewriteSelection{EndUTF16: len(utf16.Encode([]rune(selected))), SelectedText: selected},
		Instruction: "polish", SourceIDs: []string{}}
	return service, sources, model, input
}

// actionContextRequest 给一份走动作路径的改写请求。
func actionContextRequest(base SelectedRewriteRequest) SelectedRewriteRequest {
	base.WritingAction = actionPointer(writingaction.ActionRewrite)
	base.OutputParams = &writingaction.OutputParams{Length: writingaction.LengthShort, Tone: writingaction.ToneFormal}
	base.Scope = actionPointer(writingaction.ScopeSection)
	base.Instruction = ""
	return base
}

// TestRewriteActionAllowsSelectingItsOptionalContext 断言动作的可选上下文
// 集合真的被声明出来了。
//
// 词汇包是这件事的唯一拥有者：可选集合恒为空，客户端就没有任何可选的
// 东西可选，A37 也就无从谈起。
func TestRewriteActionAllowsSelectingItsOptionalContext(t *testing.T) {
	plan, err := writingaction.PlanFor(writingaction.ActionRewrite, writingaction.ScopeSection,
		writingaction.OutputParams{Length: writingaction.LengthShort, Tone: writingaction.ToneFormal})
	require.NoError(t, err)
	require.Equal(t, []writingaction.ContextItem{writingaction.ContextAssets, writingaction.ContextCards}, plan.Context.Optional)
	require.True(t, plan.AllowsContext(writingaction.ContextAssets))
	require.True(t, plan.AllowsContext(writingaction.ContextCards))
	// 模板与规则是这个动作读得着的：它只是不由用户增删。
	require.True(t, plan.AllowsContext(writingaction.ContextTemplate))
	// 整章上下文对这个动作是被禁的：选区执行不读整章。
	require.False(t, plan.AllowsContext(writingaction.ContextChapter))
}

// TestRewriteContextSelectionRejectsCategoriesTheActionDoesNotRead 断言越过
// 策略的上下文选择被拒。
//
// 客户端要求纳入一个这个动作根本不读的东西，服务端不会替它猜一个意思，
// 也不会把这一类上下文悄悄塞进模型请求。
func TestRewriteContextSelectionRejectsCategoriesTheActionDoesNotRead(t *testing.T) {
	service, _, model, base := contextRewriteFixture(t)
	input := actionContextRequest(base)
	input.Context = &RewriteContextSelection{
		Include: map[writingaction.ContextItem][]RewriteContextItem{
			writingaction.ContextTemplate: {{ID: "template-1"}},
		},
	}
	_, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "context-not-allowed", input)
	require.ErrorIs(t, err, ErrRewriteContextNotAllowed)
	require.Zero(t, model.calls, "越过策略的请求不得触达模型")
	var rows int64
	require.NoError(t, service.store.db.Model(&selectedRewriteRow{}).Count(&rows).Error)
	require.Zero(t, rows, "越过策略的请求不得留下候选行")
}

// TestRewriteContextSelectionRefusesToRemoveARequiredContext 断言用户不能把
// 这个动作必须用的那一类上下文剔除掉。
func TestRewriteContextSelectionRefusesToRemoveARequiredContext(t *testing.T) {
	service, _, model, base := contextRewriteFixture(t)
	input := actionContextRequest(base)
	input.Context = &RewriteContextSelection{
		Exclude: map[writingaction.ContextItem][]RewriteContextItem{
			writingaction.ContextTemplate: {{ID: "template-1"}},
		},
	}
	_, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "context-required", input)
	require.ErrorIs(t, err, ErrRewriteContextRequired)
	require.Zero(t, model.calls)
}

// TestRewriteContextSelectionAddsAndRemovesSources 断言用户能在执行前增删
// 纳入的来源，而且删掉的确实没进模型请求。
func TestRewriteContextSelectionAddsAndRemovesSources(t *testing.T) {
	service, _, model, base := contextRewriteFixture(t)
	input := actionContextRequest(base)
	input.Context = &RewriteContextSelection{
		Include: map[writingaction.ContextItem][]RewriteContextItem{
			writingaction.ContextAssets: {{ID: "extra"}},
		},
		Exclude: map[writingaction.ContextItem][]RewriteContextItem{
			writingaction.ContextAssets: {{ID: "existing"}},
		},
	}
	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "context-add-remove", input)
	require.NoError(t, err)
	require.Equal(t, 1, model.calls)
	require.Equal(t, []string{"extra"}, candidate.SourceIDs, "只有用户选中的来源进了模型请求")
	require.Len(t, model.prompt.Sources, 1)
	require.Equal(t, "extra", model.prompt.Sources[0].ID)
	require.NotContains(t, candidate.SourceIDs, "existing")
}

// TestRewriteContextSelectionDropsUnauthorizedItemsAndNamesThem 断言没授权
// 的条目不进模型请求，而且被点名。
//
// 静默丢弃会让用户以为它进去了：模型没看到它，候选里也没有它，而用户
// 以为它参与了这次改写。
func TestRewriteContextSelectionDropsUnauthorizedItemsAndNamesThem(t *testing.T) {
	service, _, model, base := contextRewriteFixture(t)
	input := actionContextRequest(base)
	input.Context = &RewriteContextSelection{
		Include: map[writingaction.ContextItem][]RewriteContextItem{
			writingaction.ContextAssets: {{ID: "extra"}, {ID: "revoked"}},
		},
	}
	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "context-dropped", input)
	require.NoError(t, err)
	require.Equal(t, 1, model.calls)
	require.Equal(t, []string{"existing", "extra"}, candidate.SourceIDs)
	require.Len(t, candidate.DroppedContext, 1)
	require.Equal(t, writingaction.ContextAssets, candidate.DroppedContext[0].Category)
	require.Equal(t, "revoked", candidate.DroppedContext[0].ID)
	require.Equal(t, rewriteContextDroppedNotAuthorized, candidate.DroppedContext[0].Reason)
	for _, source := range model.prompt.Sources {
		require.NotEqual(t, "revoked", source.ID, "没授权的来源不得进模型请求")
	}
}
