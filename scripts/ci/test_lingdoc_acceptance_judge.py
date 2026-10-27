"""Guard tests for the acceptance judge (#458).

What this file guards
---------------------
``scripts/lingdoc_acceptance_judge.py`` re-derives every row's verdict from
the judgement rules' predicates (§4.2/§4.3).  A judge that silently agrees
with whatever the matrix claims is not a judge -- so these tests pin the
**failure directions**, one injected violation at a time: a ``PASS`` row
missing each of its three evidence pieces, a head carried over from an old
judgement, a ``NOT RUN`` row with no registry entry behind it, a deferral
missing a carrier, a silent skip sitting in ``passed[]``, and every
malformed shape the parser has to refuse.

They also pin the three exit codes (§10), because the third one is the
easy one to get wrong: **未比对** -- a baseline that could not be obtained
-- must never read as "no problem found".

And they run the judge against the pair that lives on ``main``
(``Spec-129-…验收矩阵.md`` × ``T129-验证报告.json``): a judge must be able
to read the repository's own real evidence without crashing, and what it
finds there is pinned as a class, not as a count -- fixing the evidence
must turn the test greener, not red.

They run offline: no service, no browser, no network, no test suite.
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

import lingdoc_acceptance_judge as judge  # noqa: E402

HEAD = "aa11bb22cc33"
OTHER_HEAD = "dd44ee55ff66"
CALIBER_COMMIT = "1f195def6fb9"

MATRIX_HEADER = """# 某 Spec 判定矩阵

> 本表按《验收判定口径》v1.1 判定；口径 commit = `{commit}`；本表验收点总数 = 3（原子验收点口径）。

