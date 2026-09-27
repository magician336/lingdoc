"""Focused tests for the F01 black-box workflow runner."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path
import unittest

from urllib.request import Request

from scripts.lingdoc_mock.run_f01 import (
    CredentialSafeRedirectHandler,
    ScenarioRunner,
    ScenarioSpec,
    WorkflowError,
    spec_from_document,
    substitute,
)


ROOT = Path(__file__).resolve().parents[2]
LOOPBACK_SERVER = [{"url": "http://127.0.0.1:8080/api/v1/lingdoc"}]
ONE_OPERATION = {"/projects": {"post": {"operationId": "createProject"}}}


class FakeResponse:
    def __init__(self, payload: bytes, status: int = 200, content_type: str = "application/json") -> None:
        self.payload = payload
        self.status = status
        self.headers = {"Content-Type": content_type}

    def read(self, limit: int = -1) -> bytes:
        return self.payload[:limit]

    def close(self) -> None:
        pass


class WorkflowRunnerTest(unittest.TestCase):
    def test_substitution_preserves_typed_capture(self) -> None:
        self.assertEqual(substitute({"n": "{{number}}", "text": "id={{number}}"}, {"number": 12}),
                         {"n": 12, "text": "id=12"})
        with self.assertRaisesRegex(WorkflowError, "not captured yet"):
            substitute("{{missing}}", {})

    def test_repository_f01_resolves_all_steps(self) -> None:
        runner = ScenarioRunner.from_files(
            ROOT / "docs/08-本轮实施方案/contracts/openapi.json",
            ROOT / "docs/08-本轮实施方案/contracts/workflow.json",
        )
        self.assertEqual(runner.spec.id, "F01")
        self.assertEqual(len(runner.spec.steps), 23)
        for step in runner.spec.steps:
            self.assertIn(step["operation_id"], runner.operations, step["id"])

    def test_executes_all_f01_steps_with_synthetic_reference_trace(self) -> None:
        runner = ScenarioRunner.from_files(
            ROOT / "docs/08-本轮实施方案/contracts/openapi.json",
            ROOT / "docs/08-本轮实施方案/contracts/workflow.json",
            sleep=lambda _: None,
        )
        file_bytes = b"synthetic F01 export bytes; not a real DOCX"
        digest = hashlib.sha256(file_bytes).hexdigest()
        requests = []

        def opener(request, timeout):
            step = runner.spec.steps[len(requests)]
            requests.append(request)
            if step["id"] == "F01-23":
                return FakeResponse(file_bytes, 200, step["expected_binary"]["content_type"])
            payload = json.loads(json.dumps(step["reference_response"]))
            if step["id"] == "F01-22":
                payload["data"]["file_sha256"] = digest
            return FakeResponse(json.dumps(payload, ensure_ascii=False).encode("utf-8"), step["expected_http"])

        runner.client.opener = opener
        result = runner.run()
        self.assertEqual(result["completed_steps"], 23)
        self.assertEqual(len(result["steps"]), 23)
        self.assertEqual(len(requests), 23)
        self.assertEqual(requests[1].method, "POST")
        self.assertNotIn("{{", "\n".join(request.full_url for request in requests))

    def test_polls_without_reposting_and_verifies_download_bytes(self) -> None:
        file_bytes = b"test-docx-bytes"
        digest = hashlib.sha256(file_bytes).hexdigest()
        openapi = {
            "servers": [{"url": "http://provider.test/api/v1"}],
            "paths": {
                "/projects/{projectId}/exports": {"post": {"operationId": "startExport"}},
                "/projects/{projectId}/exports/{exportId}": {"get": {"operationId": "getExport"}},
                "/projects/{projectId}/exports/{exportId}/file": {"get": {"operationId": "downloadExport"}},
            },
        }
        workflow = {
            "id": "F01",
            "download_sha256_variable": "expected_sha256",
            "steps": [
                {
                    "id": "start", "operation_id": "startExport", "expected_http": 202,
                    "request": {"path_params": {"projectId": "p1"}, "headers": {"Idempotency-Key": "once"}, "json": {}},
                    "capture": {"export_id": "/data/id"},
                },
                {
                    "id": "poll", "operation_id": "getExport", "expected_http": 200,
                    "request": {"path_params": {"projectId": "p1", "exportId": "{{export_id}}"}, "headers": {}, "json": None},
                    "reference_response": {"data": {"status": "verified"}},
                    "capture": {"expected_sha256": "/data/file_sha256"},
                },
                {
                    "id": "download", "operation_id": "downloadExport", "expected_http": 200,
                    "request": {"path_params": {"projectId": "p1", "exportId": "{{export_id}}"}, "headers": {}, "json": None},
                    "expected_binary": {"content_type": "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
                    "capture": {},
                },
            ],
        }
        responses = [
            FakeResponse(b'{"data":{"id":"e1"}}', 202),
            FakeResponse(b'{"data":{"status":"queued"}}'),
            FakeResponse(json.dumps({"data": {"status": "verified", "file_sha256": digest}}).encode()),
            FakeResponse(file_bytes, 200, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"),
        ]
        requests = []

        def opener(request, timeout):
            requests.append(request)
            return responses.pop(0)

        runner = ScenarioRunner(openapi, spec_from_document(workflow, "synthetic specification"),
                                base_url="https://provider.test/api/v1", token="test-token", opener=opener,
                                sleep=lambda _: None, poll_timeout=0.1)
        result = runner.run()
        self.assertEqual(result["completed_steps"], 3)
        self.assertEqual([step["id"] for step in result["steps"]], ["start", "poll", "download"])
        self.assertEqual([request.method for request in requests], ["POST", "GET", "GET", "GET"])
        self.assertIn("/exports/e1", requests[1].full_url)
        self.assertEqual(requests[0].get_header("Idempotency-key"), "once")
        self.assertEqual(requests[0].get_header("Authorization"), "Bearer test-token")
        self.assertFalse(responses)

    def test_rejects_unexpected_download_content_type(self) -> None:
        runner = ScenarioRunner({"servers": [{"url": "http://provider.test"}], "paths": {"/": {}}},
                                ScenarioSpec(id="F01", steps=()))
        step = {"id": "download", "expected_binary": {"content_type": "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}}
        with self.assertRaisesRegex(WorkflowError, "Content-Type"):
            runner._verify_download(step, b"not a docx", {"Content-Type": "text/plain"})

    def test_refuses_to_send_bearer_token_to_remote_http(self) -> None:
        openapi = {"servers": [{"url": "http://provider.test/api/v1"}], "paths": {}}
        spec = ScenarioSpec(id="F01", steps=())
        with self.assertRaisesRegex(WorkflowError, "non-loopback HTTP"):
            ScenarioRunner(openapi, spec, token="secret")
        ScenarioRunner(openapi, spec, base_url="http://127.0.0.1:8080/api/v1", token="test-token")

    def test_redirect_protects_credentials_and_rejects_downgrades_or_mutations(self) -> None:
        handler = CredentialSafeRedirectHandler()
        request = Request("https://provider.test/api", headers={"Authorization": "Bearer secret"})
        redirected = handler.redirect_request(request, None, 302, "Found", {}, "https://files.test/export.docx")
        self.assertIsNotNone(redirected)
        self.assertNotIn("Authorization", redirected.headers)

        same_origin = handler.redirect_request(request, None, 302, "Found", {}, "https://provider.test/next")
        self.assertEqual(same_origin.headers.get("Authorization"), "Bearer secret")

        with self.assertRaisesRegex(WorkflowError, "HTTPS downgrade"):
            handler.redirect_request(request, None, 302, "Found", {}, "http://provider.test/export.docx")

        post = Request("https://provider.test/api", data=b"{}", headers={"Authorization": "Bearer secret"}, method="POST")
        with self.assertRaisesRegex(WorkflowError, "mutating provider request"):
            handler.redirect_request(post, None, 302, "Found", {}, "https://provider.test/next")


class CheckedStep:
    """One synthetic step definition; the body it is answered with is handed in separately."""

    def __init__(self, status_expected=422, status_answered=None, **extra):
        self.status_expected = status_expected
        self.status_answered = status_expected if status_answered is None else status_answered
        self.extra = extra

    def document(self, step_id="F22-01", operation_id="createProject"):
        return {"id": step_id, "operation_id": operation_id, "expected_http": self.status_expected,
                "request": {"json": {}}, **self.extra}


def driven(steps, responses, **kwargs):
    """Run a synthetic specification whose steps were built by CheckedStep."""
    requests = []

    def opener(request, timeout):
        requests.append(request)
        return responses.pop(0)

    document = {"id": "F22", "steps": [step.document(step_id=f"F22-{number:02d}") for number, step in enumerate(steps, 1)]}
    runner = ScenarioRunner({"servers": LOOPBACK_SERVER, "paths": ONE_OPERATION},
                            spec_from_document(document, "synthetic specification"),
                            base_url=LOOPBACK_SERVER[0]["url"], opener=opener, **kwargs)
    return runner, requests


def answering(step_payloads):
    """One FakeResponse per step, carrying that step's synthetic body and answered status."""
    return [FakeResponse(json.dumps(payload, ensure_ascii=False).encode("utf-8"), status)
            for payload, status in step_payloads]


