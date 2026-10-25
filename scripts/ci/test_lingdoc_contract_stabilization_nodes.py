"""Guard the F01 contract's stabilization chain and the delivery receipt id.

What this file is
-----------------
#619 asked the **contract** to say what the service already does:

* the delivery check answers with a receipt id -- ``CheckResult.receipt_id``
  (``internal/lingdoc/workspace/full_workflow_guard.go`` stores the
  computation receipt and writes its id into the answer);
* freezing is only allowed after the impact task is closed with **that**
  receipt and the Owner stabilizes the revision.  Without those three nodes
  the contract cannot express the chain at all;
* stabilization is **not** expressed by an HTTP status: ``BLOCKED`` and
  ``STABILIZED`` are both 200, and the verdict lives in ``status`` together
  with the list of blockers.

Every test here is a fact about the contract files on disk -- the contract is
read, never mocked.  The mutation tests at the end take a deliberately broken
copy of the contract and show that the guard turns red: a guard that cannot go
red is not a guard.

What this file does not do
--------------------------
It does not touch the service, the provider or the network.  It also does not
decide *what* the server must implement: the server's own answers are the
reference (see the line references above).  Where the contract and the server
disagree, the disagreement is a ticket, not something this test hides.
"""
from __future__ import annotations

import copy
import json
from pathlib import Path
import unittest

REPO_ROOT = Path(__file__).resolve().parents[2]
CONTRACTS = REPO_ROOT / "docs" / "08-本轮实施方案" / "contracts"
WORKFLOW_PATH = CONTRACTS / "workflow.json"
OPENAPI_PATH = CONTRACTS / "openapi.json"
SCENARIOS_PATH = CONTRACTS / "scenarios.json"

#: The three nodes the chain needs, in the order the chain runs them.
CHAIN = ("F01-19a", "F01-19b", "F01-19c")

#: The node that freezes.  Stabilization has to happen before it.
FREEZE = "F01-19"


