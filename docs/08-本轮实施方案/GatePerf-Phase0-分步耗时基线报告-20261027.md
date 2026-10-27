# GatePerf-Phase0：gate 分步耗时基线报告（票 #721）

- 关联：Spec #720（PR gate 耗时优化）／治理票 #719／本票 #721；ADR 0005（按 diff 内容判定）、#550（并发策略）。
- 基线：`main` @ `e8dc6930`（PR #718 合并头；样本 run 均在 2026-10-26 ～ 2026-10-27 产生，与其同代）。
- 采集日期：2026-10-27。采集人与方式：实施代理，只读 REST 采集（未触发任何工作流）。

## 0. 目的（开工卡片口径）

拿尺子再动刀：采集近 10–20 次已完成「LingDoc PR gate」run 的**分步耗时**，产出热点排序；同口径对照 app 与 integration 两个 job 的总时长；给出两个裁决数据点——守护套件实测时长（xdist 条目 go/no-go）与 provider 步实测占比（分片收益）；对照 Spec #720 的假设，一致则确认，不符则行使重排权。

## 1. 样本与采集命令

### 1.1 样本清单（17 次 gate run）

数据源：`GET /repos/magician336/lingdoc/actions/runs/{id}/jobs`（`jobs[].steps[]` 自带 `started_at`/`completed_at`，UTC）。job 名 `LingDoc PR gate`（工作流 `.github/workflows/lingdoc-review.yml`）。全部 17 个样本采集成功，无 NOT RUN 样本。

| run ID | 分支 | 触发 | 结论 | 执行面轮廓 | job wall |
|---|---|---|---|---|---|
| 40427398076 | feat/696-acceptance-c1 | pull_request | success | 全量（契约+provider+前端） | 1111s (18.5m) |
| 40429646490 | feat/696-acceptance-c1 | pull_request | success | 全量 | 859s (14.3m) |
| 40426997606 | feat/696-acceptance-c1 | pull_request | **failure** | 失败于「Select checks」 | 123s |
| 40429691938 | chore/461-466-512-pf9-pf11-handover | pull_request | success | 全量 | 953s (15.9m) |
| 40428973105 | chore/461-466-512-pf9-pf11-handover | pull_request | success | 全量 | 1247s (20.8m) |
| 40426244946 | fix/713-shape-guard-rule4 | pull_request | success | 裁剪（无前端面） | 176s |
| 40424905943 | fix/713-shape-guard-rule4 | pull_request | success | 裁剪（无前端面） | 278s |
| 40423306848 | docs/560-glossary-actor-role | pull_request | success | 全量（名义 docs 分支） | 888s (14.8m) |
| 40421100592 | docs/560-glossary-actor-role | pull_request | success | 全量 | 922s (15.4m) |
| 40418503947 | docs/560-glossary-actor-role | pull_request | success | 全量 | 1266s (21.1m) |
| 40418918807 | feat/458-acceptance-judge | pull_request | success | 全量 | 893s (14.9m) |
| 40366193209 | feat/458-acceptance-judge | pull_request | success | 全量 | 932s (15.5m) |
| 40360449901 | feat/458-acceptance-judge | pull_request | success | 全量 | 1012s (16.9m) |
| 40383933096 | spec-707/schema-shape-guard | pull_request | success | 裁剪（无前端面） | 1045s (17.4m) |
| 40380987017 | spec-707/schema-shape-guard | pull_request | success | 裁剪（无前端面） | 1020s (17.0m) |
| 40373280909 | spec-707/schema-shape-guard | pull_request | success | 裁剪（无前端面） | 1073s (17.9m) |
| 40425846517 | feat/696-acceptance-c1 | workflow_dispatch | **failure** | 失败于守护套件步 | 111s |

满足票面要求：≥1 次全量跑（10 次「契约+provider+前端」全轮廓）+ 多次按面裁剪跑（fix/713、spec-707 共 5 次无前端面）+ 2 次失败路径样本。全量跑 wall 分布 **859–1266s（14.3–21.1 分钟）**，与探索锚点（40427398076 = 18.5 分钟）一致。

### 1.2 采集命令与复现

```bash
# 列样本
gh run list --repo magician336/lingdoc --workflow lingdoc-review.yml --status completed --limit 40
# 取单 run 的 job+steps（字段 started_at/completed_at/conclusion）
gh api repos/magician336/lingdoc/actions/runs/{run_id}/jobs?per_page=100
```

