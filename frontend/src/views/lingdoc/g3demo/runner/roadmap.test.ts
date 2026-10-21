/**
 * G3 验收台-11（#295）· 路线图编辑会话链的单测。
 *
 * 这些断言盯住本票最容易出错的几个口径：
 * - 「必需 RouteNode 缺失或版本落后」读的是**服务端返回的**阻断原因与节点状态，
 *   页面不自行推算；
 * - 「取消未冻结编辑后回到上一个冻结版本」比对的是**冻结基线**，
 *   不是会话自己的字段；
 * - 临时版本（未被冻结的提案版本）不得出现在冻结版本序列里；
 * - 指向被取消临时版本的任务必须落 STALE。
 *
 * #318 补上的一条口径：**建会话用的上下文修订必须来自建会话那一刻的读数**。
 * 服务端把 `expected_context_revision` 当乐观并发闸门
 * （`internal/lingdoc/workspacecore/roadmap_service.go:60`），
 * 链首读到的旧值到建会话那一刻往往已经过期，于是整条链撞 409「基线已漂移」。
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  G3_ROADMAP_STEPS,
  readRoadmapSessionFact,
  runRoadmapChain,
  verdictAcG308,
  verdictAcG316,
  verdictG35,
  type G3RoadmapChainResult,
  type G3RoadmapNodeFact,
} from './roadmap'
import { G3_DEMO_REQUIRED_ROUTE_NODE_ID } from './project'
import { createFakeClient } from './fakeHttp'
import { createRunContext, type G3RunContext } from './runContext'
import type { G3HttpClient, G3HttpRequest, G3HttpResponse } from './httpClient'

function nodeFact(overrides: Partial<G3RoadmapNodeFact> = {}): G3RoadmapNodeFact {
  return {
    id: 'r7',
    projectId: 'p1',
    version: 1,
    name: '技术路线 R7',
    status: 'ready',
    required: true,
    state: 'current',
    stateReason: '',
    contextRevision: 1,
    inputs: [],
    targets: [],
    frozen: true,
    ...overrides,
  }
}

function ctxOf(): G3RunContext {
  return createRunContext({ runId: 'g3r-rm-1', accountEmail: 'g3demo@example.com' })
}

function baselineOf(nodes: G3RoadmapNodeFact[], version = 1): G3RoadmapChainResult['baselineBefore'] {
  return { version, contextRevision: 1, nodes, edges: [] }
}

/* ------------------------------------------------------------------ *
 * 一个照服务端规则应答的假路线图服务（#318）
 * ------------------------------------------------------------------ */

interface FakeRef {
  kind: string
  id: string
  version: string
}

interface FakeNode {
  id: string
  version: number
  name: string
  status: string
  inputs: FakeRef[]
  targets: FakeRef[]
  required: boolean
  contextRevision: number
}

interface FakeProposal {
  node_id: string
  kind: string
  name: string
  status: string
  inputs: FakeRef[]
  targets: FakeRef[]
  temporary_version: number
}

interface FakeSession {
  id: string
  status: string
  baselineVersion: number
  proposals: FakeProposal[]
}

interface FakeChangeSet {
  id: string
  status: string
  baseRevision: number
  targetRevision: number | null
  proposal: FakeProposal | null
}

/** 假服务的对外形状。 */
interface FakeRoadmapServer {
  client: G3HttpClient
  calls: G3HttpRequest[]
  /** 每次建会话时：服务端那一刻的修订号 vs 客户端发来的修订号。 */
  sessionCreations: { serverRevision: number; sentRevision: number }[]
  /** 冻结路线图里的节点（按 id）。 */
  nodes(): FakeNode[]
  /** 把项目上下文修订推进一格（模拟一次冻结路径提交的效果）。 */
  advanceRevision(): void
}

/**
 * 假路线图服务。
 *
 * 它照服务端的三条规则应答，缺一不可：
 * - 建会话的 `expected_context_revision` 必须等于**此刻**的项目上下文修订，
 *   否则回 409 `version_conflict`（`roadmap_service.go:60`）；
 * - **冻结路径**的提交把项目上下文修订推进一格（`roadmap_service.go:309-311`），
 *   语义路径的提交只产出一个变更集，改动要等变更集被应用才落进冻结路线图；
 * - 稳定化要求变更集已应用，且它的目标修订就是项目当前修订
 *   （`workspacecore/impact_service.go:1287`）。
 */
