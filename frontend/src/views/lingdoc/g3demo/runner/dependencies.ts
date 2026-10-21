/**
 * G3 验收台-4 · 依赖图与影响任务执行器。
 *
 * 这一层在 `runner/changeset.ts`（变更集生命周期）之上，把「依赖图 → 影响评估 → 影响任务」
 * 跑成真实调用：建立已确认与建议两类依赖边、对建议边做确认 / 修改 / 拒绝裁决、
 * 读影响评估并按服务端给出的确定性分组、处置影响任务（先指派给当前操作者，
 * 再以真实闭环证据完成；非阻断任务可「接受本次不处理」；依据消失的任务落为失效）。
 *
 * 写死的硬约束（不靠人记）：
 *
 * 1. **分组完全采用服务端返回的 `certainty`**：确定性只读 `impacts[].certainty`，
 *    页面与执行器都不自行重算。必需对象集合直接取 `assessment.objects`，
 *    来源对象直接取 `impacts[].causes[].source`。
 * 2. **影响任务的动作词表**只有 `assign / start / wait / complete / dismiss`；
 *    `NO_ACTION` 是**影响解析结果**，不是任务动作，也不创建任务。
 * 3. **闭环只有 `DONE` 与 `DISMISSED`**；`STALE` 是终态但**不算闭环**
 *    （既不算闭环，也不阻断稳定化，只被单独上报）。
 * 4. **blocking 一律不可「接受本次不处理」**，只能以闭环证据完成。
 * 5. **`complete` 之前必须先 `assign` 给当前操作者**，否则服务端回 **404**（不是 403）。
 * 6. **严重度由服务端按动作推导**，调用方不传、不覆盖。
 *
 * AC-G3-05 的构造：同一个变更集里建**两条来源边指向同一目标**（动作相同），
 * 服务端把它们合并成**一条逻辑 ImpactTask**，`causes` 保留两条上游原因；
 * 重复应用同一变更集不会产生第二条任务。
 *
 * AC-G3-13 的构造：建一条**建议**（suggested）边指向某章节，它只出现在「可能受影响」组，
 * 不使章节失效、也不产生任务，因而不构成导出阻断项。
 */

import { envelopeData, errorCodeOf, type G3HttpClient } from './httpClient'
import {
  assessChangeSet,
  applyChangeSet,
  confirmChapter,
  createChangeSet,
  readChangeSet,
  readImpactAssessment,
  readImpactTaskFact,
  readChapterSnapshot,
  readProjectState,
  writeChapterVersion,
  G3_CS_STEPS,
  type G3ChangeSetFact,
  type G3ImpactAssessmentFact,
  type G3ImpactObjectFact,
  type G3ImpactTaskFact,
  type G3ProjectState,
  type G3Verdict,
} from './changeset'
import { makeIdempotencyKey, runStep, type G3RunContext } from './runContext'

/**
 * 本链的步骤名。
 *
 * 它们是**矩阵规则表的锚**（见 `g3Evidence.ts` 的 `G3_ROW_EVIDENCE_RULES`）：
 * 名字对不上时，矩阵那几行会退回「未被本轮覆盖」，而不是给出假的 `PASS`。
 */
export const G3_DEP_STEPS = {
  /** 建立已确认（origin=user）的依赖边。 */
  createEdge: '建立已确认依赖边',
  /** 建立建议（origin=ai，status=suggested）的依赖边。 */
  createSuggestedEdge: '建立建议依赖边',
  /** 对建议边做「确认」裁决。 */
  decideConfirm: '裁决依赖边-确认',
  /** 对建议边做「修改」裁决。 */
  decideModify: '裁决依赖边-修改',
  /** 对建议边做「拒绝」裁决。 */
  decideReject: '裁决依赖边-拒绝',
  /** 读回依赖边列表。 */
  readEdges: '读依赖边列表',
  /** 重投被拒绝的候选边。 */
  reproposeRejected: '重投被拒绝的候选边',
  /**
   * 提交一批**依赖候选**（AI 侧提案）。
   *
   * 这一组名字是 `g3Evidence.ts` 里 AC-G3-15 那一行的 `requires`：
   * 候选与已确认边**分表**存放（服务端 `impact_dependency_candidate.go`），
   * 所以「批量提出候选」这条链的步骤名带「助手-」前缀，与矩阵规则表对齐。
   * 名字对不上时，矩阵那行会退回「未被本轮覆盖」，而不是给出假的 `PASS`。
   */
  submitCandidates: '助手-提交候选批次',
  /** 对候选做「确认」裁决。 */
  decideCandidateConfirm: '助手-候选决策-确认',
  /** 对候选做「修改」裁决。 */
  decideCandidateModify: '助手-候选决策-修改',
  /** 对候选做「拒绝」裁决。 */
  decideCandidateReject: '助手-候选决策-拒绝',
  /** 在同一版本里重投被拒候选（服务端应当抑制，不新建）。 */
  reproposeRejectedCandidate: '助手-重投被拒候选',
  /** 确认一个依据版本已变更的候选（期望被 409 拒掉）。 */
  decideStaleCandidate: '助手-确认版本已变更的候选',
  /** 创建变更草稿（AC-G3-14 的实跑那半段）。 */
  createDraft: '创建变更草稿',
  /** 读影响任务。 */
  readTasks: '读影响任务',
  /** 重复应用同一个变更集（验证不产生第二条任务）。 */
  replayApply: '重复应用变更集',
  /** 未指派就完成（预期 404）。 */
  completeWithoutAssign: '未指派直接完成',
  /** 指派给当前操作者。 */
  assignTask: '指派影响任务',
  /** 开始处置。 */
  startTask: '开始处置影响任务',
  /** 以闭环证据完成。 */
  completeTask: '完成影响任务',
  /** 接受本次不处理（非阻断任务）。 */
  dismissTask: '接受不处理影响任务',
  /** 触发任务失效（依据版本被替换）。 */
  staleTask: '触发任务失效',
} as const

/**
 * 影响任务的**处置动作**词表。
 *
 * 注意它与「影响解析动作」（`REVIEW_REQUIRED` / `NO_ACTION` …）是两套词：
 * 后者是 `impacts[].action`，前者才是 `POST /impact-tasks/{id}/actions` 的 `action`。
 */
export const IMPACT_TASK_ACTION_VOCABULARY = ['assign', 'start', 'wait', 'complete', 'dismiss'] as const
export type G3ImpactTaskAction = (typeof IMPACT_TASK_ACTION_VOCABULARY)[number]

/** 闭环状态集合：只有 `DONE` 与 `DISMISSED`（`STALE` 是终态但不算闭环）。 */
export const IMPACT_TASK_CLOSED_STATUSES = ['DONE', 'DISMISSED'] as const

/** 终态集合：`STALE` 在其中，但它**不算闭环**。 */
export const IMPACT_TASK_TERMINAL_STATUSES = ['DONE', 'DISMISSED', 'STALE'] as const

/** 是不是闭环状态。 */
export function isClosedTaskStatus(status: string): boolean {
  return (IMPACT_TASK_CLOSED_STATUSES as readonly string[]).includes(status)
}

/** 是不是终态。 */
export function isTerminalTaskStatus(status: string): boolean {
  return (IMPACT_TASK_TERMINAL_STATUSES as readonly string[]).includes(status)
}

/**
 * 能不能「接受本次不处理」。
 *
 * 只有**非阻断**影响才可以被责任人接受掉；阻断影响只能以闭环证据完成。
 */
export function canDismissTask(severity: string): boolean {
  return severity !== 'blocking'
}

/** 对象版本引用。 */
export interface G3ObjectVersionRef {
  kind: string
  id: string
  version: string
}

/** 依赖边事实。 */
export interface G3DependencyEdgeFact {
  id: string
  projectId: string
  source: G3ObjectVersionRef
  target: G3ObjectVersionRef
  relation: string
  action: string
  origin: string
  reason: string
  /** `confirmed` / `suggested` / `rejected`。 */
  status: string
  confidence: number
  version: number
  /** 被人工「修改」掉的旧关系与动作。 */
  supersededRelation: string
  supersededAction: string
}

/**
 * 一条**依赖候选**（AI 侧提案）。
 *
 * 候选与已确认的依赖边**分表**存放：候选在决策之前**不是边**，
 * 只有「确认 / 修改」才产生依赖边，「拒绝」不产生边
 * （服务端 `internal/lingdoc/workspacecore/impact_dependency_candidate.go`）。
 */
export interface G3DependencyCandidateFact {
  id: string
  batchId: string
  source: G3ObjectVersionRef
  target: G3ObjectVersionRef
  relation: string
  action: string
  /** 依据片段。 */
  evidence: string
  /** 建议理由。 */
  reason: string
  confidence: number
  /** `proposed` / `confirmed` / `modified` / `rejected` / `stale`。 */
  status: string
  /** 人工结论：`confirm` / `modify` / `reject`。 */
  decision: string
  /** 被人工「修改」掉的旧关系与动作。 */
  supersededRelation: string
  supersededAction: string
  version: number
  /** 候选提出时的项目版本：人工结论绑在它上面。 */
  projectVersion: number
  /** 确认 / 修改产生的依赖边；**被拒绝的候选没有边**。 */
  edgeId: string
}

