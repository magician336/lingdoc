#!/usr/bin/env python3
"""Run the T15-10 executable evidence scenarios against the authenticated local service.

F03 and F18 use their declared request steps through the contract runner. F15 and F20
need asynchronous model-produced candidates, so this driver polls the same public HTTP
operations and records response checks without placing generated text or identifiers in
the report. F06/F09/F14 are explicitly not_run because they need a controlled model run.
"""
from __future__ import annotations

import argparse
from datetime import datetime
import hashlib
import io
import json
import runpy
import subprocess
import sys
import time
import zipfile
import xml.etree.ElementTree as ET
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.run_f01 import OPENAPI_PATH, SCENARIOS_PATH
from scripts.lingdoc_mock.run_scenario import run_scenario
from scripts.lingdoc_mock.run_t15_08_live import LiveRun, build_state, data

REPORT_PATH = ROOT / "docs/08-本轮实施方案/T15-10-验证报告.json"
BASE_URL = "http://127.0.0.1:8080/api/v1/lingdoc"


def record(steps: list[dict[str, Any]], name: str, status: int, expected: int) -> None:
    steps.append({"step": name, "expected_http": expected, "actual_http": status,
                  "verdict": "passed" if status == expected else "failed"})


class ScenarioFailure(RuntimeError):
    def __init__(self, message: str, steps: list[dict[str, Any]]) -> None:
        super().__init__(message)
        self.steps = steps


def exchange(run: LiveRun, steps: list[dict[str, Any]], name: str, operation: str,
             params: dict[str, Any] | None = None, body: Any = None,
             headers: dict[str, str] | None = None, expected: int = 200,
             continue_on_status_mismatch: bool = False) -> tuple[Any, dict[str, str]]:
    status, payload, response_headers = run.call(operation, params or {}, body, headers or {})
    record(steps, name, status, expected)
    if status != expected:
        code = ((payload or {}).get("error") or {}).get("code") if isinstance(payload, dict) else None
        if not continue_on_status_mismatch:
            raise ScenarioFailure(f"{name}: HTTP {status}, expected {expected}, code={code or 'unavailable'}", steps)
        return payload, response_headers
    if status >= 400:
        return payload, response_headers
    return data(payload), response_headers


def poll_generation(run: LiveRun, steps: list[dict[str, Any]], project_id: str, run_id: str,
                    timeout: float) -> dict[str, Any]:
    deadline = time.monotonic() + timeout
    while True:
        generation, _ = exchange(run, steps, "poll-generation", "getGeneration",
                                 {"projectId": project_id, "runId": run_id})
        if generation.get("status") in {"succeeded", "failed", "interrupted", "cancelled"}:
            return generation
        if time.monotonic() >= deadline:
            raise ScenarioFailure("generation did not reach a terminal state before timeout", steps)
        time.sleep(1)


def list_chapters(run: LiveRun, steps: list[dict[str, Any]], project_id: str) -> tuple[str, str, dict[str, Any]]:
    chapters, _ = exchange(run, steps, "read-chapters", "listChapters", {"projectId": project_id})
    question = next(chapter for chapter in chapters if chapter["section_id"] == "question")
    method = next(chapter for chapter in chapters if chapter["section_id"] == "method")
    return question["id"], method["id"], {chapter["section_id"]: chapter for chapter in chapters}


def decisions(review_items: list[dict[str, Any]], reason: str) -> list[dict[str, str]]:
    return [{"review_item_id": item["id"], "disposition": "retained_warning", "reason": reason}
            for item in review_items]


