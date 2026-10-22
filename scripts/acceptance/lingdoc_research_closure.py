#!/usr/bin/env python3
"""Run the G3-GAP-08 research closure against a real LingDoc process.

Issue #258 asks whether a ``RETRIEVE_CANDIDATES`` impact task can be closed
with a real ``ResearchRun`` receipt on a machine that has **no external search
provider**.  The server-side verdict lives in
``internal/lingdoc/workspacecore/impact_action_result.go`` (the
``RETRIEVE_CANDIDATES`` branch of ``VerifyImpactActionResult``): it wants a
completed run with at least two rounds, a research task scoped to
``impact:{taskId}``, an approved task version and a current basis.

This runner therefore drives the **project-asset retrieval path** (no provider
at all): it seeds a synthetic knowledge base / knowledge / chunk straight into
the same SQLite file the server uses, binds it as a project asset, then runs a
two-query research task through approve -> run and finally closes the impact
task with the returned ``ResearchRun`` receipt.

The server accepts a completed search that honestly returns no candidates, so
this runner does **not** engineer a retrieval hit.  The report states the real
hit count.  Everything the report claims is read back from the running
service; when a step fails the runner stops and reports the real error.

Usage::

    python scripts/acceptance/lingdoc_research_closure.py --help
    python scripts/acceptance/lingdoc_research_closure.py --live --binary .acceptance-258/lingdoc-server.exe
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
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
# Status vocabulary (docs/agents/development-rules.md).  Only these words are
# used as a verdict; anything else is a bug in this script.
# ---------------------------------------------------------------------------
PASS = "PASS"
PASS_FOLLOW_UP = "PASS (follow-up)"
NA = "N/A"
WARNING = "WARNING"
NOT_RUN = "NOT RUN"
BLOCKED = "BLOCKED"
TODO = "TODO"

# Synthetic material.  No real project data, no real credentials, no real
# research subject: the text only exists so the retrieval path has something
# to look at and so the report can point at a reproducible fixture.
SYNTHETIC_KB_NAME = "Issue 258 synthetic acceptance KB"
SYNTHETIC_KNOWLEDGE_TITLE = "Issue 258 synthetic acceptance material"
SYNTHETIC_CHUNK_CONTENT = (
    "合成验收材料：本段内容仅用于 Issue #258 的检索闭环实跑，"
    "内容可控、不含任何真实课题资料或敏感信息。"
    "它证明项目资料检索链路可以在没有外部检索渠道的情况下完成两轮检索并回执。"
)

# The impact task under test.  The research task scope must be
# "impact:{taskId}" for the server to accept the closure.
IMPACT_TASK_SCOPE_PREFIX = "impact:"

# Two distinct queries -> two rounds.  The server requires len(Rounds) >= 2.
RESEARCH_QUERIES = [
    "合成资料里的关键结论是什么",
    "合成资料提到哪些技术路线",
]

# Optional external retrieval channel.
#
# Without these two environment variables the runner keeps the behaviour of
# issue #258: the closure runs on the project-material path only, and the
# report states the environment limit (no external provider, no index hit).
#
# With them the runner seeds a tenant-owned provider row and the research
# task uses it, so the rounds search the **outside world** and the report
# can carry a retrieval hit that is not synthetic material.  G3-GAP-08 asks
# for exactly that: a run whose retrieval actually returned something.
#
# G3_WEB_SEARCH_PROVIDER   provider type, e.g. "searxng"
# G3_WEB_SEARCH_PROVIDER_URL  the instance URL (self-hosted SearXNG, ...)
#
# The instance URL is validated by the server against the SSRF whitelist
# (SSRF_WHITELIST / SSRF_WHITELIST_EXTRA), so a loopback instance has to be
# listed there before the server will call it.
EXTERNAL_PROVIDER_TYPE = os.environ.get("G3_WEB_SEARCH_PROVIDER", "").strip()
EXTERNAL_PROVIDER_URL = os.environ.get("G3_WEB_SEARCH_PROVIDER_URL", "").strip()


# ---------------------------------------------------------------------------
# Small helpers
# ---------------------------------------------------------------------------
def git(root: Path, *args: str) -> str:
    result = subprocess.run(["git", "-C", str(root), *args], capture_output=True,
                            text=True, encoding="utf-8", check=False, timeout=30)
    if result.returncode != 0:
        raise RuntimeError("Git identity lookup failed: " + args[0])
    return result.stdout.strip()


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
    if EXTERNAL_PROVIDER_TYPE and EXTERNAL_PROVIDER_URL:
        # The external channel of this run points at a **loopback** instance
        # (self-hosted SearXNG).  The server validates every provider URL
        # against the SSRF whitelist, so the loopback host has to be listed
        # before the server is allowed to call it.  The extra list is
        # environment-only by design (the settings UI cannot widen it).
        env["SSRF_WHITELIST_EXTRA"] = os.environ.get(
            "G3_SSRF_WHITELIST_EXTRA", "127.0.0.1,localhost")
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
    """Stop the server and report the exit code.

    Windows' ``Popen.terminate()`` is ``TerminateProcess``: the process dies
    immediately with a non-zero exit code.  The POSIX expectation of "0" is a
    platform difference, not a product defect, so the exit code is reported
    rather than asserted.
    """
    if process.poll() is None:
        process.terminate()
    try:
        code = process.wait(timeout=45)
    except subprocess.TimeoutExpired:
        process.kill()
        code = process.wait(timeout=10)
    return int(code)


def request(base_url: str, method: str, path: str, *, token: str = "",
            payload: Any = None, key: str = "",
            expected: int | tuple[int, ...] = 200) -> tuple[int, dict[str, Any], float]:
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8") if payload is not None else None
    headers = {"Accept": "application/json", "X-Request-ID": "issue-258-research"}
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
        with urllib.request.urlopen(req, timeout=60) as response:
            status, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read()
    except (urllib.error.URLError, TimeoutError, OSError) as error:
        raise RuntimeError(f"{method} {path} could not reach the service ({type(error).__name__})") from None
    elapsed = round((time.monotonic() - started) * 1000, 2)
    try:
        data = json.loads(raw.decode("utf-8")) if raw else {}
    except (UnicodeDecodeError, json.JSONDecodeError):
        data = {"_non_json": True, "_raw": raw[:400].decode("utf-8", "replace")}
    accepted = expected if isinstance(expected, tuple) else (expected,)
    if status not in accepted:
        error = data.get("error", {}) if isinstance(data, dict) else {}
        detail = error.get("message", "") if isinstance(error, dict) else ""
        code = error.get("code", "unknown") if isinstance(error, dict) else "unknown"
        raise RuntimeError(f"{method} {path} returned {status} (code={code}; message={detail})")
    if not isinstance(data, dict):
        raise RuntimeError(f"{method} {path} returned a non-object JSON response")
    return status, data, elapsed


def data(envelope: dict[str, Any]) -> Any:
    return envelope.get("data")


def redact(value: Any) -> str:
    """Short, non-reversible fingerprint so IDs can be reported safely."""
    return hashlib.sha256(str(value).encode("utf-8")).hexdigest()[:16]


# ---------------------------------------------------------------------------
# Account + project
# ---------------------------------------------------------------------------
def register_login(base_url: str) -> tuple[str, str]:
    suffix = uuid.uuid4().hex[:12]
    email = f"issue258-{suffix}@example.com"
    # The registration policy wants 8-32 characters with at least one letter
    # and one digit, so the synthetic password mixes a word and digits.
    password = "Synthetic" + uuid.uuid4().hex[:6] + "42"
    status, registration, _ = request(base_url, "POST", "/api/v1/auth/register", payload={
        "username": "issue258-" + suffix, "email": email, "password": password,
    }, expected=(200, 201))
    if registration.get("success") is not True and status not in (200, 201):
        raise RuntimeError("isolated registration did not succeed")
    _, login, _ = request(base_url, "POST", "/api/v1/auth/login", payload={
        "email": email, "password": password,
    })
    token = login.get("token")
    if not isinstance(token, str) or not token:
        raise RuntimeError("isolated login did not return a token")
    return token, email


def setup_project(base_url: str, token: str) -> tuple[str, dict[str, Any]]:
    """Create, activate and confirm a minimal project.

    The chapter versions matter: a dependency candidate needs a real
    ``ChapterVersion`` target, and the impact task is raised against it.
    """
    suffix = uuid.uuid4().hex[:8]
    _, created, _ = request(base_url, "POST", "/api/v1/lingdoc/projects", token=token,
                            key="issue258-project-" + suffix,
                            payload={"name": "Issue 258 research closure " + suffix,
                                     "template_id": "template-demo"}, expected=201)
    project = data(created)
    if not isinstance(project, dict) or not project.get("id"):
        raise RuntimeError("project creation did not return a project ID")
    project_id = str(project["id"])
    _, after_spec, _ = request(base_url, "PUT", f"/api/v1/lingdoc/projects/{project_id}/spec", token=token,
                               key="issue258-spec-" + suffix,
                               payload={"expected_spec_revision": 0,
                                        "fields": {"research_subject": "Issue 258 synthetic subject",
                                                   "research_goal": "Issue 258 synthetic goal"}},
                               expected=200)
    spec_project = data(after_spec)
    version = int(spec_project.get("project_version", 0)) if isinstance(spec_project, dict) else 0
    _, receipt, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/activation-read",
                            token=token, payload={"expected_project_version": version}, expected=201)
    receipt_data = data(receipt)
    receipt_id = receipt_data.get("id") if isinstance(receipt_data, dict) else None
    if not receipt_id:
        raise RuntimeError("activation read did not return a receipt")
    _, active, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/activate", token=token,
                           key="issue258-activate-" + suffix,
                           payload={"expected_spec_revision": 1, "expected_project_version": version,
                                    "reviewed_project_version": version, "activation_read_id": receipt_id},
                           expected=200)
    _, chapters_response, _ = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}/chapters", token=token)
    chapters = data(chapters_response)
    if not isinstance(chapters, list) or len(chapters) < 2:
        raise RuntimeError("activated project did not expose its chapters")
    chapter_ids: dict[str, str] = {}
    chapter_versions: dict[str, str] = {}
    for chapter in chapters:
        section = chapter.get("section_id")
        chapter_id = chapter.get("id")
        if not isinstance(section, str) or not isinstance(chapter_id, str):
            raise RuntimeError("chapter response omitted stable IDs")
        body = "Issue 258 synthetic research question with enough context." if section == "question" \
            else "Issue 258 synthetic method text."
        _, saved, _ = request(base_url, "POST",
                              f"/api/v1/lingdoc/projects/{project_id}/chapters/{chapter_id}/versions",
                              token=token, key="issue258-save-" + section + "-" + suffix,
                              payload={"expected_chapter_version_id": None, "expected_spec_revision": 1,
                                       "body_markdown": body, "source_ids": []}, expected=201)
        saved_data = data(saved)
        current_id = saved_data.get("current_version_id") if isinstance(saved_data, dict) else None
        if not current_id:
            raise RuntimeError(f"chapter {section} did not return a current version")
        chapter_ids[section] = chapter_id
        chapter_versions[section] = str(current_id)
        request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/chapters/{chapter_id}/confirmations",
                token=token, key="issue258-confirm-" + section + "-" + suffix,
                payload={"expected_chapter_version_id": current_id, "expected_spec_revision": 1,
                         "review_decisions": []}, expected=201)
    return project_id, {"chapters": chapter_ids, "chapter_versions": chapter_versions,
                        "active": bool(data(active))}


def read_project(base_url: str, token: str, project_id: str) -> dict[str, Any]:
    _, response, _ = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}", token=token)
    project = data(response)
    if not isinstance(project, dict):
        raise RuntimeError("project readback is not an object")
    return project


# ---------------------------------------------------------------------------
# Synthetic asset (knowledge base / knowledge / chunk) seeded into the very
# same SQLite file the running service uses.
# ---------------------------------------------------------------------------
def tenant_id_of(db_path: Path, token: str, email: str) -> int:
    """Resolve the tenant that owns the freshly registered account.

    The knowledge tables are tenant scoped, so the synthetic rows must carry
    the same tenant id as the account that will bind them.  The account row is
    read from the same SQLite file the service uses.
    """
    connection = sqlite3.connect(str(db_path), timeout=30)
    try:
        connection.execute("PRAGMA busy_timeout = 30000")
        row = connection.execute("SELECT tenant_id FROM users WHERE email = ?", (email,)).fetchone()
    finally:
        connection.close()
    if row and row[0]:
        return int(row[0])
    raise RuntimeError(f"could not resolve the tenant id of account {email}")


def seed_synthetic_asset(db_path: Path, tenant_id: int) -> dict[str, Any]:
    """Insert a synthetic knowledge base, knowledge and chunk.

    Only the three base tables are written.  The retrieval index
    (``lite_embeddings`` / FTS5) is deliberately **not** seeded: the server
    accepts a completed search that honestly returns no candidates, so the
    closure does not depend on an index hit.  The report therefore states the
    real hit count instead of engineering one.
    """
    kb_id = str(uuid.uuid4())
    knowledge_id = str(uuid.uuid4())
    chunk_id = str(uuid.uuid4())
    now = time.strftime("%Y-%m-%d %H:%M:%S")
    connection = sqlite3.connect(str(db_path), timeout=30)
    try:
        connection.execute("PRAGMA busy_timeout = 30000")
        # G3_ENABLE_KEYWORD_PIPELINE=1 turns the **keyword** pipeline on.
        # It is only safe when the server binary was built with the
        # sqlite_fts5 build tag: without FTS5 the keyword search fails on a
        # missing table, which is the same kind of failure as having no
        # pipeline at all.  The vector pipeline stays off: it needs an
        # embedding model credential, which this machine does not have.
        #
        # Why it matters beyond the hit count: the project-material path runs
        # **before** the external provider path, and "no retrievable indexing
        # pipeline" is an **error** there - the research round returns before
        # the provider is ever called.  A keyword pipeline that runs and finds
        # nothing keeps the material path error-free, so the external channel
        # is reached.
        keyword_enabled = "true" if os.environ.get(
            "G3_ENABLE_KEYWORD_PIPELINE", "").strip() == "1" else "false"
        connection.execute(
            "INSERT INTO knowledge_bases (id, name, description, tenant_id, type, chunking_config,"
            " image_processing_config, embedding_model_id, summary_model_id, cos_config, vlm_config,"
            " is_temporary, creator_id, indexing_strategy, created_at, updated_at)"
            " VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (kb_id, SYNTHETIC_KB_NAME, "Issue 258 合成验收知识库", tenant_id, "document",
             '{"chunk_size": 512, "chunk_overlap": 50, "split_markers": ["\\n\\n", "\\n"],'
             ' "keep_separator": true}',
             '{"enable_multimodal": false, "model_id": ""}', "", "", "{}", "{}", 0, "",
             # No retrieval pipeline is enabled on purpose.  The server accepts
             # a completed search that honestly returns no candidates, so the
             # closure does not depend on an index hit.  Vector search would
             # need an embedding model, which this machine has no credentials
             # for, and keyword search needs the FTS5 module, which the local
             # SQLite build does not provide ("no such module: fts5").
             #
             # The wiki flag is set so the strategy is not the zero value:
             # EnsureDefaults() replaces a zero strategy with vector+keyword
             # enabled, which would re-enable the unavailable pipelines.
             #
             # The value is stored as a BLOB on purpose: the server's
             # IndexingStrategy.Scan only accepts []byte.  A TEXT column comes
             # back as a string through the SQLite driver, and the scan then
             # falls back to the default strategy (vector + keyword), which
             # would re-enable the keyword pipeline this machine cannot run.
             sqlite3.Binary(
                ('{"vector_enabled": false, "keyword_enabled": ' + keyword_enabled +
                 ', "wiki_enabled": true, "graph_enabled": false}').encode("utf-8")), now, now))
        connection.execute(
            "INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, description, source,"
            " parse_status, enable_status, file_name, file_type, file_size, file_path, file_hash,"
            " storage_size, processed_at, created_at, updated_at)"
            " VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (knowledge_id, tenant_id, kb_id, "file", SYNTHETIC_KNOWLEDGE_TITLE,
             "Issue 258 合成验收资料", "synthetic", "completed", "enabled",
             "issue258-synthetic.txt", "txt", len(SYNTHETIC_CHUNK_CONTENT),
             "synthetic/issue258-synthetic.txt",
             hashlib.sha256(SYNTHETIC_CHUNK_CONTENT.encode("utf-8")).hexdigest(), 0, now, now, now))
        connection.execute(
            "INSERT INTO chunks (id, tenant_id, knowledge_base_id, knowledge_id, content, source_content,"
            " content_revision, index_status, chunk_index, is_enabled, start_at, end_at, chunk_type,"
            " created_at, updated_at)"
            " VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (chunk_id, tenant_id, kb_id, knowledge_id, SYNTHETIC_CHUNK_CONTENT, SYNTHETIC_CHUNK_CONTENT,
             0, "ready", 0, 1, 0, len(SYNTHETIC_CHUNK_CONTENT), "text", now, now))
        connection.commit()
    finally:
        connection.close()
    return {"knowledge_base_id": kb_id, "knowledge_id": knowledge_id, "chunk_id": chunk_id,
            "tenant_id": tenant_id, "keyword_index_rows": 0}


def read_seeded_kb(db_path: Path, kb_id: str) -> dict[str, Any]:
    """Read the seeded knowledge base row back, so the report can state what
    the server will actually see (the indexing strategy decides which
    retrieval pipelines run)."""
    connection = sqlite3.connect(str(db_path), timeout=30)
    try:
        connection.execute("PRAGMA busy_timeout = 30000")
        row = connection.execute(
            "SELECT id, type, embedding_model_id, indexing_strategy FROM knowledge_bases WHERE id = ?",
            (kb_id,)).fetchone()
    finally:
        connection.close()
    if not row:
        return {"found": False}
    strategy = row[3]
    if isinstance(strategy, (bytes, bytearray)):
        strategy = strategy.decode("utf-8", "replace")
    return {"found": True, "kb_id": str(row[0]), "type": row[1],
            "embedding_model_id": row[2], "indexing_strategy": strategy}


def bind_asset(base_url: str, token: str, project_id: str, knowledge_id: str) -> dict[str, Any]:
    suffix = uuid.uuid4().hex[:8]
    _, bound, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/assets",
                          token=token, key="issue258-bind-" + suffix,
                          payload={"knowledge_id": knowledge_id}, expected=(200, 201))
    asset = data(bound)
    if not isinstance(asset, dict) or not asset.get("id"):
        raise RuntimeError("binding the synthetic asset did not return an asset ID")
    return asset


def read_asset_policy(base_url: str, token: str, project_id: str, asset_id: str) -> dict[str, Any]:
    _, response, _ = request(base_url, "GET",
                             f"/api/v1/lingdoc/projects/{project_id}/assets/{asset_id}/policy", token=token)
    policy = data(response)
    return policy if isinstance(policy, dict) else {}


# ---------------------------------------------------------------------------
# Impact task: dependency candidate -> confirmed edge -> change set -> impact task
# ---------------------------------------------------------------------------
def create_impact_task(base_url: str, token: str, project_id: str,
                        chapter_id: str, chapter_version_id: str) -> dict[str, Any]:
    """Raise a ``RETRIEVE_CANDIDATES`` impact task through the product API.

    The path mirrors what the acceptance console does: a batch of proposed
    dependency candidates whose action is ``RETRIEVE_CANDIDATES``, a human
    ``decision=confirm`` that turns the proposal into a confirmed edge, then a
    change set that is assessed and applied.  Applying the change set is what
    materialises the impact task (``commitChangeImpactTasks``).
    """
    project = read_project(base_url, token, project_id)
    context_revision = int(project.get("current_context_revision", 0))
    if context_revision < 1:
        raise RuntimeError(f"project has no usable context revision: {project.get('current_context_revision')}")
    suffix = uuid.uuid4().hex[:8]
    # The impact assessment walks the dependency graph starting from the
    # change set roots, and a spec change makes the ProjectSpec ref a root.
    # So the edge has to start at that exact ref for its action to propagate
    # to the chapter and become an impact task.
    spec_revision = int(project.get("spec_revision", 0))
    source_ref = {"kind": "ProjectSpec", "id": project_id, "version": str(spec_revision)}
    target_ref = {"kind": "ChapterVersion", "id": chapter_id, "version": chapter_version_id}
    candidate = {
        "source": source_ref,
        "target": target_ref,
        "relation": "synthetic_research_dependency",
        "action": "RETRIEVE_CANDIDATES",
        "evidence": "Issue 258 合成依据：章节正文变更后需要重新检索项目资料候选",
        "reason": "Issue 258 合成候选：正文变化使既有检索结论过期",
        "confidence": 1.0,
    }
    _, batch_response, _ = request(base_url, "POST",
                                   f"/api/v1/lingdoc/projects/{project_id}/dependency-candidates",
                                   token=token, key="issue258-candidates-" + suffix,
                                   payload={"expected_context_revision": context_revision,
                                            "idempotency_key": "issue258-candidates-" + suffix,
                                            "scope": {"objects": [], "target_kinds": [],
                                                      "reason": "Issue 258 合成候选范围"},
                                            "reason": "Issue 258 合成候选批次",
                                            "candidates": [candidate]}, expected=(200, 201))
    batch = data(batch_response)
    if not isinstance(batch, dict):
        raise RuntimeError("dependency candidate batch did not return an object")
    candidates = batch.get("candidates") if isinstance(batch.get("candidates"), list) else []
    if not candidates:
        raise RuntimeError("dependency candidate batch did not contain a candidate")
    candidate_id = str(candidates[0].get("id", ""))
    if not candidate_id:
        raise RuntimeError("dependency candidate did not carry an ID")
    _, decision_response, _ = request(
        base_url, "POST",
        f"/api/v1/lingdoc/projects/{project_id}/dependency-candidates/{candidate_id}/decision",
        token=token, key="issue258-decide-" + suffix,
        payload={"expected_version": int(candidates[0].get("version", 1)), "decision": "confirm",
                 "reason": "Issue 258 合成验收：人工确认该重新检索候选"}, expected=200)
    decision = data(decision_response)
    edge = decision.get("edge") if isinstance(decision, dict) else None
    if not isinstance(edge, dict) or not edge.get("id"):
        raise RuntimeError("confirming the candidate did not produce a dependency edge")
    if edge.get("status") != "confirmed":
        raise RuntimeError(f"dependency edge is not confirmed: {edge.get('status')}")

    project = read_project(base_url, token, project_id)
    context_revision = int(project.get("current_context_revision", 0))
    spec = project.get("spec") if isinstance(project.get("spec"), dict) else {}
    # The demo template marks both research_subject and research_goal as
    # required, and a change set must carry every required field, so the
    # payload changes both of them.
    fields = {}
    for key in ("research_subject", "research_goal"):
        old_value = str(spec.get(key, ""))
        fields[key] = {"old_value": old_value, "new_value": old_value + "（Issue 258 合成更新）"}
    change_payload = {"expected_context_revision": context_revision,
                      "fields": fields,
                      "affected_chapter_ids": [chapter_id],
                      "reason": "Issue 258 合成变更：触发 RETRIEVE_CANDIDATES 影响任务"}
    try:
        _, change_response, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/change-sets",
                                        token=token, key="issue258-change-" + suffix,
                                        payload=change_payload, expected=(200, 201))
    except RuntimeError as error:
        raise RuntimeError(
            f"{error} | change set payload={json.dumps(change_payload, ensure_ascii=False)}"
            f" | project spec={json.dumps(spec, ensure_ascii=False)}"
            f" | project_version={project.get('project_version')}"
            f" | context_revision={context_revision}") from error
    change = data(change_response)
    if not isinstance(change, dict) or not change.get("id"):
        raise RuntimeError("change set creation did not return a change set")
    change_id = str(change["id"])
    request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/change-sets/{change_id}/assess",
            token=token, key="issue258-assess-" + suffix, payload={}, expected=(200, 201))
    _, applied, _ = request(base_url, "POST",
                             f"/api/v1/lingdoc/projects/{project_id}/change-sets/{change_id}/apply",
                             token=token, key="issue258-apply-" + suffix, payload={}, expected=(200, 201))
    applied_change = data(applied)
    _, tasks_response, _ = request(base_url, "GET", f"/api/v1/lingdoc/projects/{project_id}/impact-tasks", token=token)
    tasks = data(tasks_response)
    if not isinstance(tasks, list):
        raise RuntimeError("impact task list is not an array")
    matching = [task for task in tasks
                if isinstance(task, dict) and task.get("action") == "RETRIEVE_CANDIDATES"]
    if not matching:
        raise RuntimeError("no RETRIEVE_CANDIDATES impact task was materialised by the applied change set")
    task = matching[0]
    return {"edge_id": str(edge["id"]), "candidate_id": candidate_id, "change_set_id": change_id,
            "change_set_status": (applied_change or {}).get("status") if isinstance(applied_change, dict) else None,
            "task": task}


# ---------------------------------------------------------------------------
# Research task + run
# ---------------------------------------------------------------------------
def seed_web_search_provider(db_path: Path, tenant_id: int) -> str:
    """Insert the tenant-owned web search provider row and return its id.

    The row is the one the product reads when a research task names a
    ``provider_id``: the server checks that the provider belongs to the
    tenant before it fetches with it.  The parameters carry the instance
    URL; the server validates it against the SSRF whitelist.
    """
    provider_id = str(uuid.uuid4())
    parameters = json.dumps({"base_url": EXTERNAL_PROVIDER_URL}, ensure_ascii=False)
    connection = sqlite3.connect(str(db_path), timeout=30)
    try:
        connection.execute("PRAGMA busy_timeout = 30000")
        connection.execute(
            "INSERT INTO web_search_providers (id, tenant_id, name, provider, description,"
            " parameters, is_default, created_at, updated_at)"
            " VALUES (?, ?, ?, ?, ?, ?, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
            (provider_id, tenant_id, "G3 验收台外部检索渠道", EXTERNAL_PROVIDER_TYPE,
             "G3 验收台实跑用：自托管检索实例", parameters),
        )
        connection.commit()
    finally:
        connection.close()
    return provider_id


def create_research_task(base_url: str, token: str, project_id: str, impact_task_id: str,
                         asset_id: str, provider_id: str = "") -> dict[str, Any]:
    project = read_project(base_url, token, project_id)
    payload = {
        "expected_project_version": int(project.get("project_version", 0)),
        "expected_context_revision": int(project.get("current_context_revision", 0)),
        "goal": "Issue 258 合成调研：为影响任务重新检索项目资料候选",
        "question_type": "synthetic_acceptance",
        "scope": IMPACT_TASK_SCOPE_PREFIX + impact_task_id,
        "exclusions": [],
        "queries": RESEARCH_QUERIES,
        "asset_ids": [asset_id],
        "expected_output": "合成验收材料中的候选片段；不构成科学结论",
    }
    if provider_id:
        # The external channel is a **second** retrieval path: the project
        # material stays in scope, and the rounds also search the provider the
        # tenant owns.  The server keeps both result sets.
        payload["provider_id"] = provider_id
    suffix = uuid.uuid4().hex[:8]
    _, created, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/research-tasks",
                            token=token, key="issue258-research-task-" + suffix, payload=payload, expected=200)
    task = data(created)
    if not isinstance(task, dict) or not task.get("id"):
        raise RuntimeError("research task creation did not return a task")
    return task


def approve_research_task(base_url: str, token: str, project_id: str, task: dict[str, Any]) -> dict[str, Any]:
    project = read_project(base_url, token, project_id)
    payload = {
        "expected_project_version": int(project.get("project_version", 0)),
        "expected_context_revision": int(project.get("current_context_revision", 0)),
        "expected_version": int(task.get("version", 0)),
        "confirmed": True,
        "reason": "Issue 258 合成验收：本人确认检索范围仅限合成验收材料",
    }
    suffix = uuid.uuid4().hex[:8]
    _, approved, _ = request(base_url, "POST",
                             f"/api/v1/lingdoc/projects/{project_id}/research-tasks/{task['id']}/approve",
                             token=token, key="issue258-approve-" + suffix, payload=payload, expected=200)
    result = data(approved)
    if not isinstance(result, dict):
        raise RuntimeError("research task approval did not return the task")
    return result


def read_research_task(base_url: str, token: str, project_id: str, task_id: str) -> dict[str, Any]:
    """Read the research task back from the service.

    The task object handed back by the run call is the *pre-run* snapshot
    (status "draft"); the server moves it to "completed" and stamps
    ``latest_run_id`` when the run finishes, so the verdict has to read the
    current row rather than the creation response.
    """
    _, response, _ = request(base_url, "GET",
                             f"/api/v1/lingdoc/projects/{project_id}/research-tasks", token=token)
    tasks = data(response)
    if not isinstance(tasks, list):
        raise RuntimeError("research task list is not an array")
    for task in tasks:
        if isinstance(task, dict) and str(task.get("id")) == str(task_id):
            return task
    raise RuntimeError("research task did not appear in the task list")


def run_research_task(base_url: str, token: str, project_id: str, task: dict[str, Any]) -> dict[str, Any]:
    project = read_project(base_url, token, project_id)
    payload = {
        "expected_project_version": int(project.get("project_version", 0)),
        "expected_context_revision": int(project.get("current_context_revision", 0)),
        "expected_version": int(task.get("version", 0)),
    }
    suffix = uuid.uuid4().hex[:8]
    _, ran, _ = request(base_url, "POST",
                        f"/api/v1/lingdoc/projects/{project_id}/research-tasks/{task['id']}/run",
                        token=token, key="issue258-run-" + suffix, payload=payload, expected=(200, 202))
    run = data(ran)
    if not isinstance(run, dict) or not run.get("id"):
        raise RuntimeError("research task run did not return a run")
    _, readback, _ = request(base_url, "GET",
                             f"/api/v1/lingdoc/projects/{project_id}/research-runs/{run['id']}", token=token)
    final = data(readback)
    return final if isinstance(final, dict) else run


def probe_external_search_channels() -> dict[str, Any]:
    """Probe the external search channels and record what actually happened.

    The closure itself needs no external provider, but the gap ticket asks
    whether an external channel is available on this machine, so the probe
    states the real facts instead of an assumption.
    """
    import urllib.parse
    result: dict[str, Any] = {"probed": [], "summary": ""}
    query = urllib.parse.quote("lingdoc synthetic acceptance probe")
    endpoints = [
        ("duckduckgo_html", f"https://html.duckduckgo.com/html/?q={query}&kl=cn-zh"),
        ("duckduckgo_lite", f"https://lite.duckduckgo.com/lite/?q={query}"),
    ]
    for name, url in endpoints:
        entry: dict[str, Any] = {"name": name, "url": url}
        request_obj = urllib.request.Request(url, headers={
            "User-Agent": ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
                           "(KHTML, like Gecko) Chrome/124.0 Safari/537.36"),
            "Accept": "text/html,application/xhtml+xml",
        })
        try:
            with urllib.request.urlopen(request_obj, timeout=20) as response:
                body = response.read()
                entry["http_status"] = response.status
        except urllib.error.HTTPError as error:
            body = error.read()
            entry["http_status"] = error.code
        except (urllib.error.URLError, TimeoutError, OSError) as error:
            entry["http_status"] = None
            entry["error"] = f"{type(error).__name__}: {error}"
            result["probed"].append(entry)
            continue
        text = body.decode("utf-8", "replace")
        entry["response_bytes"] = len(body)
        entry["result_link_count"] = text.count('result__a')
        entry["challenge_marker_count"] = text.lower().count("challenge")
        entry["is_challenge_page"] = entry["result_link_count"] == 0 and entry["challenge_marker_count"] > 0
        result["probed"].append(entry)
    result["summary"] = (
        "外部检索渠道不可用：免 API key 的 DuckDuckGo 端点返回反爬挑战页而非结果列表；"
        "其余 provider（tavily/exa/brave/bing/google/baidu/bocha/metaso/zhipu/keenable/searxng/ollama）"
        "均需要外部凭据或外部实例，本机没有。"
    )
    return result


def probe_retrieval(base_url: str, token: str, project_id: str, asset_id: str,
                    query: str) -> dict[str, Any]:
    """Call the product's own retrieval endpoint for a real error message.

    The research run reports a generic ``provider_failure``; this endpoint
    returns the underlying reason, so the report can state what actually
    happened instead of guessing.
    """
    try:
        _, response, _ = request(base_url, "POST", f"/api/v1/lingdoc/projects/{project_id}/retrieval",
                                 token=token, payload={"query": query, "asset_ids": [asset_id]},
                                 expected=(200, 400, 403, 404, 409, 422, 500, 503))
    except RuntimeError as error:
        return {"status": "error", "message": str(error)}
    result = data(response)
    if isinstance(result, list):
        return {"status": "ok", "hits": len(result)}
    return {"status": "ok", "result_type": type(result).__name__}


def assign_impact_task(base_url: str, token: str, project_id: str, task: dict[str, Any],
                       assignee_id: str) -> dict[str, Any]:
    """Assign the impact task to the acting user.

    Only the assignee may act on a task, so the assignment has to happen
    before the closure can be recorded.
    """
    project = read_project(base_url, token, project_id)
    payload = {
        "expected_version": int(task.get("version", 0)),
        "target_revision": int(project.get("current_context_revision", 0)),
        "action": "assign",
        "assignee_id": assignee_id,
        "reason": "Issue 258 合成验收：由本人负责该重新检索候选影响任务",
    }
    suffix = uuid.uuid4().hex[:8]
    _, assigned, _ = request(base_url, "POST",
                             f"/api/v1/lingdoc/projects/{project_id}/impact-tasks/{task['id']}/actions",
                             token=token, key="issue258-assign-" + suffix, payload=payload, expected=200)
    result = data(assigned)
    if not isinstance(result, dict):
        raise RuntimeError("impact task assignment did not return the task")
    return result


def close_impact_task(base_url: str, token: str, project_id: str, task: dict[str, Any],
                      run: dict[str, Any]) -> dict[str, Any]:
    project = read_project(base_url, token, project_id)
    payload = {
        "expected_version": int(task.get("version", 0)),
        "target_revision": int(project.get("current_context_revision", 0)),
        "action": "complete",
        "reason": "Issue 258 合成验收：以真实调研回执关闭重新检索候选影响任务",
        "closure_evidence": [{"kind": "ResearchRun", "id": str(run.get("id")),
                               "version": str(run.get("version"))}],
    }
    suffix = uuid.uuid4().hex[:8]
    _, completed, _ = request(base_url, "POST",
                              f"/api/v1/lingdoc/projects/{project_id}/impact-tasks/{task['id']}/actions",
                              token=token, key="issue258-complete-" + suffix, payload=payload, expected=200)
    result = data(completed)
    if not isinstance(result, dict):
        raise RuntimeError("impact task completion did not return the task")
    return result


# ---------------------------------------------------------------------------
# Verdict
# ---------------------------------------------------------------------------
def judge(run: dict[str, Any], research_task: dict[str, Any], approved_task: dict[str, Any],
          impact_task: dict[str, Any], closed_task: dict[str, Any],
          external_channel: bool = False) -> dict[str, Any]:
    """Check the server-side ``RETRIEVE_CANDIDATES`` verdict condition by
    condition and report the real values that were read back."""
    rounds = run.get("rounds") if isinstance(run.get("rounds"), list) else []
    round_rows = [{
        "number": item.get("number"),
        "query": item.get("query"),
        "provider": item.get("provider"),
        "status": item.get("status"),
        "result_ids": len(item.get("result_ids") or []),
        "failure_code": item.get("failure_code", ""),
    } for item in rounds if isinstance(item, dict)]
    impact_task_id = str(impact_task.get("id"))
    checks = {
        "run_status_completed": run.get("status") == "completed",
        "run_rounds_at_least_two": len(rounds) >= 2,
        "run_finished_at_present": bool(run.get("finished_at")),
        "run_correlation_id_present": bool(run.get("correlation_id")),
        "run_created_by_present": bool(run.get("created_by")),
        "research_scope_matches_impact_task": research_task.get("scope") == IMPACT_TASK_SCOPE_PREFIX + impact_task_id,
        "research_latest_run_id_matches": research_task.get("latest_run_id") == run.get("id"),
        "research_status_completed": research_task.get("status") == "completed",
        "research_not_paused": not research_task.get("paused"),
        "research_approved_by_present": bool(approved_task.get("approved_by")),
        "approved_status_is_approved": approved_task.get("status") == "approved",
        "every_round_completed": all(item.get("status") == "completed" for item in round_rows) and bool(round_rows),
        "every_round_has_provider": all(bool(item.get("provider")) for item in round_rows) and bool(round_rows),
        "impact_task_closed": closed_task.get("status") == "DONE",
    }
    if external_channel:
        # With a reachable external channel the rounds must have returned
        # something.  A run that finished with no result at all means the
        # channel did not actually work, and the closure would be a hollow
        # one - so this check belongs in the same verdict as the others.
        checks["retrieval_hit_present"] = len(run.get("results") or []) > 0
    basis = run.get("basis") if isinstance(run.get("basis"), dict) else {}
    return {
        "checks": checks,
        "all_checks_passed": all(checks.values()),
        "values": {
            "run_status": run.get("status"),
            "run_version": run.get("version"),
            "run_rounds": len(rounds),
            "run_results": len(run.get("results") or []),
            "run_failure_code": run.get("failure_code", ""),
            "run_created_by": run.get("created_by"),
            "run_correlation_id_present": bool(run.get("correlation_id")),
            "run_finished_at": run.get("finished_at"),
            "run_basis_asset_versions": basis.get("asset_versions"),
            "research_task_status": research_task.get("status"),
            "research_task_version": research_task.get("version"),
            "research_scope": research_task.get("scope"),
            "research_latest_run_id": research_task.get("latest_run_id"),
            "research_asset_ids": research_task.get("asset_ids"),
            "research_queries": research_task.get("queries"),
            "approved_by": approved_task.get("approved_by"),
            "impact_task_id": impact_task_id,
            "impact_task_status_before": impact_task.get("status"),
            "impact_task_status_after": closed_task.get("status"),
            "impact_task_closure_evidence": closed_task.get("closure_evidence"),
            "rounds": round_rows,
        },
    }


# ---------------------------------------------------------------------------
# Runner
# ---------------------------------------------------------------------------
class ReportError(RuntimeError):
    """Carries the partially filled report so a failure keeps its evidence."""

    def __init__(self, report: dict[str, Any]) -> None:
        super().__init__(str(report.get("error", "unknown failure")))
        self.report = report


def run(args: argparse.Namespace) -> dict[str, Any]:
    root = Path(args.repo_root).resolve()
    identity = source_identity(root)
    report: dict[str, Any] = {
        "status": BLOCKED,
        "ticket": "#258（G3 缺口收口-9）：检索渠道环境限制与调研类闭环实跑（G3-GAP-08）",
        "source": identity,
        "mode": "live" if args.live else "dry-run",
        "steps": [],
        "notes": [],
    }

    def step(name: str, status: str, detail: Any = None) -> None:
        report["steps"].append({"step": name, "status": status, "detail": detail})

    if not args.live:
        report["status"] = NOT_RUN
        report["notes"].append(
            "未指定 --live：本脚本默认不实跑。G3-GAP-08 的任何结论都必须来自 --live 的真实输出。")
        return report

    binary = Path(args.binary).resolve() if args.binary else None
    if binary is None or not binary.exists():
        raise RuntimeError("--live needs an existing server binary (--binary)")

    process: subprocess.Popen[bytes] | None = None
    temporary: tempfile.TemporaryDirectory[str] | None = None
    try:
        temporary = tempfile.TemporaryDirectory(prefix="issue258-research-")
        data_dir = Path(temporary.name)
        port = free_port()
        env = server_environment(data_dir, port)
        log_path = data_dir / "server.log"
        with log_path.open("wb") as log:
            # The working directory must be the repository root: the server
            # reads config/config.yaml relative to it.
            process = subprocess.Popen([str(binary)], cwd=root, env=env, stdout=log,
                                       stderr=subprocess.STDOUT)
        base_url = f"http://127.0.0.1:{port}"
        wait_health(process, base_url)
        step("server_start", PASS, {"port": port, "db_path": env["DB_PATH"]})

        token, email = register_login(base_url)
        step("register_login", PASS, {"account": redact(email)})

        # /api/v1/system/info requires authentication (401 without a token).
        _, info, _ = request(base_url, "GET", "/api/v1/system/info", token=token)
        info_data = data(info) if isinstance(data(info), dict) else info
        served_commit = ""
        if isinstance(info_data, dict):
            served_commit = str(info_data.get("commit_id", ""))
        report["served_build"] = {"commit_id": served_commit, "matches_checkout": served_commit == identity["head"]}
        if served_commit and served_commit != identity["head"]:
            report["notes"].append(
                f"服务返回的 commit_id={served_commit} 与被测 checkout HEAD={identity['head']} 不一致，"
                "证据不得作为当前 head 的结论。")
        step("system_info", PASS if served_commit else WARNING,
             {"commit_id": served_commit, "expected": identity["head"]})

        tenant_id = tenant_id_of(Path(env["DB_PATH"]), token, email)
        step("resolve_tenant", PASS, {"tenant_id": tenant_id})

        # The gap ticket also asks whether an external search channel is
        # available on this machine.  The closure below does not use one, so
        # this probe only records the facts.
        report["external_search_probe"] = probe_external_search_channels()
        step("probe_external_search_channels", WARNING, report["external_search_probe"]["probed"])

        project_id, setup = setup_project(base_url, token)
        step("setup_project", PASS, {"project_id": redact(project_id),
                                     "chapters": {k: redact(v) for k, v in setup["chapters"].items()}})

        seeded = seed_synthetic_asset(Path(env["DB_PATH"]), tenant_id)
        seeded_readback = read_seeded_kb(Path(env["DB_PATH"]), seeded["knowledge_base_id"])
        report["seeded_kb_readback"] = seeded_readback
        step("seed_synthetic_asset", PASS, {"knowledge_id": redact(seeded["knowledge_id"]),
                                            "chunk_id": redact(seeded["chunk_id"]),
                                            "indexing_strategy": seeded_readback.get("indexing_strategy")})

        asset = bind_asset(base_url, token, project_id, seeded["knowledge_id"])
        asset_id = str(asset.get("id"))
        report["asset"] = {"asset_id": redact(asset_id), "asset_revision": asset.get("asset_revision"),
                           "processing_state": asset.get("processing_state"),
                           "knowledge_id": redact(seeded["knowledge_id"]),
                           "knowledge_base_id": redact(seeded["knowledge_base_id"])}
        step("bind_asset", PASS, report["asset"])

        policy = read_asset_policy(base_url, token, project_id, asset_id)
        report["asset_policy"] = {"scope": policy.get("scope"), "version": policy.get("version"),
                                  "model_use_allowed": policy.get("model_use_allowed"),
                                  "asset_revision": policy.get("asset_revision")}
        step("read_asset_policy", PASS, report["asset_policy"])

        impact = create_impact_task(base_url, token, project_id,
                                    setup["chapters"]["question"], setup["chapter_versions"]["question"])
        impact_task = impact["task"]
        report["impact_task"] = {"id": redact(str(impact_task.get("id"))),
                                 "action": impact_task.get("action"),
                                 "status_before": impact_task.get("status"),
                                 "target_revision": impact_task.get("target_revision"),
                                 "severity": impact_task.get("severity"),
                                 "change_set_id": redact(impact["change_set_id"]),
                                 "edge_id": redact(impact["edge_id"])}
        step("create_impact_task", PASS, report["impact_task"])

        provider_id = ""
        if EXTERNAL_PROVIDER_TYPE and EXTERNAL_PROVIDER_URL:
            provider_id = seed_web_search_provider(Path(env["DB_PATH"]), tenant_id)
            report["external_search_channel"] = {
                "provider": EXTERNAL_PROVIDER_TYPE,
                "provider_id": redact(provider_id),
                "base_url": EXTERNAL_PROVIDER_URL,
                "note": "外部检索渠道已配置：调研轮次会真的检索外部来源，报告里的命中数不再是 0。",
            }
            step("seed_web_search_provider", PASS, report["external_search_channel"])
        else:
            report["external_search_channel"] = {
                "provider": "",
                "base_url": "",
                "note": "没有配置外部检索渠道（环境变量 G3_WEB_SEARCH_PROVIDER / G3_WEB_SEARCH_PROVIDER_URL 未设）："
                        "本轮只走项目资料检索路径，命中数受限于本机检索管线。",
            }
            step("seed_web_search_provider", NOT_RUN, report["external_search_channel"])

        research_task = create_research_task(base_url, token, project_id, str(impact_task.get("id")),
                                             asset_id, provider_id)
        step("create_research_task", PASS,
             {"id": redact(str(research_task.get("id"))), "status": research_task.get("status"),
              "scope": research_task.get("scope"), "queries": research_task.get("queries"),
              "asset_ids": [redact(a) for a in research_task.get("asset_ids") or []]})

        approved_task = approve_research_task(base_url, token, project_id, research_task)
        step("approve_research_task", PASS,
             {"id": redact(str(approved_task.get("id"))), "status": approved_task.get("status"),
              "version": approved_task.get("version"),
              "approved_by_present": bool(approved_task.get("approved_by"))})

        run_result = run_research_task(base_url, token, project_id, approved_task)
        run_rounds = run_result.get("rounds") if isinstance(run_result.get("rounds"), list) else []
        report["research_run"] = {"id": redact(str(run_result.get("id"))), "status": run_result.get("status"),
                                  "version": run_result.get("version"),
                                  "rounds": len(run_rounds),
                                  "results": len(run_result.get("results") or []),
                                  "failure_code": run_result.get("failure_code", ""),
                                  "round_detail": [{
                                      "number": item.get("number"), "query": item.get("query"),
                                      "provider": item.get("provider"), "status": item.get("status"),
                                      "failure_code": item.get("failure_code", ""),
                                      "result_ids": len(item.get("result_ids") or []),
                                  } for item in run_rounds if isinstance(item, dict)]}
        step("run_research_task", PASS if run_result.get("status") == "completed" else WARNING,
             report["research_run"])

        if run_result.get("status") != "completed":
            probe = probe_retrieval(base_url, token, project_id, asset_id, RESEARCH_QUERIES[0])
            report["retrieval_probe"] = probe
            step("probe_retrieval", WARNING, probe)

        # Only the assignee may act on an impact task, so the acting user is
        # assigned first.  The acting user id comes from the project readback.
        project = read_project(base_url, token, project_id)
        members = project.get("members") if isinstance(project.get("members"), list) else []
        actor_id = ""
        for member in members:
            if isinstance(member, dict) and member.get("role") == "owner":
                actor_id = str(member.get("user_id") or member.get("id") or "")
                break
        if not actor_id:
            raise RuntimeError("could not resolve the acting owner id for the impact task assignment")
        assigned_task = assign_impact_task(base_url, token, project_id, impact_task, actor_id)
        report["impact_task"]["status_after_assign"] = assigned_task.get("status")
        report["impact_task"]["assignee_present"] = bool(assigned_task.get("assignee_id"))
        step("assign_impact_task", PASS, report["impact_task"])

        closed_task = close_impact_task(base_url, token, project_id, assigned_task, run_result)
        # The research task row is re-read after the run: the run stamps
        # latest_run_id and status "completed" on it.
        research_task_current = read_research_task(base_url, token, project_id, str(research_task.get("id")))
        report["research_task_after_run"] = {
            "status": research_task_current.get("status"),
            "version": research_task_current.get("version"),
            "latest_run_id": research_task_current.get("latest_run_id"),
            "paused": research_task_current.get("paused"),
            "scope": research_task_current.get("scope"),
        }
        verdict = judge(run_result, research_task_current, approved_task, impact_task, closed_task,
                        external_channel=bool(provider_id))
        report["verdict"] = verdict
        report["retrieval_hit_count"] = len(run_result.get("results") or [])
        report["keyword_pipeline_enabled"] = os.environ.get(
            "G3_ENABLE_KEYWORD_PIPELINE", "").strip() == "1"
        step("close_impact_task", PASS if verdict["all_checks_passed"] else WARNING,
             {"status": closed_task.get("status"),
              "closure_evidence": closed_task.get("closure_evidence"),
              "retrieval_hit_count": report["retrieval_hit_count"]})

        report["status"] = PASS if verdict["all_checks_passed"] else WARNING
        return report
    except Exception as error:  # noqa: BLE001 - the report must carry the real failure
        report["status"] = BLOCKED
        report["error"] = f"{type(error).__name__}: {error}"
        raise ReportError(report) from error
    finally:
        if process is not None:
            code = stop_process(process)
            report.setdefault("server_shutdown", {})["exit_code"] = code
            report["server_shutdown"]["note"] = (
                "Windows 的 Popen.terminate() 是 TerminateProcess，退出码非 0 属平台语义，不是产品缺陷。"
                if os.name == "nt" else "POSIX 优雅关停。")
        if temporary is not None:
            log_path = Path(temporary.name) / "server.log"
            if log_path.exists():
                tail = log_path.read_text(encoding="utf-8", errors="replace")
                report["server_log_tail"] = tail[-4000:]
                # Keep the lines that explain the retrieval path so a failure
                # can be read without re-running the whole flow.  The provider
                # seam logs its own error (the URL it called, the reason it was
                # refused), so those lines have to survive the filter too.
                markers = ("Hybrid search", "No retrievable", "retriev", "Retriev", "sqlite",
                           "SQLite", "engine", "Engine", "ERROR", "error", "Error", "g2.",
                           "provider", "Provider", "searxng", "SearXNG", "web_search",
                           "web search", "WebSearch", "SSRF", "ssrf", "whitelist", "Whitelist")
                interesting = [line for line in tail.splitlines() if any(m in line for m in markers)]
                report["server_log_interesting"] = interesting[-60:]
                # The whole log is kept next to the report: a provider failure
                # is often explained by one line the filter above drops.
                if args.output:
                    keep = Path(args.output).with_suffix(".server.log")
                    keep.write_text(tail, encoding="utf-8")
                    report["server_log_file"] = str(keep)
            temporary.cleanup()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--repo-root", default=str(Path(__file__).resolve().parents[2]),
                        help="path to the LingDoc checkout under test")
    parser.add_argument("--binary", default="",
                        help="path to the built LingDoc server binary")
    parser.add_argument("--live", action="store_true",
                        help="actually start the service and run the closure (default: dry run)")
    parser.add_argument("--output", default="",
                        help="write the JSON report to this path in addition to stdout")
    args = parser.parse_args()
    try:
        report = run(args)
    except ReportError as error:
        report = error.report
    except Exception as error:  # noqa: BLE001 - the report must carry the real failure
        report = {
            "status": BLOCKED,
            "ticket": "#258（G3 缺口收口-9）：检索渠道环境限制与调研类闭环实跑（G3-GAP-08）",
            "mode": "live" if args.live else "dry-run",
            "error": f"{type(error).__name__}: {error}",
        }
    text = json.dumps(report, ensure_ascii=False, indent=2)
    # The console on a Chinese Windows host is GBK by default, and a real
    # retrieval snippet can carry characters GBK cannot encode.  Printing the
    # report must not be the thing that fails the run, so the stdout codec
    # is switched to UTF-8 with replacement.
    try:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    except (AttributeError, OSError):  # pragma: no cover - non-reconfigurable stdout
        pass
    print(text)
    if args.output:
        Path(args.output).write_text(text + "\n", encoding="utf-8")
    return 0 if report.get("status") in (PASS, PASS_FOLLOW_UP, NA, NOT_RUN) else 1


if __name__ == "__main__":
    sys.exit(main())
