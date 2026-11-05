package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func sourceAnswerTestRequest() core.G2ModelRequest {
	pack := g2TestModelContext()
	pack.SourceRefs = []core.AgentSourceRef{{SourceID: "source-1", QuotedTextHash: "fixture-hash", QuotedText: "测量显示：小组平均完成时间为十分钟。下一轮需要复核。", ModelUseAllowed: true}}
	return core.G2ModelRequest{Agent: "research_agent", Purpose: "conversation", Context: pack, UserInput: "测量结果是什么？"}
}

func TestSourceAnswerValidatesExactSelectedExcerpt(t *testing.T) {
	sources := sourceAnswerTestRequest().Context.SourceRefs
	answer, err := validateSourceAnswer(`{"reply":"平均完成时间为十分钟[1]。","citations":[{"source_id":"source-1","quote":"平均完成时间为十分钟"}]}`, sources)
	require.NoError(t, err)
	require.Equal(t, "平均完成时间为十分钟", answer.Citations[0].Quote)
	for _, raw := range []string{
		`{"reply":"回答[1]","citations":[{"source_id":"not-selected","quote":"平均完成时间为十分钟"}]}`,
		`{"reply":"回答[1]","citations":[{"source_id":"source-1","quote":"平均完成时间为五分钟"}]}`,
		`{"reply":"回答[1]","citations":[{"source_id":"source-1","quote":"测量显示……需要复核"}]}`,
		`{"reply":"回答[1]","citations":[{"source_id":"source-1","quote":" "}]}`,
		`{"reply":"回答","citations":[],"change_draft":{}}`,
		`{"reply":"回答","citations":[{"source_id":"source-1","quote":"十分钟","url":"https://invented.invalid"}]}`,
		`{"reply":"无引用字段"}`,
		`{"reply":"回答","citations":null}`,
		`{"reply":"回答","citations":[]} {"extra":true}`,
	} {
		_, err = validateSourceAnswer(raw, sources)
		require.ErrorIs(t, err, core.ErrInvalidRequest, raw)
	}
	answer, err = validateSourceAnswer(`{"reply":"无来源也能确定所有结论。","citations":[]}`, sources)
	require.NoError(t, err)
	require.Equal(t, insufficientSourceAnswer, answer.Reply)
	require.Empty(t, answer.Citations)
}

func TestSourceAnswerLimitsAndConsentRejectBeforeProvider(t *testing.T) {
	tests := map[string]func(*core.G2ModelRequest){
		"unconsented": func(r *core.G2ModelRequest) { r.Context.SourceRefs[0].ModelUseAllowed = false },
		"too many": func(r *core.G2ModelRequest) {
			ref := r.Context.SourceRefs[0]
			r.Context.SourceRefs = []core.AgentSourceRef{ref, ref, ref, ref, ref}
		},
		"long source":    func(r *core.G2ModelRequest) { r.Context.SourceRefs[0].QuotedText = strings.Repeat("文", 12001) },
		"long question":  func(r *core.G2ModelRequest) { r.UserInput = strings.Repeat("问", 2001) },
		"no source":      func(r *core.G2ModelRequest) { r.Context.SourceRefs = nil },
		"no source text": func(r *core.G2ModelRequest) { r.Context.SourceRefs[0].QuotedText = "" },
		"other agent":    func(r *core.G2ModelRequest) { r.Agent = "task_assistant" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			request := sourceAnswerTestRequest()
			change(&request)
			provider := &sourceAnswerRejectingChat{}
			models := &acceptanceModelServiceStub{chatModel: provider}
			_, err := NewG2SourceAnswerModel(models, "local-model").Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, request)
			require.Error(t, err)
			require.Zero(t, provider.calls)
		})
	}
}

type sourceAnswerRejectingChat struct {
	acceptanceChatStub
	calls     int
	remaining time.Duration
}

func (s *sourceAnswerRejectingChat) Chat(ctx context.Context, messages []chat.Message, options *chat.ChatOptions) (*types.ChatResponse, error) {
	s.calls++
	if deadline, ok := ctx.Deadline(); ok {
		s.remaining = time.Until(deadline)
	}
	return s.acceptanceChatStub.Chat(ctx, messages, options)
}

func TestSourceAnswerRejectsFabricationOnceWithBoundedProviderOptions(t *testing.T) {
	provider := &sourceAnswerRejectingChat{acceptanceChatStub: acceptanceChatStub{response: &types.ChatResponse{
		Content: `{"reply":"回答[1]","citations":[{"source_id":"invented","quote":"不存在"}]}`, FinishReason: "stop",
	}}}
	models := &acceptanceModelServiceStub{models: []*types.Model{
		{ID: "other-default", TenantID: 7, Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive, IsDefault: true},
		{ID: "local-model", TenantID: 7, Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
	}, chatModel: provider}
	request := sourceAnswerTestRequest()
	request.History = []core.AgentMessage{{Role: "user", Content: "旧资料里的敏感内容"}, {Role: "assistant", Content: "旧回答"}}
	_, err := NewG2SourceAnswerModel(models, "local-model").Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, request)
	require.ErrorIs(t, err, core.ErrInvalidRequest)
	require.Equal(t, 1, provider.calls)
	require.Equal(t, "local-model", models.selected)
	require.Len(t, provider.messages, 3)
	require.NotContains(t, provider.messages[1].Content, "旧资料")
	for _, privateField := range []string{"project_id", "template_id", "card_refs", "structured_input", "asset_id", "quoted_text_hash", "synthetic-project"} {
		require.NotContains(t, provider.messages[1].Content, privateField)
	}
	require.Equal(t, 2048, provider.options.MaxCompletionTokens)
	require.NotNil(t, provider.options.Thinking)
	require.False(t, *provider.options.Thinking)
	require.Greater(t, provider.remaining, 59*time.Second)
	require.LessOrEqual(t, provider.remaining, 60*time.Second)

	provider.response = nil
	provider.err = errors.New("synthetic-provider-credential-do-not-echo")
	_, err = NewG2SourceAnswerModel(models, "local-model").Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, request)
	require.ErrorIs(t, err, core.ErrG2Unavailable)
	require.NotContains(t, err.Error(), "synthetic-provider-credential")
	require.Equal(t, 2, provider.calls, "one call per explicit turn, no automatic retry")
}

func TestSourceAnswerDoesNotSelectForeignOrBuiltinModel(t *testing.T) {
	for _, candidate := range []*types.Model{
		{ID: "local-model", TenantID: 8, Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
		{ID: "local-model", TenantID: 7, IsBuiltin: true, Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
	} {
		provider := &sourceAnswerRejectingChat{}
		models := &acceptanceModelServiceStub{models: []*types.Model{candidate}, chatModel: provider}
		_, err := NewG2SourceAnswerModel(models, "local-model").Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, sourceAnswerTestRequest())
		require.ErrorIs(t, err, core.ErrG2Unavailable)
		require.Zero(t, provider.calls)
	}
}
