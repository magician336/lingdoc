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

import contextlib
import io
import json
import os
from pathlib import Path
import re
import tempfile
import textwrap
import unittest
from unittest import mock

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
    steps are skipped one by one.  Mistakes that stay silent in CI are guarded
    here: a step that decides the formatting-only shortcut a second time, a
    step whose condition combines two booleans -- and, since #723, the return
    of the deleted frontend rerun in any form, be it a dead ``run_frontend``
    read or the npm commands themselves.
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

    def test_the_provider_matrix_job_is_gated(self):
        """The provider verification is a matrix job gated by one boolean.

        The decision module folded the formatting-only shortcut into
        ``run_providers``, so the job reads that boolean and nothing else.
        The second term only keeps the matrix from being empty (the go-lint
        precedent): an empty matrix is a job error, not a skip -- and the
        decision module publishes an empty shard list exactly when the
        provider step does not run (#724).
        """
        providers = jobs_of(self.REVIEW).get("providers") or {}
        self.assertTrue(providers, "the provider matrix job was not found")
        condition = str(providers.get("if", ""))
        self.assertNotIn("format_only", condition)
        self.assertEqual(
            condition,
            "needs.prepare.outputs.run_providers == 'true' && "
            "needs.prepare.outputs.provider_shards != '[]'")

    def test_the_gate_no_longer_reruns_the_frontend(self):
        """The frontend verification lives once, in the frontend workflow.

        ADR 0014, section three: the gate's own setup-node step and its
        "Test, type-check and build frontend" step are gone, and with them
        the ``run_frontend`` read.  A leftover condition on ``run_frontend``
        would read an output the workflow never consumes, and a leftover npm
        step would be the second copy of the three commands this ticket
        exists to delete.
        """
        text = (WORKFLOWS / self.REVIEW).read_text(encoding="utf-8")
        self.assertNotIn(
            "run_frontend", text,
            "the gate still reads run_frontend; the frontend rerun was to be "
            "deleted, not kept on a dead output")
        self.assertNotIn("setup-node", text,
                         "the gate still sets up Node; it runs no JavaScript")
        self.assertNotIn("npm", text,
                         "the gate still runs an npm command; the three "
                         "frontend commands live in frontend.yml alone")

    def test_the_gate_publishes_the_delegated_frontend_verdict(self):
        """The summary's frontend row reads the decision, not a dead outcome.

        The five-key table keeps its frontend field; its value is now the
        decision's delegation statement, so the step summary can say who
        verifies the frontend instead of reporting the outcome of a step
        that no longer exists.
        """
        text = (WORKFLOWS / self.REVIEW).read_text(encoding="utf-8")
        self.assertIn("steps.scope.outputs.frontend", text)
        self.assertNotIn("steps.frontend.outcome", text)
        # The wording keeps delegation and skip apart (ADR 0014): a
        # delegated check has real evidence on the named job; a skipped
        # check is NOT RUN.
        self.assertIn("Skipped means NOT RUN", text)
        self.assertIn("Delegated", text)


