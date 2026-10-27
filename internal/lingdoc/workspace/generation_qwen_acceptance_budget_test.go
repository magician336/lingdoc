package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// The tests below are offline. The chat client is a stub that counts the
// calls it receives, so every assertion about "no request left the process"
// is a fact about the stub, not an inference.

func qwenApprovedModel() *types.Model {
	return &types.Model{
		ID:     "qwen-1",
		Name:   acceptancebudget.QwenChatModel,
		Type:   types.ModelTypeKnowledgeQA,
		Source: types.ModelSourceRemote,
		Status: types.ModelStatusActive,
		Parameters: types.ModelParameters{
			Provider: acceptancebudget.QwenProvider,
			BaseURL:  acceptancebudget.QwenApprovedBaseURLs[0],
		},
	}
}

// qwenCountingChat counts the provider calls it receives.
type qwenCountingChat struct {
	response *types.ChatResponse
	err      error
	calls    int
	options  *chat.ChatOptions
}

func (s *qwenCountingChat) Chat(_ context.Context, _ []chat.Message, options *chat.ChatOptions) (*types.ChatResponse, error) {
	s.calls++
	s.options = options
	return s.response, s.err
}

func (*qwenCountingChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (*qwenCountingChat) GetModelName() string { return acceptancebudget.QwenChatModel }
func (*qwenCountingChat) GetModelID() string   { return "qwen-1" }

func qwenBudget(t *testing.T) *generationAcceptanceBudget {
	t.Helper()
	t.Setenv(generationAcceptanceProfileEnv, acceptancebudget.QwenProfile)
	path := filepath.Join(t.TempDir(), "round.json")
	t.Setenv(generationAcceptanceLedgerEnv, path)
	budget, err := loadGenerationAcceptanceBudget()
	if err != nil {
		t.Fatalf("loadGenerationAcceptanceBudget() error = %v", err)
	}
	if budget == nil {
		t.Fatal("the Qwen profile must arm the acceptance budget")
	}
	return budget
}

// The Qwen rule approves one model and refuses every other shape. The
// DeepSeek rule is not consulted: it names another provider, another
// endpoint and another model.
func TestQwenSelectionApprovesOnlyTheApprovedKnowledgeQAModel(t *testing.T) {
	t.Setenv(generationAcceptanceProfileEnv, acceptancebudget.QwenProfile)

	approved := qwenApprovedModel()
	selected, ok := selectDeepSeekAcceptanceModel([]*types.Model{approved})
	if !ok || selected == nil || selected.ID != approved.ID {
		t.Fatalf("selected model = %+v, valid = %v", selected, ok)
	}

	wrongType := *approved
	wrongType.Type = types.ModelTypeEmbedding
	wrongProvider := *approved
	wrongProvider.Parameters.Provider = "generic"
	wrongEndpoint := *approved
	wrongEndpoint.Parameters.BaseURL = "https://api.deepseek.com"
	wrongName := *approved
	wrongName.Name = "deepseek-flash"
	inactive := *approved
	inactive.Status = types.ModelStatusDownloadFailed
	local := *approved
	local.Source = types.ModelSourceLocal

	for name, model := range map[string]*types.Model{
		"embedding type":   &wrongType,
		"generic provider": &wrongProvider,
		"other endpoint":   &wrongEndpoint,
		"other model":      &wrongName,
		"inactive":         &inactive,
		"local source":     &local,
	} {
		if _, ok := selectDeepSeekAcceptanceModel([]*types.Model{model}); ok {
			t.Fatalf("%s was approved for the controlled Qwen call", name)
		}
	}

	// Two approved rows are ambiguous: the ledger could not say which one
	// answered.
	if _, ok := selectDeepSeekAcceptanceModel([]*types.Model{approved, qwenApprovedModel()}); ok {
		t.Fatal("an ambiguous duplicate model was approved")
	}
}

// The stored provider value must be the LITERAL "aliyun".
//
// This is a configuration requirement, not a code limitation: the real
// deployment stores "generic" on the approved row, because
// provider.DetectProvider does not recognise a regional Model Studio
// workspace endpoint and answers "generic" for it. A rule that accepted the
// detected value would approve whatever provider the endpoint happened to
// look like, so the rule reads the stored field instead and the deployment
// declares the provider explicitly.
//
// The consequence is deliberate and worth stating: until the deployment
// writes "aliyun" on that row, the controlled path refuses it (fail closed).
func TestQwenSelectionRefusesAStoredGenericProviderEvenOnTheApprovedEndpoint(t *testing.T) {
	t.Setenv(generationAcceptanceProfileEnv, acceptancebudget.QwenProfile)

	stored := qwenApprovedModel()
	stored.Parameters.Provider = "generic"

	if _, ok := selectDeepSeekAcceptanceModel([]*types.Model{stored}); ok {
		t.Fatal("a row whose stored provider is 'generic' must not be approved")
	}

	declared := qwenApprovedModel()
	declared.Parameters.Provider = acceptancebudget.QwenProvider
	if _, ok := selectDeepSeekAcceptanceModel([]*types.Model{declared}); !ok {
		t.Fatal("the same row with the provider declared as 'aliyun' must be approved")
	}
}

// The DeepSeek wire-format requirement belongs to the DeepSeek rule. The
// Qwen rule neither requires nor forbids it.
func TestQwenSelectionDoesNotRequireTheDeepSeekThinkingWireFormat(t *testing.T) {
	t.Setenv(generationAcceptanceProfileEnv, acceptancebudget.QwenProfile)

	withoutControl := qwenApprovedModel()
	if _, ok := selectDeepSeekAcceptanceModel([]*types.Model{withoutControl}); !ok {
		t.Fatal("an approved Qwen model was refused for a missing DeepSeek wire format")
	}
	withControl := qwenApprovedModel()
	withControl.Parameters.ExtraConfig = map[string]string{chat.ExtraConfigThinkingControl: "thinking_type"}
	if _, ok := selectDeepSeekAcceptanceModel([]*types.Model{withControl}); !ok {
		t.Fatal("an approved Qwen model was refused for carrying the DeepSeek wire format")
	}
}

// The controlled path names the model it may use. It does not need the
// global default slot to be moved, and it must not take it: the default slot
// is read by other paths, and moving it would change what THEY call.
func TestQwenSelectionIgnoresTheGlobalDefaultSlot(t *testing.T) {
	t.Setenv(generationAcceptanceProfileEnv, acceptancebudget.QwenProfile)

	other := acceptanceDeepSeekModel()
	other.IsDefault = true
	approved := qwenApprovedModel()

	selected, ok := selectDeepSeekAcceptanceModel([]*types.Model{other, approved})
	if !ok || selected == nil || selected.ID != approved.ID {
		t.Fatalf("selected model = %+v, valid = %v; want the approved Qwen model", selected, ok)
	}

	// A default row alone is not an approved row: the controlled path fails
	// closed instead of falling back to whatever is the default.
	if _, ok := selectDeepSeekAcceptanceModel([]*types.Model{other}); ok {
		t.Fatal("the controlled path fell back to the global default model")
	}
}

// The same fact, end to end: with another model holding the default slot,
// the run still calls the approved Qwen model.
func TestQwenProfileCallsTheApprovedModelEvenWhenAnotherModelIsTheDefault(t *testing.T) {
	budget := qwenBudget(t)
	stub := &qwenCountingChat{response: &types.ChatResponse{
		Content:      `{"body_markdown":"受控执行结论 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 320, CompletionTokens: 128, TotalTokens: 448},
	}}
	other := acceptanceDeepSeekModel()
	other.IsDefault = true
	service := &acceptanceModelServiceStub{models: []*types.Model{other, qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if service.selected != "qwen-1" {
		t.Fatalf("called model ID = %q, want the approved Qwen model row, not the default one", service.selected)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want exactly 1", stub.calls)
	}
}

// The gate is real: it runs before the provider is called, it sends the
// output ceiling in the request, and it records what the provider reported.
func TestQwenProfileGateRunsBeforeTheProviderAndRecordsReportedUsage(t *testing.T) {
	budget := qwenBudget(t)
	stub := &qwenCountingChat{response: &types.ChatResponse{
		Content:      `{"body_markdown":"受控执行结论 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 320, CompletionTokens: 128, TotalTokens: 448},
	}}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	draft, err := host.Generate(context.Background(), acceptanceGenerationInput())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want exactly 1", stub.calls)
	}
	if stub.options == nil || stub.options.MaxCompletionTokens != acceptancebudget.QwenOutputMax {
		t.Fatalf("request options = %+v, want the output ceiling %d in the request",
			stub.options, acceptancebudget.QwenOutputMax)
	}
	if len(draft.Sources) != 1 || draft.Sources[0].ID != "source-1" {
		t.Fatalf("draft sources = %+v", draft.Sources)
	}

	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Requests) != 1 {
		t.Fatalf("recorded requests = %d, want exactly 1", len(ledger.Requests))
	}
	request := ledger.Requests[0]
	if request.Model != acceptancebudget.QwenChatModel {
		t.Fatalf("recorded model = %q, want the approved Qwen model", request.Model)
	}
	if request.OutputLimit != acceptancebudget.QwenOutputMax {
		t.Fatalf("recorded output limit = %d, want %d", request.OutputLimit, acceptancebudget.QwenOutputMax)
	}
	if request.InputTokens != 320 || request.OutputTokens != 128 || request.TotalTokens != 448 {
		t.Fatalf("recorded tokens = %d/%d/%d, want the provider's 320/128/448",
			request.InputTokens, request.OutputTokens, request.TotalTokens)
	}
	if request.UsageStatus != acceptancebudget.UsageStatusReported {
		t.Fatalf("recorded usage status = %q, want %q", request.UsageStatus, acceptancebudget.UsageStatusReported)
	}
	if ledger.CostBasis != acceptancebudget.CostBasisNotApplicable {
		t.Fatalf("ledger cost basis = %q, want %q", ledger.CostBasis, acceptancebudget.CostBasisNotApplicable)
	}
	if request.ActualNanoUSD != 0 || request.ReservedNanoUSD != 0 || ledger.Actual != 0 {
		t.Fatalf("money columns must stay uncomputed: request %d/%d ledger %d",
			request.ReservedNanoUSD, request.ActualNanoUSD, ledger.Actual)
	}
	if strings.Contains(ledger.PricingBasis, "DeepSeek") {
		t.Fatalf("a Qwen ledger must not carry the DeepSeek price basis: %q", ledger.PricingBasis)
	}

	// The probe that shows the gate really ran: the shared budget ledger
	// holds the same one reservation. It is written by the reservation
	// itself, before the provider is called.
	total, err := readAcceptanceLedger(budget.ledgerPath + ".calls.json")
	if err != nil {
		t.Fatalf("the shared budget ledger must exist: %v", err)
	}
	if len(total.Requests) != 1 || total.Requests[0].Model != acceptancebudget.QwenChatModel {
		t.Fatalf("the shared budget ledger must hold exactly one reservation of the approved model: %+v", total.Requests)
	}
}

