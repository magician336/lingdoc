package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
)

func TestGenerationCostOnlyMigrationPreservesHistoryAndAllowsSeventh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "round.json")
	old := &generationAcceptanceBudget{
		profile:    generationAcceptanceProfile,
		ledgerPath: path,
		total:      acceptancebudget.New(generationAcceptanceProfile, path+".calls.json"),
	}
	for i := 0; i < 4; i++ {
		reservation, err := old.reserve(context.Background(), generationAcceptanceModel, 100)
		if err != nil {
			t.Fatalf("legacy call %d: %v", i+1, err)
		}
		if err := reservation.complete("response_received", 50, 50, "stop", false); err != nil {
			t.Fatal(err)
		}
	}
	beforeChat, err := readAcceptanceLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	totalData, err := os.ReadFile(path + ".calls.json")
	if err != nil {
		t.Fatal(err)
	}
	var beforeTotal acceptancebudget.Ledger
	if err := json.Unmarshal(totalData, &beforeTotal); err != nil {
		t.Fatal(err)
	}
	costOnly := &generationAcceptanceBudget{
		profile:    acceptancebudget.CostOnlyProfile,
		ledgerPath: path,
		total:      acceptancebudget.New(acceptancebudget.CostOnlyProfile, path+".calls.json"),
	}
	for i := 4; i < 8; i++ {
		reservation, err := costOnly.reserve(context.Background(), generationAcceptanceModel, 100)
		if err != nil {
			t.Fatalf("cost-only call %d: %v", i+1, err)
		}
		if err := reservation.complete("response_received", 50, 50, "stop", false); err != nil {
			t.Fatal(err)
		}
	}
	afterChat, err := readAcceptanceLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	totalData, err = os.ReadFile(path + ".calls.json")
	if err != nil {
		t.Fatal(err)
	}
	var afterTotal acceptancebudget.Ledger
	if err := json.Unmarshal(totalData, &afterTotal); err != nil {
		t.Fatal(err)
	}
	if afterChat.Profile != acceptancebudget.CostOnlyProfile || afterChat.CallsMax != 0 ||
		afterTotal.Profile != acceptancebudget.CostOnlyProfile || afterTotal.MaxCalls != 0 {
		t.Fatal("both generation ledgers must be upgraded to the cost-only profile")
	}
	if len(afterChat.Requests) != 8 || len(afterTotal.Requests) != 8 ||
		!reflect.DeepEqual(beforeChat.Requests, afterChat.Requests[:4]) ||
		!reflect.DeepEqual(beforeTotal.Requests, afterTotal.Requests[:4]) {
		t.Fatal("legacy request history was lost or the seventh call was rejected")
	}
}

func TestGenerationCostOnlyMigrationRejectsBlockedLegacyHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "round.json")
	old := &generationAcceptanceBudget{
		profile:    generationAcceptanceProfile,
		ledgerPath: path,
		total:      acceptancebudget.New(generationAcceptanceProfile, path+".calls.json"),
	}
	reservation, err := old.reserve(context.Background(), generationAcceptanceModel, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.unknown("outcome_unknown"); !errors.Is(err, errAcceptanceLedgerBlocked) {
		t.Fatalf("unknown generation call was not blocked: %v", err)
	}
	costOnly := &generationAcceptanceBudget{
		profile:    acceptancebudget.CostOnlyProfile,
		ledgerPath: path,
		total:      acceptancebudget.New(acceptancebudget.CostOnlyProfile, path+".calls.json"),
	}
	if _, err := costOnly.reserve(context.Background(), generationAcceptanceModel, 100); err == nil {
		t.Fatal("blocked legacy ledger was migrated")
	}
	after, err := readAcceptanceLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Profile != generationAcceptanceProfile || !after.Blocked || len(after.Requests) != 1 {
		t.Fatalf("blocked history changed: %+v", after)
	}
}

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
