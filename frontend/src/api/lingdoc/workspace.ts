import { get, post, put } from '@/utils/request'

export interface Project {
  id: string
  name: string
  status: 'draft' | 'active'
  project_version: number
  spec_revision: number
  spec: Record<string, string>
  template_id: string
  template_version: string
  members: Array<{ user_id: string; role: 'owner' | 'collaborator' }>
}

export interface ReviewItem {
  id: string
  statement: string
  origin_candidate_id: string
}

export interface ReviewDecision {
  review_item_id: string
  disposition: 'resolved' | 'retained_warning'
  reason: string
}

export interface Confirmation {
  id: string
  chapter_id: string
  chapter_version_id: string
  spec_revision: number
  asset_versions: Array<{ asset_id: string; asset_revision: number }>
  template_version: string
  actor_user_id: string
  created_at: string
  valid: boolean
  review_decisions: ReviewDecision[]
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

export interface Asset {
  id: string
  project_id: string
  knowledge_id: string
  title: string
  asset_revision: number
  processing_state: 'pending' | 'processing' | 'ready' | 'failed' | 'replaced'
}

export interface Source {
  id: string
  project_id: string
  asset_id: string
  asset_revision: number
  locator: string
  quoted_text: string
  quoted_text_hash: string
  status: 'available' | 'stale' | 'unavailable'
}

/**
 * 语境窗口里的一段邻居（不含被引用的那一段本身——那一段是 `source.quoted_text`）。
 *
 * `verbatim` 说的是「这段文字此刻逐字等于原文坐标上的那一段」。取不回原文（PDF/DOCX
 * 这类要重解析的格式）时它是 false，而 `text` **照样有内容**——那是分块表里落库的正文，
 * 只是无法被原文证明。两种 false 在界面上的措辞不能混成一句「原文」。
 *
 * 强档下逐字对不上的（坐标漂移、该块被单独编辑过）`text` 是空串：那一段确实不是原文，
 * 摆出来就是错的。它仍留在 `segments` 里——丢掉它会让窗口看上去是连续的。
 */
export interface SourceContextSegment {
  source_id: string
  chunk_index: number
  relation: 'before' | 'after'
  verbatim: boolean
  text: string
}

/**
 * 引用处的原文上下文（契约 getSourceContext）。
 *
 * 与「跳回原文」的区别：不跳转、不要知识库 ID、也不重新检索，只把这一条**已产出**引用
 * 所在的分块摊开。展开的是分块表的文本，不是重新解析原文件得到的逐字原文（后者没有落库，
 * 见 ADR-0001）。
 *
 * `context_available` 为 false 时 `segments` 一定是空数组：引用块本身是派生块
 * （summary / image_ocr / …，没有指向原文的坐标），或它此刻指不回原文。两种情况都由
 * `source.status` 如实说着，界面不必自己猜。
 */
export interface SourceContext {
  source: Source
  context_available: boolean
  window: { before: number; after: number }
  segments: SourceContextSegment[]
}

export interface Result<T> {
  data: T
  request_id: string
  meta: { replayed: boolean; refresh_required: boolean }
}

/**
 * 撤权之后的恢复入口（契约 §8）。它只答状态与恢复动作，不含正文、研究条件、资料标题。
 *
 * `content_access` 三态：`available` 一次都不拒绝；`restricted` 至少一条绑定资料被拒；
 * `unknown` 是服务端答不出（读不到绑定，或资料网关报错）。`unknown` 不是错误码，
 * 它是「答不出但能如实说答不出」——界面不能拿它当 `available`。
 */
export interface AccessStatus {
  project_id: string
  content_access: 'available' | 'unknown' | 'restricted'
  /**
   * 取值由服务端定，本轮只有 `restore_source_authorization` 与 `create_clean_project`。
   *
   * 刻意写成 `string[]` 而不是字面量联合：字面量联合在编译期看着更严，代价是界面上
   * 那处 `switch` 的兜底分支变成「不可达」，而它恰恰是唯一能兜住枚举漂移的地方——
   * 服务端将来多一个动作，界面必须原样把它列出来，不能因为不认识就悄悄吞掉一条
   * 恢复路径（同 `deliveryState.ts` 的 `issueTargetLabel`：认不出来就显示 ID，不猜）。
   */
  recovery_actions: string[]
  can_create_project: boolean
}

const base = '/api/v1/lingdoc/projects'
const segment = (id: string) => encodeURIComponent(id)
const keyHeader = (key: string) => ({ headers: { 'Idempotency-Key': key } })

export const listProjects = () => get<Result<{ items: Project[]; truncated: boolean }>>(base)
export const createProject = (name: string, key: string) =>
  post<Result<Project>>(base, { name, template_id: 'template-demo' }, keyHeader(key))
export const getProject = (id: string) => get<Result<Project>>(`${base}/${segment(id)}`)
export const saveSpec = (id: string, expected: number, fields: Record<string, string>, key: string) =>
  put<Result<Project>>(`${base}/${segment(id)}/spec`,
    { expected_spec_revision: expected, fields }, keyHeader(key))
export const activateProject = (id: string, expected: number, key: string) =>
  post<Result<Project>>(`${base}/${segment(id)}/activate`,
    { expected_spec_revision: expected }, keyHeader(key))
export const listChapters = (id: string) => get<Result<Chapter[]>>(`${base}/${segment(id)}/chapters`)
export const getAccessStatus = (id: string) => get<Result<AccessStatus>>(`${base}/${segment(id)}/access-status`)
export const listAssets = (id: string) => get<Result<Asset[]>>(`${base}/${segment(id)}/assets`)
export const bindAsset = (id: string, knowledgeId: string, key: string) =>
  post<Result<Asset>>(`${base}/${segment(id)}/assets`, { knowledge_id: knowledgeId }, keyHeader(key))
export const retrieveSources = (id: string, query: string, assetIds: string[]) =>
  post<Result<Source[]>>(`${base}/${segment(id)}/retrieval`, { query, asset_ids: assetIds })
export const getSource = (projectId: string, sourceId: string) =>
  get<Result<Source>>(`${base}/${segment(projectId)}/sources/${segment(sourceId)}`)
export const getSourceContext = (projectId: string, sourceId: string) =>
  get<Result<SourceContext>>(`${base}/${segment(projectId)}/sources/${segment(sourceId)}/context`)
export const saveChapter = (projectId: string, chapterId: string, input: {
  expected_chapter_version_id: string | null
  expected_spec_revision: number
  body_markdown: string
  source_ids: string[]
}, key: string) => post<Result<Chapter>>(
  `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/versions`, input, keyHeader(key),
)
export const confirmChapter = (projectId: string, chapterId: string, input: {
  expected_chapter_version_id: string
  expected_spec_revision: number
  review_decisions: ReviewDecision[]
}, key: string) => post<Result<Confirmation>>(
  `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/confirmations`, input, keyHeader(key),
)
