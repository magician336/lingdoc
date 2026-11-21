//! Read-only selection AI result window.
//!
//! Its visibility is deliberately separate from task lifetime. Pinning only preserves the window
//! across selection changes; it is not a collection, a second result slot, or a live anchor.

use egui::{Rect, Ui, Vec2, pos2, vec2};
use wordcraft_engine::Selection;

use crate::WordApp;
use crate::ai::{AiAction, AiFinish, AiResult, AiResultPhase, AiSource, AiSourceRef, AiTool};
use crate::theme::Tokens;

/// Rewrite-only configuration surface (approved plan B, task §8): the frozen selection plus
/// the sources the user explicitly checked for this rewrite. Consumed by 开始改写; never
/// persisted, never carried across tasks, sessions or launches.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct AiPopoverConfig {
    pub sources: Vec<AiSourceRef>,
    /// Optional user-specified guidance for this rewrite only; empty uses the default rewrite prompt.
    pub custom_instruction: String,
}

const VGAP: f32 = 8.0;
pub const POPOVER_W: f32 = 440.0;
pub const POPOVER_H: f32 = 460.0;
const POPOVER_MIN_W: f32 = 360.0;
const POPOVER_MIN_H: f32 = 320.0;
const POPOVER_SIZE: Vec2 = Vec2::new(POPOVER_W, POPOVER_H);
const POPOVER_MIN_SIZE: Vec2 = Vec2::new(POPOVER_MIN_W, POPOVER_MIN_H);
const SOURCE_H: f32 = 76.0;

#[derive(Clone, Debug, Default, PartialEq)]
pub struct AiPopoverState {
    pub open: bool,
    pub pinned: bool,
    /// Initial screen rectangle, subsequently updated only by the user's own window drag.
    pub rect: Option<Rect>,
    pub document: u64,
    /// Display-only selection snapshot. Tasks never store or consult this value.
    pub selection: Option<Selection>,
    pub source: String,
    pub task: Option<u32>,
    pub message: Option<String>,
    /// Rewrite configuration state; `Some` renders the configure surface, `None` the task.
    pub config: Option<AiPopoverConfig>,
}

impl AiPopoverState {
    pub fn open(&mut self, task: u32, document: u64, selection: Selection, source: String, rect: Option<Rect>) {
        self.open = true;
        self.pinned = false;
        self.rect = rect;
        self.document = document;
        self.selection = Some(selection);
        self.source = source;
        self.task = Some(task);
        self.message = None;
        self.config = None;
    }

    /// Plan B entry: the same frozen snapshot without a task yet; the configure surface owns
    /// the window until 开始改写 freezes the request or the user cancels.
    pub fn open_config(&mut self, document: u64, selection: Selection, source: String, rect: Option<Rect>) {
        self.open = true;
        self.pinned = false;
        self.rect = rect;
        self.document = document;
        self.selection = Some(selection);
        self.source = source;
        self.task = None;
        self.message = None;
        self.config = Some(AiPopoverConfig::default());
    }

    /// Close only the surface. In particular this does not clear `task`, `source`, or any task
    /// receiver, and a later Finished event has no path that sets `open` back to true.
    pub fn close(&mut self) {
        self.open = false;
        self.rect = None;
        self.message = None;
        self.config = None;
    }

    pub fn toggle_pin(&mut self) {
        self.pinned = !self.pinned;
    }

    pub fn observe_selection(&mut self, current: &Selection) {
        if self.open && !self.pinned && self.selection.as_ref() != Some(current) {
            self.close();
        }
    }
}

/// Left-aligned popover placement using the mini toolbar's above-first, clamped policy.
pub fn place(selection: Rect, viewport: Rect) -> Option<Rect> {
    if !selection.is_finite() || !viewport.is_finite() || !viewport.is_positive() {
        return None;
    }
    let x = selection.min.x.clamp(viewport.left(), (viewport.right() - POPOVER_W).max(viewport.left()));
    let above = selection.min.y - VGAP - POPOVER_H;
    let below = selection.min.y + VGAP;
    let above_fits = above >= viewport.top() && above + POPOVER_H <= viewport.bottom();
    let below_fits = below >= viewport.top() && below + POPOVER_H <= viewport.bottom();
    let y = if above_fits {
        above
    } else if below_fits {
        below
    } else {
        let desired = if above < viewport.top() { below } else { above };
        desired.clamp(viewport.top(), (viewport.bottom() - POPOVER_H).max(viewport.top()))
    };
    Some(Rect::from_min_size(pos2(x, y), POPOVER_SIZE))
}

