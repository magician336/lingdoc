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

/**
 * 候选的有效性。
 *
 * `not_authorized` 是候选级的状态：撤权 / 权限丧失导致候选不可用。它与
 * `stale`（输入变了所以旧）不同，两者都不可采纳。
 */
export type CandidateValidity = 'fresh' | 'stale' | 'not_authorized'

/** 一次采纳把候选写到哪里。词表归服务端所有，客户端不枚举。 */
export type AdoptionTarget =
  | 'working_copy_partial'
  | 'working_copy_whole'
  | 'formal_version_whole'

export interface Candidate {
  id: string
  project_id: string
  chapter_id: string
  run_id: string
  body_markdown: string
  source_ids: string[]
  basis: CandidateBasis
  validity: CandidateValidity
  review_items: ReviewItem[]
  /** 候选正文的来源。三值文案由采纳对话框写，不在客户端猜。 */
  origin?: 'human' | 'ai_generated' | 'ai_assisted_human'
  /**
   * 产生这个候选的写作动作。
   *
   * 动作词表归服务端所有：这里只有形状。候选必须记下自己是怎么来的——采纳
   * 是一次显式确认，确认载荷要能拿它和候选对上。
   */
  writing_action?: string
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

/**
 * 部分采纳写入工作副本的范围（UTF-16 偏移，与选区一致）。
 */
export interface AdoptionScope {
  start_utf16: number
  end_utf16: number
}

/**
 * 采纳的显式确认载荷。
 *
 * 采纳不是「点一下就写进去」：用户必须确认这次采纳的是哪个动作的结果、
 * 写到哪里、写到哪一段、确认时刻、确认人是谁、候选是什么来源。缺任何一项，
 * 服务层拒——所以这不是界面上的一个勾选框，而是请求体的一部分。
 */
export interface AdoptionConfirmation {
  writing_action: string
  adoption_target: AdoptionTarget
  scope?: AdoptionScope
  confirmed_at: string
  confirmed_by: string
  origin: 'human' | 'ai_generated' | 'ai_assisted_human'
}

export interface AcceptCandidateInput {
  candidate_id: string
  expected_chapter_version_id: string | null
  expected_spec_revision: number
  replace_existing: boolean
  confirmation: AdoptionConfirmation
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
