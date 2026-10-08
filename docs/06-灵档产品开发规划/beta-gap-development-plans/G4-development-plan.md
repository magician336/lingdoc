# G4 模板与规则：按 Matt 方法的开发计划

> 基线：`main` / `50f0dba921cd93d0660bfa166d49cefc354fa5bd`（2026-10-05 核对）。
> 过程路由：`grill-with-docs → to-spec → to-tickets → implement`。
> 本文件是 G4 的实施 spec 与 tracer-bullet ticket 拆分；相关长文仍是设计参考，不表示这些能力已经实现：
> [G4 模板、规则与问题治理](G4-template-rules.md)、[G4 项目级模板副本](G4-project-template-copy.md)。

## 1. 要交付的用户结果

在一个已有的 LingDoc 项目中，项目 Owner 能够：

1. 从已发布的模板版本创建项目私有副本，看到来源、版本、哈希和适用范围；
2. 在草稿期编辑字段、章节、术语和声明式规则，预览差异、缺失字段、孤儿值和迁移影响；
3. 发布一个不可变的副本版本，并让项目骨架、`ProjectSpec`、章节和交付检查消费同一版本；
4. 看到 Template Check 与导出前 Preflight 的规则、目标、证据和阻断原因；
5. 对允许处置的问题执行带权限、理由和版本条件的 `resolve`/`dismiss`，但不能用处置绕过 blocking、授权或版本失配；
6. 在模板或规则版本变化后回放旧快照，并看到新版本重新求值而不是复用旧裁定。

成功标准是“模板和规则的来源、版本、求值输入、问题处置和冻结快照可追溯”，不是让系统替专家判断创新性、学术价值或结论正确性。

## 2. 当前事实、范围和停止条件

### 已有事实（代码可核对）

- `internal/lingdoc/template` 已有 `Template`、`Rule`、`Reader` 契约；`internal/lingdoc/delivery` 的 `FixedTemplateReader` 只提供 `template-demo/1` 和确定性章节规则。
- `workspacecore` 已把 `TemplateReader` 注入项目创建、模板迁移和生成上下文；G1 已覆盖模板迁移预览、字段缺失/孤儿保留和版本条件写入。
- `delivery` 已能在冻结输入上计算章节非空、确认和来源版本等规则，并生成 digest；T12–T14 的当前性、冻结和导出是现有交付边界。
- 当前契约/Mock 能消费固定模板；它们不能证明项目副本、规则编辑、问题治理或模板升级已经存在。

### 本次必须

- 建立不可变的 `TemplateProfile`/`TemplateVersion`/`ProjectTemplateCopy`/`RuleSet`/`RuleDefinition` 最小领域模型与持久化边界。
- 项目创建时复制并绑定明确模板版本；源模板的新版本不得静默改变已绑定副本。
- 草稿副本支持字段、章节、术语和声明式规则的版本化编辑；提交使用预期版本和幂等键，冲突不得覆盖。
- 规则只允许白名单种类（`computed`、`pattern`、`presence`、`consistency`，以及显式标记的 `model_assisted` 建议），禁止上传或执行任意代码。
- 统一 `RuleEvaluationInput`/`RuleEvaluationResult`，让 Template Check 与 Preflight 使用同一规则语义；Preflight 是唯一导出闸门。
- `ValidationIssue` 绑定 `(rule_id, target_ref, target_version, ruleset_hash)`，稳定去重但不跨新版本复用旧裁定；处置动作必须审计并重新鉴权。
- 接入 G3 `template_upgrade` ChangeSet、G5 权限边界和 G6 质量/运行指标，但不复制它们的真相。

### 明确不做

- 模板市场、跨项目共享编辑、通用拖拽编辑器和实时多人协作；
- 用户上传规则代码、自动发布模板、自动改变严重度、自动放行 Preflight；
- 自动判断创新性、学术价值、结论正确性或替专家写研究判断；
- 重新实现 G3 ChangeSet/ImpactTask、G5 权限引擎或 G6 运营平台；
- 为了让演示通过而放宽撤权、版本失配、缺源或冻结内容变化的阻断。

### 停止条件

若无法在同一输入上证明写作期检查与冻结前 Preflight 的规则结果一致，停止扩展 UI，先修正规则契约和可重复求值测试。若任何接口把客户端传来的 `severity`、`ruleset_hash`、授权结论或“已处置”当可信值，停止该切片。若模板升级不能原子保留旧快照和旧版本回放，暂不接入 active 项目。

