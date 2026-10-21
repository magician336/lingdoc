package workspace

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/membership"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// The legacy wide ports are gone.
//
// ApplicationService (37 methods), WorkspaceReadApplication (12) and
// WorkspaceWriteApplication (22) used to be the transport's entry into the
// workspace. They are retired: every consumer now holds the narrow port of
// the context it belongs to, and the composition root no longer infers one
// port from another (Spec #47 决策 34 与决策 37).

// ProjectApplication is the focused application port for project lifecycle
// and project preparation use cases. It is intentionally smaller than the
// legacy ApplicationService so a project route does not depend on chapter,
// membership, or change-set operations.
type ProjectApplication = core.ProjectApplication

// MembershipApplication is the focused application port for member and
// Owner lifecycle operations.
type MembershipApplication = membership.Application

// ChapterApplication is the focused application port for chapter editing and
// working-copy versioning. Rewrite application stays here because it mutates
// the same working-copy aggregate and shares its replay/source contract.
type ChapterApplication = core.ChapterApplication

// TemplateApplication is the focused application port for template previews,
// migration, and project-owned template copies.
type TemplateApplication = core.TemplateApplication

// ChangeSetApplication is the focused application port for context-change
// assessment and its explicit apply/reject lifecycle.
type ChangeSetApplication = core.ChangeSetApplication

// SourceApplicationService owns the source and asset use cases consumed by
// workspace HTTP routes. Keeping these operations behind one port prevents
// the transport from composing authorization, catalog reads, and search.
type SourceApplicationService interface {
	ListAssets(context.Context, Actor, string) ([]evidence.Asset, error)
	BindAsset(context.Context, Actor, string, string, string) (evidence.Asset, bool, error)
	GetSource(context.Context, Actor, string, string) (evidence.Source, error)
	GetSourceContext(context.Context, Actor, string, string) (map[string]any, error)
	RetrieveSources(context.Context, Actor, string, RetrieveSourcesInput) ([]evidence.Source, error)
	AccessStatus(context.Context, Actor, string) (AccessStatus, error)
}

// ProjectAuthorizer is the minimal workspace access port needed by asset and
// source use cases. It deliberately does not call source authorization itself.
//
// It is the same port as core.CapabilityAuthorizer: the owner of the
// authorization language declares it, and this alias keeps the transport's
// own name for it.
type ProjectAuthorizer = core.CapabilityAuthorizer

type RetrieveSourcesInput struct {
	Query    string   `json:"query"`
	AssetIDs []string `json:"asset_ids"`
}

type AccessStatus struct {
	ProjectID        string   `json:"project_id"`
	ContentAccess    string   `json:"content_access"`
	RecoveryActions  []string `json:"recovery_actions"`
	CanCreateProject bool     `json:"can_create_project"`
}

// DeniedAssetsError preserves item-level authorization reasons for the HTTP
// adapter while keeping Gin/HTTP response types out of the application layer.
type DeniedAssetsError struct {
	Denied []evidence.DeniedAsset
}

func (e *DeniedAssetsError) Error() string { return "one or more requested assets are unavailable" }

// BindingStore and SourceCatalog are replaceable ports around evidence
// persistence and the underlying knowledge/chunk catalog.
type BindingStore interface {
	evidence.BindingSource
	evidence.RevisionReader
	BindIdempotent(context.Context, uint64, string, string, string, evidence.BindInput) (evidence.Asset, bool, error)
	AssetForKnowledge(context.Context, string, string) (evidence.Asset, error)
	AssetScope(context.Context, string, string) (evidence.AssetScopeRef, error)
}

type SourceCatalog interface {
	Knowledge(context.Context, string) (*SourceKnowledge, error)
	Chunk(context.Context, string) (*SourceChunk, error)
}

type SourceKnowledge struct {
	ID              string
	TenantID        uint64
	KnowledgeBaseID string
	Title           string
	ParseStatus     string
	FileHash        string
	FileSize        int64
	ProcessedAt     *time.Time
}

type SourceChunk struct {
	ID              string
	KnowledgeID     string
	ChunkIndex      int
	StartAt         int
	EndAt           int
	Content         string
	ContentRevision int
}

type KnowledgeSearchService interface {
	GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error)
	HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error)
}

// HandlerDependencies is what the workspace transport needs in order to serve
// its routes.
//
// Six ports are required. Each one backs a family of routes that cannot
// answer without it, so a handler assembled without it would answer a
// request with a nil dereference instead of a refusal. The composition root
// therefore fails at assembly time when one of them is missing: a service that
// cannot serve a route must not start.
//
// The ports are named, not inferred: the composition root hands over the
// project, membership, chapter, template and change set families, and the
// source family. Nothing is deduced from another port, so a capability has
// exactly one injection point (Spec #47 决策 37).
//
// The remaining ports are optional, and the difference is stated here rather
// than left to a nil check at the call site:
//
//   - Integration supplies the shared source recheck and the frozen-input
//     builder. Without it the workspace does not offer the delivery input
//     assembly, and the delivery surface answers that it is unavailable.
//   - Templates reads template definitions. Without it a fixed demo template
//     is used, so the template routes still answer.
//   - SelectedRewrites, the machine reads and the review anchors are enabled
//     by calling the With... methods below. Not calling them means those
//     endpoints are not registered - a decision the caller makes by writing
//     the line, not by leaving a field empty.
type HandlerDependencies struct {
	FullWorkflow *FullWorkflowDependencies
	Projects     ProjectApplication
	Members      MembershipApplication
	Chapters     ChapterApplication
	TemplateApp  TemplateApplication
	ChangeSets   ChangeSetApplication
	Sources      SourceApplicationService
	Integration  WorkspaceIntegration
	Templates    delivery.TemplateReader

	// The optional capabilities, as the caller enabled them. They are private
	// so the With... methods below are the only way to turn one on: an
	// endpoint's presence follows the caller's explicit choice.
	selectedRewrites SelectedRewriteApplication
	machineReads     MachineReadApplication
	anchorsRead      ReviewAnchorReadApplication
	anchorsWrite     ReviewAnchorWriteApplication
}

// WithSelectedRewrites enables the rewrite endpoints.
//
// The endpoints exist only when the caller asks for them. Registering them
// because a field happened to be filled in would make an endpoint's presence
// an accident; asking for them makes it a decision.
func (d *HandlerDependencies) WithSelectedRewrites(app SelectedRewriteApplication) *HandlerDependencies {
	d.selectedRewrites = app
	return d
}

// WithMachineReads enables the machine read endpoints.
func (d *HandlerDependencies) WithMachineReads(app MachineReadApplication) *HandlerDependencies {
	d.machineReads = app
	return d
}

// WithReviewAnchors enables the review anchor endpoints.
func (d *HandlerDependencies) WithReviewAnchors(read ReviewAnchorReadApplication, write ReviewAnchorWriteApplication) *HandlerDependencies {
	d.anchorsRead = read
	d.anchorsWrite = write
	return d
}

// WorkspaceIntegration supplies the same source recheck and frozen-input adapter to all consumers.
type WorkspaceIntegration interface {
	CandidateAdoptionSourcePolicy() candidateadoption.SourcePolicy
	WorkspaceSourcePolicy() SourcePolicy
	DeliveryInputBuilder() DeliveryInputAssembler
}
