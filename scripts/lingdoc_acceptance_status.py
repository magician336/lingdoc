#!/usr/bin/env python3
"""The acceptance status vocabulary, and the two gates that read it.

What this file owns
-------------------
One question, one answer: **what does a status word mean, and does it block
anything?**  The words live in four places -- the acceptance protocol, the
judgement rules, the gate definitions and the progress source:

* ``docs/08-本轮实施方案/Beta首版真实验收协议.md`` -- the release protocol
  (which states block a Beta release);
* ``docs/08-本轮实施方案/验收判定口径.md`` -- the judgement rules (which
  states a single acceptance point may take);
* ``docs/agents/development-rules.md`` -- the gate definitions (which states
  block a pull request);
* ``docs/BETA_STATUS.md`` -- the progress source (the vocabulary as used).

When one place means a word differently from another, the reader has to guess
which meaning the author used.  So this module is the **vocabulary** those
four places quote, and ``scripts/ci/test_lingdoc_acceptance_status.py`` is the
contract between them: the documents must name exactly the words this module
names, and the two gates below must behave the way the documents say.

The two gates
-------------
``matrix_blocks_merge``
    The pull request gate.  A matrix row that is still open blocks the merge
    -- except a ``DEFERRED_MANUAL`` row, which is open but *carried*: it
    names a follow-up ticket and the condition that releases it, so the code
    side may go on while the manual acceptance waits.

``beta_release_blocks``
    The Beta release gate.  A main flow that contains any of these is not a
    Beta pass.  A deferred manual acceptance is one of them: **merging the
    code does not pass the acceptance.**

The rule that keeps a deferral honest
--------------------------------------
``DEFERRED_MANUAL`` is the only status that lets an unfinished acceptance sit
in a matrix without blocking the merge.  That makes it the status a lazy
author would reach for to make an unfinished acceptance look finished.  Two
things are therefore mandatory on every ``DEFERRED_MANUAL`` entry -- the
**release condition** (what has to happen) and the **follow-up ticket**
(``#NNN``, who carries it) -- and ``deferred_manual_problems`` says what is
missing.  The guard test injects both violations and checks that they are
caught, because a rule that only fires on the historical inventory is
decoration.

Two status words, and they are not the same thing
-------------------------------------------------
An acceptance row carries two of them, and the guard reads both:

* **the run fact** (``status`` in JSON) -- what this run actually did
  (``not_run``, ``passed``, ``failed``);
* **the matrix registration** (``matrix_status`` in JSON, the 判定状态 column
  in a matrix) -- what the acceptance matrix says about the row (``PASS`` /
  ``FAIL`` / ``NOT RUN`` / ``DEFERRED_MANUAL``).

``DEFERRED_MANUAL`` is a **matrix registration**, not a run fact: a run
either happened or it did not, and what got carried is the row.  A guard
that reads only one of the two fields silently misses every entry written in
the other shape -- which is exactly how the JSON half of this rule stayed
unenforced until the real reports were read.

Where this file lives
---------------------
In ``scripts/`` and not in ``scripts/ci/``: that directory belongs to the
gate judgement layer, whose whole point is that exactly one module answers
"what runs".  This module answers another question -- "what does a status
word mean" -- and a second registry in the judgement layer would blur that
boundary.  Its guard test lives in ``scripts/ci/`` because that is where the
CI runs guard tests from.

What this file does not do
--------------------------
It does not judge an acceptance point: it does not read a matrix and decide
whether a row passed.  The judgement rules say who does that (a later
ticket).  It does not know what a follow-up ticket contains; it only checks
that one is named.
"""
from __future__ import annotations

import json
from pathlib import Path
import re
from typing import Any, Iterable, Iterator, Mapping

REPO_ROOT = Path(__file__).resolve().parents[1]

# --- the words -------------------------------------------------------------

PASS = "PASS"
PASS_FOLLOW_UP = "PASS (follow-up)"
NA = "N/A"
WARNING = "WARNING"
NOT_RUN = "NOT RUN"
BLOCKED = "BLOCKED"
TODO = "TODO"
FAIL = "FAIL"
DEFERRED_MANUAL = "DEFERRED_MANUAL"

