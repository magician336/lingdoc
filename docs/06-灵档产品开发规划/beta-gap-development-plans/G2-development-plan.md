# G2 依据回看：按 Matt 方法的开发计划

> 基线：`main` / `ab5fe3d`（2026-10-04 核对）。
> 本文件是第二阶段减量计划的 G2 实施 spec 与 ticket 拆分；实现范围以 G2-spec 和当前代码为准。
> 过程路由：`grill-with-docs → to-spec → to-tickets → implement`。实现交接记录见 `docs/08-本轮实施方案/G2-验收与交接.md`。

## 1. 要交付的用户结果

在一个已立项项目的章节工作台中，作者能够：

1. 选择项目内已授权且可用的资料片段，把它作为章节引用保存；
2. 看到引用对应的资料版本、定位、原文片段和当前状态，并能重新打开上下文；
3. 给每条引用补一条人工用途/限制备注；
4. 在来源撤权、重解析、版本变化或定位失效后看到“待核/不可用”原因；
5. 保存章节新版本时保留引用和备注，确认/冻结/导出只消费当前版本且状态可用的引用。

成功标准是“保存后的引用仍能回到正确来源，状态变化不会静默放行旧依据”，不是搜索结果数量或自动生成完整研究结论。

## 2. 事实、范围和停止条件

### 已有事实（代码可核对）

- `internal/evidence` 已提供 `AssetGateway`、`SourceResolver`、`SourcePolicy.Validate`，并按 ADR-0001 保留定位/哈希/失效判据。
- `internal/lingdoc/workspace` 已有资料绑定、`retrieveSources`、`getSource`、`getSourceContext`，且会再次检查项目归属、授权、资料版本和定位。
- `internal/lingdoc/workspacecore` 的 `ChapterVersion` 不可变，保存 `body_markdown`、`source_ids` 和 `review_items`；`delivery` 已冻结 `FrozenSource` 和资产版本。
- 前端 `Workspace.vue` 已展示来源、上下文和待核项，但章节引用目前只有 `source_ids`，没有持久化的用途/限制备注。

### 本次必须

- 为章节引用增加稳定的、版本化的“引用使用记录”（至少含 `source_id`、用途、限制、创建者、创建时间和基于的章节版本）。
- 读章节时返回引用记录及实时来源状态；来源不可用/过期时只标记待核或阻断交付，不返回受限正文。
- 保存引用记录与章节版本采用同一预期版本/幂等边界；冲突不得覆盖另一窗口的修改。
- 前端提供查看来源、编辑备注、保存失败恢复和待核原因入口；不另建平行工作台。
- 增加领域、SQLite/迁移、HTTP、前端 adapter 和连续流程证据。

### 明确不做

- `ResearchTask`/`ResearchRun`、外部多渠道检索、通用 Research Agent；
- `ResearchClaim`、`EvidenceCard`、争议证据并存和“每段先建主张”；
- 重新解析产物落库、PDF/DOCX 强档提升、通用知识图谱或权限引擎；
- 自动判断主张是否成立、自动消除待核项、自动把旧引用迁移到新文本。

### 停止条件

若 T09 的真实来源复核、T08 的章节版本/条件写入或 T12–T14 的当前性检查无法提供稳定接口，先交固定 adapter 和失败证据，不在 G2 内复制第二套判据；若任何路径可能把撤权正文放入响应或 AI/交付输入，立即停止该切片。

## 3. 领域决策（spec 固定点）

1. **引用使用记录不是 EvidenceCard。** 它只表达“本章节使用了哪条已定位来源、用途和限制”，不表达主张已被证明。
2. **状态由实时 SourcePolicy 决定。** 数据库中保存来源版本快照供历史回放；每次查看、确认、冻结和导出都重新授权和复核。
3. **备注随章节版本不可变保存。** 修改用途/限制会创建新章节版本；旧版本和旧备注保留，不能原地更新历史。
4. **失效传播保守处理。** `stale`、`unavailable`、撤权和版本不匹配进入 `needs_review`；不能用人工备注把它们豁免为可交付。
5. **接口保持深模块。** 工作区只依赖一个 `CitationUsageStore`/`SourcePolicy` 组合接口；定位、版本和授权判据留在 evidence 模块，HTTP 只做薄适配。

## 4. Tracer-bullet tickets 与阻塞边

### G2-1：引用使用记录领域模型（最小红绿切片）

**用户结果**：给一条当前 `Source` 写入用途/限制，返回不可变记录；同一章节版本和幂等键重放不重复写。

**接口**：`CitationUsageStore.Save(ctx, actor, projectID, chapterID, expectedChapterVersionID, usages, idempotencyKey)` 与 `ListForChapterVersion`。记录必须绑定 `source_id`、`asset_revision`、`chapter_version_id`，不得接收客户端可信的授权结论。

**先写测试**：空/重复 source、跨项目 source、预期版本冲突、同键重放、撤权/版本变化拒绝写入；成功后旧版本读回不变。

