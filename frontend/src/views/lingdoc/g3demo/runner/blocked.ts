/**
 * G3 验收台-6 · 稳定化阻断与恢复路径执行器。
 *
 * 这一层在 `runner/stabilization.ts`（稳定化与交付闭环）之上，把
 * 「构造阻断 → 原样展示阻断原因 → 处置规则问题 → 闭环阻断项 → 重投 → 再次已稳定」
 * 跑成真实调用，并单独用**隔离项目**跑一次「人工复核任务不能被自动关闭」的探针。
 *
 * 写死的硬约束（不靠人记）：
 *
 * 1. **稳定化是判定，不是错误**：`POST /change-sets/{id}/stabilize` 的请求体**严格是 `{}`**；
 *    阻断态（`status=BLOCKED`）同样以 **200 成功响应**返回，`blockers` 与 `stale_task_ids`
 *    原样保留——页面必须**逐条**展示完整阻断原因（码 / 对象 / 原因），不能折叠成一句失败。
 * 2. **影响任务的状态机有强前置顺序**：`assign`（指派给当前操作者）→ `start` →
 *    复写章节并按**新规格修订**重新确认 → `complete`（带对当前版本有效的闭环证据）。
 *    顺序错了（先复写再 `start`）会让任务落 `STALE` / `target_version_replaced`。
 * 3. **接受不处理（`dismiss`）只对非阻断影响可达**：阻断级影响一律只能以闭环证据完成；
 *    页面上对阻断级任务要**给出说明**，而不是隐藏入口。
 * 4. **失效任务**（`STALE`）既不算闭环，也不阻断稳定化，只被单独上报。
 * 5. **规则问题与影响任务分离**：人工处置规则问题不闭环任何影响任务；
 *    对应的 ImpactTask 仍是 `OPEN` 时，项目仍然不能稳定。
 * 6. **人工复核不能被自动关闭**：复核类任务缺 `human_decision` 时服务端拒绝关闭。
 *
 * 步骤名一律以「阻断-」开头：它们是矩阵规则表（`g3Evidence.ts`）的锚，
 * **不与前几票的步骤名重名**——否则本轮的步骤会顺手把别的行也判成已覆盖。
 */

import { envelopeData, errorCodeOf, type G3HttpClient } from './httpClient'
import { makeIdempotencyKey, runStep, type G3RunContext } from './runContext'
import {
  applyChangeSet,
  assessChangeSet,
  confirmChapter,
  createChangeSet,
  readImpactAssessment,
  readProjectState,
  writeChapterVersion,
  type G3ChangeSetFact,
  type G3ImpactAssessmentFact,
  type G3ProjectState,
  type G3Verdict,
} from './changeset'
import {
  actOnImpactTask,
  canDismissTask,
  createDependencyEdge,
  listImpactTaskDetails,
  type G3ImpactTaskDetailFact,
  type G3ObjectVersionRef,
  type G3TaskTransition,
} from './dependencies'
import {
  blockerLines,
  readStabilizationFact,
  stabilizeChangeSet,
  type G3StabilizationFact,
} from './stabilization'
import { runProjectChain } from './project'

/**
 * 本链的步骤名。
 *
 * 它们是**矩阵规则表的锚**（见 `g3Evidence.ts` 的 `G3_ROW_EVIDENCE_RULES`）：
 * 名字对不上时，矩阵那几行会退回「未被本轮覆盖」，而不是给出假的 `PASS`。
 */
export const G3_BLOCKED_STEPS = {
  readProject: '阻断-读项目状态',
  writeChapter: '阻断-写入章节正文',
  confirmChapter: '阻断-确认章节',
  createClaim: '阻断-写入研究主张',
  updateClaim: '阻断-更新研究主张',
  createEdge: '阻断-建立依赖边',
  createChangeSet: '阻断-创建变更集',
  assess: '阻断-评估变更集',
  readAssessment: '阻断-读影响评估',
  apply: '阻断-应用变更集',
  readTasks: '阻断-读影响任务',
  stabilizeBlocked: '阻断-稳定化（阻断态）',
  deliveryCheck: '阻断-交付检查',
  dismissRuleIssue: '阻断-接受不处理规则问题',
  resolveRuleIssue: '阻断-处置规则问题',
  recheck: '阻断-重检交付检查',
  dismissBlockingTask: '阻断-对阻断影响尝试接受不处理',
  dismissNonBlockingTask: '阻断-对非阻断影响接受不处理',
  assignTask: '阻断-指派影响任务',
  startTask: '阻断-开始处置影响任务',
  startStaleTask: '阻断-开始处置（目标版本已替换）',
  completeTask: '阻断-完成影响任务',
  rewriteChapter: '阻断-复写章节正文',
  reconfirm: '阻断-重新确认章节',
  stabilizeAfterDismiss: '阻断-稳定化（规则问题处置后）',
  stabilizeAfterClosure: '阻断-稳定化（阻断项闭环后）',
  replaceTarget: '阻断-替换目标对象版本',
  stabilizeVersionMismatch: '阻断-稳定化（版本不匹配）',
  reissueChangeSet: '阻断-重投变更集',
  reissueEdge: '阻断-重投-建立依赖边',
  reissueAssess: '阻断-重投-评估变更集',
  reissueApply: '阻断-重投-应用变更集',
  stabilizeFinal: '阻断-稳定化（重投闭环后）',
  reviewProbeProject: '阻断-人工复核探针-建项目',
  reviewProbeClaim: '阻断-人工复核探针-写入研究主张',
  reviewProbeEdge: '阻断-人工复核探针-建立依赖边',
  reviewProbeChangeSet: '阻断-人工复核探针-创建变更集',
  reviewProbeApply: '阻断-人工复核探针-应用变更集',
  reviewProbeAssign: '阻断-人工复核探针-指派',
  reviewProbeStart: '阻断-人工复核探针-开始处置',
  reviewProbeCompleteNoReview: '阻断-人工复核探针-缺少人工复核完成',
  reviewProbeCompleteWithReview: '阻断-人工复核探针-带人工复核完成',
} as const

/* ------------------------------------------------------------------ *
 * 事实读取
 * ------------------------------------------------------------------ */

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

/** 一条规则问题（`ValidationIssue`）。 */
export interface G3RuleIssueFact {
  id: string
  ruleId: string
  severity: string
  targetId: string
  targetVersion: string
  message: string
  /** 人工处置动作（`dismiss` / `resolve` / `waive`）；没有处置时为空串。 */
  dispositionAction: string
  dispositionReason: string
  dispositionActorId: string
}

/** 读一条规则问题。处置信息**原样保留**，没有就不编。 */
export function readRuleIssueFact(data: unknown): G3RuleIssueFact {
  const raw = objectOf(data)
  const disposition = objectOf(raw.disposition)
  return {
    id: str(raw.id),
    ruleId: str(raw.rule_id),
    severity: str(raw.severity),
    targetId: str(raw.target_id),
    targetVersion: str(raw.target_version),
    message: str(raw.message),
    dispositionAction: str(disposition.action),
    dispositionReason: str(disposition.reason),
    dispositionActorId: str(disposition.actor_id),
  }
}

/** 交付检查结果（含全部规则问题）。 */
export interface G3CheckWithIssuesFact {
  receiptId: string
  status: string
  projectVersion: number
  issues: G3RuleIssueFact[]
}

/** 读交付检查结果。规则问题逐条保留。 */
export function readCheckWithIssues(data: unknown): G3CheckWithIssuesFact {
  const raw = objectOf(data)
  return {
    receiptId: str(raw.receipt_id),
    status: str(raw.status),
    projectVersion: num(raw.project_version),
    issues: (Array.isArray(raw.issues) ? raw.issues : []).map(readRuleIssueFact),
  }
}

/** 人工处置回执。 */
export interface G3IssueDispositionFact {
  issueId: string
  ruleId: string
  severity: string
  targetId: string
  projectVersion: number
  action: string
  reason: string
  actorId: string
  createdAt: string
}

/** 读人工处置回执。 */
export function readIssueDispositionFact(data: unknown): G3IssueDispositionFact {
  const raw = objectOf(data)
  return {
    issueId: str(raw.issue_id),
    ruleId: str(raw.rule_id),
    severity: str(raw.severity),
    targetId: str(raw.target_id),
    projectVersion: num(raw.project_version),
    action: str(raw.action),
    reason: str(raw.reason),
    actorId: str(raw.actor_id),
    createdAt: str(raw.created_at),
  }
}

/**
 * 「接受不处理」在某个严重度上是否可达。
 *
 * 阻断级影响**一律不可接受**：只能以闭环证据完成。所以这里返回的是
 * 「能不能」+「为什么不能」的说明，页面据此给出说明，而不是把入口藏起来。
 */
export function dismissAvailability(severity: string): { allowed: boolean; explanation: string } {
  if (canDismissTask(severity)) {
    return { allowed: true, explanation: '' }
  }
  return {
    allowed: false,
    explanation:
      '阻断级影响不能「接受本次不处理」：它只能以真实的闭环证据完成，' +
      '否则项目无法标记为已稳定、也不能导出。',
  }
}

