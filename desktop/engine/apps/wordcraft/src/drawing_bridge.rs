//! Minimal native-document adapter for the existing LingDoc drawing workspace.
//! The page owns drawing/library state; this adapter owns only selection and explicit DOCX writes.

use std::collections::BTreeMap;
use std::time::{Duration, Instant};

use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use wordcraft_doc::{Pos, para::InlineObject};
use wordcraft_ui_egui::WordApp;

const IMAGE_LIMIT: usize = 4 * 1024 * 1024;
const PICK_TTL: Duration = Duration::from_secs(5 * 60);
const EDIT_TTL: Duration = Duration::from_secs(2 * 60 * 60);

struct Picker {
    id: String,
    document_id: u64,
    drawing: Value,
    request: Option<Value>,
    expires: Instant,
}

struct Edit {
    snapshot: Value,
    expires: Instant,
}

#[derive(Default)]
pub struct DrawingDocument {
    picker: Option<Picker>,
    edits: BTreeMap<String, Edit>,
    default_target: Option<Value>,
    operation_document: Option<u64>,
    operations: BTreeMap<String, (Value, Value)>,
}

pub fn random_id() -> Result<String, String> {
    let mut bytes = [0u8; 16];
    getrandom::fill(&mut bytes).map_err(|error| error.to_string())?;
    Ok(bytes.iter().map(|byte| format!("{byte:02x}")).collect())
}

fn identity(app: &WordApp) -> Value {
    json!({"documentId":app.session.document_id(),"revision":app.session.rev(),"dirty":app.session.dirty})
}

fn valid_id(value: &str) -> bool {
    !value.is_empty() && value.len() <= 128 && value.bytes().all(|byte| byte.is_ascii_alphanumeric() || byte == b'_' || byte == b'-')
}

fn operation_id(request: &Value) -> Result<&str, String> {
    request.get("operationId").and_then(Value::as_str).filter(|id| valid_id(id)).ok_or_else(|| "无效的图片操作编号。".into())
}

fn check_identity(app: &WordApp, request: &Value) -> Result<(), String> {
    if request.get("expectedDocumentId").and_then(Value::as_u64) != Some(app.session.document_id()) {
        return Err("目标文稿已变化，请重新定位。".into());
    }
    if request.get("expectedRevision").and_then(Value::as_u64) != Some(app.session.rev()) {
        return Err("正文在绘图期间已变化，请返回正文重新选择位置。".into());
    }
    Ok(())
}

fn diagram_image_at(app: &WordApp, pos: &Pos) -> Result<Option<Value>, String> {
    let Some(InlineObject::Image { media, alt, .. }) = app.session.doc.para_at(pos).and_then(|paragraph| paragraph.object_at(pos.off)) else { return Ok(None) };
    let bytes = app.session.doc.media.get(media).ok_or("选中图片的数据缺失。")?;
    if bytes.len() > IMAGE_LIMIT { return Err("选中图片超过绘图大小限制。".into()); }
    let digest = Sha256::digest(bytes.as_slice()).iter().map(|byte| format!("{byte:02x}")).collect::<String>();
    Ok(Some(json!({"sha256":digest,"data":wordcraft_engine::cmd::insert::base64_encode(bytes.as_slice()),"alt":alt})))
}

fn diagram_image(app: &WordApp) -> Result<Option<Value>, String> {
    let Some((pos, _)) = wordcraft_engine::cmd::objects::object_selection(&app.session) else { return Ok(None) };
    diagram_image_at(app, &pos)
}

fn diagram_png(request: &Value) -> Result<&str, String> {
    let data = request.get("data").and_then(Value::as_str).ok_or("缺少 PNG 数据。")?;
    if data.len() > IMAGE_LIMIT * 4 / 3 + 4 { return Err("图稿 PNG 超过大小限制。".into()); }
    let bytes = wordcraft_engine::cmd::insert::base64_decode(data).ok_or("图稿 PNG 编码无效。")?;
    if bytes.len() > IMAGE_LIMIT || !bytes.starts_with(b"\x89PNG\r\n\x1a\n") { return Err("图稿必须是有效的 PNG。".into()); }
    Ok(data)
}

fn bookmark_selection(request: &Value) -> Result<wordcraft_engine::Selection, String> {
    let bookmark = request.get("bookmark").ok_or("缺少正文位置。")?;
    let anchor = bookmark.get("anchor").and_then(|value| serde_json::from_value::<Pos>(value.clone()).ok()).ok_or("正文起点无效。")?;
    let focus = bookmark.get("focus").and_then(|value| serde_json::from_value::<Pos>(value.clone()).ok()).ok_or("正文终点无效。")?;
    Ok(wordcraft_engine::Selection { anchor, focus })
}

