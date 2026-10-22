/**
 * G3 验收台 · 身份：注册 / 登录 / 读取当前身份。
 *
 * **演示账号由运行时自建**：本页不依赖任何预置账号，也不接受手工填入的固定口令。
 * 账号名、邮箱与口令都在运行时随机生成，注册成功后存到浏览器本地，
 * 于是**同一账号可以跨运行复用**（AC：每次运行新建项目，不复用历史项目）。
 *
 * 这里的函数不碰令牌存储：令牌由页面交给前端既有的认证存储（`weknora_token`），
 * 执行器只负责把令牌带在后续请求上——不另起一套平行鉴权。
 */

import { envelopeData, type G3HttpClient } from './httpClient'
import { runStep, type G3RunContext } from './runContext'

/** 运行时自建的演示账号。 */
export interface G3DemoAccount {
  username: string
  email: string
  password: string
}

/** 浏览器本地存储的最小接口（localStorage 天然满足）。 */
export interface G3AccountStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

/** 演示账号在本地存储里的键。 */
export const G3_ACCOUNT_STORAGE_KEY = 'g3demo_account'

/** 用注入的随机源生成十六进制串。 */
function randomHex(random: () => number, length: number): string {
  let out = ''
  while (out.length < length) {
    out += Math.floor(random() * 16).toString(16)
  }
  return out.slice(0, length)
}

/**
 * 造一个演示账号。
 *
 * 口令是随机生成的，**不是**写死的常量——写死的口令等于把凭据提交进仓库。
 */
export function createDemoAccount(random: () => number = Math.random): G3DemoAccount {
  const suffix = randomHex(random, 10)
  return {
    username: `impactCenterAcceptance-${suffix}`,
    email: `impactCenterAcceptance-${suffix}@example.com`,
    password: `G3-${randomHex(random, 24)}`,
  }
}

/** 读取本地保存的演示账号。 */
export function loadDemoAccount(storage: G3AccountStorage): G3DemoAccount | null {
  const raw = storage.getItem(G3_ACCOUNT_STORAGE_KEY)
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as Partial<G3DemoAccount>
    if (!parsed.username || !parsed.email || !parsed.password) return null
    return {
      username: parsed.username,
      email: parsed.email,
      password: parsed.password,
    }
  } catch {
    return null
  }
}

/** 保存演示账号，供后续运行复用。 */
export function saveDemoAccount(storage: G3AccountStorage, account: G3DemoAccount): void {
  storage.setItem(G3_ACCOUNT_STORAGE_KEY, JSON.stringify(account))
}

/** 注册结果。 */
export interface G3RegisteredIdentity {
  userId: string
  username: string
  email: string
  tenantId: string
}

/** 注册演示账号。 */
export async function registerAccount(
  ctx: G3RunContext,
  client: G3HttpClient,
  account: G3DemoAccount,
  options: { stepName?: string } = {},
): Promise<G3RegisteredIdentity> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName ?? '注册演示账号',
    method: 'POST',
    path: '/api/v1/auth/register',
    body: {
      username: account.username,
      email: account.email,
      password: account.password,
    },
    conclusion: `注册成功（HTTP {status}）：${account.email}`,
    detail: (body) => {
      const user = (body as { user?: Record<string, unknown> } | null)?.user
      if (!user) return undefined
      return {
        user_id: user.id,
        username: user.username,
        email: user.email,
        tenant_id: user.tenant_id,
      }
    },
  })

  const body = (data ?? {}) as { user?: Record<string, unknown> }
  const user = body.user ?? {}
  return {
    userId: String(user.id ?? ''),
    username: String(user.username ?? account.username),
    email: String(user.email ?? account.email),
    tenantId: String(user.tenant_id ?? ''),
  }
}

/** 登录并返回访问令牌。 */
export async function loginAccount(
  ctx: G3RunContext,
  client: G3HttpClient,
  account: G3DemoAccount,
  options: { stepName?: string } = {},
): Promise<string> {
  const { data } = await runStep(ctx, client, {
    name: options.stepName ?? '登录',
    method: 'POST',
    path: '/api/v1/auth/login',
    body: { email: account.email, password: account.password },
    conclusion: `登录成功（HTTP {status}）：${account.email}`,
    detail: (body) => {
      const payload = body as { user?: Record<string, unknown>; active_tenant?: Record<string, unknown> }
      return {
        user_id: payload?.user?.id,
        email: payload?.user?.email,
        tenant_id: payload?.active_tenant?.id ?? payload?.user?.tenant_id,
      }
    },
  })

  const body = (data ?? {}) as { token?: unknown }
  const token = typeof body.token === 'string' ? body.token : ''
  if (!token) {
    throw new Error('登录成功但没有拿到访问令牌')
  }
  return token
}

/** 当前身份。 */
export interface G3CurrentIdentity {
  userId: string
  username: string
  email: string
  tenantId: string
}

/** 读取当前身份（校验令牌并回读身份信息）。 */
export async function readCurrentIdentity(
  ctx: G3RunContext,
  client: G3HttpClient,
  token: string,
): Promise<G3CurrentIdentity> {
  const { response } = await runStep(ctx, client, {
    name: '读取当前身份',
    method: 'GET',
    path: '/api/v1/auth/validate',
    token,
    conclusion: '令牌有效（HTTP {status}）',
  })

  const body = envelopeData(response.body) as { user?: Record<string, unknown> } | null
  const user = body?.user ?? {}
  return {
    userId: String(user.id ?? ''),
    username: String(user.username ?? ''),
    email: String(user.email ?? ''),
    tenantId: String(user.tenant_id ?? ''),
  }
}
