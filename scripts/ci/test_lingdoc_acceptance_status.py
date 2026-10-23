"""Guard tests for the acceptance status vocabulary (#523).

What this file guards
---------------------
Four documents name the acceptance statuses -- the release protocol, the
judgement rules, the gate definitions and the progress source -- and one
module (``scripts/lingdoc_acceptance_status.py``) is the vocabulary they
quote.  When the four drift apart, a reader cannot tell which meaning the
author used, and the two gates (the pull request gate and the Beta release
gate) stop meaning what the documents say.

These tests pin that:

* the vocabulary names ``DEFERRED_MANUAL``, and the four documents name the
  same words the module names;
* a matrix row that is ``DEFERRED_MANUAL`` does **not** block the merge --
  the "NOT RUN blocks" rule must not catch it by accident -- but it is still
  an open acceptance, never a conclusion;
* a ``DEFERRED_MANUAL`` entry without its **release condition** or without
  its **follow-up ticket** is rejected, and the repository contains no such
  entry;
* ``NOT RUN`` keeps its meaning: it still blocks the merge, it is not
  ``DEFERRED_MANUAL``, and it was not rewritten into a pass;
* the Beta release gate is not weakened: a deferred manual acceptance blocks
  the release, exactly like a ``FAIL`` or a ``NOT RUN`` -- **merging the code
  does not pass the acceptance**.

The failure direction is pinned, not just the green direction: the tests
inject an entry that is missing each carrier and check that the guard
catches it.  A rule that only fires on the historical inventory is
decoration.

They run offline: no service, no browser, no network.
"""
from __future__ import annotations

import json
from pathlib import Path
import shutil
import sys
import tempfile
import unittest

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "scripts"))

import lingdoc_acceptance_status as status  # noqa: E402


def read(relative: str) -> str:
    """The text of one repository document."""
    return (REPO_ROOT / relative).read_text(encoding="utf-8")


def table_cell(text: str, prefix: str, index: int = -1) -> str:
    """The cell of the first table row that starts with ``prefix``."""
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped.startswith(prefix):
            continue
        cells = [cell.strip()
                 for cell in stripped.replace("\\|", "\x00").strip("|").split("|")]
        cells = [cell.replace("\x00", "|") for cell in cells]
        return cells[index]
    raise AssertionError(f"no table row starts with {prefix!r}")


def vocabulary_of(cell: str) -> set[str]:
    """The status words a vocabulary cell lists.

    The cell separates the words with slashes; ``N/A`` contains one itself,
    so it is protected before the split.
    """
    protected = cell.replace("N/A", "N\x00A")
    words = set()
    for part in protected.split("/"):
        word = part.strip().replace("`", "").replace("**", "").strip()
        word = word.replace("\x00", "/")
        if word:
            words.add(word)
    return words


def blockquote(lines: list[str], start: int) -> str:
    """The block quote that starts at ``start`` (the lines beginning with ``>``)."""
    collected = []
    for line in lines[start:]:
        if not line.strip().startswith(">"):
            break
        collected.append(line)
    return "\n".join(collected)