/** 任务状态的三种语义（按术语表）。 */
export interface G3TaskSemantics {
  /** 是否算闭环（只有 `DONE` 与 `DISMISSED` 算）。 */
  closed: boolean
  /** 是否阻断稳定化。 */
  blocking: boolean
  /** 是否被单独上报（失效任务）。 */
  reported: boolean
}

/**
 * 任务状态的语义。
 *
 * - `DONE` / `DISMISSED`：算闭环。
 * - `STALE`（失效任务）：**既不算闭环，也不阻断稳定化**，只被单独上报。
 * - 其余（`OPEN` / `IN_PROGRESS` / `WAITING_USER`）：阻断级未闭环时阻断稳定化。
 */
export function staleTaskSemantics(status: string): G3TaskSemantics {
  if (status === 'DONE' || status === 'DISMISSED') {
    return { closed: true, blocking: false, reported: false }
  }
  if (status === 'STALE') {
    return { closed: false, blocking: false, reported: true }
  }
  return { closed: false, blocking: true, reported: false }
}

/* ------------------------------------------------------------------ *
 * 单步调用
 * ------------------------------------------------------------------ */

/** 交付检查。**不带 `Idempotency-Key`**（契约未标这个操作）。 */
export async function runDeliveryCheckWithIssues(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  expectedProjectVersion: number,
  stepName: string,
): Promise<G3CheckWithIssuesFact> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'POST',
    path: projectPath(projectId, '/checks'),
    token,
    body: { expected_project_version: expectedProjectVersion },
    conclusion: '交付检查已完成（HTTP {status}）',
    detail: (body) => {
      const fact = readCheckWithIssues(objectOf(body).data)
      return {
        receipt_id: fact.receiptId,
        status: fact.status,
        project_version: fact.projectVersion,
        issue_count: fact.issues.length,
        issues: fact.issues.map((issue) => `${issue.ruleId}:${issue.severity}:${issue.id}`),
      }
    },
  })
  return readCheckWithIssues(envelopeData(data))
}

/**
 * 人工处置一条规则问题。
 *
 * `action` 的取值：`dismiss`（接受本次不处理）/ `resolve`（已解决）/ `waive`（豁免）。
 * 服务端对**阻断级**问题拒绝 `waive`（400）；`dismiss` 在当前后端上会落到 500，
 * 这条事实由本函数原样记录（不吞、不改写）。
 */
export async function disposeRuleIssue(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  issueId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedProjectVersion: number
    action: 'dismiss' | 'resolve' | 'waive'
    reason: string
    expectedStatus?: number | readonly number[]
  },
): Promise<{ status: number; code: string; disposition: G3IssueDispositionFact | null }> {
  const { response, data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/issues/${encodeURIComponent(issueId)}/disposition`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    expectedStatus: options.expectedStatus,
    body: {
      expected_project_version: options.expectedProjectVersion,
      action: options.action,
      reason: options.reason,
    },
    conclusion: `规则问题处置「${options.action}」（HTTP {status}）`,
    detail: (body) => ({
      issue_id: issueId,
      action: options.action,
      status_code: errorCodeOf(body),
    }),
  })
  const disposition = response.ok ? readIssueDispositionFact(envelopeData(data)) : null
  return {
    status: response.status,
    code: errorCodeOf(response.body),
    disposition,
  }
}

/** 一次稳定化调用的实况：状态码、错误码与判定结果。 */
export interface G3StabilizeOutcome {
  /** HTTP 状态码。 */
  status: number
  /** 错误码（成功时为空）。 */
  code: string
  /** 判定结果（被拒时为空字段）。 */
  fact: G3StabilizationFact
}

/**
 * 调用稳定化并**如实**记录状态码。
 *
 * 请求体**严格是 `{}`**。阻断态是 200 成功响应；而当目标对象在任务完成之后
 * 被新版本替换时，服务端在重新读取闭环证据时就会发现版本不匹配，
 * 并以 **409 `version_conflict`** 拒掉这一次稳定化——这一版修订**没有被放行**。
 * 两种「没有放行」都要原样呈现，不能折叠成一句失败。
 */
export async function stabilizeWithOutcome(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
  options: { stepName: string; idemLabel: string; expectedStatus?: number | readonly number[] },
): Promise<G3StabilizeOutcome> {
  const { response, data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}/stabilize`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    expectedStatus: options.expectedStatus,
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
  return {
    status: response.status,
    code: errorCodeOf(response.body),
    fact: readStabilizationFact(envelopeData(data)),
  }
}

/** 写一条研究主张（人工撰写并确认，不经过模型）。 */
export async function saveResearchClaim(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedProjectVersion: number
    expectedContextRevision: number
    text: string
    scope: string
    reason: string
    claimId?: string
    expectedVersion?: number
  },
): Promise<{ id: string; version: number }> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, '/research-claims'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    body: {
      expected_project_version: options.expectedProjectVersion,
      expected_context_revision: options.expectedContextRevision,
      ...(options.claimId ? { claim_id: options.claimId } : {}),
      ...(options.expectedVersion ? { expected_version: options.expectedVersion } : {}),
      text: options.text,
      scope: options.scope,
      status: 'active',
      reason: options.reason,
      confirmed: true,
    },
    conclusion: '研究主张已写入（HTTP {status}）',
    detail: (body) => {
      const claim = objectOf(objectOf(body).data)
      return { claim_id: claim.id, version: claim.version, status: claim.status }
    },
  })
  const claim = objectOf(envelopeData(data))
  return { id: str(claim.id), version: num(claim.version) }
}

/* ------------------------------------------------------------------ *
 * 链
 * ------------------------------------------------------------------ */

/** 链的输入。 */
export interface G3BlockedChainOptions {
  projectId: string
  /** 模板激活后自动创建的两个章节。 */
  chapters: { id: string; sectionId: string; title: string }[]
  /** 当前操作者（指派与闭环都由他执行）。 */
  operatorId: string
  /**
   * 是否跑「人工复核探针」（AC-G3-07 的人工复核那半段）。
   *
   * 探针需要**另一个隔离项目**：复核类任务会阻断稳定化，放在主链里会让
   * 「闭环后转为已稳定」永远走不到。
   */
  runReviewProbe?: boolean
}

/** 一次「接受不处理」尝试的记录。 */
export interface G3DismissAttempt {
  label: string
  taskId: string
  severity: string
  status: number
  code: string
  finalStatus: string
  /** 阻断级影响上的说明（非阻断时为空）。 */
  explanation: string
}

/** 规则问题处置的记录。 */
export interface G3RuleIssueDispositionRecord {
  issueId: string
  ruleId: string
  severity: string
  targetId: string
  /** `dismiss` 尝试的实况。 */
  dismissStatus: number
  dismissCode: string
  /** `resolve` 处置的实况。 */
  resolveStatus: number
  resolveCode: string
  dispositionAction: string
  /** 处置后重检时，问题是否带上了处置。 */
  carriesDisposition: boolean
}

/** 人工复核探针的记录。 */
export interface G3ReviewProbeFact {
  projectId: string
  claimId: string
  taskId: string
  taskAction: string
  targetKind: string
  /** 缺人工复核决策时的关闭尝试。 */
  completeWithoutReviewStatus: number
  completeWithoutReviewCode: string
  /** 带人工复核决策时的关闭尝试。 */
  completeWithReviewStatus: number
  completeWithReviewCode: string
  completeWithReviewAccepted: boolean
  /** 真实模型那半段是否覆盖（本票不覆盖：造证据卡要走真实模型）。 */
  modelSideCovered: boolean
}

/** 稳定化阻断与恢复链的结果。 */
export interface G3BlockedChainResult {
  changeSet: G3ChangeSetFact
  reissuedChangeSet: G3ChangeSetFact
  assessment: G3ImpactAssessmentFact
  /** 第一次稳定化：阻断态（200 成功响应）。 */
  blocked: G3StabilizationFact
  blockedLines: string[]
  /** 规则问题处置后再次稳定化：仍然阻断。 */
  afterDisposition: G3StabilizationFact
  /** 阻断项闭环后再次稳定化：已稳定。 */
  stabilized: G3StabilizationFact
  /** 目标对象版本被替换后再次稳定化：保持阻断。 */
  versionMismatch: G3StabilizationFact
  /** 目标对象版本被替换后那次稳定化的 HTTP 状态码与错误码。 */
  mismatchStatus: number
  mismatchCode: string
  /** 重投闭环后再次稳定化：已稳定。 */
  finalStabilized: G3StabilizationFact
  /** 交付检查（处置前 / 处置后）。 */
  checkBefore: G3CheckWithIssuesFact
  checkAfter: G3CheckWithIssuesFact
  /** 规则问题处置记录。 */
  disposition: G3RuleIssueDispositionRecord | null
  /** 「接受不处理」的边界记录。 */
  dismissAttempts: G3DismissAttempt[]
  /** 任务处置过程。 */
  transitions: G3TaskTransition[]
  /** 被判为失效的任务。 */
  staleTasks: { id: string; action: string; severity: string; staleReason: string }[]
  /** 重投出来的任务。 */
  reissuedTaskIds: string[]
  /** 被新版本替换掉的目标章节版本 id。 */
  replacedVersionId: string
  /** 人工复核探针（AC-G3-07 的那半段）。 */
  reviewProbe: G3ReviewProbeFact | null
  verdicts: G3Verdict[]
}

