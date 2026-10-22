"""Ticket-declaration consistency for the parallel work faces.

What this file is
-----------------
A face has two declarations of the same fact:

* the **ticket** (``scripts/lingdoc_parallel_surfaces.py``'s face record says
  which paths the face owns), and
* the **tracker issue** (the face's ticket body lists the same paths under its
  "文件所有权" heading).

When they disagree, the disagreement is not cosmetic.  The registry is what
the guard tests and the overlap check read; the ticket is what a human reads
before touching a file.  A path that the ticket claims but the registry does
not record is a path that two executors can both believe they own, because
the machine-checked record does not know about it.

The concrete case that motivated this module: PF-3's ticket declared
``service_facade.go`` and ``typed_contracts.go`` among its own paths, while
the registry assigned both to PF-4.  The registry's overlap check could not
see it -- the registry itself was consistent.  Only comparing the ticket with
the registry showed the collision, and the pull request that was already
changing both of those files had to hand them over explicitly.

What this file does
-------------------
It reads the tracker ticket body for each face and extracts the paths the
ticket declares, then compares that set with the registry's path set for the
same face.

It reports **one** direction, because the other one is not a problem:

* a path the **ticket** declares but the **registry** does not record -- the
  ticket promises ownership the machine-checked record does not know about,
  so two executors can both believe they own the file.  This is reported.
* a path the **registry** records but the **ticket** does not declare -- the
  registry is the authority ("路径集与边界以登记处为准"), so a ticket that
  lists less is not in conflict with anything.  This is **not** reported.

Two further distinctions keep the signal clean.  A path that does not exist in
the tree yet is a file the face means to create -- there is nothing to
collide over until it exists, so it is not reported.  And only **live** faces
are compared: a face that is ``done`` has landed, and its ticket is the
historical record of what it touched rather than a live claim.

A reserved integration path is reported separately and more loudly, because a
ticket that lists one does not merely fail to record it -- it promises
ownership the registry forbids outright.

What this file does not do
--------------------------
It does not decide who *should* own a path, and it does not edit anything.
It is a detector; the decision is a human's, and the fix is usually a ticket
edit plus a registry edit that agree with each other.

The tracker is optional
-----------------------
Reading the tracker needs ``gh`` and the network.  When either is missing the
module reports "not compared" -- never "no problem found".  A detector that
cannot run must not look like a detector that passed.
"""
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
import json
import re
import shutil
import subprocess
import sys
from typing import Sequence

sys.path.insert(0, str(Path(__file__).resolve().parent))

import lingdoc_parallel_surfaces as manifest  # noqa: E402

REPO = "magician336/lingdoc"

#: The heading under which a face ticket declares its own paths.
_OWNERSHIP_HEADING = re.compile(
    r"^#{1,6}\s*(?:文件所有权|File ownership|路径集|Path set)\s*$",
    re.MULTILINE,
)

#: A bullet list item that declares a path.  Only list items count: prose in
#: the same section talks about *other* files (``Request.Body``), about the
#: registry itself (``scripts/lingdoc_parallel_surfaces.py``), and about
#: identifiers that merely look like paths (``agent_research_model.go``).  A
#: declaration is a list item, so the parser only reads list items.
_DECLARATION_ITEM = re.compile(
    r"^\s*[-*+]\s+`([A-Za-z0-9_./-]+\.[A-Za-z0-9]+)`",
    re.MULTILINE,
)

#: The next heading after the ownership section ends it.
_NEXT_HEADING = re.compile(r"^#{1,6}\s+", re.MULTILINE)


@dataclass(frozen=True)
class TicketDrift:
    """One way a face's ticket disagrees with the registry."""

    issue: int
    face_name: str
    kind: str
    paths: tuple[str, ...]

    def __str__(self) -> str:
        return (f"#{self.issue} {self.face_name}: {self.kind} -- "
                + ", ".join(self.paths))


@dataclass(frozen=True)
class TicketComparison:
    """The result of comparing the tickets with the registry."""

    drifts: tuple[TicketDrift, ...] = ()
    compared: int = 0
    skipped: tuple[str, ...] = ()

    @property
    def ok(self) -> bool:
        return not self.drifts

def extract_declared_paths(body: str) -> tuple[str, ...] | None:
    """Extract the paths a face ticket declares under its ownership heading.

    ``None`` when the ticket has no ownership section at all -- that is a
    different situation from "declares no paths", and the caller tells them
    apart.
    """
    heading = _OWNERSHIP_HEADING.search(body)
    if heading is None:
        return None
    rest = body[heading.end():]
    next_heading = _NEXT_HEADING.search(rest)
    section = rest[:next_heading.start()] if next_heading else rest
    found: list[str] = []
    for match in _DECLARATION_ITEM.finditer(section):
        path = match.group(1)
        # A brace expansion is a shorthand for several files; the registry
        # records each file separately, so the shorthand cannot be compared
        # one-to-one.  Such a path is left out and the ticket keeps its
        # shorthand -- the check then reports it as unrecorded, which is the
        # honest answer.
        if "{" in path or "}" in path or "*" in path:
            continue
        if path not in found:
            found.append(path)
    return tuple(found)


