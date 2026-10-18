"""Decide whether a workflow should run for the latest PR update.

PR path filters describe the complete PR, so a later documentation-only commit
can otherwise retrigger every expensive job.  For ``synchronize`` events this
module scopes the decision to the commit range delivered by that update.  PR
creation/reopen events intentionally use the complete base..head range.
"""
from __future__ import annotations

import argparse
import fnmatch
import json
import os
from pathlib import Path
import subprocess
from typing import Any


def _run_git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip()


def event_range(event: dict[str, Any], root: Path) -> tuple[str | None, bool]:
    """Return ``(diff_range, forced)`` for the current Actions event."""
    event_name = os.environ.get("GITHUB_EVENT_NAME", "pull_request")
    if event_name == "workflow_dispatch":
        return None, True

    pr = event.get("pull_request")
    if not isinstance(pr, dict):
        raise ValueError("Expected a pull_request event; use workflow_dispatch for manual runs")
    base = pr.get("base", {}).get("sha")
    head = pr.get("head", {}).get("sha")
    if not isinstance(base, str) or not isinstance(head, str):
        raise ValueError("Pull request event is missing base/head SHAs")

    # A synchronize event is delivered for one ref update.  Some event
    # payloads expose its before/after range directly; otherwise the checked-
    # out head's parent is the safe boundary for the normal single-push case.
    # Creation/reopen events must inspect the whole PR.
    if event.get("action") == "synchronize":
        before = event.get("before")
        after = event.get("after")
        if (isinstance(before, str) and isinstance(after, str)
                and before and after and after == head):
            return f"{before}...{after}", False
        parent = _run_git(root, "rev-parse", f"{head}^")
        return f"{parent}...{head}", False
    return f"{base}...{head}", False


def changed_paths(root: Path, diff_range: str) -> list[str]:
    raw = subprocess.check_output(
        ["git", "diff", "--name-only", "--no-renames", "-z", diff_range, "--"],
        cwd=root,
    )
    return [os.fsdecode(item) for item in raw.split(b"\0") if item]


def matches(paths: list[str], patterns: list[str]) -> bool:
    positive = [pattern for pattern in patterns if not pattern.startswith("!")]
    negative = [pattern[1:] for pattern in patterns if pattern.startswith("!")]

    def match(path: str, pattern: str) -> bool:
        return fnmatch.fnmatchcase(path, pattern) or (
            pattern.startswith("**/") and fnmatch.fnmatchcase(path, pattern[3:])
        )

    for path in paths:
        if any(match(path, pattern) for pattern in negative):
            continue
        for pattern in positive:
            if match(path, pattern):
                return True
    return False


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--event", type=Path, default=os.environ.get("GITHUB_EVENT_PATH"))
    parser.add_argument("--root", type=Path, default=Path.cwd())
    parser.add_argument("--patterns", required=True, help="newline-separated fnmatch patterns")
    args = parser.parse_args()
    if args.event is None:
        parser.error("--event or GITHUB_EVENT_PATH is required")

    root = args.root.resolve()
    event = json.loads(Path(args.event).read_text(encoding="utf-8"))
    diff_range, forced = event_range(event, root)
    paths = [] if diff_range is None else changed_paths(root, diff_range)
    patterns = [line.strip() for line in args.patterns.splitlines() if line.strip()]
    should_run = forced or matches(paths, patterns)
    output = {
        "run": should_run,
        "forced": forced,
        "diff_range": diff_range,
        "changed_paths": paths,
        "patterns": patterns,
    }
    print(json.dumps(output, ensure_ascii=True, indent=2))
    if output_path := os.environ.get("GITHUB_OUTPUT"):
        with open(output_path, "a", encoding="utf-8") as handle:
            handle.write(f"run={str(should_run).lower()}\n")
            handle.write(f"changed_paths={len(paths)}\n")


if __name__ == "__main__":
    main()
