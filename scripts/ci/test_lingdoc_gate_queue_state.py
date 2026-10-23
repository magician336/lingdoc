"""Guard tests for the queued-or-running judgement (#550).

GitHub shows one word -- ``in progress`` -- for a gate that is running its
steps and for a gate that is still waiting for a runner.  The two need
different reactions, so the judgement has to tell them apart from data.

These tests pin that judgement, and they pin it the way issue #550 states
it: the verdict follows ``started_at``, and a record that does not say is
``missing`` rather than green.

The tests never reach the network.  They feed the judgement the records the
GitHub API publishes and ask it what it concludes.
"""
from __future__ import annotations

import contextlib
import io
import json
import os
from pathlib import Path
import unittest

import yaml

import lingdoc_gate_queue_state as queue_state

CI_DIR = Path(__file__).resolve().parent
REPO_ROOT = CI_DIR.parents[1]
REVIEW_WORKFLOW = REPO_ROOT / ".github" / "workflows" / "lingdoc-review.yml"

GATE_NAME = queue_state.GATE_CHECK_NAME


def gate_check(**overrides):
    """A check-run record of the gate, as the Checks API publishes it."""
    record = {
        "id": 120653396590,
        "name": GATE_NAME,
        "status": "in_progress",
        "conclusion": None,
        "started_at": None,
        "completed_at": None,
    }
    record.update(overrides)
    return record


def gate_job(**overrides):
    """A job record of the gate, as the Actions API publishes it."""
    record = {
        "id": 120653396590,
        "name": GATE_NAME,
        "status": "queued",
        "conclusion": None,
        "started_at": None,
        "completed_at": None,
    }
    record.update(overrides)
    return record


def sibling_check(**overrides):
    """A check-run of another workflow on the same head."""
    record = {
        "id": 120583752050,
        "name": "workspace-concurrency",
        "status": "completed",
        "conclusion": "skipped",
        "started_at": "2026-10-23T18:24:29Z",
        "completed_at": "2026-10-23T18:24:29Z",
    }
    record.update(overrides)
    return record


class QueuedTests(unittest.TestCase):
    """A gate that has not been given a runner yet is queued."""

    def test_a_check_that_has_not_started_is_queued(self):
        """AC-1 row 1: ``started_at`` empty plus ``in_progress`` is queued."""
        result = queue_state.judge(gate_check(), [])
        self.assertEqual(result["verdict"], queue_state.VERDICT_QUEUED)
        self.assertTrue(result["queued"])
        self.assertFalse(result["running"])
        self.assertFalse(result["passed"])

    def test_a_job_that_has_not_started_is_queued(self):
        result = queue_state.judge(
            gate_check(), [gate_job(status="queued", started_at=None)])
        self.assertEqual(result["verdict"], queue_state.VERDICT_QUEUED)

    def test_the_status_word_alone_does_not_decide(self):
        """The verdict rests on ``started_at``, not on the word GitHub shows."""
        for status in ("queued", "in_progress", "waiting", "pending", "requested"):
            with self.subTest(status=status):
                result = queue_state.judge(gate_check(status=status), [])
                self.assertEqual(result["verdict"], queue_state.VERDICT_QUEUED)

    def test_a_gate_whose_siblings_finished_is_queued(self):
        """AC-1 row 3: the aggregate check is the only open check of the head.

        Every other check of the head has finished while the gate is still
        open, so the gate is the one job left waiting for a runner.
        """
        siblings = [sibling_check(), sibling_check(id=2, name="F01 runner regression")]
        result = queue_state.judge(gate_check(), [], siblings)
        self.assertEqual(result["verdict"], queue_state.VERDICT_QUEUED)
        self.assertTrue(result["evidence"]["all_other_checks_completed"])

    def test_the_job_record_wins_over_the_aggregate_check(self):
        """The job record knows about the runner allocation; the check-run
        record is the aggregate.  When they disagree, the job decides."""
        result = queue_state.judge(
            gate_check(started_at="2026-10-23T18:24:21Z"),
            [gate_job(started_at=None)])
        self.assertEqual(result["verdict"], queue_state.VERDICT_QUEUED)


