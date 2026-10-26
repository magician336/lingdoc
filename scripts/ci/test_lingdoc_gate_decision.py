"""Tests for the gate decision module (``scripts/ci/lingdoc_gate_decision.py``).

The module is the deep module of the gate: it owns the diff ranges, the
changed files, the tree hashes, the tested surface, the gate-surface
classification and the formatting fast path.  Its public interface is
``decide`` / ``write_outputs`` / ``report`` / ``publish`` /
``gate_conclusion`` / ``conclusion_markdown`` / ``check_whitespace``.

This file is the **one seam** where the judgement semantics are tested: every
case below observes the decision through that interface.  The guard tests --
the wiring between the workflows, the manifest and the documents -- live in
their own ``test_lingdoc_*.py`` files.

These tests pin the behaviour ADR 0006 already established, one fixture per
rule:

* the contribution is ``base...head``, never the last push;
* the base moved when the two merge bases differ;
* a change to the gate itself is judged by the whole gate;
* the decision is a typed value, and only ``write_outputs`` stringifies it;
* an undecidable surface falls back to the full scope and says so;
* the formatting fast path keeps ADR 0005's mouth;
* the three artifact fields keep their names and their meaning;
* the per-job verdicts are the ones the old implementation gave.
"""
from __future__ import annotations

import ast
import contextlib
import dataclasses
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

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


def _job_runs(result: decision.Decision, workflow: str, job: str) -> bool:
    """Whether one job of the manifest is selected for this decision."""
    for entry in result.jobs:
        if entry.workflow == workflow and entry.job == job:
            return entry.run
    raise AssertionError(f"no job {job!r} in {workflow!r}")


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

    def _contribution(self, root: Path,
                      changes: dict[str, str]) -> decision.Decision:
        """The decision for a pull request that contributes exactly these files.

        The pull request carries one commit; the base is the commit it
        branches off, so the contribution is ``base...head``.
        """
        base = git(root, "rev-parse", "HEAD")
        git(root, "checkout", "-b", "topic")
        for relative, content in changes.items():
            write(root, relative, content)
        git(root, "add", "-A")
        git(root, "commit", "-m", "contribution")
        head = git(root, "rev-parse", "HEAD")
        return decision.decide(self._event(base, head, base), root)


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
            # The tested surface's own content did not change ...
            self.assertFalse(result.run_code)
            # ... and this change only reformats the code, so ADR 0005's
            # shortcut keeps the test jobs away.  The base move alone is not
            # what decides that: see BaseMoveCoverageTests.
            self.assertTrue(result.format_only)
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


