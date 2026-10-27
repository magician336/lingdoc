# GatePerf-委托失败核验：验收报告（票 #734）

- 关联：Spec #720（PR gate 耗时优化）／治理票 #719／本票 #734；前置：#721（Phase 0 实测）、#722（ADR 0014）、#723（前端委托）、#724（provider 分片）。
- 分支：`ticket-734/delegation-verify`（工作树 `D:\wt\gp-df`，起点 `db45c9ff`——含 #723 委托与 #724 分片的全部落地）。
- 语义依据：[ADR 0014](../adr/0014-gate-evidence-sharding-frontend-delegation-red-lines.md) 第三节的失败子句：「前端工作流在这个 head 上没有真实运行时（触发、排队、失败都算没有），require-coverage 必须红」。本票把这句从静态判定兑现成机制：gate 聚合（publish 步）在结论发布前查询 head SHA 的 check-runs，对被委托 job 的真实结果核验。
- 来源：#728 code-review Spec 轴 (c)——委托的「失败也算没有」无实现缝。

## 0. 施工并行面核对

`scripts/lingdoc_parallel_surfaces.py`（13 面）按 loader 数据核实（2026-10-26）：全部 `done`，无 `claimed`/`ready` 面占用 `.github/workflows/lingdoc-review.yml`、`scripts/ci/lingdoc_gate_decision.py`、`scripts/ci/test_lingdoc_*.py`。本票与任何在施票无文件交叠。

## 1. 验收矩阵（开工前立，证据随行补齐）

判定状态口径：`PASS` / `FAIL` / `NOT RUN` / `DEFERRED_MANUAL`（`docs/agents/development-rules.md`「门禁状态和例外语义」）。

