// Package delivery contains the small delivery-domain contracts shared by the
// LingDoc internal demo.  Transport registration belongs to the T01 module
// integration work; callers use these contracts directly in the meantime.
package delivery

import (
	"errors"
	"fmt"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
)

const (
	// DemoTemplateID is the only template available in the internal demo.
	DemoTemplateID = "template-demo"
	// DemoTemplateVersion is intentionally fixed: this demo does not support
	// changing templates for a project at runtime.
	DemoTemplateVersion = "1"
	// DemoRulesetHash is the ruleset fingerprint published in the LingDoc
	// OpenAPI proposal.  Consumers persist it with their own versioned data.
	DemoRulesetHash = "ad814c1ffba1956c0654abd1fc7fc48fad526109f43406633f05861116ec15a1"
)

// ErrTemplateNotFound is returned when the fixed reader cannot supply the
// requested immutable template version.
var ErrTemplateNotFound = errors.New("lingdoc template not found")

// TemplateReader is the shared consumer contract used by project, generation,
// and checking code. T07 must use this contract rather than redeclaring a
// look-alike interface: Go method return types must be identical.
type TemplateReader = lingdoctemplate.Reader

// These aliases keep the original T02 API stable while making the exact same
// types available to T07, T10, and T13 through internal/lingdoc/template.
type Template = lingdoctemplate.Template
type Section = lingdoctemplate.Section
type Rule = lingdoctemplate.Rule

// FixedTemplateReader provides the one versioned template used for the
// internal demonstration.
type FixedTemplateReader struct{}

// NewFixedTemplateReader creates the T02 fixed implementation.
func NewFixedTemplateReader() TemplateReader {
	return FixedTemplateReader{}
}

// DemoTemplate returns a copy of the internal-demo template. Consumers can
// safely keep or alter the returned value without changing future reads.
func DemoTemplate() Template {
	return cloneTemplate(demoTemplate)
}

// Get returns an exact template version. Both id and version are required so a
// caller cannot accidentally use the current template after recording a
// different version in a project, candidate or release snapshot.
func (FixedTemplateReader) Get(id, version string) (Template, error) {
	if id != DemoTemplateID || version != DemoTemplateVersion {
		return Template{}, fmt.Errorf("%w: %q version %q", ErrTemplateNotFound, id, version)
	}
	return DemoTemplate(), nil
}

var demoTemplate = Template{
	ID:      DemoTemplateID,
	Name:    "演示模板：研究问题与研究方案",
	Version: DemoTemplateVersion,
	IsDemo:  true,
	Sections: []Section{
		{ID: "question", Title: "研究问题", Required: true},
		{ID: "method", Title: "研究方案", Required: true},
	},
	RequiredFields: []string{"research_subject", "research_goal"},
	RulesetHash:    DemoRulesetHash,
	Rules: []Rule{
		{ID: "required-fields", Kind: "computed", Severity: "blocking", Evaluator: "required_fields", Parameters: map[string]any{}},
		{ID: "chapter-nonempty", Kind: "computed", Severity: "blocking", Evaluator: "nonempty_chapter", Parameters: map[string]any{}},
		{ID: "chapter-confirmed", Kind: "computed", Severity: "blocking", Evaluator: "confirmed_chapter", Parameters: map[string]any{}},
		{ID: "review-items-decided", Kind: "computed", Severity: "blocking", Evaluator: "review_items_decided", Parameters: map[string]any{}},
		{ID: "source-available", Kind: "computed", Severity: "blocking", Evaluator: "source_available", Parameters: map[string]any{}},
	},
}

func cloneTemplate(template Template) Template {
	copy := template
	copy.Sections = append([]Section(nil), template.Sections...)
	copy.RequiredFields = append([]string(nil), template.RequiredFields...)
	copy.Rules = make([]Rule, len(template.Rules))
	for i, rule := range template.Rules {
		copy.Rules[i] = rule
		copy.Rules[i].Parameters = cloneParameters(rule.Parameters)
	}
	return copy
}

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
	default:
		return value
	}
}
