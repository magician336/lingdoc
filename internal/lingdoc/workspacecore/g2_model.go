package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrG2Unavailable        = errors.New("lingdoc: research or agent dependency unavailable")
	ErrG2ConsentRequired    = errors.New("lingdoc: explicit action confirmation required")
	ErrG2EvidenceIncomplete = errors.New("lingdoc: evidence is incomplete or not current")
	ErrG2OutcomeUnknown     = errors.New("lingdoc: external action outcome unknown; reconcile before retry")
)

const (
	G2ResearchTaskKind = "research_task"
	G2ResearchRunKind  = "research_run"
	G2ImportKind       = "import_candidate"
	G2AnchorKind       = "source_anchor"
	G2ClaimKind        = "research_claim"
	G2CardKind         = "evidence_card"
	G2SessionKind      = "agent_session"
	G2TurnKind         = "agent_turn"
	G2ActionKind       = "action_proposal"
)

type G2VersionRef struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Kind    string `json:"kind,omitempty"`
}
type G2Expectation struct {
	ExpectedProjectVersion  int64 `json:"expected_project_version"`
	ExpectedContextRevision int64 `json:"expected_context_revision"`
	ExpectedVersion         int64 `json:"expected_version"`
}
type G2Basis struct {
	ProjectVersion       int64            `json:"project_version"`
	SpecRevision         int64            `json:"spec_revision"`
	ContextRevision      int64            `json:"context_revision"`
	ChapterID            string           `json:"chapter_id,omitempty"`
	ChapterVersionID     string           `json:"chapter_version_id,omitempty"`
	AssetVersions        []G2AssetVersion `json:"asset_versions"`
	SourceCatalogVersion string           `json:"source_catalog_version"`
}
type G2AssetVersion struct {
	AssetID       string `json:"asset_id"`
	AssetRevision int    `json:"asset_revision"`
}
type ResearchTask struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	Version        int64     `json:"version"`
	ChapterID      string    `json:"chapter_id,omitempty"`
	Goal           string    `json:"goal"`
	QuestionType   string    `json:"question_type"`
	Scope          string    `json:"scope"`
	Exclusions     []string  `json:"exclusions"`
	Queries        []string  `json:"queries"`
	AssetIDs       []string  `json:"asset_ids"`
	ProviderID     string    `json:"provider_id,omitempty"`
	ExpectedOutput string    `json:"expected_output"`
	Status         string    `json:"status"`
	Paused         bool      `json:"paused"`
	Basis          G2Basis   `json:"basis"`
	LatestRunID    string    `json:"latest_run_id,omitempty"`
	ApprovedBy     string    `json:"approved_by,omitempty"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type SaveResearchTaskInput struct {
	G2Expectation
	TaskID         string   `json:"task_id,omitempty"`
	ChapterID      string   `json:"chapter_id,omitempty"`
	Goal           string   `json:"goal"`
	QuestionType   string   `json:"question_type"`
	Scope          string   `json:"scope"`
	Exclusions     []string `json:"exclusions"`
	Queries        []string `json:"queries"`
	AssetIDs       []string `json:"asset_ids"`
	ProviderID     string   `json:"provider_id,omitempty"`
	ExpectedOutput string   `json:"expected_output"`
}
type G2ConfirmedInput struct {
	G2Expectation
	Confirmed bool   `json:"confirmed"`
	Reason    string `json:"reason"`
}
type RunResearchTaskInput struct {
	G2Expectation
	ResumeRunID string `json:"resume_run_id,omitempty"`
}
type ControlResearchTaskInput struct {
	G2Expectation
	Action string `json:"action"`
	Reason string `json:"reason"`
}
type ResearchResult struct {
	ID             string    `json:"id"`
	Level          string    `json:"level"`
	SourceID       string    `json:"source_id,omitempty"`
	AssetID        string    `json:"asset_id,omitempty"`
	AssetRevision  int       `json:"asset_revision,omitempty"`
	Locator        string    `json:"locator,omitempty"`
	QuotedTextHash string    `json:"quoted_text_hash,omitempty"`
	Title          string    `json:"title"`
	URL            string    `json:"url,omitempty"`
	Author         string    `json:"author,omitempty"`
	PublishedAt    string    `json:"published_at,omitempty"`
	RetrievedAt    time.Time `json:"retrieved_at"`
	Provider       string    `json:"provider"`
	SourceType     string    `json:"source_type"`
	QualityNote    string    `json:"quality_note"`
	Uncertainty    string    `json:"uncertainty"`
	Summary        string    `json:"summary,omitempty"`
	Status         string    `json:"status"`
}
type ResearchRound struct {
	Number      int       `json:"number"`
	Query       string    `json:"query"`
	Provider    string    `json:"provider"`
	RetrievedAt time.Time `json:"retrieved_at"`
	ResultIDs   []string  `json:"result_ids"`
	Status      string    `json:"status"`
	FailureCode string    `json:"failure_code,omitempty"`
	Feedback    string    `json:"feedback,omitempty"`
}
type ResearchRun struct {
	ID            string           `json:"id"`
	ProjectID     string           `json:"project_id"`
	Version       int64            `json:"version"`
	TaskID        string           `json:"task_id"`
	TaskVersion   int64            `json:"task_version"`
	Basis         G2Basis          `json:"basis"`
	Rounds        []ResearchRound  `json:"rounds"`
	Results       []ResearchResult `json:"results"`
	Status        string           `json:"status"`
	FailureCode   string           `json:"failure_code,omitempty"`
	RunMode       string           `json:"run_mode"`
	CorrelationID string           `json:"correlation_id"`
	CreatedBy     string           `json:"created_by"`
	StartedAt     time.Time        `json:"started_at"`
	FinishedAt    *time.Time       `json:"finished_at,omitempty"`
}
type ImportCandidate struct {
	KnowledgeBaseID       string    `json:"knowledge_base_id,omitempty"`
	ID                    string    `json:"id"`
	ProjectID             string    `json:"project_id"`
	Version               int64     `json:"version"`
	RunID                 string    `json:"run_id,omitempty"`
	ResultID              string    `json:"result_id,omitempty"`
	SourceID              string    `json:"source_id,omitempty"`
	KnowledgeID           string    `json:"knowledge_id,omitempty"`
	URL                   string    `json:"url,omitempty"`
	FinalURL              string    `json:"final_url,omitempty"`
	Title                 string    `json:"title"`
	Level                 string    `json:"level"`
	SelectionType         string    `json:"selection_type"`
	ConversationSessionID string    `json:"conversation_session_id,omitempty"`
	ConversationTurnIDs   []string  `json:"conversation_turn_ids,omitempty"`
	Locator               string    `json:"locator,omitempty"`
	QuotedTextHash        string    `json:"quoted_text_hash,omitempty"`
	Purpose               string    `json:"purpose"`
	SaveMode              string    `json:"save_mode"`
	Scope                 string    `json:"scope"`
	ModelUseAllowed       bool      `json:"model_use_allowed"`
	License               string    `json:"license"`
	Status                string    `json:"status"`
	Basis                 G2Basis   `json:"basis"`
	AssetID               string    `json:"asset_id,omitempty"`
	AssetRevision         int       `json:"asset_revision,omitempty"`
	FailureCode           string    `json:"failure_code,omitempty"`
	DedupKey              string    `json:"dedup_key"`
	CreatedBy             string    `json:"created_by"`
	CreatedAt             time.Time `json:"created_at"`
}
type CreateImportCandidateInput struct {
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
	G2Expectation
	RunID                 string   `json:"run_id,omitempty"`
	ResultID              string   `json:"result_id,omitempty"`
	SourceID              string   `json:"source_id,omitempty"`
	URL                   string   `json:"url,omitempty"`
	Title                 string   `json:"title"`
	Purpose               string   `json:"purpose"`
	SaveMode              string   `json:"save_mode"`
	Scope                 string   `json:"scope"`
	ModelUseAllowed       bool     `json:"model_use_allowed"`
	License               string   `json:"license"`
	ConversationSessionID string   `json:"conversation_session_id,omitempty"`
	ConversationTurnIDs   []string `json:"conversation_turn_ids,omitempty"`
}
type SourceAnchor struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"project_id"`
	Version          int64     `json:"version"`
	SourceID         string    `json:"source_id"`
	AssetID          string    `json:"asset_id"`
	AssetRevision    int       `json:"asset_revision"`
	Locator          string    `json:"locator"`
	Page             int       `json:"page,omitempty"`
	Section          string    `json:"section,omitempty"`
	Paragraph        int       `json:"paragraph,omitempty"`
	Coordinates      []float64 `json:"coordinates,omitempty"`
	QuotedTextHash   string    `json:"quoted_text_hash"`
	SelectionStart   int       `json:"selection_start"`
	SelectionEnd     int       `json:"selection_end"`
	ExtractionType   string    `json:"extraction_type"`
	OCRConfidence    float64   `json:"ocr_confidence"`
	HumanVerified    bool      `json:"human_verified"`
	OriginalOCR      string    `json:"original_ocr,omitempty"`
	CorrectedText    string    `json:"corrected_text,omitempty"`
	CorrectionReason string    `json:"correction_reason,omitempty"`
	Status           string    `json:"status"`
	Basis            G2Basis   `json:"basis"`
	DedupKey         string    `json:"dedup_key"`
	CreatedBy        string    `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
}
type SaveSourceAnchorInput struct {
	G2Expectation
	AnchorID         string    `json:"anchor_id,omitempty"`
	SourceID         string    `json:"source_id"`
	SelectionStart   int       `json:"selection_start"`
	SelectionEnd     int       `json:"selection_end"`
	Page             int       `json:"page,omitempty"`
	Section          string    `json:"section,omitempty"`
	Paragraph        int       `json:"paragraph,omitempty"`
	Coordinates      []float64 `json:"coordinates,omitempty"`
	ExtractionType   string    `json:"extraction_type"`
	OCRConfidence    float64   `json:"ocr_confidence"`
	HumanVerified    bool      `json:"human_verified"`
	CorrectedText    string    `json:"corrected_text,omitempty"`
	CorrectionReason string    `json:"correction_reason,omitempty"`
	Confirmed        bool      `json:"confirmed"`
}
type ResearchClaim struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Version     int64     `json:"version"`
	Text        string    `json:"text"`
	Scope       string    `json:"scope"`
	Status      string    `json:"status"`
	Explanation string    `json:"explanation,omitempty"`
	Basis       G2Basis   `json:"basis"`
	Reason      string    `json:"reason"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}