| 定位坐标 | 验收点原文 | 判定状态 | 交付分类 | 证据 | 备注 |
| --- | --- | --- | --- | --- | --- |
"""

PASS_ROW = ("| US-01 | As a 负责人, I want X. | PASS | 已交付 "
            "| `TestSomethingWorks`；`go test ./pkg/ -run '^TestSomethingWorks$'`"
            f"；head `{HEAD}` | — |\n")
NOT_RUN_ROW = "| US-02 | As a 成员, I want Y. | NOT RUN | 证据缺失 | — | 见 not_run[]。 |\n"
DEFERRED_ROW = ("| US-03 | As a 成员, I want Z. | DEFERRED_MANUAL | 证据缺失 | — "
                "| 解除条件：按 #523 在真实浏览器跑完整链（跟进票 #523）。 |\n")

REPORT_HEAD = {"mode": "acceptance-judgement", "head": HEAD, "base": "0" * 40}
REPORT_PASSED = [{
    "id": "US-01", "status": "passed",
    "evidence": (f"`TestSomethingWorks`；`go test ./pkg/ -run '^TestSomethingWorks$'`"
                 f"；head `{HEAD}`"),
}]
REPORT_NOT_RUN = [{
    "id": "US-02", "status": "not_run",
    "reason": "本轮没有真实浏览器。",
    "unblock": "解除条件：在真实浏览器跑一遍（跟进票 #523）。",
}]
REPORT_DEFERRED = [{
    "id": "US-03", "status": "not_run", "matrix_status": "DEFERRED_MANUAL",
    "ticket": "#523",
    "unblock": "解除条件：按 #523 在真实浏览器跑完整链（跟进票 #523）。",
}]


def matrix_text(*rows: str) -> str:
    return MATRIX_HEADER.format(commit=CALIBER_COMMIT) + "".join(rows)


def report(**overrides) -> dict:
    document = {
        **REPORT_HEAD,
        "passed": json.loads(json.dumps(REPORT_PASSED)),
        "not_run": json.loads(json.dumps(REPORT_NOT_RUN + REPORT_DEFERRED)),
    }
    document.update(overrides)
    return document


class PairCase(unittest.TestCase):
    """A harness that writes one matrix/report pair and judges it."""

    def setUp(self) -> None:
        self.root = Path(tempfile.mkdtemp(prefix="lingdoc-judge-"))

    def tearDown(self) -> None:
        shutil.rmtree(self.root, ignore_errors=True)

    def write(self, matrix: str | None, report_document: dict | None = None
              ) -> tuple[Path, Path | None]:
        matrix_path = None
        if matrix is not None:
            matrix_path = self.root / "matrix.md"
            matrix_path.write_text(matrix, encoding="utf-8")
        report_path = None
        if report_document is not None:
            report_path = self.root / "T999-验证报告.json"
            report_path.write_text(
                json.dumps(report_document, ensure_ascii=False), encoding="utf-8")
        return matrix_path, report_path

    def run_judge(self, matrix_path: Path | None,
                  report_path: Path | None = None, *extra: str
                  ) -> tuple[int, str]:
        argv: list[str] = []
        if matrix_path is not None:
            argv += ["--matrix", str(matrix_path)]
        if report_path is not None:
            argv += ["--report", str(report_path)]
        argv += list(extra)
        buffer = io.StringIO()
        with contextlib.redirect_stdout(buffer):
            code = judge.main(argv)
        return code, buffer.getvalue()

    def findings_of(self, matrix: str, report_document: dict | None = None
                    ) -> list[str]:
        matrix_path, report_path = self.write(matrix, report_document)
        code, output = self.run_judge(matrix_path, report_path)
        self.assertEqual(code, judge.EXIT_PROBLEMS,
                         f"the injected violation was not caught:\n{output}")
        return [line[len("问题:"):] for line in output.splitlines()
                if line.startswith("问题:")]


class ThreeStateExitTests(PairCase):
    """The three exit codes, and the word 未比对 on the third (§10)."""

    def test_a_clean_pair_passes_with_zero(self):
        matrix_path, report_path = self.write(matrix_text(PASS_ROW, NOT_RUN_ROW,
                                                          DEFERRED_ROW), report())
        code, output = self.run_judge(matrix_path, report_path)
        self.assertEqual(code, judge.EXIT_PASS, output)
        self.assertIn("结论:通过", output)

    def test_an_injected_violation_fails_with_one(self):
        matrix_path, report_path = self.write(
            matrix_text(PASS_ROW.replace(HEAD, OTHER_HEAD),
                        NOT_RUN_ROW, DEFERRED_ROW), report())
        code, output = self.run_judge(matrix_path, report_path)
        self.assertEqual(code, judge.EXIT_PROBLEMS, output)
        self.assertIn("结论:发现问题", output)

    def test_no_head_anywhere_is_not_compared_with_two(self):
        """No --as-of, no report head, no git: nothing is comparable."""
        matrix_path, _ = self.write(matrix_text(PASS_ROW))
        (self.root / "T999-验证报告.json").write_text(
            json.dumps({"passed": [], "not_run": []}), encoding="utf-8")
        code, output = self.run_judge(matrix_path, self.root / "T999-验证报告.json")
        self.assertEqual(code, judge.EXIT_NOT_COMPARED, output)
        self.assertIn("未比对", output)

    def test_a_missing_file_is_not_compared_not_clean(self):
        code, output = self.run_judge(self.root / "absent.md")
        self.assertEqual(code, judge.EXIT_NOT_COMPARED, output)
        self.assertIn("未比对", output)

    def test_an_unreadable_report_is_not_compared_not_clean(self):
        matrix_path, _ = self.write(matrix_text(PASS_ROW))
        broken = self.root / "broken.json"
        broken.write_text("{not json", encoding="utf-8")
        code, output = self.run_judge(matrix_path, broken)
        self.assertEqual(code, judge.EXIT_NOT_COMPARED, output)
        self.assertIn("未比对", output)

    def test_at_least_one_input_is_required(self):
        with self.assertRaises(SystemExit):
            judge.main([])


class PassRowTests(PairCase):
    """The PASS predicate: every piece of the triple, and the head binding."""

    def test_a_pass_row_without_a_named_case_is_caught(self):
        """No ``TestXxx`` and no quoted title anywhere: nothing to grep."""
        row = PASS_ROW.replace(
            "`TestSomethingWorks`；`go test ./pkg/ -run '^TestSomethingWorks$'`；",
            "`npm run build`；")
        findings = self.findings_of(matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW),
                                    report())
        self.assertTrue(any("缺少可 grep 的具名用例" in finding for finding in findings),
                        findings)

    def test_a_pass_row_without_a_command_is_caught(self):
        row = PASS_ROW.replace(
            "`go test ./pkg/ -run '^TestSomethingWorks$'`；", "")
        findings = self.findings_of(matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW),
                                    report())
        self.assertTrue(any("缺少可复现命令" in finding for finding in findings),
                        findings)

    def test_a_pass_row_without_a_head_is_caught(self):
        row = PASS_ROW.replace(f"；head `{HEAD}`", "")
        findings = self.findings_of(matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW),
                                    report())
        self.assertTrue(any("缺少落证 head" in finding for finding in findings),
                        findings)

    def test_a_pass_row_bound_to_an_old_head_is_caught(self):
        """The rules' hard line: numbers are never carried from an old head."""
        row = PASS_ROW.replace(HEAD, OTHER_HEAD)
        findings = self.findings_of(matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW),
                                    report())
        self.assertTrue(any("证据 head ≠ 判定 head" in finding for finding in findings),
                        findings)

    def test_a_pass_row_the_report_does_not_back_is_caught(self):
        findings = self.findings_of(matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW),
                                    report(passed=[]))
        self.assertTrue(any("没有通过条目支撑" in finding for finding in findings),
                        findings)

    def test_a_short_head_token_binds_to_the_full_judged_head(self):
        row = PASS_ROW.replace(f"`{HEAD}`", "`aa11bb2`")
        matrix_path, report_path = self.write(
            matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW), report())
        code, output = self.run_judge(matrix_path, report_path)
        self.assertEqual(code, judge.EXIT_PASS, output)


