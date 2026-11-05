//! Native project resources and source Q&A. The host owns persistence and model calls;
//! this module only renders snapshots and emits explicit user actions.

use egui::{Align2, Color32, Rect, RichText, Sense, Stroke, Ui, pos2, vec2};
use serde_json::{Value, json};
use std::collections::BTreeMap;

use crate::WordApp;
use crate::theme::{Tokens, animation_time, fit_text, medium, motion, radius, semibold};

const ACTIVITY_BAR_WIDTH: f32 = 48.0;
const ACTIVITY_BUTTON_SIZE: f32 = 40.0;

/// OS-style file colours are the deliberate exception to the two-colour icon rule
/// (DESIGN-SYSTEM.md §2.7 v1.1). The paper stays white in both interface themes.
fn paint_file_icon(ui: &Ui, row: Rect, title: &str, text_dim: Color32) {
    let lower = title.to_lowercase();
    let (color, label) = if lower.ends_with(".pdf") {
        (Color32::from_rgb(0xC4, 0x48, 0x3C), "PDF")
    } else if lower.ends_with(".docx") {
        (Color32::from_rgb(0x2B, 0x57, 0x9A), "W")
    } else if lower.ends_with(".txt") {
        (text_dim, "TXT")
    } else if lower.ends_with(".md") || lower.ends_with(".markdown") {
        (text_dim, "MD")
    } else {
        (text_dim, "")
    };
    let snap = |value: f32| (value * 2.0).round() / 2.0;
    let center = pos2(snap(row.left() + 16.0), snap(row.center().y));
    let grid = Rect::from_center_size(center, vec2(20.0, 20.0));
    let page = Rect::from_min_max(grid.left_top() + vec2(3.5, 3.0), grid.left_top() + vec2(16.5, 17.0));
    let stroke = Stroke::new(1.5, color);
    ui.painter().rect(page, radius::SMALL, Color32::WHITE, stroke, egui::StrokeKind::Inside);
    let fold = page.right_top();
    ui.painter().line_segment([pos2(fold.x - 4.0, fold.y), pos2(fold.x - 4.0, fold.y + 4.0)], stroke);
    ui.painter().line_segment([pos2(fold.x - 4.0, fold.y + 4.0), fold], stroke);
    if !label.is_empty() {
        let size = if label.len() > 1 { 5.0 } else { 6.5 };
        ui.painter().text(pos2(page.center().x, page.bottom() - 3.0), Align2::CENTER_CENTER, label, semibold(size), color);
    }
}

/// A predictable single-line tree row. Its 32px height is part of the native layout contract.
fn file_row(ui: &mut Ui, title: &str, selected: bool) -> egui::Response {
    file_row_details(ui, title, selected, None, false)
}

/// Source rows add real snapshot metadata without changing the established row height or
/// selection semantics. All sources are read-only; search capability remains in the tooltip.
fn file_row_details(ui: &mut Ui, title: &str, selected: bool, version: Option<&str>, read_only: bool) -> egui::Response {
    let t = Tokens::get(ui.ctx());
    let (rect, response) = ui.allocate_exact_size(vec2(ui.available_width(), 32.0), Sense::click());
    response.widget_info(|| egui::WidgetInfo::selected(egui::WidgetType::SelectableLabel, ui.is_enabled(), selected, title));
    if selected {
        ui.painter().rect_filled(rect, radius::BASE, t.checked);
        ui.painter().rect_filled(Rect::from_min_size(rect.left_top() + vec2(0.0, 6.0), vec2(3.0, 20.0)), radius::SMALL, t.accent);
    } else if response.hovered() {
        ui.painter().rect_filled(rect, radius::BASE, t.hover);
    }
    paint_file_icon(ui, rect, title, t.text_dim);

    let version = version.filter(|value| !value.trim().is_empty()).map(|value| {
        if value.chars().all(|ch| ch.is_ascii_digit()) { format!("v{value}") } else { value.to_owned() }
    });
    let meta_font = medium(10.5);
    let badge_width = if read_only { 34.0 } else { 0.0 };
    let version_width = version.as_ref().map_or(0.0, |_| 36.0);
    let metadata_width = badge_width + if read_only && version.is_some() { 6.0 } else { 0.0 } + version_width;
    let ink = if selected { t.accent_text } else { t.text };
    let mut job = egui::text::LayoutJob::single_section(title.to_owned(), egui::text::TextFormat::simple(medium(14.0), ink));
    job.wrap = egui::text::TextWrapping {
        max_width: (rect.width() - 42.0 - metadata_width).max(1.0),
        max_rows: 1,
        break_anywhere: true,
        overflow_character: Some('…'),
    };
    let galley = ui.painter().layout_job(job);
    ui.painter().galley(pos2(rect.left() + 32.0, rect.center().y - galley.size().y * 0.5), galley, ink);

    let mut right = rect.right() - 8.0;
    if read_only {
        let badge = Rect::from_center_size(pos2(right - badge_width * 0.5, rect.center().y), vec2(badge_width, 16.0));
        ui.painter().rect_stroke(badge, radius::SMALL, Stroke::new(1.0, t.border_strong), egui::StrokeKind::Inside);
        ui.painter().text(badge.center(), Align2::CENTER_CENTER, "只读", meta_font.clone(), t.text_dim);
        right = badge.left() - 6.0;
    }
    if let Some(version) = version {
        let shown = fit_text(ui.painter(), &version, &meta_font, 0.0, t.text_dim, version_width);
        ui.painter().text(pos2(right, rect.center().y), Align2::RIGHT_CENTER, shown, meta_font, t.text_dim);
    }
    response
}

/// Activity Bar buttons are real keyboard-focusable widgets. Only the two panel toggles have a
/// selected state; search and delivery immediately dispatch their existing commands.
fn activity_button(ui: &mut Ui, rect: Rect, id: &str, icon: &str, label: &str, selected: bool, badge: usize) -> egui::Response {
    let t = Tokens::get(ui.ctx());
    let response = ui.interact(rect, egui::Id::new(("lingdoc-activity", id)), Sense::click());
    let accessible_label = if badge > 0 { format!("{label}，{badge} 份上下文") } else { label.to_owned() };
    response.widget_info(|| egui::WidgetInfo::selected(egui::WidgetType::Button, ui.is_enabled(), selected, accessible_label.clone()));
    let fill = if response.is_pointer_button_down_on() {
        Some(t.pressed)
    } else if response.hovered() {
        Some(t.hover)
    } else {
        None
    };
    if let Some(fill) = fill {
        ui.painter().rect_filled(rect, radius::PANEL, fill);
    }
    if selected {
        ui.painter().rect_filled(
            Rect::from_min_size(rect.left_top() + vec2(-4.0, 9.0), vec2(3.0, 22.0)),
            radius::SMALL,
            t.accent,
        );
    }
    if response.has_focus() {
        ui.painter().rect_stroke(rect.shrink(1.0), radius::PANEL, Stroke::new(2.0, t.focus_ring), egui::StrokeKind::Inside);
    }
    let color = if selected { t.accent } else if response.hovered() { t.icon } else { t.text_dim };
    crate::icons::paint(
        ui.painter(),
        Rect::from_center_size(rect.center(), vec2(20.0, 20.0)),
        icon,
        color,
        t.accent,
    );
    if badge > 0 {
        let marker = Rect::from_center_size(pos2(rect.right() - 8.0, rect.top() + 8.0), vec2(14.0, 14.0));
        ui.painter().rect_filled(marker, 7.0, t.accent);
        ui.painter().text(marker.center(), Align2::CENTER_CENTER, badge.min(9).to_string(), semibold(9.0), t.on_accent);
    }
    response.on_hover_text(label)
}

fn section_label(ui: &mut Ui, label: &str, count: usize) {
    ui.horizontal(|ui| {
        ui.label(RichText::new(label).font(semibold(14.0)));
        ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
            ui.label(RichText::new(count.to_string()).small().color(Tokens::get(ui.ctx()).text_dim));
        });
    });
}

#[derive(Clone, Default)]
struct Source {
    id: String,
    title: String,
    version: String,
    folder: String,
    readable: bool,
    searchable: bool,
}

#[derive(Clone)]
struct Citation {
    source: Source,
    quote: String,
    page: Option<u64>,
}

#[derive(Clone)]
struct Message {
    id: String,
    role: String,
    text: String,
    citations: Vec<Citation>,
}

#[derive(Clone)]
struct Session {
    id: String,
    title: String,
}

pub struct ProjectWorkspace {
    enabled: bool,
    pub document_bound: bool,
    project_title: String,
    project_id: String,
    active_source_id: String,
    source_row_rects: BTreeMap<String, Rect>,
    import_busy: bool,
    import_error: String,
    imports: Vec<Value>,
    pub project_nav_rect: Option<Rect>,
    pub project_selected: bool,
    project_card_rect: Option<Rect>,
    project_document_rect: Option<Rect>,
    import_button_rect: Option<Rect>,
    resume_available: bool,
    resume_tab_count: u64,
    resume_group_count: u64,
    resume_rect: Option<Rect>,
    project_search: String,
    project_search_rect: Option<Rect>,
    project_search_clear_rect: Option<Rect>,
    project_back_rect: Option<Rect>,
    doc_title: String,
    sources: Vec<Source>,
    sessions: Vec<Session>,
    current_session: String,
    messages: Vec<Message>,
    context_sources: Vec<Source>,
    context_note: String,
    busy: bool,
    error: String,
    draft: String,
    session_drafts: BTreeMap<String, String>,
    pending_text: Option<String>,
    pending_existing_ids: Vec<String>,
    actions: Vec<Value>,
    pub resources_open: bool,
    pub chat_open: bool,
    active: bool,
    pub resource_rect: Option<Rect>,
    pub chat_rect: Option<Rect>,
    pub top_rect: Option<Rect>,
    composer_rect: Option<Rect>,
    send_rect: Option<Rect>,
    chat_toggle_rect: Option<Rect>,
    context_menu_rect: Option<Rect>,
    context_add_rects: BTreeMap<String, Rect>,
    context_remove_rects: BTreeMap<String, Rect>,
    citation_open_rects: BTreeMap<String, Rect>,
}

