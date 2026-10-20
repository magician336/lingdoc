"""Tests for the push event's tested surface (#308).

A pull request carries a contribution: the range ``base...head``.  A push
carries no pull request, but it does carry a range of its own -- GitHub
reports the ``before`` and ``after`` commits of the push -- so the push's
tested surface is the diff the push delivers, and the push's baseline is the
commit it started from.

These tests pin that behaviour at the module's public seam
(:func:`lingdoc_gate_decision.decide` and
:func:`lingdoc_gate_decision.write_outputs`), on a real git repository:

* a push that touches the frontend selects the frontend jobs;
* a push that only touches documentation selects no code job;
* a push that touches the backend selects the app jobs;
* a push never takes the formatting-only fast path (ADR 0005);
* a first push -- an all-zero ``before`` -- falls back to the parent of the
  new head;
* a push that changes the gate itself is judged by the whole gate.
"""
from __future__ import annotations

from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import lingdoc_gate_decision as decision

BASE_FILES = {
    "internal/lingdoc/workspace/service.go":
        "package workspace\n\nfunc F() int { return 1 }\n",
    "internal/lingdoc/workspacecore/store.go": "package workspacecore\n",
    "internal/evidence/source.go": "package evidence\n",
    "frontend/src/views/Workspace.vue": "<template/>\n",
    "docs/note.md": "note\n",
}


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(
        ["git", *args], cwd=root, stderr=subprocess.DEVNULL).decode().strip()


def write(root: Path, relative: str, content: str) -> None:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


class PushFixture(unittest.TestCase):
    """A repository with the paths the gate surfaces watch."""

    def _repo(self, root: Path) -> None:
        git(root, "init", "-b", "main")
        git(root, "config", "user.email", "synthetic@example.com")
        git(root, "config", "user.name", "Synthetic CI test")
        for relative, content in BASE_FILES.items():
            write(root, relative, content)
        git(root, "add", ".")
        git(root, "commit", "-m", "base")

    def _push(self, root: Path, changes: dict[str, str], message: str
              ) -> dict[str, str]:
        """Commit the changes and return the push event GitHub would send."""
        for relative, content in changes.items():
            write(root, relative, content)
        git(root, "add", ".")
        git(root, "commit", "-m", message)
        after = git(root, "rev-parse", "HEAD")
        before = git(root, "rev-parse", "HEAD^")
        return {"ref": "refs/heads/main", "before": before, "after": after}

    def _outputs(self, root: Path, event: dict) -> dict[str, str]:
        return decision.write_outputs(decision.decide(event, root))


class PushSurfaceTests(PushFixture):
    """A push is judged by the diff it delivers."""

    def test_a_push_that_touches_the_frontend_selects_the_frontend_jobs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            event = self._push(root, {"frontend/src/views/Workspace.vue":
                                      "<template>changed</template>\n"},
                               "frontend")

            outputs = self._outputs(root, event)

            self.assertEqual(outputs["job_frontend_verify"], "true")
            self.assertEqual(outputs["job_frontend_docker_image"], "true")
            self.assertEqual(outputs["job_app_verify"], "false")
            self.assertEqual(outputs["job_go_lint_changes"], "false")

    def test_a_push_that_only_touches_documentation_selects_no_code_job(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            event = self._push(root, {"docs/note.md": "changed\n"}, "docs")

            outputs = self._outputs(root, event)

            for key in ("job_frontend_verify", "job_frontend_docker_image",
                        "job_go_lint_changes", "job_go_lint_golangci",
                        "job_app_verify", "job_lingdoc_mock_mock",
                        "job_lingdoc_f01_runner_runner_tests",
                        "job_lingdoc_workspace_concurrency_workspace_concurrency",
                        "job_lingdoc_interface_integration_integration",
                        "job_lingdoc_interface_integration_frontend"):
                with self.subTest(key=key):
                    self.assertEqual(outputs[key], "false")
            self.assertEqual(outputs["run"], "false")

    def test_a_push_that_touches_the_backend_selects_the_app_jobs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            event = self._push(root, {
                "internal/lingdoc/workspace/service.go":
                    "package workspace\n\nfunc F() int { return 2 }\n"}, "code")

            outputs = self._outputs(root, event)

            self.assertEqual(outputs["job_app_verify"], "true")
            self.assertEqual(outputs["job_go_lint_changes"], "true")
            self.assertEqual(outputs["job_go_lint_golangci"], "true")
            self.assertEqual(outputs["job_frontend_verify"], "false")
            # The workspace core is a surface of its own; the push did not
            # touch it.
            self.assertEqual(
                outputs["job_lingdoc_workspace_concurrency_workspace_concurrency"],
                "false")

    def test_the_push_names_its_own_surface_and_baseline(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            event = self._push(root, {
                "internal/lingdoc/workspace/service.go":
                    "package workspace\n\nfunc F() int { return 2 }\n"}, "code")

            outputs = self._outputs(root, event)

            self.assertEqual(outputs["base"], event["before"])
            self.assertEqual(outputs["diff_range"],
                             f"{event['before']}...{event['after']}")
            self.assertEqual(outputs["surface"], "internal/lingdoc/workspace")
            self.assertIn("push", outputs["reason"])


class PushBaselineTests(PushFixture):
    """A push that creates a branch names no commit before it."""

    def test_a_first_push_falls_back_to_the_parent_of_the_new_head(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            event = self._push(root, {
                "internal/lingdoc/workspace/service.go":
                    "package workspace\n\nfunc F() int { return 2 }\n"}, "code")
            # GitHub reports an all-zero `before` when it creates a ref.
            event["before"] = "0" * 40
            parent = git(root, "rev-parse", "HEAD^")

            outputs = self._outputs(root, event)

            self.assertEqual(outputs["diff_range"], f"{parent}...{event['after']}")
            self.assertEqual(outputs["base"], parent)
            self.assertEqual(outputs["job_app_verify"], "true")


class PushFormatTests(PushFixture):
    """ADR 0005: a push never takes the formatting-only fast path."""

    def test_a_formatting_only_push_still_runs_the_test_jobs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            event = self._push(root, {
                "internal/lingdoc/workspace/service.go":
                    "package workspace\n\n// A note.\nfunc F() int { return 1 }\n"},
                "format")

            outputs = self._outputs(root, event)

            self.assertEqual(outputs["format_only"], "false")
            self.assertEqual(outputs["job_app_verify"], "true")


class PushGovernanceTests(PushFixture):
    """The gate judges itself: a push that changes the gate runs everything."""

    def test_a_push_that_changes_the_gate_is_judged_by_the_whole_gate(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            event = self._push(root, {"scripts/ci/lingdoc_gate_decision.py":
                                      "# gate\n"}, "gate")

            outputs = self._outputs(root, event)

            self.assertEqual(outputs["run_code"], "true")
            self.assertEqual(outputs["run_targeted"], "true")
            self.assertEqual(outputs["contracts"], "true")
            self.assertEqual(outputs["backend"], "true")
            self.assertEqual(outputs["frontend"], "true")
            self.assertIn("frontend", outputs["surface"])
            self.assertIn("./internal/lingdoc/...", outputs["provider_packages"])


if __name__ == "__main__":
    unittest.main()
