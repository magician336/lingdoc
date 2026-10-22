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
	require.Len(t, generation, 2, "生成管道做生成与转写两个动作")
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
