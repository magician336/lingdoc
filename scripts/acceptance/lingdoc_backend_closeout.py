#!/usr/bin/env python3
"""Run LingDoc backend checks against SQLite and an isolated disposable PostgreSQL."""
from __future__ import annotations
import argparse
import atexit
from datetime import datetime, timezone
from functools import partial
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
from urllib.parse import parse_qs, unquote, urlparse

ROOT = Path(os.environ.get("LINGDOC_ACCEPTANCE_REPO_ROOT", Path(__file__).resolve().parents[2])).resolve()
PACKAGES = ["./internal/database", "./internal/lingdoc/...", "./internal/router", "./internal/container", "./internal/evidence"]
GROUPS = {
    "sqlite_migrations": ["TestSQLiteMigrationsCreateVersionedSchema", "TestSQLiteMigrationsUpgradeV4PreservesData", "TestSQLiteProductionMigrationBatch"],
    "postgres_migration": ["TestPostgresMigrationAndRepositoryContract"],
    "source_revocation_download_auth": ["TestCheckPassedSnapshotCannotDownloadAfterRealKBShareRevocation", "TestExportRoutesDenyADownloadAfterBoundSourceAccessIsRevoked", "TestListChaptersDeniesRevokedSourceAndRedactsStaleContent"],
    "version_conflict": ["TestConfirmationServiceRejectsStaleVersionBeforeWrite", "TestDeliveryRoutesRefuseAVersionTheCallerDidNotSee", "TestHandlerMapsStaleChangeSetApplyToVersionConflictEnvelope"],
    "duplicate_idempotency": ["TestAcceptCandidateCreatesVersionPreservesReviewItemsAndReplays", "TestConfirmationIdempotencyKeyIsUniqueInAutoMigratedStore", "TestExportRoutesReplayTheSameAction", "TestRecordFreezeReplaysTheSnapshotForTheSameAction", "TestSpecRetryPreservesIdempotencyOrder"],
    "response_lost_after_commit": ["TestLingDocRealAPIThroughMainRegistration/response_lost_after_commit"],
    "provider_timeout": ["TestExecuteStoresProviderTimeoutAsInterrupted"],
    "task_cancellation": ["TestExecuteStoresProviderCancellationAsInterrupted", "TestSQLiteRepositoryCancelsQueuedAndRunningRuns", "TestSpecRetryBoundAndCancellation"],
    "restart_readback": ["TestReleaseSnapshotIDsSurviveServiceRestart", "TestFreezeActionSurvivesAReopen"],
    "corrupted_file_recovery": ["TestDownloadRejectsPersistedCorruption", "TestExportDownloadRejectsCorruptedPersistedBytes", "TestRecoveryFlowFailedRenderCanRetryWithoutExposingOldBytes"],
    "postgres_failure_recovery": ["TestPostgresDownloadRejectsPersistedCorruption", "TestPostgresGenerationFailureBoundaries", "TestPostgresWorkerCrashLeasePersistence", "TestPostgresRendererFailureUsesRealDOCXValidator"],
}
NOT_RUN = [
    {"id": "real_provider_timeout", "status": "NOT RUN", "reason": "The deterministic fake-provider deadline is tested; no real upstream model timeout is exercised."},
    {"id": "production_migration_rollback", "status": "NOT RUN", "reason": "Down/up roundtrip runs only against an owned disposable test database; no development or production schema is rolled back."},
    {"id": "real_model_browser_e2e", "status": "NOT RUN", "reason": "This runner uses synthetic fixtures and does not call a model or browser."},
]
SENSITIVE = [
    (re.compile(r"(?i)(password\s*=\s*)(?:'[^']*'|\"[^\"]*\"|\S+)"), r"\1[REDACTED]"),
    (re.compile(r"(?i)(postgres(?:ql)?://[^:/\s]+:)[^@\s]+@"), r"\1[REDACTED]@"),
    (re.compile(r"(?i)(authorization:\s*bearer\s+)[A-Za-z0-9._~+/-]+"), r"\1[REDACTED]"),
]
FAILURE_FIXTURE_ENV_VARS = frozenset({
    "LINGDOC_FAILURE_TEST_DB",
    "LINGDOC_FAILURE_TEST_USER",
    "LINGDOC_FAILURE_TEST_DSN",
    "LINGDOC_FAILURE_FIXTURE_MARKER",
    "LINGDOC_FAILURE_CRASH_RUN",
    "LINGDOC_FAILURE_CRASH_READY",
    "DB_PASSWORD",
})
POSTGRES_TARGET_QUERY_KEYS = frozenset({
    "host", "hostaddr", "port", "user", "dbname", "database",
})