impl Default for ProjectWorkspace {
    fn default() -> Self {
        Self {
            enabled: false,
            document_bound: false,
            project_title: String::new(),
            project_id: String::new(),
            active_source_id: String::new(),
            source_row_rects: BTreeMap::new(),
            import_busy: false,
            import_error: String::new(),
            imports: Vec::new(),
            project_nav_rect: None,
            project_selected: false,
            project_card_rect: None,
            project_document_rect: None,
            import_button_rect: None,
            resume_available: false,
            resume_tab_count: 0,
            resume_group_count: 0,
            resume_rect: None,
            project_search: String::new(),
            project_search_rect: None,
            project_search_clear_rect: None,
            project_back_rect: None,
            doc_title: String::new(),
            sources: Vec::new(),
            sessions: Vec::new(),
            current_session: String::new(),
            messages: Vec::new(),
            context_sources: Vec::new(),
            context_note: String::new(),
            busy: false,
            error: String::new(),
            draft: String::new(),
            session_drafts: BTreeMap::new(),
            pending_text: None,
            pending_existing_ids: Vec::new(),
            actions: Vec::new(),
            resources_open: true,
            chat_open: false,
            active: false,
            resource_rect: None,
            chat_rect: None,
            top_rect: None,
            composer_rect: None,
            send_rect: None,
            chat_toggle_rect: None,
            context_menu_rect: None,
            context_add_rects: BTreeMap::new(),
            context_remove_rects: BTreeMap::new(),
            citation_open_rects: BTreeMap::new(),
        }
    }
}

fn string(value: &Value, field: &str, max: usize) -> Result<String, String> {
    let text = match value.get(field) {
        None | Some(Value::Null) => String::new(),
        Some(Value::String(text)) => text.clone(),
        Some(Value::Number(n)) if field == "version" => n.to_string(),
        _ => return Err(format!("{field} must be text")),
    };
    if text.len() > max {
        return Err(format!("{field} exceeds the UI limit"));
    }
    Ok(text)
}

fn array<'a>(value: &'a Value, field: &str, max: usize) -> Result<&'a [Value], String> {
    match value.get(field) {
        None | Some(Value::Null) => Ok(&[]),
        Some(Value::Array(items)) if items.len() <= max => Ok(items),
        _ => Err(format!("{field} must be a bounded list")),
    }
}

fn source(value: &Value, citation: bool) -> Result<Source, String> {
    let id = string(value, if citation { "sourceId" } else { "id" }, 512)?;
    if id.is_empty() {
        return Err("source identity is missing".into());
    }
    let capability=|field:&str| match value.get(field) {
        None=>Ok(true),
        Some(Value::Bool(flag))=>Ok(*flag),
        _=>Err(format!("{field} must be boolean")),
    };
    Ok(Source { id, title: string(value, "title", 1024)?, version: string(value, "version", 512)?, folder: string(value, "folder", 256)?, readable:capability("readable")?, searchable:capability("searchable")? })
}

impl ProjectWorkspace {
    /// Validate the entire snapshot before replacing displayed data. Local draft and
    /// panel visibility are never reset by background refreshes.
    pub fn hydrate(&mut self, value: Value) -> Result<(), String> {
        if !value.is_object() {
            return Err("project snapshot must be an object".into());
        }
        let project_title = string(&value, "projectTitle", 1024)?;
        let project_id = string(&value, "projectId", 512)?;
        let active_source_id = if value.get("activeSourceId").is_some() { Some(string(&value, "activeSourceId", 512)?) } else { None };
        let import_error = string(&value, "importError", 4096)?;
        let imports = array(&value, "imports", 500)?.iter().map(|item| {
            Ok(json!({"id":string(item,"id",512)?,"sourceId":string(item,"sourceId",512)?,"name":string(item,"name",1024)?,"status":string(item,"status",128)?,"error":string(item,"error",4096)?}))
        }).collect::<Result<Vec<_>,String>>()?;
        let doc_title = string(&value, if value.get("documentTitle").is_some() { "documentTitle" } else { "docTitle" }, 1024)?;
        let current_session = string(&value, "currentSessionId", 512)?;
        let error = string(&value, "error", 4096)?;
        let context_note = string(&value, "contextNote", 1024)?;
        let sources = array(&value, "sources", 500)?.iter().map(|s| source(s, false)).collect::<Result<Vec<_>, _>>()?;
        let context_sources = array(&value, "contextSources", 100)?.iter().map(|s| source(s, false)).collect::<Result<Vec<_>, _>>()?;
        let sessions = array(&value, "sessions", 500)?
            .iter()
            .map(|s| Ok(Session { id: string(s, "id", 512)?, title: string(s, "title", 1024)? }))
            .collect::<Result<Vec<_>, String>>()?;
        let messages = array(&value, "messages", 1000)?
            .iter()
            .map(|m| {
                let role = string(m, "role", 32)?;
                if !matches!(role.as_str(), "user" | "assistant") {
                    return Err("unsupported message role".into());
                }
                let mut citations = array(m, "citations", 64)?
                    .iter()
                    .map(|c| {
                        Ok(Citation {
                            source: source(c, true)?,
                            quote: string(c, "quote", 16384)?,
                            page: c.get("page").and_then(Value::as_u64).filter(|p| *p > 0),
                        })
                    })
                    .collect::<Result<Vec<_>, String>>()?;
                for used in array(m, "usedSources", 64)? {
                    let used_source = source(used, true)?;
                    if !citations.iter().any(|c| c.source.id == used_source.id && c.source.version == used_source.version) {
                        citations.push(Citation {
                            source: used_source,
                            quote: String::new(),
                            page: used.get("page").and_then(Value::as_u64).filter(|p| *p > 0),
                        });
                    }
                }
                Ok(Message { id: string(m, "id", 512)?, role, text: string(m, "text", 65536)?, citations })
            })
            .collect::<Result<Vec<_>, String>>()?;
        let host_busy = value.get("busy").and_then(Value::as_bool).unwrap_or(false);
        if self.enabled && current_session != self.current_session {
            if self.session_drafts.len() >= 100 && !self.session_drafts.contains_key(&self.current_session) {
                if let Some(oldest) = self.session_drafts.keys().next().cloned() {
                    self.session_drafts.remove(&oldest);
                }
            }
            self.session_drafts.insert(self.current_session.clone(), self.draft.clone());
            self.draft = self.session_drafts.get(&current_session).cloned().unwrap_or_default();
            self.pending_text = None;
            self.pending_existing_ids.clear();
        }
        if let Some(pending) = &self.pending_text {
            let accepted = messages.iter().any(|m| m.role == "user" && m.text == *pending && !self.pending_existing_ids.contains(&m.id));
            if !host_busy && (accepted || !error.is_empty()) {
                if accepted && error.is_empty() && self.draft.trim() == pending.as_str() {
                    self.draft.clear();
                }
                self.pending_text = None;
                self.pending_existing_ids.clear();
            }
        }
        self.resume_available = value.get("resumeAvailable").and_then(Value::as_bool).unwrap_or(false);
        self.resume_tab_count = value.get("resumeTabCount").and_then(Value::as_u64).unwrap_or(0).min(100);
        self.resume_group_count = value.get("resumeGroupCount").and_then(Value::as_u64).unwrap_or(0).min(10);
        self.enabled = true;
        self.document_bound = value.get("documentBound").and_then(Value::as_bool).unwrap_or(true);
        self.project_title = project_title;
        self.project_id = project_id;
        if let Some(id) = active_source_id { self.active_source_id = id; }
        self.import_error = import_error;
        self.imports = imports;
        self.import_busy = value.get("importBusy").and_then(Value::as_bool).unwrap_or(false);
        self.doc_title = doc_title;
        self.current_session = current_session;
        self.sources = sources;
        self.context_sources = context_sources;
        self.context_note = context_note;
        self.sessions = sessions;
        self.messages = messages;
        self.error = error;
        self.busy = host_busy || self.pending_text.is_some();
        Ok(())
    }

    pub fn is_enabled(&self) -> bool { self.enabled }

    pub fn document_title(&self) -> Option<&str> {
        (self.enabled && self.document_bound && !self.doc_title.is_empty()).then_some(self.doc_title.as_str())
    }

    pub fn set_active_source(&mut self, id: &str) { self.active_source_id = id.to_owned(); }

    pub fn route_standalone(&mut self, command: &str, params: &Value) -> bool {
        if !self.enabled || !self.document_bound || !matches!(command, "file.new" | "file.open") { return false; }
        self.enqueue(json!({"kind":"standaloneCommand","command":command,"params":params}));
        true
    }

    pub fn take_actions(&mut self) -> Vec<Value> {
        std::mem::take(&mut self.actions)
    }

    pub fn auxiliary_active(&self) -> bool {
        self.enabled && self.document_bound && self.active
    }

    /// Explicitly hand focus back after a user chooses a body/source view.
    pub fn release_focus(&mut self) {
        self.active = false;
    }

