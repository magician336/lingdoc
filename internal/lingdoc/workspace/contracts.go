package workspace

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// ApplicationService is the HTTP-facing workspace contract. Tests and
// alternative transports can provide an implementation without a database.
type ApplicationService interface {
	Authorize(context.Context, Actor, string, string) error
	GetProject(context.Context, Actor, string) (Project, error)
	ListProjects(context.Context, Actor) ([]Project, bool, error)
	CreateProject(context.Context, Actor, string, CreateProjectInput) (json.RawMessage, int, bool, error)
	SaveSpec(context.Context, Actor, string, string, SaveSpecInput) (json.RawMessage, int, bool, error)
	SaveMembers(context.Context, Actor, string, string, SaveMembersInput) (json.RawMessage, int, bool, error)
	TransferOwner(context.Context, Actor, string, string, TransferOwnerInput) (json.RawMessage, int, bool, error)
	ActivateProject(context.Context, Actor, string, string, ActivateProjectInput) (json.RawMessage, int, bool, error)
	CreateDraftCandidate(context.Context, Actor, string, string, DraftCandidateInput) (json.RawMessage, int, bool, error)
	ListDraftCandidates(context.Context, Actor, string) ([]DraftCandidate, error)
	ListAuditEvents(context.Context, Actor, string) ([]AuditEvent, error)
	ActivationDiff(context.Context, Actor, string, int64) (ActivationDiff, error)
	RequestOwnerTransfer(context.Context, Actor, string, string, OwnerTransferInput) (json.RawMessage, int, bool, error)
	AcceptOwnerTransfer(context.Context, Actor, string, string, string) (json.RawMessage, int, bool, error)
	PreviewTemplateMigration(context.Context, Actor, string, TemplateMigrationInput) (TemplateMigrationPreview, error)
	ChangeTemplate(context.Context, Actor, string, string, TemplateMigrationInput) (json.RawMessage, int, bool, error)
	GetTemplateCopy(context.Context, Actor, string, int64) (ProjectTemplateCopy, error)
	PreviewTemplateCopyEdit(context.Context, Actor, string, TemplateCopyDefinitionInput) (TemplateMigrationPreview, error)
	SaveTemplateCopy(context.Context, Actor, string, string, TemplateCopyDefinitionInput) (json.RawMessage, int, bool, error)
	DiscardProject(context.Context, Actor, string, string, int64) (json.RawMessage, int, bool, error)
	RestoreProject(context.Context, Actor, string, string, int64) (json.RawMessage, int, bool, error)
	ListChapters(context.Context, Actor, string) ([]Chapter, error)
	SaveChapter(context.Context, Actor, string, string, string, SaveChapterInput) (json.RawMessage, int, bool, error)
	GetWorkingCopy(context.Context, Actor, string, string) (WorkingCopy, error)
	SaveWorkingCopy(context.Context, Actor, string, string, string, SaveWorkingCopyInput) (json.RawMessage, int, bool, error)
	ApplyRewrite(context.Context, Actor, string, string, string, core.SaveWorkingCopyInput, []core.ReviewItem) (json.RawMessage, int, bool, error)
	CommitWorkingCopy(context.Context, Actor, string, string, string, CommitWorkingCopyInput) (json.RawMessage, int, bool, error)
	ListChapterVersions(context.Context, Actor, string, string) ([]ChapterVersion, error)
	RestoreWorkingCopy(context.Context, Actor, string, string, string, RestoreWorkingCopyInput) (json.RawMessage, int, bool, error)
	GenerationContext(context.Context, Actor, string, string) (GenerationContext, error)
	CreateChangeSet(context.Context, Actor, string, string, CreateChangeSetInput) (json.RawMessage, int, bool, error)
	ListChangeSets(context.Context, Actor, string) ([]ChangeSet, error)
	GetChangeSet(context.Context, Actor, string, string) (ChangeSet, error)
	ApplyChangeSet(context.Context, Actor, string, string, string) (json.RawMessage, int, bool, error)
	RejectChangeSet(context.Context, Actor, string, string, string) (json.RawMessage, int, bool, error)
}

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
	Service          ApplicationService
	Sources          SourceApplicationService
	Integration      WorkspaceIntegration
	SelectedRewrites SelectedRewriteApplication
}

// WorkspaceIntegration supplies the same source recheck and frozen-input adapter to all consumers.
type WorkspaceIntegration interface {
	CandidateAdoptionSourcePolicy() candidateadoption.SourcePolicy
	WorkspaceSourcePolicy() SourcePolicy
	DeliveryInputBuilder() DeliveryInputAssembler
}
