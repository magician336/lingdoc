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