def _load(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def _order(flow: dict) -> list[str]:
    return [step["id"] for step in flow["steps"]]


def _steps(flow: dict) -> dict[str, dict]:
    return {step["id"]: step for step in flow["steps"]}


def _assert_chain(flow: dict) -> None:
    """The three stabilization nodes exist, in order, before the freeze.

    The chain is: delivery check -> close the impact task with that receipt
    -> stabilization -> freeze.  The freeze is last because the server only
    lets a stabilized revision be frozen.
    """
    order = _order(flow)
    for node in CHAIN:
        assert node in order, f"the contract declares no {node} node"
    positions = [order.index(node) for node in CHAIN]
    assert positions == sorted(positions), (
        "the stabilization nodes are out of order: " + " ".join(order))
    assert max(positions) < order.index(FREEZE), (
        "the stabilization nodes have to run before the freeze "
        f"({FREEZE}): " + " ".join(order))
    steps = _steps(flow)
    assert steps["F01-19a"]["operation_id"] == "checkCurrent", (
        "F01-19a has to be the delivery check: it produces the receipt")
    assert steps["F01-19b"]["operation_id"] == "acknowledgeImpactPreflight", (
        "F01-19b has to close the impact task with that receipt")
    assert steps["F01-19c"]["operation_id"] == "stabilizeRevision", (
        "F01-19c has to be the Owner's stabilization verdict")


def _assert_proof_binding(flow: dict) -> None:
    """The acknowledgement is bound to the delivery check's receipt id.

    The server accepts one kind of proof: a check receipt that was really
    produced under the current baseline.  A frozen snapshot id or an
    idempotency key is not that receipt, so the contract must not send one.
    """
    steps = _steps(flow)
    check_step = steps["F01-19a"]
    acknowledgement = steps["F01-19b"]
    assert check_step.get("capture", {}).get("receipt_id") == "/data/receipt_id", (
        "F01-19a has to capture the receipt id out of the delivery check answer")
    proof = acknowledgement["request"]["json"].get("proof_id")
    assert proof == "{{receipt_id}}", (
        "the acknowledgement has to send the captured receipt id, not " + repr(proof))
    idempotency = (acknowledgement["request"].get("headers") or {}).get("Idempotency-Key", "")
    assert proof != idempotency, (
        "the idempotency key is not a proof: the server rejects it")
    assert proof != "{{snapshot_id}}", (
        "a frozen snapshot id is not a proof: the server rejects it")
    assert proof != "{{policy_change_set_id}}", (
        "a change set id is not a proof: the server rejects it")


def _assert_stabilization_status(flow: dict) -> None:
    """The stabilization node judges the verdict field, not the status code.

    ``BLOCKED`` and ``STABILIZED`` are both HTTP 200.  A node that only
    looked at the status code would call every blocked revision stable.
    """
    step = _steps(flow)["F01-19c"]
    assert step["operation_id"] == "stabilizeRevision"
    assert step["expected_http"] == 200, (
        "both verdicts answer 200; the status code is not the verdict")
    checks = step.get("checks") or []
    status_checks = [check for check in checks if check.get("path") == "/data/status"]
    assert status_checks, (
        "the stabilization node asserts nothing about /data/status: "
        "a 200 alone cannot tell STABILIZED from BLOCKED")
    verdicts = {check.get("equals") for check in status_checks if "equals" in check}
    assert "STABILIZED" in verdicts, (
        "the stabilization node has to say which verdict it expects: " + str(sorted(verdicts)))
    assert any(check.get("path") == "/data/blockers" for check in checks), (
        "the stabilization node has to say what the blocker list has to look like")


def _assert_receipt_schema(openapi: dict) -> None:
    """``CheckResult`` publishes the receipt id and stays closed.

    The answer the service really sends carries ``receipt_id``; the schema
    published ``additionalProperties: false`` without it, so the real answer
    violated the contract.  The field goes in as a published property; the
    closed shape stays closed.
    """
    schema = openapi["components"]["schemas"]["CheckResult"]
    properties = schema.get("properties") or {}
    assert "receipt_id" in properties, (
        "CheckResult does not publish receipt_id: the delivery check's real "
        "answer violates the contract")
    assert properties["receipt_id"].get("type") == "string", (
        "receipt_id is a string on the wire")
    assert schema.get("additionalProperties") is False, (
        "CheckResult must not accept unknown fields: the shape is closed")
    # receipt_id is optional on purpose, and this is why: the same schema
    # also describes the frozen snapshot's check, which is Evaluate's result
    # (internal/lingdoc/delivery/snapshot.go:424) and carries no receipt.
    assert "receipt_id" not in (schema.get("required") or []), (
        "receipt_id must stay optional here: the same schema describes the "
        "frozen snapshot's check, which carries no receipt")


def _assert_check_idempotency_agreement(flow: dict, openapi: dict) -> None:
    """The node and the published operation agree about the idempotency key.

    The delivery check does not read an idempotency key -- it does not freeze
    anything (see the ``Check`` handler in
    ``internal/lingdoc/workspace/delivery_http.go``), and the published
    operation declares no such header.  Whichever way this is decided, the
    node and the operation must not disagree: a header the operation does not
    declare is a claim the contract cannot back.
    """
    headers = _steps(flow)["F01-19a"]["request"].get("headers") or {}
    sent = any(name.lower() == "idempotency-key" for name in headers)
    declared = any(
        parameter.get("in") == "header"
        and str(parameter.get("name", "")).lower() == "idempotency-key"
        and parameter.get("required")
        for parameter in openapi["paths"]["/projects/{projectId}/checks"]["post"]
        .get("parameters", [])
    )
    assert sent == declared, (
        "the check node sends an idempotency key the published operation does "
        f"not declare (sent={sent}, declared={declared})")


def _assert_scenarios(cases: dict) -> None:
    """The scenario file covers the stabilization paths, not just the happy one."""
    stabilization = []
    for scenario in cases["scenarios"]:
        steps = scenario.get("steps") or []
        if any(step.get("operation_id") == "stabilizeRevision" for step in steps):
            stabilization.append(scenario)
    assert stabilization, (
        "no scenario drives the stabilization path: the contract's failure "
        "paths are untested")
    blocker_codes = set()
    verdicts = set()
    for scenario in stabilization:
        for step in scenario.get("steps") or []:
            for check in step.get("checks") or []:
                if check.get("path", "").startswith("/data/blockers"):
                    if "equals" in check:
                        blocker_codes.add(check["equals"])
                if (step.get("operation_id") == "stabilizeRevision"
                        and check.get("path") == "/data/status" and "equals" in check):
                    verdicts.add(check["equals"])
    assert "BLOCKED" in verdicts, (
        "no scenario says what a blocked stabilization looks like")
    assert "impact_task_open" in blocker_codes, (
        "no scenario covers the open impact task blocking the stabilization")
    assert "impact_object_stale" in blocker_codes, (
        "no scenario covers the replaced impacted object blocking the stabilization")


class ContractStabilizationChainTests(unittest.TestCase):
    """What the contract says about the chain the server actually runs."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.flow = _load(WORKFLOW_PATH)
        cls.openapi = _load(OPENAPI_PATH)
        cls.cases = _load(SCENARIOS_PATH)

    def test_the_contract_declares_the_stabilization_chain_in_order(self):
        _assert_chain(self.flow)

    def test_the_acknowledgement_is_bound_to_the_check_receipt(self):
        _assert_proof_binding(self.flow)

    def test_the_stabilization_node_judges_the_status_field(self):
        _assert_stabilization_status(self.flow)

    def test_the_check_result_schema_carries_the_receipt_id(self):
        _assert_receipt_schema(self.openapi)

    def test_the_check_node_agrees_with_the_published_operation(self):
        _assert_check_idempotency_agreement(self.flow, self.openapi)

    def test_the_scenarios_cover_the_stabilization_paths(self):
        _assert_scenarios(self.cases)

    # ------------------------------------------------------------ mutations
    # A guard that cannot go red is not a guard: every fact above is broken
    # on purpose below, and the guard has to notice.

    def _mutated_flow(self, mutate) -> dict:
        flow = copy.deepcopy(self.flow)
        mutate(flow)
        return flow

    def test_removing_a_chain_node_turns_the_guard_red(self):
        flow = self._mutated_flow(
            lambda f: f["steps"].remove(next(s for s in f["steps"] if s["id"] == "F01-19b")))
        with self.assertRaises(AssertionError):
            _assert_chain(flow)

    def test_moving_the_chain_after_the_freeze_turns_the_guard_red(self):
        def mutate(flow):
            node = next(s for s in flow["steps"] if s["id"] == "F01-19c")
            flow["steps"].remove(node)
            flow["steps"].append(node)
        with self.assertRaises(AssertionError):
            _assert_chain(self._mutated_flow(mutate))

    def test_swapping_two_chain_nodes_turns_the_guard_red(self):
        def mutate(flow):
            first = next(i for i, s in enumerate(flow["steps"]) if s["id"] == "F01-19a")
            second = next(i for i, s in enumerate(flow["steps"]) if s["id"] == "F01-19b")
            flow["steps"][first], flow["steps"][second] = flow["steps"][second], flow["steps"][first]
        with self.assertRaises(AssertionError):
            _assert_chain(self._mutated_flow(mutate))

    def test_binding_the_proof_to_the_idempotency_key_turns_the_guard_red(self):
        def mutate(flow):
            step = next(s for s in flow["steps"] if s["id"] == "F01-19b")
            step["request"]["json"]["proof_id"] = "f01-preflight-ack-0001"
        with self.assertRaises(AssertionError):
            _assert_proof_binding(self._mutated_flow(mutate))

    def test_binding_the_proof_to_the_snapshot_id_turns_the_guard_red(self):
        def mutate(flow):
            step = next(s for s in flow["steps"] if s["id"] == "F01-19b")
            step["request"]["json"]["proof_id"] = "{{snapshot_id}}"
        with self.assertRaises(AssertionError):
            _assert_proof_binding(self._mutated_flow(mutate))

    def test_dropping_the_receipt_capture_turns_the_guard_red(self):
        def mutate(flow):
            step = next(s for s in flow["steps"] if s["id"] == "F01-19a")
            step["capture"] = {}
        with self.assertRaises(AssertionError):
            _assert_proof_binding(self._mutated_flow(mutate))

    def test_a_wrong_stabilization_expectation_turns_the_guard_red(self):
        def mutate(flow):
            step = next(s for s in flow["steps"] if s["id"] == "F01-19c")
            for check in step["checks"]:
                if check.get("path") == "/data/status":
                    check["equals"] = "BLOCKED"
        with self.assertRaises(AssertionError):
            _assert_stabilization_status(self._mutated_flow(mutate))

    def test_a_stabilization_node_without_a_status_check_turns_the_guard_red(self):
        def mutate(flow):
            step = next(s for s in flow["steps"] if s["id"] == "F01-19c")
            step["checks"] = [c for c in step["checks"] if c.get("path") != "/data/status"]
        with self.assertRaises(AssertionError):
            _assert_stabilization_status(self._mutated_flow(mutate))

    def test_a_schema_without_the_receipt_id_turns_the_guard_red(self):
        openapi = copy.deepcopy(self.openapi)
        del openapi["components"]["schemas"]["CheckResult"]["properties"]["receipt_id"]
        with self.assertRaises(AssertionError):
            _assert_receipt_schema(openapi)

    def test_a_schema_that_accepts_anything_turns_the_guard_red(self):
        openapi = copy.deepcopy(self.openapi)
        openapi["components"]["schemas"]["CheckResult"]["additionalProperties"] = True
        with self.assertRaises(AssertionError):
            _assert_receipt_schema(openapi)

    def test_making_the_receipt_id_required_turns_the_guard_red(self):
        openapi = copy.deepcopy(self.openapi)
        schema = openapi["components"]["schemas"]["CheckResult"]
        schema["required"] = sorted(set(schema["required"]) | {"receipt_id"})
        with self.assertRaises(AssertionError):
            _assert_receipt_schema(openapi)

    def test_sending_an_undeclared_idempotency_key_turns_the_guard_red(self):
        def mutate(flow):
            step = next(s for s in flow["steps"] if s["id"] == "F01-19a")
            step["request"]["headers"]["Idempotency-Key"] = "f01-check-0001"
        with self.assertRaises(AssertionError):
            _assert_check_idempotency_agreement(self._mutated_flow(mutate), self.openapi)

    def test_scenarios_without_the_stabilization_paths_turn_the_guard_red(self):
        cases = copy.deepcopy(self.cases)
        for scenario in cases["scenarios"]:
            scenario["steps"] = [
                step for step in (scenario.get("steps") or [])
                if step.get("operation_id") != "stabilizeRevision"
            ]
        with self.assertRaises(AssertionError):
            _assert_scenarios(cases)


if __name__ == "__main__":
    unittest.main()
