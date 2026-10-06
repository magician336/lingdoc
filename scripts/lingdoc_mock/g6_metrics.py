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

from scripts.lingdoc_mock.g6_evidence import RUNTIME_MODES, build_evidence, is_finite_number

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
                           "runtime_modes", "rollback_result", "uncovered_risks", "process_evidence"})
SENSITIVE_FIELDS = frozenset({"body", "content", "quoted_text", "source_text", "token", "authorization",
                              "prompt", "completion", "model_input"})
ALIAS_PATTERN = re.compile(r"[a-z][a-z0-9_.-]*\.[a-z0-9_.-]+|sha256:[0-9a-f]{64}")
RESULTS = {"PASS", "FAIL", "NOT RUN", "BLOCKED"}
ROLLBACK_RESULTS = RESULTS
NUMERIC_FIELDS = frozenset(field for _, numerator, denominator in METRICS
                            for field in (numerator, denominator))
COUNT_FIELDS = NUMERIC_FIELDS - {"manual_minutes"}


def _rate(numerator: int, denominator: int) -> float | None:
    return None if denominator <= 0 else numerator / denominator


def _sanitize_window(window: dict[str, Any]) -> dict[str, Any]:
    if not isinstance(window, dict):
        raise ValueError("effect window must be an object")
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
    if window.get("rollback_result") not in ROLLBACK_RESULTS:
        raise ValueError("rollback_result must be PASS, FAIL, NOT RUN or BLOCKED")
    for field in ("included_projects", "included_users"):
        values = window.get(field, [])
        if not isinstance(values, list) or not all(
                isinstance(value, str) and re.fullmatch(r"[a-z][a-z0-9_.-]*\.(?:id|key)", value)
                for value in values):
            raise ValueError(f"{field} must contain redacted aliases")
    modes = window.get("runtime_modes", [])
    if modes != ["real"]:
        raise ValueError("effect window runtime_modes must be exactly ['real']")
    for field in ("target_samples", "minimum_reportable_samples"):
        value = window.get(field)
        if not isinstance(value, int) or isinstance(value, bool) or value < 0:
            raise ValueError(f"{field} must be a non-negative integer")
    if not isinstance(window.get("redaction"), str) or not re.fullmatch(
            r"[A-Za-z0-9_.:-]{1,64}", window["redaction"]):
        raise ValueError("redaction must be a short safe label")
    risks = window.get("uncovered_risks")
    if not isinstance(risks, list) or not all(
            isinstance(value, str) and re.fullmatch(r"[a-z][a-z0-9_.-]{0,127}", value)
            for value in risks):
        raise ValueError("uncovered_risks must contain redacted short codes")
    process = window.get("process_evidence")
    required_process = ("authorization", "main_path", "failure_path", "manual_review",
                        "repair_or_rollback", "key_scenario_rerun")
    if not isinstance(process, dict) or set(process) != set(required_process):
        raise ValueError("process_evidence must cover authorization, main_path, failure_path, manual_review, repair_or_rollback and key_scenario_rerun")
    if any(value not in {"observed", "not_run", "blocked"} for value in process.values()):
        raise ValueError("process_evidence values must be observed, not_run or blocked")
    return {key: window[key] for key in sorted(window)}


def _redacted_sample_id(value: Any) -> str:
    if not isinstance(value, str) or not ALIAS_PATTERN.fullmatch(value):
        raise ValueError("sample_id must be a redacted alias or digest")
    return value


def _validate_sample(sample: dict[str, Any]) -> None:
    if not isinstance(sample, dict):
        raise ValueError("each sample must be an object")
    _redacted_sample_id(sample.get("sample_id"))
    if not isinstance(sample.get("sample_version"), str) or not ALIAS_PATTERN.fullmatch(sample["sample_version"]):
        raise ValueError("sample_version must be a redacted alias or digest")
    if sample.get("runtime_mode") not in RUNTIME_MODES:
        raise ValueError("sample runtime_mode must be one of " + ", ".join(RUNTIME_MODES))
    if sample.get("quality_result") not in RESULTS:
        raise ValueError("sample quality_result must be PASS, FAIL, NOT RUN or BLOCKED")
    if (sample.get("dependency_status") is not None
            and sample.get("dependency_status") not in {"verified", "not_verified", "BLOCKED"}):
        raise ValueError("sample dependency_status must be verified, not_verified or BLOCKED")
    if sample.get("severity") is not None and sample["severity"] not in {"P0", "P1", "P2", "P3"}:
        raise ValueError("sample severity must be P0, P1, P2 or P3")
    for field in NUMERIC_FIELDS:
        value = sample.get(field)
        if value is not None and (not is_finite_number(value) or value < 0):
            raise ValueError(f"sample {field} must be a non-negative number")
        if field in COUNT_FIELDS and value is not None and not isinstance(value, int):
            raise ValueError(f"sample {field} must be a non-negative integer")


