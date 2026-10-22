#!/usr/bin/env python3
"""Issue #310: 装配受控故障注入后，SQLite 上的后续写操作被阻塞。

这个 runner 回答三个问题（票面原文）：

1. 注入实现返回错误时，调用方是否走了显式回滚，还是让事务悬挂；
2. 回滚是否在**所有**注入点（impact_task_write / audit_write / authorization）上成立；
3. 组合根装配注入后，连接池是否被占满（每次失败都留下一条未归还的连接）。

做法：起**真实后端**（cwd 必须是仓库根，否则它会读错目录下的 config/config.yaml），
装配 LINGDOC_FAULT_INJECTION + LINGDOC_FAULT_TENANT_ID，触发一次注入，然后**紧接着
再做几次写**，记录每个请求的 HTTP 状态、耗时与是否超时；同时读回注入前后的项目
revision / 模板引用 / ImpactTask 集合 / 业务审计事件，证明注入那一笔整体未生效。

租户号必须在**启动时**就已知：注入只在进程启动时按环境变量装配一次。因此脚本分
两阶段跑同一个数据目录：阶段一不带注入注册账号并解析出它的租户号，阶段二重启
同一个数据目录并带上注入环境变量。

Usage::

    python scripts/acceptance/lingdoc_fault_injection_write.py --help
    python scripts/acceptance/lingdoc_fault_injection_write.py --live --binary .acceptance-310/lingdoc-server.exe
"""
from __future__ import annotations

import argparse
import json
import os
import re
import secrets
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
from typing import Any

# ---------------------------------------------------------------------------
# Status vocabulary (docs/agents/development-rules.md).
# ---------------------------------------------------------------------------
PASS = "PASS"
PASS_FOLLOW_UP = "PASS (follow-up)"
NA = "N/A"
WARNING = "WARNING"
NOT_RUN = "NOT RUN"
BLOCKED = "BLOCKED"
TODO = "TODO"

FAULT_POINTS = ("impact_task_write", "audit_write", "authorization")

# 注入命中时服务端给出的口径（#256 已定，不得改动）。
INJECTED_FAULT_STATUS = 503
INJECTED_FAULT_CODE = "injected_fault"


# ---------------------------------------------------------------------------
# Small helpers
# ---------------------------------------------------------------------------
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


def wait_health(process: subprocess.Popen[bytes], base_url: str, timeout: int = 240) -> None:
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


def stop_process(process: subprocess.Popen[bytes]) -> int:
    if process.poll() is None:
        process.terminate()
    try:
        code = process.wait(timeout=45)
    except subprocess.TimeoutExpired:
        process.kill()
        code = process.wait(timeout=10)
    return int(code)


def request(base_url: str, method: str, path: str, *, token: str = "",
            payload: Any = None, key: str = "", timeout: int = 60,
            expected: int | tuple[int, ...] | None = 200) -> dict[str, Any]:
    """One HTTP call. Never raises on a non-2xx status: the point of this runner
    is to *record* what the server did, including timeouts."""
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8") if payload is not None else None
    headers = {"Accept": "application/json", "X-Request-ID": "issue-310-fault-write"}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    if key:
        headers["Idempotency-Key"] = key
    started = time.monotonic()
    record: dict[str, Any] = {"method": method, "path": path, "status": 0,
                              "elapsed_ms": 0.0, "timed_out": False, "code": "", "message": ""}
    req = urllib.request.Request(base_url + path, data=body, headers=headers, method=method)
    raw = b""
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            status, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read()
    except TimeoutError:
        record["timed_out"] = True
        record["elapsed_ms"] = round((time.monotonic() - started) * 1000, 2)
        record["message"] = f"request timed out after {timeout}s"
        return record
    except (urllib.error.URLError, OSError) as error:
        record["elapsed_ms"] = round((time.monotonic() - started) * 1000, 2)
        record["message"] = f"{type(error).__name__}: {error}"
        return record
    record["status"] = status
    record["elapsed_ms"] = round((time.monotonic() - started) * 1000, 2)
    try:
        parsed = json.loads(raw.decode("utf-8")) if raw else {}
    except (UnicodeDecodeError, json.JSONDecodeError):
        parsed = {}
    if isinstance(parsed, dict):
        error_body = parsed.get("error")
        if isinstance(error_body, dict):
            record["code"] = str(error_body.get("code", ""))
            record["message"] = str(error_body.get("message", ""))
        record["body"] = parsed
    if expected is not None:
        accepted = expected if isinstance(expected, tuple) else (expected,)
        if status not in accepted:
            record["unexpected_status"] = True
    return record


