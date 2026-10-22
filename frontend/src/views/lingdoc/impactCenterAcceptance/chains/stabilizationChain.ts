/**
 * G3 验收台-5 · 稳定化与交付闭环执行器。
 *
 * 这一层在 `chains/dependencyChain.ts`（依赖图与影响任务）之上，把
 * 「稳定化 → 交付检查 → 冻结快照 → 导出 → 下载核对」跑成真实调用。
 *
 * 写死的硬约束（不靠人记）：
 *
 * 1. **稳定化是判定，不是错误**：`POST /change-sets/{id}/stabilize` 的请求体**严格是 `{}`**；
 *    阻断态（`status=BLOCKED`）同样以 **200 成功响应**返回，`blockers` 与 `stale_task_ids`
 *    原样保留——页面必须展示完整阻断原因，而不是折叠成一句失败。
 * 2. **稳定化的前置**：调用者是 Owner、变更集状态 ∈ {applied, stabilized}、
 *    且目标修订 == 项目当前修订；否则 409（非 Owner → 404）。
 * 3. **交付检查与模板检查不带 `Idempotency-Key`**（契约未标这两个操作）；
 *    冻结、导出必须带。
 * 4. **判定导出成功只看产物的最终状态与 `file_sha256`**（`verified` 才算成功），
 *    **不看受理接口的 202**——202 只表示已受理，渲染失败会落成 `failed`。
 * 5. **下载用响应里的 `download_path` 绝对路径**，不自己拼前缀；下载后按**字节与哈希**核对。
 * 6. **列表接口的形状**：`GET /projects/{id}/releases` 与 `GET /projects/{id}/exports`
 *    的 `data` 是 `{items:[...], truncated}`，**不是数组**。
 * 7. **闭环证据按动作类型不同**：`RECOMPUTE` 要当前交付检查的真实回执编号
 *    （`ComputationReceipt`），`RETRIEVE_CANDIDATES` 要真实调研任务回执编号（`ResearchRun`，
 *    且至少两轮检索），其余动作用当前章节版本（`ChapterVersion`）。
 * 8. **导出正文标题必须 2–4 级**：一级标题会让导出落成 `failed`。
 *
 * 步骤名一律以「稳定化-」开头：它们是矩阵规则表（`rowEvidenceRules.ts`）的锚，
 * 也是「本轮跑到了哪些步骤」的记录。**不与前几票的步骤名重名**——
 * 否则本轮的步骤会顺手把别的行也判成已覆盖。
 */

import {
  envelopeData,
  errorCodeOf,
  type G3HttpClient,
} from './httpClient'
import { makeIdempotencyKey, recordStep, runStep, type G3RunContext } from './runContext'
import {
  applyChangeSet,
  confirmChapter,
  createChangeSet,
  listContextRevisions,
  readChangeSet,
  readProjectState,
  writeChapterVersion,
  type G3ChangeSetFact,
  type G3ContextRevisionFact,
  type G3ProjectState,
  type G3Verdict,
} from './changeSetChain'
import {
  actOnImpactTask,
  createDependencyEdge,
  listImpactTaskDetails,
  type G3ImpactTaskDetailFact,
  type G3ObjectVersionRef,
  type G3TaskTransition,
} from './dependencyChain'
import { isClientRejection } from './factReaders'

/**
 * 本链的步骤名。
 *
 * 它们是**矩阵规则表的锚**（见 `rowEvidenceRules.ts` 的 `G3_ROW_EVIDENCE_RULES`）：
 * 名字对不上时，矩阵那几行会退回「未被本轮覆盖」，而不是给出假的 `PASS`。
 */
export const G3_STAB_STEPS = {
  readProject: '稳定化-读项目状态',
  writeChapter: '稳定化-写入章节正文',
  confirmChapter: '稳定化-确认章节',
  createEdge: '稳定化-建立依赖边',
  createChangeSet: '稳定化-创建变更集',
  createConcurrentChangeSet: '稳定化-创建并发变更集',
  assess: '稳定化-评估变更集',
  readAssessment: '稳定化-读影响评估',
  apply: '稳定化-应用变更集',
  applyStale: '稳定化-应用过期基线变更集',
  readStale: '稳定化-读回过期变更集',
  readRevisions: '稳定化-读上下文修订',
  readTasks: '稳定化-读影响任务',
  stabilizeBlocked: '稳定化-稳定化（阻断态）',
  freezeBeforeStable: '稳定化-稳定化前冻结（预期被拒）',
  rewriteChapter: '稳定化-复写章节正文',
  reconfirm: '稳定化-重新确认章节',
  assignTask: '稳定化-指派影响任务',
  startTask: '稳定化-开始处置影响任务',
  completeTask: '稳定化-完成影响任务',
  deliveryCheck: '稳定化-交付检查',
  templateCheck: '稳定化-模板检查',
  readReceipt: '稳定化-读交付检查回执',
  stabilize: '稳定化-稳定化',
  stabilizeAfterReceipt: '稳定化-稳定化（交付回执闭环后）',
  freeze: '稳定化-冻结交付快照',
  listReleases: '稳定化-读交付快照列表',
  startExport: '稳定化-发起导出',
  pollExport: '稳定化-轮询导出产物',
  download: '稳定化-下载导出产物',
} as const

/** 导出前的可操作提示：标题层级是交付渲染器的硬约束。 */
export const DOCX_BODY_HEADING_HINT =
  '导出正文标题必须是 2–4 级（## / ### / ####）：一级标题会让导出落成 failed（unsupported_format）。'

/** 闭环证据的对象类型。 */
export type G3ClosureEvidenceKind = 'ComputationReceipt' | 'ResearchRun' | 'ChapterVersion'

/**
 * 闭环证据按动作类型取哪种对象。
 *
 * - `RECOMPUTE`：当前交付检查的**真实回执编号**（`ComputationReceipt`）。
 * - `RETRIEVE_CANDIDATES`：真实调研任务回执（`ResearchRun`，服务端要求至少两轮检索）。
 * - 其余：当前章节版本（`ChapterVersion`）。
 */
