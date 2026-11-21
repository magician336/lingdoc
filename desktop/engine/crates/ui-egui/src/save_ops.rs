//! Native project saves: snapshot on the UI thread, commit on the engine worker.
//!
//! The coordinator deliberately has one pending operation. In particular, an
//! `Unknown` result keeps that operation pending until the caller explicitly
//! resolves the same save id; it is never submitted a second time.

use std::path::PathBuf;
use std::sync::Arc;

use wordcraft_engine::Session;
use wordcraft_engine::project::ProjectBinding;
use wordcraft_engine::save::{DiskIdentity, SaveFailure, SaveId, SaveOperation, SaveOutcome, SaveWorker, disk_identity};

trait SaveBackend {
    fn submit(&mut self, op: SaveOperation) -> Result<SaveId, SaveFailure>;
    fn poll(&mut self) -> Option<SaveOutcome>;
    fn query(&mut self, id: SaveId) -> SaveOutcome;
}

impl SaveBackend for SaveWorker {
    fn submit(&mut self, op: SaveOperation) -> Result<SaveId, SaveFailure> {
        SaveWorker::submit(self, op)
    }

    fn poll(&mut self) -> Option<SaveOutcome> {
        SaveWorker::poll(self)
    }

    fn query(&mut self, id: SaveId) -> SaveOutcome {
        SaveWorker::query(self, id)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum PendingPhase {
    Active,
    Unknown,
}

#[derive(Debug)]
struct PendingSave {
    save_id: SaveId,
    document_id: u64,
    save_revision: u64,
    phase: PendingPhase,
}

/// Coordinates immutable UI snapshots with the single-in-flight engine worker.
pub struct SaveCoordinator {
    worker: Box<dyn SaveBackend>,
    main_docx: PathBuf,
    /// The bytes this project opened or most recently confirmed on disk. Never re-baseline an
    /// external replacement merely because the user has now clicked Save.
    base_disk: DiskIdentity,
    pending: Option<PendingSave>,
}

impl SaveCoordinator {
    pub fn start(binding: &ProjectBinding, wake: Arc<dyn Fn() + Send + Sync>) -> Result<Self, String> {
        let base_disk = disk_identity(&binding.main_docx).map_err(|error| format!("{}: {error}", binding.main_docx.display()))?;
        let worker = SaveWorker::start(binding.main_docx.clone(), binding.backup_path(), wake)?;
        Ok(Self::with_backend(binding.main_docx.clone(), Box::new(worker), base_disk))
    }

    fn with_backend(main_docx: PathBuf, worker: Box<dyn SaveBackend>, base_disk: DiskIdentity) -> Self {
        Self { worker, main_docx, base_disk, pending: None }
    }

    /// Capture and submit one immutable DOCX snapshot without changing `dirty`.
    ///
    /// `SaveId(0)` is only a construction placeholder: the worker replaces it
    /// with the next real id after accepting the operation, and that returned id
    /// is the one retained here and returned to the caller.
    pub fn request(&mut self, session: &Session) -> Result<SaveId, SaveFailure> {
        if self.pending.is_some() {
            return Err(SaveFailure::Busy);
        }

        let (document, document_id, save_revision) = session.save_snapshot();
        let bytes: Arc<[u8]> = wordcraft_engine::io::save_bytes(&self.main_docx.to_string_lossy(), &document).map_err(SaveFailure::Snapshot)?.into();
        let op = SaveOperation::new(SaveId(0), document_id, save_revision, self.base_disk, bytes);
        let save_id = self.worker.submit(op)?;
        self.pending = Some(PendingSave { save_id, document_id, save_revision, phase: PendingPhase::Active });
        Ok(save_id)
    }

    /// Apply at most one worker receipt. Only a matching save id and document id
    /// may advance the session's saved revision.
    pub fn poll(&mut self, session: &mut Session) -> Option<SaveOutcome> {
        let outcome = self.worker.poll()?;
        Some(self.apply_outcome(session, outcome))
    }

    /// Resolve an unknown result by querying its original save id. This is the
    /// only resolution path; no new operation or backup write is initiated here.
    pub fn resolve_unresolved(&mut self, session: &mut Session) -> Option<SaveOutcome> {
        let pending = self.pending.as_ref()?;
        if pending.phase != PendingPhase::Unknown {
            return None;
        }
        let outcome = self.worker.query(pending.save_id);
        Some(self.apply_outcome(session, outcome))
    }

    /// True for both an active save and an unresolved `Unknown` result.
    pub fn unconfirmed(&self) -> bool {
        self.pending.is_some()
    }

    fn apply_outcome(&mut self, session: &mut Session, outcome: SaveOutcome) -> SaveOutcome {
        let outcome_id = outcome_save_id(&outcome);
        let Some(pending) = self.pending.as_ref() else {
            return outcome;
        };
        if &pending.save_id != outcome_id {
            return outcome;
        }

        if matches!(outcome, SaveOutcome::Unknown { .. }) {
            if let Some(pending) = self.pending.as_mut() {
                pending.phase = PendingPhase::Unknown;
            }
            return outcome;
        }

        if let SaveOutcome::Committed { disk, .. } = &outcome {
            self.base_disk = *disk;
        }
        let pending = self.pending.take();
        if let (SaveOutcome::Committed { .. }, Some(pending)) = (&outcome, pending) {
            if pending.document_id == session.document_id() {
                session.confirm_saved_through(pending.save_revision);
            }
        }
        outcome
    }
}

fn outcome_save_id(outcome: &SaveOutcome) -> &SaveId {
    match outcome {
        SaveOutcome::Committed { save_id, .. } | SaveOutcome::Failed { save_id, .. } | SaveOutcome::Unknown { save_id, .. } => save_id,
    }
}

#[cfg(test)]
mod tests {
    use std::cell::RefCell;
    use std::collections::VecDeque;
    use std::fs;
    use std::rc::Rc;
    use std::sync::atomic::{AtomicU64, Ordering};

    use wordcraft_engine::{Session, blank_document};

    use super::*;

    #[derive(Default)]
    struct MockState {
        next_id: u64,
        submitted: Vec<SaveOperation>,
        receipts: VecDeque<SaveOutcome>,
        query_results: VecDeque<SaveOutcome>,
        queries: Vec<SaveId>,
    }

    struct MockWorker(Rc<RefCell<MockState>>);

    impl SaveBackend for MockWorker {
        fn submit(&mut self, mut op: SaveOperation) -> Result<SaveId, SaveFailure> {
            let mut state = self.0.borrow_mut();
            state.next_id += 1;
            let id = SaveId(state.next_id);
            op.save_id = id;
            state.submitted.push(op);
            Ok(id)
        }

        fn poll(&mut self) -> Option<SaveOutcome> {
            self.0.borrow_mut().receipts.pop_front()
        }

        fn query(&mut self, id: SaveId) -> SaveOutcome {
            let mut state = self.0.borrow_mut();
            state.queries.push(id);
            state.query_results.pop_front().unwrap_or_else(|| SaveOutcome::Unknown { save_id: id, cause: "still unknown".into() })
        }
    }

    struct TestDir(PathBuf);

    impl TestDir {
        fn new() -> Result<Self, String> {
            static NEXT: AtomicU64 = AtomicU64::new(0);
            let path = std::env::temp_dir().join(format!("wordcraft-save-ops-{}-{}", std::process::id(), NEXT.fetch_add(1, Ordering::Relaxed)));
            fs::create_dir(&path).map_err(|error| format!("{}: {error}", path.display()))?;
            Ok(Self(path))
        }
    }

    impl Drop for TestDir {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    fn fixture() -> Result<(TestDir, PathBuf, Rc<RefCell<MockState>>, SaveCoordinator, Session), String> {
        let dir = TestDir::new()?;
        let main = dir.0.join("main.docx");
        let bytes = wordcraft_engine::io::save_bytes("main.docx", &blank_document(&wordcraft_engine::Region::default()))?;
        fs::write(&main, bytes).map_err(|error| format!("{}: {error}", main.display()))?;
        let state = Rc::new(RefCell::new(MockState::default()));
        let coordinator = SaveCoordinator::with_backend(main.clone(), Box::new(MockWorker(state.clone())), disk_identity(&main)?);
        Ok((dir, main, state, coordinator, Session::new(blank_document(&wordcraft_engine::Region::default()))))
    }

    fn disk() -> DiskIdentity {
        DiskIdentity { sha256: [7; 32], len: 1 }
    }

    fn committed(save_id: SaveId) -> SaveOutcome {
        SaveOutcome::Committed { save_id, disk: disk() }
    }

    #[test]
    fn request_captures_immutable_docx_and_open_disk() -> Result<(), String> {
        let (dir, main, state, mut coordinator, mut session) = fixture()?;
        let opened_disk = disk_identity(&main)?;
        let mut externally_replaced = fs::read(&main).map_err(|error| format!("{}: {error}", main.display()))?;
        externally_replaced.extend_from_slice(b"external replacement before Save");
        fs::write(&main, externally_replaced).map_err(|error| format!("{}: {error}", main.display()))?;
        session.doc.core.title = "snapshot".into();
        session.touch();
        let save_revision = session.rev();
        let document_id = session.document_id();
        let save_id = coordinator.request(&session).map_err(|error| format!("{error:?}"))?;
        session.doc.core.title = "edited later".into();
        session.touch();

        let state = state.borrow();
        let op = state.submitted.first().ok_or("missing submitted operation")?;
        assert_eq!(op.save_id, save_id);
        assert_eq!(op.document_id, document_id);
        assert_eq!(op.save_revision, save_revision);
        assert_eq!(op.base_disk, opened_disk, "Save must not re-baseline an external replacement");
        assert_ne!(op.base_disk, disk_identity(&main)?);
        let snapshot = wordcraft_engine::io::open_bytes("main.docx", &op.bytes)?;
        assert_eq!(snapshot.core.title, "snapshot");
        let snapshot_path = dir.0.join("snapshot.docx");
        fs::write(&snapshot_path, &op.bytes).map_err(|error| format!("{}: {error}", snapshot_path.display()))?;
        let snapshot_disk = disk_identity(&snapshot_path)?;
        assert_eq!(op.digest, snapshot_disk.sha256);
        assert_eq!(op.bytes.len() as u64, snapshot_disk.len);
        Ok(())
    }

    #[test]
    fn active_save_rejects_a_second_request_before_any_new_io() -> Result<(), String> {
        let (_dir, main, state, mut coordinator, mut session) = fixture()?;
        session.touch();
        let first = coordinator.request(&session).map_err(|error| format!("{error:?}"))?;
        session.touch();
        fs::remove_file(&main).map_err(|error| format!("{}: {error}", main.display()))?;
        assert!(matches!(coordinator.request(&session), Err(SaveFailure::Busy)));
        assert_eq!(state.borrow().submitted.len(), 1);
        assert_eq!(state.borrow().submitted.first().map(|op| op.save_id), Some(first));
        assert!(coordinator.unconfirmed());
        Ok(())
    }

    #[test]
    fn committed_revision_stays_dirty_after_later_edits() -> Result<(), String> {
        let (_dir, _main, state, mut coordinator, mut session) = fixture()?;
        for _ in 1..5 {
            session.touch();
        }
        assert_eq!(session.rev(), 5);
        let first = coordinator.request(&session).map_err(|error| format!("{error:?}"))?;
        for _ in 5..8 {
            session.touch();
        }
        state.borrow_mut().receipts.push_back(committed(first));
        assert!(matches!(coordinator.poll(&mut session), Some(SaveOutcome::Committed { .. })));
        assert_eq!(session.saved_rev(), 5);
        assert_eq!(session.rev(), 8);
        assert!(session.dirty);
        assert!(!coordinator.unconfirmed());

        let second = coordinator.request(&session).map_err(|error| format!("{error:?}"))?;
        state.borrow_mut().receipts.push_back(committed(second));
        let _ = coordinator.poll(&mut session);
        assert_eq!(session.saved_rev(), 8);
        assert!(!session.dirty);
        Ok(())
    }

    #[test]
    fn late_commit_for_replaced_document_does_not_confirm() -> Result<(), String> {
        let (_dir, _main, state, mut coordinator, mut session) = fixture()?;
        session.touch();
        let save_id = coordinator.request(&session).map_err(|error| format!("{error:?}"))?;
        session.set_document(blank_document(&wordcraft_engine::Region::default()));
        let saved_rev = session.saved_rev();
        let rev = session.rev();
        let dirty = session.dirty;
        state.borrow_mut().receipts.push_back(committed(save_id));
        assert!(matches!(coordinator.poll(&mut session), Some(SaveOutcome::Committed { .. })));
        assert_eq!(session.saved_rev(), saved_rev);
        assert_eq!(session.rev(), rev);
        assert_eq!(session.dirty, dirty);
        assert!(!coordinator.unconfirmed());
        Ok(())
    }

    #[test]
    fn receipt_with_another_save_id_cannot_finish_pending_save() -> Result<(), String> {
        let (_dir, _main, state, mut coordinator, mut session) = fixture()?;
        session.touch();
        let save_id = coordinator.request(&session).map_err(|error| format!("{error:?}"))?;
        let saved_rev = session.saved_rev();
        state.borrow_mut().receipts.push_back(committed(SaveId(save_id.0 + 100)));
        let _ = coordinator.poll(&mut session);
        assert_eq!(session.saved_rev(), saved_rev);
        assert!(coordinator.unconfirmed());
        state.borrow_mut().receipts.push_back(committed(save_id));
        let _ = coordinator.poll(&mut session);
        assert_eq!(session.saved_rev(), 2);
        assert!(!coordinator.unconfirmed());
        Ok(())
    }

    #[test]
    fn unknown_is_only_resolved_through_the_original_save_id() -> Result<(), String> {
        let (_dir, _main, state, mut coordinator, mut session) = fixture()?;
        session.touch();
        let save_id = coordinator.request(&session).map_err(|error| format!("{error:?}"))?;
        session.touch();
        state.borrow_mut().receipts.push_back(SaveOutcome::Unknown { save_id, cause: "readback unavailable".into() });
        assert!(matches!(coordinator.poll(&mut session), Some(SaveOutcome::Unknown { .. })));
        assert!(coordinator.unconfirmed());
        assert!(state.borrow().queries.is_empty());
        assert!(matches!(coordinator.request(&session), Err(SaveFailure::Busy)));

        assert!(matches!(coordinator.resolve_unresolved(&mut session), Some(SaveOutcome::Unknown { .. })));
        assert!(coordinator.unconfirmed());
        state.borrow_mut().query_results.push_back(committed(save_id));
        assert!(matches!(coordinator.resolve_unresolved(&mut session), Some(SaveOutcome::Committed { .. })));
        assert!(!coordinator.unconfirmed());
        assert_eq!(session.saved_rev(), 2);
        assert_eq!(session.rev(), 3);
        assert!(session.dirty);
        assert_eq!(state.borrow().queries, vec![save_id, save_id]);
        assert_eq!(state.borrow().submitted.len(), 1);
        Ok(())
    }

    #[test]
    fn resolve_does_nothing_while_save_is_active() -> Result<(), String> {
        let (_dir, _main, state, mut coordinator, mut session) = fixture()?;
        session.touch();
        let _ = coordinator.request(&session).map_err(|error| format!("{error:?}"))?;
        assert!(coordinator.resolve_unresolved(&mut session).is_none());
        assert!(state.borrow().queries.is_empty());
        assert!(coordinator.unconfirmed());
        Ok(())
    }
}
