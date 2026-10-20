"""Guard tests for the wiring between the workflows and the gate decision.

The seven workflow-level gates used to carry their own path table and to ask
a scope script what to run.  They now ask the decision module and read one
boolean per job.  Two mistakes stay silent in CI, and are guarded here:

* a job output id that GitHub's workflow schema does not accept.  A dotted
  id is refused by the schema, and a job that names it never starts.
* a job that reads ``needs.<job>.outputs.<name>`` for a name the providing
  job never declared.  The read then yields an empty string, the comparison
  degenerates, and the gate silently does the wrong thing.

The tests read the workflow files with PyYAML, the same way the manifest
validates its consumers.
"""
from __future__ import annotations

from pathlib import Path
import re
import unittest

import yaml

import lingdoc_gate_decision as decision
import lingdoc_gate_surfaces as manifest

REPO_ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = REPO_ROOT / ".github" / "workflows"

# The seven workflow-level gates.  Each of them carries a scope job that
# asks the decision module, and the jobs below it read its answer.
GATED_WORKFLOWS = (
    "frontend.yml",
    "go-lint.yml",
    "app.yml",
    "lingdoc-mock.yml",
    "lingdoc-f01-runner.yml",
    "lingdoc-workspace-concurrency.yml",
    "lingdoc-interface-integration.yml",
)

# GitHub's workflow schema spells an output id like this.
OUTPUT_ID = re.compile(r"^[_a-zA-Z][a-zA-Z0-9_-]*$")

# A cross-job output read: ``needs.<job>.outputs.<name>``.
NEEDS_OUTPUT = re.compile(r"needs\.([A-Za-z0-9_-]+)\.outputs\.([A-Za-z0-9_-]+)")

# The one condition that keeps a second term.  The lint matrix must not be
# empty -- an empty matrix is a job error, not a skip -- so the golangci job
# keeps the module list next to the decision.  The gate decision itself is
# still the decision module's boolean.
MATRIX_GUARDS = {
    ("go-lint.yml", "golangci"): "needs.changes.outputs.modules != '[]'",
}


def load(name: str) -> dict:
    document = yaml.safe_load((WORKFLOWS / name).read_text(encoding="utf-8"))
    return document or {}


def jobs_of(name: str) -> dict:
    return load(name).get("jobs") or {}


def consumers_of(workflow: str):
    """The jobs of one workflow that the manifest registers as consumers."""
    path = f".github/workflows/{workflow}"
    found = []
    for surface in manifest.load_surfaces():
        for consumer in surface.consumers:
            if consumer.workflow == path:
                found.append(consumer)
    return found


class OutputNameTests(unittest.TestCase):
    """Every declared output name is an id GitHub accepts."""

    def test_every_declared_output_name_is_a_valid_output_id(self):
        for path in sorted(WORKFLOWS.glob("*.yml")):
            for job_key, job in jobs_of(path.name).items():
                for output in (job.get("outputs") or {}):
                    with self.subTest(workflow=path.name, job=job_key,
                                      output=output):
                        self.assertRegex(output, OUTPUT_ID)


class OutputsWhitelistTests(unittest.TestCase):
    """Every cross-job output read is declared by the job that provides it."""

    def test_every_cross_job_output_read_is_declared(self):
        for path in sorted(WORKFLOWS.glob("*.yml")):
            jobs = jobs_of(path.name)
            for job_key, job in jobs.items():
                condition = str(job.get("if") or "")
                for provider, output in NEEDS_OUTPUT.findall(condition):
                    with self.subTest(workflow=path.name, job=job_key,
                                      provider=provider, output=output):
                        self.assertIn(provider, jobs,
                                      f"{path.name}: {job_key} reads a job that "
                                      f"does not exist")
                        declared = jobs[provider].get("outputs") or {}
                        self.assertIn(
                            output, declared,
                            f"{path.name}: {job_key} reads "
                            f"needs.{provider}.outputs.{output}, which {provider} "
                            f"never declares; the read yields an empty string and "
                            f"the condition degenerates silently")


class DecisionKeyTests(unittest.TestCase):
    """The decision module publishes one flat key per consuming job."""

    def test_every_consuming_job_has_a_key_the_schema_accepts(self):
        for surface in manifest.load_surfaces():
            for consumer in surface.consumers:
                key = decision.output_key(consumer.workflow, consumer.job)
                with self.subTest(surface=surface.name, job=consumer.job):
                    self.assertRegex(key, decision.OUTPUT_ID)
                    self.assertNotIn(".", key)

    def test_a_job_that_watches_several_surfaces_still_has_one_key(self):
        owners: dict[str, str] = {}
        for surface in manifest.load_surfaces():
            for consumer in surface.consumers:
                key = decision.output_key(consumer.workflow, consumer.job)
                name = f"{consumer.workflow}#{consumer.job}"
                previous = owners.setdefault(key, name)
                self.assertEqual(previous, name,
                                 f"{name} and {previous} share the key {key}")

    def test_the_key_of_a_job_does_not_depend_on_the_surface(self):
        # The pull request gate job watches five surfaces.  All five have to
        # name it the same way, or the published key would depend on the order
        # of the manifest.
        keys = {decision.output_key(consumer.workflow, consumer.job)
                for surface in manifest.load_surfaces()
                for consumer in surface.consumers
                if consumer.workflow.endswith("lingdoc-review.yml")}
        self.assertEqual(len(keys), 1)


class WorkflowWiringTests(unittest.TestCase):
    """The seven gates read the decision module; they do not decide."""

    def test_no_workflow_carries_a_path_table_any_more(self):
        for path in sorted(WORKFLOWS.glob("*.yml")):
            with self.subTest(workflow=path.name):
                self.assertNotIn("LINGDOC_SCOPE_PATHS",
                                 path.read_text(encoding="utf-8"))

    def test_every_gated_workflow_asks_the_decision_module(self):
        for name in GATED_WORKFLOWS:
            text = (WORKFLOWS / name).read_text(encoding="utf-8")
            with self.subTest(workflow=name):
                self.assertIn("scripts/ci/lingdoc_gate_decision.py", text)
                self.assertNotIn("lingdoc_change_scope.py", text)

    def test_the_scope_job_declares_exactly_the_keys_of_its_own_jobs(self):
        for name in GATED_WORKFLOWS:
            declared = (jobs_of(name).get("scope") or {}).get("outputs") or {}
            expected = {consumer.job: decision.output_key(consumer.workflow,
                                                          consumer.job)
                        for consumer in consumers_of(name)}
            with self.subTest(workflow=name):
                self.assertEqual(set(declared), set(expected))
                for job, key in expected.items():
                    self.assertEqual(declared[job],
                                     f"${{{{ steps.scope.outputs.{key} }}}}")

    def test_every_gate_condition_reads_one_decision_boolean(self):
        for name in GATED_WORKFLOWS:
            for consumer in consumers_of(name):
                condition = str(jobs_of(name)[consumer.job].get("if") or "")
                expected = f"needs.scope.outputs.{consumer.job} == 'true'"
                guard = MATRIX_GUARDS.get((name, consumer.job))
                with self.subTest(workflow=name, job=consumer.job):
                    self.assertNotIn("format_only", condition)
                    self.assertEqual(condition.count("&&"),
                                     1 if guard else 0)
                    if guard:
                        self.assertTrue(condition.endswith(guard))
                        self.assertTrue(condition.startswith(expected))
                    else:
                        self.assertEqual(condition, expected)


if __name__ == "__main__":
    unittest.main()
