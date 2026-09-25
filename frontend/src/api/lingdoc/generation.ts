import { get, post } from '@/utils/request'
import type { Candidate } from './candidateAdoption'

export interface GenerationRun {
  id: string
  project_id: string
  chapter_id: string
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'interrupted'
  candidate_id: string | null
  error?: { code: string; message: string; retryable: boolean }
}

export interface GenerationRequest {
  chapter_id: string
  asset_ids: string[]
  instruction: string
  expected_spec_revision: number
  expected_chapter_version_id: string | null
}

export interface GenerationCandidateSummary {
  id: string
  run_id: string
  validity: 'fresh' | 'stale'
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
