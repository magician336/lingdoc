"""The gate decision: one deep module, one decision.

What this module owns
----------------------
Every fact the gate needs in order to decide *what runs*:

* the diff ranges -- the pull request's own range and the range a push
  reports;
* the changed files of a range;
* the tree hashes behind the tested surface;
* the tested surface itself (Go packages, frontend directories);
* the gate-surface classification: which surface a change belongs to;
* the formatting fast path (ADR 0005);
* the ownership knowledge -- provider roots, contracts directory, governance
  file set, frontend root -- which is **read from the gate surface manifest**
  instead of being written down a second time.

The public interface
--------------------

The interface is registered **once**, in ADR 0007, and the guard test
``scripts/ci/test_lingdoc_gate_scripts.py`` asserts that the module exposes
exactly the names that are registered there: a new public name that nobody
registered makes the guard test fail.

``decide(event, root)``
    Answers one event with a typed :class:`Decision`.  It never lets an
    undecidable surface escape: it falls back to the full scope and says so
    in the reason.  There is exactly one recovery path, and it lives here.

``write_outputs(decision, *, patterns=)``
    The one place where a decision becomes strings, for the GitHub Actions
    output boundary and for the gate artifacts.  The per-job verdicts are
    published under flat keys (``job_frontend_verify``): a job output id
    cannot carry a dot, so a job level reference
    (``needs.scope.outputs.<name>``) can only name such a key.  ``patterns``
    is the path table of the gate that asks for the decision: the same
    ``Decision`` answers differently for different workflows, because
    different workflows watch different paths -- so the path table has to
    come from the caller.

``report(decision)``
    The report the gate publishes: the decision and what it selected.

``publish(event, root)``
    What the gate's scope step runs: decide, publish the decision, publish
    the conclusion, and check the pull request's whitespace boundary.  When
    an artifact directory is given, it also records the **run anchor**
    (``gate-anchor.json``): the facts a later run needs in order to verify
    that this run's evidence still stands -- the pull request's own head
    SHA (never ``GITHUB_SHA``, which is the merge commit), the main tip the
    run's merge ref integrated, this run's id and pull request number, the
    workflow that produced the run, and the scope step's own verdict.  The
    anchor is an ancillary product: its write never changes the gate's
    verdict, and an incomplete record makes a later reuse fallback, never
    a false pass.

``gate_conclusion(decision, workflow)`` and ``conclusion_markdown(...)``
    Where a decision becomes something a **reader** sees: the job summary on
    the pull request page and the gate artifact.  Every gate publishes the
    same conclusion -- ``surface``, ``reason`` and ``base`` -- and every
    check that did not run says why.  A skipped check without a reason
    would break "Skipped means NOT RUN, not passed".

``publish_conclusion(decision, workflow)``
    Writes that conclusion into the job summary and the gate artifact.

``check_whitespace(root, diff_range)``
    The pull request gate's boundary check: the diff carries no whitespace
    damage.

``output_key(workflow, job)``
    The output key of one consuming job: ``frontend.yml``'s ``verify`` job is
    published as ``job_frontend_verify``.

``Decision`` and ``JobDecision``
    The decision is a value: it crosses no process boundary as a string.
    ``Decision.as_outputs`` is the string form of a decision.

Everything else in this module is a helper.  None of them decides anything
on its own; the decision is made once, in :func:`decide`.

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
import shutil
import subprocess
import sys
from datetime import datetime, timezone
from typing import Any, Callable, Mapping, Sequence

import lingdoc_gate_surfaces as manifest

SHA = re.compile(r"[0-9a-fA-F]{40}")
GO_SUFFIX = ".go"

# A job output id may only carry letters, digits, "-" and "_": GitHub's
# workflow schema spells it ``^[_a-zA-Z][a-zA-Z0-9_-]*$``.  A dotted key can
# therefore never be declared as a job output, and a job level reference
# (``needs.scope.outputs.<name>``) can never name one.
OUTPUT_ID = re.compile(r"^[_a-zA-Z][a-zA-Z0-9_-]*$")
OUTPUT_KEY_SEPARATORS = re.compile(r"[^0-9A-Za-z]+")


def output_key(workflow: str, job: str) -> str:
    """The output key of the job that consumes a surface.

    ``frontend.yml``'s ``verify`` job is published as ``job_frontend_verify``:
    one flat namespace, spelled with the characters a job output id accepts.
    """
    return "job_" + OUTPUT_KEY_SEPARATORS.sub("_", f"{Path(workflow).stem}_{job}")

# The ownership knowledge lives in the gate surface manifest.  It is read from
# there, never written down here.
PROVIDER_ROOTS = tuple(manifest.PROVIDER_ROOTS)
CONTRACT_DIR = manifest.CONTRACT_DIR
GOVERNANCE_FILES = tuple(manifest.GOVERNANCE_FILES)
GOVERNANCE_DIRS = tuple(manifest.GOVERNANCE_DIRS)
FRONTEND_ROOT = manifest.FRONTEND_ROOT

# --------------------------------------------------------------------------
# the published conclusion
# --------------------------------------------------------------------------
# The boundary statement every gate publishes.  A green gate is a statement
# about automated checks; it is never an approval and never a product
# acceptance.  All eight gates say the same sentence, so that no reader has
# to guess what one gate's silence meant.
BOUNDARY = ("Automated checks only; not consumer approval, a live-model run, "
            "or product acceptance.")

# What a gate says when it could not narrow the scope.  ADR 0006's own
# wording stays untouched in ``reason``; this note is the readable Chinese
# counterpart of the ``undecided`` flag.
UNDECIDED_NOTE = ("未判定，已回退全量（not decided: the gate did not narrow "
                  "the scope, so the full scope was used）")


# --------------------------------------------------------------------------
# the manifest seam
# --------------------------------------------------------------------------
def _manifest_surfaces() -> tuple[manifest.Surface, ...]:
    """The manifest's surfaces, read strictly.

    The shape of the records is checked first -- that needs no dependency.
    The consuming jobs are then checked against the workflow files they name;
    that check needs PyYAML, and a job that installs nothing skips it rather
    than failing: the gate never depends on the check, only on the data.

    Every failure raises :class:`lingdoc_gate_surfaces.SurfaceManifestError`:
    the decision has to know that the knowledge it judges by is broken.
    """
    surfaces = manifest.load_surfaces()
    try:
        manifest.check_consumers(surfaces, manifest.REPO_ROOT)
    except ImportError:
        pass
    return surfaces


def _readable_surfaces() -> tuple[manifest.Surface, ...]:
    """The manifest's surfaces, as far as they can be read.

    This is what the recovery path and the publication use: an invalid
    manifest must not turn into a traceback, it must turn into the full
    scope.
    """
    try:
        return _manifest_surfaces()
    except manifest.SurfaceManifestError:
        return manifest.load_surfaces_lenient()


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
    def readable_key(self) -> str:
        """The human-readable key of this job, e.g. ``job.frontend.verify``."""
        return f"job.{Path(self.workflow).stem}.{self.job}"

    @property
    def key(self) -> str:
        """The output key of this job, e.g. ``job_frontend_verify``.

        A job level reference has to name a declared output, and a declared
        output id cannot carry a dot.  The readable form is kept beside it
        for humans and for the artifacts.
        """
        return output_key(self.workflow, self.job)


@dataclasses.dataclass(frozen=True)
class _Coverage:
    """One touched surface and the job that verifies it on this head.

    ``surface`` names the part of the contribution the row is about.
    ``paths`` are the contribution's paths it consists of.  ``verified_by``
    names the jobs that compile, test or build those paths **and** run on this
    head; ``watched_by`` names the jobs that watch them at all, including the
    ones that only inspect them.

    ``status`` is the verdict a reader needs:

    ``verified``
        a running job compiles, tests or builds this surface.
    ``not verified``
        a verifying job watches this surface but none of them runs on this
        head -- the surface has no compile or test evidence here.
    ``not verified (no gate job covers this surface)``
        no verifying job watches this surface at all; the gate makes no
        promise about it.
    """

    surface: str
    paths: tuple[str, ...]
    verified_by: tuple[str, ...]
    watched_by: tuple[str, ...]
    status: str

    @property
    def covered(self) -> bool:
        """Whether this surface has compile or test evidence on this head."""
        return self.status == "verified"


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

    # -- who verifies the touched surface ---------------------------------
    coverage: tuple[_Coverage, ...] = ()
    uncovered: bool = False

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
def _run_git(root: Path, *args: str) -> str:
    """Run a git command in ``root`` and return its trimmed output."""
    return subprocess.check_output(
        ["git", *args], cwd=root, text=True, stderr=subprocess.PIPE
    ).strip()


def _changed_paths(root: Path, diff_range: str) -> list[str]:
    """The files a diff range touches.

    ``--no-renames`` includes BOTH sides of a move across a gate boundary.
    """
    raw = subprocess.check_output(
        ["git", "diff", "--name-only", "--no-renames", "-z", diff_range, "--"], cwd=root)
    return [os.fsdecode(item) for item in raw.split(b"\0") if item]


def _matches(paths: Sequence[str], patterns: Sequence[str]) -> bool:
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


def _pr_range(event: Mapping[str, Any], *, require_sha: bool = True) -> str:
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


def _push_range(root: Path, event: Mapping[str, Any],
                git: Callable[..., str] = _run_git) -> str:
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


def _event_range(root: Path, event: Mapping[str, Any], *,
                 event_name: str | None = None,
                 git: Callable[..., str] = _run_git,
                 require_sha: bool = False) -> tuple[str | None, bool]:
    """Return ``(diff_range, forced)`` for the current event.

    A manual run has no range and is forced.  A push reports its own range.
    A pull request is scoped to the complete pull request range ``base...head``
    -- the contribution surface, never the last push.
    """
    kind = _event_kind(event, event_name)
    if kind == "workflow_dispatch":
        return None, True
    if kind == "push":
        return _push_range(root, event, git), False
    return _pr_range(event, require_sha=require_sha), False


def _select_paths(root: Path, event: Mapping[str, Any], *,
                  event_name: str | None = None,
                  git: Callable[..., str] = _run_git,
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
            return _event_range(root, event, event_name=event_name, git=git)
    if paths_fn is None:
        paths_fn = _changed_paths
    if full_range_fn is None:
        def full_range_fn(root: Path, event: Mapping[str, Any]) -> str:
            return _pr_range(event)

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
class _Surface:
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
        _run_git(root, "rev-parse", "--verify", _tree_spec(sha, path))
    except subprocess.CalledProcessError:
        return False
    return True


def _go_package_dirs(root: Path, sha: str, paths: Sequence[str]) -> list[str]:
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


def _frontend_dirs(paths: Sequence[str]) -> list[str]:
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


def _surface_at(root: Path, base: str, sha: str) -> _Surface:
    """The tested surface of the pull request as of one commit."""
    contribution = tuple(_changed_paths(root, f"{base}...{sha}"))
    return _Surface(
        base=base,
        head=sha,
        contribution=contribution,
        go_packages=tuple(_go_package_dirs(root, sha, list(contribution))),
        frontend_dirs=tuple(_frontend_dirs(list(contribution))),
        contract_changed=any(path.startswith(CONTRACT_DIR) for path in contribution),
        governance_changed=any(_is_governance(path) for path in contribution),
    )


def _content_fingerprint(root: Path, sha: str, surface: _Surface) -> str:
    """Hash the content of the surface at one commit."""
    digest = hashlib.sha256()
    for package in surface.go_packages:
        try:
            tree = _run_git(root, "rev-parse", _tree_spec(sha, package))
        except subprocess.CalledProcessError:
            tree = "absent"
        digest.update(f"go:{package}:{tree}\n".encode("utf-8"))
    for directory in surface.frontend_dirs:
        try:
            tree = _run_git(root, "rev-parse", _tree_spec(sha, directory))
        except subprocess.CalledProcessError:
            tree = "absent"
        digest.update(f"fe:{directory}:{tree}\n".encode("utf-8"))
    return digest.hexdigest()


def _base_moved_under(root: Path, base: str, before: str, after: str) -> bool:
    """True when the base changed underneath the branch between two heads."""
    try:
        old_base = _run_git(root, "merge-base", base, before)
        new_base = _run_git(root, "merge-base", base, after)
    except subprocess.CalledProcessError:
        return False
    return old_base != new_base


# --------------------------------------------------------------------------
# the surfaces the manifest owns
# --------------------------------------------------------------------------
def _classify(paths: Sequence[str]) -> dict[str, bool]:
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


def _existing_provider_roots(root: Path) -> list[str]:
    """The provider roots that exist in this checkout."""
    return [f"./{path}/..." for path in PROVIDER_ROOTS
            if any((root / path).rglob("*.go"))]


def _provider_packages_of(go_packages: Sequence[str]) -> list[str]:
    """The provider packages among the given Go package directories."""
    packages: list[str] = []
    for package in go_packages:
        if any(package == root or package.startswith(root + "/")
               for root in PROVIDER_ROOTS):
            packages.append(f"./{package}/...")
    return packages


def _provider_packages(surface: Surface) -> list[str]:
    """The provider packages the contribution touches."""
    return _provider_packages_of(surface.go_packages)


def _pattern_verdict(forced: bool, run_code: bool,
                      contribution: Sequence[str],
                      patterns: Sequence[str], *,
                      base_moved: bool = False) -> bool:
    """Whether a path table selects this event.

    This is the one formula behind every workflow-level gate: the gate runs
    when it is forced, or when the contribution changed and the contribution
    touches what the gate watches.

    The base moving under the branch is a second reason to run.  It makes the
    **merge result** the gate tests new, even when the contribution's own
    content did not change: the base's new commits are part of what the gate
    compiles and tests.  A gate that watches the contribution therefore has to
    run again after a base move -- otherwise the touched surface would have no
    compile or test evidence on this head at all, and the gate would report a
    green check for code nobody ran.
    """
    return bool(forced) or ((bool(run_code) or bool(base_moved))
                            and _matches(list(contribution), list(patterns)))


def _touches(paths: Sequence[str], surface_paths: Sequence[str],
             path_kind: str) -> bool:
    """Whether the contribution touches a surface's paths."""
    if path_kind == manifest.PATH_KIND_GLOB:
        return _matches(list(paths), list(surface_paths))
    if path_kind == manifest.PATH_KIND_PREFIX:
        return any(path.startswith(prefix) for path in paths
                   for prefix in surface_paths)
    return any(path in surface_paths for path in paths)


