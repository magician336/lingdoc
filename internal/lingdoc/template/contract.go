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
	ID             string
	Name           string
	Version        string
	IsDemo         bool
	Sections       []Section
	RequiredFields []string
	RulesetHash    string
	Rules          []Rule
}

// Section identifies one required chapter in a template.
type Section struct {
	ID       string
	Title    string
	Required bool
}

// Rule describes a deterministic delivery check. Parameters deliberately keep
// JSON-shaped values so later rules can use typed configuration without a DTO
// redesign.
type Rule struct {
	ID         string
	Kind       string
	Severity   string
	Evaluator  string
	Parameters map[string]any
}