class BaseMoveCoverageTests(DecisionFixture):
    """After a base move, the touched surface still has evidence (#602).

    The shape the ticket pins: a contribution that touches a surface which is
    neither a provider package nor covered by the pull request gate's provider
    step -- ``internal/database`` -- must not be left without a compile or
    test job on the new head.  Before the fix the App workflow skipped (the
    merge result was "not new" as far as the decision was concerned) and the
    pull request gate re-tested only the provider packages, so nothing
    compiled or tested that surface at all.
    """

    def _repo_with_a_database_package(self, root: Path) -> None:
        git(root, "init", "-b", "main")
        git(root, "config", "user.email", "synthetic@example.com")
        git(root, "config", "user.name", "Synthetic CI test")
        write(root, "README.md", "base\n")
        write(root, "internal/lingdoc/workspace/service.go", "package workspace\n")
        write(root, "internal/database/store.go", "package database\n")
        write(root, "cli/tool.go", "package cli\n")
        git(root, "add", ".")
        git(root, "commit", "-m", "base")

    def _contribution_whose_base_moved(self, root: Path,
                                       changes: dict[str, str]) -> decision.Decision:
        """A pull request whose base moved under it while its own code stayed.

        The upstream commit touches a file the contribution does not, so the
        contribution's surface is unchanged and only the base moved -- the
        shape issue #602 is about.
        """
        git(root, "checkout", "-b", "topic")
        for relative, content in changes.items():
            write(root, relative, content)
        git(root, "add", "-A")
        git(root, "commit", "-m", "contribution")
        before = git(root, "rev-parse", "HEAD")
        git(root, "checkout", "main")
        write(root, "docs/upstream.md", "upstream\n")
        git(root, "add", "."); git(root, "commit", "-m", "upstream")
        base = git(root, "rev-parse", "HEAD")
        git(root, "checkout", "topic")
        git(root, "merge", "--no-edit", "main")
        after = git(root, "rev-parse", "HEAD")
        return decision.decide(self._event(before, after, base), root)

    def test_the_app_workflow_does_not_skip_a_touched_database_surface(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo_with_a_database_package(root)

            result = self._contribution_whose_base_moved(
                root, {"internal/database/store.go": "package database\n\nfunc Store() string { return \"stored\" }\n"})

            self.assertTrue(result.base_moved)
            self.assertFalse(result.surface_changed)
            # The App workflow must not skip: its job is the full Go build and
            # test, and it is what compiles internal/database.
            self.assertTrue(_job_runs(result, ".github/workflows/app.yml", "verify"))
            # The PostgreSQL rehearsal lives in the integration gate.
            self.assertTrue(_job_runs(
                result, ".github/workflows/lingdoc-interface-integration.yml",
                "integration"))
            # And the conclusion says who verifies the surface.
            row = next(row for row in result.coverage
                       if row.surface == "internal/database")
            self.assertEqual(row.status, "verified")
            self.assertIn("Format, vet, test, and build", row.verified_by)
            self.assertFalse(result.uncovered)

    def test_the_scope_optimisation_still_holds_for_a_documentation_change(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo_with_a_database_package(root)

            result = self._contribution_whose_base_moved(
                root, {"docs/note.md": "evidence\n"})

            self.assertTrue(result.base_moved)
            # A contribution that touches no code starts no Go suite: the base
            # move alone is not a reason to run the repository-wide matrix.
            self.assertFalse(_job_runs(result, ".github/workflows/app.yml", "verify"))
            self.assertFalse(_job_runs(
                result, ".github/workflows/lingdoc-interface-integration.yml",
                "integration"))
            self.assertFalse(result.uncovered)

    def test_a_surface_the_gate_does_not_watch_is_reported_not_hidden(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo_with_a_database_package(root)

            result = self._contribution_whose_base_moved(
                root, {"cli/tool.go": "package cli\n\nfunc Tool() string { return \"tool\" }\n"})

            # The cli module has its own workflow, and the App workflow
            # excludes it on purpose: the gate makes no promise about it.
            # The conclusion must say so instead of leaving a silent blank.
            row = next(row for row in result.coverage if row.surface == "cli")
            self.assertEqual(row.status,
                             "not verified (no gate job covers this surface)")
            self.assertEqual(row.verified_by, ())
            # It is reported, not fatal: the gate does not own that module.
            self.assertFalse(result.uncovered)

    def _manifest_without_a_verifier(self) -> tuple:
        """A manifest in which no running job verifies internal/database.

        The two repository-level jobs no longer watch the package, and the
        only surface that still claims it is one whose step does not test it:
        the shape of the gap issue #602 is about.
        """
        patched = []
        for surface in manifest.load_surfaces():
            if surface.name in ("app", "lingdoc-interface-integration"):
                patched.append(dataclasses.replace(surface, paths=("frontend/**",)))
            elif surface.name == "pr-gate-provider":
                patched.append(dataclasses.replace(
                    surface, paths=surface.paths + ("internal/database/",)))
            else:
                patched.append(surface)
        return tuple(patched)

    def test_a_surface_nobody_tests_fails_the_gate(self):
        """A surface a verifying job watches but nobody tests is fatal."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo_with_a_database_package(root)
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            write(root, "internal/database/store.go",
                  "package database\n\nfunc Store() string { return \"stored\" }\n")
            git(root, "add", "-A"); git(root, "commit", "-m", "contribution")
            head = git(root, "rev-parse", "HEAD")
            event = self._event(base, head, base)

            with mock.patch.object(decision, "_manifest_surfaces",
                                   return_value=self._manifest_without_a_verifier()):
                result = decision.decide(event, root)

            self.assertTrue(result.surface_changed)
            self.assertTrue(result.uncovered)
            self.assertIn("no job that compiles or tests", result.reason)
            row = next(row for row in result.coverage
                       if row.surface == "internal/database")
            self.assertEqual(row.status, "not verified")
            self.assertIn("internal/database", result.reason)

    def test_the_gate_fails_the_scope_step_when_a_surface_has_no_evidence(self):
        """The scope step fails, so the gate cannot pass on an unverified surface."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            scratch = Path(tmp) / "scratch"
            scratch.mkdir()
            self._repo_with_a_database_package(root)
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            write(root, "internal/database/store.go",
                  "package database\n\nfunc Store() string { return \"stored\" }\n")
            git(root, "add", "-A"); git(root, "commit", "-m", "contribution")
            head = git(root, "rev-parse", "HEAD")
            event = self._event(base, head, base)

            with mock.patch.object(decision, "_manifest_surfaces",
                                   return_value=self._manifest_without_a_verifier()):
                with self.assertRaises(SystemExit) as caught:
                    decision.publish(event, root, require_coverage=True,
                                     whitespace=False)

            self.assertNotEqual(caught.exception.code, 0)

    def test_the_scope_step_succeeds_when_every_surface_is_verified(self):
        """The release condition never fires on a covered head."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            scratch = Path(tmp) / "scratch"
            scratch.mkdir()
            self._repo_with_a_database_package(root)
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            write(root, "internal/database/store.go",
                  "package database\n\nfunc Store() string { return \"stored\" }\n")
            git(root, "add", "-A"); git(root, "commit", "-m", "contribution")
            head = git(root, "rev-parse", "HEAD")
            event_path = scratch / "event.json"
            event_path.write_text(json.dumps(self._event(base, head, base)),
                                  encoding="utf-8")

            result = subprocess.run(
                [sys.executable, str(CI_DIR / "lingdoc_gate_decision.py"),
                 "--event", str(event_path), "--root", str(root),
                 "--require-coverage", "--no-whitespace"],
                capture_output=True, text=True, encoding="utf-8",
                errors="replace")

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotIn("no job that compiles or tests", result.stderr)

    def test_the_conclusion_shows_which_surface_was_verified(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo_with_a_database_package(root)

            result = self._contribution_whose_base_moved(
                root, {"internal/database/store.go": "package database\n\nfunc Store() string { return \"stored\" }\n"})
            conclusion = decision.gate_conclusion(
                result, ".github/workflows/lingdoc-review.yml", root=root)
            markdown = decision.conclusion_markdown(conclusion)

            self.assertTrue(conclusion["coverage"])
            self.assertEqual(conclusion["coverage"][0]["surface"], "internal/database")
            self.assertEqual(conclusion["coverage"][0]["status"], "verified")
            self.assertIn("| Surface | Paths | Verified by | Status |", markdown)
            self.assertIn("internal/database", markdown)
            self.assertIn("Format, vet, test, and build", markdown)


class ForcePushTests(DecisionFixture):
    """A force push makes ``before`` unfetchable; the scope still narrows (#539).

    A force push replaces the branch's history, so the CI runner never
    fetched the ``before`` head and ``git diff base...before`` fails.  Before
    the fix that one failure threw the whole judgement into the full-scope
    fallback: a pull request whose contribution was one path-map document
    and one registry script (PR #508) then ran the Go integration job for
    more than twenty minutes.

    The previous surface is an **absence claim** -- it is what lets the gate
    say "nothing changed".  A ``before`` that cannot be read gives that claim
    up, exactly as an opened pull request does; the contribution, which
    resolved, still narrows the scope.  Only the current head being
    unreadable is a failed judgement (see ``MalformedEventTests``).
    """

    # A well-formed SHA that no fetch will ever resolve: the shape the CI
    # runner saw for the pre-force-push head.
    ABSENT = "0" * 39 + "1"

    def test_an_unfetchable_previous_head_keeps_the_scope_narrow(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            result = self._contribution(
                root, {"docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md": "path map\n",
                       "scripts/lingdoc_parallel_surfaces.py": "STATE = {}\n"})
            head = git(root, "rev-parse", "HEAD")
            event = self._event(self.ABSENT, head, result.base)

            replay = decision.decide(event, root)

            # The judgement did not fail: the scope is narrowed, not forced.
            self.assertFalse(replay.forced)
            self.assertFalse(replay.undecided)
            self.assertNotIn("fell back to the full scope", replay.reason)
            # The contribution is the pull request's own two non-Go files.
            self.assertEqual(
                set(replay.contribution),
                {"docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md",
                 "scripts/lingdoc_parallel_surfaces.py"})
            # The "unchanged" shortcut is given up -- that is the
            # conservative direction -- but no Go gate is selected by it.
            self.assertTrue(replay.surface_changed)
            self.assertFalse(_job_runs(
                replay, ".github/workflows/lingdoc-interface-integration.yml",
                "integration"))
            self.assertFalse(_job_runs(
                replay, ".github/workflows/app.yml", "verify"))
            outputs = decision.write_outputs(replay)
            self.assertEqual(
                outputs["job_lingdoc_interface_integration_integration"],
                "false")

    def test_an_unfetchable_previous_head_still_selects_the_go_gates(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            # A real code change, not a comment: the formatting fast path
            # (ADR 0005) would rightly keep the test jobs away from a
            # comment-only update.
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\nfunc Extra() string { return \"extra\" }\n")
            git(root, "add", ".")
            git(root, "commit", "-m", "code")
            head = git(root, "rev-parse", "HEAD")

            replay = decision.decide(self._event(self.ABSENT, head, base), root)

            # Narrowing never weakens the Go gates: a contribution that
            # touches the integration surface still runs it, forced or not.
            self.assertFalse(replay.forced)
            self.assertTrue(_job_runs(
                replay, ".github/workflows/lingdoc-interface-integration.yml",
                "integration"))
            self.assertTrue(_job_runs(
                replay, ".github/workflows/app.yml", "verify"))
            self.assertEqual(replay.provider_packages,
                             ("./internal/lingdoc/workspace/...",))
            self.assertTrue(replay.provider_tests)

    def test_an_acceptance_script_change_still_selects_integration(self):
        """The integration job runs the acceptance scripts, so it watches them.

        This pins the design decision the same ticket re-asked (#528): a
        change under ``scripts/acceptance/**`` selects the integration job,
        because that job executes those scripts.  The force-push fix must
        not narrow this away.
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            write(root, "scripts/acceptance/beta_material_ingest.py",
                  "import sys\n")
            git(root, "add", ".")
            git(root, "commit", "-m", "evidence script")
            head = git(root, "rev-parse", "HEAD")
            # PR #528's shape: the pull request opens with the script.
            opened = {"action": "opened",
                      "pull_request": {"base": {"sha": base},
                                       "head": {"sha": head}}}

            result = decision.decide(opened, root)

            self.assertTrue(_job_runs(
                result, ".github/workflows/lingdoc-interface-integration.yml",
                "integration"))
            self.assertFalse(_job_runs(
                result, ".github/workflows/app.yml", "verify"))


class GovernanceTests(DecisionFixture):
    """The gate judges itself: a change to the gate runs the whole gate."""

    def test_a_change_to_the_gate_itself_is_judged_by_the_whole_gate(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            write(root, "scripts/ci/lingdoc_gate_decision.py", "# gate\n")
            write(root, "internal/lingdoc/workspace/service.go", "package workspace\n")
            write(root, "internal/evidence/source.go", "package evidence\n")
            git(root, "add", ".")
            git(root, "commit", "-m", "base")
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            before = git(root, "rev-parse", "HEAD")
            write(root, "scripts/ci/lingdoc_gate_decision.py", "# gate changed\n")
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


class JobVerdictGoldenTests(DecisionFixture):
    """The per-job verdicts the old implementation produced, kept as literals.

    These are the answers the workflow-level scope script gave before the
    decision module existed, recorded job by job.  They are the independent
    source of truth for "the rewiring changed the structure, not the
    verdicts": the shell that produced them is gone, so the comparison now
    runs against these literals.

    The pull request gate is listed with them: it is the consumer of the
    targeted surfaces, and since #309 it appears on every pull request -- its
    job runs and its steps are skipped one by one.
    """

    # The jobs that run, per scenario.  A job is named
    # ``<workflow file>#<job key>``.  The pull request gate is the consumer
    # of the targeted surfaces, so it runs whenever one of them does.
    CODE_CHANGE = {
        "app.yml#verify",
        "go-lint.yml#changes",
        "go-lint.yml#golangci",
        "lingdoc-interface-integration.yml#integration",
        "lingdoc-interface-integration.yml#frontend",
        "lingdoc-review.yml#gate",
    }
    FRONTEND_CHANGE = {"frontend.yml#verify", "frontend.yml#docker-image",
                       "lingdoc-review.yml#gate"}
    # A formatting-only change keeps the meaning of the code: only the lint
    # jobs run, because lint is the judge of formatting.  The pull request
    # gate still runs: it is the gate that publishes the conclusion.
    FORMAT_ONLY_CHANGE = {"go-lint.yml#changes", "go-lint.yml#golangci",
                          "lingdoc-review.yml#gate"}
    UNRELATED_CHANGE: set[str] = set()

    def _event_for(self, root: Path, changes: dict[str, str]) -> dict:
        """A pull request that carries one commit with the given changes."""
        git(root, "checkout", "-b", "topic")
        for relative, content in changes.items():
            write(root, relative, content)
        git(root, "add", "-A")
        git(root, "commit", "-m", "change")
        head = git(root, "rev-parse", "HEAD")
        base = git(root, "merge-base", "main", head)
        return {"action": "synchronize", "before": base, "after": head,
                "pull_request": {"base": {"sha": base}, "head": {"sha": head}}}

    def _running_jobs(self, root: Path, event: dict) -> set[str]:
        """The jobs the decision selects for this event."""
        outputs = decision.write_outputs(decision.decide(event, root))
        running = set()
        for surface in manifest.load_surfaces():
            for consumer in surface.consumers:
                key = decision.output_key(consumer.workflow, consumer.job)
                if outputs.get(key) == "true":
                    running.add(f"{Path(consumer.workflow).name}#{consumer.job}")
        return running

    def test_a_code_change_runs_the_same_jobs_as_before(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            event = self._event_for(
                root, {"internal/lingdoc/workspace/service.go":
                       "package workspace\n\nfunc F() int { return 43 }\n"})

            self.assertEqual(self._running_jobs(root, event), self.CODE_CHANGE)

    def test_a_frontend_change_runs_the_same_jobs_as_before(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            event = self._event_for(
                root, {"frontend/src/views/lingdoc/Workspace.vue":
                       "<template>changed</template>\n"})

            self.assertEqual(self._running_jobs(root, event),
                             self.FRONTEND_CHANGE)

    def test_a_formatting_only_change_skips_the_same_jobs_as_before(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            event = self._event_for(
                root, {"internal/lingdoc/workspace/service.go":
                       "package workspace\n\n// A note.\n// Another note.\n"})

            self.assertEqual(self._running_jobs(root, event),
                             self.FORMAT_ONLY_CHANGE)

    def test_an_unrelated_change_runs_no_job_as_before(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            event = self._event_for(root, {"docs/note.md": "changed\n"})

            self.assertEqual(self._running_jobs(root, event),
                             self.UNRELATED_CHANGE)


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

    One value changed with ticket #301: the formatting judgement of the
    ``baseline_merge`` scenario.  The decision no longer looks at the last
    push but at the pull request's own contribution, and that contribution
    carries a documentation file, so it is not a formatting-only update.

    Two values changed with ticket #602: the ``run`` verdict and the ``reason``
    of the ``baseline_merge`` scenario.  A base move makes the merge result
    the repository-level gates test new, so the gate that watches the
    contribution has to run again instead of leaving the contribution's
    surface without any compile or test evidence on the new head.
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
            "run": "true", "format_only": "false",
            "surface": "internal/lingdoc/workspace",
            "base_moved": "true", "surface_changed": "false",
            "reason": "the base moved under the branch; the merge result is "
                      "new, so the contribution is re-tested against it",
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


class OfflineEntryPointTests(DecisionFixture):
    """The decision can be reproduced offline, from an event and a work tree."""

    def test_the_command_prints_the_decision_without_a_network(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "repo"
            scratch = Path(tmp) / "scratch"
            root.mkdir()
            scratch.mkdir()
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            before = git(root, "rev-parse", "HEAD")
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            after = git(root, "rev-parse", "HEAD")
            event_path = scratch / "event.json"
            event_path.write_text(json.dumps(self._event(before, after, base)),
                                  encoding="utf-8")

            result = subprocess.run(
                [sys.executable, str(CI_DIR / "lingdoc_gate_decision.py"),
                 "--event", str(event_path), "--root", str(root)],
                capture_output=True, text=True)

            self.assertEqual(result.returncode, 0, result.stderr)
            report = json.loads(result.stdout)
            self.assertEqual(report["decision"]["surface"], "internal/lingdoc/workspace")
            self.assertEqual(report["decision"]["reason"], "contribution surface changed")
            self.assertEqual(report["decision"]["base"], base)
            self.assertTrue(report["jobs"])

    def test_the_command_reaches_no_network(self):
        source = (CI_DIR / "lingdoc_gate_decision.py").read_text(encoding="utf-8")
        imported = set()
        for node in ast.walk(ast.parse(source)):
            if isinstance(node, ast.Import):
                imported.update(alias.name.split(".")[0] for alias in node.names)
            elif isinstance(node, ast.ImportFrom) and node.module:
                imported.add(node.module.split(".")[0])
        self.assertEqual(imported & {"socket", "http", "urllib", "requests"}, set())


class PullRequestGateWiringTests(DecisionFixture):
    """The pull request gate takes its decision from the decision module."""

    WORKFLOW = CI_DIR.parents[1] / ".github" / "workflows" / "lingdoc-review.yml"

    def test_the_gate_scope_step_asks_the_decision_module(self):
        workflow = self.WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("scripts/ci/lingdoc_gate_decision.py", workflow)
        # The decision module is the only entry point: no scope script is
        # called any more.
        self.assertNotRegex(workflow, r"lingdoc_\w*_scope\.py")

    def test_the_gate_still_publishes_the_three_artifact_fields(self):
        workflow = self.WORKFLOW.read_text(encoding="utf-8")
        for field in ("surface", "reason", "base"):
            self.assertIn(f"steps.scope.outputs.{field}", workflow)
        self.assertIn("result['surface']", workflow)
        self.assertIn("result['reason']", workflow)
        self.assertIn("result['base']", workflow)

    def test_the_gate_scope_step_publishes_the_three_fields_as_outputs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "repo"
            scratch = Path(tmp) / "scratch"
            root.mkdir()
            scratch.mkdir()
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            self._topic(root)
            before = git(root, "rev-parse", "HEAD")
            write(root, "internal/lingdoc/workspace/service.go",
                  "package workspace\n\n// change\n")
            git(root, "add", "."); git(root, "commit", "-m", "code")
            after = git(root, "rev-parse", "HEAD")
            event_path = scratch / "event.json"
            event_path.write_text(json.dumps(self._event(before, after, base)),
                                  encoding="utf-8")
            output = scratch / "outputs.txt"

            result = subprocess.run(
                [sys.executable, str(CI_DIR / "lingdoc_gate_decision.py"),
                 "--event", str(event_path), "--root", str(root)],
                env={**os.environ, "GITHUB_OUTPUT": str(output)},
                capture_output=True, text=True)

            self.assertEqual(result.returncode, 0, result.stderr)
            written = output.read_text(encoding="utf-8")
            self.assertIn(f"base={base}", written)
            self.assertIn("surface=internal/lingdoc/workspace", written)
            self.assertIn("reason=contribution surface changed", written)


class ScopeClassificationTests(DecisionFixture):
    """Which surface a contribution belongs to (ADR 0006's classification)."""

    def _scope_of(self, changes: dict[str, str]) -> tuple[bool, bool, bool]:
        """The (contracts, backend, frontend) flags of one contribution."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            result = self._contribution(root, changes)
            return (result.contracts, result.backend, result.frontend)

    def test_a_planning_document_only_selects_the_contract_checks(self):
        for path in ("docs/06-灵档产品开发规划/05-团队分工与代码边界.md",
                     "docs/TEAM_DEVELOPMENT_TASKS.md"):
            with self.subTest(path=path):
                self.assertEqual(self._scope_of({path: "plan\n"}),
                                 (True, False, False))

    def test_a_contract_change_selects_the_contract_checks(self):
        # The contract examples are re-validated.  The tested code surface is
        # untouched, so no package is re-tested: the frontend flag also asks
        # whether the surface behind it moved.
        self.assertEqual(
            self._scope_of({"docs/08-本轮实施方案/contracts/openapi.json": "{}\n"}),
            (True, True, False))

    def test_a_contract_change_counts_as_a_frontend_change(self):
        # A contract change changes what the frontend consumes, so it is a
        # frontend-scope change: together with a frontend change it selects
        # the frontend surface.
        self.assertEqual(
            self._scope_of({
                "docs/08-本轮实施方案/contracts/openapi.json": "{}\n",
                "frontend/src/utils/request.ts": "export const x = 1\n"}),
            (True, True, True))

    def test_a_provider_or_shared_backend_change_selects_the_backend(self):
        for path, content in (
                ("internal/lingdoc/workspacecore/service.go", "package workspacecore\n"),
                ("internal/evidence/source.go", "package evidence\n\n// change\n"),
                ("internal/router/router.go", "package router\n"),
                ("migrations/sqlite/a.sql", "-- migration\n"),
                ("go.mod", "module example\n"),
                ("go.sum", ""),
                ("cmd/server/main.go", "package main\n"),
                ("config/config.yaml", "key: value\n"),
                ("internal/models/acceptancebudget/reconcile.go", "package budget\n"),
                ("cmd/acceptance-budget-reconcile/main.go", "package main\n")):
            with self.subTest(path=path):
                self.assertEqual(self._scope_of({path: content}),
                                 (True, True, False))

    def test_a_frontend_change_selects_the_frontend(self):
        for path, content in (
                ("frontend/src/views/lingdoc/Workspace.vue", "<template>changed</template>\n"),
                ("frontend/src/utils/request.ts", "export const x = 1\n"),
                ("frontend/package-lock.json", "{}\n")):
            with self.subTest(path=path):
                self.assertEqual(self._scope_of({path: content}),
                                 (True, False, True))

    def test_a_governance_change_checks_its_contract_dependency(self):
        for path, content in (
                (".github/workflows/lingdoc-review.yml", "name: gate\n"),
                (".github/pull_request_template.md", "# template\n"),
                ("scripts/ci/requirements-lingdoc.txt", "PyYAML==6.0.3\n")):
            with self.subTest(path=path):
                self.assertEqual(self._scope_of({path: content}),
                                 (True, True, True))

    def test_an_unrelated_change_selects_nothing(self):
        for path, content in (("README.md", "changed\n"),
                              ("cli/cmd/doc/get.go", "package main\n"),
                              ("client/knowledge.go", "package client\n")):
            with self.subTest(path=path):
                self.assertEqual(self._scope_of({path: content}),
                                 (False, False, False))

    def test_an_empty_contribution_selects_nothing(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")

            result = decision.decide(self._event(base, base, base), root)

            self.assertEqual((result.contracts, result.backend,
                              result.frontend), (False, False, False))


class ProviderSelectionTests(DecisionFixture):
    """The provider packages the decision claims, and only those."""

    def _full_scope(self, root: Path) -> decision.Decision:
        return decision.decide({"action": "manual",
                                "reason": "full scope requested"}, root)

    def test_a_manual_run_claims_only_the_provider_roots_that_exist(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / "internal/evidence/source.go"
            path.parent.mkdir(parents=True)
            path.write_text("package evidence\n", encoding="utf-8")

            result = self._full_scope(root)

            self.assertEqual(result.provider_packages, ("./internal/evidence/...",))
            self.assertTrue(result.run_code)
            self.assertTrue(result.run_targeted)
            self.assertTrue(result.contracts)
            self.assertTrue(result.backend)
            self.assertTrue(result.frontend)

    def test_the_budget_package_is_claimed_without_the_other_provider_roots(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / "internal/models/acceptancebudget/reconcile_test.go"
            path.parent.mkdir(parents=True)
            path.touch()

            result = self._full_scope(root)

            self.assertEqual(result.provider_packages,
                             ("./internal/models/acceptancebudget/...",))

    def test_the_reconciliation_command_is_claimed_on_its_own(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / "cmd/acceptance-budget-reconcile/main_test.go"
            path.parent.mkdir(parents=True)
            path.touch()

            result = self._full_scope(root)

            self.assertEqual(result.provider_packages,
                             ("./cmd/acceptance-budget-reconcile/...",))

    def test_every_provider_root_reaches_the_command_line_targets(self):
        roots = ("internal/lingdoc", "internal/evidence",
                 "internal/models/acceptancebudget", "cmd/acceptance-budget-reconcile")
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for name in roots:
                path = root / name / "package_test.go"
                path.parent.mkdir(parents=True)
                path.touch()
            output = root / "outputs.txt"

            result = subprocess.run(
                [sys.executable, str(CI_DIR / "lingdoc_gate_decision.py"),
                 "--all", "--root", str(root)],
                env={**os.environ, "GITHUB_OUTPUT": str(output)},
                capture_output=True, text=True)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(result.stdout)["provider_packages"],
                             [f"./{name}/..." for name in roots])
            self.assertIn("provider_tests=true", output.read_text(encoding="utf-8"))


class PatternTableTests(DecisionFixture):
    """The path table of a gate selects the event (the ``run`` verdict)."""

    def _run_verdict(self, changes: dict[str, str],
                     patterns: list[str]) -> str:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            result = self._contribution(root, changes)
            return decision.write_outputs(result, patterns=patterns)["run"]

    def test_a_documentation_only_contribution_selects_no_code_gate(self):
        self.assertEqual(
            self._run_verdict({"docs/report.md": "report\n"},
                              ["internal/lingdoc/**", "**/*.go"]),
            "false")

    def test_a_code_path_selects_the_gate(self):
        self.assertEqual(
            self._run_verdict(
                {"internal/lingdoc/workspace/service.go": "package workspace\n\n// c\n"},
                ["internal/lingdoc/**", "**/*.go"]),
            "true")

    def test_a_double_star_matches_a_file_at_the_repository_root(self):
        self.assertEqual(self._run_verdict({"main.go": "package main\n"},
                                           ["**/*.go"]), "true")

    def test_a_negative_pattern_keeps_an_unrelated_module_out(self):
        patterns = ["**/*.go", "!cli/**", "!client/**"]
        self.assertEqual(
            self._run_verdict({"cli/main.go": "package main\n"}, patterns), "false")
        self.assertEqual(
            self._run_verdict({"internal/main.go": "package internal\n"}, patterns),
            "true")


class MalformedEventTests(DecisionFixture):
    """A malformed event costs compute, never correctness."""

    def test_a_malformed_commit_sha_is_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            event = self._event(base, base, base)
            event["pull_request"]["head"]["sha"] = "$(touch injected)"
            event["after"] = "$(touch injected)"

            result = decision.decide(event, root)

            self.assertTrue(result.forced)
            self.assertTrue(result.undecided)
            self.assertIn("risk surface undetermined", result.reason)
            self.assertFalse((root / "injected").exists())

    def test_an_event_without_a_pull_request_falls_back_to_the_full_scope(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)

            result = decision.decide({}, root)

            self.assertTrue(result.forced)
            self.assertTrue(result.run_code)
            self.assertTrue(result.run_targeted)
            self.assertTrue(result.contracts)
            self.assertTrue(result.backend)
            self.assertTrue(result.frontend)


class FormatClassifierTests(DecisionFixture):
    """ADR 0005's mouth: only Go, never a compiler directive, never a guess."""

    PROGRAM = ("package sample\n\n"
               "// A documented function.\n"
               "func Value() int {\n"
               "\treturn 42\n"
               "}\n")

    def _format_verdict(self, before: dict[str, str], after: dict[str, str],
                        action: str = "synchronize") -> decision.Decision:
        """The decision for a pull request update that rewrites these files."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            git(root, "init", "-b", "main")
            git(root, "config", "user.email", "synthetic@example.com")
            git(root, "config", "user.name", "Synthetic CI test")
            for relative, content in before.items():
                write(root, relative, content)
            git(root, "add", "-A")
            git(root, "commit", "-m", "base")
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            for relative, content in after.items():
                write(root, relative, content)
            for relative in before:
                if relative not in after:
                    (root / relative).unlink()
            git(root, "add", "-A")
            git(root, "commit", "-m", "update")
            head = git(root, "rev-parse", "HEAD")
            event = self._event(base, head, base)
            event["action"] = action
            return decision.decide(event, root)

    def _program_change(self, after: str) -> decision.Decision:
        return self._format_verdict({"sample.go": self.PROGRAM},
                                     {"sample.go": after})

    # --- judgement (b): comments and whitespace only ------------------------

    def test_a_comment_only_change_is_a_formatting_only_update(self):
        result = self._program_change(
            self.PROGRAM.replace("// A documented function.",
                                  "// A documented function.\n// It returns a constant."))

        self.assertTrue(result.format_only, result.format_reasons)
        self.assertTrue(any("sample.go" in reason for reason in result.format_reasons))

    def test_a_whitespace_only_change_is_a_formatting_only_update(self):
        result = self._program_change(self.PROGRAM.replace("\treturn 42",
                                                           "    return 42"))

        self.assertTrue(result.format_only, result.format_reasons)

    def test_adding_a_blank_line_is_a_formatting_only_update(self):
        result = self._program_change(self.PROGRAM.replace("func Value() int {",
                                                           "\nfunc Value() int {"))

        self.assertTrue(result.format_only, result.format_reasons)

    # --- judgement (a): gofmt equivalence -----------------------------------

    def test_gofmt_output_is_a_formatting_only_update(self):
        # gofmt sorts the imports inside a block.  That changes the token
        # order, so only the gofmt judgement can accept it.
        before = "package sample\n\nimport (\n\t\"os\"\n\t\"fmt\"\n)\n"
        after = "package sample\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n"

        result = self._format_verdict({"sample.go": before}, {"sample.go": after})

        self.assertTrue(result.format_only, result.format_reasons)
        self.assertTrue(any("gofmt" in reason for reason in result.format_reasons))

    # --- judgement (b) must not be fooled by string literals ----------------

    def test_whitespace_inside_a_string_literal_is_not_a_formatting_only_change(self):
        before = 'package sample\n\nfunc Value() string {\n\treturn "a b"\n}\n'
        after = 'package sample\n\nfunc Value() string {\n\treturn "a  b"\n}\n'

        result = self._format_verdict({"sample.go": before}, {"sample.go": after})

        self.assertFalse(result.format_only, result.format_reasons)

    def test_a_comment_marker_inside_a_string_is_not_a_comment(self):
        before = 'package sample\n\nfunc Value() string {\n\treturn "http://example.com/x"\n}\n'
        after = 'package sample\n\nfunc Value() string {\n\treturn "http://example.com/y"\n}\n'

        result = self._format_verdict({"sample.go": before}, {"sample.go": after})

        self.assertFalse(result.format_only, result.format_reasons)

    def test_a_raw_string_literal_is_kept_verbatim(self):
        before = "package sample\n\nvar s = `raw  text`\n"
        after = "package sample\n\nvar s = `raw   text`\n"

        result = self._format_verdict({"sample.go": before}, {"sample.go": after})

        self.assertFalse(result.format_only, result.format_reasons)

    # --- judgement (b) must not be fooled by code changes -------------------

    def test_a_logic_change_is_not_a_formatting_only_change(self):
        result = self._program_change(self.PROGRAM.replace("return 42", "return 43"))

        self.assertFalse(result.format_only, result.format_reasons)

    def test_an_added_code_line_is_not_a_formatting_only_change(self):
        result = self._program_change(
            self.PROGRAM.replace("\treturn 42", "\tx := 1\n\treturn x"))

        self.assertFalse(result.format_only, result.format_reasons)

    def test_reordered_code_lines_are_not_a_formatting_only_change(self):
        result = self._program_change(
            self.PROGRAM.replace("\treturn 42", "\tx := 1\n\ty := 2\n\treturn x + y"))

        self.assertFalse(result.format_only, result.format_reasons)

    def test_a_compiler_directive_change_is_never_a_formatting_only_change(self):
        before = "//go:build linux\n\npackage sample\n\nfunc Value() int {\n\treturn 42\n}\n"
        after = "//go:build windows\n\npackage sample\n\nfunc Value() int {\n\treturn 42\n}\n"

        result = self._format_verdict({"sample.go": before}, {"sample.go": after})

        self.assertFalse(result.format_only, result.format_reasons)
        self.assertTrue(any("directive" in reason for reason in result.format_reasons))

    # --- mixed and non-Go changes -------------------------------------------

    def test_a_mixed_change_is_not_a_formatting_only_change(self):
        result = self._format_verdict(
            {"sample.go": self.PROGRAM},
            {"sample.go": self.PROGRAM.replace("return 42", "return 43"),
             "other.go": "package sample\n\nfunc Other() int {\n\treturn 1\n}\n"})

        self.assertFalse(result.format_only, result.format_reasons)
        self.assertTrue(any("other.go" in reason for reason in result.format_reasons))

    def test_a_non_go_change_is_not_a_formatting_only_change(self):
        # Documentation-only updates are handled by the path-based scope; the
        # content-level verdict must not claim them as formatting.
        result = self._format_verdict({"sample.go": self.PROGRAM},
                                      {"sample.go": self.PROGRAM,
                                       "notes.md": "hello\n"})

        self.assertFalse(result.format_only, result.format_reasons)

    def test_an_added_file_is_not_a_formatting_only_change(self):
        result = self._format_verdict(
            {"sample.go": self.PROGRAM},
            {"sample.go": self.PROGRAM,
             "added.go": "package sample\n\nfunc Added() int {\n\treturn 1\n}\n"})

        self.assertFalse(result.format_only, result.format_reasons)

    def test_a_deleted_file_is_not_a_formatting_only_change(self):
        result = self._format_verdict({"sample.go": self.PROGRAM}, {})

        self.assertFalse(result.format_only, result.format_reasons)

    # --- the fast path only applies to a pull request update ----------------

    def test_an_opened_pull_request_runs_the_full_matrix(self):
        result = self._program_change(
            self.PROGRAM.replace("// A documented function.",
                                  "// A documented function.\n// It returns a constant."))
        # Re-judge the same change as an opened pull request.
        opened = self._format_verdict(
            {"sample.go": self.PROGRAM},
            {"sample.go": self.PROGRAM.replace(
                "// A documented function.",
                "// A documented function.\n// It returns a constant.")},
            action="opened")

        self.assertTrue(result.format_only, result.format_reasons)
        self.assertFalse(opened.format_only, opened.format_reasons)


class WhitespaceBoundaryTests(DecisionFixture):
    """The pull request gate's boundary check covers the complete range."""

    def _whitespace_fixture(self, root: Path):
        git(root, "init", "-b", "main")
        git(root, "config", "user.email", "synthetic@example.com")
        git(root, "config", "user.name", "Synthetic CI test")
        (root / "base.txt").write_text("base\n", encoding="utf-8")
        git(root, "add", "."); git(root, "commit", "-m", "base")
        git(root, "checkout", "-b", "topic")
        (root / "topic.md").write_text("topic\n", encoding="utf-8")
        git(root, "add", "."); git(root, "commit", "-m", "topic")
        before = git(root, "rev-parse", "HEAD")
        git(root, "checkout", "main")
        (root / "upstream.sh").write_text("upstream trailing space  \n",
                                          encoding="utf-8")
        git(root, "add", "."); git(root, "commit", "-m", "upstream")
        base = git(root, "rev-parse", "HEAD")
        git(root, "checkout", "topic")
        git(root, "merge", "--no-edit", "main")
        return base, before

    def _run_gate(self, root: Path, event: dict) -> subprocess.CompletedProcess:
        event_path = root / "event.json"
        event_path.write_text(json.dumps(event), encoding="utf-8")
        return subprocess.run(
            [sys.executable, str(CI_DIR / "lingdoc_gate_decision.py"),
             "--event", str(event_path), "--root", str(root)],
            capture_output=True, text=True)

    def test_an_upstream_merge_is_not_a_pull_request_defect(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base, before = self._whitespace_fixture(root)
            head = git(root, "rev-parse", "HEAD")
            event = self._event(before, head, base)

            result = self._run_gate(root, event)

            self.assertEqual(result.returncode, 0, result.stderr)
            report = json.loads(result.stdout)
            self.assertEqual(report["whitespace_diff_range"], f"{base}...{head}")
            # The report names the pull request's own contribution.  The
            # upstream merge is not part of it: the baseline already carries
            # the upstream file, so the merge adds nothing of the pull
            # request's own.
            self.assertIn("topic.md", report["changed_paths"])
            self.assertNotIn("upstream.sh", report["changed_paths"])

    def test_whitespace_of_an_older_commit_is_still_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base, _ = self._whitespace_fixture(root)
            (root / "topic.md").write_text("topic trailing space  \n",
                                           encoding="utf-8")
            git(root, "add", "topic.md")
            git(root, "commit", "-m", "bad topic whitespace")
            before = git(root, "rev-parse", "HEAD")
            (root / "followup.md").write_text("clean followup\n", encoding="utf-8")
            git(root, "add", "followup.md")
            git(root, "commit", "-m", "clean latest push")
            head = git(root, "rev-parse", "HEAD")
            event = self._event(before, head, base)

            result = self._run_gate(root, event)

            self.assertNotEqual(result.returncode, 0,
                                "Full PR whitespace must not be skipped")
            self.assertIn("topic.md", result.stderr)


class PushEventTests(DecisionFixture):
    """A push is judged by the diff it delivers (#308).

    A pull request carries a contribution: the range ``base...head``.  A push
    carries no pull request, but it does carry a range of its own -- GitHub
    reports the ``before`` and ``after`` commits of the push -- so the push's
    tested surface is the diff the push delivers, and the push's baseline is
    the commit it started from.
    """

    def _push(self, root: Path, changes: dict[str, str],
              message: str) -> dict[str, str]:
        """Commit the changes and return the push event GitHub would send."""
        for relative, content in changes.items():
            write(root, relative, content)
        git(root, "add", "-A")
        git(root, "commit", "-m", message)
        after = git(root, "rev-parse", "HEAD")
        before = git(root, "rev-parse", "HEAD^")
        return {"ref": "refs/heads/main", "before": before, "after": after}

    def _outputs(self, root: Path, event: dict) -> dict[str, str]:
        return decision.write_outputs(decision.decide(event, root))

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

    def test_a_formatting_only_push_still_runs_the_test_jobs(self):
        # ADR 0005: a push never takes the formatting-only fast path.
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


class ManifestUnavailableTests(DecisionFixture):
    """An unreadable manifest costs compute, never correctness.

    ADR 0007 says it in one sentence: a judgement that fails -- an
    unresolvable range, a commit that does not exist, **a manifest that
    cannot be read** -- falls back to the full scope and says so.  There is
    one recovery path, and the manifest is part of the judgement, so the
    manifest takes the same path.
    """

    def _decide_with_manifest(self, root: Path, records, *, repo_root=None):
        """The decision for a pull request, with a broken manifest in place."""
        head = git(root, "rev-parse", "HEAD")
        event = self._event(head, head, head)
        with contextlib.ExitStack() as stack:
            stack.enter_context(mock.patch.object(manifest, "SURFACES", records))
            if repo_root is not None:
                stack.enter_context(
                    mock.patch.object(manifest, "REPO_ROOT", repo_root))
            return decision.decide(event, root)

    def _record(self, **overrides) -> dict:
        """A manifest record that names one real workflow job."""
        record = {
            "name": "example-surface",
            "gate_level": manifest.GATE_LEVEL_REPOSITORY,
            "path_kind": manifest.PATH_KIND_GLOB,
            "role": manifest.ROLE_PATHS,
            "paths": ("frontend/**",),
            "consumers": ({
                "workflow": ".github/workflows/frontend.yml",
                "job": "verify",
                "job_name": "Test, type-check, and build",
            },),
            "skipped_on_format_only": True,
        }
        record.update(overrides)
        return record

    def _assert_fell_back_to_the_full_scope(self, result) -> None:
        self.assertTrue(result.undecided)
        self.assertTrue(result.forced)
        self.assertTrue(result.run_code)
        self.assertTrue(result.run_targeted)
        self.assertTrue(result.contracts)
        self.assertTrue(result.backend)
        self.assertTrue(result.frontend)
        self.assertIn("manifest", result.reason)
        self.assertIn("full scope", result.reason)

    def test_a_manifest_with_a_missing_field_does_not_escape(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            broken = self._record()
            del broken["paths"]

            result = self._decide_with_manifest(root, [broken])

            self._assert_fell_back_to_the_full_scope(result)

    def test_a_manifest_with_a_duplicate_surface_does_not_escape(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)

            result = self._decide_with_manifest(
                root, [self._record(), self._record()])

            self._assert_fell_back_to_the_full_scope(result)

    def test_a_manifest_naming_an_absent_job_does_not_escape(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            broken = self._record(consumers=({
                "workflow": ".github/workflows/frontend.yml",
                "job": "absent-job",
                "job_name": "Test, type-check, and build",
            },))

            result = self._decide_with_manifest(root, [broken])

            self._assert_fell_back_to_the_full_scope(result)

    def test_a_manifest_naming_a_different_display_name_does_not_escape(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            broken = self._record(consumers=({
                "workflow": ".github/workflows/frontend.yml",
                "job": "verify",
                "job_name": "Some other name",
            },))

            result = self._decide_with_manifest(root, [broken])

            self._assert_fell_back_to_the_full_scope(result)

    def test_a_manifest_naming_an_absent_workflow_does_not_escape(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            broken = self._record(consumers=({
                "workflow": ".github/workflows/absent.yml",
                "job": "verify",
                "job_name": "Test, type-check, and build",
            },))

            result = self._decide_with_manifest(root, [broken])

            self._assert_fell_back_to_the_full_scope(result)

    def test_a_manifest_with_an_unreadable_workflow_does_not_escape(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            fake_root = Path(tmp) / "checkout"
            workflow = fake_root / ".github" / "workflows" / "frontend.yml"
            workflow.parent.mkdir(parents=True, exist_ok=True)
            workflow.write_text("name: Frontend\non: [unbalanced\n",
                                encoding="utf-8")

            result = self._decide_with_manifest(
                root, [self._record()], repo_root=fake_root)

            self._assert_fell_back_to_the_full_scope(result)

    def test_the_fallback_publishes_the_full_scope(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            broken = self._record()
            del broken["paths"]

            result = self._decide_with_manifest(root, [broken])
            outputs = decision.write_outputs(result)

            self.assertEqual(outputs["run_code"], "true")
            self.assertEqual(outputs["run_targeted"], "true")
            self.assertEqual(outputs["contracts"], "true")
            self.assertEqual(outputs["frontend"], "true")
            self.assertIn("manifest", outputs["reason"])


class FullScopeRequestTests(DecisionFixture):
    """A full scope that was asked for is decided, not undetermined.

    ``undecided`` says that the judgement **failed** and the gate fell back
    to the full scope.  A manual run asked for the full scope on purpose: it
    was decided, and the conclusion must not say "未判定，已回退全量".
    """

    def test_a_manual_run_is_not_undecided(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)

            result = decision.decide(
                {"action": "manual", "reason": "full scope requested"}, root)

            self.assertFalse(result.undecided)
            self.assertIn("full scope requested", result.reason)
            self.assertTrue(result.run_code)
            self.assertTrue(result.forced)

    def test_a_manual_run_publishes_no_undecided_note(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)

            result = decision.decide(
                {"action": "manual", "reason": "full scope requested"}, root)
            conclusion = decision.gate_conclusion(
                result, ".github/workflows/frontend.yml")
            markdown = decision.conclusion_markdown(conclusion)

            self.assertFalse(conclusion["undecided"])
            self.assertEqual(conclusion["undecided_note"], "")
            self.assertNotIn("未判定", markdown)

    def test_a_failed_judgement_is_undecided(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            event = self._event(base, base, base)
            event["pull_request"]["head"]["sha"] = "f" * 40
            event["after"] = "f" * 40

            result = decision.decide(event, root)
            conclusion = decision.gate_conclusion(
                result, ".github/workflows/frontend.yml")

            self.assertTrue(result.undecided)
            self.assertTrue(conclusion["undecided"])
            self.assertIn("未判定，已回退全量", conclusion["undecided_note"])
            self.assertIn("未判定", decision.conclusion_markdown(conclusion))


if __name__ == "__main__":
    unittest.main()
