"""Guard tests for the two-step shape of the acceptance contract (#501).

What this file guards
---------------------
The contract can now say "this step happens after that step, and it asserts the
consequence".  Three things have to stay true, and each of them fails quietly
when it breaks:

*The shape is read, not assumed.*  A successor must name an **earlier** step,
and the two must be about the same resource -- the coordinates they share must
agree, and a coordinate the successor adds must be one the predecessor
produced.  A step about a different resource would prove nothing about the
operation before it, and the machine would not know.

*A step that follows another is only judged when the other held.*  If the first
operation failed, the second step's expectation describes a world that did not
happen.  Judging it anyway is how a two-step assertion turns true no matter
what: the successor reads the old state and the expectation still holds.  So the
successor is **blocked** and its expectations are **not judged**.

*The report says which outcome this was.*  "Passed because the answer was 2xx"
and "passed because it was refused as the contract expected" are different
results, and a reader of the evidence has to be able to tell them apart.

Where this file lives
---------------------
It is a new file on purpose: the two-step shape belongs to #501 and must not
collide with the guard of #499 (``scripts/ci/test_lingdoc_beta_check_kinds.py``)
or with the existing operator tables.  The CI job ``lingdoc-f01-runner`` runs
``scripts/lingdoc_mock/test_workflow*.py``, so this file is picked up by the
glob without touching another file.
"""
from __future__ import annotations

import json
from pathlib import Path
import unittest

from scripts.lingdoc_mock.run_f01 import (
    OPENAPI_PATH,
    SCENARIOS_PATH,
    ScenarioRunner,
    WorkflowError,
    spec_from_document,
)

REPO_ROOT = Path(__file__).resolve().parents[2]


class _Response:
    """One answer from a provider that never exists."""

    def __init__(self, body: bytes, status: int) -> None:
        self._body = body
        self.status = status
        self.headers = {"Content-Type": "application/json"}

    def read(self, limit: int = 16 * 1024 * 1024) -> bytes:
        return self._body

    def close(self) -> None:
        pass


def _openapi() -> dict:
    return json.loads(OPENAPI_PATH.read_text(encoding="utf-8"))


