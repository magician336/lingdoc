"""Assemble the G6 gate reports into one deterministic, redacted run record."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
import sys
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.g6_evidence import (RUNTIME_DEPENDENCIES, RUNTIME_MODES, SAFE_FIXTURE_ID,
                                               build_evidence, digest,
                                               main_path_report_to_scenario_evidence,
                                               normalize_scenario_evidence, reduce_results)
from scripts.lingdoc_mock.g6_evidence import SENSITIVE_VALUE_PATTERN
from scripts.lingdoc_mock.g6_main_path import build_main_path_report
from scripts.lingdoc_mock.g6_metrics import build_beta_report
from scripts.lingdoc_mock.g6_observability import build_observability_report
from scripts.lingdoc_mock.g6_rollback import build_rollback_report
from scripts.lingdoc_mock.g6_security import build_security_report

SENSITIVE_KEYS = frozenset({"body", "content", "quoted_text", "source_text", "token",
                            "authorization", "prompt", "completion", "model_input"})
MATRIX_ID = SAFE_FIXTURE_ID
SAFE_VERSION_KEY = re.compile(r"[A-Za-z][A-Za-z0-9_.-]{0,63}")
SAFE_VERSION_VALUE = re.compile(r"(?:[A-Za-z][A-Za-z0-9_.:-]{0,127}|[0-9a-f]{64}|sha256:[0-9a-f]{64})")
SENSITIVE_VERSION_KEYS = frozenset({"token", "authorization", "secret", "password", "cookie", "api_key"})


def _read_json(path: Path | None, label: str, default: Any) -> Any:
    if path is None:
        return default
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError(f"cannot read {label}: {error}") from error
    return value


def _overall_result(results: list[str]) -> str:
    return reduce_results(results)


def _empty_f01_report() -> dict[str, Any]:
    return {"workflow": "F01", "runner_status": "not_started", "completed_steps": 0,
            "total_steps": 23, "steps": [], "verification_scope": "not_recorded"}


def _empty_runtime_matrix() -> dict[str, Any]:
    """Return an explicit G6-02 blocked record when no matrix was supplied.

    The unified runner must show that G6-02 was considered.  It must not silently
    omit the gate merely because driving the provider requires an external service.
    """
    dependencies = {
        "mock": {"required": ["contract"], "verified": [], "missing": ["contract"],
                 "status": "not_verified"},
        "real_api_fake_model": {"required": ["api", "permissions", "queue", "file"],
                                 "verified": [],
                                 "missing": ["api", "permissions", "queue", "file"],
                                 "status": "not_verified"},
        "real": {"required": ["api", "permissions", "queue", "file", "model", "weknora", "docx"],
                  "verified": [],
                  "missing": ["api", "permissions", "queue", "file", "model", "weknora", "docx"],
                  "status": "not_verified"},
    }
    return {
        "report_version": 1,
        "scope": "same scenarios driven independently for each runtime mode",
        "runtime_modes": list(RUNTIME_MODES),
        "result": "BLOCKED",
        "shared_fixture": False,
        "fixture_ids": [],
        "dependency_matrix": dependencies,
        "modes": [],
        "reports": [],
        "reason": "runtime matrix was not supplied by the scenario runner",
    }


def _dependency_row_complete(row: Any, runtime_mode: str) -> bool:
    """Return whether a mode's dependency observation covers its full contract."""
    if not isinstance(row, dict) or row.get("status") != "verified":
        return False
    required = row.get("required")
    verified = row.get("verified")
    missing = row.get("missing")
    if not all(isinstance(values, list) and all(isinstance(value, str) for value in values)
               for values in (required, verified, missing)):
        return False
    expected = set(RUNTIME_DEPENDENCIES[runtime_mode])
    return set(required) == expected and set(verified) == expected and not missing


