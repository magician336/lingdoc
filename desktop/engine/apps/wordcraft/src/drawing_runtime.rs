//! Desktop host for the existing LingDoc drawing workbench and pinned next.ai canvas.

use std::net::TcpListener;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::{Arc, Mutex};
use std::sync::mpsc::Receiver;
use std::time::{Duration, Instant};

use serde_json::{Value, json};
use wordcraft_ui_egui::{ControlRequest, WordApp};

use crate::drawing_bridge::{DrawingDocument, edit_snapshot, random_id};

#[derive(Clone, Default)]
pub struct DrawingCommands(Arc<Mutex<Vec<Value>>>);

impl DrawingCommands {
    pub fn open_edit(&self, app: &WordApp) -> Result<(), String> {
        let snapshot = edit_snapshot(app)?;
        self.0.lock().unwrap_or_else(|error| error.into_inner()).push(json!({"kind":"openEdit","snapshot":snapshot}));
        Ok(())
    }
    fn take(&self) -> Vec<Value> { std::mem::take(&mut *self.0.lock().unwrap_or_else(|error| error.into_inner())) }
}

struct Workbench {
    next_ai: Child,
    workspace: Child,
    origin: String,
    next_origin: String,
    started: Instant,
    opened: bool,
}

fn free_port() -> Result<u16, String> {
    let listener = TcpListener::bind(("127.0.0.1", 0)).map_err(|error| format!("无法分配绘图端口：{error}"))?;
    Ok(listener.local_addr().map_err(|error| error.to_string())?.port())
}

fn node() -> std::ffi::OsString {
    std::env::var_os("LINGDOC_DRAWING_NODE").or_else(|| std::env::var_os("KIMI_DESKTOP_RUNTIME_NODE")).or_else(|| {
        // Shared portable runtime provisioned at .tooling\node, so the workbench does not
        // depend on a user-wide Node installation being present on PATH.
        let candidate = PathBuf::from(r"D:\Mon3tr\projects\lingdoc-dachuang\.tooling\node\node.exe");
        candidate.is_file().then(|| candidate.into_os_string())
    }).unwrap_or_else(|| "node".into())
}

/// The pinned next.ai standalone build lives outside the source tree so one copy serves
/// every worktree: `LINGDOC_DRAWING_UPSTREAM` wins, then the shared `.tooling` build
/// produced by `drawing-upstream/prepare.mjs`, then the checkout-local legacy path.
fn application_directory() -> PathBuf {
    if let Some(explicit) = std::env::var_os("LINGDOC_DRAWING_UPSTREAM") {
        let explicit = PathBuf::from(explicit);
        if explicit.join(".next/standalone/server.js").is_file() {
            return explicit;
        }
    }
    let shared = PathBuf::from(r"D:\Mon3tr\projects\lingdoc-dachuang\.tooling\drawing-runtime\source");
    if shared.join(".next/standalone/server.js").is_file() {
        return shared;
    }
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../drawing-upstream/runtime/source")
}

fn stop(child: &mut Child) {
    let _ = child.kill();
    let _ = child.wait();
}