class TwoStepShapeTests(unittest.TestCase):
    """The shape itself: what the runner accepts and what it refuses."""

    def drive(self, steps: list[dict], answers: list[tuple[bytes, int]],
              stop_on_mismatch: bool = True) -> dict:
        document = {"id": "TWO-STEP", "steps": steps}

        def opener(request, timeout):
            body, status = answers.pop(0)
            return _Response(body, status)

        runner = ScenarioRunner(_openapi(), spec_from_document(document, "guard specification"),
                                base_url="http://127.0.0.1:4010/api/v1/lingdoc",
                                opener=opener, variables={"project_id": "p-1"})
        runner.run(stop_on_mismatch=stop_on_mismatch)
        return runner.report("completed")

    @staticmethod
    def write_step(step_id: str, **extra) -> dict:
        step = {"id": step_id, "operation_id": "createProject", "expected_http": 201,
                "request": {"path_params": {"projectId": "{{project_id}}"}, "headers": {}, "json": {}},
                "capture": {"made": "/data/id"}, "checks": []}
        step.update(extra)
        return step

    @staticmethod
    def read_step(step_id: str, **extra) -> dict:
        step = {"id": step_id, "operation_id": "getProject", "expected_http": 200,
                "request": {"path_params": {"projectId": "{{project_id}}"}, "headers": {}, "json": None},
                "capture": {}, "checks": []}
        step.update(extra)
        return step

    def test_a_successor_must_name_an_earlier_step(self):
        steps = [self.read_step("S-02", after_step="S-01",
                                 checks=[{"path": "/data/id", "non_empty": True, "intent": "后继的断言"}]),
                 self.write_step("S-01")]
        with self.assertRaisesRegex(WorkflowError, "after_step must name an earlier step"):
            self.drive(steps, [(b"{}", 200)])

    def test_a_successor_must_be_about_the_same_resource(self):
        steps = [self.write_step("S-01"),
                 self.read_step("S-02", after_step="S-01",
                                request={"path_params": {"projectId": "{{project_id}}",
                                                        "otherId": "{{someone_else}}"},
                                         "headers": {}, "json": None},
                                checks=[{"path": "/data/id", "non_empty": True, "intent": "后继的断言"}])]
        with self.assertRaisesRegex(WorkflowError, "same resource"):
            self.drive(steps, [(b"{}", 201)])

    def test_a_successor_may_address_what_the_predecessor_produced(self):
        """The export task the first step created is the resource the second one reads."""
        steps = [self.write_step("S-01"),
                 self.read_step("S-02", after_step="S-01",
                                request={"path_params": {"projectId": "{{project_id}}",
                                                        "exportId": "{{made}}"},
                                         "headers": {}, "json": None},
                                checks=[{"path": "/data/id", "non_empty": True, "intent": "后继的断言"}])]
        report = self.drive(steps, [(json.dumps({"data": {"id": "e-1"}}).encode(), 201),
                                    (json.dumps({"data": {"id": "e-1"}}).encode(), 200)])
        self.assertEqual([step["id"] for step in report["steps"]], ["S-01", "S-02"])
        self.assertEqual(report["steps"][1]["after_step_observation"]["after_step"], "S-01")

    def test_a_successor_is_blocked_when_the_step_it_follows_did_not_hold(self):
        """The expectation describes a world that did not happen: it is not judged."""
        steps = [self.write_step("S-01"),
                 self.read_step("S-02", after_step="S-01",
                                checks=[{"path": "/data/id", "non_empty": True, "intent": "后继的断言"}])]
        report = self.drive(steps, [(b"{}", 409)], stop_on_mismatch=False)
        blocked = [step for step in report["steps"] if step["id"] == "S-02"][0]
        self.assertEqual(blocked["node_kind"], "blocked")
        # The assertion of the blocked step was not judged at all: judging it is the
        # bug this rule exists to prevent.
        self.assertNotIn("S-02", [check["step_id"] for check in report.get("checks", [])])
        self.assertIn("precondition_unmet", [failure["kind"] for failure in report["failures"]])

    def test_the_report_tells_a_refusal_from_a_success(self):
        """\"Passed because it was refused\" and \"passed because it was 2xx\" differ."""
        steps = [self.read_step("S-01", expected_http=403,
                                checks=[{"path": "/error/code", "equals": "version_conflict",
                                         "intent": "按预期被拒"}])]
        report = self.drive(steps, [(json.dumps({"error": {"code": "version_conflict"}}).encode(), 403)])
        self.assertEqual(report["steps"][0]["node_kind"], "expected_refusal")
        self.assertEqual(report["steps"][0]["expected_http"], 403)

        plain = [self.read_step("S-01", checks=[{"path": "/data/id", "non_empty": True,
                                                 "intent": "普通成功"}])]
        report = self.drive(plain, [(json.dumps({"data": {"id": "x"}}).encode(), 200)])
        self.assertNotIn("node_kind", report["steps"][0],
                         "a plain 2xx success keeps the shape the records always had")

    def test_the_expected_refusal_turned_into_a_release_must_fail(self):
        """The negative direction: the server allowed it, so the assertion must fail."""
        steps = [self.read_step("S-01", expected_http=403,
                                checks=[{"path": "/error/code", "equals": "version_conflict",
                                         "intent": "按预期被拒"}])]
        report = self.drive(steps, [(json.dumps({"data": {"id": "x"}}).encode(), 200)],
                            stop_on_mismatch=False)
        self.assertEqual([check["status"] for check in report["checks"]], ["failed"])
        self.assertIn("http_status", [failure["kind"] for failure in report["failures"]])


class ContractShapeTests(unittest.TestCase):
    """The contract the repository actually carries must use the shape it declares."""

    def contract(self) -> dict:
        return json.loads(SCENARIOS_PATH.read_text(encoding="utf-8"))

    def test_the_expected_refusal_declares_the_write_that_caused_it(self):
        scenarios = {scenario["id"]: scenario for scenario in self.contract()["scenarios"]}
        f05 = {step.get("id"): step for step in scenarios["F05"]["steps"]}
        self.assertEqual(f05["F05-04"].get("after_step"), "F05-03")

    def test_the_revocation_nodes_say_the_precondition_is_the_environment_s(self):
        scenarios = {scenario["id"]: scenario for scenario in self.contract()["scenarios"]}
        for step in scenarios["F07"]["steps"]:
            declaration = step.get("environment_precondition")
            self.assertIsNotNone(declaration, step.get("operation_id"))
            self.assertTrue(declaration["action"].strip())
            self.assertEqual(declaration["performed_by"], "environment")

    def test_the_export_failure_drill_reads_the_terminal_state_it_declared(self):
        scenarios = {scenario["id"]: scenario for scenario in self.contract()["scenarios"]}
        drill = {step.get("id"): step for step in scenarios["F24"]["steps"]}
        self.assertEqual(drill["F24-03"].get("after_step"), "F24-02")
        self.assertEqual(drill["F24-03"].get("expected_terminal_state"), "failed")


if __name__ == "__main__":
    unittest.main()
