"""Migration, grey-release and rollback rehearsal gate for G6."""
from __future__ import annotations

import hashlib
import json
from typing import Any

CHECKS = (
    ("old_objects_readable", "old-version objects remain readable during the compatibility window"),
    ("new_fields_unknown_safe", "missing fields default to UNKNOWN or pending migration"),
    ("unknown_state_not_confirmed", "unknown permission/version/provenance never confirms delivery"),
    ("flag_pause_by_scope", "tenant/project/template/ruleset/runtime flag can pause new writes"),
    ("history_immutable", "published templates, frozen snapshots, files and audit remain unchanged"),
    ("reverse_changeset", "applied changes restore through an inverse change rather than pointer edits"),
    ("post_rollback_verification", "recovery reruns permission, snapshot and download checks"),
)


def _digest(value: Any) -> str:
    payload = json.dumps(value, ensure_ascii=False, sort_keys=True,
                         separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(payload).hexdigest()


def evaluate_rehearsal(observations: dict[str, bool] | None = None) -> dict[str, Any]:
    """Evaluate status-only rehearsal observations without mutating repository data."""
    observations = observations or {}
    rows = []
    for check_id, requirement in CHECKS:
        observed = observations.get(check_id)
        rows.append({
            "id": check_id,
            "requirement": requirement,
            "observed": observed,
            "result": "PASS" if observed is True else "FAIL" if observed is False else "BLOCKED",
            "evidence_ref": f"rollback:{check_id}",
        })
    results = [row["result"] for row in rows]
    overall = "FAIL" if "FAIL" in results else "BLOCKED" if "BLOCKED" in results else "PASS"
    return {"checks": rows, "result": overall}


def build_rollback_report(*, observations: dict[str, bool] | None = None,
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
        "quality_evidence": {
            "evidence_version": 1,
            "fixture_id": "G6-ROLLBACK-01",
            "runtime_mode": runtime_mode,
            "result": evaluated["result"],
            "input_hash": _digest(observations or {}),
            "output_hash": _digest(evaluated),
            "attribution": ["runtime"] if evaluated["result"] == "BLOCKED" else ["domain_logic"],
            "evidence_refs": ["plan", "checks"],
            "owner": "release-operations",
            "reviewer": "unassigned",
        },
        "safety_boundary": "This report never edits migration state, revision pointers or published artifacts.",
    }


def main() -> int:
    report = build_rollback_report()
    print(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True))
    return 0 if report["result"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
