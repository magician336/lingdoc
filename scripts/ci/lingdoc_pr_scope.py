"""Select LingDoc checks from the actual Git diff, never from PR prose or labels."""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

PROVIDER_ROOTS = ("internal/lingdoc", "internal/evidence")
GOVERNANCE = (".github/workflows/lingdoc-review.yml", ".github/pull_request_template.md")


def classify(paths: list[str]) -> dict[str, bool]:
    """Conservative shared-code coverage; unrelated CLI changes stay cheap."""
    governance = any(p in GOVERNANCE or p.startswith("scripts/ci/") for p in paths)
    contract_change = any(p.startswith("docs/08-本轮实施方案/contracts/") for p in paths)
    backend = governance or contract_change or any(
        p in ("go.mod", "go.sum")
        or p.startswith(("internal/", "migrations/", "config/"))
        or (p.startswith("cmd/") and p.endswith(".go"))
        for p in paths
    )
    frontend = governance or contract_change or any(p.startswith("frontend/") for p in paths)
    docs = any(
        p == "docs/TEAM_DEVELOPMENT_TASKS.md"
        or p.startswith(("docs/08-本轮实施方案/", "docs/06-灵档产品开发规划/"))
        for p in paths
    )
    return {"contracts": governance or backend or frontend or docs,
            "backend": backend, "frontend": frontend}


def git_range(event: dict) -> str:
    pr = event.get("pull_request")
    if not isinstance(pr, dict):
        raise ValueError("Expected a pull_request event; use --all for a manual run")
    shas = [pr.get(part, {}).get("sha", "") for part in ("base", "head")]
    if not all(isinstance(s, str) and re.fullmatch(r"[0-9a-fA-F]{40}", s) for s in shas):
        raise ValueError("Event is missing valid base/head commit SHAs")
    return "...".join(shas)


def changed_paths(root: Path, diff_range: str) -> list[str]:
    # --no-renames includes BOTH sides of a move across a gate boundary.
    raw = subprocess.check_output(
        ["git", "diff", "--name-only", "--no-renames", "-z", diff_range, "--"], cwd=root)
    return [os.fsdecode(p) for p in raw.split(b"\0") if p]


def provider_packages(root: Path) -> list[str]:
    # Main can legitimately have only design artifacts before feature PRs merge.
    return [f"./{p}/..." for p in PROVIDER_ROOTS if any((root / p).rglob("*.go"))]


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--all", action="store_true", help="Run all applicable checks")
    parser.add_argument("--event", type=Path, default=os.environ.get("GITHUB_EVENT_PATH"))
    parser.add_argument("--root", type=Path, default=Path.cwd())
    args = parser.parse_args()
    root = args.root.resolve()
    if args.all:
        paths, diff_range = [], None
        scope = dict.fromkeys(("contracts", "backend", "frontend"), True)
    else:
        if args.event is None:
            parser.error("--event or GITHUB_EVENT_PATH is required")
        diff_range = git_range(json.loads(args.event.read_text(encoding="utf-8")))
        paths = changed_paths(root, diff_range)
        scope = classify(paths)
    packages = provider_packages(root) if scope["backend"] else []
    report = {"scope": scope, "diff_range": diff_range, "changed_paths": paths,
              "provider_packages": packages,
              "note": "Selection only; not a test result or an approval."}
    print(json.dumps(report, ensure_ascii=True, indent=2))
    if output := os.environ.get("GITHUB_OUTPUT"):
        with open(output, "a", encoding="utf-8") as handle:
            for key, value in scope.items():
                handle.write(f"{key}={str(value).lower()}\n")
            handle.write(f"provider_tests={str(bool(packages)).lower()}\n")
    if diff_range:
        subprocess.run(["git", "diff", "--check", diff_range, "--"], cwd=root, check=True, stdout=sys.stderr)


if __name__ == "__main__":
    main()
