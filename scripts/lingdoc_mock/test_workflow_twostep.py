"""The two-step shape: operate on a resource, then assert what it caused.

``readback_of`` already said "a GET reads back what an earlier write did".  It
could not say "this step happens after that step and asserts its consequence",
because the step it names had to be a write and the step that named it had to
be a GET.  ``after_step`` is that other shape: the two steps are related by
order, they reach the same resource, and the second one may only assert
anything at all when the first one actually held.

These tests drive the contract's own two-step scenario (F24: revoke a
collaborator, then read the same project as that collaborator) against an
offline stand-in for the membership surface, so the assertion is really
evaluated -- and they check the negative direction too: when the revocation
does not take effect, the assertion has to turn red.
"""
from __future__ import annotations

import copy
import hashlib
import json
from pathlib import Path
import unittest
from urllib.parse import urlsplit

from scripts.lingdoc_mock.run_f01 import (
    SCENARIOS_PATH,
    WORKFLOW_PATH,
    ScenarioRunner,
    WorkflowError,
    load_spec,
    spec_from_document,
)


ROOT = Path(__file__).resolve().parents[2]
OPENAPI_PATH = ROOT / "docs/08-本轮实施方案/contracts/openapi.json"
OPENAPI = json.loads(OPENAPI_PATH.read_text(encoding="utf-8"))
SERVER_PATH = urlsplit(OPENAPI["servers"][0]["url"]).path.rstrip("/")
PROJECT_ID = "p-demo"
OWNER_TOKEN = "owner-token"
MEMBER_TOKEN = "member-token"


def published_example(operation_id: str, status: int, name: str) -> dict:
    """The payload the contract itself publishes for one exchange."""
    for item in OPENAPI["paths"].values():
        for operation in item.values():
            if not isinstance(operation, dict) or operation.get("operationId") != operation_id:
                continue
            examples = operation["responses"][str(status)]["content"]["application/json"]["examples"]
            return copy.deepcopy(examples[name]["value"])
    raise AssertionError(f"the contract publishes no example for {operation_id} {status} {name}")


class FakeResponse:
    def __init__(self, payload: bytes, status: int = 200, content_type: str = "application/json") -> None:
        self.payload, self.status = payload, status
        self.headers = {"Content-Type": content_type}

    def read(self, limit: int = -1) -> bytes:
        return self.payload[:limit]

    def close(self) -> None:
        pass


class MembershipDouble:
    """An offline stand-in for the membership surface.

    It answers the calls the two-step scenario makes with the payloads the
    contract publishes, and it simulates the one thing the assertion is about:
    whether the collaborator is still a member of the project.  The service
    answers a caller who is not an active member with 404 (the Go truth:
    ``internal/lingdoc/workspacecore/permission_policy.go`` answers
    ``ErrNotFound`` from ``authorizeCurrentProject``), and so does this.

    ``revocation_applies`` decides whether the revocation really happens.
    Turning it off is the negative control: the assertion has to turn red.
    """

    def __init__(self, revocation_applies: bool = True, revocation_fails: bool = False) -> None:
        self.revocation_applies = revocation_applies
        self.revocation_fails = revocation_fails
        self.revoked = False
        self.project_version = published_example("getProject", 200, "success")["data"]["project_version"]
        self.requests: list[dict] = []

    def open(self, request, timeout):
        path = urlsplit(request.full_url).path[len(SERVER_PATH):]
        token = request.get_header("Authorization") or ""
        actor = {f"Bearer {OWNER_TOKEN}": "u-owner", f"Bearer {MEMBER_TOKEN}": "u-member"}.get(token, "unknown")
        self.requests.append({"method": request.method, "path": path, "actor": actor})
        if request.method == "GET" and path.startswith("/projects/"):
            return self._read_project(actor)
        if request.method == "PUT" and path.endswith("/members"):
            return self._save_members(request)
        raise AssertionError(f"the double was not asked to answer {request.method} {path}")

    def _members(self) -> list[dict]:
        members = [{"user_id": "u-owner", "role": "owner"}]
        if not self.revoked:
            members.append({"user_id": "u-member", "role": "collaborator"})
        return members

    def _read_project(self, actor: str) -> FakeResponse:
        if actor == "u-member" and self.revoked:
            return self._json(published_example("getProject", 404, "not_found"), 404)
        payload = published_example("getProject", 200, "success")
        payload["data"]["project_version"] = self.project_version
        payload["data"]["members"] = self._members()
        return self._json(payload, 200)

    def _save_members(self, request) -> FakeResponse:
        if self.revocation_fails:
            return self._json(published_example("saveMembers", 409, "version_conflict"), 409)
        body = json.loads(request.data.decode("utf-8"))
        if body.get("expected_project_version") != self.project_version:
            return self._json(published_example("saveMembers", 409, "version_conflict"), 409)
        self.project_version += 1
        if self.revocation_applies:
            self.revoked = True
        payload = published_example("saveMembers", 200, "success")
        payload["data"]["project_version"] = self.project_version
        payload["data"]["members"] = self._members()
        return self._json(payload, 200)

    @staticmethod
    def _json(payload: dict, status: int) -> FakeResponse:
        return FakeResponse(json.dumps(payload, ensure_ascii=False).encode("utf-8"), status)


