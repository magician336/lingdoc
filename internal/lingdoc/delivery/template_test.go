package delivery

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
)

// This is the type T07, T10, and T13 consume. Keep it as a compile-time test:
// a same-looking interface declared in another package is not sufficient when
// its Get method returns a different named Template type.
var _ lingdoctemplate.Reader = NewFixedTemplateReader()

func TestFixedTemplateReaderReturnsDemoContract(t *testing.T) {
	reader := NewFixedTemplateReader()
	template, err := reader.Get(DemoTemplateID, DemoTemplateVersion)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	want := Template{
		ID:             "template-demo",
		Name:           "演示模板：研究问题与研究方案",
		Version:        "1",
		IsDemo:         true,
		Source:         "lingdoc_builtin",
		Scope:          "internal-demo",
		Sections:       []Section{{ID: "question", Title: "研究问题", Order: 1, Required: true}, {ID: "method", Title: "研究方案", Order: 2, Required: true}},
		Fields:         []lingdoctemplate.Field{{ID: "research_subject", Label: "研究对象", Type: "string", Order: 1, Required: true}, {ID: "research_goal", Label: "研究目标", Type: "string", Order: 2, Required: true}},
		RequiredFields: []string{"research_subject", "research_goal"},
		RulesetHash:    "ad814c1ffba1956c0654abd1fc7fc48fad526109f43406633f05861116ec15a1",
		Rules: []Rule{
			{ID: "required-fields", Kind: "computed", Severity: "blocking", Evaluator: "required_fields", Parameters: map[string]any{}},
			{ID: "chapter-nonempty", Kind: "computed", Severity: "blocking", Evaluator: "nonempty_chapter", Parameters: map[string]any{}},
			{ID: "chapter-confirmed", Kind: "computed", Severity: "blocking", Evaluator: "confirmed_chapter", Parameters: map[string]any{}},
			{ID: "review-items-decided", Kind: "computed", Severity: "blocking", Evaluator: "review_items_decided", Parameters: map[string]any{}},
			{ID: "source-available", Kind: "computed", Severity: "blocking", Evaluator: "source_available", Parameters: map[string]any{}},
		},
	}
	if !reflect.DeepEqual(template, want) {
		t.Fatalf("Get() = %#v, want %#v", template, want)
	}
}

func TestFixedTemplateReaderRejectsUnknownOrUnversionedTemplate(t *testing.T) {
	reader := NewFixedTemplateReader()
	for _, request := range []struct{ id, version string }{
		{id: "unknown", version: DemoTemplateVersion},
		{id: DemoTemplateID, version: ""},
		{id: DemoTemplateID, version: "99"},
	} {
		_, err := reader.Get(request.id, request.version)
		if !errors.Is(err, ErrTemplateNotFound) {
			t.Errorf("Get(%q, %q) error = %v, want ErrTemplateNotFound", request.id, request.version, err)
		}
	}
}

// The demo catalog has two immutable versions. The published one is the
// contract version; the second one declares the required route node. Both are
// served, and each of them keeps its own identity.
func TestFixedTemplateReaderServesBothDemoVersions(t *testing.T) {
	reader := NewFixedTemplateReader()
	published, err := reader.Get(DemoTemplateID, DemoTemplateVersion)
	if err != nil {
		t.Fatalf("Get(%q, %q) error = %v", DemoTemplateID, DemoTemplateVersion, err)
	}
	if published.Version != DemoTemplateVersion {
		t.Fatalf("published version = %q, want %q", published.Version, DemoTemplateVersion)
	}
	route, err := reader.Get(DemoTemplateID, lingdoctemplate.DemoRouteTemplateVersion)
	if err != nil {
		t.Fatalf("Get(%q, %q) error = %v", DemoTemplateID, lingdoctemplate.DemoRouteTemplateVersion, err)
	}
	if route.Version != lingdoctemplate.DemoRouteTemplateVersion {
		t.Fatalf("route version = %q, want %q", route.Version, lingdoctemplate.DemoRouteTemplateVersion)
	}
	if !slices.Contains(route.RequiredFields, lingdoctemplate.DemoRequiredRouteNodeID) {
		t.Fatalf("the route template version must declare the required route node: %+v", route.RequiredFields)
	}
	if slices.Contains(published.RequiredFields, lingdoctemplate.DemoRequiredRouteNodeID) {
		t.Fatalf("the published template version must not declare a route node: %+v", published.RequiredFields)
	}
}

func TestFixedTemplateReaderReturnsIndependentCopies(t *testing.T) {
	reader := NewFixedTemplateReader()
	first, err := reader.Get(DemoTemplateID, DemoTemplateVersion)
	if err != nil {
		t.Fatalf("first Get() error = %v", err)
	}
	first.Sections[0].Title = "mutated"
	first.RequiredFields[0] = "mutated"
	first.Rules[0].Parameters["nested"] = map[string]any{"value": "mutated"}

	second, err := reader.Get(DemoTemplateID, DemoTemplateVersion)
	if err != nil {
		t.Fatalf("second Get() error = %v", err)
	}
	if second.Sections[0].Title != "研究问题" || second.RequiredFields[0] != "research_subject" {
		t.Fatalf("second Get() reused mutable template data: %#v", second)
	}
	if _, ok := second.Rules[0].Parameters["nested"]; ok {
		t.Fatalf("second Get() reused mutable rule parameters: %#v", second.Rules[0].Parameters)
	}
}

func TestCloneTemplateCopiesNestedRuleParameters(t *testing.T) {
	original := map[string]any{
		"nested": map[string]any{"items": []any{map[string]any{"value": "original"}}},
	}
	cloned := lingdoctemplate.Clone(lingdoctemplate.Template{Rules: []lingdoctemplate.Rule{{Parameters: original}}})
	copy := cloned.Rules[0].Parameters
	copy["nested"].(map[string]any)["items"].([]any)[0].(map[string]any)["value"] = "mutated"

	if got := original["nested"].(map[string]any)["items"].([]any)[0].(map[string]any)["value"]; got != "original" {
		t.Fatalf("cloneParameters mutated source nested value: %q", got)
	}
}