/** 同一版本里被抑制的提案（已有人工结论，不再重复推荐）。 */
export interface G3DependencySuppressionFact {
  source: G3ObjectVersionRef
  target: G3ObjectVersionRef
  relation: string
  action: string
  /** `rejected_in_same_version` 等服务端给出的抑制原因。 */
  reason: string
  previousCandidateId: string
  previousStatus: string
}

/** 一批依赖候选。 */
export interface G3DependencyCandidateBatchFact {
  id: string
  status: string
  projectVersion: number
  contextRevision: number
  candidates: G3DependencyCandidateFact[]
  suppressed: G3DependencySuppressionFact[]
}

/** 一次候选决策的读回结果。 */
export interface G3CandidateDecisionFact {
  candidateId: string
  decision: string
  /** 候选的读回状态。 */
  status: string
  /** HTTP 状态码（被拒时是 409 / 422 等）。 */
  httpStatus: number
  /** 服务端错误码。 */
  code: string
  /** 产生的依赖边；**被拒绝的候选没有边**。 */
  edgeId: string
  relation: string
  action: string
  supersededRelation: string
  supersededAction: string
}

/** 影响任务的一条上游原因。 */
export interface G3ImpactCauseFact {
  edgeId: string
  source: G3ObjectVersionRef
  reason: string
}

/** 影响任务事实（含处置所需的全部字段）。 */
export interface G3ImpactTaskDetailFact {
  id: string
  changeSetId: string
  target: G3ObjectVersionRef
  targetRevision: number
  action: string
  severity: string
  responsibilityDomain: string
  assigneeId: string
  status: string
  causes: G3ImpactCauseFact[]
  version: number
  disposition: string
  humanDecision: string
  closureEvidence: G3ObjectVersionRef[]
  staleReason: string
  staleAt: string
}

/** 分组里的一条影响。 */
export interface G3ImpactGroupEntry {
  target: G3ObjectVersionRef
  action: string
  /** 服务端返回的确定性，原样保留。 */
  certainty: string
  /** 来源对象：直接取 `impacts[].causes[].source`。 */
  sources: G3ObjectVersionRef[]
  causes: G3ImpactCauseFact[]
}

/** 三组呈现。 */
export interface G3ImpactGrouping {
  /** 确定受影响：服务端 `certainty === 'definite'`。 */
  definite: G3ImpactGroupEntry[]
  /** 可能受影响：服务端返回的其余确定性。 */
  possible: G3ImpactGroupEntry[]
  /** 当前未发现影响：既不在 `assessment.objects`、也不在 `impacts` 里的已知对象。 */
  unaffected: G3ObjectVersionRef[]
  /** 确定影响对应的必需对象集合：直接取 `assessment.objects`。 */
  requiredObjects: G3ImpactObjectFact[]
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

function refOf(value: unknown): G3ObjectVersionRef {
  const raw = objectOf(value)
  return { kind: str(raw.kind), id: str(raw.id), version: str(raw.version) }
}

function projectPath(projectId: string, suffix: string): string {
  return `/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}${suffix}`
}

/** 把后端返回的依赖边读成事实。 */
export function readDependencyEdgeFact(data: unknown): G3DependencyEdgeFact {
  const raw = objectOf(data)
  return {
    id: str(raw.id),
    projectId: str(raw.project_id),
    source: refOf(raw.source),
    target: refOf(raw.target),
    relation: str(raw.relation),
    action: str(raw.action),
    origin: str(raw.origin),
    reason: str(raw.reason),
    status: str(raw.status),
    confidence: num(raw.confidence),
    version: num(raw.version),
    supersededRelation: str(raw.superseded_relation),
    supersededAction: str(raw.superseded_action),
  }
}

/** 把后端返回的依赖候选读成事实。 */
export function readDependencyCandidateFact(data: unknown): G3DependencyCandidateFact {
  const raw = objectOf(data)
  return {
    id: str(raw.id),
    batchId: str(raw.batch_id),
    source: refOf(raw.source),
    target: refOf(raw.target),
    relation: str(raw.relation),
    action: str(raw.action),
    evidence: str(raw.evidence),
    reason: str(raw.reason),
    confidence: num(raw.confidence),
    status: str(raw.status),
    decision: str(raw.decision),
    supersededRelation: str(raw.superseded_relation),
    supersededAction: str(raw.superseded_action),
    version: num(raw.version),
    projectVersion: num(raw.project_version),
    edgeId: str(raw.edge_id),
  }
}

/** 把后端返回的抑制记录读成事实。 */
export function readDependencySuppressionFact(data: unknown): G3DependencySuppressionFact {
  const raw = objectOf(data)
  return {
    source: refOf(raw.source),
    target: refOf(raw.target),
    relation: str(raw.relation),
    action: str(raw.action),
    reason: str(raw.reason),
    previousCandidateId: str(raw.previous_candidate_id),
    previousStatus: str(raw.previous_status),
  }
}

/** 把后端返回的候选批次读成事实。 */
export function readDependencyCandidateBatchFact(data: unknown): G3DependencyCandidateBatchFact {
  const raw = objectOf(data)
  return {
    id: str(raw.id),
    status: str(raw.status),
    projectVersion: num(raw.project_version),
    contextRevision: num(raw.context_revision),
    candidates: (Array.isArray(raw.candidates) ? raw.candidates : []).map(readDependencyCandidateFact),
    suppressed: (Array.isArray(raw.suppressed) ? raw.suppressed : []).map(readDependencySuppressionFact),
  }
}

/** 把后端返回的影响任务读成事实。 */
export function readImpactTaskDetailFact(data: unknown): G3ImpactTaskDetailFact {
  const raw = objectOf(data)
  return {
    id: str(raw.id),
    changeSetId: str(raw.change_set_id),
    target: refOf(raw.target),
    targetRevision: num(raw.target_revision),
    action: str(raw.action),
    severity: str(raw.severity),
    responsibilityDomain: str(raw.responsibility_domain),
    assigneeId: str(raw.assignee_id),
    status: str(raw.status),
    causes: (Array.isArray(raw.causes) ? raw.causes : []).map((cause) => {
      const item = objectOf(cause)
      return {
        edgeId: str(item.edge_id),
        source: refOf(item.source),
        reason: str(item.reason),
      }
    }),
    version: num(raw.version),
    disposition: str(raw.disposition),
    humanDecision: str(raw.human_decision),
    closureEvidence: (Array.isArray(raw.closure_evidence) ? raw.closure_evidence : []).map(refOf),
    staleReason: str(raw.stale_reason),
    staleAt: str(raw.stale_at),
  }
}

/**
 * 三组呈现。
 *
 * **页面不自行重算确定性**：分组只读服务端返回的 `certainty`；
 * 必需对象集合直接取 `assessment.objects`；来源对象直接取 `impacts[].causes[].source`。
 */
export function groupImpactAssessment(
  assessment: G3ImpactAssessmentFact,
  knownObjects: readonly G3ObjectVersionRef[],
): G3ImpactGrouping {
  const entryOf = (impact: G3ImpactAssessmentFact['impacts'][number]): G3ImpactGroupEntry => ({
    target: impact.target,
    action: impact.action,
    certainty: impact.certainty,
    sources: impact.causes.map((cause) => cause.source),
    causes: impact.causes,
  })

  const definite = assessment.impacts.filter((impact) => impact.certainty === 'definite').map(entryOf)
  const possible = assessment.impacts.filter((impact) => impact.certainty !== 'definite').map(entryOf)

  const inObjects = (ref: G3ObjectVersionRef): boolean =>
    assessment.objects.some((item) => item.kind === ref.kind && item.id === ref.id)
  const inImpacts = (ref: G3ObjectVersionRef): boolean =>
    assessment.impacts.some((impact) => impact.target.kind === ref.kind && impact.target.id === ref.id)

  return {
    definite,
    possible,
    unaffected: knownObjects.filter((ref) => !inObjects(ref) && !inImpacts(ref)),
    requiredObjects: assessment.objects,
  }
}

/** 建立依赖边。`origin=user` 得到已确认边，`origin=ai` 得到建议边。 */
export async function createDependencyEdge(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedContextRevision: number
    source: G3ObjectVersionRef
    target: G3ObjectVersionRef
    relation: string
    action: string
    origin: 'user' | 'rule' | 'ai'
    reason: string
    confidence: number
    expectedStatus?: number | readonly number[]
  },
): Promise<G3DependencyEdgeFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, '/dependencies'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    expectedStatus: options.expectedStatus,
    body: {
      expected_context_revision: options.expectedContextRevision,
      source: options.source,
      target: options.target,
      relation: options.relation,
      action: options.action,
      origin: options.origin,
      reason: options.reason,
      confidence: options.confidence,
    },
    conclusion: `依赖边已建立（HTTP {status}）：${options.source.kind} → ${options.target.kind} · ${options.relation}`,
    detail: (body) => {
      const edge = readDependencyEdgeFact(objectOf(body).data)
      return {
        edge_id: edge.id,
        status: edge.status,
        origin: edge.origin,
        action: edge.action,
        relation: edge.relation,
        version: edge.version,
        error_code: errorCodeOf(body),
      }
    },
  })
  return readDependencyEdgeFact(envelopeData(data))
}

