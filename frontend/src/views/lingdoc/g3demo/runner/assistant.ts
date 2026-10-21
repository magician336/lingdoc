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
 *
 * ## #320：台面自己造一条**真实助手草稿**
 *
 * 上一段只交了一半：台面能**读回**草稿上的两组说明，但读回的草稿得**别人**先造好。
 * 缺口清单 G3-GAP-04 的解除条件正是这一条：「提供可用的模型凭据跑出一条带两组说明的
 * 助手草稿」。所以本段把**产生**草稿的那条路径也接上：
 *
 * 1. **自然语言提出修改** → `POST /agents/task_assistant/sessions` + `POST .../turns`
 *    （真实模型轮次：服务端 `G2HostAgentModel.Reply`）；
 * 2. **登记变更提案** → `POST /action-proposals`（动作 `create_change_set`，**只登记不执行**）；
 * 3. **人工确认** → `POST /action-proposals/{id}/confirm`（这一步才落草稿，步骤名是
 *    「创建变更草稿」——矩阵 AC-G3-14 的规则表按这个名字认「本轮跑到了没有」）；
 * 4. **读回两组说明** → `GET /change-sets/{id}`（读的是**草稿上的** `affected_explanation`，
 *    服务端从已落库的 turn 重新推导，客户端说了不算）；
 * 5. **进入正式评估** → `POST /change-sets/{id}/assess`（**在人工确认之后**，
 *    这正是 AC-G3-14 的口径：用户确认后才进入正式评估）。
 *
 * **服务端从不变**：`internal/lingdoc/workspace/agent_model.go` 与
 * `internal/lingdoc/workspacecore/g2_assistant_explanation.go`（#315 已放宽模型边界）。
 * 服务端**从不**替助手编造受影响对象；模型没给两组说明时，台面如实记
 * 「助手未给两组说明」，不崩掉也不假装有。
 */

import { errorCodeOf, type G3HttpClient } from './httpClient'
import { num, objectOf, projectPath, refOf, str } from './factHelpers'
import type { G3ProjectState, G3Verdict } from './changeset'
import { makeIdempotencyKey, runStep, type G3RunContext } from './runContext'

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
  /** 建立助手会话（`task_assistant`）。 */
  createSession: '助手-创建会话',
  /** 发送助手轮次（真实模型调用）。 */
  sendTurn: '助手-发送轮次',
  /** 登记变更提案（只登记，不执行）。 */
  createProposal: '助手-登记变更提案',
  /**
   * 人工确认变更提案 → 落一条变更草稿。
   *
   * 名字是「创建变更草稿」而不是「助手-…」，因为**矩阵 AC-G3-14 的规则表**按这个
   * 名字认「本轮跑到了这一行所需步骤没有」
   * （`g3Evidence.ts` 的 `G3_ROW_EVIDENCE_RULES`：requires = ['创建变更草稿', '助手-读回草稿说明']）。
   * 名字对不上，那一行就退回「未被本轮覆盖」——这是刻意的：证据链断在名字上时，宁可不判定。
   */
  createDraft: '创建变更草稿',
  /** 评估草稿：人工确认**之后**才进入正式评估。 */
  assessDraft: '助手-评估草稿',
} as const

/**
 * 助手**不允许**触达的端点（否定性证据的比对基准）。
 *
 * 与 `runner/model.ts` 的同名清单同一口径：助手只能提出候选与草稿，
 * 不能直接应用变更、关闭人工任务或放行导出。
 */
export const G3_ASSISTANT_FORBIDDEN_PATHS = [
  /\/change-sets\/[^/]+\/apply$/,
  /\/exports(\/|$)/,
  /\/releases(\/|$)/,
  /\/impact-tasks\/[^/]+\/actions$/,
  /\/change-sets\/[^/]+\/reject$/,
] as const

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

/** 助手轮次事实（`core.AgentTurn` 里本链用到的字段）。 */
export interface G3AssistantTurnFact {
  id: string
  sessionId: string
  agent: string
  status: string
  failureCode: string
  modelId: string
  runMode: string
  inputTokens: number
  outputTokens: number
  version: number
  reply: string
  /** 模型给出的结构化输出（`change_draft` / `affected_explanation` 在里面）。 */
  suggestions: Record<string, unknown>
}

