"""Guard tests for the Beta evidence producers (54-Spec / 53-Spec).

What this file guards
---------------------
Three producers land in this repository for the Beta acceptance round:

* ``scripts/acceptance/beta_http_readback.py`` -- the HTTP side of the
  dual runner (real backend, HTTP only),
* ``scripts/acceptance/beta_browser_cdp.mjs`` -- the browser side of the
  dual runner (real front end, real backend, CDP),
* ``scripts/acceptance/beta_evidence_parity.py`` -- the orchestrator and the
  three-way comparison (browser == HTTP == after-reload),

and one more producer lands in a parallel face:

* ``scripts/acceptance/beta_evidence.py`` -- the six-artifact evidence
  directory of the Beta acceptance protocol (docs/08-本轮实施方案/Beta首版真实验收协议.md §6).

These tests pin the seams a reviewer needs to trust:

* the three command line contracts stay exactly as documented,
* the parity report keeps its shape and its three-state semantics
  (PASS / FAIL / NOT RUN),
* **nothing is skipped silently** -- whatever did not run is written into
  ``not_run[]`` with a machine-readable unblock condition,
* the assertions can go red (a deliberately injected inconsistency has to
  turn the corresponding comparison red),
* no artifact carries a token, a password, a request body, a response body
  or an unredacted absolute path,
* the six artifacts of a Beta evidence directory are complete, redacted and
  self-consistent (the same head SHA, the same run id, and no "passed"
  verdict that hides a FAIL / BLOCKED / NOT RUN in the main flow).

Why these tests live in ``scripts/ci``
--------------------------------------
They are the guard layer: CI discovers them with
``python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py' -v``.
They must run **offline**: no real service, no real browser, no network.

The "silent skip" rule
-----------------------
A check that cannot run is not allowed to pass quietly.  When the evidence
directory does not exist yet, these tests record an explicit
``NOT RUN`` line (on stderr, with a reason and an unblock condition) instead
of pretending the check passed.  When the evidence directory *does* exist
but is incomplete, the test is red.
"""
from __future__ import annotations

import json
import os
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any, Iterable

REPO_ROOT = Path(__file__).resolve().parents[2]
ACCEPTANCE_DIR = REPO_ROOT / "scripts" / "acceptance"
HTTP_EXECUTOR = ACCEPTANCE_DIR / "beta_http_readback.py"
BROWSER_EXECUTOR = ACCEPTANCE_DIR / "beta_browser_cdp.mjs"
PARITY_EXECUTOR = ACCEPTANCE_DIR / "beta_evidence_parity.py"

sys.path.insert(0, str(ACCEPTANCE_DIR))
# The evidence producer and the contract runner it drives live two levels down,
# so importing them needs the repository root on the path as well.
sys.path.insert(0, str(REPO_ROOT))
import beta_evidence  # noqa: E402
import beta_evidence_parity as parity  # noqa: E402
from scripts.lingdoc_mock.run_f01 import (  # noqa: E402
    ScenarioRunner, WorkflowError, spec_from_document, _SelfTestResponse,
)

# The six artifacts of the Beta acceptance protocol (§6).  The evidence
# directory is ``docs/08-本轮实施方案/acceptance/beta-first/<full SHA>/<run id>/``.
EVIDENCE_ROOT = REPO_ROOT / "docs" / "08-本轮实施方案" / "acceptance" / "beta-first"
SIX_ARTIFACTS = (
    "README.md",
    "f01-report.json",
    "runtime-manifest.json",
    "browser-check.md",
    "restart-readback.json",
    "word-check.md",
)

# Values that mean "there is nothing here", so a field carrying one of them
# is not a leak.
PLACEHOLDERS = {
    "", "***", "****", "redacted", "masked", "present", "absent", "none", "null",
    "n/a", "na", "not stored", "not recorded", "(redacted)", "（已脱敏）", "（未记录）",
    "不写入", "不进证据", "未记录",
}

# --------------------------------------------------------------------------
# Redaction detectors
#
# The rule (54-Spec decision 8 / 53-Spec decision 8): an artifact that shows
# a token-shaped string, a password field, a request body, a response body or
# an unredacted absolute path is red.  The detectors below look for the
# *shape* of a leak, not for one particular value.
# --------------------------------------------------------------------------
TOKEN_SHAPES: tuple[tuple[str, re.Pattern[str]], ...] = (
    ("jwt", re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}")),
    ("bearer-token", re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._\-]{16,}")),
    ("token-assignment", re.compile(
        r"(?i)\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|secret|token|jwt)\b\s*[=:]\s*"
        r"[\"']?[A-Za-z0-9._\-]{12,}")),
)

PASSWORD_FIELD = re.compile(
    r"(?i)\b(?:password|passwd|pwd|口令|密码)\b[\"']?\s*[=:：]\s*[\"']?([^\s\"',}]{4,})")

BODY_FIELD = re.compile(
    r"(?i)\b(?:request_body|response_body|request_payload|response_payload|raw_request|raw_response|"
    r"request_text|response_text|body_raw)\b[\"']?\s*[=:：]\s*(.+)")

# A user home directory is the classic absolute-path leak: it names the
# operator and their account.  The repository's own path is not a leak -- the
# evidence directory lives inside it.
HOME_PATH = re.compile(r"(?i)(?:[A-Z]:[\\/]Users[\\/][^\\\/\s\"']+)|(?:/(?:home|Users)/[^/\s\"']+)")

