//! One persistent editable document and an independent, read-only source view.
//! Page images are supplied by the host; this module never opens them in the document session.

use std::collections::{BTreeMap, VecDeque};

use egui::{ColorImage, Rect, RichText, Sense, Stroke, TextureHandle, Ui, UiBuilder, Vec2, pos2, vec2};
use serde_json::{Value, json};

use crate::WordApp;
use crate::theme::{Tokens, medium, radius};

mod tabs;
use tabs::ReaderGroup;

const MAX_PAGES: usize = 64;
const MAX_ENCODED: usize = 32 * 1024 * 1024;
const MAX_PIXELS: u64 = 32 * 1024 * 1024;
const MAX_TEXT_BYTES: usize = 2 * 1024 * 1024;
const MAX_READING_POSITIONS: usize = 32;

#[derive(Clone)]
struct ReadingPosition {
    page: usize,
    zoom: f32,
    positions: BTreeMap<usize, Vec2>,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
enum Mode {
    #[default]
    Body,
    Source,
    Split,
}

pub enum SourceInput {
    Pages { id: String, title: String, version: String, pages: Vec<Vec<u8>> },
    Text { id: String, title: String, version: String, text: String },
}

#[derive(Default)]
pub struct SourceWorkspace {
    readers: BTreeMap<String, Box<SourceWorkspace>>,
    groups: Vec<ReaderGroup>,
    focus_group: u64,
    maximized: Option<u64>,
    next_group: u64,
    closed_tabs: Vec<(String, u64, usize)>,
    jump_back: Option<(String, u64, usize, Vec2)>,
    group_rects: Vec<(u64, Rect)>,
    groups_viewport_rect: Option<Rect>,
    tab_rects: BTreeMap<String, Rect>,
    group_menu_rects: BTreeMap<u64, Rect>,
    body_fit_rect: Option<Rect>,
    body_actual_size_rect: Option<Rect>,
    multi_error: String,
    id: String,
    title: String,
    version: String,
    pages: Vec<ColorImage>,
    text: Option<String>,
    texture: Option<(usize, TextureHandle)>,
    mode: Mode,
    pub active_source: bool,
    pub source_rect: Option<Rect>,
    page: usize,
    zoom: f32,
    positions: BTreeMap<usize, Vec2>,
    reading_positions: VecDeque<((String, String), ReadingPosition)>,
    view_epoch: u64,
    body_tab_rect: Option<Rect>,
    body_rect: Option<Rect>,
    body_fit_width: bool,
    markdown_raw: bool,
    markdown_toggle_rect: Option<Rect>,
    body_page_rects: Vec<Rect>,
    source_page_rect: Option<Rect>,
}

impl SourceWorkspace {
    /// Decode into temporary storage, so a rejected input cannot replace the current source.
    pub fn load_pages(&mut self, id: String, title: String, version: String, pages: Vec<Vec<u8>>) -> Result<(), String> {
        Self::check_identity(&id, &title, &version)?;
        if pages.is_empty() || pages.len() > MAX_PAGES {
            return Err("source must contain 1–64 rendered pages".into());
        }
        let mut encoded = 0usize;
        let mut pixels = 0u64;
        let mut decoded = Vec::new();
        for data in &pages {
            encoded = encoded.checked_add(data.len()).ok_or("source size overflow")?;
            if encoded > MAX_ENCODED || data.get(..8) != Some(b"\x89PNG\r\n\x1a\n") || data.get(12..16) != Some(b"IHDR") {
                return Err("source page is not PNG or exceeds the 32 MiB input limit".into());
            }
            let dimension = |start| -> Result<u32, String> {
                let bytes: [u8; 4] = data.get(start..start + 4).ok_or("truncated PNG header")?.try_into().map_err(|_| "invalid PNG header")?;
                Ok(u32::from_be_bytes(bytes))
            };
            let (w, h) = (dimension(16)?, dimension(20)?);
            pixels = pixels.checked_add(u64::from(w) * u64::from(h)).ok_or("source dimensions overflow")?;
            if w == 0 || h == 0 || w > 8192 || h > 8192 || pixels > MAX_PIXELS {
                return Err("source raster dimensions exceed the reading limit".into());
            }
            let image = image::load_from_memory_with_format(data, image::ImageFormat::Png)
                .map_err(|e| format!("cannot decode source page: {e}"))?
                .into_rgba8();
            decoded.push(ColorImage::from_rgba_unmultiplied([w as usize, h as usize], image.as_raw()));
        }
        self.check_cache_budget(&id, &version, pixels)?;
        self.change_identity(id, title, version);
        self.pages = decoded;
        self.text = None;
        self.texture = None;
        self.page = self.page.min(self.pages.len().saturating_sub(1));
        self.mode = Mode::Body;
        self.active_source = false;
        self.register_current();
        self.view_epoch = self.view_epoch.saturating_add(1);
        Ok(())
    }

    fn check_identity(id: &str, title: &str, version: &str) -> Result<(), String> {
        if id.trim().is_empty() || title.trim().is_empty() || version.trim().is_empty() || id.len() > 512 || title.len() > 1024 || version.len() > 512
        {
            return Err("source identity/title/version is missing or too long".into());
        }
        Ok(())
    }

    /// Exact plain-text content, not a paginated facsimile or a generated summary.
    pub fn load_text(&mut self, id: String, title: String, version: String, text: String) -> Result<(), String> {
        Self::check_identity(&id, &title, &version)?;
        if text.len() > MAX_TEXT_BYTES {
            return Err("source text exceeds the 2 MiB reading limit".into());
        }
        self.check_cache_budget(&id, &version, 0)?;
        self.change_identity(id, title, version);
        self.pages.clear();
        self.texture = None;
        self.text = Some(text);
        self.page = 0;
        self.mode = Mode::Body;
        self.active_source = false;
        self.register_current();
        self.view_epoch = self.view_epoch.saturating_add(1);
        Ok(())
    }

    /// Reserve a new asynchronous source request without changing the visible view.
    pub fn begin_open(&mut self) -> u64 {
        self.view_epoch = self.view_epoch.saturating_add(1);
        self.view_epoch
    }

    /// Commit payload, page and final layout as one native operation. Native tab changes
    /// and newer requests invalidate the token even while the host is awaiting I/O.
    pub fn commit_open(&mut self, expected_epoch: u64, input: SourceInput, mode: &str, page: Option<usize>) -> Result<(), String> {
        if expected_epoch != self.view_epoch {
            return Err("source request superseded".into());
        }
        let preserve_layout = mode == "keep";
        let mode = match mode {
            "keep" => self.mode,
            "body" => Mode::Body,
            "source" => Mode::Source,
            "split" => Mode::Split,
            _ => return Err("mode must be body, source, split or keep".into()),
        };
        match &input {
            SourceInput::Pages { pages, .. } if page.is_some_and(|p| p == 0 || p > pages.len()) => {
                return Err("source page is outside the loaded range".into());
            }
            SourceInput::Text { .. } if page.is_some() => return Err("plain text has no page numbers".into()),
            _ => {}
        }
        let return_point = if page.is_some() {
            self.groups.iter().find(|g| g.id == self.focus_group).map(|g| {
                let reader = if self.has_source() && self.key() == g.active { Some(&*self) } else { self.readers.get(&g.active).map(Box::as_ref) };
                (g.active.clone(), g.id, reader.map_or(0, |r| r.page), reader.and_then(|r| r.positions.get(&r.page).copied()).unwrap_or(Vec2::ZERO))
            })
        } else { None };
        match input {
            SourceInput::Pages { id, title, version, pages } => self.load_pages(id, title, version, pages)?,
            SourceInput::Text { id, title, version, text } => self.load_text(id, title, version, text)?,
        }
        if let Some(page) = page {
            self.page = page - 1;
        }
        if !preserve_layout { self.set_view_mode(match mode {Mode::Body=>"body",Mode::Source=>"source",Mode::Split=>"split"})?; }
        if preserve_layout || mode != Mode::Body {let key=self.key();self.multi_action(&json!({"action":"activate","key":key}))?;}
        if return_point.is_some() { self.jump_back = return_point; }
        Ok(())
    }

