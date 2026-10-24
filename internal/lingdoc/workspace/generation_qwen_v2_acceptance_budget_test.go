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

// The second controlled Qwen profile.
//
// The tests below are offline. The chat client is a stub that counts the
// calls it receives, so every assertion about "no request left the process"
// is a fact about the stub, not an inference.

// qwenV2Budget arms the second Qwen profile on a fresh ledger.
func qwenV2Budget(t *testing.T) *generationAcceptanceBudget {
	t.Helper()
	t.Setenv(generationAcceptanceProfileEnv, acceptancebudget.QwenProfileV2)
	path := filepath.Join(t.TempDir(), "round.json")
	t.Setenv(generationAcceptanceLedgerEnv, path)
	budget, err := loadGenerationAcceptanceBudget()
	if err != nil {
		t.Fatalf("loadGenerationAcceptanceBudget() error = %v", err)
	}
	if budget == nil {
		t.Fatal("the second Qwen profile must arm the acceptance budget")
	}
	return budget
}

// qwenV2TightenCeiling lowers the cumulative token ceiling of one window to
// the number of tokens it has already reserved.
//
// Lowering the ceiling of one window is what an operator may do -- the
// profile's number stays the hard maximum -- and it is how the cumulative
// gate becomes observable: a window that fits no further call refuses the
// next request before it leaves the process.
func qwenV2TightenCeiling(t *testing.T, path string) {
	t.Helper()
	ledger, err := readAcceptanceLedger(path, acceptancebudget.QwenProfileV2)
	if err != nil {
		t.Fatalf("readAcceptanceLedger() error = %v", err)
	}
	if ledger.ReservedTokens <= 0 {
		t.Fatalf("the window has reserved nothing yet: %+v", ledger)
	}
	ledger.CumulativeTokenMax = ledger.ReservedTokens
	if err := writeAcceptanceLedger(path, ledger); err != nil {
		t.Fatalf("writeAcceptanceLedger() error = %v", err)
	}
}