/** 链跑失败时保留已经拿到的证据。 */
export class G3BlockedChainError extends Error {
  readonly partial: Partial<G3BlockedChainResult>

  constructor(message: string, partial: Partial<G3BlockedChainResult>) {
    super(message)
    this.name = 'G3BlockedChainError'
    this.partial = partial
  }
}

/** 一次影响任务处置的完整记录（含状态迁移）。 */
async function disposeTask(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  task: G3ImpactTaskDetailFact,
  options: {
    stepName: string
    idemLabel: string
    action: 'assign' | 'start' | 'complete'
    reason: string
    closureEvidence?: G3ObjectVersionRef[]
    humanDecision?: string
    expectedStatus?: number | readonly number[]
    assigneeId?: string
  },
): Promise<{ status: number; code: string; task: G3ImpactTaskDetailFact | null; version: number }> {
  const result = await actOnImpactTask(ctx, client, token, projectId, task.id, {
    stepName: options.stepName,
    idemLabel: options.idemLabel,
    expectedVersion: task.version,
    targetRevision: task.targetRevision,
    action: options.action,
    reason: options.reason,
    closureEvidence: options.closureEvidence,
    humanDecision: options.humanDecision,
    expectedStatus: options.expectedStatus,
    assigneeId: options.assigneeId,
  })
  return {
    status: result.status,
    code: result.code,
    task: result.task,
    version: result.task?.version ?? task.version + 1,
  }
}

/**
 * 跑「构造阻断 → 处置 → 闭环 → 版本不匹配 → 重投 → 再次已稳定」这一整条。
 *
 * 顺序是**状态机的前置条件**：评估必须早于应用，闭环必须早于稳定化，
 * 而「复写章节」必须夹在 `start` 与 `complete` 之间。
 */