def _surface_verdict(surface, decision_fields: Mapping[str, Any]) -> bool:
    """Whether the checks of one manifest surface are selected.

    A workflow-level gate is selected by the contribution touching what it
    watches.  The pull request gate owns several surfaces, and each of them
    is the condition of the step that owns it.

    Which fact selects a surface is the surface's **role**, read from the
    manifest.  It is never guessed from the path set: a path that happens to
    look like the provider roots is not the provider surface.
    """
    if surface.path_kind == manifest.PATH_KIND_GLOB:
        return _pattern_verdict(decision_fields["forced"],
                                decision_fields["run_code"],
                                decision_fields["contribution"],
                                surface.paths,
                                base_moved=decision_fields["base_moved"])
    if surface.role == manifest.ROLE_PROVIDER:
        return bool(decision_fields["provider_tests"])
    if surface.role == manifest.ROLE_FRONTEND:
        return bool(decision_fields["frontend"])
    if surface.role == manifest.ROLE_CONTRACTS:
        return bool(decision_fields["contracts"])
    if surface.role == manifest.ROLE_GOVERNANCE:
        return bool(decision_fields["governance_changed"])
    return (bool(decision_fields["forced"]) or bool(decision_fields["run_code"])
            or bool(decision_fields["base_moved"])) and _touches(
                decision_fields["contribution"], surface.paths, surface.path_kind)


