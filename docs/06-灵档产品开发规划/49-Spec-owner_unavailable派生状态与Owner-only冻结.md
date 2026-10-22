# Spec：owner_unavailable 派生状态与 Owner-only 冻结

> 上游领域 Spec：[`27-Spec-G5Owner生命周期与转让.md`](27-Spec-G5Owner生命周期与转让.md)（Issue #127）。本 Spec 是它在「Owner 租户身份失效」这一支上的**实现契约**，并顺带补上 #127 冻结清单里尚未存在的归档能力。
> 施工面：PF-4（Issue #328）。本面解开 #127 → #143 这条「行为未稳定」级的实现依赖，解开 G1 立项的一个验收点。

## Problem Statement

项目 Owner 的租户身份可能失效——租户侧停用、降权或移除该成员，而项目里的 Owner 记录不变。此时项目 Owner 专属的动作必须冻结：立项确认、模板升级、ChangeSet 应用、正式导出、归档。普通协作照常：读取、编辑、调研、复核不受影响。

今天这条规则**只在一个地方真正生效**。Owner 是否「还在」这件事，代码里只有「Owner 当前是否还能作为租户成员行动」这一个谓词；它在五项 Owner 专属动作里只有一项被问到，其余四项在 Owner 租户身份失效时**照常放行**——Owner 可以照样立项确认、照样应用变更集，项目照常导出。与此同时，同一条可用性判定在两个地方各写了一遍，两份的条件子集还不一致。

冻结一旦发生，用户看到的**与「这不是你的项目」完全一样**。项目进入一个「Owner 还在、但项目推不动」的状态，而恢复路径要么等租户身份恢复、要么由租户 Owner 指定受让人发起一次转让——这两条路径都没有被产品化地暴露出来，用户只能靠猜。#127 的用户故事明确要求「Owner 专属动作要显示为什么被冻结」，今天没有任何一处能回答这个问题。

## Solution

把「Owner 专属动作现在能不能做」收口成**一个领域级的派生判定**，用领域已有的名字 `owner_unavailable` 称呼它。

判定由 Owner 当前的租户成员状态与项目成员状态派生：**不落库、不新增项目状态、不新增列**。因为是派生的，租户身份恢复即自动解冻，不需要任何修复动作，也不存在「解冻忘了做」的中间态。

五项 Owner 专属动作统一走这道闸门。冻结时拒绝必须**可区分于无权**——调用方能说出「因为 Owner 的租户身份失效」，而不是「你不是 Owner」。领域文档已把 `owner_unavailable` 定为项目上的派生只读标记，因此它在读侧可见、在写侧可归因。

契约侧只做既有形状的自然延伸：权限读出口在五项 Owner-only 能力被冻结时给出「拒绝 + 原因」；另加一个项目级的只读出口，供项目页与成员页显示阻断提示与两条恢复路径。前端据此把入口置灰并给出恢复指引。

同时补上归档能力：它是 #127 冻结清单里的一项，若不存在，「冻结归档」就是一句空话，而归档本身也只是把 Owner 专属动作清单补全。归档与其它四项受同一道闸门管。

## User Stories

