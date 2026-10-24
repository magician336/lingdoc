"""Queued or running?  A machine-readable judgement of the pull request gate.

What this file is
-----------------
The pull request gate of this repository publishes exactly one check,
``LingDoc PR gate``.  While that check has not finished, GitHub shows one
word: ``in progress``.  That word covers two different situations:

* the job is running its steps, and
* the job has not been given a runner yet and is waiting in the queue.

The two need different reactions.  A running gate is waited for.  A queued
gate is waited for too, but pushing another commit does not help: it adds one
more job to the queue, and under ``cancel-in-progress: true`` it also cancels
the run that was already waiting.  A person who cannot tell the two apart
keeps pushing, and the gate never turns green.  Issue #550 is that
situation.

This module answers "which of the two is it?" with data instead of with a
look at the picture.  The judgement reads the ``started_at`` field of the
gate's job (and, as a fallback, of the gate's check-run) and nothing else.
``started_at`` is empty exactly while the job waits for a runner, so it is
the one field that separates the two situations.  The ``status`` word cannot
do the job: GitHub reports ``in_progress`` for a job that has not started.

The judgement is deliberately conservative.  When the data does not say, the
verdict is ``missing`` -- never ``passed``.  A verdict that could not be
established must not be read as a green gate.  The same rule applies to the
difference between a field that is ``null`` and a field that is not there at
all: ``null`` is GitHub saying "this job has not started", while a missing
field is GitHub not saying anything.

There is a third observation form, and it is the one that has no check-run at
all.  A workflow run that has not been given a runner yet has neither a job
record nor a check-run, so the gate of that head looks absent from the Checks
API even though it exists and is waiting.  That is not "the gate did not
run": the run says the gate is waiting, so the verdict is ``queued``.  A run
that has started, or that has finished, without a check-run is a different
thing -- there the data does not say, and the verdict stays ``missing``.

How to use it
-------------
::

    python scripts/ci/lingdoc_gate_queue_state.py --pr 550
    python scripts/ci/lingdoc_gate_queue_state.py --run 40022872106

The command prints one JSON object on stdout and exits with a code that says
how urgent the answer is:

===========  ====  =========================================================
verdict      exit  what it means
===========  ====  =========================================================
``queued``   0     the gate waits for a runner; push nothing, wait
``running``  0     the gate is running its steps; wait
``passed``   0     the gate is green on this head
``failed``   1     the gate did not pass; the head is not green
``missing``  2     the state could not be established; treat it as not green
===========  ====  =========================================================

``queued`` and ``running`` are normal states, not failures: a script that
watches the gate reads the JSON, not the exit code.  Only ``failed`` and
``missing`` are non-zero, and they are different non-zeros, so a caller can
tell "the gate said no" from "the gate could not be asked".

What this file is not
---------------------
This module does not decide anything about the merge.  It reports what the
gate is doing; the ruleset still demands a green ``LingDoc PR gate`` on the
current head.  Nothing here is wired into the merge flow, and nothing here
changes what the gate runs.

Terms
-----
``check-run``
    The record of one check on one commit, as the Checks API reports it.
``job``
    The record of one job of one workflow run, as the Actions API reports
    it.  It is the record that knows about the runner allocation.
``gate``
    The check the branch ruleset demands: the job ``gate`` of
    ``.github/workflows/lingdoc-review.yml``, whose display name is
    ``LingDoc PR gate``.
"""
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from datetime import datetime, timezone
from typing import Any, Mapping, Sequence

import lingdoc_required_checks

#: The check the branch ruleset demands.  The name is not written out a
#: second time here: ``lingdoc_required_checks`` is the one list of the
#: names the ruleset asks for.
GATE_CHECK_NAME = lingdoc_required_checks.REQUIRED_CHECK_NAMES[0]

#: The workflow that publishes the gate.
GATE_WORKFLOW_NAME = "LingDoc Review"

#: The verdicts this module can reach.
VERDICT_QUEUED = "queued"
VERDICT_RUNNING = "running"
VERDICT_PASSED = "passed"
VERDICT_FAILED = "failed"
VERDICT_MISSING = "missing"

VERDICTS = (VERDICT_QUEUED, VERDICT_RUNNING, VERDICT_PASSED,
            VERDICT_FAILED, VERDICT_MISSING)

#: Exit codes.  ``queued`` and ``running`` are normal states, so they share
#: the success code; ``failed`` and ``missing`` are different non-zero codes
#: because they ask different things of the caller.
EXIT_OK = 0
EXIT_FAILED = 1
EXIT_MISSING = 2

