"""Offline guard tests for the ``--email`` reuse path of the HTTP executor (#667).

What this file guards
---------------------
``scripts/acceptance/beta_http_readback.py`` promises (its ``--email`` help
text) to reuse an existing account instead of registering a new one.  Issue
#667: for an email that already exists, ``POST /api/v1/auth/register``
answers **400 (code=1000)** -- "user with this email already exists" -- and
the executor treated that as fatal, so the promised reuse path could never
run: every item landed in ``not_run`` with the register 400 as the reason.

These tests drive the **real executor as a subprocess** against a faithful
local fake backend (same envelope, same status words, same 400 code=1000 for
a duplicate email) and pin the three behaviours the ticket asks for:

* an **existing** email falls back to ``/api/v1/auth/login`` and reads the
  ``--project-id`` project (snapshot ``PASS`` with items),
* a **fresh** email keeps the register-then-login path,
* a login that lands on a **different account** than ``--email`` is fatal,
  never a silent comparison against the wrong project.

Why offline
-----------
Like the other ``scripts/ci`` guard tests, these run with
``python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py' -v`` and
must not need a real backend, a browser or the network: the fake backend is
an ``http.server`` on 127.0.0.1 bound to an ephemeral port.
"""
from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

REPO_ROOT = Path(__file__).resolve().parents[2]
ACCEPTANCE_DIR = REPO_ROOT / "scripts" / "acceptance"
HTTP_EXECUTOR = ACCEPTANCE_DIR / "beta_http_readback.py"

sys.path.insert(0, str(ACCEPTANCE_DIR))
from beta_http_readback import API_ROOT_PATH  # noqa: E402

PROJECT_ID = "p-1"
SNAPSHOT_ID = "snap-1"
EXPORT_ID = "exp-1"
SNAPSHOT_DIGEST = "9eb2c49a5abe0147f2c9" + "a" * 20
EXPORT_SHA = "0123456789abcdef" + "0" * 20


class _FakeBackendHandler(BaseHTTPRequestHandler):
    """The three auth endpoints plus the minimal delivery readback surface.

    Error bodies follow the product's envelope (``{"error": {"code": ...}}``),
    and the duplicate-email register answer is the one captured in the field
    (issue #667): 400 with code=1000.
    """

    server_version = "FakeLingDoc/667"

    def log_message(self, format: str, *args: Any) -> None:  # noqa: A002 - stdlib signature
        pass  # offline guard tests stay quiet

    # -- plumbing ----------------------------------------------------------
    def _body(self) -> dict[str, Any]:
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b""
        try:
            payload = json.loads(raw.decode("utf-8")) if raw else {}
        except (UnicodeDecodeError, json.JSONDecodeError):
            payload = {}
        return payload if isinstance(payload, dict) else {}

    def _answer(self, status: int, payload: Any) -> None:
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _reject(self, status: int, code: int, message: str) -> None:
        self._answer(status, {"error": {"code": code, "message": message}})

    # -- routing -----------------------------------------------------------
    def do_GET(self) -> None:  # noqa: N802 - stdlib signature
        backend = self.server.backend
        backend.calls.append(("GET", self.path, 0))
        if self.path == "/health":
            return self._answer(200, {})
        if self.path == f"{API_ROOT_PATH}/projects/{PROJECT_ID}":
            return self._answer(200, {"data": {"id": PROJECT_ID, "project_version": 5}})
        if self.path == f"{API_ROOT_PATH}/projects/{PROJECT_ID}/releases":
            return self._answer(200, {"data": {"items": [{
                "id": SNAPSHOT_ID,
                "snapshot_digest": SNAPSHOT_DIGEST,
                "frozen_input": {"project_version": 5, "spec_revision": 1,
                                 "chapters": [{"id": "c-1"}, {"id": "c-2"}],
                                 "sources": [{"id": "s-1"}]},
                "check": {"status": "passed"},
            }]}})
        if self.path == f"{API_ROOT_PATH}/projects/{PROJECT_ID}/exports":
            return self._answer(200, {"data": {"items": [{
                "id": EXPORT_ID, "snapshot_id": SNAPSHOT_ID, "file_sha256": EXPORT_SHA,
            }]}})
        return self._reject(404, 1404, f"no such route {self.path}")

    def do_POST(self) -> None:  # noqa: N802 - stdlib signature
        backend = self.server.backend
        body = self._body()
        if self.path == "/api/v1/auth/register":
            email, password = str(body.get("email", "")), str(body.get("password", ""))
            if not email or not password:
                return self._reject(400, 1000, "Username, email and password are required")
            if email in backend.registered:
                # The answer the real backend gives for an existing email (#667).
                return self._reject(400, 1000, "user with this email already exists")
            backend.registered[email] = password
            backend.calls.append(("POST", self.path, 201))
            return self._answer(201, {"success": True, "user": {"email": email}})
        if self.path == "/api/v1/auth/login":
            email, password = str(body.get("email", "")), str(body.get("password", ""))
            if backend.registered.get(email) != password:
                return self._reject(401, 1001, "invalid email or password")
            identity = backend.login_identity or email
            backend.calls.append(("POST", self.path, 200))
            return self._answer(200, {"success": True, "token": "tok-" + email,
                                      "user": {"email": identity}})
        calls = backend.calls
        if self.path == f"{API_ROOT_PATH}/projects/{PROJECT_ID}/checks":
            calls.append(("POST", self.path, 200))
            return self._answer(200, {"data": {"status": "passed"}})
        if self.path == f"{API_ROOT_PATH}/projects/{PROJECT_ID}/releases":
            calls.append(("POST", self.path, 201))
            return self._answer(201, {"data": {"id": SNAPSHOT_ID}})
        if self.path == f"{API_ROOT_PATH}/projects/{PROJECT_ID}/releases/{SNAPSHOT_ID}/exports":
            calls.append(("POST", self.path, 201))
            return self._answer(201, {"data": {"id": EXPORT_ID}})
        calls.append(("POST", self.path, 404))
        return self._reject(404, 1404, f"no such route {self.path}")


