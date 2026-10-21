/**
 * G3 验收台-11（#295）· 受控故障注入可观测口径的单测。
 *
 * 这些断言盯住本票最关键的一条口径：
 * - **注入是夹具，不是端点**：台面只能从 apply 的真实响应里判断后端是否装配了注入；
 * - 装配了 → 回滚证据完整才判 PASS；
 * - **未装配 → 记 NOT RUN，不得判 PASS**，并指向夹具证据文件。
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  G3_FAULT_FIXTURE_EVIDENCE,
  G3_INJECTED_FAULT_CODE,
  G3_INJECTED_FAULT_STATUS,
  detectFaultInjection,
  runFaultInjectionChain,
  sameProjectSnapshot,
  verdictAcG303,
  type G3FaultChainResult,
  type G3FaultProjectSnapshot,
} from './faultInjection'
import { createFakeClient } from './fakeHttp'
import { createRunContext, type G3RunContext } from './runContext'

function snapshotOf(overrides: Partial<G3FaultProjectSnapshot> = {}): G3FaultProjectSnapshot {
  return {
    projectVersion: 3,
    specRevision: 1,
    currentContextRevision: 2,
    templateId: 'template-demo',
    templateCopyVersion: 1,
    rulesetHash: 'hash',
    ...overrides,
  }
}

function ctxOf(): G3RunContext {
  return createRunContext({ runId: 'g3r-fi-1', accountEmail: 'g3demo@example.com' })
}

function resultOf(overrides: Partial<G3FaultChainResult['observation']>): G3FaultChainResult {
  return {
    projectBefore: snapshotOf(),
    projectAfter: snapshotOf(),
    tasksBefore: { count: 1, ids: ['task-1'] },
    tasksAfter: { count: 1, ids: ['task-1'] },
    auditBefore: { count: 2, actions: ['project.change_set.create'] },
    auditAfter: { count: 2, actions: ['project.change_set.create'] },
    changeSet: {} as G3FaultChainResult['changeSet'],
    assessment: {} as G3FaultChainResult['assessment'],
    observation: {
      status: 503,
      code: G3_INJECTED_FAULT_CODE,
      faultInjectionAssembled: true,
      changeSetStatus: 'assessed',
      projectUnchanged: true,
      tasksUnchanged: true,
      auditUnchanged: true,
      ...overrides,
    },
    verdicts: [],
  }
}

test('注入命中口径：503 + injected_fault 才是注入', () => {
  assert.equal(detectFaultInjection(503, 'injected_fault'), true)
  assert.equal(detectFaultInjection(503, 'other_code'), false)
  assert.equal(detectFaultInjection(500, 'injected_fault'), false)
  assert.equal(detectFaultInjection(200, ''), false)
})

test('项目快照比对：revision 与模板引用逐项相同才算未变', () => {
  assert.equal(sameProjectSnapshot(snapshotOf(), snapshotOf()), true)
  assert.equal(
    sameProjectSnapshot(snapshotOf(), snapshotOf({ currentContextRevision: 3 })),
    false,
  )
  assert.equal(
    sameProjectSnapshot(snapshotOf(), snapshotOf({ templateCopyVersion: 2 })),
    false,
  )
})

test('AC-G3-03：装配了注入且回滚证据完整时判定成立', () => {
  const verdict = verdictAcG303(resultOf({}))
  assert.equal(verdict.holds, true)
  assert.equal(verdict.id, 'AC-G3-03')
  assert.equal(verdict.status, 'PASS')
})

test('AC-G3-03：回滚证据不完整时不成立', () => {
  const verdict = verdictAcG303(resultOf({ tasksUnchanged: false }))
  assert.equal(verdict.holds, false)
  assert.equal(verdict.status, 'NOT RUN')
})

test('AC-G3-03：未装配注入时记 NOT RUN、不得判 PASS，并指向夹具证据文件', () => {
  const verdict = verdictAcG303(
    resultOf({ status: 200, code: '', faultInjectionAssembled: false }),
  )
  assert.equal(verdict.holds, false)
  assert.equal(verdict.status, 'NOT RUN')
  assert.equal(verdict.detail.includes(G3_FAULT_FIXTURE_EVIDENCE), true)
  assert.equal(verdict.detail.includes('不是产品端点'), true)
  assert.equal(verdict.evidence.some((line) => line.includes(G3_FAULT_FIXTURE_EVIDENCE)), true)
})

test('故障注入链：装配了注入时 apply 返回 503 + injected_fault，读回证明未变', async () => {
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
  fake.reply(`${projectPath}/impact-tasks`, {
    status: 200,
    body: { data: [{ id: 'task-1', target: { kind: 'ProjectSpec', id: 'p1', version: '1' } }] },
  })
  fake.reply(`${projectPath}/audit`, {
    status: 200,
    body: { data: [{ id: 'e1', action: 'project.change_set.create' }] },
  })
  fake.reply(`${projectPath}/change-sets`, {
    status: 201,
    body: {
      data: {
        id: 'cs-1',
        project_id: 'p1',
        status: 'assessed',
        base_context_revision: 2,
        base_spec_revision: 1,
        fields: [],
        impacts: [],
      },
    },
  })
  fake.reply(`${projectPath}/change-sets/cs-1/assess`, {
    status: 200,
    body: { data: { id: 'cs-1', status: 'assessed' } },
  })
  fake.reply(`${projectPath}/change-sets/cs-1/impact-assessment`, {
    status: 200,
    body: {
      data: {
        change_set_id: 'cs-1',
        project_id: 'p1',
        context_revision: 2,
        input_hash: 'h',
        objects: [],
        impacts: [],
        edges: [],
      },
    },
  })
  fake.reply(`${projectPath}/change-sets/cs-1/apply`, {
    status: 503,
    body: {
      error: {
        code: 'injected_fault',
        message: '受控故障注入已生效（impact_task_write）：演示。本次操作整体未生效，状态保持原样。',
      },
    },
  })
  fake.reply(`${projectPath}/change-sets/cs-1`, {
    status: 200,
    body: { data: { id: 'cs-1', project_id: 'p1', status: 'assessed', fields: [], impacts: [] } },
  })

  const ctx = ctxOf()
  const result = await runFaultInjectionChain(ctx, fake.client, 'jwt-1', {
    projectId: 'p1',
  })

  assert.equal(result.observation.faultInjectionAssembled, true)
  assert.equal(result.observation.status, G3_INJECTED_FAULT_STATUS)
  assert.equal(result.observation.code, G3_INJECTED_FAULT_CODE)
  assert.equal(result.observation.projectUnchanged, true)
  assert.equal(result.observation.tasksUnchanged, true)
  assert.equal(result.observation.auditUnchanged, true)
  assert.equal(result.verdicts.length, 1)
  assert.equal(result.verdicts[0].id, 'AC-G3-03')
  assert.equal(result.verdicts[0].holds, true)
})

test('故障注入链：未装配注入时 apply 成功，本轮记 NOT RUN 而不是 PASS', async () => {
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
  fake.reply(`${projectPath}/impact-tasks`, { status: 200, body: { data: [] } })
  fake.reply(`${projectPath}/audit`, { status: 200, body: { data: [] } })
  fake.reply(`${projectPath}/change-sets`, {
    status: 201,
    body: {
      data: {
        id: 'cs-1',
        project_id: 'p1',
        status: 'assessed',
        base_context_revision: 2,
        base_spec_revision: 1,
        fields: [],
        impacts: [],
      },
    },
  })
  fake.reply(`${projectPath}/change-sets/cs-1/assess`, {
    status: 200,
    body: { data: { id: 'cs-1', status: 'assessed' } },
  })
  fake.reply(`${projectPath}/change-sets/cs-1/impact-assessment`, {
    status: 200,
    body: {
      data: {
        change_set_id: 'cs-1',
        project_id: 'p1',
        context_revision: 2,
        input_hash: 'h',
        objects: [],
        impacts: [],
        edges: [],
      },
    },
  })
  fake.reply(`${projectPath}/change-sets/cs-1/apply`, {
    status: 200,
    body: { data: { id: 'cs-1', status: 'applied' } },
  })
  fake.reply(`${projectPath}/change-sets/cs-1`, {
    status: 200,
    body: { data: { id: 'cs-1', project_id: 'p1', status: 'applied', fields: [], impacts: [] } },
  })

  const ctx = ctxOf()
  const result = await runFaultInjectionChain(ctx, fake.client, 'jwt-1', { projectId: 'p1' })

  assert.equal(result.observation.faultInjectionAssembled, false)
  assert.equal(result.verdicts[0].holds, false)
  assert.equal(result.verdicts[0].status, 'NOT RUN')
  assert.equal(result.verdicts[0].detail.includes(G3_FAULT_FIXTURE_EVIDENCE), true)
})