    pub fn has_source(&self) -> bool {
        self.text.is_some() || !self.pages.is_empty()
    }

    // Preserve each exact-version reader and its lightweight fallback position.
    fn change_identity(&mut self, id: String, title: String, version: String) {
        if self.has_source() {
            let old_key = (self.id.clone(), self.version.clone());
            self.reading_positions.retain(|(key, _)| key != &old_key);
            self.reading_positions.push_back((old_key, ReadingPosition { page: self.page, zoom: self.zoom, positions: self.positions.clone() }));
            while self.reading_positions.len() > MAX_READING_POSITIONS {
                self.reading_positions.pop_front();
            }
        }
        let target_key = json!([id,version]).to_string();
        let cached = (self.has_source() && self.key()==target_key) || self.readers.contains_key(&target_key);
        self.remember_current_before_load(&id, &version);
        let key = (id.clone(), version.clone());
        if cached {
            // Preserve the newest cached reader state instead of an older lightweight snapshot.
        } else if let Some((_, saved)) = self.reading_positions.iter().find(|(old_key, _)| old_key == &key) {
            self.page = saved.page;
            self.zoom = saved.zoom;
            self.positions = saved.positions.clone();
        } else {
            self.page = 0;
            self.zoom = 1.0;
            self.positions.clear();
        }
        self.id = id;
        self.title = title;
        self.version = version;
        if !cached { self.markdown_raw = false; }
    }

    pub fn set_mode(&mut self, mode: &str) -> Result<(), String> { self.set_view_mode(mode) }

    fn check_cache_budget(&self, id: &str, version: &str, incoming: u64) -> Result<(), String> {
        let key = json!([id, version]).to_string();
        let count = self.readers.len() + usize::from(self.has_source());
        if count >= 16 && self.key() != key && !self.readers.contains_key(&key) { return Err("最多缓存16份资料；关闭不需要的标签后重新打开项目可释放缓存。".into()); }
        let pixels = |r: &Self| r.pages.iter().map(|p| p.pixels.len() as u64).sum::<u64>();
        let resident = self.readers.iter().filter(|(k,_)| **k != key).map(|(_,r)| pixels(r)).sum::<u64>() + if self.key()!=key {pixels(self)} else {0};
        if resident.saturating_add(incoming) > MAX_PIXELS * 2 {return Err("资料阅读缓存达到256MiB像素上限；请关闭部分标签并重新打开项目。".into());}
        Ok(())
    }

    pub fn source_active(&self) -> bool {
        self.has_source() && self.active_source && self.mode != Mode::Body
    }

    /// Navigate to a 1-based source page; its existing scroll offset is preserved.
    pub fn set_page(&mut self, page: usize) -> Result<(), String> {
        if page == 0 || page > self.pages.len() {
            return Err("source page is outside the loaded range".into());
        }
        let next = page - 1;
        if next != self.page {
            self.page = next;
            self.texture = None;
        }
        Ok(())
    }

    pub fn status(&self) -> Value {
        let mut result = json!({"sourceId": self.id, "title": self.title, "version": self.version, "viewEpoch": self.view_epoch,
            "bodyTabRect": self.body_tab_rect.map(|r| json!({"x":r.left(),"y":r.top(),"width":r.width(),"height":r.height()})),
            "bodyRect": self.body_rect.map(rect_json), "sourceRect": self.source_rect.map(rect_json),
            "bodyFitWidth": self.body_fit_width, "markdownRaw": self.markdown_raw,
            "bodyFitRect": self.body_fit_rect.map(rect_json), "bodyActualSizeRect": self.body_actual_size_rect.map(rect_json),
            "workspaceRect": self.groups_viewport_rect.map(rect_json),
            "groupMenuRects": self.group_menu_rects.iter().map(|(id,r)|(id.to_string(),rect_json(*r))).collect::<BTreeMap<_,_>>(),
            "markdownToggleRect": self.markdown_toggle_rect.map(rect_json),
            "bodyPageRects": self.body_page_rects.iter().copied().map(rect_json).collect::<Vec<_>>(),
            "sourcePageRect": self.source_page_rect.map(rect_json),
            "mode": match self.mode {Mode::Body => "body", Mode::Source => "source", Mode::Split => "split"},
            "sourceActive": self.source_active(), "page": if self.text.is_none() && !self.pages.is_empty() { Some(self.page + 1) } else { None }, "pageCount": self.pages.len(),
            "zoom": self.zoom, "scroll": self.positions.get(&self.page).map(|p| [p.x,p.y]).unwrap_or([0.0,0.0]),
            "rendering": if self.text.is_some() { "plain-text" } else { "host-rendered-png" }, "textSelection": self.text.is_some()});
        result["tabs"]=json!(self.tabs_status());result["layout"]=self.layout_snapshot();result["error"]=json!(self.multi_error);
        result["tabRects"]=json!(self.tab_rects.iter().map(|(k,r)|(k.clone(),rect_json(*r))).collect::<BTreeMap<_,_>>());
        result["groupRects"]=json!(self.group_rects.iter().map(|(id,r)|json!({"id":id,"rect":rect_json(*r)})).collect::<Vec<_>>());
        result
    }

    fn reader(&mut self, ui: &mut Ui) {
        self.source_page_rect = None;
        if self.text.is_some() {
            self.text_reader(ui);
            return;
        }
        // Capture the assigned width before toolbar contents can grow a child UI.
        let assigned_width = ui.available_width();
        let t = Tokens::get(ui.ctx());
        ui.horizontal_wrapped(|ui| {
            ui.label(RichText::new("PDF 页图 · 只读").font(medium(13.0)).color(t.info));
            ui.separator();
            if ui.add_enabled(self.page > 0, egui::Button::new("‹").min_size(vec2(30.0, 28.0))).on_hover_text("上一页").clicked() {
                self.page = self.page.saturating_sub(1);
            }
            ui.label(RichText::new(format!("{} / {}", self.page + 1, self.pages.len())).font(medium(13.0)));
            if ui.add_enabled(self.page + 1 < self.pages.len(), egui::Button::new("›").min_size(vec2(30.0, 28.0))).on_hover_text("下一页").clicked() {
                self.page += 1;
            }
            ui.separator();
            if ui.add(egui::Button::new("−").min_size(vec2(30.0, 28.0))).on_hover_text("缩小资料").clicked() {
                self.zoom = (self.zoom - 0.1).max(0.25);
            }
            ui.add(egui::Label::new(RichText::new(format!("适配 {}%", (self.zoom * 100.0).round() as u32)).font(medium(13.0))))
                .on_hover_text("100% 是适配阅读区后的基准尺寸，不是屏幕原像素大小");
            if ui.add(egui::Button::new("＋").min_size(vec2(30.0, 28.0))).on_hover_text("放大资料").clicked() {
                self.zoom = (self.zoom + 0.1).min(3.0);
            }
            if ui.add(egui::Button::new("适合宽度").min_size(vec2(76.0, 28.0))).clicked() {
                self.zoom = 1.0;
            }
        });
        ui.add_space(8.0);
        if self.texture.as_ref().is_none_or(|(page, _)| *page != self.page) {
            if let Some(image) = self.pages.get(self.page) {
                self.texture = Some((self.page, ui.ctx().load_texture("lingdoc-source-page", image.clone(), egui::TextureOptions::LINEAR)));
            }
        }
        let available = (assigned_width - 48.0).max(1.0);
        let offset = self.positions.get(&self.page).copied().unwrap_or(Vec2::ZERO);
        let output = egui::ScrollArea::both().id_salt(("source-reader", &self.id, &self.version, self.page)).scroll_offset(offset).show(ui, |ui| {
            if let Some((_, texture)) = &self.texture {
                let original = texture.size_vec2();
                let width = available * self.zoom;
                let size = vec2(width, width * original.y / original.x.max(1.0));
                ui.horizontal(|ui| {
                    ui.add_space(24.0);
                    let page = ui.add(egui::Image::new((texture.id(), size))).on_hover_cursor(egui::CursorIcon::Default);
                    self.source_page_rect = Some(page.rect);
                });
            }
        });
        self.positions.insert(self.page, output.state.offset);
    }