EXIT_CODES = {
    VERDICT_QUEUED: EXIT_OK,
    VERDICT_RUNNING: EXIT_OK,
    VERDICT_PASSED: EXIT_OK,
    VERDICT_FAILED: EXIT_FAILED,
    VERDICT_MISSING: EXIT_MISSING,
}

#: What a reader should do with each verdict.
NEXT_ACTIONS = {
    VERDICT_QUEUED: (
        "The gate has not started yet: it is either waiting for a runner or "
        "waiting behind an earlier run of the same pull request. Do not push "
        "again; another commit does not shorten the wait, it only adds one "
        "more waiting run."
    ),
    VERDICT_RUNNING: (
        "The gate is running. Wait for it to finish; do not push again."
    ),
    VERDICT_PASSED: (
        "The gate is green on this head."
    ),
    VERDICT_FAILED: (
        "The gate did not pass, so this head is not green. Read the run's log."
    ),
    VERDICT_MISSING: (
        "The state of the gate could not be established: this head has no "
        "gate check, or GitHub did not report enough to judge it. Treat the "
        "head as not green and read the run list."
    ),
}

#: The repository this module reads when the caller does not say.
DEFAULT_REPO = "magician336/lingdoc"

#: The workflow run states that mean the run has not started a job yet.
#: A run in one of these states has no job record and no check-run, which is
#: the third observation form of issue #550: the aggregate gate has not
#: produced a check-run yet.
RUN_NOT_STARTED = ("queued", "pending", "waiting", "requested")

#: What a run that has not started yet is waiting for.
#:
#: The two kinds of wait need the same reaction -- push nothing -- but a
#: different explanation: one waits for a runner, the other waits behind an
#: earlier run of the same pull request.  The reason says which one it is,
#: so a reader does not have to look the run status up to know it.
RUN_WAIT_REASON = {
    "queued": "the gate is queued and waiting for a runner",
    "pending": ("the gate is queued behind an earlier run of the same "
                "pull request"),
    "waiting": "the gate is queued and waiting for a runner",
    "requested": "the gate is queued and has not been started yet",
}

#: The text an evidence field carries when the record does not have it.
ABSENT = "<absent>"


class GateStateError(RuntimeError):
    """The GitHub data could not be read at all."""


class _Absent:
    """The marker for a field a record does not carry.

    It is not ``None``: ``None`` is a value GitHub publishes ("this job has
    not started"), while the marker means "GitHub did not say".
    """

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return "<absent>"


ABSENT_MARKER = _Absent()


def _field(record: Any, name: str) -> Any:
    """The value of a field, or the marker when the record does not carry it."""
    if isinstance(record, Mapping) and name in record:
        return record[name]
    return ABSENT_MARKER


def _first(records: Sequence[Any], name: str) -> Any:
    """The value of a field from the first record that carries it.

    The records are ordered from the most specific to the least specific: the
    job record knows about the runner allocation, the check-run record knows
    about the check.
    """
    for record in records:
        value = _field(record, name)
        if value is not ABSENT_MARKER:
            return value
    return ABSENT_MARKER


def _plain(value: Any) -> Any:
    """A field value as it can be published."""
    if value is ABSENT_MARKER:
        return ABSENT
    return value


def _gate_job(jobs: Sequence[Any], check: Any) -> Any:
    """The gate's job record within a run, if the run has one.

    A check-run and the job it reports share one id, so the id is the
    precise match; the display name is the fallback for a record that does
    not carry one.
    """
    jobs = list(jobs or ())
    check_id = _field(check, "id")
    if check_id is not ABSENT_MARKER:
        for job in jobs:
            if _field(job, "id") == check_id:
                return job
    for job in jobs:
        if _field(job, "name") == GATE_CHECK_NAME:
            return job
    return None


def _evidence_of(record: Any) -> Any:
    """The fields of a record this judgement is allowed to look at."""
    if record is None:
        return None
    return {
        "id": _plain(_field(record, "id")),
        "name": _plain(_field(record, "name")),
        "status": _plain(_field(record, "status")),
        "conclusion": _plain(_field(record, "conclusion")),
        "started_at": _plain(_field(record, "started_at")),
        "completed_at": _plain(_field(record, "completed_at")),
    }


def _all_other_jobs_completed(jobs: Sequence[Any], gate_job: Any) -> bool:
    """Whether every job of the run but the gate has finished.

    This is the corroborating observation of issue #550: a run whose other
    jobs are done while the gate check is still open is almost certainly
    waiting for a runner rather than running.  It is reported as evidence;
    the verdict itself rests on ``started_at``.
    """
    gate_id = _field(gate_job, "id")
    others = [job for job in (jobs or ()) if _field(job, "id") != gate_id]
    if not others:
        return False
    return all(_field(job, "status") == "completed" for job in others)


