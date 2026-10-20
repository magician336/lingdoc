"""Decide what a pull request must be tested against, and whether at all.

The unit of validation is **(surface content, base content)**: a pull request is
re-tested when the content of its tested surface changed, or when the base moved
underneath it.  Both facts are read from Git, never from PR prose or labels.

Why this exists
---------------
The previous rule scoped a ``synchronize`` event to the *last push*.  After a
baseline merge that diff is the whole incoming baseline, so every expensive job
re-ran on code the pull request never touched.  The baseline commits were
already validated by the baseline's own CI, and the pull request's own
contribution had not changed at all.  This module instead derives the surface
from the pull request's own contribution (``base...head``) and compares it
across the ref update, so a baseline merge re-tests the contribution against
the new baseline instead of re-running everything.

Nothing here is a test result or an approval: it only selects scope.
"""
from __future__ import annotations

import argparse
import dataclasses
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from typing import Any

SHA = re.compile(r"[0-9a-fA-F]{40}")

# The LingDoc provider roots the PR gate owns.  A contribution outside them is
# not LingDoc provider work and must not be claimed as a provider test.
PROVIDER_ROOTS = ("internal/lingdoc", "internal/evidence",
                  "internal/models/acceptancebudget", "cmd/acceptance-budget-reconcile")
GOVERNANCE = (".github/workflows/lingdoc-review.yml", ".github/pull_request_template.md")
CONTRACT_DIR = "docs/08-本轮实施方案/contracts/"
FRONTEND_ROOT = "frontend/"
GO_SUFFIX = ".go"


@dataclasses.dataclass(frozen=True)
class Surface:
    """The tested surface of one pull request at one commit."""

    base: str
    head: str
    contribution: tuple[str, ...] = ()
    go_packages: tuple[str, ...] = ()
    frontend_dirs: tuple[str, ...] = ()
    contract_changed: bool = False
    governance_changed: bool = False

    def fingerprint(self) -> str:
        """Content identity of the surface: which paths, and what they contain."""
        parts = [f"go:{package}" for package in self.go_packages]
        parts += [f"fe:{directory}" for directory in self.frontend_dirs]
        return hashlib.sha256("|".join(sorted(parts)).encode("utf-8")).hexdigest()


@dataclasses.dataclass(frozen=True)
class Decision:
    """What to run, on what surface, and why."""

    run_code: bool
    run_targeted: bool
    reason: str
    surface: tuple[str, ...]
    base: str
    contracts: bool
    backend: bool
    frontend: bool
    provider_packages: tuple[str, ...]
    contribution: tuple[str, ...] = ()
    base_moved: bool = False
    surface_changed: bool = False
    forced: bool = False

    def as_outputs(self) -> dict[str, str]:
        return {
            "run_code": str(self.run_code).lower(),
            "run_targeted": str(self.run_targeted).lower(),
            "reason": self.reason,
            "surface": " ".join(self.surface),
            "base": self.base,
            "contracts": str(self.contracts).lower(),
            "backend": str(self.backend).lower(),
            "frontend": str(self.frontend).lower(),
            "provider_tests": str(bool(self.provider_packages)).lower(),
            "provider_packages": " ".join(self.provider_packages),
            "base_moved": str(self.base_moved).lower(),
            "surface_changed": str(self.surface_changed).lower(),
        }


def run_git(root: Path, *args: str) -> str:
    return subprocess.check_output(
        ["git", *args], cwd=root, text=True, stderr=subprocess.PIPE
    ).strip()


def changed_paths(root: Path, diff_range: str) -> list[str]:
    # --no-renames includes BOTH sides of a move across a gate boundary.
    raw = subprocess.check_output(
        ["git", "diff", "--name-only", "--no-renames", "-z", diff_range, "--"], cwd=root)
    return [os.fsdecode(item) for item in raw.split(b"\0") if item]


def event_shas(event: dict[str, Any]) -> tuple[str, str]:
    pr = event.get("pull_request")
    if not isinstance(pr, dict):
        raise ValueError("Expected a pull_request event; use --all for a manual run")
    base = pr.get("base", {}).get("sha", "")
    head = pr.get("head", {}).get("sha", "")
    if not (isinstance(base, str) and SHA.fullmatch(base)):
        raise ValueError("Event is missing a valid base commit SHA")
    if not (isinstance(head, str) and SHA.fullmatch(head)):
        raise ValueError("Event is missing a valid head commit SHA")
    return base, head


