"""Offline regression tests for scope selection, including rename/delete paths."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import lingdoc_pr_scope as gate


class ScopeTests(unittest.TestCase):
    def test_contract_and_planning_changes(self):
        for path in ("docs/06-灵档产品开发规划/05-团队分工与代码边界.md",
                     "docs/TEAM_DEVELOPMENT_TASKS.md"):
            with self.subTest(path=path):
                self.assertEqual(gate.classify([path]),
                                 dict(contracts=True, backend=False, frontend=False))

    def test_contract_changes_retest_consumers(self):
        self.assertEqual(gate.classify(["docs/08-本轮实施方案/contracts/openapi.json"]),
                         dict(contracts=True, backend=True, frontend=True))

    def test_provider_and_shared_backend_changes(self):
        for path in ("internal/lingdoc/workspacecore/service.go", "internal/evidence/source.go",
                     "internal/router/router.go", "migrations/sqlite/a.sql", "go.mod", "go.sum",
                     "cmd/server/main.go", "config/config.yaml"):
            with self.subTest(path=path):
                self.assertEqual(gate.classify([path]),
                                 dict(contracts=True, backend=True, frontend=False))

    def test_frontend_checks_are_not_just_lingdoc_views(self):
        for path in ("frontend/src/views/lingdoc/Workspace.vue", "frontend/src/utils/request.ts",
                     "frontend/package-lock.json"):
            with self.subTest(path=path):
                self.assertEqual(gate.classify([path]),
                                 dict(contracts=True, backend=False, frontend=True))

    def test_governance_change_checks_its_contract_dependency(self):
        for path in (*gate.GOVERNANCE, "scripts/ci/lingdoc_pr_scope.py",
                     "scripts/ci/requirements-lingdoc.txt"):
            self.assertEqual(gate.classify([path]),
                             dict(contracts=True, backend=True, frontend=True))

    def test_unrelated_pr_and_empty_diff(self):
        for paths in ([], ["README.md"], ["cli/cmd/doc/get.go"], ["client/knowledge.go"]):
            self.assertFalse(any(gate.classify(paths).values()))

    def test_valid_shas_only(self):
        event = {"pull_request": {"base": {"sha": "a" * 40}, "head": {"sha": "b" * 40}}}
        self.assertEqual(gate.git_range(event), "a" * 40 + "..." + "b" * 40)
        for value in ("main", "a" * 39, "$(touch injected)", "a" * 40 + "\n", None, 3):
            event["pull_request"]["head"]["sha"] = value
            with self.subTest(value=value), self.assertRaises(ValueError):
                gate.git_range(event)

    def test_missing_event_is_an_error_not_an_empty_diff(self):
        with self.assertRaises(ValueError):
            gate.git_range({})

    def test_missing_packages_are_not_claimed_as_tests(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.assertEqual(gate.provider_packages(root), [])
            path = root / "internal/evidence/source.go"
            path.parent.mkdir(parents=True)
            path.touch()
            self.assertEqual(gate.provider_packages(root), ["./internal/evidence/..."])

    def test_diff_failure_does_not_pass(self):
        with patch.object(subprocess, "check_output", side_effect=subprocess.CalledProcessError(1, "git")):
            with self.assertRaises(subprocess.CalledProcessError):
                gate.changed_paths(Path.cwd(), "a...b")

    def test_names_with_spaces_and_newlines(self):
        with patch.object(subprocess, "check_output", return_value=b"internal/evidence/a b.go\0docs/a\nb.md\0"):
            self.assertEqual(gate.changed_paths(Path.cwd(), "a...b"),
                             ["internal/evidence/a b.go", "docs/a\nb.md"])

    def test_actual_git_move_out_of_provider_still_selects_tests(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            def git(*args):
                return subprocess.check_output(["git", *args], cwd=root, stderr=subprocess.DEVNULL).decode().strip()
            git("init")
            git("config", "user.name", "Gate test")
            git("config", "user.email", "gate@example.invalid")
            path = root / "internal/evidence/source.go"
            path.parent.mkdir(parents=True)
            path.write_text("package evidence\n", encoding="utf-8")
            git("add", ".")
            git("commit", "-m", "base")
            base = git("rev-parse", "HEAD")
            path.rename(root / "moved.txt")
            git("add", "-A")
            git("commit", "-m", "move")
            head = git("rev-parse", "HEAD")
            paths = gate.changed_paths(root, f"{base}...{head}")
            self.assertIn("internal/evidence/source.go", paths)
            self.assertIn("moved.txt", paths)
            self.assertTrue(gate.classify(paths)["backend"])
            event = root / "event.json"
            event.write_text(json.dumps({"pull_request": {"base": {"sha": base}, "head": {"sha": head}}}))
            output = root / "outputs.txt"
            env = {**os.environ, "GITHUB_OUTPUT": str(output)}
            result = subprocess.run([sys.executable, str(Path(gate.__file__).resolve()), "--root", str(root),
                                     "--event", str(event)], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("backend=true", output.read_text())
            self.assertIn("provider_tests=false", output.read_text())


if __name__ == "__main__":
    unittest.main()