// qwenV2Answer is the response the stub returns for a successful call.
func qwenV2Answer() *types.ChatResponse {
	return &types.ChatResponse{
		Content:      `{"body_markdown":"受控执行结论 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 320, CompletionTokens: 128, TotalTokens: 448},
	}
}

// The second profile arms the controlled path, approves the same one model
// and sends the same output ceiling to the provider.
//
// The numbers are LITERALS on purpose: an assertion written against the
// constant it is testing proves nothing, because the constant moves with
// the expectation.
func TestQwenV2ProfileApprovesTheSameModelAndSendsTheOutputCeiling(t *testing.T) {
	if acceptancebudget.QwenOutputMax != 4096 {
		t.Fatalf("QwenOutputMax = %d, want the authorized 4096", acceptancebudget.QwenOutputMax)
	}
	budget := qwenV2Budget(t)
	stub := &qwenCountingChat{response: qwenV2Answer()}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want exactly 1", stub.calls)
	}
	if service.selected != "qwen-1" {
		t.Fatalf("called model ID = %q, want the approved Qwen model row", service.selected)
	}
	if stub.options == nil || stub.options.MaxCompletionTokens != 4096 {
		t.Fatalf("request options = %+v, want the output ceiling 4096 in the request", stub.options)
	}
}

// The call budget of the second profile is FOUR, and the fifth request is
// refused BEFORE the provider is reached.
//
// The numbers are literals: the authorization says four calls, and the test
// has to say four too.
func TestQwenV2RefusesTheFifthRequestWithoutCallingTheProvider(t *testing.T) {
	if acceptancebudget.QwenV2MaxCalls != 4 {
		t.Fatalf("QwenV2MaxCalls = %d, want the authorized 4", acceptancebudget.QwenV2MaxCalls)
	}
	budget := qwenV2Budget(t)
	stub := &qwenCountingChat{response: qwenV2Answer()}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	for call := 1; call <= 4; call++ {
		if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
			t.Fatalf("Generate() %d error = %v, want the authorized call to be allowed", call, err)
		}
	}
	if stub.calls != 4 {
		t.Fatalf("provider calls = %d, want 4", stub.calls)
	}
	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); !errors.Is(err, generation.ErrAcceptanceBudgetExceeded) {
		t.Fatalf("fifth Generate() error = %v, want the call budget to be exhausted", err)
	}
	if stub.calls != 4 {
		t.Fatalf("provider calls after the fifth run = %d, want 4: the fifth attempt must not reach the provider",
			stub.calls)
	}
}

// The cumulative token gate refuses BEFORE the provider is reached.
//
// The window's ceiling is lowered to what it has already reserved, so the
// next request does not fit. The refusal happens during the reservation, so
// the provider is never called.
func TestQwenV2CumulativeBudgetRefusesBeforeTheProvider(t *testing.T) {
	budget := qwenV2Budget(t)
	stub := &qwenCountingChat{response: qwenV2Answer()}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
		t.Fatalf("first Generate() error = %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", stub.calls)
	}

	qwenV2TightenCeiling(t, budget.ledgerPath)

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); !errors.Is(err, generation.ErrAcceptanceBudgetExceeded) {
		t.Fatalf("second Generate() error = %v, want the cumulative token budget to be exhausted", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls after the refused run = %d, want 1: a request that does not fit must not reach the provider",
			stub.calls)
	}
}

// A reservation that fails leaves no provider request behind.
//
// This is the same fact seen from the other side: the run stops during the
// reservation, so the provider is never asked.
func TestQwenV2BudgetReservationFailureLeavesZeroProviderRequests(t *testing.T) {
	budget := qwenV2Budget(t)
	stub := &qwenCountingChat{response: qwenV2Answer()}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
		t.Fatalf("first Generate() error = %v", err)
	}
	qwenV2TightenCeiling(t, budget.ledgerPath)
	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err == nil {
		t.Fatal("the second run must be refused")
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want 1: the refused run must not have reached the provider", stub.calls)
	}

	// The refused request left no row behind: the ledger holds the one call
	// that happened and nothing else.
	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Requests) != 1 {
		t.Fatalf("recorded requests = %d, want 1: a refused reservation must not be recorded", len(ledger.Requests))
	}
}

// The two ledgers agree, and the three token numbers are kept apart.
//
// The chat ledger and the shared budget ledger hold the same reservation.
// The reservation says what the window could consume, the reported usage
// says what the provider consumed, and the remainder is a number against
// one of those two counts.
func TestQwenV2KeepsReservationUsageAndRemainingApart(t *testing.T) {
	budget := qwenV2Budget(t)
	stub := &qwenCountingChat{response: qwenV2Answer()}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Requests) != 1 {
		t.Fatalf("recorded requests = %d, want 1", len(ledger.Requests))
	}
	request := ledger.Requests[0]
	if request.InputTokens != 320 || request.OutputTokens != 128 || request.TotalTokens != 448 {
		t.Fatalf("recorded tokens = %d/%d/%d, want the provider's 320/128/448",
			request.InputTokens, request.OutputTokens, request.TotalTokens)
	}
	if request.UsageStatus != acceptancebudget.UsageStatusReported {
		t.Fatalf("recorded usage status = %q, want %q", request.UsageStatus, acceptancebudget.UsageStatusReported)
	}

	reserved := request.EstimatedInputUpperBound + request.OutputLimit
	if ledger.ReservedTokens != reserved {
		t.Fatalf("cumulative reservation = %d, want %d", ledger.ReservedTokens, reserved)
	}
	if ledger.ActualTokens != 448 {
		t.Fatalf("cumulative reported usage = %d, want the provider's 448", ledger.ActualTokens)
	}
	if ledger.ReservedTokens == ledger.ActualTokens {
		t.Fatal("the reservation and the reported usage must not be the same number")
	}

	state := ledger.tokenBudgetState()
	if state.Ceiling != acceptancebudget.QwenV2CumulativeTokenMax {
		t.Fatalf("ceiling = %d, want %d", state.Ceiling, acceptancebudget.QwenV2CumulativeTokenMax)
	}
	if state.RemainingReserved != acceptancebudget.QwenV2CumulativeTokenMax-reserved {
		t.Fatalf("remaining against the reservation = %d, want %d",
			state.RemainingReserved, acceptancebudget.QwenV2CumulativeTokenMax-reserved)
	}
	if state.RemainingActual == nil || *state.RemainingActual != acceptancebudget.QwenV2CumulativeTokenMax-448 {
		t.Fatalf("remaining against the reported usage = %v, want %d",
			state.RemainingActual, acceptancebudget.QwenV2CumulativeTokenMax-448)
	}

	// The shared budget ledger holds the same reservation.
	total, err := readAcceptanceLedger(budget.ledgerPath + ".calls.json")
	if err != nil {
		t.Fatalf("the shared budget ledger must exist: %v", err)
	}
	if len(total.Requests) != len(ledger.Requests) {
		t.Fatalf("the two ledgers disagree: %d rows against %d rows", len(total.Requests), len(ledger.Requests))
	}
	if total.ReservedTokens != ledger.ReservedTokens || total.ActualTokens != ledger.ActualTokens {
		t.Fatalf("the two ledgers disagree about the tokens: reserved %d/%d, actual %d/%d",
			total.ReservedTokens, ledger.ReservedTokens, total.ActualTokens, ledger.ActualTokens)
	}
}

// A usage that could not be verified stops the window.
//
// The provider answered without usage, so nobody can prove what that call
// consumed, and the next request is refused. The counters stay empty: an
// unknown usage is not a usage of zero.
func TestQwenV2MissingUsageStopsTheWindow(t *testing.T) {
	budget := qwenV2Budget(t)
	stub := &qwenCountingChat{response: &types.ChatResponse{
		Content:      `{"body_markdown":"受控执行结论 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`,
		FinishReason: "stop",
	}}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); !errors.Is(err, generation.ErrAcceptanceOutcomeUnknown) {
		t.Fatalf("Generate() error = %v, want an unknown usage", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want exactly 1", stub.calls)
	}

	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	request := ledger.Requests[0]
	if request.UsageStatus != acceptancebudget.UsageStatusUnavailable {
		t.Fatalf("recorded usage status = %q, want %q", request.UsageStatus, acceptancebudget.UsageStatusUnavailable)
	}
	if request.InputTokens != 0 || request.OutputTokens != 0 || request.TotalTokens != 0 {
		t.Fatalf("an unverifiable usage must leave the counters empty: %d/%d/%d",
			request.InputTokens, request.OutputTokens, request.TotalTokens)
	}
	if ledger.ActualTokens != 0 {
		t.Fatalf("cumulative reported usage = %d, want 0: an unverifiable usage is not a usage", ledger.ActualTokens)
	}
	state := ledger.tokenBudgetState()
	if !state.UsageUnverifiable {
		t.Fatal("the window must report that its usage is unverifiable")
	}
	if state.RemainingActual != nil {
		t.Fatal("the remainder against the usage is UNKNOWN while a usage is unverifiable")
	}

	// The next request is refused: the window cannot prove that it fits.
	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); !errors.Is(err, generation.ErrAcceptanceOutcomeUnknown) {
		t.Fatalf("second Generate() error = %v, want the window to stay stopped", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls after the stopped run = %d, want 1: an unverifiable usage must not buy another request",
			stub.calls)
	}
}

