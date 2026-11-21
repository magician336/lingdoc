//! Native project resources and source Q&A. The host owns persistence and model calls;
//! this module only renders snapshots and emits explicit user actions.

use egui::{Align2, Color32, Rect, RichText, Sense, Stroke, Ui, pos2, vec2};
use serde_json::{Value, json};
use std::collections::BTreeMap;

use crate::WordApp;
use crate::theme::{Tokens, animation_time, fit_text, medium, motion, radius, semibold, space};

const ACTIVITY_BAR_WIDTH: f32 = 48.0;
const ACTIVITY_BUTTON_SIZE: f32 = 40.0;

/// Approved side-panel width rule (task §8): the resources and Q&A panels share one draggable
/// range with one default. A drag released below the minimum collapses the panel back to the
/// Activity Bar instead of sticking at the narrowest width.
const PANEL_WIDTH_DEFAULT: f32 = 224.0;
const PANEL_WIDTH_MIN: f32 = 200.0;
const PANEL_WIDTH_MAX: f32 = 520.0;
/// A live drag may shrink below the minimum so the collapse gesture is visible before release.
const PANEL_DRAG_FLOOR: f32 = 96.0;
const PANEL_DRAG_GRIP: f32 = 6.0;

/// Resolve the persisted panel width: missing (0), non-finite or junk reads as the default;
/// a remembered width is clamped into the approved range.
fn saved_panel_width(saved: f32) -> f32 {
    if saved.is_finite() && saved > 0.0 { saved.clamp(PANEL_WIDTH_MIN, PANEL_WIDTH_MAX) } else { PANEL_WIDTH_DEFAULT }
}

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
fn file_row(ui: &mut Ui, title: &str, selected: bool, active: bool) -> egui::Response {
    file_row_details(ui, title, selected, active, None, false)
}

/// Source rows add real snapshot metadata without changing the established row height or
/// selection semantics. All sources are read-only; search capability remains in the tooltip.
/// `active` marks the row the reader is currently showing: it gets the same accent rail as a
/// selected row, so the panel always names the visible source.
fn file_row_details(ui: &mut Ui, title: &str, selected: bool, active: bool, version: Option<&str>, read_only: bool) -> egui::Response {
    let t = Tokens::get(ui.ctx());
    let (rect, response) = ui.allocate_exact_size(vec2(ui.available_width(), 32.0), Sense::click());
    response.widget_info(|| egui::WidgetInfo::selected(egui::WidgetType::SelectableLabel, ui.is_enabled(), selected, title));
    let rail = Rect::from_min_size(rect.left_top() + vec2(0.0, 6.0), vec2(3.0, 20.0));
    if selected {
        ui.painter().rect_filled(rect, radius::BASE, t.checked);
        ui.painter().rect_filled(rail, radius::SMALL, t.accent);
    } else if active {
        ui.painter().rect_filled(rect, radius::BASE, t.hover);
        ui.painter().rect_filled(rail, radius::SMALL, t.accent);
    } else if response.hovered() {
        ui.painter().rect_filled(rect, radius::BASE, t.hover);
    }
    paint_file_icon(ui, rect, title, t.text_dim);

    let version = version
        .filter(|value| !value.trim().is_empty())
        .map(|value| if value.chars().all(|ch| ch.is_ascii_digit()) { format!("v{value}") } else { value.to_owned() });
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
        ui.painter().rect_filled(Rect::from_min_size(rect.left_top() + vec2(-4.0, 9.0), vec2(3.0, 22.0)), radius::SMALL, t.accent);
    }
    if response.has_focus() {
        ui.painter().rect_stroke(rect.shrink(1.0), radius::PANEL, Stroke::new(2.0, t.focus_ring), egui::StrokeKind::Inside);
    }
    let color = if selected {
        t.accent
    } else if response.hovered() {
        t.icon
    } else {
        t.text_dim
    };
    crate::icons::paint(ui.painter(), Rect::from_center_size(rect.center(), vec2(20.0, 20.0)), icon, color, t.accent);
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
    /// Actual request inputs, rendered as plain provenance text rather than invented citations.
    used_sources: Vec<Source>,
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
    explorer: Option<Value>,
    project_listing: Vec<Value>,
    explorer_selected: String,
    explorer_rects: BTreeMap<String, Rect>,
    explorer_menu_rects: BTreeMap<String, Rect>,
    sessions: Vec<Session>,
    current_session: String,
    messages: Vec<Message>,
    context_sources: Vec<Source>,
    context_note: String,
    busy: bool,
    uncertain: bool,
    session_action_pending: bool,
    error: String,
    draft: String,
    session_drafts: BTreeMap<(String, String), String>,
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
    refresh_state_rect: Option<Rect>,
    new_session_rect: Option<Rect>,
    session_menu_rect: Option<Rect>,
    session_option_rects: BTreeMap<String, Rect>,
    chat_toggle_rect: Option<Rect>,
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
            explorer: None,
            project_listing: Vec::new(),
            explorer_selected: String::new(),
            explorer_rects: BTreeMap::new(),
            explorer_menu_rects: BTreeMap::new(),
            sessions: Vec::new(),
            current_session: String::new(),
            messages: Vec::new(),
            context_sources: Vec::new(),
            context_note: String::new(),
            busy: false,
            uncertain: false,
            session_action_pending: false,
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
            refresh_state_rect: None,
            new_session_rect: None,
            session_menu_rect: None,
            session_option_rects: BTreeMap::new(),
            chat_toggle_rect: None,
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
    let capability = |field: &str| match value.get(field) {
        None => Ok(true),
        Some(Value::Bool(flag)) => Ok(*flag),
        _ => Err(format!("{field} must be boolean")),
    };
    Ok(Source {
        id,
        title: string(value, "title", 1024)?,
        version: string(value, "version", 512)?,
        folder: string(value, "folder", 256)?,
        readable: capability("readable")?,
        searchable: capability("searchable")?,
    })
}

impl ProjectWorkspace {
    /// Open the project Q&A panel and prefill the composer draft with the frozen selection and
    /// its one-shot AI result so the user can ask multi-turn follow-up questions without
    /// auto-sending or modifying the document.
    pub fn handoff_selection_result(&mut self, action_label: &str, selection: &str, result_text: &str) {
        self.chat_open = true;
        self.active = true;
        let mut prefill = format!(
            "关于这段选文（{action_label}结果）：\n> {}\n\nAI 结果：\n{}\n\n请帮我进一步：",
            selection.trim(),
            result_text.trim()
        );
        if !self.draft.trim().is_empty() {
            prefill.push('\n');
            prefill.push_str(self.draft.trim());
        }
        self.draft = prefill;
    }

