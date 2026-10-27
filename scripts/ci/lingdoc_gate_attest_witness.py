"""The gate step that assembles the reuse evidence and asks the witness.

What this file is
-----------------
The witness decision (``lingdoc_gate_attest_reuse``) is a pure function: it
fetches nothing and reads no file.  This module is its hands.  It is the
script the gate's attest step runs, and its one job is to turn GitHub's
machine records into the two arguments the decision needs -- the current
observation of this pull request and the candidate run's anchor record --
and to publish the answer where the workflow reads it.

The records come from three places, and from nowhere else:

* this run's own anchor (``gate-anchor.json`` in the artifact directory the
  scope step has just written) is the current observation: the same
  computation, made moments ago, of the pull request, its head and the
  main tip its merge ref integrates;
* the run list of that head, read through the Actions API, yields the one
  candidate: the latest earlier run of the gate workflow on the same head
  (the current run itself is excluded).  The latest, not "any green one":
  a failing run of the same merge ref is itself evidence, and stepping
  over it to an older green run would hide exactly the failure the author
  needs to see;
* that candidate run's checks artifact holds its anchor.  The candidate's
  ``status`` and ``conclusion`` always come from the run record the Actions
  API reports -- never from the anchor's artifact-side ``conclusion``,
  which is the scope step's own verdict and misses a later step's failure
  (#628).  The anchor is used for the five condition fields and for the
  cross-checks: an anchor whose run id or head sha contradicts the run
  record, or whose scope verdict says failure behind a successful run
  record, is a record that does not tell one story, and no story is
  reused.

Every way the fetching can fail -- the anchor missing or unreadable, the
artifact expired, the download broken, a record of the wrong shape, the API
timing out or rate-limiting -- falls back to a full rerun.  The witness is
an accelerator, not a gate: its fallback direction is always "more
verification", and it never fails the gate itself.

How to use it
-------------
The gate's attest step runs it after the scope step and before the heavy
steps::

    python scripts/ci/lingdoc_gate_attest_witness.py \
      --artifact-dir "$RUNNER_TEMP/lingdoc-checks"

It prints the verdict as JSON on stdout, writes ``reuse`` and
``reused_run_id`` into ``$GITHUB_OUTPUT``, and appends a summary section
saying which run was reused, with the five conditions compared, or why the
gate reruns in full.  The complete judgement -- reuse, reason, the five
conditions compared field by field, and the moment it was decided -- is
also written into the artifact directory as ``gate-attest-decision.json``,
for reuse and fallback alike, so the audit trail survives the run log.
The exit code is always zero.

When the verdict is reuse, the candidate run's scope verdict records
(``scope.json`` and its anchor) are copied from the candidate's artifact
into this run's artifact directory as ``reused-scope.json`` and
``reused-gate-anchor.json``, so require-coverage's "skipped is not passed"
stays auditable on a reused conclusion.

What this file is not
---------------------
This module decides nothing: whether the records allow a reuse is
``lingdoc_gate_attest_reuse``'s answer, and this module would not know a
condition from a coincidence.  It also does not touch the workflow's
conclusion; the publish step reports what ran, skipped steps included.
"""
from __future__ import annotations

import argparse
import io
import json
import os
import subprocess
import sys
import zipfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Mapping, Sequence

import lingdoc_gate_attest_reuse as reuse
import lingdoc_gate_queue_state as queue_state
import lingdoc_gate_decision as gate_decision

#: The one trigger whose gate may ask the witness.  Every other trigger --
#: ``opened``, ``synchronize``, ``reopened``, the full ``workflow_dispatch``
#: escape hatch -- falls back without asking, so their behaviour stays
#: exactly what it was before the witness existed.
REUSE_EVENT = "ready_for_review"

#: The anchor record, inside this run's artifact directory and inside the
#: candidate run's artifact.  The name is the scope step's contract; it is
#: read from the module that froze it, never spelled a second time.
ANCHOR_FILE = gate_decision.GATE_ANCHOR_FILE

