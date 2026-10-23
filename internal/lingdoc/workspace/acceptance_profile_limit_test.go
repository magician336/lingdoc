package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"github.com/Tencent/WeKnora/internal/types"
)

// The tests below are offline. The chat client is a stub that records the
// options it was called with, so every assertion about "what the provider was
// asked for" is a fact about the stub and not an inference.
//
// What is being pinned down here is one invariant, seen from both sides:
//
//   - the completion budget the request carries is the ceiling the ACTIVE
//     PROFILE declares, and
//   - the JSON ledger records that same ceiling as output_limit_tokens.
//
// A ledger that records a ceiling the request never asked for cannot be
// checked against the request, and a request that asks for less than the
// ledger records makes the two numbers describe two different calls.

// acceptanceProfileRecordedOutputLimit reads the ceiling the JSON ledger
// recorded for its one request.
//
// It reads the raw JSON on purpose: the point of the assertion is the field
// a reader of the ledger file sees, not an in-memory copy of it.
func acceptanceProfileRecordedOutputLimit(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the ledger %s: %v", path, err)
	}
	var record struct {
		Requests []struct {
			OutputLimit int `json:"output_limit_tokens"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode the ledger %s: %v", path, err)
	}
	if len(record.Requests) != 1 {
		t.Fatalf("the ledger %s holds %d requests, want exactly 1", path, len(record.Requests))
	}
	return record.Requests[0].OutputLimit
}

// The agent turn asks the provider for the profile's ceiling, and the ledger
// records the same number.
func TestQwenProfileAgentTurnAsksForTheProfileCeilingAndTheLedgerRecordsIt(t *testing.T) {
	budget := qwenBudget(t)
	stub := &qwenCountingChat{response: &types.ChatResponse{
		Content:      `{"reply":"合成回复"}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 320, CompletionTokens: 128, TotalTokens: 448},
	}}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := &HostAgentModel{models: service, budget: budget}

	if _, err := host.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"},
		core.ModelRequest{Agent: core.AgentTaskAssistant, Purpose: core.AgentPurposeConversation,
			Context: testModelContext(), UserInput: "解释当前任务"}); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want exactly 1", stub.calls)
	}
	if stub.options == nil || stub.options.MaxCompletionTokens != acceptancebudget.QwenOutputMax {
		t.Fatalf("request options = %+v, want the profile's output ceiling %d in the request",
			stub.options, acceptancebudget.QwenOutputMax)
	}
	// The core invariant: the number the ledger records IS the number the
	// request asked for.
	recorded := acceptanceProfileRecordedOutputLimit(t, budget.ledgerPath)
	if recorded != stub.options.MaxCompletionTokens {
		t.Fatalf("the ledger records output_limit_tokens = %d, but the request asked for %d",
			recorded, stub.options.MaxCompletionTokens)
	}
	recordedShared := acceptanceProfileRecordedOutputLimit(t, budget.ledgerPath+".calls.json")
	if recordedShared != stub.options.MaxCompletionTokens {
		t.Fatalf("the shared ledger records output_limit_tokens = %d, but the request asked for %d",
			recordedShared, stub.options.MaxCompletionTokens)
	}
}

