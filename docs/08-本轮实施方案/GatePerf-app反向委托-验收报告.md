# GatePerf-app 反向委托：验收报告（票 #737）

- 关联：治理票 #719／本票 #737；先例 #723（前端委托，同型反向）、#724（provider 分片）；语义依据 [ADR 0014](../adr/0014-gate-evidence-sharding-frontend-delegation-red-lines.md) 第一节（工作流不新增路径知识）、第二节（verified_by 记分片名、require-coverage 不弱化）、第四节（四条红线）。
- 分支：`ticket-737/app-delegation`（工作树 `D:\wt\gp2-737`）。提交序列：取证 `e65773da` → 红样本 `620a6ba5` → 实现 `ea51b4f8` → merge origin/main `0c50ced9` → 本报告。
- 取证部分（独立交付）：`GatePerf-docs560全量provider归属取证报告.md` —— 根因=`scripts/ci/` 治理面把新增守护测试当「gate 判定自身被改」，触发治理全量回退；缩窄方案单列未实施。

## 0. 施工并行面核对

`scripts/lingdoc_parallel_surfaces.py` loader 数据核实（2026-10-27）：唯一 claimed 面 PF-696（#696），与本票文件（`.github/workflows/app.yml`、`scripts/ci/lingdoc_gate_{surfaces,decision,concepts}.py`、三个 `test_lingdoc_*.py`、`GLOSSARY.md` 门禁判定节、两份报告）零交集；并行票 #735/#736 的工作树未被触碰。

## 1. 语义（设计决策，本票无 ADR——与 #735 同例，记录于报告，ADR 编号让位并行票）

app 工作流与 gate 的 provider 分片在 PR 事件上双跑：gate 分片 `-race -count=1` 真跑被触及的 provider 包（ADR 0014 红线二：无 `-race` 的 app 测试不构成 gate 证据的替代品），app 又以非 `-race` 复测同包。本票把方向定为**「谁有更强证据谁持有证据」**：

1. **委托恰在分片被选中时成立**：判定模块新增输出键 `provider_delegate`（分片包清单的并集，`provider_shards` 非空时非空）。app 工作流仅在 `pull_request` 事件且该键非空时把它从 `go list ./...` 测试集剔除；未触及 provider 包的 PR（键为空）与 push 到 main（事件门）**行为与现状逐字节一致**——不产生「provider 包没人测」的缺口：app 补测未被触及的部分，分片持有被触及部分的 race 证据。
2. **fail-open 三层**：非 PR 事件→全量；键为空或 `go list <清单>` 解析失败→全量；剔除后测试集为空→全量。宁可复跑，不可漏跑。
3. **coverage 署名方向登记在面清单**：app 面新增 `delegates: "pr-gate-provider"`（唯一登记处，零路径复制）。判定模块在计算 coverage 时，被委托面在**委托目标真实运行的本 head**上不再署名被接管的路径行——由分片 job 名署名；目标未运行时 app 的署名保留（fail-open）。
4. **必需检查与触发不变**：app 的 job 显示名 "Format, vet, test, and build"、gate 的 "LingDoc PR gate"、双方触发器均未动；四条红线逐条核对见 §3 M9/M10 与 §4。

## 2. 验收矩阵（判定状态口径：PASS / FAIL / NOT RUN / DEFERRED_MANUAL）

