#!/usr/bin/env python3
"""Consolidate the committed T15 scenario evidence into one deterministic matrix.

This command reads saved evidence only. It does not start a service, send requests,
re-run a model, or turn a missing observation into a pass.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import sys
from typing import Any, Iterable


ROOT = Path(__file__).resolve().parents[2]
CONTRACT_REL = "docs/08-本轮实施方案/contracts/scenarios.json"
REPORT_REL = "docs/08-本轮实施方案/T15-11-验证报告.json"
EVIDENCE_REL = "docs/08-本轮实施方案/T15-11-证据.json"

# Each mode deliberately selects only the scenarios this saved artifact actually ran.
# The generic scenario runner reports 23 rows, most of them not_run, on every partial run;
# those unselected rows must never overwrite results from another ticket's run.
# F23 is the T16 acceptance-budget/model-boundary scenario. Its evidence is the Go regression
# suite, so it is intentionally outside this saved T15 HTTP matrix (which remains F01-F22).
T15_SCENARIO_IDS = {f"F{i:02d}" for i in range(1, 23)}
SOURCE_REPORTS = (
    ("docs/08-本轮实施方案/T15-S5-证据.json", "f22_evidence"),
    ("docs/08-本轮实施方案/T15-S6-验证报告.json", "executed"),
    ("docs/08-本轮实施方案/T15-S7-验证报告.json", "executed"),
    ("docs/08-本轮实施方案/T15-S8-验证报告.json", "executed"),
    ("docs/08-本轮实施方案/T15-S9-验证报告.json", "scenario_map"),
    ("docs/08-本轮实施方案/T15-09-验证报告.json", "scenario_map"),
    ("docs/08-本轮实施方案/T15-10-验证报告.json", "scenario_map"),
)

SUPPORT_FILES = (
    CONTRACT_REL,
    "docs/TEAM_DEVELOPMENT_TASKS.md",
    "docs/08-本轮实施方案/T15-真实服务启动.md",
    "docs/08-本轮实施方案/T15-场景到报告.md",
    "docs/08-本轮实施方案/T15-F02-白盒观察.md",
    "docs/08-本轮实施方案/02-接口与Mock约定.md",
)

VERDICT_ORDER = ("passed", "failed", "not_run")
VERDICTS = set(VERDICT_ORDER)

F01_NOT_RUN = {
    "scenario": "F01",
    "verdict": "not_run",
    "reason": (
        "T15-01 只证明本机真实服务接受了请求，没有运行完整的 23 步 F01；"
        "完整页面组合与 F01 接真属于任务池 T16。"
    ),
    "source_artifacts": [
        "docs/08-本轮实施方案/T15-真实服务启动.md",
        "docs/TEAM_DEVELOPMENT_TASKS.md",
    ],
}

RED_ITEM_OWNERS: dict[str, dict[str, str]] = {
    "T15-04-R1": {
        "owner_task": "T10",
        "owner": "T10 generation task owner; no named assignee",
        "owner_assignment_status": "unassigned",
    },
    "T15-08-R1": {
        "owner_task": "T08 with T09 authorization support",
        "owner": "T08 workspace chapter-read task owner; no named assignee",
        "owner_assignment_status": "unassigned",
    },
    "T15-08-R2": {
        "owner_task": "T14",
        "owner": "T14 delivery/export task owner; no named assignee",
        "owner_assignment_status": "unassigned",
    },
    "T15-09-R1": {
        "owner_task": "T14",
        "owner": "T14 delivery/export task owner; no named assignee",
        "owner_assignment_status": "unassigned",
    },
    "T15-10-R1": {
        "owner_task": "T09",
        "owner": "T09 asset/retrieval task owner; no named assignee",
        "owner_assignment_status": "unassigned",
    },
    "T15-10-R2": {
        "owner_task": "T12",
        "owner": "T12 chapter-confirmation task owner; no named assignee",
        "owner_assignment_status": "unassigned",
    },
    "T15-10-R3": {
        "owner_task": "T12/T13",
        "owner": "T12/T13 confirmation and frozen-snapshot task owners; no named assignee",
        "owner_assignment_status": "unassigned",
    },
    "F15-MODEL-OUTPUT": {
        "owner_task": "T10",
        "owner": "T10 generation/model-prompt task owner; no named assignee",
        "owner_assignment_status": "unassigned",
    },
}

GAP_POLICIES: dict[str, dict[str, Any]] = {
    "T15-04-R1": {
        "scenario_ids": ["F03", "F22"],
        "status": "open",
    },
    "T15-08-R1": {
        "scenario_ids": ["F07"],
        "status": "open",
    },
    "T15-08-R2": {
        "scenario_ids": ["F07", "F19"],
        "status": "open",
    },
    "T15-09-R1": {
        "scenario_ids": ["F19"],
        "status": "open",
        "related_gap_id": "T15-08-R2",
    },
    "T15-10-R1": {
        "scenario_ids": ["F03"],
        "status": "open",
    },
    "T15-10-R2": {
        "scenario_ids": ["F15"],
        "status": "fix_present_retest_pending",
        "remediation_commit": "52651d8",
    },
    "T15-10-R3": {
        "scenario_ids": ["F15"],
        "status": "fix_present_retest_pending",
        "remediation_commit": "52651d8",
    },
}

FAILED_STEP_OWNERS: dict[tuple[str, str], dict[str, str]] = {
    ("F07", "chapters"): {
        "red_item_id": "T15-08-R1",
    },
    ("F07", "export_download"): {
        "red_item_id": "T15-08-R2",
    },
    ("F15", "candidate-body-has-no-warning"): {
        "red_item_id": "F15-MODEL-OUTPUT",
    },
    ("F15", "confirm-without-decisions"): {
        "red_item_id": "T15-10-R2",
    },
    ("F15", "missing-review-decision-rejected"): {
        "red_item_id": "T15-10-R2",
    },
    ("F15", "frozen-confirmation-binds-to-frozen-chapter"): {
        "red_item_id": "T15-10-R3",
    },
    ("F15", "snapshot-check-passed"): {
        "red_item_id": "T15-10-R3",
    },
    ("F19", "retry-same-download-after-revocation"): {
        "red_item_id": "T15-09-R1",
    },
}

BOUNDARIES = [
    {
        "id": "source-text",
        "statement": (
            "Source context expands text stored in the chunk table; it does not re-parse the source file "
            "to recover exact original text."
        ),
    },
    {
        "id": "browser-real-backend",
        "statement": "The UI has not been exercised in a browser against the real backend.",
    },
    {
        "id": "model-and-async",
        "statement": (
            "A saved HTTP result does not establish model quality, unobserved worker effects, or "
            "unexecuted race/timeout paths."
        ),
    },
    {
        "id": "f01",
        "statement": "The full 23-step F01 remains outside this matrix run and belongs to T16.",
    },
]


class MatrixError(ValueError):
    """Raised when source evidence cannot safely produce a complete matrix."""


def _canonical_source_bytes(path: Path) -> bytes:
    raw = path.read_bytes()
    return raw.replace(b"\r\n", b"\n").replace(b"\r", b"\n")


def _read_json(root: Path, relative_path: str) -> dict[str, Any]:
    path = root / relative_path
    try:
        value = json.loads(_canonical_source_bytes(path).decode("utf-8-sig"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise MatrixError(f"cannot read JSON evidence {relative_path}: {error}") from error
    if not isinstance(value, dict):
        raise MatrixError(f"JSON evidence is not an object: {relative_path}")
    return value


def _source_digest(root: Path, relative_path: str) -> str:
    try:
        canonical = _canonical_source_bytes(root / relative_path)
    except OSError as error:
        raise MatrixError(f"cannot read source artifact {relative_path}: {error}") from error
    return hashlib.sha256(canonical).hexdigest()


def _scenario_entries(document: dict[str, Any]) -> list[dict[str, Any]]:
    entries = document.get("scenarios")
    if isinstance(entries, list):
        values = entries
    elif isinstance(entries, dict):
        values = list(entries.values())
    else:
        raise MatrixError("contract has no scenarios list or mapping")
    normalized = [entry for entry in values if isinstance(entry, dict) and entry.get("id")]
    if len(normalized) != len(values):
        raise MatrixError("contract contains a scenario without an id")
    return normalized


def _report_records(document: dict[str, Any], mode: str, source: str) -> list[dict[str, Any]]:
    if mode == "f22_evidence":
        scenario = document.get("scenario")
        step_result = document.get("step_result")
        if not isinstance(scenario, dict) or not isinstance(step_result, dict):
            raise MatrixError(f"F22 evidence is missing scenario or step_result: {source}")
        values = [{"scenario": scenario.get("id"), **step_result}]
    elif mode == "executed":
        values = document.get("executed")
        if isinstance(values, dict):
            values = [values]
        if not isinstance(values, list):
            raise MatrixError(f"report has no executed scenario records: {source}")
    elif mode == "scenario_map":
        mapped = document.get("scenarios")
        if isinstance(mapped, dict):
            values = [dict(value, scenario=value.get("scenario", key))
                      for key, value in mapped.items() if isinstance(value, dict)]
        elif isinstance(mapped, list):
            values = mapped
        else:
            raise MatrixError(f"report has no scenario results: {source}")
        contract_runner = document.get("contract_runner")
        executions = contract_runner.get("executed", []) if isinstance(contract_runner, dict) else []
        executions_by_scenario = {
            record.get("scenario"): record
            for record in executions
            if isinstance(record, dict) and isinstance(record.get("scenario"), str)
        } if isinstance(executions, list) else {}
        merged_values = []
        for value in values:
            scenario_id = value.get("scenario") or value.get("id")
            steps = value.get("steps")
            if not isinstance(steps, list):
                steps = executions_by_scenario.get(scenario_id, {}).get("steps", [])
            merged_values.append({**value, "steps": steps})
        values = merged_values
    else:
        raise MatrixError(f"unknown source report mode {mode!r}")

    records: list[dict[str, Any]] = []
    for value in values:
        if not isinstance(value, dict):
            raise MatrixError(f"scenario result is not an object: {source}")
        scenario_id = value.get("scenario") or value.get("id")
        verdict = value.get("verdict")
        if not isinstance(scenario_id, str) or verdict not in VERDICTS:
            raise MatrixError(f"invalid scenario result in {source}: {scenario_id!r} / {verdict!r}")
        steps = value.get("steps")
        if steps is None and mode == "f22_evidence":
            steps = [value]
        if not isinstance(steps, list):
            steps = []
        record: dict[str, Any] = {
            "scenario": scenario_id,
            "verdict": verdict,
            "source_artifacts": [source],
            "observed_steps": len(steps),
            "failed_steps": _failed_steps(scenario_id, steps),
        }
        reason = value.get("reason")
        if isinstance(reason, str) and reason:
            record["reason"] = reason
        records.append(record)
    return records


def _step_name(step: dict[str, Any]) -> str:
    for key in ("step", "step_id", "id"):
        value = step.get(key)
        if isinstance(value, str):
            return value
    return "unknown-step"


def _failed_steps(scenario_id: str, steps: list[dict[str, Any]]) -> list[dict[str, Any]]:
    failures: list[dict[str, Any]] = []
    for step in steps:
        if not isinstance(step, dict) or step.get("verdict") != "failed":
            continue
        name = _step_name(step)
        ownership = FAILED_STEP_OWNERS.get((scenario_id, name))
        if ownership is None:
            raise MatrixError(f"failed scenario step has no owner mapping: {scenario_id}/{name}")
        responsibility = RED_ITEM_OWNERS.get(ownership["red_item_id"])
        if responsibility is None:
            raise MatrixError(f"red item has no task/owner mapping: {ownership['red_item_id']}")
        failure: dict[str, Any] = {
            "step": name,
            "red_item_id": ownership["red_item_id"],
            **responsibility,
        }
        for key in ("operation_id", "expected_http", "actual_http", "expected", "actual"):
            value = step.get(key)
            if isinstance(value, (str, int, float, bool)) or value is None:
                if value is not None:
                    failure[key] = value
        failures.append(failure)
    return failures


def _scenario_sources(root: Path) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    observations: list[dict[str, Any]] = []
    documents: list[dict[str, Any]] = []
    for relative_path, mode in SOURCE_REPORTS:
        document = _read_json(root, relative_path)
        documents.append({"path": relative_path, "document": document})
        observations.extend(_report_records(document, mode, relative_path))
    observations.append(dict(F01_NOT_RUN))
    return observations, documents


def _collect_gaps(source_documents: list[dict[str, Any]]) -> list[dict[str, Any]]:
    found: dict[str, dict[str, Any]] = {}
    for source in source_documents:
        document = source["document"]
        candidates: list[Any] = []
        gaps = document.get("registered_gaps")
        if isinstance(gaps, list):
            candidates.extend(gaps)
        singular = document.get("registered_gap")
        if isinstance(singular, dict):
            candidates.append(singular)
        for candidate in candidates:
            if not isinstance(candidate, dict) or not isinstance(candidate.get("id"), str):
                continue
            gap_id = candidate["id"]
            policy = GAP_POLICIES.get(gap_id)
            if policy is None:
                raise MatrixError(f"registered gap has no task/owner mapping: {gap_id}")
            responsibility = RED_ITEM_OWNERS.get(gap_id)
            if responsibility is None:
                raise MatrixError(f"registered gap has no task/owner mapping: {gap_id}")
            if gap_id not in found:
                source_status = candidate.get("status")
                status = source_status if source_status in {
                    "open", "resolved", "fix_present_retest_pending"
                } else policy["status"]
                item: dict[str, Any] = {
                    "id": gap_id,
                    "title": candidate.get("title", gap_id),
                    "scenario_ids": policy["scenario_ids"],
                    **responsibility,
                    "status": status,
                    "source_status": candidate.get("status"),
                    "evidence": candidate.get("evidence") or candidate.get("service_does") or candidate.get("consequence"),
                    "source_artifacts": [],
                }
                if policy.get("related_gap_id"):
                    item["related_gap_id"] = policy["related_gap_id"]
                if policy.get("remediation_commit"):
                    item["remediation_commit"] = policy["remediation_commit"]
                    item["retest_required"] = True
                scenario_step = candidate.get("scenario_step")
                if scenario_step:
                    item["scenario_step"] = scenario_step
                found[gap_id] = item
            if source["path"] not in found[gap_id]["source_artifacts"]:
                found[gap_id]["source_artifacts"].append(source["path"])

    expected = set(GAP_POLICIES)
    missing = expected - set(found)
    if missing:
        raise MatrixError(f"registered gap evidence is missing: {', '.join(sorted(missing))}")
    return [found[gap_id] for gap_id in sorted(found)]


def assemble_matrix(
    contract: dict[str, Any],
    observations: Iterable[dict[str, Any]],
    red_items: list[dict[str, Any]] | None = None,
) -> dict[str, Any]:
    scenarios = _scenario_entries(contract)
    scenario_ids = [entry["id"] for entry in scenarios]
    if len(scenario_ids) != len(set(scenario_ids)):
        raise MatrixError("contract contains duplicate scenario ids")

    observed: dict[str, dict[str, Any]] = {}
    for record in observations:
        scenario_id = record.get("scenario")
        if scenario_id in observed:
            raise MatrixError(f"duplicate scenario evidence for {scenario_id}")
        if scenario_id not in scenario_ids:
            raise MatrixError(f"evidence references unknown scenario {scenario_id}")
        if record.get("verdict") not in VERDICTS:
            raise MatrixError(f"invalid verdict for {scenario_id}: {record.get('verdict')!r}")
        observed[scenario_id] = record

    missing = set(scenario_ids) - set(observed)
    if missing:
        raise MatrixError(f"missing scenario evidence: {', '.join(sorted(missing))}")

    red_by_scenario: dict[str, list[str]] = {}
    for item in red_items or []:
        if not item.get("owner_task") or not item.get("owner"):
            raise MatrixError(f"red item has no task/owner: {item.get('id')}")
        for scenario_id in item.get("scenario_ids", []):
            red_by_scenario.setdefault(scenario_id, []).append(item["id"])

    rows: list[dict[str, Any]] = []
    for scenario in scenarios:
        scenario_id = scenario["id"]
        record = observed[scenario_id]
        row: dict[str, Any] = {
            "id": scenario_id,
            "name": scenario["name"],
            "verdict": record["verdict"],
            "source_artifacts": record.get("source_artifacts", []),
            "observed_steps": record.get("observed_steps", 0),
            "red_item_ids": sorted(set(red_by_scenario.get(scenario_id, []))),
            "failed_steps": record.get("failed_steps", []),
        }
        if record.get("reason"):
            row["reason"] = record["reason"]
        if scenario_id == "F03":
            row["qualification"] = (
                "The saved step assertions passed, while T15-10-R1 separately records that the "
                "not-ready reason is categorized as asset_not_authorized rather than asset_not_ready."
            )
        if scenario_id == "F15":
            row["qualification"] = (
                "This is the saved pre-remediation live result. Commit 52651d8 changes confirmation "
                "handling; the real scenario has not been re-run after that change, so this historical "
                "failure is not evidence of a post-fix result."
            )
        rows.append(row)

    counts = {verdict: sum(row["verdict"] == verdict for row in rows) for verdict in VERDICT_ORDER}
    counts["total"] = len(rows)
    not_run = [
        {"id": row["id"], "reason": row["reason"]}
        for row in rows if row["verdict"] == "not_run"
    ]
    return {
        "report_version": 1,
        "contract_version": "0.5.0",
        "scope": (
            "A deterministic consolidation of saved T15 evidence. This command performs no live "
            "HTTP, database, model, or browser run. Each verdict describes its referenced observation."
        ),
        "scenarios": rows,
        "summary": counts,
        "red_items": red_items or [],
        "not_run": not_run,
        "boundaries": BOUNDARIES,
        "task_pool_alignment": {
            "T15": (
                "T15 records cross-task HTTP behavior, side effects, evidence, and gaps; it does not "
                "take over fixes owned by T08-T14."
            ),
            "T16": "The complete F01 flow and browser composition remain assigned to T16.",
        },
        "ownership_note": (
            "owner_task and owner identify the responsible task and task-owner role. The task pool "
            "deliberately has no named assignees prefilled; each matching task currently has no "
            "individual assignee, so no person's name is implied."
        ),
    }


def _load_gap_documents(source_documents: list[dict[str, Any]]) -> list[dict[str, Any]]:
    gaps = _collect_gaps(source_documents)
    # The warning-free F15 fixture was not met by the real model in the saved run. Keep it
    # separate from the service contract gaps and name the generation owner explicitly.
    f15 = next((source["document"].get("scenarios", {}).get("F15")
                for source in source_documents
                if isinstance(source["document"].get("scenarios"), dict)
                and source["document"]["scenarios"].get("F15")), None)
    has_warning_failure = isinstance(f15, dict) and any(
        isinstance(step, dict)
        and step.get("step") == "candidate-body-has-no-warning"
        and step.get("verdict") == "failed"
        for step in f15.get("steps", [])
    )
    if has_warning_failure:
        gaps.append({
            "id": "F15-MODEL-OUTPUT",
            "title": "F15 real model candidate included a warning in the chapter body",
            "scenario_ids": ["F15"],
            **RED_ITEM_OWNERS["F15-MODEL-OUTPUT"],
            "status": "observed_failure",
            "evidence": "candidate-body-has-no-warning expected true but the saved real run observed false.",
            "source_artifacts": ["docs/08-本轮实施方案/T15-10-验证报告.json"],
        })
    return sorted(gaps, key=lambda item: item["id"])


def render_json(document: dict[str, Any]) -> bytes:
    return (json.dumps(document, ensure_ascii=False, indent=2) + "\n").encode("utf-8")


def build_report(root: Path = ROOT) -> tuple[dict[str, Any], list[dict[str, Any]], list[dict[str, Any]]]:
    contract = _read_json(root, CONTRACT_REL)
    contract = {
        **contract,
        "scenarios": [
            entry for entry in _scenario_entries(contract)
            if entry["id"] in T15_SCENARIO_IDS
        ],
    }
    observations, source_documents = _scenario_sources(root)
    red_items = _load_gap_documents(source_documents)
    report = assemble_matrix(contract, observations, red_items)
    # Include all committed inputs used for verdicts, names, ownership, and boundaries.
    input_paths = sorted(set(SUPPORT_FILES) | {path for path, _ in SOURCE_REPORTS})
    source_artifacts = [
        {"path": path, "sha256": _source_digest(root, path)}
        for path in input_paths
    ]
    return report, source_artifacts, source_documents


def build_documents(root: Path = ROOT) -> tuple[dict[str, Any], dict[str, Any]]:
    report, source_artifacts, _ = build_report(root)
    report_bytes = render_json(report)

    repeated_report, repeated_sources, _ = build_report(root)
    repeated_bytes = render_json(repeated_report)
    if report_bytes != repeated_bytes or source_artifacts != repeated_sources:
        raise MatrixError("the same committed inputs rendered different matrix bytes")

    evidence = {
        "evidence_version": 1,
        "verification_mode": "offline_consolidation_of_saved_evidence",
        "report_path": REPORT_REL,
        "report_sha256": hashlib.sha256(report_bytes).hexdigest(),
        "source_artifacts": source_artifacts,
        "determinism": {
            "first_render_sha256": hashlib.sha256(report_bytes).hexdigest(),
            "second_render_sha256": hashlib.sha256(repeated_bytes).hexdigest(),
            "identical": report_bytes == repeated_bytes,
        },
    }
    return report, evidence


def _output_path(root: Path, value: str) -> Path:
    path = Path(value)
    return path if path.is_absolute() else root / path


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", type=Path, default=ROOT, help="repository root (defaults to this checkout)")
    parser.add_argument("--report", default=REPORT_REL, help="matrix report output path")
    parser.add_argument("--evidence", default=EVIDENCE_REL, help="evidence output path")
    args = parser.parse_args(argv)

    try:
        report, evidence = build_documents(args.repo_root.resolve())
        report_bytes = render_json(report)
        evidence_bytes = render_json(evidence)
        report_path = _output_path(args.repo_root, args.report)
        evidence_path = _output_path(args.repo_root, args.evidence)
        report_path.parent.mkdir(parents=True, exist_ok=True)
        evidence_path.parent.mkdir(parents=True, exist_ok=True)
        report_path.write_bytes(report_bytes)
        evidence_path.write_bytes(evidence_bytes)
    except (MatrixError, OSError, ValueError) as error:
        print(f"T15 matrix failed: {error}", file=sys.stderr)
        return 1

    summary = report["summary"]
    print(
        f"T15 matrix: {summary['passed']} passed, {summary['failed']} failed, "
        f"{summary['not_run']} not_run; report={args.report}; evidence={args.evidence}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
