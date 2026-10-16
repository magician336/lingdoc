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

## PR 合并前复评（#175 实现完成）

- 阶段：#172–#175 实现完成、合并前
- Git SHA：`5c92309e3072a0595b937de82a640661476007b8`
- PR：待创建
- 实现范围：Validation Issue 的 Record/Replay typed result 统一使用共享 payload decoder；Replay 保留 Found/not-found 语义并为损坏持久化 payload 提供领域错误标签；剩余 Project/TransferOwner typed result 迁移到 `adaptMutationResult`，删除未引用的重复 decoder helper。
- CodeGraph：在实现 worktree 初始化并完成 `codegraph sync`；仓库已有 Dockerfile 解析警告，不影响本切片 Go 符号索引。
- 验证证据：
  - PASS：`go test ./internal/lingdoc/workspacecore -run 'TestValidationIssueDispositionTypedResults' -count=1`
  - PASS：`go test ./internal/lingdoc/workspacecore -count=1`
  - PASS：`go test ./internal/lingdoc/workspace -run '^TestIssueDispositionRoutePersistsWithoutWaivingDeliveryGate$' -count=1 -v`
  - PASS：`go test ./internal/lingdoc/workspace -count=1 -timeout 180s`
  - PASS：集成分支补充的 DraftCandidate HTTP contract commit `7df1529` 已合入本分支。
  - 工作树中未保留 `docs/08-本轮实施方案/T09-S4-证据.json` 测试副作用。

| 维度 | 权重 | H_L 分项 | F_R 分项 | 实现后证据与剩余问题 |
| --- | ---: | ---: | ---: | --- |
| 复杂性控制 | 20 | 4.1 | 4.0 | 结果 payload 解码和 Project/Validation typed adapter 统一；Replay 的 Found 校验仍需一次业务前置解码，这是明确的特殊状态语义。 |
| 模块深度与边界 | 20 | 4.0 | 3.8 | `workspacecore` 隐藏 JSON 与 Replayed 组装，Workspace HTTP 继续只消费领域结果；跨包 HTTP 契约仍需随 PR CI 复核。 |
| 演化式设计 | 15 | 3.8 | 3.8 | #172→(#173、#174)→#175 的增量迁移和原始 HTTP 方法兼容保留；后续新 mutation 仍需直接采用共享 adapter。 |
| 战略执行 | 15 | 3.8 | 4.0 | 改动限定在 LingDoc Workspace typed mutation 边界，未改变 schema、公开接口名或 WeKnora 宿主模块。 |
| 一致性与显然性 | 15 | 4.1 | 4.0 | 各领域错误标签、Replayed、Found 与 ChangeSet 冲突语义集中且有测试；健康记录与验证命令已同步。 |
| 证据、反馈与恢复 | 15 | 4.0 | 3.8 | workspacecore 全量与 workspace 全量定向回归通过；CI、真实服务和完整前端链路尚未在本分支复核。 |

按协议权重计算，复评为 `H_L 80/100`、`F_R 78/100`，相对实现前基线 `ΔH_L +14`、`ΔF_R +9`，置信度 A（本地自动化证据；CI/真实服务证据待 PR 阶段补充）。

未覆盖风险：审计详情读取和 source-sensitive operation 的内部 JSON 解码属于持久化结构/来源校验路径，不是 typed mutation result adapter；本票未改变其错误契约。CI 尚未刷新，不能据此声明 PR 已通过远端门禁。

## 代码审查修复后复核

- 阶段：代码审查修复完成、PR 合并前
- Git SHA：`cfa1d1e`
- 复核结论：所有 typed result 在调用错误或 payload 解码错误时保留 `Replayed`；ApplyRewrite、CommitWorkingCopy、RestoreWorkingCopy 的 source-sensitive replay 前置解码复用共享 decoder，并仅将损坏的已存 replay payload 标记为 replay error。
- 验证：Windows 与 WSL2 Ubuntu 24.04 均通过 `go test ./internal/lingdoc/workspacecore ./internal/lingdoc/workspace -count=1 -timeout 180s`；`git diff --check` 与 CodeGraph sync 通过。
- 复评：`H_L 82/100`、`F_R 79/100`，相对实现完成复评为 `ΔH_L +2`、`ΔF_R +1`，置信度 A（本地 Windows/WSL2 自动化证据；远程 CI 尚待 PR 门禁）。
- 剩余风险：未运行完整仓库 `go test ./...`、真实服务验收和远程 CI；这些不阻塞本 Spec 的 workspace typed mutation 回归结论。