fn diagram_bookmark(request: &Value) -> Result<wordcraft_engine::Selection, String> {
    let selection = bookmark_selection(request)?;
    let (start, end) = selection.ordered();
    if start.story != end.story || start.path != end.path || start.off.checked_add(wordcraft_doc::para::OBJ.len_utf8()) != Some(end.off) {
        return Err("更新目标必须恰好是一张图片。".into());
    }
    Ok(selection)
}

pub fn edit_snapshot(app: &WordApp) -> Result<Value, String> {
    let (pos, _) = wordcraft_engine::cmd::objects::object_selection(&app.session).ok_or("请先选中一张图稿。")?;
    if !wordcraft_engine::cmd::objects::is_lingdoc_diagram_at(&app.session, &pos) { return Err("这张图片没有可编辑的图稿源。".into()); }
    let image = diagram_image(app)?.ok_or("无法读取选中图稿。")?;
    let selection = wordcraft_engine::cmd::sel_result(&app.session).map_err(|error| error.to_string())?;
    Ok(json!({"state":identity(app),"selection":selection,"image":image}))
}

impl DrawingDocument {
    fn prune(&mut self, app: &WordApp) {
        let now = Instant::now();
        self.edits.retain(|_, edit| edit.expires > now);
        if self.picker.as_ref().is_some_and(|picker| picker.expires <= now && picker.request.is_none()) { self.picker = None; }
        let document = app.session.document_id();
        if self.operation_document != Some(document) {
            self.operation_document = Some(document);
            self.operations.clear();
            self.picker = None;
            self.default_target = None;
        }
    }

    /// Freeze the explicit cursor target before the external workbench can take focus.
    pub fn capture_default_target(&mut self, app: &mut WordApp) -> Result<(), String> {
        let selection = app.execute("document.selection", json!({}))?;
        self.operation_document = Some(app.session.document_id());
        self.default_target = (selection.get("anchor") == selection.get("focus"))
            .then(|| json!({"state":identity(app),"selection":selection}));
        Ok(())
    }

    pub fn picker_snapshot(&self) -> Value {
        self.picker.as_ref().map_or(Value::Null, |picker| json!({
            "id":picker.id,"title":picker.drawing.get("title").and_then(Value::as_str).unwrap_or("图稿"),
            "waiting":picker.request.is_some(),
        }))
    }

    pub fn register_edit(&mut self, ticket: String, snapshot: Value) {
        self.edits.insert(ticket, Edit { snapshot, expires: Instant::now() + EDIT_TTL });
        while self.edits.len() > 16 {
            let Some(oldest) = self.edits.keys().next().cloned() else { break };
            self.edits.remove(&oldest);
        }
    }

    fn begin_pick(&mut self, app: &mut WordApp, ctx: &egui::Context, request: &Value) -> Result<Value, String> {
        let id = request.get("pickId").and_then(Value::as_str).filter(|id| valid_id(id)).ok_or("定位编号无效。")?;
        let drawing = request.get("drawing").cloned().ok_or("图稿尚未保存。")?;
        if request.get("draftId").and_then(Value::as_str) != drawing.get("id").and_then(Value::as_str)
            || drawing.get("revision").and_then(Value::as_u64).is_none_or(|revision| revision < 1)
            || drawing.get("embeddedPng").and_then(Value::as_str).is_none_or(|png| png.is_empty() || png.len() > IMAGE_LIMIT * 4 / 3 + 4) {
            return Err("图稿尚未准备好，请先保存再定位。".into());
        }
        if self.picker.as_ref().is_some_and(|picker| picker.request.is_some()) { return Err("已有插图等待确认，请先完成或取消。".into()); }
        app.project_workspace.release_focus();
        app.source_workspace.set_mode("body")?;
        app.ui.backstage = false;
        app.canvas.want_focus = true;
        self.picker = Some(Picker { id:id.into(), document_id:app.session.document_id(), drawing, request:None, expires:Instant::now() + PICK_TTL });
        ctx.send_viewport_cmd(egui::ViewportCommand::Focus);
        Ok(json!({"picking":true}))
    }

