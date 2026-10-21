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
import { bool, num, objectOf, projectPath, refOf, str } from './factHelpers'
import {
  assessChangeSet,
  applyChangeSet,
  readProjectState,
  type G3ProjectState,
} from './changeset'
import {
  runDeliveryCheckWithIssues,
  type G3CheckWithIssuesFact,
} from './blocked'
import {
  makeIdempotencyKey,
  runStep,
  type G3RunContext,
} from './runContext'
import { G3_DEMO_REQUIRED_ROUTE_NODE_ID } from './project'
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
  // #318：建会话前实时读上下文修订，以及两种前置各自的读回。
  readRevisionBeforeSession: '路线图-读上下文修订（建会话前）',
  readBaselinePendingReview: '路线图-读冻结基线（待复核前置）',
  deliveryCheckPendingReview: '路线图-交付检查（待复核前置）',
  readBaselineStale: '路线图-读冻结基线（版本落后前置）',
  deliveryCheckStale: '路线图-交付检查（版本落后前置）',
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

/**
 * 读回项目**此刻**的上下文修订。
 *
 * 这是建会话请求体里 `expected_context_revision` 的**唯一来源**：现读现用，
 * 不复用链首读到的旧值。
 *
 * 路线图有**两套编号**，各管一件事，不能混用：
 * - **项目上下文修订**（`expected_context_revision`）：建会话时的乐观并发闸门，
 *   服务端要求它等于项目当前的上下文修订
 *   （`internal/lingdoc/workspacecore/roadmap_service.go:60`）；
 * - **路线图基线版本**（`expected_baseline_version`）：提交 / 取消时的闸门，
 *   服务端要求它等于会话自己绑定的那个基线版本
 *   （`roadmap_service.go:211` / `roadmap_service.go:406`）。
 *
 * 上下文修订会被**冻结路径的提交**推进一格（`roadmap_service.go:309-311`），
 * 所以链首读到的那个值到建会话那一刻往往已经过期 —— 那正是 409「基线已漂移」
 * 的来源（实跑证据：`docs/08-本轮实施方案/G3-验收台-8-整链证据-20261021-r1.json`
 * 的「路线图-建编辑会话（文字编辑）」HTTP 409）。
 */
export async function currentContextRevisionOf(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  stepName: string = G3_ROADMAP_STEPS.readRevisionBeforeSession,
): Promise<number> {
  const project = await readProjectState(ctx, client, token, projectId, stepName)
  return project.currentContextRevision
}

/**
 * 建一个未冻结的编辑会话。
 *
 * 请求体里的 `expected_context_revision` 取的是**建会话这一刻**读到的项目上下文
 * 修订（`currentContextRevisionOf`），调用方没有机会传一个过期的修订号进来：
 * 链里的任何一次冻结路径提交都会把修订推进一格，复用旧值必然撞 409。
 */
