// Package templategovernance implements tenant-scoped template publication and
// human-reviewed rule suggestions. RuleSet storage and evaluation remain owned
// by rulesetstore and template; this package never executes a second rule engine.
package templategovernance

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"gorm.io/gorm"
)

type Actor = rulesetstore.Actor
type Action string

const (
	ActionRead             Action = "read"
	ActionCreateProfile    Action = "create_profile"
	ActionSaveDraft        Action = "save_draft"
	ActionPublish          Action = "publish"
	ActionWithdraw         Action = "withdraw"
	ActionRecall           Action = "recall"
	ActionSuggest          Action = "suggest"
	ActionAcceptSuggestion Action = "accept_suggestion"
	ActionRejectSuggestion Action = "reject_suggestion"
)

var (
	ErrUnavailable         = errors.New("template_governance_unavailable")
	ErrUnauthorized        = errors.New("template_governance_authorization_unavailable")
	ErrNotFound            = errors.New("template_governance_not_found")
	ErrInvalidRequest      = errors.New("template_governance_invalid_request")
	ErrVersionConflict     = errors.New("template_governance_version_conflict")
	ErrIdempotencyConflict = errors.New("template_governance_idempotency_conflict")
	ErrInvalidState        = errors.New("template_governance_invalid_state")
	ErrReplayRequired      = errors.New("template_replay_required")
	ErrSourceUnavailable   = errors.New("template_source_unavailable")
	ErrModelUnavailable    = errors.New("rule_suggestion_model_unavailable")
	ErrStale               = errors.New("rule_suggestion_stale")
	ErrInvalidOutput       = errors.New("rule_suggestion_invalid_output")
)

