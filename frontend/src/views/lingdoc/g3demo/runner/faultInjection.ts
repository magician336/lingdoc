/**
 * G3 验收台-11（#295）· 受控故障注入的**可观测口径**。
 *
 * ## 为什么这条链不新增任何产品端点
 *
 * 受控故障注入是**组合根按环境变量装配的夹具**，不是产品能力：
 * `LINGDOC_FAULT_INJECTION` + `LINGDOC_FAULT_TENANT_ID` 缺一即不装配
 * （`internal/container/lingdoc_fault_injection.go`）。所以：
 *
 * - **禁止**为了探测它而新增产品端点——那会把「如何制造写入失败」这条测试知识
 *   泄漏进公开契约。
 * - 台面的做法只能是**观测**：从 apply 的真实 HTTP 响应里判断后端是否装配了注入。
 *   命中注入时服务端返回 **503** 且错误码是 **`injected_fault`**
 *   （`internal/lingdoc/workspacecore/fault_injection.go:139-146`）；
 *   未装配时 apply 正常成功。
 *
 * ## 判定口径
 *
 * - **装配了** → 回滚可观测，可判 `PASS`（前提是回滚证据完整：项目 revision、
 *   模板引用、ImpactTask 集合、业务审计事件均未变）。
 * - **未装配** → 该行记 `NOT RUN`，并指向夹具证据文件
 *   `docs/08-本轮实施方案/G3-缺口收口-7-验证报告-20261020.json`，
 *   **不得**判 `PASS`。
 *
 * 这条口径同时写进代码注释与落盘证据：注入是夹具，不是端点；页面无法触发，
 * 只能观测。
 */

import { errorCodeOf, type G3HttpClient } from './httpClient'
import {
  assessChangeSet,
  createChangeSet,
  readChangeSet,
  readImpactAssessment,
  readProjectState,
  type G3ChangeSetFact,
  type G3ImpactAssessmentFact,
  type G3ProjectState,
} from './changeset'
import { makeIdempotencyKey, runStep, type G3RunContext } from './runContext'
import type { G3Verdict } from './changeset'

/** 步骤名。矩阵规则表按这些名字认「这一行跑到了没有」。 */
export const G3_FAULT_STEPS = {
  readProjectBefore: '故障注入-读项目（前）',
  readTasksBefore: '故障注入-读影响任务（前）',
  readAuditBefore: '故障注入-读审计（前）',
  createChangeSet: '故障注入-建变更集',
  assessChangeSet: '故障注入-评估变更集',
  readAssessment: '故障注入-读影响评估',
  apply: '故障注入-应用变更集',
  readProjectAfter: '故障注入-读项目（后）',
  readTasksAfter: '故障注入-读影响任务（后）',
  readAuditAfter: '故障注入-读审计（后）',
  readChangeSetAfter: '故障注入-读回变更集',
} as const

/** 注入点的可读名（与 `core.FaultPoint` 对齐）。 */
export const G3_FAULT_POINTS = ['impact_task_write', 'audit_write', 'authorization'] as const

/** 注入命中时服务端给出的错误码。 */
export const G3_INJECTED_FAULT_CODE = 'injected_fault'

/** 注入命中时服务端给出的状态码。 */
export const G3_INJECTED_FAULT_STATUS = 503

/** 未装配注入时的夹具证据文件（已落证的实跑事实）。 */
export const G3_FAULT_FIXTURE_EVIDENCE =
  'docs/08-本轮实施方案/G3-缺口收口-7-验证报告-20261020.json'

/** 项目与模板引用的快照。 */
export interface G3FaultProjectSnapshot {
  projectVersion: number
  specRevision: number
  currentContextRevision: number
  templateId: string
  templateCopyVersion: number
  rulesetHash: string
}

/** 影响任务集合的快照（只取条数与 id 集合）。 */
export interface G3FaultTaskSnapshot {
  count: number
  ids: string[]
}

/** 业务审计事件的快照（只取条数与动作集合）。 */
export interface G3FaultAuditSnapshot {
  count: number
  actions: string[]
}

