"""Guard tests for the shape of the judgement layer (#304).

The gate decides once, in ``lingdoc_gate_decision``.  Everything else in
``scripts/ci`` either holds knowledge (the manifest, the vocabulary, the
required check names) or checks something else (the route checker).  A second
script that answers "what runs" would be a second source of truth, and two
sources of truth can disagree -- that is the defect ADR 0007 set out to
remove.

These tests are guard tests, not judgement tests: they read the repository as
it is and say nothing about what a particular event decides.
"""
from __future__ import annotations

from pathlib import Path
import re
import unittest

CI_DIR = Path(__file__).resolve().parent
REPO_ROOT = CI_DIR.parents[1]
WORKFLOWS = REPO_ROOT / ".github" / "workflows"

# The module that owns the decision.
DECISION_MODULE = "lingdoc_gate_decision.py"

# The scripts that used to answer "what runs" and no longer exist.
RETIRED_SCRIPTS = (
    "lingdoc_change_scope.py",
    "lingdoc_pr_scope.py",
    "lingdoc_risk_surface.py",
    "lingdoc_format_scope.py",
)

# The scripts that stay, and what each of them is for.  A script that is not
# on this list has no business living in ``scripts/ci``.
KNOWN_SCRIPTS = {
    "lingdoc_gate_concepts.py": "the documented vocabulary of the decision",
    "lingdoc_gate_decision.py": "the decision itself",
    "lingdoc_gate_surfaces.py": "the gate surface manifest",
    "lingdoc_required_checks.py": "the names the branch ruleset demands",
    "check_lingdoc_routes.py": "the route contract checker",
}


class RetiredScriptTests(unittest.TestCase):
    """The old scope scripts are gone."""

    def test_no_retired_script_is_left_behind(self):
        for name in RETIRED_SCRIPTS:
            with self.subTest(script=name):
                self.assertFalse((CI_DIR / name).exists(),
                                 f"{name} still exists; the judgement layer has "
                                 "one module now")

    def test_the_retired_scripts_are_not_imported_any_more(self):
        for path in sorted(CI_DIR.glob("*.py")):
            source = path.read_text(encoding="utf-8")
            for name in RETIRED_SCRIPTS:
                module = name[:-len(".py")]
                with self.subTest(file=path.name, module=module):
                    self.assertNotRegex(
                        source, rf"^\s*(import|from)\s+{re.escape(module)}\b",
                        f"{path.name} still imports {module}")


class JudgementLayerTests(unittest.TestCase):
    """The judgement layer is one module with one entry point."""

    def test_the_ci_directory_holds_only_known_scripts(self):
        for path in sorted(CI_DIR.glob("*.py")):
            if path.name.startswith("test_"):
                continue
            with self.subTest(script=path.name):
                self.assertIn(path.name, KNOWN_SCRIPTS,
                              f"{path.name} is not a known CI script; a new "
                              "script has to say what it is for")

    def test_no_workflow_calls_another_scope_script(self):
        for path in sorted(WORKFLOWS.glob("*.yml")):
            text = path.read_text(encoding="utf-8")
            for name in RETIRED_SCRIPTS:
                with self.subTest(workflow=path.name, script=name):
                    self.assertNotIn(name, text)

    def test_the_workflows_ask_the_decision_module(self):
        for path in sorted(WORKFLOWS.glob("*.yml")):
            text = path.read_text(encoding="utf-8")
            if "scripts/ci/lingdoc" not in text:
                continue  # only the shared npm helper, not a gate script
            with self.subTest(workflow=path.name):
                self.assertIn(f"scripts/ci/{DECISION_MODULE}", text)

    def test_the_decision_module_owns_the_formatting_fast_path(self):
        # The formatting fast path is a judgement, and ADR 0007 gives the
        # judgement to one module.  A separate classifier file would be a
        # second place where "does this run" is decided.
        source = (CI_DIR / DECISION_MODULE).read_text(encoding="utf-8")
        self.assertIn("def _format_only_update", source)
        self.assertIn("def _format_fast_path_applies", source)


if __name__ == "__main__":
    unittest.main()
