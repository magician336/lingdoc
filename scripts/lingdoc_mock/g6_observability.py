"""Small, deterministic observability contract for G6 runtime evidence."""
from __future__ import annotations

import hashlib
import json
from typing import Any

EVENT_TYPES = (
    "request", "task", "changeset", "validation", "snapshot", "export", "download", "audit",
)
REQUIRED_FIELDS = ("event_type", "status", "correlation_id", "runtime_mode")
ALLOWED_FIELDS = frozenset({
    *REQUIRED_FIELDS, "causation_id", "request_id", "task_id", "changeset_id", "snapshot_id",
    "export_id", "resource_type", "tenant_id", "project_id", "context_revision", "target_revision",
    "template_version", "ruleset_hash", "error_code", "retry_count", "stale", "duplicate_side_effect",
    "permission_decision", "download_reauthorized", "file_loss_class", "provider", "result_code",
})
SENSITIVE_FIELDS = frozenset({"body", "content", "quoted_text", "source_text", "token", "authorization",
                              "prompt", "completion", "model_input"})

ALERT_ACTIONS = (
    {"signal": "permission_denied_or_revocation_block", "action": "pause_affected_flow"},
    {"signal": "async_failure_or_retry_spike", "action": "retry_then_notify_owner"},
    {"signal": "stale_or_duplicate_side_effect", "action": "stop_write_and_open_incident"},
    {"signal": "docx_loss_detected", "action": "block_export"},
    {"signal": "external_dependency_unavailable", "action": "mark_blocked_and_notify_owner"},
)


def _digest(value: Any) -> str:
    payload = json.dumps(value, ensure_ascii=False, sort_keys=True,
                         separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(payload).hexdigest()


def sanitize_event(event: dict[str, Any]) -> dict[str, Any]:
    """Keep only the correlation and outcome fields permitted in structured logs."""
    if not isinstance(event, dict):
        raise ValueError("event must be an object")
    leaked = sorted(SENSITIVE_FIELDS.intersection(event))
    if leaked:
        raise ValueError("sensitive event fields are forbidden: " + ", ".join(leaked))
    missing = [field for field in REQUIRED_FIELDS if not event.get(field)]
    if missing:
        raise ValueError("event is missing required fields: " + ", ".join(missing))
    unknown = sorted(set(event) - ALLOWED_FIELDS)
    if unknown:
        raise ValueError("event contains unsupported fields: " + ", ".join(unknown))
    if event["event_type"] not in EVENT_TYPES:
        raise ValueError("unsupported event_type")
    if not isinstance(event["correlation_id"], str) or len(event["correlation_id"]) > 128:
        raise ValueError("correlation_id must be a short string")
    return {key: event[key] for key in sorted(event)}


def _chain_coverage(events: list[dict[str, Any]]) -> dict[str, bool]:
    types = {event["event_type"] for event in events}
    return {event_type: event_type in types for event_type in EVENT_TYPES}


def build_observability_report(*, events: list[dict[str, Any]] | None = None,
                               runtime_mode: str = "real") -> dict[str, Any]:
    """Validate a redacted event stream and attach executable alert actions."""
    events = events or []
    sanitized = [sanitize_event(event) for event in events]
    correlation_ids = sorted({event["correlation_id"] for event in sanitized})
    coverage = _chain_coverage(sanitized)
    complete = bool(sanitized) and all(coverage.values()) and len(correlation_ids) == 1
    result = "PASS" if complete else "BLOCKED"
    if any(event.get("file_loss_class") for event in sanitized):
        result = "FAIL"
    return {
        "report_version": 1,
        "scope": "redacted request-to-download correlation and executable quality alerts",
        "runtime_mode": runtime_mode,
        "result": result,
        "correlation_ids": correlation_ids,
        "chain_coverage": coverage,
        "events": sanitized,
        "alerts": list(ALERT_ACTIONS),
        "quality_evidence": {
            "evidence_version": 1,
            "fixture_id": "G6-OBS-01",
            "runtime_mode": runtime_mode,
            "result": result,
            "input_hash": _digest(sanitized),
            "output_hash": _digest({"coverage": coverage, "alerts": ALERT_ACTIONS}),
            "attribution": ["runtime", "external_service"] if result == "BLOCKED" else ["domain_logic"],
            "evidence_refs": ["events", "chain_coverage", "alerts"],
            "owner": "quality-operations",
            "reviewer": "unassigned",
        },
        "redaction": {
            "forbidden_fields": sorted(SENSITIVE_FIELDS),
            "allowed_shape": sorted(ALLOWED_FIELDS),
        },
    }


def main() -> int:
    report = build_observability_report()
    print(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True))
    return 0 if report["result"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
