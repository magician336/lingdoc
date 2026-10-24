"""Guard tests for the contract's per-review-item decision mapping (#621).

What this file guards
---------------------
F01-16 used to say "the first decision names the first review item".  That
assumption is false: the provider sorts the decisions by ``review_item_id``,
and the first review item is the one the model wrote first.  The assertion now
says what it means: **the decisions answer the review items one to one, in any
order, and every decision says what it does** (a retained warning, with a
reason).

Five things have to stay true, and each of them is a way the fix could
silently become the defect it fixed:

*The contract states the mapping, not a position.*  The assertion is about two
collections, not about the first entry of one of them.  A position is not a
mapping: it says nothing about the entries the position hides.

*The order does not matter.*  The provider's order is not the contract's
business.  A contract that depended on it would fail for a reason that has
nothing to do with the decision -- and it would fail on a correct answer.

*A mapping is a mapping.*  A decision that names an item nobody waits for, an
item nobody decided, and an item decided twice while another one was not
decided at all are three different defects.  None of them may pass, and none
of them may be answered by "the counts happen to be equal".

*Every decision says what it does.*  "Kept as a warning" and "kept, and here
is why" are two claims; the assertion makes both of them.

*The guard is not vacuous.*  A weakened implementation is copied into a
temporary file and watched letting the broken mappings through: a guard that
cannot fail proves nothing.

Where this file lives
---------------------
It sits in ``scripts/ci/`` because it is a guard test, and the review workflow
collects the guard tests from there (``python -m unittest discover -s
scripts/ci -p 'test_lingdoc_*.py'``).
"""
from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT))

from scripts.lingdoc_mock import run_f01  # noqa: E402

CONTRACTS = REPO_ROOT / "docs" / "08-本轮实施方案" / "contracts"
WORKFLOW = CONTRACTS / "workflow.json"
OPENAPI = CONTRACTS / "openapi.json"
VALIDATOR = CONTRACTS / "validate_artifacts.py"
RUNNER = REPO_ROOT / "scripts" / "lingdoc_mock" / "run_f01.py"

#: The reason the contract sends with every decision, and the disposition it
#: sends: a retained warning does not block the release, but it has to be
#: listed with the file.
REASON = "仅作内部演示，保留该项并随文件明确列出。"
DISPOSITION = "retained_warning"

#: The review items the model wrote, in the order it wrote them.  The first
#: one is deliberately **not** the smallest id: that is what the old
#: assertion got wrong.
REVIEW_ITEMS = [
    {"id": "bc2cf1f4-0f3a-4c21-9a77-0d5f1b2c3d41", "statement": "待核事项一"},
    {"id": "9a569da8-1a44-4d32-8b88-1e6a2c3d4e52", "statement": "待核事项二"},
    {"id": "c56757ab-2b55-4e43-9c99-2f7b3d4e5f63", "statement": "待核事项三"},
]

#: The same decisions, in the order the provider returns them: sorted by
#: ``review_item_id``.  The first decision is therefore **not** the first
#: review item.
REVIEW_DECISIONS = [
    {"review_item_id": "9a569da8-1a44-4d32-8b88-1e6a2c3d4e52",
     "disposition": DISPOSITION, "reason": REASON},
    {"review_item_id": "bc2cf1f4-0f3a-4c21-9a77-0d5f1b2c3d41",
     "disposition": DISPOSITION, "reason": REASON},
    {"review_item_id": "c56757ab-2b55-4e43-9c99-2f7b3d4e5f63",
     "disposition": DISPOSITION, "reason": REASON},
]


class FakeResponse:
    """One provider answer, for the runner to read."""

    def __init__(self, payload: bytes, status: int = 200) -> None:
        self.payload = payload
        self.status = status
        self.headers = {"Content-Type": "application/json"}

    def read(self, limit: int = 16 * 1024 * 1024) -> bytes:
        return self.payload[:limit]

    def close(self) -> None:
        pass


def contract() -> dict:
    return json.loads(WORKFLOW.read_text(encoding="utf-8"))


def step_of(document: dict, step_id: str) -> dict:
    return next(step for step in document["steps"] if step["id"] == step_id)