class NotRunRowTests(PairCase):
    """A NOT RUN row must be registered, not merely claimed (§4.3)."""

    def test_an_unregistered_not_run_row_is_a_silent_skip(self):
        findings = self.findings_of(matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW),
                                    report(not_run=[]))
        self.assertTrue(any("无对应条目" in finding and "静默 SKIP" in finding
                            for finding in findings), findings)

    def test_a_registered_not_run_row_is_accepted(self):
        matrix_path, report_path = self.write(
            matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW), report())
        code, output = self.run_judge(matrix_path, report_path)
        self.assertEqual(code, judge.EXIT_PASS, output)

    def test_an_entry_without_an_unblock_is_caught(self):
        entry = {"id": "US-02", "status": "not_run", "reason": "本轮没有真实浏览器。"}
        findings = self.findings_of(matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW),
                                    report(not_run=[entry, *REPORT_DEFERRED]))
        self.assertTrue(any("缺机器可读的 unblock" in finding for finding in findings),
                        findings)

    def test_an_entry_without_a_reason_is_caught(self):
        entry = {"id": "US-02", "status": "not_run", "unblock": "解除条件：真跑。"}
        findings = self.findings_of(matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW),
                                    report(not_run=[entry, *REPORT_DEFERRED]))
        self.assertTrue(any("缺 reason" in finding for finding in findings),
                        findings)


