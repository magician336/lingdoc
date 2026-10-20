/**
 * G3 验收台-11（#295）· 路线图编辑会话链。
 *
 * 这一段把「技术路线」这条产品能力接进验收台：未冻结的编辑会话（提案 / 提交 /
 * 取消）、冻结版本序列与前后比较、必需路线节点缺失或版本落后时的阻断。
 *
 * 事实来源全部是真实 HTTP 面（`internal/lingdoc/workspace/roadmap_routes.go`）：
 * - `GET  /projects/{id}/roadmap` → 冻结基线
 * - `GET  /projects/{id}/roadmap/edits/{sessionId}` → 会话
 * - `POST /projects/{id}/roadmap/edits` → 建会话
 * - `POST /projects/{id}/roadmap/edits/{sessionId}/nodes` → 节点提案
 * - `POST /projects/{id}/roadmap/edits/{sessionId}/edges` → 边提案
 * - `POST /projects/{id}/roadmap/edits/{sessionId}/commit` → 提交
 * - `POST /projects/{id}/roadmap/edits/{sessionId}/cancel` → 取消
 * - `GET  /projects/{id}/roadmap/nodes/{nodeId}/versions` → 版本序列
 * - `GET  /projects/{id}/roadmap/nodes/{nodeId}/compare?from=&to=` → 前后比较
 *
 * 两条判定：
 * - `verdictAcG308`：必需 RouteNode 缺失或版本落后时，出现 blocking 校验问题，
 *   且项目不进入 STABILIZED / READY。
 * - `verdictAcG316`：取消未冻结编辑后回到上一个冻结版本，临时版本被释放，
 *   指向临时版本的任务落 STALE（失效任务，见 `GLOSSARY.md`）。
 *
 * 判定形状沿用 `runner/changeset.ts:209` 的 `G3Verdict`（= `G3RunVerdict`），
 * 不自创结构。
 */

import { errorCodeOf, type G3HttpClient } from './httpClient'
import { readProjectState, type G3ProjectState } from './changeset'
import {
  runDeliveryCheckWithIssues,
  type G3CheckWithIssuesFact,
} from './blocked'
import {
  makeIdempotencyKey,
  runStep,
  type G3RunContext,
} from './runContext'
import type { G3Verdict } from './changeset'

/** 步骤名。矩阵规则表按这些名字认「这一行跑到了没有」。 */
export const G3_ROADMAP_STEPS = {
  readBaseline: '路线图-读冻结基线',
  readTemplateRequired: '路线图-读模板必需项',
  beginSession: '路线图-建编辑会话',
  proposeNode: '路线图-提案节点',
  commit: '路线图-提交编辑',
  assessChangeSet: '路线图-评估变更集',
  applyChangeSet: '路线图-应用变更集',
  readBaselineAfterNode: '路线图-读冻结基线（建节点后）',
  beginEditSession: '路线图-建编辑会话（文字编辑）',
  proposeEditNode: '路线图-提案节点（文字编辑）',
  commitEdit: '路线图-提交编辑（文字编辑）',
  readSession: '路线图-读会话',
  readVersions: '路线图-读版本序列',
  compareVersions: '路线图-读前后比较',
  beginCancelSession: '路线图-建第二个编辑会话',
  proposeCancelNode: '路线图-提案（待取消）',
  cancel: '路线图-取消编辑',
  readAfterCancel: '路线图-读取消后基线',
  readTasks: '路线图-读影响任务',
  deliveryCheck: '路线图-交付检查',
  stabilize: '路线图-稳定化',
} as const

/** 路线图基线（`core.RoadmapBaseline`）。 */
export interface G3RoadmapBaselineFact {
  version: number
  contextRevision: number
  nodes: G3RoadmapNodeFact[]
  edges: G3RoadmapEdgeFact[]
}

/** 冻结路线节点（`core.RoadmapNodeView`）。 */
export interface G3RoadmapNodeFact {
  id: string
  projectId: string
  version: number
  name: string
  status: string
  /** 模板规则声明的必需标记。 */
  required: boolean
  /**
   * 节点状态：`current` / `pending_review` / `stale` / `missing` / `retired`。
   * 「待复核」与「版本落后」是**服务端算出来的状态**，不是页面的猜测。
   */
  state: string
  stateReason: string
  contextRevision: number
  inputs: G3RoadmapRefFact[]
  targets: G3RoadmapRefFact[]
  frozen: boolean
}

/** 路线边（`core.RouteEdge`）。 */
export interface G3RoadmapEdgeFact {
  id: string
  projectId: string
  version: number
  sourceId: string
  targetId: string
  relation: string
  status: string
  contextRevision: number
}

/** 对象版本引用。 */
export interface G3RoadmapRefFact {
  kind: string
  id: string
  version: string
}

/** 节点提案（`core.RoadmapNodeProposal`）。 */
export interface G3RoadmapNodeProposalFact {
  nodeId: string
  kind: string
  name: string
  status: string
  /** 提案占用的**临时版本号**：它永远不会被冻结。 */
  temporaryVersion: number
}

/** 边提案（`core.RoadmapEdgeProposal`）。 */
export interface G3RoadmapEdgeProposalFact {
  edgeId: string
  kind: string
  sourceId: string
  targetId: string
  relation: string
  temporaryVersion: number
}

/** 提交结果（`core.RoadmapCommitResult`）。 */
export interface G3RoadmapCommitFact {
  sessionId: string
  baselineVersion: number
  committedVersion: number
  appliedNodes: G3RoadmapNodeFact[]
  appliedEdges: G3RoadmapEdgeFact[]
  /** 语义改动时服务端给的是变更集，而不是冻结版本。 */
  changeSetId: string
  semantic: boolean
}

