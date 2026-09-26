package workspacecore

import (
	"encoding/json"
	"errors"
	"time"
)

// These views match the proposed HTTP contract. IDs are opaque to callers.
type Member struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type Project struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Status          string            `json:"status"`
	ProjectVersion  int64             `json:"project_version"`
	SpecRevision    int64             `json:"spec_revision"`
	Spec            map[string]string `json:"spec"`
	TemplateID      string            `json:"template_id"`
	TemplateVersion string            `json:"template_version"`
	Members         []Member          `json:"members"`
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
	TenantID uint64
	UserID   string
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
	ID              string `gorm:"primaryKey;size:36"`
	TenantID        uint64 `gorm:"not null;index"`
	Name            string `gorm:"not null;size:120"`
	Status          string `gorm:"not null;size:16"`
	ProjectVersion  int64  `gorm:"not null"`
	SpecRevision    int64  `gorm:"not null"`
	SpecJSON        string `gorm:"column:spec_json;not null;type:text"`
	TemplateID      string `gorm:"not null;size:80"`
	TemplateVersion string `gorm:"not null;size:40"`
	CreatedAt       time.Time
}

func (projectRow) TableName() string { return "lingdoc_projects" }

type memberRow struct {
	ProjectID string `gorm:"primaryKey;size:36"`
	UserID    string `gorm:"primaryKey;size:64"`
	Role      string `gorm:"not null;size:20"`
}

func (memberRow) TableName() string { return "lingdoc_members" }

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
