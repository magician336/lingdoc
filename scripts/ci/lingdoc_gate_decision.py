"""The gate decision: one deep module, two public entry points.

What this module owns
----------------------
Every fact the gate needs in order to decide *what runs*:

* the diff ranges -- the pull request's own range and the range of the last
  push;
* the changed files of a range;
* the tree hashes behind the tested surface;
* the tested surface itself (Go packages, frontend directories);
* the gate-surface classification: which surface a change belongs to;
* the formatting fast path;
* the ownership knowledge -- provider roots, contracts directory, governance
  file set, frontend root -- which is **read from the gate surface manifest**
  instead of being written down a second time.

The public interface is two functions
-------------------------------------
``decide(event, root)``
    Answers one event with a typed :class:`Decision`.  It never lets an
    undecidable surface escape: it falls back to the full scope and says so
    in the reason.  There is exactly one recovery path, and it lives here.

``write_outputs(decision)``
    The one place where a decision becomes strings, for the GitHub Actions
    output boundary and for the gate artifacts.

Everything else in this module is a helper that the transitional shells call.
None of them decides anything on its own; the decision is made once, in
:func:`decide`.

Why one module
--------------
The gate used to decide twice: once per scope script, plus the knowledge of
what belongs to what was written down in three places.  Two decisions about
one event can disagree, and a knowledge base with three copies is a knowledge
base that drifts.  This module is the one place that answers "what runs, on
what surface, and why".
"""
from __future__ import annotations

import argparse
import dataclasses
import fnmatch
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from typing import Any, Callable, Mapping, Sequence

import lingdoc_format_scope
import lingdoc_gate_surfaces as manifest

SHA = re.compile(r"[0-9a-fA-F]{40}")
GO_SUFFIX = ".go"

# The ownership knowledge lives in the gate surface manifest.  It is read from
# there, never written down here.
PROVIDER_ROOTS = tuple(manifest.PROVIDER_ROOTS)
CONTRACT_DIR = manifest.CONTRACT_DIR
GOVERNANCE_FILES = tuple(manifest.GOVERNANCE_FILES)
GOVERNANCE_DIRS = tuple(manifest.GOVERNANCE_DIRS)
FRONTEND_ROOT = manifest.FRONTEND_ROOT


# --------------------------------------------------------------------------
# the types
# --------------------------------------------------------------------------
@dataclasses.dataclass(frozen=True)
class JobDecision:
    """One gate job's verdict for one event.

    ``run`` says whether the checks this job owns are selected for this
    event.  For a workflow-level gate that is the job's trigger; for the pull
    request gate it is the condition of the step that owns the surface.
    """

    workflow: str
    job: str
    job_name: str
    surface: str
    run: bool
    gate_level: str
    path_kind: str
    paths: tuple[str, ...]
    skipped_on_format_only: bool

    @property
    def key(self) -> str:
        """The readable output key of this job, e.g. ``job.frontend.verify``."""
        return f"job.{Path(self.workflow).stem}.{self.job}"


@dataclasses.dataclass(frozen=True)
class Decision:
    """What to run, on what surface, and why.

    The decision is a value: it crosses no process boundary as a string.  The
    one place where it becomes strings is :func:`write_outputs`.
    """

    # -- what to run ------------------------------------------------------
    jobs: tuple[JobDecision, ...]
    run_code: bool
    run_targeted: bool
    provider_packages: tuple[str, ...]
    contracts: bool
    backend: bool
    frontend: bool

    # -- why, and against what --------------------------------------------
    reason: str
    surface: tuple[str, ...]
    base: str
    fingerprint: str
    base_moved: bool
    surface_changed: bool
    forced: bool
    governance_changed: bool
    undecided: bool
    contribution: tuple[str, ...]

    # -- the event's own diff ---------------------------------------------
    diff_range: str | None
    changed_paths: tuple[str, ...]
    format_only: bool
    format_reasons: tuple[str, ...]

    @property
    def provider_tests(self) -> bool:
        """Whether the provider packages are actually going to be tested."""
        return bool(self.provider_packages) and self.run_targeted

    def as_outputs(self) -> dict[str, str]:
        """The string form of this decision (see :func:`write_outputs`)."""
        return write_outputs(self)


