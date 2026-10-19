import assert from 'node:assert/strict'
import test from 'node:test'
import { G3ChainError, isValidIdempotencyKey } from './httpClient'
import { createFakeClient } from './fakeHttp'
import {
  createRunContext,
  createRunId,
  headsDiffer,
  makeIdempotencyKey,
  markRunStale,
  probeBackendAlive,
  readBackendHead,
  runStep,
} from './runContext'

test('运行标识唯一且长度够做幂等键前缀', () => {
  const a = createRunId(1_700_000_000_000, () => 0.1)
  const b = createRunId(1_700_000_000_000, () => 0.9)
  assert.notEqual(a, b)
  assert.ok(a.length >= 8, `运行标识过短：${a}`)
  assert.match(a, /^g3r-/)
})

test('幂等键含本次运行标识且长度合规', () => {
  const runId = createRunId()
  const key = makeIdempotencyKey(runId, 'create-project')
  assert.ok(key.includes(runId), `幂等键未含运行标识：${key}`)
  assert.ok(isValidIdempotencyKey(key), `幂等键长度不合规：${key}`)
})

test('幂等键超长时截断标签部分，但保住运行标识', () => {
  const runId = createRunId()
  const key = makeIdempotencyKey(runId, 'x'.repeat(400))
  assert.ok(isValidIdempotencyKey(key), `截断后仍不合规：${key.length}`)
  assert.ok(key.includes(runId), '截断后丢了运行标识')
})

test('写路由缺幂等键就拒绝发请求', async () => {
  const ctx = createRunContext({ runId: 'g3r-test-1', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  await assert.rejects(
    () => runStep(ctx, fake.client, {
      name: '建项',
      method: 'POST',
      path: '/api/v1/lingdoc/projects',
    }),
    (error: unknown) => error instanceof G3ChainError && error.code === 'missing_idempotency_key',
  )
  assert.equal(fake.calls.length, 0, '缺键的请求不应发出去')
})

test('GET 不带幂等键，即使调用方给了', async () => {
  const ctx = createRunContext({ runId: 'g3r-test-2', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  fake.reply('/api/v1/lingdoc/projects/p1/chapters', { status: 200, body: { data: [] } })
  await runStep(ctx, fake.client, {
    name: '读章节列表',
    method: 'GET',
    path: '/api/v1/lingdoc/projects/p1/chapters',
    idempotencyKey: makeIdempotencyKey(ctx.runId, 'chapters'),
  })
  assert.equal(fake.calls[0].idempotencyKey, undefined)
})

test('契约未标幂等键的路由即使给了键也不发', async () => {
  const ctx = createRunContext({ runId: 'g3r-test-3', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  fake.reply('/api/v1/lingdoc/projects/p1/checks', { status: 200, body: { data: {} } })
  await runStep(ctx, fake.client, {
    name: '交付检查',
    method: 'POST',
    path: '/api/v1/lingdoc/projects/p1/checks',
    idempotencyKey: makeIdempotencyKey(ctx.runId, 'checks'),
  })
  assert.equal(fake.calls[0].idempotencyKey, undefined)
})

test('每一步都留下结构化记录', async () => {
  const ctx = createRunContext({ runId: 'g3r-test-4', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  fake.reply('/api/v1/lingdoc/projects', { status: 201, body: { data: { id: 'p1' } } })
  await runStep(ctx, fake.client, {
    name: '建项',
    method: 'POST',
    path: '/api/v1/lingdoc/projects',
    idempotencyKey: makeIdempotencyKey(ctx.runId, 'create-project'),
    conclusion: '项目已建立（HTTP {status}）',
  })
  assert.equal(ctx.steps.length, 1)
  const step = ctx.steps[0]
  assert.equal(step.index, 1)
  assert.equal(step.name, '建项')
  assert.equal(step.method, 'POST')
  assert.equal(step.path, '/api/v1/lingdoc/projects')
  assert.equal(step.status, 201)
  assert.equal(step.ok, true)
  assert.equal(step.durationMs, 1)
  assert.match(step.conclusion, /201/)
  assert.ok(step.idempotencyKey?.includes(ctx.runId))
})

test('失败的步骤先记录再抛可读错误', async () => {
  const ctx = createRunContext({ runId: 'g3r-test-5', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  fake.reply('/api/v1/auth/login', { status: 401, body: { error: 'invalid credentials' } })
  await assert.rejects(
    () => runStep(ctx, fake.client, { name: '登录', method: 'POST', path: '/api/v1/auth/login' }),
    (error: unknown) => error instanceof G3ChainError && /认证失败/.test(error.message),
  )
  assert.equal(ctx.steps.length, 1)
  assert.equal(ctx.steps[0].ok, false)
  assert.equal(ctx.steps[0].status, 401)
  assert.match(ctx.steps[0].error ?? '', /认证失败/)
})

test('存活探针的 401 是允许的失败，不抛错', async () => {
  const ctx = createRunContext({ runId: 'g3r-test-6', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  fake.reply('/api/v1/version', { status: 401, body: { error: 'Unauthorized: missing authentication' } })
  const status = await probeBackendAlive(ctx, fake.client)
  assert.equal(status, 401)
  assert.equal(ctx.steps.length, 1)
  assert.equal(ctx.steps[0].ok, false)
  // 探针的 401 是「服务存活」的信号，不能写成「认证失败」。
  assert.match(ctx.steps[0].conclusion, /服务存活/)
  assert.doesNotMatch(ctx.steps[0].conclusion, /认证失败/)
  assert.equal(ctx.steps[0].error, undefined)
})

test('head 读数来自 system/info 的 commit_id', async () => {
  const ctx = createRunContext({ runId: 'g3r-test-7', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  fake.reply('/api/v1/version', { status: 401, body: {} })
  fake.reply('/api/v1/system/info', {
    status: 200,
    body: { code: 0, data: { commit_id: '7f5fd8c', build_time: '2026-10-19 17:42:56 UTC' } },
  })
  await probeBackendAlive(ctx, fake.client)
  const head = await readBackendHead(ctx, fake.client, 'token-1')
  assert.ok(head)
  assert.equal(head.commitId, '7f5fd8c')
  assert.equal(head.probeStatus, 401)
  assert.equal(ctx.head?.commitId, '7f5fd8c')
})

test('head 读不到时如实返回 null，不编一个 head 出来', async () => {
  const ctx = createRunContext({ runId: 'g3r-test-8', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  fake.reply('/api/v1/system/info', { status: 403, body: { error: { message: 'forbidden' } } })
  const head = await readBackendHead(ctx, fake.client, 'token-1')
  assert.equal(head, null)
  assert.equal(ctx.head, null)
})

test('head 变化才判过期；head 未知不判', () => {
  const base = { commitId: '7f5fd8c', probedAt: '', probePath: '/api/v1/version', probeStatus: 401 }
  assert.equal(headsDiffer(base, { ...base }), false)
  assert.equal(headsDiffer(base, { ...base, commitId: 'af18497' }), true)
  assert.equal(headsDiffer(null, { ...base }), false)
  assert.equal(headsDiffer({ ...base }, null), false)
})

test('标记过期会写下原因', () => {
  const ctx = createRunContext({ runId: 'g3r-test-9', accountEmail: 'a@example.com' })
  markRunStale(ctx, '后端 head 从 7f5fd8c 变为 af18497')
  assert.equal(ctx.stale, true)
  assert.match(ctx.staleReason ?? '', /af18497/)
})
