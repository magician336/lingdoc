"""Guard tests for the client module's compile-and-test gate (#605).

Why this file exists
--------------------
``client/`` is a Go module of its own.  The root module's build never saw it
-- ``app.yml`` excludes it on purpose -- and the lint gate only *reads* the
client code.  Before #605 a pull request that touched nothing but ``client/``
could be merged with a client module that did not even compile: no job
compiled it and no job ran its tests.

The fix has two halves that have to agree:

* a **job** that really compiles and really tests the client module, and
* a **gate surface** that registers the client module as a surface whose
  compile-or-test evidence the pull request gate demands.

The second half is the one that used to be missing, and the one that can rot
silently: a surface nobody verifies is reported as "not verified (no gate job
covers this surface)", which is *not* fatal.  These tests therefore pin the
two contrasts that tell the two halves apart:

* a change that only touches ``client/`` must select the client job -- and
  must **not** select the root module's build (the app job watches the root
  module, and the client module is not part of it);
* a change that only touches ``docs/`` must select neither.

The tests read the repository as it is and observe the decision through the
decision module's public interface (``decide`` / ``write_outputs`` /
``publish``), the same seam the judgement tests use.
"""
from __future__ import annotations

import dataclasses
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import yaml

import lingdoc_gate_decision as decision
import lingdoc_gate_surfaces as manifest

CI_DIR = Path(__file__).resolve().parent
REPO_ROOT = CI_DIR.parents[1]
WORKFLOWS = REPO_ROOT / ".github" / "workflows"

# The workflow, the job and the display name the client surface names.
CLIENT_WORKFLOW = ".github/workflows/client.yml"
CLIENT_JOB = "verify"
CLIENT_JOB_NAME = "Build and test the client module"

# The job of the root module's gate.  The client module is not part of it.
APP_WORKFLOW = ".github/workflows/app.yml"
APP_JOB = "verify"


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(
        ["git", *args], cwd=root, stderr=subprocess.DEVNULL).decode().strip()


def write(root: Path, relative: str, content: str) -> None:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def workflow_document(name: str) -> dict:
    return yaml.safe_load((WORKFLOWS / name).read_text(encoding="utf-8")) or {}


def client_surface():
    """The manifest's client surface."""
    surfaces = manifest.load_and_validate()
    return next(surface for surface in surfaces.surfaces
                if surface.name == "client")


class ClientSurfaceRegistrationTests(unittest.TestCase):
    """The manifest registers the client module as a verifying surface."""

    def test_the_client_surface_is_registered_and_verifies(self):
        surface = client_surface()

        self.assertTrue(surface.verifies,
                        "the client surface must state that its job compiles "
                        "and tests it; a surface that only inspects its code "
                        "cannot carry compile or test evidence")
        self.assertEqual(surface.path_kind, manifest.PATH_KIND_GLOB)
        self.assertEqual(surface.role, manifest.ROLE_PATHS)
        self.assertEqual(surface.gate_level, manifest.GATE_LEVEL_REPOSITORY)

    def test_the_client_surface_watches_the_client_module(self):
        surface = client_surface()

        self.assertIn("client/**", surface.paths)
        self.assertIn(CLIENT_WORKFLOW, surface.paths)

    def test_the_client_surface_is_consumed_by_the_client_job(self):
        surface = client_surface()

        self.assertEqual(
            [(consumer.workflow, consumer.job, consumer.job_name)
             for consumer in surface.consumers],
            [(CLIENT_WORKFLOW, CLIENT_JOB, CLIENT_JOB_NAME)])

    def test_the_client_workflow_mirrors_the_surface_paths_on_push(self):
        # The push path list is the static mirror of the manifest: GitHub
        # cannot generate a path filter dynamically.
        surface = client_surface()
        trigger = workflow_document("client.yml").get("on")
        trigger = (trigger or workflow_document("client.yml").get(True) or {})
        paths = (trigger.get("push") or {}).get("paths")

        self.assertEqual(list(paths or []), list(surface.paths))

    def test_the_client_gate_never_filters_pull_request_paths(self):
        # A top-level path filter would stop the workflow from starting, and
        # the pull request page would show no check at all.
        document = workflow_document("client.yml")
        trigger = document.get("on") or document.get(True) or {}
        pull_request = trigger.get("pull_request") or {}

        self.assertIn("pull_request", trigger)
        self.assertNotIn("paths", pull_request)
        self.assertNotIn("paths-ignore", pull_request)


class ClientJobWiringTests(unittest.TestCase):
    """The client job really compiles and really tests the client module."""

    def _steps(self) -> list[dict]:
        job = workflow_document("client.yml")["jobs"][CLIENT_JOB]
        return job.get("steps") or []

    def _run_lines(self) -> list[str]:
        return [str(step.get("run") or "") for step in self._steps()]

    def test_the_job_builds_the_client_module(self):
        lines = self._run_lines()

        self.assertTrue(any("go build" in line for line in lines),
                        "the client gate has to compile the client module")

    def test_the_job_really_runs_the_tests(self):
        lines = self._run_lines()

        self.assertTrue(any("go test" in line for line in lines),
                        "the client gate has to run the client module's tests")

    def test_the_job_runs_in_the_client_module(self):
        job = workflow_document("client.yml")["jobs"][CLIENT_JOB]
        working_directory = (job.get("defaults") or {}).get("run") or {}

        self.assertEqual(working_directory.get("working-directory"), "client")

    def test_the_scope_job_declares_exactly_the_keys_of_its_own_jobs(self):
        document = workflow_document("client.yml")
        declared = (document["jobs"].get("scope") or {}).get("outputs") or {}
        expected = {consumer.job: decision.output_key(consumer.workflow,
                                                      consumer.job)
                    for consumer in client_surface().consumers}

        self.assertEqual(set(declared), set(expected))
        for job, key in expected.items():
            self.assertEqual(declared[job],
                             f"${{{{ steps.scope.outputs.{key} }}}}")

    def test_the_job_condition_reads_one_decision_boolean(self):
        job = workflow_document("client.yml")["jobs"][CLIENT_JOB]
        condition = str(job.get("if") or "")

        self.assertEqual(condition, f"needs.scope.outputs.{CLIENT_JOB} == 'true'")
        self.assertNotIn("format_only", condition)
        self.assertNotIn("&&", condition)


