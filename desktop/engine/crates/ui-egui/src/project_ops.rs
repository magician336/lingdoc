//! Native project opening and the UI side of its single-in-flight save transaction.

use std::path::PathBuf;
use std::sync::Arc;

use serde_json::{Value, json};
use wordcraft_engine::project::{ProjectBinding, ProjectError};
use wordcraft_engine::save::{SaveFailure, SaveOutcome};

use crate::file_dialogs::{AfterPick, Asked};
use crate::{FileDialogRequest, WordApp, dialogs, now_ms};

impl WordApp {
    /// Open and lock a project fully before replacing the current binding. A failed directory
    /// decision, lock acquisition, worker start or DOCX read leaves the current project untouched.
    pub fn open_project(&mut self, root: PathBuf, wake: Arc<dyn Fn() + Send + Sync>) -> Result<Value, String> {
        if self.save_unconfirmed() {
            return Err("上一次保存尚未确认，请先等待或查询原保存结果，再打开其他项目。".into());
        }
        let binding = ProjectBinding::open(root).map_err(project_error)?;
        let lock = binding.lock().map_err(project_error)?;
        let saves = crate::save_ops::SaveCoordinator::start(&binding, wake)?;
        let main = binding.main_docx.clone();
        self.session
            .run("file.open", &json!({"path": main.to_string_lossy()}))
            .map_err(|error| format!("无法打开主 DOCX {}：{error}", main.display()))?;

        let project_id = binding.root.to_string_lossy().into_owned();
        let project_title = binding
            .root
            .file_name()
            .map(|name| name.to_string_lossy().into_owned())
            .filter(|name| !name.is_empty())
            .unwrap_or_else(|| project_id.clone());
        let document_title = binding.main_docx.file_name().unwrap_or_default().to_string_lossy().into_owned();
        self.project = Some(binding);
        self.project_lock = Some(lock);
        self.project_saves = Some(saves);
        // The native opener owns the same workspace snapshot as the web host. Without this
        // hydration the save binding exists but the activity bar and both reversible panels
        // remain hidden behind their document-binding display condition.
        self.project_workspace.hydrate(json!({
            "projectId": project_id,
            "projectTitle": project_title,
            "documentTitle": document_title,
            "documentBound": true,
        }))?;
        self.last_project_resolve = 0.0;
        self.no_document = None;
        self.recovery_seen = true;
        self.autosave_path = None;
        self.autosave = false;
        self.session.autosave = false;
        self.ui.backstage = false;
        self.canvas.want_focus = true;
        Ok(json!({"project": self.project.as_ref().map(|p| p.root.to_string_lossy()), "path": main, "locked": true}))
    }

    /// Guided initialization for an empty project folder (no main DOCX yet): creates one blank
    /// main DOCX inside `root` without overwriting existing documents, then opens and locks the
    /// project through the standard `open_project` contract.
    pub fn init_blank_project(&mut self, root: PathBuf, file_name: &str, wake: Arc<dyn Fn() + Send + Sync>) -> Result<Value, String> {
        if self.save_unconfirmed() {
            return Err("上一次保存尚未确认，请先等待或查询原保存结果，再新建项目。".into());
        }
        match ProjectBinding::open(root.clone()) {
            Ok(_) => return Err("该目录已经存在主 DOCX，请直接打开项目。".into()),
            Err(ProjectError::SeveralMainDocuments(_)) => {
                return Err("该目录存在多份 DOCX，请只保留一份主文稿（其余可移出或在打开后作为项目资料导入）。".into());
            }
            Err(ProjectError::NoMainDocument) => {}
            Err(error) => return Err(project_error(error)),
        }
        let stem = file_name.trim().trim_end_matches(".docx").trim();
        let final_name = if stem.is_empty() { "主文稿.docx".to_owned() } else { format!("{stem}.docx") };
        let target = root.join(&final_name);
        if target.exists() {
            return Err(format!("目标文件已存在：{}", target.display()));
        }
        let document = wordcraft_engine::blank_document(&wordcraft_engine::Region::default());
        let bytes = wordcraft_engine::io::save_bytes(&final_name, &document)
            .map_err(|error| format!("无法生成空白主文稿 {final_name}：{error}"))?;
        std::fs::write(&target, bytes).map_err(|error| format!("无法写入空白主文稿 {}：{error}", target.display()))?;
        self.open_project(root, wake)
    }

    pub(crate) fn open_project_dialog(&mut self) -> Result<Value, String> {
        match self.ask_file(FileDialogRequest::PickFolder, AfterPick::OpenProject) {
            Asked::Done(result) => result,
            Asked::Pending => Ok(json!({"pending": "openProject"})),
            Asked::Busy => Ok(json!({"done": false})),
        }
    }

