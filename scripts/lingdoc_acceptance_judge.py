#!/usr/bin/env python3
"""The acceptance judge: one matrix plus one report, judged row by row.

What this file owns
-------------------
The judgement rules (``docs/08-本轮实施方案/验收判定口径.md`` §10) leave the
judge itself to a construction ticket -- this is that judge (#458, map #695
M-0).  It reads one acceptance matrix (the six-column judgement table, §3)
and the Spec's ``T*-验证报告.json`` (the machine-readable copy), and re-derives
every row's verdict from the predicates the rules state.  The matrix's own
claim is never trusted: a claimed ``PASS`` whose evidence fails the predicate
is a finding, and the recomputed verdict is what the exit code reports.

What is checked
---------------
Matrix rows (§3, §4):

* the header must carry the citation the rules demand (§8): the rules'
  name, the rules' commit, and the declared point total;
* the coordinate must parse and the status word must be one of the four
  (§3.1); an ``IMPL`` row carries no status -- implementation decisions are
  not acceptance points (§1.2);
* a ``PASS`` row needs the evidence triple (§4.2): a grep-able named case,
  a reproducible command, and a recorded head -- and that head must be the
  head the judgement is made at (§4.3: numbers are never carried over from
  an old head);
* a ``NOT RUN`` row must have its matching entry in the report's
  ``not_run[]`` (§3.3 rule 4) -- a claim of "not run" without the registry
  entry is a silent skip, and a silent skip is a ``FAIL`` (§4.3);
* a ``DEFERRED_MANUAL`` row must carry its release condition and its
  follow-up ticket in the same cell (§5.3); the vocabulary module's own
  predicate is reused so the two readers cannot drift.

Report entries (§5, §6.1 shape):

* every ``passed[]`` entry needs the evidence triple;
* a ``passed[]`` entry whose run fact is not a pass and that carries no
  ``DEFERRED_MANUAL`` registration is a silent skip (§5.3: the two status
  fields are not the same thing);
* every ``not_run[]`` entry names why it did not run and what would unblock
  it (§5.1/§5.2);
* a ``DEFERRED_MANUAL`` entry carries both carriers, with the ticket
  resolved in the fixed order of §5.3.

Cross (JSON is the authority, §6.3): a matrix ``PASS`` row with no
pass-shaped report entry behind it is a finding, and so is the reverse -- a
coordinate the report passes that the matrix never registers.

The head the judgement is made at
---------------------------------
The rules bind evidence to "the head the judgement is based on".  The judge
resolves that head in a fixed order: ``--as-of`` when given, else the
report's own top-level ``head`` field (a pair is judged self-consistent at
the head it declares), else the current git HEAD of the repository the
files live in.  When none of the three can be resolved the judge cannot
compare anything, and it exits with the third state: **未比对** -- because
"no baseline to compare against" must never read as "no problem found".

Three exit codes (§10)
----------------------
0 -- 通过: no finding on any row or entry.
1 -- 发现问题: at least one finding; each is printed with its location.
2 -- 未比对: the baseline could not be obtained (missing file, unreadable
     JSON, no head to judge at).  The word 未比对 is always printed.

``--execute`` runs the reproducible commands of the ``PASS`` rows for real
(the rules' "the command runs green at the head"); the default does not,
because a repository-wide guard must stay cheap.  A guard pins both modes.

What this file does not do
--------------------------
It does not judge whether a named test really covers the point's semantics
(§4.1: nobody does, that cost is accepted); it does not scan the whole
repository -- one invocation is one Spec (§6.2); it does not rewrite any
status word anywhere.
"""
from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Mapping

sys.path.insert(0, str(Path(__file__).resolve().parent))
import lingdoc_acceptance_status as status  # noqa: E402

EXIT_PASS = 0
EXIT_PROBLEMS = 1
EXIT_NOT_COMPARED = 2

NOT_COMPARED = "未比对"

#: The coordinate a matrix row is addressed by (§3.1).  ``TD-02.1`` and
#: ``AC-04-03`` are real shapes on main; ``IMPL`` is accepted only to be
#: rejected when it carries a status (§1.2).
COORDINATE = re.compile(r"^(?:US|TD|IMPL|AC)-\d+(?:[.-]\d+)*$")

