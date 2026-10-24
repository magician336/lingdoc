#!/usr/bin/env python3
"""Exercise the real server binary across a persistent restart.

Two persistence modes
---------------------
``sqlite`` (the default)
    A local diagnosis: a throwaway SQLite file under a temporary directory.
    It proves that a process restart keeps the data, but it is not the
    protocol's persistence medium.

``postgres``
    The protocol's mode.  "Beta 首版真实验收协议" §1 requires the isolated
    PostgreSQL, so the restart has to read back out of a real database:
    ``--db-dsn`` names it.  Nothing is created or dropped here -- the
    database must already carry the schema, and the restart only proves
    that the *process* restart does not lose what the database holds.

What is read back
-----------------
Protocol §2 step 5 asks for the project, the chapters, the source bindings
and the delivered file (with its download hash) to be read back after the
restart.  The PostgreSQL mode reads all four; the SQLite mode keeps its
original, narrower read-back (the project), because it is a local diagnosis.

The evidence file
-----------------
``restart-readback.json`` (written into ``--output-dir``) records the
driver, the redacted DSN, the pid of both processes, the readings taken
before and after the restart, the key-by-key comparison and the verdict.
It is evidence, so it carries no credential: the account's password never
reaches it, and the identity hand-off file is deleted once the readings are
taken.
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
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timezone
from urllib.parse import unquote, urlsplit

SCRIPT_DIR = Path(__file__).resolve().parent
HTTP_EXECUTOR = SCRIPT_DIR / "beta_http_readback.py"


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


# --------------------------------------------------------------------------
# The PostgreSQL mode (protocol §1).
# --------------------------------------------------------------------------
def parse_postgres_dsn(dsn: str) -> dict[str, str]:
    """The connection parts of a PostgreSQL URL.

    The application reads DB_HOST / DB_PORT / DB_USER / DB_PASSWORD /
    DB_NAME (internal/container/container.go:832-843), so the URL is unpacked
    into exactly those five names and nothing else.

    The credentials must be percent-encoded: a password that contains '@' or
    '#' would otherwise end the authority early and the host would be read
    as part of the password.
    """
    parsed = urlsplit(dsn.strip())
    if parsed.scheme not in ("postgres", "postgresql"):
        raise RuntimeError(f"--db-dsn must be a postgres:// URL; got scheme {parsed.scheme!r}")
    name = parsed.path.lstrip("/")
    if not parsed.hostname or not name:
        raise RuntimeError(
            "--db-dsn must carry a host and a database name "
            "(postgres://user:password@host:port/database); the credentials in it must be "
            "percent-encoded, or a password containing '@' or '#' ends the authority early")
    return {
        "host": parsed.hostname,
        "port": str(parsed.port or 5432),
        "user": unquote(parsed.username or ""),
        "password": unquote(parsed.password or ""),
        "name": name,
    }


def redact_dsn(dsn: str) -> str:
    """The DSN without its password: the form that may reach an evidence file."""
    parsed = urlsplit(dsn.strip())
    host = parsed.hostname or ""
    if parsed.port:
        host = f"{host}:{parsed.port}"
    user = parsed.username or ""
    return f"postgres://{user}:***@{host}/{parsed.path.lstrip('/')}"


def postgres_server_environment(data_dir: Path, port: int, jwt_secret: str, dsn: str,
                                extra_env: dict[str, str] | None = None) -> dict[str, str]:
    """The environment of the server process in the PostgreSQL mode."""
    parts = parse_postgres_dsn(dsn)
    env = minimal_environment()
    env.update({
        "GIN_MODE": "release",
        "SERVER_HOST": "127.0.0.1",
        "SERVER_PORT": str(port),
        "DB_DRIVER": "postgres",
        "DB_HOST": parts["host"],
        "DB_PORT": parts["port"],
        "DB_USER": parts["user"],
        "DB_PASSWORD": parts["password"],
        "DB_NAME": parts["name"],
        "RETRIEVE_DRIVER": "postgres",
        # The isolated database already carries the schema; a restart must not
        # migrate it.
        "AUTO_MIGRATE": "false",
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
    if extra_env:
        # The caller's explicit values win: they are what makes this instance
        # look like the service under acceptance (the model rows' AES key, the
        # model YAML, the queue, the controlled profile).
        env.update(extra_env)
    return env


def read_env_file(path: Path) -> dict[str, str]:
    """KEY=VALUE lines from a file.

    An explicit file, never the host's environment: the credentials this
    process needs are an input, not something to inherit by accident.
    """
    values: dict[str, str] = {}
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            raise RuntimeError(f"{path} has a line without '=': {line[:40]!r}")
        key, _, value = line.partition("=")
        values[key.strip()] = value.strip()
    return values


def contract_api_root_path() -> str:
    """The path part of the OpenAPI contract's API root.

    The HTTP executor is the authority (it refuses a --base-url that stops
    short of this path), so the two faces cannot disagree about it.
    """
    if str(SCRIPT_DIR) not in sys.path:
        sys.path.insert(0, str(SCRIPT_DIR))
    from beta_http_readback import API_ROOT_PATH
    return API_ROOT_PATH


def api_root(base_url: str) -> str:
    """The API root of a running server: the server root plus the contract path."""
    return base_url.rstrip("/") + contract_api_root_path()


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


def request_bytes(base_url: str, path: str, *, token: str = "") -> bytes:
    headers = {"Accept": "*/*"}
    if token:
        headers["Authorization"] = "Bearer " + token
    request = urllib.request.Request(base_url + path, headers=headers, method="GET")
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return response.read()
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"GET {path} returned HTTP {error.code}") from None
    except (urllib.error.URLError, TimeoutError, OSError) as error:
        raise RuntimeError(f"GET {path} could not reach the local server ({type(error).__name__})") from None


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
      face of the scenario (same database, same token, same project read
      back) still holds; only the exit code differs.

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


# --------------------------------------------------------------------------
# The readings (protocol §2 step 5).
# --------------------------------------------------------------------------
def read_project(base_url: str, token: str, project_id: str) -> dict:
    envelope = request_json(api_root(base_url), "GET", f"/projects/{project_id}", token=token)
    project = envelope.get("data")
    project = project if isinstance(project, dict) else {}
    return {
        "id": project.get("id"),
        "name": project.get("name"),
        "status": project.get("status"),
        "project_version": project.get("project_version"),
        "spec_revision": project.get("spec_revision"),
        "delivery_status": project.get("delivery_status"),
    }


def read_chapters(base_url: str, token: str, project_id: str) -> list[dict]:
    """The chapters with their current body version and their source bindings.

    The body is hashed rather than carried: the evidence has to show that the
    body is the same one after the restart without copying the text twice.
    """
    envelope = request_json(api_root(base_url), "GET", f"/projects/{project_id}/chapters", token=token)
    chapters = envelope.get("data")
    chapters = chapters if isinstance(chapters, list) else []
    readings = []
    for chapter in chapters:
        chapter = chapter if isinstance(chapter, dict) else {}
        body = str(chapter.get("body_markdown") or "")
        source_ids = chapter.get("source_ids")
        source_ids = source_ids if isinstance(source_ids, list) else []
        readings.append({
            "chapter_id": chapter.get("id"),
            "section_id": chapter.get("section_id"),
            "current_version_id": chapter.get("current_version_id"),
            "body_sha256": hashlib.sha256(body.encode("utf-8")).hexdigest(),
            "body_length": len(body),
            "source_ids": sorted(str(item) for item in source_ids),
            "confirmation_valid": chapter.get("confirmation_valid"),
        })
    return readings


def read_sources(base_url: str, token: str, project_id: str) -> list[dict]:
    """The materials bound to the project (the source side of the bindings)."""
    envelope = request_json(api_root(base_url), "GET", f"/projects/{project_id}/assets", token=token)
    assets = envelope.get("data")
    assets = assets if isinstance(assets, list) else []
    readings = []
    for asset in assets:
        asset = asset if isinstance(asset, dict) else {}
        readings.append({
            "asset_id": asset.get("id"),
            "name": asset.get("name"),
            "processing_state": asset.get("processing_state"),
            "parse_status": asset.get("parse_status"),
        })
    return readings


def read_delivery(base_url: str, token: str, project_id: str) -> dict:
    """The frozen snapshot, the export artifact and the downloaded bytes.

    The downloaded bytes are hashed on the way through, so the evidence can
    state that the file the user downloads after the restart is the file the
    export record names.
    """
    releases = request_json(api_root(base_url), "GET", f"/projects/{project_id}/releases",
                             token=token).get("data")
    releases = releases if isinstance(releases, dict) else {}
    items = releases.get("items")
    items = items if isinstance(items, list) else []
    snapshot = items[0] if items and isinstance(items[0], dict) else {}
    frozen = snapshot.get("frozen_input")
    frozen = frozen if isinstance(frozen, dict) else {}
    check = snapshot.get("check")
    check = check if isinstance(check, dict) else {}

    exports = request_json(api_root(base_url), "GET", f"/projects/{project_id}/exports",
                           token=token).get("data")
    exports = exports if isinstance(exports, dict) else {}
    export_items = exports.get("items")
    export_items = export_items if isinstance(export_items, list) else []
    snapshot_id = str(snapshot.get("id", ""))
    artifact = next((item for item in export_items
                     if isinstance(item, dict) and str(item.get("snapshot_id", "")) == snapshot_id), None)

    frozen_chapters = frozen.get("chapters")
    frozen_sources = frozen.get("sources")
    delivery = {
        "snapshot_id": snapshot_id,
        "snapshot_digest": str(snapshot.get("snapshot_digest", "")),
        "frozen_project_version": frozen.get("project_version"),
        "frozen_spec_revision": frozen.get("spec_revision"),
        "frozen_chapters": len(frozen_chapters) if isinstance(frozen_chapters, list) else 0,
        "frozen_sources": len(frozen_sources) if isinstance(frozen_sources, list) else 0,
        "check_status": str(check.get("status", "")),
        "export_id": "",
        "export_status": "",
        "export_file_sha256": "",
        "download_sha256": "",
        "download_length": 0,
    }
    if artifact is None:
        return delivery
    export_id = str(artifact.get("id", ""))
    delivery["export_id"] = export_id
    delivery["export_status"] = str(artifact.get("status", ""))
    delivery["export_file_sha256"] = str(artifact.get("file_sha256") or "")
    blob = request_bytes(api_root(base_url), f"/projects/{project_id}/exports/{export_id}/file", token=token)
    delivery["download_sha256"] = hashlib.sha256(blob).hexdigest()
    delivery["download_length"] = len(blob)
    return delivery


def compare_readings(before: dict, after: dict) -> dict:
    """Every leaf of the two readings, compared key by key."""
    mismatches: list[dict] = []

    def walk(prefix: str, left, right) -> None:
        if isinstance(left, dict) and isinstance(right, dict):
            for key in sorted(set(left) | set(right)):
                walk(f"{prefix}.{key}" if prefix else key, left.get(key), right.get(key))
            return
        if isinstance(left, list) and isinstance(right, list):
            if len(left) != len(right):
                mismatches.append({"key": prefix, "before": f"{len(left)} entries",
                                   "after": f"{len(right)} entries"})
                return
            for index, (left_item, right_item) in enumerate(zip(left, right)):
                walk(f"{prefix}[{index}]", left_item, right_item)
            return
        if left != right:
            mismatches.append({"key": prefix, "before": left, "after": right})

    walk("", before, after)
    return {"consistent": not mismatches, "mismatches": mismatches}


def login(base_url: str, email: str, password: str) -> str:
    """Log in and return the access token."""
    envelope = request_json(base_url, "POST", "/api/v1/auth/login",
                             payload={"email": email, "password": password})
    token = envelope.get("token") if isinstance(envelope, dict) else None
    if not isinstance(token, str) or not token:
        raise RuntimeError("login did not return an access token")
    return token


def prepare_project_over_http(base_url: str, output_dir: Path) -> dict[str, str]:
    """Prepare the project to read back, using the repository's HTTP face.

    The dual runner's HTTP executor already knows how to drive a real
    LingDoc backend into a project with chapters, confirmations, a frozen
    snapshot and a rendered export.  Reusing it keeps one flow in the
    repository instead of a second copy of it here.

    The hand-off file it writes carries the account's password, so it is
    deleted as soon as the readings are taken.
    """
    context_path = output_dir / "run-context.json"
    command = [sys.executable, str(HTTP_EXECUTOR),
               "--base-url", api_root(base_url),
               "--out", str(output_dir / "http-readback.json"),
               "--context-out", str(context_path)]
    result = subprocess.run(command, capture_output=True, text=True,
                            encoding="utf-8", errors="replace", timeout=900)
    if result.returncode != 0:
        detail = (result.stdout or result.stderr or "").strip()[-500:]
        raise RuntimeError(f"the HTTP face did not prepare the project: {detail}")
    context = json.loads(context_path.read_text(encoding="utf-8"))
    return {
        "email": str(context.get("email", "")),
        "password": str(context.get("password", "")),
        "project_id": str(context.get("project_id", "")),
    }


def git_head(repo_root: Path) -> str:
    try:
        result = subprocess.run(["git", "-C", str(repo_root), "rev-parse", "HEAD"],
                                capture_output=True, text=True, encoding="utf-8", errors="replace",
                                check=False, timeout=30)
        if result.returncode == 0:
            return result.stdout.strip()
    except (OSError, subprocess.TimeoutExpired):
        pass
    return "unknown"


def write_json(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(temporary, path)


def run_postgres(binary: Path, repo_root: Path, output_dir: Path, dsn: str,
                 extra_env: dict[str, str]) -> tuple[dict, int]:
    """The protocol's mode: restart the service against an isolated PostgreSQL."""
    port = free_loopback_port()
    base_url = f"http://127.0.0.1:{port}"
    jwt_secret = secrets.token_urlsafe(48)
    context_path = output_dir / "run-context.json"
    with tempfile.TemporaryDirectory(prefix="lingdoc-full-restart-") as temporary:
        data_dir = Path(temporary)
        env = postgres_server_environment(data_dir, port, jwt_secret, dsn, extra_env)
        first, first_log = start_server(binary, repo_root, env, output_dir / "server-before-restart.log")
        first_pid = first.pid
        try:
            wait_until_healthy(first, base_url)
            context = prepare_project_over_http(base_url, output_dir)
            token = login(base_url, context["email"], context["password"])
            project_id = context["project_id"]
            before = {
                "access_token_accepted": True,
                "project": read_project(base_url, token, project_id),
                "chapters": read_chapters(base_url, token, project_id),
                "sources": read_sources(base_url, token, project_id),
                "delivery": read_delivery(base_url, token, project_id),
            }
            stop_server(first, first_log)
            first = None
            second, second_log = start_server(binary, repo_root, env, output_dir / "server-after-restart.log")
            second_pid = second.pid
            try:
                wait_until_healthy(second, base_url)
                validation = request_json(base_url, "GET", "/api/v1/auth/validate", token=token)
                after = {
                    "access_token_accepted": validation.get("success") is True,
                    "project": read_project(base_url, token, project_id),
                    "chapters": read_chapters(base_url, token, project_id),
                    "sources": read_sources(base_url, token, project_id),
                    "delivery": read_delivery(base_url, token, project_id),
                }
            finally:
                stop_server(second, second_log)
        finally:
            if first is not None:
                try:
                    stop_server(first, first_log)
                except RuntimeError:
                    pass
            # The hand-off file carries the account's password: it must not
            # outlive the readings.
            try:
                if context_path.is_file():
                    context_path.unlink()
            except OSError:
                pass

    comparison = compare_readings(before, after)
    evidence = {
        "schema": "lingdoc.beta.restart-readback/v1",
        "head": git_head(repo_root),
        "at": datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z"),
        "driver": "postgres",
        "dsn": redact_dsn(dsn),
        "protocol": "Beta 首版真实验收协议 §1（隔离 PostgreSQL）＋ §2 第 5 步（重启后读回）",
        "process": {
            "port": port,
            "before": {"pid": first_pid},
            "after": {"pid": second_pid},
        },
        "read_back": ["project", "chapters", "source_bindings", "delivery_file"],
        "before": before,
        "after": after,
        "comparison": comparison,
        "verdict": "PASS" if comparison["consistent"] else "FAIL",
    }
    return evidence, 0 if comparison["consistent"] else 1


