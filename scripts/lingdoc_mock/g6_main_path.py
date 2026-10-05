"""G6 canonical main-path evidence wrapper around the existing F01 runner.

The wrapper does not create a second workflow. It derives coverage from the committed F01
workflow and requires a separate failure observation before declaring the complete G6 fixture
ready. That keeps a successful HTTP trace from masquerading as full failure-side-effect evidence.
"""
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
from scripts.lingdoc_mock.run_f01 import OPENAPI_PATH, WORKFLOW_PATH, ScenarioRunner, WorkflowError, write_report

REQUIRED_PARTS = ("project", "asset", "chapter", "check", "release", "export", "download")
OPERATION_PARTS = {
    "createProject": ("project",),
    "bindAsset": ("asset",),
    "saveChapter": ("chapter",),
    "confirmChapter": ("chapter",),
    "prepareRelease": ("check", "release"),
    "startExport": ("export",),
    "getExport": ("export",),
    "downloadExport": ("download",),
}


def _observed_result(report: dict[str, Any]) -> str:
    if report.get("runner_status") != "completed":
        return "BLOCKED"
    verdicts = [step.get("verdict") for step in report.get("steps", [])]
    if any(verdict == "failed" for verdict in verdicts):
        return "FAIL"
    return "PASS" if verdicts and all(verdict == "passed" for verdict in verdicts) else "BLOCKED"


def _coverage(report: dict[str, Any]) -> dict[str, Any]:
    operations = {step.get("operation_id") for step in report.get("steps", [])}
    constructed = sorted({part for operation, parts in OPERATION_PARTS.items() if operation in operations
                          for part in parts})
    missing = sorted(set(REQUIRED_PARTS) - set(constructed))
    return {
        "required_parts": list(REQUIRED_PARTS),
        "constructed_parts": constructed,
        "constructed_complete": not missing,
        "missing_parts": missing,
    }


def _failure_path(observation: dict[str, Any] | None) -> dict[str, Any]:
    if observation is None:
        return {
            "result": "BLOCKED",
            "status": "not_run",
            "no_formal_side_effect": None,
            "evidence_refs": ["failure_path"],
        }
    no_side_effect = observation.get("no_formal_side_effect") is True
    return {
        "result": "PASS" if no_side_effect else "FAIL",
        "status": "observed",
        "no_formal_side_effect": no_side_effect,
        "readback": observation.get("readback", "not_recorded"),
        "evidence_refs": ["failure_path", "side_effect_readback"],
    }


def build_main_path_report(*, main_report: dict[str, Any], runtime_mode: str = "mock",
                           failure_observation: dict[str, Any] | None = None,
                           fixture_id: str = "G6-F01-v1") -> dict[str, Any]:
    if main_report.get("workflow") != "F01":
        raise ValueError("G6 main path must be derived from the committed F01 workflow")
    coverage = _coverage(main_report)
    main_result = _observed_result(main_report)
    failure = _failure_path(failure_observation)
    result = main_result if main_result != "PASS" else failure["result"]
    if not coverage["constructed_complete"]:
        result = "BLOCKED"
    return {
        "report_version": 1,
        "scope": "canonical F01 project-to-download path plus one failure side-effect fixture",
        "fixture_id": fixture_id,
        "fixture_digest": digest({"fixture_id": fixture_id, "required_parts": REQUIRED_PARTS}),
        "runtime_mode": runtime_mode,
        "result": result,
        "fixture_coverage": coverage,
        "main_path": {
            "result": main_result,
            "completed_steps": main_report.get("completed_steps", 0),
            "total_steps": main_report.get("total_steps", 0),
            "verification_scope": main_report.get("verification_scope", "not_recorded"),
        },
        "failure_path": failure,
        "quality_evidence": build_evidence(
            fixture_id=fixture_id, runtime_mode=runtime_mode, result=result,
            input_value={"fixture_id": fixture_id, "coverage": coverage},
            output_value={"main_path": main_report, "failure_path": failure},
            attribution=["fixture_or_test"] if result == "BLOCKED" else ["domain_logic"],
            evidence_refs=["fixture_coverage", "main_path", "failure_path"],
            owner="quality-operations", reviewer="unassigned",
            fixture_digest=digest({"fixture_id": fixture_id, "required_parts": REQUIRED_PARTS}),
            input_summary={"required_parts": len(REQUIRED_PARTS)},
            output_summary={"completed_steps": main_report.get("completed_steps", 0),
                            "failure_side_effect_status": failure["status"]},
        ),
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run the canonical G6 F01 main path and write a quality report.")
    parser.add_argument("--base-url")
    parser.add_argument("--token")
    parser.add_argument("--runtime-mode", choices=("mock", "real_api_fake_model", "real"), default="mock")
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args(argv)
    runner = None
    try:
        runner = ScenarioRunner.from_files(OPENAPI_PATH, WORKFLOW_PATH, base_url=args.base_url, token=args.token)
        write_report(args.report, build_main_path_report(main_report=runner.report("not_started"),
                                                         runtime_mode=args.runtime_mode))
        result = runner.run()
        report = build_main_path_report(main_report=runner.report("completed"), runtime_mode=args.runtime_mode)
        write_report(args.report, report)
        print(f"{report['fixture_id']} {report['result']} report written to {args.report}")
        return 0 if report["result"] == "PASS" else 1
    except (WorkflowError, OSError) as error:
        if runner is not None:
            write_report(args.report, build_main_path_report(main_report=runner.report("failed"),
                                                             runtime_mode=args.runtime_mode))
        print(f"G6 main path FAILED: {error}")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
