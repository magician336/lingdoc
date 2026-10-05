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
        # double: the status the contract declares, but not the refusal it declares.
        report, _ = drive(provider=AnswersThePreflightWrongly(answered_with_the_wrong_code))
        executed = report["executed"]
        self.assertEqual(executed["verdict"], "failed")
        step = executed["steps"][0]
        self.assertEqual((step["expected_http"], step["actual_http"]), (422, 422))
        self.assertEqual(step["why"],
                         ['/error/code：期望等于 "asset_not_authorized"，实际 "source_access_denied"'])
        by_path = {check["path"]: check for check in step["checks"]}
        self.assertEqual(by_path["/error/code"]["status"], "failed")
        self.assertEqual(by_path["/error/details/denied/0/reason"]["status"], "passed")

    def test_a_state_that_does_not_read_back_fails_the_verdict_before_any_step_is_sent(self):
        report, synthetic = drive(provider=provider(project_name="读回来对不上的名字"))
        executed = report["executed"]
        self.assertEqual(executed["verdict"], "failed")
        self.assertEqual(executed["steps"], [])
        self.assertEqual([mismatch["pointer"] for mismatch in executed["state"]["mismatches"]], ["/project/name"])
        self.assertNotIn("why", executed)
        # The read-back probes each binding on its own; what must not have happened is the
        # scenario's own request going out over a state that is not the declared one.
        self.assertEqual(scenario_requests(synthetic), [])

    def test_the_same_input_twice_produces_the_same_report_bytes(self):
        first, _ = drive()
        second, _ = drive()
        self.assertEqual(module.render_report(first), module.render_report(second))
        with tempfile.TemporaryDirectory() as directory:
            paths = []
            for number, report in enumerate((first, second)):
                path = Path(directory) / f"run-{number}.json"
                write_report(path, report)
                paths.append(path)
            self.assertEqual(paths[0].read_bytes(), paths[1].read_bytes())
            # The written bytes and the in-memory rendering are one format, not two.
            self.assertEqual(paths[0].read_text(encoding="utf-8"), module.render_report(first))

    def test_two_runs_whose_identifiers_differ_still_produce_the_same_report_bytes(self):
        # The judgement is about the redaction, not about the double's id generator: a real
        # provider hands out a new project every run, so a double that reuses its counter would
        # let the redaction be deleted outright and the test above still pass. Both halves are
        # asserted here — the identifiers really do differ, and the report really does not.
        first, first_provider = drive(provider=NumberedProvider(1))
        second, second_provider = drive(provider=NumberedProvider(2))
        self.assertNotEqual(set(first_provider.projects), set(second_provider.projects))
        self.assertNotEqual({asset["id"] for assets in first_provider.assets.values() for asset in assets},
                            {asset["id"] for assets in second_provider.assets.values() for asset in assets})
        self.assertEqual(module.render_report(first), module.render_report(second))

    def test_every_contract_scenario_appears_once_and_only_the_driven_one_has_a_verdict(self):
        report, _ = drive()
        document = json.loads(SCENARIOS_PATH.read_text(encoding="utf-8"))
        self.assertEqual([entry["scenario"] for entry in report["scenarios"]],
                         [scenario["id"] for scenario in document["scenarios"]])
        verdicts = {entry["scenario"]: entry["verdict"] for entry in report["scenarios"]}
        self.assertEqual(verdicts["F22"], "passed")
        self.assertEqual({sid for sid, verdict in verdicts.items() if verdict != "not_run"}, {"F22"})
        self.assertEqual(report["summary"], {
            "passed": 1,
            "failed": 0,
            "not_run": len(document["scenarios"]) - 1,
        })

    def test_a_scenario_that_cannot_be_driven_says_which_part_is_missing(self):
        report, _ = drive()
        entries = {entry["scenario"]: entry for entry in report["scenarios"]}
        self.assertFalse(entries["F17"]["executable"])
        self.assertIn("没有步骤", entries["F17"]["reason"])
        self.assertFalse(entries["F06"]["executable"])
        self.assertIn("没有请求定义", entries["F06"]["reason"])
        for scenario_id in ("F03", "F15", "F18", "F20"):
            self.assertTrue(entries[scenario_id]["executable"], scenario_id)
        # F01 declares its 23 steps and every one of them carries a request, but it never says
        # where a run should start — and this driver builds the declared state before it sends
        # anything, so F01 is not something it can drive.
        self.assertFalse(entries["F01"]["executable"])
        self.assertIn("没有声明 starting_state", entries["F01"]["reason"])
        self.assertTrue(entries["F02"]["executable"])
        self.assertIn("可执行", entries["F02"]["reason"])
        for entry in report["scenarios"]:
            self.assertTrue(entry["reason"].strip(), entry["scenario"])
            self.assertTrue(entry["name"].strip(), entry["scenario"])

    def test_the_report_publishes_no_provider_identifier_and_no_timestamp(self):
        text = module.render_report(drive()[0])
        self.assertIsNone(PROVIDER_ID.search(text), text)
        for word in ("timestamp", "generated_at", "started_at", "duration", "elapsed", "synthetic-owner-token"):
            self.assertNotIn(word, text)
        for identifier in ("knowledge-1", "knowledge-2"):
            self.assertNotIn(identifier, text)

    def test_a_scenario_without_a_declared_starting_state_is_refused(self):
        with self.assertRaisesRegex(WorkflowError, "F01 declares no starting_state"):
            module.run_scenario("F01", SCENARIOS_PATH, OPENAPI_PATH, knowledge={}, member={},
                                identities={}, opener=provider().open)

    def test_a_scenario_whose_steps_cannot_be_sent_is_refused_by_name(self):
        document = json.loads(SCENARIOS_PATH.read_text(encoding="utf-8"))
        scenario = next(item for item in document["scenarios"] if item["id"] == "F03")
        scenario["steps"][0].pop("request")
        with tempfile.TemporaryDirectory() as directory:
            states = Path(directory) / "scenarios.json"
            states.write_text(json.dumps(document, ensure_ascii=False), encoding="utf-8")
            with self.assertRaisesRegex(WorkflowError, "F03-01 has no request definition"):
                module.run_scenario("F03", states, OPENAPI_PATH, knowledge={}, member={},
                                    identities={}, opener=provider().open)

    def test_f02_requires_and_reports_its_explicit_white_box_observation(self):
        synthetic = provider()
        with patch.object(module, "collect_white_box_observation",
                          return_value=(F02_WHITE_BOX, "go test observer")) as collect:
            module.run_scenario("F02", SCENARIOS_PATH, OPENAPI_PATH, knowledge={}, member={},
                                identities=dict(IDENTITIES), opener=synthetic.open)
            collect.assert_called_once()
        synthetic = provider()
        with patch.object(module, "collect_white_box_observation",
                          return_value=(F02_WHITE_BOX, "go test observer")):
            report = module.run_scenario("F02", SCENARIOS_PATH, OPENAPI_PATH, knowledge={}, member={},
                                         identities=dict(IDENTITIES), opener=synthetic.open)
        executed = report["executed"]
        self.assertEqual(executed["verdict"], "passed")
        self.assertEqual(executed["steps"][0]["actual_http"], 400)
        observation = executed["white_box_observation"]
        self.assertEqual(observation["classification"], "white_box")
        self.assertEqual(observation["status"], "passed")
        self.assertEqual(observation["observed_counters"], F02_WHITE_BOX)
        self.assertIn("真实 HTTP 结果单独记录", observation["boundary"])

    def test_a_failed_white_box_observer_blocks_f02_before_provider_writes(self):
        synthetic = provider()
        with patch.object(module, "collect_white_box_observation",
                          side_effect=WorkflowError("white-box observer failed with exit code 1")):
            with self.assertRaisesRegex(WorkflowError, "observer failed"):
                module.run_scenario("F02", SCENARIOS_PATH, OPENAPI_PATH, knowledge={}, member={},
                                    identities=dict(IDENTITIES), opener=synthetic.open)
        self.assertEqual(synthetic.calls, [])

    def test_white_box_collector_requires_the_observer_process_and_exact_zero_counters(self):
        observation = module.scenario_entry(
            module.read_json_object(SCENARIOS_PATH, "scenarios document"), "F02", SCENARIOS_PATH.name
        )["white_box_observation"]
        output = "WHITE_BOX_OBSERVATION " + json.dumps(F02_WHITE_BOX)
        result = type("ProcessResult", (), {"returncode": 0, "stdout": output, "stderr": ""})()
        with patch.object(module.subprocess, "run", return_value=result) as run:
            counters, command = module.collect_white_box_observation(observation)
        self.assertEqual(counters, F02_WHITE_BOX)
        self.assertIn("TestStartEmptyAssetScopeHasNoDownstreamEffects", command)
        self.assertEqual(run.call_args.args[0], observation["observer"])
        self.assertEqual(run.call_args.kwargs["cwd"], module.ROOT)

        result.returncode = 1
        with patch.object(module.subprocess, "run", return_value=result):
            with self.assertRaisesRegex(WorkflowError, "exit code 1"):
                module.collect_white_box_observation(observation)

    def test_an_environment_name_no_declaration_uses_is_refused_before_the_first_write(self):
        synthetic = provider()
        with self.assertRaisesRegex(WorkflowError, "k-dmeo"):
            module.run_scenario("F22", SCENARIOS_PATH, OPENAPI_PATH, knowledge={**KNOWLEDGE, "k-dmeo": "x"},
                                member={}, identities=dict(IDENTITIES), opener=synthetic.open)
        self.assertEqual(synthetic.calls, [])

    def test_multiple_scenarios_are_recorded_as_independent_executions(self):
        def report(scenario_id):
            all_ids = ["F08", "F05", "F12"]
            return {
                "report_version": 1,
                "executed": {"scenario": scenario_id, "verdict": "passed", "steps": [], "state": {"mismatches": []}},
                "scenarios": [{"scenario": item, "verdict": "passed" if item == scenario_id else "not_run",
                               "name": item, "reason": "executed" if item == scenario_id else "not selected"}
                              for item in all_ids],
                "summary": {"passed": 1, "failed": 0, "not_run": 2},
                "not_run": ["initial"],
            }

        reports = [report(scenario_id) for scenario_id in ("F08", "F05", "F12")]
        with patch.object(module, "run_scenario", side_effect=reports) as run_one:
            combined = module.run_scenarios(["F08", "F05", "F12"], SCENARIOS_PATH, OPENAPI_PATH,
                                             knowledge={}, member={}, identities={
                                                 "u-owner": "test-owner-token",
                                                 "u-member": "test-member-token",
                                             })

        self.assertEqual([item["scenario"] for item in combined["executed"]], ["F08", "F05", "F12"])
        self.assertEqual({item["scenario"] for item in combined["scenarios"]
                          if item["verdict"] == "passed"}, {"F08", "F05", "F12"})
        self.assertEqual(combined["summary"], {"passed": 3, "failed": 0, "not_run": 0})
        self.assertEqual(run_one.call_count, 3)

    def test_a_scenario_startup_failure_does_not_discard_other_batch_results(self):
        def report(scenario_id):
            all_ids = ["F08", "F05", "F12"]
            return {
                "report_version": 1,
                "executed": {"scenario": scenario_id, "verdict": "passed", "steps": [], "state": {"mismatches": []}},
                "scenarios": [{"scenario": item, "verdict": "passed" if item == scenario_id else "not_run",
                               "name": item, "reason": "executed" if item == scenario_id else "not selected"}
                              for item in all_ids],
                "summary": {"passed": 1, "failed": 0, "not_run": 2},
                "not_run": [],
            }

        with patch.object(module, "run_scenario", side_effect=[report("F08"), WorkflowError("synthetic failure"), report("F12")]):
            combined = module.run_scenarios(
                ["F08", "F05", "F12"], SCENARIOS_PATH, OPENAPI_PATH,
                knowledge={}, member={}, identities={
                    "u-owner": "test-owner-token", "u-member": "test-member-token",
                })

        self.assertEqual([item["scenario"] for item in combined["executed"]], ["F08", "F05", "F12"])
        self.assertEqual([item["verdict"] for item in combined["executed"]], ["passed", "failed", "passed"])
        self.assertEqual(combined["summary"], {"passed": 2, "failed": 1, "not_run": 0})

    def test_duplicate_scenario_ids_are_rejected_before_any_scenario_runs(self):
        with patch.object(module, "run_scenario") as run_one:
            with self.assertRaisesRegex(WorkflowError, "duplicate"):
                module.run_scenarios(["F08", "F08"], SCENARIOS_PATH, OPENAPI_PATH,
                                     knowledge={}, member={})
        run_one.assert_not_called()

    def test_f08_f05_and_f12_capture_values_across_realistic_provider_steps(self):
        synthetic = SyntheticProvider()
        report = module.run_scenarios(
            ["F08", "F05", "F12"], SCENARIOS_PATH, OPENAPI_PATH,
            base_url="http://127.0.0.1:8080/api/v1/lingdoc",
            knowledge={"k-demo": PROVIDER_IDS["k-demo"]},
            member={"u-member": MEMBER_ID},
            identities={"u-owner": "synthetic-owner-token", "u-member": "synthetic-member-token"},
            token="synthetic-owner-token", opener=synthetic.open)

        self.assertEqual([item["scenario"] for item in report["executed"]], ["F08", "F05", "F12"])
        total_scenarios = len(json.loads(SCENARIOS_PATH.read_text(encoding="utf-8"))["scenarios"])
        self.assertEqual(report["summary"], {
            "passed": 3,
            "failed": 0,
            "not_run": total_scenarios - 3,
        })
        f08 = report["executed"][0]
        self.assertEqual([step["actual_http"] for step in f08["steps"]], [200, 202, 202, 409])
        self.assertEqual([check["status"] for step in f08["steps"] for check in step["checks"]],
                         ["passed", "passed", "passed", "passed", "passed"])
        f05 = report["executed"][1]
        self.assertEqual([step["actual_http"] for step in f05["steps"]], [200, 200, 201, 409, 200])
        self.assertEqual(f05["steps"][3]["checks"][0]["expected"], "version_conflict")
        self.assertEqual([check["status"] for check in f05["steps"][4]["checks"]], ["passed", "passed"])
        f12 = report["executed"][2]
        self.assertEqual([step["actual_http"] for step in f12["steps"]], [200, 200, 409, 200])
        self.assertEqual(f12["steps"][2]["checks"][0]["expected"], "version_conflict")
        self.assertEqual([check["status"] for check in f12["steps"][3]["checks"]], ["passed", "passed"])


