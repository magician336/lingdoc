# GatePerf-provider分片：验收报告（票 #724）

- 关联：Spec #720（PR gate 耗时优化）／治理票 #719／本票 #724；前置：#721（Phase 0 实测）、#722（ADR 0014）、#723（前端委托）。
- 分支：`ticket-724/provider-shards`（工作树 `D:\wt\gp-724-shards`，起点 `2e0baa1e`，已含 #723）。
- 语义依据：[ADR 0014](../adr/0014-gate-evidence-sharding-frontend-delegation-red-lines.md) 第一节（分片是判定模块的数据输出、空分片定义掉、不新增第二个跑不跑入口、工作流不新增路径知识）与第二节（verified_by 记分片 job 名、require-coverage 不弱化）。
- Phase 0 校准：provider 步均值 657s、最大 934s、占 wall 63–86%；报告遗留风险③明说「分片参数需实施票用 `go test -json` 补测」——本报告 §2 即该补测。

## 0. 施工并行面核对

`scripts/lingdoc_parallel_surfaces.py` 登记的是应用代码面；`scripts/ci/lingdoc_gate_*.py`、`.github/workflows/lingdoc-review.yml`、`GLOSSARY.md` 门禁判定节均无面条目（2026-10-26 以 loader 数据核实，口径同 #723 报告 §0）。Spec #720 子票间的文件边界以票面为准：本票不碰前端委托（#723 已落）、不碰 cancel-in-progress（#550）、不碰 `-race -count=1` 与 require-coverage 语义、不改 gate job 名（必需检查稳定名）。

## 1. 验收矩阵（开工前立，证据随行补齐）

判定状态口径：`PASS` / `FAIL` / `NOT RUN` / `DEFERRED_MANUAL`（`docs/agents/development-rules.md`「门禁状态和例外语义」）。

| # | 矩阵行 | 证据（命令 / seam） | 状态 |
|---|---|---|---|
| M1 | 分片是纯数据输出：给定包清单 → 分片契约（覆盖全部包、无空片、无重叠、确定性顺序）；与 provider_packages 同源（从 PROVIDER_ROOTS 派生） | `scripts/ci/test_lingdoc_gate_decision.py::ProviderShardTests`（纯函数 seam） | PASS（本地） |
| M2 | 域内主包按包族细分；真实仓库的每个包族都已登记、无根级散包（whole-root 展开无损） | `ProviderShardTests::test_every_family_of_the_real_main_root_is_registered`（文件系统事实 × 计划表） | PASS（本地） |
| M3 | 空 组 不 生 成：未触发 provider 验证（run_providers=false，含纯格式提交）→ 分片输出为空列表，工作流 matrix 整组跳过沿用既有条件语义 | `ProviderShardTests` 空清单用例 + `test_lingdoc_workflow_wiring.py` matrix 条件断言（`run_providers == 'true'` + `!= '[]'` 双保险，go-lint 先例） | PASS（本地） |
| M4 | 红：某分片含必失败测试 → gate 红（必需检查）且失败信息含该分片名 | 故障注入：对 gate job publish 步内嵌聚合逻辑做真执行（PROVIDERS=failure + 抽走该片 .passed 标记）→ SystemExit≠0 且输出含分片名；分片 leg 的 not-empty 断言同样真执行（零通过 → 退出码≠0 且信息含分片名） | PASS（本地） |
| M5 | 红：抽掉 provider 面运行证据 → gate 红（require-coverage 不弱化，#602 语义不变） | 既有 `BaseMoveCoverageTests::test_the_gate_fails_the_scope_step_when_a_surface_has_no_evidence`（补丁清单 + `publish(require_coverage=True)` 抛 SystemExit）；分片后 provider 面 verified_by 记分片 job 名 | PASS（本地） |
| M6 | 「加第 5 个根只改清单」：清单新增根 → 输出含新分片；工作流零改动（YAML 不含任何包名/根名） | `ProviderShardTests::test_a_fifth_root_yields_a_shard_without_a_workflow_change` + `test_lingdoc_workflow_wiring.py::GateStepWiringTests::test_the_workflow_carries_no_package_names` | PASS（本地） |
| M7 | 工作流 matrix 消费判定输出：providers job 声明消费 `provider_shards`（fromJSON），prepare job outputs 显式声明转发的键；每分片保留 `-race -count=1` | `test_lingdoc_workflow_wiring.py::ProviderMatrixWiringTests`（PyYAML 解析真实 workflow）+ 既有 `WiringTriadTests`（双向接线契约） | PASS（本地） |
| M8 | gate job 名不变（`LingDoc PR gate`，必需检查稳定名）；gate job 汇总 prepare+providers（`if: always()` 聚合，任一红则必需检查红）；门禁始终出现 | `test_lingdoc_gate_queue_state.py`（既有）+ `ProviderMatrixWiringTests::test_the_gate_job_aggregates_the_shards`；`lingdoc_required_checks.py` 扩展登记 gate 内部 job 名 | PASS（本地） |
| M9 | 概念登记：「分片」进 `lingdoc_gate_concepts.py` 与 `GLOSSARY.md` 门禁判定节（定义 + _避免_ 行，与「委托」条目风格一致）；「委托」不动 | `test_lingdoc_gate_contracts.py::DocumentationContractTests`（双向契约：文档措辞 ↔ 判定字段/输出键） | PASS（本地） |
| M10 | coverage 归属：provider 面 verified_by 记分片 job 名（matrix 展开名），summary/结论表可读 | `FrontendDelegationTests` 同构的 ProviderShardTests 用例（coverage 断言）+ publish 步内嵌分片表 | PASS（本地） |
| M11 | 全套守护测试全绿（#723 后基线 906 项） | `python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'` | PASS（本地，930 项，见 §4） |
| M12 | 真实 PR 验证：分片后 gate wall 对比 + runner 分钟双口径（与 Phase 0 报告同口径） | 由编排者在 push 后采集 | NOT RUN（pending-orchestrator） |