/** 模型给出的变更草稿候选（`core.AgentChangeDraftSuggestion`）。 */
export interface G3AssistantChangeDraftCandidate {
  expectedContextRevision: number
  reason: string
  /**
   * 字段键 → 拟议新值。
   *
   * **没有旧值**：模型的上下文里没有旧值（服务端刻意不给），旧值由台面按当前研究条件填
   * （`internal/lingdoc/workspacecore/g2_change_draft_action.go` 的
   * `DecodeAgentChangeDraftInput` 会拿旧值与当前规格逐字比对）。
   */
  fields: Record<string, string>
  /** 模型建议的受影响章节（候选；勾选是人的动作）。 */
  affectedChapterIds: string[]
}

/** 助手草稿链的输入。 */
export interface G3AssistantDraftOptions {
  projectId: string
  /** 当前项目状态（期望值从这里取）。 */
  project: G3ProjectState
  /** 自然语言诉求：用户用自然语言提出一次研究条件修改。 */
  userInput: string
  /** 人工选定的受影响章节（模型给候选，勾选是人的动作）。 */
  affectedChapterIds: string[]
  /**
   * 这一轮挂着的章节（工作台的提问场景：用户在某个章节里问助手）。
   *
   * 助手只能引用**上下文给出的**对象：挂着章节，助手才有 `object_version`
   * 依据可引，两组说明才有可指回的对象可写（模型上下文里没有的来源它不能编）。
   */
  chapterId?: string
}

/**
 * 验收台用的自然语言诉求：用自然语言提出一次研究条件修改。
 *
 * 合成材料，不含真实课题信息。它按**真实用户会怎么说**来写：把字段键写明
 * （模型在字段键不明确时会先反问，不给候选）、说清受影响范围、明确只要候选。
 * 助手据此**只提出候选**（`change_draft`），落草稿仍要经人工确认
 * （台面在链里显式确认那一步）。
 */
export const G3_ASSISTANT_DRAFT_USER_INPUT =
  '请把研究条件字段 research_goal 改为「仅使用合成访谈数据比较软件流程的可追踪性」，' +
  '受影响范围是当前章节。只提出候选草稿，不要直接应用，也不要替我确认。'