function createFakeRoadmapServer(): FakeRoadmapServer {
  const calls: G3HttpRequest[] = []
  const sessionCreations: { serverRevision: number; sentRevision: number }[] = []
  const state = {
    revision: 3,
    projectVersion: 7,
    roadmapVersion: 0,
    counter: 0,
    nodes: new Map<string, FakeNode>(),
    history: new Map<string, FakeNode[]>(),
    sessions: new Map<string, FakeSession>(),
    changeSets: new Map<string, FakeChangeSet>(),
  }

  const ok = (data: unknown, status = 200): G3HttpResponse => ({
    status,
    ok: true,
    body: { data },
    durationMs: 1,
  })
  const conflict = (): G3HttpResponse => ({
    status: 409,
    ok: false,
    body: { error: { code: 'version_conflict', message: '内容已变化，请先读取当前版本。' } },
    durationMs: 1,
  })

  /** 节点状态按服务端的口径算（`roadmap_service.go:995-1013`）。 */
  const stateOf = (node: FakeNode): { state: string; reason: string } => {
    if (node.status === 'retired') return { state: 'retired', reason: '节点已退出路线' }
    if (node.status !== 'ready') return { state: 'pending_review', reason: '节点尚未复核' }
    for (const ref of node.inputs) {
      const target = state.nodes.get(ref.id)
      if (!target) return { state: 'stale', reason: `引用的 ${ref.kind}/${ref.id} 当前不可读` }
      if (String(target.version) !== ref.version) {
        return {
          state: 'stale',
          reason: `引用的 ${ref.kind}/${ref.id} 已从 v${ref.version} 变为 v${target.version}`,
        }
      }
    }
    return { state: 'current', reason: '' }
  }

  const nodeView = (node: FakeNode): Record<string, unknown> => {
    const derived = stateOf(node)
    return {
      id: node.id,
      project_id: 'p1',
      version: node.version,
      name: node.name,
      status: node.status,
      required: node.required,
      state: derived.state,
      state_reason: derived.reason,
      context_revision: node.contextRevision,
      inputs: node.inputs,
      targets: node.targets,
      frozen: true,
    }
  }

  const sessionView = (session: FakeSession): Record<string, unknown> => ({
    id: session.id,
    project_id: 'p1',
    status: session.status,
    baseline: {
      version: session.baselineVersion,
      context_revision: state.revision,
      nodes: [],
      edges: [],
    },
    node_proposals: session.proposals,
    edge_proposals: [],
  })

  const changeSetView = (change: FakeChangeSet): Record<string, unknown> => ({
    id: change.id,
    project_id: 'p1',
    status: change.status,
    base_context_revision: change.baseRevision,
    target_context_revision: change.targetRevision,
  })

  const client: G3HttpClient = async (request: G3HttpRequest): Promise<G3HttpResponse> => {
    calls.push(request)
    const method = request.method.toUpperCase()
    const path = request.path
    const body = (request.body ?? {}) as Record<string, unknown>

    // GET /projects/p1
    if (method === 'GET' && path === '/api/v1/lingdoc/projects/p1') {
      return ok({
        id: 'p1',
        status: 'active',
        project_version: state.projectVersion,
        spec_revision: 1,
        current_context_revision: state.revision,
        spec: { research_subject: 's' },
        template_id: 'template-demo',
        template_copy_version: 2,
        template_copy: { ruleset_hash: 'hash' },
      })
    }

    // GET /projects/p1/roadmap
    if (method === 'GET' && path === '/api/v1/lingdoc/projects/p1/roadmap') {
      return ok({
        version: state.roadmapVersion,
        context_revision: state.revision,
        nodes: [...state.nodes.values()].map(nodeView),
        edges: [],
      })
    }

    // POST /projects/p1/roadmap/edits
    if (method === 'POST' && path === '/api/v1/lingdoc/projects/p1/roadmap/edits') {
      const sent = Number(body.expected_context_revision ?? 0)
      sessionCreations.push({ serverRevision: state.revision, sentRevision: sent })
      if (sent !== state.revision) return conflict()
      state.counter += 1
      const session: FakeSession = {
        id: `sess-${state.counter}`,
        status: 'open',
        baselineVersion: state.roadmapVersion,
        proposals: [],
      }
      state.sessions.set(session.id, session)
      return ok(sessionView(session), 201)
    }

    // POST /projects/p1/roadmap/edits/{id}/nodes
    const propose = /^\/api\/v1\/lingdoc\/projects\/p1\/roadmap\/edits\/([^/]+)\/nodes$/.exec(path)
    if (method === 'POST' && propose) {
      const session = state.sessions.get(propose[1])
      if (!session) return { status: 404, ok: false, body: {}, durationMs: 1 }
      const current = state.nodes.get(String(body.node_id ?? ''))
      const temporaryVersion = body.kind === 'add' ? 1 : (current?.version ?? 0) + 1
      session.proposals = [
        {
          node_id: String(body.node_id ?? ''),
          kind: String(body.kind ?? ''),
          name: String(body.name ?? ''),
          status: String(body.status ?? ''),
          inputs: Array.isArray(body.inputs) ? (body.inputs as FakeRef[]) : [],
          targets: Array.isArray(body.targets) ? (body.targets as FakeRef[]) : [],
          temporary_version: temporaryVersion,
        },
      ]
      return ok(sessionView(session))
    }

    // POST /projects/p1/roadmap/edits/{id}/commit
    const commit = /^\/api\/v1\/lingdoc\/projects\/p1\/roadmap\/edits\/([^/]+)\/commit$/.exec(path)
    if (method === 'POST' && commit) {
      const session = state.sessions.get(commit[1])
      if (!session) return { status: 404, ok: false, body: {}, durationMs: 1 }
      if (Number(body.expected_baseline_version) !== session.baselineVersion) return conflict()
      const proposal = session.proposals[0]
      if (!proposal) return { status: 422, ok: false, body: {}, durationMs: 1 }
      const current = state.nodes.get(proposal.node_id)
      const before = (current?.inputs ?? []).map((ref) => ref.id).sort().join('|')
      const after = proposal.inputs.map((ref) => ref.id).sort().join('|')
      const semantic = proposal.kind === 'add' || proposal.kind === 'delete' || before !== after
      session.status = 'committed'
      state.roadmapVersion += 1
      if (semantic) {
        // 语义路径：路线图不被直接改写，提交只产出一个变更集。
        state.counter += 1
        const change: FakeChangeSet = {
          id: `cs-${state.counter}`,
          status: 'draft',
          baseRevision: state.revision,
          targetRevision: null,
          proposal,
        }
        state.changeSets.set(change.id, change)
        return ok({
          session_id: session.id,
          baseline_version: session.baselineVersion,
          committed_version: session.baselineVersion + 1,
          applied_nodes: [],
          applied_edges: [],
          change_set_id: change.id,
          semantic: true,
        })
      }
      // 冻结路径：提交当场冻结一个新版本，并把上下文修订推进一格。
      const next: FakeNode = {
        ...(current as FakeNode),
        version: current!.version + 1,
        name: proposal.name,
        status: proposal.status,
        inputs: proposal.inputs,
        targets: proposal.targets,
        contextRevision: state.revision,
      }
      state.nodes.set(next.id, next)
      const history = state.history.get(next.id) ?? []
      history.push(next)
      state.history.set(next.id, history)
      state.revision += 1
      state.projectVersion += 1
      return ok({
        session_id: session.id,
        baseline_version: session.baselineVersion,
        committed_version: session.baselineVersion + 1,
        applied_nodes: [nodeView(next)],
        applied_edges: [],
        semantic: false,
      })
    }

    // POST /projects/p1/roadmap/edits/{id}/cancel
    const cancel = /^\/api\/v1\/lingdoc\/projects\/p1\/roadmap\/edits\/([^/]+)\/cancel$/.exec(path)
    if (method === 'POST' && cancel) {
      const session = state.sessions.get(cancel[1])
      if (!session) return { status: 404, ok: false, body: {}, durationMs: 1 }
      if (Number(body.expected_baseline_version) !== session.baselineVersion) return conflict()
      session.status = 'cancelled'
      return ok(sessionView(session))
    }

    // GET /projects/p1/roadmap/edits/{id}
    const readSession = /^\/api\/v1\/lingdoc\/projects\/p1\/roadmap\/edits\/([^/]+)$/.exec(path)
    if (method === 'GET' && readSession) {
      const session = state.sessions.get(readSession[1])
      if (!session) return { status: 404, ok: false, body: {}, durationMs: 1 }
      return ok(sessionView(session))
    }

    // GET /projects/p1/roadmap/nodes/{id}/versions
    const versions = /^\/api\/v1\/lingdoc\/projects\/p1\/roadmap\/nodes\/([^/]+)\/versions$/.exec(path)
    if (method === 'GET' && versions) {
      const history = state.history.get(versions[1]) ?? []
      return ok(
        history.map((node) => ({
          version: node.version,
          name: node.name,
          status: node.status,
          required: node.required,
          state: stateOf(node).state,
          state_reason: stateOf(node).reason,
          context_revision: node.contextRevision,
        })),
      )
    }

    // GET /projects/p1/roadmap/nodes/{id}/compare?from=&to=
    const compare = /^\/api\/v1\/lingdoc\/projects\/p1\/roadmap\/nodes\/([^/]+)\/compare/.exec(path)
    if (method === 'GET' && compare) {
      const history = state.history.get(compare[1]) ?? []
      const first = history[0]
      const last = history[history.length - 1]
      return ok({
        kind: 'RouteNode',
        id: compare[1],
        from: first?.version ?? 0,
        to: last?.version ?? 0,
        changes: first && last && first.name !== last.name
          ? [{ field: 'name', from: first.name, to: last.name }]
          : [],
      })
    }

    // POST /projects/p1/change-sets/{id}/assess
    const assess = /^\/api\/v1\/lingdoc\/projects\/p1\/change-sets\/([^/]+)\/assess$/.exec(path)
    if (method === 'POST' && assess) {
      const change = state.changeSets.get(assess[1])
      if (!change) return { status: 404, ok: false, body: {}, durationMs: 1 }
      if (change.baseRevision !== state.revision) return conflict()
      change.status = 'assessed'
      return ok(changeSetView(change))
    }

    // POST /projects/p1/change-sets/{id}/apply
    const apply = /^\/api\/v1\/lingdoc\/projects\/p1\/change-sets\/([^/]+)\/apply$/.exec(path)
    if (method === 'POST' && apply) {
      const change = state.changeSets.get(apply[1])
      if (!change) return { status: 404, ok: false, body: {}, durationMs: 1 }
      if (change.baseRevision !== state.revision) return conflict()
      change.status = 'applied'
      change.targetRevision = state.revision + 1
      state.revision += 1
      state.projectVersion += 1
      const proposal = change.proposal
      if (proposal) {
        if (proposal.kind === 'add') {
          const created: FakeNode = {
            id: proposal.node_id,
            version: 1,
            name: proposal.name,
            status: proposal.status,
            inputs: proposal.inputs,
            targets: proposal.targets,
            required: proposal.node_id === G3_DEMO_REQUIRED_ROUTE_NODE_ID,
            contextRevision: state.revision,
          }
          state.nodes.set(created.id, created)
          state.history.set(created.id, [created])
        } else if (proposal.kind === 'update') {
          const current = state.nodes.get(proposal.node_id)
          if (current) {
            const next: FakeNode = {
              ...current,
              version: current.version + 1,
              name: proposal.name,
              status: proposal.status,
              inputs: proposal.inputs,
              targets: proposal.targets,
              contextRevision: state.revision,
            }
            state.nodes.set(next.id, next)
            const history = state.history.get(next.id) ?? []
            history.push(next)
            state.history.set(next.id, history)
          }
        }
      }
      return ok(changeSetView(change))
    }

    // POST /projects/p1/change-sets/{id}/stabilize
    const stabilize = /^\/api\/v1\/lingdoc\/projects\/p1\/change-sets\/([^/]+)\/stabilize$/.exec(path)
    if (method === 'POST' && stabilize) {
      const change = state.changeSets.get(stabilize[1])
      if (!change) return { status: 404, ok: false, body: {}, durationMs: 1 }
      if (change.status !== 'applied' || change.targetRevision !== state.revision) return conflict()
      const required = state.nodes.get(G3_DEMO_REQUIRED_ROUTE_NODE_ID)
      const blockers = required && stateOf(required).state !== 'current'
        ? [
            {
              code: 'required_route_not_current',
              target_id: G3_DEMO_REQUIRED_ROUTE_NODE_ID,
              reason: '必经技术路线未就绪、未复核或依赖版本已变化',
            },
          ]
        : []
      return ok({
        change_set_id: change.id,
        context_revision: state.revision,
        status: blockers.length > 0 ? 'BLOCKED' : 'STABILIZED',
        blockers,
        stale_task_ids: [],
        checked_at: '2026-10-21T00:00:00Z',
      })
    }

    // GET /projects/p1/impact-tasks
    if (method === 'GET' && path === '/api/v1/lingdoc/projects/p1/impact-tasks') {
      return ok([])
    }

    // POST /projects/p1/checks
    if (method === 'POST' && path === '/api/v1/lingdoc/projects/p1/checks') {
      if (Number(body.expected_project_version) !== state.projectVersion) return conflict()
      const required = state.nodes.get(G3_DEMO_REQUIRED_ROUTE_NODE_ID)
      const issues = required && stateOf(required).state !== 'current'
        ? [
            {
              id: 'issue-route-r7',
              rule_id: 'route-node-required',
              severity: 'blocking',
              target_id: `field/${G3_DEMO_REQUIRED_ROUTE_NODE_ID}`,
              target_version: String(state.projectVersion),
              message: '必经技术路线未就绪、未复核或依赖版本已变化',
            },
          ]
        : []
      return ok({
        receipt_id: 'receipt-1',
        status: issues.length > 0 ? 'blocked' : 'passed',
        project_version: state.projectVersion,
        issues,
      })
    }

    return { status: 404, ok: false, body: { error: { code: 'not_found' } }, durationMs: 1 }
  }

  return {
    client,
    calls,
    sessionCreations,
    nodes: () => [...state.nodes.values()],
    advanceRevision: () => {
      state.revision += 1
    },
  }
}

