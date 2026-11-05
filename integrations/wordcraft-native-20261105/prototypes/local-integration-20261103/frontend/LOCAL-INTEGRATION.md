# Kimi LD-V1 本地真实接线

本目录来自 gong 提供的 `LingDoc-LD-V1前端源码与交接.zip`（2026-11-03）。`handoff/` 保留原件，7 个文件哈希和场景引用检查通过。原 README 与实施映射记录的是云端交互原型交付，本文件说明后续本地接线增量。

本批直接沿用 Kimi 的项目概览、写作和审阅界面。WordCraft 使用同源 `/native.html` 长驻 iframe；跨页和侧栏切换不销毁宿主，不替换为 HTML 编辑器。保存只调用已有 `native-adapter.js`，真实保存回执独立于任务完成状态。DOCX 副本从核对过的保存版本读取，并再次计算 SHA256 后下载。

`src/lib/live.tsx` 通过 `/integration/config` 获取当前运行令牌；所有业务请求带 `X-Integration-Token`。`GET /integration/state` 读取资料版本、意见和处理记录；`POST /integration/actions` 带 `key`、`expectedRevision`，类型为 `publish_source`、`comment`、`anchor`、`decide`、`complete`。网络错误保留原请求供重试，409 读回项目并要求重新确认，不静默覆盖。

意见选区由真实原生 selection 获取。保存 documentId、revision、selection、excerpt 及本次宿主实例标识；正文修订变化或页面重开后保守显示需重新定位，旧摘录仍保留。本批不把文本搜索当精准定位。

资料 v2 在登记前明确显示为待登记更新；登记后回看任务才进入队列。选择修改只是处理方向；实际完成还需用户说明、真实保存版本，服务端校验版本和摘要。图源与研究方案版本尚未接入，因此不会凭演示图形操作登记完成。

AI 回复、建议和图源仍明确是演示；任意 DOCX 导入未接入。没有将旧 Go ImpactTask 动作伪装为新合同，人工意见与回看由本地持久化扩展提供，后端适配和迁移边界由同级服务文档说明。

依赖复用工作树 `prototypes/frontend-native-20261102/node_modules`，本目录 node_modules 为目录联接；未安装新依赖。构建 `npm run build`，静态结果在 `dist/`，由同级集成服务提供。冻结设计包校验 `node handoff/verify-handoff.mjs`。本批 TypeScript / Vite build 与 ESLint 已通过，真实 HTTP、浏览器与重开验收由主执行者完成，不能拿 build 代替业务验收。