// One external request, and no second one: the call budget is one, and the
// second attempt is refused before the provider is reached.
func TestQwenProfileRefusesASecondRequestWithoutCallingTheProvider(t *testing.T) {
	budget := qwenBudget(t)
	stub := &qwenCountingChat{response: &types.ChatResponse{
		Content:      `{"body_markdown":"受控执行结论 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 320, CompletionTokens: 128, TotalTokens: 448},
	}}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
		t.Fatalf("first Generate() error = %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls after the first run = %d, want 1", stub.calls)
	}
	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); !errors.Is(err, generation.ErrAcceptanceBudgetExceeded) {
		t.Fatalf("second Generate() error = %v, want the call budget to be exhausted", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls after the second run = %d, want 1: the second attempt must not reach the provider", stub.calls)
	}
}

// A provider failure stops the run. Nothing is retried, nothing is sent
// again, and the record of the failed call is kept.
func TestQwenProfileFailureStopsTheRunWithoutASecondRequest(t *testing.T) {
	budget := qwenBudget(t)
	stub := &qwenCountingChat{err: errors.New("provider refused the request")}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); !errors.Is(err, generation.ErrAcceptanceOutcomeUnknown) {
		t.Fatalf("Generate() error = %v, want an unknown outcome", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want exactly 1", stub.calls)
	}
	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); !errors.Is(err, generation.ErrAcceptanceOutcomeUnknown) {
		t.Fatalf("second Generate() error = %v, want the run to stay blocked", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls after the failed run = %d, want 1: a failed call must not be sent again", stub.calls)
	}
	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Requests) != 1 || ledger.Requests[0].Status != "outcome_unknown" {
		t.Fatalf("the failed call must stay on the record: %+v", ledger.Requests)
	}
	if !ledger.Blocked {
		t.Fatal("a failed call must block the ledger")
	}
}

// A provider that answers without usage is recorded as "unavailable". The
// estimate of the request is never written as an actual usage.
func TestQwenProfileRecordsMissingUsageAsUnavailable(t *testing.T) {
	budget := qwenBudget(t)
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
	if len(ledger.Requests) != 1 {
		t.Fatalf("recorded requests = %d, want 1", len(ledger.Requests))
	}
	request := ledger.Requests[0]
	if request.UsageStatus != acceptancebudget.UsageStatusUnavailable {
		t.Fatalf("recorded usage status = %q, want %q", request.UsageStatus, acceptancebudget.UsageStatusUnavailable)
	}
	if request.InputTokens != 0 || request.OutputTokens != 0 || request.TotalTokens != 0 {
		t.Fatalf("a missing usage must leave the counters empty: %d/%d/%d",
			request.InputTokens, request.OutputTokens, request.TotalTokens)
	}
}

// An input over the ceiling is refused before the provider is reached, and
// nothing is written to the ledger.
func TestQwenProfileRefusesOversizedInputBeforeTheProvider(t *testing.T) {
	budget := qwenBudget(t)
	stub := &qwenCountingChat{response: &types.ChatResponse{Content: "{}", FinishReason: "stop"}}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	input := acceptanceGenerationInput()
	// The instruction is the part of the request that is not truncated, so
	// it is what pushes the encoded request over the ceiling.
	input.Instruction = strings.Repeat("字", acceptancebudget.QwenInputMax)
	if _, err := host.Generate(context.Background(), input); !errors.Is(err, generation.ErrAcceptanceBudgetExceeded) {
		t.Fatalf("Generate() error = %v, want the input ceiling to be enforced", err)
	}
	if stub.calls != 0 {
		t.Fatalf("provider calls = %d, want 0: an oversized input must not reach the provider", stub.calls)
	}
	if _, err := os.Stat(budget.ledgerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an oversized input must not create a ledger, stat error = %v", err)
	}
}

// A profile without a price basis computes no amount -- not even for a
// counter that cannot be true.
//
// The order of the two checks matters: if the priced profile's sentinel were
// returned for an impossible counter, a Qwen row that reserved nothing would
// carry a DeepSeek-sized amount, which is the combination the cost basis
// exists to prevent.
func TestQwenProfileCostStaysZeroForImpossibleCounters(t *testing.T) {
	if got := actualAcceptanceCost(acceptancebudget.QwenProfile, -1, -1); got != 0 {
		t.Fatalf("actualAcceptanceCost(QwenProfile, -1, -1) = %d, want 0", got)
	}
	if got := actualAcceptanceCost(acceptancebudget.QwenProfile, 100, 50); got != 0 {
		t.Fatalf("actualAcceptanceCost(QwenProfile, 100, 50) = %d, want 0", got)
	}
	if got := actualAcceptanceCost(acceptancebudget.Profile, -1, -1); got == 0 {
		t.Fatal("a priced profile must keep its own behaviour for an impossible counter")
	}
}

// The DB row's money columns follow the profile too.
//
// Under the Qwen profile there is no price basis, so both money columns are 0
// and the MEANING of that 0 lives in the JSON ledger ("cost_basis":
// "not_applicable"). Under the DeepSeek profile the row keeps its own
// per-call amount, byte for byte.
func TestQwenProfileLedgerRowCarriesNoMoneyAndDeepSeekKeepsItsOwn(t *testing.T) {
	budget := qwenBudget(t)
	stub := &qwenCountingChat{response: &types.ChatResponse{
		Content:      `{"body_markdown":"受控执行结论 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 320, CompletionTokens: 128, TotalTokens: 448},
	}}
	ledger := &recordingModelCallLedger{}
	service := &acceptanceModelServiceStub{models: []*types.Model{qwenApprovedModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{},
		acceptanceBudget: budget, ledger: ledger}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(ledger.entries) != 1 {
		t.Fatalf("ledger rows = %d, want exactly 1", len(ledger.entries))
	}
	row := ledger.entries[0]
	if row.ReservedNanoUSD != 0 || row.ActualNanoUSD != 0 {
		t.Fatalf("a profile without a price basis must reserve and consume no money: %d/%d",
			row.ReservedNanoUSD, row.ActualNanoUSD)
	}
	// The DB row says what its zeros mean (#527): not_applicable, so the row
	// read alone identifies as "not computed" instead of "free".
	if row.CostBasis != acceptancebudget.CostBasisNotApplicable {
		t.Fatalf("row cost basis = %q, want %q (the profile computed no money)",
			row.CostBasis, acceptancebudget.CostBasisNotApplicable)
	}
	if row.Provider != acceptancebudget.QwenProvider {
		t.Fatalf("row provider = %q, want %q (taken from the model row that answered)", row.Provider, acceptancebudget.QwenProvider)
	}
	if row.ModelID != "qwen-1" {
		t.Fatalf("row model id = %q, want the approved model row", row.ModelID)
	}
	if row.SourceKind != core.ModelCallLedgerSourceGenerationRun {
		t.Fatalf("row source kind = %q, want %q", row.SourceKind, core.ModelCallLedgerSourceGenerationRun)
	}
	if row.InputTokens != 320 || row.OutputTokens != 128 {
		t.Fatalf("row tokens = %d/%d, want the provider's 320/128", row.InputTokens, row.OutputTokens)
	}

	// The priced profile keeps its own amount: the change to the reserved
	// column must not silently zero the DeepSeek path.
	t.Setenv(generationAcceptanceProfileEnv, generationAcceptanceProfile)
	deepSeekLedger := &recordingModelCallLedger{}
	deepSeekStub := &acceptanceChatStub{response: &types.ChatResponse{
		Content:      `{"body_markdown":"受控执行结论 [[source:source-1]]","source_ids":["source-1"],"review_items":[]}`,
		FinishReason: "stop",
		Usage:        types.TokenUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
	}}
	deepSeekService := &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: deepSeekStub}
	deepSeekHost := generationHostModel{models: deepSeekService, templates: core.ContractDemoTemplate{},
		acceptanceBudget: &generationAcceptanceBudget{profile: generationAcceptanceProfile, ledgerPath: filepath.Join(t.TempDir(), "round.json")},
		ledger:           deepSeekLedger}
	if _, err := deepSeekHost.Generate(context.Background(), acceptanceGenerationInput()); err != nil {
		t.Fatalf("DeepSeek Generate() error = %v", err)
	}
	if len(deepSeekLedger.entries) != 1 {
		t.Fatalf("DeepSeek ledger rows = %d, want 1", len(deepSeekLedger.entries))
	}
	deepSeekRow := deepSeekLedger.entries[0]
	if deepSeekRow.ReservedNanoUSD != generationAcceptancePerCall {
		t.Fatalf("DeepSeek reserved amount = %d, want %d", deepSeekRow.ReservedNanoUSD, generationAcceptancePerCall)
	}
	if deepSeekRow.ActualNanoUSD != actualDeepSeekCost(100, 50) {
		t.Fatalf("DeepSeek actual amount = %d, want %d", deepSeekRow.ActualNanoUSD, actualDeepSeekCost(100, 50))
	}
	// The priced profile names the basis its amounts were computed under, so
	// the two profiles' rows cannot be read with one another's meaning (#527).
	if deepSeekRow.CostBasis != acceptancebudget.CostBasisDeepSeekPeakUSD {
		t.Fatalf("DeepSeek row cost basis = %q, want %q",
			deepSeekRow.CostBasis, acceptancebudget.CostBasisDeepSeekPeakUSD)
	}
}

// A model row that is not the approved Qwen model is refused, and the
// provider is not reached.
func TestQwenProfileRefusesAModelRowItDidNotApprove(t *testing.T) {
	budget := qwenBudget(t)
	stub := &qwenCountingChat{response: &types.ChatResponse{Content: "{}", FinishReason: "stop"}}
	service := &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: stub}
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, acceptanceBudget: budget}

	if _, err := host.Generate(context.Background(), acceptanceGenerationInput()); !errors.Is(err, generation.ErrAcceptanceModelMismatch) {
		t.Fatalf("Generate() error = %v, want a model mismatch", err)
	}
	if stub.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", stub.calls)
	}
}
