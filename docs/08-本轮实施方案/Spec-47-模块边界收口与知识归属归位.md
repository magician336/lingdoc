# Spec #47：模块边界收口与知识归属归位 — 实施与证据

> 对应 Spec：[`../../06-灵档产品开发规划/47-Spec-模块边界收口与知识归属归位.md`](../../06-灵档产品开发规划/47-Spec-模块边界收口与知识归属归位.md)
> GitHub Issue：见本文件末尾「发布记录」。
> 状态：开工前（2026-10-20）。本文件承载**文件级证据、命令与结果、决策记录**；Spec 正文只承载契约与判据。

## 开工卡片

```text
Issue / Spec：#47 模块边界收口与知识归属归位
用户可见结果：无新增功能；同一类问题只有一处决定。新增业务错误、变更集状态、
              引用标记语法不再需要多处同步。
范围内：
  段一 集中泄漏（4 个炸弹各一个主人）
    1.1 变更集/变更影响状态词表归位 + 契约补齐全部可达状态
    1.2 章节「已确认」单一谓词 + 失效单一触发点 + 交付侧数据形状补齐
    1.3 业务错误对外形态归位（ADR-0003 §6 的推迟项落地）
    1.4 引用标记语法解析与计数约束归位
  段二 提取共享核心：操作管道（幂等/重放/授权/审计/来源复核/事务）成为真深模块，
        调研域并入主管道
  段三 上下文切分与端口收窄：按上下文切出实现并带走端口；传输面不再持有聚合体
        具体类型；组合根缺能力启动期 fail-closed
范围外：
  - 任何对外行为变化（路由/信封/状态码/错误码/文案/重放元数据/schema/幂等记录格式）
  - 规则集生命周期双状态机与双传输面（独立候选，与 #187/#193 对齐后再定）
  - 组合根其余职责重排（DI 清单、适配器选择、迁移脚本、生命周期管理）
  - 界面改造
前置 Issue / 当前依赖：
  - 无阻塞前置。#164 已关闭，其边界是本 Spec 的输入而非依赖。
  - #246（G3 缺口清单）在途：段三若触及影响中心路由，需先与 #246 对齐。
  - #187 / #193（RuleSet）在途：本 Spec 不触碰其代码，但错误归位会覆盖其错误码口径，
    需在段一 1.3 的「少数口径变更」清单中显式记录。
真实消费者：
  - 领域开发者（internal/lingdoc/workspacecore、internal/lingdoc/workspace）
  - 传输层（internal/lingdoc/workspace 的 8 个注册面）
  - 组合根（internal/container）
  - 对外契约（docs/08-本轮实施方案/contracts/openapi.json）
  - 前端（frontend/src/api/lingdoc/、frontend/src/views/lingdoc/）
领域不变量与事务边界：
  - 身份/幂等/请求体哈希真相唯一（操作管道）
  - 授权、审计、幂等记录、业务写入同事务；来源复核在只读事务内且不跨事务持连接
  - 失败时零业务写入（审计是唯一允许写入）
  - 对外错误码与文案冻结
  - 契约状态集合 = 代码可达状态集合
成功 / 失败 / 权限 / 恢复场景：
  - 成功：各段的目标结构成立且行为逐字不变
  - 失败：非法状态、非法标记、非法迁移被拒
  - 权限：授权拒绝与对象不存在对无权调用方返回同一种对外形态
  - 恢复：幂等重放返回同一结果；重放位与顺序不变
持久化 / 并发 / 审计证据：
  - 幂等记录格式与写入时序不变（段二并入后逐条对照）
  - 审计写入仍与判定同事务
  - 事务锁策略不变
当前风险与未决决策：
  - 决策记录 1：章节确认是否允许章节标识为空（2.2 §14）
  - 决策记录 2：规则集状态类错误最终口径（2.3 §21）
  - 风险：段一 1.2 触及 G3 稳定化判定，需回放 #217/#238–#243 既有证据
  - 风险：段一 1.3 的错误码收口触及已冻结对外契约，必须零变化
```

## 证据基线（开工前取证）

> 取证方式：worktree 本地 CodeGraph 索引（3,611 文件 / 81,590 节点 / 236,646 边）+ 4 路并行静态取证 + 人工复核。脚本与完整清单见 `.scratch/lingdoc-arch-20261020/`（不入库）。

### 规模与热点

| 包 | LOC | 文件 | 测试文件 |
| --- | ---: | ---: | ---: |
| `internal/lingdoc/workspacecore` | 40,805 | 115 | 62 |
| `internal/lingdoc/workspace` | 25,656 | 99 | 61 |
| `internal/container` | 5,072 | 20 | 12 |

最大单文件：`workspacecore/service_facade.go` 3,752 · `workspacecore/g2_service.go` 2,574 ·
`container/container.go` 2,013 · `workspace/http.go` 1,372 · `workspacecore/impact_service.go` 1,260 ·
`workspace/selected_rewrite.go` 1,010 · `delivery/snapshot.go` 974。

近两月改动最频繁：`workspacecore/service_facade.go` 66 次、`workspace/http.go` 51 次、
`workspacecore/service.go` 40 次、`container/container.go` 24 次、`workspace/core_alias.go` 22 次。

### 四个变化炸弹的拆弹点现状

**1. 变更集状态词表** — 变更集状态共 **7 个**取值：`draft` / `assessed` / `applied` / `stabilized` / `rejected` / `stale` / `satisfied`；
写入与比较散落在 `workspacecore` 的 `change_creation_mutation.go:108,151`、
`service_facade.go:2244,2247,2464,2532,2570,2644,2647`、
`change_inverse_lineage.go:87,124,139`、`impact_service.go:1184,1207,1209,1238`、
`release_gate.go:229,230,375`、`evidence_card_delta.go:48`、`context_revision.go:140`、
`agent_action_service.go:1188,1306,1374,2423`、`agent_object_storage.go:376` 等 9+ 个生产文件。
对照正确范例：`workspacecore/impact_model.go:144` `ImpactTaskStatuses`、`:148` `ImpactTaskClosedStatuses`。

**已核实：契约并无缺口。** 四方已经一致地覆盖这 7 个取值：
- 对外契约 `docs/08-本轮实施方案/contracts/openapi.json` → `ChangeSet.status` 枚举为
  `['draft','assessed','applied','rejected','stale','stabilized','satisfied']`；
