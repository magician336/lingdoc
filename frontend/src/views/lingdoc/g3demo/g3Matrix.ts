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

/**
 * 证据位的**类别**（结构化，不靠中文子串判定）。
 *
 * - `this_run`：这一行的结论由一次真实运行判定得出（可能是上一轮实跑，
 *   不是「尚未判定」）；
 * - `historical_snapshot`：结论来自历史快照（b3 那一轮的历史记录）；
 * - `not_judged`：没有落证，状态只是静态声明。
 */
export type G3EvidenceKind = 'this_run' | 'historical_snapshot' | 'not_judged'

/**
 * 判定来源（结构化）：**哪条链的哪张判定卡**判的。
 *
 * 为什么要有它：证据位是**散文**，读者没法拿散文做机器核对。
 * 「这一行由哪条链的哪张卡判的」写成结构化字段之后，
 * 导出体才能核对「本文件里有没有这张卡」——没有卡的 `PASS` 行必须被标出来。
 */
export interface G3JudgedBy {
  /** 判定这条行的链脚本（仓库内相对路径的文件名）。 */
  chain: string
  /** 判定卡编号。 */
  card: string
  /** 同一脚本里的分段（一条脚本跑多段链时写清楚是哪一段）。 */
  segment?: string
}

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
  /**
   * 证据位的**类别**（结构化）。
   *
   * 「有落证」这件事必须能被机器核对，不能靠 `evidence` 里有没有「实跑」
   * 两个中文字符——否定句（本轮**未**实跑）也会命中那两个字。
   */
  evidenceKind: G3EvidenceKind
  /**
   * 判定来源：哪条链的哪张判定卡判的。
   *
   * 没有落证的行不设这个字段：宁可不写，也不写一个取不到的卡号。
   */
  judgedBy?: G3JudgedBy
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

/**
 * 七条切片行的落证文件（收口轮验证报告）。
 *
 * 证据位只指**文件**，不指运行标识与 head：
 * 运行标识与 head 每次重跑都会变（服务端 commitId 也会跟着变），
 * 写死在矩阵里等于写下**下一次就过期**的串，读者还取不到。
 * 这一次到底是哪一次运行、哪个 head，由运行上下文（`ctx.runId` /
 * `ctx.head.commitId`）在页面与导出体里带出来。
 */
const G3_SLICE_EVIDENCE_FILE =
  'docs/08-本轮实施方案/G3-缺口收口-21-验证报告-20261021.json'