def archived_fingerprint(project_id: str, chapter_id: str, version_id: str,
                         confirmation_id: str, steps: list[dict[str, Any]]) -> tuple[str, ...]:
    """Read only row presence and opaque digests from the local service database."""
    import re
    if not all(re.fullmatch(r"[A-Za-z0-9-]{1,64}", value)
               for value in (project_id, chapter_id, version_id, confirmation_id)):
        raise ScenarioFailure("database history probe received an unexpected identifier shape", steps)
    query = (
        "SELECT "
        f"EXISTS(SELECT 1 FROM lingdoc_chapter_versions WHERE id='{version_id}' AND project_id='{project_id}' "
        f"AND chapter_id='{chapter_id}')::int::text || '|' || "
        "COALESCE((SELECT md5(concat_ws(chr(31), body_markdown, source_ids_json, review_items_json, "
        "confirmation_valid::text, spec_revision::text)) FROM lingdoc_chapter_versions "
        f"WHERE id='{version_id}' AND project_id='{project_id}' AND chapter_id='{chapter_id}'), 'missing') || '|' || "
        f"EXISTS(SELECT 1 FROM lingdoc_chapter_confirmations WHERE id='{confirmation_id}' "
        f"AND chapter_id='{chapter_id}' AND chapter_version_id='{version_id}' AND valid=true)::int::text || '|' || "
        "COALESCE((SELECT md5(details_json || chr(31) || valid::text) FROM lingdoc_chapter_confirmations "
        f"WHERE id='{confirmation_id}' AND chapter_id='{chapter_id}' AND chapter_version_id='{version_id}'), 'missing')"
    )
    result = subprocess.run([
        "docker", "exec", "WeKnora-postgres-dev", "psql", "-U", "postgres", "-d", "WeKnora",
        "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-c", query,
    ], capture_output=True, text=True, check=False)
    if result.returncode != 0:
        raise ScenarioFailure("local database history readback could not complete", steps)
    values = tuple(result.stdout.strip().split("|"))
    if len(values) != 4:
        raise ScenarioFailure("local database history readback returned an unexpected shape", steps)
    return values


