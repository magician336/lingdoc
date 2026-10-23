#!/usr/bin/env python3
"""Produce the Beta acceptance evidence directory (acceptance protocol §6).

One execution, one head, six artifacts
--------------------------------------
The acceptance protocol (``docs/08-本轮实施方案/Beta首版真实验收协议.md`` §6)
asks for the evidence of one run to live in

    docs/08-本轮实施方案/acceptance/beta-first/<full SHA>/<run id>/

and names six artifacts: ``README.md``, ``f01-report.json``,
``runtime-manifest.json``, ``browser-check.md``, ``restart-readback.json``
and ``word-check.md``.  This script produces all six from one run, so that
they belong to one execution instead of being stitched together.

The three states are the protocol's own (§5), spelled as it spells them:
``BLOCKED`` (the main flow cannot continue), ``FAIL`` (the behaviour broke an
assertion) and ``NOT RUN`` (it did not run).  A run that could not happen
says so in ``not_run[]`` with a machine-readable unblock condition; it never
reports a pass it did not earn, and a mock never stands in for a real
dependency.

What this script does not do
----------------------------
* It does not call a model.  The backend under test does that; the executor
  only speaks HTTP to it (53-Spec decision 5).
* It does not judge the Word document's content.  It computes the hashes,
  prints the template and checks that the required fields are filled
  (53-Spec decision 7).
* It does not write tokens, keys, bodies or unredacted absolute paths into
  the artifacts (53-Spec decision 8).

Usage
-----
    python scripts/acceptance/beta_evidence.py --base-url http://127.0.0.1:8080 \
        --frontend-url http://127.0.0.1:5173 --out <evidence dir>

Every part that cannot run is recorded as ``NOT RUN`` with a reason and an
unblock condition; the command still produces the six artifacts.
"""
from __future__ import annotations

import argparse
from dataclasses import dataclass, field
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tempfile
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
EVIDENCE_ROOT = ROOT / "docs/08-本轮实施方案/acceptance/beta-first"
CONTRACT_PATH = ROOT / "docs/08-本轮实施方案/contracts/workflow.json"
RESTART_SCRIPT = ROOT / "scripts/acceptance/lingdoc_full_server_restart.py"
HTTP_EXECUTOR = ROOT / "scripts/acceptance/beta_http_readback.py"
BROWSER_EXECUTOR = ROOT / "scripts/acceptance/beta_browser_cdp.mjs"
PARITY_EXECUTOR = ROOT / "scripts/acceptance/beta_evidence_parity.py"

if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.run_f01 import (  # noqa: E402
    OPENAPI_PATH,
    WORKFLOW_PATH,
    ScenarioRunner,
    WorkflowError,
    redact,
)

# The three states of the protocol §5, plus the word for "this expectation
# held".  The passing word is deliberately not "passed": a report that says
# "passed" next to a NOT RUN entry would be a pass claim the run did not earn.
HELD = "held"
FAILED = "failed"
BLOCKED = "blocked"
NOT_RUN = "not_run"

SIX_ARTIFACTS = ("README.md", "f01-report.json", "runtime-manifest.json",
                 "browser-check.md", "restart-readback.json", "word-check.md")

# The Word check's required fields (protocol §4).  A check that does not
# carry all of them did not happen; the executor checks the fields, never the
# document's content.
WORD_REQUIRED_FIELDS = ("Word 产品版本", "Windows 版本", "原始 DOCX SHA-256",
                        "修改后文件 SHA-256", "检查结果")
WORD_PLACEHOLDERS = ("<待填", "<机器填写", "待填", "TODO", "TBD", "n/a", "N/A")

# The leak shapes the guard test enforces (53-Spec decision 8).  The producer
# checks its own output too: a guard that only runs in CI is a guard that
# runs after the artifact is already in the repository.
LEAK_PATTERNS = (
    ("jwt", re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}")),
    ("bearer-token", re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._\-]{16,}")),
    ("token-assignment", re.compile(
        r"(?i)\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|secret|token|jwt)\b\s*[=:]\s*"
        r"[\"']?[A-Za-z0-9._\-]{12,}")),
    ("password-field", re.compile(
        r"(?i)\b(?:password|passwd|pwd|口令|密码)\b[\"']?\s*[=:：]\s*[\"']?([^\s\"',}]{4,})")),
    ("request-or-response-body", re.compile(
        r"(?i)\b(?:request_body|response_body|request_payload|response_payload|raw_request|raw_response|"
        r"request_text|response_text|body_raw)\b[\"']?\s*[=:：]\s*(.+)")),
    ("absolute-home-path", re.compile(r"(?i)(?:[A-Z]:[\\/]Users[\\/][^\\\/\s\"']+)|(?:/(?:home|Users)/[^/\s\"']+)")),
)


class EvidenceError(RuntimeError):
    """An error the operator has to see; never a silent pass."""


@dataclass
class Outcome:
    """One result of one part of the run.

    ``status`` is one of ``held`` / ``failed`` / ``blocked`` / ``not_run``.
    A ``not_run`` outcome always carries a reason and an unblock condition:
    "did not happen" is only allowed to exist together with "how to make it
    happen".
    """

    id: str
    status: str
    evidence: str = ""
    reason: str = ""
    unblock: str = ""

    def as_result(self) -> dict[str, str]:
        return {"id": self.id, "status": self.status, "evidence": self.evidence}

    def as_not_run(self) -> dict[str, str]:
        return {"id": self.id, "reason": self.reason, "unblock": self.unblock}


def now_iso() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def git_output(root: Path, *arguments: str) -> str:
    """One git answer, or an empty string when git cannot answer."""
    try:
        result = subprocess.run(["git", "-C", str(root), *arguments], capture_output=True,
                                text=True, encoding="utf-8", errors="replace", check=False,
                                timeout=30)
    except (OSError, subprocess.TimeoutExpired):
        return ""
    return result.stdout.strip() if result.returncode == 0 else ""


def git_head(root: Path) -> str:
    """The full commit SHA the run is bound to."""
    return git_output(root, "rev-parse", "HEAD")


def git_base(root: Path) -> str:
    """The base the run is compared against, when the repository knows one."""
    for candidate in ("origin/main", "main", "master"):
        merge_base = git_output(root, "merge-base", "HEAD", candidate)
        if merge_base:
            return merge_base
    return ""


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def short(value: Any, limit: int = 160) -> str:
    """A short, single-line rendering of a value, for an evidence sentence."""
    text = value if isinstance(value, str) else json.dumps(value, ensure_ascii=False, sort_keys=True)
    text = " ".join(text.split())
    return text if len(text) <= limit else text[:limit] + "…"


