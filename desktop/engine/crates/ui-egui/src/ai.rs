//! Selection AI's UI contract and task state machine.
//!
//! The host owns transport and maps its result to [`AiEvent`]. This module owns only the frozen
//! request, one in-flight task slot, and the separation between task lifetime and popover
//! visibility. It reads bounded current-document context without widening the frozen selection,
//! and never retries a request or writes document text.

use std::sync::mpsc::{Receiver, TryRecvError};

use egui::Rect;
use serde::Serialize;
use serde_json::{Value, json};
use wordcraft_engine::Selection;

use crate::WordApp;
use crate::ai_popover::AiPopoverState;
use crate::ai_toolbar::AiToolbarState;

pub const MAX_INPUT_CHARS: usize = 4000;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum AiAction {
    Explain,
    Summarize,
    Translate,
    Polish,
    Rewrite,
}

impl AiAction {
    pub const ALL: [Self; 5] = [Self::Explain, Self::Summarize, Self::Translate, Self::Polish, Self::Rewrite];

    pub fn wire_name(self) -> &'static str {
        match self {
            Self::Explain => "explain",
            Self::Summarize => "summarize",
            Self::Translate => "translate",
            Self::Polish => "polish",
            Self::Rewrite => "rewrite",
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            Self::Explain => "Explain",
            Self::Summarize => "Summarize",
            Self::Translate => "Translate",
            // Keep the editing action separate from the proofing language named "Polish".
            Self::Polish => "Polish text",
            Self::Rewrite => "Rewrite",
        }
    }
}

/// Network capabilities explicitly allowed for one request. Both default to off; neither implies
/// that the corresponding tool was actually called.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize)]
pub struct AiPermissions {
    pub web_search: bool,
    pub url_read: bool,
}

/// A request frozen at the moment the user chooses an action.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AiRequest {
    pub task_id: u32,
    pub action: AiAction,
    pub document: u64,
    pub text: String,
    /// Read-only current-document context. The explicit frozen selection in `text` remains the
    /// sole processing target; long documents are reduced to the selection's nearby section.
    pub document_context: String,
    pub permissions: AiPermissions,
    /// Project sources the user explicitly checked for this rewrite only. Resolved by the UI
    /// through the host at send time; empty for the four one-click actions.
    pub sources: Vec<AiContextSource>,
    /// Optional user-specified instruction for Rewrite (e.g. tone or length). Empty for defaults.
    pub custom_instruction: String,
}

/// One project source attached to a rewrite request: the identity the user saw plus the exact
/// full text the host read when the request was frozen. Never read implicitly.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AiContextSource {
    pub id: String,
    pub title: String,
    pub version: String,
    pub text: String,
}

/// The identity half of [`AiContextSource`], all the rewrite configuration state needs until the
/// user presses 开始改写 and the host resolves the full texts.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AiSourceRef {
    pub id: String,
    pub title: String,
    pub version: String,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
pub enum AiTool {
    WebSearch,
    UrlRead,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub enum AiSource {
    Web { url: String, title: String },
    Url { url: String },
}

impl AiSource {
    pub fn url(&self) -> &str {
        match self {
            Self::Web { url, .. } | Self::Url { url } => url,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum AiResultPhase {
    Streaming,
    Complete,
    Unknown(String),
    Failed(String),
}

impl AiResultPhase {
    pub fn wire_name(&self) -> &'static str {
        match self {
            Self::Streaming => "streaming",
            Self::Complete => "complete",
            Self::Unknown(_) => "unknown",
            Self::Failed(_) => "failed",
        }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize)]
pub enum AiFinish {
    #[default]
    Unknown,
    Stop,
    Length,
}

/// The only result projection retained by the UI. Transport-specific usage, request echoes and
/// raw JSON deliberately stop at the host boundary.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AiResult {
    pub text: String,
    pub tools: Vec<AiTool>,
    pub sources: Vec<AiSource>,
    pub phase: AiResultPhase,
    pub finish: AiFinish,
}

impl AiResult {
    pub fn streaming() -> Self {
        Self { text: String::new(), tools: Vec::new(), sources: Vec::new(), phase: AiResultPhase::Streaming, finish: AiFinish::Unknown }
    }

    pub fn push_tool(&mut self, tool: AiTool) {
        if !self.tools.contains(&tool) {
            self.tools.push(tool);
        }
    }

    pub fn push_sources(&mut self, from: Vec<AiSource>) {
        for source in from {
            if !self.sources.iter().any(|old| old.url() == source.url()) {
                self.sources.push(source);
            }
        }
    }

