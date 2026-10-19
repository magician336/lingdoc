/**
 * G3 验收台-8 · 实现缺口清单（前端侧的可读副本）。
 *
 * 权威副本是缺口清单 Issue #246；这里是**同一份内容的结构化形态**，
 * 供页面渲染与导出体引用，避免页面上再抄一遍散文。
 *
 * 两条纪律：
 *
 * 1. **每条缺口都必须写清三件事**：缺什么、对应 Spec 位置、解除条件。
 *    只写「没做」的缺口不可行动。
 * 2. **缺口对应行的状态不写 `PASS`**：缺能力记 `BLOCKED`，部分覆盖记
 *    `PASS (follow-up)`，宿主环境限制记 `WARNING`，未落证记 `NOT RUN`
 *    （`docs/agents/development-rules.md:31`）。
 */

import { G3_MAIN_BASELINE_PATH, type SpecAnchor } from './g3Matrix'
import type { G3Status } from './g3Status'

/** 缺口清单 Issue（常驻跟踪票，缺口未补齐前保持 open）。 */
export const G3_GAP_LIST_ISSUE = 246

/** 一条实现缺口。 */
export interface G3ImplementationGap {
  /** 缺口编号（与 #246 正文一致）。 */
  id: string
  /** 缺口标题。 */
  title: string
  /**
   * **直接缺口行**：因缺能力而无法实跑、状态不写 `PASS` 的矩阵行。
   *
   * 与 `limits` 分开，是为了不把「覆盖受限」误说成「没跑过」——
   * 判据本身已判定成立、但某类证据没覆盖到，属于 `limits`。
   */
  rows: readonly string[]
  /** 覆盖受限的判据：判据已判定，但某类证据 / 分支本轮没覆盖到。 */
  limits: readonly string[]
  /** 缺什么。 */
  missing: string
  /** 对应 Spec 位置。 */
  specAnchor: SpecAnchor
  /** 解除条件。 */
  unblockCondition: string
  /** 缺口清单 Issue。 */
  issue: number
  /** 该缺口对应行在矩阵中的状态（绝不会是 `PASS`）。 */
  rowStatus: G3Status
  /** 实测事实（若有）。 */
  fact?: string
}

const anchor = (start: number, end: number = start): SpecAnchor => ({
  path: G3_MAIN_BASELINE_PATH,
  start,
  end,
})