def relative_to_root(path: Path) -> str:
    """A repository-relative path: an absolute path would name the operator."""
    try:
        return str(path.resolve().relative_to(ROOT)).replace("\\", "/")
    except ValueError:
        return path.name


def find_leaks(text: str) -> list[str]:
    """Every leak shape found in one artifact."""
    found: list[str] = []
    for name, pattern in LEAK_PATTERNS:
        match = pattern.search(text)
        if not match:
            continue
        if name == "password-field" and match.group(1).strip().lower() in {
                "", "***", "n/a", "na", "not recorded", "未记录", "不写入", "不进证据"}:
            continue
        if name == "request-or-response-body" and match.group(1).strip().strip("\"'").strip() in {
                "{}", "[]", "not recorded", "未记录"}:
            continue
        found.append(name)
    return found


def run_command(command: list[str], *, cwd: Path = ROOT, timeout: float = 900.0) -> subprocess.CompletedProcess[str]:
    """One child process, with a UTF-8 environment so its Chinese output survives."""
    environment = os.environ.copy()
    environment["PYTHONIOENCODING"] = "utf-8"
    environment["PYTHONUTF8"] = "1"
    return subprocess.run(command, cwd=str(cwd), capture_output=True, text=True, encoding="utf-8",
                          errors="replace", env=environment, check=False, timeout=timeout)


# ---------------------------------------------------------------------------
# The F01 sequence
# ---------------------------------------------------------------------------
def run_f01(base_url: str, token: str | None, identities: dict[str, str]) -> tuple[list[Outcome], dict[str, Any]]:
    """Drive the F01 contract against the provider and turn it into outcomes.

    The runner is the one in ``scripts/lingdoc_mock/run_f01.py``: the steps and
    the assertions live in the contract, and this script only asks for the
    result.  A provider that cannot be reached is BLOCKED, not a failure of
    the assertions and not a pass either.
    """
    outcomes: list[Outcome] = []
    if not base_url:
        outcomes.append(Outcome(
            id="F01 契约序列（24 个节点）", status=BLOCKED,
            evidence="没有给出 --base-url，本轮没有对任何提供方执行。",
            reason="未提供真实后端地址，F01 契约序列没有执行。",
            unblock="用 --base-url <真实后端地址> 重跑本脚本。"))
        return outcomes, {}
    try:
        runner = ScenarioRunner.from_files(
            OPENAPI_PATH, WORKFLOW_PATH, base_url=base_url, token=token,
            identities=identities)
    except WorkflowError as error:
        outcomes.append(Outcome(
            id="F01 契约序列（24 个节点）", status=BLOCKED,
            evidence=f"执行器拒绝了这轮配置：{error}",
            reason=f"执行器在发出第一个请求前就拒绝了本轮配置：{error}",
            unblock="按错误信息补齐 --token / --identity 后重跑本脚本。"))
        return outcomes, {}
    try:
        runner.run(stop_on_mismatch=False)
    except WorkflowError as error:
        outcomes.append(Outcome(
            id="F01 契约序列（24 个节点）", status=BLOCKED,
            evidence=f"序列中断：{error}",
            reason=f"主流程缺依赖或无法继续：{error}",
            unblock="让真实后端可达（隔离 PostgreSQL + 迁移 + 启动）后重跑本脚本。"))
        report = runner.report("failed")
        outcomes.extend(check_outcomes(report))
        return outcomes, report
    report = runner.report("completed")
    outcomes.extend(check_outcomes(report))
    return outcomes, report


def check_outcomes(report: dict[str, Any]) -> list[Outcome]:
    """Turn the runner's record into one outcome per step and per check."""
    outcomes: list[Outcome] = []
    failures = {entry.get("step_id"): entry for entry in report.get("failures", [])}
    for step in report.get("steps", []):
        step_id = step.get("id", "")
        failure = failures.get(step_id)
        outcomes.append(Outcome(
            id=f"{step_id} {step.get('operation_id', '')}".strip(),
            status=FAILED if failure else HELD,
            evidence=(f"契约声明 HTTP {failure.get('expected')}，实际 {failure.get('actual')}。"
                      if failure else f"HTTP {step.get('http_status')}，与契约声明一致。")))
    for check in report.get("checks", []):
        outcomes.append(Outcome(
            id=f"{check.get('step_id')} {check.get('operator')} {check.get('path')}",
            status=HELD if check.get("status") == "passed" else FAILED,
            evidence=(f"{check.get('intent')}｜期望 {short(check.get('expected'))}｜"
                      f"实际 {short(check.get('actual'))}")))
    return outcomes