def _jobs(fields: Mapping[str, Any],
          surfaces: Sequence[manifest.Surface]) -> tuple[JobDecision, ...]:
    """One entry per consuming job of every surface the manifest declares.

    The verdict of a job already accounts for the formatting-only shortcut:
    a test job stays away when the update only reformats the code, and a lint
    job never does -- lint is the judge of formatting.
    """
    jobs: list[JobDecision] = []
    format_only = bool(fields.get("format_only"))
    for surface in surfaces:
        verdict = _surface_verdict(surface, fields)
        if format_only and surface.skipped_on_format_only:
            verdict = False
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
# the coverage: who verifies the touched surface
# --------------------------------------------------------------------------
def _surface_runs(decision: Decision, surface: manifest.Surface,
                  outputs: Mapping[str, str]) -> bool:
    """Whether the job that owns this surface runs on this head.

    A surface with a step is selected by that step's output -- that is what
    the workflow reads -- and a surface without one by the job's own verdict.
    """
    if surface.step is not None:
        return outputs.get(surface.step.output, "false") == "true"
    return any(job.run for job in decision.jobs if job.surface == surface.name)


def _owner_names(surface: manifest.Surface) -> str:
    """The name of the job (or step) that owns a surface, as a reader sees it."""
    if surface.step is not None:
        return surface.step.name
    return "; ".join(consumer.job_name for consumer in surface.consumers)


def _belongs_to(path: str, surface: str) -> bool:
    """Whether a changed path belongs to a tested surface."""
    if surface == ".":
        return "/" not in path
    return path.startswith(surface + "/")


def _coverage_rows(decision: Decision,
                   surfaces: Sequence[manifest.Surface]) -> tuple[_Coverage, ...]:
    """The coverage of every surface the contribution touches.

    The unit is the **tested surface** -- the Go packages and frontend
    directories the contribution touches.  That is the unit the artifact
    calls "Surface", and it is the unit that has to carry compile or test
    evidence.

    A surface is **verified** when a job that compiles, tests or builds it
    runs on this head.  It is **not verified** when a verifying job watches it
    but none of them runs -- the surface then has no compile or test evidence
    here, and the decision has to say so instead of skipping it in silence.
    It is **not verified (no gate job covers this surface)** when no
    verifying job watches it at all: the gate makes no promise about it (the
    cli module has its own workflow, which is not a gate surface).
    """
    outputs = write_outputs(decision)
    rows: list[_Coverage] = []
    for tested in decision.surface:
        paths = tuple(sorted(path for path in decision.contribution
                             if _belongs_to(path, tested)))
        if not paths:
            continue
        watching = [surface for surface in surfaces
                    if _touches(paths, surface.paths, surface.path_kind)]
        verifying = [surface for surface in watching if surface.verifies]
        running = [surface for surface in verifying
                   if _surface_runs(decision, surface, outputs)]
        if running:
            status = "verified"
        elif verifying:
            status = "not verified"
        else:
            status = "not verified (no gate job covers this surface)"
        rows.append(_Coverage(
            surface=tested,
            paths=paths,
            verified_by=tuple(_owner_names(surface) for surface in running),
            watched_by=tuple(_owner_names(surface) for surface in watching),
            status=status,
        ))
    return tuple(rows)


def _coverage_required(decision: Decision) -> bool:
    """Whether this head needs fresh compile or test evidence.

    Two cases need none, and both are deliberate:

    * a formatting-only change -- ADR 0005's shortcut;
    * a head whose code under test did not change -- neither the tested
      surface nor the base moved, so the verification of that same content
      still holds.
    """
    return not decision.format_only and decision.run_targeted


def _uncovered_reason(coverage: Sequence[_Coverage]) -> str:
    """The sentence that says a surface has no evidence on this head."""
    names = ", ".join(f"`{row.surface}`" for row in coverage
                      if row.status == "not verified")
    return (f"no job that compiles or tests {names} runs on this head, so "
            "that surface has no test evidence here")


# --------------------------------------------------------------------------
# the formatting fast path (ADR 0005)
# --------------------------------------------------------------------------
# A formatting-only change keeps the meaning of the code, so the expensive
# test matrix does not have to run again for that update.  Two judgements are
# accepted, and both are conservative:
#
# * **gofmt equivalence** -- the file after the change is exactly what
#   ``gofmt`` produces for the file before the change.
# * **comments and whitespace only** -- after removing comments and the
#   whitespace that is not inside a string literal, the two versions have
#   the same token sequence.
#
# Anything the classifier cannot decide is **not** formatting-only, so the
# full matrix runs.  A file is accepted only when *every* changed file is a
# Go file accepted by one of the two judgements.
#
# Only Go files are judged.  The repository has no formatter for the
# frontend, and a whitespace-insensitive comparison would treat a change
# inside a string literal as formatting -- which changes behaviour.

# A comment that the Go toolchain reads.  Changing one changes the build or
# the reported source position, so such a file is never formatting-only.
_DIRECTIVE_MARKERS = ("//go:", "//line", "/*go:")


@dataclasses.dataclass(frozen=True)
class _FormatVerdict:
    """The classifier's answer plus the reasons a reviewer can check."""

    format_only: bool
    reasons: tuple[str, ...] = ()


def _semantic_projection(source: str) -> list[str]:
    """Return the token sequence of a Go file, ignoring comments and layout.

    Comments are dropped.  Whitespace outside string literals is dropped.
    String and raw string literals are kept **verbatim**, including the
    whitespace inside them, because that whitespace is part of the value.
    """
    lines: list[str] = []
    current: list[str] = []
    state = "code"
    index = 0
    length = len(source)
    while index < length:
        char = source[index]
        if char == "\n":
            if state in ("string", "raw", "rune"):
                # An unterminated literal: stop treating the rest as a
                # literal so the comparison stays conservative.
                state = "code"
            elif state == "line_comment":
                # A line comment ends with its line.
                state = "code"
            lines.append("".join(current))
            current = []
            index += 1
            continue
        if state == "code":
            if char == "/" and index + 1 < length and source[index + 1] == "/":
                state = "line_comment"
                index += 2
                continue
            if char == "/" and index + 1 < length and source[index + 1] == "*":
                state = "block_comment"
                index += 2
                continue
            if char == '"':
                state = "string"
                current.append(char)
                index += 1
                continue
            if char == "`":
                state = "raw"
                current.append(char)
                index += 1
                continue
            if char == "'":
                state = "rune"
                current.append(char)
                index += 1
                continue
            if char.isspace():
                index += 1
                continue
            current.append(char)
            index += 1
            continue
        if state == "line_comment":
            index += 1
            continue
        if state == "block_comment":
            if char == "*" and index + 1 < length and source[index + 1] == "/":
                state = "code"
                index += 2
                continue
            index += 1
            continue
        if state == "string":
            current.append(char)
            if char == "\\" and index + 1 < length:
                current.append(source[index + 1])
                index += 2
                continue
            if char == '"':
                state = "code"
            index += 1
            continue
        if state == "raw":
            current.append(char)
            if char == "`":
                state = "code"
            index += 1
            continue
        if state == "rune":
            current.append(char)
            if char == "\\" and index + 1 < length:
                current.append(source[index + 1])
                index += 2
                continue
            if char == "'":
                state = "code"
            index += 1
            continue
    lines.append("".join(current))
    return [line for line in lines if line]


def _git_bytes(root: Path, *args: str) -> bytes:
    """Run a git command and return its raw output."""
    return subprocess.check_output(["git", *args], cwd=root, stderr=subprocess.PIPE)


