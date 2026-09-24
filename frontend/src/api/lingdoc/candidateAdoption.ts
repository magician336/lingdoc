import { get, post } from '@/utils/request'

export interface ReviewItem {
  id: string
  statement: string
  origin_candidate_id: string
}

export interface CandidateBasis {
  spec_revision: number
  chapter_version_id: string | null
  template_id: string
  template_version: string
  ruleset_hash: string
  asset_versions: Array<{ asset_id: string; asset_revision: number }>
}

export interface Candidate {
  id: string
  project_id: string
  chapter_id: string
  run_id: string
  body_markdown: string
  source_ids: string[]
  basis: CandidateBasis
  validity: 'fresh' | 'stale'
  review_items: ReviewItem[]
}

export interface Chapter {
  id: string
  project_id: string
  section_id: string
  title: string
  current_version_id: string | null
  body_markdown: string
  source_ids: string[]
  review_items: ReviewItem[]
  confirmation_valid: boolean
}

export interface ApiMeta {
  replayed: boolean
  refresh_required: boolean
}

export interface Envelope<T> {
  data: T
  request_id: string
  meta: ApiMeta
}

export interface AcceptCandidateInput {
  candidate_id: string
  expected_chapter_version_id: string | null
  expected_spec_revision: number
  replace_existing: boolean
}

export function getCandidate(projectId: string, candidateId: string): Promise<Envelope<Candidate>> {
  return get(`/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}/candidates/${encodeURIComponent(candidateId)}`) as unknown as Promise<Envelope<Candidate>>
}

export function listChapters(projectId: string): Promise<Envelope<Chapter[]>> {
  return get(`/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}/chapters`) as unknown as Promise<Envelope<Chapter[]>>
}

export function acceptCandidate(
  projectId: string,
  chapterId: string,
  payload: AcceptCandidateInput,
  idempotencyKey: string,
): Promise<Envelope<Chapter>> {
  return post(
    `/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}/chapters/${encodeURIComponent(chapterId)}/acceptances`,
    payload,
    { headers: { 'Idempotency-Key': idempotencyKey } },
  ) as unknown as Promise<Envelope<Chapter>>
}

export function newIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `candidate-adoption-${crypto.randomUUID()}`
  }
  return `candidate-adoption-${Date.now()}-${Math.random().toString(36).slice(2)}`
}
