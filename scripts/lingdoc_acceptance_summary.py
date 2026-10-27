#!/usr/bin/env python3
"""The horizontal acceptance summary: generated, never hand-written.

What this file owns
-------------------
The judgement rules (``docs/08-本轮实施方案/验收判定口径.md`` §6.2) forbid a
hand-maintained cross-Spec table: the per-Spec judgement matrices and their
``T*-验证报告.json`` already hold the facts, a second copy written by hand
would drift from them, and the repository has been burned by exactly that
("one fact, several copies, all disagreeing").  So the horizontal view is
**generated**: this script reads the report JSONs and prints one row per
report -- how much the Spec's evidence covers, what is still registered as
not run, what is carried, what remains.

What a row says
---------------
* **declared** -- the point total the pair declared (the report's
  ``acceptance_point_count``, when it declares one; the matrix header is
  the other place, and the judge checks the two against the table).
* **covered** -- the distinct coordinates behind pass-shaped ``passed[]``
  entries.  A run fact is a fact: a carried ``DEFERRED_MANUAL`` row covers
  nothing.
* **remaining** -- declared minus covered, when declared exists.  It can go
  negative: that is not the script's opinion, it is the pair's own two
  numbers disagreeing, and the summary shows it instead of hiding it.
* **not_run / carried** -- the registries' sizes; ``carried`` counts the
  ``DEFERRED_MANUAL`` registrations in either status field (§5.3).
* **pg** -- how many ``not_run[]`` entries are still PostgreSQL-conditional
  (their reason or unblock names PG).  Since #360 landed these are
  *unblocked but unrerun* -- the summary shows them so no one reads an old
  "CI cannot run PG" as still true.

The script reports; it does not judge.  A report whose entries are
incomplete is not this script's finding -- the judge
(``scripts/lingdoc_acceptance_judge.py``) is the one that judges, and the
authority order (§6.3) is untouched here.

Scope is the caller's
---------------------
Which reports are in this campaign is policy, and policy lives with the
caller: pass file paths, or a directory plus a glob pattern.  The script
hard-codes no list of Specs -- a second list of Spec numbers in code would
be exactly the drift the rules forbid.

Exit codes follow the judge's three states: 0 when every requested file was
read, 2 (未比对) when one could not be -- a summary built on files that
half-loaded would silently undercount what remains.
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any, Mapping

sys.path.insert(0, str(Path(__file__).resolve().parent))
import lingdoc_acceptance_status as status  # noqa: E402

EXIT_OK = 0
EXIT_NOT_COMPARED = 2

NOT_COMPARED = "未比对"

COORDINATE_IN_TEXT = re.compile(r"\b(?:US|TD|IMPL|AC)-\d+(?:[.-]\d+)*\b")
TICKET_IN_FILENAME = re.compile(r"\bT(\d+)\b")
PG_MARKER = re.compile(r"\bPG\b|PostgreSQL")

PASS_RUN_FACTS = ("pass", "passed")

DEFAULT_DIR = Path("docs/08-本轮实施方案")
DEFAULT_PATTERN = "T*-验证报告.json"


def load_report(path: Path) -> Mapping[str, Any]:
    """One report, read strictly; unreadable is 未比对, not an empty row."""
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise SystemExit(f"{NOT_COMPARED}:{path} 读不到或不是合法 JSON({error})")
    if not isinstance(document, dict):
        raise SystemExit(f"{NOT_COMPARED}:{path} 顶层不是对象")
    return document


def covered_coordinates(report: Mapping[str, Any]) -> set[str]:
    """The coordinates pass-shaped ``passed[]`` entries cover."""
    covered: set[str] = set()
    entries = report.get("passed")
    if not isinstance(entries, list):
        return covered
    for entry in entries:
        if not isinstance(entry, dict):
            continue
        run_fact = str(entry.get("status") or "").strip().casefold()
        if run_fact not in PASS_RUN_FACTS:
            continue
        covered.update(COORDINATE_IN_TEXT.findall(str(entry.get("id") or "")))
    return covered


def carried_count(report: Mapping[str, Any]) -> int:
    """The ``DEFERRED_MANUAL`` registrations, in either status field."""
    count = 0
    for list_name in ("passed", "not_run"):
        entries = report.get(list_name)
        if not isinstance(entries, list):
            continue
        for entry in entries:
            if not isinstance(entry, dict):
                continue
            if any(status.normalize_status(entry.get(field_name)) == status.DEFERRED_MANUAL
                   for field_name in status.STATUS_FIELDS):
                count += 1
    return count


def pg_conditional_count(report: Mapping[str, Any]) -> int:
    """The ``not_run[]`` entries whose text still names PostgreSQL."""
    entries = report.get("not_run")
    if not isinstance(entries, list):
        return 0
    count = 0
    for entry in entries:
        if not isinstance(entry, dict):
            continue
        text = " ".join(str(entry.get(field_name) or "")
                        for field_name in ("reason", "unblock", "id"))
        if PG_MARKER.search(text):
            count += 1
    return count


def summarize(path: Path) -> dict[str, Any]:
    """One report's row of the summary."""
    report = load_report(path)
    ticket = TICKET_IN_FILENAME.search(path.name)
    declared = report.get("acceptance_point_count")
    covered = covered_coordinates(report)
    remaining = None
    if isinstance(declared, int):
        remaining = declared - len(covered)
    head = str(report.get("head") or "")
    return {
        "file": path.name,
        "ticket": int(ticket.group(1)) if ticket else None,
        "mode": str(report.get("mode") or ""),
        "caliber": str(report.get("caliber") or ""),
        "head": head[:12] if head else "",
        "declared": declared if isinstance(declared, int) else None,
        "covered": len(covered),
        "remaining": remaining,
        "passed": len(report.get("passed") or []) if isinstance(report.get("passed"), list) else 0,
        "not_run": len(report.get("not_run") or []) if isinstance(report.get("not_run"), list) else 0,
        "carried": carried_count(report),
        "pg": pg_conditional_count(report),
        "known_limits": len(report.get("known_limits") or [])
        if isinstance(report.get("known_limits"), list) else 0,
    }