    fn text_reader(&mut self, ui: &mut Ui) {
        let assigned_width = ui.available_width();
        let t = Tokens::get(ui.ctx());
        let markdown = self.title.split("（独立导入 ").next().unwrap_or(&self.title).trim_end().to_lowercase().ends_with(".md");
        ui.horizontal_wrapped(|ui| {
            ui.label(RichText::new(if markdown && !self.markdown_raw { "Markdown · 只读" } else { "文本原文 · 只读" }).font(medium(13.0)).color(t.info));
            if markdown {
                let toggle = ui.add(egui::Button::selectable(
                    self.markdown_raw,
                    RichText::new("原始文本").color(if self.markdown_raw { t.accent_text } else { t.text_dim }),
                ).fill(if self.markdown_raw { t.checked } else { t.panel }).min_size(vec2(76.0, 28.0)));
                self.markdown_toggle_rect = Some(toggle.rect);
                if toggle.clicked() {
                    self.markdown_raw = !self.markdown_raw;
                }
            }
            ui.separator();
            if ui.add(egui::Button::new("−").min_size(vec2(30.0, 28.0))).on_hover_text("减小字号").clicked() {
                self.zoom = (self.zoom - 0.1).max(0.6);
            }
            ui.label(RichText::new(format!("字号 {}%", (self.zoom * 100.0).round() as u32)).font(medium(13.0)));
            if ui.add(egui::Button::new("＋").min_size(vec2(30.0, 28.0))).on_hover_text("增大字号").clicked() {
                self.zoom = (self.zoom + 0.1).min(2.0);
            }
        });
        ui.add_space(8.0);
        let offset = self.positions.get(&0).copied().unwrap_or(Vec2::ZERO);
        let width = assigned_width.max(1.0);
        let viewport_height = ui.available_height().max(100.0);
        let margin = (width * 0.065).clamp(24.0, 56.0);
        let paper_width = width.min(880.0);
        let output = egui::ScrollArea::vertical().id_salt(("source-text", &self.id, &self.version)).scroll_offset(offset).show(ui, |ui| {
            ui.set_min_width(width);
            let paper = Rect::from_min_size(ui.cursor().min + vec2((width - paper_width) * 0.5, 16.0), vec2(paper_width, viewport_height));
            let background = ui.painter().add(egui::Shape::Noop);
            let content = Rect::from_min_size(paper.min + vec2(margin, 40.0), vec2((paper_width - margin * 2.0).max(1.0), paper.height()));
            let mut child = ui.new_child(UiBuilder::new().max_rect(content));
            child.visuals_mut().override_text_color = Some(t.text);
            child.spacing_mut().item_spacing.y = 12.0 * self.zoom;
            if let Some(text) = &self.text {
                if markdown && !self.markdown_raw {
                    render_markdown(&mut child, text, self.zoom);
                } else {
                    for line in text.lines() {
                        child.add(egui::Label::new(egui::RichText::new(if line.is_empty() { " " } else { line }).size(16.0 * self.zoom)).wrap().selectable(true))
                            .on_hover_cursor(egui::CursorIcon::Default);
                    }
                }
            }
            let bottom = child.min_rect().bottom() + 48.0;
            let paper = Rect::from_min_max(paper.min, pos2(paper.right(), bottom.max(paper.bottom())));
            ui.painter().set(background, egui::Shape::rect_filled(paper, radius::SMALL, t.input));
            ui.painter().rect_stroke(paper, radius::SMALL, Stroke::new(1.0, t.border), egui::StrokeKind::Inside);
            ui.allocate_rect(paper, Sense::hover()).on_hover_cursor(egui::CursorIcon::Default);
        });
        self.positions.insert(0, output.state.offset);
    }
}

fn rect_json(r: Rect) -> Value {
    json!({"x":r.left(),"y":r.top(),"width":r.width(),"height":r.height()})
}

// Deliberately small, offline subset. HTML, URLs and image syntax remain inert text.
fn markdown_line(line: &str, code: bool) -> (String, f32, bool) {
    if code { return (line.to_owned(), 16.0, true); }
    let hashes = line.bytes().take_while(|b| *b == b'#').count();
    if (1..=6).contains(&hashes) && line.as_bytes().get(hashes) == Some(&b' ') {
        return (line[hashes + 1..].to_owned(), 28.0 - hashes as f32 * 2.0, false);
    }
    if let Some(rest) = line.strip_prefix("- ").or_else(|| line.strip_prefix("* ")) { return (format!("• {rest}"), 16.0, false); }
    if let Some(rest) = line.strip_prefix("> ") { return (format!("│ {rest}"), 16.0, false); }
    (line.to_owned(), 16.0, false)
}

fn render_markdown(ui: &mut Ui, text: &str, zoom: f32) {
    let mut code = false;
    for line in text.lines() {
        if line.starts_with("```") { code = !code; continue; }
        let (line, size, mono) = markdown_line(line, code);
        let mut style = egui::RichText::new(if line.is_empty() { " ".to_owned() } else { line }).size(size * zoom);
        if mono { style = style.monospace(); }
        if size > 16.0 { style = style.strong(); }
        ui.add(egui::Label::new(style).wrap().selectable(true)).on_hover_cursor(egui::CursorIcon::Default);
    }
}

fn paint_readonly_lock(ui: &Ui, center: egui::Pos2, color: egui::Color32) {
    let stroke = Stroke::new(1.0, color);
    let body = Rect::from_center_size(center + vec2(0.0, 2.0), vec2(8.0, 7.0));
    ui.painter().rect_stroke(body, radius::SMALL, stroke, egui::StrokeKind::Inside);
    let left = body.left_top() + vec2(2.0, -4.0);
    let right = body.right_top() + vec2(-2.0, -4.0);
    ui.painter().line_segment([left, left + vec2(0.0, 4.0)], stroke);
    ui.painter().line_segment([left, right], stroke);
    ui.painter().line_segment([right, right + vec2(0.0, 4.0)], stroke);
}

/// A document identity, not a view command. Dirty and read-only markers are drawn
/// from the live session/reader kind; neither marker changes persistence state.
fn reader_tab(ui: &mut Ui, label: &str, active: bool, read_only: bool, dirty: bool) -> egui::Response {
    let t = Tokens::get(ui.ctx());
    let color = if active { t.text } else { t.text_dim };
    let font = medium(13.0);
    let galley = ui.painter().layout_no_wrap(label.to_owned(), font, color);
    let extras = if read_only { 14.0 } else { 0.0 } + if dirty { 12.0 } else { 0.0 };
    let width = galley.size().x + 20.0 + extras;
    let (rect, _) = ui.allocate_exact_size(vec2(width, 30.0), Sense::hover());
    let response = ui.interact(rect, ui.id().with(("reader-document-tab", label)), Sense::click_and_drag());
    response.widget_info(|| egui::WidgetInfo::selected(egui::WidgetType::SelectableLabel, ui.is_enabled(), active, label));
    let fill = if active {
        t.input
    } else if response.is_pointer_button_down_on() {
        t.pressed
    } else if response.hovered() {
        t.hover
    } else {
        t.tab_strip
    };
    let corners = egui::CornerRadius { nw: 6, ne: 6, sw: 0, se: 0 };
    ui.painter().rect_filled(rect, corners, fill);
    if response.has_focus() {
        ui.painter().rect_stroke(rect.shrink(1.0), corners, Stroke::new(2.0, t.focus_ring), egui::StrokeKind::Inside);
    }
    if active {
        let top = Rect::from_min_max(rect.left_top() + vec2(8.0, 0.0), pos2(rect.right() - 8.0, rect.top() + 2.0));
        ui.painter().rect_filled(top, radius::SMALL, t.accent);
    }
    let mut text_left = rect.left() + 10.0;
    if read_only {
        paint_readonly_lock(ui, pos2(text_left + 4.0, rect.center().y - 1.0), t.text_dim);
        text_left += 14.0;
    }
    ui.painter().galley(pos2(text_left, rect.center().y - galley.size().y * 0.5), galley, color);
    if dirty {
        ui.painter().circle_filled(pos2(rect.right() - 8.0, rect.center().y), 3.0, t.warning);
    }
    response
}

fn reader_tab_close(ui: &mut Ui) -> egui::Response {
    let t = Tokens::get(ui.ctx());
    let (rect, response) = ui.allocate_exact_size(vec2(24.0, 28.0), Sense::click());
    response.widget_info(|| egui::WidgetInfo::labeled(egui::WidgetType::Button, ui.is_enabled(), "×"));
    if response.hovered() {
        ui.painter().rect_filled(rect, radius::BASE, t.hover);
    }
    if response.has_focus() {
        ui.painter().rect_stroke(rect, radius::BASE, Stroke::new(2.0, t.focus_ring), egui::StrokeKind::Inside);
    }
    ui.painter().text(rect.center(), egui::Align2::CENTER_CENTER, "×", medium(14.0), if response.hovered() { t.text } else { t.text_dim });
    response
}

// Native view settings only: no document command, relayout mode change or undo entry.
fn sync_body_view(app: &mut WordApp) {
    app.source_workspace.body_fit_width = app.session.view.fit == "pageWidth" && !app.session.view.read_mode && app.session.view.mode == wordcraft_layout::ViewMode::Print;
}

/// Native document groups share exactly one editable body Session.
pub fn show(app: &mut WordApp, ui: &mut Ui) {
    app.source_workspace.ensure_groups();
    app.source_workspace.body_rect = None;
    app.source_workspace.source_rect = None;
    app.source_workspace.body_page_rects.clear();
    app.source_workspace.group_rects.clear();
    app.source_workspace.groups_viewport_rect=Some(ui.available_rect_before_wrap().intersect(ui.clip_rect()));
    app.source_workspace.tab_rects.clear();
    app.source_workspace.group_menu_rects.clear();
    app.source_workspace.body_tab_rect = None;
    app.source_workspace.body_fit_rect = None;
    app.source_workspace.body_actual_size_rect = None;
    let mut action: Option<Value> = None;
    if !app.source_workspace.multi_error.is_empty() {ui.colored_label(Tokens::get(ui.ctx()).danger,&app.source_workspace.multi_error);}
    sync_body_view(app);
    let groups:Vec<_>=app.source_workspace.groups.iter().filter(|g|app.source_workspace.maximized.is_none_or(|id|id==g.id)).cloned().collect();
    // Keep each reader operable when several explicit groups and both side panels are open.
    // Horizontal scrolling preserves the user's groups and body zoom instead of squeezing controls.
    let minimum_width=groups.iter().map(group_minimum_width).sum::<f32>()+6.0*groups.len().saturating_sub(1) as f32;
    let content_width=ui.available_width().max(minimum_width);
    egui::ScrollArea::horizontal().id_salt("native-reader-groups").auto_shrink([false,false]).show(ui,|ui| {
    ui.set_width(content_width);
    ui.set_min_height(ui.available_height());
    let rect=ui.available_rect_before_wrap();
    let usable=(rect.width()-6.0*(groups.len().saturating_sub(1)) as f32).max(1.0);
    let widths=reader_group_widths(&groups,usable);
    let mut x=rect.left();
    for (index,g) in groups.iter().enumerate() {
        let width=widths[index];
        let gr=Rect::from_min_max(pos2(x,rect.top()),pos2((x+width).min(rect.right()),rect.bottom()));
        app.source_workspace.group_rects.push((g.id,gr));
        let mut pane=ui.new_child(UiBuilder::new().id_salt(("reader-group",g.id)).max_rect(gr));
        pane.set_clip_rect(gr.intersect(ui.clip_rect()));
        pane.set_max_width(gr.width());
        pane.horizontal(|ui| {
            let menu_width=40.0;
            let tabs_width=(gr.width()-menu_width-ui.spacing().item_spacing.x).max(80.0);
            ui.allocate_ui(vec2(tabs_width,32.0),|ui| {
                egui::ScrollArea::horizontal().id_salt(("reader-tabs",g.id)).show(ui,|ui| {
                    ui.horizontal(|ui| {
                        for key in &g.tabs {
                            let name=reader_tab_title(app,key);
                            let limit=((gr.width()-96.0)/11.0).clamp(8.0,24.0) as usize;
                            let label=if name.chars().count()>limit{format!("{}…",name.chars().take(limit-1).collect::<String>())}else{name.clone()};
                            let response=reader_tab(ui,&label,g.active==*key,key!="$body",key=="$body"&&app.session.dirty)
                                .on_hover_text(format!("{}\n双击最大化此组 · 中键关闭 · 拖动移组",name));
                            app.source_workspace.tab_rects.insert(key.clone(),response.rect);
                            if key=="$body" {app.source_workspace.body_tab_rect=Some(response.rect);}
                            if response.clicked(){action=Some(json!({"action":"activate","key":key}));}
                            if response.double_clicked(){action=Some(json!({"action":"maximize","group":g.id}));}
                            if response.clicked_by(egui::PointerButton::Middle){action=Some(json!({"action":"close","key":key}));}
                            response.context_menu(|ui| {
                                if ui.button("关闭标签").clicked(){action=Some(json!({"action":"close","key":key}));ui.close();}
                                if app.source_workspace.groups.len()<3 && g.tabs.len()>1 && ui.button("移到新阅读组").clicked(){action=Some(json!({"action":"newGroup","key":key}));ui.close();}
                                for (i,other) in app.source_workspace.groups.iter().enumerate(){
                                    if other.id!=g.id && ui.button(format!("移到阅读组 {}",i+1)).clicked(){action=Some(json!({"action":"move","key":key,"group":other.id}));ui.close();}
                                }
                            });
                            if response.drag_started(){response.dnd_set_drag_payload(key.clone());}
                            if key!="$body" && reader_tab_close(ui).on_hover_text("关闭资料标签，保留项目资料").clicked(){action=Some(json!({"action":"close","key":key}));}
                        }
                    });
                });
            });
            let menu=ui.menu_button(RichText::new("…").font(medium(16.0)),|ui| {
                if app.source_workspace.maximized==Some(g.id) {
                    ui.label(RichText::new("已最大化").color(Tokens::get(ui.ctx()).text_dim));
                    ui.separator();
                }
                if ui.button(if app.source_workspace.maximized.is_some(){"恢复并排布局"}else{"最大化此阅读组"}).clicked(){action=Some(json!({"action":"maximize","group":g.id}));ui.close();}
                if ui.button("仅查看正文").clicked(){let _=app.source_workspace.set_mode("body");app.project_workspace.release_focus();app.canvas.want_focus=true;ui.close();}
                if ui.add_enabled(app.source_workspace.has_source(),egui::Button::new("正文与资料并排")).clicked(){let _=app.source_workspace.set_mode("split");ui.close();}
                ui.separator();
                let fit=ui.add_enabled(!app.session.view.read_mode,egui::Button::new("正文适合宽度"));
                app.source_workspace.body_fit_rect=Some(fit.rect);
                if fit.clicked(){apply_tabs_action(app,&json!({"action":"activate","key":"$body"}));if let Err(error)=app.execute("view.zoom",json!({"value":"pageWidth"})){app.source_workspace.multi_error=error;}ui.close();}
                let actual=ui.button("正文 100%");
                app.source_workspace.body_actual_size_rect=Some(actual.rect);
                if actual.clicked(){apply_tabs_action(app,&json!({"action":"activate","key":"$body"}));if let Err(error)=app.execute("view.zoom100",json!({})){app.source_workspace.multi_error=error;}ui.close();}
                ui.separator();
                ui.label("当前组已打开");
                for key in &g.tabs {
                    let title=reader_tab_title(app,key);
                    if ui.selectable_label(g.active==*key,title).clicked(){action=Some(json!({"action":"activate","key":key}));ui.close();}
                }
                ui.separator();
                if app.source_workspace.groups.len()<3 && ui.button("当前标签移到新组").clicked(){action=Some(json!({"action":"newGroup","key":g.active}));ui.close();}
                for (other_index,other) in app.source_workspace.groups.iter().enumerate() {
                    if other.id!=g.id && ui.button(format!("当前标签移到组 {}",other_index+1)).clicked(){action=Some(json!({"action":"move","key":g.active,"group":other.id}));ui.close();}
                }
                if app.source_workspace.groups.len()>1 && ui.button("合并所有组").clicked(){action=Some(json!({"action":"merge","group":g.id}));ui.close();}
                if ui.button("关闭当前标签").clicked(){action=Some(json!({"action":"close","key":g.active}));ui.close();}
                if ui.add_enabled(!app.source_workspace.closed_tabs.is_empty(),egui::Button::new("恢复关闭标签")).clicked(){action=Some(json!({"action":"reopen"}));ui.close();}
                if app.source_workspace.jump_back.is_some() && ui.button("返回跳转前").clicked(){action=Some(json!({"action":"returnCitation"}));ui.close();}
            });
            let menu_response=menu.response.on_hover_text("视图与分屏");
            app.source_workspace.group_menu_rects.insert(g.id,menu_response.rect);
        });
        let content=pane.available_rect_before_wrap();
        if g.active=="$body" {
            body_canvas(app,&mut pane,content);
        } else {
            if g.id==app.source_workspace.focus_group || app.source_workspace.source_rect.is_none() {app.source_workspace.source_rect=Some(content);}
            let mut reader_ui=pane.new_child(UiBuilder::new().id_salt(("reader-content",&g.active)).max_rect(content));
            reader_ui.set_clip_rect(content.intersect(pane.clip_rect()));
            if g.active==app.source_workspace.key(){app.source_workspace.reader(&mut reader_ui);}
            else if let Some(mut reader)=app.source_workspace.readers.remove(&g.active){reader.reader(&mut reader_ui);app.source_workspace.readers.insert(g.active.clone(),reader);}
        }
        let drop=ui.interact(gr,ui.id().with(("reader-drop",g.id)),Sense::hover());
        if let Some(key)=drop.dnd_release_payload::<String>(){action=Some(json!({"action":"move","key":key.as_str(),"group":g.id}));}
        if g.id==app.source_workspace.focus_group {ui.painter().line_segment([gr.left_top(),gr.right_top()],Stroke::new(1.0,Tokens::get(ui.ctx()).border));}
        x+=width+6.0;
        if let Some(next)=groups.get(index+1){
            let divider=Rect::from_min_max(pos2(x-6.0,rect.top()),pos2(x,rect.bottom()));
            let drag=ui.interact(divider,ui.id().with(("group-divider",g.id)),Sense::drag()).on_hover_cursor(egui::CursorIcon::ResizeHorizontal);
            let t=Tokens::get(ui.ctx());
            if drag.hovered() || drag.dragged() {
                let center=divider.center().x;
                let hover=Rect::from_min_max(pos2(center-2.0,divider.top()),pos2(center+2.0,divider.bottom()));
                ui.painter().rect_filled(hover,radius::SMALL,t.hover);
            }
            ui.painter().line_segment([pos2(divider.center().x,divider.top()),pos2(divider.center().x,divider.bottom())],Stroke::new(1.0,t.border));
            if drag.double_clicked(){
                let pair=width+widths[index+1];
                let left=(pair/2.0).clamp(group_minimum_width(g),(pair-group_minimum_width(next)).max(group_minimum_width(g)));
                for (i,shown) in groups.iter().enumerate(){if let Some(real)=app.source_workspace.groups.iter_mut().find(|item|item.id==shown.id){real.weight=if i==index{left/usable}else if i==index+1{(pair-left)/usable}else{widths[i]/usable};}}
                app.source_workspace.view_epoch=app.source_workspace.view_epoch.saturating_add(1);
            }
            if drag.dragged(){
                let pair=width+widths[index+1];
                let left=(width+drag.drag_delta().x).clamp(group_minimum_width(g),(pair-group_minimum_width(next)).max(group_minimum_width(g)));
                for (i,shown) in groups.iter().enumerate(){if let Some(real)=app.source_workspace.groups.iter_mut().find(|item|item.id==shown.id){real.weight=if i==index{left/usable}else if i==index+1{(pair-left)/usable}else{widths[i]/usable};}}
                app.source_workspace.view_epoch=app.source_workspace.view_epoch.saturating_add(1);
            }
        }
    }
    if let Some(a)=action {apply_tabs_action(app,&a);}
    });
}

fn group_minimum_width(group:&ReaderGroup)->f32 {
    if group.tabs.iter().any(|key|key=="$body"){360.0}else{220.0}
}

// Clamp only the displayed widths, keeping saved proportions intact until the user drags.
fn reader_group_widths(groups:&[ReaderGroup],available:f32)->Vec<f32> {
    let mut widths=vec![0.0;groups.len()];
    let mut pending:Vec<usize>=(0..groups.len()).collect();
    let mut remaining=available.max(groups.iter().map(group_minimum_width).sum());
    while !pending.is_empty(){
        let total=pending.iter().map(|i|groups[*i].weight as f64).sum::<f64>().max(f64::MIN_POSITIVE);
        let share=|i:usize|(remaining as f64*(groups[i].weight as f64/total)) as f32;
        let constrained:Vec<usize>=pending.iter().copied().filter(|i|share(*i)<group_minimum_width(&groups[*i])).collect();
        if constrained.is_empty(){for i in pending {widths[i]=share(i);}break;}
        for i in constrained {widths[i]=group_minimum_width(&groups[i]);remaining-=widths[i];pending.retain(|index|*index!=i);}
    }
    widths
}

fn reader_tab_title(app:&WordApp,key:&str)->String {
    if key=="$body" {
        return app.project_workspace.document_title().unwrap_or("正文").to_owned();
    }
    let reader=if key==app.source_workspace.key(){Some(&app.source_workspace)}else{app.source_workspace.readers.get(key).map(Box::as_ref)};
    reader.map(|r|format!("{} · {}{} · 只读",r.title,if r.version.starts_with('v'){""}else{"v"},r.version)).unwrap_or_else(||"资料未加载 · 只读".into())
}

fn apply_tabs_action(app:&mut WordApp, action:&Value) {
    match app.source_workspace.multi_action(action) {Ok(_)=>{app.source_workspace.multi_error.clear();app.project_workspace.release_focus();app.canvas.want_focus=!app.source_workspace.source_active();},Err(error)=>app.source_workspace.multi_error=error}
}

fn body_canvas(app: &mut WordApp, ui: &mut Ui, rect: Rect) {
    app.source_workspace.body_rect = Some(rect);
    if app.project_workspace.auxiliary_active() {
        app.canvas.focused = false;
        app.canvas.want_focus = false;
        if let Some((id, _)) = app.canvas.widget_id {
            ui.memory_mut(|m| m.surrender_focus(id));
        }
    }
    let mut child = ui.new_child(UiBuilder::new().id_salt("body-workspace").max_rect(rect));
    child.set_clip_rect(rect.intersect(ui.clip_rect()));
    crate::canvas::show(app, &mut child);
    // Actual page geometry produced by the native canvas, not the container bounds.
    app.source_workspace.body_page_rects = app.canvas.page_rects.clone();
}

/// Call before chrome/command handling, using the previous frame's reader bounds.
pub fn prepare_frame(app: &mut WordApp, ctx: &egui::Context) {
    if let Some(pos)=ctx.input(|i|if i.pointer.any_pressed(){i.pointer.interact_pos()}else{None}) {
        if let Some((id,_))=app.source_workspace.group_rects.iter().find(|(_,r)|app.source_workspace.groups_viewport_rect.is_some_and(|viewport|viewport.contains(pos))&&r.contains(pos)).copied(){
            if let Some(key)=app.source_workspace.groups.iter().find(|g|g.id==id).map(|g|g.active.clone()) {
                // Every user interaction invalidates a pending source load, even in the active group.
                let _=app.source_workspace.multi_action(&json!({"action":"activate","key":key}));
                app.project_workspace.release_focus();
            }
        }
    }
    let active_id=if app.source_workspace.source_active(){app.source_workspace.id.clone()}else{String::new()};
    app.project_workspace.set_active_source(&active_id);
    if app.source_workspace.source_active() || app.project_workspace.auxiliary_active() {
        app.canvas.focused=false;app.canvas.want_focus=false;
        if let Some((id,_))=app.canvas.widget_id{ctx.memory_mut(|m|m.surrender_focus(id));}
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn narrow_pdf_toolbar_does_not_inflate_fit_width() {
        let mut source = SourceWorkspace::default();
        source.load_pages("a".into(), "PDF".into(), "1".into(), vec![png()]).unwrap();
        let mut fonts_ready = false;
        let mut h = egui_kittest::Harness::builder().with_size(vec2(190.0, 600.0)).build_ui_state(
            move |ui, source: &mut SourceWorkspace| {
                if !fonts_ready {
                    ui.ctx().set_fonts(crate::theme::font_definitions(false, false));
                    fonts_ready = true;
                    return;
                }
                source.reader(ui);
            }, source,
        );
        h.run_steps(4);
        let fitted = h.state().source_page_rect.unwrap().width();
        assert!(fitted > 100.0 && fitted < 190.0, "100% follows allocated width, not toolbar width");
        h.state_mut().zoom = 2.0;
        h.run_steps(3);
        assert!((h.state().source_page_rect.unwrap().width() - fitted * 2.0).abs() < 1.0);
        assert_eq!(h.state().page, 0);
    }

    #[test]
    fn split_switch_preserves_body_zoom_fit_document_and_undo() {
        let mut app = edited_app();
        let original = snapshot(&app);
        app.session.view.zoom = 1.7;
        app.session.view.fit = "onePage".into();
        app.source_workspace.load_text("a".into(), "Notes.txt".into(), "1".into(), "text".into()).unwrap();
        app.source_workspace.set_mode("split").unwrap();
        sync_body_view(&mut app);
        assert_eq!(app.session.view.zoom, 1.7);
        assert_eq!(app.session.view.fit, "onePage");
        assert!(!app.source_workspace.body_fit_width);
        // An explicit change while split remains the user's choice on exit.
        app.session.view.zoom = 0.8;
        app.session.view.fit.clear();
        for mode in ["source", "body", "split", "body"] {
            app.source_workspace.set_mode(mode).unwrap();
            sync_body_view(&mut app);
            assert_eq!(app.session.view.zoom, 0.8);
            assert!(app.session.view.fit.is_empty());
            assert_eq!(snapshot(&app), original);
        }
        app.execute("edit.undo", json!({})).unwrap();
        assert_eq!(app.session.undo_depth(), 0);
    }

    #[test]
    fn markdown_subset_keeps_html_links_and_code_inert() {
        assert_eq!(markdown_line("# Heading", false), ("Heading".into(), 26.0, false));
        assert_eq!(markdown_line("- Item", false).0, "• Item");
        for line in ["<script>alert(1)</script>", "![image](https://example.invalid/a)", "[link](file:///secret)"] {
            assert_eq!(markdown_line(line, false).0, line);
        }
        assert_eq!(markdown_line("# literal", true), ("# literal".into(), 16.0, true));
    }

    #[test]
    fn split_geometry_fits_body_page_inside_its_viewport() {
        let mut app = edited_app();
        let original = snapshot(&app);
        app.source_workspace.load_text("a".into(), "Notes.txt".into(), "1".into(), "text".into()).unwrap();
        app.source_workspace.set_mode("split").unwrap();
        app.session.view.fit = "pageWidth".into(); // Explicit user fit action.
        let mut h = egui_kittest::Harness::builder().with_size(vec2(1440.0, 900.0)).build_ui_state(
            |ui, app: &mut WordApp| { let ctx = ui.ctx().clone(); app.logic(&ctx); app.ui(ui); }, app,
        );
        h.run_steps(6);
        let app = h.state();
        let body = app.source_workspace.body_rect.unwrap();
        let source = app.source_workspace.source_rect.unwrap();
        assert!(body.width() > 200.0 && source.width() > 200.0);
        assert!(body.right() < source.left());
        assert!(app.source_workspace.body_fit_width);
        for page in &app.canvas.page_rects { assert!(page.width() <= body.width(), "body page must fit in split viewport"); }
        assert_eq!(snapshot(app), original);
    }

    fn png() -> Vec<u8> {
        let image = image::RgbaImage::from_pixel(2, 3, image::Rgba([255, 255, 255, 255]));
        let mut encoded = std::io::Cursor::new(Vec::new());
        image.write_to(&mut encoded, image::ImageFormat::Png).unwrap();
        encoded.into_inner()
    }

    fn edited_app() -> WordApp {
        let mut app = WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()), crate::Services::default());
        app.execute("text.insert", json!({"text":"Unsaved body"})).unwrap();
        app
    }