def _tree_spec(sha: str, path: str) -> str:
    """The tree specification of a path at a commit.

    The repository root is spelled with an empty path (``<sha>:``); ``<sha>:.``
    is not a valid revision.
    """
    return f"{sha}:" if path in (".", "") else f"{sha}:{path}"


def _tree_exists(root: Path, sha: str, path: str) -> bool:
    try:
        run_git(root, "rev-parse", "--verify", _tree_spec(sha, path))
    except subprocess.CalledProcessError:
        return False
    return True


def go_package_dirs(root: Path, sha: str, paths: list[str]) -> list[str]:
    """Package directories the contribution touches, at the given commit."""
    directories: set[str] = set()
    for path in paths:
        if not path.endswith(GO_SUFFIX):
            continue
        directory = str(Path(path).parent).replace("\\", "/")
        if directory in (".", ""):
            directory = "."
        if not _tree_exists(root, sha, directory):
            continue
        directories.add(directory)
    return sorted(directories)


def frontend_dirs(paths: list[str]) -> list[str]:
    directories: set[str] = set()
    for path in paths:
        if not path.startswith(FRONTEND_ROOT):
            continue
        directory = str(Path(path).parent).replace("\\", "/")
        directories.add(directory)
    return sorted(directories)


def surface_at(root: Path, base: str, sha: str) -> Surface:
    """The tested surface of the pull request as of one commit."""
    contribution = tuple(changed_paths(root, f"{base}...{sha}"))
    return Surface(
        base=base,
        head=sha,
        contribution=contribution,
        go_packages=tuple(go_package_dirs(root, sha, list(contribution))),
        frontend_dirs=tuple(frontend_dirs(list(contribution))),
        contract_changed=any(path.startswith(CONTRACT_DIR) for path in contribution),
        governance_changed=any(
            path in GOVERNANCE or path.startswith("scripts/ci/") for path in contribution),
    )


def content_fingerprint(root: Path, sha: str, surface: Surface) -> str:
    """Hash the content of the surface at one commit."""
    digest = hashlib.sha256()
    for package in surface.go_packages:
        try:
            tree = run_git(root, "rev-parse", _tree_spec(sha, package))
        except subprocess.CalledProcessError:
            tree = "absent"
        digest.update(f"go:{package}:{tree}\n".encode("utf-8"))
    for directory in surface.frontend_dirs:
        try:
            tree = run_git(root, "rev-parse", _tree_spec(sha, directory))
        except subprocess.CalledProcessError:
            tree = "absent"
        digest.update(f"fe:{directory}:{tree}\n".encode("utf-8"))
    return digest.hexdigest()


def base_moved_under(root: Path, base: str, before: str, after: str) -> bool:
    """True when the base changed underneath the branch between two heads."""
    try:
        old_base = run_git(root, "merge-base", base, before)
        new_base = run_git(root, "merge-base", base, after)
    except subprocess.CalledProcessError:
        return False
    return old_base != new_base


def _provider_packages(surface: Surface) -> list[str]:
    packages: list[str] = []
    for package in surface.go_packages:
        if any(package == root or package.startswith(root + "/") for root in PROVIDER_ROOTS):
            packages.append(f"./{package}/...")
    return packages


def _full_decision(base: str, reason: str) -> Decision:
    return Decision(
        run_code=True, run_targeted=True, reason=reason, surface=(), base=base,
        contracts=True, backend=True, frontend=True, provider_packages=(), forced=True)


def existing_provider_roots(root: Path) -> list[str]:
    """The provider roots that exist in this checkout."""
    return [f"./{path}/..." for path in PROVIDER_ROOTS if any((root / path).rglob("*.go"))]


def full_decision(root: Path, reason: str) -> Decision:
    """Every applicable check, over the whole provider surface."""
    return Decision(
        run_code=True, run_targeted=True, reason=reason, surface=(), base="",
        contracts=True, backend=True, frontend=True,
        provider_packages=tuple(existing_provider_roots(root)), forced=True)