def _normalize_dependency_matrix(value: Any) -> dict[str, dict[str, Any]]:
    """Keep only dependency status fields allowed in a runtime matrix."""
    normalized: dict[str, dict[str, Any]] = {}
    for mode in RUNTIME_MODES:
        row = value.get(mode) if isinstance(value, dict) else None
        if not isinstance(row, dict):
            normalized[mode] = {"required": [], "verified": [], "missing": [],
                                "status": "not_verified"}
            continue
        required = row.get("required", [])
        verified = row.get("verified", [])
        missing = row.get("missing", [])
        status = row.get("status")
        allowed = set(RUNTIME_DEPENDENCIES[mode])
        valid_shape = (
            all(isinstance(items, list) and all(isinstance(item, str) for item in items)
                for items in (required, verified, missing))
            and status in {"verified", "not_verified"}
            and all(item in allowed for item in [*required, *verified, *missing])
        )
        required = sorted({item for item in required if item in allowed}) if isinstance(required, list) else []
        verified = sorted({item for item in verified if item in allowed}) if isinstance(verified, list) else []
        missing = sorted({item for item in missing if item in allowed}) if isinstance(missing, list) else []
        complete = (valid_shape and set(required) == allowed and set(verified) == allowed
                    and not missing and status == "verified")
        normalized[mode] = {
            "required": required,
            "verified": verified,
            "missing": missing,
            "status": "verified" if complete else "not_verified",
        }
    return normalized


def _safe_version_map(value: Any) -> tuple[dict[str, str], bool]:
    """Normalize version labels and report whether the source had a safe shape."""
    if not isinstance(value, dict) or not value:
        return {"status": "not_observed"}, False
    safe: dict[str, str] = {}
    valid = True
    for key, item in value.items():
        if (not isinstance(key, str) or key.lower() in SENSITIVE_VERSION_KEYS
                or not SAFE_VERSION_KEY.fullmatch(key)
                or not isinstance(item, str) or not SAFE_VERSION_VALUE.fullmatch(item)
                or SENSITIVE_VALUE_PATTERN.search(item)):
            valid = False
            continue
        safe[key] = item
    observed = any(key != "status" and item not in {"not_observed", "unknown", "not_run"}
                   for key, item in safe.items())
    return (safe or {"status": "not_observed"}), valid and observed


def _report_versions(report: dict[str, Any]) -> tuple[dict[str, str], bool, dict[str, str], bool]:
    """Extract redacted version maps from a sanitized F01 report."""
    quality = report.get("quality_evidence")
    if isinstance(quality, list):
        quality = quality[0] if quality and isinstance(quality[0], dict) else {}
    if not isinstance(quality, dict):
        quality = {}
    dependency = report.get("dependency_versions", quality.get("dependency_versions"))
    environment = report.get("environment_versions", quality.get("environment_versions"))
    dependency_safe, dependency_valid = _safe_version_map(dependency)
    environment_safe, environment_valid = _safe_version_map(environment)
    return dependency_safe, dependency_valid, environment_safe, environment_valid


