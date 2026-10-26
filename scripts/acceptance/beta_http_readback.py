#!/usr/bin/env python3
"""HTTP-side executor of the 54-Spec dual runner (real browser x real backend).

What this file is
-----------------
54-Spec compares three observations of the *same* project:

* what the browser showed,
* what the HTTP API returned,
* what the browser shows again after a page reload.

This file is the HTTP side.  It drives a **real** LingDoc backend over HTTP
(register -> login -> create -> activate -> author -> confirm -> check ->
freeze -> export) and writes a snapshot whose keys are exactly the keys the
browser side reads out of the rendered page.

Why a separate executable
--------------------------
Implementation Decision 1 of 54-Spec keeps G4's shape: two runs plus a
post-hoc comparison, not a same-process dual run.  Two independent
observers are what makes the comparison worth something; one process
asserting itself is worth nothing.

Evidence boundary (Implementation Decision 8)
--------------------------------------------
No token, no password, no request body and no response body reach the
snapshot -- only identifiers, counters and status words.  The identity
hand-off file (``--context-out``) is **not** evidence and is written outside
the evidence directory by the orchestrator.

What this file never does
--------------------------
* It never runs a real model (Decision 9).
* It never claims a pass for something it did not run.  Whatever it could
  not read lands in ``not_run`` with a machine-readable unblock condition.

Usage
-----
    python scripts/acceptance/beta_http_readback.py --base-url <API ROOT> --out <http.json>
    python scripts/acceptance/beta_http_readback.py --list-items
"""
from __future__ import annotations

import argparse
import json
import os
import secrets
import sys
import urllib.error
import urllib.request
import uuid
from urllib.parse import urlsplit
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

# --------------------------------------------------------------------------
# The base URL: one shape, taken from the contract.
#
# The OpenAPI contract says where the LingDoc API lives
# (``servers[0].url``).  That is the single authority for the base URL on
# both sides of the dual runner: the F01 executor and this HTTP side take the
# **API root** (``http://host:port``).  The server root -- the
# address without that path -- is *derived* from it, because a few paths
# (``/health``, ``/api/v1/auth/...``) live outside the API root.
# --------------------------------------------------------------------------
ROOT = Path(__file__).resolve().parents[2]
CONTRACT_PATH = ROOT / "docs/08-本轮实施方案/contracts/openapi.json"


def contract_api_root_path() -> str:
    """The path part of the contract's API root."""
    document = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
    servers = document.get("servers") or []
    if not servers or not isinstance(servers[0].get("url"), str):
        raise RunnerError(f"the OpenAPI contract {CONTRACT_PATH} has no server URL")
    path = urlsplit(servers[0]["url"]).path.rstrip("/")
    if not path:
        raise RunnerError(f"the OpenAPI contract's server URL has no path: {servers[0]['url']}")
    return path


API_ROOT_PATH = contract_api_root_path()


def normalize_base_url(base_url: str) -> str:
    """The base URL as the contract names it: the API root.

    One shape, one place.  A base URL that stops short of the API root would
    make every request answer 404, and the run would look like a pass with
    nothing behind it -- so it is refused by name instead.
    """
    trimmed = base_url.rstrip("/")
    if not trimmed.endswith(API_ROOT_PATH):
        raise RunnerError(
            f"--base-url must be the API root the contract names ({API_ROOT_PATH}); "
            f"got {trimmed!r}. The F01 executor takes the same address, so the two "
            f"sides of the dual runner cannot disagree about it.")
    return trimmed


def server_root(api_root: str) -> str:
    """The server root, derived from the API root."""
    return api_root[: -len(API_ROOT_PATH)]


