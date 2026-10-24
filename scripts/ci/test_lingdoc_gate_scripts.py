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

import inspect
from pathlib import Path
import re
import unittest

import lingdoc_gate_decision as decision

CI_DIR = Path(__file__).resolve().parent
REPO_ROOT = CI_DIR.parents[1]
WORKFLOWS = REPO_ROOT / ".github" / "workflows"

# The module that owns the decision.
DECISION_MODULE = "lingdoc_gate_decision.py"

# The decision layer's public interface is registered **once**, in ADR 0007.
# Every entry is a line of the form ``- `name` — one sentence``.
ADR_0007 = REPO_ROOT / "docs" / "adr" / "0007-gate-decision-deep-module.md"
REGISTERED_INTERFACE = re.compile(r"^- `([A-Za-z_][A-Za-z0-9_.]*)` — ",
                                   re.MULTILINE)

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
    "lingdoc_gate_attest_reuse.py": "the witness that may reuse an earlier green gate run of the same merge ref (spec #627)",
    "lingdoc_gate_queue_state.py": "the queued-or-running judgement of the pull request gate",
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


class PublicInterfaceTests(unittest.TestCase):
    """The decision layer exposes exactly the interface ADR 0007 registers.

    The registry is the single source: a public name that nobody registered
    is an unregistered door into the judgement, and a registered name the
    module does not have is a promise the documents keep and the code
    breaks.  Both make this test red.
    """

    @classmethod
    def setUpClass(cls) -> None:
        cls.module = decision
        cls.registered = cls._registered_names()
        cls.exposed = cls._exposed_names(cls.module)

    @staticmethod
    def _registered_names() -> list[str]:
        """The names ADR 0007 registers, in the order it registers them."""
        text = ADR_0007.read_text(encoding="utf-8")
        return REGISTERED_INTERFACE.findall(text)

    @staticmethod
    def _exposed_names(module) -> set[str]:
        """The public functions and classes the module defines itself."""
        return {
            name for name, value in vars(module).items()
            if not name.startswith("_")
            and (inspect.isfunction(value) or inspect.isclass(value))
            and getattr(value, "__module__", "") == module.__name__
        }

    def test_the_adr_registers_the_interface(self):
        self.assertTrue(self.registered,
                        f"{ADR_0007.name} registers no public interface; the "
                        "decision layer has no single source any more")

    def test_the_module_exposes_exactly_the_registered_interface(self):
        registered = {name for name in self.registered if "." not in name}
        self.assertEqual(
            self.exposed, registered,
            "the decision layer's public interface and ADR 0007's registry "
            f"disagree: only in the module: {sorted(self.exposed - registered)}; "
            f"only in the ADR: {sorted(registered - self.exposed)}")

    def test_every_registered_member_exists(self):
        for name in self.registered:
            if "." not in name:
                continue
            owner, _, member = name.partition(".")
            with self.subTest(member=name):
                self.assertTrue(hasattr(self.module, owner),
                                f"ADR 0007 registers {name!r}, but the module "
                                f"has no {owner!r}")
                self.assertTrue(hasattr(getattr(self.module, owner), member),
                                f"ADR 0007 registers {name!r}, but the module "
                                f"does not have it")

    def test_no_undocumented_public_name_is_exposed(self):
        registered = set(self.registered)
        for name in sorted(self.exposed):
            with self.subTest(name=name):
                self.assertIn(
                    name, registered,
                    f"the decision module exposes {name!r}, but ADR 0007 does "
                    "not register it; either register it or make it private")


if __name__ == "__main__":
    unittest.main()