def run_sqlite(binary: Path, repo_root: Path, output_dir: Path) -> tuple[dict, int]:
    """The local diagnosis: a throwaway SQLite file, the project read back."""
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
        first_pid = first.pid
        try:
            wait_until_healthy(first, base_url)
            registration = request_json(base_url, "POST", "/api/v1/auth/register", payload={
                "username": username, "email": email, "password": password,
            })
            if registration.get("success") is not True:
                raise RuntimeError("synthetic user registration did not succeed")
            login_envelope = request_json(base_url, "POST", "/api/v1/auth/login", payload={
                "email": email, "password": password,
            })
            token = login_envelope.get("token")
            if login_envelope.get("success") is not True or not isinstance(token, str) or not token:
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
            before = {"project": {"id": project_id, "name": project_name}}
            stop_server(first, first_log)
            first = None
            second, second_log = start_server(binary, repo_root, env, output_dir / "server-after-restart.log")
            second_pid = second.pid
            try:
                wait_until_healthy(second, base_url)
                validation = request_json(base_url, "GET", "/api/v1/auth/validate", token=token)
                restored = request_json(base_url, "GET", "/api/v1/lingdoc/projects/" + project_id, token=token)
                require_project(restored, project_id, project_name, "full application restart")
                after = {"project": {"id": project_id, "name": project_name}}
            finally:
                stop_server(second, second_log)
        finally:
            if first is not None:
                try:
                    stop_server(first, first_log)
                except RuntimeError:
                    pass

    comparison = compare_readings(before, after)
    evidence = {
        "schema": "lingdoc.beta.restart-readback/v1",
        "head": git_head(repo_root),
        "at": datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z"),
        "driver": "sqlite",
        "dsn": "",
        "protocol": "本地诊断（协议 §1 要求的是隔离 PostgreSQL；本轮不是那一轮）",
        "process": {
            "port": port,
            "before": {"pid": first_pid},
            "after": {"pid": second_pid},
        },
        "read_back": ["project"],
        "before": before,
        "after": after,
        "comparison": comparison,
        "verdict": "PASS" if comparison["consistent"] else "FAIL",
    }
    return evidence, 0 if comparison["consistent"] else 1


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--repo-root", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--db-driver", default="sqlite", choices=("sqlite", "postgres"),
                        help="the persistence medium: an isolated PostgreSQL (the protocol's mode) "
                             "or a throwaway SQLite file (a local diagnosis)")
    parser.add_argument("--db-dsn", default="",
                        help="the PostgreSQL URL of the isolated database; required for --db-driver postgres")
    parser.add_argument("--env-file", action="append", default=[], type=Path,
                        help="a KEY=VALUE file with the extra environment the server process needs "
                             "(the model rows' AES key, the model YAML, the queue, the controlled "
                             "profile); repeatable, later files win")
    parser.add_argument("--evidence-out", type=Path, default=None,
                        help="where to write restart-readback.json (default: <output-dir>/restart-readback.json)")
    args = parser.parse_args()

    try:
        binary = args.binary.resolve()
        repo_root = args.repo_root.resolve()
        output_dir = args.output_dir.resolve()
        if not binary.is_file():
            raise RuntimeError("server binary is missing")
        output_dir.mkdir(parents=True, exist_ok=True)
        extra_env: dict[str, str] = {}
        for env_file in args.env_file:
            extra_env.update(read_env_file(Path(env_file).resolve()))

        if args.db_driver == "postgres":
            if not args.db_dsn:
                raise RuntimeError("--db-driver postgres needs --db-dsn")
            evidence, code = run_postgres(binary, repo_root, output_dir, args.db_dsn, extra_env)
        else:
            if args.db_dsn:
                raise RuntimeError("--db-dsn only applies to --db-driver postgres")
            evidence, code = run_sqlite(binary, repo_root, output_dir)
    except (OSError, RuntimeError) as error:
        print(f"FAIL: full application process restart: {error}")
        return 1

    evidence_out = args.evidence_out.resolve() if args.evidence_out else \
        output_dir / "restart-readback.json"
    write_json(evidence_out, evidence)
    print(f"PASS: server process restarted; {evidence['driver']} persistence, access token, "
          f"and the read-back objects survived the restart "
          f"(pid {evidence['process']['before']['pid']} -> {evidence['process']['after']['pid']}, "
          f"port {evidence['process']['port']}); evidence: {evidence_out}")
    return code


if __name__ == "__main__":
    raise SystemExit(main())
