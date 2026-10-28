import json, pathlib
P = pathlib.Path(r"D:\wt\lingdoc-485-evidence\embedding-run\flash-run\preflight_db.json")
d = json.loads(P.read_text(encoding="utf-8"))

print("=== knowledge_bases ===")
for kb in d["knowledge_bases"]:
    print(" id=%s tenant=%s name=%s embed_model=%r" % (kb["id"], kb["tenant_id"], kb["name"], kb["embedding_model_id"]))
    print("     strategy=%s" % kb["indexing_strategy"])
print()
print("=== knowledges ===")
for k in d["knowledges"]:
    print(" id=%s kb=%s name=%r parse=%s index=%s chunk_count=%s err=%s" % (
        k["id"], k["knowledge_base_id"], k["name"], k["parse_status"], k["index_status"], k["chunk_count"], k["error_message"]))
print()
print("=== chunks ===")
for c in d["chunks"]:
    print(" id=%s kn=%s kb=%s idx=%s status=%s len=%s emb_null=%s emb_len=%s created=%s updated=%s" % (
        c["id"], c["knowledge_id"], c["knowledge_base_id"], c["chunk_index"], c["index_status"],
        c["content_len"], c["embedding_is_null"], c["embedding_len"], c["created_at"], c["updated_at"]))
print()
print("=== db_model_call_ledger ===")
print(" rows=%s" % d.get("db_model_call_ledger_rows"))
for r in d.get("db_model_call_ledger_models", []):
    print("  ", r)
print()
print("=== chapter_versions ===")
for c in d.get("chapter_versions", []):
    print("  ", c["id"], "|", c["title"], "|", c["source_ids"])