    fn normalize(&mut self) {
        let tools = std::mem::take(&mut self.tools);
        for tool in tools {
            self.push_tool(tool);
        }
        let sources = std::mem::take(&mut self.sources);
        self.push_sources(sources);
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum AiEvent {
    Delta { task_id: u32, text: String },
    Tool { task_id: u32, tool: AiTool, sources: Vec<AiSource> },
    Finished { task_id: u32, result: AiResult },
}

impl AiEvent {
    fn task_id(&self) -> u32 {
        match self {
            Self::Delta { task_id, .. } | Self::Tool { task_id, .. } | Self::Finished { task_id, .. } => *task_id,
        }
    }
}

/// The host starts work immediately and returns this task's own event channel. No thread or
/// transport API appears here, so the same contract remains usable by native and test hosts.
pub type AiGenerate = Box<dyn Fn(AiRequest) -> Result<Receiver<AiEvent>, String>>;

#[derive(Debug)]
pub struct AiTask {
    pub id: u32,
    pub action: AiAction,
    pub document: u64,
    /// Frozen selected text, retained for inspection and for the read-only source view.
    pub input: String,
    pub permissions: AiPermissions,
    pub result: AiResult,
    /// No product timeout is imposed by this module. Integration may inject a deadline.
    pub deadline: Option<f64>,
    rx: Option<Receiver<AiEvent>>,
}

impl AiTask {
    pub fn has_receiver(&self) -> bool {
        self.rx.is_some()
    }
}

#[derive(Debug)]
pub struct AiTaskState {
    next_id: u32,
    /// The sole task slot. Replacing it drops the old receiver, so old work has no delivery path.
    pub current: Option<AiTask>,
}

impl Default for AiTaskState {
    fn default() -> Self {
        Self { next_id: 1, current: None }
    }
}

impl AiTaskState {
    fn start(
        &mut self,
        action: AiAction,
        document: u64,
        input: String,
        document_context: String,
        permissions: AiPermissions,
        sources: Vec<AiContextSource>,
        custom_instruction: String,
        generate: &AiGenerate,
    ) -> Result<u32, String> {
        let id = self.next_id;
        self.next_id = self.next_id.checked_add(1).ok_or_else(|| "AI 任务编号已用尽。".to_string())?;
        let request = AiRequest { task_id: id, action, document, text: input.clone(), document_context, permissions, sources, custom_instruction };

        // A new invocation owns the slot even if the host rejects the request. In the latter case
        // there is no new task and the caller closes the old popover rather than showing stale text.
        self.current = None;
        let rx = generate(request)?;
        self.current = Some(AiTask { id, action, document, input, permissions, result: AiResult::streaming(), deadline: None, rx: Some(rx) });
        Ok(id)
    }

    fn accept(&mut self, event: AiEvent) -> bool {
        let event_task = event.task_id();
        let Some(task) = self.current.as_mut() else { return false };
        // Identity and terminal silence are checked before touching any result field.
        if task.id != event_task || task.result.phase != AiResultPhase::Streaming {
            return false;
        }
        match event {
            AiEvent::Delta { text, .. } => task.result.text.push_str(&text),
            AiEvent::Tool { tool, sources, .. } => {
                task.result.push_tool(tool);
                task.result.push_sources(sources);
            }
            AiEvent::Finished { mut result, .. } => {
                // The host's terminal projection is authoritative: never append it to deltas or
                // merge its sources with the provisional stream.
                result.normalize();
                task.result = result;
                task.rx = None;
            }
        }
        true
    }
}

/// All selection-AI state kept by `WordApp`. Its methods are independent of `WordApp`, making the
/// identity, visibility and timeout rules testable without a window or a real model.
#[derive(Default)]
pub struct Ai {
    pub tasks: AiTaskState,
    pub toolbar: AiToolbarState,
    pub popover: AiPopoverState,
    /// Integration may set this before submission. The default remains fully offline.
    pub permissions: AiPermissions,
}

impl Ai {
    pub fn task(&self) -> Option<&AiTask> {
        self.tasks.current.as_ref()
    }

    /// Start from an already frozen selection snapshot. `rect` is the popover's initial screen
    /// rectangle, not a live selection anchor.
    pub fn start_frozen(
        &mut self,
        action: AiAction,
        document: u64,
        selection: Selection,
        input: String,
        document_context: String,
        sources: Vec<AiContextSource>,
        rect: Option<Rect>,
        generate: &AiGenerate,
    ) -> Result<u32, String> {
        self.start_frozen_with_instruction(action, document, selection, input, document_context, sources, String::new(), rect, generate)
    }

    pub fn start_frozen_with_instruction(
        &mut self,
        action: AiAction,
        document: u64,
        selection: Selection,
        input: String,
        document_context: String,
        sources: Vec<AiContextSource>,
        custom_instruction: String,
        rect: Option<Rect>,
        generate: &AiGenerate,
    ) -> Result<u32, String> {
        check_input(&input)?;
        match self.tasks.start(action, document, input.clone(), document_context, self.permissions, sources, custom_instruction, generate) {
            Ok(id) => {
                self.popover.open(id, document, selection, input, rect);
                Ok(id)
            }
            Err(error) => {
                self.popover.close();
                Err(error)
            }
        }
    }

    /// Stop receiving further stream events for the current task, keeping any text already received.
    pub fn cancel_current(&mut self) -> bool {
        let Some(task) = self.tasks.current.as_mut() else { return false };
        if task.result.phase != AiResultPhase::Streaming {
            return false;
        }
        task.rx = None;
        task.result.phase = AiResultPhase::Complete;
        true
    }

    /// Apply an event by task id alone. Selection and document revision are intentionally absent.
    pub fn accept(&mut self, event: AiEvent) -> bool {
        self.tasks.accept(event)
    }

    /// Advance display state, drain this task's channel, and apply an optional injected deadline.
    pub fn settle_at(&mut self, document: u64, selection: &Selection, now: f64) {
        // This is the sole document-isolation check. A switched document discards old work even
        // when its popover was pinned; the old task is neither migrated nor retried.
        if self.tasks.current.as_ref().is_some_and(|task| task.document != document) {
            self.tasks.current = None;
            self.popover.close();
            return;
        }

        // Selection affects visibility only. It never participates in accept() below.
        self.popover.observe_selection(selection);

        let mut events = Vec::new();
        'drain: loop {
            let received = {
                let Some(task) = self.tasks.current.as_ref() else { break 'drain };
                if task.result.phase != AiResultPhase::Streaming {
                    break 'drain;
                }
                let Some(rx) = task.rx.as_ref() else { break 'drain };
                rx.try_recv()
            };
            match received {
                Ok(event) => events.push(event),
                Err(TryRecvError::Empty | TryRecvError::Disconnected) => break 'drain,
            }
        }
        for event in events {
            self.accept(event);
        }

        if let Some(task) = self.tasks.current.as_mut()
            && task.result.phase == AiResultPhase::Streaming
            && task.deadline.is_some_and(|deadline| now > deadline)
        {
            task.result.phase = AiResultPhase::Unknown("客户端超时".into());
            task.rx = None;
        }
    }

    pub fn close_popover(&mut self) {
        self.popover.close();
    }

    pub fn status(&self) -> Value {
        let task = self.tasks.current.as_ref().map(|task| {
            json!({
                "taskId": task.id,
                "action": task.action.wire_name(),
                "document": task.document,
                "permissions": task.permissions,
                "phase": task.result.phase.wire_name(),
                "phaseMessage": match &task.result.phase {
                    AiResultPhase::Unknown(message) | AiResultPhase::Failed(message) => Some(message),
                    AiResultPhase::Streaming | AiResultPhase::Complete => None,
                },
                "text": task.result.text,
                "tools": task.result.tools,
                "sources": task.result.sources,
                "finish": task.result.finish,
                "deadline": task.deadline,
                "receiving": task.rx.is_some(),
            })
        });
        json!({
            "task": task,
            "popover": {
                "open": self.popover.open,
                "pinned": self.popover.pinned,
                "taskId": self.popover.task,
                "document": self.popover.document,
                "source": self.popover.source,
                "message": self.popover.message,
                "configSources": self.popover.config.as_ref().map(|config| {
                    config.sources.iter().map(|source| json!({"id": source.id, "title": source.title, "version": source.version})).collect::<Vec<_>>()
                }),
            }
        })
    }
}

fn check_input(text: &str) -> Result<(), String> {
    if text.trim().is_empty() {
        return Err("请先选择要处理的文字。".into());
    }
    let count = text.chars().count();
    if count > MAX_INPUT_CHARS {
        return Err(format!("选区为 {count} 个 Unicode 字符，超过 {MAX_INPUT_CHARS} 字符上限；请缩小选区，输入未被截断。"));
    }
    Ok(())
}

const MAX_DOCUMENT_CONTEXT_CHARS: usize = 12_000;

fn current_document_context(app: &WordApp, selected_text: &str) -> String {
    let document = app.session.doc.plain_text(wordcraft_doc::StoryRef::Body);
    document_context_for_selection(&document, selected_text)
}

/// Small documents are carried whole. For a long document, retain a bounded window around the
/// exact frozen selection and prepend the nearest short heading; never turn the whole document
/// into the processing target.
fn document_context_for_selection(document: &str, selected_text: &str) -> String {
    if document.chars().count() <= MAX_DOCUMENT_CONTEXT_CHARS {
        return document.to_owned();
    }
    let document_chars = document.chars().collect::<Vec<_>>();
    let selection_start = document.find(selected_text).map(|byte| document[..byte].chars().count()).unwrap_or(0);
    let selection_len = selected_text.chars().count().min(MAX_DOCUMENT_CONTEXT_CHARS);
    let side_budget = (MAX_DOCUMENT_CONTEXT_CHARS - selection_len) / 2;
    let mut start = selection_start.saturating_sub(side_budget);
    let mut end = (start + MAX_DOCUMENT_CONTEXT_CHARS).min(document_chars.len());
    if end - start < MAX_DOCUMENT_CONTEXT_CHARS {
        start = end.saturating_sub(MAX_DOCUMENT_CONTEXT_CHARS);
        end = (start + MAX_DOCUMENT_CONTEXT_CHARS).min(document_chars.len());
    }
    let start_byte = document.char_indices().nth(start).map(|(index, _)| index).unwrap_or(document.len());
    let heading = document[..start_byte]
        .lines()
        .rev()
        .map(str::trim)
        .find(|line| {
            !line.is_empty()
                && line.chars().count() <= 80
                && (line.starts_with('#') || (line.starts_with('第') && (line.contains('章') || line.contains('节'))))
        });
    let mut context = String::new();
    if let Some(heading) = heading {
        context.push_str(heading);
        context.push('\n');
    }
    if start > 0 {
        context.push_str("……\n");
    }
    context.extend(document_chars[start..end].iter());
    if end < document_chars.len() {
        context.push_str("\n……");
    }
    context
}

/// UI command entry point. The frozen selection is the processing target; current document text
/// is read only as bounded context for terminology, references and style.
pub fn start(app: &mut WordApp, action: AiAction) -> Result<Value, String> {
    let Some(generate) = app.services.ai_generate.as_ref() else {
        let error = tl!("The current host provides no selection AI service.").to_string();
        app.status(error.clone());
        return Err(error);
    };
    let document = app.session.document_id();
    let selection = app.session.sel.clone();
    let input = app.session.selected_text();
    let document_context = current_document_context(app, &input);
    let rect = match (app.canvas.mini_anchor, app.canvas.viewport) {
        (Some(anchor), Some(viewport)) => crate::ai_popover::place(anchor, viewport),
        _ => None,
    };
    let result = app.ai.start_frozen(action, document, selection, input, document_context, Vec::new(), rect, generate);
    match result {
        Ok(task_id) => Ok(json!({"taskId": task_id, "action": action.wire_name()})),
        Err(error) => {
            app.status(error.clone());
            Err(error)
        }
    }
}

/// Rewrite-with-sources entry (approved plan B, task §8): open the popover in its configuration
/// state — frozen selection plus this task's explicitly checked sources — instead of sending.
/// The four one-click actions keep using [`start`].
pub fn open_rewrite_config(app: &mut WordApp) -> Result<Value, String> {
    if app.services.ai_generate.is_none() {
        let error = tl!("The current host provides no selection AI service.").to_string();
        app.status(error.clone());
        return Err(error);
    }
    let input = app.session.selected_text();
    if let Err(error) = check_input(&input) {
        app.status(error.clone());
        return Err(error);
    }
    let document = app.session.document_id();
    let selection = app.session.sel.clone();
    let rect = match (app.canvas.mini_anchor, app.canvas.viewport) {
        (Some(anchor), Some(viewport)) => crate::ai_popover::place(anchor, viewport),
        _ => None,
    };
    app.ai.popover.open_config(document, selection, input, rect);
    Ok(json!({"configuring": true, "action": AiAction::Rewrite.wire_name()}))
}

/// 开始改写: resolve the checked sources through the host, freeze the request and send. The
/// configuration state is consumed by this call either way — it never outlives the task.
pub fn begin_rewrite(app: &mut WordApp) -> Result<Value, String> {
    let Some(generate) = app.services.ai_generate.as_ref() else {
        let error = tl!("The current host provides no selection AI service.").to_string();
        app.status(error.clone());
        return Err(error);
    };
    let (selection, input, refs, custom_instruction, frozen_document) = {
        let popover = &app.ai.popover;
        let Some(config) = popover.config.clone() else {
            let error = tl!("The rewrite configuration was closed.").to_string();
            app.status(error.clone());
            return Err(error);
        };
        (
            popover.selection.clone().unwrap_or_default(),
            popover.source.clone(),
            config.sources,
            config.custom_instruction.trim().to_owned(),
            popover.document,
        )
    };
    // The configuration is bound to the document it was opened on; never send its frozen
    // selection against a different document.
    if frozen_document != app.session.document_id() {
        cancel_rewrite_config(app);
        let error = tl!("The document was switched; the rewrite configuration is stale.").to_string();
        app.status(error.clone());
        return Err(error);
    }
    let sources = if refs.is_empty() {
        Vec::new()
    } else {
        let Some(resolve) = app.services.ai_sources.as_ref() else {
            let error = tl!("The current host provides no source resolution service.").to_string();
            app.status(error.clone());
            return Err(error);
        };
        let keys: Vec<(String, String)> = refs.iter().map(|r| (r.id.clone(), r.version.clone())).collect();
        match resolve(&keys) {
            Ok(sources) => sources,
            Err(error) => {
                app.status(error.clone());
                return Err(error);
            }
        }
    };
    let document = app.session.document_id();
    let document_context = current_document_context(app, &input);
    let rect = app.ai.popover.rect;
    match app.ai.start_frozen_with_instruction(
        AiAction::Rewrite,
        document,
        selection,
        input,
        document_context,
        sources,
        custom_instruction,
        rect,
        generate,
    ) {
        Ok(task_id) => Ok(json!({"taskId": task_id, "action": AiAction::Rewrite.wire_name()})),
        Err(error) => {
            app.status(error.clone());
            Err(error)
        }
    }
}

pub fn cancel_rewrite_config(app: &mut WordApp) {
    app.ai.popover.close();
}

pub fn settle(app: &mut WordApp, ctx: &egui::Context) {
    let now = ctx.input(|input| input.time);
    settle_at(app, now);
}

pub fn settle_at(app: &mut WordApp, now: f64) {
    let document = app.session.document_id();
    let selection = app.session.sel.clone();
    app.ai.settle_at(document, &selection, now);
}

pub fn accept(app: &mut WordApp, event: AiEvent) -> bool {
    app.ai.accept(event)
}

/// Close only the visual surface. The task and its receiver remain alive.
pub fn close_popover(app: &mut WordApp) {
    app.ai.close_popover();
}

pub fn status(app: &WordApp) -> Value {
    app.ai.status()
}

#[cfg(test)]
mod tests {
    use std::cell::RefCell;
    use std::rc::Rc;
    use std::sync::mpsc::{Sender, channel};

    use wordcraft_doc::Pos;

    use super::*;

    type Requests = Rc<RefCell<Vec<AiRequest>>>;
    type Senders = Rc<RefCell<Vec<Sender<AiEvent>>>>;

    fn host() -> (AiGenerate, Requests, Senders) {
        let requests = Requests::default();
        let senders = Senders::default();
        let requests_hook = requests.clone();
        let senders_hook = senders.clone();
        let generate: AiGenerate = Box::new(move |request| {
            let (tx, rx) = channel();
            requests_hook.borrow_mut().push(request);
            senders_hook.borrow_mut().push(tx);
            Ok(rx)
        });
        (generate, requests, senders)
    }

    fn selection(end: usize) -> Selection {
        Selection { anchor: Pos::body(0, 0), focus: Pos::body(0, end) }
    }

    fn rect() -> Option<Rect> {
        Some(Rect::from_min_size(egui::pos2(20.0, 30.0), egui::vec2(420.0, 360.0)))
    }

    fn start(ai: &mut Ai, generate: &AiGenerate, action: AiAction, text: &str) -> u32 {
        ai.start_frozen(action, 7, selection(3), text.to_string(), "测试文档上下文".into(), Vec::new(), rect(), generate).expect("test request starts")
    }

    fn complete(text: &str) -> AiResult {
        AiResult { text: text.into(), tools: Vec::new(), sources: Vec::new(), phase: AiResultPhase::Complete, finish: AiFinish::Stop }
    }

    fn rewrite_app(generate: AiGenerate, resolve: Option<crate::AiSourceResolve>) -> crate::WordApp {
        let mut services = crate::Services::default();
        services.ai_generate = Some(generate);
        if let Some(resolve) = resolve {
            services.ai_sources = Some(resolve);
        }
        crate::WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()), services)
    }

    #[test]
    fn rewrite_config_resolves_checked_sources_then_frees_the_request() {
        let (generate, requests, _) = host();
        let resolve: crate::AiSourceResolve = Box::new(|keys: &[(String, String)]| {
            Ok(keys
                .iter()
                .map(|(id, version)| AiContextSource {
                    id: id.clone(), title: "标题".into(), version: version.clone(), text: format!("全文-{id}")
                })
                .collect())
        });
        let mut app = rewrite_app(generate, Some(resolve));
        app.ai.popover.open_config(app.session.document_id(), selection(3), "选文".into(), None);
        app.ai.popover.config.as_mut().expect("config state").sources.push(AiSourceRef {
            id: "s1".into(),
            title: "资料一".into(),
            version: "2".into(),
        });

        let result = super::begin_rewrite(&mut app);
        assert!(result.is_ok(), "begin_rewrite: {:?}", result.err());
        assert_eq!(result.unwrap()["action"], "rewrite");
        let request = &requests.borrow()[0];
        assert_eq!(request.text, "选文");
        assert_eq!(request.sources.len(), 1);
        assert_eq!(request.sources[0].id, "s1");
        assert_eq!(request.sources[0].text, "全文-s1");
        // The configuration state is consumed: the popover shows the task, nothing lingers.
        assert!(app.ai.popover.config.is_none());
        assert!(app.ai.popover.task.is_some());
    }

    #[test]
    fn rewrite_config_survives_a_failed_resolve_and_sends_nothing() {
        let (generate, requests, _) = host();
        let resolve: crate::AiSourceResolve = Box::new(|_| Err("资料不存在或版本已变化".into()));
        let mut app = rewrite_app(generate, Some(resolve));
        app.ai.popover.open_config(app.session.document_id(), selection(3), "选文".into(), None);
        app.ai.popover.config.as_mut().expect("config state").sources.push(AiSourceRef {
            id: "gone".into(),
            title: "旧资料".into(),
            version: "1".into(),
        });

        let result = super::begin_rewrite(&mut app);
        assert!(result.is_err());
        assert!(requests.borrow().is_empty(), "a failed resolve must not reach the host");
        assert!(app.ai.popover.config.is_some(), "the user keeps the configure surface to adjust");
        assert!(app.ai.popover.task.is_none());
    }

    #[test]
    fn rewrite_config_without_sources_sends_the_plain_request() {
        let (generate, requests, _) = host();
        let mut app = rewrite_app(generate, None);
        app.ai.popover.open_config(app.session.document_id(), selection(3), "选文".into(), None);

        let result = super::begin_rewrite(&mut app);
        assert!(result.is_ok(), "begin_rewrite: {:?}", result.err());
        assert!(requests.borrow()[0].sources.is_empty());
        assert!(app.ai.popover.config.is_none());
        assert!(app.ai.popover.task.is_some());
    }

    #[test]
    fn rewrite_config_dies_when_the_document_is_replaced() {
        let (generate, requests, _) = host();
        let resolve: crate::AiSourceResolve = Box::new(|keys| {
            Ok(keys
                .iter()
                .map(|(id, version)| AiContextSource { id: id.clone(), title: "标题".into(), version: version.clone(), text: "全文".into() })
                .collect())
        });
        let mut app = rewrite_app(generate, Some(resolve));
        let document = app.session.document_id();
        app.ai.popover.open_config(document, selection(3), "选文".into(), None);
        app.ai.popover.config.as_mut().expect("config state").sources.push(AiSourceRef {
            id: "s1".into(),
            title: "资料一".into(),
            version: "2".into(),
        });
        drop(app.run("file.new", json!({})).expect("file.new"));
        assert_ne!(app.session.document_id(), document, "the test must actually replace the document");

        let result = super::begin_rewrite(&mut app);
        assert!(result.is_err(), "a replaced document must not send the frozen selection");
        assert!(requests.borrow().is_empty(), "nothing reaches the host");
        assert!(app.ai.popover.config.is_none(), "the stale configuration is retired");
    }

    #[test]
    fn every_action_freezes_document_context_without_widening_the_selection_target() {
        let (generate, requests, _) = host();
        let mut ai = Ai::default();
        for action in AiAction::ALL {
            start(&mut ai, &generate, action, "唯一处理选文");
        }
        let requests = requests.borrow();
        assert_eq!(requests.len(), AiAction::ALL.len());
        for (request, action) in requests.iter().zip(AiAction::ALL) {
            assert_eq!(request.action, action);
            assert_eq!(request.text, "唯一处理选文");
            assert_eq!(request.document_context, "测试文档上下文");
        }
    }

    #[test]
    fn long_document_context_is_bounded_around_selection_with_nearest_heading() {
        let document = format!(
            "{}\n# 研究方法\n{}\n需要解释的选文\n{}",
            "前置铺垫。".repeat(2_000),
            "选文前的邻近论述。".repeat(600),
            "选文后的补充论述。".repeat(600)
        );
        let context = super::document_context_for_selection(&document, "需要解释的选文");
        assert!(context.contains("# 研究方法"));
        assert!(context.contains("需要解释的选文"));
        assert!(context.contains("……"));
        assert!(context.chars().count() <= super::MAX_DOCUMENT_CONTEXT_CHARS + 84);
        assert!(context.chars().count() < document.chars().count());
        assert_eq!(super::document_context_for_selection("短文档全文", "选文"), "短文档全文");
    }

    #[test]
    fn unicode_limit_accepts_4000_and_rejects_4001_without_calling_or_truncating() {
        let (generate, requests, _) = host();
        let mut ai = Ai::default();
        let accepted = "字".repeat(MAX_INPUT_CHARS);
        start(&mut ai, &generate, AiAction::Explain, &accepted);
        assert_eq!(requests.borrow()[0].text, accepted);

        let mut rejected = Ai::default();
        let too_long = "字".repeat(MAX_INPUT_CHARS + 1);
        let error = rejected.start_frozen(AiAction::Explain, 7, selection(3), too_long, "测试文档上下文".into(), Vec::new(), rect(), &generate);
        assert!(error.is_err());
        assert_eq!(requests.borrow().len(), 1, "rejection must not invoke the host");
        assert!(rejected.tasks.current.is_none());
        assert!(!rejected.popover.open);
    }

    #[test]
    fn explicit_input_may_cross_paragraphs_and_permissions_default_off() {
        let (generate, requests, _) = host();
        let mut ai = Ai::default();
        let text = "第一段\n第二段\n第三段";
        start(&mut ai, &generate, AiAction::Summarize, text);
        assert_eq!(requests.borrow()[0].text, text);
        assert_eq!(requests.borrow()[0].permissions, AiPermissions { web_search: false, url_read: false });
        assert_eq!(requests.borrow()[0].document, 7);
    }

    #[test]
    fn whitespace_input_is_rejected_before_replacing_existing_task() {
        let (generate, requests, _) = host();
        let mut ai = Ai::default();
        let id = start(&mut ai, &generate, AiAction::Explain, "保留任务");
        let result = ai.start_frozen(AiAction::Rewrite, 7, selection(3), " \n\t".into(), String::new(), Vec::new(), None, &generate);
        assert!(result.is_err());
        assert_eq!(ai.tasks.current.as_ref().map(|task| task.id), Some(id));
        assert_eq!(requests.borrow().len(), 1);
        assert!(ai.popover.open);
    }

    #[test]
    fn late_task_id_cannot_change_new_result_or_popover() {
        let (generate, _, _) = host();
        let mut ai = Ai::default();
        let old = start(&mut ai, &generate, AiAction::Explain, "旧选文");
        let new = start(&mut ai, &generate, AiAction::Translate, "新选文");
        let before_result = ai.tasks.current.as_ref().map(|task| task.result.clone());
        let before_popover = ai.popover.clone();

        assert!(!ai.accept(AiEvent::Delta { task_id: old, text: "旧迟到增量".into() }));
        assert!(!ai.accept(AiEvent::Finished { task_id: old, result: complete("旧迟到完成") }));
        assert_eq!(ai.tasks.current.as_ref().map(|task| task.id), Some(new));
        assert_eq!(ai.tasks.current.as_ref().map(|task| task.result.clone()), before_result);
        assert_eq!(ai.popover, before_popover);
    }

    #[test]
    fn terminal_result_is_silent_to_every_later_event() {
        let (generate, _, _) = host();
        let mut ai = Ai::default();
        let id = start(&mut ai, &generate, AiAction::Polish, "选文");
        assert!(ai.accept(AiEvent::Delta { task_id: id, text: "部分".into() }));
        assert!(ai.accept(AiEvent::Finished { task_id: id, result: complete("完整") }));
        let finished = ai.tasks.current.as_ref().map(|task| task.result.clone());
        assert!(!ai.tasks.current.as_ref().is_some_and(AiTask::has_receiver));

        assert!(!ai.accept(AiEvent::Delta { task_id: id, text: "迟到".into() }));
        assert!(!ai.accept(AiEvent::Tool { task_id: id, tool: AiTool::WebSearch, sources: Vec::new() }));
        assert!(!ai.accept(AiEvent::Finished { task_id: id, result: complete("二次完成") }));
        assert_eq!(ai.tasks.current.as_ref().map(|task| task.result.clone()), finished);
    }

    #[test]
    fn finished_overwrites_body_tools_and_sources_then_deduplicates_by_url() {
        let (generate, _, _) = host();
        let mut ai = Ai::default();
        let id = start(&mut ai, &generate, AiAction::Explain, "选文");
        ai.accept(AiEvent::Delta { task_id: id, text: "不应保留的增量".into() });
        ai.accept(AiEvent::Tool { task_id: id, tool: AiTool::UrlRead, sources: vec![AiSource::Url { url: "https://old.example".into() }] });
        let url = "https://example.com/a";
        let result = AiResult {
            text: "宿主完整正文".into(),
            tools: vec![AiTool::WebSearch, AiTool::WebSearch, AiTool::UrlRead],
            sources: vec![
                AiSource::Web { url: url.into(), title: "首次标题".into() },
                AiSource::Web { url: url.into(), title: "重复标题".into() },
                AiSource::Url { url: url.into() },
            ],
            phase: AiResultPhase::Complete,
            finish: AiFinish::Stop,
        };
        assert!(ai.accept(AiEvent::Finished { task_id: id, result }));
        let task = ai.tasks.current.as_ref().expect("current task");
        assert_eq!(task.result.text, "宿主完整正文");
        assert_eq!(task.result.tools, vec![AiTool::WebSearch, AiTool::UrlRead]);
        assert_eq!(task.result.sources, vec![AiSource::Web { url: url.into(), title: "首次标题".into() }]);
        assert!(!task.result.sources.iter().any(|source| source.url() == "https://old.example"));
    }

    #[test]
    fn unpinned_selection_change_closes_but_completion_does_not_reopen() {
        let (generate, _, _) = host();
        let mut ai = Ai::default();
        let id = start(&mut ai, &generate, AiAction::Explain, "冻结选文");
        ai.settle_at(7, &selection(20), 0.0);
        assert!(!ai.popover.open);
        assert_eq!(ai.tasks.current.as_ref().map(|task| task.result.phase.clone()), Some(AiResultPhase::Streaming));

        assert!(ai.accept(AiEvent::Finished { task_id: id, result: complete("后台完成") }));
        ai.settle_at(7, &selection(20), 1.0);
        assert_eq!(ai.tasks.current.as_ref().map(|task| task.result.text.as_str()), Some("后台完成"));
        assert!(!ai.popover.open);
    }

    #[test]
    fn pin_survives_selection_change_and_new_task_resets_it() {
        let (generate, _, _) = host();
        let mut ai = Ai::default();
        let old = start(&mut ai, &generate, AiAction::Explain, "第一份选文");
        ai.popover.toggle_pin();
        let old_rect = ai.popover.rect;
        ai.settle_at(7, &selection(30), 0.0);
        assert!(ai.popover.open && ai.popover.pinned);
        assert_eq!(ai.popover.rect, old_rect);
        assert_eq!(ai.popover.source, "第一份选文");
        assert_eq!(ai.tasks.current.as_ref().map(|task| task.id), Some(old));

        let new = start(&mut ai, &generate, AiAction::Rewrite, "第二份选文");
        assert_ne!(new, old);
        assert!(ai.popover.open && !ai.popover.pinned);
        assert_eq!(ai.popover.task, Some(new));
        assert_eq!(ai.popover.source, "第二份选文");
    }

    #[test]
    fn close_does_not_cancel_and_finished_does_not_reopen() {
        let (generate, _, senders) = host();
        let mut ai = Ai::default();
        let id = start(&mut ai, &generate, AiAction::Explain, "选文");
        ai.close_popover();
        assert!(!ai.popover.open);
        assert!(ai.tasks.current.as_ref().is_some_and(AiTask::has_receiver));

        senders.borrow()[0].send(AiEvent::Finished { task_id: id, result: complete("仍已接收") }).expect("send finished");
        ai.settle_at(7, &selection(3), 0.0);
        assert_eq!(ai.tasks.current.as_ref().map(|task| task.result.phase.clone()), Some(AiResultPhase::Complete));
        assert_eq!(ai.tasks.current.as_ref().map(|task| task.result.text.as_str()), Some("仍已接收"));
        assert!(!ai.popover.open);
    }

    #[test]
    fn document_switch_discards_even_pinned_task_and_old_events_have_no_slot() {
        let (generate, _, _) = host();
        let mut ai = Ai::default();
        let id = start(&mut ai, &generate, AiAction::Explain, "旧文档选文");
        ai.popover.toggle_pin();
        ai.settle_at(8, &selection(3), 0.0);
        assert!(ai.tasks.current.is_none());
        assert!(!ai.popover.open);
        assert!(!ai.accept(AiEvent::Delta { task_id: id, text: "旧文档迟到".into() }));
    }

    #[test]
    fn timeout_is_unknown_preserves_partial_results_and_never_retries() {
        let (generate, requests, _) = host();
        let mut ai = Ai::default();
        let id = start(&mut ai, &generate, AiAction::Explain, "选文");
        ai.accept(AiEvent::Delta { task_id: id, text: "部分正文".into() });
        ai.accept(AiEvent::Tool {
            task_id: id,
            tool: AiTool::WebSearch,
            sources: vec![AiSource::Web { url: "https://example.com".into(), title: "来源".into() }],
        });
        if let Some(task) = ai.tasks.current.as_mut() {
            task.deadline = Some(5.0);
        }
        ai.settle_at(7, &selection(3), 5.0);
        assert_eq!(ai.tasks.current.as_ref().map(|task| task.result.phase.clone()), Some(AiResultPhase::Streaming));
        ai.settle_at(7, &selection(3), 5.1);
        let task = ai.tasks.current.as_ref().expect("timed out task remains");
        assert_eq!(task.result.phase, AiResultPhase::Unknown("客户端超时".into()));
        assert_eq!(task.result.text, "部分正文");
        assert_eq!(task.result.sources.len(), 1);
        assert!(!task.has_receiver());
        assert!(!ai.accept(AiEvent::Delta { task_id: id, text: "超时后迟到".into() }));
        assert_eq!(requests.borrow().len(), 1, "timeout must not retry");
    }

    #[test]
    fn failed_and_unknown_terminal_projections_remain_distinct() {
        let (generate, _, _) = host();
        let mut failed = Ai::default();
        let failed_id = start(&mut failed, &generate, AiAction::Explain, "选文");
        failed.accept(AiEvent::Finished {
            task_id: failed_id,
            result: AiResult {
                text: String::new(),
                tools: Vec::new(),
                sources: Vec::new(),
                phase: AiResultPhase::Failed("鉴权失败".into()),
                finish: AiFinish::Unknown,
            },
        });
        assert_eq!(failed.tasks.current.as_ref().map(|task| task.result.phase.clone()), Some(AiResultPhase::Failed("鉴权失败".into())));

        let mut unknown = Ai::default();
        let unknown_id = start(&mut unknown, &generate, AiAction::Explain, "选文");
        unknown.accept(AiEvent::Finished {
            task_id: unknown_id,
            result: AiResult {
                text: "已有部分".into(),
                tools: Vec::new(),
                sources: Vec::new(),
                phase: AiResultPhase::Unknown("传输中断".into()),
                finish: AiFinish::Unknown,
            },
        });
        let task = unknown.tasks.current.as_ref().expect("unknown task");
        assert_eq!(task.result.phase, AiResultPhase::Unknown("传输中断".into()));
        assert_eq!(task.result.text, "已有部分");
    }

    #[test]
    fn host_submission_error_leaves_no_task_or_popover_and_does_not_retry() {
        let calls = Rc::new(RefCell::new(0));
        let calls_hook = calls.clone();
        let generate: AiGenerate = Box::new(move |_| {
            *calls_hook.borrow_mut() += 1;
            Err("请求未发出".into())
        });
        let mut ai = Ai::default();
        let result = ai.start_frozen(AiAction::Explain, 7, selection(3), "选文".into(), "测试文档上下文".into(), Vec::new(), rect(), &generate);
        assert_eq!(result, Err("请求未发出".into()));
        assert!(ai.tasks.current.is_none());
        assert!(!ai.popover.open);
        ai.settle_at(7, &selection(3), 100.0);
        assert_eq!(*calls.borrow(), 1);
    }
}