class VocabularyTests(unittest.TestCase):
    """The vocabulary exists, and the four places name the same words."""

    def test_the_vocabulary_names_deferred_manual(self):
        self.assertIn(status.DEFERRED_MANUAL, status.ALL_STATUSES)
        self.assertEqual(status.DEFERRED_MANUAL, "DEFERRED_MANUAL")
        self.assertIn(status.DEFERRED_MANUAL, status.GATE_MATRIX_STATUSES)
        self.assertIn(status.DEFERRED_MANUAL, status.JUDGEMENT_STATUSES)

    def test_the_four_definition_sites_name_deferred_manual(self):
        for relative in (status.PROTOCOL, status.JUDGEMENT_RULES,
                         status.GATE_RULES, status.PROGRESS_SOURCE):
            with self.subTest(document=relative):
                self.assertIn(status.DEFERRED_MANUAL, read(relative),
                              f"{relative} does not name {status.DEFERRED_MANUAL}")

    def test_the_gate_matrix_vocabulary_matches_the_gate_rules(self):
        cell = table_cell(read(status.GATE_RULES), "| 每个 User Story")
        self.assertEqual(vocabulary_of(cell), set(status.GATE_MATRIX_STATUSES))

    def test_the_judgement_vocabulary_matches_the_judgement_rules(self):
        cell = table_cell(read(status.JUDGEMENT_RULES), "| **判定状态**", index=1)
        self.assertEqual(vocabulary_of(cell), set(status.JUDGEMENT_STATUSES))

    def test_the_protocol_states_the_beta_release_blocking_set(self):
        """The protocol's gate names the same words the module blocks on."""
        paragraph = ""
        for line in read(status.PROTOCOL).splitlines():
            if "主流程不能含" in line:
                paragraph = line
                break
        self.assertTrue(paragraph, "the protocol no longer states its main-flow gate")
        for word in status.BETA_RELEASE_BLOCKING:
            with self.subTest(word=word):
                self.assertIn(word, paragraph)

    def test_the_progress_source_separates_the_deferral_from_the_other_words(self):
        """``DEFERRED_MANUAL`` is not ``DEFERRED`` and not ``NOT RUN``."""
        lines = read(status.PROGRESS_SOURCE).splitlines()
        start = next((index for index, line in enumerate(lines)
                      if "状态枚举" in line), None)
        self.assertIsNotNone(start, "the progress source has no status vocabulary")
        paragraph = blockquote(lines, start)
        self.assertIn(status.DEFERRED_MANUAL, paragraph)
        self.assertIn(status.NOT_RUN, paragraph)
        self.assertIn(status.BLOCKED, paragraph)

    def test_the_deferral_is_not_the_deferred_work_status(self):
        """``DEFERRED`` (the work is postponed) is a different word."""
        self.assertNotEqual(status.DEFERRED_MANUAL, "DEFERRED")
        self.assertIn("DEFERRED", read(status.PROGRESS_SOURCE))

    def test_the_judgement_rules_say_which_json_field_is_which(self):
        """The rule document has to name the two JSON status fields.

        ``status`` is the run fact and ``matrix_status`` is the matrix
        registration.  A document that leaves the two unexplained is how the
        JSON half of this rule went unenforced in the first place.
        """
        text = read(status.JUDGEMENT_RULES)
        self.assertIn("`status` ＝ **运行事实**", text)
        self.assertIn("`matrix_status` ＝ **矩阵登记状态**", text)


class MergeGateTests(unittest.TestCase):
    """The pull request gate: a carried deferral does not block the merge."""

    def test_deferred_manual_does_not_block_the_merge(self):
        self.assertFalse(status.matrix_blocks_merge([status.DEFERRED_MANUAL]))

    def test_deferred_manual_is_still_an_open_conclusion(self):
        """It is not done; it is carried.  Being carried is not being done."""
        self.assertTrue(status.is_open(status.DEFERRED_MANUAL))
        self.assertEqual(status.open_statuses([status.PASS, status.DEFERRED_MANUAL]),
                         [status.DEFERRED_MANUAL])

    def test_a_matrix_with_a_deferred_manual_row_is_not_blocked(self):
        """The "NOT RUN blocks" rule must not catch a deferral by accident."""
        matrix = [status.PASS, status.DEFERRED_MANUAL, status.PASS]
        self.assertFalse(status.matrix_blocks_merge(matrix))
        self.assertEqual(status.open_statuses(matrix), [status.DEFERRED_MANUAL])

    def test_not_run_still_blocks_the_merge(self):
        self.assertTrue(status.matrix_blocks_merge([status.NOT_RUN]))

    def test_todo_and_blocked_still_block_the_merge(self):
        for word in (status.TODO, status.BLOCKED):
            with self.subTest(word=word):
                self.assertTrue(status.matrix_blocks_merge([word]))

    def test_a_status_with_prose_is_read_as_its_word(self):
        """``NOT RUN（Beta 链账本 0 行）`` is ``NOT RUN`` and still blocks."""
        self.assertTrue(status.matrix_blocks_merge(["NOT RUN（Beta 链账本 0 行）"]))
        self.assertTrue(status.matrix_blocks_merge(["**BLOCKED**"]))


class BetaReleaseGateTests(unittest.TestCase):
    """The Beta release gate: the merge does not pass the acceptance."""

    def test_deferred_manual_blocks_the_beta_release(self):
        self.assertTrue(status.beta_release_blocks([status.DEFERRED_MANUAL]))

    def test_the_release_gate_still_blocks_fail_not_run_and_blocked(self):
        for word in (status.FAIL, status.NOT_RUN, status.BLOCKED):
            with self.subTest(word=word):
                self.assertTrue(status.beta_release_blocks([word]))

    def test_a_clean_main_flow_is_not_blocked(self):
        self.assertFalse(status.beta_release_blocks([status.PASS, status.PASS]))

    def test_a_deferred_manual_row_blocks_the_release_but_not_the_merge(self):
        """The two gates answer two different questions."""
        deferred = [status.DEFERRED_MANUAL]
        self.assertFalse(status.matrix_blocks_merge(deferred))
        self.assertTrue(status.beta_release_blocks(deferred))