def _split_range(diff_range: str) -> tuple[str, str, bool]:
    """Return (left, right, three_dot) for a diff range expression."""
    if "..." in diff_range:
        left, right = diff_range.split("...", 1)
        return left.strip(), right.strip(), True
    if ".." in diff_range:
        left, right = diff_range.split("..", 1)
        return left.strip(), right.strip(), False
    return "", "", False


def _resolve_revisions(root: Path, diff_range: str) -> tuple[str, str]:
    """Return the two revisions whose contents are compared."""
    left, right, three_dot = _split_range(diff_range)
    if not left or not right:
        raise ValueError(f"unusable diff range: {diff_range!r}")
    if three_dot:
        base = _git_bytes(root, "merge-base", left, right).decode().strip()
    else:
        base = left
    return base, right


def _blob(root: Path, revision: str, path: str) -> bytes | None:
    try:
        return _git_bytes(root, "show", f"{revision}:{path}")
    except subprocess.CalledProcessError:
        return None


def _gofmt_equivalent(before: bytes, after: bytes) -> bool | None:
    """True when gofmt(before) == after; None when gofmt cannot judge."""
    program = shutil.which("gofmt")
    if program is None:
        return None
    try:
        result = subprocess.run([program], input=before, capture_output=True,
                                timeout=30)
    except (OSError, subprocess.SubprocessError):
        return None
    if result.returncode != 0:
        return None
    return result.stdout == after


def _has_directive(source: bytes) -> bool:
    text = source.decode("utf-8", "replace")
    return any(marker in text for marker in _DIRECTIVE_MARKERS)


def _file_is_formatting_only(root: Path, base: str, right: str,
                             path: str) -> tuple[bool, str]:
    """Whether one file of the range only reformats."""
    before = _blob(root, base, path)
    after = _blob(root, right, path)
    if before is None or after is None:
        return False, f"{path}: added, deleted or unreadable in this range"
    if _has_directive(before) or _has_directive(after):
        return False, f"{path}: contains a compiler directive; never treated as formatting"
    if _gofmt_equivalent(before, after) is True:
        return True, f"{path}: gofmt-equivalent"
    try:
        before_text = before.decode("utf-8")
        after_text = after.decode("utf-8")
    except UnicodeDecodeError:
        return False, f"{path}: not valid UTF-8"
    if _semantic_projection(before_text) == _semantic_projection(after_text):
        return True, f"{path}: comments and whitespace only"
    return False, f"{path}: code changed"


def _format_fast_path_applies(event: Mapping[str, Any]) -> tuple[bool, str]:
    """Whether the formatting-only fast path may be used for an event.

    Only a pull request **update** qualifies.  A pull request that is opened
    or reopened is always checked in full: the first time a reviewer sees
    it, the whole matrix has to run.  A push to main is always checked in
    full as well.
    """
    pull_request = event.get("pull_request")
    if not isinstance(pull_request, Mapping):
        return False, "not a pull request event; the full matrix runs"
    action = event.get("action")
    if action != "synchronize":
        return False, (f"pull request action {action!r} is not an update; "
                       "the full matrix runs")
    return True, "pull request update"


def _format_only_update(root: Path, diff_range: str | None) -> tuple[bool, list[str]]:
    """Answer whether an update only reformats, and say why.

    A formatting-only update keeps the meaning of the code, so the test
    matrix does not have to run again for it.  Anything the classifier
    cannot decide is **not** formatting-only: the expensive jobs then run
    as usual.
    """
    if not diff_range:
        return False, ["no diff range resolved; the full matrix runs"]
    try:
        verdict = _classify_formatting(root, diff_range)
    except Exception as error:  # noqa: BLE001 - a failed judgement must never skip a gate
        return False, [f"format classification failed: {error}"]
    return verdict.format_only, list(verdict.reasons)


def _classify_formatting(root: Path, diff_range: str) -> _FormatVerdict:
    """Decide whether a diff range only reformats the code it touches."""
    try:
        base, right = _resolve_revisions(root, diff_range)
        # The one git primitive for "the files a range touches"; the format
        # classifier reads the same list as everything else.
        paths = _changed_paths(root, diff_range)
    except (subprocess.CalledProcessError, ValueError, OSError) as error:
        return _FormatVerdict(False, (
            f"cannot resolve the diff range {diff_range!r}: {error}",))
    if not paths:
        return _FormatVerdict(False, ("the range touches no file",))
    reasons: list[str] = []
    for path in paths:
        if not path.endswith(GO_SUFFIX):
            return _FormatVerdict(False, tuple(reasons + [
                f"{path}: not a Go file; the path-based scope decides"]))
        accepted, reason = _file_is_formatting_only(root, base, right, path)
        reasons.append(reason)
        if not accepted:
            return _FormatVerdict(False, tuple(reasons))
    return _FormatVerdict(True, tuple(reasons))


# --------------------------------------------------------------------------
# the decision
# --------------------------------------------------------------------------
def _full_fields(root: Path, reason: str, base: str, forced: bool,
                 diff_range: str | None, changed: Sequence[str],
                 format_reasons: Sequence[str],
                 *, undecided: bool = True) -> dict[str, Any]:
    """The fields of a decision that covers the whole scope.

    ``undecided`` says whether the full scope is the result of a **failed**
    judgement.  A full scope that was asked for on purpose is decided, not
    undecided: it must not be published as "未判定，已回退全量".
    """
    provider_packages = tuple(_existing_provider_roots(root))
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
        "undecided": undecided,
        "contribution": (),
        "diff_range": diff_range,
        "changed_paths": tuple(changed),
        "format_only": False,
        "format_reasons": tuple(format_reasons),
    }


def _push_baseline(diff_range: str | None) -> str:
    """The commit a push starts from: the left side of the push's range.

    A push that creates a branch reports an all-zero ``before``; the range
    then names the new head's parent, which is the boundary
    :func:`push_range` already picked.
    """
    if not diff_range:
        return ""
    return diff_range.split("...", 1)[0].strip()


def _decide_push(event: Mapping[str, Any], root: Path,
                 diff_range: str | None, forced: bool,
                 changed: Sequence[str],
                 surfaces: Sequence[manifest.Surface]) -> Decision:
    """The decision for a branch push.

    A push carries no pull request contribution, but it carries a range of
    its own: GitHub reports the ``before`` and ``after`` commits of the
    push.  The tested surface of a push is therefore the diff the push
    delivers, and its baseline is the commit the push started from.  That is
    the same validation unit ADR 0006 describes -- (tested surface content,
    baseline content) -- with the push's own range in place of the pull
    request's contribution.

    A push never takes the formatting-only fast path (ADR 0005), and a push
    that changes the gate itself is judged by the whole gate, exactly as a
    pull request that changes it is.
    """
    after = event.get("after")
    after_sha = after if isinstance(after, str) and SHA.fullmatch(after) else ""
    base = _push_baseline(diff_range)
    paths = list(changed)

    go_packages = _go_package_dirs(root, after_sha, paths) if after_sha else []
    front = _frontend_dirs(paths)
    governance = any(_is_governance(path) for path in paths)
    scope = _classify(paths)
    provider_packages = tuple(_provider_packages_of(go_packages))
    surface = list(go_packages) + list(front)
    run_code = bool(paths)
    run_targeted = bool(paths)

    if governance:
        # The gate judges itself, exactly as it does for a pull request.
        surface = list(_existing_provider_roots(root)) + [FRONTEND_ROOT.rstrip("/")]
        provider_packages = tuple(_existing_provider_roots(root))
        reason = "governance changed; the whole scope is tested"
    elif forced and not paths:
        # The range could not be resolved: the whole scope is tested.
        surface = list(_existing_provider_roots(root)) + [FRONTEND_ROOT.rstrip("/")]
        provider_packages = tuple(_existing_provider_roots(root))
        run_code = run_targeted = True
        reason = "the push range is undetermined; the whole scope is tested"
    elif paths:
        reason = "the push diff is the tested surface"
    else:
        reason = "the push carries no file"

    fields: dict[str, Any] = {
        "run_code": run_code,
        "run_targeted": run_targeted,
        "provider_packages": provider_packages,
        "contracts": scope["contracts"],
        "backend": scope["backend"],
        "frontend": bool(scope["frontend"]),
        "reason": reason,
        "surface": tuple(surface),
        "base": base,
        "fingerprint": _content_fingerprint(
            root, after_sha,
            _Surface(base=base, head=after_sha, contribution=tuple(paths),
                     go_packages=tuple(go_packages), frontend_dirs=tuple(front))),
        "base_moved": False,
        "surface_changed": bool(paths),
        "forced": forced,
        "governance_changed": governance,
        "undecided": False,
        "contribution": tuple(paths),
        "diff_range": diff_range,
        "changed_paths": tuple(paths),
        "format_only": False,
        "format_reasons": ("a push never takes the formatting-only fast "
                           "path; the full matrix runs",),
    }
    return _build(fields, surfaces)