    pub fn confirm_pick(&mut self, app: &mut WordApp) -> Result<Value, String> {
        self.prune(app);
        let (document_id, drawing, previous) = {
            let picker = self.picker.as_mut().ok_or("定位已结束，请在绘图页重新定位。")?;
            (picker.document_id, picker.drawing.clone(), picker.request.clone())
        };
        if document_id != app.session.document_id() { return Err("目标文稿已变化，请重新定位。".into()); }
        if app.source_workspace.source_active() || app.project_workspace.auxiliary_active() { return Err("请先切回正文再选择位置。".into()); }
        let request = if let Some(request) = previous { request } else {
            let selection = app.execute("document.selection", json!({}))?;
            if selection.get("anchor") != selection.get("focus") { return Err("请在正文单击插入光标，而不是选择一段文字。".into()); }
            let pick_id = self.picker.as_ref().map(|picker| picker.id.clone()).unwrap_or_default();
            json!({"op":"diagram.insert","operationId":format!("pick-{pick_id}"),
                "expectedDocumentId":document_id,"expectedRevision":app.session.rev(),"bookmark":selection,
                "data":drawing.get("embeddedPng").and_then(Value::as_str).unwrap_or_default(),
                "alt":drawing.get("title").and_then(Value::as_str).unwrap_or("图稿")})
        };
        if let Some(picker) = self.picker.as_mut() { picker.request = Some(request.clone()); }
        let result = self.perform_write(app, &request)?;
        self.picker = None;
        Ok(result)
    }

    pub fn cancel_pick(&mut self) {
        if self.picker.as_ref().is_some_and(|picker| picker.request.is_none()) { self.picker = None; }
    }

    fn perform_write(&mut self, app: &mut WordApp, request: &Value) -> Result<Value, String> {
        let id = operation_id(request)?;
        if let Some((original, receipt)) = self.operations.get(id) {
            if original != request { return Err("图片操作编号已属于另一项请求。".into()); }
            return Ok(receipt.clone());
        }
        if self.operations.len() >= 128 { return Err("本次正文图片操作过多，请保存并重开文稿后再试。".into()); }
        check_identity(app, request)?;
        let op = request.get("op").and_then(Value::as_str).ok_or("缺少图片操作。")?;
        let replace = op == "diagram.replace";
        let bookmark = request.get("bookmark").ok_or("缺少正文位置。")?;
        let selection = app.execute("document.selection", json!({}))?;
        let restore = request.get("restoreBookmark").and_then(Value::as_bool) == Some(true);
        if bookmark.get("anchor").is_none() || bookmark.get("focus").is_none()
            || (!restore && (bookmark.get("anchor") != selection.get("anchor") || bookmark.get("focus") != selection.get("focus"))) {
            return Err("正文选区已变化，请重新选择位置。".into());
        }
        let target = if restore { Some(bookmark_selection(request)?) } else { None };
        if !replace && target.as_ref().map_or(bookmark.get("anchor") != bookmark.get("focus"), |target| target.anchor != target.focus) {
            return Err("请先选择插入光标。".into());
        }
        if replace {
            let target = if restore { Some(diagram_bookmark(request)?) } else { None };
            let image = if let Some(target) = &target { diagram_image_at(app, &target.ordered().0)? } else { diagram_image(app)? }.ok_or("请先选中要更新的原图。")?;
            if request.get("expectedImageSha256").and_then(Value::as_str) != image.get("sha256").and_then(Value::as_str) {
                return Err("原图已变化，请重新打开编辑。".into());
            }
        }
        let data = diagram_png(request)?;
        let alt = request.get("alt").and_then(Value::as_str).unwrap_or("");
        if alt.chars().count() > 120 { return Err("图稿替代文字过长。".into()); }
        let previous = app.session.sel.clone();
        if let Some(target) = target { app.session.sel = target; }
        let command = if replace { "picture.change" } else { "insert.picture" };
        let result = match app.execute(command, json!({"data":data,"alt":alt,"floating":!replace})) {
            Ok(result) => result,
            Err(error) => { app.session.sel = previous; return Err(error); }
        };
        let receipt = json!({"operationId":id,"status":"applied","state":identity(app),"result":result});
        self.operations.insert(id.into(), (request.clone(), receipt.clone()));
        Ok(receipt)
    }

