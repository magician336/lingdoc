// Package template defines the versioned template contract shared by LingDoc
// consumers. It deliberately contains no transport or storage dependency.
package template

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
	Sections       []Section `json:"sections"`
	RequiredFields []string  `json:"required_fields"`
	RulesetHash    string    `json:"ruleset_hash"`
	Rules          []Rule    `json:"rules"`
}

// Section identifies one required chapter in a template.
type Section struct {
	ID       string `json:"section_id"`
	Title    string `json:"title"`
	Required bool   `json:"required"`
}

// Rule describes a deterministic delivery check. Parameters deliberately keep
// JSON-shaped values so later rules can use typed configuration without a DTO
// redesign.
type Rule struct {
	ID         string         `json:"rule_id"`
	Kind       string         `json:"kind"`
	Severity   string         `json:"severity"`
	Evaluator  string         `json:"evaluator"`
	Parameters map[string]any `json:"parameters"`
}