## 3. 固定领域决策（spec）

1. **模板版本不可变。** `TemplateVersion`、发布后的 `RuleSet` 和 `ProjectTemplateCopy` 只能创建新版本；历史版本可读、可回放，不原地修改。
2. **副本拥有项目编辑语义。** 草稿副本记录源模板版本、内容/规则哈希和迁移结果；项目读取字段、章节和规则时只依赖已绑定副本版本。
3. **规则是声明式数据。** 求值器按白名单 `kind` 解释结构化参数；未知 kind、越权目标和任意表达式在保存/发布前拒绝。
4. **问题身份包含版本。** `ValidationIssue` 的逻辑身份至少包含规则、目标和目标版本；规则集或目标 revision 改变后重新求值，不能沿用旧 `resolved`/`dismissed`。
5. **处置不是放行。** `dismiss` 只影响同一规则/目标/版本的问题展示和阻断语义；`waive` 受严重度和权限约束，blocking 不可 waiver；任何处置都不绕过授权、来源和冻结一致性。
6. **确定性闸门归后端。** 前端可解释问题和提交意图，但不能决定发布、严重度、问题状态或导出许可；AI 只能产生建议草稿和解释。
7. **深模块边界。** workspace 依赖模板/规则/问题的窄接口；HTTP 负责鉴权、版本条件和错误映射，不能复制规则判据。

## 4. Tracer-bullet tickets 与阻塞边

### G4-1：模板版本与项目副本领域模型

**用户结果**：从 `template-demo/1` 创建一个可独立读取的项目副本，源模板更新不会改变它。

**最小接口**：`TemplateCatalog.GetVersion`、`ProjectTemplateCopies.Create`、`Bind`、`GetForProject`；返回 source、version、status、content hash、ruleset hash。

**先写测试**：未知模板/版本、跨项目读取、重复创建、源版本更新、绑定旧版本回放、项目访问撤权；验证历史副本只读且无半完成绑定。

**阻塞边**：T01/T04 的迁移与事务约定、T07 ProjectAccess、G1 模板迁移接口。解除标准是 SQLite 重启读回、唯一约束和原子绑定均有副作用断言。

### G4-2：副本草稿编辑与迁移预览

**用户结果**：Owner 能编辑字段、章节、术语并看到缺失/孤儿/类型变化；保存冲突不丢另一窗口修改。

**最小接口**：草稿读取、`PreviewChange`、`SaveDraft(expected_version, idempotency_key)`、发布前结构检查。字段必须有稳定 `field_id`；删除或改类型必须产生可处理的迁移项，不能静默丢值。

**先写测试**：字段新增/删除/改类型、章节重排、孤儿值保留、必填缺失、重复提交、预期版本冲突、失败后草稿仍可恢复。

**阻塞边**：G4-1、G1 的迁移预览/提交语义。解除标准是同一副本的 ProjectSpec 和骨架生成读取同一版本。

### G4-3：声明式 RuleDefinition 与统一求值器

**用户结果**：同一条规则在写作期和冻结前对同一输入得到一致、可解释的结果。

**最小接口**：`RuleRegistry.Validate`、`RuleEvaluator.Evaluate(RuleEvaluationInput)`、`RuleSet.Hash`。输出必须含 rule、target、severity、message、evidence、evaluator_version；不接受客户端结论。

**先写测试**：presence/pattern/consistency/computed 的正反例、未知 kind、非法参数、稳定排序/hash、编辑输入与 FrozenDeliveryInput 一致性、模型建议不改变 blocking。

**阻塞边**：G4-1、G4-2、现有 `delivery` 规则与冻结输入契约。解除标准是重复运行得到相同 canonical 结果，并能指出输入证据。

### G4-4：Template Check、Preflight 与 ValidationIssue

**用户结果**：用户能定位规则问题及下一步；写作期问题不会被误当成导出许可，Preflight 重新读取当前版本。

**最小接口**：`CheckTemplate`、`RunPreflight`、`ListIssues`、问题去重/版本化存储。问题必须绑定目标版本和 ruleset hash，输出阻断原因和当前性。

**先写测试**：空章/缺必填、规则命中与清除、规则集变化重新出现、冻结输入变化、过期结果标 stale、重复求值不重复写、导出只接受当前通过的 Preflight。

**阻塞边**：G4-3、T12/T13/T14 的 DeliveryInput/FrozenDeliveryInput、G3 当前 revision。解除标准是旧问题不能关闭新版本问题，blocked 快照不能下载。