    fn snapshot(app: &WordApp) -> Value {
        json!({"id":app.session.document_id(),"revision":app.session.rev(),"dirty":app.session.dirty,
            "selection":app.session.sel,"undo":app.session.undo_depth(),"document":app.session.doc})
    }

    #[test]
    fn reading_modes_preserve_live_document_and_reject_source_edits() {
        let mut app = edited_app();
        let original = snapshot(&app);
        assert!(app.session.dirty && app.session.can_undo());
        app.source_workspace.load_pages("a".into(), "Source".into(), "v1".into(), vec![png()]).unwrap();
        for mode in ["body", "source", "split", "source", "body"] {
            app.source_workspace.set_mode(mode).unwrap();
            assert_eq!(snapshot(&app), original, "mode {mode} must preserve the live document");
        }
        app.source_workspace.set_mode("source").unwrap();
        assert!(app.execute("text.insert", json!({"text":"wrong target"})).is_err());
        assert!(app.execute("edit.undo", json!({})).is_err());
        assert!(app.run("format.bold", json!({})).is_err());
        assert_eq!(snapshot(&app), original);
        app.source_workspace.set_mode("body").unwrap();
        app.execute("edit.undo", json!({})).unwrap();
        assert_eq!(app.session.document_id(), original["id"].as_u64().unwrap());
        assert_eq!(app.session.undo_depth(), 0);
        assert_ne!(snapshot(&app)["document"], original["document"]);
    }

