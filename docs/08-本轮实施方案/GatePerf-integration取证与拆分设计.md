# GatePerf-integration：closeout 取证与拆分设计（票 #735，取证+设计阶段）

- 关联：治理票 #719／Spec #720／本票 #735（取证+设计；实施按选定方案另行推进）；前置 `GatePerf-Phase0-分步耗时基线报告-20261027.md`（§6 记 integration job 743–1384s）、`GatePerf-provider分片-验收报告.md`（§2.2/§4 workspace 单包下界）。
- 基线：`main` @ `cb57f5c9`（与 Phase 0 基线同代的当前 head）。工作流：`.github/workflows/lingdoc-interface-integration.yml`（152 行，3 job：scope / integration / frontend）。
- 采集日期：2026-10-27。方式：只读 REST 采集（`gh api`，全部带 20–30s 退避重试，未触发任何工作流）+ CI artifact 解析 + WSL 本地一次性 PG 分桶实测（`pg735` 容器，用后即停，未改仓库）。
- **本票零代码改动**；选定方案的决策记录草案见 §6.3（**ADR 编号 0017（本票不落 ADR 文件）**——编号勘误：#736 的工作区分片决策已定为 ADR 0016（本 PR 同分支落档），原「预留 0016」系基于过时的 adr 目录清单；#735 正式落档时用 0017）。

## 0. 目的

Phase 0 已把 integration job 钉为「gate 之外剩余最大耗时大头」（job 743–1384s，大头 backend closeout 653–1159s），但步级粒度看不到 closeout 内部。本报告回答三件事：

1. closeout 步（1100+s）的**内部构成**：时间花在哪个子命令、哪个包、哪些测试；
2. **PG 依赖矩阵**：哪些段离开 PostgreSQL 即 skip/FAIL，哪些段与 PG 无关，哪些段可并行；
3. 在取证之上给出**结构不同的拆分候选**（Ousterhout「设计它两次」）与选定方案、实施蓝图。

## 1. integration job 全步拆解（workflow 文本口径）

文件：`.github/workflows/lingdoc-interface-integration.yml`（head cb57f5c9）。触发：PR→main / workflow_dispatch；并发组 `lingdoc-interface-<PR号或ref>`，cancel-in-progress。

### 1.1 scope job（≈8s，与拆分无关）

checkout（fetch-depth:0）→ `lingdoc_gate_decision.py --no-whitespace` 判定（输出键 `job_lingdoc_interface_integration_integration` / `..._frontend`）→ 无条件发布结论 artifact（skipped 带原因；本报告三样本中两次 integration 整 job skipped，即该判定生效）。触发面见 `scripts/ci/lingdoc_gate_surfaces.py:346`（`internal/lingdoc/**`、`internal/evidence/**`、`scripts/acceptance/**`、`internal/container/lingdoc*`、`internal/router/lingdoc*`、`internal/database/**`、工作流自身；formatting-only 提交跳过）。

### 1.2 integration job（取证主体）

前置环境：`services.postgres`（postgres:17，`lingdoc_test`/`synthetic_ci_only`，健康检查 `pg_isready`）；env `CGO_ENABLED=1`、`LINGDOC_TEST_POSTGRES_DSN`（指向 job service）、`LINGDOC_ACCEPTANCE_HEAD_SHA`；`timeout-minutes: 40`。

| # | 步 | 命令要点 | PG 依赖 |
|---|---|---|---|
| 1 | Initialize containers | 起 postgres:17 service | — |
| 2 | checkout / setup-go / setup-python | fetch-depth:2；go-version-file | — |
| 3 | Test closeout matrix helpers | `python -m unittest discover -s scripts/acceptance -p 'test_*.py'` | 无 |
| 4 | Rehearse PostgreSQL migration upgrades and rollbacks | `go test -count=1 -v -run '^TestPostgres' ./internal/database/...` | **是**（无 DSN 时用例自跳） |
| 5 | Run backend closeout against isolated PostgreSQL | `python scripts/acceptance/lingdoc_backend_closeout.py --output-dir … --require-postgres` | **是**（DSN 注入） |
| 6 | Repeat sequential API contract without race instrumentation | `go test -count=20 -run '^TestLingDocRealAPIThroughMainRegistration$' ./internal/router` | 无（router 无 DSN 读取，SQLite 即可） |
| 7 | Verify RuleSet HTTP-to-export persistence | `go test -count=1 -v -run '^TestRuleSetProductionHTTPToExportPersistsAcrossRestart(SQLite|Postgres)$' ./internal/container` | **是**（Postgres 变体） |
| 8 | Upload backend closeout logs and status matrix（`if: always()`） | artifact `lingdoc-backend-closeout-<run_id>` | — |