/**
 * 对**建议**依赖边做裁决：确认 / 修改 / 拒绝。
 *
 * 只有 `suggested` 状态的边可以被裁决；`modify` 必须至少给出一个可改字段
 * （关系、动作、置信度），其余两种裁决不能带这些字段（服务端会拒）。
 */
export async function decideDependencyEdge(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  edgeId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedVersion: number
    decision: 'confirm' | 'modify' | 'reject'
    reason: string
    relation?: string
    action?: string
    confidence?: number
  },
): Promise<G3DependencyEdgeFact> {
  const body: Record<string, unknown> = {
    expected_version: options.expectedVersion,
    decision: options.decision,
    reason: options.reason,
  }
  if (options.decision === 'modify') {
    if (options.relation !== undefined) body.relation = options.relation
    if (options.action !== undefined) body.action = options.action
    if (options.confidence !== undefined) body.confidence = options.confidence
  }

  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/dependencies/${encodeURIComponent(edgeId)}/decision`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    body,
    conclusion: `依赖边裁决「${options.decision}」已提交（HTTP {status}）`,
    detail: (body2) => {
      const edge = readDependencyEdgeFact(objectOf(body2).data)
      return {
        edge_id: edge.id,
        decision: options.decision,
        status: edge.status,
        relation: edge.relation,
        action: edge.action,
        confidence: edge.confidence,
        version: edge.version,
        superseded_relation: edge.supersededRelation,
        superseded_action: edge.supersededAction,
        error_code: errorCodeOf(body2),
      }
    },
  })
  return readDependencyEdgeFact(envelopeData(data))
}

/** 读回依赖边列表。 */
export async function listDependencyEdges(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string = G3_DEP_STEPS.readEdges,
): Promise<G3DependencyEdgeFact[]> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, '/dependencies'),
    token,
    conclusion: '依赖边已读回（HTTP {status}）',
    detail: (body) => {
      const list = Array.isArray(objectOf(body).data) ? (objectOf(body).data as unknown[]) : []
      return {
        edge_count: list.length,
        edges: list.map((item) => {
          const edge = readDependencyEdgeFact(item)
          return `${edge.relation}:${edge.status}`
        }),
      }
    },
  })
  const list = Array.isArray(envelopeData(data)) ? (envelopeData(data) as unknown[]) : []
  return list.map(readDependencyEdgeFact)
}

/**
 * 提交一批**依赖候选**（AI 侧提案）。
 *
 * 两条硬约束（服务端按对象引用校验，违反会被拒）：
 * - `source` 必须是**变更集 roots 里的 ProjectSpec ref**（影响评估从 roots 出发
 *   遍历依赖边，source 不是那个 ref 时 action 不传播）；
 * - `target` 必须是**本项目**的章节版本（拿别的项目的对象引用会被 404 拒掉）。
 */
export async function submitDependencyCandidateBatch(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedContextRevision: number
    reason: string
    candidates: {
      source: G3ObjectVersionRef
      target: G3ObjectVersionRef
      relation: string
      action: string
      evidence: string
      reason: string
      confidence: number
    }[]
  },
): Promise<G3DependencyCandidateBatchFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, '/dependency-candidates'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    body: {
      expected_context_revision: options.expectedContextRevision,
      reason: options.reason,
      scope: {},
      candidates: options.candidates,
    },
    conclusion: `候选依赖批次已提交（HTTP {status}）`,
    detail: (body) => {
      const batch = readDependencyCandidateBatchFact(objectOf(body).data)
      return {
        batch_id: batch.id,
        status: batch.status,
        candidate_count: batch.candidates.length,
        suppressed_count: batch.suppressed.length,
        error_code: errorCodeOf(body),
      }
    },
  })
  return readDependencyCandidateBatchFact(data)
}

/**
 * 对一个**候选**做三态决策（确认 / 修改 / 拒绝）。
 *
 * 「拒绝」不产生依赖边；「确认 / 修改」产生一条边并记住被替换的元组。
 * `expectedStatus` 用于「预期失败」的证据步骤（例如依据版本已变更 → 409）。
 */
export async function decideDependencyCandidate(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  candidateId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedVersion: number
    decision: 'confirm' | 'modify' | 'reject'
    reason: string
    relation?: string
    action?: string
    confidence?: number
    expectedStatus?: number | readonly number[]
    /** 允许失败：失败也记录，但不中断整条链（例如「依据版本已变更」本该被拒）。 */
    allowFailure?: boolean
  },
): Promise<G3CandidateDecisionFact> {
  const body: Record<string, unknown> = {
    expected_version: options.expectedVersion,
    decision: options.decision,
    reason: options.reason,
  }
  if (options.decision === 'modify') {
    if (options.relation !== undefined) body.relation = options.relation
    if (options.action !== undefined) body.action = options.action
    if (options.confidence !== undefined) body.confidence = options.confidence
  }

  const { response, data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/dependency-candidates/${encodeURIComponent(candidateId)}/decision`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    expectedStatus: options.expectedStatus,
    allowFailure: options.allowFailure,
    body,
    conclusion: `候选决策「${options.decision}」已提交（HTTP {status}）`,
    detail: (raw) => {
      const payload = objectOf(objectOf(raw).data)
      const candidate = objectOf(payload.candidate)
      const edge = objectOf(payload.edge)
      return {
        candidate_id: candidate.id,
        status: candidate.status,
        decision: candidate.decision,
        edge_id: edge.id,
        superseded_relation: candidate.superseded_relation,
        superseded_action: candidate.superseded_action,
        error_code: errorCodeOf(raw),
      }
    },
  })
  const payload = objectOf(data)
  const candidate = objectOf(payload.candidate)
  const edge = objectOf(payload.edge)
  return {
    candidateId: str(candidate.id) || candidateId,
    decision: options.decision,
    status: str(candidate.status),
    httpStatus: response.status,
    code: errorCodeOf(response.body),
    edgeId: str(edge.id),
    relation: str(candidate.relation),
    action: str(candidate.action),
    supersededRelation: str(candidate.superseded_relation),
    supersededAction: str(candidate.superseded_action),
  }
}

/** 读回影响任务（含处置所需的全部字段）。 */
export async function listImpactTaskDetails(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string = G3_DEP_STEPS.readTasks,
): Promise<G3ImpactTaskDetailFact[]> {
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
          const task = readImpactTaskDetailFact(item)
          return `${task.action}:${task.severity}:${task.status}:causes=${task.causes.length}`
        }),
      }
    },
  })
  const list = Array.isArray(envelopeData(data)) ? (envelopeData(data) as unknown[]) : []
  return list.map(readImpactTaskDetailFact)
}

/**
 * 处置影响任务。
 *
 * `action` 只能取词表内的值；**严重度不传**（服务端按动作推导，ADR 0003）。
 * `expectedStatus` 用于「预期失败」的证据步骤（例如未指派就完成 → 404）。
 */
