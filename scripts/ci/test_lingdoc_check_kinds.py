"""Guard tests for the check kinds of the acceptance contract (#499).

What this file guards
---------------------
The contract's assertions are the Beta release's only machine judgement.  Three
things have to stay true, and each of them used to be false somewhere:

*There is one vocabulary.*  The kinds used to live in three places -- the runner,
the contract's static validator, and the spec -- and the copies disagreed.  A
second copy is a defect, so these tests read the vocabulary from the one
authority (``scripts/lingdoc_check_kinds.py``) and prove that the runner, the
validator and the report's wording all read *that* one.

*Every kind is used, and every assertion is judgeable.*  A kind nobody uses is
idle expression power; an assertion that names no kind, or two, or a key no kind
declares, is an expectation nobody can judge.  The first is a hole in the
contract, the second is a hole in the machinery.

*A broken expectation turns red.*  An assertion that cannot fail proves nothing.
So every new kind is driven against its own reference response with the
expectation broken on purpose, and the run has to say "failed".

Where this file lives
---------------------
It sits in ``scripts/ci/`` because it is a guard test, and the review workflow
collects the guard tests from there.  The authority it guards lives in
``scripts/`` next to the other "one truth about X" modules.
"""
from __future__ import annotations

import json
from pathlib import Path
import re
import sys
import unittest

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT))

import scripts.lingdoc_check_kinds as check_kinds  # noqa: E402
from scripts.lingdoc_mock import run_f01  # noqa: E402
from scripts.lingdoc_mock.run_f01 import ScenarioRunner, spec_from_document  # noqa: E402

CONTRACTS = REPO_ROOT / "docs" / "08-本轮实施方案" / "contracts"
WORKFLOW = CONTRACTS / "workflow.json"
SCENARIOS = CONTRACTS / "scenarios.json"
OPENAPI = CONTRACTS / "openapi.json"
GAPS = REPO_ROOT / "docs" / "08-本轮实施方案" / "acceptance" / "gaps.json"
RUNNER_SOURCE = REPO_ROOT / "scripts" / "lingdoc_mock" / "run_f01.py"
VALIDATOR_SOURCE = CONTRACTS / "validate_artifacts.py"