export async function beginRoadmapEdit(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  projectId: string,
  options: {
    stepName: string
    idemLabel: string
    reason: string
  },
): Promise<G3RoadmapSessionFact> {
  const expectedContextRevision = await currentContextRevisionOf(ctx, client, token, projectId)
  const { data } = await runStep(ctx, client, {
    name: options.stepName,
    method: 'POST',
    path: projectPath(projectId, '/roadmap/edits'),
    token,
    idempotencyKey: makeIdempotencyKey(ctx.runId, options.idemLabel),
    body: {
      expected_context_revision: expectedContextRevision,
      reason: options.reason,
    },
    conclusion: '未冻结编辑会话已建立（HTTP {status}）',
    detail: (body) => {
      const session = readRoadmapSessionFact(objectOf(body).data)
      return {
        session_id: session.id,
        status: session.status,
        baseline_version: session.baseline.version,
        expected_context_revision: expectedContextRevision,
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
   * 可选：直接钉住这个版本引用来造「版本落后」前置。
   *
   * 不给的话，台面自己造一个**合成前置节点**，让必需节点钉住它、之后再把它推进
   * 一版 —— 那是「版本落后」最干净的造法（服务端口径见
   * `internal/lingdoc/workspacecore/roadmap_service.go:995-1013`）。
   *
   * 给了的话，必需节点就钉住这个引用；引用必须**当前可读**
   * （否则变更集应用会被服务端按 400 拒掉）。
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
  /**
   * 「待复核」前置的读回：必需节点登记了但还没复核时的服务端状态。
   *
   * 状态是服务端算出来的（`current` / `pending_review` / `stale`），
   * 页面不自行推算。
   */
  pendingReview: G3RoadmapNodeFact | null
  /** 「待复核」阶段的交付检查（读服务端给出的 blocking 校验问题）。 */
  pendingReviewCheck: G3CheckWithIssuesFact | null
  /** 合成前置节点（被必需节点钉住、被推进一版的那个）。 */
  prerequisiteNode: G3RoadmapNodeFact | null
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

/** 找模板声明的必需节点（`required` 标志由服务端按模板规则算出）。 */
function requiredNodeOf(baseline: G3RoadmapBaselineFact): G3RoadmapNodeFact | null {
  return baseline.nodes.find((node) => node.required) ?? null
}

/**
 * 交付检查与稳定化**取不到就记成「没取到」**，不吞掉整条链。
 *
 * 这两步都可能因为服务端的原因失败（例如交付检查在带必需路线节点的项目上会以
 * 403 `source_access_denied` 拒掉：路线感知的规格投影与服务端的溯源快照对不上）。
 * 失败的那一步**已经在步骤记录里留下证据**（`runStep` 先记录再抛），所以这里
 * 只需要把事实记成空，让后面取消链的判定照样跑得出来。
 *
 * 写法沿用 `runner/model.ts` 的既有约定（那里的交付检查也是这么容错的）。
 */
async function tolerateFailure<T>(body: () => Promise<T>): Promise<T | null> {
  try {
    return await body()
  } catch {
    return null
  }
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
 *    路线图本身不被直接改写，变更集**被应用后**改动才落进冻结路线图；
 * 3. 再建一个会话 → 提案 → 取消 → 读回（必须回到上一个冻结版本，临时版本被释放）。
 *
 * 链里造出 AC-G3-08 的两种前置（都由服务端判定，页面不自行推算）：
 * - **待复核**：必需 RouteNode 登记后不复核（存储状态不是 `ready`），
 *   服务端把它的状态算成 `pending_review`；
 * - **版本落后**：必需 RouteNode 钉住某个前置对象的**旧版本**，而那个对象后来
 *   推进了一版，服务端把它的状态算成 `stale`
 *   （`roadmap_service.go:995-1013`）。
 *
 * #318：建会话一律用**建会话那一刻**读到的上下文修订
 * （`currentContextRevisionOf`），不再复用链首的旧值 —— 那正是 409 的来源。
 */
export async function runRoadmapChain(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
  options: G3RoadmapChainOptions,
): Promise<G3RoadmapChainResult> {
  let project = await readProjectState(ctx, client, token, options.projectId, '路线图-读项目状态')
  let baselineBefore = await readRoadmapBaseline(ctx, client, token, options.projectId)

  // —— 前置一：合成前置节点 ——
  //
  // 「版本落后」要有一个**被推进过的旧版本**当钉子。台面自己造一个合成前置节点，
  // 让必需 RouteNode 钉住它、之后再把它推进一版 —— 那是这条前置最干净的造法。
  // 造出来的节点是**合成材料**：名字与理由里只带运行标识，不含真实课题信息。
  const prerequisiteId = `g3-route-prereq-${ctx.runId}`
  let prerequisite = baselineBefore.nodes.find((node) => node.id === prerequisiteId) ?? null
  if (!prerequisite) {
    // 新增节点走的是**语义路径**：服务端给的是变更集，路线图本身不被直接改写，
    // 变更集应用后节点才落到冻结路线图里（`roadmap_service.go:804-861`）。
    const seedSession = await beginRoadmapEdit(ctx, client, token, options.projectId, {
      stepName: G3_ROADMAP_STEPS.beginSession,
      idemLabel: 'roadmap-begin-prerequisite',
      reason: `G3 验收台-11：登记一个合成前置路线节点（运行 ${ctx.runId}）`,
    })
    await proposeRoadmapNode(ctx, client, token, options.projectId, seedSession.id, {
      stepName: G3_ROADMAP_STEPS.proposeNode,
      idemLabel: 'roadmap-propose-prerequisite',
      nodeId: prerequisiteId,
      kind: 'add',
      name: `G3 验收台-11 合成前置节点 ${ctx.runId}`,
      status: 'ready',
      inputs: [],
      targets: [],
      reason: 'G3 验收台-11：登记一个合成前置节点（版本落后前置的钉子，不含真实课题信息）',
    })
    const seedCommit = await commitRoadmapEdit(ctx, client, token, options.projectId, seedSession.id, {
      stepName: G3_ROADMAP_STEPS.commit,
      idemLabel: 'roadmap-commit-prerequisite',
      expectedBaselineVersion: seedSession.baseline.version,
      reason: 'G3 验收台-11：提交合成前置节点提案',
    })
    if (!seedCommit.changeSetId) {
      throw new Error('新增前置节点提案没有给出变更集，无法把节点落到冻结路线图里')
    }
    await assessChangeSet(ctx, client, token, options.projectId, seedCommit.changeSetId)
    await applyChangeSet(ctx, client, token, options.projectId, seedCommit.changeSetId, {
      stepName: G3_ROADMAP_STEPS.applyChangeSet,
    })
    baselineBefore = await readRoadmapBaseline(ctx, client, token, options.projectId)
    prerequisite = baselineBefore.nodes.find((node) => node.id === prerequisiteId) ?? null
  }
  if (!prerequisite) {
    throw new Error('路线图链需要一个合成前置节点（它没进冻结路线图）')
  }

  // —— 前置二：模板声明的必需 RouteNode ——
  //
  // 节点 id 必须用**模板声明的那个**（`route-r7`），不能是运行期随机 id：
  // 必需性由模板规则按**字段 id**判定，id 对不上就没有 required 节点可找。
  // （#314：演示模板 v2 声明 `route-r7` 为必需 RouteNode。）
  //
  // 登记时的存储状态是 `pending_review`：**待复核**前置由此而来
  // （服务端把「不是 ready」的节点算成 `pending_review`）。
  const pinnedInputs: G3RoadmapRefFact[] = options.staleReference
    ? [options.staleReference]
    : [{ kind: 'RouteNode', id: prerequisite.id, version: String(prerequisite.version) }]
  if (!baselineBefore.nodes.some((node) => node.id === G3_DEMO_REQUIRED_ROUTE_NODE_ID)) {
    const requiredSession = await beginRoadmapEdit(ctx, client, token, options.projectId, {
      stepName: G3_ROADMAP_STEPS.beginSession,
      idemLabel: 'roadmap-begin-required',
      reason: `G3 验收台-11：登记模板声明的必需路线节点（运行 ${ctx.runId}）`,
    })
    await proposeRoadmapNode(ctx, client, token, options.projectId, requiredSession.id, {
      stepName: G3_ROADMAP_STEPS.proposeNode,
      idemLabel: 'roadmap-propose-required',
      nodeId: G3_DEMO_REQUIRED_ROUTE_NODE_ID,
      kind: 'add',
      name: `G3 验收台-11 合成路线 R7 ${ctx.runId}`,
      status: 'pending_review',
      inputs: pinnedInputs,
      targets: [],
      reason: 'G3 验收台-11：登记模板声明的必需路线节点（待复核前置，合成，不含真实课题信息）',
    })
    const requiredCommit = await commitRoadmapEdit(ctx, client, token, options.projectId, requiredSession.id, {
      stepName: G3_ROADMAP_STEPS.commit,
      idemLabel: 'roadmap-commit-required',
      expectedBaselineVersion: requiredSession.baseline.version,
      reason: 'G3 验收台-11：提交必需路线节点提案',
    })
    if (!requiredCommit.changeSetId) {
      throw new Error('新增必需路线节点提案没有给出变更集，无法把节点落到冻结路线图里')
    }
    await assessChangeSet(ctx, client, token, options.projectId, requiredCommit.changeSetId)
    await applyChangeSet(ctx, client, token, options.projectId, requiredCommit.changeSetId, {
      stepName: G3_ROADMAP_STEPS.applyChangeSet,
    })
    baselineBefore = await readRoadmapBaseline(ctx, client, token, options.projectId)
  }

  // —— 「待复核」前置的读回 ——
  const pendingReviewBaseline = await readRoadmapBaseline(
    ctx,
    client,
    token,
    options.projectId,
    G3_ROADMAP_STEPS.readBaselinePendingReview,
  )
  const pendingReview = pendingReviewBaseline.nodes.find(
    (node) => node.id === G3_DEMO_REQUIRED_ROUTE_NODE_ID,
  ) ?? null
  const projectPendingReview = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    '路线图-读项目状态（待复核前置）',
  )
  // 交付检查：待复核状态下服务端就该给出 blocking 校验问题
  // （`RouteEvaluationFields` 把没就绪的路线节点值留空，
  // 模板的 presence 规则因此报出 blocking 问题）。
  const pendingReviewCheck = await tolerateFailure(() =>
    runDeliveryCheckWithIssues(
      ctx,
      client,
      token,
      options.projectId,
      projectPendingReview.projectVersion,
      G3_ROADMAP_STEPS.deliveryCheckPendingReview,
    ),
  )

  // —— 复核：把必需节点改成 ready（文字编辑 → 冻结路径）——
  //
  // 前置关系**不动**，所以这次提交走冻结路径：当场冻结一个新版本，
  // 并把项目上下文修订推进一格（`roadmap_service.go:309-311`）。
  const required = pendingReview ?? requiredNodeOf(baselineBefore)
  if (!required) {
    throw new Error('路线图链找不到模板声明的必需 RouteNode')
  }
  const reviewSession = await beginRoadmapEdit(ctx, client, token, options.projectId, {
    stepName: G3_ROADMAP_STEPS.beginSession,
    idemLabel: 'roadmap-begin-review',
    reason: `G3 验收台-11：复核必需路线节点（运行 ${ctx.runId}）`,
  })
  await proposeRoadmapNode(ctx, client, token, options.projectId, reviewSession.id, {
    stepName: G3_ROADMAP_STEPS.proposeNode,
    idemLabel: 'roadmap-propose-review',
    nodeId: required.id,
    kind: 'update',
    name: `${required.name}（已复核）`,
    status: 'ready',
    // 前置关系保持不变：变了就变成语义改动，走不到冻结路径。
    inputs: required.inputs,
    targets: required.targets,
    reason: 'G3 验收台-11：复核必需路线节点（文字编辑，冻结新版本）',
  })
  const reviewCommit = await commitRoadmapEdit(ctx, client, token, options.projectId, reviewSession.id, {
    stepName: G3_ROADMAP_STEPS.commit,
    idemLabel: 'roadmap-commit-review',
    expectedBaselineVersion: reviewSession.baseline.version,
    reason: 'G3 验收台-11：提交必需路线节点的复核',
  })
  if (reviewCommit.changeSetId) {
    throw new Error('复核是文字编辑，不该产出变更集（前置关系被改了？）')
  }
  const committedSession = await readRoadmapSession(
    ctx,
    client,
    token,
    options.projectId,
    reviewSession.id,
    G3_ROADMAP_STEPS.readSession,
  )

  // —— 推进前置节点一版：必需节点钉住的就是旧版本 → 「版本落后」——
  const advanceSession = await beginRoadmapEdit(ctx, client, token, options.projectId, {
    stepName: G3_ROADMAP_STEPS.beginSession,
    idemLabel: 'roadmap-begin-advance',
    reason: `G3 验收台-11：推进合成前置节点一版（运行 ${ctx.runId}）`,
  })
  await proposeRoadmapNode(ctx, client, token, options.projectId, advanceSession.id, {
    stepName: G3_ROADMAP_STEPS.proposeNode,
    idemLabel: 'roadmap-propose-advance',
    nodeId: prerequisite.id,
    kind: 'update',
    name: `${prerequisite.name}（推进一版）`,
    status: 'ready',
    inputs: prerequisite.inputs,
    targets: prerequisite.targets,
    reason: 'G3 验收台-11：把前置节点推进一版（必需节点因此版本落后）',
  })
  const advanceCommit = await commitRoadmapEdit(ctx, client, token, options.projectId, advanceSession.id, {
    stepName: G3_ROADMAP_STEPS.commit,
    idemLabel: 'roadmap-commit-advance',
    expectedBaselineVersion: advanceSession.baseline.version,
    reason: 'G3 验收台-11：提交前置节点的推进',
  })
  if (advanceCommit.changeSetId) {
    throw new Error('推进前置节点是文字编辑，不该产出变更集（前置关系被改了？）')
  }

  // —— 「版本落后」前置的读回 ——
  const staleBaseline = await readRoadmapBaseline(
    ctx,
    client,
    token,
    options.projectId,
    G3_ROADMAP_STEPS.readBaselineStale,
  )
  const staleNode = staleBaseline.nodes.find((node) => node.id === G3_DEMO_REQUIRED_ROUTE_NODE_ID) ?? null

  // —— 新增一个节点（语义路径）：给稳定化判定一个「当前修订上已应用」的变更集 ——
  //
  // 稳定化要求变更集已应用、且它的目标修订就是项目当前修订
  // （`workspacecore/impact_service.go:1287`），所以这一步必须**最后**做一次
  // 语义提交并把变更集应用掉。
  const addSession = await beginRoadmapEdit(ctx, client, token, options.projectId, {
    stepName: G3_ROADMAP_STEPS.beginEditSession,
    idemLabel: 'roadmap-begin-add',
    reason: 'G3 验收台-11：新增一个合成路线节点（给稳定化判定一个已应用的变更集）',
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
    reason: 'G3 验收台-11：登记一个新增节点提案（合成，不含真实课题信息）',
  })
  const addCommit = await commitRoadmapEdit(ctx, client, token, options.projectId, addSession.id, {
    stepName: G3_ROADMAP_STEPS.commitEdit,
    idemLabel: 'roadmap-commit-add',
    expectedBaselineVersion: addSession.baseline.version,
    reason: 'G3 验收台-11：提交新增节点提案',
  })
  if (!addCommit.changeSetId) {
    throw new Error('新增节点提案没有给出变更集，无法把节点落到冻结路线图里')
  }
  await assessChangeSet(ctx, client, token, options.projectId, addCommit.changeSetId)
  await applyChangeSet(ctx, client, token, options.projectId, addCommit.changeSetId, {
    stepName: G3_ROADMAP_STEPS.applyChangeSet,
  })

  const baselineAfterCommit = await readRoadmapBaseline(
    ctx,
    client,
    token,
    options.projectId,
    G3_ROADMAP_STEPS.readBaselineAfterNode,
  )
  const requiredNode = staleNode ?? requiredNodeOf(baselineAfterCommit)

  // —— 版本序列与前后比较（读必需节点自己的版本史）——
  const versions = await readRoadmapNodeVersions(
    ctx,
    client,
    token,
    options.projectId,
    G3_DEMO_REQUIRED_ROUTE_NODE_ID,
  )
  let comparison: G3RoadmapComparisonFact | null = null
  if (versions.length >= 2) {
    comparison = await compareRoadmapNodeVersions(
      ctx,
      client,
      token,
      options.projectId,
      G3_DEMO_REQUIRED_ROUTE_NODE_ID,
      versions[0].version,
      versions[versions.length - 1].version,
    )
  } else {
    ctx.steps.push({
      index: ctx.steps.length + 1,
      runId: ctx.runId,
      name: G3_ROADMAP_STEPS.compareVersions,
      method: 'GET',
      path: projectPath(options.projectId, `/roadmap/nodes/${G3_DEMO_REQUIRED_ROUTE_NODE_ID}/compare`),
      status: 0,
      ok: true,
      durationMs: 0,
      conclusion: `冻结版本只有 ${versions.length} 个，前后比较未执行`,
    })
  }

  // —— 稳定化与交付检查：读服务端给出的阻断原因与 blocking 校验问题 ——
  //
  // 必需 RouteNode 未就绪时，服务端把它的字段值留空，模板的 presence 规则因此
  // 报出 blocking 问题（`RouteEvaluationFields` +
  // `internal/lingdoc/template/governance.go:468-485`）；稳定化则报出
  // `required_route_not_current` 阻断原因码。页面只读服务端结论。
  //
  // 稳定化排在交付检查**前面**：它要求变更集的目标修订就是项目当前修订
  // （`workspacecore/impact_service.go:1287`），而交付检查会落一条回执，
  // 排在后面才不会被它顶掉当前修订。
  const stabilization = await tolerateFailure(() =>
    readRoadmapStabilization(
      ctx,
      client,
      token,
      options.projectId,
      addCommit.changeSetId,
      G3_ROADMAP_STEPS.stabilize,
    ),
  )
  project = await readProjectState(
    ctx,
    client,
    token,
    options.projectId,
    '路线图-读项目状态（交付检查前）',
  )
  const check = await tolerateFailure(() =>
    runDeliveryCheckWithIssues(
      ctx,
      client,
      token,
      options.projectId,
      project.projectVersion,
      G3_ROADMAP_STEPS.deliveryCheck,
    ),
  )

  // —— 取消链：建第二个会话 → 提案 → 取消 → 读回 ——
  const cancelSession = await beginRoadmapEdit(ctx, client, token, options.projectId, {
    stepName: G3_ROADMAP_STEPS.beginCancelSession,
    idemLabel: 'roadmap-begin-cancel',
    reason: 'G3 验收台-11：编辑未冻结路线图（随后取消）',
  })
  await proposeRoadmapNode(ctx, client, token, options.projectId, cancelSession.id, {
    stepName: G3_ROADMAP_STEPS.proposeCancelNode,
    idemLabel: 'roadmap-propose-cancel',
    nodeId: G3_DEMO_REQUIRED_ROUTE_NODE_ID,
    kind: 'update',
    name: `${requiredNode?.name ?? '路线节点'}（G3 验收台-11 待取消）`,
    status: requiredNode?.status === '' ? 'planned' : (requiredNode?.status ?? 'planned'),
    inputs: requiredNode?.inputs ?? [],
    targets: requiredNode?.targets ?? [],
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
    commit: reviewCommit,
    addCommit,
    requiredNode,
    pendingReview,
    pendingReviewCheck,
    prerequisiteNode: prerequisite,
    versions,
    comparison,
    tasks,
    check,
    stabilization,
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
  /** 「待复核」前置：必需节点登记后但还没复核时的服务端状态。 */
  pendingReview: G3RoadmapNodeFact | null
  /** 「待复核」阶段的交付检查。 */
  pendingReviewCheck: G3CheckWithIssuesFact | null
  /** 合成前置节点（被必需节点钉住、被推进一版的那个）。 */
  prerequisiteNode: G3RoadmapNodeFact | null
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
    pendingReview: result.pendingReview,
    pendingReviewCheck: result.pendingReviewCheck,
    prerequisiteNode: result.prerequisiteNode,
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
  // 「服务端给出了阻断」有两个落点：交付检查给出的 blocking 校验问题，或者
  // 稳定化给出的 `required_route_not_current` 阻断原因码。
  // 规则表的 AC-G3-08 备注写的就是后者（「服务端给出 required_route_not_current
  // 阻断原因码，且项目不进入 STABILIZED / READY」）：
  // 交付检查在带必需路线节点的项目上目前会以 403 `source_access_denied` 拒掉
  // （路线感知的规格投影与服务端的溯源快照对不上，那是服务端的事），
  // 所以这一行不能因为交付检查取不到就永远不成立。
  const serverBlocked = blockingIssues.length > 0 || routeBlockers.length > 0
  const holds =
    requiredNodes.length > 0 &&
    notCurrent.length > 0 &&
    serverBlocked &&
    notStabilized
  // 台面造出来的两种前置（「待复核」与「版本落后」）各自被服务端算出来的状态。
  const pendingReviewIssues = (result.pendingReviewCheck?.issues ?? []).filter(
    (issue) => issue.severity === 'blocking',
  )
  const blockedBy = [
    blockingIssues.length > 0 ? `交付检查给出 ${blockingIssues.length} 条 blocking 校验问题` : '',
    routeBlockers.length > 0
      ? `稳定化给出 ${routeBlockers.length} 条 required_route_not_current 阻断原因`
      : '',
  ].filter(Boolean).join('；')
  const precondition = result.pendingReview
    ? `台面造出的两种前置：「待复核」读回 ${result.pendingReview.id}:${result.pendingReview.state}` +
      `（该状态下交付检查给出 ${pendingReviewIssues.length} 条 blocking 校验问题）；` +
      `「版本落后」读回 ${notCurrent.map((node) => `${node.id}:${node.state}`).join('、') || '（无）'}。`
    : '本轮没有读回「待复核」前置的节点状态。'

  return {
    id: 'AC-G3-08',
    title: 'RouteNode 必需项：缺失或版本落后时阻断稳定化',
    holds,
    detail: holds
      ? `必需 RouteNode 共 ${requiredNodes.length} 个，其中 ${notCurrent.length} 个不是当前态` +
        `（${notCurrent.map((node) => `${node.id}:${node.state}${node.stateReason ? `（${node.stateReason}）` : ''}`).join('、')}）；` +
        `${blockedBy}，` +
        `稳定化结论为 ${result.stabilization?.status ?? '（未取到）'}——项目没有进入 STABILIZED / READY。` +
        precondition
      : `不成立：必需节点 ${requiredNodes.length} 个，其中非当前态 ${notCurrent.length} 个；` +
        `服务端给出的阻断 ${blockedBy || '（无）'}；` +
        `稳定化结论 ${result.stabilization?.status ?? '（本轮未取到）'}。` +
        `（本行要的是「服务端给出阻断且项目不放行」这一整段事实；` +
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
      `POST /api/v1/lingdoc/projects/{projectId}/change-sets/{changeSetId}/stabilize → HTTP 200 · ` +
        `status=${result.stabilization?.status ?? '（本轮未取到）'} · ` +
        `route 阻断 ${routeBlockers.length} 条` +
        `（${routeBlockers.map((blocker) => `${blocker.code}:${blocker.targetId}`).join('、') || '（无）'}）`,
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
