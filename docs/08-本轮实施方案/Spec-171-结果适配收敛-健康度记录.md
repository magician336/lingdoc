# Spec #171 结果适配收敛：健康度记录

## 实现前基线

- 阶段：Spec 完成、实现前
- Git SHA：`8328624c12d0b8ea9b84f565c30afa227df4b103`
- Spec：[Issue #171](https://github.com/magician336/lingdoc/issues/171)
- PR：待创建
- 取样范围：`internal/lingdoc/workspacecore` 的 typed mutation result、`internal/lingdoc/workspace` 的 ApplicationService/HTTP 适配、相关测试、`docs/README.md` 与项目协议。
- 验证状态：CodeGraph 已同步；实现前尚未运行本次变更的测试，记为 NOT RUN。

| 维度 | 权重 | H_L 分项 | F_R 分项 | 结构性证据 |
| --- | ---: | ---: | ---: | --- |
| 复杂性控制 | 20 | 3.2 | 3.5 | 多个 decoder 重复 JSON 解码、错误标签和 Replayed 组装；HTTP 与应用边界已分开。 |
| 模块深度与边界 | 20 | 3.2 | 3.5 | `workspacecore` 已承载业务规则，但结果转换知识散落在多个入口；未扩散到宿主通用模块。 |
| 演化式设计 | 15 | 3.2 | 3.5 | Issue #171 拆成 #172→(#173、#174)→#175，可增量迁移并保留显式领域结果。 |
| 战略执行 | 15 | 3.5 | 3.5 | 变更限定在 LingDoc 自有 Workspace typed mutation 边界，无 schema/API 扩散。 |
| 一致性与显然性 | 15 | 3.5 | 3.5 | 领域结果命名和 HTTP 契约已有测试；decoder 错误标签仍存在重复实现。 |
| 证据、反馈与恢复 | 15 | 3.3 | 3.2 | 有 ApplicationService/HTTP 测试模式；本切片的 malformed payload、replay 和失败传播尚待实现后复核。 |

按协议权重计算，基线约为 `H_L 66/100`、`F_R 69/100`，整体置信度 B。主要风险是同一结果适配规则在多个入口重复，新增 mutation 结果时会放大维护和错误语义漂移。

## 实现前重构门槛

1. `workspacecore` 私有 decoder 统一 payload 解码和领域错误标签；typed result 仍保留显式领域类型。
2. WorkingCopy 四种操作只共享结果转换路径，不合并业务验证、版本和状态转移。
3. 以 ApplicationService typed mutation 边界覆盖成功、重放、解码失败、权限/版本/依赖/事务错误；HTTP 契约确认响应不变。
4. PR 合并前再次评分，记录 `ΔH_L`、`ΔF_R`、精确验证命令和未覆盖风险。
