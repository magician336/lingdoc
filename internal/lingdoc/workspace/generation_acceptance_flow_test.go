package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type acceptanceModelServiceStub struct {
	interfaces.ModelService
	models    []*types.Model
	chatModel chat.Chat
	selected  string
}

func (s *acceptanceModelServiceStub) ListModels(context.Context) ([]*types.Model, error) {
	return s.models, nil
}

func (s *acceptanceModelServiceStub) GetChatModel(_ context.Context, id string) (chat.Chat, error) {
	s.selected = id
	return s.chatModel, nil
}

type acceptanceChatStub struct {
	response *types.ChatResponse
	err      error
	options  *chat.ChatOptions
	messages []chat.Message
}

func (s *acceptanceChatStub) Chat(_ context.Context, messages []chat.Message, options *chat.ChatOptions) (*types.ChatResponse, error) {
	s.messages = messages
	s.options = options
	return s.response, s.err
}

func (*acceptanceChatStub) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (*acceptanceChatStub) GetModelName() string { return generationAcceptanceModel }
func (*acceptanceChatStub) GetModelID() string   { return "model-1" }

func TestGenerationHostModelUsesAcceptanceBudgetAndExplicitDeepSeekOptions(t *testing.T) {
	responseBody := `{"body_markdown":"验证结论 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`
	model := &acceptanceChatStub{response: &types.ChatResponse{
		Content:      responseBody,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 128, CompletionTokens: 64, TotalTokens: 192},
	}}
	service := &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}
	budget := &generationAcceptanceBudget{ledgerPath: t.TempDir() + "/round.json"}
	host := generationHostModel{models: service, acceptanceBudget: budget}

	draft, err := host.Generate(context.Background(), acceptanceGenerationInput())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if service.selected != "model-1" {
		t.Fatalf("selected model ID = %q, want exact configured row", service.selected)
	}
	if model.options == nil || model.options.MaxCompletionTokens != generationAcceptanceOutputMax || model.options.Thinking == nil || *model.options.Thinking {
		t.Fatalf("model options = %+v, want 6000 output and thinking explicitly disabled", model.options)
	}
	if len(model.messages) != 2 || len(draft.Sources) != 1 || draft.Sources[0].ID != "source-1" {
		t.Fatalf("messages=%d draft sources=%+v", len(model.messages), draft.Sources)
	}
	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil || len(ledger.Requests) != 1 || ledger.Requests[0].Status != "response_received" || ledger.Requests[0].InputTokens != 128 || ledger.Requests[0].OutputTokens != 64 {
		t.Fatalf("recorded request=%+v error=%v", ledger.Requests, err)
	}
}

func TestGenerationHostModelMarksLengthFinishReasonIncomplete(t *testing.T) {
	model := &acceptanceChatStub{response: &types.ChatResponse{
		Content:      `{"body_markdown":"partial`,
		FinishReason: "length",
		Usage:        types.TokenUsage{PromptTokens: 128, CompletionTokens: generationAcceptanceOutputMax, TotalTokens: 6128},
	}}
	service := &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}
	budget := &generationAcceptanceBudget{ledgerPath: t.TempDir() + "/round.json"}
	host := generationHostModel{models: service, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); !errors.Is(err, generation.ErrGenerationIncomplete) {
		t.Fatalf("Generate() error = %v, want incomplete output", err)
	}
	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil || len(ledger.Requests) != 1 || ledger.Requests[0].Status != "incomplete" || ledger.Blocked {
		t.Fatalf("incomplete response ledger=%+v error=%v", ledger, err)
	}
}

func acceptanceDeepSeekModel() *types.Model {
	return &types.Model{
		ID: "model-1", Name: generationAcceptanceModel,
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
		Parameters: types.ModelParameters{
			Provider: "generic", BaseURL: "https://api.deepseek.com",
			ExtraConfig: map[string]string{chat.ExtraConfigThinkingControl: "thinking_type"},
		},
	}
}

func acceptanceGenerationInput() generation.Input {
	return generation.Input{
		Actor: generation.Actor{TenantID: 1, UserID: "synthetic-acceptance-user"},
		Workspace: candidateadoption.GenerationContext{
			ProjectID: "synthetic-project", ChapterID: "synthetic-chapter", SpecRevision: 1,
			Chapter: candidateadoption.Chapter{ID: "synthetic-chapter", ProjectID: "synthetic-project", SectionID: "question", Title: "合成研究问题"},
		},
		Basis:       candidateadoption.Basis{TemplateID: "template-demo", TemplateVersion: "1", SpecRevision: 1},
		ProjectSpec: map[string]string{"research_subject": "合成课题", "research_goal": "验证单轮预算"},
		Sources: []generation.Source{{ID: "source-1", ProjectID: "synthetic-project", AssetID: "synthetic-asset", AssetRevision: 1,
			Locator: "page:1", QuotedText: "本轮合成资料只用于验证模型生成和引用回传。"}},
		Request: generation.Request{ChapterID: "synthetic-chapter", Instruction: "依据合成资料写一段短研究问题"},
	}
}
