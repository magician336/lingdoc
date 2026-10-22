# Spec #49：owner_unavailable 派生状态与 Owner-only 冻结 —— 实施与验收证据档

> 对应正式 Spec：[`../06-灵档产品开发规划/49-Spec-owner_unavailable派生状态与Owner-only冻结.md`](../06-灵档产品开发规划/49-Spec-owner_unavailable派生状态与Owner-only冻结.md)。
> 发布副本：Issue [#391](https://github.com/magician336/lingdoc/issues/391)（工作面 PF-4 / Issue #328 的子票）。
> 执行票：#406（判定内核与变更集应用的冻结归因）、#407（项目级冻结提示与两条恢复路径）、#408（立项确认与模板升级的冻结与置灰）、#409（正式导出的冻结与归因）、#410（归档能力落地并在 Owner 不可用时同样被冻结）。
> 集成分支：`feat/pf4-owner-freeze`。

## 1. 验收矩阵

矩阵行 = Spec 承诺的一条性质。「验证 seam」指**在哪个可观察点**证明它。本面的 seam 在 **HTTP 契约层**：读写出口的端到端断言；域内判定只作实现细节，不单独设 seam。

| # | 验收点 | 验证 seam | 成功 / 失败 / 权限 / 恢复覆盖 | 验证命令 | 证据去处 | 本轮结果 |
| --- | --- | --- | --- | --- | --- | --- |
| AC-49-01 | Owner 是否可用由**一处**判定产出；现存的内联复制被合并掉 | 域内（实现形状）+ HTTP（行为） | 成功：判定可被复用；失败：散落写法被守护检查拒绝；权限：不适用；恢复：不适用 | `go test ./internal/lingdoc/...` | `T49-验证报告.json` | **PASS** — 判定与门面同处 `owner_unavailable.go`；AST 守护 `TestOwnerAvailabilityJudgementHasOneImplementation` PASS，故意注入内联副本后守护转红点名该文件，删除后转绿 |
| AC-49-02 | 判定区分「Owner 专属」与「Owner 也有权」，后者在 Owner 不可用时仍放行 | HTTP | 成功：四类「Owner 也有权」路径放行；失败：Owner 专属路径被拒；权限：覆盖；恢复：租户恢复后放行 | 同上 | 同上 | **PASS** — 四处「Owner 也有权」走 `OwnerMayAct`（不读可用性）；`TestOwnerUnavailableDoesNotFreezeOrdinaryCollaboration` / `TestDraftCollaborationSurvivesTheOwnerFreeze` / `TestActiveCollaborationSurvivesTheOwnerFreeze` 全 PASS |
| AC-49-03 | `owner_unavailable` 是**派生**的：租户身份恢复后自动解冻 | HTTP | 成功：停用→拒绝、恢复→放行，无修复动作；失败：残留状态导致仍被拒；权限：不适用；恢复：覆盖 | 同上 | 同上 | **PASS** — `TestOwnerAvailabilityIsDerivedRatherThanStored`（Project 无新字段、无新列）；三条「停用→拒绝、恢复→放行」用例 + 转让后不回退用例全 PASS |
| AC-49-04 | 五项 Owner 专属动作各自被冻结且拒绝可归因 | HTTP | 成功：每项被拒并带原因；失败：任一项仍放行；权限：覆盖；恢复：恢复后放行 | 同上 | 同上 | **PASS** — 立项确认 / 模板升级 / ChangeSet 应用 / 正式导出 / 归档五条用例各一条，全 PASS；既有 404 + `not_found` 不变，原因只进可选 `details.reason_code` |
| AC-49-05 | 冻结期间普通协作照常 | HTTP | 成功：读取、编辑、调研、复核放行；失败：误冻；权限：覆盖；恢复：不适用 | 同上 | 同上 | **PASS** — 四包整包回归全 ok（workspacecore 43.450s / membership 3.303s / workspace 200.743s / router 16.153s），相对基线零新增失败 |
| AC-49-06 | 冻结原因在读出口、写拒绝与项目级出口三处一致可读 | HTTP | 成功：三处同因；失败：任一处缺原因或口径不同；权限：不适用；恢复：不适用 | 同上 | 同上 | **PASS** — 写拒绝 `details.reason_code`、权限读出口 `reason_code`、项目级只读出口 `reason_code` 三处同源（同一 `OwnerAuthorityOf`），各自有用例 |
| AC-49-07 | 前端入口据冻结原因置灰并给出恢复指引 | 前端 + HTTP | 成功：入口置灰且文案含原因与两条恢复路径；失败：仍可点击或文案缺失；权限：不适用；恢复：恢复后自动解除 | `cd frontend && npm test` | 同上 | **PASS** — `useOwnerAvailability.test.ts` 7/7 pass + `workspaceOwnerFreeze.test.js` + `ProjectArchivePanel.test.ts`；前端全量 type-check exit 0、1642 tests / 1641 pass / 0 fail / 1 skipped |
| AC-49-08 | 归档：Owner 专属、幂等、版本检查、审计、终态、`draft` 不可归档 | HTTP | 成功：归档生效且只生效一次；失败：非 Owner / 陈旧版本 / 重复请求被拒；权限：覆盖；恢复：不适用（终态） | 同上 | 同上 | **PASS** — 八条归档用例全 PASS；迁移 sqlite/000053 与 versioned/000132（000052/000131 已被 PF-3 占用） |
| AC-49-09 | 既有契约与端点零变化（只允许新增归档端点、新原因码、项目级只读出口） | 契约校验 + 路由双向核对 | 成功：校验通过；失败：任一形状漂移即红；权限：现有权限用例覆盖；恢复：回到改名前的契约即通过 | `python docs/08-本轮实施方案/contracts/validate_artifacts.py`；`python scripts/ci/check_lingdoc_routes.py` | `contracts/validation-result.json` | **PASS** — 契约校验 exit 0（paths 152 / operations 178），路由双向核对 exit 0；既有 404 / `not_found` 与既有 `capability_or_scope_denied` 语义均未变 |
| AC-49-10 | 闸门统一后既有 Owner-only 动作的放行结果变化被逐条记录 | 回归 | 成功：既有 Owner-only 用例作基线回归跑完并逐条说明差异；失败：出现未说明的差异；权限：覆盖；恢复：不适用 | `go test ./internal/lingdoc/...` | `T49-验证报告.json` | **PASS** — 四条差异逐条记录（可用时放行不变 / 不可用时新增冻结拒绝 / 非 Owner 既有拒绝不变 / 非 Owner-only 能力的既有 reason_code 不被吞掉） |

**落证声明**：本档矩阵的十行在**当前 head**（`eae2d246`）上全部为 **PASS**，证据逐条落在 `T49-验证报告.json`。本档初建时（实现开始前）十行均为 NOT RUN —— 那份骨架没有保留为现状，本节记录的是最终状态。证据之后的提交只改文档与证据，代码面逐字未变（`git diff eae2d246 <最终 head> -- '*.go' '*.ts' '*.vue' '*.js' '*.sql'` 为空）。

**本轮修掉的评审发现**：权限读出口曾对**任何**被拒能力都报 `owner_unavailable`，把「你无权」误标成「Owner 失效」，吞掉既有 `capability_or_scope_denied` 的语义。修复提交 `7243d6d2`，红验证（还原旧行为后新断言转红）见 `T49-验证报告.json` 的 `known_limits`。

## 2. 批次与执行票

| 票 | 内容 | 阻塞于 | 交付 |
| --- | --- | --- | --- |
| #406 | 判定内核与变更集应用的冻结归因 | 无 | Owner 可用性收口到一处 + 派生 `owner_unavailable` 与原因；变更集应用/驳回冻结并可归因；前端两个入口置灰 |
| #407 | 项目级冻结提示与两条恢复路径 | #406 | 项目级只读出口 + 项目页/成员页阻断提示与恢复指引 |
| #408 | 立项确认与模板升级的冻结与置灰 | #406 | 草稿期两项 Owner 专属动作冻结并归因；工作台入口置灰 |
| #409 | 正式导出的冻结与归因 | #406 | 导出拒绝可归因；前端无导出入口，本票到 HTTP 契约层为止 |
| #410 | 归档能力落地并在 Owner 不可用时同样被冻结 | #406 | 归档操作 + 契约端点 + 前端入口 + 冻结 |

**依赖边 ≠ 施工顺序**：#407–#410 语义上互不依赖，但都碰同一个 Go 文件与路由文件，施工时是串行的。

## 3. 两处「集成步骤」而非面拥有

- **项目工作台**：所有前端面的保留集成点，任何面都不得独占它；那里有立项确认与模板相关的入口。
- **影响面板**：属 PF-1 已交面的文件；那一面已交面、没有并行竞争者。

这两处的改动在 PR 正文里显式标注，**不记进登记处的路径集**。

## 4. 证据约定

- 每票一个分支、一个 worktree（基于集成分支）、一个实现者子代理；合入集成分支由合并者子代理完成。
- 验证证据落 `docs/08-本轮实施方案/T49-验证报告.json`。
- **不贴进 issue**，不在 PR body 里贴大段日志。
- 改名与新端点必须声明**对外契约零变化**，并附契约校验与路由检查的结果。
- 本机是 Windows：`internal/agent/tools` 与 `internal/application/service` 有 **11 处既有环境失败**（缺 `/bin/bash`、Windows 路径、临时目录文件锁），`cmd/server` 等 **4 个包缺 `sqlite3.h`** 无法编译（跑 go 命令前需设 `CGO_CFLAGS=-IC:/Users/ASUS/.sqlite-headers`）—— 这两类都是**既有环境限制**，须与本轮失败区分开，逐条与基线对照。
- `internal/router` 包很慢（整包 16s 定向、整包回归更久），且对磁盘争用敏感：与其它 `workspace.test` 并跑时会逼近 Go 默认的 10 分钟超时。回归时优先用 `-run` 精确匹配本面用例。

## 5. 本轮踩到并修掉的三个夹具/schema 缺陷

这三条不是设计问题，是「加东西就会遇到」的那类，留在档里免得下一面重踩：

1. **夹具的迁移清单会停在旧迁移点。** #406 的 owner 冻结夹具手写迁移清单，停在 `000041`；PF-3 在 main 上给审计表加了 `target_version` / `project_revision` 两列（迁移 `000052`）之后，夹具建不出这两列，**任何带审计的写入都以 500 结束** —— 被测的冻结拒绝根本没机会发生。修法：清单补上 `000052`（提交 `db9e884b`）。
2. **迁移编号是跨会话共享的串行资源。** #410 原用 sqlite `000052` / versioned `000131`，而这两个号已被 PF-3 占用 ⇒ 同目录同编号不同内容 = add/add 冲突。修法：取下一号 `000053` / `000132`（两套序列独立）。
3. **同一份迁移清单会散成多份副本。** 三票各自在**自己的**测试文件里补了 PF-3 那条迁移（幂等、互不冲突，但迁移路径散在四处），下一次加列要改四处。修法：清单只留在共享夹具一处，各票的幂等补丁删除（合并收尾提交 `7b5ca42c`）。
