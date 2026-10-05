package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
)

// These views match the proposed HTTP contract. IDs are opaque to callers.
type Member struct {
	UserID         string              `json:"user_id"`
	Role           string              `json:"role"`
	GovernanceRole string              `json:"governance_role,omitempty"`
	FunctionRoles  []string            `json:"function_roles,omitempty"`
	FunctionScopes map[string][]string `json:"function_scopes,omitempty"`
	Status         string              `json:"status,omitempty"`
}

type Project struct {
	ID                     string               `json:"id"`
	Name                   string               `json:"name"`
	Status                 string               `json:"status"`
	ProjectVersion         int64                `json:"project_version"`
	SpecRevision           int64                `json:"spec_revision"`
	CurrentContextRevision int64                `json:"current_context_revision"`
	DeliveryStatus         string               `json:"delivery_status"`
	BaselineConfirmationID string               `json:"baseline_confirmation_id,omitempty"`
	Spec                   map[string]string    `json:"spec"`
	SpecFields             map[string]SpecField `json:"spec_fields,omitempty"`
	TemplateID             string               `json:"template_id"`
	TemplateVersion        string               `json:"template_version"`
	TemplateCopyVersion    int64                `json:"template_copy_version"`
	TemplateCopy           *ProjectTemplateCopy `json:"template_copy,omitempty"`
	DiscardedAt            *time.Time           `json:"discarded_at,omitempty"`
	Members                []Member             `json:"members"`
}

// ProjectTemplateCopy is an append-only project-owned snapshot of one exact
// published template version. Lifecycle status may advance; Definition and
// its server-computed hashes never change after the row is inserted.
type ProjectTemplateCopy struct {
	ID                    string                   `json:"id"`
	ProjectID             string                   `json:"project_id"`
	SourceTemplateID      string                   `json:"source_template_id"`
	SourceTemplateVersion string                   `json:"source_template_version"`
	Version               int64                    `json:"version"`
	Status                string                   `json:"status"`
	ContentHash           string                   `json:"content_hash"`
	RulesetHash           string                   `json:"ruleset_hash"`
	Definition            lingdoctemplate.Template `json:"definition"`
	CreatedBy             string                   `json:"created_by"`
	CreatedAt             time.Time                `json:"created_at"`
}

const (
	TemplateCopyDraft      = "draft"
	TemplateCopyBound      = "bound"
	TemplateCopySuperseded = "superseded"
	TemplateCopyDiscarded  = "discarded"
)

type ProvenanceRecord struct {
	SourceType         string    `json:"source_type"`
	SourceID           string    `json:"source_id,omitempty"`
	SourceVersion      string    `json:"source_version,omitempty"`
	Locator            string    `json:"locator,omitempty"`
	Purpose            string    `json:"purpose,omitempty"`
	AuthorizationScope string    `json:"authorization_scope,omitempty"`
	CreatedBy          string    `json:"created_by"`
	CreatedAt          time.Time `json:"created_at"`
	Accessible         bool      `json:"accessible"`
	NeedsReview        bool      `json:"needs_review"`
}

type SpecField struct {
	Value                  string            `json:"value"`
	Origin                 string            `json:"origin"`
	Status                 string            `json:"status"`
	Provenance             *ProvenanceRecord `json:"provenance,omitempty"`
	ModifiedBy             string            `json:"modified_by"`
	ModifiedAt             time.Time         `json:"modified_at"`
	ModifiedProjectVersion int64             `json:"modified_project_version,omitempty"`
}

type DraftCandidate struct {
	ID                     string            `json:"id"`
	ProjectID              string            `json:"project_id"`
	Kind                   string            `json:"kind"`
	Title                  string            `json:"title"`
	Content                string            `json:"content"`
	Level                  string            `json:"level"`
	BasedOnContextRevision int64             `json:"based_on_context_revision"`
	Provenance             *ProvenanceRecord `json:"provenance,omitempty"`
	CreatedBy              string            `json:"created_by"`
	CreatedAt              time.Time         `json:"created_at"`
}

