"""Assemble the G6 gate reports into one deterministic, redacted run record."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import sys
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.g6_evidence import build_evidence, digest
from scripts.lingdoc_mock.g6_main_path import build_main_path_report
from scripts.lingdoc_mock.g6_metrics import build_beta_report
from scripts.lingdoc_mock.g6_observability import build_observability_report
from scripts.lingdoc_mock.g6_rollback import build_rollback_report
from scripts.lingdoc_mock.g6_security import build_security_report


def _read_json(path: Path | None, label: str, default: Any) -> Any:
    if path is None:
        return default
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError(f"cannot read {label}: {error}") from error
    return value


def _overall_result(results: list[str]) -> str:
    if "FAIL" in results:
        return "FAIL"
    if "BLOCKED" in results:
        return "BLOCKED"
    if "NOT RUN" in results:
        return "NOT RUN"
    return "PASS" if results and all(result == "PASS" for result in results) else "BLOCKED"


def _empty_f01_report() -> dict[str, Any]:
    return {"workflow": "F01", "runner_status": "not_started", "completed_steps": 0,
            "total_steps": 23, "steps": [], "verification_scope": "not_recorded"}


def build_g6_report(*, main_path_report: dict[str, Any] | None = None,
                    security_observations: dict[str, dict[str, Any]] | None = None,
                    events: list[dict[str, Any]] | None = None,
                    rollback_observations: dict[str, Any] | None = None,
                    samples: list[dict[str, Any]] | None = None,
                    window: dict[str, Any] | None = None,
                    runtime_mode: str = "mock") -> dict[str, Any]:
    gates = {
        "G6-01": (build_main_path_report(main_report=main_path_report or _empty_f01_report(),
                                           runtime_mode=runtime_mode)),
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