class DeferralCarrierTests(unittest.TestCase):
    """A deferral must carry its release condition and its follow-up ticket."""

    def test_a_complete_entry_is_accepted(self):
        problems = status.deferred_manual_problems(
            "解除条件：按 #523 在真实浏览器跑完整链并把证据绑到当前 head",
            "解除条件：按 #523 在真实浏览器跑完整链并把证据绑到当前 head")
        self.assertEqual(problems, [])

    def test_an_entry_without_a_release_condition_is_rejected(self):
        problems = status.deferred_manual_problems("", "#523")
        self.assertIn("缺少解除条件", problems)

    def test_an_entry_without_a_follow_up_ticket_is_rejected(self):
        problems = status.deferred_manual_problems("解除条件：跑完整链", "解除条件：跑完整链")
        self.assertIn("缺少跟进票号（#NNN）", problems)

    def test_an_entry_without_both_carriers_is_rejected_twice(self):
        problems = status.deferred_manual_problems("", "")
        self.assertEqual(len(problems), 2)

    def test_the_repository_has_no_deferral_without_its_carriers(self):
        broken = [entry for entry in status.deferred_manual_entries()
                  if entry["problems"]]
        self.assertEqual(broken, [],
                         f"a DEFERRED_MANUAL entry is missing its release "
                         f"condition or its follow-up ticket: {broken}")

    def test_the_rule_has_a_subject_in_the_repository(self):
        """A rule with no subject proves nothing; there must be one today."""
        entries = status.deferred_manual_entries()
        self.assertTrue(entries,
                        "no DEFERRED_MANUAL entry exists in the repository; "
                        "the judgement rules' own example must carry one")

    def test_a_deferral_is_never_counted_as_a_pass(self):
        """A deferral is an open acceptance, not a conclusion."""
        self.assertNotIn(status.DEFERRED_MANUAL, (status.PASS,))
        self.assertTrue(status.is_open(status.DEFERRED_MANUAL))
        self.assertFalse(status.is_open(status.PASS))


class InjectionTests(unittest.TestCase):
    """The guard has to catch a deferral that is missing a carrier."""

    def setUp(self) -> None:
        self.root = Path(tempfile.mkdtemp(prefix="lingdoc-deferred-manual-"))
        (self.root / "docs").mkdir()

    def tearDown(self) -> None:
        shutil.rmtree(self.root, ignore_errors=True)

    def _matrix(self, row: str) -> None:
        (self.root / "docs" / "matrix.md").write_text(
            "| 定位坐标 | 判定状态 | 备注 |\n"
            "| --- | --- | --- |\n"
            f"{row}\n",
            encoding="utf-8")

    def test_a_row_without_a_ticket_is_caught(self):
        self._matrix("| US-01 | DEFERRED_MANUAL | 解除条件：跑完整链 |")
        entries = status.deferred_manual_entries(self.root)
        self.assertEqual(len(entries), 1)
        self.assertIn("缺少跟进票号（#NNN）", entries[0]["problems"])

    def test_a_row_without_a_release_condition_is_caught(self):
        self._matrix("| US-01 | DEFERRED_MANUAL | 跟进票 #523 |")
        entries = status.deferred_manual_entries(self.root)
        self.assertEqual(len(entries), 1)
        self.assertIn("缺少解除条件", entries[0]["problems"])

    def test_a_complete_row_is_accepted(self):
        self._matrix("| US-01 | DEFERRED_MANUAL | 解除条件：跑完整链（跟进票 #523） |")
        entries = status.deferred_manual_entries(self.root)
        self.assertEqual(len(entries), 1)
        self.assertEqual(entries[0]["problems"], [])

    def test_a_json_entry_without_its_carriers_is_caught(self):
        (self.root / "docs" / "report.json").write_text(
            json.dumps({"not_run": [{"id": "AC-01", "status": "DEFERRED_MANUAL"}]}),
            encoding="utf-8")
        entries = status.deferred_manual_entries(self.root)
        self.assertEqual(len(entries), 1)
        self.assertEqual(len(entries[0]["problems"]), 2)

    def test_a_json_entry_with_its_carriers_is_accepted(self):
        (self.root / "docs" / "report.json").write_text(
            json.dumps({"not_run": [{"id": "AC-01", "status": "DEFERRED_MANUAL",
                                     "unblock": "按 #523 跑完整链"}]}),
            encoding="utf-8")
        entries = status.deferred_manual_entries(self.root)
        self.assertEqual(len(entries), 1)
        self.assertEqual(entries[0]["problems"], [])

    def test_a_row_that_is_not_deferred_manual_is_not_an_entry(self):
        self._matrix("| US-01 | NOT RUN | 解除条件：跑完整链（跟进票 #523） |")
        self.assertEqual(status.deferred_manual_entries(self.root), [])


