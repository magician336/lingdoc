import type { G3ResponsibilityDomain, G3Severity, G3Status } from './g3Status'

/**
 * G3 验收台 · 验收矩阵的纯数据与锚点解析。
 *
 * 判据原文**照录**主基准：
 * `docs/06-灵档产品开发规划/beta-gap-development-plans/G3-pcc-change-coordination.md`
 * —— §12 交付切片（`:336-346`）与 §13 领域验收场景（`:348-412`）。
 * 原文一个字都不改写：矩阵的价值在于「读者能回到基准逐字核对」。
 */

/** 主基准文档路径。 */
export const G3_MAIN_BASELINE_PATH =
  'docs/06-灵档产品开发规划/beta-gap-development-plans/G3-pcc-change-coordination.md'

/** 另两份给出不同 ChangeSet 状态集合的文档，页面要标出各自位置。 */
export const G3_LIFECYCLE_SPEC_PATH = 'docs/06-灵档产品开发规划/30-Spec-G3-ChangeSet生命周期与ContextRevision.md'
export const G3_FIRST_RELEASE_REVIEW_PATH = 'docs/08-本轮实施方案/G3-首版最小变更复核.md'

/** 仓库门禁的验收矩阵八列（`docs/agents/development-rules.md:80-82`）。 */
export const G3_MATRIX_COLUMNS = [
  '验收项',
  '用户可见入口',
  '领域不变量/事务边界',
  '成功',
  '失败/权限/恢复',
  '持久化与并发',
  '测试或运行证据',
  '状态',
] as const

/** Spec 锚点：基准文档路径 + 行号范围。 */
export interface SpecAnchor {
  path: string
  start: number
  end: number
}

/** 把锚点渲染成「路径:行号」或「路径:起-止」。 */
export function formatAnchor(anchor: SpecAnchor): string {
  return anchor.start === anchor.end
    ? `${anchor.path}:${anchor.start}`
    : `${anchor.path}:${anchor.start}-${anchor.end}`
}

/**
 * 解析「路径:行号」或「路径:起-止」。
 *
 * 解析不出来就返回 null，**不猜**：行号是读者核对判据的入口，
 * 猜一个行号比没有行号更坏——它会把人引到错误的那一段。
 */
export function parseAnchor(text: string): SpecAnchor | null {
  if (typeof text !== 'string') return null
  const trimmed = text.trim()
  const separator = trimmed.lastIndexOf(':')
  if (separator <= 0 || separator === trimmed.length - 1) return null

  const path = trimmed.slice(0, separator)
  const range = trimmed.slice(separator + 1)
  if (!path) return null

  const match = /^(\d+)(?:-(\d+))?$/.exec(range)
  if (!match) return null

  const start = Number(match[1])
  const end = match[2] === undefined ? start : Number(match[2])
  if (!Number.isInteger(start) || !Number.isInteger(end)) return null
  if (start < 1 || end < 1) return null
  if (end < start) return null
  return { path, start, end }
}

export type G3MatrixGroup = 'slice' | 'ac'

export interface G3MatrixRow {
  /** 验收项编号：切片用 G3.x，场景用 AC-G3-xx。 */
  id: string
  group: G3MatrixGroup
  /** 验收项名称。 */
  name: string
  /** 判据原文（照录，不改写）。 */
  criterion: string
  /** Spec 锚点。 */
  anchor: SpecAnchor
  /** 状态徽标，取仓库门禁词表。 */
  status: G3Status
  /** 证据位：这条判据的证据在哪里。 */
  evidence: string
  /** 补充说明：为什么是这个状态、边界在哪。 */
  note?: string
  /** 承接该验收项的独立 GitHub Issue（follow-up 用）。 */
  followUpIssue?: number
  /** 影响严重度（只在判据原文支持时给出）。 */
  severity?: G3Severity
  /** 责任域（只在判据原文支持时给出）。 */
  domain?: G3ResponsibilityDomain
  /** 切片的交付物（仅切片行有）。 */
  deliverable?: string
}