    /// Open the project Q&A panel and prefill the composer draft with a source excerpt and its
    /// title so the user can ask follow-up questions while reading side-by-side. Never auto-sends.
    pub fn handoff_source_excerpt(&mut self, source_title: &str, excerpt: &str) {
        self.chat_open = true;
        self.active = true;
        let excerpt = excerpt.trim();
        let mut prefill = if excerpt.is_empty() {
            format!("关于资料《{source_title}》，请帮我：")
        } else {
            format!("关于资料《{source_title}》中的这段内容：\n> {excerpt}\n\n请帮我：")
        };
        if !self.draft.trim().is_empty() {
            prefill.push('\n');
            prefill.push_str(self.draft.trim());
        }
        self.draft = prefill;
    }

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
                let citations = array(m, "citations", 64)?
                    .iter()
                    .map(|c| {
                        Ok(Citation {
                            source: source(c, true)?,
                            quote: string(c, "quote", 16384)?,
                            page: c.get("page").and_then(Value::as_u64).filter(|p| *p > 0),
                        })
                    })
                    .collect::<Result<Vec<_>, String>>()?;
                let used_sources = array(m, "usedSources", 64)?.iter().map(|used| source(used, false)).collect::<Result<Vec<_>, String>>()?;
                Ok(Message { id: string(m, "id", 512)?, role, text: string(m, "text", 65536)?, citations, used_sources })
            })
            .collect::<Result<Vec<_>, String>>()?;
        // Browser persistence is scoped by both identities. Only accept restored drafts
        // at an identity boundary, so background refreshes cannot overwrite new typing.
        let restored_draft = value.get("restoredDraft").map(|_| string(&value, "restoredDraft", 16384)).transpose()?;
        let host_busy = value.get("busy").and_then(Value::as_bool).unwrap_or(false);
        let interaction_pending = value.get("interactionPending").and_then(Value::as_bool);
        let uncertain = value.get("uncertain").and_then(Value::as_bool).unwrap_or(false);
        let identity_changed = project_id != self.project_id || current_session != self.current_session;
        if self.enabled && identity_changed {
            let previous = (self.project_id.clone(), self.current_session.clone());
            if self.draft.is_empty() {
                self.session_drafts.remove(&previous);
            } else {
                self.session_drafts.insert(previous, self.draft.clone());
            }
            self.draft = self.session_drafts.get(&(project_id.clone(), current_session.clone())).cloned().unwrap_or_default();
            self.pending_text = None;
            self.pending_existing_ids.clear();
        }
        if !self.enabled || identity_changed {
            if let Some(draft) = restored_draft {
                self.draft = draft;
            }
        }
        if identity_changed || interaction_pending == Some(false) || !error.is_empty() {
            self.session_action_pending = false;
        }
        if let Some(pending) = &self.pending_text {
            let accepted = messages.iter().any(|m| m.role == "user" && m.text == *pending && !self.pending_existing_ids.contains(&m.id));
            if !host_busy && interaction_pending != Some(true) && (accepted || !error.is_empty() || uncertain) {
                if accepted && error.is_empty() && !uncertain && self.draft.trim() == pending.as_str() {
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
        if let Some(id) = active_source_id {
            self.active_source_id = id;
        }
        self.import_error = import_error;
        self.imports = imports;
        self.import_busy = value.get("importBusy").and_then(Value::as_bool).unwrap_or(false);
        self.doc_title = doc_title;
        self.current_session = current_session;
        self.sources = sources;
        if let Some(projects) = value.get("projects").and_then(Value::as_array) {
            self.project_listing = projects.clone();
        }
        self.explorer = value.get("explorer").filter(|v| v.is_object()).cloned();
        if identity_changed {
            self.explorer_selected.clear();
        }
        self.context_sources = context_sources;
        self.context_note = context_note;
        self.sessions = sessions;
        self.messages = messages;
        self.error = error;
        self.uncertain = uncertain;
        self.busy = host_busy || interaction_pending == Some(true) || self.pending_text.is_some() || self.session_action_pending;
        Ok(())
    }

    pub fn is_enabled(&self) -> bool {
        self.enabled
    }

    pub fn document_title(&self) -> Option<&str> {
        (self.enabled && self.document_bound && !self.doc_title.is_empty()).then_some(self.doc_title.as_str())
    }

    pub fn set_active_source(&mut self, id: &str) {
        self.active_source_id = id.to_owned();
    }

    pub fn route_standalone(&mut self, command: &str, params: &Value) -> bool {
        if !self.enabled || !self.document_bound || !matches!(command, "file.new" | "file.open") {
            return false;
        }
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

    /// The catalog the rewrite configuration surface picks from: every hydrated project source
    /// with whether it can be sent to a model. Identities only; full texts stay with the host.
    pub fn source_catalog(&self) -> Vec<(crate::ai::AiSourceRef, bool)> {
        self.sources
            .iter()
            .map(|source| {
                (crate::ai::AiSourceRef { id: source.id.clone(), title: source.title.clone(), version: source.version.clone() }, source.searchable)
            })
            .collect()
    }

    fn source_summary(&self) -> String {
        let mut groups = BTreeMap::new();
        for source in &self.sources {
            *groups.entry(source.folder.clone()).or_insert(0usize) += 1;
        }
        groups.iter().map(|(folder, count)| format!("{folder} {count} 份")).collect::<Vec<_>>().join(" · ")
    }

    pub fn status(&self) -> Value {
        let rect = |r: Option<Rect>| r.map(|r| json!({"x":r.left(),"y":r.top(),"width":r.width(),"height":r.height()}));
        let mut status = json!({"enabled":self.enabled,"documentBound":self.document_bound,"resourcesOpen":self.resources_open,"chatOpen":self.chat_open,
            "auxiliaryActive":self.auxiliary_active(),"busy":self.busy,"uncertain":self.uncertain,"draft":self.draft,"error":self.error,
            "currentSessionId":self.current_session,"sourceCount":self.sources.len(),"messageCount":self.messages.len(),
            "projectTitle":self.project_title,"documentTitle":self.doc_title,"projectSummary":self.source_summary(),
            "activeSourceId":self.active_source_id,"sourceRowRects":self.source_row_rects.iter().map(|(id,r)|(id.clone(),rect(Some(*r)))).collect::<BTreeMap<_,_>>(),"projectId":self.project_id,"importBusy":self.import_busy,"importError":self.import_error,"imports":self.imports,
            "projectSelected":self.project_selected,"projectCardRect":rect(self.project_card_rect),"projectNavRect":rect(self.project_nav_rect),"projectDocumentRect":rect(self.project_document_rect),"importButtonRect":rect(self.import_button_rect),
            "sources":self.sources.iter().map(|s|json!({"id":s.id,"title":s.title,"version":s.version,"folder":s.folder,"readable":s.readable,"searchable":s.searchable})).collect::<Vec<_>>(),
            "sessions":self.sessions.iter().map(|s|json!({"id":s.id,"title":s.title})).collect::<Vec<_>>(),
            "messages":self.messages.iter().map(|m|json!({"id":m.id,"role":m.role,"text":m.text,
                "citations":m.citations.iter().map(|c|json!({"sourceId":c.source.id,"title":c.source.title,"version":c.source.version,"quote":c.quote,"page":c.page})).collect::<Vec<_>>(),
                "usedSources":m.used_sources.iter().map(|s|json!({"id":s.id,"title":s.title,"version":s.version})).collect::<Vec<_>>()
            })).collect::<Vec<_>>(),
            "contextSources":self.context_sources.iter().map(|s|json!({"id":s.id,"title":s.title,"version":s.version})).collect::<Vec<_>>(),
            "contextNote":self.context_note,"pendingActions":self.actions.len(),"resourceRect":rect(self.resource_rect),"chatRect":rect(self.chat_rect),"topRect":rect(self.top_rect),
            "composerRect":rect(self.composer_rect),"sendRect":rect(self.send_rect),
            "newSessionRect":rect(self.new_session_rect),"sessionMenuRect":rect(self.session_menu_rect)});
        status["explorerRects"] = json!(self.explorer_rects.iter().map(|(id, r)| (id.clone(), rect(Some(*r)))).collect::<BTreeMap<_, _>>());
        status["explorerMenuRects"] = json!(self.explorer_menu_rects.iter().map(|(id, r)| (id.clone(), rect(Some(*r)))).collect::<BTreeMap<_, _>>());
        status["sessionOptionRects"] =
            json!(self.session_option_rects.iter().map(|(id, r)| (id.clone(), rect(Some(*r)))).collect::<BTreeMap<_, _>>());
        status["chatToggleRect"] = json!(rect(self.chat_toggle_rect));
        status["refreshStateRect"] = json!(rect(self.refresh_state_rect));
        status["citationOpenRects"] = json!(self.citation_open_rects.iter().map(|(id, r)| (id.clone(), rect(Some(*r)))).collect::<BTreeMap<_, _>>());
        let extra = json!({"resumeAvailable":self.resume_available,"resumeTabCount":self.resume_tab_count,
            "resumeGroupCount":self.resume_group_count,"resumeRect":rect(self.resume_rect),
            "projectSearch":self.project_search,"projectSearchRect":rect(self.project_search_rect),
            "projectSearchClearRect":rect(self.project_search_clear_rect),"projectBackRect":rect(self.project_back_rect)});
        if let (Some(target), Some(fields)) = (status.as_object_mut(), extra.as_object()) {
            target.extend(fields.clone());
        }
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

    fn request_session(&mut self, target: Option<&str>) {
        if self.busy || self.session_action_pending || target == Some(self.current_session.as_str()) {
            return;
        }
        let action = match target {
            Some(id) => json!({"kind":"selectSession","sessionId":id}),
            None => json!({"kind":"newSession"}),
        };
        if self.enqueue(action) {
            self.session_action_pending = true;
            self.busy = true;
        }
    }

    fn send(&mut self) {
        let text = self.draft.trim().to_string();
        if self.busy || self.uncertain || text.is_empty() {
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
        if !self.enabled || self.import_busy {
            return;
        }
        if self.enqueue(json!({"kind":"importSources","projectId":self.project_id})) {
            self.import_busy = true;
            self.import_error.clear();
        }
    }

    fn import_button(&mut self, ui: &mut Ui) {
        let label = if self.import_busy { "正在导入…" } else { "＋ 导入资料" };
        let response = ui.add_enabled(!self.import_busy, egui::Button::new(RichText::new(label).font(medium(14.0))).min_size(vec2(104.0, 30.0)));
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
        let (rect, response) = ui.allocate_exact_size(vec2(ui.available_width(), 34.0), if enabled { Sense::click() } else { Sense::hover() });
        let label = if self.import_busy { "正在导入…" } else { "＋ 导入资料" };
        response.widget_info(|| egui::WidgetInfo::labeled(egui::WidgetType::Button, enabled, label));
        if response.hovered() && enabled {
            ui.painter().rect_filled(rect, radius::CARD, t.hover);
        }
        let border = if response.hovered() && enabled { t.accent } else { t.border_strong };
        let dash = |from: egui::Pos2, to: egui::Pos2| {
            let delta = to - from;
            let length = delta.length();
            if length <= 0.0 {
                return;
            }
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
        let ink = if !enabled {
            t.text_disabled
        } else if response.hovered() {
            t.accent_text
        } else {
            t.text
        };
        ui.painter().text(rect.center(), Align2::CENTER_CENTER, label, medium(12.0), ink);
        self.import_button_rect = Some(response.rect);
        if response.on_hover_text(tl!("Import a TXT / Markdown / DOCX copy into the project")).clicked() && enabled {
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
        // The native host routes these actions to the established directory dialogs. Keep the
        // entries on the Project page itself: an empty snapshot must not leave a dead-end page.
        #[cfg(not(target_arch = "wasm32"))]
        if !self.project_selected {
            ui.horizontal(|ui| {
                if ui
                    .button(RichText::new("新建项目…").font(medium(14.0)))
                    .on_hover_text("选择一个空目录；应用会创建主文稿.docx，不会覆盖已有文档。")
                    .clicked()
                {
                    self.enqueue(json!({"kind":"newProject"}));
                }
                if ui.button(RichText::new("打开项目目录…").font(medium(14.0))).clicked() {
                    self.enqueue(json!({"kind":"openProject"}));
                }
            });
            ui.add_space(20.0);
        }
        // A hydrated project list is useful even when no project is currently connected.
        if !self.project_selected && !self.project_listing.is_empty() {
            ui.heading("全部项目");
            ui.label("新建项目不会覆盖已有项目。选择一个项目继续工作。");
            ui.add_space(12.0);
            let projects = self.project_listing.clone();
            egui::ScrollArea::vertical().id_salt("all-projects").show(ui, |ui| {
                for project in &projects {
                    let id = project["id"].as_str().unwrap_or("");
                    let name = project["name"].as_str().unwrap_or("未命名项目");
                    let status = if project["status"].as_str() == Some("draft") { "草稿" } else { "已立项" };
                    let response = ui.group(|ui| {
                        ui.set_min_width((ui.available_width().min(620.0) - 24.0).max(60.0));
                        ui.label(RichText::new(name).font(semibold(19.0)));
                        ui.label(format!(
                            "{} · {}{}",
                            status,
                            project["documentTitle"].as_str().unwrap_or("主文稿"),
                            if id == self.project_id { " · 当前项目" } else { "" }
                        ));
                        let button = ui.add_enabled(project["openable"].as_bool().unwrap_or(false), egui::Button::new("打开项目"));
                        self.explorer_rects.insert(format!("projectOpen:{id}"), button.rect);
                        if button.clicked() {
                            self.enqueue(json!({"kind":"switchProject","projectId":id}));
                        }
                    });
                    if self.project_card_rect.is_none() {
                        self.project_card_rect = Some(response.response.rect);
                    }
                    ui.add_space(12.0);
                }
            });
            return;
        }
        if !self.enabled || self.project_id.trim().is_empty() {
            ui.label(RichText::new("还没有打开项目").font(semibold(17.0)));
            ui.add_space(8.0);
            if !self.error.is_empty() {
                egui::ScrollArea::vertical().id_salt("project-connection-error").max_height(80.0).show(ui, |ui| {
                    ui.add(egui::Label::new(RichText::new(&self.error).font(medium(14.0))).wrap());
                });
            }
            ui.add(egui::Label::new("可新建一个项目，或打开包含唯一主 DOCX 的项目目录。").wrap());
            return;
        }
        let project_title = if self.project_title.trim().is_empty() { "未命名项目".to_string() } else { self.project_title.clone() };
        if !self.project_selected {
            let t = Tokens::get(ui.ctx());
            let summary = self.source_summary();
            let (card, response) = ui.allocate_exact_size(vec2(ui.available_width().min(640.0), 172.0), Sense::click());
            response.widget_info(|| egui::WidgetInfo::labeled(egui::WidgetType::Button, true, format!("{project_title}，进入工作区")));
            ui.painter().rect_filled(card, 8.0, if response.hovered() { t.hover } else { t.input });
            let border_color = if response.has_focus() || response.hovered() { t.accent } else { t.border_strong };
            ui.painter().rect_stroke(card, 8.0, Stroke::new(if response.has_focus() { 2.0 } else { 1.0 }, border_color), egui::StrokeKind::Inside);
            ui.painter().rect_filled(Rect::from_min_size(card.left_top() + vec2(0.0, 18.0), vec2(3.0, 36.0)), radius::SMALL, t.accent);
            let mut job = egui::text::LayoutJob::single_section(project_title.to_owned(), egui::text::TextFormat::simple(semibold(20.0), t.text));
            job.wrap = egui::text::TextWrapping {
                max_width: (card.width() - 40.0).max(1.0),
                max_rows: 1,
                break_anywhere: true,
                overflow_character: Some('…'),
            };
            let galley = ui.painter().layout_job(job);
            ui.painter().galley(card.left_top() + vec2(20.0, 16.0), galley, t.text);
            ui.painter().text(
                card.left_top() + vec2(20.0, 54.0),
                egui::Align2::LEFT_TOP,
                format!("主文稿：{}", self.doc_title),
                medium(14.0),
                t.text,
            );
            ui.painter().text(
                card.left_top() + vec2(20.0, 82.0),
                egui::Align2::LEFT_TOP,
                format!("资料 {} 份 · {summary}", self.sources.len()),
                medium(13.0),
                t.text_dim,
            );
            let primary = ui.put(
                Rect::from_min_size(card.left_top() + vec2(20.0, 124.0), vec2(144.0, 32.0)),
                egui::Button::new(RichText::new("进入工作区").color(t.on_accent).font(medium(14.0)))
                    .fill(t.accent)
                    .corner_radius(egui::CornerRadius::same(6)),
            );
            let secondary = ui.put(
                Rect::from_min_size(card.left_top() + vec2(172.0, 124.0), vec2(144.0, 32.0)),
                egui::Button::new(RichText::new("查看全部资料").font(medium(14.0))).corner_radius(egui::CornerRadius::same(6)),
            );
            if primary.clicked()
                || (response.clicked() && !secondary.clicked())
                || (response.has_focus() && ui.input(|input| input.key_pressed(egui::Key::Enter)))
            {
                self.enqueue(json!({"kind":"enterProject","projectId":self.project_id}));
            }
            if secondary.clicked() {
                self.enqueue(json!({"kind":"enterProject","projectId":self.project_id,"focus":"sources"}));
            }
            let response = response.on_hover_text(format!("{}\n{}\n进入工作区，或查看全部资料", project_title, self.doc_title));
            self.project_card_rect = Some(response.rect);
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
            let response = ui
                .add(
                    egui::Button::new(RichText::new("继续上次工作").font(semibold(15.0)).color(t.on_accent))
                        .fill(t.accent)
                        .min_size(vec2(176.0, 40.0)),
                )
                .on_hover_text("打开已保存的项目文稿，并恢复本浏览器的资料标签和阅读分组；不恢复未保存的正文。");
            self.resume_rect = Some(response.rect);
            if response.clicked() {
                self.enqueue(json!({"kind":"enterProject","projectId":self.project_id,"resume":true}));
            }
            ui.add_space(4.0);
            ui.add(
                egui::Label::new(
                    RichText::new(format!("恢复 {} 份资料 · {} 个阅读组", self.resume_tab_count, self.resume_group_count))
                        .font(medium(14.0))
                        .color(Tokens::get(ui.ctx()).text_dim),
                )
                .wrap(),
            );
            ui.add_space(16.0);
        }
        ui.horizontal(|ui| {
            let clear_width = 46.0 + ui.spacing().item_spacing.x;
            let search = ui.add_sized(
                vec2((ui.available_width() - clear_width).max(40.0), 34.0),
                egui::TextEdit::singleline(&mut self.project_search)
                    .font(medium(14.0))
                    .char_limit(256)
                    .hint_text("查找文稿、资料或分类")
                    .margin(vec2(10.0, 8.0)),
            );
            self.project_search_rect = Some(search.rect);
            let clear = ui.add_enabled(!self.project_search.is_empty(), egui::Button::new("清空").min_size(vec2(46.0, 34.0)));
            self.project_search_clear_rect = Some(clear.rect);
            if clear.clicked() {
                self.project_search.clear();
                search.request_focus();
            }
        });
        let query = self.project_search.trim().to_lowercase();
        ui.add_space(20.0);
        section_label(ui, "文稿", usize::from(!self.doc_title.is_empty()));
        ui.add_space(6.0);
        let title = if self.doc_title.is_empty() { "打开项目文稿" } else { &self.doc_title };
        if self.doc_title.is_empty() {
            ui.label(RichText::new("项目中尚无文稿").font(medium(14.0)));
        } else if query.is_empty() || title.to_lowercase().contains(&query) {
            let response = file_row(ui, title, false, false)
                .on_hover_text(crate::i18n::fmt(tl!("Open the project document in WordCraft\n{title}"), &[("title", title)]));
            self.project_document_rect = Some(response.rect);
            if response.clicked() {
                self.enqueue(json!({"kind":"enterProject","projectId":self.project_id}));
            }
        } else {
            ui.label(RichText::new("没有匹配的文稿").small());
        }
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
                if !query.is_empty() && !record["name"].as_str().unwrap_or_default().to_lowercase().contains(&query) {
                    continue;
                }
                let status = record["status"].as_str().unwrap_or_default();
                if matches!(status, "ready" | "success" | "completed" | "imported") {
                    continue;
                }
                // Read-only imports already have a selectable source row. Do not list the same
                // material twice; capability information belongs beside that source.
                if matches!(status, "reading_only" | "read_only" | "readable" | "readable_only")
                    && record["sourceId"].as_str().is_some_and(|id| sources.iter().any(|s| s.id == id))
                {
                    continue;
                }
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
                if let Some(error) = record["error"].as_str().filter(|s| !s.is_empty()) {
                    egui::CollapsingHeader::new("详情").id_salt(("import-record", index, record["id"].as_str())).show(ui, |ui| {
                        ui.add(egui::Label::new(RichText::new(error).small()).wrap());
                    });
                }
            }
            let mut matches = 0;
            for source in
                sources.iter().filter(|s| query.is_empty() || s.title.to_lowercase().contains(&query) || s.folder.to_lowercase().contains(&query))
            {
                matches += 1;
                let folder = if source.folder.is_empty() { "未分类" } else { &source.folder };
                let capability = if !source.readable {
                    "暂不可阅读"
                } else if !source.searchable {
                    "仅阅读 · 不参与问答"
                } else {
                    "可用于问答"
                };
                let details = format!("{folder} · 版本 {} · {capability}", source.version);
                ui.push_id(("project-source", &source.id), |ui| {
                    let row = ui
                        .add_enabled_ui(source.readable, |ui| file_row(ui, &source.title, false, false))
                        .inner
                        .on_hover_text(format!("{}\n{details}", source.title));
                    self.source_row_rects.insert(source.id.clone(), row.rect);
                    if row.clicked() {
                        self.enqueue(json!({"kind":"openSource","sourceId":source.id.clone(),"version":source.version.clone()}));
                    }
                    ui.add(egui::Label::new(RichText::new(&details).font(medium(12.0)).color(Tokens::get(ui.ctx()).text_dim)).truncate())
                        .on_hover_text(&details);
                    ui.add_space(6.0);
                });
            }
            if matches == 0 && !query.is_empty() {
                ui.label(RichText::new("没有匹配的资料").small());
            }
            if sources.is_empty() && self.imports.is_empty() {
                ui.add_space(12.0);
                ui.add(
                    egui::Label::new(
                        RichText::new("尚无资料。点击“导入资料”添加项目副本。").font(medium(14.0)).color(Tokens::get(ui.ctx()).text_dim),
                    )
                    .wrap(),
                );
            }
        });
    }

    fn explorer_action(&mut self, action: &str, target_type: &str, target: &Value) {
        self.enqueue(json!({"kind":"explorer","action":action,"targetType":target_type,
            "targetId":target["id"].as_str().unwrap_or(""),"sourceId":target["sourceId"].as_str().unwrap_or(""),
            "version":target["version"].as_str().unwrap_or(""),"folder":target["path"].as_str().or(target["folder"].as_str()).unwrap_or(""),
            "folderId":target["folderId"].as_str().unwrap_or(""),"name":target["title"].as_str().or(target["name"].as_str()).unwrap_or("")}));
    }

    fn explorer_menu(&mut self, ui: &mut Ui, target_type: &str, target: &Value) {
        let actions: &[(&str, &str)] = match target_type {
            "project" => &[
                ("newFile", "新建文件"),
                ("newFolder", "新建文件夹"),
                ("import", "导入资料"),
                ("refresh", "刷新"),
                ("openTrash", "回收站"),
                ("projects", "全部项目 / 切换项目"),
            ],
            "folder" => &[
                ("newFile", "新建文件"),
                ("newFolder", "新建子文件夹"),
                ("import", "导入到此文件夹"),
                ("rename", "重命名"),
                ("move", "移动"),
                ("refresh", "刷新"),
                ("trash", "移到回收站"),
            ],
            "document" => &[("openDocument", "打开主文稿")],
            _ => &[
                ("open", "打开"),
                ("rename", "重命名"),
                ("move", "移动"),
                ("copy", "创建资料副本"),
                ("download", "下载此版本"),
                ("update", "更新资料版本"),
                ("trash", "移到回收站"),
            ],
        };
        for (action, label) in actions {
            let button = ui.button(*label);
            self.explorer_menu_rects.insert((*action).to_owned(), button.rect);
            if button.clicked() {
                self.explorer_action(action, target_type, target);
                ui.close();
            }
        }
    }

    fn explorer_file(&mut self, ui: &mut Ui, file: &Value) {
        let id = file["id"].as_str().unwrap_or("");
        let source_id = file["sourceId"].as_str().unwrap_or(id);
        let title = file["title"].as_str().unwrap_or("未命名资料");
        let version = file["version"].as_str().unwrap_or("");
        let searchable = file["searchable"].as_bool().unwrap_or(true);
        let selected = self.explorer_selected == id;
        let active = self.active_source_id == source_id && !source_id.is_empty();
        let row = file_row_details(ui, title, selected, active, Some(version), false)
            .on_hover_text(format!("{title} · v{version} · 右键查看更多操作；更新会保留历史版本"));
        self.source_row_rects.insert(source_id.to_owned(), row.rect);
        self.explorer_rects.insert(format!("source:{id}"), row.rect);
        row.context_menu(|ui| self.explorer_menu(ui, "source", file));
        if row.clicked() {
            eprintln!("DEBUG row clicked: {title}");
            self.explorer_selected = id.to_owned();
            row.request_focus();
            self.enqueue(json!({"kind":"openSource","sourceId":source_id,"version":version}));
        }
        // Reveal by pointer position, not `row.hovered()`: egui narrows `hovered` to the press
        // target on the press frame, which would make the buttons flicker out between the press
        // and release frames and swallow the click (both the row and the buttons lost it).
        let pointer_over_row = ui.ctx().input(|i| i.pointer.hover_pos()).is_some_and(|pos| row.rect.contains(pos));
        if pointer_over_row {
            self.source_row_actions(ui, file, source_id, version, searchable);
        }
        if selected && row.has_focus() {
            if ui.input_mut(|i| i.consume_key(egui::Modifiers::NONE, egui::Key::F2)) {
                self.explorer_action("rename", "source", file);
            }
            if ui.input_mut(|i| i.consume_key(egui::Modifiers::NONE, egui::Key::Delete)) {
                self.explorer_action("trash", "source", file);
            }
        }
    }

    /// Hover-revealed read action. Q&A uses the whole readable project by default, so source
    /// rows no longer double as per-question selectors.
    fn source_row_actions(&mut self, ui: &mut Ui, file: &Value, source_id: &str, version: &str, _searchable: bool) {
        let t = Tokens::get(ui.ctx());
        let Some(row) = self.explorer_rects.get(&format!("source:{}", file["id"].as_str().unwrap_or(""))).copied() else { return };
        let block_width = 50.0;
        let block = Rect::from_min_size(pos2(row.right() - block_width, row.top()), vec2(block_width, row.height()));
        ui.painter().rect_filled(block, radius::BASE, t.panel);
        let open_rect = Rect::from_min_size(pos2(block.left(), row.top() + 2.0), vec2(44.0, 28.0));
        let open = ui.put(
            open_rect,
            egui::Button::new(RichText::new(tl!("Open")).small().color(t.info))
                .fill(t.input)
                .stroke(Stroke::new(1.0, t.border))
                .corner_radius(radius::PANEL),
        );
        self.explorer_rects.insert(format!("source-open:{source_id}"), open_rect);
        if open.on_hover_text(tl!("Open in the source reader")).clicked() {
            self.enqueue(json!({"kind":"openSource","sourceId":source_id,"version":version}));
        }
    }

    fn explorer_folder(&mut self, ui: &mut Ui, folder: &Value, folders: &[Value], files: &[Value], depth: usize) {
        if depth > 20 {
            return;
        }
        let path = folder["path"].as_str().unwrap_or("");
        let name = folder["name"].as_str().unwrap_or_else(|| path.rsplit('/').next().unwrap_or(path));
        let direct: Vec<_> = files.iter().filter(|f| f["folder"].as_str().unwrap_or("") == path).cloned().collect();
        let label = format!("{name}  {}", direct.len());
        let shown = fit_text(ui.painter(), &label, &medium(14.0), 0.0, Tokens::get(ui.ctx()).text, (ui.available_width() - 40.0).max(20.0));
        let response = egui::CollapsingHeader::new(RichText::new(shown).font(medium(14.0)))
            .id_salt(("source-folder", &self.project_id, path))
            .default_open(true)
            .show(ui, |ui| {
                for child in folders.iter().filter(|f| f["path"].as_str().and_then(|p| p.rsplit_once('/').map(|v| v.0)) == Some(path)) {
                    self.explorer_folder(ui, child, folders, files, depth + 1);
                }
                for file in &direct {
                    self.explorer_file(ui, file);
                }
            });
        self.explorer_rects.insert(format!("folder:{}", folder["id"].as_str().unwrap_or(path)), response.header_response.rect);
        response.header_response.context_menu(|ui| self.explorer_menu(ui, "folder", folder));
        let rect = response.header_response.rect;
        let hover_rect = Rect::from_min_max(rect.min, pos2(ui.max_rect().right(), rect.bottom()));
        if ui.input(|i| i.pointer.hover_pos().is_some_and(|p| hover_rect.contains(p))) {
            let action = ui
                .put(Rect::from_min_size(pos2(ui.max_rect().right() - 22.0, rect.top()), vec2(22.0, 22.0)), egui::Button::new("＋").small())
                .on_hover_text("在此文件夹新建文件");
            if action.clicked() {
                self.explorer_action("newFile", "folder", folder);
            }
        }
    }

    fn resources(&mut self, ui: &mut Ui) {
        self.source_row_rects.clear();
        self.import_error(ui);
        self.explorer_rects.clear();
        self.explorer_menu_rects.clear();
        let project = json!({"id":self.project_id,"name":self.project_title});
        let header = ui
            .horizontal(|ui| {
                let width = (ui.available_width() - 120.0).max(20.0);
                let title = fit_text(ui.painter(), &self.project_title, &semibold(12.0), 0.0, Tokens::get(ui.ctx()).text, width);
                let response = ui.add_sized(vec2(width, 26.0), egui::Label::new(RichText::new(title).font(semibold(12.0))).sense(Sense::click()));
                for (icon, label, action) in [("＋", "新建文件", "newFile"), ("▣", "新建文件夹", "newFolder"), ("↻", "刷新项目", "refresh")]
                {
                    let button = ui.small_button(icon).on_hover_text(label);
                    self.explorer_rects.insert(format!("project:{action}"), button.rect);
                    if button.clicked() {
                        self.explorer_action(action, "project", &project);
                    }
                }
                response
            })
            .inner;
        self.explorer_rects.insert("project".into(), header.rect);
        header.context_menu(|ui| self.explorer_menu(ui, "project", &project));
        let files=self.explorer.as_ref().and_then(|v|v["files"].as_array()).cloned().unwrap_or_else(||self.sources.iter().map(|s|json!({"id":s.id,"sourceId":s.id,"title":s.title,"version":s.version,"folder":if s.folder.is_empty(){"未分类"}else{&s.folder},"readable":s.readable,"searchable":s.searchable})).collect());
        let mut folders = self.explorer.as_ref().and_then(|v| v["folders"].as_array()).cloned().unwrap_or_default();
        for file in &files {
            let path = file["folder"].as_str().unwrap_or("");
            if !path.is_empty() && !folders.iter().any(|f| f["path"].as_str() == Some(path)) {
                folders.push(json!({"id":path,"path":path}));
            }
        }
        folders.sort_by(|a, b| a["path"].as_str().cmp(&b["path"].as_str()));
        let body_height = (ui.available_height() - 52.0).max(0.0);
        egui::ScrollArea::vertical().id_salt("project-source-tree").max_height(body_height).auto_shrink([false, false]).show(ui, |ui| {
            if self.document_bound && !self.doc_title.is_empty() {
                let title = self.doc_title.clone();
                let body_active = self.active_source_id.is_empty();
                let row = file_row(ui, &title, body_active, body_active)
                    .on_hover_text(tl!("Current main document · editable; source management never replaces the body"));
                self.explorer_rects.insert("document".into(), row.rect);
                if row.clicked() {
                    self.explorer_action("openDocument", "document", &json!({"id":self.project_id}));
                }
                row.context_menu(|ui| self.explorer_menu(ui, "document", &json!({"id":self.project_id})));
                ui.separator();
            }
            for folder in folders.iter().filter(|f| !f["path"].as_str().unwrap_or("").contains('/')) {
                self.explorer_folder(ui, folder, &folders, &files, 0);
            }
            for file in files.iter().filter(|f| f["folder"].as_str().unwrap_or("").is_empty()) {
                self.explorer_file(ui, file);
            }
            if files.is_empty() && folders.is_empty() {
                ui.label(
                    RichText::new(tl!("Supports TXT / Markdown / DOCX; right-click a project to create a folder or import sources."))
                        .font(medium(12.0))
                        .color(Tokens::get(ui.ctx()).text_dim),
                );
            }
        });
        ui.add_space(8.0);
        self.sidebar_import_button(ui);
    }

    fn chat(&mut self, ui: &mut Ui) {
        // Keep the whole panel reachable even when the window is shorter than
        // the controls. Nested message/composer scrolls retain their own limits.
        let bottom = ui.max_rect().bottom();
        egui::ScrollArea::vertical().id_salt("project-chat-panel").auto_shrink([false, false]).show(ui, |ui| self.chat_contents(ui, bottom));
    }

    fn chat_contents(&mut self, ui: &mut Ui, bottom: f32) {
        self.refresh_state_rect = None;
        self.session_option_rects.clear();
        let current_title = self.sessions.iter().find(|s| s.id == self.current_session).map(|s| s.title.clone()).unwrap_or_else(|| "新会话".into());
        let sessions = self.sessions.clone();
        ui.horizontal(|ui| {
            ui.label(RichText::new("资料问答").font(semibold(15.0)));
            ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                if ui.add(egui::Button::new("×").min_size(vec2(28.0, 28.0))).on_hover_text("收起聊天；正在进行的请求不会取消").clicked()
                {
                    self.chat_open = false;
                }
                let new_session = ui.add_enabled(!self.busy, egui::Button::new("＋").min_size(vec2(28.0, 28.0))).on_hover_text("新建会话");
                self.new_session_rect = Some(new_session.rect);
                if new_session.clicked() {
                    self.request_session(None);
                }
                let history = ui
                    .add_enabled_ui(!self.busy, |ui| {
                        ui.menu_button(RichText::new("⏱").font(medium(16.0)), |ui| {
                            ui.set_min_width(220.0);
                            if sessions.is_empty() {
                                ui.label(RichText::new("暂无历史会话").small().weak());
                            }
                            for session in sessions {
                                let option = ui.selectable_label(session.id == self.current_session, &session.title);
                                self.session_option_rects.insert(session.id.clone(), option.rect);
                                if option.clicked() {
                                    self.request_session(Some(&session.id));
                                    ui.close();
                                }
                            }
                        })
                        .response
                    })
                    .inner;
                self.session_menu_rect = Some(history.rect);
                history.on_hover_text(format!("历史会话\n当前项目：{}\n当前会话：{}\n切换会话保留输入草稿", self.project_title, current_title));
            });
        });
        ui.separator();
        let messages = self.messages.clone();
        let has_status = !self.error.is_empty() || self.busy || self.uncertain;
        let status_height = if has_status { space::L + 28.0 } else { 0.0 };
        // Quick actions, scope, composer and Send stay reachable in a narrow panel.
        let controls_height = if ui.available_width() < 300.0 { 280.0 } else { 230.0 };
        let height = (bottom - ui.cursor().top() - controls_height - status_height).max(0.0);
        egui::ScrollArea::vertical().id_salt(("project-chat-messages", &self.current_session)).max_height(height).stick_to_bottom(true).show(
            ui,
            |ui| {
                ui.set_min_height(height);
                if messages.is_empty() {
                    ui.add_space(space::L);
                    ui.label(RichText::new("有什么想了解的？").font(semibold(16.0)));
                    ui.add_space(space::S);
                    ui.add(egui::Label::new(RichText::new("直接提问；回答会结合当前文档和项目资料。").color(Tokens::get(ui.ctx()).text_dim)).wrap());
                }
                let message_count = messages.len();
                for (index, message) in messages.into_iter().enumerate() {
                    ui.push_id((&message.id, index), |ui| {
                        let t = Tokens::get(ui.ctx());
                        let is_user = message.role == "user";
                        if is_user {
                            egui::Frame::NONE
                                .fill(t.checked)
                                .corner_radius(radius::CARD)
                                .inner_margin(egui::Margin::symmetric(space::M as i8, space::S as i8))
                                .show(ui, |ui| {
                                    ui.set_width(ui.available_width());
                                    ui.label(RichText::new("你").font(semibold(12.0)).color(t.accent_text));
                                    ui.add_space(space::XS);
                                    ui.add(egui::Label::new(RichText::new(&message.text).font(medium(14.0)).color(t.text)).wrap());
                                });
                        } else {
                            let now = ui.input(|i| i.time);
                            let copied_id = ui.make_persistent_id("chat-msg-copied-at");
                            let copied_recently = ui
                                .data(|d| d.get_temp::<f64>(copied_id))
                                .is_some_and(|at| (now - at) < 2.0);
                            ui.horizontal(|ui| {
                                ui.label(RichText::new("AI 助手").font(semibold(12.0)).color(t.text_dim));
                                if !message.text.trim().is_empty() {
                                    ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                                        let label = if copied_recently { "已复制" } else { "复制" };
                                        let copy_btn = ui
                                            .add(
                                                egui::Button::new(RichText::new(label).small().color(if copied_recently {
                                                    t.accent_text
                                                } else {
                                                    t.text_dim
                                                }))
                                                .fill(Color32::TRANSPARENT)
                                                .stroke(Stroke::new(1.0, t.border))
                                                .corner_radius(radius::SMALL)
                                                .min_size(vec2(42.0, 22.0)),
                                            )
                                            .on_hover_text("复制这条 AI 回复到剪贴板；不会修改正文");
                                        if copy_btn.clicked() {
                                            ui.ctx().copy_text(message.text.clone());
                                            ui.data_mut(|d| d.insert_temp(copied_id, now));
                                        }
                                    });
                                }
                            });
                            ui.add_space(space::XS);
                            render_chat_markdown(ui, &message.text, &t);
                        }
                        if !message.used_sources.is_empty() {
                            ui.add_space(space::XS);
                            ui.horizontal_wrapped(|ui| {
                                ui.label(RichText::new("本次使用：").small().color(t.text_dim));
                                for used in &message.used_sources {
                                    if used.id == "__main_document__" {
                                        ui.label(RichText::new(&used.title).small().color(t.text_dim))
                                            .on_hover_text("当前主文档（仅表示实际进入本次请求，不代表精确页码引用）");
                                    } else {
                                        let chip = ui
                                            .add(
                                                egui::Button::new(RichText::new(&used.title).small().color(t.accent_text))
                                                    .fill(t.input)
                                                    .stroke(Stroke::new(1.0, t.border))
                                                    .corner_radius(radius::SMALL)
                                                    .min_size(vec2(0.0, 22.0)),
                                            )
                                            .on_hover_text("点击在内置阅读器中打开（仅表示实际进入本次请求的资料）");
                                        if chip.clicked() {
                                            self.enqueue(json!({
                                                "kind": "openSource",
                                                "sourceId": used.id.clone(),
                                                "version": used.version.clone(),
                                            }));
                                        }
                                    }
                                }
                            });
                        }
                        if !message.citations.is_empty() {
                            ui.add_space(space::S);
                            let citation_count = message.citations.len();
                            let citations_open_id = ui.make_persistent_id("message-citations-open");
                            let citations_open = ui.data(|data| data.get_temp::<bool>(citations_open_id)).unwrap_or(false);
                            let toggle_label = format!("{citation_count} 份出处 · {}", if citations_open { "收起" } else { "展开" });
                            let toggle = ui.add(
                                egui::Button::new(RichText::new(toggle_label).font(medium(12.0)).color(t.text_dim))
                                    .fill(t.input)
                                    .stroke(Stroke::new(1.0, t.border))
                                    .corner_radius(radius::PANEL)
                                    .min_size(vec2(0.0, 28.0)),
                            );
                            if toggle.clicked() {
                                ui.data_mut(|data| data.insert_temp(citations_open_id, !citations_open));
                            }
                            if citations_open {
                                ui.add_space(space::S);
                                for (citation_index, citation) in message.citations.into_iter().enumerate() {
                                    if citation_index > 0 {
                                        ui.add_space(space::S);
                                    }
                                    egui::Frame::NONE
                                        .fill(t.input)
                                        .inner_margin(egui::Margin::same(space::M as i8))
                                        .corner_radius(radius::PANEL)
                                        .stroke(Stroke::new(1.0, t.border))
                                        .show(ui, |ui| {
                                            ui.label(RichText::new(if citation.quote.is_empty() { "本轮参考资料" } else { "原文出处" }).small().color(t.text_dim));
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
                                            let open = ui.add(egui::Button::new(RichText::new(if citation.quote.is_empty() { "打开资料" } else { "查看出处" }).color(t.info)).min_size(vec2(72.0, 28.0)));
                                            self.citation_open_rects.insert(json!([message.id,citation.source.id,citation.source.version,citation.page]).to_string(), open.rect);
                                            if open.clicked() {
                                                self.enqueue(json!({"kind":"openSource","sourceId":citation.source.id,"version":citation.source.version,"page":citation.page}));
                                            }
                                        });
                                }
                            }
                        }
                        if index + 1 < message_count {
                            ui.add_space(space::S);
                            let (divider, _) = ui.allocate_exact_size(vec2(ui.available_width(), 1.0), Sense::hover());
                            ui.painter().line_segment([divider.left_center(), divider.right_center()], Stroke::new(1.0, t.border));
                            ui.add_space(space::S);
                        }
                    });
                }
            },
        );
        ui.add_space(space::S);
        let quick_enabled = !self.busy && !self.uncertain && self.draft.trim().is_empty();
        let quick_hint = if self.busy {
            "正在回答，完成后可使用快捷提问"
        } else if self.uncertain {
            "上次结果尚未确认；请先核对历史，不会自动重发"
        } else if !self.draft.trim().is_empty() {
            "请先发送或清空当前输入，避免快捷提问覆盖草稿"
        } else {
            "基于当前主文档和项目资料发送这条预设提问"
        };
        ui.horizontal_wrapped(|ui| {
            ui.spacing_mut().item_spacing = vec2(space::S, space::S);
            for prompt in ["总结当前文档", "提炼项目要点", "核对论述依据"] {
                let chip = ui
                    .add_enabled(
                        quick_enabled,
                        egui::Button::new(RichText::new(prompt).font(medium(13.0))).corner_radius(radius::PANEL).min_size(vec2(0.0, 26.0)),
                    )
                    .on_hover_text(quick_hint);
                if chip.clicked() {
                    self.draft = prompt.into();
                    self.send();
                }
            }
        });
        // Selection rewriting remains on the editor's explicit selection toolbar. Do not show
        // project-chat writing actions until this panel actually implements that separate flow.
        ui.add_space(space::S);
        let t = Tokens::get(ui.ctx());
        let status = if !self.error.is_empty() {
            Some((self.error.clone(), t.danger))
        } else if self.busy {
            Some((if self.session_action_pending { "正在切换会话…".into() } else { "正在回答…收起面板不会取消请求".into() }, t.info))
        } else if self.uncertain {
            Some(("结果尚未确认；请核对历史。不会自动重发，可新建会话继续。".into(), t.warning))
        } else {
            None
        };
        if let Some((status_text, status_color)) = status {
            egui::Frame::NONE
                .fill(t.input)
                .stroke(Stroke::new(1.0, status_color))
                .corner_radius(radius::PANEL)
                .inner_margin(egui::Margin::same(space::S as i8))
                .show(ui, |ui| {
                    ui.horizontal(|ui| {
                        ui.spacing_mut().item_spacing = vec2(space::S, space::S);
                        let leading_width = if self.busy { 14.0 + space::S } else { 0.0 };
                        let stop_width = if self.busy && !self.session_action_pending { 72.0 + space::S } else { 0.0 };
                        let unlock_width = if self.uncertain && !self.busy { 72.0 + space::S } else { 0.0 };
                        let refresh_width = if self.uncertain || !self.error.is_empty() { 72.0 + space::S } else { 0.0 };
                        let label_width = (ui.available_width() - leading_width - stop_width - unlock_width - refresh_width).max(1.0);
                        if self.busy {
                            // 呼吸脉冲点（motion/pulse）：动画开关关闭时静态显示，不请求重绘。
                            let strength = if crate::theme::animations_enabled(ui.ctx()) {
                                let phase = (ui.input(|i| i.time) % crate::theme::motion::PULSE as f64 / crate::theme::motion::PULSE as f64) as f32;
                                ui.ctx().request_repaint();
                                0.45 + 0.55 * (phase * std::f32::consts::TAU).sin().abs()
                            } else {
                                1.0
                            };
                            let (dot, _) = ui.allocate_exact_size(vec2(14.0, 14.0), Sense::hover());
                            ui.painter().circle_filled(dot.center(), 4.0, t.accent.gamma_multiply(strength));
                        }
                        ui.add_sized(
                            vec2(label_width, 28.0),
                            egui::Label::new(RichText::new(status_text.clone()).font(medium(13.0)).color(status_color)).truncate(),
                        )
                        .on_hover_text(status_text);
                        if self.busy && !self.session_action_pending {
                            let stop = ui
                                .add(
                                    egui::Button::new(RichText::new("停止生成").color(t.danger).font(medium(13.0)))
                                        .min_size(vec2(72.0, 26.0)),
                                )
                                .on_hover_text("停止接收后续内容，保留已生成的回答并解锁输入");
                            if stop.clicked() {
                                self.enqueue(json!({"kind":"cancelQuestion"}));
                            }
                        }
                        if self.uncertain && !self.busy {
                            let unlock = ui
                                .add(
                                    egui::Button::new(RichText::new("继续会话").color(t.accent_text).font(medium(13.0)))
                                        .min_size(vec2(72.0, 26.0)),
                                )
                                .on_hover_text("我已核对历史记录，解除锁定并继续当前会话");
                            if unlock.clicked() {
                                self.enqueue(json!({"kind":"clearUncertain"}));
                            }
                        }
                        if self.uncertain || !self.error.is_empty() {
                            let refresh = ui
                                .add_enabled(!self.busy, egui::Button::new("刷新状态").min_size(vec2(72.0, 26.0)))
                                .on_hover_text("只读取服务端状态，不重发问题或生成请求");
                            self.refresh_state_rect = Some(refresh.rect);
                            if refresh.clicked() {
                                self.enqueue(json!({"kind":"refreshState"}));
                            }
                        }
                    });
                });
            ui.add_space(space::S);
        }
        egui::Frame::NONE
            .fill(t.input)
            .stroke(Stroke::new(1.0, t.border_strong))
            .corner_radius(radius::PANEL)
            .inner_margin(egui::Margin::same(space::S as i8))
            .show(ui, |ui| {
                let composer_font = medium(15.0);
                let composer_line_height = ui.fonts_mut(|fonts| fonts.row_height(&composer_font));
                let composer_scroll = egui::ScrollArea::vertical()
                    .id_salt("project-chat-composer-scroll")
                    .auto_shrink([false, true])
                    .max_height(5.0 * composer_line_height)
                    .show(ui, |ui| {
                        ui.add(
                            egui::TextEdit::multiline(&mut self.draft)
                                .id_salt("project-chat-draft")
                                .hint_text(if self.messages.is_empty() {
                                    "询问当前文档或整个项目…"
                                } else {
                                    "继续追问…"
                                })
                                .font(composer_font)
                                .char_limit(4000)
                                .desired_rows(1)
                                .desired_width(f32::INFINITY)
                                .frame(egui::Frame::NONE),
                        )
                    });
                let composer = composer_scroll.inner;
                self.composer_rect = Some(composer.rect.intersect(composer_scroll.inner_rect));
                if composer.has_focus() {
                    self.active = true;
                }
                ui.separator();
                ui.horizontal(|ui| {
                    let source_count = self.sources.iter().filter(|source| source.readable && source.searchable).count();
                    ui.add_sized(
                        vec2((ui.available_width() - 72.0).max(1.0), 28.0),
                        egui::Label::new(RichText::new(format!("范围：整个项目 · 主文档＋{source_count} 份资料")).small().color(t.text_dim)).truncate(),
                    )
                    .on_hover_text("默认携带当前主文档和全部可读取项目资料；较长项目会按问题节选。");
                    ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                        let send = ui.add_enabled(
                            !self.busy && !self.uncertain && !self.draft.trim().is_empty(),
                            egui::Button::new(RichText::new("发送").color(t.on_accent).font(medium(14.0)))
                                .fill(t.accent)
                                .corner_radius(radius::PANEL)
                                .min_size(vec2(64.0, 28.0)),
                        );
                        self.send_rect = Some(send.rect);
                        if send.clicked() {
                            self.send();
                        }
                    });
                });
            });
        let count = self.draft.chars().count();
        if count >= 3500 {
            ui.label(RichText::new(format!("问题 {count} / 4000")).small().weak());
        }
        ui.label(RichText::new("AI 生成内容，仅供参考；结论以原文出处为准").small().color(t.text_dim));
    }
}

