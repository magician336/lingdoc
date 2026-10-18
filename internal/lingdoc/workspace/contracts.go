package workspace

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// ApplicationService is the HTTP-facing workspace contract. Tests and
// alternative transports can provide an implementation without a database.
type ApplicationService interface {
	Authorize(context.Context, Actor, string, string) error
	GetProject(context.Context, Actor, string) (Project, error)
	ListProjects(context.Context, Actor) ([]Project, bool, error)
	CreateProjectResult(context.Context, Actor, string, CreateProjectInput) (CreateProjectResult, error)
	SaveSpecResult(context.Context, Actor, string, string, SaveSpecInput) (SaveSpecResult, error)
	SaveMembersResult(context.Context, Actor, string, string, SaveMembersInput) (ProjectMutationResult, error)
	TransferOwnerResult(context.Context, Actor, string, string, TransferOwnerInput) (ProjectMutationResult, error)
	ActivateProjectResult(context.Context, Actor, string, string, ActivateProjectInput) (ProjectMutationResult, error)
	CreateDraftCandidateResult(context.Context, Actor, string, string, DraftCandidateInput) (DraftCandidateMutationResult, error)
	ListDraftCandidates(context.Context, Actor, string) ([]DraftCandidate, error)
	ListAuditEvents(context.Context, Actor, string) ([]AuditEvent, error)
	ActivationDiff(context.Context, Actor, string, int64) (ActivationDiff, error)
	RequestOwnerTransferResult(context.Context, Actor, string, string, OwnerTransferInput) (OwnerTransferResult, error)
	AcceptOwnerTransferResult(context.Context, Actor, string, string, string) (OwnerTransferResult, error)
	PreviewTemplateMigration(context.Context, Actor, string, TemplateMigrationInput) (TemplateMigrationPreview, error)
	ChangeTemplateResult(context.Context, Actor, string, string, TemplateMigrationInput) (ProjectMutationResult, error)
	GetTemplateCopy(context.Context, Actor, string, int64) (ProjectTemplateCopy, error)
	PreviewTemplateCopyEdit(context.Context, Actor, string, TemplateCopyDefinitionInput) (TemplateMigrationPreview, error)
	SaveTemplateCopyResult(context.Context, Actor, string, string, TemplateCopyDefinitionInput) (ProjectMutationResult, error)
	BindTemplateCopyRuleSetResult(context.Context, Actor, string, string, BindTemplateCopyRuleSetInput) (ProjectMutationResult, error)
	DiscardProjectResult(context.Context, Actor, string, string, int64) (ProjectMutationResult, error)
	RestoreProjectResult(context.Context, Actor, string, string, int64) (ProjectMutationResult, error)
	ListChapters(context.Context, Actor, string) ([]Chapter, error)
	SaveChapterResult(context.Context, Actor, string, string, string, SaveChapterInput) (ChapterMutationResult, error)
	GetWorkingCopy(context.Context, Actor, string, string) (WorkingCopy, error)
	SaveWorkingCopyResult(context.Context, Actor, string, string, string, SaveWorkingCopyInput) (WorkingCopyMutationResult, error)
	ApplyRewriteResult(context.Context, Actor, string, string, string, core.SaveWorkingCopyInput, []core.ReviewItem) (WorkingCopyMutationResult, error)
	CommitWorkingCopyResult(context.Context, Actor, string, string, string, CommitWorkingCopyInput) (CommittedChapterMutationResult, error)
	ListChapterVersions(context.Context, Actor, string, string) ([]ChapterVersion, error)
	RestoreWorkingCopyResult(context.Context, Actor, string, string, string, RestoreWorkingCopyInput) (WorkingCopyMutationResult, error)
	GenerationContext(context.Context, Actor, string, string) (GenerationContext, error)
	CreateChangeSetResult(context.Context, Actor, string, string, CreateChangeSetInput) (ChangeSetMutationResult, error)
	ListChangeSets(context.Context, Actor, string) ([]ChangeSet, error)
	GetChangeSet(context.Context, Actor, string, string) (ChangeSet, error)
	ReassessChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error)
	ApplyChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error)
	RejectChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error)
}