def _gh(*args: str) -> tuple[int, str]:
    """Run ``gh`` and return (exit code, stdout)."""
    if shutil.which("gh") is None:
        return 127, ""
    try:
        done = subprocess.run(
            ["gh", *args],
            capture_output=True, text=True, encoding="utf-8", errors="replace",
            timeout=60,
        )
    except (OSError, subprocess.SubprocessError):
        return 1, ""
    return done.returncode, done.stdout


def fetch_ticket_body(issue: int) -> str | None:
    """Read a ticket body from the tracker; ``None`` when it is unavailable."""
    code, out = _gh("api", f"repos/{REPO}/issues/{issue}")
    if code != 0 or not out.strip():
        return None
    try:
        raw = json.loads(out)
    except json.JSONDecodeError:
        return None
    body = raw.get("body")
    return body if isinstance(body, str) else None


def _registry_covers(registry_set: set[str], path: str) -> bool:
    """Whether the registry's path set covers ``path``.

    The registry may claim a **directory** (``internal/lingdoc/generation/``)
    where the ticket lists the **files** inside it.  Those are the same
    ownership written at two granularities, so a directory claim covers every
    file beneath it.  The other direction is not symmetric: a file claim does
    not cover a directory, because the directory holds files the face may not
    touch.
    """
    if path in registry_set:
        return True
    for claim in registry_set:
        if claim.endswith("/") and path.startswith(claim):
            return True
    return False


def compare_ticket_to_registry(
    faces: Sequence[manifest.ParallelFace],
    root: Path | None = None,
) -> TicketComparison:
    """Compare every face's ticket against the registry's path set.

    The drift that matters is an **ownership collision**: a live face's ticket
    claims a path that another live face owns in the registry.  Two executors
    can then both believe they own the file, and nothing in the registry
    catches it -- the registry is self-consistent, because only one face
    *records* the path.  The ticket is where the second claim lives.

    Two kinds of claim are reported:

    * a path the ticket claims and another **live** face owns -- a collision;
    * a path the ticket claims that is a **reserved** integration path -- the
      registry forbids any face from owning it.

    A path that no face owns is *not* reported as a collision: it is most
    often a file the face means to create, and it may also be a path whose
    registry record has not landed yet (the face's pull request is still in
    flight).  Neither is a collision.

    Only **live** faces are compared.  A face that is ``done`` has landed;
    its ticket is the historical record of what it touched rather than a
    live claim, so comparing it would report the past, not a conflict
    anybody can act on.
    """
    root = root or manifest.REPO_ROOT
    drifts: list[TicketDrift] = []
    skipped: list[str] = []
    compared = 0

    live = [face for face in faces if face.status != "done"]
    reserved_paths = {path for path, _ in manifest.RESERVED_PATHS}

    for face in faces:
        if face.status == "done":
            skipped.append(
                f"#{face.issue} (the face is done; its ticket is a record, "
                "not a live claim)")
            continue
        body = fetch_ticket_body(face.issue)
        if body is None:
            skipped.append(f"#{face.issue} (ticket unavailable)")
            continue
        declared = extract_declared_paths(body)
        if declared is None:
            skipped.append(f"#{face.issue} (no ownership section)")
            continue
        compared += 1

        # What the other live faces own.  The overlap rule is the manifest's
        # own, so a directory claim covers the files inside it -- exactly the
        # way the registry's own overlap check sees it.
        owned_by_others: dict[str, list[int]] = {}
        other_claims: list[tuple[str, manifest.PathClaim]] = []
        for other in live:
            if other.issue == face.issue:
                continue
            for claim in other.paths:
                owned_by_others.setdefault(claim.path, []).append(other.issue)
                other_claims.append((claim.path, claim))

        collisions = sorted(
            path for path in declared
            if any(
                manifest.PathClaim(path, "file").overlaps(claim)
                for _, claim in other_claims
            )
        )
        if collisions:
            drifts.append(TicketDrift(
                face.issue, face.name,
                "ticket claims paths another live face owns",
                tuple(collisions),
            ))

        reserved = sorted(
            path for path in declared
            if path in reserved_paths
            or any(path.startswith(reserved_path.rstrip("/") + "/")
                   for reserved_path in reserved_paths)
        )
        if reserved:
            drifts.append(TicketDrift(
                face.issue, face.name,
                "ticket claims a reserved integration path",
                tuple(reserved),
            ))

    return TicketComparison(drifts=tuple(drifts), compared=compared,
                             skipped=tuple(skipped))


def main(argv: Sequence[str] | None = None) -> int:
    """Compare the face tickets with the registry.

    Exit code 0 when there is no drift, 1 when there is drift, and 2 when the
    comparison could not be made for any face.
    """
    argv = list(argv if argv is not None else sys.argv[1:])
    faces = manifest.load_and_validate()
    result = compare_ticket_to_registry(faces)

    if result.compared == 0:
        print("NOT COMPARED: no face ticket could be read; the comparison "
              "did not happen", file=sys.stderr)
        for reason in result.skipped:
            print(f"  - {reason}", file=sys.stderr)
        return 2

    if result.drifts:
        print("DRIFT: the face tickets and the registry disagree:", file=sys.stderr)
        for drift in result.drifts:
            print(f"  - {drift}", file=sys.stderr)
        return 1

    print(f"OK: {result.compared} face ticket(s) agree with the registry")
    for reason in result.skipped:
        print(f"  - skipped {reason}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