def mapping_check() -> dict:
    """F01-16's assertion about the decisions, as the contract states it."""
    checks = [check for check in step_of(contract(), "F01-16")["checks"]
              if "one_to_one" in check]
    if len(checks) != 1:
        raise AssertionError("F01-16 must declare exactly one mapping assertion")
    return checks[0]


def mapping_document(check: dict) -> dict:
    """Two steps: the chapter that carries the review items, and the confirmation."""
    return {"id": "MAPPING-GUARD", "steps": [
        {"id": "F01-13", "operation_id": "saveChapter", "expected_http": 201,
         "request": {"path_params": {"projectId": "p-1", "chapterId": "c-1"},
                     "headers": {}, "json": {}},
         "capture": {}, "checks": [], "assertions": []},
        {"id": "F01-16", "operation_id": "confirmChapter", "expected_http": 201,
         "request": {"path_params": {"projectId": "p-1", "chapterId": "c-1"},
                     "headers": {}, "json": {}},
         "capture": {}, "checks": [check], "assertions": []}]}


def mapping_verdict(module, review_items: list[dict] | None = None,
                     review_decisions: list[dict] | None = None,
                     check: dict | None = None) -> str:
    """Judge the contract's mapping assertion against two answers.

    ``module`` is the runner that judges it: the repository's own runner, or a
    weakened copy of it (see ``TheGuardIsNotVacuousTest``).
    """
    items = REVIEW_ITEMS if review_items is None else review_items
    decisions = REVIEW_DECISIONS if review_decisions is None else review_decisions
    assertion = mapping_check() if check is None else check
    answers = [(json.dumps({"data": {"review_items": items}}).encode("utf-8"), 201),
               (json.dumps({"data": {"review_decisions": decisions}}).encode("utf-8"), 201)]

    def opener(request, timeout):
        body, status = answers.pop(0)
        return FakeResponse(body, status)

    openapi = json.loads(OPENAPI.read_text(encoding="utf-8"))
    spec = module.spec_from_document(mapping_document(assertion), "mapping guard")
    runner = module.ScenarioRunner(openapi, spec,
                                   base_url="http://127.0.0.1:4010/api/v1/lingdoc",
                                   opener=opener)
    runner.run(stop_on_mismatch=False)
    recorded = runner.report("completed").get("checks", [])
    if not recorded:
        raise AssertionError("the run recorded no verdict at all")
    return recorded[0]["status"]