def build_runtime_matrix_from_main_path_reports(
        reports: dict[str, dict[str, Any]],
        dependency_matrix: dict[str, dict[str, Any]] | None = None,
        provider_semantics: dict[str, str] | None = None) -> dict[str, Any]:
    """Assemble a G6-02 matrix directly from one sanitized F01 report per runtime mode."""
    if not isinstance(reports, dict) or set(reports) != set(RUNTIME_MODES):
        raise ValueError("main path reports must contain mock, real_api_fake_model and real")
    dependencies = dependency_matrix or {
        mode: {"required": list(RUNTIME_DEPENDENCIES[mode]), "verified": [],
               "missing": list(RUNTIME_DEPENDENCIES[mode]), "status": "not_verified"}
        for mode in RUNTIME_MODES
    }
    if not isinstance(dependencies, dict):
        raise ValueError("dependency matrix must be an object keyed by runtime mode")
    dependencies = _normalize_dependency_matrix(dependencies)
    modes = []
    fixture_ids = []
    for mode in RUNTIME_MODES:
        report = reports[mode]
        if not isinstance(report, dict):
            raise ValueError(f"main path report for {mode} must be an object")
        report_mode = report.get("runtime_mode")
        result = report.get("result") if report.get("result") in {"PASS", "FAIL", "NOT RUN", "BLOCKED"} else "BLOCKED"
        evidence = main_path_report_to_scenario_evidence(report)
        dependency_versions, dependency_versions_valid, environment_versions, environment_versions_valid = _report_versions(report)
        fixture_id = evidence["main_path"].get("fixture_id")
        if fixture_id:
            fixture_ids.append(fixture_id)
        main_scope = evidence["main_path"].get("verification_scope", "not_recorded")
        modes.append({
            "runtime_mode": mode,
            "result": result if report_mode in {None, mode} else "BLOCKED",
            "dependency_status": (dependencies.get(mode, {}) or {}).get("status", "not_verified"),
            "provider_semantics_status": (provider_semantics or {}).get(mode, "not_verified"),
            "dependency_versions": dependency_versions,
            "environment_versions": environment_versions,
            "version_evidence_valid": dependency_versions_valid and environment_versions_valid,
            "verification_scope": main_scope,
            "scenario_evidence": evidence,
            "quality_evidence": {"result": result, "fixture_id": fixture_id},
            "summary": {"main_path_result": evidence["main_path"]["result"],
                        "failure_path_result": evidence["key_failure"]["result"]},
        })
    unique_fixture_ids = sorted(set(fixture_ids))
    for mode in modes:
        scenario_evidence, scenario_result = normalize_scenario_evidence(
            mode, unique_fixture_ids, mode["runtime_mode"])
        mode["scenario_evidence"] = scenario_evidence
        mode["result"] = reduce_results([mode["result"], scenario_result])
        dependency = dependencies.get(mode["runtime_mode"], {})
        dependency_complete = _dependency_row_complete(dependency, mode["runtime_mode"])
        if not dependency_complete:
            mode["result"] = "BLOCKED" if mode["result"] != "FAIL" else "FAIL"
        provider_status = (provider_semantics or {}).get(mode["runtime_mode"], "not_verified")
        if mode["runtime_mode"] != "mock" and provider_status != "verified":
            mode["result"] = "BLOCKED" if mode["result"] != "FAIL" else "FAIL"
        if mode["runtime_mode"] != "mock" and not mode.get("version_evidence_valid", False):
            mode["result"] = "BLOCKED" if mode["result"] != "FAIL" else "FAIL"
        mode["dependency_status"] = "verified" if dependency_complete else "not_verified"
        mode["provider_semantics_status"] = (provider_status
                                               if provider_status in {"verified", "not_verified"}
                                               else "not_verified")
        mode["quality_evidence"]["result"] = mode["result"]
        mode["summary"] = {
            "main_path_result": scenario_evidence["main_path"]["result"],
            "failure_path_result": scenario_evidence["key_failure"]["result"],
        }
    result = reduce_results([mode["result"] for mode in modes])
    return {
        "report_version": 1,
        "scope": "same F01 main path reports independently for each runtime mode",
        "runtime_modes": list(RUNTIME_MODES),
        "result": result,
        "shared_fixture": len(unique_fixture_ids) == 1 and len(fixture_ids) == len(RUNTIME_MODES),
        "fixture_ids": unique_fixture_ids,
        "dependency_matrix": dependencies,
        "modes": modes,
        "reports": [{"runtime_mode": mode, "report": {"result": modes[index]["result"],
                                                        "fixture_id": modes[index]["scenario_evidence"]["main_path"].get("fixture_id")}}
                    for index, mode in enumerate(RUNTIME_MODES)],
    }


