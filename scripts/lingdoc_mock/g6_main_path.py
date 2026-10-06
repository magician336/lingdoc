"""G6 canonical main-path evidence wrapper around the existing F01 runner.

The wrapper does not create a second workflow. It derives coverage from the committed F01
workflow and requires a separate failure observation before declaring the complete G6 fixture
ready. That keeps a successful HTTP trace from masquerading as full failure-side-effect evidence.
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

from scripts.lingdoc_mock.g6_evidence import RUNTIME_MODES, SAFE_FIXTURE_ID, build_evidence, digest
from scripts.lingdoc_mock.run_f01 import OPENAPI_PATH, WORKFLOW_PATH, ScenarioRunner, WorkflowError, write_report

REQUIRED_PARTS = ("project", "asset", "chapter", "check", "release", "export", "download")
_WORKFLOW_DOCUMENT = json.loads(WORKFLOW_PATH.read_text(encoding="utf-8"))
REQUIRED_STEP_IDS = tuple(step["id"] for step in _WORKFLOW_DOCUMENT["steps"])
EXPECTED_STEP_HTTP = {step["id"]: step["expected_http"] for step in _WORKFLOW_DOCUMENT["steps"]}
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
SAFE_STEP_ID = re.compile(r"F01-[0-9]{2}")


def _step_id(step: dict[str, Any]) -> str | None:
    value = step.get("id", step.get("step_id"))
    return value if isinstance(value, str) and SAFE_STEP_ID.fullmatch(value) else None


def _step_http(step: dict[str, Any]) -> int | None:
    value = step.get("http_status", step.get("actual_http"))
    return value if isinstance(value, int) and not isinstance(value, bool) and 100 <= value <= 599 else None


def _step_verdict(step: dict[str, Any]) -> str:
    verdict = step.get("verdict")
    step_id = _step_id(step)
    status = _step_http(step)
    if (verdict == "passed" and step_id is not None and status is not None
            and EXPECTED_STEP_HTTP.get(step_id) != status):
        return "failed"
    if verdict in {"passed", "failed", "not_run"}:
        return verdict
    if step_id is not None and status is not None:
        return "passed" if EXPECTED_STEP_HTTP.get(step_id) == status else "failed"
    return "unknown"


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


def _safe_main_report(report: dict[str, Any]) -> dict[str, Any]:
    """Serialize only outcome fields from an F01 report into G6 evidence."""
    steps = []
    source_steps = report.get("steps", [])
    if not isinstance(source_steps, list):
        source_steps = []
    for step in source_steps:
        if not isinstance(step, dict):
            continue
        operation_id = step.get("operation_id")
        step_id = _step_id(step)
        verdict = _step_verdict(step)
        item = {
            "operation_id": operation_id if operation_id in OPERATION_PARTS else "unknown",
            "verdict": verdict if verdict in {"passed", "failed", "not_run"} else "unknown",
        }
        if step_id is not None:
            item["step_id"] = step_id
        status = _step_http(step)
        if status is not None:
            item["actual_http"] = status
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


def _observed_result(report: dict[str, Any]) -> str:
    if report.get("runner_status") != "completed":
        return "BLOCKED"
    steps = report.get("steps", [])
    if not isinstance(steps, list):
        return "BLOCKED"
    if any(_step_id(step) is not None and _step_http(step) is None
           for step in steps if isinstance(step, dict)):
        return "BLOCKED"
    verdicts = [_step_verdict(step) for step in steps if isinstance(step, dict)]
    if len(verdicts) != len(steps):
        return "BLOCKED"
    if any(verdict == "failed" for verdict in verdicts):
        return "FAIL"
    return "PASS" if verdicts and all(verdict == "passed" for verdict in verdicts) else "BLOCKED"


def _coverage(report: dict[str, Any]) -> dict[str, Any]:
    steps = report.get("steps", [])
    if not isinstance(steps, list):
        steps = []
    observed_step_ids = [_step_id(step) for step in steps if isinstance(step, dict)]
    expected_step_ids = list(REQUIRED_STEP_IDS)
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
    if missing:
        no_side_effect = (observation.get("no_formal_side_effect")
                          if isinstance(observation.get("no_formal_side_effect"), bool)
                          else None)
        return {
            "result": "BLOCKED",
            "status": "incomplete",
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
    if not isinstance(observation.get("failure_case"), str) or not observation["failure_case"].strip():
        return {
            "result": "FAIL", "status": "observed", "no_formal_side_effect": no_side_effect,
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
    coverage = _coverage(main_report)
    main_result = _observed_result(main_report)
    failure = _failure_path(failure_observation)
    metadata = _safe_metadata(main_report.get("fixture_metadata", {}))
    fixture_state = _safe_fixture_state(main_report.get("fixture_state"))
    safe_main_report = _safe_main_report(main_report)
    fixture_definition = {
        "fixture_id": fixture_id,
        "required_parts": list(REQUIRED_PARTS),
        "required_step_ids": list(REQUIRED_STEP_IDS),
        "metadata": metadata,
        "fixture_state": fixture_state,
        "main_steps": safe_main_report["steps"],
        "failure_shape": {
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
            "completed_steps": safe_main_report["completed_steps"],
            "total_steps": safe_main_report["total_steps"],
            "verification_scope": safe_main_report["verification_scope"],
        },
        "fixture_state": fixture_state,
        "failure_path": failure,
        "quality_evidence": build_evidence(
            fixture_id=fixture_id, runtime_mode=runtime_mode, result=result,
            input_value={"fixture_id": fixture_id, "coverage": coverage},
            output_value={"main_path": safe_main_report, "failure_path": failure},
            attribution=["fixture_or_test"] if result == "BLOCKED" else ["domain_logic"],
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