export async function runBlockedChain(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: G3BlockedChainOptions,
): Promise<G3BlockedChainResult> {
  const question = options.chapters.find((chapter) => chapter.sectionId === 'question')
  const method = options.chapters.find((chapter) => chapter.sectionId === 'method')
  if (!question || !method) {
    throw new Error('阻断链需要模板激活后的 question / method 两个章节')
  }
  if (!options.operatorId) {
    throw new Error('阻断链需要当前操作者（用于指派影响任务）')
  }

  const partial: Partial<G3BlockedChainResult> = {}
  const transitions: G3TaskTransition[] = []
  const dismissAttempts: G3DismissAttempt[] = []

  // —— 前置：两个章节都要有正文且已确认，ChapterVersion 才是可用的依赖对象 ——
  let project: G3ProjectState = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    G3_BLOCKED_STEPS.readProject,
  )
  const chapterVersions: Record<string, string> = {}
  for (const chapter of [question, method]) {
    const written = await writeChapterVersion(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_BLOCKED_STEPS.writeChapter,
      idemLabel: `blocked-chapter-body-${chapter.sectionId}`,
      expectedChapterVersionId: null,
      expectedSpecRevision: project.specRevision,
      bodyMarkdown: `## ${chapter.title}\n\nG3 验收台-6 阻断与恢复演示正文（运行 ${ctx.runId}）。`,
    })
    if (!written.currentVersionId) {
      throw new Error(`章节 ${chapter.sectionId} 保存正文后没有拿到当前版本`)
    }
    chapterVersions[chapter.sectionId] = written.currentVersionId
    await confirmChapter(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_BLOCKED_STEPS.confirmChapter,
      idemLabel: `blocked-confirm-${chapter.sectionId}`,
      expectedChapterVersionId: written.currentVersionId,
      expectedSpecRevision: project.specRevision,
    })
  }
  project = await readProjectState(ctx, client, token, options.projectId, G3_BLOCKED_STEPS.readProject)

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

  // —— 依赖边：两条**阻断**影响（研究方案确认失效、变更集直接波及研究方案）
  //    与一条**非阻断**影响（交付结论需重算，用于「接受不处理」的边界对照）。——
  await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_BLOCKED_STEPS.createEdge,
    idemLabel: 'blocked-edge-spec-method',
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
    stepName: G3_BLOCKED_STEPS.createEdge,
    idemLabel: 'blocked-edge-spec-template',
    expectedContextRevision: project.currentContextRevision,
    source: specRef,
    target: templateRef,
    relation: 'spec_requires_recompute',
    action: 'RECOMPUTE',
    origin: 'user',
    reason: '研究条件变化后交付结论需要按当前规则重算',
    confidence: 1,
  })

  // —— 变更集 → 评估 → 读影响评估 → 应用 ——
  const changeSet = await createChangeSet(ctx, client, token, options.projectId, {
    stepName: G3_BLOCKED_STEPS.createChangeSet,
    idemLabel: 'blocked-change-set',
    expectedContextRevision: project.currentContextRevision,
    fields: {
      research_goal: {
        old_value: project.spec.research_goal ?? '',
        new_value: `${project.spec.research_goal ?? ''}（G3 验收台-6 阻断与恢复）`,
      },
    },
    affectedChapterIds: [method.id],
    reason: 'G3 验收台-6：研究条件变化，构造稳定化阻断与恢复路径',
  })
  await assessChangeSet(ctx, client, token, options.projectId, changeSet.id)
  const assessment = await readImpactAssessment(
    ctx,
    client,
    token,
    options.projectId,
    changeSet.id,
    G3_BLOCKED_STEPS.readAssessment,
  )
  await applyChangeSet(ctx, client, token, options.projectId, changeSet.id, {
    stepName: G3_BLOCKED_STEPS.apply,
    idemLabel: 'blocked-apply',
  })
  project = await readProjectState(ctx, client, token, options.projectId, G3_BLOCKED_STEPS.readProject)

  // —— 稳定化（阻断态）：200 成功响应，阻断原因逐条保留 ——
  const blockedOutcome = await stabilizeWithOutcome(ctx, client, token, options.projectId, changeSet.id, {
    stepName: G3_BLOCKED_STEPS.stabilizeBlocked,
    idemLabel: 'blocked-stabilize-blocked',
  })
  const blocked = blockedOutcome.fact
  partial.blocked = blocked
  partial.blockedLines = blockerLines(blocked)

  // —— 规则问题：交付检查给出 ValidationIssue ——
  const checkBefore = await runDeliveryCheckWithIssues(
    ctx,
    client,
    token,
    options.projectId,
    project.projectVersion,
    G3_BLOCKED_STEPS.deliveryCheck,
  )

  // 「接受本次不处理」（dismiss）与「已解决」（resolve）两条处置都试：
  // 服务端的真实行为由这里原样记录，页面据此呈现。
  const issue = checkBefore.issues[0]
  let disposition: G3RuleIssueDispositionRecord | null = null
  if (issue) {
    const dismiss = await disposeRuleIssue(ctx, client, token, options.projectId, issue.id, {
      stepName: G3_BLOCKED_STEPS.dismissRuleIssue,
      idemLabel: 'blocked-dismiss-rule-issue',
      expectedProjectVersion: project.projectVersion,
      action: 'dismiss',
      reason: 'G3 验收台-6：本次不处理该规则问题',
      expectedStatus: [201, 400, 403, 404, 409, 422, 500],
    })
    const resolve = await disposeRuleIssue(ctx, client, token, options.projectId, issue.id, {
      stepName: G3_BLOCKED_STEPS.resolveRuleIssue,
      idemLabel: 'blocked-resolve-rule-issue',
      expectedProjectVersion: project.projectVersion,
      action: 'resolve',
      reason: 'G3 验收台-6：已人工核对并处置该规则问题',
      expectedStatus: [201, 400, 403, 404, 409, 422, 500],
    })
    disposition = {
      issueId: issue.id,
      ruleId: issue.ruleId,
      severity: issue.severity,
      targetId: issue.targetId,
      dismissStatus: dismiss.status,
      dismissCode: dismiss.code,
      resolveStatus: resolve.status,
      resolveCode: resolve.code,
      dispositionAction: resolve.disposition?.action ?? '',
      carriesDisposition: false,
    }
  }

  // —— 处置之后重检：问题是否带上处置、结论是否变化 ——
  const projectAfterCheck = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    G3_BLOCKED_STEPS.readProject,
  )
  const checkAfter = await runDeliveryCheckWithIssues(
    ctx,
    client,
    token,
    options.projectId,
    projectAfterCheck.projectVersion,
    G3_BLOCKED_STEPS.recheck,
  )
  if (disposition) {
    const reissued = checkAfter.issues.find((item) => item.id === disposition?.issueId)
    disposition.carriesDisposition = Boolean(reissued?.dispositionAction)
    disposition.dispositionAction = reissued?.dispositionAction ?? disposition.dispositionAction
  }

  // —— 规则问题被处置了，但对应的**影响任务**仍未闭环：项目仍然不能稳定 ——
  const tasksAfterApply = await listImpactTaskDetails(
    ctx,
    client,
    token,
    options.projectId,
    G3_BLOCKED_STEPS.readTasks,
  )
  const afterDispositionOutcome = await stabilizeWithOutcome(
    ctx,
    client,
    token,
    options.projectId,
    changeSet.id,
    {
      stepName: G3_BLOCKED_STEPS.stabilizeAfterDismiss,
      idemLabel: 'blocked-stabilize-after-disposition',
    },
  )
  const afterDisposition = afterDispositionOutcome.fact
  partial.afterDisposition = afterDisposition

  // —— 「接受不处理」的边界：阻断级被拒，非阻断级可达 ——
  const blockingTask = tasksAfterApply.find(
    (task) => task.severity === 'blocking' && task.status !== 'DONE' && task.status !== 'STALE',
  )
  if (blockingTask) {
    const availability = dismissAvailability(blockingTask.severity)
    const attempt = await actOnImpactTask(ctx, client, token, options.projectId, blockingTask.id, {
      stepName: G3_BLOCKED_STEPS.dismissBlockingTask,
      idemLabel: 'blocked-dismiss-blocking-task',
      expectedVersion: blockingTask.version,
      targetRevision: blockingTask.targetRevision,
      action: 'dismiss',
      reason: 'G3 验收台-6：阻断级影响不接受不处理',
      expectedStatus: [200, 400, 403, 409, 422],
    })
    dismissAttempts.push({
      label: '阻断级影响',
      taskId: blockingTask.id,
      severity: blockingTask.severity,
      status: attempt.status,
      code: attempt.code,
      finalStatus: attempt.task?.status ?? blockingTask.status,
      explanation: availability.explanation,
    })
  }
  const nonBlockingTask = tasksAfterApply.find(
    (task) => task.severity !== 'blocking' && task.status !== 'DONE' && task.status !== 'STALE',
  )
  if (nonBlockingTask) {
    const availability = dismissAvailability(nonBlockingTask.severity)
    const attempt = await actOnImpactTask(ctx, client, token, options.projectId, nonBlockingTask.id, {
      stepName: G3_BLOCKED_STEPS.dismissNonBlockingTask,
      idemLabel: 'blocked-dismiss-non-blocking-task',
      expectedVersion: nonBlockingTask.version,
      targetRevision: nonBlockingTask.targetRevision,
      action: 'dismiss',
      reason: 'G3 验收台-6：非阻断影响本次不处理',
      expectedStatus: [200, 400, 403, 409, 422],
    })
    dismissAttempts.push({
      label: '非阻断级影响',
      taskId: nonBlockingTask.id,
      severity: nonBlockingTask.severity,
      status: attempt.status,
      code: attempt.code,
      finalStatus: attempt.task?.status ?? nonBlockingTask.status,
      explanation: availability.explanation,
    })
  }

  // —— 闭环：assign → start →（复写并重新确认）→ complete ——
  const blockingTasks = tasksAfterApply.filter(
    (task) => task.severity === 'blocking' && task.status !== 'DONE' && task.status !== 'STALE',
  )
  const startedVersions: Record<string, number> = {}
  for (const task of blockingTasks) {
    const assigned = await disposeTask(ctx, client, token, options.projectId, task, {
      stepName: G3_BLOCKED_STEPS.assignTask,
      idemLabel: `blocked-assign-${task.id}`,
      action: 'assign',
      reason: 'G3 验收台-6：把影响任务指派给当前操作者',
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
    const started = await disposeTask(ctx, client, token, options.projectId, { ...task, version: assigned.version }, {
      stepName: G3_BLOCKED_STEPS.startTask,
      idemLabel: `blocked-start-${task.id}`,
      action: 'start',
      reason: 'G3 验收台-6：开始处置影响任务',
    })
    startedVersions[task.id] = started.version
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

  // 复写受影响章节并按**新的规格修订**重新确认（闭环证据的来源）
  const newVersions: Record<string, string> = {}
  for (const chapter of [method, question]) {
    const rewritten = await writeChapterVersion(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_BLOCKED_STEPS.rewriteChapter,
      idemLabel: `blocked-rewrite-${chapter.sectionId}`,
      expectedChapterVersionId: chapterVersions[chapter.sectionId],
      expectedSpecRevision: project.specRevision,
      bodyMarkdown: `## ${chapter.title}\n\nG3 验收台-6 变更后复写的正文（运行 ${ctx.runId}，规格修订 ${project.specRevision}）。`,
    })
    if (!rewritten.currentVersionId) {
      throw new Error(`复写章节 ${chapter.sectionId} 后没有拿到当前版本`)
    }
    newVersions[chapter.sectionId] = rewritten.currentVersionId
    await confirmChapter(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_BLOCKED_STEPS.reconfirm,
      idemLabel: `blocked-reconfirm-${chapter.sectionId}`,
      expectedChapterVersionId: rewritten.currentVersionId,
      expectedSpecRevision: project.specRevision,
    })
  }

  for (const task of blockingTasks) {
    const sectionId =
      task.target.kind === 'ChapterVersion'
        ? options.chapters.find((chapter) => chapter.id === task.target.id)?.sectionId
        : undefined
    const version = sectionId ? newVersions[sectionId] : undefined
    if (!version) {
      throw new Error(
        `影响任务 ${task.id}（动作 ${task.action}）的目标不是本轮能重新确认的章节版本，无法造出闭环证据`,
      )
    }
    const closureRef: G3ObjectVersionRef = { kind: 'ChapterVersion', id: task.target.id, version }
    const completed = await disposeTask(
      ctx,
      client,
      token,
      options.projectId,
      { ...task, version: startedVersions[task.id] ?? task.version + 2 },
      {
        stepName: G3_BLOCKED_STEPS.completeTask,
        idemLabel: `blocked-complete-${task.id}`,
        action: 'complete',
        reason: 'G3 验收台-6：已按当前规格修订复写并重新确认章节，提交闭环证据',
        closureEvidence: [closureRef],
      },
    )
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

  // —— 阻断项闭环后重跑稳定化：已稳定 ——
  const stabilizedOutcome = await stabilizeWithOutcome(ctx, client, token, options.projectId, changeSet.id, {
    stepName: G3_BLOCKED_STEPS.stabilizeAfterClosure,
    idemLabel: 'blocked-stabilize-after-closure',
  })
  const stabilized = stabilizedOutcome.fact
  partial.stabilized = stabilized

  // —— AC-G3-12：目标对象在任务「已完成」之后被新版本替换 → 稳定化重新读取发现版本不匹配 ——
  const replaced = await writeChapterVersion(ctx, client, token, options.projectId, method.id, {
    stepName: G3_BLOCKED_STEPS.replaceTarget,
    idemLabel: 'blocked-replace-target',
    expectedChapterVersionId: newVersions.method,
    expectedSpecRevision: project.specRevision,
    bodyMarkdown: `## ${method.title}\n\nG3 验收台-6：目标对象被新版本替换（运行 ${ctx.runId}）。`,
  })
  partial.replacedVersionId = replaced.currentVersionId ?? ''
  const mismatchOutcome = await stabilizeWithOutcome(ctx, client, token, options.projectId, changeSet.id, {    stepName: G3_BLOCKED_STEPS.stabilizeVersionMismatch,
    idemLabel: 'blocked-stabilize-version-mismatch',
    expectedStatus: [200, 409, 422],
  })
  const versionMismatch = mismatchOutcome.fact
  partial.versionMismatch = versionMismatch
  partial.mismatchStatus = mismatchOutcome.status
  partial.mismatchCode = mismatchOutcome.code

  // —— 重投：再走一次变更集 → 评估 → 应用，为新版本重新提出影响任务 ——
  const projectBeforeReissue = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    G3_BLOCKED_STEPS.readProject,
  )
  const reissuedChangeSet = await createChangeSet(ctx, client, token, options.projectId, {
    stepName: G3_BLOCKED_STEPS.reissueChangeSet,
    idemLabel: 'blocked-reissue-change-set',
    expectedContextRevision: projectBeforeReissue.currentContextRevision,
    fields: {
      research_subject: {
        old_value: projectBeforeReissue.spec.research_subject ?? '',
        new_value: `${projectBeforeReissue.spec.research_subject ?? ''}（G3 验收台-6 重投）`,
      },
    },
    affectedChapterIds: [method.id],
    reason: 'G3 验收台-6：目标对象版本已变化，按新基线重投影响任务',
  })
  // 重投需要一条**指向当前版本**的依赖边：原边指向的是被替换掉的旧版本，
  // 重新评估时已不再有效。没有它，重投只会得到变更集自身带来的那一条任务。
  await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_BLOCKED_STEPS.reissueEdge,
    idemLabel: 'blocked-reissue-edge',
    expectedContextRevision: projectBeforeReissue.currentContextRevision,
    source: {
      kind: 'ProjectSpec',
      id: options.projectId,
      version: String(projectBeforeReissue.specRevision),
    },
    target: {
      kind: 'ChapterVersion',
      id: method.id,
      version: replaced.currentVersionId ?? newVersions.method,
    },
    relation: 'spec_invalidates_method_again',
    action: 'INVALIDATE_CONFIRMATION',
    origin: 'user',
    reason: '目标对象版本已变化，研究条件变化仍使研究方案章节的确认失效',
    confidence: 1,
  })
  await assessChangeSet(ctx, client, token, options.projectId, reissuedChangeSet.id)
  await applyChangeSet(ctx, client, token, options.projectId, reissuedChangeSet.id, {
    stepName: G3_BLOCKED_STEPS.reissueApply,
    idemLabel: 'blocked-reissue-apply',
  })
  const projectAfterReissue = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    G3_BLOCKED_STEPS.readProject,
  )

  const tasksAfterReissue = await listImpactTaskDetails(
    ctx,
    client,
    token,
    options.projectId,
    G3_BLOCKED_STEPS.readTasks,
  )
  const reissuedTasks = tasksAfterReissue.filter(
    (task) => task.changeSetId === reissuedChangeSet.id && task.status !== 'DONE' && task.status !== 'STALE',
  )
  const reissuedTaskIds = reissuedTasks.map((task) => task.id)

  // 重投出来的任务**逐条**处置：先闭环一条，再开始另一条。
  // 第一条闭环会把章节推到新版本；第二条随后 `start` 时它提出的版本已被替换，
  // 服务端据此把任务落成 `STALE`（失效任务）——既不算闭环，也不阻断稳定化。
  const reissueStarted: Record<string, number> = {}
  const reissueVersions: Record<string, string> = {
    method: replaced.currentVersionId ?? newVersions.method,
    question: newVersions.question,
  }
  const staleTasks: { id: string; action: string; severity: string; staleReason: string }[] = []
  let chaptersRewritten = false

  const rewriteAndConfirm = async (): Promise<void> => {
    for (const chapter of [method, question]) {
      const rewritten = await writeChapterVersion(ctx, client, token, options.projectId, chapter.id, {
        stepName: G3_BLOCKED_STEPS.rewriteChapter,
        idemLabel: `blocked-reissue-rewrite-${chapter.sectionId}`,
        expectedChapterVersionId: reissueVersions[chapter.sectionId],
        expectedSpecRevision: projectAfterReissue.specRevision,
        bodyMarkdown: `## ${chapter.title}\n\nG3 验收台-6 重投后复写的正文（运行 ${ctx.runId}，规格修订 ${projectAfterReissue.specRevision}）。`,
      })
      if (!rewritten.currentVersionId) {
        throw new Error(`重投后复写章节 ${chapter.sectionId} 没有拿到当前版本`)
      }
      reissueVersions[chapter.sectionId] = rewritten.currentVersionId
      await confirmChapter(ctx, client, token, options.projectId, chapter.id, {
        stepName: G3_BLOCKED_STEPS.reconfirm,
        idemLabel: `blocked-reissue-reconfirm-${chapter.sectionId}`,
        expectedChapterVersionId: rewritten.currentVersionId,
        expectedSpecRevision: projectAfterReissue.specRevision,
      })
    }
    chaptersRewritten = true
  }

  for (const task of reissuedTasks) {
    const assigned = await disposeTask(ctx, client, token, options.projectId, task, {
      stepName: G3_BLOCKED_STEPS.assignTask,
      idemLabel: `blocked-reissue-assign-${task.id}`,
      action: 'assign',
      reason: 'G3 验收台-6：重投任务的指派',
      assigneeId: options.operatorId,
    })
    transitions.push({
      taskId: task.id,
      action: 'assign',
      severity: assigned.task?.severity ?? task.severity,
      from: task.status,
      to: assigned.task?.status ?? task.status,
      closureEvidence: [],
      note: `重投任务：指派给当前操作者（assignee_id=${options.operatorId}）`,
      status: assigned.status,
      code: assigned.code,
    })
    const started = await disposeTask(ctx, client, token, options.projectId, { ...task, version: assigned.version }, {
      stepName: G3_BLOCKED_STEPS.startTask,
      idemLabel: `blocked-reissue-start-${task.id}`,
      action: 'start',
      reason: 'G3 验收台-6：重投任务的开始处置',
    })
    transitions.push({
      taskId: task.id,
      action: 'start',
      severity: started.task?.severity ?? task.severity,
      from: assigned.task?.status ?? task.status,
      to: started.task?.status ?? task.status,
      closureEvidence: [],
      note: '重投任务：责任人开始处置',
      status: started.status,
      code: started.code,
    })
    if (started.task?.status === 'STALE') {
      // 目标版本已被替换 → 失效任务。
      staleTasks.push({
        id: task.id,
        action: task.action,
        severity: task.severity,
        staleReason: started.task.staleReason,
      })
      continue
    }
    reissueStarted[task.id] = started.version
    if (!chaptersRewritten) {
      await rewriteAndConfirm()
    }
    const sectionId =
      task.target.kind === 'ChapterVersion'
        ? options.chapters.find((chapter) => chapter.id === task.target.id)?.sectionId
        : undefined
    const version = sectionId ? reissueVersions[sectionId] : undefined
    if (!version) {
      continue
    }
    const closureRef: G3ObjectVersionRef = { kind: 'ChapterVersion', id: task.target.id, version }
    const completed = await disposeTask(
      ctx,
      client,
      token,
      options.projectId,
      { ...task, version: reissueStarted[task.id] ?? task.version + 2 },
      {
        stepName: G3_BLOCKED_STEPS.completeTask,
        idemLabel: `blocked-reissue-complete-${task.id}`,
        action: 'complete',
        reason: 'G3 验收台-6：重投任务以当前章节版本闭环',
        closureEvidence: [closureRef],
      },
    )
    transitions.push({
      taskId: task.id,
      action: 'complete',
      severity: completed.task?.severity ?? task.severity,
      from: 'IN_PROGRESS',
      to: completed.task?.status ?? task.status,
      closureEvidence: [closureRef],
      note: `重投任务：以闭环证据完成（${closureRef.kind}/${closureRef.id}@${closureRef.version}）`,
      status: completed.status,
      code: completed.code,
    })
    if (completed.task?.status === 'STALE') {
      staleTasks.push({
        id: task.id,
        action: task.action,
        severity: task.severity,
        staleReason: completed.task.staleReason,
      })
    }
  }

  if (!chaptersRewritten) {
    await rewriteAndConfirm()
  }

  const finalOutcome = await stabilizeWithOutcome(
    ctx,
    client,
    token,
    options.projectId,
    reissuedChangeSet.id,
    {
      stepName: G3_BLOCKED_STEPS.stabilizeFinal,
      idemLabel: 'blocked-stabilize-final',
    },
  )
  const finalStabilized = finalOutcome.fact
  partial.finalStabilized = finalStabilized

  // —— 失效任务：既不算闭环，也不阻断稳定化，只被单独上报 ——
  const tasksFinal = await listImpactTaskDetails(
    ctx,
    client,
    token,
    options.projectId,
    G3_BLOCKED_STEPS.readTasks,
  )
  for (const task of tasksFinal) {
    if (task.status === 'STALE' && !staleTasks.some((item) => item.id === task.id)) {
      staleTasks.push({
        id: task.id,
        action: task.action,
        severity: task.severity,
        staleReason: task.staleReason,
      })
    }
  }

  // —— 人工复核探针（另起一个隔离项目）——
  const reviewProbe = options.runReviewProbe
    ? await runReviewProbe(ctx, client, token, options.operatorId)
    : null

  const result: G3BlockedChainResult = {
    changeSet,
    reissuedChangeSet,
    assessment,
    blocked,
    blockedLines: blockerLines(blocked),
    afterDisposition,
    stabilized,
    versionMismatch,
    mismatchStatus: mismatchOutcome.status,
    mismatchCode: mismatchOutcome.code,
    finalStabilized,
    checkBefore,
    checkAfter,
    disposition,
    dismissAttempts,
    transitions,
    staleTasks,
    reissuedTaskIds,
    replacedVersionId: replaced.currentVersionId ?? '',
    reviewProbe,
    verdicts: [],
  }

  result.verdicts = [
    verdictBlockedRecovery(result),
    verdictAC0909({
      issueId: disposition?.issueId ?? '',
      dismissStatus: disposition?.dismissStatus ?? 0,
      dismissCode: disposition?.dismissCode ?? '',
      resolveStatus: disposition?.resolveStatus ?? 0,
      dispositionAction: disposition?.dispositionAction ?? '',
      checkStatusBefore: checkBefore.status,
      checkStatusAfter: checkAfter.status,
      issueCarriesDisposition: disposition?.carriesDisposition ?? false,
      impactTaskStatus: tasksAfterApply.find((task) => task.severity === 'blocking')?.status ?? '',
      stabilizationStatus: afterDisposition.status,
      stabilizationBlockerCodes: afterDisposition.blockers.map((blocker) => blocker.code),
    }),
    verdictAC0912({
      mismatchHttpStatus: mismatchOutcome.status,
      mismatchCode: mismatchOutcome.code,
      stabilizeStatus: versionMismatch.status,
      stabilizeBlockerCodes: versionMismatch.blockers.map((blocker) => blocker.code),
      staleTaskIds: versionMismatch.staleTaskIds,
      reissuedTaskIds,
      reissuedTaskStatuses: reissuedTasks.map((task) => task.status),
      finalStabilizeStatus: finalStabilized.status,
      finalBlockerCount: finalStabilized.blockers.length,
    }),
    verdictAC0907({
      taskAction: reviewProbe?.taskAction ?? 'REVIEW_REQUIRED',
      targetKind: reviewProbe?.targetKind ?? 'ResearchClaim',
      completeWithoutReviewStatus: reviewProbe?.completeWithoutReviewStatus ?? 0,
      completeWithoutReviewCode: reviewProbe?.completeWithoutReviewCode ?? '',
      completeWithReviewStatus: reviewProbe?.completeWithReviewStatus ?? 0,
      completeWithReviewAccepted: reviewProbe?.completeWithReviewAccepted ?? false,
      modelSideCovered: reviewProbe?.modelSideCovered ?? false,
    }),
    verdictDismissBoundary({
      blockingDismissStatus: dismissAttempts.find((item) => item.severity === 'blocking')?.status ?? 0,
      blockingDismissCode: dismissAttempts.find((item) => item.severity === 'blocking')?.code ?? '',
      nonBlockingDismissStatus: dismissAttempts.find((item) => item.severity !== 'blocking')?.status ?? 0,
      nonBlockingDismissFinalStatus:
        dismissAttempts.find((item) => item.severity !== 'blocking')?.finalStatus ?? '',
    }),
    verdictStaleTask(result),
  ]
  ctx.verdicts = [...(ctx.verdicts ?? []), ...result.verdicts]
  return result
}

