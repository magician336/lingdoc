"""Migration, grey-release and rollback rehearsal gate for G6."""
from __future__ import annotations

import json
from typing import Any

from scripts.lingdoc_mock.g6_evidence import build_evidence

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


def evaluate_rehearsal(observations: dict[str, Any] | None = None) -> dict[str, Any]:
    """Evaluate status-only rehearsal observations without mutating repository data."""
    observations = observations or {}
    check_observations = observations.get("checks", observations)
    rows = []
    for check_id, requirement in CHECKS:
        observed = check_observations.get(check_id)
        rows.append({
            "id": check_id,
            "requirement": requirement,
            "observed": observed,
            "result": "PASS" if observed is True else "FAIL" if observed is False else "BLOCKED",
            "evidence_ref": f"rollback:{check_id}",
        })
    results = [row["result"] for row in rows]
    metadata_complete = all(key in observations and observations[key] not in (None, "")
                            for key in REHEARSAL_METADATA)
    overall = ("FAIL" if "FAIL" in results else "BLOCKED"
               if "BLOCKED" in results or not metadata_complete else "PASS")
    return {"checks": rows, "result": overall,
            "drill": {key: observations.get(key) for key in REHEARSAL_METADATA},
            "metadata_complete": metadata_complete}


def build_rollback_report(*, observations: dict[str, Any] | None = None,
                          runtime_mode: str = "real") -> dict[str, Any]:
    evaluated = evaluate_rehearsal(observations)
    plan = {
        "pause_scope": ["tenant", "project", "template_version", "ruleset_hash", "runtime_mode"],
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
