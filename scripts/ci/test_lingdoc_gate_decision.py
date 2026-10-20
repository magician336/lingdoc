"""Tests for the gate decision module (``scripts/ci/lingdoc_gate_decision.py``).

The module is the deep module of the gate: it owns the diff ranges, the
changed files, the tree hashes, the tested surface, the gate-surface
classification and the formatting fast path.  Its public interface is two
functions -- :func:`decide` returns a typed :class:`Decision`, and
:func:`write_outputs` is the one place where a decision becomes strings.

These tests pin the behaviour ADR 0006 already established, one fixture per
rule:

* the contribution is ``base...head``, never the last push;
* the base moved when the two merge bases differ;
* a change to the gate itself is judged by the whole gate;
* the decision is a typed value, and only ``write_outputs`` stringifies it;
* an undecidable surface falls back to the full scope and says so;
* the formatting fast path keeps ADR 0005's mouth;
* the three artifact fields keep their names and their meaning;
* the transitional scripts and the decision module agree, item by item.
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import lingdoc_gate_decision as decision
import lingdoc_gate_surfaces as manifest

CI_DIR = Path(__file__).resolve().parent


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(
        ["git", *args], cwd=root, stderr=subprocess.DEVNULL).decode().strip()


def write(root: Path, relative: str, content: str) -> None:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


class DecisionFixture(unittest.TestCase):
    """A repository with a base branch, a topic branch and provider code."""

    def _repo(self, root: Path) -> None:
        git(root, "init", "-b", "main")
        git(root, "config", "user.email", "synthetic@example.com")
        git(root, "config", "user.name", "Synthetic CI test")
        write(root, "README.md", "base\n")
        write(root, "internal/lingdoc/workspace/service.go", "package workspace\n")
        write(root, "internal/evidence/source.go", "package evidence\n")
        write(root, "frontend/src/views/lingdoc/Workspace.vue", "<template/>\n")
        git(root, "add", ".")
        git(root, "commit", "-m", "base")

    def _event(self, before: str, after: str, base: str) -> dict:
        return {
            "action": "synchronize",
            "before": before,
            "after": after,
            "pull_request": {"base": {"sha": base}, "head": {"sha": after}},
        }

    def _topic(self, root: Path) -> str:
        git(root, "checkout", "-b", "topic")
        return git(root, "rev-parse", "HEAD")


class ContributionTests(DecisionFixture):
    """The contribution is the pull request's own range."""

    def test_contribution_is_the_pull_request_range_not_the_last_push(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            before = git(root, "rev-parse", "HEAD")
            # The last push only carries documentation, but the pull request
            # itself contributes code.
            write(root, "docs/note.md", "evidence\n")
            git(root, "add", "."); git(root, "commit", "-m", "docs")
            after = git(root, "rev-parse", "HEAD")

            result = decision.decide(self._event(before, after, base), root)

            self.assertIn("internal/lingdoc/workspace/service.go", result.contribution)
            self.assertIn("docs/note.md", result.contribution)
            self.assertEqual(result.provider_packages,
                             ("./internal/lingdoc/workspace/...",))
            self.assertEqual(result.surface, ("internal/lingdoc/workspace",))
            self.assertEqual(result.base, base)

    def test_the_decision_is_typed_and_only_write_outputs_stringifies(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            before = git(root, "rev-parse", "HEAD")
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            after = git(root, "rev-parse", "HEAD")

            result = decision.decide(self._event(before, after, base), root)

            self.assertIsInstance(result, decision.Decision)
            self.assertIsInstance(result.surface_changed, bool)
            self.assertIsInstance(result.base_moved, bool)
            self.assertIsInstance(result.forced, bool)
            self.assertIsInstance(result.reason, str)
            self.assertTrue(result.reason)
            outputs = decision.write_outputs(result)
            self.assertEqual(outputs["surface_changed"], "true")
            self.assertEqual(outputs["base"], base)
            self.assertIn("contribution surface changed", outputs["reason"])


class BaseMovementTests(DecisionFixture):
    """The base moved when the two merge bases differ."""

    def test_a_baseline_merge_moves_the_base_under_the_branch(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            self._topic(root)
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            before = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "main")
            write(root, "internal/evidence/source.go",
                  "package evidence\n\n// upstream\n")
            git(root, "add", "."); git(root, "commit", "-m", "upstream")
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "topic")
            git(root, "merge", "--no-edit", "main")
            after = git(root, "rev-parse", "HEAD")

            result = decision.decide(self._event(before, after, base), root)

            self.assertTrue(result.base_moved)
            self.assertFalse(result.surface_changed)
            # The contribution is re-tested against the new baseline ...
            self.assertTrue(result.run_targeted)
            self.assertEqual(result.provider_packages,
                             ("./internal/lingdoc/workspace/...",))
            # ... but the repository-wide suites stay away.
            self.assertFalse(result.run_code)
            self.assertIn("base moved", result.reason)

    def test_a_push_that_changes_nothing_moves_nothing(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            before = git(root, "rev-parse", "HEAD")
            write(root, "docs/note.md", "evidence\n")
            git(root, "add", "."); git(root, "commit", "-m", "docs")
            after = git(root, "rev-parse", "HEAD")

            result = decision.decide(self._event(before, after, base), root)

            self.assertFalse(result.base_moved)
            self.assertFalse(result.surface_changed)
            self.assertFalse(result.run_code)
            self.assertFalse(result.run_targeted)


class GovernanceTests(DecisionFixture):
    """The gate judges itself: a change to the gate runs the whole gate."""

    def test_a_change_to_the_gate_itself_is_judged_by_the_whole_gate(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            write(root, "scripts/ci/lingdoc_pr_scope.py", "# gate\n")
            write(root, "internal/lingdoc/workspace/service.go", "package workspace\n")
            write(root, "internal/evidence/source.go", "package evidence\n")
            git(root, "add", ".")
            git(root, "commit", "-m", "base")
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            before = git(root, "rev-parse", "HEAD")
            write(root, "scripts/ci/lingdoc_pr_scope.py", "# gate changed\n")
            git(root, "add", "."); git(root, "commit", "-m", "gate")
            after = git(root, "rev-parse", "HEAD")

            result = decision.decide(self._event(before, after, base), root)

            self.assertTrue(result.governance_changed)
            self.assertTrue(result.run_code)
            self.assertTrue(result.run_targeted)
            self.assertTrue(result.contracts)
            self.assertTrue(result.backend)
            self.assertTrue(result.frontend)
            self.assertEqual(result.provider_packages,
                             ("./internal/lingdoc/...", "./internal/evidence/..."))
            self.assertIn("frontend", result.surface)

    def test_a_change_to_a_gate_workflow_is_judged_by_the_whole_gate(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            before = git(root, "rev-parse", "HEAD")
            write(root, ".github/workflows/lingdoc-review.yml", "name: gate\n")
            git(root, "add", "."); git(root, "commit", "-m", "workflow")
            after = git(root, "rev-parse", "HEAD")

            result = decision.decide(self._event(before, after, base), root)

            self.assertTrue(result.governance_changed)
            self.assertTrue(result.run_code)
            self.assertTrue(result.run_targeted)
            self.assertIn("frontend", result.surface)


class ValidationUnitTests(DecisionFixture):
    """The unit of validation is (surface content, base content)."""

    def test_a_changed_surface_is_re_tested(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            before = git(root, "rev-parse", "HEAD")
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            after = git(root, "rev-parse", "HEAD")

            result = decision.decide(self._event(before, after, base), root)

            self.assertTrue(result.surface_changed)
            self.assertTrue(result.run_code)
            self.assertTrue(result.run_targeted)
            self.assertEqual(result.surface, ("internal/lingdoc/workspace",))
            self.assertTrue(result.fingerprint)

    def test_an_unchanged_surface_is_not_re_tested(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            before = git(root, "rev-parse", "HEAD")
            write(root, "docs/note.md", "evidence\n")
            git(root, "add", "."); git(root, "commit", "-m", "docs")
            after = git(root, "rev-parse", "HEAD")

            result = decision.decide(self._event(before, after, base), root)

            self.assertFalse(result.surface_changed)
            self.assertFalse(result.base_moved)
            self.assertFalse(result.run_code)
            self.assertFalse(result.run_targeted)


class FailOpenTests(DecisionFixture):
    """An undecidable surface costs compute, never correctness."""

    def _broken_event(self, root: Path) -> dict:
        base = git(root, "rev-parse", "HEAD")
        event = self._event(base, base, base)
        # A head that does not exist makes every git query fail.
        event["pull_request"]["head"]["sha"] = "f" * 40
        event["after"] = "f" * 40
        return event

    def test_an_undecidable_surface_falls_back_to_the_full_scope(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)

            result = decision.decide(self._broken_event(root), root)

            self.assertTrue(result.undecided)
            self.assertTrue(result.forced)
            self.assertTrue(result.run_code)
            self.assertTrue(result.run_targeted)
            self.assertTrue(result.contracts)
            self.assertTrue(result.backend)
            self.assertTrue(result.frontend)
            self.assertEqual(result.provider_packages,
                             ("./internal/lingdoc/...", "./internal/evidence/..."))

    def test_the_fallback_says_why_in_the_published_outputs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)

            result = decision.decide(self._broken_event(root), root)
            outputs = decision.write_outputs(result)

            self.assertIn("risk surface undetermined", result.reason)
            self.assertIn("risk surface undetermined", outputs["reason"])
            self.assertEqual(outputs["run_code"], "true")
            self.assertEqual(outputs["run_targeted"], "true")


class FormatFastPathTests(DecisionFixture):
    """ADR 0005's mouth: only Go, never a compiler directive, never a guess."""

    def _update(self, root: Path, content: str) -> dict:
        base = git(root, "rev-parse", "HEAD")
        self._topic(root)
        before = git(root, "rev-parse", "HEAD")
        write(root, "internal/lingdoc/workspace/service.go", content)
        git(root, "add", "."); git(root, "commit", "-m", "update")
        after = git(root, "rev-parse", "HEAD")
        return self._event(before, after, base)

    def test_a_comment_only_change_is_a_formatting_only_update(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)

            result = decision.decide(
                self._update(root, "package workspace\n\n// note\n"), root)

            self.assertTrue(result.format_only)
            self.assertTrue(result.format_reasons)

    def test_a_compiler_directive_is_never_a_formatting_only_change(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)

            result = decision.decide(
                self._update(root, "package workspace\n\n//go:build linux\n"), root)

            self.assertFalse(result.format_only)
            self.assertTrue(any("directive" in reason
                                for reason in result.format_reasons))

    def test_a_change_that_is_not_go_is_not_a_formatting_only_change(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            before = git(root, "rev-parse", "HEAD")
            write(root, "docs/note.md", "evidence\n")
            git(root, "add", "."); git(root, "commit", "-m", "docs")
            after = git(root, "rev-parse", "HEAD")

            result = decision.decide(self._event(before, after, base), root)

            self.assertFalse(result.format_only)


class ArtifactFieldTests(DecisionFixture):
    """The three artifact fields keep their names and their meaning."""

    def test_surface_reason_and_base_are_published_with_their_meaning(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            before = git(root, "rev-parse", "HEAD")
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            after = git(root, "rev-parse", "HEAD")

            result = decision.decide(self._event(before, after, base), root)
            outputs = decision.write_outputs(result)

            self.assertEqual(outputs["surface"], "internal/lingdoc/workspace")
            self.assertEqual(outputs["reason"], "contribution surface changed")
            self.assertEqual(outputs["base"], base)


class CrossImplementationConsistencyTests(DecisionFixture):
    """The decision module and the transitional scripts answer alike.

    While the seven workflow-level gates still call the old scripts, the two
    implementations answer the same event side by side.  The fields on which
    they may differ are listed explicitly: the workflow-level scripts publish
    the decision's own contract flag, the pull request gate publishes the
    scope flag, and only the gate's own flag is read by a workflow.
    """

    COMPARED = ("run", "format_only", "surface", "base", "base_moved",
                "surface_changed", "reason", "run_code", "run_targeted",
                "contracts", "backend", "frontend", "provider_tests",
                "provider_packages")
    # The two implementations must not differ on any compared field.  The
    # whitelist is empty on purpose: a difference here means the decision
    # module and the transitional scripts answer the same event differently.
    EXPECTED_DIFFERENCES = frozenset()

    def _run(self, script: str, args: list[str], root: Path,
             scratch: Path) -> dict[str, str]:
        output = scratch / "outputs.txt"
        if output.exists():
            output.unlink()
        env = {**os.environ, "GITHUB_OUTPUT": str(output)}
        result = subprocess.run(
            [sys.executable, str(CI_DIR / script), *args],
            env=env, capture_output=True, text=True, cwd=str(root))
        self.assertEqual(result.returncode, 0, result.stderr)
        written: dict[str, str] = {}
        for line in output.read_text(encoding="utf-8").splitlines():
            key, _, value = line.partition("=")
            written[key] = value
        return written

    def _scenarios(self, root: Path):
        """The three events the two implementations are compared on."""
        base = git(root, "rev-parse", "HEAD")
        self._topic(root)
        before = git(root, "rev-parse", "HEAD")
        write(root, "internal/lingdoc/workspace/service.go",
              "package workspace\n\n// change\n")
        git(root, "add", "."); git(root, "commit", "-m", "code")
        after = git(root, "rev-parse", "HEAD")
        code_change = self._event(before, after, base)

        before = after
        write(root, "docs/note.md", "evidence\n")
        git(root, "add", "."); git(root, "commit", "-m", "docs")
        after = git(root, "rev-parse", "HEAD")
        docs_push = self._event(before, after, base)

        before = after
        git(root, "checkout", "main")
        write(root, "internal/evidence/source.go",
              "package evidence\n\n// upstream\n")
        git(root, "add", "."); git(root, "commit", "-m", "upstream")
        base = git(root, "rev-parse", "HEAD")
        git(root, "checkout", "topic")
        git(root, "merge", "--no-edit", "main")
        after = git(root, "rev-parse", "HEAD")
        baseline_merge = self._event(before, after, base)

        return {"code_change": code_change, "docs_push": docs_push,
                "baseline_merge": baseline_merge}

    def test_the_module_and_the_scripts_answer_the_same_questions(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "repo"
            scratch = Path(tmp) / "scratch"
            root.mkdir()
            scratch.mkdir()
            self._repo(root)
            scenarios = self._scenarios(root)
            patterns = next(surface.paths for surface in manifest.load_surfaces()
                            if surface.name == "app")

            for name, event in scenarios.items():
                event_path = scratch / "event.json"
                event_path.write_text(json.dumps(event), encoding="utf-8")
                with self.subTest(scenario=name):
                    result = decision.decide(event, root)
                    module_outputs = decision.write_outputs(result, patterns=patterns)
                    script_outputs = self._run(
                        "lingdoc_change_scope.py",
                        ["--event", str(event_path), "--root", str(root),
                         "--patterns", "\n".join(patterns)], root, scratch)
                    differences = {
                        field for field in self.COMPARED
                        if module_outputs.get(field) != script_outputs.get(field)}
                    self.assertEqual(
                        differences, set(self.EXPECTED_DIFFERENCES),
                        f"{name}: {[(field, module_outputs.get(field), script_outputs.get(field)) for field in sorted(differences)]}")

    def test_the_pull_request_gate_reads_the_same_decision(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "repo"
            scratch = Path(tmp) / "scratch"
            root.mkdir()
            scratch.mkdir()
            self._repo(root)
            scenarios = self._scenarios(root)

            for name, event in scenarios.items():
                event_path = scratch / "event.json"
                event_path.write_text(json.dumps(event), encoding="utf-8")
                with self.subTest(scenario=name):
                    result = decision.decide(event, root)
                    module_outputs = decision.write_outputs(result)
                    script_outputs = self._run(
                        "lingdoc_pr_scope.py",
                        ["--event", str(event_path), "--root", str(root),
                         "--latest"], root, scratch)
                    differences = {
                        field for field in self.COMPARED
                        if module_outputs.get(field) != script_outputs.get(field)}
                    # The gate publishes the scope flags; the module publishes
                    # the same flags, so nothing may differ here.
                    self.assertEqual(differences, set())


class PreRefactorGoldenTests(DecisionFixture):
    """The verdicts the old implementation produced, kept as literals.

    These values were recorded from the scripts before the decision module
    existed (commit f684be2).  They are the independent source of truth for
    "the refactor changed the structure, not the verdicts".

    Two fields of the *workflow-level* scripts did change, and only these two:
    ``contracts`` and ``provider_tests``.  The old scripts published the
    decision's own contract flag and an ungated provider flag; the decision
    module publishes the scope flag and the gated provider flag, which is what
    the pull request gate has always read.  The gate's own values are pinned
    by :class:`PreRefactorGoldenTests` below, so the live behaviour is
    unchanged.
    """

    GOLDEN = {
        "code_change": {
            "run": "true", "format_only": "true",
            "surface": "internal/lingdoc/workspace",
            "base_moved": "false", "surface_changed": "true",
            "reason": "contribution surface changed",
            "run_code": "true", "run_targeted": "true",
            "backend": "true", "frontend": "false",
            "provider_packages": "./internal/lingdoc/workspace/...",
        },
        "docs_push": {
            "run": "false", "format_only": "false",
            "surface": "internal/lingdoc/workspace",
            "base_moved": "false", "surface_changed": "false",
            "reason": "tested surface unchanged and base unchanged",
            "run_code": "false", "run_targeted": "false",
            "backend": "true", "frontend": "false",
            "provider_packages": "./internal/lingdoc/workspace/...",
        },
        "baseline_merge": {
            "run": "false", "format_only": "true",
            "surface": "internal/lingdoc/workspace",
            "base_moved": "true", "surface_changed": "false",
            "reason": "the base moved under the branch; the contribution is "
                      "re-tested against it",
            "run_code": "false", "run_targeted": "true",
            "backend": "true", "frontend": "false",
            "provider_packages": "./internal/lingdoc/workspace/...",
        },
    }

    def test_the_verdicts_are_the_ones_the_old_implementation_gave(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            before = git(root, "rev-parse", "HEAD")
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            after = git(root, "rev-parse", "HEAD")
            events = {"code_change": self._event(before, after, base)}

            before = after
            write(root, "docs/note.md", "evidence\n")
            git(root, "add", "."); git(root, "commit", "-m", "docs")
            after = git(root, "rev-parse", "HEAD")
            events["docs_push"] = self._event(before, after, base)

            before = after
            git(root, "checkout", "main")
            write(root, "internal/evidence/source.go",
                  "package evidence\n\n// upstream\n")
            git(root, "add", "."); git(root, "commit", "-m", "upstream")
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "topic")
            git(root, "merge", "--no-edit", "main")
            after = git(root, "rev-parse", "HEAD")
            events["baseline_merge"] = self._event(before, after, base)

            patterns = next(surface.paths for surface in manifest.load_surfaces()
                            if surface.name == "app")
            for name, event in events.items():
                with self.subTest(scenario=name):
                    outputs = decision.write_outputs(
                        decision.decide(event, root), patterns=patterns)
                    for field, expected in self.GOLDEN[name].items():
                        self.assertEqual(outputs[field], expected, field)

    # What the pull request gate read before the refactor, and still reads.
    GATE_GOLDEN = {
        "code_change": {"contracts": "true", "backend": "true",
                        "frontend": "false", "provider_tests": "true"},
        "docs_push": {"contracts": "true", "backend": "true",
                      "frontend": "false", "provider_tests": "false"},
        "baseline_merge": {"contracts": "true", "backend": "true",
                           "frontend": "false", "provider_tests": "true"},
    }

    def test_the_gate_flags_are_the_ones_the_old_implementation_gave(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            before = git(root, "rev-parse", "HEAD")
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            after = git(root, "rev-parse", "HEAD")
            events = {"code_change": self._event(before, after, base)}

            before = after
            write(root, "docs/note.md", "evidence\n")
            git(root, "add", "."); git(root, "commit", "-m", "docs")
            after = git(root, "rev-parse", "HEAD")
            events["docs_push"] = self._event(before, after, base)

            before = after
            git(root, "checkout", "main")
            write(root, "internal/evidence/source.go",
                  "package evidence\n\n// upstream\n")
            git(root, "add", "."); git(root, "commit", "-m", "upstream")
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "topic")
            git(root, "merge", "--no-edit", "main")
            after = git(root, "rev-parse", "HEAD")
            events["baseline_merge"] = self._event(before, after, base)

            for name, event in events.items():
                with self.subTest(scenario=name):
                    outputs = decision.write_outputs(decision.decide(event, root))
                    for field, expected in self.GATE_GOLDEN[name].items():
                        self.assertEqual(outputs[field], expected, field)


if __name__ == "__main__":
    unittest.main()
