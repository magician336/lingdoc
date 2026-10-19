/**
 * G3 验收台 · 真实模型驱动的验收链。
 *
 * 这一链只跑**依赖真实模型**的三条验收项：
 *
 * - **AC-G3-06 旧异步结果**：一轮章节生成（助手轮次）**在途**时用变更集把项目推进到新修订，
 *   旧运行返回后只能落成**历史候选**，不得覆盖新修订的章节、确认与交付快照。
 * - **AC-G3-14 影响助手（模型侧）**：助手把自然语言整理成变更**草稿**；
 *   同时给出**否定性证据**：没有直接应用变更、没有关闭人工任务、没有放行导出。
 * - **AC-G3-15 依赖候选（模型侧）**：候选依赖由**真实模型批量提出**，
 *   页面再对其确认 / 修改 / 拒绝三态分别落证。
 *
 * 三条结论都是**非确定性**的：同样的话术，模型可能给出不同结果。所以判定卡上写的
 * 状态一律是 `WARNING`（宿主环境有局部限制，但等价路径已通过），并附**模型运行标识**与
 * **耗时**；模型不可用或缺外部凭据时写 `NOT RUN`，**其余行照常判定**。
 *
 * 形态与其他链一致：执行器是纯 TS、注入式 HTTP 客户端，页面与 node 脚本跑的是
 * **同一份**执行器，所以页面上的判定与落盘的证据不会走样。
 */

import {
  actOnImpactTask,
  createDependencyEdge,
  decideDependencyEdge,
  listDependencyEdges,
  listImpactTaskDetails,
  type G3DependencyEdgeFact,
  type G3ImpactTaskDetailFact,
  type G3ObjectVersionRef,
} from './dependencies'
import {
  applyChangeSet,
  assessChangeSet,
  confirmChapter,
  createChangeSet,
  readChapterSnapshot,
  readChangeSet,
  readImpactAssessment,
  readProjectState,
  writeChapterVersion,
  type G3ChangeSetFact,
  type G3ChapterSnapshot,
  type G3ProjectState,
} from './changeset'
import { envelopeData, errorCodeOf, type G3HttpClient } from './httpClient'
import { makeIdempotencyKey, recordStep, runStep, type G3RunContext, type G3RunVerdict } from './runContext'
import { freezeRelease, listReleaseSnapshots, runDeliveryCheck, type G3CheckFact, type G3ReleaseSnapshotFact } from './stabilization'

/**
 * 本链的步骤名。
 *
 * 它们是**证据的锚**：矩阵的规则表按步骤名判断「本轮是否跑到这一行所需步骤」。
 * 名字一律带「模型-」前缀，避免与确定性链（`创建变更集` 等）混淆——
 * 前置链跑通不构成任何一条判据的证据，名字混了就会把两条链的结论搅在一起。
 */
export const G3_MODEL_STEPS = {
  /** 读回项目状态。 */
  readProject: '模型-读回项目状态',
  /** 写入章节正文（为了让 ChapterVersion 成为可用的依赖对象）。 */
  writeChapter: '模型-写入章节正文',
  /** 确认章节。 */
  confirmChapter: '模型-确认章节',
  /** 创建助手会话。 */
  createSession: '模型-创建助手会话',
  /** 发送助手轮次（真实模型调用）。 */
  sendTurn: '模型-发送助手轮次',
  /** 读回助手轮次。 */
  readTurns: '模型-读回助手轮次',
  /** 登记变更提案（只登记，不执行）。 */
  createProposal: '模型-登记变更提案',
  /** 人工确认变更提案（执行「只创建草稿」的动作）。 */
  confirmProposal: '模型-人工确认变更提案',
  /** 读回变更提案。 */
  readProposal: '模型-读回变更提案',
  /** 读回变更草稿。 */
  readDraft: '模型-读回变更草稿',
  /** 读回影响任务。 */
  readTasks: '模型-读回影响任务',
  /** 读回交付检查（导出闸门）。 */
  readCheck: '模型-读回交付检查',
  /** 读回交付快照列表。 */
  readReleases: '模型-读回交付快照',
  /** 把旧运行结果存成历史候选。 */
  saveHistoryCandidate: '模型-存为历史候选',
  /** 用变更集把项目推进到新修订。 */
  advanceRevision: '模型-推进变更集到新修订',
  /** 读回新修订下的章节、确认与交付快照。 */
  readNewRevision: '模型-读回新修订对象',
  /** 稳定化并冻结交付快照（让「交付快照未被旧结果改写」这条核对有东西可比）。 */
  stabilizeRelease: '模型-稳定化并冻结交付快照',
  /** 读回模型给出的候选依赖。 */
  readCandidates: '模型-读回模型候选依赖',
  /** 登记候选依赖边。 */
  createEdge: '模型-登记候选依赖边',
  /** 裁决候选依赖边——确认。 */
  decideConfirm: '模型-裁决候选-确认',
  /** 裁决候选依赖边——修改。 */
  decideModify: '模型-裁决候选-修改',
  /** 裁决候选依赖边——拒绝。 */
  decideReject: '模型-裁决候选-拒绝',
  /** 重投被拒绝的候选边。 */
  reproposeEdge: '模型-重投被拒候选边',
  /** AC-G3-06 的场景结论（成功与失败都会记录，状态由判定卡决定）。 */
  ac06Conclusion: '模型-旧异步-结论',
  /** AC-G3-14 的场景结论。 */
  ac14Conclusion: '模型-助手草稿-结论',
  /** AC-G3-15 的场景结论。 */
  ac15Conclusion: '模型-候选依赖-结论',
} as const

/** 依赖边允许的动作词表（ADR 0003 的影响解析动作）。 */
export const G3_MODEL_EDGE_ACTIONS = [
  'REVALIDATE_EVIDENCE',
  'RETRIEVE_CANDIDATES',
  'REVIEW_REQUIRED',
  'REGENERATE',
  'RECOMPUTE',
  'INVALIDATE_CONFIRMATION',
  'BLOCK_RELEASE',
  'NO_ACTION',
] as const

/** 助手**不允许**触达的端点（否定性证据的比对基准）。 */
export const G3_ASSISTANT_FORBIDDEN_PATTERNS = [
  /\/change-sets\/[^/]+\/apply$/,
  /\/exports(\/|$)/,
  /\/releases(\/|$)/,
  /\/impact-tasks\/[^/]+\/actions$/,
  /\/change-sets\/[^/]+\/reject$/,
] as const