def two_step_runner(double: MembershipDouble) -> ScenarioRunner:
    """The runner driving the contract's own two-step scenario."""
    return ScenarioRunner.from_files(
        OPENAPI_PATH,
        SCENARIOS_PATH,
        scenario_id="F24",
        opener=double.open,
        identities={"u-owner": OWNER_TOKEN, "u-member": MEMBER_TOKEN},
        variables={"project.id": PROJECT_ID},
    )


def contract_scenario() -> dict:
    document = json.loads(SCENARIOS_PATH.read_text(encoding="utf-8"))
    return next(item for item in document["scenarios"] if item["id"] == "F24")


class TwoStepContractTest(unittest.TestCase):
    """The shape is in the contract, and the machine can read it."""

    def test_the_contract_declares_a_two_step_requirement_on_one_resource(self):
        scenario = contract_scenario()
        steps = {step["id"]: step for step in scenario["steps"]}
        self.assertEqual(sorted(steps), ["F24-01", "F24-02", "F24-03"])
        self.assertNotIn("after_step", steps["F24-01"])
        self.assertNotIn("after_step", steps["F24-02"])
        successor = steps["F24-03"]
        self.assertEqual(successor["after_step"], "F24-02")
        # The two steps are related by order and by resource: the same path
        # coordinates, so the second one is about what the first one changed.
        self.assertEqual(successor["request"]["path_params"], steps["F24-02"]["request"]["path_params"])
        # The successor says what it expects; a step with no expectation would
        # make the relation decoration.
        self.assertTrue(successor["checks"])
        # And the expectation is about the refusal, not about the project.
        self.assertEqual(successor["expected_http"], 404)

    def test_the_runner_reports_the_relation_and_the_kind_of_node(self):
        double = MembershipDouble()
        runner = two_step_runner(double)
        runner.run()
        report = runner.report("completed")
        last = report["steps"][-1]
        self.assertEqual(last["id"], "F24-03")
        self.assertEqual(last["after_step_observation"], {
            "kind": "after_step",
            "request_step_id": "F24-03",
            "after_step": "F24-02",
            "operation_id": "getProject",
        })
        # A node that passed because it was refused must not look like a node
        # that passed because it answered 2xx.
        self.assertEqual(last["node_kind"], "expected_refusal")
        self.assertEqual(last["expected_http"], 404)

    def test_a_successor_that_names_no_step_is_refused(self):
        spec = spec_from_document({
            "id": "F90",
            "steps": [{
                "id": "F90-01", "operation_id": "getProject", "expected_http": 200,
                "request": {"path_params": {"projectId": "p-1"}},
                "after_step": "F90-99",
                "checks": [{"path": "/data/status", "equals": "active", "intent": "synthetic"}],
            }],
        }, "synthetic specification")
        runner = ScenarioRunner(OPENAPI, spec, opener=lambda request, timeout: FakeResponse(b'{"data":{}}'))
        with self.assertRaisesRegex(WorkflowError, "after_step names a step this specification does not have"):
            runner.run()

    def test_a_successor_that_names_a_later_step_is_refused(self):
        spec = spec_from_document({
            "id": "F90",
            "steps": [
                {
                    "id": "F90-01", "operation_id": "getProject", "expected_http": 200,
                    "request": {"path_params": {"projectId": "p-1"}},
                    "after_step": "F90-02",
                    "checks": [{"path": "/data/status", "equals": "active", "intent": "synthetic"}],
                },
                {
                    "id": "F90-02", "operation_id": "getProject", "expected_http": 200,
                    "request": {"path_params": {"projectId": "p-1"}},
                },
            ],
        }, "synthetic specification")
        runner = ScenarioRunner(OPENAPI, spec, opener=lambda request, timeout: FakeResponse(b'{"data":{}}'))
        with self.assertRaisesRegex(WorkflowError, "after_step must name an earlier step"):
            runner.run()

    def test_a_successor_that_reaches_another_resource_is_refused(self):
        spec = spec_from_document({
            "id": "F90",
            "steps": [
                {
                    "id": "F90-01", "operation_id": "getProject", "expected_http": 200,
                    "request": {"path_params": {"projectId": "p-1"}},
                },
                {
                    "id": "F90-02", "operation_id": "getProject", "expected_http": 404,
                    "request": {"path_params": {"projectId": "p-2"}},
                    "after_step": "F90-01",
                    "checks": [{"path": "/error/code", "non_empty": True, "intent": "synthetic"}],
                },
            ],
        }, "synthetic specification")
        runner = ScenarioRunner(OPENAPI, spec, opener=lambda request, timeout: FakeResponse(b'{"data":{}}'))
        with self.assertRaisesRegex(WorkflowError, "after_step must name a step on the same resource"):
            runner.run()

    def test_a_step_that_follows_another_must_say_what_it_expects(self):
        with self.assertRaisesRegex(WorkflowError, "must declare at least one check"):
            spec_from_document({
                "id": "F90",
                "steps": [
                    {
                        "id": "F90-01", "operation_id": "getProject", "expected_http": 200,
                        "request": {"path_params": {"projectId": "p-1"}},
                    },
                    {
                        "id": "F90-02", "operation_id": "getProject", "expected_http": 404,
                        "request": {"path_params": {"projectId": "p-1"}},
                        "after_step": "F90-01",
                    },
                ],
            }, "synthetic specification")


