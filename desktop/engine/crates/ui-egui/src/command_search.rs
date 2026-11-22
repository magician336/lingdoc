//! Command-search activation and focus handoff to the standard ribbon controls.

use egui::Id;
use serde_json::json;

use crate::{WordApp, command_catalog, widgets};

fn target_id() -> Id {
    Id::new("command_search_target")
}

/// Focus an existing parameter menu once, without executing its command with missing input.
pub(crate) fn focus_menu(ctx: &egui::Context, id: &str, response: &egui::Response) -> bool {
    let matches = ctx.data(|d| d.get_temp::<String>(target_id()).is_some_and(|s| s == id));
    if matches {
        response.request_focus();
        response.scroll_to_me(Some(egui::Align::Center));
        ctx.data_mut(|d| d.remove::<String>(target_id()));
    }
    matches
}

fn needs_controls(app: &WordApp, id: &str) -> bool {
    if id == "ui.dialog" {
        return true;
    }
    app.session.registry.get(id).is_some_and(|s| {
        // Parameter descriptions already distinguish required keys ("key":) from optional
        // ones ("key"?:). Keep that registry as the source; no second parameter table.
        s.params.contains("\":")
    })
}

/// Open a searched command's existing controls instead of executing it with empty input.
pub(crate) fn activate(app: &mut WordApp, ctx: &egui::Context, id: &str) {
    if !widgets::enabled(app, id) {
        return;
    }
    if needs_controls(app, id) && crate::input_dialog(id, &json!({})).is_none() {
        if let Some(item) = command_catalog::catalog(&app.session).into_iter().find(|c| c.id == id) {
            app.ui.tab = item.category;
            app.ui.ribbon_collapsed = false;
            ctx.data_mut(|d| d.insert_temp(target_id(), id.to_string()));
        }
    } else {
        widgets::run_from_ribbon(app, id, json!({}));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn full_ribbon_search_opens_smart_art_controls_without_empty_parameters() {
        let mut app = WordApp::new(
            wordcraft_engine::Session::new(wordcraft_doc::Document::from_text("Example paragraph")),
            crate::Services::default(),
        );
        app.run("insert.smartArt", json!({"layout":"basicList"})).unwrap();
        let revision = app.session.rev();
        let ctx = egui::Context::default();
        for id in ["smartArt.layout", "smartArt.colors"] {
            activate(&mut app, &ctx, id);
            assert_eq!(app.ui.tab, "SmartArt Design");
            assert_eq!(app.session.rev(), revision);
            assert_eq!(ctx.data(|d| d.get_temp::<String>(target_id())).as_deref(), Some(id));
        }
    }
}
