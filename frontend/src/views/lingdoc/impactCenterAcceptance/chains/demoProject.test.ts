import assert from 'node:assert/strict'
import test from 'node:test'
import { createFakeClient } from './fakeClient'
import { createRunContext } from './runContext'
import {
  G3_DEMO_REQUIRED_SECTIONS,
  missingRequiredSections,
  runProjectChain,
} from './demoProject'

function seedReplies(fake: ReturnType<typeof createFakeClient>, projectId: string): void {
  fake.reply('/api/v1/lingdoc/projects', {
    status: 201,
    body: {
      data: {
        id: projectId,
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
  fake.reply(`/api/v1/lingdoc/projects/${projectId}/spec`, {
    status: 200,
    body: {
      data: {
        id: projectId,
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
  fake.reply(`/api/v1/lingdoc/projects/${projectId}/activation-read`, {
    status: 201,
    body: { data: { id: 'read-1', project_version: 2, spec_revision: 1 } },
  })
  fake.reply(`/api/v1/lingdoc/projects/${projectId}/activate`, {
    status: 200,
    body: {
      data: {
        id: projectId,
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
  fake.reply(`/api/v1/lingdoc/projects/${projectId}/chapters`, {
    status: 200,
    body: {
      data: [
        { id: 'c-q', section_id: 'question', title: '研究问题', current_version_id: null, body_markdown: '' },
        { id: 'c-m', section_id: 'method', title: '研究方案', current_version_id: null, body_markdown: '' },
      ],
    },
  })
}

test('项目链按 建项 → 写规格 → 立项阅读 → 激活 → 读章节 的顺序调用', async () => {
  const ctx = createRunContext({ runId: 'g3r-p-2', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  seedReplies(fake, 'p1')

  const { project, chapters } = await runProjectChain(ctx, fake.client, 'token-1', {
    projectName: 'demo-project',
  })

  assert.deepEqual(
    ctx.steps.map((step) => step.name),
    ['建项', '写规格', '登记立项阅读', '激活', '读章节列表'],
  )
  assert.deepEqual(
    ctx.steps.map((step) => step.method),
    ['POST', 'PUT', 'POST', 'POST', 'GET'],
  )
  assert.equal(project.status, 'active')
  assert.equal(project.currentContextRevision, 1)
  assert.equal(chapters.length, 2)
  assert.deepEqual(missingRequiredSections(chapters), [])
  assert.equal(ctx.projectId, 'p1')
})

test('写路由都带含运行标识的幂等键，读路由不带', async () => {
  const ctx = createRunContext({ runId: 'g3r-p-3', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  seedReplies(fake, 'p1')
  await runProjectChain(ctx, fake.client, 'token-1', { projectName: 'demo-project' })

  for (const step of ctx.steps) {
    if (step.method === 'GET') {
      assert.equal(step.idempotencyKey, undefined, `${step.name} 不该带幂等键`)
    } else {
      assert.ok(step.idempotencyKey, `${step.name} 缺幂等键`)
      assert.ok(step.idempotencyKey.includes(ctx.runId), `${step.name} 的幂等键未含运行标识`)
      assert.ok(step.idempotencyKey.length >= 8 && step.idempotencyKey.length <= 128)
    }
  }
})

test('每次运行都新建项目：项目名带运行标识', async () => {
  const ctx = createRunContext({ runId: 'g3r-p-4', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  seedReplies(fake, 'p1')
  await runProjectChain(ctx, fake.client, 'token-1', { projectName: `demo-${ctx.runId}` })
  const createCall = fake.calls[0]
  const body = createCall.body as { name: string }
  assert.equal(body.name, `demo-${ctx.runId}`)
})

test('激活必须带 activation_read_id', async () => {
  const ctx = createRunContext({ runId: 'g3r-p-5', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  seedReplies(fake, 'p1')
  await runProjectChain(ctx, fake.client, 'token-1', { projectName: 'demo-project' })
  const activateCall = fake.calls.find((call) => call.path.endsWith('/activate'))
  assert.ok(activateCall)
  const body = activateCall.body as Record<string, unknown>
  assert.equal(body.activation_read_id, 'read-1')
  assert.equal(body.expected_project_version, 2)
  assert.equal(body.reviewed_project_version, 2)
})

test('必需章节没就位就如实列出来', () => {
  const missing = missingRequiredSections([
    { id: 'c-q', sectionId: 'question', title: '研究问题', currentVersionId: null, bodyEmpty: true },
  ])
  assert.deepEqual(missing, ['method'])
  assert.deepEqual([...G3_DEMO_REQUIRED_SECTIONS], ['question', 'method'])
})
