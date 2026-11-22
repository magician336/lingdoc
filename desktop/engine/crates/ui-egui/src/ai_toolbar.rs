//! The five-action selection AI toolbar.
//!
//! Geometry and layering mirror `mini_toolbar`, but this is an independent surface: it neither
//! changes formatting nor adds controls to the existing formatting toolbar.

use egui::{Align, CornerRadius, Layout, Order, Rect, Vec2, pos2};
use wordcraft_engine::Selection;

use crate::WordApp;
use crate::ai::AiAction;
use crate::theme::Tokens;

const VGAP: f32 = 8.0;
const ANCHOR_INSET: f32 = 4.0;
const PAD_X: f32 = 4.0;
const PAD_Y: f32 = 4.0;
const ROW_H: f32 = 26.0;
const ACTION_W: f32 = 68.0;
const SPACING: f32 = 2.0;

pub const BAR_W: f32 = PAD_X * 2.0 + ACTION_W * 5.0 + SPACING * 4.0;
pub const BAR_H: f32 = PAD_Y * 2.0 + ROW_H;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct AiToolbarState {
    /// Outside-the-document dismissal is remembered only until the selection changes.
    dismissed: Option<Selection>,
}

impl AiToolbarState {
    pub fn is_dismissed(&self) -> bool {
        self.dismissed.is_some()
    }
}

/// Parameterized copy of the mini toolbar's proven above/below and clamping policy. Keeping the
/// dimensions local avoids changing that existing surface or its tests.
pub fn place(selection: Rect, viewport: Rect) -> Option<Rect> {
    if !selection.is_finite() || !viewport.is_finite() || !viewport.is_positive() {
        return None;
    }
    let cx = (selection.min.x + ANCHOR_INSET).clamp(viewport.left(), viewport.right());
    let x = (cx - BAR_W / 2.0).clamp(viewport.left(), (viewport.right() - BAR_W).max(viewport.left()));
    let above = selection.min.y - VGAP - BAR_H;
    let below = selection.min.y + VGAP;
    let above_fits = above >= viewport.top() && above + BAR_H <= viewport.bottom();
    let below_fits = below >= viewport.top() && below + BAR_H <= viewport.bottom();
    let y = if above_fits {
        above
    } else if below_fits {
        below
    } else {
        let desired = if above < viewport.top() { below } else { above };
        desired.clamp(viewport.top(), (viewport.bottom() - BAR_H).max(viewport.top()))
    };
    Some(Rect::from_min_size(pos2(x, y), Vec2::new(BAR_W, BAR_H)))
}

pub fn shown_id() -> egui::Id {
    egui::Id::new("wc_ai_toolbar").with("shown")
}

pub fn visible(app: &WordApp) -> bool {
    if app.services.ai_generate.is_none() || app.session.view.read_mode {
        return false;
    }
    if app.dialog.is_some() || app.colors.is_some() || app.ui.backstage {
        return false;
    }
    if app.canvas.context_menu_open || app.canvas.drag_selecting() || app.canvas.table_drag.is_some() {
        return false;
    }
    if app.ui.keytips != crate::keytips::Phase::Off || app.session.cell_range().is_some() {
        return false;
    }
    if app.source_workspace.source_active() || app.project_workspace.auxiliary_active() {
        return false;
    }
    if app.ai.toolbar.dismissed.as_ref() == Some(&app.session.sel) {
        return false;
    }
    !app.session.sel.is_collapsed()
}

/// Mirror the formatting mini toolbar's outside-document dismissal without sharing its state.
pub fn track_dismissal(app: &mut WordApp, ctx: &egui::Context, viewport: Rect) {
    if app.ai.toolbar.dismissed.as_ref().is_some_and(|old| *old != app.session.sel) {
        app.ai.toolbar.dismissed = None;
    }
    let Some(pointer) = ctx.input(|input| if input.pointer.any_pressed() { input.pointer.press_origin() } else { None }) else {
        return;
    };
    // Foreground menus and the AI result window own their presses.
    if ctx.layer_id_at(pointer).is_some_and(|layer| layer.order >= Order::Foreground) {
        return;
    }
    app.ai.toolbar.dismissed = if viewport.contains(pointer) { None } else { Some(app.session.sel.clone()) };
}

