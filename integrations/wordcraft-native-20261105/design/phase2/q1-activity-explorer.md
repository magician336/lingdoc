# Q1 · Activity Bar + 文件资源管理器侧栏

> 集成分支：`codex/ui-phase2-20261105`。交互稿：`design/phase2/mock-v4.html`（左缘竖条 + 资料侧栏部分）。
> **云端施工，未经原生编译验证。**

## 现状

- 侧翼开关是 chrome 下一行"伪页签带"（项目资源 / 问答 / 正文·可编辑），开关与状态混排；
- 资源树在 `project_workspace.rs`（`resources()` / `file_row()` / `paint_file_icon()`），P1/P5 已令牌化但图标为单色。

## 施工点

1. **Activity Bar**（新增，约 48px 竖条，建议落在 `chrome.rs` 或新模块 `activity_bar.rs`）：
   - 图标项：资料（开关左侧栏）、问答（开关右面板，徽章 = 上下文份数）、命令搜索（tools.search）、底部：准备交付（backstage export）；
   - 选中项 3pt 品牌左条（与 mock 一致）；hover/pressed 走 P1 令牌；键盘可达（widget_info + 焦点环 `focus_ring`）；
   - 只动面板显隐状态，不动任何命令逻辑。
2. **文件侧栏**：资源树迁入左栏（原位置移除），文件夹分组、版本号、「只读」徽标、正在阅读项品牌左条保持 P5 语义；
3. **OS 式格式图标**：`paint_file_icon()` 从单色改为分格式着色——PDF `#C4483C`、DOCX `#2B579A`、TXT/MD `text_dim`；白底 + 折角 + 底部格式字母（DESIGN-SYSTEM.md §2.7 补充条款）；
4. **导入入口**：侧栏底部常驻「＋ 导入资料」虚线按钮（复用现有 import 动作，不改合同）。

## 明确不做

- 不删旧"伪页签带"的命令注册（先摘 UI，命令保留，`view.navigationPane` 等仍可达）；
- 不改动正文画布与标签条（Q2 的范围）；
- 不动 AI 面板内容（Q3 的范围）。

## 验收

- `cargo check -p wordcraft-ui-egui` 通过；workspace 54/54 不回退；
- 深浅主题下 Activity Bar 三态（常规/hover/选中）可辨；侧栏开合宽度动画走 `motion::BASE` 且 `Animations::Off` 时瞬时完成。