# ---------------------------------------------------------------------------
# The browser side (54-Spec's three-way comparison)
# ---------------------------------------------------------------------------
def run_browser_part(base_url: str, frontend_url: str, work_dir: Path) -> tuple[list[Outcome], dict[str, Any]]:
    """Run the HTTP side, the browser side and the comparison, if they can run."""
    outcomes: list[Outcome] = []
    summary: dict[str, Any] = {"http": None, "browser": None, "parity": None}
    if not base_url:
        outcomes.append(Outcome(
            id="浏览器对偶：HTTP 侧快照", status=NOT_RUN,
            reason="没有给出 --base-url，HTTP 侧没有对着真实后端跑。",
            unblock="用 --base-url <真实后端地址> 重跑本脚本。"))
        return outcomes, summary
    if not frontend_url:
        outcomes.append(Outcome(
            id="浏览器对偶：浏览器侧快照", status=NOT_RUN,
            reason="没有给出 --frontend-url，浏览器侧没有对着真实前端跑。",
            unblock="起真实前端（Vite dev server）并用 --frontend-url <地址> 重跑本脚本。"))
    else:
        context_path = work_dir / "run-context.json"
        http_out = work_dir / "http.json"
        browser_out = work_dir / "browser.json"
        http_result = run_command([sys.executable, str(HTTP_EXECUTOR), "--base-url", base_url,
                                    "--out", str(http_out), "--context-out", str(context_path)])
        summary["http"] = read_json(http_out)
        if http_result.returncode != 0 or summary["http"] is None:
            outcomes.append(Outcome(
                id="浏览器对偶：HTTP 侧快照", status=NOT_RUN,
                reason=f"HTTP 侧没有跑完（退出码 {http_result.returncode}）。",
                unblock="让真实后端可达后重跑 `python scripts/acceptance/beta_http_readback.py "
                        "--base-url <地址> --out <http.json>`。"))
        else:
            outcomes.append(Outcome(
                id="浏览器对偶：HTTP 侧快照", status=HELD,
                evidence=f"HTTP 侧在 {summary['http'].get('at')} 读回了 {len(summary['http'].get('items', {}))} 个比对项。"))
        if frontend_url:
            browser_result = run_command(["node", str(BROWSER_EXECUTOR), "--url", frontend_url,
                                          "--out", str(browser_out), "--context", str(context_path)])
            summary["browser"] = read_json(browser_out)
            if browser_result.returncode != 0 or summary["browser"] is None:
                outcomes.append(Outcome(
                    id="浏览器对偶：浏览器侧快照", status=NOT_RUN,
                    reason=f"浏览器侧没有跑完（退出码 {browser_result.returncode}）。",
                    unblock="起真实前端与 CDP 浏览器后重跑 `node scripts/acceptance/beta_browser_cdp.mjs "
                            "--url <地址> --out <browser.json>`。"))
            else:
                outcomes.append(Outcome(
                    id="浏览器对偶：浏览器侧快照", status=HELD,
                    evidence=f"浏览器侧在 {summary['browser'].get('at')} 读回了 "
                             f"{len(summary['browser'].get('items', {}))} 个比对项。"))
    parity_out = work_dir / "parity.json"
    if summary.get("http") and summary.get("browser"):
        parity_result = run_command([sys.executable, str(PARITY_EXECUTOR), "--http", str(work_dir / "http.json"),
                                      "--browser", str(work_dir / "browser.json"), "--out", str(parity_out)])
        summary["parity"] = read_json(parity_out)
        if parity_result.returncode != 0 or summary["parity"] is None:
            outcomes.append(Outcome(
                id="三方一致性：浏览器 = HTTP = 刷新后读回", status=NOT_RUN,
                reason=f"三方比对没有跑完（退出码 {parity_result.returncode}）。",
                unblock="重跑 `python scripts/acceptance/beta_evidence_parity.py --http <http.json> "
                        "--browser <browser.json> --out <parity.json>`。"))
        else:
            parity = summary["parity"]
            consistent = bool(parity.get("consistent"))
            outcomes.append(Outcome(
                id="三方一致性：浏览器 = HTTP = 刷新后读回",
                status=HELD if consistent else FAILED,
                evidence=(f"比对了 {parity.get('checked')} 项，不一致 {len(parity.get('mismatches', []))} 项，"
                          f"结论 {parity.get('status', '未知')}。")))
    else:
        outcomes.append(Outcome(
            id="三方一致性：浏览器 = HTTP = 刷新后读回", status=NOT_RUN,
            reason="三方比对需要两侧快照；有一侧没有跑成，比对没有发生。",
            unblock="让 HTTP 侧与浏览器侧都跑成后重跑本脚本。"))
    return outcomes, summary


# ---------------------------------------------------------------------------
# The restart read-back
# ---------------------------------------------------------------------------
def run_restart_part(server_binary: Path | None, repo_root: Path, work_dir: Path,
                     db_driver: str, db_dsn: str) -> tuple[list[Outcome], dict[str, Any]]:
    """Restart the real server and read the project back."""
    outcomes: list[Outcome] = []
    driver = db_driver or "sqlite"
    summary: dict[str, Any] = {"driver": driver, "script": relative_to_root(RESTART_SCRIPT)}
    if driver != "postgres":
        # The protocol asks for an isolated PostgreSQL; a SQLite run is a local
        # diagnosis.  It is worth having, and it is not the protocol's gate.
        outcomes.append(Outcome(
            id="重启后读回：协议 §1 的隔离 PostgreSQL", status=NOT_RUN,
            reason=f"本轮的后端是 {driver}；协议 §1 要求隔离 PostgreSQL，那一轮没有发生。",
            unblock="用 --db-driver postgres --db-dsn <隔离库 DSN> 重跑本脚本。"))
    if server_binary is None or not Path(server_binary).is_file():
        outcomes.append(Outcome(
            id="重启后读回：同一个项目、访问令牌与持久化", status=NOT_RUN,
            reason="没有给出可用的 --server-binary，重启没有发生。",
            unblock="用 `make build` 产出服务端二进制，并以 --server-binary <路径> 重跑本脚本。"))
        return outcomes, summary
    command = [sys.executable, str(RESTART_SCRIPT), "--binary", str(server_binary),
               "--repo-root", str(repo_root), "--output-dir", str(work_dir / "restart")]
    if db_driver:
        command += ["--db-driver", db_driver]
    if db_dsn:
        command += ["--db-dsn", db_dsn]
    result = run_command(command)
    summary["exit_code"] = result.returncode
    summary["output"] = (result.stdout or "").strip()[-2000:]
    if result.returncode != 0:
        outcomes.append(Outcome(
            id="重启后读回：同一个项目、访问令牌与持久化", status=FAILED,
            evidence=f"重启脚本以退出码 {result.returncode} 结束：{summary['output']}",
            reason=f"重启后读回失败：{summary['output']}",
            unblock="按重启脚本的输出修正环境（数据库、迁移、端口）后重跑本脚本。"))
        return outcomes, summary
    outcomes.append(Outcome(
        id="重启后读回：同一个项目、访问令牌与持久化", status=HELD,
        evidence=f"后端 {driver}；{summary['output'] or '重启后读回了同一个项目。'}"))
    return outcomes, summary


# ---------------------------------------------------------------------------
# The Word check (protocol §4)
# ---------------------------------------------------------------------------
def word_template(docx_hash: str, modified_hash: str, windows_version: str) -> str:
    """The form the executor hands to the operator."""
    return "\n".join([
        "# Word 人工检查（协议 §4）",
        "",
        "这一份由执行者填写。执行器只负责算哈希、给模板和校验必填字段齐全，",
        "**不判定文档内容**——程序库 roundtrip 不能替代这一检查。",
        "",
        "| 字段 | 值 |",
        "| --- | --- |",
        f"| Word 产品版本 | <待填：例如 Microsoft Word 16.0.14326.20106> |",
        f"| Windows 版本 | {windows_version or '<机器填写：未取到>'} |",
        f"| 原始 DOCX SHA-256 | {docx_hash or '<机器填写：本轮没有拿到下载的文件>'} |",
        f"| 修改后文件 SHA-256 | {modified_hash or '<待填：保存后由执行器计算，或由执行者填写>'} |",
        "| 检查结果 | <待填：通过 / 不通过> |",
        "| 检查人 | <待填> |",
        "| 检查时间 | <待填> |",
        "",
        "## 检查清单",
        "",
        "- [ ] 打开下载的文件，改一处正文，保存，关闭并重新打开",
        "- [ ] 修改保留",
        "- [ ] 正文、标题层级、引用和基本格式仍可用",
        "- [ ] 附录列出 review-1 原文、保留原因及内部演示标识",
        "- [ ] 摘要 hash 与下载真实字节相等（这一条由 F01-23 的 hash_equals 机器核对）",
        "",
        "## 本轮怎么算的",
        "",
        f"- 原始 DOCX 的 SHA-256 由执行器对本轮下载的文件计算：{docx_hash or '本轮没有拿到文件'}",
        f"- 修改后文件的 SHA-256：{modified_hash or '本轮没有拿到修改后的文件'}",
        f"- Windows 版本：{windows_version or '本机没有取到'}",
        "",
    ])


