package workspace

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/stretchr/testify/require"
)

type formatRewriteModel struct{ text string }

func (*formatRewriteModel) RunMode() string { return "mock" }
func (m *formatRewriteModel) RewriteSelected(context.Context, Actor, SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	return SelectedRewriteOutput{ReplacementMarkdown: m.text, SourceIDs: []string{"existing"}, RunMode: "mock"}, nil
}

func TestSelectedRewriteExportBoundaryBeforeReady(t *testing.T) {
	for _, tc := range []struct{ name, text, validity, code string }{
		{"plain_with_citation", "研究问题：保留事实 [[source:existing]]\n\n限制：尚未实施。", string(candidateadoption.ValidityFresh), ""},
		{"bold_label", "**研究问题**：保留事实 [[source:existing]]", string(candidateadoption.ValidityFresh), ""},
		{"list", "- 保留事实 [[source:existing]]", string(candidateadoption.ValidityFresh), ""},
		{"blockquote", "> 保留事实 [[source:existing]]", "", "unsupported_format"},
		{"code", "`代码` [[source:existing]]", "", "unsupported_format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w, _, _, input := inheritedRewriteFixture(t)
			before := w.copy
			s.model = &formatRewriteModel{text: tc.text}
			candidate, err := s.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", "format-key", input)
			require.NoError(t, err)
			require.Equal(t, tc.validity, string(candidate.Validity))
			require.Equal(t, tc.code, candidate.ErrorCode)
			require.Equal(t, before, w.copy, "candidate generation must not change the working copy")
			if tc.validity == "" {
				require.Empty(t, candidate.ReplacementMarkdown)
			} else {
				require.Equal(t, tc.text, candidate.ReplacementMarkdown)
			}
		})
	}
}
