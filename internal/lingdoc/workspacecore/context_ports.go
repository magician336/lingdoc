package workspacecore

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
)

// 这组窄口是「一个上下文一个窄口」的落点（Spec #47 决策 34）。
//
// 聚合体 *Service 有两百多个方法，任何一个传输面拿到它就能调用其中任意一个。
// 下面这些口把同一批方法按上下文重新分桶：每个口只含一门语言的调用者，
// 传输面因此不再持有聚合体具体类型（决策 35）。
//
// 契约：每个方法的方法签名与语义，逐字取自迁移前聚合体上的同名方法。
// 收窄只发生在「哪些方法被分到哪个口」，不发生在方法本身。
// 这一点由 internal/lingdoc/workspacecore/context_ports_test.go 的
// 结构断言守住。

// compile-time proof that the aggregate still satisfies every narrow port.
// 聚合体仍实现这些口，是本段重构期间的行为基线：换的是消费方的持有形态，
// 不是聚合体的能力集合。

// GovernanceReviewApplication 是规则治理运行时在一次治理事务里要做的两件事：授权一个能力、把校验问题的处置读回来。
// 消费方：规则治理运行时。
type GovernanceReviewApplication interface {
	AuthorizeInTransaction(ctx context.Context, tx Transaction, actor Actor, projectID, capability string) (Project, error)
	ValidationIssueDispositionsInTransaction(ctx context.Context, tx Transaction, actor Actor, projectID string, findings []ValidationIssueBinding) (map[string]ValidationIssueDisposition, error)
}

// AgentContextApplication 是智能体上下文：组装给智能体看的上下文包，以及读回项目的资料策略。
// 消费方：规则治理运行时、G2 路由。
type AgentContextApplication interface {
	BuildAgentContext(ctx context.Context, actor Actor, projectID string, input AgentContextInput) (AgentContextPackage, error)
	GetProjectAssetPolicy(ctx context.Context, actor Actor, projectID, assetID string) (ProjectAssetPolicy, error)
}

// ChangeSetDraftApplication 是变更集草稿：起草、评估、逆转与稳定化一条变更集草稿。
// 消费方：影响中心路由、G2 路由、智能体上下文。
type ChangeSetDraftApplication interface {
	CreateChangeDraft(ctx context.Context, actor Actor, projectID, key string, input ExtendedChangeSetInput) (json.RawMessage, int, bool, error)
	EditChangeDraft(ctx context.Context, actor Actor, projectID, changeID, key string, input ExtendedChangeSetInput) (json.RawMessage, int, bool, error)
	AssessChangeSet(ctx context.Context, actor Actor, projectID, changeID, key string) (json.RawMessage, int, bool, error)
	ReverseChangeSet(ctx context.Context, actor Actor, projectID, changeID, key string) (json.RawMessage, int, bool, error)
	StabilizeRevision(ctx context.Context, actor Actor, projectID, changeID, key string) (json.RawMessage, int, bool, error)
}

// ImpactApplication 是影响中心：一次影响评估的对象集合、预检回执、上下文修订、路线节点与影响任务。
// 消费方：影响中心路由、全流程守卫。
type ImpactApplication interface {
	GetImpactAssessment(ctx context.Context, actor Actor, projectID, changeID string) (ImpactAssessment, error)
	AcknowledgeImpactPreflight(ctx context.Context, actor Actor, projectID, changeID, snapshotID, key string) (json.RawMessage, int, bool, error)
	ReadImpactComputationReceipt(ctx context.Context, actor Actor, projectID, id string) (ImpactComputationReceipt, error)
	ListContextRevisions(ctx context.Context, actor Actor, projectID string) ([]ContextRevisionRecord, error)
	ListRouteNodes(ctx context.Context, actor Actor, projectID string) ([]RouteNode, error)
	ListImpactTasks(ctx context.Context, actor Actor, projectID string) ([]ImpactTask, error)
	ActOnImpactTask(ctx context.Context, actor Actor, projectID, taskID, key string, input ImpactTaskActionInput) (json.RawMessage, int, bool, error)
}

