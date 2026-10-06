"""Small, deterministic observability contract for G6 runtime evidence."""
from __future__ import annotations

import json
import math
import re
from pathlib import Path
import sys
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.g6_evidence import RUNTIME_MODES, build_evidence, digest, is_finite_number

EVENT_TYPES = (
    "request", "task", "changeset", "validation", "snapshot", "export", "download", "audit",
)
REQUIRED_FIELDS = ("event_type", "status", "correlation_id", "runtime_mode")
ALLOWED_FIELDS = frozenset({
    *REQUIRED_FIELDS, "causation_id", "request_id", "task_id", "changeset_id", "snapshot_id",
    "export_id", "resource_type", "tenant_id", "project_id", "context_revision", "target_revision",
    "template_version", "ruleset_hash", "error_code", "retry_count", "stale", "duplicate_side_effect",
    "permission_decision", "download_reauthorized", "file_loss_class", "provider", "result_code",
    "permission_reason", "duration_ms",
})
SENSITIVE_FIELDS = frozenset({"body", "content", "quoted_text", "source_text", "token", "authorization",
                              "prompt", "completion", "model_input"})
ID_FIELDS = frozenset({"correlation_id", "causation_id", "request_id", "task_id", "changeset_id", "snapshot_id", "export_id",
                       "tenant_id", "project_id", "context_revision", "target_revision"})
SAFE_STATUSES = frozenset({"ok", "success", "denied", "conflict", "failed", "error", "retry",
                           "blocked", "pending", "completed", "skipped", "unknown", "not_run"})
SAFE_CODE_FIELDS = frozenset({"resource_type", "template_version", "ruleset_hash", "error_code", "provider",
                              "result_code", "permission_reason", "file_loss_class"})
BOOLEAN_FIELDS = frozenset({"stale", "duplicate_side_effect", "download_reauthorized"})
PERMISSION_DECISIONS = frozenset({"allow", "deny", "allow_metadata_only"})
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

ACTIONABLE_SIGNALS = (
    "request_denied", "request_conflict", "async_failure_retry", "stale_or_duplicate",
    "permission_denied", "revocation_intercept", "download_reauthorization", "docx_loss",
    "external_dependency",
)
LATENCY_SIGNAL = "latency_p95_p99"
ALERT_ACTIONS = (
    {"signal": "request_denied", "action": "notify_owner"},
    {"signal": "request_conflict", "action": "notify_owner"},
    {"signal": "permission_denied", "action": "pause_affected_flow"},
    {"signal": "revocation_intercept", "action": "pause_affected_flow"},
    {"signal": "async_failure_retry", "action": "retry_then_notify_owner"},
    {"signal": "stale_or_duplicate", "action": "rollback_or_stop_write"},
    {"signal": "download_reauthorization", "action": "recheck_permission_before_download"},
    {"signal": "docx_loss", "action": "block_export"},
    {"signal": "external_dependency", "action": "mark_blocked_and_notify_owner"},
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
    if event.get("status") not in SAFE_STATUSES:
        raise ValueError("status must be a safe structured-log status")
    for field in SAFE_CODE_FIELDS.intersection(event):
        value = event[field]
        if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z][A-Za-z0-9_.:-]{0,127}", value):
            raise ValueError(f"{field} must be a safe structured-log code")
    if "duration_ms" in event and (not is_finite_number(event["duration_ms"])
                                   or event["duration_ms"] < 0):
        raise ValueError("duration_ms must be a non-negative number")
    if "retry_count" in event and (not isinstance(event["retry_count"], int)
                                    or isinstance(event["retry_count"], bool)
                                    or event["retry_count"] < 0):
        raise ValueError("retry_count must be a non-negative integer")
    for field in BOOLEAN_FIELDS.intersection(event):
        if not isinstance(event[field], bool):
            raise ValueError(f"{field} must be a boolean")
    if "permission_decision" in event and event["permission_decision"] not in PERMISSION_DECISIONS:
        raise ValueError("permission_decision must be an allowed decision")
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


def _latency_summary(events: list[dict[str, Any]]) -> dict[str, Any]:
    """Summarize observed request/task durations without retaining timestamps or payloads."""
    durations = sorted(
        event["duration_ms"] for event in events
        if isinstance(event.get("duration_ms"), (int, float))
        and not isinstance(event.get("duration_ms"), bool)
    )
    if not durations:
        return {"sample_count": 0, "p95_ms": None, "p99_ms": None, "status": "not_observed"}

    def nearest_rank(percentile: float) -> int | float:
        rank = max(1, math.ceil(percentile * len(durations)))
        return durations[rank - 1]

    return {
        "sample_count": len(durations),
        "p95_ms": nearest_rank(0.95),
        "p99_ms": nearest_rank(0.99),
        "status": "observed",
    }


def _signal_coverage(events: list[dict[str, Any]]) -> dict[str, bool]:
    retry_observed = any(
        event["event_type"] == "task"
        and (event.get("status") in {"failed", "error", "retry"}
             or (isinstance(event.get("retry_count", 0), int)
                 and not isinstance(event.get("retry_count", 0), bool)
                 and event.get("retry_count", 0) > 0))
        for event in events
    )
    return {
        "request_success": any(event["event_type"] == "request"
                                and event.get("status") in {"ok", "success"}
                                for event in events),
        "request_denied": any(event["event_type"] == "request" and event.get("status") == "denied"
                               for event in events),
        "request_conflict": any(event["event_type"] == "request" and event.get("status") == "conflict"
                                 for event in events),
        "async_failure_retry": retry_observed,
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
        LATENCY_SIGNAL: any("duration_ms" in event for event in events),
    }


def build_observability_report(*, events: list[dict[str, Any]] | None = None,
                               runtime_mode: str = "real") -> dict[str, Any]:
    """Validate a redacted event stream and attach executable alert actions."""
    events = events or []
    sanitized = [sanitize_event(event) for event in events]
    correlation_ids = sorted({event["correlation_id"] for event in sanitized})
    coverage = _chain_coverage(sanitized)
    signals = _signal_coverage(sanitized)
    latency = _latency_summary(sanitized)
    alert_signals = {alert["signal"] for alert in ALERT_ACTIONS}
    alert_coverage = {signal: signal in alert_signals for signal in ACTIONABLE_SIGNALS}
    uncovered_scope = ([f"event:{event_type}" for event_type, observed in coverage.items() if not observed]
                       + [f"signal:{signal}" for signal, observed in signals.items() if not observed]
                       + [f"alert:{signal}" for signal, observed in alert_coverage.items() if not observed])
    complete = (bool(sanitized) and all(coverage.values()) and all(signals.values())
                and all(alert_coverage.values())
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
        "latency": latency,
        "alert_coverage": alert_coverage,
        "uncovered_scope": uncovered_scope,
        "events": sanitized,
        "alerts": list(ALERT_ACTIONS),
        "quality_evidence": build_evidence(
                fixture_id="G6-OBS-01", runtime_mode=runtime_mode, result=result,
                input_value=sanitized, output_value={"coverage": coverage, "signals": signals},
                attribution=["runtime", "external_service"] if result == "BLOCKED" else ["domain_logic"],
                evidence_refs=["events", "chain_coverage", "signal_coverage", "alerts"],
                owner="quality-operations", reviewer="unassigned",
                input_summary={"event_count": len(sanitized)},
                output_summary={"chain_coverage": coverage, "latency": latency},
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
