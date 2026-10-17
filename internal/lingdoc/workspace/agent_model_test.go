package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"github.com/Tencent/WeKnora/internal/types"
)

func g2TestModelContext() core.AgentContextPackage {
	return core.AgentContextPackage{ProjectID: "synthetic-project", ProjectVersion: 1, ContextRevision: 1, AuthorizationState: "allowed", SourceRefs: []core.AgentSourceRef{}, CorrelationID: "synthetic-agent-correlation"}
}
func TestG2HostAgentUsesExistingChatAndStructuredCandidateOutput(t *testing.T) {
	model := &acceptanceChatStub{response: &types.ChatResponse{Content: `{"reply":"这是一条待核建议","task_card":{"goal":"合成调研","queries":["定义","限制"]}}`, FinishReason: "stop", Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 30}}}
	host := &G2HostAgentModel{models: &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}}
	reply, err := host.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, core.G2ModelRequest{Agent: "research_agent", Purpose: "conversation", Context: g2TestModelContext(), History: []core.AgentMessage{{Role: "user", Content: "上一轮问题"}, {Role: "assistant", Content: "上一轮候选"}}, UserInput: "给出可编辑检索计划"})
	if err != nil || reply.Content != "这是一条待核建议" || !json.Valid(reply.StructuredOutput) || reply.ModelID == "" {
		t.Fatalf("candidate reply not parsed: %+v %v", reply, err)
	}
	if len(model.messages) != 5 || model.options.MaxCompletionTokens != 2048 {
		t.Fatalf("unexpected host call: messages=%d options=%+v", len(model.messages), model.options)
	}
	if !strings.Contains(model.messages[0].Content, "禁止编造") || strings.Contains(model.messages[0].Content, "已核实") {
		t.Fatal("research boundary omitted")
	}
}
func TestG2HostAgentRejectsMissingConsentBeforeChat(t *testing.T) {
	model := &acceptanceChatStub{}
	host := &G2HostAgentModel{models: &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}}
	pack := g2TestModelContext()
	pack.SourceRefs = []core.AgentSourceRef{{SourceID: "source", QuotedTextHash: "hash", ModelUseAllowed: false}}
	if _, err := host.Reply(context.Background(), core.Actor{TenantID: 1, UserID: "owner"}, core.G2ModelRequest{Agent: "task_assistant", Context: pack}); !errors.Is(err, core.ErrG2ConsentRequired) {
		t.Fatalf("unconsented source: %v", err)
	}
	if model.messages != nil {
		t.Fatal("provider called before consent")
	}
}
func TestG2HostAgentTimeoutOutcomeBlocksSharedLedgerWithoutRetry(t *testing.T) {
	model := &acceptanceChatStub{response: &types.ChatResponse{Content: `{"reply":"合成响应"}`, Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 0}}}
	path := filepath.Join(t.TempDir(), "agent-ledger.json")
	host := &G2HostAgentModel{models: &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}, budget: &generationAcceptanceBudget{ledgerPath: path}}
	request := core.G2ModelRequest{Agent: "task_assistant", Purpose: "conversation", Context: g2TestModelContext(), UserInput: "解释当前任务"}
	if _, err := host.Reply(context.Background(), core.Actor{TenantID: 1, UserID: "owner"}, request); !errors.Is(err, core.ErrG2OutcomeUnknown) {
		t.Fatalf("unknown usage: %v", err)
	}
	model.messages = nil
	if _, err := host.Reply(context.Background(), core.Actor{TenantID: 1, UserID: "owner"}, request); !errors.Is(err, generation.ErrAcceptanceOutcomeUnknown) {
		t.Fatalf("unknown outcome was retried: %v", err)
	}
	if model.messages != nil {
		t.Fatal("blocked ledger allowed a second chat")
	}
}
func TestG2RuleSuggestionUsesSharedEmbeddingAndGenerationBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared-agent-ledger.json")
	shared := acceptancebudget.New(generationAcceptanceProfile, path+".calls.json")
	for i := 0; i < 5; i++ {
		reservation, err := shared.Reserve(context.Background(), acceptancebudget.QwenEmbedding, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err = reservation.Complete("response_received", 50, 0, "", false); err != nil {
			t.Fatal(err)
		}
	}
	model := &acceptanceChatStub{response: &types.ChatResponse{Content: `{"suggestions":[],"basis":{"template_version":"v1"}}`, FinishReason: "stop", Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 30}}}
	host := &G2HostAgentModel{models: &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}, budget: &generationAcceptanceBudget{ledgerPath: path, total: shared}}
	request := core.G2ModelRequest{Agent: "task_assistant", Purpose: "rule_suggestion", Context: g2TestModelContext(), StructuredInput: json.RawMessage(`{"template_version":"v1"}`), UserInput: "给出规则候选"}
	reply, err := host.Reply(context.Background(), core.Actor{TenantID: 1, UserID: "owner"}, request)
	if err != nil || !json.Valid(reply.StructuredOutput) {
		t.Fatalf("rule suggestion: %+v %v", reply, err)
	}
	model.messages = nil
	if _, err = host.Reply(context.Background(), core.Actor{TenantID: 1, UserID: "owner"}, request); !errors.Is(err, generation.ErrAcceptanceBudgetExceeded) {
		t.Fatalf("shared seventh call: %v", err)
	}
	if model.messages != nil {
		t.Fatal("G4/G2 bypassed shared call limit")
	}
}
func TestG2HostRejectsTruncatedOrNonObjectOutput(t *testing.T) {
	for _, payload := range []string{`[]`, `{"reply":"候选"} {"reply":"尾随"}`, `{"missing_reply":true}`} {
		model := &acceptanceChatStub{response: &types.ChatResponse{Content: payload, FinishReason: "stop"}}
		host := &G2HostAgentModel{models: &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}}
		if _, err := host.Reply(context.Background(), core.Actor{TenantID: 1, UserID: "owner"}, core.G2ModelRequest{Agent: "task_assistant", Purpose: "conversation", Context: g2TestModelContext()}); err == nil {
			t.Fatalf("invalid output accepted: %s", payload)
		}
	}
}
