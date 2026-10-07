"""Migration, grey-release and rollback rehearsal gate for G6."""
from __future__ import annotations

import json
import re
from pathlib import Path
import sys
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.g6_evidence import build_evidence, is_finite_number

CHECKS = (
    ("old_objects_readable", "old-version objects remain readable during the compatibility window"),
    ("new_fields_unknown_safe", "missing fields default to UNKNOWN or pending migration"),
    ("unknown_state_not_confirmed", "unknown permission/version/provenance never confirms delivery"),
    ("flag_pause_by_scope", "tenant/project/template/ruleset/runtime flag can pause new writes"),
    ("history_immutable", "published templates, frozen snapshots, files and audit remain unchanged"),
    ("reverse_changeset", "applied changes restore through an inverse change rather than pointer edits"),
    ("post_rollback_verification", "recovery reruns permission, snapshot and download checks"),
)


REHEARSAL_METADATA = ("drill_id", "severity", "impact_scope", "duration_ms",
                      "recovery_verified", "uncovered_risks")
REDACTED_ID = re.compile(r"[a-z][a-z0-9_.-]*\.(?:id|key)|sha256:[0-9a-f]{64}")


def evaluate_rehearsal(observations: dict[str, Any] | None = None) -> dict[str, Any]:
    """Evaluate status-only rehearsal observations without mutating repository data."""
    observations = {} if observations is None else observations
    if not isinstance(observations, dict):
        raise ValueError("rollback observations must be an object")
    check_observations = observations.get("checks", observations)
    if not isinstance(check_observations, dict):
        raise ValueError("rollback checks must be an object")
    known_check_ids = {check_id for check_id, _ in CHECKS}
    allowed_top_level = known_check_ids | set(REHEARSAL_METADATA)
    unknown_top_level = sorted(set(observations) - (allowed_top_level | {"checks"}))
    if unknown_top_level:
        raise ValueError("rollback observations contain unsupported fields: " + ", ".join(unknown_top_level))
    unknown_checks = sorted(set(check_observations) - (known_check_ids if "checks" in observations
                                                       else allowed_top_level))
    if unknown_checks:
        raise ValueError("rollback checks contain unsupported fields: " + ", ".join(unknown_checks))
    rows = []
    for check_id, requirement in CHECKS:
        observed = check_observations.get(check_id)
        rows.append({
            "id": check_id,
            "requirement": requirement,
            "observed": observed if isinstance(observed, bool) else None,
            "result": "PASS" if observed is True else "FAIL" if observed is False else "BLOCKED",
            "evidence_ref": f"rollback:{check_id}",
        })
    results = [row["result"] for row in rows]
    metadata_complete = all(key in observations and observations[key] not in (None, "")
                            for key in REHEARSAL_METADATA)
    metadata_valid = (
        isinstance(observations.get("drill_id"), str)
        and bool(REDACTED_ID.fullmatch(observations.get("drill_id", "")))
        and observations.get("severity") in {"P0", "P1"}
        and isinstance(observations.get("impact_scope"), list)
        and bool(observations.get("impact_scope"))
        and all(isinstance(value, str) and re.fullmatch(r"[a-z][a-z0-9_.-]*\.(?:id|key)", value)
                for value in observations.get("impact_scope", []))
        and is_finite_number(observations.get("duration_ms"))
        and observations.get("duration_ms") >= 0
        and observations.get("recovery_verified") is True
        and isinstance(observations.get("uncovered_risks"), list)
        and all(isinstance(value, str) and re.fullmatch(r"[a-z][a-z0-9_.-]{0,127}", value)
                for value in observations.get("uncovered_risks", []))
    )
    overall = ("FAIL" if "FAIL" in results else "BLOCKED"
               if "BLOCKED" in results or not metadata_complete or not metadata_valid else "PASS")
    safe_drill = {
        "drill_id": observations.get("drill_id") if isinstance(observations.get("drill_id"), str)
        and REDACTED_ID.fullmatch(observations.get("drill_id", "")) else None,
        "severity": observations.get("severity") if observations.get("severity") in {"P0", "P1"} else None,
        "impact_scope": [value for value in observations.get("impact_scope", [])
                         if isinstance(value, str) and re.fullmatch(r"[a-z][a-z0-9_.-]*\.(?:id|key)", value)]
        if isinstance(observations.get("impact_scope"), list) else [],
        "duration_ms": observations.get("duration_ms") if is_finite_number(observations.get("duration_ms"))
        and observations.get("duration_ms") >= 0 else None,
        "recovery_verified": observations.get("recovery_verified")
        if isinstance(observations.get("recovery_verified"), bool) else None,
        "uncovered_risks": [value for value in observations.get("uncovered_risks", [])
                            if isinstance(value, str) and re.fullmatch(r"[a-z][a-z0-9_.-]{0,127}", value)]
        if isinstance(observations.get("uncovered_risks"), list) else [],
    }
    return {"checks": rows, "result": overall,
            "drill": safe_drill,
            "metadata_complete": metadata_complete, "metadata_valid": metadata_valid}


def build_rollback_report(*, observations: dict[str, Any] | None = None,
                          runtime_mode: str = "real") -> dict[str, Any]:
    evaluated = evaluate_rehearsal(observations)
    plan = {
        "pause_scope": ["tenant", "project", "template_version", "ruleset_hash", "runtime_mode"],
        "migration_compatibility": {
            "old_version_read": "required",
            "new_version_write": "versioned",
            "rollback_window": "explicit",
            "failure_cleanup": "required",
            "unknown_default": "UNKNOWN",
        },
        "write_policy": "stop_new_version_writes_only",
        "preserve": ["published_template", "frozen_snapshot", "historical_file", "audit", "revision_pointer"],
        "recovery": ["inverse_changeset", "compatibility_read", "permission_recheck", "download_recheck"],
    }
    return {
        "report_version": 1,
        "fixture_id": "G6-ROLLBACK-01",
        "scope": "migration compatibility, grey pause and P0/P1 rollback rehearsal",
        "runtime_mode": runtime_mode,
        "result": evaluated["result"],
        "plan": plan,
        "checks": evaluated["checks"],
        "drill": evaluated["drill"],
        "quality_evidence": build_evidence(
            fixture_id="G6-ROLLBACK-01", runtime_mode=runtime_mode, result=evaluated["result"],
            input_value=observations or {}, output_value=evaluated,
            attribution=["runtime"] if evaluated["result"] == "BLOCKED" else ["domain_logic"],
            evidence_refs=["plan", "checks"], owner="release-operations", reviewer="unassigned",
            input_summary={"check_count": len(CHECKS)}, output_summary={"result": evaluated["result"]},
        ),
        "safety_boundary": "This report never edits migration state, revision pointers or published artifacts.",
    }


def main() -> int:
    report = build_rollback_report()
    print(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True))
    return 0 if report["result"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