class ClientScopeContrastTests(unittest.TestCase):
    """The two contrasts: a client change runs the client gate, a docs change does not."""

    def _repo(self, root: Path) -> None:
        git(root, "init", "-b", "main")
        git(root, "config", "user.email", "synthetic@example.com")
        git(root, "config", "user.name", "Synthetic CI test")
        write(root, "README.md", "base\n")
        write(root, "client/knowledge.go", "package client\n")
        write(root, "docs/note.md", "base\n")
        git(root, "add", ".")
        git(root, "commit", "-m", "base")

    def _contribution(self, root: Path,
                      changes: dict[str, str]) -> decision.Decision:
        """The decision for a pull request that contributes exactly these files."""
        base = git(root, "rev-parse", "HEAD")
        git(root, "checkout", "-b", "topic")
        for relative, content in changes.items():
            write(root, relative, content)
        git(root, "add", "-A")
        git(root, "commit", "-m", "contribution")
        head = git(root, "rev-parse", "HEAD")
        event = {
            "action": "synchronize",
            "before": base,
            "after": head,
            "pull_request": {"base": {"sha": base}, "head": {"sha": head}},
        }
        return decision.decide(event, root)

    def _job_runs(self, result: decision.Decision, workflow: str,
                  job: str) -> bool:
        for entry in result.jobs:
            if entry.workflow == workflow and entry.job == job:
                return entry.run
        raise AssertionError(f"no job {job!r} in {workflow!r}")

    def test_a_client_only_change_selects_the_client_gate(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)

            result = self._contribution(
                root, {"client/knowledge.go":
                       "package client\n\nconst ClientModuleName = \"weknora\"\n"})

            # The client module has to be compiled and tested.
            self.assertTrue(self._job_runs(result, CLIENT_WORKFLOW, CLIENT_JOB))
            # The root module's build is a different surface: the client
            # module is not part of it, so it must stay away.
            self.assertFalse(self._job_runs(result, APP_WORKFLOW, APP_JOB))
            # The change is code, not a reformatting: the shortcut must not
            # hide the client module behind a skipped job.
            self.assertFalse(result.format_only)
            self.assertTrue(result.run_code)
            # And the coverage says the client module has compile or test
            # evidence on this head.
            row = next(row for row in result.coverage if row.surface == "client")
            self.assertEqual(row.status, "verified")
            self.assertIn(CLIENT_JOB_NAME, row.verified_by)
            self.assertFalse(result.uncovered)

    def test_a_documentation_only_change_selects_neither_gate(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)

            result = self._contribution(root, {"docs/note.md": "evidence\n"})

            self.assertFalse(self._job_runs(result, CLIENT_WORKFLOW, CLIENT_JOB))
            self.assertFalse(self._job_runs(result, APP_WORKFLOW, APP_JOB))
            self.assertFalse(result.uncovered)

    def _client_surface_without_a_running_job(self) -> tuple:
        """A client surface whose verifying job never runs on a client change.

        The role decides the job: a provider surface is selected by the
        provider step, which a client change does not select.  The surface
        still *watches* the client module, so the contribution has a surface
        that is verified by nobody.
        """
        patched = []
        for surface in manifest.load_surfaces():
            if surface.name == "client":
                patched.append(dataclasses.replace(
                    surface, role=manifest.ROLE_PROVIDER,
                    path_kind=manifest.PATH_KIND_PREFIX,
                    paths=("client/",)))
            else:
                patched.append(surface)
        return tuple(patched)

    def test_a_client_change_without_a_running_client_job_fails_the_gate(self):
        """The release condition has to fire for the client module too.

        A surface that is watched by a verifying job, but that no verifying
        job runs for, has no compile or test evidence on this head: the gate
        must not give a green conclusion on it (#602).
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._repo(root)
            base = git(root, "rev-parse", "HEAD")
            git(root, "checkout", "-b", "topic")
            write(root, "client/knowledge.go",
                  "package client\n\nconst ClientModuleName = \"weknora\"\n")
            git(root, "add", "-A")
            git(root, "commit", "-m", "contribution")
            head = git(root, "rev-parse", "HEAD")
            event = {
                "action": "synchronize",
                "before": base,
                "after": head,
                "pull_request": {"base": {"sha": base}, "head": {"sha": head}},
            }

            with mock.patch.object(decision, "_manifest_surfaces",
                                   return_value=self._client_surface_without_a_running_job()):
                with self.assertRaises(SystemExit) as caught:
                    decision.publish(event, root, require_coverage=True,
                                     whitespace=False)

            self.assertNotEqual(caught.exception.code, 0)


if __name__ == "__main__":
    unittest.main()
