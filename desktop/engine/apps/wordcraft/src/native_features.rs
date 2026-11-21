//! Desktop integration for project-source Q&A and native AI drawing.

use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::mpsc::{Receiver, TryRecvError};

use serde_json::Value;
use wordcraft_ui_egui::{FileDialogAnswer, WordApp, ai::AiContextSource, source_workspace::SourceInput};

use crate::deepseek::{ChatRequest, ClientConfig, DeepSeekEvent, DeepSeekResult, DeepSeekStatus, FinishReason, PartialChatResult};
use crate::drawing_runtime::{DrawingCommands, DrawingRuntime};
use crate::file_dialogs::Launcher;
use crate::project_ai_host::{ProjectAiEffect, ProjectAiHost, ProjectAiTransport, ProjectDescriptor, SourceTextIndex};

/// The exact text budget one checked source may contribute to a rewrite request. It matches the
/// built-in reader's plain-text limit, so a source readable in the app is attachable as-is.
const MAX_AI_SOURCE_TEXT: usize = 2 * 1024 * 1024;

pub struct NativeFeatures {
    project_ai: ProjectAiHost,
    drawing: DrawingRuntime,
    project_id: String,
    import: Option<Receiver<FileDialogAnswer>>,
    /// Identity → on-disk location for the sources a rewrite may explicitly check (plan B).
    /// Attached to the Q&A host (which rebuilds it) and to the ai_sources service (which reads it).
    source_index: SourceTextIndex,
}

impl NativeFeatures {
    pub fn new(drawing_commands: DrawingCommands) -> Self {
        let source_index = SourceTextIndex::default();
        let mut project_ai = if std::env::var("WORDCRAFT_PROJECT_AI_MOCK").as_deref() == Ok("1") {
            ProjectAiHost::with_transport(Arc::new(LocalProjectAi))
        } else {
            ProjectAiHost::new(ClientConfig::default())
        };
        project_ai.attach_source_index(source_index.clone());
        Self { project_ai, drawing: DrawingRuntime::new(drawing_commands), project_id: String::new(), import: None, source_index }
    }

    pub fn tick(&mut self, app: &mut WordApp, dialogs: &Launcher, ctx: &egui::Context, frame: &eframe::Frame) {
        self.poll_import();
        self.sync_project(app);
        self.project_ai.poll();
        self.attach_ai_sources(app);
        for action in app.project_workspace.take_actions() {
            // Backstage project entries reuse the existing native commands and directory dialogs;
            // they do not create a second project workflow in the shared workspace component.
            match action.get("kind").and_then(Value::as_str) {
                Some("enterProject") => {
                    enter_project_workspace(app, &action);
                    continue;
                }
                Some("newProject") => {
                    if let Err(error) = app.run("file.newProject", serde_json::json!({})) {
                        app.status(format!("新建项目未完成：{error}"));
                    }
                    continue;
                }
                Some("openProject") => {
                    if let Err(error) = app.run("file.openProject", serde_json::json!({})) {
                        app.status(format!("打开项目未完成：{error}"));
                    }
                    continue;
                }
                _ => {}
            }
            if !is_project_ai_action(&action) {
                continue;
            }
            let live_document_text = (action.get("kind").and_then(Value::as_str) == Some("send"))
                .then(|| app.session.doc.plain_text(wordcraft_engine::doc::StoryRef::Body));
            match self.project_ai.apply_action_with_document_text(&action, live_document_text) {
                Ok(effects) => {
                    for effect in effects {
                        match effect {
                            ProjectAiEffect::ChooseImportFiles if self.import.is_none() => {
                                self.import = Some(dialogs.pick_project_sources(frame));
                            }
                            ProjectAiEffect::ChooseImportFiles => {}
                            ProjectAiEffect::OpenSource { source_id, path } => open_source(app, &source_id, &path),
                        }
                    }
                }
                Err(error) => app.status(format!("资料问答操作未完成：{error}")),
            }
        }
        match serde_json::to_value(self.project_ai.snapshot()) {
            Ok(snapshot) => {
                if let Err(error) = app.project_workspace.hydrate(snapshot) {
                    log::error!("project AI snapshot: {error}");
                    app.status("资料问答状态暂时无法显示，请重开项目。");
                }
            }
            Err(error) => {
                log::error!("project AI snapshot: {error}");
                app.status("资料问答状态暂时无法序列化。");
            }
        }
        if self.project_ai.snapshot().busy {
            ctx.request_repaint_after(std::time::Duration::from_millis(80));
        }
        self.drawing.tick(app, ctx);
    }

