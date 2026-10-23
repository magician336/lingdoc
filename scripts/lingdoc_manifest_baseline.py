"""Baseline comparison for the parallel work face manifest.

What this file is
-----------------
``scripts/lingdoc_parallel_surfaces.py`` answers "which paths belong to which
face", and its loader answers "is this manifest internally coherent" -- no two
faces own the same path, a claimed face names its claimant, and so on.  Both of
those questions are about **one** manifest.

This module answers a different question: **is this manifest a step forward from
the baseline?**  A manifest can be perfectly coherent and still be a step
*backwards*, because coherence is judged against the file's own contents.  Two
real cases motivated this module:

* A pull request built on an older ``main`` carried its own copy of the
  manifest.  That copy was internally valid, but merging it would have
  silently reverted two other faces: their ``claimed`` status became
  ``available`` again and their path sets shrank.  Nothing in the manifest
  could see this, because the regression was only visible against ``main``.
* A face's ticket declares a path set that the manifest does not record, so
  the ticket and the manifest disagree about who owns what.

Why the comparison is a *baseline* comparison
---------------------------------------------
The manifest is the live register.  Every face is expected to grow: it gains
paths as the work turns out to touch more files, and it moves
``available -> claimed -> done``.  A rule of the shape "the manifest may not
shrink" would therefore be wrong on its own -- the point is not that the
manifest changes, but that **a change may not undo somebody else's progress**.

So the comparison is asymmetric, and each asymmetry is a decision with a
reason:

``status may not go backwards``
    A face that was ``claimed`` by somebody may not become ``available``
    again, and a face that was ``done`` may not become anything else.  Work
    that was claimed is work that is happening; re-opening it behind the
    claimant's back is exactly the silent overwrite this whole registry
    exists to prevent.  ``available -> claimed`` and ``claimed -> done`` are
    the forward moves and are always fine.

``a face's path set may only shrink when the face itself is finishing``
    Shrinking a live face's path set is how the work silently escapes its
    owner: the paths then belong to nobody, and the next executor may take
    them.  A face that is ``done`` may release paths, because its work has
    landed and its paths are now part of ``main``.

``a path may be handed over, but only to a face that claims it``
    The legitimate way to move a path between faces is that one face gives it
    up and another takes it.  That is what happened when the G6 panel was
    split into its own face: the paths left the completed face and joined the
    new one.  This module does not forbid that -- it requires that the
    receiving side actually claims the path, so a path can never end up owned
    by nobody.

What this file does not do
--------------------------
It does not decide whether a face *may start* -- the ``status`` field records
that, and a human keeps it current.  It does not read the tracker; the ticket
is the place where a claim is made, and this module is about the manifest's
progress relative to a baseline.  It never degrades silently: a comparison it
cannot make is reported as "not compared", never as "no problem found".

Where this file lives
---------------------
It sits in ``scripts/`` next to the manifest it reads, because it is about the
manifest's meaning rather than about what CI runs.  ``scripts/ci/`` stays the
gate judgement layer, where exactly one module answers "what runs".
"""
from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
import subprocess
import sys
from typing import Mapping, Sequence

sys.path.insert(0, str(Path(__file__).resolve().parent))

import lingdoc_parallel_surfaces as manifest  # noqa: E402


@dataclass(frozen=True)
class Regression:
    """One way the current manifest goes backwards from the baseline."""

    face_issue: int
    face_name: str
    kind: str
    detail: str

    def __str__(self) -> str:
        return f"#{self.face_issue} {self.face_name}: {self.kind} -- {self.detail}"


@dataclass(frozen=True)
class Comparison:
    """The result of comparing two manifests."""

    regressions: tuple[Regression, ...] = ()
    compared: bool = False
    reason: str = ""

    @property
    def ok(self) -> bool:
        return self.compared and not self.regressions


#: Statuses that mean the face is *finished*.  A finished face may release
#: its paths, and it may not reopen.
_FINISHED_STATUS = "done"

#: Statuses that mean somebody is actively on the face.  These may not be
#: undone -- except by finishing them, which is forward progress.
_ACTIVE_STATUS = "claimed"


def _path_set(face: manifest.ParallelFace) -> frozenset[str]:
    return frozenset(claim.path for claim in face.paths)


