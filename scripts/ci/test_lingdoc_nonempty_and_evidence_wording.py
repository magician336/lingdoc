"""Guard: the contract validator and the runner judge emptiness the same way (#639).

What this file guards
---------------------

``docs/08-本轮实施方案/contracts/validate_artifacts.py`` judges emptiness in
three places -- ``marked_or_absent``, ``non_empty`` and ``all_non_empty``.
Those three places carried their own copy of the rule -- "empty means
``None``, ``''``, ``[]`` or ``{}``" -- while the runner
(``scripts/lingdoc_mock/run_f01.py``'s ``ScenarioRunner._is_marked``) says a
value is empty when it is ``None``, ``False``, a whitespace-only string, or an
empty collection.  The two rules answered five kinds of value differently: a
whitespace-only string, a tab-and-newline string, a ``False``, an empty tuple
and an empty set.  A contract the validator and the runner judge differently
is a contract nobody can trust.

The validator already carried the runner's rule once (``is_marked``, added by
#626).  The three judgment sites now call that one rule instead of carrying a
copy of it.  This file ties **the three call sites themselves** to the runner,
value by value -- so a site that drifts back turns red -- and proves it end to
end by running the real validator over a scratch copy of the contract.

How these tests run
-------------------

They run offline: no service, no browser, no network.  The validator is a
script, so importing it would run it: its judgment functions are therefore
read out of the parsed source and executed on their own, which is also what
lets the guard hold a *weakened* copy of the validator beside the real one.
"""
from __future__ import annotations

import ast
import json
from pathlib import Path
import re
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

#: The judgment the validator's three sites used before #639.  It is older and
#: weaker than the runner's rule: a whitespace-only string, a tab-and-newline
#: string, a ``False``, an empty tuple and an empty set all pass it as "not
#: empty", while the runner calls every one of them empty.
OLD_JUDGMENT = "not in (None,'',[],{})"

#: The three places in the validator that judge emptiness, and the name of the
#: variable each of them judges.
JUDGMENT_SITES = (
    ("marked_or_absent", "marker"),
    ("non_empty", "actual"),
    ("all_non_empty", "entry"),
)

#: Every value the two rules could answer differently on, plus the values they
#: agree on anyway -- a guard that only watches the disagreements cannot say
#: that the two judges are one judge.
#:
#: Each entry is ``(label, value, present)``.  ``present`` is False for the
#: one case where the field is not there at all: the runner reads a field it
#: cannot find as ``None``, so the runner's answer for that case is the answer
#: for ``None``.
PROBES = (
    ("a missing field", None, False),
    ("null", None, True),
    ("an empty string", "", True),
    ("a whitespace-only string", "   ", True),
    ("a tab-and-newline string", "\t\n", True),
    ("the string zero", "0", True),
    ("the number zero", 0, True),
    ("the float zero", 0.0, True),
    ("false", False, True),
    ("true", True, True),
    ("the number one", 1, True),
    ("an empty list", [], True),
    ("a list with one entry", ["a"], True),
    ("an empty object", {}, True),
    ("an object with one entry", {"k": "v"}, True),
    ("an empty tuple", (), True),
    ("an empty set", set(), True),
    ("a non-empty string", "理由", True),
    ("a non-empty tuple", (1,), True),
)

#: The refusal the validator raises for each of the three judgments.  The
#: guard reads it so that a red validator is red *for this reason*: the same
#: scratch copy could break another rule too, and a red validator that says
#: something else would not prove that this judgment spoke.
REFUSALS = {
    "marked_or_absent": "the example marks the item with nothing",
    "non_empty": "the example holds an empty value",
    "all_non_empty": "the example holds an empty entry",
}


# ---------------------------------------------------------------------------
# reading the validator's own judgment out of its source
# ---------------------------------------------------------------------------

def validator_functions(source: str) -> dict:
    """The validator's judgment functions, executed on their own.

    The validator is a script, not a module: importing it would run it.  The
    functions that decide the three judgments are therefore read out of the
    parsed source and executed on their own, so what the guard compares is the
    judgment the validator really carries -- not a copy of it, and not a
    re-implementation of it.
    """
    tree = ast.parse(source)
    wanted = ("lookup", "has_variable", "ids_of", "ids_in_order", "is_marked", "tie_expectation")
    nodes = [node for node in tree.body
             if isinstance(node, ast.FunctionDef) and node.name in wanted]
    found = {node.name for node in nodes}
    missing = [name for name in wanted if name not in found]
    if missing:
        raise AssertionError("the validator no longer carries " + ", ".join(missing))
    namespace: dict = {"re": re}
    exec(compile(ast.Module(body=nodes, type_ignores=[]), str(VALIDATOR), "exec"), namespace)
    # ``has_variable`` reads one module-level pattern; it is read from the
    # source as well, so the guard does not carry a second copy of it.
    for node in tree.body:
        if not isinstance(node, ast.Assign):
            continue
        if not any(isinstance(target, ast.Name) and target.id == "VARIABLE"
                   for target in node.targets):
            continue
        namespace["VARIABLE"] = eval(compile(ast.Expression(node.value), str(VALIDATOR), "eval"),
                                     {"re": re})
        break
    return namespace