def _is_full_scope_request(event: Mapping[str, Any]) -> bool:
    """Whether the event asks for the full scope on purpose.

    A manual run -- ``--all``, a workflow dispatch -- asks for the whole
    scope.  That is **not** an undetermined surface: it was decided, on
    purpose, and the conclusion says so.
    """
    if event.get("action") in ("manual", "workflow_dispatch"):
        return True
    return str(event.get("reason") or "") == "full scope requested"


def _decide(event: Mapping[str, Any], root: Path,
            surfaces: Sequence[manifest.Surface]) -> Decision:
    """The decision for one event, without the recovery path."""
    # The decision is scoped to the pull request's own contribution, never to
    # the last push: a baseline merge changes no contribution, so the
    # repository-wide suites stay away while the contribution is re-tested
    # against the new baseline.
    diff_range, forced, changed = _select_paths(root, event)

    if not isinstance(event.get("pull_request"), Mapping):
        if _event_kind(event, None) == "push":
            # A push has no contribution, but it has a range of its own: the
            # diff it delivers is the tested surface.
            return _decide_push(event, root, diff_range, forced, changed,
                                surfaces)
        # Not a pull request: the contribution-surface rule does not apply,
        # so the whole scope is used and the reason says so.  A manual run
        # asked for it on purpose; anything else is undetermined.
        deliberate = _is_full_scope_request(event)
        if deliberate:
            reason = str(event.get("reason") or "full scope requested")
            format_reasons = ["the full scope was requested on purpose"]
        else:
            reason = str(event.get("reason") or "risk surface undetermined, "
                                             "fell back to the full scope")
            format_reasons = ["the gate is not judging a pull request"]
        fields = _full_fields(root, reason, "", forced, diff_range, changed,
                              format_reasons, undecided=not deliberate)
        return _build(fields, surfaces)

    base, head = _event_shas(event)
    after = event.get("after")
    before = event.get("before")
    after_sha = after if isinstance(after, str) and SHA.fullmatch(after) else head
    before_sha = before if isinstance(before, str) and SHA.fullmatch(before) else None

    current = _surface_at(root, base, after_sha)
    previous = None
    if before_sha:
        try:
            previous = _surface_at(root, base, before_sha)
        except (subprocess.CalledProcessError, OSError) as error:
            # A force push replaces the branch's history, so the runner never
            # fetched the ``before`` head and its surface cannot be read.  The
            # previous surface is an **absence claim** -- it is what lets the
            # gate say "nothing changed".  A ``before`` that cannot be read
            # gives that claim up, exactly as an opened pull request does;
            # the current contribution, which resolved, still narrows the
            # scope.  Failing the whole judgement here instead forced every
            # job onto pure documentation changes (issue #539); the current
            # head being unreadable remains a failed judgement, because
            # ``current`` above is resolved first.
            print(f"warning: the previous head {before_sha} cannot be read "
                  f"({error}); judging the pull request as if it was just "
                  "opened", file=sys.stderr)

    surface_changed = True
    base_moved = False
    if previous is not None:
        base_moved = _base_moved_under(root, base, before_sha, after_sha)
        surface_changed = (
            previous.go_packages != current.go_packages
            or previous.frontend_dirs != current.frontend_dirs
            or _content_fingerprint(root, before_sha, previous)
            != _content_fingerprint(root, after_sha, current)
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
        surface = list(_existing_provider_roots(root)) + [FRONTEND_ROOT.rstrip("/")]
        provider_packages = tuple(_existing_provider_roots(root))

    if not surface_changed and not base_moved:
        reason = "tested surface unchanged and base unchanged"
    elif surface_changed and base_moved:
        reason = "contribution surface changed and the base moved under the branch"
    elif surface_changed:
        reason = "contribution surface changed"
    else:
        reason = ("the base moved under the branch; the merge result is new, "
                  "so the contribution is re-tested against it")

    scope = _classify(list(current.contribution))
    run_code = surface_changed
    run_targeted = surface_changed or base_moved

    format_only = False
    format_reasons = ["the formatting-only fast path does not apply to this event"]
    if _format_fast_path_applies(event)[0]:
        format_only, format_reasons = _format_only_update(root, diff_range)

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
        "fingerprint": _content_fingerprint(root, after_sha, current),
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
    return _build(fields, surfaces)


def _build(fields: Mapping[str, Any],
           surfaces: Sequence[manifest.Surface]) -> Decision:
    """Turn the decision fields into a :class:`Decision`."""
    fields = dict(fields)
    fields["provider_tests"] = (bool(fields["provider_packages"])
                                and fields["run_targeted"])
    decision = Decision(
        jobs=_jobs(fields, surfaces),
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
    coverage = _coverage_rows(decision, surfaces)
    uncovered = (_coverage_required(decision)
                 and any(row.status == "not verified" for row in coverage))
    decision = dataclasses.replace(decision, coverage=coverage, uncovered=uncovered)
    if uncovered:
        # The reason is what a reader sees when a check is skipped.  A surface
        # that nobody compiles or tests must not be skipped in silence.
        decision = dataclasses.replace(
            decision,
            reason=f"{decision.reason}; {_uncovered_reason(coverage)}")
    return decision


def _fallback_decision(event: Mapping[str, Any], root: Path, reason: str,
                       surfaces: Sequence[manifest.Surface],
                       *, undecided: bool = True) -> Decision:
    """The one recovery path: the full scope, and a reason that says why.

    Every failure of the judgement -- an unresolvable range, a commit that
    does not exist, an unreadable manifest -- lands here.  There is no second
    path: the gate falls back to the full scope, marks the decision as
    undecided, and says why in the reason.
    """
    base = ""
    try:
        base, _ = _event_shas(event)
    except ValueError:
        pass
    diff_range, forced, changed = None, True, []
    try:
        diff_range, forced, changed = _select_paths(root, event)
    except (subprocess.CalledProcessError, ValueError, OSError):
        pass
    fields = _full_fields(root, reason, base, True, diff_range, changed,
                          [reason], undecided=undecided)
    return _build(fields, surfaces)


def decide(event: Mapping[str, Any], root: Path) -> Decision:
    """Decide what the gates run for one event, and why.

    A scope-discovery failure must cost compute, never correctness: when the
    surface cannot be resolved every applicable check runs rather than
    silently skipping.  This is the one recovery path of the gate.

    The manifest is part of the judgement: a manifest that cannot be read is
    a failure of the judgement, not a second kind of gate that runs.  It
    therefore takes the **same** path -- the full scope, ``undecided``, and a
    reason that says the manifest was unavailable.
    """
    try:
        surfaces = _manifest_surfaces()
    except manifest.SurfaceManifestError as error:
        print(f"warning: the gate surface manifest is unavailable ({error}); "
              "falling back to the full scope", file=sys.stderr)
        return _fallback_decision(
            event, root,
            "the gate surface manifest is unavailable; fell back to the full scope",
            manifest.load_surfaces_lenient())
    try:
        return _decide(event, root, surfaces)
    except (subprocess.CalledProcessError, ValueError, OSError) as error:
        print(f"warning: unable to resolve the risk surface ({error}); "
              "falling back to the full scope", file=sys.stderr)
        return _fallback_decision(
            event, root, "risk surface undetermined, fell back to the full scope",
            surfaces)


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
                               decision.contribution, patterns,
                               base_moved=decision.base_moved)
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
        # The two steps of the pull request gate that run the test matrix.
        # They read one boolean each: the formatting-only shortcut is
        # already folded in, so no condition has to combine two outputs.
        "run_providers": _text(decision.provider_tests and not decision.format_only),
        "run_frontend": _text(decision.frontend and not decision.format_only),
        "provider_tests": _text(decision.provider_tests),
        "provider_packages": " ".join(decision.provider_packages),
        "base_moved": _text(decision.base_moved),
        "surface_changed": _text(decision.surface_changed),
        "forced": _text(decision.forced),
        "format_only": _text(decision.format_only),
        "format_reasons": " | ".join(decision.format_reasons),
        "changed_paths": str(len(decision.changed_paths)),
        "diff_range": decision.diff_range or "",
        # The coverage of the touched surface: which job verifies it on this
        # head.  One line, because a GitHub output value cannot carry a
        # newline; the table itself lives in the conclusion.
        "coverage": " | ".join(
            f"{row.surface}: {row.status}"
            + (f" by {', '.join(row.verified_by)}" if row.verified_by else "")
            for row in decision.coverage),
        "uncovered": _text(decision.uncovered),
    }
    # A job that watches several surfaces runs when any of them is selected.
    # The published key says so once, not once per surface.
    verdicts: dict[str, bool] = {}
    for job in decision.jobs:
        verdicts[job.key] = verdicts.get(job.key, False) or job.run
    for key, run in verdicts.items():
        outputs[key] = _text(run)
    return outputs