- 数据库取值约束（见 `internal/lingdoc/workspace/change_set_http_test.go:46` 的建表语句）为同样 7 值；
- 前端类型 `frontend/src/api/lingdoc/workspace.ts:173` 的联合类型为同样 7 值；
- 前端中文标签 `frontend/src/views/lingdoc/Workspace.vue:1540` 覆盖同样 7 值。
**真正的缺口是 Go 侧没有这份声明**——7 个取值以裸字符串散落。交付形态因此是「建立唯一 Go 词表 + 四方漂移断言」，
而不是「补契约」。**本条曾被初稿误记为「契约缺 `stale`/`satisfied`」，已更正。**

附带观察（不在本 Spec 范围，供后续参考）：G3 演示台自己的状态口径与后端不同
（`frontend/src/views/lingdoc/impactCenterAcceptance/impactMatrix.ts:468` 声明「五态，没有持久化 draft、没有 stabilized」），
而后端确实可产出 `stabilized`。这属于演示台口径与生产口径的差异，不在本 Spec 的归位范围内。

**2. 章节「已确认」判定** — 三套条件：
- `workspacecore/impact_service.go:60`：`ConfirmationValid && 版本研究条件修订 == 项目当前研究条件修订`
- `delivery/template_check.go:173-181`：确认标识、章节标识、章节版本标识、研究条件修订、模板版本、确认人非空、确认时间非零
- `workspace/template_governance_runtime.go:786-805`：内联 SQL 查 `lingdoc_chapter_confirmations`（`valid=true`）并再校验章节标识可空

数据形状缺口：`delivery.Confirmation`（`delivery/snapshot.go:92-102`）**没有有效性字段**，
`candidateadoption.Confirmation`（`candidateadoption/confirmation.go:31`）有 ⇒ 交付侧口径**结构上看不到失效**。
失效写入散落：`workspacecore/service_facade.go:3529,3638`、`workspacecore/working_copy.go:329`、
`workspacecore/impact_service.go:642`、`workspacecore/service.go:684,693,706`。

**3. 业务错误对外形态** — 5 张平表：
| 映射器 | 位置 | 分支数 |
| --- | --- | ---: |
| 工作区主路由 | `internal/lingdoc/workspace/http.go:368-495` | 42 |
| G4 治理路由 | `internal/lingdoc/workspace/template_governance_routes.go:409-437` | 11 |
| G2 路由 | `internal/lingdoc/workspace/agent_routes.go:10-25` | 5（其余回落主表） |
| 规则集存储路由 | `internal/lingdoc/rulesetstore/http.go:310-331` | 7 |
| 候选采纳 / 生成路由 | `internal/lingdoc/candidateadoption/candidate_adoption_http.go:213-257`、`internal/lingdoc/generation/http.go:196-260` | 各 1 组 |

可复现的口径分歧：
- `template.ErrRuleSetNotDraft` / `template.ErrRuleSetNotPublished`：
  G4 路由 409 `invalid_state`（`template_governance_routes.go:427-428`）vs 规则集路由 409 `state_conflict`（`rulesetstore/http.go:318-319`）
- `rulesetstore.ErrRuleSetVersionExists` / `ErrRuleSetStateConflict`：
  G4 路由 409 `version_conflict`（`template_governance_routes.go:425-426`）vs 规则集路由 409 `state_conflict`
- 5xx 可重试标记相反：`rulesetstore/http.go:334` 用 `status >= 500` 判可重试；
  `workspace/http.go:492-494` 只对 `request_in_progress`/`dependency_unavailable` 判可重试；
  `workspace/http.go:497-500` 的发送路径恒判不可重试 ⇒ G4 的 503 恒不可重试，与规则集面的 503 相反
- 同错误码不同文案：`workspace/http.go:415,456`「内容已变化，请先读取当前版本。」vs `generation/http.go:248`「输入版本已变化，请重新读取。」
- 502 只在 `workspace/g4_full_http.go:432` 出现
- 已有方向未落地：`workspacecore/impact_service.go:137-142` 定义了「错误自带对外形态」接口，
  全仓**只有 1 个实现者**（`:164-166` `impactTaskSourceUnavailable`）

**4. 引用标记语法** — 8 处独立实现：
| 位置 | 形式 |
| --- | --- |
| `workspacecore/service_facade.go:3571,3578` | 正则 + 计数校验 |
| `workspacecore/working_copy.go:15,22` | 正则 + 计数校验（与上**逐字节相同**） |
| `workspace/selected_rewrite.go:269,607,787` | 正则（与上逐字节相同）+ 两处计数校验 |
| `docx/render.go:44` | 正则（与上逐字节相同） |
| `candidateadoption/candidate_adoption.go:387,391` | 手写字符串扫描 |
| `delivery/snapshot.go:950` | 手写字符串扫描 |
| `generation/service.go:748` | 手写前缀/后缀扫描 |
| `docx/markdown.go:84` | 只做计数校验 |

### 聚合体与接缝现状

- `internal/lingdoc/workspace/core_alias.go:7` `type Service = core.Service`（**类型别名**）：
  传输包声明的 13 个端口全部落到同一个 god struct（245 个导出方法 / 36 个文件）背后。
- 5 个 focused port（项目 11 / 成员 4 / 章节 8 / 模板 6 / 变更集 6）**方法互不重叠**（脚本核验）。
- 传输层持有聚合体**具体类型** 7 处：
  `workspace/agent_context.go:49`、`workspace/full_workflow_guard.go:23,299,327`、
  `workspace/full_workflow_http.go:12`、`workspace/impact_routes.go:13`、
  `workspace/template_governance_runtime.go:32`；
  并以具体参数传入 5 处：`agent_context.go:54`、`full_workflow_http.go:26`、
  `agent_routes.go:83`、`impact_routes.go:78`、`template_governance_runtime.go:75`。
- **一个实例被 8 个消费点持有**：`internal/container/lingdoc.go:52` 造出 `projectService`，
  交给 `NewHandler` 的六个依赖位（`:70-76`）、`NewG2ResearchAdapter`（`:58`）、
  `NewG2ActionExecutor`（`:60`）、`NewTemplateGovernanceRuntime`（`:61`）、
  `NewFullWorkflowGuard`（`:67`）、`NewSelectedRewriteService`（`:80`）；
  `runtime.ConnectProjects(projectService)`（`:54`）反向持有它。
  ⇒ 任何一个面都可以调用任一方法（后门泄漏）。
- 组合根两种相反的失败模式：
  - `workspace/http.go:301-350` 的路由注册**无条件**执行 → 端口为空时首个请求空指针；
  - `workspace/g5_machine_read_http.go:105-109` 端口为空时**静默不注册** → 端点变 404。
  - `workspace/http.go:151-153` 的类型断言**丢弃错误**（`machineReads, _ := ...`）。
