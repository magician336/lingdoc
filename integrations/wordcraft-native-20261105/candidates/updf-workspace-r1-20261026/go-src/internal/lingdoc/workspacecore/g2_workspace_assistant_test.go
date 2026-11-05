package workspacecore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func validSelection() *AgentDocumentSelection {
	text := "需要讨论的正文"
	return &AgentDocumentSelection{
		DocumentID: 2, DocumentRevision: 7, Text: text,
		TextSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(text))), Locator: "正文选区",
	}
}

func TestValidateAgentDocumentSelection(t *testing.T) {
	require.NoError(t, ValidateAgentDocumentSelection(nil))
	require.NoError(t, ValidateAgentDocumentSelection(validSelection()))

	changed := validSelection()
	changed.Text += "被篡改"
	require.ErrorIs(t, ValidateAgentDocumentSelection(changed), ErrInvalidRequest)

	empty := validSelection()
	empty.Text = " "
	require.ErrorIs(t, ValidateAgentDocumentSelection(empty), ErrInvalidRequest)

	long := validSelection()
	long.Text = strings.Repeat("文", 6001)
	long.TextSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(long.Text)))
	require.ErrorIs(t, ValidateAgentDocumentSelection(long), ErrInvalidRequest)
}

func TestValidateAgentDocumentSelectionRejectsIncompleteRange(t *testing.T) {
	value := validSelection()
	value.SelectionRange = &AgentDocumentRange{Anchor: json.RawMessage(`{"off":3}`)}
	require.ErrorIs(t, ValidateAgentDocumentSelection(value), ErrInvalidRequest)
}

func TestWorkspaceAssistantSessionAndSelectionContext(t *testing.T) {
	f := newG2Fixture(t, false)
	raw, _, _, err := f.svc.CreateAgentSession(context.Background(), f.owner, f.project.ID,
		WorkspaceAssistantAgent, "workspace-session", f.expectation(0))
	session := g2Decode[AgentSession](t, raw, err)
	require.Equal(t, WorkspaceAssistantAgent, session.Agent)

	pack, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{
		G2Expectation: f.expectation(0), DocumentSelection: validSelection(), SensitiveContextConfirmed: true,
	})
	require.NoError(t, err)
	require.NotNil(t, pack.DocumentSelection)
	require.Equal(t, "需要讨论的正文", pack.DocumentSelection.Text)
}

func setAgentSourceText(f g2Fixture, sourceID, text string) G2Source {
	source := f.source.sources[sourceID]
	source.SourceID = sourceID
	source.QuotedText = text
	source.QuotedTextHash = g2Hash(text)
	f.source.sources[sourceID] = source
	return source
}

func agentSourceExcerpt(source G2Source, start, end int) AgentSourceExcerptInput {
	quoted := string([]rune(source.QuotedText)[start:end])
	return AgentSourceExcerptInput{
		SourceID: source.SourceID, SelectionStart: start, SelectionEnd: end,
		QuotedText: quoted, ExcerptTextHash: g2Hash(quoted), SourceQuotedTextHash: source.QuotedTextHash,
	}
}

func TestBuildAgentContextUsesVerifiedExcerpt(t *testing.T) {
	f := newG2Fixture(t, false)
	full := "学生不是没有时间，而是不知道怎样开始"
	selected := "不知道怎样开始"
	setAgentSourceText(f, "source-1", full)
	pack, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{
		G2Expectation: f.expectation(0), SensitiveContextConfirmed: true,
		SourceExcerpts: []AgentSourceExcerptInput{{
			SourceID: "source-1", SelectionStart: 11, SelectionEnd: 18,
			QuotedText: selected, ExcerptTextHash: g2Hash(selected),
			SourceQuotedTextHash: g2Hash(full),
		}},
	})
	require.NoError(t, err)
	require.Len(t, pack.SourceRefs, 1)
	require.Equal(t, selected, pack.SourceRefs[0].QuotedText)
	require.Equal(t, g2Hash(full), pack.SourceRefs[0].QuotedTextHash)
	require.Equal(t, g2Hash(selected), pack.SourceRefs[0].ExcerptTextHash)
	require.NotNil(t, pack.SourceRefs[0].SelectionStart)
	require.NotNil(t, pack.SourceRefs[0].SelectionEnd)
	require.Equal(t, 11, *pack.SourceRefs[0].SelectionStart)
	require.Equal(t, 18, *pack.SourceRefs[0].SelectionEnd)
}

func TestBuildAgentContextUsesVerifiedExcerptRejectsTampering(t *testing.T) {
	full := "学生不是没有时间，而是不知道怎样开始"
	tests := []struct {
		name   string
		mutate func(*AgentSourceExcerptInput)
		want   error
	}{
		{name: "range", mutate: func(in *AgentSourceExcerptInput) { in.SelectionEnd = 19 }, want: ErrInvalidRequest},
		{name: "quoted text", mutate: func(in *AgentSourceExcerptInput) { in.QuotedText += "篡改" }, want: ErrInvalidRequest},
		{name: "excerpt hash", mutate: func(in *AgentSourceExcerptInput) { in.ExcerptTextHash = g2Hash("篡改") }, want: ErrInvalidRequest},
		{name: "source hash", mutate: func(in *AgentSourceExcerptInput) { in.SourceQuotedTextHash = g2Hash("旧版本") }, want: ErrVersionConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newG2Fixture(t, false)
			source := setAgentSourceText(f, "source-1", full)
			in := agentSourceExcerpt(source, 11, 18)
			test.mutate(&in)
			_, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{
				G2Expectation: f.expectation(0), SensitiveContextConfirmed: true,
				SourceExcerpts: []AgentSourceExcerptInput{in},
			})
			require.ErrorIs(t, err, test.want)
		})
	}
}

