"""Guard tests: a check nobody evaluated is not a check that passed.

What this file guards
---------------------
The Beta release decision is made by two paths, and they must not disagree
about the same run:

* ``scripts/acceptance/beta_evidence.py`` -- the **main path**.  It diffs the
  runner's record against the contract's own assertions (``checks``) and
  turns the difference into one outcome per node and per assertion;
* ``scripts/lingdoc_mock/main_path_evidence.py`` -- the **second path**.  It
  wraps the same runner for the G6 fixture and decides whether the main path
  may say PASS.  The G6 report consumes it (``scripts/lingdoc_mock/run_record.py``,
  gate ``G6-01``).

The second path used to read only the assertions the run **did** produce and
say nothing about the ones it did not.  The contract declares 58 machine
assertions; a run that evaluated 15 of them and held every one of those 15
was reported by the second path as a run whose assertions all held -- while
the main path counted the very same run as NOT RUN, with one NOT RUN outcome
per unevaluated assertion.  The release decision read the optimistic verdict
of one run.

These tests pin that shut:

* a required check that the run never evaluated is NOT RUN, never PASS;
* the difference is published, so a reader can see *which* assertions nobody
  judged;
* an explicit failure stays FAIL, and it stays FAIL before the NOT RUN tier;
* a result set that does not line up with the contract -- a check nobody
  declared, the same check twice, a check of a kind nobody knows -- is
  BLOCKED rather than silently ignored;
* the published evidence may not contradict itself: ``main_path.result`` and
  the report's own ``result`` may not disagree about one run.

They run offline: no service, no browser, no network.
"""
from __future__ import annotations

import copy
import json
from pathlib import Path
import sys
import tempfile
import unittest

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "scripts"))
sys.path.insert(0, str(REPO_ROOT / "scripts" / "acceptance"))
sys.path.insert(0, str(REPO_ROOT))

import beta_evidence  # noqa: E402
from scripts.lingdoc_mock import main_path_evidence  # noqa: E402

CONTRACT_PATH = REPO_ROOT / "docs" / "08-本轮实施方案" / "contracts" / "workflow.json"

#: A failure observation in the shape the second path accepts: the failure
#: side is observed, so the verdict is decided by the main path alone.
FAILURE_OBSERVATION = {"failure_case": "duplicate_formal_write",
                       "expected_http": 409, "actual_http": 409,
                       "no_formal_side_effect": True,
                       "readback": {"status": "unchanged"}}


def _contract() -> dict:
    return beta_evidence.read_json(CONTRACT_PATH) or {}


def _declared_checks() -> list[dict[str, str]]:
    """The contract's own assertions, as the second path reads them."""
    return main_path_evidence.declared_contract_checks()


class RunnerShapedReport:
    """A report in the shape the F01 runner writes, built from the contract.

    The runner's record carries the node ids, the HTTP status every node
    answered with, and one entry per machine assertion it evaluated.  A report
    of this shape is what both decision paths read, so the guard can hand the
    very same report to both of them.
    """

    @staticmethod
    def build(*, checks: list[dict] | None = None,
              manual_assertions: list[dict] | None = None,
              provider_semantics_status: str = "verified",
              steps: list[dict] | None = None) -> dict:
        contract = _contract()
        node_steps = steps if steps is not None else [
            {"id": step["id"], "operation_id": step["operation_id"],
             "http_status": step["expected_http"]}
            for step in contract["steps"]]
        if checks is None:
            checks = [{"step_id": check["step_id"], "operator": check["operator"],
                       "path": check["path"], "status": "passed"}
                      for check in _declared_checks()]
        return {
            "workflow": contract["id"],
            "runner_status": "completed",
            "completed_steps": len(node_steps),
            "total_steps": len(node_steps),
            "verification_scope": "http_smoke_only",
            "provider_semantics_status": provider_semantics_status,
            "manual_assertions": list(manual_assertions or []),
            "steps": node_steps,
            "checks": copy.deepcopy(checks),
        }