def word_check(word_check_path: Path | None, docx_path: Path | None,
               modified_path: Path | None) -> tuple[list[Outcome], str, dict[str, str]]:
    """Fill the Word check form, or judge the one the operator filled in."""
    windows_version = platform.version()
    docx_hash = sha256_file(Path(docx_path)) if docx_path and Path(docx_path).is_file() else ""
    modified_hash = sha256_file(Path(modified_path)) if modified_path and Path(modified_path).is_file() else ""
    form = word_template(docx_hash, modified_hash, windows_version)
    metadata = {"docx_sha256": docx_hash, "modified_sha256": modified_hash,
                "windows_version": windows_version}
    if word_check_path is None or not Path(word_check_path).is_file():
        return [Outcome(
            id="Word 人工检查（协议 §4）", status=NOT_RUN,
            reason="执行者还没有填写 Word 检查表；协议 §4 的这一段是人工的。",
            unblock=f"填写 {relative_to_root(Path(word_check_path)) if word_check_path else 'word-check.md'} 里的五个必填字段"
                    "（Word 产品版本 / Windows 版本 / 原始 DOCX SHA-256 / 修改后文件 SHA-256 / 检查结果），"
                    "并用 --word-check <该文件> 重跑本脚本。")], form, metadata
    text = Path(word_check_path).read_text(encoding="utf-8", errors="replace")
    missing: list[str] = []
    values: dict[str, str] = {}
    for field_name in WORD_REQUIRED_FIELDS:
        value = ""
        for line in text.splitlines():
            if field_name in line:
                value = line.split("|", 2)[-1] if line.count("|") >= 2 else line.split(":", 1)[-1]
                value = value.strip().strip("|").strip()
                if value.lower() in {placeholder.lower() for placeholder in WORD_PLACEHOLDERS} \
                        or value.startswith("<待填") or value.startswith("<机器填写"):
                    value = ""
                break
        values[field_name] = value
        if not value:
            missing.append(field_name)
    if missing:
        return [Outcome(
            id="Word 人工检查（协议 §4）", status=NOT_RUN,
            reason=f"执行者填的 Word 检查表缺必填字段：{'、'.join(missing)}。",
            unblock=f"补齐 {'、'.join(missing)} 后用 --word-check <该文件> 重跑本脚本。")], form, metadata
    verdict = values["检查结果"]
    passed = "不通过" not in verdict and "fail" not in verdict.lower()
    outcomes = [Outcome(
        id="Word 人工检查（协议 §4）", status=HELD if passed else FAILED,
        evidence=f"Word {values['Word 产品版本']}；Windows {values['Windows 版本']}；"
                 f"原始 DOCX SHA-256 {values['原始 DOCX SHA-256']}；"
                 f"修改后 SHA-256 {values['修改后文件 SHA-256']}；执行者结论：{verdict}")]
    return outcomes, text, metadata


# ---------------------------------------------------------------------------
# The model call ledger (55-Spec's table)
# ---------------------------------------------------------------------------
LEDGER_COLUMNS = ("id", "tenant_id", "project_id", "kind", "provider", "model_id", "run_mode",
                  "parameters_json", "input_tokens", "output_tokens", "reserved_nano_usd",
                  "actual_nano_usd", "duration_ms", "status", "error_code", "source_kind",
                  "source_id", "switched_from_model_id", "switch_reason", "affected_steps_json",
                  "created_at")


def read_ledger(dsn: str, sqlite_path: str) -> tuple[list[dict[str, Any]], str]:
    """Read the model call ledger, or say why it could not be read."""
    if sqlite_path:
        try:
            import sqlite3
        except ImportError as error:  # pragma: no cover - the stdlib always has it
            return [], f"sqlite3 不可用：{error}"
        path = Path(sqlite_path)
        if not path.is_file():
            return [], f"账本数据库不存在：{relative_to_root(path)}"
        try:
            with sqlite3.connect(str(path)) as connection:
                connection.row_factory = sqlite3.Row
                rows = connection.execute(
                    f"SELECT {', '.join(LEDGER_COLUMNS)} FROM lingdoc_model_call_ledger "
                    "ORDER BY created_at").fetchall()
        except Exception as error:  # sqlite3 raises a family of errors
            return [], f"读账本失败：{type(error).__name__}: {error}"
        return [dict(row) for row in rows], ""
    if not dsn:
        return [], "没有给出账本位置（--ledger-dsn 或 --ledger-sqlite）。"
    psql = shutil.which("psql")
    if not psql:
        return [], "本机没有 psql，也没有 --ledger-sqlite 可读。"
    parts = parse_dsn(dsn)
    command = [psql, "-X", "-A", "-t", "-F", "\t", "-c",
               f"SELECT {', '.join(LEDGER_COLUMNS)} FROM lingdoc_model_call_ledger ORDER BY created_at"]
    environment = os.environ.copy()
    if parts.get("host"):
        environment["PGHOST"] = parts["host"]
    if parts.get("port"):
        environment["PGPORT"] = str(parts["port"])
    if parts.get("user"):
        environment["PGUSER"] = parts["user"]
    if parts.get("dbname"):
        environment["PGDATABASE"] = parts["dbname"]
    if parts.get("password"):
        environment["PGPASSWORD"] = parts["password"]
    try:
        result = subprocess.run(command, capture_output=True, text=True, encoding="utf-8",
                                errors="replace", env=environment, check=False, timeout=60)
    except (OSError, subprocess.TimeoutExpired) as error:
        return [], f"读账本失败：{type(error).__name__}: {error}"
    if result.returncode != 0:
        return [], f"读账本失败：{(result.stderr or '').strip()[:300]}"
    rows: list[dict[str, Any]] = []
    for line in (result.stdout or "").splitlines():
        if not line.strip():
            continue
        values = line.split("\t")
        if len(values) != len(LEDGER_COLUMNS):
            continue
        rows.append(dict(zip(LEDGER_COLUMNS, values)))
    return rows, ""


