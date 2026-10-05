"""Auditable Beta-window metrics with sample and denominator guards."""
from __future__ import annotations

import json
import re
from pathlib import Path
import sys
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.g6_evidence import build_evidence

METRICS = (
    ("manual_effort_minutes", "manual_minutes", "projects"),
    ("source_support_rate", "supported_claims", "total_claims"),
    ("missed_edit_rate", "missed_edits", "changesets"),
    ("false_positive_rate", "dismissed_issues", "issues_reviewed"),
    ("unconfirmed_rate", "unconfirmed_items", "snapshots"),
    ("export_loss_rate", "export_loss_items", "export_checks"),
)

WINDOW_FIELDS = frozenset({"window_id", "target_samples", "minimum_reportable_samples", "template_version",
                           "ruleset_hash", "redaction", "included_projects", "included_users",
                           "runtime_modes", "rollback_result", "uncovered_risks"})
SENSITIVE_FIELDS = frozenset({"body", "content", "quoted_text", "source_text", "token", "authorization",
                              "prompt", "completion", "model_input"})
ALIAS_PATTERN = re.compile(r"[a-z][a-z0-9_.-]*\.[a-z0-9_.-]+|sha256:[0-9a-f]{64}")


def _rate(numerator: int, denominator: int) -> float | None:
    return None if denominator <= 0 else numerator / denominator


def _sanitize_window(window: dict[str, Any]) -> dict[str, Any]:
    leaked = sorted(SENSITIVE_FIELDS.intersection(window))
    if leaked:
        raise ValueError("sensitive window fields are forbidden: " + ", ".join(leaked))
    unknown = sorted(set(window) - WINDOW_FIELDS)
    if unknown:
        raise ValueError("unsupported window fields: " + ", ".join(unknown))
    for field in ("window_id", "template_version", "ruleset_hash"):
        value = window.get(field)
        if not isinstance(value, str) or not ALIAS_PATTERN.fullmatch(value):
            raise ValueError(f"{field} must be a redacted alias or digest")
    for field in ("included_projects", "included_users"):
        values = window.get(field, [])
        if not isinstance(values, list) or not all(
                isinstance(value, str) and re.fullmatch(r"[a-z][a-z0-9_.-]*\.(?:id|key)", value)
                for value in values):
            raise ValueError(f"{field} must contain redacted aliases")
    modes = window.get("runtime_modes", [])
    if modes != ["real"]:
        raise ValueError("effect window runtime_modes must be exactly ['real']")
    return {key: window[key] for key in sorted(window)}


def _redacted_sample_id(value: Any) -> str:
    if not isinstance(value, str) or not ALIAS_PATTERN.fullmatch(value):
        raise ValueError("sample_id must be a redacted alias or digest")
    return value


def build_beta_report(*, samples: list[dict[str, Any]], window: dict[str, Any]) -> dict[str, Any]:
    """Aggregate only eligible real samples and preserve raw counts for all samples."""
    window = _sanitize_window(window)
    for sample in samples:
        _redacted_sample_id(sample.get("sample_id"))
    required_window = ("window_id", "target_samples", "minimum_reportable_samples", "template_version",
                       "ruleset_hash", "redaction", "included_projects", "included_users", "runtime_modes",
                       "rollback_result", "uncovered_risks")
    missing_window = [key for key in required_window
                      if key not in window or window[key] in (None, "")]
    real_samples = [sample for sample in samples if sample.get("runtime_mode") == "real"]
    excluded = [sample for sample in samples if sample.get("runtime_mode") != "real"]
    eligible = [sample for sample in real_samples if sample.get("quality_result") == "PASS"]
    p0_p1 = [sample for sample in real_samples if sample.get("severity") in {"P0", "P1"}]
    p2 = [sample for sample in real_samples if sample.get("severity") == "P2"]
    dependency_blocked = [sample for sample in real_samples
                          if sample.get("dependency_status") == "BLOCKED"
                          or sample.get("quality_result") in {"BLOCKED", "NOT RUN"}]
    raw_counts = {
        "submitted": len(samples),
        "real": len(real_samples),
        "eligible_real": len(eligible),
        "excluded_non_real": len(excluded),
        "p0_p1": len(p0_p1),
        "p2": len(p2),
        "dependency_blocked": len(dependency_blocked),
    }
    totals = {name: 0 for name, _, _ in METRICS}
    numerators = {name: 0 for name, _, _ in METRICS}
    for sample in eligible:
        for name, numerator_key, denominator_key in METRICS:
            numerators[name] += int(sample.get(numerator_key, 0))
            totals[name] += int(sample.get(denominator_key, 0))
    metrics = {
        name: {"numerator": numerators[name], "denominator": totals[name],
               "rate": _rate(numerators[name], totals[name])}
        for name, _, _ in METRICS
    }
    missing_denominators = sorted(name for name, values in metrics.items() if values["denominator"] == 0)
    minimum = int(window.get("minimum_reportable_samples", 0) or 0)
    if missing_window:
        result, decision, readiness = "BLOCKED", "continue_trial", "NOT READY"
        reason = "window metadata is incomplete"
    elif p0_p1 or dependency_blocked or window.get("rollback_result") != "PASS":
        result, decision, readiness = "BLOCKED", "pause", "BLOCKED"
        reason = "unresolved P0/P1, blocked real dependency or unverified rollback"
    elif len(eligible) < minimum or missing_denominators:
        result, decision, readiness = "BLOCKED", "continue_trial", "NOT READY"
        reason = "sample or denominator minimum is not met"
    else:
        result, decision, readiness = "PASS", "expand", "READY"
        reason = "eligible real sample and denominator gates passed"
    return {
        "report_version": 1,
        "scope": "one locked Beta trial window; only eligible real samples enter effect rates",
        "window": dict(window),
        "result": result,
        "readiness": readiness,
        "decision": decision,
        "decision_reason": reason,
        "raw_counts": raw_counts,
        "excluded_sample_ids": [_redacted_sample_id(sample.get("sample_id")) for sample in excluded],
        "missing_denominators": missing_denominators,
        "metrics": metrics,
        "quality_evidence": build_evidence(
            fixture_id=f"G6-BETA-{window.get('window_id', 'unknown')}", runtime_mode="real",
            result=result, input_value={"window": window, "samples": samples},
            output_value={"raw_counts": raw_counts, "metrics": metrics},
            attribution=["fixture_or_test"] if result == "BLOCKED" else ["permission", "runtime"]
            if result == "BLOCKED" else ["domain_logic"],
            evidence_refs=["window", "raw_counts", "metrics", "decision"],
            owner="product-and-qa", reviewer="unassigned",
            input_summary={"sample_count": len(samples)}, output_summary={"decision": decision},
        ),
    }


def main() -> int:
    report = build_beta_report(samples=[], window={
        "window_id": "window.manual", "target_samples": 0, "minimum_reportable_samples": 1,
        "template_version": "template.unknown", "ruleset_hash": "rules.unknown", "redaction": "fixture-v1",
        "included_projects": [], "included_users": [], "runtime_modes": ["real"],
        "rollback_result": "unknown", "uncovered_risks": ["no_samples"],
    })
    print(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True))
    return 0 if report["result"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