class _FakeBackend:
    """A startable/stoppable fake LingDoc backend for one test."""

    def __init__(self, *, login_identity: str | None = None) -> None:
        self.registered: dict[str, str] = {}
        self.calls: list[tuple[str, str, int]] = []
        self.login_identity = login_identity
        self._server = ThreadingHTTPServer(("127.0.0.1", 0), _FakeBackendHandler)
        self._server.backend = self  # type: ignore[attr-defined]
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)

    @property
    def base_url(self) -> str:
        return f"http://127.0.0.1:{self._server.server_address[1]}{API_ROOT_PATH}"

    def __enter__(self) -> "_FakeBackend":
        self._thread.start()
        return self

    def __exit__(self, *exc: object) -> None:
        self._server.shutdown()
        self._server.server_close()
        self._thread.join(timeout=5)

    def saw(self, method: str, path: str) -> bool:
        return any(call[0] == method and call[1] == path for call in self.calls)


def run_executor(base_url: str, out: Path, *extra: str,
                 timeout: float = 60.0) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(HTTP_EXECUTOR), "--base-url", base_url,
         "--out", str(out), *extra],
        capture_output=True, text=True, encoding="utf-8", errors="replace",
        timeout=timeout, check=False)


class EmailReuseTests(unittest.TestCase):
    """The ``--email`` reuse path keeps the promise its help text makes."""

    def test_an_existing_email_falls_back_to_login_and_reads_the_project(self):
        """Issue #667's exact symptom must not come back.

        Before the fix the run died on the register 400 (the fake backend
        answers it exactly as the real one did) and every item was NOT RUN
        with "auth/register returned 400" as the reason.
        """
        with _FakeBackend() as backend:
            backend.registered["reuse-667@example.com"] = "Beta452-reuse"
            with tempfile.TemporaryDirectory() as work:
                out = Path(work) / "http.json"
                result = run_executor(backend.base_url, out,
                                      "--email", "reuse-667@example.com",
                                      "--password", "Beta452-reuse",
                                      "--project-id", PROJECT_ID)
                payload = json.loads(out.read_text(encoding="utf-8"))
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(payload.get("status"), "PASS", payload)
                self.assertGreater(len(payload.get("items", {})), 0, payload)
                self.assertTrue(backend.saw("POST", "/api/v1/auth/login"),
                                "an existing account must be reused through login")
                self.assertTrue(backend.saw("GET", f"{API_ROOT_PATH}/projects/{PROJECT_ID}"),
                                "the reuse path must still read the requested project")

    def test_a_fresh_email_still_registers_and_logs_in(self):
        """Criterion 3 of #667: the register-then-login path keeps working."""
        with _FakeBackend() as backend:
            with tempfile.TemporaryDirectory() as work:
                out = Path(work) / "http.json"
                result = run_executor(backend.base_url, out,
                                      "--email", "fresh-667@example.com",
                                      "--password", "Beta452-fresh",
                                      "--project-id", PROJECT_ID)
                payload = json.loads(out.read_text(encoding="utf-8"))
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(payload.get("status"), "PASS", payload)
                self.assertGreater(len(payload.get("items", {})), 0, payload)
                self.assertTrue(backend.saw("POST", "/api/v1/auth/register"))
                self.assertTrue(backend.saw("POST", "/api/v1/auth/login"))
                self.assertTrue(backend.saw("GET", f"{API_ROOT_PATH}/projects/{PROJECT_ID}"))

    def test_a_login_that_lands_on_a_different_account_is_fatal(self):
        """Criterion 4 of #667: a mismatched identity must never pass quietly."""
        with _FakeBackend(login_identity="someone-else@example.com") as backend:
            backend.registered["reuse-667@example.com"] = "Beta452-reuse"
            with tempfile.TemporaryDirectory() as work:
                out = Path(work) / "http.json"
                result = run_executor(backend.base_url, out,
                                      "--email", "reuse-667@example.com",
                                      "--password", "Beta452-reuse",
                                      "--project-id", PROJECT_ID)
                payload = json.loads(out.read_text(encoding="utf-8"))
                self.assertNotEqual(result.returncode, 0)
                self.assertNotEqual(payload.get("status"), "PASS", payload)
                reasons = " | ".join(entry.get("reason", "")
                                     for entry in payload.get("not_run", []))
                self.assertIn("different account", reasons)


if __name__ == "__main__":
    unittest.main()