#: Every coordinate inside free text -- a report id may name several
#: coordinates ("US-01、US-03") or one wrapped in prose.
COORDINATE_IN_TEXT = re.compile(r"\b(?:US|TD|IMPL|AC)-\d+(?:[.-]\d+)*\b")

#: A grep-able Go case name (§4.2: the Go side names ``TestXxx``).
GO_TEST_NAME = re.compile(r"\bTest[A-Z][A-Za-z0-9_]*\b")

#: A frontend test title, the shape the real matrices write (「…」).
FRONTEND_TITLE = re.compile(r"「[^」]+」")

#: A recorded head: 7 to 40 hex characters, backticked or bare.  Both the
#: full form and the short form appear in real evidence.
HEAD_TOKEN = re.compile(r"\b[0-9a-f]{7,40}\b")

#: The run facts that mean the run happened.  The real reports write both
#: ``pass`` (T139) and ``passed`` (T129).
PASS_RUN_FACTS = ("pass", "passed")

#: Backtick segments that look like a command: they contain a space, or they
#: start with a runner the repository actually uses.
COMMAND_RUNNERS = ("go ", "npm", "npx", "node", "python", "pytest", "make")

DELIVERY_CLASSES = ("已交付", "证据缺失", "未交付")


class NotCompared(Exception):
    """The baseline could not be obtained; the judge must exit 2."""

    def __init__(self, reason: str):
        super().__init__(reason)
        self.reason = reason


@dataclass
class Row:
    """One judgement-table row, parsed but not yet judged."""

    line: int
    coordinate: str
    original: str
    claimed: str
    delivery: str
    evidence: str
    note: str
    findings: list[str] = field(default_factory=list)


# --- reading a judgement table --------------------------------------------


def split_cells(line: str) -> list[str]:
    """The cells of one markdown table row, backtick-aware.

    The real matrices write commands that contain pipes (``go test -run
    'TestA|TestB'``) inside backticks.  A pipe inside a code span is part of
    the cell's content, not a boundary -- splitting on every pipe would cut
    the evidence cell into pieces and every column after it would be read
    wrong.  ``\\|`` is an escaped pipe anywhere, code span or not.
    """
    stripped = line.strip()
    if stripped.startswith("|"):
        stripped = stripped[1:]
    if stripped.endswith("|"):
        stripped = stripped[:-1]
    cells: list[str] = []
    buf: list[str] = []
    in_code = False
    index = 0
    while index < len(stripped):
        char = stripped[index]
        if char == "\\" and index + 1 < len(stripped) and stripped[index + 1] == "|":
            buf.append("|")
            index += 2
            continue
        if char == "`":
            in_code = not in_code
        if char == "|" and not in_code:
            cells.append("".join(buf).strip())
            buf = []
            index += 1
            continue
        buf.append(char)
        index += 1
    cells.append("".join(buf).strip())
    return cells


def judgement_table(text: str) -> tuple[list[str], list[tuple[int, list[str]]]]:
    """The six-column judgement table of a matrix document.

    The table is the one whose header declares 定位坐标 and 判定状态; any
    other table (the rules' own comparison tables, a delivery-class legend)
    is not a judgement table.  Raises ``KeyError`` when the document has none.
    """
    header: list[str] | None = None
    rows: list[tuple[int, list[str]]] = []
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped.startswith("|"):
            if header is not None:
                return header, rows
            continue
        cells = split_cells(stripped)
        if header is None:
            if ("定位坐标" in cells) and ("判定状态" in cells):
                header = cells
            continue
        if all(set(cell) <= set("-: ") for cell in cells):
            continue
        rows.append((len(rows) + 1, cells))
    if header is None:
        raise KeyError("no judgement table")
    return header, rows