class EvaluableCheckTest(unittest.TestCase):
    DENIED = {"error": {"code": "asset_not_authorized", "retryable": False,
                        "details": {"denied": [{"asset_id": "provider-asset-id", "reason": "not_ready"}]}}}

    def checks_of(self, payload, checks, status_answered=422, **kwargs):
        step = CheckedStep(status_answered=status_answered, checks=checks)
        runner, requests = driven([step], answering([(payload, status_answered)]), **kwargs)
        runner.run(stop_on_mismatch=False)
        return runner, requests

    def test_every_operator_reads_the_response_rather_than_the_contract_text(self):
        checks = [
            {"path": "/error/code", "equals": "asset_not_authorized", "intent": "拒绝码是契约声明的那个"},
            {"path": "/error/details/denied", "length": 1, "intent": "恰好一条被拒资料"},
            {"path": "/error/details/denied/0", "keys": ["asset_id", "reason"], "intent": "每项恰好两键，不折叠"},
            {"path": "/error/details/denied/0/reason", "one_of": ["not_authorized", "not_ready", "not_found"],
             "intent": "原因落在契约词表里"},
            {"path": "/error/retryable", "equals": False, "intent": "不可重试"},
        ]
        runner, _ = self.checks_of(self.DENIED, checks)
        recorded = runner.report("completed")["checks"]
        self.assertEqual([(c["path"], c["status"]) for c in recorded], [(c["path"], "passed") for c in checks])
        self.assertEqual(recorded[1]["actual"], [{"asset_id": "provider-asset-id", "reason": "not_ready"}])
        self.assertEqual(recorded[2]["actual"], {"asset_id": "provider-asset-id", "reason": "not_ready"})
        self.assertEqual(recorded[3]["actual"], "not_ready")

    def test_a_check_that_does_not_hold_records_what_was_expected_and_what_arrived(self):
        payload = {"error": {"code": "source_access_denied"}}
        runner, _ = self.checks_of(payload, [{"path": "/error/code", "equals": "asset_not_authorized",
                                              "intent": "拒绝码是契约声明的那个"}])
        recorded = runner.report("completed")["checks"][0]
        self.assertEqual(recorded["status"], "failed")
        self.assertEqual(recorded["expected"], "asset_not_authorized")
        self.assertEqual(recorded["actual"], "source_access_denied")
        self.assertTrue(recorded["found"])
        self.assertEqual(recorded["intent"], "拒绝码是契约声明的那个")

    def test_a_missing_pointer_is_a_failure_and_not_a_crash(self):
        runner, _ = self.checks_of({}, [{"path": "/error/details/denied/0/asset_id", "equals": "a",
                                         "intent": "点名被拒的那条资料"}])
        recorded = runner.report("completed")["checks"][0]
        self.assertEqual(recorded["status"], "failed")
        self.assertFalse(recorded["found"])
        self.assertIsNone(recorded["actual"])

    def test_a_missing_pointer_under_a_scalar_is_not_an_index_error(self):
        runner, _ = self.checks_of({"error": "not an object"},
                                   [{"path": "/error/code", "equals": "x", "intent": "读一个不存在的字段"}])
        self.assertEqual(runner.report("completed")["checks"][0]["status"], "failed")

    def test_checks_are_compared_on_the_real_values_and_recorded_under_declared_names(self):
        asset_id = "3f2a1c60-9b7e-4d21-8f0a-51c2b7e40d19"
        payload = {"error": {"code": "asset_not_authorized",
                             "details": {"denied": [{"asset_id": asset_id, "reason": "not_ready"}]}}}
        runner, _ = self.checks_of(
            payload,
            [{"path": "/error/details/denied/0/asset_id", "equals": "{{asset.k-notready}}",
              "intent": "点名被拒的那条资料"}],
            variables={"asset.k-notready": asset_id},
            redactions={asset_id: "asset.k-notready"},
        )
        recorded = runner.report("completed")["checks"][0]
        self.assertEqual(recorded["status"], "passed")
        self.assertEqual(recorded["expected"], "asset.k-notready")
        self.assertEqual(recorded["actual"], "asset.k-notready")
        self.assertNotIn(asset_id, json.dumps(runner.report("completed"), ensure_ascii=False))

    def test_an_unmapped_provider_identifier_is_replaced_rather_than_published(self):
        payload = {"error": {"details": {"denied": [{"asset_id": "8c1d0f42-77aa-4c33-91bb-0e5d3a6f2210"}]}}}
        runner, _ = self.checks_of(payload, [{"path": "/error/details/denied/0/asset_id", "equals": "x",
                                              "intent": "读一个测试没登记名字的标识"}])
        recorded = runner.report("completed")["checks"][0]
        self.assertEqual(recorded["actual"], "<provider-id>")
        self.assertNotIn("8c1d0f42", json.dumps(runner.report("completed"), ensure_ascii=False))

    def test_broken_check_shapes_are_refused_before_any_request(self):
        cases = [
            ([{"path": "/a", "equals": 1, "one_of": [1], "intent": "x"}], "exactly one of"),
            ([{"path": "/a", "intent": "x"}], "exactly one of"),
            ([{"path": "/a", "equals": 1}], "intent"),
            ([{"equals": 1, "intent": "x"}], "path"),
            ([{"path": "a", "equals": 1, "intent": "x"}], "starts with '/"),
            ([{"path": "/a", "length": -1, "intent": "x"}], "length"),
            ([{"path": "/a", "length": True, "intent": "x"}], "length"),
            ([{"path": "/a", "keys": "asset_id", "intent": "x"}], "keys"),
            ([{"path": "/a", "one_of": [], "intent": "x"}], "one_of"),
            ([{"path": "/a", "equals": 1, "intent": ""}], "intent"),
            (["not an object"], "must be an object"),
        ]
        for checks, expected in cases:
            with self.subTest(expected=expected):
                with self.assertRaisesRegex(WorkflowError, expected):
                    spec_from_document({"id": "F92", "steps": [CheckedStep(checks=checks).document()]},
                                       "synthetic specification")