def _all_other_checks_completed(check: Any, checks: Sequence[Any]) -> bool:
    """Whether every other check of the same head has finished.

    A head carries the checks of every workflow that ran for it.  When all of
    them are done and the gate check is the only one still open, the gate is
    the only job left waiting -- for a runner.  It is reported as evidence;
    the verdict itself rests on ``started_at``.
    """
    gate_id = _field(check, "id")
    others = [entry for entry in (checks or ()) if _field(entry, "id") != gate_id]
    if not others:
        return False
    return all(_field(entry, "status") == "completed" for entry in others)


def _result(verdict: str, reason: str, evidence: Mapping[str, Any],
            status: Any = ABSENT_MARKER,
            conclusion: Any = ABSENT_MARKER) -> dict[str, Any]:
    """One judgement, in the shape every reader of this module expects."""
    return {
        "verdict": verdict,
        "queued": verdict == VERDICT_QUEUED,
        "running": verdict == VERDICT_RUNNING,
        "passed": verdict == VERDICT_PASSED,
        "failed": verdict == VERDICT_FAILED,
        "missing": verdict == VERDICT_MISSING,
        "reason": reason,
        "observed_status": _plain(status),
        "observed_conclusion": _plain(conclusion),
        "evidence": dict(evidence),
    }


def judge(check: Any, jobs: Sequence[Any],
          checks: Sequence[Any] = (), run: Any = None) -> dict[str, Any]:
    """The verdict for one observation of the gate.

    ``check`` is the gate's check-run record, or ``None`` when the head
    carries no such check.  ``jobs`` are the job records of the same run,
    ``checks`` the check-run records of the same head, and ``run`` the
    workflow run itself.  The judgement reads ``started_at`` and the
    conclusion; it never guesses from a status word alone.

    There is a third observation form: the gate's workflow run exists and has
    not started, so it has neither a job record nor a check-run.  That is not
    "the gate did not run" and it is certainly not "the gate passed" -- it is
    a gate that has not started, and it is judged as queued.
    """
    gate_job = _gate_job(jobs, check)
    records = [record for record in (gate_job, check) if record is not None]
    evidence = {
        "check": _evidence_of(check),
        "gate_job": _evidence_of(gate_job),
        "jobs": [_evidence_of(job) for job in (jobs or ())],
        "checks": [_evidence_of(entry) for entry in (checks or ())],
        "all_other_jobs_completed": _all_other_jobs_completed(jobs, gate_job),
        "all_other_checks_completed": _all_other_checks_completed(check, checks),
    }

    if not records:
        run_status = _plain(_field(run, "status"))
        if run_status in RUN_NOT_STARTED:
            return _result(
                VERDICT_QUEUED,
                "the aggregate gate has not produced a check-run yet (its "
                f"workflow run is {run_status!r}); "
                f"{RUN_WAIT_REASON[run_status]}",
                evidence, run_status, None)
        if run is None:
            return _result(
                VERDICT_MISSING,
                "this head has no gate check-run and no gate job, and no "
                "workflow run says the gate is waiting",
                evidence, run_status, None)
        return _result(
            VERDICT_MISSING,
            "the aggregate gate has not produced a check-run, and its workflow "
            f"run ({run_status!r}) does not say it is waiting; the gate's "
            "state cannot be established",
            evidence, run_status, None)

    status = _first(records, "status")
    conclusion = _first(records, "conclusion")
    completed_at = _first(records, "completed_at")
    started_at = _first(records, "started_at")

    finished = status == "completed" or completed_at is not None

    if finished:
        if conclusion is ABSENT_MARKER or conclusion is None:
            return _result(
                VERDICT_MISSING,
                "the gate finished but GitHub reported no conclusion; the "
                "outcome is undetermined",
                evidence, status, conclusion)
        if conclusion == "success":
            return _result(VERDICT_PASSED, "the gate finished successfully",
                           evidence, status, conclusion)
        if conclusion == "skipped":
            return _result(
                VERDICT_FAILED,
                "the gate was skipped; skipped means NOT RUN, not passed",
                evidence, status, conclusion)
        return _result(
            VERDICT_FAILED,
            f"the gate finished with conclusion {conclusion!r}",
            evidence, status, conclusion)

    if started_at is ABSENT_MARKER:
        return _result(
            VERDICT_MISSING,
            "the gate record carries no started_at; GitHub did not say whether "
            "the job has begun",
            evidence, status, conclusion)
    if started_at is None:
        return _result(
            VERDICT_QUEUED,
            "the gate job has not started; it is waiting for a runner",
            evidence, status, conclusion)
    return _result(
        VERDICT_RUNNING,
        "the gate job has started; it is running its steps",
        evidence, status, conclusion)