def build_beta_report(*, samples: list[dict[str, Any]], window: dict[str, Any]) -> dict[str, Any]:
    """Aggregate only eligible real samples and preserve raw counts for all samples."""
    window = _sanitize_window(window)
    if not isinstance(samples, list):
        raise ValueError("samples must be a list")
    for sample in samples:
        _validate_sample(sample)
    required_window = ("window_id", "target_samples", "minimum_reportable_samples", "template_version",
                       "ruleset_hash", "redaction", "included_projects", "included_users", "runtime_modes",
                       "rollback_result", "uncovered_risks", "process_evidence")
    missing_window = [key for key in required_window
                      if key not in window or window[key] in (None, "")]
    real_samples = [sample for sample in samples if sample.get("runtime_mode") == "real"]
    excluded = [sample for sample in samples if sample.get("runtime_mode") != "real"]
    quality_pass_real = [sample for sample in real_samples if sample.get("quality_result") == "PASS"]
    eligible = [sample for sample in quality_pass_real
                if sample.get("dependency_status") == "verified"]
    p0_p1 = [sample for sample in real_samples if sample.get("severity") in {"P0", "P1"}]
    p2 = [sample for sample in real_samples if sample.get("severity") == "P2"]
    dependency_blocked = [sample for sample in real_samples
                          if sample.get("dependency_status") != "verified"
                          or sample.get("quality_result") in {"BLOCKED", "NOT RUN"}]
    raw_counts = {
        "submitted": len(samples),
        "real": len(real_samples),
        "quality_pass_real": len(quality_pass_real),
        "eligible_real": len(eligible),
        "excluded_non_real": len(excluded),
        "p0_p1": len(p0_p1),
        "p2": len(p2),
        "dependency_blocked": len(dependency_blocked),
    }
    missing_values = [
        {"sample_id": sample["sample_id"], "sample_version": sample["sample_version"],
         "missing_fields": sorted(NUMERIC_FIELDS - sample.keys())}
        for sample in samples if NUMERIC_FIELDS - sample.keys()
    ]
    raw_counts["samples_with_missing_values"] = len(missing_values)
    missing_metric_values = {
        name for name, numerator_key, denominator_key in METRICS
        if any(numerator_key not in sample or denominator_key not in sample for sample in eligible)
    }
    totals = {name: 0 for name, _, _ in METRICS}
    numerators = {name: 0 for name, _, _ in METRICS}
    for sample in eligible:
        for name, numerator_key, denominator_key in METRICS:
            numerator = sample.get(numerator_key, 0)
            # Manual effort is measured in minutes and may be fractional for
            # short real runs. Preserve that precision; the other numerators
            # are event counts and remain integers.
            numerators[name] += (float(numerator) if numerator_key == "manual_minutes"
                                  else int(numerator))
            totals[name] += int(sample.get(denominator_key, 0))
    metrics = {
        name: {"numerator": numerators[name], "denominator": totals[name],
               "rate": (None if name in missing_metric_values
                        else _rate(numerators[name], totals[name]))}
        for name, _, _ in METRICS
    }
    missing_denominators = sorted(name for name, values in metrics.items()
                                  if values["denominator"] == 0 or name in missing_metric_values)
    minimum = int(window.get("minimum_reportable_samples", 0) or 0)
    process_incomplete = any(value != "observed" for value in window["process_evidence"].values())
    if process_incomplete or dependency_blocked:
        for values in metrics.values():
            values["rate"] = None
        missing_denominators = sorted(set(missing_denominators) | set(metrics))
    if missing_window:
        result, decision, readiness = "BLOCKED", "continue_trial", "NOT READY"
        reason = "window metadata is incomplete"
    elif p0_p1 or dependency_blocked or window.get("rollback_result") != "PASS":
        result, decision, readiness = "BLOCKED", "pause", "BLOCKED"
        reason = "unresolved P0/P1, blocked real dependency or unverified rollback"
    elif process_incomplete:
        result, decision, readiness = "BLOCKED", "continue_trial", "NOT READY"
        reason = "trial process evidence is incomplete"
    elif len(eligible) < minimum or missing_denominators:
        result, decision, readiness = "BLOCKED", "continue_trial", "NOT READY"
        reason = "sample or denominator minimum is not met"
    else:
        result, decision, readiness = "PASS", "expand", "READY"
        reason = "eligible real sample and denominator gates passed"
    evidence_attribution = (["fixture_or_test"] if readiness == "NOT READY"
                            else ["permission", "runtime"] if result == "BLOCKED"
                            else ["domain_logic"])
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
        "sample_versions": [{"sample_id": sample["sample_id"], "sample_version": sample["sample_version"],
                             "runtime_mode": sample["runtime_mode"],
                             "quality_result": sample["quality_result"],
                             "severity": sample.get("severity"),
                             "eligible": sample in eligible}
                            for sample in samples],
        "decision_evidence": {
            "real_sample_ids": [sample["sample_id"] for sample in real_samples],
            "eligible_real_sample_ids": [sample["sample_id"] for sample in eligible],
            "p0_p1_sample_ids": [sample["sample_id"] for sample in p0_p1],
            "p2_sample_ids": [sample["sample_id"] for sample in p2],
            "rollback_result": window["rollback_result"],
            "uncovered_risks": list(window["uncovered_risks"]),
        },
        "missing_values": missing_values,
        "missing_denominators": missing_denominators,
        "metrics": metrics,
        "quality_evidence": build_evidence(
            fixture_id=f"G6-BETA-{window.get('window_id', 'unknown')}", runtime_mode="real",
            result=result, input_value={"window": window, "samples": samples},
            output_value={"raw_counts": raw_counts, "metrics": metrics},
            attribution=evidence_attribution,
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
        "rollback_result": "BLOCKED", "uncovered_risks": ["no_samples"],
        "process_evidence": {"authorization": "not_run", "main_path": "not_run",
                              "failure_path": "not_run", "manual_review": "not_run",
                              "repair_or_rollback": "not_run", "key_scenario_rerun": "not_run"},
    })
    print(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True))
    return 0 if report["result"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