type AuditEvent struct {
	ID         string           `json:"id,omitempty"`
	ProjectID  string           `json:"project_id,omitempty"`
	ActorID    string           `json:"actor_id,omitempty"`
	Action     string           `json:"action,omitempty"`
	Target     string           `json:"target,omitempty"`
	Details    map[string]any   `json:"details,omitempty"`
	CreatedAt  time.Time        `json:"created_at,omitempty"`
	TenantID   uint64           `json:"tenant_id,omitempty"`
	UserID     string           `json:"user_id,omitempty"`
	Role       types.TenantRole `json:"role,omitempty"`
	Capability string           `json:"capability,omitempty"`
	Decision   string           `json:"decision,omitempty"`
	Reason     string           `json:"reason,omitempty"`
}

type OwnerTransfer struct {
	ID                     string     `json:"id"`
	ProjectID              string     `json:"project_id"`
	FromUserID             string     `json:"from_user_id"`
	ToUserID               string     `json:"to_user_id"`
	Status                 string     `json:"status"`
	ExpectedProjectVersion int64      `json:"expected_project_version"`
	CreatedAt              time.Time  `json:"created_at"`
	AcceptedAt             *time.Time `json:"accepted_at,omitempty"`
}

type SpecFieldChange struct {
	Key                    string    `json:"key"`
	Value                  string    `json:"value"`
	Origin                 string    `json:"origin"`
	Status                 string    `json:"status"`
	ModifiedBy             string    `json:"modified_by"`
	ModifiedAt             time.Time `json:"modified_at"`
	ModifiedProjectVersion int64     `json:"modified_project_version,omitempty"`
}

type ActivationDiff struct {
	ProjectID             string            `json:"project_id"`
	CurrentProjectVersion int64             `json:"current_project_version"`
	CurrentSpecRevision   int64             `json:"current_spec_revision"`
	ChangedSinceVersion   int64             `json:"changed_since_version"`
	ChangedFields         []SpecFieldChange `json:"changed_fields"`
	PendingAIFields       []SpecFieldChange `json:"pending_ai_fields"`
}

type ChangeFieldInput struct {
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
}

type ChangeFieldDelta struct {
	Key      string `json:"key"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
}

type ChangeImpact struct {
	ChapterID        string  `json:"chapter_id"`
	ChapterVersionID *string `json:"chapter_version_id,omitempty"`
	Title            string  `json:"title"`
	Reason           string  `json:"reason"`
	Status           string  `json:"status"`
}

type ChangeSet struct {
	ID                    string             `json:"id"`
	ProjectID             string             `json:"project_id"`
	CreatedBy             string             `json:"created_by"`
	Reason                string             `json:"reason"`
	Status                string             `json:"status"`
	BaseContextRevision   int64              `json:"base_context_revision"`
	TargetContextRevision *int64             `json:"target_context_revision,omitempty"`
	BaseSpecRevision      int64              `json:"base_spec_revision"`
	TargetSpecRevision    *int64             `json:"target_spec_revision,omitempty"`
	Fields                []ChangeFieldDelta `json:"fields"`
	Impacts               []ChangeImpact     `json:"impacts"`
	CreatedAt             time.Time          `json:"created_at"`
	AppliedAt             *time.Time         `json:"applied_at,omitempty"`
}

type CreateChangeSetInput struct {
	ExpectedContextRevision int64                       `json:"expected_context_revision"`
	Fields                  map[string]ChangeFieldInput `json:"fields"`
	AffectedChapterIDs      []string                    `json:"affected_chapter_ids"`
	Reason                  string                      `json:"reason"`
}

type TemplateMigrationField struct {
	FieldID string `json:"field_id"`
	Value   string `json:"value,omitempty"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
	OldType string `json:"old_type,omitempty"`
	NewType string `json:"new_type,omitempty"`
	Status  string `json:"status"`
}

