"""The parallel construction status: one screen that says who is building what.

What this file is
-----------------
The G1-G7 renaming route is executed as **parallel work faces**.  Each face
owns a disjoint set of paths, so several faces can be built at the same time.
Three kinds of state answer "where are we", and each of them lives where it
belongs -- this module only *joins* them, it never stores a fourth copy:

============  ==============================  ==========================
state         lives in                        kept current by
============  ==============================  ==========================
who builds    the tracker ticket's assignee   the executor (native
              (Issue #362 - #369)              GitHub assignee, with
                                               history)
which face    the tracker ticket's label      the person picking up work
is claimable  (``ready-for-agent`` = open)
what is left  the naming registry             the executor of that face,
              (``scripts/lingdoc_naming_        when it repays its own
              registry.py``)                   segment of the ratchet
============  ==============================  ==========================

Why the split
-------------
Putting the claimant in the repository would rot the moment somebody goes
away; putting the path ownership in a ticket would make it unverifiable.  The
registry is the only place that can be *checked* (it is what the guard test
compares the repository against), and the tracker is the only place that
carries who-is-doing-what with a timestamp.  This module reads both and prints
them together.

How to read the output
----------------------
``remaining files`` / ``remaining identifiers`` come from the registry's
*ratchet segment*: the entries still marked as legacy for that ticket.  They go
to zero as the faces repay their debt.  ``baseline`` is the ratchet value the
guard test enforces; it must equal the sum of the remaining segments.

The tracker part is optional.  When ``gh`` is missing or not authenticated,
this module still prints the registry part and says the tracker part is
unavailable -- it never fails the build because the network is down.
"""
from __future__ import annotations

import json
import shutil
import subprocess
import sys
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Mapping, Sequence

sys.path.insert(0, str(Path(__file__).resolve().parent))

import lingdoc_naming_registry as registry  # noqa: E402

REPO = "magician336/lingdoc"

#: The tickets that carry a parallel work face.
FACE_TICKETS = (362, 363, 364, 365, 366, 367, 368, 369)


@dataclass(frozen=True)
class TrackerTicket:
    """What the tracker says about one face ticket."""

    number: int
    state: str
    assignees: tuple[str, ...]
    labels: tuple[str, ...]
    blocked_by: tuple[int, ...]

    @property
    def claimable(self) -> bool:
        """Whether the ticket is in the pool somebody may pick up."""
        return "ready-for-agent" in self.labels and self.state == "open"


@dataclass(frozen=True)
class FaceState:
    """One face: the registry's view and the tracker's view, joined."""

    ticket: int
    registry_files: int
    registry_files_remaining: int
    registry_ids: int
    registry_ids_remaining: int
    tracker: TrackerTicket | None

    @property
    def claimant(self) -> str:
        if self.tracker is None:
            return "(tracker 不可用)"
        return ", ".join(self.tracker.assignees) or "-"


def registry_view() -> dict[int, dict[str, int]]:
    """Per-ticket counts taken from the registry's ratchet segment."""
    files_remaining: dict[int, int] = {}
    files_total: dict[int, int] = {}
    for entry in registry.NAMED_FILES:
        ticket = entry["ticket"]
        files_total[ticket] = files_total.get(ticket, 0) + 1
        if entry["legacy"]:
            files_remaining[ticket] = files_remaining.get(ticket, 0) + 1

    file_to_ticket = {e["path"]: e["ticket"] for e in registry.NAMED_FILES}
    ids_remaining: dict[int, int] = {}
    ids_total: dict[int, int] = {}
    for entry in registry.NAMED_IDENTIFIERS:
        ticket = file_to_ticket.get(entry["file"])
        if ticket is None:
            continue
        ids_total[ticket] = ids_total.get(ticket, 0) + 1
        if entry["legacy"]:
            ids_remaining[ticket] = ids_remaining.get(ticket, 0) + 1

    return {
        ticket: {
            "files": files_total.get(ticket, 0),
            "files_remaining": files_remaining.get(ticket, 0),
            "ids": ids_total.get(ticket, 0),
            "ids_remaining": ids_remaining.get(ticket, 0),
        }
        for ticket in FACE_TICKETS
    }


def _gh(*args: str) -> tuple[int, str]:
    """Run ``gh`` and return (exit code, stdout).

    The proxy in front of the tracker returns transient errors (TLS handshake
    timeouts, empty bodies) often enough that a single attempt reports a
    reachable tracker as unreachable.  Retry a few times before believing it.
    """
    if shutil.which("gh") is None:
        return 127, ""
    last = (1, "")
    for attempt in range(3):
        try:
            done = subprocess.run(
                ["gh", *args],
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=60,
            )
        except (OSError, subprocess.SubprocessError):
            last = (1, "")
        else:
            if done.returncode == 0 and done.stdout.strip():
                return 0, done.stdout
            last = (done.returncode, done.stdout)
        if attempt < 2:
            time.sleep(2 * (attempt + 1))
    return last


