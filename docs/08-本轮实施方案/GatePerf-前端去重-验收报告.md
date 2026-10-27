# GatePerf-前端去重：验收报告（票 #723）

- 关联：Spec #720（PR gate 耗时优化）／治理票 #719／本票 #723；前置：#721（Phase 0 实测）、#722（ADR 0014）。
- 分支：`ticket-723/frontend-delegation`（工作树 `D:\wt\gp-723-frontend`，起点 `af6d07bf`）。
- 语义依据：[ADR 0014](../adr/0014-gate-evidence-sharding-frontend-delegation-red-lines.md) 第三节（前端委托：字段保留、值=委托声明+被委托 job 名；「委托 ≠ 跳过」；require-coverage 不弱化；工作流不新增路径知识）。
- Phase 0 校准：前端步实测约 154s/轮，本票 wall 收益上限约 2.5 分钟；消除双份命令（漂移温床）的理由独立成立。

## 0. 施工并行面核对

`scripts/lingdoc_parallel_surfaces.py`（13 面）登记的是应用代码面；`scripts/ci/lingdoc_gate_*.py`、`.github/workflows/lingdoc-review.yml`、`GLOSSARY.md` 门禁判定节均无面条目（2026-10-26 以 loader 数据核实：按 `lingdoc_gate`/`lingdoc-review`/`GLOSSARY` 过滤为空）。Spec #720 子票间的文件边界以票面为准：本票不碰 provider 侧（#724）、不碰 cancel-in-progress（#550）与 race/缓存语义。

## 1. 验收矩阵（开工前立，证据随行补齐）

判定状态口径：`PASS` / `FAIL` / `NOT RUN` / `DEFERRED_MANUAL`（`docs/agents/development-rules.md`「门禁状态和例外语义」）。

| # | 矩阵行 | 证据（命令 / seam） | 状态 |
|---|---|---|---|
| M1 | 红：只碰前端的合成贡献面 → 判定选中前端工作流 verify job；coverage 的 verified_by 指向前端工作流 job（不含已删除的 gate 内步骤名）；gate 输出含委托语义 | `scripts/ci/test_lingdoc_gate_decision.py::FrontendDelegationTests`（判定模块公开接口 seam，合成 git 仓库） | PASS（本地） |
| M2 | 红：抽掉前端面的运行证据 → gate 红（require-coverage 不弱化，#602） | `FrontendDelegationTests::test_removing_the_delegated_run_leaves_the_frontend_uncovered`（补丁清单 + `publish(require_coverage=True)` 抛 SystemExit） | PASS（本地） |
| M3 | gate 工作流删除前端复跑：gate 内无 setup-node、无 npm 三命令、不读 `run_frontend`；汇总表仍五键、frontend 值=委托声明 | `scripts/ci/test_lingdoc_workflow_wiring.py::GateStepWiringTests`（扩展）；PyYAML 解析 `.github/workflows/lingdoc-review.yml` | PASS（本地） |
| M4 | 接线守护：gate 只读它声明消费的输出；判定模块不再发布无消费者的 `run_frontend` | `test_lingdoc_gate_contracts.py::WiringTriadTests`（真实 YAML × 真实模块双向） | PASS（本地） |
| M5 | 「委托 ≠ 跳过」措辞：summary 文案与 boundary 说明区分两者；NOT RUN 不被写成委托 | `GateStepWiringTests::test_the_gate_publishes_the_delegated_frontend_verdict` + `FrontendDelegationTests` 的 not-run 断言 | PASS（本地） |
| M6 | 概念登记：「委托」进 `lingdoc_gate_concepts.py` 与 `GLOSSARY.md` 门禁判定节（定义 + _避免_ 行）；「分片」不动（留 #724） | `test_lingdoc_gate_contracts.py::DocumentationContractTests`（双向契约） | PASS（本地） |
| M7 | 工作流 YAML 零新增路径知识 | 既有 `test_no_workflow_carries_a_path_table_any_more`（wiring + surfaces 两份）+ M3 的人工复核 | PASS（本地） |
| M8 | 门禁 job 永远存在；cancel-in-progress 与 race 语义不动 | 既有 `test_lingdoc_gate_workflow_triggers.py`；diff 复核（并发段与 provider 步零改动） | PASS（本地） |
| M9 | 全套守护测试全绿（基线 898 项） | `python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'` | PASS（本地，906 项） |
| M10 | 真实 PR 验证：碰前端 / 不碰前端两种贡献面的 gate 结论与耗时对比（与 Phase 0 同口径） | 由编排者在 push 后采集 | NOT RUN（pending-orchestrator） |

