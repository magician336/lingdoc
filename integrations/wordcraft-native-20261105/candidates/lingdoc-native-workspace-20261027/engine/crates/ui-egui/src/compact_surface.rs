//! LingDoc command surfaces; document commands keep their original engine and UI controls.
pub const TITLE_H: f32 = 42.0;
pub const COMMAND_H: f32 = 48.0;
pub const ALL_TOOLS_W: f32 = 96.0;

use egui::{Id, Ui};
use serde_json::json;
use crate::{WordApp, command_catalog, widgets};

pub fn all_tools_panel_height(ctx: &egui::Context) -> f32 {
    ((ctx.content_rect().height() - TITLE_H - COMMAND_H - 28.0).max(0.0) * 0.45).clamp(0.0, 320.0)
}

/// Consecutive formatting should not move the document after every command. Dialogs,
/// file transitions and one-shot insertion still leave the tools to their own surface.
pub(crate) fn keeps_all_tools_open(id: &str) -> bool {
    ["format.", "para.", "styles.", "layout."].iter().any(|prefix| id.starts_with(prefix))
        || matches!(id, "edit.undo" | "edit.redo" | "table.autofit" | "equation.convert" | "equation.inputFormat")
}

/// Card widths are layout hints only; the commands still come from the original groups.
pub(crate) fn all_tools_group_width(title: &str, available: f32) -> f32 {
    let preferred: f32 = match title {
        "Clipboard" => 144.0, "Font" => 370.0, "Paragraph" => 294.0, "Styles" => 340.0,
        "Editing" => 164.0, "Voice" => 90.0, "Editor" => 100.0,
        "Pages" => 130.0, "Tables" => 94.0, "Illustrations" => 280.0, "Links" => 164.0,
        "Comments" => 92.0, "Header & Footer" => 240.0, "Text" => 256.0, "Symbols" => 190.0,
        _ => 230.0,
    };
    preferred.min(if available.is_finite() { available.max(0.0) } else { preferred })
}

fn target_id() -> Id { Id::new("all_tools_target") }
fn active_id() -> Id { Id::new("all_tools_rendering") }
fn group_id() -> Id { Id::new("all_tools_group") }
pub(crate) fn rendering_all_tools(ctx: &egui::Context) -> bool {
    ctx.data(|d| d.get_temp::<bool>(active_id()).unwrap_or(false))
}
pub(crate) fn includes_group(ctx: &egui::Context, group: &str) -> bool {
    ctx.data(|d| d.get_temp::<String>(group_id()).is_none_or(|s| s == group))
}

/// Focus an existing parameter menu once, without executing its command with missing input.
pub(crate) fn focus_menu(ctx: &egui::Context, id: &str, response: &egui::Response) -> bool {
    let matches = rendering_all_tools(ctx) && ctx.data(|d| d.get_temp::<String>(target_id()).is_some_and(|s| s == id));
    if matches {
        response.request_focus();
        response.scroll_to_me(Some(egui::Align::Center));
        ctx.data_mut(|d| d.remove::<String>(target_id()));
    }
    matches
}

fn needs_controls(app: &WordApp, id: &str) -> bool {
    if id == "ui.dialog" { return true; }
    app.session.registry.get(id).is_some_and(|s| {
        // Parameter descriptions already distinguish required keys ("key":) from optional
        // ones ("key"?:). Keep that registry as the source; no second parameter table.
        s.params.contains("\":")
    })
}