export function closureEvidenceKindForAction(action: string): G3ClosureEvidenceKind {
  if (action === 'RECOMPUTE') return 'ComputationReceipt'
  if (action === 'RETRIEVE_CANDIDATES') return 'ResearchRun'
  return 'ChapterVersion'
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function num(value: unknown): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

function objectOf(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

function projectPath(projectId: string, suffix: string): string {
  return `/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}${suffix}`
}

/** 一条阻断原因。 */
export interface G3StabilizationBlocker {
  code: string
  targetId: string
  reason: string
}

/** 稳定化判定结果。 */
export interface G3StabilizationFact {
  changeSetId: string
  contextRevision: number
  /** `STABILIZED` 或 `BLOCKED`。 */
  status: string
  blockers: G3StabilizationBlocker[]
  staleTaskIds: string[]
  checkedAt: string
}

/** 读稳定化结果。阻断原因**原样保留**。 */
export function readStabilizationFact(data: unknown): G3StabilizationFact {
  const raw = objectOf(data)
  return {
    changeSetId: str(raw.change_set_id),
    contextRevision: num(raw.context_revision),
    status: str(raw.status),
    blockers: (Array.isArray(raw.blockers) ? raw.blockers : []).map((item) => {
      const blocker = objectOf(item)
      return {
        code: str(blocker.code),
        targetId: str(blocker.target_id),
        reason: str(blocker.reason),
      }
    }),
    staleTaskIds: (Array.isArray(raw.stale_task_ids) ? raw.stale_task_ids : []).map((item) => str(item)),
    checkedAt: str(raw.checked_at),
  }
}

/** 是不是「已稳定」。 */
export function isStabilizedFact(fact: G3StabilizationFact): boolean {
  return fact.status === 'STABILIZED'
}

/**
 * 阻断原因的可读行。
 *
 * 页面照这个渲染：**码、对象、原因**三样都要在，不能只说一句「稳定化失败」。
 */
export function blockerLines(fact: G3StabilizationFact): string[] {
  return fact.blockers.map(
    (blocker) =>
      `${blocker.code || '（服务端未给码）'}｜对象 ${blocker.targetId || '（未指明）'}｜原因 ${blocker.reason || '（服务端未给原因）'}`,
  )
}

/** 交付检查里的一条问题。 */
export interface G3CheckIssueFact {
  ruleId: string
  severity: string
  targetId: string
  message: string
}

/** 交付检查结果。 */
export interface G3CheckFact {
  receiptId: string
  status: string
  projectVersion: number
  rulesetHash: string
  issues: G3CheckIssueFact[]
}

/** 读交付检查结果。 */
export function readCheckFact(data: unknown): G3CheckFact {
  const raw = objectOf(data)
  return {
    receiptId: str(raw.receipt_id),
    status: str(raw.status),
    projectVersion: num(raw.project_version),
    rulesetHash: str(raw.ruleset_hash),
    issues: (Array.isArray(raw.issues) ? raw.issues : []).map((item) => {
      const issue = objectOf(item)
      return {
        ruleId: str(issue.rule_id),
        severity: str(issue.severity),
        targetId: str(issue.target_id),
        message: str(issue.message),
      }
    }),
  }
}

/** 模板检查结果（信息性）。 */
export interface G3TemplateCheckFact {
  status: string
  evaluationCount: number
  issueCount: number
}

/** 读模板检查结果。 */
export function readTemplateCheckFact(data: unknown): G3TemplateCheckFact {
  const raw = objectOf(data)
  const evaluations = Array.isArray(raw.evaluations) ? raw.evaluations : []
  const issues = Array.isArray(raw.issues) ? raw.issues : []
  return {
    status: str(raw.status),
    evaluationCount: evaluations.length,
    issueCount: issues.length,
  }
}

/** 交付检查回执（`ImpactComputationReceipt`）。 */
export interface G3ReceiptFact {
  id: string
  version: number
  status: string
  contextRevision: number
  projectVersion: number
}

/** 读交付检查回执。 */
export function readReceiptFact(data: unknown): G3ReceiptFact {
  const raw = objectOf(data)
  return {
    id: str(raw.id),
    version: num(raw.version),
    status: str(raw.status),
    contextRevision: num(raw.context_revision),
    projectVersion: num(raw.project_version),
  }
}

/** 冻结的交付快照。 */
export interface G3ReleaseSnapshotFact {
  id: string
  projectId: string
  snapshotDigest: string
  /** 快照里那份检查的结论：`passed` / `blocked` / `not_evaluated`。 */
  checkStatus: string
  isCurrent: boolean
  createdAt: string
}

/** 读冻结快照。 */
export function readReleaseSnapshotFact(data: unknown): G3ReleaseSnapshotFact {
  const raw = objectOf(data)
  const check = objectOf(raw.check)
  return {
    id: str(raw.id),
    projectId: str(raw.project_id),
    snapshotDigest: str(raw.snapshot_digest),
    checkStatus: str(check.status),
    isCurrent: raw.is_current === true,
    createdAt: str(raw.created_at),
  }
}

/** 导出产物。 */
export interface G3ExportArtifactFact {
  id: string
  projectId: string
  snapshotId: string
  /** `verified` / `failed`。 */
  status: string
  fileSha256: string
  downloadPath: string
  errorCode: string
  errorMessage: string
}

/** 读导出产物。 */
export function readExportArtifactFact(data: unknown): G3ExportArtifactFact {
  const raw = objectOf(data)
  const failure = objectOf(raw.error)
  return {
    id: str(raw.id),
    projectId: str(raw.project_id),
    snapshotId: str(raw.snapshot_id),
    status: str(raw.status),
    fileSha256: str(raw.file_sha256),
    downloadPath: str(raw.download_path),
    errorCode: str(failure.code),
    errorMessage: str(failure.message),
  }
}

/**
 * 判定「导出真的成功了吗」。
 *
 * **只看产物的最终状态与文件哈希**：受理接口的 202 只表示已受理。
 * 渲染失败会落成 `failed`，那时哪怕有哈希也不算成功。
 */
export function exportSucceeded(artifact: G3ExportArtifactFact): boolean {
  return artifact.status === 'verified' && artifact.fileSha256 !== ''
}

/** 下载核对结果。 */
export interface G3DownloadFact {
  /** 字节数。 */
  size: number
  /** 实测 sha256。 */
  sha256: string
  /** 产物上写的 sha256。 */
  expectedSha256: string
  /** 两者是否一致。 */
  matches: boolean
}

/** 计算 sha256（浏览器与 node 都用 WebCrypto）。 */
export async function sha256Hex(bytes: Uint8Array): Promise<string> {
  const subtle = globalThis.crypto?.subtle
  if (!subtle) {
    throw new Error('当前运行环境没有 WebCrypto（crypto.subtle），无法核对产物哈希。')
  }
  const digest = await subtle.digest('SHA-256', bytes as unknown as ArrayBuffer)
  return Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('')
}

/** 下载后按字节与哈希核对。 */
export async function verifyDownloadedArtifact(
  bytes: Uint8Array,
  expectedSha256: string,
): Promise<G3DownloadFact> {
  const sha256 = await sha256Hex(bytes)
  return {
    size: bytes.byteLength,
    sha256,
    expectedSha256,
    matches: expectedSha256 !== '' && sha256 === expectedSha256,
  }
}

/**
 * 导出前的正文体检：标题必须在 2–4 级。
 *
 * 页面在导出前用它给出**可操作**提示（哪一行是问题），而不是一句「格式不对」。
 */
export function deliveryBodyTitleCheck(bodyMarkdown: string): { ok: boolean; offending: string[] } {
  const offending: string[] = []
  for (const line of String(bodyMarkdown ?? '').split('\n')) {
    const match = /^(#{1,6})\s+(.*)$/.exec(line)
    if (!match) continue
    const level = match[1].length
    if (level < 2 || level > 4) {
      offending.push(line.trim())
    }
  }
  return { ok: offending.length === 0, offending }
}

/* ------------------------------------------------------------------ *
 * 单步调用
 * ------------------------------------------------------------------ */

/** 调用稳定化。请求体**严格是 `{}`**，阻断态同样是成功响应。 */
export async function stabilizeChangeSet(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
  options: { stepName: string; idemLabel: string },
): Promise<G3StabilizationFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}/stabilize`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    body: {},
    conclusion: '稳定化判定已返回（HTTP {status}）',
    detail: (body) => {
      const fact = readStabilizationFact(objectOf(body).data)
      return {
        change_set_id: fact.changeSetId,
        status: fact.status,
        context_revision: fact.contextRevision,
        checked_at: fact.checkedAt,
        blocker_count: fact.blockers.length,
        blockers: fact.blockers.map((blocker) => `${blocker.code}:${blocker.targetId}`),
        stale_task_ids: fact.staleTaskIds,
        error_code: errorCodeOf(body),
      }
    },
  })
  return readStabilizationFact(envelopeData(data))
}

/** 交付检查。**不带 `Idempotency-Key`**（契约未标这个操作）。 */
export async function runDeliveryCheck(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  expectedProjectVersion: number,
  stepName: string,
): Promise<G3CheckFact> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'POST',
    path: projectPath(projectId, '/checks'),
    token,
    body: { expected_project_version: expectedProjectVersion },
    conclusion: '交付检查已完成（HTTP {status}）',
    detail: (body) => {
      const fact = readCheckFact(objectOf(body).data)
      return {
        receipt_id: fact.receiptId,
        status: fact.status,
        project_version: fact.projectVersion,
        issue_count: fact.issues.length,
        issues: fact.issues.map((issue) => `${issue.ruleId}:${issue.severity}`),
      }
    },
  })
  return readCheckFact(envelopeData(data))
}

/** 模板检查。同样**不带幂等键**。 */
export async function runTemplateCheck(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  expectedProjectVersion: number,
  stepName: string,
): Promise<G3TemplateCheckFact> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'POST',
    path: projectPath(projectId, '/template-checks'),
    token,
    body: { expected_project_version: expectedProjectVersion },
    conclusion: '模板检查已完成（HTTP {status}）',
    detail: (body) => {
      const fact = readTemplateCheckFact(objectOf(body).data)
      return { status: fact.status, evaluation_count: fact.evaluationCount, issue_count: fact.issueCount }
    },
  })
  return readTemplateCheckFact(envelopeData(data))
}

/** 读交付检查回执。 */
export async function readCheckReceipt(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  receiptId: string,
  stepName: string,
): Promise<G3ReceiptFact> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, `/check-receipts/${encodeURIComponent(receiptId)}`),
    token,
    conclusion: '交付检查回执已读回（HTTP {status}）',
    detail: (body) => {
      const fact = readReceiptFact(objectOf(body).data)
      return {
        receipt_id: fact.id,
        version: fact.version,
        status: fact.status,
        context_revision: fact.contextRevision,
        project_version: fact.projectVersion,
      }
    },
  })
  return readReceiptFact(envelopeData(data))
}

/** 冻结交付快照。要幂等键 + `expected_project_version`。 */
export async function freezeRelease(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  expectedProjectVersion: number,
  options: { stepName: string; idemLabel: string; expectedStatus?: number | readonly number[] },
): Promise<G3ReleaseSnapshotFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, '/releases'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    expectedStatus: options.expectedStatus,
    body: { expected_project_version: expectedProjectVersion },
    conclusion: '交付快照已冻结（HTTP {status}）',
    detail: (body) => {
      const fact = readReleaseSnapshotFact(objectOf(body).data)
      return {
        snapshot_id: fact.id,
        check_status: fact.checkStatus,
        is_current: fact.isCurrent,
        snapshot_digest: fact.snapshotDigest,
        error_code: errorCodeOf(body),
      }
    },
  })
  return readReleaseSnapshotFact(envelopeData(data))
}

/** 读冻结快照列表。`data` 是 `{items, truncated}`，**不是数组**。 */
export async function listReleaseSnapshots(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string,
): Promise<{ items: G3ReleaseSnapshotFact[]; truncated: boolean }> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, '/releases'),
    token,
    conclusion: '交付快照列表已读回（HTTP {status}）',
    detail: (body) => {
      const raw = objectOf(objectOf(body).data)
      const items = Array.isArray(raw.items) ? (raw.items as unknown[]) : []
      return {
        item_count: items.length,
        truncated: raw.truncated === true,
        check_statuses: items.map((item) => str(objectOf(objectOf(item).check).status)),
      }
    },
  })
  const raw = objectOf(envelopeData(data))
  const items = Array.isArray(raw.items) ? (raw.items as unknown[]) : []
  return {
    items: items.map(readReleaseSnapshotFact),
    truncated: raw.truncated === true,
  }
}

/** 发起导出。请求体只有 `format`，返回 **202**（只表示已受理）。 */
export async function startExport(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  snapshotId: string,
  options: { stepName: string; idemLabel: string },
): Promise<{ status: number; artifact: G3ExportArtifactFact }> {
  const { response, data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/releases/${encodeURIComponent(snapshotId)}/exports`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    expectedStatus: 202,
    body: { format: 'docx' },
    conclusion: '导出已被受理（HTTP {status}：受理不等于成功，最终以产物状态与哈希为准）',
    detail: (body) => {
      const fact = readExportArtifactFact(objectOf(body).data)
      return {
        export_id: fact.id,
        status: fact.status,
        file_sha256: fact.fileSha256,
        download_path: fact.downloadPath,
        error_code: errorCodeOf(body),
      }
    },
  })
  return { status: response.status, artifact: readExportArtifactFact(envelopeData(data)) }
}