def run_f15(token: str, tenant_id: int, knowledge_id: str, timeout: float) -> dict[str, Any]:
    steps: list[dict[str, Any]] = []
    run = LiveRun(BASE_URL, token, tenant_id, timeout=min(timeout, 30))
    loader, state = build_state(run, knowledge_id, "S5")
    project_id = loader.identifiers["project_id"]
    question_id, method_id, chapters = list_chapters(run, steps, project_id)
    question = chapters["question"]
    asset_id = loader.asset_id("k-demo")

    queued, _ = exchange(run, steps, "start-generation", "startGeneration", {"projectId": project_id}, {
        "chapter_id": question_id, "asset_ids": [asset_id],
        "instruction": "正文只写研究问题，不要写警示；必须把待核事实单列在 review_items。",
        "expected_spec_revision": 1, "expected_chapter_version_id": question.get("current_version_id"),
    }, {"Idempotency-Key": "f15-generate-0001"}, 202)
    generation = poll_generation(run, steps, project_id, queued["id"], timeout)
    if generation.get("status") != "succeeded" or not generation.get("candidate_id"):
        raise ScenarioFailure(f"the model did not produce an F15 candidate (status={generation.get('status')})", steps)
    candidate, _ = exchange(run, steps, "read-generated-candidate", "getCandidate", {
        "projectId": project_id, "candidateId": generation["candidate_id"],
    })
    reviews = candidate.get("review_items") or []
    body = candidate.get("body_markdown") or ""
    warning_terms = ("待核", "待核实", "需核实", "警示", "warning")
    body_has_warning = any(term.casefold() in body.casefold() for term in warning_terms)
    steps.append({"step": "candidate-body-has-no-warning", "actual": not body_has_warning,
                  "expected": True, "verdict": "passed" if not body_has_warning else "failed"})
    steps.append({"step": "candidate-has-review-items", "actual": len(reviews) > 0,
                  "expected": True, "verdict": "passed" if reviews else "failed"})
    if not reviews:
        raise ScenarioFailure("the real model output did not satisfy F15's warning-free body/review-item fixture", steps)

    accepted, _ = exchange(run, steps, "accept-candidate", "acceptCandidate", {
        "projectId": project_id, "chapterId": question_id,
    }, {
        "candidate_id": candidate["id"], "expected_chapter_version_id": question.get("current_version_id"),
        "expected_spec_revision": 1, "replace_existing": False,
    }, {"Idempotency-Key": "f15-accept-0001"}, 201)
    adopted_ids = [item["id"] for item in accepted.get("review_items") or []]
    steps.append({"step": "adoption-retains-review-ids", "actual": adopted_ids == [item["id"] for item in reviews],
                  "expected": True, "verdict": "passed" if adopted_ids == [item["id"] for item in reviews] else "failed"})

    # Keep source markers consistent if the generated candidate used any verified sources.
    source_ids = accepted.get("source_ids") or []
    body_markdown = "人工编辑后的合成研究问题。" + "".join(f" [[source:{source_id}]]" for source_id in source_ids)
    edited, _ = exchange(run, steps, "manual-edit", "saveChapter", {
        "projectId": project_id, "chapterId": question_id,
    }, {
        "expected_chapter_version_id": accepted["current_version_id"], "expected_spec_revision": 1,
        "body_markdown": body_markdown, "source_ids": source_ids,
    }, {"Idempotency-Key": "f15-edit-0001"}, 201)
    edited_ids = [item["id"] for item in edited.get("review_items") or []]
    steps.append({"step": "manual-edit-retains-review-ids", "actual": edited_ids == adopted_ids,
                  "expected": True, "verdict": "passed" if edited_ids == adopted_ids else "failed"})

    missing_payload, _ = exchange(run, steps, "confirm-without-decisions", "confirmChapter", {
        "projectId": project_id, "chapterId": question_id,
    }, {"expected_chapter_version_id": edited["current_version_id"], "expected_spec_revision": 1,
         "review_decisions": []}, {"Idempotency-Key": "f15-confirm-missing-0001"}, 422,
        continue_on_status_mismatch=True)
    missing_code = ((missing_payload or {}).get("error") or {}).get("code")
    missing_ok = steps[-1]["actual_http"] == 422 and missing_code == "invalid_state"
    steps.append({"step": "missing-review-decision-rejected",
                  "actual": {"http": steps[-1]["actual_http"], "code": missing_code},
                  "expected": {"http": 422, "code": "invalid_state"},
                  "verdict": "passed" if missing_ok else "failed"})

    reason = "保留为内部演示待核项；原文及本理由须随演示文件附录。"
    confirmed, _ = exchange(run, steps, "confirm-retained-warnings", "confirmChapter", {
        "projectId": project_id, "chapterId": question_id,
    }, {"expected_chapter_version_id": edited["current_version_id"], "expected_spec_revision": 1,
         "review_decisions": decisions(edited.get("review_items") or [], reason)},
        {"Idempotency-Key": "f15-confirm-retained-0001"}, 201)
    # A simple manual second chapter lets the frozen release include the confirmed question.
    method_saved, _ = exchange(run, steps, "save-method-chapter", "saveChapter", {
        "projectId": project_id, "chapterId": method_id,
    }, {"expected_chapter_version_id": None, "expected_spec_revision": 1,
         "body_markdown": "合成演示方法章节。", "source_ids": []},
        {"Idempotency-Key": "f15-method-save-0001"}, 201)
    exchange(run, steps, "confirm-method-chapter", "confirmChapter", {
        "projectId": project_id, "chapterId": method_id,
    }, {"expected_chapter_version_id": method_saved["current_version_id"], "expected_spec_revision": 1,
         "review_decisions": []}, {"Idempotency-Key": "f15-method-confirm-0001"}, 201)
    after_confirmations, _ = exchange(run, steps, "read-confirmed-chapters", "listChapters",
                                      {"projectId": project_id})
    confirmations_valid = all(item.get("confirmation_valid") for item in after_confirmations)
    steps.append({"step": "both-current-chapters-confirmed", "actual": confirmations_valid,
                  "expected": True, "verdict": "passed" if confirmations_valid else "failed"})
    project, _ = exchange(run, steps, "read-project-version", "getProject", {"projectId": project_id})
    snapshot, _ = exchange(run, steps, "prepare-release", "prepareRelease", {"projectId": project_id}, {
        "expected_project_version": project["project_version"],
    }, {"Idempotency-Key": "f15-release-0001"}, 201)
    released, _ = exchange(run, steps, "read-back-release", "getRelease", {
        "projectId": project_id, "snapshotId": snapshot["id"],
    })
    frozen_chapter = next(item for item in released["frozen_input"]["chapters"]
                          if item["chapter_id"] == question_id)
    decisions_frozen = frozen_chapter["confirmation"].get("review_decisions") or []
    frozen_items_ok = frozen_chapter.get("review_items") == edited.get("review_items")
    expected_decisions = decisions(edited.get("review_items") or [], reason)
    frozen_by_id = {item.get("review_item_id"): item for item in decisions_frozen}
    expected_by_id = {item["review_item_id"]: item for item in expected_decisions}
    frozen_decision_ids_ok = set(frozen_by_id) == set(expected_by_id)
    frozen_dispositions_ok = all(item.get("disposition") == "retained_warning" for item in decisions_frozen)
    frozen_reasons_ok = all(item.get("reason") == reason for item in decisions_frozen)
    frozen_decisions_ok = (frozen_decision_ids_ok and frozen_dispositions_ok and frozen_reasons_ok)
    appendix_ok = frozen_items_ok and frozen_decisions_ok
    steps.append({"step": "release-freezes-warning-and-reason",
                  "actual": {"review_items_match": frozen_items_ok, "decisions_match": frozen_decisions_ok,
                             "decision_ids_match": frozen_decision_ids_ok,
                             "all_retained_warning": frozen_dispositions_ok,
                             "reasons_match": frozen_reasons_ok,
                             "review_item_count": len(frozen_chapter.get("review_items") or []),
                             "decision_count": len(decisions_frozen)},
                  "expected": {"review_items_match": True, "decision_ids_match": True,
                               "all_retained_warning": True, "reasons_match": True},
                  "verdict": "passed" if appendix_ok else "failed"})
    sources = {item["id"]: item for item in released["frozen_input"].get("sources", [])}
    template_version = released["frozen_input"]["template"]["version"]
    spec_revision = released["frozen_input"]["spec_revision"]
    frozen_confirmation_checks = []
    for chapter in released["frozen_input"]["chapters"]:
        confirmation = chapter.get("confirmation") or {}
        try:
            datetime.fromisoformat((confirmation.get("created_at") or "").replace("Z", "+00:00"))
            timestamp_valid = True
        except ValueError:
            timestamp_valid = False
        expected_assets = {(sources[source_id]["asset_id"], sources[source_id]["asset_revision"])
                           for source_id in chapter.get("source_ids", []) if source_id in sources}
        actual_assets = {(item["asset_id"], item["asset_revision"])
                         for item in confirmation.get("asset_versions", [])}
        checks = {"version_matches": confirmation.get("chapter_version_id") == chapter.get("chapter_version_id"),
                  "chapter_matches": bool(confirmation.get("chapter_id")) and confirmation.get("chapter_id") == chapter.get("chapter_id"),
                  "spec_matches": confirmation.get("spec_revision") == spec_revision,
                  "template_matches": confirmation.get("template_version") == template_version,
                  "actor_present": bool(confirmation.get("actor_user_id")),
                  "created_at_valid": timestamp_valid,
                  "asset_versions_match": expected_assets == actual_assets,
                  "expected_asset_version_count": len(expected_assets),
                  "actual_asset_version_count": len(actual_assets)}
        frozen_confirmation_checks.append({"section_id": chapter.get("section_id"), **checks,
                                           "valid": all(value for value in checks.values()
                                                        if type(value) is bool)})
    frozen_binding_ok = all(item["valid"] for item in frozen_confirmation_checks)
    steps.append({"step": "frozen-confirmation-binds-to-frozen-chapter",
                  "actual": frozen_confirmation_checks,
                  "expected": "every frozen chapter confirmation matches chapter, spec, template, actor, time, and sources",
                  "verdict": "passed" if frozen_binding_ok else "failed"})
    snapshot_status = (snapshot.get("check") or {}).get("status")
    blocking_rules = sorted({item.get("rule_id", "unknown") for item in (snapshot.get("check") or {}).get("issues", [])
                             if item.get("severity") == "blocking"})
    confirmation_diagnostics = sorted({item.get("message", "")
                                       for item in (snapshot.get("check") or {}).get("issues", [])
                                       if item.get("rule_id") == "chapter-confirmed"})
    steps.append({"step": "snapshot-check-passed", "actual": snapshot_status,
                  "blocking_rules": blocking_rules,
                  "confirmation_diagnostics": confirmation_diagnostics,
                  "expected": "passed", "verdict": "passed" if snapshot_status == "passed" else "failed"})
    if snapshot_status == "passed":
        export_task, _ = exchange(run, steps, "start-docx-export", "startExport", {
            "projectId": project_id, "snapshotId": snapshot["id"],
        }, {"format": "docx"}, {"Idempotency-Key": "f15-export-0001"}, 202)
        deadline = time.monotonic() + timeout
        export: dict[str, Any] = {}
        while True:
            export, _ = exchange(run, steps, "poll-docx-export", "getExport", {
                "projectId": project_id, "exportId": export_task["id"],
            })
            if export.get("status") in {"verified", "failed"}:
                break
            if time.monotonic() >= deadline:
                raise ScenarioFailure("DOCX export did not reach a terminal state before timeout", steps)
            time.sleep(1)
        if export.get("status") != "verified":
            raise ScenarioFailure("DOCX export failed verification", steps)
        file_status, file_bytes, file_headers = run.call("downloadExport", {
            "projectId": project_id, "exportId": export_task["id"],
        })
        record(steps, "download-docx", file_status, 200)
        if file_status != 200 or not isinstance(file_bytes, bytes):
            raise ScenarioFailure("verified DOCX could not be downloaded", steps)
        bytes_hash_ok = hashlib.sha256(file_bytes).hexdigest() == export.get("file_sha256")
        steps.append({"step": "download-hash-matches-export", "actual": bytes_hash_ok,
                      "expected": True, "verdict": "passed" if bytes_hash_ok else "failed"})
        try:
            with zipfile.ZipFile(io.BytesIO(file_bytes)) as archive:
                document = ET.fromstring(archive.read("word/document.xml"))
            document_text = "".join(node.text or "" for node in document.iter()
                                     if node.tag.endswith("}t"))
        except (KeyError, OSError, zipfile.BadZipFile, ET.ParseError) as error:
            raise ScenarioFailure("verified export was not a readable DOCX document", steps) from error
        statements_present = all((item.get("statement") or "") in document_text
                                 for item in edited.get("review_items") or [])
        reasons_present = all((decision.get("reason") or "") in document_text for decision in decisions_frozen)
        internal_demo_present = "内部演示" in document_text or "internal_demo" in document_text
        appendix_checks = statements_present and reasons_present and internal_demo_present
        steps.append({"step": "docx-appendix-contains-review-text-reason-and-demo-label",
                      "actual": {"statements_present": statements_present, "reasons_present": reasons_present,
                                 "internal_demo_present": internal_demo_present},
                      "expected": {"statements_present": True, "reasons_present": True,
                                   "internal_demo_present": True},
                      "verdict": "passed" if appendix_checks else "failed"})
    else:
        steps.append({"step": "docx-appendix-content", "actual": "not_run",
                      "reason": "release snapshot check was not passed, so the public API would reject export",
                      "verdict": "not_run"})
    verdict = "passed" if all(item.get("verdict") == "passed" for item in steps) else "failed"
    return {"verdict": verdict, "starting_state": "S5", "steps": steps,
            "scope": "real HTTP generation, candidate adoption, manual edit, confirmation, frozen release, DOCX download, and appendix text check",
            "not_run_assertions": ["原始候选正文是否没有警示由真实模型决定；本次响应本身记录在布尔检查中。"],
            "state_readback": {"runner_status": state.get("runner_status"), "mismatches": len(state["mismatches"])}}