/* ------------------------------------------------------------------ *
 * 单元判定
 * ------------------------------------------------------------------ */

test('会话事实按服务端 json tag 读：临时版本号是提案自己的版本', () => {
  const session = readRoadmapSessionFact({
    id: 'sess-1',
    project_id: 'p1',
    status: 'open',
    baseline: { version: 2, context_revision: 3, nodes: [], edges: [] },
    node_proposals: [
      { node_id: 'r7', kind: 'update', name: '新路线', status: 'ready', temporary_version: 4 },
    ],
    edge_proposals: [
      { edge_id: 'e1', kind: 'add', source_id: 'r7', target_id: 'r8', relation: 'rel', temporary_version: 1 },
    ],
  })

  assert.equal(session.id, 'sess-1')
  assert.equal(session.status, 'open')
  assert.equal(session.baseline.version, 2)
  assert.equal(session.baseline.contextRevision, 3)
  assert.equal(session.nodeProposals[0].temporaryVersion, 4)
  assert.equal(session.edgeProposals[0].temporaryVersion, 1)
  assert.equal(session.result, null)
})

test('AC-G3-16：取消后回到上一个冻结版本、临时版本被释放、指向它的任务落 STALE 时成立', () => {
  const frozen = nodeFact({ version: 2, name: '技术路线 R7（已提交）' })
  const result = {
    baselineBefore: baselineOf([nodeFact()]),
    baselineAfterCommit: baselineOf([frozen], 1),
    baselineAfterCancel: baselineOf([frozen], 1),
    committedSession: { status: 'committed' },
    cancelledSession: {
      status: 'cancelled',
      nodeProposals: [{ nodeId: 'r7', kind: 'update', name: '', status: '', temporaryVersion: 3 }],
      edgeProposals: [],
    },
    commit: {},
    versions: [{ version: 1 }, { version: 2 }],
    comparison: null,
    tasks: [
      {
        id: 'task-1',
        target: { kind: 'RouteNode', id: 'r7', version: '3' },
        status: 'STALE',
        staleReason: 'cancelled_temporary_version',
        closureEvidence: [],
      },
    ],
    stabilization: null,
    temporaryRefs: [{ kind: 'RouteNode', id: 'r7', version: '3' }],
    project: {},
  } as unknown as G3RoadmapChainResult

  const verdict = verdictAcG316(result)
  assert.equal(verdict.holds, true)
  assert.equal(verdict.id, 'AC-G3-16')
  assert.equal(verdict.status, 'PASS')
})

