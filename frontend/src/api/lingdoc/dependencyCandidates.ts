import { get, post } from '@/utils/request'
import type { Result } from './workspace'
import type { DependencyEdge, ObjectVersionRef } from './mainlineChanges'

// The AI-proposed dependency candidates (#252, AC-G3-15).
//
// A candidate is a proposal, not a dependency edge: it carries the object
// VERSIONS it was raised against, the excerpt that triggered it and the
// proposed rationale, so a human can check it before deciding. The client
// never decides the status: the server says whether a candidate is still
// waiting, was confirmed, corrected, refused, or needs re-evaluation because
// its basis moved on.

export type DependencyCandidateStatus = 'proposed' | 'confirmed' | 'modified' | 'rejected' | 'stale'
export type DependencyCandidateDecision = 'confirm' | 'modify' | 'reject'

export interface DependencyCandidate {
  id: string
  project_id: string
  batch_id: string
  source: ObjectVersionRef
  target: ObjectVersionRef
  relation: string
  action: string
  evidence: string
  reason: string
  confidence: number
  status: DependencyCandidateStatus
  decision?: DependencyCandidateDecision
  decision_reason?: string
  decided_by?: string
  decided_at?: string
  edge_id?: string
  superseded_relation?: string
  superseded_action?: string
  project_version: number
  context_revision: number
  model_run_id?: string
  actor_id: string
  version: number
  created_at: string
  updated_at: string
}

// A candidate that was NOT proposed again, and the human decision that
// covers it in the current project version.
export interface DependencyCandidateSuppression {
  source: ObjectVersionRef
  target: ObjectVersionRef
  relation: string
  action: string
  reason: string
  previous_candidate_id?: string
  previous_status?: string
  previous_project_version?: number
}

export interface DependencyCandidateScope {
  objects?: ObjectVersionRef[]
  target_kinds?: string[]
  reason?: string
}

export interface DependencyCandidateBatch {
  id: string
  project_id: string
  idempotency_key: string
  scope: DependencyCandidateScope
  status: string
  candidates: DependencyCandidate[]
  suppressed: DependencyCandidateSuppression[]
  project_version: number
  context_revision: number
  actor_id: string
  reason: string
  created_at: string
  updated_at: string
}

export interface DependencyCandidateInput {
  source: ObjectVersionRef
  target: ObjectVersionRef
  relation: string
  action: string
  evidence: string
  reason: string
  confidence: number
  model_run_id?: string
}

export interface DependencyCandidateBatchInput {
  expected_context_revision: number
  idempotency_key?: string
  scope: DependencyCandidateScope
  reason: string
  candidates: DependencyCandidateInput[]
}

// The decision reuses the three states of the dependency edge decision:
// confirm, modify, reject. The optional fields are omitted (not empty) to
// leave a value untouched.
export interface DependencyCandidateDecisionInput {
  expected_version: number
  decision: DependencyCandidateDecision
  reason: string
  relation?: string
  action?: string
  confidence?: number
}

export interface DependencyCandidateDecisionResult {
  candidate: DependencyCandidate
  edge?: DependencyEdge
}

const segment = (id: string) => encodeURIComponent(id)
const base = (id: string) => `/api/v1/lingdoc/projects/${segment(id)}`
const headers = (key: string) => ({ headers: { 'Idempotency-Key': key } })

export const listDependencyCandidateBatches = (id: string, batchId?: string) =>
  get<Result<DependencyCandidateBatch[]>>(`${base(id)}/dependency-candidates${batchId ? `?batch_id=${segment(batchId)}` : ''}`)
export const submitDependencyCandidateBatch = (id: string, input: DependencyCandidateBatchInput, key: string) =>
  post<Result<DependencyCandidateBatch>>(`${base(id)}/dependency-candidates`, input, headers(key))
export const decideDependencyCandidate = (id: string, candidateId: string, input: DependencyCandidateDecisionInput, key: string) =>
  post<Result<DependencyCandidateDecisionResult>>(`${base(id)}/dependency-candidates/${segment(candidateId)}/decision`, input, headers(key))
