#!/usr/bin/env python3
"""Run LingDoc backend checks against SQLite and an isolated disposable PostgreSQL."""
from __future__ import annotations
import argparse
import atexit
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import subprocess
import sys
import time
import uuid
from urllib.parse import unquote, urlparse

ROOT = Path(os.environ.get("LINGDOC_ACCEPTANCE_REPO_ROOT", Path(__file__).resolve().parents[2])).resolve()
PACKAGES = ["./internal/database", "./internal/lingdoc/...", "./internal/router", "./internal/container", "./internal/evidence"]
GROUPS = {
    "sqlite_migrations": ["TestSQLiteMigrationsCreateVersionedSchema", "TestSQLiteMigrationsUpgradeV4PreservesData", "TestSQLiteProductionMigrationBatch"],
    "postgres_migration": ["TestPostgresMigrationAndRepositoryContract"],
    "source_revocation_download_auth": ["TestCheckPassedSnapshotCannotDownloadAfterRealKBShareRevocation", "TestExportRoutesDenyADownloadAfterBoundSourceAccessIsRevoked", "TestListChaptersDeniesRevokedSourceAndRedactsStaleContent"],
    "version_conflict": ["TestConfirmationServiceRejectsStaleVersionBeforeWrite", "TestDeliveryRoutesRefuseAVersionTheCallerDidNotSee", "TestHandlerMapsStaleChangeSetApplyToVersionConflictEnvelope"],
    "duplicate_idempotency": ["TestAcceptCandidateCreatesVersionPreservesReviewItemsAndReplays", "TestConfirmationIdempotencyKeyIsUniqueInAutoMigratedStore", "TestExportRoutesReplayTheSameAction", "TestRecordFreezeReplaysTheSnapshotForTheSameAction", "TestSpecRetryPreservesIdempotencyOrder"],
    "task_cancellation": ["TestExecuteStoresProviderCancellationAsInterrupted", "TestSQLiteRepositoryCancelsQueuedAndRunningRuns", "TestSpecRetryBoundAndCancellation"],
    "restart_readback": ["TestReleaseSnapshotIDsSurviveServiceRestart", "TestFreezeActionSurvivesAReopen"],
    "corrupted_file_recovery": ["TestDownloadRejectsPersistedCorruption", "TestExportDownloadRejectsCorruptedPersistedBytes", "TestRecoveryFlowFailedRenderCanRetryWithoutExposingOldBytes"],
    "postgres_failure_recovery": ["TestPostgresDownloadRejectsPersistedCorruption", "TestPostgresGenerationFailureBoundaries", "TestPostgresWorkerCrashLeasePersistence", "TestPostgresRendererFailureUsesRealDOCXValidator"],
}
NOT_RUN = [
    {"id": "response_lost_after_commit", "status": "NOT RUN", "reason": "Same-key replay is tested; no HTTP drop-after-commit injector."},
    {"id": "provider_timeout", "status": "NOT RUN", "reason": "Cancellation is tested; deterministic model timeout injection is not wired."},
    {"id": "full_server_restart", "status": "NOT RUN", "reason": "Store reopen tests do not restart the application process."},
    {"id": "postgres_down_migration", "status": "NOT RUN", "reason": "Down migrations are deliberately not run automatically."},
    {"id": "real_model_browser_e2e", "status": "NOT RUN", "reason": "This runner uses synthetic fixtures and does not call a model or browser."},
]
SENSITIVE = [
    (re.compile(r"(?i)(password\s*=\s*)(?:'[^']*'|\"[^\"]*\"|\S+)"), r"\1[REDACTED]"),
    (re.compile(r"(?i)(postgres(?:ql)?://[^:/\s]+:)[^@\s]+@"), r"\1[REDACTED]@"),
    (re.compile(r"(?i)(authorization:\s*bearer\s+)[A-Za-z0-9._~+/-]+"), r"\1[REDACTED]"),
]

def redact(value: str) -> str:
    for pattern, replacement in SENSITIVE:
        value = pattern.sub(replacement, value)
    return value

