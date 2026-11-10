//! Isolated same-origin host bridge. Commands run against the canvas's actual WordApp.
//! Saving is two phase: serialization never clears dirty; verified host receipt does.
use std::cell::RefCell;
use std::collections::BTreeMap;
use js_sys::{Function, Promise};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use wasm_bindgen::prelude::*;
use wasm_bindgen::JsCast as _;
use wordcraft_ui_egui::WordApp;
use wordcraft_doc::props::Underline;
use wordcraft_doc::{Block, CharProps, Document, ParaProps, Paragraph, Path, Pos, StoryRef, StyleSheet, Table, TextColor};

struct Request { json: String, resolve: Function }
#[derive(Default)]
struct Bridge {
    queue: Vec<Request>, ctx: Option<egui::Context>, pending: Option<(u64, u64, String, String, u64)>, serial: u64,
    operations: BTreeMap<String, (Value, Value)>,
    diagram_document: Option<u64>, diagram_operations: BTreeMap<String, (String, Value)>,
    standalone_saves: BTreeMap<u64, (u64, u64)>,
}
thread_local! { static BRIDGE: RefCell<Bridge> = RefCell::new(Bridge::default()); }

/// Local-host font bytes use a typed array, not the document JSON request queue.
#[wasm_bindgen]
pub fn host_install_font(name: &str, bytes: &[u8]) -> Result<String, JsValue> {
    let fonts: Value = serde_json::from_str(include_str!("host-fonts.json"))
        .map_err(|_| JsValue::from_str("invalid fixed host font manifest"))?;
    let expected = fonts.as_array().and_then(|fonts| fonts.iter().find(|font| font["name"].as_str() == Some(name)))
        .ok_or_else(|| JsValue::from_str("unsupported host font"))?;
    if bytes.len() > 32 * 1024 * 1024 || expected["bytes"].as_u64() != Some(bytes.len() as u64) {
        return Err(JsValue::from_str("unsupported host font or size"));
    }
    let digest = Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect::<String>();
    if expected["sha256"].as_str() != Some(digest.as_str()) {
        return Err(JsValue::from_str("host font digest mismatch"));
    }
    let db = wordcraft_fonts::FontDb::global();
    db.install_font(name, bytes).map_err(|error| JsValue::from_str(&error.to_string()))?;
    let missing: Vec<char> = "灵档文件开始插入设计布局引用邮件审阅视图中文输入候选窗".chars()
        .filter(|ch| db.fallback_for(*ch, u32::MAX).is_none()).collect();
    let (catalogs, _) = wordcraft_ui_egui::i18n::Lang::cjk_font_text_for(true, false);
    let catalog_chars: std::collections::BTreeSet<char> = catalogs.iter().flat_map(|text| text.chars())
        .filter(|ch| wordcraft_fonts::is_cjk(*ch)).collect();
    let catalog_missing: Vec<char> = catalog_chars.iter().copied().filter(|ch| db.fallback_for(*ch, u32::MAX).is_none()).collect();
    BRIDGE.with(|cell| { if let Some(ctx) = &cell.borrow().ctx { ctx.request_repaint(); } });
    Ok(json!({"name":name,"sha256":digest,"bytes":bytes.len(),"generation":db.generation(),"sampleMissing":missing,"catalogCjkCharacters":catalog_chars.len(),"catalogMissing":catalog_missing}).to_string())
}

