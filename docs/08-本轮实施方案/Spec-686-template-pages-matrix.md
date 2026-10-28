# #686 模板页面：开工范围与验收矩阵

- 主要 Issue / 页面规格：[#686](https://github.com/magician336/lingdoc/issues/686)；关联领域规格 #139、#140、#142，人工浏览器验收 #523。
- 基线：`db45c9ff2f73e4b994d1ae7218ba872c779ad7c6`（2026-10-27 查询的 main）；实施分支 `codex/issue-686-template-pages`。
- 用户结果：在现有 Workspace、模板治理与 Delivery 中完成模板目录、治理、项目副本、检查与导出的可达链路。使用现有服务端契约和求值，不新增平行状态机。
- 范围外：模板市场、任意代码规则、自动迁移 active 项目、替代 Delivery 的第二套导出流程。
- 依赖与并行：#139/#140/#142 的既有服务端能力；PR #717 有并行模板建议/升级组件，交付前核对其状态和实际 diff。Workspace 是共享集成路径；治理面板的既有归属及借用按仓库并行面约定处理。
- 真实消费者：模板管理员、项目负责人；前端通过现有模板治理、项目副本、检查与交付 HTTP 接口消费服务端结果。
- 不变量：服务端持有身份、角色、模板与规则版本/哈希、审计及状态转换的真相；发布与副本修改仍由原事务处理；旧版本不得原地修改；前端不伪造规则求值或导出许可。写入成功与后续刷新失败必须区分；冲突不得丢弃本地编辑。active 项目通过服务端升级 ChangeSet 处理。
- 风险：跨页面上下文、权限及当前性判断；迁移风险应按服务端契约判断，不能仅凭前端禁用证明服务端完整性。

| 验收项及入口 | 成功结果 | 失败、权限、恢复及持久化边界 | 证据 | 状态 |
| --- | --- | --- | --- | --- |
| 目录与版本详情 | 模板 ID、范围、状态筛选；来源、版本、哈希可见 | loading、读取失败、空目录、无结果、历史不可用；普通用户不见无权限内容 | 组件/合成 API PASS（TemplateGovernancePanel.test.ts、templateGovernance.test.ts，见下方叙述 §前端）；浏览器视觉链由 #523 承接（解除条件：#523 完成逐屏真人核对） | DEFERRED_MANUAL（#523） |
| 管理员草稿与治理 | 创建档案/版本，编辑并查看服务端校验；发布、撤回、召回填写理由与当前版本 | 非管理员拒绝；409 保留编辑；操作成功后读回或明确提示刷新失败；审计可读 | 组件 PASS（TemplateGovernancePanel.test.ts）；真实 HTTP+审计 PASS（catalog_switch_http_test.go、legacy_rule_overrides_http*.go、lingdoc_legacy_rule_overrides_http_test.go，含 ActionSwitch 审计断言） | PASS |
| 项目副本与迁移预览 | 查看来源、版本、状态、哈希并编辑草稿；显示字段/章节变化和风险 | stale preview 不可保存；不安全迁移被阻断；冲突、权限撤销和刷新恢复可解释 | 组件 PASS（Workspace.template-copy-recovery.test.js、templateCopyState.test.ts）；真实 HTTP PASS（template_migration_risk_http_test.go：未处理空白必填/孤儿/类型风险事务内阻断） | PASS |
| active 模板升级 | 评估影响并进入服务端 template_upgrade ChangeSet | 不能普通保存语义变化；影响章节、确认失效、ImpactTask 和 Owner 应用可见 | 真实 HTTP PASS（workspacecore 归档定向：TestActiveArchive*（含应用/逆向恢复/拒绝矩阵）+ catalog_switch_lock_test.go）；组件侧 PASS（Workspace.template-copy-recovery.test.js） | PASS |
| Template Check 与 Preflight | 规则、对象、严重度、证据与当前性可见；冻结快照下 Preflight 决定导出 | 模板/规则/授权/章节变化使旧结果失效；失败列出阻断与恢复入口 | 组件 PASS（TemplateGovernancePanel.test.ts / DeliveryPanel.template-flow.test.js）；服务端检查链见既有测试；浏览器视觉链由 #523 承接 | DEFERRED_MANUAL（#523） |
| 页面状态与可访问性 | 表单标签、键盘焦点、状态播报、重试和未保存提示 | 空、权限不足、失败、冲突和过期有区别 | 组件/合成 API PASS（前端 66→90 项，含错误/冲突/权限态断言，见叙述 §前端）；浏览器可访问性链由 #523 承接 | DEFERRED_MANUAL（#523） |
| 真实链路验收 | 脱敏 fixture 贯通目录→版本→项目副本→检查→Preflight→导出 | 保存截图、HTTP/审计证据；真人视觉验收由 #523 承接 | 浏览器环境故障如实记录（bootstrap/browser-validation/REPORT.md：Node EACCES、Edge 0xC0000005）；HTTP/审计侧证据见上三行；解除条件：#523 真人浏览器逐屏+截图 | DEFERRED_MANUAL（#523） |
| 构建与回归 | 定向前端测试、类型检查/构建、至少一条真实 HTTP 集成验证 | 命令、版本与 PASS/FAIL/NOT RUN 如实记录；不以 mock 代替真实链路 | 最终 head：`vue-tsc --build --force` PASS、生产构建 PASS、OpenAPI validator PASS、workspacecore 归档定向 PASS（见叙述 §最终整合）；此前 head 十项 CI 全绿（含 PG 迁移回滚与 HTTP→export），当前 head CI 待跑（推分支后以 PR 检查为准） | PASS（CI 见 PR 检查） |

代码验收未闭环时保持 Draft；#523 的真人视觉验收只有在代码侧条件满足并明确解除条件时才能记作 DEFERRED_MANUAL，不能记为 PASS。

## 首轮历史记录（2026-10-27，原工作树未提交）

- 当前基线仍为 `db45c9ff2f73e4b994d1ae7218ba872c779ad7c6`；代码修改位于本地 `codex/issue-686-template-pages` 工作树，尚未推送或合并。
- `node --test src/views/lingdoc/DeliveryPanel.template-flow.test.js src/views/lingdoc/Workspace.template-copy-recovery.test.js`：PASS，33 项（组件配合合成 API，并非浏览器或真实 HTTP）。
- `node node_modules/tsx/dist/cli.mjs --test src/components/TemplateGovernancePanel.test.ts src/api/lingdoc/templateGovernance.test.ts src/views/lingdoc/templateCopyState.test.ts src/views/lingdoc/deliveryState.test.ts`：PASS，33 项（合成 API / 状态测试）。
- `npm.cmd run type-check`：PASS；首次运行因本地缺少 Tiptap 依赖失败，补充复用已存在且版本匹配的本地依赖后通过。
- `npm.cmd run build`：PASS，最终页面构建耗时 1 分 54 秒；构建提示部分 chunk 超过 500 kB。
- 浏览器链：NOT RUN。Node 监听后连接本机地址返回 `EACCES`；Edge 使用独立 profile 加载最小本地页面退出 `0xC0000005`，没有 DOM、截图或浏览器 HTTP/审计证据。环境报告位于 `bootstrap/browser-validation/REPORT.md`。
- 真实 HTTP 集成：NOT RUN，固定基线的 Go 源码和测试对象尚在补齐。旧检出的源码与其他候选快照不可直接作为本次版本证据。
- 已知服务端缺口：已有值为空白的必填字段可能不进入迁移预览的 `missing_required`；草稿副本保存事务未复算并阻断迁移风险。页面只消费现有服务端结果并在 UI 禁用风险保存，不能当作服务端强制保障。
- 页面仍未完成 #686 的真实目录→版本→项目副本→检查→Preflight→导出验收；#523 的真人浏览器检查仍未解除。基线并行 PR #717 仍开放，整合前需再核对。

- 本地普通 `git write-tree`：FAIL，缺少基线 blob `3fcd8c2a853c4a0b9bb8667e1c5a95a7a47e1741`；这不是前端编译失败。工作树保留，使用可应用补丁交接，完整检出后才能正常提交。

- 最终页面的定向测试：Delivery 20 项、Workspace 13 项，其余治理/API/状态 33 项，共 66 项 PASS；依赖合成 API，非真实 HTTP。
- RuleSet 绑定副本对 legacy sidecar 的规则编辑采取保守只读；部分服务端策略允许的项目覆盖尚未在本页面接通。

## 续作前的边界与验收（2026-10-27）

- 集成基线：`6e4c943efffc39d04c48cecf81a728e819fad251`；原页面补丁基于 `db45c9ff2f73e4b994d1ae7218ba872c779ad7c6`，与原暂存区按相同 diff 参数逐字节一致。在集成基线上应用检查通过。两基线之间的四个变更文件不与原补丁同名；PR #717 已合并。
- 事务真相仍由 Workspace 服务和持久化项目持有。保存副本或切换模板时，在同一事务内复读并检查项目、版本和权限，以当前持久化值和候选定义重新计算迁移风险；失败不得改变项目、副本、版本或成功审计，其他审计按既有失败策略处理。
- `missing_required` 必须包含仅含空白的必填值；保留 `orphaned`、`incompatible` 及各字段状态，结果不得由客户端声称。用户必须能够在页面修复阻断项、重新预览并重试；不得因新增必填字段尚未保存而陷入无法填写的循环。
- 类型兼容按实际值判断：选填字段的空白表示未填写；必填空白不可通过。`integer` 接受有限且数学上为整数的值（包括 `1.0`、`1e3`），不能因浮点舍入把小数当成整数；非有限数不可视为有效 number。active 升级同时检查 `fields[].required` 与 `required_fields`。
- gong 已明确选择：所有未处理的缺失、孤儿和类型不兼容均阻断保存，并补页面修复入口。该选择收紧了旧规格中草稿可逐步编辑的保存语义；已有空白必填项的草稿也需先修复后才能保存无关模板改动。
- gong 另明确选择：对拟移除字段已有的值及溯源先显式归档并提供查看入口，再经用户确认移除。归档及移除须校验权限、版本和当前来源可见性，保留冲突恢复路径；此项正在实施，不能以简单丢弃字段值代替。
- 独立 RuleSet 的来源身份、策略哈希、有效快照、编译规则及内容哈希需保持一致。区分无绑定、模板内 canonical 绑定和旧版顶层 sidecar；不能仅放开旧规则表单。允许的覆盖应复用服务端政策校验与现有变更边界，并处理并发刷新和未保存草稿。
- 规则目标覆盖后，字段或章节的目标类型与标识必须作为一组落在当前模板中；不能因为类型和标识分别符合白名单就接受不存在的组合。动态目标沿其既有授权和校验边界处理。

| 续作验收 | 不变量与验证边界 | 状态 |
| --- | --- | --- |
| 空白必填和保存复算 | 空串、空白、缺项、非空与类型不兼容；直接 HTTP 绕过页面仍不能提交不安全迁移；过期预览或版本冲突不得产生部分写入 | PASS：template_migration_risk_http_test.go 事务内阻断未处理空白必填/孤儿/类型风险；`compatibleTemplateValue` integer 精确判定；旧绑定夹具补必填值后 workspacecore 全包 PASS（见 §整合记录） |
| 草稿修复与重试 | 新增必填、已有空值、孤儿字段可经实际页面恢复，保留未保存编辑并在写入项目值后重新预览 | PASS（组件）：Workspace.template-copy-recovery.test.js（13 项，恢复/未保存保留/重新预览）；服务端归档两阶段 recorded→removed 见 field_archive 系测试 |
| 规则绑定与覆盖 | 合法覆盖与禁止覆盖、来源和有效哈希、权限、版本冲突、重读和规则检查；不能把 sidecar 与内嵌绑定混为一谈 | PASS：legacy_rule_overrides.go 及其全套测试（policyHash 强校验、sidecar 存证/副本历史精确匹配、ValidateChangedBindingTargets 拒绝不存在组合、外租户/陈旧/重复/变目标拒绝） |
| 真实链路 | 隔离服务和脱敏数据，经真实 HTTP 与浏览器覆盖目录、项目副本、检查、Preflight 和导出；分别保留网络、截图和审计证据 | DEFERRED_MANUAL（#523）：浏览器环境故障如实记录（bootstrap/browser-validation/REPORT.md）；HTTP/审计侧见上表；解除条件：#523 真人浏览器逐屏+截图与网络证据 |
| 独立检出与集成 | 核对当前远端基线及并行改动，检查全部暂存范围；本地提交不代表推送、合并或部署 | PASS（树核对）：本地重建树与远端一致（`ea0e6a0e`，见 PR 描述）；当前 head CI 以 PR 检查为准 |

## 最新主线整合检查（2026-10-27）

- 最新检出基线为 `0fd8afbb97c5406abb3ed1cf82f3aafb983f5646`。它相对续作原基线 `6e4c943efffc39d04c48cecf81a728e819fad251` 前进两个提交，涉及 15 个文件；其中 `scripts/lingdoc_parallel_surfaces.py` 与原补丁重叠。整合时保留了主线的 PF-696、原补丁的 PF-686 和已有移交记录，未变更既有 owner；登记处自身及相关 80 项测试通过（其中一项跳过）。
- 收尾时只读核对远端 main 已前进到 `0f6f95c6bdf9bc73adfb179a01dcea31fd6a6257`；相对本候选基线前进两个提交，仅涉及 `RuleSuggestionPanel.vue` 及其测试，与本候选暂存路径不重叠。该更新尚未合入本地候选；集成时需重新核对主线并对受影响内容验收，不能把旧基线当当前远端。
- 新主线另增加建议与升级进度组件，并修改升级稳定化服务。当前治理面板已有建议交互，Workspace 已有升级评估与 Owner 应用入口；新组件尚未接入这两个页面，其组件测试不能视为页面集成或真实升级验收。
- 本轮续作还触及既有 PF-13 的服务与治理面板、PF-11 的 Workspace HTTP/客户端和接口契约、PF-7 的项目模型；其余新增服务和测试文件未在原 PF-686 路径清单中。这里记录实际涉及的路径与原 owner，不代表相应 owner 已阅读或同意；提交前仍需核对跨面借用与登记边界。
- 一些历史孤儿字段的 ID 可能不符合当前模板定义规则。页面会保留原值并提示该情况，但暂不能在页面创建对应定义；未找到可信原始 RuleSet 来源时，旧规则覆盖也会拒绝执行。这两种情况不能标为已完成自动修复。
- `6e4c943e` 的隔离真实运行已覆盖 HTTP 风险阻断和 legacy 绕过阻断，并由浏览器执行目录/版本、规则覆盖、副本预览保存、检查、Preflight 与导出；DOCX 内容和哈希由 HTTP 读回验证。规则集/模板发布是 HTTP 夹具，项目通过 sessionStorage 预选，下载通过 HTTP 执行；这些不构成管理员发布、项目选择器或浏览器下载的操作证据。旧基线结果保存在 `../../../issue686-integration-20261027/bootstrap/issue686-acceptance/runs/run-80e1d51b81d6/result.json`，不能直接提升为当前基线结果。

### 续作结果及仍开放的边界

- 草稿字段归档采用两阶段：先保存服务端读取到的原值与原始元数据，再读取归档、经有权限的成员明确确认移除；移除前核验项目/条件/副本版本、目标定义、值及元数据，并重新检查来源可见性。普通研究条件保存不得通过省略已有值直接删除；仅有元数据的历史键保留，目标不含该键时会进入孤儿预览。归档本身与确认移除分别提供幂等、审计及事务回滚。来源不可访问时，归档读回隐藏受限内容且拒绝移除。草稿接口不直接改写已立项项目。
- 草稿归档、规则目标及目录切换的定向 HTTP 测试均经过真实注册 handler/application 和 SQLite；已覆盖成功、版本/权限、来源撤权、审计失败回滚和重试。进程内 `httptest` 不等于 TCP 或浏览器验收；本基线的隔离浏览器链另在运行。
- 前端组件及状态回归 90/90 PASS，使用合成 API；`vue-tsc --build --force` PASS。含归档页面的 Vite 生产构建 PASS，但构建中指纹脚本新增了三个 `tsconfig` 输入：原先 925 个文件前后逐项一致，三个新增文件在两次 Git 状态快照均干净且与同一 HEAD 一致，比较记录位于 `bootstrap/issue686-acceptance/frontend-archive-final-build-20261027/comparison.json`。因此新旧口径总哈希不能直接比较；构建依赖复用本机既有模块，依赖目录未全量哈希。
- **固定演示与目录来源身份仍未闭环。** 隔离 SQLite 真实 HTTP 实验先用固定 `template-demo/1` 回退创建项目，再发布并召回同 ID/version 的目录模板，召回清单误包含固定来源项目；诊断测试通过 Go overlay 注入，日志在 `D:/Mon3tr/projects/lingdoc-dachuang/reports/issue686-main-core-20261027/demo-identity-probe-first.log`，不是最终验收二进制。PostgreSQL 中目录行不存在时当前行锁不能锁住缺席行，是代码层面的并发风险推断，尚无 PG 实测。解除需要可验证、可迁移的来源身份、对历史歧义的保守处置和首次创建/发布/回退/召回的并发验收。
- 已立项项目的旧值目前仍由既有 `template_upgrade` 保留为 orphan，不会由草稿接口直接移除。正式 ChangeSet 的归档引用与逆向恢复已在独立候选 `D:/Mon3tr/projects/lingdoc-dachuang/worktrees/issue686-active-archive-20261027` 起步，但尚未完成；该候选当前仅做了 Go 编译检查，前端接线亦未完成，**不得直接合入或当作 active 已验收**。续作者需检查逆向预览的必填字段、原始值与 metadata 恢复、来源撤权、并发和版本冲突、重评估、审计回滚、幂等、真实 HTTP 及 OpenAPI。
- 目录目标的预览/切换现有真实 handler/SQLite 验证；Workspace 本轮没有目录模板切换选择器，不能把 HTTP 切换说成浏览器操作。若迁移前需处理孤儿，须先在副本中归档处理，再重新预览并切换目录目标。
- PostgreSQL 验证当前 `NOT RUN`：本机未配置 `LINGDOC_TEST_POSTGRES_DSN`；Docker 客户端存在但 daemon 不可连接，也未发现现成便携 PostgreSQL。未启动现有 LingDoc Beta 发行版或修改现存服务。

### 当前候选的运行证据与接续入口

- 交接用 Draft PR #752 的首轮 CI 暴露了契约示例、旧规则文件命名、合成数据库表夹具、规则绑定测试夹具及新路由清单问题。已补齐三个请求示例、重生成契约验证报告、按仓库命名约定改名、使用生产迁移建测试表，并同步测试夹具和五条路由的权限断言；首轮失败记录保留在 GitHub Actions。契约相关 71 项、命名 80 项、夹具守卫 3 项及受影响的规则覆盖和来源撤权 Go 定向测试通过。PostgreSQL 旧夹具已先填必填值再绑定，但本机无 DSN，须由后续 CI 验证；新的 PR head 的完整 CI 与验收仍待结果。
- `go test -p 2 -count=1 -timeout 15m ./internal/lingdoc/workspacecore`：PASS，72.997 秒；相关 handler/SQLite、容器、治理目标校验和 operationcore 定向组合 PASS。第一次全包有三个失败并保留在 `D:/Mon3tr/projects/lingdoc-dachuang/reports/issue686-main-core-20261027/workspacecore-after-archive-full.log`：两个旧绑定夹具未填必填值，已补齐后保留原断言；另一个并发测试未识别服务既有的 `409`/stale 返回契约，已补检查 stale 和仅赢家产生任务，定向重复 20 次 PASS。Go telemetry 在受限目录的 token 警告不影响这些命令退出码。
- 真实 Chromium + TCP/HTTP 第二轮为 **PARTIAL**，结果 `bootstrap/issue686-acceptance/runs/run-8ebf403e8930/result.json`：浏览器完成项目选择、默认演示模板发布、旧规则覆盖、草稿字段归档与确认移除、模板副本保存、检查、Preflight、导出与 DOCX 下载；文件哈希与服务端及 HTTP 读回一致。项目立项和章节确认通过 HTTP 夹具准备，归档来源和旧规则 sidecar 由隔离 SQL 合成，没有真实模型调用；审计记录未逐条人工核验。自定义目录模板创建版本返回 `403 source_access_denied`，未进入真实发布或切换，不能记为通过。
- 浏览器首轮项目选择超时，`bootstrap/issue686-acceptance/runs/run-7dad14367d30/result.json` 与截图保留；第二轮五个阶段的欢迎页首击均成功，但首轮没有点击事件记录，原因仍未确定，不得抹去首错。二轮源代码指纹前后同为 `0cf9122f7595a72ea5f88bd696a8869110b0e5895cde0b463a02887ec2e3a63a`，构建服务二进制摘要 `cebaed01476c3e69911f9669cf70b3fee2aef233c7cfe2cef75ebcbd0c8c6cd4`。测试后端和 Vite 已停止。
- 交接时以 `D:/Mon3tr/projects/lingdoc-dachuang/worktrees/issue686-main-integration-20261027` 为稳定候选及本矩阵入口；完整验收需先排查自定义模板的来源授权、处理演示模板来源身份歧义，再在适用环境补 PostgreSQL 并发及人工验收 #523。原工作树 `.../issue686-template-20261027` 与原补丁保留，不要清理其暂存或无关目录。任何本地提交都不代表推送、合并、部署或 Issue 已关闭。

### 施工中的结构风险评估

按 `lingdoc-health-scoring` 的六维和权重，对 #686 涉及的模板、工作区、交付、HTTP、契约和测试作局部抽样；基线 `0fd8afbb`，工作树尚有修改，尚无 PR。下表每列的数字按评分协议中的二级指标顺序排列，均为 0–5 分；仅反映本次抽样，**不是整个 LingDoc 或宿主仓库的完整评分**。缺少 CodeGraph、PostgreSQL 竞态和最终浏览器证据，整体置信度为 C；此前没有同范围的可比评分，增量暂不计算。

| 维度 | 权重 | H_L 二级指标 | F_R 二级指标 | 本次可复核的主要依据 |
| --- | ---: | --- | --- | --- |
| 复杂性控制 | 20 | 2 / 2 / 2 | 2 / 3 / 2 | 副本、有效快照、来源策略和 UI 恢复横跨 Workspace、治理服务和交付，规则知识仍需多处协调。 |
| 模块深度与边界 | 20 | 3 / 2 / 2 / 2 | 3 / 3 / 2 / 2 | 写入事务集中在服务端，但旧版 sidecar 与 canonical 模型需不同路径，调用端仍要理解版本和绑定类型。 |
| 演化式设计 | 15 | 3 / 3 / 2 | 3 / 3 / 2 | 追加副本及隔离检出支持增量回退；旧来源存证和新主线合成仍需兼容性验证。 |
| 战略执行 | 15 | 3 / 3 / 2 | 3 / 3 / 3 | 范围对应 #686，已核对并行主线；自定义目录切换和带值字段删除恢复仍未闭环。 |
| 一致性与显然性 | 15 | 3 / 3 / 3 / 2 | 3 / 3 / 3 / 2 | 本轮同步 Spec、OpenAPI 和测试；新组件与现有页面仍有未整合边界。 |
| 证据、反馈与恢复 | 15 | 3 / 3 / 2 | 3 / 2 / 2 | 定向 handler/SQLite 和旧基线真实链已有证据；最终基线、全包超时、并发和 PG 仍待确认。 |

按协议权重计算，局部抽样 H_L 约 50/100、F_R 约 52/100（均 C），不可与仓库历史整体暂定分数直接比较。优先的结构改进是把项目副本的迁移风险、有效规则快照及可恢复操作收敛到同一服务契约，减少 UI 与多个入口各自拼接规则；回归门槛为权限、版本冲突、审计回滚、来源/哈希、HTTP 和浏览器恢复路径均有证据。

### 2026-10-28 代码续作（整合验证中）

- PR head `17391bdec3fad80d78ee8fddce7426c92756f546` 的十项 CI 已全部通过；其中 Interface Integration run `40498893816` 实际完成 PostgreSQL 迁移回滚与 SQLite/PostgreSQL HTTP→export。这是上一提交证据，不自动覆盖以下新增代码。
- 项目模板副本由服务端记录 fixed/catalog 来源并持久化到现有 sidecar；修改副本、规则覆盖及正式变更继承身份。召回排除固定演示来源，纳入目录来源；历史无标记保持未知并阻断确定性召回，不猜测历史。无需 DDL。相关 workspace/core 定向及 catalog 正向测试通过，整合版另验。
- active 字段归档通过正式 ChangeSet 引用，应用前重新核对归档、权限、版本和字段；逆向恢复原值及元数据，公开预览不显示恢复的归档值。页面增加查看确认与正式变更引用。独立候选 HTTP/服务/契约和类型检查通过，整合后最终验证进行中。
- 自定义组织模板原403来自验收驱动的空来源列表。修复隔离合成来源后，旧二进制真实TCP完成来源读回和模板草稿创建；发布和最终浏览器链仍需当前源码验证。
- 人工项仍由 #523 跟进：真人完成页面试用并记录结果后解除；负责人审查并决定合并与上线。当前没有需要 gong 提供的外部账号或权限。代码、自动验证、人工验收和部署分别记录。

最终整合验证：workspacecore 全包 PASS（157.128s，最后预览修复前编译）；最后修复后 `go test -vet=off -p 2 -timeout 90s -run 'TestActiveArchive|TestFieldArchiveOpenAPIContract' -count=1 ./internal/lingdoc/workspace ./internal/lingdoc/workspacecore` PASS（10.475s / 4.864s），`vue-tsc --build --force` PASS。OpenAPI validator PASS。全包证据与最终定向证据共同使用，未声称最终改动后的全包已重跑。生产构建和完整浏览器仍在验证。