const FOLLOW_UP_NOTE = (issue: number) =>
  `本项在本票未实跑；真实服务验收由独立 GitHub Issue #${issue} 承接，未写成已实测。`

/** §12 交付切片：G3.1–G3.7 的完成判据（主基准 `:340-346`，原文）。 */
export const G3_SLICE_ROWS: readonly G3MatrixRow[] = [
  {
    id: 'G3.1',
    group: 'slice',
    name: 'G3.1 ChangeSet',
    deliverable: '创建、评估、应用、拒绝、过期和 revision 条件写入',
    criterion: '旧基线不能应用；应用无半完成写入',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 340, end: 340 },
    status: 'TODO',
    evidence: '本票未实跑（形态预览）',
    note: '本票只建立判据基准与骨架，该切片的完成判据尚未判定。',
  },
  {
    id: 'G3.2',
    group: 'slice',
    name: 'G3.2 依赖图',
    deliverable: 'DependencyRegistry、版本化边、AI 候选导入、传播查询和审计',
    criterion: '每条影响可追溯到来源边；重复边、建议边和人工确认边有明确语义',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 341, end: 341 },
    status: 'TODO',
    evidence: '本票未实跑（形态预览）',
    note: '本票只建立判据基准与骨架，该切片的完成判据尚未判定。',
  },
  {
    id: 'G3.3',
    group: 'slice',
    name: 'G3.3 影响解析',
    deliverable: 'ImpactResolver、预览、动作映射和任务幂等',
    criterion: '只生成受影响任务；无关对象保持原状态',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 342, end: 342 },
    status: 'TODO',
    evidence: '本票未实跑（形态预览）',
    note: '本票只建立判据基准与骨架，该切片的完成判据尚未判定。',
  },
  {
    id: 'G3.4',
    group: 'slice',
    name: 'G3.4 任务协调',
    deliverable: '分派、处置、关闭、STALE 和跨域事件',
    criterion: '旧任务不能关闭新版本；异步成功不等于人工关闭',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 343, end: 343 },
    status: 'TODO',
    evidence: '本票未实跑（形态预览）',
    note: '本票只建立判据基准与骨架，该切片的完成判据尚未判定。',
  },
  {
    id: 'G3.5',
    group: 'slice',
    name: 'G3.5 RouteNode',
    deliverable: '节点/边版本、章节/主张关联、必需性',
    criterion: '路线图可比较、可恢复；必需项缺失可阻断',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 344, end: 344 },
    status: 'TODO',
    evidence: '本票未实跑（形态预览）',
    note: '本票只建立判据基准与骨架，该切片的完成判据尚未判定。',
  },
  {
    id: 'G3.6',
    group: 'slice',
    name: 'G3.6 稳定化',
    deliverable: '聚合器、确认复核、冻结输入和 READY 闸门',
    criterion: '未关闭阻断项不能 STABILIZED 或导出',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 345, end: 345 },
    // 切片行的状态由**实跑证据**驱动，不由静态声明预判。G3.6 的调研类闭环
    // 已在当前 head 实跑（#258），但该切片的完成判据整体尚未判定，因此这里
    // 仍是 TODO；外部检索渠道的覆盖受限见 G3-GAP-08。
    status: 'TODO',
    evidence: '本票未实跑（形态预览）',
    note:
      '本票只建立判据基准与骨架，该切片的完成判据尚未判定；' +
      '调研类闭环虽已在 #258 实跑（真实 `ResearchRun` 回执），但覆盖受限，' +
      '不足以判定该切片成立。',
  },
  {
    id: 'G3.7',
    group: 'slice',
    name: 'G3.7 影响中心',
    deliverable: '变更、影响树、任务、路线图最小 UI',
    criterion: '用户能看见影响理由、责任人和下一步动作',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 346, end: 346 },
    // 状态口径与缺口清单一致：G3-GAP-06 / G3-GAP-07 记的是「未实跑（NOT RUN）」，
    // 不是「待办（TODO）」——这一行**跑过**相关链路，只是验收台页面自己读原始响应的
    // 落证没做（工作台面板已由 2786bcc 补齐）。
    status: 'NOT RUN',
    evidence: '本轮未实跑（形态预览）',
    note: '本票只建立判据基准与骨架，该切片的完成判据尚未判定。',
  },
]

