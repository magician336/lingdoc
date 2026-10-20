/**
 * G3 验收台-8 · 实现缺口清单（前端侧的可读副本）。
 *
 * 权威副本是缺口清单 Issue #246；这里是**同一份内容的结构化形态**，
 * 供页面渲染与导出体引用，避免页面上再抄一遍散文。
 *
 * **仓库内镜像**：`docs/08-本轮实施方案/G3-缺口清单-246-副本.md` 是 #246 正文的
 * 仓库内镜像，供离线守护用（`g3Gaps.test.ts` 的「同源守护」断言直接读它，
 * 逐条比对编号、标题、对应验收行、当前状态与承接票）。改动这里的字段时，
 * 同一份事实要同步进那份镜像；两者不一致时以镜像为准。
 *
 * **事实基线**：验收台在 head `a87459f` 的实跑结论（#257）。本文件按该结果写，
 * 不再写「未实现 / 待当前 head 落证」这类已被实跑作废的旧口径。
 *
 * 两条纪律：
 *
 * 1. **每条缺口都必须写清三件事**：缺什么、对应 Spec 位置、解除条件。
 *    只写「没做」的缺口不可行动。
 * 2. **缺口对应行的状态不写 `PASS`**：缺能力记 `BLOCKED`，部分覆盖记
 *    `PASS (follow-up)`，宿主环境限制记 `WARNING`，未落证记 `NOT RUN`
 *    （`docs/agents/development-rules.md` 的门禁词表）。
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
  /**
   * 承接本缺口的子票号。
   *
   * 缺口清单 Issue #246 是**权威副本**，本文件是它的结构化形态；子票是
   * 「谁来做」的落点。开发计划见
   * `docs/06-灵档产品开发规划/beta-gap-development-plans/G3-gap-closure-development-plan.md`。
   */
  ticket?: number
  /** 该缺口对应行在矩阵中的状态（绝不会是 `PASS`）。 */
  rowStatus: G3Status
  /** 实测事实（若有）。 */
  fact?: string
  /**
   * 该缺口指向的能力**是否已经实现**。
   *
   * 「缺什么」写的是**缺口还差哪一步**，不是「产品有没有这个能力」——
   * 能力已实现、只差验收台链路时，这里是 `true`。
   * 宿主环境限制型缺口（G3-GAP-08）不设这个字段：它卡在环境，不在实现。
   */
  implemented?: boolean
  /** 支撑该缺口的证据文件（仓库内相对路径）。 */
  evidence?: string
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
      '受控故障注入能力**已经实现**；缺的是**验收台链路**：' +
      '验收台没有能触发「任务写入失败 / 权限失效」并从 HTTP 面观测「整体回滚」的步骤，' +
      '所以行 `AC-G3-03` 仍为 `BLOCKED`。',
    specAnchor: anchor(358, 360),
    unblockCondition:
      '由 #295 给验收台补受控故障注入的可观测步骤（触发失败 → 读回项目 revision 未变、' +
      '模板引用未变、ImpactTask 集合完整），并在当前 head 实跑落证；' +
      '落证后 `AC-G3-03` 才能离开 `BLOCKED`。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 256,
    rowStatus: 'BLOCKED',
    fact:
      '受控故障注入**已实现**（受控、默认关闭、只对测试 / 演示租户开放；' +
      '由组合根按环境变量与演示租户校验装配会失败的窄 port，' +
      'HTTP 路由与产品契约零新增端点，`apply` 走真事务、失败真发生）。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-7-验证报告-20261020.json`（#256）。' +
      '卡点在验收台没有对应链路 → #295；行 `AC-G3-03` 仍 `BLOCKED`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-7-验证报告-20261020.json',
  },
  {
    id: 'G3-GAP-02',
    title: 'RouteNode 只有只读接口，造不出「必需项缺失」与「版本落后」',
    rows: ['AC-G3-08'],
    limits: [],
    missing:
      '路线图的**版本化编辑会话模块已经实现**；缺的是**验收台链路**：' +
      '验收台没有覆盖「提案 / 提交 / 取消」与版本比较的步骤，' +
      '所以行 `AC-G3-08` 仍为 `BLOCKED`。',
    specAnchor: anchor(378, 380),
    unblockCondition:
      '由 #295 给验收台补路线图编辑会话（提案 / 提交 / 取消 / 版本比较）的步骤，' +
      '并在当前 head 实跑落证；落证后 `AC-G3-08` 才能离开 `BLOCKED`。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 250,
    rowStatus: 'BLOCKED',
    fact:
      '路线图版本化编辑会话**已实现**（提案不进冻结路线图、提交冻结新版本并产生复核任务、' +
      '取消恢复基线并释放临时引用、版本序列与比较、' +
      '三种必需节点前置给出 blocking ValidationIssue）。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-1-验证报告-20261020.json`（#250）。' +
      '原先「只有只读端点」的旧事实已作废；' +
      '卡点在验收台没有对应链路 → #295；行 `AC-G3-08` 仍 `BLOCKED`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-1-验证报告-20261020.json',
  },
  {
    id: 'G3-GAP-03',
    title: '缺 RouteNode 写入与「取消未冻结编辑」端点',
    rows: ['AC-G3-16'],
    limits: [],
    missing:
      'RouteNode 写入与「取消未冻结编辑」**已经实现**；缺的是**验收台链路**：' +
      '验收台没有覆盖版本比较与恢复的步骤，所以行 `AC-G3-16` 仍为 `BLOCKED`。',
    specAnchor: anchor(410, 412),
    unblockCondition:
      '由 #295 给验收台补路线图版本比较与恢复的步骤（取消编辑后回到上一个 RouteNode / ' +
      'RouteEdge 版本，关联 ImpactTask 与章节引用不指向被取消的临时版本），' +
      '并在当前 head 实跑落证；落证后 `AC-G3-16` 才能离开 `BLOCKED`。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 250,
    rowStatus: 'BLOCKED',
    fact:
      'RouteNode 写入与「取消未冻结编辑」**已实现**（同一项目只允许一个未冻结会话；' +
      '取消恢复基线并释放临时版本引用）。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-1-验证报告-20261020.json`（#250）。' +
      '原先「没有写入端点」的旧事实已作废；' +
      '卡点在验收台没有对应链路 → #295；行 `AC-G3-16` 仍 `BLOCKED`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-1-验证报告-20261020.json',
  },
  {
    id: 'G3-GAP-04',
    title: '助手输出缺「确定受影响 / 可能受影响」解释分组',
    rows: ['AC-G3-14'],
    limits: [],
    missing:
      '助手草稿里的「确定受影响 / 可能受影响」两组说明**已经实现**；' +
      '缺的是**验收台未覆盖读侧**：验收台没有把草稿里的两组说明读回来核对，' +
      '所以行 `AC-G3-14` 仍为 `BLOCKED`。',
    specAnchor: anchor(402, 404),
    unblockCondition:
      '由 #295 给验收台补助手草稿两组说明的读回步骤（草稿里读 `definite_affected` / ' +
      '`possible_affected`，与 `impacts[].certainty` 显式区分），并在当前 head 实跑落证；' +
      '落证后 `AC-G3-14` 才能离开 `BLOCKED`。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 251,
    rowStatus: 'BLOCKED',
    fact:
      '助手两组说明**已实现**（助手侧用 `definite_affected` / `possible_affected`，' +
      '服务端从已落库的助手 turn 重新推导，每条说明必须同时有对象引用、原因与依据）。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-8-验证报告-20261020.json`（#251）。' +
      '按 `GLOSSARY.md`：助手的影响说明只是**助手的解读**，始终随草稿存在，' +
      '须经**人工确认**后才进入正式评估。' +
      '工作台 `ImpactOpsPanel.vue` 的分组是**影响解析结果**的分组（`impacts[].certainty`），' +
      '**不是** AC-G3-14 要求的**助手草稿解释分组**，两者不可互替。' +
      '卡点在验收台未覆盖读侧 → #295；行 `AC-G3-14` 仍 `BLOCKED`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-8-验证报告-20261020.json',
  },
  {
    id: 'G3-GAP-05',
    title: '依赖候选无批量提出端点',
    rows: ['AC-G3-15'],
    limits: [],
    missing:
      '依赖候选的**批量提出已经实现**；缺的是**验收台未覆盖读侧**：' +
      '验收台没有把批量候选与既有决策三态的读回核对，' +
      '所以行 `AC-G3-15` 仍为 `BLOCKED`。',
    specAnchor: anchor(406, 408),
    unblockCondition:
      '由 #295 给验收台补候选批量读回与既有 `decision` 三态对接的步骤，' +
      '并在当前 head 实跑落证；落证后 `AC-G3-15` 才能离开 `BLOCKED`。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 252,
    rowStatus: 'BLOCKED',
    fact:
      '依赖候选批量提出**已实现**（一次提出若干候选依赖，候选与已确认边**分表存放**，' +
      '候选带对象版本、依据片段与建议理由，可与既有 `decision` 三态对接）。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-3-验证报告-20261020.json`（#252）。' +
      '卡点在验收台未覆盖读侧 → #295；行 `AC-G3-15` 仍 `BLOCKED`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-3-验证报告-20261020.json',
  },
  {
    id: 'G3-GAP-06',
    title: '影响中心：必需对象集合已落证，矩阵行 G3.7 仍未判定',
    rows: ['G3.7'],
    limits: ['AC-G3-04'],
    missing:
      '「本次必须处理的对象」集合（`assessment.objects`）的渲染能力**已实现，' +
      '且已在当前 head 落证**（运行标识 `g3r-mvgqpt88-7b4e074d`，head `a87459f`）；' +
      '缺的是把这份落证并入矩阵行 `G3.7` 的完成判据判定，该行当前仍记 `NOT RUN`。',
    specAnchor: anchor(346),
    unblockCondition:
      '把本次落证并入 `G3.7` 的完成判据判定（验收台页面自己读 `assessment.objects` 渲染，' +
      '不改工作台面板的 props 与行为，并按对象 `state` / `confirmed` / `context_revision` ' +
      '逐字段核对）；判定成立前本行保持 `NOT RUN`。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 257,
    rowStatus: 'NOT RUN',
    fact:
      '渲染能力**已实现并已在当前 head 落证**：运行标识 `g3r-mvgqpt88-7b4e074d`、' +
      'head `a87459f`，证据 ' +
      '`docs/08-本轮实施方案/G3-验收台-8-证据-g3r-mvgqpt88-7b4e074d.json`（#257）。' +
      '验收台 `G3Demo.vue` 已渲染 `assessment.objects`，工作台面板 `ImpactOpsPanel.vue` ' +
      '也已渲染「本次必须处理的对象」区块。' +
      '矩阵行 `G3.7` 仍记 `NOT RUN`；覆盖受限行 `AC-G3-04` 记 `PASS (follow-up)`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-验收台-8-证据-g3r-mvgqpt88-7b4e074d.json',
  },
  {
    id: 'G3-GAP-07',
    title: '影响中心：来源对象与确定/可能影响分组已落证，矩阵行 G3.7 仍未判定',
    rows: ['G3.7'],
    limits: ['AC-G3-04'],
    missing:
      '「来源对象」（`impacts[].causes[].source`）与「确定影响 / 可能影响」分组的渲染能力' +
      '**已实现，且已在当前 head 落证**（运行标识 `g3r-mvgqpt88-7b4e074d`，head `a87459f`）；' +
      '缺的是把这份落证并入矩阵行 `G3.7` 的完成判据判定，该行当前仍记 `NOT RUN`。',
    specAnchor: anchor(346),
    unblockCondition:
      '把本次落证并入 `G3.7` 的完成判据判定（验收台页面按 `certainty` 分两组渲染 `impacts[]` ' +
      '并逐条渲染 `causes[].source`，含对象 kind / id / version）；' +
      '判定成立前本行保持 `NOT RUN`。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 257,
    rowStatus: 'NOT RUN',
    fact:
      '渲染能力**已实现并已在当前 head 落证**：运行标识 `g3r-mvgqpt88-7b4e074d`、' +
      'head `a87459f`，证据 ' +
      '`docs/08-本轮实施方案/G3-验收台-8-证据-g3r-mvgqpt88-7b4e074d.json`（#257）。' +
      '验收台 `G3Demo.vue` 已渲染来源对象并按 `certainty` 分「确定影响 / 可能影响」两段；' +
      '工作台面板 `ImpactOpsPanel.vue` 同样在位。' +
      '矩阵行 `G3.7` 仍记 `NOT RUN`；覆盖受限行 `AC-G3-04` 记 `PASS (follow-up)`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-验收台-8-证据-g3r-mvgqpt88-7b4e074d.json',
  },
  {
    id: 'G3-GAP-08',
    title: '本机未配检索渠道，调研类闭环证据不可实跑',
    rows: [],
    limits: ['G3.6'],
    missing:
      '宿主环境缺检索渠道：本机 `web_search_providers` 表为空（父会话复核 ' +
      '`select count(*)` = 0），没有 asset 且不传 `provider_id` 时 ' +
      '`POST /projects/{id}/research-tasks` 返回 400 `invalid_request`，' +
      '补 `provider_id` 后返回 500 `internal_error` —— ' +
      '`RETRIEVE_CANDIDATES` 类影响任务要求的真实 `ResearchRun` 回执**造不出来**。',
    specAnchor: anchor(345),
    unblockCondition:
      '先在租户里配好检索渠道凭据（或给一个真实 `asset`），再重跑调研链；' +
      '在解除之前这一项只能记环境限制（矩阵行 `G3.6` 尚未判定、记 `TODO`，' +
      '本缺口记 `WARNING`）并写明缺什么，**不得**用其他回执冒充。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 258,
    rowStatus: 'WARNING',
    fact:
      '因此 G3.6 的闭环证据只覆盖 INVALIDATE_CONFIRMATION / REVIEW_REQUIRED / RECOMPUTE ' +
      '三类动作；矩阵行 `G3.6` 仍是 `TODO`（尚未判定），' +
      '本缺口按宿主环境限制记 `WARNING`，不产出冒充证据。',
  },
  {
    id: 'G3-GAP-09',
    title: '阻断级规则问题「接受不处理」（dismiss）返回 500 的缺陷',
    rows: ['AC-G3-09'],
    limits: [],
    missing:
      '对**阻断级**规则问题调用 disposition 的 `action=dismiss` 时，服务端曾返回 ' +
      '500 `internal_error`（`resolve` 返回 201、`waive` 返回 400），' +
      '「接受不处理」这条路径在阻断级上不可用。' +
      '**根因已定位并修复（#253，2026-10-20）**；当前缺的是 AC-G3-09 场景在当前 head 的实跑落证。',
    specAnchor: anchor(382, 384),
    unblockCondition:
      '由验收台在当前 head 重跑 AC-G3-09 场景落证（阻断级 dismiss 被受理、处置落库、' +
      '审计行可读）；「不再阻断当次求值」那半段由 #254 负责，' +
      '在两半都成立前本行只能是 `PASS (follow-up)`。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 253,
    rowStatus: 'PASS (follow-up)',
    fact:
      '根因：授权审计行把「动作 + 资源 ID」的复合 capability 写进了有界的 action 分类列' +
      '（`lingdoc_project_audits.action`，PostgreSQL `varchar(64)`），' +
      '阻断级 dismiss 的 action 长 73 字符 → PostgreSQL 报 SQLSTATE 22001 → ' +
      '事务回滚 → 未映射的原始错误变成 HTTP 500；resolve 的 action 恰好 64 字符压线通过。' +
      '**SQLite 迁移把该列声明为无界 TEXT，所以缺陷只在 PostgreSQL 暴露。**' +
      '修复：action 列只写分类（`authorization.issue-dismiss-blocking`，36 字符），' +
      '完整 capability 留在 `details.capability`；审计写失败改为自带对外形态的领域错误。' +
      '已在隔离 PostgreSQL（生产迁移）上复现（修复前 22001）并复验（修复后 201）。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-4-验证报告-20261020.json`（#253）；' +
      '行 `AC-G3-09` 记 `PASS (follow-up)`（页面实跑落证前不得记 `PASS`）。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-4-验证报告-20261020.json',
  },
  {
    id: 'G3-GAP-10',
    title: '求值不读 disposition：「问题不再阻断当次求值」那半段缺失',
    rows: ['AC-G3-09'],
    limits: [],
    missing:
      '`delivery.Evaluate` 与 `TemplateGovernanceRuntime.Check` 此前**都不读 disposition**：' +
      '人工处置只是工作流元数据，处置后重检的求值结论不变（blocked → blocked），' +
      '因此 AC-G3-09 的「问题不再阻断当次求值」那半段不成立。' +
      '**现已改为两阶段求值**，`delivery.BlockingIssues` 是唯一阻断集判定函数；' +
      '当前缺的是该 HTTP 场景在当前 head 的实跑落证。',
    specAnchor: anchor(382, 384),
    unblockCondition:
      '让求值在读规则问题时考虑该问题的 disposition（被 dismiss 的规则问题不再阻断当次求值，' +
      '但对应 ImpactTask 仍为 OPEN、项目仍不能稳定），并由 AC-G3-09 场景实跑落证。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 254,
    rowStatus: 'PASS (follow-up)',
    fact:
      '已改为两阶段求值，`delivery.BlockingIssues` 是唯一阻断集判定函数：' +
      '**阻断级问题被「误报删除」（dismiss）后不再阻断当次求值**；' +
      '「修正」（resolve）与「豁免」（waive）**不解除阻断**。' +
      'dismiss 不关闭 ImpactTask、不解除稳定化阻断。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-5-验证报告-20261020.json`（#254）；' +
      '决策与术语见 `docs/adr/0004-误报删除进入规则问题求值.md` 与 `GLOSSARY.md`。' +
      '行 `AC-G3-09` 记 `PASS (follow-up)`（页面实跑落证前不得记 `PASS`）。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-5-验证报告-20261020.json',
  },
  {
    id: 'G3-GAP-11',
    title: '目标对象版本被替换后稳定化直接 409，closure_stale 分支在该链路上到不了',
    rows: [],
    limits: ['AC-G3-12'],
    missing:
      '目标对象在任务「已完成」之后被新版本替换时，稳定化在**预读闭环证据**阶段就直接返回 ' +
      '409 `version_conflict`，因此 AC-G3-12 要求的「保持 BLOCKED 并创建 / 重投对应任务」' +
      '这半段在该 HTTP 场景下没有可观测的落点。' +
      '**稳定化版本不匹配已落到 `closure_stale`**；该分支**存在**且有字面量断言，' +
      '准确说法是「该 HTTP 场景在到达该分支前被 409 拦掉」，不是「分支不可达」。',
    specAnchor: anchor(394, 396),
    unblockCondition:
      '让该 HTTP 场景在版本不匹配时走到 `closure_stale` 分支（保持 BLOCKED 并创建 / 重投任务），' +
      '而不是在预读闭环证据时直接 409；补齐后由 AC-G3-12 场景实跑落证。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 255,
    rowStatus: 'PASS (follow-up)',
    fact:
      '实测：复写章节版本替换目标对象后 `POST .../stabilize` → HTTP 409 code=version_conflict' +
      '（阻断原因 0 条、失效任务 0 条）；本轮改走「按新基线重投变更集 → 重投任务闭环 → 再次稳定化」' +
      '才回到 STABILIZED。`closure_stale` 分支本身**存在**且已有字面量断言' +
      '（`internal/lingdoc/workspacecore/stabilization_conditions.go`、' +
      '`internal/lingdoc/workspacecore/stabilization_guard_test.go`）—— ' +
      '准确说法是「该 HTTP 场景在到达该分支前被 409 拦掉」，而不是「分支不可达」。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-6-验证报告-20261020.json`（#255）；' +
      '覆盖受限行 `AC-G3-12` 记 `PASS (follow-up)`（页面实跑落证前不得记 `PASS`）。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-6-验证报告-20261020.json',
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
