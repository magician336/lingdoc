"""Before/after comparison of this round (the report's comparison section).

It compares the pre-phase snapshot with the post-phase snapshot field by
field, so every claim in the report's comparison section is a fact about two
recorded snapshots and not a recollection.
"""

import json
import pathlib

BASE = pathlib.Path(r"D:\wt\lingdoc-485-evidence\embedding-run\flash-run")
PRE = BASE / "pre-phase-snapshot.json"
POST = BASE / "post-phase-snapshot.json"
OUT = BASE / "before-after-comparison.json"

TARGET_KB = "7c104e1b-eb03-48ff-9d79-8a62b73cf082"
ISO_KB = "895c0a35-e7d5-4c28-b374-16f5d694ddea"


def main():
    pre = json.loads(PRE.read_text(encoding="utf-8"))
    post = json.loads(POST.read_text(encoding="utf-8"))
    out = {}

    def ledger(pre_doc, name):
        return (pre_doc.get("ledgers") or {}).get(name) or {}

    for name in ("qwen-beta-sqlite.json", "qwen-beta-sqlite.json.calls.json",
                 "qwen-embedding-budget.json.calls.json"):
        a, b = ledger(pre, name), ledger(post, name)
        out["ledger:" + name] = {
            "sha256_before": a.get("sha256"),
            "sha256_after": b.get("sha256"),
            "byte_identical": a.get("sha256") == b.get("sha256"),
            "requests_before": a.get("request_count"),
            "requests_after": b.get("request_count"),
            "blocked_before": a.get("blocked"),
            "blocked_after": b.get("blocked"),
            "reserved_tokens_before": a.get("reserved_tokens"),
            "reserved_tokens_after": b.get("reserved_tokens"),
            "actual_tokens_before": a.get("actual_tokens"),
            "actual_tokens_after": b.get("actual_tokens"),
            "reserved_nano_cny_before": a.get("reserved_nano_cny"),
            "reserved_nano_cny_after": b.get("reserved_nano_cny"),
            "actual_nano_cny_before": a.get("actual_nano_cny"),
            "actual_nano_cny_after": b.get("actual_nano_cny"),
        }

    out["embedding_ledger_rows_after"] = [
        {
            "number": r.get("number"), "model": r.get("model"), "status": r.get("status"),
            "input_upper_bound_tokens": r.get("input_upper_bound_tokens"),
            "input_tokens": r.get("input_tokens"), "usage_status": r.get("usage_status"),
            "reserved_nano_cny": r.get("reserved_nano_cny"),
            "actual_nano_cny": r.get("actual_nano_cny"),
            "reconciled": r.get("reconciliation") is not None,
        }
        for r in (ledger(post, "qwen-embedding-budget.json.calls.json").get("requests") or [])
    ]

    # ---- the target knowledge base: it must NOT have changed ---------------
    def target_chunks(doc):
        return sorted(
            [{"id": c["id"], "knowledge_id": c["knowledge_id"], "chunk_index": c["chunk_index"],
              "index_status": c["index_status"], "start_at": c["start_at"], "end_at": c["end_at"],
              "content_sha256": c["content_sha256"], "content_len_bytes": c["content_len_bytes"]}
             for c in doc["chunks"] if c["knowledge_base_id"] == TARGET_KB],
            key=lambda c: c["chunk_index"])

    before_target = target_chunks(pre)
    after_target = target_chunks(post)
    out["target_kb_chunks"] = {
        "before": before_target,
        "after": after_target,
        "identical": before_target == after_target,
    }

    # ---- the vector knowledge base ---------------------------------------
    def iso_chunks(doc):
        return sorted(
            [{"id": c["id"], "knowledge_id": c["knowledge_id"], "chunk_index": c["chunk_index"],
              "index_status": c["index_status"], "start_at": c["start_at"], "end_at": c["end_at"],
              "content_sha256": c["content_sha256"], "created_at": c.get("created_at")}
             for c in doc["chunks"] if c["knowledge_base_id"] == ISO_KB],
            key=lambda c: (c["created_at"], c["chunk_index"]))

    out["iso_kb_chunks_before"] = iso_chunks(pre)
    out["iso_kb_chunks_after"] = iso_chunks(post)
    out["iso_kb_vector_rows_before"] = pre.get("lite_embeddings_rows_for_iso_kb")
    out["iso_kb_vector_rows_after"] = post.get("lite_embeddings_rows_for_iso_kb")
    out["target_kb_vector_rows_before"] = pre.get("lite_embeddings_rows_for_target_kb")
    out["target_kb_vector_rows_after"] = post.get("lite_embeddings_rows_for_target_kb")
    out["vector_tables_before"] = pre.get("vector_tables")
    out["vector_tables_after"] = post.get("vector_tables")
    out["lite_embeddings_count_before"] = pre.get("lite_embeddings_count")
    out["lite_embeddings_count_after"] = post.get("lite_embeddings_count")

    # ---- the chapter binding ----------------------------------------------
    out["chapter_source_ids_before"] = pre.get("chapter_source_ids")
    out["chapter_source_ids_after"] = post.get("chapter_source_ids")
    out["chapter_source_ids_identical"] = pre.get("chapter_source_ids") == post.get("chapter_source_ids")
    out["chapter_cited_chunks_all_exist_before"] = pre.get("chapter_cited_chunks_all_exist")
    out["chapter_cited_chunks_all_exist_after"] = post.get("chapter_cited_chunks_all_exist")

    # ---- the DB-side model call ledger ------------------------------------
    out["db_model_call_ledger_count_before"] = pre.get("db_model_call_ledger_count")
    out["db_model_call_ledger_count_after"] = post.get("db_model_call_ledger_count")
    out["db_model_call_ledger_rows_after"] = post.get("db_model_call_ledger_rows")

    # ---- the model rows ---------------------------------------------------
    out["embedding_model_before"] = pre.get("embedding_model_row")
    out["embedding_model_after"] = post.get("embedding_model_row")
    out["chat_model_is_qwen3_8_flash_after"] = post.get("chat_model_is_qwen3_8_flash")

    # ---- the queues -------------------------------------------------------
    def queue(doc, db):
        return (doc.get("redis") or {}).get(db) or {}

    for db in ("db=1", "db=2"):
        before = queue(pre, db)
        after = queue(post, db)
        out["redis:" + db] = {
            "servers_before": len(before.get("server_registrations") or []),
            "servers_after": len(after.get("server_registrations") or []),
            "pending_tasks_before": before.get("pending_task_key_count"),
            "pending_tasks_after": after.get("pending_task_key_count"),
            "new_registrations": sorted(
                set(after.get("server_registrations") or []) - set(before.get("server_registrations") or [])),
        }

    OUT.write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({
        "chat_ledger_byte_identical": out["ledger:qwen-beta-sqlite.json.calls.json"]["byte_identical"],
        "chat_ledger_base_byte_identical": out["ledger:qwen-beta-sqlite.json"]["byte_identical"],
        "target_kb_chunks_identical": out["target_kb_chunks"]["identical"],
        "chapter_source_ids_identical": out["chapter_source_ids_identical"],
        "chapter_cited_chunks_all_exist_after": out["chapter_cited_chunks_all_exist_after"],
        "db_model_call_ledger_count_before": out["db_model_call_ledger_count_before"],
        "db_model_call_ledger_count_after": out["db_model_call_ledger_count_after"],
        "iso_kb_vector_rows_before": len(out["iso_kb_vector_rows_before"] or []),
        "iso_kb_vector_rows_after": len(out["iso_kb_vector_rows_after"] or []),
        "target_kb_vector_rows_before": len(out["target_kb_vector_rows_before"] or []),
        "target_kb_vector_rows_after": len(out["target_kb_vector_rows_after"] or []),
        "vector_tables_after": out["vector_tables_after"],
        "redis_db1_pending_after": out["redis:db=1"]["pending_tasks_after"],
        "redis_db2_pending_after": out["redis:db=2"]["pending_tasks_after"],
        "redis_db2_new_registrations": out["redis:db=2"]["new_registrations"],
    }, ensure_ascii=False, indent=2))
    print("written:", OUT)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
