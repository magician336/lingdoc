"""Guard: the check operator table's self-test really runs in CI (#626).

What this file guards
---------------------
The operator table -- every check kind, and the proof that the kind judges --
lives in ``scripts/lingdoc_mock/run_f01.py``.  The six ``one_to_one`` cases were
added there, and a case that only a manual ``--self-test`` invocation runs is a
case that CI never runs: the operator table could be silently disconnected and
every automated path would stay green.

``scripts/lingdoc_mock/test_check_operators.py`` is the mirror that puts those
cases where the CI finds them, the same shape
``scripts/lingdoc_mock/test_material_gate.py`` has.  This file makes sure the
mirror keeps doing that:

*The CI job runs this discovery.*  The ``lingdoc-mock`` workflow's "Test engine,
actual contract and corrupt-response rejection" step is the command that has to
collect the cases; the guard reads the workflow rather than assuming it.

*The mirror is where the discovery looks.*  Its name has to match the pattern
the CI job runs, or the discovery walks straight past it.

*The mirror declares the operator table.*  It re-exports the class the way its
sibling does, so the mirror's shape is pinned rather than merely conventional.

*The discovery collects the six cases, and they pass.*  The guard runs the CI's
own command -- with ``-k`` narrowing what runs, not what is discovered -- and
reads the six case names out of what it ran.
"""
from __future__ import annotations

import ast
import fnmatch
from pathlib import Path
import subprocess
import sys
import unittest

REPO_ROOT = Path(__file__).resolve().parents[2]
MOCK_DIR = REPO_ROOT / "scripts" / "lingdoc_mock"
MIRROR = MOCK_DIR / "test_check_operators.py"
WORKFLOW = REPO_ROOT / ".github" / "workflows" / "lingdoc-mock.yml"

#: The pattern the CI job runs.  A mirror whose name does not match it is a
#: mirror the discovery never opens.
CI_PATTERN = "test_*.py"

#: The six cases the operator table gained with the ``one_to_one`` kind.
ONE_TO_ONE_CASES = (
    "test_one_to_one_holds_when_two_collections_answer_each_other_in_any_order",
    "test_one_to_one_fails_when_an_entry_points_at_an_item_that_is_not_there",
    "test_one_to_one_fails_when_one_item_is_left_without_a_decision",
    "test_one_to_one_fails_when_a_repeated_id_takes_the_place_of_a_missing_one",
    "test_one_to_one_fails_when_an_entry_does_not_carry_the_stated_field_value",
    "test_one_to_one_fails_when_an_entry_leaves_the_reason_empty",
)


class TheCiJobRunsThisDiscoveryTest(unittest.TestCase):
    """The command the guard runs is the command the CI job runs."""

    def test_the_mock_workflow_discovers_the_mock_tests_with_this_pattern(self):
        lines = WORKFLOW.read_text(encoding="utf-8").splitlines()
        commands = [line for line in lines
                    if "unittest discover" in line and "scripts/lingdoc_mock" in line]
        self.assertTrue(commands, "the mock workflow no longer runs a unittest discovery "
                                  "over scripts/lingdoc_mock")
        self.assertTrue(any(CI_PATTERN in line for line in commands),
                        f"the mock workflow no longer discovers {CI_PATTERN} over "
                        "scripts/lingdoc_mock; the mirror's name would not be collected")


class TheMirrorIsWhereTheDiscoveryLooksTest(unittest.TestCase):
    """The mirror's name and its declared shape."""

    def test_the_mirror_exists_and_matches_the_discovery_pattern(self):
        self.assertTrue(MIRROR.is_file(),
                        f"the operator table's mirror {MIRROR.name} is gone")
        self.assertTrue(fnmatch.fnmatch(MIRROR.name, CI_PATTERN),
                        f"{MIRROR.name} does not match {CI_PATTERN}, so the CI "
                        "discovery walks past it")

    def test_the_mirror_declares_the_operator_table(self):
        tree = ast.parse(MIRROR.read_text(encoding="utf-8"))
        exported = []
        imports = []
        for node in tree.body:
            if isinstance(node, ast.Assign) and any(
                    isinstance(target, ast.Name) and target.id == "__all__"
                    for target in node.targets):
                exported = [element.value for element in node.value.elts
                            if isinstance(element, ast.Constant)]
            if isinstance(node, ast.ImportFrom):
                imports.append((node.module, [alias.name for alias in node.names]))
        self.assertEqual(exported, ["CheckOperatorSelfTest"],
                         "the mirror no longer declares exactly the operator table it "
                         "re-exports: unittest's discovery does not read __all__ -- it "
                         "collects every TestCase class the module's namespace holds -- "
                         "so this declaration is the mirror's statement of what it "
                         "re-exports, and it has to stay true")
        self.assertIn(("scripts.lingdoc_mock.run_f01", ["CheckOperatorSelfTest"]), imports,
                      "the mirror no longer imports the operator table from the runner")


class TheDiscoveryRunsTheCasesTest(unittest.TestCase):
    """The CI's own discovery collects the six cases, and they pass."""

    def run_ci_discovery(self) -> subprocess.CompletedProcess:
        return subprocess.run(
            [sys.executable, "-m", "unittest", "discover",
             "-s", "scripts/lingdoc_mock", "-p", CI_PATTERN, "-v",
             "-k", "CheckOperatorSelfTest"],
            cwd=str(REPO_ROOT), capture_output=True, text=True,
            encoding="utf-8", errors="replace")

    def test_the_discovery_collects_and_passes_every_one_to_one_case(self):
        result = self.run_ci_discovery()
        self.assertEqual(result.returncode, 0, result.stderr[-3000:])
        output = result.stdout + result.stderr
        for case in ONE_TO_ONE_CASES:
            with self.subTest(case=case):
                self.assertIn(case, output,
                              f"the CI discovery did not run {case}: the operator "
                              "table's cases are not where the CI finds them")


if __name__ == "__main__":
    unittest.main()
