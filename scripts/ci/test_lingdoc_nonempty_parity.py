"""Guard: the contract's static validator and the runner judge emptiness the same way (#626).

What this file guards
---------------------
``docs/08-本轮实施方案/contracts/validate_artifacts.py`` says a value is empty
when it is not one of ``None``/``''``/``[]``/``{}``; the runner
(``scripts/lingdoc_mock/run_f01.py``'s ``ScenarioRunner._is_marked``) says a value
is empty when it is ``None``, ``False``, a whitespace-only string, or an empty
collection.  The two answers disagreed about a whitespace-only string: the
validator let it through, the runner turned the same published example red.  Two
judges that answer one sample differently are not two judges.

The validator restates the runner's rule instead of importing it -- a contract
check has to be able to run without any implementation beside it -- so the two
restatements have to be tied together.  That is what this file does:

*The two rules are one rule.*  The validator's own ``is_marked`` is compared with
the runner's ``_is_marked`` on a probe set that covers every way the two could
disagree: whitespace-only strings, ``False``, ``0``, empty collections.

*The validator really turns red on such an example.*  A scratch copy of the
contract -- outside the repository -- carries a published example whose field is
a whitespace-only string, and the validator has to refuse it.

*The runner refuses the same answer.*  The same example is put to the runner, so
the two verdicts are about one answer and not about two fixtures.

*The guard is not vacuous.*  A weakened copy of the validator -- the old
judgment, put back -- lets the whitespace-only example through, so the guard
above can turn red.
"""
from __future__ import annotations

import ast
import copy
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
VALIDATOR = CONTRACTS / "validate_artifacts.py"
WORKFLOW = CONTRACTS / "workflow.json"
OPENAPI = CONTRACTS / "openapi.json"

#: The judgment the validator used before #626: a whitespace-only string was
#: "not empty" for the validator and "empty" for the runner.
OLD_JUDGMENT = "(entry.get(name) not in (None,'',[],{}))"
#: The judgment the validator uses now: the runner's own rule.
NEW_JUDGMENT = "is_marked(entry.get(name))"

#: The message the validator raises when it refuses an entry that leaves a field
#: empty.  The guard reads it so that a red validator is red *for this reason*:
#: the same sample breaks another rule too, and a red validator that says
#: something else would not prove that this judgment is the one that spoke.
REFUSAL = "an entry leaves reason empty"

#: Every value the two rules could answer differently on.
PROBES = (
    None, False, True, 0, 1, 0.0,
    "", "   ", "\t\n ", "理由",
    [], ["a"], {}, {"k": "v"}, (), set(),
)


def validator_is_marked():
    """The validator's own emptiness rule, read out of its source.

    The validator is a script, not a module: importing it would run it.  Its
    ``is_marked`` is therefore read out of the parsed source and executed on its
    own, so what the guard compares is the rule the validator really carries.
    """
    tree = ast.parse(VALIDATOR.read_text(encoding="utf-8"))
    node = next((item for item in tree.body
                 if isinstance(item, ast.FunctionDef) and item.name == "is_marked"), None)
    if node is None:
        raise AssertionError("the validator no longer carries an is_marked rule")
    namespace: dict = {}
    exec(compile(ast.Module(body=[node], type_ignores=[]), str(VALIDATOR), "exec"), namespace)
    return namespace["is_marked"]


def step_of(document: dict, step_id: str) -> dict:
    return next(step for step in document["steps"] if step["id"] == step_id)


def contract() -> dict:
    return json.loads(WORKFLOW.read_text(encoding="utf-8"))


def mapping_check() -> dict:
    """F01-16's assertion about the decisions, as the contract states it."""
    checks = [check for check in step_of(contract(), "F01-16")["checks"]
              if "one_to_one" in check]
    if len(checks) != 1:
        raise AssertionError("F01-16 must declare exactly one mapping assertion")
    return checks[0]


def blank_the_reason(document: dict) -> None:
    """Leave one decision's reason empty -- with whitespace, which is not empty."""
    decisions = step_of(document, "F01-16")["reference_response"]["data"]["review_decisions"]
    decisions[0]["reason"] = "   "


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