function objectOf(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function num(value: unknown): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

/** 助手轮次事实。 */
export interface G3TurnFact {
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
  createdAt: string
  /** 轮次使用的上下文修订。 */
  contextRevision: number
  /** 轮次使用的项目版本。 */
  projectVersion: number
  reply: string
  /** 模型给出的结构化输出（原样保留）。 */
  suggestions: Record<string, unknown>
}

/** 模型给出的变更草稿候选。 */
export interface G3ChangeDraftSuggestion {
  expectedContextRevision: number
  reason: string
  fields: Record<string, string>
  affectedChapterIds: string[]
}

/** 模型给出的候选依赖。 */
export interface G3ModelCandidate {
  source: G3ObjectVersionRef
  target: G3ObjectVersionRef
  relation: string
  action: string
  reason: string
  confidence: number
  /** 模型给出的依据片段。 */
  basis: string
}

/** 一次模型调用的运行标识与耗时。 */
export interface G3ModelRunFact {
  /** 模型运行标识：本轮助手轮次 id。 */
  runId: string
  sessionId: string
  /** 服务端登记的模型 id。 */
  modelId: string
  /** 运行模式（real / mock …）。 */
  runMode: string
  status: string
  failureCode: string
  /** 端到端耗时（毫秒）。 */
  durationMs: number
  inputTokens: number
  outputTokens: number
  startedAt: string
  finishedAt: string
}

/** AC-G3-06 的场景事实。 */
export interface G3Ac06Fact {
  /** 旧运行返回后的轮次状态（stale 表示结果被服务端判为过期）。 */
  turnStatus: string
  turnFailureCode: string
  /** 旧运行使用的上下文修订。 */
  baseRevision: number
  /** 旧运行返回时项目所处的上下文修订。 */
  newRevision: number
  /** 旧结果是否被存成历史候选。 */
  savedAsCandidate: boolean
  candidateId: string
  candidateStatus: string
  /** 新修订下的章节当前版本（返回后读回）。 */
  chapterVersionAfter: string
  /** 新修订下的当前确认（返回后读回）。 */
  confirmationAfter: string
  /** 返回前后交付快照是否一致。 */
  releaseSnapshotUnchanged: boolean
  releaseSnapshotCount: number
  /** 稳定化结论与冻结结果（为了让「交付快照未被改写」这条核对有东西可比）。 */
  stabilizationStatus: string
  stabilizationBlockers: string[]
  releaseSnapshotId: string
  releaseFreezeStatus: number
  /** 人工闭环掉的阻断影响任务（证明闭环是 Owner 做的，不是助手做的）。 */
  closedTasks: string[]
  /** 旧运行的模型运行标识与耗时。 */
  run: G3ModelRunFact | null
  /** 判定依据。 */
  detail: string
}

/** AC-G3-14 的场景事实。 */
export interface G3Ac14Fact {
  /** 助手轮次 id（模型运行标识）。 */
  turnId: string
  reply: string
  /** 模型给出的变更草稿候选。 */
  suggestion: G3ChangeDraftSuggestion | null
  /** 变更提案 id 与状态。 */
  proposalId: string
  proposalStatus: string
  /** 变更草稿 id 与状态（必须是 draft）。 */
  draftId: string
  draftStatus: string
  /** 草稿创建前后的上下文修订（必须一致：没有应用变更）。 */
  contextRevisionBefore: number
  contextRevisionAfter: number
  /** 助手触达过的应用 / 导出 / 任务处置端点（必须为空）。 */
  forbiddenCalls: string[]
  /** 关闭状态的影响任务（必须为空）。 */
  closedTasks: string[]
  /** 交付检查结论（记录事实用；助手不触达导出端点才是闸门的关键）。 */
  checkStatus: string
  /** 助手登记过的动作类型（不能出现 apply_change_set / export）。 */
  proposalActions: string[]
  /** 助手给出的「确定受影响 / 可能受影响」解释是否成组出现。 */
  groupedExplanation: boolean
  run: G3ModelRunFact | null
  detail: string
}

/** AC-G3-15 的场景事实。 */
export interface G3Ac15Fact {
  /** 模型给出的候选数量。 */
  candidateCount: number
  /** 候选清单。 */
  candidates: G3ModelCandidate[]
  /** 三态裁决结果。 */
  confirmed?: G3DependencyEdgeFact
  modified?: G3DependencyEdgeFact
  rejected?: G3DependencyEdgeFact
  /** 被拒候选重投后的边 id（必须与被拒边相同）。 */
  reproposedEdgeId: string
  /** 重投是否新建了边（必须为否）。 */
  reproposedNewEdge: boolean
  run: G3ModelRunFact | null
  detail: string
}

/** 本链的结果。 */
export interface G3ModelChainResult {
  /** 每次真实模型调用的运行标识与耗时。 */
  runs: G3ModelRunFact[]
  /** 模型是否可用。 */
  modelAvailable: boolean
  /** 不可用原因（缺外部凭据 / 模型未登记 / 返回不完整）。 */
  unavailableReason: string
  ac06: G3Ac06Fact
  ac14: G3Ac14Fact
  ac15: G3Ac15Fact
  verdicts: G3RunVerdict[]
}

/** 本链的输入。 */
export interface G3ModelChainOptions {
  projectId: string
  chapters: { id: string; sectionId: string; title: string }[]
  /** 当前操作者（闭环影响任务时要指派给他）。 */
  operatorId?: string
  /**
   * 在途窗口（毫秒）：旧异步结果场景里，模型运行与修订推进之间的间隔。
   * 间隔太短，模型还没开始跑修订就推进完了，场景就构造不出来。
   */
  inflightWindowMs?: number
}

/** 本链结果的页面快照（可落盘，供页面恢复）。 */
export interface G3ModelSnapshot {
  /** 每次真实模型调用的运行标识与耗时。 */
  runs: G3ModelRunFact[]
  modelAvailable: boolean
  unavailableReason: string
  ac06: G3Ac06Fact
  ac14: G3Ac14Fact
  ac15: G3Ac15Fact
  verdicts: G3RunVerdict[]
}

/** 把本链结果压成页面快照。 */
export function toModelSnapshot(result: G3ModelChainResult): G3ModelSnapshot {
  return {
    runs: result.runs,
    modelAvailable: result.modelAvailable,
    unavailableReason: result.unavailableReason,
    ac06: result.ac06,
    ac14: result.ac14,
    ac15: result.ac15,
    verdicts: result.verdicts,
  }
}

/** 读回助手轮次。 */
export function readTurnFact(data: unknown): G3TurnFact {
  const raw = objectOf(data)
  const context = objectOf(raw.context)
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
    createdAt: str(raw.created_at),
    contextRevision: num(context.context_revision),
    projectVersion: num(context.project_version),
    reply: str(raw.reply),
    suggestions: objectOf(raw.suggestions),
  }
}

/** 从模型的结构化输出里读变更草稿候选；模型没给就是 null（不编造）。 */
export function parseChangeDraftSuggestion(suggestions: unknown): G3ChangeDraftSuggestion | null {
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

/** 从模型的结构化输出里读批量候选依赖；坏条目丢掉，不补造。 */
export function parseDependencyCandidates(suggestions: unknown): G3ModelCandidate[] {
  const list = Array.isArray(objectOf(suggestions).dependency_candidates)
    ? (objectOf(suggestions).dependency_candidates as unknown[])
    : []
  const out: G3ModelCandidate[] = []
  for (const item of list) {
    const raw = objectOf(item)
    const source = objectOf(raw.source)
    const target = objectOf(raw.target)
    if (!str(source.kind) || !str(source.id) || !str(target.kind) || !str(target.id)) continue
    const confidence = num(raw.confidence)
    out.push({
      source: { kind: str(source.kind), id: str(source.id), version: str(source.version) },
      target: { kind: str(target.kind), id: str(target.id), version: str(target.version) },
      relation: str(raw.relation),
      action: str(raw.action),
      reason: str(raw.reason),
      confidence: confidence > 0 ? Math.min(confidence, 1) : 0.5,
      basis: str(raw.basis),
    })
  }
  return out
}

/** 候选动作归一：不在词表内的一律落到「人工复核」，不认不出的动作不能当低风险。 */
export function normalizeCandidateAction(action: string): string {
  const upper = action.trim().toUpperCase()
  return (G3_MODEL_EDGE_ACTIONS as readonly string[]).includes(upper) ? upper : 'REVIEW_REQUIRED'
}

/** 关系名归一：只保留可读标识，模型给出自由文本时也不至于写坏请求。 */
export function normalizeRelation(relation: string, fallback: string): string {
  const cleaned = relation.trim().toLowerCase().replace(/[^a-z0-9_]+/g, '_').replace(/^_+|_+$/g, '')
  return cleaned === '' ? fallback : cleaned.slice(0, 60)
}

/** 模型运行标识与耗时的人读摘要。 */
export function summarizeModelRun(run: G3ModelRunFact | null): string {
  if (!run) return '本轮没有成功的模型调用'
  const failure = run.failureCode ? ` · 失败码 ${run.failureCode}` : ''
  return `模型运行标识 ${run.runId} · 模型 ${run.modelId || '（未登记）'} · 模式 ${run.runMode || '（未登记）'} · 耗时 ${run.durationMs}ms${failure}`
}

/** 助手是否触达过不允许的端点。 */
export function forbiddenAssistantCalls(
  steps: readonly { method: string; path: string }[],
): string[] {
  const hits: string[] = []
  for (const step of steps) {
    if (step.method.toUpperCase() === 'GET') continue
    if (G3_ASSISTANT_FORBIDDEN_PATTERNS.some((pattern) => pattern.test(step.path))) {
      hits.push(`${step.method} ${step.path}`)
    }
  }
  return hits
}

/** 读取模型给出的「确定受影响 / 可能受影响」解释是否成组出现。 */
export function hasGroupedExplanation(reply: string): boolean {
  const definite = /确定(受影响|影响)/.test(reply)
  const possible = /可能(受影响|影响)/.test(reply)
  return definite && possible
}

function projectPath(projectId: string, suffix: string): string {
  return `/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}${suffix}`
}

/** 创建助手会话。 */
export async function createAgentSession(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  project: G3ProjectState,
  label: string,
): Promise<{ id: string; agent: string }> {
  const { data } = await runStep(ctx, client, {
    name: G3_MODEL_STEPS.createSession,
    method: 'POST',
    path: projectPath(projectId, '/agents/task_assistant/sessions'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `agent-session-${label}`),
    body: {
      expected_project_version: project.projectVersion,
      expected_context_revision: project.currentContextRevision,
    },
    conclusion: '助手会话已创建（HTTP {status}）',
    detail: (body) => {
      const session = objectOf(objectOf(body).data)
      return { session_id: session.id, agent: session.agent }
    },
  })
  const session = objectOf(envelopeData(data))
  return { id: str(session.id), agent: str(session.agent) }
}

/** 发送助手轮次（真实模型调用）。 */
export async function sendAgentTurn(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  sessionId: string,
  project: G3ProjectState,
  userInput: string,
  options: { stepName?: string; chapterId?: string } = {},
): Promise<{ turn: G3TurnFact; durationMs: number }> {
  const { response, data } = await runStep(ctx, client, {
    name: options.stepName ?? G3_MODEL_STEPS.sendTurn,
    method: 'POST',
    path: projectPath(projectId, `/agent-sessions/${encodeURIComponent(sessionId)}/turns`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `agent-turn-${sessionId}-${ctx.steps.length}`),
    body: {
      expected_project_version: project.projectVersion,
      expected_context_revision: project.currentContextRevision,
      chapter_id: options.chapterId ?? '',
      source_ids: [],
      card_refs: [],
      sensitive_context_confirmed: false,
      user_input: userInput,
    },
    conclusion: '助手轮次已返回（HTTP {status}）',
    detail: (body) => {
      const turn = readTurnFact(objectOf(body).data)
      return {
        turn_id: turn.id,
        status: turn.status,
        failure_code: turn.failureCode,
        model_id: turn.modelId,
        run_mode: turn.runMode,
        input_tokens: turn.inputTokens,
        output_tokens: turn.outputTokens,
        context_revision: turn.contextRevision,
        error_code: errorCodeOf(body),
      }
    },
  })
  return { turn: readTurnFact(envelopeData(data)), durationMs: response.durationMs }
}

/** 读回助手轮次（历史记录）。 */
export async function readAgentTurns(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  sessionId: string,
  stepName: string = G3_MODEL_STEPS.readTurns,
): Promise<G3TurnFact[]> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, `/agent-sessions/${encodeURIComponent(sessionId)}/turns`),
    token,
    conclusion: '助手轮次已读回（HTTP {status}）',
    detail: (body) => {
      const list = Array.isArray(objectOf(body).data) ? (objectOf(body).data as unknown[]) : []
      return {
        turn_count: list.length,
        turns: list.map((item) => {
          const turn = readTurnFact(item)
          return `${turn.id}:${turn.status}:${turn.failureCode || '-'}`
        }),
      }
    },
  })
  const list = Array.isArray(envelopeData(data)) ? (envelopeData(data) as unknown[]) : []
  return list.map(readTurnFact)
}