/** 读一份导出产物的状态。 */
export async function readExportArtifact(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  exportId: string,
  stepName: string,
): Promise<G3ExportArtifactFact> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, `/exports/${encodeURIComponent(exportId)}`),
    token,
    conclusion: '导出产物状态已读回（HTTP {status}）',
    detail: (body) => {
      const fact = readExportArtifactFact(objectOf(body).data)
      return {
        export_id: fact.id,
        status: fact.status,
        file_sha256: fact.fileSha256,
        download_path: fact.downloadPath,
        error_code: fact.errorCode,
      }
    },
  })
  return readExportArtifactFact(envelopeData(data))
}

/** 下载产物字节。用响应里的 `download_path` 绝对路径，不自己拼前缀。 */
export async function downloadExportBytes(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  downloadPath: string,
  stepName: string,
): Promise<Uint8Array> {
  const started = Date.now()
  const response = await client({
    method: 'GET',
    path: downloadPath,
    token,
    raw: true,
  })
  const bytes = response.bytes ?? new Uint8Array()
  recordStep(ctx, {
    name: stepName,
    method: 'GET',
    path: downloadPath,
    status: response.status,
    ok: response.ok && bytes.byteLength > 0,
    durationMs: Date.now() - started,
    conclusion: response.ok
      ? `产物已下载（HTTP ${response.status}）：${bytes.byteLength} 字节`
      : `产物下载失败（HTTP ${response.status}）`,
    details: { byte_length: bytes.byteLength },
    error: response.ok ? undefined : '产物下载失败',
  })
  if (!response.ok) {
    throw new Error(`产物下载失败（HTTP ${response.status}）`)
  }
  return bytes
}

/* ------------------------------------------------------------------ *
 * 链
 * ------------------------------------------------------------------ */

/** 链的输入。 */
export interface G3StabilizationChainOptions {
  projectId: string
  /** 模板激活后自动创建的两个章节。 */
  chapters: { id: string; sectionId: string; title: string }[]
  /** 当前操作者（指派与闭环都由他执行）。 */
  operatorId: string
  /** 轮询产物的最大次数与间隔。 */
  pollAttempts?: number
  pollIntervalMs?: number
}