/// Lightweight offline Markdown subset renderer for AI assistant chat replies.
/// Supports fenced code blocks, headings, bullet/numbered lists, blockquotes, inline **bold**
/// and inline `code`. Never executes links or HTML.
fn render_chat_markdown(ui: &mut Ui, text: &str, t: &Tokens) {
    let mut in_code = false;
    let mut code_buf = String::new();
    for raw_line in text.lines() {
        let trimmed = raw_line.trim_start();
        if trimmed.starts_with("```") {
            if in_code {
                render_code_block(ui, &code_buf, t);
                code_buf.clear();
                in_code = false;
            } else {
                in_code = true;
            }
            continue;
        }
        if in_code {
            if !code_buf.is_empty() {
                code_buf.push('\n');
            }
            code_buf.push_str(raw_line);
            continue;
        }
        if trimmed.is_empty() {
            ui.add_space(space::XS);
            continue;
        }
        if let Some(heading) = trimmed.strip_prefix("### ") {
            ui.add_space(space::XS);
            ui.add(egui::Label::new(inline_markdown_job(heading, 14.5, true, t.text, t)).wrap());
        } else if let Some(heading) = trimmed.strip_prefix("## ") {
            ui.add_space(space::XS);
            ui.add(egui::Label::new(inline_markdown_job(heading, 15.5, true, t.text, t)).wrap());
        } else if let Some(heading) = trimmed.strip_prefix("# ") {
            ui.add_space(space::XS);
            ui.add(egui::Label::new(inline_markdown_job(heading, 16.5, true, t.text, t)).wrap());
        } else if let Some(quote) = trimmed.strip_prefix("> ") {
            egui::Frame::NONE
                .fill(t.input)
                .inner_margin(egui::Margin { left: 8, right: 6, top: 3, bottom: 3 })
                .corner_radius(radius::SMALL)
                .show(ui, |ui| {
                    ui.add(egui::Label::new(inline_markdown_job(quote, 13.5, false, t.text_dim, t)).wrap());
                });
        } else if let Some(item) = trimmed.strip_prefix("- ").or_else(|| trimmed.strip_prefix("* ")).or_else(|| trimmed.strip_prefix("• ")) {
            ui.horizontal_top(|ui| {
                ui.add_space(4.0);
                ui.label(RichText::new("•").font(medium(14.0)).color(t.text_dim));
                ui.add(egui::Label::new(inline_markdown_job(item, 14.0, false, t.text, t)).wrap());
            });
        } else if let Some((num, rest)) = split_ordered_list_item(trimmed) {
            ui.horizontal_top(|ui| {
                ui.add_space(4.0);
                ui.label(RichText::new(num).font(medium(13.5)).color(t.text_dim));
                ui.add(egui::Label::new(inline_markdown_job(rest, 14.0, false, t.text, t)).wrap());
            });
        } else {
            ui.add(egui::Label::new(inline_markdown_job(raw_line, 14.0, false, t.text, t)).wrap());
        }
    }
    if in_code && !code_buf.is_empty() {
        render_code_block(ui, &code_buf, t);
    }
}