1. As a 项目 Owner, I want my Owner-only actions to stop working when my tenant identity lapses, so that a project I cannot personally drive is not silently changed by stale authority.
2. As a 项目成员, I want ordinary collaboration to keep working while the Owner is unavailable, so that the team is not blocked by one person's account.
3. As a 项目成员, I want the Owner-only entries to be visibly disabled, so that I do not click into an action that cannot succeed.
4. As a 项目成员, I want to be told that the Owner is unavailable rather than that I lack permission, so that I know the project is not the problem.
5. As a 项目成员, I want to be told how the project can be recovered, so that I know whether to wait for the tenant to restore the Owner or to nominate a successor.
6. As a 项目负责人, I want the frozen state to appear on the project page, so that the situation is visible to everyone working on the project.
7. As a 项目负责人, I want the frozen state to appear on the members page, so that the team can see who is missing and why.
8. As a QA 负责人, I want the frozen state to be readable from the API, so that I can assert it instead of inferring it from a 404.
9. As a 项目 Owner, I want my project's activation to be frozen while I am unavailable, so that the project does not go live under a responsibility I cannot carry.
10. As a 项目 Owner, I want template changes to be frozen while I am unavailable, so that the project's rules do not drift without my involvement.
11. As a 项目 Owner, I want ChangeSet application to be frozen while I am unavailable, so that no content change lands without the final responsible person.
12. As a 项目 Owner, I want delivery export to stay frozen while I am unavailable, so that the frozen set is consistent across every Owner-only action.
13. As a 项目 Owner, I want archiving to stay frozen while I am unavailable, so that archiving is not the one action that slips through.
14. As a 项目成员, I want to keep editing my own change drafts while the Owner is unavailable, so that my work is not discarded.
15. As a 项目成员, I want to keep commenting and reviewing while the Owner is unavailable, so that review does not stall.
16. As a 项目成员, I want to keep researching and reading while the Owner is unavailable, so that the investigation can continue.
17. As a 项目负责人, I want the frozen state to be derived rather than stored, so that it cannot go stale relative to the tenant's membership.
18. As a 项目负责人, I want the frozen state to clear itself when the same Owner's tenant identity is restored, so that a temporary suspension does not require a new transfer.
19. As a 项目负责人, I want a completed ownership transfer to survive a later tenant restoration, so that restoration does not resurrect the previous Owner.
20. As a 租户 Owner, I want to nominate a successor while the project Owner is unavailable, so that a stranded project can recover without waiting.
21. As a 项目 Admin, I want to be unable to initiate an ordinary ownership transfer, so that ownership never changes silently.
22. As a 受让人, I want to review and accept a transfer before it takes effect, so that ownership is not forced on me.
23. As a 项目负责人, I want an archive request to carry the project version, so that a stale decision cannot archive a project that has since changed.
24. As a 项目负责人, I want archiving to be idempotent, so that a retried request does not produce two archive events.
25. As a 项目负责人, I want archiving to write an audit event, so that the end of a project is traceable.
26. As a 项目负责人, I want an archived project to stop accepting changes, so that the archive means what it says.
27. As a QA 负责人, I want the frozen decisions to be judged at the HTTP boundary, so that the guarantee holds for the real consumer rather than for an internal function.
28. As a QA 负责人, I want the existing Owner-only behaviour to be regression-tested, so that concentrating the judgement does not silently change who may act.

## Implementation Decisions

- **`owner_unavailable` 是派生只读标记**：由 Owner 当前的租户成员状态与项目成员状态派生，不是项目状态值，不落库、不新增列、不新增状态枚举。它只在读侧出现，在写侧表现为「拒绝 + 原因」。
- **判定只有一个入口**：领域里「Owner 专属动作现在能不能做」由 workspacecore 的一个判定负责。判定与它对外的门面必须在同一处；今天判定门面与判定实现分处两地、同一判定还有一份内联复制，这三处必须合并为一处，否则「收口」只是把同一份知识换了三个地方。
- **判定接口的形状**：输入是「项目 + 租户 + 一项 Owner 专属动作」，输出是「可否执行 + 冻结原因 + Owner 身份」。原因是一个可枚举的稳定值，既有原因码的语义不变，只多出 `owner_unavailable` 这一个。
- **冻结清单是可数的五项**：立项确认、模板升级、ChangeSet 应用、正式导出、归档。不引入「能力 × 角色」的通用矩阵——#127 给的是五项具体动作，通用化是猜测性抽象。
- **「Owner 也有权」不等于「Owner 专属」**：现有判定点里有一类是「创建者**或** Owner 可编辑/可评论/可查看转让记录」，它们**不受冻结影响**。收口时必须逐点分类，只对五项动作加闸门；把闸门一把加到全部判定点会误冻普通协作。
- **接入方式是替换而不是叠加**：五个冻结点各自把「是不是 Owner」换成「这个 Owner 专属动作现在能不能做」，而不是在既有判定之后再加一层可用性检查。
- **拒绝必须可区分**：冻结时返回的拒绝要能带上原因，使调用方能区分「Owner 租户身份失效」与「你不是 Owner」。既有 HTTP 状态码与既有错误码语义不变；不新增状态码。
- **读侧两处出口、同一份判定**：权限读出口在五项 Owner-only 能力被冻结时给出「拒绝 + 原因」，供按钮级判断；另有一个项目级只读出口给出 Owner 可用性事实与恢复路径标识，供项目页与成员页的阻断提示。两者读的是同一份判定，不是两套逻辑。派生状态**不挂到项目的领域结构体上**——它是读时派生的事实，不是项目的属性。
- **归档能力**：Owner 专属；带项目版本检查；幂等；写审计；`active` 项目可归档、不能丢弃，`draft` 项目只能丢弃、不能归档（既有语义不变）；归档是终态，归档后项目不再接受变更。归档与其它四项受同一道闸门管，因此「Owner 不可用时归档也被冻结」是清单成立的前提，而不是空话。
- **契约变化的边界**：既有端点、既有操作、既有请求与响应形状不改；新增的是归档端点、权限读出口的一个新原因码，以及项目级只读出口。
- **前端消费**：新增一个 composable 持有 Owner 可用性事实与恢复指引文案，页面与按钮消费它；按钮的置灰条件、提示条的文案与「恢复指引」都来自这一处，不在各页面各写一遍。前端有两处落点是集成步骤而不是面拥有：项目工作台是所有前端面的集成点、不得被任一面独占；影响面板属已交面的文件。其余前端改动落在本面新建的文件里。
- **并发与幂等**：冻结判定是只读派生，不引入新的写路径；归档与既有 Owner 专属动作一样走既有幂等键与项目版本检查。
- **保留 #127 的旁路**：Beta 阶段 `owner_unavailable` 下的转让只能由 WeKnora 租户 Owner 指定受让人并发起，仍须受让人确认后生效；项目 Admin 不能发起普通转让；不开放平台管理员旁路。
- **名称沿用领域词**：代码层使用领域已有的 `owner_unavailable`，不另造第二个词。同一领域概念在文档层与代码层用同一个词，是本仓刚在代码命名收口里付清的那笔债的延续。

