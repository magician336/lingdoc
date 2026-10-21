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
import { isSuccessStatus } from './runner/factHelpers'

/**
 * 单个请求的超时（毫秒）。
 *
 * 没有超时的话，一个**挂住**的请求会把整轮实跑永久卡死：页面上表现为「点了没反应」，
 * 看上去像跑得慢，实际上什么结论都没有。带上超时之后，挂住会变成一次可读的失败——
 * 链路把它记成「本条链没有跑完：请求超时」，其余链路照跑。
 *
 * 口径与 node 脚本那份客户端一致（`frontend/scripts/g3demo/fetchClient.ts`）：
 * 同一份执行器契约，两边的超时口径不能不一样。
 */
const REQUEST_TIMEOUT_MS = 60_000

/** 读出「这一次是超时」的判定。axios 报 ECONNABORTED/ETIMEDOUT，fetch 报 TimeoutError。 */
function isTimeoutError(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const code = (error as { code?: unknown }).code
  if (code === 'ECONNABORTED' || code === 'ETIMEDOUT') return true
  return (error as { name?: unknown }).name === 'TimeoutError'
}

/** 从响应体里读出状态码。axios 响应拦截器把它挂在非枚举属性 `$httpStatus` 上。 */
function statusOf(payload: unknown): number {
  if (payload && typeof payload === 'object' && '$httpStatus' in payload) {
    const value = (payload as { $httpStatus?: unknown }).$httpStatus
    if (typeof value === 'number') return value
  }
  return 200
}

/** 把 axios 的二进制响应体读成字节。 */
function toBytes(payload: unknown): Uint8Array | undefined {
  if (payload instanceof ArrayBuffer) return new Uint8Array(payload)
  if (ArrayBuffer.isView(payload)) {
    return new Uint8Array(payload.buffer, payload.byteOffset, payload.byteLength)
  }
  return undefined
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
      const response = await fetch(request.path, {
        method: request.method,
        headers: { Accept: 'application/json' },
        signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
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
        durationMs: performance.now() - started,
      }
    }

    const headers: Record<string, string> = { ...(request.headers ?? {}) }
    if (request.idempotencyKey) {
      headers['Idempotency-Key'] = request.idempotencyKey
    }
    const config = Object.keys(headers).length > 0
      ? { headers, timeout: REQUEST_TIMEOUT_MS }
      : { timeout: REQUEST_TIMEOUT_MS }
    const method = request.method.toUpperCase()

    try {
      let payload: unknown
      if (method === 'GET') {
        // 下载产物：这条接口返回二进制（docx），必须按字节读，
        // 让 axios 解析成 JSON 会把字节弄坏，产物哈希就核对不了。
        payload = request.raw
          ? await get(request.path, { responseType: 'arraybuffer' })
          : await get(request.path, config)
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
        ok: isSuccessStatus(status),
        body: payload,
        bytes: request.raw ? toBytes(payload) : undefined,
        durationMs: performance.now() - started,
      }
    } catch (error) {
      // 超时要有一句人读得懂的话：原始的 axios/fetch 异常在证据里只会显示
      // 「请求失败（HTTP 0）」，看不出是「服务端挂住」还是「网络断了」。
      if (isTimeoutError(error)) {
        return {
          status: 0,
          ok: false,
          body: { error: `请求超时：服务端在 ${REQUEST_TIMEOUT_MS / 1000} 秒内没有返回。` },
          durationMs: performance.now() - started,
        }
      }
      return {
        status: statusOfError(error),
        ok: false,
        body: error,
        durationMs: performance.now() - started,
      }
    }
  }
}
