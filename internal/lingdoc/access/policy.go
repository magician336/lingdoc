// Package access owns the application-level authorization and currentness
// seam shared by LingDoc use cases. Concrete storage and source providers
// stay behind this port.
package access

import "context"

// LingDocAccessContext is the authenticated identity carried through a
// LingDoc application use case. ProjectID is repeated here deliberately: it
// prevents a policy implementation from having to recover the resource from
// an unrelated request argument.
type LingDocAccessContext struct {
	TenantID  uint64
	UserID    string
	Role      string
	ProjectID string
}

// CurrentnessKind identifies the immutable input whose live state is checked.
type CurrentnessKind string

const (
	GenerationInput CurrentnessKind = "generation_input"
	DeliveryInput   CurrentnessKind = "delivery_input"
)

// AssetVersion is the source asset revision captured by a frozen input.
type AssetVersion struct {
	AssetID  string
	Revision int
}

// Source is the source identity and locator captured by a delivery input.
type Source struct {
	ID             string
	ProjectID      string
	AssetID        string
	AssetRevision  int
	Locator        string
	QuotedTextHash string
}

// ChapterVersion is the version identity captured by a delivery input.
type ChapterVersion struct {
	ChapterID        string
	ChapterVersionID *string
}

// CurrentnessInput contains only the immutable facts required to compare a
// queued or frozen input with the live workspace. It intentionally contains
// no provider or database types.
type CurrentnessInput struct {
	Kind             CurrentnessKind
	ProjectID        string
	ProjectVersion   int64
	SpecRevision     int64
	ChapterID        string
	ChapterVersionID *string
	TemplateID       string
	TemplateVersion  string
	RulesetHash      string
	AssetVersions    []AssetVersion
	Chapters         []ChapterVersion
	Sources          []Source
}

// Policy is the sole application-level port for LingDoc authorization and
// immutable-input currentness. Use cases should not combine separate
// project authorizers and source checkers at their composition roots.
type Policy interface {
	Authorize(context.Context, LingDocAccessContext, string) error
	Current(context.Context, LingDocAccessContext, CurrentnessInput) (bool, error)
}