/** §13 领域验收场景：AC-G3-01…16 原文照录（主基准 `:348-412`）。 */
export const G3_AC_ROWS: readonly G3MatrixRow[] = [
  {
    id: 'AC-G3-01',
    group: 'ac',
    name: '基线上的研究条件修改',
    criterion:
      '用户将研究对象从“普通本科生”改为“接受过创业教育的本科生”。系统创建 ChangeSet，预览受影响的 EvidenceCard、ResearchClaim、方法章节和 RouteNode；Owner 应用后生成新 revision，旧确认保留历史并进入待复核。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 350, end: 352 },
    status: 'PASS (follow-up)',
    evidence: '历史快照 b3（变更→应用→新修订，部分覆盖）',
    note: FOLLOW_UP_NOTE(239),
    followUpIssue: 239,
  },
  {
    id: 'AC-G3-02',
    group: 'ac',
    name: '旧基线拒绝应用',
    criterion:
      'CS-01 和 CS-02 都基于 revision 3。CS-01 先应用到 revision 4；CS-02 应用失败并标记 STALE，不产生新 revision、任务或模板写入。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 354, end: 356 },
    status: 'PASS (follow-up)',
    evidence: '历史快照 b3（未覆盖并发拒绝路径）',
    note: FOLLOW_UP_NOTE(239),
    followUpIssue: 239,
  },
  {
    id: 'AC-G3-03',
    group: 'ac',
    name: '应用原子性',
    criterion:
      '应用过程中任务写入失败或权限失效。项目仍保持原 revision 和旧模板引用，不能出现新 revision 已存在但 ImpactTask 不完整的状态。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 358, end: 360 },
    status: 'PASS (follow-up)',
    evidence:
      'docs/08-本轮实施方案/G3-缺口收口-18-验证报告-20261021.json（当前 head 的两轮实跑）',
    note:
      '本轮实跑判定成立：装配受控故障注入后 apply 返回 503 + injected_fault，' +
      '读回证明项目 revision、模板引用与 ImpactTask 集合均未变、变更集未变成 applied。' +
      '行状态记 PASS (follow-up) 而不是 PASS：缺口清单 #246 仍未关闭，' +
      '按仓库规则本票所列缺口对应的行在关闭前不判 PASS。' +
      '解除条件与 Spec 位置见缺口清单 #246。',
    followUpIssue: 246,
    domain: 'B',
    severity: 'blocking',
  },
  {
    id: 'AC-G3-04',
    group: 'ac',
    name: '局部影响',
    criterion:
      '变更只依赖 Ch2 和 RouteNode.R7。Ch1、无关 EvidenceCard 和无关路线节点不产生失效或复核任务，并能在预览中显示未受影响理由。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 362, end: 364 },
    status: 'PASS (follow-up)',
    evidence: '历史快照 b3（两章均受影响，未覆盖无关对象）',
    note: FOLLOW_UP_NOTE(240),
    followUpIssue: 240,
  },
  {
    id: 'AC-G3-05',
    group: 'ac',
    name: '多上游任务合并',
    criterion:
      '同一个章节同时受研究条件和证据版本变化影响。系统生成一条逻辑 ImpactTask，保留两个来源和动作原因；重复重投不产生第二条任务。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 366, end: 368 },
    status: 'PASS (follow-up)',
    evidence: '历史快照 b3（每章一条任务，未覆盖双来源合并）',
    note: FOLLOW_UP_NOTE(240),
    followUpIssue: 240,
    domain: 'B',
  },
  {
    id: 'AC-G3-06',
    group: 'ac',
    name: '旧异步结果',
    criterion:
      'revision 4 的章节生成运行期间，ChangeSet 产生 revision 5。revision 4 的结果可以保存为历史候选，但不能覆盖 revision 5 的章节、确认或 ReleaseSnapshot。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 370, end: 372 },
    status: 'PASS (follow-up)',
    evidence: '历史快照 b3（未覆盖跨修订并发）',
    note: FOLLOW_UP_NOTE(241),
    followUpIssue: 241,
  },
  {
    id: 'AC-G3-07',
    group: 'ac',
    name: '证据复核不能自动确认',
    criterion:
      'A 完成证据重检并返回新候选。任务仍需责任人查看和确认；模型或异步作业成功不能直接把 ResearchClaim 标为 confirmed 或关闭人工复核任务。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 374, end: 376 },
    status: 'PASS (follow-up)',
    evidence: '历史快照 b3（人工处置闭环，未覆盖模型重检）',
    note: FOLLOW_UP_NOTE(243),
    followUpIssue: 243,
    domain: 'A',
    severity: 'blocking',
  },
  {
    id: 'AC-G3-08',
    group: 'ac',
    name: 'RouteNode 必需项',
    criterion:
      '模板声明 RouteNode.R7 为必需。R7 缺失、待复核或版本落后时，C 产生 blocking ValidationIssue；即使其他任务已完成，revision 也不能 STABILIZED 或 READY。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 378, end: 380 },
    // #314 的完成线要求：本票完成前，AC-G3-08 / AC-G3-16 在缺口清单里保持 BLOCKED。
    status: 'BLOCKED',
    evidence: '实现缺口 · 缺口清单 #246',
    note:
      '路线图的版本化编辑会话与节点写入**已实现**，验收台链路也已补上（#295）；' +
      '本轮实跑 `NOT RUN`：演示模板不声明任何 RouteNode，基线路线图为空，' +
      '造不出「必需节点缺失」与「版本落后」两种前置。' +
      '解除条件与 Spec 位置见缺口清单 #246。',
    followUpIssue: 246,
    domain: 'C',
    severity: 'blocking',
  },
  {
    id: 'AC-G3-09',
    group: 'ac',
    name: '规则问题与任务分离',
    criterion:
      'Owner dismiss 当前规则问题后，ValidationIssue 不再阻断该次求值，但对应 ImpactTask 仍为 OPEN，项目不能稳定；新目标 revision 重新求值后问题可以再次出现。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 382, end: 384 },
    status: 'PASS (follow-up)',
    evidence: '历史快照 b3（未覆盖 dismiss 路径）',
    note: FOLLOW_UP_NOTE(242),
    followUpIssue: 242,
  },
  {
    id: 'AC-G3-10',
    group: 'ac',
    name: '模板升级',
    criterion:
      'active 项目尝试直接改模板字段或章节，普通保存被拒。通过 `template_upgrade` ChangeSet 评估字段迁移、规则、章节、RouteNode、确认和快照影响；应用后旧快照仍绑定旧模板版本。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 386, end: 388 },
    status: 'PASS (follow-up)',
    evidence: '历史快照 b3（普通字段变更，未覆盖模板升级）',
    note: FOLLOW_UP_NOTE(239),
    followUpIssue: 239,
  },
  {
    id: 'AC-G3-11',
    group: 'ac',
    name: '撤销变化',
    criterion:
      '用户想恢复旧研究对象。系统不把项目指针静默改回 revision 3，而是要求基于当前 revision 创建逆向 ChangeSet，并重新进行影响评估和确认。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 390, end: 392 },
    status: 'PASS (follow-up)',
    evidence: '历史快照 b3（未覆盖逆向变更集）',
    note: FOLLOW_UP_NOTE(239),
    followUpIssue: 239,
  },
  {
    id: 'AC-G3-12',
    group: 'ac',
    name: '稳定化重新读取',
    criterion:
      '所有任务页面显示已完成，但其中一个目标对象后来被新版本替换。稳定化聚合器重新读取后发现版本不匹配，保持 BLOCKED 并创建/重投对应任务。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 394, end: 396 },
    status: 'PASS (follow-up)',
    evidence: '历史快照 b3（稳定化成功路径，未覆盖版本漂移）',
    note: FOLLOW_UP_NOTE(242),
    followUpIssue: 242,
    domain: 'C',
    severity: 'blocking',
  },
  {
    id: 'AC-G3-13',
    group: 'ac',
    name: '建议依赖边',
    criterion:
      '模型提出某章节可能依赖某证据，但用户未确认该关系。系统可以在预览中提示“待确认关系”，不能单独使章节失效或阻断导出。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 398, end: 400 },
    status: 'PASS (follow-up)',
    evidence: '历史快照 b3（未覆盖建议依赖边）',
    note: FOLLOW_UP_NOTE(240),
    followUpIssue: 240,
  },
  {
    id: 'AC-G3-14',
    group: 'ac',
    name: 'G3 影响助手',
    criterion:
      '用户用自然语言提出修改。助手生成 ChangeSet 草稿，并用“确定受影响”和“可能受影响”两组说明解释原因；用户确认后才进入正式评估。助手不能直接应用变更、关闭任务或放行导出。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 402, end: 404 },
    status: 'BLOCKED',
    evidence: '实现缺口 · 缺口清单 #246',
    note:
      '助手草稿里的「确定受影响 / 可能受影响」**解释分组已实现**，验收台读回链路也已补上（#295）；' +
      '模型边界已放宽（#315：缺两组说明时不再整条拒，改为如实标注「缺说明」）。' +
      '**行状态按 #315 的完成线保持 `BLOCKED`**：这一行的判定要由验收台在当前 head 实跑得出，' +
      '而本轮没有可读回的助手草稿（需要一条真实模型轮次 → #320）；' +
      '工作台那个分组是影响解析结果，不可互替。解除条件与 Spec 位置见缺口清单 #246。',
    followUpIssue: 246,
  },
  {
    id: 'AC-G3-15',
    group: 'ac',
    name: 'AI 批量导入依赖',
    criterion:
      '用户选择“研究条件、证据卡和第 2–4 章”作为范围。AI 提出若干候选依赖，并显示对象版本、依据片段和建议理由；用户确认其中一部分、修改一部分、拒绝一部分。只有确认的关系进入正式依赖图，拒绝记录不会在同一版本中反复推荐。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 406, end: 408 },
    status: 'BLOCKED',
    evidence: '实现缺口 · 缺口清单 #246',
    note:
      '依赖边的确认 / 修改 / 拒绝三态有端点，依赖候选的批量提出无端点。' +
      '解除条件与 Spec 位置见缺口清单 #246。',
    followUpIssue: 246,
  },
  {
    id: 'AC-G3-16',
    group: 'ac',
    name: '路线图版本恢复',
    criterion:
      '用户编辑未冻结路线图后取消编辑。系统恢复到上一个 RouteNode/RouteEdge 版本，关联任务和章节引用不指向被取消的临时版本。',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 410, end: 412 },
    status: 'BLOCKED',
    evidence: '实现缺口 · 缺口清单 #246',
    note:
      '同样缺技术路线节点的写入与取消未冻结编辑端点。' +
      '解除条件与 Spec 位置见缺口清单 #246。',
    followUpIssue: 246,
    domain: 'C',
    severity: 'blocking',
  },
]