def citation_problems(text: str) -> list[str]:
    """What the matrix header's citation line is missing (§8).

    The header must name the rules and their version, record the rules'
    commit, and declare the table's point total -- without them a number
    quoted from the table cannot be traced to a counting rule.
    """
    problems: list[str] = []
    if "《验收判定口径》" not in text:
        problems.append("头部未写《验收判定口径》(口径 §8)")
    if not re.search(r"commit\s*[=＝]\s*`?[0-9a-f]{7,40}", text):
        problems.append("头部未写口径 commit(口径 §8)")
    if not re.search(r"总数\s*[=＝]\s*\d+", text):
        problems.append("头部未写本表验收点总数(口径 §8)")
    return problems


def declared_total(text: str) -> int | None:
    """The point total the header declares, when it declares one."""
    match = re.search(r"总数\s*[=＝]\s*(\d+)", text)
    return int(match.group(1)) if match else None


def column_index(header: list[str], name: str) -> int:
    """Where a named column sits; ``-1`` when absent."""
    for index, cell in enumerate(header):
        if cell.strip().replace("*", "").replace("`", "") == name:
            return index
    return -1


def parse_rows(header: list[str],
               rows: list[tuple[int, list[str]]]) -> list[Row]:
    """The judgement rows, shaped.

    A row shorter than the header is kept: its missing columns read empty
    and the row-level checks report what the shape is missing, instead of
    the parser silently dropping the row from the count.
    """
    coordinate_at = column_index(header, "定位坐标")
    claimed_at = column_index(header, "判定状态")
    delivery_at = column_index(header, "交付分类")
    evidence_at = column_index(header, "证据")
    note_at = column_index(header, "备注")
    original_at = column_index(header, "验收点原文")
    parsed: list[Row] = []
    for number, cells in rows:
        def cell(at: int) -> str:
            return cells[at] if 0 <= at < len(cells) else ""

        raw_coordinate = cell(coordinate_at).strip()
        parsed.append(Row(
            line=number,
            coordinate=raw_coordinate,
            original=cell(original_at),
            claimed=status.normalize_status(cell(claimed_at)),
            delivery=cell(delivery_at).strip(),
            evidence=cell(evidence_at),
            note=cell(note_at),
        ))
    return parsed


# --- the evidence triple (§4.2) --------------------------------------------


def backtick_segments(text: str) -> list[str]:
    """The code spans of one cell, in order."""
    return re.findall(r"`([^`]*)`", text)


def command_segments(text: str) -> list[str]:
    """The code spans of a cell that look like a reproducible command."""
    return [segment for segment in backtick_segments(text)
            if " " in segment or segment.startswith(COMMAND_RUNNERS)]


def named_case_present(text: str) -> bool:
    """Whether the cell names a case a reader can grep (§4.2 item 1)."""
    return bool(GO_TEST_NAME.search(text) or FRONTEND_TITLE.search(text))


def head_tokens(text: str) -> list[str]:
    """Every head-shaped token of a cell, in order."""
    return [token.lower() for token in HEAD_TOKEN.findall(text)]


def head_matches(token: str, judged_head: str) -> bool:
    """Whether a recorded head is the judged head (short form binds too)."""
    token, judged_head = token.lower(), judged_head.lower()
    return token == judged_head or judged_head.startswith(token)


# --- the head the judgement is made at --------------------------------------


def git_head(base: Path) -> str | None:
    """The current HEAD of the repository ``base`` lives in, if any."""
    try:
        result = subprocess.run(
            ["git", "rev-parse", "HEAD"], cwd=str(base),
            capture_output=True, text=True, timeout=30, check=False)
    except (OSError, subprocess.TimeoutExpired):
        return None
    head = result.stdout.strip()
    return head if result.returncode == 0 and head else None


def resolve_judged_head(as_of: str, report: Mapping[str, Any] | None,
                        base: Path) -> tuple[str, str]:
    """The head the judgement is made at, and where it came from.

    The order is fixed: an explicit ``--as-of``, else the report's own
    declared ``head`` (a stored pair is judged self-consistent at the head
    it declares), else the repository's current HEAD.  Resolving to nothing
    is not an empty verdict -- it is 未比对 (§10: a missing baseline must
    never read as a pass).
    """
    if as_of:
        return as_of.strip(), "--as-of"
    if report and str(report.get("head") or "").strip():
        return str(report["head"]).strip(), "报告 head"
    head = git_head(base)
    if head:
        return head, "当前 HEAD"
    raise NotCompared("无法确定判定 head:未提供 --as-of,报告未声明 head,文件所在目录也不是 git 仓库")


