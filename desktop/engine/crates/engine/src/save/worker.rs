use std::collections::BTreeMap;
use std::path::PathBuf;
use std::sync::{Arc, mpsc};

use super::{SaveFailure, SaveId, SaveOperation, SaveOutcome, commit, resolve_unknown};

/// One worker thread and at most one unacknowledged operation. An outcome that has arrived but
/// has not been returned by [`poll`](Self::poll) still blocks submission, so a caller cannot
/// capture a stale disk base before observing the preceding commit.
pub struct SaveWorker {
    tx: mpsc::SyncSender<SaveOperation>,
    rx: mpsc::Receiver<SaveOutcome>,
    main: PathBuf,
    outcomes: BTreeMap<SaveId, SaveOutcome>,
    operations: BTreeMap<SaveId, SaveOperation>,
    latest: SaveId,
    active: Option<SaveId>,
    alive: bool,
}

impl SaveWorker {
    pub fn start(main: PathBuf, backup: PathBuf, wake: Arc<dyn Fn() + Send + Sync>) -> Result<Self, String> {
        let (tx, jobs) = mpsc::sync_channel(1);
        let (events, rx) = mpsc::channel();
        let worker_main = main.clone();
        std::thread::Builder::new()
            .name("lingdoc-save".into())
            .spawn(move || {
                while let Ok(op) = jobs.recv() {
                    let outcome = commit(&op, &worker_main, &backup);
                    if events.send(outcome).is_err() {
                        break;
                    }
                    wake();
                }
            })
            .map_err(|e| e.to_string())?;
        Ok(Self { tx, rx, main, outcomes: BTreeMap::new(), operations: BTreeMap::new(), latest: SaveId(0), active: None, alive: true })
    }

    /// Accept one operation and allocate its id. Busy returns before changing the counter,
    /// sending work, writing either file, or rotating the backup.
    pub fn submit(&mut self, mut op: SaveOperation) -> Result<SaveId, SaveFailure> {
        if self.active.is_some() || self.has_unknown() {
            return Err(SaveFailure::Busy);
        }
        if !self.alive {
            return Err(SaveFailure::Snapshot("save worker is unavailable".into()));
        }
        let id = SaveId(self.latest.0.checked_add(1).ok_or_else(|| SaveFailure::Snapshot("save id space exhausted".into()))?);
        op.save_id = id;
        self.tx.try_send(op.clone()).map_err(|e| match e {
            mpsc::TrySendError::Full(_) => SaveFailure::Busy,
            mpsc::TrySendError::Disconnected(_) => SaveFailure::Snapshot("save worker is unavailable".into()),
        })?;
        self.latest = id;
        self.active = Some(id);
        self.operations.insert(id, op);
        Ok(id)
    }

    pub fn poll(&mut self) -> Option<SaveOutcome> {
        match self.rx.try_recv() {
            Ok(outcome) => Some(self.record(outcome)),
            Err(mpsc::TryRecvError::Empty) => None,
            Err(mpsc::TryRecvError::Disconnected) => {
                self.alive = false;
                let id = self.active.take()?;
                Some(self.record(SaveOutcome::Unknown {
                    save_id: id,
                    cause: "save worker stopped before the operation outcome was known; query the original save id".into(),
                }))
            }
        }
    }

    fn record(&mut self, outcome: SaveOutcome) -> SaveOutcome {
        let id = outcome.save_id();
        if self.active == Some(id) {
            self.active = None;
        }
        if !matches!(outcome, SaveOutcome::Unknown { .. }) {
            self.operations.remove(&id);
        }
        self.outcomes.insert(id, outcome.clone());
        outcome
    }

    /// Re-read an Unknown operation without writing. Stable terminal outcomes are returned from
    /// the receipt table, so repeated queries do not touch the filesystem again.
    pub fn query(&mut self, id: SaveId) -> SaveOutcome {
        if let Some(outcome) = self.outcomes.get(&id).cloned() {
            if !matches!(outcome, SaveOutcome::Unknown { .. }) {
                return outcome;
            }
            let Some(op) = self.operations.get(&id) else {
                return outcome;
            };
            let resolved = resolve_unknown(op, &self.main);
            if matches!(resolved, SaveOutcome::Unknown { .. }) {
                return resolved;
            }
            return self.record(resolved);
        }
        if self.active == Some(id) {
            return SaveOutcome::Unknown { save_id: id, cause: "save operation is still active".into() };
        }
        SaveOutcome::Failed { save_id: id, reason: SaveFailure::Stale }
    }

