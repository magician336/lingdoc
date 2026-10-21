#!/usr/bin/env python3
"""Exercise the real server binary across a persistent synthetic SQLite restart."""
from __future__ import annotations

import argparse
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


def minimal_environment() -> dict[str, str]:
    """Keep host runtime requirements but never inherit credentials or CI DSNs."""
    allowed = ["PATH", "LANG", "LC_ALL", "TZ", "HOME", "TMPDIR", "TMP", "TEMP"]
    if os.name == "nt":
        allowed.extend(["SYSTEMROOT", "WINDIR", "SystemDrive", "USERPROFILE"])
    return {key: os.environ[key] for key in allowed if os.environ.get(key)}


def free_loopback_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def server_environment(data_dir: Path, port: int, jwt_secret: str) -> dict[str, str]:
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
        "JWT_SECRET": jwt_secret,
        "REDIS_ADDR": "",
        "DISABLE_REGISTRATION": "false",
        "WEKNORA_AUTH_COMPLEX_PASSWORD_ENABLED": "false",
        "WEKNORA_AUTH_DEFAULT_TENANT_MODE": "create_personal",
        "WEKNORA_TENANT_ENABLE_RBAC": "true",
    })
    return env


def request_json(base_url: str, method: str, path: str, *, token: str = "",
                 payload: dict | None = None, idempotency_key: str = "") -> dict:
    body = json.dumps(payload).encode("utf-8") if payload is not None else None
    headers = {"Accept": "application/json"}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    if idempotency_key:
        headers["Idempotency-Key"] = idempotency_key
    request = urllib.request.Request(base_url + path, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=8) as response:
            raw = response.read()
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"{method} {path} returned HTTP {error.code}") from None
    except (urllib.error.URLError, TimeoutError, OSError) as error:
        raise RuntimeError(f"{method} {path} could not reach the local server ({type(error).__name__})") from None
    if not raw:
        return {}
    try:
        return json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        raise RuntimeError(f"{method} {path} returned invalid JSON") from None


def wait_until_healthy(process: subprocess.Popen, base_url: str, timeout: int = 180) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"server exited during startup (exit {process.returncode})")
        try:
            with urllib.request.urlopen(base_url + "/health", timeout=3) as response:
                if response.status == 200:
                    return
        except (urllib.error.URLError, TimeoutError, OSError):
            time.sleep(1)
    raise RuntimeError("server did not become healthy within 180 seconds")


def start_server(binary: Path, repo_root: Path, env: dict[str, str], log_path: Path) -> tuple[subprocess.Popen, object]:
    log = log_path.open("w", encoding="utf-8")
    try:
        process = subprocess.Popen([str(binary)], cwd=repo_root, env=env,
                                   stdout=log, stderr=subprocess.STDOUT)
    except OSError:
        log.close()
        raise RuntimeError("server binary could not be started") from None
    return process, log


def stop_server(process: subprocess.Popen, log) -> None:
    """Stop the server and check the exit code against this platform's semantics.

    The exit-code expectation is platform-specific, and the reason is the
    termination semantics, not a functional difference:

    - POSIX: ``Popen.terminate()`` sends SIGTERM, the server shuts down
      gracefully and exits with 0. That is the expected code there.
    - Windows: ``Popen.terminate()`` is ``TerminateProcess`` — an immediate
      kill with no graceful shutdown, so the exit code is 1. The functional
      face of the scenario (same SQLite database, same token, same project
      read back) still holds; only the exit code differs.

    Accepting 0 or 1 on Windows keeps the assertion meaningful (a crash or a
    non-zero signal failure is still rejected) instead of deleting it.
    """
    if process.poll() is None:
        process.terminate()
    try:
        exit_code = process.wait(timeout=45)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=10)
        log.close()
        raise RuntimeError("server did not complete graceful shutdown within 45 seconds") from None
    finally:
        if not log.closed:
            log.close()
    if exit_code == 0:
        return
    if os.name == "nt" and exit_code == 1:
        # TerminateProcess semantics: the process was killed, not shut down
        # gracefully. The readback assertions below still cover the
        # functional face of this scenario.
        print(
            "NOTE: server shutdown on Windows exited with code 1 (TerminateProcess semantics, "
            "not a graceful shutdown); the readback assertions still decide the scenario"
        )
        return
    raise RuntimeError(f"server shutdown returned exit {exit_code}")


