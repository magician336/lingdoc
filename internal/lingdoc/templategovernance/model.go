// Package templategovernance implements tenant-scoped template publication and
// human-reviewed rule suggestions. RuleSet storage and evaluation remain owned
// by rulesetstore and template; this package never executes a second rule engine.
package templategovernance

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"gorm.io/gorm"
)

// Actor identifies the tenant member a governance operation is performed as.
//
// Role is the tenant role the member held when the operation was requested.
// Its value comes from the tenant role vocabulary owned by internal/types
// (types.TenantRole), and IsValid is the only judge of membership in it.
//
// The value is not decoration: the audit rows this package writes record it
// in audit_logs.actor_role. The column therefore means "the tenant role the
// operator held at that moment" -- not a project role, not a governance
// role, not a function role. A value outside the vocabulary would put a word
// into that column that no reader can interpret, so the service refuses it
// rather than writing it.
type Actor = rulesetstore.Actor
type Action string

const (
	ActionRead             Action = "read"
	ActionCreateProfile    Action = "create_profile"
	ActionCopy             Action = "copy"
	ActionSaveDraft        Action = "save_draft"
	ActionPublish          Action = "publish"
	ActionWithdraw         Action = "withdraw"
	ActionRecall           Action = "recall"
	ActionRegisterFixture  Action = "register_fixture"
	ActionSuggest          Action = "suggest"
	ActionAcceptSuggestion Action = "accept_suggestion"
	ActionRejectSuggestion Action = "reject_suggestion"
)

// The failures of the rule governance surface state their own outward form.
//
// The wording a caller reads is part of the contract, so it is declared here
// instead of in the transport's error table.
var (
	// ErrUnverifiedActor marks a request whose claimed actor cannot be
	// attributed to the authenticated caller. Such a request must not author
	// a failure receipt in the claimed actor's name.
	ErrUnverifiedActor       = errors.New("template_governance_actor_unverified")
	ErrPermissionDenied      = errors.New("template_governance_permission_denied")
	ErrAuditHistoryForbidden = clienterror.New(clienterror.Form{Status: 403, Code: "forbidden", Message: "仅当前租户目录管理员可读取治理审计历史。", Retryable: false}, "template_governance_audit_history_forbidden")
	// ErrUnavailable is a governance service that is not configured.
	//
	// Not configured is not temporarily unavailable: the service is absent, so a
	// retry cannot make it better. DR-47-03 (D-47-05, #292) keeps every other
	// 5xx retryable but holds this one back — a caller that retries a
	// "not configured" answer only waits for a configuration that is not coming.
	ErrUnavailable = clienterror.New(clienterror.Form{
		Status:    503,
		Code:      "template_governance_unavailable",
		Message:   "目录或规则建议服务尚未配置。",
		Retryable: false,
	}, "template_governance_unavailable")
	// ErrModelUnavailable is a rule suggestion model that is not configured.
	// It shares ErrUnavailable's reason: nothing is going to change on its own.
	ErrModelUnavailable = clienterror.New(clienterror.Form{
		Status:    503,
		Code:      "template_governance_unavailable",
		Message:   "目录或规则建议服务尚未配置。",
		Retryable: false,
	}, "rule_suggestion_model_unavailable")
	// ErrUnauthorized is a governance authorization that could not be
	// established.
	ErrUnauthorized = clienterror.New(clienterror.Form{Status: 403, Code: "template_governance_authorization_unavailable", Message: "目录治理授权不可用。", Retryable: false}, "template_governance_authorization_unavailable")
	// ErrNotFound is a governed object that does not exist.
	ErrNotFound = clienterror.New(clienterror.NotFound, "template_governance_not_found")
	// ErrInvalidRequest is a governance request whose fields do not follow the
	// agreed shape.
	ErrInvalidRequest = clienterror.New(clienterror.InvalidRequest, "template_governance_invalid_request")
	// ErrVersionConflict is governed content or a baseline that changed.
	ErrVersionConflict = clienterror.New(clienterror.BaselineConflict, "template_governance_version_conflict")
	// ErrStale is a rule suggestion that was built on an outdated basis.
	ErrStale = clienterror.New(clienterror.BaselineConflict, "rule_suggestion_stale")
	// ErrIdempotencyConflict is one operation key used by two different
	// requests.
	ErrIdempotencyConflict = clienterror.New(clienterror.IdempotencyConflict, "template_governance_idempotency_conflict")
	// ErrInvalidState is a governed object whose current state does not allow
	// the requested operation.
	ErrInvalidState = clienterror.New(clienterror.RuleSetStateNotAllowed, "template_governance_invalid_state")
	// ErrReplayRequired is a rule suggestion that has to be replayed against
	// the registered fixtures first.
	ErrReplayRequired = clienterror.New(clienterror.Form{Status: 422, Code: "template_replay_required", Message: "须用服务器注册且绑定当前版本的正反例完成回放。", Retryable: false}, "template_replay_required")
	// ErrSourceUnavailable is a governed source that is gone or not
	// authorized.
	ErrSourceUnavailable = clienterror.New(clienterror.Form{Status: 403, Code: "source_access_denied", Message: "来源已不可用或无当前授权。", Retryable: false}, "template_source_unavailable")
	// ErrInvalidOutput is a model output that does not fit the current goal
	// and the rule constraints. It is the only 502 in the catalog.
	ErrInvalidOutput = clienterror.New(clienterror.InvalidModelOutput, "rule_suggestion_invalid_output")
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
type AuditHistoryFilter struct {
	AfterID uint64
	Limit   int
}
type AuditEvent struct {
	ID          uint64 `json:"id"`
	ActorUserID string `json:"actor_user_id"`
	// ActorRole is read back from audit_logs.actor_role. It means "the tenant
	// role the operator held at that moment", taken from the tenant role
	// vocabulary owned by internal/types. It is not a project role, not a
	// governance role and not a function role.
	ActorRole  string          `json:"actor_role"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	Outcome    string          `json:"outcome"`
	Details    json.RawMessage `json:"details"`
	CreatedAt  time.Time       `json:"created_at"`
}
type AuditHistoryPage struct {
	Events      []AuditEvent `json:"events"`
	NextAfterID uint64       `json:"next_after_id,omitempty"`
}
type CopyFailure struct {
	TemplateID              string
	TemplateVersion         string
	ProjectID               string
	Cause                   error
	AuditPersistenceFailure bool
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
	// CanManageTemplateCatalog rechecks the current tenant role in the same
	// read transaction. Public catalog readers must not receive unpublished
	// drafts, source IDs, or another project's recall impact list.
	CanManageTemplateCatalog(context.Context, *gorm.DB, Actor) (bool, error)
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
	// Fixtures is the merged replay fixture store. Fixture registration
	// fails closed when it is absent; the read paths keep working without it
	// only because the builtin set is code-defined.
	Fixtures ReplayFixtureStore
}
type Service struct {
	db    *gorm.DB
	ports Ports
}

func NewService(db *gorm.DB, ports Ports) *Service { return &Service{db: db, ports: ports} }