## 2. 红绿样本对照

提交序列：`2830f020`（docs：矩阵先行）→ `65ae068f`（test：红样本，10 处失败）→ `cfe15867`（feat：转绿）。

| 样本 | 红（`65ae068f` 时点） | 绿（`cfe15867` 时点） |
|---|---|---|
| 只碰 `frontend/**` 的 PR | coverage 的 verified_by 含 gate 内步骤名 `Frontend: test, type-check and build`；outputs.frontend=`true`（布尔，无归属） | verified_by=`Test, type-check, and build`（前端工作流 job，去重后不再重复计名）；outputs.frontend=`delegated to frontend.yml#verify (Test, type-check, and build)` |
| 只碰 Go 的 PR | outputs.frontend=`false` | outputs.frontend=`not run: frontend verification is not selected on this head` |
| 手动全量（workflow_dispatch） | outputs.frontend=`true`，gate 自己跑前端三命令 | outputs.frontend=`not run: frontend verification is selected on this head, but this event does not start frontend.yml#verify (…)`（不虚指委托） |
| 前端面运行证据被抽掉（补丁清单：两个前端面停看 `frontend/`，provider 面扩看） | 无人验证前端仍可绿（未钉住） | coverage `not verified` → `uncovered` → scope 步失败（gate 红，`publish(require_coverage=True)` 抛 SystemExit） |
| 接线：gate 工作流 | 存在 `run_frontend` 条件步与 `steps.frontend.outcome` 读取 | 零 `run_frontend` 引用、零 `setup-node`、零 npm 命令；FRONTEND 汇总值读 `steps.scope.outputs.frontend` |

红样本落点：`scripts/ci/test_lingdoc_gate_decision.py::FrontendDelegationTests`（6 用例，红时 4 失败 2 护栏通过）与 `test_lingdoc_workflow_wiring.py::GateStepWiringTests`（3 用例，红时 2 失败）；金样本（GOLDEN / GATE_GOLDEN / push-governance）随语义更新，红时一并失败（4 处），合计红态 10 失败。

## 3. 验证命令与结果

- 全套守护（实现后 head）：`python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'` → `Ran 906 tests in 142.8s` `OK`（基线 898 全绿；新增 6 个委托判定用例 + 接线用例 1 拆 3，净增 8）。
- YAML 自检：PyYAML `safe_load` 解析 `lingdoc-review.yml`（jobs=gate）与 `frontend.yml`（jobs=scope/verify/docker-image）；逐 step 断言 gate 内无 `npm`/`setup-node`/`run_frontend` → PASS。
- 清单校验：`python scripts/ci/lingdoc_gate_surfaces.py` → `13 gate surfaces validated`。
- CLI 冒烟（离线入口）：head 上 no-op PR 范围 → frontend 判定 `not run: …not selected…`、`run_frontend` 键不存在；`--all --require-coverage`（dispatch 路径）→ exit 0、frontend 判定为不虚指的 not run、coverage 空。
- `git merge spec-720/gate-perf` → `Already up to date`（`af6d07bf` 已含 `068193e6`）。

## 4. pending-orchestrator（NOT RUN，解除条件）

1. M10 真实 PR 双贡献面对比：push 后由编排者采集两轮 gate run（碰前端 / 不碰前端）的结论与分步耗时，口径同 Phase 0 报告。解除条件：PR 打开且两轮 run 完成。
2. 分支保护对 `LingDoc PR gate` 的必需检查呈现：合并授权侧确认 gate 结论页 frontend 行显示委托语义。解除条件：PR 页面可读。

## 5. 遗留风险

- **委托的跨工作流证据依赖**（ADR 0014 已登记）：PR 事件上前端工作流裸 `pull_request:` 触发器保证被委托 job 与 gate 同 head 启动；手动 dispatch 不启动前端工作流，gate 如实报 not run（不虚指委托）。push 事件读不到该输出（gate 无 push 触发器）。
- `decision.jobs` 中 `frontend.yml#verify` 现有两行（`frontend` 面 + `pr-gate-frontend` 面，同一输出键 OR 合并）；`report()` 的 jobs 列表相应多一行，属预期。
- summary 表 frontend 值从枚举（success/skipped/…）变为声明语句：消费 `lingdoc-checks-*/result.json` 的下游若假设旧枚举需跟进（仓内无此类消费者；artifact 保留 14 天仅供人读）。
