# Beta 首版真实验收 · 证据目录

| 项 | 值 |
| --- | --- |
| 执行者 | PF-10 面施工会话（magician336 认领） |
| 代码 head | `9f1ca3adb5193fce733bb7b22eb1d09ee48eb0d9` |
| 基线 | `275d438bf275d0186efc707fb90974a21a5b675b` |
| 运行标识 | `20261023T035219Z` |
| 生成时间 | 2026-10-23T03:52:20Z |
| 总结果 | **FAIL** |

## 环境与依赖版本

| 项 | 值 |
| --- | --- |
| Python | 3.14.3 |
| 平台 | Windows-11-10.0.26200-SP0 |
| 数据库驱动 | sqlite |
| DSN（脱敏形态） | D:\code_warehouse\Projects\lingdoc\.worktrees\feat-auto-20261023-6a598156\tmp\beta10\backend-data\lingdoc.sqlite= |
| 迁移版本 | 55 |

## 实际命令

```
python scripts/lingdoc_mock/run_f01.py --base-url <base-url> --report <f01-report.json>   # by scripts/acceptance/beta_evidence.py
python scripts/acceptance/beta_http_readback.py --base-url <base-url> --out <http.json>
node scripts/acceptance/beta_browser_cdp.mjs --url <frontend-url> --out <browser.json>
python scripts/acceptance/beta_evidence_parity.py --http <http.json> --browser <browser.json> --out <parity.json>
python scripts/acceptance/lingdoc_full_server_restart.py --binary <server-binary> --repo-root <repo-root> --output-dir <work-dir>
python scripts/acceptance/beta_evidence.py --word-check <word-check.md> (执行者填好后重跑)
```

## 范围

- F01 契约的 24 个节点（本轮用 contracts/workflow.json 的断言求值）
- 浏览器对偶三方比对（HTTP = 浏览器 = 刷新后读回）
- 服务端真实重启后的读回
- Word 人工检查（执行者填写，执行器只算哈希与校验必填字段）
- 模型调用账本里的参数、预算与模型切换记录

## 结论项

| 结论项 | 状态 | 证据 |
| --- | --- | --- |
| F01 契约序列（24 个节点） | blocked | 序列中断：workflow variable is not captured yet: template_id |
| F01-01 length /data/ruleset_hash | failed | 模板规则集摘要是 64 位十六进制：冻结输入靠它与模板对齐｜期望 64｜实际 null |
| 浏览器对偶：浏览器侧快照 | not_run | 没有给出 --frontend-url，浏览器侧没有对着真实前端跑。 |
| 三方一致性：浏览器 = HTTP = 刷新后读回 | not_run | 三方比对需要两侧快照；有一侧没有跑成，比对没有发生。 |
| 重启后读回：协议 §1 的隔离 PostgreSQL | not_run | 本轮的后端是 sqlite；协议 §1 要求隔离 PostgreSQL，那一轮没有发生。 |
| 重启后读回：同一个项目、访问令牌与持久化 | held | 后端 sqlite；NOTE: server shutdown on Windows exited with code 1 (TerminateProcess semantics, not a graceful shutdown); the readback assertions still decide the scenario PASS: server process restarted; same sqlite database, access token, and LingDoc project read back NOTE: server shutdown on Windows exited with code 1 (TerminateProcess semantics, not a graceful shutdown); the readback assertions stil… |
| Word 人工检查（协议 §4） | not_run | 执行者还没有填写 Word 检查表；协议 §4 的这一段是人工的。 |
| 模型调用账本：参数、预算与模型切换记录 | held | 读到了 0 行账本记录。 |
| 合成材料哈希（协议 §2 第 1 步） | not_run | 本轮没有给出 --material，合成材料的哈希没有记录。 |

## 未运行项

| 未运行项 | 原因 | 解除条件 |
| --- | --- | --- |
| 浏览器对偶：浏览器侧快照 | 没有给出 --frontend-url，浏览器侧没有对着真实前端跑。 | 起真实前端（Vite dev server）并用 --frontend-url <地址> 重跑本脚本。 |
| 三方一致性：浏览器 = HTTP = 刷新后读回 | 三方比对需要两侧快照；有一侧没有跑成，比对没有发生。 | 让 HTTP 侧与浏览器侧都跑成后重跑本脚本。 |
| 重启后读回：协议 §1 的隔离 PostgreSQL | 本轮的后端是 sqlite；协议 §1 要求隔离 PostgreSQL，那一轮没有发生。 | 用 --db-driver postgres --db-dsn <隔离库 DSN> 重跑本脚本。 |
| Word 人工检查（协议 §4） | 执行者还没有填写 Word 检查表；协议 §4 的这一段是人工的。 | 填写 word-check.md 里的五个必填字段（Word 产品版本 / Windows 版本 / 原始 DOCX SHA-256 / 修改后文件 SHA-256 / 检查结果），并用 --word-check <该文件> 重跑本脚本。 |
| 合成材料哈希（协议 §2 第 1 步） | 本轮没有给出 --material，合成材料的哈希没有记录。 | 用 --material <名称>=<sha256> 给出本轮合成材料的哈希后重跑本脚本。 |

## 这份目录说明什么

- 六件产物来自同一次执行、同一个 head、同一个运行标识。
- 状态三态按协议 §5：BLOCKED（主流程缺依赖或无法继续）、FAIL（业务行为违反断言）、NOT RUN（没有执行）。总结果由主流程决定，不用平均覆盖。
- 产物已脱敏：令牌、密钥、请求体与响应体不写入本目录（协议 §6）。
- 协议制定完成不等于验收已经执行或通过。
