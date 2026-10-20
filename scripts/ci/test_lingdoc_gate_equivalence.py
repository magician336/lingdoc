"""The rewiring did not change what the gates run (#301).

Before the rewiring every workflow-level gate carried its own path table
and asked ``lingdoc_change_scope.py`` what to run.  That script is still in
the repository -- it is a transitional shell until T6 -- and it still
answers the same question.  This module runs the old script and the new
decision side by side on the same event and compares the conclusions, job
by job.

The path tables below are the ones the workflows carried before the
rewiring.  They are the independent source of truth: if the manifest's
surface drifts away from them, the comparison fails and somebody has to
say so on purpose.

Two things did change on purpose, and they are checked separately:

* the decision no longer looks at the last push but at the pull request's
  own contribution, and
* the formatting-only shortcut is folded into the decision instead of being
  a second term of the workflow's condition.
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

# The path table each workflow carried before the rewiring.
PATH_TABLES = {
    "frontend": (
        "frontend/**",
        "scripts/build_frontend_dist.sh",
        ".github/workflows/frontend.yml",
    ),
    "go-lint": (
        "**/*.go",
        "**/go.mod",
        "**/go.sum",
        ".golangci.yml",
        ".github/workflows/go-lint.yml",
    ),
    "app": (
        "**/*.go",
        "!cli/**",
        "!client/**",
        "!docreader/**",
        "!third_party/anydoc-go/**",
        "go.mod",
        "go.sum",
        "Makefile",
        "config/**",
        "migrations/**",
        "skills/preloaded/**",
        "testdata/**",
        "docker/Dockerfile.app",
        "LICENSE",
        "THIRD_PARTY_NOTICES.md",
        "licenses/**",
        "internal/textconv/data/**",
        "scripts/*license*.sh",
        "cmd/desktop/build/windows/installer/project.nsi",
        ".github/workflows/app.yml",
    ),
    "lingdoc-mock": (
        "scripts/lingdoc_mock/**",
        "scripts/ci/requirements-lingdoc.txt",
        "scripts/ci/check_lingdoc_routes.py",
        "docs/08-本轮实施方案/contracts/**",
        "frontend/src/dev/lingdoc-mock/**",
        "frontend/lingdoc-mock.html",
        "frontend/src/utils/request.ts",
        "frontend/package*.json",
        "frontend/vite.config.ts",
        ".github/workflows/lingdoc-mock.yml",
    ),
    "lingdoc-f01-runner": (
        "scripts/lingdoc_mock/run_f01.py",
        "scripts/lingdoc_mock/load_state.py",
        "scripts/lingdoc_mock/run_scenario.py",
        "scripts/lingdoc_mock/build_t15_matrix.py",
        "scripts/lingdoc_mock/test_workflow*.py",
        "docs/08-本轮实施方案/contracts/**",
        "docs/08-本轮实施方案/README.md",
        "docs/08-本轮实施方案/02-接口与Mock约定.md",
        "docs/08-本轮实施方案/T15-11-验证报告.json",
        "docs/08-本轮实施方案/T15-11-证据.json",
        ".github/workflows/lingdoc-f01-runner.yml",
    ),
    "lingdoc-workspace-concurrency": (
        "internal/lingdoc/workspacecore/**",
        "migrations/sqlite/000018_lingdoc_workspace.*.sql",
        "go.mod",
        "go.sum",
        ".github/workflows/lingdoc-workspace-concurrency.yml",
    ),
    "lingdoc-interface-integration": (
        "internal/lingdoc/**",
        "internal/evidence/**",
        "scripts/acceptance/**",
        "internal/container/lingdoc*",
        "internal/router/lingdoc*",
        ".github/workflows/lingdoc-interface-integration.yml",
    ),
}

# The jobs the formatting-only shortcut used to skip, per surface.  Lint is
# the judge of formatting, so it was never skipped.
FORMAT_ONLY_SKIPPED = {
    "frontend": ("verify", "docker-image"),
    "go-lint": (),
    "app": ("verify",),
    "lingdoc-mock": ("mock",),
    "lingdoc-f01-runner": ("runner-tests",),
    "lingdoc-workspace-concurrency": ("workspace-concurrency",),
    "lingdoc-interface-integration": ("integration", "frontend"),
}

BASE_FILES = {
    "internal/lingdoc/workspace/service.go": "package workspace\n",
    "internal/lingdoc/workspacecore/store.go": "package workspacecore\n",
    "internal/evidence/source.go": "package evidence\n",
    "frontend/src/views/Workspace.vue": "<template/>\n",
    "scripts/lingdoc_mock/run_f01.py": "print('f01')\n",
    "scripts/acceptance/check.py": "print('acceptance')\n",
    "docs/note.md": "note\n",
}

# A change of the code itself, and a change that only adds comments.
CODE = "package workspace\n\nfunc F() int { return 42 }\n"
CODE_CHANGED = "package workspace\n\nfunc F() int { return 43 }\n"
COMMENT_ADDED = "package workspace\n\n// A note.\n// Another note.\n"


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(
        ["git", *args], cwd=root, stderr=subprocess.DEVNULL).decode().strip()


def write(root: Path, relative: str, content: str) -> None:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


class GateFixture(unittest.TestCase):
    """A repository whose pull request touches the surfaces of the gates."""

    def _repo(self, root: Path) -> str:
        git(root, "init", "-b", "main")
        git(root, "config", "user.email", "synthetic@example.com")
        git(root, "config", "user.name", "Synthetic CI test")
        for relative, content in BASE_FILES.items():
            write(root, relative, content)
        git(root, "add", ".")
        git(root, "commit", "-m", "base")
        base = git(root, "rev-parse", "HEAD")
        git(root, "checkout", "-b", "topic")
        return base

    def _pull_request(self, root: Path, changes: dict[str, str],
                      base: str | None = None) -> dict:
        """A pull request that carries one commit with the given changes."""
        for relative, content in changes.items():
            write(root, relative, content)
        git(root, "add", ".")
        git(root, "commit", "-m", "change")
        head = git(root, "rev-parse", "HEAD")
        base = base or git(root, "merge-base", "main", head)
        return {"action": "synchronize", "before": base, "after": head,
                "pull_request": {"base": {"sha": base}, "head": {"sha": head}}}

    def _old_outputs(self, root: Path, event: dict, table,
                     event_name: str = "pull_request"
                     ) -> tuple[dict[str, str], dict]:
        """What the pre-rewiring script answered for this event.

        Two sources, both of the old script: the outputs it wrote to
        ``GITHUB_OUTPUT`` and the report it printed.  The report is the one
        that names the range the old script resolved for itself.
        """
        with tempfile.TemporaryDirectory() as scratch:
            event_path = Path(scratch) / "event.json"
            event_path.write_text(json.dumps(event), encoding="utf-8")
            output_path = Path(scratch) / "outputs.txt"
            env = {**os.environ, "GITHUB_OUTPUT": str(output_path),
                   "GITHUB_EVENT_NAME": event_name}
            result = subprocess.run(
                [sys.executable, str(CI_DIR / "lingdoc_change_scope.py"),
                 "--event", str(event_path), "--root", str(root),
                 "--patterns", "\n".join(table)],
                env=env, capture_output=True, text=True, cwd=str(root))
            self.assertEqual(result.returncode, 0, result.stderr)
            written: dict[str, str] = {}
            for line in output_path.read_text(encoding="utf-8").splitlines():
                key, _, value = line.partition("=")
                written[key] = value
            return written, json.loads(result.stdout)

    def _new_outputs(self, root: Path, event: dict) -> dict[str, str]:
        """What the decision module answers for this event."""
        return decision.write_outputs(decision.decide(event, root))


class PathTableTests(GateFixture):
    """The manifest still carries the tables the workflows carried."""

    def test_every_surface_still_watches_the_paths_it_watched(self):
        for name, table in PATH_TABLES.items():
            with self.subTest(surface=name):
                surface = next(surface for surface in manifest.load_surfaces()
                               if surface.name == name)
                self.assertEqual(list(surface.paths), list(table))

    def test_the_manifest_skips_the_same_jobs_on_a_formatting_only_update(self):
        for name, skipped in FORMAT_ONLY_SKIPPED.items():
            with self.subTest(surface=name):
                surface = next(surface for surface in manifest.load_surfaces()
                               if surface.name == name)
                self.assertEqual(surface.skipped_on_format_only, bool(skipped))
                for consumer in surface.consumers:
                    self.assertEqual(consumer.job in skipped,
                                     surface.skipped_on_format_only)


class ConclusionEquivalenceTests(GateFixture):
    """The rewiring did not change the conclusion of any gate job."""

    def _compare(self, root: Path, event: dict) -> set[str]:
        """Compare the old conclusion with the new one, job by job."""
        running: set[str] = set()
        for name, table in PATH_TABLES.items():
            surface = next(surface for surface in manifest.load_surfaces()
                           if surface.name == name)
            old, _ = self._old_outputs(root, event, table)
            new = self._new_outputs(root, event)
            old_run = old["run"] == "true"
            old_format_only = old["format_only"] == "true"
            for consumer in surface.consumers:
                key = decision.output_key(consumer.workflow, consumer.job)
                skipped = consumer.job in FORMAT_ONLY_SKIPPED[name]
                expected = old_run and not (old_format_only and skipped)
                identity = f"{Path(consumer.workflow).name}#{consumer.job}"
                with self.subTest(surface=name, job=consumer.job):
                    self.assertEqual(new[key], "true" if expected else "false")
                if expected:
                    running.add(identity)
        return running

    def test_a_code_change_runs_the_same_gates_as_before(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = self._repo(root)
            event = self._pull_request(
                root, {"internal/lingdoc/workspace/service.go": CODE_CHANGED},
                base)

            running = self._compare(root, event)

            self.assertEqual(running, {
                "app.yml#verify",
                "go-lint.yml#changes",
                "go-lint.yml#golangci",
                "lingdoc-interface-integration.yml#integration",
                "lingdoc-interface-integration.yml#frontend",
            })

    def test_a_frontend_change_runs_the_same_gates_as_before(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = self._repo(root)
            event = self._pull_request(
                root, {"frontend/src/views/Workspace.vue": "<template>changed</template>\n"},
                base)

            running = self._compare(root, event)

            self.assertEqual(running, {"frontend.yml#verify",
                                       "frontend.yml#docker-image"})

    def test_a_formatting_only_change_skips_the_same_gates_as_before(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = self._repo(root)
            event = self._pull_request(
                root, {"internal/lingdoc/workspace/service.go": COMMENT_ADDED},
                base)

            running = self._compare(root, event)

            # Only the lint job runs: lint is the judge of formatting.
            self.assertEqual(running, {"go-lint.yml#changes",
                                       "go-lint.yml#golangci"})

    def test_an_unrelated_change_runs_the_same_gates_as_before(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = self._repo(root)
            event = self._pull_request(root, {"docs/note.md": "changed\n"}, base)

            running = self._compare(root, event)

            self.assertEqual(running, set())


class ContributionRangeTests(GateFixture):
    """The decision reads the pull request's contribution, not the last push."""

    def test_the_last_push_no_longer_decides_the_range(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = self._repo(root)
            # The pull request carries a code change; its last push carries
            # documentation only.
            write(root, "internal/lingdoc/workspace/service.go", CODE)
            git(root, "add", "."); git(root, "commit", "-m", "code")
            before = git(root, "rev-parse", "HEAD")
            write(root, "docs/note.md", "changed\n")
            git(root, "add", "."); git(root, "commit", "-m", "docs")
            head = git(root, "rev-parse", "HEAD")
            event = {"action": "synchronize", "before": before, "after": head,
                     "pull_request": {"base": {"sha": base}, "head": {"sha": head}}}

            old, report = self._old_outputs(root, event, PATH_TABLES["app"])
            new = self._new_outputs(root, event)
            decided = decision.decide(event, root)

            self.assertEqual(new["diff_range"], f"{base}...{head}")
            # The old script scoped itself to the last push.
            self.assertEqual(report["diff_range"], f"{before}...{head}")
            self.assertNotEqual(report["diff_range"], new["diff_range"])
            # The contribution is the pull request's own, code included.
            self.assertIn("internal/lingdoc/workspace/service.go",
                          decided.contribution)


class BaselineMergeTests(GateFixture):
    """A baseline merge re-tests the contribution and nothing else."""

    def test_the_repository_matrix_stays_away_and_the_contribution_is_retested(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = self._repo(root)
            write(root, "internal/lingdoc/workspace/service.go", CODE)
            git(root, "add", "."); git(root, "commit", "-m", "code")
            before = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "main")
            write(root, "internal/evidence/source.go",
                  "package evidence\n\n// upstream\n")
            git(root, "add", "."); git(root, "commit", "-m", "upstream")
            new_base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "topic")
            git(root, "merge", "--no-edit", "main")
            head = git(root, "rev-parse", "HEAD")
            event = {"action": "synchronize", "before": before, "after": head,
                     "pull_request": {"base": {"sha": new_base}, "head": {"sha": head}}}

            new = self._new_outputs(root, event)

            # The repository-wide suites do not run again: the contribution
            # did not change, so there is nothing of main's to re-test.
            self.assertEqual(new["run_code"], "false")
            for name, table in PATH_TABLES.items():
                surface = next(surface for surface in manifest.load_surfaces()
                               if surface.name == name)
                for consumer in surface.consumers:
                    key = decision.output_key(consumer.workflow, consumer.job)
                    with self.subTest(surface=name, job=consumer.job):
                        self.assertEqual(new[key], "false")
            # The contribution itself is re-tested against the new baseline.
            self.assertEqual(new["base_moved"], "true")
            self.assertEqual(new["run_targeted"], "true")
            self.assertEqual(new["run_providers"], "true")