def markdown_table(rows: list[dict[str, Any]]) -> str:
    """The summary as one markdown table."""
    header = ("| 报告 | Spec | 口径 | 判定 head | 声明 | 已覆盖 | 剩余 "
              "| passed | not_run | 承接 | PG 条件 | known_limits |")
    separator = "| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |"
    lines = [header, separator]
    for row in rows:
        remaining = ("—" if row["remaining"] is None else str(row["remaining"]))
        declared = "—" if row["declared"] is None else str(row["declared"])
        ticket = "—" if row["ticket"] is None else f"#{row['ticket']}"
        lines.append(
            f"| {row['file']} | {ticket} | {row['caliber'] or '—'}"
            f" | {row['head'] or '—'} | {declared} | {row['covered']} | {remaining}"
            f" | {row['passed']} | {row['not_run']} | {row['carried']}"
            f" | {row['pg']} | {row['known_limits']} |")
    return "\n".join(lines)


def main(argv: list[str] | None = None) -> int:
    """The summary's command line; 0 or 未比对 (2), never a verdict."""
    parser = argparse.ArgumentParser(
        description="从各 Spec 的 T*-验证报告.json 生成横向汇总(不手写第二份)")
    parser.add_argument("files", nargs="*", type=Path,
                        help="要汇总的验证报告 JSON;不给则用 --dir 与 --pattern")
    parser.add_argument("--dir", type=Path, default=DEFAULT_DIR,
                        help=f"扫描目录(默认 {DEFAULT_DIR})")
    parser.add_argument("--pattern", default=DEFAULT_PATTERN,
                        help=f"文件名模式(默认 {DEFAULT_PATTERN})")
    parser.add_argument("--format", choices=("markdown", "json"), default="markdown",
                        help="输出形态(默认 markdown)")
    args = parser.parse_args(argv)

    paths = args.files or sorted(args.dir.glob(args.pattern))
    if not paths:
        print(f"{NOT_COMPARED}:没有找到任何验证报告({args.dir}/{args.pattern})")
        return EXIT_NOT_COMPARED
    try:
        rows = [summarize(path) for path in paths]
    except SystemExit as exit_failure:
        print(exit_failure)
        return EXIT_NOT_COMPARED

    if args.format == "json":
        print(json.dumps(rows, ensure_ascii=False, indent=2))
    else:
        print(markdown_table(rows))
        print(f"\n共 {len(rows)} 份报告;剩余合计 "
              f"{sum(row['remaining'] for row in rows if row['remaining'] is not None)} 点"
              f"(仅计声明了总数的报告)。")
    return EXIT_OK


if __name__ == "__main__":
    sys.exit(main())
