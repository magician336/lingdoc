# GatePerf-委托失败核验：验收报告（票 #734）

- 关联：Spec #720（PR gate 耗时优化）／治理票 #719／本票 #734；前置：#721（Phase 0 实测）、#722（ADR 0014）、#723（前端委托）、#724（provider 分片）。
- 分支：`ticket-734/delegation-verify`（工作树 `D:\wt\gp-df`，起点 `db45c9ff`——含 #723 委托与 #724 分片的全部落地）。
- 提交序列：`895ec147`（docs：矩阵先行）→ `80c0333e`（test：红态 16 失败）→ `32268065`（feat：转绿，961 项全绿）→ `81327555`（merge origin/main，967 项全绿）→ 本报告（docs）。
- 语义依据：[ADR 0014](../adr/0014-gate-evidence-sharding-frontend-delegation-red-lines.md) 第三节的失败子句：「前端工作流在这个 head 上没有真实运行时（触发、排队、失败都算没有），require-coverage 必须红」。本票把这句从静态判定兑现成机制：gate 聚合（publish 步）在结论发布前查询 head SHA 的 check-runs，对被委托 job 的真实结果核验。
- 来源：#728 code-review Spec 轴 (c)——委托的「失败也算没有」无实现缝。

## 0. 施工并行面核对

`scripts/lingdoc_parallel_surfaces.py`（13 面）按 loader 数据核实（2026-10-26）：全部 `done`，无 `claimed`/`ready` 面占用 `.github/workflows/lingdoc-review.yml`、`scripts/ci/lingdoc_gate_decision.py`、`scripts/ci/test_lingdoc_*.py`。本票与任何在施票无文件交叠。

## 1. 验收矩阵（开工前立，证据随行补齐）

判定状态口径：`PASS` / `FAIL` / `NOT RUN` / `DEFERRED_MANUAL`（`docs/agents/development-rules.md`「门禁状态和例外语义」）。证据均在 head `81327555`（merge origin/main 后）采集，除注明时点者外。

