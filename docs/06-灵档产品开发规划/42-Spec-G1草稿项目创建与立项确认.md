# Spec：G1 草稿项目创建、协作与立项确认

> 本 Spec 将 G1 的已确认语义转成下一阶段可开发契约。设计背景见 [建项阶段设计](07-建项阶段设计.md) 与 [G1 草稿基线](beta-gap-development-plans/G1-draft-baseline.md)。它不修改历史 G1 验收记录，也不把已有 G1 PASS 当作本 Spec 已完成。

## Problem Statement

科研团队需要从一个尚未成形的想法开始工作。用户可能一次完成建项，也可能先邀请成员、整理研究条件、使用 AI 和保存候选材料，几天后再回来。当前后端已经有 draft、版本、Owner 转让和 activation 基础，但工作台没有完整呈现差异、AI 溯源、成员协作、草稿候选、丢弃恢复和立项确认闸门。

如果这些对象和阶段没有清晰边界，用户会遇到：草稿被误当成正式项目、AI 建议被当成已确认事实、他人修改没有被 Owner 看到、失效的确认页面仍能立项、章节在项目基线成立前出现，以及丢弃和归档含义混淆。

## Solution

提供三段式建项流程：创建记录得到 `draft + Context Revision 0`；草稿阶段保存研究条件、成员、资料关联、调研候选和 AI 候选；Owner 在确认页查看两类差异后完成立项。立项通过 G4 共享规则求值器检查当前模板副本，原子地把项目变为 `active`、建立 `Context Revision 1`、冻结 ProjectSpec、生成章节骨架并写入审计。

草稿候选池只由 G1 管理项目归属、revision 0、层级和基本读写；完整调研任务、检索执行和候选导入由 G2 负责。AI Spec 负责生成字段候选，G1 负责保存候选值、状态、来源和 Provenance，并在确认页展示。

## User Stories

1. As a 租户 active Contributor, I want 创建一个 draft 项目, so that 我可以先保存研究构想。
2. As a 创建者, I want 创建请求幂等, so that 重试不会产生第二个项目。
3. As a 创建者, I want 看到当前模板和副本版本, so that 我知道研究条件字段来自什么模板。
4. As a 创建者, I want 在创建时指定负责人, so that 课题负责人可以接管项目。
5. As a 创建者, I want 在负责人接受前继续作为临时 Owner, so that 项目不会进入无 Owner 状态。
6. As a 受让人, I want 接受 Owner 转让, so that 所有权和原 Owner 降级在一次事务中完成。
7. As a 项目成员, I want 被邀请后立即参与 draft 协作, so that 团队可以从建项开始共同整理。
8. As a 项目成员, I want draft 成员拥有治理和职能角色但没有章节 scope, so that 尚未生成的章节不会产生虚假写权限。
9. As a 项目成员, I want 保存研究条件草稿, so that 我可以离开后继续。
10. As a 项目成员, I want 草稿保存带 project_version 和 spec_revision, so that 并发修改可检测。
11. As a 项目成员, I want 冲突时保留自己的输入, so that 系统不会静默覆盖内容。
12. As a 项目成员, I want 每个字段显示人工、AI 或 AI 后人工修改来源, so that 我知道内容如何产生。
13. As a 项目成员, I want AI 字段默认处于 pending_confirmation, so that AI 推断不会伪装成事实。
14. As a 项目成员, I want 查看 AI 字段的会话、轮次和片段 Provenance, so that 我能回看依据。
15. As a 项目成员, I want AI 候选没有来源时仍能保存为草稿, so that 我不会丢失工作，但系统明确提示溯源缺失。
16. As a 项目成员, I want 对 AI 字段提出质疑, so that 生成候选和人工审查保持分离。
17. As a 项目成员, I want 保存草稿候选池条目, so that 搜索和对话结果不会自动进入正式证据。
18. As a 项目成员, I want 候选池保留层级和 revision 0, so that 立项时不会错误地标记候选为 stale。
19. As a 项目成员, I want 草稿期查看项目资料和候选, so that 立项前的准备工作不会丢失。
20. As a Owner, I want 看到他人修改的字段, so that 我不会确认未查看的协作变更。
21. As a Owner, I want 看到仍由 AI 填写且未经人工修改的字段, so that 我能识别另一类风险。
22. As a Owner, I want 系统记录我查看差异的 project_version, so that 过期确认页不能继续提交。
23. As a Owner, I want 一次确认整个研究条件草稿, so that 不必逐字段重复点击。
24. As a Owner, I want 立项时使用与 G4 相同的共享规则求值器, so that 写作期和立项期判定一致。
25. As a Owner, I want 阻断级模板问题阻止立项, so that 不完整基线不会进入 active。
26. As a Owner, I want 警告和提示显示但不被误解为学术结论, so that 模板检查保持边界。
27. As a Owner, I want 立项成功时得到章节骨架, so that 团队可以开始正式写作。
28. As a 项目成员, I want 立项成功后看到 `active`、`Context Revision 1` 和 `NOT_READY`, so that 下一步清楚可见。
29. As a 项目成员, I want 草稿候选和资料授权在 `0 → 1` 后继续保留, so that 建项不会使准备工作失效。
30. As a 项目成员, I want active 后不能普通保存研究条件, so that 实质修改必须走 ChangeSet。
31. As a Owner, I want 显式丢弃 draft, so that 未成立的构想不会继续出现在普通列表。
32. As a Owner, I want 恢复已丢弃 draft, so that 我可以继续原来的构想。
33. As a 项目成员, I want 区分丢弃和归档, so that 项目生命周期含义不会混淆。
34. As a Owner, I want Owner 不可用时不能立项, so that 失效身份不能完成基线确认。
35. As a 项目成员, I want 非成员访问 draft 得到统一不可见结果, so that 项目存在性不会泄露。
36. As a 审计人员, I want 查看创建、保存、AI 候选、差异查看、转让、激活、丢弃和恢复记录, so that 建项过程可回放。