def safe_postgres_target(dsn: str) -> dict[str, str]:
    if dsn.startswith(("postgres://", "postgresql://")):
        parsed = urlparse(dsn)
        host, user = (parsed.hostname or "").lower(), unquote(parsed.username or "")
        database = unquote(parsed.path.lstrip("/"))
    else:
        fields = {}
        for part in shlex.split(dsn):
            if "=" in part:
                key, value = part.split("=", 1)
                fields[key.lower()] = value
        host, user = fields.get("host", "").lower(), fields.get("user", "")
        database = fields.get("dbname", fields.get("database", ""))
    if host not in {"localhost", "127.0.0.1", "::1"}:
        raise ValueError("PostgreSQL host must be loopback")
    if user != "lingdoc_test" or database != "lingdoc_test":
        raise ValueError("PostgreSQL target must use the synthetic lingdoc_test identity/database")
    return {"host": host, "user": user, "database": database}

def parse_go_events(lines: list[str]) -> dict[str, list[str]]:
    found: dict[str, list[str]] = {}
    for line in lines:
        try:
            item = json.loads(line)
        except (TypeError, json.JSONDecodeError):
            continue
        name, action = item.get("Test"), item.get("Action")
        if isinstance(name, str) and action in {"pass", "fail", "skip"}:
            found.setdefault(name, []).append({"pass": "PASS", "fail": "FAIL", "skip": "NOT RUN"}[action])
    return found

def group_result(required: list[str], events: dict[str, list[str]]) -> tuple[str, list[dict[str, str]]]:
    observed = []
    for expected in required:
        matches = [status for name, values in events.items()
                   if name == expected or name.startswith(expected + "/") for status in values]
        status = "FAIL" if "FAIL" in matches else "PASS" if "PASS" in matches else "NOT RUN" if "NOT RUN" in matches else "BLOCKED"
        observed.append({"test": expected, "status": status})
    statuses = [row["status"] for row in observed]
    overall = "FAIL" if "FAIL" in statuses else "BLOCKED" if "BLOCKED" in statuses else "NOT RUN" if "NOT RUN" in statuses else "PASS"
    return overall, observed

def overall_result(checks: list[dict], cases: list[dict], not_run: list[dict]) -> str:
    statuses = [row["status"] for row in checks + cases + not_run]
    return "FAIL" if "FAIL" in statuses else "PASS" if all(value == "PASS" for value in statuses) else "BLOCKED"

def run_command(argv: list[str], env: dict[str, str], log: Path, timeout: int = 2400) -> dict:
    try:
        result = subprocess.run(argv, cwd=ROOT, env=env, text=True, encoding="utf-8", errors="replace",
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout, check=False)
        output, code = result.stdout, result.returncode
        status = "PASS" if code == 0 else "FAIL"
    except FileNotFoundError:
        output, code, status = "Required tool is not installed: " + argv[0], None, "BLOCKED"
    except subprocess.TimeoutExpired as error:
        output = error.stdout.decode("utf-8", "replace") if isinstance(error.stdout, bytes) else (error.stdout or "")
        output += "\nCommand timed out."
        code, status = 124, "FAIL"
    log.parent.mkdir(parents=True, exist_ok=True)
    log.write_text(redact(output), encoding="utf-8")
    return {"name": log.stem, "status": status, "exit_code": code, "log": str(log)}