| # | 矩阵行 | 证据（命令 / seam） | 状态 |
|---|---|---|---|
| M1 | 绿态（对照）：委托成立 + 被委托 job check-run `conclusion=success` → gate 绿，`result.json` 记 `frontend_delegation.outcome=verified`；summary 记委托核验行；查询 URL 命中 `GET {api}/repos/{repo}/commits/{head}/check-runs`（per_page=100, page=1） | `DelegationVerificationTests::test_a_successful_delegated_job_keeps_the_gate_green`（exec 真 heredoc × 本地 stub，断言精确请求路径与 query） | PASS |
| M2 | 红：`conclusion=failure` → gate 红，点名 job 与最新 conclusion（cancelled 旧 run 不顶替 failure 说话） | `…::test_a_failed_delegated_job_fails_the_gate_and_names_it`（双 run：cancelled id=1 + failure id=2 → 报 failure 不报 cancelled） | PASS |
| M3 | 红：check-run 不存在且轮询窗口耗尽 → gate 红，点名 job 与「no check run + 窗口耗尽」，且确证轮询（≥3 次请求） | `…::test_an_absent_delegated_job_exhausts_the_window_and_fails` | PASS |
| M4 | 红：`status=in_progress` 轮询至超时 → gate 红，点名 job 与 `status=in_progress; polling window exhausted` | `…::test_an_in_progress_job_polled_past_the_window_fails` | PASS |
| M5 | 绿：`in_progress` 在窗口内完成 → 轮询生效转绿（非一锤子判定） | `…::test_an_in_progress_job_that_finishes_in_time_is_accepted` | PASS |
| M6 | 红：API 500 → fail-closed，gate 红 + 原因点名（HTTP 500 / query failed），零重试（恰 1 次请求），result.json 记 `query-failed` | `…::test_an_api_failure_fails_closed_and_names_the_reason` | PASS |
| M7 | 归属精确性：`frontend_delegate` 为空（未碰前端 / 手动 dispatch）→ 零查询（stub 零请求、gate 绿）；decision 侧委托/未选/手动三态键值 | `…::test_no_delegation_claim_means_zero_queries` + `FrontendDelegationTests` 三新用例 | PASS |
| M8 | 分页翻全：目标在第二页（首页 100 条填充）→ 仍找到并绿，页序 `[1, 2]` | `…::test_the_lookup_pages_through_all_check_runs` | PASS |
| M9 | 同 head 多 run 不误判：旧 run `cancelled` + 新 run 进行中 → 判 pending 不判失败；新 run success → 绿 | `…::test_a_cancelled_older_run_does_not_fail_a_live_rerun` | PASS |
| M10 | 判定模块输出 `frontend_delegate`：委托成立=`Test, type-check, and build`；非前端/手动=空串 | `test_lingdoc_gate_decision.py::FrontendDelegationTests` 三用例（红态 3 errors → 绿） | PASS |
| M11 | 权限最小化：`permissions` 恰为 `{contents: read, checks: read}`；查询持 `${{ github.token }}`（权限即工作流声明本身） | `…::test_the_workflow_permissions_stay_minimal` + `…::test_the_publish_step_authenticates_with_the_workflow_token`（PyYAML 精确相等断言） | PASS |
| M12 | 接线：prepare 声明 `frontend_delegate`；gate env 读 `FRONTEND_DELEGATE` 与 `HEAD_SHA`（`github.event.pull_request.head.sha`）；无未声明读取 | `…::test_the_prepare_job_declares_the_delegate_and_the_gate_reads_it` + `WiringTriadTests`（真实 YAML × 真实模块双向） | PASS |
| M13 | 轮询参数 env 可调、默认 30s / 600s（对齐前端 job 实测 154s + 余量）；gate job 超时 15 ≥ 窗口余量 | `…::test_the_polling_knobs_are_env_tunable_with_defaults` + `…::test_the_gate_job_outlives_the_polling_window` | PASS |
| M14 | 被委托显示名跨工作流唯一（check-run 名过滤无歧义前提） | `…::test_the_delegated_check_name_is_unique_across_the_workflows`（红态即绿：现况已唯一，守护防漂移） | PASS |
| M15 | 红线一/二/三不动：cancel-in-progress 不动、race/`-count=1` 不动、require-coverage 不弱化（本票强化它：静态选中之外还要真实 success） | 全套绿含既有 `test_lingdoc_gate_workflow_triggers.py`、`test_every_shard_keeps_race_and_fresh_counts`、`test_removing_the_delegated_run_leaves_the_frontend_uncovered`；diff 复核并发段与 provider 步零改动 | PASS |
| M16 | 红线四：工作流零新增路径知识（本票只加 job 显示名过滤与轮询参数，无包名/目录/分片名） | 既有 `test_the_workflow_carries_no_package_names` 全套绿内复核；新 heredoc 无任何路径字面量 | PASS |
| M17 | 概念登记：`frontend_delegate` 归入「委托」概念 keys；GLOSSARY 委托条目补核验句；ADR 0014 §三补核验机制条目 | `DocumentationContractTests` 双向契约（全套绿内） | PASS |
| M18 | 全套守护测试全绿（基线 943 项）+ PyYAML 自检 | 见 §4：红态 16 失败 → 绿 961 → merge 后 967 全 OK；PyYAML 解析两份 YAML | PASS |
| M19 | 真实 PR 验证：前端 verify 失败 → gate 红且点名 | 验证 PR #741（run 40480884603，head 32ca4330）| PASS：gate `error: the delegated frontend verification did not pass on this head: Test, type-check, and build (conclusion=failure)`，decide 同 head 绿（命名/空白符修正后）；对照路径（verify 成功→委托绿）见 #731 与 40457683775 批次 |

## 2. 红绿样本对照

红态时点 `80c0333e`（仅测试）；绿态时点 `32268065`。

| 样本（stub 响应 / 贡献面） | 红（`80c0333e`） | 绿（`32268065`） |
|---|---|---|
| 被委托 job `conclusion=success` | heredoc 不核验 → gate 绿但 result.json 无 `frontend_delegation`、无查询 | gate 绿；恰一次 `GET …/commits/{head}/check-runs?per_page=100&page=1`；result.json 记 `verified/conclusion=success`；summary 记 `frontend delegation` 行 |
| `conclusion=failure`（另有 cancelled 旧 run） | gate 绿（假绿——本票要消灭的缺陷本体） | gate 红 exit 1；`error: the delegated frontend verification did not pass on this head: Test, type-check, and build (conclusion=failure)` |
| check-run 不存在，窗口 0.4s（夹具缩放） | gate 绿 | 9 次轮询后红：`no check run on this head yet; polling window exhausted (0.4s)` |
| `status=in_progress` 恒定，窗口 0.4s | gate 绿 | 红：`status=in_progress; polling window exhausted (0.4s)` |
| `in_progress` → 第 2 轮 `success` | gate 绿（未核验） | 轮询生效，绿 + `verified` |
| API 500 | gate 绿（静默放行——fail-open 缺陷） | 红 + `query failed: HTTP Error 500 …`；恰 1 次请求；result.json 记 `query-failed` |
| 只碰 Go / 手动 dispatch | `frontend_delegate` 键不存在（3 errors） | 键=空串，stub 零请求，gate 绿 |
| 碰前端 | 键不存在 | 键=`Test, type-check, and build`（manifest 登记的被委托 job 名） |
| 工作流接线 | permissions 无 checks:read、prepare 无声明、gate env 无读取、无轮询旋钮 | 全部就位；`WiringTriadTests` 双向一致 |

