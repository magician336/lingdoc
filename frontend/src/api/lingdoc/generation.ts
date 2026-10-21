import { get, post } from '@/utils/request'
import type { Candidate } from './candidateAdoption'

/**
 * 一次 run 的输入摘要。
 *
 * 它是摘要不是输入：失败的 run 要能读回「这次是按什么发起的」，但服务端
 * 不把说明原文吐出来——这里只有长度与摘要。
 */
export interface GenerationRunInputSummary {
  chapter_id: string
  asset_ids: string[]
  evidence_card_ids: string[]
  spec_revision: number
  chapter_version_id: string | null
  instruction_runes: number
  instruction_hash: string
}

export interface GenerationRun {
  id: string
  project_id: string
  chapter_id: string
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'interrupted'
  candidate_id: string | null
  error?: { code: string; message: string; retryable: boolean }
  /** 输入摘要。读不到输入时字段不出现。 */
  input_summary?: GenerationRunInputSummary
}

export interface GenerationRequest {
  evidence_card_refs?: Array<{ id: string; version: number }>
  chapter_id: string
  asset_ids: string[]
  instruction: string
  expected_spec_revision: number
  expected_chapter_version_id: string | null
}

export interface GenerationCandidateSummary {
  id: string
  run_id: string
  /**
   * 候选的有效性。
   *
   * `not_authorized` 是候选级的状态：授权没了。它与 `stale`（输入变了）
   * 不同，两者的下一步也不同——恢复授权再重跑，还是直接重跑。
   */
  validity: 'fresh' | 'stale' | 'not_authorized'
  created_at: string
}

interface Envelope<T> {
  data: T
  request_id: string
  meta: { replayed: boolean; refresh_required: boolean }
}

const base = '/api/v1/lingdoc/projects'
const segment = (id: string) => encodeURIComponent(id)

export const startGeneration = (projectId: string, input: GenerationRequest, key: string) =>
  post<Envelope<GenerationRun>>(`${base}/${segment(projectId)}/generations`, input,
    { headers: { 'Idempotency-Key': key } })

export const getGeneration = (projectId: string, runId: string) =>
  get<Envelope<GenerationRun>>(`${base}/${segment(projectId)}/generations/${segment(runId)}`)

export const getGeneratedCandidate = (projectId: string, runId: string) =>
  get<Envelope<Candidate>>(`${base}/${segment(projectId)}/generations/${segment(runId)}/candidate`)

export const cancelGeneration = (projectId: string, runId: string) =>
  post<Envelope<GenerationRun>>(`${base}/${segment(projectId)}/generations/${segment(runId)}/cancel`, {})

export const listGenerationCandidates = (projectId: string, chapterId: string) =>
  get<Envelope<GenerationCandidateSummary[]>>(`${base}/${segment(projectId)}/chapters/${segment(chapterId)}/candidates`)