#[wasm_bindgen]
pub fn host_request(request: &str) -> Promise {
    let input = request.to_string();
    Promise::new(&mut |resolve, _reject| {
        BRIDGE.with(|cell| {
            let mut bridge = cell.borrow_mut();
            if input.len() > 8 * 1024 * 1024 || bridge.queue.len() >= 16 {
                let _ = resolve.call1(&JsValue::NULL, &JsValue::from_str(r#"{"ok":false,"error":"host request limit"}"#));
                return;
            }
            bridge.queue.push(Request { json: input.clone(), resolve });
            if let Some(ctx) = &bridge.ctx { ctx.request_repaint(); }
        });
    })
}

fn identity(app: &WordApp) -> Value {
    json!({"documentId": app.session.document_id(), "revision": app.session.rev(), "dirty": app.session.dirty})
}

fn check(app: &WordApp, request: &Value) -> Result<(), String> {
    if request.get("expectedDocumentId").and_then(Value::as_u64) != Some(app.session.document_id())
        || request.get("expectedRevision").and_then(Value::as_u64) != Some(app.session.rev()) {
        return Err("stale document or revision".into());
    }
    Ok(())
}

/// Only an explicitly selected single image is an update target. A collapsed caret next to
/// an image is not enough: asynchronous work must never replace an implicit neighbour.
fn diagram_image(app: &WordApp) -> Result<Option<Value>, String> {
    let Some((pos, _)) = wordcraft_engine::cmd::objects::object_selection(&app.session) else { return Ok(None); };
    diagram_image_at(app, &pos)
}

fn diagram_image_at(app: &WordApp, pos: &Pos) -> Result<Option<Value>, String> {
    use wordcraft_doc::para::InlineObject;
    let Some(InlineObject::Image { media, alt, .. }) = app.session.doc.para_at(pos).and_then(|p| p.object_at(pos.off)) else { return Ok(None); };
    let bytes = app.session.doc.media.get(media).ok_or("selected image media missing")?;
    if bytes.len() > 4 * 1024 * 1024 { return Err("selected image exceeds diagram limit".into()); }
    let digest = Sha256::digest(bytes.as_slice()).iter().map(|b| format!("{b:02x}")).collect::<String>();
    Ok(Some(json!({"sha256":digest,"data":wordcraft_engine::cmd::insert::base64_encode(bytes.as_slice()),"alt":alt})))
}

pub fn diagram_edit_available() -> bool {
    web_sys::window().and_then(|window| js_sys::Reflect::get(&window, &JsValue::from_str("lingdocEditDiagram")).ok())
        .is_some_and(|value| value.is_function())
}

pub fn request_diagram_edit(app: &WordApp) -> Result<(), String> {
    let (pos, _) = wordcraft_engine::cmd::objects::object_selection(&app.session).ok_or("select a drawing first")?;
    if !wordcraft_engine::cmd::objects::is_lingdoc_diagram_at(&app.session, &pos) { return Err("selected picture has no drawing source".into()); }
    let image = diagram_image(app)?.ok_or("selected drawing is unavailable")?;
    let selection = wordcraft_engine::cmd::sel_result(&app.session).map_err(|e| e.to_string())?;
    let snapshot = json!({"state":identity(app),"selection":selection,"image":image}).to_string();
    let window = web_sys::window().ok_or("drawing host unavailable")?;
    let callback = js_sys::Reflect::get(&window, &JsValue::from_str("lingdocEditDiagram"))
        .map_err(|_| "drawing host unavailable")?.dyn_into::<Function>().map_err(|_| "drawing host unavailable")?;
    callback.call1(&window, &JsValue::from_str(&snapshot)).map_err(|_| "drawing window could not be opened")?;
    Ok(())
}

fn diagram_bookmark(r: &Value) -> Result<wordcraft_engine::Selection, String> {
    let bookmark = r.get("bookmark").ok_or("missing selection bookmark")?;
    let anchor = bookmark.get("anchor").and_then(|v| serde_json::from_value::<Pos>(v.clone()).ok()).ok_or("invalid bookmark anchor")?;
    let focus = bookmark.get("focus").and_then(|v| serde_json::from_value::<Pos>(v.clone()).ok()).ok_or("invalid bookmark focus")?;
    let selection = wordcraft_engine::Selection { anchor, focus };
    let (a, b) = selection.ordered();
    if a.story != b.story || a.path != b.path || a.off.checked_add(wordcraft_doc::para::OBJ.len_utf8()) != Some(b.off) {
        return Err("bookmark must select exactly one picture".into());
    }
    Ok(selection)
}

fn request_digest(r: &Value) -> String {
    Sha256::digest(r.to_string().as_bytes()).iter().map(|b| format!("{b:02x}")).collect()
}

#[cfg(test)]
mod diagram_tests {
    use super::*;
    use wordcraft_engine::Session;
    use wordcraft_ui_egui::Services;

    const PNG: &str = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==";

    #[test]
    fn insert_is_guarded_and_retries_are_idempotent() {
        let mut app = WordApp::new(Session::new(Document::new()), Services::default());
        let mut bridge = Bridge::default();
        let selected = execute(&mut app, &mut bridge, &json!({"op":"diagram.selection"})).unwrap();
        let request = json!({"op":"diagram.insert","operationId":"diagram-1",
            "expectedDocumentId":selected["state"]["documentId"], "expectedRevision":selected["state"]["revision"],
            "bookmark":selected["selection"], "data":PNG, "alt":"drawing"});
        let receipt = execute(&mut app, &mut bridge, &request).unwrap();
        assert_eq!(receipt["status"], "applied");
        let after = app.session.rev();
        assert_eq!(execute(&mut app, &mut bridge, &request).unwrap()["status"], "applied");
        assert_eq!(app.session.rev(), after);
        let mut changed = request.clone();
        changed["alt"] = json!("different");
        assert!(execute(&mut app, &mut bridge, &changed).is_err());
        changed["operationId"] = json!("diagram-2");
        assert!(execute(&mut app, &mut bridge, &changed).is_err());
        app.execute("edit.undo", json!({})).unwrap();
        assert!(app.session.doc.media.is_empty());
    }

    #[test]
    fn png_validation_rejects_bad_or_non_png_data() {
        assert!(diagram_png(&json!({"data":PNG})).is_ok());
        assert!(diagram_png(&json!({"data":"not base64"})).is_err());
        assert!(diagram_png(&json!({"data":"SGVsbG8="})).is_err());
    }

    #[test]
    fn replacement_requires_exact_picture_and_digest() {
        let mut app = WordApp::new(Session::new(Document::new()), Services::default());
        let mut bridge = Bridge::default();
        app.execute("insert.picture", json!({"data":PNG})).unwrap();
        app.session.sel = wordcraft_engine::Selection {
            anchor: Pos::body(0, 0), focus: Pos::body(0, wordcraft_doc::para::OBJ.len_utf8())
        };
        let selected = execute(&mut app, &mut bridge, &json!({"op":"diagram.selection"})).unwrap();
        let mut request = json!({"op":"diagram.replace","operationId":"replace-1",
            "expectedDocumentId":selected["state"]["documentId"], "expectedRevision":selected["state"]["revision"],
            "bookmark":selected["selection"], "expectedImageSha256":"wrong", "data":PNG});
        assert!(execute(&mut app, &mut bridge, &request).is_err());
        request["expectedImageSha256"] = selected["image"]["sha256"].clone();
        assert_eq!(execute(&mut app, &mut bridge, &request).unwrap()["status"], "applied");
    }

    #[test]
    fn guarded_replace_restores_bookmark_only_after_validation() {
        let mut app = WordApp::new(Session::new(Document::new()), Services::default());
        let mut bridge = Bridge::default();
        app.execute("insert.picture", json!({"data":PNG,"width":100})).unwrap();
        app.session.sel = wordcraft_engine::Selection {anchor:Pos::body(0,0),focus:Pos::body(0,wordcraft_doc::para::OBJ.len_utf8())};
        let snapshot = execute(&mut app, &mut bridge, &json!({"op":"diagram.selection"})).unwrap();
        let original_image = wordcraft_engine::cmd::objects::selected(&app.session).unwrap().1;
        app.session.sel = wordcraft_engine::Selection::caret(Pos::body(0,0));
        let previous = app.session.sel.clone();
        let mut request = json!({"op":"diagram.replace","operationId":"restore-test","restoreBookmark":true,
            "expectedDocumentId":snapshot["state"]["documentId"],"expectedRevision":snapshot["state"]["revision"],
            "bookmark":snapshot["selection"],"expectedImageSha256":"bad","data":PNG});
        assert!(execute(&mut app, &mut bridge, &request).is_err());
        assert_eq!(app.session.sel, previous);
        request["expectedImageSha256"] = snapshot["image"]["sha256"].clone();
        assert_eq!(execute(&mut app, &mut bridge, &request).unwrap()["status"], "applied");
        let updated = wordcraft_engine::cmd::objects::selected(&app.session).unwrap().1;
        assert_eq!(updated.frame(), original_image.frame());
    }

    #[test]
    fn diagram_receipts_have_separate_capacity_and_follow_the_document() {
        let mut app = WordApp::new(Session::new(Document::new()), Services::default());
        let mut bridge = Bridge::default();
        for n in 0..128 {
            bridge.operations.insert(format!("text-{n}"), (json!({"op":"replaceRange"}), json!({})));
        }
        let selected = execute(&mut app, &mut bridge, &json!({"op":"diagram.selection"})).unwrap();
        let mut request = json!({"op":"diagram.insert","operationId":"drawing",
            "expectedDocumentId":selected["state"]["documentId"], "expectedRevision":selected["state"]["revision"],
            "bookmark":selected["selection"], "data":PNG});
        execute(&mut app, &mut bridge, &request).unwrap();
        assert_eq!(bridge.diagram_operations.len(), 1);
        assert_eq!(bridge.operations.len(), 128);
        app.session.set_document(Document::new());
        let selected = execute(&mut app, &mut bridge, &json!({"op":"diagram.selection"})).unwrap();
        assert!(bridge.diagram_operations.is_empty());
        assert_eq!(bridge.operations.len(), 128);
        request["expectedDocumentId"] = selected["state"]["documentId"].clone();
        request["expectedRevision"] = selected["state"]["revision"].clone();
        request["bookmark"] = selected["selection"].clone();
        assert_eq!(execute(&mut app, &mut bridge, &request).unwrap()["status"], "applied");
    }
}

fn diagram_png(r: &Value) -> Result<&str, String> {
    let data = r.get("data").and_then(Value::as_str).ok_or("missing PNG data")?;
    if data.len() > 4 * 1024 * 1024 * 4 / 3 + 4 { return Err("diagram PNG exceeds limit".into()); }
    let bytes = wordcraft_engine::cmd::insert::base64_decode(data).ok_or("invalid PNG base64")?;
    if bytes.len() > 4 * 1024 * 1024 || !bytes.starts_with(b"\x89PNG\r\n\x1a\n") { return Err("diagram must be a bounded PNG".into()); }
    Ok(data)
}

fn save_nonce() -> Result<String, String> {
    let crypto = js_sys::Reflect::get(&js_sys::global(), &JsValue::from_str("crypto")).map_err(|_| "WebCrypto unavailable")?;
    let random = js_sys::Reflect::get(&crypto, &JsValue::from_str("randomUUID"))
        .map_err(|_| "randomUUID unavailable")?
        .dyn_into::<Function>().map_err(|_| "randomUUID unavailable")?;
    random.call0(&crypto).map_err(|_| "randomUUID failed")?.as_string().ok_or_else(|| "randomUUID returned no identity".into())
}

fn source_input(r: &Value) -> Result<wordcraft_ui_egui::source_workspace::SourceInput, String> {
    use wordcraft_ui_egui::source_workspace::SourceInput;
    let id = r.get("sourceId").and_then(Value::as_str).ok_or("missing sourceId")?.to_string();
    let title = r.get("title").and_then(Value::as_str).ok_or("missing title")?.to_string();
    let version = r.get("version").and_then(Value::as_str).ok_or("missing version")?.to_string();
    match r.get("op").and_then(Value::as_str) {
        Some("source.openText") => {
            let text = r.get("text").and_then(Value::as_str).ok_or("missing source text")?.to_string();
            Ok(SourceInput::Text { id, title, version, text })
        }
        Some("source.open") => {
            let raw = r.get("pages").and_then(Value::as_array).ok_or("missing pages")?;
            if raw.is_empty() || raw.len() > 32 { return Err("source page limit".into()); }
            let pages = raw.iter().map(|p| {
                let encoded = p.as_str().ok_or("invalid page")?;
                if encoded.len() > 4 * 1024 * 1024 { return Err("rendered page exceeds bridge limit"); }
                wordcraft_engine::cmd::insert::base64_decode(encoded).ok_or("invalid PNG base64")
            }).collect::<Result<Vec<_>, _>>()?;
            Ok(SourceInput::Pages { id, title, version, pages })
        }
        _ => Err("unsupported source payload".into()),
    }
}

fn execute(app: &mut WordApp, bridge: &mut Bridge, r: &Value) -> Result<Value, String> {
    let document = app.session.document_id();
    if bridge.diagram_document != Some(document) {
        bridge.diagram_document = Some(document);
        bridge.diagram_operations.clear();
    }
    let op = r.get("op").and_then(Value::as_str).ok_or("missing op")?;
    match op {
        "status" => Ok(identity(app)),
        "save.begin" => {
            if app.services.save_intent.is_some() { return Err("use the project save coordinator".into()); }
            check(app, r)?;
            if app.services.async_saves.is_none() { return Err("native save receipts unavailable".into()); }
            let result = app.execute("file.save", json!({}))?;
            let ticket = result.get("pendingSave").and_then(Value::as_u64).ok_or("native save did not provide a completion ticket")?;
            let state = identity(app);
            bridge.standalone_saves.insert(ticket, (app.session.document_id(), app.session.rev()));
            while bridge.standalone_saves.len() > 128 { bridge.standalone_saves.pop_first(); }
            Ok(json!({"ticket":ticket,"state":state}))
        }
        "save.status" => {
            use wordcraft_ui_egui::async_saves::SaveOutcome;
            let ticket = r.get("ticket").and_then(Value::as_u64).ok_or("missing save ticket")?;
            let &(document, revision) = bridge.standalone_saves.get(&ticket).ok_or("unknown save ticket")?;
            let current = identity(app);
            let receipt = app.services.async_saves.as_ref().ok_or("native save receipts unavailable")?.receipt(ticket);
            let Some((outcome, download)) = receipt else { return Ok(json!({"status":"pending","state":current})); };
            let status = match &outcome {
                SaveOutcome::Cancelled => "cancelled",
                SaveOutcome::Failed(_) => "failed",
                SaveOutcome::Saved if document != app.session.document_id() || revision != app.session.rev() || app.session.dirty => "stale",
                SaveOutcome::Saved if download => "downloaded",
                SaveOutcome::Saved => "saved",
            };
            let error = match outcome { SaveOutcome::Failed(error) => Some(error), _ => None };
            Ok(json!({"status":status,"state":current,"documentId":document,"revision":revision,"error":error}))
        }
        "workspace.home" => {
            let page = r.get("page").and_then(Value::as_str).unwrap_or("home");
            if !matches!(page, "home" | "projects" | "new" | "open") { return Err("invalid start page".into()); }
            app.project_workspace.release_focus();
            app.ui.backstage = true;
            app.ui.backstage_page = page.into();
            Ok(json!({"page":page}))
        }
        "workspace.editor" => {
            app.project_workspace.release_focus();
            app.ui.backstage = false;
            Ok(identity(app))
        }
        "standalone.new" => {
            if app.project_workspace.document_bound { return Err("ordinary documents require the standalone entry".into()); }
            app.project_workspace.release_focus();
            app.source_workspace.set_mode("body")?;
            app.run("file.new", r.get("params").cloned().unwrap_or_else(|| json!({})))
        }
        "source.begin" => {
            if r.get("expectedDocumentId").is_some_and(|v| v.as_u64() != Some(app.session.document_id())) { return Err("source document changed".into()); }
            if r.get("expectedViewEpoch").is_some_and(|v| v != &app.source_workspace.status()["viewEpoch"]) { return Err("source request superseded".into()); }
            app.source_workspace.begin_open();
            Ok(json!({"document":identity(app),"source":app.source_workspace.status()}))
        }
        "source.commit" => {
            if r.get("expectedDocumentId").and_then(Value::as_u64) != Some(app.session.document_id()) {
                return Err("source document changed".into());
            }
            let epoch = r.get("expectedViewEpoch").and_then(Value::as_u64).ok_or("missing source view epoch")?;
            if app.source_workspace.status()["viewEpoch"].as_u64() != Some(epoch) { return Err("source request superseded".into()); }
            let page = match r.get("page") {
                None | Some(Value::Null) => None,
                Some(value) => Some(value.as_u64().and_then(|n| usize::try_from(n).ok()).ok_or("invalid page")?),
            };
            let mode = r.get("mode").and_then(Value::as_str).ok_or("missing final source mode")?;
            let input = source_input(r.get("payload").ok_or("missing source payload")?)?;
            app.source_workspace.commit_open(epoch, input, mode, page)?;
            let source_id = app.source_workspace.status()["sourceId"].as_str().unwrap_or("").to_string();
            app.project_workspace.set_active_source(if mode == "body" { "" } else { &source_id });
            if app.source_workspace.source_active() {
                app.canvas.focused = false;
                app.canvas.want_focus = false;
                if let Some(ctx) = &bridge.ctx { wordcraft_ui_egui::source_workspace::prepare_frame(app, ctx); }
            }
            Ok(app.source_workspace.status())
        }
        "workspace.hydrate" => {
            app.project_workspace.hydrate(r.get("state").cloned().ok_or("missing workspace state")?)?;
            Ok(app.project_workspace.status())
        }
        "workspace.actions" => Ok(json!(app.project_workspace.take_actions())),
        "workspace.status" => Ok(app.project_workspace.status()),
        "workspace.layout" => {
            if let Some(open) = r.get("resourcesOpen").and_then(Value::as_bool) { app.project_workspace.resources_open = open; }
            if let Some(open) = r.get("chatOpen").and_then(Value::as_bool) { app.project_workspace.chat_open = open; }
            Ok(app.project_workspace.status())
        }
        "source.openText" => {
            let id = r.get("sourceId").and_then(Value::as_str).ok_or("missing sourceId")?;
            let title = r.get("title").and_then(Value::as_str).ok_or("missing title")?;
            let version = r.get("version").and_then(Value::as_str).ok_or("missing version")?;
            let text = r.get("text").and_then(Value::as_str).ok_or("missing source text")?;
            app.source_workspace.load_text(id.into(), title.into(), version.into(), text.into())?;
            Ok(app.source_workspace.status())
        }
        "source.open" => {
            let id = r.get("sourceId").and_then(Value::as_str).ok_or("missing sourceId")?;
            let title = r.get("title").and_then(Value::as_str).ok_or("missing title")?;
            let version = r.get("version").and_then(Value::as_str).ok_or("missing version")?;
            let raw = r.get("pages").and_then(Value::as_array).ok_or("missing pages")?;
            if raw.is_empty() || raw.len() > 32 { return Err("source page limit".into()); }
            let pages = raw.iter().map(|p| {
                let encoded = p.as_str().ok_or("invalid page")?;
                if encoded.len() > 4 * 1024 * 1024 { return Err("rendered page exceeds bridge limit"); }
                wordcraft_engine::cmd::insert::base64_decode(encoded).ok_or("invalid PNG base64")
            }).collect::<Result<Vec<_>, _>>()?;
            app.source_workspace.load_pages(id.into(), title.into(), version.into(), pages)?;
            Ok(app.source_workspace.status())
        }
        "source.mode" => {
            app.source_workspace.set_mode(r.get("mode").and_then(Value::as_str).ok_or("missing mode")?)?;
            let source_id = app.source_workspace.status()["sourceId"].as_str().unwrap_or("").to_string();
            app.project_workspace.set_active_source(if r.get("mode").and_then(Value::as_str) == Some("body") { "" } else { &source_id });
            if app.source_workspace.source_active() {
                app.canvas.focused = false;
                app.canvas.want_focus = false;
                if let Some(ctx) = &bridge.ctx {
                    wordcraft_ui_egui::source_workspace::prepare_frame(app, ctx);
                }
            } else {
                if r.get("mode").and_then(Value::as_str) == Some("body") {
                    app.project_workspace.release_focus();
                }
                app.canvas.want_focus = true;
            }
            Ok(app.source_workspace.status())
        }
        "source.status" => Ok(app.source_workspace.status()),
        "source.layout" => Ok(app.source_workspace.layout_snapshot()),
        "source.tabs" | "source.restore" => {
            if r.get("expectedDocumentId").is_some_and(|v| v.as_u64() != Some(app.session.document_id())) { return Err("source document changed".into()); }
            if r.get("expectedViewEpoch").is_some_and(|v| v != &app.source_workspace.status()["viewEpoch"]) { return Err("source request superseded".into()); }
            if op == "source.restore" {
                // Restore is asynchronous host work, so identity guards are mandatory.
                if r.get("expectedDocumentId").and_then(Value::as_u64).is_none() || r.get("expectedViewEpoch").and_then(Value::as_u64).is_none() { return Err("missing source restore identity".into()); }
                app.source_workspace.restore_layout(r.get("state").ok_or("missing source layout")?)?;
            } else {
                app.source_workspace.multi_action(r)?;
            }
            let status = app.source_workspace.status();
            let source_id = status["sourceId"].as_str().unwrap_or("");
            app.project_workspace.set_active_source(if status["mode"].as_str() == Some("body") { "" } else { source_id });
            if app.source_workspace.source_active() {
                app.canvas.focused = false;
                app.canvas.want_focus = false;
                if let Some(ctx) = &bridge.ctx { wordcraft_ui_egui::source_workspace::prepare_frame(app, ctx); }
            } else {
                app.project_workspace.release_focus();
                app.canvas.want_focus = true;
            }
            Ok(status)
        },
        "source.page" => {
            let current = app.source_workspace.status();
            if r.get("sourceId").is_some_and(|id| id != &current["sourceId"]) || r.get("version").is_some_and(|v| v != &current["version"]) {
                return Err("source identity or version changed".into());
            }
            let page = r.get("page").and_then(Value::as_u64).and_then(|n| usize::try_from(n).ok()).ok_or("invalid page")?;
            app.source_workspace.set_page(page)?;
            Ok(app.source_workspace.status())
        }
        "selection" => {
            let selection = app.execute("document.selection", json!({}))?;
            Ok(json!({"state": identity(app), "selection": selection}))
        }
        "diagram.selection" => {
            let selection = app.execute("document.selection", json!({}))?;
            Ok(json!({"state":identity(app), "selection":selection, "image":diagram_image(app)?}))
        }
        "diagram.insert" | "diagram.replace" => {
            let id = operation_id(r)?;
            if let Some((original, receipt)) = bridge.diagram_operations.get(id) {
                if original != &request_digest(r) { return Err("operationId already belongs to a different request".into()); }
                return Ok(operation_receipt(app, receipt));
            }
            if bridge.diagram_operations.len() >= 128 { return Err("diagram operation receipt limit reached; save and reopen before another task".into()); }
            check(app, r)?;
            let bookmark = r.get("bookmark").ok_or("missing selection bookmark")?;
            let selection = app.execute("document.selection", json!({}))?;
            let restore = op == "diagram.replace" && r.get("restoreBookmark").and_then(Value::as_bool) == Some(true);
            if bookmark.get("anchor").is_none() || bookmark.get("focus").is_none()
                || (!restore && (bookmark.get("anchor") != selection.get("anchor") || bookmark.get("focus") != selection.get("focus"))) {
                return Err("selection changed".into());
            }
            if op == "diagram.insert" && bookmark.get("anchor") != bookmark.get("focus") {
                return Err("select an insertion point first".into());
            }
            let target = if restore { Some(diagram_bookmark(r)?) } else { None };
            if op == "diagram.replace" {
                let image = if let Some(target) = &target { diagram_image_at(app, &target.ordered().0)? } else { diagram_image(app)? }
                    .ok_or("select the original picture first")?;
                if r.get("expectedImageSha256").and_then(Value::as_str) != image["sha256"].as_str() {
                    return Err("selected picture changed".into());
                }
            }
            let data = diagram_png(r)?;
            let alt = r.get("alt").and_then(Value::as_str).unwrap_or("");
            if alt.len() > 4096 { return Err("diagram alt text exceeds limit".into()); }
            let before = app.session.rev();
            let command = if op == "diagram.insert" { "insert.picture" } else { "picture.change" };
            let previous = app.session.sel.clone();
            if let Some(target) = target { app.session.sel = target; }
            let result = match app.execute(command, json!({"data":data,"alt":alt,"floating":op == "diagram.insert"})) {
                Ok(value) => value,
                Err(error) => { app.session.sel = previous; return Err(error); }
            };
            bridge.pending = None;
            let receipt = json!({"operationId":id,"status":"applied","documentId":app.session.document_id(),
                "beforeRevision":before,"afterRevision":app.session.rev(),"state":identity(app),"result":result});
            bridge.diagram_operations.insert(id.to_string(), (request_digest(r), receipt.clone()));
            Ok(operation_receipt(app, &receipt))
        }
        "inspect" => app.execute("document.inspect", json!({"text":true})),
        "layout" => app.execute("document.layout", json!({})),
        "page_png" => {
            let number = r.get("page").and_then(Value::as_u64).and_then(|p| usize::try_from(p).ok())
                .filter(|p| (1..=32).contains(p)).ok_or("page must be 1..32")?;
            let layout = app.session.export_layout();
            let page = layout.pages.get(number - 1).ok_or("page not present")?;
            if !page.w.is_finite() || !page.h.is_finite() || page.w <= 0.0 || page.h <= 0.0 || page.w > 1200.0 || page.h > 1800.0 { return Err("page exceeds diagnostic bounds".into()); }
            let image = wordcraft_engine::render::render_page(&app.session.doc, page, 2.0, &wordcraft_engine::render::RenderOptions::default());
            let bytes = image.to_png();
            if bytes.is_empty() || bytes.len() > 4 * 1024 * 1024 { return Err("diagnostic PNG failed or too large".into()); }
            let digest = Sha256::digest(&bytes).iter().map(|b| format!("{b:02x}")).collect::<String>();
            Ok(json!({"page":number,"scale":2.0,"width":image.width,"height":image.height,"sha256":digest,"bytes":bytes,
                "source":"actual WASM session export_layout and FontDb, no proofing; CPU print-page diagnostic, not a canvas GPU screenshot"}))
        }
        "geometry" => {
            use wordcraft_engine::layout::Placed;
            let output = r.get("output").and_then(Value::as_bool).unwrap_or(false);
            let layout = if output { app.session.export_layout() } else { app.session.layout() };
            if layout.pages.len() > 32 { return Err("geometry exceeds 32 page diagnostic limit".into()); }
            let mut rows = Vec::new();
            let mut hashes = std::collections::HashMap::new();
            for (page, p) in layout.pages.iter().enumerate() {
                for (area, items) in [("body", &p.items), ("header", &p.header), ("footer", &p.footer)] {
                    for item in items {
                        match item {
                            Placed::Lines { story, path, para, l0, l1, x, y, .. } => {
                                let text = app.session.doc.para(*story, path).map(|p| p.text.as_str()).unwrap_or("");
                                let styles: Vec<Value> = para.styles.iter().map(|s| {
                                    let hash = hashes.entry(s.face.id()).or_insert_with(|| Sha256::digest(s.face.data()).iter().map(|b| format!("{b:02x}")).collect::<String>());
                                    json!({"font":s.rc.font,"fontEa":s.rc.font_ea,"size":s.size,"ascent":s.ascent,"descent":s.descent,
                                        "face":s.face.family,"faceStyle":s.face.style,"faceIndex":s.face.index(),"faceSha256":hash,
                                        "upem":s.face.upem,"faceAscent":s.face.ascent,"faceDescent":s.face.descent,"fontBytes":s.face.data().len(),
                                        "spacing":s.rc.spacing,"scale":s.rc.scale,"bold":s.rc.bold,"italic":s.rc.italic,"synthBold":s.synth_bold,"synthItalic":s.synth_italic})
                                }).collect();
                                let selected = para.lines.get(*l0..*l1).ok_or("invalid internal line range")?;
                                let mut lines = Vec::new();
                                for l in selected {
                                    let clusters = para.clusters.get(l.c0..l.c1).ok_or("invalid internal cluster range")?;
                                    let clusters: Vec<Value> = clusters.iter().map(|c| json!({"text":text.get(c.start..c.end).unwrap_or(""),"adv":c.adv,
                                        "style":c.style,"kind":format!("{:?}",c.kind),"breakAfter":c.break_after})).collect();
                                    lines.push(json!({"top":l.top,"height":l.height,"baseline":l.baseline,"left":l.left,"right":l.right,"start":l.start,"stop":l.stop,
                                        "xs":l.xs,"text":text.get(l.start..l.stop).unwrap_or(""),"clusters":clusters}));
                                }
                                rows.push(json!({"page":page+1,"area":area,"story":format!("{story:?}"),"path":path.0,"x":x,"y":y,"text":text,
                                    "lineSpacing":format!("{:?}",para.rp.line_spacing),"styles":styles,"lines":lines}));
                            }
                            Placed::Cell { rect, covered, table, row, cell, clip, .. } => rows.push(json!({
                                "page":page+1,"area":area,"kind":"cell","table":table.0,"row":row,"cell":cell,"covered":covered,"clip":clip,
                                "rect":{"x":rect.x,"y":rect.y,"w":rect.w,"h":rect.h}
                            })),
                            _ => {}
                        }
                        if rows.len() > 4096 { return Err("geometry exceeds row diagnostic limit".into()); }
                    }
                }
            }
            Ok(json!({"state":identity(app),"layoutMode":if output {"exportPrint"} else {"canvasSession"},"pages":layout.pages.len(),"rows":rows}))
        }
        "open" => {
            if app.session.dirty { return Err("unsaved document: save before open".into()); }
            let data = r.get("data").and_then(Value::as_str).ok_or("missing base64 data")?;
            if data.len() > 6 * 1024 * 1024 { return Err("document exceeds bridge limit".into()); }
            // Explicit host readback is already scoped to its bound project. It is
            // distinct from the user's New/Open command that leaves that binding.
            let bound = app.project_workspace.document_bound;
            app.project_workspace.document_bound = false;
            let opened = app.execute("file.open", json!({"path":"host-document.docx", "data": data}));
            app.project_workspace.document_bound = bound;
            opened?;
            app.ui.backstage = false;
            bridge.pending = None;
            bridge.operations.clear();
            bridge.diagram_document = Some(app.session.document_id());
            bridge.diagram_operations.clear();
            Ok(identity(app))
        }
        "replaceRange" => {
            let id = operation_id(r)?;
            if r.to_string().len() > 200_000 { return Err("controlled replacement request exceeds limit".into()); }
            if let Some((original, receipt)) = bridge.operations.get(id) {
                if original != r { return Err("operationId already belongs to a different request".into()); }
                return Ok(operation_receipt(app, receipt));
            }
            if bridge.operations.len() >= 128 { return Err("operation receipt limit reached; save and reopen before another task".into()); }
            let before = app.session.rev();
            app.execute("text.replaceRange", r.clone())?;
            bridge.pending = None;
            let receipt = json!({"operationId":id,"status":"applied","documentId":app.session.document_id(),
                "beforeRevision":before,"afterRevision":app.session.rev(),"beforeText":r["expectedText"],"afterText":r["text"],"state":identity(app)});
            bridge.operations.insert(id.to_string(), (r.clone(), receipt.clone()));
            Ok(operation_receipt(app, &receipt))
        }
        "operationStatus" => {
            let id = operation_id(r)?;
            Ok(bridge.operations.get(id).map(|(_, receipt)| operation_receipt(app, receipt))
                .unwrap_or_else(|| json!({"operationId":id,"status":"not_found","currentState":identity(app),"matchesCurrentRevision":false})))
        }
        "undoOperation" | "redoOperation" => {
            let id = operation_id(r)?;
            let (_, receipt) = bridge.operations.get(id).ok_or("unknown operation; no change was made")?;
            let undo = op == "undoOperation";
            let required_status = if undo { "applied" } else { "undone" };
            if receipt.get("status").and_then(Value::as_str) != Some(required_status) {
                return Err("operation is not in the required undo/redo state".into());
            }
            if r.get("documentId").and_then(Value::as_u64) != Some(app.session.document_id())
                || receipt["state"]["documentId"].as_u64() != Some(app.session.document_id())
                || r.get("expectedRevision").and_then(Value::as_u64) != Some(app.session.rev())
                || receipt.get("afterRevision").and_then(Value::as_u64) != Some(app.session.rev()) {
                return Err("document changed after this operation; use native undo history".into());
            }
            app.execute(if undo { "edit.undo" } else { "edit.redo" }, json!({}))?;
            bridge.pending = None;
            let state = identity(app);
            let (_, receipt) = bridge.operations.get_mut(id).ok_or("operation receipt unavailable")?;
            receipt["status"] = json!(if undo { "undone" } else { "applied" });
            receipt["afterRevision"] = json!(app.session.rev());
            receipt["state"] = state;
            Ok(operation_receipt(app, receipt))
        }
        "select" => {
            check(app, r)?;
            let range = r.get("range").ok_or("missing range")?;
            app.execute("select.range", range.clone())?;
            Ok(identity(app))
        }
        "insert" => {
            check(app, r)?;
            let bookmark = r.get("bookmark").ok_or("missing selection bookmark")?;
            let current = app.execute("document.selection", json!({}))?;
            if bookmark.get("anchor") != current.get("anchor") || bookmark.get("focus") != current.get("focus") {
                return Err("selection changed".into());
            }
            let text = r.get("text").and_then(Value::as_str).ok_or("missing text")?;
            if text.len() > 65536 { return Err("insertion too large".into()); }
            app.execute("text.insert", json!({"text":text}))?;
            bridge.pending = None;
            Ok(identity(app))
        }
        "export" => {
            check(app, r)?;
            let bytes = wordcraft_engine::io::save_bytes("host-document.docx", &app.session.doc)?;
            if bytes.len() > 4 * 1024 * 1024 { return Err("serialized document exceeds bridge limit".into()); }
            let digest = Sha256::digest(&bytes).iter().map(|b| format!("{b:02x}")).collect::<String>();
            let disk_version = r.get("expectedDiskVersion").and_then(Value::as_u64).ok_or("missing expected disk version")?;
            bridge.serial = bridge.serial.wrapping_add(1);
            let token = format!("{}:{}:{}:{}", app.session.document_id(), app.session.rev(), bridge.serial, save_nonce()?);
            bridge.pending = Some((app.session.document_id(), app.session.rev(), token.clone(), digest.clone(), disk_version));
            Ok(json!({"state":identity(app), "saveToken":token, "sha256":digest, "bytes":bytes}))
        }
        "ack" => {
            check(app, r)?;
            let token = r.get("saveToken").and_then(Value::as_str).ok_or("missing save token")?;
            let digest = r.get("sha256").and_then(Value::as_str).ok_or("missing receipt digest")?;
            let disk_version = r.get("diskVersion").and_then(Value::as_u64).and_then(|v| v.checked_sub(1)).ok_or("invalid receipt version")?;
            let expected = (app.session.document_id(), app.session.rev(), token.to_string(), digest.to_string(), disk_version);
            if bridge.pending.as_ref() != Some(&expected) { return Err("no matching pending save".into()); }
            if r.get("verified").and_then(Value::as_bool) != Some(true) { return Err("host did not verify disk receipt".into()); }
            app.session.dirty = false;
            bridge.pending = None;
            Ok(identity(app))
        }
        "manifest" => manifest(app, r),
        "focusSection" => focus_section(app, r),
        _ => Err("unsupported host operation".into()),
    }
}

fn operation_id(request: &Value) -> Result<&str, String> {
    request.get("operationId").and_then(Value::as_str)
        .filter(|id| !id.is_empty() && id.len() <= 96 && id.bytes().all(|c| c.is_ascii_alphanumeric() || b"-_:.".contains(&c)))
        .ok_or_else(|| "operationId must contain 1..96 ASCII letters, digits, dash, underscore, colon or dot".into())
}

fn operation_receipt(app: &WordApp, receipt: &Value) -> Value {
    let mut result = receipt.clone();
    result["currentState"] = identity(app);
    result["matchesCurrentRevision"] = json!(receipt["state"]["documentId"].as_u64() == Some(app.session.document_id())
        && receipt["afterRevision"].as_u64() == Some(app.session.rev()));
    result
}

// ----------------------------------------------------------------------------------------------
// R4 six-section adapter: `manifest` and `focusSection` (`SECTIONS-R4-ADAPTER.md` v1).
//
// Both are read-only with respect to the document: they never edit text, never set `dirty` and
// never take an undo step. `focusSection` only moves the selection (`select.range` is a pure
// command, `session.rev()` does not move). Every offset they return is computed at run time from
// the document's own structure — the single-cell table whose cell paragraphs begin with the six
// template tokens — and the constants below are identity only: they feed `manifestHash`.
// ----------------------------------------------------------------------------------------------

/// Template family this adapter reads (`SECTIONS-R4-ADAPTER.md` §1).
const TEMPLATE_ID: &str = "nsfc2026-annual-application";
/// Adapter identity: a changed token, guidance length or layout needs a new version.
const ADAPTER_VERSION: &str = "nsfc2026-r4-adapter-v1";
/// Digest algorithm of a section's delivery payload (§4).
const SECTION_ALGORITHM: &str = "sectionDeliveryHash/1";
/// Digest algorithm of the document-wide dependencies (§4).
const GLOBAL_ALGORITHM: &str = "globalDependency/1";

/// Hostile-input guards: the module cell stays a normal cell and a body stays bridge-sized.
const MAX_CELL_PARAGRAPHS: usize = 4096;
const MAX_BODY_TEXT_BYTES: usize = 4 * 1024 * 1024;
const MAX_DEPENDENCY_BYTES: usize = 16 * 1024 * 1024;

/// One chapter of the six-section module, in delivery order (`SECTIONS-R4-ADAPTER.md` §1, §1.1).
struct SectionSpec {
    id: &'static str,
    order: u64,
    /// The token the chapter's anchor paragraph starts with.
    token: &'static str,
    /// UTF-8 bytes of the protected part: token, two spaces and the template's instruction.
    guidance: usize,
}

const SECTIONS: [SectionSpec; 6] = [
    SectionSpec { id: "topic-summary", order: 1, token: "[选题说明]", guidance: 97 },
    SectionSpec { id: "topic-basis", order: 2, token: "[选题依据]", guidance: 184 },
    SectionSpec { id: "research-content", order: 3, token: "[研究内容]", guidance: 151 },
    SectionSpec { id: "innovation", order: 4, token: "[创新之处]", guidance: 76 },
    SectionSpec { id: "expected-results", order: 5, token: "[预期成果]", guidance: 97 },
    SectionSpec { id: "references", order: 6, token: "[参考文献]", guidance: 79 },
];

/// A paragraph read from the module table's only cell; `path` is its `[block, row, cell, para]`.
struct CellParagraph<'a> {
    path: Path,
    para: &'a Paragraph,
}

/// The located six-section module: the table's position and its six anchor paragraph indexes.
struct Module<'a> {
    block: usize,
    rows: usize,
    cols: usize,
    cell: Vec<CellParagraph<'a>>,
    anchors: Vec<usize>,
}