/** 整张矩阵：先切片七行，再验收场景十六行。 */
export const G3_MATRIX_ROWS: readonly G3MatrixRow[] = [...G3_SLICE_ROWS, ...G3_AC_ROWS]

/** 统计各状态出现次数，七种状态都要有键（没有的记 0）。 */
export function statusCounts(rows: readonly G3MatrixRow[]): Record<G3Status, number> {
  const counts = {
    'PASS': 0,
    'PASS (follow-up)': 0,
    'N/A': 0,
    'WARNING': 0,
    'NOT RUN': 0,
    'BLOCKED': 0,
    'TODO': 0,
  } as Record<G3Status, number>
  for (const row of rows) {
    counts[row.status] += 1
  }
  return counts
}

/**
 * ChangeSet 状态集合的**三种**读法。
 *
 * 仓库里确实有三种说法，页面不替读者裁决，但必须把各自的位置摆出来：
 * 口径以主基准的七态为准（已定），另两处的差异原样标注。
 */
export interface G3LifecycleReading {
  /** 读法名。 */
  id: string
  /** 文档名。 */
  source: string
  /** 该读法在文档中的位置。 */
  anchor: SpecAnchor
  /** 该读法给出的状态集合。 */
  states: readonly string[]
  /** 差异说明。 */
  note: string
  /** 是否是本页面采用的口径。 */
  primary: boolean
}

