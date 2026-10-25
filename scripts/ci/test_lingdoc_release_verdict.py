"""Guard tests for the release verdict layer (the Beta release decision).

What this file guards
---------------------
The Beta release decision is made by two paths, and they must not disagree
about the same run:

* ``scripts/acceptance/beta_evidence.py`` -- the **main path**.  It reads the
  runner's record (the HTTP status of every node) *and* the contract's own
  assertions (``checks``), and turns both into one outcome per step and per
  check;
* ``scripts/lingdoc_mock/main_path_evidence.py`` -- the **second path**.  It
  wraps the same runner for the G6 fixture and decides whether the main path
  may say PASS.

The second path used to read the HTTP status only.  A node whose every
assertion had failed was still reported as a node that had passed, and the
evidence said the HTTP matched the contract.  That is a false PASS in the
release decision, and these tests pin it shut.

They pin the two channels the second path used to leave out as well.  The
contract's nodes carry two kinds of assertion: the machine ones (``checks``)
and the prose ones no machine can read (``manual_assertions``), and the
runner's own verdict on the provider's semantics
(``provider_semantics_status``) belongs to the same story.  The main path
reads all three; the second path read only the first one, so one run had two
verdicts -- and the release decision read the optimistic one.

They also pin two facts that go stale silently:

* the number of nodes the F01 contract declares is **read from the contract**
  (it was written down as 24, and the contract has 31 nodes now);
* the second path's expectations come from the contract **as it is on disk**,
  and a disagreement between the contract and the expectations this module was
  loaded with is named instead of being followed silently.

They run offline: no service, no browser, no network.
"""
from __future__ import annotations

import copy
import json
from pathlib import Path
import sys
import unittest

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "scripts"))
sys.path.insert(0, str(REPO_ROOT / "scripts" / "acceptance"))
sys.path.insert(0, str(REPO_ROOT))

import beta_evidence  # noqa: E402
from scripts.lingdoc_mock import main_path_evidence  # noqa: E402

CONTRACT_PATH = REPO_ROOT / "docs" / "08-本轮实施方案" / "contracts" / "workflow.json"