class RunningTests(unittest.TestCase):
    """A gate that has started is running."""

    def test_a_check_that_has_started_is_running(self):
        """AC-1 row 2: ``started_at`` set plus ``in_progress`` is running."""
        result = queue_state.judge(
            gate_check(started_at="2026-10-23T18:24:21Z"), [])
        self.assertEqual(result["verdict"], queue_state.VERDICT_RUNNING)
        self.assertTrue(result["running"])
        self.assertFalse(result["queued"])
        self.assertFalse(result["passed"])

    def test_a_job_that_has_started_is_running(self):
        result = queue_state.judge(
            gate_check(started_at="2026-10-23T18:24:21Z"),
            [gate_job(status="in_progress", started_at="2026-10-23T18:24:21Z")])
        self.assertEqual(result["verdict"], queue_state.VERDICT_RUNNING)

    def test_a_started_gate_is_running_even_when_the_status_word_is_queued(self):
        result = queue_state.judge(
            gate_check(status="queued", started_at="2026-10-23T18:24:21Z"), [])
        self.assertEqual(result["verdict"], queue_state.VERDICT_RUNNING)


class FinishedTests(unittest.TestCase):
    """A gate that has finished is passed or failed, and nothing else."""

    def test_a_successful_gate_has_passed(self):
        """AC-1 row 4."""
        result = queue_state.judge(
            gate_check(status="completed", conclusion="success",
                       started_at="2026-10-23T18:24:21Z",
                       completed_at="2026-10-23T18:26:46Z"),
            [])
        self.assertEqual(result["verdict"], queue_state.VERDICT_PASSED)
        self.assertTrue(result["passed"])

    def test_a_failed_gate_has_failed(self):
        """AC-1 row 5."""
        for conclusion in ("failure", "cancelled", "timed_out", "action_required"):
            with self.subTest(conclusion=conclusion):
                result = queue_state.judge(
                    gate_check(status="completed", conclusion=conclusion,
                               started_at="2026-10-23T18:24:21Z",
                               completed_at="2026-10-23T18:26:46Z"),
                    [])
                self.assertEqual(result["verdict"], queue_state.VERDICT_FAILED)
                self.assertTrue(result["failed"])
                self.assertFalse(result["passed"])

    def test_a_skipped_gate_has_not_passed(self):
        """Skipped means NOT RUN, not passed (#482 / AC-04-05)."""
        result = queue_state.judge(
            gate_check(status="completed", conclusion="skipped"), [])
        self.assertEqual(result["verdict"], queue_state.VERDICT_FAILED)
        self.assertFalse(result["passed"])


class UndecidedTests(unittest.TestCase):
    """Data that does not say must not be read as green."""

    def test_a_head_without_a_gate_check_is_missing(self):
        """AC-1 row 6: no gate check is not a green gate."""
        result = queue_state.judge(None, [])
        self.assertEqual(result["verdict"], queue_state.VERDICT_MISSING)
        self.assertFalse(result["passed"])
        self.assertFalse(result["queued"])
        self.assertFalse(result["running"])

    def test_a_record_without_started_at_is_missing(self):
        """The difference between ``null`` and "not there" is a fact.

        ``started_at: null`` is GitHub saying the job has not started; a
        record without the field is GitHub saying nothing at all.
        """
        record = gate_check()
        del record["started_at"]
        result = queue_state.judge(record, [])
        self.assertEqual(result["verdict"], queue_state.VERDICT_MISSING)
        self.assertFalse(result["queued"])
        self.assertFalse(result["running"])
        self.assertFalse(result["passed"])

    def test_a_finished_gate_without_a_conclusion_is_missing(self):
        record = gate_check(status="completed", conclusion=None,
                            started_at="2026-10-23T18:24:21Z",
                            completed_at="2026-10-23T18:26:46Z")
        result = queue_state.judge(record, [])
        self.assertEqual(result["verdict"], queue_state.VERDICT_MISSING)
        self.assertFalse(result["passed"])

    def test_no_observation_is_ever_green(self):
        """Whatever the records say, a missing verdict is never a pass."""
        for check, jobs in ((None, []),
                            (gate_check(), []),
                            (gate_check(started_at="2026-10-23T18:24:21Z"), []),
                            (gate_check(status="completed", conclusion="success"), [])):
            with self.subTest(check=check, jobs=jobs):
                result = queue_state.judge(check, jobs)
                if result["verdict"] != queue_state.VERDICT_PASSED:
                    self.assertFalse(result["passed"])