impl Workbench {
    fn start(directory: &Path, ctx: &egui::Context) -> Result<(Self, Receiver<ControlRequest>), String> {
        let application = application_directory();
        let server = application.join(".next/standalone/server.js");
        let workspace_server = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("native-drawing-server.mjs");
        if !server.is_file() || !workspace_server.is_file() { return Err("旧版绘图工作台资源不完整，请先运行 drawing-upstream/prepare.mjs --build 准备既有绘图组件。".into()); }
        let next_port = free_port()?;
        let workspace_port = free_port()?;
        let next_origin = format!("http://127.0.0.1:{next_port}");
        let origin = format!("http://127.0.0.1:{workspace_port}");
        let temporary = std::env::temp_dir();
        let mut next = Command::new(node());
        next.arg(&server).current_dir(&application)
            .env("HOSTNAME", "127.0.0.1").env("PORT", next_port.to_string()).env("NODE_ENV", "production")
            .env("NEXT_TELEMETRY_DISABLED", "1").env("NEXT_PUBLIC_BASE_PATH", "")
            .env("LANGFUSE_ENABLED", "false").env("LANGFUSE_SECRET_KEY", "").env("LANGFUSE_PUBLIC_KEY", "")
            .env("DEBUG_LLM_PAYLOAD", "false").env("ORIGIN_SECRET", "").env("DYNAMODB_QUOTA_TABLE", "")
            .env("SETTINGS_FILE", temporary.join(format!("lingdoc-drawing-settings-{next_port}.json")))
            .env("AI_MODELS_CONFIG_PATH", temporary.join(format!("lingdoc-drawing-models-unused-{next_port}.json")))
            .env("LINGDOC_DRAWING_PARENT_ORIGIN", &origin).env("LINGDOC_DRAWING_SELF_ORIGIN", &next_origin)
            .env("LINGDOC_DRAWING_RUNTIME_ID", format!("native-{next_port}"))
            .stdout(Stdio::null()).stderr(Stdio::null());
        let api_key = crate::deepseek::ApiKey::from_configured().ok();
        if let Some(key) = api_key.as_ref() {
            next.env("AI_PROVIDER", "deepseek").env("AI_MODEL", "deepseek-chat")
                .env("DEEPSEEK_API_KEY", key.as_str()).env("DEEPSEEK_BASE_URL", "https://api.deepseek.com/v1")
                .env("AI_MODELS_CONFIG", "{\"providers\":[{\"name\":\"LingDoc\",\"provider\":\"deepseek\",\"models\":[\"deepseek-chat\"],\"default\":true}]}");
        } else {
            next.env("AI_PROVIDER", "").env("AI_MODEL", "").env("AI_MODELS_CONFIG", "{\"providers\":[]}");
        }
        let ai_configured = api_key.is_some();
        let mut next_ai = next.spawn().map_err(|error| format!("无法启动 next.ai 画布：{error}"))?;
        let (control_port, control_key, requests) = match crate::control_server::start_drawing(ctx.clone()) {
            Ok(control) => control,
            Err(error) => { stop(&mut next_ai); return Err(error); }
        };
        let token = match random_id() {
            Ok(token) => token,
            Err(error) => { stop(&mut next_ai); return Err(error); }
        };
        let mut workspace = Command::new(node());
        workspace.arg(&workspace_server).current_dir(PathBuf::from(env!("CARGO_MANIFEST_DIR")))
            .env("LINGDOC_NATIVE_DRAWING_PORT", workspace_port.to_string()).env("LINGDOC_NATIVE_DRAWING_TOKEN", token)
            .env("LINGDOC_CONTROL_PORT", control_port.to_string()).env("LINGDOC_CONTROL_KEY", control_key)
            .env("LINGDOC_DRAWING_DIRECTORY", directory).env("LINGDOC_NEXT_AI_ORIGIN", &next_origin)
            .env("LINGDOC_NEXT_AI_APPLICATION", &application).env("LINGDOC_DRAWING_AI_CONFIGURED", if ai_configured { "1" } else { "0" })
            .stdout(Stdio::null()).stderr(Stdio::null());
        let workspace = match workspace.spawn() {
            Ok(child) => child,
            Err(error) => { stop(&mut next_ai); return Err(format!("无法启动旧版绘图工作台：{error}")); }
        };
        Ok((Self { next_ai, workspace, origin, next_origin, started:Instant::now(), opened:false }, requests))
    }

    fn ready(&self) -> bool {
        let Ok(client) = reqwest::blocking::Client::builder().no_proxy().timeout(Duration::from_secs(2)).build() else { return false };
        let next = client.get(format!("{}/api/lingdoc-bridge-config", self.next_origin)).send().is_ok_and(|response| response.status().is_success());
        let workspace = client.get(format!("{}/native-reading/drawing-config.js", self.origin)).send().is_ok_and(|response| response.status().is_success());
        next && workspace
    }

    fn poll(&mut self, query: &str) -> Result<bool, String> {
        if self.next_ai.try_wait().map_err(|error| error.to_string())?.is_some() { return Err("next.ai 画布服务已停止，请重新打开绘图工作台。".into()); }
        if self.workspace.try_wait().map_err(|error| error.to_string())?.is_some() { return Err("绘图工作台服务已停止，请重新打开。".into()); }
        if !self.opened && self.ready() {
            open_workbench(&format!("{}/native-reading/drawing{}", self.origin, query))?;
            self.opened = true;
        }
        if !self.opened && self.started.elapsed() > Duration::from_secs(120) { return Err("绘图工作台启动超时，请重新打开。".into()); }
        Ok(self.opened)
    }

    fn reopen(&self, query: &str) -> Result<(), String> {
        open_workbench(&format!("{}/native-reading/drawing{}", self.origin, query))
    }
}

impl Drop for Workbench {
    fn drop(&mut self) { stop(&mut self.workspace); stop(&mut self.next_ai); }
}

#[cfg(target_os = "windows")]
fn open_workbench(url: &str) -> Result<(), String> {
    if std::env::var("WORDCRAFT_DRAWING_NO_BROWSER").as_deref() == Ok("1") { return Ok(()); }
    let mut candidates = Vec::new();
    for variable in ["PROGRAMFILES(X86)", "PROGRAMFILES", "LOCALAPPDATA"] {
        if let Some(root) = std::env::var_os(variable) { candidates.push(PathBuf::from(root).join("Microsoft/Edge/Application/msedge.exe")); }
    }
    if let Some(edge) = candidates.into_iter().find(|path| path.is_file()) {
        Command::new(edge).arg(format!("--app={url}")).arg("--new-window").stdout(Stdio::null()).stderr(Stdio::null()).spawn()
            .map(|_| ()).map_err(|error| format!("无法打开绘图工作台：{error}"))
    } else {
        open::that(url).map_err(|error| format!("无法打开绘图工作台：{error}"))
    }
}