## 2. 分片参数校准（Phase 0 遗留风险③的补测）

方法：WSL2（go1.26.0 linux/amd64，CGO_ENABLED=1）逐包族 `go test -count=1 -json`（非 race）计时估相对比例，再对**三个分片整体**跑 `go test -race -count=1 -json` 实测 race 时长。

### 2.1 包族非 race 计时（相对比例）

| 包族/根 | 非 race 秒 | 占比 | 备注 |
|---|---|---|---|
| internal/lingdoc/workspace | 315.6 | 63.4% | **单一 Go 包（114 个测试文件，无子包）——包粒度不可再分** |
| internal/lingdoc/workspacecore | 59.7 | 12.0% | 单一 Go 包 |
| internal/lingdoc/candidateadoption | 21.5 | 4.3% | |
| internal/lingdoc/templategovernance | 13.1 | 2.6% | |
| internal/lingdoc/rulesetstore | 11.4 | 2.3% | |
| internal/lingdoc/membership | 11.0 | 2.2% | |
| internal/lingdoc/demoroute | 10.4 | 2.1% | |
| internal/lingdoc/delivery | 9.5 | 1.9% | |
| internal/lingdoc/generation | 7.9 | 1.6% | |
| internal/lingdoc/operationcore | 5.5 | 1.1% | |
| internal/lingdoc/writingaction | 5.3 | 1.1% | |
| internal/lingdoc/materiallingest | 3.3 | 0.7% | |
| internal/evidence | 5.8 | 1.2% | 根 |
| internal/models/acceptancebudget | 10.6 | 2.1% | 根 |
| 其余 lingdoc 包族（access/docx/sourcepolicy/template/reviewanchor/sourcemarker） | ≈6.0 | ≈1.2% | 各 ≤1.6s |
| cmd/acceptance-budget-reconcile | 0.8 | 0.2% | 根 |
| **合计** | **≈498** | 100% | 对照 Phase 0 race 全量 556–934s |

### 2.2 分片整体 race 实测（WSL2，`go test -race -count=1`）

| 分片名 | 成员 | 非 race 秒 | race 实测（WSL） | 说明 |
|---|---|---|---|---|
| `lingdoc-workspace` | workspace 包族 | 315.6 | **≥678.5s（触发默认 10m 包级超时 panic，rc=1；463 项已通过）** | WSL 硬件慢于 CI runner；panic 时正在跑 `TestActualOneConnectionCurrentSourceProjectionReplayAndNoDataLoss`。该值是**下界**（被 10m 截断） |
| `lingdoc-core` | 其余 17 个 lingdoc 包族 | 165.4 | 313.5s（rc=0） | race/非 race ≈ 1.9× |
| `provider-rest` | evidence + acceptancebudget + reconcile 三根 | 17.2 | 41.0s（rc=0） | race/非 race ≈ 2.4× |

