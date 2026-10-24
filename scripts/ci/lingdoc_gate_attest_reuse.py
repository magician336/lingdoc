"""May this gate run reuse an earlier green run?  The witness decision.

What this file is
-----------------
A pull request that turns from Draft to Ready reruns the whole gate for a
merge ref that has already been tested green.  Spec #627 shortens exactly
that wait: when an earlier run of the **same pull request** tested **the
same head** against **the same main tip** and finished green, the gate may
end green on the strength of that run's record instead of paying the run
again.  This module is the one place that answers "may it?".

The answer rests on five conditions, all of them machine-judgeable, and on
nothing else:

1. the candidate run belongs to the same pull request (never across PRs);
2. the current head SHA equals the candidate run's head SHA;
3. the current main tip equals the main tip the candidate run recorded --
   the gate actually runs on the merge of head and main, so the same head
   alone does not mean the same tested content;
4. the candidate run's ``conclusion`` is ``success`` and its ``status`` is
   ``completed`` -- a failed, cancelled or unfinished run is never reused;
5. the candidate run was produced by this gate's workflow.

The judgement is deliberately conservative, in the same way the
queued-or-running judgement is: a record that is missing, a field that is
not there, a field of the wrong shape, anything the records do not say --
every one of those falls back to a full rerun.  Reuse is a pure gain: the
fallback direction is always "more verification", never less.

No time limit
-------------
There is deliberately no freshness window.  With the head and the main tip
both anchored, two runs verify a merge ref that is identical byte for byte;
how long ago that happened is a subjective judgement, and a subjective
judgement has no place in a machine decision.  Fields a record carries
beyond the ones named below (a ``completed_at``, say) are ignored.

How to use it
-------------
The module is a pure function: it makes no GitHub API call and touches no
file.  The caller (the attest step of the gate, wired by the follow-up
ticket) fetches the current observation and the candidate run's anchor
record and hands them in::

    decision = lingdoc_gate_attest_reuse.decide_reuse(current, candidate)

``current`` is the observation of the pull request asking to reuse, a
mapping with ``pr`` (an int), ``head_sha`` and ``main_sha`` (non-empty
strings).  ``candidate`` is the anchor record of one earlier gate run, the
shape spec #627 freezes -- ``head_sha``, ``main_sha``, ``run_id``,
``conclusion`` -- extended with the fields the five conditions read:
``pr``, ``status`` and the source ``workflow`` identifier.  A field the
record does not carry is a witness failure, not an assumption.

The answer is one mapping.  ``reuse`` is the decision; ``reason`` says why
in one sentence; ``evidence`` carries the reused run's id and the
side-by-side comparison value of every condition, so an auditor can recheck
the decision field by field.  ``reused_run_id`` is the id of the run whose
conclusion is reused, and ``None`` when nothing is reused.

The queue-state shape guarantee
-------------------------------
queue-state (issue #550) judges a check-run by its record alone; it cannot
and must not know whether the check came from a full run or from a reuse.
The guarantee this module keeps is therefore about the machine view only:

* ``reuse == True`` implies ``verdict == "passed"``, ``passed == True``,
  ``missing == False``, and ``observed_status`` / ``observed_conclusion``
  are the candidate run's own ``completed`` / ``success`` -- exactly the
  shape a full run's green conclusion publishes;
* ``reuse == False`` implies ``verdict == "missing"``, ``passed == False``:
  the one queue-state verdict that means "not established -- treat the head
  as not green".  On the reuse path the check itself has produced no
  observation yet; the full rerun that follows will publish the real one.
  A fallback never reads as green and never reads as a gate failure.

What this file is not
---------------------
This module fetches nothing and writes nothing.  Reading the run list, the
artifacts and the anchor record, and publishing the verdict into the
workflow's outputs, are the caller's work; keeping them here would make the
judgement untestable without the network.
"""
from __future__ import annotations

from typing import Any, Mapping

import lingdoc_gate_queue_state as queue_state
import lingdoc_gate_surfaces as manifest

#: The workflow that publishes the gate, by name and by path.  Either form
#: identifies the gate in a candidate record; both are exact spellings, so
#: the comparison stays machine-judgeable either way.  The path is the
#: manifest's knowledge -- it registers the gate workflow as a governance
#: file -- so it is read from there, never written down a second time.
GATE_WORKFLOW_NAME = queue_state.GATE_WORKFLOW_NAME
GATE_WORKFLOW_FILE = "lingdoc-review.yml"
GATE_WORKFLOW_PATH = next(path for path in manifest.GOVERNANCE_FILES
                          if path.endswith("/" + GATE_WORKFLOW_FILE))