class ExitCodeTests(unittest.TestCase):
    """The exit code says how urgent the answer is."""

    def test_a_normal_state_exits_zero(self):
        for verdict in (queue_state.VERDICT_QUEUED, queue_state.VERDICT_RUNNING,
                        queue_state.VERDICT_PASSED):
            with self.subTest(verdict=verdict):
                self.assertEqual(queue_state.EXIT_CODES[verdict], 0)

    def test_a_failed_gate_exits_non_zero(self):
        self.assertNotEqual(
            queue_state.EXIT_CODES[queue_state.VERDICT_FAILED], 0)

    def test_an_undetermined_gate_exits_non_zero(self):
        self.assertNotEqual(
            queue_state.EXIT_CODES[queue_state.VERDICT_MISSING], 0)

    def test_failure_and_missing_are_different_codes(self):
        """A caller can tell "the gate said no" from "the gate could not be
        asked"."""
        self.assertNotEqual(
            queue_state.EXIT_CODES[queue_state.VERDICT_FAILED],
            queue_state.EXIT_CODES[queue_state.VERDICT_MISSING])


class StubApi:
    """A GitHub API that answers from a fixture instead of the network."""

    def __init__(self, pull=None, runs=(), jobs=(), checks=()):
        self.pull = pull
        self.run_records = list(runs)
        self.job_records = list(jobs)
        self.check_records = list(checks)

    def pull_request(self, number):
        return self.pull

    def run(self, run_id):
        for entry in self.run_records:
            if entry.get("id") == run_id:
                return entry
        return {}

    def runs_for_head(self, sha):
        return self.run_records

    def jobs(self, run_id):
        return self.job_records

    def check_runs(self, sha):
        return self.check_records