- 组合根依赖解析级联：`workspace/http.go:39-161`（123 行），并存遗留适配器与按上下文拆分适配器两套转发层。

### 共享管道现状

- 主管道 `workspacecore/service_facade.go:660-944`（`runOperation`，约 225 行）：
  身份与人工决策门 → 请求体哈希 → 幂等身份 → 缓存变更准备 → 来源敏感只读前置事务
  （授权 + 幂等冲突 + 来源重查）→ 主写事务（租户/项目授权 + 审计同事务 + 运行时闸门
  + 幂等冲突）→ 写入与重放。
  调用面：50 处 / 14 个文件。
- 并行管道 `workspacecore/g2_service.go:134`（`g2Operation`）：自行实现操作者与键校验、
  请求体哈希、`ErrIdempotencyConflict`、事务、审计、幂等记录与投影。调用面 19 处 / 2 个文件。
- 审计双路径：`workspacecore/service_facade.go:435-443` —— GORM 事务内写审计（同事务），
  非 GORM 走 `s.audit.Record`（出事务）；生产 sink `internal/container/lingdoc.go:147-173`
  **吞掉错误并返回 nil** ⇒ `nil` 同时表示「已记录」与「已丢弃」。

## 验证命令

> 基线在 `origin/main @ 5eab412`、集成分支创建后、任何实现改动之前跑出。
> 环境：`go1.27.1 windows/amd64`，`GOCACHE=D:\gocache-lingdoc`，`GOTMPDIR=D:\gotmp-lingdoc`。

| 目的 | 命令 | 结果 |
| --- | --- | --- |
| 工作区领域包测试 | `go test ./internal/lingdoc/workspacecore/...` | **PASS**（83.5s） |
| 工作区传输包测试 | `go test ./internal/lingdoc/workspace/...` | **PASS**（511.5s） |
| 规则集存储 | `go test ./internal/lingdoc/rulesetstore/...` | **PASS**（17.4s） |
| 候选采纳 | `go test ./internal/lingdoc/candidateadoption/...` | **PASS**（3.8s） |
| 生成 | `go test ./internal/lingdoc/generation/...` | **PASS**（3.7s） |
| 交付 | `go test ./internal/lingdoc/delivery/...` | **PASS**（16.2s） |
| 模板治理 | `go test ./internal/lingdoc/templategovernance/...` | **PASS**（20.0s） |
| 路由与集成 | `go test ./internal/router/...` | **PASS**（3.3s） |
| 组合根 | `go test ./internal/container/...` | **BLOCKED（环境）**：`sqlite-vec-go-bindings/cgo` 缺 `sqlite3.h`，构建期失败；未出现业务断言失败 |
| 格式 | `gofmt -l internal/lingdoc internal/container` | **PASS**（无输出） |
| 空白与补丁 | `git diff --check` | **PASS**（无输出） |
| 索引 | `codegraph sync` | 实施改动后重跑 |
| 全量 | `go test ./...` | NOT RUN（受上述环境限制与仓库既有环境依赖测试影响） |

### 改动后在当前 head 上的同一组命令

> 上表是**改动前**的基线（`origin/main @ 5eab412`）。下表是**改动后**在同一组命令上的结果。
> 两表逐项对照，差异为零（`internal/container` 的环境阻断在两边相同）。
> 具体数字见下方「PR 阶段」一节与 PR #285 的 body；本节只给出对照结论。

| 目的 | 命令 | 改动前 | 改动后 |
| --- | --- | --- | --- |
| 工作区领域包 | `go test ./internal/lingdoc/workspacecore/...` | PASS 83.5s | PASS |
| 工作区传输包 | `go test ./internal/lingdoc/workspace/...` | PASS 511.5s | PASS |
| 规则集存储 | `go test ./internal/lingdoc/rulesetstore/...` | PASS 17.4s | PASS |
| 候选采纳 | `go test ./internal/lingdoc/candidateadoption/...` | PASS 3.8s | PASS |
| 生成 | `go test ./internal/lingdoc/generation/...` | PASS 3.7s | PASS |
| 交付 | `go test ./internal/lingdoc/delivery/...` | PASS 16.2s | PASS |
| 模板治理 | `go test ./internal/lingdoc/templategovernance/...` | PASS 20.0s | PASS |
| 路由与集成 | `go test ./internal/router/...` | PASS 3.3s | PASS |
| 组合根 | `go test ./internal/container/...` | BLOCKED（环境） | BLOCKED（环境），相同原因 |
| 全仓编译 | `go vet ./...` | 未跑 | 唯一失败是 `sqlite-vec-go-bindings/cgo` 缺 `sqlite3.h`（既有环境限制），**所有测试文件均编译通过** |
| 格式 | `gofmt -l internal/lingdoc internal/evidence internal/clienterror` | 无输出 | 无输出 |
| 空白与补丁 | `git diff --check origin/main...HEAD` | 无输出 | 无输出 |

> **环境限制说明**：`internal/container` 的构建失败是本机缺少 `sqlite3.h` 导致的 cgo 阻断，
> 与本次 LingDoc 断言无关，按既有规则记 `BLOCKED（环境）`，不与业务断言失败混记。
> 涉及组合根的改动（T10）其行为证据需改由可运行的包与装配测试承担，并在此注明该限制。
> `internal/lingdoc/...` 与 `internal/router` 在本机可正常运行，说明 sqlite3.h 缺失只影响
> 引入 `sqlite-vec` 的宿主包。

## 段一完成情况（2026-10-20）

段一的四个变化炸弹**全部有归属**，但**段一整体尚未完成**：其中「章节已确认判定」这一个炸弹
分两步走——47.02 把有效性**事实**搬到交付侧（本 PR 交付），47.03 把三处判定收敛到**一个谓词**
并把失效写入收成一个触发点（**未做，#271 开放**）。

**本 PR 的范围 = 段一里的四张票**（47.01、47.02、47.04、47.05）。段二（提取共享核心）与
段三（上下文切分与端口收窄）作为后续 PR，已发布为 #259 的子 Issue 并带原生阻塞边。

**跨路由的错误口径归并不在本 PR**：47.05 让**工作区主路由**不再持有业务错误表，并让 45 个错误
各自声明形态；其余 4 张表（`template_governance_routes.go`、`agent_routes.go`、`rulesetstore/http.go`、
`candidate_adoption_http.go` + `generation/http.go`）原样保留，它们对 DR-47-02 的收拢
（`state_conflict` 退役、502 入统一语义表、同码不同文案归一）属 47.06（#262）。

