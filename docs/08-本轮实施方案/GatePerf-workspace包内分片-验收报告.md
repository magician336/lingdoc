# GatePerf-workspace包内分片：验收报告（票 #736）

- 关联：Spec #720（PR gate 耗时优化）／治理票 #719／本票 #736；前置：#721（Phase 0 实测）、#724（provider 分片 + ADR 0014，其验收报告 §2.2/§6 登记的包内拆分顾虑）。
- 分支：`ticket-736/workspace-adr`（工作树 `D:\wt\gp2-736`，起点 `cb57f5c9`；实现提交 `24e928d9`、决策提交 `f8fdc238`、merge origin/main 后 `41ebbd06`）。
- 语义依据：[ADR 0015](../adr/0015-workspace包内按测试名分片.md)（workspace 维持单一 Go 包，gate 按**测试名分区**把它的测试分给多个分片 job；拆子包与维持现状两候选的量化否决理由同文）。
- 本报告证据口径：合并后 head `41ebbd06`（含 origin/main 15 个提交；workspace 族测试从 495 增至 499，分区**自动**吸收，零登记）。

## 0. 施工并行面核对

治理线三票（#735/#736/#737）文件边界以共享笔记为准：本票只碰 `docs/adr/0015-*.md`、`docs/08-本轮实施方案/GatePerf-workspace包内分片-验收报告.md`、`scripts/ci/lingdoc_gate_decision.py`、`scripts/ci/test_lingdoc_gate_decision.py`、`scripts/ci/test_lingdoc_workflow_wiring.py`、新增 `scripts/ci/test_lingdoc_workspace_shard_partition.py`、`.github/workflows/lingdoc-review.yml`、`GLOSSARY.md`、`docs/agents/development-rules.md`。不碰 `-race -count=1` 语义、不碰 cancel-in-progress（#550）、不改 gate job 稳定名、不碰前端委托（#723/#734）。`internal/lingdoc/workspace` 的**产品代码与测试文件零改动**（只读枚举测试名；merge 带入的测试文件原样保留）。

## 1. 验收矩阵（开工前立，证据随行补齐）

判定状态口径：`PASS` / `FAIL` / `NOT RUN` / `DEFERRED_MANUAL`（`docs/agents/development-rules.md`「门禁状态和例外语义」）。