/**
 * 人工复核探针（AC-G3-07 的那半段）：另起一个隔离项目。
 *
 * 复核类任务会阻断稳定化，放在主链里会让「闭环后转为已稳定」走不到，
 * 所以这里用**另一个项目**单独跑：
 * 1. 人工撰写一条研究主张（不经过模型）；
 * 2. 用依赖边把它挂到「研究条件需人工复核」的影响上；
 * 3. 应用变更集，产生一条 `REVIEW_REQUIRED` 的复核任务；
 * 4. **不带** `human_decision` 去关闭 → 服务端拒绝（异步作业成功也关不掉）；
 * 5. 把主张更新到当前规格修订后，**带** `human_decision` 再关闭 → 人工复核闭环。
 */
export async function runReviewProbe(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  operatorId: string,
): Promise<G3ReviewProbeFact> {
  const { project, chapters } = await runProjectChain(ctx, client, token, {
    projectName: `G3 验收台-6 人工复核探针（运行 ${ctx.runId}）`,
    idemPrefix: 'review-probe-',
  })
  const method = chapters.find((chapter) => chapter.sectionId === 'method')
  if (!method) {
    throw new Error('人工复核探针需要模板激活后的 method 章节')
  }
  const state = await readProjectState(ctx, client, token, project.id, G3_BLOCKED_STEPS.readProject)

  const claim = await saveResearchClaim(ctx, client, token, project.id, {
    stepName: G3_BLOCKED_STEPS.reviewProbeClaim,
    idemLabel: 'blocked-review-claim',
    expectedProjectVersion: state.projectVersion,
    expectedContextRevision: state.currentContextRevision,
    text: 'G3 验收台-6：研究条件变化后需要人工复核的研究主张。',
    scope: '研究对象',
    reason: 'G3 验收台-6 人工复核探针：人工撰写并确认的研究主张',
  })

  await createDependencyEdge(ctx, client, token, project.id, {
    stepName: G3_BLOCKED_STEPS.reviewProbeEdge,
    idemLabel: 'blocked-review-edge',
    expectedContextRevision: state.currentContextRevision,
    source: { kind: 'ProjectSpec', id: project.id, version: String(state.specRevision) },
    target: { kind: 'ResearchClaim', id: claim.id, version: String(claim.version) },
    relation: 'spec_requires_claim_review',
    action: 'REVIEW_REQUIRED',
    origin: 'user',
    reason: '研究条件变化后研究主张需要人工复核',
    confidence: 1,
  })

  const changeSet = await createChangeSet(ctx, client, token, project.id, {
    stepName: G3_BLOCKED_STEPS.reviewProbeChangeSet,
    idemLabel: 'blocked-review-change-set',
    expectedContextRevision: state.currentContextRevision,
    fields: {
      research_goal: {
        old_value: state.spec.research_goal ?? '',
        new_value: `${state.spec.research_goal ?? ''}（G3 验收台-6 人工复核探针）`,
      },
    },
    affectedChapterIds: [method.id],
    reason: 'G3 验收台-6 人工复核探针：研究条件变化',
  })
  await assessChangeSet(ctx, client, token, project.id, changeSet.id)
  await applyChangeSet(ctx, client, token, project.id, changeSet.id, {
    stepName: G3_BLOCKED_STEPS.reviewProbeApply,
    idemLabel: 'blocked-review-apply',
  })

  const tasks = await listImpactTaskDetails(ctx, client, token, project.id, G3_BLOCKED_STEPS.readTasks)
  const reviewTask = tasks.find(
    (task) => task.target.kind === 'ResearchClaim' && task.status !== 'DONE' && task.status !== 'STALE',
  )
  if (!reviewTask) {
    throw new Error('人工复核探针没有拿到研究主张上的复核任务')
  }

  const assigned = await disposeTask(ctx, client, token, project.id, reviewTask, {
    stepName: G3_BLOCKED_STEPS.reviewProbeAssign,
    idemLabel: 'blocked-review-assign',
    action: 'assign',
    reason: 'G3 验收台-6 人工复核探针：指派给当前操作者',
    assigneeId: operatorId,
  })
  const started = await disposeTask(
    ctx,
    client,
    token,
    project.id,
    { ...reviewTask, version: assigned.version },
    {
      stepName: G3_BLOCKED_STEPS.reviewProbeStart,
      idemLabel: 'blocked-review-start',
      action: 'start',
      reason: 'G3 验收台-6 人工复核探针：开始处置',
    },
  )

  // 不带 human_decision：服务端必须拒绝（异步作业成功不能关闭人工复核任务）
  const withoutReview = await disposeTask(
    ctx,
    client,
    token,
    project.id,
    { ...reviewTask, version: started.version },
    {
      stepName: G3_BLOCKED_STEPS.reviewProbeCompleteNoReview,
      idemLabel: 'blocked-review-complete-no-review',
      action: 'complete',
      reason: 'G3 验收台-6 人工复核探针：缺少人工复核决策',
      closureEvidence: [{ kind: 'ResearchClaim', id: claim.id, version: String(claim.version) }],
      expectedStatus: [200, 400, 403, 409, 422],
    },
  )

  // 把主张更新到当前规格修订，再带 human_decision 关闭。
  const stateAfter = await readProjectState(ctx, client, token, project.id, G3_BLOCKED_STEPS.readProject)
  let claimVersion = claim.version
  let completeWithReviewStatus = 0
  let completeWithReviewCode = ''
  let completeWithReviewAccepted = false
  try {
    const updated = await saveResearchClaim(ctx, client, token, project.id, {
      stepName: G3_BLOCKED_STEPS.updateClaim,
      idemLabel: 'blocked-review-claim-update',
      expectedProjectVersion: stateAfter.projectVersion,
      expectedContextRevision: stateAfter.currentContextRevision,
      expectedVersion: claim.version,
      claimId: claim.id,
      text: 'G3 验收台-6：研究条件变化后已经人工复核的研究主张。',
      scope: '研究对象',
      reason: 'G3 验收台-6 人工复核探针：复核后更新研究主张',
    })
    claimVersion = updated.version
    const withReview = await disposeTask(
      ctx,
      client,
      token,
      project.id,
      { ...reviewTask, version: started.version },
      {
        stepName: G3_BLOCKED_STEPS.reviewProbeCompleteWithReview,
        idemLabel: 'blocked-review-complete-with-review',
        action: 'complete',
        reason: 'G3 验收台-6 人工复核探针：责任人已查看并复核',
        closureEvidence: [{ kind: 'ResearchClaim', id: claim.id, version: String(claimVersion) }],
        humanDecision: 'reviewed',
        expectedStatus: [200, 400, 403, 409, 422],
      },
    )
    completeWithReviewStatus = withReview.status
    completeWithReviewCode = withReview.code
    completeWithReviewAccepted = withReview.task?.status === 'DONE'
  } catch (error) {
    completeWithReviewCode = error instanceof Error ? error.message : String(error)
  }

  return {
    projectId: project.id,
    claimId: claim.id,
    taskId: reviewTask.id,
    taskAction: reviewTask.action,
    targetKind: reviewTask.target.kind,
    completeWithoutReviewStatus: withoutReview.status,
    completeWithoutReviewCode: withoutReview.code,
    completeWithReviewStatus,
    completeWithReviewCode,
    completeWithReviewAccepted,
    modelSideCovered: false,
  }
}