def judgment_assert(source: str, kind: str, variable: str) -> ast.Assert:
    """The assert the validator judges emptiness with at one site."""
    tree = ast.parse(source)
    function = next((node for node in tree.body
                     if isinstance(node, ast.FunctionDef) and node.name == "tie_expectation"), None)
    if function is None:
        raise AssertionError("the validator no longer carries a tie_expectation")
    branch = None
    for node in ast.walk(function):
        if not isinstance(node, ast.If) or not isinstance(node.test, ast.Compare):
            continue
        left = node.test.left
        if not (isinstance(left, ast.Name) and left.id == "kind"):
            continue
        for operator, comparator in zip(node.test.ops, node.test.comparators):
            if not isinstance(operator, ast.Eq):
                continue
            if isinstance(comparator, ast.Constant) and comparator.value == kind:
                branch = node
                break
        if branch is not None:
            break
    if branch is None:
        raise AssertionError(f"the validator no longer judges the kind {kind}")
    matching = [node for node in ast.walk(branch)
                if isinstance(node, ast.Assert)
                and variable in {child.id for child in ast.walk(node.test)
                                 if isinstance(child, ast.Name)}]
    if len(matching) != 1:
        raise AssertionError(f"the {kind} judgment does not read exactly one value "
                             f"({variable}); found {len(matching)} candidates")
    return matching[0]


def site_example(kind: str, value: object, present: bool) -> dict:
    """An example in which the judgment at one site is about ``value``."""
    if kind == "marked_or_absent":
        # The item is present; the marker inside it is the value being judged.
        return {"item": ({"statement": value} if present else {"id": "x"})}
    if kind == "non_empty":
        return ({"value": value} if present else {})
    if kind == "all_non_empty":
        # The collection holds one entry, and that entry is the value judged.
        return ({"items": [value]} if present else {})
    raise AssertionError(f"no example is known for the kind {kind}")


def site_check(kind: str) -> dict:
    """The contract's own shape of a check of one kind."""
    if kind == "marked_or_absent":
        return {"path": "/item", "marked_or_absent": "/statement"}
    if kind == "non_empty":
        return {"path": "/value", "non_empty": True}
    if kind == "all_non_empty":
        return {"path": "/items", "all_non_empty": True}
    raise AssertionError(f"no check is known for the kind {kind}")


def validator_accepts(namespace: dict, kind: str, value: object, present: bool) -> bool:
    """Whether the validator's own judgment lets this value through."""
    check = site_check(kind)
    example = site_example(kind, value, present)
    try:
        verdict = namespace["tie_expectation"](check, kind, example, "probe")
    except AssertionError:
        return False
    if verdict != "tied":
        raise AssertionError(f"the {kind} judgment left the example unjudged: {verdict}")
    return True


def weakened_validator(source: str) -> str:
    """The validator's three judgment sites put back the way they were before #639."""
    weakened = source
    for _, variable in JUDGMENT_SITES:
        weakened = weakened.replace(f"is_marked({variable})",
                                    f"{variable} " + OLD_JUDGMENT)
    return weakened


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


def flow_step(document: dict, step_id: str) -> dict:
    return next(step for step in document["steps"] if step["id"] == step_id)


def plant_marked_or_absent(document: dict, statement: str) -> None:
    """Put a value where the contract's own ``marked_or_absent`` judgment reads it."""
    step = flow_step(document, "F01-09")
    step["reference_response"]["data"]["review_items"][0]["statement"] = statement


def plant_non_empty(document: dict, locator: str) -> None:
    """Put a value where the contract's own ``non_empty`` judgment reads it."""
    step = flow_step(document, "F01-10")
    step["reference_response"]["data"]["locator"] = locator


def plant_all_non_empty(document: dict, entries: list[str]) -> None:
    """Put a list of values where an ``all_non_empty`` judgment reads them.

    The contract's own ``all_non_empty`` assertion is about asset versions, and
    the schema says an asset version is an object with two required fields --
    so it cannot hold a string to judge.  The scratch copy therefore gets one
    more assertion, about the contract's own array of permitted asset ids,
    which the schema says is an array of non-empty strings.
    """
    step = flow_step(document, "F01-19")
    frozen = step["reference_response"]["data"]["frozen_input"]
    frozen["policy_asset_ids"] = entries
    step["checks"].append({
        "path": "/data/frozen_input/policy_asset_ids",
        "all_non_empty": True,
        "intent": "守卫用的断言：这一列逐项都要有内容。",
    })


# ---------------------------------------------------------------------------
# the two judges answer the same question the same way
# ---------------------------------------------------------------------------

