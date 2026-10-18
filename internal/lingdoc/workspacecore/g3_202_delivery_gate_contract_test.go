package workspacecore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// G3-198.4 acceptance #1: STABILIZED, the frozen snapshot, READY/preflight and
// export must stay independently expressible. The OpenAPI contract is the
// shared surface the client types are generated from, so each of those four
// concepts needs its own published path or schema rather than being folded
// into one delivery status string.

func g3202Contract(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "08-本轮实施方案", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestG3202ContractExposesStabilizedAsItsOwnOperation(t *testing.T) {
	doc := g3202Contract(t)
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("contract has no paths object")
	}
	for _, name := range []string{
		"/projects/{projectId}/change-sets/{changeSetId}/stabilize",
		"/projects/{projectId}/change-sets/{changeSetId}/preflight",
		"/projects/{projectId}/check-receipts/{receiptId}",
	} {
		if _, exists := paths[name]; !exists {
			t.Errorf("contract is missing the %s operation", name)
		}
	}
	schemas, ok := doc["components"].(map[string]any)["schemas"].(map[string]any)
	if !ok {
		t.Fatal("contract has no components.schemas object")
	}
	for _, name := range []string{"StabilizationResult", "ImpactBlocker", "ImpactComputationReceipt"} {
		if _, exists := schemas[name]; !exists {
			t.Errorf("contract is missing the %s schema", name)
		}
	}
}

// The ChangeSet status enum must keep `stabilized` distinct from `applied`, so
// a client can tell "Owner applied this" from "Owner also stabilized it"
// without re-deriving the delivery gate.
func TestG3202ContractKeepsStabilizedDistinctFromApplied(t *testing.T) {
	doc := g3202Contract(t)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	changeSet, ok := schemas["ChangeSet"].(map[string]any)
	if !ok {
		t.Fatal("contract has no ChangeSet schema")
	}
	status, ok := changeSet["properties"].(map[string]any)["status"].(map[string]any)
	if !ok {
		t.Fatal("ChangeSet.status is not an object schema")
	}
	values, ok := status["enum"].([]any)
	if !ok {
		t.Fatal("ChangeSet.status has no enum")
	}
	seen := map[string]bool{}
	for _, value := range values {
		seen[value.(string)] = true
	}
	for _, want := range []string{"applied", "stabilized"} {
		if !seen[want] {
			t.Errorf("ChangeSet.status enum is missing the distinct %q value", want)
		}
	}
}

// The stabilization result itself must publish both terminal states so the UI
// renders the server's verdict rather than inferring it from other endpoints.
func TestG3202ContractPublishesBothStabilizationTerminalStates(t *testing.T) {
	doc := g3202Contract(t)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	result, ok := schemas["StabilizationResult"].(map[string]any)
	if !ok {
		t.Fatal("contract has no StabilizationResult schema")
	}
	status, ok := result["properties"].(map[string]any)["status"].(map[string]any)
	if !ok {
		t.Fatal("StabilizationResult.status is not an object schema")
	}
	values, ok := status["enum"].([]any)
	if !ok {
		t.Fatal("StabilizationResult.status has no enum")
	}
	seen := map[string]bool{}
	for _, value := range values {
		seen[value.(string)] = true
	}
	for _, want := range []string{"BLOCKED", "STABILIZED"} {
		if !seen[want] {
			t.Errorf("StabilizationResult.status enum is missing %q", want)
		}
	}
	if _, ok := result["properties"].(map[string]any)["blockers"]; !ok {
		t.Error("StabilizationResult must publish blockers so a client never has to infer them")
	}
}