    pub fn status(&self) -> Value {
        let rect = |r: Option<Rect>| r.map(|r| json!({"x":r.left(),"y":r.top(),"width":r.width(),"height":r.height()}));
        let mut status = json!({"enabled":self.enabled,"documentBound":self.document_bound,"resourcesOpen":self.resources_open,"chatOpen":self.chat_open,
            "auxiliaryActive":self.auxiliary_active(),"busy":self.busy,"draft":self.draft,"error":self.error,
            "currentSessionId":self.current_session,"sourceCount":self.sources.len(),"messageCount":self.messages.len(),
            "projectTitle":self.project_title,"documentTitle":self.doc_title,
            "activeSourceId":self.active_source_id,"sourceRowRects":self.source_row_rects.iter().map(|(id,r)|(id.clone(),rect(Some(*r)))).collect::<BTreeMap<_,_>>(),"projectId":self.project_id,"importBusy":self.import_busy,"importError":self.import_error,"imports":self.imports,
            "projectSelected":self.project_selected,"projectCardRect":rect(self.project_card_rect),"projectNavRect":rect(self.project_nav_rect),"projectDocumentRect":rect(self.project_document_rect),"importButtonRect":rect(self.import_button_rect),
            "sources":self.sources.iter().map(|s|json!({"id":s.id,"title":s.title,"version":s.version,"folder":s.folder,"readable":s.readable,"searchable":s.searchable})).collect::<Vec<_>>(),
            "sessions":self.sessions.iter().map(|s|json!({"id":s.id,"title":s.title})).collect::<Vec<_>>(),
            "messages":self.messages.iter().map(|m|json!({"id":m.id,"role":m.role,"text":m.text,"citations":m.citations.iter().map(|c|json!({"sourceId":c.source.id,"title":c.source.title,"version":c.source.version,"quote":c.quote,"page":c.page})).collect::<Vec<_>>()})).collect::<Vec<_>>(),
            "contextSources":self.context_sources.iter().map(|s|json!({"id":s.id,"title":s.title,"version":s.version})).collect::<Vec<_>>(),
            "contextNote":self.context_note,"pendingActions":self.actions.len(),"resourceRect":rect(self.resource_rect),"chatRect":rect(self.chat_rect),"topRect":rect(self.top_rect),
            "composerRect":rect(self.composer_rect),"sendRect":rect(self.send_rect)});
        status["chatToggleRect"]=json!(rect(self.chat_toggle_rect));
        status["contextMenuRect"]=json!(rect(self.context_menu_rect));
        status["contextAddRects"]=json!(self.context_add_rects.iter().map(|(id,r)|(id.clone(),rect(Some(*r)))).collect::<BTreeMap<_,_>>());
        status["contextRemoveRects"]=json!(self.context_remove_rects.iter().map(|(id,r)|(id.clone(),rect(Some(*r)))).collect::<BTreeMap<_,_>>());
        status["citationOpenRects"]=json!(self.citation_open_rects.iter().map(|(id,r)|(id.clone(),rect(Some(*r)))).collect::<BTreeMap<_,_>>());
        let extra = json!({"resumeAvailable":self.resume_available,"resumeTabCount":self.resume_tab_count,
            "resumeGroupCount":self.resume_group_count,"resumeRect":rect(self.resume_rect),
            "projectSearch":self.project_search,"projectSearchRect":rect(self.project_search_rect),
            "projectSearchClearRect":rect(self.project_search_clear_rect),"projectBackRect":rect(self.project_back_rect)});
        if let (Some(target),Some(fields))=(status.as_object_mut(),extra.as_object()){target.extend(fields.clone());}
        status
    }

    fn enqueue(&mut self, action: Value) -> bool {
        if self.actions.len() >= 32 {
            self.error = "操作仍在处理中，请稍后再试。".into();
            return false;
        }
        self.actions.push(action);
        true
    }

    fn send(&mut self) {
        let text = self.draft.trim().to_string();
        if self.busy || text.is_empty() {
            return;
        }
        if text.chars().count() > 4000 {
            self.error = "问题最多4000字符，请缩短后再发送。".into();
            return;
        }
        if self.enqueue(json!({"kind":"send","text":text})) {
            self.pending_text = Some(text);
            self.pending_existing_ids = self.messages.iter().map(|m| m.id.clone()).collect();
            self.busy = true;
            self.error.clear();
        }
    }

    fn request_import(&mut self) {
        if !self.enabled || self.import_busy { return; }
        if self.enqueue(json!({"kind":"importSources","projectId":self.project_id})) {
            self.import_busy = true;
            self.import_error.clear();
        }
    }

    fn import_button(&mut self, ui: &mut Ui) {
        let label = if self.import_busy { "正在导入…" } else { "＋ 导入资料" };
        let response = ui.add_enabled(
            !self.import_busy,
            egui::Button::new(RichText::new(label).font(medium(14.0))).min_size(vec2(104.0, 30.0)),
        );
        self.import_button_rect = Some(response.rect);
        if response.clicked() {
            self.request_import();
        }
    }

    /// Persistent Explorer footer. This is only a new visual entry to the established import
    /// action; no file access or import transaction lives in the native UI.
    fn sidebar_import_button(&mut self, ui: &mut Ui) {
        let t = Tokens::get(ui.ctx());
        let enabled = !self.import_busy;
        let (rect, response) = ui.allocate_exact_size(
            vec2(ui.available_width(), 34.0),
            if enabled { Sense::click() } else { Sense::hover() },
        );
        let label = if self.import_busy { "正在导入…" } else { "＋ 导入资料" };
        response.widget_info(|| egui::WidgetInfo::labeled(egui::WidgetType::Button, enabled, label));
        if response.hovered() && enabled {
            ui.painter().rect_filled(rect, radius::CARD, t.hover);
        }
        let border = if response.hovered() && enabled { t.accent } else { t.border_strong };
        let dash = |from: egui::Pos2, to: egui::Pos2| {
            let delta = to - from;
            let length = delta.length();
            if length <= 0.0 { return; }
            let direction = delta / length;
            let mut offset = 0.0;
            while offset < length {
                let end = (offset + 4.0).min(length);
                ui.painter().line_segment([from + direction * offset, from + direction * end], Stroke::new(1.0, border));
                offset += 7.0;
            }
        };
        dash(rect.left_top(), rect.right_top());
        dash(rect.right_top(), rect.right_bottom());
        dash(rect.right_bottom(), rect.left_bottom());
        dash(rect.left_bottom(), rect.left_top());
        if response.has_focus() {
            ui.painter().rect_stroke(rect.expand(2.0), radius::CARD, Stroke::new(2.0, t.focus_ring), egui::StrokeKind::Inside);
        }
        let ink = if !enabled { t.text_disabled } else if response.hovered() { t.accent_text } else { t.text };
        ui.painter().text(rect.center(), Align2::CENTER_CENTER, label, medium(12.0), ink);
        self.import_button_rect = Some(response.rect);
        if response.on_hover_text("导入 TXT / MD / PDF / DOCX 项目副本").clicked() && enabled {
            self.request_import();
        }
    }

    fn import_error(&self, ui: &mut Ui) {
        if !self.import_error.is_empty() {
            egui::CollapsingHeader::new("资料导入未完成 · 查看详情").id_salt("import-error-details").show(ui, |ui| {
                ui.add(egui::Label::new(RichText::new(&self.import_error).small()).wrap());
            });
        }
    }