def _pick_gate_check(checks: Sequence[Any], jobs: Sequence[Any]) -> Any:
    """The gate's check-run among the checks of one head.

    A head can carry more than one: a re-run publishes a second check-run
    under the same name.  The one that belongs to the gate job of the run in
    hand wins; without a job record the newest one does.
    """
    gate_checks = [check for check in (checks or ())
                   if _field(check, "name") == GATE_CHECK_NAME]
    if not gate_checks:
        return None
    job_ids = {_field(job, "id") for job in (jobs or ())}
    for check in gate_checks:
        if _field(check, "id") in job_ids:
            return check
    return max(gate_checks, key=lambda check: _field(check, "id") or 0)


def _run_evidence(run: Any) -> Any:
    """The fields of a run this judgement reports.

    The workflow run object publishes no ``cancelled_at`` field: a run that
    was cancelled says so in its ``conclusion``, which is why this reports a
    ``cancelled`` boolean instead.
    """
    if run is None:
        return None
    conclusion = _plain(_field(run, "conclusion"))
    return {
        "id": _plain(_field(run, "id")),
        "name": _plain(_field(run, "name")),
        "status": _plain(_field(run, "status")),
        "conclusion": conclusion,
        "cancelled": conclusion == "cancelled",
        "created_at": _plain(_field(run, "created_at")),
        "run_started_at": _plain(_field(run, "run_started_at")),
        "updated_at": _plain(_field(run, "updated_at")),
    }


def _now() -> str:
    """The moment the judgement was made, in UTC."""
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _state(pr: Any, run_id: Any, head_sha: str, verdict: Mapping[str, Any],
           run: Any) -> dict[str, Any]:
    """The machine-readable answer: one JSON object, one exit code."""
    answer = {
        "pr": pr,
        "run_id": run_id,
        "head_sha": head_sha,
        "gate_state": verdict["verdict"],
        "verdict": verdict["verdict"],
        "queued": verdict["queued"],
        "running": verdict["running"],
        "passed": verdict["passed"],
        "failed": verdict["failed"],
        "missing": verdict["missing"],
        "reason": verdict["reason"],
        "observed_status": verdict["observed_status"],
        "observed_conclusion": verdict["observed_conclusion"],
        "next_action": NEXT_ACTIONS[verdict["verdict"]],
        "evidence": dict(verdict["evidence"]),
        "checked_at": _now(),
    }
    answer["evidence"]["run"] = _run_evidence(run)
    answer["exit_code"] = EXIT_CODES[verdict["verdict"]]
    return answer


class GitHubApi:
    """The few GitHub reads this judgement needs.

    The module reads GitHub through the ``gh`` CLI, which is already
    authenticated on a developer machine and in this repository's own
    tooling.  Every read is a plain ``GET``.
    """

    def __init__(self, repo: str) -> None:
        self.repo = repo

    def _get(self, path: str) -> Any:
        try:
            completed = subprocess.run(
                ["gh", "api", path], capture_output=True, text=True,
                encoding="utf-8", errors="replace", check=False)
        except OSError as error:
            raise GateStateError(f"gh could not be run: {error}") from error
        if completed.returncode != 0:
            detail = completed.stderr.strip() or f"exit code {completed.returncode}"
            raise GateStateError(f"gh api {path} failed: {detail}")
        try:
            return json.loads(completed.stdout or "null")
        except json.JSONDecodeError as error:
            raise GateStateError(f"gh api {path} did not return JSON: {error}") from error

    def pull_request(self, number: int) -> Any:
        return self._get(f"repos/{self.repo}/pulls/{number}")

    def run(self, run_id: int) -> Any:
        return self._get(f"repos/{self.repo}/actions/runs/{run_id}")

    def runs_for_head(self, sha: str) -> list[Any]:
        payload = self._get(
            f"repos/{self.repo}/actions/runs?head_sha={sha}&per_page=100")
        return list((payload or {}).get("workflow_runs") or ())

    def jobs(self, run_id: int) -> list[Any]:
        payload = self._get(
            f"repos/{self.repo}/actions/runs/{run_id}/jobs?per_page=100")
        return list((payload or {}).get("jobs") or ())

    def check_runs(self, sha: str) -> list[Any]:
        payload = self._get(
            f"repos/{self.repo}/commits/{sha}/check-runs?per_page=100")
        return list((payload or {}).get("check_runs") or ())