test('AC-G3-16：取消后基线版本没有回去就不成立', () => {
  const result = {
    baselineBefore: baselineOf([nodeFact()]),
    baselineAfterCommit: baselineOf([nodeFact({ version: 2 })], 1),
    baselineAfterCancel: baselineOf([nodeFact({ version: 3 })], 2),
    committedSession: { status: 'committed' },
    cancelledSession: {
      status: 'cancelled',
      nodeProposals: [{ nodeId: 'r7', kind: 'update', name: '', status: '', temporaryVersion: 4 }],
      edgeProposals: [],
    },
    commit: {},
    versions: [{ version: 1 }, { version: 2 }],
    comparison: null,
    tasks: [],
    stabilization: null,
    temporaryRefs: [{ kind: 'RouteNode', id: 'r7', version: '4' }],
    project: {},
  } as unknown as G3RoadmapChainResult

  const verdict = verdictAcG316(result)
  assert.equal(verdict.holds, false)
  assert.equal(verdict.status, 'NOT RUN')
})

test('AC-G3-16：指向临时版本的任务不是 STALE 时不成立', () => {
  const frozen = nodeFact({ version: 2 })
  const result = {
    baselineBefore: baselineOf([nodeFact()]),
    baselineAfterCommit: baselineOf([frozen], 1),
    baselineAfterCancel: baselineOf([frozen], 1),
    committedSession: { status: 'committed' },
    cancelledSession: {
      status: 'cancelled',
      nodeProposals: [{ nodeId: 'r7', kind: 'update', name: '', status: '', temporaryVersion: 3 }],
      edgeProposals: [],
    },
    commit: {},
    versions: [{ version: 1 }, { version: 2 }],
    comparison: null,
    tasks: [
      {
        id: 'task-1',
        target: { kind: 'RouteNode', id: 'r7', version: '3' },
        status: 'OPEN',
        staleReason: '',
        closureEvidence: [],
      },
    ],
    stabilization: null,
    temporaryRefs: [{ kind: 'RouteNode', id: 'r7', version: '3' }],
    project: {},
  } as unknown as G3RoadmapChainResult

  assert.equal(verdictAcG316(result).holds, false)
})

