# Spec-751 开工卡片与验收矩阵 —— 依赖边人工登记接入 AI 候选推荐(冷启动)

- 票:magician336/lingdoc#751(裁决依据 #748 / 地图 #743;关联既有 Spec:#132 G3 依赖图增量)
- 分支:`feat/751-ai-dependency-candidates`(基线:origin/main @ `cb57f5c9`)
- 日期:2026-10-27
- 性质:跨模块(模型适配器 + workspacecore 服务 + HTTP + 前端)+ 审计(模型调用记账),走完整两道门

## 开工卡片

**用户可见结果**
零边/少边项目的「依赖与影响」面板上,用户点「AI 推荐依赖边」→ 服务端收集项目既有信号(正文 `[[source:]]` 引用、CitationUsage/ReviewItems、证据卡 `scope_chapter_ids`、模板字段-章节结构、RouteNode 声明、既有边摘要),调用项目默认生成模型(演示环境为 qwen3.8-flash)产候选边,逐条复核后进入**既有** dependency-candidates 队列;用户在既有候选面板逐条 confirm/modify/reject,确认的候选按既有语义落边。AI 只建议不建边,人工门不放宽。

**范围内**
- `workspacecore`:推荐服务方法(信号收集、复核、进队、幂等预检)+ 窄模型接口 `DependencyRecommendationModel`(照 `AgentModel`/`RuleSuggestionModel` 先例);
- `workspace`:`HostDependencyRecommendationModel` 适配器(模型表选择 → 单次 `Chat`(json_object)→ strict 解析 → 记账,新 source_kind `dependency_recommendation_run`);
- `workspace` HTTP:`POST /projects/:projectId/dependency-recommendations`(human-only,同步,reason 必填,Idempotency-Key 幂等;不用 `/dependency-candidates/recommendations` 子路径,避免与 `:candidateId` 参数段路由冲突);
- 前端:ImpactOpsPanel 登记表单区 + 影响预览零边文案处的「AI 推荐依赖边」动作;新 api client 函数;成功后刷新既有 DependencyCandidatePanel;
- 契约同步(契约测试)与本文档证据回写。

