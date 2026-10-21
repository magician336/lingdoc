package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// fixedRewriteModel 回一个写定的模型输出。
type fixedRewriteModel struct {
	inheritedRewriteModel
	output SelectedRewriteOutput
}

func (m *fixedRewriteModel) RewriteSelected(_ context.Context, _ Actor, p SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	m.calls++
	m.prompt = p
	return m.output, nil
}

// TestUnsupportedReferenceBecomesAReviewItem 覆盖 A26 的前半：模型给不出
// 支持的引用时，它把问题列成待核事项，候选照样可用。
//
// 「不支持的引用」不是编造：模型照实说「这一条我没有依据」，比它硬编一个
// 来源要诚实得多。
func TestUnsupportedReferenceBecomesAReviewItem(t *testing.T) {
	model := &fixedRewriteModel{output: SelectedRewriteOutput{
		ReplacementMarkdown: "有据可依的结论 [[source:existing]]",
		SourceIDs:           []string{"existing"},
		ReviewItems:         []string{"第二段的统计口径待核"},
		RunMode:             "mock",
	}}
	service, _, _, input := revocationRewriteFixture(t, model)

	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "unsupported-reference", input)
	require.NoError(t, err)
	require.Equal(t, "ready", candidate.Status)
	require.Len(t, candidate.ReviewItems, 1)
	require.Equal(t, "第二段的统计口径待核", candidate.ReviewItems[0].Statement)
	require.Equal(t, candidate.CandidateID, candidate.ReviewItems[0].OriginCandidateID)
}

// TestFabricatedSourceIsRejected 覆盖 A26 的后半：模型编造的来源被拒。
//
// 编造的来源不进候选：它没有授权，也没有快照，正文里却会留下一个看起来
// 有据可依的标记。
func TestFabricatedSourceIsRejected(t *testing.T) {
	model := &fixedRewriteModel{output: SelectedRewriteOutput{
		ReplacementMarkdown: "结论 [[source:invented]]",
		SourceIDs:           []string{"invented"},
		RunMode:             "mock",
	}}
	service, _, _, input := revocationRewriteFixture(t, model)

	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "fabricated-source", input)
	require.NoError(t, err)
	require.Equal(t, "failed", candidate.Status)
	require.Equal(t, "source_access_denied", candidate.ErrorCode)
	require.Empty(t, candidate.ReplacementMarkdown)
	require.NotContains(t, candidate.SourceIDs, "invented", "编造的来源不得进候选")
}

// TestUnsupportedSourceMarkerFormatIsRejected 覆盖 A26 的格式侧：来源标记
// 写成语法不支持的样子，候选失败而不是把这段正文发出去。
func TestUnsupportedSourceMarkerFormatIsRejected(t *testing.T) {
	model := &fixedRewriteModel{output: SelectedRewriteOutput{
		ReplacementMarkdown: "结论 [[source:a.b]]",
		SourceIDs:           []string{"existing"},
		RunMode:             "mock",
	}}
	service, _, _, input := revocationRewriteFixture(t, model)

	candidate, err := service.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"},
		"project", "chapter", "unsupported-marker", input)
	require.NoError(t, err)
	require.Equal(t, "failed", candidate.Status)
	require.Equal(t, "rewrite_failed", candidate.ErrorCode)
	require.Empty(t, candidate.ReplacementMarkdown)
}
