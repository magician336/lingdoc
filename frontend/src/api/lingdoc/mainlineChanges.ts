import { get, post, postNoBody, put } from '@/utils/request'
import type { ChangeSet, Result } from './workspace'
import type { ResearchRun, G2VersionRef } from './g2'
import type { GenerationRun, GenerationCandidateSummary } from './generation'
import type { Candidate } from './candidateAdoption'
import type { ReleaseSnapshot, ExportArtifact } from './delivery'

export type MainlineChangeSet = ChangeSet & { semantic_deltas?: SemanticDelta[] }
export interface ObjectVersionRef { kind: string; id: string; version: string }
export interface DependencyEdge { id: string; project_id: string; source: ObjectVersionRef; target: ObjectVersionRef; relation: string; action: string; origin: 'user' | 'rule' | 'ai'; reason: string; status: 'suggested' | 'confirmed' | 'rejected'; confidence: number; actor_id: string; version: number }
export interface DependencyEdgeInput { expected_context_revision: number; source: ObjectVersionRef; target: ObjectVersionRef; relation: string; action: string; origin: 'user' | 'ai'; reason: string; confidence: number }
export interface ImpactTask { id: string; project_id: string; change_set_id: string; target: ObjectVersionRef; target_revision: number; action: string; severity: string; responsibility_domain: string; assignee_id?: string; status: string; causes: Array<{ edge_id: string; source: ObjectVersionRef; reason: string }>; version: number; disposition?: string; closure_evidence: ObjectVersionRef[] }
export interface ImpactAssessment { change_set_id: string; context_revision: number; input_hash: string; ruleset_hash: string; impacts: Array<{ target: ObjectVersionRef; action: string; certainty: 'definite' | 'possible'; causes: Array<{ edge_id: string; reason: string }> }> }
export interface StabilizationResult { change_set_id: string; context_revision: number; status: 'BLOCKED' | 'STABILIZED'; blockers: Array<{ code: string; target_id?: string; reason: string }> }
export interface ImpactComputationReceipt { id: string; version: number; project_id: string; status: string; project_version: number; context_revision: number; template_copy_version: number; ruleset_hash: string; input_hash: string; computation_basis_hash: string; source_basis_hash: string; source_ids: string[]; evaluations: Array<{ status: string; rule_id?: string; ruleset_hash?: string; evaluator_version?: string }>; checked_at: string }
export interface ContextRevisionRecord { project_id: string; revision: number; change_set_id: string; spec_revision: number; template_copy_version: number; spec: Record<string, string>; created_at: string }
export interface RuleBindingOverride { rule_id: string; enabled?: boolean; target_kind?: string; target_id?: string; severity?: string; parameters?: Record<string, unknown>; remove_parameter_keys?: string[] }
export interface RouteNode { id: string; project_id: string; version: number; name: string; inputs: ObjectVersionRef[]; required: boolean; status: 'planned' | 'ready' | 'blocked' | 'retired'; context_revision: number }
export interface EvidenceCardSemanticInput { claim_ref: G2VersionRef; anchor_refs: G2VersionRef[]; support_type: 'supports' | 'refutes' | 'qualifies' | 'background'; scope: string; scope_chapter_ids: string[]; limitations: string; gaps: string; alternative_explanation: string; confirmed: boolean }
export interface SemanticDelta { kind: 'EvidenceCard' | 'ResearchClaim' | 'ProjectAssetScope' | 'RuleBinding' | 'RouteNode'; target_id: string; expected_version: number; before_hash?: string; before?: Record<string, unknown>; source_ids?: string[]; proposed_card?: EvidenceCardSemanticInput; proposed_claim?: { text: string; scope: string; status: 'active' | 'disputed' | 'superseded'; explanation?: string; confirmed: boolean; reason?: string }; proposed_asset_policy?: { asset_id?: string; scope: 'personal' | 'project' | 'team'; purpose: string; allowed_user_ids: string[]; model_use_allowed: boolean }; proposed_binding?: RuleBindingOverride; proposed_route?: Pick<RouteNode, 'name' | 'inputs' | 'required' | 'status'> }
export interface ProjectBindingView { rule_set_snapshot?: { bindings: Array<{ rule_id: string; target_kind: string; target_id: string; severity: string; enabled: boolean; parameters?: Record<string, unknown> }> }; binding_policies?: Array<{ rule_id: string; may_disable: boolean; allowed_severities?: string[]; allowed_parameter_keys?: string[] }>; source_policy_hash?: string }
export interface ChangeDraftInput { semantic_deltas?: SemanticDelta[]; expected_context_revision: number; reason: string; fields: Record<string, { old_value: string; new_value: string }>; affected_chapter_ids: string[] }
export interface ImpactTaskActionInput { expected_version: number; target_revision: number; action: 'assign' | 'start' | 'wait' | 'complete' | 'dismiss' | 'cancel'; assignee_id?: string; human_decision?: string; reason: string; closure_evidence: ObjectVersionRef[] }
export interface RuntimeScope { kind: 'tenant' | 'project' | 'template_version' | 'ruleset_hash' | 'runtime_mode'; value: string }
export interface RuntimeControl { id: string; project_id: string; scope: RuntimeScope; version: number; status: 'PAUSED' | 'ACTIVE'; actor_id: string; reason: string; recovery_verified: boolean; snapshot_id?: string; export_id?: string; updated_at: string }
export interface RuntimeControlInput { expected_version: number; scope: RuntimeScope; action: 'pause' | 'resume'; reason: string; snapshot_id?: string; export_id?: string }
export interface RuntimeEvent { id: string; origin: 'runtime' | 'reported'; tenant_alias: string; project_alias: string; correlation_id: string; causation_id?: string; request_id?: string; task_id?: string; changeset_id?: string; impact_task_id?: string; snapshot_id?: string; export_id?: string; template_version?: string; ruleset_hash?: string; context_revision: number; target_revision: number; runtime_mode: string; resource_category: string; status: string; error_code?: string; duration_ms: number; created_at: string }
export interface RuntimeMetrics { event_count: number; counts: Record<string, number>; p95_ms: number | null; p99_ms: number | null; queue_depth: number; missing: string[] }
export interface RuntimeAlert { id: string; rule_id: string; event_id: string; severity: string; action: string; responsible_id: string; status: string }
export interface RuntimeAlertRule { signal: string; severity: 'P0' | 'P1' | 'P2' | 'P3'; action: 'notify' | 'retry' | 'pause' | 'block_export' | 'rollback' | 'human_confirm'; responsible_id: string; scope?: RuntimeScope }
export interface TrialWindow { id: string; project_id: string; version: number; status: 'DRAFT' | 'LOCKED' | 'PAUSED' | 'CLOSED'; starts_at: string; ends_at: string; included_project_ids: string[]; included_user_ids: string[]; decision_owners: Record<'product' | 'qa' | 'safety', string>; target_samples: number; minimum_samples: number; template_version: string; ruleset_hash: string; runtime_modes: ['real']; human_support: string; redaction: string; exit_conditions: string[]; created_by: string; created_at: string }
export type TrialWindowInput = Omit<TrialWindow, 'id' | 'project_id' | 'version' | 'status' | 'created_by' | 'created_at'>
export interface MetricObservation { name: string; numerator: number | null; denominator: number | null; missing: boolean; version: string }
export interface TrialSample { id: string; window_id: string; sample_version: string; fixture_digest: string; project_id: string; template_version: string; ruleset_hash: string; context_revision: number; runtime_mode: 'mock' | 'real_api_fake_model' | 'real'; dependency_versions: Record<string, string>; permission_snapshot: string; input_hash: string; output_hash: string; executed_at: string; executor_id: string; reviewer_id?: string; correlation_id: string; runtime_event_ids: string[]; process_evidence: Record<string, string>; quality_result: 'NOT RUN' | 'PASS' | 'FAIL' | 'BLOCKED'; severity: '' | 'P0' | 'P1' | 'P2' | 'P3'; human_decision?: string; word_roundtrip_evidence?: string; metrics: MetricObservation[]; version: number }
export type TrialSampleInput = Omit<TrialSample, 'id' | 'window_id' | 'executor_id' | 'reviewer_id' | 'quality_result' | 'human_decision' | 'word_roundtrip_evidence' | 'version'>
export interface TrialReviewInput { expected_version: number; quality_result: TrialSample['quality_result']; human_decision: 'accept' | 'reject' | 'needs_work'; reason: string; word_roundtrip_evidence: string; repair_owner_id?: string; repair_version?: string; rerun_evidence?: string }
export interface TrialReport { window_id: string; window_version: number; readiness: 'NOT READY' | 'BLOCKED' | 'READY'; submitted: number; eligible: number; excluded: Record<string, string[]>; metrics: Array<{ name: string; numerator: number; denominator: number; value: number | null; missing_samples: string[] }>; blockers: string[]; input_hash: string }
export interface TrialDecision { id: string; window_id: string; report: TrialReport; decision: 'expand' | 'continue' | 'pause'; reason: string; signatures: Partial<Record<'product' | 'qa' | 'safety', string>>; status: 'WAITING_HUMAN' | 'DECIDED'; version: number; decided_at?: string }