type SaveResearchClaimInput struct {
	G2Expectation
	ClaimID     string `json:"claim_id,omitempty"`
	Text        string `json:"text"`
	Scope       string `json:"scope"`
	Status      string `json:"status"`
	Explanation string `json:"explanation,omitempty"`
	Reason      string `json:"reason"`
	Confirmed   bool   `json:"confirmed"`
}
type EvidenceConfirmation struct {
	ConfirmedBy     string         `json:"confirmed_by"`
	ConfirmedAt     time.Time      `json:"confirmed_at"`
	CardVersion     int64          `json:"card_version"`
	ClaimRef        G2VersionRef   `json:"claim_ref"`
	AnchorRefs      []G2VersionRef `json:"anchor_refs"`
	ContextRevision int64          `json:"context_revision"`
	ScopeChapterIDs []string       `json:"scope_chapter_ids"`
	Reason          string         `json:"reason"`
	Meaning         string         `json:"meaning"`
}
type EvidenceCard struct {
	ID                     string                `json:"id"`
	ProjectID              string                `json:"project_id"`
	Version                int64                 `json:"version"`
	ClaimRef               *G2VersionRef         `json:"claim_ref,omitempty"`
	AnchorRefs             []G2VersionRef        `json:"anchor_refs"`
	SourceIDs              []string              `json:"source_ids"`
	SupportType            string                `json:"support_type"`
	Scope                  string                `json:"scope"`
	ScopeChapterIDs        []string              `json:"scope_chapter_ids"`
	Limitations            string                `json:"limitations"`
	Gaps                   string                `json:"gaps"`
	AlternativeExplanation string                `json:"alternative_explanation"`
	Status                 string                `json:"status"`
	Basis                  G2Basis               `json:"basis"`
	Confirmation           *EvidenceConfirmation `json:"confirmation,omitempty"`
	InvalidReason          string                `json:"invalid_reason,omitempty"`
	CreatedBy              string                `json:"created_by"`
	CreatedAt              time.Time             `json:"created_at"`
}
type SaveEvidenceCardInput struct {
	G2Expectation
	CardID                 string         `json:"card_id,omitempty"`
	ClaimRef               *G2VersionRef  `json:"claim_ref,omitempty"`
	AnchorRefs             []G2VersionRef `json:"anchor_refs"`
	SupportType            string         `json:"support_type"`
	Scope                  string         `json:"scope"`
	ScopeChapterIDs        []string       `json:"scope_chapter_ids"`
	Limitations            string         `json:"limitations"`
	Gaps                   string         `json:"gaps"`
	AlternativeExplanation string         `json:"alternative_explanation"`
	Confirmed              bool           `json:"confirmed"`
}
type ReviewEvidenceCardInput struct {
	G2Expectation
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	Confirmed bool   `json:"confirmed"`
}
type G2Source struct {
	SourceID        string `json:"source_id"`
	AssetID         string `json:"asset_id"`
	AssetRevision   int    `json:"asset_revision"`
	KnowledgeID     string `json:"knowledge_id"`
	Title           string `json:"title"`
	Locator         string `json:"locator"`
	QuotedText      string `json:"quoted_text"`
	QuotedTextHash  string `json:"quoted_text_hash"`
	Status          string `json:"status"`
	ProcessingState string `json:"processing_state"`
	StableOriginal  bool   `json:"stable_original"`
	ModelUseAllowed bool   `json:"model_use_allowed"`
}
type AgentSourceRef struct {
	SourceID        string `json:"source_id"`
	AssetID         string `json:"asset_id"`
	AssetRevision   int    `json:"asset_revision"`
	QuotedTextHash  string `json:"quoted_text_hash"`
	Locator         string `json:"locator"`
	QuotedText      string `json:"quoted_text,omitempty"`
	ModelUseAllowed bool   `json:"model_use_allowed"`
}
type AgentContextPackage struct {
	ProjectID          string           `json:"project_id"`
	ProjectVersion     int64            `json:"project_version"`
	SpecRevision       int64            `json:"spec_revision"`
	ContextRevision    int64            `json:"context_revision"`
	ChapterID          string           `json:"chapter_id,omitempty"`
	ChapterVersionID   string           `json:"chapter_version_id,omitempty"`
	TaskID             string           `json:"task_id,omitempty"`
	TaskVersion        int64            `json:"task_version,omitempty"`
	TemplateID         string           `json:"template_id"`
	TemplateVersion    string           `json:"template_version"`
	RuleSetID          string           `json:"ruleset_id,omitempty"`
	RuleSetVersion     string           `json:"ruleset_version,omitempty"`
	SourceRefs         []AgentSourceRef `json:"source_refs"`
	CardRefs           []G2VersionRef   `json:"card_refs"`
	SelectionReasons   []string         `json:"selection_reasons"`
	AuthorizationState string           `json:"authorization_state"`
	OutputConstraints  []string         `json:"output_constraints"`
	CorrelationID      string           `json:"correlation_id"`
}
type AgentMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type AgentSession struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Version   int64     `json:"version"`
	Agent     string    `json:"agent"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}
type AgentTurn struct {
	ID           string              `json:"id"`
	ProjectID    string              `json:"project_id"`
	SessionID    string              `json:"session_id"`
	Version      int64               `json:"version"`
	Agent        string              `json:"agent"`
	UserInput    string              `json:"user_input"`
	Reply        string              `json:"reply"`
	Suggestions  json.RawMessage     `json:"suggestions,omitempty"`
	Context      AgentContextPackage `json:"context"`
	Status       string              `json:"status"`
	FailureCode  string              `json:"failure_code,omitempty"`
	ModelID      string              `json:"model_id,omitempty"`
	RunMode      string              `json:"run_mode"`
	InputTokens  int                 `json:"input_tokens"`
	OutputTokens int                 `json:"output_tokens"`
	CreatedBy    string              `json:"created_by"`
	CreatedAt    time.Time           `json:"created_at"`
}
type AgentContextInput struct {
	G2Expectation
	ChapterID                 string         `json:"chapter_id,omitempty"`
	TaskID                    string         `json:"task_id,omitempty"`
	SourceIDs                 []string       `json:"source_ids"`
	CardRefs                  []G2VersionRef `json:"card_refs"`
	SensitiveContextConfirmed bool           `json:"sensitive_context_confirmed"`
}
type AgentTurnInput struct {
	AgentContextInput
	UserInput string `json:"user_input"`
}
type G2ModelRequest struct {
	Agent           string
	Purpose         string
	Context         AgentContextPackage
	History         []AgentMessage
	UserInput       string
	StructuredInput json.RawMessage
}
type G2ModelReply struct {
	Content          string
	StructuredOutput json.RawMessage
	ModelID          string
	RunMode          string
	InputTokens      int
	OutputTokens     int
	FinishReason     string
}
type ActionProposal struct {
	ID             string          `json:"id"`
	ProjectID      string          `json:"project_id"`
	Version        int64           `json:"version"`
	Action         string          `json:"action"`
	TargetID       string          `json:"target_id"`
	Input          json.RawMessage `json:"input"`
	InputHash      string          `json:"input_hash"`
	SourceIDs      []string        `json:"source_ids"`
	Basis          G2Basis         `json:"basis"`
	ExpectedEffect string          `json:"expected_effect"`
	Reversibility  string          `json:"reversibility"`
	Status         string          `json:"status"`
	Result         json.RawMessage `json:"result,omitempty"`
	FailureCode    string          `json:"failure_code,omitempty"`
	CreatedBy      string          `json:"created_by"`
	CreatedAt      time.Time       `json:"created_at"`
}
type CreateActionProposalInput struct {
	G2Expectation
	Action         string          `json:"action"`
	TargetID       string          `json:"target_id"`
	Input          json.RawMessage `json:"input"`
	SourceIDs      []string        `json:"source_ids"`
	ExpectedEffect string          `json:"expected_effect"`
}
type G2ResearchSearcher interface {
	SearchResearch(context.Context, Actor, string, ResearchTask, string) ([]ResearchResult, string, error)
}
type G2SourceReader interface {
	ReadG2Source(context.Context, Actor, string, string) (G2Source, error)
	CheckG2AssetScope(context.Context, Actor, string, []string) ([]G2AssetVersion, error)
}
type G2AgentModel interface {
	Reply(context.Context, Actor, G2ModelRequest) (G2ModelReply, error)
}
type G2ImportedAsset struct {
	AssetID         string
	AssetRevision   int
	ProcessingState string
	KnowledgeID     string
	FinalURL        string
}
type G2ImportProcessor interface {
	ImportResearchCandidate(context.Context, Actor, string, string, ImportCandidate) (G2ImportedAsset, error)
}
type G2ActionExecutor interface {
	ExecuteAgentAction(context.Context, Actor, ActionProposal, string) (json.RawMessage, error)
}
type G2Runtime struct {
	Sources  G2SourceReader
	Research G2ResearchSearcher
	Model    G2AgentModel
	Imports  G2ImportProcessor
	Actions  G2ActionExecutor
}
type G2DependencyFact struct {
	Source    G2VersionRef `json:"source"`
	Target    G2VersionRef `json:"target"`
	SourceIDs []string     `json:"source_ids"`
}
type G2EvidenceSelection struct {
	Cards           []EvidenceCard   `json:"cards"`
	Sources         []AgentSourceRef `json:"sources"`
	Pending         []G2VersionRef   `json:"pending"`
	ContextRevision int64            `json:"context_revision"`
}

type G2ResearchCatalog interface {
	ResearchCatalogVersion(context.Context, Actor, string, ResearchTask) (string, error)
}

// ProjectAssetPolicy narrows existing project/KB access. It never creates a KB
// grant. Version is the native asset_revision; PolicyVersion is domain history.
const G2AssetPolicyKind = "project_asset_policy"

type ProjectAssetPolicy struct {
	ID                string    `json:"id"`
	ProjectID         string    `json:"project_id"`
	AssetID           string    `json:"asset_id"`
	Version           int64     `json:"version"`
	AssetRevision     int64     `json:"asset_revision"`
	PolicyVersion     int64     `json:"policy_version"`
	Scope             string    `json:"scope"`
	Purpose           string    `json:"purpose"`
	AllowedUserIDs    []string  `json:"allowed_user_ids"`
	ModelUseAllowed   bool      `json:"model_use_allowed"`
	Reason            string    `json:"reason"`
	CreatedBy         string    `json:"created_by"`
	GrantedBy         string    `json:"granted_by"`
	SourceVersionHash string    `json:"source_version_hash"`
	ContextRevision   int64     `json:"context_revision"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type G2ResearchRoundIdentity interface {
	ResearchRoundProvider(context.Context, Actor, string, ResearchTask) (string, error)
}
