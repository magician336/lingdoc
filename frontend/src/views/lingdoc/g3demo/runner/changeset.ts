/**
 * G3 验收台 · 变更集生命周期链。
 *
 * 这一层在 `runner/project.ts`（前置链）之上，把「变更集生命周期」跑成真实调用：
 * 创建变更集 → 读影响评估 → Owner 应用 → 受影响章节重新确认，
 * 并在同一轮里构造出三条并发/逆向场景：
 *
 * - **AC-G3-01**：研究对象变更 → 应用后生成新上下文修订，受影响章节的当前确认失效并进入待复核。
 * - **AC-G3-04**：局部影响 —— 未列入受影响范围的章节不被本次变更集失效，也没有复核任务。
 * - **AC-G3-02**：两个变更集基于同一上下文修订，先应用的推进修订，后应用的落为陈旧（STALE）。
 * - **AC-G3-11**：撤销经由新建逆向变更集完成，原变更集历史仍可读。
 *
 * 三条硬约束写死在代码里，不靠人记：
 * 1. **读影响评估严格早于应用变更**（`readImpactAssessment` 之后才有 `applyChangeSet`，
 *    链末还会复核步骤顺序，不满足就抛错）。
 * 2. **请求体形状**：`/assess`、`/reverse` 严格是 `{}`；`/reassess` **完全空体**
 *    （服务端只读 1 字节，多 1 字节即 400，见 `internal/lingdoc/workspace/http.go:1292-1298`）。
 * 3. **判定来自响应事实**：每条 AC 的结论由读回的响应算出，不写死；
 *    读不到依据时如实说「服务端未提供」，不编造。
 */

import { envelopeData, errorCodeOf, type G3HttpClient } from './httpClient'
import { createDemoAccount, loginAccount, registerAccount, type G3DemoAccount } from './identity'
import { makeIdempotencyKey, runStep, type G3RunContext, type G3RunVerdict } from './runContext'

/**
 * 本链的步骤名。
 *
 * 它们是**证据的锚**：右栏矩阵的规则表按步骤名判断「本轮是否跑到这一行所需步骤」
 * （见 `g3Evidence.ts` 的 `G3_ROW_EVIDENCE_RULES`），所以名字一旦改动，
 * 矩阵那几行就会退回「未被本轮覆盖」——这是刻意的：证据链断在名字上时，宁可不判定。
 */
export const G3_CS_STEPS = {
  /** 写入章节正文。 */
  writeChapter: '写入章节正文',
  /** 确认章节（首次）。 */
  confirmChapter: '确认章节',
  /** 读回章节列表。 */
  readChapters: '读回章节列表',
  /** 读章节版本列表（章节级与版本级确认标记）。 */
  readVersions: '读章节版本列表',
  /** 读当前确认。 */
  readCurrentConfirmation: '读当前确认',
  /** 创建变更集（局部影响场景）。 */
  create: '创建变更集',
  /** 评估变更集（请求体严格 `{}`）。 */
  assess: '评估变更集',
  /** 读影响评估。 */
  readAssessment: '读影响评估',
  /** 应用变更集。 */
  apply: '应用变更集',
  /** 创建并发变更集（同一基线）。 */
  createConcurrent: '创建并发变更集',
  /** 应用并发变更集（预期 409，变更集落为陈旧）。 */
  applyConcurrent: '应用并发变更集',
  /** 读回陈旧变更集。 */
  readStale: '读回陈旧变更集',
  /** 读上下文修订。 */
  readRevisions: '读上下文修订',
  /** 读影响任务。 */
  readTasks: '读影响任务',
  /** 读项目状态。 */
  readProject: '读项目状态',
  /** 重评估陈旧变更集（请求体完全为空）。 */
  reassess: '重评估变更集',
  /** 复写章节正文（按新的规格修订）。 */
  rewriteChapter: '复写章节正文',
  /** 重新确认章节。 */
  reconfirm: '重新确认章节',
  /** 创建逆向变更集（请求体严格 `{}`）。 */
  reverse: '创建逆向变更集',
  /** 读回原变更集（历史仍可读）。 */
  readSource: '读回原变更集',
  /** 读逆向影响评估。 */
  readReverseAssessment: '读逆向影响评估',
  /** 应用逆向变更集。 */
  applyReverse: '应用逆向变更集',
  /** 注册非 Owner 账号。 */
  registerNonOwner: '注册非 Owner 账号',
  /** 非 Owner 登录。 */
  loginNonOwner: '非 Owner 登录',
  /** 把非 Owner 加进同一租户。 */
  addTenantMember: '添加租户成员',
  /** 邀请非 Owner 进项目。 */
  inviteProjectMember: '邀请项目成员',
  /** 非 Owner 接受项目邀请。 */
  acceptInvitation: '接受项目邀请',
  /** 非 Owner 调用应用。 */
  nonOwnerApply: '非 Owner 应用变更集',
  /** 非 Owner 调用拒绝。 */
  nonOwnerReject: '非 Owner 拒绝变更集',
} as const

/** 变更集事实。 */
export interface G3ChangeSetFact {
  id: string
  projectId: string
  status: string
  reason: string
  baseContextRevision: number
  targetContextRevision: number | null
  baseSpecRevision: number
  targetSpecRevision: number | null
  createdBy: string
  decisionBy: string
  staleReason: string
  reassessedFromId: string
  reversesChangeSetId: string
  fields: { key: string; old_value: string; new_value: string }[]
  impacts: {
    chapter_id: string
    chapter_version_id: string | null
    title: string
    reason: string
    status: string
  }[]
}

/** 影响评估里的一个对象。 */
export interface G3ImpactObjectFact {
  kind: string
  id: string
  version: string
  state: string
  confirmed: boolean
  contextRevision: number
}

/** 影响评估里的一条影响。 */
export interface G3ResolvedImpactFact {
  target: { kind: string; id: string; version: string }
  action: string
  certainty: string
  causes: {
    edgeId: string
    source: { kind: string; id: string; version: string }
    reason: string
  }[]
}

/** 影响评估事实。 */
export interface G3ImpactAssessmentFact {
  changeSetId: string
  projectId: string
  contextRevision: number
  inputHash: string
  objects: G3ImpactObjectFact[]
  impacts: G3ResolvedImpactFact[]
  edges: unknown[]
}

/** 影响任务事实。 */
export interface G3ImpactTaskFact {
  id: string
  changeSetId: string
  action: string
  severity: string
  responsibilityDomain: string
  status: string
  target: { kind: string; id: string; version: string }
}

/** 上下文修订事实。 */
export interface G3ContextRevisionFact {
  revision: number
  changeSetId: string
  specRevision: number
}

/** 章节快照事实。 */
export interface G3ChapterSnapshot {
  chapterId: string
  sectionId: string
  title: string
  currentVersionId: string | null
  /** 章节级确认（读视图口径：确认绑定的规格修订必须仍是当前修订）。 */
  confirmationValid: boolean
  /** 章节版本级确认标记：只有被本次变更集列入失效范围的章节才会变 false。 */
  versionConfirmationValid: boolean | null
  /** 当前章节版本的规格修订。 */
  versionSpecRevision: number | null
  /** 当前确认（GET /chapters/{id}/confirmations/current）。 */
  currentConfirmation: { id: string; chapterVersionId: string; specRevision: number } | null
}

/** 一次失败调用的结果。 */
export interface G3FailureFact {
  status: number
  code: string
  message: string
}

/** 非 Owner 权限探测结果。 */
export interface G3PermissionProbe {
  /** 非 Owner 的账号（不含口令）。 */
  account: { username: string; email: string }
  /** 非 Owner 是否能读到变更集（能读到但不能决定，才说明是 Owner 专属）。 */
  read: G3FailureFact | { status: number; code: string; message: string }
  apply: G3FailureFact
  reject: G3FailureFact
}

