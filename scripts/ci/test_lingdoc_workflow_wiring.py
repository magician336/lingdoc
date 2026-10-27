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
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Sequence
from urllib.parse import parse_qsl, urlparse
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


def gate_publish_heredoc() -> str:
    """The python source of the gate's publish step, extracted verbatim.

    The aggregation logic is the python heredoc inside the publish step; the
    tests execute the real source against a fake runner instead of trusting
    its wording (#724, #734).
    """
    for step in (jobs_of("lingdoc-review.yml").get("gate") or {}).get(
            "steps") or []:
        match = re.search(r"python - <<'PY'\n(.*?)\nPY\n",
                          str(step.get("run") or ""), re.DOTALL)
        if match:
            return textwrap.dedent(match.group(1))
    raise AssertionError("the gate publish step carries no python heredoc")


def run_gate_publish(env: dict, *, markers: Sequence[str] = (),
                     shards: list[dict] | None = None,
                     api: "CheckRunsStub | None" = None):
    """Run the publish step's heredoc in a fake runner workspace.

    Returns ``(exit, summary, stderr, folder)``: the SystemExit the heredoc
    raised (``None`` when it finished), the step summary it wrote, its
    stderr, and the artifact folder.  ``api`` points ``GITHUB_API_URL`` at a
    local check-runs stub (#734); without it the URL names a closed port, so
    an accidental query fails fast instead of reaching the real API.

    Every ``GITHUB_*`` variable the heredoc reads is set here: the CI step
    running this suite sits inside a real event, and an inherited variable
    must never speak for the fixture (#720's lesson, kept for #734).
    """
    source = gate_publish_heredoc()
    tmp = Path(tempfile.mkdtemp())
    folder = tmp / "lingdoc-checks"
    folder.mkdir(parents=True)
    scope = {"coverage": [], "uncovered": False,
             "provider_shards": (SHARD_FIXTURE if shards is None else shards)}
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
        # The delegation verification (#734) reads these; a fixture without
        # a delegation claim must cost zero queries, and one with a claim
        # must never reach the real API.
        "FRONTEND_DELEGATE": "",
        "HEAD_SHA": "a" * 40,
        "GITHUB_REPOSITORY": "owl/example",
        "GITHUB_API_URL": "http://127.0.0.1:1",
        "SURFACE": "internal/lingdoc/workspace",
        "REASON": "contribution surface changed",
        "BASE": "b" * 40,
    }
    if api is not None:
        environment["GITHUB_API_URL"] = api.url
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


DELEGATED_JOB = "Test, type-check, and build"

# The shard matrix a fixture scope report carries (#724's aggregation tests).
SHARD_FIXTURE = [
    {"name": "lingdoc-workspace",
     "packages": ["./internal/lingdoc/workspace/..."]},
    {"name": "provider-rest", "packages": ["./internal/evidence/..."]},
]


def check_run(*, name: str = DELEGATED_JOB, status: str = "completed",
              conclusion: str | None = "success", run_id: int = 1) -> dict:
    """One check-run record shaped like the API returns it."""
    return {"id": run_id, "name": name, "status": status,
            "conclusion": conclusion, "head_sha": "a" * 40}


def payload(*runs: dict) -> dict:
    """The check-runs response body for one page."""
    return {"total_count": len(runs), "check_runs": list(runs)}


def paged_behavior(pages: dict[int, tuple[dict, ...]]):
    """Answers ``page`` from a table; a page the table omits is empty."""
    def behavior(_path: str, query: dict) -> tuple[int, dict]:
        return 200, payload(*pages.get(int(query.get("page", "1")), ()))
    return behavior


def sequence_behavior(responses: list[tuple[int, dict]]):
    """Consumes ``(status, payload)`` pairs in order; the last repeats."""
    lock = threading.Lock()
    state = {"next": 0}

    def behavior(_path: str, _query: dict) -> tuple[int, dict]:
        with lock:
            index = state["next"]
            state["next"] = index + 1
        return responses[min(index, len(responses) - 1)]
    return behavior