test('AC-G3-08：必需节点非当前态 + 服务端给出 blocking 校验问题 + 未稳定化时成立', () => {
  const result = {
    baselineAfterCancel: baselineOf([nodeFact({ state: 'stale', stateReason: '版本落后' })]),
    versions: [{ version: 1, state: 'stale' }],
    check: {
      status: 'blocked',
      issues: [
        { ruleId: 'required-fields', severity: 'blocking', targetId: 'field/r7', message: 'required field is missing' },
      ],
    },
    stabilization: {
      status: 'BLOCKED',
      blockers: [
        {
          code: 'required_route_not_current',
          targetId: 'r7',
          reason: '必经技术路线未就绪、未复核或依赖版本已变化',
        },
      ],
    },
  } as unknown as G3RoadmapChainResult

  const verdict = verdictAcG308(result)
  assert.equal(verdict.holds, true)
  assert.equal(verdict.id, 'AC-G3-08')
  assert.equal(verdict.status, 'PASS')
})

/** 造一份「可比较 / 可恢复 / 必需项不满足时阻断」的路线图链事实。 */
function g35ResultOf(overrides: Partial<G3RoadmapChainResult> = {}): G3RoadmapChainResult {
  const frozen = nodeFact({ version: 2, state: 'stale', stateReason: '版本落后' })
  const base = {
    baselineBefore: baselineOf([nodeFact()]),
    baselineAfterCommit: baselineOf([frozen], 1),
    baselineAfterCancel: baselineOf([frozen], 1),
    committedSession: { status: 'committed' },
    cancelledSession: { status: 'cancelled', nodeProposals: [], edgeProposals: [] },
    versions: [{ version: 1, state: 'current' }, { version: 2, state: 'stale' }],
    comparison: { kind: 'RouteNode', id: 'r7', from: 1, to: 2, changes: [{ field: 'name', from: '旧', to: '新' }] },
    check: {
      status: 'blocked',
      issues: [{ ruleId: 'required-route', severity: 'blocking', targetId: 'route/r7', message: '必经路线未就绪' }],
    },
    stabilization: {
      status: 'BLOCKED',
      blockers: [{ code: 'required_route_not_current', targetId: 'r7', reason: '必经技术路线未就绪' }],
    },
  } as unknown as G3RoadmapChainResult
  return { ...base, ...overrides } as G3RoadmapChainResult
}

