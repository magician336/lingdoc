package workspacecore

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/clienterror"
)

var (
	ErrDependencyUnavailable            = clienterror.New(clienterror.Form{Status: 503, Code: "dependency_unavailable", Message: "资料或模型能力暂不可用。", Retryable: true}, "lingdoc: research or agent dependency unavailable")
	ErrConsentRequired                  = clienterror.New(clienterror.Form{Status: 422, Code: "confirmation_required", Message: "请明确核对范围并确认该操作。", Retryable: false}, "lingdoc: explicit action confirmation required")
	ErrEvidenceIncomplete               = clienterror.New(clienterror.Form{Status: 422, Code: "evidence_incomplete", Message: "证据卡、原文定位或当前版本尚不完整。", Retryable: false}, "lingdoc: evidence is incomplete or not current")
	ErrOutcomeUnknown                   = clienterror.New(clienterror.Form{Status: 409, Code: "outcome_unknown", Message: "原操作结果尚待核对，请勿重复执行。", Retryable: false}, "lingdoc: external action outcome unknown; reconcile before retry")
	ErrAcademicGlobalCatalogUnavailable = clienterror.New(clienterror.Form{Status: 503, Code: "academic_global_catalog_unavailable", Message: "学术来源全局白名单尚未配置；当前检索与元数据候选均已阻断。", Retryable: false}, "lingdoc: approved global academic catalog is unavailable")
	ErrResearchBudgetUnavailable        = clienterror.New(clienterror.Form{Status: 503, Code: "research_budget_unavailable", Message: "项目累计检索配额与持久化账本尚未配置；当前研究检索已阻断。", Retryable: false}, "lingdoc: project research budget ledger is unavailable")
	ErrResearchBudgetSettlementUnknown  = clienterror.New(clienterror.Form{Status: 503, Code: "research_budget_settlement_unknown", Message: "研究配额账本结算结果未能确认；请先核对账本后再重试。", Retryable: false}, "lingdoc: research budget settlement outcome is unknown")
)

const (
	ResearchTaskKind    = "research_task"
	ResearchRunKind     = "research_run"
	ImportCandidateKind = "import_candidate"
	SourceAnchorKind    = "source_anchor"
	ResearchClaimKind   = "research_claim"
	EvidenceCardKind    = "evidence_card"
	AgentSessionKind    = "agent_session"
	AgentTurnKind       = "agent_turn"
	ActionProposalKind  = "action_proposal"
)

// The agents a session may declare, and the shapes an answer may take.
//
// These words belong to the server. The model adapter refers to them instead
// of comparing loose strings: a prompt that branches on a misspelled word
// loses its instructions without anybody noticing, and the server would go on
// believing the turn got the answer shape it asked for.
//
// The two vocabularies have different jobs and stay apart: an agent is the
// role a session plays, a purpose is the shape the answer of that role takes.
const (
	AgentTaskAssistant = "task_assistant"
	AgentResearch      = "research_agent"
	AgentConstraint    = "constraint"
)

const (
	AgentPurposeConversation   = "conversation"
	AgentPurposeRuleSuggestion = "rule_suggestion"
	AgentPurposeResearch       = "research_proposal"
)

// AgentPurposeForAgent derives the purpose of a turn from the agent its
// session declares.
//
// The purpose is the shape of the answer, and the shape is the server's to
// decide: the client states no purpose, so the request shape - and with it
// the idempotency digest - does not move, and a key issued before this
// derivation existed still replays.
//
// The research agent answers with a research proposal; every other session
// keeps the conversation shape it has always had.
func AgentPurposeForAgent(agent string) string {
	if agent == AgentResearch {
		return AgentPurposeResearch
	}
	return AgentPurposeConversation
}