**范围外**(票面边界 + 调研确认)
- 不自动成边、不改 confirmed/suggested 两级语义、不动导出闸门与稳定化条件;
- 正文研究条件变更检测(#753 另行开票);
- 不做异步 run 状态机(templategovernance suggestion 式);MVP 同步执行 + 显式超时,超时频繁再立票;
- 不改既有 `SubmitDependencyCandidateBatch`/decision 行为(只加只读幂等预检查法);
- 零数据库迁移:候选两表(`lingdoc_g3_dependency_candidate_*`)、边表(`lingdoc_g3_records`)、账本表(`lingdoc_model_call_ledger`)均已存在;ledger `source_kind` 为字符串列无枚举约束,新增常量不改表。

**前置依赖**
- 无阻塞前置。模型行为 = 租户模型表(`builtin_models.yaml` + env 或 UI 写入),qwen3.8-flash 实测可用(#750 剧本链路,2026-10-27)。

**真实消费者**
- HTTP:前端 ImpactOpsPanel(Workspace.vue:755 挂载);内部验收台 impactCenterAcceptance 链不受影响;
- 服务端:`HostAgentModel` 同款装配路径(container/lingdoc.go),`recordModelCallRow` 共享记账样板(agent_model.go:318-385)。

**领域不变量与事务边界**
- **AI 只建议不决定**:模型输出经服务端逐条复核(对象 id 必须在信号清单内、版本由服务端补齐——模型不得发明版本、action ∈ 8 合法值、evidence/reason 非空≤2000、confidence 0..1、同元组既有非 rejected 边则丢弃 `edge_already_present`),合法候选才进批次;全部不合法 → 不提交、显式报错;
- **零写入保证**:模型失败/超时/输出不可信时,除账本行(成败都记,`context.WithoutCancel` 写)外无任何业务写入;批次落库沿用既有单事务;
- **一幂等键至多一次模型调用**:提交前先按 `(project_id, actor_id, idempotency_key)` 预检,命中即重放原批次、不再调模型;底层仍走既有 `SubmitDependencyCandidateBatch` 幂等与 UNIQUE 约束;
- **拒绝记忆沿用既有**:`suppressDependencyCandidate` 同 ProjectVersion 抑制已决/已提元组,重复触发不会堆重复候选;
- **审计/记账**:一次触发恰好一行 ledger(成功 `succeeded`/失败 `failed`,含 model_id/provider/run_mode/token/cost_basis/source_kind=`dependency_recommendation_run`);候选决策审计沿用 `dependency.candidate.{decision}`;
- **触发权限**:推荐端点 human-only(机器主体 403,与 decision 端点同款);提交进队的 actor = 触发的人工主体。

**成功 / 失败 / 权限 / 恢复场景**
- 成功:201 返回 `{batch, dropped[], model_run}`;dropped 带逐条原因(`fabricated_object`/`invalid_action`/`edge_already_present`/`overflow`…);候选面板刷新可见新批次;
- 失败:无可用默认生成模型 → 503 类 fail-closed;模型超时/错误 → 显式错误 + ledger failed 行 + 零候选写入;输出坏 JSON/形状不符 → `dependency_recommendation_invalid_output`(502),零写入;合法候选为 0 → 422 类,零写入;以上任一失败后人工登记路径完全可用;
- 权限:机器主体触发 → 403 `human_decision_required`;
- 恢复:失败后换新幂等键重试即可;同键重放拿回原批次(模型不被重复计费)。

**持久化 / 并发 / 审计证据**
- 持久化:批次/候选落既有两表(重启读回走既有 GET 队列端点);账本落既有表(重启读回走既有查询);
- 并发:同项目并发触发(不同键)各成批次互不破坏(既有批次表键含 actor+key);并发窗口内同元组双写属既有抑制机制的已知边界,本票不扩(记入风险);
- 审计:ledger 行 + 既有候选决策审计事件。

**当前风险与未决决策**
- 风险 1(口径澄清,已裁定):票面「确认候选落 suggested 边→possible 影响」与 main 既有实现不符——`decideDependencyCandidateEdge`(impact_dependency_candidate.go:697-751)在 confirm 时同事务落 v1 suggested 后升 v2 confirmed 边,确认后传播为 definite。本票范围外不改该语义,矩阵按实现事实验收(行 3),并在票上留口径澄清评论。
- 风险 2(同步超时):同步触发持连接至多 60s(ctx deadline,env 可覆盖);演示单用户可接受;超时频繁的解除条件 = 立异步 run 票(templategovernance suggestion 模式)。
- 风险 3(信号规模):超大项目信号可能超模型上下文 → 模型 4xx 显式失败;MVP 不截断,记录为已知限制。
- 风险 4(环境):Windows 本机 gojieba cgo init 崩溃 → Go 测试在 WSL2 跑(既有惯例);真实模型链路验证用本地服务 + 主仓 .env 的 DASHSCOPE key。

## 验收矩阵

当前 head:`feat/751-ai-dependency-candidates` @(矩阵先于代码提交,实现后回填)
证据环境:WSL2 Ubuntu-24.04(go 与 CI 同版本)/ Windows(前端 tsx 测试);真实模型链路 = 本地服务 + qwen3.8-flash。

| # | 验收项 | 用户可见入口 | 领域不变量/事务边界 | 成功 | 失败/权限/恢复 | 持久化与并发 | 测试或运行证据 | 状态 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | 开工前门:本卡片+矩阵先行 | 本文档 | 范围/不变量先锁 | 落盘并先于代码提交 | — | N/A | 提交序列(矩阵 commit 在首个代码 commit 前) | PASS |
| 2 | 零边项目触发推荐→候选进队,每条带可追溯依据 | ImpactOpsPanel「AI 推荐依赖边」→ POST `/dependency-recommendations` | 复核后进队;evidence 非空;版本服务端补齐 | 201 `{batch, dropped, model_run}`;队列 GET 可见 proposed 候选(source→target/relation/action/evidence/confidence/model_run_id) | 模型拒绝输出→见行 5/6 | 批次+候选落既有表;重启读回=GET 队列 | core+HTTP 测试(stub 模型);真实链路:本地服务+qwen3.8-flash 实测 | TODO |
| 3 | 确认候选→成边→改研究条件沿边传播(口径按既有实现:confirm=confirmed 边→definite;modify 同) | 既有候选面板 confirm | 不改两级语义(范围外);`decideDependencyCandidateEdge` 既有路径 | 边出现(confirmed,Origin=ai);改 spec 字段→评估经该边出 definite 影响(既有 BFS :541-544 的 confirmed 分支) | reject→无边(既有) | 边落 `lingdoc_g3_records` | 既有决策测试不回归 + 推荐链路端到端断言传播 | TODO |
| 4 | 拒绝记忆:同版本重复触发不复提 | 二次触发推荐 | `suppressDependencyCandidate` 既有;同元组进 suppressed | 第二批不含被拒元组,suppressed 列表可见 | 版本推进后允许复提(既有语义) | 既有候选表 | core 测试:拒绝→再触发→断言 suppressed | TODO |
| 5 | 模型不可用/超时:显式失败,零写入,人工路径不受影响 | 触发按钮错误提示 | 一行 failed ledger;无业务写入 | 503/504 类错误含 failure code;ledger failed 行;同面板人工登记仍成功 | 恢复=换键重试 | ledger `context.WithoutCancel` 写 | core 测试(stub err/超时)+ ledger stub 断言恰一行 | TODO |
| 6 | 输出不可信:编造 id/非法 action/坏 JSON→丢弃或整批拒 | dropped 列表/错误 | id∈信号清单;action∈8 值;strict decode(DisallowUnknownFields+trailing EOF);全弃→不提交 | 部分→合法进队+dropped 带原因;全弃→422 零写入 | 坏 JSON→502 `dependency_recommendation_invalid_output` | 零业务写入 | core 测试:编造 id、非法 action、坏 JSON 三夹具 | TODO |
| 7 | 记账:一次触发恰一行,字段齐;幂等重放不加行 | ledger 查询 | source_kind=`dependency_recommendation_run`;成败都记;run_mode 从模型行推断 | succeeded 行含 model_id/provider/tokens/cost_basis | failed 行含 error_code | 重启读回=既有账本查询 | 适配器测试(recordingModelCallLedger 样板) | TODO |
| 8 | 幂等:同键重放原批次,不重复调模型 | 重复点击(同键) | 预检查批次命中→重放;模型调用计数=1 | `{batch, replayed:true}` | 同键不同内容→既有 conflict | 既有 UNIQUE(project,actor,key) | core+HTTP 测试(stub 调用计数) | TODO |
| 9 | 权限:触发 human-only | — | 与 decision 端点同款 `impactRouteWrite(h,true)` | 人工触发 201 | 机器主体 403 `human_decision_required` | N/A | HTTP 测试(机器 principal 夹具) | TODO |
| 10 | 已有边不重复推荐 | dropped `edge_already_present` | 复核含既有边同元组比对(非 rejected 边) | RouteNode 已落边元组被丢弃并说明 | rejected 边元组可再提 | 只读比对 | core 测试:预置 confirmed 边→触发→断言 dropped | TODO |
| 11 | HTTP 契约与路由注册守护 | 契约测试 | 真 gin seam;响应信封沿用 | 契约文件同步;路由注册不与 `:candidateId` 冲突 | 缺路由→守护红 | N/A | 既有契约测试套件扩展 | TODO |
| 12 | 前端:按钮/失败显式/成功刷新候选面板;readonly 隐藏 | ImpactOpsPanel | 复用全局 reason 必填;幂等键 mutate 样板 | 触发→loading→新批次在 DependencyCandidatePanel 可见 | 失败信息展示;readonly 无按钮 | N/A | runtime fixture 挂载测试(r26ComponentRuntimeFixture) | TODO |
| 13 | 前端 api client 契约 | dependencyCandidates.ts 新函数 | Idempotency-Key+body 幂等;body 不替服务端说话 | 请求形状断言 | — | N/A | vm 沙箱测试(dependencyCandidates.test.js 模式) | TODO |
| 14 | 既有面无回归 | — | 不改既有提交/决策/抑制行为 | workspacecore/workspace 定向包 + 契约套件全绿 | 回归即 FAIL | N/A | WSL `go test` 定向命令 | TODO |
| 15 | 真实模型链路(实测) | 本地服务+演示环境模型行 | 真实 provider+记账 | 触发→真实候选入队;ledger 有真实行 | 网络/配额失败如实记录 | 真实库 | 本地起服+curl 实测,证据贴验证报告 | TODO |
| 16 | 文档同步:矩阵回写+PR body+票面口径澄清 | 本文件/PR/#751 | 证据=当前 head | 全行闭环 | — | N/A | 本文件+PR body+issue 评论 | TODO |

## 验证命令清单(实现后回填结果)

```bash
# Go 定向(WSL2)
wsl.exe -e bash -lc "cd /mnt/d/code_warehouse/Projects/lingdoc/.worktrees/issue-751-ai-depcand && go test ./internal/lingdoc/workspacecore/ -run 'DependencyRecommend' -count=1 -v"
wsl.exe -e bash -lc "cd /mnt/d/code_warehouse/Projects/lingdoc/.worktrees/issue-751-ai-depcand && go test ./internal/lingdoc/workspace/ -run 'DependencyRecommend|DependencyCandidate' -count=1 -v"
wsl.exe -e bash -lc "cd /mnt/d/code_warehouse/Projects/lingdoc/.worktrees/issue-751-ai-depcand && go test ./internal/lingdoc/workspacecore/ ./internal/lingdoc/workspace/ -count=1"
# 前端
cd frontend && npm test   # node:test/tsx;定向:src/api/lingdoc + views/lingdoc 新测试
cd frontend && npm run type-check && npm run build
```