def runner_verdict(reason: str) -> str:
    """What the runner answers when one decision leaves its reason blank."""
    workflow = contract()
    items = step_of(workflow, "F01-13")["reference_response"]["data"]["review_items"]
    decisions = copy.deepcopy(
        step_of(workflow, "F01-16")["reference_response"]["data"]["review_decisions"])
    decisions[0]["reason"] = reason
    answers = [(json.dumps({"data": {"review_items": items}}).encode("utf-8"), 201),
               (json.dumps({"data": {"review_decisions": decisions}}).encode("utf-8"), 201)]

    def opener(request, timeout):
        body, status = answers.pop(0)
        return FakeResponse(body, status)

    document = {"id": "PARITY-GUARD", "steps": [
        {"id": "F01-13", "operation_id": "saveChapter", "expected_http": 201,
         "request": {"path_params": {"projectId": "p-1", "chapterId": "c-1"},
                     "headers": {}, "json": {}},
         "capture": {}, "checks": [], "assertions": []},
        {"id": "F01-16", "operation_id": "confirmChapter", "expected_http": 201,
         "request": {"path_params": {"projectId": "p-1", "chapterId": "c-1"},
                     "headers": {}, "json": {}},
         "capture": {}, "checks": [mapping_check()], "assertions": []}]}
    openapi = json.loads(OPENAPI.read_text(encoding="utf-8"))
    spec = run_f01.spec_from_document(document, "parity guard")
    runner = run_f01.ScenarioRunner(openapi, spec,
                                    base_url="http://127.0.0.1:4010/api/v1/lingdoc",
                                    opener=opener)
    runner.run(stop_on_mismatch=False)
    recorded = runner.report("completed").get("checks", [])
    if not recorded:
        raise AssertionError("the run recorded no verdict at all")
    return recorded[0]["status"]


def run_validator(mutate=None, weaken: bool = False) -> subprocess.CompletedProcess:
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
        if weaken:
            validator = scratch / "validate_artifacts.py"
            source = validator.read_text(encoding="utf-8")
            if NEW_JUDGMENT not in source:
                raise AssertionError("the validator does not carry the judgment this weakens")
            validator.write_text(source.replace(NEW_JUDGMENT, OLD_JUDGMENT), encoding="utf-8")
        # Never mistake the committed report for a result from this run.
        (scratch / "validation-result.json").unlink(missing_ok=True)
        return subprocess.run(
            [sys.executable, "-X", "utf8", str(scratch / "validate_artifacts.py"),
             "--source-root", str(REPO_ROOT)],
            capture_output=True, text=True, encoding="utf-8", errors="replace")


class TheTwoRulesAreOneRuleTest(unittest.TestCase):
    """The validator's emptiness rule is the runner's emptiness rule."""

    def test_the_two_judges_answer_the_same_on_every_probe(self):
        validator = validator_is_marked()
        runner = run_f01.ScenarioRunner._is_marked
        for probe in PROBES:
            with self.subTest(probe=repr(probe)):
                self.assertEqual(validator(probe), runner(probe),
                                 f"the validator and the runner disagree about {probe!r}")

    def test_a_whitespace_only_string_is_empty_for_both(self):
        validator = validator_is_marked()
        self.assertFalse(validator("   "))
        self.assertFalse(run_f01.ScenarioRunner._is_marked("   "))

    def test_the_validator_no_longer_carries_the_old_judgment(self):
        source = VALIDATOR.read_text(encoding="utf-8")
        self.assertNotIn(OLD_JUDGMENT, source,
                         "the validator still judges emptiness the way the runner does not")
        self.assertIn(NEW_JUDGMENT, source,
                      "the validator no longer judges emptiness the way the runner does")


class TheValidatorTurnsRedTest(unittest.TestCase):
    """The static validator refuses a published example the runner refuses."""

    def test_the_unchanged_contract_still_validates(self):
        result = run_validator()
        self.assertEqual(result.returncode, 0, result.stderr[-2000:])

    def test_a_whitespace_only_reason_makes_the_validator_refuse(self):
        result = run_validator(blank_the_reason)
        self.assertNotEqual(result.returncode, 0,
                            "the validator accepted a published example that leaves a "
                            "field empty")
        self.assertIn(REFUSAL, result.stderr,
                      "the validator refused the example for another reason, so it does "
                      "not prove that this judgment spoke")

    def test_the_runner_refuses_the_same_answer(self):
        self.assertEqual(runner_verdict("   "), "failed",
                         "the runner has to refuse the answer the validator refuses")

    def test_the_runner_accepts_the_reason_the_contract_publishes(self):
        published = step_of(contract(), "F01-16")["reference_response"]["data"]
        self.assertEqual(runner_verdict(published["review_decisions"][0]["reason"]), "passed",
                         "the runner has to accept the contract's own answer")


class TheGuardIsNotVacuousTest(unittest.TestCase):
    """A guard that cannot fail proves nothing: weaken the validator and watch it."""

    def test_the_old_judgment_lets_the_whitespace_example_through(self):
        weakened = run_validator(blank_the_reason, weaken=True)
        self.assertNotIn(REFUSAL, weakened.stderr,
                         "the weakened validator did not let the whitespace-only example "
                         "through: the guard above cannot turn red, so it proves nothing")


if __name__ == "__main__":
    unittest.main()
