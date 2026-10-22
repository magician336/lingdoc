import assert from 'node:assert/strict'
import test from 'node:test'
import { runChain } from './chainExecution'
import { createFakeClient } from './fakeClient'
import { createDemoAccount } from './demoIdentity'

function seedHappyPath(fake: ReturnType<typeof createFakeClient>): void {
  fake.reply('/api/v1/version', { status: 401, body: { error: 'Unauthorized: missing authentication' } })
  fake.reply('/api/v1/system/info', {
    status: 200,
    body: { code: 0, data: { commit_id: '7f5fd8c', build_time: '2026-10-19 17:42:56 UTC' } },
  })
  fake.reply('/api/v1/auth/register', {
    status: 201,
    body: { success: true, user: { id: 'u1', username: 'impactCenterAcceptance-x', email: 'impactCenterAcceptance-x@example.com', tenant_id: 10055 } },
  })
  fake.reply('/api/v1/auth/login', {
    status: 200,
    body: { success: true, token: 'jwt-1', user: { id: 'u1' }, active_tenant: { id: 10055 } },
  })
  fake.reply('/api/v1/auth/validate', {
    status: 200,
    body: { success: true, user: { id: 'u1', username: 'impactCenterAcceptance-x', email: 'impactCenterAcceptance-x@example.com', tenant_id: 10055 } },
  })
  fake.reply('/api/v1/lingdoc/projects', {
    status: 201,
    body: {
      data: {
        id: 'p1',
        name: 'demo',
        status: 'draft',
        project_version: 1,
        spec_revision: 0,
        current_context_revision: 0,
        template_id: 'template-demo',
        template_version: '1',
      },
    },
  })
  fake.reply('/api/v1/lingdoc/projects/p1/spec', {
    status: 200,
    body: {
      data: {
        id: 'p1',
        name: 'demo',
        status: 'draft',
        project_version: 2,
        spec_revision: 1,
        current_context_revision: 0,
        template_id: 'template-demo',
        template_version: '1',
      },
    },
  })
  fake.reply('/api/v1/lingdoc/projects/p1/activation-read', {
    status: 201,
    body: { data: { id: 'read-1', project_version: 2, spec_revision: 1 } },
  })
  fake.reply('/api/v1/lingdoc/projects/p1/activate', {
    status: 200,
    body: {
      data: {
        id: 'p1',
        name: 'demo',
        status: 'active',
        project_version: 3,
        spec_revision: 1,
        current_context_revision: 1,
        template_id: 'template-demo',
        template_version: '1',
      },
    },
  })
  fake.reply('/api/v1/lingdoc/projects/p1/chapters', {
    status: 200,
    body: {
      data: [
        { id: 'c-q', section_id: 'question', title: '研究问题', current_version_id: null, body_markdown: '' },
        { id: 'c-m', section_id: 'method', title: '研究方案', current_version_id: null, body_markdown: '' },
      ],
    },
  })
}

test('整条链的步骤顺序固定', async () => {
  const fake = createFakeClient()
  seedHappyPath(fake)
  const account = createDemoAccount(() => 0.5)
  const result = await runChain(fake.client, { account, registerFirst: true, runId: 'g3r-c-1' })

  assert.deepEqual(
    result.ctx.steps.map((step) => step.name),
    [
      '后端存活探针',
      '注册演示账号',
      '登录',
      '读取当前身份',
      '读取后端 head',
      '建项',
      '写规格',
      '登记立项阅读',
      '激活',
      '读章节列表',
    ],
  )
  assert.equal(result.ctx.head?.commitId, '7f5fd8c')
  assert.equal(result.project.status, 'active')
  assert.deepEqual(result.missingSections, [])
  assert.equal(result.chapters.length, 2)
})

test('已注册的账号直接登录，不再注册', async () => {
  const fake = createFakeClient()
  seedHappyPath(fake)
  const account = createDemoAccount(() => 0.5)
  const result = await runChain(fake.client, { account, registerFirst: false, runId: 'g3r-c-2' })
  assert.equal(result.ctx.steps.some((step) => step.name === '注册演示账号'), false)
  assert.equal(result.token, 'jwt-1')
})

test('认证失败会抛出可读错误，并留下失败步骤', async () => {
  const fake = createFakeClient()
  seedHappyPath(fake)
  fake.reply('/api/v1/auth/login', { status: 401, body: { error: 'invalid credentials' } })
  const account = createDemoAccount(() => 0.5)
  await assert.rejects(
    () => runChain(fake.client, { account, registerFirst: false, runId: 'g3r-c-3' }),
    /认证失败/,
  )
})

test('权限不足会抛出可读错误', async () => {
  const fake = createFakeClient()
  seedHappyPath(fake)
  fake.reply('/api/v1/lingdoc/projects/p1/activate', {
    status: 403,
    body: { error: { code: 'human_decision_required', message: '需要人工决策' } },
  })
  const account = createDemoAccount(() => 0.5)
  await assert.rejects(
    () => runChain(fake.client, { account, registerFirst: false, runId: 'g3r-c-4' }),
    /权限不足/,
  )
})