fn render_code_block(ui: &mut Ui, code: &str, t: &Tokens) {
    egui::Frame::NONE
        .fill(t.input)
        .stroke(Stroke::new(1.0, t.border))
        .corner_radius(radius::SMALL)
        .inner_margin(egui::Margin::same(space::S as i8))
        .show(ui, |ui| {
            ui.set_width(ui.available_width());
            ui.add(egui::Label::new(RichText::new(code).monospace().size(12.5).color(t.text)).wrap());
        });
}

fn split_ordered_list_item(line: &str) -> Option<(&str, &str)> {
    let dot = line.find(". ")?;
    let prefix = &line[..dot];
    if prefix.is_empty() || prefix.len() > 3 || !prefix.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    Some((&line[..=dot], &line[dot + 2..]))
}

fn inline_markdown_job(line: &str, size: f32, default_bold: bool, color: Color32, t: &Tokens) -> egui::text::LayoutJob {
    let mut job = egui::text::LayoutJob::default();
    let normal_font = if default_bold { semibold(size) } else { medium(size) };
    let bold_font = semibold(size);
    let code_font = egui::FontId::monospace((size - 1.0).max(11.0));
    let mut rest = line;
    while !rest.is_empty() {
        let next_bold = rest.find("**");
        let next_code = rest.find('`');
        let take_bold = match (next_bold, next_code) {
            (Some(b), Some(c)) => b < c,
            (Some(_), None) => true,
            _ => false,
        };
        if take_bold && let Some(b) = next_bold {
            if b > 0 {
                job.append(&rest[..b], 0.0, egui::TextFormat::simple(normal_font.clone(), color));
            }
            let after = &rest[b + 2..];
            if let Some(end) = after.find("**") {
                job.append(&after[..end], 0.0, egui::TextFormat::simple(bold_font.clone(), color));
                rest = &after[end + 2..];
            } else {
                job.append("**", 0.0, egui::TextFormat::simple(normal_font.clone(), color));
                rest = after;
            }
        } else if let Some(c) = next_code {
            if c > 0 {
                job.append(&rest[..c], 0.0, egui::TextFormat::simple(normal_font.clone(), color));
            }
            let after = &rest[c + 1..];
            if let Some(end) = after.find('`') {
                let mut fmt = egui::TextFormat::simple(code_font.clone(), t.accent_text);
                fmt.background = t.input;
                job.append(&after[..end], 0.0, fmt);
                rest = &after[end + 1..];
            } else {
                job.append("`", 0.0, egui::TextFormat::simple(normal_font.clone(), color));
                rest = after;
            }
        } else {
            job.append(rest, 0.0, egui::TextFormat::simple(normal_font.clone(), color));
            break;
        }
    }
    job
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
    let chat_toggle = activity_button(ui, chat_rect, "chat", "comment", "资料问答", chat_open, 0);
    app.project_workspace.chat_toggle_rect = Some(chat_toggle.rect);
    if chat_toggle.clicked() {
        app.project_workspace.chat_open = !chat_open;
    }
    // Rewrite works on the current selection; without one the button stays inert and says why.
    let (rewrite_rect, _) = ui.allocate_exact_size(vec2(ACTIVITY_BUTTON_SIZE, ACTIVITY_BUTTON_SIZE), Sense::hover());
    let has_selection = !app.session.sel.is_collapsed();
    let rewrite = activity_button(ui, rewrite_rect, "rewrite", "pen", "改写", false, 0);
    let rewrite = if has_selection { rewrite } else { rewrite.on_hover_text("先在正文里选中一段文字，再使用改写") };
    if has_selection && rewrite.clicked() {
        // Plan B (task §8): the popover opens in its configuration state and only 开始改写 sends.
        let _ = crate::ai::open_rewrite_config(app);
    }
    // AI drawing opens the existing old workbench and next.ai, never Word's upstream ink tab.
    // Clicking the Activity Bar marks the sidebar active before this widget runs; that focus
    // change is not a reason to reject the very click which requested the independent workbench.
    let (draw_rect, _) = ui.allocate_exact_size(vec2(ACTIVITY_BUTTON_SIZE, ACTIVITY_BUTTON_SIZE), Sense::hover());
    let draw_ready = app.has_document() && !app.source_workspace.source_active();
    let draw = activity_button(ui, draw_rect, "drawing", "draw", "AI 绘图", false, 0);
    app.drawing.set_open_rect(draw.rect);
    let draw = if draw_ready {
        draw
    } else {
        let hint = if !app.has_document() { "打开项目文稿后可用" } else { "先回到正文，再打开 AI 绘图" };
        draw.on_hover_text(hint)
    };
    #[cfg(not(target_arch = "wasm32"))]
    if draw_ready && draw.clicked() {
        app.drawing.open();
        app.project_workspace.release_focus();
    }
    let (search_rect, _) = ui.allocate_exact_size(vec2(ACTIVITY_BUTTON_SIZE, ACTIVITY_BUTTON_SIZE), Sense::hover());
    if activity_button(ui, search_rect, "search", "search", "命令搜索", false, 0).clicked() {
        let _ = app.run("tools.search", json!({}));
    }
}

