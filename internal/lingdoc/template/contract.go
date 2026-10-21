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
	// RuleSetSnapshot pins the independently governed rule set used with this
	// template copy. It is a server-verified immutable published snapshot.
	RuleSetSnapshot *RuleSet `json:"ruleset_snapshot,omitempty"`
	// RuleSetRef is the immutable RuleSet identity pinned by this template
	// version. It stores identity/version/hash separately from a project copy's
	// snapshot and is exposed by checks and release records.
	RuleSetRef *RuleSetReference `json:"ruleset_ref,omitempty"`
	// RuleBindingOverrides are project-copy-only, allowlisted changes. They
	// never mutate the published RuleSet snapshot.
	RuleBindingOverrides []RuleBindingOverride `json:"ruleset_binding_overrides,omitempty"`
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
	// RuleRouteNodeRequired is the blocking presence rule bound to the route
	// node the demo template declares as required. It is the rule that turns
	// "the required route node is missing / not reviewed / behind" into a
	// blocking validation issue.
	RuleRouteNodeRequired = "route-node-required"
)

// DemoRouteTemplateVersion is the second immutable version of the demo
// template.
//
// Version 1 is published: it is frozen into the T13 wire contract
// (docs/08-本轮实施方案/contracts/frozen-input.canonical.json) and into the
// demo delivery fixtures, so it is never edited in place. Version 2 carries
// the one declaration AC-G3-08 needs and version 1 does not have: a required
// RouteNode. A new version is the only honest way to add a requirement to a
// published template.
//
// DemoRequiredRouteNodeID is the route node this template version declares as
// required. The id is the rule binding target: a route node whose id is in the
// template's required fields is required **by the template rules**, and the
// server computes the node's `required` flag from exactly that. A client can
// never state the flag itself.
//
// The identifier is lowercase because a rule binding target has to be a stable
// id in this codebase.
const (
	DemoRouteTemplateVersion = "2"
	DemoRequiredRouteNodeID  = "route-r7"
	// DemoRouteTemplateRulesetHash is the server-computed ruleset hash of
	// DemoRouteTemplate. Like DemoRulesetHash it is published: the rule set
	// identity is part of the wire contract of every evaluation.
	DemoRouteTemplateRulesetHash = "eb4106f0dfebcb5fc56a837e01e931e4d4f482dc99ffe8e4d6770272c4924139"
)

// FixedDemoReader is the immutable T02 seed catalog. G4 copies this exact
// version; the exported version itself is never edited in place.
type FixedDemoReader struct{}

func (FixedDemoReader) Get(id, version string) (Template, error) {
	if id != DemoTemplateID {
		return Template{}, fmt.Errorf("%w: %q version %q", ErrVersionNotFound, id, version)
	}
	switch version {
	case DemoTemplateVersion:
		return DemoTemplate(), nil
	case DemoRouteTemplateVersion:
		return DemoRouteTemplate(), nil
	default:
		return Template{}, fmt.Errorf("%w: %q version %q", ErrVersionNotFound, id, version)
	}
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

// DemoRouteTemplate is the second immutable version of the demo template.
//
// It is version 1 plus exactly one declaration: the route node
// DemoRequiredRouteNodeID is a required field, and the blocking presence rule
// RuleRouteNodeRequired is bound to it. Everything else is identical to
// version 1, so a project that uses this template behaves like a demo project
// with one extra required route.
//
// The route node is declared the way the architecture declares it: as a
// template field that is also a required field. The server reads the required
// fields of the project's template copy to decide whether a route node is
// required (see the roadmap module), so the requirement cannot be downgraded
// by a client that proposes the node.
func DemoRouteTemplate() Template {
	template := DemoTemplate()
	template.Version = DemoRouteTemplateVersion
	template.Fields = append(template.Fields, Field{
		ID: DemoRequiredRouteNodeID, Label: "技术路线 R7（演示必需节点）", Type: "text", Order: 3, Required: true,
	})
	template.RequiredFields = append(template.RequiredFields, DemoRequiredRouteNodeID)
	template.Rules = append(template.Rules, Rule{
		ID: RuleRouteNodeRequired, Kind: "presence", Severity: SeverityBlocking,
		Parameters: map[string]any{"target_kind": "field", "target_id": DemoRequiredRouteNodeID},
	})
	template.RulesetHash = DemoRouteTemplateRulesetHash
	return template
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
	if template.RuleSetSnapshot != nil {
		ruleSet := cloneRuleSet(*template.RuleSetSnapshot)
		copy.RuleSetSnapshot = &ruleSet
	}
	if template.RuleSetRef != nil {
		ruleSetRef := *template.RuleSetRef
		copy.RuleSetRef = &ruleSetRef
	}
	copy.RuleBindingOverrides = CloneRuleBindingOverrides(template.RuleBindingOverrides)
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
