package workspacecore

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// These views match the proposed HTTP contract. IDs are opaque to callers.
type Member struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
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
	DiscardedAt            *time.Time           `json:"discarded_at,omitempty"`
	Members                []Member             `json:"members"`
}

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
	ID        string         `json:"id"`
	ProjectID string         `json:"project_id"`
	ActorID   string         `json:"actor_id"`
	Action    string         `json:"action"`
	Target    string         `json:"target"`
	Details   map[string]any `json:"details,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
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

type TemplateMigrationField struct {
	FieldID string `json:"field_id"`
	Value   string `json:"value,omitempty"`
	Status  string `json:"status"`
}

type TemplateMigrationPreview struct {
	ProjectID              string                   `json:"project_id"`
	SourceTemplateID       string                   `json:"source_template_id"`
	SourceTemplateVersion  string                   `json:"source_template_version"`
	TargetTemplateID       string                   `json:"target_template_id"`
	TargetTemplateVersion  string                   `json:"target_template_version"`
	ExpectedProjectVersion int64                    `json:"expected_project_version"`
	Fields                 []TemplateMigrationField `json:"fields"`
	MissingRequired        []string                 `json:"missing_required"`
	Orphaned               []string                 `json:"orphaned"`
	Incompatible           []string                 `json:"incompatible"`
}

type ReviewItem struct {
	ID                string `json:"id"`
	Statement         string `json:"statement"`
	OriginCandidateID string `json:"origin_candidate_id"`
}

type Chapter struct {
	ID                string       `json:"id"`
	ProjectID         string       `json:"project_id"`
	SectionID         string       `json:"section_id"`
	Title             string       `json:"title"`
	CurrentVersionID  *string      `json:"current_version_id"`
	BodyMarkdown      string       `json:"body_markdown"`
	SourceIDs         []string     `json:"source_ids"`
	ReviewItems       []ReviewItem `json:"review_items"`
	ConfirmationValid bool         `json:"confirmation_valid"`
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
}

type Section struct {
	ID       string
	Title    string
	Required bool
}

type Template struct {
	ID             string
	Version        string
	RulesetHash    string
	Sections       []Section
	RequiredFields []string
	Fields         []TemplateField
}

type TemplateField struct {
	ID       string
	Type     string
	Required bool
}

type TemplateReader interface {
	Get(id, version string) (Template, error)
}

// ContractDemoTemplate is a replaceable T02 stand-in. It is not an official
// grant application template and must not be used to claim formal readiness.
type ContractDemoTemplate struct{}

func (ContractDemoTemplate) Get(id, version string) (Template, error) {
	if id != "template-demo" || (version != "" && version != "1") {
		return Template{}, ErrInvalidState
	}
	return Template{
		ID: "template-demo", Version: "1",
		RulesetHash: "ad814c1ffba1956c0654abd1fc7fc48fad526109f43406633f05861116ec15a1",
		Sections: []Section{
			{ID: "question", Title: "研究问题", Required: true},
			{ID: "method", Title: "研究方案", Required: true},
		},
		RequiredFields: []string{"research_subject", "research_goal"},
		Fields:         []TemplateField{{ID: "research_subject", Type: "string", Required: true}, {ID: "research_goal", Type: "string", Required: true}},
	}, nil
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
	DiscardedAt            *time.Time `gorm:"column:discarded_at"`
	CreatedAt              time.Time
}

func (projectRow) TableName() string { return "lingdoc_projects" }

type memberRow struct {
	ProjectID string `gorm:"primaryKey;size:36"`
	UserID    string `gorm:"primaryKey;size:64"`
	Role      string `gorm:"not null;size:20"`
}

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
	ID                string  `gorm:"primaryKey;size:36"`
	ProjectID         string  `gorm:"not null;index;size:36"`
	ChapterID         string  `gorm:"not null;index;size:36"`
	ParentVersionID   *string `gorm:"size:36"`
	BodyMarkdown      string  `gorm:"not null;type:text"`
	SourceIDsJSON     string  `gorm:"column:source_ids_json;not null;type:text"`
	ReviewItemsJSON   string  `gorm:"column:review_items_json;not null;type:text"`
	SpecRevision      int64   `gorm:"not null;default:0"`
	ConfirmationValid bool    `gorm:"not null;default:false"`
	CreatedAt         time.Time
}

func (chapterVersionRow) TableName() string { return "lingdoc_chapter_versions" }

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
