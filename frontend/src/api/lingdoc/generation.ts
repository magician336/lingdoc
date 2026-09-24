import { get, post } from '@/utils/request'

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