#[cfg(not(target_os = "windows"))]
fn open_workbench(url: &str) -> Result<(), String> {
    if std::env::var("WORDCRAFT_DRAWING_NO_BROWSER").as_deref() == Ok("1") { return Ok(()); }
    open::that(url).map_err(|error| format!("无法打开绘图工作台：{error}"))
}

pub struct DrawingRuntime {
    commands: DrawingCommands,
    document: DrawingDocument,
    control: Option<Receiver<ControlRequest>>,
    workbench: Option<Workbench>,
    pending_query: String,
    error: String,
}

impl DrawingRuntime {
    pub fn new(commands: DrawingCommands) -> Self {
        Self { commands, document:DrawingDocument::default(), control:None, workbench:None, pending_query:String::new(), error:String::new() }
    }

    pub fn tick(&mut self, app: &mut WordApp, ctx: &egui::Context) {
        let mut actions = self.commands.take();
        actions.extend(app.drawing.take_actions());
        if self.workbench.as_ref().is_some_and(|_| !self.pending_query.is_empty()) || self.workbench.is_some() {
            ctx.request_repaint_after(Duration::from_millis(100));
        }
        self.drain_document_requests(app, ctx);
        for action in actions { self.action(app, ctx, action); }
        if let Some(workbench) = self.workbench.as_mut() {
            match workbench.poll(&self.pending_query) {
                Ok(true) => {},
                Ok(false) => {}
                Err(error) => { self.error = error; self.workbench = None; self.control = None; }
            }
        }
        app.drawing.hydrate(self.snapshot()).unwrap_or_else(|error| {
            log::error!("drawing snapshot: {error}");
            app.status("绘图状态暂时无法显示，请重新打开绘图工作台。");
        });
    }

    fn drain_document_requests(&mut self, app: &mut WordApp, ctx: &egui::Context) {
        let Some(rx) = self.control.take() else { return };
        while let Ok(request) = rx.try_recv() {
            let reply = match self.document.handle(app, ctx, &request.method, &request.params) {
                Ok(result) => json!({"ok":true,"result":result}),
                Err(error) => json!({"ok":false,"error":error}),
            };
            let _ = request.reply.send(reply);
        }
        self.control = Some(rx);
    }

    fn action(&mut self, app: &mut WordApp, ctx: &egui::Context, action: Value) {
        self.error.clear();
        let result: Result<(), String> = (|| match action.get("kind").and_then(Value::as_str) {
            Some("openWorkbench") => {
                self.document.capture_default_target(app)?;
                self.open(app, ctx, String::new())
            },
            Some("openEdit") => {
                let ticket = random_id()?;
                let snapshot = action.get("snapshot").cloned().ok_or("原图状态缺失。")?;
                self.document.register_edit(ticket.clone(), snapshot);
                self.open(app, ctx, format!("?edit={ticket}&host=native"))
            }
            Some("confirmPick") => self.document.confirm_pick(app).map(|_| {
                app.status("图稿已插入正文；请检查排版后保存 DOCX。");
            }),
            Some("cancelPick") => { self.document.cancel_pick(); Ok(()) }
            _ => Err("未知的绘图操作。".into()),
        })();
        if let Err(error) = result { self.error = error.clone(); app.status(format!("绘图操作未完成：{error}")); }
    }

    fn open(&mut self, app: &mut WordApp, ctx: &egui::Context, query: String) -> Result<(), String> {
        if !app.has_document() { return Err("请先打开正文，再进入绘图工作台。".into()); }
        self.pending_query = query;
        if let Some(workbench) = self.workbench.as_ref().filter(|workbench| workbench.opened) {
            let query = self.pending_query.clone();
            workbench.reopen(&query)?;
            {};
            return Ok(());
        }
        if self.workbench.is_none() {
            let root = app.project_workspace.status().get("projectId").and_then(Value::as_str).filter(|id| !id.is_empty()).map(PathBuf::from);
            let directory = root.map(|root| root.join(".lingdoc").join("drawings"))
                .or_else(|| wordcraft_control_key::settings_dir().map(|path| path.join("drawings")))
                .ok_or("无法确定图稿库目录。")?;
            let (workbench, requests) = Workbench::start(&directory, ctx)?;
            self.workbench = Some(workbench);
            self.control = Some(requests);
        }
        Ok(())
    }

    fn snapshot(&self) -> Value {
        json!({"workbenchStarting":self.workbench.as_ref().is_some_and(|workbench| !workbench.opened),
            "workbenchReady":self.workbench.as_ref().is_some_and(|workbench| workbench.opened),
            "workbenchUrl":self.workbench.as_ref().map(|workbench| format!("{}/native-reading/drawing{}",workbench.origin,self.pending_query)).unwrap_or_default(),
            "error":self.error,"picker":self.document.picker_snapshot()})
    }
}
