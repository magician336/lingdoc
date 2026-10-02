package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestAcceptanceBudgetReservesSixCallsAndRejectsSeventh(t *testing.T) {
	budget := &generationAcceptanceBudget{ledgerPath: t.TempDir() + "/round.json"}
	for i := 0; i < generationAcceptanceCallsMax; i++ {
		reservation, err := budget.reserve(context.Background(), generationAcceptanceModel, 2048)
		if err != nil {
			t.Fatalf("reserve request %d: %v", i+1, err)
		}
		if err := reservation.complete("response_received", 1000, 1000, "stop", false); err != nil {
			t.Fatalf("complete request %d: %v", i+1, err)
		}
	}
	if _, err := budget.reserve(context.Background(), generationAcceptanceModel, 2048); !errors.Is(err, errAcceptanceBudgetSpent) {
		t.Fatalf("seventh request error = %v, want exhausted budget", err)
	}
	ledger, err := readAcceptanceLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Requests) != 6 || ledger.Reserved != generationAcceptanceTotal || ledger.Actual != 9_000_000 {
		t.Fatalf("ledger totals = calls:%d reserved:%d actual:%d", len(ledger.Requests), ledger.Reserved, ledger.Actual)
	}
}

func TestAcceptanceBudgetRejectsOversizedInputBeforeReservation(t *testing.T) {
	budget := &generationAcceptanceBudget{ledgerPath: t.TempDir() + "/round.json"}
	upperBound, err := acceptanceInputUpperBound([]chat.Message{{Role: "user", Content: strings.Repeat("x", generationAcceptanceInputMax)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := budget.reserve(context.Background(), generationAcceptanceModel, upperBound); !errors.Is(err, errAcceptanceInputTooLarge) {
		t.Fatalf("oversized request error = %v, want input limit rejection", err)
	}
	if _, err := os.Stat(budget.ledgerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("oversized preflight unexpectedly created a ledger")
	}
}

func TestAcceptanceBudgetUnknownOutcomeBlocksNextCall(t *testing.T) {
	budget := &generationAcceptanceBudget{ledgerPath: t.TempDir() + "/round.json"}
	reservation, err := budget.reserve(context.Background(), generationAcceptanceModel, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.unknown("outcome_unknown"); !errors.Is(err, errAcceptanceLedgerBlocked) {
		t.Fatalf("unknown outcome record error = %v", err)
	}
	if _, err := budget.reserve(context.Background(), generationAcceptanceModel, 2048); !errors.Is(err, errAcceptanceLedgerBlocked) {
		t.Fatalf("next request error = %v, want reconciliation block", err)
	}
}

func TestAcceptanceBudgetLockSerializesInFlightRequests(t *testing.T) {
	budget := &generationAcceptanceBudget{ledgerPath: t.TempDir() + "/round.json"}
	first, err := budget.reserve(context.Background(), generationAcceptanceModel, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := budget.reserve(ctx, generationAcceptanceModel, 2048); !errors.Is(err, errAcceptanceLedgerBusy) {
		t.Fatalf("parallel request error = %v, want serialized timeout", err)
	}
	if err := first.complete("response_received", 1000, 1000, "stop", false); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptanceProfilePinsDeepSeekModelAndDisabledThinkingOptions(t *testing.T) {
	model := &types.Model{
		ID:     "model-1",
		Name:   generationAcceptanceModel,
		Type:   types.ModelTypeKnowledgeQA,
		Source: types.ModelSourceRemote,
		Status: types.ModelStatusActive,
		Parameters: types.ModelParameters{
			Provider: "generic",
			BaseURL:  "https://api.deepseek.com/",
			ExtraConfig: map[string]string{
				chat.ExtraConfigThinkingControl: "thinking_type",
			},
		},
	}
	if selected, ok := selectDeepSeekAcceptanceModel([]*types.Model{model}); !ok || selected.ID != model.ID {
		t.Fatalf("selected model = %+v, valid = %v", selected, ok)
	}
	wrongControl := *model
	wrongControl.Parameters.ExtraConfig = map[string]string{chat.ExtraConfigThinkingControl: "chat_template_kwargs"}
	if _, ok := selectDeepSeekAcceptanceModel([]*types.Model{&wrongControl}); ok {
		t.Fatal("accepted model with an unverified thinking wire format")
	}
	if _, ok := selectDeepSeekAcceptanceModel([]*types.Model{model, &wrongControl}); ok {
		t.Fatal("accepted an ambiguous duplicate model name")
	}
	options := acceptanceGenerationChatOptions()
	if options.MaxCompletionTokens != generationAcceptanceOutputMax || options.Thinking == nil || *options.Thinking {
		t.Fatalf("acceptance options = %+v, want 6000 output and explicit thinking=false", options)
	}
}

func TestAcceptanceCostUsesPeakCacheMissRates(t *testing.T) {
	if got := actualDeepSeekCost(generationAcceptanceInputMax, generationAcceptanceOutputMax); got != generationAcceptancePerCall {
		t.Fatalf("max single-call cost = %d nano-USD, want %d", got, generationAcceptancePerCall)
	}
}