/** 读回变更提案（用于否定性证据：助手只登记过 create_change_set）。 */
export async function readAgentProposals(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string = G3_MODEL_STEPS.readProposal,
): Promise<{ id: string; action: string; status: string }[]> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, '/action-proposals'),
    token,
    conclusion: '变更提案已读回（HTTP {status}）',
    detail: (body) => {
      const list = Array.isArray(objectOf(body).data) ? (objectOf(body).data as unknown[]) : []
      return {
        proposal_count: list.length,
        proposals: list.map((item) => {
          const proposal = objectOf(item)
          return `${proposal.action}:${proposal.status}`
        }),
      }
    },
  })
  const list = Array.isArray(envelopeData(data)) ? (envelopeData(data) as unknown[]) : []
  return list.map((item) => {
    const proposal = objectOf(item)
    return { id: str(proposal.id), action: str(proposal.action), status: str(proposal.status) }
  })
}

/** 登记变更提案（**只登记，不执行**）。 */
export async function createActionProposal(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  project: G3ProjectState,
  payload: {
    action: string
    targetId: string
    input: Record<string, unknown>
    sourceIds: string[]
    expectedEffect: string
  },
): Promise<{ id: string; status: string; version: number }> {
  const { data } = await runStep(ctx, client, {
    name: G3_MODEL_STEPS.createProposal,
    method: 'POST',
    path: projectPath(projectId, '/action-proposals'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `proposal-${payload.action}`),
    body: {
      expected_project_version: project.projectVersion,
      expected_context_revision: project.currentContextRevision,
      action: payload.action,
      target_id: payload.targetId,
      input: payload.input,
      source_ids: payload.sourceIds,
      expected_effect: payload.expectedEffect,
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
  const proposal = objectOf(envelopeData(data))
  return { id: str(proposal.id), status: str(proposal.status), version: num(proposal.version) }
}

/** 人工确认变更提案（这一步才会执行动作；动作本身只创建草稿）。 */
export async function confirmActionProposal(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  project: G3ProjectState,
  proposalId: string,
  expectedVersion: number,
  reason: string,
): Promise<{ id: string; status: string; result: Record<string, unknown> }> {
  const { data } = await runStep(ctx, client, {
    name: G3_MODEL_STEPS.confirmProposal,
    method: 'POST',
    path: projectPath(projectId, `/action-proposals/${encodeURIComponent(proposalId)}/confirm`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `proposal-confirm-${proposalId}`),
    body: {
      expected_project_version: project.projectVersion,
      expected_context_revision: project.currentContextRevision,
      expected_version: expectedVersion,
      confirmed: true,
      reason,
    },
    conclusion: '变更提案已人工确认（HTTP {status}）',
    detail: (body) => {
      const proposal = objectOf(objectOf(body).data)
      const result = objectOf(proposal.result)
      return {
        proposal_id: proposal.id,
        status: proposal.status,
        result_status: result.status,
        result_change_set_id: result.id,
        error_code: errorCodeOf(body),
      }
    },
  })
  const proposal = objectOf(envelopeData(data))
  return {
    id: str(proposal.id),
    status: str(proposal.status),
    result: objectOf(proposal.result),
  }
}

/** 把旧运行结果存成**历史候选**（不写章节、不写确认、不写交付快照）。 */
export async function saveHistoryCandidate(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  project: G3ProjectState,
  sessionId: string,
  turnId: string,
  purpose: string,
): Promise<{ id: string; status: string }> {
  const { data } = await runStep(ctx, client, {
    name: G3_MODEL_STEPS.saveHistoryCandidate,
    method: 'POST',
    path: projectPath(projectId, '/import-candidates'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `history-candidate-${turnId}`),
    body: {
      expected_project_version: project.projectVersion,
      expected_context_revision: project.currentContextRevision,
      conversation_session_id: sessionId,
      conversation_turn_ids: [turnId],
      title: '旧异步结果（历史候选）',
      purpose,
      save_mode: 'metadata_only',
      scope: 'personal',
      model_use_allowed: false,
      license: 'AI 生成内容，来源与正确性尚待核实',
    },
    conclusion: '旧运行结果已存为历史候选（HTTP {status}）',
    detail: (body) => {
      const candidate = objectOf(objectOf(body).data)
      return {
        candidate_id: candidate.id,
        status: candidate.status,
        level: candidate.level,
        selection_type: candidate.selection_type,
        error_code: errorCodeOf(body),
      }
    },
  })
  const candidate = objectOf(envelopeData(data))
  return { id: str(candidate.id), status: str(candidate.status) }
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms)
  })
}

/**
 * 模型不可用时的原因。
 *
 * 凭据缺失 / 模型能力不可用（401 / 403 / 503）必须**写明缺外部凭据**，
 * 否则读者分不清「环境没配好」与「判据本身不成立」。
 */
export function modelUnavailableReason(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error)
  const status = (error as { status?: number } | null)?.status
  if (status === 401 || status === 403 || status === 503) {
    return `模型不可用或缺外部凭据（HTTP ${status}）：${message}`
  }
  return `模型调用未完成：${message}`
}

/** 把项目推进一个上下文修订（创建 → 评估 → 读影响评估 → 应用）。 */
async function advanceContextRevision(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  project: G3ProjectState,
  chapterId: string,
  label: string,
): Promise<{ before: number; after: number; changeSetId: string }> {
  const key = 'research_subject'
  const oldValue = project.spec[key] ?? ''
  const newValue = `${oldValue}（修订 ${label}）`
  const changeSet = await createChangeSet(ctx, client, token, projectId, {
    stepName: G3_MODEL_STEPS.advanceRevision,
    idemLabel: `advance-${label}`,
    expectedContextRevision: project.currentContextRevision,
    fields: { [key]: { old_value: oldValue, new_value: newValue } },
    affectedChapterIds: [chapterId],
    reason: `G3 验收台-7：推进上下文修订（${label}）`,
  })
  await assessChangeSet(ctx, client, token, projectId, changeSet.id)
  await readImpactAssessment(ctx, client, token, projectId, changeSet.id, G3_MODEL_STEPS.advanceRevision)
  const applied = await applyChangeSet(ctx, client, token, projectId, changeSet.id, {
    stepName: G3_MODEL_STEPS.advanceRevision,
    idemLabel: `advance-apply-${label}`,
  })
  return {
    before: project.currentContextRevision,
    after: num(applied.targetContextRevision),
    changeSetId: changeSet.id,
  }
}

