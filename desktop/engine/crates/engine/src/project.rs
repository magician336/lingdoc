//! A native LingDoc project is a directory containing exactly one eligible main DOCX.

use std::fs::{File, OpenOptions};
use std::path::{Path, PathBuf};

#[derive(Debug)]
pub struct ProjectBinding {
    pub root: PathBuf,
    pub main_docx: PathBuf,
}

impl ProjectBinding {
    /// Decide the unique main document by reading only the project root. This never creates the
    /// lock file; call [`lock`](Self::lock) after the caller is ready to bind the project.
    pub fn open(root: PathBuf) -> Result<Self, ProjectError> {
        let mut documents = Vec::new();
        for entry in std::fs::read_dir(&root).map_err(|e| io_error(&root, e))? {
            let entry = entry.map_err(|e| io_error(&root, e))?;
            if !entry.file_type().map_err(|e| io_error(&entry.path(), e))?.is_file() {
                continue;
            }
            let path = entry.path();
            if is_word_temporary(&path) || is_rolling_backup(&path) || !is_docx(&path) {
                continue;
            }
            documents.push(path);
        }
        documents.sort();
        let main_docx = match documents.len() {
            0 => return Err(ProjectError::NoMainDocument),
            1 => documents.remove(0),
            _ => return Err(ProjectError::SeveralMainDocuments(documents)),
        };

        // Reuse the DOCX reader's format-level detector without opening or decrypting the document.
        // Encrypted projects are rejected before Recovery or editing can silently lose coverage.
        let bytes = std::fs::read(&main_docx).map_err(|e| io_error(&main_docx, e))?;
        if wordcraft_docx::is_encrypted(&bytes) {
            return Err(ProjectError::EncryptedMainDocument(main_docx));
        }
        Ok(Self { root, main_docx })
    }

    /// The one rolling backup, beside the main DOCX and named `<main file name>.bak`.
    pub fn backup_path(&self) -> PathBuf {
        let mut name = self.main_docx.file_name().unwrap_or_default().to_os_string();
        name.push(".bak");
        self.main_docx.with_file_name(name)
    }

    pub fn lock(&self) -> Result<ProjectLock, ProjectError> {
        let path = self.root.join(".lingdoc.lock");
        let file = OpenOptions::new().read(true).write(true).create(true).open(&path).map_err(|e| io_error(&path, e))?;
        match file.try_lock() {
            Ok(()) => Ok(ProjectLock { _file: file }),
            Err(std::fs::TryLockError::WouldBlock) => Err(ProjectError::SecondInstance),
            Err(e) => Err(io_error(&path, e.into())),
        }
    }
}

#[derive(Debug)]
pub struct ProjectLock {
    _file: File,
}

#[derive(Debug, PartialEq, Eq)]
pub enum ProjectError {
    SecondInstance,
    NoMainDocument,
    SeveralMainDocuments(Vec<PathBuf>),
    EncryptedMainDocument(PathBuf),
    Io(String),
}

fn is_docx(path: &Path) -> bool {
    path.extension().and_then(|extension| extension.to_str()).is_some_and(|extension| extension.eq_ignore_ascii_case("docx"))
}

fn is_word_temporary(path: &Path) -> bool {
    path.file_name().is_some_and(|name| name.to_string_lossy().starts_with("~$"))
}

fn is_rolling_backup(path: &Path) -> bool {
    path.file_name().is_some_and(|name| name.to_string_lossy().to_ascii_lowercase().ends_with(".docx.bak"))
}

fn io_error(path: &Path, error: std::io::Error) -> ProjectError {
    ProjectError::Io(format!("{}: {error}", path.display()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use std::sync::atomic::{AtomicU64, Ordering};

    struct Scratch(PathBuf);

    impl Scratch {
        fn new() -> Self {
            static NEXT: AtomicU64 = AtomicU64::new(0);
            let path = std::env::temp_dir().join(format!("lingdoc-project-test-{}-{}", std::process::id(), NEXT.fetch_add(1, Ordering::Relaxed)));
            fs::create_dir(&path).unwrap();
            Self(path)
        }
    }

    impl Drop for Scratch {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    #[test]
    fn zero_main_documents_are_rejected() {
        let d = Scratch::new();
        fs::write(d.0.join("notes.txt"), b"not a main document").unwrap();
        assert_eq!(ProjectBinding::open(d.0.clone()).unwrap_err(), ProjectError::NoMainDocument);
        assert!(!d.0.join(".lingdoc.lock").exists());
    }

    #[test]
    fn one_root_docx_is_selected_without_recursing_and_backup_name_is_fixed() {
        let d = Scratch::new();
        let main = d.0.join("申请书.DOCX");
        fs::write(&main, b"main").unwrap();
        fs::create_dir(d.0.join("nested")).unwrap();
        fs::write(d.0.join("nested").join("other.docx"), b"nested").unwrap();
        let binding = ProjectBinding::open(d.0.clone()).unwrap();
        assert_eq!(binding.main_docx, main);
        assert_eq!(binding.backup_path(), d.0.join("申请书.DOCX.bak"));
    }

    #[test]
    fn several_main_documents_are_all_reported() {
        let d = Scratch::new();
        let first = d.0.join("a.docx");
        let second = d.0.join("B.DoCx");
        fs::write(&first, b"a").unwrap();
        fs::write(&second, b"b").unwrap();
        assert_eq!(ProjectBinding::open(d.0.clone()).unwrap_err(), ProjectError::SeveralMainDocuments(vec![second, first]));
    }

    #[test]
    fn word_temporary_files_and_rolling_backups_do_not_count() {
        let d = Scratch::new();
        let main = d.0.join("main.docx");
        fs::write(&main, b"main").unwrap();
        fs::write(d.0.join("~$main.docx"), b"word temporary").unwrap();
        fs::write(d.0.join("main.docx.bak"), b"old main").unwrap();
        assert_eq!(ProjectBinding::open(d.0.clone()).unwrap().main_docx, main);

        fs::remove_file(d.0.join("main.docx")).unwrap();
        assert_eq!(ProjectBinding::open(d.0.clone()).unwrap_err(), ProjectError::NoMainDocument);
    }

    #[test]
    fn encrypted_main_docx_is_rejected_without_a_password_attempt() {
        let d = Scratch::new();
        let main = d.0.join("encrypted.docx");
        let bytes = crate::io::save_bytes("encrypted.docx", &wordcraft_doc::Document::new()).unwrap();
        let encrypted = wordcraft_docx::encrypt(&bytes, "test password").unwrap();
        fs::write(&main, encrypted).unwrap();
        assert_eq!(ProjectBinding::open(d.0.clone()).unwrap_err(), ProjectError::EncryptedMainDocument(main));
    }

    #[test]
    fn a_live_lock_rejects_another_handle_and_drop_releases_it() {
        let d = Scratch::new();
        fs::write(d.0.join("main.docx"), b"main").unwrap();
        let binding = ProjectBinding::open(d.0.clone()).unwrap();
        let lock = binding.lock().unwrap();
        assert_eq!(binding.lock().unwrap_err(), ProjectError::SecondInstance);
        drop(lock);
        let reacquired = binding.lock().unwrap();
        assert!(d.0.join(".lingdoc.lock").exists());
        drop(reacquired);
    }
}
