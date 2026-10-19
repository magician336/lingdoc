/**
 * G3 验收台 · 左栏的 G3 主流程分步叙事骨架。
 *
 * 步骤顺序取自 G3 状态机的前置条件（建项 → 写规格 → 立项阅读 → 激活 → … → 下载），
 * 硬约束写在 `precondition` 里，因为顺序错一步就会得到 409/422 而不是别的错误。
 *
 * **本票只是形态预览**：每一步的执行状态都是「未执行」，页面不发起任何真实调用。
 * 把执行状态做成数据而不是散落在模板里，是为了让「未执行」这件事无法被悄悄抹掉。
 */

/** 本票的执行状态：形态预览，一律未执行。 */
export const G3_FLOW_EXECUTION_STATE = '未执行'

export interface G3FlowStep {
  index: number
  /** 步骤名。 */
  name: string
  /** HTTP 方法。 */
  method: string
  /** 端点（相对 `/api/v1/lingdoc/projects/{projectId}`）。 */
  path: string
  /** 前置条件与硬约束。 */
  precondition: string
}

export const G3_FLOW_STEPS: readonly G3FlowStep[] = [
  {
    index: 1,
    name: '建项',
    method: 'POST',
    path: '/projects',
    precondition: '演示模板 template-demo（version 1），必填字段 research_subject / research_goal。',
  },
  {
    index: 2,
    name: '写规格',
    method: 'PUT',
    path: '/projects/{id}/spec',
    precondition: '带 expected_spec_revision；active 项目的语义变更不能走普通保存。',
  },
  {
    index: 3,
    name: '登记立项阅读',
    method: 'POST',
    path: '/projects/{id}/activation-read',
    precondition: '带 expected_project_version，返回 activation_read_id。',
  },
  {
    index: 4,
    name: '激活',
    method: 'POST',
    path: '/projects/{id}/activate',
    precondition: '必须带 activation_read_id，且只允许 Owner（非 Owner 返回 404，不泄露存在性）；激活才自动创建 question / method 两个章节。',
  },
  {
    index: 5,
    name: '保存章节版本',
    method: 'POST',
    path: '/chapters/{id}/versions',
    precondition: '带 expected_chapter_version_id 与 expected_spec_revision。',
  },
  {
    index: 6,
    name: '章节确认',
    method: 'POST',
    path: '/chapters/{id}/confirmations',
    precondition: '带 expected_chapter_version_id、expected_spec_revision 与 review_decisions；变更集应用后确认会失效，必须重新确认。',
  },
  {
    index: 7,
    name: '建变更集',
    method: 'POST',
    path: '/change-sets',
    precondition: '带 expected_context_revision 与 fields；写路由都要带 Idempotency-Key。',
  },
  {
    index: 8,
    name: '读影响评估',
    method: 'GET',
    path: '/change-sets/{id}/impact-assessment',
    precondition: '必须早于 apply；apply 之后再读会因基线过期返回 409。',
  },
  {
    index: 9,
    name: '应用',
    method: 'POST',
    path: '/change-sets/{id}/apply',
    precondition: '需要 Owner + 项目 active + 基线匹配；落后返回 409 version_conflict，非 Owner 返回 404。',
  },
  {
    index: 10,
    name: '处置影响任务',
    method: 'POST',
    path: '/impact-tasks/{id}/actions',
    precondition: '带 expected_version；complete 前必须先 assign 给当前操作者，否则是 404（不是 403）。',
  },
  {
    index: 11,
    name: '稳定化',
    method: 'POST',
    path: '/change-sets/{id}/stabilize',
    precondition: '需要 Owner 且目标修订等于项目当前修订；返回 BLOCKED 是 200 成功响应，必须带全部 blockers。',
  },
  {
    index: 12,
    name: '交付检查',
    method: 'POST',
    path: '/projects/{id}/checks',
    precondition: '带 expected_project_version；这个端点不要带 Idempotency-Key。',
  },
  {
    index: 13,
    name: '冻结交付快照',
    method: 'POST',
    path: '/projects/{id}/releases',
    precondition: '闸门关闭时不是拒绝，而是冻结一份「被阻断」的快照（check.status = blocked）。',
  },
  {
    index: 14,
    name: '发起导出',
    method: 'POST',
    path: '/projects/{id}/releases/{sid}/exports',
    precondition: 'format 只能是 docx；202 只表示已受理，判定成功要看产物 status=verified 与 file_sha256。',
  },
  {
    index: 15,
    name: '下载导出产物',
    method: 'GET',
    path: '/projects/{id}/exports/{eid}/file',
    precondition: '唯一不返回 JSON 的接口；下载地址用响应里的 download_path，不要自己拼前缀。',
  },
]

/** 每一步的执行状态。本票恒为「未执行」。 */
export function flowExecutionStates(): string[] {
  return G3_FLOW_STEPS.map(() => G3_FLOW_EXECUTION_STATE)
}
