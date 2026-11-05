# P3 · 动效层（motion）

> 所属集成：`codex/ui-design-system-20261105` → 草稿 PR #6 → main。设计依据：`DESIGN-SYSTEM.md` §2.6。
> **云端施工，未经原生编译验证。** 依赖 P1 已合入（`theme::motion` 常量与 `Tokens::focus_ring`）。

## 范围

1. **a11y 收口**：`theme.rs` 已有 `Animations`（System/On/Off）设置项（约 184 行起）。找到它的读取点，提供一个公共判定函数（如 `pub fn animations_enabled(ctx) -> bool`），让动效消费者统一经过它；Off 时所有过渡瞬时完成。
2. **存量调用统一到令牌**：`lib.rs`（5 处）、`canvas.rs`（8 处）、`scroll.rs`（11 处）、`widgets.rs`（3 处）中现存的 `animate_*` 调用，时长常量替换为 `motion::FAST/BASE/SLOW`；编辑画布与滚动本身的动画只换常量、不改行为。
3. **新增克制的反馈动效**（仅以下三处，逐点接入）：
   - 按钮/列表项 hover 背景渐变（`motion::FAST`）；
   - 面板开合（`motion::BASE`，挂 `project_workspace` / `panes` 的显隐点）；
   - 保存中/AI 生成中的状态点脉冲（`motion::PULSE`，取真实回执状态，不得用动画伪造"已保存"——KIMI-CONTRACT 红线）。

## 明确不做

- 编辑画布、标尺、页内容不加任何动画；
- 不引入弹簧物理或第三方动画库；
- 不改变任何状态机逻辑，只消费已有状态。

## 验收

- `cargo check -p wordcraft-ui-egui` 通过；
- `Animations::Off` 下全部动画瞬时完成（本地人工验证）；
- 无持续重绘泄漏：静止界面不触发逐帧 repaint（用 egui 的 repaint 计数或本地观察 CPU）。