type Profile struct {
	TenantID  uint64    `json:"-"`
	ID        string    `json:"template_id"`
	Name      string    `json:"name"`
	Revision  int64     `json:"revision"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}
type RuleSetRef struct {
	ID      string `json:"ruleset_id"`
	Version string `json:"version"`
	Hash    string `json:"ruleset_hash,omitempty"`
}
type SourceRef struct {
	ID      string `json:"source_id"`
	Version string `json:"source_version"`
	Locator string `json:"locator,omitempty"`
}
type RecallRecord struct {
	Reason             string    `json:"reason"`
	MigrationAdvice    string    `json:"migration_advice"`
	AffectedProjectIDs []string  `json:"affected_project_ids"`
	ActorID            string    `json:"actor_id"`
	RecordedAt         time.Time `json:"recorded_at"`
}
type TemplateVersion struct {
	BindingPolicies []BindingPolicy       `json:"binding_policies"`
	PolicyHash      string                `json:"policy_hash"`
	TemplateID      string                `json:"template_id"`
	Version         string                `json:"version"`
	Revision        int64                 `json:"revision"`
	State           template.RuleSetState `json:"state"`
	Definition      template.Template     `json:"definition"`
	RuleSet         RuleSetRef            `json:"ruleset"`
	RuleSetSnapshot template.RuleSet      `json:"ruleset_snapshot"`
	SourceType      string                `json:"source_type"`
	Scope           string                `json:"scope"`
	Sources         []SourceRef           `json:"source_refs"`
	Curator         string                `json:"curator"`
	ContentHash     string                `json:"content_hash"`
	RulesetHash     string                `json:"ruleset_hash"`
	PublishedAt     *time.Time            `json:"published_at,omitempty"`
	Recall          *RecallRecord         `json:"recall,omitempty"`
	CreatedBy       string                `json:"created_by"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
}
type CatalogFilter struct {
	Scope string
	State template.RuleSetState
}
type CreateProfileInput struct {
	ID     string `json:"template_id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}
type SaveDraftInput struct {
	BindingPolicies  []BindingPolicy   `json:"binding_policies"`
	TemplateID       string            `json:"template_id"`
	Version          string            `json:"version"`
	ExpectedRevision int64             `json:"expected_revision"`
	Definition       template.Template `json:"definition"`
	RuleSet          RuleSetRef        `json:"ruleset"`
	SourceType       string            `json:"source_type"`
	Scope            string            `json:"scope"`
	Sources          []SourceRef       `json:"source_refs"`
	Curator          string            `json:"curator"`
	Reason           string            `json:"reason"`
}
type PublishInput struct {
	TemplateID       string   `json:"template_id"`
	Version          string   `json:"version"`
	ExpectedRevision int64    `json:"expected_revision"`
	ReplayFixtureIDs []string `json:"replay_fixture_ids"`
	Reason           string   `json:"reason"`
}
type GovernanceInput struct {
	TemplateID       string `json:"template_id"`
	Version          string `json:"version"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type RecallInput struct {
	GovernanceInput
	MigrationAdvice string `json:"migration_advice"`
}

// VersionBasis is populated from current project state by the application
// adapter. Request values are expectations; they are never authorization proof.
type VersionBasis struct {
	ProjectVersion      int64      `json:"project_version"`
	SpecRevision        int64      `json:"spec_revision"`
	ContextRevision     int64      `json:"context_revision"`
	TemplateCopyVersion int64      `json:"template_copy_version"`
	TemplateID          string     `json:"template_id"`
	TemplateVersion     string     `json:"template_version"`
	RuleSet             RuleSetRef `json:"ruleset"`
	CurrentSnapshotID   string     `json:"current_snapshot_id,omitempty"`
}
type SuggestionScope struct {
	ProjectID       string       `json:"project_id"`
	Basis           VersionBasis `json:"basis"`
	TemplateID      string       `json:"template_id"`
	TemplateVersion string       `json:"template_version"`
	CatalogRevision int64        `json:"catalog_revision"`
	RuleSet         RuleSetRef   `json:"ruleset"`
	TargetKind      string       `json:"target_kind"`
	TargetID        string       `json:"target_id"`
	Sources         []SourceRef  `json:"source_refs"`
}
type Authorizer interface {
	AuthorizeTemplate(context.Context, *gorm.DB, Actor, Action, string) error
	AuthorizeSuggestion(context.Context, *gorm.DB, Actor, Action, SuggestionScope) error
	CurrentProjectBasis(context.Context, *gorm.DB, Actor, string) (VersionBasis, error)
}
type SourceVerifier interface {
	VerifyTemplateSources(context.Context, Actor, []SourceRef) error
}

// ReplayVerifier must execute actual examples with the same allowlisted
// evaluator and fail unless every selected positive/negative fixture passes.
type ReplayVerifier interface {
	Verify(context.Context, Actor, TemplateVersion, []string) error
}
type TemplateUsageReader interface {
	AffectedProjects(context.Context, *gorm.DB, Actor, string, string) ([]string, error)
}
type AuthorizedSource struct {
	SourceRef
	AssetID         string `json:"asset_id,omitempty"`
	QuotedTextHash  string `json:"quoted_text_hash"`
	QuotedText      string `json:"quoted_text"`
	ModelUseAllowed bool   `json:"model_use_allowed"`
}
type AuthorizedContext struct {
	ProjectID          string             `json:"project_id"`
	Basis              VersionBasis       `json:"basis"`
	Sources            []AuthorizedSource `json:"sources"`
	AuthorizationState string             `json:"authorization_state"`
}

// Build is an adapter to the shared G2 authorized context builder. It must
// retrieve sources under current grants, never echo request-declared grants.
type SuggestionContextProvider interface {
	Build(context.Context, Actor, SuggestionScope) (AuthorizedContext, error)
}
type ProposedRule struct {
	RuleID            string         `json:"rule_id,omitempty"`
	DefinitionID      string         `json:"definition_id"`
	DefinitionVersion string         `json:"definition_version"`
	TargetKind        string         `json:"target_kind"`
	TargetID          string         `json:"target_id"`
	Parameters        map[string]any `json:"parameters,omitempty"`
	Message           string         `json:"message"`
	Rationale         string         `json:"rationale"`
	EvidenceSourceIDs []string       `json:"evidence_source_ids"`
}
type SuggestionPayload struct {
	Proposals   []ProposedRule `json:"proposals"`
	Explanation string         `json:"explanation"`
}
type RuleSuggestionRequest struct {
	Scope         SuggestionScope
	Template      template.Template
	RuleSet       template.RuleSet
	Context       AuthorizedContext
	PromptVersion string
	CorrelationID string
}
type RuleSuggestionOutput struct {
	StructuredOutput json.RawMessage
	ModelID          string
	RunMode          string
	InputTokens      int
	OutputTokens     int
	ToolNames        []string
}
type RuleSuggestionModel interface {
	Suggest(context.Context, Actor, RuleSuggestionRequest) (RuleSuggestionOutput, error)
}
type SuggestionRun struct {
	ID             string             `json:"id"`
	ProjectID      string             `json:"project_id"`
	ActorID        string             `json:"actor_id"`
	Revision       int64              `json:"revision"`
	Status         string             `json:"status"`
	Scope          SuggestionScope    `json:"scope"`
	Output         *SuggestionPayload `json:"output,omitempty"`
	ModelID        string             `json:"model_id,omitempty"`
	RunMode        string             `json:"run_mode,omitempty"`
	PromptVersion  string             `json:"prompt_version"`
	ToolNames      []string           `json:"tool_names"`
	InputTokens    int                `json:"input_tokens"`
	OutputTokens   int                `json:"output_tokens"`
	DurationMS     int64              `json:"duration_ms"`
	ErrorCode      string             `json:"error_code,omitempty"`
	ErrorMessage   string             `json:"error_message,omitempty"`
	DecisionReason string             `json:"decision_reason,omitempty"`
	DraftRuleSet   *RuleSetRef        `json:"draft_ruleset,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}
type AcceptSuggestionInput struct {
	ExpectedRevision int64                  `json:"expected_revision"`
	RuleSetID        string                 `json:"ruleset_id"`
	Version          string                 `json:"version"`
	Bindings         []template.RuleBinding `json:"bindings"`
	Reason           string                 `json:"reason"`
}
type RejectSuggestionInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type Ports struct {
	Authorizer Authorizer
	RuleSets   rulesetstore.Authorizer
	Sources    SourceVerifier
	Replay     ReplayVerifier
	Usage      TemplateUsageReader
	Context    SuggestionContextProvider
	Model      RuleSuggestionModel
}
type Service struct {
	db    *gorm.DB
	ports Ports
}

func NewService(db *gorm.DB, ports Ports) *Service { return &Service{db: db, ports: ports} }
