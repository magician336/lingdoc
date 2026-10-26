"""Final closure verification: every load-bearing claim of the report is
asserted here, against the recorded artifacts, so the report is not a
narrative but a set of checked facts."""

import hashlib
import json
import pathlib
import re
import sqlite3

BASE = pathlib.Path(r"D:\wt\lingdoc-485-evidence\embedding-run\flash-run")
PRE = json.loads((BASE / "pre-phase-snapshot.json").read_text(encoding="utf-8"))
POST = json.loads((BASE / "post-phase-snapshot.json").read_text(encoding="utf-8"))
PHASE_A = json.loads((BASE / "phase-a-result.json").read_text(encoding="utf-8"))
PHASE_B = json.loads((BASE / "phase-b-result.json").read_text(encoding="utf-8"))
LEDGER_PATH = pathlib.Path(r"D:\wt\lingdoc-484-evidence\ledger\qwen-embedding-budget.json.calls.json")
LEDGER = json.loads(LEDGER_PATH.read_text(encoding="utf-8"))
LOG = (BASE / "server-embedding-20261026-075556.log").read_text(encoding="utf-8", errors="replace")
STDOUT = (BASE / "server-embedding-20261026-075556.stdout.log").read_text(encoding="utf-8", errors="replace")
STDERR = (BASE / "server-embedding-20261026-075556.stderr.log").read_text(encoding="utf-8", errors="replace")
ALL_LOG = LOG + "\n" + STDOUT + "\n" + STDERR
STARTUP = (BASE / "startup-log-queue-identity.txt").read_text(encoding="utf-8")
DB = pathlib.Path(r"D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db")
TARGET_KB = "7c104e1b-eb03-48ff-9d79-8a62b73cf082"
ISO_KB = "895c0a35-e7d5-4c28-b374-16f5d694ddea"

checks = []


def check(name, condition, detail=""):
    checks.append({"check": name, "ok": bool(condition), "detail": str(detail)})


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


# ---- 1. the chat ledgers are byte-identical -------------------------------
for name in ("qwen-beta-sqlite.json", "qwen-beta-sqlite.json.calls.json"):
    before = PRE["ledgers"][name]["sha256"]
    after = POST["ledgers"][name]["sha256"]
    check("chat ledger byte-identical: " + name, before == after, before + " / " + after)

# ---- 2. the provider request count of this round --------------------------
new_rows = [r for r in LEDGER["requests"] if r["number"] > 1]
check("provider requests this round == 2", len(new_rows) == 2, len(new_rows))
check("both new rows are the flash model",
      all(r["model"] == "qwen3.7-text-embedding-flash" for r in new_rows),
      [r["model"] for r in new_rows])
check("both new rows are response_received with reported usage",
      all(r["status"] == "response_received" and r["usage_status"] == "reported" for r in new_rows),
      [(r["status"], r["usage_status"]) for r in new_rows])
check("the historical 403 row is preserved verbatim",
      LEDGER["requests"][0]["status"] == "http_error"
      and LEDGER["requests"][0]["model"] == "qwen3.7-text-embedding"
      and LEDGER["requests"][0]["input_upper_bound_tokens"] == 3085
      and LEDGER["requests"][0]["reserved_nano_cny"] == 1542500
      and LEDGER["requests"][0]["usage_status"] == "unavailable"
      and LEDGER["requests"][0].get("reconciliation") is not None,
      LEDGER["requests"][0]["status"])

# ---- 3. the budget ceilings ------------------------------------------------
check("the window is not blocked", LEDGER.get("blocked") is False, LEDGER.get("blocked"))
check("cumulative reserved tokens <= 8000", LEDGER["reserved_tokens"] <= 8000, LEDGER["reserved_tokens"])
check("cumulative reserved money <= 8000000 nano-CNY (Y0.008)",
      LEDGER["reserved_nano_cny"] <= LEDGER["max_reserve_nano_cny"], LEDGER["reserved_nano_cny"])
check("the actual billing of this round <= 8000000 nano-CNY",
      sum(r["actual_nano_cny"] for r in new_rows) <= 8000000,
      sum(r["actual_nano_cny"] for r in new_rows))