def run_f20(token: str, tenant_id: int, knowledge_id: str, timeout: float) -> dict[str, Any]:
    steps: list[dict[str, Any]] = []
    run = LiveRun(BASE_URL, token, tenant_id, timeout=min(timeout, 30))
    loader, state = build_state(run, knowledge_id, "S5")
    project_id = loader.identifiers["project_id"]
    question_id, _, chapters = list_chapters(run, steps, project_id)
    asset_id = loader.asset_id("k-demo")

    # Start with a real human-written version, then create and confirm a review item before editing.
    seeded, _ = exchange(run, steps, "save-initial-human-chapter", "saveChapter", {
        "projectId": project_id, "chapterId": question_id,
    }, {"expected_chapter_version_id": chapters["question"].get("current_version_id"),
         "expected_spec_revision": 1, "body_markdown": "人工撰写的初始合成研究问题。", "source_ids": []},
        {"Idempotency-Key": "f20-human-seed-0001"}, 201)

    # Create a real review item, accept it, confirm it, and then manually edit it.
    first_queued, _ = exchange(run, steps, "start-first-generation", "startGeneration", {"projectId": project_id}, {
        "chapter_id": question_id, "asset_ids": [asset_id],
        "instruction": "生成研究问题；把一个明确不能由合成资料证明的说法单列为 review_item。",
        "expected_spec_revision": 1, "expected_chapter_version_id": seeded.get("current_version_id"),
    }, {"Idempotency-Key": "f20-first-generate-0001"}, 202)
    first_generation = poll_generation(run, steps, project_id, first_queued["id"], timeout)
    if first_generation.get("status") != "succeeded" or not first_generation.get("candidate_id"):
        raise ScenarioFailure(f"the model did not produce F20's initial candidate (status={first_generation.get('status')})", steps)
    first_candidate, _ = exchange(run, steps, "read-first-candidate", "getCandidate", {
        "projectId": project_id, "candidateId": first_generation["candidate_id"],
    })
    old_reviews = first_candidate.get("review_items") or []
    if not old_reviews:
        raise ScenarioFailure("the real model did not create the review item needed for F20's old version", steps)
    first_accept, _ = exchange(run, steps, "accept-first-candidate", "acceptCandidate", {
        "projectId": project_id, "chapterId": question_id,
    }, {"candidate_id": first_candidate["id"], "expected_chapter_version_id": seeded.get("current_version_id"),
         "expected_spec_revision": 1, "replace_existing": True},
        {"Idempotency-Key": "f20-first-accept-0001"}, 201)
    old_version = first_accept["current_version_id"]
    warning_reason = "仅为内部合成演示，保留该待核项及其原文。"
    old_confirmation, _ = exchange(run, steps, "confirm-first-candidate", "confirmChapter", {
        "projectId": project_id, "chapterId": question_id,
    }, {"expected_chapter_version_id": old_version, "expected_spec_revision": 1,
         "review_decisions": decisions(old_reviews, warning_reason)},
        {"Idempotency-Key": "f20-first-confirm-0001"}, 201)
    old_body = "人工编辑的当前正文。" + "".join(f" [[source:{sid}]]" for sid in first_accept.get("source_ids") or [])
    manual, _ = exchange(run, steps, "manual-edit-old-version", "saveChapter", {
        "projectId": project_id, "chapterId": question_id,
    }, {"expected_chapter_version_id": old_version, "expected_spec_revision": 1,
         "body_markdown": old_body, "source_ids": first_accept.get("source_ids") or []},
        {"Idempotency-Key": "f20-manual-edit-0001"}, 201)
    steps.append({"step": "manual-edit-inherits-old-review-ids",
                  "actual": [x["id"] for x in manual.get("review_items") or []] == [x["id"] for x in old_reviews],
                  "expected": True,
                  "verdict": "passed" if [x["id"] for x in manual.get("review_items") or []] == [x["id"] for x in old_reviews] else "failed"})
    old_version = manual["current_version_id"]
    old_confirmation, _ = exchange(run, steps, "confirm-manually-edited-old-version", "confirmChapter", {
        "projectId": project_id, "chapterId": question_id,
    }, {"expected_chapter_version_id": old_version, "expected_spec_revision": 1,
         "review_decisions": decisions(manual.get("review_items") or [], warning_reason)},
        {"Idempotency-Key": "f20-manual-confirm-0001"}, 201)
    history_before = archived_fingerprint(project_id, question_id, old_version, old_confirmation["id"], steps)

    second_queued, _ = exchange(run, steps, "start-replacement-generation", "startGeneration", {"projectId": project_id}, {
        "chapter_id": question_id, "asset_ids": [asset_id],
        "instruction": "根据提供的合成资料重写研究问题并引用其中证据；正文保持审慎，不写超出资料支持范围的结论。",
        "expected_spec_revision": 1, "expected_chapter_version_id": manual["current_version_id"],
    }, {"Idempotency-Key": "f20-replacement-generate-0001"}, 202)
    second_generation = poll_generation(run, steps, project_id, second_queued["id"], timeout)
    if second_generation.get("status") != "succeeded" or not second_generation.get("candidate_id"):
        raise ScenarioFailure(f"the model did not produce F20's replacement candidate (status={second_generation.get('status')})", steps)
    second_candidate, _ = exchange(run, steps, "read-replacement-candidate", "getCandidate", {
        "projectId": project_id, "candidateId": second_generation["candidate_id"],
    })
    replacement_reviews = second_candidate.get("review_items") or []
    candidate_basis_matches = ((second_candidate.get("basis") or {}).get("chapter_version_id")
                               == manual.get("current_version_id"))
    steps.append({"step": "replacement-candidate-basis-matches-current-human-version",
                  "actual": candidate_basis_matches, "expected": True,
                  "verdict": "passed" if candidate_basis_matches else "failed"})
    replacement, _ = exchange(run, steps, "accept-replacement", "acceptCandidate", {
        "projectId": project_id, "chapterId": question_id,
    }, {"candidate_id": second_candidate["id"], "expected_chapter_version_id": manual["current_version_id"],
         "expected_spec_revision": 1, "replace_existing": True},
        {"Idempotency-Key": "f20-reaccept-0001"}, 201)
    result_ok = (replacement.get("review_items") == second_candidate.get("review_items")
                 and replacement.get("body_markdown") == second_candidate.get("body_markdown")
                 and replacement.get("source_ids") == second_candidate.get("source_ids")
                 and not replacement.get("confirmation_valid")
                 and replacement.get("current_version_id") != manual.get("current_version_id"))
    steps.append({"step": "replacement-preserves-live-candidate-review-set",
                  "actual": replacement.get("review_items") == second_candidate.get("review_items"),
                  "expected": True,
                  "verdict": "passed" if replacement.get("review_items") == second_candidate.get("review_items") else "failed"})
    steps.append({"step": "replacement-is-whole-new-version-unconfirmed", "actual": result_ok,
                  "expected": True, "verdict": "passed" if result_ok else "failed"})
    current_chapters, _ = exchange(run, steps, "read-current-chapters", "listChapters", {"projectId": project_id})
    current = next(item for item in current_chapters if item["id"] == question_id)
    steps.append({"step": "current-chapter-matches-replacement", "actual": current == replacement,
                  "expected": True, "verdict": "passed" if current == replacement else "failed"})
    history_after = archived_fingerprint(project_id, question_id, old_version, old_confirmation["id"], steps)
    history_ok = (history_before == history_after and len(history_after) == 4
                  and history_after[0] == "1" and history_after[2] == "1")
    steps.append({"step": "archived-version-and-confirmation-unchanged-in-database",
                  "actual": {"version_row_present": history_after[0] == "1",
                             "confirmation_row_valid": history_after[2] == "1",
                             "before_after_fingerprints_match": history_before == history_after},
                  "expected": {"version_row_present": True, "confirmation_row_valid": True,
                               "before_after_fingerprints_match": True},
                  "verdict": "passed" if history_ok else "failed"})
    history = {"verdict": "passed" if history_ok else "failed",
               "version_row_present": history_after[0] == "1",
               "confirmation_row_valid": history_after[2] == "1",
               "before_after_fingerprints_match": history_before == history_after}
    verdict = "passed" if all(item.get("verdict") == "passed" for item in steps) else "failed"
    return {"verdict": verdict, "starting_state": "S5", "steps": steps,
            "history_readback": history,
            "candidate_variation": {"replacement_review_item_count": len(replacement_reviews),
                                    "reference_example_has_empty_review_items": True},
            "previous_confirmation_observed_before_replacement": bool(old_confirmation),
            "not_run_assertions": ["同键未提交 request_in_progress/Retry-After 需要可控并发窗口。",
                                   "撤权后不重放受限内容需要跨租户撤权 fixture，属于 #35 场景。"],
            "scope": "real HTTP model generation, confirmation, manual edit, whole-chapter replacement, and Postgres row-preservation fingerprints",
            "state_readback": {"runner_status": state.get("runner_status"), "mismatches": len(state["mismatches"])}}


