"""G6 canonical main-path evidence wrapper around the existing F01 runner.

The wrapper does not create a second workflow. It derives coverage from the committed F01
workflow and requires a separate failure observation before declaring the complete G6 fixture
ready. That keeps a successful HTTP trace from masquerading as full failure-side-effect evidence.

What the verdict is made of
---------------------------
Four things have to hold before this path may say PASS:

* the runner finished (``runner_status`` is ``completed``);
* every node answered with the HTTP status the **contract** declares.  The
  contract is read from disk at verdict time rather than from a copy frozen
  at import time, so a contract that moved on cannot be judged against the
  old expectations -- and where the two disagree, the disagreement is named
  instead of being followed silently;
* every **machine** assertion the contract's own nodes carry **held**
  (``checks``);
* every **prose** assertion the contract's own nodes carry was judged by
  somebody (``manual_assertions``), and the runner's own verdict on the
  provider's semantics is ``verified`` (``provider_semantics_status``).

The third one used to be missing.  This path read the HTTP status only, so a
node whose every assertion had failed was still reported as a node that had
passed, and the evidence said the HTTP matched the contract.  A node that
answered as the contract says but whose own assertion did not hold has not
passed, and the report now says which of the two happened.

The fourth one used to be missing too, and it was the more expensive one.
The contract's nodes carry two kinds of assertion: the machine ones above,
and the prose ones no machine can read -- a human judges those.  This path
read neither the prose assertions nor the runner's verdict on the provider's
semantics, so a run whose prose assertions nobody had judged was reported
as a pass.  The main path (``scripts/acceptance/beta_evidence.py``) counts
the very same run as NOT RUN, so one run had two verdicts and the release
decision read the optimistic one.  An unjudged prose assertion is neither a
failure nor a pass: it is NOT RUN, and that is what this path says now.
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
import sys
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.quality_evidence import RUNTIME_MODES, SAFE_FIXTURE_ID, build_evidence, digest
from scripts.lingdoc_mock.run_f01 import OPENAPI_PATH, WORKFLOW_PATH, ScenarioRunner, WorkflowError, write_report

REQUIRED_PARTS = ("project", "asset", "chapter", "check", "release", "export", "download")
#: The contract this path judges against.  It is the only place that says
#: what a node must answer, so it is read from disk every time a verdict is
#: made -- see ``_contract_expectations``.
CONTRACT_PATH = WORKFLOW_PATH
OPERATION_PARTS = {
    "createProject": ("project",),
    "bindAsset": ("asset",),
    "saveChapter": ("chapter",),
    "confirmChapter": ("chapter",),
    "prepareRelease": ("check", "release"),
    "startExport": ("export",),
    "getExport": ("export",),
    "downloadExport": ("download",),
}
REDACTED_VALUE = re.compile(r"(?:[a-z][a-z0-9_.-]*\.[a-z0-9_.-]+|sha256:[0-9a-f]{64})")
SAFE_STATUS = {"unchanged", "not_recorded", "observed", "failed", "blocked", "ok", "unknown", "active", "passed"}
FIXTURE_PARTS = ("project", "asset", "chapter", "check", "release", "export", "download")
SAFE_WORKFLOWS = {"F01"}
SAFE_RUNNER_STATUSES = {"completed", "failed", "not_run", "blocked"}
SAFE_VERIFICATION_SCOPES = {"contract_fixture_only", "http_smoke_only", "observed", "not_recorded"}
SAFE_METADATA_STATUSES = {"mock", "not_run", "unknown", "observed", "not_observed"}
# The trace's step ids are F01-01..F01-23 plus F01-03b (the activation read)
# and the four model-use permit nodes F01-06b..F01-06e: those were inserted
# in the middle so the existing ids did not have to be renumbered (they are
# quoted in historical evidence files).
SAFE_STEP_ID = re.compile(r"F01-[0-9]{2}[a-z]?")
SAFE_FAILURE_CASE = re.compile(r"[a-z][a-z0-9_.-]{1,63}")
#: The shape of the two fields a failed check may publish.  Both come from
#: the report, so both are checked before they are written into the evidence.
SAFE_OPERATOR = re.compile(r"[a-z_][a-z0-9_]{0,63}")
SAFE_POINTER = re.compile(r"(?:/[A-Za-z0-9_.~%-]{0,64}){0,32}")
#: The statuses a prose assertion may carry once somebody judged it.  The
#: main path reads the same set (``beta_evidence.manual_assertion_outcomes``);
#: any other status means nobody judged it.
JUDGED_MANUAL_ASSERTION_STATUSES = {"passed", "held", "judged"}
#: The only value of ``provider_semantics_status`` that may lead to a PASS.
#: The main path uses the same one (``beta_evidence.PROVIDER_SEMANTICS_VERIFIED``).
PROVIDER_SEMANTICS_VERIFIED = "verified"


def _contract_expectations() -> dict[str, Any] | None:
    """The node ids and the HTTP statuses the contract declares, read now.

    The contract is the only place that says what a node must answer, so it
    is read every time a verdict is made rather than frozen at import time.
    A frozen copy is a second truth, and a second truth goes stale the moment
    the contract gains a node.

    ``None`` means the contract could not be read, or does not carry the shape
    this module needs.  A verdict without the contract is not a verdict, so
    the caller says BLOCKED instead of guessing.
    """
    try:
        document = json.loads(WORKFLOW_PATH.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError):
        return None
    steps = document.get("steps") if isinstance(document, dict) else None
    if not isinstance(steps, list):
        return None
    step_ids: list[str] = []
    expected_http: dict[str, int] = {}
    for step in steps:
        if not isinstance(step, dict):
            return None
        step_id = step.get("id")
        status = step.get("expected_http")
        if not isinstance(step_id, str) or not step_id:
            return None
        if not isinstance(status, int) or isinstance(status, bool):
            return None
        step_ids.append(step_id)
        expected_http[step_id] = status
    return {"step_ids": tuple(step_ids), "expected_http": expected_http}


#: What this module was loaded with.  It is kept so the difference against the
#: contract on disk can be named -- see ``_contract_mismatch``.
_CONTRACT_AT_IMPORT = _contract_expectations()
REQUIRED_STEP_IDS = tuple(_CONTRACT_AT_IMPORT["step_ids"]) if _CONTRACT_AT_IMPORT else ()
EXPECTED_STEP_HTTP = dict(_CONTRACT_AT_IMPORT["expected_http"]) if _CONTRACT_AT_IMPORT else {}


def _contract_mismatch(current: dict[str, Any] | None) -> list[str]:
    """Where the contract on disk and this module's declared expectations differ.

    ``EXPECTED_STEP_HTTP`` says what this module was loaded with.  When the
    file has moved on, the difference is named instead of being followed
    silently: a verdict made against expectations nobody can name is not
    evidence, so the caller refuses to say PASS.
    """
    if current is None:
        return [f"契约读不到：{CONTRACT_PATH}"]
    if not EXPECTED_STEP_HTTP and not REQUIRED_STEP_IDS:
        return ["本模块载入时没有读到契约，声明的期望值是空的"]
    differences: list[str] = []
    for step_id in sorted(set(current["expected_http"]) | set(EXPECTED_STEP_HTTP)):
        declared = current["expected_http"].get(step_id)
        loaded = EXPECTED_STEP_HTTP.get(step_id)
        if declared != loaded:
            differences.append(f"{step_id}：契约声明 {declared}，本模块声明 {loaded}")
    if tuple(current["step_ids"]) != tuple(REQUIRED_STEP_IDS):
        differences.append("节点清单与本模块声明的不一致")
    return differences


def _step_id(step: dict[str, Any]) -> str | None:
    value = step.get("id", step.get("step_id"))
    return value if isinstance(value, str) and (value in EXPECTED_STEP_HTTP or SAFE_STEP_ID.fullmatch(value)) else None


def _step_http(step: dict[str, Any]) -> int | None:
    value = step.get("http_status", step.get("actual_http"))
    return value if isinstance(value, int) and not isinstance(value, bool) and 100 <= value <= 599 else None


def _step_verdict(step: dict[str, Any], expected_http: dict[str, int],
                  failed_checks: dict[str, list[dict[str, Any]]] | None = None) -> str:
    """What this node's own verdict is.

    Two different things can make a node fail, and they have to stay
    different: the status it answered with is not the one the contract
    declares, or one of the assertions the node itself carries did not
    hold.  The second one used to be invisible here, which is how a node
    whose every assertion had failed was reported as a node that had passed.

    A verdict the report states about itself does not overrule the contract:
    the contract's own assertion is the fact, the report's word is a claim.
    """
    failed_checks = failed_checks or {}
    verdict = step.get("verdict")
    step_id = _step_id(step)
    status = _step_http(step)
    if step_id is not None and step_id in failed_checks:
        return "failed"
    if (verdict == "passed" and step_id is not None and status is not None
            and expected_http.get(step_id) != status):
        return "failed"
    if verdict in {"passed", "failed", "not_run"}:
        return verdict
    if step_id is not None and status is not None:
        return "passed" if expected_http.get(step_id) == status else "failed"
    return "unknown"


def _failed_checks(report: dict[str, Any]) -> dict[str, list[dict[str, Any]]]:
    """The checks of this run that did not hold, grouped by the node they belong to.

    A check is the node's own assertion: "it answered 200" is not the same
    fact as "it answered 200 with the state the contract names".  Only the
    shape is kept -- the operator and the pointer the contract declared --
    so that a report cannot smuggle a payload into the evidence.
    """
    checks = report.get("checks", [])
    if not isinstance(checks, list):
        return {}
    grouped: dict[str, list[dict[str, Any]]] = {}
    for check in checks:
        if not isinstance(check, dict):
            continue
        if check.get("status") == "passed":
            continue
        step_id = check.get("step_id")
        key = step_id if isinstance(step_id, str) and SAFE_STEP_ID.fullmatch(step_id) else ""
        operator = check.get("operator")
        pointer = check.get("path")
        grouped.setdefault(key, []).append({
            "step_id": key,
            "operator": (operator if isinstance(operator, str)
                         and SAFE_OPERATOR.fullmatch(operator) else "unknown"),
            "path": pointer if isinstance(pointer, str) and SAFE_POINTER.fullmatch(pointer) else "",
            "status": "failed",
        })
    return grouped


def _unjudged_manual_assertions(report: dict[str, Any]) -> list[dict[str, str]]:
    """The prose assertions of this run that nobody judged.

    A prose assertion is one no machine can read: the contract leaves it to a
    human.  Until a human judges it, the run has not verified it -- and a run
    that does not say so looks like a run that did.  The main path counts the
    same entries as NOT RUN, so this path counts them the same way: one run
    may not have two verdicts.

    Only the node the assertion belongs to is published.  The assertion text
    itself is not carried into the evidence.
    """
    entries = report.get("manual_assertions", [])
    if not isinstance(entries, list):
        return []
    unjudged: list[dict[str, str]] = []
    for entry in entries:
        if not isinstance(entry, dict):
            continue
        if str(entry.get("status", "")).lower() in JUDGED_MANUAL_ASSERTION_STATUSES:
            continue
        step_id = entry.get("step_id")
        unjudged.append({
            "step_id": step_id if isinstance(step_id, str) and SAFE_STEP_ID.fullmatch(step_id) else "",
            "status": "not_run",
        })
    return unjudged


def _provider_semantics_status(report: dict[str, Any]) -> str:
    """The runner's own verdict on the provider's semantics.

    ``not_verified`` when the report does not carry the verdict at all: an
    unknown verdict is not a verified one, and the main path reads a missing
    verdict the same way.
    """
    return str(report.get("provider_semantics_status", "not_verified") or "not_verified")


def _safe_fixture_state(value: Any) -> dict[str, Any]:
    """Keep only redacted, status-shaped fixture state in the quality report."""
    if not isinstance(value, dict):
        return {}
    safe: dict[str, Any] = {}
    for part in FIXTURE_PARTS:
        entry = value.get(part)
        if not isinstance(entry, dict):
            continue
        safe_entry: dict[str, Any] = {}
        for key, item in sorted(entry.items()):
            if key in {"id", "version", "snapshot"} and isinstance(item, str) and REDACTED_VALUE.fullmatch(item):
                safe_entry[key] = item
            elif key in {"status", "permission"} and isinstance(item, str) and item in SAFE_STATUS | {"allow", "deny", "pending", "ready"}:
                safe_entry[key] = item
            elif key in {"reauthorized", "unchanged"} and isinstance(item, bool):
                safe_entry[key] = item
        if safe_entry:
            safe[part] = safe_entry
    return safe


def _safe_metadata(value: Any) -> dict[str, Any]:
    """Keep version and permission metadata to aliases/statuses before evidence serialization."""
    if not isinstance(value, dict):
        return {}
    safe: dict[str, Any] = {}
    for key in ("template_version", "ruleset_hash"):
        item = value.get(key)
        if isinstance(item, str) and REDACTED_VALUE.fullmatch(item):
            safe[key] = item
    permission = value.get("permission_snapshot")
    if isinstance(permission, dict):
        safe_permission = {}
        for key, item in sorted(permission.items()):
            if key == "status" and isinstance(item, str) and item in SAFE_METADATA_STATUSES:
                safe_permission[key] = item
            elif key in {"project", "actor"} and isinstance(item, str) and REDACTED_VALUE.fullmatch(item):
                safe_permission[key] = item
        if safe_permission:
            safe["permission_snapshot"] = safe_permission
    for field in ("dependency_versions", "environment_versions", "object_versions"):
        values = value.get(field)
        if not isinstance(values, dict):
            continue
        safe_values = {}
        for key, item in sorted(values.items()):
            if isinstance(item, str) and (item in SAFE_METADATA_STATUSES or REDACTED_VALUE.fullmatch(item)):
                safe_values[key] = item
        if safe_values:
            safe[field] = safe_values
    return safe


def _step_reason(step: dict[str, Any], expected_http: dict[str, int],
                 failed_checks: dict[str, list[dict[str, Any]]]) -> str:
    """Which of the two failures this node ran into.

    "the HTTP status is not the one the contract declares" and "the HTTP
    status is right but the node's own assertion did not hold" are two
    different facts, and a reader of the evidence has to be able to tell them
    apart.
    """
    step_id = _step_id(step)
    status = _step_http(step)
    if step_id is not None and status is not None and expected_http.get(step_id) != status:
        return "HTTP 与契约声明不一致"
    if step_id is not None and step_id in failed_checks:
        return "HTTP 符合契约，但节点自己的断言不成立"
    return "没有跑出结论"


def _safe_main_report(report: dict[str, Any], expected_http: dict[str, int],
                      failed_checks: dict[str, list[dict[str, Any]]] | None = None) -> dict[str, Any]:
    """Serialize only outcome fields from an F01 report into G6 evidence."""
    failed_checks = failed_checks or {}
    steps = []
    source_steps = report.get("steps", [])
    if not isinstance(source_steps, list):
        source_steps = []
    for step in source_steps:
        if not isinstance(step, dict):
            continue
        operation_id = step.get("operation_id")
        step_id = _step_id(step)
        verdict = _step_verdict(step, expected_http, failed_checks)
        item = {
            "operation_id": operation_id if operation_id in OPERATION_PARTS else "unknown",
            "verdict": verdict if verdict in {"passed", "failed", "not_run"} else "unknown",
        }
        if step_id is not None:
            item["step_id"] = step_id
        status = _step_http(step)
        if status is not None:
            item["actual_http"] = status
        if item["verdict"] != "passed":
            item["reason"] = _step_reason(step, expected_http, failed_checks)
        steps.append(item)
    completed_steps = report.get("completed_steps", 0)
    total_steps = report.get("total_steps", 0)
    return {
        "workflow": report.get("workflow") if report.get("workflow") in SAFE_WORKFLOWS else "unknown",
        "runner_status": report.get("runner_status") if report.get("runner_status") in SAFE_RUNNER_STATUSES else "unknown",
        "completed_steps": completed_steps if isinstance(completed_steps, int)
        and not isinstance(completed_steps, bool) and completed_steps >= 0 else 0,
        "total_steps": total_steps if isinstance(total_steps, int)
        and not isinstance(total_steps, bool) and total_steps >= 0 else 0,
        "verification_scope": report.get("verification_scope") if report.get("verification_scope") in SAFE_VERIFICATION_SCOPES else "not_recorded",
        "steps": steps,
        "fixture_state": _safe_fixture_state(report.get("fixture_state")),
    }


def _observed_analysis(report: dict[str, Any]) -> dict[str, Any]:
    """What this run observed, and what it therefore says.

    Five different things can happen, and the report has to say which one:

    * the runner did not finish -- BLOCKED;
    * the contract and this module's declared expectations disagree --
      BLOCKED, because a verdict made against expectations nobody can name is
      not evidence;
    * a node answered with a status the contract does not declare, **or** a
      node answered as the contract says but one of its own machine
      assertions did not hold -- FAIL.  The second one used to be PASS: this
      path read the HTTP status only, so a node whose every assertion had
      failed was reported as a node that had passed;
    * a prose assertion nobody judged, or a provider whose semantics this run
      did not verify -- NOT RUN.  Nothing failed, but something was not judged
      either, and the main path counts the same run as NOT RUN;
    * nothing failed and nothing was left unjudged -- PASS.

    The two FAIL causes stay apart in the evidence: "the HTTP status is wrong"
    and "the HTTP status is right but the assertion did not hold" are two
    different facts.  The NOT RUN cause says which prose assertions nobody
    judged and what the runner's verdict on the provider's semantics was.
    """
    contract = _contract_expectations()
    unjudged = _unjudged_manual_assertions(report)
    semantics = _provider_semantics_status(report)
    analysis: dict[str, Any] = {
        "result": "BLOCKED",
        "reason": "",
        "http_mismatches": [],
        "failed_checks": [],
        "unjudged_manual_assertions": unjudged,
        "provider_semantics_status": semantics,
        "contract_mismatch": _contract_mismatch(contract),
    }
    if contract is None:
        analysis["reason"] = f"契约读不到（{CONTRACT_PATH}），判定没有依据。"
        return analysis
    if analysis["contract_mismatch"]:
        analysis["reason"] = ("契约与本模块声明的期望值不一致，判定没有依据："
                              + "；".join(analysis["contract_mismatch"]))
        return analysis
    expected_http = contract["expected_http"]
    if report.get("runner_status") != "completed":
        analysis["reason"] = f"runner_status={report.get('runner_status')!r}：主路径没有跑完。"
        return analysis
    steps = report.get("steps", [])
    if not isinstance(steps, list):
        analysis["reason"] = "报告里的 steps 不是列表，判定没有依据。"
        return analysis
    failed = _failed_checks(report)
    # The HTTP mismatches of the nodes this run answered, read from the nodes
    # themselves: a node that answered with a status the contract does not
    # declare is a mismatch whether or not the runner wrote it down.
    for step in steps:
        if not isinstance(step, dict):
            continue
        step_id = _step_id(step)
        status = _step_http(step)
        if step_id is None or status is None:
            continue
        declared = expected_http.get(step_id)
        if declared is not None and declared != status:
            analysis["http_mismatches"].append({
                "step_id": step_id, "expected": declared, "actual": status})
    # The mismatches the runner recorded for the nodes that did **not** answer
    # as the contract says: those nodes are absent from ``steps``.
    for failure in report.get("failures", []) or []:
        if not isinstance(failure, dict) or failure.get("kind") != "http_status":
            continue
        step_id = failure.get("step_id")
        if not isinstance(step_id, str) or not SAFE_STEP_ID.fullmatch(step_id):
            continue
        if any(item["step_id"] == step_id for item in analysis["http_mismatches"]):
            continue
        expected = failure.get("expected")
        actual = failure.get("actual")
        analysis["http_mismatches"].append({
            "step_id": step_id,
            "expected": expected if isinstance(expected, int) and not isinstance(expected, bool) else None,
            "actual": actual if isinstance(actual, int) and not isinstance(actual, bool) else None,
        })
    for group in failed.values():
        analysis["failed_checks"].extend(group)
    if any(_step_id(step) is not None and _step_http(step) is None
           for step in steps if isinstance(step, dict)):
        analysis["reason"] = "有节点没有记录 HTTP 状态码，判定没有依据。"
        return analysis
    verdicts = [_step_verdict(step, expected_http, failed)
                for step in steps if isinstance(step, dict)]
    if len(verdicts) != len(steps):
        analysis["reason"] = "有节点不是对象，判定没有依据。"
        return analysis
    if analysis["http_mismatches"] or analysis["failed_checks"]:
        analysis["result"] = "FAIL"
        reasons: list[str] = []
        if analysis["http_mismatches"]:
            reasons.append("HTTP 与契约声明不一致："
                           + "；".join(f"{item['step_id']} 契约声明 {item['expected']}，实际 {item['actual']}"
                                       for item in analysis["http_mismatches"]))
        if analysis["failed_checks"]:
            prefix = "" if analysis["http_mismatches"] else "HTTP 符合契约，但"
            reasons.append(prefix + "节点自己的断言不成立："
                           + "；".join(f"{item['step_id']} {item['operator']} {item['path']}"
                                       for item in analysis["failed_checks"]))
        analysis["reason"] = "；".join(reasons)
        return analysis
    if any(verdict == "failed" for verdict in verdicts):
        analysis["result"] = "FAIL"
        analysis["reason"] = "有节点的判定不是通过，详见 main_steps。"
        return analysis
    if unjudged or semantics != PROVIDER_SEMANTICS_VERIFIED:
        analysis["result"] = "NOT RUN"
        reasons: list[str] = []
        if unjudged:
            reasons.append("还有人工/散文断言未判定："
                           + "；".join(item["step_id"] or "（未标注节点）"
                                       for item in unjudged))
        if semantics != PROVIDER_SEMANTICS_VERIFIED:
            reasons.append(f"provider_semantics_status = {semantics}："
                           "提供方的语义本轮没有核验。")
        analysis["reason"] = "；".join(reasons)
        return analysis
    if verdicts and all(verdict == "passed" for verdict in verdicts):
        analysis["result"] = "PASS"
        analysis["reason"] = ("全部节点的 HTTP 与契约声明一致，机器断言全部成立，"
                              "散文断言都有人判定，提供方的语义已核验。")
        return analysis
    analysis["reason"] = "没有跑出结论。"
    return analysis


def _observed_result(report: dict[str, Any]) -> str:
    """The verdict of this path alone: PASS, FAIL, NOT RUN or BLOCKED."""
    return _observed_analysis(report)["result"]


def _coverage(report: dict[str, Any], expected_step_ids: list[str] | None = None) -> dict[str, Any]:
    """Which parts of the declared sequence this run covered.

    The expected sequence is the contract's own: it is read from the contract
    rather than from a copy frozen at import time, so a contract that moved on
    is measured against itself.
    """
    steps = report.get("steps", [])
    if not isinstance(steps, list):
        steps = []
    observed_step_ids = [_step_id(step) for step in steps if isinstance(step, dict)]
    expected_step_ids = list(expected_step_ids if expected_step_ids is not None else REQUIRED_STEP_IDS)
    missing_step_ids = sorted(set(expected_step_ids) - set(observed_step_ids))
    unexpected_step_ids = sorted({step_id for step_id in observed_step_ids if step_id is not None}
                                 - set(expected_step_ids))
    step_sequence_complete = observed_step_ids == expected_step_ids
    completed_steps = report.get("completed_steps")
    total_steps = report.get("total_steps")
    step_counts_complete = (
        len(steps) == len(expected_step_ids)
        and isinstance(completed_steps, int) and not isinstance(completed_steps, bool)
        and isinstance(total_steps, int) and not isinstance(total_steps, bool)
        and completed_steps == total_steps == len(expected_step_ids)
    )
    operations = {step.get("operation_id") for step in steps if isinstance(step, dict)}
    constructed = sorted({part for operation, parts in OPERATION_PARTS.items() if operation in operations
                          for part in parts})
    missing = sorted(set(REQUIRED_PARTS) - set(constructed))
    return {
        "required_parts": list(REQUIRED_PARTS),
        "constructed_parts": constructed,
        "constructed_complete": not missing,
        "missing_parts": missing,
        "step_sequence_complete": step_sequence_complete,
        "step_counts_complete": step_counts_complete,
        "missing_step_ids": missing_step_ids,
        "unexpected_step_ids": unexpected_step_ids,
    }


def _failure_path(observation: dict[str, Any] | None) -> dict[str, Any]:
    if observation is None:
        return {
            "result": "BLOCKED",
            "status": "not_run",
            "no_formal_side_effect": None,
            "evidence_refs": ["failure_path"],
        }
    if not isinstance(observation, dict):
        raise ValueError("failure observation must be an object")
    required = ("failure_case", "expected_http", "actual_http", "no_formal_side_effect", "readback")
    missing = [field for field in required if field not in observation]
    failure_case = observation.get("failure_case")
    safe_failure_case = (failure_case if isinstance(failure_case, str)
                         and SAFE_FAILURE_CASE.fullmatch(failure_case) else None)
    if missing:
        no_side_effect = (observation.get("no_formal_side_effect")
                          if isinstance(observation.get("no_formal_side_effect"), bool)
                          else None)
        return {
            "result": "BLOCKED",
            "status": "incomplete",
            "failure_case": safe_failure_case,
            "no_formal_side_effect": no_side_effect,
            "missing_fields": missing,
            "evidence_refs": ["failure_path", "side_effect_readback"],
        }
    expected_http = observation.get("expected_http")
    actual_http = observation.get("actual_http")
    if (not isinstance(expected_http, int) or isinstance(expected_http, bool)
            or not isinstance(actual_http, int) or isinstance(actual_http, bool)
            or expected_http != actual_http or not 400 <= expected_http <= 599):
        return {
            "result": "FAIL",
            "status": "observed",
            "failure_case": safe_failure_case,
            "no_formal_side_effect": observation.get("no_formal_side_effect") is True,
            "expected_http": expected_http,
            "actual_http": actual_http,
            "evidence_refs": ["failure_path"],
        }
    no_side_effect = observation.get("no_formal_side_effect") is True
    readback = observation.get("readback")
    # Keep only status-shaped read-back fields.  A failure fixture must never turn
    # the main-path report into a transport for document text or provider payloads.
    if isinstance(readback, dict):
        safe_readback = {}
        for key, value in sorted(readback.items()):
            if key in {"status", "state"} and isinstance(value, str) and value in SAFE_STATUS:
                safe_readback[key] = value
            elif key in {"revision", "version"} and isinstance(value, str) and REDACTED_VALUE.fullmatch(value):
                safe_readback[key] = value
            elif key == "unchanged" and isinstance(value, bool):
                safe_readback[key] = value
        readback = safe_readback
    elif isinstance(readback, str) and readback in SAFE_STATUS:
        # Accept the compact status-only shape emitted by some F01 adapters, but
        # never turn an arbitrary string or payload into an observed read-back.
        readback = {"status": readback}
    else:
        readback = {}
    if safe_failure_case is None:
        return {
            "result": "FAIL", "status": "observed", "failure_case": None,
            "no_formal_side_effect": no_side_effect,
            "expected_http": expected_http, "actual_http": actual_http,
            "evidence_refs": ["failure_path"],
        }
    readback_status = "not_recorded"
    if isinstance(readback, dict):
        for field in ("status", "state"):
            if readback.get(field) in SAFE_STATUS:
                readback_status = readback[field]
                break
    if not no_side_effect or readback_status != "unchanged":
        result = "FAIL"
    else:
        result = "PASS"
    return {
        "result": result,
        "status": "observed",
        "failure_case": safe_failure_case,
        "no_formal_side_effect": no_side_effect,
        "expected_http": expected_http,
        "actual_http": actual_http,
        "readback": readback if readback is not None else "not_recorded",
        "evidence_refs": ["failure_path", "side_effect_readback"],
    }


def build_mock_main_path_fixture() -> tuple[dict[str, Any], dict[str, Any]]:
    """Build a deterministic contract-only success and failure fixture.

    The fixture is intentionally explicit about its scope: it proves the report
    shape and the no-half-write failure assertion, not a live API deployment.

    It is a **fully verified** synthetic run: every node answers as the
    contract declares, no prose assertion is left unjudged, and the runner's
    verdict on the provider's semantics is ``verified``.  That is what lets
    this path reach PASS at all -- a run whose prose assertions nobody judged
    is NOT RUN, and a real F01 run is in exactly that state today, because the
    contract leaves four prose assertions to a human.
    """
    workflow = json.loads(WORKFLOW_PATH.read_text(encoding="utf-8"))
    steps = [
        {"step_id": step["id"], "operation_id": step["operation_id"],
         "verdict": "passed", "actual_http": step["expected_http"]}
        for step in workflow["steps"]
    ]
    main_report = {
        "workflow": "F01",
        "runner_status": "completed",
        "completed_steps": len(steps),
        "total_steps": len(steps),
        "verification_scope": "contract_fixture_only",
        "provider_semantics_status": "verified",
        "manual_assertions": [],
        "fixture_metadata": {
            "template_version": "template.v1",
            "ruleset_hash": "rules.key",
            "permission_snapshot": {"status": "mock", "project": "project.id", "actor": "user.id"},
            "dependency_versions": {"contract": "contract.v1", "api": "not_run",
                                     "model": "not_run", "weknora": "not_run", "docx": "not_run"},
            "environment_versions": {"runtime": "python.contract", "platform": "platform.contract"},
            "object_versions": {"project": "project.v1", "spec": "spec.revision",
                                 "chapter": "chapter.v1", "asset": "asset.v1"},
        },
        "fixture_state": {
            "project": {"id": "project.id", "version": "project.v1", "status": "active"},
            "asset": {"id": "asset.id", "permission": "allow", "status": "ready"},
            "chapter": {"id": "chapter.id", "version": "chapter.v1", "status": "ready"},
            "check": {"id": "check.id", "status": "passed"},
            "release": {"id": "release.id", "snapshot": "snapshot.id", "status": "ready"},
            "export": {"id": "export.id", "status": "ready"},
            "download": {"id": "export.id", "reauthorized": True},
        },
        "steps": steps,
    }
    failure_observation = {
        "failure_case": "duplicate_formal_write",
        "expected_http": 409,
        "actual_http": 409,
        "no_formal_side_effect": True,
        "readback": {"status": "unchanged", "revision": "revision.id"},
    }
    return main_report, failure_observation


def build_main_path_report(*, main_report: dict[str, Any], runtime_mode: str = "mock",
                           failure_observation: dict[str, Any] | None = None,
                           fixture_id: str = "G6-F01-v1") -> dict[str, Any]:
    if not isinstance(main_report, dict):
        raise ValueError("main path report must be an object")
    if main_report.get("workflow") != "F01":
        raise ValueError("G6 main path must be derived from the committed F01 workflow")
    if not isinstance(fixture_id, str) or not SAFE_FIXTURE_ID.fullmatch(fixture_id):
        raise ValueError("fixture_id must be a redacted fixture alias or digest")
    # The contract on disk is the expectation this verdict is made against; the
    # module's own declaration is only the fallback for the case where the
    # contract cannot be read at all (the verdict is BLOCKED then).
    contract = _contract_expectations()
    expected_http = dict(contract["expected_http"]) if contract else dict(EXPECTED_STEP_HTTP)
    expected_step_ids = list(contract["step_ids"]) if contract else list(REQUIRED_STEP_IDS)
    coverage = _coverage(main_report, expected_step_ids)
    analysis = _observed_analysis(main_report)
    main_result = analysis["result"]
    failure = _failure_path(failure_observation)
    metadata = _safe_metadata(main_report.get("fixture_metadata", {}))
    fixture_state = _safe_fixture_state(main_report.get("fixture_state"))
    failed_checks = _failed_checks(main_report)
    safe_main_report = _safe_main_report(main_report, expected_http, failed_checks)
    fixture_definition = {
        "fixture_id": fixture_id,
        "required_parts": list(REQUIRED_PARTS),
        "required_step_ids": list(expected_step_ids),
        "metadata": metadata,
        "fixture_state": fixture_state,
        "main_steps": safe_main_report["steps"],
        "failure_shape": {
            "failure_case": failure.get("failure_case"),
            "expected_http": failure.get("expected_http"),
            "actual_http": failure.get("actual_http"),
            "no_formal_side_effect": failure.get("no_formal_side_effect"),
            "readback": failure.get("readback", {}),
        },
    }
    fixture_digest = digest(fixture_definition)
    result = main_result if main_result != "PASS" else failure["result"]
    if (result != "FAIL" and
            (not coverage["constructed_complete"] or not coverage["step_sequence_complete"]
             or not coverage["step_counts_complete"])):
        result = "BLOCKED"
    return {
        "report_version": 1,
        "scope": "canonical F01 project-to-download path plus one failure side-effect fixture",
        "fixture_id": fixture_id,
        "fixture_digest": fixture_digest,
        "runtime_mode": runtime_mode,
        "result": result,
        "fixture_coverage": coverage,
        "main_path": {
            "result": main_result,
            "reason": analysis["reason"],
            "completed_steps": safe_main_report["completed_steps"],
            "total_steps": safe_main_report["total_steps"],
            "contract_nodes": len(expected_step_ids),
            "verification_scope": safe_main_report["verification_scope"],
            "http_mismatches": analysis["http_mismatches"],
            "failed_checks": analysis["failed_checks"],
            "unjudged_manual_assertions": analysis["unjudged_manual_assertions"],
            "provider_semantics_status": analysis["provider_semantics_status"],
            "contract_mismatch": analysis["contract_mismatch"],
        },
        "fixture_state": fixture_state,
        "failure_path": failure,
        "quality_evidence": build_evidence(
            fixture_id=fixture_id, runtime_mode=runtime_mode, result=result,
            input_value={"fixture_id": fixture_id, "coverage": coverage},
            output_value={"main_path": safe_main_report, "failure_path": failure},
            attribution=(["runtime"] if result == "NOT RUN"
                         else ["fixture_or_test"] if result == "BLOCKED"
                         else ["domain_logic"]),
            evidence_refs=["fixture_coverage", "main_path", "failure_path"],
            owner="quality-operations", reviewer="unassigned",
            fixture_digest=fixture_digest,
            template_version=metadata.get("template_version"),
            ruleset_hash=metadata.get("ruleset_hash"),
            permission_snapshot=metadata.get("permission_snapshot"),
            dependency_versions=metadata.get("dependency_versions"),
            environment_versions=metadata.get("environment_versions"),
            object_versions=metadata.get("object_versions"),
            input_summary={"required_parts": len(REQUIRED_PARTS)},
            output_summary={"completed_steps": safe_main_report["completed_steps"],
                            "failure_side_effect_status": failure["status"],
                            "fixture_parts_observed": sorted(fixture_state)},
        ),
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run the canonical G6 F01 main path and write a quality report.")
    parser.add_argument("--base-url")
    parser.add_argument("--token")
    parser.add_argument("--runtime-mode", choices=RUNTIME_MODES, default="mock")
    parser.add_argument("--failure-observation", type=Path,
                        help="脱敏的失败副作用读回 JSON；缺少时主路径保持 BLOCKED")
    parser.add_argument("--mock-fixture", action="store_true",
                        help="运行内置 contract-only 主路径成功/失败 fixture")
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args(argv)
    runner = None
    try:
        if args.mock_fixture:
            if args.runtime_mode != "mock":
                raise ValueError("--mock-fixture only supports runtime-mode=mock")
            main_report, failure = build_mock_main_path_fixture()
            if args.failure_observation:
                try:
                    failure = json.loads(args.failure_observation.read_text(encoding="utf-8"))
                except (OSError, json.JSONDecodeError) as error:
                    raise WorkflowError(f"cannot read failure observation: {error}") from error
                if not isinstance(failure, dict):
                    raise WorkflowError("failure observation must be an object")
            report = build_main_path_report(main_report=main_report, runtime_mode="mock",
                                             failure_observation=failure)
            write_report(args.report, report)
            print(f"{report['fixture_id']} {report['result']} report written to {args.report}")
            return 0 if report["result"] == "PASS" else 1
        runner = ScenarioRunner.from_files(OPENAPI_PATH, WORKFLOW_PATH, base_url=args.base_url, token=args.token)
        failure_observation = None
        if args.failure_observation:
            try:
                failure_observation = json.loads(args.failure_observation.read_text(encoding="utf-8"))
            except (OSError, json.JSONDecodeError) as error:
                raise WorkflowError(f"cannot read failure observation: {error}") from error
            if not isinstance(failure_observation, dict):
                raise WorkflowError("failure observation must be an object")
        write_report(args.report, build_main_path_report(main_report=runner.report("not_started"),
                                                         runtime_mode=args.runtime_mode,
                                                         failure_observation=failure_observation))
        result = runner.run()
        report = build_main_path_report(main_report=runner.report("completed"), runtime_mode=args.runtime_mode,
                                        failure_observation=failure_observation)
        write_report(args.report, report)
        print(f"{report['fixture_id']} {report['result']} report written to {args.report}")
        return 0 if report["result"] == "PASS" else 1
    except (WorkflowError, OSError, ValueError) as error:
        if runner is not None:
            write_report(args.report, build_main_path_report(main_report=runner.report("failed"),
                                                             runtime_mode=args.runtime_mode))
        print(f"G6 main path FAILED: {error}")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
