package workspacecore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func workspaceHistoryTurn(id, input, reply string, selection *AgentDocumentSelection, sources []AgentSourceRef) AgentTurn {
	return AgentTurn{ID: id, Status: "completed", UserInput: input, Reply: reply,
		Context: AgentContextPackage{SourceRefs: sources, DocumentSelection: selection}}
}

func TestBuildWorkspaceAssistantHistory(t *testing.T) {
	selection := validSelection()
	source := AgentSourceRef{SourceID: "source-1", AssetRevision: 1, QuotedTextHash: "hash", ModelUseAllowed: true}
	turns := []AgentTurn{
		workspaceHistoryTurn("plain", "先列两点", "第一点。第二点。", nil, nil),
		workspaceHistoryTurn("selected", "解释选区", "选区解释", selection, nil),
		workspaceHistoryTurn("old-selection", "旧选区", "旧回答", &AgentDocumentSelection{TextSHA256: "old"}, nil),
		workspaceHistoryTurn("old-source", "旧资料", "旧回答", nil, []AgentSourceRef{source}),
		{ID: "running", Status: "running", UserInput: "未完成", Context: AgentContextPackage{SourceRefs: []AgentSourceRef{}}},
	}
	messages, usage := BuildWorkspaceAssistantHistory(turns, AgentContextPackage{SourceRefs: []AgentSourceRef{}, DocumentSelection: selection})
	require.Len(t, messages, 4)
	require.Equal(t, "解释选区", messages[2].Content)
	require.Equal(t, 2, usage.IncludedTurns)
	require.Equal(t, 2, usage.ExcludedTurns)
	require.Equal(t, 1, usage.Reasons["selection_changed"])
	require.Equal(t, 1, usage.Reasons["sources_changed"])
}

func TestWorkspaceHistoryCapsWholeTurnsAndCharacters(t *testing.T) {
	turns := []AgentTurn{}
	for i := 0; i < 10; i++ {
		turns = append(turns, workspaceHistoryTurn(fmt.Sprint(i), "问", "答", nil, nil))
	}
	messages, usage := BuildWorkspaceAssistantHistory(turns, AgentContextPackage{SourceRefs: []AgentSourceRef{}})
	require.Len(t, messages, 16)
	require.Equal(t, 8, usage.IncludedTurns)
	require.Equal(t, 2, usage.TruncatedTurns)

	long := []AgentTurn{
		workspaceHistoryTurn("old", strings.Repeat("旧", 12001), "答", nil, nil),
		workspaceHistoryTurn("new", "新", "答", nil, nil),
	}
	messages, usage = BuildWorkspaceAssistantHistory(long, AgentContextPackage{SourceRefs: []AgentSourceRef{}})
	require.Len(t, messages, 2)
	require.Equal(t, "新", messages[0].Content)
	require.Equal(t, 1, usage.TruncatedTurns)
}

func TestWithAgentHistoryUsage(t *testing.T) {
	raw := WithAgentHistoryUsage(json.RawMessage(`{"reply":"回答","citations":[]}`), AgentHistoryUsage{IncludedTurns: 1, Reasons: map[string]int{}})
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &object))
	require.Contains(t, object, "history_usage")
}

func TestWorkspaceAssistantSendTurnUsesCompatibleHistory(t *testing.T) {
	f := newG2Fixture(t, false)
	raw, _, _, err := f.svc.CreateAgentSession(context.Background(), f.owner, f.project.ID,
		WorkspaceAssistantAgent, "workspace-history-session", f.expectation(0))
	session := g2Decode[AgentSession](t, raw, err)
	input := AgentTurnInput{AgentContextInput: AgentContextInput{G2Expectation: f.expectation(0), SourceIDs: []string{}, SensitiveContextConfirmed: true}, UserInput: "先列两点"}
	_, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, "workspace-turn-1", input)
	require.NoError(t, err)
	input.UserInput = "展开第二点"
	raw, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, "workspace-turn-2", input)
	turn := g2Decode[AgentTurn](t, raw, err)
	require.Equal(t, "completed", turn.Status)
	require.Len(t, f.model.requests[1].History, 2)
	require.Equal(t, "先列两点", f.model.requests[1].History[0].Content)
	var suggestions map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(turn.Suggestions, &suggestions))
	require.Contains(t, suggestions, "history_usage")
}

func TestWorkspaceHistoryDistinguishesSourceExcerpts(t *testing.T) {
	for _, agent := range []string{WorkspaceAssistantAgent, "task_assistant"} {
		t.Run(agent, func(t *testing.T) {
			f := newG2Fixture(t, false)
			source := setAgentSourceText(f, "source-1", "学生不是没有时间，而是不知道怎样开始")
			raw, _, _, err := f.svc.CreateAgentSession(context.Background(), f.owner, f.project.ID,
				agent, "workspace-history-excerpt-session", f.expectation(0))
			session := g2Decode[AgentSession](t, raw, err)
			input := AgentTurnInput{AgentContextInput: AgentContextInput{
				G2Expectation: f.expectation(0), SensitiveContextConfirmed: true,
				SourceExcerpts: []AgentSourceExcerptInput{agentSourceExcerpt(source, 11, 14)},
			}, UserInput: "解释第一个选段"}
			raw, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, "workspace-excerpt-turn-1", input)
			first := g2Decode[AgentTurn](t, raw, err)
			require.Equal(t, "completed", first.Status)
			require.Len(t, first.Context.SourceRefs, 1)
			stored := first.Context.SourceRefs[0]
			require.Equal(t, g2Hash("不知道"), stored.ExcerptTextHash)
			require.NotNil(t, stored.SelectionStart)
			require.NotNil(t, stored.SelectionEnd)
			require.Equal(t, 11, *stored.SelectionStart)
			require.Equal(t, 14, *stored.SelectionEnd)
			require.Equal(t, source.QuotedTextHash, stored.QuotedTextHash)

			input.UserInput = "解释第二个选段"
			input.SourceExcerpts = []AgentSourceExcerptInput{agentSourceExcerpt(source, 14, 18)}
			_, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, "workspace-excerpt-turn-2", input)
			require.NoError(t, err)
			require.Empty(t, f.model.requests[1].History, "a different excerpt from the same source must not share history")

			input.UserInput = "继续第二个选段"
			raw, _, _, err = f.svc.SendAgentTurn(context.Background(), f.owner, f.project.ID, session.ID, "workspace-excerpt-turn-3", input)
			third := g2Decode[AgentTurn](t, raw, err)
			require.Equal(t, "completed", third.Status)
			require.Len(t, f.model.requests[2].History, 2)
			require.Equal(t, "解释第二个选段", f.model.requests[2].History[0].Content)
		})
	}
}