pub(crate) fn activate_catalog_command(app: &mut WordApp, ctx: &egui::Context, id: &str) {
    if !widgets::enabled(app, id) { return; }
    if let Some(name) = crate::input_dialog(id, &json!({})) {
        app.dialog = crate::dialogs::Dialog::open(name, app);
        app.ui.all_tools = false;
    } else if needs_controls(app, id) {
        let item = command_catalog::catalog(&app.session).into_iter().find(|c| c.id == id);
        app.ui.all_tools = true;
        let group = if id == "ui.dialog" { Some("Links".into()) } else {
            item.as_ref().and_then(|i| i.location.split(" › ").nth(1)).map(|g| {
                if item.as_ref().is_some_and(|i| i.category == "Equation") && g == "Conversions" { "Tools".into() } else { g.to_string() }
            })
        };
        app.ui.all_tools_category = item.map(|i| i.category).unwrap_or_else(|| "Insert".into());
        ctx.data_mut(|d| { d.remove::<String>(group_id()); if let Some(group) = group { d.insert_temp(group_id(), group); } });
        ctx.data_mut(|d| d.insert_temp(target_id(), id.to_string()));
    } else {
        widgets::run_from_ribbon(app, id, json!({}));
    }
}

pub fn show_all_tools(app: &mut WordApp, ui: &mut Ui) {
    if !app.ui.all_tools { return; }
    let items = command_catalog::catalog(&app.session);
    let categories = command_catalog::categories(&items);
    if !categories.contains(&app.ui.all_tools_category) {
        app.ui.all_tools_category = categories.iter().find(|c| c.as_str() == "Home").or_else(|| categories.first()).cloned().unwrap_or_default();
    }
    let limit = all_tools_panel_height(ui.ctx());
    let max_body = (limit - 44.0).max(0.0);
    let category = app.ui.all_tools_category.clone();
    let layout_id = Id::new(("all_tools_content_height", &category));
    let body_height = ui.ctx().data(|d| d.get_temp::<f32>(layout_id)).unwrap_or(224.0).clamp(0.0, max_body);
    let height = (body_height + 44.0).min(limit);
    let t = crate::theme::Tokens::get(ui.ctx());
    egui::Panel::top("all_tools").exact_size(height)
        .frame(egui::Frame::NONE.fill(t.ribbon).inner_margin(8)).show(ui, |ui| {
            ui.horizontal(|ui| {
                ui.label(egui::RichText::new(format!("全部工具 · {}", tl!(&category))).strong());
                ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                    if ui.button("收起").clicked() { app.ui.all_tools = false; }
                });
            });
            ui.add_space(4.0);
            let mut measured = 0.0;
            if ui.available_width() < 640.0 {
                egui::ComboBox::from_id_salt("all_tools_category").selected_text(tl!(&app.ui.all_tools_category)).show_ui(ui, |ui| {
                    for name in &categories {
                        if ui.selectable_value(&mut app.ui.all_tools_category, name.clone(), tl!(name)).clicked() {
                            ui.ctx().data_mut(|d| { d.remove::<String>(target_id()); d.remove::<String>(group_id()); });
                        }
                    }
                });
                measured = all_tools_commands(app, ui, &items);
            } else {
                ui.horizontal_top(|ui| {
                    egui::ScrollArea::vertical().id_salt("all_tools_categories").max_height(body_height).show(ui, |ui| {
                        ui.vertical(|ui| {
                            ui.set_width(132.0);
                            for name in &categories {
                                if ui.selectable_value(&mut app.ui.all_tools_category, name.clone(), tl!(name)).clicked() {
                                    ui.ctx().data_mut(|d| { d.remove::<String>(target_id()); d.remove::<String>(group_id()); });
                                }
                            }
                        });
                    });
                    ui.separator();
                    ui.vertical(|ui| { measured = all_tools_commands(app, ui, &items); });
                });
            }
            if app.ui.all_tools_category == category {
                let measured = measured.clamp(0.0, max_body);
                ui.ctx().data_mut(|d| d.insert_temp(layout_id, measured));
                if (measured - body_height).abs() > 0.5 { ui.ctx().request_repaint(); }
            }
        });
}

