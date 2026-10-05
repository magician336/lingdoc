"""Shared, stable G6 quality-evidence shape."""
from __future__ import annotations

import hashlib
import json
import math
import re
from typing import Any

RUNTIME_MODES = ("mock", "real_api_fake_model", "real")
RUNTIME_DEPENDENCIES = {
    "mock": ("contract",),
    "real_api_fake_model": ("api", "permissions", "queue", "file"),
    "real": ("api", "permissions", "queue", "file", "model", "weknora", "docx"),
}
RESULTS = ("PASS", "FAIL", "NOT RUN", "BLOCKED")
RESULT_SET = frozenset(RESULTS)
SCENARIO_SCOPES = {"observed", "scenario_runner_matrix", "http_smoke_only", "contract_fixture_only"}
SAFE_READBACK_STATUS = {"unchanged", "not_recorded", "observed", "blocked", "unknown"}
SAFE_FIXTURE_ID = re.compile(
    r"(?:[A-Za-z][A-Za-z0-9_.-]{1,63}(?::[A-Za-z0-9_.-]{1,64})?|[a-z][a-z0-9_.-]*\.(?:id|key)|sha256:[0-9a-f]{64})"
)


def reduce_results(results: list[str]) -> str:
    """Reduce independent gate results without allowing missing evidence to pass."""
    if "FAIL" in results:
        return "FAIL"
    if "BLOCKED" in results:
        return "BLOCKED"
    if "NOT RUN" in results:
        return "NOT RUN"
    return "PASS" if results and all(result == "PASS" for result in results) else "BLOCKED"


def digest(value: Any) -> str:
    payload = json.dumps(value, ensure_ascii=False, sort_keys=True,
                         separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(payload).hexdigest()


def is_finite_number(value: Any) -> bool:
    """Accept measurable numeric observations while rejecting bool/NaN/Infinity."""
    return (isinstance(value, (int, float)) and not isinstance(value, bool)
            and math.isfinite(value))


def normalize_scenario_evidence(entry: dict[str, Any], fixture_ids: list[str],
                                runtime_mode: str) -> tuple[dict[str, Any], str]:
    """Keep only status-shaped main/failure evidence and require both for a mode PASS."""
    raw = entry.get("scenario_evidence")
    blocked = {
        "main_path": {"result": "BLOCKED", "fixture_id": None, "verification_scope": "not_recorded"},
        "key_failure": {"result": "BLOCKED", "fixture_id": None, "http_status": None,
                         "no_formal_side_effect": None, "readback_status": "not_recorded"},
    }
    if not isinstance(raw, dict):
        return blocked, "BLOCKED"
    main = raw.get("main_path")
    failure = raw.get("key_failure")
    if not isinstance(main, dict) or not isinstance(failure, dict):
        return blocked, "BLOCKED"
    main_id = main.get("fixture_id")
    failure_id = failure.get("fixture_id")
    main_result = main.get("result") if main.get("result") in RESULT_SET else "BLOCKED"
    failure_result = failure.get("result") if failure.get("result") in RESULT_SET else "BLOCKED"
    main_scope = main.get("verification_scope") if main.get("verification_scope") in SCENARIO_SCOPES else "not_recorded"
    main_steps = main.get("completed_steps", 0)
    allowed_main_scopes = {"observed", "scenario_runner_matrix"}
    if runtime_mode == "mock":
        allowed_main_scopes.add("contract_fixture_only")
    main_complete = (main_result == "PASS" and isinstance(main_id, str)
                     and SAFE_FIXTURE_ID.fullmatch(main_id) and main_id in fixture_ids
                     and isinstance(main_steps, int) and not isinstance(main_steps, bool) and main_steps > 0
                     and main_scope in allowed_main_scopes)
    actual_http = failure.get("actual_http")
    readback_status = failure.get("readback_status")
    failure_complete = (failure_result == "PASS" and isinstance(failure_id, str)
                        and SAFE_FIXTURE_ID.fullmatch(failure_id) and failure_id in fixture_ids
                        and isinstance(actual_http, int) and not isinstance(actual_http, bool)
                        and 400 <= actual_http <= 599
                        and failure.get("no_formal_side_effect") is True
                        and readback_status in SAFE_READBACK_STATUS
                        and readback_status == "unchanged")
    normalized = {
        "main_path": {
            "result": main_result,
            "fixture_id": main_id if isinstance(main_id, str) and SAFE_FIXTURE_ID.fullmatch(main_id) else None,
            "completed_steps": main_steps if isinstance(main_steps, int) and not isinstance(main_steps, bool) else 0,
            "verification_scope": main_scope,
        },
        "key_failure": {
            "result": failure_result,
            "fixture_id": failure_id if isinstance(failure_id, str) and SAFE_FIXTURE_ID.fullmatch(failure_id) else None,
            "http_status": actual_http if isinstance(actual_http, int) and not isinstance(actual_http, bool) else None,
            "no_formal_side_effect": failure.get("no_formal_side_effect")
            if isinstance(failure.get("no_formal_side_effect"), bool) else None,
            "readback_status": readback_status if readback_status in SAFE_READBACK_STATUS else "not_recorded",
        },
    }
    evidence_result = reduce_results([main_result, failure_result])
    if main_complete and failure_complete:
        return normalized, evidence_result
    return normalized, "BLOCKED" if evidence_result != "FAIL" else "FAIL"


def build_evidence(*, fixture_id: str, runtime_mode: str, result: str,
                   input_value: Any, output_value: Any, attribution: list[str],
                   evidence_refs: list[str], owner: str, reviewer: str,
                   fixture_digest: str | None = None, template_version: str | None = None,
                   ruleset_hash: str | None = None, permission_snapshot: dict[str, Any] | None = None,
                   object_versions: dict[str, Any] | None = None,
                   dependency_versions: dict[str, Any] | None = None,
                   input_summary: dict[str, Any] | None = None,
                   output_summary: dict[str, Any] | None = None,
                   context_revision: str | None = None, target_version: str | None = None) -> dict[str, Any]:
    if result not in RESULT_SET:
        raise ValueError("G6 quality result must be PASS, FAIL, NOT RUN or BLOCKED")
    if runtime_mode not in RUNTIME_MODES:
        raise ValueError("runtime_mode must be one of " + ", ".join(RUNTIME_MODES))
    if not isinstance(fixture_id, str) or not SAFE_FIXTURE_ID.fullmatch(fixture_id):
        raise ValueError("fixture_id must be a redacted fixture alias or digest")
    return {
        "evidence_version": 1,
        "fixture_id": fixture_id,
        "fixture_digest": fixture_digest or digest(input_value),
        "project_id": "project.id",
        "template_version": template_version,
        "ruleset_hash": ruleset_hash,
        "object_versions": object_versions or {"status": "not_observed"},
        "permission_snapshot": permission_snapshot or {"status": "not_observed"},
        "runtime_mode": runtime_mode,
        "dependency_versions": dependency_versions or {"weknora": "unknown", "model": "unknown"},
        "context": {"context_revision": context_revision, "target_version": target_version,
                    "status": "not_available" if context_revision is None and target_version is None else "observed"},
        "input_summary": input_summary or {},
        "output_summary": output_summary or {},
        "input_hash": digest(input_value),
        "output_hash": digest(output_value),
        "execution_window": {"start": None, "end": None, "status": "not_recorded"},
        "result": result,
        "attribution": attribution,
        "evidence_refs": evidence_refs,
        "owner": owner,
        "reviewer": reviewer,
    }