class UnevaluatedChecksTests(unittest.TestCase):
    """The difference between what the contract asks and what the run judged."""

    def _second_path(self, report: dict) -> dict:
        return main_path_evidence.build_main_path_report(
            main_report=report, failure_observation=FAILURE_OBSERVATION)

    def _main_path(self, report: dict) -> str:
        # The main path always diffs the runner's record against the contract
        # (``beta_evidence.run_f01`` does the same), so the guard does it too.
        outcomes = beta_evidence.check_outcomes(report, _contract())
        return beta_evidence.overall_result(
            outcomes, beta_evidence.provider_semantics_status(report))

    # -- 1. every check evaluated and held ---------------------------------

    def test_every_contract_check_evaluated_and_held_passes(self):
        """The fix may not overreach: a fully judged run still passes."""
        report = RunnerShapedReport.build()
        second = self._second_path(report)
        self.assertEqual(second["main_path"]["result"], "PASS")
        self.assertEqual(second["result"], "PASS")
        self.assertEqual(second["main_path"]["unevaluated_checks"], [])
        self.assertEqual(second["main_path"]["check_set_problems"], [])
        self.assertEqual(self._main_path(report), "PASS")

    def test_the_required_checks_are_the_contract_own(self):
        """Both paths diff against the same set, read from the contract."""
        _, declared = beta_evidence.contract_expectations(_contract())
        self.assertEqual(
            [(check["step_id"], check["operator"], check["path"])
             for check in _declared_checks()],
            [(check["step_id"], check["operator"], check["path"])
             for check in declared])

    # -- 2. one check failed ------------------------------------------------

    def test_one_failed_contract_check_fails_the_run(self):
        """An assertion that did not hold is a failure, and it stays one."""
        checks = [{"step_id": check["step_id"], "operator": check["operator"],
                   "path": check["path"], "status": "passed"}
                  for check in _declared_checks()]
        checks[0]["status"] = "failed"
        report = RunnerShapedReport.build(checks=checks)
        second = self._second_path(report)
        self.assertEqual(second["main_path"]["result"], "FAIL")
        self.assertEqual(second["result"], "FAIL")
        self.assertEqual(len(second["main_path"]["failed_checks"]), 1)
        self.assertEqual(self._main_path(report), "FAIL")

    def test_a_failed_check_outranks_the_unevaluated_ones(self):
        """FAIL sits above NOT RUN: an explicit failure is still a failure."""
        checks = [{"step_id": check["step_id"], "operator": check["operator"],
                   "path": check["path"], "status": "passed"}
                  for check in _declared_checks()][:-1]
        checks[0]["status"] = "failed"
        report = RunnerShapedReport.build(checks=checks)
        second = self._second_path(report)
        self.assertEqual(second["main_path"]["result"], "FAIL")

    # -- 3. one required check never ran ------------------------------------

    def test_a_required_check_that_never_ran_cannot_pass(self):
        """The gap this file was opened for: one assertion nobody evaluated."""
        checks = [{"step_id": check["step_id"], "operator": check["operator"],
                   "path": check["path"], "status": "passed"}
                  for check in _declared_checks()]
        missing = checks.pop(0)
        report = RunnerShapedReport.build(checks=checks)
        second = self._second_path(report)
        self.assertNotEqual(second["main_path"]["result"], "PASS")
        self.assertNotEqual(second["result"], "PASS")
        self.assertEqual(second["main_path"]["result"], "NOT RUN")
        self.assertEqual(len(second["main_path"]["unevaluated_checks"]), 1)
        self.assertEqual(second["main_path"]["unevaluated_checks"][0]["step_id"],
                         missing["step_id"])
        self.assertEqual(second["main_path"]["unevaluated_checks"][0]["operator"],
                         missing["operator"])
        self.assertEqual(self._main_path(report), "NOT RUN")

    def test_a_run_that_evaluated_no_check_at_all_cannot_pass(self):
        """No assertion judged is not a run whose assertions all held."""
        report = RunnerShapedReport.build(checks=[])
        second = self._second_path(report)
        self.assertNotEqual(second["main_path"]["result"], "PASS")
        self.assertEqual(second["main_path"]["result"], "NOT RUN")
        self.assertEqual(len(second["main_path"]["unevaluated_checks"]),
                         len(_declared_checks()))

    # -- 4. part of the checks held, the rest never ran --------------------

    def test_a_partially_evaluated_run_cannot_pass_and_names_the_missing_checks(self):
        """The difference is published, so a reader can see what is missing."""
        checks = [{"step_id": check["step_id"], "operator": check["operator"],
                   "path": check["path"], "status": "passed"}
                  for check in _declared_checks()]
        evaluated = checks[:15]
        report = RunnerShapedReport.build(checks=evaluated)
        second = self._second_path(report)
        self.assertNotEqual(second["main_path"]["result"], "PASS")
        self.assertNotEqual(second["result"], "PASS")
        self.assertEqual(second["main_path"]["result"], "NOT RUN")
        unevaluated = second["main_path"]["unevaluated_checks"]
        self.assertEqual(len(unevaluated), len(_declared_checks()) - 15)
        missing = {(check["step_id"], check["operator"]) for check in checks[15:]}
        self.assertEqual({(check["step_id"], check["operator"]) for check in unevaluated},
                         missing)
        # The reason names them too -- a verdict nobody can read is not evidence.
        for step_id, operator in sorted(missing)[:3]:
            self.assertIn(f"{step_id} {operator}", second["main_path"]["reason"])
        self.assertEqual(self._main_path(report), "NOT RUN")

    # -- 5. a prose assertion nobody judged --------------------------------

    def test_an_unjudged_prose_assertion_cannot_pass(self):
        """A sentence no machine can judge is not a pass until a human judged it."""
        report = RunnerShapedReport.build(
            manual_assertions=[{"step_id": "F01-06b",
                                "assertion": "一句机器读不了的话",
                                "status": "not_run"}])
        second = self._second_path(report)
        self.assertNotEqual(second["main_path"]["result"], "PASS")
        self.assertNotEqual(second["result"], "PASS")
        self.assertEqual(second["main_path"]["result"], "NOT RUN")
        self.assertEqual(len(second["main_path"]["unjudged_manual_assertions"]), 1)

    # -- 6. the provider's semantics were not verified ----------------------

    def test_an_unverified_provider_cannot_pass(self):
        """The runner's own verdict on the provider's semantics counts here too."""
        report = RunnerShapedReport.build(provider_semantics_status="not_verified")
        second = self._second_path(report)
        self.assertNotEqual(second["main_path"]["result"], "PASS")
        self.assertNotEqual(second["result"], "PASS")
        self.assertEqual(second["main_path"]["result"], "NOT RUN")
        self.assertEqual(second["main_path"]["provider_semantics_status"], "not_verified")

    # -- 7. the result set does not line up --------------------------------

    def test_a_check_the_contract_does_not_declare_blocks_the_verdict(self):
        """An assertion nobody declared is not a smaller set of assertions."""
        checks = [{"step_id": check["step_id"], "operator": check["operator"],
                   "path": check["path"], "status": "passed"}
                  for check in _declared_checks()]
        checks.append({"step_id": "F01-07", "operator": "equals",
                       "path": "/data/not_declared_by_the_contract", "status": "passed"})
        report = RunnerShapedReport.build(checks=checks)
        second = self._second_path(report)
        self.assertEqual(second["main_path"]["result"], "BLOCKED")
        self.assertNotEqual(second["result"], "PASS")
        self.assertTrue(second["main_path"]["check_set_problems"])

    def test_a_duplicated_check_blocks_the_verdict(self):
        """The same assertion twice is a result set nobody can name."""
        checks = [{"step_id": check["step_id"], "operator": check["operator"],
                   "path": check["path"], "status": "passed"}
                  for check in _declared_checks()]
        checks.append(dict(checks[0]))
        report = RunnerShapedReport.build(checks=checks)
        second = self._second_path(report)
        self.assertEqual(second["main_path"]["result"], "BLOCKED")
        self.assertNotEqual(second["result"], "PASS")
        self.assertTrue(second["main_path"]["check_set_problems"])

    def test_a_check_of_an_unknown_kind_blocks_the_verdict(self):
        """A kind nobody knows states a fact nobody can judge."""
        checks = [{"step_id": check["step_id"], "operator": check["operator"],
                   "path": check["path"], "status": "passed"}
                  for check in _declared_checks()]
        checks.append({"step_id": "F01-07", "operator": "no_such_kind",
                       "path": "/data/status", "status": "passed"})
        report = RunnerShapedReport.build(checks=checks)
        second = self._second_path(report)
        self.assertEqual(second["main_path"]["result"], "BLOCKED")
        self.assertNotEqual(second["result"], "PASS")
        self.assertTrue(second["main_path"]["check_set_problems"])

    def test_a_contract_that_declares_an_unreadable_check_blocks_the_verdict(self):
        """A contract assertion nobody can read blocks the verdict.

        The contract is the expectation; an expectation that cannot be read is
        not a smaller expectation -- it is no expectation at all, and the
        verdict is refused instead of being computed against a set nobody can
        name.
        """
        document = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
        document["steps"][0]["checks"] = [{"path": "/data/status", "intent": "没有声明类型"}]
        with tempfile.TemporaryDirectory() as directory:
            broken = Path(directory) / "workflow.json"
            broken.write_text(json.dumps(document, ensure_ascii=False), encoding="utf-8")
            original = main_path_evidence.WORKFLOW_PATH
            try:
                main_path_evidence.WORKFLOW_PATH = broken
                report = RunnerShapedReport.build()
                second = self._second_path(report)
            finally:
                main_path_evidence.WORKFLOW_PATH = original
        self.assertEqual(second["main_path"]["result"], "BLOCKED")
        self.assertNotEqual(second["result"], "PASS")
        self.assertIn("没有声明一个已知的类型", second["main_path"]["reason"])

    def test_a_check_without_a_readable_result_cannot_pass(self):
        """A record that carries no result is not a result that held."""
        checks = [{"step_id": check["step_id"], "operator": check["operator"],
                   "path": check["path"], "status": "passed"}
                  for check in _declared_checks()]
        del checks[0]["status"]
        report = RunnerShapedReport.build(checks=checks)
        second = self._second_path(report)
        self.assertNotEqual(second["main_path"]["result"], "PASS")
        self.assertNotEqual(second["result"], "PASS")

    # -- 8. the two paths say the same thing --------------------------------

    def test_the_two_paths_agree_about_the_same_run(self):
        """One run, one verdict -- for a failure, a gap and a partial run."""
        failed = [{"step_id": check["step_id"], "operator": check["operator"],
                   "path": check["path"], "status": "passed"}
                  for check in _declared_checks()]
        failed[0]["status"] = "failed"
        missing = [dict(check) for check in failed]
        del missing[0]["status"]
        partial = [{"step_id": check["step_id"], "operator": check["operator"],
                    "path": check["path"], "status": "passed"}
                   for check in _declared_checks()][:-1]
        cases = {
            "a failed assertion": RunnerShapedReport.build(checks=failed),
            "an assertion nobody evaluated": RunnerShapedReport.build(checks=missing),
            "a partially evaluated run": RunnerShapedReport.build(checks=partial),
        }
        for label, report in cases.items():
            with self.subTest(case=label):
                second = self._second_path(report)["main_path"]["result"]
                main = self._main_path(report)
                self.assertEqual(second, main)
                self.assertNotEqual(second, "PASS")

    # -- the published evidence may not contradict itself -------------------

    def test_the_main_path_verdict_is_downgraded_with_the_overall_verdict(self):
        """``main_path.result = "PASS"`` under ``result = "BLOCKED"`` is a lie.

        The coverage gap is a fact about the main path, so the main path's own
        verdict has to be downgraded with the report's.
        """
        main, failure = main_path_evidence.build_mock_main_path_fixture()
        main["steps"] = main["steps"][1:]
        main["completed_steps"] -= 1
        report = main_path_evidence.build_main_path_report(
            main_report=main, failure_observation=failure)
        self.assertEqual(report["result"], "BLOCKED")
        self.assertEqual(report["main_path"]["result"], "BLOCKED")
        self.assertFalse(report["fixture_coverage"]["step_sequence_complete"])

    def test_the_published_evidence_carries_the_difference(self):
        """The unevaluated assertions are part of the evidence, not a secret."""
        checks = [{"step_id": check["step_id"], "operator": check["operator"],
                   "path": check["path"], "status": "passed"}
                  for check in _declared_checks()][:15]
        report = RunnerShapedReport.build(checks=checks)
        second = self._second_path(report)
        self.assertTrue(second["main_path"]["unevaluated_checks"])
        encoded = json.dumps(second, ensure_ascii=False)
        self.assertIn("unevaluated_checks", encoded)
        # Only the shape of an assertion is published, never its values.
        self.assertNotIn("intent", encoded)


if __name__ == "__main__":
    unittest.main()