#: The witness's own record in this run's artifact directory: the complete
#: verdict -- reuse, reason, and the five conditions compared field by field
#: -- plus the moment it was decided.  A reuse and a fallback both write it,
#: so the audit question "why was this run (not) reused" survives the run
#: log (spec #627, user story 14).
DECISION_FILE = "gate-attest-decision.json"

#: The candidate's records, handed on into this run's artifact on reuse.
REUSED_SCOPE_FILE = "reused-scope.json"
REUSED_ANCHOR_FILE = "reused-gate-anchor.json"

#: Every full run uploads its checks under this name prefix.
CHECKS_ARTIFACT_PREFIX = "lingdoc-checks-"


def _fallback(current: Any, reason: str) -> dict[str, Any]:
    """The no answer, in the shape the decision module publishes.

    The shape is not copied here: ``decide_reuse`` builds it from records
    that cannot be judged, and only the reason is replaced with the one
    sentence saying what actually went wrong.
    """
    verdict = reuse.decide_reuse(current, None)
    verdict["reason"] = reason
    return verdict


def _read_current_anchor(artifact_dir: Path | str | None) -> dict[str, Any] | None:
    """The current observation: this run's own anchor, or ``None``.

    The scope step wrote the anchor moments ago from the same event and the
    same merge ref this run tests, so its ``pr``, ``head_sha`` and
    ``main_sha`` are the current observation -- and an anchor that cannot be
    read is a run that cannot be witnessed.  The shape judgement itself is
    left to the decision: what is handed on is exactly the three fields it
    reads, and nothing more.
    """
    if artifact_dir is None:
        return None
    try:
        record = json.loads((Path(artifact_dir) / ANCHOR_FILE).read_text(
            encoding="utf-8"))
    except (OSError, ValueError):
        return None
    if not isinstance(record, Mapping):
        return None
    head_sha = record.get("head_sha")
    if not isinstance(head_sha, str) or not head_sha:
        return None
    return {"pr": record.get("pr"), "head_sha": head_sha,
            "main_sha": record.get("main_sha")}


def _pick_candidate(runs: Any, current_run_id: int | None) -> Mapping | None:
    """The one candidate: the latest earlier gate run on this head.

    The current run reports the same head sha as the pull request, so it is
    in the list the API returns; it is excluded by its own run id.  A run of
    another workflow is not the gate's evidence.
    """
    gate_runs = [run for run in (runs or [])
                 if isinstance(run, Mapping)
                 and run.get("name") == reuse.GATE_WORKFLOW_NAME
                 and run.get("id") != current_run_id]
    return max(gate_runs, key=lambda run: run.get("id") or 0, default=None)


def _candidate_bundle(api: Any, run: Mapping) -> tuple[Mapping, dict[str, str]]:
    """The candidate's anchor record and the artifact's files, or an error.

    Everything between the artifact list and the anchor record -- the
    listing, the download, the zip, the JSON -- can fail in its own way;
    they all raise here and the caller falls back.
    """
    artifacts = api.artifacts(run["id"])
    checks = [entry for entry in (artifacts or [])
              if isinstance(entry, Mapping)
              and str(entry.get("name", "")).startswith(CHECKS_ARTIFACT_PREFIX)]
    if not checks:
        raise queue_state.GateStateError(
            f"run {run['id']} publishes no {CHECKS_ARTIFACT_PREFIX}* artifact")
    data = api.artifact_zip(checks[0]["id"])
    bundle: dict[str, str] = {}
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        for name in archive.namelist():
            bundle[name] = archive.read(name).decode("utf-8")
    anchor_text = bundle.get(ANCHOR_FILE)
    if anchor_text is None:
        raise queue_state.GateStateError(
            f"the artifact carries no {ANCHOR_FILE}")
    anchor = json.loads(anchor_text)
    if not isinstance(anchor, Mapping):
        raise queue_state.GateStateError(f"the {ANCHOR_FILE} is malformed")
    return anchor, bundle


