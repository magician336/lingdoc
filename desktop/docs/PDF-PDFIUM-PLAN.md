# LingDoc 原生 PDF 支持开发方案（PDFium）

- **工作分支**：`codex/pdf-pdfium-20261120`
- **工作树**：`D:\Mon3tr\projects\lingdoc-dachuang\worktrees\pdf-pdfium-20261120`
- **基线提交**：`639dc0cb`（`codex/current-7241-20261116`）
- **独立构建目录**：`D:\Mon3tr\projects\lingdoc-dachuang\.tooling\target-pdf-pdfium`
- **产品红线依据**：[DECISIONS.md](DECISIONS.md)（DOCX 唯一正文真值、打开/导入不为 AI 预抽全文、不引入 Python/外部常驻服务、不造假引用）

---

## 1. 目标与选型结论

目标是在 Rust/egui 原生桌面端提供接近市面成熟产品（Chrome / UPDF / 学术阅读器）的 PDF 资料阅读与 AI 上下文能力，采用 **`pdfium-render`（Google PDFium 动态绑定）**：

- **协议友好**：BSD / MIT / Apache-2.0（规避 MuPDF 的 AGPL-3.0 传染风险）。
- **学术场景稳定**：Chrome 同款渲染与提字引擎，完整支持中文、双栏排版、LaTeX 数学公式和内嵌 CJK 字体。
- **架构契合**：零 Python、零外部服务进程；Windows 运行时仅需同级目录或工具目录提供单个预编译 `pdfium.dll`（约 10–15 MB）。

---

## 2. 运行时 `pdfium.dll` 加载策略

新增独立模块 `ENGINE/apps/wordcraft/src/pdf_source.rs` 统一封装 PDFium 绑定与所有 PDF 操作，按以下顺序查找 `pdfium.dll`：

1. 环境变量 `LINGDOC_PDFIUM_DLL`（便于隔离测试与自定义路径）；
2. 当前可执行文件同级目录（`<exe_dir>/pdfium.dll`，正式分发默认路径）；
3. 本机开发工具目录（`D:\Mon3tr\projects\lingdoc-dachuang\.tooling\pdfium\pdfium.dll`）。

**缺失降级**：若本机未放置 `pdfium.dll`，不得崩溃；导入或打开 PDF 时返回明确中文提示，TXT / MD / DOCX 全部功能不受影响。

---

## 3. 两阶段实施切片

### Phase 1：导入 + 高精度提字进 AI + 原版高清并排阅读（本轮优先）

1. **资料导入（懒加载不变）**
   - `APP/project_ai_host.rs`：`import_one()` 放开 `.pdf` 扩展名，仅复制原件并写 `source.json`；**导入与打开项目时不解析、不预抽全文**。
   - `APP/file_dialogs.rs`：确认资料选择弹窗过滤列表包含 `pdf`。
2. **按需文本提取（接入项目问答 & 改写选资料）**
   - `APP/pdf_source.rs`：提供 `extract_pdf_text(path: &Path) -> Result<PdfTextResult, String>`：
     - 逐页提取文本并保留 `[第 N 页]` 分隔标记；
     - 限制单文档最大页数/字符上限，防止超大畸形文件耗尽内存；
     - 若总有效文本为空（纯扫描图片型 PDF），返回 `PdfTextResult::ScannedOnly` 而非静默失败。
   - `APP/project_ai_host.rs`：`extract_text()` 接入 `pdf_source::extract_pdf_text`；后台线程构建上下文时，遇到纯扫描件跳过其文本并在上下文/状态中诚实标注“扫描件仅支持阅读，暂不支持问答检索”，不阻断其他可读资料。
   - `APP/native_features.rs`：`source_text_for_ai()` 接入 PDF 提字，使选区「改写」配置浮层勾选的 PDF 资料也能正常参与改写（扫描件返回明确提示）。
3. **原版高清页图阅读（复用现有 `SourceWorkspace`）**
   - `APP/pdf_source.rs`：提供 `render_pdf_pages(path: &Path, max_pages: usize) -> Result<Vec<Vec<u8>>, String>`，使用高 DPI（如 144–192 DPI，限制单页最大像素尺寸与 `SourceWorkspace` 的 64 页 / 32 MiB PNG 预算）渲染为 PNG 字节数组。
   - `APP/native_features.rs`：`open_source()` 识别 `.pdf` 后调用 `render_pdf_pages` 并传入 `SourceInput::Pages`，直接复用 `UI/source_workspace.rs` 现有的 PDF 页图阅读器（翻页、缩放、并排阅读、阅读位置记忆）。

### Phase 2：对标商业阅读器的高级交互（Phase 1 验收后推进）

1. **PDF 原版页图划词复制与划词问 AI**：通过 PDFium 字符边界框在页图上叠加透明选区层，支持拖选文字后右键「就这段问 AI」和「复制（带页码出处）」。
2. **页内搜索高亮与大纲书签跳转**：支持 `Ctrl+F` 命中高亮及 PDF 内置目录树跳转。

---

## 4. 验证与验收清单（Phase 1）

- **单元测试（`cargo test -p wordcraft --bin wordcraft`）**：
  - `pdf_source`：正常文字型 PDF 逐页提字（含中文/多页标记）与 PNG 页图渲染；
  - `pdf_source`：损坏 PDF 与缺失 `pdfium.dll` 时明确报错且不崩溃；
  - `project_ai_host`：导入 `.pdf` 不预抽全文，发问时按需提字进入请求上下文。
- **隔离构建与真机验收**：
  - 在 `.tooling\target-pdf-pdfium` 构建独立 `wordcraft.exe`；
  - 使用隔离项目导入真实中文学术 PDF，验证：导入成功 → 内置阅读器高清并排显示 → 项目问答能回答 PDF 内容 → 改写勾选 PDF 生效。
- **未覆盖项明确声明**：纯扫描版 PDF 不含 OCR，仅支持阅读不支持检索；Phase 2 划词与书签搜索在下一切片实现。
