"""Offline regression tests for the risk-surface decision.

These tests build real Git fixtures: a base branch, a topic branch, and the
ref updates a pull request would report.  The unit of validation is
(surface content, base content), so every case states which of the two moved.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import lingdoc_risk_surface as surface


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(
        ["git", *args], cwd=root, stderr=subprocess.DEVNULL).decode().strip()


def write(root: Path, relative: str, content: str) -> None:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


class RiskSurfaceTests(unittest.TestCase):
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

    def _event(self, root: Path, before: str, after: str, base: str) -> dict:
        return {
            "action": "synchronize",
            "before": before,
            "after": after,
            "pull_request": {"base": {"sha": base}, "head": {"sha": after}},
        }

    def test_documentation_only_push_keeps_the_surface_unchanged(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            write(root, "internal/lingdoc/workspace/service.go", "package workspace\n\n// note\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            before = git(root, "rev-parse", "HEAD")
            write(root, "docs/note.md", "evidence\n")
            git(root, "add", "."); git(root, "commit", "-m", "docs")
            after = git(root, "rev-parse", "HEAD")

            decision = surface.decide(root, self._event(root, before, after, base))
            self.assertFalse(decision.surface_changed)
            self.assertFalse(decision.base_moved)
            self.assertFalse(decision.run_code)
            self.assertFalse(decision.run_targeted)
            self.assertIn("unchanged", decision.reason)

    def test_code_change_re_runs_only_the_touched_package(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            before = git(root, "rev-parse", "HEAD")
            write(root, "internal/lingdoc/workspace/service.go", "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            after = git(root, "rev-parse", "HEAD")

            decision = surface.decide(root, self._event(root, before, after, base))
            self.assertTrue(decision.surface_changed)
            self.assertTrue(decision.run_code)
            self.assertTrue(decision.run_targeted)
            self.assertEqual(decision.provider_packages, ("./internal/lingdoc/workspace/...",))
            self.assertNotIn("./internal/evidence/...", decision.provider_packages)
            self.assertIn("internal/lingdoc/workspace", decision.surface)

    def test_baseline_merge_re_tests_the_contribution_on_the_new_base(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            git(root, "checkout", "-b", "topic")
            write(root, "internal/lingdoc/workspace/service.go", "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            before = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "main")
            write(root, "internal/evidence/source.go", "package evidence\n\n// upstream\n")
            git(root, "add", "."); git(root, "commit", "-m", "upstream")
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "topic")
            git(root, "merge", "--no-edit", "main")
            after = git(root, "rev-parse", "HEAD")

            decision = surface.decide(root, self._event(root, before, after, base))
            self.assertTrue(decision.base_moved)
            self.assertFalse(decision.surface_changed)
            # The contribution is re-tested against the new baseline ...
            self.assertTrue(decision.run_targeted)
            self.assertEqual(decision.provider_packages, ("./internal/lingdoc/workspace/...",))
            # ... but the repository-wide suites stay away: the contribution did
            # not change, only its context did.
            self.assertFalse(decision.run_code)
            self.assertIn("base moved", decision.reason)

    def test_documentation_only_contribution_skips_code_checks_with_a_reason(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            before = git(root, "rev-parse", "HEAD")
            write(root, "docs/08-本轮实施方案/acceptance/report.md", "evidence\n")
            git(root, "add", "."); git(root, "commit", "-m", "docs")
            after = git(root, "rev-parse", "HEAD")

            decision = surface.decide(root, self._event(root, before, after, base))
            self.assertFalse(decision.backend)
            self.assertFalse(decision.frontend)
            self.assertEqual(decision.provider_packages, ())
            self.assertFalse(decision.run_targeted)
            self.assertTrue(decision.reason)

    def test_frontend_and_go_contributions_select_both_surfaces(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            before = git(root, "rev-parse", "HEAD")
            write(root, "internal/lingdoc/workspace/service.go", "package workspace\n\n// change\n")
            write(root, "frontend/src/views/lingdoc/Workspace.vue", "<template>changed</template>\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            after = git(root, "rev-parse", "HEAD")

            decision = surface.decide(root, self._event(root, before, after, base))
            self.assertTrue(decision.backend)
            self.assertTrue(decision.frontend)
            self.assertIn("frontend/src/views/lingdoc", decision.surface)
            self.assertIn("internal/lingdoc/workspace", decision.surface)

    def test_undecidable_surface_falls_back_to_the_full_scope(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            event = self._event(root, base, base, base)
            # A head that does not exist makes every git query fail.
            event["pull_request"]["head"]["sha"] = "f" * 40
            event["after"] = "f" * 40
            with self.assertRaises(subprocess.CalledProcessError):
                surface.decide(root, event)
            # The CLI turns that into an explicit full scope instead of a skip.
            event_path = root / "event.json"
            event_path.write_text(json.dumps(event), encoding="utf-8")
            output = root / "outputs.txt"
            env = {**os.environ, "GITHUB_OUTPUT": str(output)}
            result = subprocess.run(
                [sys.executable, str(Path(surface.__file__).resolve()),
                 "--event", str(event_path), "--root", str(root)],
                env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            written = output.read_text()
            self.assertIn("run_code=true", written)
            self.assertIn("run_targeted=true", written)
            self.assertIn("risk surface undetermined", written)

    def test_opened_and_reopened_events_validate_the_whole_contribution(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            write(root, "internal/lingdoc/workspace/service.go", "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            head = git(root, "rev-parse", "HEAD")
            for action in ("opened", "reopened", "ready_for_review"):
                event = {"action": action,
                         "pull_request": {"base": {"sha": base}, "head": {"sha": head}}}
                decision = surface.decide(root, event)
                self.assertTrue(decision.run_code, action)
                self.assertTrue(decision.run_targeted, action)
                self.assertEqual(decision.provider_packages, ("./internal/lingdoc/workspace/...",), action)

    def test_outputs_expose_surface_reason_and_base(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            before = git(root, "rev-parse", "HEAD")
            write(root, "internal/lingdoc/workspace/service.go", "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            after = git(root, "rev-parse", "HEAD")
            event_path = root / "event.json"
            event_path.write_text(json.dumps(self._event(root, before, after, base)), encoding="utf-8")
            output = root / "outputs.txt"
            env = {**os.environ, "GITHUB_OUTPUT": str(output)}
            result = subprocess.run(
                [sys.executable, str(Path(surface.__file__).resolve()),
                 "--event", str(event_path), "--root", str(root)],
                env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            written = output.read_text()
            self.assertIn(f"base={base}", written)
            self.assertIn("surface=internal/lingdoc/workspace", written)
            self.assertIn("reason=contribution surface changed", written)
            self.assertIn("provider_tests=true", written)

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
            git(root, "checkout", "-b", "topic")
            before = git(root, "rev-parse", "HEAD")
            write(root, "scripts/ci/lingdoc_pr_scope.py", "# gate changed\n")
            git(root, "add", "."); git(root, "commit", "-m", "gate")
            after = git(root, "rev-parse", "HEAD")

            decision = surface.decide(root, self._event(root, before, after, base))
            self.assertTrue(decision.run_code)
            self.assertTrue(decision.run_targeted)
            self.assertTrue(decision.contracts)
            self.assertTrue(decision.backend)
            self.assertTrue(decision.frontend)
            self.assertEqual(decision.provider_packages,
                             ("./internal/lingdoc/...", "./internal/evidence/..."))

    def test_full_scope_is_requested_explicitly(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            decision = surface.full_decision(root, "full scope requested")
            self.assertTrue(decision.run_code)
            self.assertTrue(decision.run_targeted)
            self.assertTrue(decision.contracts)
            self.assertTrue(decision.backend)
            self.assertTrue(decision.frontend)
            self.assertEqual(decision.provider_packages, ())


if __name__ == "__main__":
    unittest.main()