/** 稳定化阶段的结果。 */
export interface G3StabilizationPhaseResult {
  changeSet: G3ChangeSetFact
  concurrentChangeSet: G3ChangeSetFact
  /** 应用过期基线变更集时的服务端回应。 */
  staleApply: { status: number; code: string }
  contextRevisionsBefore: number
  contextRevisionsAfter: number
  tasksBeforeStaleApply: number
  tasksAfterStaleApply: number
  /** 阻断态的稳定化结果（200 成功响应）。 */
  blocked: G3StabilizationFact
  /** 稳定化前冻结的尝试（预期被拒）。 */
  freezeBeforeStable: { status: number; code: string }
  /** 全部阻断影响闭环后的稳定化结果。 */
  stabilized: G3StabilizationFact
  transitions: G3TaskTransition[]
}

/** 交付闭环阶段的结果。 */
export interface G3DeliveryClosureResult {
  check: G3CheckFact
  templateCheck: G3TemplateCheckFact
  receipt: G3ReceiptFact
  /** 用交付检查回执闭环的重算类影响任务的处置过程。 */
  receiptTransitions: G3TaskTransition[]
  /** 交付回执闭环之后再次稳定化的结果。 */
  stabilizedAfterReceipt: G3StabilizationFact | null
  snapshot: G3ReleaseSnapshotFact
  releaseList: { count: number; truncated: boolean }
  exportAccepted: { status: number }
  exportArtifact: G3ExportArtifactFact
  pollStatuses: string[]
  download: G3DownloadFact
}

/** 整条链的结果。 */
export interface G3StabilizationChainResult extends G3StabilizationPhaseResult, G3DeliveryClosureResult {
  verdicts: G3Verdict[]
}

/** 链跑失败时保留已经拿到的证据。 */
export class G3StabilizationChainError extends Error {
  readonly partial: Partial<G3StabilizationChainResult>