def parse_dsn(dsn: str) -> dict[str, str]:
    """Read the parts of a PostgreSQL DSN, keeping the password out of the artifacts."""
    parts: dict[str, str] = {}
    if "://" in dsn:
        remainder = dsn.split("://", 1)[1]
        authority, _, path = remainder.partition("/")
        if "@" in authority:
            credentials, _, hostport = authority.rpartition("@")
            user, _, password = credentials.partition(":")
            parts["user"] = user
            parts["password"] = password
        else:
            hostport = authority
        host, _, port = hostport.partition(":")
        parts["host"] = host
        if port:
            parts["port"] = port
        parts["dbname"] = path.split("?", 1)[0]
        return parts
    for item in dsn.split():
        key, _, value = item.partition("=")
        parts[key.strip()] = value.strip()
    return parts


def redact_dsn(dsn: str) -> str:
    """The shape of a DSN without its password."""
    if not dsn:
        return ""
    parts = parse_dsn(dsn)
    if "://" in dsn:
        host = parts.get("host", "")
        port = f":{parts['port']}" if parts.get("port") else ""
        user = f"{parts.get('user', '')}@" if parts.get("user") else ""
        return f"postgres://{user}{host}{port}/{parts.get('dbname', '')}"
    return " ".join(f"{key}={'（已脱敏）' if key == 'password' else value}"
                    for key, value in sorted(parts.items()))


# ---------------------------------------------------------------------------
# The artifacts
# ---------------------------------------------------------------------------
def verification_report(mode: str, note: str, head: str, base: str, head_note: str, base_note: str,
                       outcomes: list[Outcome], known_limits: list[dict[str, str]]) -> dict[str, Any]:
    """The repository's verification-report shape (53-Spec decision 9).

    ``passed[]`` carries every executed conclusion of the run -- ``held``,
    ``failed`` or ``blocked``; ``not_run[]`` carries what did not run, each with
    a reason and a machine-readable unblock condition.  The overall result is
    decided by the main flow, not by an average over the array.
    """
    return {
        "mode": mode,
        "note": note,
        "head": head,
        "base": base,
        "head_note": head_note,
        "base_note": base_note,
        "passed": [outcome.as_result() for outcome in outcomes if outcome.status != NOT_RUN],
        "not_run": [outcome.as_not_run() for outcome in outcomes if outcome.status == NOT_RUN],
        "known_limits": known_limits,
    }


def overall_result(outcomes: list[Outcome]) -> str:
    """The run's overall result, decided by the main flow (protocol §5 and §6).

    A single failed assertion is a FAIL; a main flow that could not continue
    is BLOCKED; something that did not run is NOT RUN.  Only a run without any
    of those may say it passed.
    """
    statuses = {outcome.status for outcome in outcomes}
    if FAILED in statuses:
        return "FAIL"
    if BLOCKED in statuses:
        return "BLOCKED"
    if NOT_RUN in statuses:
        return "NOT RUN"
    if not outcomes:
        return "NOT RUN"
    return "PASS"


def render_readme(executor: str, head: str, base: str, run_id: str, result: str,
                  outcomes: list[Outcome], commands: list[str], environment: dict[str, str],
                  scope: list[str], known_limits: list[dict[str, str]],
                  manifest: dict[str, Any]) -> str:
    """The README of the evidence directory (protocol §6)."""
    lines = [
        "# Beta 首版真实验收 · 证据目录",
        "",
        "| 项 | 值 |",
        "| --- | --- |",
        f"| 执行者 | {executor} |",
        f"| 代码 head | `{head}` |",
        f"| 基线 | `{base or '（本机没有可比的基线）'}` |",
        f"| 运行标识 | `{run_id}` |",
        f"| 生成时间 | {manifest.get('generated_at')} |",
        f"| 总结果 | **{result}** |",
        "",
        "## 环境与依赖版本",
        "",
        "| 项 | 值 |",
        "| --- | --- |",
    ]
    for name, value in environment.items():
        lines.append(f"| {name} | {value} |")
    lines += ["", "## 实际命令", "", "```"]
    lines += commands
    lines += ["```", "", "## 范围", ""]
    lines += [f"- {item}" for item in scope]
    lines += ["", "## 结论项", "", "| 结论项 | 状态 | 证据 |", "| --- | --- | --- |"]
    for outcome in outcomes:
        evidence = outcome.evidence or outcome.reason
        lines.append(f"| {outcome.id} | {outcome.status} | {evidence} |")
    not_run = [outcome for outcome in outcomes if outcome.status == NOT_RUN]
    lines += ["", "## 未运行项", ""]
    if not_run:
        lines += ["| 未运行项 | 原因 | 解除条件 |", "| --- | --- | --- |"]
        for outcome in not_run:
            lines.append(f"| {outcome.id} | {outcome.reason} | {outcome.unblock} |")
    else:
        lines.append("（本轮没有未运行项。）")
    if known_limits:
        lines += ["", "## 已知限制", ""]
        for limit in known_limits:
            unblock = limit.get("unblock")
            suffix = f"（解除条件：{unblock}）" if unblock else ""
            lines.append(f"- **{limit.get('id')}**：{limit.get('detail')}{suffix}")
    lines += ["",
              "## 这份目录说明什么",
              "",
              "- 六件产物来自同一次执行、同一个 head、同一个运行标识。",
              "- 状态三态按协议 §5：BLOCKED（主流程缺依赖或无法继续）、FAIL（业务行为违反断言）、"
              "NOT RUN（没有执行）。总结果由主流程决定，不用平均覆盖。",
              "- 产物已脱敏：令牌、密钥、请求体与响应体不写入本目录（协议 §6）。",
              "- 协议制定完成不等于验收已经执行或通过。",
              ""]
    return "\n".join(lines)