/* ------------------------------------------------------------------ *
 * 判定
 * ------------------------------------------------------------------ */

/** 阻断与恢复闭环判定。 */
export function verdictBlockedRecovery(result: G3BlockedChainResult): G3Verdict {
  const blockedWithReasons = result.blocked.status === 'BLOCKED' && result.blocked.blockers.length > 0
  const stillBlockedAfterDisposition = result.afterDisposition.status === 'BLOCKED'
  const stabilized = result.stabilized.status === 'STABILIZED' && result.stabilized.blockers.length === 0
  const holds = blockedWithReasons && stillBlockedAfterDisposition && stabilized
  return {
    id: 'G3.6-BLOCKED-RECOVERY',
    title: '稳定化阻断与恢复：阻断原因原样展示 → 闭环 → 再次已稳定',
    holds,
    detail: holds
      ? `未闭环的阻断影响存在时，稳定化以 200 成功响应返回 ${result.blocked.status}，` +
        `带出 ${result.blocked.blockers.length} 条阻断原因（${result.blockedLines.join('；')}）；` +
        `规则问题被处置之后再次稳定化仍为 ${result.afterDisposition.status}` +
        `（${result.afterDisposition.blockers.length} 条阻断原因），项目仍然不能稳定；` +
        `阻断影响按「指派 → 开始 → 复写并重新确认 → 以闭环证据完成」闭环之后，` +
        `稳定化转为 ${result.stabilized.status}。`
      : `不成立：阻断态=${result.blocked.status}（原因 ${result.blocked.blockers.length} 条）、` +
        `处置后=${result.afterDisposition.status}、闭环后=${result.stabilized.status}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/stabilize（阻断态）→ HTTP 200 · ` +
        `status=${result.blocked.status} · 阻断原因 ${result.blocked.blockers.length} 条`,
      ...result.blockedLines.map((line) => `阻断原因：${line}`),
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/stabilize（规则问题处置后）→ HTTP 200 · ` +
        `status=${result.afterDisposition.status} · 阻断原因 ${result.afterDisposition.blockers.length} 条`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/stabilize（阻断项闭环后）→ HTTP 200 · ` +
        `status=${result.stabilized.status} · checked_at=${result.stabilized.checkedAt}`,
    ],
  }
}