- 网络纪律：采集脚本对每次请求做退避重试（失败后 sleep 30–60s，至多 6 次）。本次采集**未触发**任何工作流、未写任何远端状态；两个失败 run 是仓库里既有样本，如实入表。
- 时间口径：步秒数 = 步 `completed_at − started_at`；job wall = job `completed_at − started_at`（含步间开销）；占比分母为 job wall。步号 ≥25 的 Post 步不计（清理性质，合计 <15s）。

## 2. 逐 run 逐步耗时矩阵（秒）

列 = run ID；`—` = 该步被按面裁剪跳过；`**Ns F**` = 该步失败；粗体行 = job wall。

| 步 | 40427398076 | 40429646490 | 40426997606 | 40429691938 | 40428973105 | 40426244946 | 40424905943 | 40423306848 | 40421100592 | 40418503947 | 40418918807 | 40366193209 | 40360449901 | 40383933096 | 40380987017 | 40373280909 | 40425846517 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Set up job | 2 | 1 | 1 | 1 | 1 | 2 | 1 | 2 | 2 | 1 | 3 | 1 | 2 | 2 | 1 | 1 | 2 |
| checkout@v6 (fetch-depth:0) | 2 | 3 | 8 | 3 | 3 | 3 | 4 | 2 | 3 | 3 | 2 | 3 | 4 | 4 | 3 | 3 | 3 |
| setup-python@v6 | 1 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 1 | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 1 |
| setup-go@v6 | 13 | 22 | 9 | 23 | 9 | 17 | 8 | 24 | 28 | 8 | 8 | 25 | 9 | 9 | 8 | 9 | 8 |
| Install CI scope script deps | 6 | 4 | 2 | 3 | 4 | 7 | 2 | 3 | 6 | 3 | 2 | 4 | 2 | 9 | 2 | 2 | 2 |
| Test CI scope scripts（守护套件） | 71 | 86 | 94 | 96 | 89 | 99 | 72 | 91 | 94 | 93 | 55 | 95 | 73 | 91 | 90 | 94 | **89s F** |
| Select checks + whitespace | 1 | 0 | **1s F** | 0 | 1 | 1 | 0 | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 1 | 1 | — |
| Install contract validator dep | 0 | 0 | — | 1 | 0 | 0 | 1 | 0 | 0 | 1 | 1 | 0 | 1 | 1 | 0 | 0 | — |
| Validate contracts | 24 | 31 | — | 33 | 31 | 33 | 24 | 31 | 33 | 33 | 18 | 33 | 24 | 31 | 31 | 33 | — |
| Test and vet provider packages（-race） | 842 | 556 | — | 625 | 911 | 7 | 155 | 567 | 579 | 934 | 662 | 595 | 726 | 892 | 880 | 925 | — |
| setup-node@v6 | 2 | 2 | — | 2 | 1 | — | — | 2 | 4 | 1 | 1 | 1 | 1 | — | — | — | — |
| Frontend test/type-check/build | 130 | 149 | — | 162 | 175 | — | — | 158 | 164 | 166 | 121 | 170 | 148 | — | — | — | — |
| Publish check boundary | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| upload-artifact@v4 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 2 | 2 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 |
| **job wall** | **1111s** | **859s** | **123s** | **953s** | **1247s** | **176s** | **278s** | **888s** | **922s** | **1266s** | **893s** | **932s** | **1012s** | **1045s** | **1020s** | **1073s** | **111s** |

## 3. 热点排序（步骤 ran 时的均值占 wall 比）

| 排名 | 步 | 样本数 n | 均值秒 | 最大秒 | 均值占 wall | 备注 |
|---|---|---|---|---|---|---|
| 1 | Test and vet provider packages（-race） | 15 | **657.1s** | 934s | **67.1%** | 裁剪跑（无前端面）中达 85–86% |
| 2 | Frontend test/type-check/build | 10 | **154.3s** | 175s | **15.5%** | 与 frontend.yml 逐命令重复（§6） |
| 3 | Test CI scope scripts（守护套件） | 17 | 86.6s | 99s | 8.5%（全量跑均值） | 无条件步；55–99s 全区间 < 2 分钟 |
| 4 | Validate contracts | 15 | 29.5s | 33s | 4.4% | |
| 5 | setup 链（setup-go/checkout/其余安装） | 17 | ≈30s 合计 | — | ≈3–5% | checkout 仅 2–8s（fetch-depth:0） |