class StepActorTest(unittest.TestCase):
    def two_step_document(self):
        return {"id": "F22", "steps": [
            {**CheckedStep(status_expected=200).document(step_id="F22-01"), "actor": "u-owner"},
            {**CheckedStep(status_expected=200).document(step_id="F22-02"), "actor": "u-member"},
        ]}

    def test_each_step_is_sent_with_the_credential_of_the_actor_it_names(self):
        requests = []

        def opener(request, timeout):
            requests.append(request)
            return FakeResponse(b'{"a":1}')

        runner = ScenarioRunner(
            {"servers": LOOPBACK_SERVER, "paths": ONE_OPERATION},
            spec_from_document(self.two_step_document(), "synthetic specification"),
            base_url=LOOPBACK_SERVER[0]["url"], opener=opener,
            identities={"u-owner": "owner-token", "u-member": "member-token"},
        )
        runner.run()
        self.assertEqual([request.get_header("Authorization") for request in requests],
                         ["Bearer owner-token", "Bearer member-token"])

    def test_an_actor_the_environment_cannot_fill_is_refused_before_the_first_request(self):
        requests = []
        with self.assertRaisesRegex(WorkflowError, "supplies no credential for u-member"):
            ScenarioRunner(
                {"servers": LOOPBACK_SERVER, "paths": ONE_OPERATION},
                spec_from_document(self.two_step_document(), "synthetic specification"),
                base_url=LOOPBACK_SERVER[0]["url"],
                opener=lambda request, timeout: requests.append(request),
                identities={"u-owner": "owner-token"},
            )
        self.assertEqual(requests, [])

    def test_a_credential_no_step_ever_names_is_refused(self):
        with self.assertRaisesRegex(WorkflowError, "never names as an actor"):
            ScenarioRunner(
                {"servers": LOOPBACK_SERVER, "paths": ONE_OPERATION},
                spec_from_document(self.two_step_document(), "synthetic specification"),
                base_url=LOOPBACK_SERVER[0]["url"], opener=lambda request, timeout: None,
                identities={"u-owner": "owner-token", "u-member": "member-token", "u-ghost": "ghost-token"},
            )

    def test_a_specification_that_names_no_actor_takes_no_identity(self):
        with self.assertRaisesRegex(WorkflowError, "never names as an actor"):
            ScenarioRunner(
                {"servers": LOOPBACK_SERVER, "paths": ONE_OPERATION},
                spec_from_document({"id": "F01", "steps": [CheckedStep().document()]}, "synthetic specification"),
                base_url=LOOPBACK_SERVER[0]["url"], opener=lambda request, timeout: None,
                identities={"u-owner": "owner-token"},
            )