#: The pull request matrix vocabulary (gate definitions §2).
GATE_MATRIX_STATUSES = (
    TODO, PASS, PASS_FOLLOW_UP, NA, WARNING, NOT_RUN, BLOCKED, DEFERRED_MANUAL,
)

#: The per-acceptance-point vocabulary (judgement rules §3.1).
JUDGEMENT_STATUSES = (PASS, FAIL, NOT_RUN, DEFERRED_MANUAL)

#: Every word any of the two vocabularies uses.
ALL_STATUSES = tuple(dict.fromkeys(GATE_MATRIX_STATUSES + JUDGEMENT_STATUSES))

#: A status that blocks the merge (gate definitions §2).
MERGE_BLOCKING = (TODO, NOT_RUN, BLOCKED)

#: A status that blocks the Beta release (release protocol §6).
BETA_RELEASE_BLOCKING = (FAIL, BLOCKED, NOT_RUN, DEFERRED_MANUAL)

#: A status with no conclusion yet: the acceptance has not happened.
NO_CONCLUSION = (TODO, NOT_RUN, BLOCKED, DEFERRED_MANUAL)

#: The four places that name the vocabulary.
PROTOCOL = "docs/08-本轮实施方案/Beta首版真实验收协议.md"
JUDGEMENT_RULES = "docs/08-本轮实施方案/验收判定口径.md"
GATE_RULES = "docs/agents/development-rules.md"
PROGRESS_SOURCE = "docs/BETA_STATUS.md"

#: A follow-up ticket number.
TICKET_PATTERN = re.compile(r"#\d+")

#: The words that introduce a release condition.  The Chinese one is the
#: repository's own wording (the judgement rules' own example writes
#: "解除条件：…"); the English one is the JSON field name.
UNBLOCK_MARKERS = ("解除条件", "unblock")

#: The column names that hold a status.  A markdown table that declares one
#: of them is a **status table**: its rows are acceptance points, so a
#: ``DEFERRED_MANUAL`` row in it has to carry its release condition and its
#: follow-up ticket.  A table that merely *talks about* the statuses (the
#: judgement rules' own comparison table) declares no such column and is not
#: scanned -- a rule document has to be able to name the word.
STATUS_COLUMNS = ("判定状态", "验收", "状态")

#: The JSON fields that carry a status word.  ``status`` is the **run fact**
#: ("what this run did"); ``matrix_status`` is the **matrix registration**
#: ("what the acceptance matrix says about the row"); ``判定状态`` is the
#: judgement rules' own Chinese column name.  ``DEFERRED_MANUAL`` is a matrix
#: registration, so the guard has to read the field a document actually uses
#: -- reading only one of them misses every entry written in the other.
STATUS_FIELDS = ("status", "matrix_status", "判定状态")

#: The JSON fields that carry the **release condition** of a deferral.
UNBLOCK_FIELDS = ("unblock", "unblock_condition", "release_condition")

#: The JSON fields that carry the **follow-up ticket** of a deferral.
TICKET_FIELDS = ("ticket", "follow_up_issue")


# --- reading a status ------------------------------------------------------

def normalize_status(raw: Any) -> str:
    """The status word a cell or a field carries.

    A real matrix cell carries the word plus prose: ``**PASS**``,
    ``NOT RUN（Beta 链账本 0 行）``, ``PASS (follow-up)``.  The word is the
    part before the prose; the longest word that fits wins, so
    ``PASS (follow-up)`` is never read as ``PASS``.  A cell that carries no
    known word is returned as it is -- an unknown word is not silently
    turned into a known one.
    """
    text = str(raw or "").strip().replace("**", "").replace("`", "").strip()
    for status in sorted(ALL_STATUSES, key=len, reverse=True):
        if text == status:
            return status
        if text.startswith(status):
            rest = text[len(status):]
            if rest and (rest[0].isalnum() or rest[0] == "_"):
                continue
            return status
    return text