GATE_WORKFLOW_IDENTIFIERS = (GATE_WORKFLOW_NAME, GATE_WORKFLOW_PATH)

#: What condition 4 demands, spelled once.
REQUIRED_CONCLUSION = "success"
REQUIRED_STATUS = "completed"

#: The five conditions, in the order spec #627 states them.  The order is
#: also the order the judgement reports: the first condition that does not
#: hold names itself in the reason.
CONDITION_NAMES = (
    "same_pr",
    "same_head_sha",
    "same_main_sha",
    "green_and_completed",
    "same_workflow",
)

#: The fields the current observation must carry, with the shape each must
#: have: an int that is not a bool, or a non-empty string.
CURRENT_FIELDS = {
    "pr": int,
    "head_sha": str,
    "main_sha": str,
}

#: The fields the candidate's anchor record must carry.  The first four are
#: the shape spec #627 freezes; ``pr``, ``status`` and ``workflow`` are what
#: the five conditions read.
RECORD_FIELDS = {
    "pr": int,
    "head_sha": str,
    "main_sha": str,
    "run_id": int,
    "conclusion": str,
    "status": str,
    "workflow": str,
}


def _read_field(record: Any, name: str, kind: type) -> tuple[Any, str | None]:
    """The value of one field, or the sentence saying why it cannot be read.

    A field that is not there is a missing record; a field of the wrong
    shape is a malformed record.  Both are witness failures, and both fall
    back -- the distinction only makes the reason say which one it was.
    """
    if not isinstance(record, Mapping):
        return None, "no usable record was provided"
    if name not in record:
        return None, f"the record carries no {name}"
    value = record[name]
    if kind is int:
        if isinstance(value, bool) or not isinstance(value, int):
            return None, f"the record's {name} is malformed: {value!r}"
        if name == "run_id" and value <= 0:
            return None, f"the record's {name} is malformed: {value!r}"
        return value, None
    if not isinstance(value, str) or not value:
        return None, f"the record's {name} is malformed: {value!r}"
    return value, None


def _read_all(record: Any, fields: Mapping[str, type]) -> tuple[dict[str, Any], str | None]:
    """Every field of a record, or the first problem found."""
    values: dict[str, Any] = {}
    for name, kind in fields.items():
        value, problem = _read_field(record, name, kind)
        if problem is not None:
            return {}, problem
        values[name] = value
    return values, None


def _empty_evidence() -> dict[str, Any]:
    """The evidence shape every outcome carries, before any value fills it.

    The shape is fixed up front so that an auditor comparing two decisions
    -- one reused, one fallen back -- finds the same fields in both.
    """
    return {
        "run_id": None,
        "compared": {
            "pr": {"current": None, "candidate": None, "match": None},
            "head_sha": {"current": None, "candidate": None, "match": None},
            "main_sha": {"current": None, "candidate": None, "match": None},
            "status": {"candidate": None, "required": REQUIRED_STATUS,
                       "match": None},
            "conclusion": {"candidate": None, "required": REQUIRED_CONCLUSION,
                           "match": None},
            "workflow": {"candidate": None,
                         "gate": list(GATE_WORKFLOW_IDENTIFIERS),
                         "match": None},
        },
        "conditions": {name: None for name in CONDITION_NAMES},
    }


def _verdict(reuse: bool, reason: str, evidence: dict[str, Any],
             observed_status: Any, observed_conclusion: Any) -> dict[str, Any]:
    """One judgement, in the shape every reader of this module expects.

    The shape carries the queue-state green keys beside ``reuse`` so that a
    reader applying queue-state's rule -- a verdict that could not be
    established must not be read as green -- needs to know nothing about
    which path produced the check.
    """
    return {
        "reuse": reuse,
        "verdict": (queue_state.VERDICT_PASSED if reuse
                    else queue_state.VERDICT_MISSING),
        "queued": False,
        "running": False,
        "passed": reuse,
        "failed": False,
        "missing": not reuse,
        "observed_status": observed_status,
        "observed_conclusion": observed_conclusion,
        "reason": reason,
        "reused_run_id": evidence["run_id"] if reuse else None,
        "evidence": evidence,
    }


def _reused(values: dict[str, Any], evidence: dict[str, Any]) -> dict[str, Any]:
    """The green answer: the conditions held, the run's verdict is reused."""
    run_id = values["run_id"]
    reason = (f"the gate run {run_id} of pull request {values['pr']} "
              "finished successfully on the same head and the same main "
              "tip; its green conclusion is reused")
    return _verdict(True, reason, evidence,
                    values["status"], values["conclusion"])


