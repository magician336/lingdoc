#!/usr/bin/env python3
"""Run the Issue #196 G4 HTTP/application path against a real LingDoc process.

The runner deliberately keeps provider, browser, and database conclusions separate
from the HTTP path.  It can attach to an already running service with ``--base-url``
or start an isolated SQLite process with ``--binary``.  Every report is redacted and
bound to the checkout HEAD; credentials and response bodies never enter the report.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from typing import Any


def git(root: Path, *args: str) -> str:
    commands = [["git", "-C", str(root), *args]]
    # A WSL-mounted Windows worktree stores a Windows absolute path in its
    # .git file.  Linux git cannot resolve that path, while Git for Windows
    # can.  Use the Windows executable only as an identity lookup fallback;
    # no credentials or repository contents are sent elsewhere.
    if os.name == "posix":
        try:
            converted = subprocess.run(["wslpath", "-w", str(root)], capture_output=True,
                                       text=True, encoding="utf-8", check=False, timeout=10)
            if converted.returncode == 0 and converted.stdout.strip():
                commands.append(["git.exe", "-C", converted.stdout.strip(), *args])
        except (OSError, subprocess.TimeoutExpired):
            pass
    for command in commands:
        try:
            result = subprocess.run(command, capture_output=True, text=True,
                                    encoding="utf-8", check=False, timeout=30)
        except (OSError, subprocess.TimeoutExpired):
            continue
        if result.returncode == 0:
            return result.stdout.strip()
    raise RuntimeError("Git identity lookup failed")


def source_identity(root: Path) -> dict[str, Any]:
    return {
        "root": str(root),
        "head": git(root, "rev-parse", "HEAD"),
        "tree": git(root, "rev-parse", "HEAD^{tree}"),
        "dirty": bool(git(root, "status", "--porcelain=v1")),
    }


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def minimal_environment() -> dict[str, str]:
    names = ["PATH", "LANG", "LC_ALL", "TZ", "HOME", "TMPDIR", "TMP", "TEMP"]
    if os.name == "nt":
        names += ["SYSTEMROOT", "WINDIR", "SystemDrive", "USERPROFILE"]
    return {name: os.environ[name] for name in names if os.environ.get(name)}


def server_environment(data_dir: Path, port: int) -> dict[str, str]:
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


def wait_health(process: subprocess.Popen[bytes], base_url: str, timeout: int = 180) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"server exited during startup ({process.returncode})")
        try:
            with urllib.request.urlopen(base_url + "/health", timeout=3) as response:
                if response.status == 200:
                    return
        except (urllib.error.URLError, TimeoutError, OSError):
            time.sleep(1)
    raise RuntimeError("server did not become healthy within the startup timeout")


def stop_process(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is None:
        process.terminate()
    try:
        code = process.wait(timeout=45)
    except subprocess.TimeoutExpired:
        process.kill()
        code = process.wait(timeout=10)
    if code != 0:
        raise RuntimeError(f"server shutdown returned exit {code}")


def request(base_url: str, method: str, path: str, *, token: str = "",
            payload: Any = None, key: str = "", expected: int | tuple[int, ...] = 200) -> tuple[int, dict[str, Any], float]:
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8") if payload is not None else None
    headers = {"Accept": "application/json", "X-Request-ID": "issue196-g4"}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    if key:
        headers["Idempotency-Key"] = key
    started = time.monotonic()
    req = urllib.request.Request(base_url + path, data=body, headers=headers, method=method)
    status = 0
    raw = b""
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            status, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read()
    except (urllib.error.URLError, TimeoutError, OSError) as error:
        raise RuntimeError(f"{method} {path} could not reach the service ({type(error).__name__})") from None
    elapsed = round((time.monotonic() - started) * 1000, 2)
    try:
        data = json.loads(raw.decode("utf-8")) if raw else {}
    except (UnicodeDecodeError, json.JSONDecodeError):
        data = {"_non_json": True}
    accepted = expected if isinstance(expected, tuple) else (expected,)
    if status not in accepted:
        error = data.get("error", {}) if isinstance(data, dict) else {}
        detail = error.get("message", "") if isinstance(error, dict) else ""
        raise RuntimeError(f"{method} {path} returned {status} (code={error.get('code', 'unknown')}; message={detail})")
    if not isinstance(data, dict):
        raise RuntimeError(f"{method} {path} returned a non-object JSON response")
    return status, data, elapsed


def data(envelope: dict[str, Any]) -> Any:
    return envelope.get("data")


def identify(value: Any) -> dict[str, Any]:
    if not isinstance(value, dict):
        return {}
    allowed = ("id", "status", "state", "project_version", "template_copy_version", "version",
               "ruleset_hash", "ruleset_ref", "snapshot_digest", "download_path", "current_version_id")
    result: dict[str, Any] = {}
    for key in allowed:
        if key not in value:
            continue
        item = value[key]
        if key == "download_path" and isinstance(item, str):
            result[key] = "present"
        elif key == "ruleset_ref" and isinstance(item, dict):
            result[key] = {name: item.get(name) for name in ("ruleset_id", "version", "hash") if name in item}
        elif isinstance(item, (str, int, float, bool)) or item is None:
            result[key] = item
    return result


def validation_issue_summary(value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, dict) or not isinstance(value.get("issues"), list):
        return []
    summary = []
    for issue in value["issues"]:
        if not isinstance(issue, dict):
            continue
        row = {name: issue.get(name) for name in ("id", "rule_id", "severity", "status", "target_id", "target_version")
               if name in issue}
        ref = issue.get("ruleset_ref")
        if isinstance(ref, dict):
            row["ruleset_ref"] = {name: ref.get(name) for name in ("ruleset_id", "version", "hash") if name in ref}
        summary.append(row)
    return summary


def register_login(base_url: str) -> str:
    suffix = uuid.uuid4().hex[:12]
    email = f"issue196-{suffix}@example.com"
    password = "Synthetic-" + secrets.token_hex(8)
    status, registration, _ = request(base_url, "POST", "/api/v1/auth/register", payload={
        "username": "issue196-" + suffix, "email": email, "password": password,
    }, expected=201)
    if status != 201 or registration.get("success") is not True:
        raise RuntimeError("isolated registration did not succeed")
    _, login, _ = request(base_url, "POST", "/api/v1/auth/login", payload={
        "email": email, "password": password,
    })
    token = login.get("token")
    if login.get("success") is not True or not isinstance(token, str) or not token:
        raise RuntimeError("isolated login did not return a token")
    return token


def setup_project(base_url: str, token: str, *, canonical: bool) -> tuple[str, str, dict[str, Any]]:
    suffix = uuid.uuid4().hex[:8]
    _, created, _ = request(base_url, "POST", "/api/v1/lingdoc/projects", token=token, key="issue196-project-" + suffix,
                            payload={"name": "Issue 196 G4 " + suffix, "template_id": "template-demo"}, expected=201)
    project = data(created)
    if not isinstance(project, dict) or not project.get("id"):
        raise RuntimeError("project creation did not return a project ID")
    project_id = str(project["id"])
    ruleset_id = "issue196-ruleset-" + suffix
    if canonical:
        draft = {
            "ruleset_id": ruleset_id, "version": "v1",
            "bindings": [{"rule_id": "method-length", "definition_id": "computed/word_count",
                          "definition_version": "1", "origin": "organization", "target_kind": "chapter",
                          "target_id": "method", "parameters": {"minimum": 5, "maximum": 500},
                          "severity": "warning", "trigger_scenes": ["preflight"], "enabled": True,
                          "owner": "research-office"}],
        }
        _, _, _ = request(base_url, "POST", "/api/v1/lingdoc/rule-sets", token=token, payload=draft, expected=201)
        _, published, _ = request(base_url, "POST", f"/api/v1/lingdoc/rule-sets/{ruleset_id}/versions/v1/publish",
                                  token=token, payload={}, expected=200)
        published_data = data(published)
        if not isinstance(published_data, dict) or not published_data.get("ruleset_hash"):
            raise RuntimeError("published RuleSet did not return a server-computed hash")
        _, bound, _ = request(base_url, "PUT", f"/api/v1/lingdoc/projects/{project_id}/template-copy/rule-set",
                              token=token, key="issue196-bind-" + suffix,
                              payload={"expected_project_version": 1, "expected_template_copy_version": 1,
                                       "ruleset_id": ruleset_id, "ruleset_version": "v1", "overrides": []}, expected=200)
        project = data(bound)
        if not isinstance(project, dict) or not isinstance(project.get("template_copy"), dict):
            raise RuntimeError("RuleSet binding did not return a template copy")
    _, after_spec, _ = request(base_url, "PUT", f"/api/v1/lingdoc/projects/{project_id}/spec", token=token,
                               key="issue196-spec-" + suffix,
                               payload={"expected_spec_revision": 0, "fields": {"research_subject": "Issue 196 subject",
                                                                                  "research_goal": "Issue 196 goal"}}, expected=200)
    spec_project = data(after_spec)
    version = int(spec_project.get("project_version", 0)) if isinstance(spec_project, dict) else 0
    _, receipt, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/activation-read", token=token,
                            payload={"expected_project_version": version}, expected=201)
    receipt_data = data(receipt)
    receipt_id = receipt_data.get("id") if isinstance(receipt_data, dict) else None
    if not receipt_id:
        raise RuntimeError("activation read did not return a receipt")
    _, active, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/activate", token=token,
                           key="issue196-activate-" + suffix,
                           payload={"expected_spec_revision": 1, "expected_project_version": version,
                                    "reviewed_project_version": version, "activation_read_id": receipt_id}, expected=200)
    _, chapters_response, _ = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}/chapters", token=token)
    chapters = data(chapters_response)
    if not isinstance(chapters, list) or len(chapters) < 2:
        raise RuntimeError("activated project did not expose its chapters")
    chapter_ids: dict[str, str] = {}
    for chapter in chapters:
        section = chapter.get("section_id")
        chapter_id = chapter.get("id")
        if not isinstance(section, str) or not isinstance(chapter_id, str):
            raise RuntimeError("chapter response omitted stable IDs")
        body = "Issue 196 research question with enough context." if section == "question" else "Issue 196 method text."
        _, saved, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/chapters/{chapter_id}/versions",
                              token=token, key="issue196-save-" + section + "-" + suffix,
                              payload={"expected_chapter_version_id": None, "expected_spec_revision": 1,
                                       "body_markdown": body, "source_ids": []}, expected=201)
        saved_data = data(saved)
        current_id = saved_data.get("current_version_id") if isinstance(saved_data, dict) else None
        if not current_id:
            raise RuntimeError(f"chapter {section} did not return a current version")
        chapter_ids[section] = chapter_id
        request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/chapters/{chapter_id}/confirmations", token=token,
                key="issue196-confirm-" + section + "-" + suffix,
                payload={"expected_chapter_version_id": current_id, "expected_spec_revision": 1,
                         "review_decisions": []}, expected=201)
    return project_id, chapter_ids["question"], {"project": identify(project), "active": identify(data(active))}


def run_flow(base_url: str, token: str, *, canonical: bool) -> dict[str, Any]:
    project_id, chapter_id, setup = setup_project(base_url, token, canonical=canonical)
    _, current_response, _ = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}", token=token)
    current = data(current_response)
    project_version = current.get("project_version") if isinstance(current, dict) else None
    if not isinstance(project_version, int):
        raise RuntimeError("project readback omitted project_version")
    version_body = {"expected_project_version": project_version}
    _, template_check, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/template-checks", token=token,
                                   payload=version_body)
    _, check, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/checks", token=token, payload=version_body)
    check_data = data(check)
    _, snapshot_response, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/releases", token=token,
                                      key="issue196-freeze-" + uuid.uuid4().hex[:10], payload=version_body, expected=201)
    snapshot = data(snapshot_response)
    snapshot_id = snapshot.get("id") if isinstance(snapshot, dict) else None
    if not snapshot_id:
        raise RuntimeError("release did not return a snapshot ID")
    _, export_response, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/releases/{snapshot_id}/exports",
                                    token=token, key="issue196-export-" + uuid.uuid4().hex[:10],
                                    payload={"format": "docx"}, expected=(202, 201))
    export = data(export_response)
    export_id = export.get("id") if isinstance(export, dict) else None
    if not export_id:
        raise RuntimeError("export did not return an artifact ID")
    _, snapshot_read, _ = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}/releases/{snapshot_id}", token=token)
    _, export_read, _ = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}/exports/{export_id}", token=token)
    _, project_audit, _ = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}/audit", token=token)
    _, use_audit, _ = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}/rule-set-use-audits", token=token)
    use_items = data(use_audit).get("items", []) if isinstance(data(use_audit), dict) else []
    if canonical and len(use_items) < 4:
        raise RuntimeError("canonical path did not read back check/preflight/release/export use audits")
    if not canonical and use_items:
        raise RuntimeError("legacy path emitted a governed RuleSet-use audit without a binding")
    if not isinstance(data(project_audit), list) or not data(project_audit):
        raise RuntimeError("project audit readback is empty")
    # Replaying the same action key is the part of the failure matrix that is
    # deterministic without injecting a provider timeout.
    replay_key = "issue196-replay-" + uuid.uuid4().hex[:10]
    _, first_replay, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/releases", token=token,
                                 key=replay_key, payload=version_body, expected=201)
    _, second_replay, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/releases", token=token,
                                  key=replay_key, payload=version_body, expected=201)
    replayed = bool(second_replay.get("meta", {}).get("replayed"))
    if data(first_replay).get("id") != data(second_replay).get("id") or not replayed:
        raise RuntimeError("same release idempotency key did not replay the original snapshot")
    return {
        "status": "PASS",
        "mode": "canonical" if canonical else "legacy",
        "project_id": hashlib.sha256(project_id.encode()).hexdigest()[:16],
        "chapter_id": hashlib.sha256(chapter_id.encode()).hexdigest()[:16],
        "setup": setup,
        "template_check": identify(data(template_check)),
        "check": identify(check_data),
        "validation_issues": validation_issue_summary(check_data),
        "snapshot": identify(data(snapshot_response)),
        "snapshot_readback": identify(data(snapshot_read)),
        "export": identify(export),
        "export_readback": identify(data(export_read)),
        "project_audit_events": len(data(project_audit)) if isinstance(data(project_audit), list) else 0,
        "ruleset_use_audit_events": len(use_items),
        "idempotency_replay": "PASS",
    }


def run(args: argparse.Namespace) -> dict[str, Any]:
    root = args.repo_root.resolve()
    identity = source_identity(root)
    if identity["dirty"]:
        raise RuntimeError("working tree is dirty; commit the fixture before claiming current-head evidence")
    output: dict[str, Any] = {"status": "BLOCKED", "source": identity, "flows": [], "uncovered": []}
    process: subprocess.Popen[bytes] | None = None
    temporary: tempfile.TemporaryDirectory[str] | None = None
    base_url = args.base_url.rstrip("/") if args.base_url else ""
    try:
        if args.binary:
            temporary = tempfile.TemporaryDirectory(prefix="issue196-g4-")
            port = free_port()
            env = server_environment(Path(temporary.name), port)
            log = (args.output_dir / "server.log").open("wb")
            process = subprocess.Popen([str(args.binary.resolve())], cwd=root, env=env, stdout=log, stderr=subprocess.STDOUT)
            wait_health(process, f"http://127.0.0.1:{port}")
            base_url = f"http://127.0.0.1:{port}"
        if not base_url:
            raise RuntimeError("provide --base-url or --binary")
        token = register_login(base_url)
        # An unauthenticated request must not reveal an arbitrary project body.
        unauth_status, _, _ = request(base_url, "GET", "/api/v1/lingdoc/projects/does-not-exist", expected=401)
        output["unauthenticated_status"] = unauth_status
        for canonical in (True, False):
            output["flows"].append(run_flow(base_url, token, canonical=canonical))
        output["status"] = "PASS"
        output["uncovered"] = [
            {"id": "G4-BROWSER-01", "status": "NOT RUN", "reason": "No Edge/CDP endpoint was supplied to this HTTP runner."},
            {"id": "G4-MODEL-01", "status": "BLOCKED", "reason": "Real Qwen call requires an authorized provider endpoint/key configured in the service environment."},
            {"id": "G4-FAIL-01", "status": "NOT RUN", "reason": "Provider timeout/HTTP error injection is not part of the deterministic HTTP runner."},
            {"id": "G4-PG-01", "status": "NOT RUN", "reason": "This run uses isolated SQLite; PostgreSQL requires an explicit disposable DSN."},
        ]
        return output
    finally:
        if process is not None:
            stop_process(process)
        if temporary is not None:
            temporary.cleanup()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--base-url")
    parser.add_argument("--binary", type=Path)
    args = parser.parse_args()
    args.output_dir = args.output_dir.resolve()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    try:
        report = run(args)
    except (OSError, RuntimeError) as error:
        report = {"status": "BLOCKED", "reason": str(error)}
        print("BLOCKED: " + str(error))
        code = 2
    else:
        print("PASS: Issue #196 HTTP/application runner completed")
        code = 0
    target = args.output_dir / "issue196-g4-real-http.json"
    temporary = target.with_suffix(".tmp")
    temporary.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(temporary, target)
    return code


if __name__ == "__main__":
    raise SystemExit(main())