### 1.3 closeout 脚本内部（`scripts/acceptance/lingdoc_backend_closeout.py`，428 行）

按执行序：

1. `capture_source_binding`（源绑定取证，秒级）；
2. `start_postgres()`：CI 注入 DSN → 直接 PASS（`injected_loopback_test_database`；本地无注入时自起一次性 docker 容器）；
3. **failure fixture create**：`go run ./scripts/acceptance/postgres_failure_fixture.go create`（生成 `lingdoc_fault_<hex>` 库 + `lingdoc_fault_user_<hex>` 角色，应用 39 条生产迁移；超时上限 300s）；
4. **主 sweep**：`go test -race -json -count=1 -timeout=40m -mod=readonly ./internal/database ./internal/lingdoc/... ./internal/router ./internal/container ./internal/evidence`（超时上限 2500s）——**包面即这 5 个包根**（`PACKAGES`，`internal/lingdoc/...` 展开 18 个包）；
5. `go vet -mod=readonly`（同 5 包根）；
6. `go build ./cmd/server` → **full-server-restart**（真实二进制起停两次 + SQLite 读回校验，超时上限 600s）；
7. g6 质量守护：`python -m unittest discover -s scripts/lingdoc_mock -p test_g6_quality.py`（上限 180s）；
8. **rollback-roundtrip**：`go run …postgres_failure_fixture.go rollback-roundtrip`（39 条迁移 down+re-up，上限 300s）→ **fixture drop**（上限 300s）；
9. 源绑定比对 + 写 `report.json`/`summary.md`；`--require-postgres` 语义：任一场景非 PASS → 退出码 2（红）。

## 2. 近 3 次真实 run 的分步耗时（run ID 可复核）

样本选取：该工作流近 12 次完成 run 中，scope 判定为「integration 实跑」的最近 3 次（其余 9 次 integration/frontend 被 scope 裁剪 skipped，无耗时意义）。原始 jobs JSON 存工作树外 `D:\wt\gp2-735-data\jobs-<run_id>.json`。秒数 = 步 `completed_at − started_at`（UTC，API 原生步级字段）。

| 步（秒） | 40482358130 | 40475262990 | 40474212702 |
|---|---|---|---|
| 分支 / head | fix/residual-batch2 / ab7e6284f7 | fix/residual-batch2 / 5df26f29c1 | feat/696-acceptance-c1 / 75e11eda69 |
| Set up job | 2 | 1 | 2 |
| Initialize containers（PG service） | 19 | 12 | 13 |
| checkout | 12 | 3 | 4 |
| setup-go | 10 | 8 | 9 |
| setup-python | 0 | 1 | 0 |
| Test closeout matrix helpers | 1 | 0 | 2 |
| **Rehearse PostgreSQL migrations** | **126** | **122** | **125** |
| **Run backend closeout** | **1162** | **1169** | **1169** |
| Repeat sequential API（count=20） | 22 | 24 | 23 |
| Verify RuleSet SQLite+Postgres | 30 | 32 | 32 |
| Upload artifact | 2 | 1 | 1 |
| **job wall** | **1427** | **1393** | **1405** |

占比（三样本均值）：**closeout 83.5%**（1162–1169s）、PG rehearsal 8.8%（122–126s）、RuleSet 2.2%、count=20 1.6%、setup 链+PG init 合计 ≈4%（PG service init 仅 12–19s）。**closeout 是唯一值得拆的段**—— rehearsal/count20/RuleSet 三步合计 ≈180s，即使全并进别处，收益上限 ~13%。

## 3. closeout 内部构成（CI artifact 实证 + 时间轴）