// DependencyApplication 是依赖：项目内的依赖边与 AI 提出的依赖候选。
// 消费方：影响中心路由。
type DependencyApplication interface {
	ListDependencyEdges(ctx context.Context, actor Actor, projectID string) ([]DependencyEdge, error)
	CreateDependencyEdge(ctx context.Context, actor Actor, projectID, key string, input DependencyEdgeInput) (json.RawMessage, int, bool, error)
	DecideDependencyEdge(ctx context.Context, actor Actor, projectID, edgeID, key string, input DependencyDecisionInput) (json.RawMessage, int, bool, error)
	ListDependencyCandidates(ctx context.Context, actor Actor, projectID, batchID string) ([]DependencyCandidateBatch, error)
	DecideDependencyCandidate(ctx context.Context, actor Actor, projectID, candidateID, key string, input DependencyDecisionInput) (json.RawMessage, int, bool, error)
	SubmitDependencyCandidateBatch(ctx context.Context, actor Actor, projectID, key string, input DependencyCandidateBatchInput) (json.RawMessage, int, bool, error)
}

// RuntimeApplication 是运行时：控制项、事件流、指标与告警规则。
// 消费方：影响中心路由。
type RuntimeApplication interface {
	ListRuntimeControls(ctx context.Context, actor Actor, projectID string) ([]RuntimeControl, error)
	ControlRuntime(ctx context.Context, actor Actor, projectID, key string, input RuntimeControlInput) (json.RawMessage, int, bool, error)
	ListRuntimeEvents(ctx context.Context, actor Actor, projectID, correlationID string) ([]RuntimeEvent, error)
	GetRuntimeMetrics(ctx context.Context, actor Actor, projectID string) (RuntimeMetrics, error)
	ListRuntimeAlerts(ctx context.Context, actor Actor, projectID string) ([]RuntimeAlert, error)
	SaveRuntimeAlertRule(ctx context.Context, actor Actor, projectID, key string, input RuntimeAlertRule) (json.RawMessage, int, bool, error)
}

// TrialApplication 是试验：试验窗口、样本、试验报告与试验决策。
// 消费方：影响中心路由。
type TrialApplication interface {
	ListTrialWindows(ctx context.Context, actor Actor, projectID string) ([]TrialWindow, error)
	CreateTrialWindow(ctx context.Context, actor Actor, projectID, key string, input TrialWindow) (json.RawMessage, int, bool, error)
	ActOnTrialWindow(ctx context.Context, actor Actor, projectID, windowID, key string, input TrialWindowActionInput) (json.RawMessage, int, bool, error)
	ListTrialSamples(ctx context.Context, actor Actor, projectID, windowID string) ([]TrialSample, error)
	RecordTrialSample(ctx context.Context, actor Actor, projectID, windowID, key string, input TrialSample) (json.RawMessage, int, bool, error)
	ReviewTrialSample(ctx context.Context, actor Actor, projectID, windowID, sampleID, key string, input TrialReviewInput) (json.RawMessage, int, bool, error)
	GetTrialReport(ctx context.Context, actor Actor, projectID, windowID string) (TrialReport, error)
	ListTrialDecisions(ctx context.Context, actor Actor, projectID, windowID string) ([]TrialDecisionRecord, error)
	CreateTrialDecision(ctx context.Context, actor Actor, projectID, windowID, key string, input TrialDecisionInput) (json.RawMessage, int, bool, error)
	SignTrialDecision(ctx context.Context, actor Actor, projectID, decisionID, key string, input TrialDecisionSignInput) (json.RawMessage, int, bool, error)
}

