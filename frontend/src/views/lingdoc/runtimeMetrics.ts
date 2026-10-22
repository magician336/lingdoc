// G6 运行指标口径表 —— 前端对服务端 `RuntimeMetrics.counts` 的投影。
//
// 服务端只给 `counts`（键为 `category/status`），值班人员要看的是按语义归并
// 之后的项。这张表是 G6 计划 §7「最小运行指标」在前端的翻译，不是前端自己
// 发明的口径：每一项都指向文档里既定的指标字典。
//
// 三条约束（改动本文件时一并守住）：
//   1. 本表**不复制**服务端完整的 category / status 白名单，只声明「哪些语义
//      组合归到哪一项」。运行时按服务端返回的 counts 键逐个判定。
//   2. 本文件出现的每个 category / status 都必须在服务端白名单内
//      —— runtimeMetrics.test.ts 读 internal/lingdoc/workspacecore/runtime_control.go
//      核对。服务端改了白名单而本表没改，测试会红。
//   3. 服务端若将来提供专用指标字段，改用服务端的，不要继续往本表加口径。

export interface RuntimeMetricSpec {
  /** 指标项的稳定标识，供测试与锚点使用。 */
  key: string
  /** 面向值班人员的中文名。 */
  label: string
  /** 本项统计哪些资源类别；`*` 表示全部类别。 */
  categories: '*' | readonly string[]
  /** 本项统计哪些状态；`*` 表示全部状态。 */
  statuses: '*' | readonly string[]
  /**
   * 该项在服务端没有对应观测时的说明。存在时该项不参与投影，永远显示
   * 「后端未提供该项观测」——不显示 0，也不改名硬凑成别的含义。
   */
  unavailableReason?: string
  /** 与其它指标项的重叠关系说明。 */
  note?: string
}

export const RUNTIME_METRIC_SPECS: readonly RuntimeMetricSpec[] = [
  { key: 'async_failed', label: '异步失败', categories: ['task'], statuses: ['failed'] },
  { key: 'async_retry', label: '异步重试', categories: ['task'], statuses: ['retry'] },
  { key: 'stale', label: '陈旧结果', categories: ['task'], statuses: ['stale'] },
  { key: 'duplicate', label: '重复副作用', categories: ['task'], statuses: ['duplicate'] },
  { key: 'permission_rejected', label: '权限拒绝', categories: '*', statuses: ['rejected'] },
  { key: 'revoked', label: '撤权拦截', categories: '*', statuses: ['revoked'] },
  {
    key: 'download_reauth',
    label: '下载再鉴权拦截',
    categories: ['download'],
    statuses: ['rejected', 'revoked'],
    note: '是「权限拒绝」与「撤权拦截」的下集，两处都会计入。',
  },
  { key: 'preflight', label: '预检问题', categories: ['preflight'], statuses: ['failed', 'rejected', 'conflict'], note: '只统计预检的问题类状态，成功的预检不是问题；其中的 rejected 同时计入「权限拒绝」。' },
  { key: 'export_loss', label: '导出损失', categories: '*', statuses: ['loss'] },
  { key: 'dependency_unavailable', label: '外部依赖不可用', categories: ['dependency'], statuses: ['unavailable'] },
  {
    key: 'permission_decisions',
    label: 'G5 授权判定（allow/deny）',
    categories: [],
    statuses: [],
    unavailableReason: '服务端运行事件没有记录授权判定结果，后端未提供该项观测。',
  },
]

/**
 * 资源类别的中文名。服务端新增类别时本表可以暂缺，界面回落到原始键；
 * 服务端删除类别时守护测试会红。
 */
export const RUNTIME_CATEGORY_LABELS: Readonly<Record<string, string>> = {
  request: '请求',
  task: '异步任务',
  changeset: '变更集',
  impact_task: '影响任务',
  validation: '规则校验',
  stabilization: '稳定化',
  preflight: '预检',
  snapshot: '交付快照',
  export: '导出',
  download: '下载',
  audit: '审计',
  dependency: '外部依赖',
}