| # | 矩阵行 | 证据（命令 / seam） | 状态 |
|---|---|---|---|
| M1 | 取证独立交付：docs/560 全量 provider 根因定位（artifact 为证）+ 缩窄方案单列 | `GatePerf-docs560全量provider归属取证报告.md`（run `40421100592`、artifact id `12699430350`、`scope.json` provider 段、gate-conclusion「Gate judged by the whole scope (scripts/ci/)」） | PASS |
| M2 | `provider_delegate` 输出：恰在分片选中时非空、与分片清单并集同源；未触及 provider 为空 | `test_lingdoc_gate_decision.py::ProviderDelegationTests::test_a_provider_change_publishes_the_delegated_packages`、`test_a_change_touching_no_provider_publishes_no_delegate` | PASS |
| M3 | coverage 归属正确：provider 行 verified_by 记分片 job 名、不记 app job；watched_by 保留 app（fail-open 背书者） | `ProviderDelegationTests::test_the_coverage_of_a_provider_row_names_the_shards_not_the_app_job` | PASS |
| M4 | coverage fail-open：分片未运行时 app 署名保留（含分片 step 接线坏掉的注入形态） | `ProviderDelegationTests::test_when_the_shards_do_not_run_the_app_job_keeps_the_claim`、`test_a_delegate_that_is_not_running_leaves_the_app_claim_intact` | PASS |
| M5 | 红→绿：PR 事件上 app 测试集剔除委托包（步骤源码真执行，stub go 记录 vet/test 实参） | `test_lingdoc_workflow_wiring.py::AppProviderDelegationWiringTests::test_a_pull_request_test_set_leaves_out_the_delegated_packages`（红样本在 `620a6ba5` 为 FAIL，`ea51b4f8` 后 PASS） | PASS |
| M6 | push 到 main 全量保持（事件门，委托输出被忽略） | `AppProviderDelegationWiringTests::test_a_push_keeps_the_full_list` | PASS |
| M7 | fail-open（workflow seam）：空委托 / 清单解析失败 / 剔除将清空测试集 → 全量 | `AppProviderDelegationWiringTests::test_an_empty_delegation_keeps_the_full_list`、`test_an_unresolvable_delegation_keeps_the_full_list`、`test_a_delegation_that_would_empty_the_list_keeps_it` | PASS |
| M8 | 红线四：工作流不新增路径知识（步骤零路径、包清单只来自判定输出） | `AppProviderDelegationWiringTests::test_the_delegation_step_carries_no_package_names`、`test_the_verify_job_reads_the_declared_delegation_output`（跨 job 读=显式声明） | PASS |
| M9 | 红线一/二/三不碰：cancel-in-progress 未动；分片 `-race -count=1` 未动；require-coverage 不弱化 | 回归绿：`ProviderMatrixWiringTests::test_every_shard_keeps_race_and_fresh_counts`、`test_a_failed_shard_fails_the_gate_and_names_it`（抽掉分片标记→gate 红，#724 既有钉住）；`BaseMoveCoverageTests` require-coverage 用例回归绿；`git diff` 核对 `cancel-in-progress`/`-race -count=1` 无改动 | PASS |
| M10 | 面清单登记：`delegates` 字段（唯一登记处、引用校验、自委托拒绝、宽松读取防御） | `test_lingdoc_gate_surfaces.py::ManifestLoadTests::test_the_app_surface_registers_its_provider_delegation`、`test_a_surface_without_a_delegation_names_none`、`test_the_lenient_loader_reads_the_delegates_claim_defensively`；`ManifestValidationTests::test_an_unknown_delegate_target_is_rejected`、`test_a_self_delegation_is_rejected` | PASS |
| M11 | 接线三方一致：scope 声明=转发=读取；白名单例外显式登记（wiring+contracts 双处） | `WorkflowWiringTests::test_the_scope_job_declares_exactly_the_keys_of_its_own_jobs`（`SCOPE_DELEGATION_KEYS`）、`test_lingdoc_gate_contracts.py::WiringTriadTests::test_a_workflow_declares_exactly_the_keys_it_publishes`（`DELEGATION_FORWARDS`） | PASS |
| M12 | 概念登记：「反向委托」进 `lingdoc_gate_concepts.py` 与 `GLOSSARY.md` 门禁判定节（含 _避免_ 行） | `test_lingdoc_gate_contracts.py::DocumentationContractTests`（双向契约）回归绿 | PASS |
| M13 | 必需检查名不变：app 显示名、gate 显示名、触发器不动 | `test_the_gate_job_aggregates_the_shards`（"LingDoc PR gate"）回归绿；`git diff .github/workflows/` 仅 app.yml 三处增补；`test_lingdoc_gate_workflow_triggers.py` 回归绿 | PASS |
| M14 | 全套守护全绿（主回归门） | `python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'`：**Ran 999 tests, OK**（merged head `0c50ced9`，Windows host，192.2s；新增 17 项：decision 5 + surfaces 5 + wiring 7） | PASS |
| M15 | PyYAML 自检 | `yaml.safe_load(app.yml)`、`yaml.safe_load(lingdoc-review.yml)` OK；`python scripts/ci/lingdoc_gate_surfaces.py` → 13 gate surfaces validated | PASS |
| M16 | 同口径 wall 对比（app job PR 事件，前 vs 后，真实 run） | 见 §4：本地估算 PASS；真实 run **pending-orchestrator** | pending-orchestrator |