class CheckRunsStub:
    """A local stand-in for the GitHub check-runs API (#734).

    The aggregation talks to ``{GITHUB_API_URL}/repos/{repo}/commits/{sha}/
    check-runs`` through urllib; the stub answers that route with the
    behaviour a test hands it and records every request, so a test can pin
    the URL construction, the pagination and the polling -- all on
    127.0.0.1, no network.
    """

    def __init__(self, behavior):
        self.requests: list[tuple[str, dict]] = []
        stub = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                parsed = urlparse(self.path)
                query = dict(parse_qsl(parsed.query))
                stub.requests.append((parsed.path, query))
                status, body = stub._behavior(parsed.path, query)
                content = json.dumps(body).encode("utf-8")
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(content)))
                self.end_headers()
                self.wfile.write(content)

            def log_message(self, *_args):
                pass

        self._behavior = behavior
        self._server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self._thread = threading.Thread(target=self._server.serve_forever,
                                        daemon=True)
        self._thread.start()

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self._server.server_port}"

    def close(self) -> None:
        self._server.shutdown()
        self._server.server_close()
        self._thread.join(timeout=5)

    def __enter__(self) -> "CheckRunsStub":
        return self

    def __exit__(self, *_exc) -> None:
        self.close()


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

    SHARDS = SHARD_FIXTURE

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
        return run_gate_publish(env, markers=markers, shards=shards)

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


