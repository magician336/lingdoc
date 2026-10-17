package workspacecore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Contract examples are consumed independently of HTTP fixture success. New
// frozen provenance must obey the actual server digest/value-binding policy.
func TestOpenAPIFrozenProvenanceExamplesMatchServerContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "08-本轮实施方案", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	count := 0
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if frozen, ok := v["frozen_input"].(map[string]any); ok {
				if provenance, exists := frozen["spec_provenance"]; exists {
					encoded, err := json.Marshal(provenance)
					if err != nil {
						t.Fatal(err)
					}
					var snapshot ProjectProvenanceSnapshot
					if err := json.Unmarshal(encoded, &snapshot); err != nil {
						t.Fatal(err)
					}
					encoded, err = json.Marshal(frozen["spec"])
					if err != nil {
						t.Fatal(err)
					}
					var spec map[string]string
					if err := json.Unmarshal(encoded, &spec); err != nil {
						t.Fatal(err)
					}
					if err := ValidateFrozenProvenanceInput(spec, snapshot); err != nil {
						t.Fatalf("example %v: invalid server provenance: %v", v["id"], err)
					}
					count++
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(doc["paths"])
	if count == 0 {
		t.Fatal("no frozen-provenance examples checked")
	}
}