def render_browser_check(summary: dict[str, Any], outcomes: list[Outcome]) -> str:
    """The browser evidence: the three-way comparison, in prose a reader can check."""
    lines = ["# 浏览器检查（协议 §2 第 4 步）", "",
             "页面证据：浏览器侧在真实前端上做了什么、HTTP 侧读回了什么、"
             "刷新之后读回了什么，以及三方是否一致。", ""]
    http_snapshot = summary.get("http") or {}
    browser_snapshot = summary.get("browser") or {}
    parity = summary.get("parity") or {}
    lines += ["## 三方快照", "",
              "| 快照 | 时刻 | 比对项数 |", "| --- | --- | --- |",
              f"| HTTP 侧 | {http_snapshot.get('at', '（没有跑）')} | {len(http_snapshot.get('items', {}) or {})} |",
              f"| 浏览器侧 | {browser_snapshot.get('at', '（没有跑）')} | "
              f"{len(browser_snapshot.get('items', {}) or {})} |",
              f"| 刷新后读回 | {(browser_snapshot.get('after_reload') or {}).get('at', '（没有跑）')} | "
              f"{len(((browser_snapshot.get('after_reload') or {}).get('items')) or {})} |", ""]
    lines += ["## 三方一致性结论", ""]
    if parity:
        lines.append(f"- 一致：**{parity.get('consistent')}**（比对了 {parity.get('checked')} 项，"
                     f"不一致 {len(parity.get('mismatches', []) or [])} 项，结论 {parity.get('status', '未知')}）。")
        for mismatch in (parity.get("mismatches") or [])[:20]:
            lines.append(f"  - 不一致：`{mismatch.get('key')}`｜HTTP {short(mismatch.get('http'))}｜"
                         f"浏览器 {short(mismatch.get('browser'))}｜刷新后 {short(mismatch.get('after_reload'))}")
    else:
        lines.append("- 一致：**没有比对发生**（两侧快照至少有一侧没有跑成）。")
    lines += ["", "## 本节的结论项", "", "| 结论项 | 状态 | 证据 |", "| --- | --- | --- |"]
    for outcome in outcomes:
        lines.append(f"| {outcome.id} | {outcome.status} | {outcome.evidence or outcome.reason} |")
    lines += ["", "## 未运行项", ""]
    not_run = [outcome for outcome in outcomes if outcome.status == NOT_RUN]
    if not_run:
        for outcome in not_run:
            lines.append(f"- **{outcome.id}**：{outcome.reason} 解除条件：{outcome.unblock}")
    else:
        lines.append("（本节没有未运行项。）")
    lines.append("")
    return "\n".join(lines)


def read_json(path: Path) -> dict[str, Any] | None:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None
    return payload if isinstance(payload, dict) else None


def write_text(path: Path, text: str) -> None:
    path.write_text(text, encoding="utf-8", newline="\n")


def write_json(path: Path, payload: Any) -> None:
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


# ---------------------------------------------------------------------------
# The run
# ---------------------------------------------------------------------------
def parse_arguments(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default=os.environ.get("LINGDOC_BASE_URL", ""),
                        help="真实后端地址；F01 契约序列与 HTTP 侧都对它说话")
    parser.add_argument("--frontend-url", default=os.environ.get("LINGDOC_FRONTEND_URL", ""),
                        help="真实前端地址；浏览器侧对它说话")
    parser.add_argument("--out", type=Path, default=None,
                        help="证据目录；默认是协议 §6 的位置（<head>/<运行标识>）")
    parser.add_argument("--run-id", default="", help="运行标识；默认是 UTC 时间戳")
    parser.add_argument("--head", default="", help="本轮绑定的 head；默认取当前 HEAD")
    parser.add_argument("--token", default=os.environ.get("LINGDOC_TEST_TOKEN", ""),
                        help="短期测试令牌")
    parser.add_argument("--identity", action="append", default=[], metavar="NAME=TOKEN",
                        help="契约里某个 actor 的凭据（可重复）")
    parser.add_argument("--executor", default=os.environ.get("LINGDOC_EXECUTOR", ""),
                        help="执行者名字，写进 README")
    parser.add_argument("--word-check", type=Path, default=None, help="执行者填好的 Word 检查表")
    parser.add_argument("--docx", type=Path, default=None, help="本轮下载的 DOCX（算哈希）")
    parser.add_argument("--modified-docx", type=Path, default=None, help="修改后的 DOCX（算哈希）")
    parser.add_argument("--ledger-dsn", default=os.environ.get("LINGDOC_LEDGER_DSN", ""),
                        help="模型调用账本的 PostgreSQL DSN")
    parser.add_argument("--ledger-sqlite", default=os.environ.get("LINGDOC_LEDGER_SQLITE", ""),
                        help="模型调用账本的 SQLite 文件")
    parser.add_argument("--server-binary", type=Path, default=None, help="服务端二进制（重启读回用）")
    parser.add_argument("--repo-root", type=Path, default=ROOT, help="仓库根目录")
    parser.add_argument("--db-driver", default=os.environ.get("LINGDOC_DB_DRIVER", ""),
                        help="本轮用的数据库驱动（sqlite / postgres）")
    parser.add_argument("--db-dsn", default=os.environ.get("LINGDOC_DB_DSN", ""), help="本轮用的 DSN")
    parser.add_argument("--material", action="append", default=[], metavar="NAME=SHA256",
                        help="合成材料的哈希（可重复）")
    parser.add_argument("--work-dir", type=Path, default=None,
                        help="中间件目录；默认是系统临时目录（不落在证据目录里）")
    return parser.parse_args(argv)


def parse_identity(values: list[str]) -> dict[str, str]:
    identities: dict[str, str] = {}
    for value in values:
        name, separator, token = value.partition("=")
        if not separator or not name.strip() or not token.strip():
            raise EvidenceError(f"--identity takes name=token, got: {value}")
        identities[name.strip()] = token.strip()
    return identities


def parse_material(values: list[str]) -> list[dict[str, str]]:
    materials: list[dict[str, str]] = []
    for value in values:
        name, separator, digest = value.partition("=")
        if not separator or not name.strip() or not digest.strip():
            raise EvidenceError(f"--material takes name=sha256, got: {value}")
        materials.append({"name": name.strip(), "sha256": digest.strip()})
    return materials