fn positioned_id() -> egui::Id {
    egui::Id::new("selection-ai-popover").with("positioned-task")
}

/// Show the current task's projection. No editor command, text edit, adoption, diff, or retry
/// control exists on this surface.
pub fn show(app: &mut WordApp, ctx: &egui::Context) {
    if !app.ai.popover.open {
        return;
    }
    app.escape_free = false;
    if ctx.input(|input| input.key_pressed(egui::Key::Escape)) {
        app.ai.popover.close();
        return;
    }
    if app.ai.popover.config.is_some() {
        show_config(app, ctx);
        return;
    }

    let crate::ai::Ai { tasks, popover, .. } = &mut app.ai;
    let Some(task_id) = popover.task else {
        popover.close();
        return;
    };
    let Some(task) = tasks.current.as_ref().filter(|task| task.id == task_id) else {
        popover.close();
        return;
    };
    let result = &task.result;

    let tokens = Tokens::get(ctx);
    let fresh_position = ctx.data(|data| data.get_temp::<u32>(positioned_id())) != Some(task_id);
    let fallback = ctx.content_rect();
    let fallback_pos = pos2((fallback.right() - POPOVER_W - 24.0).max(fallback.left()), (fallback.top() + 24.0).min(fallback.bottom()));
    let initial = popover.rect.map_or(fallback_pos, |rect| rect.min);
    // The window title names the operation; the corner grip resizes down to the approved floor.
    let mut window = egui::Window::new(tl!(task.action.label()))
        .id(egui::Id::new("selection-ai-popover"))
        .collapsible(false)
        .resizable(true)
        .default_size(POPOVER_SIZE)
        .min_size(POPOVER_MIN_SIZE)
        .constrain_to(fallback);
    // Apply a computed position only on the task's first shown frame. Thereafter egui owns drag
    // movement, so neither scrolling nor selection changes can snap the window back to an anchor.
    window = if fresh_position { window.current_pos(initial) } else { window.default_pos(initial) };

    let mut close = false;
    let mut stop_streaming = false;
    let mut handoff: Option<(&'static str, String, String)> = None;
    let is_streaming = result.phase == AiResultPhase::Streaming;
    let project_enabled = app.project_workspace.is_enabled();
    let shown = crate::window_close::window(ctx, window).show(ctx, |ui| {
        ui.set_width(ui.available_width());
        source_view(ui, &tokens, &popover.source);
        ui.add_space(6.0);
        result_view(ui, &tokens, result);
        ui.add_space(6.0);
        ui.horizontal(|ui| {
            let can_copy = !result.text.is_empty() && !matches!(result.phase, AiResultPhase::Failed(_));
            let font = egui::TextStyle::Body.resolve(ui.style());
            if is_streaming {
                let stop = ui
                    .button(egui::RichText::new("停止").color(tokens.danger).font(font.clone()))
                    .on_hover_text("停止接收后续内容，保留已生成的文字");
                if stop.clicked() {
                    stop_streaming = true;
                }
            }
            let copy = ui.add_enabled_ui(can_copy, |ui| crate::widgets::icon_text_button(ui, "copy", tl!("Copy"), font.clone())).inner;
            if copy.clicked() {
                ctx.copy_text(result.text.clone());
                popover.message = Some("已复制结果；正文未修改。".into());
            }
            if project_enabled {
                let to_chat = ui
                    .add_enabled_ui(can_copy, |ui| {
                        ui.button(egui::RichText::new("转入问答").font(font.clone()))
                            .on_hover_text("带入原选文与当前结果到右侧项目问答输入框，不自动发送")
                    })
                    .inner;
                if to_chat.clicked() {
                    handoff = Some((task.action.label(), popover.source.clone(), result.text.clone()));
                    popover.message = Some("已带入右侧问答输入框，可补充问题后发送。".into());
                }
            }
            let pin = crate::widgets::icon_text_button(ui, "pin", tl!("Pin"), font.clone());
            if pin.clicked() {
                popover.toggle_pin();
            }
            if crate::widgets::icon_text_button(ui, "close", tl!("Close"), font).clicked() {
                close = true;
            }
            if popover.pinned {
                ui.label(egui::RichText::new("已固定").color(tokens.text_dim).small());
            }
            if let Some(message) = popover.message.as_deref() {
                ui.label(egui::RichText::new(message).color(tokens.text_dim).small());
            }
        });
    });

    if let Some(shown) = shown {
        popover.rect = Some(shown.response.rect);
        ctx.data_mut(|data| data.insert_temp(positioned_id(), task_id));
        close |= crate::window_close::show(ctx, &shown.response, &tokens);
    }
    if stop_streaming {
        if app.ai.cancel_current() {
            app.ai.popover.message = Some("已停止生成。".into());
        }
    }
    if close {
        app.ai.popover.close();
    }
    if let Some((action_label, source_text, result_text)) = handoff {
        app.project_workspace.handoff_selection_result(action_label, &source_text, &result_text);
    }
}

/// The rewrite configuration surface (approved plan B, task §8): frozen selection, this task's
/// explicitly checked sources (Q&A selector style, visible and removable, at most 4), and the
/// explicit 开始改写 / 取消 pair. Nothing here sends until 开始改写.
fn show_config(app: &mut WordApp, ctx: &egui::Context) {
    // The frozen selection belongs to one document; switching documents retires the
    // configuration instead of letting it send against the new document.
    if app.ai.popover.document != app.session.document_id() {
        crate::ai::cancel_rewrite_config(app);
        app.status(tl!("The document was switched; the rewrite configuration was closed.").to_string());
        return;
    }
    let tokens = Tokens::get(ctx);
    // Key the first-frame positioning by the configuration's own identity: a new configuration
    // always anchors once at its selection, never inheriting the previous window's position.
    let position_key = format!("{:?}|{}|{}", app.ai.popover.selection, app.ai.popover.source, app.ai.popover.document);
    let fresh_position = ctx.data(|data| data.get_temp::<String>(positioned_config_id())).as_deref() != Some(position_key.as_str());
    let fallback = ctx.content_rect();
    let fallback_pos = pos2((fallback.right() - POPOVER_W - 24.0).max(fallback.left()), (fallback.top() + 24.0).min(fallback.bottom()));
    let initial = app.ai.popover.rect.map_or(fallback_pos, |rect| rect.min);
    let mut window = egui::Window::new(tl!(AiAction::Rewrite.label()))
        .id(egui::Id::new("selection-ai-popover"))
        .collapsible(false)
        .resizable(true)
        .default_size(POPOVER_SIZE)
        .min_size(POPOVER_MIN_SIZE)
        .constrain_to(fallback);
    window = if fresh_position { window.current_pos(initial) } else { window.default_pos(initial) };

    let mut begin = false;
    let mut cancel = false;
    let shown = crate::window_close::window(ctx, window).show(ctx, |ui| {
        ui.set_width(ui.available_width());
        let source = app.ai.popover.source.clone();
        source_view(ui, &tokens, &source);
        ui.add_space(6.0);
        config_sources_view(app, ui, &tokens);
        ui.add_space(6.0);
        if let Some(config) = &mut app.ai.popover.config {
            ui.label(egui::RichText::new("改写要求（可选，留空按默认改写）").color(tokens.text_dim).small());
            ui.add_space(2.0);
            ui.add(
                egui::TextEdit::singleline(&mut config.custom_instruction)
                    .id_salt("rewrite-custom-instruction")
                    .hint_text("例如：更学术 / 压缩到 150 字 / 答辩口吻")
                    .char_limit(300)
                    .desired_width(f32::INFINITY),
            );
        }
        ui.add_space(6.0);
        ui.horizontal(|ui| {
            let t = &tokens;
            let start = ui
                .add(
                    egui::Button::new(egui::RichText::new(tl!("Start rewrite")).color(t.on_accent).font(crate::theme::medium(14.0)))
                        .fill(t.accent)
                        .corner_radius(crate::theme::radius::PANEL)
                        .min_size(vec2(88.0, 28.0)),
                )
                .on_hover_text(tl!("Send with the checked sources; cleared after sending"));
            if start.clicked() {
                begin = true;
            }
            if ui
                .add(egui::Button::new(egui::RichText::new(tl!("Cancel")).color(t.text).font(crate::theme::medium(14.0))).min_size(vec2(64.0, 28.0)))
                .clicked()
            {
                cancel = true;
            }
        });
    });

    if let Some(shown) = shown {
        app.ai.popover.rect = Some(shown.response.rect);
        ctx.data_mut(|data| data.insert_temp(positioned_config_id(), position_key));
        cancel |= crate::window_close::show(ctx, &shown.response, &tokens);
    }
    if begin {
        let _ = crate::ai::begin_rewrite(app);
        ctx.request_repaint();
    } else if cancel {
        crate::ai::cancel_rewrite_config(app);
    }
}

fn positioned_config_id() -> egui::Id {
    egui::Id::new("selection-ai-popover").with("positioned-config")
}

/// Checked sources as removable chips plus the project-source picker, both in the Q&A panel's
/// visual language. Selection changes only the local configuration state — no host round trip.
fn config_sources_view(app: &mut WordApp, ui: &mut Ui, tokens: &Tokens) {
    let t = tokens;
    let catalog = app.project_workspace.source_catalog();
    let max_reached = app.ai.popover.config.as_ref().is_some_and(|config| config.sources.len() >= 4);
    ui.label(egui::RichText::new(tl!("Sources for this rewrite (up to 4, optional)")).color(t.text_dim).small());
    ui.add_space(2.0);
    let mut remove_at: Option<usize> = None;
    if let Some(config) = &app.ai.popover.config {
        if config.sources.is_empty() {
            ui.label(egui::RichText::new(tl!("No sources selected; rewrites the selection only")).color(t.text_dim).small());
        }
    }
    let sources = app.ai.popover.config.as_ref().map(|config| config.sources.clone()).unwrap_or_default();
    for (index, source) in sources.iter().enumerate() {
        let chip_width = ui.available_width();
        ui.allocate_ui_with_layout(vec2(chip_width, 28.0), egui::Layout::top_down(egui::Align::LEFT), |ui| {
            egui::Frame::NONE
                .fill(t.checked)
                .corner_radius(crate::theme::radius::PANEL)
                .inner_margin(egui::Margin::symmetric(crate::theme::space::S as i8, 0))
                .show(ui, |ui| {
                    ui.horizontal(|ui| {
                        ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                            let remove =
                                ui.add(egui::Button::new(egui::RichText::new(tl!("Remove")).small().color(t.danger)).min_size(vec2(42.0, 28.0)));
                            if remove.on_hover_text(tl!("Remove from this rewrite")).clicked() {
                                remove_at = Some(index);
                            }
                            ui.add_sized(
                                vec2(ui.available_width().max(1.0), 28.0),
                                egui::Label::new(egui::RichText::new(format!("{} · {}", source.title, source.version)).small().color(t.accent_text))
                                    .truncate(),
                            );
                        });
                    });
                });
        });
    }
    if let Some(index) = remove_at {
        if let Some(config) = &mut app.ai.popover.config {
            config.sources.remove(index);
        }
    }
    if !sources.is_empty() {
        ui.add_space(crate::theme::space::S);
    }
    let menu = ui.menu_button(egui::RichText::new(format!("＋ {}", tl!("Select sources"))).font(crate::theme::medium(13.0)), |ui| {
        ui.set_max_width(300.0);
        ui.label(tl!("Project sources · check to add to this rewrite"));
        if catalog.is_empty() {
            ui.label(tl!("No project sources yet; import one from the sources panel"));
        }
        egui::ScrollArea::vertical().max_height(200.0).show(ui, |ui| {
            for (source_ref, searchable) in &catalog {
                let checked = sources.iter().any(|s| s.id == source_ref.id && s.version == source_ref.version);
                let label = if !searchable {
                    crate::i18n::fmt(tl!("{title} · read-only, not available for rewriting yet"), &[("title", &source_ref.title)])
                } else if checked {
                    format!("✓ {}", source_ref.title)
                } else {
                    source_ref.title.clone()
                };
                let row = ui.add_enabled(!checked && *searchable && !max_reached, egui::Button::new(label).wrap());
                if row.on_hover_text(format!("{} · {}", source_ref.title, source_ref.version)).clicked() {
                    if let Some(config) = &mut app.ai.popover.config {
                        config.sources.push(source_ref.clone());
                    }
                    ui.close();
                }
            }
        });
        ui.label(egui::RichText::new(tl!("Only for this rewrite; check sources again after sending")).small().weak());
    });
    menu.response.on_hover_text(tl!("Add a source (up to 4)"));
}

