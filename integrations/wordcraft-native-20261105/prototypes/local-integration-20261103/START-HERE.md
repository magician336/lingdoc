# 当前接线包入口

## 当前试用：受控写作 0.3

无图表的受控文稿修改第一版已完成本地交叉审查和完整真实浏览器验收（30项）。当前结果见 `V1-IMPLEMENTATION.md`，接口见 `WORKFLOW-API.md`，试用步骤见 `受控写作-试用步骤.md`，前端接续见 `受控写作-前端接续说明.md`。当前源码与制品清单为 `CONTROLLED-INTEGRATION.json`。人工体验验收待完成，未合并或发布，不把旧0.2通过记录沿用到新功能。

新试用启动器为 `Start-ControlledTrial.ps1`（7211、独立 `runtime/controlled-trial`），停止用 `Stop-ControlledTrial.ps1`。旧7210及其数据保留。

以下内容和 `CHAIN-INTEGRATION.json` 是0.2历史交接基线；旧ZIP仍保留。当前源码已继续开发，不能再用旧包哈希声称当前源码完全一致。R1冻结设计包没有改写。

## 历史接线包 0.2

设计基线：`LD-V1-20261103-R1`。本地集成版本：`review-chain-0.2.0`。这是 gong 在 2026-11-03 提供的新 Kimi 源码的后续接线版本，不是 11 月 2 日旧界面。

1. 看 `Kimi-链路接线回执.md`：确认哪些能力实际接通、哪些仍为演示。
2. 看 `CHAIN-API.md`、`frontend/LOCAL-INTEGRATION.md`：接线合同、保存版本、状态和错误处理。
3. 看 `试用与验收.md`：实际试用步骤与分层证据。
4. 运行 `node verify-chain-handoff.mjs`：核对当前包文件哈希和冻结设计包。当前文件清单在 `CHAIN-INTEGRATION.json`；哈希一致只证明文件一致。

## 给 Kimi

本包含最新接线后的 `frontend/src/` 与 `frontend/dist/`。请以这里的接线增量继续设计，不要把旧的演示状态管理覆盖回真实保存、意见和回看状态。原 `frontend/README.md` 与实施映射是接线前的云端交付记录；当前增量以 `frontend/LOCAL-INTEGRATION.md` 为准。原 `frontend/handoff/` 设计包保持不变。

只做前端设计时，使用常规 Node 前端环境即可；不需要在云端安装 WSL、Go、WordCraft 编译链或配置私密模型。真实接口依赖同源本地网关，不能仅启动 Vite 就宣称已接通本地服务；本机 `localhost` 也不是云端的 `localhost`。未接通时保留连接失败提示。

代码接收、哈希核对、联调、实际运行和发布分别回执。本包尚未由 Kimi 云端确认接收，当前不声明全端同步完成。

## 本机运行边界

把此目录放在现有 LingDoc 工作树的 `prototypes/local-integration-20261103/`，使用 `Start-LocalTrial.ps1`。入口为 <http://127.0.0.1:7210/>，停止用 `Stop-LocalTrial.ps1`；两者只管理独立试用实例。

包内不含依赖安装目录、用户数据、令牌、原生 WASM/Go 二进制或完整 LingDoc 仓库。服务复用工作树既有 `candidates/lingdoc-native-workspace-20261027/scripts/delivery-runtime.mjs`、DS/WordCraft 资源、Python、WSL `LingDoc-Beta-R29-Ubuntu2404` 及 `/tmp/lingdoc-local-source-ai`，不是可以任意机器直接独立运行的产品安装包。

数据留在 `runtime/trial/`，测试数据在独立目录。不要通过删除这些目录重置项目。7210 是本批入口；7100、7106 和原数据库未改动。