/** 助手草稿链的结果。 */
export interface G3AssistantDraftRun {
  sessionId: string
  turn: G3AssistantTurnFact | null
  turns: G3AssistantTurnFact[]
  /** 模型给出的变更草稿候选。 */
  candidate: G3AssistantChangeDraftCandidate | null
  proposalId: string
  proposalStatus: string
  /** 提案版本（人工确认时按它做乐观锁）。 */
  proposalVersion: number
  /** 人工确认后落库的草稿 id。 */
  draftId: string
  draftStatus: string
  /** 读回草稿上的两组说明。 */
  draft: G3AssistantDraftFact | null
  /** 正式评估后的草稿状态（`assessed`）。 */
  assessmentStatus: string
  /** 拿不到助手草稿时的原因（模型不可用 / 轮次失败 / 模型没给候选）。 */
  unavailableReason: string
  /** 助手触达过的**不允许**的端点（否定性证据，必须为空）。 */
  forbiddenCalls: string[]
  /** 本轮各步的 HTTP 状态（证据用）。 */
  httpStatuses: Record<string, number>
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

/** 读回一条助手轮次。 */
export function readAssistantTurnFact(data: unknown): G3AssistantTurnFact {
  const raw = objectOf(data)
  return {
    id: str(raw.id),
    sessionId: str(raw.session_id),
    agent: str(raw.agent),
    status: str(raw.status),
    failureCode: str(raw.failure_code),
    modelId: str(raw.model_id),
    runMode: str(raw.run_mode),
    inputTokens: num(raw.input_tokens),
    outputTokens: num(raw.output_tokens),
    version: num(raw.version),
    reply: str(raw.reply),
    suggestions: objectOf(raw.suggestions),
  }
}

/**
 * 从模型的结构化输出里读变更草稿候选；模型没给就是 null（**不编造**）。
 *
 * 服务端在 `agent_model.go` 的 `change_draft` 分支校验过这份候选
 * （字段键非空、拟议值非空、修订号与上下文一致），所以这里只做形状读取。
 */
export function parseAssistantChangeDraftCandidate(
  suggestions: unknown,
): G3AssistantChangeDraftCandidate | null {
  const raw = objectOf(objectOf(suggestions).change_draft)
  if (Object.keys(raw).length === 0) return null
  const fields: Record<string, string> = {}
  for (const [key, value] of Object.entries(objectOf(raw.fields))) {
    if (typeof value === 'string' && value.trim() !== '') fields[key] = value
  }
  const affected = Array.isArray(raw.affected_chapter_ids)
    ? raw.affected_chapter_ids.filter((item): item is string => typeof item === 'string')
    : []
  return {
    expectedContextRevision: num(raw.expected_context_revision),
    reason: str(raw.reason),
    fields,
    affectedChapterIds: affected,
  }
}

/** 这一段步骤里有没有助手不该触达的写端点。 */
function forbiddenAssistantCalls(steps: readonly { method: string; path: string }[]): string[] {
  const hits: string[] = []
  for (const step of steps) {
    if (step.method.toUpperCase() === 'GET') continue
    if (G3_ASSISTANT_FORBIDDEN_PATHS.some((pattern) => pattern.test(step.path))) {
      hits.push(`${step.method} ${step.path}`)
    }
  }
  return hits
}

/**
 * 把链上抛出的错误翻译成一句人读得懂的原因。
 *
 * 只带状态码、错误码与服务端给的消息——**不带**请求体与凭据。
 */
function draftFailureReason(prefix: string, error: unknown): string {
  const failure = error as { status?: number; code?: string }
  const message = error instanceof Error ? error.message : String(error)
  const status = typeof failure.status === 'number' && failure.status > 0 ? failure.status : 0
  const code = failure.code ? `（code=${failure.code}）` : ''
  return `${prefix}${status > 0 ? `（HTTP ${status}）` : ''}${code}：${message}`
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

/**
 * 建立助手会话（`task_assistant`）。
 *
 * 请求体是 `core.G2Expectation`：`expected_version` 恒为 0（新建会话没有前一个版本）。
 */
export async function createAssistantSession(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  project: G3ProjectState,
  idemLabel: string,
): Promise<{ id: string; agent: string; httpStatus: number }> {
  const { response, data } = await runStep(ctx, client, {
    name: G3_ASSISTANT_STEPS.createSession,
    method: 'POST',
    path: projectPath(projectId, '/agents/task_assistant/sessions'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `assistant-session-${idemLabel}`),
    body: {
      expected_project_version: project.projectVersion,
      expected_context_revision: project.currentContextRevision,
      expected_version: 0,
    },
    conclusion: '助手会话已建立（HTTP {status}）',
    detail: (body) => {
      const session = objectOf(objectOf(body).data)
      return { session_id: session.id, agent: session.agent, error_code: errorCodeOf(body) }
    },
  })
  const session = objectOf(data)
  return { id: str(session.id), agent: str(session.agent), httpStatus: response.status }
}

/** 发送助手轮次（**真实模型调用**）。 */
export async function sendAssistantTurn(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  sessionId: string,
  project: G3ProjectState,
  userInput: string,
  chapterId: string = '',
): Promise<{ turn: G3AssistantTurnFact; httpStatus: number }> {
  const { response, data } = await runStep(ctx, client, {
    name: G3_ASSISTANT_STEPS.sendTurn,
    method: 'POST',
    path: projectPath(projectId, `/agent-sessions/${encodeURIComponent(sessionId)}/turns`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `assistant-turn-${sessionId}`),
    body: {
      expected_project_version: project.projectVersion,
      expected_context_revision: project.currentContextRevision,
      expected_version: 0,
      chapter_id: chapterId,
      source_ids: [],
      card_refs: [],
      sensitive_context_confirmed: false,
      user_input: userInput,
    },
    conclusion: '助手轮次已返回（HTTP {status}）',
    detail: (body) => {
      const turn = readAssistantTurnFact(objectOf(body).data)
      return {
        turn_id: turn.id,
        status: turn.status,
        failure_code: turn.failureCode,
        model_id: turn.modelId,
        run_mode: turn.runMode,
        error_code: errorCodeOf(body),
      }
    },
  })
  return { turn: readAssistantTurnFact(data), httpStatus: response.status }
}

/** 读回助手轮次（历史记录）。 */
export async function readAssistantTurns(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  sessionId: string,
): Promise<G3AssistantTurnFact[]> {
  const { data } = await runStep(ctx, client, {
    name: G3_ASSISTANT_STEPS.readTurns,
    method: 'GET',
    path: projectPath(projectId, `/agent-sessions/${encodeURIComponent(sessionId)}/turns`),
    token,
    conclusion: '助手轮次已读回（HTTP {status}）',
    detail: (body) => {
      const list = Array.isArray(objectOf(body).data) ? (objectOf(body).data as unknown[]) : []
      return {
        turn_count: list.length,
        turns: list.map((item) => {
          const turn = readAssistantTurnFact(item)
          return `${turn.id}:${turn.status}:${turn.failureCode || '-'}`
        }),
      }
    },
  })
  const list = Array.isArray(data) ? (data as unknown[]) : []
  return list.map(readAssistantTurnFact)
}

/**
 * 登记变更提案（**只登记，不执行**）。
 *
 * 动作是 `create_change_set`，`input` 是 `core.AgentChangeDraftInput`：
 * 字段值 + 每个字段的出处（`field_metadata` 指回那条助手轮次）。
 * 服务端会拿这份出处**从已落库的 turn 重新推导**两组说明——客户端说了不算。
 */
export async function createAssistantChangeProposal(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  project: G3ProjectState,
  payload: {
    reason: string
    fields: Record<string, { old_value: string; new_value: string }>
    affectedChapterIds: string[]
    fieldMetadata: Record<string, unknown>
  },
): Promise<{ id: string; status: string; version: number; httpStatus: number }> {
  const { response, data } = await runStep(ctx, client, {
    name: G3_ASSISTANT_STEPS.createProposal,
    method: 'POST',
    path: projectPath(projectId, '/action-proposals'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, 'assistant-change-proposal'),
    body: {
      expected_project_version: project.projectVersion,
      expected_context_revision: project.currentContextRevision,
      expected_version: 0,
      action: 'create_change_set',
      target_id: projectId,
      input: {
        expected_context_revision: project.currentContextRevision,
        reason: payload.reason,
        fields: payload.fields,
        affected_chapter_ids: payload.affectedChapterIds,
        field_metadata: payload.fieldMetadata,
      },
      source_ids: [],
      expected_effect: '仅创建待评估的变更草稿，正式版本不变',
    },
    conclusion: '变更提案已登记（HTTP {status}，未执行）',
    detail: (body) => {
      const proposal = objectOf(objectOf(body).data)
      return {
        proposal_id: proposal.id,
        action: proposal.action,
        status: proposal.status,
        version: proposal.version,
        error_code: errorCodeOf(body),
      }
    },
  })
  const proposal = objectOf(data)
  return {
    id: str(proposal.id),
    status: str(proposal.status),
    version: num(proposal.version),
    httpStatus: response.status,
  }
}

/**
 * 人工确认变更提案：这一步才执行动作，动作本身**只创建草稿**。
 *
 * 步骤名是「创建变更草稿」——矩阵 AC-G3-14 的规则表按这个名字认「本轮跑到了没有」。
 */
export async function confirmAssistantChangeProposal(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  project: G3ProjectState,
  proposalId: string,
  expectedVersion: number,
  reason: string,
): Promise<{ id: string; status: string; draftId: string; draftStatus: string; httpStatus: number }> {
  const { response, data } = await runStep(ctx, client, {
    name: G3_ASSISTANT_STEPS.createDraft,
    method: 'POST',
    path: projectPath(projectId, `/action-proposals/${encodeURIComponent(proposalId)}/confirm`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `assistant-proposal-confirm-${proposalId}`),
    body: {
      expected_project_version: project.projectVersion,
      expected_context_revision: project.currentContextRevision,
      expected_version: expectedVersion,
      confirmed: true,
      reason,
    },
    conclusion: '变更提案已人工确认（HTTP {status}）：只创建待评估草稿',
    detail: (body) => {
      const proposal = objectOf(objectOf(body).data)
      const result = objectOf(proposal.result)
      return {
        proposal_id: proposal.id,
        status: proposal.status,
        draft_id: result.id,
        draft_status: result.status,
        error_code: errorCodeOf(body),
      }
    },
  })
  const proposal = objectOf(data)
  const result = objectOf(proposal.result)
  return {
    id: str(proposal.id),
    status: str(proposal.status),
    draftId: str(result.id),
    draftStatus: str(result.status),
    httpStatus: response.status,
  }
}

/**
 * 评估草稿（进入正式评估）。
 *
 * 请求体**严格是 `{}`**：服务端这个端点不读业务字段，多 1 字节即 400
 * （与 `runner/changeset.ts` 的 `assessChangeSet` 同一口径）。
 */
export async function assessAssistantDraft(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
): Promise<{ id: string; status: string; httpStatus: number }> {
  const { response, data } = await runStep(ctx, client, {
    name: G3_ASSISTANT_STEPS.assessDraft,
    method: 'POST',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}/assess`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `assistant-assess-${changeSetId}`),
    body: {},
    conclusion: '草稿已进入正式评估（HTTP {status}）',
    detail: (body) => {
      const change = objectOf(objectOf(body).data)
      return { change_set_id: change.id, status: change.status, error_code: errorCodeOf(body) }
    },
  })
  const change = objectOf(data)
  return { id: str(change.id), status: str(change.status), httpStatus: response.status }
}

/**
 * 跑一遍「自然语言 → 助手草稿 → 读回两组说明 → 人工确认 → 正式评估」。
 *
 * 拿不到助手草稿时**不抛错**：原因写进 `unavailableReason`，
 * 让判定卡能如实说「缺什么」，而不是把整轮实跑带崩。
 */
export async function runAssistantDraftChain(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: G3AssistantDraftOptions,
): Promise<G3AssistantDraftRun> {
  const project = options.project
  const run: G3AssistantDraftRun = {
    sessionId: '',
    turn: null,
    turns: [],
    candidate: null,
    proposalId: '',
    proposalStatus: '',
    proposalVersion: 0,
    draftId: '',
    draftStatus: '',
    draft: null,
    assessmentStatus: '',
    unavailableReason: '',
    forbiddenCalls: [],
    httpStatuses: {},
  }
  const startIndex = ctx.steps.length

  try {
    const session = await createAssistantSession(
      ctx, client, token, options.projectId, project, 'assistant-draft',
    )
    run.sessionId = session.id
    run.httpStatuses[G3_ASSISTANT_STEPS.createSession] = session.httpStatus
  } catch (error) {
    run.unavailableReason = draftFailureReason('助手会话没能建立', error)
    run.forbiddenCalls = forbiddenAssistantCalls(ctx.steps.slice(startIndex))
    return run
  }

  try {
    const sent = await sendAssistantTurn(
      ctx, client, token, options.projectId, run.sessionId, project, options.userInput,
      options.chapterId ?? '',
    )
    run.turn = sent.turn
    run.httpStatuses[G3_ASSISTANT_STEPS.sendTurn] = sent.httpStatus
  } catch (error) {
    run.unavailableReason = draftFailureReason(
      '助手轮次没跑通（多半是模型不可用或缺外部凭据）',
      error,
    )
    run.forbiddenCalls = forbiddenAssistantCalls(ctx.steps.slice(startIndex))
    return run
  }

  run.turns = await readAssistantTurns(ctx, client, token, options.projectId, run.sessionId)

  if (run.turn.status !== 'completed') {
    run.unavailableReason =
      `助手轮次未完成（status=${run.turn.status || '（空）'}，` +
      `failure_code=${run.turn.failureCode || '（空）'}）：${run.turn.reply || '（无回复）'}`
    run.forbiddenCalls = forbiddenAssistantCalls(ctx.steps.slice(startIndex))
    return run
  }

  run.candidate = parseAssistantChangeDraftCandidate(run.turn.suggestions)
  if (!run.candidate) {
    run.unavailableReason =
      '模型这轮没有给出变更草稿候选（只回了自然语言）：' + (run.turn.reply || '（无回复）')
    run.forbiddenCalls = forbiddenAssistantCalls(ctx.steps.slice(startIndex))
    return run
  }
  if (Object.keys(run.candidate.fields).length === 0) {
    run.unavailableReason = '模型给出的变更草稿候选里没有可用的字段键。'
    run.forbiddenCalls = forbiddenAssistantCalls(ctx.steps.slice(startIndex))
    return run
  }

  // 旧值由台面按**当前研究条件**填：模型的上下文里没有旧值，编造旧值会被服务端拒掉。
  const fields: Record<string, { old_value: string; new_value: string }> = {}
  const fieldMetadata: Record<string, unknown> = {}
  for (const [key, newValue] of Object.entries(run.candidate.fields)) {
    const oldValue = project.spec[key]
    if (oldValue === undefined) {
      run.unavailableReason = `模型提出的字段键「${key}」不在当前研究条件里，服务端会拒掉这条提案。`
      run.forbiddenCalls = forbiddenAssistantCalls(ctx.steps.slice(startIndex))
      return run
    }
    fields[key] = { old_value: oldValue, new_value: newValue }
    fieldMetadata[key] = {
      origin: 'ai_assisted_human',
      status: 'pending_confirmation',
      provenance: {
        source_type: 'agent_turn',
        source_id: run.turn.id,
        source_version: String(run.turn.version),
        locator: 'reply',
        purpose: 'project_spec',
      },
    }
  }

  try {
    const proposal = await createAssistantChangeProposal(
      ctx, client, token, options.projectId, project, {
        reason: run.candidate.reason || 'G3 验收台-12：助手提出的研究条件候选',
        fields,
        affectedChapterIds: options.affectedChapterIds,
        fieldMetadata,
      },
    )
    run.proposalId = proposal.id
    run.proposalStatus = proposal.status
    run.proposalVersion = proposal.version
    run.httpStatuses[G3_ASSISTANT_STEPS.createProposal] = proposal.httpStatus
  } catch (error) {
    run.unavailableReason = draftFailureReason('变更提案没能登记', error)
    run.forbiddenCalls = forbiddenAssistantCalls(ctx.steps.slice(startIndex))
    return run
  }

  try {
    const confirmed = await confirmAssistantChangeProposal(
      ctx, client, token, options.projectId, project, run.proposalId, run.proposalVersion,
      'G3 验收台-12：人工确认助手提案，只创建待评估草稿',
    )
    run.proposalStatus = confirmed.status
    run.draftId = confirmed.draftId
    run.draftStatus = confirmed.draftStatus
    run.httpStatuses[G3_ASSISTANT_STEPS.createDraft] = confirmed.httpStatus
  } catch (error) {
    run.unavailableReason = draftFailureReason('人工确认没能落草稿', error)
    run.forbiddenCalls = forbiddenAssistantCalls(ctx.steps.slice(startIndex))
    return run
  }

  if (run.draftId) {
    run.draft = await readAssistantDraft(ctx, client, token, options.projectId, run.draftId)
    try {
      const assessed = await assessAssistantDraft(ctx, client, token, options.projectId, run.draftId)
      run.assessmentStatus = assessed.status
      run.httpStatuses[G3_ASSISTANT_STEPS.assessDraft] = assessed.httpStatus
    } catch (error) {
      run.assessmentStatus = 'failed'
      run.unavailableReason = draftFailureReason('草稿没能进入正式评估', error)
    }
  }

  run.forbiddenCalls = forbiddenAssistantCalls(ctx.steps.slice(startIndex))
  return run
}

/** 读回候选批次。 */
export async function readDependencyCandidateBatches(  ctx: G3RunContext,
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
  /**
   * 本轮**自己造**一条助手草稿（#320）。
   *
   * 给了就跑「自然语言 → 助手轮次 → 变更提案 → 人工确认 → 读回两组说明 → 正式评估」；
   * 不给就只读回 `draftChangeSetId` 指的那条（外部先造好的）草稿。
   */
  draft?: G3AssistantDraftOptions
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
  /** 本轮自己造助手草稿的那段链路事实（没跑就是 null）。 */
  draftRun: G3AssistantDraftRun | null
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
 *
 * 给了 `options.draft` 时，这段链**先自己造一条助手草稿**（自然语言 → 真实模型轮次 →
 * 变更提案 → 人工确认 → 读回两组说明 → 正式评估），再读回它的两组说明。
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

  let draftRun: G3AssistantDraftRun | null = null
  let draft: G3AssistantDraftFact | null = null
  if (options.draft) {
    draftRun = await runAssistantDraftChain(ctx, client, token, options.draft)
    draft = draftRun.draft
  } else if (options.draftChangeSetId) {
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
    draftRun,
    batchesBefore,
    submitted,
    batchesAfter,
    decisions: [],
    resubmission: null,
    staleConfirmation: null,
    verdicts: [],
  }

  result.verdicts = [verdictAcG314(result), verdictCandidateBatchSubmitted(result)]
  ctx.verdicts = [...(ctx.verdicts ?? []), ...result.verdicts]

  return result
}

/** 页面快照。 */
export interface G3AssistantSnapshot {
  verdicts: G3Verdict[]
  draft: G3AssistantDraftFact | null
  draftRun: G3AssistantDraftRun | null
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
    draftRun: result.draftRun,
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
 *   （模型边界放宽的情形，见文件头注释），不得崩掉、不得假装有；
 * - 本轮自己造过助手草稿时，还要看**那条路径本身**跑没跑通：模型轮次完成、
 *   提案经人工确认落了草稿、草稿在人工确认**之后**进入正式评估、
 *   助手没有触达应用 / 任务处置 / 导出端点。路径没跑通就不判成立。
 */
export function verdictAcG314(result: G3AssistantChainResult): G3Verdict {
  const draft = result.draft
  const run = result.draftRun ?? null
  const explanation = draft?.explanation ?? null
  const definite = explanation?.definiteAffected ?? []
  const possible = explanation?.possibleAffected ?? []
  const traceable = [...definite, ...possible].every(hasTraceableBasis)
  const twoGroups = definite.length + possible.length > 0
  /** 两组**存在**但一条都没有：模型给了两组，却没指出任何受影响对象。 */
  const groupsEmpty = explanation !== null && !twoGroups
  // 本轮跑过助手草稿路径时，判定还要看这条路径本身跑没跑通。
  const pathRan = run === null
  /**
   * 本轮是否**依赖真实模型**。
   *
   * 台面自己造草稿时，那条草稿的两组说明是**模型给的**（模型说哪些对象受影响），
   * 所以结论是非确定性的：按仓库口径（`runContext.ts` 的 `G3RunVerdict.modelDependent`）
   * 状态记 `WARNING`、并标成模型依赖，不与确定性结论混读。
   * 只读回一条**已存在**的草稿（本轮没跑模型轮次）时，判的是已落库的事实，照旧记 `PASS`。
   */
  const modelDriven = run !== null
  const pathOk = pathRan || (
    run.unavailableReason === '' &&
    run.draftId !== '' &&
    run.draftId === draft?.changeSetId &&
    run.proposalStatus === 'completed' &&
    run.assessmentStatus !== '' &&
    run.forbiddenCalls.length === 0
  )
  const holds = draft !== null && explanation !== null && twoGroups && traceable && pathOk

  const runEvidence = run === null
    ? []
    : [
      `POST /api/v1/lingdoc/projects/{projectId}/agents/task_assistant/sessions → HTTP ` +
        `${run.httpStatuses[G3_ASSISTANT_STEPS.createSession] ?? '（未执行）'} · 会话 ${run.sessionId || '（无）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/agent-sessions/{sessionId}/turns → HTTP ` +
        `${run.httpStatuses[G3_ASSISTANT_STEPS.sendTurn] ?? '（未执行）'} · 轮次 ${run.turn?.id || '（无）'} · ` +
        `status=${run.turn?.status || '（无）'} · run_mode=${run.turn?.runMode || '（无）'} · ` +
        `模型 ${run.turn?.modelId || '（未登记）'} · 失败码 ${run.turn?.failureCode || '（无）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/action-proposals → HTTP ` +
        `${run.httpStatuses[G3_ASSISTANT_STEPS.createProposal] ?? '（未执行）'} · ` +
        `提案 ${run.proposalId || '（无）'} · status=${run.proposalStatus || '（无）'}（只登记，未执行）`,
      `POST /api/v1/lingdoc/projects/{projectId}/action-proposals/{proposalId}/confirm（人工确认）→ HTTP ` +
        `${run.httpStatuses[G3_ASSISTANT_STEPS.createDraft] ?? '（未执行）'} · ` +
        `草稿 ${run.draftId || '（无）'} · status=${run.draftStatus || '（无）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{changeSetId}/assess（人工确认之后才评估）→ HTTP ` +
        `${run.httpStatuses[G3_ASSISTANT_STEPS.assessDraft] ?? '（未执行）'} · 状态 ${run.assessmentStatus || '（无）'}`,
      `助手未触达不允许的端点（应用 / 任务处置 / 导出 / 放行）：` +
        `${run.forbiddenCalls.length === 0 ? '是（0 次）' : run.forbiddenCalls.join('、')}`,
    ]

  return {    id: 'AC-G3-14',
    title: 'G3 影响助手：草稿随附「确定受影响 / 可能受影响」两组说明',
    holds,
    detail: !draft
      ? run !== null && run.unavailableReason !== ''
        ? `本轮没有拿到助手草稿：${run.unavailableReason}。` +
          `这一行不成立——缺的是一条**真实模型轮次**产出的助手草稿` +
          `（解除条件见缺口清单 #246 的 G3-GAP-04）。`
        : '本轮没有读回助手草稿：没有可读回的草稿 id，这一行不成立。'
      : explanation === null
        ? `草稿 ${draft.changeSetId}（状态 ${draft.status}）存在，但**助手未给两组说明**` +
          `（草稿上读不到 affected_explanation）。这是模型未遵守 system prompt 时的容错情形：` +
          `服务端**不再因为缺说明就丢掉整条回包**，草稿照常写出、说明位缺席并被如实标注` +
          `（internal/lingdoc/workspace/agent_model.go 与 workspacecore/g2_assistant_explanation.go，` +
          `三态 present / missing / not_applicable）。台面如实记「助手未给两组说明」，不崩掉、` +
          `也不假装有。`
        : !pathOk
          ? `草稿 ${draft.changeSetId}（状态 ${draft.status}）上的两组说明齐备` +
            `（「确定受影响」${definite.length} 条、「可能受影响」${possible.length} 条），` +
            `但本轮**助手草稿路径本身没跑通**（${run?.unavailableReason || '提案未完成、评估未执行，或助手触达了不允许的端点'}），` +
            `这一行不成立。`
          : groupsEmpty
            ? `草稿 ${draft.changeSetId}（状态 ${draft.status}）上的**两组都是空的**：` +
              `「确定受影响」0 条、「可能受影响」0 条。` +
              `这与「说明位缺席」（服务端状态 missing）是**两件事**：本轮服务端状态是 present` +
              `（两组键都在，服务端从已落库的 turn 重新推导后随草稿落库），但助手这轮` +
              `**没指出任何受影响对象**（常见原因：这一轮上下文里没有可引用的来源、卡片或依赖边，` +
              `模型如实留空）。两组都空就没有「每条都带对象引用、原因与可追溯依据」可核对，这一行不成立。`
            : holds
              ? `草稿 ${draft.changeSetId}（状态 ${draft.status}）带两组说明：` +
                `「确定受影响」${definite.length} 条、「可能受影响」${possible.length} 条，` +
                `每条都带对象引用、原因与可追溯依据（依据类型取自 ` +
                `${G3_ASSISTANT_BASIS_TYPES.join('、')}）。` +
                `说明是**服务端从已落库的 turn 重新推导**的，随草稿存在，须经人工确认后才进入正式评估。` +
                (pathRan
                  ? ''
                  : `本轮这条草稿是台面自己跑出来的：自然语言 → 真实模型轮次（${run?.turn?.modelId || '（未登记）'}，` +
                    `run_mode=${run?.turn?.runMode || '（无）'}）→ 变更提案 → 人工确认落草稿 → 读回两组说明 → ` +
                    `进入正式评估（状态 ${run?.assessmentStatus || '（无）'}）。`)
              : `草稿 ${draft.changeSetId}（状态 ${draft.status}）的两组说明不完整：` +
                `「确定受影响」${definite.length} 条、「可能受影响」${possible.length} 条，` +
                `每条依据都可追溯=${traceable}。`,
    evidence: [
      `GET /api/v1/lingdoc/projects/{projectId}/change-sets/{changeSetId} → HTTP 200 · ` +
        `草稿状态 ${draft?.status || '（未读回）'} · ` +
        `有 affected_explanation=${draft ? (draft.explanationMissing ? '否' : '是') : '（未读回）'} · ` +
        `两组状态=${draft === null ? '（未读回）' : draft.explanationMissing ? 'missing（助手未给两组说明）' : groupsEmpty ? 'present 但两组都是空的' : 'present'} · ` +
        `确定受影响 ${definite.length} 条 · 可能受影响 ${possible.length} 条`,
      `说明口径：确定受影响 / 可能受影响 是**助手自己的**解读（字段名 affected_explanation），` +
        `与求值器的影响确定性（ImpactAssessment.impacts[].certainty）**不是同一回事**，两边不可互替。`,
      ...runEvidence,
    ],
    status: holds ? (modelDriven ? 'WARNING' : 'PASS') : 'NOT RUN',
    modelDependent: modelDriven ? true : undefined,
  }
}

/**
 * 候选批次三态决策的判定——**交付面编号**，不对应某条 AC 行。
 *
 * 为什么不能占用 `AC-G3-15`：
 * - `AC-G3-15` 这一行由**依赖候选链**（`runner/dependencies.ts`）判定：那条链真的
 *   跑三态裁决（确认 / 修改 / 拒绝）、同版本重投与确认时的版本复检。
 * - 助手链只**提交**候选批次并读回，不做三态裁决，所以这条判定在助手链里
 *   **恒为空转**（holds=false、状态 NOT RUN）。
 * - 两条判定同号时，投影按 id 取**第一条**（`g3Evidence.ts` 的 `evaluateGroups`），
 *   助手链那条会盖住依赖链的真实判定。交付面编号沿用既有先例
 *   （`TASK-CLOSURE`、`DRAFT-CREATED`）。
 *
 * 口径（服务端实现 `internal/lingdoc/workspacecore/impact_dependency_candidate.go`）：
 * - 确认 → 候选状态 `confirmed` 且**产生**一条依赖边；
 * - 修改 → 候选状态 `modified`，并记住被替换的元组；
 * - 拒绝 → 候选状态 `rejected`，**不产生**边；
 * - 同版本不再重提 → 同一版本里重投被拒的元组，服务端记为抑制，不新建候选；
 * - 确认时版本复检 → 依据对象版本已变更的候选，确认被拒（409）。
 */
export function verdictCandidateBatchSubmitted(result: G3AssistantChainResult): G3Verdict {
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
    id: 'CANDIDATE-BATCH-SUBMITTED',
    title: '候选批次提交与三态读回（本链路的交付面，不对应某条 AC 行）',
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
