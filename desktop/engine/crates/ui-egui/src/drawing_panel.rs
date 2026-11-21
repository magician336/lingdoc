//! Entry to the existing LingDoc drawing workbench and its native insertion confirmation bar.
//! There is deliberately no native candidate/generation floating window.

use serde_json::{Value, json};

use crate::WordApp;
use crate::theme::{Tokens, medium, semibold};

#[derive(Default)]
pub struct DrawingPanel {
    starting: bool,
    ready: bool,
    url: String,
    error: String,
    picker: Value,
    picker_busy: bool,
    actions: Vec<Value>,
    open_rect: Option<egui::Rect>,
    confirm_rect: Option<egui::Rect>,
    cancel_rect: Option<egui::Rect>,
}

impl DrawingPanel {
    pub fn open(&mut self) {
        self.error.clear();
        self.enqueue(json!({"kind":"openWorkbench"}));
    }

    pub fn set_open_rect(&mut self, rect: egui::Rect) { self.open_rect = Some(rect); }

    pub fn take_actions(&mut self) -> Vec<Value> { std::mem::take(&mut self.actions) }

    pub fn hydrate(&mut self, value: Value) -> Result<(), String> {
        if !value.is_object() { return Err("drawing snapshot must be an object".into()); }
        self.starting = value.get("workbenchStarting").and_then(Value::as_bool).unwrap_or(false);
        self.ready = value.get("workbenchReady").and_then(Value::as_bool).unwrap_or(false);
        self.url = value.get("workbenchUrl").and_then(Value::as_str).unwrap_or_default().into();
        self.error = value.get("error").and_then(Value::as_str).unwrap_or_default().into();
        self.picker = value.get("picker").cloned().unwrap_or(Value::Null);
        self.picker_busy = false;
        Ok(())
    }

    pub fn status(&self) -> Value {
        let rect = |rect: Option<egui::Rect>| rect.map(|r| json!({"x":r.left(),"y":r.top(),"width":r.width(),"height":r.height()}));
        json!({"workbenchStarting":self.starting,"workbenchReady":self.ready,"workbenchUrl":self.url,
            "error":self.error,"picker":self.picker,"pendingActions":self.actions.len(),
            "openRect":rect(self.open_rect),"confirmRect":rect(self.confirm_rect),"cancelRect":rect(self.cancel_rect)})
    }

    fn enqueue(&mut self, action: Value) {
        if self.actions.len() < 16 { self.actions.push(action); }
        else { self.error = "操作仍在处理中，请稍后再试。".into(); }
    }

    fn picker_bar(&mut self, ctx: &egui::Context) {
        if !self.picker.is_object() { self.confirm_rect = None; self.cancel_rect = None; return; }
        let tokens = Tokens::get(ctx);
        let title = self.picker.get("title").and_then(Value::as_str).unwrap_or("图稿").to_owned();
        let waiting = self.picker.get("waiting").and_then(Value::as_bool).unwrap_or(false);
        let mut confirm = false;
        let mut cancel = false;
        egui::Area::new(egui::Id::new("lingdoc-native-drawing-picker"))
            .order(egui::Order::Foreground)
            .anchor(egui::Align2::CENTER_TOP, egui::vec2(0.0, 104.0))
            .show(ctx, |ui| {
                egui::Frame::window(ui.style()).show(ui, |ui| {
                    ui.set_min_width(560.0);
                    ui.set_max_width(760.0);
                    ui.label(egui::RichText::new(format!("插入“{title}”")).font(semibold(15.0)));
                    ui.label(egui::RichText::new("第 1 步：在正文单击要插图的位置\n第 2 步：点击下方“插入到这里”").font(medium(13.0)));
                    ui.add_space(8.0);
                    ui.horizontal_wrapped(|ui| {
                        let ok = ui.add_enabled(!self.picker_busy, egui::Button::new(egui::RichText::new(if waiting { "重试插入到这里" } else { "插入到这里" }).font(semibold(13.0))));
                        self.confirm_rect = Some(ok.rect);
                        if ok.clicked() { confirm = true; }
                        let no = ui.add_enabled(!self.picker_busy && !waiting, egui::Button::new("取消"));
                        self.cancel_rect = Some(no.rect);
                        if no.clicked() { cancel = true; }
                    });
                    if !self.error.is_empty() {
                        ui.add_space(6.0);
                        ui.colored_label(tokens.danger, egui::RichText::new(&self.error).font(medium(12.0)));
                    }
                });
            });
        if confirm {
            self.picker_busy = true;
            self.enqueue(json!({"kind":"confirmPick"}));
        }
        if cancel { self.enqueue(json!({"kind":"cancelPick"})); }
    }
}

pub fn show(app: &mut WordApp, ctx: &egui::Context) { app.drawing.picker_bar(ctx); }

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn entry_opens_old_workbench_without_a_native_candidate_window() {
        let mut panel = DrawingPanel::default();
        panel.open();
        assert_eq!(panel.take_actions(), vec![json!({"kind":"openWorkbench"})]);
        panel.hydrate(json!({"workbenchReady":true,"workbenchUrl":"http://127.0.0.1:1234/native-reading/drawing"})).unwrap();
        assert_eq!(panel.status()["workbenchReady"], true);
    }

    #[test]
    fn picker_is_explicitly_confirmed() {
        let mut panel = DrawingPanel::default();
        panel.hydrate(json!({"picker":{"id":"p1","title":"流程图","waiting":false}})).unwrap();
        assert_eq!(panel.status()["picker"]["title"], "流程图");
        panel.enqueue(json!({"kind":"confirmPick"}));
        assert_eq!(panel.take_actions(), vec![json!({"kind":"confirmPick"})]);
    }
}