/** 运行事件状态的中文名。 */
export const RUNTIME_STATUS_LABELS: Readonly<Record<string, string>> = {
  success: '成功',
  rejected: '拒绝',
  conflict: '冲突',
  failed: '失败',
  retry: '重试',
  stale: '陈旧',
  duplicate: '重复',
  unavailable: '不可用',
  revoked: '已撤权',
  loss: '损失',
}

export interface RuntimeMetricValue {
  key: string
  label: string
  /** 命中的复合键；空表示没有该项观测。 */
  keys: string[]
  /** 命中键的求和；null 表示没有该项观测，不得显示成 0。 */
  total: number | null
  unavailableReason?: string
  note?: string
}

/** 一个复合键「资源类别/状态」拆成两段。键里没有斜杠时不算命中。 */
export function splitRuntimeCountKey(key: string): { category: string; status: string } | null {
  const slash = key.indexOf('/')
  if (slash <= 0 || slash === key.length - 1) return null
  return { category: key.slice(0, slash), status: key.slice(slash + 1) }
}

function specMatches(spec: RuntimeMetricSpec, category: string, status: string): boolean {
  if (spec.categories !== '*' && !spec.categories.includes(category)) return false
  if (spec.statuses !== '*' && !spec.statuses.includes(status)) return false
  return true
}

/**
 * 把服务端返回的 counts 投影成按语义归并的指标项。
 *
 * 只消费服务端给出的计数：界面不自己判定任何运行状态。没有命中的项返回
 * `total: null`（无观测），而不是 0 —— 把「没有测量」当成「测得 0」会制造
 * 不存在的确定性。
 */
export function projectRuntimeMetrics(counts: Record<string, number> | undefined | null): RuntimeMetricValue[] {
  const source = counts && typeof counts === 'object' ? counts : {}
  return RUNTIME_METRIC_SPECS.map(spec => {
    if (spec.unavailableReason) {
      return { key: spec.key, label: spec.label, keys: [], total: null, unavailableReason: spec.unavailableReason, note: spec.note }
    }
    const keys: string[] = []
    let total = 0
    for (const [key, value] of Object.entries(source)) {
      const parts = splitRuntimeCountKey(key)
      if (!parts || !specMatches(spec, parts.category, parts.status)) continue
      keys.push(key)
      total += Number.isFinite(value) ? value : 0
    }
    return {
      key: spec.key,
      label: spec.label,
      keys: keys.sort(),
      total: keys.length ? total : null,
      note: spec.note,
    }
  })
}

/** 完整 counts 展开表的一行：原始键 + 可读名 + 计数。 */
export interface RuntimeCountRow {
  key: string
  category: string
  status: string
  categoryLabel: string
  statusLabel: string
  count: number
}

/**
 * 服务端 counts 的完整展开。这是权威视图：定向汇总是它的投影，两处同时
 * 呈现，读者不必信任汇总口径。
 */
export function expandRuntimeCounts(counts: Record<string, number> | undefined | null): RuntimeCountRow[] {
  const source = counts && typeof counts === 'object' ? counts : {}
  return Object.entries(source)
    .map(([key, value]) => {
      const parts = splitRuntimeCountKey(key)
      const category = parts ? parts.category : ''
      const status = parts ? parts.status : ''
      return {
        key,
        category,
        status,
        categoryLabel: RUNTIME_CATEGORY_LABELS[category] || category || '未分类',
        statusLabel: RUNTIME_STATUS_LABELS[status] || status || '未标注',
        count: Number.isFinite(value) ? value : 0,
      }
    })
    .sort((left, right) => left.key.localeCompare(right.key))
}

/** 本表用到的资源类别，供守护测试与服务端白名单核对。 */
export function usedRuntimeCategories(): string[] {
  const used = new Set<string>()
  for (const spec of RUNTIME_METRIC_SPECS) {
    if (spec.categories === '*') continue
    for (const category of spec.categories) used.add(category)
  }
  return [...used].sort()
}

/** 本表用到的状态，供守护测试与服务端白名单核对。 */
export function usedRuntimeStatuses(): string[] {
  const used = new Set<string>()
  for (const spec of RUNTIME_METRIC_SPECS) {
    if (spec.statuses === '*') continue
    for (const status of spec.statuses) used.add(status)
  }
  return [...used].sort()
}