数据源：run 40475262990 的 artifact `lingdoc-backend-closeout-40475262990`（`go-test.jsonl` 为 `go test -race -json` 全量事件流，含每事件 UTC 时间戳；原始文件存 `D:\wt\gp2-735-data\artifact-40475262990\`）。该 run：automated PASS / closeout PASS / `postgres_setup.mode=injected_loopback_test_database`，8 项 check + 14 项 scenario 全 PASS——**失败路径未在本次取证样本中出现，红路径语义以脚本退出码口径（§1.3 第 9 条）为准**。

### 3.1 三段时间轴（步 1169s 的分解）

| 段 | 时长 | UTC 锚点（40475262990） | 内容 |
|---|---|---|---|
| pre-go-test | ≈145s | 08:44:05→08:46:30 | 源绑定 + fixture create（`go run` 冷编译 + 39 条迁移）+ `go test` 首批包 -race 编译 |
| go-test 窗口 | **800.7s** | 08:46:30→08:59:50（首事件→末事件） | 2937 个测试，5 包根，`-race -count=1`；包级 stagger 编译包含在窗口内 |
| post-go-test | ≈225s | 08:59:50→09:03:34 | vet + build + full-server-restart + g6 + rollback-roundtrip + fixture drop + 绑定比对 + report |

交叉校验：restart-readback.json `at=09:03:34Z`、report.json `generated_at=09:03:35Z`，与步末吻合（±1s）。

### 3.2 go-test 包级分布（窗口 800.7s，Σ包墙 1421.6s → 有效并行度 1.77，runner 4 核）

| 包 | 包墙(s) | 占窗口 | 测试数 | 逐测试耗时和(s) | 包内并行度 |
|---|---|---|---|---|---|
| **internal/lingdoc/workspace** | **690.6** | **86%** | 763 | 985.8 | 1.43 |
| internal/lingdoc/workspacecore | 443.7 | 55% | 674 | 571.0 | 1.29 |
| internal/router | 112.9 | 14% | 335 | 125.5 | 1.11 |
| internal/database | 36.9 | 5% | 48 | 35.1 | — |
| lingdoc/membership | 27.7 | | 64 | 36.6 | |
| lingdoc/demoroute | 26.4 | | 6 | 37.4 | |
| internal/container | 25.3 | | 59 | 23.3 | |
| lingdoc/candidateadoption | 10.1 | | 180 | 8.8 | |
| lingdoc/templategovernance | 9.4 | | 45 | 10.6 | |
| lingdoc/rulesetstore | 9.0 | | 33 | 8.8 | |
| 其余 12 包（各 ≤7.1） | ≈36 | | ≈730 | ≈18 | |
| **合计** | Σ1421.6 | | **2937** | Σ1860.6 | |

调度证据（各包首事件时刻，相对窗口起点）：轻包 0–40s 内起跑；**workspace +110.1s / workspacecore +113.4s**（-race 编译错峰）；**container +436.8s / evidence +438.8s**——排在 workspacecore 让出槽位之后。即：**单命令 4 槽调度下，wall 关键路径 = workspace 包墙 690.6s**；末两包纯属排队受害者。

最慢单测（run→end，top-7 全部在 workspace）：50.3s（TestR26AgentDraftOldEditReplayAfterRemovalRechecksOriginalSource）、38.0s、36.0s、28.6s、27.5s、25.4s、24.8s；workspacecore 最大 22.8s。**workspace 763 项测试串行和 985.8s，即使包内并行也压不过 690s 的包墙**——这是包粒度拆分的地板，与 provider 分片报告 §2.2/§4（gate 侧 workspace 片 job 墙 660s）互为印证。

### 3.3 post 段（≈225s）构成

CI artifact 无逐子命令时间戳（zip 剥离 mtime；`report.json` 只有结果），本段以「聚合 225s」为准绳，分量由本地同口径实测（§3.4）标定方向：vet（5 包根）、build（整服务二进制）、full-server-restart（起停真实进程两次）、g6（秒级）、rollback-roundtrip（39 迁移 down+up）、fixture drop。

### 3.4 本地分桶实测（WSL，一次性 PG，同 flags）

复现命令：`docker run -d --rm --name pg735 -e POSTGRES_DB=lingdoc_test -e POSTGRES_USER=lingdoc_test -e POSTGRES_PASSWORD=synthetic_ci_only -p 5433:5432 postgres:17`（已 `docker stop pg735` 清理）；DSN 同 CI（host=127.0.0.1 port=5433）；`wsl.exe go test -race -json -count=1 -timeout=40m -mod=readonly <桶>`，串行分桶。**本地 wall 含该桶冷 -race 编译**（CI sweep 内编译错峰已摊入窗口），故只作方向标定，不替代 CI 数字：

| 桶 | 成员 | 本地 wall |
|---|---|---|
| B1 | ./internal/database | 103s |
| B2 | workspace + workspacecore | **724s**（对照 CI 双包墙和 690.6+443.7 并行≈max=690.6 → 本地≈CI×1.05） |
| B3 | lingdoc 其余 16 包 | 54s |
| B4 | ./internal/router | 276s（冷编译为主） |
| B5 | container + evidence | 312s（冷编译为主） |
| 附件 | fixture create / rollback-roundtrip / drop（warm `go run`） | 3s / 2s / 1s |
| 附件 | `go build ./cmd/server`（冷） | 341s |
| NOT RUN | `go vet` 本地计时 | 计时脚本缺陷数值作废（rc=0）；CI 口径已由 post 段聚合 225s 覆盖，如实登记 |

### 3.5 构成结论

closeout ≈「**pre 145s（fixture+绑定+首批编译）+ go-test 800.7s（关键路径=workspace 单包 690.6s）+ post 225s（vet/build/restart/g6/rollback）**」。**PG 依赖段**：fixture create/rollback/drop + 4 个 failure-fixture 包（workspace/delivery/generation 的 Postgres 用例）+ rehearsal 步——其中只有 rehearsal/rollback 是「纯 PG 段」，其余 PG 依赖测试与 SQLite 测试**同包混布，无法按段分离，只能按包分离**。**非 PG 段**：vet/build/restart(SQLite)/g6/count=20。**可并行段**：go-test 包级（go test 已按 4 核调度）；job 级可并行=包族矩阵；tail 与 sweep、count=20/RuleSet 与 closeout 之间均无数据依赖（当前串行只因同 job）。

## 4. PG 依赖矩阵（源码级 + CI 覆盖位）

依据：`grep LINGDOC_TEST_POSTGRES_DSN / LINGDOC_FAILURE_TEST_DSN internal -r`、`^func TestPostgres` 计数、closeout GROUPS、app.yml（`go list ./...` **无 DSN**）。

| 包 | `^TestPostgres` 函数 | 其他 DSN 依赖 | 无 DSN 行为 | CI 中谁真跑（有 PG） |
|---|---|---|---|---|
| internal/database | **13**（迁移演练 7 + row shape guard 6） | — | 显式 skip（skip≠pass） | rehearsal 步 **和** closeout sweep（双重跑） |
| lingdoc/workspacecore | 6（含 TestPostgresMigrationAndRepositoryContract） | roadmap/pr150/audit 迁移演练读 DSN | skip | closeout sweep |
| lingdoc/workspace | 2 | failure fixture（导出损坏拒绝） | skip | closeout sweep |
| lingdoc/generation | 2 | failure fixture | skip | closeout sweep |
| lingdoc/delivery | 1 | failure fixture | skip | closeout sweep |
| lingdoc/candidateadoption | 0 | adoption_concurrency 读 DSN | skip | closeout sweep |
| lingdoc/rulesetstore | 1 | — | skip | closeout sweep |
| lingdoc/templategovernance | 1 | — | skip | closeout sweep |
| internal/container | 0 | RuleSet e2e Postgres 变体（2 文件） | skip | closeout sweep + 独立 RuleSet 步 |
| **internal/types** | **1**（json_column 方言） | — | skip | **无处真跑——取证缺口**：closeout `PACKAGES` 不含 types，app.yml 无 DSN，rehearsal 只扫 `./internal/database/...` |
| router / evidence / 其余 11 个 lingdoc 包 | 0 | 0 | 正常跑 SQLite | closeout sweep |

要点：(1) `^TestPostgres` 命名约定只在 `internal/database` 被 rehearsal 步兑现；其余包的 PG 用例靠 closeout 的全包 sweep 顺带覆盖——**拆分时必须保持「这些包落在带 DSN 的分片里」**，否则 PG 覆盖静默退化为 skip。(2) types 是取证发现的既有覆盖缺口（非本票引入，建议另行立票收口）。

## 5. 方案比较（「设计它两次」：≥2 个结构不同候选）

统一记：现状 job wall **1393–1427s**（三样本）；runner 分钟按 job wall 计（ubuntu-latest，公共仓库计费免费但计入组织并发额度）；scope job ≈8s 三案相同，不参与比较。

### 候选 A：按包族拆矩阵（3 片 + 尾部配重）

integration job → matrix：`workspace`（workspace 包）/ `workspacecore`（workspacecore 包）/ `platform`（database+router+container+evidence+lingdoc 其余 16 包，**并承载** rehearsal 之外的 tail：vet/build/restart/g6/rollback + count=20 + RuleSet）。每片各自带 postgres service、各自跑 closeout 脚本（脚本加 `--packages` 参数，各自产出 report；`workspacecore` 片可跳过 failure fixture——其 PG 用例不需要）。

| 维度 | 评估 |
|---|---|
| wall | 关键路径=workspace 片 ≈ setup 35 + PG init 15 + fixture/绑定 ≈40 + 编译 ≈60 + **690.6** ≈ **840–880s**（−36~38%） |
| runner 分钟 | ≈860+615+540 ≈ **2015s（+41~47%）**：三片各付 setup+PG+fixture 固定开销 |
| 失败归因 | **最好**：check 名即片名=包族，红片直指；票面红样本验收（「某分段失败→结论红且指到分段」）天然满足 |
| PG 服务复用 | 无复用（Actions job 间不共享 service）：3 个独立 service（+2×~15s init），但换来每片独占库、failure fixture 无跨片干扰 |
| 必需检查影响 | main **无分支保护**（2026-10-27 实测 404），无 GitHub 级必需检查；但 check 名 `integration` → `integration (workspace)` 等，`lingdoc_gate_surfaces.py` consumers 的 job_name、`test_lingdoc_gate_decision.py:996` 的 pin、wiring/surfaces 守护测试需同步；结论 artifact 的「skipped=NOT RUN 带原因」语义由 scope job 保持不变 |
| 实现面 | **中高**：脚本参数化 + 每片 report + 聚合语义（全片绿才 PASS）+ workflow matrix + 守护测试 |

### 候选 B：closeout 分步多 job（按步骤边界拆 3 job）

`sweep`（fixture+go-test 全量+rollback）/ `tail`（vet+build+restart+g6，无 PG）/ `contracts`（count=20+RuleSet，带 PG）。脚本几乎不动（sweep job 继续跑整脚本或加 `--skip-tail` 类旗标）。

| 维度 | 评估 |
|---|---|
| wall | sweep 片 ≈ 145+800.7+60+固定 50 ≈ **1030–1060s（−25%）**——800s 大头原样保留在单片 |
| runner 分钟 | ≈1035+300+120 ≈ **1455s（+3~5%）**，近乎持平 |
| 失败归因 | 按阶段清晰（测试/vet/重启/契约），但 sweep 仍是 2937 项单片大日志，包级定位靠人翻 |
| PG 服务复用 | 2 个 service（tail 免 PG） |
| 必需检查影响 | 同类接线更新但无 matrix 展开名问题，改动面小 |
| 实现面 | **低** |

### 候选 C：pg/非 pg 拆矩阵（对照，否决）

「PG 片 vs 非 PG 片」在包粒度上不成立：workspace 763 项里只有 2 个 PG 函数、workspacecore 674 项里 6 个——把 PG 用例单独成片必然用 `-run` 正则在包内切，这违反 provider 分片报告 §2.2/§4 的既定裁定（改变「每分片跑同一条测试命令」语义、ADR 0014 红线二的邻接面，该报告已明确「另行立票」）。若退到「纯 PG 段（rehearsal/rollback）vs 其余」，go-test 800s 大头纹丝不动，wall 收益 ≈126s（rehearsal 步），不构成候选。**否决理由如实入档：切不开包内 PG/非 PG，只能切包；切包即候选 A。**

### 三维对比汇总

| 维度 | 现状 | A 包族矩阵 | B 分步多 job | C pg/非pg |
|---|---|---|---|---|
| job wall | 1393–1427s | **≈860s（−37%）** | ≈1045s（−25%） | ≈1380s（≈0） |
| runner 分钟 | 1430s | 2015s（+42%） | 1455s（+4%） | ≈持平 |
| 失败归因 | 单片大日志 | **片名=包族，最优** | 按阶段，次优 | 最差（-run 切片） |
| PG service 数 | 1 | 3 | 2 | 2（若成立） |

## 6. 选定方案与决策记录草案

### 6.1 选定：候选 A（包族 3 片矩阵 + 尾部配重到 platform 片）

理由：

1. **唯一触及 wall 地板**：go-test 关键路径=workspace 单包墙 690.6s（§3.2），任何不拆 workspace 包的方案都压不过它；候选 A 把 wall 从 1400s 压到 ≈860s，即「票面目标：降向最大分段」的字面达成。再往下只能包内拆分（`-run`/拆包）——语义变更，维持 provider 分片报告的裁定另行立票。
2. **失败归因直接服务验收标准**：#735 红样本验收（分段失败→红且指到分段）在 matrix 下是平台原生行为。
3. **结构同构**：与 #728 已落地的 gate provider 分片（matrix+聚合+判定模块纯数据输出）同一模式，守护测试与心智都有先例可抄。
4. runner 分钟 +42% 是已知代价：公共仓库 Actions 免费、concurrency cancel-in-progress 已限浪费、收益是每个受影响 Go PR 省 ~9 分钟 wall。

### 6.2 wall 目标校准（票面「wall 目标（取证后校准）」）

受影响 Go 类 PR 的 integration wall：**1393–1427s → ≈860–900s（约 14.3–15 分钟；−37%）**。地板声明：workspace 单包 690.6s（CI 实测）+ 片级固定 ≈150–170s（setup+PG+fixture+编译）→ **包粒度拆分的物理下界 ≈850s**；要进 10 分钟以内必须做 workspace 包内拆分或拆包（独立决策，另票）。

### 6.3 决策记录草案（ADR 0017，本票不落文件）

> - **编号**：0017（#736 的工作区分片决策为 ADR 0016；**本票不建 ADR 文件**）。
> - **标题**：integration closeout 按包族拆片（3 片矩阵 + 尾部配重）。
> - **Context**：integration job 1393–1427s 中 closeout 占 83.5%（1162–1169s）；closeout 内 go-test 窗口 800.7s 的关键路径是 workspace 单包墙 690.6s（CI artifact 40475262990 实证，2937 测试）；PG 依赖测试与 SQLite 测试同包混布，按段分离不可行、按包分离可行。
> - **Decision**：integration job 改 3 片 matrix（workspace / workspacecore / platform），每片独立 postgres service 与 closeout report；vet/build/restart/g6/rollback/count=20/RuleSet 配重到 platform 片；`workspacecore` 片免 failure fixture。聚合语义：全片 PASS 才算本 gate PASS；scope 判定 skipped 时全片 skip、结论 artifact 保持「NOT RUN 带原因」。
> - **Consequences**：wall −37%（≈860s）；runner 分钟 +42%；check 名由 `integration` 变矩阵展开名（main 无分支保护，无必需检查破坏；gate_surfaces/判定 pin/守护测试同步）；不触碰 ADR 0014 红线（未用 `-run` 切包、每片同一条测试命令语义、`-race -count=1` 保留）。
> - **Alternatives**：分步多 job（wall 仅 −25%，否决为次选）；pg/非 pg 拆片（需 `-run` 切包内，违反 0014 邻接裁定，否决）。

## 7. 实施蓝图（选定方案的分票建议）

| 子票 | 内容 | 阻塞 |
|---|---|---|
| S1 脚本参数化 | `lingdoc_backend_closeout.py` 加 `--packages`（纯数据、缺省=现 5 包根，向后兼容）；fixture 生命周期随 `--packages` 走（`--skip-fixture` 供 workspacecore 片）；每片独立 `report.json`；聚合器（或在 scope/conclusion 步聚合） | 无，可立即开工 |
| S2 workflow matrix | integration job → matrix 3 片（片名/包清单从判定模块或脚本常量读，YAML 不写死包名——沿用 gate 「工作流零包名」先例）；每片 `services.postgres`；platform 片承载 tail+count=20+RuleSet；`timeout-minutes` 40→25 校准；聚合步保证工作流结论语义 | S1 |
| S3 守护测试+文档同步 | `lingdoc_gate_surfaces.py` consumers job_name（矩阵展开名）、`test_lingdoc_gate_decision.py:996`/`test_lingdoc_gate_surfaces.py:121`/`test_lingdoc_workflow_wiring.py:50` 的 pin、concepts/GLOSSARY「分片」条目扩展到本 gate；`docs/08-本轮实施方案/` 与 development-rules 门禁层清单核对（工作流数量不变） | S2 定稿形态 |
| S4 真实验证+前后对比 | 同口径 dispatch 分步耗时对比（本报告 §2 表为 before）；红样本注入（某片必失败测试 → 工作流红且信息指到该片）；PR Draft 保持到矩阵行证据齐 | S3 |

配套另票建议（非本票范围）：(a) `internal/types` TestPostgres 无 CI 真跑覆盖——把 `./internal/types` 纳入 rehearsal `-run` 范围或 closeout 包面；(b) workspace 包内拆分（`-run`/拆包）以打破 690s 地板。

## 8. 验收矩阵（本票 = 取证+设计阶段）

判定口径：`PASS` / `FAIL` / `NOT RUN`（`docs/agents/development-rules.md`）。

| # | 验收项 | 证据 | 状态 |
|---|---|---|---|
| M1 | workflow 全步拆解 + 近 3 次实跑 run 的分步耗时表（run ID 可复核，退避重试采集） | §1、§2；原始 JSON `D:\wt\gp2-735-data\jobs-<rid>.json`（12 run 采集，3 实跑+9 skipped 如实区分） | **PASS** |
| M2 | closeout 内部构成：子命令时间轴 + go-test 包级/测试级分布（CI artifact 实证） | §3.1–3.3；artifact 解析自 run 40475262990（2937 测试、逐包墙、top 慢测、调度错峰证据） | **PASS** |
| M3 | PG 依赖矩阵（包 × PG 用例 × 无 DSN 行为 × CI 覆盖位） | §4（源码 grep + workflow + app.yml 对照；含 types 缺口发现） | **PASS** |
| M4 | 本地 `go test -json` 分桶实测（一次性 PG，用后即停） | §3.4（B1–B5 + fixture 3 项；`pg735` 已 stop；go_vet 计时缺陷如实 NOT RUN） | **PASS**（vet 计时段 NOT RUN） |
| M5 | ≥2 个结构不同候选 + wall/runner 分钟/失败归因三维对比 + 选定理由 | §5（A/B/C 三案）+ §6.1 | **PASS** |
| M6 | 决策记录草案（编号 0017，**本票不建 ADR 文件**） | §6.3（编号勘误：工作区分片为 ADR 0016，0015 系 main 既有 read-audit ADR） | **PASS** |
| M7 | 实施蓝图：分票拆分 + 阻塞边 | §7（S1–S4 + 阻塞边 + 另票建议） | **PASS** |
| M8 | 零代码改动；仅本报告落 `docs/08-本轮实施方案/`；提交前 merge origin/main；不 push、不开 PR、不动其他工作树 | git 记录（单提交 `docs(ci): #735 …`）；远端无推送 | **PASS**（提交后） |

