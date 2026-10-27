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

import lingdoc_gate_attest_witness as witness
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
                self.assertNotRegex(text, r"lingdoc_\w*_scope\.py")

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


class GateStepWiringTests(unittest.TestCase):
    """The steps of the pull request gate read one decision boolean each.

    The pull request gate is a *step-level* gate: its job always runs and its
    steps are skipped one by one.  These three mistakes stay silent in CI --
    a step that decides the formatting-only shortcut a second time, a step
    that keeps running when the shortcut says otherwise, and a step whose
    condition combines two booleans -- so they are guarded here.
    """

    REVIEW = "lingdoc-review.yml"

    def _steps(self, name: str) -> list[dict]:
        steps: list[dict] = []
        for job in jobs_of(name).values():
            steps.extend(job.get("steps") or [])
        return steps

    def test_the_lint_job_keeps_running(self):
        # Lint is the judge of whether an update is well formatted, so it
        # must not be skipped by the rule it judges.
        condition = str(jobs_of("go-lint.yml")["changes"].get("if", ""))
        self.assertNotIn("format_only", condition)
        surface = next(surface for surface in manifest.load_surfaces()
                       if surface.name == "go-lint")
        self.assertFalse(surface.skipped_on_format_only,
                         "the lint surface must not be skipped on a formatting-only "
                         "update")

    def test_the_contract_validation_steps_keep_running(self):
        contract_steps = [step for step in self._steps(self.REVIEW)
                          if "contracts" in str(step.get("if", ""))
                          and "provider_tests" not in str(step.get("if", ""))]
        self.assertTrue(contract_steps, "the contract validation steps were not found")
        for step in contract_steps:
            self.assertNotIn("format_only", str(step.get("if", "")))

    # The condition every heavy step of the gate stands behind: the scope
    # boolean first, then the witness.  A reuse means the earlier green run
    # already produced this evidence, and the heavy work is not paid again.
    NO_REUSE = "&& steps.attest.outputs.reuse != 'true'"

    def test_the_provider_and_frontend_steps_are_gated(self):
        gated = [step for step in self._steps(self.REVIEW)
                 if "run_providers" in str(step.get("if", ""))
                 or ("run_frontend" in str(step.get("if", ""))
                     and "contracts" not in str(step.get("if", "")))]
        self.assertTrue(gated, "the provider and frontend steps were not found")
        for step in gated:
            condition = str(step.get("if", ""))
            self.assertNotIn("format_only", condition)
            self.assertRegex(condition,
                             r"steps\.scope\.outputs\.run_(providers|frontend) == 'true'")
            self.assertTrue(condition.endswith(self.NO_REUSE),
                            f"{condition!r}: every heavy step also stands "
                            "behind the witness")

    def _step_by_id(self, name: str, step_id: str) -> dict:
        for job in jobs_of(name).values():
            for step in job.get("steps") or []:
                if step.get("id") == step_id:
                    return step
        self.fail(f"{name}: no step has the id {step_id!r}")

    def test_the_heavy_steps_stand_behind_the_scope_and_the_witness(self):
        expected = {
            "contracts": ("steps.scope.outputs.contracts == 'true' "
                          "&& steps.attest.outputs.reuse != 'true'"),
            "providers": ("steps.scope.outputs.run_providers == 'true' "
                          "&& steps.attest.outputs.reuse != 'true'"),
            "frontend": ("steps.scope.outputs.run_frontend == 'true' "
                         "&& steps.attest.outputs.reuse != 'true'"),
        }
        for step_id, condition in expected.items():
            with self.subTest(step=step_id):
                self.assertEqual(
                    str(self._step_by_id(self.REVIEW, step_id).get("if", "")),
                    condition)


