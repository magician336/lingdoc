import { get, post, put } from '@/utils/request'

export interface Project {
  id: string
  name: string
  status: 'draft' | 'active'
  project_version: number
  spec_revision: number
  current_context_revision: number
  spec: Record<string, string>
  template_id: string
  template_version: string
  template_copy_version?: number
  template_copy?: ProjectTemplateCopy
  members: Array<{ user_id: string; role: 'owner' | 'collaborator' }>
}

export interface TemplateField {
  field_id: string
  label: string
  description?: string
  type: string
  order?: number
  required: boolean
  options?: string[]
}

export interface TemplateSection {
  section_id: string
  title: string
  description?: string
  order?: number
  required: boolean
}

export interface TemplateTerm {
  term_id: string
  preferred: string
  variants: string[]
}

export interface TemplateRule {
  rule_id: string
  kind: string
  severity: string
  evaluator: string
  message?: string
  parameters: Record<string, unknown>
}

export interface TemplateCopyDefinition {
  fields: TemplateField[]
  sections: TemplateSection[]
  terms: TemplateTerm[]
  required_fields: string[]
  rules: TemplateRule[]
}

export interface ProjectTemplateCopy {
  id: string
  project_id: string
  source_template_id: string
  source_template_version: string
  version: number
  status: 'draft' | 'bound' | 'superseded' | 'discarded'
  content_hash: string
  ruleset_hash: string
  definition: TemplateCopyDefinition & { id: string; name: string; version: string; is_demo: boolean }
  created_by: string
  created_at: string
}

export interface TemplateCopyEditInput extends TemplateCopyDefinition {
  expected_project_version: number
  expected_template_copy_version: number
}

export interface TemplateMigrationPreview {
  project_id: string
  source_template_id: string
  source_template_version: string
  source_copy_version: number
  target_template_id: string
  target_template_version: string
  target_copy_version: number
  target_content_hash: string
  target_ruleset_hash: string
  ruleset_changed: boolean
  expected_project_version: number
  fields: Array<{
    field_id: string
    value?: string
    before?: string
    after?: string
    old_type?: string
    new_type?: string
    status: string
  }>
  section_changes: Array<{ section_id: string; before?: string; after?: string; status: 'added' | 'removed' | 'changed' }>
  missing_required: string[]
  orphaned: string[]
  incompatible: string[]
}

