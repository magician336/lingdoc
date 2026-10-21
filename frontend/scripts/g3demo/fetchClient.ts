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

/**
 * 单个请求的超时（毫秒）。
 *
 * 没有超时的话，一个**挂住**的请求会把整轮实跑永久卡死——表现为「脚本不报错也不
 * 结束」，看上去像跑得慢，实际上什么结论都没有。带上超时之后，挂住会变成一次
 * 可读的失败：链路把它记成「本条链没有跑完：<真实原因>」，其余链路照跑。
 */
const REQUEST_TIMEOUT_MS = 60_000

/**
 * 造一个打向真实后端的客户端。
 *
 * 超时**不往外抛**：抛出去的话，异常会绕过步骤记录，实跑里只留下一句
 * 「本条链没有跑完」，看不出是服务端挂住。返回一次可读的失败，链路就能把
 * 「请求超时：服务端在 N 秒内没有返回」记进证据。
 */
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

    let response: Response
    try {
      response = await fetch(baseUrl + request.path, {
        method: request.method,
        headers,
        body: request.body === undefined ? undefined : JSON.stringify(request.body),
        signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
      })
    } catch (error) {
      const timeout = (error as { name?: unknown } | null)?.name === 'TimeoutError'
      return {
        status: 0,
        ok: false,
        body: {
          error: timeout
            ? `请求超时：服务端在 ${REQUEST_TIMEOUT_MS / 1000} 秒内没有返回。`
            : `请求失败：${(error as { message?: unknown } | null)?.message ?? String(error)}`,
        },
        durationMs: Math.round(performance.now() - started),
      }
    }
    // 下载产物：这条接口不返回 JSON 信封，只能按字节读。
    if (request.raw) {
      const buffer = await response.arrayBuffer()
      return {
        status: response.status,
        ok: response.ok,
        body: new Uint8Array(buffer),
        bytes: new Uint8Array(buffer),
        durationMs: Math.round(performance.now() - started),
      }
    }
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