class AttestWiringTests(unittest.TestCase):
    """The ready_for_review path asks the witness before the heavy steps.

    The attest step sits between the scope step and the heavy steps.  The
    scope step must run unconditionally -- its anchor is the current
    observation the witness reads -- and the publish and artifact steps must
    run on every path: a reuse is recorded, never assumed.  The check name
    the main ruleset demands does not move.
    """

    REVIEW = "lingdoc-review.yml"

    def _steps(self) -> list[dict]:
        steps: list[dict] = []
        for job in jobs_of(self.REVIEW).values():
            steps.extend(job.get("steps") or [])
        return steps

    def _step_ids(self) -> dict[str, int]:
        return {step.get("id"): index
                for index, step in enumerate(self._steps())
                if step.get("id")}

    def test_the_attest_step_sits_between_the_scope_and_the_heavy_steps(self):
        ids = self._step_ids()
        for step_id in ("scope", "attest", "contracts", "providers",
                        "frontend"):
            self.assertIn(step_id, ids, f"the step {step_id} is missing")
        self.assertLess(ids["scope"], ids["attest"],
                        "the witness reads the scope step's anchor; it must "
                        "run after the scope step")
        self.assertLess(ids["attest"], ids["contracts"],
                        "the witness decides before the heavy steps start")
        self.assertLess(ids["attest"], ids["providers"])
        self.assertLess(ids["attest"], ids["frontend"])

    def test_the_attest_step_asks_the_witness_module(self):
        step = next(step for step in self._steps()
                    if step.get("id") == "attest")
        self.assertIn("scripts/ci/lingdoc_gate_attest_witness.py",
                      str(step.get("run", "")))
        # The witness falls back on its own, but a crash of the step itself
        # must also degrade to a full rerun, not to a red gate.
        self.assertTrue(step.get("continue-on-error"),
                        "a crashed witness must not fail the gate")

    def test_the_scope_step_runs_on_every_path(self):
        scope = next(step for step in self._steps()
                     if step.get("id") == "scope")
        self.assertEqual(scope.get("if", ""), "",
                         "the scope step writes the anchor the witness "
                         "reads; it must never be skipped")

    def test_the_publish_and_artifact_steps_are_never_skipped_by_the_witness(
            self):
        publish = next(step for step in self._steps()
                       if "Publish check boundary" in str(step.get("name", "")))
        upload = next(step for step in self._steps()
                      if str(step.get("uses", "")).startswith(
                          "actions/upload-artifact"))
        for step in (publish, upload):
            self.assertEqual(step.get("if"), "always()")
            self.assertNotIn("attest", str(step.get("if", "")),
                             "a reuse is recorded in the results, never "
                             "assumed by skipping the reporting")

    def test_the_publish_step_records_the_attest_outcome(self):
        """A broken attest step degrades to a full rerun, but the degradation
        must not vanish into the step log: the publish step (which runs on
        every path) records the attest step's outcome in the result artifact
        and in the summary, so a permanently damaged witness leaves a trace
        in the conclusion the gate publishes (#627)."""
        publish = next(step for step in self._steps()
                       if "Publish check boundary" in str(step.get("name", "")))
        # The step reads the outcome through its env block, the way it reads
        # every other step's outcome.
        self.assertIn("steps.attest.outcome", str(publish.get("env", "")),
                      "the publish step must read the attest step's "
                      "outcome, not only its reuse output")
        run = str(publish.get("run", ""))
        self.assertIn("attest_outcome", run,
                      "the outcome must be published as a result field")
        self.assertGreaterEqual(run.count("attest_outcome"), 2,
                                "the outcome must reach the summary as well, "
                                "not only the result artifact")

    def test_the_check_name_the_ruleset_demands_is_unchanged(self):
        for job in jobs_of(self.REVIEW).values():
            self.assertEqual(job.get("name"), "LingDoc PR gate")

    def test_the_witness_may_read_other_runs_artifacts(self):
        permissions = load(self.REVIEW).get("permissions") or {}
        self.assertEqual(permissions.get("actions"), "read",
                         "reading another run's artifact needs actions: read")
        self.assertEqual(permissions.get("contents"), "read")

    def test_the_artifact_name_still_carries_the_checks_prefix(self):
        """The witness finds the candidate's artifact by the prefix the
        upload step spells; the two ends of that contract agree."""
        upload = next(step for step in self._steps()
                      if str(step.get("uses", "")).startswith(
                          "actions/upload-artifact"))
        name = str((upload.get("with") or {}).get("name", ""))
        self.assertTrue(name.startswith(witness.CHECKS_ARTIFACT_PREFIX),
                        f"{name!r} does not start with "
                        f"{witness.CHECKS_ARTIFACT_PREFIX!r}")


if __name__ == "__main__":
    unittest.main()