# --------------------------------------------------------------------------
# the published conclusion
# --------------------------------------------------------------------------
def _paths_phrase(paths: Sequence[str]) -> str:
    """The path set of a surface, as one readable phrase."""
    return ", ".join(paths)


def _in_scope_reason(decision: Decision, paths: Sequence[str],
                      subject: str = "what this surface judges",
                      detail: str = "") -> str:
    """Why a check of this gate runs."""
    text = f"in scope: the contribution changes {subject} ({_paths_phrase(paths)})"
    if detail:
        text = f"{text}; {detail}"
    if decision.base_moved:
        text = f"{text}; the base moved under the branch"
    return text


def _out_of_scope_reason(decision: Decision, paths: Sequence[str],
                         subject: str = "what this surface judges", *,
                         format_only: bool = False) -> str:
    """Why a check of this gate does not run.

    Every skip carries a readable reason: a bare ``false`` would leave the
    reader guessing whether the check failed, passed or never ran.
    """
    if format_only:
        return ("the change only reformats the code, so this check is skipped; "
                "the lint check still runs")
    if not decision.run_code:
        if decision.base_moved:
            return ("the base moved under the branch but the contribution's "
                    "surface did not change, so the checks that need a "
                    "changed surface stay away")
        return ("the tested surface and the base did not change, so there is "
                "nothing to re-test")
    return (f"out of scope: the contribution does not change {subject} "
            f"({_paths_phrase(paths)})")


def _check(check: str, name: str, kind: str, surface: str,
           paths: Sequence[str], run: bool, reason: str) -> dict[str, Any]:
    """One published check: what it is, whether it runs, and why."""
    return {
        "check": check,
        "name": name,
        "kind": kind,
        "surface": surface,
        "paths": list(paths),
        "run": run,
        "verdict": "runs" if run else "skipped",
        "reason": reason,
    }


def _workflow_checks(decision: Decision, workflow: str) -> list[dict[str, Any]]:
    """The consuming jobs of one workflow-level gate.

    A workflow-level gate skips whole jobs, so the job is the unit that is
    skipped.  One job may watch several surfaces; it then runs when any of
    them is selected.
    """
    surfaces = {surface.name: surface for surface in _readable_surfaces()}
    grouped: dict[tuple[str, str], dict[str, Any]] = {}
    for job in decision.jobs:
        if job.workflow != workflow:
            continue
        entry = grouped.setdefault((job.workflow, job.job), {
            "name": job.job_name,
            "surfaces": [],
            "paths": [],
            "selected": [],
            "run": False,
            "format_only": False,
        })
        entry["run"] = entry["run"] or job.run
        entry["surfaces"].append(job.surface)
        surface = surfaces.get(job.surface)
        if surface is not None:
            entry["paths"].extend(surface.paths)
            entry["format_only"] = entry["format_only"] or surface.skipped_on_format_only
        if job.run:
            entry["selected"].append(job.surface)

    checks: list[dict[str, Any]] = []
    for (workflow_name, job_key), entry in grouped.items():
        paths = list(dict.fromkeys(entry["paths"]))
        selected_paths: list[str] = []
        for surface_name in entry["selected"]:
            surface = surfaces.get(surface_name)
            if surface is not None:
                selected_paths.extend(surface.paths)
        selected_paths = list(dict.fromkeys(selected_paths))
        if entry["run"]:
            reason = _in_scope_reason(decision, selected_paths or paths)
        else:
            reason = _out_of_scope_reason(
                decision, paths,
                format_only=decision.format_only and entry["format_only"])
        checks.append(_check(
            job_key, entry["name"], "job",
            ", ".join(entry["surfaces"]), paths, entry["run"], reason))
    return checks


def _is_step_level(workflow: str) -> bool:
    """Whether this gate selects its checks at step level.

    A job that watches several surfaces cannot be skipped as a whole: the
    pull request gate's job always runs and its steps are skipped one by
    one.  Such a gate publishes its steps; every other gate publishes the
    jobs it skips.
    """
    counts: dict[str, int] = {}
    for surface in _readable_surfaces():
        for consumer in surface.consumers:
            if consumer.workflow != workflow:
                continue
            counts[consumer.job] = counts.get(consumer.job, 0) + 1
    return any(count > 1 for count in counts.values())


def _step_checks(decision: Decision, workflow: str) -> list[dict[str, Any]]:
    """The checks of a step-level gate.

    The steps that the decision selects read one boolean output each; the
    surfaces without a step of their own -- the governance surfaces -- say
    whether the gate judged itself by the whole scope.
    """
    outputs = write_outputs(decision)
    surfaces = {surface.name: surface for surface in _readable_surfaces()}
    verdicts: dict[str, dict[str, bool]] = {}
    for job in decision.jobs:
        if job.workflow != workflow:
            continue
        verdicts.setdefault(job.job, {})[job.surface] = job.run

    checks: list[dict[str, Any]] = []
    for job_name, surface_verdicts in verdicts.items():
        for surface_name, verdict in surface_verdicts.items():
            surface = surfaces.get(surface_name)
            paths = list(surface.paths) if surface is not None else []
            step = surface.step if surface is not None else None
            if step is not None:
                name = step.name
                output = step.output
                format_only_skips = step.skipped_on_format_only
                subject = step.subject
                run = outputs.get(output, "false") == "true"
                kind = "step"
                detail = ""
                if output == "run_providers" and decision.provider_packages:
                    detail = ("the provider packages are: "
                              + ", ".join(decision.provider_packages))
            else:
                governance = (surface is not None
                              and surface.role == manifest.ROLE_GOVERNANCE)
                name = ("Gate judged by the whole scope" if governance
                        else f"Gate scope: {surface_name}")
                run = verdict
                kind = "surface"
                format_only_skips = False
                subject = ("the gate's own files" if governance
                           else "what this surface judges")
                detail = ""
            if run:
                reason = _in_scope_reason(decision, paths, subject, detail)
            else:
                reason = _out_of_scope_reason(
                    decision, paths, subject,
                    format_only=decision.format_only and format_only_skips)
            checks.append(_check(surface_name, name, kind, surface_name,
                                 paths, run, reason))
    return checks