def _hand_on_scope_records(artifact_dir: Path | str | None,
                           bundle: Mapping[str, str]) -> None:
    """Copy the reused run's scope verdict records into this run's artifact.

    A copy that fails never changes the verdict -- the reuse is already
    decided and is sound; only the audit trail would be thinner.
    """
    if artifact_dir is None:
        return
    for source, target in (("scope.json", REUSED_SCOPE_FILE),
                           (ANCHOR_FILE, REUSED_ANCHOR_FILE)):
        if source not in bundle:
            print(f"warning: the reused run's {source} is not in its "
                  f"artifact; {target} is not handed on.", file=sys.stderr)
            continue
        try:
            (Path(artifact_dir) / target).write_text(bundle[source],
                                                     encoding="utf-8")
        except OSError as error:
            print(f"warning: {target} could not be written ({error}); "
                  "the reuse verdict is unaffected.", file=sys.stderr)


def _now_iso() -> str:
    """The decision's moment, UTC: what makes the record auditable."""
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def _write_decision_record(artifact_dir: Path | str | None,
                           verdict: Mapping[str, Any]) -> None:
    """The complete judgement, beside the records the gate already publishes.

    The stdout verdict and the summary section are gone with the run log;
    the artifact stays.  A reuse and a fallback both leave their record
    here, so the audit question "why was this run (not) reused" -- with the
    five conditions compared field by field and the decision's moment --
    stays answerable (spec #627, user story 14).  The record is an
    ancillary product: a failure to write it degrades the audit trail,
    never the verdict.
    """
    if artifact_dir is None:
        return
    try:
        record = dict(verdict)
        record["decided_at"] = _now_iso()
        (Path(artifact_dir) / DECISION_FILE).write_text(
            json.dumps(record, ensure_ascii=True, indent=2) + "\n",
            encoding="utf-8")
    except (OSError, TypeError, ValueError) as error:
        print(f"warning: the witness decision record could not be written "
              f"({error}); the verdict is unaffected.", file=sys.stderr)


def decide(event_name: str, artifact_dir: Path | str | None, api: Any,
           current_run_id: int | None) -> dict[str, Any]:
    """Whether this gate run may reuse an earlier green run, and why.

    ``event_name`` is this run's trigger; only ``ready_for_review`` asks.
    ``artifact_dir`` is the artifact directory the scope step has just
    written this run's anchor into.  ``api`` answers the three reads the
    candidate needs; ``current_run_id`` excludes this run from its own
    candidates.  The verdict is the decision module's, whatever happens:
    the assembly only chooses between handing it complete records and
    handing it a fallback reason.  Both outcomes -- reuse and fallback --
    leave the complete judgement in the artifact directory as
    ``DECISION_FILE``.
    """
    verdict = _decide(event_name, artifact_dir, api, current_run_id)
    _write_decision_record(artifact_dir, verdict)
    return verdict


def _decide(event_name: str, artifact_dir: Path | str | None, api: Any,
            current_run_id: int | None) -> dict[str, Any]:
    """The reuse judgement, without the artifact record (see :func:`decide`)."""
    if event_name != REUSE_EVENT:
        return _fallback(None, f"the {event_name or 'unknown'} trigger never "
                               "reuses; the gate reruns in full")

    current = _read_current_anchor(artifact_dir)
    if current is None:
        return _fallback(None, "this run's own anchor record could not be "
                               "read; nothing is reused")

    try:
        runs = api.runs_for_head(current["head_sha"])
    except Exception as error:  # any API failure falls back, never fails
        return _fallback(current, f"the run list could not be read ({error}); "
                                  "nothing is reused")

    candidate_run = _pick_candidate(runs, current_run_id)
    if candidate_run is None:
        return _fallback(current, "no earlier gate run of this head exists; "
                                  "nothing is reused")

    # The run record is the source of the candidate's own verdict (#628):
    # the anchor's conclusion is the scope step's verdict and is never read
    # as the run's.
    status = candidate_run.get("status")
    conclusion = candidate_run.get("conclusion")
    if not isinstance(status, str) or not status \
            or not isinstance(conclusion, str) or not conclusion:
        return _fallback(current, "the candidate run record carries no "
                                  "status or conclusion; nothing is reused")

    try:
        anchor, bundle = _candidate_bundle(api, candidate_run)
    except Exception as error:  # any fetch or parse failure falls back
        return _fallback(current, "the candidate run's anchor record could "
                                  f"not be read ({error}); nothing is reused")

    # Cross-checks: the anchor and the run record must tell one story.
    if anchor.get("run_id") != candidate_run.get("id"):
        return _fallback(current, f"the anchor records run "
                                  f"{anchor.get('run_id')!r}, but the run "
                                  f"record says {candidate_run.get('id')!r}; "
                                  "the record contradicts itself, so nothing "
                                  "is reused")
    if anchor.get("head_sha") != candidate_run.get("head_sha"):
        return _fallback(current, "the anchor records a different head than "
                                  "the run record; the record contradicts "
                                  "itself, so nothing is reused")
    anchor_conclusion = anchor.get("conclusion")
    if anchor_conclusion not in ("success", "failure"):
        return _fallback(current, "the anchor's scope verdict is unreadable; "
                                  "nothing is reused")
    if anchor_conclusion == "failure" and conclusion == "success":
        return _fallback(current, "the anchor records a failed scope step "
                                  "while the run record says success; the "
                                  "record contradicts itself, so nothing is "
                                  "reused")

    candidate = {"pr": anchor.get("pr"),
                 "head_sha": anchor.get("head_sha"),
                 "main_sha": anchor.get("main_sha"),
                 "run_id": anchor.get("run_id"),
                 "conclusion": conclusion,
                 "status": status,
                 "workflow": anchor.get("workflow")}
    verdict = reuse.decide_reuse(current, candidate)
    if verdict.get("reuse"):
        _hand_on_scope_records(artifact_dir, bundle)
    return verdict