| # | 矩阵行 | 证据（命令 / seam） | 状态 |
|---|---|---|---|
| M1 | ADR 先行：`docs/adr/0015-*.md` 落档，比较 ≥2 个结构不同候选（拆 Go 子包／`-run` 测试名分片／维持现状），否定理由带量化事实，并对 #724 报告 §2.2 的「同一条命令」顾虑正面裁决 | ADR 0015（§背景耦合实测、§决策一/二/三、§四维持现状否决、§被否决的备选方案） | PASS |
| M2 | 分区是判定模块的纯数据输出：给定测试名清单 → 每片一个 `-run` 锚定模式；覆盖完备、两两不交、各片非空、确定性；枚举不可用时安全降级为单片不过滤 | `test_lingdoc_gate_decision.py::ProviderShardTests`（全量分片形态、确定性、`test_an_unavailable_enumeration_degrades_to_one_unsharded_shard`、`test_the_test_name_filters_are_deterministic`） | PASS（本地 + WSL） |
| M3 | 分区对真实仓库 fail-closed：workspace 族全部 `func Test…`（合并后 499 项）恰好各落一片；任何名字零片/多片 → 守护红 | `test_lingdoc_workspace_shard_partition.py` 6 用例（独立行解析 × 模块枚举双向核对；§4 命令） | PASS（WSL） |
| M4 | 工作流接线：`go_test_filter` 只从 matrix 逐片读取（工作流零测试名/零包名）；`-race -count=1 -timeout 15m` 逐字保留 | `test_lingdoc_workflow_wiring.py::ProviderMatrixWiringTests::test_a_shards_test_filter_is_matrix_data_not_workflow_knowledge` + `test_every_shard_keeps_race_and_fresh_counts` + `test_the_workflow_carries_no_package_names` | PASS（本地 + WSL） |
| M5 | 每片 not-empty 断言照旧：某片 0 个通过测试 → 该片步骤红且点名分片；分片聚合进必需检查不变 | 既有 `test_a_shard_with_no_passing_test_fails_and_names_the_shard`（真执行 heredoc）+ gate 聚合用例 | PASS（本地 + WSL） |
| M6 | 测试零删减：三片并集 = 全集、零跨片重叠、各片恰好自己的分配集（见 §3 表）；`go vet ./...` 语义不变 | §3 并集核对（脚本对 `736-run2-*.jsonl`）+ §4 vet | PASS（WSL） |
| M7 | 概念与词表同步：GLOSSARY「分片」条目扩展「枚举即分区」语义；概念登记 snippet（「分片策略只写在判定模块一处」）与 keys 兼容 | `test_lingdoc_gate_contracts.py::DocumentationContractTests`（双向契约，全套内） | PASS（WSL） |
| M8 | 全套守护测试全绿 + `go build ./...` + `go vet ./...` | 合并后 head `41ebbd06`：`Ran 992 tests … OK (skipped=1)`，rc=0；build/vet rc=0（§4） | PASS（WSL） |
| M9 | WSL 实测各新片 race 时长 <450s（目标）；与未分片下界同口径对照 | §3 实测表（逐片 rc/wall/passed） | PASS（WSL，最大片 319s） |
| M10 | 分片输出单点登记不变：调整分区策略只改判定模块一处；工作流零知识 | M2/M4 守护 + 实证：merge 带入 4 个新 workspace 测试，模块枚举 495→499、分区表 155/169/175 自动更新，零人工登记（`cd scripts/ci && python -c "import lingdoc_gate_decision as d; …"` 输出见 §4） | PASS（本地） |

## 2. 候选比较的量化事实（ADR 0015 的数据底稿）

实测于起点 `cb57f5c9`，脚本化扫描 `internal/lingdoc/workspace/`：

- 包形态：单一 Go 包，51 个产品文件 + 116 个测试文件（0 个子目录、0 个 `TestMain`、0 个 testify suite、0 个 `Example/Benchmark/Fuzz`、0 个 build tag、0 处 `t.Parallel()`）——包内无现成的结构与边界可拆。
- 未导出耦合：产品文件含 233 个未导出顶层声明；116 个测试文件中 **73 个**引用其中 **63 个**未导出标识——测试搬出包目录（外部测试包）即失去访问权。
- 共享 helper：测试文件内 282 个小写 helper 函数，其中 **96 个被 >1 个测试文件使用**（最大者 `testRuntime` 被 20 个文件使用）；**87/116** 个测试文件至少依赖一个跨文件 helper。
- 测试名主题：495 个 `func Test`，104 个名字前缀——名字前缀与文件主题不同构（`The`×15、`Handler`×14），按前缀人工分组不可维护。

## 3. 各片 race 实测（WSL2，`go test -race -count=1 -timeout 15m -json`）

合并后 head `41ebbd06`，逐片顺序执行（独占机器），模式取自该 head 的判定模块枚举（499 测试 → 分区 155/169/175）：

| 分片 | 分配测试数 | rc | wall | passed（顶层） | 跳过 | 失败 |
|---|---|---|---|---|---|---|
| `lingdoc-workspace-a` | 155 | 0 | **205s** | 154 | 2（Postgres DSN 门控） | 0 |
| `lingdoc-workspace-b` | 169 | 0 | **229s** | 169 | 0 | 0 |
| `lingdoc-workspace-c` | 175 | 0 | **319s** | 174 | 1（Postgres DSN 门控） | 0 |

