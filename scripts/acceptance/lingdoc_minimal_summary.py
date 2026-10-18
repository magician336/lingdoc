"""Write a small SHA-bound LingDoc acceptance summary for every CI run.

The summary deliberately reports checks that were actually executed and keeps
live-provider, Word, and full-server checks explicit as NOT RUN. It is a
review artifact, not a substitute for the heavier backend closeout workflow.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path
from lingdoc_source_binding import capture_source_binding, compare_source_bindings


def run_check(name: str, argv: list[str], root: Path) -> dict[str, object]:
    try:
        completed = subprocess.run(
            argv,
            cwd=root,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=120,
            check=False,
        )
    except FileNotFoundError as exc:
        return {"name": name, "status": "BLOCKED", "reason": str(exc)}
    except subprocess.TimeoutExpired:
        return {"name": name, "status": "FAIL", "reason": "timeout"}
    status = "PASS" if completed.returncode == 0 else "FAIL"
    return {
        "name": name,
        "status": status,
        "exit_code": completed.returncode,
        "output": completed.stdout[-4000:],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    claimed_head = os.environ.get("LINGDOC_ACCEPTANCE_HEAD_SHA")
    source_before = capture_source_binding(root, claimed_head, artifact_paths=(args.output,), runner_path=Path(__file__))
    if source_before["status"] != "PASS":
        print("BLOCKED: source binding: " + source_before["reason"])
        return 2
    checks = [
        run_check("acceptance_unit_tests", [sys.executable, "-m", "unittest", "discover", "-s", "scripts/acceptance", "-p", "test_*.py"], root),
    ]
    source_after = capture_source_binding(root, claimed_head, artifact_paths=(args.output,), ignore_new_artifacts=True,
                                          runner_path=Path(__file__))
    sha_result = compare_source_bindings(source_before, source_after)
    sha = source_before["checkout_commit"]
    sha_result["value"] = sha
    report = {
        "schema": "lingdoc-minimal-acceptance/v1",
        "recorded_at": datetime.now(timezone.utc).isoformat(),
        "event": os.environ.get("GITHUB_EVENT_NAME", "local"),
        "ref": os.environ.get("GITHUB_REF", "local"),
        "commit": sha,
        "commit_binding": sha_result,
        "checks": checks,
        "not_run": [
            {"name": "real_provider_timeout", "status": "NOT RUN"},
            {"name": "word_manual_roundtrip", "status": "NOT RUN"},
            {"name": "full_server_restart", "status": "NOT RUN"},
        ],
    }
    statuses = [item["status"] for item in report["checks"]]
    report["overall"] = "FAIL" if "FAIL" in statuses else "BLOCKED" if "BLOCKED" in statuses or sha_result["status"] != "PASS" else "PASS"
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"commit": sha, "overall": report["overall"]}, ensure_ascii=False))
    return 0 if report["overall"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
