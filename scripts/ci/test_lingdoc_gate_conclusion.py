"""Every gate publishes the same decision conclusion (#302).

Eight gates judge this repository: the seven workflow-level gates and the
pull request gate.  ADR 0006 demands three things of a gate that skips a
check:

* the conclusion names the tested ``surface``, the ``reason`` and the
  ``base``,
* a skipped check says **why** it did not run -- "Skipped means NOT RUN, not
  passed" is only true when the reason is readable,
* the conclusion is an artefact, not a claim: it carries the boundary
  statement that keeps "green" from being read as "accepted".

These tests pin the *publication* of the conclusion.  They do not touch the
decision semantics: the verdicts themselves are pinned by
``test_lingdoc_gate_equivalence.py`` and ``test_lingdoc_gate_decision.py``.
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import yaml

import lingdoc_gate_decision as decision
import lingdoc_gate_surfaces as manifest

CI_DIR = Path(__file__).resolve().parent
REPO_ROOT = CI_DIR.parents[1]
WORKFLOWS = REPO_ROOT / ".github" / "workflows"

# The seven workflow-level gates and the pull request gate.
WORKFLOW_LEVEL_GATES = (
    "frontend.yml",
    "go-lint.yml",
    "app.yml",
    "lingdoc-mock.yml",
    "lingdoc-f01-runner.yml",
    "lingdoc-workspace-concurrency.yml",
    "lingdoc-interface-integration.yml",
)
PR_GATE = "lingdoc-review.yml"
ALL_GATES = WORKFLOW_LEVEL_GATES + (PR_GATE,)

BOUNDARY = ("Automated checks only; not consumer approval, a live-model run, "
            "or product acceptance.")


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(
        ["git", *args], cwd=root, stderr=subprocess.DEVNULL).decode().strip()


def write(root: Path, relative: str, content: str) -> None:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


class ConclusionFixture(unittest.TestCase):
    """A repository whose pull request changes the provider packages."""

    def _repo(self, root: Path) -> None:
        git(root, "init", "-b", "main")
        git(root, "config", "user.email", "synthetic@example.com")
        git(root, "config", "user.name", "Synthetic CI test")
        write(root, "README.md", "base\n")
        write(root, "internal/lingdoc/workspace/service.go", "package workspace\n")
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

    def _decision(self, root: Path, *, change: str = "go") -> decision.Decision:
        """A decision for a pull request that changes one kind of code."""
        base = git(root, "rev-parse", "HEAD")
        git(root, "checkout", "-b", "topic")
        before = git(root, "rev-parse", "HEAD")
        if change == "go":
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\nfunc F() int { return 42 }\n")
        elif change == "format":
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\n// only a comment\n")
        else:
            write(root, "frontend/src/views/lingdoc/Workspace.vue",
                  "<template>changed</template>\n")
        git(root, "add", ".")
        git(root, "commit", "-m", "change")
        after = git(root, "rev-parse", "HEAD")
        return decision.decide(self._event(before, after, base), root)

    def _repo_with_decision(self, tmp: str, *, change: str = "go"):
        root = Path(tmp) / "repo"
        root.mkdir()
        self._repo(root)
        return root, self._decision(root, change=change)


class ConclusionShapeTests(ConclusionFixture):
    """Every gate publishes the same conclusion, with the same three fields."""

    def test_all_eight_gates_publish_the_same_three_fields(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = self._repo_with_decision(tmp)
            conclusions = [decision.gate_conclusion(result, f".github/workflows/{name}")
                           for name in ALL_GATES]
            for conclusion in conclusions:
                with self.subTest(gate=conclusion["gate"]):
                    for field in ("surface", "reason", "base"):
                        self.assertIn(field, conclusion)
                        self.assertEqual(conclusion[field],
                                         conclusions[0][field],
                                         f"{conclusion['gate']} publishes a "
                                         f"different {field}")
            self.assertTrue(conclusions[0]["surface"])
            self.assertTrue(conclusions[0]["reason"])
            self.assertTrue(conclusions[0]["base"])

    def test_every_gate_lists_its_checks_with_a_reason(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = self._repo_with_decision(tmp)
            for name in ALL_GATES:
                conclusion = decision.gate_conclusion(
                    result, f".github/workflows/{name}")
                with self.subTest(gate=name):
                    self.assertTrue(conclusion["checks"],
                                    f"{name} publishes no check at all")
                    for check in conclusion["checks"]:
                        self.assertIn(check["verdict"], ("runs", "skipped"))
                        self.assertTrue(check["reason"].strip())
                        self.assertNotIn(check["reason"], ("true", "false"))
                        self.assertTrue(check["surface"])

    def test_every_gate_carries_the_same_boundary_statement(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = self._repo_with_decision(tmp)
            for name in ALL_GATES:
                conclusion = decision.gate_conclusion(
                    result, f".github/workflows/{name}")
                with self.subTest(gate=name):
                    self.assertEqual(conclusion["boundary"], BOUNDARY)


class SkipReasonTests(ConclusionFixture):
    """A skipped check says why it did not run."""

    def test_a_skipped_job_states_why_in_the_conclusion(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = self._repo_with_decision(tmp, change="go")
            conclusion = decision.gate_conclusion(
                result, ".github/workflows/frontend.yml")
            skipped = [check for check in conclusion["checks"] if not check["run"]]
            self.assertTrue(skipped, "the frontend gate should skip a Go-only change")
            for check in skipped:
                self.assertEqual(check["verdict"], "skipped")
                self.assertIn("contribution", check["reason"])
                self.assertGreater(len(check["reason"]), 20)

    def test_a_formatting_only_change_says_why_the_check_is_skipped(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = self._repo_with_decision(tmp, change="format")
            self.assertTrue(result.format_only)
            conclusion = decision.gate_conclusion(
                result, ".github/workflows/frontend.yml")
            for check in conclusion["checks"]:
                self.assertFalse(check["run"])
                self.assertIn("only reformats the code", check["reason"])

    def test_the_summary_shows_the_skip_reason(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = self._repo_with_decision(tmp, change="go")
            summary = Path(tmp) / "summary.md"
            summary.write_text("", encoding="utf-8")
            conclusion = decision.publish_conclusion(
                result, ".github/workflows/frontend.yml", summary=summary)
            text = summary.read_text(encoding="utf-8")
            for check in conclusion["checks"]:
                self.assertIn(check["name"], text)
                self.assertIn(check["reason"], text)
            self.assertIn(BOUNDARY, text)
            self.assertIn("Skipped means NOT RUN, not passed.", text)

    def test_the_artifact_shows_the_skip_reason(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = self._repo_with_decision(tmp, change="go")
            folder = Path(tmp) / "artifact"
            conclusion = decision.publish_conclusion(
                result, ".github/workflows/frontend.yml", artifact_dir=folder)
            written = json.loads(
                (folder / "gate-conclusion.json").read_text(encoding="utf-8"))
            self.assertEqual(written["boundary"], BOUNDARY)
            self.assertEqual(written["surface"], conclusion["surface"])
            self.assertEqual(written["reason"], conclusion["reason"])
            self.assertEqual(written["base"], conclusion["base"])
            skipped = [check for check in written["checks"] if not check["run"]]
            self.assertTrue(skipped)
            for check in skipped:
                self.assertTrue(check["reason"])
            self.assertIn("Skipped means NOT RUN, not passed.",
                          (folder / "gate-conclusion.md").read_text(encoding="utf-8"))


class UndecidedTests(ConclusionFixture):
    """An undecided gate says that it fell back to the full scope."""

    def _undecided(self) -> decision.Decision:
        """A decision the public interface reports as undecided.

        The judgement seam is the module's public interface: an event that
        carries no pull request cannot be judged, so the gate falls back to
        the full scope and says so.  Nothing here reaches into a private
        helper -- the same rule applies to every judgement test.
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            return decision.decide({}, root)

    def test_the_conclusion_says_it_fell_back_to_the_full_scope(self):
        conclusion = decision.gate_conclusion(
            self._undecided(), ".github/workflows/frontend.yml")
        self.assertTrue(conclusion["undecided"])
        self.assertIn("未判定，已回退全量", conclusion["undecided_note"])
        self.assertIn("risk surface undetermined, fell back to the full scope",
                      conclusion["reason"])

    def test_the_summary_says_it_fell_back_to_the_full_scope(self):
        with tempfile.TemporaryDirectory() as tmp:
            summary = Path(tmp) / "summary.md"
            decision.publish_conclusion(
                self._undecided(), ".github/workflows/frontend.yml", summary=summary)
            self.assertIn("未判定，已回退全量",
                          summary.read_text(encoding="utf-8"))