check("the flash price is applied (648 tokens -> 81000 nano-CNY)",
      new_rows[0]["actual_nano_cny"] == new_rows[0]["input_tokens"] * 125
      and new_rows[1]["actual_nano_cny"] == new_rows[1]["input_tokens"] * 125,
      [new_rows[0]["actual_nano_cny"], new_rows[1]["actual_nano_cny"]])

# ---- 4. the target knowledge base is untouched -----------------------------
def target_rows(doc):
    return sorted([(c["id"], c["chunk_index"], c["index_status"], c["start_at"], c["end_at"],
                    c["content_sha256"])
                   for c in doc["chunks"] if c["knowledge_base_id"] == TARGET_KB])
check("the target knowledge base's two chunk rows are unchanged",
      target_rows(PRE) == target_rows(POST), target_rows(POST))
check("the target knowledge base's vector rows are unchanged",
      PRE["lite_embeddings_rows_for_target_kb"] == POST["lite_embeddings_rows_for_target_kb"],
      POST["lite_embeddings_rows_for_target_kb"])

# ---- 5. the chapter binding ------------------------------------------------
check("the chapter source_ids are unchanged",
      PRE["chapter_source_ids"] == POST["chapter_source_ids"])
check("every cited chunk still exists",
      POST["chapter_cited_chunks_all_exist"] is True)

# ---- 6. the vector index ----------------------------------------------------
check("the vector knowledge base has 2 vector rows",
      len(POST["lite_embeddings_rows_for_iso_kb"]) == 2,
      len(POST["lite_embeddings_rows_for_iso_kb"]))
check("both vector rows carry dimension 1024",
      all(r["dimension"] == 1024 for r in POST["lite_embeddings_rows_for_iso_kb"]),
      [r["dimension"] for r in POST["lite_embeddings_rows_for_iso_kb"]])
check("the vec0 virtual table for 1024 exists",
      any(t.startswith("vec_embeddings_1024") for t in POST["vector_tables"]),
      POST["vector_tables"])
check("both indexed chunks are ready",
      all(c["index_status"] == "ready" for c in PHASE_A["chunks_after"]),
      [(c["chunk_index"], c["index_status"]) for c in PHASE_A["chunks_after"]])

# ---- 7. the equivalence of the indexed text --------------------------------
con = sqlite3.connect("file:%s?mode=ro" % DB.as_posix(), uri=True)
cur = con.cursor()
target_sha = [r[0] for r in cur.execute(
    "select content from chunks where knowledge_base_id = ? and deleted_at is null "
    "order by chunk_index", (TARGET_KB,)).fetchall()]
iso_sha = [r[0] for r in cur.execute(
    "select content from chunks where knowledge_base_id = ? and deleted_at is null "
    "order by chunk_index", (ISO_KB,)).fetchall()]
target_sha = [hashlib.sha256((t or "").encode()).hexdigest() for t in target_sha]
iso_sha = [hashlib.sha256((t or "").encode()).hexdigest() for t in iso_sha]
check("the vectorized chunks carry the target chunks' content byte-for-byte",
      target_sha == iso_sha and len(target_sha) == 2, (target_sha, iso_sha))
target_coords = [r for r in cur.execute(
    "select chunk_index, start_at, end_at from chunks where knowledge_base_id = ? "
    "and deleted_at is null order by chunk_index", (TARGET_KB,)).fetchall()]
iso_coords = [r for r in cur.execute(
    "select chunk_index, start_at, end_at from chunks where knowledge_base_id = ? "
    "and deleted_at is null order by chunk_index", (ISO_KB,)).fetchall()]
check("the vectorized chunks carry the target chunks' coordinates",
      target_coords == iso_coords and len(iso_coords) == 2, (target_coords, iso_coords))
historical = cur.execute(
    "select count(*) from chunks where knowledge_base_id = ? and deleted_at is not null "
    "and index_status = 'failed'", (ISO_KB,)).fetchone()[0]
check("the previous attempts' failed chunk rows are PRESERVED (soft-deleted, not erased)",
      historical == 6, historical)
titles = [r[0] for r in cur.execute(
    "select title from knowledges where knowledge_base_id in (?,?)", (TARGET_KB, ISO_KB)).fetchall()]
