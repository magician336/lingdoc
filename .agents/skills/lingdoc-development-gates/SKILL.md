---
name: lingdoc-development-gates
description: "按 LingDoc 的 Spec-first 两道门推进 G1–G7 开发和 PR 准备：先锁定范围、验收矩阵和不可变契约，再用当前 head 闭环验证并决定 Draft/Ready。适用于涉及 Spec、数据库、权限、跨模块、并发、审计或真实适配器的变更。"
---

# LingDoc 开发门禁

使用本 Skill 时，先阅读仓库根目录的 [`docs/agents/development-rules.md`](../../../docs/agents/development-rules.md)。它是详细流程、模板和例外边界的唯一来源；`AGENTS.md` 只保留硬门槛。

## 适用范围

对以下工作使用本 Skill：

- 实现或拆分 G1–G7 Spec；
- 修改数据库、权限、跨模块调用、并发、审计或真实适配器；
- 准备、复审或返工相关 PR；
- 判断 PR 是否可以从 Draft 转为 Ready，或是否满足合并条件。

纯文字、换行或单文件机械重命名可以跳过完整矩阵，但仍需说明影响范围并通过现有 CI。

## 工作流程

### 1. 代码前门：锁定目的地

先确认一个主要 Issue 和一个 Spec，并写清楚用户可见结果、范围内/范围外、依赖和真实消费者。

建立验收矩阵。每个 User Story、失败场景和恢复场景至少一行，包含：

- 用户入口；
- 身份、版本、哈希、幂等、锁、审计等领域不变量；
- 成功、失败、权限和恢复结果；
- 持久化、迁移、重启读回、并发和无部分写入；
- 测试、CI、运行报告或人工验收证据；
- `TODO`、`PASS`、`NOT RUN` 或 `BLOCKED` 状态。

在写代码前决定：哪个模块拥有真相，哪个事务保护读取/校验/写入/审计，哪些 reader/writer port 必需，失败时如何零写入和幂等重试。未决规则先记录为决策或 Issue，不用代码猜测。

### 2. 实现门：沿矩阵形成垂直切片

- 每个提交有一个可验证目的；不要把机械清理和业务修复混在一起。
- 先覆盖领域不变量和失败路径，再接 HTTP、前端和导出。
- 跨模块、持久化、并发或审计变化必须补真实 application/HTTP seam；直接 store 测试不能替代生产路径证据。
- 代码、迁移、OpenAPI、Spec、PR body 和测试名称使用同一术语。
- 开发计划完成后运行一次 `lingdoc-health-scoring`，记录风险和未验证证据。存在未决规则时，可用 Wayfinder 建立决策票；决策未解决前保持 Draft。

### 3. 评审前门：只接受当前 head 证据

提 PR 前确认：

- 矩阵每一项都有当前 head 的实现位置和证据；
- 定向测试、构建、lint 和相关集成测试的命令及结果已记录；
- 适用时完成 SQLite/PostgreSQL、迁移、重启读回、失败回滚、并发、幂等、权限、审计和恢复验证；
- 关键行为经过 application/HTTP 或真实交付 seam；
- PR body 的 SHA、CI 链接和结论都指向当前 head；
- 完成 `lingdoc-health-scoring` 的合并前复评。

只要必需项有 `TODO`、`NOT RUN` 或 `BLOCKED`，保持 Draft。绿色通用 CI 不能替代 Spec 证据。

### 4. 评审反馈和合并

将评审意见分为：

- `Blocker`：违反 Spec、事务、权限、数据一致性或缺少当前 head 证据，必须修复；
- `Required`：本轮应完成的契约、测试或文档同步；
- `Suggestion`：记录为后续 Issue，不阻塞当前目的地。

评审发现新的不变量时，先更新验收矩阵，再修改代码。连续两轮出现新的 `Blocker` 时，停止堆补丁，重新切分 Spec、补决策记录或拆 PR。

只有在 PR 非 Draft、评审阻断已解决、当前 head 的必需 CI 全部通过时才合并。只有最后一个交付切片完成全部矩阵项后才关闭 Issue。

## 交付格式

使用本 Skill 完成工作后，报告：

1. Issue/Spec 和本次目的地；
2. 验收矩阵中已完成、未运行、阻塞的项目；
3. 当前 head、验证命令和结果；
4. Draft/Ready/合并建议及其理由；
5. 未覆盖风险和后续决策。

不要把历史 head 的证据写成当前结论。