export async function actOnImpactTask(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  taskId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedVersion: number
    targetRevision: number
    action: G3ImpactTaskAction
    reason: string
    closureEvidence?: G3ObjectVersionRef[]
    assigneeId?: string
    humanDecision?: string
    expectedStatus?: number | readonly number[]
  },
): Promise<{ status: number; code: string; task: G3ImpactTaskDetailFact | null }> {
  const body: Record<string, unknown> = {
    expected_version: options.expectedVersion,
    target_revision: options.targetRevision,
    action: options.action,
    reason: options.reason,
    closure_evidence: options.closureEvidence ?? [],
  }
  if (options.assigneeId) body.assignee_id = options.assigneeId
  if (options.humanDecision) body.human_decision = options.humanDecision

  const { response, data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/impact-tasks/${encodeURIComponent(taskId)}/actions`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    expectedStatus: options.expectedStatus,
    body,
    conclusion: `影响任务处置「${options.action}」（HTTP {status}）`,
    detail: (body2) => {
      const task = readImpactTaskDetailFact(objectOf(body2).data)
      return {
        task_id: task.id || taskId,
        action: options.action,
        status: task.status,
        severity: task.severity,
        assignee_id: task.assigneeId,
        version: task.version,
        disposition: task.disposition,
        stale_reason: task.staleReason,
        closure_evidence_count: task.closureEvidence.length,
        error_code: errorCodeOf(body2),
      }
    },
  })
  const task = readImpactTaskDetailFact(envelopeData(data))
  return {
    status: response.status,
    code: errorCodeOf(response.body),
    task: task.id ? task : null,
  }
}

/** 创建变更草稿（AC-G3-14 实跑的那半段）。 */
export async function createChangeDraft(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedContextRevision: number
    fields: Record<string, { old_value: string; new_value: string }>
    affectedChapterIds: string[]
    reason: string
  },
): Promise<G3ChangeSetFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, '/change-sets/drafts'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    body: {
      expected_context_revision: options.expectedContextRevision,
      fields: options.fields,
      affected_chapter_ids: options.affectedChapterIds,
      reason: options.reason,
    },
    conclusion: `变更草稿已生成（HTTP {status}）`,
    detail: (body) => {
      const change = objectOf(objectOf(body).data)
      return {
        change_set_id: change.id,
        status: change.status,
        base_context_revision: change.base_context_revision,
        affected_chapter_ids: options.affectedChapterIds,
      }
    },
  })
  const raw = objectOf(envelopeData(data))
  return {
    id: str(raw.id),
    projectId: str(raw.project_id),
    status: str(raw.status),
    reason: str(raw.reason),
    baseContextRevision: num(raw.base_context_revision),
    targetContextRevision: null,
    baseSpecRevision: num(raw.base_spec_revision),
    targetSpecRevision: null,
    createdBy: str(raw.created_by),
    decisionBy: '',
    staleReason: '',
    reassessedFromId: '',
    reversesChangeSetId: '',
    fields: [],
    impacts: [],
  }
}

/** 链的输入。 */
export interface G3DependencyChainOptions {
  projectId: string
  /** 模板激活后自动创建的两个章节。 */
  chapters: { id: string; sectionId: string; title: string }[]
  /** 当前操作者（指派与闭环都由他执行）。 */
  operatorId: string
}

/**
 * 候选三态的事实集合（AC-G3-15）。
 *
 * 三条语义各有各的落点，缺一条判定就不成立：
 * - **拒绝不产生边**：`decisions` 里拒绝那条的 `edgeId` 为空，且决策前后
 *   依赖边只多了两条（确认 + 修改各一条）；
 * - **同版本不再重提**：`reproposal` 里没有新建候选，只有抑制记录；
 * - **确认时版本复检**：`staleConfirmation` 是 409（依据版本已变更）。
 */
export interface G3CandidateChainFacts {
  /** 提交的那一批候选。 */
  batch: G3DependencyCandidateBatchFact | null
  /** 三态决策的读回结果（确认 / 修改 / 拒绝）。 */
  decisions: G3CandidateDecisionFact[]
  /** 同版本里重投被拒候选的批次（新建 0 条 + 抑制 ≥1 条）。 */
  reproposal: G3DependencyCandidateBatchFact | null
  /** 依据版本已变更的候选在确认时的回应（期望 409）。 */
  staleConfirmation: G3CandidateDecisionFact | null
  /** 候选决策**之前**的依赖边条数。 */
  edgesBeforeDecisions: number
  /** 候选决策**之后**的依赖边条数。 */
  edgesAfterDecisions: number
}

/** 链的结果。 */
export interface G3DependencyChainResult {
  /** 变更集。 */
  changeSet: G3ChangeSetFact
  /** 变更草稿（AC-G3-14 的实跑那半段）。 */
  draft: G3ChangeSetFact
  /** 读到的依赖边。 */
  edges: G3DependencyEdgeFact[]
  /** 影响评估。 */
  assessment: G3ImpactAssessmentFact
  /** 三组呈现。 */
  grouping: G3ImpactGrouping
  /** 应用后的影响任务。 */
  tasks: G3ImpactTaskDetailFact[]
  /** 重复应用之后的影响任务（用于验证不产生第二条任务）。 */
  tasksAfterReplay: G3ImpactTaskDetailFact[]
  /** 未指派就完成时的服务端回应。 */
  unassignedCompletion: { status: number; code: string }
  /** 每条任务的状态迁移。 */
  transitions: G3TaskTransition[]
  /** 建议边的裁决结果。 */
  decisions: {
    confirm: G3DependencyEdgeFact
    modify: G3DependencyEdgeFact
    reject: G3DependencyEdgeFact
    reproposed: G3DependencyEdgeFact
  }
  /** 候选批次的三态事实（AC-G3-15）。 */
  candidates: G3CandidateChainFacts
  /** 建议边（AC-G3-13）。 */
  suggestedEdge: G3DependencyEdgeFact
  /** 建议边目标章节的版本级确认标记（应用后）。 */
  suggestedTargetVersionConfirmationValid: boolean | null
  /** 已知对象（用于「当前未发现影响」组）。 */
  knownObjects: G3ObjectVersionRef[]
  /** 判定。 */
  verdicts: G3Verdict[]
}

/** 一条任务的状态迁移。 */
export interface G3TaskTransition {
  taskId: string
  action: string
  severity: string
  from: string
  to: string
  /** 这一步带上的闭环证据。 */
  closureEvidence: G3ObjectVersionRef[]
  /** 服务端回应的说明。 */
  note: string
  status: number
  code: string
}

/**
 * 依赖图与影响任务链的页面快照。
 *
 * 只留页面要展示的字段，且全部可 JSON 序列化：它跟着运行摘要落进浏览器本地存储。
 */
export interface G3DependencySnapshot {
  verdicts: G3Verdict[]
  changeSet: G3ChangeSetFact
  draft: G3ChangeSetFact
  edges: G3DependencyEdgeFact[]
  assessment: G3ImpactAssessmentFact
  grouping: G3ImpactGrouping
  tasks: G3ImpactTaskDetailFact[]
  tasksAfterReplay: G3ImpactTaskDetailFact[]
  unassignedCompletion: { status: number; code: string }
  transitions: G3TaskTransition[]
  decisions: G3DependencyChainResult['decisions']
  candidates: G3CandidateChainFacts
  suggestedEdge: G3DependencyEdgeFact
  suggestedTargetVersionConfirmationValid: boolean | null
  knownObjects: G3ObjectVersionRef[]
}

/** 把链结果压成页面快照。 */
export function toDependencySnapshot(result: G3DependencyChainResult): G3DependencySnapshot {
  return {
    verdicts: result.verdicts,
    changeSet: result.changeSet,
    draft: result.draft,
    edges: result.edges,
    assessment: result.assessment,
    grouping: result.grouping,
    tasks: result.tasks,
    tasksAfterReplay: result.tasksAfterReplay,
    unassignedCompletion: result.unassignedCompletion,
    transitions: result.transitions,
    decisions: result.decisions,
    candidates: result.candidates,
    suggestedEdge: result.suggestedEdge,
    suggestedTargetVersionConfirmationValid: result.suggestedTargetVersionConfirmationValid,
    knownObjects: result.knownObjects,
  }
}

/**
 * 跑完整条「依赖图 → 影响评估 → 影响任务」链。
 *
 * 顺序是**状态机的前置条件**：依赖边必须先于评估（评估读的是当时的图），
 * 评估必须先于应用（应用会复核评估指纹），指派必须先于完成。
 */
export async function runDependencyChain(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: G3DependencyChainOptions,
): Promise<G3DependencyChainResult> {
  const question = options.chapters.find((chapter) => chapter.sectionId === 'question')
  const method = options.chapters.find((chapter) => chapter.sectionId === 'method')
  if (!question || !method) {
    throw new Error('依赖图链需要模板激活后的 question / method 两个章节')
  }
  if (!options.operatorId) {
    throw new Error('依赖图链需要当前操作者（用于指派影响任务）')
  }

  const project: G3ProjectState = await readProjectState(ctx, client, token, options.projectId)

  // —— 前置：两个章节都要有正文且已确认，ChapterVersion 才是可用的依赖对象 ——
  const chapterVersions: Record<string, string> = {}
  for (const chapter of [question, method]) {
    const written = await writeChapterVersion(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_CS_STEPS.writeChapter,
      idemLabel: `dep-chapter-body-${chapter.sectionId}`,
      expectedChapterVersionId: null,
      expectedSpecRevision: project.specRevision,
      bodyMarkdown: `## ${chapter.title}\n\nG3 验收台-4 依赖图演示正文（运行 ${ctx.runId}）。`,
    })
    if (!written.currentVersionId) {
      throw new Error(`章节 ${chapter.sectionId} 保存正文后没有拿到当前版本`)
    }
    chapterVersions[chapter.sectionId] = written.currentVersionId
    await confirmChapter(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_CS_STEPS.confirmChapter,
      idemLabel: `dep-confirm-${chapter.sectionId}`,
      expectedChapterVersionId: written.currentVersionId,
      expectedSpecRevision: project.specRevision,
    })
  }

  // —— 依赖对象引用（kind 取值见 internal/lingdoc/workspacecore/impact_service.go 的 validObjectRef）——
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
  const ruleSetRef: G3ObjectVersionRef = {
    kind: 'RuleSet',
    id: project.templateId ?? '',
    version: project.rulesetHash ?? '',
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

  const knownObjects: G3ObjectVersionRef[] = [
    specRef,
    templateRef,
    methodRef,
    questionRef,
    ...(ruleSetRef.version ? [ruleSetRef] : []),
  ]

  // —— 已确认依赖边：两条来源边指向同一目标、动作相同（AC-G3-05 的构造）——
  const specToTemplate = await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_DEP_STEPS.createEdge,
    idemLabel: 'dep-edge-spec-template',
    expectedContextRevision: project.currentContextRevision,
    source: specRef,
    target: templateRef,
    relation: 'spec_regenerates_template',
    action: 'REGENERATE',
    origin: 'user',
    reason: '研究条件变化后需要重新生成模板派生内容',
    confidence: 1,
  })
  const specToMethod = await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_DEP_STEPS.createEdge,
    idemLabel: 'dep-edge-spec-method',
    expectedContextRevision: project.currentContextRevision,
    source: specRef,
    target: methodRef,
    relation: 'spec_invalidates_method',
    action: 'INVALIDATE_CONFIRMATION',
    origin: 'user',
    reason: '研究条件变化使研究方案章节的确认失效',
    confidence: 1,
  })
  const templateToMethod = await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_DEP_STEPS.createEdge,
    idemLabel: 'dep-edge-template-method',
    expectedContextRevision: project.currentContextRevision,
    source: templateRef,
    target: methodRef,
    relation: 'template_invalidates_method',
    action: 'INVALIDATE_CONFIRMATION',
    origin: 'user',
    reason: '模板规则集变化同样使研究方案章节的确认失效',
    confidence: 1,
  })

  // —— 建议依赖边：AC-G3-13 的构造（只提示，不使章节失效）——
  const suggestedEdge = await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_DEP_STEPS.createSuggestedEdge,
    idemLabel: 'dep-edge-suggested-question',
    expectedContextRevision: project.currentContextRevision,
    source: specRef,
    target: questionRef,
    relation: 'candidate_question_dependency',
    action: 'REVIEW_REQUIRED',
    origin: 'ai',
    reason: '模型提出的候选关系：研究问题可能依赖研究条件版本',
    confidence: 0.6,
  })

  // —— AC-G3-15 的三态裁决（候选由人工逐条提出，不是 AI 批量提出）——
  const candidateA = await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_DEP_STEPS.createSuggestedEdge,
    idemLabel: 'dep-edge-candidate-a',
    expectedContextRevision: project.currentContextRevision,
    source: specRef,
    target: templateRef,
    relation: 'candidate_a',
    action: 'NO_ACTION',
    origin: 'ai',
    reason: '候选关系 A：待人工判断是否成立',
    confidence: 0.4,
  })
  const candidateB = await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_DEP_STEPS.createSuggestedEdge,
    idemLabel: 'dep-edge-candidate-b',
    expectedContextRevision: project.currentContextRevision,
    source: specRef,
    target: templateRef,
    relation: 'candidate_b',
    action: 'NO_ACTION',
    origin: 'ai',
    reason: '候选关系 B：待人工判断是否成立',
    confidence: 0.5,
  })
  const candidateC = await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_DEP_STEPS.createSuggestedEdge,
    idemLabel: 'dep-edge-candidate-c',
    expectedContextRevision: project.currentContextRevision,
    source: specRef,
    target: templateRef,
    relation: 'candidate_c',
    action: 'NO_ACTION',
    origin: 'ai',
    reason: '候选关系 C：待人工判断是否成立',
    confidence: 0.3,
  })

  const confirmed = await decideDependencyEdge(ctx, client, token, options.projectId, candidateA.id, {
    stepName: G3_DEP_STEPS.decideConfirm,
    idemLabel: 'dep-decide-confirm',
    expectedVersion: candidateA.version,
    decision: 'confirm',
    reason: '人工确认：候选关系 A 成立',
  })
  const modified = await decideDependencyEdge(ctx, client, token, options.projectId, candidateB.id, {
    stepName: G3_DEP_STEPS.decideModify,
    idemLabel: 'dep-decide-modify',
    expectedVersion: candidateB.version,
    decision: 'modify',
    reason: '人工修改：候选关系 B 的关系名改为 candidate_b_corrected',
    relation: 'candidate_b_corrected',
    confidence: 0.8,
  })
  const rejected = await decideDependencyEdge(ctx, client, token, options.projectId, candidateC.id, {
    stepName: G3_DEP_STEPS.decideReject,
    idemLabel: 'dep-decide-reject',
    expectedVersion: candidateC.version,
    decision: 'reject',
    reason: '人工拒绝：候选关系 C 不成立',
  })
  // 拒绝过的候选在同一版本里再投一次：服务端不新建边（返回既有那条）。
  const reproposed = await createDependencyEdge(ctx, client, token, options.projectId, {
    stepName: G3_DEP_STEPS.reproposeRejected,
    idemLabel: 'dep-repropose-rejected',
    expectedContextRevision: project.currentContextRevision,
    source: specRef,
    target: templateRef,
    relation: 'candidate_c',
    action: 'NO_ACTION',
    origin: 'ai',
    reason: '候选关系 C：待人工判断是否成立',
    confidence: 0.3,
  })

  const edges = await listDependencyEdges(ctx, client, token, options.projectId)

  // ——
  // AC-G3-15 的候选三态：**候选与已确认边分表存放**（候选不是边）。
  //
  // 两条硬约束（服务端按对象引用校验，违反会被拒）：
  // - 候选的 source 必须是**变更集 roots 里的 ProjectSpec ref**（影响评估从 roots
  //   出发遍历依赖边，source 不是那个 ref 时 action 不传播）；
  // - 候选的 target 必须是**本项目**的章节版本（别处的对象引用会被 404 拒掉）。
  // ——
  const projectBeforeCandidates = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    '读项目状态（候选批次前）',
  )
  const candidateSourceRef: G3ObjectVersionRef = {
    kind: 'ProjectSpec',
    id: options.projectId,
    version: String(projectBeforeCandidates.specRevision),
  }
  const candidateBatch = await submitDependencyCandidateBatch(ctx, client, token, options.projectId, {
    stepName: G3_DEP_STEPS.submitCandidates,
    idemLabel: 'dep-candidates',
    expectedContextRevision: projectBeforeCandidates.currentContextRevision,
    reason: 'G3 验收台-4：提交一批候选依赖（AI 侧提案，待人工裁决）',
    candidates: [
      {
        source: candidateSourceRef,
        target: questionRef,
        relation: 'candidate_confirm',
        action: 'NO_ACTION',
        evidence: `合成依据片段：研究条件修订 ${projectBeforeCandidates.specRevision} 与研究问题章节的对应关系`,
        reason: '候选关系 A：待人工判断是否成立',
        confidence: 0.6,
      },
      {
        source: candidateSourceRef,
        target: questionRef,
        relation: 'candidate_modify',
        action: 'NO_ACTION',
        evidence: '合成依据片段：研究方法章节引用了被改条件，关系名需要人工修正',
        reason: '候选关系 B：待人工判断是否成立',
        confidence: 0.5,
      },
      {
        source: candidateSourceRef,
        target: questionRef,
        relation: 'candidate_reject',
        action: 'NO_ACTION',
        evidence: '合成依据片段：模型猜测，没有正文依据可核对',
        reason: '候选关系 C：待人工判断是否成立',
        confidence: 0.3,
      },
      {
        source: candidateSourceRef,
        target: questionRef,
        relation: 'candidate_stale',
        action: 'NO_ACTION',
        evidence: '合成依据片段：提出时引用的规格修订会在本轮被替换',
        reason: '候选关系 D：待人工判断是否成立（依据版本随后会变更）',
        confidence: 0.4,
      },
    ],
  })
  const candidateByRelation = (relation: string): G3DependencyCandidateFact | undefined =>
    candidateBatch.candidates.find((item) => item.relation === relation)
  const candidateConfirm = candidateByRelation('candidate_confirm')
  const candidateModify = candidateByRelation('candidate_modify')
  const candidateReject = candidateByRelation('candidate_reject')
  const candidateStale = candidateByRelation('candidate_stale')
  if (!candidateConfirm || !candidateModify || !candidateReject || !candidateStale) {
    throw new Error(
      `候选批次没有读回预期的四条候选（读到 ${candidateBatch.candidates.length} 条：` +
        `${candidateBatch.candidates.map((item) => item.relation).join('、') || '（空）'}）`,
    )
  }

  const edgesBeforeCandidateDecisions = edges.length
  const candidateDecisions: G3CandidateDecisionFact[] = []
  candidateDecisions.push(
    await decideDependencyCandidate(ctx, client, token, options.projectId, candidateConfirm.id, {
      stepName: G3_DEP_STEPS.decideCandidateConfirm,
      idemLabel: 'dep-candidate-confirm',
      expectedVersion: candidateConfirm.version,
      decision: 'confirm',
      reason: '人工确认：候选关系 A 成立',
    }),
  )
  candidateDecisions.push(
    await decideDependencyCandidate(ctx, client, token, options.projectId, candidateModify.id, {
      stepName: G3_DEP_STEPS.decideCandidateModify,
      idemLabel: 'dep-candidate-modify',
      expectedVersion: candidateModify.version,
      decision: 'modify',
      reason: '人工修改：候选关系 B 的关系名改为 candidate_b_corrected',
      relation: 'candidate_b_corrected',
      confidence: 0.8,
    }),
  )
  candidateDecisions.push(
    await decideDependencyCandidate(ctx, client, token, options.projectId, candidateReject.id, {
      stepName: G3_DEP_STEPS.decideCandidateReject,
      idemLabel: 'dep-candidate-reject',
      expectedVersion: candidateReject.version,
      decision: 'reject',
      reason: '人工拒绝：候选关系 C 不成立',
    }),
  )
  // 决策之后再读一次依赖边：确认 + 修改各产生一条边，**拒绝不产生边**。
  const edgesAfterCandidateDecisions = (
    await listDependencyEdges(ctx, client, token, options.projectId, '读依赖边列表（候选决策后）')
  ).length

  // 同一版本里重投被拒的候选：服务端记为抑制，不新建候选。
  const candidateReproposal = await submitDependencyCandidateBatch(ctx, client, token, options.projectId, {
    stepName: G3_DEP_STEPS.reproposeRejectedCandidate,
    idemLabel: 'dep-candidates-repropose',
    expectedContextRevision: projectBeforeCandidates.currentContextRevision,
    reason: 'G3 验收台-4：同一版本里重投被拒候选（服务端应当抑制，不新建）',
    candidates: [
      {
        source: candidateSourceRef,
        target: questionRef,
        relation: 'candidate_reject',
        action: 'NO_ACTION',
        evidence: '合成依据片段：模型猜测，没有正文依据可核对',
        reason: '候选关系 C：待人工判断是否成立',
        confidence: 0.3,
      },
    ],
  })

  // —— AC-G3-14 的实跑那半段：生成变更草稿 ——
  const draft = await createChangeDraft(ctx, client, token, options.projectId, {
    stepName: G3_DEP_STEPS.createDraft,
    idemLabel: 'dep-change-draft',
    expectedContextRevision: project.currentContextRevision,
    fields: {
      research_goal: {
        old_value: project.spec.research_goal ?? '',
        new_value: `${project.spec.research_goal ?? ''}（G3 验收台-4 草稿）`,
      },
    },
    affectedChapterIds: [method.id],
    reason: 'G3 验收台-4：助手草稿生成（分组解释已实现，见缺口清单 #246 与 #295）',
  })

  // —— 变更集 → 评估 → 读影响评估（必须早于应用）——
  const changeSet = await createChangeSet(ctx, client, token, options.projectId, {
    idemLabel: 'dep-change-set',
    expectedContextRevision: project.currentContextRevision,
    fields: {
      research_goal: {
        old_value: project.spec.research_goal ?? '',
        new_value: `${project.spec.research_goal ?? ''}（G3 验收台-4 依赖图）`,
      },
    },
    affectedChapterIds: [method.id],
    reason: 'G3 验收台-4：研究条件变化，验证多上游任务合并与建议边语义',
  })
  await assessChangeSet(ctx, client, token, options.projectId, changeSet.id)
  const assessment = await readImpactAssessment(ctx, client, token, options.projectId, changeSet.id)
  const grouping = groupImpactAssessment(assessment, knownObjects)

  // —— 应用：任务在这里产生 ——
  await applyChangeSet(ctx, client, token, options.projectId, changeSet.id)
  const projectAfterApply = await readProjectState(ctx, client, token, options.projectId)
  const tasks = await listImpactTaskDetails(ctx, client, token, options.projectId)

  // —— 确认时版本复检：候选 D 的依据（规格修订）已经变了，确认必须被拒（409）——
  const staleConfirmation = await decideDependencyCandidate(
    ctx,
    client,
    token,
    options.projectId,
    candidateStale.id,
    {
      stepName: G3_DEP_STEPS.decideStaleCandidate,
      idemLabel: 'dep-candidate-stale',
      expectedVersion: candidateStale.version,
      decision: 'confirm',
      reason: '人工确认：候选关系 D（依据版本已变更，应当被复检拦下）',
      expectedStatus: 409,
      allowFailure: true,
    },
  )

  // —— 重复应用同一个变更集：不得产生第二条任务 ——
  await applyChangeSet(ctx, client, token, options.projectId, changeSet.id, {
    stepName: G3_DEP_STEPS.replayApply,
    idemLabel: 'dep-apply-replay',
  })
  const tasksAfterReplay = await listImpactTaskDetails(ctx, client, token, options.projectId)

  const transitions: G3TaskTransition[] = []
  const byTarget = (kind: string, id: string, action: string): G3ImpactTaskDetailFact | undefined =>
    tasks.find(
      (task) => task.target.kind === kind && task.target.id === id && task.action === action,
    )

  const reviewTask = byTarget('ChapterVersion', method.id, 'REVIEW_REQUIRED')
  const mergedTask = byTarget('ChapterVersion', method.id, 'INVALIDATE_CONFIRMATION')
  const dismissTask = byTarget('TemplateCopy', templateRef.id, 'REGENERATE')

  // —— 硬约束：未指派不能直接完成（服务端回 404，不是 403）——
  let unassignedCompletion = { status: 0, code: '' }
  if (reviewTask) {
    const probe = await actOnImpactTask(ctx, client, token, options.projectId, reviewTask.id, {
      stepName: G3_DEP_STEPS.completeWithoutAssign,
      idemLabel: 'dep-complete-without-assign',
      expectedVersion: reviewTask.version,
      targetRevision: reviewTask.targetRevision,
      action: 'complete',
      reason: 'G3 验收台-4：未指派直接完成（预期被拒）',
      closureEvidence: [methodRef],
      expectedStatus: 404,
    })
    unassignedCompletion = { status: probe.status, code: probe.code }
  }

  // —— 指派 → 开始 → 复写并重新确认 → 以闭环证据完成 ——
  if (reviewTask) {
    const assigned = await actOnImpactTask(ctx, client, token, options.projectId, reviewTask.id, {
      stepName: G3_DEP_STEPS.assignTask,
      idemLabel: 'dep-assign',
      expectedVersion: reviewTask.version,
      targetRevision: reviewTask.targetRevision,
      action: 'assign',
      reason: 'G3 验收台-4：把影响任务指派给当前操作者',
      assigneeId: options.operatorId,
    })
    transitions.push({
      taskId: reviewTask.id,
      action: 'assign',
      severity: assigned.task?.severity ?? reviewTask.severity,
      from: reviewTask.status,
      to: assigned.task?.status ?? reviewTask.status,
      closureEvidence: [],
      note: `指派给当前操作者（assignee_id=${options.operatorId}）`,
      status: assigned.status,
      code: assigned.code,
    })

    const started = await actOnImpactTask(ctx, client, token, options.projectId, reviewTask.id, {
      stepName: G3_DEP_STEPS.startTask,
      idemLabel: 'dep-start',
      expectedVersion: assigned.task?.version ?? reviewTask.version + 1,
      targetRevision: reviewTask.targetRevision,
      action: 'start',
      reason: 'G3 验收台-4：开始处置影响任务',
    })
    transitions.push({
      taskId: reviewTask.id,
      action: 'start',
      severity: started.task?.severity ?? reviewTask.severity,
      from: assigned.task?.status ?? reviewTask.status,
      to: started.task?.status ?? reviewTask.status,
      closureEvidence: [],
      note: '责任人开始处置',
      status: started.status,
      code: started.code,
    })

    // 闭环证据必须对**当前版本**有效：先按新的规格修订复写并重新确认章节。
    const rewritten = await writeChapterVersion(ctx, client, token, options.projectId, method.id, {
      stepName: G3_CS_STEPS.rewriteChapter,
      idemLabel: 'dep-rewrite-method',
      expectedChapterVersionId: reviewTask.target.version,
      expectedSpecRevision: projectAfterApply.specRevision,
      bodyMarkdown: `## ${method.title}\n\n变更后复写的正文（运行 ${ctx.runId}，规格修订 ${projectAfterApply.specRevision}）。`,
    })
    if (!rewritten.currentVersionId) {
      throw new Error('复写章节正文后没有拿到当前版本')
    }
    await confirmChapter(ctx, client, token, options.projectId, method.id, {
      stepName: G3_CS_STEPS.reconfirm,
      idemLabel: 'dep-reconfirm-method',
      expectedChapterVersionId: rewritten.currentVersionId,
      expectedSpecRevision: projectAfterApply.specRevision,
    })
    const closureRef: G3ObjectVersionRef = {
      kind: 'ChapterVersion',
      id: method.id,
      version: rewritten.currentVersionId,
    }
    const completed = await actOnImpactTask(ctx, client, token, options.projectId, reviewTask.id, {
      stepName: G3_DEP_STEPS.completeTask,
      idemLabel: 'dep-complete',
      expectedVersion: started.task?.version ?? reviewTask.version + 2,
      targetRevision: reviewTask.targetRevision,
      action: 'complete',
      reason: 'G3 验收台-4：已按当前规格修订复写并重新确认章节，提交闭环证据',
      closureEvidence: [closureRef],
    })
    transitions.push({
      taskId: reviewTask.id,
      action: 'complete',
      severity: completed.task?.severity ?? reviewTask.severity,
      from: started.task?.status ?? reviewTask.status,
      to: completed.task?.status ?? reviewTask.status,
      closureEvidence: [closureRef],
      note: `以闭环证据完成：${closureRef.kind}/${closureRef.id}@${closureRef.version}`,
      status: completed.status,
      code: completed.code,
    })
  }

  // —— 依据被替换的任务落为失效（STALE：终态但不算闭环）——
  if (mergedTask) {
    const staled = await actOnImpactTask(ctx, client, token, options.projectId, mergedTask.id, {
      stepName: G3_DEP_STEPS.staleTask,
      idemLabel: 'dep-stale',
      expectedVersion: mergedTask.version,
      targetRevision: mergedTask.targetRevision,
      action: 'start',
      reason: 'G3 验收台-4：目标版本已被替换，触发失效判定',
    })
    transitions.push({
      taskId: mergedTask.id,
      action: 'start',
      severity: staled.task?.severity ?? mergedTask.severity,
      from: mergedTask.status,
      to: staled.task?.status ?? mergedTask.status,
      closureEvidence: [],
      note: `失效原因：${staled.task?.staleReason || '（服务端未给出）'}`,
      status: staled.status,
      code: staled.code,
    })
  }

  // —— 非阻断任务「接受本次不处理」——
  if (dismissTask) {
    const assigned = await actOnImpactTask(ctx, client, token, options.projectId, dismissTask.id, {
      stepName: G3_DEP_STEPS.assignTask,
      idemLabel: 'dep-assign-dismiss',
      expectedVersion: dismissTask.version,
      targetRevision: dismissTask.targetRevision,
      action: 'assign',
      reason: 'G3 验收台-4：把非阻断影响任务指派给当前操作者',
      assigneeId: options.operatorId,
    })
    transitions.push({
      taskId: dismissTask.id,
      action: 'assign',
      severity: assigned.task?.severity ?? dismissTask.severity,
      from: dismissTask.status,
      to: assigned.task?.status ?? dismissTask.status,
      closureEvidence: [],
      note: `指派给当前操作者（assignee_id=${options.operatorId}）`,
      status: assigned.status,
      code: assigned.code,
    })
    const dismissed = await actOnImpactTask(ctx, client, token, options.projectId, dismissTask.id, {
      stepName: G3_DEP_STEPS.dismissTask,
      idemLabel: 'dep-dismiss',
      expectedVersion: assigned.task?.version ?? dismissTask.version + 1,
      targetRevision: dismissTask.targetRevision,
      action: 'dismiss',
      reason: 'G3 验收台-4：本次不处理该非阻断影响（已说明理由）',
    })
    transitions.push({
      taskId: dismissTask.id,
      action: 'dismiss',
      severity: dismissed.task?.severity ?? dismissTask.severity,
      from: assigned.task?.status ?? dismissTask.status,
      to: dismissed.task?.status ?? dismissTask.status,
      closureEvidence: [],
      note: dismissed.task?.disposition || '已说明理由，接受本次不处理',
      status: dismissed.status,
      code: dismissed.code,
    })
  }

  const finalTasks = await listImpactTaskDetails(ctx, client, token, options.projectId, '读回影响任务状态')

  // —— 建议边目标章节的版本级确认标记（版本级口径：只有被列入失效范围的章节才会变 false）——
  const suggestedSnapshot = await readChapterSnapshot(ctx, client, token, options.projectId, {
    id: question.id,
    sectionId: question.sectionId,
    title: question.title,
  })
  const suggestedTargetVersionConfirmationValid = suggestedSnapshot.versionConfirmationValid

  const result: G3DependencyChainResult = {
    changeSet,
    draft,
    edges,
    assessment,
    grouping,
    tasks,
    tasksAfterReplay,
    unassignedCompletion,
    transitions,
    decisions: { confirm: confirmed, modify: modified, reject: rejected, reproposed },
    candidates: {
      batch: candidateBatch,
      decisions: candidateDecisions,
      reproposal: candidateReproposal,
      staleConfirmation,
      edgesBeforeDecisions: edgesBeforeCandidateDecisions,
      edgesAfterDecisions: edgesAfterCandidateDecisions,
    },
    suggestedEdge,
    suggestedTargetVersionConfirmationValid,
    knownObjects,
    verdicts: [],
  }

  result.verdicts = [
    verdictAcG305(result),
    verdictAcG313(result),
    verdictAcG314(result),
    verdictAcG315(result),
    verdictTaskClosure(result),
  ]
  // 判定挂到运行上下文上：矩阵规则表按判定 id 取结论，判定不成立就不许写 PASS。
  ctx.verdicts = [...(ctx.verdicts ?? []), ...result.verdicts]

  return result
}