class PushEventTests(GateFixture):
    """A push to main is not a pull request; the contribution does not apply."""

    def test_a_push_answers_the_same_as_before(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            write(root, "internal/lingdoc/workspace/service.go", CODE)
            git(root, "add", "."); git(root, "commit", "-m", "code")
            before = git(root, "rev-parse", "HEAD~1")
            after = git(root, "rev-parse", "HEAD")
            event = {"ref": "refs/heads/main", "before": before, "after": after}

            old, _ = self._old_outputs(root, event, PATH_TABLES["app"],
                                       event_name="push")
            new = self._new_outputs(root, event)

            # The old script and the new decision answer the same: a push
            # carries no pull request, so there is no contribution surface to
            # select and the gate stays away.
            self.assertEqual(old["run"], "false")
            for name, table in PATH_TABLES.items():
                surface = next(surface for surface in manifest.load_surfaces()
                               if surface.name == name)
                for consumer in surface.consumers:
                    key = decision.output_key(consumer.workflow, consumer.job)
                    with self.subTest(surface=name, job=consumer.job):
                        self.assertEqual(new[key], "false")


class PullRequestGateTests(GateFixture):
    """The pull request gate's steps read the same verdicts as before."""

    def test_the_provider_and_frontend_steps_keep_their_verdicts(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = self._repo(root)
            event = self._pull_request(
                root, {"internal/lingdoc/workspace/service.go": CODE}, base)

            outputs = self._new_outputs(root, event)

            self.assertEqual(outputs["run_providers"],
                             "true" if outputs["provider_tests"] == "true"
                             and outputs["format_only"] != "true" else "false")
            self.assertEqual(outputs["run_frontend"],
                             "true" if outputs["frontend"] == "true"
                             and outputs["format_only"] != "true" else "false")

    def test_a_formatting_only_update_skips_the_test_steps(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = self._repo(root)
            event = self._pull_request(
                root, {"internal/lingdoc/workspace/service.go": COMMENT_ADDED},
                base)

            outputs = self._new_outputs(root, event)

            self.assertEqual(outputs["format_only"], "true")
            self.assertEqual(outputs["run_providers"], "false")
            self.assertEqual(outputs["run_frontend"], "false")


if __name__ == "__main__":
    unittest.main()