class DeferredRowTests(PairCase):
    """A carried deferral keeps both carriers, in the matrix and in the JSON."""

    def test_a_row_without_a_ticket_is_caught(self):
        row = DEFERRED_ROW.replace(
            "解除条件：按 #523 在真实浏览器跑完整链（跟进票 #523）", "解除条件：真跑完整链")
        findings = self.findings_of(matrix_text(PASS_ROW, NOT_RUN_ROW, row),
                                    report())
        self.assertTrue(any("缺少跟进票号" in finding for finding in findings),
                        findings)

    def test_a_row_without_a_release_condition_is_caught(self):
        row = DEFERRED_ROW.replace("解除条件：按 #523 在真实浏览器跑完整链", "真跑一遍")
        findings = self.findings_of(matrix_text(PASS_ROW, NOT_RUN_ROW, row),
                                    report())
        self.assertTrue(any("缺少解除条件" in finding for finding in findings),
                        findings)

    def test_a_row_without_a_report_entry_is_caught(self):
        findings = self.findings_of(matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW),
                                    report(not_run=REPORT_NOT_RUN))
        self.assertTrue(any("无同坐标的承接条目" in finding for finding in findings),
                        findings)

    def test_a_json_carrier_with_an_emptied_ticket_is_not_rescued(self):
        """§5.3 rule 3: an emptied field is a forgotten ticket; the document's
        ticket must not paper over it, and a condition that names no ticket
        cannot rescue it either."""
        entry = dict(REPORT_DEFERRED[0], ticket="",
                     unblock="解除条件：按跟进票的验收步骤在真实浏览器跑完整链。")
        document = report(not_run=[*REPORT_NOT_RUN, entry], follow_up_issue="#246")
        findings = self.findings_of(matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW),
                                    document)
        self.assertTrue(any("缺少跟进票号" in finding for finding in findings),
                        findings)

    def test_a_json_carrier_resolves_the_ticket_from_the_document(self):
        """§5.3 rule 2: a row naming no ticket field may use the document's."""
        entry = {key: value for key, value in REPORT_DEFERRED[0].items()
                 if key != "ticket"}
        document = report(not_run=[*REPORT_NOT_RUN, entry],
                          follow_up_issue="#523")
        matrix_path, report_path = self.write(
            matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW), document)
        code, output = self.run_judge(matrix_path, report_path)
        self.assertEqual(code, judge.EXIT_PASS, output)


class ReportShapeTests(PairCase):
    """The report side: silent skips and the evidence triple (§5.3/§6.1)."""

    def test_a_silent_skip_in_passed_is_caught(self):
        """A row that did not run sits in passed[] with no registration."""
        entry = {"id": "US-09", "status": "not_run",
                 "evidence": f"`TestX`；`go test ./pkg/`；head `{HEAD}`"}
        findings = self.findings_of(matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW),
                                    report(passed=[*REPORT_PASSED, entry]))
        self.assertTrue(any("静默 SKIP" in finding for finding in findings),
                        findings)

    def test_a_carried_row_may_sit_in_passed(self):
        """§5.3: the run fact stays not_run; what is carried is the row."""
        entry = {"id": "US-09", "status": "not_run", "matrix_status": "DEFERRED_MANUAL",
                 "ticket": "#523",
                 "unblock": "解除条件：按 #523 真跑（跟进票 #523）。"}
        document = report(passed=[*REPORT_PASSED, entry])
        matrix_path, report_path = self.write(
            matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW), document)
        code, output = self.run_judge(matrix_path, report_path)
        self.assertEqual(code, judge.EXIT_PASS, output)

    def test_a_passed_entry_without_evidence_pieces_is_caught(self):
        entry = {"id": "US-01", "status": "passed", "evidence": "跑过了"}
        findings = self.findings_of(matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW),
                                    report(passed=[entry]))
        joined = "\n".join(findings)
        for missing in ("缺具名用例", "缺可复现命令", "缺落证 head"):
            self.assertIn(missing, joined)


