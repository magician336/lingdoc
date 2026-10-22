/**
 * G3 验收台 · 事实读取的共用小工具。
 *
 * 这些小工具此前在多个 runner 模块里**逐字复制**（`str` / `num` / `bool` /
 * `objectOf` / `refOf` / `projectPath`）。复制版本的语义完全一致，因此这里
 * 收成一份：调用方改为 import，行为不变。
 *
 * 语义约定（与各模块原先的本地实现逐字一致）：
 * - `str`：非字符串一律读成空串；
 * - `num`：不可解析成有限数的值一律读成 0；
 * - `bool`：只有严格的 `true` 才是真；
 * - `objectOf`：非对象（含数组）一律读成空对象；
 * - `refOf`：把对象引用读成 `{ kind, id, version }`；
 * - `projectPath`：拼 `/api/v1/lingdoc/projects/{projectId}{suffix}`。
 */

/** 对象版本引用（`kind` / `id` / `version`）。 */
export interface G3ObjectVersionRef {
  kind: string
  id: string
  version: string
}

/** 读字符串：非字符串读成空串。 */
export function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/** 读数字：不可解析成有限数的值读成 0。 */
export function num(value: unknown): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

/** 读布尔：只有严格的 `true` 才是真。 */
export function bool(value: unknown): boolean {
  return value === true
}

/** 读对象：非对象（含数组）读成空对象。 */
export function objectOf(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

/** 读对象版本引用。 */
export function refOf(value: unknown): G3ObjectVersionRef {
  const raw = objectOf(value)
  return { kind: str(raw.kind), id: str(raw.id), version: str(raw.version) }
}

/** 拼项目级路径。 */
export function projectPath(projectId: string, suffix: string): string {
  return `/api/v1/lingdoc/projects/${encodeURIComponent(projectId)}${suffix}`
}

/**
 * HTTP 状态码的三种判据。
 *
 * 为什么单独收成一份：**「服务端拒了这个请求」只能由客户端错误（4xx）成立。**
 * 5xx 是服务端**自己**崩了——把它算成「按契约拒掉」，等于让判定卡在服务端出
 * 故障时反而判成立，那是拿错误冒充拒绝。
 *
 * 三者的区别：
 * - `isSuccessStatus`：2xx 才算成功；
 * - `isClientRejection`：**只有 4xx** 算「服务端按契约拒了这个请求」；
 * - `isFailedRequest`：请求没拿到 2xx 成功（4xx 拒绝**或** 5xx 服务端错误）——
 *   用来如实叙述「这次调用失败了，HTTP 是多少」，不替它判断责任方。
 */
export function isSuccessStatus(status: number): boolean {
  return status >= 200 && status < 300
}

/** 服务端按契约拒了这个请求：只有 4xx 算（5xx 是服务端自身错误，不算拒绝）。 */
export function isClientRejection(status: number): boolean {
  return status >= 400 && status < 500
}

/** 这次调用没拿到 2xx 成功（可能是 4xx 拒绝，也可能是 5xx 服务端错误）。 */
export function isFailedRequest(status: number): boolean {
  return !isSuccessStatus(status)
}
