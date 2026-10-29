package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// The generic route scanner does not cover these legacy-handler routes.
// Compare their registered methods and OpenAPI surface to the actual Go DTOs.
func TestFieldArchiveOpenAPIContractMatchesRegisteredRoutes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "08-本轮实施方案", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	paths := document["paths"].(map[string]any)
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	h := newMigrationRiskHarness(t, nil)
	registered := map[string]bool{}
	for _, route := range h.router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	base := "/projects/{projectId}/template-copy/field-archives"
	for _, tc := range []struct {
		path, method, request, response string
		statuses                        []string
	}{
		{base, "GET", "", "FieldArchiveListResponse", []string{"200", "404"}},
		{base, "POST", "CreateFieldArchiveInput", "FieldArchiveReceiptResponse", []string{"201", "400", "403", "404", "409", "422"}},
		{base + "/{archiveId}", "GET", "", "FieldArchiveResponse", []string{"200", "404"}},
		{base + "/{archiveId}/remove", "POST", "RemoveArchivedFieldInput", "FieldArchiveReceiptResponse", []string{"200", "400", "403", "404", "409", "422"}},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			ginPath := strings.ReplaceAll(strings.ReplaceAll(tc.path, "{projectId}", ":projectId"), "{archiveId}", ":archiveId")
			if !registered[tc.method+" /api/v1/lingdoc"+ginPath] {
				t.Fatal("method/path missing from the actual handler")
			}
			path, ok := paths[tc.path].(map[string]any)
			if !ok {
				t.Fatal("path missing from OpenAPI")
			}
			op, ok := path[strings.ToLower(tc.method)].(map[string]any)
			if !ok {
				t.Fatal("method missing from OpenAPI")
			}
			foundKey := false
			for _, item := range op["parameters"].([]any) {
				parameter := item.(map[string]any)
				if parameter["name"] != "Idempotency-Key" {
					continue
				}
				schema := parameter["schema"].(map[string]any)
				foundKey = parameter["in"] == "header" && parameter["required"] == true &&
					schema["minLength"] == float64(operationcore.MinKeyLength) && schema["maxLength"] == float64(operationcore.MaxKeyLength)
			}
			if foundKey != (tc.method == "POST") {
				t.Fatalf("Idempotency-Key contract mismatch: found=%t", foundKey)
			}
			if tc.request != "" {
				body := op["requestBody"].(map[string]any)
				ref := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"]
				if body["required"] != true || ref != "#/components/schemas/"+tc.request {
					t.Fatalf("request schema mismatch: %v", ref)
				}
			}
			responses := op["responses"].(map[string]any)
			for _, status := range tc.statuses {
				if _, ok := responses[status]; !ok {
					t.Fatalf("documented response %s missing", status)
				}
			}
			success := responses[tc.statuses[0]].(map[string]any)
			ref := success["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"]
			if ref != "#/components/schemas/"+tc.response {
				t.Fatalf("response schema mismatch: %v", ref)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		typeOf reflect.Type
	}{
		{"CreateFieldArchiveInput", reflect.TypeOf(core.CreateFieldArchiveInput{})},
		{"RemoveArchivedFieldInput", reflect.TypeOf(core.RemoveArchivedFieldInput{})},
		{"FieldArchiveReceipt", reflect.TypeOf(core.FieldArchiveReceipt{})},
		{"FieldArchiveView", reflect.TypeOf(core.FieldArchiveView{})},
	} {
		properties := schemas[tc.name].(map[string]any)["properties"].(map[string]any)
		documented, actual := []string{}, []string{}
		for key := range properties {
			documented = append(documented, key)
		}
		for i := 0; i < tc.typeOf.NumField(); i++ {
			name := strings.Split(tc.typeOf.Field(i).Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				actual = append(actual, name)
			}
		}
		sort.Strings(documented)
		sort.Strings(actual)
		if !reflect.DeepEqual(documented, actual) {
			t.Errorf("%s properties documented=%v actual=%v", tc.name, documented, actual)
		}
	}
}
