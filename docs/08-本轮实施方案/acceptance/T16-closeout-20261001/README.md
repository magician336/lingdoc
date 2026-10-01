# T16 MVP交接证据

接续既有任务 `lingdoc-t16-closeout-20260927-01`，分支 `codex/t16-cloud-handoff-20260930`；当前源码验证提交 `93f4bacd43c32d15915eba9110db70b170d7fa62`，其父提交 `fa910e4df5ce5ef69b6e5c3f4692f7cc75e74b45`，契约静态校验提交 `9c98be578521fe15ac50773d609953a2194dd279`，PR base `1992abde95ca5636fdc1a2af39f4565fde358a99`。`1c01f98` 仅是原始真实集成 checkpoint。

[下载完整脱敏验收包](evidence.zip)，解压打开根目录 `验收入口.html`。包含全部T01–T16状态、录屏、DOCX、PDF、关键API/日志、模型账本和manifest。每项未覆盖范围保留，不宣称完整产品全部通过。

[交付页面截图](delivery-current-history.png)、[引用回源截图](candidate-source.png)、[真实模型DOCX](real-model-delivery.docx)。

## 证据归属

- 真实供应商：云端4次Embedding、2次DeepSeek，共6次；本次准备PR不新增调用。费用估算¥0.02166，不是人民币结算账单。此前超出保守云端5次承诺1次的事实保留；gong已知另1次连接测试为人工提供，如计入已知合计7次，超过原整轮6次限额。
- 真实服务：PostgreSQL、Redis、生产DI/迁移103、真实登录/跨租户权限、当前运行候选、Vite/Chromium及DOCX下载。候选生成/初次采纳主要为API实测；补录浏览器回源、旧候选409、两章待核确认、冻结、导出、下载。不是新录制一轮付费生成。
- Word人工：gong反馈长文正常并提供三页截图，明确确认使用Word修改、保存、关闭后重开且修改保留。T05/T14 Office条件按gong人工证据通过；版本未提供，云端没有独立运行Word或读取另存文件。
- 受控测试：隔离PG实际迁移/生产仓库，模型/当前性适配器为受控替身；真实worker子进程SIGKILL，隔离租约时间注入；坏渲染器使用真实DOCX校验器拒绝并持久化失败、无下载字节、新动作恢复。不能作为真实供应商故障证据。
- 尚未覆盖：真实供应商超时/未知扣费、F14精确仅相关无支撑输入、实际技能孤儿容器回收、第二人在全新环境独立启动。单纯开PR、CI通过、Word人工反馈不替代最终MVP范围确认。

## 评审复核

本PR以 `codex/t09-t12-integration` 为基础，依赖尚未合并的PR #25；不向main重复带入全部前置集成，也不合并/移植PR #39。当前main新增工程技能配置，此分支未改其内容。

可复现付费请求已停止。不要重跑生成以验证这份历史证据。自动/受控测试、实际供应商证据、gong人工反馈和未验证条件分别列明。压缩包不含密码、token、API key或服务凭证文件；清单列每个文件SHA256。

对当前 PR 的验证清单见本目录 `pr-validation.json`；真实集成二进制仍属于历史 checkpoint，不能替代当前源码验证。manifest 明确列出源码验证提交和逐文件 Git blob；最终 PR head 与 GitHub CI 以远端状态为准。本次没有重跑付费模型或重启主服务。
