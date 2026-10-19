/**
 * G3 验收台 · node 脚本用的 fetch 风格 HTTP 客户端。
 *
 * 与浏览器侧适配器（`g3HttpAdapter.ts`）走的是同一份执行器契约：
 * 方法 / 路径 / 请求体 / 幂等键 / 令牌，两边语义一致，证据不会走样。
 *
 * **请求体为 `undefined` 时不发体**——`POST /change-sets/{id}/reassess`
 * 要求完全空体（服务端只读 1 字节，多 1 字节即 400）。
 */

import type { G3HttpClient, G3HttpRequest, G3HttpResponse } from '../../src/views/lingdoc/g3demo/runner/httpClient'

/** 造一个打向真实后端的客户端。 */
export function createFetchClient(baseUrl: string): G3HttpClient {
  return async (request: G3HttpRequest): Promise<G3HttpResponse> => {
    const started = performance.now()
    const headers: Record<string, string> = { Accept: 'application/json' }
    if (request.body !== undefined) headers['Content-Type'] = 'application/json'
    if (request.token && !request.anonymous) headers.Authorization = `Bearer ${request.token}`
    if (request.idempotencyKey) headers['Idempotency-Key'] = request.idempotencyKey
    for (const [name, value] of Object.entries(request.headers ?? {})) {
      headers[name] = value
    }

    const response = await fetch(baseUrl + request.path, {
      method: request.method,
      headers,
      body: request.body === undefined ? undefined : JSON.stringify(request.body),
    })
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
      durationMs: Math.round(performance.now() - started),
    }
  }
}