class CommandLineTests(unittest.TestCase):
    """The command prints one machine-readable answer."""

    def _run(self, argv, api):
        buffer = io.StringIO()
        with contextlib.redirect_stdout(buffer):
            code = queue_state.main(argv, api=api)
        return code, json.loads(buffer.getvalue())

    def test_a_pull_request_reports_the_state_of_its_head(self):
        api = StubApi(
            pull={"head": {"sha": "abc123"}},
            runs=[{"id": 42, "name": queue_state.GATE_WORKFLOW_NAME,
                   "head_sha": "abc123"}],
            jobs=[gate_job()],
            checks=[gate_check()])
        code, answer = self._run(["--pr", "550"], api)
        self.assertEqual(code, 0)
        self.assertEqual(answer["verdict"], queue_state.VERDICT_QUEUED)
        self.assertEqual(answer["pr"], 550)
        self.assertEqual(answer["head_sha"], "abc123")
        self.assertEqual(answer["run_id"], 42)
        self.assertTrue(answer["queued"])

    def test_a_run_reports_the_state_of_its_gate(self):
        api = StubApi(
            runs=[{"id": 42, "name": queue_state.GATE_WORKFLOW_NAME,
                   "head_sha": "abc123"}],
            jobs=[gate_job()],
            checks=[gate_check()])
        code, answer = self._run(["--run", "42"], api)
        self.assertEqual(code, 0)
        self.assertEqual(answer["run_id"], 42)
        self.assertEqual(answer["verdict"], queue_state.VERDICT_QUEUED)

    def test_the_answer_carries_the_fields_a_reader_needs(self):
        api = StubApi(pull={"head": {"sha": "abc123"}}, checks=[])
        _code, answer = self._run(["--pr", "550"], api)
        for field in ("pr", "head_sha", "gate_state", "queued", "running",
                      "verdict", "evidence", "checked_at", "exit_code",
                      "next_action", "reason"):
            with self.subTest(field=field):
                self.assertIn(field, answer)

    def test_the_evidence_carries_the_started_at_of_every_job(self):
        api = StubApi(
            pull={"head": {"sha": "abc123"}},
            runs=[{"id": 42, "name": queue_state.GATE_WORKFLOW_NAME}],
            jobs=[gate_job(), sibling_check(id=7, name="F01 runner regression")],
            checks=[gate_check()])
        _code, answer = self._run(["--pr", "550"], api)
        jobs = answer["evidence"]["jobs"]
        self.assertEqual(len(jobs), 2)
        for job in jobs:
            with self.subTest(job=job["name"]):
                self.assertIn("started_at", job)
                self.assertIn("status", job)

    def test_an_undetermined_state_exits_non_zero(self):
        api = StubApi(pull={"head": {"sha": "abc123"}}, checks=[])
        code, answer = self._run(["--pr", "550"], api)
        self.assertNotEqual(code, 0)
        self.assertEqual(answer["verdict"], queue_state.VERDICT_MISSING)
        self.assertFalse(answer["passed"])

    def test_the_repository_comes_from_the_environment(self):
        previous = os.environ.get("GITHUB_REPOSITORY")
        os.environ["GITHUB_REPOSITORY"] = "someone/something"
        try:
            self.assertEqual(queue_state.default_repo(), "someone/something")
        finally:
            if previous is None:
                os.environ.pop("GITHUB_REPOSITORY", None)
            else:
                os.environ["GITHUB_REPOSITORY"] = previous

    def test_a_remote_url_yields_the_repository(self):
        for url in ("git@github.com:magician336/lingdoc.git",
                    "https://github.com/magician336/lingdoc.git",
                    "https://github.com/magician336/lingdoc",
                    "ssh://git@github.com/magician336/lingdoc.git"):
            with self.subTest(url=url):
                self.assertEqual(queue_state._repo_from_remote(url),
                                 "magician336/lingdoc")

    def test_a_url_that_is_not_a_repository_yields_nothing(self):
        self.assertEqual(queue_state._repo_from_remote("not-a-url"), "")


class WorkflowPolicyTests(unittest.TestCase):
    """The workflow keeps the policy this ticket decided."""

    def test_the_gate_does_not_cancel_a_run_that_is_waiting(self):
        """AC-2: a new push must not cancel the run that is already queued."""
        document = yaml.safe_load(REVIEW_WORKFLOW.read_text(encoding="utf-8"))
        concurrency = document.get("concurrency") or {}
        self.assertIn("cancel-in-progress", concurrency)
        self.assertFalse(concurrency["cancel-in-progress"],
                         "the gate still cancels an in-flight run; a new push "
                         "can cancel the only verification of a head")

    def test_the_workflow_still_publishes_the_gate_this_module_judges(self):
        document = yaml.safe_load(REVIEW_WORKFLOW.read_text(encoding="utf-8"))
        names = [str(job.get("name") or key)
                 for key, job in (document.get("jobs") or {}).items()]
        self.assertIn(GATE_NAME, names)

    def test_the_gate_still_runs_on_a_hosted_runner(self):
        """This ticket changes the cancellation policy only."""
        document = yaml.safe_load(REVIEW_WORKFLOW.read_text(encoding="utf-8"))
        gate = (document.get("jobs") or {}).get("gate") or {}
        self.assertEqual(gate.get("runs-on"), "ubuntu-latest")


if __name__ == "__main__":
    unittest.main()
