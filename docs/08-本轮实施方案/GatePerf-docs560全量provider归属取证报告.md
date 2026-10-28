# GatePerf-docs560 全量 provider 归属取证报告（票 #737）

- 关联：治理票 #719／本票 #737（两轴评审审计 #3、#4 同源）；同型先例 #723（前端委托）、#724（provider 分片）。
- 分支：`ticket-737/app-delegation`（工作树 `D:\wt\gp2-737`，起点 `cb57f5c9`）。
- 本报告是**独立可交付的取证部分**：本票只取证、只在确认归属过宽时单列缩窄方案（不实施，可另票）；反向委托实现与其证据见 `GatePerf-app反向委托-验收报告.md`。

## 0. 现象（Phase 0 §10.4 的实测异常）

Phase 0（#721）17 run 分步矩阵留下一条未解异常：**名义 docs 分支 `docs/560-glossary-actor-role` 触发了全量 provider 面**，provider 步 567–934s，三次 run 的 gate wall 为 888s / 922s / 1266s（`GatePerf-Phase0-分步耗时基线报告-20261027.md` §run 列表与 §10.4）。按 ADR 0006 的「按贡献面判定」，一个不碰任何 Go 代码的分支不应该跑 provider 测试。

## 1. 取证对象与证据链

| 项 | 值 |
|---|---|
| PR | #714「docs(560): actor_role 语义入两本术语表 + 词表漂移守护 + 释放 L6 陈锁」（已合并） |
| 分支 / head | `docs/560-glossary-actor-role` / `67de3e63c4ca68e6226e63694cde1b209c52b93c` |
| 取证 run | `40421100592`（LingDoc Review，pull_request，2026-10-26，wall 922s） |
| artifact | `lingdoc-checks-40421100592-1`（id 12699430350，retention 14 天内取回，2026-10-27 下载） |
| 读取文件 | `scope.json`（判定报告）、`gate-conclusion.json`（门禁结论）、`result.json` |

PR #714 的贡献恰好四个文件（scope.json `changed_paths`，与 `GET /pulls/714/files` 一致）：

1. `GLOSSARY.md`
2. `docs/06-灵档产品开发规划/16-术语表.md`
3. `docs/BETA_STATUS.md`
4. **`scripts/ci/test_lingdoc_glossary_actor_role.py`**（词表漂移守护——一个纯新增的守护测试文件）

## 2. scope.json 取证结果

| 字段 | 值 | 说明 |
|---|---|---|
| `diff_range` | `c8785d1f...67de3e63` | 完整 PR 贡献区间 |
| `changed_paths` | 上节 4 个文件 | 无任何 `.go` 文件 |
| `provider_packages` | `./internal/lingdoc/...` `./internal/evidence/...` `./internal/models/acceptancebudget/...` `./cmd/acceptance-budget-reconcile/...` | **全部四个 provider 根，非贡献触及** |
| `decision.run_providers` / `provider_tests` | `true` / `true` | 全量 provider 验证被选中 |
| `decision.reason` | `contribution surface changed and the base moved under the branch` | |
| `decision.surface` | 四个 provider 根模式 + `frontend` | **被测面=全量**，不是贡献面 |

gate-conclusion 的检查行直接写明了归属：

```
[runs] Gate judged by the whole scope :: in scope: the contribution changes the
       gate's own files (scripts/ci/); the base moved under the branch
[runs] Provider packages: test and vet :: in scope: the contribution changes the
       provider packages (internal/lingdoc, internal/evidence, ...)
```

## 3. 根因链（代码定位，当前 main `cb57f5c9` 仍成立）

1. 面清单 `scripts/ci/lingdoc_gate_surfaces.py` 把**整个 `scripts/ci/` 目录前缀**登记为治理路径：`GOVERNANCE_DIRS = ("scripts/ci/",)`（governance-scripts 面，`role: governance`）。
2. 判定模块 `scripts/ci/lingdoc_gate_decision.py` 的 `_is_governance()` 对 `scripts/ci/` 下**任何**文件返回 True——包括一个纯新增的 `test_lingdoc_*.py`。
3. `_decide()` 的治理分支（`if governance:`）语义是「gate 在judging 自己：判定知识被改，必须全量回退」——`provider_packages = _existing_provider_roots(root)`（全部现有根）、`surface` 置为全量、`governance_changed=True`。
4. 于是 run_providers=true → provider 步按四个根顺序全跑（该 run 尚在 #724 分片之前，wall=各根之和）→ 567–934s。