def decide_reuse(current: Any, candidate: Any) -> dict[str, Any]:
    """Whether the gate may reuse an earlier green run, and why.

    ``current`` is the observation of the pull request asking to reuse;
    ``candidate`` is the anchor record of one earlier gate run.  Both are
    plain mappings; the judgement reads them and nothing else.  Every
    failure to read them -- a missing record, a missing field, a malformed
    field -- is a fallback, and a fallback never reads as green.
    """
    evidence = _empty_evidence()

    current_values, problem = _read_all(current, CURRENT_FIELDS)
    if problem is not None:
        return _verdict(False,
                        f"the current observation cannot be judged: {problem}; "
                        "nothing is reused", evidence, None, None)

    candidate_values, problem = _read_all(candidate, RECORD_FIELDS)
    if problem is not None:
        return _verdict(False,
                        f"the candidate run cannot be judged: {problem}; "
                        "nothing is reused", evidence, None, None)

    evidence["run_id"] = candidate_values["run_id"]
    compared = evidence["compared"]

    # Condition 1: the same pull request.
    compared["pr"].update(current=current_values["pr"],
                          candidate=candidate_values["pr"],
                          match=current_values["pr"] == candidate_values["pr"])
    evidence["conditions"]["same_pr"] = compared["pr"]["match"]
    if not compared["pr"]["match"]:
        return _verdict(False,
                        f"the candidate run belongs to pull request "
                        f"{candidate_values['pr']}, not to pull request "
                        f"{current_values['pr']}; nothing is reused",
                        evidence, candidate_values["status"],
                        candidate_values["conclusion"])

    # Condition 2: the same head SHA.
    compared["head_sha"].update(current=current_values["head_sha"],
                                candidate=candidate_values["head_sha"],
                                match=(current_values["head_sha"]
                                       == candidate_values["head_sha"]))
    evidence["conditions"]["same_head_sha"] = compared["head_sha"]["match"]
    if not compared["head_sha"]["match"]:
        return _verdict(False,
                        f"the candidate run tested head "
                        f"{candidate_values['head_sha']}, not the current "
                        f"head {current_values['head_sha']}; nothing is "
                        "reused", evidence, candidate_values["status"],
                        candidate_values["conclusion"])

    # Condition 3: the same main tip -- the merge ref is anchored by both.
    compared["main_sha"].update(current=current_values["main_sha"],
                                candidate=candidate_values["main_sha"],
                                match=(current_values["main_sha"]
                                       == candidate_values["main_sha"]))
    evidence["conditions"]["same_main_sha"] = compared["main_sha"]["match"]
    if not compared["main_sha"]["match"]:
        return _verdict(False,
                        f"the candidate run recorded main tip "
                        f"{candidate_values['main_sha']}, not the current "
                        f"main tip {current_values['main_sha']}; the merge "
                        "ref is not the same, so nothing is reused",
                        evidence, candidate_values["status"],
                        candidate_values["conclusion"])

    # Condition 4: green, and finished.
    green = (candidate_values["status"] == REQUIRED_STATUS
             and candidate_values["conclusion"] == REQUIRED_CONCLUSION)
    compared["status"].update(candidate=candidate_values["status"],
                              match=(candidate_values["status"]
                                     == REQUIRED_STATUS))
    compared["conclusion"].update(candidate=candidate_values["conclusion"],
                                  match=(candidate_values["conclusion"]
                                         == REQUIRED_CONCLUSION))
    evidence["conditions"]["green_and_completed"] = green
    if candidate_values["status"] != REQUIRED_STATUS:
        return _verdict(False,
                        f"the candidate run has not completed (status "
                        f"{candidate_values['status']!r}); nothing is reused",
                        evidence, candidate_values["status"],
                        candidate_values["conclusion"])
    if candidate_values["conclusion"] != REQUIRED_CONCLUSION:
        return _verdict(False,
                        f"the candidate run finished with conclusion "
                        f"{candidate_values['conclusion']!r}, not success; "
                        "nothing is reused", evidence,
                        candidate_values["status"],
                        candidate_values["conclusion"])

    # Condition 5: produced by this gate's workflow.
    same_workflow = (candidate_values["workflow"]
                     in GATE_WORKFLOW_IDENTIFIERS)
    compared["workflow"].update(candidate=candidate_values["workflow"],
                                match=same_workflow)
    evidence["conditions"]["same_workflow"] = same_workflow
    if not same_workflow:
        return _verdict(False,
                        f"the candidate run was produced by workflow "
                        f"{candidate_values['workflow']!r}, not by the gate "
                        "workflow; nothing is reused", evidence,
                        candidate_values["status"],
                        candidate_values["conclusion"])

    return _reused(candidate_values, evidence)