fn all_tools_commands(app: &mut WordApp, ui: &mut Ui, items: &[command_catalog::CommandItem]) -> f32 {
    let category = app.ui.all_tools_category.clone();
    let output = egui::ScrollArea::vertical().id_salt(("all_tools_commands", &category)).max_height(ui.available_height())
        .auto_shrink([false, true]).show(ui, |ui| {
            ui.ctx().data_mut(|d| d.insert_temp(active_id(), true));
            if ui.ctx().data(|d| d.get_temp::<String>(group_id()).is_some()) && ui.button(tl!("Show All")).clicked() {
                ui.ctx().data_mut(|d| d.remove::<String>(group_id()));
            }
            ui.add_enabled_ui(app.has_document() || matches!(category.as_str(), "Help" | "File" | "LingDoc"), |ui| {
                ui.spacing_mut().item_spacing = egui::vec2(8.0, 8.0);
                ui.with_layout(egui::Layout::left_to_right(egui::Align::Min).with_main_wrap(true), |ui| {
                    crate::ribbon::show_category(app, ui, &category);
                });
            });
            ui.ctx().data_mut(|d| d.insert_temp(active_id(), false));
            ui.separator();
            // Complete directory, including commands outside the old ribbon tabs. Parameter
            // choices are made in the original controls above, never by guessing empty input.
            ui.collapsing("当前分类的全部命令", |ui| {
                for item in items.iter().filter(|i| i.category == category) {
                    let label = format!("{} — {}", tl!(item.label), crate::i18n::location(item.location));
                    if ui.add_enabled(widgets::enabled(app, item.id), egui::Button::new(label).wrap()).clicked() {
                        activate_catalog_command(app, ui.ctx(), item.id);
                    }
                }
            });
        });
    output.content_size.y
}

pub(crate) fn toggle_all_tools(app: &mut WordApp, ctx: &egui::Context) {
    app.ui.all_tools = !app.ui.all_tools;
    app.ui.all_tools_from_keyboard = false;
    ctx.data_mut(|d| { d.remove::<String>(target_id()); d.remove::<String>(group_id()); });
}

/// Normal editing mode's single row. Reading retains its existing, smaller toolbar.
pub fn show(app: &mut WordApp, ui: &mut Ui) {
    if app.session.view.read_mode { crate::ribbon::show(app, ui); return; }
    use crate::compact_fit::CompactGroup;
    use crate::ribbon_fit::{FULL, COLLAPSED};
    let groups = [
        CompactGroup { id: "clipboard", widths: [94.0, 66.0, 38.0], required: true },
        CompactGroup { id: "styles", widths: [134.0, 104.0, 36.0], required: false },
        CompactGroup { id: "font", widths: [292.0, 238.0, 92.0], required: true },
        CompactGroup { id: "paragraph", widths: [266.0, 144.0, 36.0], required: false },
        CompactGroup { id: "insert", widths: [114.0, 36.0, 36.0], required: false },
        CompactGroup { id: "overflow", widths: [28.0; 3], required: true },
        CompactGroup { id: "all-tools", widths: [ALL_TOOLS_W; 3], required: true },
    ];
    let t = crate::theme::Tokens::get(ui.ctx());
    egui::Panel::top("compact_commands").exact_size(COMMAND_H)
        .frame(egui::Frame::NONE.fill(t.ribbon).inner_margin(egui::Margin { left: 8, right: 8, top: 8, bottom: 8 }))
        .show(ui, |ui| {
            let plan = crate::compact_fit::plan(&groups, ui.available_width(), false);
            ui.horizontal_centered(|ui| {
                ui.spacing_mut().item_spacing = egui::vec2(8.0, 0.0);
                ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                    if ui.add_sized([ALL_TOOLS_W, 32.0], egui::Button::new("全部工具").selected(app.ui.all_tools)).clicked() {
                        toggle_all_tools(app, ui.ctx());
                    }
                    let more = ui.add_sized([28.0, 32.0], egui::Button::new("")).on_hover_text("更多常用工具");
                    crate::icons::paint(ui.painter(), egui::Rect::from_center_size(more.rect.center(), egui::vec2(16.0, 16.0)), "more", t.icon, t.accent);
                    more.widget_info(|| egui::WidgetInfo::labeled(egui::WidgetType::Button, app.has_document(), "更多常用工具"));
                    egui::Popup::menu(&more).show(|ui| {
                        ui.add_enabled_ui(app.has_document(), |ui| {
                            for id in ["clipboard", "styles", "font", "paragraph", "insert"] {
                                ui.push_id(id, |ui| { ui.horizontal(|ui| high_frequency_group(app, ui, id, FULL)); });
                            }
                        });
                    });
                    if plan.essential_menu { return; }
                    ui.with_layout(egui::Layout::left_to_right(egui::Align::Center), |ui| {
                        ui.add_enabled_ui(app.has_document(), |ui| {
                            for (g, &level) in groups.iter().take(5).zip(&plan.levels) {
                                let width = g.widths.get(usize::from(level)).copied().unwrap_or(0.0);
                                ui.allocate_ui_with_layout(egui::vec2(width, 32.0), egui::Layout::left_to_right(egui::Align::Center), |ui| {
                                    ui.spacing_mut().item_spacing = egui::vec2(2.0, 0.0);
                                    ui.push_id(g.id, |ui| {
                                        if level == COLLAPSED && !matches!(g.id, "font" | "clipboard") {
                                            let (icon, tip) = group_label(g.id);
                                            widgets::menu_button(ui, app, icon, None, tip, false, "", |ui, app| {
                                                ui.horizontal(|ui| high_frequency_group(app, ui, g.id, FULL));
                                            });
                                        } else { high_frequency_group(app, ui, g.id, level); }
                                    });
                                });
                            }
                        });
                    });
                });
            });
        });
    show_all_tools(app, ui);
}

