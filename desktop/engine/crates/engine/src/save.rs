//! Explicit project saves: immutable snapshots, external-change checks, one rolling backup,
//! atomic replacement, and read-back verification.

use std::path::Path;
use std::sync::Arc;

use sha2::{Digest, Sha256};

mod worker;
pub use worker::SaveWorker;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord)]
pub struct SaveId(pub u64);

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct DiskIdentity {
    pub sha256: [u8; 32],
    pub len: u64,
}

impl DiskIdentity {
    pub fn from_bytes(bytes: &[u8]) -> Self {
        Self { sha256: Sha256::digest(bytes).into(), len: bytes.len() as u64 }
    }
}

#[derive(Clone)]
pub struct SaveOperation {
    /// Assigned by [`SaveWorker::submit`]; the value supplied by the caller is only a placeholder.
    pub save_id: SaveId,
    pub document_id: u64,
    pub save_revision: u64,
    pub base_disk: DiskIdentity,
    pub bytes: Arc<[u8]>,
    pub digest: [u8; 32],
}

impl SaveOperation {
    pub fn new(save_id: SaveId, document_id: u64, save_revision: u64, base_disk: DiskIdentity, bytes: Arc<[u8]>) -> Self {
        let digest = Sha256::digest(&bytes).into();
        Self { save_id, document_id, save_revision, base_disk, bytes, digest }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SaveFailure {
    Busy,
    Stale,
    ExternalChange { expected: DiskIdentity, found: Option<DiskIdentity> },
    Backup(String),
    Replace(String),
    Snapshot(String),
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SaveOutcome {
    Committed { save_id: SaveId, disk: DiskIdentity },
    Failed { save_id: SaveId, reason: SaveFailure },
    Unknown { save_id: SaveId, cause: String },
}

impl SaveOutcome {
    pub fn save_id(&self) -> SaveId {
        match self {
            Self::Committed { save_id, .. } | Self::Failed { save_id, .. } | Self::Unknown { save_id, .. } => *save_id,
        }
    }
}

pub fn disk_identity(path: &Path) -> Result<DiskIdentity, String> {
    let bytes = std::fs::read(path).map_err(|e| format!("{}: {e}", path.display()))?;
    Ok(DiskIdentity::from_bytes(&bytes))
}

/// Commit in the contract's fixed order: verify the old bytes, write the rolling backup from
/// those exact bytes, atomically replace the main document, then read it back.
pub fn commit(op: &SaveOperation, main: &Path, backup: &Path) -> SaveOutcome {
    commit_with(op, main, backup, write_atomic, write_atomic, disk_identity)
}

fn write_atomic(path: &Path, bytes: &[u8]) -> Result<(), String> {
    crate::io::write_file(&path.to_string_lossy(), bytes)
}

fn expected_disk(op: &SaveOperation) -> DiskIdentity {
    DiskIdentity { sha256: op.digest, len: op.bytes.len() as u64 }
}

fn failed(id: SaveId, reason: SaveFailure) -> SaveOutcome {
    SaveOutcome::Failed { save_id: id, reason }
}

fn external_change(op: &SaveOperation, found: Option<DiskIdentity>) -> SaveOutcome {
    failed(op.save_id, SaveFailure::ExternalChange { expected: op.base_disk, found })
}

/// The two write closures and the final read are the narrow failure-injection seam used by the
/// unit tests. Production uses the same atomic writer for backup and main document.
fn commit_with(
    op: &SaveOperation,
    main: &Path,
    backup: &Path,
    write_backup: impl FnOnce(&Path, &[u8]) -> Result<(), String>,
    replace_main: impl FnOnce(&Path, &[u8]) -> Result<(), String>,
    read_back: impl FnOnce(&Path) -> Result<DiskIdentity, String>,
) -> SaveOutcome {
    let expected = expected_disk(op);
    if DiskIdentity::from_bytes(&op.bytes) != expected {
        return failed(op.save_id, SaveFailure::Snapshot("save snapshot digest does not match its bytes".into()));
    }

    let old = match std::fs::read(main) {
        Ok(bytes) => bytes,
        Err(_) => return external_change(op, None),
    };
    let found = DiskIdentity::from_bytes(&old);
    if found != op.base_disk {
        return external_change(op, Some(found));
    }

    if let Err(e) = write_backup(backup, &old) {
        return failed(op.save_id, SaveFailure::Backup(e));
    }
    if let Err(e) = replace_main(main, &op.bytes) {
        return failed(op.save_id, SaveFailure::Replace(e));
    }

    // The rename has been issued. Anything short of a matching read-back is Unknown, never a
    // claim that the old main file is still intact.
    match read_back(main) {
        Ok(disk) if disk == expected => SaveOutcome::Committed { save_id: op.save_id, disk },
        Ok(_) => SaveOutcome::Unknown {
            save_id: op.save_id,
            cause: "the main document did not match the saved snapshot during read-back verification".into(),
        },
        Err(e) => SaveOutcome::Unknown { save_id: op.save_id, cause: format!("the main document could not be read back after replacement: {e}") },
    }
}

/// Resolve an Unknown outcome by reading only. Matching the original base means this operation
/// definitely did not land and is stale; a third contents identity remains Unknown.
pub fn resolve_unknown(op: &SaveOperation, main: &Path) -> SaveOutcome {
    match disk_identity(main) {
        Ok(disk) if disk == expected_disk(op) => SaveOutcome::Committed { save_id: op.save_id, disk },
        Ok(disk) if disk == op.base_disk => failed(op.save_id, SaveFailure::Stale),
        Ok(_) => SaveOutcome::Unknown {
            save_id: op.save_id,
            cause: "the main document contains neither the saved snapshot nor its original disk contents".into(),
        },
        Err(e) => SaveOutcome::Unknown { save_id: op.save_id, cause: format!("the main document still cannot be read: {e}") },
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use std::path::PathBuf;
    use std::sync::atomic::{AtomicU64, Ordering};

    struct Scratch(PathBuf);

    impl Scratch {
        fn new() -> Self {
            static NEXT: AtomicU64 = AtomicU64::new(0);
            let path = std::env::temp_dir().join(format!("lingdoc-save-test-{}-{}", std::process::id(), NEXT.fetch_add(1, Ordering::Relaxed)));
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
        SaveOperation::new(SaveId(7), 3, 5, disk_identity(main).unwrap(), Arc::from(bytes))
    }

    #[test]
    fn commit_backs_up_verified_old_bytes_and_commits_the_snapshot() {
        let d = Scratch::new();
        fs::write(d.main(), b"old").unwrap();
        let op = operation(&d.main(), b"new");
        let outcome = commit(&op, &d.main(), &d.backup());
        assert_eq!(outcome, SaveOutcome::Committed { save_id: SaveId(7), disk: expected_disk(&op) });
        assert_eq!(fs::read(d.main()).unwrap(), b"new");
        assert_eq!(fs::read(d.backup()).unwrap(), b"old");
    }

    #[test]
    fn external_disk_change_blocks_backup_and_overwrite() {
        let d = Scratch::new();
        fs::write(d.main(), b"old").unwrap();
        let op = operation(&d.main(), b"new");
        fs::write(d.main(), b"external").unwrap();
        let external = disk_identity(&d.main()).unwrap();
        assert_eq!(commit(&op, &d.main(), &d.backup()), external_change(&op, Some(external)));
        assert_eq!(fs::read(d.main()).unwrap(), b"external");
        assert!(!d.backup().exists());
    }

    #[test]
    fn backup_failure_leaves_the_main_document_unchanged() {
        let d = Scratch::new();
        fs::write(d.main(), b"old").unwrap();
        fs::create_dir(d.backup()).unwrap();
        let op = operation(&d.main(), b"new");
        assert!(matches!(commit(&op, &d.main(), &d.backup()), SaveOutcome::Failed { reason: SaveFailure::Backup(_), .. }));
        assert_eq!(fs::read(d.main()).unwrap(), b"old");
    }

    #[test]
    fn replace_failure_keeps_main_and_the_old_byte_backup() {
        let d = Scratch::new();
        fs::write(d.main(), b"old").unwrap();
        let op = operation(&d.main(), b"new");
        let outcome = commit_with(&op, &d.main(), &d.backup(), write_atomic, |_, _| Err("injected replace failure".into()), disk_identity);
        assert!(matches!(outcome, SaveOutcome::Failed { reason: SaveFailure::Replace(_), .. }));
        assert_eq!(fs::read(d.main()).unwrap(), b"old");
        assert_eq!(fs::read(d.backup()).unwrap(), b"old");
        assert_eq!(fs::read_dir(&d.0).unwrap().count(), 2);
    }

    #[test]
    fn unknown_after_replace_is_resolved_without_writing_backup_again() {
        let d = Scratch::new();
        fs::write(d.main(), b"old").unwrap();
        let op = operation(&d.main(), b"new");
        let outcome = commit_with(&op, &d.main(), &d.backup(), write_atomic, write_atomic, |_| Err("injected read-back failure".into()));
        assert!(matches!(outcome, SaveOutcome::Unknown { .. }));
        assert_eq!(fs::read(d.main()).unwrap(), b"new");
        assert_eq!(fs::read(d.backup()).unwrap(), b"old");

        // A sentinel makes a second backup rotation observable without relying on timestamps.
        fs::write(d.backup(), b"query must not rotate this").unwrap();
        assert_eq!(resolve_unknown(&op, &d.main()), SaveOutcome::Committed { save_id: SaveId(7), disk: expected_disk(&op) });
        assert_eq!(fs::read(d.backup()).unwrap(), b"query must not rotate this");
    }

    #[test]
    fn unknown_resolves_to_stale_only_when_the_original_bytes_are_present() {
        let d = Scratch::new();
        fs::write(d.main(), b"old").unwrap();
        let op = operation(&d.main(), b"new");
        assert_eq!(resolve_unknown(&op, &d.main()), failed(SaveId(7), SaveFailure::Stale));
        fs::write(d.main(), b"third party").unwrap();
        assert!(matches!(resolve_unknown(&op, &d.main()), SaveOutcome::Unknown { .. }));
    }
}