def tracker_view(tickets: Sequence[int] = FACE_TICKETS) -> dict[int, TrackerTicket]:
    """Read each face ticket from the tracker.  Missing tickets are skipped."""
    result: dict[int, TrackerTicket] = {}
    for ticket in tickets:
        code, out = _gh("api", f"repos/{REPO}/issues/{ticket}")
        if code != 0 or not out.strip():
            continue
        try:
            raw = json.loads(out)
        except json.JSONDecodeError:
            continue
        blocked: list[int] = []
        dep_code, dep_out = _gh(
            "api", f"repos/{REPO}/issues/{ticket}/dependencies/blocked_by"
        )
        if dep_code == 0 and dep_out.strip():
            try:
                for item in json.loads(dep_out):
                    blocked.append(int(item["number"]))
            except json.JSONDecodeError:
                pass
        result[ticket] = TrackerTicket(
            number=int(raw.get("number", ticket)),
            state=str(raw.get("state", "unknown")),
            assignees=tuple(a.get("login", "") for a in raw.get("assignees", []) if a.get("login")),
            labels=tuple(l.get("name", "") for l in raw.get("labels", []) if l.get("name")),
            blocked_by=tuple(sorted(blocked)),
        )
    return result


def build_status() -> list[FaceState]:
    """Join the registry view and the tracker view for every face."""
    reg = registry_view()
    tracker = tracker_view()
    faces: list[FaceState] = []
    for ticket in FACE_TICKETS:
        counts = reg.get(ticket, {"files": 0, "files_remaining": 0, "ids": 0, "ids_remaining": 0})
        faces.append(
            FaceState(
                ticket=ticket,
                registry_files=counts["files"],
                registry_files_remaining=counts["files_remaining"],
                registry_ids=counts["ids"],
                registry_ids_remaining=counts["ids_remaining"],
                tracker=tracker.get(ticket),
            )
        )
    return faces


def render(faces: Sequence[FaceState]) -> str:
    """Render the status table."""
    lines: list[str] = []
    lines.append("施工并行状态")
    lines.append("")
    header = (
        f"{'面':<7}{'票状态':<9}{'可领':<7}{'认领人':<16}"
        f"{'拥有文件':<11}{'剩余文件':<13}{'剩余标识符':<14}{'阻塞于'}"
    )
    lines.append(header)
    lines.append("-" * len(header))
    for face in faces:
        tracker = face.tracker
        state = tracker.state if tracker else "?"
        claimable = "是" if (tracker and tracker.claimable) else "否"
        blocked = ", ".join(f"#{b}" for b in (tracker.blocked_by if tracker else ())) or "-"
        lines.append(
            f"#{face.ticket:<6}"
            f"{state:<9}"
            f"{claimable:<7}"
            f"{face.claimant:<16}"
            f"{face.registry_files:<11}"
            f"{face.registry_files_remaining:<6}/{face.registry_files:<6}"
            f"{face.registry_ids_remaining:<7}/{face.registry_ids:<6}"
            f"{blocked}"
        )
    total_files = sum(f.registry_files for f in faces)
    left_files = sum(f.registry_files_remaining for f in faces)
    total_ids = sum(f.registry_ids for f in faces)
    left_ids = sum(f.registry_ids_remaining for f in faces)
    lines.append("-" * len(header))
    lines.append(
        f"{'合计':<7}{'':<9}{'':<7}{'':<16}"
        f"{total_files:<11}{left_files:<6}/{'':<6}{left_ids:<7}/{'':<6}"
    )
    baseline = registry.RATCHET_BASELINE
    lines.append("")
    lines.append(
        f"棘轮基线：{baseline['files']} 文件 / {baseline['exported_identifiers']} 导出标识符"
        f" / {baseline['private_identifiers']} 文件私有标识符   @ {baseline['base_sha'][:8]}"
    )
    lines.append(f"统计范围：{baseline['scope']}")
    lines.append("")
    lines.append("「可领 = 是」表示票上带 ready-for-agent 标签且未关闭——可以直接认领开工。")
    lines.append("「剩余」来自登记表的棘轮段；它归零即该面还清。")
    return "\n".join(lines)


def main() -> int:
    print(render(build_status()))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