/// A section's body: the delivery paragraphs and their text (paragraphs joined with `\n`).
struct SectionBody {
    paragraphs: Vec<Value>,
    text: String,
}

fn sha256_hex(bytes: &[u8]) -> String {
    Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect::<String>()
}

/// Canonical JSON: `serde_json` keeps its maps ordered (no `preserve_order` feature), so this is
/// byte-stable across runs and builds.
fn canonical_json(value: &Value) -> String {
    value.to_string()
}

/// A formatting number as a stable decimal; hostile non-finite values become `0.0`.
fn rounded(value: f32) -> f64 {
    let value = if value.is_finite() { f64::from(value) } else { 0.0 };
    (value * 1000.0).round() / 1000.0
}

/// `auto` renders as black, which is how the payload spells it.
fn color_text(color: &TextColor) -> String {
    match color {
        TextColor::Auto => "000000".to_string(),
        TextColor::Rgb(rgb) => rgb.hex().to_lowercase(),
    }
}

fn position(path: &Path, off: usize) -> Value {
    serde_json::to_value(Pos::new(StoryRef::Body, path.clone(), off)).unwrap_or(Value::Null)
}

fn range_value(anchor: &Path, anchor_off: usize, focus: &Path, focus_off: usize) -> Value {
    json!({"anchor": position(anchor, anchor_off), "focus": position(focus, focus_off)})
}