type VersionRef struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Kind    string `json:"kind,omitempty"`
}
type VersionExpectation struct {
	ExpectedProjectVersion  int64 `json:"expected_project_version"`
	ExpectedContextRevision int64 `json:"expected_context_revision"`
	ExpectedVersion         int64 `json:"expected_version"`
}
type VersionBasis struct {
	ProjectVersion       int64                 `json:"project_version"`
	SpecRevision         int64                 `json:"spec_revision"`
	ContextRevision      int64                 `json:"context_revision"`
	ChapterID            string                `json:"chapter_id,omitempty"`
	ChapterVersionID     string                `json:"chapter_version_id,omitempty"`
	AssetVersions        []ProjectAssetVersion `json:"asset_versions"`
	SourceCatalogVersion string                `json:"source_catalog_version"`
}

// String is the comparable form of the basis.
//
// It encodes exactly the fields that decide whether a basis still holds: the
// project version, the research-conditions revision and the context revision.
// The other fields describe what the basis was collected for, not whether it
// is still current, so they stay out of the comparison.
func (b VersionBasis) String() string {
	return strconv.FormatInt(b.ProjectVersion, 10) + ":" +
		strconv.FormatInt(b.SpecRevision, 10) + ":" +
		strconv.FormatInt(b.ContextRevision, 10)
}

type ProjectAssetVersion struct {
	AssetID       string `json:"asset_id"`
	AssetRevision int    `json:"asset_revision"`
}

// The research task's own bounds. They live here, next to the task they
// belong to, and the research proposal contract refers to them instead of
// restating them: two copies of a number are two numbers that drift, and a
// proposal that decodes has to be savable as a task without being edited into
// shape first.
const (
	// researchTaskMinimumQueries is the smallest number of queries a task may
	// carry. One query does not define a search.
	researchTaskMinimumQueries = 2
	// researchTaskMaximumQueries is the largest number of queries a task may
	// carry.
	researchTaskMaximumQueries = 8
	// researchResultsPerRound is the maximum number of result records retained
	// from one provider request, bounding one run to at most 160 results.
	researchResultsPerRound        = 20
	researchRunBudgetPolicyVersion = "research-run-bounds-v1"
	// researchTaskMaximumQueryLength is the longest a single query may be.
	researchTaskMaximumQueryLength = 1000
	// researchTaskMaximumTextLength is the longest a task's long text field may
	// be: the goal, and the proposal's scope and expected output.
	researchTaskMaximumTextLength = 6000
)