def run_live(token: str, tenant_id: int, ready_knowledge_id: str, not_ready_knowledge_id: str,
             timeout: float, report_path: Path = REPORT_PATH) -> dict[str, Any]:
    report: dict[str, Any] = {
        "issue": 37,
        "mode": "real_http_evidence_scenarios",
        "provider_host": urlsplit(BASE_URL).netloc,
        "scenarios": {},
        "registered_gaps": [],
        "scope": "authenticated local HTTP observations; generated text and provider IDs are excluded",
    }
    try:
        for scenario_id, knowledge in (("F03", {"k-notready": not_ready_knowledge_id}),
                                       ("F18", {"k-demo": ready_knowledge_id})):
            run_report = run_scenario(scenario_id, SCENARIOS_PATH, OPENAPI_PATH,
                                      knowledge=knowledge, member={}, identities={},
                                      base_url=BASE_URL, token=token, timeout=min(timeout, 30))
            report["scenarios"][scenario_id] = run_report["executed"]
            if scenario_id == "F18":
                report["scenarios"][scenario_id]["not_run_assertions"] = [
                    "同键未提交 request_in_progress 与 Retry-After 需要可控并发窗口。",
                    "撤权后不重放受限内容需要跨租户撤权 fixture，已在 #35 的 F19 族覆盖。",
                ]

        try:
            report["scenarios"]["F15"] = run_f15(token, tenant_id, ready_knowledge_id, timeout)
        except Exception as error:
            report["scenarios"]["F15"] = {"verdict": "failed", "reason": str(error),
                                            "steps": getattr(error, "steps", []), "starting_state": "S5"}
        try:
            report["scenarios"]["F20"] = run_f20(token, tenant_id, ready_knowledge_id, timeout)
        except Exception as error:
            report["scenarios"]["F20"] = {"verdict": "failed", "reason": str(error),
                                            "steps": getattr(error, "steps", []), "starting_state": "S5"}

        report["scenarios"].update({
            "F06": {"verdict": "not_run", "reason": "需要真实模型完成一轮生成并在运行中修改条件；本票将生成期间竞态场景标为 not_run，不使用合成生成结果。"},
            "F09": {"verdict": "not_run", "reason": "需要可控模型超时或 worker 中断注入；当前真实服务没有公开注入点。"},
            "F14": {"verdict": "not_run", "reason": "需要真实模型针对只有相关、没有支撑的资料生成带 review_items 的候选，并进行人工质量核查。"},
        })
        report["registered_gaps"].append({
            "id": "T15-10-R1",
            "title": "retrieveSources 对未就绪绑定返回 asset_not_authorized",
            "status": "open",
            "evidence": "F03 起点读回给出 deny_reason=not_ready；retrieveSources 实际返回 HTTP 422 asset_not_authorized，没有给出 asset_not_ready。",
        })
        report["registered_gaps"].append({
            "id": "T15-10-R2",
            "title": "缺少待核决定的确认请求返回 400",
            "status": "open",
            "evidence": "F15 实际请求遗漏全部 review_decisions 后返回 HTTP 400 invalid_request；场景契约声明预期 HTTP 422。",
        })
        report["registered_gaps"].append({
            "id": "T15-10-R3",
            "title": "F15 快照确认资料版本与冻结来源不一致",
            "status": "open",
            "evidence": "F15 当前章节读回显示已确认；冻结快照中问题章确认记录的 asset_versions 与冻结来源集合不一致，快照以 chapter-confirmed 阻断。",
        })
        report["summary"] = {verdict: sum(item.get("verdict") == verdict for item in report["scenarios"].values())
                              for verdict in ("passed", "failed", "not_run")}
        report["runner_status"] = "completed"
    except Exception as error:
        report["runner_status"] = "failed"
        report["fatal"] = {"reason": str(error)}
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--local-facts-path", type=Path, required=True,
                        help="existing local fixture helper providing credentials and knowledge IDs")
    parser.add_argument("--timeout", type=float, default=240)
    args = parser.parse_args()
    if not args.local_facts_path.is_file():
        parser.error("local fixture helper does not exist")
    facts = runpy.run_path(str(args.local_facts_path))
    token = facts["login"]()[1]["token"]
    knowledge_base_id = facts["ensure_kb"](token)[0]["id"]
    ready_id = facts["ensure_knowledge"](token, knowledge_base_id, facts["READY_TITLE"], "ready")[0]["id"]
    not_ready_id = facts["ensure_knowledge"](token, knowledge_base_id, facts["NOT_READY_TITLE"], "draft")[0]["id"]
    report = run_live(token, facts["TENANT"], ready_id, not_ready_id, args.timeout)
    print(json.dumps({"runner_status": report.get("runner_status"), "summary": report.get("summary"),
                      "scenarios": {key: value.get("verdict") for key, value in report.get("scenarios", {}).items()}},
                     ensure_ascii=False))
    return 0 if report.get("runner_status") == "completed" and not report.get("summary", {}).get("failed") else 1


if __name__ == "__main__":
    raise SystemExit(main())
