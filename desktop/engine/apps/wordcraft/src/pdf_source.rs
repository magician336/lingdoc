//! Native PDF text extraction and high-fidelity page rendering via Google PDFium (`pdfium-render`).
//!
//! `pdfium.dll` is loaded dynamically at runtime so missing binaries degrade gracefully without
//! crashing or affecting TXT/MD/DOCX workflows.

use std::io::Cursor;
use std::path::{Path, PathBuf};

use image::ImageFormat;
use pdfium_render::prelude::*;

const MAX_TEXT_PAGES: usize = 200;
const MAX_TEXT_CHARS: usize = 2_000_000;
/// Guard against pathological single pages before the full-string allocation in `all()`:
/// a normal A4 page holds a few thousand characters; 100k is already book-chapter scale.
const MAX_TEXT_CHARS_PER_PAGE: usize = 100_000;
/// Matches `SourceWorkspace::load_pages` limits (max 64 pages, <= 32 MiB encoded PNGs).
const MAX_RENDER_PAGES: usize = 64;
const MAX_RENDER_TOTAL_BYTES: usize = 30 * 1024 * 1024;
/// The reader rejects the whole open above 32 Mi cumulative pixels; stay under it so a
/// normal ten-page document at 1400x2400 still fits (10 x 3.36M = 33.6M would fail there).
const MAX_RENDER_TOTAL_PIXELS: u64 = 30 * 1024 * 1024;
const RENDER_TARGET_WIDTH: i32 = 1400;

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum PdfTextResult {
    Text(String),
    ScannedOnly { page_count: usize },
}

fn candidate_dll_paths() -> Vec<PathBuf> {
    let mut paths = Vec::new();
    if let Ok(explicit) = std::env::var("LINGDOC_PDFIUM_DLL")
        && !explicit.trim().is_empty()
    {
        paths.push(PathBuf::from(explicit));
    }
    if let Ok(exe) = std::env::current_exe()
        && let Some(dir) = exe.parent()
    {
        paths.push(dir.join("pdfium.dll"));
    }
    paths.push(PathBuf::from(r"D:\Mon3tr\projects\lingdoc-dachuang\.tooling\pdfium\pdfium.dll"));
    paths
}

fn bind_pdfium() -> Result<Pdfium, String> {
    for candidate in candidate_dll_paths() {
        if candidate.is_file() {
            let bindings = Pdfium::bind_to_library(&candidate)
                .map_err(|error| format!("无法加载 PDF 引擎 {}：{error}", candidate.display()))?;
            return Ok(Pdfium::new(bindings));
        }
    }
    Pdfium::bind_to_system_library()
        .map(Pdfium::new)
        .map_err(|_| "未找到 pdfium.dll，暂无法解析或渲染 PDF；请将 pdfium.dll 放在程序同级目录或 .tooling\\pdfium\\ 目录。".to_owned())
}

/// Extract per-page text on demand for project Q&A and rewrite-with-sources.
/// Pages are annotated with `[第 N 页]` headers so answers can cite approximate page numbers.
pub fn extract_pdf_text(path: &Path) -> Result<PdfTextResult, String> {
    let pdfium = bind_pdfium()?;
    let document = pdfium
        .load_pdf_from_file(path, None)
        .map_err(|error| format!("PDF 打开失败：{error}"))?;
    let page_count = document.pages().len() as usize;
    let limit = page_count.min(MAX_TEXT_PAGES);
    let mut out = String::new();
    let mut total_chars = 0usize;
    let mut access_failures = 0usize;

    for index in 0..limit {
        let Ok(page) = document.pages().get(index as u16) else {
            access_failures = access_failures.saturating_add(1);
            continue;
        };
        let Ok(text_obj) = page.text() else {
            access_failures = access_failures.saturating_add(1);
            continue;
        };
        // Check the budget before `all()` allocates the full page string: a hostile page
        // would otherwise exhaust memory before any limit could fire.
        let page_chars = text_obj.len() as usize;
        let header_room = 12; // "[第 123456 页]\n" plus the paragraph separator
        let remaining = MAX_TEXT_CHARS.saturating_sub(total_chars);
        if page_chars > MAX_TEXT_CHARS_PER_PAGE || page_chars.saturating_add(header_room) > remaining {
            break;
        }
        let page_text = text_obj.all();
        let trimmed = page_text.trim();
        if trimmed.is_empty() {
            continue;
        }
        let header = format!("[第 {} 页]\n", index + 1);
        let added = header.chars().count().saturating_add(trimmed.chars().count()).saturating_add(2);
        total_chars = total_chars.saturating_add(added);
        if !out.is_empty() {
            out.push_str("\n\n");
        }
        out.push_str(&header);
        out.push_str(trimmed);
    }

    if out.trim().is_empty() {
        // Only report a scanned-only document when the pages were actually readable but carried
        // no text; total access failure is a parse error, not a scan.
        if limit > 0 && access_failures >= limit {
            Err(format!("PDF 解析失败：{limit} 页均无法读取，文件可能已损坏或受密码保护"))
        } else {
            Ok(PdfTextResult::ScannedOnly { page_count })
        }
    } else {
        Ok(PdfTextResult::Text(out))
    }
}

