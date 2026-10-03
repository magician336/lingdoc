# G1 隔离 PostgreSQL 与来源撤权验证

日期：2026-10-03
分支：`codex/g1-implementation`
数据：全部为合成数据；演练数据库已在结束后删除。

## 隔离 PostgreSQL 升级/回滚

使用独立数据库 `lingdoc_g1_drill_20261003`，不触碰开发库。实际执行序列：

1. 全新数据库执行 versioned migrations 到 `103`，确认 `schema_migrations=(103,false)`。
2. 写入一个 draft 项目、一个 release snapshot，以及一个在 migration 103 时已是 `active` 的项目。
3. 执行 `103 → 109`：六个 migration（104–109）全部成功。
4. 读回确认：`schema_migrations=(109,false)`；新增的四张 G1 表存在；draft 的 `current_context_revision=0`；active 的回填值为 `1`；原项目、`spec_json` 和 release snapshot 保留。
5. 执行 `109 → 103`：六个 down migration 全部成功。
6. 读回确认：`schema_migrations=(103,false)`；104–109 新增表和列已移除；103 原有项目、`spec_json` 和 release snapshot 仍保留。
7. 再次执行 `103 → 109`，确认升级可重复，最终为 `schema_migrations=(109,false)`，active/draft 的 revision 仍分别为 `1/0`。

结论：**PASS**。本次演练验证的是生产 PostgreSQL versioned migrations 的升级、降级、数据保留、active 回填和再次升级；不等同于备份恢复或跨版本线上切换演练。

## 现有 KB/组织分享撤权触发

执行命令：

```text
go test ./internal/lingdoc/workspace -run '^TestCheckPassedSnapshotCannotDownloadAfterRealKBShareRevocation$' -count=1 -v
```

结果：**PASS**（0.907s）。测试使用真实 `KBShareService` 和生产 `SourcePolicy` 装配：

- 先通过 `ShareKnowledgeBase` 授予跨租户资料访问；快照检查通过，首次导出得到 verified 文件。
- 再通过 `RemoveShare` 撤销资料分享。
- 同一份已通过检查的快照再次启动导出返回 HTTP `403 source_access_denied`。
- 撤权前已生成的历史文件再次下载同样返回 HTTP `403 source_access_denied`。

这验证了 LingDoc 当前选定的来源撤权触发：**项目所依赖的 KB/组织资料授权撤销由应用服务直接反映到每次 SourcePolicy 检查**。它不是某个外部供应商 webhook。

## 边界

当前 G1 没有绑定具体的飞书、Notion、Confluence 等外部数据源连接器，也没有把连接器凭据失效通知映射成事件总线消息。若后续选择某个连接器，需要另加其 webhook/轮询适配、签名验证、幂等和事件到 Provenance 的映射；本次结果不替代那项供应商特定集成。