class GitHubApi:
    """The three GitHub reads the candidate needs, through ``gh api``.

    The same CLI the queue-state judgement reads through: authenticated on
    the runner and on a developer machine, and every read is a plain
    ``GET``.  Reading another run's artifact needs ``actions: read``; the
    gate workflow grants it for exactly this witness.
    """

    def __init__(self, repo: str) -> None:
        self.repo = repo

    def _run_gh(self, path: str) -> bytes:
        try:
            completed = subprocess.run(
                ["gh", "api", path], capture_output=True, check=False)
        except OSError as error:
            raise queue_state.GateStateError(
                f"gh could not be run: {error}") from error
        if completed.returncode != 0:
            detail = (completed.stderr or b"").decode(
                "utf-8", errors="replace").strip() \
                or f"exit code {completed.returncode}"
            raise queue_state.GateStateError(f"gh api {path} failed: {detail}")
        return completed.stdout

    def _get(self, path: str) -> Any:
        raw = self._run_gh(path)
        try:
            return json.loads(raw.decode("utf-8", errors="replace") or "null")
        except json.JSONDecodeError as error:
            raise queue_state.GateStateError(
                f"gh api {path} did not return JSON: {error}") from error

    def runs_for_head(self, sha: str) -> list[Any]:
        payload = self._get(
            f"repos/{self.repo}/actions/runs?head_sha={sha}&per_page=100")
        return list((payload or {}).get("workflow_runs") or ())

    def artifacts(self, run_id: int) -> list[Any]:
        payload = self._get(
            f"repos/{self.repo}/actions/runs/{run_id}/artifacts?per_page=100")
        return list((payload or {}).get("artifacts") or ())

    def artifact_zip(self, artifact_id: int) -> bytes:
        return self._run_gh(
            f"repos/{self.repo}/actions/artifacts/{artifact_id}/zip")


def _run_url(repo: str, run_id: int) -> str:
    """The link a reader follows from the summary to the reused run."""
    return f"https://github.com/{repo}/actions/runs/{run_id}"


