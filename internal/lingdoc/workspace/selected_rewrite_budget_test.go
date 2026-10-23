package workspace

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"github.com/Tencent/WeKnora/internal/types"
	"strings"
	"testing"
)

func TestRewriteSharesEmbeddingBudgetAndStopsBeforeSeventhCall(t *testing.T) {
	path := t.TempDir() + "/round.json"
	shared := acceptancebudget.New(generationAcceptanceProfile, path+".calls.json")
	for i := 0; i < 5; i++ {
		r, err := shared.Reserve(context.Background(), acceptancebudget.QwenEmbedding, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err = r.Complete("response_received", 50, 0, "", false); err != nil {
			t.Fatal(err)
		}
	}
	model := &acceptanceChatStub{response: &types.ChatResponse{Content: `{"replacement_markdown":"保留事实","source_ids":[],"review_items":[]}`, FinishReason: "stop", Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 30}}}
	service := &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}
	host := selectedRewriteHostModel{models: service, acceptanceBudget: &generationAcceptanceBudget{ledgerPath: path, total: shared}}
	if _, err := host.RewriteSelected(context.Background(), Actor{TenantID: 1, UserID: "test"}, SelectedRewritePrompt{Instruction: "仅润色"}); err != nil {
		t.Fatal(err)
	}
	// The request carries the profile's output ceiling -- the same number
	// the ledger records as output_limit_tokens. The adapter used to ask for
	// a hard-coded 2048 instead, so the ledger described a call that was
	// never made (#542).
	if model.options.MaxCompletionTokens != acceptancebudget.OutputMax || model.options.Thinking == nil || *model.options.Thinking {
		t.Fatal("output/thinking limits not applied")
	}
	model.messages = nil
	if _, err := host.RewriteSelected(context.Background(), Actor{TenantID: 1, UserID: "test"}, SelectedRewritePrompt{}); !errors.Is(err, generation.ErrAcceptanceBudgetExceeded) {
		t.Fatalf("seventh: %v", err)
	}
	if model.messages != nil {
		t.Fatal("provider called beyond shared limit")
	}
}
func TestRewriteRejectsOversizeAndUnknownUsage(t *testing.T) {
	model := &acceptanceChatStub{response: &types.ChatResponse{Content: `{"replacement_markdown":"有效输出","source_ids":[],"review_items":[]}`, Usage: types.TokenUsage{PromptTokens: 100}}}
	service := &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}
	host := selectedRewriteHostModel{models: service, acceptanceBudget: &generationAcceptanceBudget{ledgerPath: t.TempDir() + "/round.json"}}
	actor := Actor{TenantID: 1, UserID: "test"}
	if _, err := host.RewriteSelected(context.Background(), actor, SelectedRewritePrompt{Instruction: strings.Repeat("x", 16001)}); !errors.Is(err, generation.ErrAcceptanceBudgetExceeded) {
		t.Fatalf("input: %v", err)
	}
	if model.messages != nil {
		t.Fatal("oversize called provider")
	}
	if _, err := host.RewriteSelected(context.Background(), actor, SelectedRewritePrompt{}); !errors.Is(err, generation.ErrAcceptanceOutcomeUnknown) {
		t.Fatalf("usage: %v", err)
	}
	model.messages = nil
	if _, err := host.RewriteSelected(context.Background(), actor, SelectedRewritePrompt{}); !errors.Is(err, generation.ErrAcceptanceOutcomeUnknown) {
		t.Fatalf("blocked: %v", err)
	}
	if model.messages != nil {
		t.Fatal("unknown usage allowed another call")
	}
}
func TestRewriteInvalidProfileFailsClosed(t *testing.T) {
	t.Setenv(generationAcceptanceProfileEnv, "invalid")
	_, err := NewSelectedRewriteHostModel(nil).RewriteSelected(context.Background(), Actor{}, SelectedRewritePrompt{})
	if !errors.Is(err, generation.ErrAcceptanceBudgetUnavailable) {
		t.Fatal(err)
	}
}