  constructor(message: string, partial: Partial<G3StabilizationChainResult>) {
    super(message)
    this.name = 'G3StabilizationChainError'
    this.partial = partial
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

/** 评估变更集。请求体**严格是 `{}`**（契约要求这个端点不读业务字段）。 */
async function assessChangeSetStrict(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
  stepName: string,
): Promise<void> {
  await runStep(ctx, client, {
    name: stepName,
    method: 'POST',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}/assess`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `stab-assess-${changeSetId}`),
    body: {},
    conclusion: '变更集已评估（HTTP {status}）',
    detail: (body) => ({
      change_set_id: str(objectOf(objectOf(body).data).id),
      status: str(objectOf(objectOf(body).data).status),
    }),
  })
}

/**
 * 跑「变更集 → 影响任务 → 稳定化」这一段。
 *
 * 顺序是**状态机的前置条件**：评估必须早于应用，闭环必须早于稳定化，
 * 稳定化必须早于冻结。
 */
export async function runStabilizationPhase(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: G3StabilizationChainOptions,
  progress?: Partial<G3StabilizationPhaseResult>,
): Promise<G3StabilizationPhaseResult> {
  const question = options.chapters.find((chapter) => chapter.sectionId === 'question')
  const method = options.chapters.find((chapter) => chapter.sectionId === 'method')
  if (!question || !method) {
    throw new Error('稳定化链需要模板激活后的 question / method 两个章节')
  }
  if (!options.operatorId) {
    throw new Error('稳定化链需要当前操作者（用于指派影响任务）')
  }

  const project: G3ProjectState = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    G3_STAB_STEPS.readProject,
  )

  // —— 前置：两个章节都要有正文且已确认，ChapterVersion 才是可用的依赖对象 ——
  const chapterVersions: Record<string, string> = {}
  for (const chapter of [question, method]) {
    const written = await writeChapterVersion(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_STAB_STEPS.writeChapter,
      idemLabel: `stab-chapter-body-${chapter.sectionId}`,
      expectedChapterVersionId: null,
      expectedSpecRevision: project.specRevision,
      bodyMarkdown: `## ${chapter.title}\n\nG3 验收台-5 稳定化与交付闭环演示正文（运行 ${ctx.runId}）。`,
    })
    if (!written.currentVersionId) {
      throw new Error(`章节 ${chapter.sectionId} 保存正文后没有拿到当前版本`)
    }
    chapterVersions[chapter.sectionId] = written.currentVersionId
    await confirmChapter(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_STAB_STEPS.confirmChapter,
      idemLabel: `stab-confirm-${chapter.sectionId}`,
      expectedChapterVersionId: written.currentVersionId,
      expectedSpecRevision: project.specRevision,
    })
  }

  const specRef: G3ObjectVersionRef = {
    kind: 'ProjectSpec',
    id: options.projectId,
    version: String(project.specRevision),
  }
  const templateRef: G3ObjectVersionRef = {
    kind: 'TemplateCopy',
    id: project.templateId ?? '',
    version: String(project.templateCopyVersion ?? 0),
  }
  const methodRef: G3ObjectVersionRef = {
    kind: 'ChapterVersion',
    id: method.id,
    version: chapterVersions.method,
  }
  const questionRef: G3ObjectVersionRef = {
    kind: 'ChapterVersion',
    id: question.id,
    version: chapterVersions.question,
  }

  // —— 依赖边：两条**阻断**影响（方法章节确认失效、研究问题需人工复核）
  //    与一条**重算**影响（用交付检查回执闭环）。——
  await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_STAB_STEPS.createEdge,
    idemLabel: 'stab-edge-spec-method',
    expectedContextRevision: project.currentContextRevision,
    source: specRef,
    target: methodRef,
    relation: 'spec_invalidates_method',
    action: 'INVALIDATE_CONFIRMATION',
    origin: 'user',
    reason: '研究条件变化使研究方案章节的确认失效',
    confidence: 1,
  })
  await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_STAB_STEPS.createEdge,
    idemLabel: 'stab-edge-spec-question',
    expectedContextRevision: project.currentContextRevision,
    source: specRef,
    target: questionRef,
    relation: 'spec_requires_question_review',
    action: 'REVIEW_REQUIRED',
    origin: 'user',
    reason: '研究条件变化后研究问题需要人工复核',
    confidence: 1,
  })
  await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_STAB_STEPS.createEdge,
    idemLabel: 'stab-edge-spec-template',
    expectedContextRevision: project.currentContextRevision,
    source: specRef,
    target: templateRef,
    relation: 'spec_requires_recompute',
    action: 'RECOMPUTE',
    origin: 'user',
    reason: '研究条件变化后交付结论需要按当前规则重算',
    confidence: 1,
  })

  // —— 两个**同一基线**的变更集：先应用的那个推进修订，后应用的那个必须被拒 ——
  const changeSet = await createChangeSet(ctx, client, token, options.projectId, {
    stepName: G3_STAB_STEPS.createChangeSet,
    idemLabel: 'stab-change-set',
    expectedContextRevision: project.currentContextRevision,
    fields: {
      research_goal: {
        old_value: project.spec.research_goal ?? '',
        new_value: `${project.spec.research_goal ?? ''}（G3 验收台-5 稳定化）`,
      },
    },
    affectedChapterIds: [method.id],
    reason: 'G3 验收台-5：研究条件变化，验证稳定化与交付闭环',
  })
  const concurrent = await createChangeSet(ctx, client, token, options.projectId, {
    stepName: G3_STAB_STEPS.createConcurrentChangeSet,
    idemLabel: 'stab-change-set-concurrent',
    expectedContextRevision: project.currentContextRevision,
    fields: {
      research_subject: {
        old_value: project.spec.research_subject ?? '',
        new_value: `${project.spec.research_subject ?? ''}（G3 验收台-5 并发探针）`,
      },
    },
    affectedChapterIds: [question.id],
    reason: 'G3 验收台-5：与主变更集同一基线，用于验证旧基线不能应用',
  })

  await assessChangeSetStrict(ctx, client, token, options.projectId, changeSet.id, G3_STAB_STEPS.assess)
  await runStep(ctx, client, {
    name: G3_STAB_STEPS.readAssessment,
    method: 'GET',
    path: projectPath(options.projectId, `/change-sets/${encodeURIComponent(changeSet.id)}/impact-assessment`),
    token,
    conclusion: '影响评估已读回（HTTP {status}）',
    detail: () => ({ change_set_id: changeSet.id }),
  })

  await applyChangeSet(ctx, client, token, options.projectId, changeSet.id, {
    stepName: G3_STAB_STEPS.apply,
    idemLabel: 'stab-apply',
  })

  const revisionsBefore = await listContextRevisions(
    ctx,
    client,
    token,
    options.projectId,
    G3_STAB_STEPS.readRevisions,
  )
  const tasksBefore = await listImpactTaskDetails(
    ctx,
    client,
    token,
    options.projectId,
    G3_STAB_STEPS.readTasks,
  )

  // —— 旧基线不能应用：被拒之后不能有半完成写入 ——
  const staleApply = await applyChangeSet(ctx, client, token, options.projectId, concurrent.id, {
    stepName: G3_STAB_STEPS.applyStale,
    idemLabel: 'stab-apply-stale',
    expectedStatus: 409,
  })
  const staleFact = await readChangeSet(
    ctx,
    client,
    token,
    options.projectId,
    concurrent.id,
    G3_STAB_STEPS.readStale,
  )
  const revisionsAfter = await listContextRevisions(
    ctx,
    client,
    token,
    options.projectId,
    G3_STAB_STEPS.readRevisions,
  )
  const tasksAfter = await listImpactTaskDetails(
    ctx,
    client,
    token,
    options.projectId,
    G3_STAB_STEPS.readTasks,
  )

  // —— 阻断态的稳定化：**200 成功响应**，阻断原因要完整 ——
  const blocked = await stabilizeChangeSet(ctx, client, token, options.projectId, changeSet.id, {
    stepName: G3_STAB_STEPS.stabilizeBlocked,
    idemLabel: 'stab-stabilize-blocked',
  })
  if (progress) progress.blocked = blocked

  // —— 稳定化之前不能冻结（未关闭阻断项不能导出）——
  const freezeProbe = await freezeRelease(ctx, client, token, options.projectId, project.projectVersion, {
    stepName: G3_STAB_STEPS.freezeBeforeStable,
    idemLabel: 'stab-freeze-before-stable',
    expectedStatus: [409, 422],
  })
  const freezeBeforeStable = {
    status: 0,
    code: '',
  }
  // 预期失败的那一步在 runStep 里记为成功，这里用步骤记录还原真实状态码。
  const probeStep = ctx.steps.find((step) => step.name === G3_STAB_STEPS.freezeBeforeStable)
  freezeBeforeStable.status = probeStep?.status ?? 0
  freezeBeforeStable.code = String(probeStep?.details?.error_code ?? '')
  if (progress) progress.freezeBeforeStable = freezeBeforeStable
  void freezeProbe

  // —— 闭环：复写受影响章节并按新的规格修订重新确认 ——
  const projectAfterApply = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    G3_STAB_STEPS.readProject,
  )
  // —— 闭环：先指派并开始处置（这两步针对**当前**版本），再复写并重新确认，
  //    最后以新版本作为闭环证据完成。顺序是状态机要求：任务按它提出的版本判定，
  //    章节版本被换掉之后再「开始」，服务端会把任务判为失效（终态但不算闭环）。——
  const transitions: G3TaskTransition[] = []
  const currentTasks = await listImpactTaskDetails(
    ctx,
    client,
    token,
    options.projectId,
    G3_STAB_STEPS.readTasks,
  )
  const blockingTasks = currentTasks.filter(
    (task) => task.severity === 'blocking' && task.status !== 'DONE' && task.status !== 'STALE',
  )
  const startedVersions: Record<string, number> = {}

  for (const task of blockingTasks) {
    const assigned = await actOnImpactTask(ctx, client, token, options.projectId, task.id, {
      stepName: G3_STAB_STEPS.assignTask,
      idemLabel: `stab-assign-${task.id}`,
      expectedVersion: task.version,
      targetRevision: task.targetRevision,
      action: 'assign',
      reason: 'G3 验收台-5：把影响任务指派给当前操作者',
      assigneeId: options.operatorId,
    })
    transitions.push({
      taskId: task.id,
      action: 'assign',
      severity: assigned.task?.severity ?? task.severity,
      from: task.status,
      to: assigned.task?.status ?? task.status,
      closureEvidence: [],
      note: `指派给当前操作者（assignee_id=${options.operatorId}）`,
      status: assigned.status,
      code: assigned.code,
    })

    const started = await actOnImpactTask(ctx, client, token, options.projectId, task.id, {
      stepName: G3_STAB_STEPS.startTask,
      idemLabel: `stab-start-${task.id}`,
      expectedVersion: assigned.task?.version ?? task.version + 1,
      targetRevision: task.targetRevision,
      action: 'start',
      reason: 'G3 验收台-5：开始处置影响任务',
    })
    startedVersions[task.id] = started.task?.version ?? task.version + 2
    transitions.push({
      taskId: task.id,
      action: 'start',
      severity: started.task?.severity ?? task.severity,
      from: assigned.task?.status ?? task.status,
      to: started.task?.status ?? task.status,
      closureEvidence: [],
      note: '责任人开始处置',
      status: started.status,
      code: started.code,
    })
  }

  // —— 复写受影响章节并按新的规格修订重新确认（闭环证据的来源）——
  const newVersions: Record<string, string> = {}
  for (const chapter of [method, question]) {
    const rewritten = await writeChapterVersion(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_STAB_STEPS.rewriteChapter,
      idemLabel: `stab-rewrite-${chapter.sectionId}`,
      expectedChapterVersionId: chapterVersions[chapter.sectionId],
      expectedSpecRevision: projectAfterApply.specRevision,
      bodyMarkdown: `## ${chapter.title}\n\nG3 验收台-5 变更后复写的正文（运行 ${ctx.runId}，规格修订 ${projectAfterApply.specRevision}）。`,
    })
    if (!rewritten.currentVersionId) {
      throw new Error(`复写章节 ${chapter.sectionId} 后没有拿到当前版本`)
    }
    newVersions[chapter.sectionId] = rewritten.currentVersionId
    await confirmChapter(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_STAB_STEPS.reconfirm,
      idemLabel: `stab-reconfirm-${chapter.sectionId}`,
      expectedChapterVersionId: rewritten.currentVersionId,
      expectedSpecRevision: projectAfterApply.specRevision,
    })
  }

  // —— 以闭环证据完成（全部**阻断**影响都要闭环）——
  for (const task of blockingTasks) {
    const sectionId = task.target.kind === 'ChapterVersion'
      ? options.chapters.find((chapter) => chapter.id === task.target.id)?.sectionId
      : undefined
    const version = sectionId ? newVersions[sectionId] : undefined
    if (!version) {
      throw new Error(
        `影响任务 ${task.id}（动作 ${task.action}）的目标不是本轮能重新确认的章节版本，无法造出闭环证据`,
      )
    }
    const closureRef: G3ObjectVersionRef = {
      kind: 'ChapterVersion',
      id: task.target.id,
      version,
    }
    const completed = await actOnImpactTask(ctx, client, token, options.projectId, task.id, {
      stepName: G3_STAB_STEPS.completeTask,
      idemLabel: `stab-complete-${task.id}`,
      expectedVersion: startedVersions[task.id] ?? task.version + 2,
      targetRevision: task.targetRevision,
      action: 'complete',
      reason: 'G3 验收台-5：已按当前规格修订复写并重新确认章节，提交闭环证据',
      closureEvidence: [closureRef],
    })
    transitions.push({
      taskId: task.id,
      action: 'complete',
      severity: completed.task?.severity ?? task.severity,
      from: 'IN_PROGRESS',
      to: completed.task?.status ?? task.status,
      closureEvidence: [closureRef],
      note: `以闭环证据完成：${closureRef.kind}/${closureRef.id}@${closureRef.version}`,
      status: completed.status,
      code: completed.code,
    })
  }

  // —— 全部阻断影响闭环后再稳定化：应当是「已稳定」——
  const stabilized = await stabilizeChangeSet(ctx, client, token, options.projectId, changeSet.id, {
    stepName: G3_STAB_STEPS.stabilize,
    idemLabel: 'stab-stabilize',
  })
  if (progress) progress.stabilized = stabilized

  return {
    changeSet,
    concurrentChangeSet: staleFact,
    staleApply: {
      status: probeStatusOf(ctx, G3_STAB_STEPS.applyStale),
      code: probeCodeOf(ctx, G3_STAB_STEPS.applyStale),
    },
    contextRevisionsBefore: revisionsBefore.length,
    contextRevisionsAfter: revisionsAfter.length,
    tasksBeforeStaleApply: tasksBefore.length,
    tasksAfterStaleApply: tasksAfter.length,
    blocked,
    freezeBeforeStable,
    stabilized,
    transitions,
  }
}

