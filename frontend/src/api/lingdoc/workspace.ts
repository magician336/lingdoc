import { get, post, postNoBody, put } from '@/utils/request'
import type { AdoptionConfirmation } from './candidateAdoption'
import type { RuleSet } from './templateGovernance'

export type ProjectFunctionRole = 'author' | 'researcher' | 'reviewer' | 'observer'
export type ProjectGovernanceRole = 'owner' | 'admin' | 'member'

export interface ProjectMember {
  user_id: string
  role: 'owner' | 'collaborator'
  governance_role?: ProjectGovernanceRole
  function_roles?: ProjectFunctionRole[]
  function_scopes?: Record<string, string[]>
  status?: string
}

export interface ProjectMemberInput extends Omit<ProjectMember, 'role' | 'governance_role'> {
  role: 'collaborator'
  governance_role: 'admin' | 'member'
}

export interface Project {
  id: string
  name: string
  status: 'draft' | 'active' | 'archived'
  project_version: number
  spec_revision: number
  current_context_revision: number
  spec: Record<string, string>
  spec_fields?: Record<string,{value:string;origin:string;status:string;modified_by:string;modified_at:string;provenance?:{source_type:string;source_id?:string;source_version?:string;locator?:string;created_by:string;accessible:boolean;needs_review:boolean}}>
  template_id: string
  template_version: string
  template_copy_version?: number
  template_copy?: ProjectTemplateCopy
  members: ProjectMember[]
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

export interface ProjectBindingPolicy {
  rule_id: string
  may_disable: boolean
  allowed_target_kinds?: string[]
  allowed_target_ids?: string[]
  allowed_severities?: string[]
  allowed_parameter_keys?: string[]
  parameter_bounds?: Record<string, { minimum?: number; maximum?: number }>
}

export interface LegacyRuleOverrideInput {
  expected_project_version: number
  expected_template_copy_version: number
  reason: string
  overrides: Array<{
    rule_id: string
    enabled?: boolean
    target_kind?: string
    target_id?: string
    severity?: string
    parameters?: Record<string, unknown>
    remove_parameter_keys?: string[]
  }>
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
  definition: TemplateCopyDefinition & { id: string; name: string; version: string; is_demo: boolean; ruleset_snapshot?: unknown; ruleset_ref?: unknown }
  rule_set_snapshot?: RuleSet
  binding_policies?: ProjectBindingPolicy[]
  source_policy_hash?: string
  created_by: string
  created_at: string
}

export interface TemplateCopyEditInput extends TemplateCopyDefinition {
  expected_project_version: number
  expected_template_copy_version: number
}

export interface TemplateFieldArchive {
  id: string
  project_id: string
  field_id: string
  status: 'recorded' | 'removed'
  archive_version: number
  value_present: boolean
  value: string
  metadata_present: boolean
  metadata?: NonNullable<Project['spec_fields']>[string]
  value_redacted: boolean
  created_by: string
  created_at: string
  project_version: number
  spec_revision: number
  template_copy_version: number
  target_content_hash: string
  removed_at?: string
}

export interface TemplateFieldArchiveReceipt {
  id: string
  status: 'recorded' | 'removed'
  archive_version: number
  project_version: number
  spec_revision: number
  template_copy_version: number
}

export interface CreateTemplateFieldArchiveInput {
  field_id: string
  expected_spec_revision: number
  expected_context_revision?: number
  template_copy?: TemplateCopyEditInput
  template_upgrade?: TemplateCopyEditInput & { field_values?: Record<string, string> }
}

export interface ArchivedTemplateFieldRemoval {
  field_id: string
  archive_id: string
  expected_archive_version: number
}

export interface RemoveArchivedTemplateFieldInput {
  confirmed: true
  expected_archive_version: number
  expected_spec_revision: number
  template_copy: TemplateCopyEditInput
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
  anchor?: ReviewAnchor | null
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

// The G3 impact assistant's own two-group reading of what a proposed change
// affects. This is NOT the impact resolver's verdict: that one keeps
// `impacts[].certainty` on ImpactAssessment, and the two never share a field
// name. A draft that the assistant did not produce carries no explanation.
export interface AssistantAffectedObject {
  kind: string
  id: string
  version: string
}

export interface AssistantAffectedBasis {
  source_type: 'change_set' | 'dependency_edge' | 'ruleset' | 'impact_task' | 'object_version'
  source_id: string
  detail?: string
}

export interface AssistantAffectedEntry {
  object: AssistantAffectedObject
  reason: string
  basis: AssistantAffectedBasis
}

export interface AssistantAffectedExplanation {
  definite_affected: AssistantAffectedEntry[]
  possible_affected: AssistantAffectedEntry[]
}

export interface ChangeSet {
  id: string
  project_id: string
  created_by: string
  decision_by?: string
  decided_at?: string
  reason: string
  status: 'draft' | 'assessed' | 'applied' | 'rejected' | 'stale' | 'stabilized' | 'satisfied'
  reassessed_from_id?: string
  reverses_change_set_id?: string
  stale_reason?: string
  base_context_revision: number
  target_context_revision?: number
  base_spec_revision: number
  target_spec_revision?: number
  fields: Array<{ key: string; old_value: string; new_value: string }>
  impacts: ChangeImpact[]
  affected_explanation?: AssistantAffectedExplanation
  template_upgrade?: {
    base_template_copy_version: number
    definition: ProjectTemplateCopy['definition']
    field_values: Record<string, string>
    archived_removals?: ArchivedTemplateFieldRemoval[]
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
  claim_provenance_unavailable?: boolean
  claim_provenance?: { id: string; origin_kind: 'generation' | 'rewrite'; candidate_id: string; run_id?: string; working_copy_revision: number; start_utf16: number; end_utf16: number; status: 'needs_revalidation' | 'stale'; created_at: string }[]
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
  claim_provenance?: WorkingCopy['claim_provenance']
  claim_provenance_unavailable?: boolean
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
	claims?: { text: string; references: { card_id: string; card_version: number; anchor_id: string; anchor_version: number; source_id: string; locator: string; selection_start: number; selection_end: number }[] }[]
 evidence_card_refs?:Array<{id:string;version:number}>
  candidate_id: string
  project_id: string
  chapter_id: string
  /**
   * 这个候选还能不能被采纳：与候选层同一份词表。
   *
   * 空值表示这次运行没有交出可采纳的产出——运行中是这样，一次失败也是这样；
   * 失败的原因在 error_code 里。
   */
  validity: '' | 'fresh' | 'stale' | 'not_authorized'
  /**
   * 这个候选此刻正被写进工作副本。
   *
   * 它是一个**事实**，不是一段状态词：写完之前不能重来。失效判据仍然只有
   * `validity` 一个拥有者——「还能不能被采纳」与「正在应用」回答的不是同一
   * 个问题，所以它们不共用一个字段。
   */
  apply_pending: boolean
  run_mode: 'real' | 'mock' | 'real_api_fake_model'
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
    academic_snapshot?: { raw_ref: { sha256: string; size: number }; text_ref: { sha256: string; size: number } }
  }>
  review_items: ReviewItem[]
  error_code: string
  created_at: string
  /** 产生这个候选的动作。旧路径没有动作，字段不出现。 */
  writing_action?: string
  /** 候选正文的来源。旧路径没有来源标注，字段不出现。 */
  origin?: 'human' | 'ai_generated' | 'ai_assisted_human'
  /**
   * 用户选了、但没有进入模型请求的可选上下文条目。
   *
   * 丢弃是可见的：服务端点名并附原因，界面才能告诉用户「这一条没进去」。
   */
  dropped_context?: Array<{ category: string; id: string; reason: string }>
}

/**
 * 一份语言评审报告的基线：这份报告是针对哪一份正文写的。
 *
 * 基线变了 ⇒ 报告作废，不覆盖：评审意见挂在具体的措辞上，正文变了，那些意
 * 见说的已经不是同一句话了。
 */
export interface ReviewBaseline {
  working_copy_revision: number
  base_chapter_version_id: string | null
  spec_revision: number
  body_hash: string
}

/**
 * 语言评审报告。
 *
 * 报告不是候选：它没有可采纳物，产出是一份待用户逐条处置的意见清单。用户预
 * 览之后显式确认，才由一个独立的应用入口把它写进工作副本的待核事项。
 */
export interface ReviewReport {
  report_id: string
  project_id: string
  chapter_id: string
  /** 评审意见：待核事项与问题清单。每一条最后都要落进工作副本的待核事项。 */
  findings: ReviewItem[]
  source_ids: string[]
  authorized_sources: Array<{
    source_id: string
    asset_id: string
    asset_revision: number
    locator: string
    quoted_text_hash: string
    authorized_at: string
    academic_snapshot?: { raw_ref: { sha256: string; size: number }; text_ref: { sha256: string; size: number } }
  }>
  baseline: ReviewBaseline
  /** 报告自身的版本标识：它由报告的内容算出，因此可复算。 */
  report_version: string
  /** 生成报告时的授权状态。应用时按**当前**授权复核一遍。 */
  authorization_state: '' | 'fresh' | 'stale' | 'not_authorized'
  run_mode: 'real' | 'mock' | 'real_api_fake_model'
  writing_action: string
  origin: 'human' | 'ai_generated' | 'ai_assisted_human'
  state: 'ready' | 'applied' | 'rejected'
  review_decisions: ReviewDecision[]
  error_code: string
  scope: 'chapter' | 'section'
  selection: { start_utf16: number; end_utf16: number; selected_text: string }
  dropped_context?: Array<{ category: string; id: string; reason: string }>
  created_at: string
}

/**
 * 发起一次语言评审的请求。
 *
 * `selection` 只在选段范围下带；整章范围评审的是整份正文。
 */
export interface ReviewReportRequest {
  scope: 'chapter' | 'section'
  selection?: { start_utf16: number; end_utf16: number; selected_text: string }
  context?: {
    include?: Record<string, Array<{ id: string; version?: number }>>
    exclude?: Record<string, Array<{ id: string; version?: number }>>
  }
  expected_spec_revision: number
  expected_working_copy_revision: number
  base_chapter_version_id: string | null
  /** 补充说明：它不参与动作判定。 */
  instruction: string
}

/**
 * 应用一份评审报告的请求。
 *
 * 逐条处置是必填的：每一条评审意见都要有处置与理由，缺一即拒。
 */
export interface ApplyReviewRequest {
  expected_spec_revision: number
  expected_working_copy_revision: number
  base_chapter_version_id: string | null
  review_decisions: ReviewDecision[]
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
export const createProject = (name: string, key: string, templateId='template-demo',templateVersion='1') =>
  post<Result<Project>>(base, { name, template_id: templateId,template_version:templateVersion }, keyHeader(key))
export const getProject = (id: string) => get<Result<Project>>(`${base}/${segment(id)}`)
export const saveProjectMembers = (id: string, expectedVersion: number, members: ProjectMemberInput[], key: string) =>
  put<Result<Project>>(`${base}/${segment(id)}/members`, {
    expected_project_version: expectedVersion,
    members,
  }, keyHeader(key))
export const getTemplateCopy = (projectId: string, version: number) =>
  get<Result<ProjectTemplateCopy>>(`${base}/${segment(projectId)}/template-copies/${version}`)
export const previewTemplateCopyEdit = (projectId: string, input: TemplateCopyEditInput) =>
  post<Result<TemplateMigrationPreview>>(`${base}/${segment(projectId)}/template-copy/preview`, input)
export const saveTemplateCopyEdit = (projectId: string, input: TemplateCopyEditInput, key: string) =>
  put<Result<Project>>(`${base}/${segment(projectId)}/template-copy`, input, keyHeader(key))
export const saveLegacyRuleOverrides = (projectId: string, input: LegacyRuleOverrideInput, key: string) =>
  put<Result<Project>>(`${base}/${segment(projectId)}/template-copy/legacy-rule-overrides`, input, keyHeader(key))
export const listTemplateFieldArchives = (projectId: string) =>
  get<Result<{ items: TemplateFieldArchive[] }>>(`${base}/${segment(projectId)}/template-copy/field-archives`)
export const getTemplateFieldArchive = (projectId: string, archiveId: string) =>
  get<Result<TemplateFieldArchive>>(`${base}/${segment(projectId)}/template-copy/field-archives/${segment(archiveId)}`)
export const createTemplateFieldArchive = (projectId: string, input: CreateTemplateFieldArchiveInput, key: string) =>
  post<Result<TemplateFieldArchiveReceipt>>(`${base}/${segment(projectId)}/template-copy/field-archives`, input, keyHeader(key))
export const removeArchivedTemplateField = (projectId: string, archiveId: string, input: RemoveArchivedTemplateFieldInput, key: string) =>
  post<Result<TemplateFieldArchiveReceipt>>(`${base}/${segment(projectId)}/template-copy/field-archives/${segment(archiveId)}/remove`, input, keyHeader(key))
export const saveSpec = (id: string, expected: number, fields: Record<string, string>, key: string) =>
  put<Result<Project>>(`${base}/${segment(id)}/spec`,
    { expected_spec_revision: expected, fields }, keyHeader(key))
export const activateProject = (id: string, expected: number, key: string, projectVersion: number, activationReadId?: string) =>
  post<Result<Project>>(`${base}/${segment(id)}/activate`,
    { expected_spec_revision: expected, expected_project_version: projectVersion, reviewed_project_version: projectVersion, activation_read_id: activationReadId }, keyHeader(key))
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
  // #753: optional audited dismissal of a suspected condition change; the
  // server only accepts it when it names the suspicions it just recomputed.
  condition_change_dismissal?: { dismissed_fields: string[]; reason: string }
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
  evidence_card_refs?:Array<{id:string;version:number}>
  base_chapter_version_id: string | null
  expected_spec_revision: number
  expected_working_copy_revision: number
  selection: { start_utf16: number; end_utf16: number; selected_text: string }
  instruction: string
  source_ids: string[]
  /** 这次请求按动作发起。三个字段都不给就是旧路径。 */
  writing_action?: string
  output_params?: { length?: string; tone?: string; ratio?: number; transform?: { mode?: string; target_language?: string; audience?: string } }
  scope?: 'chapter' | 'section'
  /**
   * 用户对这次执行要纳入的可选上下文的选择。
   *
   * 键是上下文类别，值是这一类别的条目。类别必须落在服务端声明的可选上下文
   * 里：越过的请求会被拒。没授权的条目不进模型请求，服务端在响应里点名。
   */
  context?: {
    include?: Record<string, Array<{ id: string; version?: number }>>
    exclude?: Record<string, Array<{ id: string; version?: number }>>
  }
}, key: string) => post<Result<SelectedRewriteCandidate>>(
  `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/rewrite-candidates`, input, keyHeader(key),
)
export const getSelectedRewrite = (projectId: string, candidateId: string) =>
  get<Result<SelectedRewriteCandidate>>(`${base}/${segment(projectId)}/rewrite-candidates/${segment(candidateId)}`)
export const applySelectedRewrite = (projectId: string, chapterId: string, candidateId: string, input: {
  expected_spec_revision: number
  expected_working_copy_revision: number
  base_chapter_version_id: string | null
  /**
   * 采纳的显式确认载荷。
   *
   * 动作化路径的候选必须带它：服务端要确认这次采纳的是哪个动作的结果、写
   * 到哪里、写到哪一段。缺了会被拒。形状与候选采纳端共用一份。
   */
  confirmation?: AdoptionConfirmation
}, key: string) => post<Result<WorkingCopy>>(
  `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/rewrite-candidates/${segment(candidateId)}/apply`, input, keyHeader(key),
)
/**
 * 发起一次语言评审：产出是一份报告，不写工作副本。
 *
 * 写操作带幂等键：同键同请求是重放，同键不同请求是冲突。
 */
export const createReviewReport = (projectId: string, chapterId: string, input: ReviewReportRequest, key: string) =>
  post<Result<ReviewReport>>(
    `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/review-reports`, input, keyHeader(key),
  )
/** 读回一份评审报告（预览）。 */
export const getReviewReport = (projectId: string, reportId: string) =>
  get<Result<ReviewReport>>(`${base}/${segment(projectId)}/review-reports/${segment(reportId)}`)
/** 拒绝一份评审报告：工作副本一个字符都不动。 */
export const rejectReviewReport = (projectId: string, reportId: string, key: string) =>
  postNoBody<Result<ReviewReport>>(
    `${base}/${segment(projectId)}/review-reports/${segment(reportId)}/reject`, keyHeader(key),
  )
/** 把一份评审报告显式应用到工作副本：逐条处置必填。 */
export const applyReviewReport = (projectId: string, chapterId: string, reportId: string, input: ApplyReviewRequest, key: string) =>
  post<Result<WorkingCopy>>(
    `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/review-reports/${segment(reportId)}/apply`, input, keyHeader(key),
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
  reverses_change_set_id?: string
  template_upgrade?: TemplateCopyEditInput & { field_values?: Record<string, string>; archived_removals?: ArchivedTemplateFieldRemoval[] }
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
export const retrieveSources = (id: string, query: string, assetIds: string[], chapterId?: string) =>
  post<Result<Source[]>>(`${base}/${segment(id)}/retrieval`, { query, asset_ids: assetIds, ...(chapterId ? { chapter_id: chapterId } : {}) })
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

/* ---- 文稿编辑器合同（document-editor-20261013）：ReviewAnchor PUT + 当前确认 GET ---- */
export interface ReviewAnchor {
  start_utf16: number
  end_utf16: number
  quote_markdown: string
  working_copy_revision: number
  chapter_version_id: string | null
  status: 'located' | 'stale'
}

export const putReviewItemAnchor = (projectId: string, chapterId: string, itemId: string, input: {
  expected_chapter_version_id: string | null
  expected_spec_revision: number
  expected_working_copy_revision: number
  anchor: { start_utf16: number; end_utf16: number; quote_markdown: string } | null
}, key: string) => put<Result<WorkingCopy>>(
  `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/working-copy/review-items/${segment(itemId)}/anchor`, input, keyHeader(key),
)

export const getCurrentConfirmation = (projectId: string, chapterId: string) =>
  get<Result<Confirmation | null>>(
    `${base}/${segment(projectId)}/chapters/${segment(chapterId)}/confirmations/current`,
  )