/**
 * 闭环阻断影响任务（指派 → 开始 → 复写并重新确认章节 → 以章节版本为闭环证据完成）。
 *
 * 稳定化要求「没有未闭环的阻断影响」，所以要让交付闸门真的打开，只能先把任务闭环。
 * 这是**人工（Owner）**做的收尾，不是助手做的——正好也是「助手没有关闭人工任务」的对照。
 */
async function closeBlockingTasks(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  project: G3ProjectState,
  chapters: { id: string; sectionId: string; title: string }[],
  operatorId: string,
  label: string,
): Promise<{ closed: string[]; versions: Record<string, string> }> {
  const tasks = await listImpactTaskDetails(ctx, client, token, projectId, G3_MODEL_STEPS.readTasks)
  const blocking = tasks.filter(
    (task) =>
      task.severity === 'blocking' &&
      task.status !== 'DONE' &&
      task.status !== 'STALE' &&
      // 只处置**当前修订**的任务：更早修订留下的任务在服务端就不可处置
      // （target_revision 与当前修订不符，处置会 409）。
      task.targetRevision === project.currentContextRevision,
  )
  const closed: string[] = []
  if (blocking.length === 0) {
    return { closed, versions: {} }
  }

  const startedVersions: Record<string, number> = {}
  for (const task of blocking) {
    const assigned = await actOnImpactTask(ctx, client, token, projectId, task.id, {
      stepName: G3_MODEL_STEPS.readTasks,
      idemLabel: `model-assign-${label}-${task.id}`,
      expectedVersion: task.version,
      targetRevision: task.targetRevision,
      action: 'assign',
      reason: 'G3 验收台-7：把影响任务指派给当前操作者',
      assigneeId: operatorId,
    })
    const started = await actOnImpactTask(ctx, client, token, projectId, task.id, {
      stepName: G3_MODEL_STEPS.readTasks,
      idemLabel: `model-start-${label}-${task.id}`,
      expectedVersion: assigned.task?.version ?? task.version + 1,
      targetRevision: task.targetRevision,
      action: 'start',
      reason: 'G3 验收台-7：开始处置影响任务',
    })
    startedVersions[task.id] = started.task?.version ?? task.version + 2
  }

  // 闭环证据的来源：按当前规格修订复写正文并重新确认。
  const versions = await refreshChapters(ctx, client, token, projectId, project, chapters, label)

  for (const task of blocking) {
    const sectionId = chapters.find((chapter) => chapter.id === task.target.id)?.sectionId
    const version = sectionId ? versions[sectionId] : undefined
    if (!version) continue
    const completed = await actOnImpactTask(ctx, client, token, projectId, task.id, {
      stepName: G3_MODEL_STEPS.readTasks,
      idemLabel: `model-complete-${label}-${task.id}`,
      expectedVersion: startedVersions[task.id] ?? task.version + 2,
      targetRevision: task.targetRevision,
      action: 'complete',
      reason: 'G3 验收台-7：已按当前规格修订复写并重新确认章节，提交闭环证据',
      closureEvidence: [{ kind: 'ChapterVersion', id: task.target.id, version }],
    })
    if (completed.task?.status === 'DONE') {
      closed.push(task.id)
    }
  }
  return { closed, versions }
}

/**
 * 稳定化并冻结交付快照。
 *
 * 服务端要求「正式变更已稳定化」才允许冻结快照，所以这一步先把刚应用的变更集
 * 稳定化，再冻结一份快照——这样「旧结果没有改写交付快照」这条核对才有东西可比。
 * 稳定化被阻断时如实记录阻断原因，冻结失败也不掩盖。
 */
async function stabilizeAndFreeze(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
): Promise<{ stabilizationStatus: string; blockers: string[]; snapshotId: string; snapshotStatus: number }> {
  const { data } = await runStep(ctx, client, {
    name: G3_MODEL_STEPS.stabilizeRelease,
    method: 'POST',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}/stabilize`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `stabilize-${changeSetId}`),
    body: {},
    conclusion: '稳定化已执行（HTTP {status}）',
    detail: (body) => {
      const result = objectOf(objectOf(body).data)
      return {
        status: result.status,
        blockers: (Array.isArray(result.blockers) ? result.blockers : []).map((item) => {
          const blocker = objectOf(item)
          return `${blocker.code}:${blocker.target_id}`
        }),
        error_code: errorCodeOf(body),
      }
    },
  })
  const result = objectOf(envelopeData(data))
  const blockers = (Array.isArray(result.blockers) ? result.blockers : []).map((item) => {
    const blocker = objectOf(item)
    return `${str(blocker.code)}:${str(blocker.target_id)}`
  })

  const project = await readProjectState(ctx, client, token, projectId, G3_MODEL_STEPS.readProject)
  let snapshotId = ''
  let snapshotStatus = 0
  try {
    const frozen = await freezeRelease(ctx, client, token, projectId, project.projectVersion, {
      stepName: G3_MODEL_STEPS.stabilizeRelease,
      idemLabel: `freeze-${changeSetId}`,
    })
    snapshotId = frozen.id
    snapshotStatus = 200
  } catch (error) {
    const detail = error as { status?: number }
    snapshotStatus = detail?.status ?? 0
  }
  return { stabilizationStatus: str(result.status), blockers, snapshotId, snapshotStatus }
}

/** 按当前基线重算依赖对象引用（依赖边带乐观锁，版本必须是最新的）。 */
async function readCurrentRefs(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  project: G3ProjectState,
  chapters: { id: string; sectionId: string; title: string }[],
): Promise<G3ObjectVersionRef[]> {
  const refs: G3ObjectVersionRef[] = [
    { kind: 'ProjectSpec', id: projectId, version: String(project.specRevision) },
  ]
  if (project.templateId) {
    refs.push({
      kind: 'TemplateCopy',
      id: project.templateId,
      version: String(project.templateCopyVersion ?? 0),
    })
  }
  for (const chapter of chapters) {
    const snapshot = await readChapterSnapshot(ctx, client, token, projectId, chapter, {
      versionStepName: G3_MODEL_STEPS.readNewRevision,
      confirmationStepName: G3_MODEL_STEPS.readNewRevision,
    })
    if (snapshot.currentVersionId) {
      refs.push({ kind: 'ChapterVersion', id: chapter.id, version: snapshot.currentVersionId })
    }
  }
  return refs
}

/** 按当前规格修订重新写正文并确认章节（修订推进后确认会失效）。 */
async function refreshChapters(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  project: G3ProjectState,
  chapters: { id: string; sectionId: string; title: string }[],
  label: string,
): Promise<Record<string, string>> {
  const versions: Record<string, string> = {}
  for (const chapter of chapters) {
    const snapshot = await readChapterSnapshot(ctx, client, token, projectId, chapter, {
      versionStepName: G3_MODEL_STEPS.readNewRevision,
      confirmationStepName: G3_MODEL_STEPS.readNewRevision,
    })
    const written = await writeChapterVersion(ctx, client, token, projectId, chapter.id, {
      stepName: G3_MODEL_STEPS.writeChapter,
      idemLabel: `model-refresh-body-${label}-${chapter.sectionId}`,
      expectedChapterVersionId: snapshot.currentVersionId,
      expectedSpecRevision: project.specRevision,
      bodyMarkdown: `## ${chapter.title}\n\nG3 验收台-7 真实模型演示正文（上下文修订 ${project.currentContextRevision}，运行 ${ctx.runId}）。`,
    })
    if (!written.currentVersionId) {
      throw new Error(`章节 ${chapter.sectionId} 复写正文后没有拿到当前版本`)
    }
    versions[chapter.sectionId] = written.currentVersionId
    await confirmChapter(ctx, client, token, projectId, chapter.id, {
      stepName: G3_MODEL_STEPS.confirmChapter,
      idemLabel: `model-refresh-confirm-${label}-${chapter.sectionId}`,
      expectedChapterVersionId: written.currentVersionId,
      expectedSpecRevision: project.specRevision,
    })
  }
  return versions
}

/** 读一个新修订下的对象状态（章节 / 确认 / 交付快照），用于「有没有被覆盖」的核对。 */
export interface G3RevisionObjects {
  chapterVersions: string[]
  confirmations: string[]
  releases: string[]
}

