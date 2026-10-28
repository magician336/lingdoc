import { get, post, postNoBody, put } from '@/utils/request'
import type { ChangeSet, Result } from './workspace'
import type { VersionRef } from './agentApi'

// The runtime and trial endpoints used to live here as well.  They moved to
// their own modules (api/lingdoc/runtimeControl.ts and
// api/lingdoc/trialWindow.ts) when the operations panel became a surface of
// its own.
export type MainlineChangeSet = ChangeSet & { semantic_deltas?: SemanticDelta[] }
export interface ObjectVersionRef { kind: string; id: string; version: string }
// Status/certainty are server-decided: 'suggested' edges report certainty
// 'possible' and never enter the required object set, so they cannot gate a
// delivery. Clients must not re-derive either value locally.
export interface DependencyEdge { id: string; project_id: string; source: ObjectVersionRef; target: ObjectVersionRef; relation: string; action: string; origin: DependencyOrigin; reason: string; status: DependencyStatus; superseded_relation?: string; superseded_action?: string; confidence: number; actor_id: string; version: number; created_at: string; updated_at: string }
export type DependencyOrigin = 'user' | 'rule' | 'ai'
export type DependencyStatus = 'suggested' | 'confirmed' | 'rejected'
export type DependencyDecision = 'confirm' | 'reject' | 'modify'
export interface DependencyEdgeInput { expected_context_revision: number; source: ObjectVersionRef; target: ObjectVersionRef; relation: string; action: string; origin: DependencyOrigin; reason: string; confidence: number }
// 'modify' rebinds relation/action/confidence and leaves the edge confirmed;
// the optional fields are omitted (not empty) to leave a value untouched.
export interface DependencyDecisionInput { expected_version: number; decision: DependencyDecision; reason: string; relation?: string; action?: string; confidence?: number }
// A task's identity is change_set_id + target_ref + target_revision +
// action_type, and `id` is derived from that identity, so a redelivered event
// addresses the same task rather than forking a second one. Duplicate causes
// merge into `causes` instead of appending a second task.
// ImpactTaskStatus: 'STALE' is terminal but is NOT closure: the basis the task
// was raised against no longer exists, so it satisfies no gate. 'DISMISSED' is
// a responsible manager's reasoned acceptance that this impact needs no work
// this time - the impact-side counterpart of a waiver - and is therefore only
// reachable for a non-blocking impact. There is no CANCELLED: accepting and
// cancelling both meant "not doing it", and one word per decision is enough.
// `stale_reason` explains which of target_version_replaced | assignee_revoked
// applied. A revoked source is deliberately NOT here: the whole operation is
// refused instead, leaving the task unhandled rather than quietly obsolete.
export type ImpactTaskStatus = 'OPEN' | 'IN_PROGRESS' | 'WAITING_USER' | 'DONE' | 'DISMISSED' | 'STALE'
export type ImpactTaskStaleReason = 'target_version_replaced' | 'assignee_revoked'
// ImpactTaskSeverity is shared with validation-issue severity. Only 'blocking'
// prevents stabilization, and only a non-blocking impact may be accepted, so a
// client can decide the accept affordance from this value alone.
export type ImpactTaskSeverity = 'blocking' | 'warning' | 'info'
// #774 档 A：正文位置引用是任务的附件而非判定。start_offset/end_offset 是服务端
// 对章节正文口径的 UTF-16 偏移，只作参考——客户端定位时以 value 原文就近重查。
export interface ImpactLocationHint { source: 'change_old_value' | 'registered_spec_value'; field_key: string; value: string; paragraph_index: number; start_offset: number; end_offset: number; excerpt: string }
export interface ImpactTask { id: string; project_id: string; change_set_id: string; target: ObjectVersionRef; target_revision: number; action: string; severity: ImpactTaskSeverity; responsibility_domain: string; assignee_id?: string; status: ImpactTaskStatus; causes: Array<{ edge_id: string; source: ObjectVersionRef; reason: string }>; location_hints?: ImpactLocationHint[]; version: number; disposition?: string; human_decision?: string; closure_evidence: ObjectVersionRef[]; stale_reason?: ImpactTaskStaleReason; stale_at?: string; created_at: string; updated_at: string }
// An object state records what the assessment concluded about one object
// version: whether its confirmation survived, which source versions back it,
// and the baseline revision that verdict was recorded at. `objects` is the
// required set - the definite half of definite-vs-possible.
export interface ImpactObjectState { ref: ObjectVersionRef; state: string; source_ids: string[]; confirmed: boolean; context_revision: number }
// The cause's `source` is the origin the impact propagated FROM along `edge_id`;
// without it a client can name the edge but not what fed it.
export interface ImpactCause { edge_id: string; source: ObjectVersionRef; reason: string }
export interface ResolvedImpact { target: ObjectVersionRef; action: string; certainty: 'definite' | 'possible'; causes: ImpactCause[] }
// `edges` is the full edge snapshot the assessment was derived from, including
// suggested and rejected edges: a `possible` impact is a suggested edge's doing,
// so rendering certainty without the edge set hides its own evidence.
// `semantic_hash` is absent (not empty) when the run produced no semantic hash;
// comparing it as '' would report a stale mismatch for a fresh assessment.
export interface ImpactAssessment { change_set_id: string; project_id: string; context_revision: number; template_copy_version: number; ruleset_hash: string; input_hash: string; objects: ImpactObjectState[]; edges: DependencyEdge[]; impacts: ResolvedImpact[]; semantic_hash?: string; assessed_at: string }
// A 'complete' whose recorded target version was replaced becomes STALE, not
// DONE, so clients must render the reason rather than treating it as closed.
export interface StabilizationResult { change_set_id: string; context_revision: number; status: 'BLOCKED' | 'STABILIZED'; blockers: Array<{ code: string; target_id?: string; reason: string }>; stale_task_ids: string[]; checked_at: string }
export interface ImpactComputationReceipt { id: string; version: number; project_id: string; status: string; project_version: number; context_revision: number; template_copy_version: number; ruleset_hash: string; input_hash: string; computation_basis_hash: string; source_basis_hash: string; source_ids: string[]; evaluations: Array<{ status: string; rule_id?: string; ruleset_hash?: string; evaluator_version?: string }>; checked_at: string }
export interface ContextRevisionRecord { project_id: string; revision: number; change_set_id: string; spec_revision: number; template_copy_version: number; spec: Record<string, string>; created_at: string }
export interface RuleBindingOverride { rule_id: string; enabled?: boolean; target_kind?: string; target_id?: string; severity?: string; parameters?: Record<string, unknown>; remove_parameter_keys?: string[] }
export interface RouteNode { id: string; project_id: string; version: number; name: string; inputs: ObjectVersionRef[]; required: boolean; status: 'planned' | 'ready' | 'blocked' | 'retired'; context_revision: number }
export interface EvidenceCardSemanticInput { claim_ref: VersionRef; anchor_refs: VersionRef[]; support_type: 'supports' | 'refutes' | 'qualifies' | 'background'; scope: string; scope_chapter_ids: string[]; limitations: string; gaps: string; alternative_explanation: string; confirmed: boolean }
export interface SemanticDelta { kind: 'EvidenceCard' | 'ResearchClaim' | 'ProjectAssetScope' | 'RuleBinding' | 'RouteNode'; target_id: string; expected_version: number; before_hash?: string; before?: Record<string, unknown>; source_ids?: string[]; proposed_card?: EvidenceCardSemanticInput; proposed_claim?: { text: string; scope: string; status: 'active' | 'disputed' | 'superseded'; explanation?: string; confirmed: boolean; reason?: string }; proposed_asset_policy?: { asset_id?: string; scope: 'personal' | 'project' | 'team'; purpose: string; allowed_user_ids: string[]; model_use_allowed: boolean }; proposed_binding?: RuleBindingOverride; proposed_route?: Pick<RouteNode, 'name' | 'inputs' | 'required' | 'status'> }
export interface ProjectBindingView { rule_set_snapshot?: { bindings: Array<{ rule_id: string; target_kind: string; target_id: string; severity: string; enabled: boolean; parameters?: Record<string, unknown> }> }; binding_policies?: Array<{ rule_id: string; may_disable: boolean; allowed_severities?: string[]; allowed_parameter_keys?: string[] }>; source_policy_hash?: string }
export interface ChangeDraftInput { semantic_deltas?: SemanticDelta[]; expected_context_revision: number; reason: string; fields: Record<string, { old_value: string; new_value: string }>; affected_chapter_ids: string[] }
export interface ImpactTaskActionInput { expected_version: number; target_revision: number; action: 'assign' | 'start' | 'wait' | 'complete' | 'dismiss'; assignee_id?: string; human_decision?: string; reason: string; closure_evidence: ObjectVersionRef[] }