/** AC-G3-05 的判定：多上游任务合并。 */
export function verdictAcG305(result: G3DependencyChainResult): G3Verdict {
  const merged = result.assessment.impacts.filter(
    (impact) => impact.causes.length > 1,
  )
  const logicalTasks = result.tasks.filter((task) => task.causes.length > 1)
  const sameCount = result.tasksAfterReplay.length === result.tasks.length
  const holds = merged.length >= 1 && logicalTasks.length === 1 && sameCount

  const task = logicalTasks[0]
  // 证据要指向**真的产生了那条任务**的那条影响：同一张图里可能还有别的多原因
  // 影响（例如 NO_ACTION 的候选边合流），它们不产生任务，不能拿来当证据。
  const impact =
    merged.find(
      (item) =>
        item.target.kind === task?.target.kind && item.target.id === task?.target.id,
    ) ?? merged[0]
  return {
    id: 'AC-G3-05',
    title: '多上游任务合并',
    holds,
    detail: holds
      ? `同一目标（${impact?.target.kind}/${impact?.target.id}）的同一动作被合并成一条逻辑 ImpactTask，` +
        `causes 保留 ${task?.causes.length ?? 0} 条上游原因（` +
        `${task?.causes.map((cause) => `${cause.source.kind}/${cause.source.id}`).join('、') ?? ''}）；` +
        `重复应用同一变更集后影响任务数量仍为 ${result.tasksAfterReplay.length}，没有产生第二条任务。`
      : `不成立：合并影响条数=${merged.length}，多原因任务条数=${logicalTasks.length}，重复应用后任务数不变=${sameCount}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/dependencies → HTTP 201 · 两条来源边指向同一目标、动作相同`,
      `GET /api/v1/lingdoc/projects/{projectId}/change-sets/${result.changeSet?.id ?? ''}/impact-assessment → HTTP 200 · ` +
        `多原因影响条数=${merged.length}（其中产生任务的 1 条）· 合并影响 causes 数量=${impact?.causes.length ?? 0}`,
      `GET /api/v1/lingdoc/projects/{projectId}/impact-tasks → HTTP 200 · 任务数=${result.tasks.length} · ` +
        `多原因任务=${logicalTasks.length} · 任务 id=${logicalTasks[0]?.id ?? '（无）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/${result.changeSet?.id ?? ''}/apply（重复）→ HTTP 200 · ` +
        `重复应用后任务数=${result.tasksAfterReplay.length}`,
    ],
  }
}