class TwoStepExecutionTest(unittest.TestCase):
    """The assertion is really executed, in both directions."""

    def test_after_the_revocation_the_same_resource_is_refused(self):
        double = MembershipDouble()
        runner = two_step_runner(double)
        result = runner.run()
        self.assertEqual(result["completed_steps"], 3)
        self.assertNotIn("failures", result)
        statuses = {step["id"]: step["http_status"] for step in result["steps"]}
        self.assertEqual(statuses, {"F24-01": 200, "F24-02": 200, "F24-03": 404})
        self.assertTrue(all(check["status"] == "passed" for check in result["checks"]))

    def test_before_the_revocation_the_same_resource_is_still_served(self):
        # The reverse control: the refusal that follows has to be caused by the
        # revocation, so the same resource has to be served before it.
        double = MembershipDouble()
        runner = two_step_runner(double)
        result = runner.run()
        first = result["steps"][0]
        self.assertEqual(first["id"], "F24-01")
        self.assertEqual(first["http_status"], 200)
        checks = [check for check in result["checks"] if check["step_id"] == "F24-01"]
        self.assertTrue(checks)
        self.assertTrue(all(check["status"] == "passed" for check in checks))

    def test_the_refusal_is_an_explicit_refusal(self):
        double = MembershipDouble()
        runner = two_step_runner(double)
        result = runner.run()
        refusal = [check for check in result["checks"] if check["step_id"] == "F24-03"]
        self.assertTrue(refusal)
        self.assertTrue(all(check["status"] == "passed" for check in refusal))
        # The three calls the scenario makes are the three the contract declares.
        self.assertEqual([(call["method"], call["actor"]) for call in double.requests],
                         [("GET", "u-member"), ("PUT", "u-owner"), ("GET", "u-member")])


class TwoStepFailureTest(unittest.TestCase):
    """A two-step operation that fails is not silently skipped."""

    def test_a_failed_revocation_blocks_the_assertion_instead_of_evaluating_it(self):
        double = MembershipDouble(revocation_fails=True)
        runner = two_step_runner(double)
        result = runner.run(stop_on_mismatch=False)
        blocked = [step for step in result["steps"] if step["id"] == "F24-03"]
        self.assertEqual(len(blocked), 1)
        self.assertEqual(blocked[0]["node_kind"], "blocked")
        self.assertIsNone(blocked[0]["http_status"])
        # The assertion was not judged: judging it would make it true no matter
        # what the revocation did.
        self.assertEqual([check for check in result["checks"] if check["step_id"] == "F24-03"], [])
        # And it was not silently skipped either: the run says why.
        blocked_failures = [failure for failure in result["failures"]
                            if failure["kind"] == "precondition_unmet"]
        self.assertEqual(len(blocked_failures), 1)
        self.assertEqual(blocked_failures[0]["step_id"], "F24-03")
        self.assertEqual(blocked_failures[0]["after_step"], "F24-02")
        # The blocked step was never sent.
        self.assertEqual(len(double.requests), 2)

    def test_a_failed_revocation_stops_the_run_that_stops_on_a_mismatch(self):
        double = MembershipDouble(revocation_fails=True)
        runner = two_step_runner(double)
        with self.assertRaisesRegex(WorkflowError, "F24-02 saveMembers: HTTP 409; expected 200"):
            runner.run()

    def test_a_predecessor_whose_own_expectation_failed_blocks_its_successor(self):
        # The predecessor answered 200, but what it asserted did not hold: the
        # consequence its successor would assert never happened either.
        spec = spec_from_document({
            "id": "F90",
            "steps": [
                {
                    "id": "F90-01", "operation_id": "getProject", "expected_http": 200,
                    "request": {"path_params": {"projectId": "p-1"}},
                    "checks": [{"path": "/data/status", "equals": "active", "intent": "synthetic"}],
                },
                {
                    "id": "F90-02", "operation_id": "getProject", "expected_http": 404,
                    "request": {"path_params": {"projectId": "p-1"}},
                    "after_step": "F90-01",
                    "checks": [{"path": "/error/code", "non_empty": True, "intent": "synthetic"}],
                },
            ],
        }, "synthetic specification")
        sent = []

        def opener(request, timeout):
            sent.append(request)
            return FakeResponse(b'{"data":{"status":"draft"}}')

        runner = ScenarioRunner(OPENAPI, spec, opener=opener)
        result = runner.run(stop_on_mismatch=False)
        blocked = [step for step in result["steps"] if step["id"] == "F90-02"]
        self.assertEqual(blocked[0]["node_kind"], "blocked")
        self.assertEqual([check for check in result["checks"] if check["step_id"] == "F90-02"], [])
        self.assertEqual([failure["kind"] for failure in result["failures"]], ["precondition_unmet"])
        # The blocked step was never sent.
        self.assertEqual(len(sent), 1)


