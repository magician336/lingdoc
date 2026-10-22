/**
 * G3 验收台-8 · 实现缺口清单（前端侧的可读副本）。
 *
 * 权威副本是缺口清单 Issue #246；这里是**同一份内容的结构化形态**，
 * 供页面渲染与导出体引用，避免页面上再抄一遍散文。
 *
 * **仓库内镜像**：`docs/08-本轮实施方案/G3-缺口清单-246-副本.md` 是 #246 正文的
 * 仓库内镜像，供离线守护用（`implementationGaps.test.ts` 的「同源守护」断言直接读它，
 * 逐条比对编号、标题、对应验收行、当前状态与承接票）。改动这里的字段时，
 * 同一份事实要同步进那份镜像；两者不一致时以镜像为准。
 *
 * **#246 已关闭**（2026-10-21）：镜像里那张复制票态的「承接票清单」表已退役，
 * 票态的权威只有 GitHub 票面一处。每条缺口的承接票见镜像缺口清单表的「承接票」列。
 *
 * **事实基线**：本轮（切片七行接实跑判定那一轮）在当前 head 重跑了影响中心那条链，
 * 所以下面凡引用落证的地方都指名**落证文件与判定卡**；
 * `a87459f` 那一轮（#257）是**页面渲染**那一半的落证，两处不混读。
 *
 * **不写运行标识与 head**：运行标识每次重跑都变、后端 head 也会跟着变，
 * 写死等于写下**下一次就过期**的串，读者还取不到。
 * 这一次到底是哪一次运行、哪个 head，由运行上下文在页面与导出体里带出来。
 *
 * 两条纪律：
 *
 * 1. **每条缺口都必须写清三件事**：缺什么、对应 Spec 位置、解除条件。
 *    只写「没做」的缺口不可行动。
 * 2. **缺口对应行的状态不写 `PASS`**：缺能力记 `BLOCKED`，部分覆盖记
 *    `PASS (follow-up)`，覆盖受限（宿主环境限制或产品缺陷）记 `WARNING`，
 *    未落证记 `NOT RUN`（`docs/agents/development-rules.md` 的门禁词表）。
 */

import { G3_MAIN_BASELINE_PATH, type SpecAnchor } from './impactMatrix'
import type { G3Status } from './taskStatus'