class RecordedOutcomeTest(unittest.TestCase):
    def test_a_status_mismatch_can_be_recorded_instead_of_aborting_the_specification(self):
        steps = [CheckedStep(status_expected=422, status_answered=403,
                             checks=[{"path": "/error/code", "equals": "asset_not_authorized", "intent": "拒绝码"}]),
                 CheckedStep(status_expected=200)]
        runner, requests = driven(steps,
                                  answering([({"error": {"code": "source_access_denied"}}, 403),
                                             ({"ok": True}, 200)]))
        runner.run(stop_on_mismatch=False)
        self.assertEqual(len(requests), 2)
        report = runner.report("failed")
        self.assertEqual(report["failures"],
                         [{"step_id": "F22-01", "operation_id": "createProject", "kind": "http_status",
                           "expected": 422, "actual": 403, "intent": "该操作按契约返回声明的状态码"}])
        self.assertEqual([c["status"] for c in report["checks"]], ["failed"])
        self.assertEqual([step["id"] for step in report["steps"]], ["F22-02"])

    def test_an_aborted_run_keeps_no_recorded_outcomes(self):
        steps = [CheckedStep(status_expected=422, status_answered=500)]
        runner, _ = driven(steps, answering([({"a": 1}, 500)]))
        with self.assertRaisesRegex(WorkflowError, "HTTP 500; expected 422"):
            runner.run()
        report = runner.report("failed")
        self.assertNotIn("failures", report)
        self.assertNotIn("checks", report)

    def test_a_specification_without_checks_keeps_the_f01_report_shape(self):
        runner, _ = driven([CheckedStep(status_expected=200)], answering([({"ok": True}, 200)]))
        runner.run()
        report = runner.report("completed")
        self.assertEqual(set(report), {"workflow", "runner_status", "completed_steps", "total_steps", "steps",
                                       "verification_scope", "provider_semantics_status", "manual_assertions"})

    def test_seeded_variables_reach_the_steps_and_a_second_run_reproduces_the_first(self):
        document = {"id": "F22", "steps": [{"id": "F22-01", "operation_id": "createProject", "expected_http": 200,
                                            "request": {"json": {"id": "{{asset.k-demo}}"}},
                                            "capture": {"scratch": "/data/id"}}]}
        seen = []

        def opener(request, timeout):
            seen.append(json.loads(request.data.decode("utf-8")))
            return FakeResponse(b'{"data":{"id":"provider-asset-id"}}')

        runner = ScenarioRunner({"servers": LOOPBACK_SERVER, "paths": ONE_OPERATION},
                                spec_from_document(document, "synthetic specification"),
                                base_url=LOOPBACK_SERVER[0]["url"], opener=opener,
                                variables={"asset.k-demo": "provider-asset-id"})
        first = runner.run()["variables"]
        second = runner.run()["variables"]
        # The seeded names survive; a capture from the previous run does not.
        self.assertEqual(seen, [{"id": "provider-asset-id"}] * 2)
        self.assertEqual(first["scratch"], "provider-asset-id")
        self.assertEqual(second, first)


class DottedVariableTest(unittest.TestCase):
    def test_a_variable_name_may_be_namespaced_the_way_the_contract_spells_it(self):
        variables = {"project.id": "p-1", "asset.k-notready": "asset-1", "chapter.method": "c-1"}
        self.assertEqual(substitute("{{asset.k-notready}}", variables), "asset-1")
        self.assertEqual(substitute({"path_params": {"projectId": "{{project.id}}"}}, variables),
                         {"path_params": {"projectId": "p-1"}})
        self.assertEqual(substitute("{{chapter.method}}/{{project.id}}", variables), "c-1/p-1")
        with self.assertRaisesRegex(WorkflowError, "not captured yet"):
            substitute("{{asset.k-typo}}", variables)


if __name__ == "__main__":
    unittest.main()