    #[test]
    fn clicking_source_tab_does_not_type_into_hidden_body() {
        use egui_kittest::kittest::Queryable;
        let mut app = edited_app();
        app.source_workspace.load_pages("a".into(), "Source".into(), "v1".into(), vec![png()]).unwrap();
        let mut h = egui_kittest::Harness::builder().with_size(vec2(1440.0, 900.0)).build_ui_state(
            |ui, app: &mut WordApp| {
                let ctx = ui.ctx().clone();
                app.logic(&ctx);
                app.ui(ui);
            },
            app,
        );
        h.run_steps(6);
        let before = snapshot(h.state());
        h.get_by_label("Source · v1 · 只读").click();
        h.run_steps(2);
        assert!(h.state().source_workspace.source_active());
        assert!(!h.state().canvas.focused);
        h.event(egui::Event::Text("should not enter body".into()));
        h.key_press(egui::Key::Backspace);
        h.run_steps(2);
        assert_eq!(snapshot(h.state()), before);
        h.get_by_label("正文").click();
        h.run_steps(2);
        assert!(!h.state().source_workspace.source_active());
        h.state_mut().execute("edit.undo", json!({})).unwrap();
        assert_eq!(h.state().session.undo_depth(), 0);
    }
    #[test]
    fn invalid_source_does_not_replace_state() {
        let mut reader = SourceWorkspace::default();
        reader.id = "original".into();
        assert!(reader.load_pages("next".into(), "PDF".into(), "v1".into(), vec![b"not png".to_vec()]).is_err());
        assert_eq!(reader.id, "original");
        assert!(!reader.source_active());
    }
    #[test]
    fn no_source_cannot_enter_reader_or_split() {
        let mut reader = SourceWorkspace::default();
        assert!(reader.set_mode("source").is_err());
        assert!(reader.set_mode("split").is_err());
        assert!(reader.set_mode("unknown").is_err());
        assert!(reader.set_mode("body").is_ok());
    }

