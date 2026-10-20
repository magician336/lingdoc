/**
 * G3 验收台-11（#295）· 助手两组说明与依赖候选批次的**读回**。
 *
 * ## AC-G3-14：助手的影响说明（两组）
 *
 * 术语（`GLOSSARY.md`）：助手影响说明 = 「确定受影响」+「可能受影响」两组，
 * 每条含对象引用、原因与依据；依据必须能指回一条真实记录（变更集、依赖边、
 * 规则集、影响任务或对象版本）；**它只是助手的解读，且始终随草稿存在，
 * 须经人工确认后才进入正式评估**。
 *
 * 服务端实现：`internal/lingdoc/workspacecore/g2_assistant_explanation.go`。
 * 说明随草稿的 `fields_json` 信封落库，**服务端从已落库的 turn 重新推导**
 * （没有新表 / 新列 / 迁移）。所以台面读回的是**草稿上的** `affected_explanation`，
 * 不是模型回复里的自由文本。
 *
 * **不要与「影响确定性」混为一谈**：求值器算出的 definite / possible 判定在
 * `ImpactAssessment.impacts[].certainty` 上；助手说明在 `affected_explanation` 上，
 * 两边字段名刻意不同，互不可替。
 *
 * ## 已知风险：模型边界收紧
 *
 * `internal/lingdoc/workspace/agent_model.go` 的 `change_draft` 分支收紧了模型边界
 * ——模型给 `change_draft` 必须同时给 `affected_explanation`，否则整条回包被拒。
 * 因此台面读回时**必须容忍缺 `affected_explanation` 的草稿**（记「助手未给两组说明」
 * 而不是崩掉），并把这一点写进链路结论。
 *
 * ## AC-G3-15：依赖候选批次
 *
 * 服务端实现：`internal/lingdoc/workspacecore/impact_dependency_candidate.go`。
 * 候选与已确认边**分表**存放（「候选不是边」是物理保证）。台面读回候选批次：
 * 三态决策（确认 / 修改 / 拒绝）、同版本不再重提、确认时版本复检。
 *
 * AC-G3-14 / AC-G3-15 的**矩阵行状态不由本票改**（实跑后由主会话统一改），
 * 本票只交链路与判定函数。
 */

import { errorCodeOf, type G3HttpClient } from './httpClient'
import { num, objectOf, projectPath, refOf, str } from './factHelpers'
import { readChangeSet } from './changeset'
import { makeIdempotencyKey, runStep, type G3RunContext } from './runContext'
import type { G3Verdict } from './changeset'

/** 步骤名。矩阵规则表按这些名字认「这一行跑到了没有」。 */
export const G3_ASSISTANT_STEPS = {
  readChangeSet: '助手-读回草稿说明',
  readTurns: '助手-读回助手轮次',
  readCandidates: '助手-读回候选批次',
  submitCandidates: '助手-提交候选批次',
  decideConfirm: '助手-候选决策-确认',
  decideModify: '助手-候选决策-修改',
  decideReject: '助手-候选决策-拒绝',
  resubmitRejected: '助手-重投被拒候选',
  confirmStale: '助手-确认版本已变更的候选',
} as const

/** 助手说明里依据可以指回的真实记录类型（`core.AssistantAffectedBasisTypes`）。 */
export const G3_ASSISTANT_BASIS_TYPES = [
  'change_set',
  'dependency_edge',
  'ruleset',
  'impact_task',
  'object_version',
] as const

/** 助手说明里的一条（`core.AssistantAffectedEntry`）。 */
export interface G3AssistantExplanationEntry {
  object: { kind: string; id: string; version: string }
  reason: string
  basis: { sourceType: string; sourceId: string; detail: string }
}

/** 助手的两组说明（`core.AssistantAffectedExplanation`）。 */
export interface G3AssistantExplanation {
  definiteAffected: G3AssistantExplanationEntry[]
  possibleAffected: G3AssistantExplanationEntry[]
}

/** 助手草稿读回的事实。 */
export interface G3AssistantDraftFact {
  changeSetId: string
  status: string
  /** 服务端从已落库 turn 重新推导出的两组说明；没有就是 null。 */
  explanation: G3AssistantExplanation | null
  /** 助手**没有**给两组说明（草稿本身仍存在）。 */
  explanationMissing: boolean
}