def compare_faces(
    baseline: Sequence[manifest.ParallelFace],
    current: Sequence[manifest.ParallelFace],
) -> tuple[Regression, ...]:
    """Find every way ``current`` goes backwards from ``baseline``.

    The comparison is deliberately asymmetric: growth is always allowed, and
    only the specific backwards moves listed in the module docstring are
    reported.
    """
    baseline_by_issue = {face.issue: face for face in baseline}
    current_by_issue = {face.issue: face for face in current}
    regressions: list[Regression] = []

    # The union of all paths, before and after.  A path that is in the union
    # now is owned by somebody; a path that left the union is owned by
    # nobody.  This is what makes a handover a handover: the path moves from
    # one face to another and stays in the union.
    baseline_union: set[str] = set()
    for face in baseline:
        baseline_union |= _path_set(face)
    current_union: set[str] = set()
    for face in current:
        current_union |= _path_set(face)

    for issue, old in sorted(baseline_by_issue.items()):
        new = current_by_issue.get(issue)
        if new is None:
            # A face disappearing from the manifest is a regression of the
            # strongest kind: its ticket, its path set and its claim all
            # vanish at once.  Callers that legitimately retire a face must
            # say so explicitly.
            regressions.append(Regression(
                issue, old.name, "face-removed",
                "the face is no longer in the manifest; retiring a face must "
                "be an explicit decision, not a side effect of a merge",
            ))
            continue

        # 1. Status may not go backwards.
        #
        # The forward moves are ``available -> claimed`` and
        # ``claimed -> done``; ``done`` is the end of the line.  What is
        # forbidden is a face that somebody was working on quietly becoming
        # available again, and a finished face reopening.
        if old.status == _FINISHED_STATUS and new.status != _FINISHED_STATUS:
            regressions.append(Regression(
                issue, old.name, "status-regression",
                f"the face was done and became {new.status!r}; a finished "
                "face does not reopen silently",
            ))
        elif old.status == _ACTIVE_STATUS and new.status == "available":
            regressions.append(Regression(
                issue, old.name, "status-regression",
                f"the face was {old.status!r} by {old.claimed_by or 'somebody'} "
                f"and became {new.status!r}; claimed work is not handed back "
                "behind the claimant's back",
            ))
        elif old.status == _ACTIVE_STATUS and new.status == "blocked":
            regressions.append(Regression(
                issue, old.name, "status-regression",
                f"the face was {old.status!r} by {old.claimed_by or 'somebody'} "
                f"and became {new.status!r}; a claim is not silently downgraded",
            ))

        # 2. A live face's path set may not shrink.
        #
        # A path that left this face but joined another one is a handover and
        # is fine.  A path that left this face and joined nobody is either
        # released by a finished face (allowed) or lost by a live one (a
        # regression) -- and it is also reported below as unowned, so it is
        # only reported here when the face is still live.
        old_paths = _path_set(old)
        new_paths = _path_set(new)
        released = old_paths - new_paths
        lost = {path for path in released if path not in current_union}
        if lost and new.status != _FINISHED_STATUS:
            regressions.append(Regression(
                issue, old.name, "path-set-shrunk",
                f"{len(lost)} path(s) left the face while it is "
                f"{new.status!r}: " + ", ".join(sorted(lost)[:5])
                + (" ..." if len(lost) > 5 else "")
                + "; a live face may only gain paths, and a finished face "
                  "releases them",
            ))

    # 3. A path may be handed over, but only to a face that claims it.
    #
    # The check is on the union: a path that left every face and joined no
    # other is owned by nobody, and the next executor may take it.  Paths
    # that moved from one face to another are fine -- that is the handover.
    unowned = baseline_union - current_union
    if unowned:
        regressions.append(Regression(
            0, "(no face)", "path-unowned",
            f"{len(unowned)} path(s) are owned by no face: "
            + ", ".join(sorted(unowned)[:5])
            + (" ..." if len(unowned) > 5 else "")
            + "; a path must be handed to the face that takes it over",
        ))

    return tuple(regressions)


def load_manifest_from_source(source: str, module_name: str = "lingdoc_parallel_surfaces"):
    """Load and validate a manifest from arbitrary source text.

    Used to read the baseline out of git history without disturbing the
    manifest module that is already imported.
    """
    import importlib.util
    import tempfile

    with tempfile.TemporaryDirectory() as tmp:
        path = Path(tmp) / f"{module_name}.py"
        path.write_text(source, encoding="utf-8")
        spec = importlib.util.spec_from_file_location(module_name, path)
        module = importlib.util.module_from_spec(spec)
        sys.modules[module_name] = module
        try:
            spec.loader.exec_module(module)
            return module.load_and_validate()
        finally:
            sys.modules.pop(module_name, None)


