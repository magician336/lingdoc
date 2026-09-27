"""Specification loading and scenario selection; no provider is contacted."""
from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from scripts.lingdoc_mock.run_f01 import (
    SCENARIOS_PATH,
    WORKFLOW_PATH,
    ScenarioRunner,
    WorkflowError,
    load_spec,
    main,
    spec_from_document,
)


class FakeResponse:
    def __init__(self, payload: bytes, status: int = 200) -> None:
        self.payload, self.status = payload, status
        self.headers = {"Content-Type": "application/json"}

    def read(self, limit: int = -1) -> bytes:
        return self.payload[:limit]

    def close(self) -> None:
        pass


OPENAPI = {
    "servers": [{"url": "https://provider.test/api/v1/lingdoc"}],
    "paths": {"/projects/{projectId}": {"get": {"operationId": "getProject"}}},
}


def write_json(directory: str, name: str, document: dict) -> Path:
    path = Path(directory) / name
    path.write_text(json.dumps(document, ensure_ascii=False), encoding="utf-8")
    return path


def one_step_spec(scenario_id: str):
    return spec_from_document({
        "id": scenario_id,
        "steps": [{
            "id": f"{scenario_id}-01", "operation_id": "getProject", "expected_http": 200,
            "request": {"path_params": {"projectId": "p-1"}},
            "assertions": ["Synthetic intent text stays an evidence item."],
        }],
    }, "synthetic specification")