/** AC-G3-13 的判定：建议依赖边只提示。 */
export function verdictAcG313(result: G3DependencyChainResult): G3Verdict {
  const possibleOnTarget = result.assessment.impacts.filter(
    (impact) => impact.certainty !== 'definite',
  )
  const taskOnTarget = result.tasks.filter(
    (task) => task.target.kind === result.suggestedEdge.target.kind && task.target.id === result.suggestedEdge.target.id,
  )
  const stillValid = result.suggestedTargetVersionConfirmationValid === true
  const holds =
    result.suggestedEdge.status === 'suggested' &&
    possibleOnTarget.length > 0 &&
    taskOnTarget.length === 0 &&
    stillValid

  return {
    id: 'AC-G3-13',
    title: '建议依赖边',
    holds,
    detail: holds
      ? `建议边（status=suggested）只产生 ${possibleOnTarget.length} 条「可能受影响」影响，` +
        `没有为它的目标章节产生任何影响任务（任务数=${taskOnTarget.length}）；` +
        `该章节的版本级确认标记仍为 ${result.suggestedTargetVersionConfirmationValid}，` +
        `即建议边没有单独使章节失效。没有未闭环的阻断任务，它就不构成导出阻断项。`
      : `不成立：建议边状态=${result.suggestedEdge.status}，可能影响条数=${possibleOnTarget.length}，` +
        `目标任务数=${taskOnTarget.length}，版本级确认标记=${result.suggestedTargetVersionConfirmationValid}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/dependencies → HTTP 201 · edge_id=${result.suggestedEdge.id} · ` +
        `status=${result.suggestedEdge.status} · origin=${result.suggestedEdge.origin} · ` +
        `target=${result.suggestedEdge.target.kind}/${result.suggestedEdge.target.id}`,
      `GET /api/v1/lingdoc/projects/{projectId}/change-sets/${result.changeSet?.id ?? ''}/impact-assessment → HTTP 200 · ` +
        `possible 影响=${possibleOnTarget.length} · 确定性取值来自服务端 certainty`,
      `GET /api/v1/lingdoc/projects/{projectId}/impact-tasks → HTTP 200 · 针对建议边目标任务的任务数=${taskOnTarget.length}`,
      `GET /api/v1/lingdoc/projects/{projectId}/chapters/{questionId}/versions → HTTP 200 · ` +
        `confirmation_valid=${result.suggestedTargetVersionConfirmationValid}（版本级口径）`,
    ],
  }
}