# --------------------------------------------------------------------------
# git primitives
# --------------------------------------------------------------------------
def run_git(root: Path, *args: str) -> str:
    """Run a git command in ``root`` and return its trimmed output."""
    return subprocess.check_output(
        ["git", *args], cwd=root, text=True, stderr=subprocess.PIPE
    ).strip()


def changed_paths(root: Path, diff_range: str) -> list[str]:
    """The files a diff range touches.

    ``--no-renames`` includes BOTH sides of a move across a gate boundary.
    """
    raw = subprocess.check_output(
        ["git", "diff", "--name-only", "--no-renames", "-z", diff_range, "--"], cwd=root)
    return [os.fsdecode(item) for item in raw.split(b"\0") if item]


def matches(paths: Sequence[str], patterns: Sequence[str]) -> bool:
    """Whether any path matches the gitignore-style pattern table.

    A negative pattern (``!cli/**``) excludes a path even when a positive
    pattern would match it.
    """
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


# --------------------------------------------------------------------------
# the ranges
# --------------------------------------------------------------------------
def _event_shas(event: Mapping[str, Any]) -> tuple[str, str]:
    """The base and head commits of a pull request event."""
    pull_request = event.get("pull_request")
    if not isinstance(pull_request, Mapping):
        raise ValueError("Expected a pull_request event; use --all for a manual run")
    base = pull_request.get("base", {}).get("sha", "")
    head = pull_request.get("head", {}).get("sha", "")
    if not (isinstance(base, str) and SHA.fullmatch(base)):
        raise ValueError("Event is missing a valid base commit SHA")
    if not (isinstance(head, str) and SHA.fullmatch(head)):
        raise ValueError("Event is missing a valid head commit SHA")
    return base, head


def pr_range(event: Mapping[str, Any], *, require_sha: bool = True) -> str:
    """The complete pull request range: ``base...head``.

    ``require_sha`` says whether the two commits must look like commit SHAs.
    The workflow-level gate only needs two named revisions; the pull request
    gate insists on real commits.
    """
    if require_sha:
        base, head = _event_shas(event)
        return f"{base}...{head}"
    pull_request = event.get("pull_request")
    if not isinstance(pull_request, Mapping):
        raise ValueError("Expected a pull_request event; use --all for a manual run")
    base = pull_request.get("base", {}).get("sha")
    head = pull_request.get("head", {}).get("sha")
    if not isinstance(base, str) or not isinstance(head, str):
        raise ValueError("Pull request event is missing base/head SHAs")
    return f"{base}...{head}"


def range_is_resolvable(root: Path, before: str, after: str,
                        git: Callable[..., str] = run_git) -> bool:
    """Whether a three-dot diff has both commits and a merge base."""
    try:
        git(root, "merge-base", before, after)
    except subprocess.CalledProcessError:
        return False
    return True


def push_range(root: Path, event: Mapping[str, Any],
               git: Callable[..., str] = run_git) -> str:
    """The range a branch push reports.

    A push that creates a branch reports an all-zero ``before``, which names
    no commit; the parent of the new head is then the only usable boundary.
    """
    after = event.get("after")
    before = event.get("before")
    if not isinstance(after, str) or not after:
        raise ValueError("Push event is missing the after SHA")
    if isinstance(before, str) and before and set(before) != {"0"}:
        return f"{before}...{after}"
    parent = git(root, "rev-parse", f"{after}^")
    return f"{parent}...{after}"