    #[test]
    fn page_navigation_preserves_offsets_and_rejects_out_of_range() {
        let mut reader = SourceWorkspace::default();
        assert!(reader.set_page(1).is_err());
        reader.load_pages("a".into(), "Source".into(), "v1".into(), vec![png(), png()]).unwrap();
        reader.positions.insert(0, vec2(0.0, 12.0));
        reader.positions.insert(1, vec2(0.0, 27.0));
        reader.set_page(2).unwrap();
        assert_eq!(reader.status()["page"], 2);
        assert_eq!(reader.status()["scroll"], json!([0.0, 27.0]));
        for page in [0, 3, usize::MAX] {
            assert!(reader.set_page(page).is_err());
            assert_eq!(reader.status()["page"], 2);
        }
        reader.set_page(1).unwrap();
        assert_eq!(reader.status()["scroll"], json!([0.0, 12.0]));
    }

    #[test]
    fn oversized_png_is_rejected_before_pixel_decode() {
        let mut data = b"\x89PNG\r\n\x1a\n\0\0\0\rIHDR".to_vec();
        data.extend(u32::MAX.to_be_bytes());
        data.extend(u32::MAX.to_be_bytes());
        let mut reader = SourceWorkspace::default();
        let error = reader.load_pages("a".into(), "Huge".into(), "v1".into(), vec![data]).unwrap_err();
        assert!(error.contains("dimensions"));
        assert!(reader.pages.is_empty());
    }