def start_postgres() -> tuple[str | None, dict, str | None]:
    injected = os.environ.get("LINGDOC_TEST_POSTGRES_DSN", "")
    if injected:
        try:
            target = safe_postgres_target(injected)
        except ValueError:
            return None, {"status": "BLOCKED", "mode": "injected_dsn_rejected", "reason": "Only loopback lingdoc_test is allowed"}, None
        return injected, {"status": "PASS", "mode": "injected_loopback_test_database", "target": target}, None
    name = "lingdoc-acceptance-" + uuid.uuid4().hex[:12]
    try:
        info = subprocess.run(["docker", "info"], capture_output=True, timeout=10, check=False)
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return None, {"status": "BLOCKED", "mode": "docker", "reason": "Docker daemon unavailable"}, None
    if info.returncode:
        return None, {"status": "BLOCKED", "mode": "docker", "reason": "Docker daemon unavailable"}, None
    try:
        started = subprocess.run(["docker", "run", "--rm", "-d", "--name", name,
            "--label", "com.lingdoc.acceptance=true", "-e", "POSTGRES_DB=lingdoc_test",
            "-e", "POSTGRES_USER=lingdoc_test", "-e", "POSTGRES_PASSWORD=synthetic_ci_only",
            "-p", "127.0.0.1::5432", "postgres:17"], capture_output=True, text=True, timeout=60, check=False)
        if started.returncode:
            return None, {"status": "BLOCKED", "mode": "docker", "reason": "Disposable PostgreSQL container failed to start"}, None
        port_info = subprocess.run(["docker", "port", name, "5432/tcp"], capture_output=True, text=True, timeout=10, check=False)
        port = port_info.stdout.strip().splitlines()[0].rsplit(":", 1)[-1] if port_info.returncode == 0 and port_info.stdout.strip() else ""
        if not port.isdigit():
            return None, {"status": "BLOCKED", "mode": "docker", "reason": "PostgreSQL port was not published"}, name
        ready = False
        for _ in range(90):
            probe = subprocess.run(["docker", "exec", name, "pg_isready", "-U", "lingdoc_test", "-d", "lingdoc_test"],
                                   capture_output=True, timeout=5, check=False)
            if probe.returncode == 0:
                ready = True
                break
            time.sleep(1)
        if not ready:
            return None, {"status": "BLOCKED", "mode": "docker", "reason": "PostgreSQL did not become ready"}, name
        dsn = f"host=127.0.0.1 port={port} user=lingdoc_test password=synthetic_ci_only dbname=lingdoc_test sslmode=disable"
        return dsn, {"status": "PASS", "mode": "disposable_docker", "image": "postgres:17",
                     "database": "lingdoc_test", "identity": "lingdoc_test", "persistent_volume": False}, name
    except (OSError, subprocess.TimeoutExpired):
        return None, {"status": "BLOCKED", "mode": "docker", "reason": "PostgreSQL setup failed"}, name

def stop_docker_container(name: str) -> bool:
    try:
        stopped = subprocess.run(["docker", "stop", name], capture_output=True, timeout=30, check=False)
    except (OSError, subprocess.TimeoutExpired):
        return False
    return stopped.returncode == 0