def matrix_blocks_merge(statuses: Iterable[Any]) -> bool:
    """Does a matrix with these statuses block the merge?

    The rule is the one the gate definitions state: ``TODO``, ``NOT RUN``
    and ``BLOCKED`` block.  ``DEFERRED_MANUAL`` does not -- it is an open
    acceptance that is *carried* by a follow-up ticket, and the code side may
    go on.  It is still an open acceptance, so it never counts as a
    conclusion; see ``open_statuses``.
    """
    return any(normalize_status(status) in MERGE_BLOCKING for status in statuses)


def beta_release_blocks(statuses: Iterable[Any]) -> bool:
    """Does a main flow with these statuses block the Beta release?

    The release protocol's gate: the main flow may not contain a ``FAIL``, a
    ``BLOCKED``, a ``NOT RUN`` or a ``DEFERRED_MANUAL``.  A manual
    acceptance that has not happened blocks the release exactly like an
    assertion that has not run -- **the merge of the code does not pass the
    acceptance**.
    """
    return any(normalize_status(status) in BETA_RELEASE_BLOCKING
               for status in statuses)


def open_statuses(statuses: Iterable[Any]) -> list[str]:
    """The statuses of these that have reached no conclusion yet.

    A deferred manual acceptance is in here: it is not done, it is carried.
    The point of keeping it separate from the blocking set is that "not done"
    and "blocks the merge" are two different questions.
    """
    return [status for status in statuses
            if normalize_status(status) in NO_CONCLUSION]


def is_open(status: Any) -> bool:
    """Has this acceptance point reached a conclusion yet?"""
    return normalize_status(status) in NO_CONCLUSION


# --- the rule that keeps a deferral honest --------------------------------

def deferred_manual_problems(unblock: str, ticket: str) -> list[str]:
    """What a ``DEFERRED_MANUAL`` entry is missing.

    Two things, and both are required: the **release condition** (what has to
    happen for the manual acceptance to be done) and the **follow-up ticket**
    (``#NNN``) that carries it.  A deferral without them is a promise with
    nothing behind it -- and a promise without a ticket is exactly how an
    unfinished acceptance gets washed into "looks done".

    The two arguments are the release condition and the ticket *as they were
    read from the entry*.  A caller that only found the condition somewhere
    else in the row passes that cell as both: the ticket has to sit with the
    condition, so that the row names one thing.
    """
    problems: list[str] = []
    if not str(unblock or "").strip():
        problems.append("缺少解除条件")
    if not TICKET_PATTERN.search(str(ticket or "")):
        problems.append("缺少跟进票号（#NNN）")
    return problems


# --- finding the entries ---------------------------------------------------

def _cells(line: str) -> list[str]:
    """The cells of one markdown table row."""
    cells = [cell.strip()
             for cell in line.strip().replace("\\|", "\x00").strip("|").split("|")]
    return [cell.replace("\x00", "|") for cell in cells]


def _is_separator(cells: list[str]) -> bool:
    """Whether a row is the ``| --- | --- |`` separator of a table."""
    return bool(cells) and all(cell and set(cell) <= set("-: ") for cell in cells)


def markdown_tables(text: str) -> list[tuple[int, list[str], list[tuple[int, list[str]]]]]:
    """Every markdown table of a document.

    Each table is returned as ``(header line, header cells, rows)``, and each
    row as ``(line number, cells)``.  The separator row is not a row.
    """
    tables: list[tuple[int, list[str], list[tuple[int, list[str]]]]] = []
    header: list[str] | None = None
    header_line = 0
    rows: list[tuple[int, list[str]]] = []
    for number, line in enumerate(text.splitlines(), 1):
        stripped = line.strip()
        if not stripped.startswith("|"):
            if header is not None:
                tables.append((header_line, header, rows))
                header = None
                rows = []
            continue
        cells = _cells(stripped)
        if header is None:
            header = cells
            header_line = number
            continue
        if _is_separator(cells):
            continue
        rows.append((number, cells))
    if header is not None:
        tables.append((header_line, header, rows))
    return tables