- 全量贡献面 → **3 个分片 job**；CI 口径换算（Phase 0：race 全量 556–934s / 非 race 498s，比值 1.12–1.88）：workspace 片 ≈ 350–590s、core 片 ≈ 185–310s、rest 片 ≈ 20–45s。wall 由「四根串行求和 657s 均值」变为「最大分片 ≈ workspace 片」。
- **结构性下界（如实登记）**：workspace 是单一 Go 包，`go test` 以包为最小单元——最大分片 ≈ workspace 族时长，是包粒度分片的地板。再往下切只能用 `-run` 正则把一个包拆开，那会改变「每个分片跑同一条测试命令」的语义（ADR 0014 红线二的邻接面），本票**不做**；按族细分的意义在于其余三个根与 17 个包族不再串行叠加在 workspace 之后。
- **10 分钟包级超时是既有风险、非本票新增**：`go test` 的默认 `-timeout 10m` 按包生效——旧串行步里 workspace 同样是「一个包一个 10m 窗口」，分片不改变它的暴露面。CI 证据：Phase 0 的 17 次 race 全量 run 全绿（总长 556–934s），说明 CI runner 上 workspace 包 <10m。WSL 实测把这条余量薄的风险显形了，登记进 §6。
- 小 PR 自动少起 job：只碰单族的贡献面只生成 1 个非空分片（空分片被定义掉）。
- runner 分钟口径：全量场景 3 片之和 ≈ 串行之和，加每 job 固定开销（checkout/setup-go ≈40–60s/片）；wall 收益与账单代价并记，M12 由编排者采真实双口径。

## 3. 红绿样本对照

提交序列：`d65b0777`（docs：矩阵先行）→ `1c6ec703`（test：红样本，24 处失败）→ `debce318`（feat：转绿）。

| 样本 | 红（`1c6ec703` 时点） | 绿（`debce318` 时点） |
|---|---|---|
| 分片纯函数契约（`ProviderShardTests`，12 用例） | `decision._provider_shards` / `Decision.provider_shards` 不存在 → 10 ERROR；2 FAIL 如实暴露旧行为 | 全量根 → `[lingdoc-workspace, lingdoc-core, provider-rest]` 三片、每包恰达一片、无空片、顺序确定；空清单/未触发/纯格式 → `provider_shards=()` 且输出 `[]` |
| coverage 归属 | `verified_by=('Format, vet, test, and build', 'integration; frontend', 'Provider packages: test and vet')`——记的是不再跑测试的 gate 内步骤名 | `verified_by` 含 `Provider shard lingdoc-workspace`（matrix 展开名），不再含 `Provider packages: test and vet`；结论 checks 的 reason 逐片列「分片 job 名（包清单）」 |
| 工作流接线（`ProviderMatrixWiringTests` 12 用例 + `test_the_provider_matrix_job_is_gated` 重写） | workflow 无 `prepare`/`providers` job → 9 ERROR + 3 FAIL | matrix=`fromJSON(needs.prepare.outputs.provider_shards)`、fail-fast=false、条件=run_providers 布尔 + `!= '[]'` 双保险；prepare job outputs 显式声明转发键；每片保留 `go test -race -count=1` |
| 红：某分片失败（故障注入，**真执行** gate publish 步内嵌聚合逻辑） | 旧 publish heredoc 不聚合 providers → PROVIDERS=failure 仍不退出（SystemExit=None） | `SystemExit(1)`，stderr 含 `provider shard(s) failed on this head: Provider shard provider-rest`（按缺失的 `.passed` 标记点名），result.json 仍如实发布 `providers=failure` |
| 红：分片零通过（真执行 shard 步内嵌 not-empty 断言） | 旧 provider 步 heredoc 存在但断言不点名分片 | 零 pass 事件 → `SystemExit`，信息含 `Provider shard <名>`；有通过事件 → 打印通过数（非覆盖率） |
| 红：抽掉 provider 面运行证据（#602 钉住，既有用例） | 既有 `BaseMoveCoverageTests` 两用例红态即已钉住（未随本票改动，持续 PASS） | 分片后语义不变：补丁清单下 `publish(require_coverage=True)` 仍抛 SystemExit；coverage 行 verified_by 记分片 job 名 |
| 「加第 5 个根只改清单」 | `_provider_shards` 不存在 | patch `PROVIDER_ROOTS+('internal/newthing',)` → 输出新分片 `internal-newthing`；工作流文本零包名/根名（红线四双守护：PROVIDER_ROOTS 逐条断言 + `internal/|cmd/` 正则断言） |
| gate job 聚合 | gate 单 job 无聚合 | `needs: [prepare, providers]` + `if: always()`；名字保持 `LingDoc PR gate`（必需检查稳定名）；prepare 红或任一分片红 → gate 红 |

