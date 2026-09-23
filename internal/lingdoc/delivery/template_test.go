package delivery

import (
	"errors"
	"reflect"
	"testing"
)

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
		Sections:       []Section{{ID: "question", Title: "研究问题", Required: true}, {ID: "method", Title: "研究方案", Required: true}},
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
		{id: DemoTemplateID, version: "2"},
	} {
		_, err := reader.Get(request.id, request.version)
		if !errors.Is(err, ErrTemplateNotFound) {
			t.Errorf("Get(%q, %q) error = %v, want ErrTemplateNotFound", request.id, request.version, err)
		}
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