/**
 * 一条判据判定。
 *
 * 形状取自 `runner/runContext.ts` 的 `G3RunVerdict`（矩阵规则表按 id 取结论），
 * 这里保留别名，链内代码照旧引用 `G3Verdict`。
 */
export type G3Verdict = G3RunVerdict

function projectPath(projectId: string, suffix: string): string {
  return `/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}${suffix}`
}

function num(value: unknown): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function objectOf(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

/** 把后端返回的变更集读成事实。 */
export function readChangeSetFact(data: unknown): G3ChangeSetFact {
  const raw = objectOf(data)
  return {
    id: str(raw.id),
    projectId: str(raw.project_id),
    status: str(raw.status),
    reason: str(raw.reason),
    baseContextRevision: num(raw.base_context_revision),
    targetContextRevision: raw.target_context_revision === null || raw.target_context_revision === undefined
      ? null
      : num(raw.target_context_revision),
    baseSpecRevision: num(raw.base_spec_revision),
    targetSpecRevision: raw.target_spec_revision === null || raw.target_spec_revision === undefined
      ? null
      : num(raw.target_spec_revision),
    createdBy: str(raw.created_by),
    decisionBy: str(raw.decision_by),
    staleReason: str(raw.stale_reason),
    reassessedFromId: str(raw.reassessed_from_id),
    reversesChangeSetId: str(raw.reverses_change_set_id),
    fields: (Array.isArray(raw.fields) ? raw.fields : []).map((item) => {
      const field = objectOf(item)
      return { key: str(field.key), old_value: str(field.old_value), new_value: str(field.new_value) }
    }),
    impacts: (Array.isArray(raw.impacts) ? raw.impacts : []).map((item) => {
      const impact = objectOf(item)
      return {
        chapter_id: str(impact.chapter_id),
        chapter_version_id: impact.chapter_version_id ? str(impact.chapter_version_id) : null,
        title: str(impact.title),
        reason: str(impact.reason),
        status: str(impact.status),
      }
    }),
  }
}

/** 把后端返回的影响评估读成事实。 */
export function readImpactAssessmentFact(data: unknown): G3ImpactAssessmentFact {
  const raw = objectOf(data)
  return {
    changeSetId: str(raw.change_set_id),
    projectId: str(raw.project_id),
    contextRevision: num(raw.context_revision),
    inputHash: str(raw.input_hash),
    objects: (Array.isArray(raw.objects) ? raw.objects : []).map((item) => {
      const state = objectOf(item)
      const ref = objectOf(state.ref)
      return {
        kind: str(ref.kind),
        id: str(ref.id),
        version: str(ref.version),
        state: str(state.state),
        confirmed: state.confirmed === true,
        contextRevision: num(state.context_revision),
      }
    }),
    impacts: (Array.isArray(raw.impacts) ? raw.impacts : []).map((item) => {
      const impact = objectOf(item)
      const target = objectOf(impact.target)
      return {
        target: { kind: str(target.kind), id: str(target.id), version: str(target.version) },
        action: str(impact.action),
        certainty: str(impact.certainty),
        causes: (Array.isArray(impact.causes) ? impact.causes : []).map((cause) => {
          const item2 = objectOf(cause)
          const source = objectOf(item2.source)
          return {
            edgeId: str(item2.edge_id),
            source: { kind: str(source.kind), id: str(source.id), version: str(source.version) },
            reason: str(item2.reason),
          }
        }),
      }
    }),
    edges: Array.isArray(raw.edges) ? raw.edges : [],
  }
}

/** 把后端返回的影响任务读成事实。 */
export function readImpactTaskFact(data: unknown): G3ImpactTaskFact {
  const raw = objectOf(data)
  const target = objectOf(raw.target)
  return {
    id: str(raw.id),
    changeSetId: str(raw.change_set_id),
    action: str(raw.action),
    severity: str(raw.severity),
    responsibilityDomain: str(raw.responsibility_domain),
    status: str(raw.status),
    target: { kind: str(target.kind), id: str(target.id), version: str(target.version) },
  }
}

/** 创建变更集。 */
export async function createChangeSet(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  options: {
    stepName?: string
    idemLabel: string
    expectedContextRevision: number
    fields: Record<string, { old_value: string; new_value: string }>
    affectedChapterIds: string[]
    reason: string
  },
): Promise<G3ChangeSetFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName ?? G3_CS_STEPS.create,
    method: 'POST',
    path: projectPath(projectId, '/change-sets'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    body: {
      expected_context_revision: options.expectedContextRevision,
      fields: options.fields,
      affected_chapter_ids: options.affectedChapterIds,
      reason: options.reason,
    },
    conclusion: `变更集已创建（HTTP {status}）：${options.reason}`,
    detail: (body) => {
      const change = objectOf(objectOf(body).data)
      return {
        change_set_id: change.id,
        status: change.status,
        base_context_revision: change.base_context_revision,
        base_spec_revision: change.base_spec_revision,
        affected_chapter_ids: options.affectedChapterIds,
      }
    },
  })
  return readChangeSetFact(envelopeData(data))
}

/**
 * 评估变更集。
 *
 * 请求体**严格是 `{}`**：契约要求这个端点不读业务字段（`impact_routes.go:90-92`）。
 */
export async function assessChangeSet(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
): Promise<G3ChangeSetFact> {
  const { data } = await runStep(ctx, client, {
    name: G3_CS_STEPS.assess,
    method: 'POST',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}/assess`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `assess-${changeSetId}`),
    body: {},
    conclusion: `变更集已评估（HTTP {status}）`,
    detail: (body) => {
      const change = objectOf(objectOf(body).data)
      return { change_set_id: change.id, status: change.status }
    },
  })
  return readChangeSetFact(envelopeData(data))
}

/** 读影响评估。必须在应用变更之前读。 */
export async function readImpactAssessment(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
  stepName: string = G3_CS_STEPS.readAssessment,
): Promise<G3ImpactAssessmentFact> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}/impact-assessment`),
    token,
    conclusion: '影响评估已读回（HTTP {status}）',
    detail: (body) => {
      const assessment = readImpactAssessmentFact(objectOf(body).data)
      return {
        change_set_id: assessment.changeSetId,
        context_revision: assessment.contextRevision,
        object_count: assessment.objects.length,
        impact_count: assessment.impacts.length,
      }
    },
  })
  return readImpactAssessmentFact(envelopeData(data))
}

/**
 * 应用变更集。
 *
 * `expectedStatus` 用于「预期失败」的证据步骤：陈旧应用返回 409 是**本轮要证明的事实**，
 * 不是失败，所以命中期望状态码时这一步记为成功（`runContext.ts` 里处理）。
 */
export async function applyChangeSet(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
  options: { stepName?: string; idemLabel?: string; expectedStatus?: number } = {},
): Promise<G3ChangeSetFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName ?? G3_CS_STEPS.apply,
    method: 'POST',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}/apply`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel ?? `apply-${changeSetId}`),
    expectedStatus: options.expectedStatus,
    conclusion: options.expectedStatus
      ? `应用按预期被拒（HTTP {status}）：基线已推进，变更集落为陈旧`
      : `变更集已应用（HTTP {status}）`,
    detail: (body) => {
      const change = objectOf(objectOf(body).data)
      return {
        change_set_id: change.id,
        status: change.status,
        target_context_revision: change.target_context_revision,
        target_spec_revision: change.target_spec_revision,
        error_code: errorCodeOf(body),
      }
    },
  })
  return readChangeSetFact(envelopeData(data))
}

/** 拒绝变更集（Owner 决策）。 */
export async function rejectChangeSet(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
  options: { stepName?: string; expectedStatus?: number } = {},
): Promise<G3ChangeSetFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName ?? '拒绝变更集',
    method: 'POST',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}/reject`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `reject-${changeSetId}`),
    expectedStatus: options.expectedStatus,
    conclusion: options.expectedStatus ? `拒绝按预期被拒（HTTP {status}）` : `变更集已拒绝（HTTP {status}）`,
    detail: (body) => {
      const change = objectOf(objectOf(body).data)
      return { change_set_id: change.id, status: change.status, error_code: errorCodeOf(body) }
    },
  })
  return readChangeSetFact(envelopeData(data))
}