    /// Native Backstage project entry. Only real hydrated project data is shown.
    pub fn project_page(&mut self, ui: &mut Ui) {
        ui.set_max_width(ui.available_width().min(760.0));
        ui.label(RichText::new("项目").font(semibold(26.0)));
        ui.add_space(24.0);
        self.resume_rect = None;
        self.project_search_rect = None;
        self.project_search_clear_rect = None;
        self.project_back_rect = None;
        self.source_row_rects.clear();
        self.project_card_rect = None;
        self.project_document_rect = None;
        self.import_button_rect = None;
        if !self.enabled || self.project_id.trim().is_empty() {
            ui.label(RichText::new("项目尚未连接").font(semibold(17.0)));
            ui.add_space(8.0);
            if !self.error.is_empty() {
                egui::ScrollArea::vertical().id_salt("project-connection-error").max_height(80.0).show(ui, |ui| {
                    ui.add(egui::Label::new(RichText::new(&self.error).font(medium(14.0))).wrap());
                });
            }
            ui.add(egui::Label::new("仍可从左侧新建或打开文稿。").wrap());
            return;
        }
        let project_title = if self.project_title.trim().is_empty() { "未命名项目" } else { &self.project_title };
        if !self.project_selected {
            let t = Tokens::get(ui.ctx());
            let (card, response) = ui.allocate_exact_size(vec2(ui.available_width().min(420.0), 96.0), Sense::click());
            response.widget_info(|| egui::WidgetInfo::labeled(egui::WidgetType::Button, true, project_title));
            ui.painter().rect_filled(card, 6.0, if response.hovered() { t.hover } else { t.input });
            ui.painter().rect_stroke(card, 6.0, Stroke::new(if response.has_focus() { 2.0 } else { 1.0 }, if response.has_focus() { t.accent } else { t.border_strong }), egui::StrokeKind::Inside);
            let mut job = egui::text::LayoutJob::single_section(project_title.to_owned(), egui::text::TextFormat::simple(semibold(17.0), t.text));
            job.wrap = egui::text::TextWrapping { max_width: (card.width() - 32.0).max(1.0), max_rows: 1, break_anywhere: true, overflow_character: Some('…') };
            let galley = ui.painter().layout_job(job);
            ui.painter().galley(card.left_top() + vec2(16.0, 18.0), galley, t.text);
            ui.painter().text(card.left_top() + vec2(16.0, 57.0), egui::Align2::LEFT_TOP,
                format!("{} 份文稿 · {} 份资料", usize::from(!self.doc_title.is_empty()), self.sources.len()), medium(14.0), t.text_dim);
            let response = response.on_hover_text(format!("{}\n{}\n打开项目文稿与资料", project_title, self.doc_title));
            self.project_card_rect = Some(response.rect);
            if response.clicked() { self.project_selected = true; }
            return;
        }
        let back = ui.add(egui::Button::new("‹ 项目列表").min_size(vec2(104.0, 30.0)));
        self.project_back_rect = Some(back.rect);
        if back.clicked() {
            self.project_selected = false;
            return;
        }
        ui.add_space(20.0);
        ui.add(egui::Label::new(RichText::new(project_title).font(semibold(24.0))).truncate()).on_hover_text(&self.project_title);
        ui.add_space(16.0);
        if !self.error.is_empty() && self.error != self.import_error {
            ui.label(RichText::new("上次操作未完成").font(semibold(14.0)).color(ui.visuals().error_fg_color));
            egui::ScrollArea::vertical().id_salt("project-operation-error").max_height(64.0).show(ui, |ui| {
                ui.add(egui::Label::new(RichText::new(&self.error).font(medium(14.0))).wrap());
            });
            ui.add_space(12.0);
        }
        if self.resume_available {
            let t = Tokens::get(ui.ctx());
            let response = ui.add(egui::Button::new(RichText::new("继续上次工作").font(semibold(15.0)).color(t.on_accent)).fill(t.accent).min_size(vec2(176.0, 40.0)))
                .on_hover_text("打开已保存的项目文稿，并恢复本浏览器的资料标签和阅读分组；不恢复未保存的正文。");
            self.resume_rect = Some(response.rect);
            if response.clicked() { self.enqueue(json!({"kind":"enterProject","projectId":self.project_id,"resume":true})); }
            ui.add_space(4.0);
            ui.add(egui::Label::new(RichText::new(format!("恢复 {} 份资料 · {} 个阅读组", self.resume_tab_count, self.resume_group_count)).font(medium(14.0)).color(Tokens::get(ui.ctx()).text_dim)).wrap());
            ui.add_space(16.0);
        }
        ui.horizontal(|ui| {
            let clear_width = 46.0 + ui.spacing().item_spacing.x;
            let search = ui.add_sized(vec2((ui.available_width() - clear_width).max(40.0), 34.0),
                egui::TextEdit::singleline(&mut self.project_search).font(medium(14.0)).char_limit(256).hint_text("查找文稿、资料或分类").margin(vec2(10.0, 8.0)));
            self.project_search_rect = Some(search.rect);
            let clear = ui.add_enabled(!self.project_search.is_empty(), egui::Button::new("清空").min_size(vec2(46.0, 34.0)));
            self.project_search_clear_rect = Some(clear.rect);
            if clear.clicked() { self.project_search.clear(); search.request_focus(); }
        });
        let query = self.project_search.trim().to_lowercase();
        ui.add_space(20.0);
        section_label(ui, "文稿", usize::from(!self.doc_title.is_empty()));
        ui.add_space(6.0);
        let title = if self.doc_title.is_empty() { "打开项目文稿" } else { &self.doc_title };
        if self.doc_title.is_empty() {
            ui.label(RichText::new("项目中尚无文稿").font(medium(14.0)));
        } else if query.is_empty() || title.to_lowercase().contains(&query) {
            let response = file_row(ui, title, false).on_hover_text(format!("{title}\n在 WordCraft 中打开项目文稿"));
            self.project_document_rect = Some(response.rect);
            if response.clicked() { self.enqueue(json!({"kind":"enterProject","projectId":self.project_id})); }
        } else { ui.label(RichText::new("没有匹配的文稿").small()); }
        ui.add_space(24.0);
        ui.horizontal(|ui| {
            ui.label(RichText::new("资料").font(semibold(14.0)));
            ui.label(RichText::new(self.sources.len().to_string()).small().color(Tokens::get(ui.ctx()).text_dim));
            ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| self.import_button(ui));
        });
        self.import_error(ui);
        ui.add_space(8.0);
        let sources = self.sources.clone();
        egui::ScrollArea::vertical().id_salt("backstage-project-sources").show(ui, |ui| {
            for (index, record) in self.imports.iter().enumerate() {
                if !query.is_empty() && !record["name"].as_str().unwrap_or_default().to_lowercase().contains(&query) { continue; }
                let status = record["status"].as_str().unwrap_or_default();
                if matches!(status, "ready" | "success" | "completed" | "imported") { continue; }
                // Read-only imports already have a selectable source row. Do not list the same
                // material twice; capability information belongs beside that source.
                if matches!(status, "reading_only" | "read_only" | "readable" | "readable_only")
                    && record["sourceId"].as_str().is_some_and(|id| sources.iter().any(|s| s.id == id)) { continue; }
                let label = match status {
                    "failed" | "error" => "导入失败",
                    "reading_only" | "read_only" | "readable" | "readable_only" => "仅阅读",
                    "pending" | "copying" | "processing" | "importing" | "extracting" | "uploading" => "处理中",
                    _ => "等待处理",
                };
                ui.horizontal(|ui| {
                    ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                        ui.label(RichText::new(label).font(medium(13.0)).color(Tokens::get(ui.ctx()).text_dim));
                        let name = record["name"].as_str().unwrap_or_default();
                        ui.add(egui::Label::new(RichText::new(name).font(medium(14.0))).truncate()).on_hover_text(name);
                    });
                });
                if let Some(error) = record["error"].as_str().filter(|s| !s.is_empty()) { egui::CollapsingHeader::new("详情").id_salt(("import-record",index,record["id"].as_str())).show(ui, |ui| { ui.add(egui::Label::new(RichText::new(error).small()).wrap()); }); }
            }
            let mut matches = 0;
            for source in sources.iter().filter(|s| query.is_empty() || s.title.to_lowercase().contains(&query) || s.folder.to_lowercase().contains(&query)) {
                matches += 1;
                let folder = if source.folder.is_empty() { "未分类" } else { &source.folder };
                let capability = if !source.readable { "暂不可阅读" } else if !source.searchable { "仅阅读 · 不参与问答" } else { "可用于问答" };
                let details = format!("{folder} · 版本 {} · {capability}", source.version);
                ui.push_id(("project-source", &source.id), |ui| {
                    let row = ui.add_enabled_ui(source.readable, |ui| file_row(ui, &source.title, false)).inner
                        .on_hover_text(format!("{}\n{details}", source.title));
                    self.source_row_rects.insert(source.id.clone(), row.rect);
                    if row.clicked() { self.enqueue(json!({"kind":"openSource","sourceId":source.id.clone(),"version":source.version.clone()})); }
                    ui.add(egui::Label::new(RichText::new(&details).font(medium(12.0)).color(Tokens::get(ui.ctx()).text_dim)).truncate()).on_hover_text(&details);
                    ui.add_space(6.0);
                });
            }
            if matches == 0 && !query.is_empty() { ui.label(RichText::new("没有匹配的资料").small()); }
            if sources.is_empty() && self.imports.is_empty() {
                ui.add_space(12.0);
                ui.add(egui::Label::new(RichText::new("尚无资料。点击“导入资料”添加项目副本。").font(medium(14.0)).color(Tokens::get(ui.ctx()).text_dim)).wrap());
            }
        });
    }

    fn resources(&mut self, ui: &mut Ui) {
        ui.add(egui::Label::new(RichText::new("项目资料").font(semibold(12.0)).color(Tokens::get(ui.ctx()).text_dim)).selectable(false));
        self.source_row_rects.clear();
        self.import_error(ui);
        ui.add_space(6.0);
        let mut folders = vec!["申报要求与模板".to_string(), "参考文献".to_string(), "前期研究材料".to_string()];
        for source in &self.sources {
            if !source.folder.is_empty() && !folders.contains(&source.folder) {
                folders.push(source.folder.clone());
            }
        }
        if self.sources.iter().any(|s| s.folder.is_empty()) {
            folders.push("未分类".into());
        }
        let sources = self.sources.clone();
        // Reserve the footer before laying out the tree, so import stays visible at every
        // content length instead of scrolling away with the folders.
        let body_height = (ui.available_height() - 52.0).max(0.0);
        egui::ScrollArea::vertical()
            .id_salt("project-source-tree")
            .max_height(body_height)
            .auto_shrink([false, false])
            .show(ui, |ui| {
                for folder in folders {
                    let count = sources.iter().filter(|s| if s.folder.is_empty() { folder == "未分类" } else { s.folder == folder }).count();
                    egui::CollapsingHeader::new(RichText::new(format!("{folder}  {count}")).font(medium(14.0)))
                        .id_salt(("source-folder", &folder))
                        .default_open(true)
                        .show(ui, |ui| {
                            for source in sources.iter().filter(|s| if s.folder.is_empty() { folder == "未分类" } else { s.folder == folder }) {
                                let selected = self.active_source_id == source.id;
                                let capability = if !source.readable { "暂不可阅读" } else if !source.searchable { "仅阅读，暂不可用于问答" } else { "只读，可用于问答" };
                                let row = file_row_details(ui, &source.title, selected, Some(&source.version), true)
                                    .on_hover_text(format!("{} · {} · {}{}", source.title, source.version, capability, if selected { " · 正在阅读" } else { "" }));
                                self.source_row_rects.insert(source.id.clone(), row.rect);
                                if row.clicked() {
                                    self.enqueue(json!({"kind":"openSource","sourceId":source.id.clone(),"version":source.version.clone()}));
                                }
                            }
                        });
                    ui.add_space(4.0);
                }
                if sources.is_empty() {
                    ui.add_space(12.0);
                    ui.add(egui::Label::new(RichText::new("尚无资料。使用下方入口导入项目副本。").font(medium(12.0)).color(Tokens::get(ui.ctx()).text_dim)).wrap());
                }
            });
        ui.add_space(8.0);
        self.sidebar_import_button(ui);
    }

    fn chat(&mut self, ui: &mut Ui) {
        let current_title = self.sessions.iter().find(|s| s.id == self.current_session).map(|s| s.title.clone()).unwrap_or_else(|| "新会话".into());
        let sessions = self.sessions.clone();
        ui.horizontal(|ui| {
            ui.label(RichText::new("资料问答").font(semibold(15.0)));
            ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                if ui.add(egui::Button::new("×").min_size(vec2(28.0, 28.0))).on_hover_text("收起聊天").clicked() {
                    self.chat_open = false;
                }
                if ui.add_enabled(!self.busy, egui::Button::new("＋").min_size(vec2(28.0, 28.0))).on_hover_text("新建会话").clicked() {
                    self.enqueue(json!({"kind":"newSession"}));
                }
                let history = ui.add_enabled_ui(!self.busy, |ui| {
                    ui.menu_button(RichText::new("⏱").font(medium(16.0)), |ui| {
                        ui.set_min_width(220.0);
                        if sessions.is_empty() {
                            ui.label(RichText::new("暂无历史会话").small().weak());
                        }
                        for session in sessions {
                            if ui.selectable_label(session.id == self.current_session, &session.title).clicked() {
                                self.enqueue(json!({"kind":"selectSession","sessionId":session.id}));
                                ui.close();
                            }
                        }
                    }).response
                }).inner;
                history.on_hover_text(format!("历史会话\n当前：{current_title}"));
            });
        });
        ui.separator();
        if !self.context_note.is_empty() {
            ui.add(egui::Label::new(RichText::new(&self.context_note).font(medium(14.0)).color(Tokens::get(ui.ctx()).text_dim)).wrap());
        }
        let messages = self.messages.clone();
        let error_height = if self.error.is_empty() { 0.0 } else { 58.0 };
        let context_height = if self.context_sources.is_empty() { 0.0 } else { (self.context_sources.len() as f32 * 37.0).min(102.0) };
        let height = (ui.available_height() - 280.0 - context_height - error_height).max(0.0);
        egui::ScrollArea::vertical().id_salt(("project-chat-messages", &self.current_session)).max_height(height).stick_to_bottom(true).show(
            ui,
            |ui| {
                ui.set_min_height(height);
                if messages.is_empty() {
                    ui.add_space(24.0);
                    ui.label(RichText::new("向项目资料提问").font(semibold(16.0)));
                    ui.add_space(6.0);
                    ui.add(egui::Label::new(RichText::new("直接提问，或添加资料后连续追问。回答附有可打开的出处。").color(Tokens::get(ui.ctx()).text_dim)).wrap());
                }
                for message in messages {
                    ui.push_id(&message.id, |ui| {
                        let t = Tokens::get(ui.ctx());
                        let is_user = message.role == "user";
                        // 用户消息带弱底色气泡，AI 消息保持纸面留白：角色一眼可辨（DESIGN-SYSTEM.md §0 智能域）。
                        let bubble = egui::Frame::NONE.corner_radius(egui::CornerRadius::same(8)).inner_margin(egui::Margin::symmetric(12, 8));
                        let bubble = if is_user { bubble.fill(t.hover) } else { bubble };
                        bubble.show(ui, |ui| {
                            ui.label(RichText::new(if is_user { "你" } else { "AI 助手" }).font(semibold(13.0)).color(if is_user { t.accent_text } else { t.text_dim }));
                            ui.add(egui::Label::new(RichText::new(&message.text).font(medium(15.0))).wrap());
                            for citation in message.citations {
                                let t = Tokens::get(ui.ctx());
                                egui::Frame::NONE.fill(t.input).inner_margin(egui::Margin::same(10)).corner_radius(egui::CornerRadius::same(6)).stroke(Stroke::new(1.0, t.border)).show(ui, |ui| {
                                    ui.label(RichText::new(if citation.quote.is_empty(){"本轮参考资料"}else{"原文出处"}).small().color(t.text_dim));
                                    ui.add(egui::Label::new(RichText::new(&citation.source.title).font(semibold(14.0))).wrap());
                                    let location = citation.page.map(|p| format!(" · 第 {p} 页")).unwrap_or_default();
                                    ui.label(RichText::new(format!("{}{}", citation.source.version, location)).small().color(t.text_dim));
                                    if !citation.quote.is_empty() {
                                        egui::CollapsingHeader::new(RichText::new("引用原句").font(medium(14.0)))
                                            .id_salt(("citation-quote", &citation.source.id, &citation.source.version, citation.page, &citation.quote))
                                            .default_open(false)
                                            .show(ui, |ui| {
                                                ui.add(egui::Label::new(RichText::new(&citation.quote).font(medium(14.0)).color(t.text)).wrap());
                                            });
                                    }
                                    ui.horizontal_wrapped(|ui| {
                                        let open=ui.add(egui::Button::new(RichText::new(if citation.quote.is_empty() { "打开资料" } else { "查看出处" }).color(t.info)).min_size(vec2(72.0, 28.0)));
                                        self.citation_open_rects.insert(json!([message.id,citation.source.id,citation.source.version,citation.page]).to_string(),open.rect);
                                        if open.clicked() {
                                            self.enqueue(json!({"kind":"openSource","sourceId":citation.source.id,"version":citation.source.version,"page":citation.page}));
                                        }
                                        let pinned = self.context_sources.iter().any(|s| s.id == citation.source.id && s.version == citation.source.version);
                                        let context_full=self.context_sources.len()>=4;
                                        if ui
                                            .add_enabled(!self.busy && !pinned && !context_full, egui::Button::new(if pinned { "已加入" } else { "加入会话" }).min_size(vec2(72.0, 28.0)))
                                            .on_hover_text(if pinned { "已在当前会话中" } else if context_full { "已添加4份资料，请先移除一份" } else { "加入当前会话，供后续追问使用" })
                                            .clicked()
                                        {
                                            self.enqueue(json!({"kind":"addContext","sourceId":citation.source.id,"version":citation.source.version}));
                                        }
                                    });
                                });
                            }
                        });
                        ui.add_space(10.0);
                    });
                }
            },
        );
        if !self.error.is_empty() {
            // 失败状态独立成条：危险色弱底 + 描边，文案保持宿主原文（不自动重试，KIMI-CONTRACT）。
            let t = Tokens::get(ui.ctx());
            egui::Frame::NONE
                .fill(t.danger.gamma_multiply(0.08))
                .stroke(Stroke::new(1.0, t.danger.gamma_multiply(0.4)))
                .corner_radius(egui::CornerRadius::same(6))
                .inner_margin(egui::Margin::symmetric(10, 6))
                .show(ui, |ui| {
                    egui::ScrollArea::vertical().id_salt("project-chat-error").max_height(40.0).show(ui, |ui| {
                        ui.add(egui::Label::new(RichText::new(&self.error).font(medium(14.0)).color(t.danger)).wrap());
                    });
                });
            ui.add_space(4.0);
        }
        ui.add_space(8.0);
        let quick_enabled = !self.busy && !self.context_sources.is_empty() && self.draft.trim().is_empty();
        let quick_hint = if self.busy {
            "正在回答，完成后可使用快捷提问"
        } else if self.context_sources.is_empty() {
            "请先添加至少一份上下文资料，再使用快捷提问"
        } else if !self.draft.trim().is_empty() {
            "请先发送或清空当前输入，避免快捷提问覆盖草稿"
        } else {
            "使用当前上下文资料发送这条预设提问"
        };
        ui.horizontal_wrapped(|ui| {
            ui.spacing_mut().item_spacing = vec2(6.0, 6.0);
            for prompt in ["总结这份资料", "提炼可引用观点", "核对这段出处"] {
                let chip = ui.add_enabled(
                    quick_enabled,
                    egui::Button::new(RichText::new(prompt).font(medium(13.0)))
                        .corner_radius(egui::CornerRadius::same(6))
                        .min_size(vec2(0.0, 26.0)),
                ).on_hover_text(quick_hint);
                if chip.clicked() {
                    self.draft = prompt.into();
                    self.send();
                }
            }
        });
        if self.context_sources.is_empty() {
            ui.add_space(4.0);
            ui.label(RichText::new("添加至少一份上下文资料后，可使用快捷提问。").small().color(Tokens::get(ui.ctx()).text_dim));
        }
        if !self.context_sources.is_empty() {
            ui.add_space(8.0);
            let sources = self.context_sources.clone();
            egui::ScrollArea::vertical().id_salt("project-chat-context").max_height(96.0).show(ui, |ui| {
                for source in sources {
                    // 每份上下文是一枚 chip：品牌弱强调底 + 圆角，移除动作用危险色（DESIGN-SYSTEM.md §2.5）。
                    let t = Tokens::get(ui.ctx());
                    egui::Frame::NONE
                        .fill(t.checked)
                        .corner_radius(egui::CornerRadius::same(6))
                        .inner_margin(egui::Margin::symmetric(10, 3))
                        .show(ui, |ui| {
                            ui.horizontal(|ui| {
                                ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                                    let remove=ui.add_enabled(!self.busy, egui::Button::new(RichText::new("移除").small().color(t.danger)).min_size(vec2(42.0,28.0)));
                                    self.context_remove_rects.insert(source.id.clone(),remove.rect);
                                    if remove.on_hover_text("不再加入后续上下文；历史回答不变").clicked() {
                                        self.enqueue(json!({"kind":"removeContext","sourceId":source.id}));
                                    }
                                    let label=format!("{} · {}",source.title,source.version);
                                    let open=ui.add_sized(vec2(ui.available_width().max(1.0),28.0),egui::Button::new(RichText::new(&label).small().color(t.accent_text)).frame_when_inactive(false).truncate());
                                    if open.on_hover_text(format!("{label}\n打开这份参考资料")).clicked(){self.enqueue(json!({"kind":"openSource","sourceId":source.id,"version":source.version}));}
                                });
                            });
                        });
                    ui.add_space(3.0);
                }
            });
        }
        ui.add_space(8.0);
        let t = Tokens::get(ui.ctx());
        egui::Frame::NONE
            .fill(t.input)
            .stroke(Stroke::new(1.0, t.border_strong))
            .corner_radius(egui::CornerRadius::same(8))
            .inner_margin(egui::Margin::symmetric(8, 6))
            .show(ui, |ui| {
                let composer_scroll = egui::ScrollArea::vertical()
                    .id_salt("project-chat-composer-scroll")
                    .max_height(76.0)
                    .show(ui, |ui| ui.add(
                        egui::TextEdit::multiline(&mut self.draft)
                            .id_salt("project-chat-draft")
                            .hint_text(if self.messages.is_empty(){"就项目资料提问，或继续追问…"}else{"继续追问，或提出新问题…"})
                            .font(medium(15.0))
                            .char_limit(4000)
                            .desired_rows(3)
                            .desired_width(f32::INFINITY)
                            .frame(egui::Frame::NONE),
                    ));
                let composer = composer_scroll.inner;
                self.composer_rect = Some(composer.rect.intersect(composer_scroll.inner_rect));
                if composer.has_focus() {
                    self.active = true;
                }
                ui.separator();
                ui.horizontal(|ui| {
                    let available=self.sources.clone();
                    let menu=ui.menu_button(RichText::new("＋").font(medium(16.0)),|ui| {
                        ui.set_max_width(300.0);
                        if available.is_empty(){ui.label("项目还没有资料，请先导入。");}
                        egui::ScrollArea::vertical().max_height(240.0).show(ui,|ui| {
                            for source in available {
                                let pinned=self.context_sources.iter().any(|s|s.id==source.id&&s.version==source.version);
                                let label=if !source.searchable {format!("{} · 仅阅读，暂不能用于问答",source.title)} else if pinned {format!("✓ {}",source.title)} else {source.title.clone()};
                                let row=ui.add_enabled(!self.busy&&!pinned&&source.searchable&&self.context_sources.len()<4,egui::Button::new(label).wrap());
                                self.context_add_rects.insert(source.id.clone(),row.rect);
                                if row.on_hover_text(format!("{} · {}",source.title,source.version)).clicked(){
                                    self.enqueue(json!({"kind":"addContext","sourceId":source.id,"version":source.version}));ui.close();
                                }
                            }
                        });
                        ui.label(RichText::new("加入后保留在本会话，可连续追问。").small().weak());
                    });
                    self.context_menu_rect=Some(menu.response.rect);
                    menu.response.on_hover_text("添加资料（最多 4 份）");
                    ui.label(RichText::new("模型：项目配置").small().color(t.text_dim))
                        .on_hover_text("模型由宿主项目配置决定；当前快照未提供具体型号，此处仅作只读说明。");
                    ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                        let send = ui.add_enabled(
                            !self.busy && !self.draft.trim().is_empty(),
                            egui::Button::new(RichText::new("发送").color(t.on_accent).font(medium(14.0)))
                                .fill(t.accent)
                                .corner_radius(egui::CornerRadius::same(4))
                                .min_size(vec2(64.0, 28.0)),
                        );
                        self.send_rect = Some(send.rect);
                        if send.clicked() {
                            self.send();
                        }
                    });
                });
            });
        ui.add_space(6.0);
        ui.horizontal_wrapped(|ui| {
            if self.busy {
                // 呼吸脉冲点（motion/pulse）：动画开关关闭时静态显示，不请求重绘（P3 的公共判定）。
                let strength = if crate::theme::animations_enabled(ui.ctx()) {
                    let phase = (ui.input(|i| i.time) % crate::theme::motion::PULSE as f64 / crate::theme::motion::PULSE as f64) as f32;
                    ui.ctx().request_repaint();
                    0.45 + 0.55 * (phase * std::f32::consts::TAU).sin().abs()
                } else {
                    1.0
                };
                let (dot, _) = ui.allocate_exact_size(vec2(14.0, 14.0), Sense::hover());
                ui.painter().circle_filled(dot.center(), 4.0, t.accent.gamma_multiply(strength));
                ui.label(RichText::new("正在检索资料并回答…").small().color(t.text_dim));
            }
            let context_len = self.context_sources.len();
            // 快照不含上下文字符数，只展示真实份数；满 4 份转为警告色，不伪造字符预算。
            let count_color = if context_len >= 4 { t.warning } else { t.text_dim };
            ui.label(RichText::new(format!("上下文 {context_len} / 4 份")).small().color(count_color));
            let count=self.draft.chars().count();
            if count>=3500 {ui.label(RichText::new(format!("问题 {count} / 4000")).small().weak());}
        });
        ui.vertical_centered(|ui| {
            ui.label(RichText::new("AI 生成内容，仅供参考；结论以原文出处为准").small().color(t.text_dim));
        });
    }
}