type TemplateMigrationPreview struct {
	ProjectID              string                   `json:"project_id"`
	SourceTemplateID       string                   `json:"source_template_id"`
	SourceTemplateVersion  string                   `json:"source_template_version"`
	SourceCopyVersion      int64                    `json:"source_copy_version,omitempty"`
	TargetTemplateID       string                   `json:"target_template_id"`
	TargetTemplateVersion  string                   `json:"target_template_version"`
	TargetCopyVersion      int64                    `json:"target_copy_version,omitempty"`
	TargetContentHash      string                   `json:"target_content_hash,omitempty"`
	TargetRulesetHash      string                   `json:"target_ruleset_hash,omitempty"`
	RulesetChanged         bool                     `json:"ruleset_changed,omitempty"`
	ExpectedProjectVersion int64                    `json:"expected_project_version"`
	Fields                 []TemplateMigrationField `json:"fields"`
	SectionChanges         []TemplateSectionChange  `json:"section_changes,omitempty"`
	MissingRequired        []string                 `json:"missing_required"`
	Orphaned               []string                 `json:"orphaned"`
	Incompatible           []string                 `json:"incompatible"`
}

type ReviewItem struct {
	ID                string `json:"id"`
	Statement         string `json:"statement"`
	OriginCandidateID string `json:"origin_candidate_id"`
}

// CitationUsage records the human context for one source used by a chapter.
// It is versioned with the chapter and does not assert that the source proves
// a research claim.
type CitationUsage struct {
	SourceID   string `json:"source_id"`
	Purpose    string `json:"purpose,omitempty"`
	Limitation string `json:"limitation,omitempty"`
}

type Chapter struct {
	ID                string           `json:"id"`
	ProjectID         string           `json:"project_id"`
	SectionID         string           `json:"section_id"`
	Title             string           `json:"title"`
	CurrentVersionID  *string          `json:"current_version_id"`
	BodyMarkdown      string           `json:"body_markdown"`
	SourceIDs         []string         `json:"source_ids"`
	CitationUsages    []CitationUsage  `json:"citation_usages"`
	CitationStatuses  []CitationStatus `json:"citation_statuses,omitempty"`
	ReviewItems       []ReviewItem     `json:"review_items"`
	ConfirmationValid bool             `json:"confirmation_valid"`
}