def data(envelope: dict[str, Any]) -> Any:
    return envelope.get("body", {}).get("data") if isinstance(envelope.get("body"), dict) else None


# ---------------------------------------------------------------------------
# Account
# ---------------------------------------------------------------------------
def register(base_url: str) -> tuple[str, str]:
    suffix = uuid.uuid4().hex[:12]
    email = f"issue310-{suffix}@example.com"
    password = "Synthetic" + uuid.uuid4().hex[:6] + "42"
    registration = request(base_url, "POST", "/api/v1/auth/register", payload={
        "username": "issue310-" + suffix, "email": email, "password": password,
    }, timeout=120, expected=(200, 201))
    if registration.get("status") not in (200, 201):
        raise RuntimeError(f"registration failed: {registration}")
    return email, password


def login(base_url: str, email: str, password: str) -> str:
    response = request(base_url, "POST", "/api/v1/auth/login", payload={
        "email": email, "password": password,
    }, timeout=120, expected=(200,))
    body = response.get("body") or {}
    token = body.get("token")
    if not isinstance(token, str) or not token:
        raise RuntimeError(f"login did not return a token: {response}")
    return token


def tenant_id_of(db_path: Path, email: str) -> int:
    connection = sqlite3.connect(str(db_path), timeout=30)
    try:
        connection.execute("PRAGMA busy_timeout = 30000")
        row = connection.execute("SELECT tenant_id FROM users WHERE email = ?", (email,)).fetchone()
    finally:
        connection.close()
    if row and row[0]:
        return int(row[0])
    raise RuntimeError(f"could not resolve the tenant id of account {email}")


# ---------------------------------------------------------------------------
# Workspace fixture
# ---------------------------------------------------------------------------
def setup_project(base_url: str, token: str, suffix: str) -> tuple[str, list[str], dict[str, str]]:
    created = request(base_url, "POST", "/api/v1/lingdoc/projects", token=token,
                      key="issue310-project-" + suffix,
                      payload={"name": "Issue 310 fault write " + suffix,
                               "template_id": "template-demo"}, timeout=120, expected=201)
    if created.get("status") != 201:
        raise RuntimeError(f"project creation failed: {created}")
    project = data(created)
    if not isinstance(project, dict) or not project.get("id"):
        raise RuntimeError("project creation did not return a project id")
    project_id = str(project["id"])

    spec = request(base_url, "PUT", f"/api/v1/lingdoc/projects/{project_id}/spec", token=token,
                   key="issue310-spec-" + suffix,
                   payload={"expected_spec_revision": 0,
                            "fields": {"research_subject": "Issue 310 synthetic subject",
                                       "research_goal": "Issue 310 synthetic goal"}},
                   timeout=120, expected=200)
    if spec.get("status") != 200:
        raise RuntimeError(f"spec save failed: {spec}")
    version = int((data(spec) or {}).get("project_version", 0))

    receipt = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/activation-read",
                      token=token, payload={"expected_project_version": version},
                      timeout=120, expected=201)
    receipt_id = (data(receipt) or {}).get("id")
    if not receipt_id:
        raise RuntimeError(f"activation read failed: {receipt}")

    activated = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/activate", token=token,
                        key="issue310-activate-" + suffix,
                        payload={"expected_spec_revision": 1, "expected_project_version": version,
                                 "reviewed_project_version": version, "activation_read_id": receipt_id},
                        timeout=120, expected=200)
    if activated.get("status") != 200:
        raise RuntimeError(f"activation failed: {activated}")

    chapters = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}/chapters",
                       token=token, timeout=120, expected=200)
    chapter_list = data(chapters)
    if not isinstance(chapter_list, list) or not chapter_list:
        raise RuntimeError(f"chapters read failed: {chapters}")
    chapter_ids: list[str] = []
    chapter_versions: dict[str, str] = {}
    for chapter in chapter_list:
        chapter_id = str(chapter.get("id"))
        chapter_ids.append(chapter_id)
        saved = request(base_url, "POST",
                        f"/api/v1/lingdoc/projects/{project_id}/chapters/{chapter_id}/versions",
                        token=token, key="issue310-chapter-" + chapter_id + "-" + suffix,
                        payload={"expected_chapter_version_id": None, "expected_spec_revision": 1,
                                 "body_markdown": "Issue 310 synthetic chapter body.",
                                 "source_ids": []}, timeout=120, expected=201)
        if saved.get("status") != 201:
            raise RuntimeError(f"chapter save failed: {saved}")
        current_id = (data(saved) or {}).get("current_version_id")
        chapter_versions[chapter_id] = str(current_id)
        request(base_url, "POST",
                f"/api/v1/lingdoc/projects/{project_id}/chapters/{chapter_id}/confirmations",
                token=token, key="issue310-confirm-" + chapter_id + "-" + suffix,
                payload={"expected_chapter_version_id": current_id, "expected_spec_revision": 1,
                         "review_decisions": []}, timeout=120, expected=201)
    return project_id, chapter_ids, chapter_versions