class ProviderMatrixWiringTests(unittest.TestCase):
    """The provider shards are a matrix fed by the decision module (#724).

    ADR 0014, section one: the shard list is data the decision module
    publishes; the workflow only declares that it consumes it.  A cross-job
    reference to an undeclared output reads an empty string and degenerates
    silently, so the prepare job must declare exactly what the shard jobs
    and the gate job read -- the allow-list rule the workflow-level gates
    already live under.

    The gate's aggregation logic is the python heredoc inside its publish
    step.  The tests below do not trust its wording: they execute the real
    heredoc against a fake runner and pin what it does -- a red shard turns
    the gate red **and names the shard**, a green matrix publishes the
    boundary, a skipped matrix stays green without shard evidence.
    """

    REVIEW = "lingdoc-review.yml"

    SHARDS = [
        {"name": "lingdoc-workspace",
         "packages": ["./internal/lingdoc/workspace/..."]},
        {"name": "provider-rest", "packages": ["./internal/evidence/..."]},
    ]

    # -- reading the workflow ------------------------------------------------
    def _gate(self) -> dict:
        return jobs_of(self.REVIEW)["gate"]

    def _providers(self) -> dict:
        return jobs_of(self.REVIEW)["providers"]

    def _publish_step(self) -> dict:
        for step in self._gate().get("steps") or []:
            if "python - <<'PY'" in str(step.get("run") or ""):
                return step
        self.fail("the gate publish step carries no python heredoc")

    def _heredoc(self, run: str) -> str:
        match = re.search(r"python - <<'PY'\n(.*?)\nPY\n", run, re.DOTALL)
        self.assertIsNotNone(match, "no closable python heredoc in the step")
        return textwrap.dedent(match.group(1))

    # -- executing a step's python against a fake runner ---------------------
    def _run_publish(self, env: dict, markers: list[str],
                     shards: list[dict] | None = None):
        """Run the publish step's heredoc in a fake runner workspace.

        Returns ``(exit, summary, stderr, folder)``: the SystemExit the
        heredoc raised (``None`` when it finished), the step summary it
        wrote, its stderr, and the artifact folder.
        """
        source = self._heredoc(str(self._publish_step().get("run")))
        tmp = Path(tempfile.mkdtemp())
        folder = tmp / "lingdoc-checks"
        folder.mkdir(parents=True)
        scope = {"coverage": [], "uncovered": False,
                 "provider_shards": (self.SHARDS if shards is None else shards)}
        (folder / "scope.json").write_text(json.dumps(scope), encoding="utf-8")
        for name in markers:
            (folder / f"provider-shard-{name}.passed").touch()
        environment = {
            "RUNNER_TEMP": str(tmp),
            "GITHUB_STEP_SUMMARY": str(tmp / "summary.md"),
            "GITHUB_SHA": "a" * 40,
            "PREPARE_RESULT": "success",
            "CISCRIPTS": "success",
            "SCOPE": "success",
            "CONTRACTS": "success",
            "PROVIDERS": "success",
            "FRONTEND": "delegated to frontend.yml#verify "
                        "(Test, type-check, and build)",
            "SURFACE": "internal/lingdoc/workspace",
            "REASON": "contribution surface changed",
            "BASE": "b" * 40,
        }
        environment.update(env)
        stderr = io.StringIO()
        raised = None
        with mock.patch.dict(os.environ, environment), \
                contextlib.redirect_stderr(stderr):
            try:
                exec(compile(source, "publish-step", "exec"), {})
            except SystemExit as exit:
                raised = exit
        summary_path = Path(environment["GITHUB_STEP_SUMMARY"])
        summary = summary_path.read_text(encoding="utf-8") \
            if summary_path.exists() else ""
        return raised, summary, stderr.getvalue(), folder

    # -- the wiring ----------------------------------------------------------
    def test_the_provider_matrix_consumes_the_decision_output(self):
        providers = self._providers()
        strategy = providers.get("strategy") or {}
        matrix = strategy.get("matrix") or {}
        self.assertEqual(
            matrix.get("shard"),
            "${{ fromJSON(needs.prepare.outputs.provider_shards) }}",
            "the matrix must be generated from the decision module's shard "
            "list, not from a list written down in the workflow")
        self.assertFalse(strategy.get("fail-fast", True),
                         "a failed shard must not cancel the other shards: "
                         "every shard is an independent verification of "
                         "this head")
        self.assertEqual(str(providers.get("needs") or ""), "prepare")

    def test_the_prepare_job_declares_the_outputs_the_shards_read(self):
        declared = (jobs_of(self.REVIEW).get("prepare") or {}).get("outputs") or {}
        self.assertEqual(declared.get("provider_shards"),
                         "${{ steps.scope.outputs.provider_shards }}")
        self.assertEqual(declared.get("run_providers"),
                         "${{ steps.scope.outputs.run_providers }}")

    def test_every_shard_keeps_race_and_fresh_counts(self):
        """Red line two (ADR 0014): sharding must not dilute the command."""
        body = yaml.safe_dump(self._providers())
        self.assertIn("go test -race -count=1", body,
                      "a shard runs the same command as the old step: race "
                      "detection stays on and cached results stay out")
        # The not-empty assertion per shard: a shard whose suite passes zero
        # tests must fail, with the shard name in the failure.
        source = self._heredoc(next(
            str(step.get("run") or "")
            for step in self._providers().get("steps") or []
            if "python - <<'PY'" in str(step.get("run") or "")))
        self.assertIn("no tests passed", source)
        self.assertIn("SHARD_NAME", source)

    def test_a_shard_with_no_passing_test_fails_and_names_the_shard(self):
        source = self._heredoc(next(
            str(step.get("run") or "")
            for step in self._providers().get("steps") or []
            if "python - <<'PY'" in str(step.get("run") or "")))
        tmp = Path(tempfile.mkdtemp())
        log = tmp / "lingdoc-checks" / "go-test-lingdoc-workspace.jsonl"
        log.parent.mkdir(parents=True)
        event = json.dumps({"Action": "fail", "Test": "TestBroken",
                            "Package": "internal/lingdoc/workspace"})
        log.write_text("\n".join([event, event]) + "\n", encoding="utf-8")
        with mock.patch.dict(os.environ, {"RUNNER_TEMP": str(tmp),
                                          "SHARD_NAME": "lingdoc-workspace"}):
            with self.assertRaises(SystemExit) as caught:
                exec(compile(source, "shard-step", "exec"), {})
        self.assertIn("lingdoc-workspace", str(caught.exception))

    def test_a_shard_with_passing_tests_publishes_the_count(self):
        source = self._heredoc(next(
            str(step.get("run") or "")
            for step in self._providers().get("steps") or []
            if "python - <<'PY'" in str(step.get("run") or "")))
        tmp = Path(tempfile.mkdtemp())
        log = tmp / "lingdoc-checks" / "go-test-provider-rest.jsonl"
        log.parent.mkdir(parents=True)
        events = [json.dumps({"Action": "pass", "Test": f"Test{i}",
                              "Package": "internal/evidence"})
                  for i in range(3)]
        log.write_text("\n".join(events) + "\n", encoding="utf-8")
        with mock.patch.dict(os.environ, {"RUNNER_TEMP": str(tmp),
                                          "SHARD_NAME": "provider-rest"}):
            exec(compile(source, "shard-step", "exec"), {})

    def test_the_workflow_carries_no_package_names(self):
        """Red line four (ADR 0014): the workflow is a dumb executor."""
        text = (WORKFLOWS / self.REVIEW).read_text(encoding="utf-8")
        for root in manifest.PROVIDER_ROOTS:
            self.assertNotIn(root, text)
        self.assertNotRegex(
            text, r"internal/|cmd/",
            "the workflow learned a path; the shard list comes from the "
            "decision module alone")

    def test_the_matrix_job_name_matches_the_coverage_attribution(self):
        template = str(self._providers().get("name") or "")
        sample = "lingdoc-workspace"
        expanded = template.replace("${{ matrix.shard.name }}", sample)
        self.assertEqual(expanded,
                         decision.SHARD_JOB_NAME.format(name=sample),
                         "the shard check a reader sees on the pull request "
                         "must be the name the coverage table records")

    def test_the_gate_job_aggregates_the_shards(self):
        gate = self._gate()
        self.assertEqual(gate.get("name"), "LingDoc PR gate",
                         "the required check keeps its stable name")
        self.assertEqual(list(gate.get("needs") or []),
                         ["prepare", "providers"])
        self.assertEqual(str(gate.get("if") or ""), "always()",
                         "the gate job is always created (#309)")
        body = yaml.safe_dump(gate)
        self.assertIn("needs.providers.result", body,
                      "the gate must aggregate the shard matrix")
        self.assertIn("download-artifact", body,
                      "the gate publishes the scope report and shard logs, "
                      "so it has to fetch them from the other jobs")

    # -- the aggregation, executed -------------------------------------------
    def test_a_failed_shard_fails_the_gate_and_names_it(self):
        raised, summary, errors, folder = self._run_publish(
            {"PROVIDERS": "failure"},
            markers=["lingdoc-workspace"])

        self.assertIsNotNone(raised,
                             "a failed shard must fail the required check")
        self.assertNotEqual(raised.code, 0)
        self.assertIn("Provider shard provider-rest", errors,
                      "the gate must name the failed shard")
        result = json.loads((folder / "result.json").read_text(
            encoding="utf-8"))
        self.assertEqual(result["providers"], "failure")
        self.assertIn("boundary", result)

    def test_every_shard_verified_publishes_the_boundary(self):
        raised, summary, errors, folder = self._run_publish(
            {}, markers=["lingdoc-workspace", "provider-rest"])

        self.assertIsNone(raised, errors)
        result = json.loads((folder / "result.json").read_text(
            encoding="utf-8"))
        self.assertEqual(result["providers"], "success")
        self.assertEqual([row["name"] for row in result["provider_shards"]],
                         ["lingdoc-workspace", "provider-rest"])
        self.assertIn("Provider shard lingdoc-workspace", summary)
        self.assertIn("Skipped means NOT RUN", summary)

    def test_a_skipped_matrix_keeps_the_gate_green_without_shard_evidence(self):
        raised, summary, errors, folder = self._run_publish(
            {"PROVIDERS": "skipped"}, markers=[], shards=[])

        self.assertIsNone(raised, errors)
        result = json.loads((folder / "result.json").read_text(
            encoding="utf-8"))
        self.assertEqual(result["providers"], "skipped")
        self.assertEqual(result["provider_shards"], [])
        self.assertIn("Skipped means NOT RUN", summary)

    def test_a_failed_decision_job_fails_the_gate(self):
        raised, summary, errors, folder = self._run_publish(
            {"PREPARE_RESULT": "failure", "SCOPE": "failure",
             "PROVIDERS": "skipped"},
            markers=[], shards=[])

        self.assertIsNotNone(raised,
                             "a red decision job must turn the gate red")
        self.assertNotEqual(raised.code, 0)
        self.assertIn("decision step", errors)


if __name__ == "__main__":
    unittest.main()