# --- judging ----------------------------------------------------------------


def judge_row(row: Row, report: Mapping[str, Any], judged_head: str,
              coordinates: Mapping[str, set[str]]) -> None:
    """One row, judged in place; findings land on the row.

    The predicates are the rules' own (§4.2/§4.3), applied to what the row
    and the report actually carry -- the row's claim is only the claim.
    """
    if not COORDINATE.match(row.coordinate):
        row.findings.append(f"定位坐标无法识别:{row.coordinate!r}(口径 §3.1)")
        return
    if row.coordinate.startswith("IMPL"):
        row.findings.append("IMPL 是实现决策不是验收点,不得携带判定状态(口径 §1.2)")
        return
    if row.claimed not in status.JUDGEMENT_STATUSES:
        row.findings.append(f"判定状态不是四值之一:{row.claimed!r}(口径 §3.1)")
        return
    if row.claimed == status.PASS and row.delivery not in DELIVERY_CLASSES:
        row.findings.append(f"交付分类必须是三分之一:{row.delivery!r}(口径 §3.1)")

    if row.claimed == status.PASS:
        _judge_pass_row(row, judged_head, coordinates)
    elif row.claimed == status.NOT_RUN:
        _judge_not_run_row(row, report, coordinates)
    elif row.claimed == status.DEFERRED_MANUAL:
        _judge_deferred_row(row, report, coordinates)


def _judge_pass_row(row: Row, judged_head: str,
                    coordinates: Mapping[str, set[str]]) -> None:
    """The ``PASS`` predicate: the evidence triple, bound to the head."""
    if not named_case_present(row.evidence):
        row.findings.append("PASS 缺少可 grep 的具名用例(口径 §4.2 三件套之一)")
    if not command_segments(row.evidence):
        row.findings.append("PASS 缺少可复现命令(口径 §4.2 三件套之一)")
    heads = head_tokens(row.evidence)
    if not heads:
        row.findings.append("PASS 缺少落证 head(口径 §4.2 三件套之一)")
    elif not any(head_matches(token, judged_head) for token in heads):
        row.findings.append(f"证据 head ≠ 判定 head {judged_head[:12]}(口径 §4.3:数字不得沿用旧 head)")
    if not coordinates.get("has_report"):
        row.findings.append("未提供 --report,无法核对 passed[] 的支撑条目(口径 §6.3:JSON 权威)")
    elif row.coordinate not in coordinates["passed"]:
        row.findings.append("矩阵判 PASS,但验证报告的 passed[] 没有通过条目支撑(口径 §6.3:JSON 权威)")


def _judge_not_run_row(row: Row, report: Mapping[str, Any] | None,
                       coordinates: Mapping[str, set[str] | dict]) -> None:
    """A ``NOT RUN`` row must be registered, not just claimed (§3.3/§4.3)."""
    if not coordinates.get("has_report"):
        row.findings.append("未提供 --report,无法核对 not_run[] 登记(口径 §3.3 第 4 条)")
        return
    if row.coordinate in coordinates["deferred"]:
        row.findings.append("该坐标在报告中登记为 DEFERRED_MANUAL,矩阵却写 NOT RUN;两处口径不一致")
        return
    entry = coordinates["not_run"].get(row.coordinate)
    if entry is None:
        row.findings.append("NOT RUN 在验证报告的 not_run[] 无对应条目(静默 SKIP 判 FAIL,口径 §4.3)")
        return
    if not str(entry.get("reason") or "").strip():
        row.findings.append("not_run[] 条目缺 reason(口径 §5.1)")
    if not str(entry.get("unblock") or "").strip():
        row.findings.append("not_run[] 条目缺机器可读的 unblock(口径 §5.2)")


