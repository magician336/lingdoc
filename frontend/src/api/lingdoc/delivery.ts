import { get, getDown, post } from '@/utils/request'
import type { Result } from './workspace'

// T13（检查与冻结）与 T14（导出与下载）的接口层。
//
// 八个操作分两组，各自对应契约里的一族：检查与冻结四个（checkCurrent /
// prepareRelease / listReleases / getRelease），导出四个（startExport /
// listExports / getExport / downloadExport）。

export type CheckStatus = 'passed' | 'blocked' | 'not_evaluated'
export type IssueSeverity = 'blocking' | 'warning'

/** 契约 §3 的 ValidationIssue。target_id 是「阻断原因可定位」的抓手。 */
export interface ValidationIssue {
  id: string
  rule_id: string
  ruleset_hash: string
  severity: IssueSeverity
  /**
   * 章节 ID、项目 ID，或 unresolved-project（一条项目级、却没有项目 ID 可指的发现）。
   * 后端保证非空——空目标会把这条缺陷从调用方眼前藏起来。
   */
  target_id: string
  target_version: string | null
  message: string
}

export interface CheckResult {
  project_version: number
  ruleset_hash: string
  status: CheckStatus
  issues: ValidationIssue[]
}

/**
 * 冻结下来的输入快照。契约里的完整形状是 `FrozenDeliveryInput`（还有 spec、template、
 * asset_versions、policy_asset_ids、delivery_kind）。
 *
 * 这里只声明界面要读的那几个字段：交付面板显示的是**冻结时冻下的数字**，而「冻结后
 * 改了章节，这几个数不变」正是「冻结后内容变化不改变快照」看得见的样子。其余字段由
 * 后端渲染时使用，界面不解释它们——需要时按契约那个名字去补，别在这里猜。
 */
export interface FrozenInput {
  project_id: string
  project_name: string
  project_version: number
  spec_revision: number
  chapters: Array<{ chapter_id: string; title: string }>
  sources: unknown[]
}

export interface ReleaseSnapshot {
  id: string
  project_id: string
  frozen_input: FrozenInput
  snapshot_digest: string
  check: CheckResult
  /**
   * 由查询时的当前项目/资料状态算出，**不是**冻结时存下的那个值。所以它只在这一次
   * 响应里有效，不能缓存下来当成快照的属性。
   *
   * 尤其：**交付面板不得拿这个字段渲染当前性标签**（列表响应里的那一份也不行），
   * 哪怕服务端已经逐条重算过。§7 要求界面按 snapshot_id 调 getRelease，依据**那次
   * 读取**的 is_current 显示当前/历史——要展示或要下载的那一条必须来自它自己那次读取。
   * 字段留着是因为它随响应交付，且是判断这份快照「此刻还代表不代表工作区」的唯一依据。
   */
  is_current: boolean
  created_at: string
}

export type ExportStatus = 'queued' | 'verified' | 'failed'

export interface ExportFailure {
  code: string
  message: string
  retryable: boolean
}

export interface ExportArtifact {
  id: string
  project_id: string
  snapshot_id: string
  status: ExportStatus
  /** required + nullable：文件做出来了才有值。 */
  file_sha256: string | null
  /** required + nullable：同上。只在 verified 时是一个能取到文件的地址。 */
  download_path: string | null
  error?: ExportFailure
}

export interface ReleaseHistory {
  items: ReleaseSnapshot[]
  /** 契约 §3：设了上限就必须显式提示，界面不能把截断后的列表当成全部历史。 */
  truncated: boolean
}

export interface ExportHistory {
  items: ExportArtifact[]
  truncated: boolean
}

const base = '/api/v1/lingdoc/projects'
const segment = (id: string) => encodeURIComponent(id)
const keyHeader = (key: string) => ({ headers: { 'Idempotency-Key': key } })

export const checkDelivery = (projectId: string, expectedProjectVersion: number) =>
  post<Result<CheckResult>>(`${base}/${segment(projectId)}/checks`,
    { expected_project_version: expectedProjectVersion })

export const prepareRelease = (projectId: string, expectedProjectVersion: number, key: string) =>
  post<Result<ReleaseSnapshot>>(`${base}/${segment(projectId)}/releases`,
    { expected_project_version: expectedProjectVersion }, keyHeader(key))

export const listReleases = (projectId: string) =>
  get<Result<ReleaseHistory>>(`${base}/${segment(projectId)}/releases`)

export const getRelease = (projectId: string, snapshotId: string) =>
  get<Result<ReleaseSnapshot>>(`${base}/${segment(projectId)}/releases/${segment(snapshotId)}`)

export const startExport = (projectId: string, snapshotId: string, key: string) =>
  post<Result<ExportArtifact>>(`${base}/${segment(projectId)}/releases/${segment(snapshotId)}/exports`,
    { format: 'docx' }, keyHeader(key))

export const listExports = (projectId: string) =>
  get<Result<ExportHistory>>(`${base}/${segment(projectId)}/exports`)

export const getExport = (projectId: string, exportId: string) =>
  get<Result<ExportArtifact>>(`${base}/${segment(projectId)}/exports/${segment(exportId)}`)

/**
 * 下载一份产物。传的是响应里给的那个 download_path（服务端发的绝对路径），不是自己
 * 拼的地址——发出去的地址取不到文件的那天，问题应该出在服务端而不是在这里被掩盖。
 *
 * 不走 get/post 而走 getDown：那是这条接口里唯一一条不返回 JSON 的响应。它带的是同一个
 * axios 实例，所以 Authorization 照旧自动带上；失败响应会被拦截器还原成 JSON，于是
 * catch 里的 error.message 是后端那句中文。
 *
 * **每次点下载都真发一次请求**——后端在那一刻重查授权，这就是「下载仍单独鉴权」在前端
 * 侧的全部含义。所以这里不缓存 Blob，也不接受缓存。
 */
export const downloadExport = (downloadPath: string): Promise<Blob> => getDown(downloadPath)

/**
 * 一次交付动作的幂等键。
 *
 * 与 candidateAdoption 的那个分开，是因为它的前缀写死在函数名通用的实现里；交付的键
 * 带上 candidate-adoption- 前缀，会在排查时指向一个错误的域。三行工具不值得为它引
 * 一层跨模块共享。
 */
export function newDeliveryKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `lingdoc-delivery-${crypto.randomUUID()}`
  }
  return `lingdoc-delivery-${Date.now()}-${Math.random().toString(36).slice(2)}`
}