/** AC-G3-14 的判定（部分）：变更草稿生成实跑；分组解释**已实现**，本链路未覆盖读侧。 */
export function verdictAcG314(result: G3DependencyChainResult): G3Verdict {
  const draftCreated = result.draft.id !== '' && result.draft.status === 'draft'
  const holds = draftCreated
  return {
    id: 'AC-G3-14',
    title: 'G3 影响助手（部分判定）',
    holds,
    detail: holds
      ? `实跑那半段成立：助手草稿已生成（change_set_id=${result.draft.id} · status=${result.draft.status}）。` +
        `**本链路未覆盖那半段**：草稿里的「确定受影响 / 可能受影响」分组解释**已经实现**` +
      `（见缺口清单 #246 与 #295）；本链路只跑到「草稿已生成」，读回那半段由助手链负责，` +
      `（工作台那个分组是影响解析结果，不可互替），` +
        `缺口清单见 #246。`
      : `不成立：草稿未生成（change_set_id=${result.draft.id} · status=${result.draft.status}）`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/drafts → HTTP 201 · change_set_id=${result.draft.id} · status=${result.draft.status}`,
      '草稿的分组解释已实现，缺口清单 #246（AC-G3-14 的「确定受影响 / 可能受影响」解释分组）；' +
      '本链路未覆盖读侧，读回见 #295。',
    ],
  }
}

/**
 * AC-G3-15 的判定：依赖候选的三态决策与三条语义。
 *
 * 判据（`g3Matrix.ts` 的 AC-G3-15 行）：「AI 提出若干候选依赖……用户确认其中
 * 一部分、修改一部分、拒绝一部分。**只有确认的关系进入正式依赖图，拒绝记录
 * 不会在同一版本中反复推荐。**」
 *
 * 四件事都要有事实，缺一件就不成立（没跑成不能写成已跑通）：
 * 1. **三态齐备**：确认 → `confirmed`、修改 → `modified`、拒绝 → `rejected`；
 * 2. **拒绝不产生边**：被拒的候选没有 `edge_id`，且依赖边只多了两条
 *    （确认 + 修改各一条）；
 * 3. **同版本不再重提**：重投被拒的元组没有新建候选，只有抑制记录；
 * 4. **确认时版本复检**：依据版本已变更的候选在确认时被拒（409）。
 */
export function verdictAcG315(result: G3DependencyChainResult): G3Verdict {
  const { confirm, modify, reject, reproposed } = result.decisions
  const candidates = result.candidates
  const candidateConfirm = candidates?.decisions.find((item) => item.decision === 'confirm')
  const candidateModify = candidates?.decisions.find((item) => item.decision === 'modify')
  const candidateReject = candidates?.decisions.find((item) => item.decision === 'reject')

  const threeStates =
    candidateConfirm?.status === 'confirmed' &&
    candidateModify?.status === 'modified' &&
    candidateReject?.status === 'rejected'
  // 拒绝不产生边：候选读回没有边 id，依赖边集合也只多了两条。
  const rejectHasNoEdge =
    candidateReject !== undefined &&
    candidateReject.edgeId === '' &&
    candidates !== undefined &&
    candidates.edgesAfterDecisions - candidates.edgesBeforeDecisions === 2
  // 同版本不再重提：重投没有新建候选，服务端记为抑制。
  const notReproposed =
    candidates !== undefined &&
    candidates.reproposal !== null &&
    candidates.reproposal.candidates.length === 0 &&
    candidates.reproposal.suppressed.length > 0
  // 确认时版本复检：依据版本已变更的候选，确认被 409 拒掉。
  const staleRefused =
    candidates !== undefined &&
    candidates.staleConfirmation !== null &&
    candidates.staleConfirmation.httpStatus === 409
  const holds = threeStates && rejectHasNoEdge && notReproposed && staleRefused

  return {
    id: 'AC-G3-15',
    title: 'AI 批量导入依赖：候选批次的三态决策与三条语义',
    holds,
    detail: holds
      ? `候选批次三态都跑通：确认（status=${candidateConfirm?.status}，产生边 ${candidateConfirm?.edgeId || '（无）'}）、` +
        `修改（status=${candidateModify?.status}，记住被替换元组 ${candidateModify?.supersededRelation || '（无）'}/` +
        `${candidateModify?.supersededAction || '（无）'}）、拒绝（status=${candidateReject?.status}，**没有**产生边）；` +
        `依赖边在三次决策前后从 ${candidates?.edgesBeforeDecisions ?? 0} 条变成 ${candidates?.edgesAfterDecisions ?? 0} 条` +
        `（只有确认与修改各产生一条）；同一版本里重投被拒的元组，服务端记为抑制（新建 ` +
        `${candidates?.reproposal?.candidates.length ?? 0} 条 · 抑制 ${candidates?.reproposal?.suppressed.length ?? 0} 条，` +
        `原因 ${candidates?.reproposal?.suppressed.map((item) => item.reason).join('、') || '（无）'}），没有再推荐；` +
        `依据版本已变更的候选在确认时被复检拦下（HTTP ${candidates?.staleConfirmation?.httpStatus}，` +
        `code=${candidates?.staleConfirmation?.code || '（无）'}），不会被静默确认。` +
        `候选与已确认边**分表**存放：「候选不是边」是物理保证，只有确认 / 修改才产生边。`
      : `不成立：三态齐备=${threeStates}（确认=${candidateConfirm?.status || '（未执行）'}、` +
        `修改=${candidateModify?.status || '（未执行）'}、拒绝=${candidateReject?.status || '（未执行）'}）；` +
        `拒绝未产生边=${rejectHasNoEdge}（依赖边 ${candidates?.edgesBeforeDecisions ?? 0} → ` +
        `${candidates?.edgesAfterDecisions ?? 0} 条）；同版本不再重提=${notReproposed}` +
        `（新建 ${candidates?.reproposal?.candidates.length ?? 0} 条 · 抑制 ` +
        `${candidates?.reproposal?.suppressed.length ?? 0} 条）；版本复检拒绝=${staleRefused}` +
        `（HTTP ${candidates?.staleConfirmation?.httpStatus ?? '（未执行）'}）。` +
        `另：依赖边层面的三态裁决也在本轮跑过（确认=${confirm.status}、修改=${modify.status}、拒绝=${reject.status}，` +
        `同元组重投返回同一条边=${reproposed.id === reject.id}）。`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/dependency-candidates → HTTP 200 · ` +
        `batch_id=${candidates?.batch?.id ?? '（未执行）'} · 候选 ${candidates?.batch?.candidates.length ?? 0} 条 · ` +
        `抑制 ${candidates?.batch?.suppressed.length ?? 0} 条`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependency-candidates/{candidateId}/decision → HTTP ` +
        `${candidateConfirm?.httpStatus ?? '（未执行）'} · decision=confirm · status=${candidateConfirm?.status || '（未执行）'} · ` +
        `edge_id=${candidateConfirm?.edgeId || '（无）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependency-candidates/{candidateId}/decision → HTTP ` +
        `${candidateModify?.httpStatus ?? '（未执行）'} · decision=modify · status=${candidateModify?.status || '（未执行）'} · ` +
        `superseded=${candidateModify?.supersededRelation || '（无）'}/${candidateModify?.supersededAction || '（无）'} · ` +
        `edge_id=${candidateModify?.edgeId || '（无）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependency-candidates/{candidateId}/decision → HTTP ` +
        `${candidateReject?.httpStatus ?? '（未执行）'} · decision=reject · status=${candidateReject?.status || '（未执行）'} · ` +
        `产生边=${candidateReject?.edgeId || '（无）'}（拒绝不产生边）`,
      `GET /api/v1/lingdoc/projects/{projectId}/dependencies（候选决策前后）→ HTTP 200 · ` +
        `边数 ${candidates?.edgesBeforeDecisions ?? 0} → ${candidates?.edgesAfterDecisions ?? 0}`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependency-candidates（重投被拒元组）→ HTTP 200 · ` +
        `新建 ${candidates?.reproposal?.candidates.length ?? 0} 条 · 抑制 ` +
        `${candidates?.reproposal?.suppressed.length ?? 0} 条` +
        `（原因 ${candidates?.reproposal?.suppressed.map((item) => item.reason).join('、') || '（无）'}）`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependency-candidates/{candidateId}/decision（依据版本已变更）→ HTTP ` +
        `${candidates?.staleConfirmation?.httpStatus ?? '（未执行）'} · ` +
        `code=${candidates?.staleConfirmation?.code || '（无）'}`,
    ],
    status: holds ? 'PASS' : 'NOT RUN',
  }
}