红态合计 16 失败：wiring 13 failures + decision 3 errors（`test_no_delegation_claim_means_zero_queries` 与 `test_the_delegated_check_name_is_unique_across_the_workflows` 为防漂移守卫，红态即绿，属预期）。

## 3. ADR 0014 §三逐句对照

| §三原句 | 本票实现 / 不变声明 | 状态 |
|---|---|---|
| 「gate 不再复跑前端三命令。前端面的验证证据完全委托前端工作流 `.github/workflows/frontend.yml` 的 `verify` job」 | 不变（#723 落地）。核验以该委托声明为前提：判定模块 `frontend_delegate` 恰在委托成立时非空，heredoc 仅在非空时查询——「gate 复跑」没有借核验之名回来 | 不变，PASS |
| 「gate 结果产物的 `frontend` 字段保留……语义从『本轮执行结果』改为『委托声明 + 被委托 job 名』」 | 字段名与位置不动；本票新增 `result.json.frontend_delegation` 记录被委托 job 的**真实**核验结果，summary 表增一行，`frontend` 行措辞原样 | 扩展，PASS |
| 「『委托』与『跳过 = NOT RUN』是两个词，不得混用……把真实存在的证据说成没有，与把没跑的说成跑过，是同一种假绿」 | 「Skipped means NOT RUN」「Delegated is not skipped」boundary 文案一字未改；核验失败是「委托的红」（点名 job 与状态），不被改写成 skipped；查询失败也点名原因，不是「没有证据」的静默 | 不变，PASS |
| 「同 head 证据原则对委托同样适用：被指认的证据必须来自同一个 head 上真实运行的前端工作流 job。**前端工作流在这个 head 上没有真实运行时（触发、排队、失败都算没有），require-coverage 必须红**——不得把别的 head 的绿灯记到这个 head 名下」 | 本票核心。查询按 PR head SHA（`HEAD_SHA: ${{ github.event.pull_request.head.sha }}`；实证：PR #731 head `7c763c59be` 的 check-runs 含被委托 job，merge 提交 `db45c9ff` 侧则只有 push 类 run——查错 SHA 即假红/假绿）。触发（不存在→窗口耗尽红）、排队（in_progress→轮询，超窗红）、失败（completed 非 success→立即红）都算没有；唯一绿是同 head 上真实 `conclusion=success` | 兑现，PASS |
| 「『门禁始终出现』不受影响：gate job 永远创建」 | gate job `if: always()` 未动；`test_the_gate_job_aggregates_the_shards` 全套绿 | 不变，PASS |
| 「接线纪律照旧：跨 job 引用的输出必须在 job 的 `outputs:` 里显式声明；接线守护测试扩展既有断言，不开新缝」 | `frontend_delegate` 经 prepare `outputs:` 显式声明 → gate env 显式读取；`WiringTriadTests`/`OutputsWhitelistTests` 原样钉住；接线测试扩展于既有 `test_lingdoc_workflow_wiring.py`，无新缝 | 遵守，PASS |

四条红线复核：cancel-in-progress 未动（红线一）；`go test -race -count=1` 未动（红线二）；require-coverage 未弱化——本票在其上叠加真实结果核验，属强化（红线三）；工作流新 heredoc 无包名/目录/分片名，被过滤的 job 显示名来自判定模块输出（红线四）。

## 4. 验证命令与结果

