# 二期 main 本地验收（2026-11-05）

验证源码：`3d2487bc66ddeb98fdbdebe4aef1e6731d3d2c9b`。独立工作树及独立数据库，未修改产品功能。

结论：本轮完整 WASM release 构建、指定基线回归和深浅主题截图检查通过。此结论不是整个仓库全量测试、真实 AI 复验或 gong 的人工验收。

## 实际结果

| 检查 | 结果 |
|---|---|
| 原生 backstage | 34/34 |
| 原生 workspace | 55/55（旧基线54，新增一项已包含） |
| 原生 animation | 9/9 |
| 网页桥接 Node 测试 | 48/48 |
| WASM release + wasm-bindgen | 成功；保留已有编译警告 |
| 开始页浏览器回归 | 22/22 |
| 普通文稿新建、保存、重新打开 | 6/6 |
| 项目/普通文稿隔离 | 5/5 |
| 深浅主题截图 | 32张，1366×768 / 1920×1080；无浏览器报错，正文状态不变 |
| 实际加载的 WASM | 与本次本地构建 SHA256 一致，见 build-identity.json |

## 目检及边界

检查开始页、文件页、功能区、并排阅读、聊天，以及 UI 注入的 busy/error/四份上下文状态。截图为真实 egui/WASM，不是 HTML 原型。busy/error/full 是显示状态验证，不是模型调用证据。已查看两种主题联系表及1920关键原图。

保留正文缩放是既有行为：1366窄窗口同时打开资源、资料、聊天，正文100%时会需要横向阅读；用户可通过文档组菜单主动选择“正文适合宽度”。本轮没有改为自动缩放。

AI配置健康检查为 aiConfigured=true，但本轮模型请求数为0，不能据此声称真实AI问答或出处链路重新验收通过。划词上下文、Go版本桥接仍为挂起项。

首轮预启动早于WASM产物生成，因缺少 wordcraft.js 退出；保留 startup-first-attempt.txt。构建完成后启动成功，未删除数据库绕过错误。

## 试用与保留

- 新实例：http://localhost:7231/project （或根路径查看原生开始页）
- 隔离数据：D:/Mon3tr/projects/lingdoc-dachuang/phase2-runtime
- 原有7227、7230服务与数据未改动。
- 只新增验收脚本和证据，不改产品源码，也未合入main。

构建使用既有Rust工具链，命令：cargo test -p wordcraft-ui-egui --lib backstage/workspace/animation --offline（分别运行）；cargo build --target wasm32-unknown-unknown -p wordcraft-web --release --jobs 2 --offline，然后 wasm-bindgen。release LTO=false、codegen-units=16，与既有本地构建路径一致。