# --------------------------------------------------------------------------
# The compared item set.
#
# The browser side declares the same list.  ``--list-items`` on both
# executors exists so the guard test (scripts/ci/test_lingdoc_beta_evidence.py)
# can fail when the two sides drift apart -- a silent drift would make the
# comparison compare nothing while still looking green.
#
# The first five names are G4's five consistency assertion shapes
# (docs/08-本轮实施方案/acceptance/issue196-g4-browser-20261020/browser-evidence.json);
# the rest extend the coverage to the F01 surface (Decision 4).
# --------------------------------------------------------------------------
ITEM_KEYS: tuple[str, ...] = (
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

# Items this face cannot run without a real model.  They stay in the key set
# so the coverage gap is visible in every artifact, and both sides report
# them as NOT RUN with the same unblock condition.
MODEL_GATED_ITEMS: dict[str, tuple[str, str]] = {
    "browser_candidate_adoption_state_matches": (
        "候选采纳的读回需要一份真实生成的候选稿；本面不跑真实模型（54-Spec 决策 9）。",
        "起真实模型（55-Spec / Beta 协议 §2 的模型账本）后重跑 HTTP 侧与浏览器侧。",
    ),
    "browser_candidate_cancelled_state_matches": (
        "候选取消的读回需要一份真实生成的候选稿；本面不跑真实模型（54-Spec 决策 9）。",
        "起真实模型（55-Spec / Beta 协议 §2 的模型账本）后重跑 HTTP 侧与浏览器侧。",
    ),
}

# The delivery panel renders truncated digests and ids
# (frontend/src/views/lingdoc/DeliveryPanel.vue:501,509,547,554), so the
# HTTP side truncates the same way and the two sides compare the same
# string.  G4 did the same thing with the "9eb2c49a5abe..." prefix.
DIGEST_PREFIX = 12
ID_PREFIX = 8
SHA_PREFIX = 12


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


class RunnerError(RuntimeError):
    """A failure that must be reported as NOT RUN, never as a silent skip."""


def envelope_data(payload: Any) -> Any:
    if isinstance(payload, dict) and "data" in payload:
        return payload["data"]
    return payload


def request(api_root: str, method: str, path: str, *, token: str = "",
            payload: Any = None, key: str = "",
            expected: int | tuple[int, ...] = 200,
            timeout: float = 30.0) -> tuple[int, Any]:
    """One HTTP call against the real backend.

    Mirrors scripts/acceptance/governance_real_http.py: same envelope, same
    "raise with a readable reason" behaviour, same redaction discipline (the
    response body never leaves this function).
    """
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8") if payload is not None else None
    headers = {"Accept": "application/json", "X-Request-ID": "beta-452-http"}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    if key:
        headers["Idempotency-Key"] = key
    req = urllib.request.Request(api_root + path, data=body, headers=headers, method=method)
    status = 0
    raw = b""
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            status, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read()
    except (urllib.error.URLError, TimeoutError, OSError) as error:
        raise RunnerError(f"{method} {path} could not reach the service ({type(error).__name__})") from None
    try:
        data = json.loads(raw.decode("utf-8")) if raw else {}
    except (UnicodeDecodeError, json.JSONDecodeError):
        data = {}
    accepted = expected if isinstance(expected, tuple) else (expected,)
    if status not in accepted:
        detail = ""
        if isinstance(data, dict):
            error_obj = data.get("error")
            if isinstance(error_obj, dict):
                detail = str(error_obj.get("code", "unknown"))
        raise RunnerError(f"{method} {path} returned {status} (code={detail or 'unknown'})")
    return status, data


def health(api_root: str, timeout: float = 5.0) -> bool:
    try:
        with urllib.request.urlopen(server_root(api_root) + "/health", timeout=timeout) as response:
            return response.status == 200
    except (urllib.error.URLError, TimeoutError, OSError):
        return False


def register_login(api_root: str, email: str, password: str, *, timeout: float) -> tuple[str, bool]:
    """Register (once) and log in an isolated account.  Returns ``(token, reused)``.

    An email that already exists is the whole point of ``--email`` (#667):
    the server answers 400 ("user with this email already exists") and the
    account is reused through the login below instead of failing the run.
    Every other rejected status (invite-only 403, unreachable, 5xx) stays
    fatal with its own reason -- only the one answer that means "the account
    is already there" falls through.
    """
    suffix = uuid.uuid4().hex[:12]
    username = "beta452-" + suffix
    status, _registration = request(server_root(api_root), "POST", "/api/v1/auth/register", payload={
        "username": username, "email": email, "password": password,
    }, expected=(200, 201, 400), timeout=timeout)
    reused = status == 400
    _, login = request(server_root(api_root), "POST", "/api/v1/auth/login", payload={
        "email": email, "password": password,
    }, timeout=timeout)
    token = login.get("token") if isinstance(login, dict) else None
    if not isinstance(token, str) or not token:
        raise RunnerError("isolated login did not return a token")
    identity = login.get("user") if isinstance(login, dict) else None
    logged_in = identity.get("email") if isinstance(identity, dict) else None
    if not isinstance(logged_in, str) or not logged_in.strip():
        raise RunnerError("isolated login omitted the account identity (user.email); "
                          "cannot confirm the reused account is the one --email named")
    if logged_in.strip().lower() != email.strip().lower():
        raise RunnerError(f"isolated login returned a different account ({logged_in}) "
                          f"than --email ({email}); refusing to compare the wrong project")
    return token, reused


def create_and_activate(api_root: str, token: str, *, timeout: float) -> str:
    """Create a project, give it a spec, activate it, author and confirm its chapters."""
    suffix = uuid.uuid4().hex[:8]
    _, created = request(api_root, "POST", "/projects", token=token,
                         key="beta452-project-" + suffix,
                         payload={"name": "Beta 452 parity " + suffix, "template_id": "template-demo"},
                         expected=(200, 201), timeout=timeout)
    project = envelope_data(created)
    if not isinstance(project, dict) or not project.get("id"):
        raise RunnerError("project creation did not return a project id")
    project_id = str(project["id"])
    _, spec_response = request(api_root, "PUT", f"/projects/{project_id}/spec",
                               token=token, key="beta452-spec-" + suffix,
                               payload={"expected_spec_revision": 0, "fields": {
                                   "research_subject": "Beta 452 dual runner subject",
                                   "research_goal": "Beta 452 dual runner goal"}},
                               timeout=timeout)
    spec_project = envelope_data(spec_response)
    project_version = int(spec_project.get("project_version", 0)) if isinstance(spec_project, dict) else 0
    _, receipt_response = request(api_root, "POST", f"/projects/{project_id}/activation-read",
                                  token=token, payload={"expected_project_version": project_version},
                                  expected=(200, 201), timeout=timeout)
    receipt = envelope_data(receipt_response)
    receipt_id = receipt.get("id") if isinstance(receipt, dict) else None
    if not receipt_id:
        raise RunnerError("activation read did not return a receipt")
    request(api_root, "POST", f"/projects/{project_id}/activate", token=token,
            key="beta452-activate-" + suffix,
            payload={"expected_spec_revision": 1, "expected_project_version": project_version,
                     "reviewed_project_version": project_version, "activation_read_id": receipt_id},
            timeout=timeout)
    _, chapters_response = request(api_root, "GET", f"/projects/{project_id}/chapters",
                                   token=token, timeout=timeout)
    chapters = envelope_data(chapters_response)
    if not isinstance(chapters, list) or not chapters:
        raise RunnerError("activated project did not expose its chapters")
    for chapter in chapters:
        chapter_id = chapter.get("id")
        if not isinstance(chapter_id, str):
            continue
        section = chapter.get("section_id")
        body = "Beta 452 dual runner body for section " + str(section) + "."
        _, saved = request(api_root, "POST",
                           f"/projects/{project_id}/chapters/{chapter_id}/versions",
                           token=token, key="beta452-save-" + str(section) + "-" + suffix,
                           payload={"expected_chapter_version_id": None, "expected_spec_revision": 1,
                                    "body_markdown": body, "source_ids": []},
                           expected=(200, 201), timeout=timeout)
        saved_data = envelope_data(saved)
        current_id = saved_data.get("current_version_id") if isinstance(saved_data, dict) else None
        if not current_id:
            raise RunnerError(f"chapter {section} did not return a current version")
        request(api_root, "POST",
                f"/projects/{project_id}/chapters/{chapter_id}/confirmations",
                token=token, key="beta452-confirm-" + str(section) + "-" + suffix,
                payload={"expected_chapter_version_id": current_id, "expected_spec_revision": 1,
                         "review_decisions": []},
                expected=(200, 201), timeout=timeout)
    return project_id


def prepare_delivery(api_root: str, token: str, project_id: str, *, timeout: float,
                     not_run: list[dict[str, str]]) -> None:
    """Run the delivery check, freeze a snapshot and start the export.

    This is the HTTP twin of what a user does in the delivery panel: check,
    freeze, generate the DOCX.  Each step is independent: when one of them
    cannot run, that fact is recorded in ``not_run`` instead of silently
    producing an empty snapshot.
    """
    _, project_response = request(api_root, "GET", f"/projects/{project_id}",
                                  token=token, timeout=timeout)
    project = envelope_data(project_response)
    project_version = project.get("project_version") if isinstance(project, dict) else None
    if not isinstance(project_version, int):
        raise RunnerError("project readback omitted project_version")

    try:
        request(api_root, "POST", f"/projects/{project_id}/checks", token=token,
                payload={"expected_project_version": project_version}, timeout=timeout)
    except RunnerError as error:
        not_run.append({
            "item": "current_check_status_matches",
            "reason": f"The delivery check did not run: {error}",
            "unblock": "Make the delivery check succeed on the real backend and re-run the HTTP side.",
        })

    try:
        _, freeze = request(api_root, "POST", f"/projects/{project_id}/releases",
                             token=token, key="beta452-freeze-" + uuid.uuid4().hex[:10],
                             payload={"expected_project_version": project_version},
                             expected=(200, 201), timeout=timeout)
        snapshot = envelope_data(freeze)
        snapshot_id = snapshot.get("id") if isinstance(snapshot, dict) else None
    except RunnerError as error:
        snapshot_id = None
        not_run.append({
            "item": "browser_snapshot_digest_matches_http",
            "reason": f"The delivery snapshot could not be frozen: {error}",
            "unblock": "Make the freeze succeed on the real backend (the delivery check must pass) "
                       "and re-run the HTTP side.",
        })
    if not snapshot_id:
        return

    try:
        request(api_root, "POST", f"/projects/{project_id}/releases/{snapshot_id}/exports",
                token=token, key="beta452-export-" + uuid.uuid4().hex[:10],
                payload={"format": "docx"}, expected=(200, 201, 202), timeout=timeout)
    except RunnerError as error:
        not_run.append({
            "item": "browser_export_id_matches_http",
            "reason": f"The DOCX export could not be started: {error}",
            "unblock": "Make the export succeed on the real backend and re-run the HTTP side.",
        })


def read_items(api_root: str, token: str, project_id: str, *, timeout: float,
               not_run: list[dict[str, str]]) -> dict[str, Any]:
    """Read the delivery state and turn it into the comparable item set.

    The reading order mirrors the delivery panel exactly: the panel shows
    ``snapshots[0]`` first (frontend/src/views/lingdoc/DeliveryPanel.vue:109-123)
    and hangs the artifacts of that snapshot under it, so the HTTP side reads
    the same server-ordered lists.
    """
    items: dict[str, Any] = {}
    _, project_response = request(api_root, "GET", f"/projects/{project_id}",
                                  token=token, timeout=timeout)
    project = envelope_data(project_response)
    project_version = project.get("project_version") if isinstance(project, dict) else None
    if not isinstance(project_version, int):
        raise RunnerError("project readback omitted project_version")

    _, releases_response = request(api_root, "GET", f"/projects/{project_id}/releases",
                                   token=token, timeout=timeout)
    releases = envelope_data(releases_response)
    release_items = releases.get("items") if isinstance(releases, dict) else None
    if not isinstance(release_items, list) or not release_items:
        # No snapshot: the panel would render nothing either.  Every
        # snapshot-dependent item is NOT RUN, not silently equal.
        for item in ("browser_snapshot_digest_matches_http", "snapshot_id_matches",
                     "project_version_matches", "spec_revision_matches",
                     "chapters_in_snapshot_matches", "sources_in_snapshot_matches",
                     "snapshot_check_status"):
            not_run.append({
                "item": item,
                "reason": "The project has no frozen delivery snapshot, so the delivery panel shows nothing.",
                "unblock": "Freeze a delivery snapshot for this project (delivery check -> freeze), "
                           "then re-run the HTTP side.",
            })
        return items
    snapshot = release_items[0]

    _, exports_response = request(api_root, "GET", f"/projects/{project_id}/exports",
                                  token=token, timeout=timeout)
    exports = envelope_data(exports_response)
    export_items = exports.get("items") if isinstance(exports, dict) else None
    export_items = export_items if isinstance(export_items, list) else []
    snapshot_id = str(snapshot.get("id", ""))
    artifact = next((item for item in export_items
                     if isinstance(item, dict) and str(item.get("snapshot_id", "")) == snapshot_id), None)

    frozen = snapshot.get("frozen_input") if isinstance(snapshot, dict) else None
    frozen = frozen if isinstance(frozen, dict) else {}
    check = snapshot.get("check") if isinstance(snapshot, dict) else None
    check = check if isinstance(check, dict) else {}

    digest = str(snapshot.get("snapshot_digest", ""))
    items["browser_snapshot_digest_matches_http"] = digest[:DIGEST_PREFIX]
    items["snapshot_id_matches"] = snapshot_id[:ID_PREFIX]
    items["project_version_matches"] = frozen.get("project_version")
    items["spec_revision_matches"] = frozen.get("spec_revision")
    chapters = frozen.get("chapters")
    items["chapters_in_snapshot_matches"] = len(chapters) if isinstance(chapters, list) else 0
    sources = frozen.get("sources")
    items["sources_in_snapshot_matches"] = len(sources) if isinstance(sources, list) else 0
    items["snapshot_check_status"] = str(check.get("status", ""))

    if artifact is None:
        not_run.append({
            "item": "browser_export_id_matches_http",
            "reason": "This project has no export artifact under the compared snapshot.",
            "unblock": "Generate a DOCX export for the frozen snapshot and re-run the HTTP side.",
        })
        not_run.append({
            "item": "browser_export_sha_matches_http",
            "reason": "This project has no export artifact under the compared snapshot.",
            "unblock": "Generate a DOCX export for the frozen snapshot and re-run the HTTP side.",
        })
    else:
        items["browser_export_id_matches_http"] = str(artifact.get("id", ""))[:ID_PREFIX]
        sha = str(artifact.get("file_sha256", "") or "")
        if sha:
            items["browser_export_sha_matches_http"] = sha[:SHA_PREFIX]
        else:
            not_run.append({
                "item": "browser_export_sha_matches_http",
                "reason": "The export artifact exists but carries no file hash yet "
                          "(the DOCX render is asynchronous and real rendering is out of this face's scope).",
                "unblock": "Let the DOCX render finish (docreader / renderer dependency), then re-run "
                           "the HTTP side so the artifact carries file_sha256.",
            })

    _, check_response = request(api_root, "POST", f"/projects/{project_id}/checks",
                                 token=token, payload={"expected_project_version": project_version},
                                 timeout=timeout)
    current = envelope_data(check_response)
    items["current_check_status"] = str(current.get("status", "")) if isinstance(current, dict) else ""
    return items


def model_gated_not_run() -> list[dict[str, str]]:
    return [{"item": item, "reason": reason, "unblock": unblock}
            for item, (reason, unblock) in sorted(MODEL_GATED_ITEMS.items())]


def write_json(path: Path, payload: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(temporary, path)


def run(args: argparse.Namespace) -> tuple[dict[str, Any], dict[str, Any] | None, int]:
    api_root = normalize_base_url(args.base_url)
    not_run: list[dict[str, str]] = []
    email = args.email
    password = args.password
    project_id = args.project_id

    if not health(api_root):
        # Not reachable: every item is NOT RUN with an unblock condition.
        # Reporting a green snapshot here would be a silent skip.
        for item in ITEM_KEYS:
            reason, unblock = MODEL_GATED_ITEMS.get(
                item, ("The real backend did not answer /health.",
                       "Start the real LingDoc backend (make dev-app or an isolated server process) "
                       "and point --base-url at it, then re-run."))
            not_run.append({"item": item, "reason": reason, "unblock": unblock})
        return {
            "source": "http",
            "at": now_iso(),
            "items": {},
            "status": "NOT RUN",
            "not_run": not_run,
        }, None, 2

    context: dict[str, Any] = {}
    if args.context and args.context.is_file():
        context = json.loads(args.context.read_text(encoding="utf-8"))
        email = email or str(context.get("email", ""))
        password = password or str(context.get("password", ""))
        project_id = project_id or str(context.get("project_id", "")) or None

    if not email:
        email = f"beta452-{uuid.uuid4().hex[:12]}@example.com"
    if not password:
        password = "Beta452-" + secrets.token_hex(8)
    token, account_reused = register_login(api_root, email, password, timeout=args.timeout)
    if not project_id:
        project_id = create_and_activate(api_root, token, timeout=args.timeout)

    prepare_delivery(api_root, token, project_id, timeout=args.timeout, not_run=not_run)
    items = read_items(api_root, token, project_id, timeout=args.timeout, not_run=not_run)
    not_run.extend(model_gated_not_run())

    snapshot = {
        "source": "http",
        "at": now_iso(),
        "items": items,
        "status": "PASS" if items else "NOT RUN",
        "not_run": not_run,
        "base_url": api_root,
        # New or reused account, so a reviewer never mistakes a reused
        # identity for a freshly registered one (#667).
        "account": "reused" if account_reused else "registered",
    }
    handoff = {
        "base_url": api_root,
        "email": email,
        "password": password,
        "project_id": project_id,
        "created_at": now_iso(),
        "note": "Hand-off for the browser side. Not evidence: it carries a password.",
    }
    return snapshot, handoff, 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--base-url",
                        help="API root of the real LingDoc backend, as the OpenAPI contract "
                             f"names it (e.g. http://127.0.0.1:8080{API_ROOT_PATH})")
    parser.add_argument("--out", help="Where to write the HTTP-side snapshot JSON")
    parser.add_argument("--context", help="Identity/project hand-off file written by a previous run")
    parser.add_argument("--context-out", help="Where to write the hand-off file for the browser side")
    parser.add_argument("--email", default="", help="Reuse this account instead of registering a new one")
    parser.add_argument("--password", default="", help="Password of --email")
    parser.add_argument("--project-id", default="", help="Read back an existing project instead of creating one")
    parser.add_argument("--timeout", type=float, default=30.0, help="Per-request timeout in seconds")
    parser.add_argument("--list-items", action="store_true",
                        help="Print the compared item key set as JSON and exit")
    args = parser.parse_args()

    if args.list_items:
        print(json.dumps({"source": "http", "items": list(ITEM_KEYS)}, ensure_ascii=False))
        return 0
    if not args.base_url or not args.out:
        parser.error("--base-url and --out are required")

    out = Path(args.out).resolve()
    try:
        snapshot, handoff, code = run(args)
    except RunnerError as error:
        not_run = [{"item": item, "reason": str(error),
                    "unblock": "Fix the real backend / the credentials and re-run the HTTP side."}
                   for item in ITEM_KEYS]
        snapshot = {"source": "http", "at": now_iso(), "items": {}, "status": "NOT RUN", "not_run": not_run}
        handoff = None
        code = 2
    write_json(out, snapshot)
    if handoff is not None:
        context_out = Path(args.context_out).resolve() if args.context_out else \
            out.parent / (".beta-run-context.json")
        write_json(context_out, handoff)
    if snapshot.get("status") == "PASS":
        print(f"PASS: HTTP side read {len(snapshot.get('items', {}))} items from {args.base_url}")
    else:
        print(f"NOT RUN: HTTP side produced no comparable items ({args.base_url})")
    return code


if __name__ == "__main__":
    raise SystemExit(main())