NOT_RUN_RECORDS: list[dict[str, str]] = []

# The NOT RUN lines below carry Chinese text.  On a Windows console the
# default stderr encoding is the console codepage, which would mangle them in
# the CI log; UTF-8 keeps them readable wherever the log is read.
try:  # pragma: no cover - depends on the interpreter's stream support
    sys.stderr.reconfigure(encoding="utf-8", errors="replace")
except (AttributeError, OSError, ValueError):
    pass


def record_not_run(item: str, reason: str, unblock: str) -> dict[str, str]:
    """Record an explicit NOT RUN.

    A check that could not run must be visible.  The record goes to stderr
    (so it shows up in the CI log next to the test names) and into the
    module level list, so a test can assert that it exists and carries both
    a reason and an unblock condition.
    """
    entry = {"item": item, "reason": reason, "unblock": unblock}
    NOT_RUN_RECORDS.append(entry)
    sys.stderr.write("NOT RUN item={item} reason={reason} unblock={unblock}\n".format(**entry))
    sys.stderr.flush()
    return entry


def find_leaks(text: str) -> list[str]:
    """Every leak shape found in one artifact."""
    leaks: list[str] = []
    for name, pattern in TOKEN_SHAPES:
        if pattern.search(text):
            leaks.append(name)
    for match in PASSWORD_FIELD.finditer(text):
        if match.group(1).strip().lower() not in PLACEHOLDERS:
            leaks.append("password-field")
            break
    for match in BODY_FIELD.finditer(text):
        value = match.group(1).strip().strip('"').strip("'").strip()
        if value.lower() in PLACEHOLDERS or value in ("{}", "[]"):
            continue
        leaks.append("request-or-response-body")
        break
    if HOME_PATH.search(text):
        leaks.append("unredacted-absolute-path")
    return leaks


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def subprocess_env() -> dict[str, str]:
    """A child environment whose standard streams are UTF-8.

    The Windows console codepage is not UTF-8, so a child that prints the
    Chinese docstrings would otherwise emit GBK bytes and the capture would
    fail to decode them.
    """
    env = os.environ.copy()
    env["PYTHONIOENCODING"] = "utf-8"
    env["PYTHONUTF8"] = "1"
    return env


def run_python(*args: str, timeout: float = 300.0) -> subprocess.CompletedProcess[str]:
    return subprocess.run([sys.executable, *args], capture_output=True, text=True,
                          encoding="utf-8", errors="replace", env=subprocess_env(),
                          check=False, timeout=timeout)


def run_node(*args: str, timeout: float = 300.0) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["node", *args], capture_output=True, text=True,
                          encoding="utf-8", errors="replace", env=subprocess_env(),
                          check=False, timeout=timeout)


def node_available() -> bool:
    return shutil.which("node") is not None


def evidence_runs() -> list[tuple[str, str, Path]]:
    """Every ``<full SHA>/<run id>/`` evidence directory in the repository."""
    if not EVIDENCE_ROOT.is_dir():
        return []
    runs: list[tuple[str, str, Path]] = []
    for sha_dir in sorted(EVIDENCE_ROOT.iterdir()):
        if not sha_dir.is_dir():
            continue
        for run_dir in sorted(sha_dir.iterdir()):
            if run_dir.is_dir():
                runs.append((sha_dir.name, run_dir.name, run_dir))
    return runs