/**
 * 缺口清单 Issue（#246，2026-10-21 已关闭）。
 *
 * 编号保留：页面与导出体引用的是**这张清单的历史**——11 条缺口、
 * 各自的状态与解除条件。票态的权威只有 GitHub 票面一处，本文件不复制。
 */
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
   * G3-GAP-08 不设这个字段：它缺的不是能力（能力侧无缺口），
   * 而是**产品走不到**已可用的外部渠道——设成 `true` 会让读者以为「已实现 = 已闭合」。
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
    title: '应用原子性：故障注入的可观测口径',
    rows: ['AC-G3-03'],
    limits: [],
    missing:
      '受控故障注入能力**已经实现**；**验收台链路也已补上**（#295）：' +
      '台面能触发「任务写入失败 / 权限失效」并从 HTTP 面观测「整体回滚」。' +
      '现在缺的是**注入命中之后能读回完整回滚证据**——命中之后后端的写操作被阻塞。',
    specAnchor: anchor(358, 360),
    unblockCondition:
      '本轮已达成（#310 已修、实跑已判成立）。行状态按缺口清单 #246 的规则保留 ' +
      '`PASS (follow-up)`：本票所列缺口对应的行在 #246 关闭前不判 `PASS`。' +
      '彻底收口要等 #246 关闭（其余十行各自补齐）。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 256,
    rowStatus: 'PASS (follow-up)',
    fact:
      '受控故障注入**已实现**（受控、默认关闭、只对测试 / 演示租户开放；' +
      '由组合根按环境变量与演示租户校验装配会失败的窄 port，' +
      'HTTP 路由与产品契约零新增端点，`apply` 走真事务、失败真发生）。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-7-验证报告-20261020.json`（#256）。' +
      '**本轮实跑判定成立**（head `cd69d7c`）：轮 1 未装配注入 → apply 返回 200，该行按口径记 `NOT RUN`；' +
      '轮 2 装配注入 → apply 返回 503 + `injected_fault`，读回证明项目 revision 与模板引用未变、' +
      'ImpactTask 集合未变、变更集未变成 applied。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-18-验证报告-20261021.json`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-7-验证报告-20261020.json',
  },
  {
    id: 'G3-GAP-02',
    title: 'RouteNode：造不出「必需项缺失」与「版本落后」两种前置',
    rows: ['AC-G3-08'],
    limits: [],
    missing:
      '路线图的**版本化编辑会话模块已经实现**；**验收台链路也已补上**（#295）：' +
      '台面覆盖「提案 / 提交 / 取消」与版本比较。' +
      '现在缺的是**本轮基线具备前置**——演示模板不声明任何 RouteNode，基线路线图为空。',
    specAnchor: anchor(378, 380),
    unblockCondition:
      '本轮已达成：模板侧声明了必需节点（#314），台面造前置的 409 也修掉了（#318），' +
      '「缺失」那一种前置由服务端侧给 blocking（#319）。' +
      '`AC-G3-08` 彻底收口要等缺口清单 #246 关闭。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 250,
    rowStatus: 'PASS (follow-up)',
    fact:
      '路线图版本化编辑会话**已实现**（提案不进冻结路线图、提交冻结新版本并产生复核任务、' +
      '取消恢复基线并释放临时引用、版本序列与比较、' +
      '三种必需节点前置给出 blocking ValidationIssue）。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-1-验证报告-20261020.json`（#250）。' +
      '原先「只有只读端点」的旧事实已作废；' +
      '原先「本轮实跑 NOT RUN：演示模板不声明任何 RouteNode」与「台面自造前置后撞 409' +
      '『基线已漂移』」两条旧事实已作废：' +
      '模板侧新增不可变 v2 声明 `route-r7` 为必需节点（#314），' +
      '台面建会话改用**建会话那一刻**的上下文修订，不再用链首的旧快照（#318）。' +
      '**本轮实跑判定成立**：台面造出「待复核」与「版本落后」两种前置，' +
      '稳定化给出 required_route_not_current 阻断，项目没有进入 STABILIZED / READY；' +
      '「缺失」那一种前置由服务端侧保证（#319）。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-20-验证报告-20261021.json`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-1-验证报告-20261020.json',
  },
  {
    id: 'G3-GAP-03',
    title: 'RouteNode：取消未冻结编辑后恢复上一版本',
    rows: ['AC-G3-16'],
    limits: [],
    missing:
      'RouteNode 写入与「取消未冻结编辑」**已经实现**；**验收台链路也已补上**（#295）：' +
      '台面覆盖版本比较与恢复。' +
      '现在缺的是**本轮基线里有一个可编辑的冻结节点**——基线路线图为空。',
    specAnchor: anchor(410, 412),
    unblockCondition:
      '本轮已达成（同 G3-GAP-02：模板侧声明必需 RouteNode + 台面 409 修掉 + 当前 head 重跑落证）。' +
      '`AC-G3-16` 彻底收口要等缺口清单 #246 关闭。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 250,
    rowStatus: 'PASS (follow-up)',
    fact:
      'RouteNode 写入与「取消未冻结编辑」**已实现**（同一项目只允许一个未冻结会话；' +
      '取消恢复基线并释放临时版本引用）。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-1-验证报告-20261020.json`（#250）。' +
      '原先「没有写入端点」的旧事实已作废；' +
      '原先「本轮实跑 BLOCKED：基线里没有可编辑的冻结节点」这条旧事实已作废' +
      '（与 G3-GAP-02 同因，同一批修复解掉）。' +
      '**本轮实跑判定成立**：取消未冻结编辑后冻结基线版本回到取消前的版本，' +
      '被取消会话占用的临时版本不出现在冻结版本序列里。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-20-验证报告-20261021.json`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-1-验证报告-20261020.json',
  },
  {
    id: 'G3-GAP-04',
    title: '助手：「确定受影响 / 可能受影响」两组说明',
    rows: ['AC-G3-14'],
    limits: [],
    missing:
      '助手草稿里的「确定受影响 / 可能受影响」两组说明**已经实现**；' +
      '**验收台读回链路也已补上**（#295）：台面把草稿里的两组说明读回来核对' +
      '（缺说明时记「助手未给两组说明」，不崩掉也不假装有）。' +
      '现在缺的是**一条带两组说明的助手草稿**——草稿由真实模型轮次产生。',
    specAnchor: anchor(402, 404),
    unblockCondition:
      '本轮已达成：模型凭据从环境变量读，台面自己跑真实模型轮次并读回两组说明（#320）。' +
      '`AC-G3-14` 彻底收口要等缺口清单 #246 关闭。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 251,
    rowStatus: 'WARNING',
    fact:
      '助手两组说明**已实现**（助手侧用 `definite_affected` / `possible_affected`，' +
      '服务端从已落库的助手 turn 重新推导，每条说明必须同时有对象引用、原因与依据）。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-8-验证报告-20261020.json`（#251）。' +
      '按 `GLOSSARY.md`：助手的影响说明只是**助手的解读**，始终随草稿存在，' +
      '须经**人工确认**后才进入正式评估。' +
      '工作台 `ImpactOpsPanel.vue` 的分组是**影响解析结果**的分组（`impacts[].certainty`），' +
      '**不是** AC-G3-14 要求的**助手草稿解释分组**，两者不可互替。' +
      '原先「本轮实跑 NOT RUN：没有可读回的助手草稿」与「行状态按 #315 的完成线保持 BLOCKED」' +
      '两条旧事实已作废。' +
      '模型边界已放宽（#315：缺两组说明时不再整条拒，而是如实标注「缺说明」），' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-17-验证报告-20261021.json`。' +
      '**本轮实跑判定成立**（#320）：台面走「自然语言 → 真实模型轮次 → 变更提案 → 人工确认落草稿 → ' +
      '读回两组说明 → 进入正式评估」，读回「确定受影响」1 条，每条带可追溯依据。' +
      '结论依赖真实模型（非确定性），行状态记 `WARNING`，不写 `PASS`。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-20-验证报告-20261021.json`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-8-验证报告-20261020.json',
  },
  {
    id: 'G3-GAP-05',
    title: '依赖候选：批量提出与三态读回',
    rows: ['AC-G3-15'],
    limits: [],
    missing:
      '依赖候选的**批量提出已经实现**；**验收台读回链路也已补上**（#295）：' +
      '台面把批量候选与既有决策三态的读回核对。' +
      '现在缺的是**在当前 head 上跑出绿**：本轮实跑撞出候选对象引用与项目对齐的问题（已修），' +
      '尚未重跑。',
    specAnchor: anchor(406, 408),
    unblockCondition:
      '本轮已达成：在当前 head 重跑实跑，拿到三态决策（确认 / 修改 / 拒绝）、同版本不再重提、' +
      '确认时版本复检的完整证据。`AC-G3-15` 彻底收口要等缺口清单 #246 关闭。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 252,
    rowStatus: 'PASS (follow-up)',
    fact:
      '依赖候选批量提出**已实现**（一次提出若干候选依赖，候选与已确认边**分表存放**，' +
      '候选带对象版本、依据片段与建议理由，可与既有 `decision` 三态对接）。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-3-验证报告-20261020.json`（#252）。' +
      '原先「本轮实跑 BLOCKED：候选的 source / target 约束已修但未在当前 head 重跑出绿」' +
      '这条旧事实已作废。' +
      '**本轮实跑判定成立**（#321）：三态齐备（确认 / 修改 / 拒绝），拒绝不产生边、' +
      '同版本重投被抑制（`rejected_in_same_version`）、' +
      '确认时版本复检被服务端按 409 `dependency_candidate_requires_reevaluation` 拒掉。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-20-验证报告-20261021.json`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-缺口收口-3-验证报告-20261020.json',
  },
  {
    id: 'G3-GAP-06',
    title: '影响中心：必需对象集合已落证并入 G3.7 判定，矩阵行 G3.7 记 PASS (follow-up)',
    rows: ['G3.7'],
    limits: ['AC-G3-04'],
    missing:
      '「本次必须处理的对象」集合（`assessment.objects`）的渲染能力**已实现**，' +
      '**也已并入矩阵行 `G3.7` 的完成判据判定**：本轮 head 的依赖链实跑把该集合读回来，' +
      '并按对象 `state` / `confirmed` / `context_revision` 逐字段核对。' +
      '**仍缺**：① 缺口清单 #246 未关闭前该行不得判 `PASS`（按规则记 `PASS (follow-up)`）；' +
      '② **页面渲染那半段**没有在当前 head 重跑——headless 实跑驱动不到验收台页面，' +
      '页面上自己读 `assessment.objects` 渲染的那一段沿用 #257 在 head `a87459f` 的落证。',
    specAnchor: anchor(346),
    unblockCondition:
      '数据面判定本轮已达成并入矩阵行 `G3.7`：必需对象集合（`assessment.objects`）逐字段核对齐全。' +
      '**仍缺**的是页面渲染那半段在当前 head 的重跑（headless 实跑驱动不到验收台页面，' +
      '页面上自己读 `assessment.objects` 渲染的那一段沿用 #257 在 head `a87459f` 的落证），' +
      '且缺口清单 #246 未关闭前该行只能记 `PASS (follow-up)`，不得判 `PASS`。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 257,
    rowStatus: 'PASS (follow-up)',
    fact:
      '渲染能力**已实现**（验收台 `AcceptanceConsole.vue` 与工作台面板 `ImpactOpsPanel.vue` 都渲染' +
      '「本次必须处理的对象」区块），并在 head `a87459f` 落证（#257）。' +
      '**本轮实跑判定成立**（依赖图与影响任务链 · 判定卡 `G3.7` · ' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-21-验证报告-20261021.json`）：' +
      '本轮 head 的实跑把 `assessment.objects` 读回来（3 个对象）并逐字段核对 ' +
      '`state` / `confirmed` / `context_revision`，判定卡 `G3.7` 成立。' +
      '矩阵行 `G3.7` 记 `PASS (follow-up)`（不是 `NOT RUN`）：判定已成立，' +
      '但缺口清单 #246 未关闭前带缺口的行不判 `PASS`，且页面渲染那半段没有在当前 head 重跑。' +
      '覆盖受限行 `AC-G3-04` 记 `PASS (follow-up)`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-验收台-8-证据-g3r-mvgqpt88-7b4e074d.json',
  },
  {
    id: 'G3-GAP-07',
    title: '影响中心：来源对象与确定/可能影响分组已落证并入 G3.7 判定，矩阵行 G3.7 记 PASS (follow-up)',
    rows: ['G3.7'],
    limits: ['AC-G3-04'],
    missing:
      '「来源对象」（`impacts[].causes[].source`）与「确定影响 / 可能影响」分组的渲染能力' +
      '**已实现**，**也已并入矩阵行 `G3.7` 的完成判据判定**：本轮 head 的依赖链实跑把来源对象' +
      '（含对象 kind / id / version）与影响理由读回来，并按 `certainty` 分两组核对。' +
      '**仍缺**：① 缺口清单 #246 未关闭前该行不得判 `PASS`（按规则记 `PASS (follow-up)`）；' +
      '② **页面渲染那半段**没有在当前 head 重跑——headless 实跑驱动不到验收台页面，' +
      '页面上按 `certainty` 分两组渲染 `impacts[]` 的那一段沿用 #257 在 head `a87459f` 的落证。',
    specAnchor: anchor(346),
    unblockCondition:
      '数据面判定本轮已达成并入矩阵行 `G3.7`：来源对象与影响理由齐全，' +
      '确定 / 可能两组不重不漏地覆盖全部影响。' +
      '**仍缺**的是页面渲染那半段在当前 head 的重跑（headless 实跑驱动不到验收台页面，' +
      '页面上按 `certainty` 分两组渲染 `impacts[]` 的那一段沿用 #257 在 head `a87459f` 的落证），' +
      '且缺口清单 #246 未关闭前该行只能记 `PASS (follow-up)`，不得判 `PASS`。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 257,
    rowStatus: 'PASS (follow-up)',
    fact:
      '渲染能力**已实现**（验收台 `AcceptanceConsole.vue` 按 `certainty` 分「确定影响 / 可能影响」两段并逐条渲染 ' +
      '`causes[].source`；工作台面板 `ImpactOpsPanel.vue` 同样在位），' +
      '并在 head `a87459f` 落证（#257）。' +
      '**本轮实跑判定成立**（依赖图与影响任务链 · 判定卡 `G3.7` · ' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-21-验证报告-20261021.json`）：' +
      '本轮 head 的实跑把每条影响的来源对象与影响理由读回来' +
      '（6 条影响，缺来源或原因 0 条），确定影响 5 条 + 可能影响 1 条 = 全部影响' +
      '（分组取服务端 `certainty`，页面不自行重算）。' +
      '矩阵行 `G3.7` 记 `PASS (follow-up)`（不是 `NOT RUN`）：判定已成立，' +
      '但缺口清单 #246 未关闭前带缺口的行不判 `PASS`，且页面渲染那半段没有在当前 head 重跑。' +
      '覆盖受限行 `AC-G3-04` 记 `PASS (follow-up)`。',
    implemented: true,
    evidence: 'docs/08-本轮实施方案/G3-验收台-8-证据-g3r-mvgqpt88-7b4e074d.json',
  },
  {
    id: 'G3-GAP-08',
    title: '调研类闭环：外部检索渠道可用但产品走不到，证据覆盖受限',
    rows: [],
    limits: ['G3.6'],
    missing:
      '**产品走不到外部检索渠道**。宿主环境**不缺**渠道：本机自托管的 SearXNG 实例' +
      '（经代理出网）对中文通用查询返回 **38~40 条真实命中**，**不需要任何 API key**；' +
      '但调研轮次从**不调用**它。缺的两处都在产品侧：' +
      '① `internal/lingdoc/workspace/research_adapter.go:164` 的 `SearchResearch` **先**跑' +
      '项目资料检索，那条路径返回错误时函数直接 return，外部 provider 那条路**走不到**；' +
      '② `internal/lingdoc/workspacecore/g2_service.go:564-571` 把 provider 的真实错误映射成 ' +
      '`provider_failure`，**既不返回也不记录**，于是「渠道没配 / URL 被 SSRF 拒了 / ' +
      'provider 自己失败」在实测里**完全不可区分**。',
    specAnchor: anchor(345),
    unblockCondition:
      '修掉上面两处（见 **#335**），让调研轮次真的调用外部渠道并带回真实命中，再重跑调研链；' +
      '在此之前本项记 `WARNING`（覆盖受限）——**流程闭环成立不代表真实课题资料质量通过**，' +
      '也不得用零命中的回执说成真实资料通过。另派生缺陷 #306（provider_id 无效返回 500）已修。',
    issue: G3_GAP_LIST_ISSUE,
    ticket: 258,
    rowStatus: 'WARNING',
    fact:
      '**外部检索渠道可用**（本机自托管 SearXNG，经代理出网）：中文通用查询返回 **38~40 条真实命中**，' +
      '**不需要任何 API key**。' +
      '**但产品走不到**：`SearchResearch` 先跑项目资料检索、那条路径返回错误时直接 return，' +
      '外部 provider 那条路走不到；provider 的真实错误又被映射成 `provider_failure` 且既不返回也不记录，' +
      '三种失败原因在实测里不可区分。轮次的 provider 身份已经是 ' +
      '`weknora_hybrid;web_search:<id>:searxng`（产品认得这个渠道），但服务端日志里没有 provider 的任何一行。' +
      '已开票 **#335**。' +
      '因此本项仍记 `WARNING`：这不是宿主环境限制，是**产品缺陷**——' +
      '「流程闭环成立」不代表「真实课题资料质量通过」。' +
      '落证：`docs/08-本轮实施方案/G3-缺口收口-21-验证报告-20261021.json` 的 ' +
      '`external_search_channel` 节。',
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

/**
 * **缺口对应的验收行一律不得判 `PASS`**（`docs/agents/development-rules.md`）。
 *
 * 这条规则最初写成「#246 未关闭前…」；#246 已于 2026-10-21 关闭，但
 * **11 条缺口尚未逐条复核通过**（行状态仍是 `PASS (follow-up)` 与 `WARNING`），
 * 所以降级依然成立——变的是理由，不是行为。
 *
 * 这条规则必须在**投影处**兜住，不能只靠静态声明：判定卡自己会带 `status`，
 * 判定成立时常常就是 `PASS`（AC-G3-03 的原子性判定、路线图判定、助手判定都是）。
 * 照搬判定卡状态的话，一个**仍带实现缺口**的行会被投影成 `PASS`，直接违反那条规则。
 *
 * 降级口径：`PASS` → `PASS (follow-up)`（判据成立，但仍有未关闭的缺口或覆盖受限，
 * 因此保留跟进链接，不写「完全通过」）。其余状态原样保留。
 *
 * 覆盖两类缺口：
 * - **直接缺口**（`gap_ids`）：因缺能力而无法实跑的行；
 * - **覆盖受限缺口**（`limiting_gap_ids`）：判据已判定，但某类证据 / 分支没覆盖到。
 *
 * 三处投影（`evidenceExport.ts` 的两条导出路径、`rowEvidenceRules.ts` 的页面矩阵投影）
 * 共用这一个函数，避免「只修了一处、另一处还在投影 PASS」。
 */
export function downgradeGapPass(rowId: string, status: G3Status): G3Status {
  if (status !== 'PASS') return status
  const hasGap = gapIdsForRow(rowId).length > 0 || limitingGapIdsForRow(rowId).length > 0
  return hasGap ? 'PASS (follow-up)' : status
}
