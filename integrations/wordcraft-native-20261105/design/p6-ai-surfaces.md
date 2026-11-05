# P6 · 智能域与浮层（AI surfaces / panes / dialogs）

> 所属集成：`codex/ui-design-system-20261105` → 草稿 PR #6 → main。设计依据：`DESIGN-SYSTEM.md` §0（智能域）、§1（显式优于自动、状态诚实）。
> **云端施工，未经原生编译验证。** 依赖 P1。

## 范围

文件：`project_workspace.rs` 的会话部分、`panes.rs`、`dialogs.rs` 与 `dialogs_*.rs`（约 12 个文件）、`widgets.rs`、`mini_toolbar.rs`、`compact_surface.rs`。

施工点：

1. **会话面板**：消息气泡/列表层级精修；`busy` 态用 `motion::PULSE` 呼吸点（与 P3 的公共脉冲一致）；`error` 态用 `danger` + 明确重试提示（失败发送不自动重试，KIMI-CONTRACT）。
2. **上下文 chips**：每个上下文来源是一枚可移除 chip——`brand/50` 底、`accent_text` 文字、移除 ✕ 的 hover `danger`；预算接近上限时 chip 区整体转 `warning` 提示条。
3. **出处引用**：`usedSources` 渲染为可点击出处卡（`info` 链接色），注明"真实送入模型的资料，不等于逐条引用"的既有语义不得改写。
4. **对话框体系**：全部 `dialogs_*.rs` 统一到 `radius::DIALOG`、标题字阶 17、按钮主/次规范（主按钮 `accent`/`on_accent`，悬停 `accent_hover`）；焦点环用 `focus_ring`。
5. **mini_toolbar / compact_surface**：小窗态的令牌对齐，不加新功能。

## 明确不做

- 不接新模型、不改上下文预算（4 份 / 12000 字符 / 单问 4000 字符）与任何提示文案的事实内容；
- 不新增"AI 主动建议"入口（设计原则 §1.3）；
- 不改会话历史的持久化逻辑。

## 验收

- `cargo check -p wordcraft-ui-egui` 通过；workspace 54/54 不回退；
- busy/error/超预算三态在本地真实链路下目检（需本地模型配置，云上不声称已验证）。

## 施工记录（云端，2026-11-05）

已改：

- `project_workspace.rs`：上下文区改为 chip 形态（`checked` 弱强调底、6pt 圆角、移除按钮危险色、标题为品牌色幽灵按钮）；「参考资料（n / 4）」满 4 份时计数转 `warning`；消息气泡分角色（用户 `hover` 底 + 品牌色角色标，AI 保持纸面）；出处卡 6pt 圆角、「查看出处/打开资料」用 `info` 色；错误条独立成块（`danger` 弱底 + 描边 + 圆角，文案仍为宿主原文，不自动重试）；发送按钮 4pt 圆角。
- `widgets.rs`：`primary_button` 统一 4pt 圆角与 28pt 最小高度（全库对话框主按钮都经过它）。
- `panes.rs`：10 处侧面板框架内边距统一到 12pt（space/M 网格）。

暂缓（留给后续包，原因写明）：

- 对话框窗口级圆角（`radius::DIALOG` 12pt）：由 `theme::apply` 的 `window_corner_radius` 全局驱动，属 P1 的 theme.rs 范围，本包不越界改；
- busy 呼吸脉冲（`motion::PULSE`）：等 P3 的 a11y 判定函数落地后统一接，本包暂保留 `ui.spinner()`；
- `dialogs_*.rs` 各对话框内部排版微调：共享助手已改，逐个对话框的字段级精修收益低、回归面大，建议合并后再评估。

未改动任何 enqueue 动作、JSON 合同、测试注册的命中矩形字段名与文案；`mini_toolbar.rs` / `compact_surface.rs` 检查后确认已是令牌驱动，无需改动。