function probeStatusOf(ctx: G3RunContext, stepName: string): number {
  const steps = ctx.steps.filter((step) => step.name === stepName)
  return steps.length > 0 ? steps[steps.length - 1].status : 0
}

function probeCodeOf(ctx: G3RunContext, stepName: string): string {
  const steps = ctx.steps.filter((step) => step.name === stepName)
  return steps.length > 0 ? String(steps[steps.length - 1].details?.error_code ?? '') : ''
}

/** 跑「交付检查 → 冻结 → 导出 → 轮询 → 下载核对」这一段。 */
export async function runDeliveryClosure(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: {
    projectId: string
    snapshotId?: string
    expectedProjectVersion?: number
    changeSetId?: string
    /** 当前操作者（指派重算类影响任务时用）。 */
    operatorId?: string
    pollAttempts?: number
    pollIntervalMs?: number
  },
): Promise<G3DeliveryClosureResult> {
  const project = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    G3_STAB_STEPS.readProject,
  )
  const expectedProjectVersion = options.expectedProjectVersion ?? project.projectVersion

  // —— 交付检查（不带幂等键）：结论与真实回执编号 ——
  const check = await runDeliveryCheck(
    ctx,
    client,
    token,
    options.projectId,
    expectedProjectVersion,
    G3_STAB_STEPS.deliveryCheck,
  )
  const templateCheck = await runTemplateCheck(
    ctx,
    client,
    token,
    options.projectId,
    expectedProjectVersion,
    G3_STAB_STEPS.templateCheck,
  )
  const receipt = check.receiptId
    ? await readCheckReceipt(
      ctx,
      client,
      token,
      options.projectId,
      check.receiptId,
      G3_STAB_STEPS.readReceipt,
    )
    : { id: '', version: 0, status: '', contextRevision: 0, projectVersion: 0 }

  // —— 重算类影响用**当前交付检查的真实回执**闭环 ——
  const receiptTransitions: G3TaskTransition[] = []
  if (options.changeSetId && receipt.id) {
    const tasks = await listImpactTaskDetails(
      ctx,
      client,
      token,
      options.projectId,
      G3_STAB_STEPS.readTasks,
    )
    const recomputeTasks = tasks.filter(
      (task) => task.action === 'RECOMPUTE' && task.status !== 'DONE' && task.status !== 'STALE',
    )
    for (const task of recomputeTasks) {
      const closureRef: G3ObjectVersionRef = {
        kind: closureEvidenceKindForAction(task.action),
        id: receipt.id,
        version: String(receipt.version),
      }
      const assigned = await actOnImpactTask(ctx, client, token, options.projectId, task.id, {
        stepName: G3_STAB_STEPS.assignTask,
        idemLabel: `stab-assign-${task.id}`,
        expectedVersion: task.version,
        targetRevision: task.targetRevision,
        action: 'assign',
        reason: 'G3 验收台-5：把重算影响任务指派给当前操作者',
        assigneeId: options.operatorId,
      })
      receiptTransitions.push({
        taskId: task.id,
        action: 'assign',
        severity: assigned.task?.severity ?? task.severity,
        from: task.status,
        to: assigned.task?.status ?? task.status,
        closureEvidence: [],
        note: `指派给当前操作者（assignee_id=${options.operatorId}）`,
        status: assigned.status,
        code: assigned.code,
      })
      const started = await actOnImpactTask(ctx, client, token, options.projectId, task.id, {
        stepName: G3_STAB_STEPS.startTask,
        idemLabel: `stab-start-${task.id}`,
        expectedVersion: assigned.task?.version ?? task.version + 1,
        targetRevision: task.targetRevision,
        action: 'start',
        reason: 'G3 验收台-5：开始处置重算影响任务',
      })
      receiptTransitions.push({
        taskId: task.id,
        action: 'start',
        severity: started.task?.severity ?? task.severity,
        from: assigned.task?.status ?? task.status,
        to: started.task?.status ?? task.status,
        closureEvidence: [],
        note: '责任人开始处置',
        status: started.status,
        code: started.code,
      })
      const completed = await actOnImpactTask(ctx, client, token, options.projectId, task.id, {
        stepName: G3_STAB_STEPS.completeTask,
        idemLabel: `stab-complete-${task.id}`,
        expectedVersion: started.task?.version ?? task.version + 2,
        targetRevision: task.targetRevision,
        action: 'complete',
        reason: 'G3 验收台-5：以当前交付检查回执闭环重算影响',
        closureEvidence: [closureRef],
      })
      receiptTransitions.push({
        taskId: task.id,
        action: 'complete',
        severity: completed.task?.severity ?? task.severity,
        from: 'IN_PROGRESS',
        to: completed.task?.status ?? task.status,
        closureEvidence: [closureRef],
        note: `以交付检查回执闭环：${closureRef.kind}/${closureRef.id}@${closureRef.version}`,
        status: completed.status,
        code: completed.code,
      })
    }
  }

  // —— 交付回执闭环之后再稳定化一次：让服务端复核这份回执确实成立 ——
  const stabilizedAfterReceipt = options.changeSetId
    ? await stabilizeChangeSet(ctx, client, token, options.projectId, options.changeSetId, {
      stepName: G3_STAB_STEPS.stabilizeAfterReceipt,
      idemLabel: 'stab-stabilize-after-receipt',
    })
    : null

  // —— 冻结交付快照（要幂等键 + 版本）——
  const snapshot = await freezeRelease(
    ctx,
    client,
    token,
    options.projectId,
    expectedProjectVersion,
    { stepName: G3_STAB_STEPS.freeze, idemLabel: 'stab-freeze' },
  )
  const releaseList = await listReleaseSnapshots(
    ctx,
    client,
    token,
    options.projectId,
    G3_STAB_STEPS.listReleases,
  )

  // —— 发起导出（202 只表示已受理）——
  const accepted = await startExport(ctx, client, token, options.projectId, snapshot.id, {
    stepName: G3_STAB_STEPS.startExport,
    idemLabel: 'stab-export',
  })

  // —— 轮询产物：判定依据是产物的**最终状态与文件哈希** ——
  const attempts = options.pollAttempts ?? 5
  const interval = options.pollIntervalMs ?? 300
  const pollStatuses: string[] = [accepted.artifact.status]
  let artifact = accepted.artifact
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    const current = await readExportArtifact(
      ctx,
      client,
      token,
      options.projectId,
      artifact.id,
      G3_STAB_STEPS.pollExport,
    )
    artifact = current
    pollStatuses.push(current.status)
    if (current.status === 'verified' || current.status === 'failed') {
      break
    }
    await sleep(interval)
  }

  // —— 下载并按字节与哈希核对 ——
  const bytes = await downloadExportBytes(
    ctx,
    client,
    token,
    artifact.downloadPath,
    G3_STAB_STEPS.download,
  )
  const download = await verifyDownloadedArtifact(bytes, artifact.fileSha256)

  return {
    check,
    templateCheck,
    receipt,
    receiptTransitions,
    stabilizedAfterReceipt,
    snapshot,
    releaseList: { count: releaseList.items.length, truncated: releaseList.truncated },
    exportAccepted: { status: accepted.status },
    exportArtifact: artifact,
    pollStatuses,
    download,
  }
}