def latest_range(root: Path, event: Mapping[str, Any],
                 git: Callable[..., str] = run_git, *,
                 require_sha: bool = True) -> str:
    """Scope a pull request update to the ref update that ended at its head."""
    action = event.get("action")
    if action != "synchronize":
        return pr_range(event, require_sha=require_sha)
    head = event.get("pull_request", {}).get("head", {}).get("sha", "")
    if not isinstance(head, str) or not head or (require_sha and not SHA.fullmatch(head)):
        raise ValueError("Event is missing a valid pull request head SHA")
    before = event.get("before")
    after = event.get("after")
    usable = (isinstance(before, str) and isinstance(after, str)
              and before and after
              and (not require_sha or (SHA.fullmatch(before) and SHA.fullmatch(after)))
              and after == head)
    if usable and range_is_resolvable(root, before, after, git):
        return f"{before}...{after}"
    parent = git(root, "rev-parse", f"{head}^")
    return f"{parent}...{head}"


def _event_kind(event: Mapping[str, Any], event_name: str | None) -> str:
    """Which kind of event this is: pull request, push, or manual."""
    if event_name:
        if event_name in ("push", "workflow_dispatch"):
            return event_name
        return "pull_request"
    if isinstance(event.get("pull_request"), Mapping):
        return "pull_request"
    if isinstance(event.get("after"), str):
        return "push"
    return "workflow_dispatch"


def event_range(root: Path, event: Mapping[str, Any], *, latest: bool = True,
                event_name: str | None = None,
                git: Callable[..., str] = run_git,
                require_sha: bool = False) -> tuple[str | None, bool]:
    """Return ``(diff_range, forced)`` for the current event.

    A manual run has no range and is forced.  A push reports its own range.
    A pull request update is scoped to the ref update that was delivered;
    every other pull request event is scoped to the complete pull request.
    """
    kind = _event_kind(event, event_name)
    if kind == "workflow_dispatch":
        return None, True
    if kind == "push":
        return push_range(root, event, git), False
    if latest:
        return latest_range(root, event, git, require_sha=require_sha), False
    return pr_range(event, require_sha=require_sha), False


def select_paths(root: Path, event: Mapping[str, Any], *, latest: bool = True,
                 event_name: str | None = None,
                 git: Callable[..., str] = run_git,
                 event_range_fn: Callable[..., tuple[str | None, bool]] | None = None,
                 paths_fn: Callable[[Path, str], list[str]] | None = None,
                 full_range_fn: Callable[..., str] | None = None
                 ) -> tuple[str | None, bool, list[str]]:
    """Select the files of the event's range, without ever failing a gate.

    When the scoped range cannot be resolved the complete pull request range
    is used instead; when that fails too the event is forced and no file is
    claimed.
    """
    if event_range_fn is None:
        def event_range_fn(root: Path, event: Mapping[str, Any]):
            return event_range(root, event, latest=latest, event_name=event_name, git=git)
    if paths_fn is None:
        paths_fn = changed_paths
    if full_range_fn is None:
        def full_range_fn(root: Path, event: Mapping[str, Any]) -> str:
            return pr_range(event)

    try:
        diff_range, forced = event_range_fn(root, event)
        paths = [] if diff_range is None else paths_fn(root, diff_range)
        return diff_range, forced, paths
    except (subprocess.CalledProcessError, ValueError, OSError) as error:
        print(f"warning: unable to resolve the scoped diff ({error}); "
              "falling back to the complete pull request range", file=sys.stderr)
    try:
        diff_range = full_range_fn(root, event)
        return diff_range, False, paths_fn(root, diff_range)
    except (subprocess.CalledProcessError, ValueError, OSError) as error:
        print(f"warning: unable to resolve the complete pull request diff ({error}); "
              "forcing this gate to run", file=sys.stderr)
        return None, True, []


# --------------------------------------------------------------------------
# the tested surface
# --------------------------------------------------------------------------
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


def go_package_dirs(root: Path, sha: str, paths: Sequence[str]) -> list[str]:
    """The package directories the contribution touches, at the given commit."""
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


