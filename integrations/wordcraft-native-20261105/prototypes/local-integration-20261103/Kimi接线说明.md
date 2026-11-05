# 本地原生适配模块接线

设计基线 LD-V1-20261103-R1，模块版本 native-adapter-0.1.0。它不是新 UI，也不包含 Go/WASM。云端没有原生资源时仍按设计稿占位，不安装整套本地环境。

## Kimi 可以先做的工作

前端预留一个长期挂载的原生宿主区域。UI 消费 NativeSaveState，通过按钮调用 save，通过事件/适度轮询调用 refresh。界面切换不要卸载 iframe。真实原生宿主接入后传入同源 contentWindow。

```ts
import {createNativeAdapter} from './native-adapter.js';
const adapter = createNativeAdapter({hostWindow: frame.contentWindow!, onState: state => {
  // 按 phase 呈现保存反馈；不要自行通过定时器标记成功。
  setSaveState(state);
}});
await adapter.refresh();
// 监听 iframe 的重新加载时 dispose 旧 adapter，并在宿主就绪后重新创建。
// 切换业务页面只隐藏或显示宿主，不触发 iframe reload。
```

not_registered 表示目前没有能够对应当前文档与修订的完整项目保存记录，不等于文件内容为空。保存请求返回成功后仍需检查最终 state：旧修订已经保存，而新输入可能仍未保存。saved 需要真实校验回执、原生 clean 和当前文档身份/修订一致。

conflict 展示差异处理入口，保留内容；save_failed 保留内容，明确重试。error.persisted 仅说明已有部分保存事实，不代表整个保存完成。unavailable 表示原生未就绪、宿主变化或访问失败；不能切换为 HTML 编辑器继续制造“已经保存”的假象。

原生菜单里的保存仍使用宿主原有逻辑，refresh 会观察其结果。适配器不重新实现保存、不直接调用 ack、不修改接口令牌，不自行创建或重载文稿。它也不负责资料、AI、意见和影响任务，这些在后续模块接入。

## 本地接线仍需完成的部分

最终 Kimi 源码回到本地后，由本地执行者配置同源宿主和受保护后端。云端 localhost 不能指向 gong 的电脑，不能通过放宽 Origin/Host 检查解决跨域。当前验证通过的是适配模块与已有真实宿主组合，不是 Kimi 新界面的端到端验收。

## 当前验证边界

真实 Go/SQLite/DS/WASM/Edge 使用独立的既有固定模板项目。保存中编辑、读回及整组重开走真实服务；网络失败为测试注入；409 冲突与旧请求竞态在针对性测试覆盖，未冒充真实双窗口冲突验收。任意新模板及科研申报新场景需要后续适配。