class MainPathChecksTests(unittest.TestCase):
    """The second path has to read the contract's own assertions."""

    def _report_with_check(self, status: str) -> tuple[dict, dict]:
        """The mock fixture, with one node's own assertion not holding.

        The assertion is one the **contract declares**: a check nobody declared
        is not a failed assertion of this contract, it is a result set that
        does not line up -- and this path refuses to judge that instead of
        calling it a failure.
        """
        main, failure = main_path_evidence.build_mock_main_path_fixture()
        target = next(check for check in main["checks"] if check["step_id"] == "F01-07")
        target["status"] = status
        return main, failure

    def _declared_check(self, step_id: str) -> dict:
        """The contract's own assertion of one node."""
        return next(check for check in main_path_evidence.declared_contract_checks()
                    if check["step_id"] == step_id)

    def test_a_node_whose_assertion_did_not_hold_cannot_pass(self):
        """The failure this path used to miss: HTTP fine, assertion not."""
        main, failure = self._report_with_check("failed")
        report = main_path_evidence.build_main_path_report(
            main_report=main, failure_observation=failure)
        self.assertEqual(report["main_path"]["result"], "FAIL")
        self.assertEqual(report["result"], "FAIL")

    def test_every_assertion_of_a_node_failing_cannot_pass(self):
        """All of them failing is not a weaker failure than one of them."""
        main, failure = main_path_evidence.build_mock_main_path_fixture()
        targets = [check for check in main["checks"]
                   if check["step_id"] in {"F01-07", "F01-08"}]
        for check in targets:
            check["status"] = "failed"
        report = main_path_evidence.build_main_path_report(
            main_report=main, failure_observation=failure)
        self.assertEqual(report["main_path"]["result"], "FAIL")
        self.assertEqual(len(report["main_path"]["failed_checks"]), len(targets))

    def test_the_evidence_says_the_http_was_fine_but_the_assertion_was_not(self):
        main, failure = self._report_with_check("failed")
        report = main_path_evidence.build_main_path_report(
            main_report=main, failure_observation=failure)
        reason = report["main_path"]["reason"]
        self.assertIn("断言不成立", reason)
        self.assertNotIn("HTTP 与契约声明不一致", reason)
        failed = report["main_path"]["failed_checks"]
        self.assertEqual(len(failed), 1)
        self.assertEqual(failed[0]["step_id"], "F01-07")
        declared = self._declared_check("F01-07")
        self.assertEqual(failed[0]["operator"], declared["operator"])
        self.assertEqual(failed[0]["path"], declared["path"])

    def test_the_evidence_says_the_http_did_not_match_the_contract(self):
        """The other failure: the status itself is not the declared one."""
        main, failure = main_path_evidence.build_mock_main_path_fixture()
        main["steps"][0]["actual_http"] = 500
        report = main_path_evidence.build_main_path_report(
            main_report=main, failure_observation=failure)
        self.assertEqual(report["main_path"]["result"], "FAIL")
        self.assertIn("HTTP 与契约声明不一致", report["main_path"]["reason"])
        self.assertEqual(report["main_path"]["failed_checks"], [])

    def test_the_two_paths_agree_about_the_same_failed_assertion(self):
        """Two conclusions about one run must not be one PASS and one FAIL."""
        main, failure = self._report_with_check("failed")
        second_path = main_path_evidence.build_main_path_report(
            main_report=main, failure_observation=failure)
        main_path = beta_evidence.check_outcomes({
            "steps": [{"id": "F01-07", "operation_id": "startGeneration",
                       "http_status": 202}],
            "checks": [{"step_id": "F01-07", "operator": "marked_or_absent",
                        "path": "/data/status", "status": "failed",
                        "intent": "契约声明的状态"}],
        })
        self.assertEqual(second_path["main_path"]["result"], "FAIL")
        self.assertEqual(beta_evidence.overall_result(main_path), "FAIL")

    def test_a_run_without_any_assertion_is_not_a_failure(self):
        """The fix must not turn "no assertion was judged" into a failure."""
        main, failure = main_path_evidence.build_mock_main_path_fixture()
        report = main_path_evidence.build_main_path_report(
            main_report=main, failure_observation=failure)
        self.assertEqual(report["main_path"]["result"], "PASS")
        self.assertEqual(report["main_path"]["failed_checks"], [])

    def test_a_passed_assertion_does_not_fail_the_run(self):
        main, failure = self._report_with_check("passed")
        report = main_path_evidence.build_main_path_report(
            main_report=main, failure_observation=failure)
        self.assertEqual(report["main_path"]["result"], "PASS")
        self.assertEqual(report["main_path"]["failed_checks"], [])

    def test_a_report_cannot_smuggle_a_payload_through_a_failed_check(self):
        """Only the shape of a check is published, never its values."""
        main, failure = self._report_with_check("failed")
        main["checks"][0]["expected"] = "Bearer SECRET_TOKEN_VALUE"
        main["checks"][0]["actual"] = "raw document body"
        report = main_path_evidence.build_main_path_report(
            main_report=main, failure_observation=failure)
        encoded = json.dumps(report, ensure_ascii=False)
        self.assertNotIn("SECRET_TOKEN_VALUE", encoded)
        self.assertNotIn("raw document body", encoded)