def decide_or_full(root: Path, event: dict[str, Any]) -> Decision:
    """Decide, and fall back to the full scope when the surface is undecidable.

    A scope-discovery failure must cost compute, never correctness: when the
    surface cannot be resolved every applicable check runs rather than
    silently skipping.
    """
    try:
        return decide(root, event)
    except (subprocess.CalledProcessError, ValueError, OSError) as error:
        print(f"warning: unable to resolve the risk surface ({error}); "
              "falling back to the full scope", file=sys.stderr)
        base = ""
        try:
            base, _ = event_shas(event)
        except ValueError:
            pass
        return Decision(
            run_code=True, run_targeted=True,
            reason="risk surface undetermined, fell back to the full scope",
            surface=(), base=base, contracts=True, backend=True, frontend=True,
            provider_packages=tuple(existing_provider_roots(root)), forced=True)


def decide(root: Path, event: dict[str, Any], *, force_all: bool = False) -> Decision:
    """Select the surface and the trigger for one Actions event."""
    if force_all:
        return _full_decision("", "full scope requested")
    base, head = event_shas(event)
    action = event.get("action")
    after = event.get("after")
    before = event.get("before")
    after_sha = after if isinstance(after, str) and SHA.fullmatch(after) else head
    before_sha = before if isinstance(before, str) and SHA.fullmatch(before) else None

    current = surface_at(root, base, after_sha)
    previous = surface_at(root, base, before_sha) if before_sha else None

    surface_changed = True
    base_moved = False
    if previous is not None:
        base_moved = base_moved_under(root, base, before_sha, after_sha)
        surface_changed = (
            previous.go_packages != current.go_packages
            or previous.frontend_dirs != current.frontend_dirs
            or content_fingerprint(root, before_sha, previous)
            != content_fingerprint(root, after_sha, current)
        )
    elif action in ("opened", "reopened", "ready_for_review"):
        surface_changed = True

    provider_packages = _provider_packages(current)
    surface = list(current.go_packages) + list(current.frontend_dirs)
    governance = current.governance_changed
    contracts = current.contract_changed or governance
    backend = bool(current.go_packages) or governance or current.contract_changed
    frontend = bool(current.frontend_dirs) or governance or current.contract_changed

    if governance:
        # The gate judges itself: a change to the gate or to the scripts that
        # decide the scope must be judged by the whole gate, never by a
        # narrowed surface.
        surface_changed = True
        surface = list(existing_provider_roots(root)) + [FRONTEND_ROOT.rstrip("/")]
        provider_packages = list(existing_provider_roots(root))
        contracts = backend = frontend = True

    if not surface_changed and not base_moved:
        reason = "tested surface unchanged and base unchanged"
    elif surface_changed and base_moved:
        reason = "contribution surface changed and the base moved under the branch"
    elif surface_changed:
        reason = "contribution surface changed"
    else:
        reason = "the base moved under the branch; the contribution is re-tested against it"

    return Decision(
        run_code=surface_changed,
        run_targeted=surface_changed or base_moved,
        reason=reason,
        surface=tuple(surface),
        base=base,
        contracts=contracts,
        backend=backend,
        frontend=frontend,
        provider_packages=tuple(provider_packages),
        contribution=current.contribution,
        base_moved=base_moved,
        surface_changed=surface_changed,
    )


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--all", action="store_true", help="Run all applicable checks")
    parser.add_argument("--event", type=Path, default=os.environ.get("GITHUB_EVENT_PATH"))
    parser.add_argument("--root", type=Path, default=Path.cwd())
    args = parser.parse_args()
    root = args.root.resolve()

    if args.all:
        decision = full_decision(root, "full scope requested")
    else:
        if args.event is None:
            parser.error("--event or GITHUB_EVENT_PATH is required")
        event = json.loads(args.event.read_text(encoding="utf-8"))
        decision = decide_or_full(root, event)

    report = {
        "decision": decision.as_outputs(),
        "contribution": list(decision.contribution),
        "note": "Selection only; not a test result or an approval.",
    }
    print(json.dumps(report, ensure_ascii=True, indent=2))
    if output_path := os.environ.get("GITHUB_OUTPUT"):
        with open(output_path, "a", encoding="utf-8") as handle:
            for key, value in decision.as_outputs().items():
                handle.write(f"{key}={value}\n")


if __name__ == "__main__":
    main()