/**
 * 创建逆向变更集。
 *
 * 请求体**严格是 `{}`**：逆向计划完全由服务端从源变更集推导
 * （`internal/lingdoc/workspacecore/change_inverse_operation.go:30`）。
 */
export async function reverseChangeSet(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
): Promise<G3ChangeSetFact> {
  const { data } = await runStep(ctx, client, {
    name: G3_CS_STEPS.reverse,
    method: 'POST',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}/reverse`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `reverse-${changeSetId}`),
    body: {},
    conclusion: `逆向变更集已创建（HTTP {status}）`,
    detail: (body) => {
      const change = objectOf(objectOf(body).data)
      return {
        change_set_id: change.id,
        status: change.status,
        reverses_change_set_id: change.reverses_change_set_id,
        base_context_revision: change.base_context_revision,
        fields: (Array.isArray(change.fields) ? change.fields : []).map((field) => {
          const item = objectOf(field)
          return `${item.key}: ${item.old_value} → ${item.new_value}`
        }),
      }
    },
  })
  return readChangeSetFact(envelopeData(data))
}

/**
 * 重评估变更集。
 *
 * 请求体**完全为空**：服务端只读 1 字节，多 1 字节即 400
 * （`internal/lingdoc/workspace/http.go:1292-1298`）。所以这里传 `undefined`，
 * 不是 `{}`。
 */
export async function reassessChangeSet(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
): Promise<G3ChangeSetFact> {
  const { data } = await runStep(ctx, client, {
    name: G3_CS_STEPS.reassess,
    method: 'POST',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}/reassess`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `reassess-${changeSetId}`),
    body: undefined,
    conclusion: `变更集已重评估（HTTP {status}）`,
    detail: (body) => {
      const change = objectOf(objectOf(body).data)
      return {
        change_set_id: change.id,
        status: change.status,
        reassessed_from_id: change.reassessed_from_id,
        base_context_revision: change.base_context_revision,
      }
    },
  })
  return readChangeSetFact(envelopeData(data))
}

/** 读单个变更集。 */
export async function readChangeSet(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
  stepName: string,
): Promise<G3ChangeSetFact> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}`),
    token,
    conclusion: '变更集已读回（HTTP {status}）',
    detail: (body) => {
      const change = readChangeSetFact(objectOf(body).data)
      return {
        change_set_id: change.id,
        status: change.status,
        stale_reason: change.staleReason,
        reassessed_from_id: change.reassessedFromId,
        reverses_change_set_id: change.reversesChangeSetId,
      }
    },
  })
  return readChangeSetFact(envelopeData(data))
}

/** 读影响任务列表。 */
export async function listImpactTasks(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string = G3_CS_STEPS.readTasks,
): Promise<G3ImpactTaskFact[]> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, '/impact-tasks'),
    token,
    conclusion: '影响任务已读回（HTTP {status}）',
    detail: (body) => {
      const list = Array.isArray(objectOf(body).data) ? (objectOf(body).data as unknown[]) : []
      return {
        task_count: list.length,
        tasks: list.map((item) => {
          const task = readImpactTaskFact(item)
          return `${task.changeSetId.slice(0, 8)}:${task.action}:${task.status}`
        }),
      }
    },
  })
  const list = Array.isArray(envelopeData(data)) ? (envelopeData(data) as unknown[]) : []
  return list.map(readImpactTaskFact)
}

/** 读上下文修订列表。 */
export async function listContextRevisions(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string = G3_CS_STEPS.readRevisions,
): Promise<G3ContextRevisionFact[]> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, '/context-revisions'),
    token,
    conclusion: '上下文修订已读回（HTTP {status}）',
    detail: (body) => {
      const list = Array.isArray(objectOf(body).data) ? (objectOf(body).data as unknown[]) : []
      return {
        revision_count: list.length,
        revisions: list.map((item) => num(objectOf(item).revision)),
      }
    },
  })
  const list = Array.isArray(envelopeData(data)) ? (envelopeData(data) as unknown[]) : []
  return list.map((item) => {
    const raw = objectOf(item)
    return {
      revision: num(raw.revision),
      changeSetId: str(raw.change_set_id),
      specRevision: num(raw.spec_revision),
    }
  })
}

/** 项目当前状态。 */
export interface G3ProjectState {
  id: string
  status: string
  projectVersion: number
  specRevision: number
  currentContextRevision: number
  spec: Record<string, string>
  /** 模板 id（依赖边的 `TemplateCopy` / `RuleSet` 对象用它）。 */
  templateId?: string
  /** 项目自有模板副本版本（`TemplateCopy` 对象的版本）。 */
  templateCopyVersion?: number
  /** 模板规则集哈希（`RuleSet` 对象的版本）。 */
  rulesetHash?: string
}

/** 读项目状态。 */
export async function readProjectState(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string = G3_CS_STEPS.readProject,
): Promise<G3ProjectState> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, ''),
    token,
    conclusion: '项目状态已读回（HTTP {status}）',
    detail: (body) => {
      const project = objectOf(objectOf(body).data)
      return {
        project_id: project.id,
        status: project.status,
        project_version: project.project_version,
        spec_revision: project.spec_revision,
        current_context_revision: project.current_context_revision,
      }
    },
  })
  const raw = objectOf(envelopeData(data))
  const spec: Record<string, string> = {}
  for (const [key, value] of Object.entries(objectOf(raw.spec))) {
    spec[key] = String(value)
  }
  const templateCopy = objectOf(raw.template_copy)
  return {
    id: str(raw.id),
    status: str(raw.status),
    projectVersion: num(raw.project_version),
    specRevision: num(raw.spec_revision),
    currentContextRevision: num(raw.current_context_revision),
    spec,
    templateId: str(raw.template_id),
    templateCopyVersion: num(raw.template_copy_version),
    rulesetHash: str(templateCopy.ruleset_hash),
  }
}

/** 写章节正文。 */
export async function writeChapterVersion(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  chapterId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedChapterVersionId: string | null
    expectedSpecRevision: number
    bodyMarkdown: string
  },
): Promise<{ currentVersionId: string | null }> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/chapters/${encodeURIComponent(chapterId)}/versions`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    body: {
      expected_chapter_version_id: options.expectedChapterVersionId,
      expected_spec_revision: options.expectedSpecRevision,
      body_markdown: options.bodyMarkdown,
      source_ids: [],
    },
    conclusion: `章节正文已保存（HTTP {status}）：规格修订 ${options.expectedSpecRevision}`,
    detail: (body) => {
      const chapter = objectOf(objectOf(body).data)
      return {
        chapter_id: chapter.id,
        current_version_id: chapter.current_version_id,
        confirmation_valid: chapter.confirmation_valid,
      }
    },
  })
  const chapter = objectOf(envelopeData(data))
  return { currentVersionId: chapter.current_version_id ? str(chapter.current_version_id) : null }
}