class DelegationVerificationTests(unittest.TestCase):
    """The gate verifies the delegated job's real result on this head (#734).

    ADR 0014, section three's failure clause: a head where the frontend
    workflow did not really run -- trigger, queue and failure all count as
    not run -- must be red.  Since #723 the published frontend verdict is a
    delegation statement, so the aggregation now asks the check runs of the
    pull request's head SHA whether the delegated job really finished, and
    exactly one conclusion keeps the delegation green: ``success``.  A
    failed or absent delegated job, a window that closes on a pending run,
    and a failed query all turn the gate red **and name the job and the
    state that caused it**; the states are executed against a local
    check-runs stub, the same way the shard aggregation executes the real
    heredoc (#724).
    """

    REVIEW = "lingdoc-review.yml"
    JOB = DELEGATED_JOB

    def _gate(self) -> dict:
        return jobs_of(self.REVIEW)["gate"]

    def _run_gate(self, env: dict, stub: CheckRunsStub | None = None,
                  shards: list[dict] | None = None):
        return run_gate_publish(env, shards=shards, api=stub)

    # -- the five states, executed against the stub ---------------------------
    def test_a_successful_delegated_job_keeps_the_gate_green(self):
        """The one green state: the delegated job really succeeded here."""
        with CheckRunsStub(paged_behavior({1: (check_run(),)})) as stub:
            raised, summary, errors, folder = self._run_gate(
                {"FRONTEND_DELEGATE": self.JOB}, stub=stub)

        self.assertIsNone(raised, errors)
        # The query is addressed to the pull request's head SHA, with the
        # page size the lookup paginates by.
        self.assertEqual(stub.requests, [
            (f"/repos/owl/example/commits/{'a' * 40}/check-runs",
             {"per_page": "100", "page": "1"}),
        ])
        result = json.loads((folder / "result.json").read_text(
            encoding="utf-8"))
        self.assertEqual(result["frontend_delegation"],
                         {"delegated_job": self.JOB,
                          "outcome": "verified",
                          "detail": "conclusion=success"})
        self.assertIn("frontend delegation", summary)
        self.assertIn(self.JOB, summary)

    def test_a_failed_delegated_job_fails_the_gate_and_names_it(self):
        """A red delegated job is a red gate, naming job and conclusion."""
        runs = (check_run(conclusion="cancelled", run_id=1),
                check_run(conclusion="failure", run_id=2))
        with CheckRunsStub(paged_behavior({1: runs})) as stub:
            raised, _summary, errors, folder = self._run_gate(
                {"FRONTEND_DELEGATE": self.JOB}, stub=stub)

        self.assertIsNotNone(raised,
                             "a failed delegated job must fail the gate")
        self.assertNotEqual(raised.code, 0)
        self.assertIn(self.JOB, errors)
        # The newest completed run speaks: a stale `cancelled` from a run
        # the re-push replaced must not be the reported state.
        self.assertIn("conclusion=failure", errors)
        self.assertNotIn("conclusion=cancelled", errors)
        result = json.loads((folder / "result.json").read_text(
            encoding="utf-8"))
        self.assertEqual(result["frontend_delegation"]["outcome"], "failed")

    def test_an_absent_delegated_job_exhausts_the_window_and_fails(self):
        """No check run on the head: the gate polls, then fails naming it."""
        with CheckRunsStub(paged_behavior({})) as stub:
            raised, _summary, errors, _folder = self._run_gate(
                {"FRONTEND_DELEGATE": self.JOB,
                 "GATE_DELEGATION_POLL_INTERVAL": "0.05",
                 "GATE_DELEGATION_POLL_TIMEOUT": "0.4"}, stub=stub)

        self.assertIsNotNone(raised, "an absent delegated job must fail")
        self.assertNotEqual(raised.code, 0)
        self.assertIn(self.JOB, errors)
        self.assertIn("no check run", errors)
        self.assertIn("polling window exhausted", errors)
        self.assertGreaterEqual(len(stub.requests), 3,
                                "the lookup must poll, not ask once")

    def test_an_in_progress_job_polled_past_the_window_fails(self):
        """Queueing counts as not run once the polling window closes."""
        pending = check_run(status="in_progress", conclusion=None)
        with CheckRunsStub(paged_behavior({1: (pending,)})) as stub:
            raised, _summary, errors, _folder = self._run_gate(
                {"FRONTEND_DELEGATE": self.JOB,
                 "GATE_DELEGATION_POLL_INTERVAL": "0.05",
                 "GATE_DELEGATION_POLL_TIMEOUT": "0.4"}, stub=stub)

        self.assertIsNotNone(raised, "a window that closed on a pending "
                                      "run must fail the gate")
        self.assertNotEqual(raised.code, 0)
        self.assertIn(self.JOB, errors)
        self.assertIn("status=in_progress", errors)
        self.assertIn("polling window exhausted", errors)

    def test_an_in_progress_job_that_finishes_in_time_is_accepted(self):
        """The polling waits for a live run and accepts its success."""
        responses = [(200, payload(check_run(status="in_progress",
                                             conclusion=None))),
                     (200, payload(check_run())),
                     (200, payload(check_run()))]
        with CheckRunsStub(sequence_behavior(responses)) as stub:
            raised, _summary, errors, folder = self._run_gate(
                {"FRONTEND_DELEGATE": self.JOB,
                 "GATE_DELEGATION_POLL_INTERVAL": "0.05"}, stub=stub)

        self.assertIsNone(raised, errors)
        self.assertGreaterEqual(len(stub.requests), 2,
                                "the gate must have polled, not asked once")
        result = json.loads((folder / "result.json").read_text(
            encoding="utf-8"))
        self.assertEqual(result["frontend_delegation"]["outcome"], "verified")

    def test_an_api_failure_fails_closed_and_names_the_reason(self):
        """A failed query is a red gate with the reason; never a silent pass."""
        with CheckRunsStub(
                lambda _path, _query: (500, {"message": "boom"})) as stub:
            raised, _summary, errors, folder = self._run_gate(
                {"FRONTEND_DELEGATE": self.JOB}, stub=stub)

        self.assertIsNotNone(raised, "a query failure must fail closed")
        self.assertNotEqual(raised.code, 0)
        self.assertIn("500", errors)
        self.assertIn("query failed", errors)
        self.assertEqual(len(stub.requests), 1,
                         "a query failure must not be retried into silence")
        result = json.loads((folder / "result.json").read_text(
            encoding="utf-8"))
        self.assertEqual(result["frontend_delegation"]["outcome"],
                         "query-failed")

    # -- attribution precision ------------------------------------------------
    def test_no_delegation_claim_means_zero_queries(self):
        """Only a delegated face is verified; every other face costs nothing.

        A manual dispatch, an unselected frontend and a head that cannot
        start the frontend workflow all publish an empty delegate key; the
        gate must then query nothing at all.
        """
        with CheckRunsStub(paged_behavior({})) as stub:
            raised, _summary, errors, _folder = self._run_gate(
                {"FRONTEND_DELEGATE": "",
                 "FRONTEND": "not run: frontend verification is not "
                             "selected on this head"},
                stub=stub)

        self.assertIsNone(raised, errors)
        self.assertEqual(stub.requests, [])

    # -- the lookup itself ----------------------------------------------------
    def test_the_lookup_pages_through_all_check_runs(self):
        """A delegated job beyond the first page is still found."""
        filler = tuple(check_run(name=f"Other job {index}", run_id=index)
                       for index in range(100))
        with CheckRunsStub(paged_behavior({1: filler, 2: (check_run(),)})) \
                as stub:
            raised, _summary, errors, _folder = self._run_gate(
                {"FRONTEND_DELEGATE": self.JOB}, stub=stub)

        self.assertIsNone(raised, errors)
        self.assertEqual([query.get("page") for _path, query in stub.requests],
                         ["1", "2"])

    def test_a_cancelled_older_run_does_not_fail_a_live_rerun(self):
        """frontend.yml cancels in progress on a re-push.

        The stale `cancelled` check run must read as *pending*, not as a
        failure, while the live run is still going; the live run's own
        conclusion then settles the delegation.  (The gate's publish step
        starts minutes after the event, so the live run's check run already
        exists by then; a state where a stale cancelled run is the only one
        on the head is a delegation that really did not run, and fails.)
        """
        cancelled = check_run(conclusion="cancelled", run_id=1)
        live_pending = check_run(status="in_progress", conclusion=None,
                                 run_id=2)
        responses = [
            (200, payload(cancelled, live_pending)),
            (200, payload(cancelled, live_pending)),
            (200, payload(cancelled, check_run(run_id=2))),
        ]
        with CheckRunsStub(sequence_behavior(responses)) as stub:
            raised, _summary, errors, _folder = self._run_gate(
                {"FRONTEND_DELEGATE": self.JOB,
                 "GATE_DELEGATION_POLL_INTERVAL": "0.05"}, stub=stub)

        self.assertIsNone(raised, errors)
        self.assertGreaterEqual(len(stub.requests), 3)

    # -- the wiring -----------------------------------------------------------
    def test_the_workflow_permissions_stay_minimal(self):
        """The verification reads check runs; that read is the only addition."""
        self.assertEqual(load(self.REVIEW).get("permissions"),
                         {"contents": "read", "checks": "read"})

    def test_the_prepare_job_declares_the_delegate_and_the_gate_reads_it(self):
        """The cross-job reference is an explicitly declared output (ADR 0005)."""
        declared = (jobs_of(self.REVIEW).get("prepare") or {}).get("outputs") \
            or {}
        self.assertEqual(declared.get("frontend_delegate"),
                         "${{ steps.scope.outputs.frontend_delegate }}")
        gate_env = self._gate().get("env") or {}
        self.assertEqual(gate_env.get("FRONTEND_DELEGATE"),
                         "${{ needs.prepare.outputs.frontend_delegate }}")
        # The check runs attach to the pull request's head commit, not to
        # the merge commit GITHUB_SHA names (read off PR #731's check runs).
        self.assertEqual(gate_env.get("HEAD_SHA"),
                         "${{ github.event.pull_request.head.sha }}")

    def test_the_publish_step_authenticates_with_the_workflow_token(self):
        for step in self._gate().get("steps") or []:
            if "python - <<'PY'" in str(step.get("run") or ""):
                self.assertEqual(
                    (step.get("env") or {}).get("GITHUB_TOKEN"),
                    "${{ github.token }}",
                    "the query carries exactly this workflow's permissions")
                return
        self.fail("the gate publish step carries no python heredoc")

    def test_the_polling_knobs_are_env_tunable_with_defaults(self):
        """The window and its interval are env knobs with registered defaults."""
        source = gate_publish_heredoc()
        interval = re.search(r"GATE_DELEGATION_POLL_INTERVAL',\s*'([0-9.]+)'",
                             source)
        window = re.search(r"GATE_DELEGATION_POLL_TIMEOUT',\s*'([0-9.]+)'",
                           source)
        self.assertIsNotNone(interval, "the poll interval must be an env knob")
        self.assertIsNotNone(window, "the poll window must be an env knob")
        self.assertEqual(float(interval.group(1)), 30.0,
                         "the registered default interval is 30 seconds")
        self.assertEqual(float(window.group(1)), 600.0,
                         "the registered default window is 10 minutes, the "
                         "measured frontend job (154s) plus margin")

    def test_the_gate_job_outlives_the_polling_window(self):
        """A job timeout below the window would kill the named reason."""
        self.assertGreaterEqual(
            int(self._gate().get("timeout-minutes") or 0), 15,
            "the 10-minute polling window needs job headroom; a tighter "
            "timeout would kill the publish step and lose the failure")

    def test_the_delegated_check_name_is_unique_across_the_workflows(self):
        """The check-run filter matches the display name; an ambiguous name
        would let the gate read another job's evidence."""
        delegated = {consumer.job_name
                     for surface in manifest.load_surfaces()
                     if surface.role == manifest.ROLE_FRONTEND
                     for consumer in surface.consumers}
        self.assertTrue(delegated)
        published: dict[str, list[str]] = {}
        for path in sorted(WORKFLOWS.glob("*.yml")):
            for job in (jobs_of(path.name)).values():
                name = str(job.get("name") or "unnamed")
                published.setdefault(name, []).append(path.name)
        for name in sorted(delegated):
            with self.subTest(job_name=name):
                self.assertEqual(published.get(name, []), ["frontend.yml"],
                                 f"the delegated check name {name!r} must "
                                 "identify exactly one job in the frontend "
                                 "workflow")


if __name__ == "__main__":
    unittest.main()