export interface TemplateUpgradeImpact {
  affected_field_ids: string[]
  affected_section_ids: string[]
  affected_chapter_ids: string[]
  invalidated_confirmation_chapter_ids: string[]
  changed_rule_ids: string[]
  validation_issue_effect: string
  delivery_snapshot_effect: string
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

export interface CitationUsage {
  source_id: string
  purpose: string
  limitation: string
}

export interface CitationStatus {
  source_id: string
  status: 'available' | 'needs_review' | 'unavailable'
  detail?: string
}

export interface ChangeImpact {
  chapter_id: string
  chapter_version_id?: string | null
  title: string
  reason: string
  status: 'open' | 'reviewed' | 'stale'
}

export interface ChangeSet {
  id: string
  project_id: string
  created_by: string
  reason: string
  status: 'assessed' | 'applied' | 'rejected' | 'stale'
  base_context_revision: number
  target_context_revision?: number
  base_spec_revision: number
  target_spec_revision?: number
  fields: Array<{ key: string; old_value: string; new_value: string }>
  impacts: ChangeImpact[]
  template_upgrade?: {
    base_template_copy_version: number
    definition: ProjectTemplateCopy['definition']
    field_values: Record<string, string>
    preview: TemplateMigrationPreview
    impact?: TemplateUpgradeImpact
  }
  created_at: string
  applied_at?: string
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
  citation_usages?: CitationUsage[]
  citation_statuses?: CitationStatus[]
  review_items: ReviewItem[]
  confirmation_valid: boolean
}

export interface WorkingCopy {
  citation_usages?: CitationUsage[]
  project_id: string
  chapter_id: string
  base_chapter_version_id: string | null
  spec_revision: number
  working_copy_revision: number
  body_markdown: string
  source_ids: string[]
  review_items: ReviewItem[]
  updated_at: string
}

export interface ChapterVersion {
  id: string
  project_id: string
  chapter_id: string
  parent_version_id: string | null
  body_markdown: string
  source_ids: string[]
  review_items: ReviewItem[]
  spec_revision: number
  confirmation_valid: boolean
  created_at: string
}

export interface CommittedChapterVersion {
  chapter_version_id: string
  parent_chapter_version_id: string | null
  committed_working_copy_revision: number
  next_working_copy_revision: number
  spec_revision: number
  body_markdown: string
  source_ids: string[]
  review_items: ReviewItem[]
}

export interface SelectedRewriteCandidate {
  candidate_id: string
  project_id: string
  chapter_id: string
  status: 'queued' | 'ready' | 'applying' | 'failed' | 'stale'
  run_mode: 'mock' | 'real_api_fake_model' | 'real'
  base_chapter_version_id: string | null
  spec_revision: number
  working_copy_revision: number
  selection: { start_utf16: number; end_utf16: number; selected_text: string }
  replacement_markdown: string
  source_ids: string[]
  authorized_sources: Array<{
    source_id: string
    asset_id: string
    asset_revision: number
    locator: string
    quoted_text_hash: string
    authorized_at: string
  }>
  review_items: ReviewItem[]
  error_code: string
  created_at: string
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
export const getTemplateCopy = (projectId: string, version: number) =>
  get<Result<ProjectTemplateCopy>>(`${base}/${segment(projectId)}/template-copies/${version}`)
export const previewTemplateCopyEdit = (projectId: string, input: TemplateCopyEditInput) =>
  post<Result<TemplateMigrationPreview>>(`${base}/${segment(projectId)}/template-copy/preview`, input)
export const saveTemplateCopyEdit = (projectId: string, input: TemplateCopyEditInput, key: string) =>
  put<Result<Project>>(`${base}/${segment(projectId)}/template-copy`, input, keyHeader(key))
export const saveSpec = (id: string, expected: number, fields: Record<string, string>, key: string) =>
  put<Result<Project>>(`${base}/${segment(id)}/spec`,
    { expected_spec_revision: expected, fields }, keyHeader(key))
export const activateProject = (id: string, expected: number, key: string, projectVersion: number) =>
  post<Result<Project>>(`${base}/${segment(id)}/activate`,
    { expected_spec_revision: expected, expected_project_version: projectVersion, reviewed_project_version: projectVersion }, keyHeader(key))
export const listChapters = (id: string) => get<Result<Chapter[]>>(`${base}/${segment(id)}/chapters`)
export const getWorkingCopy = (projectId: string, chapterId: string) =>
  get<Result<WorkingCopy>>(`${base}/${segment(projectId)}/chapters/${segment(chapterId)}/working-copy`)
export const saveWorkingCopy = (projectId: string, chapterId: string, input: {
  citation_usages?: CitationUsage[]
  base_chapter_version_id: string | null
  expected_spec_revision: number
  expected_working_copy_revision: number
  body_markdown: string
  source_ids: string[]
}, key: string) => put<Result<WorkingCopy>>(
  `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/working-copy`, input, keyHeader(key),
)
export const commitWorkingCopy = (projectId: string, chapterId: string, input: {
  expected_spec_revision: number
  expected_working_copy_revision: number
  expected_chapter_version_id: string | null
}, key: string) => post<Result<CommittedChapterVersion>>(
  `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/working-copy/commit`, input, keyHeader(key),
)
export const listChapterVersions = (projectId: string, chapterId: string) =>
  get<Result<{ items: ChapterVersion[] }>>(`${base}/${segment(projectId)}/chapters/${segment(chapterId)}/versions`)
export const restoreWorkingCopy = (projectId: string, chapterId: string, input: {
  chapter_version_id: string
  expected_spec_revision: number
  expected_working_copy_revision: number
  expected_chapter_version_id: string | null
}, key: string) => post<Result<WorkingCopy>>(
  `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/working-copy/restore`, input, keyHeader(key),
)
export const createSelectedRewrite = (projectId: string, chapterId: string, input: {
  base_chapter_version_id: string | null
  expected_spec_revision: number
  expected_working_copy_revision: number
  selection: { start_utf16: number; end_utf16: number; selected_text: string }
  instruction: string
  source_ids: string[]
}, key: string) => post<Result<SelectedRewriteCandidate>>(
  `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/rewrite-candidates`, input, keyHeader(key),
)
export const getSelectedRewrite = (projectId: string, candidateId: string) =>
  get<Result<SelectedRewriteCandidate>>(`${base}/${segment(projectId)}/rewrite-candidates/${segment(candidateId)}`)
export const applySelectedRewrite = (projectId: string, chapterId: string, candidateId: string, input: {
  expected_spec_revision: number
  expected_working_copy_revision: number
  base_chapter_version_id: string | null
}, key: string) => post<Result<WorkingCopy>>(
  `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/rewrite-candidates/${segment(candidateId)}/apply`, input, keyHeader(key),
)
export const getAccessStatus = (id: string) => get<Result<AccessStatus>>(`${base}/${segment(id)}/access-status`)
export const listChangeSets = (id: string) => get<Result<ChangeSet[]>>(`${base}/${segment(id)}/change-sets`)
export const getChangeSet = (projectId: string, changeSetId: string) =>
  get<Result<ChangeSet>>(`${base}/${segment(projectId)}/change-sets/${segment(changeSetId)}`)
export const createChangeSet = (projectId: string, input: {
  expected_context_revision: number
  fields: Record<string, { old_value: string; new_value: string }>
  affected_chapter_ids: string[]
  reason: string
  template_upgrade?: TemplateCopyEditInput & { field_values?: Record<string, string> }
}, key: string) => post<Result<ChangeSet>>(
  `${base}/${segment(projectId)}/change-sets`, input, keyHeader(key),
)
export const applyChangeSet = (projectId: string, changeSetId: string, key: string) =>
  post<Result<ChangeSet>>(`${base}/${segment(projectId)}/change-sets/${segment(changeSetId)}/apply`, {}, keyHeader(key))
export const rejectChangeSet = (projectId: string, changeSetId: string, key: string) =>
  post<Result<ChangeSet>>(`${base}/${segment(projectId)}/change-sets/${segment(changeSetId)}/reject`, {}, keyHeader(key))
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
  citation_usages: CitationUsage[]
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