/** 一次 apply 的观测结果。 */
export interface G3FaultApplyObservation {
  /** apply 的 HTTP 状态码。 */
  status: number
  /** apply 的错误码（成功时为空）。 */
  code: string
  /** 后端**本轮是否装配了**受控故障注入。 */
  faultInjectionAssembled: boolean
  /** 变更集应用后的状态。 */
  changeSetStatus: string
  /** 读回证明：项目 revision / 模板引用是否未变。 */
  projectUnchanged: boolean
  /** 读回证明：ImpactTask 集合是否未变。 */
  tasksUnchanged: boolean
  /** 读回证明：业务审计事件是否未变。 */
  auditUnchanged: boolean
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

/** 把项目状态读成「回滚可比对」的快照。 */
export function toFaultProjectSnapshot(project: G3ProjectState): G3FaultProjectSnapshot {
  return {
    projectVersion: project.projectVersion,
    specRevision: project.specRevision,
    currentContextRevision: project.currentContextRevision,
    templateId: project.templateId ?? '',
    templateCopyVersion: project.templateCopyVersion ?? 0,
    rulesetHash: project.rulesetHash ?? '',
  }
}

/** 两个项目快照是否逐项相同。 */
export function sameProjectSnapshot(
  before: G3FaultProjectSnapshot,
  after: G3FaultProjectSnapshot,
): boolean {
  return (
    before.projectVersion === after.projectVersion &&
    before.specRevision === after.specRevision &&
    before.currentContextRevision === after.currentContextRevision &&
    before.templateId === after.templateId &&
    before.templateCopyVersion === after.templateCopyVersion &&
    before.rulesetHash === after.rulesetHash
  )
}

/**
 * 从 apply 的响应里判断后端**是否装配了注入**。
 *
 * 这是本条链唯一允许的探测方式：注入是夹具，没有产品端点可问。
 * 命中注入 = HTTP 503 且错误码 `injected_fault`。
 */
export function detectFaultInjection(status: number, code: string): boolean {
  return status === G3_INJECTED_FAULT_STATUS && code === G3_INJECTED_FAULT_CODE
}

/** 读回影响任务集合。 */
export async function readFaultImpactTasks(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string,
): Promise<G3FaultTaskSnapshot> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, '/impact-tasks'),
    token,
    conclusion: '影响任务集合已读回（HTTP {status}）',
    detail: (body) => {
      const list = Array.isArray(objectOf(body).data) ? (objectOf(body).data as unknown[]) : []
      return { task_count: list.length }
    },
  })
  const list = Array.isArray(data) ? (data as unknown[]) : []
  return {
    count: list.length,
    ids: list.map((item) => str(objectOf(item).id)).sort(),
  }
}

/** 读回业务审计事件集合。 */
export async function readFaultAuditEvents(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string,
): Promise<G3FaultAuditSnapshot> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, '/audit'),
    token,
    conclusion: '业务审计事件已读回（HTTP {status}）',
    detail: (body) => {
      const list = Array.isArray(objectOf(body).data) ? (objectOf(body).data as unknown[]) : []
      return {
        event_count: list.length,
        actions: list.map((item) => str(objectOf(item).action)).sort(),
      }
    },
  })
  const list = Array.isArray(data) ? (data as unknown[]) : []
  return {
    count: list.length,
    actions: list.map((item) => str(objectOf(item).action)).sort(),
  }
}

/** 链的输入。 */
export interface G3FaultChainOptions {
  projectId: string
  /** 被改动的研究条件字段（默认 research_subject）。 */
  specField?: string
  /** 受影响章节。 */
  affectedChapterIds?: string[]
  /** 变更理由。 */
  reason?: string
}

/** 链的结果。 */
export interface G3FaultChainResult {
  projectBefore: G3FaultProjectSnapshot
  projectAfter: G3FaultProjectSnapshot
  tasksBefore: G3FaultTaskSnapshot
  tasksAfter: G3FaultTaskSnapshot
  auditBefore: G3FaultAuditSnapshot
  auditAfter: G3FaultAuditSnapshot
  changeSet: G3ChangeSetFact
  assessment: G3ImpactAssessmentFact
  observation: G3FaultApplyObservation
  verdicts: G3Verdict[]
}

/**
 * 跑一次「受控故障注入」的可观测链。
 *
 * 顺序：读前态 → 建变更集 → 评估 → 读影响评估 → **应用（探测点）** → 读后态。
 * 探测点是 apply：它要么被注入打成 503 + `injected_fault`，要么正常成功。
 */