class ScenarioCommandLineTest(unittest.TestCase):
    def test_the_command_line_writes_the_report_and_exits_on_the_verdict(self):
        report, _ = drive()
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory) / "report.json"
            with patch.object(module, "run_scenarios", return_value=report), \
                    redirect_stdout(io.StringIO()) as stdout:
                status = module.main(["--scenario", "F22", "--knowledge", "k-demo=knowledge-1",
                                      "--knowledge", "k-notready=knowledge-2",
                                      "--identity", "u-owner=synthetic-owner-token",
                                      "--report", str(destination)])
            self.assertEqual(status, 0)
            self.assertEqual(json.loads(destination.read_text(encoding="utf-8")), report)
            self.assertIn("F22 passed", stdout.getvalue())

    def test_a_failed_verdict_exits_non_zero_and_still_leaves_the_report(self):
        report, _ = drive(provider=provider(failures={r".*/lingdoc/projects/[^/]+/retrieval": 200}))
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory) / "report.json"
            with patch.object(module, "run_scenarios", return_value=report), redirect_stdout(io.StringIO()):
                status = module.main(["--scenario", "F22", "--knowledge", "k-demo=knowledge-1",
                                      "--knowledge", "k-notready=knowledge-2",
                                      "--report", str(destination)])
            self.assertEqual(status, 1)
            self.assertEqual(json.loads(destination.read_text(encoding="utf-8"))["executed"]["verdict"], "failed")

    def test_a_broken_environment_binding_is_reported_without_a_traceback(self):
        stderr = io.StringIO()
        with redirect_stderr(stderr):
            status = module.main(["--scenario", "F22", "--knowledge", "k-demo"])
        self.assertEqual(status, 1)
        self.assertIn("--knowledge takes name=id", stderr.getvalue())
        self.assertNotIn("Traceback", stderr.getvalue())


if __name__ == "__main__":
    unittest.main()
