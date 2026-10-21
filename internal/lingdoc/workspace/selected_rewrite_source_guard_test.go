package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 这条断言指认改写侧的既有保证：A14「不得编造授权来源」。
//
// 模型可以引用资料，但只能引用**被授权**的那几条。改写侧的判据是
// validateRewriteOutput(output, input.SourceIDs)：模型输出的来源集合必须落在
// 请求授权的来源集合内，且正文里的来源标记集合必须与它一致。
//
// 既有证据：selected_rewrite_inherited_sources_test.go 的
// TestSelectedRewriteInheritanceDoesNotAuthorizeModelInventedSources
// （它从继承来源的夹具出发，证明模型编造来源不会被继承的授权放行）。
// 这条断言盯的是**判据本身**——validateRewriteOutput 就是那道门。
func TestRewriteOutputRefusesModelInventedSources(t *testing.T) {
	requested := []string{"source-1"}

	// 模型在来源集合里塞了一条没被授权的来源。
	err := validateRewriteOutput(SelectedRewriteOutput{
		ReplacementMarkdown: "改写后的正文 [[source:source-1]]",
		SourceIDs:           []string{"source-1", "invented"},
	}, requested)
	require.ErrorIs(t, err, ErrRewriteSourceDenied, "模型编造的来源不得被授权放行")

	// 正文里的来源标记与来源集合对不上，同样拒。
	err = validateRewriteOutput(SelectedRewriteOutput{
		ReplacementMarkdown: "改写后的正文 [[source:invented]]",
		SourceIDs:           []string{"source-1"},
	}, requested)
	require.ErrorIs(t, err, ErrRewriteInvalidRequest, "正文里的来源标记必须与来源集合一致")

	// 只引用被授权的那一条，通过。
	err = validateRewriteOutput(SelectedRewriteOutput{
		ReplacementMarkdown: "改写后的正文 [[source:source-1]]",
		SourceIDs:           []string{"source-1"},
	}, requested)
	require.NoError(t, err)
}