def walk_objects(value: Any, path: str = "") -> Iterator[tuple[str, Mapping[str, Any]]]:
    """Every object of a JSON document, with the path that leads to it."""
    if isinstance(value, Mapping):
        yield path or "$", value
        for key, child in value.items():
            yield from walk_objects(child, f"{path}.{key}" if path else str(key))
    elif isinstance(value, list):
        for index, child in enumerate(value):
            yield from walk_objects(child, f"{path}[{index}]")


def _markdown_entries(path: Path) -> list[dict[str, Any]]:
    """The ``DEFERRED_MANUAL`` rows of one markdown document.

    Only the rows of a **status table** count (a table that declares a
    status column).  A table that compares the states with each other -- the
    judgement rules have one -- declares no status column and is not an
    acceptance record.
    """
    text = path.read_text(encoding="utf-8", errors="replace")
    entries: list[dict[str, Any]] = []
    for _header_line, header, rows in markdown_tables(text):
        if not any(cell.strip() in STATUS_COLUMNS for cell in header):
            continue
        for number, cells in rows:
            if not any(normalize_status(cell) == DEFERRED_MANUAL for cell in cells):
                continue
            condition = next((cell for cell in cells
                              if any(marker in cell for marker in UNBLOCK_MARKERS)), "")
            entries.append({
                "where": f"{path.as_posix()}:{number}",
                "shape": "markdown-row",
                "unblock": condition,
                "ticket": condition,
                "problems": deferred_manual_problems(condition, condition),
            })
    return entries


def _first_text(obj: Mapping[str, Any], fields: tuple[str, ...]) -> str:
    """The first non-empty value among ``fields`` of one object."""
    for field in fields:
        value = obj.get(field)
        if isinstance(value, str) and value.strip():
            return value
    return ""


def _json_entries(path: Path) -> list[dict[str, Any]]:
    """The ``DEFERRED_MANUAL`` objects of one JSON document.

    A row of an acceptance report carries **two** status words, and they are
    not the same thing:

    * ``status`` is the **run fact** -- what this run actually did
      (``not_run``, ``passed``, ``failed`` ...);
    * ``matrix_status`` is the **matrix registration** -- what the
      acceptance matrix says about the row (``PASS`` / ``FAIL`` /
      ``NOT RUN`` / ``DEFERRED_MANUAL``).

    ``DEFERRED_MANUAL`` is a **matrix registration**, not a run fact: a run
    either happened or it did not, and what got carried is the row.  The
    guard reads **both** fields, because which field a document uses depends
    on the shape it follows -- and a guard that only reads one of them
    silently misses every entry written in the other shape.

    The two carriers are read from the row first and from the document root
    second: a report may carry the follow-up ticket once at the top
    (``follow_up_issue``) and leave it off every row.
    """
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError):
        return []
    root = document if isinstance(document, Mapping) else {}
    root_ticket = _first_text(root, TICKET_FIELDS)
    entries: list[dict[str, Any]] = []
    for where, obj in walk_objects(document):
        if not any(normalize_status(obj.get(field)) == DEFERRED_MANUAL
                   for field in STATUS_FIELDS):
            continue
        condition = _first_text(obj, UNBLOCK_FIELDS)
        ticket = _first_text(obj, TICKET_FIELDS) or root_ticket or condition
        entries.append({
            "where": f"{path.as_posix()}:{where}",
            "shape": "json-entry",
            "unblock": condition,
            "ticket": ticket,
            "problems": deferred_manual_problems(condition, ticket),
        })
    return entries


def deferred_manual_entries(root: Path | None = None) -> list[dict[str, Any]]:
    """Every ``DEFERRED_MANUAL`` entry in the repository's acceptance material.

    Two shapes are recognised: a markdown table row whose status cell reads
    ``DEFERRED_MANUAL``, and a JSON object whose status does -- in either
    the ``status`` (run fact) or the ``matrix_status`` (matrix registration)
    field.  Each entry reports where it is, what it carries, and what it is
    missing.
    """
    base = Path(root) if root is not None else REPO_ROOT
    entries: list[dict[str, Any]] = []
    for path in sorted(base.glob("docs/**/*.md")):
        entries.extend(_markdown_entries(path))
    for path in sorted(base.glob("docs/**/*.json")):
        entries.extend(_json_entries(path))
    return entries