// The window refuses the next request when a usage could not be verified.
//
// The refusal does not lean on the ledger being blocked: an unverifiable
// usage is an UNKNOWN usage, and an unknown usage cannot prove that the
// next call fits inside the ceiling.
//
// The shared budget ledger of this test is CLEAN -- it holds a reported
// usage -- so the refusal can only come from the chat ledger's own rule.
func TestQwenV2UnverifiableUsageStopsTheWindowEvenWhenTheLedgerIsNotBlocked(t *testing.T) {
	budget := qwenV2Budget(t)
	stub := &qwenCountingChat{response: qwenV2Answer()}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", stub.calls)
	}

	// The recorded usage becomes unverifiable while the ledger stays
	// unblocked: the window now holds an unknown usage.
	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	ledger.Requests[0].UsageStatus = acceptancebudget.UsageStatusUnavailable
	if err := writeAcceptanceLedger(budget.ledgerPath, ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.Blocked {
		t.Fatal("this test needs a ledger that is NOT blocked")
	}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); !errors.Is(err, generation.ErrAcceptanceBudgetExceeded) {
		t.Fatalf("second Generate() error = %v, want the window to be stopped", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want 1: an unverifiable usage must not buy another request", stub.calls)
	}
}

// The money columns of a profile without a price basis stay uncomputed.
//
// A 0 under the "not_applicable" basis reads as "nothing was priced", never
// as "the call was free". The basis says which of the two it is.
func TestQwenV2CostStaysUncomputedAndUnpriced(t *testing.T) {
	budget := qwenV2Budget(t)
	stub := &qwenCountingChat{response: qwenV2Answer()}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.CostBasis != acceptancebudget.CostBasisNotApplicable {
		t.Fatalf("ledger cost basis = %q, want %q", ledger.CostBasis, acceptancebudget.CostBasisNotApplicable)
	}
	if ledger.Reserved != 0 || ledger.Actual != 0 || ledger.ReserveMax != 0 {
		t.Fatalf("the money columns must stay uncomputed: reserve %d, reserved %d, actual %d",
			ledger.ReserveMax, ledger.Reserved, ledger.Actual)
	}
	if ledger.Requests[0].ReservedNanoUSD != 0 || ledger.Requests[0].ActualNanoUSD != 0 {
		t.Fatalf("the row's money columns must stay uncomputed: %+v", ledger.Requests[0])
	}
}

