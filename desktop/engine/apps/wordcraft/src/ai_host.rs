//! Desktop selection-AI host: one background request, one task-owned event channel.

use std::sync::mpsc::channel;
use std::time::Duration;

use wordcraft_ui_egui::ai::{AiAction, AiEvent, AiFinish, AiGenerate, AiRequest, AiResult, AiResultPhase, AiSource, AiTool};

use crate::deepseek::{
    ApiError, Citation, ClientConfig, DeepSeekClient, DeepSeekEvent, DeepSeekResult, DeepSeekStatus, FinishReason, Operation, SourceContext, Timeouts,
};

pub fn hook(ctx: &egui::Context) -> AiGenerate {
    let ctx = ctx.clone();
    Box::new(move |request| {
        let (tx, rx) = channel();
        let ctx = ctx.clone();
        std::thread::Builder::new()
            .name(format!("selection-ai-{}", request.task_id))
            .spawn(move || run(request, &ctx, tx))
            .map_err(|error| format!("无法启动选区 AI 后台任务：{error}"))?;
        Ok(rx)
    })
}

fn run(request: AiRequest, ctx: &egui::Context, tx: std::sync::mpsc::Sender<AiEvent>) {
    let task_id = request.task_id;
    let operation = match request.action {
        AiAction::Explain => Operation::Explain,
        AiAction::Summarize => Operation::Summarize,
        AiAction::Translate => Operation::Translate,
        AiAction::Polish => Operation::Polish,
        AiAction::Rewrite => Operation::Rewrite,
    };
    let config = ClientConfig {
        base_url: std::env::var("DEEPSEEK_BASE_URL").unwrap_or_else(|_| crate::deepseek::DEFAULT_BASE_URL.into()),
        model: std::env::var("DEEPSEEK_MODEL").unwrap_or_else(|_| crate::deepseek::DEFAULT_MODEL.into()),
        timeouts: Timeouts { connect: Some(Duration::from_secs(10)), total: Some(Duration::from_secs(90)) },
        ..ClientConfig::default()
    };
    let send = |event| {
        if tx.send(event).is_ok() {
            ctx.request_repaint();
        }
    };
    match DeepSeekClient::from_configured(config) {
        Ok(client) => {
            let sources: Vec<SourceContext> = request.sources.iter().map(project_source).collect();
            let stream = |event| match event {
                DeepSeekEvent::Delta { text } => send(AiEvent::Delta { task_id, text }),
                DeepSeekEvent::Tool { event } => {
                    let tool = if event.kind.contains("search") {
                        Some(AiTool::WebSearch)
                    } else if event.kind.contains("url") {
                        Some(AiTool::UrlRead)
                    } else {
                        None
                    };
                    if let Some(tool) = tool {
                        send(AiEvent::Tool { task_id, tool, sources: Vec::new() });
                    }
                }
                DeepSeekEvent::Finished { result } => send(AiEvent::Finished { task_id, result: project_result(result) }),
            };
            client.stream_chat_with_context_sources_and_instruction(
                operation,
                &request.text,
                &request.document_context,
                &sources,
                &request.custom_instruction,
                stream,
            );
        }
        Err(error) => send(AiEvent::Finished {
            task_id,
            result: AiResult {
                text: String::new(),
                tools: Vec::new(),
                sources: Vec::new(),
                phase: AiResultPhase::Failed(error.to_string()),
                finish: AiFinish::Unknown,
            },
        }),
    }
}

fn project_source(source: &wordcraft_ui_egui::ai::AiContextSource) -> SourceContext {
    SourceContext { id: source.id.clone(), title: source.title.clone(), version: source.version.clone(), text: source.text.clone() }
}

fn project_result(result: DeepSeekResult) -> AiResult {
    let filtered = result.finish == Some(FinishReason::ContentFilter);
    let phase = match result.status {
        DeepSeekStatus::Complete => AiResultPhase::Complete,
        DeepSeekStatus::Unknown => AiResultPhase::Unknown(error_message(result.error.as_ref())),
        DeepSeekStatus::Failed => AiResultPhase::Failed(error_message(result.error.as_ref())),
    };
    let finish = match result.finish {
        Some(FinishReason::Stop) => AiFinish::Stop,
        Some(FinishReason::Length) => AiFinish::Length,
        _ => AiFinish::Unknown,
    };
    let sources = result
        .partial
        .citations
        .into_iter()
        .filter_map(|citation| match citation {
            Citation::Url(citation) => Some(AiSource::Web { url: citation.url, title: citation.title.unwrap_or_default() }),
        })
        .collect();
    AiResult {
        // A content-filtered answer is not shown as a successful or partial rewrite.
        text: if filtered { String::new() } else { result.partial.content },
        tools: Vec::new(),
        sources,
        phase,
        finish,
    }
}

fn error_message(error: Option<&ApiError>) -> String {
    error.map(ToString::to_string).unwrap_or_else(|| "模型未给出可确认的完成状态。".into())
}
