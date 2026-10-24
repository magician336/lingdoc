"""Guard: one emptiness rule, and evidence that says what it actually means (#639, #640).

What this file guards
---------------------

Two tickets, one file, because they are the same mistake seen from two sides:
**a word that means one thing in one place and another thing in another place.**

* **#639 -- the emptiness rule.**  ``docs/08-本轮实施方案/contracts/validate_artifacts.py``
  judges emptiness in three places (``marked_or_absent``, ``non_empty``,
  ``all_non_empty``).  Those three places carried their own copy of the rule --
  "empty means ``None``, ``''``, ``[]`` or ``{}``" -- while the runner
  (``scripts/lingdoc_mock/run_f01.py``'s ``ScenarioRunner._is_marked``) says a
  value is empty when it is ``None``, ``False``, a whitespace-only string, or an
  empty collection.  The two rules answered five kinds of value differently: a
  whitespace-only string, a tab-and-newline string, a ``False``, an empty tuple
  and an empty set.  A contract the validator and the runner judge differently
  is a contract nobody can trust.

  The validator already carried the runner's rule once
  (``is_marked``, added by #626).  The three judgment sites now call that one
  rule instead of carrying a copy of it.  This file ties **the three call
  sites themselves** to the runner, value by value -- so a site that drifts
  back turns red -- and proves it end to end by running the real validator
  over a scratch copy of the contract.

* **#640 -- the evidence wording.**  ``scripts/lingdoc_mock/main_path_evidence.py``
  publishes what a run found.  Three of its words were wrong:

  1. a check the contract declares and the run **never judged** was published
     with ``status: "failed"`` -- so the evidence said of an assertion nobody
     had produced a fact about that it did not hold.  An unevaluated assertion
     now carries a value of its own (``not_run``) and a shape of its own, so
     that it can be told apart from a failed one;
  2. when a coverage gap downgraded the verdict to BLOCKED, the reason kept
     the text of the verdict it had replaced -- "every assertion held, every
     prose assertion was judged, the provider's semantics were verified" --
     under a BLOCKED verdict.  The reason now names the gap by the coverage's
     own fields;
  3. this path makes no emptiness judgment of its own, so there is no third
     copy of the rule to drift.  The file says so, and the guard says so.

The verdict the whole report carries and the verdict the main path carries are
two different scopes, so they may differ -- but only by a rule that is written
down and pinned here, not by accident.

How these tests run
-------------------

They run offline: no service, no browser, no network.  The two changed modules
are read the way the repository reads them: the validator is a script, so its
judgment functions are read out of the parsed source and executed on their
own; the evidence module is imported as a module, because that is what the
release decision imports.
"""
from __future__ import annotations

import ast
import copy
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

from scripts.lingdoc_mock import main_path_evidence, run_f01  # noqa: E402

CONTRACTS = REPO_ROOT / "docs" / "08-本轮实施方案" / "contracts"
VALIDATOR = CONTRACTS / "validate_artifacts.py"
WORKFLOW = CONTRACTS / "workflow.json"

#: The evidence module, read as text.  The guard reads its own source so that
#: it can hold a *weakened* copy beside it and watch the guard turn red.
EVIDENCE_SOURCE = (REPO_ROOT / "scripts" / "lingdoc_mock" / "main_path_evidence.py").read_text(encoding="utf-8")

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

#: A failure observation in the shape the second path accepts: the failure
#: side is observed, so the verdict is decided by the main path alone.
FAILURE_OBSERVATION = {"failure_case": "duplicate_formal_write",
                       "expected_http": 409, "actual_http": 409,
                       "no_formal_side_effect": True,
                       "readback": {"status": "unchanged"}}


# ---------------------------------------------------------------------------
# #639 -- reading the validator's own judgment out of its source
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
# #640 -- reading the evidence module
# ---------------------------------------------------------------------------

