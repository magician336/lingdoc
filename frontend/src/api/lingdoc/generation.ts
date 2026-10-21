import { get, post } from '@/utils/request'
import type { Candidate } from './candidateAdoption'
import type { GenerationActionDeclaration } from './generationAction'
import type { WritingActionOutputParams, WritingActionScope } from './writingAction'

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
  /** 这次是按哪个动作发起的。旧路径没有动作，字段不出现。 */
  writing_action?: string
  /** 这次动作的目标范围。旧路径没有范围，字段不出现。 */
  scope?: WritingActionScope
}

/**
 * 一个没有进入模型请求的可选上下文条目。
 *
 * 丢弃是可见的：静默丢弃会让用户以为它进去了。
 */
export interface GenerationDroppedContextItem {
  category: string
  id: string
  reason: string
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
  /** 用户选了、但没有进入模型请求的可选上下文条目。 */
  dropped_context?: GenerationDroppedContextItem[]
}

/** 一个上下文条目。资料、来源与证据卡都是它自己的 ID。 */
export interface GenerationContextItem {
  id: string
  version?: number
}

/**
 * 用户对这次执行要纳入的可选上下文的选择。
 *
 * 键是上下文类别，值是这一类别的条目。类别必须落在动作声明的可选上下文里。
 */
export interface GenerationContextSelection {
  include?: Record<string, GenerationContextItem[]>
  exclude?: Record<string, GenerationContextItem[]>
}

export interface GenerationRequest extends Partial<GenerationActionDeclaration> {
  evidence_card_refs?: Array<{ id: string; version: number }>
  chapter_id: string
  asset_ids: string[]
  instruction: string
  expected_spec_revision: number
  expected_chapter_version_id: string | null
  /**
   * 动作声明。
   *
   * 三个字段全部缺省 ⇒ 旧路径（自由文本说明驱动）；任一出现 ⇒ 动作路径
   * （动作与产出参数必齐）。取值落在动作声明的闭合取值集之外时，服务端拒绝。
   */
  writing_action?: string
  output_params?: WritingActionOutputParams
  scope?: WritingActionScope
  /** 用户对可选上下文的选择。只在动作路径下有意义。 */
  context?: GenerationContextSelection
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
