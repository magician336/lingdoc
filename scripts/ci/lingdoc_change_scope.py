"""Whether a workflow-level gate runs for the latest push or pull request update.

Path filters describe the complete push, so a later documentation-only commit
can otherwise retrigger every expensive job.  For ``synchronize`` events this
module scopes the decision to the commit range delivered by that update.  PR
creation/reopen events intentionally use the complete base..head range, and
``push`` events scope to the before..after range GitHub reports for that push.

The decision itself lives in ``lingdoc_gate_decision``; this script is the
workflow-level adapter around it.  It asks for the decision, publishes it, and
writes the outputs the workflow's conditions read.

Ticket T3 replaces these calls with the decision results directly.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
from typing import Any, Mapping, Sequence

import lingdoc_gate_decision as gate

_run_git = gate.run_git


def _range_is_resolvable(root: Path, before: str, after: str) -> bool:
    """Whether a three-dot diff has both commits and a merge base."""
    return gate.range_is_resolvable(root, before, after, git=_run_git)


def _full_pr_range(event: Mapping[str, Any]) -> str:
    """The complete pull request range: ``base...head``."""
    return gate.pr_range(event, require_sha=False)


def _push_range(event: Mapping[str, Any], root: Path) -> str:
    """The range a branch push reports."""
    return gate.push_range(root, event, git=_run_git)


def event_range(event: Mapping[str, Any],
                root: Path) -> tuple[str | None, bool]:
    """Return ``(diff_range, forced)`` for the current Actions event."""
    return gate.event_range(
        root, event,
        event_name=os.environ.get("GITHUB_EVENT_NAME", "pull_request"),
        git=_run_git,
    )


def changed_paths(root: Path, diff_range: str) -> list[str]:
    """The files a diff range touches."""
    return gate.changed_paths(root, diff_range)


def matches(paths: Sequence[str], patterns: Sequence[str]) -> bool:
    """Whether any path matches the gitignore-style pattern table."""
    return gate.matches(paths, patterns)


def select_paths(root: Path, event: Mapping[str, Any]) -> tuple[str | None, bool, list[str]]:
    """Select paths without making a scope-discovery failure fail the workflow."""
    return gate.select_paths(
        root, event,
        event_range_fn=lambda r, e: event_range(e, r),
        paths_fn=changed_paths,
        full_range_fn=lambda r, e: _full_pr_range(e),
    )


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
    diff_range, forced, paths = select_paths(root, event)
    patterns = [line.strip() for line in args.patterns.splitlines() if line.strip()]
    # The trigger follows the pull request's own contribution, not the last
    # push: a baseline merge changes no contribution, so the repository-wide
    # suites stay away while the targeted checks re-test the contribution
    # against the new baseline.
    decision = gate.decide(event, root)
    outputs = gate.write_outputs(decision, patterns=patterns)
    output = {
        "run": outputs["run"] == "true",
        "forced": forced,
        "diff_range": diff_range,
        "changed_paths": paths,
        "patterns": patterns,
        "format_only": decision.format_only,
        "format_reasons": list(decision.format_reasons),
        "decision": outputs,
    }
    print(json.dumps(output, ensure_ascii=True, indent=2))
    if output_path := os.environ.get("GITHUB_OUTPUT"):
        with open(output_path, "a", encoding="utf-8") as handle:
            for key, value in outputs.items():
                handle.write(f"{key}={value}\n")


if __name__ == "__main__":
    main()