// The first profile keeps its single call, and the second request is
// refused before the provider is reached.
//
// This is the "the old profile is unchanged" proof at the application seam:
// the same facts, the same refusals, the same ledger.
func TestQwenV1ProfileStillRefusesItsSecondRequestWithoutCallingTheProvider(t *testing.T) {
	t.Setenv(generationAcceptanceProfileEnv, acceptancebudget.QwenProfile)
	path := filepath.Join(t.TempDir(), "round.json")
	t.Setenv(generationAcceptanceLedgerEnv, path)
	budget, err := loadGenerationAcceptanceBudget()
	if err != nil {
		t.Fatalf("loadGenerationAcceptanceBudget() error = %v", err)
	}
	stub := &qwenCountingChat{response: qwenV2Answer()}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
		t.Fatalf("first Generate() error = %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", stub.calls)
	}
	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); !errors.Is(err, generation.ErrAcceptanceBudgetExceeded) {
		t.Fatalf("second Generate() error = %v, want the call budget to be exhausted", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls after the second run = %d, want 1: the second attempt must not reach the provider", stub.calls)
	}

	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.CallsMax != 1 {
		t.Fatalf("the first profile's call budget = %d, want 1", ledger.CallsMax)
	}
	if ledger.CumulativeTokenMax != 0 {
		t.Fatalf("the first profile must not carry a cumulative ceiling, got %d", ledger.CumulativeTokenMax)
	}
	if ledger.ReservedTokens != 0 || ledger.ActualTokens != 0 {
		t.Fatalf("the first profile must not keep cumulative token counters: %d/%d",
			ledger.ReservedTokens, ledger.ActualTokens)
	}
}

// The first profile's ledger file does not carry the second profile's
// fields, so an old ledger reads exactly as it did before.
func TestQwenV1LedgerFileCarriesNoCumulativeTokenFields(t *testing.T) {
	t.Setenv(generationAcceptanceProfileEnv, acceptancebudget.QwenProfile)
	path := filepath.Join(t.TempDir(), "round.json")
	t.Setenv(generationAcceptanceLedgerEnv, path)
	budget, err := loadGenerationAcceptanceBudget()
	if err != nil {
		t.Fatalf("loadGenerationAcceptanceBudget() error = %v", err)
	}
	stub := &qwenCountingChat{response: qwenV2Answer()}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	data, err := os.ReadFile(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cumulative_token_max", "reserved_tokens", "actual_tokens"} {
		if containsJSONKey(data, key) {
			t.Fatalf("the first profile's ledger must not carry %q: %s", key, data)
		}
	}
}

// containsJSONKey reports whether a JSON document carries the given key at
// the top level.
func containsJSONKey(data []byte, key string) bool {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	_, ok := raw[key]
	return ok
}