def _gate_label(workflow: str, root: Path | None) -> str:
    """The name of the gate, as a human reads it.

    The workflow's display name is read from the file with a regular
    expression: the manifest's YAML stays a lazy import, because the
    workflow-level gates read the manifest in a job that installs nothing.
    """
    if root is not None:
        try:
            text = (Path(root) / workflow).read_text(encoding="utf-8")
        except OSError:
            text = ""
        match = re.search(r"^name:\s*(.+?)\s*$", text, re.MULTILINE)
        if match:
            name = match.group(1).strip("'\"")
            return f"{name} gate"
    return f"{Path(workflow).stem} gate"


def gate_conclusion(decision: Decision, workflow: str, *,
                    root: Path | None = None) -> dict[str, Any]:
    """The conclusion one gate publishes: what it tested, why, against what.

    Every gate publishes the same three fields -- ``surface``, ``reason``
    and ``base`` -- because they describe the one decision, not the gate.
    What differs per gate is the list of checks: each entry says whether the
    check runs and, when it does not, why.
    """
    workflow = str(workflow).replace("\\", "/")
    checks = (_step_checks(decision, workflow) if _is_step_level(workflow)
              else _workflow_checks(decision, workflow))
    return {
        "gate": _gate_label(workflow, root),
        "workflow": workflow,
        "surface": " ".join(decision.surface),
        "reason": decision.reason,
        "base": decision.base,
        "undecided": decision.undecided,
        "undecided_note": UNDECIDED_NOTE if decision.undecided else "",
        "coverage_required": _coverage_required(decision),
        "coverage": [dataclasses.asdict(row) for row in decision.coverage],
        "uncovered": decision.uncovered,
        "boundary": BOUNDARY,
        "checks": checks,
    }


def conclusion_markdown(conclusion: Mapping[str, Any]) -> str:
    """The conclusion as it appears in the job summary."""
    lines = [f"## LingDoc gate conclusion: {conclusion['gate']}", ""]
    lines += ["| Field | Value |", "|---|---|"]
    lines.append(f"| Surface | {conclusion['surface'] or '(whole repository)'} |")
    lines.append(f"| Reason | {conclusion['reason'] or '(not recorded)'} |")
    lines.append(f"| Base | {conclusion['base'] or '(not recorded)'} |")
    lines += ["", "| Check | Surface | Verdict | Why |", "|---|---|---|---|"]
    for check in conclusion["checks"]:
        lines.append(f"| {check['name']} | {check['surface']} | "
                     f"{check['verdict']} | {check['reason']} |")
    lines += _coverage_markdown(conclusion)
    lines.append("")
    if conclusion.get("undecided_note"):
        lines += [conclusion["undecided_note"], ""]
    lines += ["Skipped means NOT RUN, not passed.", "", conclusion["boundary"], ""]
    return "\n".join(lines)


def _coverage_markdown(conclusion: Mapping[str, Any]) -> list[str]:
    """The coverage table: which surface was verified, and by which job."""
    rows = conclusion.get("coverage") or []
    if not rows:
        return []
    lines = ["", "| Surface | Paths | Verified by | Status |", "|---|---|---|---|"]
    for row in rows:
        verified_by = ", ".join(row.get("verified_by") or ()) or "(nobody)"
        lines.append(f"| {row['surface']} | {', '.join(row['paths'])} | "
                     f"{verified_by} | {row['status']} |")
    if conclusion.get("coverage_required"):
        lines.append("")
        lines.append("Fresh compile and test evidence is required on this head.")
    else:
        lines.append("")
        lines.append("No fresh evidence is required on this head: the change "
                     "only reformats the code, or the tested content did not "
                     "change.")
    if conclusion.get("uncovered"):
        lines.append("")
        lines.append("A surface above has no compile or test evidence on this "
                     "head.  This gate must not pass on it.")
    return lines


def publish_conclusion(decision: Decision, workflow: str, *,
                       artifact_dir: Path | None = None,
                       summary: Path | None = None,
                       root: Path | None = None) -> dict[str, Any]:
    """Publish the conclusion of one gate: the summary and the artifact.

    The summary is what a reader sees on the pull request page; the
    artifact is what a reviewer can download and check.  Both carry the
    same conclusion, including the reason of every skipped check.
    """
    conclusion = gate_conclusion(decision, workflow, root=root)
    markdown = conclusion_markdown(conclusion)
    if artifact_dir is not None:
        folder = Path(artifact_dir)
        folder.mkdir(parents=True, exist_ok=True)
        (folder / "gate-conclusion.json").write_text(
            json.dumps(conclusion, ensure_ascii=False, indent=2) + "\n",
            encoding="utf-8")
        (folder / "gate-conclusion.md").write_text(markdown, encoding="utf-8")
    if summary is None:
        summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as handle:
            handle.write(markdown + "\n")
    return conclusion


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
        "coverage": [dataclasses.asdict(row) for row in decision.coverage],
        "uncovered": decision.uncovered,
        "decision": outputs,
        "jobs": [dict(dataclasses.asdict(job), key=job.key,
                      readable_key=job.readable_key)
                 for job in decision.jobs],
        "note": "Selection only; not a test result or an approval.",
    }


def _whitespace_check(root: Path, diff_range: str | None) -> int:
    """The boundary check's return code: nonzero when the diff has damage."""
    if not diff_range:
        return 0
    return subprocess.run(["git", "diff", "--check", diff_range, "--"],
                          cwd=root, check=False, stdout=sys.stderr).returncode


def check_whitespace(root: Path, diff_range: str | None) -> None:
    """The pull request gate's boundary check: the diff has no whitespace damage.

    The check covers the **complete** pull request range, not the last push:
    a whitespace defect introduced by an earlier commit is still a defect.
    """
    returncode = _whitespace_check(root, diff_range)
    if returncode:
        raise subprocess.CalledProcessError(
            returncode, ["git", "diff", "--check", diff_range, "--"])


def publish(event: Mapping[str, Any], root: Path, *,
            all_checks: bool = False, whitespace: bool = True,
            gate: str | None = None,
            artifact_dir: Path | None = None,
            require_coverage: bool = False) -> dict[str, Any]:
    """Decide, publish the decision, and check the pull request's boundary.

    This is what the gate's scope step runs: it prints the report, writes the
    outputs the workflow's conditions read, and fails the step when the diff
    carries whitespace damage.

    ``whitespace`` is the boundary check.  The pull request gate owns it; the
    workflow-level gates never ran it, and asking them to would change what
    they do, so they turn it off.

    ``gate`` names the workflow whose gate publishes the conclusion: the
    summary a reader sees on the pull request page and the artifact a
    reviewer can download.  Every gate publishes the same conclusion; what
    differs per gate is which of its checks ran and which did not.

    ``require_coverage`` is the pull request gate's **release condition**: it
    fails the step when a surface the contribution touches has no compile or
    test evidence on this head.  A gate that passes on a surface nobody
    compiled or tested says nothing about that surface, so it must not give a
    green conclusion.
    """
    if all_checks:
        event = {"action": "manual", "reason": "full scope requested"}
    decision = decide(event, root)
    whitespace_range = None
    if isinstance(event.get("pull_request"), Mapping):
        whitespace_range = _pr_range(event)
    published = report(decision, whitespace_range)
    print(json.dumps(published, ensure_ascii=True, indent=2))
    if output_path := os.environ.get("GITHUB_OUTPUT"):
        with open(output_path, "a", encoding="utf-8") as handle:
            for key, value in write_outputs(decision).items():
                handle.write(f"{key}={value}\n")
    # The anchor's conclusion is the scope step's own verdict, so the record
    # is written before the step fails on it -- a red run leaves a red
    # record, and the write never changes which of these failures happens.
    coverage_failed = require_coverage and bool(decision.uncovered)
    whitespace_rc = 0
    if whitespace and not coverage_failed:
        whitespace_rc = _whitespace_check(
            root, whitespace_range or decision.diff_range)
    _write_gate_anchor(artifact_dir, event, root, os.environ,
                       "failure" if coverage_failed or whitespace_rc
                       else "success")
    if gate:
        publish_conclusion(decision, gate, artifact_dir=artifact_dir, root=root)
    if coverage_failed:
        _fail_uncovered(decision)
    if whitespace_rc:
        raise subprocess.CalledProcessError(
            whitespace_rc,
            ["git", "diff", "--check", whitespace_range or decision.diff_range,
             "--"])
    return published


