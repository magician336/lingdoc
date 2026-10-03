"""Driving one contract scenario from its declared state to a report; no live provider is used.

The provider double is the same one the loader's tests use: the point here is the
composition — build the declared state, read it back, drive the steps as the actors they
name, decide every declared expectation, and write one report that says what held and
what did not. What the report *means* about the real service is in
docs/08-本轮实施方案/T15-场景到报告.md.
"""
from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from scripts.lingdoc_mock import run_scenario as module
from scripts.lingdoc_mock.run_f01 import OPENAPI_PATH, PROVIDER_ID, SCENARIOS_PATH, WorkflowError, write_report
from scripts.lingdoc_mock.test_workflow_states import MEMBER_ID, PROVIDER_IDS, Response, SyntheticProvider

KNOWLEDGE = {name: resolved for name, resolved in PROVIDER_IDS.items()}
IDENTITIES = {"u-owner": "synthetic-owner-token"}
F02_WHITE_BOX = {
    "input_resolver_calls": 0,
    "repository_find_replay_calls": 0,
    "repository_create_run_calls": 0,
    "enqueue_calls": 0,
    "model_generate_calls": 0,
}
DENIED = {"/error/code", "/error/details/denied", "/error/details/denied/0/asset_id",
          "/error/details/denied/0", "/error/details/denied/0/reason", "/error/retryable"}


def provider(**kwargs):
    # The double keys asset state by the provider's knowledge id, the way the loader's own
    # tests do. The declaration names the asset the other way, and mapping between the two
    # is the environment's job (--knowledge k-notready=<id>), not the double's.
    return SyntheticProvider(asset_states={PROVIDER_IDS["k-notready"]: "pending"}, **kwargs)


class NumberedProvider(SyntheticProvider):
    """A double whose identifiers start somewhere else each run, the way a real provider's do.

    The base double counts from zero, so two runs of it hand out the same project and binding
    ids — which is exactly the shape of environment a redaction bug hides in.
    """

    def __init__(self, run: int):
        super().__init__(asset_states={PROVIDER_IDS["k-notready"]: "pending"})
        self.counter = run * 1000


class AnswersThePreflightWrongly(SyntheticProvider):
    """A provider that answers the scenario's own preflight with something other than the truth.

    The read-back probes one asset at a time, so keying on the request's own shape keeps the
    declared state building exactly as declared while the step under test is answered
    wrongly — which is the only way to reach the failure path without also breaking the
    starting state the scenario is supposed to run from.
    """

    def __init__(self, answer):
        super().__init__(asset_states={PROVIDER_IDS["k-notready"]: "pending"})
        self.answer = answer

    def _retrieve(self, project_id, body):
        response = super()._retrieve(project_id, body)
        return self.answer(response) if len(body["asset_ids"]) > 1 else response


def answered_with_200(response):
    return Response(200, SyntheticProvider._envelope([]))


def answered_with_the_wrong_code(response):
    """The shape the real service has on startGeneration: 422, but a code the contract does not put there."""
    payload = json.loads(response.payload.decode("utf-8"))
    payload["error"]["code"] = "source_access_denied"
    return Response(response.status, payload)


def drive(**kwargs):
    """Run F22 against a synthetic provider and return (report, provider)."""
    synthetic = kwargs.pop("provider", None) or provider()
    report = module.run_scenario(
        "F22", SCENARIOS_PATH, OPENAPI_PATH,
        base_url="http://127.0.0.1:8080/api/v1/lingdoc",
        knowledge=dict(KNOWLEDGE), member={}, identities=dict(IDENTITIES),
        opener=synthetic.open, **kwargs)
    return report, synthetic


def scenario_requests(synthetic):
    """The retrievals the scenario itself sent, told apart from the read-back's one-asset probes."""
    return [call for call in synthetic.calls if call["path"].endswith("/retrieval")
            and len(call["body"]["asset_ids"]) > 1]


class ScenarioReportTest(unittest.TestCase):
    def test_execution_errors_are_sanitized_before_they_reach_reports(self):
        private_id = "f838a8d9-03a1-4951-bd35-1da51ba802d3"
        error = WorkflowError(f"request failed for http://127.0.0.1/projects/{private_id}: connection reset")

        safe = module.safe_execution_error(error)

        self.assertNotIn(private_id, safe)
        self.assertNotIn("http://", safe)
        self.assertIn("HTTP", safe)

    def test_a_scenario_that_holds_is_reported_step_by_step_and_check_by_check(self):
        report, _ = drive()
        executed = report["executed"]
        self.assertEqual(executed["scenario"], "F22")
        self.assertEqual(executed["starting_state"], "S7")
        self.assertEqual(executed["verdict"], "passed")
        self.assertEqual(executed["state"]["mismatches"], [])
        self.assertEqual([step["step_id"] for step in executed["steps"]], ["F22-01"])

        step = executed["steps"][0]
        self.assertEqual(step["operation_id"], "retrieveSources")
        self.assertEqual(step["actor"], "u-owner")
        self.assertEqual((step["expected_http"], step["actual_http"]), (422, 422))
        self.assertEqual(step["verdict"], "passed")
        self.assertNotIn("why", step)
        self.assertEqual({check["path"] for check in step["checks"]}, DENIED)
        self.assertEqual({check["status"] for check in step["checks"]}, {"passed"})

    def test_the_declared_asset_name_is_what_the_report_publishes(self):
        report, synthetic = drive()
        checks = report["executed"]["steps"][0]["checks"]
        named = next(check for check in checks if check["path"] == "/error/details/denied/0/asset_id")
        self.assertEqual(named["actual"], "asset.k-notready")
        self.assertEqual(named["status"], "passed")
        # The refusal must be about the asset the declaration calls unavailable, and the
        # request must have carried both bindings — not just the one that gets refused.
        requests = scenario_requests(synthetic)
        self.assertEqual(len(requests), 1)
        bound = {asset["id"] for assets in synthetic.assets.values() for asset in assets}
        self.assertEqual(len(bound), 2)
        self.assertEqual(set(requests[0]["body"]["asset_ids"]), bound)

    def test_an_expectation_that_does_not_hold_is_reported_with_what_was_expected_and_what_arrived(self):
        report, _ = drive(provider=AnswersThePreflightWrongly(answered_with_200))
        executed = report["executed"]
        self.assertEqual(executed["verdict"], "failed")
        self.assertEqual(executed["state"]["mismatches"], [], "the state must still have built as declared")
        step = executed["steps"][0]
        self.assertEqual(step["verdict"], "failed")
        self.assertEqual((step["expected_http"], step["actual_http"]), (422, 200))
        # Every declared expectation is answered, not just the first: a success envelope
        # holds none of the places the refusal was supposed to hold.
        self.assertEqual(step["why"][0], "期望 HTTP 422，实际 HTTP 200")
        self.assertEqual(len(step["why"]), 1 + len(DENIED))
        self.assertIn('/error/code：期望等于 "asset_not_authorized"，实际 <该位置在响应里不存在>', step["why"])
        self.assertEqual({check["found"] for check in step["checks"]}, {False})

    def test_a_denial_that_uses_another_code_is_reported_beside_the_status_that_did_match(self):
        # The defect T15-04 registered against startGeneration, reproduced against the
        # double: the status the contract declares, but not the refusal it declare