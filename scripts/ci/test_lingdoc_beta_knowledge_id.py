"""Guard tests for the F01 material identifier (#494).

What this file guards
---------------------
The F01 contract binds a material at F01-06.  The identifier it binds used to
be the literal ``k-demo``, so the run could only ever work on an environment
that happened to have that identifier -- and it broke at the sixth node, with
the rest of the sequence unreachable.  The identifier is now a variable
(``{{knowledge_id}}``) that the environment supplies.

What these tests pin:

* the contract does not carry the placeholder any more, and it says where the
  value comes from and that there is no default;
* the runner refuses a run whose contract names a variable nobody supplies --
  it must **not** fall back to the placeholder, because a silent fallback
  turns "it ran" into a fiction;
* the identifier travels from the entry point to the bind step, and there is no
  second place that hard-codes one;
* a run against an identifier that does not exist fails **at that node**, says
  what is missing, and does not skip the step or swap the identifier;
* the run's own record shows which identifier the run used.

They run offline: no provider, no network, no browser.
"""
from __future__ import annotations

import json
import sys
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT))

from scripts.lingdoc_mock.run_f01 import (  # noqa: E402
    ScenarioRunner, WorkflowError, spec_from_document, _SelfTestResponse,
)

CONTRACT = REPO_ROOT / "docs" / "08-本轮实施方案" / "contracts" / "workflow.json"
EVIDENCE = REPO_ROOT / "scripts" / "acceptance" / "beta_evidence.py"
RUNNER = REPO_ROOT / "scripts" / "lingdoc_mock" / "run_f01.py"


def contract_document() -> dict:
    return json.loads(CONTRACT.read_text(encoding="utf-8"))


def step_by_id(document: dict, step_id: str) -> dict:
    for step in document["steps"]:
        if step.get("id") == step_id:
            return step
    raise AssertionError(f"the contract has no step {step_id}")


class ContractShapeTests(unittest.TestCase):
    """The contract names the material; it does not carry one."""

    def test_the_bind_step_takes_the_identifier_from_a_variable(self):
        body = step_by_id(contract_document(), "F01-06")["request"]["json"]
        self.assertEqual(body.get("knowledge_id"), "{{knowledge_id}}")

    def test_the_placeholder_is_gone_from_the_executable_part_of_the_contract(self):
        """A reference response may carry the placeholder id; the run may not.

        The contract says so itself: "the ids in the reference response are
        for the static semantic check; a real run takes the id from the
        capture".  So the placeholder may stay in a ``reference_response`` --
        what must not stay is a hard-coded identifier in anything the run
        actually sends.
        """
        document = contract_document()
        for step in document["steps"]:
            blob = json.dumps(step.get("request"), ensure_ascii=False)
            self.assertNotIn("k-demo", blob, f"step {step.get('id')} still sends a hard-coded id")
        self.assertNotIn("k-demo", json.dumps(document["setup"], ensure_ascii=False))
        # The reference response keeps the placeholder, and the contract says
        # that the ids in it are placeholders.
        self.assertIn("reference_ids", document)
        self.assertIn("参考响应", document["reference_ids"])

    def test_the_setup_says_where_the_identifier_comes_from_and_that_there_is_no_default(self):
        ready = contract_document()["setup"].get("ready_knowledge", "")
        self.assertIn("{{knowledge_id}}", ready)
        self.assertIn("--knowledge-id", ready)
        self.assertNotIn("k-demo", ready)

    def test_no_second_place_hard_codes_an_identifier(self):
        """Only the entry point may name the identifier; the contract takes it."""
        for path in (EVIDENCE, RUNNER):
            source = path.read_text(encoding="utf-8")
            self.assertNotIn("k-demo", source, f"{path.name} still hard-codes the demo identifier")


class RunnerPreflightTests(unittest.TestCase):
    """A contract that names a variable nobody supplies must be refused."""

    OPENAPI = {
        "servers": [{"url": "https://provider.test/api/v1"}],
        "paths": {"/assets": {"post": {"operationId": "bindAsset"}}},
    }

    @staticmethod
    def specification() -> dict:
        return spec_from_document({"id": "S", "steps": [
            {"id": "S1", "operation_id": "bindAsset", "expected_http": 201,
             "request": {"path_params": {}, "headers": {}, "json": {"knowledge_id": "{{knowledge_id}}"}},
             "capture": {}, "checks": []},
        ]}, "guard specification")

    def test_a_run_without_the_identifier_is_refused_before_any_request(self):
        sent = []

        def opener(request, timeout):
            sent.append(request)
            raise AssertionError("the runner must not send a request")

        runner = ScenarioRunner(self.OPENAPI, self.specification(),
                                base_url="https://provider.test/api/v1", opener=opener)
        with self.assertRaises(WorkflowError) as caught:
            runner.run()
        message = str(caught.exception)
        self.assertIn("knowledge_id", message)
        self.assertIn("does not supply", message)
        self.assertIn("no default", message)
        self.assertEqual(sent, [])

    def test_a_run_with_the_identifier_sends_it_to_the_bind_step(self):
        seen = {}

        def opener(request, timeout):
            seen["body"] = request.data.decode("utf-8") if request.data else ""
            return _SelfTestResponse({"data": {"id": "asset-1"}}, status=201)

        runner = ScenarioRunner(self.OPENAPI, self.specification(),
                                base_url="https://provider.test/api/v1", opener=opener,
                                variables={"knowledge_id": "k-real-42"})
        runner.run()
        self.assertEqual(json.loads(seen["body"]).get("knowledge_id"), "k-real-42")

    def test_an_identifier_that_does_not_exist_fails_at_that_node_and_keeps_the_identifier(self):
        """The negative check: a material that is not there must fail, not skip."""
        seen = {}

        def opener(request, timeout):
            seen["body"] = request.data.decode("utf-8") if request.data else ""
            return _SelfTestResponse({"error": {"code": "not_found"}}, status=404)

        runner = ScenarioRunner(self.OPENAPI, self.specification(),
                                base_url="https://provider.test/api/v1", opener=opener,
                                variables={"knowledge_id": "k-does-not-exist"})
        runner.run(stop_on_mismatch=False)
        # The identifier that was asked for is the one that was sent: no swap.
        self.assertEqual(json.loads(seen["body"]).get("knowledge_id"), "k-does-not-exist")
        # The step failed instead of being skipped.
        self.assertEqual(len(runner.failures), 1)
        self.assertEqual(runner.failures[0]["step_id"], "S1")
        self.assertEqual(runner.failures[0]["expected"], 201)
        self.assertEqual(runner.failures[0]["actual"], 404)
        self.assertEqual(runner.step_results, [])


class RecordTests(unittest.TestCase):
    """The run's own record has to show which material it used."""

    def test_the_evidence_producer_takes_the_identifier_and_records_it(self):
        source = EVIDENCE.read_text(encoding="utf-8")
        self.assertIn("--knowledge-id", source)
        self.assertIn('"knowledge_id": knowledge_id', source)
        self.assertIn('"knowledge": ', source)

    def test_the_runner_exposes_the_identifier_on_the_command_line(self):
        source = RUNNER.read_text(encoding="utf-8")
        self.assertIn('"--knowledge-id"', source)
        self.assertIn('"knowledge_id": args.knowledge_id', source)


if __name__ == "__main__":
    unittest.main()