// RoadmapApplication 是路线图：路线图本体、边与节点版本、编辑会话与编辑提案。
// 消费方：影响中心路由。
type RoadmapApplication interface {
	ReadRoadmap(ctx context.Context, actor Actor, projectID string) (RoadmapBaseline, error)
	ReadRouteEdges(ctx context.Context, actor Actor, projectID string) ([]RouteEdge, error)
	ReadRouteEdgeVersions(ctx context.Context, actor Actor, projectID, edgeID string) ([]RouteEdge, error)
	ReadRoadmapNodeVersions(ctx context.Context, actor Actor, projectID, nodeID string) ([]RoadmapVersionEntry, error)
	ListRoadmapSessions(ctx context.Context, actor Actor, projectID string) ([]RoadmapEditSession, error)
	GetRoadmapSession(ctx context.Context, actor Actor, projectID, sessionID string) (RoadmapEditSession, error)
	BeginRoadmapEdit(ctx context.Context, actor Actor, projectID, key string, input RoadmapEditBeginInput) (json.RawMessage, int, bool, error)
	CommitRoadmapEdit(ctx context.Context, actor Actor, projectID, sessionID, key string, input RoadmapCommitInput) (json.RawMessage, int, bool, error)
	CancelRoadmapEdit(ctx context.Context, actor Actor, projectID, sessionID, key string, input RoadmapCancelInput) (json.RawMessage, int, bool, error)
	ProposeRoadmapNode(ctx context.Context, actor Actor, projectID, sessionID, key string, input RoadmapNodeProposalInput) (json.RawMessage, int, bool, error)
	ProposeRoadmapEdge(ctx context.Context, actor Actor, projectID, sessionID, key string, input RoadmapEdgeProposalInput) (json.RawMessage, int, bool, error)
	CompareRoadmapNodeVersions(ctx context.Context, actor Actor, projectID, nodeID string, from, to int64) (RoadmapComparison, error)
}

// ResearchTaskApplication 是调研任务：立项、审批、控制、运行与结果查看。
// 消费方：G2 路由。
type ResearchTaskApplication interface {
	SaveResearchTask(ctx context.Context, actor Actor, projectID, key string, input SaveResearchTaskInput) (json.RawMessage, int, bool, error)
	ListResearchTasks(ctx context.Context, actor Actor, projectID string) ([]ResearchTask, error)
	ApproveResearchTask(ctx context.Context, actor Actor, projectID, taskID, key string, input ConfirmationInput) (json.RawMessage, int, bool, error)
	ControlResearchTask(ctx context.Context, actor Actor, projectID, taskID, key string, input ControlResearchTaskInput) (json.RawMessage, int, bool, error)
	RunResearchTask(ctx context.Context, actor Actor, projectID, taskID, key string, input RunResearchTaskInput) (json.RawMessage, int, bool, error)
	GetResearchRun(ctx context.Context, actor Actor, projectID, runID string) (ResearchRun, error)
	InspectResearchResult(ctx context.Context, actor Actor, projectID, runID, resultID string) (ResearchResult, error)
	ListResearchRuns(ctx context.Context, actor Actor, projectID string) ([]ResearchRun, error)
}

// ImportCandidateApplication 是导入候选：建候选、确认、驳回、刷新与列出。
// 消费方：G2 路由。
type ImportCandidateApplication interface {
	CreateImportCandidate(ctx context.Context, actor Actor, projectID, key string, input CreateImportCandidateInput) (json.RawMessage, int, bool, error)
	ConfirmImportCandidate(ctx context.Context, actor Actor, projectID, candidateID, key string, input ConfirmationInput) (json.RawMessage, int, bool, error)
	RejectImportCandidate(ctx context.Context, actor Actor, projectID, candidateID, key string, input ConfirmationInput) (json.RawMessage, int, bool, error)
	RefreshImportCandidate(ctx context.Context, actor Actor, projectID, candidateID string) (ImportCandidate, error)
	ListImportCandidates(ctx context.Context, actor Actor, projectID string) ([]ImportCandidate, error)
}

