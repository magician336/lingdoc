"""Guard tests for the check kinds of the acceptance contract (#499).

What this file guards
---------------------
The contract's assertions are the Beta release's only machine judgement.  Four
things have to stay true, and each of them used to be false somewhere:

*There is one vocabulary.*  The kinds used to live in three places -- the runner,
the contract's static validator, and the spec -- and the copies disagreed.  A
second copy is a defect, so these tests read the vocabulary from the one
authority (``scripts/lingdoc_check_kinds.py``) and prove that the runner, the
validator and the report's wording all read *that* one.

*Every kind has an evaluation branch, and only the kinds in the vocabulary have
one.*  This is the quiet one.  The domain kinds used to end in a fallthrough: a
kind nobody handled was judged as ``non_empty``.  The structural kinds used to
end in ``else: # one_of``.  Either way the report said "passed" about an
expectation nobody judged -- the assertion looked evaluated while it had been
answered by a different question.  The runner now refuses a kind it has no
branch for, and these tests hold both ends of that: the vocabulary and the
branch set are the same set, and the refusal really happens (a kind with no
branch is injected and watched being refused).

*Every assertion of the contract declares exactly one known kind.*  A check that
names no kind, two kinds, or a kind together with a key no kind declares is an
expectation nobody can judge.  The contract's own assertions used to be checked
only when the runner reached them -- so a broken one stayed invisible until the
day it ran.

*No kind may sit idle.*  A kind nobody uses is expression power nobody
exercises, and it used to happen that ``keys`` and ``one_of`` were implemented
and never used.  Every kind must be used by one of the two contract documents,
or be registered as known but not enabled yet in the F01 contract's
``check_kinds`` block -- with a reason and a follow-up.  Silent idleness is not
allowed.

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
from unittest.mock import patch

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT))

import scripts.lingdoc_check_kinds as check_kinds  # noqa: E402
from scripts.lingdoc_mock import run_f01  # noqa: E402
from scripts.lingdoc_mock.run_f01 import ScenarioRunner, WorkflowError, spec_from_document  # noqa: E402

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


def not_enabled_yet(document: dict) -> dict[str, dict]:
    """The kinds the F01 contract registers as known but not enabled yet.

    "Known but not enabled yet" is the contract's own bookkeeping: a kind that
    exists and works, but that no assertion uses yet.  It has to be **registered**
    -- silence is not allowed -- and the registration says why and what releases
    it.
    """
    block = document.get("check_kinds", {})
    entries = block.get("not_in_use", []) or block.get("unmounted", []) or []
    return {entry["kind"]: entry for entry in entries if isinstance(entry, dict) and "kind" in entry}


#: One probe per kind: a valid check of that kind, and the payload it is judged
#: against.  The table is keyed by the kind, and the guard asserts that it covers
#: the vocabulary exactly -- so extending the vocabulary without a probe turns
#: the guard red instead of leaving the new kind untested.
PROBES: dict[str, tuple[dict, dict]] = {
    "equals": ({"path": "/data/x", "equals": "v"}, {"data": {"x": "v"}}),
    "length": ({"path": "/data/x", "length": 1}, {"data": {"x": ["a"]}}),
    "keys": ({"path": "/data/x", "keys": ["a"]}, {"data": {"x": {"a": 1}}}),
    "one_of": ({"path": "/data/x", "one_of": ["a", "b"]}, {"data": {"x": "a"}}),
    "ref_in": ({"path": "/data/x", "ref_in": "/data/others"},
               {"data": {"x": "s-1", "others": [{"id": "s-1"}]}}),
    "marked_or_absent": ({"path": "/data/x", "marked_or_absent": "/statement"},
                         {"data": {"x": {"statement": "s"}}}),
    "version_bound": ({"path": "/data/x", "version_bound": "/data/y"},
                      {"data": {"x": "v", "y": "v"}}),
    "readback_equals": ({"path": "/data/x", "readback_equals": "v"}, {"data": {"x": "v"}}),
    "hash_equals": ({"path": "/data/x", "hash_equals": {"from_step": "SYN-01"}},
                    {"data": {"x": "h"}}),
    "non_empty": ({"path": "/data/x", "non_empty": True}, {"data": {"x": "v"}}),
    "http_error": ({"path": "/error", "http_error": {"code": "version_conflict"}},
                   {"error": {"code": "version_conflict", "message": "m", "retryable": False}}),
    "not_in": ({"path": "/data/x", "not_in": "/data/others"},
               {"data": {"x": "s-1", "others": [{"id": "s-2"}]}}),
    "all_non_empty": ({"path": "/data/items", "all_non_empty": True},
                      {"data": {"items": [{"id": "a"}]}}),
    "equals_step": ({"path": "/data/x", "equals_step": {"from_step": "SYN-01", "path": "/data/x"}},
                    {"data": {"x": "v"}}),
    "provenance_real": ({"path": "/data/run_mode", "provenance_real": True},
                        {"data": {"run_mode": "real"}}),
    "one_to_one": ({"path": "/data/x", "one_to_one": {"from_step": "SYN-01", "path": "/data/items"}},
                   {"data": {"x": ["a"]}}),
}


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
        unmounted = not_enabled_yet(workflow)
        gaps = contract(GAPS)["GAPS"]
        for kind in check_kinds.KNOWN_CHECK_OPERATORS:
            if kind in used:
                self.assertNotIn(kind, unmounted,
                                 f"{kind} is used by the contract and also declared as idle")
                continue
            self.assertIn(kind, unmounted,
                          f"the kind {kind} is idle: no assertion uses it and the contract "
                          "does not register it as known but not enabled yet")
            self.assertIn(unmounted[kind]["gap"], gaps,
                          f"{kind} names a gap the registry does not hold")

    def test_the_unmounted_kinds_are_the_ones_the_contract_cannot_carry(self):
        workflow = contract(WORKFLOW)
        used = kinds_in(workflow) | kinds_in(contract(SCENARIOS))
        for kind, entry in not_enabled_yet(workflow).items():
            self.assertNotIn(kind, used)
            self.assertTrue(str(entry.get("why", "")).strip(),
                            f"{kind} says nothing about why it is not enabled yet")

    def test_every_assertion_of_both_contract_documents_declares_one_known_kind(self):
        for path in (WORKFLOW, SCENARIOS):
            for where, check in all_checks(contract(path)):
                name = check_kinds.validate_check(check, f"{where} check")
                self.assertIn(name, check_kinds.KNOWN_CHECK_OPERATORS)

    def test_every_assertion_of_the_contract_names_a_kind_that_has_a_real_branch(self):
        """All of them, not a sample: every check's kind must be judgeable.

        A check whose kind has no branch would be judged by the nearest branch
        above it, and the report would say "passed" about a different question.
        """
        branches = BranchCoverageTests().branch_set()
        for path in (WORKFLOW, SCENARIOS):
            for where, check in all_checks(contract(path)):
                name = check_kinds.validate_check(check, f"{where} check")
                self.assertIn(name, branches,
                              f"the check at {where} names the kind {name}, which the runner "
                              "has no evaluation branch for")

    def test_every_assertion_declares_exactly_the_allowed_keys(self):
        """``{path, intent}`` plus one registered kind -- and only that kind's own keys.

        Nothing else may ride along: an extra key is either a kind the contract
        does not declare (and then it was ignored) or a typo that was read as
        part of the expectation.  The only extras a kind may carry are the ones
        the kind itself declares (``id_field`` for the two id-set kinds, and
        only when the check actually uses it).
        """
        for path in (WORKFLOW, SCENARIOS):
            for where, check in all_checks(contract(path)):
                name = check_kinds.validate_check(check, f"{where} check")
                record = check_kinds.kind_of(name)
                required = {"path", "intent", name}
                allowed = required | set(record.auxiliary_keys)
                self.assertTrue(required <= set(check),
                                f"the check at {where} does not declare {sorted(required - set(check))}")
                self.assertTrue(set(check) <= allowed,
                                f"the check at {where} carries keys the kind {name} does "
                                f"not declare: {sorted(set(check) - allowed)}")

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


class BranchCoverageTests(unittest.TestCase):
    """Every kind has an evaluation branch, and only the vocabulary's kinds have one."""

    #: A branch is written as ``operator == "<kind>"``; the set of those names is
    #: the set of kinds the runner can actually judge.  Only the two dispatchers
    #: are read, so a kind mentioned in a comment or a test is not counted as a
    #: branch.
    BRANCH = re.compile(r'operator == "([a-z_]+)"')
    DISPATCHERS = ("    def _evaluate_checks(", "    def _judge_domain_check(")

    def dispatcher_source(self) -> str:
        source = RUNNER_SOURCE.read_text(encoding="utf-8")
        pieces: list[str] = []
        for marker in self.DISPATCHERS:
            start = source.index(marker)
            pieces.append(source[start:start + 8000])
        return "\n".join(pieces)

    def branch_set(self) -> set[str]:
        return set(self.BRANCH.findall(self.dispatcher_source()))

    def test_the_vocabulary_and_the_evaluation_branches_are_the_same_set(self):
        branches = self.branch_set()
        vocabulary = set(check_kinds.KNOWN_CHECK_OPERATORS)
        self.assertEqual(branches - vocabulary, set(),
                         "the runner judges a kind the vocabulary does not declare")
        self.assertEqual(vocabulary - branches, set(),
                         "the vocabulary declares a kind the runner has no branch for")

    def test_every_kind_of_the_vocabulary_reaches_a_real_branch(self):
        """Every kind is judged, not merely mentioned.

        The table above reads the dispatchers; this one *runs* them.  A kind with
        no branch is refused by the runner, so a probe that comes back with a
        verdict proves the kind reached a branch -- and a kind nobody implemented
        cannot pass this test quietly.
        """
        for kind in check_kinds.KNOWN_CHECK_OPERATORS:
            with self.subTest(kind=kind):
                verdicts = self.judge(kind)
                self.assertTrue(verdicts,
                                f"the kind {kind} produced no verdict; the runner refused it")

    def test_the_probe_table_covers_the_vocabulary_exactly(self):
        """Extending the vocabulary without a probe must turn this guard red."""
        self.assertEqual(set(PROBES), set(check_kinds.KNOWN_CHECK_OPERATORS),
                         "the probe table and the vocabulary disagree: a kind was added, "
                         "removed or renamed without its probe following it")

    def judge(self, kind: str) -> list[dict]:
        """Judge one probe of one kind and return the verdicts the run produced."""
        check, payload = PROBES[kind]
        check = {**check, "intent": f"探针：{kind}"}
        predecessor = {
            "id": "SYN-01", "operation_id": "createProject", "expected_http": 201,
            "request": {"path_params": {}, "headers": {}, "json": {}},
            "capture": {}, "checks": []}
        probe = {
            "id": "SYN-02", "operation_id": "getProject", "expected_http": 200,
            "request": {"path_params": {"projectId": "p-1"}, "headers": {}, "json": None},
            "capture": {}, "checks": [check]}
        if kind == "readback_equals":
            # A read-back assertion is only meaningful on a step that reads after a write.
            probe["readback_of"] = "SYN-01"
        document = {"id": "GUARD", "steps": [predecessor, probe]}

        class _Response:
            headers = {"Content-Type": "application/json"}

            def __init__(self, body: bytes, status: int) -> None:
                self._body = body
                self.status = status

            def read(self, limit: int = 16 * 1024 * 1024) -> bytes:
                return self._body

            def close(self) -> None:
                pass

        answers = [(json.dumps({"data": {"id": "p-1", "x": "v", "y": "v", "items": [{"id": "a"}],
                                         "others": [{"id": "s-1"}], "run_mode": "real",
                                         "statement": "s"}}).encode("utf-8"), 201),
                   (json.dumps(payload).encode("utf-8"), 200)]

        def opener(request, timeout):
            body, status = answers.pop(0)
            return _Response(body, status)

        runner = ScenarioRunner(contract(OPENAPI), spec_from_document(document, "guard specification"),
                                base_url="http://127.0.0.1:4010/api/v1/lingdoc", opener=opener)
        runner.run()
        return runner.report("completed").get("checks", [])

    def _judged(self, check: dict) -> list[dict]:
        """Judge one check that is not in the probe table (the injected ones)."""
        document = {"id": "GUARD", "steps": [{
            "id": "SYN-01", "operation_id": "getProject", "expected_http": 200,
            "request": {"path_params": {"projectId": "p-1"}, "headers": {}, "json": None},
            "capture": {}, "checks": [{**check, "intent": check.get("intent", "注入的算子")}]}]}

        class _Response:
            status = 200
            headers = {"Content-Type": "application/json"}

            def read(self, limit: int = 16 * 1024 * 1024) -> bytes:
                return json.dumps({"data": {"x": "v"}}).encode("utf-8")

            def close(self) -> None:
                pass

        runner = ScenarioRunner(contract(OPENAPI), spec_from_document(document, "guard specification"),
                                base_url="http://127.0.0.1:4010/api/v1/lingdoc",
                                opener=lambda request, timeout: _Response())
        runner.run()
        return runner.report("completed").get("checks", [])

    def test_a_domain_kind_without_a_branch_is_refused_instead_of_silently_judged(self):
        # The defect this closes: a domain kind nobody handled used to be judged as
        # non_empty, and the report said "passed" about a different question.
        kind = check_kinds.CheckKind("a_kind_without_a_branch", "domain", True,
                                      lambda value, at: None)
        with patch.object(check_kinds, "KNOWN_CHECK_OPERATORS",
                          check_kinds.KNOWN_CHECK_OPERATORS + (kind.name,)), \
             patch.dict(check_kinds.CHECK_KINDS, {kind.name: kind}, clear=False), \
             patch.object(sys.modules[run_f01.__name__], "DOMAIN_CHECK_OPERATORS",
                          run_f01.DOMAIN_CHECK_OPERATORS + (kind.name,)):
            with self.assertRaisesRegex(WorkflowError, "no evaluation branch"):
                self._judged({"path": "/data/x", kind.name: True, "intent": "没有分支的 kind"})

    def test_a_structural_kind_without_a_branch_is_refused_instead_of_silently_judged(self):
        # The same defect on the structural side: the kinds used to end in
        # "else: one_of".
        kind = check_kinds.CheckKind("a_structural_kind_without_a_branch", "structural", False,
                                      lambda value, at: None)
        with patch.object(check_kinds, "KNOWN_CHECK_OPERATORS",
                          check_kinds.KNOWN_CHECK_OPERATORS + (kind.name,)), \
             patch.dict(check_kinds.CHECK_KINDS, {kind.name: kind}, clear=False):
            with self.assertRaisesRegex(WorkflowError, "no evaluation branch"):
                self._judged({"path": "/data/x", kind.name: True, "intent": "没有分支的 kind"})


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