/** 依赖候选（`core.DependencyCandidate`）。 */
export interface G3DependencyCandidateFact {
  id: string
  batchId: string
  source: { kind: string; id: string; version: string }
  target: { kind: string; id: string; version: string }
  relation: string
  action: string
  evidence: string
  reason: string
  confidence: number
  status: string
  decision: string
  supersededRelation: string
  supersededAction: string
  version: number
  projectVersion: number
  edgeId: string
}

/** 被抑制的候选（`core.DependencyCandidateSuppression`）。 */
export interface G3DependencySuppressionFact {
  source: { kind: string; id: string; version: string }
  target: { kind: string; id: string; version: string }
  relation: string
  action: string
  reason: string
  previousCandidateId: string
  previousStatus: string
}

/** 候选批次（`core.DependencyCandidateBatch`）。 */
export interface G3DependencyCandidateBatchFact {
  id: string
  status: string
  projectVersion: number
  contextRevision: number
  candidates: G3DependencyCandidateFact[]
  suppressed: G3DependencySuppressionFact[]
}

/** 三态决策的结果。 */
export interface G3CandidateDecisionFact {
  candidateId: string
  decision: string
  status: string
  httpStatus: number
  code: string
  edgeId: string
  relation: string
  action: string
  supersededRelation: string
  supersededAction: string
}

/**
 * 读助手说明里的一条。
 *
 * 依据必须能指回一条真实记录：`basis.source_type` 落在
 * `G3_ASSISTANT_BASIS_TYPES` 之内、且 `source_id` 非空，才算「有依据」。
 */
export function readAssistantEntry(data: unknown): G3AssistantExplanationEntry {
  const raw = objectOf(data)
  const basis = objectOf(raw.basis)
  return {
    object: refOf(raw.object),
    reason: str(raw.reason),
    basis: {
      sourceType: str(basis.source_type),
      sourceId: str(basis.source_id),
      detail: str(basis.detail),
    },
  }
}

/**
 * 读助手的两组说明。缺说明返回 null（**不是**空两组）。
 *
 * 只有**两组都在**时才算有说明：服务端落库的信封要么带完整的两组，要么不带
 * （`affected_explanation` 是可选字段）。只带一半的载荷按「没有说明」处理，
 * 不让页面拿半份说明当完整说明。
 */
export function readAssistantExplanation(data: unknown): G3AssistantExplanation | null {
  const raw = objectOf(data)
  if (!('definite_affected' in raw) || !('possible_affected' in raw)) return null
  const definite = Array.isArray(raw.definite_affected) ? raw.definite_affected : []
  const possible = Array.isArray(raw.possible_affected) ? raw.possible_affected : []
  return {
    definiteAffected: definite.map(readAssistantEntry),
    possibleAffected: possible.map(readAssistantEntry),
  }
}

/** 一条说明是否「有依据」：依据类型在词表内且指回了具体记录。 */
export function hasTraceableBasis(entry: G3AssistantExplanationEntry): boolean {
  const typeOk = (G3_ASSISTANT_BASIS_TYPES as readonly string[]).includes(entry.basis.sourceType)
  return typeOk && entry.basis.sourceId !== '' && entry.reason !== ''
}

/** 把一个依赖候选读成事实。 */
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

/** 把一个抑制记录读成事实。 */
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

/** 把一个候选批次读成事实。 */
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