// An answer over the profile's ceiling is a limit violation, and it is
// recorded as one.
//
// The ceiling used to be another profile's constant, so an answer between the
// Qwen ceiling and that constant was accepted and written down as a normal
// response: the ledger said the call was inside the rules while the profile
// said otherwise.
func TestQwenProfileAgentTurnMarksAnAnswerOverTheProfileCeilingAsLimitViolation(t *testing.T) {
	budget := qwenBudget(t)
	stub := &qwenCountingChat{response: &types.ChatResponse{
		Content:      `{"reply":"合成回复"}`,
		FinishReason: "stop",
		Usage: types.TokenUsage{PromptTokens: 320,
			CompletionTokens: acceptancebudget.QwenOutputMax + 1,
			TotalTokens:      320 + acceptancebudget.QwenOutputMax + 1},
	}}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := &HostAgentModel{models: service, budget: budget}

	_, err := host.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"},
		core.ModelRequest{Agent: core.AgentTaskAssistant, Purpose: core.AgentPurposeConversation,
			Context: testModelContext(), UserInput: "解释当前任务"})
	if err == nil {
		t.Fatal("an answer over the profile's ceiling must not be reported as a normal turn")
	}
	if !errors.Is(err, generation.ErrAcceptanceBudgetExceeded) && !errors.Is(err, generation.ErrAcceptanceBudgetUnavailable) {
		t.Fatalf("Reply() error = %v, want an acceptance budget error", err)
	}
	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Requests) != 1 {
		t.Fatalf("recorded requests = %d, want exactly 1", len(ledger.Requests))
	}
	if ledger.Requests[0].Status != "limit_violation" {
		t.Fatalf("recorded status = %q, want limit_violation", ledger.Requests[0].Status)
	}
	if !ledger.Blocked {
		t.Fatal("a limit violation must block the ledger")
	}
}