def evidence_functions(source: str) -> dict:
    """The evidence module's own entry builders, executed on their own.

    The guard holds a *weakened* copy of the module's source beside the real
    one.  Importing a weakened copy of the whole module would drag the
    repository's packages into it; reading the two functions that write the
    entries out of the parsed source does not, and it is exactly those two
    functions that decide what an entry says.
    """
    tree = ast.parse(source)
    wanted = ("_safe_subject", "_safe_check_entry",
              "_safe_failed_check_entry", "_safe_unevaluated_check_entry")
    nodes = [node for node in tree.body
             if isinstance(node, ast.FunctionDef) and node.name in wanted]
    found = {node.name for node in nodes}
    missing = [name for name in wanted if name not in found]
    if missing:
        raise AssertionError("the evidence module no longer carries " + ", ".join(missing))
    namespace: dict = {"re": re}
    exec(compile(ast.Module(body=nodes, type_ignores=[]), "<main_path_evidence>", "exec"), namespace)
    # The constants the two builders read -- the pointer pattern, the step id
    # pattern, the three check statuses -- are read out of the source as well,
    # so the guard does not carry a second copy of any of them.  A constant
    # that cannot be read on its own is not one of them, and is skipped.
    for node in tree.body:
        if not isinstance(node, ast.Assign):
            continue
        names = [target.id for target in node.targets if isinstance(target, ast.Name)]
        if not names:
            continue
        try:
            value = eval(compile(ast.Expression(node.value), "<main_path_evidence>", "eval"),
                         {"re": re})
        except Exception:
            continue
        for name in names:
            namespace[name] = value
    return namespace


def weakened_evidence(source: str) -> str:
    """The evidence's unevaluated entries published as failures again."""
    marker = "_safe_check_entry(entry, CHECK_NOT_RUN)"
    if marker not in source:
        raise AssertionError("the evidence module no longer publishes unevaluated "
                             "entries with a status of their own")
    return source.replace(marker, "_safe_check_entry(entry, CHECK_FAILED)")


def degradation_branch(source: str) -> ast.If:
    """The branch that downgrades a verdict because the coverage does not line up."""
    tree = ast.parse(source)
    function = next((node for node in tree.body
                     if isinstance(node, ast.FunctionDef)
                     and node.name == "build_main_path_report"), None)
    if function is None:
        raise AssertionError("the evidence module no longer carries a build_main_path_report")
    for node in ast.walk(function):
        if not isinstance(node, ast.If):
            continue
        downgrades = any(
            isinstance(child, ast.Assign)
            and any(isinstance(target, ast.Name) and target.id == "main_result"
                    for target in child.targets)
            for child in ast.walk(node)
            if isinstance(child, ast.Assign))
        if downgrades:
            return node
    raise AssertionError("the evidence module no longer downgrades a verdict "
                         "when the coverage does not line up")


def branch_assigns_reason(branch: ast.If) -> bool:
    """Whether a branch says why it downgraded, not only that it did."""
    for node in ast.walk(branch):
        if not isinstance(node, ast.Assign):
            continue
        for target in node.targets:
            if (isinstance(target, ast.Subscript)
                    and isinstance(target.value, ast.Name)
                    and target.value.id == "analysis"):
                return True
    return False


def runner_shaped_report(*, checks=None, manual_assertions=None,
                          provider_semantics_status="verified", steps=None) -> dict:
    """A report in the shape the F01 runner writes, built from the contract.

    The runner's record carries the node ids, the HTTP status every node
    answered with, and one entry per machine assertion it evaluated.  A report
    of this shape is what the release decision reads.
    """
    contract = json.loads(WORKFLOW.read_text(encoding="utf-8"))
    node_steps = steps if steps is not None else [
        {"id": step["id"], "operation_id": step["operation_id"],
         "http_status": step["expected_http"]}
        for step in contract["steps"]]
    if checks is None:
        checks = [{"step_id": check["step_id"], "operator": check["operator"],
                   "path": check["path"], "status": "passed"}
                  for check in main_path_evidence.declared_contract_checks()]
    return {
        "workflow": contract["id"],
        "runner_status": "completed",
        "completed_steps": len(node_steps),
        "total_steps": len(node_steps),
        "verification_scope": "http_smoke_only",
        "provider_semantics_status": provider_semantics_status,
        "manual_assertions": list(manual_assertions or []),
        "steps": node_steps,
        "checks": copy.deepcopy(checks),
    }


def all_checks() -> list[dict]:
    """The contract's own assertions, as the second path reads them."""
    return [{"step_id": check["step_id"], "operator": check["operator"],
             "path": check["path"], "status": "passed"}
            for check in main_path_evidence.declared_contract_checks()]


def second_path(report: dict, failure=FAILURE_OBSERVATION) -> dict:
    """The evidence the second path publishes about one run."""
    return main_path_evidence.build_main_path_report(main_report=report,
                                                      failure_observation=failure)


# ---------------------------------------------------------------------------
# #639 -- the two judges answer the same question the same way
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


# ---------------------------------------------------------------------------
# #640 -- the evidence says what it means
# ---------------------------------------------------------------------------

