# P5 · 资料域（sources workspace）

> 所属集成：`codex/ui-design-system-20261105` → 草稿 PR #6 → main。设计依据：`DESIGN-SYSTEM.md` §0（资料域）、§1。
> **云端施工，未经原生编译验证。** 依赖 P1。

## 范围

文件：`source_workspace.rs` 及 `source_workspace/` 子目录、`source_workspace/tabs.rs`（`ReaderGroup`）、`project_workspace.rs`（资源树 + 会话入口）。

施工点（VS Code 形态精修）：

1. **资源树**：行高保持 32px（原生布局契约，`file_row` 注释明确），选中态 = `checked` 底 + 3pt `accent` 左条（现状已实现，本包统一色值与圆角 `radius::BASE`）；文件图标（`paint_file_icon`）按图标规范重绘到半格网格。
2. **阅读标签页**：标签 strip 精修——活动标签 = `panel` 底 + 顶部 2pt `accent` 条，非活动 = `text_dim`；关闭按钮 hover 圆角；标签溢出与现状一致，不改交互。
3. **分组/分屏**：分组间分隔条 1pt `border`，拖动悬停 4pt `hover`；最大化态的视觉降级提示。
4. **只读纪律**：阅读视图所有表面必须明确"只读"——禁止出现文本光标样式的欺骗性可编辑感；页图（raster PDF）与文本模式切换控件的态规范。
5. **正文主权**：资料域任何强调色用量不得高于创作域活动页签；分屏时正文侧不做任何变暗/虚化。

## 明确不做

- 不改 `source.begin`/`source.commit` 桥接协议、身份/世代校验（KIMI-CONTRACT 红线）；
- 不改阅读位置记忆、跳转回退逻辑；
- 不加自动滚动、自动打开等任何"主动"行为。

## 验收

- `cargo check -p wordcraft-ui-egui` 通过；workspace 54/54 不回退；
- 分屏、关闭标签、跳转回退行为与施工前一致（本地人工路径走查）。