    /// Selection-AI rewrite plan B (task §8): offer the frozen-request path that turns the
    /// sources the user explicitly checked into full texts. Installed once; the closure reads
    /// through the shared index, which the Q&A host keeps current, and never caches texts.
    fn attach_ai_sources(&mut self, app: &mut WordApp) {
        if app.services.ai_sources.is_some() {
            return;
        }
        let index = self.source_index.clone();
        app.services.ai_sources = Some(Box::new(move |keys| {
            keys.iter()
                .map(|(id, version)| {
                    let entry = index.get(id, version).ok_or_else(|| format!("资料不存在或版本已变化：{id} · {version}"))?;
                    let text = source_text_for_ai(&entry.path)?;
                    Ok(AiContextSource { id: id.clone(), title: entry.title, version: version.clone(), text })
                })
                .collect()
        }));
    }

    fn sync_project(&mut self, app: &mut WordApp) {
        let status = app.project_workspace.status();
        let id = status.get("projectId").and_then(Value::as_str).unwrap_or_default().to_owned();
        if id == self.project_id {
            return;
        }
        self.project_id = id.clone();
        self.import = None;
        if id.is_empty() {
            self.project_ai.close_project();
            return;
        }
        let descriptor = ProjectDescriptor {
            root: PathBuf::from(&id),
            id,
            title: status.get("projectTitle").and_then(Value::as_str).unwrap_or("未命名项目").to_owned(),
            document_title: status.get("documentTitle").and_then(Value::as_str).unwrap_or("主文稿.docx").to_owned(),
            document_path: app.main_document_path().unwrap_or_default(),
        };
        if let Err(error) = self.project_ai.open_project(descriptor) {
            log::warn!("project AI open: {error}");
            app.status(format!("项目资料与会话未能载入：{error}"));
        }
    }

    fn poll_import(&mut self) {
        let received = match self.import.as_ref().map(Receiver::try_recv) {
            None | Some(Err(TryRecvError::Empty)) => return,
            Some(Err(TryRecvError::Disconnected)) => {
                self.import = None;
                self.project_ai.cancel_import();
                return;
            }
            Some(Ok(answer)) => answer,
        };
        self.import = None;
        match received {
            Ok(paths) if paths.is_empty() => self.project_ai.cancel_import(),
            Ok(paths) => {
                let paths = paths.into_iter().map(PathBuf::from).collect::<Vec<_>>();
                if let Err(error) = self.project_ai.import_sources(&paths) {
                    self.project_ai.cancel_import();
                    log::warn!("project source import: {error}");
                }
            }
            Err(error) => {
                self.project_ai.cancel_import();
                log::warn!("project source picker: {error}");
            }
        }
    }
}

fn enter_project_workspace(app: &mut WordApp, action: &Value) {
    app.project_workspace.project_selected = true;
    app.ui.backstage = false;
    app.canvas.want_focus = true;
    if action.get("focus").and_then(Value::as_str) == Some("sources") {
        app.project_workspace.resources_open = true;
        app.project_workspace.chat_open = false;
    }
}

#[cfg(test)]
mod project_navigation_tests {
    use super::*;

    fn app() -> WordApp {
        let document = wordcraft_engine::blank_document(&wordcraft_engine::Region::default());
        WordApp::new(wordcraft_engine::Session::new(document), wordcraft_ui_egui::Services::default())
    }

    #[test]
    fn enter_project_returns_to_the_editor_without_reopening_the_document() {
        let mut app = app();
        app.ui.backstage = true;
        app.canvas.want_focus = false;
        app.project_workspace.project_selected = false;
        app.project_workspace.resources_open = false;
        app.project_workspace.chat_open = true;

        enter_project_workspace(&mut app, &serde_json::json!({"kind":"enterProject","projectId":"p1"}));

        assert!(!app.ui.backstage);
        assert!(app.canvas.want_focus);
        assert!(app.project_workspace.project_selected);
        assert!(!app.project_workspace.resources_open);
        assert!(app.project_workspace.chat_open);
    }

    #[test]
    fn enter_project_with_source_focus_opens_only_the_source_panel() {
        let mut app = app();
        app.ui.backstage = true;
        app.project_workspace.resources_open = false;
        app.project_workspace.chat_open = true;

        enter_project_workspace(&mut app, &serde_json::json!({"kind":"enterProject","projectId":"p1","focus":"sources"}));

        assert!(!app.ui.backstage);
        assert!(app.project_workspace.resources_open);
        assert!(!app.project_workspace.chat_open);
    }
}

fn is_project_ai_action(action: &Value) -> bool {
    matches!(
        action.get("kind").and_then(Value::as_str),
        Some("send" | "cancelQuestion" | "clearUncertain" | "newSession" | "selectSession" | "importSources" | "openSource" | "refreshState")
    )
}