/// A run's delivery formatting: the effective values of the seven fields the payload states.
fn run_props(styles: &StyleSheet, para_style: Option<&str>, props: &CharProps) -> Value {
    let resolved = styles.resolve_char(para_style, props);
    json!({
        "font": resolved.font,
        "fontEa": resolved.font_ea,
        "size": rounded(resolved.size),
        "bold": resolved.bold,
        "italic": resolved.italic,
        "underline": !matches!(&resolved.underline, Underline::None),
        "color": color_text(&resolved.color),
    })
}

/// A paragraph's delivery formatting: the effective values of the six fields the payload states.
fn para_props(styles: &StyleSheet, props: &ParaProps) -> Value {
    let resolved = styles.resolve_para(props);
    json!({
        "style": props.style.clone(),
        "align": serde_json::to_value(resolved.align).unwrap_or(Value::Null),
        "indentLeft": rounded(resolved.indent_left),
        "indentRight": rounded(resolved.indent_right),
        "indentFirst": rounded(resolved.indent_first),
        "lineSpacing": serde_json::to_value(resolved.line_spacing).unwrap_or(Value::Null),
    })
}

/// `paragraphs[].runs`: the paragraph's runs clipped to `from..to` (UTF-8 byte offsets). Adjacent
/// runs whose effective formatting is equal are merged, so splitting a run in two does not move a
/// delivery digest.
fn runs_between(styles: &StyleSheet, para: &Paragraph, from: usize, to: usize) -> Vec<Value> {
    let style = para.props.style.as_deref();
    let end = to.min(para.text.len());
    let start = from.min(end);
    let mut pieces: Vec<(usize, Value)> = Vec::new();
    let mut cursor = 0usize;
    for run in &para.runs {
        let run_start = cursor;
        let run_end = run_start.saturating_add(run.len).min(para.text.len());
        cursor = run_end;
        let lo = run_start.max(start);
        let hi = run_end.min(end);
        if hi > lo {
            pieces.push((hi - lo, run_props(styles, style, &run.props)));
        }
    }
    // Runs that do not cover the whole text (a hostile or hand-built document): the rest is the
    // paragraph's own formatting rather than a panic.
    if end > start && cursor < end {
        let lo = cursor.max(start);
        if end > lo {
            pieces.push((end - lo, run_props(styles, style, &CharProps::default())));
        }
    }
    let mut merged: Vec<(usize, Value)> = Vec::new();
    for (len, props) in pieces {
        let mut joined = false;
        if let Some((last_len, last_props)) = merged.last_mut() {
            if *last_props == props {
                *last_len = last_len.saturating_add(len);
                joined = true;
            }
        }
        if !joined {
            merged.push((len, props));
        }
    }
    merged.into_iter().map(|(len, props)| json!({"len": len as u64, "props": props})).collect()
}

