"""Small, deterministic observability contract for G6 runtime evidence."""
from __future__ import annotations

import json
import re
from pathlib import Path
import sys
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.g6_evidence import RUNTIME_MODES, build_evidence, digest

EVENT_TYPES = (
    "request", "task", "changeset", "validation", "snapshot", "export", "download", "audit",
)
REQUIRED_FIELDS = ("event_type", "status", "correlation_id", "runtime_mode")
ALLOWED_FIELDS = frozenset({
    *REQUIRED_FIELDS, "causation_id", "request_id", "task_id", "changeset_id", "snapshot_id",
    "export_id", "resource_type", "tenant_id", "project_id", "context_revision", "target_revision",
    "template_version", "ruleset_hash", "error_code", "retry_count", "stale", "duplicate_side_effect",
    "permission_decision", "download_reauthorized", "file_loss_class", "provider", "result_code",
    "permission_reason",
})
SENSITIVE_FIELDS = frozenset({"body", "content", "quoted_text", "source_text", "token", "authorization",
                              "prompt", "completion", "model_input"})
ID_FIELDS = frozenset({"correlation_id", "causation_id", "request_id", "task_id", "changeset_id", "snapshot_id", "export_id",
                       "tenant_id", "project_id", "context_revision", "target_revision"})
EVENT_REQUIRED_IDS = {
    "request": ("request_id",),
    "task": ("task_id",),
    "changeset": ("changeset_id",),
    "validation": ("task_id",),
    "snapshot": ("snapshot_id",),
    "export": ("export_id",),
    "download": ("export_id",),
    "audit": ("request_id",),
}

ALERT_ACTIONS = (
    {"signal": "request_denied_or_conflict", "action": "notify_owner"},
    {"signal": "permission_denied_or_revocation_block", "action": "pause_affected_flow"},
    {"signal": "async_failure_or_retry_spike", "action": "retry_then_notify_owner"},
    {"signal": "stale_or_duplicate_side_effect", "action": "stop_write_and_open_incident"},
    {"signal": "docx_loss_detected", "action": "block_export"},
    {"signal": "external_dependency_unavailable", "action": "mark_blocked_and_notify_owner"},
)


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
    if event.get("runtime_mode") not in RUNTIME_MODES:
        raise ValueError("runtime_mode must be one of " + ", ".join(RUNTIME_MODES))
    missing_link_ids = [field for field in EVENT_REQUIRED_IDS[event["event_type"]]
                        if not event.get(field)]
    if missing_link_ids:
        raise ValueError("event is missing link fields: " + ", ".join(missing_link_ids))
    for field in ID_FIELDS.intersection(event):
        value = event[field]
        if not isinstance(value, str) or not re.fullmatch(
                r"(?:[a-z][a-z0-9_.-]*\.(?:id|key)|corr-[A-Za-z0-9_.-]+|sha256:[0-9a-f]{64})", value):
            raise ValueError(f"{field} must be a redacted alias or digest")
    return {key: event[key] for key in sorted(event)}


def _chain_coverage(events: list[dict[str, Any]]) -> dict[str, bool]:
    types = {event["event_type"] for event in events}
    return {event_type: event_type in types for event_type in EVENT_TYPES}


def _signal_coverage(events: list[dict[str, Any]]) -> dict[str, bool]:
    return {
        "request_success": any(event["event_type"] == "request" and event.get("status") == "ok"
                                for event in events),
        "request_denied": any(event["event_type"] == "request" and event.get("status") == "denied"
                               for event in events),
        "request_conflict": any(event["event_type"] == "request" and event.get("status") == "conflict"
                                 for event in events),
        "async_failure_retry": any(event["event_type"] == "task"
                                    and (event.get("status") in {"failed", "retry"}
                                         or int(event.get("retry_count", 0)) > 0)
                                    for event in events),
        "stale_or_duplicate": any(event.get("stale") is not None
                                   or event.get("duplicate_side_effect") is not None for event in events),
        "permission_denied": any(event.get("permission_decision") == "deny"
                                  for event in events),
        "revocation_intercept": any(event.get("permission_decision") == "deny"
                                     and event.get("permission_reason") == "revoked"
                                     for event in events),
        "download_reauthorization": any(event["event_type"] == "download"
                                         and event.get("download_reauthorized") is not None
                                         for event in events),
        "docx_loss": any(event.get("file_loss_class") is not None for event in events),
        "external_dependency": any(event.get("provider") or event.get("result_code")
                                    for event in events),
    }


def build_observability_report(*, events: list[dict[str, Any]] | None = None,
                               runtime_mode: str = "real") -> dict[str, Any]:
    """Validate a redacted event stream and attach executable alert actions."""
    events = events or []
    sanitized = [sanitize_event(event) for event in events]
    correlation_ids = sorted({event["correlation_id"] for event in sanitized})
    coverage = _chain_coverage(sanitized)
    signals = _signal_coverage(sanitized)
    complete = (bool(sanitized) and all(coverage.values()) and all(signals.values())
                and len(correlation_ids) == 1)
    result = "PASS" if complete else "BLOCKED"
    if any(event.get("file_loss_class") not in {None, "none"} for event in sanitized):
        result = "FAIL"
    return {
        "report_version": 1,
        "scope": "redacted request-to-download correlation and executable quality alerts",
        "runtime_mode": runtime_mode,
        "result": result,
        "correlation_ids": correlation_ids,
        "chain_coverage": coverage,
        "signal_coverage": signals,
        "events": sanitized,
        "alerts": list(ALERT_ACTIONS),
        "quality_evidence": build_evidence(
                fixture_id="G6-OBS-01", runtime_mode=runtime_mode, result=result,
                input_value=sanitized, output_value={"coverage": coverage, "signals": signals},
                attribution=["runtime", "external_service"] if result == "BLOCKED" else ["domain_logic"],
                evidence_refs=["events", "chain_coverage", "signal_coverage", "alerts"],
                owner="quality-operations", reviewer="unassigned",
                input_summary={"event_count": len(sanitized)}, output_summary={"chain_coverage": coverage},
            ),
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