fn source_view(ui: &mut egui::Ui, tokens: &Tokens, source: &str) {
    ui.label(egui::RichText::new("仅处理原选文 · 上下文：当前文档").color(tokens.text_dim).small());
    ui.label(egui::RichText::new("原选文（只读）").color(tokens.text_dim).small());
    egui::Frame::new().fill(tokens.canvas).corner_radius(egui::CornerRadius::same(4)).inner_margin(egui::Margin::same(6)).show(ui, |ui| {
        egui::ScrollArea::vertical().id_salt("selection-ai-source").max_height(SOURCE_H).auto_shrink([false, true]).show(ui, |ui| {
            ui.set_width(ui.available_width());
            ui.add(egui::Label::new(egui::RichText::new(source).color(tokens.text_dim)).wrap());
        });
    });
}

fn result_view(ui: &mut egui::Ui, tokens: &Tokens, result: &AiResult) {
    match &result.phase {
        AiResultPhase::Streaming => {
            ui.label(egui::RichText::new("生成中…").color(tokens.accent));
        }
        AiResultPhase::Complete => {
            ui.label(egui::RichText::new("生成完成").color(tokens.text_dim).small());
        }
        AiResultPhase::Unknown(reason) => {
            ui.label(egui::RichText::new(format!("结果未知／未完成：{reason}")).color(tokens.red));
            ui.label(egui::RichText::new("以下为已收到的部分结果。").color(tokens.text_dim).small());
        }
        AiResultPhase::Failed(reason) => {
            ui.label(egui::RichText::new(format!("生成失败：{reason}")).color(tokens.red));
        }
    }
    if result.finish == AiFinish::Length {
        ui.label(egui::RichText::new("已达到输出上限，可能不完整").color(tokens.red).small());
    }

    ui.add_space(4.0);
    egui::ScrollArea::vertical().id_salt("selection-ai-result").auto_shrink([false, false]).show(ui, |ui| {
        ui.set_width(ui.available_width());
        // Read-only but selectable: the result may be quoted, never adopted.
        ui.add(egui::Label::new(&result.text).wrap().selectable(true));
        ui.add_space(6.0);
        network_facts(ui, tokens, result);
    });
}