func TestBuildAgentContextUsesVerifiedExcerptLimitsAndDuplicates(t *testing.T) {
	t.Run("more than four excerpts", func(t *testing.T) {
		f := newG2Fixture(t, false)
		excerpts := make([]AgentSourceExcerptInput, 0, 5)
		for i := 1; i <= 5; i++ {
			sourceID := fmt.Sprintf("source-%d", i)
			source := setAgentSourceText(f, sourceID, "资料"+fmt.Sprint(i))
			excerpts = append(excerpts, agentSourceExcerpt(source, 0, 2))
		}
		_, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{
			G2Expectation: f.expectation(0), SensitiveContextConfirmed: true, SourceExcerpts: excerpts,
		})
		require.ErrorIs(t, err, ErrInvalidRequest)
	})

	t.Run("more than twelve thousand characters", func(t *testing.T) {
		f := newG2Fixture(t, false)
		excerpts := make([]AgentSourceExcerptInput, 0, 2)
		for _, sourceID := range []string{"source-1", "source-2"} {
			source := setAgentSourceText(f, sourceID, strings.Repeat("文", 6001))
			excerpts = append(excerpts, agentSourceExcerpt(source, 0, 6001))
		}
		_, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{
			G2Expectation: f.expectation(0), SensitiveContextConfirmed: true, SourceExcerpts: excerpts,
		})
		require.ErrorIs(t, err, ErrInvalidRequest)
	})

	t.Run("duplicate full and excerpt source", func(t *testing.T) {
		f := newG2Fixture(t, false)
		source := setAgentSourceText(f, "source-1", "学生不是没有时间，而是不知道怎样开始")
		_, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{
			G2Expectation: f.expectation(0), SensitiveContextConfirmed: true,
			SourceIDs: []string{"source-1"}, SourceExcerpts: []AgentSourceExcerptInput{agentSourceExcerpt(source, 11, 18)},
		})
		require.ErrorIs(t, err, ErrInvalidRequest)
	})
}

func TestBuildAgentContextUsesVerifiedExcerptNormalizesSourceID(t *testing.T) {
	full := "学生不是没有时间，而是不知道怎样开始"

	t.Run("padded ID still sends only the excerpt", func(t *testing.T) {
		f := newG2Fixture(t, false)
		source := setAgentSourceText(f, "source-1", full)
		in := agentSourceExcerpt(source, 11, 18)
		in.SourceID = " \tsource-1\n"
		pack, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{
			G2Expectation: f.expectation(0), SensitiveContextConfirmed: true,
			SourceExcerpts: []AgentSourceExcerptInput{in},
		})
		require.NoError(t, err)
		require.Len(t, pack.SourceRefs, 1)
		require.Equal(t, "source-1", pack.SourceRefs[0].SourceID)
		require.Equal(t, "不知道怎样开始", pack.SourceRefs[0].QuotedText)
		require.Equal(t, g2Hash("不知道怎样开始"), pack.SourceRefs[0].ExcerptTextHash)
	})

	for _, sourceID := range []string{"", " \t\n"} {
		t.Run("reject empty or whitespace-only ID "+fmt.Sprintf("%q", sourceID), func(t *testing.T) {
			f := newG2Fixture(t, false)
			source := setAgentSourceText(f, "source-1", full)
			in := agentSourceExcerpt(source, 11, 18)
			in.SourceID = sourceID
			_, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{
				G2Expectation: f.expectation(0), SensitiveContextConfirmed: true,
				SourceExcerpts: []AgentSourceExcerptInput{in},
			})
			require.ErrorIs(t, err, ErrInvalidRequest)
		})
	}

	t.Run("reject whitespace-variant duplicate IDs", func(t *testing.T) {
		f := newG2Fixture(t, false)
		source := setAgentSourceText(f, "source-1", full)
		first := agentSourceExcerpt(source, 11, 14)
		second := agentSourceExcerpt(source, 14, 18)
		second.SourceID = " source-1\t"
		_, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{
			G2Expectation: f.expectation(0), SensitiveContextConfirmed: true,
			SourceExcerpts: []AgentSourceExcerptInput{first, second},
		})
		require.ErrorIs(t, err, ErrInvalidRequest)
	})
}

func TestBuildAgentContextUsesVerifiedExcerptKeepsLegacyFullSource(t *testing.T) {
	f := newG2Fixture(t, false)
	full := "学生不是没有时间，而是不知道怎样开始"
	setAgentSourceText(f, "source-1", full)
	pack, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{
		G2Expectation: f.expectation(0), SensitiveContextConfirmed: true, SourceIDs: []string{"source-1"},
	})
	require.NoError(t, err)
	require.Len(t, pack.SourceRefs, 1)
	require.Equal(t, full, pack.SourceRefs[0].QuotedText)
	require.Equal(t, g2Hash(full), pack.SourceRefs[0].QuotedTextHash)
	require.Empty(t, pack.SourceRefs[0].ExcerptTextHash)
	require.Nil(t, pack.SourceRefs[0].SelectionStart)
	require.Nil(t, pack.SourceRefs[0].SelectionEnd)
}