class UnevaluatedIsNotFailedTest(unittest.TestCase):
    """An assertion nobody judged did not fail."""

    def test_an_unevaluated_check_is_not_published_as_a_failure(self):
        checks = all_checks()
        missing = checks.pop(0)
        report = runner_shaped_report(checks=checks)
        evidence = second_path(report)
        unevaluated = evidence["main_path"]["unevaluated_checks"]
        self.assertEqual(len(unevaluated), 1)
        self.assertEqual(unevaluated[0]["step_id"], missing["step_id"])
        self.assertEqual(unevaluated[0]["operator"], missing["operator"])
        self.assertEqual(unevaluated[0]["status"], "not_run")

    def test_an_unevaluated_check_is_not_published_as_a_pass_either(self):
        checks = all_checks()
        checks.pop(0)
        evidence = second_path(runner_shaped_report(checks=checks))
        published = json.dumps(evidence["main_path"]["unevaluated_checks"], ensure_ascii=False)
        self.assertNotIn("failed", published)
        self.assertNotIn("passed", published)

    def test_a_failed_check_and_an_unevaluated_one_have_different_shapes(self):
        """The two lists may not look alike, or a reader cannot tell them apart."""
        checks = all_checks()
        unevaluated = copy.deepcopy(checks)
        unevaluated.pop(0)
        unevaluated[0]["status"] = "failed"
        evidence = second_path(runner_shaped_report(checks=unevaluated))
        failed_entry = evidence["main_path"]["failed_checks"][0]
        unevaluated_entry = evidence["main_path"]["unevaluated_checks"][0]
        self.assertEqual(failed_entry["status"], "failed")
        self.assertEqual(unevaluated_entry["status"], "not_run")
        self.assertNotEqual(set(failed_entry), set(unevaluated_entry),
                            "the failed and the unevaluated entries have the same shape, "
                            "so they can only be told apart by the list they came from")
        self.assertNotEqual(failed_entry["status"], unevaluated_entry["status"])

    def test_the_real_failure_stays_a_failure(self):
        """The fix may not overreach: a failed check is still a failure."""
        checks = all_checks()
        checks[0]["status"] = "failed"
        evidence = second_path(runner_shaped_report(checks=checks))
        self.assertEqual(evidence["main_path"]["result"], "FAIL")
        self.assertEqual(evidence["main_path"]["failed_checks"][0]["status"], "failed")


class TheDegradedVerdictSaysWhyTest(unittest.TestCase):
    """A verdict that was downgraded carries the reason of the verdict it now is."""

    def _degraded(self) -> dict:
        main, failure = main_path_evidence.build_mock_main_path_fixture()
        main["steps"] = main["steps"][1:]
        main["completed_steps"] -= 1
        return main_path_evidence.build_main_path_report(main_report=main,
                                                         failure_observation=failure)

    def test_the_coverage_gap_downgrades_both_the_whole_and_the_main_path(self):
        evidence = self._degraded()
        self.assertEqual(evidence["result"], "BLOCKED")
        self.assertEqual(evidence["main_path"]["result"], "BLOCKED")
        self.assertFalse(evidence["fixture_coverage"]["step_sequence_complete"])

    def test_the_reason_names_the_gap_and_not_the_verdict_it_replaced(self):
        evidence = self._degraded()
        reason = evidence["main_path"]["reason"]
        self.assertIn("覆盖不完整", reason)
        missing = evidence["fixture_coverage"]["missing_step_ids"]
        self.assertTrue(missing, "the degraded run is missing a node")
        for step_id in missing:
            self.assertIn(step_id, reason,
                          "the reason does not name the node the run did not answer")
        self.assertNotIn("全部节点的 HTTP 与契约声明一致", reason,
                         "the reason still carries the text of the verdict the "
                         "coverage gap replaced")

    def test_the_degradation_branch_says_why_it_downgraded(self):
        """The reason is assigned inside the branch, not only somewhere else."""
        self.assertTrue(branch_assigns_reason(degradation_branch(EVIDENCE_SOURCE)),
                        "the branch that downgrades a verdict does not say why it did")

    def test_the_reason_function_names_the_coverage_fields(self):
        coverage = {"required_parts": ["project"], "constructed_parts": ["project"],
                    "constructed_complete": False, "missing_parts": ["export"],
                    "step_sequence_complete": False, "step_counts_complete": False,
                    "missing_step_ids": ["F01-23"], "unexpected_step_ids": []}
        reason = main_path_evidence._coverage_gap_reason(coverage)
        self.assertIn("export", reason)
        self.assertIn("F01-23", reason)
        self.assertIn("覆盖不完整", reason)