pub fn show_panels(app: &mut WordApp, ui: &mut Ui) {
    if !app.project_workspace.enabled || !app.project_workspace.document_bound {
        return;
    }
    let t = crate::theme::Tokens::get(ui.ctx());
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
    app.project_workspace.citation_open_rects.clear();

    let duration = animation_time(ui.ctx(), motion::BASE);
    let resources_id = egui::Id::new("lingdoc-project-resources-width");
    let resources_base = saved_panel_width(app.ui.lingdoc_resources_width);
    let resources_live: Option<f32> = ui.ctx().data(|d| d.get_temp(resources_id));
    let resources_visibility =
        ui.ctx().animate_bool_with_time(egui::Id::new("lingdoc-project-resources-visibility"), app.project_workspace.resources_open, duration);
    let resources_width = resources_live.unwrap_or(resources_base) * resources_visibility;
    if resources_width > 0.5 {
        let output = egui::Panel::left("lingdoc-project-resources")
            .exact_size(resources_width)
            .resizable(false)
            .frame(egui::Frame::NONE.fill(t.panel).inner_margin(8).stroke(Stroke::new(1.0, t.border)))
            .show(ui, |ui| {
                ui.set_max_width(ui.available_width().min(336.0));
                app.project_workspace.resources(ui);
            });
        app.project_workspace.resource_rect = Some(output.response.rect);
        if resources_visibility >= 1.0 {
            let edge = Rect::from_min_size(
                pos2(output.response.rect.right() - PANEL_DRAG_GRIP * 0.5, output.response.rect.top()),
                vec2(PANEL_DRAG_GRIP, output.response.rect.height()),
            );
            panel_width_drag(ui, edge, resources_id, &mut app.project_workspace.resources_open, &mut app.ui.lingdoc_resources_width, 1.0);
        }
    }
    let chat_id = egui::Id::new("lingdoc-project-chat-width");
    let chat_base = saved_panel_width(app.ui.lingdoc_chat_width);
    let chat_live: Option<f32> = ui.ctx().data(|d| d.get_temp(chat_id));
    let chat_visibility =
        ui.ctx().animate_bool_with_time(egui::Id::new("lingdoc-project-chat-visibility"), app.project_workspace.chat_open, duration);
    let chat_width = chat_live.unwrap_or(chat_base) * chat_visibility;
    if chat_width > 0.5 {
        let output = egui::Panel::right("lingdoc-project-chat")
            .exact_size(chat_width)
            .resizable(false)
            .frame(egui::Frame::NONE.fill(t.panel).inner_margin(12).stroke(Stroke::new(1.0, t.border)))
            .show(ui, |ui| {
                ui.set_max_width(ui.available_width().min(456.0));
                app.project_workspace.chat(ui);
            });
        app.project_workspace.chat_rect = Some(output.response.rect);
        if chat_visibility >= 1.0 {
            let edge = Rect::from_min_size(
                pos2(output.response.rect.left() - PANEL_DRAG_GRIP * 0.5, output.response.rect.top()),
                vec2(PANEL_DRAG_GRIP, output.response.rect.height()),
            );
            panel_width_drag(ui, edge, chat_id, &mut app.project_workspace.chat_open, &mut app.ui.lingdoc_chat_width, -1.0);
        }
    }
    let ctx = ui.ctx().clone();
    prepare_frame(app, &ctx);
}