class WorkflowWiringTests(unittest.TestCase):
    """Every gate publishes the conclusion from its own workflow file."""

    def _text(self, name: str) -> str:
        return (WORKFLOWS / name).read_text(encoding="utf-8")

    def test_every_gate_asks_the_module_to_publish_its_conclusion(self):
        for name in ALL_GATES:
            with self.subTest(gate=name):
                self.assertIn(f"--gate .github/workflows/{name}", self._text(name))

    def test_every_workflow_level_gate_uploads_the_conclusion(self):
        for name in WORKFLOW_LEVEL_GATES:
            document = yaml.safe_load(self._text(name)) or {}
            jobs = document.get("jobs") or {}
            uploads = []
            for job in jobs.values():
                for step in job.get("steps") or []:
                    uses = str(step.get("uses") or "")
                    if uses.startswith("actions/upload-artifact"):
                        uploads.append(step)
            with self.subTest(gate=name):
                self.assertTrue(uploads, f"{name} uploads no conclusion artefact")
                self.assertTrue(
                    any("gate-conclusion" in str(step.get("with", {}).get("path", ""))
                        for step in uploads),
                    f"{name} uploads no gate conclusion artefact")

    def test_the_pull_request_gate_publishes_into_its_own_artefact(self):
        text = self._text(PR_GATE)
        self.assertIn("--gate .github/workflows/lingdoc-review.yml", text)
        self.assertIn("lingdoc-checks", text)