**根因一句话**：`scripts/ci/` 的治理面登记把「新增一个守护测试文件」当成了「gate 判定自身被改动」，触发治理分支的全量回退，其中包含与贡献毫无交集的全量 provider 面。

## 4. 归属过宽的确认

治理全量回退的正当性来自「判定知识变了， narrowed 判定不可信」。但 `scripts/ci/test_lingdoc_glossary_actor_role.py`：

- 不被判定模块 import（判定只 import `lingdoc_gate_surfaces`），不可能改变任何面的选择；
- 它的验证者本来就存在且恒跑——prepare job 的守护套件 `python -m unittest discover -s scripts/ci`（无条件步，`test_lingdoc_gate_contracts.py::GuardTestExecutionTests` 钉住）会执行它，不需要 provider 全量回退来「补偿」。

**结论：确认归属过宽。** `GOVERNANCE_DIRS` 的目录前缀登记宽于治理语义（gate 判定知识与必需检查清单）所需的集合。

## 5. 缩窄方案（单列，不实施，可另票）

**方案 A（推荐）：把治理路径从目录前缀收窄为显式清单。** `GOVERNANCE_DIRS` 从 `("scripts/ci/",)` 改为在面清单中显式列举判定知识与门禁自证文件：判定/登记模块（`lingdoc_gate_surfaces.py`、`lingdoc_gate_decision.py`、`lingdoc_gate_concepts.py`、`lingdoc_required_checks.py`、`lingdoc_gate_queue_state.py` 等判定与接线模块）、`requirements-lingdoc.txt`；`GOVERNANCE_FILES`（`lingdoc-review.yml`、`pull_request_template.md`）不变；`test_lingdoc_*.py` 移出治理面（它们的证据由恒跑的守护套件承担，归属不变）。
- 收益：docs+守护测试类分支不再触发 567–934s 的全量 provider 步（Phase 0 口径）；`governance-scripts` 面的 verdict 与治理分支同源收窄，语义一致。
- 风险与缓解：新判定辅助模块漏登记 → 治理全量不触发。缓解：面清单是唯一登记处（ADR 0007），加一条守护测试对照 import 图钉「判定模块集合完整性」（新增 `scripts/ci/lingdoc_*.py` 非 test 文件必须出现在治理清单，否则红）。

**方案 B（保守变体）：保留目录前缀，判定分支特判「改动全部为 `test_lingdoc_*.py` 的新增/修改」不回退。** 判定模块内加规则，逻辑分叉多、易漂移，不如 A。

**两方案的验收口径**：另票实施时取「docs+test_lingdoc_* 分支」与「真判定模块改动分支」各一次真实 run，前者 provider 步应 skipped、后者必须仍全量（治理回退不弱化），同 Phase 0 口径记录。

## 6. 对本票（app 反向委托）的校准结论

- PR #714 上 **App 工作流的 verify job 是 skipped**（run `40421100665` jobs：`Decide what this gate runs` success、`Format, vet, test, and build` skipped——app 面的 glob 表不匹配 `.md`/`.py` 改动）。即 docs/560 异常与「app 与 gate 双跑 provider」无关：app 侧反向委托既不解决也不恶化该异常，两者独立；本票的收窄收益对象是「贡献触及 provider 包」的 PR，取证结论校准的是面清单治理归属，不改变本票实现。
- app job 全量基线（Phase 0 §分步）：wall 228–238s，内部大头「Vet and test」168–186s（无 `-race`）——反向委托的省时上限就在这一段里被委托包的份额，实测对比进验收报告（真实 run 标 pending-orchestrator）。

## 7. 证据可复核性

- artifact 在 run `40421100592` 页面 retention 14 天（约 2026-11-09 过期）；本报告 §2 的字段值即取回时原文。
- 本地重放口径：`python scripts/ci/lingdoc_gate_decision.py --event <event.json> --root <checkout>`（判定模块离线入口）对 `c8785d1f...67de3e63` 区间重放可得与 scope.json 一致的治理归属（`_is_governance` 命中 `scripts/ci/test_lingdoc_glossary_actor_role.py`）。