class MatrixShapeTests(PairCase):
    """The shapes the parser must refuse, and the header it must demand."""

    def test_an_unknown_status_word_is_caught(self):
        row = PASS_ROW.replace("| PASS |", "| 完成 |")
        findings = self.findings_of(matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW),
                                    report())
        self.assertTrue(any("不是四值之一" in finding for finding in findings),
                        findings)

    def test_an_impl_row_may_not_carry_a_status(self):
        """Implementation decisions are not acceptance points (§1.2)."""
        row = ("| IMPL-01 | 五项动作是具体五项 | PASS | 已交付 "
               f"| `TestX`；`go test ./pkg/`；head `{HEAD}` | — |\n")
        findings = self.findings_of(matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW),
                                    report())
        self.assertTrue(any("IMPL 是实现决策" in finding for finding in findings),
                        findings)

    def test_an_unreadable_coordinate_is_caught(self):
        row = PASS_ROW.replace("| US-01 |", "| 第一条 |")
        findings = self.findings_of(matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW),
                                    report())
        self.assertTrue(any("定位坐标无法识别" in finding for finding in findings),
                        findings)

    def test_an_unknown_delivery_class_is_caught(self):
        row = PASS_ROW.replace("| 已交付 |", "| 做完了 |")
        findings = self.findings_of(matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW),
                                    report())
        self.assertTrue(any("交付分类必须是三分之一" in finding for finding in findings),
                        findings)

    def test_a_declared_total_that_disagrees_with_the_table_is_caught(self):
        text = matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW).replace(
            "本表验收点总数 = 3", "本表验收点总数 = 99")
        findings = self.findings_of(text, report())
        self.assertTrue(any("头部声明验收点总数" in finding for finding in findings),
                        findings)

    def test_a_missing_citation_piece_is_caught(self):
        text = matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW).replace(
            f"口径 commit = `{CALIBER_COMMIT}`；", "")
        findings = self.findings_of(text, report())
        self.assertTrue(any("头部未写口径 commit" in finding for finding in findings),
                        findings)

    def test_pipes_inside_backticks_do_not_cut_the_evidence_cell(self):
        """The real matrices write `go test -run 'TestA|TestB'` -- the pipe
        inside the code span is content, not a boundary; the columns after
        the evidence cell must survive it."""
        row = ("| US-01 | As a 负责人, I want X. | PASS | 已交付 "
               "| `go test ./pkg/ -run 'TestSomethingWorks|TestOther'`"
               f"；head `{HEAD}` | 备注列完好 |\n")
        document = report(passed=[{
            "id": "US-01", "status": "passed",
            "evidence": (f"`go test ./pkg/ -run 'TestSomethingWorks|TestOther'`"
                         f"；head `{HEAD}`"),
        }])
        matrix_path, report_path = self.write(
            matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW), document)
        code, output = self.run_judge(matrix_path, report_path)
        self.assertEqual(code, judge.EXIT_PASS, output)
        self.assertNotIn("不是四值之一", output,
                         "a backticked pipe cut the row and shifted the columns")


class HeadResolutionTests(PairCase):
    """Where the judged head comes from, and what --as-of changes."""

    def test_the_report_head_is_the_default_judgement_head(self):
        matrix_path, report_path = self.write(
            matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW), report())
        code, output = self.run_judge(matrix_path, report_path)
        self.assertIn(f"判定 head:{HEAD}", output, output)

    def test_as_of_overrides_the_report_head_and_rebinds_the_rows(self):
        """Judging at today's head is the freshness re-check (§4.3)."""
        matrix_path, report_path = self.write(
            matrix_text(PASS_ROW, NOT_RUN_ROW, DEFERRED_ROW), report())
        code, output = self.run_judge(matrix_path, report_path,
                                      "--as-of", OTHER_HEAD)
        self.assertEqual(code, judge.EXIT_PROBLEMS, output)
        self.assertIn(f"判定 head:{OTHER_HEAD}", output)
        self.assertIn("证据 head ≠ 判定 head", output)

    def test_report_only_mode_judges_the_json_side(self):
        _matrix_path, report_path = self.write(None, report())
        code, output = self.run_judge(None, report_path)
        self.assertEqual(code, judge.EXIT_PASS, output)


