#!/usr/bin/env python3
"""Orchestrator of the 54-Spec dual runner (real browser x real backend).

Two modes, one file
-------------------
**Compare mode** (the documented contract)::

    python scripts/acceptance/beta_evidence_parity.py \\
        --http <http.json> --browser <browser.json> --out <parity.json>

compares two snapshots that were produced by two independent executors and
writes the three-way comparison: browser == HTTP == after-reload.

**Orchestrate mode** (no ``--http`` / ``--browser``)::

    python scripts/acceptance/beta_evidence_parity.py --out <parity.json> \\
        [--base-url <URL>] [--frontend-url <URL>] \\
        [--start-backend] [--backend-binary <PATH>] [--start-frontend]

starts the environment, runs the HTTP side, runs the browser side, compares
the two and writes the same ``parity.json``.  Implementation Decision 7 of
54-Spec: there is no single command that starts the front end and the
back end together (``make dev-start`` only starts the dependency services),
so the orchestrator owns the two processes and their lifecycle.

Ports are explicit (Implementation Decision 6)
---------------------------------------------
The backend's listening port does **not** follow ``APP_PORT``; it comes from
``config/config.yaml``'s ``server.port`` (which viper binds to the
``SERVER_PORT`` environment variable).  The orchestrator reads that config,
records the port it actually observed, and never guesses.  The front end is
the Vite dev server on 5173, proxying ``/api`` to
``VITE_DEV_PROXY_TARGET || FRONTEND_BACKEND_URL || 'http://localhost:8080'``
(frontend/vite.config.ts:50-53,131-136).

No silent skip (the 判定口径 v1 rule)
-------------------------------------
Anything that did not run is written into ``not_run[]`` with a ``reason`` and
a machine-readable ``unblock`` condition.  A run that could not reach the
real backend produces a parity report that says NOT RUN -- never a green
report with an empty comparison.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import secrets
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

REPO_ROOT = Path(__file__).resolve().parents[2]
HTTP_EXECUTOR = REPO_ROOT / "scripts" / "acceptance" / "beta_http_readback.py"
BROWSER_EXECUTOR = REPO_ROOT / "scripts" / "acceptance" / "beta_browser_cdp.mjs"

# The item set is declared by the executors; this is only the fallback for
# the case where the HTTP executor cannot even be started.
FALLBACK_ITEM_KEYS: tuple[str, ...] = (
    "browser_snapshot_digest_matches_http",
    "browser_export_id_matches_http",
    "browser_export_sha_matches_http",
    "project_version_matches",
    "snapshot_check_status",
    "snapshot_id_matches",
    "spec_revision_matches",
    "chapters_in_snapshot_matches",
    "sources_in_snapshot_matches",
    "current_check_status_matches",
    "browser_candidate_adoption_state_matches",
    "browser_candidate_cancelled_state_matches",
)

BACKEND_UNBLOCK = ("Start the real LingDoc backend (make dev-app, or an isolated server process) and "
                   "point --base-url at it, then re-run the orchestrator.")
FRONTEND_UNBLOCK = ("Start the real front end (npm run dev in frontend/) and point --frontend-url at it, "
                    "then re-run the orchestrator.")


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def write_json(path: Path, payload: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(temporary, path)


def read_json(path: Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def configured_backend_port(root: Path) -> int:
    """The port the backend actually listens on.

    ``APP_PORT`` does not control it (docs/08-本轮实施方案/T15-真实服务启动.md:101);
    the answer lives in ``config/config.yaml``'s ``server.port``, which viper
    binds to the ``SERVER_PORT`` environment variable
    (internal/config/config.go:503-504).  The configuration file wins over the
    environment only when the environment does not set it.
    """
    env_port = os.environ.get("SERVER_PORT", "").strip()
    if env_port.isdigit():
        return int(env_port)
    config = root / "config" / "config.yaml"
    if config.is_file():
        text = config.read_text(encoding="utf-8", errors="replace")
        match = re.search(r"^\s{2}port:\s*(\d+)\s*$", text, re.MULTILINE)
        if match:
            return int(match.group(1))
    return 8080


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def reachable(url: str, timeout: float = 3.0) -> bool:
    try:
        with urllib.request.urlopen(url.rstrip("/") + "/health", timeout=timeout) as response:
            return response.status == 200
    except urllib.error.HTTPError as error:
        return error.code == 200
    except (urllib.error.URLError, TimeoutError, OSError):
        return False


def git_head(root: Path) -> str:
    try:
        result = subprocess.run(["git", "-C", str(root), "rev-parse", "HEAD"],
                                capture_output=True, text=True, encoding="utf-8", check=False, timeout=30)
        if result.returncode == 0:
            return result.stdout.strip()
    except (OSError, subprocess.TimeoutExpired):
        pass
    return "unknown"


# --------------------------------------------------------------------------
# Process lifecycle
# --------------------------------------------------------------------------
def minimal_environment() -> dict[str, str]:
    names = ["PATH", "LANG", "LC_ALL", "TZ", "HOME", "TMPDIR", "TMP", "TEMP",
             "SystemRoot", "SYSTEMROOT", "WINDIR", "SystemDrive", "USERPROFILE", "APPDATA"]
    return {name: os.environ[name] for name in names if os.environ.get(name)}


def server_environment(data_dir: Path, port: int) -> dict[str, str]:
    """The environment of an isolated real backend process.

    Mirrors scripts/acceptance/governance_real_http.py: a real server binary
    with an isolated SQLite database, no Redis, no external dependency.  The
    point of this face is the browser/HTTP agreement, not the database.
    """
    env = minimal_environment()
    env.update({
        "GIN_MODE": "release",
        "SERVER_HOST": "127.0.0.1",
        "SERVER_PORT": str(port),
        "DB_DRIVER": "sqlite",
        "DB_PATH": str(data_dir / "lingdoc.sqlite"),
        "RETRIEVE_DRIVER": "sqlite",
        "STORAGE_TYPE": "local",
        "LOCAL_STORAGE_BASE_DIR": str(data_dir / "files"),
        "DUCKDB_SKIP_EXTENSION_LOAD": "1",
        "JWT_SECRET": secrets.token_urlsafe(48),
        "REDIS_ADDR": "",
        "DISABLE_REGISTRATION": "false",
        "WEKNORA_AUTH_COMPLEX_PASSWORD_ENABLED": "false",
        "WEKNORA_AUTH_DEFAULT_TENANT_MODE": "create_personal",
        "WEKNORA_TENANT_ENABLE_RBAC": "true",
    })
    return env


def wait_health(process: subprocess.Popen[bytes], base_url: str, timeout: float = 180.0) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            return False
        if reachable(base_url):
            return True
        time.sleep(0.5)
    return False


def stop_process(process: subprocess.Popen[bytes] | None) -> None:
    if process is None or process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=30)
    except subprocess.TimeoutExpired:
        process.kill()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            pass


def start_backend(root: Path, binary: Path, work_dir: Path, log_path: Path) -> tuple[Any, str]:
    port = free_port()
    env = server_environment(work_dir / "backend-data", port)
    log = log_path.open("wb")
    process = subprocess.Popen([str(binary)], cwd=root, env=env, stdout=log, stderr=subprocess.STDOUT)
    base_url = f"http://127.0.0.1:{port}"
    if not wait_health(process, base_url):
        stop_process(process)
        return None, base_url
    return process, base_url


def start_frontend(root: Path, backend_url: str, log_path: Path) -> tuple[Any, str]:
    frontend = root / "frontend"
    port = 5173
    env = os.environ.copy()
    env["VITE_DEV_PROXY_TARGET"] = backend_url
    env.setdefault("FRONTEND_BACKEND_URL", backend_url)
    vite = frontend / "node_modules" / "vite" / "bin" / "vite.js"
    log = log_path.open("wb")
    if vite.is_file():
        command = ["node", str(vite), "--port", str(port), "--strictPort"]
    else:
        npm = shutil.which("npm") or "npm"
        command = [npm, "run", "dev", "--", "--port", str(port), "--strictPort"]
    process = subprocess.Popen(command, cwd=frontend, env=env, stdout=log, stderr=subprocess.STDOUT)
    base_url = f"http://127.0.0.1:{port}"
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        if process.poll() is not None:
            return None, base_url
        try:
            with urllib.request.urlopen(base_url + "/", timeout=3) as response:
                if response.status < 500:
                    return process, base_url
        except urllib.error.HTTPError:
            return process, base_url
        except (urllib.error.URLError, TimeoutError, OSError):
            pass
        time.sleep(0.5)
    stop_process(process)
    return None, base_url


# --------------------------------------------------------------------------
# The comparison
# --------------------------------------------------------------------------
def not_run_entry(item: str, reason: str, unblock: str) -> dict[str, str]:
    return {"item": item, "reason": reason, "unblock": unblock}


def merge_not_run(*sources: Any) -> dict[str, dict[str, str]]:
    merged: dict[str, dict[str, str]] = {}
    for source in sources:
        if not isinstance(source, list):
            continue
        for entry in source:
            if not isinstance(entry, dict):
                continue
            item = str(entry.get("item", "")).strip()
            if not item:
                continue
            merged.setdefault(item, {
                "item": item,
                "reason": str(entry.get("reason", "")).strip(),
                "unblock": str(entry.get("unblock", "")).strip(),
            })
    return merged


def compare_snapshots(http_snapshot: dict[str, Any], browser_snapshot: dict[str, Any],
                      *, head: str, environment: dict[str, Any]) -> dict[str, Any]:
    """Three-way comparison: browser == HTTP == after-reload.

    The comparison is pairwise (Implementation Decision 5): three snapshots,
    so every key must agree across all three of them.
    """
    http_items = http_snapshot.get("items") if isinstance(http_snapshot.get("items"), dict) else {}
    browser_items = browser_snapshot.get("items") if isinstance(browser_snapshot.get("items"), dict) else {}
    after = browser_snapshot.get("after_reload")
    reload_items = after.get("items") if isinstance(after, dict) and isinstance(after.get("items"), dict) else {}

    declared = merge_not_run(http_snapshot.get("not_run"), browser_snapshot.get("not_run"))

    all_keys = set(http_items) | set(browser_items) | set(reload_items)
    comparable = sorted(key for key in all_keys
                        if key in http_items and key in browser_items and key in reload_items
                        and key not in declared)

    mismatches: list[dict[str, Any]] = []
    for key in comparable:
        http_value = http_items[key]
        browser_value = browser_items[key]
        reload_value = reload_items[key]
        if http_value != browser_value or http_value != reload_value:
            mismatches.append({
                "key": key,
                "http": http_value,
                "browser": browser_value,
                "after_reload": reload_value,
            })

    not_run: list[dict[str, str]] = []
    for key in sorted(declared):
        entry = declared[key]
        if not entry["reason"]:
            entry["reason"] = "This side did not read the item."
        if not entry["unblock"]:
            entry["unblock"] = "Re-run the side that did not read the item."
        not_run.append(entry)
    for key in sorted(all_keys - set(comparable)):
        if key in declared:
            continue
        not_run.append(not_run_entry(
            key,
            "The three snapshots do not all carry this item, so it cannot be compared.",
            "Re-run the HTTP side and the browser side against the same project, then compare again."))

    consistent = not mismatches
    if mismatches:
        status = "FAIL"
    elif comparable:
        status = "PASS"
    else:
        status = "NOT RUN"
    return {
        "consistent": consistent,
        "checked": len(comparable),
        "mismatches": mismatches,
        "not_run": not_run,
        "status": status,
        "head": head,
        "at": now_iso(),
        "environment": environment,
    }


def item_keys_from_executor() -> list[str]:
    """Ask the HTTP executor which items it declares (single source of truth)."""
    try:
        result = subprocess.run([sys.executable, str(HTTP_EXECUTOR), "--list-items"],
                                capture_output=True, text=True, encoding="utf-8",
                                check=False, timeout=60)
        if result.returncode == 0:
            payload = json.loads(result.stdout)
            items = payload.get("items")
            if isinstance(items, list) and items:
                return [str(item) for item in items]
    except (OSError, subprocess.TimeoutExpired, json.JSONDecodeError):
        pass
    return list(FALLBACK_ITEM_KEYS)


def all_not_run(keys: list[str], reason: str, unblock: str) -> list[dict[str, str]]:
    return [not_run_entry(key, reason, unblock) for key in keys]


# --------------------------------------------------------------------------
# Orchestrate mode
# --------------------------------------------------------------------------
def orchestrate(args: argparse.Namespace) -> tuple[dict[str, Any], int]:
    root = Path(args.repo_root).resolve()
    head = git_head(root)
    keys = item_keys_from_executor()
    out = Path(args.out).resolve()
    work_dir = Path(args.work_dir).resolve() if args.work_dir else \
        Path(tempfile.mkdtemp(prefix="lingdoc-beta452-"))
    work_dir.mkdir(parents=True, exist_ok=True)
    http_out = work_dir / "http-readback.json"
    browser_out = work_dir / "browser-readback.json"
    context_path = work_dir / "run-context.json"

    backend_process: Any = None
    frontend_process: Any = None
    environment: dict[str, Any] = {
        "backend_base_url": "",
        "backend_port": None,
        "backend_port_source": "",
        "frontend_url": "",
        "frontend_port": None,
        "started_by_orchestrator": {"backend": False, "frontend": False},
        "work_dir": str(work_dir),
    }

    try:
        backend_url = (args.base_url or "").rstrip("/")
        if args.start_backend:
            binary = Path(args.backend_binary).resolve() if args.backend_binary else None
            if binary is None or not binary.is_file():
                reason = "No backend binary was supplied for --start-backend."
                unblock = ("Build the server (make build) and pass --backend-binary <path>, "
                           "or start the backend yourself and pass --base-url <URL>.")
                parity = {"consistent": True, "checked": 0, "mismatches": [],
                          "not_run": all_not_run(keys, reason, unblock),
                          "status": "NOT RUN", "head": head, "at": now_iso(), "environment": environment}
                write_json(out, parity)
                return parity, 2
            backend_process, backend_url = start_backend(root, binary, work_dir, work_dir / "backend.log")
            environment["started_by_orchestrator"]["backend"] = backend_process is not None
            environment["backend_port_source"] = (
                "SERVER_PORT (the viper binding of config/config.yaml's server.port); "
                "the orchestrator chose a free port for the isolated process")
            if backend_process is None:
                reason = "The backend process did not become healthy; see the backend log in the work directory."
                parity = {"consistent": True, "checked": 0, "mismatches": [],
                          "not_run": all_not_run(keys, reason, BACKEND_UNBLOCK),
                          "status": "NOT RUN", "head": head, "at": now_iso(), "environment": environment}
                write_json(out, parity)
                return parity, 2
        if not backend_url:
            port = configured_backend_port(root)
            backend_url = f"http://127.0.0.1:{port}"
            environment["backend_port_source"] = "config/config.yaml server.port (APP_PORT does not control it)"
        environment["backend_base_url"] = backend_url
        environment["backend_port"] = _port_of(backend_url)
        if not reachable(backend_url):
            reason = f"The real backend at {backend_url} did not answer /health."
            parity = {"consistent": True, "checked": 0, "mismatches": [],
                      "not_run": all_not_run(keys, reason, BACKEND_UNBLOCK),
                      "status": "NOT RUN", "head": head, "at": now_iso(), "environment": environment}
            write_json(out, parity)
            return parity, 2

        frontend_url = (args.frontend_url or "").rstrip("/")
        if args.start_frontend:
            frontend_process, frontend_url = start_frontend(root, backend_url, work_dir / "frontend.log")
            environment["started_by_orchestrator"]["frontend"] = frontend_process is not None
            if frontend_process is None:
                reason = "The Vite dev server did not come up; see the frontend log in the work directory."
                parity = {"consistent": True, "checked": 0, "mismatches": [],
                          "not_run": all_not_run(keys, reason, FRONTEND_UNBLOCK),
                          "status": "NOT RUN", "head": head, "at": now_iso(), "environment": environment}
                write_json(out, parity)
                return parity, 2
        if not frontend_url:
            frontend_url = "http://127.0.0.1:5173"
        environment["frontend_url"] = frontend_url
        environment["frontend_port"] = _port_of(frontend_url)

        http_code = subprocess.run(
            [sys.executable, str(HTTP_EXECUTOR), "--base-url", backend_url,
             "--out", str(http_out), "--context-out", str(context_path)],
            check=False, timeout=900).returncode
        browser_code = subprocess.run(
            ["node", str(BROWSER_EXECUTOR), "--url", frontend_url,
             "--out", str(browser_out), "--context", str(context_path)],
            check=False, timeout=900).returncode

        http_snapshot = read_json(http_out) if http_out.is_file() else {
            "source": "http", "at": now_iso(), "items": {},
            "not_run": all_not_run(keys, "The HTTP executor produced no snapshot.", BACKEND_UNBLOCK)}
        browser_snapshot = read_json(browser_out) if browser_out.is_file() else {
            "source": "browser", "at": now_iso(), "items": {},
            "after_reload": {"at": now_iso(), "items": {}},
            "not_run": all_not_run(keys, "The browser executor produced no snapshot.", FRONTEND_UNBLOCK)}
        environment["http_exit_code"] = http_code
        environment["browser_exit_code"] = browser_code
        parity = compare_snapshots(http_snapshot, browser_snapshot, head=head, environment=environment)
        write_json(out, parity)
        return parity, 0 if parity["status"] == "PASS" else 2
    finally:
        stop_process(frontend_process)
        stop_process(backend_process)
        # The hand-off file carries the isolated account's password.  It is
        # only needed while the two sides run, so it does not outlive the
        # orchestration (Implementation Decision 7: the orchestrator owns the
        # lifecycle, cleanup included).
        try:
            if context_path.is_file():
                context_path.unlink()
        except OSError:
            pass


def _port_of(url: str) -> int | None:
    match = re.search(r":(\d+)(?:/|$)", url)
    return int(match.group(1)) if match else None


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--http", help="HTTP-side snapshot produced by beta_http_readback.py")
    parser.add_argument("--browser", help="Browser-side snapshot produced by beta_browser_cdp.mjs")
    parser.add_argument("--out", required=True, help="Where to write parity.json")
    parser.add_argument("--repo-root", default=str(REPO_ROOT), help="Repository root")
    parser.add_argument("--base-url", default="", help="Real backend base URL (orchestrate mode)")
    parser.add_argument("--frontend-url", default="", help="Real front end URL (orchestrate mode)")
    parser.add_argument("--backend-binary", default="", help="Server binary to start (orchestrate mode)")
    parser.add_argument("--start-backend", action="store_true", help="Start the backend (orchestrate mode)")
    parser.add_argument("--start-frontend", action="store_true", help="Start the Vite dev server (orchestrate mode)")
    parser.add_argument("--work-dir", default="", help="Where the intermediate snapshots live")
    args = parser.parse_args()

    out = Path(args.out).resolve()
    if args.http and args.browser:
        http_snapshot = read_json(Path(args.http).resolve())
        browser_snapshot = read_json(Path(args.browser).resolve())
        environment = {
            "backend_base_url": str(http_snapshot.get("base_url", "")),
            "frontend_url": str(browser_snapshot.get("url", "")),
            "mode": "compare",
        }
        parity = compare_snapshots(http_snapshot, browser_snapshot,
                                   head=git_head(Path(args.repo_root).resolve()), environment=environment)
        write_json(out, parity)
    elif args.http or args.browser:
        parser.error("--http and --browser must be given together")
    else:
        parity, code = orchestrate(args)
        if parity.get("status") == "PASS":
            print(f"PASS: browser == HTTP == after-reload over {parity.get('checked')} items")
        elif parity.get("status") == "FAIL":
            print(f"FAIL: {len(parity.get('mismatches', []))} consistency mismatches")
        else:
            print("NOT RUN: the dual runner could not compare anything; see not_run[]")
        return code
    if parity.get("status") == "PASS":
        print(f"PASS: browser == HTTP == after-reload over {parity.get('checked')} items")
    elif parity.get("status") == "FAIL":
        print(f"FAIL: {len(parity.get('mismatches', []))} consistency mismatches")
    else:
        print("NOT RUN: the dual runner could not compare anything; see not_run[]")
    return 0 if parity.get("status") == "PASS" else 2


if __name__ == "__main__":
    raise SystemExit(main())
