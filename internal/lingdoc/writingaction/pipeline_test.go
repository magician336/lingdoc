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
func TestPipelineDeclaresTheActionsItImplements(t *testing.T) {
	selection, err := writingaction.ActionsFor(writingaction.PipelineRewrite)
	require.NoError(t, err)
	require.True(t, slices.Contains(selection, writingaction.ActionRewrite),
		"选段管道做改写")
	require.True(t, slices.Contains(selection, writingaction.ActionContinue),
		"选段管道做续写")
	require.True(t, slices.Contains(selection, writingaction.ActionCompress),
		"选段管道做压缩")

	generation, err := writingaction.ActionsFor(writingaction.PipelineGeneration)
	require.NoError(t, err)
	require.Equal(t, []writingaction.Action{writingaction.ActionGenerate}, generation,
		"生成管道只做生成")
}

// TestUnknownPipelineHasNoActions 断言未知管道没有动作可问。
func TestUnknownPipelineHasNoActions(t *testing.T) {
	_, err := writingaction.ActionsFor(writingaction.Pipeline("polish"))
	require.ErrorIs(t, err, writingaction.ErrUnknownPipeline)
}

// TestActionForAnswersOnlyForSingleActionPipelines 断言「这条管道做哪个动作」
// 只在管道只做一个动作时才有答案。
func TestActionForAnswersOnlyForSingleActionPipelines(t *testing.T) {
	action, err := writingaction.ActionFor(writingaction.PipelineGeneration)
	require.NoError(t, err)
	require.Equal(t, writingaction.ActionGenerate, action)

	_, err = writingaction.ActionFor(writingaction.PipelineRewrite)
	require.ErrorIs(t, err, writingaction.ErrAmbiguousPipeline,
		"选段管道做多个动作，单动作的问答没有答案")
}
