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

from scripts.lingdoc_mock.g6_evidence import RUNTIME_MODES, build_evidence, digest, reduce_results
from scripts.lingdoc_mock.g6_main_path import build_main_path_report
from scripts.lingdoc_mock.g6_metrics import build_beta_report
from scripts.lingdoc_mock.g6_observability import build_observability_report
from scripts.lingdoc_mock.g6_rollback import build_rollback_report
from scripts.lingdoc_mock.g6_security import build_security_report
from scripts.lingdoc_mock.run_scenario import RUNTIME_DEPENDENCIES

SENSITIVE_KEYS = frozenset({"body", "content", "quoted_text", "source_text", "token",
                            "authorization", "prompt", "completion", "model_input"})
MATRIX_ID = re.compile(r"(?:[A-Z][A-Z0-9-]{1,31}:[A-Za-z0-9_.-]{1,64}|[a-z][a-z0-9_.-]*\.(?:id|key)|sha256:[0-9a-f]{64})")


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
    if (not isinstance(fixture_ids, list) or not fixture_ids
            or not all(isinstance(value, str) and MATRIX_ID.fullmatch(value) for value in fixture_ids)):
        shared_fixture = False
    dependency_matrix = matrix.get("dependency_matrix")
    if not isinstance(dependency_matrix, dict):
        dependency_matrix = {}
    normalized_dependencies = {}
    dependencies_verified = True
    for mode in RUNTIME_MODES:
        row = dependency_matrix.get(mode)
        if not isinstance(row, dict):
            dependencies_verified = False
            normalized_dependencies[mode] = {"required": [], "verified": [], "missing": [],
                                             "status": "not_verified"}
            continue
        required = row.get("required", [])
        verified = row.get("verified", [])
        missing = row.get("missing", [])
        status = row.get("status")
        allowed_dependencies = set(RUNTIME_DEPENDENCIES[mode])
        if (not all(isinstance(values, list) and all(isinstance(value, str) for value in values)
                    for values in (required, verified, missing))
                or status not in {"verified", "not_verified"}
                or any(value not in allowed_dependencies for value in [*required, *verified, *missing])):
            dependencies_verified = False
        required = [value for value in required if value in allowed_dependencies]
        verified = [value for value in verified if value in allowed_dependencies]
        missing = [value for value in missing if value in allowed_dependencies]
        if set(required) != allowed_dependencies or status != "verified" or missing or set(required) != set(verified):
            dependencies_verified = False
        normalized_dependencies[mode] = {
            "required": sorted(required), "verified": sorted(verified),
            "missing": sorted(missing), "status": status if status in {"verified", "not_verified"}
            else "not_verified",
        }
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
        mode_results.append(entry["result"])
        semantics = entry.get("provider_semantics_status", "not_verified")
        if semantics not in {"verified", "not_verified"}:
            semantics = "not_verified"
        dependency_status = entry.get("dependency_status", "not_verified")
        if dependency_status not in {"verified", "not_verified"}:
            dependency_status = "not_verified"
        if mode != "mock" and semantics != "verified":
            semantics_verified = False
        normalized_modes.append({
            "runtime_mode": mode,
            "result": entry["result"],
            "dependency_status": dependency_status,
            "provider_semantics_status": semantics,
            "verification_scope": (entry.get("verification_scope")
                                   if entry.get("verification_scope") in {"http_smoke_only",
                                                                          "http_smoke_plus_declared_f02_white_box",
                                                                          "scenario_runner_matrix",
                                                                          "observed"}
                                   else "not_recorded"),
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
            output_value={"result": result, "dependency_matrix": matrix.get("dependency_matrix", {})},
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
                    security_observations: dict[str, dict[str, Any]] | None = None,
                    events: list[dict[str, Any]] | None = None,
                    rollback_observations: dict[str, Any] | None = None,
                    runtime_matrix_report: dict[str, Any] | None = None,
                    samples: list[dict[str, Any]] | None = None,
                    window: dict[str, Any] | None = None,
                    runtime_mode: str = "mock") -> dict[str, Any]:
    gates = {
        "G6-01": (build_main_path_report(main_report=main_path_report or _empty_f01_report(),
                                           runtime_mode=runtime_mode)),
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
    parser.add_argument("--security-observations", type=Path)
    parser.add_argument("--events", type=Path)
    parser.add_argument("--rollback-observations", type=Path)
    parser.add_argument("--runtime-matrix", type=Path)
    parser.add_argument("--samples", type=Path)
    parser.add_argument("--window", type=Path)
    parser.add_argument("--runtime-mode", choices=("mock", "real_api_fake_model", "real"), default="mock")
    args = parser.parse_args(argv)
    try:
        report = build_g6_report(
            main_path_report=_read_json(args.main_path_report, "main path report", None),
            security_observations=_read_json(args.security_observations, "security observations", None),
            events=_read_json(args.events, "events", None),
            rollback_observations=_read_json(args.rollback_observations, "rollback observations", None),
            runtime_matrix_report=_read_json(args.runtime_matrix, "runtime matrix", None),
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
