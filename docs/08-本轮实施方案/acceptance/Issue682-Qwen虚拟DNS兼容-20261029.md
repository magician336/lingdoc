# Issue #682：Qwen 虚拟 DNS 兼容

范围：仅官方 `dashscope.aliyuncs.com` 在系统 DNS 返回纯 fake-IP 时，按部署者显式配置的 HTTPS DNS 服务取得公网地址。默认关闭；不改变系统代理和所有其他域名的解析。

| 验收项 | 不变量 | 状态 |
| --- | --- | --- |
| 默认与非目标域名 | 保留系统 DNS；无隐式第三方 DNS 请求 | 定向测试 PASS |
| 纯 fake-IP | 显式配置后只接受 HTTPS 解析、经分类的公网结果；实际连接锁定已检查的地址，保持目标主机 TLS 验证 | 定向测试和真实 TLS 请求 PASS |
| 失败与混合答案 | 包含非 fake-IP 的限制地址、DNS 错误、非公网 DoH 端点、解析异常和不安全结果均失败关闭；使用 CONNECT 代理而不能固定目标时拒绝回退 | 定向测试 PASS |
| 真实 Qwen | TUN 开启、真实模型调用，记录代码身份与用量；不得调整系统代理 | 2026-10-30 当前工作树：真实调研计划及写作适配器 PASS（合成资料）；真实向量适配器 PASS |

显式配置的 HTTPS DNS 服务可能看到目标域名，属于运营者选择的 DNS 出口；配置错误时不放宽现有 SSRF 检查。未覆盖其他模型提供商或任意专属端点。

复测身份：代码基线 `bf66cfc9` 加本次工作区变更；`qwen_public_dns.go` SHA-256 为 `28e3e911cfc7b9f3699da5e9c4e4371c1e2832e2452cf441a8579db4ce98fd2e`，`security.go` SHA-256 为 `ae18e39cac14f4e7211ea67960b0aeef51d6222a174674e136273af47bb11f0e`。本次新增了对系统 DNS 空响应和 HTTPS DNS 响应缺少状态字段的失败关闭。DNS 系统结果为 `198.18.0.66`，显式测试配置为 `LINGDOC_QWEN_DOH_URL=https://cloudflare-dns.com/dns-query`、`LINGDOC_QWEN_DOH_BOOTSTRAP_IP=1.1.1.1`、`NO_PROXY=dashscope.aliyuncs.com`；TUN 和系统代理未改变。

补充 HTTPS DNS 状态字段检查前，调研模型 `qwen3.8-flash` 的提案 416 输入 / 579 输出 token，写作适配器 650 输入 / 222 输出 token；检索和资料是合成夹具，未覆盖写作 HTTP job。补充检查后，真实 TLS 请求及 `qwen3.7-text-embedding-flash` 向量调用再次通过，返回一个 1024 维向量。定向命令：`go test -vet=off ./internal/utils -run 'TestQwenDNS|TestQwenDoH|TestSSRFSafeDialContextRejectsRestrictedPortAtFinalSink' -count=1`；网络命令分别显式启用 `TestQwenFakeIPLiveOptIn`、`TestIssue682QwenIntegratedSyntheticChain` 和 `TestQwenEmbeddingDoHLive`，凭据仅从本机私有配置读入子进程，不写入仓库。

其余模型域名、专属 endpoint、使用 CONNECT 代理、第三方 DNS 故障和该路径的生产部署尚未真实验收；启用前由运营者确认 DNS 服务和 bootstrap IP，无法连接时继续失败关闭。