## Testing Decisions

- **验证 seam 放在 HTTP 契约层**：权限读出口、项目级只读出口、五项 Owner-only 写操作、归档端点的端到端断言。域内判定只作实现细节，不单独设 seam。
- **好测试的标准**：只断言外部可观察行为（响应码、响应体、持久化副作用、审计事件），不锁定内部函数结构与文件划分；判定被收口到一处之后，测试仍应在 HTTP 边界成立。
- **覆盖派生状态**：Owner 租户身份有效 / 停用 / 降权 / 移除下的取值；同一 Owner 恢复后自动解冻；已完成转让后恢复不回退。
- **覆盖五项冻结**：五项各自在冻结时被拒、在未冻结时放行；并断言冻结期间读取、编辑、调研、复核仍放行。
- **覆盖「Owner 也有权」的语义**：冻结期间，那一类「创建者或 Owner 可编辑/可评论/可查看转让记录」的用法**仍放行**——这是「不得误冻普通协作」的正面证据。
- **覆盖可观测性**：冻结原因在权限读出口、项目级只读出口与写操作拒绝里都能读到，且三处一致。
- **覆盖归档**：Owner 专属、非 Owner 被拒、版本冲突被拒、幂等重试只有一次效果、审计事件写入、归档后不再接受变更、`draft` 项目不能归档。
- **覆盖旁路**：租户 Owner 在项目 Owner 不可用时可发起并被受让人接受；项目 Admin 与普通平台身份不可发起；Owner 本人不可用时不能由自己发起普通转让。
- **回归**：闸门统一后，既有 Owner-only 动作的放行结果可能变化 ⇒ 既有 Owner-only 用例作为基线回归一起跑，并逐条说明差异。
- **复用既有**：既有转让与 owner-transfer 迁移测试、access-status 用例、项目版本与幂等用例、审计用例、路由与契约的双向核对脚本、既有 E2E 场景。

## Out of Scope

- 平台管理员旁路（#127 已定：身份来源、授权边界与审计契约未明确前不启用）。
- 多 Owner、委托临时 Owner、服务账号持有所有权。
- 普通项目成员从 `suspended_by_tenant` 恢复的**显式恢复动作**（普通成员保留原角色作「待恢复授权」，由项目 Owner 或 Admin 显式恢复，属另一条线）。
- 归档后的恢复（归档是终态）。
- 把 `owner_unavailable` 做成持久化状态、可查询的历史标记或事件流。
- 项目成员级「待恢复授权」的批量恢复与提醒。
- 除前端与 HTTP 契约之外的其他消费方（报表、运营视图等）。
- #127 冻结清单之外的动作（例如让 Admin 也能发起普通转让）。

## Further Notes

- 本面解开 #127 → #143 这条 66 条原生依赖里**唯一一条「行为未稳定」级**的实现依赖，修完它，G1 立项就解开一个验收点。
- 本面同时收口一处**命名债**：领域文档与 #127 都用 `owner_unavailable`，代码里只有「Owner 是否还能行动」的谓词、没有这个概念。把领域词带进代码，是本仓刚在代码命名收口里立下的规矩的延续。
- 契约是**发布物**：既有端点与既有形状不变这一点由契约静态校验与路由双向核对脚本守住；本轮新增的归档端点与两个出口必须同时进契约、路由、前端客户端与双向核对，否则门禁会红。
- 前端有两处落点是**集成步骤**而不是面拥有（项目工作台是所有前端面的集成点；影响面板属已交面的文件）。这两处的改动在 PR 里显式标注，归属登记处不把它们记成面拥有。
- 冻结判定收口会**改变既有动作的放行结果**——这是本面有意制造的行为变化，不是回归缺陷；基线回归与逐条说明是交付的一部分。