    pub fn outcome(&self, id: SaveId) -> Option<&SaveOutcome> {
        self.outcomes.get(&id)
    }

    /// True only when a new snapshot may be submitted: no active operation and no unresolved
    /// Unknown receipt remains.
    pub fn idle(&self) -> bool {
        self.alive && self.active.is_none() && !self.has_unknown()
    }

    fn has_unknown(&self) -> bool {
        self.outcomes.values().any(|outcome| matches!(outcome, SaveOutcome::Unknown { .. }))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::save::{DiskIdentity, disk_identity};
    use std::fs;
    use std::path::Path;
    use std::sync::atomic::{AtomicU64, Ordering};
    use std::time::{Duration, Instant};

    struct Scratch(PathBuf);

    impl Scratch {
        fn new() -> Self {
            static NEXT: AtomicU64 = AtomicU64::new(0);
            let path = std::env::temp_dir().join(format!("lingdoc-save-worker-test-{}-{}", std::process::id(), NEXT.fetch_add(1, Ordering::Relaxed)));
            fs::create_dir(&path).unwrap();
            Self(path)
        }

        fn main(&self) -> PathBuf {
            self.0.join("main.docx")
        }

        fn backup(&self) -> PathBuf {
            self.0.join("main.docx.bak")
        }
    }

    impl Drop for Scratch {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    fn operation(main: &Path, bytes: &'static [u8]) -> SaveOperation {
        SaveOperation::new(SaveId(999), 1, 1, disk_identity(main).unwrap(), Arc::from(bytes))
    }

    fn wait_for_outcome(worker: &mut SaveWorker) -> SaveOutcome {
        let deadline = Instant::now() + Duration::from_secs(5);
        loop {
            if let Some(outcome) = worker.poll() {
                return outcome;
            }
            assert!(Instant::now() < deadline, "timed out waiting for save outcome");
            std::thread::sleep(Duration::from_millis(5));
        }
    }

    #[test]
    fn second_submit_is_busy_and_does_not_consume_an_id_or_write_its_bytes() {
        let d = Scratch::new();
        fs::write(d.main(), b"old").unwrap();
        let mut worker = SaveWorker::start(d.main(), d.backup(), Arc::new(|| {})).unwrap();
        let first = operation(&d.main(), b"first");
        let second = operation(&d.main(), b"second");
        assert_eq!(worker.submit(first), Ok(SaveId(1)));
        assert_eq!(worker.submit(second), Err(SaveFailure::Busy));
        assert!(matches!(wait_for_outcome(&mut worker), SaveOutcome::Committed { save_id: SaveId(1), .. }));
        assert_eq!(fs::read(d.main()).unwrap(), b"first");
        assert_eq!(fs::read(d.backup()).unwrap(), b"old");

        let second = operation(&d.main(), b"second");
        assert_eq!(worker.submit(second), Ok(SaveId(2)));
        assert!(matches!(wait_for_outcome(&mut worker), SaveOutcome::Committed { save_id: SaveId(2), .. }));
        assert_eq!(fs::read(d.main()).unwrap(), b"second");
        assert_eq!(fs::read(d.backup()).unwrap(), b"first");
    }

    #[test]
    fn unresolved_unknown_is_busy_and_stable_query_never_reads_or_writes_again() {
        let d = Scratch::new();
        fs::write(d.main(), b"new").unwrap();
        let mut worker = SaveWorker::start(d.main(), d.backup(), Arc::new(|| {})).unwrap();
        let mut unknown = SaveOperation::new(SaveId(1), 1, 1, DiskIdentity::from_bytes(b"old"), Arc::from(&b"new"[..]));
        unknown.save_id = SaveId(1);
        worker.latest = SaveId(1);
        worker.operations.insert(SaveId(1), unknown);
        worker.outcomes.insert(SaveId(1), SaveOutcome::Unknown { save_id: SaveId(1), cause: "injected unresolved outcome".into() });
        fs::write(d.backup(), b"query sentinel").unwrap();

        let rejected = operation(&d.main(), b"other");
        assert_eq!(worker.submit(rejected), Err(SaveFailure::Busy));
        assert!(!worker.idle());
        let committed = worker.query(SaveId(1));
        assert!(matches!(committed, SaveOutcome::Committed { save_id: SaveId(1), .. }));
        assert!(worker.idle());
        assert_eq!(fs::read(d.backup()).unwrap(), b"query sentinel");

        fs::remove_file(d.main()).unwrap();
        assert_eq!(worker.query(SaveId(1)), committed);
        assert_eq!(worker.outcome(SaveId(1)), Some(&committed));
    }
}