def frontend_dirs(paths: Sequence[str]) -> list[str]:
    """The frontend directories the contribution touches."""
    directories: set[str] = set()
    for path in paths:
        if not path.startswith(FRONTEND_ROOT):
            continue
        directory = str(Path(path).parent).replace("\\", "/")
        directories.add(directory)
    return sorted(directories)


def _is_governance(path: str) -> bool:
    """Whether a path belongs to the governance set of the manifest."""
    return path in GOVERNANCE_FILES or any(path.startswith(prefix)
                                           for prefix in GOVERNANCE_DIRS)


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
        governance_changed=any(_is_governance(path) for path in contribution),
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


# --------------------------------------------------------------------------
# the surfaces the manifest owns
# --------------------------------------------------------------------------
def classify(paths: Sequence[str]) -> dict[str, bool]:
    """Conservative shared-code coverage; unrelated changes stay cheap."""
    governance = any(_is_governance(path) for path in paths)
    contract_change = any(path.startswith(CONTRACT_DIR) for path in paths)
    backend = governance or contract_change or any(
        path in ("go.mod", "go.sum")
        or path.startswith(("internal/", "migrations/", "config/"))
        or (path.startswith("cmd/") and path.endswith(".go"))
        for path in paths
    )
    frontend = governance or contract_change or any(
        path.startswith(FRONTEND_ROOT) for path in paths)
    docs = any(
        path == "docs/TEAM_DEVELOPMENT_TASKS.md"
        or path.startswith(("docs/08-本轮实施方案/", "docs/06-灵档产品开发规划/"))
        for path in paths
    )
    return {"contracts": governance or backend or frontend or docs,
            "backend": backend, "frontend": frontend}


def existing_provider_roots(root: Path) -> list[str]:
    """The provider roots that exist in this checkout."""
    return [f"./{path}/..." for path in PROVIDER_ROOTS
            if any((root / path).rglob("*.go"))]


def _provider_packages(surface: Surface) -> list[str]:
    """The provider packages the contribution touches."""
    packages: list[str] = []
    for package in surface.go_packages:
        if any(package == root or package.startswith(root + "/")
               for root in PROVIDER_ROOTS):
            packages.append(f"./{package}/...")
    return packages


def _pattern_verdict(forced: bool, run_code: bool,
                      contribution: Sequence[str],
                      patterns: Sequence[str]) -> bool:
    """Whether a path table selects this event.

    This is the one formula behind every workflow-level gate: the gate runs
    when it is forced, or when the contribution changed and the contribution
    touches what the gate watches.
    """
    return bool(forced) or (bool(run_code) and matches(list(contribution),
                                                      list(patterns)))


def _touches(paths: Sequence[str], surface_paths: Sequence[str],
             path_kind: str) -> bool:
    """Whether the contribution touches a surface's paths."""
    if path_kind == manifest.PATH_KIND_GLOB:
        return matches(list(paths), list(surface_paths))
    if path_kind == manifest.PATH_KIND_PREFIX:
        return any(path.startswith(prefix) for path in paths
                   for prefix in surface_paths)
    return any(path in surface_paths for path in paths)


def _surface_verdict(surface, decision_fields: Mapping[str, Any]) -> bool:
    """Whether the checks of one manifest surface are selected.

    A workflow-level gate is selected by the contribution touching what it
    watches.  The pull request gate owns several surfaces, and each of them
    is the condition of the step that owns it.
    """
    if surface.path_kind == manifest.PATH_KIND_GLOB:
        return _pattern_verdict(decision_fields["forced"],
                                decision_fields["run_code"],
                                decision_fields["contribution"],
                                surface.paths)
    paths = tuple(surface.paths)
    if paths == tuple(PROVIDER_ROOTS):
        return bool(decision_fields["provider_tests"])
    if paths == (FRONTEND_ROOT,):
        return bool(decision_fields["frontend"])
    if paths == (CONTRACT_DIR,):
        return bool(decision_fields["contracts"])
    if paths in (tuple(GOVERNANCE_FILES), tuple(GOVERNANCE_DIRS)):
        return bool(decision_fields["governance_changed"])
    return (bool(decision_fields["forced"]) or bool(decision_fields["run_code"])
            or bool(decision_fields["base_moved"])) and _touches(
                decision_fields["contribution"], surface.paths, surface.path_kind)


