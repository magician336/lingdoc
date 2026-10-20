"""The pull request gate's scope script.

The decision lives in ``lingdoc_gate_decision``; this script is the pull
request gate's adapter.  It asks the decision module for the decision,
publishes it, and keeps the pull request gate's own boundary check -- the
whitespace check over the complete pull request range.

The ownership knowledge (provider roots, governance file set) is registered
once, in the gate surface manifest; this script reads it from there.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
from typing import Any, Mapping

import lingdoc_gate_decision as gate

# The ownership knowledge is registered once, in the gate surface manifest.
PROVIDER_ROOTS = gate.PROVIDER_ROOTS
GOVERNANCE = gate.GOVERNANCE_FILES

_run_git = gate.run_git


def git_range(event: Mapping[str, Any]) -> str:
    """The complete pull request range: ``base...head``."""
    return gate.pr_range(event)


def latest_git_range(root: Path, event: Mapping[str, Any]) -> str:
    """Scope a synchronize event to the ref update ending at the PR head."""
    return gate.latest_range(root, event, git=_run_git)


def changed_paths(root: Path, diff_range: str) -> list[str]:
    """The files a diff range touches."""
    return gate.changed_paths(root, diff_range)


def classify(paths) -> dict[str, bool]:
    """Conservative shared-code coverage; unrelated CLI changes stay cheap."""
    return gate.classify(paths)


def provider_packages(root: Path) -> list[str]:
    """The provider roots that exist in this checkout."""
    return gate.existing_provider_roots(root)


def select_paths(root: Path, event: Mapping[str, Any],
                 latest: bool) -> tuple[str | None, list[str], dict[str, bool]]:
    """Select a scope conservatively, never failing a gate on diff discovery."""
    diff_range, _forced, paths = gate.select_paths(
        root, event,
        event_range_fn=(lambda r, e: (latest_git_range(r, e), False)) if latest
        else (lambda r, e: (git_range(e), False)),
        paths_fn=changed_paths,
        full_range_fn=lambda r, e: git_range(e),
    )
    if diff_range is None:
        # No range at all: nothing can be claimed, so everything is claimed.
        scope = dict.fromkeys(("contracts", "backend", "frontend"), True)
    else:
        scope = classify(paths)
    return diff_range, paths, scope


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--all", action="store_true", help="Run all applicable checks")
    parser.add_argument("--latest", action="store_true",
                        help="On synchronize, scope checks to the latest ref update")
    parser.add_argument("--event", type=Path, default=os.environ.get("GITHUB_EVENT_PATH"))
    parser.add_argument("--root", type=Path, default=Path.cwd())
    args = parser.parse_args()
    root = args.root.resolve()

    if args.all:
        gate.publish({}, root, all_checks=True)
        return
    if args.event is None:
        parser.error("--event or GITHUB_EVENT_PATH is required")
    event = json.loads(args.event.read_text(encoding="utf-8"))
    # The decision module publishes the decision and checks the boundary; the
    # pull request gate reads the same report.
    gate.publish(event, root)


if __name__ == "__main__":
    main()