// WorkspaceReadApplication is the route-facing read port. Keeping this
// separate from writes lets transports register and test only the use cases
// they expose.
type WorkspaceReadApplication interface {
	ListProjects(context.Context, Actor) ([]Project, bool, error)
	GetProject(context.Context, Actor, string) (Project, error)
	ListDraftCandidates(context.Context, Actor, string) ([]DraftCandidate, error)
	ListAuditEvents(context.Context, Actor, string) ([]AuditEvent, error)
	ActivationDiff(context.Context, Actor, string, int64) (ActivationDiff, error)
	PreviewTemplateMigration(context.Context, Actor, string, TemplateMigrationInput) (TemplateMigrationPreview, error)
	GetTemplateCopy(context.Context, Actor, string, int64) (ProjectTemplateCopy, error)
	PreviewTemplateCopyEdit(context.Context, Actor, string, TemplateCopyDefinitionInput) (TemplateMigrationPreview, error)
	ListChapters(context.Context, Actor, string) ([]Chapter, error)
	GetWorkingCopy(context.Context, Actor, string, string) (WorkingCopy, error)
	ListChapterVersions(context.Context, Actor, string, string) ([]ChapterVersion, error)
	ListChangeSets(context.Context, Actor, string) ([]ChangeSet, error)
	GetChangeSet(context.Context, Actor, string, string) (ChangeSet, error)
}

// WorkspaceWriteApplication is the route-facing mutation port.
type WorkspaceWriteApplication interface {
	CreateProjectResult(context.Context, Actor, string, CreateProjectInput) (CreateProjectResult, error)
	SaveSpecResult(context.Context, Actor, string, string, SaveSpecInput) (SaveSpecResult, error)
	ActivateProjectResult(context.Context, Actor, string, string, ActivateProjectInput) (ProjectMutationResult, error)
	CreateDraftCandidateResult(context.Context, Actor, string, string, DraftCandidateInput) (DraftCandidateMutationResult, error)
	ChangeTemplateResult(context.Context, Actor, string, string, TemplateMigrationInput) (ProjectMutationResult, error)
	SaveTemplateCopyResult(context.Context, Actor, string, string, TemplateCopyDefinitionInput) (ProjectMutationResult, error)
	BindTemplateCopyRuleSetResult(context.Context, Actor, string, string, BindTemplateCopyRuleSetInput) (ProjectMutationResult, error)
	DiscardProjectResult(context.Context, Actor, string, string, int64) (ProjectMutationResult, error)
	RestoreProjectResult(context.Context, Actor, string, string, int64) (ProjectMutationResult, error)
	SaveMembersResult(context.Context, Actor, string, string, SaveMembersInput) (ProjectMutationResult, error)
	TransferOwnerResult(context.Context, Actor, string, string, TransferOwnerInput) (ProjectMutationResult, error)
	RequestOwnerTransferResult(context.Context, Actor, string, string, OwnerTransferInput) (OwnerTransferResult, error)
	AcceptOwnerTransferResult(context.Context, Actor, string, string, string) (OwnerTransferResult, error)
	SaveChapterResult(context.Context, Actor, string, string, string, SaveChapterInput) (ChapterMutationResult, error)
	SaveWorkingCopyResult(context.Context, Actor, string, string, string, SaveWorkingCopyInput) (WorkingCopyMutationResult, error)
	ApplyRewriteResult(context.Context, Actor, string, string, string, core.SaveWorkingCopyInput, []core.ReviewItem) (WorkingCopyMutationResult, error)
	CommitWorkingCopyResult(context.Context, Actor, string, string, string, CommitWorkingCopyInput) (CommittedChapterMutationResult, error)
	RestoreWorkingCopyResult(context.Context, Actor, string, string, string, RestoreWorkingCopyInput) (WorkingCopyMutationResult, error)
	CreateChangeSetResult(context.Context, Actor, string, string, CreateChangeSetInput) (ChangeSetMutationResult, error)
	ReassessChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error)
	ApplyChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error)
	RejectChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error)
}

var _ WorkspaceReadApplication = (*Service)(nil)
var _ WorkspaceWriteApplication = (*Service)(nil)

// ProjectApplication is the focused application port for project lifecycle
// and project preparation use cases. It is intentionally smaller than the
// legacy ApplicationService so a project route does not depend on chapter,
// membership, or change-set operations.
type ProjectApplication interface {
	ListProjects(context.Context, Actor) ([]Project, bool, error)
	GetProject(context.Context, Actor, string) (Project, error)
	CreateProjectResult(context.Context, Actor, string, CreateProjectInput) (CreateProjectResult, error)
	SaveSpecResult(context.Context, Actor, string, string, SaveSpecInput) (SaveSpecResult, error)
	ActivateProjectResult(context.Context, Actor, string, string, ActivateProjectInput) (ProjectMutationResult, error)
	ListDraftCandidates(context.Context, Actor, string) ([]DraftCandidate, error)
	CreateDraftCandidateResult(context.Context, Actor, string, string, DraftCandidateInput) (DraftCandidateMutationResult, error)
	ListAuditEvents(context.Context, Actor, string) ([]AuditEvent, error)
	ActivationDiff(context.Context, Actor, string, int64) (ActivationDiff, error)
	DiscardProjectResult(context.Context, Actor, string, string, int64) (ProjectMutationResult, error)
	RestoreProjectResult(context.Context, Actor, string, string, int64) (ProjectMutationResult, error)
}