**阻塞边**：T08 章节版本写入接口、T09 `SourcePolicy.Validate`。解除标准是固定 adapter 能在 SQLite 测试中验证原子副作用。

### G2-2：持久化与迁移

**用户结果**：服务重启后引用用途/限制仍与对应章节版本一致。

新增一张与 `lingdoc_chapter_versions` 版本绑定的表（或经评审证明可安全扩展现有 JSON；默认优先独立表以便查询和审计），同时提供 SQLite/PostgreSQL 对称迁移、唯一约束和删除/历史策略。

**验收**：生产迁移后重启读回；同一 source 在同一章节版本最多一条；旧版本仍可读；迁移漂移测试通过。

**阻塞边**：G2-1；数据库迁移评审人。不得用 `AutoMigrate` 替代版本迁移。

### G2-3：工作区 HTTP 与实时来源状态

**用户结果**：章节详情一次返回引用记录与来源状态；打开上下文时不绕过 `SourcePolicy`。

新增/扩展章节引用读写端点，复用统一 envelope、`Idempotency-Key`、预期版本和错误映射。来源响应只在当前授权和定位有效时带正文；否则返回状态、原因和恢复入口，不带受限内容。

**必验**：来源撤权、重解析、跨项目 source、丢响应重试、乱序响应；HTTP 层不得复制 evidence 判据。

**阻塞边**：G2-1、G2-2、T09、T12。解除标准是单条成功和每条失败均有可复现请求与副作用断言。

### G2-4：工作台引用回看与备注切片

**用户结果**：在现有 `Workspace.vue` 中查看来源上下文、填写用途/限制、保存后重新读回；待核状态和原因可理解。

前端新增 `CitationUsage` 类型和 adapter，沿用现有来源面板；保存失败保留用户输入，冲突要求重新读取，不做乐观覆盖。来源正文只在服务端允许时显示。

**验收**：空状态、加载、撤权、stale、网络失败、取消无写请求、刷新后读回；`npm run type-check` 与对应组件测试通过。

**阻塞边**：G2-3；组件不得先于真实状态契约自定义第二套枚举。

### G2-5：确认/冻结/导出接真与连续流程

**用户结果**：引用状态变化后不能沿用旧确认或冻结结果；恢复来源并重新确认后才能导出。

把引用用途/限制和当前 SourcePolicy 结果接入 T12–T14 的 `DeliveryInput`/`FrozenSource`，补两章连续流程：保存引用 → 重启读回 → 撤权/重解析 → 待核 → 恢复 → 重新确认 → DOCX 打开编辑保存。

**阻塞边**：G2-3、T12、T13、T14。解除标准是第二人按仓库命令复现，证据明确区分 `PASS/FAIL/NOT RUN/BLOCKED`。

## 5. 实施顺序与每张 PR 的完成线

顺序固定为：G2-1 → G2-2 → G2-3 → G2-4 → G2-5。每张 PR 都包含接口、实现、失败测试、迁移/契约变更（如有）和复现证据；不建立“后端完成后再补前端”的孤立 PR。每张 PR 合并前做两轴 review：

- **Standards**：是否复用现有 seam、迁移、请求封装、幂等和测试约定；是否把复杂度推给调用方；
- **Spec**：是否仍只放行当前授权、当前版本和可定位来源；旧确认/冻结是否被错误复用；失败是否泄露正文或产生假下载。

实现阶段按 `/implement` 的 TDD 节奏逐 ticket 运行：先一个能失败的行为测试，再最小实现，再接真实 adapter，最后运行 `/code-review`。未通过的 ticket 不得标记为完成，也不得用静态样例替代真实验收。

## 6. 交接与验证清单

- 基线与分支：`codex/g2-development-plan`，工作树 `C:\Users\ASUS\.codex\worktrees\g2-plan`。
- Go：`go test ./internal/evidence ./internal/lingdoc/workspacecore ./internal/lingdoc/workspace/... -count=1`；迁移与集成测试单独记录。
- 前端：`cd frontend; npm run type-check; npm test -- --run`（按现有脚本实际支持的参数调整并记录）。
- 场景：正常保存、重试、并发冲突、撤权、重解析、来源消失、旧版本回放、交付阻断与恢复。
- 证据：请求序列、数据库副作用、来源状态/响应体脱敏摘要、测试命令和 `PASS/FAIL/NOT RUN/BLOCKED`；不得把真实密钥或受限正文写入仓库。

## 7. 后续触发条件

只有出现真实案例证明“引用使用记录”无法表达同一依据支撑多个主张、支持与反对并存、跨来源范围或需要候选证据卡时，才重新开 spec，评估引入 `ResearchClaim`/`EvidenceCard`。只有本地资料不足且租户批准外部来源后，才新增 `ResearchTask/ResearchRun`；这些能力不作为本计划的隐含扩展。