/** 确认章节。 */
export async function confirmChapter(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  chapterId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedChapterVersionId: string
    expectedSpecRevision: number
  },
): Promise<{ id: string; chapterVersionId: string; specRevision: number }> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/chapters/${encodeURIComponent(chapterId)}/confirmations`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    body: {
      expected_chapter_version_id: options.expectedChapterVersionId,
      expected_spec_revision: options.expectedSpecRevision,
      review_decisions: [],
    },
    conclusion: `章节已确认（HTTP {status}）：绑定章节版本 ${options.expectedChapterVersionId} · 规格修订 ${options.expectedSpecRevision}`,
    detail: (body) => {
      const confirmation = objectOf(objectOf(body).data)
      return {
        confirmation_id: confirmation.id,
        chapter_version_id: confirmation.chapter_version_id,
        spec_revision: confirmation.spec_revision,
      }
    },
  })
  const confirmation = objectOf(envelopeData(data))
  return {
    id: str(confirmation.id),
    chapterVersionId: str(confirmation.chapter_version_id),
    specRevision: num(confirmation.spec_revision),
  }
}

/** 读章节快照：章节级确认、版本级确认标记、当前确认。 */
export async function readChapterSnapshot(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  chapter: { id: string; sectionId: string; title: string },
  options: { versionStepName?: string; confirmationStepName?: string } = {},
): Promise<G3ChapterSnapshot> {
  const chaptersRes = await runStep(ctx, client, {
    name: G3_CS_STEPS.readChapters,
    method: 'GET',
    path: projectPath(projectId, '/chapters'),
    token,
    conclusion: '章节列表已读回（HTTP {status}）',
    detail: (body) => {
      const list = Array.isArray(objectOf(body).data) ? (objectOf(body).data as unknown[]) : []
      return {
        chapter_count: list.length,
        chapters: list.map((item) => {
          const chapterFact = objectOf(item)
          return `${chapterFact.section_id}:confirmation_valid=${chapterFact.confirmation_valid}`
        }),
      }
    },
  })
  const list = Array.isArray(envelopeData(chaptersRes.data)) ? (envelopeData(chaptersRes.data) as unknown[]) : []
  const chapterFact = list.map(objectOf).find((item) => str(item.id) === chapter.id) ?? {}

  const versionsRes = await runStep(ctx, client, {
    name: options.versionStepName ?? G3_CS_STEPS.readVersions,
    method: 'GET',
    path: projectPath(projectId, `/chapters/${encodeURIComponent(chapter.id)}/versions`),
    token,
    conclusion: '章节版本已读回（HTTP {status}）',
    detail: (body) => {
      const items = versionItems(body)
      return {
        version_count: items.length,
        versions: items.map((item) => {
          const version = objectOf(item)
          return `${version.id}:spec=${version.spec_revision}:confirmation_valid=${version.confirmation_valid}`
        }),
      }
    },
  })
  const versions = versionItems(versionsRes.response.body)
  const currentVersionId = chapterFact.current_version_id ? str(chapterFact.current_version_id) : null
  const currentVersion = versions.find((item) => str(objectOf(item).id) === currentVersionId) ?? versions[0] ?? null

  const confirmationRes = await runStep(ctx, client, {
    name: options.confirmationStepName ?? G3_CS_STEPS.readCurrentConfirmation,
    method: 'GET',
    path: projectPath(projectId, `/chapters/${encodeURIComponent(chapter.id)}/confirmations/current`),
    token,
    conclusion: '当前确认已读回（HTTP {status}）',
    detail: (body) => {
      const confirmation = objectOf(objectOf(body).data)
      return {
        confirmation_id: confirmation.id || '（无当前确认）',
        chapter_version_id: confirmation.chapter_version_id,
        spec_revision: confirmation.spec_revision,
      }
    },
  })
  const confirmation = objectOf(envelopeData(confirmationRes.data))

  return {
    chapterId: chapter.id,
    sectionId: chapter.sectionId,
    title: chapter.title,
    currentVersionId,
    confirmationValid: chapterFact.confirmation_valid === true,
    versionConfirmationValid: currentVersion ? objectOf(currentVersion).confirmation_valid === true : null,
    versionSpecRevision: currentVersion ? num(objectOf(currentVersion).spec_revision) : null,
    currentConfirmation: confirmation.id
      ? {
          id: str(confirmation.id),
          chapterVersionId: str(confirmation.chapter_version_id),
          specRevision: num(confirmation.spec_revision),
        }
      : null,
  }
}

function versionItems(body: unknown): unknown[] {
  let value = body
  if (value && typeof value === 'object' && !Array.isArray(value) && 'data' in value) {
    value = (value as { data: unknown }).data
  }
  if (Array.isArray(value)) return value
  const items = objectOf(value).items
  return Array.isArray(items) ? items : []
}

/**
 * 未受影响章节的理由。
 *
 * **服务端给了就用服务端的，没给就如实说没给** —— 不编造理由。
 */
export function unaffectedReason(params: {
  chapterId: string
  sectionId: string
  assessment: G3ImpactAssessmentFact
  changeSetImpacts: { chapter_id: string; reason: string }[]
  impactTaskChapterIds: string[]
  versionConfirmationValid: boolean | null
}): { serverReason: string | null; text: string } {
  const object = params.assessment.objects.find((item) => item.id === params.chapterId)
  const impact = params.assessment.impacts.find((item) => item.target.id === params.chapterId)
  const inChangeSet = params.changeSetImpacts.some((item) => item.chapter_id === params.chapterId)
  const hasTask = params.impactTaskChapterIds.includes(params.chapterId)

  if (object || impact || inChangeSet || hasTask) {
    const serverReason = object
      ? `评估中出现的对象状态：${object.kind}/${object.id} · ${object.state}`
      : impact
        ? `评估中的影响：${impact.action}（${impact.certainty}）`
        : inChangeSet
          ? `变更集把它列入受影响范围：${params.changeSetImpacts.find((item) => item.chapter_id === params.chapterId)?.reason ?? ''}`
          : `已为它产生影响任务`
    return { serverReason, text: serverReason }
  }

  const parts = [
    '服务端未提供未受影响理由',
    `影响评估的 objects / impacts 与变更集 impacts 都没有 ${params.sectionId} 章节`,
    '没有为它产生影响任务',
  ]
  if (params.versionConfirmationValid === true) {
    parts.push('其章节版本的确认标记仍为 true（本次变更集未使其失效）')
  } else if (params.versionConfirmationValid === false) {
    parts.push('其章节版本的确认标记已为 false')
  }
  return { serverReason: null, text: parts.join('；') }
}

/** 链的输入。 */
export interface G3ChangeSetChainOptions {
  projectId: string
  /** 模板激活后自动创建的两个章节。 */
  chapters: { id: string; sectionId: string; title: string }[]
  /** 规格字段的当前值。不给就从项目当前规格里读。 */
  fieldValues?: { research_subject: string; research_goal: string }
  /** 租户 id（非 Owner 探测用）。 */
  tenantId?: string
  /**
   * 以非 Owner 身份发请求时的令牌切换钩子（浏览器侧用；node 脚本不需要）。
   *
   * 浏览器侧的请求拦截器会**用存储里的令牌覆盖** `Authorization`
   * （`frontend/src/utils/request.ts:78-83`），所以要以另一个成员的身份发请求，
   * 必须把令牌临时换过去。钩子拿到非 Owner 的令牌与要执行的调用，由宿主负责切换与还原。
   */
  withNonOwnerToken?: <T>(token: string, run: () => Promise<T>) => Promise<T>
  /** 是否跑非 Owner 权限探测。 */
  probeNonOwner?: boolean
}

/** 链的结果。 */
export interface G3ChangeSetChainResult {
  /** 局部影响场景的变更集。 */
  scoped: G3ChangeSetFact
  /** 并发场景的变更集。 */
  concurrent: G3ChangeSetFact
  /** 逆向变更集。 */
  reversed: G3ChangeSetFact
  /** 局部影响场景的影响评估。 */
  assessment: G3ImpactAssessmentFact
  /** 逆向变更集的影响评估。 */
  reverseAssessment: G3ImpactAssessmentFact
  /** 重评估产生的新记录。 */
  reassessed: G3ChangeSetFact
  /** 应用前后受影响章节的快照。 */
  affectedBefore: G3ChapterSnapshot
  affectedAfter: G3ChapterSnapshot
  /** 应用前后未受影响章节的快照。 */
  unaffectedBefore: G3ChapterSnapshot
  unaffectedAfter: G3ChapterSnapshot
  /** 重新确认后的快照。 */
  affectedReconfirmed: G3ChapterSnapshot
  /** 未受影响章节的理由。 */
  unaffected: { serverReason: string | null; text: string }
  /** 应用前的上下文修订与影响任务。 */
  revisionsBefore: G3ContextRevisionFact[]
  tasksBefore: G3ImpactTaskFact[]
  /** 陈旧应用之后的上下文修订与影响任务。 */
  revisionsAfter: G3ContextRevisionFact[]
  tasksAfter: G3ImpactTaskFact[]
  /** 判定。 */
  verdicts: G3Verdict[]
  /** 非 Owner 权限探测。 */
  permissionProbe?: G3PermissionProbe
}

/**
 * 生命周期链结果的页面快照。
 *
 * 只留**页面要展示的字段**，并且全部可 JSON 序列化：它要跟着运行摘要落进
 * 浏览器本地存储，刷新后页面仍然读得到本轮的判定与证据位。
 */
export interface G3LifecycleSnapshot {
  verdicts: G3Verdict[]
  changeSets: {
    scoped: G3ChangeSetFact
    concurrent: G3ChangeSetFact
    reversed: G3ChangeSetFact
    reassessed: G3ChangeSetFact
  }
  assessment: G3ImpactAssessmentFact
  reverseAssessment: G3ImpactAssessmentFact
  chapters: {
    affectedBefore: G3ChapterSnapshot
    affectedAfter: G3ChapterSnapshot
    affectedReconfirmed: G3ChapterSnapshot
    unaffectedBefore: G3ChapterSnapshot
    unaffectedAfter: G3ChapterSnapshot
  }
  unaffected: { serverReason: string | null; text: string }
  permissionProbe?: G3PermissionProbe
}

/** 把链结果压成页面快照。 */
export function toLifecycleSnapshot(result: G3ChangeSetChainResult): G3LifecycleSnapshot {
  return {
    verdicts: result.verdicts,
    changeSets: {
      scoped: result.scoped,
      concurrent: result.concurrent,
      reversed: result.reversed,
      reassessed: result.reassessed,
    },
    assessment: result.assessment,
    reverseAssessment: result.reverseAssessment,
    chapters: {
      affectedBefore: result.affectedBefore,
      affectedAfter: result.affectedAfter,
      affectedReconfirmed: result.affectedReconfirmed,
      unaffectedBefore: result.unaffectedBefore,
      unaffectedAfter: result.unaffectedAfter,
    },
    unaffected: result.unaffected,
    permissionProbe: result.permissionProbe,
  }
}

function findStepIndex(ctx: G3RunContext, name: string): number {
  return ctx.steps.findIndex((step) => step.name === name)
}

/** 取某个步骤最后一条记录的结论。 */
function conclusionOf(ctx: G3RunContext, name: string): string {
  const matches = ctx.steps.filter((step) => step.name === name)
  return matches[matches.length - 1]?.conclusion ?? ''
}

function stepOf(ctx: G3RunContext, name: string, occurrence = 0): string {
  const matches = ctx.steps.filter((step) => step.name === name)
  const step = matches[occurrence] ?? matches[matches.length - 1]
  if (!step) return `${name}（本轮没有这一步）`
  return `${step.method} ${step.path} → HTTP ${step.status}`
}

/**
 * 跑完整条变更集生命周期链。
 *
 * 顺序是**状态机的前置条件**，不是审美：先建两个同基线的变更集，再应用第一个，
 * 后一个才会因基线推进而落为陈旧（AC-G3-02 的并发就是真并发，不是模拟）。
 */
export async function runChangeSetChain(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: G3ChangeSetChainOptions,
): Promise<G3ChangeSetChainResult> {
  const question = options.chapters.find((chapter) => chapter.sectionId === 'question')
  const method = options.chapters.find((chapter) => chapter.sectionId === 'method')
  if (!question || !method) {
    throw new Error('变更集生命周期链需要模板激活后的 question / method 两个章节')
  }

  const project = await readProjectState(ctx, client, token, options.projectId)
  const baseContextRevision = project.currentContextRevision
  const baseSpecRevision = project.specRevision
  const fieldValues = {
    research_subject: options.fieldValues?.research_subject ?? project.spec.research_subject ?? '',
    research_goal: options.fieldValues?.research_goal ?? project.spec.research_goal ?? '',
  }

  // —— 前置：把两个章节写成有正文、已确认的样子 ——
  const chapterVersions: Record<string, string | null> = {}
  for (const chapter of [question, method]) {
    const written = await writeChapterVersion(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_CS_STEPS.writeChapter,
      idemLabel: `chapter-body-${chapter.sectionId}`,
      expectedChapterVersionId: null,
      expectedSpecRevision: baseSpecRevision,
      bodyMarkdown: `## ${chapter.title}\n\nG3 验收台演示正文（运行 ${ctx.runId}）。`,
    })
    chapterVersions[chapter.sectionId] = written.currentVersionId
    if (!written.currentVersionId) {
      throw new Error(`章节 ${chapter.sectionId} 保存正文后没有拿到当前版本`)
    }
    await confirmChapter(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_CS_STEPS.confirmChapter,
      idemLabel: `confirm-${chapter.sectionId}`,
      expectedChapterVersionId: written.currentVersionId,
      expectedSpecRevision: baseSpecRevision,
    })
  }

  const affectedBefore = await readChapterSnapshot(ctx, client, token, options.projectId, method)
  const unaffectedBefore = await readChapterSnapshot(ctx, client, token, options.projectId, question)
  const revisionsBefore = await listContextRevisions(ctx, client, token, options.projectId)
  const tasksBefore = await listImpactTasks(ctx, client, token, options.projectId)

  // —— AC-G3-04 / AC-G3-01：局部影响的研究条件变更（只列方法章节为受影响）——
  const scoped = await createChangeSet(ctx, client, token, options.projectId, {
    idemLabel: 'change-set-scoped',
    expectedContextRevision: baseContextRevision,
    fields: {
      research_goal: {
        old_value: fieldValues.research_goal,
        new_value: `${fieldValues.research_goal}（G3 局部影响）`,
      },
    },
    affectedChapterIds: [method.id],
    reason: '研究目标局部调整：变更只依赖方法章节',
  })
  await assessChangeSet(ctx, client, token, options.projectId, scoped.id)
  const assessment = await readImpactAssessment(ctx, client, token, options.projectId, scoped.id)

  // —— AC-G3-02：同一基线上的第二个变更集（必须先建好，再让第一个推进基线）——
  const concurrent = await createChangeSet(ctx, client, token, options.projectId, {
    stepName: G3_CS_STEPS.createConcurrent,
    idemLabel: 'change-set-concurrent',
    expectedContextRevision: baseContextRevision,
    fields: {
      research_subject: {
        old_value: fieldValues.research_subject,
        new_value: `${fieldValues.research_subject}（G3 并发）`,
      },
    },
    affectedChapterIds: [method.id],
    reason: '同一基线上的并发变更集：研究对象调整',
  })

  // —— 应用第一个变更集 ——
  const applied = await applyChangeSet(ctx, client, token, options.projectId, scoped.id)
  const projectAfterApply = await readProjectState(ctx, client, token, options.projectId)
  const affectedAfter = await readChapterSnapshot(ctx, client, token, options.projectId, method)
  const unaffectedAfter = await readChapterSnapshot(ctx, client, token, options.projectId, question)
  const revisionsAfter = await listContextRevisions(ctx, client, token, options.projectId)
  const tasksAfter = await listImpactTasks(ctx, client, token, options.projectId)

  // —— 应用同基线的第二个变更集：预期 409，变更集落为陈旧 ——
  const concurrentApply = await applyChangeSet(ctx, client, token, options.projectId, concurrent.id, {
    stepName: G3_CS_STEPS.applyConcurrent,
    idemLabel: 'apply-concurrent',
    expectedStatus: 409,
  })
  const concurrentApplyStatus =
    ctx.steps.find((step) => step.name === G3_CS_STEPS.applyConcurrent)?.status ?? 0
  const stale = await readChangeSet(ctx, client, token, options.projectId, concurrent.id, G3_CS_STEPS.readStale)
  const revisionsAfterStale = await listContextRevisions(ctx, client, token, options.projectId)
  const tasksAfterStale = await listImpactTasks(ctx, client, token, options.projectId)
  const projectAfterStale = await readProjectState(ctx, client, token, options.projectId)

  // —— 陈旧记录留历史，新评估记录绑定当前修订并追溯 ——
  const reassessed = await reassessChangeSet(ctx, client, token, options.projectId, concurrent.id)

  // —— 受影响章节：按新的规格修订复写并重新确认 ——
  const rewritten = await writeChapterVersion(ctx, client, token, options.projectId, method.id, {
    stepName: G3_CS_STEPS.rewriteChapter,
    idemLabel: 'chapter-body-rewrite',
    expectedChapterVersionId: affectedAfter.currentVersionId,
    expectedSpecRevision: projectAfterApply.specRevision,
    bodyMarkdown: `## ${method.title}\n\n变更后复写的正文（运行 ${ctx.runId}，规格修订 ${projectAfterApply.specRevision}）。`,
  })
  if (!rewritten.currentVersionId) {
    throw new Error('复写章节正文后没有拿到当前版本')
  }
  const reconfirmed = await confirmChapter(ctx, client, token, options.projectId, method.id, {
    stepName: G3_CS_STEPS.reconfirm,
    idemLabel: 'reconfirm',
    expectedChapterVersionId: rewritten.currentVersionId,
    expectedSpecRevision: projectAfterApply.specRevision,
  })
  const affectedReconfirmed = await readChapterSnapshot(ctx, client, token, options.projectId, method)

  // —— AC-G3-11：撤销经由新建逆向变更集完成 ——
  const reversed = await reverseChangeSet(ctx, client, token, options.projectId, scoped.id)
  const source = await readChangeSet(ctx, client, token, options.projectId, scoped.id, G3_CS_STEPS.readSource)
  const reverseAssessment = await readImpactAssessment(
    ctx,
    client,
    token,
    options.projectId,
    reversed.id,
    G3_CS_STEPS.readReverseAssessment,
  )
  const reverseApplied = await applyChangeSet(ctx, client, token, options.projectId, reversed.id, {
    stepName: G3_CS_STEPS.applyReverse,
    idemLabel: 'apply-reverse',
  })
  const projectAfterReverse = await readProjectState(ctx, client, token, options.projectId)
  // —— 硬约束复核：读影响评估必须严格早于应用变更 ——
  const assessmentIndex = findStepIndex(ctx, G3_CS_STEPS.readAssessment)
  const applyIndex = findStepIndex(ctx, G3_CS_STEPS.apply)
  if (assessmentIndex < 0 || applyIndex < 0 || assessmentIndex > applyIndex) {
    throw new Error('硬约束被破坏：读影响评估必须严格早于应用变更')
  }

  const unaffected = unaffectedReason({
    chapterId: question.id,
    sectionId: question.sectionId,
    assessment,
    changeSetImpacts: scoped.impacts,
    impactTaskChapterIds: tasksAfter.map((task) => task.target.id),
    versionConfirmationValid: unaffectedAfter.versionConfirmationValid,
  })

  const verdicts: G3Verdict[] = [
    verdictAcG301({
      scoped,
      assessment,
      applied,
      projectBefore: project,
      projectAfter: projectAfterApply,
      revisionsBefore,
      revisionsAfter,
      affectedBefore,
      affectedAfter,
      affectedReconfirmed,
      reconfirmation: reconfirmed,
    }),
    verdictAcG302({
      scoped,
      concurrent,
      concurrentApply,
      concurrentApplyStatus,
      stale,
      projectAfterApply,
      projectAfterStale,
      revisionsAfter,
      revisionsAfterStale,
      tasksAfter,
      tasksAfterStale,
      reassessed,
    }),
    verdictAcG304({
      scoped,
      assessment,
      tasksAfter,
      unaffectedBefore,
      unaffectedAfter,
      unaffected,
    }),
    verdictAcG311({
      scoped,
      source,
      reversed,
      reverseAssessment,
      reverseApplied,
      projectAfterApply,
      projectAfterReverse,
    }),
  ]

  const result: G3ChangeSetChainResult = {
    scoped,
    concurrent,
    reversed,
    assessment,
    reverseAssessment,
    reassessed,
    affectedBefore,
    affectedAfter,
    unaffectedBefore,
    unaffectedAfter,
    affectedReconfirmed,
    unaffected,
    revisionsBefore,
    tasksBefore,
    revisionsAfter,
    tasksAfter,
    verdicts,
  }

  if (options.probeNonOwner !== false && options.tenantId) {
    result.permissionProbe = await probeNonOwnerDecisions(ctx, client, token, {
      tenantId: options.tenantId,
      projectId: options.projectId,
      changeSetId: reversed.id,
      withNonOwnerToken: options.withNonOwnerToken,
    })
  }

  // 判定挂到运行上下文上：矩阵规则表按判定 id 取结论，判定不成立就不许写 PASS。
  ctx.verdicts = [...(ctx.verdicts ?? []), ...verdicts]

  return result
}

  /** 非 Owner 权限探测的输入。 */