/** §12 交付切片：G3.1–G3.7 的完成判据（主基准 `:340-346`，原文）。 */
export const G3_SLICE_ROWS: readonly G3MatrixRow[] = [
  {
    id: 'G3.1',
    group: 'slice',
    name: 'G3.1 ChangeSet',
    deliverable: '创建、评估、应用、拒绝、过期和 revision 条件写入',
    criterion: '旧基线不能应用；应用无半完成写入',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 340, end: 340 },
    // 切片行的状态由**实跑证据**驱动，不由静态声明预判。这一行没有 #246 缺口，
    // 稳定化链在当前 head 实跑出「旧基线被拒 + 无半完成写入」，因此判 PASS。
    status: 'PASS',
    evidence:
      `稳定化与交付闭环链实跑（run-stabilization.ts · 判定卡 G3.1）· 落证：${G3_SLICE_EVIDENCE_FILE}`,
    evidenceKind: 'this_run',
    judgedBy: { chain: 'run-stabilization.ts', card: 'G3.1' },
    note:
      '本轮实跑判定成立：同基线的第二个变更集在第一个应用后被服务端按 409 拒掉' +
      '（code=version_conflict），该变更集落为 stale（stale_reason=spec_revision_changed）；' +
      '被拒之后上下文修订条数仍为 2、影响任务条数仍为 4——没有半完成写入。' +
      '（证据见「G3.1」判定卡与该链的逐步结果。）',
  },
  {
    id: 'G3.2',
    group: 'slice',
    name: 'G3.2 依赖图',
    deliverable: 'DependencyRegistry、版本化边、AI 候选导入、传播查询和审计',
    criterion: '每条影响可追溯到来源边；重复边、建议边和人工确认边有明确语义',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 341, end: 341 },
    // 依赖链在当前 head 实跑出三类边的语义，这一行没有 #246 缺口，判 PASS。
    status: 'PASS',
    evidence:
      `依赖图与影响任务链实跑（run-dependencies.ts · 判定卡 G3.2）· 落证：${G3_SLICE_EVIDENCE_FILE}`,
    evidenceKind: 'this_run',
    judgedBy: { chain: 'run-dependencies.ts', card: 'G3.2' },
    note:
      '本轮实跑判定成立：影响评估的 6 条影响全部可追溯到来源（裁决之后读回依赖边 9 条' +
      '——确认 7 / 建议 1 / 拒绝 1，未能追溯 0 条；变更集直接列入的对象按服务端给的 ' +
      '`explicit:` 来源边记，同样可追溯）；两条来源边指向同一目标、动作相同时合并成' +
      '**一条**逻辑任务（causes 保留两条来源），重复应用同一变更集不产生第二条任务；' +
      '建议边（status=suggested）只进「可能受影响」、不为它的目标产生任务；' +
      '人工「确认 / 修改」把边落成 confirmed（修改留旧关系 candidate_b），' +
      '人工「拒绝」的边落 rejected、同一版本再投不新建边。',
  },
  {
    id: 'G3.3',
    group: 'slice',
    name: 'G3.3 影响解析',
    deliverable: 'ImpactResolver、预览、动作映射和任务幂等',
    criterion: '只生成受影响任务；无关对象保持原状态',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 342, end: 342 },
    // 变更集链在当前 head 实跑出「只生成受影响任务 + 无关对象保持原状态」，
    // 这一行没有 #246 缺口，判 PASS。
    status: 'PASS',
    evidence:
      `实时链路实跑（run-evidence.ts · 判定卡 G3.3）· 落证：${G3_SLICE_EVIDENCE_FILE}`,
    evidenceKind: 'this_run',
    judgedBy: { chain: 'run-evidence.ts', card: 'G3.3', segment: '实时链路' },
    note:
      '本轮实跑判定成立：应用之后读回的影响任务，每一条的目标都在本次变更集的受影响集合里' +
      '（评估对象与评估影响构成那个集合，集合外的任务 0 条）；' +
      '与本次变更集无关的章节既不在评估对象里、也不在影响里，没有为它产生影响任务，' +
      '它的版本级确认标记在应用前后都保持有效——无关对象保持原状态。',
  },
  {
    id: 'G3.4',
    group: 'slice',
    name: 'G3.4 任务协调',
    deliverable: '分派、处置、关闭、STALE 和跨域事件',
    criterion: '旧任务不能关闭新版本；异步成功不等于人工关闭',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 343, end: 343 },
    // 阻断链在当前 head 实跑出两段语义，这一行没有 #246 缺口，判 PASS。
    status: 'PASS',
    evidence:
      `阻断与恢复链实跑（run-blocked.ts · 判定卡 G3.4）· 落证：${G3_SLICE_EVIDENCE_FILE}`,
    evidenceKind: 'this_run',
    judgedBy: { chain: 'run-blocked.ts', card: 'G3.4' },
    note:
      '本轮实跑判定成立：目标对象在任务「已完成」之后被新版本替换，稳定化重新读取时发现' +
      '版本不匹配（HTTP 200 · status=BLOCKED）——这一版修订没有被放行；' +
      '依据旧版本的任务被服务端落成失效（1 条，stale_reason=target_version_replaced）：' +
      '失效任务终态但不算闭环，旧任务关不掉新版本。' +
      '研究主张上的复核任务缺少人工复核决策时被服务端按 422 拒掉' +
      '（code=invalid_state），带上 human_decision=reviewed 之后才允许闭环（HTTP 200）——' +
      '异步作业成功关不掉人工复核任务。',
  },
  {
    id: 'G3.5',
    group: 'slice',
    name: 'G3.5 RouteNode',
    deliverable: '节点/边版本、章节/主张关联、必需性',
    criterion: '路线图可比较、可恢复；必需项缺失可阻断',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 344, end: 344 },
    // 路线图链实跑出「可比较 / 可恢复 / 必需项不满足时阻断」三段，但判据字面的
    // 「必需项**缺失**」那一种前置本轮没有落点 ⇒ 覆盖受限，记 WARNING。
    status: 'WARNING',
    evidence:
      `实时链路的 roadmap 段实跑（run-evidence.ts · 判定卡 G3.5）· 落证：${G3_SLICE_EVIDENCE_FILE}`,
    evidenceKind: 'this_run',
    judgedBy: { chain: 'run-evidence.ts', card: 'G3.5', segment: '实时链路的 roadmap 段' },
    note:
      '本轮实跑只成立一部分：**可比较**（读出版本序列并读出两个版本之间的前后比较）、' +
      '**可恢复**（取消未冻结编辑后冻结基线回到取消前的版本，节点版本一并恢复）、' +
      '**必需项不满足时阻断**（必需 RouteNode 处于待复核 / 版本落后时，服务端给出 ' +
      'required_route_not_current 阻断，项目不进入 STABILIZED / READY）三段都成立。' +
      '**缺**的是判据字面那一段：「必需项**已声明但缺失**」的前置本轮没有可观测的落点' +
      '（台面造不出来，那一种阻断由服务端侧保证，见 #319）——' +
      '不用「等价前置」冒充「缺失」，因此记 WARNING。',
  },
  {
    id: 'G3.6',
    group: 'slice',
    name: 'G3.6 稳定化',
    deliverable: '聚合器、确认复核、冻结输入和 READY 闸门',
    criterion: '未关闭阻断项不能 STABILIZED 或导出',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 345, end: 345 },
    // 稳定化链在当前 head 实跑出「阻断态不给放行 / 不给冻结」，但调研类闭环
    // （RETRIEVE_CANDIDATES 的真实调研回执）在本机不可实跑 ⇒ 覆盖受限，
    // 记 WARNING（见 G3-GAP-08）。
    status: 'WARNING',
    evidence:
      `稳定化与交付闭环链实跑（run-stabilization.ts · 判定卡 G3.6）· 落证：${G3_SLICE_EVIDENCE_FILE}`,
    evidenceKind: 'this_run',
    judgedBy: { chain: 'run-stabilization.ts', card: 'G3.6' },
    note:
      '本轮实跑只成立一部分：阻断影响未闭环时，稳定化以 200 成功响应返回 BLOCKED 并给出' +
      '7 条完整阻断原因（impact_task_open / impact_object_unconfirmed / ' +
      'rule:chapter-confirmed），此时冻结交付快照被服务端按 422 拒掉' +
      '（code=context_unstable，不能导出）；全部阻断影响闭环后稳定化返回 STABILIZED。' +
      '**缺**的是 RETRIEVE_CANDIDATES 类影响要求的真实调研回执' +
      '（本机 `web_search_providers` 为空表，见 G3-GAP-08）——' +
      '按缺口清单的要求不得用其他回执冒充，因此记覆盖受限的 WARNING。',
  },
  {
    id: 'G3.7',
    group: 'slice',
    name: 'G3.7 影响中心',
    deliverable: '变更、影响树、任务、路线图最小 UI',
    criterion: '用户能看见影响理由、责任人和下一步动作',
    anchor: { path: G3_MAIN_BASELINE_PATH, start: 346, end: 346 },
    // 数据面（必需对象集合 / 来源对象与理由 / 确定与可能分组 / 责任人与下一步动作）
    // 已在当前 head 实跑判定成立；页面渲染那半段沿用 #257 在 head `a87459f` 的落证。
    // 这一行是 #246 的缺口行（G3-GAP-06 / G3-GAP-07）：缺口清单关闭前不判 PASS，
    // 记 PASS (follow-up)。
    status: 'PASS (follow-up)',
    evidence:
      `依赖图与影响任务链实跑（run-dependencies.ts · 判定卡 G3.7）· 落证：${G3_SLICE_EVIDENCE_FILE}`,
    evidenceKind: 'this_run',
    judgedBy: { chain: 'run-dependencies.ts', card: 'G3.7' },
    note:
      '本轮实跑判定成立（**数据面**）：「本次必须处理的对象」集合 3 个，逐对象核对 ' +
      'state / confirmed / context_revision 齐全；6 条影响都带来源对象与原因（缺 0 条）；' +
      '确定影响 5 条 + 可能影响 1 条 = 全部影响（分组取服务端 certainty，页面不自行重算）；' +
      '任务 3 条，其中 2 条带责任域与指派人（责任域取值 B）；每条影响都带下一步动作——' +
      '影响理由、责任人与下一步动作都读得回来。' +
      '**缺**的是页面渲染那半段在当前 head 的重跑（headless 实跑驱动不到验收台页面，' +
      '页面上自己读 assessment.objects 渲染的那一段沿用 #257 在 head `a87459f` 的落证）；' +
      '加上缺口清单 #246 未关闭前带缺口的行不判 PASS，因此记 PASS (follow-up)。',
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
    evidenceKind: 'historical_snapshot',
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
    evidenceKind: 'historical_snapshot',
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
      'docs/08-本轮实施方案/G3-缺口收口-20-验证报告-20261021.json（当前 head 的两轮实跑）',
    evidenceKind: 'this_run',
    judgedBy: { chain: 'run-evidence.ts', card: 'AC-G3-03', segment: '实时链路的故障注入段' },
    note:
      '本轮实跑判定成立（轮 2 装配了受控故障注入）：apply 返回 503 + injected_fault，' +
      '读回证明项目 revision、模板引用与 ImpactTask 集合均未变、变更集未变成 applied。' +
      '轮 1（未装配注入）apply 返回 HTTP 200，未观测到写入失败，该轮按口径记 NOT RUN、不判 PASS。' +
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
    evidenceKind: 'historical_snapshot',
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
    evidenceKind: 'historical_snapshot',
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
    evidenceKind: 'historical_snapshot',
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
    evidenceKind: 'historical_snapshot',
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
    // 本轮实跑判定成立（#318 / #319）：台面造出「待复核」与「版本落后」两种前置，
    // 服务端给出 required_route_not_current 阻断，项目没有进入 STABILIZED / READY。
    // 行状态记 PASS (follow-up)：缺口清单 #246 未关闭前，带缺口的行不判 PASS。
    status: 'PASS (follow-up)',
    evidence: '实现缺口 · 缺口清单 #246',
    evidenceKind: 'this_run',
    judgedBy: { chain: 'run-evidence.ts', card: 'AC-G3-08', segment: '实时链路的 roadmap 段' },
    note:
      '路线图的版本化编辑会话与节点写入**已实现**，验收台链路也已补上（#295）；' +
      '演示模板新增不可变 v2 并声明 `route-r7` 为必需节点（#314），服务端按模板规则算出 required=true。' +
      '**本轮实跑判定成立**：台面造出「待复核」与「版本落后」两种前置，' +
      '稳定化给出 required_route_not_current 阻断，项目没有进入 STABILIZED / READY' +
      '（#318 修掉了链内 409「基线已漂移」）。' +
      '「缺失」那一种前置由服务端侧保证（#319：已声明但缺失的路线节点同样给 blocking）。' +
      '行状态记 PASS (follow-up)：缺口清单 #246 未关闭前，带缺口的行不判 PASS。' +
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
    evidenceKind: 'historical_snapshot',
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
    evidenceKind: 'historical_snapshot',
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
    evidenceKind: 'historical_snapshot',
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
    evidenceKind: 'historical_snapshot',
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
    evidenceKind: 'historical_snapshot',
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
    // 本轮实跑判定成立（#320）：台面自己跑出真实模型轮次并读回两组说明。
    // 结论**依赖真实模型**（非确定性），按仓库口径记 WARNING，不写 PASS。
    status: 'WARNING',
    evidence: '实现缺口 · 缺口清单 #246',
    evidenceKind: 'this_run',
    judgedBy: { chain: 'run-model.ts', card: 'AC-G3-14' },
    note:
      '助手草稿里的「确定受影响 / 可能受影响」**解释分组已实现**，验收台读回链路也已补上（#295）；' +
      '模型边界已放宽（#315：缺两组说明时不再整条拒，改为如实标注「缺说明」）。' +
      '**本轮实跑判定成立**（#320）：台面走「自然语言 → 真实模型轮次 → 变更提案 → 人工确认落草稿 → ' +
      '读回两组说明 → 进入正式评估」，读回「确定受影响」1 条，每条带可追溯依据。' +
      '结论**依赖真实模型**（非确定性），按仓库口径记 `WARNING`，不写 `PASS`；' +
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
    // 本轮实跑判定成立（#321）：三态齐备 + 三条语义都有事实。
    // 行状态记 PASS (follow-up)：缺口清单 #246 未关闭前，带缺口的行不判 PASS。
    status: 'PASS (follow-up)',
    evidence: '实现缺口 · 缺口清单 #246',
    evidenceKind: 'this_run',
    judgedBy: { chain: 'run-dependencies.ts', card: 'AC-G3-15' },
    note:
      '依赖边的确认 / 修改 / 拒绝三态有端点，依赖候选的批量提出也已实现（#252）。' +
      '**本轮实跑判定成立**（#321）：三态齐备（确认 / 修改 / 拒绝），' +
      '拒绝不产生边、同版本重投被抑制（`rejected_in_same_version`）、' +
      '确认时版本复检被服务端按 409 `dependency_candidate_requires_reevaluation` 拒掉。' +
      '行状态记 PASS (follow-up)：缺口清单 #246 未关闭前，带缺口的行不判 PASS。' +
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
    // 本轮实跑判定成立（#318）：取消未冻结编辑后回到上一个冻结版本，临时版本被释放。
    // 行状态记 PASS (follow-up)：缺口清单 #246 未关闭前，带缺口的行不判 PASS。
    status: 'PASS (follow-up)',
    evidence: '实现缺口 · 缺口清单 #246',
    evidenceKind: 'this_run',
    judgedBy: { chain: 'run-evidence.ts', card: 'AC-G3-16', segment: '实时链路的 roadmap 段' },
    note:
      '路线图的版本化编辑会话与节点写入**已实现**（#250），台面链路已补上（#295）。' +
      '**本轮实跑判定成立**（#318）：取消未冻结编辑后冻结基线版本回到取消前的版本，' +
      '被取消会话占用的临时版本不出现在冻结版本序列里；' +
      '本轮没有指向临时版本的任务（产品面上没有造出这种任务的路径，' +
      '任务落 STALE 的非真空证据在服务端侧）。' +
      '行状态记 PASS (follow-up)：缺口清单 #246 未关闭前，带缺口的行不判 PASS。' +
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