/** 全部实现缺口。顺序与 #246 正文的编号顺序一致。 */
export const G3_IMPLEMENTATION_GAPS: readonly G3ImplementationGap[] = Object.freeze([
  {
    id: 'G3-GAP-01',
    title: '应用原子性缺故障注入能力',
    rows: ['AC-G3-03'],
    limits: [],
    missing:
      '产品 API 缺「任务 / 审计写入失败」的**故障注入**能力：' +
      '无法在 `apply` 过程中强制制造下游 ImpactTask 写入失败或权限失效，' +
      '因而无法从 HTTP 面观测「整体回滚」。',
    specAnchor: anchor(358, 360),
    unblockCondition:
      '提供一个受控且默认关闭的故障注入开关（只对测试 / 演示租户开放），' +
      '能在 `apply` 事务中令任务写入或审计写入失败，并读回证明项目 revision 未变、' +
      '模板引用未变、ImpactTask 集合完整；补齐后由验收台实跑落证。',
    issue: G3_GAP_LIST_ISSUE,
    rowStatus: 'BLOCKED',
    fact:
      '写路由 `POST /projects/{projectId}/change-sets/{changeSetId}/apply` 存在，' +
      '但请求体不受控于任何失败开关；该原子性边界当前仅由后端 Go 集成测试覆盖，' +
      '**不能**冒充页面实跑。',
  },
  {
    id: 'G3-GAP-02',
    title: 'RouteNode 只有只读接口，造不出「必需项缺失」与「版本落后」',
    rows: ['AC-G3-08'],
    limits: [],
    missing:
      '技术路线节点**没有写入端点**（只有 `GET /projects/{projectId}/route-nodes`），' +
      '无法构造模板声明的必需 RouteNode（R7）处于「缺失 / 待复核 / 版本落后」三种前置。',
    specAnchor: anchor(378, 380),
    unblockCondition:
      '补 RouteNode 写入端点（覆盖「写入 / 更新节点版本」与「把节点置于待复核或缺失状态」），' +
      '使页面能搭出 R7 缺失与 R7 版本落后两种前置，并观测到 blocking ValidationIssue ' +
      '与稳定化 / 导出被阻断。',
    issue: G3_GAP_LIST_ISSUE,
    rowStatus: 'BLOCKED',
    fact: '路线图侧当前只暴露只读端点，既不能制造缺失节点，也不能把节点版本落后到基线以下。',
  },
  {
    id: 'G3-GAP-03',
    title: '缺 RouteNode 写入与「取消未冻结编辑」端点',
    rows: ['AC-G3-16'],
    limits: [],
    missing:
      '缺技术路线节点的**写入**端点，也缺「取消未冻结路线图编辑」端点：' +
      '既造不出临时版本，也无法验证恢复到上一个 RouteNode / RouteEdge 版本。',
    specAnchor: anchor(410, 412),
    unblockCondition:
      '补 RouteNode 写入端点与取消未冻结编辑端点（并保证取消后关联 ImpactTask 与章节引用' +
      '不指向被取消的临时版本），补齐后由 AC-G3-16 场景在验收台实跑落证。',
    issue: G3_GAP_LIST_ISSUE,
    rowStatus: 'BLOCKED',
    fact: '也没有任何返回 RouteNode 版本序列的读端点，无法断言「恢复到上一版本」。',
  },
  {
    id: 'G3-GAP-04',
    title: '助手输出缺「确定受影响 / 可能受影响」解释分组',
    rows: ['AC-G3-14'],
    limits: [],
    missing:
      '**变更草稿生成有端点**，但助手产出里的「确定受影响」与「可能受影响」两组说明' +
      '（含各自的解释原因）**无实现**。',
    specAnchor: anchor(402, 404),
    unblockCondition:
      '让助手草稿产出携带「确定受影响 / 可能受影响」两组对象与各自理由' +
      '（与 `impacts[].certainty` 显式区分），并保证助手不直接应用变更 / 关闭任务 / 放行导出。',
    issue: G3_GAP_LIST_ISSUE,
    rowStatus: 'PASS (follow-up)',
    fact:
      '工作台 `ImpactOpsPanel.vue:101-102` 的分组是**影响解析结果**的分组（`impacts[].certainty`），' +
      '**不是** AC-G3-14 要求的**助手草稿解释分组**，两者不可互替。',
  },
  {
    id: 'G3-GAP-05',
    title: '依赖候选无批量提出端点',
    rows: ['AC-G3-15'],
    limits: [],
    missing:
      '依赖边的**确认 / 修改 / 拒绝三态有端点**，但「AI 批量提出候选依赖」**无端点** —— ' +
      '没有能一次提出若干候选依赖、并带对象版本 / 依据片段 / 建议理由的接口。',
    specAnchor: anchor(406, 408),
    unblockCondition:
      '补一个批量提出候选依赖的端点（返回候选集合 + 对象版本 + 依据片段 + 建议理由，' +
      '可与既有 `decision` 三态对接），补齐后由 AC-G3-15 场景在验收台实跑落证。',
    issue: G3_GAP_LIST_ISSUE,
    rowStatus: 'PASS (follow-up)',
    fact: '当前只能人工构造建议边，无法实跑「AI 批量提出」那一段。',
  },
  {
    id: 'G3-GAP-06',
    title: '影响中心：工作台已补齐，验收台页面的实跑证据待落',
    rows: ['G3.7'],
    limits: ['AC-G3-04'],
    missing:
      '「本次必须处理的对象」集合（`assessment.objects`）在**验收台页面**尚未落证：' +
      '工作台面板已由 `2786bcc` 补齐渲染，缺的是验收台页面自己读原始响应的实跑证据。',
    specAnchor: anchor(346),
    unblockCondition:
      '由验收台页面直接读 `assessment.objects` 渲染并落证（页面自己读 API 返回，' +
      '不改工作台面板的 props 与行为）；若按对象 `state` / `confirmed` / `context_revision` ' +
      '逐字段核对后仍有未渲染的子集，则回到本条继续跟踪。',
    issue: G3_GAP_LIST_ISSUE,
    rowStatus: 'NOT RUN',
    fact:
      '工作台面板 `ImpactOpsPanel.vue:42` 已渲染「本次必须处理的对象」区块' +
      '（`2786bcc` 是当前 head 的祖先）；**未落证的是验收台页面**，不是工作台。',
  },
  {
    id: 'G3-GAP-07',
    title: '影响中心：工作台已补齐，来源对象与确定 / 可能影响分组的验收台实跑证据待落',
    rows: ['G3.7'],
    limits: ['AC-G3-04'],
    missing:
      '「来源对象」（`impacts[].causes[].source`）与「确定影响 / 可能影响」分组' +
      '在**验收台页面**尚未落证：工作台面板已由 `2786bcc` 补齐渲染，缺的是验收台页面的实跑证据。',
    specAnchor: anchor(346),
    unblockCondition:
      '由验收台页面按 `certainty` 分两组渲染 `impacts[]` 并逐条渲染 `causes[].source`' +
      '（含对象 kind / id / version），落证后方可判定。',
    issue: G3_GAP_LIST_ISSUE,
    rowStatus: 'NOT RUN',
    fact:
      '工作台面板 `ImpactOpsPanel.vue:42` 已渲染来源对象、`:101-102` 已按 `certainty` ' +
      '分「确定影响 / 可能影响」两段；**未落证的是验收台页面**，不是工作台。',
  },
  {
    id: 'G3-GAP-08',
    title: '本机未配检索渠道，调研类闭环证据不可实跑',
    rows: [],
    limits: ['G3.6'],
    missing:
      '本机 `web_search_providers` 表为空（父会话复核 `select count(*)` = 0），' +
      '`POST /projects/{id}/research-tasks` 在没有 asset 且不传 `provider_id` 时返回 ' +
      '400 `invalid_request`，补 `provider_id` 后返回 500 `internal_error` —— ' +
      '`RETRIEVE_CANDIDATES` 类影响任务要求的真实 `ResearchRun` 回执**不可实跑**。',
    specAnchor: anchor(345),
    unblockCondition:
      '先在租户里配好检索渠道凭据（或给一个真实 `asset`），再重跑调研链；' +
      '在解除之前这一项只能记环境限制（`WARNING` / `NOT RUN`）并写明缺什么，' +
      '**不得**用其他回执冒充。',
    issue: G3_GAP_LIST_ISSUE,
    rowStatus: 'WARNING',
    fact: '因此 G3.6 的闭环证据只覆盖 INVALIDATE_CONFIRMATION / REVIEW_REQUIRED / RECOMPUTE 三类动作。',
  },
  {
    id: 'G3-GAP-09',
    title: '阻断级规则问题「接受不处理」（dismiss）返回 500',
    rows: ['AC-G3-09'],
    limits: [],
    missing:
      '对**阻断级**规则问题调用 disposition 的 `action=dismiss` 时，服务端返回 ' +
      '500 `internal_error`（`resolve` 返回 201、`waive` 返回 400），' +
      '「接受不处理」这条路径在阻断级上不可用。',
    specAnchor: anchor(382, 384),
    unblockCondition:
      '修掉阻断级规则问题 dismiss 的 500（让「接受不处理」要么按语义被拒（4xx）并给出可读原因，' +
      '要么对规则问题开放），并由 AC-G3-09 场景实跑落证。',
    issue: G3_GAP_LIST_ISSUE,
    rowStatus: 'PASS (follow-up)',
    fact: '实测：dismiss → HTTP 500 code=internal_error；resolve → HTTP 201；waive → HTTP 400。',
  },
  {
    id: 'G3-GAP-10',
    title: '求值不读 disposition：「问题不再阻断当次求值」那半段不成立',
    rows: ['AC-G3-09'],
    limits: [],
    missing:
      '`delivery.Evaluate` 与 `TemplateGovernanceRuntime.Check` **都不读 disposition**：' +
      '人工处置只是工作流元数据，处置后重检的求值结论不变（blocked → blocked），' +
      '因此 AC-G3-09 的「问题不再阻断当次求值」那半段在当前后端上不成立。',
    specAnchor: anchor(382, 384),
    unblockCondition:
      '让求值在读规则问题时考虑该问题的 disposition（被 dismiss 的规则问题不再阻断当次求值，' +
      '但对应 ImpactTask 仍为 OPEN、项目仍不能稳定），并由 AC-G3-09 场景实跑落证。',
    issue: G3_GAP_LIST_ISSUE,
    rowStatus: 'PASS (follow-up)',
    fact:
      '实测：处置（HTTP 201，问题带上处置）之后重检 `POST /projects/{id}/checks` 仍为 ' +
      'status=blocked；稳定化仍为 BLOCKED（阻断原因 5 条），对应 ImpactTask 仍为 OPEN —— ' +
      '「规则问题与任务分离」那半段成立。',
  },
  {
    id: 'G3-GAP-11',
    title: '目标对象版本被替换后稳定化直接 409，closure_stale 分支不可达',
    rows: [],
    limits: ['AC-G3-12'],
    missing:
      '目标对象在任务「已完成」之后被新版本替换时，稳定化在**预读闭环证据**阶段就直接返回 ' +
      '409 `version_conflict`，`closure_stale` 分支实际不可达 —— ' +
      'AC-G3-12 要求的「保持 BLOCKED 并创建 / 重投对应任务」这半段没有可观测的落点。',
    specAnchor: anchor(394, 396),
    unblockCondition:
      '让稳定化在版本不匹配时落到 `closure_stale` 分支（保持 BLOCKED 并创建 / 重投任务），' +
      '而不是在预读闭环证据时直接 409；补齐后由 AC-G3-12 场景实跑落证。',
    issue: G3_GAP_LIST_ISSUE,
    rowStatus: 'PASS (follow-up)',
    fact:
      '实测：复写章节版本替换目标对象后 `POST .../stabilize` → HTTP 409 code=version_conflict' +
      '（阻断原因 0 条、失效任务 0 条）；本轮改走「按新基线重投变更集 → 重投任务闭环 → 再次稳定化」' +
      '才回到 STABILIZED。',
  },
])

/** 某一行涉及的直接缺口（因缺能力而无法实跑）。 */
export function gapsForRow(rowId: string): G3ImplementationGap[] {
  return G3_IMPLEMENTATION_GAPS.filter((gap) => gap.rows.includes(rowId))
}

/** 某一行涉及的直接缺口编号。 */
export function gapIdsForRow(rowId: string): string[] {
  return gapsForRow(rowId).map((gap) => gap.id)
}

/** 某一行涉及的「覆盖受限」缺口（判据已判定，但某类证据 / 分支没覆盖到）。 */
export function limitingGapsForRow(rowId: string): G3ImplementationGap[] {
  return G3_IMPLEMENTATION_GAPS.filter((gap) => gap.limits.includes(rowId))
}

/** 某一行涉及的「覆盖受限」缺口编号。 */
export function limitingGapIdsForRow(rowId: string): string[] {
  return limitingGapsForRow(rowId).map((gap) => gap.id)
}