- 基线（开工前 `db45c9ff`）：`python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'` → `Ran 943 tests in 167.894s` `OK`。
- 红态（`80c0333e`）：同命令 → wiring `Ran 41 tests, FAILED (failures=13)`；decision `Ran 109 tests, FAILED (errors=3)`。
- 绿态（`32268065`）：全套 → `Ran 961 tests in 165.965s` `OK`（基线 943 + 18：`DelegationVerificationTests` 15 + `FrontendDelegationTests` 3）。
- merge origin/main 后（`81327555`）：`git fetch origin main && git merge origin/main`（`spec-720/gate-perf` 已删，按票面指示改并 main）→ 全套 `Ran 967 tests in 171.946s` `OK`（main 新增 6 项）。
- PyYAML 自检：`safe_load` 解析 `lingdoc-review.yml`（permissions=`{'contents': 'read', 'checks': 'read'}`；gate timeout=15；env 含 FRONTEND_DELEGATE/HEAD_SHA；prepare outputs 含 frontend_delegate）与 `frontend.yml` → PASS。
- 聚合核验真执行：`DelegationVerificationTests` 提取 publish 步真 heredoc `exec` 执行 × 本地 `CheckRunsStub`（127.0.0.1 ThreadingHTTPServer，零网络），五态+分页+轮询+fail-closed 全部走真实 HTTP 请求路径 → OK。
- SHA 归属实证（真实仓库）：`gh api repos/magician336/lingdoc/commits/7c763c59be…/check-runs` → 25 个 run 全部 `head_sha=7c763c59be`（PR head），含 `Test, type-check, and build (success)`；`…/commits/db45c9ff/check-runs` → 仅 7 个 push 类 run。故 `HEAD_SHA` 必须取 `github.event.pull_request.head.sha`，不能取 `GITHUB_SHA`（merge 提交）。

## 5. gate wall 影响（已接受的正确性代价）

前端类 PR 的 gate publish 步将等待被委托 job 收尾：典型情形前端 job 实测 ~154s（Phase 0），30s 轮询间隔下 gate 大约等 0–2.5 分钟（gate 的 prepare+providers 本身就要数分钟，多数情形轮询第一拍即 success）；极端情形轮询窗口 10 分钟封顶（gate 红）。gate job 自身超时从 10 分钟上调到 15 分钟以容纳窗口与下载余量。这是 ADR 0014 失败子句成立的直接代价：gate 的绿从此依赖「被委托 job 在同一 head 上真实成功」，而不是「它被声明要跑」。

**current-head 注记（评审 F2，2026-10-27）**：本报告部分证据行最初钉在 `81327555`；最终头为 `4875bec2`（评审修复 F3/F4/F5 + 本 M19 回填），修复后定向套件（decision+wiring）与全量 967 项在当前 head 复跑全绿。首次 dispatch 红/绿两 run 与 M12 数据不受影响。

## 6. pending-orchestrator（NOT RUN，解除条件）

1. **M19 真实 PR 验证**：碰前端的 PR 上令 `frontend.yml` verify 失败（临时分支）→ 观察 `LingDoc PR gate` 红且失败信息点名 `Test, type-check, and build (conclusion=failure)`；对照真实 success PR 的 summary `frontend delegation` 行显示 `verified (conclusion=success)`。解除条件：编排者 push 两条分支并采集两轮 gate run 日志。
2. **真实 runner 轮询观察**：下一次碰前端的真实 PR 采集 publish 步等待时长（预期 0–2.5 分钟），核对 §5 估计。解除条件：该 run 完成。

## 7. 遗留风险

- **cancelled-only 瞬态**：重推使旧 run 取消、新 run 的 check run 尚未登记时，轮询若恰见「只有 cancelled」会立即红（点名 `conclusion=cancelled`）。方向 fail-closed 正确，但属罕见误报窗口——真实时序下 gate publish 晚于触发事件数分钟（prepare 守护套件 ~170s + providers），新 run 的 check run 早已登记；此窗口实际不可达。误报时人工重跑 gate 即恢复。
- **API 抖动零重试**：GitHub API 5xx/网络错误立即红（fail-closed 既定语义，点名 HTTP 状态），该次 gate 需人工重跑。未做带退避的静默重试，因为重试会推迟真实失败信号并可能掩盖权限/配置错误（403/404 重试十分钟不如立刻点名）。
- **gate wall**：见 §5，前端类 PR 的 gate 发布被推后，为已接受代价。
- **fork PR**：`pull_request` 事件的 run 在 base 仓库上下文，`github.token` 带 `checks: read` 可读本仓库 check-runs；fork 分支本身的 check runs 不在查询范围（被委托 job 必须真实跑在本仓库前端工作流上，语义正确）。
- **消费 result.json 的下游**：新增 `frontend_delegation` 键为纯增量；`frontend` 行仍是委托声明语句，旧消费者不受影响（仓内无此类程序化消费者，artifact 仅供人读）。
