/**
 * G3 验收台 · 状态词表与术语的纯逻辑。
 *
 * 词表是**封闭**的：只认仓库门禁的七个值（`docs/agents/development-rules.md:78-84`），
 * 页面不得引入自造词。严重度与责任域取自仓库根 `GLOSSARY.md:43-56`，
 * 动作到严重度的推导取自 ADR 0003（`internal/lingdoc/workspacecore` 影响解析）。
 *
 * 这些判定写成纯函数，才能在不搭 DOM 的情况下钉住「界面不自己猜」的几处关键点：
 * 词表封闭、NO_ACTION 不是任务状态、认不出的动作 fail closed。
 */

/** 仓库门禁状态词表，顺序与 `development-rules.md:82` 一致。 */
export const G3_STATUS_VALUES = [
  'PASS',
  'PASS (follow-up)',
  'N/A',
  'WARNING',
  'NOT RUN',
  'BLOCKED',
  'TODO',
] as const

export type G3Status = (typeof G3_STATUS_VALUES)[number]

/** 判一个字符串是不是词表内的状态。词表外的值一律不认。 */
export function isG3Status(value: unknown): value is G3Status {
  return typeof value === 'string' && (G3_STATUS_VALUES as readonly string[]).includes(value)
}

/** 状态到界面色调的映射，一一对应，不留兜底分支。 */
export type G3StatusTone =
  | 'pass'
  | 'follow-up'
  | 'na'
  | 'warning'
  | 'not-run'
  | 'blocked'
  | 'todo'

const STATUS_TONES: Record<G3Status, G3StatusTone> = {
  'PASS': 'pass',
  'PASS (follow-up)': 'follow-up',
  'N/A': 'na',
  'WARNING': 'warning',
  'NOT RUN': 'not-run',
  'BLOCKED': 'blocked',
  'TODO': 'todo',
}

export function statusTone(status: G3Status): G3StatusTone {
  return STATUS_TONES[status]
}

/**
 * 状态的中文释义，取自 `development-rules.md:78-84` 的原意。
 * 释义是给人读的，判定仍然只看 `status` 本身。
 */
const STATUS_SUMMARIES: Record<G3Status, string> = {
  'PASS': '当前 head 有可复核证据，满足本次交付要求。',
  'PASS (follow-up)': '当前 PR 接受该项不阻塞；真实服务验收事项必须链接到独立 GitHub Issue，不能把未运行写成已实测。',
  'N/A': '验收前提在当前目标中不存在。',
  'WARNING': '宿主环境有局部限制，但等价路径已通过；保留在报告中不阻塞放行。',
  'NOT RUN': '本次必需范围内的项目尚未执行。',
  'BLOCKED': '本次必需范围内的项目被阻塞。',
  'TODO': '尚未判定。',
}

export function statusSummary(status: G3Status): string {
  return STATUS_SUMMARIES[status]
}

/**
 * 矩阵里出现这三个词时 PR 不能放行（`development-rules.md:84`）。
 * 只看状态本身，不看它挂在哪一行。
 */
export function blocksRelease(status: G3Status): boolean {
  return status === 'TODO' || status === 'NOT RUN' || status === 'BLOCKED'
}

/** 影响严重度三档（`GLOSSARY.md:46`）。只有 blocking 阻断稳定化。 */
export const G3_SEVERITIES = ['blocking', 'warning', 'info'] as const
export type G3Severity = (typeof G3_SEVERITIES)[number]

/** 严重度释义（`GLOSSARY.md:46-50`）。 */
export const G3_SEVERITY_DESCRIPTIONS: Record<G3Severity, string> = {
  'blocking': '阻断影响：放着不处理就阻断稳定化，只能提交真实闭环证据完成，不能被责任人接受掉。',
  'warning': '放着不处理会有后果，但不等同阻断稳定化。',
  'info': '供追溯与提示，不阻断稳定化。',
}

/** 责任域三域（`GLOSSARY.md:43`）。责任域不是严重度。 */
export const G3_RESPONSIBILITY_DOMAINS = ['A', 'B', 'C'] as const
export type G3ResponsibilityDomain = (typeof G3_RESPONSIBILITY_DOMAINS)[number]

/** 责任域释义（`GLOSSARY.md:43-44`）。 */
export const G3_DOMAIN_DESCRIPTIONS: Record<G3ResponsibilityDomain, string> = {
  'A': 'A 域拥有资料与证据状态（证据卡、资料锚点）。',
  'B': 'B 域拥有确认、项目与任务状态（研究条件、主张、章节确认、变更集、上下文修订、影响任务）。',
  'C': 'C 域拥有规则与交付（模板规则、路线节点的交付表示、预检、交付快照、导出）。',
}

/** 影响任务的状态词表（`internal/lingdoc/workspacecore/impact_model.go:144-148`）。 */
export const IMPACT_TASK_STATUSES = [
  'OPEN',
  'IN_PROGRESS',
  'WAITING_USER',
  'DONE',
  'DISMISSED',
  'STALE',
] as const

export type G3ImpactTaskStatus = (typeof IMPACT_TASK_STATUSES)[number]

export function isImpactTaskStatus(value: unknown): value is G3ImpactTaskStatus {
  return typeof value === 'string' && (IMPACT_TASK_STATUSES as readonly string[]).includes(value)
}

/**
 * 影响解析结果的动作——它们出现在 `impacts[].action` 上，不是任务状态。
 * ADR 0003：NO_ACTION 是影响解析结果，不是任务处置状态。
 */
const IMPACT_RESOLUTION_RESULTS = ['NO_ACTION'] as const

export function isImpactResolutionResult(value: unknown): boolean {
  return typeof value === 'string' && (IMPACT_RESOLUTION_RESULTS as readonly string[]).includes(value)
}

/**
 * 动作到严重度的推导（ADR 0003 第八节）。
 * 词表外的一律 blocking —— 认不出的动作不能因为「不认识」就被放成 info。
 */
const ACTION_SEVERITY: Record<string, G3Severity> = {
  'BLOCK_RELEASE': 'blocking',
  'REVIEW_REQUIRED': 'blocking',
  'INVALIDATE_CONFIRMATION': 'blocking',
  'REVALIDATE_EVIDENCE': 'warning',
  'RETRIEVE_CANDIDATES': 'warning',
  'REGENERATE': 'warning',
  'RECOMPUTE': 'info',
  'NO_ACTION': 'info',
}

export function severityOfAction(action: string): G3Severity {
  return ACTION_SEVERITY[action] ?? 'blocking'
}