/// The module table is the document's `1×1` table: one row, one cell, nothing spanned.
fn single_cell_table(table: &Table) -> bool {
    let Some(row) = table.rows.first() else { return false };
    let Some(cell) = row.cells.first() else { return false };
    table.rows.len() == 1 && row.cells.len() == 1 && cell.span() == 1
}

/// The direct paragraphs of `block`'s `cell(0,0)`, with the path the engine addresses them by.
fn cell_paragraphs<'a>(doc: &'a Document, block: usize) -> Result<Vec<CellParagraph<'a>>, String> {
    let table = doc.body.get(block).and_then(|block| block.as_table()).ok_or("STRUCTURE_INVALID:anchor_ambiguous")?;
    let row = table.rows.first().ok_or("STRUCTURE_INVALID:anchor_ambiguous")?;
    let cell = row.cells.first().ok_or("STRUCTURE_INVALID:anchor_ambiguous")?;
    if cell.blocks.len() > MAX_CELL_PARAGRAPHS {
        return Err("STRUCTURE_INVALID:anchor_ambiguous".into());
    }
    let block_index = u32::try_from(block).map_err(|_| "module table index out of range")?;
    let mut out: Vec<CellParagraph<'a>> = Vec::new();
    for (index, child) in cell.blocks.iter().enumerate() {
        let Block::Para(para) = child.as_ref() else { continue };
        let Ok(index) = u32::try_from(index) else { continue };
        out.push(CellParagraph { path: Path(vec![block_index, 0, 0, index]), para });
    }
    Ok(out)
}