红样本落点：`scripts/ci/test_lingdoc_gate_decision.py::ProviderShardTests`（12 用例）与 `scripts/ci/test_lingdoc_workflow_wiring.py::ProviderMatrixWiringTests` + `GateStepWiringTests::test_the_provider_matrix_job_is_gated`（合计 13 处，其中聚合/not-empty 断言为**真执行内嵌 python**，不是文本匹配）。红态合计 24 处失败（12 ERROR + 12 FAIL）。

## 4. 验证命令与结果

- 全套守护（绿实现 head `debce318`）：`python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'` → `Ran 930 tests in 177.020s` `OK`（#723 后基线 906 全绿；新增 12 分片判定用例 + 12 接线/聚合用例，净增 24）。
- 红态复核（`1c6ec703`）：同命令 → 24 处失败（`ProviderShardTests` 10E+2F；wiring 9E+3F），其余 906 项不受影响。
- YAML 自检：PyYAML `safe_load` 解析 `lingdoc-review.yml` → jobs=`[prepare, providers, gate]`；providers 条件/matrix/fail-fast 逐项断言；两处内嵌 python heredoc 可提取并真执行；文本零 `internal/|cmd/` 命中。
- 清单校验：`python scripts/ci/lingdoc_gate_surfaces.py` → `13 gate surfaces validated`（consumer 对 job 名的双向校验含新三 job 结构）。
- CLI 冒烟（真实仓 head、dispatch 全量路径）：`run_providers=true`、`provider_shards` = 3 片 JSON（workspace 1 包 / core 17 包 / rest 3 根）、结论 checks 的 provider 行逐片点名。
- 校准实测（WSL2 go1.26.0）：非 race 逐族 18+3 项（§2.1）；`-race -count=1` 三分片整体（§2.2，workspace 触发 10m 默认超时 panic 如实记录）。
- `git merge spec-720/gate-perf` → `Already up to date`（起点 `2e0baa1e` 已含）。

## 5. pending-orchestrator（NOT RUN，解除条件）

1. M12：push 后由编排者采集分片后 gate run 的分步耗时与 job 并发形态（与 Phase 0 §1.2 同口径命令），记录 wall 与 runner 分钟双口径。解除条件：PR 打开且至少一次全量贡献面的 gate run 完成。

## 6. 遗留风险

- **最大分片受单一 Go 包约束**（§2.2）：workspace 族 63% 的时长不可在包粒度内再分；若后续要进一步压 wall，需要的是包内拆分（`-run` 或拆包），属于改变测试命令语义/拆分的独立决策，不在本票。
- **workspace 包逼近 `go test` 默认 10 分钟包级超时（既有风险，本票使之显形）**：WSL2 实测该包 `-race` >10m 触发 `panic: test timed out after 10m0s`（463 项已通过）；旧串行步对 workspace 同样是「一个包一个 10m 窗口」，暴露面不变。CI 证据：Phase 0 的 17 次 race 全量 run 全绿（556–934s 总长），说明 CI runner 上该包 <10m，但余量不明。若真实 PR 验证（M12）出现分片超时红，处置方向是把 `-timeout` 显式放大（独立小改，不触碰 race/count 语义）或拆分 workspace 包，另行立票。
- **runner 计费分钟可能放大**（ADR 0014 已登记）：3 个小分片各自承担 job 固定开销，另加 prepare/gate 两段固定 wall；真实数据见 M12。
- **分片表成为第二份归属知识的风险被守护测试钉住**：包名只进判定模块（根路径连模块都不重写——计划只写包族名与根名结尾，主根从清单与真实树解析，`test_no_other_ci_script_registers_the_same_knowledge` 钉住）；工作流文本零包名由 `test_the_workflow_carries_no_package_names` 钉住；真实仓库包族全覆盖由 M2 用例钉住——新增包族未登记时该用例红，逼着开发者在模块内登记，而不是让分片悄悄漏包。
- **gate 从单 job 变三 job**：必需检查（`LingDoc PR gate`）现在聚合 prepare 与 providers；prepare 的守护套件步、scope 步、契约步语义逐条未动（无条件步、`--require-coverage`、whitespace 边界照旧）。`lingdoc_required_checks.GATE_INTERNAL_JOB_NAMES` 登记了两个内部 job 名（含 matrix 模板名），防止 gate 工作流里的检查无登记地增减。