    #[test]
    fn valid_load_is_readonly_and_failed_reload_keeps_pages() {
        let image = image::RgbaImage::from_pixel(2, 3, image::Rgba([255, 255, 255, 255]));
        let mut encoded = std::io::Cursor::new(Vec::new());
        image.write_to(&mut encoded, image::ImageFormat::Png).unwrap();
        let png = encoded.into_inner();
        let mut reader = SourceWorkspace::default();
        reader.load_pages("a".into(), "Source".into(), "v1".into(), vec![png.clone()]).unwrap();
        assert_eq!(reader.status()["mode"], "body");
        reader.set_mode("source").unwrap();
        assert!(reader.source_active());
        assert!(reader.load_pages("b".into(), "Bad".into(), "v2".into(), vec![png, vec![0]]).is_err());
        assert_eq!(reader.id, "a");
        assert_eq!(reader.pages.len(), 1);
        assert!(reader.source_active());
        reader.set_mode("body").unwrap();
        assert!(!reader.source_active());
    }

    #[test]
    fn plain_text_is_not_a_fake_paginated_document() {
        let mut app = edited_app();
        let before = snapshot(&app);
        let exact = "第一段\r\n第二段 🧭\n";
        app.source_workspace.load_text("notes".into(), "访谈记录.txt".into(), "v1".into(), exact.into()).unwrap();
        assert!(app.source_workspace.has_source());
        assert_eq!(app.source_workspace.text.as_deref(), Some(exact));
        assert_eq!(app.source_workspace.status()["rendering"], "plain-text");
        assert_eq!(app.source_workspace.status()["page"], Value::Null);
        assert_eq!(app.source_workspace.status()["pageCount"], 0);
        assert!(app.source_workspace.set_page(1).is_err());
        app.source_workspace.set_mode("source").unwrap();
        assert!(app.execute("text.insert", json!({"text":"wrong target"})).is_err());
        assert_eq!(snapshot(&app), before);
        assert!(app.source_workspace.load_text("next".into(), "Too large".into(), "v2".into(), "a".repeat(MAX_TEXT_BYTES + 1)).is_err());
        assert_eq!(app.source_workspace.text.as_deref(), Some(exact));
        assert!(app.source_workspace.source_active());
    }

    #[test]
    fn source_version_switch_restores_independent_reading_positions() {
        let mut reader = SourceWorkspace::default();
        reader.load_pages("pdf".into(), "PDF".into(), "v1".into(), vec![png(), png()]).unwrap();
        reader.set_page(2).unwrap();
        reader.zoom = 1.4;
        reader.positions.insert(1, vec2(0.0, 64.0));
        reader.load_text("notes".into(), "Notes".into(), "v1".into(), "text".into()).unwrap();
        reader.zoom = 1.2;
        reader.positions.insert(0, vec2(0.0, 99.0));
        reader.load_pages("pdf".into(), "PDF".into(), "v2".into(), vec![png(), png()]).unwrap();
        assert_eq!(reader.page, 0);
        assert_eq!(reader.zoom, 1.0);
        reader.load_pages("pdf".into(), "PDF".into(), "v1".into(), vec![png(), png()]).unwrap();
        assert_eq!(reader.page, 1);
        assert_eq!(reader.zoom, 1.4);
        assert_eq!(reader.positions.get(&1), Some(&vec2(0.0, 64.0)));
        reader.load_text("notes".into(), "Notes".into(), "v1".into(), "text".into()).unwrap();
        assert_eq!(reader.zoom, 1.2);
        assert_eq!(reader.positions.get(&0), Some(&vec2(0.0, 99.0)));
    }

    #[test]
    fn reading_position_cache_is_bounded() {
        let mut reader = SourceWorkspace::default();
        for i in 0..16 {
            reader.load_text(format!("source-{i}"), "Notes".into(), "v1".into(), "text".into()).unwrap();
            reader.positions.insert(0, vec2(0.0, i as f32));
        }
        let before = reader.layout_snapshot();
        assert!(reader.load_text("overflow".into(), "Notes".into(), "v1".into(), "text".into()).is_err());
        assert_eq!(reader.layout_snapshot(), before);
        assert_eq!(reader.id, "source-15");
        assert!(reader.reading_positions.len() <= MAX_READING_POSITIONS);
        assert_eq!(reader.readers.len(), 15);
    }

    fn incoming_text(id: &str) -> SourceInput {
        SourceInput::Text { id: id.into(), title: "Notes".into(), version: "v1".into(), text: "Original source text".into() }
    }