## Implementation Decisions

- 最高测试和接线 seam 是现有 Workspace ApplicationService 与 HTTP 入口；复用现有项目、模板副本、成员、Owner 转让、版本和审计服务。
- 创建项目时默认得到 `draft`、`current_context_revision=0`、唯一创建者 Owner、项目级模板副本和空章节集合；不创建 Claim、EvidenceCard、ChangeSet 或交付对象。
- 创建者指定负责人时沿用两步 Owner 转让：创建者先成为临时 Owner，创建待接受转让，受让人接受后原子完成 Owner 转移；不新增合并式创建接口。
- 草稿成员邀请仅面向当前租户 active 用户；草稿期的职能角色不携带章节 scope。章节 scope 只能在立项生成骨架后分配。
- `ProjectSpec` 字段保存值、origin、status、Provenance、修改者、时间和版本。`origin` 与 `status` 正交；激活推进确认状态但不删除 AI 来源或人工修改历史。
- AI Spec 负责生成字段候选；G1 提供保存、展示、质疑、差异查看和确认接口。写入 ProjectSpec 的 AI 业务动作需要用户确认，不自动改变项目状态。
- 草稿候选池保存项目归属、候选层级、based_on_revision、来源和创建者。G1 提供基本读写；G2 负责 ResearchTask、ResearchRun、ImportCandidate、SourceAnchor 和后续证据流。
- 草稿切换模板使用现有模板副本迁移预览，保留孤儿值和来源，不静默清空字段；active 项目模板变更走 G3 `template_upgrade` ChangeSet。
- 立项确认前必须完成一次服务端差异读取。读取记录绑定 Owner、project_version、changed_fields、pending_ai_fields 和时间；确认只接受仍与当前版本匹配的读取记录。
- 立项确认只能由当前可用 Owner 发起。服务端重新检查租户身份、项目状态、模板副本、迁移结果、版本和权限。
- 立项闸门调用 G4 共享规则求值器，不在 G1 复制规则判据。阻断级问题阻止激活；warning/info 必须显示但不自动等同于学术判断。
- 激活在一个事务中完成：draft → active、Context Revision 0 → 1、ProjectSpec 基线冻结、字段确认状态更新、模板副本 bound、章节骨架创建、Delivery `NOT_READY`、确认记录和审计写入。
- 激活失败不产生任何部分结果；不允许出现 active 无章节、revision 已为 1 但无确认记录或只创建部分章节。
- `0 → 1` 不使草稿候选自动 stale，也不创建 ChangeSet；后续实质研究条件修改必须走 ChangeSet。
- 丢弃只对 draft 生效，使用 discarded_at 软状态；普通列表隐藏，写入/邀请/检索/激活入口拒绝。恢复仍为 draft/revision 0，不自动恢复撤销的租户授权。
- 所有关键写入使用幂等键和预期版本；创建、成员/Owner 变更、差异读取确认、激活、丢弃和恢复的审计失败采用 fail-closed。
- 前端消费服务端状态，不复制权限、规则或版本判据；必须提供创建、草稿编辑、成员/Owner 状态、差异查看、立项、丢弃和恢复的 loading、empty、conflict、permission、failure 和 recovery 状态。

## Testing Decisions

- 测试通过 Workspace ApplicationService/HTTP 入口观察外部结果、版本、权限、审计和数据库副作用，不测试私有函数或具体表查询。
- 必验成功：创建幂等、模板副本、草稿保存和重启读回、AI provenance、候选池、成员协作、两步 Owner 转让、差异读取记录、G4 规则求值、激活和章节骨架、丢弃/恢复。
- 必验失败：非 active 租户创建、非成员读取、重复创建、版本冲突、过期差异确认、非 Owner 激活、阻断规则激活、Owner 不可用激活、draft 后的越权写入、discard 后写入、恢复越权和审计失败部分写入。
- 并发测试覆盖两个成员同时保存、差异读取后他人修改、Owner 转让同时接受、激活与保存竞争和幂等重放。
- 复用 G1 现有 `project_baseline_test.go`、workspace 版本/幂等测试、G4 evaluator 测试、G5 权限测试和迁移回滚测试模式。
- SQLite 与 PostgreSQL 迁移、重启读回、原子回滚和审计事务需要分别验证；环境不可用时记录 BLOCKED/NOT RUN。
- 前端测试覆盖差异两类列表、AI 溯源、成员/负责人状态、过期确认恢复、丢弃/恢复、网络错误和乱序响应隔离。
- 真实用户验收与 Beta 首版协议分开；本 Spec 只定义 G1 行为，不把 #120 的首版验收结论写入实现状态。

## Out of Scope

完整调研任务和检索 Agent、ImportCandidate/SourceAnchor/EvidenceCard、正式章节编辑器、G3 ChangeSet/ImpactTask 实现、G4 模板目录和 RuleSet 治理、G6 运行质量平台、外部模板市场和 Word 人工验收。

## Further Notes

- 当前代码已覆盖许多后端 G1 语义，但前端仍缺少差异读取、AI 字段填写、草稿候选池、成员/负责人、丢弃/恢复等完整入口。
- 立项确认的关键安全边界是“服务端确认 Owner 看过当前两类差异”，不能把前端自动回传的 `reviewed_project_version` 当作查看事实。
- 本 Spec 发布后可继续拆成 G1-1 创建与协作、G1-2 AI 字段与 Provenance、G1-3 差异读取与激活、G1-4 丢弃恢复与候选交接四个实现任务。