class ProseAssertionAndProviderSemanticsTests(unittest.TestCase):
    """The two channels the second path used to leave out.

    The contract's nodes carry two kinds of assertion: the machine ones
    (``checks``) and the prose ones no machine can read
    (``manual_assertions``).  The runner's own verdict on the provider's
    semantics (``provider_semantics_status``) belongs to the same story.  The
    main path reads all three; the second path read only the first one.

    An unjudged prose assertion is not a failure and not a pass either: it is
    NOT RUN.  A provider whose semantics nobody verified has not earned a
    pass either.  The main path says exactly that, so the second path has to
    say it too -- otherwise one run has two verdicts, and the release
    decision reads the optimistic one.
    """

    FAILURE_OBSERVATION = {"failure_case": "duplicate_formal_write",
                           "expected_http": 409, "actual_http": 409,
                           "no_formal_side_effect": True,
                           "readback": {"status": "unchanged"}}

    def _runner_report(self, *, manual_assertions=(),
                       provider_semantics_status="not_verified"):
        """A report in the shape the F01 runner writes.

        Every node the contract declares answered, and every machine assertion
        the contract declares was evaluated and held.  So the only thing that
        can decide the verdict is what this report says about the prose
        assertions and about the provider's semantics.
        """
        contract = beta_evidence.read_json(CONTRACT_PATH) or {}
        steps = [{"id": step["id"], "operation_id": step["operation_id"],
                  "http_status": step["expected_http"]}
                 for step in contract["steps"]]
        _, declared_checks = beta_evidence.contract_expectations(contract)
        checks = [{"step_id": check["step_id"], "operator": check["operator"],
                   "path": check["path"], "status": "passed", "intent": "一条断言"}
                  for check in declared_checks]
        return {
            "workflow": contract["id"],
            "runner_status": "completed",
            "completed_steps": len(steps),
            "total_steps": len(steps),
            "verification_scope": "http_smoke_only",
            "provider_semantics_status": provider_semantics_status,
            "manual_assertions": list(manual_assertions),
            "steps": steps,
            "checks": checks,
        }

    def _second_path(self, report):
        return main_path_evidence.build_main_path_report(
            main_report=report, failure_observation=self.FAILURE_OBSERVATION)

    def _main_path(self, report):
        # The main path always diffs the runner's record against the contract
        # (``beta_evidence.run_f01`` does the same), so the guard does it too.
        contract = beta_evidence.read_json(CONTRACT_PATH) or {}
        outcomes = beta_evidence.check_outcomes(report, contract)
        return beta_evidence.overall_result(
            outcomes, beta_evidence.provider_semantics_status(report))

    def test_the_two_paths_do_not_disagree_about_the_same_run(self):
        """One run, one verdict.  This is what this file was opened for.

        The second path used to say PASS where the main path said NOT RUN, so
        the release decision read the optimistic verdict of one run.
        """
        cases = {
            "a prose assertion nobody judged": self._runner_report(
                manual_assertions=[{"step_id": "F01-06d",
                                    "assertion": "一句机器读不了的话",
                                    "status": "not_run"}],
                provider_semantics_status="verified"),
            "a provider whose semantics nobody verified": self._runner_report(
                provider_semantics_status="not_verified"),
            "everything judged and verified": self._runner_report(
                provider_semantics_status="verified"),
        }
        for label, report in cases.items():
            with self.subTest(case=label):
                second = self._second_path(report)["main_path"]["result"]
                main = self._main_path(report)
                self.assertFalse(second == "PASS" and main == "NOT RUN",
                                 f"the two paths disagree about one run: {second} / {main}")
                self.assertEqual(second, main)

    def test_an_unjudged_prose_assertion_cannot_pass(self):
        """A sentence no machine can judge is not a pass until a human judged it.

        The report even claims ``verified`` -- this path may not take the
        runner's word for it while the report still carries an unjudged prose
        assertion.
        """
        report = self._second_path(self._runner_report(
            manual_assertions=[{"step_id": "F01-06b",
                                "assertion": "一句机器读不了的话",
                                "status": "not_run"}],
            provider_semantics_status="verified"))
        self.assertNotEqual(report["main_path"]["result"], "PASS")
        self.assertNotEqual(report["result"], "PASS")
        self.assertEqual(report["main_path"]["result"], "NOT RUN")
        self.assertEqual(len(report["main_path"]["unjudged_manual_assertions"]), 1)
        self.assertEqual(report["main_path"]["unjudged_manual_assertions"][0]["step_id"],
                         "F01-06b")
        self.assertIn("未判定", report["main_path"]["reason"])

    def test_an_unverified_provider_semantics_cannot_pass(self):
        """The runner's own verdict on the provider's semantics counts here too."""
        report = self._second_path(
            self._runner_report(provider_semantics_status="not_verified"))
        self.assertNotEqual(report["main_path"]["result"], "PASS")
        self.assertNotEqual(report["result"], "PASS")
        self.assertEqual(report["main_path"]["result"], "NOT RUN")
        self.assertEqual(report["main_path"]["provider_semantics_status"], "not_verified")
        self.assertIn("provider_semantics_status", report["main_path"]["reason"])
        self.assertIn("not_verified", report["main_path"]["reason"])

    def test_a_verified_provider_semantics_still_passes(self):
        """The fix may not overreach: a judged and verified run still passes."""
        report = self._second_path(
            self._runner_report(provider_semantics_status="verified"))
        self.assertEqual(report["main_path"]["result"], "PASS")
        self.assertEqual(report["result"], "PASS")
        self.assertEqual(report["main_path"]["unjudged_manual_assertions"], [])
        self.assertEqual(report["main_path"]["provider_semantics_status"], "verified")

    def test_a_report_without_a_provider_semantics_verdict_is_treated_as_unverified(self):
        """An unknown verdict is not a verified one -- the main path reads it so too."""
        report = self._runner_report()
        del report["provider_semantics_status"]
        second = self._second_path(report)
        self.assertEqual(second["main_path"]["provider_semantics_status"], "not_verified")
        self.assertNotEqual(second["main_path"]["result"], "PASS")
        self.assertEqual(second["main_path"]["result"], "NOT RUN")


