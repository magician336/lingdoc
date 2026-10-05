"""Shared, stable G6 quality-evidence shape."""
from __future__ import annotations

import hashlib
import json
import re
from typing import Any

RUNTIME_MODES = ("mock", "real_api_fake_model", "real")
RESULTS = ("PASS", "FAIL", "NOT RUN", "BLOCKED")
RESULT_SET = frozenset(RESULTS)
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