fn surrender_body(app: &mut WordApp, ctx: &egui::Context) {
    app.canvas.focused = false;
    app.canvas.want_focus = false;
    if let Some((id, _)) = app.canvas.widget_id {
        ctx.memory_mut(|m| m.surrender_focus(id));
    }
}

/// Run before command/paste processing and again after panel layout. The native
/// document must not consume the click frame's keys with last frame's focus.
pub fn prepare_frame(app: &mut WordApp, ctx: &egui::Context) {
    if !app.project_workspace.enabled || !app.project_workspace.document_bound {
        return;
    }
    if let Some(pos) = ctx.input(|i| if i.pointer.any_pressed() { i.pointer.interact_pos() } else { None }) {
        let state = &mut app.project_workspace;
        let inside = [state.resource_rect, state.chat_rect, state.top_rect].iter().flatten().any(|r| r.contains(pos));
        if inside {
            state.active = true;
        } else if state.top_rect.is_some_and(|r| pos.x >= r.right()) {
            state.active = false;
        }
    }
    if app.project_workspace.auxiliary_active() {
        surrender_body(app, ctx);
    }
}

fn activity_bar(app: &mut WordApp, ui: &mut Ui) {
    ui.spacing_mut().item_spacing.y = 2.0;
    let (resources_rect, _) = ui.allocate_exact_size(vec2(ACTIVITY_BUTTON_SIZE, ACTIVITY_BUTTON_SIZE), Sense::hover());
    let resources_open = app.project_workspace.resources_open;
    if activity_button(ui, resources_rect, "sources", "folder", "资料", resources_open, 0).clicked() {
        app.project_workspace.resources_open = !resources_open;
    }
    let (chat_rect, _) = ui.allocate_exact_size(vec2(ACTIVITY_BUTTON_SIZE, ACTIVITY_BUTTON_SIZE), Sense::hover());
    let chat_open = app.project_workspace.chat_open;
    let context_count = app.project_workspace.context_sources.len();
    let chat_toggle = activity_button(ui, chat_rect, "chat", "comment", "资料问答", chat_open, context_count);
    app.project_workspace.chat_toggle_rect = Some(chat_toggle.rect);
    if chat_toggle.clicked() {
        app.project_workspace.chat_open = !chat_open;
    }
    let (search_rect, _) = ui.allocate_exact_size(vec2(ACTIVITY_BUTTON_SIZE, ACTIVITY_BUTTON_SIZE), Sense::hover());
    if activity_button(ui, search_rect, "search", "search", "命令搜索", false, 0).clicked() {
        let _ = app.run("tools.search", json!({}));
    }

    // Pin delivery to the bottom without introducing a second command path.
    let bounds = ui.max_rect();
    let deliver = Rect::from_min_size(
        pos2(bounds.center().x - ACTIVITY_BUTTON_SIZE * 0.5, bounds.max.y - ACTIVITY_BUTTON_SIZE),
        vec2(ACTIVITY_BUTTON_SIZE, ACTIVITY_BUTTON_SIZE),
    );
    if deliver.top() >= ui.cursor().top() {
        activity_button(ui, deliver, "deliver", "share", "准备交付", false, 0).clicked().then(|| {
            let _ = app.run("ui.backstage", json!({"value": true, "page": "export"}));
        });
    }
}