def redact(value: str) -> str:
    for pattern, replacement in SENSITIVE:
        value = pattern.sub(replacement, value)
    return value

def isolated_process_environment() -> dict[str, str]:
    """Preserve only OS runtime variables; never forward inherited credentials."""
    names = ["PATH", "LANG", "LC_ALL", "TZ", "HOME", "TMPDIR", "TMP", "TEMP"]
    if os.name == "nt":
        names.extend(["SYSTEMROOT", "WINDIR", "SystemDrive", "USERPROFILE"])
    return {name: os.environ[name] for name in names if os.environ.get(name)}

def clean_failure_fixture_environment(environment: dict[str, str]) -> dict[str, str]:
    """Remove caller-provided fixture state before any child process starts."""
    cleaned = environment.copy()
    for name in FAILURE_FIXTURE_ENV_VARS:
        cleaned.pop(name, None)
    return cleaned

def safe_postgres_target(dsn: str) -> dict[str, str]:
    if dsn.startswith(("postgres://", "postgresql://")):
        parsed = urlparse(dsn)
        query_keys = {key.lower() for key in parse_qs(parsed.query, keep_blank_values=True)}
        overridden = sorted(query_keys & POSTGRES_TARGET_QUERY_KEYS)
        if overridden:
            raise ValueError("PostgreSQL target query cannot override " + ", ".join(overridden))
        host, user = (parsed.hostname or "").lower(), unquote(parsed.username or "")
        database = unquote(parsed.path.lstrip("/"))
        try:
            port = str(parsed.port or 5432)
        except ValueError as error:
            raise ValueError("PostgreSQL port must be numeric and valid") from error
    else:
        fields = {}
        for part in shlex.split(dsn):
            if "=" in part:
                key, value = part.split("=", 1)
                fields[key.lower()] = value
        host, user = fields.get("host", "").lower(), fields.get("user", "")
        database = fields.get("dbname", fields.get("database", ""))
        port = fields.get("port", "5432")
    if host not in {"localhost", "127.0.0.1", "::1"}:
        raise ValueError("PostgreSQL host must be loopback")
    if user != "lingdoc_test" or database != "lingdoc_test":
        raise ValueError("PostgreSQL target must use the synthetic lingdoc_test identity/database")
    if not port.isdigit() or not 1 <= int(port) <= 65535:
        raise ValueError("PostgreSQL port must be between 1 and 65535")
    return {"host": host, "user": user, "database": database, "port": port}

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
                     "database": "lingdoc_test", "identity": "lingdoc_test", "host": "127.0.0.1",
                     "port": port, "persistent_volume": False}, name
    except (OSError, subprocess.TimeoutExpired):
        return None, {"status": "BLOCKED", "mode": "docker", "reason": "PostgreSQL setup failed"}, name

def stop_docker_container(name: str) -> bool:
    try:
        stopped = subprocess.run(["docker", "stop", name], capture_output=True, timeout=30, check=False)
    except (OSError, subprocess.TimeoutExpired):
        return False
    return stopped.returncode == 0

def cleanup_docker_container(name: str, log: Path) -> bool:
    """Stop the owned container and leave a diagnostic when cleanup fails."""
    stopped = stop_docker_container(name)
    if not stopped:
        log.parent.mkdir(parents=True, exist_ok=True)
        log.write_text("BLOCKED: could not stop disposable PostgreSQL container.\n", encoding="utf-8")
    return stopped