// The selected rewrite asks the provider for the profile's ceiling, and the
// ledger records the same number.
func TestQwenProfileSelectedRewriteAsksForTheProfileCeilingAndTheLedgerRecordsIt(t *testing.T) {
	budget := qwenBudget(t)
	stub := &qwenCountingChat{response: &types.ChatResponse{
		Content:      `{"replacement_markdown":"保留事实","source_ids":[],"review_items":[]}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 320, CompletionTokens: 128, TotalTokens: 448},
	}}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := selectedRewriteHostModel{models: service, acceptanceBudget: budget}

	if _, err := host.RewriteSelected(context.Background(), Actor{TenantID: 1, UserID: "test"},
		SelectedRewritePrompt{Instruction: "仅润色"}); err != nil {
		t.Fatalf("RewriteSelected() error = %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want exactly 1", stub.calls)
	}
	if stub.options == nil || stub.options.MaxCompletionTokens != acceptancebudget.QwenOutputMax {
		t.Fatalf("request options = %+v, want the profile's output ceiling %d in the request",
			stub.options, acceptancebudget.QwenOutputMax)
	}
	recorded := acceptanceProfileRecordedOutputLimit(t, budget.ledgerPath)
	if recorded != stub.options.MaxCompletionTokens {
		t.Fatalf("the ledger records output_limit_tokens = %d, but the request asked for %d",
			recorded, stub.options.MaxCompletionTokens)
	}
	recordedShared := acceptanceProfileRecordedOutputLimit(t, budget.ledgerPath+".calls.json")
	if recordedShared != stub.options.MaxCompletionTokens {
		t.Fatalf("the shared ledger records output_limit_tokens = %d, but the request asked for %d",
			recordedShared, stub.options.MaxCompletionTokens)
	}
}

// The rewrite path records an answer over the profile's ceiling as a limit
// violation, exactly like the agent turn does.
func TestQwenProfileSelectedRewriteMarksAnAnswerOverTheProfileCeilingAsLimitViolation(t *testing.T) {
	budget := qwenBudget(t)
	stub := &qwenCountingChat{response: &types.ChatResponse{
		Content:      `{"replacement_markdown":"保留事实","source_ids":[],"review_items":[]}`,
		FinishReason: "stop",
		Usage: types.TokenUsage{PromptTokens: 320,
			CompletionTokens: acceptancebudget.QwenOutputMax + 1,
			TotalTokens:      320 + acceptancebudget.QwenOutputMax + 1},
	}}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := selectedRewriteHostModel{models: service, acceptanceBudget: budget}

	if _, err := host.RewriteSelected(context.Background(), Actor{TenantID: 1, UserID: "test"},
		SelectedRewritePrompt{Instruction: "仅润色"}); !errors.Is(err, generation.ErrAcceptanceBudgetExceeded) {
		t.Fatalf("RewriteSelected() error = %v, want the budget to be exceeded", err)
	}
	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Requests) != 1 {
		t.Fatalf("recorded requests = %d, want exactly 1", len(ledger.Requests))
	}
	if ledger.Requests[0].Status != "limit_violation" {
		t.Fatalf("recorded status = %q, want limit_violation", ledger.Requests[0].Status)
	}
	if !ledger.Blocked {
		t.Fatal("a limit violation must block the ledger")
	}
}

// An input over the profile's ceiling is refused before the provider is
// reached, on both paths.
//
// The two profiles declare the same input ceiling today, so this case does
// not separate them; it is here to keep the refusal in front of the request
// while the ceiling is read from the profile.
func TestQwenProfileRefusesOversizedInputBeforeTheProviderOnBothPaths(t *testing.T) {
	budget := qwenBudget(t)

	agentStub := &qwenCountingChat{response: &types.ChatResponse{Content: `{"reply":"合成回复"}`, FinishReason: "stop"}}
	agentService := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: agentStub}
	agentHost := &HostAgentModel{models: agentService, budget: budget}
	oversized := testModelContext()
	oversized.SourceRefs = []core.AgentSourceRef{{SourceID: "source-1", QuotedTextHash: "hash", ModelUseAllowed: true}}
	if _, err := agentHost.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"},
		core.ModelRequest{Agent: core.AgentTaskAssistant, Purpose: core.AgentPurposeConversation,
			Context: oversized, UserInput: string(make([]byte, acceptancebudget.QwenInputMax))}); err == nil {
		t.Fatal("an oversized turn must be refused")
	}
	if agentStub.calls != 0 {
		t.Fatalf("provider calls = %d, want 0: an oversized turn must not reach the provider", agentStub.calls)
	}

	rewriteStub := &qwenCountingChat{response: &types.ChatResponse{
		Content:      `{"replacement_markdown":"保留事实","source_ids":[],"review_items":[]}`,
		FinishReason: "stop",
	}}
	rewriteHost := selectedRewriteHostModel{models: &acceptanceModelServiceStub{
		models: []*types.Model{qwenApprovedModel()}, chatModel: rewriteStub}, acceptanceBudget: budget}
	if _, err := rewriteHost.RewriteSelected(context.Background(), Actor{TenantID: 1, UserID: "test"},
		SelectedRewritePrompt{Instruction: string(make([]byte, acceptancebudget.QwenInputMax))}); err == nil {
		t.Fatal("an oversized rewrite must be refused")
	}
	if rewriteStub.calls != 0 {
		t.Fatalf("provider calls = %d, want 0: an oversized rewrite must not reach the provider", rewriteStub.calls)
	}
}

// The DeepSeek profile keeps ITS ceilings: the request asks for the ceiling
// the profile declares, the ledger records the same number, and an answer
// over that ceiling is still a limit violation.
func TestDeepSeekProfileKeepsItsCeilingsOnTheAgentTurn(t *testing.T) {
	t.Setenv(generationAcceptanceProfileEnv, acceptancebudget.Profile)
	path := filepath.Join(t.TempDir(), "round.json")
	t.Setenv(generationAcceptanceLedgerEnv, path)
	budget, err := loadGenerationAcceptanceBudget()
	if err != nil || budget == nil {
		t.Fatalf("loadGenerationAcceptanceBudget() = %v, %v; want the DeepSeek profile armed", budget, err)
	}
	stub := &qwenCountingChat{response: &types.ChatResponse{
		Content:      `{"reply":"合成回复"}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 320, CompletionTokens: 128, TotalTokens: 448},
	}}
	service := &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: stub}
	host := &HostAgentModel{models: service, budget: budget}

	if _, err := host.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"},
		core.ModelRequest{Agent: core.AgentTaskAssistant, Purpose: core.AgentPurposeConversation,
			Context: testModelContext(), UserInput: "解释当前任务"}); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if stub.options == nil || stub.options.MaxCompletionTokens != acceptancebudget.OutputMax {
		t.Fatalf("request options = %+v, want the profile's output ceiling %d in the request",
			stub.options, acceptancebudget.OutputMax)
	}
	recorded := acceptanceProfileRecordedOutputLimit(t, path)
	if recorded != stub.options.MaxCompletionTokens {
		t.Fatalf("the ledger records output_limit_tokens = %d, but the request asked for %d",
			recorded, stub.options.MaxCompletionTokens)
	}
}

