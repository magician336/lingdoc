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

They also pin two facts that go stale silently:

* the number of nodes the F01 contract declares is **read from the contract**
  (it was written down as 24, and the contract has 28 nodes now);
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
        """The mock fixture, with one node's own assertion not holding."""
        main, failure = main_path_evidence.build_mock_main_path_fixture()
        main["checks"] = [{
            "step_id": "F01-07",
            "operator": "marked_or_absent",
            "path": "/data/status",
            "status": status,
            "intent": "契约声明的状态",
        }]
        return main, failure

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
        main["checks"] = [
            {"step_id": "F01-07", "operator": "marked_or_absent",
             "path": "/data/status", "status": "failed", "intent": "一"},
            {"step_id": "F01-08", "operator": "equals",
             "path": "/data/asset_id", "status": "failed", "intent": "二"},
        ]
        report = main_path_evidence.build_main_path_report(
            main_report=main, failure_observation=failure)
        self.assertEqual(report["main_path"]["result"], "FAIL")
        self.assertEqual(len(report["main_path"]["failed_checks"]), 2)

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
        self.assertEqual(failed[0]["operator"], "marked_or_absent")

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


class ContractExpectationTests(unittest.TestCase):
    """The expectations come from the contract, and a disagreement is named."""

    def test_the_expectations_are_the_contract_own(self):
        contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
        self.assertEqual(main_path_evidence.EXPECTED_STEP_HTTP,
                         {step["id"]: step["expected_http"] for step in contract["steps"]})
        self.assertEqual(main_path_evidence.REQUIRED_STEP_IDS,
                         tuple(step["id"] for step in contract["steps"]))

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