def _judge_deferred_row(row: Row, report: Mapping[str, Any] | None,
                        coordinates: Mapping[str, set[str] | dict]) -> None:
    """A carried deferral keeps both carriers in one cell (§5.3)."""
    if not coordinates.get("has_report"):
        row.findings.append("未提供 --report,无法核对承接条目(口径 §5.3)")
    elif row.coordinate not in coordinates["deferred"]:
        row.findings.append("DEFERRED_MANUAL 在验证报告中无同坐标的承接条目(口径 §5.3)")
    condition_cell = next((cell_text for cell_text in (row.evidence, row.note)
                           if any(marker in cell_text
                                  for marker in status.UNBLOCK_MARKERS)), "")
    problems = status.deferred_manual_problems(condition_cell, condition_cell)
    row.findings.extend(f"矩阵行{problem}" for problem in problems)


def report_coordinates(report: Mapping[str, Any]) -> dict[str, set[str] | dict]:
    """The coordinate index the cross checks read.

    ``passed`` -- coordinates a pass-shaped entry covers; ``not_run`` --
    coordinate to its registry entry; ``deferred`` -- coordinates whose
    entry carries the ``DEFERRED_MANUAL`` registration (in either status
    field, §5.3).
    """
    index: dict[str, set[str] | dict] = {
        "passed": set(), "not_run": {}, "deferred": set(),
    }
    for list_name in ("passed", "not_run"):
        entries = report.get(list_name)
        if not isinstance(entries, list):
            continue
        for entry in entries:
            if not isinstance(entry, dict):
                continue
            entry_coordinates = set(COORDINATE_IN_TEXT.findall(str(entry.get("id") or "")))
            deferred = any(status.normalize_status(entry.get(field_name)) == status.DEFERRED_MANUAL
                           for field_name in status.STATUS_FIELDS)
            if deferred:
                index["deferred"].update(entry_coordinates)
            if list_name == "passed":
                run_fact = str(entry.get("status") or "").strip().casefold()
                if run_fact in PASS_RUN_FACTS:
                    index["passed"].update(entry_coordinates)
            else:
                for coordinate in entry_coordinates:
                    index["not_run"].setdefault(coordinate, entry)
    return index


def _first_nonempty(obj: Mapping[str, Any], fields: tuple[str, ...]) -> str:
    """The first non-empty string among ``fields`` (§5.3's ticket lookup)."""
    for field_name in fields:
        value = obj.get(field_name)
        if isinstance(value, str) and value.strip():
            return value
    return ""


def judge_report(report: Mapping[str, Any], judged_head: str) -> list[str]:
    """The report-side checks, independent of any matrix (§5, §5.3)."""
    findings: list[str] = []
    document_ticket = _first_nonempty(report, status.TICKET_FIELDS)
    for list_name in ("passed", "not_run"):
        entries = report.get(list_name)
        if entries is not None and not isinstance(entries, list):
            findings.append(f"{list_name} 不是数组,报告形状不符(口径 §6.1)")
            continue
        for position, entry in enumerate(entries or []):
            where = f"{list_name}[{position}]"
            if not isinstance(entry, dict):
                findings.append(f"{where} 不是对象")
                continue
            entry_id = str(entry.get("id") or "").strip() or "(无 id)"
            findings.extend(_judge_report_entry(
                where, entry_id, entry, document_ticket, judged_head,
                is_passed_list=list_name == "passed"))
    return findings