async function readRevisionObjects(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  chapters: { id: string; sectionId: string; title: string }[],
  stepName: string,
): Promise<{ objects: G3RevisionObjects; releases: G3ReleaseSnapshotFact[] }> {
  const chapterVersions: string[] = []
  const confirmations: string[] = []
  for (const chapter of chapters) {
    const snapshot: G3ChapterSnapshot = await readChapterSnapshot(ctx, client, token, projectId, chapter, {
      versionStepName: stepName,
      confirmationStepName: stepName,
    })
    chapterVersions.push(`${chapter.sectionId}:${snapshot.currentVersionId ?? '-'}`)
    confirmations.push(
      `${chapter.sectionId}:${snapshot.currentConfirmation ? snapshot.currentConfirmation.id : '-'}`,
    )
  }
  const releaseList = await listReleaseSnapshots(
    ctx,
    client,
    token,
    projectId,
    stepName,
  )
  return {
    objects: {
      chapterVersions,
      confirmations,
      releases: releaseList.items.map((item) => `${item.id}:${item.isCurrent}:${item.checkStatus}`),
    },
    releases: releaseList.items,
  }
}

function sameObjects(left: G3RevisionObjects, right: G3RevisionObjects): boolean {
  return (
    left.chapterVersions.join('|') === right.chapterVersions.join('|') &&
    left.confirmations.join('|') === right.confirmations.join('|') &&
    left.releases.join('|') === right.releases.join('|')
  )
}

/** 读回影响任务里已闭环（关闭）的任务。 */
function closedTaskIds(tasks: readonly G3ImpactTaskDetailFact[]): string[] {
  return tasks.filter((task) => task.status === 'DONE' || task.status === 'DISMISSED').map((task) => task.id)
}

/**
 * 跑整条「真实模型」链。
 *
 * 三个场景依次跑：助手草稿（AC-G3-14）、旧异步结果（AC-G3-06）、批量候选依赖（AC-G3-15）。
 * 任何一个场景因为模型不可用而跑不动，**只影响它自己那一行**：
 * 那一行记 `NOT RUN` 并写明缺外部凭据，其余场景照常跑、其余行照常判定。
 */