def _summary_reuse(repo: str, verdict: Mapping[str, Any]) -> str:
    """The summary of a reuse: the run, and the five conditions compared."""
    run_id = verdict["reused_run_id"]
    compared = verdict["evidence"]["compared"]
    lines = [
        f"## Gate witness: REUSED run #{run_id}",
        "",
        f"The green conclusion of [run #{run_id}]"
        f"({_run_url(repo, run_id)}) is reused (spec #627). "
        "The five conditions, compared from the machine records:",
        "",
        "| Condition | Current | Candidate | Match |",
        "|---|---|---|---|",
    ]
    for name, field in (("same_pr", "pr"),
                        ("same_head_sha", "head_sha"),
                        ("same_main_sha", "main_sha")):
        row = compared[field]
        lines.append(f"| {name} | {row['current']} | {row['candidate']} | "
                     f"{row['match']} |")
    lines.append(f"| green_and_completed | required "
                 f"{compared['status']['required']} / "
                 f"{compared['conclusion']['required']} | observed "
                 f"{compared['status']['candidate']} / "
                 f"{compared['conclusion']['candidate']} | "
                 f"{compared['status']['match'] and compared['conclusion']['match']} |")
    lines.append(f"| same_workflow | the gate workflow | "
                 f"{compared['workflow']['candidate']} | "
                 f"{compared['workflow']['match']} |")
    lines += ["",
              "The reused run's scope verdict records are handed on in this "
              "run's artifact as `reused-scope.json` and "
              "`reused-gate-anchor.json`.", ""]
    return "\n".join(lines)


def _summary_fallback(verdict: Mapping[str, Any]) -> str:
    """The summary of a fallback: why, and what happens instead."""
    return ("\n".join([
        "## Gate witness: full rerun",
        "",
        f"No reuse: {verdict['reason']}",
        "",
        "The gate reruns in full; its verdict is this run's own.", "",
    ]))


def effective_event(settings: Mapping[str, str]) -> str:
    """The trigger name the reuse rule compares against: for ``pull_request``
    webhooks the payload's ``action`` (``ready_for_review`` is one such
    action), otherwise the event name itself.

    ``GITHUB_EVENT_NAME`` alone cannot carry the action: a ready-for-review
    run arrives with event_name ``pull_request`` and the distinguishing
    detail only in the payload file ``GITHUB_EVENT_PATH`` names.  Reading
    anything else would make the reuse question unaskable in production.
    """
    event = settings.get("GITHUB_EVENT_NAME", "")
    if event != "pull_request":
        return event
    payload_path = settings.get("GITHUB_EVENT_PATH", "")
    try:
        with open(payload_path, encoding="utf-8") as handle:
            action = json.load(handle).get("action", "")
    except (OSError, ValueError):
        return event
    return action if isinstance(action, str) and action else event


def main(argv: Sequence[str] | None = None, api: Any = None,
         env: Mapping[str, str] | None = None) -> int:
    """Answer the reuse question; never fail the gate step."""
    settings = os.environ if env is None else env
    parser = argparse.ArgumentParser(
        prog="lingdoc_gate_attest_witness",
        description="Ask whether an earlier green gate run of the same "
                    "merge ref may be reused (spec #627).")
    parser.add_argument("--artifact-dir", required=True,
                        help="the artifact directory holding this run's "
                             "anchor; reused records are written here")
    args = parser.parse_args(argv)

    repo = (settings.get("GITHUB_REPOSITORY", "").strip()
            or queue_state.default_repo())
    event = effective_event(settings)
    raw_run_id = settings.get("GITHUB_RUN_ID", "")
    run_id = int(raw_run_id) if raw_run_id.isdecimal() else None

    if api is None:
        api = GitHubApi(repo)
    verdict = decide(event, args.artifact_dir, api, run_id)
    print(json.dumps(verdict, ensure_ascii=True, indent=2))

    output_path = settings.get("GITHUB_OUTPUT")
    if output_path:
        try:
            with open(output_path, "a", encoding="utf-8") as handle:
                handle.write(f"reuse={'true' if verdict['reuse'] else 'false'}\n")
                handle.write(f"reused_run_id={verdict['reused_run_id'] or ''}\n")
        except OSError as error:
            print(f"warning: the witness outputs could not be written "
                  f"({error}); the heavy steps treat that as no reuse.",
                  file=sys.stderr)
    summary_path = settings.get("GITHUB_STEP_SUMMARY")
    if summary_path:
        section = (_summary_reuse(repo, verdict) if verdict["reuse"]
                   else _summary_fallback(verdict))
        try:
            with open(summary_path, "a", encoding="utf-8") as handle:
                handle.write(section)
        except OSError as error:
            print(f"warning: the witness summary could not be written "
                  f"({error}).", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