// WorkingCopy is the mutable, restart-safe draft for a chapter. It is kept
// separate from ChapterVersion so autosave and candidate adoption never
// create a formal version.
type WorkingCopy struct {
	CitationUsages       []CitationUsage `json:"citation_usages"`
	ProjectID            string          `json:"project_id"`
	ChapterID            string          `json:"chapter_id"`
	BaseChapterVersionID *string         `json:"base_chapter_version_id"`
	SpecRevision         int64           `json:"spec_revision"`
	WorkingCopyRevision  int64           `json:"working_copy_revision"`
	BodyMarkdown         string          `json:"body_markdown"`
	SourceIDs            []string        `json:"source_ids"`
	ReviewItems          []ReviewItem    `json:"review_items"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

// ChapterVersion is an immutable historical chapter snapshot.
type ChapterVersion struct {
	CitationUsages    []CitationUsage `json:"citation_usages"`
	ID                string          `json:"id"`
	ProjectID         string          `json:"project_id"`
	ChapterID         string          `json:"chapter_id"`
	ParentVersionID   *string         `json:"parent_version_id"`
	BodyMarkdown      string          `json:"body_markdown"`
	SourceIDs         []string        `json:"source_ids"`
	ReviewItems       []ReviewItem    `json:"review_items"`
	SpecRevision      int64           `json:"spec_revision"`
	ConfirmationValid bool            `json:"confirmation_valid"`
	CreatedAt         time.Time       `json:"created_at"`
}

type GenerationContext struct {
	ProjectID        string            `json:"project_id"`
	ProjectVersion   int64             `json:"project_version"`
	SpecRevision     int64             `json:"spec_revision"`
	Spec             map[string]string `json:"spec"`
	TemplateID       string            `json:"template_id"`
	TemplateVersion  string            `json:"template_version"`
	ChapterID        string            `json:"chapter_id"`
	ChapterVersionID *string           `json:"chapter_version_id"`
	ChapterBody      string            `json:"chapter_body"`
	Chapter          Chapter           `json:"chapter"`
}

type Actor struct {
	TenantID    uint64
	UserID      string
	TenantRole  string
	SystemAdmin bool
	Role        types.TenantRole
}

type AuthorizationMode string

const (
	AuthorizationModeLog      AuthorizationMode = "log"
	AuthorizationModeEnforce  AuthorizationMode = "enforce"
	AuthorizationModeRollback AuthorizationMode = "rollback"
)

// AuditSink is the application seam for authorization decisions. Project
// audit rows and authorization audit records intentionally share the domain
// event shape so callers can use one append-only sink.
type AuditSink interface {
	Record(context.Context, AuditEvent) error
}

type Section = lingdoctemplate.Section
type Template = lingdoctemplate.Template
type TemplateField = lingdoctemplate.Field
type TemplateReader = lingdoctemplate.Reader

// ContractDemoTemplate is a replaceable T02 stand-in. It is not an official
// grant application template and must not be used to claim formal readiness.
type ContractDemoTemplate struct{}

func (ContractDemoTemplate) Get(id, version string) (Template, error) {
	if version == "" {
		version = lingdoctemplate.DemoTemplateVersion
	}
	template, err := (lingdoctemplate.FixedDemoReader{}).Get(id, version)
	if err != nil {
		return Template{}, ErrInvalidState
	}
	return template, nil
}

var (
	ErrNotFound            = errors.New("not_found")
	ErrInvalidRequest      = errors.New("invalid_request")
	ErrInvalidState        = errors.New("invalid_state")
	ErrVersionConflict     = errors.New("version_conflict")
	ErrIdempotencyConflict = errors.New("idempotency_conflict")
	ErrRequestInProgress   = errors.New("request_in_progress")
	ErrSourceUnavailable   = errors.New("source_access_denied")
)

type projectRow struct {
	ID                     string     `gorm:"primaryKey;size:36"`
	TenantID               uint64     `gorm:"not null;index"`
	Name                   string     `gorm:"not null;size:120"`
	Status                 string     `gorm:"not null;size:16"`
	ProjectVersion         int64      `gorm:"not null"`
	SpecRevision           int64      `gorm:"not null"`
	CurrentContextRevision int64      `gorm:"column:current_context_revision;not null;default:0"`
	DeliveryStatus         string     `gorm:"column:delivery_status;not null;default:NOT_READY"`
	BaselineConfirmationID string     `gorm:"column:baseline_confirmation_id;size:36"`
	SpecJSON               string     `gorm:"column:spec_json;not null;type:text"`
	SpecMetadataJSON       string     `gorm:"column:spec_metadata_json;not null;type:text;default:'{}'"`
	TemplateID             string     `gorm:"not null;size:80"`
	TemplateVersion        string     `gorm:"not null;size:40"`
	TemplateCopyVersion    int64      `gorm:"column:template_copy_version;not null;default:0"`
	DiscardedAt            *time.Time `gorm:"column:discarded_at"`
	CreatedAt              time.Time
}

func (projectRow) TableName() string { return "lingdoc_projects" }

type projectTemplateCopyRow struct {
	ID                    string `gorm:"primaryKey;size:36"`
	ProjectID             string `gorm:"not null;size:36;uniqueIndex:idx_lingdoc_template_copy_version,priority:1"`
	Version               int64  `gorm:"not null;uniqueIndex:idx_lingdoc_template_copy_version,priority:2"`
	SourceTemplateID      string `gorm:"not null;size:80"`
	SourceTemplateVersion string `gorm:"not null;size:40"`
	Status                string `gorm:"not null;size:16"`
	ContentHash           string `gorm:"not null;size:64"`
	RulesetHash           string `gorm:"not null;size:64"`
	DefinitionJSON        string `gorm:"column:definition_json;not null;type:text"`
	CreatedBy             string `gorm:"not null;size:64"`
	CreatedAt             time.Time
}

func (projectTemplateCopyRow) TableName() string { return "lingdoc_project_template_copies" }

type memberRow struct {
	ProjectID string `gorm:"primaryKey;size:36"`
	UserID    string `gorm:"primaryKey;size:64"`
	Role      string `gorm:"not null;size:20"`
}

// memberPermissionRow is the expand-contract sidecar for the legacy role.
type memberPermissionRow struct {
	ProjectID          string `gorm:"primaryKey;size:36"`
	UserID             string `gorm:"primaryKey;size:64"`
	GovernanceRole     string `gorm:"not null;size:16"`
	FunctionRolesJSON  string `gorm:"column:function_roles_json;not null;type:text"`
	FunctionScopesJSON string `gorm:"column:function_scopes_json;not null;type:text"`
	Status             string `gorm:"not null;size:24"`
}

func (memberPermissionRow) TableName() string { return "lingdoc_member_permissions" }

func (memberRow) TableName() string { return "lingdoc_members" }

type draftCandidateRow struct {
	ID                     string `gorm:"primaryKey;size:36"`
	ProjectID              string `gorm:"not null;index;size:36"`
	Kind                   string `gorm:"not null;size:32"`
	Title                  string `gorm:"not null;size:255"`
	Content                string `gorm:"not null;type:text"`
	Level                  string `gorm:"not null;size:32"`
	BasedOnContextRevision int64  `gorm:"not null;default:0"`
	ProvenanceJSON         string `gorm:"column:provenance_json;not null;type:text;default:'{}'"`
	CreatedBy              string `gorm:"not null;size:64"`
	CreatedAt              time.Time
}

func (draftCandidateRow) TableName() string { return "lingdoc_draft_candidates" }

type auditEventRow struct {
	ID          string `gorm:"primaryKey;size:36"`
	ProjectID   string `gorm:"not null;index;size:36"`
	ActorID     string `gorm:"not null;size:64"`
	Action      string `gorm:"not null;size:64"`
	Target      string `gorm:"not null;size:128"`
	DetailsJSON string `gorm:"column:details_json;not null;type:text;default:'{}'"`
	CreatedAt   time.Time
}

func (auditEventRow) TableName() string { return "lingdoc_project_audits" }

type ownerTransferRow struct {
	ID                     string `gorm:"primaryKey;size:36"`
	ProjectID              string `gorm:"not null;index;size:36"`
	FromUserID             string `gorm:"not null;size:64"`
	ToUserID               string `gorm:"not null;size:64"`
	Status                 string `gorm:"not null;size:16"`
	ExpectedProjectVersion int64  `gorm:"not null"`
	CreatedAt              time.Time
	AcceptedAt             *time.Time
}

func (ownerTransferRow) TableName() string { return "lingdoc_owner_transfers" }

type chapterRow struct {
	ID               string  `gorm:"primaryKey;size:36"`
	ProjectID        string  `gorm:"not null;index;size:36"`
	SectionID        string  `gorm:"not null;size:80"`
	Title            string  `gorm:"not null;size:120"`
	CurrentVersionID *string `gorm:"size:36"`
}

func (chapterRow) TableName() string { return "lingdoc_chapters" }

type chapterVersionRow struct {
	ID                 string  `gorm:"primaryKey;size:36"`
	ProjectID          string  `gorm:"not null;index;size:36"`
	ChapterID          string  `gorm:"not null;index;size:36"`
	ParentVersionID    *string `gorm:"size:36"`
	BodyMarkdown       string  `gorm:"not null;type:text"`
	SourceIDsJSON      string  `gorm:"column:source_ids_json;not null;type:text"`
	CitationUsagesJSON string  `gorm:"column:citation_usages_json;not null;type:text"`
	ReviewItemsJSON    string  `gorm:"column:review_items_json;not null;type:text"`
	SpecRevision       int64   `gorm:"not null;default:0"`
	ConfirmationValid  bool    `gorm:"not null;default:false"`
	CreatedAt          time.Time
}

func (chapterVersionRow) TableName() string { return "lingdoc_chapter_versions" }

type workingCopyRow struct {
	CitationUsagesJSON   string  `gorm:"column:citation_usages_json;not null;type:text;default:'[]'"`
	ProjectID            string  `gorm:"primaryKey;size:36"`
	ChapterID            string  `gorm:"primaryKey;size:36"`
	BaseChapterVersionID *string `gorm:"size:36"`
	SpecRevision         int64   `gorm:"not null"`
	WorkingCopyRevision  int64   `gorm:"not null"`
	BodyMarkdown         string  `gorm:"not null;type:text"`
	SourceIDsJSON        string  `gorm:"column:source_ids_json;not null;type:text"`
	ReviewItemsJSON      string  `gorm:"column:review_items_json;not null;type:text"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func (workingCopyRow) TableName() string { return "lingdoc_working_copies" }

type chapterConfirmationRow struct {
	ID               string `gorm:"primaryKey;size:36"`
	ChapterID        string `gorm:"not null;size:36"`
	ChapterVersionID string `gorm:"not null;size:36"`
	Valid            bool   `gorm:"not null;default:false"`
	DetailsJSON      string `gorm:"type:text;not null;default:'{}'"`
	CreatedAt        time.Time
}

func (chapterConfirmationRow) TableName() string { return "lingdoc_chapter_confirmations" }

type chapterConfirmationDetails struct {
	ID               string `json:"id"`
	ChapterVersionID string `json:"chapter_version_id"`
	SpecRevision     int64  `json:"spec_revision"`
	TemplateVersion  string `json:"template_version"`
	Valid            bool   `json:"valid"`
}

type changeSetRow struct {
	ID                    string `gorm:"primaryKey;size:36"`
	ProjectID             string `gorm:"not null;index;size:36"`
	CreatedBy             string `gorm:"not null;size:64"`
	Reason                string `gorm:"not null;type:text"`
	Status                string `gorm:"not null;size:16"`
	BaseContextRevision   int64  `gorm:"not null"`
	TargetContextRevision *int64 `gorm:"column:target_context_revision"`
	BaseSpecRevision      int64  `gorm:"not null"`
	TargetSpecRevision    *int64 `gorm:"column:target_spec_revision"`
	FieldsJSON            string `gorm:"column:fields_json;not null;type:text"`
	ImpactsJSON           string `gorm:"column:impacts_json;not null;type:text"`
	CreatedAt             time.Time
	AppliedAt             *time.Time `gorm:"column:applied_at"`
}

func (changeSetRow) TableName() string { return "lingdoc_change_sets" }

type operationRow struct {
	TenantID     uint64 `gorm:"primaryKey"`
	UserID       string `gorm:"primaryKey;size:64"`
	Operation    string `gorm:"primaryKey;size:40"`
	Target       string `gorm:"primaryKey;size:100"`
	Key          string `gorm:"primaryKey;size:128"`
	BodyHash     string `gorm:"not null;size:64"`
	ResponseJSON string `gorm:"column:response_json;not null;type:text"`
	ResponseCode int    `gorm:"not null"`
	CreatedAt    time.Time
}

func (operationRow) TableName() string { return "lingdoc_operations" }

func decodeSpec(raw string) (map[string]string, error) {
	var spec map[string]string
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return nil, err
	}
	if spec == nil {
		spec = map[string]string{}
	}
	return spec, nil
}

func decodeSpecFields(raw string, spec map[string]string) (map[string]SpecField, error) {
	fields := make(map[string]SpecField)
	if strings.TrimSpace(raw) != "" && strings.TrimSpace(raw) != "{}" {
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return nil, err
		}
	}
	for key, value := range spec {
		field, ok := fields[key]
		if !ok {
			field = SpecField{Origin: "human", Status: "draft", ModifiedAt: time.Unix(0, 0).UTC()}
		}
		field.Value = value
		if field.Origin == "" {
			field.Origin = "human"
		}
		if field.Status == "" {
			field.Status = "draft"
		}
		fields[key] = field
	}
	return fields, nil
}