// EvidenceCardApplication 是证据卡：建卡、确认、复核与列出。
// 消费方：G2 路由、智能体上下文。
type EvidenceCardApplication interface {
	SaveEvidenceCard(ctx context.Context, actor Actor, projectID, key string, input SaveEvidenceCardInput) (json.RawMessage, int, bool, error)
	ConfirmEvidenceCard(ctx context.Context, actor Actor, projectID, cardID, key string, input ConfirmationInput) (json.RawMessage, int, bool, error)
	ReviewEvidenceCard(ctx context.Context, actor Actor, projectID, cardID, key string, input ReviewEvidenceCardInput) (json.RawMessage, int, bool, error)
	ListEvidenceCards(ctx context.Context, actor Actor, projectID string) ([]EvidenceCard, error)
}

// ResearchClaimApplication 是研究声明：写一条声明与读回声明列表。
// 消费方：G2 路由。
type ResearchClaimApplication interface {
	SaveResearchClaim(ctx context.Context, actor Actor, projectID, key string, input SaveResearchClaimInput) (json.RawMessage, int, bool, error)
	ListResearchClaims(ctx context.Context, actor Actor, projectID string) ([]ResearchClaim, error)
}

// SourceAnchorApplication 是来源锚点：把结论锚定到来源的某个位置，并读回锚点列表。
// 消费方：G2 路由。
type SourceAnchorApplication interface {
	SaveSourceAnchor(ctx context.Context, actor Actor, projectID, key string, input SaveSourceAnchorInput) (json.RawMessage, int, bool, error)
	ListSourceAnchors(ctx context.Context, actor Actor, projectID string) ([]SourceAnchor, error)
}

// AgentSessionApplication 是智能体会话：开会话、读会话、读轮次、发一轮。
// 消费方：G2 路由。
type AgentSessionApplication interface {
	CreateAgentSession(ctx context.Context, actor Actor, projectID, agent, key string, input VersionExpectation) (json.RawMessage, int, bool, error)
	ListAgentSessions(ctx context.Context, actor Actor, projectID, agent string) ([]AgentSession, error)
	GetAgentTurns(ctx context.Context, actor Actor, projectID, sessionID string) ([]AgentTurn, error)
	SendAgentTurn(ctx context.Context, actor Actor, projectID, sessionID, key string, input AgentTurnInput) (json.RawMessage, int, bool, error)
}

// ActionProposalApplication 是行动提案：提出一条提案、确认、驳回与列出。
// 消费方：G2 路由。
type ActionProposalApplication interface {
	CreateActionProposal(ctx context.Context, actor Actor, projectID, key string, input CreateActionProposalInput) (json.RawMessage, int, bool, error)
	ConfirmActionProposal(ctx context.Context, actor Actor, projectID, proposalID, key string, input ConfirmationInput) (json.RawMessage, int, bool, error)
	RejectActionProposal(ctx context.Context, actor Actor, projectID, proposalID, key string, input ConfirmationInput) (json.RawMessage, int, bool, error)
	ListActionProposals(ctx context.Context, actor Actor, projectID string) ([]ActionProposal, error)
}

// ChapterReviewApplication 是章节复核与评论：读复核状态、改复核状态、读评论、写评论。
// 消费方：全流程路由。
type ChapterReviewApplication interface {
	GetChapterReview(ctx context.Context, actor Actor, pid, cid string) (ChapterReviewState, error)
	ChangeChapterReview(ctx context.Context, actor Actor, pid, cid, key string, input ChapterReviewInput) (json.RawMessage, int, bool, error)
	ListChapterComments(ctx context.Context, actor Actor, pid, cid, versionID string) ([]ChapterComment, error)
	SaveChapterComment(ctx context.Context, actor Actor, pid, cid, key string, input ChapterCommentInput) (json.RawMessage, int, bool, error)
}

// DeliveryGuardApplication 是交付守卫：授权一次交付导出，并准备与复核导出的原生依据。
// 消费方：全流程守卫。
type DeliveryGuardApplication interface {
	AuthorizeDeliveryExport(ctx context.Context, actor Actor, projectID string) error
	PrepareDeliveryExportNativeBasis(ctx context.Context, actor Actor, projectID string, expectedProjectVersion int64, refs []DeliveryExportSourceRef) (context.Context, error)
	ValidateDeliveryExportNativeBasisInTransaction(ctx context.Context, tx Transaction, actor Actor, projectID string, refs []DeliveryExportSourceRef) error
}

