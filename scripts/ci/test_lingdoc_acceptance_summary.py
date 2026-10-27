"""Guard tests for the horizontal acceptance summary (#458).

What this file guards
---------------------
``scripts/lingdoc_acceptance_summary.py`` is the summary the judgement
rules demand be **generated, never hand-written** (§6.2).  These tests pin
that it stays a pure projection of the report JSONs:

* the counts it prints are the counts the JSON holds -- a summary that
  invents or drops a row would be a second copy of the facts, the exact
  drift the rules forbid;
* a carried ``DEFERRED_MANUAL`` row covers nothing (the run fact is the
  fact), and a negative ``remaining`` is **shown**, not hidden -- it is the
  pair's own two numbers disagreeing;
* unreadable input is 未比对 (exit 2), not a silent undercount;
* the reports that live on ``main`` are summarised as they are, with the
  counts checked against the live JSON -- an invariant, not a snapshot.

They run offline: no service, no browser, no network.
"""
from __future__ import annotations

import contextlib
import io
import json
from pathlib import Path
import shutil
import sys
import tempfile
import unittest

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "scripts"))

import lingdoc_acceptance_summary as summary  # noqa: E402

HEAD = "aa11bb22cc33"

REPORT = {
    "mode": "acceptance-judgement",
    "head": HEAD,
    "acceptance_point_count": 5,
    "passed": [
        {"id": "US-01", "status": "passed",
         "evidence": f"`TestA`；`go test ./pkg/`；head `{HEAD}`"},
        {"id": "US-02、TD-01", "status": "pass",
         "evidence": f"`「标题」`；`npm test`；head `{HEAD}`"},
    ],
    "not_run": [
        {"id": "US-03", "status": "not_run", "reason": "没有真实浏览器。",
         "unblock": "解除条件：真跑一遍（跟进票 #523）。"},
        {"id": "US-04", "status": "not_run", "reason": "CI 没有 PG service。",
         "unblock": "解除条件：PR 合入后真跑。"},
    ],
    "known_limits": ["语义覆盖无人背书。"],
}


class SummaryCase(unittest.TestCase):

    def setUp(self) -> None:
        self.root = Path(tempfile.mkdtemp(prefix="lingdoc-summary-"))

    def tearDown(self) -> None:
        shutil.rmtree(self.root, ignore_errors=True)

    def write_report(self, name: str, document: dict) -> Path:
        path = self.root / name
        path.write_text(json.dumps(document, ensure_ascii=False),
                        encoding="utf-8")
        return path

    def run_summary(self, *argv: str) -> tuple[int, str]:
        buffer = io.StringIO()
        with contextlib.redirect_stdout(buffer):
            code = summary.main(list(argv))
        return code, buffer.getvalue()


class ProjectionTests(SummaryCase):
    """The summary is a projection: its numbers are the JSON's numbers."""

    def test_the_counts_are_the_json_counts(self):
        path = self.write_report("T900-验证报告.json", REPORT)
        code, output = self.run_summary(str(path))
        self.assertEqual(code, summary.EXIT_OK, output)
        row = next(line for line in output.splitlines()
                   if line.startswith("| T900"))
        for expected in ("| #900 ", "| 5 ", "| 3 ", "| 2 ", "| 2 |", "| 1 |"):
            self.assertIn(expected, row, f"missing {expected!r} in: {row}")

    def test_covered_counts_distinct_coordinates_not_entries(self):
        """One entry covering two coordinates covers two points."""
        self.assertEqual(summary.covered_coordinates(REPORT),
                         {"US-01", "US-02", "TD-01"})

    def test_a_carried_row_covers_nothing(self):
        """The run fact is the fact: a deferral covers no point."""
        document = json.loads(json.dumps(REPORT))
        document["passed"].append({
            "id": "US-05", "status": "not_run",
            "matrix_status": "DEFERRED_MANUAL", "ticket": "#523",
            "unblock": "解除条件：按 #523 真跑（跟进票 #523）。",
        })
        self.assertEqual(summary.covered_coordinates(document),
                         {"US-01", "US-02", "TD-01"})
        self.assertEqual(summary.carried_count(document), 1)

    def test_a_disagreeing_remaining_is_shown_not_hidden(self):
        """declared 5, covered 3: remaining 2 -- and negative stays negative."""
        path = self.write_report("T900-验证报告.json", REPORT)
        _code, output = self.run_summary(str(path))
        self.assertIn("| 5 ", output)
        self.assertIn("| 2 ", output)
        document = json.loads(json.dumps(REPORT))
        document["acceptance_point_count"] = 2
        path = self.write_report("T901-验证报告.json", document)
        _code, output = self.run_summary(str(path))
        self.assertIn("| -1 ", output)

    def test_the_pg_column_counts_postgresql_conditionals(self):
        path = self.write_report("T900-验证报告.json", REPORT)
        _code, output = self.run_summary(str(path))
        row = next(line for line in output.splitlines()
                   if line.startswith("| T900"))
        self.assertIn("| 1 ", row)

    def test_the_json_format_is_machine_readable(self):
        path = self.write_report("T900-验证报告.json", REPORT)
        code, output = self.run_summary(str(path), "--format", "json")
        self.assertEqual(code, summary.EXIT_OK, output)
        rows = json.loads(output)
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["covered"], 3)
        self.assertEqual(rows[0]["remaining"], 2)


class NotComparedTests(SummaryCase):
    """An unreadable input is 未比对, never a silently smaller summary."""

    def test_a_missing_file_is_not_compared(self):
        code, output = self.run_summary(str(self.root / "absent.json"))
        self.assertEqual(code, summary.EXIT_NOT_COMPARED, output)
        self.assertIn("未比对", output)

    def test_an_unreadable_file_is_not_compared(self):
        path = self.root / "broken.json"
        path.write_text("{not json", encoding="utf-8")
        code, output = self.run_summary(str(path))
        self.assertEqual(code, summary.EXIT_NOT_COMPARED, output)
        self.assertIn("未比对", output)

    def test_no_reports_at_all_is_not_compared(self):
        code, output = self.run_summary("--dir", str(self.root),
                                        "--pattern", "T*-验证报告.json")
        self.assertEqual(code, summary.EXIT_NOT_COMPARED, output)
        self.assertIn("未比对", output)


class RealReportTests(SummaryCase):
    """The reports on main are summarised as they are, counts checked live."""

    DIRECTORY = REPO_ROOT / "docs" / "08-本轮实施方案"

    def test_the_real_reports_are_projected_faithfully(self):
        reports = sorted(self.DIRECTORY.glob("T1??-验证报告.json"))
        self.assertTrue(reports, "no T1??-验证报告.json on main; fixture gone")
        argv = [str(path) for path in reports]
        code, output = self.run_summary(*argv, "--format", "json")
        self.assertEqual(code, summary.EXIT_OK, output)
        rows = {row["file"]: row for row in json.loads(output)}
        for path in reports:
            document = json.loads(path.read_text(encoding="utf-8"))
            row = rows[path.name]
            with self.subTest(report=path.name):
                self.assertEqual(row["passed"], len(document.get("passed", [])))
                self.assertEqual(row["not_run"], len(document.get("not_run", [])))
                self.assertEqual(row["head"], str(document.get("head", ""))[:12])