export async function runFaultInjectionChain(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: G3FaultChainOptions,
): Promise<G3FaultChainResult> {
  const specField = options.specField ?? 'research_subject'
  const affectedChapterIds = options.affectedChapterIds ?? []

  const projectStateBefore = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    G3_FAULT_STEPS.readProjectBefore,
  )
  const projectBefore = toFaultProjectSnapshot(projectStateBefore)
  const tasksBefore = await readFaultImpactTasks(
    ctx,
    client,
    token,
    options.projectId,
    G3_FAULT_STEPS.readTasksBefore,
  )
  const auditBefore = await readFaultAuditEvents(
    ctx,
    client,
    token,
    options.projectId,
    G3_FAULT_STEPS.readAuditBefore,
  )

  // 演示模板把 `research_subject` 与 `research_goal` 都声明成**必填**：
  // 只提交其中一个，变更集会被按 400 `invalid_request` 拒掉（请求字段不符合约定）。
  // 所以这里两个必填字段一起改——改的是合成项目的规格值，不含真实课题信息。
  const fields: Record<string, { old_value: string; new_value: string }> = {}
  for (const key of [specField, 'research_goal']) {
    if (fields[key]) continue
    const oldValue = projectStateBefore.spec[key] ?? ''
    fields[key] = {
      old_value: oldValue,
      new_value: `${oldValue}（G3 验收台-11 受控故障注入观测）`,
    }
  }

  const changeSet = await createChangeSet(ctx, client, token, options.projectId, {
    stepName: G3_FAULT_STEPS.createChangeSet,
    idemLabel: 'fault-change-set',
    expectedContextRevision: projectStateBefore.currentContextRevision,
    fields,
    affectedChapterIds,
    reason: options.reason ?? 'G3 验收台-11：观测受控故障注入是否装配（应用原子性）',
  })
  await assessChangeSet(ctx, client, token, options.projectId, changeSet.id)
  const assessment = await readImpactAssessment(
    ctx,
    client,
    token,
    options.projectId,
    changeSet.id,
    G3_FAULT_STEPS.readAssessment,
  )

  // —— 探测点：apply ——
  // 命中注入时它返回 503 + injected_fault；未装配时它成功。
  // 两种都是**预期之内**的观察结果，所以这一步不抛错，只如实记录。
  const applyResult = await runStep(ctx, client, {
    name: G3_FAULT_STEPS.apply,
    method: 'POST',
    path: projectPath(options.projectId, `/change-sets/${encodeURIComponent(changeSet.id)}/apply`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `fault-apply-${changeSet.id}`),
    expectedStatus: [200, 201, G3_INJECTED_FAULT_STATUS],
    body: {},
    conclusion:
      '应用变更集已完成（HTTP {status}）：注入是组合根夹具，命中时服务端返回 503 + injected_fault',
    detail: (body) => {
      const change = objectOf(objectOf(body).data)
      return {
        change_set_id: change.id,
        status: change.status,
        error_code: errorCodeOf(body),
      }
    },
  })
  const applyStatus = applyResult.response.status
  const applyCode = errorCodeOf(applyResult.response.body)
  const assembled = detectFaultInjection(applyStatus, applyCode)

  const projectStateAfter = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    G3_FAULT_STEPS.readProjectAfter,
  )
  const projectAfter = toFaultProjectSnapshot(projectStateAfter)
  const tasksAfter = await readFaultImpactTasks(
    ctx,
    client,
    token,
    options.projectId,
    G3_FAULT_STEPS.readTasksAfter,
  )
  const auditAfter = await readFaultAuditEvents(
    ctx,
    client,
    token,
    options.projectId,
    G3_FAULT_STEPS.readAuditAfter,
  )
  const changeSetAfter = await readChangeSet(
    ctx,
    client,
    token,
    options.projectId,
    changeSet.id,
    G3_FAULT_STEPS.readChangeSetAfter,
  )

  const observation: G3FaultApplyObservation = {
    status: applyStatus,
    code: applyCode,
    faultInjectionAssembled: assembled,
    changeSetStatus: changeSetAfter.status,
    projectUnchanged: sameProjectSnapshot(projectBefore, projectAfter),
    tasksUnchanged:
      tasksBefore.count === tasksAfter.count &&
      tasksBefore.ids.join(',') === tasksAfter.ids.join(','),
    auditUnchanged:
      auditBefore.count === auditAfter.count &&
      auditBefore.actions.join(',') === auditAfter.actions.join(','),
  }

  const result: G3FaultChainResult = {
    projectBefore,
    projectAfter,
    tasksBefore,
    tasksAfter,
    auditBefore,
    auditAfter,
    changeSet,
    assessment,
    observation,
    verdicts: [],
  }

  result.verdicts = [verdictAcG303(result)]
  ctx.verdicts = [...(ctx.verdicts ?? []), ...result.verdicts]

  return result
}

/** 页面快照。 */
export interface G3FaultSnapshot {
  verdicts: G3Verdict[]
  projectBefore: G3FaultProjectSnapshot
  projectAfter: G3FaultProjectSnapshot
  tasksBefore: G3FaultTaskSnapshot
  tasksAfter: G3FaultTaskSnapshot
  auditBefore: G3FaultAuditSnapshot
  auditAfter: G3FaultAuditSnapshot
  observation: G3FaultApplyObservation
}

/** 把链结果压成页面快照。 */
export function toFaultSnapshot(result: G3FaultChainResult): G3FaultSnapshot {
  return {
    verdicts: result.verdicts,
    projectBefore: result.projectBefore,
    projectAfter: result.projectAfter,
    tasksBefore: result.tasksBefore,
    tasksAfter: result.tasksAfter,
    auditBefore: result.auditBefore,
    auditAfter: result.auditAfter,
    observation: result.observation,
  }
}