/** AC-G3-09 的判定输入。 */
export interface G3AC0909Input {
  issueId: string
  dismissStatus: number
  dismissCode: string
  resolveStatus: number
  dispositionAction: string
  checkStatusBefore: string
  checkStatusAfter: string
  issueCarriesDisposition: boolean
  impactTaskStatus: string
  stabilizationStatus: string
  stabilizationBlockerCodes: string[]
}

/** AC-G3-09：规则问题与任务分离。 */
export function verdictAC0909(input: G3AC0909Input): G3Verdict {
  const disposed = input.resolveStatus === 201 && input.issueCarriesDisposition
  const taskStillOpen =
    input.impactTaskStatus !== '' && input.impactTaskStatus !== 'DONE' && input.impactTaskStatus !== 'DISMISSED'
  const projectStillBlocked = input.stabilizationStatus === 'BLOCKED'
  // 判定卡只对**本轮实跑得出来的那半段**负责：
  // 「处置规则问题不闭环影响任务、项目仍不能稳定」是实跑出来的；
  // 「问题不再阻断当次求值」在当前后端上不成立（处置只是工作流元数据），
  // 由矩阵规则表按 `PASS (follow-up)` + 跟进 Issue 处理。
  const holds = disposed && taskStillOpen && projectStillBlocked
  const evaluationUnblocked = input.checkStatusBefore !== input.checkStatusAfter
  return {
    id: 'AC-G3-09',
    title: '规则问题与任务分离：处置规则问题不闭环影响任务，项目仍不能稳定',
    holds,
    detail: holds
      ? `规则问题 ${input.issueId} 被人工处置（HTTP ${input.resolveStatus}，问题已带上处置）之后，` +
        `对应的 ImpactTask 仍为 ${input.impactTaskStatus}，稳定化仍为 ${input.stabilizationStatus}` +
        `（${input.stabilizationBlockerCodes.join('、')}）——项目仍不能稳定。` +
        `**未成立那半段**：「问题不再阻断当次求值」在当前后端上不成立：` +
        `处置后重检的结论仍为 ${input.checkStatusAfter}（处置前 ${input.checkStatusBefore}），` +
        `服务端把人工处置当工作流元数据，不改求值结论` +
        (input.dismissStatus >= 400
          ? `；且对阻断级规则问题调用「接受不处理」（dismiss）本轮返回 HTTP ${input.dismissStatus}` +
            `（code=${input.dismissCode || '（未给码）'}）。`
          : '。')
      : `不成立：规则问题处置=${input.resolveStatus}（问题带处置=${input.issueCarriesDisposition}）、` +
        `求值结论 ${input.checkStatusBefore}→${input.checkStatusAfter}、影响任务=${input.impactTaskStatus}、` +
        `稳定化=${input.stabilizationStatus}。` +
        (input.dismissStatus >= 400
          ? `本轮实测：对阻断级规则问题调用「接受不处理」（dismiss）返回 HTTP ${input.dismissStatus}` +
            `（code=${input.dismissCode || '（未给码）'}）。`
          : ''),
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/checks → HTTP 200 · status=${input.checkStatusBefore}`,
      `POST /api/v1/lingdoc/projects/{projectId}/issues/{issueId}/disposition（dismiss）→ HTTP ${input.dismissStatus} · ` +
        `code=${input.dismissCode || '（未给码）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/issues/{issueId}/disposition（resolve）→ HTTP ${input.resolveStatus} · ` +
        `action=${input.dispositionAction || '（未落处置）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/checks（处置后重检）→ HTTP 200 · status=${input.checkStatusAfter} · ` +
        `问题带处置=${input.issueCarriesDisposition}`,
      `GET /api/v1/lingdoc/projects/{projectId}/impact-tasks → HTTP 200 · 对应影响任务 status=${input.impactTaskStatus}`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/stabilize → HTTP 200 · status=${input.stabilizationStatus} · ` +
        `阻断原因码 ${input.stabilizationBlockerCodes.join('、')}`,
      `求值是否被处置放行：${evaluationUnblocked ? '是' : '否（当前后端不改求值结论）'}`,
    ],
  }
}

/** AC-G3-12 的判定输入。 */
export interface G3AC0912Input {
  /** 目标对象版本被替换后那次稳定化的 HTTP 状态码。 */
  mismatchHttpStatus: number
  /** 那次稳定化的错误码。 */
  mismatchCode: string
  /** 那次稳定化返回的判定状态（被拒时为空串）。 */
  stabilizeStatus: string
  stabilizeBlockerCodes: string[]
  staleTaskIds: string[]
  reissuedTaskIds: string[]
  reissuedTaskStatuses: string[]
  finalStabilizeStatus: string
  finalBlockerCount: number
}

/** AC-G3-12：稳定化重新读取。 */
export function verdictAC0912(input: G3AC0912Input): G3Verdict {
  // 「保持阻断」有两种服务端表现，两种都不放行这一版修订：
  // 200 + status=BLOCKED（带阻断原因），或者在重新读取闭环证据时直接拒掉（409/422）。
  const notReleased =
    input.stabilizeStatus === 'BLOCKED' ||
    input.mismatchHttpStatus === 409 ||
    input.mismatchHttpStatus === 422
  const reissued = input.reissuedTaskIds.length > 0
  const recovered = input.finalStabilizeStatus === 'STABILIZED' && input.finalBlockerCount === 0
  const holds = notReleased && reissued && recovered
  const how = input.stabilizeStatus === 'BLOCKED'
    ? `保持 ${input.stabilizeStatus}（阻断原因码 ${input.stabilizeBlockerCodes.join('、')}）`
    : `被服务端按 HTTP ${input.mismatchHttpStatus} 拒掉（code=${input.mismatchCode || '（未给码）'}）`
  return {
    id: 'AC-G3-12',
    title: '稳定化重新读取：版本不匹配时保持阻断并重投对应任务',
    holds,
    detail: holds
      ? `目标对象在任务完成之后被新版本替换，稳定化重新读取时发现版本不匹配，${how}——这一版修订没有被放行；` +
        `随后重投了 ${input.reissuedTaskIds.length} 条对应任务（${input.reissuedTaskIds.join('、')}），` +
        `闭环之后稳定化转为 ${input.finalStabilizeStatus}。`
      : `不成立：版本不匹配后稳定化=${input.stabilizeStatus || `HTTP ${input.mismatchHttpStatus}`}` +
        `（${input.stabilizeBlockerCodes.join('、')}）、重投任务 ${input.reissuedTaskIds.length} 条、` +
        `最终稳定化=${input.finalStabilizeStatus}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/chapters/{id}/versions → HTTP 201 · 目标对象被新版本替换`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/stabilize（目标版本被替换后）→ HTTP ` +
        `${input.mismatchHttpStatus} · status=${input.stabilizeStatus || '（未放行）'} · ` +
        `code=${input.mismatchCode || '（无）'} · 阻断原因码 ${input.stabilizeBlockerCodes.join('、') || '（无）'} · ` +
        `失效任务 ${input.staleTaskIds.length} 条`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets → HTTP 201 · 重投变更集`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/apply → HTTP 200 · 重投任务 ` +
        `${input.reissuedTaskIds.length} 条：${input.reissuedTaskIds.join('、') || '（无）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/stabilize（重投闭环后）→ HTTP 200 · ` +
        `status=${input.finalStabilizeStatus} · 阻断原因 ${input.finalBlockerCount} 条`,
    ],
  }
}

