# Spec：G4 模板目录与版本治理

> G4 下一阶段切片。固定 `template-demo/1` 继续作为当前 Beta 基线；本 Spec 不重新定义 Beta 首版验收，也不依赖 #120。

## Problem Statement

当前 G4 以固定演示模板读取器作为模板来源。项目副本已经能够隔离和编辑，但系统不能管理多个 TemplateProfile、发布新的 TemplateVersion、撤回版本或解释模板适用范围。随着科研项目和申报方向增加，用户无法可靠选择“哪个模板可以用于我的项目”，管理员也无法安全发布修正版。

## Solution

建立可审计的 TemplateProfile/TemplateVersion 目录。模板编辑者先保存草稿，经过结构、来源、规则和回放检查后发布不可变版本。已发布版本可以被新项目复制；撤回只阻止新项目复制，不改写已有项目副本。严重错误使用显式召回记录和影响范围，历史项目和 ReleaseSnapshot 仍可回放。

## User Stories

1. As a 模板管理员, I want 创建模板档案, so that 同一申报方向拥有稳定身份。
2. As a 模板管理员, I want 创建模板草稿版本, so that 修改不会影响已发布版本。
3. As a 模板管理员, I want 编辑字段、章节、术语和规则引用, so that 模板表达完整要求。
4. As a 模板管理员, I want 看到模板来源、适用范围和整理者, so that 用户不会把内部模板误认为官方要求。
5. As a 模板管理员, I want 在发布前看到结构和规则错误, so that 无效模板不能进入项目。
6. As a 模板管理员, I want 运行回放检查, so that 新版本不会破坏已有项目输入。
7. As a 模板管理员, I want 发布不可变版本, so that 项目可以准确记录使用的版本。
8. As a 项目负责人, I want 按适用范围和状态选择模板, so that 复制的是允许使用的版本。
9. As a 项目负责人, I want 看到模板内容哈希和规则集哈希, so that 项目基线可追溯。
10. As a 模板管理员, I want 撤回版本, so that 新项目不会继续复制已知错误模板。
11. As a 模板管理员, I want 记录召回原因和影响范围, so that 已使用项目可以获得明确处置建议。
12. As a 项目负责人, I want 已有副本不随源模板更新, so that 项目不会被静默改变。
13. As a 审计人员, I want 查看发布、撤回、召回和复制历史, so that 模板治理可复核。
14. As a 并发编辑者, I want 过期发布和编辑返回版本冲突, so that 不会覆盖别人的模板草稿。
15. As a 用户, I want 模板目录加载失败时看到明确状态, so that 我不会误以为没有可用模板。

## Implementation Decisions

- 复用现有模板契约、Workspace ApplicationService、项目模板副本服务、G5 权限判定和审计事务；不让前端直接决定模板状态。
- 新增稳定的 `TemplateProfile` 和不可变 `TemplateVersion` 生命周期：`draft`、`published`、`withdrawn`、`recalled`。
- TemplateVersion 保存内容、来源类型、适用范围、content_hash、ruleset_hash、来源材料、整理者、发布时间和召回信息。
- 发布前必须通过结构校验、规则绑定校验、来源校验、示例回放和哈希计算；服务端计算哈希，不接受客户端哈希作为真相。
- 新项目只能复制当前租户可见且状态为 `published` 的版本；`withdrawn` 和 `recalled` 不能新建副本。
- 源模板版本变化不修改 ProjectTemplateCopy；已有项目继续使用其副本和哈希。
- 召回不删除历史版本；系统发布召回原因、影响项目列表或查询依据和迁移建议。是否升级由项目负责人通过 G3 流程决定。
- 模板目录读取与复制使用项目和租户权限；模板管理员的发布能力不等于读取项目研究原文的能力。
- 所有发布、撤回、召回、复制和失败动作记录操作者、时间、版本、哈希和审计理由；治理写入采用 fail-closed。
- 当前 Beta 仍可使用 `template-demo/1`；本 Spec 的目录上线不能强制改动既有项目或历史验收数据。

## Testing Decisions

- 最高 seam 是模板目录/Workspace ApplicationService 与 HTTP 入口，测试用户可见模板列表、版本状态、发布、撤回、复制和错误。
- 必验成功：创建档案、保存草稿、发布、按范围筛选、复制、历史读取、召回记录和旧副本隔离。
- 必验失败：无效结构发布、规则哈希伪造、重复版本、撤回后复制、越权发布、过期版本发布和审计失败。
- 需要验证 SQLite/PostgreSQL 对称迁移、重启读回、并发发布和幂等重试。
- 前端测试覆盖目录空态、加载失败、版本状态、适用范围、召回提示和复制入口；不复制服务端状态机。

## Out of Scope

模板市场、跨租户模板共享、实时协作、任意拖拽编辑器、自动把外部规范标为官方要求、自动迁移已有 active 项目和 Beta 首版验收。

## Further Notes

该 Spec 完成后，ProjectTemplateCopy 的来源不再只依赖固定演示读取器，但现有 `template-demo/1` 仍保留为兼容基线。