pub fn show_panels(app: &mut WordApp, ui: &mut Ui) {
    if !app.project_workspace.enabled || !app.project_workspace.document_bound {
        return;
    }
    let t = crate::theme::Tokens::get(ui.ctx());
    let compact = ui.available_width() < 1440.0;
    let activity = egui::Panel::left("lingdoc-activity-bar")
        .exact_size(ACTIVITY_BAR_WIDTH)
        .resizable(false)
        .frame(egui::Frame::NONE.fill(t.panel).inner_margin(egui::Margin { left: 4, right: 4, top: 8, bottom: 8 }).stroke(Stroke::new(1.0, t.border)))
        .show(ui, |ui| activity_bar(app, ui));
    // `top_rect` remains the established focus/status slot, now owned by the Activity Bar.
    app.project_workspace.top_rect = Some(activity.response.rect);
    app.project_workspace.resource_rect = None;
    app.project_workspace.chat_rect = None;
    app.project_workspace.composer_rect = None;
    app.project_workspace.send_rect = None;
    app.project_workspace.context_menu_rect=None;
    app.project_workspace.context_add_rects.clear();
    app.project_workspace.context_remove_rects.clear();
    app.project_workspace.citation_open_rects.clear();

    let full_width = if compact { 208.0 } else { 224.0 };
    let duration = animation_time(ui.ctx(), motion::BASE);
    let resources_width = full_width
        * ui.ctx().animate_bool_with_time(
            egui::Id::new("lingdoc-project-resources-visibility"),
            app.project_workspace.resources_open,
            duration,
        );
    if resources_width > 0.5 {
        let output = egui::Panel::left("lingdoc-project-resources")
            .exact_size(resources_width)
            .resizable(false)
            .frame(egui::Frame::NONE.fill(t.panel).inner_margin(8).stroke(Stroke::new(1.0, t.border)))
            .show(ui, |ui| { ui.set_max_width(ui.available_width().min(336.0)); app.project_workspace.resources(ui); });
        app.project_workspace.resource_rect = Some(output.response.rect);
    }
    if app.project_workspace.chat_open {
        let output = egui::Panel::right("lingdoc-project-chat")
            .default_size(if compact { 264.0 } else { 320.0 })
            .min_size(232.0)
            .max_size(480.0)
            .resizable(true)
            .frame(egui::Frame::NONE.fill(t.panel).inner_margin(12).stroke(Stroke::new(1.0, t.border)))
            .show(ui, |ui| { ui.set_max_width(ui.available_width().min(456.0)); app.project_workspace.chat(ui); });
        app.project_workspace.chat_rect = Some(output.response.rect);
    }
    let ctx = ui.ctx().clone();
    prepare_frame(app, &ctx);
}