/** 编辑会话（`core.RoadmapEditSession`）。 */
export interface G3RoadmapSessionFact {
  id: string
  projectId: string
  status: string
  baseline: G3RoadmapBaselineFact
  nodeProposals: G3RoadmapNodeProposalFact[]
  edgeProposals: G3RoadmapEdgeProposalFact[]
  result: G3RoadmapCommitFact | null
  reason: string
}

/** 版本序列条目（`core.RoadmapVersionEntry`）。 */
export interface G3RoadmapVersionFact {
  version: number
  name: string
  status: string
  required: boolean
  state: string
  stateReason: string
  contextRevision: number
}

/** 前后比较（`core.RoadmapComparison`）。 */
export interface G3RoadmapComparisonFact {
  kind: string
  id: string
  from: number
  to: number
  changes: { field: string; from: string; to: string }[]
}

/** 影响任务事实（只取判定需要的字段）。 */
export interface G3RoadmapTaskFact {
  id: string
  target: G3RoadmapRefFact
  status: string
  staleReason: string
  closureEvidence: G3RoadmapRefFact[]
}

/** 稳定化事实（只取判定需要的字段）。 */
export interface G3RoadmapStabilizeFact {
  status: string
  blockers: { code: string; targetId: string; reason: string }[]
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function num(value: unknown): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

function bool(value: unknown): boolean {
  return value === true
}

function objectOf(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

function refOf(value: unknown): G3RoadmapRefFact {
  const raw = objectOf(value)
  return { kind: str(raw.kind), id: str(raw.id), version: str(raw.version) }
}

function projectPath(projectId: string, suffix: string): string {
  return `/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}${suffix}`
}

/** 把一个路线节点读成事实。 */
export function readRoadmapNodeFact(data: unknown): G3RoadmapNodeFact {
  const raw = objectOf(data)
  return {
    id: str(raw.id),
    projectId: str(raw.project_id),
    version: num(raw.version),
    name: str(raw.name),
    status: str(raw.status),
    required: bool(raw.required),
    state: str(raw.state),
    stateReason: str(raw.state_reason),
    contextRevision: num(raw.context_revision),
    inputs: (Array.isArray(raw.inputs) ? raw.inputs : []).map(refOf),
    targets: (Array.isArray(raw.targets) ? raw.targets : []).map(refOf),
    frozen: bool(raw.frozen),
  }
}

/** 把一条路线边读成事实。 */
export function readRoadmapEdgeFact(data: unknown): G3RoadmapEdgeFact {
  const raw = objectOf(data)
  return {
    id: str(raw.id),
    projectId: str(raw.project_id),
    version: num(raw.version),
    sourceId: str(raw.source_id),
    targetId: str(raw.target_id),
    relation: str(raw.relation),
    status: str(raw.status),
    contextRevision: num(raw.context_revision),
  }
}

/** 把冻结基线读成事实。 */
export function readRoadmapBaselineFact(data: unknown): G3RoadmapBaselineFact {
  const raw = objectOf(data)
  return {
    version: num(raw.version),
    contextRevision: num(raw.context_revision),
    nodes: (Array.isArray(raw.nodes) ? raw.nodes : []).map(readRoadmapNodeFact),
    edges: (Array.isArray(raw.edges) ? raw.edges : []).map(readRoadmapEdgeFact),
  }
}

/** 把提案读成事实。 */
function readNodeProposalFact(data: unknown): G3RoadmapNodeProposalFact {
  const raw = objectOf(data)
  return {
    nodeId: str(raw.node_id),
    kind: str(raw.kind),
    name: str(raw.name),
    status: str(raw.status),
    temporaryVersion: num(raw.temporary_version),
  }
}

/** 把边提案读成事实。 */
function readEdgeProposalFact(data: unknown): G3RoadmapEdgeProposalFact {
  const raw = objectOf(data)
  return {
    edgeId: str(raw.edge_id),
    kind: str(raw.kind),
    sourceId: str(raw.source_id),
    targetId: str(raw.target_id),
    relation: str(raw.relation),
    temporaryVersion: num(raw.temporary_version),
  }
}

/** 把提交结果读成事实。 */
export function readRoadmapCommitFact(data: unknown): G3RoadmapCommitFact {
  const raw = objectOf(data)
  return {
    sessionId: str(raw.session_id),
    baselineVersion: num(raw.baseline_version),
    committedVersion: num(raw.committed_version),
    appliedNodes: (Array.isArray(raw.applied_nodes) ? raw.applied_nodes : []).map(readRoadmapNodeFact),
    appliedEdges: (Array.isArray(raw.applied_edges) ? raw.applied_edges : []).map(readRoadmapEdgeFact),
    changeSetId: str(raw.change_set_id),
    semantic: bool(raw.semantic),
  }
}

/** 把编辑会话读成事实。 */
export function readRoadmapSessionFact(data: unknown): G3RoadmapSessionFact {
  const raw = objectOf(data)
  const result = raw.result
  return {
    id: str(raw.id),
    projectId: str(raw.project_id),
    status: str(raw.status),
    baseline: readRoadmapBaselineFact(raw.baseline),
    nodeProposals: (Array.isArray(raw.node_proposals) ? raw.node_proposals : []).map(readNodeProposalFact),
    edgeProposals: (Array.isArray(raw.edge_proposals) ? raw.edge_proposals : []).map(readEdgeProposalFact),
    result: result && typeof result === 'object' ? readRoadmapCommitFact(result) : null,
    reason: str(raw.reason),
  }
}

/** 读冻结基线。 */
export async function readRoadmapBaseline(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string = G3_ROADMAP_STEPS.readBaseline,
): Promise<G3RoadmapBaselineFact> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, '/roadmap'),
    token,
    conclusion: '冻结路线图基线已读回（HTTP {status}）',
    detail: (body) => {
      const baseline = readRoadmapBaselineFact(objectOf(body).data)
      return {
        version: baseline.version,
        context_revision: baseline.contextRevision,
        node_count: baseline.nodes.length,
        edge_count: baseline.edges.length,
        required_nodes: baseline.nodes
          .filter((node) => node.required)
          .map((node) => `${node.id}:${node.state}`)
          .join('、'),
      }
    },
  })
  return readRoadmapBaselineFact(data)
}

