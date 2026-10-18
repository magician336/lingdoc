# 四 PR 实现与剩余工作（2026-10-16，currentness续记）

当前#122产品提交`c7872fdfea07f4214434e35218b49772ab9c07ea`，其上纯文档提交为PR最终受审head；main=`890578143e4bd829180dda245223e8fb5ae1fc58`。所有历史分数、UI/Word或模型证据保留原版本。本轮不新建PR、不合并/部署。

| PR | 已复核范围 | 继续工作与归属 |
|---|---|---|
| #122 | 原五域限定工程/合成结论保留。本轮双向冻结版本、真实currentness、实时租户读取门槛和拒绝撤权内容回传修复；五包/vet/契约通过 | 请magician336按[第三轮复审单](pr122-main-sync-review-20261016.md)审新head。旧CI不套用，仍Draft/CHANGES_REQUESTED |
| #149 | 原作者确认复用ProjectCollaborationPanel，旧PR头4e83709 | 不回挂ProjectMembers；是否被替代由维护者确认，不重复施工 |
| #150 | 固定6d18019：原满足态/正式迁移/Write端口修复，当前头八项选定PASS；所查九组PR CI均success | 原作者维护当前独立切片，维护者复审；与#122整合时本分支统一迁移号及draft/stabilized/satisfied并集，不能直接cherry-pick；本轮未接入 |
| #151 | 固定083e8b8：HTTP写端口、强制事务reader代码修复；本头八项选定CI用例PASS，所查八组CI阶段快照七成功一运行 | 原作者补非事务reader注入拒绝回归及Check/Preflight/ReleaseSnapshot/Export独立使用审计；精确TemplateVersion引用、override、生产适配器全链/PG/recall审计按#140继续开放。e41失败仅历史，仍Draft/CHANGES_REQUESTED |

#150原[集中评论](https://github.com/magician336/lingdoc/pull/150#issuecomment-6190943791)、#151原[集中评论](https://github.com/magician336/lingdoc/pull/151#issuecomment-6191096308)保留精确源/CI模式。迁移组合已在隔离SQL副本复现两种顺序的CHECK冲突；原单PR通过不等于组合通过，未直接接入#122，也不代维护者解除REQUEST_CHANGES或关闭Spec。

完整G1–G7与正式Beta仍按#144依赖接续，真实试写只保留[空白准备模板](team-real-writing-checklist-20261015.md)。不再要求gong当前选课题、排时段或提供资料；需要人工操作时集中报告。旧950入口仍是原运行候选，不与新源码或CI混作同一版本。
