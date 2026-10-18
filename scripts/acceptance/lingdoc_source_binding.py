"""Fail closed when an acceptance result cannot identify its tested checkout."""
from __future__ import annotations

import os
import hashlib
from pathlib import Path
import re
import subprocess
from typing import Mapping


SHA = re.compile(r"[0-9a-fA-F]{40}(?:[0-9a-fA-F]{24})?\Z")


def _git(root: Path, *arguments: str) -> str:
    completed = subprocess.run(
        ["git", "-C", str(root), *arguments], capture_output=True,
        text=True, encoding="utf-8", errors="strict", timeout=30, check=False,
    )
    if completed.returncode:
        # Do not forward arbitrary Git stderr into a public acceptance artifact.
        raise ValueError("Git identity lookup failed: " + arguments[0])
    return completed.stdout


def _inside(path: Path, parent: Path) -> bool:
    return path == parent or parent in path.parents


def _changes(raw: str) -> list[dict[str, str]]:
    entries = []
    items = iter(raw.split("\0"))
    for item in items:
        if not item:
            continue
        if len(item) < 4 or item[2] != " ":
            raise ValueError("Git status could not be parsed")
        row = {"status": item[:2], "path": item[3:]}
        if "R" in row["status"] or "C" in row["status"]:
            row["from"] = next(items)
        entries.append(row)
    return entries


def capture_source_binding(
    root: Path,
    claimed_head: str | None = None,
    *,
    artifact_paths: tuple[Path, ...] = (),
    ignore_new_artifacts: bool = False,
    environment: Mapping[str, str] | None = None,
    runner_path: Path | None = None,
) -> dict:
    """Capture Git identity; only postflight may ignore owned untracked outputs.

    Preflight must run before creating outputs. An artifact directory cannot
    contain tracked files, and existing untracked output files block preflight.
    This prevents an output exclusion from concealing an already dirty source.
    """
    root = root.resolve()
    environment = os.environ if environment is None else environment
    result = {"status": "BLOCKED", "root": str(root), "claimed_head": claimed_head,
              "checkout_commit": None, "checkout_tree": None, "claimed_tree": None,
              "candidate_kind": "unverified", "dirty": None, "changes": []}
    try:
        if runner_path is not None:
            runner_path = runner_path.resolve()
            helper_path = Path(__file__).resolve()
            result["runner"] = {
                "path": str(runner_path),
                "sha256": hashlib.sha256(runner_path.read_bytes()).hexdigest(),
                "inside_tested_checkout": _inside(runner_path, root),
                "helper_path": str(helper_path),
                "helper_sha256": hashlib.sha256(helper_path.read_bytes()).hexdigest(),
            }
        top = Path(_git(root, "rev-parse", "--show-toplevel").strip()).resolve()
        if top != root:
            raise ValueError("Acceptance root is not the Git worktree root")
        commit = _git(root, "rev-parse", "--verify", "HEAD").strip().lower()
        tree = _git(root, "rev-parse", "--verify", "HEAD^{tree}").strip().lower()
        if not SHA.fullmatch(commit) or not SHA.fullmatch(tree):
            raise ValueError("Git returned an invalid checkout identity")
        result.update(checkout_commit=commit, checkout_tree=tree)
        outputs = tuple(path.resolve() for path in artifact_paths)
        for output in outputs:
            if _inside(root, output):
                raise ValueError("Artifact output cannot be the worktree root or its ancestor")
            if _inside(output, root):
                relative = output.relative_to(root).as_posix()
                if _git(root, "ls-files", "-z", "--", relative):
                    raise ValueError("Artifact output overlaps tracked source files")
        changes = _changes(_git(root, "status", "--porcelain=v1", "-z", "--untracked-files=all"))
        ignored = []
        if ignore_new_artifacts:
            ignored = [row for row in changes if row["status"] == "??"
                       and any(_inside((root / row["path"]).resolve(), output) for output in outputs)]
            changes = [row for row in changes if row not in ignored]
        result.update(dirty=bool(changes), changes=changes,
                      ignored_untracked_artifacts=[row["path"] for row in ignored])
        if changes:
            raise ValueError("Working tree contains source changes")
        kind = "checkout"
        if claimed_head is not None:
            if not SHA.fullmatch(claimed_head):
                raise ValueError("Claimed head must be a full hexadecimal commit SHA")
            claimed_head = claimed_head.lower()
            resolved = _git(root, "rev-parse", "--verify", claimed_head + "^{commit}").strip().lower()
            claimed_tree = _git(root, "rev-parse", "--verify", claimed_head + "^{tree}").strip().lower()
            if resolved != claimed_head or not SHA.fullmatch(claimed_tree):
                raise ValueError("Claimed head could not be resolved exactly")
            result.update(claimed_head=claimed_head, claimed_tree=claimed_tree)
            if commit == claimed_head:
                kind = "exact_head"
            elif tree == claimed_tree:
                kind = "head_tree_equivalent"
            else:
                parents = _git(root, "show", "-s", "--format=%P", "HEAD").strip().lower().split()
                is_ci_merge = (
                    environment.get("GITHUB_EVENT_NAME") == "pull_request"
                    and environment.get("GITHUB_SHA", "").lower() == commit
                    and re.fullmatch(r"refs/pull/\d+/merge", environment.get("GITHUB_REF", ""))
                    and len(parents) == 2 and parents[1] == claimed_head
                )
                if not is_ci_merge:
                    raise ValueError("Claimed head does not match the tested checkout tree")
                kind = "integrated_candidate"
                result["parents"] = parents
        result.update(status="PASS", candidate_kind=kind)
    except (OSError, UnicodeError, ValueError, StopIteration, subprocess.TimeoutExpired) as error:
        result["reason"] = str(error) or "Git identity lookup failed"
    return result


def compare_source_bindings(before: dict, after: dict) -> dict:
    result = {"status": "BLOCKED", "before": before, "after": after}
    if before["status"] != "PASS" or after["status"] != "PASS":
        result["reason"] = before.get("reason") or after.get("reason") or "Source identity is unavailable"
    elif any(before.get(key) != after.get(key) for key in (
        "root", "checkout_commit", "checkout_tree", "claimed_head", "claimed_tree", "candidate_kind", "runner",
    )):
        result["reason"] = "Source identity changed during acceptance"
    else:
        result["status"] = "PASS"
    return result
