package writingaction_test

import (
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/stretchr/testify/require"
)

// TestPipelineDeclaresTheActionsItImplements 断言管道按**集合**声明它实现的动作。
//
// 一条管道可以实现多个动作：选段管道既做改写，也做续写与压缩。执行方因此只问
// 「这条管道做不做这个动作」，不问「这个动作叫什么」——它在语法上就没法
// 按动作名分支（开发计划 A33）。
//
// 转写在**两条**管道上都做得出：选段管道转写选段，生成管道转写整章。因此两条
// 管道都声明它。
//
// 提纲只在生成管道上做得出：它只允许整章，选段管道不做它——它的工作单位是整章
// 骨架。
func TestPipelineDeclaresTheActionsItImplements(t *testing.T) {
	selection, err := writingaction.ActionsFor(writingaction.PipelineRewrite)
	require.NoError(t, err)
	require.True(t, slices.Contains(selection, writingaction.ActionRewrite),
		"选段管道做改写")
	require.True(t, slices.Contains(selection, writingaction.ActionContinue),
		"选段管道做续写")
	require.True(t, slices.Contains(selection, writingaction.ActionCompress),
		"选段管道做压缩")
	require.True(t, slices.Contains(selection, writingaction.ActionExpand),
		"选段管道做扩展")
	require.True(t, slices.Contains(selection, writingaction.ActionTransform),
		"选段管道做转写")

	generation, err := writingaction.ActionsFor(writingaction.PipelineGeneration)
	require.NoError(t, err)
	require.True(t, slices.Contains(generation, writingaction.ActionGenerate),
		"生成管道做生成")
	require.True(t, slices.Contains(generation, writingaction.ActionTransform),
		"生成管道做转写")
	require.True(t, slices.Contains(generation, writingaction.ActionOutline),
		"生成管道做提纲")
	require.Len(t, generation, 3, "生成管道做生成、转写与提纲三个动作")
}

// TestTheReviewPipelineDeclaresTheReviewAction 断言评审管道声明语言评审。
//
// 评审是**第三条**路径：生成管道与选段管道都以候选收尾，评审以一份报告收尾。
// 报告不是候选，所以它不归那两条管道里的任何一条。
func TestTheReviewPipelineDeclaresTheReviewAction(t *testing.T) {
	review, err := writingaction.ActionsFor(writingaction.PipelineReview)
	require.NoError(t, err)
	require.Equal(t, []writingaction.Action{writingaction.ActionReview}, review,
		"评审管道只做语言评审")
	require.Len(t, review, 1, "评审管道只实现一个动作")

	// 评审既不做整章也不做选段之外的范围：它两种范围都做。
	scopes, err := writingaction.ScopesFor(writingaction.PipelineReview)
	require.NoError(t, err)
	require.True(t, slices.Contains(scopes, writingaction.ScopeChapter), "评审管道做整章")
	require.True(t, slices.Contains(scopes, writingaction.ScopeSection), "评审管道做选段")

	// 评审管道的默认动作就是评审：请求不必点名它。
	action, err := writingaction.DefaultActionFor(writingaction.PipelineReview)
	require.NoError(t, err)
	require.Equal(t, writingaction.ActionReview, action)

	// 另外两条管道不做评审：评审既不产生候选，也不走它们那条落地路径。
	for _, pipeline := range []writingaction.Pipeline{writingaction.PipelineGeneration, writingaction.PipelineRewrite} {
		actions, err := writingaction.ActionsFor(pipeline)
		require.NoError(t, err)
		require.False(t, slices.Contains(actions, writingaction.ActionReview),
			"管道 %q 不做语言评审", pipeline)
	}
}

// TestUnknownPipelineHasNoActions 断言未知管道没有动作可问。
func TestUnknownPipelineHasNoActions(t *testing.T) {
	_, err := writingaction.ActionsFor(writingaction.Pipeline("polish"))
	require.ErrorIs(t, err, writingaction.ErrUnknownPipeline)
}

// TestPipelineDeclaresTheScopesItImplements 断言管道按**集合**声明它能做的目标
// 范围。
//
// 范围是**管道**的声明，不是动作的声明：转写在动作层面允许整章与选段两种范围，
// 但选段管道只写选段——它的工作单位就是一段选区。管道不声明范围，执行方就没有
// 依据拒绝「在选段管道里声明整章」这种请求。
func TestPipelineDeclaresTheScopesItImplements(t *testing.T) {
	selection, err := writingaction.ScopesFor(writingaction.PipelineRewrite)
	require.NoError(t, err)
	require.Equal(t, []writingaction.Scope{writingaction.ScopeSection}, selection,
		"选段管道只做选段")

	generation, err := writingaction.ScopesFor(writingaction.PipelineGeneration)
	require.NoError(t, err)
	require.True(t, slices.Contains(generation, writingaction.ScopeChapter),
		"生成管道做整章")
	require.True(t, slices.Contains(generation, writingaction.ScopeSection),
		"生成管道做选段")

	_, err = writingaction.ScopesFor(writingaction.Pipeline("polish"))
	require.ErrorIs(t, err, writingaction.ErrUnknownPipeline)
}

// TestDefaultActionForAnswersTheActionThePipelineOwns 断言管道答得出「请求没有
// 显式声明动作时它做哪个动作」。
//
// 这是旧路径的动作身份：旧请求不带动作，候选上记的却是「这次是哪个动作做的」。
// 没有它，候选记不下自己的来源。
func TestDefaultActionForAnswersTheActionThePipelineOwns(t *testing.T) {
	generation, err := writingaction.DefaultActionFor(writingaction.PipelineGeneration)
	require.NoError(t, err)
	require.Equal(t, writingaction.ActionGenerate, generation)

	selection, err := writingaction.DefaultActionFor(writingaction.PipelineRewrite)
	require.NoError(t, err)
	require.Equal(t, writingaction.ActionRewrite, selection)

	_, err = writingaction.DefaultActionFor(writingaction.Pipeline("polish"))
	require.ErrorIs(t, err, writingaction.ErrUnknownPipeline)
}
