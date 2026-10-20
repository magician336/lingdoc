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
* the decision is a typed value, and only ``write_outputs`` stringifies it.
"""
from __future__ import annotations

from pathlib import Path
import subprocess
import tempfile
import unittest

import lingdoc_gate_decision as decision


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


if __name__ == "__main__":
    unittest.main()
