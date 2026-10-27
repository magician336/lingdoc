# Spec #627 门禁见证复用——验收矩阵

> 本表按《[验收判定口径](验收判定口径.md)》**v1.1** 判定；口径 commit = `1f195def6fb984d43876567bfe365222c34b46b5`。四值词表：`PASS` / `FAIL` / `NOT RUN` / `DEFERRED_MANUAL`；本表未出现 `FAIL` 与 `DEFERRED_MANUAL` 条目。
>
> **审计基线 HEAD = `8ad31b5f`**（集成分支 `spec/627-gate-attest-reuse` 顶端，提交链自底向上：`a92ec64d` T2 → `8c80d4ea` T1 → `99b05e49` T3 → `9745ee29` 落基 origin/main `6b394ca7` → `8ad31b5f` review fixes F1–F5）。本表随 T4 收口提交（纯 `docs/` 增量）落在该基线之上，产品与测试代码逐字节未变。
>
> **证据采集环境**：机器可判定层为 Windows 宿主 Python 3.14.3 本机实跑（2026-10-25 在 `8ad31b5f` 上重跑：全量 `python -m unittest discover -s scripts/ci -p "test_lingdoc_*.py"` → **Ran 791 tests OK**）；运行级证据（真实复用、真实回退、见证故障、透传可审计、其他触发点实跑）只能在 GitHub Actions 的真实 PR 门禁 run 上产生，本机无法产生，**属环境限制，如实标 `NOT RUN` 并逐条写解除条件**（口径 §5）。
>
> **2026-10-27 运行级回填**：Spec PR 自身的门禁 run 已产生全部五类运行级记录——复用 [40459253766](https://github.com/magician336/lingdoc/actions/runs/40459253766)（复用 [40457662987](https://github.com/magician336/lingdoc/actions/runs/40457662987)，2m20s vs 全量 15m16s）、真实回退 [40451804278](https://github.com/magician336/lingdoc/actions/runs/40451804278) 与 [40456413700](https://github.com/magician336/lingdoc/actions/runs/40456413700)（后者兼为真实故障回退：runner 内 gh 缺 GH_TOKEN 的 API 故障被机制按设计消化）、透传 artifact（id 12712759696，含 `reused-scope.json`/`reused-gate-anchor.json`）、synchronize 全量重跑（40454766551/40457662987）。其间发现并修复两处只在真实 runner 上暴露的接线缺陷：触发判定读 payload 的 action 字段（提交 `42f95b12`）、见证步骤注入 GH_TOKEN（提交 `0cd2c1bf`）。运行级行已回填 PASS；**唯一残留**：fork PR 跨 run artifact 可见性未实证（无 fork 场景）。
>
> **验收行粒度**：按 T4（#630）票面指令，验收行 = 四票（#628/#629/#631/#630）验收清单逐条 + Spec #627 `Implementation Decisions` 约束核对。US-01…US-14 的逐条映射属口径 §11「判定器施工」的范围，不在本票边界。`IMPL` 约束行**不计入点数**（口径 §1.2）。机器可判定层证据的类级明细见协调者证据摘要（`spec/627-gate-attest-reuse` @ `8ad31b5f`，已随票面留痕）；本表每行给出可复现命令，均可在当前 head 直接复制运行。
>
> **登记/文档状态**：登记处（`scripts/lingdoc_parallel_surfaces.py`）核对结论见 T4-AC3 行；workflow 头注释与三个脚本 docstring 的逐字核对结论见 T4-AC1 行。

## 一、机器可判定层（PASS）

### T1 #628 锚点落账

| 定位坐标 | 验收点原文（逐字引自票面） | 判定状态 | 交付分类 | 证据 | 备注 / 解除条件 |
| --- | --- | --- | --- | --- | --- |
| T1-AC1 | 「在真实 PR 上触发一次完整门禁 run，其 artifact 中可读到四元组，且各字段与该 run 的实际 head/main/run-id/结论一致」 | **PASS** | 已交付（运行级解除 2026-10-27） | 运行级解除（2026-10-27）：复用 run [40459253766](https://github.com/magician336/lingdoc/actions/runs/40459253766) 的 artifact（id 12712759696）内 `gate-anchor.json` 逐字段复核——head_sha=`0cd2c1bf…`（PR head，非 merge commit）、main_sha=`f55080b6…`、`main_sha_source=merge_ref_parent`、`main_sha_read_at=2026-10-27T06:10:02Z`、run_id=40459253766、pr=641、conclusion=success，与 run 实际值一致。 机器层承前： 字段语义机器层已证：`scripts/ci/test_lingdoc_gate_decision.py` `GateAnchorTests` 15 条（head_sha 取 PR head 非 `GITHUB_SHA` merge commit、main tip 取 merge-ref 首父、run_id 为 int、写失败不改结论）；命令（scripts/ci 下）：`python -m unittest test_lingdoc_gate_decision` → 102 OK；head `8ad31b5f` | 已解除（2026-10-27）；原解除条件：Spec PR 自身的 ready_for_review 门禁 run 产出 artifact 后，读取 `gate-anchor.json` 逐字段复核（与运行级第 1/2 项同一载体，见 T3-AC1） |
| T1-AC2 | 「main tip 在 run 启动时刻读取（锚定 merge ref）， artifact 中留有可复核的时间与值」 | **PASS** | 已交付（运行级解除 2026-10-27） | 运行级解除（2026-10-27）：同一 `gate-anchor.json` 带 `main_sha_source` 与 `main_sha_read_at`（06:10:02Z，witness 判定前读取），值取 merge-ref 首父，时间与值均可复核。 机器层承前： 实现含 `main_sha_source` / `main_sha_read_at` 字段与 `_main_sha` 三级回退链（merge_ref_parent → checkout_head → origin_main_unverified），单测覆盖（同上命令）；head `8ad31b5f` | 已解除（2026-10-27）；原解除条件：同 T1-AC1——真实门禁 run 的 `gate-anchor.json` 中复核时间与值 |
| T1-AC3 | 「锚点写入失败不改变门禁既有结论语义（写入是附带产物，不引入新的失败路径…）」 | PASS | 已交付 | `test_lingdoc_gate_decision.py` `GateAnchorTests` 写失败不改结论用例；命令同上 → 102 OK；head `8ad31b5f` | 实现选择即票面首选项：写入失败只 warning，不改判定 |
| T1-AC4 | 「现有门禁测试面（ciscripts）全绿；本票不触碰 ready_for_review 路径行为」 | PASS | 已交付 | 全量 `python -m unittest discover -s scripts/ci -p "test_lingdoc_*.py"` → Ran 791 OK；`test_lingdoc_workflow_wiring.py` 21 OK 断言 attest 判定不改变 `opened`/`synchronize`/`reopened` 的完整重跑语义；head `8ad31b5f` | ready_for_review 的新行为由 T3（#631）引入，由 T3 行单独验收 |

### T2 #629 复用判定模块（纯函数决策接缝）

| 定位坐标 | 验收点原文（逐字引自票面） | 判定状态 | 交付分类 | 证据 | 备注 / 解除条件 |
| --- | --- | --- | --- | --- | --- |
| T2-AC1 | 「五条件各一条通过用例 + 各一条违反回退用例，全部通过」 | PASS | 已交付 | `scripts/ci/test_lingdoc_gate_attest_reuse.py` 29 OK：五条件正/反各一（`ConditionViolationTests` 6 条反例）+ 条件 5 同接受 workflow 名与 manifest 派生路径；命令（scripts/ci 下）：`python -m unittest test_lingdoc_gate_attest_reuse`；head `8ad31b5f` | — |
| T2-AC2 | 「见证层故障用例（记录缺失、字段畸形、结论非 success、run 未完成、来源 workflow 不符）全部回退，无一例误判 reuse」 | PASS | 已交付 | 同上文件 `WitnessFailureTests`（11+ 形态全回退）+ 锚点与 run 记录自相矛盾的交叉核验回退（witness 模块测试）；命令同上 → 29 OK；head `8ad31b5f` | — |
| T2-AC3 | 「与排队判定（queue-state 三形态）的同形兼容断言：reuse 输出的结论形状与完整 run 的绿结论在机器视角一致」 | PASS | 已交付 | 同形 guard test 与真实 `judge()` 逐键比对（`lingdoc_gate_attest_reuse.py` docstring「The queue-state shape guarantee」节 + 测试断言 `verdict/passed/missing/observed_status/observed_conclusion` 同形）；命令同上；head `8ad31b5f` | — |
| T2-AC4 | 「模块在本仓库门禁的 ciscripts 测试面全绿；不引入对 GitHub API 的运行时依赖」 | PASS | 已交付 | 决策模块为纯函数（零 import 网络面，docstring「This module fetches nothing and writes nothing」）；全量 791 OK；head `8ad31b5f` | — |

### T3 #631 接线 ready_for_review 见证复用路径

| 定位坐标 | 验收点原文（逐字引自票面） | 判定状态 | 交付分类 | 证据 | 备注 / 解除条件 |
| --- | --- | --- | --- | --- | --- |
| T3-AC1 | 「真实 PR 上留证「真实复用发生」：Draft 阶段门禁绿 → 转 Ready → 门禁 check 约一分钟内绿，summary/artifact 含 REUSED run 链接与五条件比对记录」 | **PASS** | 已交付（运行级解除 2026-10-27） | 运行级解除（2026-10-27）：head `0cd2c1bf` 全量绿 run [40457662987](https://github.com/magician336/lingdoc/actions/runs/40457662987)（15m16s）之后 Draft→Ready 触发 run [40459253766](https://github.com/magician336/lingdoc/actions/runs/40459253766)：job 仅 **2m20s**（setup/checkout 为主，判定本身秒级），重型步骤全部跳过；artifact `gate-attest-decision.json` 记 `reuse:true`、`reused_run_id:40457662987`、五条件比对全 true。 机器层承前： 机器层全链已单测：witness 27 OK（happy path、四事件边界零 API）+ wiring 7 条 attest 条件断言；运行级记录不存在 | 已解除（2026-10-27）；原解除条件：Spec PR 自身转 Ready 的 ready_for_review 门禁 run（summary/artifact 留 REUSED 链接与五条件比对、`gate-attest-decision.json` 落盘）——运行级第 1 项 |
| T3-AC2 | 「真实 PR 上留证「真实回退发生」：构造任一条件不满足…门禁完整重跑且结论语义与现状一致」 | **PASS** | 已交付（运行级解除 2026-10-27） | 运行级解除（2026-10-27，两次真实回退）：run [40451804278](https://github.com/magician336/lingdoc/actions/runs/40451804278)（触发事件不带资格，当时的触发判定缺陷）与 run [40456413700](https://github.com/magician336/lingdoc/actions/runs/40456413700)（API 失败回退，见 T3-AC3）均判 fallback、完整重跑、门禁绿；fallback 原因逐字落在各自 `gate-attest-decision.json` 的 `reason`。 机器层承前： 条件违反回退机器层已证（T2-AC1）；运行级记录不存在 | 已解除（2026-10-27）；原解除条件：转 Ready 前 push 新提交，或复用首个无锚点旧绿 run 的实跑——运行级第 2 项 |
| T3-AC3 | 「真实 PR 上留证「见证故障回退」：见证判定无法取到候选记录时（可用临时注入）回退完整重跑，门禁不变红不卡死」 | **PASS** | 已交付（运行级解除 2026-10-27） | 运行级解除（2026-10-27，真实故障而非注入）：run [40456413700](https://github.com/magician336/lingdoc/actions/runs/40456413700) 的见证遇真实 API 故障（runner 内 gh 缺 GH_TOKEN），按设计回退完整重跑、门禁不变红不卡死、`reason` 留痕；故障随后由提交 `0cd2c1bf` 修复。生产环境真实故障被机制自身消化，强于注入实验。 机器层承前： 机器层等价保证已 PASS：witness 恒零退出 + `continue-on-error: true` + outputs 未设置时步骤条件判 false（wiring 断言 + witness 27 OK）；运行级注入实验未做 | 已解除（2026-10-27）；原解除条件：本机/CI 注入 API 故障实验或真实 API 故障留痕——运行级第 3 项 |
| T3-AC4 | 「复用路径透传被复用 run 的 scope 判定记录，require-coverage 的「skipped ≠ passed」语义可审计」 | **PASS** | 已交付（运行级解除 2026-10-27） | 运行级解除（2026-10-27，同仓部分）：复用 run 40459253766 的 artifact（id 12712759696）内 `reused-scope.json`（contracts/backend/frontend 全 true、diff_range、changed_paths 全清单）与 `reused-gate-anchor.json`（被复用 run 的锚点原样透传）落盘可审计。**残留**：fork PR 跨 run artifact 可见性未实证（无 fork 场景）。 机器层承前： 拷贝逻辑机器层已证（`_hand_on_scope_records` → `reused-scope.json` / `reused-gate-anchor.json`，witness 单测）；运行级确认与 fork PR 的跨 run artifact 可见性（`actions: read`）未实证 | 已解除（2026-10-27）；原解除条件：真实复用 run（T3-AC1 解除时）读取本次 artifact 中的 `reused-scope.json` 复核 + fork PR 场景实证——运行级第 4 项 |
| T3-AC5 | 「opened/synchronize/reopened 路径行为逐字不变；workflow_dispatch 全量逃生通道语义不变」 | **PASS** | 已交付（运行级解除 2026-10-27） | 运行级解除（2026-10-27，synchronize 实证）：push run [40454766551](https://github.com/magician336/lingdoc/actions/runs/40454766551) 与 [40457662987](https://github.com/magician336/lingdoc/actions/runs/40457662987) 坌完整重跑（synchronize 行为不变）；opened/reopened 与 workflow_dispatch 逃生通道以自然载体累积，机器层 wiring 断言已 PASS。 机器层承前： 机器层 wiring 断言已 PASS（重步骤条件逐字含 `steps.attest.outputs.reuse != 'true'`、`always()` 不跳过 publish、check 名 `LingDoc PR gate` 不变、`--all` 分支逐字保留；wiring 21 OK）；运行级实跑记录不存在 | 已解除（2026-10-27）；原解除条件：既有 PR 的日常 push run 即自然验证载体——运行级第 5 项 |
| T3-AC6 | 「既有 ciscripts 测试面全绿」 | PASS | 已交付 | 全量 791 OK（head `8ad31b5f`，2026-10-25 本机重跑） | — |

### Review fixes F1–F5（`8ad31b5f`，code-review 两轴发现的落实证据）

| 定位坐标 | 内容 | 判定状态 | 交付分类 | 证据 | 备注 |
| --- | --- | --- | --- | --- | --- |
| F1 | 见证步骤 outcome 进 result.json（损坏的见证留痕进结论，不只在步骤日志） | PASS | 已交付 | workflow publish 步骤 `result['attest_outcome']`；`test_lingdoc_workflow_wiring.py::test_the_publish_step_records_the_attest_outcome`；wiring 21 OK；head `8ad31b5f` | — |
| F2 | `GATE_ANCHOR_FILE` 转公共常量（跨模块读私有名不是接口） | PASS | 已交付 | `lingdoc_gate_decision.py` `GATE_ANCHOR_FILE = "gate-anchor.json"`；witness `ANCHOR_FILE = gate_decision.GATE_ANCHOR_FILE`；`test_lingdoc_gate_attest_witness.py` 断言同一常量；全量 791 OK | — |
| F3 | gate workflow 名从 manifest 派生（文件名不在第二处拼写） | PASS | 已交付 | `lingdoc_gate_attest_reuse.py` `GATE_WORKFLOW_PATH = next(... manifest.GOVERNANCE_FILES ...)`；`ManifestDerivationTests`（断言 manifest 只登记一个 governance workflow、`GATE_WORKFLOW_FILE` 不存在）；reuse 29 OK | — |
| F4 | 见证判定 JSON 落 artifact（`gate-attest-decision.json`，复用与回退都落） | PASS | 已交付 | witness `DECISION_FILE`；`DecisionRecordTests` 3 条；witness 27 OK | — |
| F5 | `_main_sha` 三级回退链（merge_ref_parent → checkout_head → origin_main_unverified，降级标注） | PASS | 已交付 | `lingdoc_gate_decision.py::_main_sha` + `GateAnchorTests` 回退链用例；decision 102 OK | — |

## 二、运行级证据（2026-10-27 全部解除；过程与 run 链接见第一节表头「运行级回填」注记）

运行级共 5 项（与 T3-AC1…AC5 一一对应）；T1-AC1/AC2 的 artifact 复核与 T4-AC2 的运行层补全随第 1–3 项同批解除。逐条登记于同目录 `T630-验证报告.json` 的 `not_run[]`（口径 §5.1）。

| 定位坐标 | 运行级验收项 | 判定状态 | 证据 | 解除条件（机器可读一行） |
| --- | --- | --- | --- | --- |
| RUN-1（=T3-AC1） | 真实复用发生（约 1 分钟绿、REUSED 链接与五条件比对、`gate-attest-decision.json` 落盘） | **PASS** | 复用 run 40459253766（2m20s）复用 40457662987（15m16s），判定 JSON 含五条件比对（artifact 12712759696） | 已解除（2026-10-27，T3-AC1 行有完整链接） |
| RUN-2（=T3-AC2） | 条件不满足回退完整重跑 | **PASS** | 40451804278 与 40456413700 两次真实回退：完整重跑、门禁绿、reason 留痕 | 已解除（2026-10-27，T3-AC2 行） |
| RUN-3（=T3-AC3） | 见证故障回退（门禁不变红不卡死） | **PASS** | 40456413700 为真实 API 故障回退（runner 内 gh 缺 GH_TOKEN），门禁不变红不卡死、reason 留痕；强于注入实验 | 已解除（2026-10-27，T3-AC3 行） |
| RUN-4（=T3-AC4） | 复用透传 scope 记录的运行级可审计性 + fork PR 跨 run artifact 可见性（actions: read） | **PASS**（同仓；fork 残留） | `reused-scope.json`/`reused-gate-anchor.json` 落盘于 artifact 12712759696，逐字段复核 | 同仓部分已解除（2026-10-27）；fork PR 场景无实例，维持登记 |
| RUN-5（=T3-AC5） | opened/synchronize/reopened 实跑逐字不变 + workflow_dispatch 全量语义不变 | **PASS**（synchronize） | push run 40454766551/40457662987 均完整重跑；opened/reopened/dispatch 留自然载体（wiring 断言已 PASS） | synchronize 部分已解除（2026-10-27）；其余以日常 PR 累积 |

## 三、T4 #630 收口自身核对

| 定位坐标 | 验收点原文（逐字引自票面） | 判定状态 | 交付分类 | 证据 | 备注 / 解除条件 |
| --- | --- | --- | --- | --- | --- |
| T4-AC1 | 「workflow 头注释与 Spec/机制逐字一致（含复用条件、回退语义、逃生通道三者的最终实现口径）」 | PASS | 已交付 | 2026-10-25 在 head `8ad31b5f` 逐字核对：`.github/workflows/lingdoc-review.yml` 头注释的复用条件五要素（同 PR、同 head、同 main tip、completed+success、本 workflow 产生）、「any doubt falls back to the full rerun」回退语义、「workflow_dispatch escape hatch always rerun in full」逃生通道、`actions: read` 权限注释、attest 步骤注释（任何不确定回退、见证永不使门禁变红）与 `lingdoc_gate_attest_reuse.py` 五条件/纯函数口径、`lingdoc_gate_decision.py` `GATE_ANCHOR_FILE`+`_main_sha` docstring、`lingdoc_gate_attest_witness.py` docstring（三记录来源、候选取最新不跳过失败、`gate-attest-decision.json`、`reused-scope.json`/`reused-gate-anchor.json` 透传、恒零退出）**全部一致，未发现漂移，未修改任何注释**；F1–F5 后的实现口径（attest_outcome 进 result.json、GATE_ANCHOR_FILE 公共常量、manifest 派生、判定 JSON 落盘、main_sha 回退链）均已反映在对应注释中（F1–F5 行） | — |
| T4-AC2 | 「#627 验收矩阵全部行有证据：机器可判定条件层的 unittest 结果、运行层的复用/回退/故障三份真实记录，PASS 与 NOT RUN 如实区分」 | **PASS** | 已交付（运行级解除 2026-10-27） | 运行级解除（2026-10-27）：运行层三份真实记录（复用 40459253766／回退 40451804278＋40456413700／故障即 40456413700）已回填本矩阵 T3-AC1/AC2/AC3 行与 `T630-验证报告.json`。 机器层承前： 矩阵本体已建立且机器可判定层全部行有证据（本文件第一节，16 行 PASS，命令均在本 head 跑通）；运行层三份真实记录（复用/回退/故障）不存在 | 已解除（2026-10-27）；原解除条件：运行级第 1–3 项（RUN-1/2/3）解除后，在本矩阵 T3-AC1/AC2/AC3 行回填真实 check summary 与 artifact 链接 |
| T4-AC3 | 「登记处纪律确认：本 Spec 触碰的门禁脚本与 workflow 路径的共享面归属已核对并在票面留痕」 | PASS | 已交付 | 2026-10-25 在 head `8ad31b5f` 核对：`scripts/lingdoc_parallel_surfaces.py`（1759 行）对本 Spec 触碰路径——`.github/workflows/lingdoc-review.yml`、`scripts/ci/lingdoc_gate_decision.py`、`lingdoc_gate_attest_reuse.py`、`lingdoc_gate_attest_witness.py` 及三个对应测试文件——**零命中**（`grep -c` 逐路径核对均为 0）。按票面指令未自行向登记处添加路径，登记由管理方统一协调 | — |
| T4-AC4 | 「独立审查人可仅凭票面与链接复核全部结论，无需追问实现者」 | PASS | 已交付 | 机器层：本矩阵每行给具名测试类 + 可复现命令 + 落证 head（三件套，口径 §4.2），772→791 条测试可在任意 clone 上复制；运行层：每条 NOT RUN 行给出机器可读解除条件（口径 §5.2），审查人凭解除条件即可判定「已解除/未解除」，无需追问实现者 | 运行层证据链接随 RUN-1…RUN-5 解除回填 |

## 四、Implementation Decisions 约束核对（不计入点数，口径 §1.2）

| 约束 | 判定 | 证据 |
| --- | --- | --- |
| 只改 `ready_for_review` 事件路径行为，其他触发点语义逐字不变 | PASS（机器层；运行级随 RUN-5） | witness 对非 ready_for_review 事件恒回退（模块 docstring「`opened`, `synchronize`, `reopened`, the full `workflow_dispatch`」非复用事件集）；wiring 条件逐字断言 |
| 复用五条件全部机器判定，缺一回退 | PASS | `lingdoc_gate_attest_reuse.py` 五条件 + reuse 29 OK 正反用例 |
| 不加时间上限 | PASS | `test_no_time_limit_keeps_an_old_run_reusable`（多余时间戳字段被忽略）；docstring「No time limit」节 |
| 锚点落账 `{head_sha, main_sha, run_id, conclusion}` 写入本次 run artifact，见证只读机器记录 | PASS（机器层；artifact 运行级随 RUN-1） | `GATE_ANCHOR_FILE` 写入 + witness 三记录来源（anchor/run 列表/候选 artifact），零推断 |
| 复用时被复用 run 的 scope 判定记录原样透传进本次 artifact | PASS（机器层；运行级随 RUN-4） | `_hand_on_scope_records` → `reused-scope.json`/`reused-gate-anchor.json`，witness 单测 |
| 见证层任何错误一律回退完整重跑，fail-open 方向永远是「更多验证」 | PASS（机器层；运行级随 RUN-3） | witness 27 OK 全回退形态 + 恒零退出 + continue-on-error |
| 逃生通道：workflow_dispatch 全量模式语义与现状逐字一致 | PASS（机器层；运行级随 RUN-5） | scope 步骤 `--all` 分支逐字保留（wiring 断言） |
| check 名 `LingDoc PR gate` 逐字不变，单 job 条件分支不拆 job | PASS | workflow job 定义 + wiring「check 名不变」断言 |
| 复用路径绿 check 与完整重跑绿 check 机器视角同形（queue-state/并发组不受影响） | PASS | `lingdoc_gate_attest_reuse.py`「The queue-state shape guarantee」节 + T2-AC3 同形断言；并发组/cancel-in-progress 配置零改动（`git diff origin/main...8ad31b5f -- .github/workflows/lingdoc-review.yml` 仅触达头注释、permissions、attest 步骤与重步骤条件） |
| 文档同步：workflow 头部哲学注释补「同 merge-ref 绿 run 可见证复用」新语义，机制与注释不漂移 | PASS | T3 已补注释；T4-AC1 逐字复核零漂移 |

## 五、判定分布与遗留

- **PASS 24 行**（2026-10-27 运行级回填后）：T1-AC1…AC4、T2-AC1…AC4、T3-AC1…AC6、F1…F5、T4-AC1…AC4；另有 10 条 IMPL 约束行全 PASS（不计点数）。
- **NOT RUN 0 行**（原 8 行于 2026-10-27 全部解除，`T630-验证报告.json` 的 `not_run[]` 已清空并附 `runtime_evidence_20261027`）。残留登记：fork PR 跨 run artifact 可见性（无场景）与 opened/reopened/dispatch 的自然载体累积——均不构成 FAIL。
- **FAIL 0 行；DEFERRED_MANUAL 0 条。**
- **遗留**：运行级 5 项的解除载体是 Spec #627 自身的 PR 门禁 run（转 Ready 场景 + 日常 push run），其证据回填属 T3-AC1…AC5 行的后续动作，不阻塞本收口票的代码合并（口径 §5.3 语义：NOT RUN 阻塞的是验收放行，不阻塞矩阵登记本身）。
