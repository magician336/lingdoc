# Spec-Beta-F01-11：intent 口径变更（只声称断言覆盖的内容）

> **票**：[#689](https://github.com/magician336/lingdoc/issues/689)（F01-11 的 intent 强于其机器断言）｜**上游**：[#679](https://github.com/magician336/lingdoc/issues/679)（采纳持久化实测，其未闭合的一半由本票承接）、[#482](https://github.com/magician336/lingdoc/issues/482)（Beta 首版放行验收）｜**权威记录**：`docs/BETA_STATUS.md` §24.3
> **变更基线**：`origin/main` = `7867c99c`（2026-10-26 本地 fetch 失败——连接被重置；基线取本地 `origin/main`）
> **执行**：#689 缺陷处理子代理；**路径归属**：`contracts/workflow.json` owner **PF-10**（done 状态仍占用路径），本次按 #608 / #546 先例**跨面借用、owner 不变**；L1 登记处与 L2 路径图（owner PF-14，claimed）**一字未动**
> **状态**：已执行票面**解除条件②**（改写 intent）；条件①（重跑 F01 补断言）本轮不可行——需要新的 provider 授权，而本轮授权窗口已用尽

---

## 1. 裁定

执行 #689 的**解除条件②**：把 F01-11 的 intent 改写为**只声称其两条机器断言真正覆盖的内容**（review_items 传递 ＋ 当前版本指向新版本），不再声称任何未被断言覆盖的事实。

**明确不做的三件事**：

| 不做 | 为什么 |
|---|---|
| **不加断言、不改断言结构** | 加 `project_version` 断言 ⇒ 要产出新证据 ⇒ 要重跑 F01 ⇒ 需要新 provider 授权（条件①，本轮授权窗口已用尽，协议 §5） |
| **不改 F01-18** | F01-18 的 `capture project_version` 不是缺陷：只捕获不断言是它的既定形态；本次连它的 intent 也不动 |
| **不追溯改写任何已归档证据** | 见 §5 |

## 2. 精确变更（仅 `docs/08-本轮实施方案/contracts/workflow.json`）

**契约 `version`：`0.5.0` → `0.5.1`**（本契约首次显式 bump；此前 #546 / #608 / #621 的就地改动未 bump。本次 bump 的理由就是本票的存在意义：让「已归档证据 ↔ 契约版本」的边界**机器可辨**——归档件记录的是 0.5.0，改动后的活契约自称 0.5.1。已核实全仓工具链**没有任何代码读** workflow.json 的 `version` 字段（`run_f01.py`、`validate_artifacts.py`、`beta_evidence.py`、rejudge 等均不读），bump 不影响任何校验。）

F01-11 check ①（`{{review_id}}` `ref_in /data/review_items`）：

> **改前**：`新版本继承review_items；项目版本增加到5。（候选的待核事项出现在新版本里；项目版本的递增由 F01-18 的读回核对）`
> **改后**：`候选的待核事项出现在新版本里：新版本继承 review_items（本断言只核对引用出现在 /data/review_items，不核对新版本与候选的 review_items 逐字一致）。project_version 的递增不在本契约任何断言内——目前只有行为级证据：#679 实测一次采纳 4→5，PR #690 回归测试钉住恰好 +1（0.5.1 起 intent 只声称断言覆盖的内容；变更记录见 docs/08-本轮实施方案/Spec-Beta-F01-11-intent口径变更-689.md）。`

F01-11 check ②（`/data/current_version_id` `non_empty`）：

> **改前**：`新版本继承review_items；项目版本增加到5。（采纳产生了新的正文版本）`
> **改后**：`采纳产生了新的正文版本：当前版本指向新版本（current_version_id 非空）。`

删除的两类声称：**「项目版本增加到 5」**（全契约无任何断言核对 `project_version` 递增）与**「项目版本的递增由 F01-18 的读回核对」**（F01-18 只 `capture` `project_version`，它断言的是 `spec_revision` 与 `status`）。同时显式写明「review_items 逐字一致」不在断言内（`ref_in` 只核对引用存在）。

## 3. 断言覆盖的事实基础

- F01-11 的机器断言**只有两条**：`ref_in /data/review_items`、`/data/current_version_id non_empty`（本次未动）。
- 全契约带 `project_version` 路径的 check 仅两条，**都不是**「采纳 ⇒ 递增」断言：F01-03b（回执 `equals` 当前捕获版本——审阅的是当前版本）与 F01-19（`version_bound` 冻结检查与冻结输入一致）。F01-03 / F01-04 / F01-18 只 `capture`。

## 4. 行为级证据（补充证据，不是契约断言）

`project_version` 递增与「继承逐字一致」当前**只有行为级证据**：#679 阶段 1 隔离环境实测（一次采纳 4→5 恰好 +1；独立读回与数据库物理行逐字一致；同幂等键重放不再递增；缺 `confirmation` ⇒ 400 零写入）；PR **#690**（合并提交 `67939fc7`）把「一次采纳 ⇒ 恰好 +1」钉进回归测试 `TestAdoptionAdvancesTheProjectVersionByExactlyOne`。**它们不在本契约断言内**；若要把递增变成机器断言，走条件①（新授权后重跑 F01）。

## 5. 归档边界（不适用于已归档的 F01 证据）

- 已归档的 F01 证据（`docs/08-本轮实施方案/acceptance/beta-first/<sha>/<run>/`）绑定**契约版本 0.5.0**：各运行的 `runtime-manifest.json` 记录的 `version` 就是 `0.5.0`。**本次改动不追溯适用于它们**：归档件一个字节未动（含 `beta-acceptance-support-20261026`、`beta-adoption-persistence-20261026` 的报告快照——它们内嵌的 intent 文本是**当时契约的历史快照**，改写它们等于伪造证据）。
- 「归档证据 ↔ 契约版本」对应关系**保持不变**：归档 ⇔ 0.5.0；本次改动产生 0.5.1，**只约束将来的运行**。
- `scenarios.json` 未动（其内容不含 F01-11 的 intent；`contract_version` 仍为 0.5.0，指其自身未变更的场景断言集；该路径 owner 为 PF-14——claimed 非 done——本次不碰）。`openapi.json`、`g7-writing-contract.json`、`validate_artifacts.py`、`frozen-input.*` 均未动。

## 6. 红灯回路（可重复检查）

检查脚本 `_red_light_689.py`（工作树内未跟踪临时件，不随提交入库；按本节规则可随时重建）核对三件事：F01-11 断言结构仍是原两条（未加断言）；两条 intent 非空且不含「项目版本增加」「版本增加到」「F01-18 的读回核对」；采纳窗口（F01-11/F01-18）的 checks 不含 `project_version`，且全契约 `project_version` check 集合仍恰为 {F01-03b, F01-19}（若将来变化 ⇒ 条件①已执行，按设计转红提示恢复递增声称并重跑 F01）。

- **改前（HEAD=7867c99c 的契约）**：`AssertionError: F01-11 intent 仍含未被断言覆盖的声称（项目版本增加）`，exit=**1**（红）
- **改后**：`GREEN: F01-11 intent 只声称其两条断言覆盖的内容`，exit=**0**（绿）
- 既有校验全部不因本改动变红：`python docs/08-本轮实施方案/contracts/validate_artifacts.py` exit 0（result=PASS；`validation-result.json` 重生成后逐字一致，无需提交）；`python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'` **821 tests OK**；`python -m unittest discover -s scripts/lingdoc_mock -p 'test_*.py'` **375 tests OK**；`python -m unittest discover -s scripts/acceptance -p 'test_*.py'` **43 tests OK**。

## 7. 边界与残留

- 本变更**不阻塞** Beta 首版放行（协议 §6 六项门槛不含「intent 与断言逐字对齐」；见 #689 票面边界）。
- 残留缺口不变：契约断言层仍无 `project_version` 递增断言、无 review_items 逐字一致断言、无跨重启 `project_version` 断言（§24.3 ④）。解除仍走条件①。
- `docs/BETA_STATUS.md` 未动（票面约束）；issue 不由本子代理评论/关闭。
