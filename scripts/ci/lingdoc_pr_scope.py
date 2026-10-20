"""Select LingDoc checks from the actual Git diff, never from PR prose or labels."""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

import lingdoc_format_scope
import lingdoc_risk_surface as risk_surface

PROVIDER_ROOTS = ("internal/lingdoc", "internal/evidence",
                  "internal/models/acceptancebudget", "cmd/acceptance-budget-reconcile")
GOVERNANCE = (".github/workflows/lingdoc-review.yml", ".github/pull_request_template.md")


def _run_git(root: Path, *args: str) -> str:
    return subprocess.check_output(
        ["git", *args], cwd=root, text=True, stderr=subprocess.PIPE
    ).strip()


def _range_is_resolvable(root: Path, before: str, after: str) -> bool:
    """Check that a three-dot diff has both commits and a merge base."""
    try:
        _run_git(root, "merge-base", before, after)
    except subprocess.CalledProcessError:
        return False
    return True


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


def latest_git_range(root: Path, event: dict) -> str:
    """Scope a synchronize event to the ref update ending at the PR head."""
    pr = event.get("pull_request")
    if not isinstance(pr, dict) or event.get("action") != "synchronize":
        return git_range(event)
    head = pr.get("head", {}).get("sha", "")
    if not isinstance(head, str) or not re.fullmatch(r"[0-9a-fA-F]{40}", head):
        raise ValueError("Event is missing a valid pull request head SHA")
    before = event.get("before")
    after = event.get("after")
    if (isinstance(before, str) and isinstance(after, str)
            and re.fullmatch(r"[0-9a-fA-F]{40}", before)
            and re.fullmatch(r"[0-9a-fA-F]{40}", after)
            and after == head
            and _range_is_resolvable(root, before, after)):
        return f"{before}...{after}"
    parent = _run_git(root, "rev-parse", f"{head}^")
    return f"{parent}...{head}"


def changed_paths(root: Path, diff_range: str) -> list[str]:
    # --no-renames includes BOTH sides of a move across a gate boundary.
    raw = subprocess.check_output(
        ["git", "diff", "--name-only", "--no-renames", "-z", diff_range, "--"], cwd=root)
    return [os.fsdecode(p) for p in raw.split(b"\0") if p]


def provider_packages(root: Path) -> list[str]:
    # Main can legitimately have only design artifacts before feature PRs merge.
    return [f"./{p}/..." for p in PROVIDER_ROOTS if any((root / p).rglob("*.go"))]


def select_paths(root: Path, event: dict, latest: bool) -> tuple[str | None, list[str], dict[str, bool]]:
    """Select a scope conservatively, never failing a gate on diff discovery."""
    try:
        diff_range = latest_git_range(root, event) if latest else git_range(event)
        paths = changed_paths(root, diff_range)
        return diff_range, paths, classify(paths)
    except subprocess.CalledProcessError as error:
        print(
            f"warning: unable to resolve the scoped PR diff ({error}); "
            "falling back to the complete PR range",
            file=sys.stderr,
        )

    try:
        diff_range = git_range(event)
        paths = changed_paths(root, diff_range)
        return diff_range, paths, classify(paths)
    except subprocess.CalledProcessError as error:
        print(
            f"warning: unable to resolve the complete PR diff ({error}); "
            "running all applicable LingDoc checks",
            file=sys.stderr,
        )
        return None, [], dict.fromkeys(("contracts", "backend", "frontend"), True)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--all", action="store_true", help="Run all applicable checks")
    parser.add_argument("--latest", action="store_true",
                        help="On synchronize, scope checks to the latest ref update")
    parser.add_argument("--event", type=Path, default=os.environ.get("GITHUB_EVENT_PATH"))
    parser.add_argument("--root", type=Path, default=Path.cwd())
    args = parser.parse_args()
    root = args.root.resolve()
    event: dict = {}
    if args.all:
        paths, diff_range, whitespace_range = [], None, None
        decision = risk_surface.full_decision(root, "full scope requested")
    else:
        if args.event is None:
            parser.error("--event or GITHUB_EVENT_PATH is required")
        event = json.loads(args.event.read_text(encoding="utf-8"))
        diff_range, paths, _ = select_paths(root, event, args.latest)
        # Latest-update scope may include upstream merge commits. Whitespace is
        # always checked over the complete PR, including earlier topic commits.
        whitespace_range = git_range(event)
        decision = risk_surface.decide_or_full(root, event)
    # The area flags describe the pull request's own contribution, not the last
    # push: a documentation-only push must not shrink the area a later
    # baseline merge is judged against.
    scope = classify(list(decision.contribution))
    packages = list(decision.provider_packages) if decision.run_targeted else []
    frontend = bool(scope["frontend"]) and decision.run_targeted
    format_only, format_reasons = (False, ["the formatting-only fast path does not apply to this event"])
    if not args.all and lingdoc_format_scope.fast_path_applies(event)[0]:
        format_only, format_reasons = lingdoc_format_scope.format_only_update(root, diff_range)
    report = {"scope": scope, "diff_range": diff_range,
              "whitespace_diff_range": whitespace_range, "changed_paths": paths,
              "provider_packages": packages,
              "format_only": format_only, "format_reasons": format_reasons,
              "decision": decision.as_outputs(),
              "note": "Selection only; not a test result or an approval."}
    print(json.dumps(report, ensure_ascii=True, indent=2))
    if output := os.environ.get("GITHUB_OUTPUT"):
        with open(output, "a", encoding="utf-8") as handle:
            handle.write(f"contracts={str(bool(scope['contracts'])).lower()}\n")
            handle.write(f"backend={str(bool(scope['backend'])).lower()}\n")
            handle.write(f"frontend={str(frontend).lower()}\n")
            handle.write(f"provider_tests={str(bool(packages)).lower()}\n")
            handle.write(f"format_only={str(format_only).lower()}\n")
            handle.write(f"format_reasons={' | '.join(format_reasons)}\n")
            for key, value in decision.as_outputs().items():
                if key in ("contracts", "backend", "frontend", "provider_tests"):
                    continue
                handle.write(f"{key}={value}\n")
    check_range = whitespace_range or diff_range
    if check_range:
        subprocess.run(["git", "diff", "--check", check_range, "--"], cwd=root, check=True, stdout=sys.stderr)


if __name__ == "__main__":
    main()
