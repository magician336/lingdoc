# P4 · 创作域外壳（chrome / ribbon / backstage）

> 所属集成：`codex/ui-design-system-20261105` → 草稿 PR #6 → main。设计依据：`DESIGN-SYSTEM.md` §0（创作域）、§1、§2。
> **云端施工，未经原生编译验证。** 依赖 P1（色值/常量）。

## 范围

文件：`chrome.rs`（1211 行）、`ribbon.rs`（3816 行）、`ribbon_fit.rs`、`backstage.rs`（1833 行）、`ruler_tabs.rs`、`status_bar` 相关（在 `chrome.rs` 或 `lib.rs` 内，先定位）、`keytips.rs`。

施工点：

1. **Word 骨架精修**：快捷访问栏、页签行（活动页签 = `accent` 文字 + 2pt 底部指示条）、功能区分组标签（`group_label` 用字阶 10.5 + 字距）、组间分隔线对齐 `border`。
2. **按钮态规范**：ribbon 按钮的 hover（`hover`）/按下（`pressed`）/选中（`checked`）/禁用（`text_disabled`）四态统一到令牌，圆角 `radius::BASE`，内边距走 `space` 网格。
3. **Backstage**（文件页）：左侧 `nav` 侧栏列表项态、右侧内容区卡片化（`radius::CARD` + `border`），标题用字阶 24。
4. **标尺与状态栏**：色值已由 P1 迁移；本包只处理刻度字阶与状态栏分隔符、缩放控件的对齐与间距。
5. **keytips**：徽标用 `accent`/`on_accent`，圆角 `radius::SMALL`。

## 明确不做

- 不改功能区命令模型、命令目录（`command_catalog.rs`）、布局算法（`ribbon_fit.rs` 的测量逻辑只读不改）；
- 不改页画布渲染；
- 所有字符串经 `tl!`，不新增硬编码英文。

## 验收

- `cargo check -p wordcraft-ui-egui` 通过；Backstage 34/34 不回退；
- 1366×768 与 1920×1080 下功能区折叠/展开行为不变（本地截图对比）；
- 深浅主题下四态齐全可辨。

## 施工记录（云端，2026-11-05）

**事故与恢复**：前序会话在 `9de9cb7` 误将 `ribbon.rs` 覆盖为单行文本，后续 5 个"restore"提交均未真正恢复。已由本会话以普通提交 `89d1fe8` 从 `1122c11` 恢复（恢复后 blob 与原文件 `397a9f2e` 逐字节一致），未强推、未删历史。

**已完成**：

- `chrome.rs`（前序会话 `1122c11`，已保留）：快捷访问栏三态 + 焦点环、搜索框焦点环、状态栏间距令牌化；
- `ribbon.rs`（本会话）：分享按钮 hover/pressed 统一走 `accent_hover`（原先误用文字色 `accent_text` 作填充）；活动页签指示条按规范收为 2pt 贴底；
- `backstage.rs`（本会话）：文件页深蓝导航的活动项增加 3pt 左侧定位条（白色，深蓝底上的 accent 类比）。

**检查后无需改动**：`ruler_tabs.rs` 无硬编码颜色；`keytips.rs` 徽标已是 `accent`/`on_accent` + 圆角；功能区分组标题与分隔线已在 P1 随 `group_label`/`border` 令牌迁移。

**暂缓**：功能区按钮四态的逐控件精修（`ribbon.rs` 内 3800 行的按钮绘制分布广，建议本地可视化回归后专项进行）；`ribbon_fit.rs` 测量逻辑按计划保持只读。