fn group_label(id: &str) -> (&'static str, &'static str) {
    match id {
        "styles" => ("style", "Styles"),
        "paragraph" => ("paragraph", "Paragraph"),
        "insert" => ("table", "Insert"),
        _ => ("more", "More"),
    }
}

fn high_frequency_group(app: &mut WordApp, ui: &mut Ui, id: &str, level: u8) {
    use crate::ribbon_fit::{FULL, COLLAPSED};
    match id {
        "clipboard" => {
            widgets::split(ui, app, "paste", "Paste", "edit.paste", json!({}), false, None, crate::ribbon::paste_menu);
            if level < COLLAPSED { widgets::small(ui, app, "copy", None, "Copy", "edit.copy", json!({}), false); }
            if level == FULL { widgets::small(ui, app, "cut", None, "Cut", "edit.cut", json!({}), false); }
        }
        "styles" => style_choice(app, ui, if level == FULL { 126.0 } else { 96.0 }),
        "font" => {
            let state = app.session.run("format.state", &json!({})).unwrap_or_default();
            crate::ribbon::font_boxes(app, ui, &state, if level == COLLAPSED { None } else { Some(126.0) });
            let flag = |k| state.get(k).and_then(serde_json::Value::as_bool).unwrap_or(false);
            widgets::small(ui, app, "bold", None, "Bold", "format.bold", json!({}), flag("bold"));
            if level == FULL {
                widgets::small(ui, app, "italic", None, "Italic", "format.italic", json!({}), flag("italic"));
                widgets::split(ui, app, "underline", "Underline", "format.underline", json!({}), flag("underline"), None, crate::ribbon::underline_menu);
            }
        }
        "paragraph" => paragraph_controls(app, ui, level == FULL),
        "insert" => {
            widgets::menu_button(ui, app, "table", None, "Add a Table", false, "insert.table", crate::ribbon::insert_table_menu);
            widgets::small(ui, app, "picture", None, "Pictures", "insert.picture", json!({}), false);
            widgets::small(ui, app, "equation", None, "Equation", "insert.equation", json!({}), false);
            widgets::small(ui, app, "link", None, "Link", "ui.dialog", json!({"name": "link"}), false);
        }
        _ => {}
    }
}