/// Show over the document's Middle layer, so a button press never reaches the page underneath.
pub fn show(app: &mut WordApp, ctx: &egui::Context, selection: Rect, viewport: Rect) {
    track_dismissal(app, ctx, viewport);
    if !visible(app) {
        return;
    }
    let Some(bar) = place(selection, viewport) else { return };
    let shown = bar.intersect(viewport);
    if !shown.is_positive() {
        return;
    }
    let tokens = Tokens::get(ctx);
    let mut clicked = None;
    let area = egui::Area::new(egui::Id::new("wc_ai_toolbar")).order(Order::Middle).interactable(true);
    area.constrain_to(viewport).constrain(false).fixed_pos(bar.min).show(ctx, |ui| {
        ui.set_clip_rect(shown);
        let painter = ui.painter();
        painter.rect_filled(bar.translate(Vec2::new(0.0, 2.0)), CornerRadius::same(6), tokens.page_shadow);
        painter.rect_filled(bar, CornerRadius::same(6), tokens.menu);
        painter.rect_stroke(bar, CornerRadius::same(6), egui::Stroke::new(1.0, tokens.border_strong), egui::StrokeKind::Inside);
        let inner = bar.shrink2(Vec2::new(PAD_X, PAD_Y));
        let mut child = ui.new_child(egui::UiBuilder::new().max_rect(inner).layout(Layout::left_to_right(Align::Center)));
        child.spacing_mut().item_spacing = Vec2::new(SPACING, 0.0);
        for action in AiAction::ALL {
            let button = child.add_sized([ACTION_W, ROW_H], egui::Button::new(tl!(action.label())));
            if button.clicked() {
                clicked = Some(action);
            }
        }
        let used = child.min_rect();
        ctx.data_mut(|data| data.insert_temp(shown_id(), [bar, used]));
    });
    if let Some(action) = clicked {
        // Plan B (task §8): only Rewrite opens the configure surface; the other four send as-is.
        if action == AiAction::Rewrite {
            let _ = crate::ai::open_rewrite_config(app);
        } else {
            let _ = crate::ai::start(app, action);
        }
        ctx.request_repaint();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn viewport() -> Rect {
        Rect::from_min_size(pos2(100.0, 50.0), Vec2::new(1200.0, 700.0))
    }

    #[test]
    fn toolbar_flips_and_stays_inside_the_viewport() {
        let selection = Rect::from_min_size(pos2(400.0, 400.0), Vec2::new(100.0, 20.0));
        let above = place(selection, viewport()).unwrap_or(Rect::EVERYTHING);
        assert!(above.max.y < selection.min.y);
        assert!(viewport().contains_rect(above));

        let top = Rect::from_min_size(pos2(110.0, 55.0), Vec2::new(100.0, 20.0));
        let below = place(top, viewport()).unwrap_or(Rect::EVERYTHING);
        assert!(below.min.y > top.min.y);
        assert!(viewport().contains_rect(below));

        let right = Rect::from_min_size(pos2(1290.0, 400.0), Vec2::new(100.0, 20.0));
        let clamped = place(right, viewport()).unwrap_or(Rect::EVERYTHING);
        assert!(clamped.max.x <= viewport().max.x);
    }

    #[test]
    fn toolbar_rejects_degenerate_geometry() {
        let selection = Rect::from_min_size(pos2(f32::NAN, 10.0), Vec2::new(10.0, 10.0));
        assert_eq!(place(selection, viewport()), None);
        assert_eq!(place(Rect::EVERYTHING, viewport()), None);
        assert_eq!(place(Rect::from_min_size(pos2(10.0, 10.0), Vec2::new(10.0, 10.0)), Rect::NOTHING), None);
    }
}