热点排序结论：**provider 步（`go test -race` 四包根串行）是 gate wall 的绝对大头**——全量跑中占 63–76%，无前端面的裁剪跑中占 85–86%；前端步第二（~15%）；守护套件第三但绝对值小（<100s）。同分支重复推送的方差也主要来自 provider 步（如 chore/461-466-512 两次：625s vs 911s），与 runner 波动叠加。

## 4. 裁决数据点 1：xdist go/no-go

- Spec #720 门槛：守护套件实测 **≥2 分钟（120s）** 才引入 pytest-xdist。
- 实测（本报告 17 个样本，无条件步每次都执行）：**55–99s，均值 86.6s，最大 99s**。全部样本 < 120s（最大值为门槛的 83%）。
- 守护测试规模（head `e8dc6930` 实数）：`scripts/ci/test_lingdoc_*.py` 共 **37 文件 / 898 个测试函数**（grep 口径；探索笔记记 36 文件/821，套件在增长）。
- **裁定：NO-GO**。按 Spec 自设门槛，本条为条件项、不达标不做；无需登记失败，接受现状。若套件继续增长（898 → ~1200 或单进程破 120s），用本报告口径复测再评估。

## 5. 裁决数据点 2：provider 步占比（分片收益）

- provider 步在 ran 的 15 个样本中均值 657.1s、最大 934s，占 wall 63–86%——**分片收益空间即这一段的「各包根之和 → 最大分片」差值**。
- 步时长随 diff 触发的包范围伸缩：fix/713（scripts 类改动）仅 7s / 155s；spec-707（同类）却 880–925s；全轮廓 Go PR 556–934s。说明「把四个包根塞一个 job 顺序跑」的 wall=求和结构在实际 diff 下已充分暴露。
- 限制：Actions API 只有步级粒度，**包级耗时分布未测**（需实施票内用 `go test -json` 输出补测）；本数据点只裁决「做不做分片」（做），不给出分片切分参数。

## 6. 同口径对照：app 与 integration 总时长

| 工作流 | job | run ID | 分支 | 结论 | 总时长 |
|---|---|---|---|---|---|
| app.yml | Format, vet, test, and build | 40428985262 | main | success | **234s (3.9m)** |
| app.yml | Format, vet, test, and build | 40426244890 | fix/713-shape-guard-rule4 | success | **238s (4.0m)** |
| app.yml | Format, vet, test, and build | 40424905947 | fix/713-shape-guard-rule4 | success | **228s (3.8m)** |
| lingdoc-interface-integration.yml | integration | 40426244829 | fix/713-shape-guard-rule4 | success | **1384s (23.1m)** |
| lingdoc-interface-integration.yml | integration | 40347765649 | beta-first-run | success | **1377s (23.0m)** |
| lingdoc-interface-integration.yml | integration | 40309055533 | fix/656-unassessed-apply-invalid-state | success | **743s (12.4m)** |

- app job 内部大头是「Vet and test」（168–186s，无 `-race`），全 job ≈ 3.9 分钟——比 gate 轻一个量级。
- integration job 743–1384s，**与 gate 全量 wall 同量级甚至更长**（内部大头「Run backend closeout against isolated PostgreSQL」653–1159s），证实 #720 把它列为「另一个独立的耗时大头、另行立票」的判断；本票不改它。
- 补充证据（前端重复）：frontend.yml 的「Test, type-check, and build」job 真实样本 40419571058（chore/492）：wall 148s，命令与 gate 前端步逐条相同（install 20s + tests 51s + type-check 24s + build 43s）；gate 前端步均值 154.3s。触面前端的 PR 两处全跑，**runner 分钟双倍**；因两 job 并行，gate wall 的可省上限即 gate 前端步本身（~2.5 分钟）。

## 7. 失败路径证据

- 40426997606：守护套件 94s 绿 → 「Select checks and check diff whitespace」1s 红 → wall 123s。判定步失败时 gate 快速失败（~2 分钟出红）。
- 40425846517（workflow_dispatch）：守护套件步 89s 红 → 后续条件步全部 skipped → wall 111s。失败 run 的条件步零执行，不产生误导性耗时。