class ContractExpectationTests(unittest.TestCase):
    """The expectations come from the contract, and a disagreement is named."""

    def test_the_expectations_are_the_contract_own(self):
        contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
        self.assertEqual(main_path_evidence.EXPECTED_STEP_HTTP,
                         {step["id"]: step["expected_http"] for step in contract["steps"]})
        self.assertEqual(main_path_evidence.REQUIRED_STEP_IDS,
                         tuple(step["id"] for step in contract["steps"]))
        self.assertEqual(main_path_evidence.EXPECTED_CHECKS,
                         tuple(main_path_evidence.declared_contract_checks()))

    def test_a_stale_expectation_is_named_instead_of_being_followed(self):
        """The old expectation is not used, and the difference is published."""
        main, failure = main_path_evidence.build_mock_main_path_fixture()
        original = main_path_evidence.EXPECTED_STEP_HTTP
        try:
            main_path_evidence.EXPECTED_STEP_HTTP = dict(original)
            main_path_evidence.EXPECTED_STEP_HTTP["F01-01"] = 599
            report = main_path_evidence.build_main_path_report(
                main_report=main, failure_observation=failure)
        finally:
            main_path_evidence.EXPECTED_STEP_HTTP = original
        mismatch = report["main_path"]["contract_mismatch"]
        self.assertTrue(mismatch, "the disagreement was not published")
        self.assertIn("F01-01", mismatch[0])
        # Not PASS, and not FAIL either: the run matches the contract, so a
        # FAIL here would mean the stale expectation had been used.
        self.assertEqual(report["main_path"]["result"], "BLOCKED")
        self.assertNotEqual(report["result"], "PASS")

    def test_a_contract_that_matches_says_nothing(self):
        main, failure = main_path_evidence.build_mock_main_path_fixture()
        report = main_path_evidence.build_main_path_report(
            main_report=main, failure_observation=failure)
        self.assertEqual(report["main_path"]["contract_mismatch"], [])
        self.assertEqual(report["main_path"]["contract_nodes"],
                         len(main_path_evidence.REQUIRED_STEP_IDS))


class EvidenceNodeCountTests(unittest.TestCase):
    """The node count in the evidence is the contract's, never a written one."""

    def _contract_node_count(self) -> int:
        contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
        return len(contract["steps"])

    def test_the_sequence_label_carries_the_contract_node_count(self):
        count = self._contract_node_count()
        self.assertEqual(beta_evidence.f01_node_count(), count)
        self.assertIn(f"{count} 个节点", beta_evidence.f01_sequence_label())
        self.assertIn(f"{count} 个节点", beta_evidence.f01_scope_label())

    def test_the_blocked_outcome_carries_the_contract_node_count(self):
        outcomes, _report = beta_evidence.run_f01("", None, {})
        self.assertEqual(len(outcomes), 1)
        self.assertIn(f"{self._contract_node_count()} 个节点", outcomes[0].id)

    def test_the_written_down_number_is_gone(self):
        """24 was the number the contract had before it gained four nodes."""
        self.assertNotIn("24 个节点", beta_evidence.f01_sequence_label())
        self.assertNotIn("24 个节点", beta_evidence.f01_scope_label())

    def test_an_unreadable_contract_says_the_count_is_unknown(self):
        original = beta_evidence.WORKFLOW_PATH
        try:
            beta_evidence.WORKFLOW_PATH = REPO_ROOT / "docs" / "08-本轮实施方案" / "contracts" / "no-such-file.json"
            self.assertIsNone(beta_evidence.f01_node_count())
            self.assertIn("未读到", beta_evidence.f01_sequence_label())
            self.assertIn("未读到", beta_evidence.f01_scope_label())
        finally:
            beta_evidence.WORKFLOW_PATH = original

    def test_the_count_follows_the_contract_when_the_contract_changes(self):
        """A contract that gains a node must not leave a stale number behind."""
        count = self._contract_node_count()
        self.assertEqual(beta_evidence.f01_sequence_label(),
                         f"F01 契约序列（{count} 个节点）")
        self.assertNotEqual(count, 24,
                            "the fixture no longer exercises a changed count")


if __name__ == "__main__":
    unittest.main()
