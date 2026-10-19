/**
 * G3 验收台 · 浏览器侧的 HTTP 客户端适配。
 *
 * 它把执行器（纯 TS、要能被 node 脚本复用）接到前端**既有**的 axios 实例上：
 * 令牌、租户头、语言头、401 刷新重放，全部由 `src/utils/request.ts` 的拦截器负责，
 * 这里不新增任何平行鉴权。
 *
 * 唯一例外是**存活探针**：它必须不带凭据（未带凭据的 401 才是「服务存活」的信号），
 * 而请求拦截器会给每个请求补 `Authorization`。所以探针走一次裸 `fetch`——
 * 它本来就不该带凭据，这不是另一套鉴权。
 */

import { del, get, post, postNoBody, put } from '@/utils/request'
import type { G3HttpClient, G3HttpResponse } from './runner/httpClient'

/** 从响应体里读出状态码。axios 响应拦截器把它挂在非枚举属性 `$httpStatus` 上。 */
function statusOf(payload: unknown): number {
  if (payload && typeof payload === 'object' && '$httpStatus' in payload) {
    const value = (payload as { $httpStatus?: unknown }).$httpStatus
    if (typeof value === 'number') return value
  }
  return 200
}

/** 从异常里读出状态码。 */
function statusOfError(error: unknown): number {
  if (error && typeof error === 'object') {
    const direct = (error as { status?: unknown }).status
    if (typeof direct === 'number') return direct
    const response = (error as { response?: { status?: unknown } }).response
    if (response && typeof response.status === 'number') return response.status
  }
  return 0
}

/** 造一个走既有 axios 实例的客户端。 */
export function createRequestClient(): G3HttpClient {
  return async (request): Promise<G3HttpResponse> => {
    const started = performance.now()

    if (request.anonymous) {
      const response = await fetch(request.path, { method: request.method, headers: { Accept: 'application/json' } })
      const text = await response.text()
      let body: unknown = text
      try {
        body = text ? JSON.parse(text) : {}
      } catch {
        // 不是 JSON 就保留原文。
      }
      return {
        status: response.status,
        ok: response.ok,
        body,
        durationMs: performance.now() - started,
      }
    }

    const headers: Record<string, string> = { ...(request.headers ?? {}) }
    if (request.idempotencyKey) {
      headers['Idempotency-Key'] = request.idempotencyKey
    }
    const config = Object.keys(headers).length > 0 ? { headers } : undefined
    const method = request.method.toUpperCase()

    try {
      let payload: unknown
      if (method === 'GET') {
        payload = await get(request.path, config)
      } else if (method === 'POST') {
        // 请求体为 `undefined` 时**不发体**：`POST /change-sets/{id}/reassess`
        // 要求完全空体（服务端只读 1 字节，多 1 字节即 400）。
        payload = request.body === undefined
          ? await postNoBody(request.path, config)
          : await post(request.path, request.body as Record<string, unknown>, config)
      } else if (method === 'PUT') {
        payload = await put(request.path, request.body ?? {}, config)
      } else {
        payload = await del(request.path, request.body)
      }
      const status = statusOf(payload)
      return {
        status,
        ok: status >= 200 && status < 300,
        body: payload,
        durationMs: performance.now() - started,
      }
    } catch (error) {
      return {
        status: statusOfError(error),
        ok: false,
        body: error,
        durationMs: performance.now() - started,
      }
    }
  }
}