fn style_choice(app: &mut WordApp, ui: &mut Ui, width: f32) {
    let state = app.session.run("format.state", &json!({})).unwrap_or_default();
    let current = state.get("style").and_then(serde_json::Value::as_str).unwrap_or("Normal");
    let styles: Vec<_> = app.session.doc.styles.gallery().iter().map(|s| (s.id.clone(), crate::i18n::style_name(s))).collect();
    let label = styles.iter().find(|(id, _)| id == current).map(|(_, name)| name.as_str()).unwrap_or(current);
    let font = crate::theme::TypeRung::Control.regular();
    let label = crate::theme::fit_text(ui.painter(), label, &font, 0.0, crate::theme::Tokens::get(ui.ctx()).text, width - 30.0);
    let r = egui::ComboBox::from_id_salt("compact_style").width(width).selected_text(egui::RichText::new(label).font(font.clone())).show_ui(ui, |ui| {
        for (id, name) in &styles {
            if ui.selectable_label(id == current, egui::RichText::new(name).font(font.clone())).clicked() { widgets::run_from_ribbon(app, "para.style", json!({"style": id})); ui.close(); }
        }
    }).response;
    r.widget_info(|| egui::WidgetInfo::labeled(egui::WidgetType::ComboBox, ui.is_enabled(), tl!("Styles")));
    r.on_hover_text(tl!("Apply Style"));
}

fn align_controls(app: &mut WordApp, ui: &mut Ui) {
    use wordcraft_doc::Align;
    let align = app.session.doc.para_at(&app.session.sel.focus).map(|p| app.session.doc.styles.resolve_para(&p.props))
        .map(|p| p.align.visual(p.bidi)).unwrap_or_default();
    for (icon, tip, id, value) in [
        ("alignLeft", "Align Left", "para.alignLeft", Align::Left),
        ("alignCenter", "Center", "para.alignCenter", Align::Center),
        ("alignRight", "Align Right", "para.alignRight", Align::Right),
        ("justify", "Justify", "para.justify", Align::Justify),
    ] { widgets::small(ui, app, icon, None, tip, id, json!({}), align == value); }
}