## 8. 与 Spec #720 假设的对照结论

| #720 假设 | 实测 | 结论 |
|---|---|---|
| provider 测试是 wall 大头（wall=各包根之和） | provider 步占 wall 63–86%（均值 67.1%），且随触发包范围伸缩（7s–934s） | **一致 → 确认**，provider 分片维持最高优先 |
| 前端三命令两处重复 | gate 前端步均值 154.3s ≈ frontend.yml job（148s），命令逐条相同 | **一致 → 确认**；但注意 wall 可省上限 ~2.5 分钟（两 job 本并行），去重的直接收益是 runner 分钟与证据单一出处 |
| 821 项守护测试单进程慢（条件引入 xdist） | 实测 55–99s（均值 86.6s）< 120s 门槛 | **门槛判定：NO-GO**，按 Spec「不达标则本项不做」接受现状 |
| 全量 clone 只为 merge-base | checkout 步实测 2–8s | 浅化 clone 的 **wall 收益可忽略**（<10s）；该顺手项保留但不应被期待提速 |

**重排权行使：不需要。** 实测热点排序（provider ≫ 前端 > 守护套件 > 契约）与 #720 的假设序一致，无重排，后续票范围与优先级照旧解锁。一处**校准说明**（不改排序，改预期）：#720 预期「前端类 PR gate 由 ~18 分钟降至 ~8–10 分钟」——实测 gate 前端步均值仅 154.3s，仅靠前端去重的 wall 降幅上限 ~2.5 分钟（18.5 → ~16）；要进入 8–10 分钟区间必须叠加 provider 侧（分片/按面收窄）的削减。此校准不推翻任何票序，反而加强 provider 分片的收益预期。

## 9. 验收矩阵（票 #721）

| 验收项 | 证据 | 状态 |
|---|---|---|
| 报告落 `docs/08-本轮实施方案/`（目的/样本/命令/结论） | 本文件 §0/§1/§2–§8 | **PASS** |
| 每个耗时点可复核：run ID + job 名 + 步名 + 秒数；10–20 样本，含 ≥1 全量 + ≥1 裁剪 | 17 次 gate run，§1.1 样本表 + §2 矩阵（job=`LingDoc PR gate`）；10 全量 + 5 裁剪 + 2 失败；原始 jobs JSON 存工作树外 `D:\wt\gp-721-data\jobs-*.json`（37 份） | **PASS** |
| 热点排序结论明确 + 与 #720 对照 | §3 排序表 + §8 对照表；一致 → 确认，无重排 | **PASS** |
| xdist go/no-go 数据点 vs ≥2 分钟门槛 | §4：55–99s（均值 86.6s）< 120s → NO-GO | **PASS** |
| API 抖动退避重试；采不到如实 NOT RUN | §1.2 退避纪律（30–60s × ≤6）；本次 17/17 + 对照 8/8 全部采到，**无 NOT RUN 样本**；两失败 run 如实按失败步入表 | **PASS** |
| 只读：不触发工作流、不改文件（报告除外） | 仅新增本报告；`gh api` 只读端点；无 `gh run` 触发类调用 | **PASS** |

NOT RUN / BLOCKED：无。补充说明：包级耗时分布（§5）与 integration 内部拆分不在本票范围，已在 §5/§6 注明测量边界。

## 10. 遗留风险

1. **xdist NO-GO 是时点结论**：守护套件 821 → 898（且仍在增长），最大样本 99s 已达门槛 83%；建议后续实施票动工前用本报告口径（§1.2 命令）复测一次。
2. **采样窗口集中在 2026-10-26～27**：runner 负载、缓存温度同质；跨周/高峰波动未覆盖。热点排序在这种方差下依然稳健（provider 最低占比 63%），但绝对值（如「18.5 分钟基线」）应取区间而非单点。
3. **provider 步内部不透明**：步级粒度看不到包/测试分布，分片参数（几片、按什么均衡）需实施票内以 `go test -json` 计时补测。
4. **名义 docs 分支触发全量 provider 面**（docs/560 provider 步 567–934s）：按 diff 判定对「docs+scripts 混合分支」的面选择值得后续票核对 scope.json 工件（本票只读，未取 artifact）。