class TheVerdictsStayApartTest(unittest.TestCase):
    """The whole and the main path may differ, but only by the written rule."""

    def _cases(self) -> list[tuple[str, dict, str, str]]:
        """(label, report, expected whole, expected main path)."""
        failed = all_checks()
        failed[0]["status"] = "failed"
        partial = all_checks()[:-1]
        undeclared = all_checks() + [{"step_id": "F01-07", "operator": "equals",
                                      "path": "/data/not_declared", "status": "passed"}]
        return [
            ("every check evaluated and held", runner_shaped_report(), "PASS", "PASS"),
            ("one check failed", runner_shaped_report(checks=failed), "FAIL", "FAIL"),
            ("one check never evaluated", runner_shaped_report(checks=partial),
             "NOT RUN", "NOT RUN"),
            ("a check the contract does not declare", runner_shaped_report(checks=undeclared),
             "BLOCKED", "BLOCKED"),
            ("the provider's semantics unverified",
             runner_shaped_report(provider_semantics_status="not_verified"), "NOT RUN", "NOT RUN"),
            ("a prose assertion nobody judged",
             runner_shaped_report(manual_assertions=[{"step_id": "F01-06b",
                                                       "assertion": "一句机器读不了的话",
                                                       "status": "not_run"}]),
             "NOT RUN", "NOT RUN"),
        ]

    def test_each_case_takes_the_verdict_the_rule_gives_it(self):
        for label, report, whole, main_path in self._cases():
            with self.subTest(case=label):
                evidence = second_path(report)
                self.assertEqual(evidence["result"], whole)
                self.assertEqual(evidence["main_path"]["result"], main_path)

    def test_the_whole_and_the_main_path_differ_only_by_the_written_rule(self):
        """Rule 1 and rule 3 make them equal; rule 2 makes the whole the failure half's."""
        for label, report, whole, main_path in self._cases():
            with self.subTest(case=label):
                evidence = second_path(report)
                if evidence["main_path"]["result"] != "PASS":
                    self.assertEqual(evidence["result"], evidence["main_path"]["result"],
                                     "the whole and the main path differ although the "
                                     "main path did not pass")
                else:
                    self.assertEqual(evidence["result"], evidence["failure_path"]["result"],
                                     "the main path passed, so the whole is the failure "
                                     "half's verdict")

    def test_the_failure_half_decides_the_whole_when_the_main_path_passed(self):
        report = runner_shaped_report()
        evidence = second_path(report, failure=None)
        self.assertEqual(evidence["main_path"]["result"], "PASS")
        self.assertEqual(evidence["result"], "BLOCKED")
        self.assertEqual(evidence["failure_path"]["result"], "BLOCKED")

    def test_the_whole_is_never_a_pass_while_the_main_path_is_not(self):
        for label, report, _, _ in self._cases():
            with self.subTest(case=label):
                evidence = second_path(report)
                if evidence["main_path"]["result"] != "PASS":
                    self.assertNotEqual(evidence["result"], "PASS")


class TheEvidenceGuardIsNotVacuousTest(unittest.TestCase):
    """Put the wording back and watch the guard turn red."""

    def test_publishing_an_unevaluated_check_as_a_failure_is_caught(self):
        namespace = evidence_functions(weakened_evidence(EVIDENCE_SOURCE))
        entry = namespace["_safe_unevaluated_check_entry"](
            {"step_id": "F01-07", "operator": "equals", "path": "/data/status"})
        self.assertNotEqual(entry["status"], "not_run",
                            "the weakened evidence still publishes an unevaluated check "
                            "with a status of its own, so the guard above cannot turn red")

    def test_a_degradation_that_does_not_say_why_is_caught(self):
        weakened = EVIDENCE_SOURCE.replace(
            'analysis["reason"] = _coverage_gap_reason(coverage)',
            'pass  # the reason is not synced with the verdict')
        self.assertNotEqual(weakened, EVIDENCE_SOURCE,
                            "the evidence module no longer syncs the reason, so the "
                            "guard above cannot turn red")
        self.assertFalse(branch_assigns_reason(degradation_branch(weakened)),
                         "the weakened evidence downgrades the verdict without saying "
                         "why, so the guard above cannot turn red")


if __name__ == "__main__":
    unittest.main()