def _judge_report_entry(where: str, entry_id: str, entry: Mapping[str, Any],
                        document_ticket: str, judged_head: str,
                        *, is_passed_list: bool) -> list[str]:
    """One report entry, judged."""
    findings: list[str] = []
    run_fact = str(entry.get("status") or "").strip().casefold()
    deferred = any(status.normalize_status(entry.get(field_name)) == status.DEFERRED_MANUAL
                   for field_name in status.STATUS_FIELDS)
    evidence = str(entry.get("evidence") or "")
    if deferred:
        condition = _first_nonempty(entry, status.UNBLOCK_FIELDS)
        ticket = _first_nonempty(entry, status.TICKET_FIELDS)
        if not ticket and not _names_field(entry, status.TICKET_FIELDS):
            ticket = document_ticket
        if not ticket:
            ticket = condition
        for problem in status.deferred_manual_problems(condition, ticket):
            findings.append(f"{where}({entry_id}){problem}(口径 §5.3)")
        return findings
    if is_passed_list and run_fact not in PASS_RUN_FACTS:
        findings.append(
            f"{where}({entry_id})的运行事实是 {run_fact or '(空)'},"
            "却放在 passed[] 且未被承接 —— 静默 SKIP 判 FAIL(口径 §4.3/§5.3)")
        return findings
    if is_passed_list:
        if not named_case_present(evidence):
            findings.append(f"{where}({entry_id})缺具名用例(口径 §4.2)")
        if not command_segments(evidence):
            findings.append(f"{where}({entry_id})缺可复现命令(口径 §4.2)")
        if not head_tokens(evidence):
            findings.append(f"{where}({entry_id})缺落证 head(口径 §4.2)")
    else:
        if not str(entry.get("reason") or "").strip():
            findings.append(f"{where}({entry_id})缺 reason(口径 §5.1)")
        if not str(entry.get("unblock") or "").strip():
            findings.append(f"{where}({entry_id})缺机器可读的 unblock(口径 §5.2)")
    return findings


def _names_field(obj: Mapping[str, Any], fields: tuple[str, ...]) -> bool:
    """Whether the object carries one of ``fields`` at all (§5.3 rule 3)."""
    return any(field_name in obj for field_name in fields)


def judge(matrix_text: str, report: Mapping[str, Any] | None,
          judged_head: str) -> tuple[list[str], dict[str, Any]]:
    """The whole judgement: findings first, then the recomputed summary."""
    findings: list[str] = list(citation_problems(matrix_text) if matrix_text else [])
    coordinates: dict[str, set[str] | dict] = (
        report_coordinates(report) if report else
        {"passed": set(), "not_run": {}, "deferred": set()})
    coordinates["has_report"] = report is not None
    if report is not None:
        findings.extend(judge_report(report, judged_head))
    rows: list[Row] = []
    if matrix_text:
        try:
            header, raw_rows = judgement_table(matrix_text)
        except KeyError:
            findings.append("矩阵中没有六列判定表(口径 §3)")
        else:
            rows = parse_rows(header, raw_rows)
            counted = sum(1 for row in rows if COORDINATE.match(row.coordinate)
                          and not row.coordinate.startswith("IMPL"))
            expected = declared_total(matrix_text)
            if expected is not None and counted != expected:
                findings.append(
                    f"头部声明验收点总数 {expected},表内实际 {counted} 行(口径 §8)")
            seen_coordinates: set[str] = set()
            for row in rows:
                judge_row(row, report or {}, judged_head, coordinates)
                findings.extend(f"判定表第 {row.line} 行({row.coordinate}):{finding}"
                                for finding in row.findings)
                if COORDINATE.match(row.coordinate):
                    seen_coordinates.add(row.coordinate)
            if report:
                for coordinate in sorted(coordinates["passed"] - seen_coordinates):
                    findings.append(
                        f"报告 passed[] 覆盖坐标 {coordinate},矩阵没有该行(口径 §3.3:一行=一个验收点)")
    counts: dict[str, int] = {name: 0 for name in status.JUDGEMENT_STATUSES}
    delivery: dict[str, int] = {name: 0 for name in DELIVERY_CLASSES}
    for row in rows:
        if row.claimed in counts:
            counts[row.claimed] += 1
        if row.delivery in delivery:
            delivery[row.delivery] += 1
    summary: dict[str, Any] = {
        "rows": len(rows), "statuses": counts, "delivery": delivery,
        "problems": len(findings),
    }
    return findings, summary