| # | 矩阵行 | 证据（命令 / seam） | 状态 |
|---|---|---|---|
| M1 | 绿态（对照）：委托成立 + 被委托 job check-run `conclusion=success` → gate 绿，`result.json` 记 `frontend_delegation.outcome=verified`；summary 记委托核验行；查询 URL 命中 `GET {api}/repos/{repo}/commits/{head}/check-runs`（per_page=100） | `test_lingdoc_workflow_wiring.py::DelegationVerificationTests::test_a_successful_delegated_job_keeps_the_gate_green`（真执行 publish heredoc × 本地 check-runs stub，断言请求路径） | 待绿 |
| M2 | 红：`conclusion=failure` → gate 红，失败信息点名被委托 job 与实际状态 | `…::test_a_failed_delegated_job_fails_the_gate_and_names_it` | 待绿 |
| M3 | 红：check-run 不存在且轮询窗口耗尽 → gate 红，点名 job 与「不存在 + 窗口耗尽」 | `…::test_an_absent_delegated_job_exhausts_the_window_and_fails` | 待绿 |
| M4 | 红：`status=in_progress` 轮询至超时 → gate 红，点名 job 与实际状态 | `…::test_an_in_progress_job_polled_past_the_window_fails` | 待绿 |
| M5 | 绿：`in_progress` 在窗口内完成 → 轮询生效转绿（非一锤子判定） | `…::test_an_in_progress_job_that_finishes_in_time_is_accepted` | 待绿 |
| M6 | 红：API 500 → fail-closed，gate 红 + 原因点名（HTTP 500），且不静默重试（恰一次请求） | `…::test_an_api_failure_fails_closed_and_names_the_reason` | 待绿 |
| M7 | 归属精确性：`frontend_delegate` 为空（未碰前端 / 手动 dispatch / 其他面）→ 零查询（stub 零请求、gate 绿） | `…::test_no_delegation_claim_means_zero_queries` + decision 侧 `FrontendDelegationTests` 三用例 | 待绿 |
| M8 | 分页翻全：目标在第二页（首页 100 条填充）→ 仍找到并绿 | `…::test_the_lookup_pages_through_all_check_runs` | 待绿 |
| M9 | 同 head 多 run 不误判：旧 run `cancelled` + 新 run 进行中 → 不把 cancelled 当失败；新 run success → 绿 | `…::test_a_cancelled_older_run_does_not_fail_a_live_rerun` | 待绿 |
| M10 | 判定模块输出 `frontend_delegate`：委托成立=被委托 job 显示名；非前端/手动=空串 | `test_lingdoc_gate_decision.py::FrontendDelegationTests` 新增三用例 | 待绿 |
| M11 | 权限最小化：`permissions` 恰为 `{contents: read, checks: read}` | `DelegationVerificationTests::test_the_workflow_permissions_stay_minimal`（PyYAML） | 待绿 |
| M12 | 接线：prepare 声明 `frontend_delegate`；gate env 读 `FRONTEND_DELEGATE` 与 `HEAD_SHA`（`github.event.pull_request.head.sha`，check-runs 挂 PR head SHA，#731 实证）；无未声明读取 | `…::test_the_prepare_job_declares_the_delegate_and_the_gate_reads_it` + `test_lingdoc_gate_contracts.py::WiringTriadTests` | 待绿 |
| M13 | 轮询参数 env 可调、默认 30s 间隔 / 600s 上限；gate job 超时 ≥ 轮询窗口余量 | `…::test_the_polling_knobs_are_env_tunable_with_the_registered_defaults` + `…::test_the_gate_job_outlives_the_polling_window` | 待绿 |
| M14 | 被委托显示名跨工作流唯一（check-run 名过滤无歧义前提） | `…::test_the_delegated_check_name_is_unique_across_the_workflows` | 待绿 |
| M15 | 红线一/二/三不动：cancel-in-progress 不动、race/`-count=1` 不动、require-coverage 不弱化（本票强化它） | 既有守护仍绿：`test_lingdoc_gate_workflow_triggers.py`、`ProviderMatrixWiringTests::test_every_shard_keeps_race_and_fresh_counts`、`FrontendDelegationTests::test_removing_the_delegated_run_leaves_the_frontend_uncovered`；diff 复核并发段与 provider 步零改动 | 待绿 |
| M16 | 红线四：工作流零新增路径知识（本票只加 job 名过滤与轮询参数，无包名/目录/分片名） | 既有 `ProviderMatrixWiringTests::test_the_workflow_carries_no_package_names` 仍绿；diff 复核 | 待绿 |
| M17 | 概念登记：`frontend_delegate` 归入「委托」概念 keys；GLOSSARY 委托条目补核验句；ADR 0014 §三补核验机制小节 | `test_lingdoc_gate_contracts.py::DocumentationContractTests`（双向契约） | 待绿 |
| M18 | 全套守护测试全绿（基线 943 项）+ PyYAML 自检 | `python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'`；PyYAML `safe_load` 解析改动的 YAML | 待绿 |
| M19 | 真实 PR 验证：碰前端的 PR 上令 verify 失败（临时分支）→ gate 必须红 | 由编排者在 push 后采集 | NOT RUN（pending-orchestrator） |

## 2. 红绿样本对照

（绿后补齐）

## 3. ADR 0014 §三逐句对照

（绿后补齐）

## 4. 验证命令与结果

（绿后补齐）

## 5. gate wall 影响（已接受的正确性代价）

前端类 PR 的 gate publish 步将等待被委托 job 收尾：典型情形前端 job 实测 ~154s（Phase 0），轮询 30s 间隔下 gate 大约等 1–2.5 分钟；极端情形轮询窗口 10 分钟封顶（gate 红）。gate job 自身超时从 10 分钟上调到 15 分钟以容纳窗口与下载余量。这是 ADR 0014 失败子句成立的直接代价：gate 的绿从此依赖「被委托 job 在同一 head 上真实成功」，而不是「它被声明要跑」。

## 6. pending-orchestrator（NOT RUN，解除条件）

1. M19：碰前端的 PR 上令 `frontend.yml` verify 失败（临时分支）→ 观察 `LingDoc PR gate` 红且失败信息点名 `Test, type-check, and build (conclusion=failure)`。解除条件：编排者 push 临时分支并采集 gate run 日志。
2. 真实 runner 上轮询行为观察（in_progress → 等待收尾的真实等待时长），口径同 Phase 0。解除条件：下一次碰前端的真实 PR run 完成。

## 7. 遗留风险

（绿后补齐）