/**
 * 稳定化与交付闭环的页面快照。
 *
 * 只留页面要展示的字段，且全部可 JSON 序列化：它跟着运行摘要落进浏览器本地存储。
 */
export interface G3StabilizationSnapshot {
  verdicts: G3Verdict[]
  blocked: G3StabilizationFact
  blockedLines: string[]
  stabilized: G3StabilizationFact
  freezeBeforeStable: { status: number; code: string }
  staleApply: { status: number; code: string }
  contextRevisionsBefore: number
  contextRevisionsAfter: number
  tasksBeforeStaleApply: number
  tasksAfterStaleApply: number
  check: G3CheckFact
  templateCheck: G3TemplateCheckFact
  receipt: G3ReceiptFact
  /** 交付回执闭环之后再次稳定化的结果。 */
  stabilizedAfterReceipt: G3StabilizationFact | null
  snapshot: G3ReleaseSnapshotFact
  releaseList: { count: number; truncated: boolean }
  exportAccepted: { status: number }
  exportArtifact: G3ExportArtifactFact
  pollStatuses: string[]
  download: G3DownloadFact
  transitions: G3TaskTransition[]
}

/** 把链结果压成页面快照。 */
export function toStabilizationSnapshot(result: G3StabilizationChainResult): G3StabilizationSnapshot {
  return {
    verdicts: result.verdicts,
    blocked: result.blocked,
    blockedLines: blockerLines(result.blocked),
    stabilized: result.stabilized,
    freezeBeforeStable: result.freezeBeforeStable,
    staleApply: result.staleApply,
    contextRevisionsBefore: result.contextRevisionsBefore,
    contextRevisionsAfter: result.contextRevisionsAfter,
    tasksBeforeStaleApply: result.tasksBeforeStaleApply,
    tasksAfterStaleApply: result.tasksAfterStaleApply,
    check: result.check,
    templateCheck: result.templateCheck,
    receipt: result.receipt,
    stabilizedAfterReceipt: result.stabilizedAfterReceipt,
    snapshot: result.snapshot,
    releaseList: result.releaseList,
    exportAccepted: result.exportAccepted,
    exportArtifact: result.exportArtifact,
    pollStatuses: result.pollStatuses,
    download: result.download,
    transitions: result.transitions,
  }
}

/** 跑完整条「稳定化 → 交付闭环」链。 */
export async function runStabilizationChain(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: G3StabilizationChainOptions,
): Promise<G3StabilizationChainResult> {
  const partial: Partial<G3StabilizationChainResult> = {}
  const phaseProgress: Partial<G3StabilizationPhaseResult> = {}
  try {
    const phase = await runStabilizationPhase(ctx, client, token, options, phaseProgress)
    Object.assign(partial, phase)
    const closure = await runDeliveryClosure(ctx, client, token, {
      projectId: options.projectId,
      changeSetId: phase.changeSet.id,
      operatorId: options.operatorId,
      pollAttempts: options.pollAttempts,
      pollIntervalMs: options.pollIntervalMs,
    })
    Object.assign(partial, closure)

    const result: G3StabilizationChainResult = {
      ...phase,
      ...closure,
      transitions: [...phase.transitions, ...closure.receiptTransitions],
      verdicts: [],
    }
    result.verdicts = [verdictG31(result), verdictG36(result), verdictDeliveryClosure(result)]
    ctx.verdicts = [...(ctx.verdicts ?? []), ...result.verdicts]
    return result
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    Object.assign(partial, phaseProgress)
    throw new G3StabilizationChainError(message, partial)
  }
}

/* ------------------------------------------------------------------ *
 * 判定
 * ------------------------------------------------------------------ */