/** 建一个未冻结的编辑会话。 */
export async function beginRoadmapEdit(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedContextRevision: number
    reason: string
  },
): Promise<G3RoadmapSessionFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, '/roadmap/edits'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    body: {
      expected_context_revision: options.expectedContextRevision,
      reason: options.reason,
    },
    conclusion: '未冻结编辑会话已建立（HTTP {status}）',
    detail: (body) => {
      const session = readRoadmapSessionFact(objectOf(body).data)
      return {
        session_id: session.id,
        status: session.status,
        baseline_version: session.baseline.version,
      }
    },
  })
  return readRoadmapSessionFact(data)
}

/** 提案一个节点改动。 */
export async function proposeRoadmapNode(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  sessionId: string,
  options: {
    stepName: string
    idemLabel: string
    nodeId: string
    kind: 'add' | 'update' | 'delete'
    name: string
    status: string
    inputs?: G3RoadmapRefFact[]
    targets?: G3RoadmapRefFact[]
    reason: string
  },
): Promise<G3RoadmapSessionFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/roadmap/edits/${encodeURIComponent(sessionId)}/nodes`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    body: {
      node_id: options.nodeId,
      kind: options.kind,
      name: options.name,
      status: options.status,
      inputs: options.inputs ?? [],
      targets: options.targets ?? [],
      reason: options.reason,
    },
    conclusion: `节点提案已登记（HTTP {status}）：${options.nodeId}`,
    detail: (body) => {
      const session = readRoadmapSessionFact(objectOf(body).data)
      const proposal = session.nodeProposals.find((item) => item.nodeId === options.nodeId)
      return {
        session_id: session.id,
        node_id: options.nodeId,
        kind: options.kind,
        temporary_version: proposal?.temporaryVersion ?? 0,
        error_code: errorCodeOf(body),
      }
    },
  })
  return readRoadmapSessionFact(data)
}

/** 提案一条边改动。 */
export async function proposeRoadmapEdge(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  sessionId: string,
  options: {
    stepName: string
    idemLabel: string
    edgeId: string
    kind: 'add' | 'update' | 'delete'
    sourceId: string
    targetId: string
    relation: string
    reason: string
  },
): Promise<G3RoadmapSessionFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/roadmap/edits/${encodeURIComponent(sessionId)}/edges`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    body: {
      edge_id: options.edgeId,
      kind: options.kind,
      source_id: options.sourceId,
      target_id: options.targetId,
      relation: options.relation,
      reason: options.reason,
    },
    conclusion: `边提案已登记（HTTP {status}）：${options.edgeId}`,
    detail: (body) => {
      const session = readRoadmapSessionFact(objectOf(body).data)
      const proposal = session.edgeProposals.find((item) => item.edgeId === options.edgeId)
      return {
        session_id: session.id,
        edge_id: options.edgeId,
        kind: options.kind,
        temporary_version: proposal?.temporaryVersion ?? 0,
        error_code: errorCodeOf(body),
      }
    },
  })
  return readRoadmapSessionFact(data)
}