def require_project(envelope: dict, project_id: str, expected_name: str, stage: str) -> None:
    project = envelope.get("data")
    if not isinstance(project, dict) or project.get("id") != project_id or project.get("name") != expected_name:
        raise RuntimeError(f"project readback after {stage} did not match persisted ID and name")


def run(binary: Path, repo_root: Path, output_dir: Path) -> None:
    if not binary.is_file():
        raise RuntimeError("server binary is missing")
    output_dir.mkdir(parents=True, exist_ok=True)
    port = free_loopback_port()
    base_url = f"http://127.0.0.1:{port}"
    username = "acceptance-" + uuid.uuid4().hex[:12]
    email = username + "@example.com"
    password = "Synthetic-" + secrets.token_hex(8)
    project_name = "synthetic restart readback " + uuid.uuid4().hex[:8]
    jwt_secret = secrets.token_urlsafe(48)
    with tempfile.TemporaryDirectory(prefix="lingdoc-full-restart-") as temporary:
        data_dir = Path(temporary)
        env = server_environment(data_dir, port, jwt_secret)
        first, first_log = start_server(binary, repo_root, env, output_dir / "server-before-restart.log")
        try:
            wait_until_healthy(first, base_url)
            registration = request_json(base_url, "POST", "/api/v1/auth/register", payload={
                "username": username, "email": email, "password": password,
            })
            if registration.get("success") is not True:
                raise RuntimeError("synthetic user registration did not succeed")
            login = request_json(base_url, "POST", "/api/v1/auth/login", payload={
                "email": email, "password": password,
            })
            token = login.get("token")
            if login.get("success") is not True or not isinstance(token, str) or not token:
                raise RuntimeError("synthetic user login did not return an access token")
            validation = request_json(base_url, "GET", "/api/v1/auth/validate", token=token)
            if validation.get("success") is not True:
                raise RuntimeError("access token did not validate before restart")
            created = request_json(base_url, "POST", "/api/v1/lingdoc/projects", token=token,
                                   idempotency_key="server-restart-" + uuid.uuid4().hex,
                                   payload={"name": project_name, "template_id": "template-demo"})
            project = created.get("data")
            project_id = project.get("id") if isinstance(project, dict) else None
            if not isinstance(project_id, str) or not project_id:
                raise RuntimeError("project creation did not return a server-assigned ID")
            require_project(request_json(base_url, "GET", "/api/v1/lingdoc/projects/" + project_id,
                                         token=token), project_id, project_name, "initial process")
            stop_server(first, first_log)
            first = None
            second, second_log = start_server(binary, repo_root, env, output_dir / "server-after-restart.log")
            try:
                wait_until_healthy(second, base_url)
                validation = request_json(base_url, "GET", "/api/v1/auth/validate", token=token)
                if validation.get("success") is not True:
                    raise RuntimeError("persisted access token did not validate after restart")
                restored = request_json(base_url, "GET", "/api/v1/lingdoc/projects/" + project_id, token=token)
                require_project(restored, project_id, project_name, "full application restart")
                print("PASS: server process restarted; same SQLite database, access token, and LingDoc project read back")
            finally:
                stop_server(second, second_log)
        finally:
            if first is not None:
                try:
                    stop_server(first, first_log)
                except RuntimeError:
                    pass


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--repo-root", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    try:
        run(args.binary.resolve(), args.repo_root.resolve(), args.output_dir.resolve())
    except (OSError, RuntimeError) as error:
        print(f"FAIL: full application process restart: {error}")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