/// Network facts as weak chips (same language as the Q&A panel's banner), not bare labels.
fn network_facts(ui: &mut egui::Ui, tokens: &Tokens, result: &AiResult) {
    if result.tools.is_empty() && result.sources.is_empty() {
        fact_chip(ui, tokens, "本次未联网");
        return;
    }
    for tool in &result.tools {
        let fact = match tool {
            AiTool::WebSearch => "进行了网页搜索",
            AiTool::UrlRead => "读取了 URL",
        };
        fact_chip(ui, tokens, fact);
    }
    for source in &result.sources {
        match source {
            AiSource::Web { url, title } => {
                if !title.is_empty() {
                    fact_chip(ui, tokens, title);
                }
                fact_chip(ui, tokens, url);
            }
            AiSource::Url { url } => {
                fact_chip(ui, tokens, url);
            }
        }
    }
}

fn fact_chip(ui: &mut egui::Ui, tokens: &Tokens, text: &str) {
    egui::Frame::NONE
        .fill(tokens.input)
        .corner_radius(crate::theme::radius::PANEL)
        .inner_margin(egui::Margin::symmetric(crate::theme::space::S as i8, 2))
        .show(ui, |ui| {
            ui.label(egui::RichText::new(text).small().color(tokens.text_dim));
        });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn popover_placement_is_left_aligned_and_clamped() {
        let viewport = Rect::from_min_size(pos2(100.0, 50.0), Vec2::new(1200.0, 700.0));
        // POPOVER_H is 440×460 (task §8), so the selection must sit below 50+8+460 to fit above.
        let selection = Rect::from_min_size(pos2(500.0, 560.0), Vec2::new(100.0, 20.0));
        let placed = place(selection, viewport).unwrap_or(Rect::EVERYTHING);
        assert_eq!(placed.min.x, selection.min.x);
        assert!(placed.max.y < selection.min.y);
        assert!(viewport.contains_rect(placed));

        let near_top = Rect::from_min_size(pos2(1290.0, 60.0), Vec2::new(100.0, 20.0));
        let flipped = place(near_top, viewport).unwrap_or(Rect::EVERYTHING);
        assert!(flipped.min.y > near_top.min.y);
        assert!(flipped.max.x <= viewport.max.x);
    }

    #[test]
    fn popover_rejects_non_finite_geometry() {
        let viewport = Rect::from_min_size(pos2(0.0, 0.0), Vec2::new(800.0, 600.0));
        assert_eq!(place(Rect::EVERYTHING, viewport), None);
        let selection = Rect::from_min_size(pos2(20.0, 20.0), Vec2::new(30.0, 20.0));
        assert_eq!(place(selection, Rect::NOTHING), None);
    }

    #[test]
    fn close_clears_only_visibility_and_position() {
        let mut state = AiPopoverState::default();
        state.open(9, 4, Selection::default(), "frozen".into(), Some(Rect::from_min_size(pos2(1.0, 2.0), POPOVER_SIZE)));
        state.toggle_pin();
        state.message = Some("copied".into());
        state.close();
        assert!(!state.open);
        assert_eq!(state.rect, None);
        assert_eq!(state.task, Some(9));
        assert_eq!(state.source, "frozen");
        assert_eq!(state.message, None);
    }
}