def _jobs(fields: Mapping[str, Any]) -> tuple[JobDecision, ...]:
    """One entry per consuming job of every surface the manifest declares."""
    jobs: list[JobDecision] = []
    for surface in manifest.load_surfaces():
        verdict = _surface_verdict(surface, fields)
        for consumer in surface.consumers:
            jobs.append(JobDecision(
                workflow=consumer.workflow,
                job=consumer.job,
                job_name=consumer.job_name,
                surface=surface.name,
                run=verdict,
                gate_level=surface.gate_level,
                path_kind=surface.path_kind,
                paths=surface.paths,
                skipped_on_format_only=surface.skipped_on_format_only,
            ))
    return tuple(jobs)


# --------------------------------------------------------------------------
# the decision
# --------------------------------------------------------------------------
def _full_fields(root: Path, reason: str, base: str, forced: bool,
                 diff_range: str | None, changed: Sequence[str],
                 format_reasons: Sequence[str]) -> dict[str, Any]:
    """The fields of a decision that covers the whole scope."""
    provider_packages = tuple(existing_provider_roots(root))
    return {
        "run_code": True,
        "run_targeted": True,
        "provider_packages": provider_packages,
        "contracts": True,
        "backend": True,
        "frontend": True,
        "reason": reason,
        "surface": (),
        "base": base,
        "fingerprint": "",
        "base_moved": False,
        "surface_changed": True,
        "forced": forced,
        "governance_changed": False,
        "undecided": True,
        "contribution": (),
        "diff_range": diff_range,
        "changed_paths": tuple(changed),
        "format_only": False,
        "format_reasons": tuple(format_reasons),
    }


def _decide(event: Mapping[str, Any], root: Path) -> Decision:
    """The decision for one event, without the recovery path."""
    diff_range, forced, changed = select_paths(root, event)

    if not isinstance(event.get("pull_request"), Mapping):
        # Not a pull request: the contribution-surface rule does not apply,
        # so the whole scope is used and the reason says so.
        reason = str(event.get("reason") or "risk surface undetermined, "
                                         "fell back to the full scope")
        fields = _full_fields(root, reason, "", forced, diff_range, changed,
                              ["the gate is not judging a pull request"])
        fields["frontend"] = True
        fields["provider_tests"] = bool(fields["provider_packages"])
        return _build(fields)

    base, head = _event_shas(event)
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
    elif event.get("action") in ("opened", "reopened", "ready_for_review"):
        surface_changed = True

    provider_packages = tuple(_provider_packages(current))
    surface = list(current.go_packages) + list(current.frontend_dirs)
    governance = current.governance_changed

    if governance:
        # The gate judges itself: a change to the gate or to the scripts that
        # decide the scope must be judged by the whole gate, never by a
        # narrowed surface.
        surface_changed = True
        surface = list(existing_provider_roots(root)) + [FRONTEND_ROOT.rstrip("/")]
        provider_packages = tuple(existing_provider_roots(root))

    if not surface_changed and not base_moved:
        reason = "tested surface unchanged and base unchanged"
    elif surface_changed and base_moved:
        reason = "contribution surface changed and the base moved under the branch"
    elif surface_changed:
        reason = "contribution surface changed"
    else:
        reason = "the base moved under the branch; the contribution is re-tested against it"

    scope = classify(list(current.contribution))
    run_code = surface_changed
    run_targeted = surface_changed or base_moved

    format_only = False
    format_reasons = ["the formatting-only fast path does not apply to this event"]
    if lingdoc_format_scope.fast_path_applies(dict(event))[0]:
        format_only, format_reasons = lingdoc_format_scope.format_only_update(
            root, diff_range)

    fields: dict[str, Any] = {
        "run_code": run_code,
        "run_targeted": run_targeted,
        "provider_packages": provider_packages,
        "contracts": scope["contracts"],
        "backend": scope["backend"],
        "frontend": bool(scope["frontend"]) and run_targeted,
        "reason": reason,
        "surface": tuple(surface),
        "base": base,
        "fingerprint": content_fingerprint(root, after_sha, current),
        "base_moved": base_moved,
        "surface_changed": surface_changed,
        "forced": forced,
        "governance_changed": governance,
        "undecided": False,
        "contribution": current.contribution,
        "diff_range": diff_range,
        "changed_paths": tuple(changed),
        "format_only": format_only,
        "format_reasons": tuple(format_reasons),
    }
    return _build(fields)