def state_for_pull_request(number: int, api: GitHubApi) -> dict[str, Any]:
    """The state of the gate on the head of a pull request."""
    pull = api.pull_request(number)
    head_sha = str(((pull or {}).get("head") or {}).get("sha") or "")
    runs = api.runs_for_head(head_sha) if head_sha else []
    gate_runs = [run for run in runs
                 if _field(run, "name") == GATE_WORKFLOW_NAME]
    run = max(gate_runs, key=lambda item: _field(item, "id") or 0, default=None)
    run_id = _field(run, "id")
    jobs = api.jobs(run_id) if run_id is not ABSENT_MARKER else []
    checks = api.check_runs(head_sha) if head_sha else []
    check = _pick_gate_check(checks, jobs)
    verdict = judge(check, jobs, checks, run)
    return _state(pr=number, run_id=None if run_id is ABSENT_MARKER else run_id,
                  head_sha=head_sha, verdict=verdict, run=run)


def state_for_run(run_id: int, api: GitHubApi) -> dict[str, Any]:
    """The state of the gate inside one workflow run."""
    run = api.run(run_id)
    head_sha = str((run or {}).get("head_sha") or "")
    jobs = api.jobs(run_id)
    checks = api.check_runs(head_sha) if head_sha else []
    check = _pick_gate_check(checks, jobs)
    verdict = judge(check, jobs, checks, run)
    return _state(pr=None, run_id=run_id, head_sha=head_sha,
                  verdict=verdict, run=run)


def _repo_from_remote(url: str) -> str:
    """The ``owner/name`` of a GitHub remote URL."""
    text = url.strip()
    if text.endswith(".git"):
        text = text[:-len(".git")]
    if text.startswith("git@") and ":" in text:
        text = text.split(":", 1)[1]
    else:
        for prefix in ("https://", "http://", "ssh://git@", "ssh://"):
            if text.startswith(prefix):
                text = text[len(prefix):]
                break
        host = text.split("/", 1)[0]
        if "@" in host:
            text = text.split("/", 1)[1]
    parts = [part for part in text.split("/") if part]
    return "/".join(parts[-2:]) if len(parts) >= 2 else ""


def default_repo() -> str:
    """The repository to read: the environment, else the origin remote."""
    from_env = os.environ.get("GITHUB_REPOSITORY", "").strip()
    if from_env:
        return from_env
    try:
        remote = subprocess.run(
            ["git", "remote", "get-url", "origin"], capture_output=True,
            text=True, encoding="utf-8", errors="replace", check=False)
    except OSError:
        remote = None
    if remote is not None and remote.returncode == 0:
        found = _repo_from_remote(remote.stdout)
        if found:
            return found
    return DEFAULT_REPO


def main(argv: Sequence[str] | None = None, api: GitHubApi | None = None) -> int:
    """Print the state of the gate as JSON; return the exit code."""
    parser = argparse.ArgumentParser(
        prog="lingdoc_gate_queue_state",
        description="Say whether the pull request gate is queued, running, "
                    "passed or failed.")
    parser.add_argument("--pr", type=int, help="the pull request number")
    parser.add_argument("--run", type=int, help="the workflow run id")
    parser.add_argument(
        "--repo", default=None,
        help="owner/name of the repository (default: GITHUB_REPOSITORY, else "
             "the origin remote)")
    args = parser.parse_args(argv)
    if (args.pr is None) == (args.run is None):
        parser.error("give exactly one of --pr or --run")

    client = api if api is not None else GitHubApi(args.repo or default_repo())
    try:
        if args.pr is not None:
            answer = state_for_pull_request(args.pr, client)
        else:
            answer = state_for_run(args.run, client)
    except GateStateError as error:
        verdict = judge(None, ())
        verdict["reason"] = f"the GitHub data could not be read: {error}"
        answer = _state(pr=args.pr, run_id=args.run, head_sha="",
                        verdict=verdict, run=None)
    # The answer carries names that GitHub published, and those can hold
    # characters this machine's console code page cannot encode -- Windows
    # defaults to GBK here, and a name may carry U+2713 or an emoji.  A
    # crash at this point would turn a readable answer into a traceback,
    # and a traceback exits non-zero, which a caller would read as "the
    # gate said no".  So the console is told to replace what it cannot
    # encode instead of failing.
    reconfigure = getattr(sys.stdout, "reconfigure", None)
    if reconfigure is not None:
        try:
            reconfigure(encoding="utf-8", errors="replace")
        except (ValueError, OSError):
            pass
    print(json.dumps(answer, indent=2, ensure_ascii=False))
    return int(answer["exit_code"])


if __name__ == "__main__":
    raise SystemExit(main())
