package workspace

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"path/filepath"
	"testing"
)

func TestGenerationCostOnlyBudgetAllowsSeventhAndBlocksUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "round.json")
	t.Setenv(generationAcceptanceProfileEnv, acceptancebudget.CostOnlyProfile)
	t.Setenv(generationAcceptanceLedgerEnv, path)
	budget, e := loadGenerationAcceptanceBudget()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 7; i++ {
		r, e := budget.reserve(context.Background(), generationAcceptanceModel, 100)
		if e != nil {
			t.Fatalf("call %d: %v", i+1, e)
		}
		if e = r.complete("response_received", 50, 50, "stop", false); e != nil {
			t.Fatal(e)
		}
	}
	ledger, e := readAcceptanceLedger(path)
	if e != nil {
		t.Fatal(e)
	}
	if ledger.CallsMax != 0 || len(ledger.Requests) != 7 || !validAcceptanceLedger(ledger) {
		t.Fatalf("invalid cost profile: %+v", ledger)
	}
	r, e := budget.reserve(context.Background(), generationAcceptanceModel, 100)
	if e != nil {
		t.Fatal(e)
	}
	_ = r.unknown("usage_unknown")
	if _, e = budget.reserve(context.Background(), generationAcceptanceModel, 100); !errors.Is(e, errAcceptanceLedgerBlocked) {
		t.Fatalf("unknown did not block: %v", e)
	}
}

func TestGenerationCostOnlyMirrorStopsAtFinancialCap(t *testing.T) {
	budget := &generationAcceptanceBudget{profile: acceptancebudget.CostOnlyProfile, ledgerPath: filepath.Join(t.TempDir(), "round.json")}
	count := 0
	for {
		r, e := budget.reserve(context.Background(), generationAcceptanceModel, generationAcceptanceInputMax)
		if errors.Is(e, errAcceptanceBudgetSpent) {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if e = r.complete("response_received", generationAcceptanceInputMax, generationAcceptanceOutputMax, "stop", false); e != nil {
			t.Fatal(e)
		}
		count++
		if count > 200 {
			t.Fatal("financial cap missing")
		}
	}
	ledger, e := readAcceptanceLedger(budget.ledgerPath)
	if e != nil {
		t.Fatal(e)
	}
	if count <= 6 || ledger.Actual*acceptancebudget.DeepSeekBudgetFXNanoCNYPerUSD >= 20_000_000_000 {
		t.Fatal("incorrect financial cap")
	}
	if !validAcceptanceLedger(ledger) {
		t.Fatal("invalid final ledger")
	}
}