class SpecificationLoadTest(unittest.TestCase):
    def test_step_list_document_is_driven_by_its_own_id_not_a_hardcoded_one(self):
        with tempfile.TemporaryDirectory() as directory:
            path = write_json(directory, "other.json", {
                "id": "F90",
                "steps": [{"operation_id": "getProject", "expected_http": 200,
                           "request": {"path_params": {"projectId": "p-1"}}}],
            })
            spec = load_spec(path)
        self.assertEqual(spec.id, "F90")
        self.assertEqual([step["id"] for step in spec.steps], ["F90-01"])

        requests = []

        def opener(request, timeout):
            requests.append(request)
            return FakeResponse(b'{"data":{}}')

        runner = ScenarioRunner(OPENAPI, spec, opener=opener)
        result = runner.run()
        self.assertEqual(result["completed_steps"], 1)
        self.assertEqual(len(requests), 1)
        self.assertEqual(requests[0].full_url, "https://provider.test/api/v1/lingdoc/projects/p-1")
        report = runner.report("completed")
        self.assertEqual(report["workflow"], "F90")
        self.assertEqual(report["steps"], [{"id": "F90-01", "operation_id": "getProject",
                                            "http_status": 200, "content_type": "application/json"}])
        self.assertEqual(report["manual_assertions"], [])

    def test_repository_f01_workflow_still_loads_as_its_own_specification(self):
        spec = load_spec(WORKFLOW_PATH)
        self.assertEqual(spec.id, "F01")
        self.assertEqual(len(spec.steps), 23)
        self.assertEqual([step["id"] for step in spec.steps], [f"F01-{n:02d}" for n in range(1, 24)])
        self.assertEqual(spec.download_sha256_variable, "expected_export_sha256")

    def test_named_scenario_follows_the_specification_file_the_contract_declares(self):
        spec = load_spec(SCENARIOS_PATH, "F01")
        self.assertEqual(spec.id, "F01")
        self.assertEqual(len(spec.steps), 23)
        self.assertEqual(spec.download_sha256_variable, "expected_export_sha256")

    def test_unknown_scenario_is_refused_by_name(self):
        with self.assertRaisesRegex(WorkflowError, "no scenario F99"):
            load_spec(SCENARIOS_PATH, "F99")

    def test_scenarios_document_without_a_named_scenario_is_refused(self):
        with self.assertRaisesRegex(WorkflowError, "name one with --scenario"):
            load_spec(SCENARIOS_PATH)

    def test_scenario_whose_steps_lack_request_definitions_names_the_step(self):
        # Exercise the refusal with a temporary incomplete copy now that F03 is executable.
        document = json.loads(SCENARIOS_PATH.read_text(encoding="utf-8"))
        scenario = next(item for item in document["scenarios"] if item["id"] == "F03")
        scenario["steps"][0].pop("request")
        with tempfile.TemporaryDirectory() as directory:
            states = Path(directory) / "scenarios.json"
            states.write_text(json.dumps(document, ensure_ascii=False), encoding="utf-8")
            with self.assertRaisesRegex(WorkflowError, "F03-01 has no request definition"):
                load_spec(states, "F03")

    def test_the_one_scenario_that_can_be_sent_carries_its_checks_and_its_actor(self):
        spec = load_spec(SCENARIOS_PATH, "F22")
        self.assertEqual(spec.id, "F22")
        step = spec.steps[0]
        self.assertEqual((step["id"], step["actor"], step["operation_id"]),
                         ("F22-01", "u-owner", "retrieveSources"))
        operators = []
        for check in step["checks"]:
            present = [name for name in ("equals", "length", "keys", "one_of") if name in check]
            self.assertEqual(len(present), 1, check)
            self.assertEqual(set(check), {"path", "intent", present[0]}, check)
            operators.append(present[0])
        # Every operator the runner knows is exercised by the one scenario that can be sent,
        # so the machinery is not carried untested.
        self.assertEqual(sorted(set(operators)), ["equals", "keys", "length", "one_of"])
        self.assertEqual(len(step["checks"]), 6)

    def test_scenario_that_holds_only_prose_is_refused_as_not_executable(self):
        with self.assertRaisesRegex(WorkflowError, "F17 has no executable steps"):
            load_spec(SCENARIOS_PATH, "F17")

    def test_named_scenario_needs_a_scenarios_document(self):
        with self.assertRaisesRegex(WorkflowError, "not a scenarios document"):
            load_spec(WORKFLOW_PATH, "F01")

    def test_singular_assertion_text_survives_as_an_evidence_item(self):
        document = {
            "id": "F91",
            "steps": [{"id": "only", "operation_id": "getProject", "expected_http": 200,
                       "request": {}, "assertion": "整体失败，不得只处理已授权子集。"}],
        }
        spec = spec_from_document(document, "synthetic specification")
        self.assertEqual(spec.steps[0]["assertions"], ["整体失败，不得只处理已授权子集。"])

    def test_broken_step_shapes_are_refused_with_their_own_reason(self):
        cases = [
            ({"id": "F92", "steps": []}, "non-empty list of steps"),
            ({"id": "F92", "steps": [{"operation_id": "getProject", "expected_http": 200}]},
             "synthetic specification step 1 has no request definition"),
            ({"id": "F92", "steps": [{"id": "F92-07", "operation_id": "getProject", "expected_http": 200}]},
             "F92-07 has no request definition"),
            ({"id": "F92", "steps": [{"operation_id": "getProject", "expected_http": "200",
                                      "request": {}}]}, "expected_http must be an HTTP status number"),
            ({"id": "F92", "steps": [{"operation_id": "", "expected_http": 200, "request": {}}]},
             "operation_id must be a non-empty string"),
            ({"steps": [{"operation_id": "getProject", "expected_http": 200, "request": {}}]},
             "id must be a non-empty string"),
        ]
        for document, expected in cases:
            with self.subTest(expected=expected), self.assertRaisesRegex(WorkflowError, expected):
                spec_from_document(document, "synthetic specification")

    def test_the_step_operation_still_has_to_exist_in_openapi(self):
        spec = one_step_spec("F93")
        openapi = {"servers": OPENAPI["servers"], "paths": {"/templates": {"get": {"operationId": "getTemplate"}}}}
        runner = ScenarioRunner(openapi, spec, opener=lambda request, timeout: FakeResponse(b"{}"))
        with self.assertRaisesRegex(WorkflowError, "F93-01: OpenAPI operation not found: getProject"):
            runner.run()

    def test_status_only_download_rejection_does_not_require_file_bytes(self):
        openapi = {
            "servers": OPENAPI["servers"],
            "paths": {"/projects/{projectId}/exports/{exportId}/file": {
                "get": {"operationId": "downloadExport"},
            }},
        }
        spec = spec_from_document({"id": "F94", "steps": [{
            "id": "F94-01", "operation_id": "downloadExport", "expected_http": 404,
            "request": {"path_params": {"projectId": "p-1", "exportId": "e-1"}},
        }]}, "synthetic specification")
        runner = ScenarioRunner(
            openapi, spec,
            opener=lambda request, timeout: FakeResponse(b'{"error":{"code":"not_found"}}', status=404),
        )
        result = runner.run()
        self.assertEqual(result["completed_steps"], 1)
        self.assertEqual(runner.report("completed")["steps"][0]["http_status"], 404)

    def test_non_json_error_preserves_http_status_for_status_only_contracts(self):
        spec = spec_from_document({"id": "F95", "steps": [{
            "id": "F95-01", "operation_id": "getProject", "expected_http": 404,
            "request": {"path_params": {"projectId": "p-1"}},
        }]}, "synthetic specification")
        runner = ScenarioRunner(
            OPENAPI, spec, opener=lambda request, timeout: FakeResponse(b"not-json", status=404),
        )
        result = runner.run()
        self.assertEqual(result["completed_steps"], 1)
        self.assertEqual(runner.report("completed")["steps"][0]["http_status"], 404)


class ScenarioCommandLineTest(unittest.TestCase):
    def test_named_scenario_is_passed_to_the_loader_with_the_scenarios_contract(self):
        spec = one_step_spec("F94")
        runner = ScenarioRunner(OPENAPI, spec, opener=lambda request, timeout: FakeResponse(b'{"data":{}}'))
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory) / "run.json"
            with patch.object(ScenarioRunner, "from_files", return_value=runner) as from_files, \
                    patch("sys.stdout"):
                status = main(["--scenario", "F01", "--report", str(destination)])
        self.assertEqual(status, 0)
        self.assertEqual(from_files.call_args.args[1], SCENARIOS_PATH)
        self.assertEqual(from_files.call_args.kwargs["scenario_id"], "F01")

    def test_step_list_runs_still_default_to_the_f01_workflow(self):
        with patch.object(ScenarioRunner, "from_files", side_effect=WorkflowError("synthetic stop")) as from_files, \
                patch("sys.stderr"):
            self.assertEqual(main([]), 1)
        self.assertEqual(from_files.call_args.args[1], WORKFLOW_PATH)
        self.assertIsNone(from_files.call_args.kwargs["scenario_id"])


if __name__ == "__main__":
    unittest.main()