def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run isolated LingDoc backend acceptance checks.")
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--require-postgres", action="store_true")
    args = parser.parse_args(argv)
    out = args.output_dir.resolve()
    out.mkdir(parents=True, exist_ok=True)
    dsn, pg, owned = start_postgres()
    if owned:
        atexit.register(stop_docker_container, owned)
    env = os.environ.copy()
    env["CGO_ENABLED"] = "1"
    if dsn is None:
        env.pop("LINGDOC_TEST_POSTGRES_DSN", None)
    else:
        env["LINGDOC_TEST_POSTGRES_DSN"] = dsn
    checks = []
    go_version = "unavailable"
    try:
        go_version = subprocess.run(["go", "version"], cwd=ROOT, env=env, capture_output=True, text=True, timeout=10, check=False).stdout.strip() or "unavailable"
        cgo = subprocess.run(["go", "env", "CGO_ENABLED"], cwd=ROOT, env=env, capture_output=True, text=True, timeout=10, check=False).stdout.strip()
    except (OSError, subprocess.TimeoutExpired):
        cgo = "unknown"
    compiler = shutil.which(os.environ.get("CC", "gcc"))
    go_blocked = go_version == "unavailable" or (cgo == "1" and compiler is None)
    if go_blocked:
        for name in ("go-test", "go-vet", "go-build"):
            log = out / (name + ".log")
            log.write_text("BLOCKED: Go/CGO compiler is unavailable.", encoding="utf-8")
            checks.append({"name": name, "status": "BLOCKED", "exit_code": None, "log": str(log)})
    else:
        checks.append(run_command(["go", "test", "-race", "-json", "-count=1", "-timeout=40m", "-mod=readonly", *PACKAGES],
                                  env, out / "go-test.jsonl", 2500))
        checks.append(run_command(["go", "vet", "-mod=readonly", *PACKAGES], env, out / "go-vet.log"))
        binary = out / ("lingdoc-server-build.exe" if os.name == "nt" else "lingdoc-server-build")
        checks.append(run_command(["go", "build", "-mod=readonly", "-o", str(binary), "./cmd/server"], env, out / "go-build.log"))
        if binary.exists():
            binary.unlink()
    checks.append(run_command([sys.executable, "-m", "unittest", "discover", "-s", "scripts/lingdoc_mock",
                               "-p", "test_g6_quality.py", "-v"], env, out / "g6-quality-tests.log", 180))
    events = {}
    go_log = out / "go-test.jsonl"
    if go_log.exists():
        events = parse_go_events(go_log.read_text(encoding="utf-8", errors="replace").splitlines())
    cases = []
    for key, tests in GROUPS.items():
        status, observed = group_result(tests, events)
        if key.startswith("postgres_") and pg["status"] != "PASS" and status == "NOT RUN":
            status = "BLOCKED"
        cases.append({"id": key, "status": status, "tests": observed})
    if owned:
        cleanup = "PASS" if stop_docker_container(owned) else "BLOCKED"
        if cleanup == "PASS":
            atexit.unregister(stop_docker_container, owned)
        else:
            cleanup_log = out / "docker-cleanup.log"
            cleanup_log.write_text("BLOCKED: could not stop disposable PostgreSQL container.\n", encoding="utf-8")
            checks.append({"name": "docker-cleanup", "status": "BLOCKED", "exit_code": None, "log": str(cleanup_log)})
    else:
        cleanup = "MANAGED_BY_CI_OR_NOT_CREATED"
    statuses = [row["status"] for row in checks]
    automated = "FAIL" if "FAIL" in statuses else "PASS" if all(value == "PASS" for value in statuses) else "BLOCKED"
    report = {
        "report_version": 1,
        "generated_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "commit": subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, capture_output=True, text=True, check=False).stdout.strip() or "unknown",
        "scope": "backend and engineering quality; synthetic data only",
        "environment": {"os": platform.platform(), "python": platform.python_version(), "go": go_version,
                        "cgo_enabled": cgo, "c_compiler_available": bool(compiler),
                        "postgres_dsn_injected": bool(os.environ.get("LINGDOC_TEST_POSTGRES_DSN"))},
        "postgres_setup": pg, "automated_result": automated,
        "closeout_result": overall_result(checks, cases, NOT_RUN), "checks": checks,
        "scenarios": cases, "not_run": NOT_RUN, "cleanup": {"status": cleanup, "owned_container": bool(owned)},
        "limitations": ["No real model credentials or user project data are used.",
                        "Repository reopen tests do not prove a full application-process restart.",
                        "PostgreSQL down migrations are not run automatically."],
    }
    (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    lines = ["# LingDoc backend closeout", "", f"- Automated checks: **{automated}**",
             f"- Full closeout: **{report['closeout_result']}**",
             f"- Commit: **{report['commit']}**", f"- PostgreSQL setup: **{pg['status']}**", "",
             "| Scenario | Status |", "|---|---|"]
    lines.extend(f"| {row['id']} | {row['status']} |" for row in cases + NOT_RUN)
    (out / "summary.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"Automated checks: {automated}; full closeout: {report['closeout_result']}")
    print(f"Reports: {out}")
    if automated == "FAIL":
        return 1
    if args.require_postgres and (pg["status"] != "PASS" or any(row["status"] != "PASS" for row in cases)
                                  or any(row["status"] != "PASS" for row in checks)):
        return 2
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
