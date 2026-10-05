"""Auditable Beta-window metrics with sample and denominator guards."""
from __future__ import annotations

import hashlib
import json
from typing import Any

METRICS = (
    ("source_support_rate", "supported_claims", "total_claims"),
    ("false_positive_rate", "dismissed_issues", "issues_reviewed"),
    ("unconfirmed_rate", "unconfirmed_items", "snapshots"),
    ("export_loss_rate", "export_loss_items", "export_checks"),
)


def _digest(value: Any) -> str:
    payload = json.dumps(value, ensure_ascii=False, sort_keys=True,
                         separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(payload).hexdigest()


def _rate(numerator: int, denominator: int) -> float | None:
    return None if denominator <= 0 else numerator / denominator


def build_beta_report(*, samples: list[dict[str, Any]], window: dict[str, Any]) -> dict[str, Any]:
    """Aggregate only eligible real samples and preserve raw counts for all samples."""
    required_window = ("window_id", "target_samples", "minimum_reportable_samples", "template_version",
                       "ruleset_hash", "redaction", "included_projects")
    missing_window = [key for key in required_window if not window.get(key)]
    real_samples = [sample for sample in samples if sample.get("runtime_mode") == "real"]
    excluded = [sample for sample in samples if sample.get("runtime_mode") != "real"]
    eligible = [sample for sample in real_samples if sample.get("quality_result") == "PASS"]
    p0_p1 = [sample for sample in real_samples if sample.get("severity") in {"P0", "P1"}]
    dependency_blocked = [sample for sample in real_samples
                          if sample.get("dependency_status") == "BLOCKED"
                          or sample.get("quality_result") in {"BLOCKED", "NOT RUN"}]
    raw_counts = {
        "submitted": len(samples),
        "real": len(real_samples),
        "eligible_real": len(eligible),
        "excluded_non_real": len(excluded),
        "p0_p1": len(p0_p1),
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
        result, decision = "NOT READY", "continue_trial"
        reason = "window metadata is incomplete"
    elif p0_p1 or dependency_blocked:
        result, decision = "BLOCKED", "pause"
        reason = "unresolved P0/P1 or blocked real dependency"
    elif len(eligible) < minimum or missing_denominators:
        result, decision = "NOT READY", "continue_trial"
        reason = "sample or denominator minimum is not met"
    else:
        result, decision = "PASS", "expand"
        reason = "eligible real sample and denominator gates passed"
    return {
        "report_version": 1,
        "scope": "one locked Beta trial window; only eligible real samples enter effect rates",
        "window": dict(window),
        "result": result,
        "decision": decision,
        "decision_reason": reason,
        "raw_counts": raw_counts,
        "excluded_sample_ids": [sample.get("sample_id", "unknown") for sample in excluded],
        "missing_denominators": missing_denominators,
        "metrics": metrics,
        "quality_evidence": {
            "evidence_version": 1,
            "fixture_id": f"G6-BETA-{window.get('window_id', 'unknown')}",
            "runtime_mode": "real",
            "result": result,
            "input_hash": _digest({"window": window, "samples": samples}),
            "output_hash": _digest({"raw_counts": raw_counts, "metrics": metrics}),
            "attribution": ["fixture_or_test"] if result == "NOT READY" else ["permission", "runtime"]
            if result == "BLOCKED" else ["domain_logic"],
            "evidence_refs": ["window", "raw_counts", "metrics", "decision"],
            "owner": "product-and-qa",
            "reviewer": "unassigned",
        },
    }


def main() -> int:
    report = build_beta_report(samples=[], window={})
    print(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True))
    return 0 if report["result"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
