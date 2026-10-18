# G4 独立运行验证与 MCP CI 夹具修补（2026-10-15）

## G4 当前证据

已有二进制与当前前端产物在独立 SQLite、文件目录和 18110/5220 上运行，模型明确禁用。原 18090/5210、主树T12及文件未改。真实鼠标/键盘已完成：模板档案创建、20条服务器登记正反例核验后的规则发布、模板发布、已发布目录创建新项目、撤回，以及独立发布基线分支的召回。

新项目是实际 UI POST 创建；正常只读回读证明项目副本内容/规则哈希等于发布版本。撤回后再次实际 UI 创建被拒绝、未产生项目；已有副本整个对象不变。召回保留原因、迁移建议和影响项目，正常读回包含该新项目ID；已有副本整个对象仍不变。

种子仅预置合法待审 RuleSet 和精确内置模板草稿，使用正常账号接口；**不把 API 准备当 UI 保存草稿证明**。发布、撤回、召回均由页面执行，服务端自己计算哈希并执行已登记样例。没有手改数据库、伪造登记ID、绕过权限或修改规则严重度。新格式/版本需要独立的真实复核样例，本轮不假装覆盖所有模板类型。

原始失败保持：首轮未展开治理折叠区便填字段；另一轮登录输入在表单挂载完成前丢失、未发登录请求；复制后的回读脚本把 items 外层当数组。依赖动作停止，后续只补缺项，不重复成功发布或项目创建。窄范围的有效步骤由 `concentrated-proof.json` 汇集，不能将包含这些失败的原始脚本改称总体PASS。

召回与撤回是 published 的两个终态分支；服务端不允许从 withdrawn 继续召回，本轮以发布时一致快照另建独立召回实例，**没有回滚活跃测试库**。撤回实例已按端口/PID/程序/UTC启动时间确认归属后停止，数据保留；召回实例供后续G6隔离验证后再停止。实际截图可读，但影响项目UUID列表过密，须折叠展示；不借目录权限读取研究原文猜标题。

证据根目录：`acceptance/full-workflow-first-pass-20261013/g4-runtime-20261015` 与 `g4-recall-20261015`。这证明指定合成模板链，仍不是完整G4所有场景、PostgreSQL、真实课题组或人工最终验收。

## 当前 CI 失败及修补

342d0df 的其余五条CI成功，App的唯一失败为 `TestMCPToolApprovalConcurrentColumnPatches`：共享内存SQLite多连接同时upsert报告 `database table is locked`。实际Lite `internal/container/container.go` 使用WAL/busy_timeout及 `SetMaxOpenConns(1)`；测试夹具与运行池不一致。修补仅使夹具采用真实单连接池，并在cleanup关闭连接；**保留两个并发goroutine、每次错误检查与 Enabled/RequireApproval 两列最终断言**，不加跳过、睡眠或重试来遮掩结果。

固定CGO/GCC离线工具链：`go test -mod=readonly -p 2 ./internal/application/repository -run TestMCPToolApproval -count=50` PASS，3.959秒；受影响repository全包单轮PASS，5.019秒。证据 `ci-mcp-pool-20261015/`。业务权限、UpsertPolicy与当前后端未修改；新远端CI必须按随后提交SHA核验，不能沿用旧头结论。

## 接续

Kimi原控制锁已由应用真实恢复，集中只读任务 `lingdoc-g4-release-route-20261015-01` 被同一会话接受并具名实读，仍busy；Root继续独立测试，只有两个施工方。G6剩余治理/逆向变更/试用不足守卫、其他权限生命周期、带操作提示的完整演示及集中最终验收尚待收口。不新建PR、不合并、不部署。