def main(argv: list[str] | None = None) -> int:
    # The report names its states in Chinese; a Windows console defaults to a
    # narrow code page and would print mojibake instead of the real words.
    for stream in (sys.stdout, sys.stderr):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure is not None:
            reconfigure(encoding="utf-8", errors="replace")
    arguments = parse_arguments(argv)
    head = arguments.head or git_head(ROOT)
    if not head:
        print("beta_evidence FAILED: no head to bind this run to; pass --head <sha> or run it "
              "inside a git worktree.", file=sys.stderr)
        return 1
    base = git_base(ROOT)
    run_id = arguments.run_id or datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    out_dir = arguments.out or (EVIDENCE_ROOT / head / run_id)
    if out_dir.is_dir() and any(entry.is_file() for entry in out_dir.iterdir()):
        # Two runs of the same head must not overwrite each other: the run id is
        # what tells them apart, so reusing one has to be an explicit choice.
        print(f"beta_evidence FAILED: {relative_to_root(out_dir)} already holds a run; "
              "pick another --run-id instead of overwriting it.", file=sys.stderr)
        return 1
    work_dir = Path(arguments.work_dir) if arguments.work_dir else Path(tempfile.mkdtemp(prefix="beta-evidence-"))
    work_dir.mkdir(parents=True, exist_ok=True)
    commands: list[str] = []
    known_limits: list[dict[str, str]] = []
    outcomes: list[Outcome] = []

    try:
        identities = parse_identity(arguments.identity)
        materials = parse_material(arguments.material)
    except EvidenceError as error:
        print(f"beta_evidence FAILED: {error}", file=sys.stderr)
        return 1

    # ---- the F01 sequence -------------------------------------------------
    commands.append("python scripts/lingdoc_mock/run_f01.py --base-url <base-url> "
                    "--report <f01-report.json>   # by scripts/acceptance/beta_evidence.py")
    f01_outcomes, f01_report = run_f01(arguments.base_url, arguments.token or None, identities)
    outcomes.extend(f01_outcomes)

    # ---- the browser side -------------------------------------------------
    commands.append("python scripts/acceptance/beta_http_readback.py --base-url <base-url> --out <http.json>")
    commands.append("node scripts/acceptance/beta_browser_cdp.mjs --url <frontend-url> --out <browser.json>")
    commands.append("python scripts/acceptance/beta_evidence_parity.py --http <http.json> "
                    "--browser <browser.json> --out <parity.json>")
    browser_outcomes, browser_summary = run_browser_part(arguments.base_url, arguments.frontend_url, work_dir)
    outcomes.extend(browser_outcomes)

    # ---- the restart read-back -------------------------------------------
    commands.append("python scripts/acceptance/lingdoc_full_server_restart.py --binary <server-binary> "
                    "--repo-root <repo-root> --output-dir <work-dir>")
    restart_outcomes, restart_summary = run_restart_part(
        arguments.server_binary, arguments.repo_root, work_dir,
        arguments.db_driver, arguments.db_dsn)
    outcomes.extend(restart_outcomes)

    # ---- the Word check ---------------------------------------------------
    commands.append("python scripts/acceptance/beta_evidence.py --word-check <word-check.md> "
                    "(执行者填好后重跑)")
    word_outcomes, word_document, word_metadata = word_check(
        arguments.word_check, arguments.docx, arguments.modified_docx)
    outcomes.extend(word_outcomes)

    # ---- the model call ledger -------------------------------------------
    ledger_rows, ledger_reason = read_ledger(arguments.ledger_dsn, arguments.ledger_sqlite)
    if arguments.ledger_dsn or arguments.ledger_sqlite:
        if ledger_reason:
            outcomes.append(Outcome(
                id="模型调用账本：参数、预算与模型切换记录", status=NOT_RUN,
                reason=ledger_reason,
                unblock="让账本表 lingdoc_model_call_ledger 可读（迁移 000054 / 000133 已应用）后重跑本脚本。"))
        else:
            outcomes.append(Outcome(
                id="模型调用账本：参数、预算与模型切换记录", status=HELD,
                evidence=f"读到了 {len(ledger_rows)} 行账本记录。"))
    else:
        outcomes.append(Outcome(
            id="模型调用账本：参数、预算与模型切换记录", status=NOT_RUN,
            reason="本轮没有给出账本位置（--ledger-dsn 或 --ledger-sqlite）。",
            unblock="用 --ledger-dsn <本轮数据库 DSN> 或 --ledger-sqlite <文件> 重跑本脚本。"))

    result = overall_result(outcomes)

    # ---- the manifest -----------------------------------------------------
    contract = read_json(CONTRACT_PATH) or {}
    template = {}
    for check in f01_report.get("checks", []):
        if check.get("path") == "/data/ruleset_hash":
            template["ruleset_hash"] = str(check.get("actual"))
    for step in f01_report.get("steps", []):
        if step.get("id") == "F01-02":
            template["id"] = "template-demo"
    manifest = {
        "head": head,
        "run_id": run_id,
        "generated_at": now_iso(),
        "executor": arguments.executor,
        "contract": {"path": relative_to_root(CONTRACT_PATH), "version": contract.get("version", ""),
                     "mode": contract.get("mode", "")},
        "environment": {
            "python": platform.python_version(),
            "platform": platform.platform(),
            "node": shutil.which("node") or "（本机没有 node）",
            "git": git_output(ROOT, "--version") or "（本机没有 git）",
        },
        "database": {
            "driver": arguments.db_driver or "（本轮没有声明驱动）",
            "dsn": redact_dsn(arguments.db_dsn) or "（本轮没有声明 DSN）",
            "migrations": migration_version(arguments.db_dsn, arguments.ledger_sqlite),
        },
        "materials": materials,
        "template": template or {"id": "（本轮没有读到模板）"},
        "model": ledger_summary(ledger_rows, ledger_reason),
        "commands": commands,
        "restart": restart_summary,
        "word_check": word_metadata,
    }

    note = (f"Beta 首版真实验收的证据目录。协议 §5 的三态按字面记：BLOCKED（主流程缺依赖或无法继续）、"
            f"FAIL（业务行为违反断言）、NOT RUN（没有执行）。总结果由主流程决定：{result}。"
            f"passed[] 承载本轮全部已执行结论项（status 取 held / failed / blocked），"
            f"NOT RUN 一律在 not_run[]，每条带 reason 与 unblock。"
            f"执行范围：{'、'.join(arguments.identity and ['带身份的执行'] or ['无身份的 HTTP 执行'])}。")

    report = verification_report(
        mode="beta-first-acceptance",
        note=note,
        head=head,
        base=base,
        head_note=f"本轮绑定的代码 head：{head}。证据目录的第一层目录名就是它。",
        base_note=(f"基线 {base or '（本机没有可比的基线）'}；"
                   f"`git diff {base} {head} -- '*.go' '*.ts' '*.vue' '*.js' '*.sql'` 为空时，"
                   "代码面逐字未变。" if base else "本机没有可比的基线；本轮只绑定 head。"),
        outcomes=outcomes,
        known_limits=known_limits)

    restart_report = verification_report(
        mode="beta-first-restart-readback",
        note=(f"重启读回的证据（协议 §2 第 5 步）。总结果：{overall_result(restart_outcomes)}。"
              "启动配方与版本按本次 SHA 与环境核对，不继承历史 PASS。"),
        head=head,
        base=base,
        head_note=f"本轮绑定的代码 head：{head}。",
        base_note=f"基线 {base or '（本机没有可比的基线）'}。",
        outcomes=restart_outcomes,
        known_limits=known_limits)

    environment = {
        "Python": platform.python_version(),
        "平台": platform.platform(),
        "数据库驱动": arguments.db_driver or "（本轮没有声明驱动）",
        "DSN（脱敏形态）": redact_dsn(arguments.db_dsn) or "（本轮没有声明 DSN）",
        "迁移版本": str(manifest["database"]["migrations"].get("version") or "（没有读到）"),
    }
    scope = [
        "F01 契约的 24 个节点（本轮用 contracts/workflow.json 的断言求值）",
        "浏览器对偶三方比对（HTTP = 浏览器 = 刷新后读回）",
        "服务端真实重启后的读回",
        "Word 人工检查（执行者填写，执行器只算哈希与校验必填字段）",
        "模型调用账本里的参数、预算与模型切换记录",
    ]
    readme = render_readme(arguments.executor or "（未署名）", head, base, run_id, result,
                           outcomes, commands, environment, scope, known_limits, manifest)

    # ---- write the six artifacts -----------------------------------------
    out_dir.mkdir(parents=True, exist_ok=True)
    artifacts = {
        "README.md": readme,
        "f01-report.json": json.dumps(report, ensure_ascii=False, indent=2) + "\n",
        "runtime-manifest.json": json.dumps(manifest, ensure_ascii=False, indent=2) + "\n",
        "browser-check.md": render_browser_check(browser_summary, browser_outcomes),
        "restart-readback.json": json.dumps(restart_report, ensure_ascii=False, indent=2) + "\n",
        "word-check.md": word_document,
    }
    leaks: list[str] = []
    for name, text in artifacts.items():
        found = find_leaks(text)
        if found:
            leaks.append(f"{name}: {', '.join(found)}")
    if leaks:
        print("beta_evidence FAILED: the artifacts would leak something: " + "; ".join(leaks),
              file=sys.stderr)
        return 1
    for name, text in artifacts.items():
        write_text(out_dir / name, text)

    present = sorted(entry.name for entry in out_dir.iterdir() if entry.is_file())
    missing = [name for name in SIX_ARTIFACTS if name not in present]
    if missing:
        print(f"beta_evidence FAILED: the evidence directory is missing {missing}", file=sys.stderr)
        return 1

    print(f"beta_evidence: {result}")
    print(f"beta_evidence: evidence directory {relative_to_root(out_dir)}")
    for outcome in outcomes:
        if outcome.status != HELD:
            print(f"beta_evidence: {outcome.status.upper()} {outcome.id}｜{outcome.reason or outcome.evidence}")
    if arguments.work_dir is None:
        shutil.rmtree(work_dir, ignore_errors=True)
    return 0 if result == "PASS" else 1