/** 提交编辑会话。 */
export async function commitRoadmapEdit(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  sessionId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedBaselineVersion: number
    reason: string
    expectedStatus?: number | readonly number[]
  },
): Promise<G3RoadmapCommitFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/roadmap/edits/${encodeURIComponent(sessionId)}/commit`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    expectedStatus: options.expectedStatus,
    body: {
      expected_baseline_version: options.expectedBaselineVersion,
      reason: options.reason,
    },
    conclusion: '编辑会话已提交（HTTP {status}）',
    detail: (body) => {
      const commit = readRoadmapCommitFact(objectOf(body).data)
      return {
        session_id: commit.sessionId,
        baseline_version: commit.baselineVersion,
        committed_version: commit.committedVersion,
        applied_nodes: commit.appliedNodes.length,
        applied_edges: commit.appliedEdges.length,
        change_set_id: commit.changeSetId,
        semantic: commit.semantic,
        error_code: errorCodeOf(body),
      }
    },
  })
  return readRoadmapCommitFact(data)
}

/** 取消编辑会话。 */
export async function cancelRoadmapEdit(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  sessionId: string,
  options: {
    stepName: string
    idemLabel: string
    expectedBaselineVersion: number
    reason: string
    expectedStatus?: number | readonly number[]
  },
): Promise<G3RoadmapSessionFact> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, `/roadmap/edits/${encodeURIComponent(sessionId)}/cancel`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    expectedStatus: options.expectedStatus,
    body: {
      expected_baseline_version: options.expectedBaselineVersion,
      reason: options.reason,
    },
    conclusion: '编辑会话已取消（HTTP {status}）',
    detail: (body) => {
      const session = readRoadmapSessionFact(objectOf(body).data)
      return {
        session_id: session.id,
        status: session.status,
        error_code: errorCodeOf(body),
      }
    },
  })
  return readRoadmapSessionFact(data)
}

/** 读编辑会话。 */
export async function readRoadmapSession(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  sessionId: string,
  stepName: string = G3_ROADMAP_STEPS.readSession,
): Promise<G3RoadmapSessionFact> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, `/roadmap/edits/${encodeURIComponent(sessionId)}`),
    token,
    conclusion: '编辑会话已读回（HTTP {status}）',
    detail: (body) => {
      const session = readRoadmapSessionFact(objectOf(body).data)
      return {
        session_id: session.id,
        status: session.status,
        node_proposals: session.nodeProposals.length,
        edge_proposals: session.edgeProposals.length,
      }
    },
  })
  return readRoadmapSessionFact(data)
}

/** 读一个节点的冻结版本序列。 */
export async function readRoadmapNodeVersions(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  nodeId: string,
  stepName: string = G3_ROADMAP_STEPS.readVersions,
): Promise<G3RoadmapVersionFact[]> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(projectId, `/roadmap/nodes/${encodeURIComponent(nodeId)}/versions`),
    token,
    conclusion: '节点版本序列已读回（HTTP {status}）',
    detail: (body) => {
      const list = Array.isArray(objectOf(body).data) ? (objectOf(body).data as unknown[]) : []
      return {
        version_count: list.length,
        versions: list.map((item) => {
          const entry = objectOf(item)
          return `${entry.version}:${entry.name}:${entry.state}`
        }),
      }
    },
  })
  const list = Array.isArray(data) ? (data as unknown[]) : []
  return list.map((item) => {
    const raw = objectOf(item)
    return {
      version: num(raw.version),
      name: str(raw.name),
      status: str(raw.status),
      required: bool(raw.required),
      state: str(raw.state),
      stateReason: str(raw.state_reason),
      contextRevision: num(raw.context_revision),
    }
  })
}

/** 读两个冻结版本之间的前后比较。 */
export async function compareRoadmapNodeVersions(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  nodeId: string,
  from: number,
  to: number,
  stepName: string = G3_ROADMAP_STEPS.compareVersions,
): Promise<G3RoadmapComparisonFact> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'GET',
    path: projectPath(
      projectId,
      `/roadmap/nodes/${encodeURIComponent(nodeId)}/compare?from=${from}&to=${to}`,
    ),
    token,
    conclusion: '节点前后比较已读回（HTTP {status}）',
    detail: (body) => {
      const comparison = objectOf(objectOf(body).data)
      const changes = Array.isArray(comparison.changes) ? comparison.changes : []
      return {
        from: comparison.from,
        to: comparison.to,
        change_count: changes.length,
        changes: changes.map((item) => {
          const change = objectOf(item)
          return `${change.field}:${change.from}→${change.to}`
        }),
      }
    },
  })
  const raw = objectOf(data)
  return {
    kind: str(raw.kind),
    id: str(raw.id),
    from: num(raw.from),
    to: num(raw.to),
    changes: (Array.isArray(raw.changes) ? raw.changes : []).map((item) => {
      const change = objectOf(item)
      return { field: str(change.field), from: str(change.from), to: str(change.to) }
    }),
  }
}

/** 读回影响任务（判定要确认「没有任务指向被取消的临时版本」）。 */
export async function readRoadmapImpactTasks(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string = G3_ROADMAP_STEPS.readTasks,
): Promise<G3RoadmapTaskFact[]> {
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
          const task = objectOf(item)
          return `${task.id}:${task.status}:${task.stale_reason || '-'}`
        }),
      }
    },
  })
  const list = Array.isArray(data) ? (data as unknown[]) : []
  return list.map((item) => {
    const raw = objectOf(item)
    return {
      id: str(raw.id),
      target: refOf(raw.target),
      status: str(raw.status),
      staleReason: str(raw.stale_reason),
      closureEvidence: (Array.isArray(raw.closure_evidence) ? raw.closure_evidence : []).map(refOf),
    }
  })
}

/** 读回稳定化结论（判定要确认「项目不进入 STABILIZED / READY」）。 */
export async function readRoadmapStabilization(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  changeSetId: string,
  stepName: string = G3_ROADMAP_STEPS.stabilize,
): Promise<G3RoadmapStabilizeFact> {
  const { data } = await runStep(ctx, client, {
    name: stepName,
    method: 'POST',
    path: projectPath(projectId, `/change-sets/${encodeURIComponent(changeSetId)}/stabilize`),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, `roadmap-stabilize-${changeSetId}`),
    body: {},
    conclusion: '稳定化已执行（HTTP {status}）',
    detail: (body) => {
      const fact = objectOf(objectOf(body).data)
      const blockers = Array.isArray(fact.blockers) ? fact.blockers : []
      return {
        status: fact.status,
        blocker_count: blockers.length,
        blockers: blockers.map((item) => {
          const blocker = objectOf(item)
          return `${blocker.code}:${blocker.target_id}`
        }),
        error_code: errorCodeOf(body),
      }
    },
  })
  const raw = objectOf(data)
  return {
    status: str(raw.status),
    blockers: (Array.isArray(raw.blockers) ? raw.blockers : []).map((item) => {
      const blocker = objectOf(item)
      return {
        code: str(blocker.code),
        targetId: str(blocker.target_id),
        reason: str(blocker.reason),
      }
    }),
  }
}

/** 链的输入。 */
export interface G3RoadmapChainOptions {
  projectId: string
  /** 当前操作者。 */
  operatorId: string
  /**
   * 供「版本落后」前置用的旧版本引用。
   *
   * 节点提案的 `inputs` 必须是**当前可读**的对象版本，否则服务端按 400 拒掉
   * （`roadmap_service.go:723-730`）。传一个**旧版本**引用正是本链要的
   * 「版本落后」前置：服务端在提交时把这种改动判成语义改动。
   */
  staleReference?: G3RoadmapRefFact
}

/** 链的结果。 */
export interface G3RoadmapChainResult {
  project: G3ProjectState
  /** 取消前的冻结基线。 */
  baselineBefore: G3RoadmapBaselineFact
  /** 提交后的冻结基线。 */
  baselineAfterCommit: G3RoadmapBaselineFact
  /** 取消后的冻结基线。 */
  baselineAfterCancel: G3RoadmapBaselineFact
  /** 被提交的会话。 */
  committedSession: G3RoadmapSessionFact
  /** 被取消的会话。 */
  cancelledSession: G3RoadmapSessionFact
  /** 第一次提交（改写节点）的结果。 */
  commit: G3RoadmapCommitFact
  /** 第二次提交（新增节点）的结果：语义路径给的是变更集。 */
  addCommit: G3RoadmapCommitFact
  /** 模板声明的必需节点（服务端按规则算出的 `required` 标志）。 */
  requiredNode: G3RoadmapNodeFact | null
  /** 冻结版本序列（读到的那一个节点）。 */
  versions: G3RoadmapVersionFact[]
  comparison: G3RoadmapComparisonFact | null
  tasks: G3RoadmapTaskFact[]
  /** 交付检查：读服务端给出的 blocking 校验问题。 */
  check: G3CheckWithIssuesFact | null
  stabilization: G3RoadmapStabilizeFact | null
  /** 被取消会话占用过的临时版本引用。 */
  temporaryRefs: G3RoadmapRefFact[]
  verdicts: G3Verdict[]
}

/** 找一个「非必需」的可编辑节点：必需节点不能被删，删了会得到 409。 */
function editableNodeOf(baseline: G3RoadmapBaselineFact): G3RoadmapNodeFact | null {
  return baseline.nodes.find((node) => node.state !== 'retired') ?? null
}

/** 找模板声明的必需节点（`required` 标志由服务端按模板规则算出）。 */
function requiredNodeOf(baseline: G3RoadmapBaselineFact): G3RoadmapNodeFact | null {
  return baseline.nodes.find((node) => node.required) ?? null
}

/**
 * 跑完整条路线图链。
 *
 * 顺序是**状态机的前置条件**，而且必须尊重服务端的两条提交路径
 * （`internal/lingdoc/workspacecore/roadmap_service.go:804-861`）：
 *
 * 1. **冻结路径**（改写已有节点的文字，前置关系不变）→ 提交后**冻结**一个新版本，
 *    版本序列与前后比较才读得到东西；
 * 2. **语义路径**（新增 / 删除节点、改前置关系、动边）→ 提交后服务端给的是**变更集**，
 *    路线图本身不被直接改写；
 * 3. 再建一个会话 → 提案 → 取消 → 读回（必须回到上一个冻结版本，临时版本被释放）。
 *
 * 链里的**两个节点提案前置**（对应 AC-G3-08 的「必需项缺失 / 版本落后」）：
 * - 必需项缺失：新增一个节点（语义路径），它不会立刻进冻结路线图；
 * - 版本落后：改写已有节点的前置引用，让它指向一个旧版本。
 * 两者都由服务端判定，本页不自行推算。
 */
export async function runRoadmapChain(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: G3RoadmapChainOptions,
): Promise<G3RoadmapChainResult> {
  const project = await readProjectState(ctx, client, token, options.projectId, '路线图-读项目状态')

  // —— 前置链：读冻结基线 → 建会话 → 提案 → 提交 → 读版本序列 → 前后比较 ——
  const baselineBefore = await readRoadmapBaseline(ctx, client, token, options.projectId)
  const session = await beginRoadmapEdit(ctx, client, token, options.projectId, {
    stepName: G3_ROADMAP_STEPS.beginSession,
    idemLabel: 'roadmap-begin',
    expectedContextRevision: project.currentContextRevision,
    reason: `G3 验收台-11：编辑技术路线（运行 ${ctx.runId}）`,
  })

  const editable = editableNodeOf(baselineBefore)
  if (!editable) {
    throw new Error('路线图链需要一个可编辑的冻结节点（当前基线里没有节点）')
  }

  // 提案一：**版本落后**前置——改写节点的前置引用，指向一个旧版本的对象。
  // 服务端会在提交时把这类改动判成语义改动（给变更集），路线图不被直接改写。
  const staleInput = options.staleReference
  const proposedInputs = staleInput
    ? [staleInput]
    : editable.inputs
  await proposeRoadmapNode(ctx, client, token, options.projectId, session.id, {
    stepName: G3_ROADMAP_STEPS.proposeNode,
    idemLabel: 'roadmap-propose-node',
    nodeId: editable.id,
    kind: 'update',
    name: `${editable.name}（G3 验收台-11 改写）`,
    status: editable.status === '' ? 'planned' : editable.status,
    inputs: proposedInputs,
    targets: editable.targets,
    reason: 'G3 验收台-11：改写节点（前置引用指向旧版本 = 版本落后前置）',
  })

  const commit = await commitRoadmapEdit(ctx, client, token, options.projectId, session.id, {
    stepName: G3_ROADMAP_STEPS.commit,
    idemLabel: 'roadmap-commit',
    expectedBaselineVersion: session.baseline.version,
    reason: 'G3 验收台-11：提交路线图编辑',
  })
  const committedSession = await readRoadmapSession(
    ctx,
    client,
    token,
    options.projectId,
    session.id,
    G3_ROADMAP_STEPS.readSession,
  )
  const baselineAfterCommit = await readRoadmapBaseline(
    ctx,
    client,
    token,
    options.projectId,
    G3_ROADMAP_STEPS.readBaselineAfterNode,
  )

  const versions = await readRoadmapNodeVersions(
    ctx,
    client,
    token,
    options.projectId,
    editable.id,
  )
  let comparison: G3RoadmapComparisonFact | null = null
  if (versions.length >= 2) {
    comparison = await compareRoadmapNodeVersions(
      ctx,
      client,
      token,
      options.projectId,
      editable.id,
      versions[0].version,
      versions[versions.length - 1].version,
    )
  } else {
    ctx.steps.push({
      index: ctx.steps.length + 1,
      runId: ctx.runId,
      name: G3_ROADMAP_STEPS.compareVersions,
      method: 'GET',
      path: projectPath(options.projectId, `/roadmap/nodes/${editable.id}/compare`),
      status: 0,
      ok: true,
      durationMs: 0,
      conclusion: `冻结版本只有 ${versions.length} 个，前后比较未执行`,
    })
  }

  // —— 提案二：**必需项缺失**前置——新增一个节点（语义路径）——
  // 新增节点是语义改动：提交后服务端给的是变更集，冻结路线图不被直接改写。
  const requiredNode = requiredNodeOf(baselineAfterCommit)
  const addSession = await beginRoadmapEdit(ctx, client, token, options.projectId, {
    stepName: G3_ROADMAP_STEPS.beginEditSession,
    idemLabel: 'roadmap-begin-add',
    expectedContextRevision: project.currentContextRevision,
    reason: 'G3 验收台-11：新增技术路线节点（必需项缺失前置）',
  })
  await proposeRoadmapNode(ctx, client, token, options.projectId, addSession.id, {
    stepName: G3_ROADMAP_STEPS.proposeEditNode,
    idemLabel: 'roadmap-propose-add',
    nodeId: `g3-route-${ctx.runId}`,
    kind: 'add',
    name: `G3 验收台-11 合成路线 ${ctx.runId}`,
    status: 'planned',
    inputs: [],
    targets: [],
    reason: 'G3 验收台-11：登记一个新增节点提案（必需项缺失前置）',
  })
  const addCommit = await commitRoadmapEdit(ctx, client, token, options.projectId, addSession.id, {
    stepName: G3_ROADMAP_STEPS.commitEdit,
    idemLabel: 'roadmap-commit-add',
    expectedBaselineVersion: addSession.baseline.version,
    reason: 'G3 验收台-11：提交新增节点提案',
  })

  // —— 取消链：建第二个会话 → 提案 → 取消 → 读回 ——
  const projectAfterCommit = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    '路线图-读项目状态（提交后）',
  )
  const cancelSession = await beginRoadmapEdit(ctx, client, token, options.projectId, {
    stepName: G3_ROADMAP_STEPS.beginCancelSession,
    idemLabel: 'roadmap-begin-cancel',
    expectedContextRevision: projectAfterCommit.currentContextRevision,
    reason: 'G3 验收台-11：编辑未冻结路线图（随后取消）',
  })
  await proposeRoadmapNode(ctx, client, token, options.projectId, cancelSession.id, {
    stepName: G3_ROADMAP_STEPS.proposeCancelNode,
    idemLabel: 'roadmap-propose-cancel',
    nodeId: editable.id,
    kind: 'update',
    name: `${editable.name}（G3 验收台-11 待取消）`,
    status: editable.status === '' ? 'planned' : editable.status,
    inputs: editable.inputs,
    targets: editable.targets,
    reason: 'G3 验收台-11：登记一个将被取消的未冻结提案',
  })
  const cancelledSession = await cancelRoadmapEdit(ctx, client, token, options.projectId, cancelSession.id, {
    stepName: G3_ROADMAP_STEPS.cancel,
    idemLabel: 'roadmap-cancel',
    expectedBaselineVersion: cancelSession.baseline.version,
    reason: 'G3 验收台-11：取消未冻结编辑',
  })
  const baselineAfterCancel = await readRoadmapBaseline(
    ctx,
    client,
    token,
    options.projectId,
    G3_ROADMAP_STEPS.readAfterCancel,
  )
  const tasks = await readRoadmapImpactTasks(ctx, client, token, options.projectId)

  // —— 交付检查：读服务端给出的 blocking 校验问题（AC-G3-08 要的那段事实）——
  // 必需 RouteNode 未就绪时，服务端把它的字段值留空，模板的 required_fields
  // 规则因此报出 blocking 问题（`RouteEvaluationFields` +
  // `internal/lingdoc/template/governance.go:468-485`）。页面只读服务端结论。
  const check = await runDeliveryCheckWithIssues(
    ctx,
    client,
    token,
    options.projectId,
    projectAfterCommit.projectVersion,
    G3_ROADMAP_STEPS.deliveryCheck,
  )
  const blockingIssues = check.issues.filter((issue) => issue.severity === 'blocking')

  // 被取消会话占用过的临时版本：它们**永远不会**被冻结。
  const temporaryRefs: G3RoadmapRefFact[] = [
    ...cancelledSession.nodeProposals.map((item) => ({
      kind: 'RouteNode',
      id: item.nodeId,
      version: String(item.temporaryVersion),
    })),
    ...cancelledSession.edgeProposals.map((item) => ({
      kind: 'RouteEdge',
      id: item.edgeId,
      version: String(item.temporaryVersion),
    })),
  ]

  const result: G3RoadmapChainResult = {
    project,
    baselineBefore,
    baselineAfterCommit,
    baselineAfterCancel,
    committedSession,
    cancelledSession,
    commit,
    addCommit,
    requiredNode,
    versions,
    comparison,
    tasks,
    check,
    stabilization: null,
    temporaryRefs,
    verdicts: [],
  }

  result.verdicts = [verdictAcG308(result), verdictAcG316(result)]
  // 判定挂到运行上下文上：矩阵规则表按判定 id 取结论，判定不成立就不许写 PASS。
  ctx.verdicts = [...(ctx.verdicts ?? []), ...result.verdicts]

  return result
}

/** 页面快照。 */
export interface G3RoadmapSnapshot {
  verdicts: G3Verdict[]
  baselineBefore: G3RoadmapBaselineFact
  baselineAfterCommit: G3RoadmapBaselineFact
  baselineAfterCancel: G3RoadmapBaselineFact
  commit: G3RoadmapCommitFact
  addCommit: G3RoadmapCommitFact
  requiredNode: G3RoadmapNodeFact | null
  versions: G3RoadmapVersionFact[]
  comparison: G3RoadmapComparisonFact | null
  tasks: G3RoadmapTaskFact[]
  temporaryRefs: G3RoadmapRefFact[]
  /** 提交后基线里的必需节点（服务端算出来的状态）。 */
  requiredNodes: G3RoadmapNodeFact[]
}

/** 把链结果压成页面快照。 */
export function toRoadmapSnapshot(result: G3RoadmapChainResult): G3RoadmapSnapshot {
  return {
    verdicts: result.verdicts,
    baselineBefore: result.baselineBefore,
    baselineAfterCommit: result.baselineAfterCommit,
    baselineAfterCancel: result.baselineAfterCancel,
    commit: result.commit,
    addCommit: result.addCommit,
    requiredNode: result.requiredNode,
    versions: result.versions,
    comparison: result.comparison,
    tasks: result.tasks,
    temporaryRefs: result.temporaryRefs,
    requiredNodes: result.baselineAfterCancel.nodes.filter((node) => node.required),
  }
}

/**
 * AC-G3-08 的判定：必需 RouteNode 缺失或版本落后时，出现 blocking 校验问题，
 * 且项目不进入 STABILIZED / READY。
 *
 * 口径说明（本票只交链路与判定函数，矩阵行状态由主会话在实跑后统一改）：
 * 「blocking 校验问题」由服务端给出——`required_route_not_current` 阻断原因码
 * （`internal/lingdoc/workspacecore/semantic_delta.go:765-785`）与
 * 「必需节点不在 `current` 态」的事实。本判定读的是**服务端返回的阻断原因**，
 * 页面不自行推算确定性。
 */
export function verdictAcG308(result: G3RoadmapChainResult): G3Verdict {
  const requiredNodes = result.baselineAfterCancel.nodes.filter((node) => node.required)
  const notCurrent = requiredNodes.filter(
    (node) => node.state !== 'current' && node.state !== 'retired',
  )
  // blocking 校验问题：服务端交付检查给出的 blocking 规则问题。
  const blockingIssues = (result.check?.issues ?? []).filter((issue) => issue.severity === 'blocking')
  // 稳定化结论：项目没有进入 STABILIZED。
  const blockers = result.stabilization?.blockers ?? []
  const routeBlockers = blockers.filter((blocker) => blocker.code === 'required_route_not_current')
  const notStabilized = result.stabilization !== null && result.stabilization.status !== 'STABILIZED'
  const holds =
    requiredNodes.length > 0 &&
    notCurrent.length > 0 &&
    blockingIssues.length > 0 &&
    notStabilized

  return {
    id: 'AC-G3-08',
    title: 'RouteNode 必需项：缺失或版本落后时阻断稳定化',
    holds,
    detail: holds
      ? `必需 RouteNode 共 ${requiredNodes.length} 个，其中 ${notCurrent.length} 个不是当前态` +
        `（${notCurrent.map((node) => `${node.id}:${node.state}${node.stateReason ? `（${node.stateReason}）` : ''}`).join('、')}）；` +
        `服务端交付检查给出 ${blockingIssues.length} 条 blocking 校验问题` +
        `（${blockingIssues.map((issue) => `${issue.ruleId}｜${issue.targetId}`).join('、')}），` +
        `稳定化结论为 ${result.stabilization?.status ?? '（未取到）'}——项目没有进入 STABILIZED / READY。`
      : `不成立：必需节点 ${requiredNodes.length} 个，其中非当前态 ${notCurrent.length} 个；` +
        `blocking 校验问题 ${blockingIssues.length} 条；` +
        `稳定化结论 ${result.stabilization?.status ?? '（本轮未取到）'}。` +
        `（本行要的是「服务端给出 blocking 校验问题且项目不放行」这一整段事实；` +
        `缺少其中任何一半都不能记 PASS。）` +
        (requiredNodes.length === 0
          ? '本轮基线里没有模板声明的必需 RouteNode，缺少「必需项缺失 / 版本落后」这一前置。'
          : ''),
    evidence: [
      `GET /api/v1/lingdoc/projects/{projectId}/roadmap → HTTP 200 · 必需节点 ${requiredNodes.length} 个 · ` +
        `非当前态 ${notCurrent.length} 个（${notCurrent.map((node) => `${node.id}:${node.state}`).join('、') || '（无）'}）`,
      `POST /api/v1/lingdoc/projects/{projectId}/checks → HTTP 200 · ` +
        `status=${result.check?.status ?? '（本轮未取到）'} · ` +
        `blocking 问题 ${blockingIssues.length} 条` +
        `（${blockingIssues.map((issue) => `${issue.ruleId}:${issue.targetId}`).join('、') || '（无）'}）`,
      `GET /api/v1/lingdoc/projects/{projectId}/roadmap/nodes/{nodeId}/versions → HTTP 200 · ` +
        `版本序列 ${result.versions.map((entry) => `${entry.version}:${entry.state}`).join('、') || '（空）'}`,
      `POST /api/v1/lingdoc/projects/{projectId}/roadmap/edits/{sessionId}/commit（新增节点）→ HTTP 200 · ` +
        `semantic=${result.addCommit?.semantic ?? false} · change_set_id=${result.addCommit?.changeSetId || '（无）'}` +
        `（新增节点是语义改动，冻结路线图不被直接改写）`,
    ],
    status: holds ? 'PASS' : 'NOT RUN',
  }
}

/**
 * AC-G3-16 的判定：取消未冻结编辑后回到上一个冻结版本，临时版本被释放，
 * 指向临时版本的任务落 STALE（失效任务）。
 *
 * 「落 STALE」的口径：凡是目标或闭环证据指向被取消临时版本的任务，
 * 它的状态必须是 `STALE`（`internal/lingdoc/workspacecore/roadmap_service.go:433-478`）。
 */
export function verdictAcG316(result: G3RoadmapChainResult): G3Verdict {
  const cancelled = result.cancelledSession.status === 'cancelled'
  const versionRestored =
    result.baselineAfterCancel.version === result.baselineAfterCommit.version
  const nodeRestored = result.baselineAfterCancel.nodes.every((node) => {
    const before = result.baselineAfterCommit.nodes.find((item) => item.id === node.id)
    return before === undefined || before.version === node.version
  })
  // 临时版本不得出现在冻结版本序列里：它**从来没有被冻结**。
  // 节点看版本序列，边看冻结基线里的边版本。
  const temporaryVersionInSequence = result.temporaryRefs.some((ref) => {
    if (ref.kind === 'RouteNode') {
      const nodeInSequence = result.versions.some(
        (entry) => entry.version === Number(ref.version),
      )
      const nodeInBaseline = result.baselineAfterCancel.nodes.some(
        (node) => node.id === ref.id && node.version === Number(ref.version),
      )
      return nodeInSequence || nodeInBaseline
    }
    const edgeInBaseline = result.baselineAfterCancel.edges.some(
      (edge) => edge.id === ref.id && edge.version === Number(ref.version),
    )
    return edgeInBaseline
  })
  const pointingAtTemporary = result.tasks.filter((task) => {
    const targetHits = result.temporaryRefs.some(
      (ref) => ref.kind === task.target.kind && ref.id === task.target.id && ref.version === task.target.version,
    )
    const evidenceHits = task.closureEvidence.some((ref) =>
      result.temporaryRefs.some(
        (item) => item.kind === ref.kind && item.id === ref.id && item.version === ref.version,
      ),
    )
    return targetHits || evidenceHits
  })
  const allStale = pointingAtTemporary.every((task) => task.status === 'STALE')
  const holds = cancelled && versionRestored && nodeRestored && !temporaryVersionInSequence && allStale

  return {
    id: 'AC-G3-16',
    title: '路线图版本恢复：取消未冻结编辑后回到上一个冻结版本',
    holds,
    detail: holds
      ? `未冻结编辑已取消（会话状态 ${result.cancelledSession.status}）；` +
        `冻结基线版本回到 ${result.baselineAfterCancel.version}（与取消前一致），` +
        `节点版本全部回到提交后的版本；被取消会话占用的临时版本 ` +
        `${result.temporaryRefs.map((ref) => `${ref.kind}/${ref.id}@v${ref.version}`).join('、') || '（无）'} ` +
        `没有出现在冻结版本序列里；指向临时版本的任务 ${pointingAtTemporary.length} 条，全部为 STALE。`
      : `不成立：会话状态 ${result.cancelledSession.status || '（未取到）'}；` +
        `基线版本 ${result.baselineAfterCommit.version} → ${result.baselineAfterCancel.version}（未恢复=${!versionRestored}）；` +
        `节点版本未恢复=${!nodeRestored}；临时版本出现在冻结序列=${temporaryVersionInSequence}；` +
        `指向临时版本的任务 ${pointingAtTemporary.length} 条，其中非 STALE ` +
        `${pointingAtTemporary.filter((task) => task.status !== 'STALE').length} 条。`,
    evidence: [
      `POST /api/v1/lingdoc/projects/{projectId}/roadmap/edits/{sessionId}/cancel → HTTP 200 · ` +
        `会话状态 ${result.cancelledSession.status || '（未取到）'}`,
      `GET /api/v1/lingdoc/projects/{projectId}/roadmap → HTTP 200 · ` +
        `基线版本 ${result.baselineAfterCommit.version} → ${result.baselineAfterCancel.version} · ` +
        `节点版本全部回到提交后=${nodeRestored}`,
      `GET /api/v1/lingdoc/projects/{projectId}/roadmap/nodes/{nodeId}/versions → HTTP 200 · ` +
        `版本序列 ${result.versions.map((entry) => String(entry.version)).join('、') || '（空）'} · ` +
        `含被取消的临时版本=${temporaryVersionInSequence}`,
      `GET /api/v1/lingdoc/projects/{projectId}/impact-tasks → HTTP 200 · ` +
        `指向临时版本的任务 ${pointingAtTemporary.length} 条 · ` +
        `状态 ${pointingAtTemporary.map((task) => `${task.id}:${task.status}`).join('、') || '（无）'}`,
    ],
    status: holds ? 'PASS' : 'NOT RUN',
  }
}