def create_change_set(base_url: str, token: str, project_id: str, chapter_ids: list[str],
                      suffix: str) -> str:
    """建一条已评估的变更集（创建即评估），返回变更集 id。

    它在**没有注入**的阶段一里建：授权类注入会让该租户的写能力整体失效，所以
    前置工作必须在装注入之前完成（与两轮编排 scripts/acceptance/
    lingdoc_g3_acceptance_rounds.py 的口径一致：同一账号、同一租户、同一 SQLite）。
    """
    project_body = data(request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}",
                                token=token, timeout=60, expected=200)) or {}
    spec = project_body.get("spec") or {}
    fields = {key: {"old_value": str(spec.get(key, "")),
                    "new_value": str(spec.get(key, "")) + "（issue310 受控故障注入观测）"}
              for key in ("research_subject", "research_goal")}
    change = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/change-sets",
                     token=token, key="issue310-change-" + suffix,
                     payload={"expected_context_revision": project_body.get("current_context_revision"),
                              "fields": fields,
                              "affected_chapter_ids": chapter_ids,
                              "reason": "Issue 310 受控故障注入观测"},
                     timeout=120, expected=(201, 200))
    change_id = str((data(change) or {}).get("id") or "")
    if not change_id:
        raise RuntimeError(f"change set creation failed: {change}")
    return change_id


def sqlite_facts(db_path: Path) -> dict[str, Any]:
    """读回 SQLite 文件与写锁的事实：文件大小、mtime 与残留的 journal 文件。

    票面记录的现象之一是「SQLite 文件 mtime 停止更新」。注意生产用的是 WAL 模式，
    写入落在 -wal 边车文件上，主库文件的 mtime 只在 checkpoint 时才变——所以这里
    同时看主库文件与 -wal 边车文件，避免用主库 mtime 误判「没有写入」。
    """
    facts: dict[str, Any] = {"path": str(db_path)}
    for suffix in ("", "-wal", "-shm", "-journal"):
        candidate = Path(str(db_path) + suffix)
        if candidate.exists():
            stat = candidate.stat()
            facts[suffix or "main"] = {"size_bytes": stat.st_size, "mtime": stat.st_mtime}
        else:
            facts[suffix or "main"] = None
    return facts