def migration_version(dsn: str, sqlite_path: str) -> dict[str, Any]:
    """The migration version of the database this run used, if it can be read."""
    if sqlite_path and Path(sqlite_path).is_file():
        try:
            import sqlite3
            with sqlite3.connect(str(sqlite_path)) as connection:
                row = connection.execute(
                    "SELECT version, dirty FROM schema_migrations").fetchone()
        except Exception as error:  # sqlite3 raises a family of errors
            return {"version": None, "readable": False, "detail": f"{type(error).__name__}: {error}"}
        if row:
            return {"version": row[0], "dirty": bool(row[1]), "readable": True}
        return {"version": None, "readable": False, "detail": "schema_migrations 里没有版本行"}
    if not dsn:
        return {"version": None, "readable": False, "detail": "本轮没有声明数据库"}
    psql = shutil.which("psql")
    if not psql:
        return {"version": None, "readable": False, "detail": "本机没有 psql"}
    parts = parse_dsn(dsn)
    environment = os.environ.copy()
    for key, name in (("host", "PGHOST"), ("port", "PGPORT"), ("user", "PGUSER"), ("dbname", "PGDATABASE")):
        if parts.get(key):
            environment[name] = str(parts[key])
    if parts.get("password"):
        environment["PGPASSWORD"] = parts["password"]
    try:
        result = subprocess.run([psql, "-X", "-A", "-t", "-c", "SELECT version, dirty FROM schema_migrations"],
                                capture_output=True, text=True, encoding="utf-8", errors="replace",
                                env=environment, check=False, timeout=60)
    except (OSError, subprocess.TimeoutExpired) as error:
        return {"version": None, "readable": False, "detail": f"{type(error).__name__}: {error}"}
    if result.returncode != 0:
        return {"version": None, "readable": False, "detail": (result.stderr or "").strip()[:200]}
    line = (result.stdout or "").strip().splitlines()
    if not line:
        return {"version": None, "readable": False, "detail": "schema_migrations 里没有版本行"}
    version, _, dirty = line[0].partition("|")
    return {"version": version.strip(), "dirty": dirty.strip() in {"t", "true", "True"}, "readable": True}


def ledger_summary(rows: list[dict[str, Any]], reason: str) -> dict[str, Any]:
    """The model parameters, the budget and the model switches, read from the ledger."""
    if reason:
        return {"source": "lingdoc_model_call_ledger", "readable": False, "detail": reason,
                "parameters": [], "budget": {}, "switches": []}
    calls = [row for row in rows if row.get("kind") == "call"]
    switches = [row for row in rows if row.get("kind") == "switch"]
    parameters: list[dict[str, Any]] = []
    for row in calls:
        raw = row.get("parameters_json") or ""
        try:
            parsed = json.loads(raw) if raw else {}
        except json.JSONDecodeError:
            parsed = {"unparsable": True}
        parameters.append({"model_id": row.get("model_id"), "provider": row.get("provider"),
                           "run_mode": row.get("run_mode"), "parameters": parsed})
    reserved = sum(int(row.get("reserved_nano_usd") or 0) for row in rows)
    actual = sum(int(row.get("actual_nano_usd") or 0) for row in rows)
    return {
        "source": "lingdoc_model_call_ledger",
        "readable": True,
        "calls": len(calls),
        "switches": len(switches),
        "parameters": parameters,
        "budget": {
            "reserved_nano_usd": reserved,
            "actual_nano_usd": actual,
            "input_tokens": sum(int(row.get("input_tokens") or 0) for row in rows),
            "output_tokens": sum(int(row.get("output_tokens") or 0) for row in rows),
        },
        "switch_records": [{"from_model_id": row.get("switched_from_model_id"),
                            "to_model_id": row.get("model_id"),
                            "reason": row.get("switch_reason"),
                            "affected_steps": row.get("affected_steps_json"),
                            "at": row.get("created_at")} for row in switches],
    }


if __name__ == "__main__":
    raise SystemExit(main())