/** 读回草稿上的助手两组说明。 */
export async function readAssistantDraft(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
  stepName: string = G3_ASSISTANT_STEPS.readChangeSet,
): Promise<G3AssistantDraftFact> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}`),
    token,
    conclusion: '助手草稿已读回（HTTP {status}）',
    detail: (body) => {
      const change = objectOf(objectOf(body).data)
      const explanation = readAssistantExplanation(change.affected_explanation)
      return {
        change_set_id: change.id,
        status: change.status,
        has_explanation: explanation !== null,
        definite_count: explanation?.definiteAffected.length ?? 0,
        possible_count: explanation?.possibleAffected.length ?? 0,
      }
    },
  })
  const raw = objectOf(data)
  const explanation = readAssistantExplanation(raw.affected_explanation)
  return {
    changeSetId: str(raw.id),
    status: str(raw.status),
    explanation,
    // 草稿在但说明缺：这正是「模型没给两组说明」的情形，**不崩掉**。
    explanationMissing: explanation === null,
  }
}

/** 读回候选批次。 */
export async function readDependencyCandidateBatches(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string = G3_ASSISTANT_STEPS.readCandidates,
): Promise<G3DependencyCandidateBatchFact[]> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, '/dependency-candidates'),
    token,
    conclusion: '依赖候选批次已读回（HTTP {status}）',
    detail: (body) => {
      const list = Array.isArray(objectOf(body).data) ? (objectOf(body).data as unknown[]) : []
      return {
        batch_count: list.length,
        candidates: list.reduce<number>(
          (total, item) => {
            const raw = objectOf(item)
            return total + (Array.isArray(raw.candidates) ? raw.candidates.length : 0)
          },
          0,
        ),
        suppressed: list.reduce<number>(
          (total, item) => {
            const raw = objectOf(item)
            return total + (Array.isArray(raw.suppressed) ? raw.suppressed.length : 0)
          },
          0,
        ),
      }
    },
  })
  const list = Array.isArray(data) ? (data as unknown[]) : []
  return list.map(readDependencyCandidateBatchFact)
}

/** 提交一批候选依赖。 */
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
      source: { kind: string; id: string; version: string }
      target: { kind: string; id: string; version: string }
      relation: string
      action: string
      evidence: string
      reason: string
      confidence: number
    }[]
    scope?: {
      objects?: { kind: string; id: string; version: string }[]
      targetKinds?: string[]
      reason?: string
    }
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
      scope: options.scope ?? {},
      candidates: options.candidates,
    },
    conclusion: '候选依赖批次已提交（HTTP {status}）',
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

/** 对一个候选做三态决策（确认 / 修改 / 拒绝）。 */
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

/** 链的输入。 */
export interface G3AssistantChainOptions {
  projectId: string
  /** 要读回说明的助手草稿（变更集 id）。空串表示本轮不读草稿。 */
  draftChangeSetId?: string
  /** 要提交的候选批次；不传则本轮只读回已有批次。 */
  candidates?: {
    source: { kind: string; id: string; version: string }
    target: { kind: string; id: string; version: string }
    relation: string
    action: string
    evidence: string
    reason: string
    confidence: number
  }[]
  /** 提交候选时的上下文修订。 */
  expectedContextRevision?: number
}

/** 链的结果。 */
export interface G3AssistantChainResult {
  draft: G3AssistantDraftFact | null
  batchesBefore: G3DependencyCandidateBatchFact[]
  submitted: G3DependencyCandidateBatchFact | null
  batchesAfter: G3DependencyCandidateBatchFact[]
  decisions: G3CandidateDecisionFact[]
  /** 重投被拒候选的结果（同一版本不应再新建）。 */
  resubmission: G3DependencyCandidateBatchFact | null
  /** 确认一个依据已变更的候选的结果（期望被拒）。 */
  staleConfirmation: G3CandidateDecisionFact | null
  verdicts: G3Verdict[]
}

/**
 * 跑「助手两组说明 + 依赖候选批次」的读回链。
 *
 * 读回优先、写入其次：台面先看**已落库**的事实（草稿上的两组说明、候选批次），
 * 再按需提交一批候选并走三态决策。
 */
export async function runAssistantChain(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: G3AssistantChainOptions,
): Promise<G3AssistantChainResult> {
  const batchesBefore = await readDependencyCandidateBatches(
    ctx,
    client,
    token,
    options.projectId,
  )

  let draft: G3AssistantDraftFact | null = null
  if (options.draftChangeSetId) {
    draft = await readAssistantDraft(
      ctx,
      client,
      token,
      options.projectId,
      options.draftChangeSetId,
    )
  }

  let submitted: G3DependencyCandidateBatchFact | null = null
  if (options.candidates && options.candidates.length > 0) {
    submitted = await submitDependencyCandidateBatch(
      ctx,
      client,
      token,
      options.projectId,
      {
        stepName: G3_ASSISTANT_STEPS.submitCandidates,
        idemLabel: 'assistant-candidates',
        expectedContextRevision: options.expectedContextRevision ?? 0,
        reason: 'G3 验收台-11：提交一批候选依赖（AI 侧提案）',
        candidates: options.candidates,
      },
    )
  }

  const batchesAfter = await readDependencyCandidateBatches(
    ctx,
    client,
    token,
    options.projectId,
    '助手-读回候选批次（决策后）',
  )

  const result: G3AssistantChainResult = {
    draft,
    batchesBefore,
    submitted,
    batchesAfter,
    decisions: [],
    resubmission: null,
    staleConfirmation: null,
    verdicts: [],
  }

  result.verdicts = [verdictAcG314(result), verdictAcG315(result)]
  ctx.verdicts = [...(ctx.verdicts ?? []), ...result.verdicts]

  return result
}

/** 页面快照。 */
export interface G3AssistantSnapshot {
  verdicts: G3Verdict[]
  draft: G3AssistantDraftFact | null
  batchesBefore: G3DependencyCandidateBatchFact[]
  batchesAfter: G3DependencyCandidateBatchFact[]
  submitted: G3DependencyCandidateBatchFact | null
  decisions: G3CandidateDecisionFact[]
  resubmission: G3DependencyCandidateBatchFact | null
  staleConfirmation: G3CandidateDecisionFact | null
}

/** 把链结果压成页面快照。 */
export function toAssistantSnapshot(result: G3AssistantChainResult): G3AssistantSnapshot {
  return {
    verdicts: result.verdicts,
    draft: result.draft,
    batchesBefore: result.batchesBefore,
    batchesAfter: result.batchesAfter,
    submitted: result.submitted,
    decisions: result.decisions,
    resubmission: result.resubmission,
    staleConfirmation: result.staleConfirmation,
  }
}

/**
 * AC-G3-14 的判定：助手草稿里的「确定受影响 / 可能受影响」两组说明。
 *
 * 口径：
 * - 两组都在、每条都有可追溯的依据 → 成立；
 * - 草稿存在但**没有**两组说明 → 不成立，并且结论里必须写明「助手未给两组说明」
 *   （模型边界收紧的情形，见文件头注释），不得崩掉、不得假装有。
 */
export function verdictAcG314(result: G3AssistantChainResult): G3Verdict {
  const draft = result.draft
  const explanation = draft?.explanation ?? null
  const definite = explanation?.definiteAffected ?? []
  const possible = explanation?.possibleAffected ?? []
  const traceable = [...definite, ...possible].every(hasTraceableBasis)
  const twoGroups = definite.length + possible.length > 0
  const holds = draft !== null && explanation !== null && twoGroups && traceable

  return {
    id: 'AC-G3-14',
    title: 'G3 影响助手：草稿随附「确定受影响 / 可能受影响」两组说明',
    holds,
    detail: !draft
      ? '本轮没有读回助手草稿：没有可读回的草稿 id，这一行不成立。'
      : explanation === null
        ? `草稿 ${draft.changeSetId}（状态 ${draft.status}）存在，但**助手未给两组说明**` +
          `（草稿上读不到 affected_explanation）。这正是模型边界收紧后的容错情形：` +
          `模型给 change_draft 必须同时给 affected_explanation，否则整条回包被拒` +
          `（internal/lingdoc/workspace/agent_model.go）。台面如实记「助手未给两组说明」，不崩掉。`
        : holds
          ? `草稿 ${draft.changeSetId}（状态 ${draft.status}）带两组说明：` +
            `「确定受影响」${definite.length} 条、「可能受影响」${possible.length} 条，` +
            `每条都带对象引用、原因与可追溯依据（依据类型取自 ` +
            `${G3_ASSISTANT_BASIS_TYPES.join('、')}）。` +
            `说明是**服务端从已落库的 turn 重新推导**的，随草稿存在，须经人工确认后才进入正式评估。`
          : `草稿 ${draft.changeSetId}（状态 ${draft.status}）的两组说明不完整：` +
            `「确定受影响」${definite.length} 条、「可能受影响」${possible.length} 条，` +
            `每条依据都可追溯=${traceable}。`,
    evidence: [
      `GET /api/v1/lingdoc/projects/{projectId}/change-sets/{changeSetId} → HTTP 200 · ` +
        `草稿状态 ${draft?.status || '（未读回）'} · ` +
        `有 affected_explanation=${draft ? (draft.explanationMissing ? '否' : '是') : '（未读回）'} · ` +
        `确定受影响 ${definite.length} 条 · 可能受影响 ${possible.length} 条`,
      `说明口径：确定受影响 / 可能受影响 是**助手自己的**解读（字段名 affected_explanation），` +
        `与求值器的影响确定性（ImpactAssessment.impacts[].certainty）**不是同一回事**，两边不可互替。`,
    ],
    status: holds ? 'PASS' : 'NOT RUN',
  }
}

/**
 * AC-G3-15 的判定：依赖候选批次的三态决策、同版本不再重提、确认时版本复检。
 *
 * 口径（服务端实现 `internal/lingdoc/workspacecore/impact_dependency_candidate.go`）：
 * - 确认 → 候选状态 `confirmed` 且**产生**一条依赖边；
 * - 修改 → 候选状态 `modified`，并记住被替换的元组；
 * - 拒绝 → 候选状态 `rejected`，**不产生**边；
 * - 同版本不再重提 → 同一版本里重投被拒的元组，服务端记为抑制，不新建候选；
 * - 确认时版本复检 → 依据对象版本已变更的候选，确认被拒（409）。
 */
export function verdictAcG315(result: G3AssistantChainResult): G3Verdict {
  const confirm = result.decisions.find((item) => item.decision === 'confirm')
  const modify = result.decisions.find((item) => item.decision === 'modify')
  const reject = result.decisions.find((item) => item.decision === 'reject')
  const threeStates =
    confirm?.status === 'confirmed' &&
    modify?.status === 'modified' &&
    reject?.status === 'rejected'
  const rejectHasNoEdge = reject !== undefined && reject.edgeId === ''
  const modifyRemembers = modify !== undefined && modify.supersededRelation !== ''
  const notReproposed =
    result.resubmission === null
      ? false
      : result.resubmission.candidates.length === 0 &&
        result.resubmission.suppressed.length > 0
  const staleRefused =
    result.staleConfirmation !== null &&
    result.staleConfirmation.httpStatus >= 400
  const holds =
    threeStates && rejectHasNoEdge && modifyRemembers && notReproposed && staleRefused

  return {
    id: 'AC-G3-15',
    title: 'AI 批量导入依赖：候选批次的三态决策与同版本不再重提',
    holds,
    detail: holds
      ? `候选批次三态都跑通：确认（status=${confirm?.status}，产生边 ${confirm?.edgeId || '（无）'}）、` +
        `修改（status=${modify?.status}，记住被替换元组 ${modify?.supersededRelation || '（无）'}/${modify?.supersededAction || '（无）'}）、` +
        `拒绝（status=${reject?.status}，**没有**产生边）；` +
        `同一版本里重投被拒的元组被服务端抑制（抑制 ${result.resubmission?.suppressed.length ?? 0} 条，新建 ${result.resubmission?.candidates.length ?? 0} 条）；` +
        `依据对象版本已变更的候选在确认时被拒（HTTP ${result.staleConfirmation?.httpStatus}，` +
        `code=${result.staleConfirmation?.code || '（无）'}），不会被静默确认。` +
        `候选与已确认边**分表**存放：「候选不是边」是物理保证，只有确认才产生边。`
      : `不成立：三态齐备=${threeStates}（确认=${confirm?.status || '（无）'}、` +
        `修改=${modify?.status || '（无）'}、拒绝=${reject?.status || '（无）'}）；` +
        `拒绝未产生边=${rejectHasNoEdge}；修改记住被替换元组=${modifyRemembers}；` +
        `同版本不再重提=${notReproposed}；版本复检拒绝=${staleRefused}。`,
    evidence: [
      `GET /api/v1/lingdoc/projects/{projectId}/dependency-candidates → HTTP 200 · ` +
        `批次 ${result.batchesAfter.length} 个 · 候选 ${result.batchesAfter.reduce((total, batch) => total + batch.candidates.length, 0)} 条 · ` +
        `抑制 ${result.batchesAfter.reduce((total, batch) => total + batch.suppressed.length, 0)} 条`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependency-candidates/{candidateId}/decision → HTTP ${confirm?.httpStatus ?? '（未执行）'} · ` +
        `decision=confirm · status=${confirm?.status || '（未执行）'} · edge_id=${confirm?.edgeId || '（无）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependency-candidates/{candidateId}/decision → HTTP ${modify?.httpStatus ?? '（未执行）'} · ` +
        `decision=modify · status=${modify?.status || '（未执行）'} · ` +
        `superseded=${modify?.supersededRelation || '（无）'}/${modify?.supersededAction || '（无）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependency-candidates/{candidateId}/decision → HTTP ${reject?.httpStatus ?? '（未执行）'} · ` +
        `decision=reject · status=${reject?.status || '（未执行）'} · 产生边=${reject?.edgeId || '（无）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependency-candidates（重投被拒元组）→ HTTP 200 · ` +
        `新建 ${result.resubmission?.candidates.length ?? 0} 条 · ` +
        `抑制 ${result.resubmission?.suppressed.length ?? 0} 条` +
        `（原因 ${result.resubmission?.suppressed.map((item) => item.reason).join('、') || '（无）'}）`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependency-candidates/{candidateId}/decision（依据版本已变更）→ HTTP ${result.staleConfirmation?.httpStatus ?? '（未执行）'} · ` +
        `code=${result.staleConfirmation?.code || '（无）'}`,
    ],
    status: holds ? 'PASS' : 'NOT RUN',
  }
}