/// The one drag rule both side panels share. `direction` is +1 for a left panel (grip on its
/// right edge) and −1 for a right panel (grip on its left edge). While dragging, the live width
/// is kept in egui temp state so the panel tracks the pointer; on release a width below the
/// minimum collapses the panel (`open`), otherwise the clamped width is remembered in `saved`.
fn panel_width_drag(ui: &mut Ui, edge: Rect, id: egui::Id, open: &mut bool, saved: &mut f32, direction: f32) {
    let grip = ui.interact(edge, id.with("grip"), Sense::drag());
    if grip.hovered() || grip.dragged() {
        ui.ctx().set_cursor_icon(egui::CursorIcon::ResizeHorizontal);
        ui.painter().rect_filled(edge, radius::SMALL, Tokens::get(ui.ctx()).hover);
    }
    let mut live: Option<f32> = ui.ctx().data(|d| d.get_temp(id));
    if grip.drag_started() {
        live = Some(saved_panel_width(*saved));
    }
    if grip.dragged() {
        let origin = live.unwrap_or_else(|| saved_panel_width(*saved));
        live = Some((origin + grip.drag_delta().x * direction).clamp(PANEL_DRAG_FLOOR, PANEL_WIDTH_MAX));
    }
    if grip.drag_stopped() {
        let released = live.unwrap_or_else(|| saved_panel_width(*saved));
        if released < PANEL_WIDTH_MIN {
            *open = false;
        } else {
            *saved = released.clamp(PANEL_WIDTH_MIN, PANEL_WIDTH_MAX);
        }
        live = None;
    }
    if let Some(width) = live {
        ui.ctx().data_mut(|d| d.insert_temp(id, width));
    } else {
        ui.ctx().data_mut(|d| d.remove_temp::<f32>(id));
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn panel_width_reads_default_for_missing_or_junk_and_clamps_remembered() {
        for saved in [0.0, -1.0, f32::NAN, f32::INFINITY] {
            assert_eq!(super::saved_panel_width(saved), super::PANEL_WIDTH_DEFAULT, "saved {saved}");
        }
        assert_eq!(super::saved_panel_width(1.0), super::PANEL_WIDTH_MIN);
        assert_eq!(super::saved_panel_width(700.0), super::PANEL_WIDTH_MAX);
        assert_eq!(super::saved_panel_width(480.0), 480.0);
        assert_eq!(super::saved_panel_width(288.0), 288.0);
    }

    #[test]
    fn source_capabilities_survive_hydration_and_default_to_compatible_true() {
        let mut state = super::ProjectWorkspace::default();
        state
            .hydrate(serde_json::json!({"sources":[
                {"id":"read-only","title":"Scan.pdf","readable":true,"searchable":false},
                {"id":"legacy","title":"Notes.txt"}
            ]}))
            .unwrap();
        assert!(state.sources[0].readable);
        assert!(!state.sources[0].searchable);
        assert!(state.sources[1].readable && state.sources[1].searchable);
        assert_eq!(state.status()["sources"][0]["searchable"], false);
        assert!(state.hydrate(serde_json::json!({"sources":[{"id":"bad","searchable":"yes"}]})).is_err());
        assert_eq!(state.sources[0].id, "read-only");
    }

    #[test]
    fn composer_limit_counts_unicode_characters_not_utf8_bytes() {
        let mut state = super::ProjectWorkspace::default();
        state.draft = "问".repeat(4000);
        state.send();
        assert_eq!(state.actions.len(), 1);
        let mut overflow = super::ProjectWorkspace::default();
        overflow.draft = "问".repeat(4001);
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
            "usedSources":[{"id":"s1","title":"One","version":"v1"},{"id":"s2","title":"Two","version":"v1"}]}]);
        state.hydrate(value).unwrap();
        assert_eq!(state.doc_title, "Preferred.docx");
        assert_eq!(state.messages[0].citations.len(), 1);
        assert_eq!(state.messages[0].used_sources.len(), 2);
        assert_eq!(state.status()["messages"][0]["usedSources"][1]["id"], "s2");
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
    fn session_switch_restores_separate_drafts_without_silent_eviction() {
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
        state.hydrate(snapshot()).unwrap();
        assert_eq!(state.draft, "Conversation one draft");
    }

    #[test]
    fn same_session_id_in_different_projects_keeps_draft_and_pending_separate() {
        let mut state = ProjectWorkspace::default();
        let mut first = snapshot();
        first["projectId"] = json!("project-a");
        let mut second = snapshot();
        second["projectId"] = json!("project-b");
        state.hydrate(first.clone()).unwrap();
        state.draft = "Project A question".into();
        state.send();
        state.take_actions();
        second["messages"] = json!([{"id":"foreign-user","role":"user","text":"Project A question"}]);
        state.hydrate(second.clone()).unwrap();
        assert!(state.draft.is_empty());
        assert!(state.pending_text.is_none());
        assert!(!state.busy);
        state.draft = "Project B draft".into();
        state.hydrate(first).unwrap();
        assert_eq!(state.draft, "Project A question");
        state.hydrate(second).unwrap();
        assert_eq!(state.draft, "Project B draft");
    }

    #[test]
    fn restored_draft_is_only_read_at_an_identity_boundary() {
        let mut state = ProjectWorkspace::default();
        let mut first = snapshot();
        first["restoredDraft"] = json!("Recovered text");
        state.hydrate(first.clone()).unwrap();
        assert_eq!(state.draft, "Recovered text");
        state.draft = "New typing".into();
        state.hydrate(first.clone()).unwrap();
        assert_eq!(state.draft, "New typing");
        first["currentSessionId"] = json!("c2");
        first["restoredDraft"] = json!("Other session text");
        state.hydrate(first).unwrap();
        assert_eq!(state.draft, "Other session text");
    }

    #[test]
    fn unknown_outcome_keeps_question_and_blocks_resend_but_allows_new_session() {
        let mut state = ProjectWorkspace::default();
        state.hydrate(snapshot()).unwrap();
        state.draft = "Possibly submitted".into();
        state.send();
        state.take_actions();
        let mut unknown = snapshot();
        unknown["uncertain"] = json!(true);
        unknown["messages"] = json!([{"id":"accepted","role":"user","text":"Possibly submitted"}]);
        state.hydrate(unknown).unwrap();
        assert_eq!(state.draft, "Possibly submitted");
        assert!(!state.busy);
        state.send();
        assert!(state.take_actions().is_empty());
        state.request_session(None);
        assert_eq!(state.take_actions(), vec![json!({"kind":"newSession"})]);
    }

    #[test]
    fn session_navigation_is_single_flight_and_failure_unlocks_without_losing_draft() {
        let mut state = ProjectWorkspace::default();
        state.hydrate(snapshot()).unwrap();
        state.draft = "Keep this".into();
        state.request_session(Some("c1"));
        assert!(state.take_actions().is_empty());
        state.request_session(None);
        state.request_session(None);
        state.request_session(Some("c2"));
        assert_eq!(state.take_actions(), vec![json!({"kind":"newSession"})]);
        state.hydrate(snapshot()).unwrap();
        assert!(state.busy);
        let mut failed = snapshot();
        failed["interactionPending"] = json!(false);
        failed["error"] = json!("Switch failed");
        state.hydrate(failed).unwrap();
        assert!(!state.busy);
        assert_eq!(state.draft, "Keep this");
        state.request_session(Some("c2"));
        assert_eq!(state.take_actions(), vec![json!({"kind":"selectSession","sessionId":"c2"})]);
        let mut switched = snapshot();
        switched["currentSessionId"] = json!("c2");
        state.hydrate(switched).unwrap();
        assert!(!state.busy);
    }

    #[test]
    fn frontend_pending_snapshot_does_not_acknowledge_send_early() {
        let mut state = ProjectWorkspace::default();
        state.hydrate(snapshot()).unwrap();
        state.draft = "Question".into();
        state.send();
        let mut pending = snapshot();
        pending["interactionPending"] = json!(true);
        pending["messages"] = json!([{"id":"user","role":"user","text":"Question"}]);
        state.hydrate(pending.clone()).unwrap();
        assert!(state.busy);
        assert_eq!(state.draft, "Question");
        pending["interactionPending"] = json!(false);
        state.hydrate(pending).unwrap();
        assert!(!state.busy);
        assert!(state.draft.is_empty());
    }

    #[test]
    fn uncertain_chat_refresh_only_emits_read_action_and_keeps_question() {
        use egui_kittest::kittest::{NodeT, Queryable};
        let mut app = WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()), crate::Services::default());
        let mut value = snapshot();
        value["uncertain"] = json!(true);
        app.project_workspace.hydrate(value.clone()).unwrap();
        app.project_workspace.draft = "Unconfirmed question".into();
        app.project_workspace.chat_open = true;
        app.project_workspace.resources_open = false;
        let mut h = egui_kittest::Harness::builder().with_size(vec2(1300.0, 1000.0)).build_ui_state(
            |ui, app: &mut WordApp| {
                let ctx = ui.ctx().clone();
                app.logic(&ctx);
                app.ui(ui);
            },
            app,
        );
        h.run_steps(6);
        assert!(!h.get_by_label("刷新状态").accesskit_node().is_disabled());
        assert!(h.get_by_label("发送").accesskit_node().is_disabled());
        assert!(h.state().project_workspace.status()["refreshStateRect"].is_object());
        h.get_by_label("刷新状态").click();
        h.run_steps(2);
        assert_eq!(h.state_mut().project_workspace.take_actions(), vec![json!({"kind":"refreshState"})]);
        assert_eq!(h.state().project_workspace.draft, "Unconfirmed question");
        value["busy"] = json!(true);
        h.state_mut().project_workspace.hydrate(value).unwrap();
        h.run_steps(2);
        assert!(h.get_by_label("刷新状态").accesskit_node().is_disabled());
    }

    #[test]
    fn used_sources_are_provenance_not_fabricated_citations() {
        let mut state = ProjectWorkspace::default();
        let mut value = snapshot();
        value["messages"] = json!([{
            "id":"answer",
            "role":"assistant",
            "text":"结合主文档的回答",
            "usedSources":[{"id":"__main_document__","title":"主文稿.docx","version":"current"}]
        }]);
        state.hydrate(value).unwrap();
        assert_eq!(state.messages[0].used_sources[0].id, "__main_document__");
        assert_eq!(state.messages[0].used_sources[0].title, "主文稿.docx");
        assert!(state.messages[0].citations.is_empty());
        assert_eq!(state.status()["messages"][0]["usedSources"][0]["id"], "__main_document__");
        assert_eq!(state.status()["messages"][0]["citations"], json!([]));
    }

    #[test]
    fn quick_actions_use_whole_project_without_manual_source_selection() {
        use egui_kittest::kittest::{NodeT, Queryable};
        let mut app = WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()), crate::Services::default());
        let mut value = snapshot();
        value["sources"] = json!([]);
        value["contextSources"] = json!([]);
        app.project_workspace.hydrate(value).unwrap();
        app.project_workspace.chat_open = true;
        app.project_workspace.resources_open = false;
        let mut h = egui_kittest::Harness::builder().with_size(vec2(1300.0, 900.0)).build_ui_state(
            |ui, app: &mut WordApp| {
                let ctx = ui.ctx().clone();
                app.logic(&ctx);
                app.ui(ui);
            },
            app,
        );
        h.run_steps(6);
        assert!(h.query_by_label("范围：整个项目 · 主文档＋0 份资料").is_some());
        for label in ["总结当前文档", "提炼项目要点", "核对论述依据"] {
            assert!(!h.get_by_label(label).accesskit_node().is_disabled());
        }
        assert!(h.state().project_workspace.actions.is_empty());
        h.get_by_label("总结当前文档").click();
        h.run_steps(2);
        assert_eq!(h.state_mut().project_workspace.take_actions(), vec![json!({"kind":"send","text":"总结当前文档"})]);
    }

    #[test]
    fn project_chat_hides_unimplemented_writing_actions() {
        use egui_kittest::kittest::Queryable;
        let mut app = WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()), crate::Services::default());
        app.project_workspace.hydrate(snapshot()).unwrap();
        app.project_workspace.chat_open = true;
        app.project_workspace.resources_open = false;
        let mut h = egui_kittest::Harness::builder().with_size(vec2(1300.0, 900.0)).build_ui_state(
            |ui, app: &mut WordApp| {
                let ctx = ui.ctx().clone();
                app.logic(&ctx);
                app.ui(ui);
            },
            app,
        );
        h.run_steps(6);
        for label in ["改写选段", "扩展选段", "压缩选段"] {
            assert!(h.query_by_label(label).is_none(), "{label} must stay out of the unconnected project-chat flow");
        }
        assert!(h.state_mut().project_workspace.take_actions().is_empty());
        assert!(h.state().project_workspace.messages.is_empty());
    }

    #[test]
    fn resource_tree_hides_empty_folders_and_keeps_import_available() {
        use egui_kittest::kittest::Queryable;
        for folder in [None, Some("参考文献"), Some("现场记录"), Some("")] {
            let mut state = super::ProjectWorkspace::default();
            let mut value = snapshot();
            value["projectId"] = json!("p1");
            value["sources"] = folder.map(|folder| json!([{"id":"s1","title":"Source","version":"1","folder":folder}])).unwrap_or_else(|| json!([]));
            state.hydrate(value).unwrap();
            let mut fonts_ready = false;
            let mut h = egui_kittest::Harness::builder().with_size(vec2(260.0, 600.0)).build_ui_state(
                move |ui, state: &mut super::ProjectWorkspace| {
                    if !fonts_ready {
                        ui.ctx().set_fonts(crate::theme::font_definitions(false, false));
                        fonts_ready = true;
                        return;
                    }
                    state.resources(ui);
                },
                state,
            );
            h.run_steps(6);
            for empty in ["申报要求与模板  0", "参考文献  0", "前期研究材料  0"] {
                assert!(h.query_by_label(empty).is_none(), "empty group remained visible: {empty}");
            }
            if let Some(folder) = folder {
                let name = if folder.is_empty() { "未分类" } else { folder };
                assert!(h.query_by_label(&format!("{name}  1")).is_some());
                assert!(h.state().source_row_rects.contains_key("s1"));
            } else {
                assert!(h.state().source_row_rects.is_empty());
            }
            h.get_by_label("＋ 导入资料").click();
            h.run_steps(2);
            assert_eq!(h.state_mut().take_actions(), vec![json!({"kind":"importSources","projectId":"p1"})]);
        }
    }

    #[test]
    fn explorer_uses_tree_identity_for_management_and_canonical_identity_for_reading() {
        use egui_kittest::kittest::Queryable;
        let mut state = super::ProjectWorkspace::default();
        let mut value = snapshot();
        value["projectId"] = json!("p1");
        value["explorer"] = json!({"revision":1,"folders":[{"id":"f1","path":"资料","name":"资料"},{"id":"f2","path":"资料/空文件夹","name":"空文件夹"}],"files":[{"id":"tree-copy","sourceId":"canonical","title":"Copied source.md","version":"3","folder":"资料","folderId":"f1"}]});
        state.hydrate(value).unwrap();
        let mut fonts_ready = false;
        let mut h = egui_kittest::Harness::builder().with_size(vec2(300.0, 700.0)).build_ui_state(
            move |ui, state: &mut super::ProjectWorkspace| {
                if !fonts_ready {
                    ui.ctx().set_fonts(crate::theme::font_definitions(false, false));
                    fonts_ready = true;
                    return;
                }
                state.resources(ui);
            },
            state,
        );
        h.run_steps(6);
        assert!(h.query_by_label("空文件夹  0").is_some());
        h.get_by_label("Copied source.md").click();
        h.run_steps(2);
        assert_eq!(h.state_mut().take_actions(), vec![json!({"kind":"openSource","sourceId":"canonical","version":"3"})]);
        // Right-click the title area: the hover-revealed action buttons (task §8) legitimately
        // own the row's right side, so the context menu is triggered from the title side.
        let title_pos = {
            let row = h.query_by_label("Copied source.md").expect("row").rect();
            egui::pos2(row.left() + 60.0, row.center().y)
        };
        for (pressed, steps) in [(true, 1), (false, 2)] {
            h.event(egui::Event::PointerButton { pos: title_pos, button: egui::PointerButton::Secondary, pressed, modifiers: egui::Modifiers::NONE });
            h.run_steps(steps);
        }
        h.get_by_label("重命名").click();
        h.run_steps(2);
        let actions = h.state_mut().take_actions();
        assert_eq!(actions.len(), 1);
        assert_eq!(actions[0]["targetId"], "tree-copy");
        assert_eq!(actions[0]["sourceId"], "canonical");
        assert_eq!(actions[0]["action"], "rename");
    }

    #[test]
    fn explorer_toolbar_stays_inside_narrow_sidebar() {
        let mut state = super::ProjectWorkspace::default();
        let mut value = snapshot();
        value["projectId"] = json!("p1");
        value["projectTitle"] = json!("Long project title ".repeat(20));
        value["explorer"] = json!({"folders":[{"id":"f1","path":"Folder","name":"Folder"}],"files":[{"id":"node","sourceId":"canonical","title":"Long source title ".repeat(20),"version":"3","folder":"Folder"}]});
        state.hydrate(value).unwrap();
        let mut fonts_ready = false;
        let mut h = egui_kittest::Harness::builder().with_size(vec2(214.0, 700.0)).build_ui_state(
            move |ui, state: &mut super::ProjectWorkspace| {
                if !fonts_ready {
                    ui.ctx().set_fonts(crate::theme::font_definitions(false, false));
                    fonts_ready = true;
                    return;
                }
                state.resources(ui);
                for key in ["project:newFile", "project:newFolder", "project:refresh"] {
                    let rect = state.explorer_rects[key];
                    assert!(rect.right() <= ui.clip_rect().right(), "{key} clipped: {rect:?}");
                }
            },
            state,
        );
        h.run_steps(6);
    }

    #[test]
    fn long_draft_and_error_keep_send_inside_chat_viewport() {
        for (width, height, failed) in [(230.0, 700.0, false), (230.0, 900.0, true), (366.0, 700.0, true)] {
            let mut state = super::ProjectWorkspace::default();
            state.hydrate(snapshot()).unwrap();
            state.draft = "A long unsent question with multiple lines.\n".repeat(50);
            if failed {
                state.error = "Request outcome unknown; refresh before retry. ".repeat(30);
                state.uncertain = true;
            }
            let mut fonts_ready = false;
            let mut h = egui_kittest::Harness::builder().with_size(vec2(width, height)).build_ui_state(
                move |ui, state: &mut super::ProjectWorkspace| {
                    if !fonts_ready {
                        ui.ctx().set_fonts(crate::theme::font_definitions(false, false));
                        fonts_ready = true;
                        return;
                    }
                    state.chat(ui);
                    let clip = ui.clip_rect();
                    assert!(clip.contains_rect(state.send_rect.unwrap()), "Send clipped by viewport: clip={clip:?}, send={:?}", state.send_rect);
                    assert!(clip.contains_rect(state.composer_rect.unwrap()), "Composer clipped by viewport");
                    if let Some(refresh) = state.refresh_state_rect {
                        assert!(clip.contains_rect(refresh));
                    }
                },
                state,
            );
            h.run_steps(8);
            let send = h.state().send_rect.unwrap();
            let composer = h.state().composer_rect.unwrap();
            assert!(send.bottom() <= height, "Send outside {width}x{height}: {send:?}");
            assert!(send.left() >= 0.0 && send.right() <= width, "Send horizontally clipped: {send:?}");
            assert!(composer.height() > 0.0 && composer.bottom() < send.bottom());
        }
    }

    #[test]
    fn citation_only_opens_source_without_adding_request_context() {
        use egui_kittest::kittest::Queryable;
        let mut app = WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()), crate::Services::default());
        let mut value = snapshot();
        value["messages"] = json!([{"id":"answer","role":"assistant","text":"A sourced answer","citations":[{"sourceId":"s5","title":"Fifth source","version":"1","quote":"Original sentence"}]}]);
        app.project_workspace.hydrate(value).unwrap();
        app.project_workspace.chat_open = true;
        app.project_workspace.resources_open = false;
        let mut h = egui_kittest::Harness::builder().with_size(vec2(1300.0, 1000.0)).build_ui_state(
            |ui, app: &mut WordApp| {
                let ctx = ui.ctx().clone();
                app.logic(&ctx);
                app.ui(ui);
            },
            app,
        );
        h.run_steps(6);
        assert!(h.query_by_label("加入本次").is_none());
        h.get_by_label("1 份出处 · 展开").click();
        h.run_steps(3);
        assert!(h.query_by_label("1 份出处 · 收起").is_some());
        assert!(h.query_by_label("加入本次").is_none());
        assert!(h.state().project_workspace.actions.is_empty());
        h.get_by_label("查看出处").click();
        h.run_steps(2);
        assert_eq!(
            h.state_mut().project_workspace.take_actions(),
            vec![json!({"kind":"openSource","sourceId":"s5","version":"1","page":null})]
        );
    }

    #[test]
    fn clicking_chat_answer_does_not_lock_history_wheel_scroll() {
        use egui_kittest::kittest::Queryable;
        let mut app = WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()), crate::Services::default());
        let mut value = snapshot();
        value["messages"] = json!([
            {"id":"first","role":"assistant","text":"Older answer line.\n".repeat(40),"citations":[{"sourceId":"s1","title":"Source","version":"1","quote":"First original"}]},
            {"id":"middle","role":"assistant","text":"Middle answer line.\n".repeat(25),"citations":[{"sourceId":"s1","title":"Source","version":"1","quote":"Middle original"}]},
            {"id":"last","role":"assistant","text":"Last response","citations":[{"sourceId":"s1","title":"Source","version":"1","quote":"Last original"}]}
        ]);
        app.project_workspace.hydrate(value).unwrap();
        app.project_workspace.chat_open = true;
        app.project_workspace.resources_open = false;
        let mut h = egui_kittest::Harness::builder().with_size(vec2(1600.0, 1000.0)).build_ui_state(
            |ui, app: &mut WordApp| {
                let ctx = ui.ctx().clone();
                app.logic(&ctx);
                app.ui(ui);
            },
            app,
        );
        h.run_steps(6);
        h.query_all_by_label("1 份出处 · 展开").last().unwrap().click();
        h.run_steps(3);
        let citation_key = json!(["last", "s1", "1", null]).to_string();
        let before = h.state().project_workspace.citation_open_rects[&citation_key].top();
        h.get_by_label("Last response").click();
        h.run_steps(3);
        let state = &h.state().project_workspace;
        // 指针落在消息区内的真实引用卡上，避免 composer 新增 chips 后滚轮命中布局变化。
        let at = state.citation_open_rects[&citation_key].center();
        h.event(egui::Event::PointerMoved(at));
        h.run_steps(2);
        h.event(egui::Event::MouseWheel {
            unit: egui::MouseWheelUnit::Point,
            delta: vec2(0.0, 200.0),
            phase: egui::TouchPhase::Move,
            modifiers: egui::Modifiers::NONE,
        });
        h.run_steps(5);
        let after = h.state().project_workspace.citation_open_rects[&citation_key].top();
        assert!(after > before + 100.0, "clicking a chat answer must not prevent scrolling upwards: {before} -> {after}");
        assert!(h.state().project_workspace.actions.is_empty());
    }

    fn start_page_harness(value: Value, selected: bool) -> egui_kittest::Harness<'static, WordApp> {
        let mut app = WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()), crate::Services::default());
        app.project_workspace.hydrate(value).unwrap();
        app.project_workspace.project_selected = selected;
        app.ui.backstage = true;
        app.ui.backstage_page = "projects".into();
        let mut h = egui_kittest::Harness::builder().with_size(vec2(900.0, 700.0)).build_ui_state(
            |ui, app: &mut WordApp| {
                let ctx = ui.ctx().clone();
                app.logic(&ctx);
                app.ui(ui);
            },
            app,
        );
        h.run_steps(6);
        h
    }

    #[test]
    fn project_card_opens_workspace_and_all_sources_without_preview() {
        use egui_kittest::kittest::Queryable;
        let mut value = snapshot();
        value["projectId"] = json!("p1");
        value["sources"] = json!([
            {"id":"s1","title":"Method","version":1,"folder":"方法参考"},
            {"id":"s2","title":"Interview A","version":1,"folder":"访谈记录"},
            {"id":"s3","title":"Interview B","version":1,"folder":"访谈记录"}
        ]);
        let mut h = start_page_harness(value, false);
        let status = h.state().project_workspace.status();
        assert_eq!(status["documentTitle"], "Draft.docx");
        assert_eq!(status["projectSummary"], "方法参考 1 份 · 访谈记录 2 份");
        h.get_by_label("进入工作区").click();
        h.run_steps(2);
        assert_eq!(h.state_mut().project_workspace.take_actions(), vec![json!({"kind":"enterProject","projectId":"p1"})]);
        h.get_by_label("查看全部资料").click();
        h.run_steps(2);
        assert_eq!(h.state_mut().project_workspace.take_actions(), vec![json!({"kind":"enterProject","projectId":"p1","focus":"sources"})]);
    }

    #[test]
    fn start_page_failed_open_is_visible_without_hiding_controls() {
        use egui_kittest::kittest::Queryable;
        let mut value = snapshot();
        value["projectId"] = json!("p1");
        value["error"] = json!("The material could not be opened. ".repeat(90));
        let h = start_page_harness(value, true);
        assert!(h.query_by_label("上次操作未完成").is_some());
        let state = &h.state().project_workspace;
        assert!(state.project_search_rect.unwrap().bottom() < 600.0);
        assert!(state.import_button_rect.unwrap().bottom() < 700.0);
        assert!(state.actions.is_empty());
    }

    #[test]
    fn start_page_clear_search_returns_focus_and_restores_sources() {
        use egui_kittest::kittest::Queryable;
        let mut value = snapshot();
        value["projectId"] = json!("p1");
        let mut h = start_page_harness(value, true);
        h.state_mut().project_workspace.project_search = "absent".into();
        h.run_steps(2);
        assert!(h.state().project_workspace.source_row_rects.is_empty());
        h.get_by_label("清空").click();
        h.run_steps(2);
        assert!(h.state().project_workspace.project_search.is_empty());
        assert!(h.state().project_workspace.source_row_rects.contains_key("s1"));
        h.event(egui::Event::Text("Reference".into()));
        h.run_steps(2);
        assert_eq!(h.state().project_workspace.project_search, "Reference");
        assert!(h.state().project_workspace.actions.is_empty());
    }

    #[test]
    fn start_page_without_project_offers_new_and_open_actions() {
        use egui_kittest::kittest::Queryable;
        let mut h = start_page_harness(json!({"projectId":"","documentBound":false}), false);
        assert!(h.query_by_label("还没有打开项目").is_some());
        h.get_by_label("新建项目…").click();
        h.run_steps(2);
        assert_eq!(h.state_mut().project_workspace.take_actions(), vec![json!({"kind":"newProject"})]);
        h.get_by_label("打开项目目录…").click();
        h.run_steps(2);
        assert_eq!(h.state_mut().project_workspace.take_actions(), vec![json!({"kind":"openProject"})]);
    }

    #[test]
    fn start_page_lists_projects_without_a_current_project() {
        use egui_kittest::kittest::Queryable;
        let value = json!({"projectId":"","documentBound":false,"projects":[
            {"id":"p1","name":"研究计划","status":"draft","documentTitle":"主文稿.docx","openable":true}
        ]});
        let mut h = start_page_harness(value, false);
        assert!(h.query_by_label("全部项目").is_some());
        assert!(h.query_by_label("研究计划").is_some());
        assert!(h.query_by_label("新建项目…").is_some());
        assert!(h.query_by_label("打开项目目录…").is_some());
        h.get_by_label("打开项目").click();
        h.run_steps(2);
        assert_eq!(h.state_mut().project_workspace.take_actions(), vec![json!({"kind":"switchProject","projectId":"p1"})]);
    }

    #[test]
    fn start_page_disconnected_snapshot_drops_previous_controls() {
        use egui_kittest::kittest::Queryable;
        let mut value = snapshot();
        value["projectId"] = json!("p1");
        let mut h = start_page_harness(value, true);
        assert!(h.state().project_workspace.project_document_rect.is_some());
        h.state_mut().project_workspace.hydrate(json!({"projectId":"","documentBound":false,"error":"Project unavailable"})).unwrap();
        h.run_steps(2);
        assert!(h.query_by_label("Project unavailable").is_some());
        let state = &h.state().project_workspace;
        assert!(state.project_card_rect.is_none() && state.project_document_rect.is_none());
        assert!(state.project_search_rect.is_none() && state.import_button_rect.is_none());
        assert!(state.source_row_rects.is_empty());
        assert!(state.actions.is_empty());
    }

    #[test]
    fn start_page_read_only_source_is_single_row_and_unreadable_source_is_disabled() {
        use egui_kittest::kittest::{NodeT, Queryable};
        let value = json!({"projectId":"p1","projectTitle":"Test","docTitle":"Draft.docx","sources":[
            {"id":"scan","title":"Scan.pdf","version":"2","folder":"参考文献","readable":true,"searchable":false},
            {"id":"missing","title":"Missing.pdf","version":"1","readable":false,"searchable":false}
        ],"imports":[{"id":"import-scan","sourceId":"scan","name":"Scan.pdf","status":"readable_only"}]});
        let mut h = start_page_harness(value, true);
        assert!(h.query_by_label("Scan.pdf · 仅阅读").is_none(), "a retained read-only material must not also appear as a duplicate import row");
        assert!(h.get_by_label("Missing.pdf").accesskit_node().is_disabled());
        assert!(h.query_by_label("参考文献 · 版本 2 · 仅阅读 · 不参与问答").is_some());
        h.get_by_label("Scan.pdf").click();
        h.run_steps(2);
        assert_eq!(h.state_mut().project_workspace.take_actions(), vec![json!({"kind":"openSource","sourceId":"scan","version":"2"})]);
    }
}