### G4-5：问题处置与权限/审计

**用户结果**：有权限的 Owner 能对允许的问题给出带理由的处置；无权限或 blocking 处置被明确拒绝。

**最小接口**：`ResolveIssue`、`DismissIssue`（必要时 `WaiveIssue`），请求含 expected target/ruleset version；服务端重新读取严重度、权限和当前性并写审计。

**先写测试**：无权限、撤权、理由为空、版本过期、blocking waiver、同一问题重复处置、ruleset/target 改变后旧处置失效；验证不产生章节、快照或导出副作用。

**阻塞边**：G4-4、G5 权限契约、G3 ImpactTask 语义。解除标准是问题处置与 ImpactTask/确认状态分离且可回放。

### G4-6：active 模板升级 ChangeSet

**用户结果**：active 项目不能直接改模板；Owner 能预览迁移影响并原子应用升级，旧交付快照仍可回放。

**最小接口**：`template_upgrade` 的评估、迁移预览、应用和 stale 条件；复用 G3 ChangeSet，不新建平行升级状态机。

**先写测试**：旧基线拒绝、字段迁移失败、必填新增阻断、确认/问题/路线节点受影响、应用中失败回滚、旧快照回放、新 revision 重新求值。

**阻塞边**：G4-1～G4-5、G3 ChangeSet/ImpactTask、G5 Owner 权限。解除标准是应用前后项目指针、模板绑定、任务和审计要么全部成功，要么全部保持旧状态。

### G4-7：编辑器与连续流程接真

**用户结果**：用户在现有工作台看见来源提示、差异、规则问题、处置状态和恢复入口，并完成“复制→编辑→发布→立项/升级→检查→冻结→导出”流程。

**交付**：API adapter、加载/空/冲突/失败状态、规则证据面板、问题中心、模板版本标签；不另建平行业务状态。

**阻塞边**：G4-1～G4-6；使用固定 Mock 开发，但正式演示必须接真实服务和 SQLite/迁移。解除标准是第二人按命令复现并打开、编辑真实 DOCX。

## 5. 实施顺序、完成线与 review

顺序固定为：`G4-1 → G4-2 → G4-3 → G4-4 → G4-5 → G4-6 → G4-7`。G4-1～G4-4 是可独立验收的最小主链；G4-5～G4-6 在权限和 G3 接口稳定后接入 active 项目。每张 PR 必须包含接口/迁移（如有）、失败测试、契约样例和复现证据，不以静态 JSON 代替真实服务验收。

按 `/implement` 的 TDD 节奏推进：每张 ticket 先写一个会失败的行为测试，再做最小领域实现，随后接 HTTP/adapter，最后执行 `/code-review` 两轴检查：

- **Standards**：是否复用现有 TemplateReader、事务、迁移、请求封装、幂等和审计 seam；是否把复杂度推给调用方；
- **Spec**：是否保持版本不可变、问题可回放、规则可重复求值、权限重新检查和 Preflight 唯一闸门。

## 6. 交接与验证证据

- 基线：记录 `git rev-parse origin/main`、工作树状态和实际测试环境；不把密钥或受限模板内容写入仓库。
- Go：至少运行 `go test ./internal/lingdoc/template ./internal/lingdoc/delivery ./internal/lingdoc/workspacecore ./internal/lingdoc/workspace/... -count=1`；迁移/SQLite/PostgreSQL 结果分开记录。
- 前端：在 `frontend` 运行 `npm run type-check` 和现有组件测试；按脚本实际支持的参数记录命令。
- 场景证据：复制隔离、字段迁移失败、并发冲突、规则正反例、问题版本重现、blocking 处置拒绝、撤权、模板升级回滚、旧快照回放、Preflight 阻断与恢复、真实 DOCX 打开编辑保存。
- 每个场景记录请求序列、响应错误码、数据库副作用、规则输入/输出摘要和 `PASS/FAIL/NOT RUN/BLOCKED`；失败时先保留最小复现，不用“计划上支持”标记完成。

## 7. 后续触发条件

只有项目副本和声明式规则已在真实模板上证明不足，才重新开 spec 讨论模板市场、复杂编辑器或更丰富规则语言。只有出现跨项目治理和组织审批的真实需求，才把权限/审计扩展交给 G5；只有有测量数据证明误报、漏报或运行成本问题，才由 G6 引入相应指标和运营能力。任何扩展都必须保留 G4 的版本、来源、冻结和人工裁定边界。
