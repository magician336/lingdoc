# 当前入口：受控文稿修改第一版

当前为 `controlled-writing-0.3.0`，图表不纳入。先读 [START-HERE.md](START-HERE.md)、[实现与验证记录](V1-IMPLEMENTATION.md) 和 [受控写作试用步骤](受控写作-试用步骤.md)。本轮真实模型候选与受限原生替换已通过完整30项浏览器验收。7211独立试用已启动，等待人工体验；模型曾发生503，随后恢复。当前实现哈希见 `CONTROLLED-INTEGRATION.json`。

## 0.2 真实资料、人工意见与变化回看（历史）

此前交付为 `review-chain-0.2.0`，设计基线 `LD-V1-20261103-R1`。已经接入 gong 新提供的 Kimi 前端。旧 [试用与验收.md](试用与验收.md) 和7210项目保留。

0.2历史包以 `CHAIN-INTEGRATION.json` 为文件校验清单；`INTEGRATION.json` 和 `LingDoc-local-native-adapter-0.1.0.zip` 是第一批历史交付。这些历史清单不能校验继续开发后的当前目录。以下保留第一批说明作为历史背景。

## 本地原生集成第一批（历史）

基线 LD-V1-20261103-R1。Kimi 负责云端前端；本目录负责本地宿主与现有保存链适配。施工范围仅本目录；不修改既有候选后端、保存协调器、用户文稿或 7100/7106 数据。

`native-adapter.js` 提供 refresh/save/subscribe/dispose。它只调用已有 wordcraftHost，不自行导出、落盘、登记或 ack。宿主窗口必须同源；原生 iframe 由外壳长期保留，dispose 只释放订阅，不关闭编辑器。适配器不开放重载或 open，避免外围误清正文。

状态区分 not_registered、unsaved、saving、saved、save_failed、conflict、unavailable。只有宿主读回校验完成且当前引擎 clean，才显示 saved。保存期间新增编辑仍为 unsaved。重复点击合并本次请求，失败由用户明确重试，继续复用宿主的原保存事务。

```js
import { createNativeAdapter } from './native-adapter.js';
const adapter = createNativeAdapter({ hostWindow: nativeFrame.contentWindow, onState: renderSaveState });
await adapter.refresh();
// 外围保存按钮调用 adapter.save()；原生工具栏仍走原有保存链。
// 可在窗口聚焦、保存完成和适度状态轮询时 refresh，卸载外壳时 dispose。
```

第一批验收是可靠保存与重开。人工意见、资料持久化、影响任务尚未接入此模块；不把首次底座验收扩展为首版整体完成。真实验证使用独立 Go/SQLite/DS 与 Edge，明确保留测试数据和首错，退出自己的服务。

运行：`npm test`；Windows/WSL 原环境下 `npm run verify:real`。无需安装 npm 依赖。完整联调还需后续接入 Kimi 的实际前端；单独通过模块验证不等于新界面已接线。
