# Spec #164：LingDoc 应用层传输解耦与幂等结果边界

> 文档状态：2026-10-16。实现切片已完成，子票 #165–#169 已关闭；PR [#170](https://github.com/magician336/lingdoc/pull/170) 仍开放，尚未合并到 `main`。上一轮 PR 检查 8 项全部通过；本次文档提交会触发新一轮检查，实时状态以 PR 页面为准。

本文件描述 Spec #164 的实现结果。它不是历史 T01–T16 任务中的 T08；历史 T08 仍专指“章节读写与生成上下文”。

## 范围

对应 GitHub Spec #164 与契约收口票 #169。Workspace 写操作及交付 issue disposition 的公开应用契约只返回领域结果和重放状态；HTTP Handler 负责请求解码、状态码、成功/错误 envelope 与 JSON 序列化。

## 已落地的边界

- `ApplicationService`、`WorkspaceWriteApplication` 不再声明 `json.RawMessage`、HTTP 状态码或 Handler 类型。
- 建项、研究条件、项目/成员/Owner、模板、章节、工作副本、ChangeSet、草稿候选和 issue disposition 使用 typed result。
- Handler 通过 typed result 统一映射既有 wire contract；ChangeSet 过期 apply 仍映射为 409 `version_conflict`。
- 交付服务通过 typed issue disposition 端口读写处置记录，认证、当前性、幂等、审计与事务仍由应用服务负责。
- `selected_rewrite` 等非 HTTP 调用方消费 typed `ApplyRewriteResult`，不再解析应用层 JSON 响应。

## 验证证据

- PASS：`go test ./internal/lingdoc/...`
- PASS：`go test ./internal/router`
- BLOCKED（环境）：`go test ./internal/container` 在 Windows 缺少 `sqlite3.h` 时被 `sqlite-vec-go-bindings/cgo` 阻断；未出现业务断言失败。
- PASS：`codegraph sync`
- PASS：`git diff --check`
- 限制：Windows 下完整 `go test ./...` 仍受缺少 `sqlite3.h` 及仓库既有环境依赖测试影响；这不等同于本次 LingDoc 断言失败。

## 保持不变的契约

现有路由、成功与错误 envelope、状态码、错误码、重放元数据和前端请求行为保持不变。持久化的幂等记录格式与数据库 schema 未改变。
