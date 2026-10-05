// Package template defines the versioned template contract shared by LingDoc
// consumers. It deliberately contains no transport or storage dependency.
package template

import (
	"errors"
	"fmt"
)

// Reader resolves one exact immutable template version. Callers must always
// pass the version persisted by their project, candidate, or release snapshot;
// an empty version must not mean "latest".
type Reader interface {
	Get(id, version string) (Template, error)
}

// Template is a versioned writing skeleton and its deterministic rules. It is
// explicitly marked as a demo and must never be presented as a formal grant
// application template.
type Template struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Version        string    `json:"version"`
	IsDemo         bool      `json:"is_demo"`
	Source         string    `json:"source,omitempty"`
	Scope          string    `json:"scope,omitempty"`
	Sections       []Section `json:"sections"`
	Fields         []Field   `json:"fields,omitempty"`
	Terms          []Term    `json:"terms,omitempty"`
	RequiredFields []string  `json:"required_fields"`
	RulesetHash    string    `json:"ruleset_hash"`
	ContentHash    string    `json:"content_hash,omitempty"`
	Rules          []Rule    `json:"rules"`
}

// Section identifies one required chapter in a template.
type Section struct {
	ID          string `json:"section_id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Order       int    `json:"order,omitempty"`
	Required    bool   `json:"required"`
}

// Field is a stable project-spec field definition. Labels are presentation
// only; persisted values and migrations always use ID.
type Field struct {
	ID          string   `json:"field_id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type"`
	Order       int      `json:"order,omitempty"`
	Required    bool     `json:"required"`
	Options     []string `json:"options,omitempty"`
}

// Term is an approved spelling and the variants that should trigger a
// deterministic consistency issue. It never performs an automatic rewrite.
type Term struct {
	ID        string   `json:"term_id"`
	Preferred string   `json:"preferred"`
	Variants  []string `json:"variants"`
}

// Rule describes a deterministic delivery check. Parameters deliberately keep
// JSON-shaped values so later rules can use typed configuration without a DTO
// redesign.
type Rule struct {
	ID         string         `json:"rule_id"`
	Kind       string         `json:"kind"`
	Severity   string         `json:"severity"`
	Evaluator  string         `json:"evaluator"`
	Message    string         `json:"message,omitempty"`
	Parameters map[string]any `json:"parameters"`
}

var ErrVersionNotFound = errors.New("published template version not found")

const (
	DemoTemplateID       = "template-demo"
	DemoTemplateVersion  = "1"
	DemoRulesetHash      = "ad814c1ffba1956c0654abd1fc7fc48fad526109f43406633f05861116ec15a1"
	SeverityBlocking     = "blocking"
	SeverityWarning      = "warning"
	SeverityInfo         = "info"
	RuleRequiredFields   = "required-fields"
	RuleChapterNonempty  = "chapter-nonempty"
	RuleChapterConfirmed = "chapter-confirmed"
	RuleReviewItems      = "review-items-decided"
	RuleSourceAvailable  = "source-available"
)

// FixedDemoReader is the immutable T02 seed catalog. G4 copies this exact
// version; the exported version itself is never edited in place.
type FixedDemoReader struct{}

func (FixedDemoReader) Get(id, version string) (Template, error) {
	if id != DemoTemplateID || version != DemoTemplateVersion {
		return Template{}, fmt.Errorf("%w: %q version %q", ErrVersionNotFound, id, version)
	}
	return DemoTemplate(), nil
}

func DemoTemplate() Template {
	return Template{
		ID: DemoTemplateID, Name: "演示模板：研究问题与研究方案", Version: DemoTemplateVersion,
		IsDemo: true, Source: "lingdoc_builtin", Scope: "internal-demo",
		Sections: []Section{
			{ID: "question", Title: "研究问题", Order: 1, Required: true},
			{ID: "method", Title: "研究方案", Order: 2, Required: true},
		},
		Fields: []Field{
			{ID: "research_subject", Label: "研究对象", Type: "string", Order: 1, Required: true},
			{ID: "research_goal", Label: "研究目标", Type: "string", Order: 2, Required: true},
		},
		RequiredFields: []string{"research_subject", "research_goal"}, RulesetHash: DemoRulesetHash,
		Rules: []Rule{
			{ID: RuleRequiredFields, Kind: "computed", Severity: SeverityBlocking, Evaluator: "required_fields", Parameters: map[string]any{}},
			{ID: RuleChapterNonempty, Kind: "computed", Severity: SeverityBlocking, Evaluator: "nonempty_chapter", Parameters: map[string]any{}},
			{ID: RuleChapterConfirmed, Kind: "computed", Severity: SeverityBlocking, Evaluator: "confirmed_chapter", Parameters: map[string]any{}},
			{ID: RuleReviewItems, Kind: "computed", Severity: SeverityBlocking, Evaluator: "review_items_decided", Parameters: map[string]any{}},
			{ID: RuleSourceAvailable, Kind: "computed", Severity: SeverityBlocking, Evaluator: "source_available", Parameters: map[string]any{}},
		},
	}
}

func cloneTemplate(template Template) Template {
	copy := template
	copy.Sections = append([]Section(nil), template.Sections...)
	copy.Fields = append([]Field(nil), template.Fields...)
	for i := range copy.Fields {
		copy.Fields[i].Options = append([]string(nil), copy.Fields[i].Options...)
	}
	copy.Terms = append([]Term(nil), template.Terms...)
	for i := range copy.Terms {
		copy.Terms[i].Variants = append([]string(nil), copy.Terms[i].Variants...)
	}
	copy.RequiredFields = append([]string(nil), template.RequiredFields...)
	copy.Rules = make([]Rule, len(template.Rules))
	for i, rule := range template.Rules {
		copy.Rules[i] = rule
		copy.Rules[i].Parameters = cloneParameters(rule.Parameters)
	}
	return copy
}

// Clone makes an isolated copy of a template definition for immutable snapshots.
func Clone(template Template) Template { return cloneTemplate(template) }

func cloneParameters(parameters map[string]any) map[string]any {
	if parameters == nil {
		return nil
	}
	copy := make(map[string]any, len(parameters))
	for key, value := range parameters {
		copy[key] = cloneParameterValue(value)
	}
	return copy
}

func cloneParameterValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneParameters(value)
	case []any:
		copy := make([]any, len(value))
		for i, item := range value {
			copy[i] = cloneParameterValue(item)
		}
		return copy
	case []string:
		return append([]string(nil), value...)
	default:
		return value
	}
}