/**
 * AC-G3-03 的判定：应用原子性。
 *
 * - 后端**装配了**注入且回滚证据完整（项目 revision / 模板引用 / ImpactTask 集合 /
 *   业务审计事件均未变）→ 判定成立。
 * - 后端**未装配**注入 → 判定不成立，状态记 `NOT RUN`，并指向夹具证据文件
 *   `docs/08-本轮实施方案/G3-缺口收口-7-验证报告-20261020.json`。
 *   **不得**判 `PASS`：没装注入就没有观测到原子性边界。
 */
export function verdictAcG303(result: G3FaultChainResult): G3Verdict {
  const assembled = result.observation.faultInjectionAssembled
  const rollbackComplete =
    result.observation.projectUnchanged &&
    result.observation.tasksUnchanged &&
    result.observation.auditUnchanged
  const holds = assembled && rollbackComplete

  return {
    id: 'AC-G3-03',
    title: '应用原子性：写入失败或权限失效时项目保持原 revision 与旧模板引用',
    holds,
    detail: holds
      ? `本轮后端**装配了**受控故障注入：apply 返回 HTTP ${result.observation.status} ` +
        `（code=${result.observation.code}）。读回证明回滚完整：` +
        `项目 revision ${result.projectBefore.currentContextRevision} → ${result.projectAfter.currentContextRevision}` +
        `（项目版本 ${result.projectBefore.projectVersion} → ${result.projectAfter.projectVersion}，` +
        `规格修订 ${result.projectBefore.specRevision} → ${result.projectAfter.specRevision}）、` +
        `模板引用（${result.projectBefore.templateId}@v${result.projectBefore.templateCopyVersion} → ` +
        `${result.projectAfter.templateId}@v${result.projectAfter.templateCopyVersion}）、` +
        `ImpactTask 集合（${result.tasksBefore.count} → ${result.tasksAfter.count} 条）、` +
        `业务审计事件（${result.auditBefore.count} → ${result.auditAfter.count} 条）均未变。`
      : assembled
        ? `本轮后端装配了注入（apply 返回 HTTP ${result.observation.status}，code=${result.observation.code}），` +
          `但回滚证据不完整：项目未变=${result.observation.projectUnchanged}、` +
          `任务集合未变=${result.observation.tasksUnchanged}、` +
          `审计事件未变=${result.observation.auditUnchanged}——不能据此判定原子性成立。`
        : `本轮后端**未装配**受控故障注入：apply 返回 HTTP ${result.observation.status}` +
          `（code=${result.observation.code || '（无）'}），没有观测到写入失败或权限失效，` +
          `因此这一行**记 NOT RUN**，不判 PASS。` +
          `注入是组合根按环境变量装配的夹具（LINGDOC_FAULT_INJECTION + LINGDOC_FAULT_TENANT_ID，缺一不装配），` +
          `不是产品端点，页面无法触发，只能观测；已落证的夹具实跑事实见 ${G3_FAULT_FIXTURE_EVIDENCE}。`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{changeSetId}/apply → HTTP ${result.observation.status} · ` +
        `code=${result.observation.code || '（无）'} · 本轮后端装配了注入=${assembled ? '是' : '否'}`,
      `GET /api/v1/lingdoc/projects/{projectId} → HTTP 200 · ` +
        `current_context_revision ${result.projectBefore.currentContextRevision} → ${result.projectAfter.currentContextRevision} · ` +
        `project_version ${result.projectBefore.projectVersion} → ${result.projectAfter.projectVersion} · ` +
        `spec_revision ${result.projectBefore.specRevision} → ${result.projectAfter.specRevision}`,
      `GET /api/v1/lingdoc/projects/{projectId} → HTTP 200 · ` +
        `template_id ${result.projectBefore.templateId} → ${result.projectAfter.templateId} · ` +
        `template_copy_version ${result.projectBefore.templateCopyVersion} → ${result.projectAfter.templateCopyVersion}`,
      `GET /api/v1/lingdoc/projects/{projectId}/impact-tasks → HTTP 200 · ` +
        `任务条数 ${result.tasksBefore.count} → ${result.tasksAfter.count}`,
      `GET /api/v1/lingdoc/projects/{projectId}/audit → HTTP 200 · ` +
        `业务审计事件条数 ${result.auditBefore.count} → ${result.auditAfter.count}`,
      assembled
        ? `注入命中口径：HTTP ${G3_INJECTED_FAULT_STATUS} + code=${G3_INJECTED_FAULT_CODE}`
        : `未装配注入：夹具证据见 ${G3_FAULT_FIXTURE_EVIDENCE}`,
    ],
    status: holds ? 'PASS' : 'NOT RUN',
  }
}
