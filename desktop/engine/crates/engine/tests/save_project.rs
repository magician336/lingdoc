#![cfg(not(target_arch = "wasm32"))]

use std::fs;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{Duration, Instant};
use wordcraft_engine::project::{ProjectBinding, ProjectError};

const CHILD_ROOT: &str = "LINGDOC_PROJECT_LOCK_TEST_ROOT";

struct Scratch(PathBuf);

impl Scratch {
    fn new() -> Self {
        static NEXT: AtomicU64 = AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!("lingdoc-project-process-test-{}-{}", std::process::id(), NEXT.fetch_add(1, Ordering::Relaxed)));
        fs::create_dir(&path).unwrap();
        fs::write(path.join("main.docx"), b"main").unwrap();
        Self(path)
    }

    fn binding(&self) -> ProjectBinding {
        ProjectBinding::open(self.0.clone()).unwrap()
    }
}

impl Drop for Scratch {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

/// Re-executed by the integration-test binary in an independent process. The environment gate
/// keeps this a no-op during the parent's ordinary test discovery.
#[test]
fn project_lock_child_process() {
    let Some(root) = std::env::var_os(CHILD_ROOT) else {
        return;
    };
    let root = PathBuf::from(root);
    let binding = ProjectBinding::open(root.clone()).unwrap();
    let deadline = Instant::now() + Duration::from_secs(10);
    let lock = loop {
        match binding.lock() {
            Ok(lock) => break lock,
            Err(ProjectError::SecondInstance) if Instant::now() < deadline => {
                std::thread::sleep(Duration::from_millis(10));
            }
            Err(e) => panic!("child could not acquire project lock: {e:?}"),
        }
    };
    let release_deadline = Instant::now() + Duration::from_secs(30);
    while !root.join("release-lock").exists() {
        assert!(Instant::now() < release_deadline, "parent did not release the child lock holder");
        std::thread::sleep(Duration::from_millis(10));
    }
    drop(lock);
}

fn spawn_lock_holder(root: &Path) -> Child {
    Command::new(std::env::current_exe().unwrap())
        .arg("project_lock_child_process")
        .arg("--exact")
        .arg("--nocapture")
        .env(CHILD_ROOT, root)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .unwrap()
}

fn wait_until_child_holds_lock(binding: &ProjectBinding, child: &mut Child) {
    let deadline = Instant::now() + Duration::from_secs(10);
    loop {
        match binding.lock() {
            Err(ProjectError::SecondInstance) => return,
            Ok(lock) => drop(lock),
            Err(e) => panic!("parent could not inspect project lock: {e:?}"),
        }
        if let Some(status) = child.try_wait().unwrap() {
            panic!("lock-holder child exited before acquiring the lock: {status}");
        }
        assert!(Instant::now() < deadline, "timed out waiting for child project lock");
        std::thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn second_instance_cannot_open_the_project_for_writing() {
    let d = Scratch::new();
    let binding = d.binding();
    let original = fs::read(&binding.main_docx).unwrap();
    let mut child = spawn_lock_holder(&d.0);
    wait_until_child_holds_lock(&binding, &mut child);
    assert!(matches!(binding.lock(), Err(ProjectError::SecondInstance)));
    assert_eq!(fs::read(&binding.main_docx).unwrap(), original);

    fs::write(d.0.join("release-lock"), b"release").unwrap();
    assert!(child.wait().unwrap().success());
    let lock = binding.lock().unwrap();
    drop(lock);
}

#[test]
fn killing_the_writer_releases_the_project_lock() {
    let d = Scratch::new();
    let binding = d.binding();
    let mut child = spawn_lock_holder(&d.0);
    wait_until_child_holds_lock(&binding, &mut child);
    child.kill().unwrap();
    child.wait().unwrap();

    let deadline = Instant::now() + Duration::from_secs(5);
    let lock = loop {
        match binding.lock() {
            Ok(lock) => break lock,
            Err(ProjectError::SecondInstance) if Instant::now() < deadline => {
                std::thread::sleep(Duration::from_millis(10));
            }
            Err(e) => panic!("lock was not released after child termination: {e:?}"),
        }
    };
    assert!(d.0.join(".lingdoc.lock").exists());
    drop(lock);
}