// MembershipApplication is the focused application port for member and
// Owner lifecycle operations.
type MembershipApplication interface {
	SaveMembersResult(context.Context, Actor, string, string, SaveMembersInput) (ProjectMutationResult, error)
	TransferOwnerResult(context.Context, Actor, string, string, TransferOwnerInput) (ProjectMutationResult, error)
	RequestOwnerTransferResult(context.Context, Actor, string, string, OwnerTransferInput) (OwnerTransferResult, error)
	AcceptOwnerTransferResult(context.Context, Actor, string, string, string) (OwnerTransferResult, error)
}

// ChapterApplication is the focused application port for chapter editing and
// working-copy versioning. Rewrite application stays here because it mutates
// the same working-copy aggregate and shares its replay/source contract.
type ChapterApplication interface {
	ListChapters(context.Context, Actor, string) ([]Chapter, error)
	GetWorkingCopy(context.Context, Actor, string, string) (WorkingCopy, error)
	ListChapterVersions(context.Context, Actor, string, string) ([]ChapterVersion, error)
	SaveChapterResult(context.Context, Actor, string, string, string, SaveChapterInput) (ChapterMutationResult, error)
	SaveWorkingCopyResult(context.Context, Actor, string, string, string, SaveWorkingCopyInput) (WorkingCopyMutationResult, error)
	ApplyRewriteResult(context.Context, Actor, string, string, string, core.SaveWorkingCopyInput, []core.ReviewItem) (WorkingCopyMutationResult, error)
	CommitWorkingCopyResult(context.Context, Actor, string, string, string, CommitWorkingCopyInput) (CommittedChapterMutationResult, error)
	RestoreWorkingCopyResult(context.Context, Actor, string, string, string, RestoreWorkingCopyInput) (WorkingCopyMutationResult, error)
}

// TemplateApplication is the focused application port for template previews,
// migration, and project-owned template copies.
type TemplateApplication interface {
	PreviewTemplateMigration(context.Context, Actor, string, TemplateMigrationInput) (TemplateMigrationPreview, error)
	GetTemplateCopy(context.Context, Actor, string, int64) (ProjectTemplateCopy, error)
	PreviewTemplateCopyEdit(context.Context, Actor, string, TemplateCopyDefinitionInput) (TemplateMigrationPreview, error)
	ChangeTemplateResult(context.Context, Actor, string, string, TemplateMigrationInput) (ProjectMutationResult, error)
	SaveTemplateCopyResult(context.Context, Actor, string, string, TemplateCopyDefinitionInput) (ProjectMutationResult, error)
	BindTemplateCopyRuleSetResult(context.Context, Actor, string, string, BindTemplateCopyRuleSetInput) (ProjectMutationResult, error)
}

// ChangeSetApplication is the focused application port for context-change
// assessment and its explicit apply/reject lifecycle.
type ChangeSetApplication interface {
	CreateChangeSetResult(context.Context, Actor, string, string, CreateChangeSetInput) (ChangeSetMutationResult, error)
	ListChangeSets(context.Context, Actor, string) ([]ChangeSet, error)
	GetChangeSet(context.Context, Actor, string, string) (ChangeSet, error)
	ReassessChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error)
	ApplyChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error)
	RejectChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error)
}

var (
	_ ProjectApplication    = (*Service)(nil)
	_ MembershipApplication = (*Service)(nil)
	_ ChapterApplication    = (*Service)(nil)
	_ TemplateApplication   = (*Service)(nil)
	_ ChangeSetApplication  = (*Service)(nil)
)

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
type ProjectAuthorizer interface {
	Authorize(context.Context, Actor, string, string) error
}

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

type HandlerDependencies struct {
	FullWorkflow *FullWorkflowDependencies
	// Service is retained for compatibility with older adapters. New route
	// composition should provide the focused application ports below.
	Service          ApplicationService
	Read             WorkspaceReadApplication
	Write            WorkspaceWriteApplication
	Projects         ProjectApplication
	Members          MembershipApplication
	Chapters         ChapterApplication
	TemplateApp      TemplateApplication
	ChangeSets       ChangeSetApplication
	Sources          SourceApplicationService
	Integration      WorkspaceIntegration
	SelectedRewrites SelectedRewriteApplication
	Templates        delivery.TemplateReader
}

// WorkspaceIntegration supplies the same source recheck and frozen-input adapter to all consumers.
type WorkspaceIntegration interface {
	CandidateAdoptionSourcePolicy() candidateadoption.SourcePolicy
	WorkspaceSourcePolicy() SourcePolicy
	DeliveryInputBuilder() DeliveryInputAssembler
}