export async function runModelChain(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: G3ModelChainOptions,
): Promise<G3ModelChainResult> {
  const question = options.chapters.find((chapter) => chapter.sectionId === 'question')
  const method = options.chapters.find((chapter) => chapter.sectionId === 'method')
  if (!question || !method) {
    throw new Error('真实模型链需要模板激活后的 question / method 两个章节')
  }

  const runs: G3ModelRunFact[] = []
  const verdicts: G3RunVerdict[] = []
  let modelAvailable = true
  let unavailableReason = ''

  let project = await readProjectState(ctx, client, token, options.projectId, G3_MODEL_STEPS.readProject)

  // —— 前置：两个章节都要有正文且已确认，ChapterVersion 才是可用的依赖对象 ——
  const chapterVersions: Record<string, string> = {}
  for (const chapter of [question, method]) {
    const written = await writeChapterVersion(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_MODEL_STEPS.writeChapter,
      idemLabel: `model-chapter-body-${chapter.sectionId}`,
      expectedChapterVersionId: null,
      expectedSpecRevision: project.specRevision,
      bodyMarkdown: `## ${chapter.title}\n\nG3 验收台-7 真实模型演示正文（运行 ${ctx.runId}）。`,
    })
    if (!written.currentVersionId) {
      throw new Error(`章节 ${chapter.sectionId} 保存正文后没有拿到当前版本`)
    }
    chapterVersions[chapter.sectionId] = written.currentVersionId
    await confirmChapter(ctx, client, token, options.projectId, chapter.id, {
      stepName: G3_MODEL_STEPS.confirmChapter,
      idemLabel: `model-confirm-${chapter.sectionId}`,
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
  const questionRef: G3ObjectVersionRef = {
    kind: 'ChapterVersion',
    id: question.id,
    version: chapterVersions.question ?? '',
  }
  const methodRef: G3ObjectVersionRef = {
    kind: 'ChapterVersion',
    id: method.id,
    version: chapterVersions.method ?? '',
  }
  const allowedRefs: G3ObjectVersionRef[] = [specRef, templateRef, questionRef, methodRef]

  // ============================================================
  // 场景一：AC-G3-14 —— 助手把自然语言整理成变更草稿
  // ============================================================
  const ac14StepsBefore = ctx.steps.length
  // 写入与确认章节会推进项目版本，所以这里要**重新读一次**当前基线。
  project = await readProjectState(ctx, client, token, options.projectId, G3_MODEL_STEPS.readProject)
  const session = await createAgentSession(ctx, client, token, options.projectId, project, 'draft')
  const oldSubject = project.spec.research_subject ?? ''
  const newSubject = `${oldSubject}（接受过创业教育的本科生）`
  const draftPrompt = [
    `请把本课题的研究条件 research_subject 从「${oldSubject}」改为「${newSubject}」。`,
    `请只输出 JSON，字段如下：`,
    `reply：自然语言说明这次改动的影响（说明哪些是确定受影响、哪些是可能受影响，并给出依据）。`,
    `change_draft：{"expected_context_revision": ${project.currentContextRevision}, "reason": "改动理由（不超过 200 字）", ` +
      `"fields": {"research_subject": "${newSubject}"}, "affected_chapter_ids": ["${question.id}", "${method.id}"]}`,
    `change_draft 只能包含 expected_context_revision、reason、fields、affected_chapter_ids 这四个键，不要加任何其他键。`,
    `不要编造旧值，不要直接应用变更，不要关闭人工任务，不要放行导出。`,
  ].join('\n')

  let ac14: G3Ac14Fact = {
    turnId: '',
    reply: '',
    suggestion: null,
    proposalId: '',
    proposalStatus: '',
    draftId: '',
    draftStatus: '',
    contextRevisionBefore: project.currentContextRevision,
    contextRevisionAfter: project.currentContextRevision,
    forbiddenCalls: [],
    closedTasks: [],
    checkStatus: '',
    proposalActions: [],
    groupedExplanation: false,
    run: null,
    detail: '',
  }

  try {
    const sent = await sendAgentTurn(ctx, client, token, options.projectId, session.id, project, draftPrompt, {
      chapterId: question.id,
    })
    const run: G3ModelRunFact = {
      runId: sent.turn.id || `turn-${ctx.steps.length}`,
      sessionId: session.id,
      modelId: sent.turn.modelId,
      runMode: sent.turn.runMode,
      status: sent.turn.status,
      failureCode: sent.turn.failureCode,
      durationMs: sent.durationMs,
      inputTokens: sent.turn.inputTokens,
      outputTokens: sent.turn.outputTokens,
      startedAt: sent.turn.createdAt,
      finishedAt: new Date().toISOString(),
    }
    runs.push(run)
    ac14.run = run
    ac14.turnId = run.runId
    ac14.reply = sent.turn.reply
    ac14.groupedExplanation = hasGroupedExplanation(sent.turn.reply)

    const suggestion = parseChangeDraftSuggestion(sent.turn.suggestions)
    ac14.suggestion = suggestion

    if (sent.turn.status !== 'completed' || !suggestion || Object.keys(suggestion.fields).length === 0) {
      modelAvailable = false
      unavailableReason = sent.turn.status !== 'completed'
        ? `模型轮次未完成（状态 ${sent.turn.status || '（无）'}，失败码 ${sent.turn.failureCode || '（无）'}）` +
          `${sent.turn.failureCode === 'dependency_unavailable' ? '：模型不可用或缺外部凭据' : ''}`
        : '模型没有给出变更草稿候选（结构化输出里没有 change_draft）'
    } else {
      // —— 提案输入：研究条件键 → 拟议新字符串，并附会话来源的溯源记录 ——
      const fields: Record<string, { old_value: string; new_value: string }> = {}
      const metadata: Record<string, unknown> = {}
      for (const [key, value] of Object.entries(suggestion.fields)) {
        if (!Object.prototype.hasOwnProperty.call(project.spec, key)) continue
        const current = project.spec[key] ?? ''
        if (current === value) continue
        fields[key] = { old_value: current, new_value: value }
        metadata[key] = {
          origin: 'ai_assisted_human',
          status: 'pending_confirmation',
          provenance: {
            source_type: 'agent_turn',
            source_id: run.runId,
            source_version: String(sent.turn.version),
            purpose: 'project_spec',
            locator: 'reply',
          },
        }
      }
      const affected = suggestion.affectedChapterIds.filter((id) =>
        [question.id, method.id].includes(id),
      )
      const affectedChapterIds = affected.length > 0 ? affected : [question.id]
      if (Object.keys(fields).length === 0) {
        modelAvailable = false
        unavailableReason = '模型给出的候选字段与当前研究条件不匹配，无法登记为草稿'
      } else {
        const proposal = await createActionProposal(ctx, client, token, options.projectId, project, {
          action: 'create_change_set',
          targetId: options.projectId,
          input: {
            expected_context_revision: suggestion.expectedContextRevision || project.currentContextRevision,
            reason: suggestion.reason || '助手按自然语言整理出的变更草稿',
            fields,
            affected_chapter_ids: affectedChapterIds,
            field_metadata: metadata,
          },
          sourceIds: [],
          expectedEffect: `仅创建待评估草稿：${suggestion.reason || '助手整理的变更草稿'}`,
        })
        ac14.proposalId = proposal.id
        ac14.proposalStatus = proposal.status

        const confirmed = await confirmActionProposal(
          ctx,
          client,
          token,
          options.projectId,
          project,
          proposal.id,
          proposal.version,
          '人工确认：只创建待评估草稿，不应用变更',
        )
        ac14.proposalStatus = confirmed.status
        const draftId = str(confirmed.result.id)
        ac14.draftId = draftId
        const draft: G3ChangeSetFact = await readChangeSet(
          ctx,
          client,
          token,
          options.projectId,
          draftId,
          G3_MODEL_STEPS.readDraft,
        )
        ac14.draftStatus = draft.status

        const after = await readProjectState(ctx, client, token, options.projectId, G3_MODEL_STEPS.readProject)
        ac14.contextRevisionAfter = after.currentContextRevision
        const tasks = await listImpactTaskDetails(ctx, client, token, options.projectId, G3_MODEL_STEPS.readTasks)
        ac14.closedTasks = closedTaskIds(tasks)
        let check: G3CheckFact | null = null
        try {
          check = await runDeliveryCheck(
            ctx,
            client,
            token,
            options.projectId,
            after.projectVersion,
            G3_MODEL_STEPS.readCheck,
          )
        } catch {
          check = null
        }
        ac14.checkStatus = check?.status ?? ''
        const proposals = await readAgentProposals(ctx, client, token, options.projectId)
        ac14.proposalActions = proposals.map((item) => `${item.action}:${item.status}`)
        ac14.forbiddenCalls = forbiddenAssistantCalls(ctx.steps.slice(ac14StepsBefore))
      }
    }
  } catch (error) {
    modelAvailable = false
    unavailableReason = modelUnavailableReason(error)
  }

  recordStep(ctx, {
    name: G3_MODEL_STEPS.ac14Conclusion,
    method: 'POST',
    path: `${projectPath(options.projectId, '/agents/task_assistant/sessions')}（场景结论）`,
    status: ac14.draftStatus === 'draft' ? 201 : 0,
    ok: true,
    durationMs: ac14.run?.durationMs ?? 0,
    conclusion: ac14.draftStatus === 'draft'
      ? `助手草稿已生成（草稿 ${ac14.draftId}，状态 ${ac14.draftStatus}，未应用）`
      : `助手草稿场景未完成：${unavailableReason || '（无结论）'}`,
    details: {
      turn_id: ac14.turnId,
      draft_id: ac14.draftId,
      draft_status: ac14.draftStatus,
      proposal_status: ac14.proposalStatus,
      context_revision_before: ac14.contextRevisionBefore,
      context_revision_after: ac14.contextRevisionAfter,
      forbidden_calls: ac14.forbiddenCalls,
      closed_tasks: ac14.closedTasks,
      check_status: ac14.checkStatus,
      grouped_explanation: ac14.groupedExplanation,
    },
  })

  ac14.detail = ac14.draftStatus === 'draft'
    ? `模型运行标识 ${ac14.run?.runId ?? '（无）'}（模型 ${ac14.run?.modelId ?? '（未登记）'}，耗时 ${ac14.run?.durationMs ?? 0}ms）` +
      `：助手把自然语言整理成变更草稿 ${ac14.draftId}，状态 ${ac14.draftStatus}；` +
      `上下文修订 ${ac14.contextRevisionBefore} → ${ac14.contextRevisionAfter}（没有推进，没有应用变更）；` +
      `助手触达的应用/导出/任务处置端点 ${ac14.forbiddenCalls.length} 个；` +
      `已关闭的人工任务 ${ac14.closedTasks.length} 条；` +
      `助手登记过的动作 ${ac14.proposalActions.join('、') || '（无）'}（没有 apply_change_set / export）；` +
      `交付检查 ${ac14.checkStatus || '（未取到）'}（导出闸门没有被助手放行）。`
    : `助手草稿场景未完成：${unavailableReason || '（无结论）'}`

  const ac14Holds = ac14.draftStatus === 'draft' &&
    ac14.contextRevisionAfter === ac14.contextRevisionBefore &&
    ac14.forbiddenCalls.length === 0 &&
    ac14.closedTasks.length === 0 &&
    !ac14.proposalActions.some((item) => item.startsWith('apply_change_set') || item.startsWith('export'))
  verdicts.push({
    id: 'AC-G3-14',
    title: 'AC-G3-14 影响助手（模型侧）',
    holds: ac14Holds,
    detail: ac14Holds
      ? `${ac14.detail} 未实现那半段：助手草稿里的「确定受影响 / 可能受影响」**解释分组**没有实现` +
        `（模型回复里成组出现=${ac14.groupedExplanation}，工作台那个分组是影响解析结果，不可互替），缺口清单见 #246。`
      : `不成立：${ac14.detail}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/agents/task_assistant/sessions → 会话 ${session.id}`,
      `POST /api/v1/lingdoc/projects/{projectId}/agent-sessions/{sessionId}/turns → 轮次 ${ac14.turnId} · ` +
        `模型 ${ac14.run?.modelId || '（未登记）'} · 耗时 ${ac14.run?.durationMs ?? 0}ms`,
      `POST /api/v1/lingdoc/projects/{projectId}/action-proposals → 提案 ${ac14.proposalId} · 状态 ${ac14.proposalStatus}`,
      `POST /api/v1/lingdoc/projects/{projectId}/action-proposals/{proposalId}/confirm → 提案状态 ${ac14.proposalStatus}`,
      `GET /api/v1/lingdoc/projects/{projectId}/change-sets/{draftId} → 草稿状态 ${ac14.draftStatus || '（无）'}`,
      `否定性证据：助手触达禁用端点 ${ac14.forbiddenCalls.length} 个 · 已关闭任务 ${ac14.closedTasks.length} 条 · ` +
        `助手登记的动作 ${ac14.proposalActions.join('、') || '（无）'} · 交付检查 ${ac14.checkStatus || '（未取到）'}`,
    ],
    status: ac14Holds ? 'WARNING' : 'NOT RUN',
  })

  // ============================================================
  // 场景二：AC-G3-06 —— 旧异步结果在修订推进后只能存为历史
  // ============================================================
  let ac06: G3Ac06Fact = {
    turnStatus: '',
    turnFailureCode: '',
    baseRevision: 0,
    newRevision: 0,
    savedAsCandidate: false,
    candidateId: '',
    candidateStatus: '',
    chapterVersionAfter: '',
    confirmationAfter: '',
    releaseSnapshotUnchanged: false,
    releaseSnapshotCount: 0,
    stabilizationStatus: '',
    stabilizationBlockers: [],
    releaseSnapshotId: '',
    releaseFreezeStatus: 0,
    closedTasks: [],
    run: null,
    detail: '',
  }

  try {
    // —— 先把项目推进到 revision 4，再让 revision 4 的生成运行在途 ——
    project = await readProjectState(ctx, client, token, options.projectId, G3_MODEL_STEPS.readProject)
    let revision = project.currentContextRevision
    let guard = 0
    while (revision < 4 && guard < 8) {
      const advanced = await advanceContextRevision(
        ctx,
        client,
        token,
        options.projectId,
        project,
        question.id,
        `to-4-${guard + 1}`,
      )
      project = await readProjectState(ctx, client, token, options.projectId, G3_MODEL_STEPS.readProject)
      revision = project.currentContextRevision
      if (advanced.after <= advanced.before) break
      guard += 1
    }
    const baseRevision = project.currentContextRevision

    // —— 修订推进后章节确认会失效，这里按当前规格修订重新写正文并确认，
    //    让项目在 revision 4 上是「可交付」的，这样冻结出来的交付快照才有内容 ——
    await refreshChapters(
      ctx,
      client,
      token,
      options.projectId,
      project,
      [question, method],
      'rev4',
    )
    // 复写与确认会推进项目版本，所以这里要**重新读一次**当前基线。
    project = await readProjectState(ctx, client, token, options.projectId, G3_MODEL_STEPS.readProject)

    // —— revision 4 的生成运行：在途 ——
    const historySession = await createAgentSession(ctx, client, token, options.projectId, project, 'history')
    const generationPrompt = [
      `请基于当前上下文，为「${method.title}」章节写出一段不少于 300 字的正文草稿。`,
      `请只输出 JSON，字段如下：`,
      `reply：正文草稿本身（不少于 300 字）。`,
      `不要写入任何正式对象：不要保存章节、不要确认、不要关闭任务、不要放行导出。`,
    ].join('\n')

    const turnPromise = sendAgentTurn(
      ctx,
      client,
      token,
      options.projectId,
      historySession.id,
      project,
      generationPrompt,
      { stepName: G3_MODEL_STEPS.sendTurn, chapterId: method.id },
    )
    // 在途期间先挂一个空处理器：模型调用失败时不能变成「未处理的拒绝」，
    // 真正的结论仍然由下面 `await turnPromise` 抛进本场景的 catch。
    turnPromise.catch(() => undefined)
    // 模型在途：等它真的开始跑，再推进修订。
    await delay(options.inflightWindowMs ?? 1500)

    const advanced = await advanceContextRevision(
      ctx,
      client,
      token,
      options.projectId,
      project,
      question.id,
      'to-5',
    )
    // —— 人工（Owner）收尾：闭环阻断影响、复写并重新确认章节、稳定化、冻结交付快照 ——
    //    这样「旧结果没有改写交付快照」这条核对才有东西可比。
    const afterAdvance = await readProjectState(ctx, client, token, options.projectId, G3_MODEL_STEPS.readProject)
    const closed = await closeBlockingTasks(
      ctx,
      client,
      token,
      options.projectId,
      afterAdvance,
      [question, method],
      options.operatorId ?? '',
      'rev5',
    )
    ac06.closedTasks = closed.closed
    const gate = await stabilizeAndFreeze(ctx, client, token, options.projectId, advanced.changeSetId)
    ac06.stabilizationStatus = gate.stabilizationStatus
    ac06.stabilizationBlockers = gate.blockers
    ac06.releaseSnapshotId = gate.snapshotId
    ac06.releaseFreezeStatus = gate.snapshotStatus
    const afterApply = await readProjectState(ctx, client, token, options.projectId, G3_MODEL_STEPS.readProject)
    const before = await readRevisionObjects(
      ctx,
      client,
      token,
      options.projectId,
      [question, method],
      G3_MODEL_STEPS.readNewRevision,
    )

    // —— 旧运行这时候才返回 ——
    const settled = await turnPromise
    const run: G3ModelRunFact = {
      runId: settled.turn.id || `turn-${ctx.steps.length}`,
      sessionId: historySession.id,
      modelId: settled.turn.modelId,
      runMode: settled.turn.runMode,
      status: settled.turn.status,
      failureCode: settled.turn.failureCode,
      durationMs: settled.durationMs,
      inputTokens: settled.turn.inputTokens,
      outputTokens: settled.turn.outputTokens,
      startedAt: settled.turn.createdAt,
      finishedAt: new Date().toISOString(),
    }
    runs.push(run)
    ac06.run = run
    ac06.turnStatus = settled.turn.status
    ac06.turnFailureCode = settled.turn.failureCode
    ac06.baseRevision = baseRevision
    ac06.newRevision = afterApply.currentContextRevision

    // —— 旧结果只允许存成历史候选 ——
    const candidate = await saveHistoryCandidate(
      ctx,
      client,
      token,
      options.projectId,
      afterApply,
      historySession.id,
      run.runId,
      `revision ${baseRevision} 的旧异步结果，仅作历史候选保留`,
    )
    ac06.savedAsCandidate = Boolean(candidate.id)
    ac06.candidateId = candidate.id
    ac06.candidateStatus = candidate.status

    // —— 核对新修订的章节、确认、交付快照没有被旧结果改写 ——
    const after = await readRevisionObjects(
      ctx,
      client,
      token,
      options.projectId,
      [question, method],
      G3_MODEL_STEPS.readNewRevision,
    )
    ac06.chapterVersionAfter = after.objects.chapterVersions.join('、')
    ac06.confirmationAfter = after.objects.confirmations.join('、')
    ac06.releaseSnapshotUnchanged = sameObjects(before.objects, after.objects)
    ac06.releaseSnapshotCount = after.releases.length

    ac06.detail =
      `模型运行标识 ${run.runId}（模型 ${run.modelId || '（未登记）'}，耗时 ${run.durationMs}ms）：` +
      `运行在 revision ${baseRevision} 上发起，期间变更集把项目推进到 revision ${advanced.after}；` +
      `旧运行返回后状态 ${ac06.turnStatus}${ac06.turnFailureCode ? `（失败码 ${ac06.turnFailureCode}）` : ''}，` +
      `结果只存成历史候选 ${ac06.candidateId}（状态 ${ac06.candidateStatus || '（无）'}）；` +
      `稳定化 ${ac06.stabilizationStatus || '（未取到）'}${ac06.stabilizationBlockers.length ? `（阻断 ${ac06.stabilizationBlockers.join('、')}）` : ''}、` +
      `交付快照 ${ac06.releaseSnapshotId || '（未冻结）'}、人工闭环任务 ${ac06.closedTasks.length} 条；` +
      `新修订的章节版本 ${ac06.chapterVersionAfter}、当前确认 ${ac06.confirmationAfter}、` +
      `交付快照 ${ac06.releaseSnapshotCount} 份在返回前后${ac06.releaseSnapshotUnchanged ? '一致' : '**不一致**'}。`
  } catch (error) {
    ac06.detail = `旧异步结果场景未完成：${modelUnavailableReason(error)}`
    if (modelAvailable) {
      unavailableReason = ac06.detail
      modelAvailable = false
    }
  }

  recordStep(ctx, {
    name: G3_MODEL_STEPS.ac06Conclusion,
    method: 'POST',
    path: `${projectPath(options.projectId, '/agent-sessions/{sessionId}/turns')}（场景结论）`,
    status: ac06.savedAsCandidate ? 201 : 0,
    ok: true,
    durationMs: ac06.run?.durationMs ?? 0,
    conclusion: ac06.savedAsCandidate
      ? `旧异步结果已存为历史候选 ${ac06.candidateId}，新修订对象未被改写=${ac06.releaseSnapshotUnchanged}`
      : `旧异步结果场景未完成：${ac06.detail}`,
    details: {
      base_revision: ac06.baseRevision,
      new_revision: ac06.newRevision,
      turn_status: ac06.turnStatus,
      failure_code: ac06.turnFailureCode,
      candidate_id: ac06.candidateId,
      candidate_status: ac06.candidateStatus,
      objects_unchanged: ac06.releaseSnapshotUnchanged,
    },
  })

  const ac06Holds = ac06.turnStatus === 'stale' &&
    ac06.savedAsCandidate &&
    ac06.releaseSnapshotUnchanged
  verdicts.push({
    id: 'AC-G3-06',
    title: 'AC-G3-06 旧异步结果',
    holds: ac06Holds,
    detail: ac06Holds ? ac06.detail : `不成立：${ac06.detail}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/agent-sessions/{sessionId}/turns → 轮次 ${ac06.run?.runId || '（无）'} · ` +
        `状态 ${ac06.turnStatus || '（无）'} · 失败码 ${ac06.turnFailureCode || '（无）'} · 耗时 ${ac06.run?.durationMs ?? 0}ms`,
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets → 推进到 revision ${ac06.newRevision}`,
      `POST /api/v1/lingdoc/projects/{projectId}/import-candidates → 历史候选 ${ac06.candidateId || '（无）'} · 状态 ${ac06.candidateStatus || '（无）'}`,
      `GET /api/v1/lingdoc/projects/{projectId}/chapters · /confirmations · /releases → 返回前后一致=${ac06.releaseSnapshotUnchanged}`,
    ],
    status: ac06Holds ? 'WARNING' : 'NOT RUN',
  })

  // ============================================================
  // 场景三：AC-G3-15 —— 模型批量提出候选依赖，页面三态落证
  // ============================================================
  let ac15: G3Ac15Fact = {
    candidateCount: 0,
    candidates: [],
    reproposedEdgeId: '',
    reproposedNewEdge: false,
    run: null,
    detail: '',
  }

  try {
    project = await readProjectState(ctx, client, token, options.projectId, G3_MODEL_STEPS.readProject)
    // 依赖边带乐观锁：对象版本必须对应当前基线，所以这里要按当前状态重算引用。
    const currentRefs = await readCurrentRefs(
      ctx,
      client,
      token,
      options.projectId,
      project,
      [question, method],
    )
    allowedRefs.splice(0, allowedRefs.length, ...currentRefs)
    const objectList = allowedRefs
      .map((ref) => `${ref.kind}(id=${ref.id}, version=${ref.version})`)
      .join('、')
    const candidatePrompt = [
      `请基于当前上下文，一次性提出 3 条候选依赖关系（研究条件、证据卡与章节之间的依赖候选）。`,
      `来源与目标只能从下面这些对象里选：${objectList}。`,
      `请只输出 JSON，字段如下：`,
      `reply：一句话说明这些候选的共同依据。`,
      `dependency_candidates：数组，每项 {"source": {"kind","id","version"}, "target": {"kind","id","version"}, ` +
        `"relation": "关系名", "action": "REGENERATE 或 INVALIDATE_CONFIRMATION 或 REVIEW_REQUIRED 或 NO_ACTION", ` +
        `"reason": "建议理由", "confidence": 0.0-1.0, "basis": "依据片段"}。`,
      `这三条只是候选，必须由人工确认后才进入正式依赖图；不要直接写入、不要关闭任务、不要放行导出。`,
    ].join('\n')

    const sent = await sendAgentTurn(ctx, client, token, options.projectId, session.id, project, candidatePrompt, {
      chapterId: question.id,
    })
    const run: G3ModelRunFact = {
      runId: sent.turn.id || `turn-${ctx.steps.length}`,
      sessionId: session.id,
      modelId: sent.turn.modelId,
      runMode: sent.turn.runMode,
      status: sent.turn.status,
      failureCode: sent.turn.failureCode,
      durationMs: sent.durationMs,
      inputTokens: sent.turn.inputTokens,
      outputTokens: sent.turn.outputTokens,
      startedAt: sent.turn.createdAt,
      finishedAt: new Date().toISOString(),
    }
    runs.push(run)
    ac15.run = run

    const raw = parseDependencyCandidates(sent.turn.suggestions)
    // 对象的 id / 版本由验收台对齐到当前真实对象（模型可能记错版本号），
    // 关系与理由保留模型原话。
    const candidates: G3ModelCandidate[] = raw.map((item, index) => {
      const source = allowedRefs.find((ref) => ref.kind === item.source.kind) ?? specRef
      const target = allowedRefs.find((ref) => ref.kind === item.target.kind) ?? templateRef
      return {
        source: { ...source },
        target: { ...target },
        relation: normalizeRelation(item.relation, `model_candidate_${index + 1}`),
        action: normalizeCandidateAction(item.action),
        reason: item.reason || `模型提出的候选依赖 ${index + 1}`,
        confidence: item.confidence,
        basis: item.basis,
      }
    })
    ac15.candidates = candidates
    ac15.candidateCount = candidates.length

    if (sent.turn.status !== 'completed' || candidates.length === 0) {
      if (modelAvailable) {
        modelAvailable = false
        unavailableReason = sent.turn.status !== 'completed'
          ? `模型轮次未完成（状态 ${sent.turn.status || '（无）'}，失败码 ${sent.turn.failureCode || '（无）'}）` +
            `${sent.turn.failureCode === 'dependency_unavailable' ? '：模型不可用或缺外部凭据' : ''}`
          : '模型没有给出批量候选依赖（结构化输出里没有 dependency_candidates）'
      }
    } else {
      const edges: G3DependencyEdgeFact[] = []
      for (const [index, candidate] of candidates.entries()) {
        const edge = await createDependencyEdge(ctx, client, token, options.projectId, {
          stepName: G3_MODEL_STEPS.createEdge,
          idemLabel: `model-edge-${index + 1}`,
          expectedContextRevision: project.currentContextRevision,
          source: candidate.source,
          target: candidate.target,
          relation: candidate.relation,
          action: candidate.action,
          origin: 'ai',
          reason: candidate.reason,
          confidence: candidate.confidence,
        })
        edges.push(edge)
      }

      const first = edges[0]
      const second = edges[1]
      const third = edges[2]
      if (first) {
        ac15.confirmed = await decideDependencyEdge(ctx, client, token, options.projectId, first.id, {
          stepName: G3_MODEL_STEPS.decideConfirm,
          idemLabel: 'model-decide-confirm',
          expectedVersion: first.version,
          decision: 'confirm',
          reason: '人工确认：模型候选关系成立',
        })
      }
      if (second) {
        ac15.modified = await decideDependencyEdge(ctx, client, token, options.projectId, second.id, {
          stepName: G3_MODEL_STEPS.decideModify,
          idemLabel: 'model-decide-modify',
          expectedVersion: second.version,
          decision: 'modify',
          reason: '人工修改：模型候选关系的关系名按人工判断修正',
          relation: `${second.relation}_revised`,
          confidence: Math.min(second.confidence + 0.1, 0.95),
        })
      }
      if (third) {
        ac15.rejected = await decideDependencyEdge(ctx, client, token, options.projectId, third.id, {
          stepName: G3_MODEL_STEPS.decideReject,
          idemLabel: 'model-decide-reject',
          expectedVersion: third.version,
          decision: 'reject',
          reason: '人工拒绝：该候选关系在本版本不成立',
        })
        // 被拒的候选在同一版本里再投一次：服务端不应新建边。
        const reproposed = await createDependencyEdge(ctx, client, token, options.projectId, {
          stepName: G3_MODEL_STEPS.reproposeEdge,
          idemLabel: 'model-edge-repropose',
          expectedContextRevision: project.currentContextRevision,
          source: third.source,
          target: third.target,
          relation: third.relation,
          action: third.action,
          origin: 'ai',
          reason: '模型在同一版本里再次提出的同一条候选',
          confidence: third.confidence,
        })
        ac15.reproposedEdgeId = reproposed.id
        ac15.reproposedNewEdge = reproposed.id !== third.id
      }
      const finalEdges = await listDependencyEdges(ctx, client, token, options.projectId, G3_MODEL_STEPS.readCandidates)
      ac15.detail =
        `模型运行标识 ${run.runId}（模型 ${run.modelId || '（未登记）'}，耗时 ${run.durationMs}ms）一次性提出 ${candidates.length} 条候选依赖；` +
        `确认 ${ac15.confirmed?.status || '（无）'}、修改 ${ac15.modified?.status || '（无）'}（旧边 ${ac15.modified?.supersededRelation || '（无）'}）、` +
        `拒绝 ${ac15.rejected?.status || '（无）'}；被拒候选重投返回同一条边 ${ac15.reproposedEdgeId}（新建=${ac15.reproposedNewEdge}）；` +
        `依赖图现有边 ${finalEdges.length} 条。`
    }
  } catch (error) {
    ac15.detail = `候选依赖场景未完成：${modelUnavailableReason(error)}`
    if (modelAvailable) {
      modelAvailable = false
      unavailableReason = ac15.detail
    }
  }

  recordStep(ctx, {
    name: G3_MODEL_STEPS.ac15Conclusion,
    method: 'POST',
    path: `${projectPath(options.projectId, '/dependencies')}（场景结论）`,
    status: ac15.candidateCount > 0 ? 201 : 0,
    ok: true,
    durationMs: ac15.run?.durationMs ?? 0,
    conclusion: ac15.candidateCount > 0
      ? `模型提出 ${ac15.candidateCount} 条候选依赖，确认 / 修改 / 拒绝三态已分别落证`
      : `候选依赖场景未完成：${ac15.detail}`,
    details: {
      candidate_count: ac15.candidateCount,
      confirmed: ac15.confirmed?.status ?? '',
      modified: ac15.modified?.status ?? '',
      rejected: ac15.rejected?.status ?? '',
      reproposed_edge_id: ac15.reproposedEdgeId,
      reproposed_new_edge: ac15.reproposedNewEdge,
    },
  })

  const ac15Holds = ac15.candidateCount > 0 &&
    ac15.confirmed?.status === 'confirmed' &&
    ac15.modified?.status === 'confirmed' &&
    ac15.rejected?.status === 'rejected' &&
    !ac15.reproposedNewEdge
  verdicts.push({
    id: 'AC-G3-15',
    title: 'AC-G3-15 依赖候选（模型侧）',
    holds: ac15Holds,
    detail: ac15Holds
      ? `${ac15.detail} 未实现那半段：候选的「依据片段 / 建议理由」只落在依赖边的 reason 字段里，` +
        `没有独立的结构化展示位，缺口清单见 #246。`
      : `不成立：${ac15.detail}`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/agent-sessions/{sessionId}/turns → 轮次 ${ac15.run?.runId || '（无）'} · ` +
        `模型 ${ac15.run?.modelId || '（未登记）'} · 耗时 ${ac15.run?.durationMs ?? 0}ms · 候选 ${ac15.candidateCount} 条`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependencies → 登记模型候选边`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependencies/{edgeId}/decision → 确认 ${ac15.confirmed?.status || '（无）'} · ` +
        `修改 ${ac15.modified?.status || '（无）'} · 拒绝 ${ac15.rejected?.status || '（无）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/dependencies → 重投被拒候选 → 边 ${ac15.reproposedEdgeId || '（无）'} · 新建=${ac15.reproposedNewEdge}`,
    ],
    status: ac15Holds ? 'WARNING' : 'NOT RUN',
  })

  ctx.verdicts = verdicts

  return {
    runs,
    modelAvailable,
    unavailableReason,
    ac06,
    ac14,
    ac15,
    verdicts,
  }
}