type ResearchTask struct {
	ID             string       `json:"id"`
	ProjectID      string       `json:"project_id"`
	Version        int64        `json:"version"`
	ChapterID      string       `json:"chapter_id,omitempty"`
	Goal           string       `json:"goal"`
	QuestionType   string       `json:"question_type"`
	Scope          string       `json:"scope"`
	Exclusions     []string     `json:"exclusions"`
	Queries        []string     `json:"queries"`
	DateFrom       string       `json:"date_from,omitempty"`
	DateTo         string       `json:"date_to,omitempty"`
	AssetIDs       []string     `json:"asset_ids"`
	ProviderID     string       `json:"provider_id,omitempty"`
	ExpectedOutput string       `json:"expected_output"`
	Status         string       `json:"status"`
	Paused         bool         `json:"paused"`
	Basis          VersionBasis `json:"basis"`
	LatestRunID    string       `json:"latest_run_id,omitempty"`
	ApprovedBy     string       `json:"approved_by,omitempty"`
	CreatedBy      string       `json:"created_by"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}
type SaveResearchTaskInput struct {
	VersionExpectation
	TaskID         string   `json:"task_id,omitempty"`
	ChapterID      string   `json:"chapter_id,omitempty"`
	Goal           string   `json:"goal"`
	QuestionType   string   `json:"question_type"`
	Scope          string   `json:"scope"`
	Exclusions     []string `json:"exclusions"`
	Queries        []string `json:"queries"`
	DateFrom       string   `json:"date_from,omitempty"`
	DateTo         string   `json:"date_to,omitempty"`
	AssetIDs       []string `json:"asset_ids"`
	ProviderID     string   `json:"provider_id,omitempty"`
	ExpectedOutput string   `json:"expected_output"`
}
type ConfirmationInput struct {
	VersionExpectation
	Confirmed bool   `json:"confirmed"`
	Reason    string `json:"reason"`
}
type RunResearchTaskInput struct {
	VersionExpectation
	ResumeRunID string `json:"resume_run_id,omitempty"`
}
type ControlResearchTaskInput struct {
	VersionExpectation
	Action string `json:"action"`
	Reason string `json:"reason"`
}
type ResearchResult struct {
	ID                    string    `json:"id"`
	Level                 string    `json:"level"`
	SourceID              string    `json:"source_id,omitempty"`
	AssetID               string    `json:"asset_id,omitempty"`
	AssetRevision         int       `json:"asset_revision,omitempty"`
	Locator               string    `json:"locator,omitempty"`
	QuotedTextHash        string    `json:"quoted_text_hash,omitempty"`
	Title                 string    `json:"title"`
	URL                   string    `json:"url,omitempty"`
	Author                string    `json:"author,omitempty"`
	PublishedAt           string    `json:"published_at,omitempty"`
	DOI                   string    `json:"doi,omitempty"`
	Journal               string    `json:"journal,omitempty"`
	ISSNs                 []string  `json:"issns,omitempty"`
	LicenseURLs           []string  `json:"license_urls,omitempty"`
	LicenseStatus         string    `json:"license_status,omitempty"`
	CatalogVersion        string    `json:"catalog_version,omitempty"`
	PolicySnapshotVersion string    `json:"policy_snapshot_version,omitempty"`
	PolicyStatus          string    `json:"policy_status,omitempty"`
	PolicyRuleID          string    `json:"policy_rule_id,omitempty"`
	RetractionStatus      string    `json:"retraction_status,omitempty"`
	DuplicateStatus       string    `json:"duplicate_status,omitempty"`
	DuplicateOf           string    `json:"duplicate_of,omitempty"`
	AcquisitionStatus     string    `json:"acquisition_status,omitempty"`
	RetrievedAt           time.Time `json:"retrieved_at"`
	Provider              string    `json:"provider"`
	SourceType            string    `json:"source_type"`
	QualityNote           string    `json:"quality_note"`
	Uncertainty           string    `json:"uncertainty"`
	Summary               string    `json:"summary,omitempty"`
	Status                string    `json:"status"`
}
type ResearchRound struct {
	Number        int        `json:"number"`
	Query         string     `json:"query"`
	DateFrom      string     `json:"date_from,omitempty"`
	DateTo        string     `json:"date_to,omitempty"`
	Provider      string     `json:"provider"`
	RetrievedAt   time.Time  `json:"retrieved_at"`
	ResultIDs     []string   `json:"result_ids"`
	Status        string     `json:"status"`
	FailureCode   string     `json:"failure_code,omitempty"`
	FailureDetail string     `json:"failure_detail,omitempty"`
	Feedback      string     `json:"feedback,omitempty"`
	FeedbackBy    string     `json:"feedback_by,omitempty"`
	FeedbackAt    *time.Time `json:"feedback_at,omitempty"`
}

// ResearchRoundFeedbackInput records one researcher's note for an existing
// query round. The run's expected version protects concurrent edits.
type ResearchRoundFeedbackInput struct {
	VersionExpectation
	RoundNumber int    `json:"round_number"`
	Feedback    string `json:"feedback"`
}

// ResearchRoundFeedbackReceipt intentionally excludes search results and
// source text; consumers re-read the authorized run after a write.
type ResearchRoundFeedbackReceipt struct {
	RunID       string `json:"run_id"`
	RunVersion  int64  `json:"run_version"`
	RoundNumber int    `json:"round_number"`
}

// ResearchUsageLedger snapshots conservative per-run ceilings and usage.
// Academic search uses no model calls; assistant turns remain in their own
// existing model-call ledger.
type ResearchUsageLedger struct {
	PolicyVersion  string `json:"policy_version"`
	SearchLimit    int    `json:"search_limit"`
	SearchesUsed   int    `json:"searches_used"`
	ResultLimit    int    `json:"result_limit"`
	ResultsUsed    int    `json:"results_used"`
	ModelCallLimit int    `json:"model_call_limit"`
	ModelCallsUsed int    `json:"model_calls_used"`
}

func newResearchUsageLedger() ResearchUsageLedger {
	return ResearchUsageLedger{
		PolicyVersion:  researchRunBudgetPolicyVersion,
		SearchLimit:    researchTaskMaximumQueries,
		ResultLimit:    researchTaskMaximumQueries * researchResultsPerRound,
		ModelCallLimit: 0,
	}
}

// normalizeResearchUsage supplies the bounded ledger to older persisted runs.
// A legacy run's searches and retained result references are charged before it
// can be resumed, so adding the ledger cannot reset its remaining allowance.
func normalizeResearchUsage(run *ResearchRun) {
	if run.Usage.PolicyVersion == researchRunBudgetPolicyVersion {
		return
	}
	usage := newResearchUsageLedger()
	usage.SearchesUsed = len(run.Rounds)
	for _, round := range run.Rounds {
		usage.ResultsUsed += len(round.ResultIDs)
	}
	if usage.ResultsUsed == 0 && len(run.Results) > 0 {
		usage.ResultsUsed = len(run.Results)
	}
	run.Usage = usage
}

type ResearchRun struct {
	ID            string              `json:"id"`
	ProjectID     string              `json:"project_id"`
	Version       int64               `json:"version"`
	TaskID        string              `json:"task_id"`
	TaskVersion   int64               `json:"task_version"`
	Basis         VersionBasis        `json:"basis"`
	Rounds        []ResearchRound     `json:"rounds"`
	Results       []ResearchResult    `json:"results"`
	Usage         ResearchUsageLedger `json:"usage"`
	Status        string              `json:"status"`
	FailureCode   string              `json:"failure_code,omitempty"`
	FailureDetail string              `json:"failure_detail,omitempty"`
	RunMode       string              `json:"run_mode"`
	CorrelationID string              `json:"correlation_id"`
	CreatedBy     string              `json:"created_by"`
	StartedAt     time.Time           `json:"started_at"`
	FinishedAt    *time.Time          `json:"finished_at,omitempty"`
}
type ImportCandidate struct {
	KnowledgeBaseID       string       `json:"knowledge_base_id,omitempty"`
	ID                    string       `json:"id"`
	ProjectID             string       `json:"project_id"`
	Version               int64        `json:"version"`
	RunID                 string       `json:"run_id,omitempty"`
	ResultID              string       `json:"result_id,omitempty"`
	SourceID              string       `json:"source_id,omitempty"`
	KnowledgeID           string       `json:"knowledge_id,omitempty"`
	URL                   string       `json:"url,omitempty"`
	FinalURL              string       `json:"final_url,omitempty"`
	Title                 string       `json:"title"`
	Author                string       `json:"author,omitempty"`
	PublishedAt           string       `json:"published_at,omitempty"`
	DOI                   string       `json:"doi,omitempty"`
	Journal               string       `json:"journal,omitempty"`
	ISSNs                 []string     `json:"issns,omitempty"`
	LicenseURLs           []string     `json:"license_urls,omitempty"`
	CatalogVersion        string       `json:"catalog_version,omitempty"`
	PolicySnapshotVersion string       `json:"policy_snapshot_version,omitempty"`
	PolicyStatus          string       `json:"policy_status,omitempty"`
	RetractionStatus      string       `json:"retraction_status,omitempty"`
	DuplicateStatus       string       `json:"duplicate_status,omitempty"`
	AcquisitionStatus     string       `json:"acquisition_status,omitempty"`
	Level                 string       `json:"level"`
	SelectionType         string       `json:"selection_type"`
	ConversationSessionID string       `json:"conversation_session_id,omitempty"`
	ConversationTurnIDs   []string     `json:"conversation_turn_ids,omitempty"`
	Locator               string       `json:"locator,omitempty"`
	QuotedTextHash        string       `json:"quoted_text_hash,omitempty"`
	Purpose               string       `json:"purpose"`
	SaveMode              string       `json:"save_mode"`
	Scope                 string       `json:"scope"`
	ModelUseAllowed       bool         `json:"model_use_allowed"`
	License               string       `json:"license"`
	Status                string       `json:"status"`
	Basis                 VersionBasis `json:"basis"`
	AssetID               string       `json:"asset_id,omitempty"`
	AssetRevision         int          `json:"asset_revision,omitempty"`
	FailureCode           string       `json:"failure_code,omitempty"`
	DedupKey              string       `json:"dedup_key"`
	CreatedBy             string       `json:"created_by"`
	CreatedAt             time.Time    `json:"created_at"`
}
type CreateImportCandidateInput struct {
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
	VersionExpectation
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
	ID               string       `json:"id"`
	ProjectID        string       `json:"project_id"`
	Version          int64        `json:"version"`
	SourceID         string       `json:"source_id"`
	AssetID          string       `json:"asset_id"`
	AssetRevision    int          `json:"asset_revision"`
	Locator          string       `json:"locator"`
	Page             int          `json:"page,omitempty"`
	Section          string       `json:"section,omitempty"`
	Paragraph        int          `json:"paragraph,omitempty"`
	Coordinates      []float64    `json:"coordinates,omitempty"`
	QuotedTextHash   string       `json:"quoted_text_hash"`
	SourceTextHash   string       `json:"source_text_hash,omitempty"`
	SelectionStart   int          `json:"selection_start"`
	SelectionEnd     int          `json:"selection_end"`
	ExtractionType   string       `json:"extraction_type"`
	OCRConfidence    float64      `json:"ocr_confidence"`
	HumanVerified    bool         `json:"human_verified"`
	OriginalOCR      string       `json:"original_ocr,omitempty"`
	CorrectedText    string       `json:"corrected_text,omitempty"`
	CorrectionReason string       `json:"correction_reason,omitempty"`
	Status           string       `json:"status"`
	Basis            VersionBasis `json:"basis"`
	DedupKey         string       `json:"dedup_key"`
	CreatedBy        string       `json:"created_by"`
	CreatedAt        time.Time    `json:"created_at"`
}
type SaveSourceAnchorInput struct {
	VersionExpectation
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
	ID          string       `json:"id"`
	ProjectID   string       `json:"project_id"`
	Version     int64        `json:"version"`
	Text        string       `json:"text"`
	Scope       string       `json:"scope"`
	Status      string       `json:"status"`
	Explanation string       `json:"explanation,omitempty"`
	Basis       VersionBasis `json:"basis"`
	Reason      string       `json:"reason"`
	CreatedBy   string       `json:"created_by"`
	CreatedAt   time.Time    `json:"created_at"`
}
type SaveResearchClaimInput struct {
	VersionExpectation
	ClaimID     string `json:"claim_id,omitempty"`
	Text        string `json:"text"`
	Scope       string `json:"scope"`
	Status      string `json:"status"`
	Explanation string `json:"explanation,omitempty"`
	Reason      string `json:"reason"`
	Confirmed   bool   `json:"confirmed"`
}
type EvidenceConfirmation struct {
	ConfirmedBy       string                     `json:"confirmed_by"`
	ConfirmedAt       time.Time                  `json:"confirmed_at"`
	CardVersion       int64                      `json:"card_version"`
	ClaimRef          VersionRef                 `json:"claim_ref"`
	AnchorRefs        []VersionRef               `json:"anchor_refs"`
	AcademicSnapshots []AcademicEvidenceSnapshot `json:"academic_snapshots,omitempty"`
	ContextRevision   int64                      `json:"context_revision"`
	ScopeChapterIDs   []string                   `json:"scope_chapter_ids"`
	Reason            string                     `json:"reason"`
	Meaning           string                     `json:"meaning"`
}
type EvidenceCard struct {
	ID                      string                `json:"id"`
	ProjectID               string                `json:"project_id"`
	Version                 int64                 `json:"version"`
	ClaimRef                *VersionRef           `json:"claim_ref,omitempty"`
	AnchorRefs              []VersionRef          `json:"anchor_refs"`
	AcademicAssociationRefs []VersionRef          `json:"academic_association_refs,omitempty"`
	SourceIDs               []string              `json:"source_ids"`
	SupportType             string                `json:"support_type"`
	Scope                   string                `json:"scope"`
	ScopeChapterIDs         []string              `json:"scope_chapter_ids"`
	Limitations             string                `json:"limitations"`
	Gaps                    string                `json:"gaps"`
	AlternativeExplanation  string                `json:"alternative_explanation"`
	Status                  string                `json:"status"`
	Basis                   VersionBasis          `json:"basis"`
	Confirmation            *EvidenceConfirmation `json:"confirmation,omitempty"`
	InvalidReason           string                `json:"invalid_reason,omitempty"`
	CreatedBy               string                `json:"created_by"`
	CreatedAt               time.Time             `json:"created_at"`
}
type SaveEvidenceCardInput struct {
	VersionExpectation
	CardID                  string       `json:"card_id,omitempty"`
	ClaimRef                *VersionRef  `json:"claim_ref,omitempty"`
	AnchorRefs              []VersionRef `json:"anchor_refs"`
	AcademicAssociationRefs []VersionRef `json:"academic_association_refs,omitempty"`
	SupportType             string       `json:"support_type"`
	Scope                   string       `json:"scope"`
	ScopeChapterIDs         []string     `json:"scope_chapter_ids"`
	Limitations             string       `json:"limitations"`
	Gaps                    string       `json:"gaps"`
	AlternativeExplanation  string       `json:"alternative_explanation"`
	Confirmed               bool         `json:"confirmed"`
}
type ReviewEvidenceCardInput struct {
	VersionExpectation
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	Confirmed bool   `json:"confirmed"`
}
type ResearchSource struct {
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
	// VerifiedPDFPage is internal proof from the source adapter, never a page
	// number supplied by the caller or reconstructed from a chunk index.
	VerifiedPDFPage int `json:"-"`
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

// SelectionReason is why one entry of the context package is present. The text
// is for the reader; the other three fields are the machine-readable key. Kind
// names what the ID belongs to, and Code names the rule that pulled the entry
// in. Rewording Text must never change Kind, ID or Code: the front end attaches
// each reason to its entry by those three fields, never by the prose.
type SelectionReason struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Code string `json:"code"`
	Text string `json:"text"`
}

// The closed vocabularies of SelectionReason. A new kind or code is a contract
// change: the OpenAPI schema and the front-end union move with it.
const (
	SelectionReasonKindCard   = "card"
	SelectionReasonKindSource = "source"
	SelectionReasonKindTask   = "task"

	SelectionReasonUserSelectedTask   = "user_selected_task"
	SelectionReasonUserSelectedCard   = "user_selected_card"
	SelectionReasonUserSelectedSource = "user_selected_source"
	SelectionReasonCardDependency     = "card_dependency"

	// Read-only vocabulary for turns written before #432: the payload held
	// free text, so there is no id to name an entry with.
	SelectionReasonKindLegacy         = "legacy"
	SelectionReasonLegacyUnstructured = "legacy_unstructured"
)

type AgentContextPackage struct {
	ProjectID          string            `json:"project_id"`
	ProjectVersion     int64             `json:"project_version"`
	SpecRevision       int64             `json:"spec_revision"`
	ContextRevision    int64             `json:"context_revision"`
	ChapterID          string            `json:"chapter_id,omitempty"`
	ChapterVersionID   string            `json:"chapter_version_id,omitempty"`
	TaskID             string            `json:"task_id,omitempty"`
	TaskVersion        int64             `json:"task_version,omitempty"`
	TemplateID         string            `json:"template_id"`
	TemplateVersion    string            `json:"template_version"`
	RuleSetID          string            `json:"ruleset_id,omitempty"`
	RuleSetVersion     string            `json:"ruleset_version,omitempty"`
	SourceRefs         []AgentSourceRef  `json:"source_refs"`
	CardRefs           []VersionRef      `json:"card_refs"`
	SelectionReasons   []SelectionReason `json:"selection_reasons"`
	AuthorizationState string            `json:"authorization_state"`
	OutputConstraints  []string          `json:"output_constraints"`
	CorrelationID      string            `json:"correlation_id"`
}

// UnmarshalJSON reads both shapes of selection_reasons: the structured entries
// current builds write, and the free text turns were stored with before #432.
//
// A legacy string becomes SelectionReason{Kind:"legacy", ID:"", Code:
// "legacy_unstructured", Text:s}. It keeps the sentence for a reader but does
// not invent an id: the old payload never recorded which entry the sentence was
// about. Legacy entries are read-only -- Marshal still writes the structured
// shape -- and the front end attaches reasons by kind+id, so a legacy entry is
// not displayed rather than mapped onto a guessed card, source or task.
//
// Every other field decodes exactly as before; only this one field needs the
// two-shape reader, because it is the one whose type changed.
func (p *AgentContextPackage) UnmarshalJSON(data []byte) error {
	type packageAlias AgentContextPackage
	decoded := struct {
		*packageAlias
		SelectionReasons []json.RawMessage `json:"selection_reasons"`
	}{packageAlias: (*packageAlias)(p)}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.SelectionReasons == nil {
		return nil
	}
	reasons := make([]SelectionReason, 0, len(decoded.SelectionReasons))
	for _, entry := range decoded.SelectionReasons {
		var reason SelectionReason
		if err := json.Unmarshal(entry, &reason); err == nil {
			reasons = append(reasons, reason)
			continue
		}
		var text string
		if err := json.Unmarshal(entry, &text); err != nil {
			return err
		}
		reasons = append(reasons, SelectionReason{Kind: SelectionReasonKindLegacy, Code: SelectionReasonLegacyUnstructured, Text: text})
	}
	p.SelectionReasons = reasons
	return nil
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
	VersionExpectation
	ChapterID                 string       `json:"chapter_id,omitempty"`
	TaskID                    string       `json:"task_id,omitempty"`
	SourceIDs                 []string     `json:"source_ids"`
	CardRefs                  []VersionRef `json:"card_refs"`
	SensitiveContextConfirmed bool         `json:"sensitive_context_confirmed"`
}
type AgentTurnInput struct {
	AgentContextInput
	UserInput string `json:"user_input"`
}
type ModelRequest struct {
	Agent           string
	Purpose         string
	Context         AgentContextPackage
	History         []AgentMessage
	UserInput       string
	StructuredInput json.RawMessage
}
type ModelReply struct {
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
	Basis          VersionBasis    `json:"basis"`
	ExpectedEffect string          `json:"expected_effect"`
	Reversibility  string          `json:"reversibility"`
	Status         string          `json:"status"`
	Result         json.RawMessage `json:"result,omitempty"`
	FailureCode    string          `json:"failure_code,omitempty"`
	CreatedBy      string          `json:"created_by"`
	CreatedAt      time.Time       `json:"created_at"`
}
type CreateActionProposalInput struct {
	VersionExpectation
	Action         string          `json:"action"`
	TargetID       string          `json:"target_id"`
	Input          json.RawMessage `json:"input"`
	SourceIDs      []string        `json:"source_ids"`
	ExpectedEffect string          `json:"expected_effect"`
}
type ResearchSearcher interface {
	SearchResearch(context.Context, Actor, string, ResearchTask, string) ([]ResearchResult, string, error)
}

// ResearchExecutionGate reserves and settles the project's cumulative budget
// around a research run, and rechecks global policy before a stored academic
// result can be promoted to a metadata candidate. Implementations must make
// reserve/settle idempotent by runID because paused runs can resume.
type ResearchExecutionGate interface {
	ReserveResearchRun(context.Context, Actor, string, string, ResearchTask) error
	SettleResearchRun(context.Context, Actor, string, string, ResearchUsageLedger) error
	ReserveResearchModelCall(context.Context, Actor, string, string) error
	SettleResearchModelCall(context.Context, Actor, string, string, ResearchModelCallUsage) error
	ValidateAcademicResearchResult(context.Context, Actor, string, ResearchResult, AcademicSourceCatalog) error
}

// ResearchModelCallUsage settles one model call made by the controlled
// research assistant. A reservation is charged even when the provider call
// fails, because it was still an attempted billable call.
type ResearchModelCallUsage struct {
	ModelCallsUsed int    `json:"model_calls_used"`
	InputTokens    int    `json:"input_tokens"`
	OutputTokens   int    `json:"output_tokens"`
	Outcome        string `json:"outcome"`
}

// ProjectResearchBudgetLedger is the durable, project-scoped quota ledger for
// academic research. Reserve must be idempotent by runID; Settle records actual
// searches/results/model calls under the policy's configured reset period.
type ProjectResearchBudgetLedger interface {
	Reserve(context.Context, Actor, string, string, ResearchTask) error
	Settle(context.Context, Actor, string, string, ResearchUsageLedger) error
	ReserveModelCall(context.Context, Actor, string, string) error
	SettleModelCall(context.Context, Actor, string, string, ResearchModelCallUsage) error
}

type SourceReader interface {
	ReadResearchSource(context.Context, Actor, string, string) (ResearchSource, error)
	CheckAssetScope(context.Context, Actor, string, []string) ([]ProjectAssetVersion, error)
}

// ChapterSourceReader preserves chapter-function scopes when a research
// operation has an explicit chapter. Adapters that do not implement this
// stronger seam are not allowed to fall back to project-wide reads.
type ChapterSourceReader interface {
	ReadResearchSourceForChapter(context.Context, Actor, string, string, string) (ResearchSource, error)
	CheckAssetScopeForChapter(context.Context, Actor, string, string, []string) ([]ProjectAssetVersion, error)
}
type AgentModel interface {
	Reply(context.Context, Actor, ModelRequest) (ModelReply, error)
}
type ImportedAsset struct {
	AssetID         string
	AssetRevision   int
	ProcessingState string
	KnowledgeID     string
	FinalURL        string
}
type ImportProcessor interface {
	ImportResearchCandidate(context.Context, Actor, string, string, ImportCandidate) (ImportedAsset, error)
}
type ActionExecutor interface {
	ExecuteAgentAction(context.Context, Actor, ActionProposal, string) (json.RawMessage, error)
}
type AgentRuntime struct {
	Sources  SourceReader
	Research ResearchSearcher
	Model    AgentModel
	Imports  ImportProcessor
	Actions  ActionExecutor
}
type DependencyFact struct {
	Source    VersionRef `json:"source"`
	Target    VersionRef `json:"target"`
	SourceIDs []string   `json:"source_ids"`
}
type EvidenceSelection struct {
	Cards           []EvidenceCard   `json:"cards"`
	Anchors         []SourceAnchor   `json:"anchors,omitempty"`
	Sources         []AgentSourceRef `json:"sources"`
	Pending         []VersionRef     `json:"pending"`
	ContextRevision int64            `json:"context_revision"`
}

type ResearchCatalog interface {
	ResearchCatalogVersion(context.Context, Actor, string, ResearchTask) (string, error)
}

// ProjectAssetPolicy narrows existing project/KB access. It never creates a KB
// grant. Version is the native asset_revision; PolicyVersion is domain history.
const AssetPolicyKind = "project_asset_policy"

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

type ResearchRoundIdentity interface {
	ResearchRoundProvider(context.Context, Actor, string, ResearchTask) (string, error)
}