# --------------------------------------------------------------------------
# the run anchor: the reuse verdict's machine record (spec #627)
# --------------------------------------------------------------------------
_ANCHOR_FILE = "gate-anchor.json"


def _anchor_head_sha(event: Mapping[str, Any], env: Mapping[str, str]) -> str:
    """The revision the run's evidence is pinned to: the PR's own head.

    On the runner ``GITHUB_SHA`` is the merge commit of head and main, not
    the revision a later run can compare against; the anchor records the
    pull request's head SHA.
    """
    pull_request = event.get("pull_request")
    if isinstance(pull_request, Mapping):
        head = pull_request.get("head", {}).get("sha", "")
        if isinstance(head, str) and SHA.fullmatch(head):
            return head
    sha = env.get("GITHUB_SHA", "")
    return sha if isinstance(sha, str) and SHA.fullmatch(sha) else ""


def _anchor_pr(event: Mapping[str, Any]) -> int | str:
    """The pull request the run belongs to, or ``""`` when there is none.

    The reuse verdict's first condition compares pull request numbers, and
    the run record cannot carry the number for a fork pull request -- the
    anchor is the reliable source.
    """
    pull_request = event.get("pull_request")
    if isinstance(pull_request, Mapping):
        number = pull_request.get("number")
        if isinstance(number, int) and not isinstance(number, bool):
            return number
    return ""


def _anchor_run_id(env: Mapping[str, str]) -> int | str:
    """This run's id as the number the run record spells, or ``""``."""
    run_id = env.get("GITHUB_RUN_ID", "")
    if isinstance(run_id, str) and run_id.isdecimal():
        return int(run_id)
    return ""


def _now_iso() -> str:
    """The reading's moment, UTC: what makes the anchor's values auditable."""
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def _main_sha(root: Path, env: Mapping[str, str]) -> tuple[str, str]:
    """The main tip this run's evidence was integrated against, and its source.

    The gate runs on the merge ref -- head merged into the main tip of the
    run's moment -- so the merge commit ``GITHUB_SHA`` carries the exact
    integrated main tip as its first parent.  That parent is the anchor's
    ``main_sha``: reading main again later (or the event's stale ``base``)
    would record a main tip the run never tested.  When the checkout did not
    create a merge commit (the pull request is already ahead of main), the
    checkout's ``origin/main`` is the main tip of the run's moment; when
    neither is readable, the record stays incomplete and the reuse verdict
    falls back on its own.
    """
    sha = env.get("GITHUB_SHA", "")
    if isinstance(sha, str) and SHA.fullmatch(sha):
        try:
            parents = _run_git(root, "rev-list", "--parents", "-n", "1",
                               sha).split()
        except (OSError, subprocess.CalledProcessError):
            parents = []
        if len(parents) == 3 and SHA.fullmatch(parents[1]):
            return parents[1], "merge_ref_parent"
    try:
        main = _run_git(root, "rev-parse", "refs/remotes/origin/main")
    except (OSError, subprocess.CalledProcessError):
        main = ""
    if SHA.fullmatch(main):
        return main, "origin_main"
    return "", ""


def _write_gate_anchor(artifact_dir: Path | None, event: Mapping[str, Any],
                       root: Path, env: Mapping[str, str],
                       conclusion: str) -> None:
    """Record the run anchor beside the scope artifact.

    The anchor is an ancillary product of the scope step: a failure to write
    it must never change the gate's own verdict, so every failure here is
    reported and swallowed.  Its ``conclusion`` is the scope step's own
    verdict -- the run's complete verdict (which also depends on the jobs
    after this step) is what the run record carries, and the reuse verdict
    composes its ``status``/``conclusion`` from that record; this
    ``conclusion`` is the artifact-side cross-check.  A field the anchor
    cannot fill truthfully stays empty, and an empty field makes the reuse
    verdict fall back on its own -- never a false pass.
    """
    if artifact_dir is None:
        return
    try:
        folder = Path(artifact_dir)
        folder.mkdir(parents=True, exist_ok=True)
        main_sha, main_source = _main_sha(root, env)
        (folder / _ANCHOR_FILE).write_text(
            json.dumps({"head_sha": _anchor_head_sha(event, env),
                        "main_sha": main_sha,
                        "main_sha_source": main_source,
                        "main_sha_read_at": _now_iso(),
                        "run_id": _anchor_run_id(env),
                        "pr": _anchor_pr(event),
                        "workflow": env.get("GITHUB_WORKFLOW", ""),
                        "conclusion": conclusion},
                       ensure_ascii=False, indent=2) + "\n",
            encoding="utf-8")
    except OSError as error:
        print(f"warning: the gate anchor could not be written ({error}); "
              "the gate's own verdict is unaffected.", file=sys.stderr)


def _fail_uncovered(decision: Decision) -> None:
    """Fail the gate: a touched surface has no compile or test evidence."""
    rows = [row for row in decision.coverage if row.status == "not verified"]
    names = ", ".join(row.surface for row in rows)
    print(f"error: the contribution touches {names}, but no job that compiles "
          "or tests it runs on this head.  This gate must not pass on a "
          "surface that was never tested.", file=sys.stderr)
    for row in rows:
        print(f"  {row.surface}: watched by "
              f"{', '.join(row.watched_by) or '(nobody)'}, none of them runs",
              file=sys.stderr)
    raise SystemExit(1)


def _main() -> None:
    """Print the decision for one event; the gate's offline entry point."""
    parser = argparse.ArgumentParser(description="Decide what the gates run for one event.")
    parser.add_argument("--all", action="store_true", help="Run all applicable checks")
    parser.add_argument("--no-whitespace", dest="whitespace", action="store_false",
                        help="Do not check the diff's whitespace boundary")
    parser.add_argument("--event", type=Path, default=os.environ.get("GITHUB_EVENT_PATH"))
    parser.add_argument("--root", type=Path, default=Path.cwd())
    parser.add_argument("--gate",
                        help="The workflow file whose gate publishes the conclusion "
                             "(summary and artifact)")
    parser.add_argument("--artifact-dir", type=Path,
                        help="Where the conclusion artifact is written")
    parser.add_argument("--require-coverage", action="store_true",
                        help="Fail when a surface the contribution touches has "
                             "no compile or test evidence on this head")
    args = parser.parse_args()
    root = args.root.resolve()

    # A manual dispatch asks for the full scope on purpose.  It is a
    # deliberate decision, not an undetermined surface.
    manual = args.all or os.environ.get("GITHUB_EVENT_NAME") == "workflow_dispatch"
    if manual:
        publish({}, root, all_checks=True, whitespace=args.whitespace,
                gate=args.gate, artifact_dir=args.artifact_dir,
                require_coverage=args.require_coverage)
        return
    if args.event is None:
        parser.error("--event or GITHUB_EVENT_PATH is required")
    event = json.loads(args.event.read_text(encoding="utf-8"))
    publish(event, root, whitespace=args.whitespace, gate=args.gate,
            artifact_dir=args.artifact_dir, require_coverage=args.require_coverage)


if __name__ == "__main__":
    _main()