def _runtime_matrix_gate(matrix: dict[str, Any] | None) -> dict[str, Any]:
    """Wrap a scenario-runner matrix in the common G6 evidence shape."""
    matrix = matrix or _empty_runtime_matrix()
    if not isinstance(matrix, dict):
        raise ValueError("runtime matrix report must be an object")
    _reject_sensitive(matrix)
    supplied_result = matrix.get("result", "BLOCKED")
    if supplied_result not in {"PASS", "FAIL", "NOT RUN", "BLOCKED"}:
        raise ValueError("runtime matrix result is invalid")
    modes = matrix.get("runtime_modes")
    if (not isinstance(modes, list) or not modes
            or any(mode not in RUNTIME_MODES for mode in modes)
            or len(modes) != len(set(modes))):
        raise ValueError("runtime matrix must list valid runtime modes")
    all_modes_present = sorted(modes) == sorted(RUNTIME_MODES)
    shared_fixture = matrix.get("shared_fixture") is True and all_modes_present
    fixture_ids = matrix.get("fixture_ids", [])
    valid_fixture_ids = ([value for value in fixture_ids
                          if isinstance(value, str) and MATRIX_ID.fullmatch(value)]
                         if isinstance(fixture_ids, list) else [])
    if not valid_fixture_ids or len(valid_fixture_ids) != (len(fixture_ids) if isinstance(fixture_ids, list) else 0):
        shared_fixture = False
    fixture_ids = sorted(set(valid_fixture_ids))
    dependency_matrix = matrix.get("dependency_matrix")
    if not isinstance(dependency_matrix, dict):
        dependency_matrix = {}
    normalized_dependencies = _normalize_dependency_matrix(dependency_matrix)
    dependencies_verified = all(
        _dependency_row_complete(normalized_dependencies[mode], mode)
        for mode in RUNTIME_MODES
    )
    mode_entries = matrix.get("modes")
    normalized_modes = []
    mode_results = []
    semantics_verified = True
    if not isinstance(mode_entries, list) or len(mode_entries) != len(RUNTIME_MODES):
        mode_entries = []
    by_mode = {entry.get("runtime_mode"): entry for entry in mode_entries
               if isinstance(entry, dict)}
    if set(by_mode) != set(RUNTIME_MODES):
        shared_fixture = False
    for mode in RUNTIME_MODES:
        entry = by_mode.get(mode)
        if not entry or entry.get("result") not in {"PASS", "FAIL", "NOT RUN", "BLOCKED"}:
            shared_fixture = False
            mode_results.append("BLOCKED")
            continue
        scenario_evidence, scenario_result = normalize_scenario_evidence(entry, fixture_ids, mode)
        mode_result = reduce_results([entry["result"], scenario_result])
        semantics = entry.get("provider_semantics_status", "not_verified")
        if semantics not in {"verified", "not_verified"}:
            semantics = "not_verified"
        dependency_status = entry.get("dependency_status", "not_verified")
        if dependency_status not in {"verified", "not_verified"}:
            dependency_status = "not_verified"
        dependency_status = ("verified" if dependency_status == "verified"
                             and normalized_dependencies[mode]["status"] == "verified"
                             else "not_verified")
        if dependency_status != "verified":
            mode_result = "BLOCKED" if mode_result != "FAIL" else "FAIL"
        if mode != "mock" and semantics != "verified":
            semantics_verified = False
            if mode_result != "FAIL":
                mode_result = "BLOCKED"
        dependency_versions, dependency_versions_valid = _safe_version_map(
            entry.get("dependency_versions"))
        environment_versions, environment_versions_valid = _safe_version_map(
            entry.get("environment_versions"))
        if mode != "mock" and not (dependency_versions_valid and environment_versions_valid):
            if mode_result != "FAIL":
                mode_result = "BLOCKED"
        mode_results.append(mode_result)
        normalized_modes.append({
            "runtime_mode": mode,
            "result": mode_result,
            "dependency_status": dependency_status,
            "provider_semantics_status": semantics,
            "dependency_versions": dependency_versions,
            "environment_versions": environment_versions,
            "verification_scope": (entry.get("verification_scope")
                                   if entry.get("verification_scope") in {"http_smoke_only",
                                                                          "http_smoke_plus_declared_f02_white_box",
                                                                          "scenario_runner_matrix",
                                                                          "observed"}
                                   else "not_recorded"),
            "scenario_evidence": scenario_evidence,
        })
    result = reduce_results(mode_results)
    if supplied_result == "FAIL":
        result = "FAIL"
    elif (not shared_fixture or not dependencies_verified or not semantics_verified
          or result != "PASS"):
        result = "BLOCKED" if result != "FAIL" else "FAIL"
    return {
        "report_version": 1,
        "scope": "same scenarios driven independently for each runtime mode",
        "runtime_modes": list(RUNTIME_MODES),
        "result": result,
        "shared_fixture": shared_fixture,
        "fixture_ids": sorted(fixture_ids) if isinstance(fixture_ids, list) else [],
        "dependency_matrix": normalized_dependencies,
        "modes": normalized_modes,
        "verification_scope": "scenario_runner_matrix",
        "quality_evidence": build_evidence(
            fixture_id="G6-RUNTIME-MATRIX-01", runtime_mode="mock", result=result,
            input_value={"runtime_modes": modes, "fixture_ids": matrix.get("fixture_ids", [])},
            output_value={"result": result, "dependency_matrix": normalized_dependencies,
                          "modes": normalized_modes},
            attribution=["runtime"] if result in {"BLOCKED", "NOT RUN"} else ["domain_logic"],
            evidence_refs=["runtime_modes", "dependency_matrix", "modes"],
            owner="quality-operations", reviewer="unassigned",
            fixture_digest=digest({"fixture": "G6-RUNTIME-MATRIX-01", "modes": modes}),
            input_summary={"mode_count": len(modes)}, output_summary={"result": result},
        ),
    }


