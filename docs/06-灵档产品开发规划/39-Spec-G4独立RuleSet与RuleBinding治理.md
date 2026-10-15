# Spec：G4 独立 RuleSet 与 RuleBinding 治理

> G4 下一阶段切片。目标是把当前嵌入模板副本的规则配置提升为可独立版本化、复用和回放的治理对象。

## Problem Statement

当前规则求值器已经具备白名单、稳定顺序和服务端哈希，但规则主要随模板定义保存。规则集不能独立发布、回放或在不改章节结构的情况下演进；模板、项目副本和规则绑定之间的责任边界也不够清晰。

## Solution

建立独立的 RuleSet、RuleDefinition 和 RuleBinding。RuleSet 以不可变版本保存规则集合；模板版本引用一个明确的 RuleSet 版本；项目副本可以在允许范围内启用、停用或配置绑定，但不能改变执行器安全边界。所有检查、问题和导出快照绑定具体 RuleSet 版本及哈希。

## User Stories

1. As a 规则管理员, I want 创建规则集档案, so that 规则可以独立于模板结构治理。
2. As a 规则管理员, I want 保存规则集草稿, so that 未审核规则不会影响项目。
3. As a 规则管理员, I want 选择白名单规则类型, so that 规则行为可预测。
4. As a 规则管理员, I want 配置规则参数和正反例, so that 规则可以被审阅和回放。
5. As a 规则管理员, I want 发布不可变 RuleSet 版本, so that 检查结果有明确依据。
6. As a 模板管理员, I want 把模板版本绑定到 RuleSet 版本, so that 模板和规则版本关系清楚。
7. As a 项目负责人, I want 在项目副本中启用或停用允许的规则, so that 项目可以表达适用范围。
8. As a 项目负责人, I want 调整允许范围内的规则参数, so that 规则适合本项目。
9. As a 项目负责人, I want 看到不能修改的安全边界, so that 我不会误以为可以上传代码改变判定。
10. As a 写作者, I want 检查结果显示 RuleSet 版本和哈希, so that 问题可以回放。
11. As a 审阅者, I want 旧规则集继续可读, so that 历史交付不会随新规则变化。
12. As a 项目负责人, I want 新规则集生成新的检查结果, so that 旧问题处置不被静默沿用。
13. As a 管理员, I want 撤回规则集版本, so that 新项目或新模板不能继续使用已知错误规则。
14. As a 审计人员, I want 看到规则发布、绑定、覆盖和检查使用记录, so that 判定链路完整。
15. As a 用户, I want 规则配置失败时保留原版本, so that 项目不会落入半更新状态。

## Implementation Decisions

- 复用现有 `template` 包白名单求值器和 DeliveryInput；新增 RuleSet/RuleDefinition/RuleBinding 的领域接口，不在 HTTP 或 Vue 中复制求值逻辑。
- RuleSet 生命周期为 `draft`、`published`、`withdrawn`、`recalled`；已发布版本不可变。
- RuleDefinition 只允许现有安全类型：`presence`、`pattern`、`consistency`、`computed`、非阻断的 `model_assisted`。
- RuleBinding 保存目标类型/范围、参数、启用状态、允许严重度、触发场景、负责人和来源；客户端不能改变执行器、严重度上限或规则身份。
- TemplateVersion 引用明确的 RuleSet 版本；ProjectTemplateCopy 保存绑定快照和项目允许范围内的覆盖配置。
- 服务端重新计算 content_hash/ruleset_hash；问题身份绑定 rule_id、target_ref、target_version、ruleset_hash 和 evaluator_version。
- RuleSet 变更不会原地修改旧检查或 ReleaseSnapshot；当前项目必须重新求值，旧处置仅可在完整绑定仍匹配时回放。
- 规则撤回不删除历史结果；新项目和新模板不能绑定撤回版本，已有项目由 G3 ChangeSet 决定迁移。
- 模型只能提供建议或解释，不能发布 RuleSet、改变严重度、改变问题状态或放行 Preflight。
- RuleSet 发布、绑定、撤回和覆盖属于治理写入，审计失败时业务写入与审计一起失败。

## Testing Decisions

- 最高 seam 是 RuleSet/Template ApplicationService 到 Template Check/Preflight 的调用链。
- 必验成功：独立规则集发布、模板绑定、项目副本覆盖、稳定哈希、同一输入同一结果、历史版本回放。
- 必验失败：未知执行器、任意表达式、阻断 model-assisted、客户端伪造严重度/哈希、撤回版本绑定、旧处置复用到新绑定。
- 跨域测试覆盖 Template Check、Preflight、ValidationIssue、ReleaseSnapshot 和导出门禁。
- 验证 SQLite/PostgreSQL 迁移、并发发布、幂等重试、授权撤回和审计无部分写入。

## Out of Scope

任意脚本规则、用户上传代码、模型自动发布规则、自动调整严重度、模板市场、自动迁移 active 项目和 Beta 首版验收。

## Further Notes

本 Spec 完成后，现有 `Template.Rules` 仍可通过兼容适配器读取；迁移必须保留现有 `template-demo/1` 的规则哈希和历史快照可回放能力。
