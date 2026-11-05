# 集成分支合并操作手册（P1–P6 → main）

> 目标分支：`codex/ui-design-system-20261105`（PR #6）→ main。全部子 PR 为草稿；合并决定权在 gong。
> 本序列已在本机完整预演（2026-11-05，rehearsal 分支）：**按下列顺序合并，六包全部 CLEAN，无冲突**。

## 合并顺序与每步验证

| 步骤 | PR | 分支 | 合并后本地验证 |
|---|---|---|---|
| 1 | #7 | `codex/ui-p1-tokens` | `cargo check -p wordcraft-ui-egui` |
| 2 | #11 | `codex/ui-p2-icons` | `cargo check`；图标 key 集合不变（P2 已静态对比） |
| 3 | #12 | `codex/ui-p3-motion` | `cargo check`；`Animations::Off` 下界面静止、无持续重绘 |
| 4 | #10 | `codex/ui-p4-chrome` | `cargo check`；Backstage 34/34；1366×768 与 1920×1080 功能区截图对比 |
| 5 | #9 | `codex/ui-p5-sources` | `cargo check`；workspace 54/54；分屏/关标签/跳转回退走查 |
| 6 | #8 | `codex/ui-p6-ai-surfaces` | `cargo check`；workspace 54/54；会话面板 busy/错误/满上下文三态目检 |

**顺序不可乱**：P2–P6 都包含 P1 的提交（变基而来），P6 还包含 P3 的提交（已预解 panes.rs 冲突并接入 busy 脉冲）。乱序合并会重复引入提交或造成可避免的冲突。

## 每个 PR 合并前的固定动作

1. 本地 `git fetch && git checkout <分支> && git pull`，确认 head 与 PR 页一致；
2. `cargo check -p wordcraft-ui-egui` 通过；
3. 该 PR 描述里列出的基线测试通过；
4. 在 GitHub 上把草稿标记为 Ready 并合并（squash 或 merge commit 均可，建议 merge commit 保留包边界）。

## 全部合并后（PR #6 → main 前）

- 完整 `cargo build`（Rust 1.95 + wasm32 + wasm-bindgen 0.2.129，见 integrations README）；
- Backstage 34/34、workspace 54/54、start-page 22/22 全量回归；
- 深浅双主题各截图一轮：开始页、功能区、文件页、资料阅读、会话面板；
- PR #6 标记 Ready，由 gong 决定合并。

## 已知暂缓项（不阻塞合并，合并后另行评估）

- 对话框窗口级 12pt 圆角（`theme::apply` 的 `window_corner_radius`，P6 记录）；
- 功能区按钮四态逐控件精修（P4 记录）；
- `lib.rs` / `canvas.rs` 的存量 animate 调用收口（P3 记录，缺安全的大文件写入通道）；
- P5/P6 同改 `project_workspace.rs` 的不同区段，预演无冲突，但合并后建议目检一次会话面板与资源树。