NOT RUN 汇总：`go vet` 本地分段计时（脚本缺陷，rc=0，无结论影响）；失败路径 run 样本（近 12 次该工作流无失败 run，红路径以脚本退出码语义口径登记）。

## 9. 遗留风险

1. **采样窗口集中**：3 个实跑样本同日（2026-10-27）、分支族窄（residual-batch2 / 696-acceptance-c1），但 closeout 步方差极小（1162–1169s，±0.3%），方向结论稳健；绝对值仍建议实施票复核时取区间。
2. **wall 地板约束**：workspace 单包 690.6s 决定候选 A 后仍 ≈14–15 分钟；「降向最大分段」达成，但与 Phase 0 校准说明里 8–10 分钟的期望区间需靠 workspace 包内拆分（语义变更另票）弥合，预期管理要提前。
3. **check 名变化的下游面**：main 当前无分支保护（实测），但若未来把本工作流纳入必需检查，矩阵展开名需重新登记；S3 的 pin 同步清单必须全绿，否则守护套件（967 项）红。
4. **PG service ×3 的组织并发**：三片并行占 3 runner，高峰期与 gate provider 3 分片叠加时并发额度竞争（cancel-in-progress 只防同 PR 重复，不防跨 PR）。
5. **fixture 固定开销 ×3**：S1 的 `--skip-fixture` 只省 workspacecore 片 ~60s；若未来 failure-fixture 包分布变化（如迁出 workspace），配重需随之复核。