/// Render up to 64 PDF pages into high-clarity PNG byte buffers for `SourceWorkspace::load_pages`.
pub fn render_pdf_pages(path: &Path) -> Result<Vec<Vec<u8>>, String> {
    let pdfium = bind_pdfium()?;
    let document = pdfium
        .load_pdf_from_file(path, None)
        .map_err(|error| format!("PDF 打开失败：{error}"))?;
    let page_count = document.pages().len() as usize;
    if page_count == 0 {
        return Err("PDF 文件没有任何页面。".to_owned());
    }
    let limit = page_count.min(MAX_RENDER_PAGES);
    let render_config = PdfRenderConfig::new()
        .set_target_width(RENDER_TARGET_WIDTH)
        .set_maximum_height(2400)
        .render_form_data(true)
        .render_annotations(true);

    let mut pages = Vec::with_capacity(limit);
    let mut total_bytes = 0usize;
    let mut total_pixels = 0u64;
    for index in 0..limit {
        let page = document
            .pages()
            .get(index as u16)
            .map_err(|error| format!("PDF 第 {} 页读取失败：{error}", index + 1))?;
        let bitmap = page
            .render_with_config(&render_config)
            .map_err(|error| format!("PDF 第 {} 页渲染失败：{error}", index + 1))?;
        let dynamic = bitmap.as_image();
        // The downstream reader rejects the whole open above its cumulative pixel cap, so stop
        // adding pages before crossing ours rather than failing the entire document there.
        let pixels = (dynamic.width() as u64).saturating_mul(dynamic.height() as u64);
        if total_pixels.saturating_add(pixels) > MAX_RENDER_TOTAL_PIXELS {
            break;
        }
        let mut buffer = Cursor::new(Vec::new());
        dynamic
            .write_to(&mut buffer, ImageFormat::Png)
            .map_err(|error| format!("PDF 第 {} 页 PNG 编码失败：{error}", index + 1))?;
        let bytes = buffer.into_inner();
        match total_bytes.checked_add(bytes.len()) {
            Some(next) if next <= MAX_RENDER_TOTAL_BYTES => total_bytes = next,
            _ => break,
        }
        total_pixels = total_pixels.saturating_add(pixels);
        pages.push(bytes);
    }
    if pages.is_empty() {
        return Err("PDF 页面超出原生阅读器内存上限，暂无法渲染。".to_owned());
    }
    Ok(pages)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn real_pdf_extracts_text_and_renders_valid_png_pages() {
        let sample = Path::new(r"D:\Mon3tr\projects\lingdoc-dachuang\DEMO-15MIN-DRAFT.pdf");
        if !sample.is_file() {
            return;
        }
        let pages = render_pdf_pages(sample).expect("PDF pages should render with pdfium.dll");
        assert!(!pages.is_empty(), "rendered PDF should contain at least one page");
        assert!(pages[0].starts_with(b"\x89PNG\r\n\x1a\n"), "rendered page must be valid PNG");

        let extracted = extract_pdf_text(sample).expect("PDF text extraction should succeed");
        match extracted {
            PdfTextResult::Text(text) => assert!(text.contains("[第 1 页]")),
            PdfTextResult::ScannedOnly { page_count } => assert!(page_count > 0),
        }
    }

    /// A real Chinese academic PDF must extract its actual text content, not just page markers.
    #[test]
    fn chinese_academic_pdf_extracts_real_content() {
        let sample = Path::new(r"D:\Mon3tr\projects\lingdoc-dachuang\references\申报标杆-国家社科基金2026\1.2026年国家社会科学基金年度项目重点研究方向.pdf");
        if !sample.is_file() {
            return;
        }
        match extract_pdf_text(sample).expect("Chinese academic PDF should extract") {
            PdfTextResult::Text(text) => {
                assert!(text.contains("[第 1 页]"), "per-page markers expected");
                assert!(text.contains("社会科学"), "extracted text must carry real Chinese content");
            }
            PdfTextResult::ScannedOnly { .. } => panic!("acceptance sample must be a text PDF"),
        }
    }

    /// A corrupt file must surface a Chinese error, never a panic (never-crash standard).
    #[test]
    fn corrupt_pdf_returns_error_instead_of_panicking() {
        if !Path::new(r"D:\Mon3tr\projects\lingdoc-dachuang\.tooling\pdfium\pdfium.dll").is_file() {
            return;
        }
        let dir = std::env::temp_dir().join(format!("lingdoc-pdf-source-test-{}", std::process::id()));
        std::fs::create_dir_all(&dir).expect("temp dir");
        let corrupt = dir.join("corrupt.pdf");
        std::fs::write(&corrupt, b"%PDF-1.7\nthis is not a real pdf body\x00\x01\x02").expect("write corrupt pdf");

        let text = extract_pdf_text(&corrupt);
        assert!(text.is_err(), "corrupt PDF must not extract text");
        assert!(render_pdf_pages(&corrupt).is_err(), "corrupt PDF must not render pages");

        std::fs::remove_dir_all(&dir).expect("cleanup temp dir");
    }
}