class MatrixStatusJsonTests(unittest.TestCase):
    """The JSON half must read the shape the real reports use.

    A row of an acceptance report carries **two** status words: the run fact
    (``status``) and the matrix registration (``matrix_status``).  A deferral
    is a *matrix registration*, so it is written in ``matrix_status`` -- and
    a guard that reads only ``status`` misses every entry written that way.
    """

    def setUp(self) -> None:
        self.root = Path(tempfile.mkdtemp(prefix="lingdoc-matrix-status-"))
        (self.root / "docs").mkdir()

    def tearDown(self) -> None:
        shutil.rmtree(self.root, ignore_errors=True)

    def _report(self, document: dict) -> list[dict]:
        (self.root / "docs" / "report.json").write_text(
            json.dumps(document, ensure_ascii=False), encoding="utf-8")
        return status.deferred_manual_entries(self.root)

    def test_the_vocabulary_reads_the_matrix_status_field(self):
        self.assertIn("matrix_status", status.STATUS_FIELDS)
        self.assertIn("status", status.STATUS_FIELDS)

    def test_a_matrix_status_deferral_without_its_carriers_is_caught(self):
        """The real shape: the run did not happen, the matrix carries it."""
        entries = self._report({"not_run": [{
            "id": "AC-04-03 真人浏览器完整链",
            "status": "not_run",
            "matrix_status": "DEFERRED_MANUAL",
            "executed": False,
            "reason": "本轮未在真实浏览器执行该链",
        }]})
        self.assertEqual(len(entries), 1, "the matrix_status deferral was not found")
        self.assertIn("缺少解除条件", entries[0]["problems"])
        self.assertIn("缺少跟进票号（#NNN）", entries[0]["problems"])

    def test_a_matrix_status_deferral_with_its_carriers_is_accepted(self):
        entries = self._report({"not_run": [{
            "id": "AC-04-03 真人浏览器完整链",
            "status": "not_run",
            "matrix_status": "DEFERRED_MANUAL",
            "ticket": "#523",
            "unblock": "解除条件：按 #523 的验收步骤在真实浏览器跑完该链（跟进票 #523）",
        }]})
        self.assertEqual(len(entries), 1)
        self.assertEqual(entries[0]["problems"], [])

    def test_a_follow_up_issue_field_carries_the_ticket(self):
        entries = self._report({"not_run": [{
            "id": "AC-04-03",
            "status": "not_run",
            "matrix_status": "DEFERRED_MANUAL",
            "follow_up_issue": "#523",
            "unblock_condition": "#523 关闭且证据满足",
        }]})
        self.assertEqual(len(entries), 1)
        self.assertEqual(entries[0]["problems"], [])

    def test_a_document_level_follow_up_issue_carries_the_ticket(self):
        """A report may name the ticket once at the top and leave it off the rows."""
        entries = self._report({
            "follow_up_issue": "#246",
            "not_run": [{
                "id": "AC-04-03",
                "status": "not_run",
                "matrix_status": "DEFERRED_MANUAL",
                "unblock": "解除条件：按 #246 的步骤补齐证据",
            }],
        })
        self.assertEqual(len(entries), 1)
        self.assertEqual(entries[0]["problems"], [])

    def test_a_run_fact_alone_is_not_a_deferral(self):
        """``status: not_run`` is the run fact; it carries no matrix registration."""
        entries = self._report({"not_run": [{
            "id": "AC-04-03",
            "status": "not_run",
            "reason": "本轮未在真实浏览器执行该链",
        }]})
        self.assertEqual(entries, [])

    def test_a_matrix_status_deferral_is_found_next_to_other_rows(self):
        entries = self._report({"not_run": [
            {"id": "AC-01", "status": "not_run", "matrix_status": "NOT RUN"},
            {"id": "AC-02", "status": "not_run", "matrix_status": "DEFERRED_MANUAL",
             "ticket": "#523", "unblock": "解除条件：按 #523 跑完整链"},
            {"id": "AC-03", "status": "passed", "matrix_status": "PASS"},
        ]})
        self.assertEqual(len(entries), 1)
        self.assertEqual(entries[0]["problems"], [])
        self.assertTrue(entries[0]["where"].endswith("not_run[1]"),
                        f"the wrong row was reported: {entries[0]['where']}")

    def test_an_empty_row_ticket_is_not_papered_over_by_the_document_one(self):
        """A row that *has* a ticket field and left it empty lost its ticket.

        The document-level ticket covers rows that name no ticket at all; it
        must not hide the row that says ``"ticket": ""``.
        """
        entries = self._report({
            "follow_up_issue": "#246",
            "not_run": [{
                "id": "AC-04-03",
                "status": "not_run",
                "matrix_status": "DEFERRED_MANUAL",
                "ticket": "",
                "unblock": "解除条件：跑完整链",
            }],
        })
        self.assertEqual(len(entries), 1)
        self.assertIn("缺少跟进票号（#NNN）", entries[0]["problems"])

    def test_an_empty_follow_up_issue_is_not_papered_over_by_the_document_one(self):
        """Same rule for the other ticket field: ``follow_up_issue: ""`` is an
        emptied field, not an absent one."""
        entries = self._report({
            "follow_up_issue": "#246",
            "not_run": [{
                "id": "AC-04-03",
                "status": "not_run",
                "matrix_status": "DEFERRED_MANUAL",
                "follow_up_issue": "",
                "unblock": "解除条件：跑完整链",
            }],
        })
        self.assertEqual(len(entries), 1)
        self.assertIn("缺少跟进票号（#NNN）", entries[0]["problems"])

    def test_a_missing_ticket_is_caught_even_without_a_document_ticket(self):
        """The negative case the guard has to catch, with nothing to fall back on."""
        entries = self._report({"not_run": [{
            "id": "AC-04-03",
            "status": "not_run",
            "matrix_status": "DEFERRED_MANUAL",
            "unblock": "解除条件：跑完整链",
        }]})
        self.assertEqual(len(entries), 1)
        self.assertIn("缺少跟进票号（#NNN）", entries[0]["problems"])

    def test_a_missing_release_condition_is_caught(self):
        """The other carrier: the ticket alone does not make an entry valid."""
        entries = self._report({"not_run": [{
            "id": "AC-04-03",
            "status": "not_run",
            "matrix_status": "DEFERRED_MANUAL",
            "ticket": "#523",
            "reason": "本轮未在真实浏览器执行该链",
        }]})
        self.assertEqual(len(entries), 1)
        self.assertIn("缺少解除条件", entries[0]["problems"])

    def test_the_entry_names_the_acceptance_point(self):
        """The point is what a report and a matrix row are counted by."""
        entries = self._report({"not_run": [{
            "id": "AC-04-03 真人浏览器完整链（模板目录 → 导出）",
            "status": "not_run",
            "matrix_status": "DEFERRED_MANUAL",
            "ticket": "#523",
            "unblock": "解除条件：按 #523 跑完整链",
        }]})
        self.assertEqual(entries[0]["point"], "AC-04-03")

    def test_one_file_may_hold_pass_rows_and_carried_rows_side_by_side(self):
        """The shape that drifts easiest, pinned.

        One report holds both kinds of row: a row that passed
        (``status: "pass"``) and a row that is carried (``status: "not_run"``
        ＋ ``matrix_status: "DEFERRED_MANUAL"``).  The carried row may sit in
        ``passed[]`` or in ``not_run[]`` -- the run fact and the matrix
        registration are two different things, and the guard has to read the
        matrix registration whichever list the row sits in.
        """
        entries = self._report({
            "passed": [
                {"id": "AC-04-01", "status": "pass", "evidence": "…"},
                {"id": "AC-04-02", "status": "not_run",
                 "matrix_status": "DEFERRED_MANUAL", "ticket": "#524",
                 "unblock": "解除条件：按 #524 在真实 PostgreSQL 上跑通（跟进票 #524）"},
            ],
            "not_run": [
                {"id": "AC-04-05", "status": "not_run",
                 "matrix_status": "DEFERRED_MANUAL", "ticket": "#524",
                 "unblock": "解除条件：按 #524 的步骤补齐证据（跟进票 #524）"},
            ],
        })
        self.assertEqual(len(entries), 2,
                         f"the carried rows were not found: {entries}")
        for entry in entries:
            self.assertEqual(entry["problems"], [])
        self.assertEqual(sorted(entry["point"] for entry in entries),
                         ["AC-04-02", "AC-04-05"])
        places = sorted(entry["where"].rsplit(":", 1)[-1] for entry in entries)
        self.assertEqual(places, ["not_run[0]", "passed[1]"])