| 票 | 变化炸弹 | Issue | 状态 |
| --- | --- | --- | --- |
| 47.01 | 变更集状态词表 | [#270](https://github.com/magician336/lingdoc/issues/270) | 已关闭 |
| 47.02 | 章节确认有效性事实 | [#260](https://github.com/magician336/lingdoc/issues/260) | 已关闭 |
| 47.04 | 引用标记语法 | [#261](https://github.com/magician336/lingdoc/issues/261) | 已关闭 |
| 47.05 | 业务错误对外形态 | [#272](https://github.com/magician336/lingdoc/issues/272) | 已关闭 |

### 段一前后对照

| 变化炸弹 | 之前 | 之后 |
| --- | --- | --- |
| 变更集状态 | 7 个取值以裸字符串散落在 10 个生产文件的 28 处；Go 侧无声明 | 一份声明（7 个取值 + 闭环子集 + 8 条合法迁移）；Go↔契约↔数据库↔前端四方漂移断言 |
| 章节确认有效性 | 交付侧的确认快照**没有**「是否有效」这个事实，交付侧只能拿周边字段重新推 | 事实随冻结输入搬运到交付侧（不序列化，对外响应与发布摘要不变） |
| 引用标记语法 | 9 处独立实现（4 处逐字节相同的正则、3 处手写扫描、2 处只做计数） | 一个叶子包 `internal/lingdoc/sourcemarker`；结构断言守住「唯一解析实现」 |
| 业务错误对外形态 | 传输层 5 张平表；主路由一张就 42 个 `errors.Is` 分支，漏一个分支就把 400 答成 500 | 45 个业务错误各自声明形态；主路由只剩兜底分支 |

### 段一的四条不变量（结构断言守护）

1. 变更集状态位置不得出现裸字面量（`go/ast` + `go/types` 类型感知，区分其它聚合的同名取值）
2. 引用标记语法只有一处实现（`go/ast` 扫描 `internal/lingdoc` 全部生产文件）
3. 工作区主路由不得按业务错误逐个分支（`go/ast` 扫 `sendError`）
4. 变更集状态与对外契约、数据库约束、前端类型必须覆盖同一组状态

**每条断言都做过阴性验证**：注入违规 → 断言精确报出 → 回滚。

### 尚未开始（后续 PR）

- 段二：47.06–47.10（错误形态跨面归并与旧表删除、共享核心提取、调研域并入、组合根 fail-closed）
- 段三：47.11–47.16（传输面直连收口 ×4、上下文切出第一例、遗留宽口退役）

> **进展更新（2026-10-21）**：以上两段**均已交付**。段二 = PR #293（merge `633ba34`）；段三 = PR #331
> （merge `a01b729b`）。段三的六张票里交付了五张（#266 / #267 / #274 / #275 / #269 已关闭），
> **#268（47.15 成员与 Owner 上下文切出）是段三遗留项**，与段四一起做，父票 #259 保持 open。
> 段三的方案与证据档：[`Spec-47-段三-上下文切分与端口收窄.md`](Spec-47-段三-上下文切分与端口收窄.md)。

## PR 合并前健康度评分（2026-10-20）

```text
评分对象：H_L / F_R
阶段：PR 合并前
Git SHA：2a8a248（**本票代码改动的 head**；其后的提交只改文档，不改代码）
Spec：#47 模块边界收口与知识归属归位
PR：本 PR（范围 = 段一）
评分日期：2026-10-20
取样范围：internal/lingdoc/**、internal/evidence、internal/clienterror、internal/router；
         契约 docs/08-本轮实施方案/contracts/openapi.json；前端 frontend/src/api/lingdoc/
```

| 维度 | 权重 | 二级指标 | H_L | F_R | 结构性不足与证据 | 置信度 | 模块级重构方向 / 回归门槛 |
| --- | ---: | --- | ---: | ---: | --- | --- | --- |
| 复杂性控制 | 20 | 变化放大 | 3.5 | 3.5 | H_L：段一消掉 4 处放大面（变更集状态 28 处→1、主路由错误分支 42→0、引用标记解析 8→1、确认事实搬运）。**仍在**：调研域独立幂等管道 `g2Operation`、5 张错误表共 84 个 `errors.Is` 分支、确认判定 3 套。F_R：宿主层 `internal/evidence` 只改 3 个声明，调用方全在 LingDoc 内 | B | 段二提共享核心 → 幂等/重放/审计一份实现。门槛：五处行为逐条等价对照 |
| | | 认知负荷 | 3 | 3.5 | 聚合体仍 245 个 `*Service` 方法 / 54 个生产文件（CodeGraph 实测，未变）；`core_alias.go` 的类型别名仍让 13 个窄端口落到同一结构体 | B | 段三端口收窄 + 上下文切出。门槛：结构断言 |
| | | 特例与信息泄漏 | 3 | 3.5 | 已消除：变更集状态、引用标记语法、主路由错误形态。**仍在**：RuleSet 双状态机、依赖边状态、ChangeImpact 状态、5 张错误表各自一套文案 | B | 47.06 跨面归并。门槛：跨面一致性测试 |
| 模块深度与边界 | 20 | 接口成本/隐藏收益 | 3.5 | 4 | 新增三个深模块：`sourcemarker`（5 函数/112 行）、`clienterror`（4 函数/89 行）、`change_set_status.go`（声明 + 判定）。**仍在**：聚合体本身是 245 方法的浅门面 | B | 段二段三。门槛：管道单测 + 端口契约测试 |
| | | 信息隐藏 | 3.5 | 3.5 | 四个炸弹的知识已归位；错误形态在 5 个包里仍有 5 份表述 | B | 47.06。门槛：跨面一致性测试 |
| | | 层次错位 | 4 | 4 | `clienterror` 落在**宿主层**（`internal/evidence` 是宿主级而 LingDoc 依赖它，机制必须更下层）；`sourcemarker` 是 LingDoc 内叶子包 | A | 已正。门槛：import 方向断言 |
| | | 耦合与透传 | 2.5 | 3 | `split*Application` 约 40 个一行转发仍在 `workspace/http.go`；`core_alias.go` 别名仍在。**本项最低分** | B | 段三。门槛：结构断言 |
| 演化式设计 | 15 | 候选方案 | 4 | 3.5 | Spec 里比较过 6 种接缝形状并记录取舍；ADR-0003 第六节提供既有裁决 | A | — |
| | | 增量与回退 | 4 | 4 | 4 票各自独立提交、可独立回滚；宿主层改动 = `binding.go` 3 个声明 + 1 个 import | A | — |
| | | 持续重构 | 3.5 | 3 | 段一 4 票已落地且有结构断言守护；段二/三 12 票已发布未开工 | B | — |
| 战略执行 | 15 | 设计投资债务 | 4 | 3.5 | 4 个高杠杆炸弹已还债；顺带关闭 ADR-0003 §6 的推迟项 | A | — |
| | | 路线依赖 | 4 | 4 | 与 G1–G7 路线一致；#187/#193 的错误码口径同步已记录；新包落在宿主层考虑了仓库契合 | B | — |
| | | 变更局部性 | 4 | 4 | 47 文件、+2320/−337；每个炸弹的改动局限在少数包内 | A | — |
| 一致性与显然性 | 15 | 术语文档 | 4 | 3.5 | Spec 47 + 本实施档 + 决策记录（DR-47-01/02/03）；术语沿用 GLOSSARY | A | — |
| | | 契约注释 | 4 | 4 | 新包注释说明「为什么指针」「为什么不序列化」「为什么以正则字符集为准」 | A | — |
| | | 代码语义 | 3.5 | 3.5 | 45 个错误形态统一到一处；5 张表仍各说各话 | B | 47.06 |
| | | 自动约束 | 4 | 3.5 | 4 条结构断言 + 1 份契约快照 + 四方漂移断言；每条都做过**阴性验证**（注入违规 → 精确报出 → 回滚）。F_R 略低：宿主层 `evidence` 的形态无独立漂移断言 | B | 47.06 后补宿主层断言 |
| 证据反馈与恢复 | 15 | 未知发现 | 4 | 3.5 | 新写裸字面量 / 新错误分支 / 新引用标记实现都会被结构断言立刻抓住；契约与代码不同步会被漂移断言抓住 | A | — |
| | | 失败权限恢复测试 | 3.5 | 3.5 | 既有全套测试 + 4 条负性验证。**未把负性验证固化为常驻测试** | B | 把负性验证的检查器留在测试里 |
| | | CI/观测/回退 | 3 | 2.5 | 本机 `internal/container` 因缺 `sqlite3.h` 构建期阻断，**CI 未核验**；`go test ./...` 全量未执行（跑了受影响的 10 个包 + 全仓 `go vet ./...`，它编译了**所有**测试文件，唯一失败就是那个 cgo 环境限制） | B | CI 上核验 |

```text
H_L = 12.8 + 13.8 + 11.5 + 12.0 + 11.6 + 10.5 = 72.2
F_R = 14.0 + 14.8 + 10.5 + 11.4 + 10.8 + 9.5  = 71.0
总分：H_L 72.2 / F_R 71.0
置信度：B
健康等级：60–79「可持续开发，但存在明确治理债务」
与暂定基线差值：ΔH_L = +6.2（基线 66）  ΔF_R = +2.0（基线 69）
关键阻断项：无（最低的二级指标是「耦合与透传」2.5/5，高于 2/5 的阻断线）
```

**最高优先级重构动作（按复杂性降低收益排序）**

1. **段二 · 提取共享核心** —— 操作管道（身份/人工决策门、幂等键与请求体哈希、重放顺序、授权、审计、来源复核、事务边界）提成一个真深模块，上层用例依赖它。**消除** `g2Operation` 与主管道的双实现（变化放大半径 14 个文件）。回归门槛：幂等冲突、请求体哈希、重放顺序、来源重查、人类决策门**五处逐条等价**对照。
2. **47.06 + 47.07 · 错误形态跨面归并与旧表删除** —— 消除剩余 5 张表里的 **84 个 `errors.Is` 分支**（`generation/http.go` 25、`template_governance_routes.go` 22、`candidate_adoption_http.go` 18、`rulesetstore/http.go` 14、`agent_routes.go` 5）。回归门槛：跨面一致性测试 + DR-47-02 少数口径清单逐条列出。
3. **段三 · 端口收窄与上下文切出** —— 让 13 个窄端口背后不再是同一个 245 方法结构体，关闭「一个实例被 8 个消费点持有、任一面可调任一方法」的后门泄漏。回归门槛：结构断言（传输层无聚合体具体类型引用）。

**未覆盖风险**

- 合并前的 GitHub CI 检查未在本机核验（本机缺 `sqlite3.h`，`internal/container` 构建期阻断）。
- `go test ./...` 全量未执行；执行的是受影响的 10 个包 + 全仓 `go vet ./...`（编译了**所有**测试文件，唯一失败是那个 cgo 环境限制）。已核实 `internal/evidence` 的消费者全部在 LingDoc 内且已测。
- 段二/段三未开始：聚合体仍 245 方法；调研域幂等管道仍并行；确认判定仍 3 套；5 张错误表仍各自维护。
- 历史快照的「已确认」语义（`Valid == nil`）待 47.03 的数据核查裁定（登记为 DR-47-04）。
- 本轮**未动**同错误码在不同路由的文案差异（如 409 `stale_input` 的三种说法），按 DR-47-02 留给 47.06。

**验证命令及结果**：见上文「验证命令」节，全部在当前 head `8228b08` 上执行。

## 评审发现与处理（2026-10-20）

PR #285 走了一遍双轴评审（标准轴 / 规格轴），两条轴各自独立。处理结果如下。

### 真实缺陷（已修）

| # | 发现 | 处理 |
| --- | --- | --- |
| 1 | **`Retry-After: 1` 响应头被静默删除**：旧映射表对 `request_in_progress` 设了这个头，删表后新映射器不再设；契约里仍声明该头。**这是本次改动引入的唯一对外回退。** | `clienterror.Form` 增加 `RetryAfter`，由错误自己声明；`request_in_progress` 声明 `RetryAfter: 1`；映射器按声明设置响应头。机械核对脚本也扩展到响应头。 |
| 2 | **「合法迁移集中判定」只做了一半**：迁移表声明了，但生产代码没有调用方，各自仍在拼装迁移条件。 | 声明改为**按状态变更命名**的前置集（`apply` / `reject` / `markStale` / `assess` / `reassess` / `satisfy` / `stabilize` / `editDraft`），生产 8 处守卫改为调用 `ChangeSetStatusCanMove`；另加 `ChangeSetStatusNeeds` 表达「尚未到达目标态」这一**幂等**问题（`!= 已是目标态` 与 `!能移动` 不是同一件事，初版迁移把这两者混同，导致稳定化与闸门用例变红，已修正）。 |
| 3 | **`core.ErrG2Unavailable` 的对外文案写错**：状态码与错误码相同、文案不同，所以既有测试没有变红。 | 由「把旧表解析成 sentinel→形态、与新声明逐条机械比对」发现并修正（`资料或模型能力暂不可用。`）。**这类错误靠人读表发现不了**——45 条形态里有 44 条当时是对的。 |
| 4 | 契约快照少 2 条（`evidence.ErrAssetNotFound`、`delivery.ErrSnapshotNotFound`）。 | 补齐为 45 条。 |
| 5 | 结构断言漏检 `switch change.Status { case "draft": }` 形态。 | 断言补上 `switch` 分支检测，并**做阴性验证**：注入后精确报出该行。 |
| 6 | 测试里自造了一个 `errorsIs` 助手重实现标准库。 | 改用标准库 `errors.Is`。 |

### 范围边界（不是缺陷，已在文档与 PR body 中写明）

| # | 发现 | 处置 |
| --- | --- | --- |
| 7 | DR-47-02 未落地：`rulesetstore/http.go` 仍答 409 `state_conflict`；502 仍私有于 G4 表；同码不同文案仍并存。 | **属 47.06（#262）**，本 PR 未动那 4 张表。已在「段一完成情况」与 PR body 中写明本 PR 的范围是「工作区主路由 + 45 个错误自带形态」。 |
| 8 | DR-47-01 未落地：规则治理运行时仍允许章节标识为空；单一谓词与单一失效点未实现。 | **属 47.03（#271）**。本 PR 只做 47.02 的事实搬运。已在验收矩阵中把该行标为「不在本 PR 范围」而不是 PASS。 |
| 9 | D-1/D-2 数据核查未执行。 | 同属 47.03；已在决策记录里标为待 47.03 裁定。 |
| 10 | 「对外契约零变化」的结论与「引用标记字符集收敛」冲突。 | **结论按精确口径重写**：错误形态 45/45 逐条相同（含响应头）；变更集状态集合不变；**唯一的行为变化是引用标记字符集收敛**（Spec §2.4 决策 23 授权），影响面是「标识含正则字符集之外的字符」的正文——这类正文此前被三条路径接受、被工作区核心拒绝，现在全域一致被拒，交付预检可因此新增阻断项。 |

### 评审未能核实的部分（已记入未覆盖风险）

- 合并前的 GitHub CI 未在本机核验（本机缺 `sqlite3.h`）。
- 前端 `npm test` 在 worktree 里没有 `node_modules`，用主仓库的 `node_modules` 建 junction 后跑通（1461 项 / 1460 通过 / 1 跳过，既有），但**不是** CI 环境。
- `internal/container` 仍无法在本机构建。


## 决策记录



| 编号 | 待裁定 | 状态 | 裁定 | 记录 |
| --- | --- | --- | --- | --- |
| DR-47-01 | 章节确认是否允许章节标识为空 | **已裁定（2026-10-20）** | **不允许为空**：确认记录的章节标识必须与目标章节一致 | 见下方「DR-47-01 裁定」 |
| DR-47-02 | 规则集状态类错误的最终对外口径 | **已裁定（2026-10-20）** | **确认以各路由多数口径为准**；少数口径变更逐条列出并记录理由 | 见下方「DR-47-02 裁定」 |
| DR-47-03 | 5xx 的「可重试」标记口径（收口过程中新发现的分歧，不在原两问内） | **暂定，待确认** | 暂按「4xx 不可重试、5xx 可重试」执行 | 见下方「DR-47-03」 |
| DR-47-04 | 历史冻结快照的「已确认」语义（`Valid == nil`） | **待 47.03 数据核查后裁定** | 暂按「不因缺该事实而否定该确认」处理 | 47.02 交付侧确认快照补齐有效性事实时新发现；待 D-1/D-2 核查后裁定 |

### DR-47-01 裁定：章节确认不允许章节标识为空

**裁定**：确认记录的章节标识必须存在且与目标章节一致；谓词不接受章节标识为空的确认。

**已核查的事实（使裁定无需数据迁移即可落地）**：
- 确认记录表的章节标识在两个数据库上都是非空且带外键：
  SQLite `migrations/sqlite/000020_lingdoc_candidate_adoption.up.sql:30,35`（`chapter_id TEXT NOT NULL` + 外键指向章节表）；
  PostgreSQL `migrations/versioned/000099_lingdoc_candidate_adoption.up.sql:32`（`chapter_id varchar(36) NOT NULL REFERENCES lingdoc_chapters(id)`）。
- 写入侧 `internal/lingdoc/candidateadoption/candidate_adoption_store.go:256` 以输入的章节标识落库，输入来自章节上下文，不会为空。
- 因此「允许为空」**只存在于规则治理运行时的一处宽松判定**
  （`internal/lingdoc/workspace/template_governance_runtime.go:800` 的「章节标识为空或等于目标章节」），
  而非数据形状允许。收口时只需去掉这处宽松，不需要迁移。

**必须区分的两件事**（不得混淆）：
- 「确认记录存在但章节标识为空或与目标章节不符」→ 非法，该确认**不成立**。
- 「章节没有确认记录」→ 交付侧当前**有意**用「没有确认」表达，并通过「阻断快照」把它变成可执行的处理项，而不是隐藏
  （`internal/lingdoc/candidateadoption/delivery_input.go:42-44`：空章节或未确认章节必须到达交付环节，以便持久化一份可处理的阻断快照）。
  这一行为**保持不变**，它与「确认记录存在但标识不符」是两回事。

**落地影响**：
- 谓词要求章节标识相等且非空。
- 规则治理运行时的那处宽松判定收紧为相等。
- 交付预检侧本就要求相等，行为不变。
- 影响解析侧的有效标志改由该谓词给出，因此携带空标识或错配标识的历史确认记录将不再被判为有效。**开票前需核查现网是否存在此类记录**（见「待核查数据」）。

### DR-47-02 裁定：规则集状态类错误以多数口径为准

**裁定**：确认以各路由多数口径为准；少数口径的变更逐条列出并记录理由。

**归并后的两类语义**（状态码两边本就一致为 409，不变）：

| 语义 | 收口前口径 A | 收口前口径 B | 裁定为 | 文案 |
| --- | --- | --- | --- | --- |
| 规则集状态不允许该操作（非草稿态、非发布态） | G4 路由 409 `invalid_state` | 规则集路由 409 `state_conflict` | **409 `invalid_state`** | 当前版本状态不允许此操作。 |
| 规则集版本或基线已变化（版本已存在、状态冲突） | G4 路由 409 `version_conflict` | 规则集路由 409 `state_conflict` | **409 `version_conflict`** | 内容或基线已变化，请先读取当前版本。 |

**裁定理由**：错误码取「该语义在工作区其它位置的既有表达」——「状态不允许操作」在工作区主表用 `invalid_state`（3 处），「版本/基线已变化」用 `version_conflict`（2 处）；`state_conflict` 只出现在规则集路由一处，属少数口径。

**少数口径变更（须在 PR body 逐条列出）**：

| # | 变更 | 影响的路由 | 理由 |
| --- | --- | --- | --- |
| 1 | `state_conflict` → `invalid_state` | 规则集存储路由 | 与 G4 路由及工作区主表的「状态不允许」表达一致 |
| 2 | `state_conflict` → `version_conflict` | 规则集存储路由 | 与 G4 路由及工作区主表的「版本/基线已变化」表达一致 |
| 3 | 对外错误码 `state_conflict` 退役 | 规则集存储路由 | 归并后无独立语义；由上面两类覆盖 |

**影响面提示**：规则集相关在途票 #187、#193 会看到这条错误码变更，需在其验收证据中同步。

### DR-47-03（暂定）：5xx 的「可重试」标记

**新发现的分歧**（不在原两问范围内，故单列）：工作区主表只把「请求进行中」与「依赖不可用」判为可重试；
规则集存储与生成路由把全部 5xx 判为可重试；工作区的另一条发送路径恒判不可重试，导致治理面的 503 恒不可重试，
与规则集面的 503 相反。

**暂定口径**：4xx 一律不可重试，5xx 一律可重试。理由：与「操作失败，请稍后再试。」这一既有文案一致；
两条路由已是此口径；全部写操作带幂等键，重试安全。

**此口径下的少数口径变更**：工作区主表的未知 5xx（`internal_error`）由不可重试改为可重试；
治理面的 503 由不可重试改为可重试。

**状态**：待确认。未确认前，段一 1.3 的其余部分照常推进，**可重试标记这一项单独成票**，不阻塞其余收口。

### 待核查数据（开票前）

| # | 核查 | 用途 | 若发现问题的处置 |
| --- | --- | --- | --- |
| D-1 | 确认记录表中是否存在章节标识与章节版本标识不匹配的记录 | DR-47-01 收紧后这些记录将不再有效 | 需产品确认是否需要数据修复；若不修复，须在 PR 中记录「历史记录按新谓词判为未确认」的影响 |
| D-2 | 确认记录表中是否存在章节标识为空或指向不存在章节的记录 | 同上 | 同上 |

## 合并到 main 时的发现与处理（2026-10-20）

### base 移动与合并

PR #285 提交后 `origin/main` 前进到 `e79f403`（PR #283 审计列宽修复 + PR #286 G3 缺口收口文档），
本分支落后 7 个提交。按「先核对 base 未移动」的约定，这次**核对发现 base 已移动**，因此先把
`origin/main` 合进集成分支再合并，避免用 GitHub 的自动合并把冲突留给评审者。

合并本身是干净的（ort 策略，`service_facade.go` 自动合并），但合并**改变了结论**：本 PR 引入的
两处断裂必须先修，否则合并会静默回退刚落地的新 main 修复。

### 断裂一：对外形态机制被绕过（新 main 的修复会被静默打回）

`origin/main` 的 PR #283 新增了 `auditWriteFailed`：审计行写失败时，它自带
`ClientStatus() / ClientCode() / ClientMessage()`，把「有效请求 + 存储失败」与「业务拒绝」区分开。

但那三个方法是**已被本 PR 退役的私有接口**。本 PR 把传输层改成只认 `clienterror.Presenter`
（`ClientForm() Form`），于是 `auditWriteFailed` 变成了 transport 读不到的形态：**它不会崩、不会报错，
只会掉进兜底 500**——调用方拿到 `internal_error`，而本该命名原因的 `audit_write_failed` 消失了。

合并前 new main 自己的测试**不会变红**：没有任何用例断言渲染出来的 code。

修法：把 `auditWriteFailed` 迁到 `clienterror`（`clienterror.Wrap`），与本 PR 已迁移的
`impact_task_source_unavailable` 同一写法。**对外形态逐字不变**（500 / `audit_write_failed` /
「审计记录写入失败，本次操作未生效，请稍后重试。」），只换声明机制。

守卫（`internal/lingdoc/workspace/client_form_ownership_test.go`）：

- `TestClientPresentationHasOneDeclarationMechanism`：扫描 `internal/` 全部生产文件，
  断言 `ClientStatus` / `ClientCode` / `ClientMessage` 三个方法名**不得再出现**。
  退役的机制一旦复活，守卫立刻报出文件与行号。
- `TestSendErrorRendersTheDeclaredForm`：HTTP 级断言「声明的形态 = 渲染出的形态」，
  补上此前缺的一环——声明对了但被渲染成兜底 500，同样是缺陷。

### 断裂二：前端跨语言漂移守卫指着已被搬走的拥有者（CI 红）

`frontend/src/views/lingdoc/chapterCitations.test.ts` 的「标记规则与服务端逐字一致」用例，
去 `internal/lingdoc/workspacecore/service_facade.go` 里找标记正则。47.04 把这段语法搬到了
`internal/lingdoc/sourcemarker`，前端守卫还指着旧位置，于是读不到正则、断言失败。

这是本 PR 引入的真实断裂：**本地那次前端验证跑在 47.01 的旧 worktree 上**（那时正则还在
`workspacecore`），没覆盖到 47.04 落地后的状态；CI 在最终 head 上报了出来。

修法：守卫改指新的唯一拥有者 `internal/lingdoc/sourcemarker/sourcemarker.go`。
字符集没有变（两端仍是 `[A-Za-z0-9_-]`），变的只是「拥有者在哪」。

### 断裂三：staticcheck S1011（CI 红）

`internal/lingdoc/workspacecore/machine_read_grant.go:210` 的逐个 append 循环被 staticcheck 判为
可换成 append 展开。已改写为 `sourceIDs = append(sourceIDs, sourcemarker.IDs(...)...)`，行为不变。

### 断裂四：Go lint job 被新的「按 diff 分级门禁」整条跳过（门禁漏洞）

`origin/main` 的 PR #287 引入「按 diff 内容分级门禁」（ADR 0005）。它把每个 workflow 的触发范围
限定到**最后一次推送**的 diff。对 `synchronize` 事件，范围是 `before...after`：

```python
if event.get("action") == "synchronize":
    ...
    return f"{before}...{after}", False
```

本 PR 的最后一次推送是「并入 `origin/main`（只含 CI 配置与文档）」，该范围里**没有 `.go` 文件**，
于是 `go-lint.yml` 的 `scope` job 输出 `run=false`，`changes` 与 `golangci` 两个 job 整条被跳过。

**后果**：PR 整体引入的 Go 代码逃过了 lint 门禁。`go-lint.yml` 自己的注释写着
「A missing/invalid base is an explicit failure, never a silent switch to … a successful skipped lint」，
而这条新路径恰好制造了一次**静默跳过**。这不是本 PR 引入的缺陷，但它会让本 PR 的 lint 证据落空。

**本 PR 的处置**：

1. 本地用 **CI 同版本 golangci-lint v2.12.2** 补跑两种口径：
   - **CI 口径**（`--no-config`，即干净 runner 上没有仓库配置文件时的默认 linter 集）：
     `golangci-lint run --no-config --new-from-rev=0eb1b38` → **0 issues**。
   - **本机口径**（主仓库根目录有一份**未入库**的 `.golangci.yml`，启用 `lll`/`revive`/`gofumpt`）：
     59 条（`lll` 42 / `revive` 14 / `gofumpt` 3）。逐条核对后确认：
     `lll` 42 条对应全仓 **7951 行**超长（905 个文件）——是该仓库既有状态，不是本 PR 引入；
     `revive` 的 14 条是「导出变量缺注释」，与仓库既有写法一致。
2. 其中 **gofumpt 的 3 条有 1 处确属本 PR**：4 个文件里把 `clienterror` 的 import 插进了
   标准库 import 组中间。已用 gofumpt 自己改（纯 import 位移，行为中性），
   改后 gofumpt 脏文件集合与 base **完全一致**（21 → 21，本 PR 新增 0）。
3. 这次修复顺带让**最后一次推送含 `.go` 改动**，CI 的 golangci-lint job 因此会真正跑起来，
   本 PR 的 lint 门禁不再依赖本地补跑。

### 阴性验证

四处修法都做了阴性验证：注入违规 → 守卫精确报出 → 回滚。

| 断言 | 注入的违规 | 报出 |
| --- | --- | --- |
| 形态唯一性 | 临时文件声明 `ClientStatus/ClientCode/ClientMessage` | 3 条，逐个带文件与行号 |
| `auditWriteFailed` 形态 | 临时退回旧式结构体声明 | 「审计写入失败没有声明对外形态」 |
| 前端漂移守卫 | Go 侧字符集临时改为 `[A-Za-z0-9_.-]` | 「sourcemarker 的标记正则变了，界面这一侧必须跟着改」 |

### 合并后的验证证据（head 含合并与三处修复）

| 验证 | 命令 | 结果 |
| --- | --- | --- |
| 行为回归 | `go test ./internal/lingdoc/... ./internal/evidence/... ./internal/clienterror/... ./internal/router/... ./internal/container/... -count=1` | 全部 ok |
| 同上 + 真 PostgreSQL | 追加 `LINGDOC_TEST_POSTGRES_DSN`（隔离实例 127.0.0.1:55433） | 全部 ok，含 new main 带来的 `TestPostgresBlockingIssueDismissSurvivesAuditColumnWidth` |
| 静态检查 | `go vet ./internal/...` | 通过（带上 `CGO_CFLAGS` 指向本地 `sqlite3.h` 后，`internal/container` 不再被环境阻断） |
| lint（CI 口径） | `golangci-lint run --no-config --new-from-rev=0eb1b38`（v2.12.2） | **0 issues** |
| 格式 | `gofmt -l internal/lingdoc internal/clienterror internal/evidence` | 无输出 |
| 格式（更严） | `golangci-lint fmt --diff`（gofumpt），与 base 逐文件对照 | 脏文件集合与 base 一致（21 → 21，本 PR 新增 0） |
| 空白 | `git diff --check` | 无输出 |
| 前端 | `tsx --test` | 1463 项 / 1462 通过 / 0 失败 / 1 跳过（既有） |

**注**：`CGO_CFLAGS='-I C:/Users/ASUS/AppData/Local/Temp/lingdoc-sqlite-inc'` 使
`internal/container` 与 `go vet ./internal/...` 在本机可跑通，解除了一直记为
`BLOCKED（环境）` 的限制；CI 不依赖该环境变量。

## 发布记录

| 对象 | 标识 | 状态 |
| --- | --- | --- |
| Spec 文件 | `docs/06-灵档产品开发规划/47-Spec-模块边界收口与知识归属归位.md` | 本分支，未提交 |
| 实施与证据档 | `docs/08-本轮实施方案/Spec-47-模块边界收口与知识归属归位.md` | 本分支，未提交 |
| GitHub Issue | [#259](https://github.com/magician336/lingdoc/issues/259) | 已发布，标签 `ready-for-agent` |
| 前序 Spec | [#164](https://github.com/magician336/lingdoc/issues/164)（应用层传输解耦与幂等结果边界） | 已关闭；本 Spec 接续其边界 |
| 实施分支 | `codex/lingdoc-architecture-improvement-20261020-arch` | 已推送到 `origin`；已并入 `origin/main @ 0eb1b38` |
| PR | [#285](https://github.com/magician336/lingdoc/pull/285) | 范围 = 段一的四张票；合并前已并入 `origin/main` 并修复合并引入的断裂（含一处静默断裂） |

## 取证与实现脚本

工作区本地（`.scratch/lingdoc-arch-20261020/`，不入库）：

| 脚本 | 用途 |
| --- | --- |
| `scan_sizes.py` | 各包 LOC / 文件数 / 测试文件数 |
| `scan_deps.py` | lingdoc 子包 import 依赖图与环检测 |
| `ports_inventory.py` | 传输包端口清单与「端口 ← 具体类型」编译期断言 |
| `port_footprint.py` | 每个 focused port 需要的 `*Service` 方法及其所在文件 |
| `raw_surface.py` | raw JSON 形态在 workspacecore 之外的消费者 |
| `leak_inventory.py` | 变更集状态字面量、引用标记解析点、错误映射行 |
| `changeset_status.py` | 变更集状态词表的地基核验 |

## 已知环境限制

- Windows 缺少 `sqlite3.h` 时，涉及 `sqlite-vec-go-bindings/cgo` 的包会被环境阻断；按既有规则记 `BLOCKED（环境）` 并与业务断言失败分开。
- 全量 `go test ./...` 受既有环境依赖测试影响；本 Spec 的行为证据以定向包测试 + 路由集成测试为准。