export interface G3NonOwnerOptions {
  tenantId: string
  projectId: string
  /** 被探测的变更集（必须处于可决定的状态）。 */
  changeSetId: string
  withNonOwnerToken?: <T>(token: string, run: () => Promise<T>) => Promise<T>
}

/**
 * 非 Owner 权限探测。
 *
 * 造一个**同租户、同一项目**的成员（治理角色 admin，能读但不是 Owner），
 * 让它调用应用与拒绝：两个都必须返回明确错误码。
 *
 * 服务端对「不是 Owner」按 404 回应（不泄露存在性，`g5_policy.go:122-125`
 * 与 `service_facade.go:2233-2235`），所以页面按仓库错误码原样呈现。
 */
export async function probeNonOwnerDecisions(
  ctx: G3RunContext,
  client: G3HttpClient,
  ownerToken: string,
  options: G3NonOwnerOptions,
): Promise<G3PermissionProbe> {
  const account: G3DemoAccount = createDemoAccount()
  const registered = await registerAccount(ctx, client, account, { stepName: G3_CS_STEPS.registerNonOwner })
  const nonOwnerToken = await loginAccount(ctx, client, account, { stepName: G3_CS_STEPS.loginNonOwner })

  // 同一租户：Owner 把非 Owner 加进租户
  await runStep(ctx, client, {
    name: G3_CS_STEPS.addTenantMember,
    method: 'POST',
    path: `/api/v1/tenants/${encodeURIComponent(options.tenantId)}/members`,
    token: ownerToken,
    body: { email: account.email, role: 'admin' },
    conclusion: '非 Owner 已加入同一租户（HTTP {status}）',
    detail: (body) => {
      const member = objectOf(objectOf(body).data)
      return { user_id: member.user_id, role: member.role, status: member.status }
    },
  })

  // 同一项目：Owner 邀请，非 Owner 接受
  const project = await readProjectState(ctx, client, ownerToken, options.projectId, G3_CS_STEPS.readProject)
  const invite = await runStep(ctx, client, {
    name: G3_CS_STEPS.inviteProjectMember,
    method: 'POST',
    path: projectPath(options.projectId, '/invitations'),
    token: ownerToken,
    idempotencyKey: makeIdempotencyKey(ctx.runId, 'invite-non-owner'),
    body: {
      expected_project_version: project.projectVersion,
      assignment: { user_id: registered.userId, role: 'collaborator', governance_role: 'admin' },
      reason: 'G3 非 Owner 权限探测',
    },
    conclusion: '非 Owner 已受邀进入项目（HTTP {status}）',
    detail: (body) => {
      const invitation = objectOf(objectOf(body).data)
      return { invitation_id: invitation.id, version: invitation.version, assignment: invitation.assignment }
    },
  })
  const invitation = objectOf(envelopeData(invite.data))
  const invitationId = str(invitation.id)
  const invitationVersion = num(invitation.version)
  // 非 Owner 在**同一租户**里操作：租户由 JWT 内的租户或这个头解析。
  const tenantHeaders = { 'X-Tenant-ID': options.tenantId }

  const accept = async (): Promise<void> => {
    await runStep(ctx, client, {
      name: G3_CS_STEPS.acceptInvitation,
      method: 'POST',
      path: projectPath(options.projectId, `/invitations/${encodeURIComponent(invitationId)}/accept`),
      token: nonOwnerToken,
      headers: tenantHeaders,
      idempotencyKey: makeIdempotencyKey(ctx.runId, 'accept-non-owner'),
      body: { expected_invitation_version: invitationVersion },
      conclusion: '非 Owner 已接受项目邀请（HTTP {status}）',
      detail: (body) => {
        const accepted = objectOf(objectOf(body).data)
        return { invitation_id: accepted.id, accepted_at: accepted.accepted_at }
      },
    })
  }
  if (options.withNonOwnerToken) {
    await options.withNonOwnerToken(nonOwnerToken, accept)
  } else {
    await accept()
  }

  const readFact = await (options.withNonOwnerToken
    ? options.withNonOwnerToken(nonOwnerToken, readNonOwnerChangeSet)
    : readNonOwnerChangeSet())

  async function readNonOwnerChangeSet() {
    return runStep(ctx, client, {
      name: '非 Owner 读变更集',
      method: 'GET',
      path: projectPath(options.projectId, `/change-sets/${encodeURIComponent(options.changeSetId)}`),
      token: nonOwnerToken,
      headers: tenantHeaders,
      conclusion: '非 Owner 读取变更集（HTTP {status}）',
      detail: (body) => {
        const change = objectOf(objectOf(body).data)
        return { change_set_id: change.id, status: change.status }
      },
    })
  }

  const decide = async (
    name: string,
    verb: 'apply' | 'reject',
  ): Promise<G3FailureFact> => {
    const result = await runStep(ctx, client, {
      name,
      method: 'POST',
      path: projectPath(options.projectId, `/change-sets/${encodeURIComponent(options.changeSetId)}/${verb}`),
      token: nonOwnerToken,
      headers: tenantHeaders,
      idempotencyKey: makeIdempotencyKey(ctx.runId, `non-owner-${verb}`),
      expectedStatus: [403, 404],
      conclusion: `非 Owner ${verb === 'apply' ? '应用' : '拒绝'}被拒（HTTP {status}）：按仓库错误码呈现`,
      detail: (body) => ({ error_code: errorCodeOf(body) }),
    })
    return {
      status: result.response.status,
      code: errorCodeOf(result.response.body),
      message: conclusionOf(ctx, name),
    }
  }

  const applyFact = options.withNonOwnerToken
    ? await options.withNonOwnerToken(nonOwnerToken, () => decide(G3_CS_STEPS.nonOwnerApply, 'apply'))
    : await decide(G3_CS_STEPS.nonOwnerApply, 'apply')
  const rejectFact = options.withNonOwnerToken
    ? await options.withNonOwnerToken(nonOwnerToken, () => decide(G3_CS_STEPS.nonOwnerReject, 'reject'))
    : await decide(G3_CS_STEPS.nonOwnerReject, 'reject')

  return {
    account: { username: account.username, email: account.email },
    read: {
      status: readFact.response.status,
      code: errorCodeOf(readFact.response.body),
      message: conclusionOf(ctx, '非 Owner 读变更集'),
    },
    apply: applyFact,
    reject: rejectFact,
  }
}