class RealReportTests(unittest.TestCase):
    """The real report on ``main`` is the fixture -- no synthesis needed.

    ``docs/08-本轮实施方案/T139-验证报告.json`` is in the repository: six
    acceptance points are carried, one of them in the matrix and in the
    report.  The guard has to recognise every one of them.
    """

    REPORT = REPO_ROOT / "docs" / "08-本轮实施方案" / "T139-验证报告.json"

    def setUp(self) -> None:
        self.entries = status.deferred_manual_entries(REPO_ROOT)
        self.json_entries = [entry for entry in self.entries
                             if entry["shape"] == "json-entry"]

    def test_the_real_report_is_in_the_repository(self):
        self.assertTrue(self.REPORT.is_file(),
                        "the real report is not on main; the fixture is gone")

    def test_the_real_report_is_recognised(self):
        """Six carried acceptance points, wherever they sit in the file.

        The report is being reorganised: rows move between ``passed[]`` and
        ``not_run[]`` as the run facts change.  The guard has to follow the
        **points**, not the indices -- so this test pins the points and the
        count, never the position of a row.
        """
        self.assertEqual(len(self.json_entries), 6,
                         f"the real report's deferrals were not found: {self.json_entries}")
        for entry in self.json_entries:
            with self.subTest(where=entry["where"]):
                self.assertRegex(entry["where"], r":(passed|not_run)\[\d+\]$",
                                 "a deferral must live in one of the report's lists")

    def test_the_real_report_mixes_pass_rows_and_deferrals(self):
        """The shape that drifts easiest: one file, one list, both kinds of row.

        A row that passed sits next to a row that is carried, in the **same**
        list.  The guard must pick the carried one out of the mixture and
        leave the passed rows alone.
        """
        document = json.loads(self.REPORT.read_text(encoding="utf-8"))
        carried = [row for row in document.get("passed", [])
                   if row.get("matrix_status") == status.DEFERRED_MANUAL]
        passed = [row for row in document.get("passed", [])
                  if row.get("status") == "pass"]
        self.assertTrue(carried,
                        "the report has no carried row beside its passed rows")
        self.assertTrue(passed,
                        "the report has no passed row beside its carried ones")
        # The guard picks the carried row out of the mixture and leaves the
        # passed rows alone: its entries in this list are exactly the carried
        # rows, and each of them is complete.
        entries_in_passed = [entry for entry in self.json_entries
                             if ":passed[" in entry["where"]]
        self.assertEqual(len(entries_in_passed), len(carried))
        for entry in entries_in_passed:
            with self.subTest(where=entry["where"]):
                self.assertEqual(entry["problems"], [])

    def test_every_deferral_in_the_real_report_carries_both_carriers(self):
        for entry in self.json_entries:
            with self.subTest(where=entry["where"]):
                self.assertEqual(entry["problems"], [],
                                 f"{entry['where']} lost its carriers")
                self.assertRegex(entry["ticket"], r"#\d+")
                self.assertIn("解除条件", entry["unblock"])

    def test_the_real_report_names_its_points(self):
        points = sorted(entry["point"] for entry in self.json_entries)
        self.assertEqual(points, ["AC-04-01", "AC-04-02", "AC-04-03",
                                   "AC-04-04", "AC-04-05", "TD-04"])

    def test_the_points_are_counted_once_although_they_appear_twice(self):
        """A point shows up as a matrix row and as a report object."""
        points = status.deferred_manual_points(REPO_ROOT)
        self.assertEqual(len(points), len(set(points)))
        for point in ("AC-04-01", "AC-04-03", "TD-04"):
            self.assertIn(point, points)
        self.assertLess(len(points), len(self.entries),
                        "the records and the points are the same number; "
                        "the dedup does nothing")


if __name__ == "__main__":
    unittest.main()
