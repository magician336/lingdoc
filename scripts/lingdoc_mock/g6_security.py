"""Deterministic G6 security acceptance matrix.

The matrix is deliberately an evidence gate rather than an authorization implementation. It
describes the negative paths that a live tenant must exercise and refuses to call them PASS until
the caller supplies observed status codes for the same fixture.
"""
from __future__ import annotations

import json
from pathlib import Path
import sys
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.g6_evidence import build_evidence, digest, reduce_results

RUNTIME_MODES = ("mock", "real_api_fake_model", "real")

SECURITY_CASES = (
    {"id": "SEC-01", "name": "revoked_source_read", "severity": "P0",
     "expected_http": 403, "expected_decision": "deny", "action": "stop_source_and_export"},
    {"id": "SEC-02", "name": "revoked_export", "severity": "P0",
     "expected_http": 403, "expected_decision": "deny", "action": "block_export"},
    {"id": "SEC-03", "name": "revoked_download", "severity": "P0",
     "expected_http": 403, "expected_decision": "deny", "action": "block_download"},
    {"id": "SEC-04", "name": "cross_tenant_id_substitution", "severity": "P0",
     "expected_http": 404, "expected_decision": "deny", "action": "stop_affected_traffic"},
    {"id": "SEC-05", "name": "cross_project_id_substitution", "severity": "P0",
     "expected_http": 404, "expected_decision": "deny", "action": "stop_affected_traffic"},
    {"id": "SEC-06", "name": "missing_scope", "severity": "P1",
     "expected_http": 403, "expected_decision": "deny", "action": "notify_owner"},
    {"id": "SEC-07", "name": "shared_agent_scope", "severity": "P1",
     "expected_http": 403, "expected_decision": "deny", "action": "notify_owner"},
    {"id": "SEC-08", "name": "audit_query_overreach", "severity": "P1",
     "expected_http": 403, "expected_decision": "deny", "action": "notify_security"},
    {"id": "SEC-09", "name": "stale_snapshot_replay", "severity": "P0",
     "expected_http": 403, "expected_decision": "deny", "action": "block_delivery"},
    {"id": "SEC-10", "name": "cache_bypass_after_revocation", "severity": "P0",
     "expected_http": 403, "expected_decision": "deny", "action": "purge_restricted_cache"},
    {"id": "SEC-11", "name": "presigned_url_replay", "severity": "P0",
     "expected_http": 403, "expected_decision": "deny", "action": "revoke_download_url"},
    {"id": "SEC-12", "name": "audit_metadata_after_revoke", "severity": "P2",
     "expected_http": 200, "expected_decision": "allow_metadata_only", "action": "retain_audit_access"},
)


def _redact(value: Any) -> Any:
    if isinstance(value, dict):
        sensitive = {"token", "authorization", "cookie", "body", "content", "quoted_text",
                     "source_text", "prompt", "completion"}
        return {key: "[redacted]" if key.lower() in sensitive else _redact(item)
                for key, item in sorted(value.items())}
    if isinstance(value, list):
        return [_redact(item) for item in value]
    return value


def _contains_restricted_content(value: Any) -> bool:
    if isinstance(value, dict):
        sensitive = {"body", "content", "quoted_text", "source_text", "token", "authorization"}
        return bool(sensitive.intersection(key.lower() for key in value)) or any(
            _contains_restricted_content(item) for item in value.values())
    if isinstance(value, list):
        return any(_contains_restricted_content(item) for item in value)
    return False


def _result(case: dict[str, Any], observation: dict[str, Any] | None) -> str:
    if not observation or "actual_http" not in observation:
        return "BLOCKED"
    if _contains_restricted_content(observation):
        return "FAIL"
    if observation["actual_http"] != case["expected_http"]:
        return "FAIL"
    if observation.get("decision") not in {None, case["expected_decision"]}:
        return "FAIL"
    return "PASS"


def build_security_report(*, runtime_mode: str = "real", fixture_id: str = "G6-SEC-01",
                          observations: dict[str, dict[str, Any]] | None = None) -> dict[str, Any]:
    """Build a stable security report from optional status-only live observations."""
    if runtime_mode not in RUNTIME_MODES:
        raise ValueError(f"runtime_mode must be one of {', '.join(RUNTIME_MODES)}")
    observations = observations or {}
    fixture_digest = digest([case["id"] for case in SECURITY_CASES])
    cases = []
    for definition in SECURITY_CASES:
        observation = observations.get(definition["id"])
        result = _result(definition, observation)
        cases.append({
            **definition,
            "runtime_mode": runtime_mode,
            "result": result,
            "actual_http": observation.get("actual_http") if observation else None,
            "observed_decision": observation.get("decision") if observation else None,
            "response_digest": digest(_redact(observation)) if observation else None,
            "evidence_refs": ["security_matrix", definition["id"]],
            "attribution": "permission" if result in {"FAIL", "BLOCKED"} else "domain_logic",
        })
    results = [case["result"] for case in cases]
    overall = reduce_results(results)
    severity_counts = {severity: sum(case["severity"] == severity for case in cases)
                       for severity in ("P0", "P1", "P2")}
    return {
        "report_version": 1,
        "fixture_id": fixture_id,
        "fixture_digest": fixture_digest,
        "scope": "current-permission security checks for body, source, export, download and audit metadata",
        "runtime_mode": runtime_mode,
        "result": overall,
        "severity_counts": severity_counts,
        "cases": cases,
        "quality_evidence": build_evidence(
            fixture_id=fixture_id, runtime_mode=runtime_mode, result=overall,
            input_value={"fixture_id": fixture_id, "cases": [case["id"] for case in cases]},
            output_value=cases, attribution=["permission"],
            evidence_refs=["cases", "severity_counts"], owner="security", reviewer="unassigned",
            fixture_digest=fixture_digest, permission_snapshot={"status": "observed_status_only"},
            input_summary={"case_count": len(cases)}, output_summary={"severity_counts": severity_counts},
        ),
        "redaction": {"body": "forbidden", "source_text": "forbidden", "token": "forbidden",
                      "stored_fields": ["status", "severity", "decision", "response_digest"]},
    }


def main() -> int:
    report = build_security_report()
    print(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True))
    return 0 if report["result"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