test('G3.5：可比较 / 可恢复 / 必需项不满足时阻断都成立时判定成立（覆盖受限记 WARNING）', () => {
  const verdict = verdictG35(g35ResultOf())
  assert.equal(verdict.id, 'G3.5')
  assert.equal(verdict.holds, true)
  // 「必需项已声明但缺失」那一种前置本轮没有落点 ⇒ 覆盖受限，记 WARNING。
  assert.equal(verdict.status, 'WARNING')
  assert.match(verdict.detail, /覆盖受限/)
  assert.match(verdict.detail, /缺失/)
})

test('G3.5：读不出前后比较就不成立（可比较没观测到）', () => {
  const broken = verdictG35(g35ResultOf({ comparison: null } as Partial<G3RoadmapChainResult>))
  assert.equal(broken.holds, false)
  assert.equal(broken.status, 'NOT RUN')
  assert.match(broken.detail, /可比较=false/)
})

test('G3.5：取消编辑后基线没有回去就不成立（可恢复没观测到）', () => {
  const broken = verdictG35(
    g35ResultOf({
      baselineAfterCancel: baselineOf([nodeFact({ version: 3, state: 'stale' })], 2),
    } as Partial<G3RoadmapChainResult>),
  )
  assert.equal(broken.holds, false)
  assert.match(broken.detail, /可恢复=false/)
})

