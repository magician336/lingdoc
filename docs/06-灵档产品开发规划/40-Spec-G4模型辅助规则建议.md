# Spec：G4 模型辅助规则建议

> G4 下一阶段切片。模型只提供建议和解释，不拥有规则发布、严重度、问题处置或导出放行权。

## Problem Statement

G4 已支持 `model_assisted` 规则的非阻断求值结果，但没有完整的建议流程。用户无法请求规则建议、查看建议依据、接受或拒绝建议，也无法追踪建议对应的模板、RuleSet 和项目版本。

## Solution

提供受控的模型辅助建议。用户明确发起建议请求，系统固定模板、RuleSet、项目 Context Revision、允许资料范围和目标对象。模型返回候选规则或解释，结果标记为 suggestion 并保存运行账本。用户可以编辑、接受或拒绝；接受只创建待审核草稿，仍需规则管理员发布。模型不能直接改变规则、严重度、ValidationIssue、Preflight 或导出状态。

## User Stories

1. As a 模板管理员, I want 请求规则建议, so that 我能发现重复或遗漏的检查。
2. As a 模板管理员, I want 看到建议使用的模板、RuleSet、项目版本和资料范围, so that 建议边界清楚。
3. As a 模板管理员, I want 看到建议的理由和证据, so that 我能判断是否值得采用。
4. As a 模板管理员, I want 编辑建议后保存为草稿, so that 人可以修正模型输出。
5. As a 模板管理员, I want 接受或拒绝建议, so that 模型不会自动改变规则。
6. As a 规则管理员, I want 发布前重新校验规则, so that 建议不会绕过白名单和安全边界。
7. As a 项目负责人, I want 看到建议仅影响草稿, so that 当前检查和导出不被静默改变。
8. As a 审计人员, I want 查看模型、输入范围、输出版本和人工决定, so that 建议可追溯。
9. As a 用户, I want 模型限流、超时或输出无效时保留失败原因, so that 我可以重试或手工处理。
10. As a 用户, I want 旧建议在模板或 RuleSet 变化后标为 stale, so that 旧建议不会覆盖新规则。

## Implementation Decisions

- 复用当前任务助手/Agent 会话基础设施、RuleSet ApplicationService、G5 上下文与审计边界；建议请求使用显式固定范围。
- 建议运行记录模型、工具、提示版本、输入范围、Context Revision、TemplateVersion、RuleSet 版本、耗时和结果摘要。
- 建议结果使用 `suggestion` 状态，不能直接成为 RuleDefinition 或 RuleBinding。
- 接受建议只创建规则草稿或变更集草稿；发布必须经过 RuleSet 的确定性验证、权限检查和人工确认。
- 模型不得设置 blocking 严重度、改变既有规则身份、关闭问题、改变 Preflight 结果或启动导出。
- 无权资料、撤权资料、跨项目资料和敏感资料不能进入建议上下文。
- 规则/模板/项目 revision 变化后，旧建议标记 stale；旧建议可以查看，但不能直接接受到当前版本。
- 限流允许按既有真实模型切换规则记录替代模型；非限流故障不得静默降级为通过。

## Testing Decisions

- 最高 seam 是 RuleSet 建议 ApplicationService/HTTP 入口，测试建议创建、读回、接受草稿、拒绝、stale 和权限。
- 必验成功：建议请求、结构化建议、人工编辑、保存草稿、重新校验和发布前阻断。
- 必验失败：模型直接写正式规则、模型提高严重度、无权资料进入上下文、旧建议覆盖新 RuleSet、空或非法输出被接受。
- 测试记录 run mode、模型、输入范围和审计；Mock 只验证失败和结构处理，真实模型另行记录。
- 前端检查建议标识、上下文范围、接受/拒绝确认、失败恢复和 stale 提示。

## Out of Scope

自动发布规则、自动改变严重度、自动关闭 ValidationIssue、自动放行 Preflight、模型替代专家判断和 Beta 首版验收。

## Further Notes

该切片依赖独立 RuleSet 治理；在 RuleSet 版本和发布边界稳定前，不应把模型建议接入正式模板发布入口。