def contract(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def all_checks(document: dict) -> list[tuple[str, dict]]:
    """Every declared assertion of a contract document, with the step it hangs on."""
    found: list[tuple[str, dict]] = []
    for step in document.get("steps", []):
        for check in step.get("checks", []) or []:
            found.append((step.get("id", ""), check))
    for scenario in document.get("scenarios", []):
        for step in scenario.get("steps", []) or []:
            for check in step.get("checks", []) or []:
                found.append((f"{scenario['id']}/{step.get('id', '')}", check))
    return found


def kinds_in(document: dict) -> set[str]:
    """The kinds a contract document actually uses."""
    used: set[str] = set()
    for _where, check in all_checks(document):
        present = [name for name in check_kinds.KNOWN_CHECK_OPERATORS if name in check]
        if len(present) == 1:
            used.add(present[0])
    return used


class OneVocabularyTests(unittest.TestCase):
    """The vocabulary is declared once."""

    def test_the_runner_uses_the_authority_rather_than_a_copy_of_it(self):
        self.assertIs(run_f01.KNOWN_CHECK_OPERATORS, check_kinds.KNOWN_CHECK_OPERATORS)
        self.assertIs(run_f01.CHECK_OPERATORS, check_kinds.STRUCTURAL_CHECK_OPERATORS)
        self.assertIs(run_f01.DOMAIN_CHECK_OPERATORS, check_kinds.DOMAIN_CHECK_OPERATORS)

    def test_the_contract_validator_reads_the_authority_rather_than_a_copy_of_it(self):
        source = VALIDATOR_SOURCE.read_text(encoding="utf-8")
        self.assertIn("import lingdoc_check_kinds as check_kinds", source)
        # A tuple of operator names written down here would be a second vocabulary.
        self.assertIsNone(re.search(r"CHECK_OPERATORS\s*=\s*\(", source),
                          "the validator declares its own operator list")

    def test_the_report_has_a_wording_for_every_kind(self):
        from scripts.lingdoc_mock import run_scenario
        self.assertEqual(set(run_scenario.EXPECTATION_PHRASES),
                         set(check_kinds.KNOWN_CHECK_OPERATORS))

    def test_every_kind_is_implemented_in_the_runner(self):
        source = RUNNER_SOURCE.read_text(encoding="utf-8")
        for kind in check_kinds.KNOWN_CHECK_OPERATORS:
            self.assertIn(f'"{kind}"', source,
                          f"the runner never mentions the kind {kind}")


class ExhaustiveVocabularyTests(unittest.TestCase):
    """No idle kind, and no assertion nobody can judge."""

    def test_every_kind_is_used_by_the_contract_or_declared_unmounted_with_a_gap(self):
        workflow = contract(WORKFLOW)
        scenarios = contract(SCENARIOS)
        used = kinds_in(workflow) | kinds_in(scenarios)
        unmounted = {entry["kind"]: entry
                     for entry in workflow.get("check_kinds", {}).get("unmounted", [])}
        gaps = contract(GAPS)["GAPS"]
        for kind in check_kinds.KNOWN_CHECK_OPERATORS:
            if kind in used:
                self.assertNotIn(kind, unmounted,
                                 f"{kind} is used by the contract and also declared unmounted")
                continue
            self.assertIn(kind, unmounted,
                          f"the kind {kind} is idle: no assertion uses it and the contract "
                          "does not say why")
            self.assertIn(unmounted[kind]["gap"], gaps,
                          f"{kind} names a gap the registry does not hold")

    def test_the_unmounted_kinds_are_the_ones_the_contract_cannot_carry(self):
        workflow = contract(WORKFLOW)
        used = kinds_in(workflow) | kinds_in(contract(SCENARIOS))
        for entry in workflow.get("check_kinds", {}).get("unmounted", []):
            self.assertNotIn(entry["kind"], used)
            self.assertTrue(str(entry.get("why", "")).strip(),
                            f"{entry['kind']} says nothing about why it is not mounted")

    def test_every_assertion_of_both_contract_documents_declares_one_known_kind(self):
        for path in (WORKFLOW, SCENARIOS):
            for where, check in all_checks(contract(path)):
                name = check_kinds.validate_check(check, f"{where} check")
                self.assertIn(name, check_kinds.KNOWN_CHECK_OPERATORS)

    def test_the_contract_uses_the_two_kinds_that_used_to_be_idle(self):
        used = kinds_in(contract(WORKFLOW))
        self.assertIn("keys", used, "the keys kind is still idle in the F01 contract")
        self.assertIn("one_of", used, "the one_of kind is still idle in the F01 contract")

    def test_the_operator_table_covers_every_kind(self):
        """Every kind has a case that holds and a case that does not."""
        names = [name for name in dir(run_f01.CheckOperatorSelfTest) if name.startswith("test_")]
        for kind in check_kinds.KNOWN_CHECK_OPERATORS:
            self.assertTrue(any(kind in name for name in names),
                            f"the operator table has no case for the kind {kind}")


class UnknownKindTests(unittest.TestCase):
    """An expectation nobody can judge is refused, not ignored."""

    def test_a_kind_nobody_knows_is_refused(self):
        with self.assertRaises(check_kinds.CheckKindError):
            check_kinds.validate_check({"path": "/data/x", "not_a_kind": 1, "intent": "拼错的 kind"},
                                        "a check")

    def test_an_unknown_key_next_to_a_known_kind_is_refused(self):
        # This is the hole the ticket names: the runner counted the kinds it knew and
        # ignored the rest, so a misspelled kind next to a well-known one disappeared.
        with self.assertRaises(check_kinds.CheckKindError):
            check_kinds.validate_check({"path": "/data/x", "equals": "v", "not_a_kind": True,
                                         "intent": "多出来的一个键"}, "a check")

    def test_a_check_that_names_two_kinds_is_refused(self):
        with self.assertRaises(check_kinds.CheckKindError):
            check_kinds.validate_check({"path": "/data/x", "equals": "v", "one_of": ["v"],
                                         "intent": "两个 kind"}, "a check")

    def test_the_runner_refuses_a_check_it_cannot_judge(self):
        with self.assertRaisesRegex(run_f01.WorkflowError, "must state exactly one of"):
            spec_from_document({"id": "SELF", "steps": [{
                "id": "SELF-01", "operation_id": "getProject", "expected_http": 200,
                "request": {"path_params": {"projectId": "p-1"}, "headers": {}, "json": None},
                "capture": {}, "checks": [{"path": "/data/x", "not_a_kind": 1, "intent": "拼错的 kind"}]}]},
                "guard specification")


class _Response:
    """One answer from a provider that never exists."""

    def __init__(self, payload: bytes, status: int = 200) -> None:
        self._payload = payload
        self.status = status
        self.headers = {"Content-Type": "application/json"}

    def read(self, limit: int = 16 * 1024 * 1024) -> bytes:
        return self._payload

    def close(self) -> None:
        pass


class BrokenExpectationTests(unittest.TestCase):
    """An assertion that cannot fail proves nothing: break it and watch it turn red."""

    def judged(self, step_id: str, check: dict, mutate=None) -> list[dict]:
        """Judge one contract assertion against the contract's own reference response.

        A cross-step assertion needs the step it compares with, so that step is
        replayed first -- under its own id, because that is the id the assertion
        names.  ``mutate`` changes the response before it is judged, so that a
        broken expectation can be watched turning red.
        """
        workflow = contract(WORKFLOW)
        steps = {step["id"]: step for step in workflow["steps"]}
        step = steps[step_id]
        openapi = contract(OPENAPI)
        _server, operations = run_f01.operation_index(openapi)
        plan: list[dict] = []
        if "equals_step" in check:
            plan.append(steps[check["equals_step"]["from_step"]])
        plan.append(step)
        answers = []
        for item in plan:
            payload = item["reference_response"]
            if mutate is not None:
                payload = mutate(payload)
            answers.append((json.dumps(payload).encode("utf-8"), item["expected_http"]))

        def opener(request, timeout):
            payload, status = answers.pop(0)
            return _Response(payload, status)

        synthetic: list[dict] = []
        for index, item in enumerate(plan, start=1):
            _method, path_template = operations[item["operation_id"]]
            params = {name: "p-1" for name in re.findall(r"\{([^}]+)\}", path_template)}
            synthetic.append({
                "id": item["id"], "operation_id": item["operation_id"],
                "expected_http": item["expected_http"],
                "request": {"path_params": params, "headers": {}, "json": None},
                "capture": {}, "checks": [check] if index == len(plan) else []})
        document = {"id": "GUARD", "steps": synthetic}
        runner = ScenarioRunner(openapi, spec_from_document(document, "guard specification"),
                                base_url="http://127.0.0.1:4010/api/v1/lingdoc", opener=opener)
        runner.run()
        return runner.report("completed").get("checks", [])

    def test_the_keys_assertion_holds_against_the_contract_and_fails_when_it_is_broken(self):
        step = next(step for step in contract(WORKFLOW)["steps"] if step["id"] == "F01-06")
        check = next(c for c in step["checks"] if "keys" in c)
        self.assertEqual(self.judged("F01-06", check)[0]["status"], "passed")
        broken = dict(check, keys=check["keys"][:-1])
        self.assertEqual(self.judged("F01-06", broken)[0]["status"], "failed")

    def test_the_one_of_assertion_holds_against_the_contract_and_fails_when_it_is_broken(self):
        step = next(step for step in contract(WORKFLOW)["steps"] if step["id"] == "F01-09")
        check = next(c for c in step["checks"] if "one_of" in c)
        self.assertEqual(self.judged("F01-09", check)[0]["status"], "passed")
        broken = dict(check, one_of=["not_a_value_at_all"])
        self.assertEqual(self.judged("F01-09", broken)[0]["status"], "failed")

    def test_the_collection_assertion_fails_when_one_entry_is_empty(self):
        step = next(step for step in contract(WORKFLOW)["steps"] if step["id"] == "F01-19")
        check = next(c for c in step["checks"] if "all_non_empty" in c)
        self.assertEqual(self.judged("F01-19", check)[0]["status"], "passed")

        def empty_entry(response):
            broken = json.loads(json.dumps(response))
            broken["data"]["frozen_input"]["asset_versions"][0] = {}
            return broken

        self.assertEqual(self.judged("F01-19", check, mutate=empty_entry)[0]["status"], "failed")

    def test_the_cross_step_assertion_fails_when_the_two_steps_disagree(self):
        step = next(step for step in contract(WORKFLOW)["steps"] if step["id"] == "F01-19")
        check = next(c for c in step["checks"] if "equals_step" in c)
        self.assertEqual(self.judged("F01-19", check)[0]["status"], "passed")
        broken = dict(check, equals_step=dict(check["equals_step"], path="/data/id"))
        self.assertEqual(self.judged("F01-19", broken)[0]["status"], "failed")


if __name__ == "__main__":
    unittest.main()
