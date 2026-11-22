# LingDoc Native Desktop Client (`desktop/`)

Rust / egui 原生桌面端，从 `Lees-42/lingdoc-dachuang` 稳定演示基线 `bbb539c6` 完整导入，与根目录 Go 服务端及 Vue `frontend/` 完全解耦、互不影响。

## 核心能力（基线 `bbb539c6`）

- **唯一主 DOCX 原生编辑器**：基于项目目录 + 单一权威主文稿（无 WorkingCopy、无 `project.json`），支持原子保存、滚动备份（`<主文件名>.bak`）、独立 Recovery 与 OS 项目锁（`.lingdoc.lock`）。
- **项目资料问答**：默认读取当前主文稿内存快照 + 全部可读取项目资料，内置确定性分块检索、会话历史与流式输出，支持点击已用资料直达内置阅读器。
- **选区 AI 辅助**：解释 / 总结 / 翻译 / 润色 / 改写；结果浮层严格只读，支持跨选区固定与转入项目问答。
- **原生 PDF 导入与阅读**：按需惰性提字进入问答，并通过 PDFium 高保真渲染原版页图至内置阅读器（只读、缩放、翻页）。
- **AI 绘图工作台集成**：支持按需启动本地 `drawing-upstream` 工作台、级联编辑与自动保存。

## 目录结构

```text
desktop/
├─ engine/            # Rust workspace（核心文档引擎与 wordcraft 桌面宿主）
├─ drawing-upstream/  # 可选 AI 绘图工作台覆盖补丁与构建脚本
└─ docs/              # 原生端产品决策、开发路由、PDF 方案与运维说明
```

## 构建与单元测试

在 `desktop/engine/` 下执行：

```bash
cargo test -p wordcraft --bin wordcraft -- project_ai_host deepseek pdf_source drawing_runtime
cargo test -p wordcraft-ui-egui --lib -- project_workspace ai_popover ai::tests
```

Windows 下推荐通过 `CARGO_TARGET_DIR` 指定统一构筑目录，避免在 `desktop/engine/` 内产生构筑缓存。详见 [docs/OPERATIONS.md](docs/OPERATIONS.md) 与 [docs/DECISIONS.md](docs/DECISIONS.md)。

## 可选运行时依赖

- **文本模型（DeepSeek）**：通过系统凭据库或 `DEEPSEEK_API_KEY` 环境变量配置；未配置时编辑与本地阅读功能照常可用。
- **PDF 原生提字与页图渲染**：需要同级或 `LINGDOC_PDFIUM_DLL` / `.tooling/pdfium/pdfium.dll`；缺失时优雅降级报错，不影响 TXT / Markdown / DOCX。
- **AI 绘图工作台**：仅在主动打开 AI 绘图时按需依赖 Node.js 与 `drawing-upstream/prepare.mjs --build` 产物。