def read_state(base_url: str, token: str, project_id: str) -> dict[str, Any]:
    project = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}", token=token,
                      timeout=60, expected=200)
    tasks = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}/impact-tasks",
                    token=token, timeout=60, expected=200)
    audit = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}/audit",
                    token=token, timeout=60, expected=200)
    project_body = data(project) or {}
    task_list = data(tasks) or []
    audit_list = data(audit) or []
    # 业务审计（apply 的那条）单独计数：授权决定审计是在 apply 的**读事务**里写
    # 并提交的，它不属于失败的那一笔事务，不能算进回滚的比较里。口径与验收台
    # （frontend/src/views/lingdoc/impactCenterAcceptance/chains/faultInjection.ts）一致。
    apply_audits = sum(1 for item in audit_list
                        if isinstance(item, dict) and item.get("action") == "project.change_set.apply")
    return {
        "project": {"status": project.get("status"), "timed_out": project.get("timed_out"),
                    "project_version": project_body.get("project_version"),
                    "spec_revision": project_body.get("spec_revision"),
                    "current_context_revision": project_body.get("current_context_revision"),
                    "template_id": project_body.get("template_id"),
                    "template_version": project_body.get("template_version"),
                    "template_copy_version": project_body.get("template_copy_version"),
                    "spec": project_body.get("spec")},
        "impact_tasks": {"status": tasks.get("status"), "timed_out": tasks.get("timed_out"),
                         "count": len(task_list) if isinstance(task_list, list) else -1,
                         "ids": sorted(str(item.get("id")) for item in task_list)
                         if isinstance(task_list, list) else []},
        "audit": {"status": audit.get("status"), "timed_out": audit.get("timed_out"),
                  "count": len(audit_list) if isinstance(audit_list, list) else -1,
                  "apply_audits": apply_audits,
                  "actions": sorted(str(item.get("action")) for item in audit_list)
                  if isinstance(audit_list, list) else []},
    }


# ---------------------------------------------------------------------------
# Log inspection
# ---------------------------------------------------------------------------
INTERESTING = re.compile(
    r"injected_fault|受控故障注入|database is locked|busy|locked|deadlock|rollback|回滚|"
    r"sql|transaction|pool|too many open files|context deadline",
    re.IGNORECASE)


def log_lines(log_path: Path, limit: int = 60) -> list[str]:
    if not log_path.exists():
        return []
    text = log_path.read_text(encoding="utf-8", errors="replace")
    lines = [line for line in text.splitlines() if line.strip()]
    interesting = [line for line in lines if INTERESTING.search(line)]
    return interesting[-limit:] if interesting else lines[-limit:]


# ---------------------------------------------------------------------------
# Runner
# ---------------------------------------------------------------------------
def build_server(root: Path) -> Path:
    """没有给 --binary 时，就地编译当前 checkout 的后端。

    这样 ``python scripts/acceptance/lingdoc_fault_injection_write.py --live`` 就是一条
    自足的命令：它跑的就是**当前工作区**的代码，而不是某个来路不明的旧二进制。
    """
    out_dir = Path(tempfile.mkdtemp(prefix="issue310-build-"))
    binary = out_dir / ("lingdoc-server.exe" if os.name == "nt" else "lingdoc-server")
    command = ["go", "build", "-o", str(binary), "./cmd/server"]
    print("building server: " + " ".join(command), flush=True)
    result = subprocess.run(command, cwd=str(root), capture_output=True, text=True,
                            encoding="utf-8", errors="replace", timeout=1800)
    if result.returncode != 0:
        raise RuntimeError("go build failed: " + (result.stderr or result.stdout)[-2000:])
    return binary


