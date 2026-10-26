## 2026-10-26 最终 Embedding 验收：真实向量链路已打通（本轮实测，2 次 provider 请求）

本轮在负责人授权的硬预算内**自主发出 2 次真实 provider 请求**（上限 2），全部成功。

### 结论

- **真实向量已写入**：2 个 chunk 经正式应用链路（`POST /knowledge/{id}/reparse` → 索引阶段 → embedding 阶段）一次**批量**向量化，provider 返回维度 **1024**；`lite_embeddings` 新增 2 行 `dimension=1024`，新建 vec0 虚表 `vec_embeddings_1024`，chunk `index_status` 由 `failed` 转为 `ready`。
- **真实向量检索已通过**：一次 query 向量化 + 一次 hybrid-search 命中 **2 条**，两条 `match_type=0`（向量匹配）；服务端日志逐行显示 **关键词通道 0 命中、向量通道 2 命中**，`fuseOrDeduplicate` 走纯向量分支，最终顺序与分数（cosine 0.5989 / 0.4554，distance 0.4011 / 0.5446）**完全来自向量通道**。
- **§6 第 6 项此前为 `NOT RUN`、向量行 0；本轮已具备申请改判 `PASS` 的完整证据**（是否改判由负责人裁定，本轮未自行改判，Beta 正式放行结论也未因此改动）。

### 账本（唯一可信口径）

embedding 账本 `ledger/qwen-embedding-budget.json.calls.json`，最终 sha256 `a9cf6cda345ab20bb8cdcac6e74288037530db9ff8f70da690049ee9696242ef`：

| 行 | 模型 | 状态 | 预留上界 | provider 实报 | 预留 nano-CNY | 实际 nano-CNY |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `qwen3.7-text-embedding` | `http_error`（403，已由操作者结清） | 3085 | 无 usage | 1,542,500 | 1,542,500 |
| 2 | `qwen3.7-text-embedding-flash` | `response_received`（2 chunk 批量） | 3085 | 648 | 385,625 | 81,000 |
| 3 | `qwen3.7-text-embedding-flash` | `response_received`（query） | 1080 | 27 | 135,000 | 3,375 |

- 累计预留 **7,250 / 8,000 token**；累计预留费用 **2,063,125 / 8,000,000 nano-CNY ＝ ¥0.002063125（上限 ¥0.008）**；**Chat 请求 0 次**；**未授权探测/测试请求 0 次**（本地链路测试用假 transport，零网络）。
- 窗口当前 `blocked=false`，剩余 750 token 预留额度与 5,936,875 nano-CNY 仍在可用区间。

### 冻结窗口的正规恢复（未手改账本、未切 profile、未绕预算）

上一轮的 403 用 #671/#674 刚落库的恢复通道解开：先原字节复制冻结账本（sha256 `238ca5bf…`）留证，再以精确 sha256 + 操作者证据执行 `acceptance-budget-reconcile -profile lingdoc-qwen-embedding-budget-20261025 -apply -confirm-full-reserve-charge`。结果：`blocked=true → false`，失败请求逐字保留（仍 `http_error`、仍无 usage、仍按预留上界计费、**未退款**），仅追加一条 `embedding_http_error_full_reserve_v1` 记录。首次执行被闸门拒绝（快照 sha256 必须小写十六进制），闸门在写文件前即拒绝，**账本未被改动**——这恰好证明快照绑定是实装的。

### 未受影响的既有证据（逐字节核对）

- chat 账本 `74e86abc…` / `dd39a0cb…` **前后逐字节未变**；DB 侧 `lingdoc_model_call_ledger` 仍 4 行且全部是 chat（embedding 路径不写该表）。
- 章节 `source_ids`（9 条）**前后逐字一致**，被引用的 2 个 chunk 全部存在 ⇒ **source_ids 8/8 PASS 的证据完整保留**；词法检索 10/10 的口径不变，仍标注为词法。
- 目标知识库的 2 个被引用 chunk 行（id / 坐标 / 内容 sha256 / `index_status`）**前后逐字一致**，其向量行仍是关键词行（维度 0），未被本轮改写。
- 上一轮 403 原始响应 `provider-403-response.txt`（sha256 `2915d119…`）保留。

### 偏离（需要负责人裁定）

向量化在**同租户、同材料、同 chunking 配置**的隔离向量知识库中进行，而不是目标知识库本体。理由：重跑目标知识库的被引用知识会**删除其 2 个 chunk 并以新 uuid 重建**，使章节 `source_ids` 悬空并翻转 `source-available` 规则。隔离知识库的 2 个 chunk 与目标知识库 2 个被引用 chunk 的**内容 sha256、坐标（0-489 / 475-902）、`context_header`、knowledge title 前缀逐字一致**，实测批量预留上界同为 3085 ⇒ 向量即目标材料的向量。此为上一轮已登记的偏离，本轮沿用并补齐了等价性证据。

若验收口径要求「被引用 chunk 本身带向量行」，则需要另一轮在目标知识库内以**单 chunk 重索引**方式实现，代价是每 chunk 1 次 provider 请求（2 chunk = 2 次），超出本轮 2 次上限。

### 请负责人裁定

1. 是否据此把 §6 第 6 项由 `NOT RUN` 改判 `PASS`；
2. 是否接受上述「在隔离向量知识库向量化」的偏离，或要求另立一轮在目标知识库本体上做向量索引；
3. 本票是否关闭（其原命题「受控 Qwen profile 拒绝 embedding 与需要检索的生成路径互斥」在 embedding profile 下已不复现；chat profile 的互斥面仍未验证，留在 #675）。

完整报告与全部证据（前后快照、账本原件、阶段 A/B 结果、检索日志、证据清单）见 `D:\wt\lingdoc-485-evidence\embedding-run\flash-run\`，报告文件 `REPORT-embedding-flash-acceptance-20261026.md`，证据清单 `artifact-manifest.json`（含每件 sha256）。