def _build(fields: Mapping[str, Any]) -> Decision:
    """Turn the decision fields into a :class:`Decision`."""
    fields = dict(fields)
    fields.setdefault("provider_tests", bool(fields["provider_packages"])
                      and fields["run_targeted"])
    fields["provider_tests"] = bool(fields["provider_packages"]) and fields["run_targeted"]
    decision = Decision(
        jobs=_jobs(fields),
        run_code=fields["run_code"],
        run_targeted=fields["run_targeted"],
        provider_packages=tuple(fields["provider_packages"]),
        contracts=fields["contracts"],
        backend=fields["backend"],
        frontend=fields["frontend"],
        reason=fields["reason"],
        surface=tuple(fields["surface"]),
        base=fields["base"],
        fingerprint=fields["fingerprint"],
        base_moved=fields["base_moved"],
        surface_changed=fields["surface_changed"],
        forced=fields["forced"],
        governance_changed=fields["governance_changed"],
        undecided=fields["undecided"],
        contribution=tuple(fields["contribution"]),
        diff_range=fields["diff_range"],
        changed_paths=tuple(fields["changed_paths"]),
        format_only=fields["format_only"],
        format_reasons=tuple(fields["format_reasons"]),
    )
    return decision


def decide(event: Mapping[str, Any], root: Path) -> Decision:
    """Decide what the gates run for one event, and why.

    A scope-discovery failure must cost compute, never correctness: when the
    surface cannot be resolved every applicable check runs rather than
    silently skipping.  This is the one recovery path of the gate.
    """
    try:
        return _decide(event, root)
    except (subprocess.CalledProcessError, ValueError, OSError) as error:
        print(f"warning: unable to resolve the risk surface ({error}); "
              "falling back to the full scope", file=sys.stderr)
        base = ""
        try:
            base, _ = _event_shas(event)
        except ValueError:
            pass
        diff_range, forced, changed = None, True, []
        try:
            diff_range, forced, changed = select_paths(root, event)
        except (subprocess.CalledProcessError, ValueError, OSError):
            pass
        fields = _full_fields(
            root, "risk surface undetermined, fell back to the full scope",
            base, True, diff_range, changed,
            ["risk surface undetermined, fell back to the full scope"])
        return _build(fields)


# --------------------------------------------------------------------------
# the one string boundary
# --------------------------------------------------------------------------
def _text(value: bool) -> str:
    return "true" if value else "false"


