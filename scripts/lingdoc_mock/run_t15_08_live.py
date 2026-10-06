#!/usr/bin/env python3
"""Run the T15-08 revocation family against a real local WeKnora/LingDoc service.

Credentials and fixture IDs are supplied by the caller. The helper creates a temporary
consumer tenant, organization and knowledge-base share, then removes those control-plane
fixtures in a finally block. LingDoc projects remain as ordinary synthetic test records.
The report contains statuses and boolean checks only; it never stores tokens, provider IDs,
source text, chapter text, knowledge titles, or export bytes.
"""
from __future__ import annotations

import argparse
import json
import os
import runpy
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

from scripts.lingdoc_mock.load_state import Bindings, StateLoader, load_state
from scripts.lingdoc_mock.run_f01 import (
    OPENAPI_PATH,
    SCENARIOS_PATH,
    ProviderClient,
    build_url,
    header_value,
    operation_index,
    read_json_object,
)

ROOT = Path(__file__).resolve().parents[2]
REPORT_PATH = ROOT / "docs/08-本轮实施方案/T15-S9-验证报告.json"


class TenantOpener:
    def __init__(self, tenant_id: int) -> None:
        self.tenant_id = tenant_id

    def __call__(self, request, timeout=20):
        request.add_header("X-Tenant-ID", str(self.tenant_id))
        return urllib.request.urlopen(request, timeout=timeout)