test('G3.5：服务端没有给出阻断就不成立', () => {
  const broken = verdictG35(
    g35ResultOf({
      check: { status: 'passed', issues: [] },
      stabilization: { status: 'STABILIZED', blockers: [] },
    } as unknown as Partial<G3RoadmapChainResult>),
  )
  assert.equal(broken.holds, false)
  assert.match(broken.detail, /必需项阻断=false/)
})

test('AC-G3-08：没有 blocking 校验问题就不能判成立', () => {  const result = {
    baselineAfterCancel: baselineOf([nodeFact({ state: 'stale' })]),
    versions: [{ version: 1, state: 'stale' }],
    check: { status: 'blocked', issues: [] },
    stabilization: { status: 'BLOCKED', blockers: [] },
  } as unknown as G3RoadmapChainResult

  const verdict = verdictAcG308(result)
  assert.equal(verdict.holds, false)
  assert.equal(verdict.status, 'NOT RUN')
})

test('AC-G3-08：稳定化已放行时不成立（项目进入了 STABILIZED）', () => {
  const result = {
    baselineAfterCancel: baselineOf([nodeFact({ state: 'stale' })]),
    versions: [{ version: 1, state: 'stale' }],
    check: {
      status: 'blocked',
      issues: [
        { ruleId: 'required-fields', severity: 'blocking', targetId: 'field/r7', message: 'x' },
      ],
    },
    stabilization: {
      status: 'STABILIZED',
      blockers: [
        { code: 'required_route_not_current', targetId: 'r7', reason: 'x' },
      ],
    },
  } as unknown as G3RoadmapChainResult

  assert.equal(verdictAcG308(result).holds, false)
})

test('AC-G3-08：交付检查取不到时，服务端给出的 required_route_not_current 阻断也算「服务端给出了阻断」', () => {
  const result = {
    baselineAfterCancel: baselineOf([nodeFact({ state: 'stale' })]),
    versions: [{ version: 1, state: 'stale' }],
    check: null,
    stabilization: {
      status: 'BLOCKED',
      blockers: [
        {
          code: 'required_route_not_current',
          targetId: 'r7',
          reason: '必经技术路线未就绪、未复核或依赖版本已变化',
        },
      ],
    },
  } as unknown as G3RoadmapChainResult

  const verdict = verdictAcG308(result)
  assert.equal(verdict.holds, true)
  assert.match(verdict.detail, /required_route_not_current/)
})

/* ------------------------------------------------------------------ *
 * 整链（#318）
 * ------------------------------------------------------------------ */

test('建会话用的上下文修订来自建会话那一刻的读数（不是链首的旧值）', async () => {
  const server = createFakeRoadmapServer()
  const ctx = ctxOf()
  const { beginRoadmapEdit, currentContextRevisionOf } = await import('./roadmap')

  const first = await currentContextRevisionOf(ctx, server.client, 'jwt-1', 'p1')
  assert.equal(first, 3)

  // 链里发生了一次推进（冻结路径的提交就是这样的效果）：修订前进一格。
  server.advanceRevision()
  const second = await currentContextRevisionOf(ctx, server.client, 'jwt-1', 'p1')
  assert.equal(second, 4, '上下文修订必须现读现用：读两次要读到两个值')

  // 建会话带上的是**此刻**的修订号。
  await beginRoadmapEdit(ctx, server.client, 'jwt-1', 'p1', {
    stepName: G3_ROADMAP_STEPS.beginSession,
    idemLabel: 'roadmap-begin-revision',
    reason: 'G3 验收台-11：建会话（修订号用例）',
  })
  const creation = server.sessionCreations[server.sessionCreations.length - 1]
  assert.equal(creation?.sentRevision, 4)
  assert.equal(creation?.serverRevision, 4)
  const body = server.calls[server.calls.length - 1].body as { expected_context_revision: number }
  assert.equal(body.expected_context_revision, 4)
})