class ExecuteTests(PairCase):
    """--execute applies the last predicate for real; the default does not."""

    def test_by_default_a_red_command_is_not_run_at_all(self):
        row = PASS_ROW.replace("go test ./pkg/ -run '^TestSomethingWorks$'",
                               "exit 3")
        matrix_path, report_path = self.write(
            matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW),
            report(passed=[{"id": "US-01", "status": "passed",
                            "evidence": f"`TestSomethingWorks`；`exit 3`；head `{HEAD}`"}]))
        code, output = self.run_judge(matrix_path, report_path)
        self.assertEqual(code, judge.EXIT_PASS, output)

    def test_execute_runs_the_commands_and_reports_a_red_one(self):
        row = PASS_ROW.replace("go test ./pkg/ -run '^TestSomethingWorks$'",
                               "exit 3")
        document = report(passed=[{"id": "US-01", "status": "passed",
                                   "evidence": f"`TestSomethingWorks`；`exit 3`；head `{HEAD}`"}])
        matrix_path, report_path = self.write(
            matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW), document)
        code, output = self.run_judge(matrix_path, report_path, "--execute")
        self.assertEqual(code, judge.EXIT_PROBLEMS, output)
        self.assertIn("命令跑红", output)

    def test_execute_accepts_a_green_command(self):
        row = PASS_ROW.replace("go test ./pkg/ -run '^TestSomethingWorks$'",
                               "exit 0")
        document = report(passed=[{"id": "US-01", "status": "passed",
                                   "evidence": f"`TestSomethingWorks`；`exit 0`；head `{HEAD}`"}])
        matrix_path, report_path = self.write(
            matrix_text(row, NOT_RUN_ROW, DEFERRED_ROW), document)
        code, output = self.run_judge(matrix_path, report_path, "--execute")
        self.assertEqual(code, judge.EXIT_PASS, output)


class RealPairTests(unittest.TestCase):
    """The pair on main is the fixture: the judge must read it, and what it
    finds there is pinned as a **class**, never as a count.

    Today's known weakness is frontend rows whose evidence names a test
    **file** but not a test **title** (§4.2: the frontend side names the
    title) -- the judge reports exactly that class.  If the evidence is
    repaired, the findings shrink and this test stays green; if anything
    else ever appears, this test goes red, and that is the point.
    """

    MATRIX = (REPO_ROOT / "docs" / "08-本轮实施方案"
              / "Spec-129-成员角色Scope生命周期验收矩阵.md")
    REPORT = REPO_ROOT / "docs" / "08-本轮实施方案" / "T129-验证报告.json"

    @classmethod
    def setUpClass(cls) -> None:
        text = cls.MATRIX.read_text(encoding="utf-8")
        document = json.loads(cls.REPORT.read_text(encoding="utf-8"))
        judged_head, _source = judge.resolve_judged_head(
            "", document, cls.REPORT.parent)
        cls.findings, cls.summary = judge.judge(text, document, judged_head)
        header, _rows = judge.judgement_table(text)
        cls.declared = judge.declared_total(text)
        cls.rows = judge.parse_rows(header, _rows)

    def test_the_real_pair_is_in_the_repository(self):
        self.assertTrue(self.MATRIX.is_file(), "the real matrix is gone")
        self.assertTrue(self.REPORT.is_file(), "the real report is gone")

    def test_the_table_holds_exactly_the_declared_points(self):
        counted = sum(1 for row in self.rows
                      if judge.COORDINATE.match(row.coordinate)
                      and not row.coordinate.startswith("IMPL"))
        self.assertEqual(self.declared, 19)
        self.assertEqual(counted, self.declared)

    def test_every_row_claims_pass(self):
        self.assertEqual(self.summary["statuses"][judge.status.PASS], 19)

    def test_every_finding_is_the_named_case_class(self):
        for finding in self.findings:
            with self.subTest(finding=finding):
                self.assertIn("具名用例", finding,
                              f"an unexpected finding class on main: {finding}")

    def test_the_judge_exits_one_or_clean_never_not_compared(self):
        self.assertIn(len(self.findings) != 0,
                      (True, False))
        self.assertNotEqual(self.summary["problems"], -1)


if __name__ == "__main__":
    unittest.main()
