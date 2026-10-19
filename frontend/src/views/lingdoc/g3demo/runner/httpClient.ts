/**
 * G3 验收台 · 真实链执行器的 HTTP 客户端契约（纯 TS，与 Vue 无关）。
 *
 * 执行器**不自己发请求**：它只把「方法 / 路径 / 请求体 / 幂等键 / 头」描述清楚，
 * 由调用方注入一个 fetch 风格的客户端去执行。这样同一份执行器既能被浏览器页面
 * 调用（注入 axios 封装，复用既有请求拦截器与令牌存储），也能被 node 脚本调用
 * （注入 fetch）——真实证据不必依赖浏览器自动化。
 *
 * 幂等键策略也放在这里，因为它属于「怎么发请求」的约定：
 * - 所有 G3 写路由都要带 `Idempotency-Key`（8–128 字符，含本次运行标识）；
 * - `GET` 一律不带；
 * - `POST /checks` 与 `POST /template-checks` 契约未标幂等键，**不要**带
 *   （`internal/lingdoc/workspace/delivery_http.go:114-116`）；
 * - 非 G3 路由（认证、版本探针、系统信息）不在幂等键契约范围内，一律不带。
 */

/** 执行器用到的 HTTP 方法。 */
export type G3HttpMethod = 'GET' | 'POST' | 'PUT' | 'DELETE'

/** 一次真实调用的描述。 */
export interface G3HttpRequest {
  method: G3HttpMethod
  /** 完整路径（含 `/api/v1` 前缀）。 */
  path: string
  /** 请求体。`undefined` 表示不带体。 */
  body?: unknown
  /** Bearer 令牌。省略表示不带凭据。 */
  token?: string
  /** 幂等键。是否真的发出由 `shouldSendIdempotencyKey` 决定。 */
  idempotencyKey?: string
  /** 附加请求头。 */
  headers?: Record<string, string>
  /**
   * 明确要求**不带凭据**。
   *
   * 只给存活探针用：请求拦截器会给每个请求补上 `Authorization`，
   * 而探针要的就是「不带凭据时的 401 = 服务存活」这个信号。
   */
  anonymous?: boolean
  /**
   * 要**原始字节**，不要 JSON。
   *
   * 只给下载产物用：`GET /projects/{id}/exports/{exportId}/file` 是这一组里
   * 唯一一条不返回 JSON 信封的接口（二进制 + `Content-Disposition`），
   * 解析成 JSON 会把字节弄坏，产物哈希就核对不了了。
   */
  raw?: boolean
}

/** 一次真实调用的结果。 */
export interface G3HttpResponse {
  status: number
  /** 2xx 为真。 */
  ok: boolean
  /** 解析后的响应体（JSON）或原始文本。 */
  body: unknown
  /**
   * 原始字节。
   *
   * 只有 `raw: true` 的请求（下载产物）才有；其余响应为 `undefined`。
   */
  bytes?: Uint8Array
  /** 端到端耗时（毫秒）。 */
  durationMs: number
}

/** 注入式 HTTP 客户端。 */
export type G3HttpClient = (request: G3HttpRequest) => Promise<G3HttpResponse>

/** G3 路由前缀。幂等键契约只覆盖这个前缀下的路由。 */
export const G3_LINGDOC_PREFIX = '/api/v1/lingdoc'

/** 幂等键长度契约：8–128 字符（去空格后），不满足即 400。 */
export const G3_IDEMPOTENCY_MIN_LENGTH = 8
export const G3_IDEMPOTENCY_MAX_LENGTH = 128

/**
 * 契约未标幂等键的写路由。
 * `POST /projects/{id}/checks` 与 `POST /projects/{id}/template-checks` 不带键。
 */
export const G3_IDEMPOTENCY_EXEMPT_SUFFIXES = ['/checks', '/template-checks'] as const

/** 判断一个幂等键是否满足长度契约。 */
export function isValidIdempotencyKey(key: string): boolean {
  const trimmed = key.trim()
  return (
    trimmed.length >= G3_IDEMPOTENCY_MIN_LENGTH &&
    trimmed.length <= G3_IDEMPOTENCY_MAX_LENGTH
  )
}

/**
 * 判断这次调用**应不应该**带幂等键。
 *
 * 这是「发送前」的唯一判定点：调用方可以给出候选键，但最终发不发由这里说了算，
 * 免得某条路由「顺手带上」而把契约破坏掉。
 */
export function shouldSendIdempotencyKey(method: string, path: string): boolean {
  if (!path.startsWith(G3_LINGDOC_PREFIX)) return false
  if (method.toUpperCase() === 'GET') return false
  return !G3_IDEMPOTENCY_EXEMPT_SUFFIXES.some((suffix) => path.endsWith(suffix))
}

/** 从统一信封里取 `data`。不是信封就原样返回。 */
export function envelopeData(body: unknown): unknown {
  if (body && typeof body === 'object' && !Array.isArray(body) && 'data' in body) {
    return (body as { data: unknown }).data
  }
  return body
}

/** 取错误码。G3 信封是 `error.code`；认证接口的 `error` 是字符串。 */
export function errorCodeOf(body: unknown): string {
  if (!body || typeof body !== 'object') return ''
  const error = (body as { error?: unknown }).error
  if (typeof error === 'string') return error
  if (error && typeof error === 'object') {
    const code = (error as { code?: unknown }).code
    if (typeof code === 'string') return code
  }
  return ''
}

/** 取可读的错误信息。 */
export function errorMessageOf(body: unknown): string {
  if (typeof body === 'string') return body
  if (!body || typeof body !== 'object') return ''
  const error = (body as { error?: unknown }).error
  if (typeof error === 'string') return error
  if (error && typeof error === 'object') {
    const message = (error as { message?: unknown }).message
    if (typeof message === 'string') return message
  }
  const message = (body as { message?: unknown }).message
  return typeof message === 'string' ? message : ''
}

/**
 * 把一次失败翻译成**人读得懂**的中文说明。
 *
 * 认证失败与权限不足必须显式说出来——静默失败会让人以为链路只是慢。
 */
export function describeHttpFailure(status: number, body: unknown): string {
  const detail = errorMessageOf(body)
  const suffix = detail ? `（${detail}）` : ''
  if (status === 401) {
    return `认证失败：令牌无效、已过期或凭据不正确${suffix}`
  }
  if (status === 403) {
    return `权限不足：当前账号没有执行该操作的权限${suffix}`
  }
  if (status === 404) {
    return `资源不存在或对当前账号不可见${suffix}`
  }
  if (status === 409) {
    return `版本冲突或幂等键冲突：基线已漂移${suffix}`
  }
  if (status === 422) {
    return `请求被拒绝：运行时状态或资产授权不满足${suffix}`
  }
  if (status === 423) {
    return `运行时已暂停${suffix}`
  }
  return `请求失败（HTTP ${status}）${suffix}`
}

/** 链上抛出的错误：带步骤名、路径、状态码与错误码。 */
export class G3ChainError extends Error {
  readonly step: string
  readonly path: string
  readonly status: number
  readonly code: string

  constructor(
    step: string,
    path: string,
    status: number,
    code: string,
    message: string,
  ) {
    super(message)
    this.name = 'G3ChainError'
    this.step = step
    this.path = path
    this.status = status
    this.code = code
  }
}
