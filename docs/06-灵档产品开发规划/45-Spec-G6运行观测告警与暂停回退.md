# Spec：G6 运行观测、告警与暂停回退

> 本 Spec 是 G6 后续的产品级运行能力。现有 Python 门禁、fixture 和 JSON 报告继续作为质量证据工具，不在本 Spec 中重复实现。当前 Beta 文件验收统一使用 Microsoft Word。

## Problem Statement

当前 G6 脚本可以校验脱敏事件和回退观察结果，但产品运行时还不能可靠回答：一次用户操作经过了哪些领域写入和异步任务、哪个版本产生了当前导出、哪个依赖失败、如何暂停受影响范围、怎样切回已验证配置。仅有报告文件无法让值班人员在真实故障中控制流量或完成恢复。

## Solution

建立 LingDoc 运行观测与控制能力。每个请求、异步任务、ChangeSet、ImpactTask、ValidationIssue、Preflight、ReleaseSnapshot、ExportArtifact 和下载审计共享关联链。系统记录脱敏运行事件、指标和告警，并提供按租户、项目、模板、RuleSet 和 runtime mode 的暂停/恢复入口。回退只停止新版本写入或切换已验证配置，保留历史对象和审计；恢复后自动要求权限、当前性和下载复验。

## User Stories

1. As a 值班人员, I want 按 correlation_id 查看一次操作从请求到下载的完整链, so that 我能定位失败阶段。
2. As a 值班人员, I want 按 project_id、template_version、ruleset_hash 和 runtime_mode 筛选运行, so that 我能隔离受影响范围。
3. As a 值班人员, I want 查看请求、任务、ChangeSet、ImpactTask、Preflight、快照、导出和审计状态, so that 我能判断当前对象是否仍可交付。
4. As a 安全责任人, I want 日志不包含正文、原文、令牌和模型输入, so that 观测不会泄露研究资料。
5. As a 值班人员, I want 请求拒绝、权限撤回、异步失败、陈旧结果、DOCX 损失和外部依赖故障产生告警, so that 我能及时处置。
6. As a 值班人员, I want 每个告警绑定暂停、重试、阻止导出、回退或通知动作, so that 告警不是无动作的统计。
7. As a 发布负责人, I want 按租户、项目、模板、规则和运行模式暂停新写入, so that 故障不会继续扩大。
8. As a 发布负责人, I want 暂停只影响新版本写入, so that 历史快照、文件和审计仍可读取。
9. As a 发布负责人, I want 切回已验证配置, so that 新版本故障可以快速止损。
10. As a 发布负责人, I want 通过领域入口执行回退, so that 运维不会直接修改 revision 指针或数据库状态。
11. As a 审计人员, I want 看到谁暂停、谁恢复、影响范围、原因和复验结果, so that 运营动作可追溯。
12. As a 值班人员, I want 恢复后自动运行权限、快照当前性和下载检查, so that 恢复不是只改一个开关。
13. As a 开发者, I want 旧对象在新版本运行期间继续可读, so that 兼容窗口真实可用。
14. As a 值班人员, I want 运行指标包含 P95/P99、队列、重试、陈旧和重复副作用, so that 我能识别退化。
15. As a 产品负责人, I want 查看某个质量窗口的失败、暂停、回退和恢复结果, so that 我能决定继续试用还是暂停。

## Implementation Decisions

- 复用现有 tracing、审计、Workspace ApplicationService、ChangeSet、ImpactTask、Preflight、ReleaseSnapshot、ExportArtifact 和队列观测 seam；新增最小运行事件与控制服务，不复制领域状态机。
- 运行事件至少包含脱敏 tenant/project、correlation_id、causation_id、request_id、task_id、changeset_id、impact_task_id、snapshot_id、export_id、template_version、ruleset_hash、context_revision、target_revision、runtime_mode、状态、错误码、耗时和资源类别。
- 事件链必须支持用户操作 → 领域写入 → 异步任务 → 影响/检查 → 稳定化/Preflight → 快照 → 导出/下载。
- 运行事件与研究正文、SourceAnchor 原文、API Key、访问令牌、prompt/completion 和不必要的模型输入分离；敏感字段在接收边界拒绝或脱敏。
- 告警规则由信号、严重度、动作和责任人组成。动作至少支持通知、重试、暂停受影响流程、阻止导出、回退或人工确认。
- 暂停范围支持 tenant、project、template_version、ruleset_hash 和 runtime_mode；暂停结果由服务端统一判定，前端不能只隐藏按钮。
- 暂停只阻止新版本写入、生成、导出或下载入口；历史快照、历史文件和审计按当前权限继续读取。
- 回退通过已验证配置或逆向 ChangeSet/领域入口完成；禁止直接改数据库指针、revision 或发布文件。
- 回退记录兼容窗口、目标版本、影响范围、操作者、原因和验证结果。恢复后必须重新检查权限、来源当前性、快照和下载。
- 运行指标至少包含请求成功/拒绝/冲突、P95/P99、队列深度、异步失败/重试/陈旧/重复副作用、权限拒绝、撤权拦截、Preflight、导出损失和外部依赖可用性。
- 现有 G6 Python 报告只作为输入校验和证据归档，不得被运行控制服务当作真实事件源替代。

## Testing Decisions

- 最高 seam 是运行控制 HTTP/ApplicationService 到现有领域服务的调用链，测试用户可见事件、告警、暂停、回退和恢复结果。
- 必验成功：事件关联、敏感字段拒绝、告警动作、按范围暂停、旧对象读取、已验证配置切换、恢复后复验。
- 必验失败：跨范围暂停、未授权回退、直接指针修改、历史快照被改写、暂停后新写入绕过、恢复未复验、告警无动作。
- 使用真实 G1–G5 运行路径产生事件；脚本 fixture 只用于契约和脱敏校验，不替代运行时集成测试。
- 验证 SQLite/PostgreSQL 事件持久化、重启读回、并发暂停/恢复、幂等和审计原子性。
- 安全测试覆盖租户、项目、模板、RuleSet、运行模式边界和观测查询越权。

## Out of Scope

完整 Beta 指标决策、试用招募和样本统计、G1–G5 领域对象、通用监控平台、P5 组织审批、Word/WPS 兼容性专项和第二套 RAG/工作流引擎。

## Further Notes

本 Spec 依赖 G5 权限判定、G3 ChangeSet/ImpactTask 和 G4 Preflight 语义。G2 完整证据对象接入后，运行事件必须继续覆盖 EvidenceCard、SourceAnchor 和证据失效传播。