// The DeepSeek ceiling was, and stays, 6000: an answer of 6001 tokens is a
// limit violation under that profile too.
func TestDeepSeekProfileStillMarksAnAnswerOverItsCeilingAsLimitViolation(t *testing.T) {
	t.Setenv(generationAcceptanceProfileEnv, acceptancebudget.Profile)
	path := filepath.Join(t.TempDir(), "round.json")
	t.Setenv(generationAcceptanceLedgerEnv, path)
	budget, err := loadGenerationAcceptanceBudget()
	if err != nil || budget == nil {
		t.Fatalf("loadGenerationAcceptanceBudget() = %v, %v; want the DeepSeek profile armed", budget, err)
	}
	stub := &qwenCountingChat{response: &types.ChatResponse{
		Content:      `{"reply":"合成回复"}`,
		FinishReason: "stop",
		Usage: types.TokenUsage{PromptTokens: 320,
			CompletionTokens: acceptancebudget.OutputMax + 1,
			TotalTokens:      320 + acceptancebudget.OutputMax + 1},
	}}
	service := &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: stub}
	host := &HostAgentModel{models: service, budget: budget}

	if _, err := host.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"},
		core.ModelRequest{Agent: core.AgentTaskAssistant, Purpose: core.AgentPurposeConversation,
			Context: testModelContext(), UserInput: "解释当前任务"}); err == nil {
		t.Fatal("an answer over the profile's ceiling must not be reported as a normal turn")
	}
	ledger, err := readAcceptanceLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Requests) != 1 || ledger.Requests[0].Status != "limit_violation" {
		t.Fatalf("recorded requests = %+v, want one limit_violation", ledger.Requests)
	}
}

// The uncontrolled paths are not the controlled profile's business: without a
// budget they keep the output budget they always had.
func TestUncontrolledPathsKeepTheirOwnOutputBudget(t *testing.T) {
	t.Setenv(generationAcceptanceProfileEnv, "")

	agentStub := &acceptanceChatStub{response: &types.ChatResponse{
		Content:      `{"reply":"合成回复"}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 100, CompletionTokens: 30},
	}}
	agentHost := &HostAgentModel{models: &acceptanceModelServiceStub{
		models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: agentStub}}
	if _, err := agentHost.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"},
		core.ModelRequest{Agent: core.AgentTaskAssistant, Purpose: core.AgentPurposeConversation,
			Context: testModelContext(), UserInput: "解释当前任务"}); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if agentStub.options == nil || agentStub.options.MaxCompletionTokens != 2048 {
		t.Fatalf("request options = %+v, want the uncontrolled budget 2048", agentStub.options)
	}

	rewriteStub := &acceptanceChatStub{response: &types.ChatResponse{
		Content:      `{"replacement_markdown":"保留事实","source_ids":[],"review_items":[]}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 100, CompletionTokens: 30},
	}}
	rewriteHost := NewSelectedRewriteHostModel(&acceptanceModelServiceStub{
		models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: rewriteStub})
	if _, err := rewriteHost.RewriteSelected(context.Background(), Actor{TenantID: 1, UserID: "test"},
		SelectedRewritePrompt{Instruction: "仅润色"}); err != nil {
		t.Fatalf("RewriteSelected() error = %v", err)
	}
	if rewriteStub.options == nil || rewriteStub.options.MaxCompletionTokens != 2048 {
		t.Fatalf("request options = %+v, want the uncontrolled budget 2048", rewriteStub.options)
	}
}
