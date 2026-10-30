package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// Marshal real response types, then check their nested snapshot properties
// against the published strict response schema, including referenced refs.
func TestAcademicSnapshotResponseMatchesPublishedSourceContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "08-本轮实施方案", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err = json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		AdditionalProperties bool                       `json:"additionalProperties"`
	}
	if err = json.Unmarshal(doc.Components.Schemas["AuthorizedSourceSnapshot"], &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties {
		t.Fatal("source contract unexpectedly allows undeclared fields")
	}
	var nested struct {
		Properties map[string]struct {
			Ref string `json:"$ref"`
		} `json:"properties"`
		Required             []string `json:"required"`
		AdditionalProperties bool     `json:"additionalProperties"`
	}
	if err = json.Unmarshal(schema.Properties["academic_snapshot"], &nested); err != nil {
		t.Fatal(err)
	}
	if nested.AdditionalProperties || len(nested.Required) != 2 {
		t.Fatal("snapshot identity contract is not strict")
	}
	for _, key := range []string{"raw_ref", "text_ref"} {
		if nested.Properties[key].Ref != "#/components/schemas/AcademicOriginalSnapshotRef" {
			t.Fatalf("%s lacks snapshot ref contract", key)
		}
	}
	ref := core.AcademicOriginalSnapshotRef{SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 12}
	snapshot := RewriteSourceSnapshot{SourceID: "source", AssetID: "asset", AssetRevision: 1, Locator: "page 1", QuotedTextHash: ref.SHA256, AuthorizedAt: time.Now().UTC(), AcademicSnapshot: &academicSnapshotIdentity{RawRef: ref, TextRef: ref}}
	for name, response := range map[string]any{
		"candidate": SelectedRewriteCandidate{AuthorizedSources: []RewriteSourceSnapshot{snapshot}},
		"review":    ReviewReport{AuthorizedSources: []RewriteSourceSnapshot{snapshot}},
	} {
		raw, e := json.Marshal(response)
		if e != nil {
			t.Fatal(e)
		}
		var actual struct {
			Sources []map[string]json.RawMessage `json:"authorized_sources"`
		}
		if e = json.Unmarshal(raw, &actual); e != nil || len(actual.Sources) != 1 {
			t.Fatalf("%s: invalid response: %v", name, e)
		}
		for key := range actual.Sources[0] {
			if _, ok := schema.Properties[key]; !ok {
				t.Fatalf("%s emitted undeclared source field %s", name, key)
			}
		}
		var identity map[string]json.RawMessage
		if e = json.Unmarshal(actual.Sources[0]["academic_snapshot"], &identity); e != nil || len(identity) != 2 {
			t.Fatalf("%s identity invalid: %v", name, e)
		}
		for _, key := range nested.Required {
			if _, ok := identity[key]; !ok {
				t.Fatalf("%s missing %s", name, key)
			}
		}
	}
}