class TheValidatorAndTheRunnerAnswerTheSameTest(unittest.TestCase):
    """Every value the two judges could answer differently on."""

    def test_the_three_sites_and_the_runner_answer_every_probe_the_same_way(self):
        """The table: one value in, two judges, one answer."""
        namespace = validator_functions(VALIDATOR.read_text(encoding="utf-8"))
        runner = run_f01.ScenarioRunner._is_marked
        for kind, _ in JUDGMENT_SITES:
            for label, value, present in PROBES:
                with self.subTest(kind=kind, probe=label):
                    runner_value = value if present else None
                    self.assertEqual(
                        validator_accepts(namespace, kind, value, present),
                        runner(runner_value),
                        f"the validator and the runner disagree about {label!r} "
                        f"at the {kind} judgment")

    def test_the_three_sites_call_the_rules_own_function(self):
        """No site carries a copy of the rule any more."""
        source = VALIDATOR.read_text(encoding="utf-8")
        for kind, variable in JUDGMENT_SITES:
            with self.subTest(kind=kind):
                node = judgment_assert(source, kind, variable)
                self.assertIsInstance(node.test, ast.Call,
                                     f"the {kind} judgment is not a call any more")
                self.assertIsInstance(node.test.func, ast.Name)
                self.assertEqual(node.test.func.id, "is_marked",
                                 f"the {kind} judgment does not call the rule")
                self.assertEqual(len(node.test.args), 1)
                self.assertIsInstance(node.test.args[0], ast.Name)
                self.assertEqual(node.test.args[0].id, variable)

    def test_the_validator_no_longer_carries_the_old_judgment(self):
        """The old rule is gone from the file, not merely unused."""
        source = VALIDATOR.read_text(encoding="utf-8")
        self.assertNotIn(OLD_JUDGMENT, source,
                         "the validator still carries the judgment the runner does not make")

    def test_a_whitespace_only_string_is_empty_for_both(self):
        """The value the two rules answered differently on most often."""
        namespace = validator_functions(VALIDATOR.read_text(encoding="utf-8"))
        runner = run_f01.ScenarioRunner._is_marked
        for kind, _ in JUDGMENT_SITES:
            with self.subTest(kind=kind):
                self.assertFalse(validator_accepts(namespace, kind, "   ", True))
                self.assertFalse(runner("   "))


class TheValidatorTurnsRedTest(unittest.TestCase):
    """The real validator, over a scratch copy of the real contract."""

    def test_the_unchanged_contract_still_validates(self):
        result = run_validator()
        self.assertEqual(result.returncode, 0, result.stderr[-2000:])

    def test_a_whitespace_only_marker_makes_the_validator_refuse(self):
        result = run_validator(lambda document: plant_marked_or_absent(document, "   "))
        self.assertNotEqual(result.returncode, 0,
                            "the validator accepted a published example that marks an "
                            "item with nothing but whitespace")
        self.assertIn(REFUSALS["marked_or_absent"], result.stderr,
                      "the validator refused the example for another reason, so it does "
                      "not prove that this judgment spoke")

    def test_a_whitespace_only_value_makes_the_validator_refuse(self):
        result = run_validator(lambda document: plant_non_empty(document, "   "))
        self.assertNotEqual(result.returncode, 0,
                            "the validator accepted a published example that holds "
                            "nothing but whitespace")
        self.assertIn(REFUSALS["non_empty"], result.stderr,
                      "the validator refused the example for another reason, so it does "
                      "not prove that this judgment spoke")

    def test_a_whitespace_only_entry_makes_the_validator_refuse(self):
        result = run_validator(lambda document: plant_all_non_empty(document, ["   ", "a-demo"]))
        self.assertNotEqual(result.returncode, 0,
                            "the validator accepted a published example whose collection "
                            "holds an entry that is nothing but whitespace")
        self.assertIn(REFUSALS["all_non_empty"], result.stderr,
                      "the validator refused the example for another reason, so it does "
                      "not prove that this judgment spoke")

    def test_a_collection_of_real_values_still_validates(self):
        """The refusal above is about the empty entry, not about the assertion."""
        result = run_validator(lambda document: plant_all_non_empty(document, ["a-demo"]))
        self.assertEqual(result.returncode, 0, result.stderr[-2000:])


class TheGuardIsNotVacuousTest(unittest.TestCase):
    """A guard that cannot fail proves nothing."""

    def test_the_old_judgment_makes_the_validator_answer_differently(self):
        """Put the old rule back and the two judges stop agreeing."""
        namespace = validator_functions(weakened_validator(VALIDATOR.read_text(encoding="utf-8")))
        runner = run_f01.ScenarioRunner._is_marked
        divergences = []
        for kind, _ in JUDGMENT_SITES:
            for label, value, present in PROBES:
                runner_value = value if present else None
                if validator_accepts(namespace, kind, value, present) != runner(runner_value):
                    divergences.append(f"{kind}/{label}")
        self.assertTrue(divergences,
                        "the weakened validator still answers like the runner, so the "
                        "guard above cannot turn red and proves nothing")


if __name__ == "__main__":
    unittest.main()
