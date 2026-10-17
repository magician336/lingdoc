# PR #122 currentness 复审收口（2026-10-16，第三轮）

本轮响应维护者[review 5544886919](https://github.com/magician336/lingdoc/pull/122#pullrequestreview-5544886919)。产品提交 `c7872fdfea07f4214434e35218b49772ab9c07ea`，父提交为原受审d2135b2；main仍为`890578143e4bd829180dda245223e8fb5ae1fc58`。最终受审SHA由原PR当前head与原集中评论绑定，本文所在纯文档提交不改变[机器证据](pr122-main-sync-evidence-20261016.json)中的受测源码子树。保持Draft、CHANGES_REQUESTED，未合并/部署。

## 修复与审查入口

1. `delivery.AccessCurrentnessInput`与`workspace.accessPolicy.deliveryCurrent`双向保留模板ID/版本、有效规则哈希、捕获的ContextRevision及AssetVersions，仍保留Spec逐字段溯源、章节和来源。当前性同时比较实际项目副本身份/内容/规则与上下文；来源构建器复核声明资产版本，不能因Sources为空而略去。
2. ContextRevision从原有一致读事务进入WorkspaceDeliveryInput和冻结DTO，契约新增可选只读字段；旧快照只允许使用其自带的冻结provenance context，绝不以今天的版本补旧快照。缺失身份不能伪装成当前。
3. 实际共享策略装配暴露交付消费者的`contributor`租户下限被当作业务capability导致invalid_request。本轮在唯一适配器映射为`delivery-read`，core检查实时contributor租户下限＋当前项目成员，仅授读取；不授章节write，现有FullWorkflowGuard的人类/Owner门槛保留。降为viewer的旧Owner token被拒绝。
4. 撤权后读取旧快照现在明确返回source_access_denied且不回传冻结正文；不把403包装成普通过期。两份旧合成夹具已补齐捕获的Context/副本身份，并更新为更严格的拒绝断言，没有放宽生产检查。

## 实际验证及来源

五个受影响包最终组合证据全部PASS：workspace、workspacecore、delivery、candidateadoption、access；共654个测试/子测试pass事件、3项PG依赖SKIP。workspace与core按修改重验，其余本轮未变包复用同轮成功输入；不重复跑15包或无改动前端。相同五包vet PASS。契约静态校验PASS：631结构检查，26场景，23步参考轨迹。

新增四个顶层用例及六个独立变化子场景，使用实际SQLite reader/core/source runtime/shared policy/release/export和DOCX：未改动时冻结、导出、下载成功；模板ID/版本、副本内容/规则hash、ContextRevision、资产revision分别变化后失效，不能新建导出或用旧下载入口取字节；组装后冻结前副本变化被拒且不持久化快照。另有双向映射与当前租户降权/无写授权断言。不是stub currentness冒充实际检查，不代表真实PG、浏览器或模型验证。

仓外`acceptance/pr122-main-sync-20261016/currentness-r3`保留首轮反例、invalid_request装配失败、两次旧夹具不全的失败及末次通过；不删失败挑选结果。新head CI须重新触发并按实际结果回原PR，旧d2135b2的十绿只属历史。新增模型调用0、未启停现有服务。

## 协作与范围

#149继续已有成员面板整合；#150的6d18019修复已有八项定向PASS和所查九组CI成功，但000036/000115与RuleSet重号、draft/stabilized/satisfied状态集合仍需整合，本轮未引入；不借currentness补丁扩大首轮复审。#151按原PR接续最新候选，当前已独立复核083e8b8：HTTP写端口及强制事务reader代码修复已确认，精确头八项选定CI用例通过；非事务reader注入拒绝回归与RuleSet使用审计仍待补。所查八组CI为七成功、一运行的阶段快照，不将e41的旧编译失败当当前阻塞，也不套用12c的全绿。完整G1–G7、正式Beta、真实成员试写均未宣告完成；gong当前只留试写空白模板，本轮无人工操作请求。

旧第二轮详细说明与结果见机器证据history_r2及Git历史d2135b2；不将旧前端/UI/Word/模型结果写成此次重跑。完整本候选相对main的文件路径继续列在[文件映射](pr122-current-file-map-20261016.tsv)，本轮仅请聚焦currentness、能力名适配和相应测试/契约。