export const G3_LIFECYCLE_READINGS: readonly G3LifecycleReading[] = [
  {
    id: '主基准',
    source: 'G3-pcc-change-coordination.md §4.2 ChangeSet 状态',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 88, end: 98 },
    states: ['DRAFT', 'ASSESSED', 'APPLIED', 'STABILIZED', 'REJECTED', 'STALE', 'SATISFIED'],
    note: '本页面以主基准的七态为准。',
    primary: true,
  },
  {
    id: '30-Spec',
    source: '30-Spec-G3-ChangeSet生命周期与ContextRevision.md',
    anchor: { path: G3_LIFECYCLE_SPEC_PATH, start: 38, end: 38 },
    states: ['DRAFT', 'ASSESSED', 'APPLIED', 'STABILIZED', 'REJECTED', 'STALE'],
    note: '六态：列了 DRAFT 却漏了 SATISFIED；同一份文档 :45 又定义了 SATISFIED，该 Spec 内部不自洽。',
    primary: false,
  },
  {
    id: '首版最小变更复核',
    source: 'G3-首版最小变更复核.md',
    anchor: { path: G3_FIRST_RELEASE_REVIEW_PATH, start: 48, end: 48 },
    states: ['assessed', 'applied', 'rejected', 'stale', 'satisfied'],
    note: '五态：明确「没有持久化 draft、没有 stabilized」，且状态用小写。',
    primary: false,
  },
]

export interface G3SatisfiedReading {
  primary: { source: string; anchor: SpecAnchor; text: string }
  alternative: { source: string; anchor: SpecAnchor; text: string }
}

/**
 * `SATISFIED` 的归属有两种读法：主基准读作「新建评估记录、原记录留历史」，
 * 30-Spec 读作「原记录变终态」。按主基准处理，并标出另一读法的位置。
 */
export const G3_SATISFIED_READING: G3SatisfiedReading = {
  primary: {
    source: 'G3-pcc-change-coordination.md §5.3 并发和过期',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 147, end: 147 },
    text: '新建评估记录、原记录留历史：仅允许对 STALE 记录执行，原记录保留为历史，新建的评估记录绑定当前修订并通过 reassessed_from_id 追溯来源。',
  },
  alternative: {
    source: '30-Spec-G3-ChangeSet生命周期与ContextRevision.md',
    anchor: { path: G3_LIFECYCLE_SPEC_PATH, start: 45, end: 45 },
    text: '另一读法：原记录变终态——重评估生成一个终态 SATISFIED 记录并链接到未变更的 stale 历史，不推进修订也不产生影响。',
  },
}
