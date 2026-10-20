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
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  G3_ROADMAP_STEPS,
  readRoadmapSessionFact,
  runRoadmapChain,
  verdictAcG308,
  verdictAcG316,
  type G3RoadmapChainResult,
  type G3RoadmapNodeFact,
} from './roadmap'
import { createFakeClient } from './fakeHttp'
import { createRunContext, type G3RunContext } from './runContext'

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

test('AC-G3-08：没有 blocking 校验问题就不能判成立', () => {
  const result = {
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

test('路线图链：真实 HTTP 面走完建会话 → 提案 → 提交 → 取消 → 读版本序列与前后比较', async () => {
  const fake = createFakeClient()
  const projectPath = '/api/v1/lingdoc/projects/p1'

  fake.reply(`${projectPath}`, {
    status: 200,
    body: {
      data: {
        id: 'p1',
        status: 'active',
        project_version: 3,
        spec_revision: 1,
        current_context_revision: 2,
        spec: { research_subject: 's' },
        template_id: 'template-demo',
        template_copy_version: 1,
        template_copy: { ruleset_hash: 'hash' },
      },
    },
  })
  fake.reply(`${projectPath}/roadmap`, {
    status: 200,
    body: {
      data: {
        version: 1,
        context_revision: 2,
        nodes: [
          {
            id: 'r7',
            project_id: 'p1',
            version: 1,
            name: '技术路线 R7',
            status: 'ready',
            required: true,
            state: 'current',
            context_revision: 2,
            inputs: [],
            targets: [],
            frozen: true,
          },
          {
            id: 'r8',
            project_id: 'p1',
            version: 1,
            name: '技术路线 R8',
            status: 'ready',
            required: false,
            state: 'current',
            context_revision: 2,
            inputs: [],
            targets: [],
            frozen: true,
          },
        ],
        edges: [],
      },
    },
  })
  fake.reply(`${projectPath}/roadmap/edits`, {
    status: 201,
    body: {
      data: {
        id: 'sess-1',
        project_id: 'p1',
        status: 'open',
        baseline: { version: 1, context_revision: 2, nodes: [], edges: [] },
        node_proposals: [],
        edge_proposals: [],
      },
    },
  })
  fake.reply(`${projectPath}/roadmap/edits/sess-1/nodes`, {
    status: 200,
    body: {
      data: {
        id: 'sess-1',
        project_id: 'p1',
        status: 'open',
        baseline: { version: 1, context_revision: 2, nodes: [], edges: [] },
        node_proposals: [
          { node_id: 'r7', kind: 'update', name: '技术路线 R7（改写）', status: 'ready', temporary_version: 2 },
        ],
        edge_proposals: [],
      },
    },
  })
  fake.replySequence(`${projectPath}/roadmap/edits/sess-1/commit`, [
    // 第一次提交：改写已有节点的文字 → 冻结路径（冻结一个新版本）。
    {
      status: 200,
      body: {
        data: {
          session_id: 'sess-1',
          baseline_version: 1,
          committed_version: 2,
          applied_nodes: [
            {
              id: 'r7',
              project_id: 'p1',
              version: 2,
              name: '技术路线 R7（改写）',
              status: 'ready',
              required: true,
              state: 'current',
              context_revision: 3,
              inputs: [],
              targets: [],
              frozen: true,
            },
          ],
          applied_edges: [],
          semantic: false,
        },
      },
    },
    // 第二次提交：新增节点 → 语义路径（服务端给的是变更集，路线图不被直接改写）。
    {
      status: 200,
      body: {
        data: {
          session_id: 'sess-1',
          baseline_version: 1,
          committed_version: 2,
          applied_nodes: [],
          applied_edges: [],
          change_set_id: 'cs-semantic-1',
          semantic: true,
        },
      },
    },
  ])
  fake.reply(`${projectPath}/roadmap/edits/sess-1`, {
    status: 200,
    body: {
      data: {
        id: 'sess-1',
        project_id: 'p1',
        status: 'committed',
        baseline: { version: 1, context_revision: 2, nodes: [], edges: [] },
        node_proposals: [],
        edge_proposals: [],
        result: { session_id: 'sess-1', baseline_version: 1, committed_version: 2 },
      },
    },
  })
  fake.reply(`${projectPath}/roadmap/nodes/r7/versions`, {
    status: 200,
    body: {
      data: [
        { version: 1, name: '技术路线 R7', status: 'ready', required: true, state: 'current', context_revision: 2 },
        { version: 2, name: '技术路线 R7（改写）', status: 'ready', required: true, state: 'current', context_revision: 3 },
      ],
    },
  })
  fake.reply(`${projectPath}/roadmap/nodes/r7/compare?from=1&to=2`, {
    status: 200,
    body: {
      data: {
        kind: 'RouteNode',
        id: 'r7',
        from: 1,
        to: 2,
        changes: [{ field: 'name', from: '技术路线 R7', to: '技术路线 R7（改写）' }],
      },
    },
  })
  fake.reply(`${projectPath}/roadmap/edits/sess-1/cancel`, {
    status: 200,
    body: {
      data: {
        id: 'sess-1',
        project_id: 'p1',
        status: 'cancelled',
        baseline: { version: 1, context_revision: 2, nodes: [], edges: [] },
        node_proposals: [],
        edge_proposals: [],
      },
    },
  })
  fake.reply(`${projectPath}/checks`, {
    status: 200,
    body: {
      data: {
        receipt_id: 'receipt-1',
        status: 'blocked',
        project_version: 3,
        issues: [
          {
            id: 'issue-1',
            rule_id: 'required-fields',
            severity: 'blocking',
            target_id: 'field/r7',
            message: 'required field is missing',
          },
        ],
      },
    },
  })
  fake.reply(`${projectPath}/impact-tasks`, {
    status: 200,
    body: {
      data: [
        {
          id: 'task-1',
          target: { kind: 'RouteNode', id: 'r7', version: '3' },
          status: 'STALE',
          stale_reason: 'cancelled_temporary_version',
          closure_evidence: [],
        },
      ],
    },
  })

  const ctx = ctxOf()
  const result = await runRoadmapChain(ctx, fake.client, 'jwt-1', {
    projectId: 'p1',
    operatorId: 'u1',
  })

  // 写路由必须带幂等键，GET 不带；`/checks` 是契约未标幂等键的例外。
  const writes = fake.calls.filter(
    (call) => call.method === 'POST' && !call.path.endsWith('/checks'),
  )
  assert.equal(writes.length > 0, true)
  for (const call of writes) {
    assert.equal(typeof call.idempotencyKey, 'string')
    assert.equal((call.idempotencyKey ?? '').length >= 8, true)
  }
  for (const call of fake.calls.filter((item) => item.method === 'GET')) {
    assert.equal(call.idempotencyKey, undefined)
  }
  // 契约未标幂等键的写路由一律不带键。
  for (const call of fake.calls.filter((item) => item.path.endsWith('/checks'))) {
    assert.equal(call.idempotencyKey, undefined)
  }

  assert.equal(result.commit.committedVersion, 2)
  assert.equal(result.commit.semantic, false)
  // 新增节点走语义路径：服务端给的是变更集，冻结路线图不被直接改写。
  assert.equal(result.addCommit.semantic, true)
  assert.equal(result.addCommit.changeSetId, 'cs-semantic-1')
  assert.equal(result.versions.length, 2)
  assert.equal(result.comparison?.changes.length, 1)
  assert.equal(result.comparison?.from, 1)
  assert.equal(result.comparison?.to, 2)
  assert.equal(result.cancelledSession.status, 'cancelled')
  assert.equal(result.verdicts.length, 2)
  // 判定挂到运行上下文上，矩阵规则表按 id 取结论。
  assert.deepEqual((ctx.verdicts ?? []).map((item) => item.id), ['AC-G3-08', 'AC-G3-16'])
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