/** AC-G3-01 的判定。 */
function verdictAcG301(input: {
  scoped: G3ChangeSetFact
  assessment: G3ImpactAssessmentFact
  applied: G3ChangeSetFact
  projectBefore: G3ProjectState
  projectAfter: G3ProjectState
  revisionsBefore: G3ContextRevisionFact[]
  revisionsAfter: G3ContextRevisionFact[]
  affectedBefore: G3ChapterSnapshot
  affectedAfter: G3ChapterSnapshot
  affectedReconfirmed: G3ChapterSnapshot
  reconfirmation: { id: string; chapterVersionId: string; specRevision: number }
}): G3Verdict {
  const newRevision = input.projectAfter.currentContextRevision > input.projectBefore.currentContextRevision
  const revisionRecorded = input.revisionsAfter.some(
    (revision) => revision.revision === input.projectAfter.currentContextRevision,
  )
  const invalidated =
    input.affectedBefore.versionConfirmationValid === true &&
    input.affectedAfter.versionConfirmationValid === false
  const reconfirmed =
    input.affectedReconfirmed.versionConfirmationValid === true &&
    input.affectedReconfirmed.currentConfirmation !== null &&
    input.affectedReconfirmed.currentConfirmation.chapterVersionId === input.affectedReconfirmed.currentVersionId &&
    input.affectedReconfirmed.currentConfirmation.specRevision === input.projectAfter.specRevision
  const holds = newRevision && revisionRecorded && invalidated && reconfirmed

  return {
    id: 'AC-G3-01',
    title: '基线上的研究条件修改',
    holds,
    detail: holds
      ? `Owner 应用后上下文修订 ${input.projectBefore.currentContextRevision} → ${input.projectAfter.currentContextRevision}，受影响章节的当前确认失效并进入待复核，复写后按新规格修订（${input.projectAfter.specRevision}）与当前章节版本重新确认。`
      : `不成立：新修订=${newRevision}，修订记录=${revisionRecorded}，确认失效=${invalidated}，重新确认=${reconfirmed}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets → HTTP 201 · change_set_id=${input.scoped.id} · status=${input.scoped.status} · base_context_revision=${input.scoped.baseContextRevision}`,
      `GET /api/v1/lingdoc/projects/{projectId}/change-sets/${input.scoped.id}/impact-assessment → HTTP 200 · objects=${input.assessment.objects.length} · impacts=${input.assessment.impacts.length} · context_revision=${input.assessment.contextRevision}`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/${input.scoped.id}/apply → HTTP 200 · status=${input.applied.status} · target_context_revision=${input.applied.targetContextRevision} · target_spec_revision=${input.applied.targetSpecRevision}`,
      `GET /api/v1/lingdoc/projects/{projectId}/context-revisions → HTTP 200 · revisions=[${input.revisionsBefore.map((r) => r.revision).join(',')}] → [${input.revisionsAfter.map((r) => r.revision).join(',')}]`,
      `GET /api/v1/lingdoc/projects/{projectId}/chapters/{methodId}/versions → HTTP 200 · confirmation_valid ${input.affectedBefore.versionConfirmationValid} → ${input.affectedAfter.versionConfirmationValid}`,
      `POST /api/v1/lingdoc/projects/{projectId}/chapters/{methodId}/confirmations → HTTP 201 · confirmation_id=${input.reconfirmation.id} · chapter_version_id=${input.reconfirmation.chapterVersionId} · spec_revision=${input.reconfirmation.specRevision}`,
    ],
  }
}

/** AC-G3-02 的判定。 */
function verdictAcG302(input: {
  scoped: G3ChangeSetFact
  concurrent: G3ChangeSetFact
  concurrentApply: G3ChangeSetFact
  concurrentApplyStatus: number
  stale: G3ChangeSetFact
  projectAfterApply: G3ProjectState
  projectAfterStale: G3ProjectState
  revisionsAfter: G3ContextRevisionFact[]
  revisionsAfterStale: G3ContextRevisionFact[]
  tasksAfter: G3ImpactTaskFact[]
  tasksAfterStale: G3ImpactTaskFact[]
  reassessed: G3ChangeSetFact
}): G3Verdict {
  const sameBaseline =
    input.scoped.baseContextRevision === input.concurrent.baseContextRevision &&
    input.concurrent.baseContextRevision < input.projectAfterApply.currentContextRevision
  const rejected = input.concurrentApplyStatus === 409
  const staleMarked = input.stale.status === 'stale' && input.stale.staleReason !== ''
  const noNewRevision =
    input.revisionsAfterStale.length === input.revisionsAfter.length &&
    input.projectAfterStale.currentContextRevision === input.projectAfterApply.currentContextRevision
  const noNewTask =
    input.tasksAfterStale.length === input.tasksAfter.length &&
    !input.tasksAfterStale.some((task) => task.changeSetId === input.concurrent.id)

  return {
    id: 'AC-G3-02',
    title: '旧基线拒绝应用',
    holds: sameBaseline && rejected && staleMarked && noNewRevision && noNewTask,
    detail:
      sameBaseline && staleMarked && noNewRevision && noNewTask
        ? `两个变更集同基于上下文修订 ${input.concurrent.baseContextRevision}；先应用的推进到 ${input.projectAfterApply.currentContextRevision}，后应用的返回 409 并落为 STALE（stale_reason=${input.stale.staleReason}），未产生新上下文修订，也未产生影响任务。`
        : `不成立：同基线=${sameBaseline}，落陈旧=${staleMarked}，无新修订=${noNewRevision}，无新任务=${noNewTask}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets → HTTP 201 · change_set_id=${input.concurrent.id} · base_context_revision=${input.concurrent.baseContextRevision}`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/${input.concurrent.id}/apply → HTTP 409 · error_code=version_conflict`,
      `GET /api/v1/lingdoc/projects/{projectId}/change-sets/${input.concurrent.id} → HTTP 200 · status=${input.stale.status} · stale_reason=${input.stale.staleReason}`,
      `GET /api/v1/lingdoc/projects/{projectId}/context-revisions → HTTP 200 · revisions=[${input.revisionsAfter.map((r) => r.revision).join(',')}] → [${input.revisionsAfterStale.map((r) => r.revision).join(',')}]`,
      `GET /api/v1/lingdoc/projects/{projectId}/impact-tasks → HTTP 200 · task_count=${input.tasksAfter.length} → ${input.tasksAfterStale.length}`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/${input.concurrent.id}/reassess → HTTP 201 · change_set_id=${input.reassessed.id} · reassessed_from_id=${input.reassessed.reassessedFromId}（原记录留历史）`,
    ],
  }
}

/** AC-G3-04 的判定。 */
function verdictAcG304(input: {
  scoped: G3ChangeSetFact
  assessment: G3ImpactAssessmentFact
  tasksAfter: G3ImpactTaskFact[]
  unaffectedBefore: G3ChapterSnapshot
  unaffectedAfter: G3ChapterSnapshot
  unaffected: { serverReason: string | null; text: string }
}): G3Verdict {
  const inObjects = input.assessment.objects.some((object) => object.id === input.unaffectedAfter.chapterId)
  const inImpacts = input.assessment.impacts.some((impact) => impact.target.id === input.unaffectedAfter.chapterId)
  const inChangeSet = input.scoped.impacts.some((impact) => impact.chapter_id === input.unaffectedAfter.chapterId)
  const hasTask = input.tasksAfter.some((task) => task.target.id === input.unaffectedAfter.chapterId)
  const keptConfirmation = input.unaffectedAfter.versionConfirmationValid === true
  const holds = !inObjects && !inImpacts && !inChangeSet && !hasTask && keptConfirmation

  return {
    id: 'AC-G3-04',
    title: '局部影响',
    holds,
    detail: holds
      ? `未列入受影响范围的 ${input.unaffectedAfter.sectionId} 章节：影响评估的 objects / impacts 与变更集 impacts 都不含它，也没有为它产生影响任务，其章节版本的确认标记仍为 true（本次变更集未使其失效）。未受影响理由：${input.unaffected.text}`
      : `不成立：进评估对象=${inObjects}，进评估影响=${inImpacts}，进变更集=${inChangeSet}，有任务=${hasTask}，确认保持=${keptConfirmation}`,
    evidence: [
      `GET /api/v1/lingdoc/projects/{projectId}/change-sets/${input.scoped.id}/impact-assessment → HTTP 200 · objects=[${input.assessment.objects.map((object) => `${object.kind}/${object.id}`).join(' ')}] · impacts=[${input.assessment.impacts.map((impact) => `${impact.action}/${impact.certainty}`).join(' ')}]`,
      `GET /api/v1/lingdoc/projects/{projectId}/chapters/{unaffectedId}/versions → HTTP 200 · confirmation_valid ${input.unaffectedBefore.versionConfirmationValid} → ${input.unaffectedAfter.versionConfirmationValid} · spec_revision=${input.unaffectedAfter.versionSpecRevision}`,
      `GET /api/v1/lingdoc/projects/{projectId}/impact-tasks → HTTP 200 · task_count=${input.tasksAfter.length} · targets=[${input.tasksAfter.map((task) => task.target.id).join(' ')}]`,
      `GET /api/v1/lingdoc/projects/{projectId}/chapters/{unaffectedId}/confirmations/current → HTTP 200 · ${input.unaffectedAfter.currentConfirmation ? `confirmation_id=${input.unaffectedAfter.currentConfirmation.id}` : '无当前确认（规格修订已推进，章节读视图口径）'}`,
    ],
  }
}

/** AC-G3-11 的判定。 */
function verdictAcG311(input: {
  scoped: G3ChangeSetFact
  source: G3ChangeSetFact
  reversed: G3ChangeSetFact
  reverseAssessment: G3ImpactAssessmentFact
  reverseApplied: G3ChangeSetFact
  projectAfterApply: G3ProjectState
  projectAfterReverse: G3ProjectState
}): G3Verdict {
  const newRecord = input.reversed.id !== '' && input.reversed.id !== input.scoped.id
  const lineage = input.reversed.reversesChangeSetId === input.scoped.id
  // 原变更集历史仍可读：读回来还是同一条记录，且保留决定与修订信息。
  const sourceReadable =
    input.source.id === input.scoped.id &&
    input.source.status === 'applied' &&
    input.source.targetContextRevision !== null &&
    input.source.decisionBy !== ''
  const reversedFields = input.reversed.fields.length > 0
  const newRevision = input.projectAfterReverse.currentContextRevision > input.projectAfterApply.currentContextRevision
  const holds = newRecord && lineage && sourceReadable && reversedFields && newRevision

  return {
    id: 'AC-G3-11',
    title: '撤销变化',
    holds,
    detail: holds
      ? `撤销经由新建逆向变更集 ${input.reversed.id} 完成（reverses_change_set_id=${input.reversed.reversesChangeSetId}，基于当前修订 ${input.reversed.baseContextRevision}），原变更集 ${input.scoped.id} 仍可读（status=${input.source.status}），逆向变更集重新评估后应用，项目指针没有静默回退。`
      : `不成立：新记录=${newRecord}，血缘=${lineage}，原记录可读=${sourceReadable}，字段反向=${reversedFields}，新修订=${newRevision}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/${input.scoped.id}/reverse → HTTP 201 · change_set_id=${input.reversed.id} · status=${input.reversed.status} · reverses_change_set_id=${input.reversed.reversesChangeSetId} · base_context_revision=${input.reversed.baseContextRevision}`,
      `GET /api/v1/lingdoc/projects/{projectId}/change-sets/${input.scoped.id} → HTTP 200 · status=${input.source.status} · decision_by=${input.source.decisionBy} · base_context_revision=${input.source.baseContextRevision} · target_context_revision=${input.source.targetContextRevision}（原变更集历史仍可读）`,
      `GET /api/v1/lingdoc/projects/{projectId}/change-sets/${input.reversed.id}/impact-assessment → HTTP 200 · impacts=${input.reverseAssessment.impacts.length} · context_revision=${input.reverseAssessment.contextRevision}`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/${input.reversed.id}/apply → HTTP 200 · status=${input.reverseApplied.status} · target_context_revision=${input.reverseApplied.targetContextRevision}`,
    ],
  }
}