    #[test]
    fn native_body_tab_cancels_an_in_flight_source_commit() {
        let mut app = edited_app();
        let document_before = snapshot(&app);
        app.source_workspace.load_text("existing".into(), "Notes".into(), "v1".into(), "Existing".into()).unwrap();
        app.source_workspace.set_mode("source").unwrap();
        let ticket = app.source_workspace.begin_open();
        // This is the same setter called by a native body-tab click while host I/O waits.
        app.source_workspace.set_mode("body").unwrap();
        let before = app.source_workspace.status();
        assert!(app.source_workspace.commit_open(ticket, incoming_text("late"), "source", None).is_err());
        assert_eq!(app.source_workspace.status(), before);
        assert_eq!(snapshot(&app), document_before);
    }

    #[test]
    fn newer_request_and_successful_commit_invalidate_old_tickets() {
        let mut reader = SourceWorkspace::default();
        let first = reader.begin_open();
        let latest = reader.begin_open();
        assert!(reader.commit_open(first, incoming_text("old"), "source", None).is_err());
        reader.commit_open(latest, incoming_text("new"), "split", None).unwrap();
        assert_eq!(reader.status()["mode"], "split");
        assert_eq!(reader.id, "new");
        let final_state = reader.status();
        assert!(reader.commit_open(latest, incoming_text("replay"), "body", None).is_err());
        assert_eq!(reader.status(), final_state);
    }

    #[test]
    fn atomic_commit_validates_page_and_payload_before_replacement() {
        let mut reader = SourceWorkspace::default();
        reader.load_text("old".into(), "Old".into(), "v1".into(), "Keep me".into()).unwrap();
        let ticket = reader.begin_open();
        let before = reader.status();
        let bad_page = SourceInput::Pages { id: "new".into(), title: "New".into(), version: "v1".into(), pages: vec![png()] };
        assert!(reader.commit_open(ticket, bad_page, "source", Some(2)).is_err());
        assert_eq!(reader.status(), before);
        let bad_png = SourceInput::Pages { id: "new".into(), title: "New".into(), version: "v1".into(), pages: vec![vec![0]] };
        assert!(reader.commit_open(ticket, bad_png, "source", None).is_err());
        assert_eq!(reader.status(), before);
        reader.commit_open(ticket, incoming_text("valid"), "source", None).unwrap();
        assert_eq!(reader.status()["mode"], "source");
        assert_eq!(reader.id, "valid");
    }

    #[test]
    fn source_view_menu_applies_explicit_body_zoom_without_editing_document() {
        use egui_kittest::kittest::Queryable;
        let mut app = edited_app();
        app.source_workspace.load_text("a".into(), "Source".into(), "v1".into(), "Read only".into()).unwrap();
        app.source_workspace.set_mode("source").unwrap();
        let mut h = egui_kittest::Harness::builder().with_size(vec2(1440.0, 900.0)).build_ui_state(
            |ui, app: &mut WordApp| { let ctx = ui.ctx().clone(); app.logic(&ctx); app.ui(ui); }, app,
        );
        h.run_steps(6);
        let before = snapshot(h.state());
        let undo_before = h.state().session.undo_depth();
        let dirty_before = h.state().session.dirty;
        for label in ["正文适合宽度", "正文 100%"] {
            h.state_mut().source_workspace.set_mode("source").unwrap();
            h.run_steps(3);
            assert!(h.state().source_workspace.source_active());
            let center = h.state().source_workspace.group_menu_rects.values().next().unwrap().center();
            h.event(egui::Event::PointerMoved(center));
            h.event(egui::Event::PointerButton { pos: center, button: egui::PointerButton::Primary, pressed: true, modifiers: egui::Modifiers::NONE });
            h.run_steps(1);
            h.event(egui::Event::PointerButton { pos: center, button: egui::PointerButton::Primary, pressed: false, modifiers: egui::Modifiers::NONE });
            h.run_steps(2);
            h.get_by_label(label).click();
            h.run_steps(3);
            assert!(!h.state().source_workspace.source_active());
            assert!(h.state().source_workspace.multi_error.is_empty());
            if label == "正文适合宽度" {
                assert_eq!(h.state().session.view.fit, "pageWidth");
            } else {
                assert_eq!(h.state().session.view.zoom, 1.0);
                assert!(h.state().session.view.fit.is_empty());
            }
            assert_eq!(snapshot(h.state()), before);
            assert_eq!(h.state().session.undo_depth(), undo_before);
            assert_eq!(h.state().session.dirty, dirty_before);
        }
        h.state_mut().execute("edit.undo", json!({})).unwrap();
        assert_eq!(h.state().session.undo_depth(), 0);
    }

    #[test]
    fn reader_widths_respect_minima_total_and_saved_weights() {
        for weights in [[0.62,0.19,0.19],[0.98,0.01,0.01],[f32::MAX,f32::MAX,f32::MAX],[f32::from_bits(1),f32::from_bits(1),f32::from_bits(1)],[f32::MAX,f32::from_bits(1),1.0]] {
            let groups:Vec<_>=weights.iter().enumerate().map(|(i,weight)|ReaderGroup {
                id:i as u64+1,tabs:vec![if i==0 {"$body".into()} else {format!("source-{i}")}],active:String::new(),weight:*weight,
            }).collect();
            for available in [100.0,800.0,1400.0] {
                let widths=reader_group_widths(&groups,available);
                assert_eq!(widths.len(),3);
                assert!(widths.iter().all(|width|width.is_finite()), "weights {weights:?}");
                for (group,width) in groups.iter().zip(&widths) {
                    assert!(*width+0.001>=group_minimum_width(group), "weights {weights:?}, widths {widths:?}");
                }
                assert!((widths.iter().sum::<f32>()-available.max(800.0)).abs()<0.01, "weights {weights:?}, widths {widths:?}");
                assert_eq!(groups.iter().map(|g|g.weight).collect::<Vec<_>>(),weights);
            }
        }
        let groups=vec![ReaderGroup{id:1,tabs:vec!["$body".into()],active:"$body".into(),weight:0.62},ReaderGroup{id:2,tabs:vec!["source".into()],active:"source".into(),weight:0.38}];
        let widths=reader_group_widths(&groups,1000.0);
        assert!((widths[0]-620.0).abs()<0.01);
        assert!((widths[1]-380.0).abs()<0.01);
    }

    #[test]
    fn click_outside_reader_viewport_does_not_activate_overflow_group() {
        let mut app=edited_app();
        app.source_workspace.load_text("a".into(),"Notes.txt".into(),"1".into(),"text".into()).unwrap();
        app.source_workspace.set_mode("split").unwrap();
        let focused=app.source_workspace.focus_group;
        let source_group=app.source_workspace.groups.iter().find(|g|g.id!=focused).unwrap().id;
        app.source_workspace.groups_viewport_rect=Some(Rect::from_min_max(pos2(100.0,100.0),pos2(500.0,500.0)));
        app.source_workspace.group_rects=vec![(source_group,Rect::from_min_max(pos2(450.0,100.0),pos2(900.0,500.0)))];
        let epoch=app.source_workspace.view_epoch;
        let ctx=egui::Context::default();
        let mut input=egui::RawInput::default();
        input.events=vec![egui::Event::PointerMoved(pos2(600.0,200.0)),egui::Event::PointerButton{pos:pos2(600.0,200.0),button:egui::PointerButton::Primary,pressed:true,modifiers:egui::Modifiers::default()}];
        ctx.run_ui(input,|ui|prepare_frame(&mut app,ui.ctx())).drop_without_applying_deltas();
        assert_eq!(app.source_workspace.focus_group,focused);
        assert_eq!(app.source_workspace.view_epoch,epoch);
    }

}