    pub fn handle(&mut self, app: &mut WordApp, ctx: &egui::Context, method: &str, request: &Value) -> Result<Value, String> {
        self.prune(app);
        match method {
            "diagram.default-target" => Ok(self.default_target.clone().unwrap_or(Value::Null)),
            "diagram.selection" => Ok(json!({"state":identity(app),"selection":app.execute("document.selection",json!({}))?,"image":diagram_image(app)?})),
            "diagram.insert" | "diagram.replace" => self.perform_write(app, request),
            "diagram.pick" => self.begin_pick(app, ctx, request),
            "diagram.cancel-pick" => {
                if request.get("pickId").and_then(Value::as_str) == self.picker.as_ref().map(|picker| picker.id.as_str()) { self.cancel_pick(); }
                Ok(json!({"canceled":self.picker.is_none()}))
            }
            "diagram.edit" => {
                let ticket = request.get("ticket").and_then(Value::as_str).ok_or("编辑会话无效。")?;
                self.edits.get(ticket).map(|edit| edit.snapshot.clone()).ok_or_else(|| String::from("编辑会话已过期，请在正文图片上重新点击编辑。"))
            }
            "diagram.edit-apply" => {
                let ticket = request.get("ticket").and_then(Value::as_str).ok_or("编辑会话无效。")?;
                let snapshot = self.edits.get(ticket).map(|edit| edit.snapshot.clone()).ok_or_else(|| String::from("编辑会话已过期，请重新打开原图。"))?;
                let replace = json!({"op":"diagram.replace","operationId":request.get("operationId").cloned().unwrap_or(Value::Null),
                    "expectedDocumentId":snapshot["state"]["documentId"],"expectedRevision":snapshot["state"]["revision"],
                    "bookmark":snapshot["selection"],"expectedImageSha256":snapshot["image"]["sha256"],"restoreBookmark":true,
                    "data":request.get("data").cloned().unwrap_or(Value::Null),"alt":request.get("alt").cloned().unwrap_or_else(|| json!("图稿"))});
                self.perform_write(app, &replace)
            }
            "diagram.return" => {
                app.project_workspace.release_focus();
                app.source_workspace.set_mode("body")?;
                app.ui.backstage = false;
                app.canvas.want_focus = true;
                ctx.send_viewport_cmd(egui::ViewportCommand::Focus);
                Ok(identity(app))
            }
            _ => Err("不支持的绘图正文操作。".into()),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use wordcraft_doc::Document;
    use wordcraft_engine::Session;
    use wordcraft_ui_egui::Services;

    const PNG: &str = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==";

    #[test]
    fn explicit_insert_and_idempotent_retry() {
        let mut app = WordApp::new(Session::new(Document::new()), Services::default());
        let mut bridge = DrawingDocument::default();
        let request = json!({"op":"diagram.insert","operationId":"drawing-1","expectedDocumentId":app.session.document_id(),
            "expectedRevision":app.session.rev(),"bookmark":wordcraft_engine::cmd::sel_result(&app.session).unwrap(),"data":PNG,"alt":"drawing"});
        assert_eq!(bridge.perform_write(&mut app, &request).unwrap()["status"], "applied");
        let revision = app.session.rev();
        assert_eq!(bridge.perform_write(&mut app, &request).unwrap()["status"], "applied");
        assert_eq!(app.session.rev(), revision);
    }

    #[test]
    fn default_target_survives_cursor_movement_but_not_document_edits() {
        let mut app = WordApp::new(Session::new(Document::from_text("正文")), Services::default());
        let mut bridge = DrawingDocument::default();
        app.session.sel = wordcraft_engine::Selection::caret(Pos::body(0, 0));
        bridge.capture_default_target(&mut app).unwrap();
        let target = bridge.default_target.clone().unwrap();
        let returned = bridge.handle(&mut app, &egui::Context::default(), "diagram.default-target", &json!({})).unwrap();
        assert_eq!(returned, target, "the first bridge read must not prune a fresh target");
        app.execute("caret.docEnd", json!({})).unwrap();
        let request = json!({"op":"diagram.insert","operationId":"drawing-default","expectedDocumentId":target["state"]["documentId"],
            "expectedRevision":target["state"]["revision"],"bookmark":target["selection"],"restoreBookmark":true,"data":PNG,"alt":"drawing"});
        assert_eq!(bridge.perform_write(&mut app, &request).unwrap()["status"], "applied");
        assert!(diagram_image_at(&app, &Pos::body(0, 0)).unwrap().is_some());

        let mut stale = request.clone();
        stale["operationId"] = json!("drawing-stale");
        stale["expectedRevision"] = json!(app.session.rev() - 1);
        assert!(bridge.perform_write(&mut app, &stale).unwrap_err().contains("正文在绘图期间已变化"));
    }

    #[test]
    fn edit_snapshot_rejects_an_ordinary_picture() {
        let mut app = WordApp::new(Session::new(Document::new()), Services::default());
        app.execute("insert.picture", json!({"data":PNG})).unwrap();
        app.session.sel = wordcraft_engine::Selection { anchor:Pos::body(0,0), focus:Pos::body(0,wordcraft_doc::para::OBJ.len_utf8()) };
        assert!(edit_snapshot(&app).is_err());
    }
}