    pub(crate) fn new_project_dialog(&mut self) -> Result<Value, String> {
        match self.ask_file(FileDialogRequest::PickFolder, AfterPick::NewProject) {
            Asked::Done(result) => result,
            Asked::Pending => Ok(json!({"pending": "newProject"})),
            Asked::Busy => Ok(json!({"done": false})),
        }
    }

    pub(crate) fn project_bound(&self) -> bool {
        self.project.is_some()
    }

    /// Read-only handoff for the native AI host; the project binding itself remains private.
    pub fn main_document_path(&self) -> Option<PathBuf> {
        self.project.as_ref().map(|project| project.main_docx.clone())
    }

    pub(crate) fn clear_project(&mut self) {
        self.project_saves = None;
        self.project_lock = None;
        self.project = None;
        self.project_workspace = Default::default();
        self.autosave_path = None;
        self.autosave = false;
        self.session.autosave = false;
    }

    pub(crate) fn request_project_save(&mut self) -> Result<Value, String> {
        let Some(saves) = self.project_saves.as_mut() else {
            return Err("当前文档没有项目保存绑定。".into());
        };
        let save_id = saves.request(&self.session).map_err(|error| save_failure(&error))?;
        Ok(json!({"pendingSave": save_id.0, "saveIntent": "native-project"}))
    }

    /// Poll the worker and, at most once a second, resolve an existing Unknown by its original
    /// save id. Querying never serializes, resubmits or rotates the backup.
    pub(crate) fn poll_project_saves(&mut self) {
        if self.project_saves.is_none() {
            return;
        }
        let now = now_ms();
        let outcome = {
            let Some(saves) = self.project_saves.as_mut() else { return };
            match saves.poll(&mut self.session) {
                Some(outcome) => Some(outcome),
                None if now - self.last_project_resolve >= 1000.0 => {
                    self.last_project_resolve = now;
                    saves.resolve_unresolved(&mut self.session)
                }
                None => None,
            }
        };
        let Some(outcome) = outcome else {
            if self.project_saves.as_ref().is_some_and(crate::save_ops::SaveCoordinator::unconfirmed)
                && let Some(ctx) = &self.ctx
            {
                ctx.request_repaint_after(std::time::Duration::from_millis(100));
            }
            return;
        };
        let save_id = outcome.save_id().0;
        match &outcome {
            SaveOutcome::Committed { .. } => self.status("项目主 DOCX 已保存并读回核验。"),
            SaveOutcome::Failed { reason, .. } => {
                let message = save_failure(reason);
                log::error!("project save {save_id} failed: {message}");
                self.status(format!("保存失败：{message}"));
            }
            SaveOutcome::Unknown { cause, .. } => {
                log::warn!("project save {save_id} is unknown: {cause}");
                self.status(format!("保存结果尚未确认；将只查询原保存编号 {save_id}，不会重复写入。{cause}"));
                return;
            }
        }

        let Some(wait) = self.awaiting_save.take_if(|wait| wait.ticket == save_id) else { return };
        if wait.document != self.session.document_id() {
            log::info!("the document held back by project save {save_id} was replaced; dropped");
            return;
        }
        match outcome {
            SaveOutcome::Committed { .. } if !self.session.dirty && self.session.rev() == wait.rev && !self.save_unconfirmed() => {
                if let Err(error) = self.execute_user(&wait.then, wait.params) {
                    self.status(error);
                }
            }
            SaveOutcome::Committed { .. } => {
                self.dialog = Some(dialogs::Dialog::SaveChanges {
                    name: self.prompt_name(),
                    then: wait.then,
                    params: wait.params,
                    document: wait.document,
                    message: "保存期间又有新的编辑；请再次决定是否保存这些更改。".into(),
                });
            }
            SaveOutcome::Failed { reason, .. } => {
                self.save_failed(&save_failure(&reason), wait.then, wait.params, wait.document);
            }
            SaveOutcome::Unknown { .. } => {}
        }
    }
}

fn project_error(error: ProjectError) -> String {
    match error {
        ProjectError::SecondInstance => "该项目已由另一个实例打开；第二个实例已拒绝打开。".into(),
        ProjectError::NoMainDocument => "项目根目录没有可打开的主 DOCX；不会递归查找或自动选择。".into(),
        ProjectError::SeveralMainDocuments(paths) => {
            format!("项目根目录有多份候选主 DOCX，已拒绝打开：{}", paths.iter().map(|path| path.display().to_string()).collect::<Vec<_>>().join("、"))
        }
        ProjectError::EncryptedMainDocument(path) => format!("首版不支持加密主 DOCX：{}", path.display()),
        ProjectError::Io(message) => message,
    }
}

