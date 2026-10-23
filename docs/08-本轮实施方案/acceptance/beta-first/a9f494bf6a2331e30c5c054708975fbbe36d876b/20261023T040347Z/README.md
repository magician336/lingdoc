# Beta 首版真实验收 · 证据目录

| 项 | 值 |
| --- | --- |
| 执行者 | （未署名） |
| 代码 head | `a9f494bf6a2331e30c5c054708975fbbe36d876b` |
| 基线 | `015bd1225ef4d4a61070295f5c300bc0dce968dd` |
| 运行标识 | `20261023T040347Z` |
| 生成时间 | 2026-10-23T04:03:47Z |
| 总结果 | **BLOCKED** |

## 环境与依赖版本

| 项 | 值 |
| --- | --- |
| Python | 3.14.3 |
| 平台 | Windows-11-10.0.26200-SP0 |
| 数据库驱动 | （本轮没有声明驱动） |
| DSN（脱敏形态） | （本轮没有声明 DSN） |
| 迁移版本 | （没有读到） |

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
| F01 契约序列（24 个节点） | blocked | 没有给出 --base-url，本轮没有对任何提供方执行。 |
| 浏览器对偶：HTTP 侧快照 | not_run | 没有给出 --base-url，HTTP 侧没有对着真实后端跑。 |
| 重启后读回：协议 §1 的隔离 PostgreSQL | not_run | 本轮的后端是 sqlite；协议 §1 要求隔离 PostgreSQL，那一轮没有发生。 |
| 重启后读回：同一个项目、访问令牌与持久化 | not_run | 没有给出可用的 --server-binary，重启没有发生。 |
| Word 人工检查（协议 §4） | not_run | 执行者还没有填写 Word 检查表；协议 §4 的这一段是人工的。 |
| 模型调用账本：参数、预算与模型切换记录 | not_run | 本轮没有给出账本位置（--ledger-dsn 或 --ledger-sqlite）。 |
| 合成材料哈希（协议 §2 第 1 步） | not_run | 本轮没有给出 --material，合成材料的哈希没有记录。 |

## 未运行项

| 未运行项 | 原因 | 解除条件 |
| --- | --- | --- |
| 浏览器对偶：HTTP 侧快照 | 没有给出 --base-url，HTTP 侧没有对着真实后端跑。 | 用 --base-url <真实后端地址> 重跑本脚本。 |
| 重启后读回：协议 §1 的隔离 PostgreSQL | 本轮的后端是 sqlite；协议 §1 要求隔离 PostgreSQL，那一轮没有发生。 | 用 --db-driver postgres --db-dsn <隔离库 DSN> 重跑本脚本。 |
| 重启后读回：同一个项目、访问令牌与持久化 | 没有给出可用的 --server-binary，重启没有发生。 | 用 `make build` 产出服务端二进制，并以 --server-binary <路径> 重跑本脚本。 |
| Word 人工检查（协议 §4） | 执行者还没有填写 Word 检查表；协议 §4 的这一段是人工的。 | 填写 word-check.md 里的五个必填字段（Word 产品版本 / Windows 版本 / 原始 DOCX SHA-256 / 修改后文件 SHA-256 / 检查结果），并用 --word-check <该文件> 重跑本脚本。 |
| 模型调用账本：参数、预算与模型切换记录 | 本轮没有给出账本位置（--ledger-dsn 或 --ledger-sqlite）。 | 用 --ledger-dsn <本轮数据库 DSN> 或 --ledger-sqlite <文件> 重跑本脚本。 |
| 合成材料哈希（协议 §2 第 1 步） | 本轮没有给出 --material，合成材料的哈希没有记录。 | 用 --material <名称>=<sha256> 给出本轮合成材料的哈希后重跑本脚本。 |

## 这份目录说明什么

- 六件产物来自同一次执行、同一个 head、同一个运行标识。
- 状态三态按协议 §5：BLOCKED（主流程缺依赖或无法继续）、FAIL（业务行为违反断言）、NOT RUN（没有执行）。总结果由主流程决定，不用平均覆盖。
- 产物已脱敏：令牌、密钥、请求体与响应体不写入本目录（协议 §6）。
- 协议制定完成不等于验收已经执行或通过。
