# Beta 首版真实验收 · 证据目录

| 项 | 值 |
| --- | --- |
| 执行者 | PF-10 面施工会话（#481：补 F01 的 template_id 捕获链） |
| 代码 head | `df707db5d854a8f7691a9505a542e9c160457c3f` |
| 基线 | `1dd3a24ceca6058d2721e28b38fffabdb21560e0` |
| 运行标识 | `20261023T065132Z` |
| 生成时间 | 2026-10-23T06:51:34Z |
| 总结果 | **FAIL** |

## 环境与依赖版本

| 项 | 值 |
| --- | --- |
| Python | 3.14.3 |
| 平台 | Windows-11-10.0.26200-SP0 |
| 数据库驱动 | sqlite |
| DSN（脱敏形态） | （本轮没有声明 DSN） |
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
| F01 契约序列（24 个节点） | blocked | 序列中断：workflow variable is not captured yet: asset_id -- the contract captures it, but the step that captures it did not produce it: that step's HTTP status did not match the contract, so its capture was skipped. The real failure is this run's failures[] entry for that step. |
| F01-01 getTemplate | held | HTTP 200，与契约声明一致。 |
| F01-02 createProject | held | HTTP 201，与契约声明一致。 |
| F01-03 saveSpec | held | HTTP 200，与契约声明一致。 |
| F01-03b recordActivationRead | held | HTTP 201，与契约声明一致。 |
| F01-04 activateProject | held | HTTP 200，与契约声明一致。 |
| F01-05 listChapters | held | HTTP 200，与契约声明一致。 |
| F01-01 length /data/ruleset_hash | held | 模板规则集摘要是 64 位十六进制：冻结输入靠它与模板对齐｜期望 64｜实际 ad814c1ffba1956c0654abd1fc7fc48fad526109f43406633f05861116ec15a1 |
| F01-02 equals /data/template_id | held | 项目绑定的模板就是本轮读到的那个模板｜期望 {{template_id}}｜实际 {{template_id}} |
| F01-03 equals /data/spec/research_subject | held | 规格字段按请求原样落库｜期望 公开的合成调查样本｜实际 公开的合成调查样本 |
| F01-03b equals /data/actor_id | held | Owner已审阅当前activation-diff；回执属于当前actor、project_version及spec_revision。（回执的actor就是执行本次审阅的Owner）｜期望 {{owner_user_id}}｜实际 {{owner_user_id}} |
| F01-03b equals /data/project_version | held | Owner已审阅当前activation-diff；回执属于当前actor、project_version及spec_revision。（回执记的是当前项目版本——审阅的就是当前版本）｜期望 2｜实际 2 |
| F01-03b equals /data/spec_revision | held | Owner已审阅当前activation-diff；回执属于当前actor、project_version及spec_revision。（回执记的是当前规格修订）｜期望 1｜实际 1 |
| F01-04 equals /data/status | held | 项目激活后状态为active，章节骨架从此可写｜期望 active｜实际 active |
| F01-05 readback_equals /data/0/section_id | held | 激活之后从库里真实读回章节骨架：第一章是研究问题｜期望 question｜实际 question |
| F01-05 readback_equals /data/1/section_id | held | 激活之后从库里真实读回章节骨架：第二章是研究方案｜期望 method｜实际 method |
| F01-06 equals /data/processing_state | failed | 合成材料真的经过接入与处理，处理就绪（协议 §1）｜期望 ready｜实际 null |
| F01-06 bindAsset | failed | 契约声明 HTTP 201，实际 404；该步没有完成，它后面的步骤没有执行。 |
| 浏览器对偶：浏览器侧快照 | not_run | 没有给出 --frontend-url，浏览器侧没有对着真实前端跑。 |
| 三方一致性：浏览器 = HTTP = 刷新后读回 | not_run | 三方比对需要两侧快照；有一侧没有跑成，比对没有发生。 |
| 重启后读回：协议 §1 的隔离 PostgreSQL | not_run | 本轮的后端是 sqlite；协议 §1 要求隔离 PostgreSQL，那一轮没有发生。 |
| 重启后读回：同一个项目、访问令牌与持久化 | held | 后端 sqlite；NOTE: server shutdown on Windows exited with code 1 (TerminateProcess semantics, not a graceful shutdown); the readback assertions still decide the scenario PASS: server process restarted; same sqlite database, access token, and LingDoc project read back NOTE: server shutdown on Windows exited with code 1 (TerminateProcess semantics, not a graceful shutdown); the readback assertions stil… |
| Word 人工检查（协议 §4） | not_run | 执行者还没有填写 Word 检查表；协议 §4 的这一段是人工的。 |
| 模型调用账本：参数、预算与模型切换记录 | held | 读到了 0 行账本记录。 |
| 真实模型调用（协议 §3：真实模型的一次选段改写） | not_run | 本轮没有发生真实模型调用：账本可读，但读到 0 行调用记录，模型参数与预算全 0。 |
| 合成材料哈希（协议 §2 第 1 步） | not_run | 本轮没有给出 --material，合成材料的哈希没有记录。 |