class LiveRun:
    def __init__(self, base_url: str, token: str, tenant_id: int, timeout: float = 20) -> None:
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.tenant_id = tenant_id
        self.openapi = read_json_object(OPENAPI_PATH, "OpenAPI document")
        _, self.operations = operation_index(self.openapi)
        self.client = ProviderClient(self.base_url, token=token, timeout=timeout,
                                     opener=TenantOpener(tenant_id))

    def call(self, operation: str, params: dict[str, Any] | None = None, body: Any = None,
             headers: dict[str, str] | None = None) -> tuple[int, Any, dict[str, str]]:
        method, template = self.operations[operation]
        url = build_url(self.base_url, template, params or {}, {})
        request_headers = dict(headers or {})
        correlation_id = os.environ.get("G6_CORRELATION_ID", "").strip()
        if correlation_id:
            request_headers.setdefault("X-Request-ID", correlation_id)
        status, response_headers, payload = self.client.request(method, url, request_headers, body)
        return status, payload, response_headers

    def control(self, method: str, path: str, body: Any = None) -> tuple[int, Any]:
        data = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
        request = urllib.request.Request(self.base_url.rsplit("/api/v1/lingdoc", 1)[0] + path,
                                         data=data, method=method)
        request.add_header("Authorization", "Bearer " + self.token)
        request.add_header("X-Tenant-ID", str(self.tenant_id))
        if data is not None:
            request.add_header("Content-Type", "application/json")
        try:
            response = urllib.request.urlopen(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        try:
            raw = response.read(4 * 1024 * 1024 + 1)
            content_type = header_value(response.headers, "Content-Type")
            payload = json.loads(raw.decode("utf-8")) if "json" in content_type.lower() else None
            return response.status, payload
        finally:
            response.close()


def data(payload: Any) -> Any:
    if not isinstance(payload, dict) or "data" not in payload:
        raise RuntimeError("provider response omitted data envelope")
    return payload["data"]


def must(status: int, expected: int, step: str, report: list[dict[str, Any]], payload: Any = None) -> Any:
    report.append({"step": step, "expected_http": expected, "actual_http": status,
                   "verdict": "passed" if status == expected else "failed"})
    if status != expected:
        code = ((payload or {}).get("error") or {}).get("code") if isinstance(payload, dict) else None
        raise RuntimeError(f"{step}: HTTP {status}, expected {expected}, code={code or 'unavailable'}")
    return payload


def build_state(run: LiveRun, knowledge_id: str, state_id: str) -> tuple[StateLoader, dict[str, Any]]:
    state = load_state(SCENARIOS_PATH, state_id)
    knowledge = Bindings("knowledge", "knowledge")
    member = Bindings("member", "user")
    for asset in state.assets:
        knowledge.bind(asset.knowledge_id, knowledge_id)
    loader = StateLoader(run.openapi, state, run.client, knowledge, member)
    loader.preflight()
    loader.build()
    readback = loader.read_back()
    if readback["mismatches"]:
        raise RuntimeError(f"{state_id} starting-state readback mismatched")
    return loader, readback


def run_live(base_url: str, token: str, source_tenant_id: int, knowledge_base_id: str,
             source_knowledge_id: str, outsider_token: str,
             report_path: Path = REPORT_PATH, timeout: float = 120) -> dict[str, Any]:
    steps: list[dict[str, Any]] = []
    controls: list[dict[str, Any]] = []
    report: dict[str, Any] = {
        "issue": 35,
        "mode": "real_http_cross_tenant_revocation",
        "provider_host": urlsplit(base_url).netloc,
        "starting_states": {},
        "scenarios": {},
        "registered_gaps": [],
        "cleanup": controls,
        "scope": "local authenticated HTTP observations; no endpoint implementation changes",
    }
    consumer_id: int | None = None
    org_id: str | None = None
    kb_share_id: str | None = None
    restored_share_id: str | None = None
    owner_run = LiveRun(base_url, token, source_tenant_id)
    try:
        status, payload = owner_run.control("POST", "/api/v1/tenants", {
            "name": f"t15-08-consumer-{int(time.time())}",
            "description": "Temporary consumer tenant for T15-08 live scenario",
        })
        tenant = data(must(status, 201, "create-consumer-tenant", steps, payload))
        consumer_id = int(tenant["id"])
        owner_run = LiveRun(base_url, token, source_tenant_id)

        status, payload = owner_run.control("POST", "/api/v1/organizations", {
            "name": f"t15-08-revocation-{int(time.time())}",
            "description": "Temporary T15-08 authorization fixture",
        })
        org = data(must(status, 201, "create-organization", steps, payload))
        org_id = org["id"]

        status, payload = owner_run.control("POST", f"/api/v1/organizations/{org_id}/invite",
                                            {"tenant_id": consumer_id, "role": "viewer"})
        must(status, 200, "invite-consumer-tenant", steps, payload)

        status, payload = owner_run.control("POST", f"/api/v1/knowledge-bases/{knowledge_base_id}/shares",
                                            {"organization_id": org_id, "permission": "viewer"})
        share = data(must(status, 201, "share-source-knowledge-base", steps, payload))
        kb_share_id = share["id"]

        consumer_run = LiveRun(base_url, token, consumer_id)
        status, member_payload = owner_run.control("GET", f"/api/v1/organizations/{org_id}/members")
        members = data(must(status, 200, "verify-organization-member", steps, member_payload))
        member_rows = (members if isinstance(members, list) else
                       members.get("members", members.get("items", [])) if isinstance(members, dict) else [])
        consumer_joined = any(str(row.get("tenant_id")) == str(consumer_id)
                              for row in member_rows if isinstance(row, dict))
        status, share_payload = owner_run.control("GET", f"/api/v1/knowledge-bases/{knowledge_base_id}/shares")
        share_rows = data(must(status, 200, "verify-knowledge-base-share", steps, share_payload))
        share_rows = share_rows if isinstance(share_rows, list) else share_rows.get("shares", share_rows.get("items", [])) \
            if isinstance(share_rows, dict) else []
        share_visible = any(row.get("id") == kb_share_id and row.get("organization_id") == org_id
                            for row in share_rows if isinstance(row, dict))
        report["share_preflight"] = {"consumer_is_organization_member": consumer_joined,
                                     "share_is_visible_to_owner": share_visible}
        if not consumer_joined or not share_visible:
            raise RuntimeError("temporary cross-tenant share fixture did not read back as declared")
        loader, state_readback = build_state(consumer_run, source_knowledge_id, "S5")
        report["starting_states"]["F07"] = {"status": "read_back_match", "state": "S5",
                                             "build_steps": len(loader.build_steps),
                                             "readback_steps": len(loader.readback_steps)}
        project_id = loader.identifiers["project_id"]
        asset_id = loader.asset_id("k-demo")
        question_id = loader.identifiers["chapter.question"]
        method_id = loader.identifiers["chapter.method"]

        status, payload, _ = consumer_run.call("retrieveSources", {"projectId": project_id}, {
            "query": "合成测试材料中的测试内容",
            "asset_ids": [asset_id],
        }, {"Idempotency-Key": "t15-08-retrieve-source-0001"})
        sources = data(must(status, 200, "retrieve-source-before-revoke", steps, payload))
        if not sources:
            raise RuntimeError("retrieve-source-before-revoke returned no usable source")
        source_id = sources[0]["id"]

        status, payload, _ = consumer_run.call("startGeneration", {"projectId": project_id}, {
            "chapter_id": question_id,
            "asset_ids": [asset_id],
            "instruction": "基于指定合成资料起草研究问题，标出待核实项。",
            "expected_spec_revision": 1,
            "expected_chapter_version_id": None,
        }, {"Idempotency-Key": "t15-08-generation-0001"})
        generation = data(must(status, 202, "start-generation-before-revoke", steps, payload))
        run_id = generation["id"]
        deadline = time.monotonic() + timeout
        while True:
            status, payload, _ = consumer_run.call("getGeneration", {"projectId": project_id, "runId": run_id})
            must(status, 200, "poll-generation-before-revoke", steps, payload)
            generation = data(payload)
            if generation.get("status") in {"succeeded", "failed", "cancelled"}:
                break
            if time.monotonic() >= deadline:
                raise RuntimeError("generation did not reach a terminal state before timeout")
            time.sleep(1)
        if generation.get("status") != "succeeded" or not generation.get("candidate_id"):
            raise RuntimeError("generation did not produce a candidate")
        candidate_id = generation["candidate_id"]

        # Create a source-backed chapter and a verified export before revocation. This is
        # the durable derivative needed to test download authorization after share removal.
        status, payload, _ = consumer_run.call("saveChapter", {"projectId": project_id, "chapterId": question_id}, {
            "expected_chapter_version_id": None,
            "expected_spec_revision": 1,
            "body_markdown": f"合成研究问题由授权资料支持 [[source:{source_id}]]。",
            "source_ids": [source_id],
        }, {"Idempotency-Key": "t15-08-question-save-0001"})
        saved = data(must(status, 201, "save-source-backed-question", steps, payload))
        status, payload, _ = consumer_run.call("confirmChapter", {"projectId": project_id, "chapterId": question_id}, {
            "expected_chapter_version_id": saved["current_version_id"],
            "expected_spec_revision": 1,
            "review_decisions": [],
        }, {"Idempotency-Key": "t15-08-question-confirm-0001"})
        must(status, 201, "confirm-source-backed-question", steps, payload)
        status, payload, _ = consumer_run.call("saveChapter", {"projectId": project_id, "chapterId": method_id}, {
            "expected_chapter_version_id": None,
            "expected_spec_revision": 1,
            "body_markdown": f"合成内容供撤权测试使用 [[source:{source_id}]]。",
            "source_ids": [source_id],
        }, {"Idempotency-Key": "t15-08-method-save-0001"})
        saved = data(must(status, 201, "save-source-backed-chapter", steps, payload))
        method_version = saved["current_version_id"]
        status, payload, _ = consumer_run.call("confirmChapter", {"projectId": project_id, "chapterId": method_id}, {
            "expected_chapter_version_id": method_version,
            "expected_spec_revision": 1,
            "review_decisions": [],
        }, {"Idempotency-Key": "t15-08-method-confirm-0001"})
        must(status, 201, "confirm-source-backed-chapter", steps, payload)

        status, payload, _ = consumer_run.call("getProject", {"projectId": project_id})
        project = data(must(status, 200, "read-project-version-for-release", steps, payload))
        status, payload, _ = consumer_run.call("prepareRelease", {"projectId": project_id}, {
            "expected_project_version": project["project_version"],
        }, {"Idempotency-Key": "t15-08-release-0001"})
        snapshot = data(must(status, 201, "prepare-source-backed-release", steps, payload))
        status, payload, _ = consumer_run.call("startExport", {"projectId": project_id,
                                                                "snapshotId": snapshot["id"]},
                                               {"format": "docx"},
                                               {"Idempotency-Key": "t15-08-export-0001"})
        export = data(must(status, 202, "start-source-backed-export", steps, payload))
        export_id = export["id"]
        deadline = time.monotonic() + timeout
        while True:
            status, payload, _ = consumer_run.call("getExport", {"projectId": project_id, "exportId": export_id})
            must(status, 200, "poll-export-before-revoke", steps, payload)
            export = data(payload)
            if export.get("status") in {"verified", "failed"}:
                break
            if time.monotonic() >= deadline:
                raise RuntimeError("export did not reach a terminal state before timeout")
            time.sleep(1)
        if export.get("status") != "verified":
            raise RuntimeError("source-backed export did not verify")

        # F04 uses an actual project, candidate and verified export. The separate
        # u-member identity belongs to the source tenant, not the consumer project.
        outsider_run = LiveRun(base_url, outsider_token, source_tenant_id)
        f04 = []
        checks = (
            ("getProject", {"projectId": project_id}, "project"),
            ("getCandidate", {"projectId": project_id, "candidateId": candidate_id}, "candidate"),
            ("downloadExport", {"projectId": project_id, "exportId": export_id}, "export_download"),
        )
        for operation, params, label in checks:
            status, _, headers = outsider_run.call(operation, params)
            f04.append({"step": label, "operation_id": operation, "expected_http": 404,
                        "actual_http": status, "content_type": header_value(headers, "Content-Type"),
                        "verdict": "passed" if status == 404 else "failed"})
        report["scenarios"]["F04"] = {"verdict": "failed" if any(s["verdict"] == "failed" for s in f04)
                                      else "passed", "steps": f04}
        report["starting_states"]["F04"] = {"state": "S5", "status": "read_back_match"}

        # The shared data is now actually revoked; these four requests use the consumer
        # tenant's same identity, so project membership remains valid throughout.
        status, payload = owner_run.control("DELETE", f"/api/v1/knowledge-bases/{knowledge_base_id}/shares/{kb_share_id}")
        must(status, 200, "revoke-source-share", steps, payload)
        kb_share_id = None

        f07 = []
        checks = (
            ("getSource", {"projectId": project_id, "sourceId": source_id}, 403, "source"),
            ("getCandidate", {"projectId": project_id, "candidateId": candidate_id}, 403, "candidate"),
            ("listChapters", {"projectId": project_id}, 403, "chapters"),
            ("downloadExport", {"projectId": project_id, "exportId": export_id}, 403, "export_download"),
        )
        for operation, params, expected, label in checks:
            status, payload, headers = consumer_run.call(operation, params)
            f07.append({"step": label, "operation_id": operation, "expected_http": expected,
                        "actual_http": status, "content_type": header_value(headers, "Content-Type"),
                        "verdict": "passed" if status == expected else "failed"})
        report["scenarios"]["F07"] = {"verdict": "failed" if any(s["verdict"] == "failed" for s in f07)
                                      else "passed", "steps": f07}
        report["registered_gaps"] = [
            {"id": "T15-08-R1", "scenario_step": "F07-03 listChapters",
             "expected": 403, "actual": next(s["actual_http"] for s in f07 if s["step"] == "chapters"),
             "owner": "workspace chapter-read owner (T08), with source authorization supplied by T09",
             "status": "open"},
            {"id": "T15-08-R2", "scenario_step": "F07-04 downloadExport",
             "expected": 403, "actual": next(s["actual_http"] for s in f07 if s["step"] == "export_download"),
             "owner": "delivery/export owner (T14)", "status": "open"},
        ]

        # F17: restricted status must contain recovery actions only, restore returns to
        # available, then permanent revocation starts a clean project with no old data.
        status, payload, _ = consumer_run.call("getAccessStatus", {"projectId": project_id})
        data(must(status, 200, "access-status-after-revoke", steps, payload))
        access = data(payload)
        action_set = set(access.get("recovery_actions") or [])
        access_shape_ok = set(access) <= {"project_id", "content_access", "recovery_actions", "can_create_project"}
        f17 = [{"step": "restricted_status", "actual": access.get("content_access"),
                "expected": "restricted", "verdict": "passed" if access.get("content_access") == "restricted" else "failed"},
               {"step": "restricted_status_has_no_sensitive_fields", "actual": access_shape_ok,
                "expected": True, "verdict": "passed" if access_shape_ok else "failed"},
               {"step": "recovery_actions", "actual": sorted(action_set),
                "expected": ["create_clean_project", "restore_source_authorization"],
                "verdict": "passed" if {"create_clean_project", "restore_source_authorization"} <= action_set else "failed"}]
        status, restored = owner_run.control("POST", f"/api/v1/knowledge-bases/{knowledge_base_id}/shares",
                                             {"organization_id": org_id, "permission": "viewer"})
        restored_share = data(must(status, 201, "restore-source-share", steps, restored))
        restored_share_id = restored_share["id"]
        status, payload, _ = consumer_run.call("getAccessStatus", {"projectId": project_id})
        must(status, 200, "access-status-after-restore", steps, payload)
        access = data(payload)
        f17.append({"step": "restored_status", "actual": access.get("content_access"), "expected": "available",
                    "verdict": "passed" if access.get("content_access") == "available" else "failed"})
        status, payload = owner_run.control("DELETE", f"/api/v1/knowledge-bases/{knowledge_base_id}/shares/{restored_share_id}")
        must(status, 200, "permanent-source-revoke", steps, payload)
        restored_share_id = None
        clean_loader, _ = build_state(consumer_run, source_knowledge_id, "S1")
        clean_project_id = clean_loader.identifiers["project_id"]
        clean_question_id = clean_loader.identifiers["chapter.question"]
        clean_text = "人工输入的新项目允许内容，不引用旧资料。"
        status, payload, _ = consumer_run.call("saveChapter", {"projectId": clean_project_id,
                                                                  "chapterId": clean_question_id}, {
            "expected_chapter_version_id": None,
            "expected_spec_revision": 1,
            "body_markdown": clean_text,
            "source_ids": [],
        }, {"Idempotency-Key": "t15-08-clean-question-save-0001"})
        must(status, 201, "write-allowed-content-to-clean-project", steps, payload)
        status, assets_payload, _ = consumer_run.call("listAssets", {"projectId": clean_project_id})
        assets = data(must(status, 200, "clean-project-assets", steps, assets_payload))
        status, chapter_payload, _ = consumer_run.call("listChapters", {"projectId": clean_project_id})
        chapters = data(must(status, 200, "clean-project-chapters", steps, chapter_payload))
        by_section = {chapter.get("section_id"): chapter for chapter in chapters}
        clean = (not assets and by_section.get("question", {}).get("body_markdown") == clean_text
                 and not by_section.get("question", {}).get("source_ids")
                 and not by_section.get("method", {}).get("body_markdown")
                 and not by_section.get("method", {}).get("source_ids"))
        f17.append({"step": "clean_project_has_no_copied_content", "actual": clean, "expected": True,
                    "asset_count": len(assets), "chapter_count": len(chapters),
                    "verdict": "passed" if clean else "failed"})
        report["scenarios"]["F17"] = {"verdict": "failed" if any(s["verdict"] == "failed" for s in f17)
                                      else "passed", "steps": f17}
        report["summary"] = {"passed": sum(x["verdict"] == "passed" for x in report["scenarios"].values()),
                             "failed": sum(x["verdict"] == "failed" for x in report["scenarios"].values()),
                             "not_run": 0}
        report["steps"] = steps
        report["starting_states"]["F17"] = {"state": "S1", "status": "read_back_match"}
        return report
    finally:
        if restored_share_id:
            status, _ = owner_run.control("DELETE", f"/api/v1/knowledge-bases/{knowledge_base_id}/shares/{restored_share_id}")
            controls.append({"cleanup": "restored_share", "http": status})
        if kb_share_id:
            status, _ = owner_run.control("DELETE", f"/api/v1/knowledge-bases/{knowledge_base_id}/shares/{kb_share_id}")
            controls.append({"cleanup": "source_share", "http": status})
        if org_id:
            status, _ = owner_run.control("DELETE", f"/api/v1/organizations/{org_id}")
            controls.append({"cleanup": "organization", "http": status})
        if consumer_id:
            consumer_admin = LiveRun(base_url, token, consumer_id)
            status, _ = consumer_admin.control("DELETE", f"/api/v1/tenants/{consumer_id}")
            controls.append({"cleanup": "consumer_tenant", "http": status})
        report.setdefault("steps", steps)
        report["runner_status"] = "completed" if "summary" in report else "failed"
        report_path.parent.mkdir(parents=True, exist_ok=True)
        report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--local-facts-path", type=Path, required=True,
                        help="existing local fixture helper providing login and ready knowledge IDs")
    parser.add_argument("--timeout", type=float, default=240)
    args = parser.parse_args()
    if not args.local_facts_path.is_file():
        parser.error("local fixture helper does not exist")
    facts = runpy.run_path(str(args.local_facts_path))
    token = facts["login"]()[1]["token"]
    facts["ensure_member"](token)
    member_status, member = facts["call"]("/api/v1/auth/login", {
        "email": facts["MEMBER_EMAIL"], "password": facts["MEMBER_PASSWORD"],
    })
    if member_status != 200 or not isinstance(member, dict) or not member.get("token"):
        parser.error(f"non-member fixture login failed with HTTP {member_status}")
    outsider = member["token"]
    knowledge_base_id = facts["ensure_kb"](token)[0]["id"]
    knowledge_id = facts["ensure_knowledge"](token, knowledge_base_id, facts["READY_TITLE"], "ready")[0]["id"]
    report = run_live("http://127.0.0.1:8080/api/v1/lingdoc", token, facts["TENANT"],
                      knowledge_base_id, knowledge_id, outsider, timeout=args.timeout)
    print(json.dumps({"runner_status": report.get("runner_status"), "summary": report.get("summary"),
                      "scenarios": {key: value.get("verdict") for key, value in report.get("scenarios", {}).items()},
                      "registered_gaps": report.get("registered_gaps"), "cleanup": report.get("cleanup")},
                     ensure_ascii=False))
    return 0 if report.get("runner_status") == "completed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