## 3. 红↔绿用例对照（实现点 ↔ 证据）

| 实现点 | 红（`620a6ba5`，FAIL/ERROR） | 绿（`0c50ced9`，PASS） |
|---|---|---|
| 判定模块发布 `provider_delegate` | 2×ERROR（KeyError：键不存在） | M2 两用例 |
| coverage 分片署名（`delegates` 求毛逻辑） | 1×FAIL（app job 仍被记入 verified_by） | M3 |
| 面清单 `delegates` 字段 + 校验 | 2×ERROR（AttributeError）+ 2×FAIL（未拒绝坏引用） | M10 五用例 |
| app.yml scope 声明 + verify 读取 | scope 断言 2×FAIL（`provider_delegate` 未声明/未转发） | M11 两用例 |
| app.yml 步骤剔除委托包 | 1×FAIL（vet/test 实参含被委托包） | M5 |
| 「清空回退」证据行 | 1×FAIL（无该措辞） | M7 第三用例 |

「抽掉 gate 分片证据 → 红（require-coverage 方向）」由既有钉住承担且回归绿（M9）：分片标记缺失/失败 → gate 聚合红并点名分片（#724）；被碰面无真实运行证据 → scope 步骤失败（#602）。本票新增的 M4/M7 把**委托不成立时不丢证据**（fail-open）的反方向钉住——两层合成后，委托的引入没有制造任何新的静默绿路径。

## 4. 同口径 wall 对比

**基线（Phase 0，真实 runner，PR 事件）**：app job wall 228–238s（3 run），内部大头「Vet and test」168–186s（无 `-race`）；provider 分片非 race 校准（#724 §2.1，`-count=1`，WSL）：域内主包族合计 ≈480s、三根合计 ≈17s。

**本地估算（非 runner 口径，供量级判断）**：剔除集=被触及 provider 包（分片并集），app job 的节省=这些包在该 job 内本会重跑的非 race 份额（Go 结果缓存使未失效包 ≈0s）：

- 未触及 provider 的 PR：键为空，行为逐字节同现状，节省 0。
- 触及叶子 provider 包的 PR：节省 ≈该包非 race 时长（#724 校准 0.8–13s 量级）。
- 触及 workspace 族（63.4% 份额）的 PR：节省受限于「Vet and test」步内该族重跑份额，估计 30–90s 量级（对 228–238s 的 job wall 即 13–38%）。
- push 到 main：0（全量保持）。gate 侧：0（分片未动）。

**pending-orchestrator（真实采证项，编排者统一推送后执行）**：

1. 触及 provider 包的 PR（理想形状：改一个 `internal/lingdoc/workspacecore` 测试文件）各开一次：观察 app job「Vet and test」步日志的 `Delegated N provider package(s)` 行与 job wall，对比 Phase 0 基线 228–238s；
2. 同 PR 确认 gate 分片照常选中且绿（provider 证据归属分片）；
3. 纯 docs PR 一次（回归口径：应与现状一致，不受本票影响）。

## 5. 遗留风险与边界

- **provider_delegate 与分片清单的耦合**：键值取自分片并集；若未来分片策略改变「并集=触及 provider 包」的恒等式（例如分片裁剪包），app 剔除集自动跟随——这是设计意图，但 M2 的同源断言（`test_a_provider_change_publishes_the_delegated_packages` 第二段）是唯一钉住点，改动分片契约时须保持它绿。
- **app job 在 PR 事件不再 vet 被委托包**：vet 证据由 gate 分片的 `go vet $SHARD_PACKAGES` 承担（同 head）；若未来分片被裁剪而触及的包不再入片，fail-open 让 app 自动接管（M4/M7），无静默缺口。
- **Windows host 的 bash 解析**：新增 wiring 用例从 PATH 探测真 bash（两行 smoke 脚本），绕开 CreateProcess 先命中 System32 WSL bash 启动器、多行 `-c` 脚本首行被吞的宿主陷阱；runner 与 WSL 内不受影响。若某宿主只有 WSL bash 可用，用例如实报 AssertionError（环境限制，不静默跳过）。
- **取证缩窄方案未实施**（`scripts/ci/` 治理面过宽）：docs+守护测试类分支仍会触发全量 provider 面（567–934s，Phase 0 口径）；方案 A 已单列于取证报告 §5，可另票。