def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run isolated LingDoc backend acceptance checks.")
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--require-postgres", action="store_true")
    args = parser.parse_args(argv)
    out = args.output_dir.resolve()
    out.mkdir(parents=True, exist_ok=True)
    dsn, pg, owned = start_postgres()
    cleanup_callback = None
    if owned:
        cleanup_callback = partial(cleanup_docker_container, owned, out / "docker-cleanup.log")
        atexit.register(cleanup_callback)
    env = clean_failure_fixture_environment(os.environ)
    env["CGO_ENABLED"] = "1"
    if dsn is None:
        env.pop("LINGDOC_TEST_POSTGRES_DSN", None)
    else:
        env["LINGDOC_TEST_POSTGRES_DSN"] = dsn
    checks = []
    failure_fixture = {
        "status": "NOT RUN",
        "setup_status": "NOT RUN",
        "setup_log": None,
        "database": None,
        "user": None,
    }
    failure_fixture_attempted = False
    failure_fixture_ready = False
    failure_fixture_env = None
    postgres_rollback_status = "NOT RUN"
    server_restart_status = "BLOCKED"
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
        if dsn is not None and pg["status"] == "PASS":
            target = safe_postgres_target(dsn)
            failure_fixture["database"] = "lingdoc_fault_" + uuid.uuid4().hex[:12]
            failure_fixture["user"] = "lingdoc_fault_user_" + uuid.uuid4().hex[:12]
            failure_password = "synthetic_ci_only"
            failure_fixture_env = env.copy()
            failure_fixture_env["LINGDOC_FAILURE_TEST_DB"] = failure_fixture["database"]
            failure_fixture_env["LINGDOC_FAILURE_TEST_USER"] = failure_fixture["user"]
            failure_fixture_env["LINGDOC_FAILURE_TEST_DSN"] = (
                f"host=127.0.0.1 port={target['port']} user={failure_fixture['user']} "
                f"password={failure_password} dbname={failure_fixture['database']} sslmode=disable"
            )
            failure_fixture_env["LINGDOC_FAILURE_FIXTURE_MARKER"] = str(out / "postgres-failure-fixture-owned.txt")
            failure_fixture_env["DB_PASSWORD"] = failure_password
            failure_fixture_attempted = True
            setup = run_command(["go", "run", "./scripts/acceptance/postgres_failure_fixture.go", "create"],
                                failure_fixture_env, out / "postgres-fixture-create.log", 300)
            checks.append(setup)
            failure_fixture["setup_status"] = setup["status"]
            failure_fixture["setup_log"] = setup["log"]
            failure_fixture_ready = setup["status"] == "PASS"
            failure_fixture["status"] = "PASS" if failure_fixture_ready else "BLOCKED"
            if failure_fixture_ready:
                env = failure_fixture_env
        checks.append(run_command(["go", "test", "-race", "-json", "-count=1", "-timeout=40m", "-mod=readonly", *PACKAGES],
                                  env, out / "go-test.jsonl", 2500))
        checks.append(run_command(["go", "vet", "-mod=readonly", *PACKAGES], env, out / "go-vet.log"))
        binary = out / ("lingdoc-server-build.exe" if os.name == "nt" else "lingdoc-server-build")
        build = run_command(["go", "build", "-mod=readonly", "-o", str(binary), "./cmd/server"], env, out / "go-build.log")
        checks.append(build)
        if binary.exists():
            if build["status"] == "PASS":
                restart = run_command(
                    [sys.executable, "scripts/acceptance/lingdoc_full_server_restart.py",
                     "--binary", str(binary), "--repo-root", str(ROOT),
                     "--output-dir", str(out / "full-server-restart")],
                    isolated_process_environment(), out / "full-server-restart.log", 600)
                checks.append(restart)
                server_restart_status = restart["status"]
            binary.unlink()
    checks.append(run_command([sys.executable, "-m", "unittest", "discover", "-s", "scripts/lingdoc_mock",
                               "-p", "test_g6_quality.py", "-v"], env, out / "g6-quality-tests.log", 180))
    failure_fixture_cleanup = "NOT_CREATED"
    if failure_fixture_attempted and failure_fixture_env is not None:
        if failure_fixture_ready:
            rollback_result = run_command(
                ["go", "run", "./scripts/acceptance/postgres_failure_fixture.go", "rollback-roundtrip"],
                failure_fixture_env, out / "postgres-migration-rollback.log", 300)
            checks.append(rollback_result)
            postgres_rollback_status = rollback_result["status"]
        else:
            postgres_rollback_status = "BLOCKED"
        cleanup_result = run_command(["go", "run", "./scripts/acceptance/postgres_failure_fixture.go", "drop"],
                                     failure_fixture_env, out / "postgres-fixture-cleanup.log", 300)
        checks.append(cleanup_result)
        failure_fixture_cleanup = cleanup_result["status"]
    events = {}
    go_log = out / "go-test.jsonl"
    if go_log.exists():
        events = parse_go_events(go_log.read_text(encoding="utf-8", errors="replace").splitlines())
    cases = []
    for key, tests in GROUPS.items():
        status, observed = group_result(tests, events)
        if key.startswith("postgres_") and pg["status"] != "PASS" and status == "NOT RUN":
            status = "BLOCKED"
        if key == "postgres_failure_recovery" and not failure_fixture_ready:
            status = "BLOCKED"
        cases.append({"id": key, "status": status, "tests": observed})
    cases.append({"id": "postgres_migration_rollback_roundtrip", "status": postgres_rollback_status,
                  "details": "Reverse LingDoc migrations in the owned disposable database, verify removal, then reapply and verify the latest schema."})
    cases.append({"id": "full_server_restart", "status": server_restart_status,
                  "details": "Start the real server binary with isolated synthetic auth and SQLite storage, create a LingDoc project, restart the process, then validate the same token and read the same project back."})
    if owned:
        cleanup = "PASS" if cleanup_callback() else "BLOCKED"
        if cleanup == "PASS":
            if cleanup_callback is not None:
                atexit.unregister(cleanup_callback)
        else:
            cleanup_log = out / "docker-cleanup.log"
            checks.append({"name": "docker-cleanup", "status": "BLOCKED", "exit_code": None, "log": str(cleanup_log)})
    else:
        cleanup = "MANAGED_BY_CI_OR_NOT_CREATED"
    statuses = [row["status"] for row in checks]
    automated = "FAIL" if "FAIL" in statuses else "PASS" if all(value == "PASS" for value in statuses) else "BLOCKED"
    checkout_commit = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, capture_output=True, text=True, check=False).stdout.strip() or "unknown"
    report_commit = os.environ.get("LINGDOC_ACCEPTANCE_HEAD_SHA", checkout_commit)
    report = {
        "report_version": 1,
        "generated_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "commit": report_commit,
        "checkout_commit": checkout_commit,
        "scope": "backend and engineering quality; synthetic data only",
        "environment": {"os": platform.platform(), "python": platform.python_version(), "go": go_version,
                        "cgo_enabled": cgo, "c_compiler_available": bool(compiler),
                        "postgres_dsn_injected": bool(os.environ.get("LINGDOC_TEST_POSTGRES_DSN"))},
        "postgres_setup": {**pg, "failure_fixture": failure_fixture}, "automated_result": automated,
        "closeout_result": overall_result(checks, cases, NOT_RUN), "checks": checks,
        "scenarios": cases, "not_run": NOT_RUN,
        "cleanup": {"status": cleanup, "owned_container": bool(owned), "failure_database": failure_fixture_cleanup},
        "limitations": ["No real model credentials or user project data are used.",
                        "Full process restart covers a synthetic SQLite deployment; PostgreSQL process restart is not separately exercised.",
                        "PostgreSQL down/up migration roundtrip runs only on an owned disposable test database; production rollback is not exercised."],
    }
    (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    lines = ["# LingDoc backend closeout", "", f"- Automated checks: **{automated}**",
             f"- Full closeout: **{report['closeout_result']}**",
             f"- Commit: **{report['commit']}**", f"- PostgreSQL setup: **{pg['status']}**", "",
             "| Scenario | Status |", "|---|---|"]
    lines.extend(f"| {row['id']} | {row['status']} |" for row in cases + NOT_RUN)
    lines.extend([
        f"| postgres_failure_fixture_setup | {failure_fixture['setup_status']} |",
        f"| postgres_failure_fixture | {failure_fixture['status']} |",
        f"| postgres_failure_fixture_cleanup | {failure_fixture_cleanup} |",
    ])
    (out / "summary.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"Automated checks: {automated}; full closeout: {report['closeout_result']}")
    for row in cases + NOT_RUN:
        print(f"Scenario {row['id']}: {row['status']}")
    print(f"Reports: {out}")
    if automated == "FAIL":
        return 1
    if args.require_postgres and (pg["status"] != "PASS" or any(row["status"] != "PASS" for row in cases)
                                  or any(row["status"] != "PASS" for row in checks)):
        return 2
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