def run(args: argparse.Namespace) -> dict[str, Any]:
    root = Path(args.repo_root).resolve()
    report: dict[str, Any] = {
        "ticket": "#310：装配受控故障注入后，SQLite 上的后续写操作被阻塞",
        "mode": "live" if args.live else "dry-run",
        "fault_point": args.point,
        "steps": [],
        "notes": [],
    }

    def step(name: str, status: str, detail: Any = None) -> None:
        report["steps"].append({"step": name, "status": status, "detail": detail})

    if not args.live:
        report["status"] = NOT_RUN
        report["notes"].append("未指定 --live：本脚本默认不实跑。")
        return report

    binary = Path(args.binary).resolve() if args.binary else build_server(root)
    if not binary.exists():
        raise RuntimeError(f"--live needs an existing server binary (--binary): {binary}")

    temporary = tempfile.TemporaryDirectory(prefix="issue310-fault-write-")
    data_dir = Path(temporary.name)
    db_path = data_dir / "lingdoc.sqlite"
    email = ""
    password = ""
    tenant_id = 0
    phase_one_log = data_dir / "server-phase1.log"
    phase_two_log = data_dir / "server-phase2.log"

    # ---- 阶段一：注册账号、解析租户号、并在**没有注入**的情况下把项目与变更集
    # 建好（注入只在进程启动时装配，所以项目必须在装注入之前就绪） ----
    port = free_port()
    env = server_environment(data_dir, port)
    with phase_one_log.open("wb") as log:
        process = subprocess.Popen([str(binary)], cwd=root, env=env, stdout=log,
                                   stderr=subprocess.STDOUT)
        base_url = f"http://127.0.0.1:{port}"
        try:
            wait_health(process, base_url)
            email, password = register(base_url)
            tenant_id = tenant_id_of(db_path, email)
            token = login(base_url, email, password)
            project_id, chapter_ids, chapter_versions = setup_project(base_url, token, uuid.uuid4().hex[:8])
            change_id = create_change_set(base_url, token, project_id, chapter_ids,
                                          uuid.uuid4().hex[:8])
        finally:
            stop_process(process)
    step("phase1_register", PASS, {"tenant_id": tenant_id, "account": email})
    step("phase1_setup", PASS, {"project_id": project_id, "change_set_id": change_id})

    # ---- 阶段二：带上注入环境变量重启同一个数据目录 ----
    port = free_port()
    env = server_environment(data_dir, port)
    env["LINGDOC_FAULT_INJECTION"] = args.point
    env["LINGDOC_FAULT_TENANT_ID"] = str(tenant_id)
    if args.fault_action:
        env["LINGDOC_FAULT_ACTION"] = args.fault_action
    env["LINGDOC_FAULT_REASON"] = args.fault_reason
    suffix = uuid.uuid4().hex[:8]
    with phase_two_log.open("wb") as log:
        process = subprocess.Popen([str(binary)], cwd=root, env=env, stdout=log,
                                   stderr=subprocess.STDOUT)
        base_url = f"http://127.0.0.1:{port}"
        try:
            wait_health(process, base_url)
            step("phase2_server_start", PASS, {"port": port, "db_path": str(db_path),
                                               "fault_point": args.point,
                                               "fault_tenant": tenant_id})
            token = login(base_url, email, password)

            # 注入前的前态：项目与变更集在阶段一（无注入）里已经建好。
            before = read_state(base_url, token, project_id)
            report["state_before"] = before
            step("read_state_before", PASS if before["project"]["status"] == 200 else BLOCKED, before)

            apply = request(base_url, "POST",
                            f"/api/v1/lingdoc/projects/{project_id}/change-sets/{change_id}/apply",
                            token=token, key="issue310-apply-" + suffix, payload={},
                            timeout=args.timeout, expected=None)
            report["apply"] = {k: v for k, v in apply.items() if k != "body"}
            report["apply"]["body_summary"] = {
                "error_code": apply.get("code"), "error_message": apply.get("message")}
            injected = (apply.get("status") == INJECTED_FAULT_STATUS
                        and apply.get("code") == INJECTED_FAULT_CODE)
            report["apply"]["injected_fault_observed"] = injected
            step("apply", PASS if apply.get("status") else BLOCKED, report["apply"])

            # 注入命中的那一笔整体未生效：先读回（**在后续写之前**读，后续写本身
            # 会推进 project_version 与审计条数）。
            after_injection = read_state(base_url, token, project_id)
            report["state_after_injection"] = after_injection
            report["rollback_unchanged"] = {
                "project": before["project"] == after_injection["project"],
                "impact_tasks": before["impact_tasks"] == after_injection["impact_tasks"],
                # 业务审计（apply 那条）的条数必须没有增加：失败的那一笔事务整体
                # 未生效。授权决定审计不算——它在 apply 的读事务里提交。
                "business_audit": before["audit"]["apply_audits"] == after_injection["audit"]["apply_audits"],
            }
            step("rollback_readback",
                 PASS if all(report["rollback_unchanged"].values()) else WARNING,
                 report["rollback_unchanged"])
            report["sqlite_after_injection"] = sqlite_facts(db_path)

            # ---- 紧接着的写：这是本票要证明的那一步 ----
            # 写的种类覆盖票面记录的那一笔（建项目）以及同一项目上的另一笔写
            # （章节版本），并且每笔都带请求级超时：挂住会被记成 timed_out，而不是
            # 让脚本无限等下去。
            writes: list[dict[str, Any]] = []
            for index in range(args.writes):
                write = request(base_url, "POST", "/api/v1/lingdoc/projects", token=token,
                                key=f"issue310-after-write-{suffix}-{index}",
                                payload={"name": f"Issue 310 after-injection write {index}",
                                         "template_id": "template-demo"},
                                timeout=args.timeout, expected=None)
                writes.append({"write": f"create project #{index}", **{k: v for k, v in write.items() if k != "body"}})
            for chapter in chapter_ids:
                write = request(base_url, "POST",
                                f"/api/v1/lingdoc/projects/{project_id}/chapters/{chapter}/versions",
                                token=token, key=f"issue310-after-chapter-{suffix}-{chapter}",
                                payload={"expected_chapter_version_id": chapter_versions.get(chapter),
                                         "expected_spec_revision": 1,
                                         "body_markdown": "Issue 310 after-injection chapter body.",
                                         "source_ids": []},
                                timeout=args.timeout, expected=None)
                writes.append({"write": f"chapter version {chapter}",
                               **{k: v for k, v in write.items() if k != "body"}})
            report["writes_after_injection"] = writes
            blocked = [w for w in writes if w.get("timed_out") or w.get("status") == 0]
            step("writes_after_injection", BLOCKED if blocked else PASS, writes)

            after = read_state(base_url, token, project_id)
            report["state_after_writes"] = after
            step("read_state_after_writes", PASS if after["project"]["status"] == 200 else BLOCKED, after)

            report["server_log_tail"] = log_lines(phase_two_log)
        finally:
            stop_process(process)

    report["status"] = BLOCKED if any(s["status"] == BLOCKED for s in report["steps"]) else PASS
    report["notes"].append(
        "注入命中口径：HTTP 503 + code=injected_fault（#256 已定）。")
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", action="store_true", help="run against a real server process")
    parser.add_argument("--binary", default="",
                        help="path to the server binary (default: build the current checkout)")
    parser.add_argument("--repo-root", default=str(Path(__file__).resolve().parents[2]),
                        help="repository root (the server must run with this as cwd)")
    parser.add_argument("--point", default="impact_task_write", choices=list(FAULT_POINTS),
                        help="fault injection point")
    parser.add_argument("--fault-action", default="", help="optional LINGDOC_FAULT_ACTION")
    parser.add_argument("--fault-reason", default="Issue 310 受控故障注入复现", help="fault reason")
    parser.add_argument("--writes", type=int, default=3, help="how many writes after the injection")
    parser.add_argument("--timeout", type=int, default=20,
                        help="per-request timeout in seconds (a stuck request is recorded, never waited on forever)")
    args = parser.parse_args()
    try:
        report = run(args)
    except Exception as error:  # noqa: BLE001 - the runner reports failures as data
        report = {"status": BLOCKED, "error": f"{type(error).__name__}: {error}"}
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0 if report.get("status") in (PASS, NOT_RUN) else 1


if __name__ == "__main__":
    sys.exit(main())