const segment = (id: string) => encodeURIComponent(id)
const base = (id: string) => `/api/v1/lingdoc/projects/${segment(id)}`
const headers = (key: string) => ({ headers: { 'Idempotency-Key': key } })
export const listMainlineChanges = (id: string) => get<Result<MainlineChangeSet[]>>(`${base(id)}/change-sets`)
export const createChangeDraft = (id: string, input: ChangeDraftInput, key: string) => post<Result<MainlineChangeSet>>(`${base(id)}/change-sets/drafts`, input, headers(key))
export const editChangeDraft = (id: string, changeId: string, input: ChangeDraftInput, key: string) => put<Result<MainlineChangeSet>>(`${base(id)}/change-sets/${segment(changeId)}/draft`, input, headers(key))
export const assessMainlineChange = (id: string, changeId: string, key: string) => post<Result<MainlineChangeSet>>(`${base(id)}/change-sets/${segment(changeId)}/assess`, {}, headers(key))
export const reassessMainlineChange = (id: string, changeId: string, key: string) => postNoBody<Result<MainlineChangeSet>>(`${base(id)}/change-sets/${segment(changeId)}/reassess`, headers(key))
export const getImpactAssessment = (id: string, changeId: string) => get<Result<ImpactAssessment>>(`${base(id)}/change-sets/${segment(changeId)}/impact-assessment`)
export const stabilizeRevision = (id: string, changeId: string, key: string) => post<Result<StabilizationResult>>(`${base(id)}/change-sets/${segment(changeId)}/stabilize`, {}, headers(key))
export const reverseMainlineChange = (id: string, changeId: string, key: string) => post<Result<MainlineChangeSet>>(`${base(id)}/change-sets/${segment(changeId)}/reverse`, {}, headers(key))
export const acknowledgeImpactPreflight = (id: string, changeId: string, proofId: string, key: string) => post<Result<{ closed_task_ids: string[] }>>(`${base(id)}/change-sets/${segment(changeId)}/preflight`, { proof_id: proofId }, headers(key))
export const listRouteNodes = (id: string) => get<Result<RouteNode[]>>(`${base(id)}/route-nodes`)
export const listContextRevisions = (id: string) => get<Result<ContextRevisionRecord[]>>(`${base(id)}/context-revisions`)
export const listDependencies = (id: string) => get<Result<DependencyEdge[]>>(`${base(id)}/dependencies`)
export const createDependency = (id: string, input: DependencyEdgeInput, key: string) => post<Result<DependencyEdge>>(`${base(id)}/dependencies`, input, headers(key))
export const decideDependency = (id: string, edgeId: string, input: DependencyDecisionInput, key: string) => post<Result<DependencyEdge>>(`${base(id)}/dependencies/${segment(edgeId)}/decision`, input, headers(key))
export const listImpactTasks = (id: string) => get<Result<ImpactTask[]>>(`${base(id)}/impact-tasks`)
export const actOnImpactTask = (id: string, taskId: string, input: ImpactTaskActionInput, key: string) => post<Result<ImpactTask>>(`${base(id)}/impact-tasks/${segment(taskId)}/actions`, input, headers(key))
export const getImpactComputationReceipt = (id: string, receiptId: string) => get<Result<ImpactComputationReceipt>>(`${base(id)}/check-receipts/${segment(receiptId)}`)
