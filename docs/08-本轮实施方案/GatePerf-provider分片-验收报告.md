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
| M11 | 全套守护测试全绿（#723 后基线 906 项） | `python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'` | PASS（本地，见 §3） |
| M12 | 真实 PR 验证：分片后 gate wall 对比 + runner 分钟双口径（与 Phase 0 报告同口径） | 由编排者在 push 后采集 | NOT RUN（pending-orchestrator） |

## 2. 分片参数校准（Phase 0 遗留风险③的补测）

方法：WSL2（go1.26.0 linux/amd64）逐包族 `go test -count=1 -json`（非 race）计时，估相对比例；`-race` 全量在宿主机不可行（gojieba 崩溃是 Windows 宿主已知限制），race 时长按 Phase 0 全量 race（556–934s，均值 657s）与全量非 race（本表合计 498s）的比值 ≈1.3–1.9 折算。

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
| **合计** | **≈498** | 100% | race 全量实测 556–934s（Phase 0），比值 ≈1.3–1.9 |

**分片方案（判定模块 `LINGDOC_FAMILY_SHARDS`）**：

| 分片名 | 成员 | 非 race 秒 | race 折算 |
|---|---|---|---|
| `lingdoc-workspace` | workspace 包族 | 315.6 | ≈410–600s |
| `lingdoc-core` | 其余 17 个 lingdoc 包族（未登记新族默认归此片） | 165.4 | ≈215–315s |
| `provider-rest` | evidence + acceptancebudget + reconcile 三根 | 17.2 | ≈23–46s |

- 全量贡献面 → 3 个分片 job；最大分片 race 折算 ≈410–600s，对比现串行 657s 均值 / 934s 最大：wall 下限收窄到「最大分片」，Phase 0 均值口径约 **-25% ～ -37%**。
- **结构性下界（如实登记）**：workspace 是单一 Go 包，`go test` 以包为最小单元——最大分片 ≈ workspace 族时长，是包粒度分片的地板。再往下切只能用 `-run` 正则把一个包拆开，那会改变「每个分片跑同一条测试命令」的语义（ADR 0014 红线二的邻接面），本票**不做**；按族细分的意义在于其余三个根与 17 个包族不再串行叠加在 workspace 之后。
- 小 PR 自动少起 job：只碰单族的贡献面只生成 1 个非空分片（空分片被定义掉）。
- runner 分钟口径：全量场景 3 片之和 ≈ 串行之和，加每 job 固定开销（checkout/setup-go ≈40–60s/片）；wall 收益与账单代价并记，M12 由编排者采真实双口径。

## 3. 红绿样本对照（占位，实现后补）

（见 §5 提交序列；此处随红/绿提交回填。）

## 4. 验证命令与结果（占位）

（实现后回填。）

## 5. pending-orchestrator（NOT RUN，解除条件）

1. M12：push 后由编排者采集分片后 gate run 的分步耗时与 job 并发形态（与 Phase 0 §1.2 同口径命令），记录 wall 与 runner 分钟双口径。解除条件：PR 打开且至少一次全量贡献面的 gate run 完成。

## 6. 遗留风险

- **最大分片受单一 Go 包约束**（§2）：workspace 族 63% 的时长不可在包粒度内再分；若后续要进一步压 wall，需要的是包内拆分（`-run` 或拆包），属于改变测试命令语义/拆分的独立决策，不在本票。
- **runner 计费分钟可能放大**（ADR 0014 已登记）：3 个小分片各自承担 job 固定开销；真实数据见 M12。
- **分片表成为第二份归属知识的风险被守护测试钉住**：包名只进判定模块；工作流文本零包名由 `test_the_workflow_carries_no_package_names` 钉住；真实仓库包族全覆盖由 M2 用例钉住——新增包族未登记时该用例红，逼着开发者在模块内登记，而不是让分片悄悄漏包。
