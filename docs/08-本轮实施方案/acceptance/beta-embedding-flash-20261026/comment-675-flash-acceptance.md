## 2026-10-26：本轮实测再次确认该缺口仍然存在（本轮未触发），并附一条真实数据

本轮最终 Embedding 验收在负责人授权的硬预算内发出 **2 次真实 provider 请求**，两次都以 `response_received` 结束且 usage 由 provider 实报 ⇒ **本轮没有触发**本票描述的冻结结局。但这不构成该缺口的修复，只是运气好。

### 本轮的真实数据（唯一可信口径来自文件账本）

`ledger/qwen-embedding-budget.json.calls.json`，最终 sha256 `a9cf6cda345ab20bb8cdcac6e74288037530db9ff8f70da690049ee9696242ef`：

| 行 | 模型 | 状态 | usage_status |
| --- | --- | --- | --- |
| 1 | `qwen3.7-text-embedding` | `http_error`（403） | `unavailable` |
| 2 | `qwen3.7-text-embedding-flash` | `response_received` | `reported`（648 token） |
| 3 | `qwen3.7-text-embedding-flash` | `response_received` | `reported`（27 token） |

### 为什么这仍然是一个待裁定的缺口

本票列出的五个冻结结局（`outcome_unknown` / `usage_unknown` / `in_flight` / `cancel_reconciliation_required` / `limit_violation`）在本轮**一次都没有出现**，但触发条件在代码里依然成立：

1. **`usage_unknown` 是最危险的一个，且与 provider 行为直接相关**：provider 只要返回 200 但**不报 usage**（`response.usage.prompt_tokens <= 0`），`OpenAIEmbedder.BatchEmbed` 就会走 `reservation.Unknown("usage_unknown")` ⇒ 该请求不可恢复地冻结窗口。本轮的 648 / 27 token 是 provider **报**的；换一个 provider 行为或换一个模型版本，就可能变成不报。
2. **`outcome_unknown` 由传输层失败触发**（`doRequestWithRetry` 报错）：网络抖动、超时、TLS 故障都会命中它，而它**没有**恢复通道。
3. **`in_flight` 窗口**：进程在请求发出后、账本落账前被杀，账本会永久留下一条 `in_flight`。
4. **`cancel_reconciliation_required`**：`CancelNoCall` 只在「请求确实没发生」时可回滚预留；任何无法确认的情形都退化成不可恢复的冻结。
5. **`limit_violation`**：provider 报的 token 数超过我们按字节算出的上界（或负计数），状态会被改写为 `limit_violation` ⇒ 同样不可恢复。
6. **chat profile 的同类缺口**：`CostBasisNotApplicable` 的 profile 在失败时 `InputTokens=0`，连「按预留上界计费」这个保守计费基数都没有 ⇒ 冻结后既无恢复通道，也无法核账。

### 本轮可以确认为真的部分

- **4xx 类**的冻结已经**有**恢复通道：#671 落库的能力位 + #674 的 `acceptance-budget-reconcile` 正规路径，本轮实际用它把上一轮的 403 窗口解开（失败请求逐字保留、未退款、仅追加一条 full-reserve 结清记录）。闸门在写文件之前拒绝快照不匹配的一次尝试，说明快照绑定是实装的。
- 本轮窗口 `blocked=false`，剩余 750 token 预留额度与 5,936,875 nano-CNY 仍在可用区间。

### 请负责人裁定（与 #546 的一并处置）

1. 是否为这五个冻结结局补一条**与 `http_error` 同构**的恢复通道（按 profile 能力位声明、按保守上界计费、快照绑定、不可退款），还是接受「一次非 4xx 失败即报废整个窗口」；
2. chat profile（`CostBasisNotApplicable`）的同类缺口是否单独处理，还是随 Qwen profile 一起退役；
3. 在裁定之前，本票保持开放。

本轮未对本票做任何代码改动，也未改判任何验收门槛。证据与完整报告见 `D:\wt\lingdoc-485-evidence\embedding-run\flash-run\`（`REPORT-embedding-flash-acceptance-20261026.md`、`artifact-manifest.json` 含每件 sha256）。