fn save_failure(reason: &SaveFailure) -> String {
    match reason {
        SaveFailure::Busy => "已有一项保存活动中或结果未确认；本次未分配新保存编号，也未写文件。".into(),
        SaveFailure::Stale => "原保存操作已过期。".into(),
        SaveFailure::ExternalChange { .. } => "主 DOCX 在打开后被外部修改或移除；已停止保存，未轮换备份。".into(),
        SaveFailure::Backup(message) => format!("写入滚动备份失败：{message}"),
        SaveFailure::Replace(message) => format!("替换主 DOCX 失败：{message}"),
        SaveFailure::Snapshot(message) => format!("生成保存快照失败：{message}"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{Services, WordApp};
    use egui::vec2;
    use egui_kittest::kittest::Queryable;
    use std::sync::atomic::{AtomicU64, Ordering};
    use wordcraft_engine::Session;

    struct Scratch(PathBuf);

    impl Scratch {
        fn new() -> Self {
            static NEXT: AtomicU64 = AtomicU64::new(0);
            let path =
                std::env::temp_dir().join(format!("lingdoc-native-sidebar-test-{}-{}", std::process::id(), NEXT.fetch_add(1, Ordering::Relaxed),));
            std::fs::create_dir(&path).unwrap();
            Self(path)
        }
    }

    impl Drop for Scratch {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }

    #[test]
    fn native_binding_exposes_reversible_resource_and_chat_panels() {
        let scratch = Scratch::new();
        let document = wordcraft_engine::blank_document(&wordcraft_engine::Region::default());
        let bytes = wordcraft_engine::io::save_bytes("main.docx", &document).unwrap();
        std::fs::write(scratch.0.join("main.docx"), bytes).unwrap();
        let mut app = WordApp::new(Session::new(document), Services::default());
        app.open_project(scratch.0.clone(), Arc::new(|| {})).unwrap();
        assert!(app.project_workspace.is_enabled());
        assert!(app.project_workspace.document_bound);
        assert_eq!(app.project_workspace.status()["documentTitle"], "main.docx");

        let mut harness = egui_kittest::Harness::builder().with_size(vec2(1440.0, 920.0)).build_ui_state(
            |ui, app: &mut WordApp| {
                let ctx = ui.ctx().clone();
                app.logic(&ctx);
                app.ui(ui);
            },
            app,
        );
        harness.run_steps(8);
        assert!(harness.state().project_workspace.status()["resourceRect"].is_object());
        harness.get_by_label("资料").click();
        harness.run_steps(8);
        assert!(!harness.state().project_workspace.resources_open);
        assert!(harness.state().project_workspace.auxiliary_active());
        let status = harness.state_mut().execute("ui.projectStatus", json!({})).unwrap();
        assert_eq!(status["workspace"]["resourcesOpen"], false);
        harness.get_by_label("资料").click();
        harness.run_steps(8);
        assert!(harness.state().project_workspace.resources_open);
        assert!(harness.state().project_workspace.status()["resourceRect"].is_object());

        harness.get_by_role_and_label(egui::accesskit::Role::Button, "资料问答").click();
        harness.run_steps(8);
        assert!(harness.state().project_workspace.chat_open);
        assert!(harness.state().project_workspace.status()["chatRect"].is_object());
        harness.get_by_role_and_label(egui::accesskit::Role::Button, "资料问答").click();
        harness.run_steps(8);
        assert!(!harness.state().project_workspace.chat_open);
        harness.get_by_role_and_label(egui::accesskit::Role::Button, "资料问答").click();
        harness.run_steps(8);
        assert!(harness.state().project_workspace.chat_open);
        assert!(harness.state().project_workspace.status()["chatRect"].is_object());

        // The Activity Bar itself activates the sidebar on pointer-down. That must not
        // disable AI drawing on the same click and leave the entry permanently inert.
        harness.get_by_role_and_label(egui::accesskit::Role::Button, "AI 绘图").click();
        harness.run_steps(4);
        assert_eq!(harness.state_mut().drawing.take_actions(), vec![json!({"kind":"openWorkbench"})]);

        harness.state_mut().clear_project();
        harness.run_steps(4);
        assert!(!harness.state().project_workspace.is_enabled());
        assert!(harness.query_by_label("资料问答").is_none());
    }

    #[test]
    fn init_blank_project_creates_main_docx_and_refuses_existing() {
        let scratch = Scratch::new();
        let document = wordcraft_engine::blank_document(&wordcraft_engine::Region::default());
        let mut app = WordApp::new(Session::new(document), Services::default());
        let opened = app
            .execute("file.newProject", json!({"path": scratch.0, "name": "研究计划"}))
            .unwrap();
        assert_eq!(opened["locked"], true);
        assert!(scratch.0.join("研究计划.docx").exists());
        assert_eq!(app.project_workspace.status()["documentTitle"], "研究计划.docx");

        app.clear_project();
        let err = app.init_blank_project(scratch.0.clone(), "另一份", Arc::new(|| {})).unwrap_err();
        assert!(err.contains("已经存在主 DOCX"));
        assert!(!scratch.0.join("另一份.docx").exists());
    }
}
