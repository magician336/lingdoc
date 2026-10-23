// 真实试用窗口客户端。
//
// 这组端点原先寄居在 api/lingdoc/mainlineChanges.ts 里，与 G3 的变更集 /
// 影响任务共用一个模块。独立化之后，「真实试用」这块知识有自己的落点。
//
// 模块里有两类端点：
//   1. 试用窗口本身的生命周期（窗口、样本、报告、决策、签署）；
//   2. 试用证据目录——试用流程读取的既有领域端点（调研回执、生成回执与候选、
//      交付快照、交付导出）。它们不是试用专属的端点，但只有试用流程消费，
//      所以跟试用窗口放在同一处，避免调用方再去别处找「能当证据读的东西」。
//
// 后端没有发布这组路由的 OpenAPI 路径，所以这里是它们唯一的机器可校验记录：
// trialWindow.test.ts 固定了每条路径、每段编码与响应外壳。
import { get, post } from '@/utils/request'
import type { Result } from './workspace'
import type { ResearchRun } from './agentApi'
import type { GenerationRun, GenerationCandidateSummary } from './generation'
import type { Candidate } from './candidateAdoption'
import type { ReleaseSnapshot, ExportArtifact } from './delivery'

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

// The trial evidence catalog: existing domain endpoints the trial flow reads.
export const listTrialResearchRuns = (id: string) => get<Result<TrialResearchRun[]>>(`${base(id)}/research-runs`)
export const getTrialResearchRun = (id: string, runId: string) => get<Result<TrialResearchRun>>(`${base(id)}/research-runs/${segment(runId)}`)
export const listTrialGenerationCandidates = (id: string, chapterId: string) => get<Result<GenerationCandidateSummary[]>>(`${base(id)}/chapters/${segment(chapterId)}/candidates`)
export const getTrialGenerationRun = (id: string, runId: string) => get<Result<TrialGenerationRun>>(`${base(id)}/generations/${segment(runId)}`)
export const getTrialGenerationCandidate = (id: string, runId: string) => get<Result<TrialGenerationCandidate>>(`${base(id)}/generations/${segment(runId)}/candidate`)
export const listTrialReleases = (id: string) => get<Result<{ items: TrialRelease[]; truncated: boolean }>>(`${base(id)}/releases`)
export const getTrialRelease = (id: string, snapshotId: string) => get<Result<TrialRelease>>(`${base(id)}/releases/${segment(snapshotId)}`)
export const listTrialExports = (id: string) => get<Result<{ items: TrialExport[]; truncated: boolean }>>(`${base(id)}/exports`)
export const getTrialExport = (id: string, exportId: string) => get<Result<TrialExport>>(`${base(id)}/exports/${segment(exportId)}`)