class TwoStepNegativeControlTest(unittest.TestCase):
    """When the revocation does not take effect, the assertion turns red."""

    def test_the_assertion_fails_when_the_revocation_does_not_take_effect(self):
        double = MembershipDouble(revocation_applies=False)
        runner = two_step_runner(double)
        with self.assertRaisesRegex(WorkflowError, "F24-03 getProject: HTTP 200; expected 404"):
            runner.run()

    def test_the_run_records_the_failure_rather_than_passing_silently(self):
        double = MembershipDouble(revocation_applies=False)
        runner = two_step_runner(double)
        result = runner.run(stop_on_mismatch=False)
        status_failures = [failure for failure in result["failures"] if failure["kind"] == "http_status"]
        self.assertEqual(len(status_failures), 1)
        self.assertEqual(status_failures[0]["step_id"], "F24-03")
        self.assertEqual((status_failures[0]["expected"], status_failures[0]["actual"]), (404, 200))
        # And the expectation of the step did not hold either: the response was
        # the project, not a refusal.
        refusal = [check for check in result["checks"] if check["step_id"] == "F24-03"]
        self.assertTrue(refusal)
        self.assertFalse(any(check["status"] == "passed" for check in refusal))


class F01SequenceUnchangedTest(unittest.TestCase):
    """The shape change does not touch the F01 sequence."""

    def test_the_f01_sequence_still_has_its_nodes_and_no_two_step_relation(self):
        spec = load_spec(WORKFLOW_PATH)
        self.assertEqual(len(spec.steps), 31)
        self.assertEqual([step["id"] for step in spec.steps if step["id"] != "F01-03b"],
                         [f"F01-{number:02d}" for number in range(1, 7)]
                         + ["F01-06b", "F01-06c", "F01-06d", "F01-06e"]
                         + [f"F01-{number:02d}" for number in range(7, 19)]
                         + ["F01-19a", "F01-19b", "F01-19c"]
                         + [f"F01-{number:02d}" for number in range(19, 24)])
        for step in spec.steps:
            self.assertNotIn("after_step", step, step["id"])

    def test_the_f01_nodes_keep_the_report_shape_they_had(self):
        runner = ScenarioRunner.from_files(OPENAPI_PATH, WORKFLOW_PATH,
                                           variables={"knowledge_id": "k-demo"})
        file_bytes = b"synthetic F01 export bytes; not a real DOCX"
        digest = hashlib.sha256(file_bytes).hexdigest()
        requests = []

        def opener(request, timeout):
            step = runner.spec.steps[len(requests)]
            requests.append(request)
            if step["id"] == "F01-23":
                return FakeResponse(file_bytes, 200, step["expected_binary"]["content_type"])
            reference = step.get("reference_response")
            payload = json.loads(json.dumps(reference)) if reference else {}
            if step["id"] == "F01-22":
                payload["data"]["file_sha256"] = digest
            return FakeResponse(json.dumps(payload, ensure_ascii=False).encode("utf-8"), step["expected_http"])

        runner.client.opener = opener
        result = runner.run()
        self.assertEqual(result["completed_steps"], 31)
        for step in result["steps"]:
            self.assertEqual(set(step), {"id", "operation_id", "http_status", "content_type"}
                             | ({"observation"} if step.get("observation") else set()))
            self.assertNotIn("node_kind", step)
            self.assertNotIn("after_step_observation", step)


if __name__ == "__main__":
    unittest.main()
