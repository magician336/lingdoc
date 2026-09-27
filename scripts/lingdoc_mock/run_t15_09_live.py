#!/usr/bin/env python3
"""Run the T15-09 delivery scenarios against the local authenticated service.

F10 and F16 use the contract runner and build/read back their declared states.
F19 needs a temporary cross-tenant share so it is driven directly, including a real
DOCX download and a second download after revocation. F13 is recorded as not_run when
the public service exposes no way to inject bytes that fail its renderer/validator.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import runpy
import sys
import time
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.run_f01 import OPENAPI_PATH, SCENARIOS_PATH, header_value
from scripts.lingdoc_mock.run_scenario import run_scenarios
from scripts.lingdoc_mock.run_t15_08_live import LiveRun, data, must

REPORT_PATH = ROOT / "docs/08-本轮实施方案/T15-09-验证报告.json"
BASE_URL = "http://127.0.0.1:8080/api/v1/lingdoc"
DOCX_TYPE = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def canonical_digest(value: Any) -> str:
    encoded = json.dumps(value, ensure_ascii=False, sort_keys=True,
                         separators=(",", ":")).encode("utf-8")
    return sha256(encoded)


def record(report: list[dict[str, Any]], step: str, status: int, expected: int) -> None:
    report.append({"step": step, "expected_http": expected, "actual_http": status,
                   "verdict": "passed" if status == expected else "failed"})


def run_live(token: str, source_tenant_id: int, knowledge_base_id: str,
             source_knowledge_id: str, timeout: float = 180,
             report_path: Path = REPORT_PATH) -> dict[str, Any]:
    report: dict[str, Any] = {
        "issue": 36,
        "mode": "real_http_delivery_scenarios",
        "provider_host": urlsplit(BASE_URL).netloc,
        "scenarios": {},
        "registered_gaps": [],
        "cleanup": [],
        "scope": "local authenticated HTTP observations; contract steps plus explicit F19 checks",
    }
    consumer_id: int | None = None
    org_id: str | None = None
    share_id: str | None = None
    current_step = "initialize"
    owner = LiveRun(BASE_URL, token, source_tenant_id, timeout=timeout)
    try:
        # These independent scenarios have no external knowledge-base dependency.
        current_step = "F10/F16 contract scenarios"
        try:
            contract_report = run_scenarios(
                ["F10", "F16"], SCENARIOS_PATH, OPENAPI_PATH,
                knowledge={}, member={}, identities={}, base_url=BASE_URL,
                token=token, timeout=min(timeout, 30),
            )
            report["scenarios"]["F10"] = next(
                item for item in contract_report["scenarios"] if item["scenario"] == "F10")
            report["scenarios"]["F16"] = next(
                item for item in contract_report["scenarios"] if item["scenario"] == "F16")
            report["contract_runner"] = {
                "summary": contract_report["summary"],
                "executed": contract_report["executed"],
            }
        except Exception as error:  # preserve a precise scenario-level failure and continue to F19
            reason = str(error).replace(token, "[redacted]")
            report["scenarios"]["F10"] = {"verdict": "failed", "blocked_at": current_step,
                                           "reason": reason}
            report["scenarios"]["F16"] = {"verdict": "failed", "blocked_at": current_step,
                                           "reason": reason}

        f19_steps: list[dict[str, Any]] = []
        f19_stage = "create-consumer-tenant"
        try:
            status, payload = owner.control("POST", "/api/v1/tenants", {
                "name": f"t15-09-consumer-{int(time.time())}",
                "description": "Temporary consumer tenant for T15-09 live scenario",
            })
            consumer_id = int(data(must(status, 201, f19_stage, f19_steps, payload))["id"])
            owner = LiveRun(BASE_URL, token, source_tenant_id, timeout=timeout)

            f19_stage = "create-organization"
            status, payload = owner.control("POST", "/api/v1/organizations", {
                "name": f"t15-09-frozen-input-{int(time.time())}",
                "description": "Temporary T15-09 cross-tenant fixture",
            })
            org_id = data(must(status, 201, f19_stage, f19_steps, payload))["id"]

            f19_stage = "invite-consumer-tenant"
            status, payload = owner.control("POST", f"/api/v1/organizations/{org_id}/invite",
                                            {"tenant_id": consumer_id, "role": "viewer"})
            must(status, 200, f19_stage, f19_steps, payload)

            f19_stage = "share-source-knowledge-base"
            status, payload = owner.control("POST", f"/api/v1/knowledge-bases/{knowledge_base_id}/shares",
                                            {"organization_id": org_id, "permission": "viewer"})
            share_id = data(must(status, 201, f19_stage, f19_steps, payload))["id"]
            consumer = LiveRun(BASE_URL, token, consumer_id, timeout=timeout)

            f19_stage = "build-and-read-back-S5"
            from scripts.lingdoc_mock.run_t15_08_live import build_state
            loader, _ = build_state(consumer, source_knowledge_id, "S5")
            project_id = loader.identifiers["project_id"]
            question_id = loader.identifiers["chapter.question"]
            method_id = loader.identifiers["chapter.method"]
            asset_id = loader.asset_id("k-demo")

            f19_stage = "retrieve-source"
            status, payload, _ = consumer.call("retrieveSources", {"projectId": project_id}, {
                "query": "合成测试材料中的测试内容", "asset_ids": [asset_id],
            }, {"Idempotency-Key": "t15-09-retrieve-source-0001"})
            sources = data(must(status, 200, f19_stage, f19_steps, payload))
            if not sources:
                raise RuntimeError("retrieveSources returned no source")
            source_id = sources[0]["id"]

            f19_stage = "get-source-and-verify-quoted-text-hash"
            status, source_payload, _ = consumer.call("getSource", {
                "projectId": project_id, "sourceId": source_id,
            })
            source = data(must(status, 200, f19_stage, f19_steps, source_payload))
            quoted_hash_ok = sha256(source["quoted_text"].encode("utf-8")) == source["quoted_text_hash"]
            f19_steps.append({"step": "quoted_text_utf8_sha256", "actual": quoted_hash_ok,
                              "expected": True, "verdict": "passed" if quoted_hash_ok else "failed"})
            if not quoted_hash_ok:
                raise RuntimeError("getSource quoted_text_hash did not match UTF-8 text bytes")

            def save_and_confirm(chapter_id: str, chapter_text: str, source_ids: list[str],
                                 label: str, save_key: str, confirm_key: str) -> str:
                status, saved_payload, _ = consumer.call("saveChapter", {
                    "projectId": project_id, "chapterId": chapter_id,
                }, {
                    "expected_chapter_version_id": None,
                    "expected_spec_revision": 1,
                    "body_markdown": chapter_text,
                    "source_ids": source_ids,
                }, {"Idempotency-Key": save_key})
                saved = data(must(status, 201, f"{label}-save", f19_steps, saved_payload))
                status, confirmed_payload, _ = consumer.call("confirmChapter", {
                    "projectId": project_id, "chapterId": chapter_id,
                }, {
                    "expected_chapter_version_id": saved["current_version_id"],
                    "expected_spec_revision": 1,
                    "review_decisions": [],
                }, {"Idempotency-Key": confirm_key})
                must(status, 201, f"{label}-confirm", f19_steps, confirmed_payload)
                return saved["current_version_id"]

            f19_stage = "save-confirm-two-chapters"
            question_text = f"合成冻结复算问题 [[source:{source_id}]]。"
            save_and_confirm(question_id, question_text, [source_id], "question-v1",
                             "t15-09-question-save-0001", "t15-09-question-confirm-0001")
            method_text = f"合成方法章节用于冻结摘要测试 [[source:{source_id}]]。"
            save_and_confirm(method_id, method_text, [source_id], "method-v1",
                             "t15-09-method-save-0001", "t15-09-method-confirm-0001")
            status, confirmed_payload, _ = consumer.call("listChapters", {"projectId": project_id})
            confirmed_chapters = data(must(status, 200, "verify-confirmed-chapters", f19_steps,
                                           confirmed_payload))
            confirmation_state = {
                item["section_id"]: bool(item.get("confirmation_valid")) for item in confirmed_chapters
            }
            confirmations_ok = confirmation_state.get("question") and confirmation_state.get("method")
            f19_steps.append({"step": "both_chapters_confirmation_valid", "actual": confirmation_state,
                              "expected": {"question": True, "method": True},
                              "verdict": "passed" if confirmations_ok else "failed"})
            if not confirmations_ok:
                raise RuntimeError("chapter confirmation did not remain valid after confirming both chapters")

            f19_stage = "prepare-first-release"
            status, project_payload, _ = consumer.call("getProject", {"projectId": project_id})
            project = data(must(status, 200, f19_stage+"-getProject", f19_steps, project_payload))
            status, release_payload, _ = consumer.call("prepareRelease", {"projectId": project_id}, {
                "expected_project_version": project["project_version"],
            }, {"Idempotency-Key": "t15-09-release-first-0001"})
            first = data(must(status, 201, f19_stage, f19_steps, release_payload))
            first_id = first["id"]
            first_input = first["frozen_input"]
            first_digest = first["snapshot_digest"]
            first_check = first.get("check") or {}
            first_check_ok = first_check.get("status") == "passed"
            blocking_issues = [issue for issue in first_check.get("issues", [])
                               if issue.get("severity") == "blocking"]
            f19_steps.append({
                "step": "first_snapshot_check_status",
                "actual": first_check.get("status"),
                "blocking_rules": [issue.get("rule_id") for issue in blocking_issues],
                "blocking_codes": [issue.get("id", "").partition("-")[0] for issue in blocking_issues],
                "expected": "passed",
                "verdict": "passed" if first_check_ok else "failed",
            })
            frozen_question = next(item for item in first_input["chapters"]
                                   if item["chapter_id"] == question_id)
            digest_ok = canonical_digest(first_input) == first_digest
            frozen_marker_ids = set().union(*(
                set(re.findall(r"\[\[source:([A-Za-z0-9_-]+)\]\]", chapter["body_markdown"]))
                for chapter in first_input["chapters"]
            ))
            frozen_chapter_source_ids = set().union(*(
                set(chapter["source_ids"]) for chapter in first_input["chapters"]
            ))
            frozen_sources = {item["id"]: item for item in first_input["sources"]}
            frozen_source_hashes_ok = (
                set(frozen_sources) == {source_id}
                and all(sha256(item["quoted_text"].encode("utf-8")) == item["quoted_text_hash"]
                        for item in frozen_sources.values())
                and frozen_sources[source_id]["quoted_text_hash"] == source["quoted_text_hash"]
            )
            source_refs_ok = (frozen_marker_ids == frozen_chapter_source_ids == {source_id}
                              and set(frozen_question["source_ids"]) == {source_id}
                              and set(frozen_sources) == {source_id})
            f19_steps.extend([
                {"step": "first_snapshot_canonical_digest", "actual": digest_ok,
                 "expected": True, "verdict": "passed" if digest_ok else "failed"},
                {"step": "source_marker_ids_and_frozen_sources_match", "actual": source_refs_ok,
                 "expected": True, "verdict": "passed" if source_refs_ok else "failed"},
                {"step": "frozen_source_hash_matches_utf8_text_and_getSource", "actual": frozen_source_hashes_ok,
                 "expected": True, "verdict": "passed" if frozen_source_hashes_ok else "failed"},
            ])
            if not digest_ok or not source_refs_ok or not frozen_source_hashes_ok:
                raise RuntimeError("first frozen input digest or source-reference set mismatched")

            f19_stage = "edit-and-reconfirm-question"
            status, chapters_payload, _ = consumer.call("listChapters", {"projectId": project_id})
            chapters = data(must(status, 200, f19_stage+"-listChapters", f19_steps, chapters_payload))
            question = next(item for item in chapters if item["id"] == question_id)
            edited_question = f"合成冻结复算问题已编辑 [[source:{source_id}]]。"
            status, saved_payload, _ = consumer.call("saveChapter", {
                "projectId": project_id, "chapterId": question_id,
            }, {
                "expected_chapter_version_id": question["current_version_id"],
                "expected_spec_revision": 1,
                "body_markdown": edited_question,
                "source_ids": [source_id],
            }, {"Idempotency-Key": "t15-09-question-edit-0002"})
            saved = data(must(status, 201, f19_stage+"-save", f19_steps, saved_payload))
            status, confirm_payload, _ = consumer.call("confirmChapter", {
                "projectId": project_id, "chapterId": question_id,
            }, {
                "expected_chapter_version_id": saved["current_version_id"],
                "expected_spec_revision": 1,
                "review_decisions": [],
            }, {"Idempotency-Key": "t15-09-question-confirm-0002"})
            must(status, 201, f19_stage+"-confirm", f19_steps, confirm_payload)

            f19_stage = "prepare-second-release"
            status, project_payload, _ = consumer.call("getProject", {"projectId": project_id})
            project = data(must(status, 200, f19_stage+"-getProject", f19_steps, project_payload))
            status, release_payload, _ = consumer.call("prepareRelease", {"projectId": project_id}, {
                "expected_project_version": project["project_version"],
            }, {"Idempotency-Key": "t15-09-release-second-0001"})
            second = data(must(status, 201, f19_stage, f19_steps, release_payload))
            second_digest = second["snapshot_digest"]
            second_check = second.get("check") or {}
            second_check_ok = second_check.get("status") == "passed"
            blocking_issues = [issue for issue in second_check.get("issues", [])
                               if issue.get("severity") == "blocking"]
            f19_steps.append({
                "step": "second_snapshot_check_status",
                "actual": second_check.get("status"),
                "blocking_rules": [issue.get("rule_id") for issue in blocking_issues],
                "blocking_codes": [issue.get("id", "").partition("-")[0] for issue in blocking_issues],
                "expected": "passed",
                "verdict": "passed" if second_check_ok else "failed",
            })

            f19_stage = "read-back-old-release"
            status, old_payload, _ = consumer.call("getRelease", {
                "projectId": project_id, "snapshotId": first_id,
            })
            old = data(must(status, 200, f19_stage, f19_steps, old_payload))
            old_unchanged = old["snapshot_digest"] == first_digest == canonical_digest(old["frozen_input"])
            old_not_current = old.get("is_current") is False
            new_digest_changed = second_digest != first_digest
            f19_steps.extend([
                {"step": "old_frozen_input_digest_stays_unchanged", "actual": old_unchanged,
                 "expected": True, "verdict": "passed" if old_unchanged else "failed"},
                {"step": "old_snapshot_is_not_current", "actual": old_not_current,
                 "expected": True, "verdict": "passed" if old_not_current else "failed"},
                {"step": "edited_frozen_input_changes_digest", "actual": new_digest_changed,
                 "expected": True, "verdict": "passed" if new_digest_changed else "failed"},
            ])
            if not old_unchanged or not old_not_current or not new_digest_changed:
                raise RuntimeError("old snapshot immutability/currentness or new digest check failed")

            f19_stage = "start-export"
            status, export_payload, _ = consumer.call("startExport", {
                "projectId": project_id, "snapshotId": second["id"],
            }, {"format": "docx"}, {"Idempotency-Key": "t15-09-export-0001"})
            export_id = data(must(status, 202, f19_stage, f19_steps, export_payload))["id"]
            deadline = time.monotonic() + timeout
            while True:
                status, export_payload, _ = consumer.call("getExport", {
                    "projectId": project_id, "exportId": export_id,
                })
                export = data(must(status, 200, "poll-export", f19_steps, export_payload))
                if export.get("status") in {"verified", "failed"}:
                    break
                if time.monotonic() >= deadline:
                    raise RuntimeError("export did not reach a terminal status before timeout")
                time.sleep(1)
            verified = export.get("status") == "verified" and bool(export.get("file_sha256"))
            f19_steps.append({"step": "export_verified", "actual": verified, "expected": True,
                              "verdict": "passed" if verified else "failed"})
            if not verified:
                raise RuntimeError("export ended without a verified file")

            f19_stage = "download-and-compare-bytes"
            status, file_bytes, headers = consumer.call("downloadExport", {
                "projectId": project_id, "exportId": export_id,
            })
            must(status, 200, f19_stage, f19_steps, None)
            content_type_ok = DOCX_TYPE in header_value(headers, "Content-Type").lower()
            bytes_hash_ok = isinstance(file_bytes, bytes) and sha256(file_bytes) == export["file_sha256"]
            f19_steps.extend([
                {"step": "download_content_type", "actual": content_type_ok,
                 "expected": True, "verdict": "passed" if content_type_ok else "failed"},
                {"step": "download_sha256_matches_getExport", "actual": bytes_hash_ok,
                 "expected": True, "verdict": "passed" if bytes_hash_ok else "failed"},
            ])
            if not content_type_ok or not bytes_hash_ok:
                raise RuntimeError("download content type or file byte hash mismatched")

            f19_stage = "revoke-share"
            status, payload = owner.control("DELETE",
                f"/api/v1/knowledge-bases/{knowledge_base_id}/shares/{share_id}")
            must(status, 200, f19_stage, f19_steps, payload)
            share_id = None
            f19_stage = "retry-same-download-after-revocation"
            status, _, _ = consumer.call("downloadExport", {
                "projectId": project_id, "exportId": export_id,
            })
            record(f19_steps, f19_stage, status, 403)
            if status != 403:
                report["registered_gaps"].append({
                    "id": "T15-09-R1",
                    "scenario_step": "F19-17 retry-same-download-after-revocation",
                    "expected_http": 403,
                    "actual_http": status,
                    "owner": "delivery/export authorization owner (T14)",
                    "related_evidence": "T15-08-R2 / F07-04",
                    "status": "open",
                })

            report["scenarios"]["F19"] = {
                "verdict": "passed" if all(item["verdict"] == "passed" for item in f19_steps) else "failed",
                "steps": f19_steps,
                "starting_state": "S5",
                "reason": (None if status == 403 else
                           f"F19 已执行至末步；撤销资料分享后重试同一文件下载返回 HTTP {status}，预期 403。"),
                "source_display_title_change": {
                    "verdict": "not_run",
                    "reason": "当前公开 API 没有修改来源展示名的操作；未直接改数据库。",
                },
            }
        except Exception as error:
            safe_reason = str(error).replace(token, "[redacted]")
            report["scenarios"]["F19"] = {
                "verdict": "failed",
                "blocked_at": f19_stage,
                "reason": safe_reason,
                "steps": f19_steps,
                "starting_state": "S5",
            }

        report["scenarios"]["F13"] = {
            "verdict": "not_run",
            "blocked_at": "before-startExport",
            "reason": ("真实服务的 renderer 与 validator 使用同一 DeliveryDocument，公开 HTTP 没有注入损坏"
                       "产物或替换校验器的入口；正常导出始终校验服务生成的同一份字节。"),
        }
        report["summary"] = {
            "passed": sum(item.get("verdict") == "passed" for item in report["scenarios"].values()),
            "failed": sum(item.get("verdict") == "failed" for item in report["scenarios"].values()),
            "not_run": sum(item.get("verdict") == "not_run" for item in report["scenarios"].values()),
        }
        report["runner_status"] = "completed"
        return report
    except Exception as error:
        report["runner_status"] = "failed"
        report["fatal"] = {"blocked_at": current_step,
                           "reason": str(error).replace(token, "[redacted]")}
        return report
    finally:
        cleanup_actions = []
        if share_id:
            cleanup_actions.append(("source_share", lambda: owner.control(
                "DELETE", f"/api/v1/knowledge-bases/{knowledge_base_id}/shares/{share_id}")))
        if org_id:
            cleanup_actions.append(("organization", lambda: owner.control("DELETE", f"/api/v1/organizations/{org_id}")))
        if consumer_id:
            cleanup_actions.append(("consumer_tenant", lambda: LiveRun(
                BASE_URL, token, consumer_id, timeout=timeout).control("DELETE", f"/api/v1/tenants/{consumer_id}")))
        for name, action in cleanup_actions:
            try:
                status, _ = action()
                report["cleanup"].append({"item": name, "http": status,
                                          "verdict": "passed" if status == 200 else "failed"})
            except Exception as error:
                report["cleanup"].append({"item": name, "http": None, "verdict": "failed",
                                          "reason": str(error).replace(token, "[redacted]")})
        cleanup_failed = any(item.get("verdict") == "failed" for item in report["cleanup"])
        if report.get("fatal") or cleanup_failed:
            report["runner_status"] = "failed"
        else:
            report.setdefault("runner_status", "completed")
        report_path.parent.mkdir(parents=True, exist_ok=True)
        report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--local-facts-path", type=Path, required=True,
                        help="existing local fixture helper providing owner token and ready knowledge IDs")
    parser.add_argument("--timeout", type=float, default=240)
    args = parser.parse_args()
    if not args.local_facts_path.is_file():
        parser.error("local fixture helper does not exist")
    facts = runpy.run_path(str(args.local_facts_path))
    token = facts["login"]()[1]["token"]
    knowledge_base_id = facts["ensure_kb"](token)[0]["id"]
    knowledge_id = facts["ensure_knowledge"](
        token, knowledge_base_id, facts["READY_TITLE"], "ready")[0]["id"]
    report = run_live(token, facts["TENANT"], knowledge_base_id, knowledge_id, timeout=args.timeout)
    summary = {key: item.get("verdict") for key, item in report.get("scenarios", {}).items()}
    print(json.dumps({"runner_status": report.get("runner_status"), "summary": report.get("summary"),
                      "scenarios": summary, "cleanup": report.get("cleanup")}, ensure_ascii=False))
    return 0 if (report.get("runner_status") == "completed"
                 and report.get("summary", {}).get("failed", 0) == 0
                 and all(item.get("verdict") == "passed" for item in report.get("cleanup", []))) else 1


if __name__ == "__main__":
    raise SystemExit(main())