#[cfg(test)]
mod tests {
    #[test]
    fn source_capabilities_survive_hydration_and_default_to_compatible_true() {
        let mut state=super::ProjectWorkspace::default();
        state.hydrate(serde_json::json!({"sources":[
            {"id":"read-only","title":"Scan.pdf","readable":true,"searchable":false},
            {"id":"legacy","title":"Notes.txt"}
        ]})).unwrap();
        assert!(state.sources[0].readable);
        assert!(!state.sources[0].searchable);
        assert!(state.sources[1].readable && state.sources[1].searchable);
        assert_eq!(state.status()["sources"][0]["searchable"],false);
        assert!(state.hydrate(serde_json::json!({"sources":[{"id":"bad","searchable":"yes"}]})).is_err());
        assert_eq!(state.sources[0].id,"read-only");
    }

    #[test]
    fn composer_limit_counts_unicode_characters_not_utf8_bytes() {
        let mut state=super::ProjectWorkspace::default();
        state.draft="问".repeat(4000);
        state.send();
        assert_eq!(state.actions.len(),1);
        let mut overflow=super::ProjectWorkspace::default();
        overflow.draft="问".repeat(4001);
        overflow.send();
        assert!(overflow.actions.is_empty());
        assert!(!overflow.error.is_empty());
    }

    #[test]
    fn resume_metadata_only_appears_when_host_provides_it_and_search_survives_refresh() {
        let mut state = super::ProjectWorkspace::default();
        state.hydrate(serde_json::json!({"projectId":"p1"})).unwrap();
        assert_eq!(state.status()["resumeAvailable"], false);
        state.project_search = "访谈".into();
        state.hydrate(serde_json::json!({"projectId":"p1","resumeAvailable":true,"resumeTabCount":3,"resumeGroupCount":2})).unwrap();
        assert_eq!(state.status()["resumeAvailable"], true);
        assert_eq!(state.status()["resumeTabCount"], 3);
        assert_eq!(state.status()["resumeGroupCount"], 2);
        assert_eq!(state.status()["projectSearch"], "访谈");
        state.hydrate(serde_json::json!({"projectId":"p1"})).unwrap();
        assert_eq!(state.status()["resumeAvailable"], false);
    }

    #[test]
    fn active_source_is_explicit_and_survives_unrelated_refresh() {
        let mut state = super::ProjectWorkspace::default();
        state.hydrate(serde_json::json!({"activeSourceId":"source-1"})).unwrap();
        state.hydrate(serde_json::json!({"projectTitle":"Project"})).unwrap();
        assert_eq!(state.status()["activeSourceId"], "source-1");
        state.set_active_source("");
        assert_eq!(state.status()["activeSourceId"], "");
    }

    #[test]
    fn standalone_snapshot_has_no_auxiliary_focus_and_does_not_route_documents() {
        let mut state = super::ProjectWorkspace::default();
        state.hydrate(serde_json::json!({"projectId":"p1","documentBound":false})).unwrap();
        state.active = true;
        assert!(!state.auxiliary_active());
        assert!(!state.route_standalone("file.new", &serde_json::json!({})));
        assert!(state.take_actions().is_empty());
    }

    #[test]
    fn project_routes_new_open_and_import_without_touching_document() {
        let mut state = super::ProjectWorkspace::default();
        state.hydrate(serde_json::json!({"projectId":"p1","documentBound":true})).unwrap();
        assert!(state.route_standalone("file.new", &serde_json::json!({"template":"blank"})));
        assert!(!state.route_standalone("file.save", &serde_json::json!({})));
        state.request_import();
        state.request_import();
        let actions = state.take_actions();
        assert_eq!(actions.len(), 2);
        assert_eq!(actions[0]["params"]["template"], "blank");
        assert_eq!(actions[1], serde_json::json!({"kind":"importSources","projectId":"p1"}));
        assert!(state.import_busy);
        state.hydrate(serde_json::json!({"projectId":"p1","importError":"cancelled"})).unwrap();
        assert!(!state.import_busy);
        assert_eq!(state.import_error, "cancelled");
    }

    use super::*;

    fn snapshot() -> Value {
        json!({"projectTitle":"Test project","docTitle":"Draft.docx","sources":[{"id":"s1","title":"Source","version":1,"folder":"参考文献"}],
            "sessions":[{"id":"c1","title":"My conversation"}],"currentSessionId":"c1","messages":[],"contextSources":[],"busy":false,"error":""})
    }

    #[test]
    fn refresh_preserves_draft_and_independent_panel_choices() {
        let mut state = ProjectWorkspace::default();
        state.hydrate(snapshot()).unwrap();
        state.draft = "Unsent question".into();
        state.chat_open = true;
        state.resources_open = false;
        state.hydrate(snapshot()).unwrap();
        assert_eq!(state.draft, "Unsent question");
        assert!(state.chat_open && !state.resources_open);
    }

    #[test]
    fn invalid_snapshot_does_not_replace_valid_project() {
        let mut state = ProjectWorkspace::default();
        state.hydrate(snapshot()).unwrap();
        let mut bad = snapshot();
        bad["projectTitle"] = json!("Wrong project");
        bad["messages"] = json!([{"role":"system","text":"bad"}]);
        assert!(state.hydrate(bad).is_err());
        assert_eq!(state.project_title, "Test project");
    }

    #[test]
    fn send_is_single_flight_and_failed_request_preserves_question() {
        let mut state = ProjectWorkspace::default();
        state.hydrate(snapshot()).unwrap();
        state.draft = "My question".into();
        state.send();
        state.send();
        assert_eq!(state.take_actions(), vec![json!({"kind":"send","text":"My question"})]);
        let mut failed = snapshot();
        failed["error"] = json!("Provider unavailable");
        state.hydrate(failed).unwrap();
        assert!(!state.busy);
        assert_eq!(state.draft, "My question");
    }

    #[test]
    fn accepted_answer_does_not_erase_new_typing() {
        let mut state = ProjectWorkspace::default();
        state.hydrate(snapshot()).unwrap();
        state.draft = "First question".into();
        state.send();
        state.draft = "Next question".into();
        let mut done = snapshot();
        done["messages"] = json!([{"id":"m1","role":"user","text":"First question"},{"id":"m2","role":"assistant","text":"Answer"}]);
        state.hydrate(done).unwrap();
        assert!(!state.busy);
        assert_eq!(state.draft, "Next question");
    }

    #[test]
    fn old_identical_question_does_not_acknowledge_new_send() {
        let mut state = ProjectWorkspace::default();
        let mut old = snapshot();
        old["messages"] = json!([{"id":"old","role":"user","text":"Same question"}]);
        state.hydrate(old.clone()).unwrap();
        state.draft = "Same question".into();
        state.send();
        state.hydrate(old).unwrap();
        assert!(state.busy);
        assert_eq!(state.draft, "Same question");
    }

    #[test]
    fn used_sources_are_visible_without_inventing_quotes_or_duplicates() {
        let mut state = ProjectWorkspace::default();
        let mut value = snapshot();
        value["documentTitle"] = json!("Preferred.docx");
        value["contextNote"] = json!("移除资料不删除旧聊天。");
        value["messages"] = json!([{"id":"answer","role":"assistant","text":"Answer",
            "citations":[{"sourceId":"s1","title":"One","version":"v1","quote":"Original sentence"}],
            "usedSources":[{"sourceId":"s1","title":"One","version":"v1"},{"sourceId":"s2","title":"Two","version":"v1"}]}]);
        state.hydrate(value).unwrap();
        assert_eq!(state.doc_title, "Preferred.docx");
        assert_eq!(state.messages[0].citations.len(), 2);
        assert!(state.messages[0].citations[1].quote.is_empty());
        assert_eq!(state.status()["messages"][0]["citations"][1]["sourceId"], "s2");
    }

    #[test]
    fn busy_user_ack_followed_by_failure_preserves_retry_draft() {
        let mut state = ProjectWorkspace::default();
        state.hydrate(snapshot()).unwrap();
        state.draft = "Question to retry".into();
        state.send();
        let mut pending = snapshot();
        pending["busy"] = json!(true);
        pending["messages"] = json!([{"id":"new-user","role":"user","text":"Question to retry"}]);
        state.hydrate(pending.clone()).unwrap();
        assert!(state.busy);
        assert_eq!(state.draft, "Question to retry");
        pending["busy"] = json!(false);
        pending["error"] = json!("Provider failed");
        state.hydrate(pending).unwrap();
        assert!(!state.busy);
        assert_eq!(state.draft, "Question to retry");
    }

    #[test]
    fn session_switch_restores_separate_drafts_with_bounded_cache() {
        let mut state = ProjectWorkspace::default();
        state.hydrate(snapshot()).unwrap();
        state.draft = "Conversation one draft".into();
        let mut second = snapshot();
        second["currentSessionId"] = json!("c2");
        state.hydrate(second.clone()).unwrap();
        assert!(state.draft.is_empty());
        state.draft = "Conversation two draft".into();
        state.hydrate(snapshot()).unwrap();
        assert_eq!(state.draft, "Conversation one draft");
        state.hydrate(second).unwrap();
        assert_eq!(state.draft, "Conversation two draft");
        for n in 0..120 {
            let mut next = snapshot();
            next["currentSessionId"] = json!(format!("other-{n}"));
            state.hydrate(next).unwrap();
        }
        assert!(state.session_drafts.len() <= 100);
    }