/** AC-G3-07 的判定输入。 */
export interface G3AC0907Input {
  taskAction: string
  targetKind: string
  completeWithoutReviewStatus: number
  completeWithoutReviewCode: string
  completeWithReviewStatus: number
  completeWithReviewAccepted: boolean
  modelSideCovered: boolean
}

/** AC-G3-07：证据复核不能自动确认。 */
export function verdictAC0907(input: G3AC0907Input): G3Verdict {
  const refusedWithoutReview = input.completeWithoutReviewStatus >= 400
  const acceptedWithReview = input.completeWithReviewAccepted
  // 判定卡只对本轮实跑得出来的那半段负责：
  // 「异步作业成功不关闭人工复核任务」是实跑出来的；
  // 「证据重检返回新候选」需要真实模型（见 #243），由矩阵规则表按
  // `PASS (follow-up)` + 跟进 Issue 处理。
  const holds = refusedWithoutReview && acceptedWithReview
  return {
    id: 'AC-G3-07',
    title: '证据复核不能自动确认：人工复核任务不能被异步结果关掉',
    holds,
    detail: holds
      ? `复核任务（${input.taskAction} / ${input.targetKind}）缺少人工复核决策时被服务端按 ` +
        `HTTP ${input.completeWithoutReviewStatus} 拒掉（code=${input.completeWithoutReviewCode || '（未给码）'}）；` +
        `带上 human_decision=reviewed 之后才允许闭环（HTTP ${input.completeWithReviewStatus}）——` +
        `异步作业成功关不掉人工复核任务。` +
        `**未覆盖那半段**：「证据重检返回新候选」需要真实模型，造证据卡要走模型侧（见 #243）。`
      : `不成立：缺少人工复核决策时服务端按 HTTP ${input.completeWithoutReviewStatus} ` +
        `（code=${input.completeWithoutReviewCode || '（未给码）'}）拒掉；` +
        `带人工复核决策的关闭=${input.completeWithReviewStatus}` +
        `${input.completeWithReviewAccepted ? '（已闭环）' : '（未闭环）'}。`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/impact-tasks/{taskId}/actions（缺 human_decision）→ HTTP ` +
        `${input.completeWithoutReviewStatus} · code=${input.completeWithoutReviewCode || '（未给码）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/impact-tasks/{taskId}/actions（带 human_decision=reviewed）→ HTTP ` +
        `${input.completeWithReviewStatus} · ${input.completeWithReviewAccepted ? '已闭环' : '未闭环'}`,
      'POST /api/v1/lingdoc/projects/{projectId}/evidence-cards（模型侧新候选）→ 本轮未执行（需真实模型，见 #243）',
    ],
  }
}

/** 接受不处理边界的判定输入。 */
export interface G3DismissBoundaryInput {
  blockingDismissStatus: number
  blockingDismissCode: string
  nonBlockingDismissStatus: number
  nonBlockingDismissFinalStatus: string
}

/** 接受不处理只对非阻断影响可达。 */
export function verdictDismissBoundary(input: G3DismissBoundaryInput): G3Verdict {
  const blockingRefused = input.blockingDismissStatus >= 400
  const nonBlockingAccepted =
    input.nonBlockingDismissStatus === 200 && input.nonBlockingDismissFinalStatus === 'DISMISSED'
  const holds = blockingRefused && nonBlockingAccepted
  return {
    id: 'G3-DISMISS-BOUNDARY',
    title: '接受不处理只对非阻断影响可达：阻断级只能以闭环证据完成',
    holds,
    detail: holds
      ? `阻断级影响的「接受本次不处理」被服务端按 HTTP ${input.blockingDismissStatus}` +
        `（code=${input.blockingDismissCode || '（未给码）'}）拒掉；` +
        `非阻断级影响接受后落到 ${input.nonBlockingDismissFinalStatus}。`
      : `不成立：阻断级=${input.blockingDismissStatus}、非阻断级=${input.nonBlockingDismissStatus}` +
        `（${input.nonBlockingDismissFinalStatus || '（未读到）'}）`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/impact-tasks/{taskId}/actions（阻断级 dismiss）→ HTTP ` +
        `${input.blockingDismissStatus} · code=${input.blockingDismissCode || '（未给码）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/impact-tasks/{taskId}/actions（非阻断级 dismiss）→ HTTP ` +
        `${input.nonBlockingDismissStatus} · status=${input.nonBlockingDismissFinalStatus || '（未读到）'}`,
    ],
  }
}

/** 失效任务语义的判定。 */
export function verdictStaleTask(result: G3BlockedChainResult): G3Verdict {
  const reported = result.staleTasks.length > 0
  const reportedByStabilization =
    result.finalStabilized.staleTaskIds.length > 0 || result.versionMismatch.staleTaskIds.length > 0
  const notBlocking = result.finalStabilized.status === 'STABILIZED'
  const holds = reported && notBlocking
  return {
    id: 'G3-STALE-TASK',
    title: '失效任务：既不算闭环，也不阻断稳定化，只被单独上报',
    holds,
    detail: holds
      ? `本轮出现 ${result.staleTasks.length} 条失效任务（${result.staleTasks
          .map((task) => `${task.id}：${task.staleReason || '（服务端未给原因）'}`)
          .join('、')}）；` +
        `稳定化把它们单独上报（stale_task_ids ${result.versionMismatch.staleTaskIds.length} → ` +
        `${result.finalStabilized.staleTaskIds.length} 条），状态仍是 ${result.finalStabilized.status}——` +
        `失效任务既不算闭环，也没有阻断稳定化。`
      : `不成立：失效任务 ${result.staleTasks.length} 条 · 稳定化=${result.finalStabilized.status}`,
    evidence: [
      `GET /api/v1/lingdoc/projects/{projectId}/impact-tasks → HTTP 200 · 失效任务 ` +
        `${result.staleTasks.map((task) => `${task.id}(${task.staleReason || '未给原因'})`).join('、') || '（无）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{id}/stabilize → HTTP 200 · ` +
        `status=${result.finalStabilized.status} · stale_task_ids=${result.finalStabilized.staleTaskIds.length} 条 · ` +
        `单独上报=${reportedByStabilization}`,
    ],
  }
}

/* ------------------------------------------------------------------ *
 * 页面快照
 * ------------------------------------------------------------------ */

/**
 * 阻断与恢复链的页面快照。
 *
 * 只留页面要展示的字段，且全部可 JSON 序列化：它跟着运行摘要落进浏览器本地存储。
 */
export interface G3BlockedSnapshot {
  verdicts: G3Verdict[]
  blocked: G3StabilizationFact
  blockedLines: string[]
  afterDisposition: G3StabilizationFact
  stabilized: G3StabilizationFact
  versionMismatch: G3StabilizationFact
  /** 版本被替换后那次稳定化的 HTTP 状态码与错误码。 */
  mismatchStatus: number
  mismatchCode: string
  finalStabilized: G3StabilizationFact
  checkBefore: G3CheckWithIssuesFact
  checkAfter: G3CheckWithIssuesFact
  disposition: G3RuleIssueDispositionRecord | null
  dismissAttempts: G3DismissAttempt[]
  transitions: G3TaskTransition[]
  staleTasks: { id: string; action: string; severity: string; staleReason: string }[]
  reissuedTaskIds: string[]
  reviewProbe: G3ReviewProbeFact | null
}

/** 把链结果压成页面快照。 */
export function toBlockedSnapshot(result: G3BlockedChainResult): G3BlockedSnapshot {
  return {
    verdicts: result.verdicts,
    blocked: result.blocked,
    blockedLines: result.blockedLines,
    afterDisposition: result.afterDisposition,
    stabilized: result.stabilized,
    versionMismatch: result.versionMismatch,
    mismatchStatus: result.mismatchStatus,
    mismatchCode: result.mismatchCode,
    finalStabilized: result.finalStabilized,
    checkBefore: result.checkBefore,
    checkAfter: result.checkAfter,
    disposition: result.disposition,
    dismissAttempts: result.dismissAttempts,
    transitions: result.transitions,
    staleTasks: result.staleTasks,
    reissuedTaskIds: result.reissuedTaskIds,
    reviewProbe: result.reviewProbe,
  }
}