def load_json(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


# --------------------------------------------------------------------------
# 1. The three command line contracts
# --------------------------------------------------------------------------
class CommandLineContractTests(unittest.TestCase):
    """The three CLIs keep the documented flags."""

    def test_http_executor_help_lists_the_documented_flags(self):
        result = run_python(str(HTTP_EXECUTOR), "--help")
        self.assertEqual(result.returncode, 0, result.stderr)
        for flag in ("--base-url", "--out", "--context", "--context-out", "--list-items"):
            self.assertIn(flag, result.stdout, f"{flag} is missing from the HTTP executor's help")

    def test_browser_executor_help_lists_the_documented_flags(self):
        self.assertTrue(node_available(), "node is required to read the browser executor's help")
        result = run_node(str(BROWSER_EXECUTOR), "--help")
        output = result.stdout + result.stderr
        self.assertIn("--url", output)
        self.assertIn("--out", output)
        self.assertIn("--context", output)

    def test_parity_cli_help_lists_the_documented_flags(self):
        result = run_python(str(PARITY_EXECUTOR), "--help")
        self.assertEqual(result.returncode, 0, result.stderr)
        for flag in ("--http", "--browser", "--out", "--base-url", "--frontend-url"):
            self.assertIn(flag, result.stdout, f"{flag} is missing from the parity CLI's help")

    def test_http_executor_and_browser_executor_declare_the_same_item_set(self):
        """Both sides must compare the same keys.

        A silent drift would make the comparison compare nothing while still
        looking green, so the two declarations are compared, not trusted.
        """
        http_result = run_python(str(HTTP_EXECUTOR), "--list-items")
        self.assertEqual(http_result.returncode, 0, http_result.stderr)
        http_items = json.loads(http_result.stdout).get("items")
        self.assertIsInstance(http_items, list)
        self.assertGreater(len(http_items), 0)
        if not node_available():
            record_not_run(
                "browser_executor_item_set",
                "node is not installed in this environment, so the browser executor could not be asked "
                "for its item set.",
                "Install Node 22+ (the CI 'LingDoc PR gate' job does not provide it yet) and re-run "
                "python -m unittest scripts.ci.test_lingdoc_beta_evidence -v.")
            return
        browser_result = run_node(str(BROWSER_EXECUTOR), "--list-items")
        self.assertEqual(browser_result.returncode, 0, browser_result.stderr)
        browser_items = json.loads(browser_result.stdout).get("items")
        self.assertEqual(sorted(http_items), sorted(browser_items),
                         "the HTTP side and the browser side declare different item sets")

    def test_http_executor_writes_the_documented_snapshot_shape(self):
        """``http.json`` keeps ``source`` / ``at`` / ``items`` even when nothing ran."""
        with tempfile.TemporaryDirectory() as work:
            out = Path(work) / "http.json"
            result = run_python(str(HTTP_EXECUTOR), "--base-url",
                                f"http://127.0.0.1:{free_port()}", "--out", str(out))
            self.assertNotEqual(result.returncode, 0, "an unreachable backend must not look like a pass")
            self.assertTrue(out.is_file(), "the HTTP side must still write its snapshot")
            payload = load_json(out)
            self.assertEqual(payload.get("source"), "http")
            self.assertIsInstance(payload.get("at"), str)
            self.assertRegex(payload.get("at", ""), r"^\d{4}-\d{2}-\d{2}T")
            self.assertIsInstance(payload.get("items"), dict)

    def test_browser_executor_writes_the_documented_snapshot_shape(self):
        """``browser.json`` keeps ``source`` / ``at`` / ``items`` / ``after_reload``."""
        if not node_available():
            record_not_run(
                "browser_executor_snapshot_shape",
                "node is not installed in this environment, so the browser executor could not be run.",
                "Install Node 22+ (the CI 'LingDoc PR gate' job does not provide it yet) and re-run "
                "python -m unittest scripts.ci.test_lingdoc_beta_evidence -v.")
            return
        with tempfile.TemporaryDirectory() as work:
            out = Path(work) / "browser.json"
            result = run_node(str(BROWSER_EXECUTOR), "--url", f"http://127.0.0.1:{free_port()}",
                              "--out", str(out), "--timeout", "20000")
            self.assertNotEqual(result.returncode, 0, "an unreachable front end must not look like a pass")
            self.assertTrue(out.is_file(), "the browser side must still write its snapshot")
            payload = load_json(out)
            self.assertEqual(payload.get("source"), "browser")
            self.assertIsInstance(payload.get("at"), str)
            self.assertRegex(payload.get("at", ""), r"^\d{4}-\d{2}-\d{2}T")
            self.assertIsInstance(payload.get("items"), dict)
            after = payload.get("after_reload")
            self.assertIsInstance(after, dict)
            self.assertIsInstance(after.get("items"), dict)
            self.assertGreater(len(payload.get("not_run", [])), 0,
                               "a browser side that ran nothing must say so")


# --------------------------------------------------------------------------
# 2. The parity report: shape and three-state semantics
# --------------------------------------------------------------------------
def snapshot(source: str, items: dict[str, Any], *, not_run: list[dict[str, str]] | None = None,
             after_reload: dict[str, Any] | None = None) -> dict[str, Any]:
    payload: dict[str, Any] = {"source": source, "at": "2026-10-23T00:00:00Z", "items": items}
    if not_run is not None:
        payload["not_run"] = not_run
    if after_reload is not None:
        payload["after_reload"] = {"at": "2026-10-23T00:01:00Z", "items": after_reload}
    return payload


class ParityReportShapeTests(unittest.TestCase):
    """The parity report keeps its documented shape and its three states."""

    def test_parity_report_shape_has_the_documented_keys(self):
        report = parity.compare_snapshots(
            snapshot("http", {"a": 1}),
            snapshot("browser", {"a": 1}, after_reload={"a": 1}),
            head="deadbeef", environment={})
        for key in ("consistent", "checked", "mismatches", "not_run"):
            self.assertIn(key, report, f"the parity report is missing {key}")
        self.assertIsInstance(report["consistent"], bool)
        self.assertIsInstance(report["checked"], int)
        self.assertIsInstance(report["mismatches"], list)
        self.assertIsInstance(report["not_run"], list)

    def test_parity_consistent_false_requires_mismatches(self):
        """``consistent=false`` always comes with at least one mismatch."""
        report = parity.compare_snapshots(
            snapshot("http", {"a": 1}),
            snapshot("browser", {"a": 2}, after_reload={"a": 2}),
            head="deadbeef", environment={})
        self.assertFalse(report["consistent"])
        self.assertGreater(len(report["mismatches"]), 0)
        for mismatch in report["mismatches"]:
            for key in ("key", "http", "browser", "after_reload"):
                self.assertIn(key, mismatch)

    def test_parity_equal_snapshots_are_consistent(self):
        report = parity.compare_snapshots(
            snapshot("http", {"a": 1, "b": "x"}),
            snapshot("browser", {"a": 1, "b": "x"}, after_reload={"a": 1, "b": "x"}),
            head="deadbeef", environment={})
        self.assertTrue(report["consistent"])
        self.assertEqual(report["mismatches"], [])
        self.assertEqual(report["checked"], 2)
        self.assertEqual(report["status"], "PASS")

    def test_parity_detects_a_mismatch_between_http_and_browser(self):
        """The assertion has to be able to go red (54-Spec Testing Decisions)."""
        report = parity.compare_snapshots(
            snapshot("http", {"browser_snapshot_digest_matches_http": "aaaa"}),
            snapshot("browser", {"browser_snapshot_digest_matches_http": "bbbb"},
                     after_reload={"browser_snapshot_digest_matches_http": "aaaa"}),
            head="deadbeef", environment={})
        self.assertEqual(report["status"], "FAIL")
        self.assertFalse(report["consistent"])
        self.assertEqual([entry["key"] for entry in report["mismatches"]],
                         ["browser_snapshot_digest_matches_http"])

    def test_parity_detects_a_mismatch_between_browser_and_after_reload(self):
        """The third snapshot is not decoration: it can turn the run red."""
        report = parity.compare_snapshots(
            snapshot("http", {"a": 1}),
            snapshot("browser", {"a": 1}, after_reload={"a": 2}),
            head="deadbeef", environment={})
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual([entry["key"] for entry in report["mismatches"]], ["a"])
        self.assertEqual(report["mismatches"][0]["after_reload"], 2)

    def test_parity_status_pass_requires_checked_items(self):
        """A run that compared nothing is NOT RUN, never a pass."""
        report = parity.compare_snapshots(
            snapshot("http", {}),
            snapshot("browser", {}, after_reload={}),
            head="deadbeef", environment={})
        self.assertEqual(report["status"], "NOT RUN")
        self.assertEqual(report["checked"], 0)
        self.assertTrue(report["consistent"])

    def test_parity_not_run_entries_carry_reason_and_unblock(self):
        """Every NOT RUN entry names why and how to unblock it."""
        report = parity.compare_snapshots(
            snapshot("http", {"a": 1}, not_run=[{"item": "b", "reason": "no model", "unblock": "run a model"}]),
            snapshot("browser", {"a": 1}, after_reload={"a": 1}),
            head="deadbeef", environment={})
        self.assertEqual(report["status"], "PASS")
        self.assertEqual(report["checked"], 1)
        self.assertEqual(len(report["not_run"]), 1)
        entry = report["not_run"][0]
        self.assertEqual(entry["item"], "b")
        self.assertTrue(entry["reason"].strip())
        self.assertTrue(entry["unblock"].strip())

    def test_parity_items_missing_from_one_side_become_not_run(self):
        """A key that only one side carries cannot be compared -- and is not a pass."""
        report = parity.compare_snapshots(
            snapshot("http", {"a": 1, "b": 2}),
            snapshot("browser", {"a": 1}, after_reload={"a": 1}),
            head="deadbeef", environment={})
        self.assertEqual(report["status"], "PASS")
        self.assertEqual(report["checked"], 1)
        items = [entry["item"] for entry in report["not_run"]]
        self.assertIn("b", items)
        for entry in report["not_run"]:
            self.assertTrue(entry["reason"].strip())
            self.assertTrue(entry["unblock"].strip())

    def test_parity_cli_writes_the_documented_report(self):
        """The documented invocation produces the documented file."""
        with tempfile.TemporaryDirectory() as work:
            http_path = Path(work) / "http.json"
            browser_path = Path(work) / "browser.json"
            out = Path(work) / "parity.json"
            http_path.write_text(json.dumps(snapshot("http", {"a": 1})), encoding="utf-8")
            browser_path.write_text(json.dumps(
                snapshot("browser", {"a": 1}, after_reload={"a": 1})), encoding="utf-8")
            result = run_python(str(PARITY_EXECUTOR), "--http", str(http_path),
                                "--browser", str(browser_path), "--out", str(out))
            self.assertEqual(result.returncode, 0, result.stderr)
            report = load_json(out)
            for key in ("consistent", "checked", "mismatches", "not_run"):
                self.assertIn(key, report)


# --------------------------------------------------------------------------
# 3. No silent skip
# --------------------------------------------------------------------------
class NoSilentSkipTests(unittest.TestCase):
    """A run that could not happen must say so."""

    def test_orchestrator_without_a_backend_reports_not_run_instead_of_a_pass(self):
        """No real backend => a parity report full of NOT RUN, and a non-zero exit."""
        with tempfile.TemporaryDirectory() as work:
            out = Path(work) / "parity.json"
            result = run_python(str(PARITY_EXECUTOR), "--out", str(out),
                                "--base-url", f"http://127.0.0.1:{free_port()}",
                                "--work-dir", str(Path(work) / "run"))
            self.assertNotEqual(result.returncode, 0, "an unreachable backend must not look like a pass")
            self.assertTrue(out.is_file(), "the orchestrator must still write its parity report")
            report = load_json(out)
            self.assertEqual(report.get("status"), "NOT RUN")
            self.assertEqual(report.get("checked"), 0)
            self.assertEqual(report.get("mismatches"), [])
            self.assertGreater(len(report.get("not_run", [])), 0,
                               "an unreachable backend must produce NOT RUN entries")

    def test_orchestrator_not_run_entries_carry_reason_and_unblock(self):
        with tempfile.TemporaryDirectory() as work:
            out = Path(work) / "parity.json"
            run_python(str(PARITY_EXECUTOR), "--out", str(out),
                       "--base-url", f"http://127.0.0.1:{free_port()}",
                       "--work-dir", str(Path(work) / "run"))
            report = load_json(out)
            for entry in report.get("not_run", []):
                self.assertIn("item", entry)
                self.assertTrue(str(entry.get("reason", "")).strip(),
                                f"{entry.get('item')} has no reason")
                self.assertTrue(str(entry.get("unblock", "")).strip(),
                                f"{entry.get('item')} has no unblock condition")

    def test_orchestrator_reports_a_missing_backend_binary_as_not_run(self):
        """Asking for a backend that does not exist is NOT RUN, not a crash."""
        with tempfile.TemporaryDirectory() as work:
            out = Path(work) / "parity.json"
            result = run_python(str(PARITY_EXECUTOR), "--out", str(out), "--start-backend",
                                "--backend-binary", str(Path(work) / "no-such-binary"),
                                "--work-dir", str(Path(work) / "run"))
            self.assertNotEqual(result.returncode, 0)
            report = load_json(out)
            self.assertEqual(report.get("status"), "NOT RUN")
            self.assertGreater(len(report.get("not_run", [])), 0)


# --------------------------------------------------------------------------
# 4. Redaction
# --------------------------------------------------------------------------
class RedactionGuardTests(unittest.TestCase):
    """No artifact carries a token, a password, a body or an absolute path."""

    def test_redaction_guard_flags_a_token_like_string(self):
        self.assertIn("jwt", find_leaks("Authorization: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghijkl"))
        self.assertIn("bearer-token", find_leaks("header Bearer abcdefghijklmnopqrstuvwxyz"))

    def test_redaction_guard_flags_a_password_field(self):
        self.assertIn("password-field", find_leaks('{"password": "hunter2-secret"}'))
        self.assertIn("password-field", find_leaks("password: Sup3rSecret!"))

    def test_redaction_guard_flags_a_request_or_response_body(self):
        self.assertIn("request-or-response-body",
                      find_leaks('{"response_body": "{\\"data\\": [1,2,3]}"}'))

    def test_redaction_guard_flags_an_unredacted_absolute_path(self):
        self.assertIn("unredacted-absolute-path", find_leaks("log file: C:\\Users\\operator\\run.log"))
        self.assertIn("unredacted-absolute-path", find_leaks("saved at /home/operator/run.log"))

    def test_redaction_guard_accepts_a_redacted_artifact(self):
        clean = json.dumps({"source": "http", "at": "2026-10-23T00:00:00Z",
                            "items": {"browser_export_id_matches_http": "export-7"}})
        self.assertEqual(find_leaks(clean), [])

    def test_the_produced_snapshots_are_redacted(self):
        """The HTTP side's own output must pass its own guard."""
        with tempfile.TemporaryDirectory() as work:
            out = Path(work) / "http.json"
            run_python(str(HTTP_EXECUTOR), "--base-url", f"http://127.0.0.1:{free_port()}",
                       "--out", str(out))
            text = out.read_text(encoding="utf-8")
            self.assertEqual(find_leaks(text), [], "the HTTP snapshot leaks something")

    def test_the_hand_off_file_is_not_evidence(self):
        """The identity hand-off carries a password and must stay out of the artifacts.

        It is written as a dot file next to the snapshot, so it is never
        mistaken for one of the six protocol artifacts.
        """
        with tempfile.TemporaryDirectory() as work:
            out = Path(work) / "http.json"
            context = Path(work) / "run-context.json"
            run_python(str(HTTP_EXECUTOR), "--base-url", f"http://127.0.0.1:{free_port()}",
                       "--out", str(out), "--context-out", str(context))
            if context.is_file():
                self.assertNotIn("http.json", context.name)
                self.assertNotIn(context.name, SIX_ARTIFACTS)


# --------------------------------------------------------------------------
# 5. The six artifacts of the Beta evidence directory
# --------------------------------------------------------------------------
def verdict_strings(payload: Any) -> list[str]:
    """Every status-ish string in a JSON document."""
    found: list[str] = []

    def walk(node: Any, key: str = "") -> None:
        if isinstance(node, dict):
            for name, value in node.items():
                walk(value, str(name))
        elif isinstance(node, list):
            for value in node:
                walk(value, key)
        elif isinstance(node, str) and key.lower() in {
                "status", "result", "verdict", "state", "outcome", "overall_status", "overall"}:
            found.append(node.strip())

    walk(payload)
    return found


PASS_LIKE = {"pass", "passed", "ok", "success", "succeeded", "通过", "合格"}
NOT_PASS_LIKE = {"fail", "failed", "blocked", "not run", "not_run", "notrun", "失败", "已阻断", "未运行"}


def head_values(payload: Any) -> list[str]:
    found: list[str] = []

    def walk(node: Any, key: str = "") -> None:
        if isinstance(node, dict):
            for name, value in node.items():
                walk(value, str(name))
        elif isinstance(node, list):
            for value in node:
                walk(value, key)
        elif isinstance(node, str) and key.lower() in {
                "head", "head_sha", "commit", "commit_sha", "acceptance_head", "git_head", "source_head"}:
            found.append(node.strip())

    walk(payload)
    return found


def run_id_values(payload: Any) -> list[str]:
    found: list[str] = []

    def walk(node: Any, key: str = "") -> None:
        if isinstance(node, dict):
            for name, value in node.items():
                walk(value, str(name))
        elif isinstance(node, list):
            for value in node:
                walk(value, key)
        elif isinstance(node, str) and key.lower() in {"run", "run_id", "run_label", "label"}:
            found.append(node.strip())

    walk(payload)
    return found


class BetaSixArtifactTests(unittest.TestCase):
    """The six artifacts of a Beta evidence directory (protocol §6).

    While the evidence directory does not exist yet, these tests record an
    explicit NOT RUN (with a reason and an unblock condition) instead of
    passing quietly.  Once the directory exists, an incomplete or leaking
    artifact is red.
    """

    @classmethod
    def setUpClass(cls):
        cls.runs = evidence_runs()
        cls.absence_record: dict[str, str] | None = None
        if not cls.runs:
            cls.absence_record = record_not_run(
                "beta_six_artifacts",
                f"The Beta evidence directory {EVIDENCE_ROOT} does not exist yet, so the six artifacts "
                "cannot be checked.",
                "Run scripts/acceptance/beta_evidence.py (53-Spec) to produce "
                "docs/08-本轮实施方案/acceptance/beta-first/<full SHA>/<run id>/ and re-run "
                "python -m unittest scripts.ci.test_lingdoc_beta_evidence -v.")

    def require_runs(self) -> list[tuple[str, str, Path]]:
        """The run directories, or an assertion that the absence was recorded."""
        if not self.runs:
            self.assertIsNotNone(self.absence_record,
                                 "the evidence directory is missing and nothing was recorded")
            assert self.absence_record is not None
            self.assertTrue(self.absence_record["reason"].strip())
            self.assertTrue(self.absence_record["unblock"].strip())
            return []
        return self.runs

    def test_beta_six_artifacts_absence_is_recorded_as_not_run(self):
        """Either the artifacts exist, or the gap is recorded -- never silent."""
        if self.runs:
            self.assertTrue(self.absence_record is None)
            return
        assert self.absence_record is not None
        self.assertTrue(self.absence_record["reason"].strip())
        self.assertTrue(self.absence_record["unblock"].strip())
        matching = [entry for entry in NOT_RUN_RECORDS if entry["item"] == "beta_six_artifacts"]
        self.assertGreater(len(matching), 0, "the missing evidence directory was not recorded")

    def test_beta_six_artifacts_are_present_in_every_run_directory(self):
        """Each run directory holds exactly the six protocol artifacts."""
        for sha, run_id, directory in self.require_runs():
            present = sorted(entry.name for entry in directory.iterdir() if entry.is_file())
            missing = [name for name in SIX_ARTIFACTS if name not in present]
            self.assertEqual(missing, [], f"{sha}/{run_id} is missing {missing}")
            extra = [name for name in present if name not in SIX_ARTIFACTS]
            self.assertEqual(extra, [], f"{sha}/{run_id} holds files outside the protocol's six: {extra}")

    def test_beta_six_artifacts_are_redacted_one_by_one(self):
        """Every one of the six artifacts is checked, not only the report."""
        for sha, run_id, directory in self.require_runs():
            for name in SIX_ARTIFACTS:
                path = directory / name
                if not path.is_file():
                    continue
                leaks = find_leaks(path.read_text(encoding="utf-8", errors="replace"))
                self.assertEqual(leaks, [], f"{sha}/{run_id}/{name} leaks {leaks}")

    def test_runtime_manifest_and_f01_report_agree_on_head_and_run_id(self):
        """The two JSON artifacts must point at the same run."""
        for sha, run_id, directory in self.require_runs():
            manifest_path = directory / "runtime-manifest.json"
            report_path = directory / "f01-report.json"
            if not manifest_path.is_file() or not report_path.is_file():
                continue
            manifest = load_json(manifest_path)
            report = load_json(report_path)
            manifest_heads = head_values(manifest)
            report_heads = head_values(report)
            self.assertGreater(len(manifest_heads), 0,
                               f"{sha}/{run_id}/runtime-manifest.json does not carry the head it was produced against")
            self.assertGreater(len(report_heads), 0,
                               f"{sha}/{run_id}/f01-report.json does not carry the head it was produced against")
            self.assertEqual(set(manifest_heads), set(report_heads),
                             f"{sha}/{run_id}: the manifest and the report disagree about the head")
            for value in set(manifest_heads) | set(report_heads):
                self.assertEqual(value, sha, f"{sha}/{run_id}: the head does not match the directory name")
            manifest_runs = run_id_values(manifest)
            report_runs = run_id_values(report)
            both = set(manifest_runs) | set(report_runs)
            self.assertGreater(len(both), 0,
                               f"{sha}/{run_id}: neither artifact names the run id")
            for value in both:
                self.assertEqual(value, run_id, f"{sha}/{run_id}: the run id does not match the directory name")

    def test_f01_report_not_run_entries_carry_reason_and_unblock(self):
        """The third state is only allowed with a machine-readable unblock."""
        for sha, run_id, directory in self.require_runs():
            report_path = directory / "f01-report.json"
            if not report_path.is_file():
                continue
            report = load_json(report_path)
            entries = report.get("not_run")
            if entries is None:
                continue
            self.assertIsInstance(entries, list)
            for entry in entries:
                self.assertIsInstance(entry, dict)
                self.assertTrue(str(entry.get("reason", "")).strip(),
                                f"{sha}/{run_id}: a not_run entry has no reason")
                self.assertTrue(str(entry.get("unblock", "")).strip(),
                                f"{sha}/{run_id}: a not_run entry has no unblock condition")

    def test_f01_report_overall_result_is_not_an_average(self):
        """A FAIL / BLOCKED / NOT RUN in the main flow forbids a "passed" verdict."""
        for sha, run_id, directory in self.require_runs():
            report_path = directory / "f01-report.json"
            if not report_path.is_file():
                continue
            report = load_json(report_path)
            verdicts = verdict_strings(report)
            has_blocking = any(value.lower() in NOT_PASS_LIKE for value in verdicts)
            not_run = report.get("not_run")
            if isinstance(not_run, list) and not_run:
                has_blocking = True
            if not has_blocking:
                continue
            for value in verdicts:
                self.assertNotIn(value.lower(), PASS_LIKE,
                                 f"{sha}/{run_id}: the report claims a pass although the flow is not clean")

    def test_readme_overall_result_is_not_an_average(self):
        """The README carries the overall result; it may not hide a dirty flow."""
        for sha, run_id, directory in self.require_runs():
            report_path = directory / "f01-report.json"
            readme_path = directory / "README.md"
            if not report_path.is_file() or not readme_path.is_file():
                continue
            report = load_json(report_path)
            verdicts = verdict_strings(report)
            not_run = report.get("not_run")
            blocking = any(value.lower() in NOT_PASS_LIKE for value in verdicts) or \
                (isinstance(not_run, list) and bool(not_run))
            if not blocking:
                continue
            for line in readme_path.read_text(encoding="utf-8", errors="replace").splitlines():
                if not re.search(r"(总结果|总体结果|结论|overall|verdict)", line, re.IGNORECASE):
                    continue
                for word in ("通过", "PASS", "passed"):
                    self.assertNotIn(word, line,
                                     f"{sha}/{run_id}/README.md claims a pass although the flow is not clean: {line}")


class CaptureChainGuardTests(unittest.TestCase):
    """The F01 capture chain has to be visible when it breaks.

    The Beta acceptance runs F01 against a real backend.  Two different things
    can stop a variable from being captured, and they need different unblock
    conditions:

    * the step that captures it did not answer as the contract said -- the
      capture is skipped and the run stops on a *derived* "variable is not
      captured yet" error;
    * the contract never captures the variable at all -- it is an execution
      precondition the environment has to provide (the value in ``setup`` is a
      placeholder).

    A third failure hides both of them: a base URL that does not address the
    contract's API root makes *every* request answer 404, so the first capture
    is skipped and the run stops on the same derived error.

    These tests pin that each of the three is reported as itself.
    """

    OPENAPI = {
        "servers": [{"url": "https://provider.test/api/v1"}],
        "paths": {
            "/templates/{templateId}": {"get": {"operationId": "getTemplate"}},
            "/projects": {"post": {"operationId": "createProject"}},
        },
    }

    @staticmethod
    def specification(capture: bool = True, second_variable: str = "{{template_id}}"):
        steps = [
            {"id": "S1", "operation_id": "getTemplate", "expected_http": 200,
             "request": {"path_params": {"templateId": "t-1"}, "headers": {}, "json": None},
             "capture": ({"template_id": "/data/id"} if capture else {}),
             "checks": []},
            {"id": "S2", "operation_id": "createProject", "expected_http": 201,
             "request": {"path_params": {}, "headers": {}, "json": {"template_id": second_variable}},
             "capture": {}, "checks": []},
        ]
        return spec_from_document({"id": "S", "steps": steps}, "guard specification")

    def test_a_base_url_without_the_api_root_is_refused_before_any_request(self):
        sent = []

        def opener(request, timeout):
            sent.append(request)
            raise AssertionError("the runner must not send a request")

        runner = ScenarioRunner(self.OPENAPI, self.specification(),
                                base_url="https://provider.test", opener=opener)
        with self.assertRaises(WorkflowError) as caught:
            runner.run()
        message = str(caught.exception)
        self.assertIn("does not address the contract's API root", message)
        self.assertIn("/api/v1", message)
        self.assertNotIn("variable is not captured yet", message)
        self.assertEqual(sent, [])

    def test_the_right_base_url_captures_the_variable(self):
        answered = {"count": 0}

        def opener(request, timeout):
            answered["count"] += 1
            if answered["count"] == 1:
                return _SelfTestResponse({"data": {"id": "t-9"}}, status=200)
            return _SelfTestResponse({"data": {"id": "p-1"}}, status=201)

        runner = ScenarioRunner(self.OPENAPI, self.specification(),
                                base_url="https://provider.test/api/v1", opener=opener)
        runner.run()
        self.assertEqual(runner.variables.get("template_id"), "t-9")

    def test_a_capture_that_did_not_happen_says_so_and_keeps_the_real_failure(self):
        def opener(request, timeout):
            return _SelfTestResponse({"error": "not found"}, status=404)

        runner = ScenarioRunner(self.OPENAPI, self.specification(),
                                base_url="https://provider.test/api/v1", opener=opener)
        with self.assertRaises(WorkflowError) as caught:
            runner.run(stop_on_mismatch=False)
        message = str(caught.exception)
        self.assertIn("template_id", message)
        self.assertIn("the contract captures it", message)
        # The real failure stays on record instead of being only a derived error.
        self.assertEqual(len(runner.failures), 1)
        self.assertEqual(runner.failures[0]["step_id"], "S1")
        self.assertEqual(runner.failures[0]["expected"], 200)
        self.assertEqual(runner.failures[0]["actual"], 404)

    def test_removing_the_capture_makes_the_run_report_the_capture_again(self):
        """The negative check: without the capture, the chain breaks on purpose.

        The run must stop, and it must name the variable that is missing -- a
        contract without the capture may not go green.  Which kind of missing
        variable it is follows the contract: with no step capturing
        ``template_id``, the contract asks the environment for it, and the
        message says so instead of pointing at a capture that no longer
        exists.
        """
        answered = {"count": 0}

        def opener(request, timeout):
            answered["count"] += 1
            if answered["count"] == 1:
                return _SelfTestResponse({"data": {"id": "t-9"}}, status=200)
            return _SelfTestResponse({"data": {"id": "p-1"}}, status=201)

        runner = ScenarioRunner(self.OPENAPI, self.specification(capture=False),
                                base_url="https://provider.test/api/v1", opener=opener)
        with self.assertRaises(WorkflowError) as caught:
            runner.run()
        message = str(caught.exception)
        self.assertIn("template_id", message)
        # Without the capture the contract asks the environment for the
        # value, and the run says so before the first request.
        self.assertIn("does not supply", message)
        self.assertIn("no default", message)
        self.assertNotIn("template_id", runner.variables)

    def test_a_variable_the_contract_never_captures_is_called_a_precondition(self):
        def opener(request, timeout):
            return _SelfTestResponse({"data": {"id": "t-9"}}, status=200)

        runner = ScenarioRunner(self.OPENAPI,
                                self.specification(second_variable="{{knowledge_id}}"),
                                base_url="https://provider.test/api/v1", opener=opener)
        with self.assertRaises(WorkflowError) as caught:
            runner.run()
        message = str(caught.exception)
        self.assertIn("knowledge_id", message)
        self.assertIn("does not supply", message)
        self.assertIn("no default", message)
        self.assertNotIn("the contract captures it", message)

    def test_a_step_that_failed_its_status_gets_its_own_conclusion(self):
        """A step that never answered must not vanish from the outcomes."""
        report = {
            "steps": [{"id": "S1", "operation_id": "getTemplate", "http_status": 200}],
            "failures": [{"step_id": "S2", "operation_id": "bindAsset",
                          "kind": "http_status", "expected": 201, "actual": 404}],
            "checks": [],
        }
        outcomes = beta_evidence.check_outcomes(report)
        identifiers = [outcome.id for outcome in outcomes]
        self.assertIn("S2 bindAsset", identifiers)
        failed = next(o for o in outcomes if o.id == "S2 bindAsset")
        self.assertEqual(failed.status, beta_evidence.FAILED)
        self.assertIn("404", failed.evidence)
        self.assertIn("201", failed.evidence)


class UnblockTargetTests(unittest.TestCase):
    """A NOT RUN item has to say who will unblock it.

    The Beta release ticket (#480) owns the items that need the environment
    rather than code: a real model credential, an isolated PostgreSQL, the
    two-chapter material, the Word check sheet.  The unblock condition of
    every such item names that ticket, so the evidence directory says who
    owes the next step instead of only saying what is missing.
    """

    def test_the_browser_and_parity_items_name_the_release_ticket(self):
        with tempfile.TemporaryDirectory(prefix="lingdoc-unblock-") as temporary:
            outcomes, _ = beta_evidence.run_browser_part(
                "http://127.0.0.1:1/api/v1/lingdoc", "", Path(temporary))
        for outcome in outcomes:
            self.assertEqual(outcome.status, beta_evidence.NOT_RUN)
            self.assertIn("#480", outcome.unblock, outcome.id)

    def test_the_word_item_names_the_release_ticket(self):
        outcomes, _, _ = beta_evidence.word_check(None, None, None)
        for outcome in outcomes:
            self.assertEqual(outcome.status, beta_evidence.NOT_RUN)
            self.assertIn("#480", outcome.unblock, outcome.id)

    def test_the_isolated_postgresql_item_names_the_release_ticket(self):
        with tempfile.TemporaryDirectory(prefix="lingdoc-unblock-") as temporary:
            outcomes, _ = beta_evidence.run_restart_part(
                None, REPO_ROOT, Path(temporary), "sqlite", "")
        isolated = next(o for o in outcomes if "隔离 PostgreSQL" in o.id)
        self.assertEqual(isolated.status, beta_evidence.NOT_RUN)
        self.assertIn("#480", isolated.unblock)

    def test_the_material_and_the_model_call_name_the_release_ticket(self):
        source = (REPO_ROOT / "scripts" / "acceptance" / "beta_evidence.py").read_text(
            encoding="utf-8")
        for marker in ("合成材料哈希（协议 §2 第 1 步）",
                       "真实模型调用（协议 §3：真实模型的一次选段改写）"):
            self.assertIn(marker, source)
            index = source.index(marker)
            window = source[index:index + 700]
            self.assertIn("#480", window, marker)


if __name__ == "__main__":
    unittest.main()