// These are read projections of existing domain endpoints. Missing runtime,
// permission or hash fields remain missing; no trial result is inferred.
export type TrialResearchRun = ResearchRun
export type TrialGenerationRun = GenerationRun
export type TrialGenerationCandidate = Candidate & { created_at?: string; basis: Candidate['basis'] & { context_revision?: number; template_copy_version?: number } }
export type TrialRelease = ReleaseSnapshot & { frozen_input: ReleaseSnapshot['frozen_input'] & { template?: { version: string; ruleset_hash: string }; template_copy_ruleset_hash?: string; template_copy_version?: number; asset_versions?: Array<{ asset_id: string; asset_revision: number }> } }
export type TrialExport = ExportArtifact & { created_at?: string }
export interface TrialEvidenceOption { kind: 'research_run' | 'generation_run' | 'snapshot' | 'export'; id: string; label: string; chapter_id?: string }

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
export const decideDependency = (id: string, edgeId: string, input: { expected_version: number; decision: 'confirm' | 'reject'; reason: string }, key: string) => post<Result<DependencyEdge>>(`${base(id)}/dependencies/${segment(edgeId)}/decision`, input, headers(key))
export const listImpactTasks = (id: string) => get<Result<ImpactTask[]>>(`${base(id)}/impact-tasks`)
export const actOnImpactTask = (id: string, taskId: string, input: ImpactTaskActionInput, key: string) => post<Result<ImpactTask>>(`${base(id)}/impact-tasks/${segment(taskId)}/actions`, input, headers(key))
export const listRuntimeControls = (id: string) => get<Result<RuntimeControl[]>>(`${base(id)}/runtime/controls`)
export const controlRuntime = (id: string, input: RuntimeControlInput, key: string) => post<Result<RuntimeControl>>(`${base(id)}/runtime/controls`, input, headers(key))
export const listRuntimeEvents = (id: string, correlation = '') => get<Result<RuntimeEvent[]>>(`${base(id)}/runtime/events${correlation ? `?correlation_id=${segment(correlation)}` : ''}`)
export const getRuntimeMetrics = (id: string) => get<Result<RuntimeMetrics>>(`${base(id)}/runtime/metrics`)
export const listRuntimeAlerts = (id: string) => get<Result<RuntimeAlert[]>>(`${base(id)}/runtime/alerts`)
export const saveRuntimeAlertRule = (id: string, input: RuntimeAlertRule, key: string) => post<Result<RuntimeAlertRule>>(`${base(id)}/runtime/alert-rules`, input, headers(key))
export const listTrialWindows = (id: string) => get<Result<TrialWindow[]>>(`${base(id)}/trial-windows`)
export const createTrialWindow = (id: string, input: TrialWindowInput, key: string) => post<Result<TrialWindow>>(`${base(id)}/trial-windows`, input, headers(key))
export const actOnTrialWindow = (id: string, windowId: string, input: { expected_version: number; action: 'lock' | 'pause' | 'close'; reason: string }, key: string) => post<Result<TrialWindow>>(`${base(id)}/trial-windows/${segment(windowId)}/actions`, input, headers(key))
export const listTrialSamples = (id: string, windowId: string) => get<Result<TrialSample[]>>(`${base(id)}/trial-windows/${segment(windowId)}/samples`)
export const recordTrialSample = (id: string, windowId: string, input: TrialSampleInput, key: string) => post<Result<TrialSample>>(`${base(id)}/trial-windows/${segment(windowId)}/samples`, input, headers(key))
export const reviewTrialSample = (id: string, windowId: string, sampleId: string, input: TrialReviewInput, key: string) => post<Result<TrialSample>>(`${base(id)}/trial-windows/${segment(windowId)}/samples/${segment(sampleId)}/review`, input, headers(key))
export const getTrialReport = (id: string, windowId: string) => get<Result<TrialReport>>(`${base(id)}/trial-windows/${segment(windowId)}/report`)
export const listTrialDecisions = (id: string, windowId: string) => get<Result<TrialDecision[]>>(`${base(id)}/trial-windows/${segment(windowId)}/decisions`)
export const createTrialDecision = (id: string, windowId: string, input: { decision: TrialDecision['decision']; reason: string; report_hash: string }, key: string) => post<Result<TrialDecision>>(`${base(id)}/trial-windows/${segment(windowId)}/decisions`, input, headers(key))
export const signTrialDecision = (id: string, decisionId: string, input: { expected_version: number; responsibility: 'product' | 'qa' | 'safety'; reason: string }, key: string) => post<Result<TrialDecision>>(`${base(id)}/trial-decisions/${segment(decisionId)}/sign`, input, headers(key))
export const getImpactComputationReceipt = (id: string, receiptId: string) => get<Result<ImpactComputationReceipt>>(`${base(id)}/check-receipts/${segment(receiptId)}`)
export const listTrialResearchRuns = (id: string) => get<Result<TrialResearchRun[]>>(`${base(id)}/research-runs`)
export const getTrialResearchRun = (id: string, runId: string) => get<Result<TrialResearchRun>>(`${base(id)}/research-runs/${segment(runId)}`)
export const listTrialGenerationCandidates = (id: string, chapterId: string) => get<Result<GenerationCandidateSummary[]>>(`${base(id)}/chapters/${segment(chapterId)}/candidates`)
export const getTrialGenerationRun = (id: string, runId: string) => get<Result<TrialGenerationRun>>(`${base(id)}/generations/${segment(runId)}`)
export const getTrialGenerationCandidate = (id: string, runId: string) => get<Result<TrialGenerationCandidate>>(`${base(id)}/generations/${segment(runId)}/candidate`)
export const listTrialReleases = (id: string) => get<Result<{ items: TrialRelease[]; truncated: boolean }>>(`${base(id)}/releases`)
export const getTrialRelease = (id: string, snapshotId: string) => get<Result<TrialRelease>>(`${base(id)}/releases/${segment(snapshotId)}`)
export const listTrialExports = (id: string) => get<Result<{ items: TrialExport[]; truncated: boolean }>>(`${base(id)}/exports`)
export const getTrialExport = (id: string, exportId: string) => get<Result<TrialExport>>(`${base(id)}/exports/${segment(exportId)}`)
