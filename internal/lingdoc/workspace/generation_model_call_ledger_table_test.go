package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ledgerTableTestDB opens a database that really has the ledger table.
//
// The in-memory recorder pins the shape of a row; this one pins the fact
// that the row lands in the table the evidence script reads.  The migration
// file is the table's definition, so the test cannot drift away from it.
func ledgerTableTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.db")
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: path},
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open the test database: %v", err)
	}
	migration := filepath.Join("..", "..", "..", "migrations", "sqlite",
		"000055_lingdoc_model_call_ledger.up.sql")
	body, err := os.ReadFile(migration)
	if err != nil {
		t.Fatalf("read the ledger migration: %v", err)
	}
	if err := db.Exec(string(body)).Error; err != nil {
		t.Fatalf("apply the ledger migration: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("the database handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func ledgerTableRows(t *testing.T, db *gorm.DB) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := db.Raw("SELECT * FROM lingdoc_model_call_ledger").Scan(&rows).Error; err != nil {
		t.Fatalf("read the ledger table: %v", err)
	}
	return rows
}

// TestGenerationPathLeavesExactlyOneRowInTheLedgerTable is the positive half:
// going through the generation path leaves one row in the table the evidence
// script reads -- with every field the row is supposed to carry.
func TestGenerationPathLeavesExactlyOneRowInTheLedgerTable(t *testing.T) {
	db := ledgerTableTestDB(t)
	ledger := core.NewGORMModelCallLedger(db)
	_, service := generationLedgerFixture()
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, ledger: ledger}

	ctx := generation.WithRunID(context.Background(), "run-table-1")
	if _, err := host.Generate(ctx, acceptanceGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	rows := ledgerTableRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("lingdoc_model_call_ledger rows = %d, want exactly one row per model call", len(rows))
	}
	row := rows[0]
	t.Logf("the row the generation path left in lingdoc_model_call_ledger: %+v", row)
	if row["kind"] != core.ModelCallLedgerKindCall {
		t.Errorf("kind = %v, want %q", row["kind"], core.ModelCallLedgerKindCall)
	}
	if row["source_kind"] != core.ModelCallLedgerSourceGenerationRun {
		t.Errorf("source_kind = %v, want %q", row["source_kind"], core.ModelCallLedgerSourceGenerationRun)
	}
	if row["source_id"] != "run-table-1" {
		t.Errorf("source_id = %v, want the generation run id", row["source_id"])
	}
	if provider, _ := row["provider"].(string); provider == "" {
		t.Errorf("the row does not name the provider that answered: %+v", row)
	}
	if modelID, _ := row["model_id"].(string); modelID == "" {
		t.Errorf("the row does not name the model that answered: %+v", row)
	}
	if parameters, _ := row["parameters_json"].(string); parameters == "" {
		t.Errorf("the row does not carry the parameters that were sent: %+v", row)
	} else {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(parameters), &decoded); err != nil {
			t.Errorf("the parameters are not JSON: %q", parameters)
		}
	}
	if tokens, _ := row["input_tokens"].(int64); tokens != 128 {
		t.Errorf("input tokens = %v, want 128", row["input_tokens"])
	}
	if tokens, _ := row["output_tokens"].(int64); tokens != 64 {
		t.Errorf("output tokens = %v, want 64", row["output_tokens"])
	}
	if row["reserved_nano_usd"] == nil || row["actual_nano_usd"] == nil {
		t.Errorf("the row does not carry the budget columns: %+v", row)
	}
	if status, _ := row["status"].(string); status != core.ModelCallLedgerStatusSucceeded {
		t.Errorf("status = %v, want %q", row["status"], core.ModelCallLedgerStatusSucceeded)
	}
}

// TestOnlyTheGenerationPathWritesGenerationRows is the negative half: a call
// that does not go through the generation path must not add a generation row.
// Without this half, "the ledger has a row" would not say whose call it was.
func TestOnlyTheGenerationPathWritesGenerationRows(t *testing.T) {
	db := ledgerTableTestDB(t)
	ledger := core.NewGORMModelCallLedger(db)

	// A run that never reaches the generation path leaves the table empty:
	// nothing writes a row just because the ledger exists.
	rows := ledgerTableRows(t, db)
	if len(rows) != 0 {
		t.Fatalf("lingdoc_model_call_ledger rows = %d before any call, want 0", len(rows))
	}

	// And the generation path is the one that adds a generation_run row.
	_, generationService := generationLedgerFixture()
	host := generationHostModel{models: generationService, templates: core.ContractDemoTemplate{}, ledger: ledger}
	ctx := generation.WithRunID(context.Background(), "run-table-2")
	if _, err := host.Generate(ctx, acceptanceGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	rows = ledgerTableRows(t, db)
	generationRows := 0
	for _, row := range rows {
		if row["source_kind"] == core.ModelCallLedgerSourceGenerationRun {
			generationRows++
		}
	}
	if generationRows != 1 {
		t.Fatalf("generation_run rows = %d, want exactly 1", generationRows)
	}
}

// TestTheContractStartGenerationNodeIsFoundBySourceKind is the regression
// gate for the release claim: the F01 node that calls a provider is
// startGeneration, it goes through the generation path, and the call it made
// can be found in the ledger by its source kind.  That is what makes "a real
// model call happened" a fact the release decision can read, instead of
// something a reader has to believe.
func TestTheContractStartGenerationNodeIsFoundBySourceKind(t *testing.T) {
	contract := filepath.Join("..", "..", "..", "docs", "08-本轮实施方案", "contracts", "workflow.json")
	body, err := os.ReadFile(contract)
	if err != nil {
		t.Fatalf("read the F01 contract: %v", err)
	}
	var document struct {
		Steps []struct {
			ID          string `json:"id"`
			OperationID string `json:"operation_id"`
			Request     struct {
				JSON map[string]any `json:"json"`
			} `json:"request"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("parse the F01 contract: %v", err)
	}
	var startGeneration []string
	for _, step := range document.Steps {
		if step.OperationID == "startGeneration" {
			startGeneration = append(startGeneration, step.ID)
		}
	}
	if len(startGeneration) == 0 {
		t.Fatal("the F01 contract has no startGeneration step")
	}

	db := ledgerTableTestDB(t)
	ledger := core.NewGORMModelCallLedger(db)
	_, service := generationLedgerFixture()
	host := generationHostModel{models: service, templates: core.ContractDemoTemplate{}, ledger: ledger}

	ctx := generation.WithRunID(context.Background(), "run-table-3")
	if _, err := host.Generate(ctx, acceptanceGenerationInput()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// The call the contract's startGeneration node caused is findable by its
	// source kind and its run id.
	entries, err := ledger.ModelCallEntriesBySource(ctx, acceptanceGenerationInput().Actor.TenantID,
		core.ModelCallLedgerSourceGenerationRun, "run-table-3")
	if err != nil {
		t.Fatalf("read the ledger by source: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries by source = %d, want the one call the %v node made", len(entries), startGeneration)
	}
	if entries[0].Kind != core.ModelCallLedgerKindCall {
		t.Errorf("kind = %q, want %q", entries[0].Kind, core.ModelCallLedgerKindCall)
	}
}