check("both knowledges carry the same title (the indexed-text prefix)",
      len(set(titles)) == 1 and titles[0] == "synth-chapter-c.md", titles)
ledger_db_rows = cur.execute("select count(*) from lingdoc_model_call_ledger").fetchone()[0]
chat_rows = cur.execute(
    "select count(*) from lingdoc_model_call_ledger where model_id = ?",
    ("lingdoc-chat-qwen3.8-flash-iso",)).fetchone()[0]
con.close()
check("the DB model call ledger still has only the 4 historical chat rows",
      ledger_db_rows == 4 and chat_rows == 4, (ledger_db_rows, chat_rows))

# ---- 8. the model rows -----------------------------------------------------
check("the embedding model row names the authorized flash model",
      POST["embedding_model_row"]["name"] == "qwen3.7-text-embedding-flash",
      POST["embedding_model_row"]["name"])
check("the chat model row is still qwen3.8-flash",
      POST["chat_model_is_qwen3_8_flash"] is True)

# ---- 9. the retrieval ------------------------------------------------------
hits = PHASE_B["hits"]
check("the search returned 2 hits", len(hits) == 2, len(hits))
check("every hit is an embedding match (match_type=0)",
      all(h["match_type"] == 0 for h in hits), [h["match_type"] for h in hits])
check("the scores descend (the vector order is the answer order)",
      hits[0]["score"] > hits[1]["score"], [h["score"] for h in hits])
check("the server logged the keyword channel returning 0 hits",
      "retriever: keywords, count: 0" in LOG)
check("the server logged the vector channel returning 2 hits",
      "retriever: vector, count: 2" in LOG)
check("the server logged 'vector=2, keyword=0' before the fusion",
      "Result count before fusion: vector=2, keyword=0" in LOG)
check("the server logged the query dimension 1024 and 2 matched rows",
      "vectorRetrieve: query_dim=1024" in LOG and "matched=2 rows" in LOG)
check("the server logged the two cosine distances",
      "distance=0.4011, score=0.5989" in LOG and "distance=0.5446, score=0.4554" in LOG)

# ---- 10. the queue identity ------------------------------------------------
queue_lines = [l for l in ALL_LOG.splitlines() if "queue_redis_db=" in l]
check("the startup log carries 6 queue identity lines", len(queue_lines) == 6, len(queue_lines))
check("every worker pool is on queue db 2 from the queue's own setting",
      all("queue_redis_db=2" in l and "queue_db_source=queue-db-env" in l for l in queue_lines))
check("the queue identity excerpt file records the same 6 pools",
      len([l for l in STARTUP.split("=== every line that mentions")[0].splitlines()
           if "queue_redis_db=" in l]) == 6)
db2 = POST["redis"]["db=2"]
db1 = POST["redis"]["db=1"]
check("the new process registered 6 worker pools on redis db 2",
      len(db2["server_registrations"]) - len(PRE["redis"]["db=2"]["server_registrations"]) == 6)
check("redis db 1 (the chat profile queue) gained no registration from this process",
      not any(":68840:" in k for k in db1["server_registrations"]))
check("no pending task is left in db 1", db1["pending_task_key_count"] == 0)
check("no pending task is left in db 2", db2["pending_task_key_count"] == 0)

# ---- 11. the artifacts -----------------------------------------------------
manifest = json.loads((BASE / "artifact-manifest.json").read_text(encoding="utf-8"))
missing = [m["path"] for m in manifest
           if not pathlib.Path(m["path"]).is_file()
           or sha(pathlib.Path(m["path"])) != m["sha256"]]
check("every artifact in the manifest still matches its recorded sha256",
      not missing, missing)

failed = [c for c in checks if not c["ok"]]
for c in checks:
    print("%-4s %-64s %s" % ("OK" if c["ok"] else "FAIL", c["check"], c["detail"][:90]))
print()
print("checks: %d, failed: %d" % (len(checks), len(failed)))
(BASE / "closure-verification.json").write_text(
    json.dumps({"checks": checks, "failed": len(failed)}, ensure_ascii=False, indent=2) + "\n",
    encoding="utf-8")
print("written:", BASE / "closure-verification.json")
raise SystemExit(1 if failed else 0)