def run_validator(mutate=None) -> subprocess.CompletedProcess:
    """Run the contract's static validator over a scratch copy of the contract.

    The scratch copy is what the review workflow does before it runs the
    validator, and it is the shape that proves the validator finds the check
    vocabulary under ``--source-root`` instead of next to itself.
    """
    with tempfile.TemporaryDirectory() as directory:
        scratch = Path(directory) / "contracts"
        shutil.copytree(CONTRACTS, scratch)
        document = json.loads((scratch / "workflow.json").read_text(encoding="utf-8"))
        if mutate is not None:
            mutate(document)
        (scratch / "workflow.json").write_text(
            json.dumps(document, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        # Never mistake the committed report for a result from this run.
        (scratch / "validation-result.json").unlink(missing_ok=True)
        return subprocess.run(
            [sys.executable, "-X", "utf8", str(scratch / "validate_artifacts.py"),
             "--source-root", str(REPO_ROOT)],
            capture_output=True, text=True, encoding="utf-8", errors="replace")


class TheContractStatesTheMappingTest(unittest.TestCase):
    """The contract states the mapping, not a position."""

    def test_f01_16_states_the_mapping_over_both_collections(self):
        check = mapping_check()
        self.assertEqual(check["path"], "/data/review_decisions")
        source = check["one_to_one"]
        self.assertEqual(source["from_step"], "F01-13")
        self.assertEqual(source["path"], "/data/review_items")
        self.assertEqual(source["id_field"], "review_item_id")
        self.assertEqual(source["other_id_field"], "id")

    def test_f01_16_demands_a_retained_warning_with_a_reason(self):
        source = mapping_check()["one_to_one"]
        self.assertEqual(source["entry_equals"], {"disposition": DISPOSITION})
        self.assertEqual(source["entry_non_empty"], ["reason"])

    def test_the_old_positional_assertion_is_gone(self):
        checks = step_of(contract(), "F01-16")["checks"]
        positional = [check for check in checks
                      if check.get("path", "").startswith("/data/review_decisions/")]
        self.assertEqual(positional, [],
                         "a position is not a mapping: the first decision says nothing "
                         "about the decisions behind it")
        self.assertNotIn("{{review_id}}", json.dumps(checks, ensure_ascii=False),
                         "the first review item is not the first decision")

    def test_the_contract_still_declares_one_check_for_f01_16(self):
        # The check was rewritten, not added and not removed: the contract's
        # own account of its expectations must not move.
        self.assertEqual(len(step_of(contract(), "F01-16")["checks"]), 3)


class TheMappingHoldsTest(unittest.TestCase):
    """A correct answer passes, whatever order it comes back in."""

    def test_a_correct_mapping_passes_although_the_order_differs(self):
        self.assertNotEqual([item["id"] for item in REVIEW_ITEMS],
                            [decision["review_item_id"] for decision in REVIEW_DECISIONS],
                            "the fixture must really answer in another order")
        self.assertEqual(mapping_verdict(run_f01), "passed")

    def test_the_contracts_own_reference_responses_satisfy_the_mapping(self):
        workflow = contract()
        items = step_of(workflow, "F01-13")["reference_response"]["data"]["review_items"]
        decisions = step_of(workflow, "F01-16")["reference_response"]["data"]["review_decisions"]
        self.assertEqual(mapping_verdict(run_f01, items, decisions), "passed")


class TheMappingFailsTest(unittest.TestCase):
    """Every way the mapping can be wrong has to turn the check red."""

    def test_a_decision_that_names_an_item_nobody_waits_for_fails(self):
        decisions = [dict(decision) for decision in REVIEW_DECISIONS]
        decisions[0] = dict(decisions[0], review_item_id="00000000-0000-4000-8000-000000000000")
        self.assertEqual(mapping_verdict(run_f01, REVIEW_ITEMS, decisions), "failed")

    def test_an_item_nobody_decided_fails(self):
        decisions = [dict(decision) for decision in REVIEW_DECISIONS[:2]]
        self.assertEqual(mapping_verdict(run_f01, REVIEW_ITEMS, decisions), "failed")

    def test_a_repeated_decision_cannot_stand_in_for_a_missing_one(self):
        # The same number of entries on both sides is not a mapping: one item is
        # left undecided and another one is decided twice.
        decisions = [dict(REVIEW_DECISIONS[0]), dict(REVIEW_DECISIONS[0]),
                     dict(REVIEW_DECISIONS[1])]
        self.assertEqual(len(decisions), len(REVIEW_ITEMS))
        self.assertEqual(mapping_verdict(run_f01, REVIEW_ITEMS, decisions), "failed")

    def test_a_repeated_review_item_cannot_be_decided_once_and_count_as_mapped(self):
        items = [REVIEW_ITEMS[0], dict(REVIEW_ITEMS[0]), REVIEW_ITEMS[1]]
        decisions = [dict(REVIEW_DECISIONS[0]), dict(REVIEW_DECISIONS[1])]
        self.assertEqual(mapping_verdict(run_f01, items, decisions), "failed")

    def test_a_decision_that_says_something_else_fails(self):
        decisions = [dict(decision) for decision in REVIEW_DECISIONS]
        decisions[1] = dict(decisions[1], disposition="dropped")
        self.assertEqual(mapping_verdict(run_f01, REVIEW_ITEMS, decisions), "failed")

    def test_a_decision_that_leaves_the_reason_empty_fails(self):
        decisions = [dict(decision) for decision in REVIEW_DECISIONS]
        decisions[2] = dict(decisions[2], reason="   ")
        self.assertEqual(mapping_verdict(run_f01, REVIEW_ITEMS, decisions), "failed")

    def test_an_answer_with_no_decision_at_all_fails(self):
        self.assertEqual(mapping_verdict(run_f01, REVIEW_ITEMS, []), "failed")


class TheOldAssertionWasTheDefectTest(unittest.TestCase):
    """The old assertion failed on a correct answer: that was the defect."""

    def test_the_old_assertion_failed_on_this_correct_answer(self):
        old = {"path": "/data/review_decisions/0/review_item_id",
               "equals": REVIEW_ITEMS[0]["id"],
               "intent": "待核事项有明确处置（保留并说明原因）"}
        self.assertEqual(mapping_verdict(run_f01, REVIEW_ITEMS, REVIEW_DECISIONS, old), "failed")

    def test_the_new_assertion_holds_on_the_same_answer(self):
        self.assertEqual(mapping_verdict(run_f01, REVIEW_ITEMS, REVIEW_DECISIONS), "passed")


class TheValidatorTiesTheMappingTest(unittest.TestCase):
    """The static validator ties the mapping to the published examples."""

    def test_the_validator_accepts_the_contract_as_it_stands(self):
        result = run_validator()
        self.assertEqual(result.returncode, 0, result.stderr[-2000:])

    def test_the_validator_refuses_examples_that_break_the_mapping(self):
        def leave_one_item_undecided(document: dict) -> None:
            items = step_of(document, "F01-13")["reference_response"]["data"]["review_items"]
            items.append({"id": "review-2", "statement": "还有一条",
                          "origin_candidate_id": "candidate-demo"})

        result = run_validator(leave_one_item_undecided)
        self.assertNotEqual(result.returncode, 0,
                            "the validator accepted examples whose decisions do not "
                            "answer their review items one to one")
        self.assertIn("one to one", result.stderr)


class TheGuardIsNotVacuousTest(unittest.TestCase):
    """A guard that cannot fail proves nothing: weaken the runner and watch it."""

    #: Each mutation replaces one line of the runner's evaluation branch.  The
    #: weakened copy is imported from a temporary file; the repository's own
    #: runner is never touched.  Each entry says what the repository's runner
    #: answers and what the weakened one answers for the same answer.
    MUTATIONS = (
        # The order becomes the contract's business again: the correct answer
        # now fails.
        ("the order decides", "and set(ids) == set(other_ids)", "and ids == other_ids",
         REVIEW_ITEMS, REVIEW_DECISIONS, "passed", "failed"),
        # A subset is enough: an item nobody decided may pass.
        ("a subset is enough", "and set(ids) == set(other_ids)",
         "and set(ids) <= set(other_ids)", REVIEW_ITEMS, REVIEW_DECISIONS[:2], "failed", "passed"),
        # A repeated id is allowed to stand in for a missing one.
        ("a repeated id is allowed",
         "and len(ids) == len(set(ids))\n                        and len(other_ids) == len(set(other_ids))",
         "and True\n                        and True",
         [REVIEW_ITEMS[0], dict(REVIEW_ITEMS[0]), REVIEW_ITEMS[1]],
         [dict(REVIEW_DECISIONS[0]), dict(REVIEW_DECISIONS[1])], "failed", "passed"),
    )

    def weakened_runner(self, source: str, replacement: str):
        """A copy of the runner with one line replaced, imported under another name."""
        original = RUNNER.read_text(encoding="utf-8")
        if source not in original:
            raise AssertionError("the runner does not carry the line this mutation replaces")
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "weakened_run_f01.py"
            path.write_text(original.replace(source, replacement), encoding="utf-8")
            spec = importlib.util.spec_from_file_location("weakened_run_f01", path)
            module = importlib.util.module_from_spec(spec)
            # A dataclass asks the module registry for its own module while it
            # is being defined, so the copy has to be registered before it runs.
            sys.modules[spec.name] = module
            try:
                spec.loader.exec_module(module)
            finally:
                sys.modules.pop(spec.name, None)
            return module

    def test_each_weakened_implementation_lets_a_broken_mapping_through(self):
        for (label, source, replacement, items, decisions,
             honest, weakened) in self.MUTATIONS:
            with self.subTest(label):
                self.assertEqual(
                    mapping_verdict(run_f01, items, decisions), honest,
                    "the repository's runner has to hold this line")
                self.assertEqual(
                    mapping_verdict(self.weakened_runner(source, replacement),
                                    items, decisions), weakened,
                    "the weakened runner did not let the broken mapping through: "
                    "the guard above cannot turn red, so it proves nothing")


if __name__ == "__main__":
    unittest.main()
