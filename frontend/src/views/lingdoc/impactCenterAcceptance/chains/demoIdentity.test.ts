import assert from 'node:assert/strict'
import test from 'node:test'
import { createFakeClient } from './fakeClient'
import {
  G3_ACCOUNT_STORAGE_KEY,
  createDemoAccount,
  loadDemoAccount,
  loginAccount,
  readCurrentIdentity,
  registerAccount,
  saveDemoAccount,
  type G3AccountStorage,
} from './demoIdentity'
import { createRunContext } from './runContext'

function memoryStorage(): G3AccountStorage & { map: Map<string, string> } {
  const map = new Map<string, string>()
  return {
    map,
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => {
      map.set(key, value)
    },
  }
}

test('演示账号是运行时随机生成的，不是写死的', () => {
  const a = createDemoAccount(() => 0.1)
  const b = createDemoAccount(() => 0.9)
  assert.notEqual(a.username, b.username)
  assert.notEqual(a.password, b.password)
  assert.match(a.username, /^g3demo-[0-9a-f]+$/)
  assert.match(a.email, /^g3demo-[0-9a-f]+@example\.com$/)
  assert.ok(a.password.length >= 16, '口令过短')
  assert.ok(!a.password.includes('password'), '口令不应是固定字面量')
})

test('账号可以跨运行复用：存下来再读出来是一致的', () => {
  const storage = memoryStorage()
  const account = createDemoAccount()
  saveDemoAccount(storage, account)
  assert.ok(storage.map.get(G3_ACCOUNT_STORAGE_KEY))
  assert.deepEqual(loadDemoAccount(storage), account)
})

test('存储里没有或坏了就如实返回 null', () => {
  const storage = memoryStorage()
  assert.equal(loadDemoAccount(storage), null)
  storage.setItem(G3_ACCOUNT_STORAGE_KEY, '{ broken')
  assert.equal(loadDemoAccount(storage), null)
  storage.setItem(G3_ACCOUNT_STORAGE_KEY, JSON.stringify({ username: 'x' }))
  assert.equal(loadDemoAccount(storage), null)
})

test('注册后拿到用户与租户', async () => {
  const ctx = createRunContext({ runId: 'g3r-id-1', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  fake.reply('/api/v1/auth/register', {
    status: 201,
    body: { success: true, user: { id: 'u1', username: 'g3demo-x', email: 'a@example.com', tenant_id: 10055 } },
  })
  const account = createDemoAccount(() => 0.5)
  const identity = await registerAccount(ctx, fake.client, account)
  assert.equal(identity.userId, 'u1')
  assert.equal(identity.tenantId, '10055')
  assert.equal(ctx.steps[0].name, '注册演示账号')
  assert.equal(ctx.steps[0].status, 201)
})

test('登录拿到令牌', async () => {
  const ctx = createRunContext({ runId: 'g3r-id-2', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  fake.reply('/api/v1/auth/login', {
    status: 200,
    body: { success: true, token: 'jwt-1', user: { id: 'u1' } },
  })
  const account = createDemoAccount(() => 0.5)
  const token = await loginAccount(ctx, fake.client, account)
  assert.equal(token, 'jwt-1')
  assert.equal(ctx.steps[0].name, '登录')
})

test('登录成功但没有令牌就报错，不假装成功', async () => {
  const ctx = createRunContext({ runId: 'g3r-id-3', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  fake.reply('/api/v1/auth/login', { status: 200, body: { success: true } })
  const account = createDemoAccount(() => 0.5)
  await assert.rejects(() => loginAccount(ctx, fake.client, account), /没有拿到访问令牌/)
})

test('读当前身份回读用户信息', async () => {
  const ctx = createRunContext({ runId: 'g3r-id-4', accountEmail: 'a@example.com' })
  const fake = createFakeClient()
  fake.reply('/api/v1/auth/validate', {
    status: 200,
    body: { success: true, user: { id: 'u1', username: 'g3demo-x', email: 'a@example.com', tenant_id: 10055 } },
  })
  const identity = await readCurrentIdentity(ctx, fake.client, 'jwt-1')
  assert.equal(identity.userId, 'u1')
  assert.equal(identity.email, 'a@example.com')
  assert.equal(ctx.steps[0].method, 'GET')
  assert.equal(ctx.steps[0].idempotencyKey, undefined)
})
