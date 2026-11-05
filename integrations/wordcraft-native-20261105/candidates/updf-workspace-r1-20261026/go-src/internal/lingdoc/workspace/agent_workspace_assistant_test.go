package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceAssistantChatsWithoutSourcesAndKeepsHistory(t *testing.T) {
	provider := &sourceAnswerRejectingChat{acceptanceChatStub: acceptanceChatStub{response: &types.ChatResponse{
		Content: `{"reply":"可以展开第二点。","citations":[]}`, FinishReason: "stop"}}}
	models := &acceptanceModelServiceStub{models: []*types.Model{{ID: "local-model", TenantID: 7,
		Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive}}, chatModel: provider}
	selection := &core.AgentDocumentSelection{DocumentID: 2, DocumentRevision: 3, Text: "正文选区",
		TextSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("正文选区"))), Locator: "正文选区"}
	request := core.G2ModelRequest{Agent: core.WorkspaceAssistantAgent, Purpose: "conversation", UserInput: "展开第二点",
		Context: core.AgentContextPackage{ProjectID: "p", AuthorizationState: "allowed", SourceRefs: []core.AgentSourceRef{}, DocumentSelection: selection},
		History: []core.AgentMessage{{Role: "user", Content: "列两点"}, {Role: "assistant", Content: "第一点。第二点。"}}}
	reply, err := NewG2LocalAgentModel(models, "local-model").Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, request)
	require.NoError(t, err)
	require.Equal(t, "可以展开第二点。", reply.Content)
	require.Len(t, provider.messages, 5)
	require.Equal(t, "列两点", provider.messages[2].Content)
	require.Contains(t, provider.messages[1].Content, "document_selection")
	require.NotContains(t, provider.messages[1].Content, "template_id")
	require.Contains(t, provider.messages[0].Content, "source_id 必须逐字复制")
	require.Contains(t, provider.messages[0].Content, "保留其中的换行和空白")
	require.NotContains(t, provider.messages[0].Content, "可选来源ID")
}

func TestWorkspaceAssistantValidatesOptionalCitations(t *testing.T) {
	sources := sourceAnswerTestRequest().Context.SourceRefs
	answer, err := validateWorkspaceAssistantResponse(`{"reply":"普通回答","citations":[]}`, sources)
	require.NoError(t, err)
	require.Equal(t, "普通回答", answer.Reply)
	require.NotEqual(t, insufficientSourceAnswer, answer.Reply)

	_, err = validateWorkspaceAssistantResponse(`{"reply":"回答","citations":[{"source_id":"invented","quote":"不存在"}]}`, sources)
	require.ErrorIs(t, err, core.ErrInvalidRequest)
	_, err = validateWorkspaceAssistantResponse(`{"reply":"回答","citations":null} {"trailing":true}`, sources)
	require.ErrorIs(t, err, core.ErrInvalidRequest)
}

func TestWorkspaceAssistantRejectsInvalidInputBeforeProvider(t *testing.T) {
	provider := &sourceAnswerRejectingChat{}
	models := &acceptanceModelServiceStub{models: []*types.Model{{ID: "local-model", TenantID: 7,
		Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive}}, chatModel: provider}
	request := core.G2ModelRequest{Agent: core.WorkspaceAssistantAgent, Purpose: "conversation", UserInput: strings.Repeat("问", 4001),
		Context: core.AgentContextPackage{ProjectID: "p", AuthorizationState: "allowed", SourceRefs: []core.AgentSourceRef{}}}
	_, err := NewG2LocalAgentModel(models, "local-model").Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, request)
	require.ErrorIs(t, err, core.ErrInvalidRequest)
	require.Zero(t, provider.calls)
}

func TestWorkspaceAssistantCitationRemainsStrictAfterPromptClarification(t *testing.T) {
	sources := []core.AgentSourceRef{{SourceID: "source-exact-id", QuotedText: "第一行。\n第二行。"}}
	valid := `{"reply":"说明","citations":[{"source_id":"source-exact-id","quote":"第一行。\n第二行。"}]}`
	answer, err := validateWorkspaceAssistantResponse(valid, sources)
	require.NoError(t, err)
	require.Equal(t, "第一行。\n第二行。", answer.Citations[0].Quote)
	for _, raw := range []string{
		`{"reply":"说明","citations":[{"source_id":"资料名称.pdf","quote":"第一行。"}]}`,
		`{"reply":"说明","citations":[{"source_id":"source-exact-id","quote":"第一行。第二行。"}]}`,
		`{"reply":"说明","citations":[{"source_id":"source-exact-id","quote":"第一行。 第二行。"}]}`,
		`{"reply":"说明","citations":[{"source_id":"source-exact-id","quote":"第一行。改写第二行。"}]}`,
		`{"reply":"说明","citations":[],"untrusted_action":"write"}`,
	} {
		_, err := validateWorkspaceAssistantResponse(raw, sources)
		require.ErrorIs(t, err, core.ErrInvalidRequest)
	}
}
