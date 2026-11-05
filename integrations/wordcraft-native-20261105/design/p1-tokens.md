# P1 · 设计令牌层（tokens）

> 所属集成：`codex/ui-design-system-20261105` → 草稿 PR #6 → main。设计依据：`integrations/wordcraft-native-20261105/DESIGN-SYSTEM.md` §2、§3。
> **本包已在云端完成施工；未经原生编译验证，合并前需本地 `cargo check -p wordcraft-ui-egui`。**

## 范围

只改 `engine/crates/ui-egui/src/theme.rs`：

1. `APP_COLOR`/`APP_INK` 保持引擎既有蓝 `#0064E6`/`#0756B8`（蓝白品牌方向，全库无其他直接引用点，已核实），注释更新为设计系统口径。
2. `Tokens::light()` / `Tokens::dark()` 全部色值迁移到暖灰中性色阶，品牌强调色统一为蓝白方向（checked 选中底、selection 等一并换系；语义色独立于品牌色）。
3. `Tokens` 新增 6 个字段：`accent_hover`、`focus_ring`、`success`、`warning`、`danger`、`info`（两个构造函数同步补齐；已核实全库仅这两个字面构造点）。
4. 新增常量模块 `space`（4–32pt 网格）、`radius`（2–12）、`motion`（0.12s/0.18s/0.26s/1.4s，秒制，供 `ctx.animate_*` 使用）。

## 明确不做

- 不改 `Visuals` 安装逻辑、字体加载、`Appearance`/`InterfaceSize`/`Animations` 设置项；
- 不动任何调用方文件（P4–P6 才消费新字段与常量）；
- 不删除旧字段、不改字段类型。

## 验收

- `cargo check -p wordcraft-ui-egui` 通过；
- Backstage 34/34、workspace 54/54 基线不回退；
- 深浅主题切换正常，新增字段在浅色下正文对比度 ≥ 4.5:1。