fn paragraph_controls(app: &mut WordApp, ui: &mut Ui, full: bool) {
    if full { align_controls(app, ui); } else {
        widgets::menu_button(ui, app, "alignLeft", None, "Alignment", false, "", |ui, app| { ui.horizontal(|ui| align_controls(app, ui)); });
    }
    let para = app.session.doc.para_at(&app.session.sel.focus).map(|p| app.session.doc.styles.resolve_para(&p.props));
    let lists = wordcraft_engine::cmd::para::list_buttons(&app.session.doc, para.as_ref().and_then(|p| p.numbering));
    widgets::small(ui, app, "bullets", None, "Bullets", "para.bullets", json!({}), lists.bullets);
    widgets::small(ui, app, "numbering", None, "Numbering", "para.numbering", json!({}), lists.numbering);
    widgets::menu_button(ui, app, "lineSpacing", None, "Line and Paragraph Spacing", false, "para.lineSpacing", crate::ribbon::line_spacing_menu);
    if full {
        widgets::small(ui, app, "outdent", None, "Decrease Indent", "para.outdent", json!({}), false);
        widgets::small(ui, app, "indent", None, "Increase Indent", "para.indent", json!({}), false);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use egui_kittest::kittest::{NodeT, Queryable};

    fn app() -> WordApp {
        let mut app = WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::from_text("Example paragraph")), crate::Services::default());
        app.ui.language = "en".into();
        app.ui.all_tools = true;
        app.ui.all_tools_category = "Home".into();
        app
    }

    fn harness(app: WordApp) -> egui_kittest::Harness<'static, WordApp> {
        let mut fonts = false;
        let mut h = egui_kittest::Harness::builder().with_size(egui::vec2(1440.0, 900.0)).build_ui_state(move |ui, app: &mut WordApp| {
            crate::i18n::set_current(crate::i18n::Lang::EN);
            if !fonts { crate::theme::install_fonts_for(ui.ctx(), false); fonts = true; return; }
            show_all_tools(app, ui);
            let ctx = ui.ctx().clone();
            crate::dialogs::show(app, &ctx);
        }, app);
        h.run_steps(4);
        h
    }

    #[test]
    fn all_tools_categories_disabled_commands_and_transient_state() {
        let mut h = harness(app());
        for category in ["References", "Zotero", "Home"] {
            h.get_by_label(category).scroll_to_me(); h.run_steps(3);
            h.get_by_label(category).click(); h.run_steps(3);
            assert_eq!(h.state().ui.all_tools_category, category);
        }
        h.state_mut().session.dirty = false;
        h.state_mut().run("file.close", json!({})).unwrap();
        let before = serde_json::to_value(&h.state().session.doc).unwrap();
        h.state_mut().ui.all_tools = true;
        h.run_steps(3);
        assert!(h.get_by_label("Bold").accesskit_node().is_disabled());
        let ctx = h.ctx.clone();
        activate_catalog_command(h.state_mut(), &ctx, "format.bold");
        assert_eq!(serde_json::to_value(&h.state().session.doc).unwrap(), before);
        let state = serde_json::to_value(&h.state().ui).unwrap();
        for key in ["allTools", "allToolsCategory", "allToolsFromKeyboard"] { assert!(state.get(key).is_none()); }
        let restored: crate::UiState = serde_json::from_value(state).unwrap();
        assert!(!restored.all_tools && !restored.all_tools_from_keyboard && restored.all_tools_category.is_empty());
    }

    #[test]
    fn all_tools_parameterized_commands_reuse_their_existing_actions() {
        for (id, setup, button) in [
            ("table.autofit", Some(("insert.table", json!({"rows": 2, "cols": 2}))), "Fixed Column Width"),
            ("equation.convert", Some(("insert.equation", json!({"linear": "x^2"}))), "Current - Linear"),
            ("para.style", None, "Heading 1"),
            ("ui.dialog", None, "Link"),
        ] {
            let mut a = app();
            if let Some((command, params)) = setup { a.run(command, params).unwrap(); }
            let before = serde_json::to_value(&a.session.doc).unwrap();
            let rev = a.session.rev();
            let mut h = harness(a);
            let ctx = h.ctx.clone();
            let status = h.state().status_msg.clone();
            activate_catalog_command(h.state_mut(), &ctx, id);
            h.run_steps(4);
            assert_eq!(h.state().session.rev(), rev, "{id}: opening choices must not dispatch empty input");
            assert_eq!(serde_json::to_value(&h.state().session.doc).unwrap(), before);
            assert_eq!(h.state().status_msg, status, "{id}: no error from missing parameters");
            if button == "Link" { h.get_by_role_and_label(egui::accesskit::Role::Button, button).click(); } else { h.get_by_label_contains(button).click(); }
            h.run_steps(3);
            if id == "ui.dialog" {
                assert_eq!(h.state().dialog.as_ref().map(|d| d.name()), Some("link"));
            } else {
                assert!(h.state().session.rev() > rev, "{id}: actual parameterized action executes");
                assert_ne!(serde_json::to_value(&h.state().session.doc).unwrap(), before, "{id}");
                h.state_mut().run("edit.undo", json!({})).unwrap();
                assert_eq!(serde_json::to_value(&h.state().session.doc).unwrap(), before, "{id}: undo");
            }
            assert_eq!(h.state().ui.all_tools, id != "ui.dialog", "{id}: formatting stays open; independent dialog closes the pane");
        }
    }

    #[test]
    fn all_tools_reflows_groups_and_keeps_insert_controls_on_screen() {
        for (width, height) in [(1440.0, 900.0), (1024.0, 768.0)] {
            let mut a = app();
            a.test_lingdoc_shell = true;
            let mut h = egui_kittest::Harness::builder().with_size(egui::vec2(width, height))
                .build_ui_state(|ui, a: &mut WordApp| { a.logic(ui.ctx()); a.ui(ui); }, a);
            for category in ["Home", "Insert", "LingDoc"] {
                h.state_mut().ui.all_tools = true;
                h.state_mut().ui.all_tools_category = category.into();
                h.run_steps(6);
                let panel = egui::containers::panel::PanelState::load(&h.ctx, Id::new("all_tools")).unwrap().outer_rect;
                let title = egui::containers::panel::PanelState::load(&h.ctx, Id::new("title_bar")).unwrap().outer_rect;
                let commands = egui::containers::panel::PanelState::load(&h.ctx, Id::new("compact_commands")).unwrap().outer_rect;
                assert_eq!(title.height() + commands.height(), 90.0);
                assert!(panel.height() <= 320.0, "{width} {category}: {panel:?}");
                assert!(panel.left() >= 0.0 && panel.right() <= width, "panel must fit viewport: {panel:?}");
                if category == "Home" {
                    let rect = h.query_all(egui_kittest::kittest::By::new().role(egui::accesskit::Role::Label).value("Voice"))
                        .next().expect("Voice group heading").rect();
                    assert!(panel.contains_rect(rect) && rect.bottom() + widgets::CONTENT_H <= panel.bottom(),
                        "{width}: last Home groups must wrap inside panel: {rect:?}");
                }
                if category == "Insert" {
                    for label in ["Header", "Footer", "Page Number", "Symbol"] {
                        let rect = h.get_by_label(label).rect();
                        assert!(panel.contains_rect(rect), "{width} {label}: {rect:?} outside {panel:?}");
                    }
                    let table = h.get_all_by_label("Add a Table").map(|n| n.rect())
                        .find(|rect| panel.contains_rect(*rect)).expect("table control in all tools");
                    let header = h.get_by_label("Header").rect();
                    assert!(table.top() < panel.top() + 130.0 && header.top() < panel.top() + 260.0);
                }
                if category == "LingDoc" { assert!(panel.height() < 220.0, "short categories shrink: {panel:?}"); }
                println!("ALL_TOOLS width={width} category={category} height={}", panel.height());
            }
        }
    }

    #[test]
    fn all_tools_supports_consecutive_formatting_and_closes_for_insertion() {
        let mut h = harness(app());
        let before = serde_json::to_value(&h.state().session.doc).unwrap();
        h.get_by_label("Bold").click(); h.run_steps(3);
        assert!(h.state().ui.all_tools);
        h.get_by_label("Italic").click(); h.run_steps(3);
        assert!(h.state().ui.all_tools);
        h.state_mut().run("edit.undo", json!({})).unwrap();
        h.state_mut().run("edit.undo", json!({})).unwrap();
        assert!(h.state().ui.all_tools);
        assert_eq!(serde_json::to_value(&h.state().session.doc).unwrap(), before);
        h.state_mut().run("insert.table", json!({"rows": 2, "cols": 2})).unwrap();
        h.run_steps(3);
        assert!(!h.state().ui.all_tools);
        h.state_mut().ui.all_tools = true;
        assert!(h.state_mut().run("para.style", json!({"style": "no-such-style"})).is_err());
        assert!(h.state().ui.all_tools, "failed commands must not close the panel");
        h.state_mut().run("ui.dialog", json!({"name": "link"})).unwrap();
        assert!(!h.state().ui.all_tools);
        assert_eq!(h.state().dialog.as_ref().map(|d| d.name()), Some("link"));
    }

    #[test]
    fn all_tools_height_is_bounded_and_narrow_height_uses_a_short_panel() {
        let height = |size| {
            let ctx = egui::Context::default();
            ctx.run_ui(egui::RawInput { screen_rect: Some(egui::Rect::from_min_size(egui::Pos2::ZERO, size)), ..Default::default() }, |_| {}).drop_without_applying_deltas();
            all_tools_panel_height(&ctx)
        };
        let wide = height(egui::vec2(1440.0, 900.0));
        let short = height(egui::vec2(800.0, 600.0));
        assert!(wide > 0.0 && wide <= 320.0);
        assert!(short > 0.0 && short < wide);
        assert_eq!(height(egui::vec2(300.0, 100.0)), 0.0);
    }

    #[test]
    fn compact_style_combo_paints_control_font_for_selection_and_items() {
        let mut a = app();
        a.ui.all_tools = false;
        a.test_lingdoc_shell = true;
        let mut h = egui_kittest::Harness::builder().with_size(egui::vec2(1024.0, 768.0)).build_ui_state(|ui, a: &mut WordApp| {
            a.logic(ui.ctx());
            a.ui(ui);
        }, a);
        h.run_steps(5);
        let painted_sizes = |h: &egui_kittest::Harness<'_, WordApp>, label: &str| {
            h.output().shapes.iter().filter_map(|clipped| match &clipped.shape {
                egui::Shape::Text(text) if text.galley.job.text == label => Some(text.galley.job.sections.iter().map(|section| section.format.font_id.size).collect::<Vec<_>>()),
                _ => None,
            }).flatten().collect::<Vec<_>>()
        };
        let selected = painted_sizes(&h, "Normal");
        assert!(!selected.is_empty() && selected.iter().all(|size| *size == 14.0), "selected style must paint at Control 14: {selected:?}");
        h.get_by_label("Styles").click();
        h.run_steps(3);
        for label in ["Normal", "Heading 1"] {
            let sizes = painted_sizes(&h, label);
            assert!(!sizes.is_empty() && sizes.iter().all(|size| *size == 14.0), "{label} dropdown item must paint at Control 14: {sizes:?}");
        }
    }

    #[test]
    fn default_surface_uses_real_42_plus_48_panels_and_short_controls() {
        use egui::accesskit::Role;
        for width in [1920.0, 1024.0, 800.0] {
            let mut a = app();
            a.ui.all_tools = false;
            a.ui.language = "zh-CN".into();
            a.session.path = Some("很长的真实中文文档标题用于确认标题省略与保存状态并列显示.docx".into());
            a.test_lingdoc_shell = true;
            let mut h = egui_kittest::Harness::builder().with_size(egui::vec2(width, 900.0)).build_ui_state(|ui, a: &mut WordApp| {
                a.logic(ui.ctx()); a.ui(ui);
            }, a);
            h.run_steps(5);
            let title = egui::containers::panel::PanelState::load(&h.ctx, Id::new("title_bar")).unwrap().outer_rect;
            let command = egui::containers::panel::PanelState::load(&h.ctx, Id::new("compact_commands")).map(|s| s.outer_rect);
            assert_eq!(title.height(), TITLE_H, "{width}: title");
            let command = command.expect("compact command panel is installed");
            assert_eq!(command.height(), COMMAND_H, "{width}: commands");
            let controls: Vec<_> = [Role::Button, Role::ComboBox, Role::TextInput].into_iter()
                .flat_map(|r| h.query_all_by_role(r)).filter(|n| command.contains(n.rect().center())).map(|n| n.rect()).collect();
            assert!(!controls.is_empty());
            for rect in &controls {
                assert!(rect.height() > 0.0 && rect.height() <= 40.0, "{width}: {rect:?}");
                assert!(rect.left() >= 0.0 && rect.right() <= width, "{width}: {rect:?}");
            }
            println!("LAYOUT width={width} title={} command={} maxControl={} controls={}", title.height(), command.height(), controls.iter().map(|r| r.height()).fold(0.0, f32::max), controls.len());
            assert!(h.query_by_label("全部工具").is_some());
            for absent in ["收起功能区", "展开功能区", "Home"] { assert!(h.query_by_label(absent).is_none(), "{absent}"); }
            for id in ["file.save", "edit.undo", "edit.redo"] { assert!(h.ctx.read_response(crate::chrome::qat_button_id(id)).is_some()); }
            h.get_by_label("全部工具").click(); h.run_steps(3);
            assert!(h.state().ui.all_tools);
            h.get_by_label("全部工具").click(); h.run_steps(3);
            assert!(!h.state().ui.all_tools);
        }
    }
}