// SourceProvenanceApplication 是来源溯源：准备项目溯源与冻结项目溯源，并在事务内复核冻结溯源。
// 消费方：全流程守卫。
type SourceProvenanceApplication interface {
	PrepareProjectProvenance(ctx context.Context, actor Actor, pid string, forModel bool) (context.Context, ProjectProvenanceSnapshot, error)
	PrepareFrozenProjectProvenance(ctx context.Context, actor Actor, pid string, spec map[string]string, snapshot ProjectProvenanceSnapshot, forModel bool) (context.Context, error)
	ValidateFrozenProjectProvenanceInTransaction(ctx context.Context, tx Transaction, actor Actor, pid string, spec map[string]string, snapshot ProjectProvenanceSnapshot, forModel bool) error
}

// ImpactGuardApplication 是影响守卫：断言一次修订已稳定化、读写影响计算回执、读预检依据。
// 消费方：全流程守卫。
type ImpactGuardApplication interface {
	AssertRevisionStabilized(ctx context.Context, actor Actor, projectID string) error
	ReadImpactComputationReceipt(ctx context.Context, actor Actor, projectID, id string) (ImpactComputationReceipt, error)
	StoreImpactComputationReceipt(ctx context.Context, actor Actor, basis PreflightBasis, inputHash, status string, evaluations []template.Evaluation, sourceIDs []string) (ImpactComputationReceipt, error)
	ReadPreflightBasis(ctx context.Context, actor Actor, projectID string, sourceIDs []string) (PreflightBasis, error)
}

// RuntimeGuardApplication 是运行时守卫：检查一次运行时操作，并观察运行时与交付运行时。
// 消费方：全流程守卫。
type RuntimeGuardApplication interface {
	CheckRuntimeOperation(ctx context.Context, actor Actor, projectID, operation, mode string) error
	ObserveRuntime(ctx context.Context, actor Actor, projectID, operation, status string, durationMS int64) error
	ObserveDeliveryRuntime(ctx context.Context, actor Actor, projectID, operation, status string, durationMS int64, objects DeliveryRuntimeObjects) error
}

// AgentActionApplication 是智能体行动执行器可以执行的那几种操作。
//
// 行动提案的种类（save_spec / create_evidence_card / create_change_set /
// apply_change_set / commit_working_copy …）由 core.ActionProposal 声明，
// 所以这套语言属于 workspacecore，口也在这里声明。
//
// 这里的七个方法都是**原始操作**形态（返回 json.RawMessage 与重放位），
// 不是 Result 形态的封装；已有的上下文口给的是封装形态，两者不可互换。
type AgentActionApplication interface {
	SaveSpec(ctx context.Context, actor Actor, projectID, key string, input SaveSpecInput) (json.RawMessage, int, bool, error)
	SaveEvidenceCard(ctx context.Context, actor Actor, projectID, key string, input SaveEvidenceCardInput) (json.RawMessage, int, bool, error)
	ConfirmEvidenceCard(ctx context.Context, actor Actor, projectID, cardID, key string, input ConfirmationInput) (json.RawMessage, int, bool, error)
	CreateChangeDraft(ctx context.Context, actor Actor, projectID, key string, input ExtendedChangeSetInput) (json.RawMessage, int, bool, error)
	PrepareAgentChangeDraft(ctx context.Context, actor Actor, proposal ActionProposal, input AgentChangeDraftInput) (context.Context, error)
	ApplyChangeSet(ctx context.Context, actor Actor, projectID, changeSetID, key string) (json.RawMessage, int, bool, error)
	CommitWorkingCopy(ctx context.Context, actor Actor, projectID, chapterID, key string, input CommitWorkingCopyInput) (json.RawMessage, int, bool, error)
}