def _reject_sensitive(value: Any) -> None:
    """Reject raw content before an externally supplied matrix enters the report."""
    if isinstance(value, dict):
        leaked = SENSITIVE_KEYS.intersection(key.lower() for key in value)
        if leaked:
            raise ValueError("runtime matrix contains sensitive fields: " + ", ".join(sorted(leaked)))
        for child in value.values():
            _reject_sensitive(child)
    elif isinstance(value, list):
        for child in value:
            _reject_sensitive(child)


def build_g6_report(*, main_path_report: dict[str, Any] | None = None,
                    failure_observation: dict[str, Any] | None = None,
                    security_observations: dict[str, dict[str, Any]] | None = None,
                    events: list[dict[str, Any]] | None = None,
                    rollback_observations: dict[str, Any] | None = None,
                    runtime_matrix_report: dict[str, Any] | None = None,
                    samples: list[dict[str, Any]] | None = None,
                    window: dict[str, Any] | None = None,
                    runtime_mode: str = "mock") -> dict[str, Any]:
    gates = {
        "G6-01": (build_main_path_report(main_report=main_path_report or _empty_f01_report(),
                                           runtime_mode=runtime_mode,
                                           failure_observation=failure_observation)),
        "G6-02": _runtime_matrix_gate(runtime_matrix_report),
        "G6-03": build_security_report(runtime_mode=runtime_mode, observations=security_observations),
        "G6-04": build_observability_report(events=events, runtime_mode=runtime_mode),
        "G6-05": build_rollback_report(observations=rollback_observations, runtime_mode=runtime_mode),
    }
    if window is None:
        window = {
            "window_id": "window.id", "target_samples": 0, "minimum_reportable_samples": 1,
            "template_version": "template.v1", "ruleset_hash": "rules.key", "redaction": "fixture-v1",
            "included_projects": [], "included_users": [], "runtime_modes": ["real"],
            "rollback_result": "unknown", "uncovered_risks": ["no_window_observation"],
            "process_evidence": {"authorization": "not_run", "main_path": "not_run",
                                  "failure_path": "not_run", "manual_review": "not_run",
                                  "repair_or_rollback": "not_run", "key_scenario_rerun": "not_run"},
        }
    gates["G6-06"] = build_beta_report(samples=samples or [], window=window)
    results = [gate["result"] for gate in gates.values()]
    result = _overall_result(results)
    return {
        "report_version": 1,
        "scope": "G6 quality, runtime, security, rollback and Beta decision gates",
        "runtime_mode": runtime_mode,
        "result": result,
        "gate_results": {name: gate["result"] for name, gate in gates.items()},
        "gates": gates,
        "quality_evidence": build_evidence(
            fixture_id="G6-RUN-01", runtime_mode=runtime_mode, result=result,
            input_value={"gate_names": sorted(gates), "runtime_mode": runtime_mode},
            output_value={"gate_results": {name: gate["result"] for name, gate in gates.items()}},
            attribution=["runtime"] if result == "BLOCKED" else ["domain_logic"],
            evidence_refs=[f"gates.{name}" for name in sorted(gates)],
            owner="quality-operations", reviewer="unassigned",
            fixture_digest=digest({"fixture": "G6-RUN-01", "gates": sorted(gates)}),
            input_summary={"gate_count": len(gates)},
            output_summary={"gate_results": {name: gate["result"] for name, gate in gates.items()}},
        ),
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Assemble one redacted G6 gate report.")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--main-path-report", type=Path)
    parser.add_argument("--failure-observation", type=Path,
                        help="脱敏的关键失败副作用读回 JSON，与 --main-path-report 一起使用")
    parser.add_argument("--security-observations", type=Path)
    parser.add_argument("--events", type=Path)
    parser.add_argument("--rollback-observations", type=Path)
    parser.add_argument("--runtime-matrix", type=Path)
    parser.add_argument("--samples", type=Path)
    parser.add_argument("--window", type=Path)
    parser.add_argument("--main-path-reports", type=Path,
                        help="JSON object keyed by runtime mode containing sanitized F01 reports")
    parser.add_argument("--runtime-dependencies", type=Path,
                        help="optional dependency_matrix JSON used with --main-path-reports")
    parser.add_argument("--provider-semantics", type=Path,
                        help="optional provider semantics JSON keyed by runtime mode")
    parser.add_argument("--runtime-mode", choices=RUNTIME_MODES, default="mock")
    args = parser.parse_args(argv)
    try:
        if args.main_path_reports and args.runtime_matrix:
            raise ValueError("--main-path-reports and --runtime-matrix are mutually exclusive")
        runtime_matrix = _read_json(args.runtime_matrix, "runtime matrix", None)
        if args.main_path_reports:
            main_path_reports = _read_json(args.main_path_reports, "main path reports", None)
            dependencies = _read_json(args.runtime_dependencies, "runtime dependencies", None)
            semantics = _read_json(args.provider_semantics, "provider semantics", None)
            if dependencies is not None and not isinstance(dependencies, dict):
                raise ValueError("runtime dependencies must be an object")
            if semantics is not None and not isinstance(semantics, dict):
                raise ValueError("provider semantics must be an object")
            runtime_matrix = build_runtime_matrix_from_main_path_reports(
                main_path_reports, dependency_matrix=dependencies, provider_semantics=semantics)
        report = build_g6_report(
            main_path_report=_read_json(args.main_path_report, "main path report", None),
            failure_observation=_read_json(args.failure_observation, "failure observation", None),
            security_observations=_read_json(args.security_observations, "security observations", None),
            events=_read_json(args.events, "events", None),
            rollback_observations=_read_json(args.rollback_observations, "rollback observations", None),
            runtime_matrix_report=runtime_matrix,
            samples=_read_json(args.samples, "samples", None),
            window=_read_json(args.window, "window", None),
            runtime_mode=args.runtime_mode,
        )
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
                               encoding="utf-8")
        print(f"G6 {report['result']} report written to {args.output}")
        return 0 if report["result"] == "PASS" else 1
    except (OSError, ValueError, TypeError) as error:
        print(f"G6 FAILED: {error}")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