def git_baseline_source(root: Path, ref: str, relative_path: str) -> str | None:
    """Read a file's content at ``ref``; ``None`` when it is not there."""
    try:
        done = subprocess.run(
            ["git", "-C", str(root), "show", f"{ref}:{relative_path}"],
            capture_output=True, text=True, encoding="utf-8", errors="replace",
            timeout=60,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    if done.returncode != 0:
        return None
    return done.stdout


def _git_output(root: Path, *args: str) -> str | None:
    """Run a git command and return its trimmed stdout, or ``None``."""
    try:
        done = subprocess.run(
            ["git", "-C", str(root), *args],
            capture_output=True, text=True, encoding="utf-8", errors="replace",
            timeout=60,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    if done.returncode != 0:
        return None
    return done.stdout.strip() or None


def _merge_base(root: Path, ref: str) -> str | None:
    """The commit where the current branch and ``ref`` diverged."""
    return _git_output(root, "merge-base", "HEAD", ref)


def compare_against_git_baseline(
    root: Path | None = None,
    ref: str = "origin/main",
    relative_path: str = "scripts/lingdoc_parallel_surfaces.py",
) -> Comparison:
    """Compare the committed manifest against the same file at ``ref``.

    The comparison only runs when the branch **changed** the manifest itself.

    That condition matters.  A branch that started before the base branch
    grew a new face has an older manifest simply because it has not merged
    the base yet -- and git will keep the base's copy of a file the branch
    never touched, so there is nothing to regress.  Flagging that would turn
    every slightly stale branch into a false alarm and train people to
    ignore the check.

    A branch that **did** change the manifest is a different case: it carries
    its own copy, and if that copy was written against an older base it can
    silently undo the base's newer state when the two are merged.  That is
    the case this check is for.

    When the baseline cannot be read the result says "not compared" -- never
    "no regression found".
    """
    root = root or manifest.REPO_ROOT
    source = git_baseline_source(root, ref, relative_path)
    if source is None:
        return Comparison(
            compared=False,
            reason=f"the baseline {ref}:{relative_path} could not be read; "
                   "the comparison did not happen",
        )

    # Did this branch change the manifest relative to where it forked?
    merge_base = _merge_base(root, ref)
    if merge_base is None:
        return Comparison(
            compared=False,
            reason=f"the merge base with {ref} could not be determined; "
                   "the comparison did not happen",
        )
    forked_source = git_baseline_source(root, merge_base, relative_path)
    current_source = git_baseline_source(root, "HEAD", relative_path)
    if current_source is None:
        return Comparison(
            compared=False,
            reason=f"the manifest at HEAD could not be read; "
                   "the comparison did not happen",
        )
    if forked_source is not None and forked_source == current_source:
        return Comparison(
            compared=True,
            reason=f"the branch did not change {relative_path}; the base's "
                   "copy wins on merge, so there is nothing to regress",
        )

    try:
        baseline_faces = load_manifest_from_source(source)
    except Exception as error:  # noqa: BLE001
        return Comparison(
            compared=False,
            reason=f"the baseline {ref}:{relative_path} does not parse as a "
                   f"manifest ({type(error).__name__}: {error}); "
                   "the comparison did not happen",
        )
    current_faces = manifest.load_and_validate()
    regressions = compare_faces(baseline_faces, current_faces)
    return Comparison(
        regressions=regressions,
        compared=True,
        reason=f"compared against {ref}:{relative_path} "
               f"({len(baseline_faces)} baseline face(s), "
               f"{len(current_faces)} current face(s))",
    )


def main(argv: Sequence[str] | None = None) -> int:
    """Compare the committed manifest against a git baseline.

    Exit code 0 when there is no regression, 1 when there is one, and 2 when
    the comparison could not be made -- the three outcomes are distinct on
    purpose, so a caller can tell "checked, fine" from "not checked".
    """
    argv = list(argv if argv is not None else sys.argv[1:])
    ref = "origin/main"
    root = manifest.REPO_ROOT
    if argv:
        if argv[0] == "--base":
            if len(argv) < 2:
                print("usage: lingdoc_manifest_baseline.py [--base <git-ref>]",
                      file=sys.stderr)
                return 2
            ref = argv[1]
        else:
            ref = argv[0]

    result = compare_against_git_baseline(root=root, ref=ref)
    if not result.compared:
        print(f"NOT COMPARED: {result.reason}", file=sys.stderr)
        return 2
    if result.regressions:
        print(f"REGRESSION: the manifest goes backwards from {ref}:", file=sys.stderr)
        for regression in result.regressions:
            print(f"  - {regression}", file=sys.stderr)
        return 1
    print(f"OK: {result.reason}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