## 未运行项

| 未运行项 | 原因 | 解除条件 |
| --- | --- | --- |
| 浏览器对偶：浏览器侧快照 | 没有给出 --frontend-url，浏览器侧没有对着真实前端跑。 | 起真实前端（Vite dev server）并用 --frontend-url <地址> 重跑本脚本。（Beta 放行执行票 #480：浏览器端到端 / 三方一致性实测） |
| 三方一致性：浏览器 = HTTP = 刷新后读回 | 三方比对需要两侧快照；有一侧没有跑成，比对没有发生。 | 让 HTTP 侧与浏览器侧都跑成后重跑本脚本。（Beta 放行执行票 #480：浏览器端到端 / 三方一致性实测） |
| 重启后读回：协议 §1 的隔离 PostgreSQL | 本轮的后端是 sqlite；协议 §1 要求隔离 PostgreSQL，那一轮没有发生。 | 用 --db-driver postgres --db-dsn <隔离库 DSN> 重跑本脚本。（Beta 放行执行票 #480 缺口 3：隔离 PostgreSQL 环境，协议 §1） |
| Word 人工检查（协议 §4） | 执行者还没有填写 Word 检查表；协议 §4 的这一段是人工的。 | 填写 word-check.md 里的五个必填字段（Word 产品版本 / Windows 版本 / 原始 DOCX SHA-256 / 修改后文件 SHA-256 / 检查结果），并用 --word-check <该文件> 重跑本脚本。（Beta 放行执行票 #480 缺口 5：Word/WPS 人工检查表） |
| 真实模型调用（协议 §3：真实模型的一次选段改写） | 本轮没有发生真实模型调用：账本可读，但读到 0 行调用记录，模型参数与预算全 0。 | 给真实模型凭据后重跑本脚本（Beta 放行执行票 #480 缺口 2：真实模型凭据，并含真实授权来源） |
| 合成材料哈希（协议 §2 第 1 步） | 本轮没有给出 --material，合成材料的哈希没有记录。 | 用 --material <名称>=<sha256> 给出本轮合成材料的哈希后重跑本脚本。（Beta 放行执行票 #480 缺口 4：同一套两章材料及其 SHA-256） |

## 这份目录说明什么

- 六件产物来自同一次执行、同一个 head、同一个运行标识。
- 状态三态按协议 §5：BLOCKED（主流程缺依赖或无法继续）、FAIL（业务行为违反断言）、NOT RUN（没有执行）。总结果由主流程决定，不用平均覆盖。
- 产物已脱敏：令牌、密钥、请求体与响应体不写入本目录（协议 §6）。
- 协议制定完成不等于验收已经执行或通过。