// RuntimeCompositionApplication 是把守卫装进运行时的那三个装配操作。
//
// 全流程守卫今天就是自己往运行时里装的：预检复核器、恢复复核器、行动执行器。
// 守卫要不再持有聚合体具体类型，这三个装配操作就得有一个窄口承载——
// 装配关系本身也是一种能力，它属于运行时组合这一门语言。
type RuntimeCompositionApplication interface {
	SetActionExecutor(executor ActionExecutor)
	SetImpactPreflightChecker(checker ImpactPreflightChecker)
	SetRuntimeRecoveryChecker(checker RuntimeRecoveryChecker)
}

// ProjectProvenanceApplication 是项目溯源的那三项能力：准备溯源、复核溯源快照、
// 断言冻结项目的溯源。
// 消费方：交付输入构建器。
type ProjectProvenanceApplication interface {
	PrepareProjectProvenance(ctx context.Context, actor Actor, pid string, forModel bool) (context.Context, ProjectProvenanceSnapshot, error)
	ValidateProjectProvenanceSnapshot(ctx context.Context, actor Actor, pid string, spec map[string]string, snapshot ProjectProvenanceSnapshot, forModel bool) error
	AssertFrozenProjectProvenance(ctx context.Context, actor Actor, pid string, spec map[string]string, snapshot ProjectProvenanceSnapshot, forModel bool) error
}

// EvidenceSelectionApplication 是「选一条生成证据」这一项能力。
// 消费方：生成输入解析、生成证据校验器、选区改写。
type EvidenceSelectionApplication interface {
	SelectGenerationEvidence(ctx context.Context, actor Actor, projectID, chapterID string, cardRefs []VersionRef, formal bool) (EvidenceSelection, error)
}

// EvidenceSelectionTransactionApplication 是同一个选择在事务里的形态。
// 消费方：候选采纳的来源策略。
type EvidenceSelectionTransactionApplication interface {
	SelectGenerationEvidenceInTransaction(ctx context.Context, tx Transaction, actor Actor, projectID, chapterID string, refs []VersionRef, formal bool, usedSourceIDs []string) (EvidenceSelection, error)
}

// EvidenceProofPreparationApplication 是「为候选准备生成证据证明」这一项能力。
// 消费方：候选采纳的来源策略。
type EvidenceProofPreparationApplication interface {
	PrepareCandidateGenerationEvidence(ctx context.Context, actor Actor, projectID, chapterID string, refs []VersionRef, usedSourceIDs []string) (context.Context, EvidenceSelection, error)
}

var (
	_ RuntimeCompositionApplication = (*Service)(nil)
	_ AgentActionApplication        = (*Service)(nil)
	_ GovernanceReviewApplication   = (*Service)(nil)

	_ AgentContextApplication = (*Service)(nil)

	_ ChangeSetDraftApplication = (*Service)(nil)

	_ ImpactApplication = (*Service)(nil)

	_ DependencyApplication = (*Service)(nil)

	_ RuntimeApplication = (*Service)(nil)

	_ TrialApplication = (*Service)(nil)

	_ RoadmapApplication = (*Service)(nil)

	_ ResearchTaskApplication = (*Service)(nil)

	_ ImportCandidateApplication = (*Service)(nil)

	_ EvidenceCardApplication = (*Service)(nil)

	_ ResearchClaimApplication = (*Service)(nil)

	_ SourceAnchorApplication = (*Service)(nil)

	_ AgentSessionApplication = (*Service)(nil)

	_ ActionProposalApplication = (*Service)(nil)

	_ ChapterReviewApplication = (*Service)(nil)

	_ DeliveryGuardApplication = (*Service)(nil)

	_ SourceProvenanceApplication = (*Service)(nil)

	_ ImpactGuardApplication = (*Service)(nil)

	_ RuntimeGuardApplication = (*Service)(nil)

	_ ProjectProvenanceApplication = (*Service)(nil)

	_ EvidenceSelectionApplication = (*Service)(nil)

	_ EvidenceSelectionTransactionApplication = (*Service)(nil)

	_ EvidenceProofPreparationApplication = (*Service)(nil)
)