def write_outputs(decision: Decision, *, patterns: Sequence[str] | None = None
                  ) -> dict[str, str]:
    """The one place where a decision becomes strings.

    ``patterns`` is the path table of the gate that asks for the decision; it
    selects the ``run`` verdict of that gate.  Without it the ``run`` verdict
    says whether any of the manifest's jobs is selected.
    """
    if patterns is None:
        run = any(job.run for job in decision.jobs)
    else:
        run = _pattern_verdict(decision.forced, decision.run_code,
                                decision.contribution, patterns)
    outputs = {
        "run": _text(run),
        "run_code": _text(decision.run_code),
        "run_targeted": _text(decision.run_targeted),
        "reason": decision.reason,
        "surface": " ".join(decision.surface),
        "base": decision.base,
        "contracts": _text(decision.contracts),
        "backend": _text(decision.backend),
        "frontend": _text(decision.frontend),
        "provider_tests": _text(decision.provider_tests),
        "provider_packages": " ".join(decision.provider_packages),
        "base_moved": _text(decision.base_moved),
        "surface_changed": _text(decision.surface_changed),
        "forced": _text(decision.forced),
        "format_only": _text(decision.format_only),
        "format_reasons": " | ".join(decision.format_reasons),
        "changed_paths": str(len(decision.changed_paths)),
        "diff_range": decision.diff_range or "",
    }
    for job in decision.jobs:
        outputs[job.key] = _text(job.run)
    return outputs


# --------------------------------------------------------------------------
# the offline entry point
# --------------------------------------------------------------------------
def report(decision: Decision, whitespace_range: str | None = None) -> dict[str, Any]:
    """The report the gate publishes: the decision and what it selected."""
    outputs = write_outputs(decision)
    packages = list(decision.provider_packages) if decision.run_targeted else []
    return {
        "scope": {"contracts": decision.contracts, "backend": decision.backend,
                  "frontend": decision.frontend},
        "diff_range": decision.diff_range,
        "whitespace_diff_range": whitespace_range,
        "changed_paths": list(decision.changed_paths),
        "provider_packages": packages,
        "format_only": decision.format_only,
        "format_reasons": list(decision.format_reasons),
        "decision": outputs,
        "jobs": [dataclasses.asdict(job) for job in decision.jobs],
        "note": "Selection only; not a test result or an approval.",
    }


def check_whitespace(root: Path, diff_range: str | None) -> None:
    """The pull request gate's boundary check: the diff has no whitespace damage.

    The check covers the **complete** pull request range, not the last push:
    a whitespace defect introduced by an earlier commit is still a defect.
    """
    if not diff_range:
        return
    subprocess.run(["git", "diff", "--check", diff_range, "--"],
                   cwd=root, check=True, stdout=sys.stderr)


def publish(event: Mapping[str, Any], root: Path, *,
            all_checks: bool = False) -> dict[str, Any]:
    """Decide, publish the decision, and check the pull request's boundary.

    This is what the gate's scope step runs: it prints the report, writes the
    outputs the workflow's conditions read, and fails the step when the diff
    carries whitespace damage.
    """
    if all_checks:
        event = {"action": "manual", "reason": "full scope requested"}
    decision = decide(event, root)
    whitespace_range = None
    if isinstance(event.get("pull_request"), Mapping):
        whitespace_range = pr_range(event)
    published = report(decision, whitespace_range)
    print(json.dumps(published, ensure_ascii=True, indent=2))
    if output_path := os.environ.get("GITHUB_OUTPUT"):
        with open(output_path, "a", encoding="utf-8") as handle:
            for key, value in write_outputs(decision).items():
                handle.write(f"{key}={value}\n")
    check_whitespace(root, whitespace_range or decision.diff_range)
    return published


def main() -> None:
    """Print the decision for one event; the gate's offline entry point."""
    parser = argparse.ArgumentParser(description="Decide what the gates run for one event.")
    parser.add_argument("--all", action="store_true", help="Run all applicable checks")
    parser.add_argument("--event", type=Path, default=os.environ.get("GITHUB_EVENT_PATH"))
    parser.add_argument("--root", type=Path, default=Path.cwd())
    args = parser.parse_args()
    root = args.root.resolve()

    if args.all:
        publish({}, root, all_checks=True)
        return
    if args.event is None:
        parser.error("--event or GITHUB_EVENT_PATH is required")
    event = json.loads(args.event.read_text(encoding="utf-8"))
    publish(event, root)


if __name__ == "__main__":
    main()