    #[test]
    fn quick_actions_require_context_and_send_preset_through_existing_action() {
        use egui_kittest::kittest::{NodeT, Queryable};
        let mut app=WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()),crate::Services::default());
        app.project_workspace.hydrate(snapshot()).unwrap();
        app.project_workspace.chat_open=true;
        app.project_workspace.resources_open=false;
        let mut h=egui_kittest::Harness::builder().with_size(vec2(1300.0,900.0)).build_ui_state(
            |ui,app:&mut WordApp|{let ctx=ui.ctx().clone();app.logic(&ctx);app.ui(ui);},app,
        );
        h.run_steps(6);
        for label in ["总结这份资料","提炼可引用观点","核对这段出处"] {
            assert!(h.get_by_label(label).accesskit_node().is_disabled());
        }
        assert!(h.state().project_workspace.actions.is_empty());
        h.state_mut().project_workspace.context_sources.push(Source {
            id:"s1".into(),title:"Source".into(),version:"1".into(),readable:true,searchable:true,..Source::default()
        });
        h.run_steps(2);
        assert!(!h.get_by_label("总结这份资料").accesskit_node().is_disabled());
        h.get_by_label("总结这份资料").click();
        h.run_steps(2);
        assert_eq!(h.state_mut().project_workspace.take_actions(),vec![json!({"kind":"send","text":"总结这份资料"})]);
    }

    #[test]
    fn long_context_title_keeps_remove_visible_and_clickable() {
        use egui_kittest::kittest::Queryable;
        let mut app=WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()),crate::Services::default());
        let mut value=snapshot();
        value["contextSources"]=json!([{"id":"s1","version":"1","title":"A very long study material filename ".repeat(10)}]);
        app.project_workspace.hydrate(value).unwrap();
        app.project_workspace.chat_open=true;
        app.project_workspace.resources_open=false;
        let mut h=egui_kittest::Harness::builder().with_size(vec2(900.0,700.0)).build_ui_state(
            |ui,app:&mut WordApp|{let ctx=ui.ctx().clone();app.logic(&ctx);app.ui(ui);},app,
        );
        h.run_steps(6);
        let state=&h.state().project_workspace;
        let remove=state.context_remove_rects["s1"];
        let menu=state.context_menu_rect.unwrap();
        let chat=state.chat_rect.unwrap();
        assert!(remove.bottom()<=menu.bottom()+45.0,"long title must not push removal below its single visible row");
        assert!(remove.left()>=chat.left()&&remove.right()<=chat.right());
        assert!(remove.height()<=30.0);
        h.get_by_label("移除").click();
        h.run_steps(2);
        let actions=h.state_mut().project_workspace.take_actions();
        assert_eq!(actions,vec![json!({"kind":"removeContext","sourceId":"s1"})]);
    }

    #[test]
    fn citation_context_add_respects_four_source_limit() {
        use egui_kittest::kittest::{NodeT, Queryable};
        let mut app=WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()),crate::Services::default());
        let mut value=snapshot();
        value["contextSources"]=json!((1..=4).map(|i|json!({"id":format!("s{i}"),"title":format!("Source {i}"),"version":"1"})).collect::<Vec<_>>());
        value["messages"]=json!([{"id":"answer","role":"assistant","text":"A sourced answer","citations":[{"sourceId":"s5","title":"Fifth source","version":"1","quote":"Original sentence"}]}]);
        app.project_workspace.hydrate(value).unwrap();
        app.project_workspace.chat_open=true;
        app.project_workspace.resources_open=false;
        let mut h=egui_kittest::Harness::builder().with_size(vec2(1300.0,1000.0)).build_ui_state(
            |ui,app:&mut WordApp|{let ctx=ui.ctx().clone();app.logic(&ctx);app.ui(ui);},app,
        );
        h.run_steps(6);
        assert!(h.get_by_label("加入会话").accesskit_node().is_disabled());
        assert!(h.state().project_workspace.actions.is_empty());
        h.state_mut().project_workspace.context_sources.pop();
        h.run_steps(2);
        assert!(!h.get_by_label("加入会话").accesskit_node().is_disabled());
        h.get_by_label("加入会话").click();
        h.run_steps(2);
        assert_eq!(h.state_mut().project_workspace.take_actions(),vec![json!({"kind":"addContext","sourceId":"s5","version":"1"})]);
    }

    #[test]
    fn clicking_chat_answer_does_not_lock_history_wheel_scroll() {
        use egui_kittest::kittest::Queryable;
        let mut app=WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()),crate::Services::default());
        let mut value=snapshot();
        value["messages"]=json!([
            {"id":"first","role":"assistant","text":"Older answer line.\n".repeat(40),"citations":[{"sourceId":"s1","title":"Source","version":"1","quote":"First original"}]},
            {"id":"middle","role":"assistant","text":"Middle answer line.\n".repeat(25),"citations":[{"sourceId":"s1","title":"Source","version":"1","quote":"Middle original"}]},
            {"id":"last","role":"assistant","text":"Last response","citations":[{"sourceId":"s1","title":"Source","version":"1","quote":"Last original"}]}
        ]);
        app.project_workspace.hydrate(value).unwrap();
        app.project_workspace.chat_open=true;
        app.project_workspace.resources_open=false;
        let mut h=egui_kittest::Harness::builder().with_size(vec2(1600.0,1000.0)).build_ui_state(
            |ui,app:&mut WordApp|{let ctx=ui.ctx().clone();app.logic(&ctx);app.ui(ui);},app,
        );
        h.run_steps(6);
        let citation_key=json!(["last","s1","1",null]).to_string();
        let before=h.state().project_workspace.citation_open_rects[&citation_key].top();
        h.get_by_label("Last response").click();
        h.run_steps(3);
        let state=&h.state().project_workspace;
        // 指针落在消息区内的真实引用卡上，避免 composer 新增 chips 后滚轮命中布局变化。
        let at=state.citation_open_rects[&citation_key].center();
        h.event(egui::Event::PointerMoved(at));
        h.run_steps(2);
        h.event(egui::Event::MouseWheel{unit:egui::MouseWheelUnit::Point,delta:vec2(0.0,200.0),phase:egui::TouchPhase::Move,modifiers:egui::Modifiers::NONE});
        h.run_steps(5);
        let after=h.state().project_workspace.citation_open_rects[&citation_key].top();
        assert!(after>before+100.0,"clicking a chat answer must not prevent scrolling upwards: {before} -> {after}");
        assert!(h.state().project_workspace.actions.is_empty());
    }

    fn start_page_harness(value: Value, selected: bool) -> egui_kittest::Harness<'static, WordApp> {
        let mut app=WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()),crate::Services::default());
        app.project_workspace.hydrate(value).unwrap();
        app.project_workspace.project_selected=selected;
        app.ui.backstage=true;
        app.ui.backstage_page="projects".into();
        let mut h=egui_kittest::Harness::builder().with_size(vec2(900.0,700.0)).build_ui_state(
            |ui,app:&mut WordApp|{let ctx=ui.ctx().clone();app.logic(&ctx);app.ui(ui);},app,
        );
        h.run_steps(6);
        h
    }

    #[test]
    fn start_page_failed_open_is_visible_without_hiding_controls() {
        use egui_kittest::kittest::Queryable;
        let mut value=snapshot();
        value["projectId"]=json!("p1");
        value["error"]=json!("The material could not be opened. ".repeat(90));
        let h=start_page_harness(value,true);
        assert!(h.query_by_label("上次操作未完成").is_some());
        let state=&h.state().project_workspace;
        assert!(state.project_search_rect.unwrap().bottom()<600.0);
        assert!(state.import_button_rect.unwrap().bottom()<700.0);
        assert!(state.actions.is_empty());
    }

    #[test]
    fn start_page_clear_search_returns_focus_and_restores_sources() {
        use egui_kittest::kittest::Queryable;
        let mut value=snapshot();value["projectId"] = json!("p1");
        let mut h=start_page_harness(value,true);
        h.state_mut().project_workspace.project_search="absent".into();h.run_steps(2);
        assert!(h.state().project_workspace.source_row_rects.is_empty());
        h.get_by_label("清空").click();h.run_steps(2);
        assert!(h.state().project_workspace.project_search.is_empty());
        assert!(h.state().project_workspace.source_row_rects.contains_key("s1"));
        h.event(egui::Event::Text("Reference".into()));h.run_steps(2);
        assert_eq!(h.state().project_workspace.project_search,"Reference");
        assert!(h.state().project_workspace.actions.is_empty());
    }

    #[test]
    fn start_page_disconnected_snapshot_drops_previous_controls() {
        use egui_kittest::kittest::Queryable;
        let mut value=snapshot();value["projectId"] = json!("p1");
        let mut h=start_page_harness(value,true);
        assert!(h.state().project_workspace.project_document_rect.is_some());
        h.state_mut().project_workspace.hydrate(json!({"projectId":"","documentBound":false,"error":"Project unavailable"})).unwrap();
        h.run_steps(2);
        assert!(h.query_by_label("Project unavailable").is_some());
        let state=&h.state().project_workspace;
        assert!(state.project_card_rect.is_none()&&state.project_document_rect.is_none());
        assert!(state.project_search_rect.is_none()&&state.import_button_rect.is_none());
        assert!(state.source_row_rects.is_empty());
        assert!(state.actions.is_empty());
    }

    #[test]
    fn start_page_read_only_source_is_single_row_and_unreadable_source_is_disabled() {
        use egui_kittest::kittest::{NodeT,Queryable};
        let value=json!({"projectId":"p1","projectTitle":"Test","docTitle":"Draft.docx","sources":[
            {"id":"scan","title":"Scan.pdf","version":"2","folder":"参考文献","readable":true,"searchable":false},
            {"id":"missing","title":"Missing.pdf","version":"1","readable":false,"searchable":false}
        ],"imports":[{"id":"import-scan","sourceId":"scan","name":"Scan.pdf","status":"readable_only"}]});
        let mut h=start_page_harness(value,true);
        assert!(h.query_by_label("Scan.pdf · 仅阅读").is_none(),"a retained read-only material must not also appear as a duplicate import row");
        assert!(h.get_by_label("Missing.pdf").accesskit_node().is_disabled());
        assert!(h.query_by_label("参考文献 · 版本 2 · 仅阅读 · 不参与问答").is_some());
        h.get_by_label("Scan.pdf").click();h.run_steps(2);
        assert_eq!(h.state_mut().project_workspace.take_actions(),vec![json!({"kind":"openSource","sourceId":"scan","version":"2"})]);
    }

}