def execute_pass_commands(matrix_text: str, cwd: Path, timeout: int) -> list[str]:
    """Run the reproducible commands of the ``PASS`` rows; return problems.

    This is the rules' last predicate -- "the command runs green at the
    head" (§4.2) -- applied for real.  Off by default: a guard that ran a
    Spec's whole test suite on every push would not stay a guard.
    """
    problems: list[str] = []
    try:
        _header, raw_rows = judgement_table(matrix_text)
    except KeyError:
        return []
    parsed = parse_rows(_header, raw_rows)
    commands: list[str] = []
    for row in parsed:
        if row.claimed == status.PASS:
            for command in command_segments(row.evidence):
                if command not in commands:
                    commands.append(command)
    for command in commands:
        try:
            result = subprocess.run(command, shell=True, cwd=str(cwd),
                                    capture_output=True, text=True,
                                    timeout=timeout, check=False)
        except subprocess.TimeoutExpired:
            problems.append(f"命令超时({timeout}s):{command}")
            continue
        except OSError as error:
            problems.append(f"命令无法启动:{command}({error})")
            continue
        if result.returncode != 0:
            problems.append(f"命令跑红(退出码 {result.returncode}):{command}")
    return problems


# --- command line -----------------------------------------------------------


def load_report(path: Path | None) -> Mapping[str, Any] | None:
    """The report JSON, or ``None`` when no report was requested."""
    if path is None:
        return None
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError) as error:
        raise NotCompared(f"验证报告读不到:{path}({error})") from error
    except json.JSONDecodeError as error:
        raise NotCompared(f"验证报告不是合法 JSON:{path}({error})") from error
    if not isinstance(document, dict):
        raise NotCompared(f"验证报告顶层不是对象:{path}")
    return document


def load_matrix(path: Path | None) -> str | None:
    """The matrix text, or ``None`` when no matrix was requested."""
    if path is None:
        return None
    try:
        return path.read_text(encoding="utf-8", errors="replace")
    except OSError as error:
        raise NotCompared(f"判定矩阵读不到:{path}({error})") from error


def main(argv: list[str] | None = None) -> int:
    """The judge's command line; returns one of the three exit codes."""
    parser = argparse.ArgumentParser(
        description="按《验收判定口径》判定一张矩阵与一份验证报告(三态退出码)")
    parser.add_argument("--matrix", type=Path, help="判定矩阵 markdown 路径")
    parser.add_argument("--report", type=Path, help="T*-验证报告.json 路径")
    parser.add_argument("--as-of", dest="as_of", default="",
                        help="判定所依据的 head;默认取报告 head,再退到当前 HEAD")
    parser.add_argument("--execute", action="store_true",
                        help="真跑 PASS 行的可复现命令(默认只做结构判定)")
    parser.add_argument("--timeout", type=int, default=600,
                        help="单条命令的超时秒数(默认 600)")
    args = parser.parse_args(argv)
    if args.matrix is None and args.report is None:
        parser.error("至少提供 --matrix 或 --report 之一")

    try:
        report = load_report(args.report)
        matrix_text = load_matrix(args.matrix)
        base = (args.matrix or args.report).resolve().parent
        judged_head, source = resolve_judged_head(args.as_of, report, base)
        findings, summary = judge(matrix_text or "", report, judged_head)
        if args.execute and matrix_text:
            findings.extend(execute_pass_commands(matrix_text, base, args.timeout))
    except NotCompared as not_compared:
        print(f"未比对:{not_compared.reason}")
        return EXIT_NOT_COMPARED

    print(f"判定 head:{judged_head[:12]}(来源:{source})")
    counts = summary["statuses"]
    delivery = summary["delivery"]
    print(
        f"行数 {summary['rows']}"
        f"(PASS {counts[status.PASS]} / FAIL {counts[status.FAIL]}"
        f" / NOT RUN {counts[status.NOT_RUN]}"
        f" / DEFERRED_MANUAL {counts[status.DEFERRED_MANUAL]})"
        f";交付分类:已交付 {delivery['已交付']}"
        f" / 证据缺失 {delivery['证据缺失']}"
        f" / 未交付 {delivery['未交付']}")
    for finding in findings:
        print(f"问题:{finding}")
    if findings:
        print(f"结论:发现问题 {len(findings)} 项")
        return EXIT_PROBLEMS
    print("结论:通过")
    return EXIT_PASS


if __name__ == "__main__":
    sys.exit(main())