fn open_source(app: &mut WordApp, source_id: &str, path: &Path) {
    let status = app.project_workspace.status();
    let source = status
        .get("sources")
        .and_then(Value::as_array)
        .and_then(|sources| sources.iter().find(|source| source.get("id").and_then(Value::as_str) == Some(source_id)));
    let title = source
        .and_then(|source| source.get("title"))
        .and_then(Value::as_str)
        .unwrap_or_else(|| path.file_name().and_then(|name| name.to_str()).unwrap_or("资料"));
    let version = source.and_then(|source| source.get("version")).and_then(Value::as_str).unwrap_or("1");
    let extension = path.extension().and_then(|ext| ext.to_str()).unwrap_or_default().to_ascii_lowercase();
    let input = if extension == "pdf" {
        let pages = match crate::pdf_source::render_pdf_pages(path) {
            Ok(pages) => pages,
            Err(error) => {
                app.status(format!("PDF 只读预览未完成：{error}"));
                return;
            }
        };
        SourceInput::Pages { id: source_id.to_owned(), title: title.to_owned(), version: version.to_owned(), pages }
    } else {
        let text = match source_text(path) {
            Ok(text) => text,
            Err(error) => {
                app.status(format!("资料只读预览未完成：{error}"));
                return;
            }
        };
        SourceInput::Text { id: source_id.to_owned(), title: title.to_owned(), version: version.to_owned(), text }
    };
    let ticket = app.source_workspace.begin_open();
    if let Err(error) = app.source_workspace.commit_open(ticket, input, "source", None) {
        app.status(format!("资料只读预览未完成：{error}"));
    }
}

fn source_text_for_ai(path: &Path) -> Result<String, String> {
    // Reject oversized sources before reading or parsing: metadata is cheap, and the post-hoc
    // length check below stays as a second fence (e.g. highly compressed DOCX).
    let oversized = std::fs::metadata(path).map(|meta| meta.len() > MAX_AI_SOURCE_TEXT as u64).unwrap_or(false);
    if oversized {
        return Err(format!("{} 的全文超过 2 MiB 上限，暂不能用于改写", path.display()));
    }
    let text = source_text(path)?;
    if text.len() > MAX_AI_SOURCE_TEXT {
        return Err(format!("{} 的全文超过 2 MiB 上限，暂不能用于改写", path.display()));
    }
    Ok(text)
}

fn source_text(path: &Path) -> Result<String, String> {
    match path.extension().and_then(|extension| extension.to_str()).unwrap_or_default().to_ascii_lowercase().as_str() {
        "txt" | "md" | "markdown" => std::fs::read_to_string(path).map_err(|error| format!("{}：{error}", path.display())),
        "docx" => wordcraft_engine::io::open_path(path)
            .map(|document| document.plain_text(wordcraft_engine::doc::StoryRef::Body))
            .map_err(|error| format!("DOCX 解析失败：{error}")),
        "pdf" => match crate::pdf_source::extract_pdf_text(path)? {
            crate::pdf_source::PdfTextResult::Text(text) => Ok(text),
            crate::pdf_source::PdfTextResult::ScannedOnly { .. } => {
                Err("该 PDF 为纯扫描图片型文档，仅支持阅读预览，暂不支持用于改写参考".into())
            }
        },
        _ => Err("该资料格式不支持原生只读预览".into()),
    }
}

struct LocalProjectAi;

impl ProjectAiTransport for LocalProjectAi {
    fn stream(&self, request: ChatRequest, on_event: &mut dyn FnMut(DeepSeekEvent)) -> DeepSeekResult {
        let question =
            request.messages().iter().rev().find(|message| message.role == "user").map(|message| message.content.as_str()).unwrap_or_default();
        let rounds = request.messages().iter().filter(|message| message.role == "user").count();
        let has_context = request.messages().first().is_some_and(|message| message.content.contains("【项目上下文】"));
        let answer = format!(
            "本地 Mock 已收到第 {rounds} 轮问题：{question}\n\n{}这条回答只用于验证原生资料问答链路，不是 DeepSeek 的真实回答。",
            if has_context {
                "请求中包含当前主文档或项目资料内容。"
            } else {
                "请求中没有项目资料；仍可处理一般问题。"
            }
        );
        let midpoint = answer.char_indices().nth(answer.chars().count() / 2).map(|(index, _)| index).unwrap_or(answer.len());
        for chunk in [&answer[..midpoint], &answer[midpoint..]] {
            if !chunk.is_empty() {
                on_event(DeepSeekEvent::Delta { text: chunk.to_owned() });
                std::thread::sleep(std::time::Duration::from_millis(80));
            }
        }
        DeepSeekResult {
            status: DeepSeekStatus::Complete,
            partial: PartialChatResult { content: answer, ..PartialChatResult::default() },
            finish: Some(FinishReason::Stop),
            error: None,
            id: None,
            model: Some("local-mock".into()),
            done_marker_missing: false,
            received_chunks: 2,
        }
    }
}