/** 任务闭环的判定（本票的交付面，不对应某条 AC 行）。 */
export function verdictTaskClosure(result: G3DependencyChainResult): G3Verdict {
  const statuses = new Set(result.transitions.map((item) => item.to))
  const hasDone = statuses.has('DONE')
  const hasDismissed = statuses.has('DISMISSED')
  const hasStale = statuses.has('STALE')
  const unassignedRejected = result.unassignedCompletion.status === 404
  const holds = hasDone && hasDismissed && hasStale && unassignedRejected
  return {
    id: 'TASK-CLOSURE',
    title: '影响任务闭环与状态迁移',
    holds,
    detail: holds
      ? `未指派直接完成被服务端按 404 拒掉；指派 → 开始 → 以闭环证据完成（DONE）、` +
        `非阻断任务「接受本次不处理」（DISMISSED）、依据版本被替换的任务落为失效（STALE，终态但不算闭环）。`
      : `不成立：未指派完成的状态码=${result.unassignedCompletion.status}，` +
        `DONE=${hasDone}，DISMISSED=${hasDismissed}，STALE=${hasStale}`,
    evidence: result.transitions.map(
      (item) =>
        `POST /api/v1/lingdoc/projects/{projectId}/impact-tasks/${item.taskId}/actions → HTTP ${item.status} · ` +
        `action=${item.action} · ${item.from} → ${item.to}${item.code ? ` · code=${item.code}` : ''}`,
    ),
  }
}

export type { G3ImpactAssessmentFact, G3ImpactTaskFact, G3Verdict }