/// Locate the six-section module by structure (`SECTIONS-R4-ADAPTER.md` §1): the only `1×1` table
/// whose cell paragraphs begin with the most template tokens. A deleted, duplicated, reordered or
/// displaced token is reported with the R4 §3.1 error code instead of being guessed at.
fn locate_module<'a>(doc: &'a Document) -> Result<Module<'a>, String> {
    let mut scored: Vec<(usize, usize)> = Vec::new();
    for (index, block) in doc.body.iter().enumerate() {
        let Some(table) = block.as_table() else { continue };
        if !single_cell_table(table) {
            continue;
        }
        let cell = cell_paragraphs(doc, index)?;
        let score = SECTIONS.iter().filter(|spec| cell.iter().any(|p| p.para.text.starts_with(spec.token))).count();
        if score > 0 {
            scored.push((index, score));
        }
    }
    let Some(best) = scored.iter().map(|(_, score)| *score).max() else {
        return Err("STRUCTURE_INVALID:anchor_ambiguous".into());
    };
    let mut top: Vec<usize> = scored.iter().filter(|(_, score)| *score == best).map(|(index, _)| *index).collect();
    if top.len() != 1 {
        return Err("STRUCTURE_INVALID:anchor_ambiguous".into());
    }
    let Some(block) = top.pop() else { return Err("STRUCTURE_INVALID:anchor_ambiguous".into()) };
    let cell = cell_paragraphs(doc, block)?;
    let mut anchors: Vec<usize> = Vec::new();
    for spec in SECTIONS.iter() {
        let mut starts = 0usize;
        let mut total = 0usize;
        let mut at: Option<usize> = None;
        for (index, para) in cell.iter().enumerate() {
            if para.para.text.starts_with(spec.token) {
                starts = starts.saturating_add(1);
                if at.is_none() {
                    at = Some(index);
                }
            }
            total = total.saturating_add(para.para.text.match_indices(spec.token).count());
        }
        if starts == 0 {
            // A token that is present but not at a paragraph start is ambiguity, not absence.
            return Err(if total > 0 { "STRUCTURE_INVALID:anchor_ambiguous".into() } else { "STRUCTURE_INVALID:anchor_missing".into() });
        }
        if starts > 1 || total > 1 {
            return Err("STRUCTURE_INVALID:anchor_duplicate".into());
        }
        let Some(index) = at else { return Err("STRUCTURE_INVALID:anchor_missing".into()) };
        let Some(para) = cell.get(index) else { return Err("STRUCTURE_INVALID:anchor_missing".into()) };
        // The protected part must exist and end on a character boundary; a different template text
        // of the same tokens is not this adapter's template.
        if para.para.text.len() < spec.guidance || !para.para.text.is_char_boundary(spec.guidance) {
            return Err("STRUCTURE_INVALID:unsupported_template".into());
        }
        anchors.push(index);
    }
    let ordered = anchors.windows(2).all(|pair| matches!(pair, [first, second] if first < second));
    if !ordered {
        return Err("STRUCTURE_INVALID:anchor_order_invalid".into());
    }
    let (rows, cols) = doc
        .body
        .get(block)
        .and_then(|block| block.as_table())
        .map(|table| (table.rows.len(), table.cols()))
        .unwrap_or((1, 1));
    Ok(Module { block, rows: rows.max(1), cols: cols.max(1), cell, anchors })
}