test('路线图链：建会话用**建会话那一刻**的上下文修订，链内不再撞 409', async () => {
  const server = createFakeRoadmapServer()
  const ctx = ctxOf()

  const result = await runRoadmapChain(ctx, server.client, 'jwt-1', {
    projectId: 'p1',
    operatorId: 'u1',
  })

  // 每次建会话带的修订号都等于服务端那一刻的修订号：链内发生过漂移也不撞 409。
  assert.equal(server.sessionCreations.length >= 4, true)
  for (const creation of server.sessionCreations) {
    assert.equal(
      creation.sentRevision,
      creation.serverRevision,
      `建会话带了过期修订号：服务端 ${creation.serverRevision}，客户端 ${creation.sentRevision}`,
    )
  }
  // 链里确实发生过上下文修订漂移（冻结路径的提交推进了修订），否则这条断言是空的。
  const revisions = server.sessionCreations.map((creation) => creation.serverRevision)
  assert.equal(new Set(revisions).size > 1, true, '链内没有发生过修订漂移，这条用例证明不了任何事')

  // 「待复核」与「版本落后」两种前置都被服务端算出来并读回。
  assert.equal(result.pendingReview?.state, 'pending_review')
  assert.equal(result.requiredNode?.state, 'stale')
  assert.equal(result.requiredNode?.id, G3_DEMO_REQUIRED_ROUTE_NODE_ID)

  // 服务端给出了阻断，且项目没有放行。
  assert.equal(result.stabilization?.status, 'BLOCKED')
  assert.equal(
    (result.stabilization?.blockers ?? []).some((blocker) => blocker.code === 'required_route_not_current'),
    true,
  )
  assert.equal(
    (result.check?.issues ?? []).filter((issue) => issue.severity === 'blocking').length > 0 ||
      (result.stabilization?.blockers ?? []).length > 0,
    true,
  )

  // 必需节点自己的版本史与前后比较都读得到（AC-G3-08 要求这两段事实）。
  assert.equal(result.versions.length >= 2, true)
  assert.equal(result.comparison?.from, 1)
  assert.equal(result.comparison?.to, 2)

  // 稳定化与交付检查这两步都在本轮记录里，且都成功。
  for (const name of [G3_ROADMAP_STEPS.deliveryCheck, G3_ROADMAP_STEPS.stabilize]) {
    const step = ctx.steps.find((item) => item.name === name)
    assert.equal(step?.ok, true, `步骤「${name}」没有成功记录`)
  }

  // 三条判定都在台面上跑出来（AC-G3-08 / AC-G3-16 / G3.5 切片行）。
  const byId = new Map(result.verdicts.map((verdict) => [verdict.id, verdict]))
  assert.equal(byId.get('AC-G3-08')?.holds, true)
  assert.equal(byId.get('AC-G3-16')?.holds, true)
  assert.deepEqual((ctx.verdicts ?? []).map((item) => item.id), ['AC-G3-08', 'AC-G3-16', 'G3.5'])
})

test('路线图链：写路由的人工确认守卫被拒时，执行器给出可读的权限说明', async () => {
  const fake = createFakeClient()
  const ctx = ctxOf()
  const { proposeRoadmapNode } = await import('./roadmap')

  fake.reply('/api/v1/lingdoc/projects/p1/roadmap/edits/sess-1/nodes', {
    status: 403,
    body: { error: { code: 'human_decision_required', message: '需要人工决策' } },
  })

  await assert.rejects(
    () =>
      proposeRoadmapNode(ctx, fake.client, 'jwt-1', 'p1', 'sess-1', {
        stepName: G3_ROADMAP_STEPS.proposeNode,
        idemLabel: 'roadmap-propose-node',
        nodeId: 'r7',
        kind: 'update',
        name: 'x',
        status: 'ready',
        reason: 'x',
      }),
    /权限不足/,
  )
  // 失败的调用**要留下记录**：失败也要有证据，不能让人以为什么都没发生。
  assert.equal(ctx.steps.length, 1)
  assert.equal(ctx.steps[0].ok, false)
  assert.equal(ctx.steps[0].status, 403)
})

test('路线图链：写路由缺幂等键时执行器拒绝发出请求（不产生半发请求）', async () => {
  const fake = createFakeClient()
  const ctx = ctxOf()
  const { runStep } = await import('./runContext')

  // 幂等键策略是**发送前**的唯一判定点：写路由缺键就直接拒绝发出去。
  await assert.rejects(
    () =>
      runStep(ctx, fake.client, {
        name: '路线图-提案节点（缺幂等键）',
        method: 'POST',
        path: '/api/v1/lingdoc/projects/p1/roadmap/edits/sess-1/nodes',
        token: 'jwt-1',
        body: { node_id: 'r7' },
      }),
    /幂等键/,
  )
  assert.equal(fake.calls.length, 0)
})
