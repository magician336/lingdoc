# 当前原生开发路由

当前施工对象是 Rust／egui 原生桌面应用，不是旧浏览器 7241／WASM 统一前端。开工先核对仓库根、分支、HEAD、未提交改动和适用 `AGENTS.md`，再读 [首版产品决策](lingdoc-refactor/DECISIONS.md)。不得覆盖他人或上一阶段尚未提交的修复。

下文使用路径缩写：

```text
ENGINE = integrations/wordcraft-native-20261105/candidates/lingdoc-native-workspace-20261027/engine
APP    = ENGINE/apps/wordcraft/src
UI     = ENGINE/crates/ui-egui/src
CORE   = ENGINE/crates/engine/src
DRAW   = integrations/wordcraft-native-20261105/prototypes/local-integration-20261103/native-reading
```

## 按任务定位

| 任务 | 先读 | 现行源码边界 |
|---|---|---|
| 前端布局与侧栏 | 首版产品决策；`UI/lib.rs` 的主布局 | `chrome.rs`、`ribbon.rs`、`panes.rs`、`project_workspace.rs`、`source_workspace.rs`、`theme.rs` |
| 项目打开与保存 | [ADR 0001](adr/0001-native-save-backup-recovery-contract.md) | `CORE/project.rs`、`CORE/save.rs`、`UI/project_ops.rs`、`UI/save_ops.rs` |
| 选区 AI | 决策第 3～4 节；DSR-02／03 仅作设计参考 | `UI/ai.rs`、`ai_toolbar.rs`、`ai_popover.rs`；`APP/deepseek.rs`、`ai_host.rs` |
| 简化资料问答 | 决策第 2～3 节；DSR-02 仅作协议参考 | `UI/project_workspace.rs`；`APP/project_ai_host.rs`、`native_features.rs`、`deepseek.rs` |
| AI 绘图 | 保留现有工作台和显式插入合同 | `UI/drawing_panel.rs`；`APP/drawing_runtime.rs`、`drawing_bridge.rs`；`DRAW/drawing.html`、`drawing.js` |
| 原生桌面装配 | `APP/main.rs` | `native_features.rs`、`project_ai_host.rs` 及实际 `Services` 钩子 |

## 现行代码不等于现行产品方向

简化资料问答属于现行方向：必须有明确问题，默认授权当前主 DOCX＋全部已导入、可读取的项目资料，不再要求逐次勾选。该范围不是无条件全文注入；超预算时由原生应用进行结构化节选并显示实际使用来源。选区 AI 只处理圈选内容，当前主 DOCX 默认作为只读上下文；改写附加项目资料仍需当次显式勾选。打开项目和导入资料时不预建 AI 上下文；内置阅读器可按需解析显示。内置资料阅读、多阅读组和并排阅读已明确保留；六章专用旧壳已从现行原生默认界面清理，通用命令搜索独立保留。仍被 Web 装配的共享保存、交付接口不属于当前原生主链，不按“全仓无旧接口”误删。

正文、保存和选区 AI 不依赖旧 DS／Go／Node／Python／WASM 宿主。现有旧版 AI 绘图工作台＋next.ai 按需启动 Node，是最新明确保留的例外；不要借其他任务顺带重写这条链路，也不要把 Word 上游 Draw／墨迹当成它的替代入口。LingDoc 项目默认界面只保留活动栏“AI 绘图”，不展示墨迹 Draw 页签。

## 历史资料何时可读

- `integrations/wordcraft-native-20261105/UNIFIED-FRONTEND.md`：旧浏览器多宿主体系，仅供追溯。
- `docs/superpowers/specs/`、`plans/`：历史设计与审查；其中的状态、行号、接口和未验证项属于当时基线。
- `docs/lingdoc-refactor/COORDINATION.md`、`assignments/`、`reports/`：已结束工作包的协作记录，不是现行任务认领规则或实时进度。
- 根目录 `candidates/`、`handover/` 及带日期的整合记录：对应历史任务的源码或证据，不作为当前默认入口。

默认不通读历史交接、全部验收 JSON 或所有候选源码。搜索先限定上表目录；确需回源时再扩大范围。发现文档、代码和最新产品决定互相矛盾时，明确指出并找 gong 对齐，不自行选择对自己方案最有利的一种解释。