/// A section's body: from `start` (paragraph index, byte offset) through `end` inclusive.
fn section_body(styles: &StyleSheet, cell: &[CellParagraph<'_>], start: (usize, usize), end: (usize, usize)) -> Result<SectionBody, String> {
    let mut paragraphs: Vec<Value> = Vec::new();
    let mut texts: Vec<String> = Vec::new();
    let mut bytes = 0usize;
    let mut index = start.0;
    while index <= end.0 {
        let Some(para) = cell.get(index) else { break };
        let len = para.para.text.len();
        let lo = if index == start.0 { start.1.min(len) } else { 0 };
        let hi = if index == end.0 { end.1.min(len) } else { len };
        let hi = hi.max(lo);
        let slice = para.para.text.get(lo..hi).ok_or("paragraph slice is not on a character boundary")?;
        bytes = bytes.saturating_add(slice.len());
        if bytes > MAX_BODY_TEXT_BYTES {
            return Err("section body exceeds bridge limit".into());
        }
        paragraphs.push(json!({
            "text": slice,
            "breaks": 1,
            "props": para_props(styles, &para.para.props),
            "runs": runs_between(styles, para.para, lo, hi),
        }));
        texts.push(slice.to_string());
        index = match index.checked_add(1) {
            Some(next) => next,
            None => break,
        };
    }
    Ok(SectionBody { paragraphs, text: texts.join("\n") })
}

/// The module heading paragraph: the block right above the six-section table (§2 `protectedAreas`).
fn heading_of(doc: &Document, table_block: usize) -> Option<(Path, String, usize)> {
    let index = table_block.checked_sub(1)?;
    let para = doc.body.get(index)?.as_para()?;
    Some((Path::top(index), para.text.clone(), para.text.len()))
}

/// `globalDependency.digest` (§4): the whole document formatting the chapters depend on. The first
/// edition is deliberately broad: any change here invalidates every chapter's confirmation.
fn global_dependency(doc: &Document) -> Result<String, String> {
    let styles = serde_json::to_value(&doc.styles).map_err(|error| format!("styles do not serialize: {error}"))?;
    let numbering = serde_json::to_value(&doc.numbering).map_err(|error| format!("numbering does not serialize: {error}"))?;
    let settings = serde_json::to_value(&doc.settings).map_err(|error| format!("settings do not serialize: {error}"))?;
    let last_section = serde_json::to_value(&doc.last_section).map_err(|error| format!("last section does not serialize: {error}"))?;
    let text = canonical_json(&json!({
        "styles": styles,
        "numbering": numbering,
        "settings": settings,
        "lastSection": last_section,
    }));
    if text.len() > MAX_DEPENDENCY_BYTES {
        return Err("global dependency exceeds bridge limit".into());
    }
    Ok(sha256_hex(text.as_bytes()))
}

/// `localFormatDigest` (§4): a quick projection of the paragraphs' formatting only.
fn local_format_digest(paragraphs: &[Value]) -> String {
    let rows: Vec<Value> = paragraphs
        .iter()
        .map(|paragraph| {
            json!({
                "props": paragraph.get("props").cloned().unwrap_or(Value::Null),
                "runs": paragraph.get("runs").cloned().unwrap_or(Value::Null),
            })
        })
        .collect();
    sha256_hex(canonical_json(&Value::Array(rows)).as_bytes())
}

/// The whole `manifest` result (`SECTIONS-R4-ADAPTER.md` §2), or `STRUCTURE_INVALID:<code>`.
pub(crate) fn build_manifest(app: &WordApp) -> Result<Value, String> {
    let doc = &app.session.doc;
    let module = locate_module(doc)?;
    let dependency = global_dependency(doc)?;
    let mut protected: Vec<Value> = Vec::new();
    if let Some((path, text, len)) = heading_of(doc, module.block) {
        protected.push(json!({"kind": "moduleHeading", "text": text, "range": range_value(&path, 0, &path, len)}));
    }
    let mut sections: Vec<Value> = Vec::new();
    let mut structure_sections: Vec<Value> = Vec::new();
    for (index, spec) in SECTIONS.iter().enumerate() {
        let Some(&anchor_index) = module.anchors.get(index) else {
            return Err("STRUCTURE_INVALID:anchor_missing".into());
        };
        let Some(anchor) = module.cell.get(anchor_index) else {
            return Err("STRUCTURE_INVALID:anchor_missing".into());
        };
        let Some(guidance_text) = anchor.para.text.get(..spec.guidance) else {
            return Err("STRUCTURE_INVALID:unsupported_template".into());
        };
        let anchor_path = anchor.path.clone();
        let anchor_path_value = json!(anchor_path.0.clone());
        let anchor_path_payload = json!(anchor_path.0.clone());
        let protected_range = range_value(&anchor_path, 0, &anchor_path, spec.guidance);
        let end = match module.anchors.get(index + 1) {
            // A chapter ends where the next chapter's anchor paragraph starts.
            Some(&next) => (next, 0usize),
            // The last chapter ends at the end of the cell's last paragraph (§3, `references`).
            None => {
                let Some(last) = module.cell.len().checked_sub(1) else {
                    return Err("STRUCTURE_INVALID:anchor_missing".into());
                };
                let Some(last_para) = module.cell.get(last) else {
                    return Err("STRUCTURE_INVALID:anchor_missing".into());
                };
                (last, last_para.para.text.len())
            }
        };
        let Some(end_para) = module.cell.get(end.0) else {
            return Err("STRUCTURE_INVALID:anchor_missing".into());
        };
        let body_end = end.1.min(end_para.para.text.len());
        let body_range = range_value(&anchor_path, spec.guidance, &end_para.path, body_end);
        let body = section_body(&doc.styles, &module.cell, (anchor_index, spec.guidance), (end.0, body_end))?;
        let text_sha = sha256_hex(body.text.as_bytes());
        let local_format = local_format_digest(&body.paragraphs);
        let payload = json!({
            "algorithmVersion": SECTION_ALGORITHM,
            "identity": {"templateId": TEMPLATE_ID, "adapterVersion": ADAPTER_VERSION, "sectionId": spec.id},
            "templateContext": {"anchorToken": spec.token, "guidanceText": guidance_text},
            "body": {"paragraphs": body.paragraphs, "textSha256": &text_sha},
            "globalDependency": {"algorithmVersion": GLOBAL_ALGORITHM, "digest": &dependency},
        });
        let delivery = sha256_hex(canonical_json(&payload).as_bytes());
        let char_count = body.text.chars().count();
        let body_text = body.text;
        protected.push(json!({"kind": "sectionAnchor", "sectionId": spec.id, "range": protected_range.clone()}));
        structure_sections.push(json!({
            "sectionId": spec.id,
            "order": spec.order,
            "anchorPath": &anchor_path_value,
            "guidanceLength": spec.guidance,
            "protectedRange": &protected_range,
            "bodyRange": &body_range,
        }));
        sections.push(json!({
            "sectionId": spec.id,
            "order": spec.order,
            "anchorToken": spec.token,
            "anchorPath": &anchor_path_payload,
            "guidanceLength": spec.guidance,
            "protectedRange": protected_range,
            "bodyRange": body_range,
            "bodyText": body_text,
            "bodyTextSha256": text_sha,
            "bodyCharCount": char_count,
            "sectionDeliveryHash": &delivery,
            "deliveryCanonical": {
                "payloadSha256": &delivery,
                "algorithmVersion": SECTION_ALGORITHM,
                "globalDependencyDigest": &dependency,
                "localFormatDigest": local_format,
            },
        }));
    }
    // The module's instruction line is the paragraph above the first anchor.
    if let (Some(first), Some(instruction)) = (module.anchors.first(), module.cell.first()) {
        if *first > 0 {
            let text_sha = sha256_hex(instruction.para.text.as_bytes());
            let range = range_value(&instruction.path, 0, &instruction.path, instruction.para.text.len());
            protected.push(json!({"kind": "instructionLine", "textSha256": text_sha, "range": range}));
        }
    }
    let module_locator = json!({
        "kind": "singleCellTable",
        "block": module.block,
        "rows": module.rows,
        "cols": module.cols,
        "cell": [0, 0],
    });
    // `manifestHash` covers structure only (§2 rule 1): identity, the module locator, the protected
    // ranges and each chapter's logical place and ranges — never `revision`, a `sectionDeliveryHash`
    // or a body text.
    let structure_payload = json!({
        "templateId": TEMPLATE_ID,
        "adapterVersion": ADAPTER_VERSION,
        "algorithmVersion": SECTION_ALGORITHM,
        "moduleLocator": &module_locator,
        "protectedAreas": protected
            .iter()
            .map(|entry| json!({"range": entry.get("range").cloned().unwrap_or(Value::Null)}))
            .collect::<Vec<Value>>(),
        "sections": structure_sections,
    });
    let manifest_hash = sha256_hex(canonical_json(&structure_payload).as_bytes());
    Ok(json!({
        "state": identity(app),
        "templateId": TEMPLATE_ID,
        "adapterVersion": ADAPTER_VERSION,
        "algorithmVersion": SECTION_ALGORITHM,
        "structure": {"ok": true, "errors": []},
        "manifestHash": manifest_hash,
        "moduleLocator": module_locator,
        "protectedAreas": protected,
        "sections": sections,
        "globalDependency": {
            "algorithmVersion": GLOBAL_ALGORITHM,
            "digest": dependency,
            "covers": ["styles", "numbering", "settings", "lastSection"],
        },
    }))
}

/// The template/adapter guards both operations accept (`§2`, `§3`).
fn adapter_guard(request: &Value) -> Result<(), String> {
    if let Some(template) = request.get("templateId").and_then(Value::as_str) {
        if template != TEMPLATE_ID {
            return Err("UNSUPPORTED_TEMPLATE".into());
        }
    }
    if let Some(version) = request.get("adapterVersion").and_then(Value::as_str) {
        if version != ADAPTER_VERSION {
            return Err("ADAPTER_MISMATCH".into());
        }
    }
    Ok(())
}

/// The optional `expectedDocumentId` / `expectedRevision` guards (`§2`).
fn revision_guard(app: &WordApp, request: &Value) -> Result<(), String> {
    if let Some(document_id) = request.get("expectedDocumentId").and_then(Value::as_u64) {
        if document_id != app.session.document_id() {
            return Err("VERSION_CHANGED".into());
        }
    }
    if let Some(revision) = request.get("expectedRevision").and_then(Value::as_u64) {
        if revision != app.session.rev() {
            return Err("VERSION_CHANGED".into());
        }
    }
    Ok(())
}

/// The print page the position sits on, for the UI's scroll hint (`§3` `scrollTo`).
fn page_of(app: &WordApp, anchor_path: &Value) -> Value {
    let Ok(path) = serde_json::from_value::<Path>(anchor_path.clone()) else {
        return Value::Null;
    };
    let layout = app.session.export_layout();
    for (index, page) in layout.pages.iter().enumerate() {
        for item in &page.items {
            if let wordcraft_engine::layout::Placed::Lines { story, path: item_path, .. } = item {
                if *story == StoryRef::Body && *item_path == path {
                    return u64::try_from(index.saturating_add(1)).map(Value::from).unwrap_or(Value::Null);
                }
            }
        }
    }
    Value::Null
}

fn manifest(app: &WordApp, request: &Value) -> Result<Value, String> {
    adapter_guard(request)?;
    revision_guard(app, request)?;
    build_manifest(app)
}

pub(crate) fn focus_section(app: &mut WordApp, request: &Value) -> Result<Value, String> {
    let section_id = request.get("sectionId").and_then(Value::as_str).ok_or("missing sectionId")?.to_string();
    adapter_guard(request)?;
    revision_guard(app, request)?;
    // Recompute from the live document: a stored offset is never trusted (`§3`).
    let manifest = build_manifest(app)?;
    let manifest_hash = manifest.get("manifestHash").and_then(Value::as_str).ok_or("manifest has no hash")?.to_string();
    if let Some(expected) = request.get("expectedManifestHash").and_then(Value::as_str) {
        if expected != manifest_hash {
            return Err("VERSION_CHANGED".into());
        }
    }
    let Some(sections) = manifest.get("sections").and_then(Value::as_array) else {
        return Err("manifest has no sections".into());
    };
    let Some(section) = sections.iter().find(|entry| entry.get("sectionId").and_then(Value::as_str) == Some(section_id.as_str())) else {
        return Err("SECTION_NOT_FOUND".into());
    };
    let Some(anchor) = section.get("bodyRange").and_then(|body| body.get("anchor")).cloned() else {
        return Err("SECTION_NOT_FOUND".into());
    };
    let selection = app.execute("select.range", json!({"anchor": anchor.clone(), "focus": anchor.clone()}))?;
    // The engine clamps a range to the document; a moved anchor would mean the document changed
    // under us, so this is a version change rather than an approximate placement (`§3`).
    if selection.get("anchor") != Some(&anchor) {
        return Err("VERSION_CHANGED".into());
    }
    let anchor_path = section.get("anchorPath").cloned().unwrap_or(Value::Null);
    let page = page_of(app, &anchor_path);
    Ok(json!({
        "state": identity(app),
        "sectionId": section_id,
        "manifestHash": manifest_hash,
        "selection": selection,
        "located": true,
        "scrollTo": {"page": page, "y": Value::Null},
    }))
}

pub fn drain(app: &mut WordApp, ctx: &egui::Context) {
    BRIDGE.with(|cell| {
        let mut bridge = cell.borrow_mut();
        bridge.ctx = Some(ctx.clone());
        let did_work = !bridge.queue.is_empty();
        for request in std::mem::take(&mut bridge.queue) {
            let result = serde_json::from_str::<Value>(&request.json).map_err(|e| e.to_string()).and_then(|r| execute(app, &mut bridge, &r));
            let reply = match result { Ok(result) => json!({"ok":true,"result":result}), Err(error) => json!({"ok":false,"error":error,"state":identity(app)}) };
            let _ = request.resolve.call1(&JsValue::NULL, &JsValue::from_str(&reply.to_string()));
        }
        if did_work {
            if let Some(on_dirty) = &app.services.on_dirty { on_dirty(app.session.dirty); }
            ctx.request_repaint();
        }
    });
}