class CommandLineTests(ConclusionFixture):
    """The command line publishes the conclusion as well."""

    def test_the_command_writes_the_conclusion_and_the_summary(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, result = self._repo_with_decision(tmp, change="go")
            scratch = Path(tmp) / "scratch"
            scratch.mkdir()
            event_path = scratch / "event.json"
            base = git(root, "rev-parse", "main")
            head = git(root, "rev-parse", "HEAD")
            event_path.write_text(json.dumps({
                "action": "synchronize",
                "before": head,
                "after": head,
                "pull_request": {"base": {"sha": base}, "head": {"sha": head}},
            }), encoding="utf-8")
            folder = scratch / "artifact"
            summary = scratch / "summary.md"
            summary.write_text("", encoding="utf-8")

            completed = subprocess.run(
                [sys.executable, str(CI_DIR / "lingdoc_gate_decision.py"),
                 "--event", str(event_path), "--root", str(root),
                 "--gate", ".github/workflows/frontend.yml",
                 "--artifact-dir", str(folder)],
                env={**os.environ, "GITHUB_STEP_SUMMARY": str(summary)},
                capture_output=True, text=True)

            self.assertEqual(completed.returncode, 0, completed.stderr)
            conclusion = json.loads(
                (folder / "gate-conclusion.json").read_text(encoding="utf-8"))
            self.assertEqual(conclusion["boundary"], BOUNDARY)
            self.assertIn(conclusion["reason"], summary.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