- **目标达成**：最大片 319s < 450s；三片之和 753s 对照未分片下界 ≥678.5s（#724 报告 §2.2，被 10m 截断）+ 合并后新增 4 测试，重复支付的编译/固定成本可数。
- **零删减核对**：三片 passed 顶层名字并集 497/499，缺失的 2 个即 `LINGDOC_TEST_POSTGRES_DSN` 未设时的既有环境跳过（与未分片运行同行为）；跨片重叠 0；每片「extra=0」——恰好运行自己分配的集合。
- 分片前实测（实现提交 `24e928d9`，495 测试）：a=267s、b=297s、c=311s，同样全部 <450s，rc 全 0；与合并后数字的差主要为 Go 构建缓存温度，两轮结论一致。
- CI 口径预估（M12 换算比值 1.12–1.88 对 Phase 0 race 全量 556–934s）：三片 CI 墙约 185–310s，全量 gate wall 地板从 660s 降到最大片；真实 dispatch 由编排者采证（§5）。

## 4. 验证命令与结果

均在 WSL2（go1.26.0 linux/amd64，CGO_ENABLED=1）于合并后 head `41ebbd06` 执行：

| 命令 | 结果 |
|---|---|
| `go build ./...` | PASS（rc=0） |
| `go vet ./...` | PASS（rc=0，无输出） |
| `python3 -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'` | PASS：`Ran 992 tests in 157.162s / OK (skipped=1)`，rc=0（合并前 head 为 977 项 1 失败 → 修复 `test_the_coverage_names_the_shard_jobs` 的 verified_by 连接形态断言后全绿） |
| `python3 -m unittest test_lingdoc_workspace_shard_partition -v`（scripts/ci 下） | PASS：6 用例全绿 |
| `python -c "import lingdoc_gate_decision as d; …"`（分区再核对） | 枚举 499；分片 155/169/175；逐名恰匹配一片（断言通过） |
| 逐片 race（§3 表） | 三片 rc=0 |
| 提交卫生 | 全部改动文件 blob 0 个 `\r`（`grep -c $'\r'`＝0 / Python 字节计数 0）；`git -c core.autocrlf=false` 语义下 staged blob 复核 0 |

## 5. pending-orchestrator（NOT RUN，解除条件）

| 项 | 状态 | 解除条件 |
|---|---|---|
| 真实 CI dispatch（全量贡献面）的分片并发形态、分步耗时、双口径 runner 分钟 | NOT RUN | 本票不 push 不开 PR（编排者统一推送采证）；届时对 #724 报告 §4b 同口径记录三片 job 墙与 gate wall |
| CI 上 Postgres DSN 门控测试的真实归属表现 | NOT RUN | 同上——若 CI 设有该 DSN，两测试在各自分片内转 pass，不改变分区结论 |

## 6. 遗留风险

- **计费分钟上升（ADR 0014 §五口径的观察项）**：全量场景 provider job 从 3 个变 5 个，每片各自承担 checkout/setup 与一次编译；wall 约 -55%（660s→~319s WSL 上界），计费分钟约 +40%。真实数字待 dispatch。
- **哈希分桶按测试数均衡 ≠ 按时长均衡**：当前 a:c ≈ 205:319s（1.56×），余量充足；若未来单一主题测试巨型化导致某片逼近 450s，复核动作在 ADR §四（模块内显式重量级桶覆盖，数据 + 守护照旧）。守护测试保证分区**正确性**，不保证**均衡性**——这是机制的已知边界。
- **not-empty 断言的假红面**：某片若恰好全为环境跳过测试会触发零通过红；当前每片 ≥154 个非门控测试，风险停留在理论。真发生时是红灯不是假绿，方向安全。
- **测试改名跨片漂移**：改名即换片（md5），跨 run 的片墙小幅抖动；分区正确性由守护逐 head 钉住，无需人工干预。
- **「枚举 = go test 运行集」的前提被守护钉住**：族内出现 build tag / `TestMain` / testify suite / Fuzz/Example/Benchmark 任一 → `test_the_family_is_what_the_split_assumes` 红，逼着开发者回到 ADR 0015 重新裁决，而不是让分区悄悄漏测。