/** G3.1 判定：旧基线不能应用；应用无半完成写入。 */
export function verdictG31(result: G3StabilizationChainResult): G3Verdict {
  const rejected = result.staleApply.status === 409
  const staleStatus = result.concurrentChangeSet?.status ?? ''
  const noHalfWrite =
    result.contextRevisionsAfter === result.contextRevisionsBefore &&
    result.tasksAfterStaleApply === result.tasksBeforeStaleApply
  const holds = rejected && staleStatus === 'stale' && noHalfWrite
  return {
    id: 'G3.1',
    title: 'ChangeSet 切片：旧基线不能应用；应用无半完成写入',
    holds,
    detail: holds
      ? `同基线的第二个变更集在第一个应用后被服务端按 409 拒掉（code=${result.staleApply.code || '（未给码）'}），` +
        `该变更集落为 ${staleStatus}；被拒之后上下文修订条数仍为 ${result.contextRevisionsAfter}，` +
        `影响任务条数仍为 ${result.tasksAfterStaleApply}——没有半完成写入。`
      : `不成立：被拒状态码=${result.staleApply.status}，并发变更集状态=${staleStatus || '（未读到）'}，` +
        `上下文修订 ${result.contextRevisionsBefore}→${result.contextRevisionsAfter}，` +
        `影响任务 ${result.tasksBeforeStaleApply}→${result.tasksAfterStaleApply}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets → HTTP 201 · 两个变更集同一基线`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/apply（先应用）→ HTTP 200 · 上下文修订推进`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/apply（过期基线）→ HTTP ${result.staleApply.status} · ` +
        `code=${result.staleApply.code || '（未给码）'}`,
      `GET /api/v1/lingdoc/projects/{projectId}/change-sets/{id} → HTTP 200 · status=${staleStatus} · ` +
        `stale_reason=${result.concurrentChangeSet?.staleReason || '（服务端未给出）'}`,
      `GET /api/v1/lingdoc/projects/{projectId}/context-revisions → HTTP 200 · 修订条数 ${result.contextRevisionsBefore} → ${result.contextRevisionsAfter}`,
      `GET /api/v1/lingdoc/projects/{projectId}/impact-tasks → HTTP 200 · 任务条数 ${result.tasksBeforeStaleApply} → ${result.tasksAfterStaleApply}`,
    ],
  }
}

/** G3.6 判定：未关闭阻断项不能 STABILIZED 或导出。 */
export function verdictG36(result: G3StabilizationChainResult): G3Verdict {
  const blockedWithReasons =
    result.blocked.status === 'BLOCKED' && result.blocked.blockers.length > 0
  // 冻结被拒只认 **4xx**：服务端按契约拒掉才是「被拒」；
  // 5xx 是服务端自身错误，不能拿它当「不能导出」的证据。
  const freezeRefused = isClientRejection(result.freezeBeforeStable.status)
  const stabilized = result.stabilized.status === 'STABILIZED' && result.stabilized.blockers.length === 0
  const receiptClosureAccepted =
    !result.stabilizedAfterReceipt || result.stabilizedAfterReceipt.status === 'STABILIZED'
  const holds = blockedWithReasons && freezeRefused && stabilized && receiptClosureAccepted
  return {
    id: 'G3.6',
    title: '稳定化切片：未关闭阻断项不能 STABILIZED 或导出',
    holds,
    detail: holds
      ? `阻断影响未闭环时，稳定化以 200 成功响应返回 ${result.blocked.status}，` +
        `并给出 ${result.blocked.blockers.length} 条完整阻断原因（${blockerLines(result.blocked).join('；')}）；` +
        `此时冻结交付快照被服务端按 ${result.freezeBeforeStable.status} ` +
        `（code=${result.freezeBeforeStable.code || '（未给码）'}）拒掉；` +
        `全部阻断影响闭环后再次稳定化返回 ${result.stabilized.status}（检查时刻 ${result.stabilized.checkedAt}）` +
        (result.stabilizedAfterReceipt
          ? `；以交付检查回执闭环重算影响之后再次稳定化仍为 ${result.stabilizedAfterReceipt.status}。`
          : '。')
      : `不成立：阻断态=${result.blocked.status}（阻断原因 ${result.blocked.blockers.length} 条）、` +
        `稳定化前冻结=${result.freezeBeforeStable.status}、最终稳定化=${result.stabilized.status}、` +
        `回执闭环后稳定化=${result.stabilizedAfterReceipt?.status || '（未跑）'}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/stabilize → HTTP 200 · status=${result.blocked.status} · ` +
        `阻断原因 ${result.blocked.blockers.length} 条 · 失效任务 ${result.blocked.staleTaskIds.length} 条`,
      ...blockerLines(result.blocked).map((line) => `阻断原因：${line}`),
      `POST /api/v1/lingdoc/projects/{projectId}/releases（稳定化前）→ HTTP ${result.freezeBeforeStable.status} · ` +
        `code=${result.freezeBeforeStable.code || '（未给码）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/stabilize（闭环后）→ HTTP 200 · status=${result.stabilized.status} · ` +
        `checked_at=${result.stabilized.checkedAt}`,
      ...(result.stabilizedAfterReceipt
        ? [`POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/stabilize（交付回执闭环后）→ HTTP 200 · ` +
          `status=${result.stabilizedAfterReceipt.status} · checked_at=${result.stabilizedAfterReceipt.checkedAt}`]
        : []),
    ],
  }
}

/** 交付闭环判定：产物 verified + 哈希一致才算导出成功。 */
export function verdictDeliveryClosure(result: G3StabilizationChainResult): G3Verdict {
  const checkPassed = result.check.status === 'passed' && result.snapshot.checkStatus === 'passed'
  const verified = exportSucceeded(result.exportArtifact)
  const matched = result.download.matches
  const holds = checkPassed && verified && matched
  return {
    id: 'DELIVERY-CLOSURE',
    title: '交付闭环：检查通过 → 冻结 → 导出 → 下载核对',
    holds,
    detail: holds
      ? `交付检查 ${result.check.status}（回执 ${result.check.receiptId || '（无）'}），` +
        `冻结快照 ${result.snapshot.id} 的检查结论 ${result.snapshot.checkStatus}；` +
        `导出受理返回 HTTP ${result.exportAccepted.status}，产物最终状态 ${result.exportArtifact.status}，` +
        `文件哈希 ${result.exportArtifact.fileSha256 || '（无）'}；` +
        `下载 ${result.download.size} 字节，实测哈希与产物一致=${result.download.matches}。`
      : `不成立：检查=${result.check.status}，快照检查=${result.snapshot.checkStatus}，` +
        `产物状态=${result.exportArtifact.status}，哈希一致=${result.download.matches}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/checks → HTTP 200 · status=${result.check.status} · receipt_id=${result.check.receiptId || '（无）'}`,
      `GET /api/v1/lingdoc/projects/{projectId}/check-receipts/{receiptId} → HTTP 200 · version=${result.receipt.version} · status=${result.receipt.status}`,
      `POST /api/v1/lingdoc/projects/{projectId}/releases → HTTP 201 · snapshot_id=${result.snapshot.id} · check.status=${result.snapshot.checkStatus} · is_current=${result.snapshot.isCurrent}`,
      `GET /api/v1/lingdoc/projects/{projectId}/releases → HTTP 200 · 条目数=${result.releaseList.count} · truncated=${result.releaseList.truncated}`,
      `POST /api/v1/lingdoc/projects/{projectId}/releases/{snapshotId}/exports → HTTP ${result.exportAccepted.status} · ` +
        `（受理不等于成功）· 产物状态序列 ${result.pollStatuses.join(' → ')}`,
      `GET /api/v1/lingdoc/projects/{projectId}/exports/{exportId} → HTTP 200 · status=${result.exportArtifact.status} · file_sha256=${result.exportArtifact.fileSha256 || '（无）'}`,
      `GET ${result.exportArtifact.downloadPath} → HTTP 200 · ${result.download.size} 字节 · sha256=${result.download.sha256} · 与产物哈希一致=${result.download.matches}`,
    ],
  }
}
