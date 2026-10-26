"""Read-only preflight probe of the isolated database (no writes, no network).

It answers the questions the acceptance depends on:
  * which model rows exist, and what the embedding model's provider-facing
    name / endpoint / provider / dimension are;
  * which knowledge bases exist, which of them are vector-enabled, and which
    knowledge entries carry chunks;
  * what the chunk rows look like (ids, index_status, embedding presence);
  * which vector tables exist;
  * how many rows the DB-side model call ledger has.
"""

import hashlib
import json
import pathlib
import sqlite3
import sys

DB = pathlib.Path(r"D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db")
OUT = pathlib.Path(r"D:\wt\lingdoc-485-evidence\embedding-run\flash-run\preflight_db.json")

CHAT_ID = "lingdoc-chat-qwen3.8-flash-iso"
EMBED_ID = "lingdoc-embed-qwen3.7-text-embedding-iso"


def main() -> int:
    con = sqlite3.connect("file:%s?mode=ro" % DB.as_posix(), uri=True)
    cur = con.cursor()

    report: dict = {"db": str(DB)}

    # ---- tables ------------------------------------------------------------
    tables = [r[0] for r in cur.execute(
        "select name from sqlite_master where type='table' order by name").fetchall()]
    report["tables"] = tables
    report["vector_like_tables"] = [t for t in tables if "vec" in t.lower()]

    # ---- models -----------------------------------------------------------
    cols = [c[1] for c in cur.execute("pragma table_info(models)").fetchall()]
    report["models_columns"] = cols
    models = []
    for row in cur.execute(
            "select * from models where deleted_at is null order by id").fetchall():
        rec = dict(zip(cols, row))
        raw = rec.get("parameters")
        if isinstance(raw, (bytes, bytearray)):
            raw = raw.decode("utf-8", "replace")
        params = {}
        try:
            params = json.loads(raw or "{}")
        except Exception as exc:  # noqa: BLE001
            params = {"_parse_error": str(exc)}
        api_key = str(params.get("api_key", ""))
        models.append({
            "id": rec.get("id"),
            "name": rec.get("name"),
            "display_name": rec.get("display_name"),
            "type": rec.get("type"),
            "source": rec.get("source"),
            "status": rec.get("status"),
            "is_default": rec.get("is_default"),
            "tenant_id": rec.get("tenant_id"),
            "provider": params.get("provider"),
            "base_url": params.get("base_url"),
            "api_key_is_ciphertext": api_key.startswith("enc:v1:"),
            "api_key_len": len(api_key),
            "api_key_sha12": hashlib.sha256(api_key.encode()).hexdigest()[:12],
            "embedding_parameters": params.get("embedding_parameters"),
            "extra_config": params.get("extra_config"),
        })
    report["models"] = models
    report["chat_model_row_present"] = any(m["id"] == CHAT_ID for m in models)
    report["embedding_model_row_present"] = any(m["id"] == EMBED_ID for m in models)

    # ---- knowledge bases --------------------------------------------------
    kb_cols = [c[1] for c in cur.execute("pragma table_info(knowledge_bases)").fetchall()]
    report["knowledge_bases_columns"] = kb_cols
    kbs = []
    for row in cur.execute("select * from knowledge_bases order by created_at").fetchall():
        rec = dict(zip(kb_cols, row))
        kbs.append({
            "id": rec.get("id"),
            "name": rec.get("name"),
            "tenant_id": rec.get("tenant_id"),
            "embedding_model_id": rec.get("embedding_model_id"),
            "summary_model_id": rec.get("summary_model_id"),
            "indexing_strategy": rec.get("indexing_strategy"),
            "created_at": str(rec.get("created_at")),
        })
    report["knowledge_bases"] = kbs

    # ---- knowledge entries + chunks ---------------------------------------
    kn_cols = [c[1] for c in cur.execute("pragma table_info(knowledges)").fetchall()]
    report["knowledge_columns"] = kn_cols
    knowledges = []
    for row in cur.execute("select * from knowledges order by created_at").fetchall():
        rec = dict(zip(kn_cols, row))
        knowledges.append({
            "id": rec.get("id"),
            "knowledge_base_id": rec.get("knowledge_base_id"),
            "name": rec.get("name"),
            "parse_status": rec.get("parse_status"),
            "index_status": rec.get("index_status"),
            "chunk_count": rec.get("chunk_count"),
            "error_message": (str(rec.get("error_message"))[:200]
                              if rec.get("error_message") else None),
        })
    report["knowledges"] = knowledges

    ch_cols = [c[1] for c in cur.execute("pragma table_info(chunks)").fetchall()]
    report["chunks_columns"] = ch_cols
    chunks = []
    for row in cur.execute(
            "select * from chunks order by created_at").fetchall():
        rec = dict(zip(ch_cols, row))
        chunks.append({
            "id": rec.get("id"),
            "knowledge_id": rec.get("knowledge_id"),
            "knowledge_base_id": rec.get("knowledge_base_id"),
            "chunk_index": rec.get("chunk_index"),
            "index_status": rec.get("index_status"),
            "is_enabled": rec.get("is_enabled"),
            "content_len": len(rec.get("content") or ""),
            "embedding_is_null": rec.get("embedding") is None,
            "embedding_len": (len(rec["embedding"]) if rec.get("embedding") else 0),
            "created_at": str(rec.get("created_at")),
            "updated_at": str(rec.get("updated_at")),
        })
    report["chunks"] = chunks

    # ---- DB-side model call ledger ----------------------------------------
    try:
        lc = [c[1] for c in cur.execute("pragma table_info(lingdoc_model_call_ledger)").fetchall()]
        rows = cur.execute(
            "select * from lingdoc_model_call_ledger order by rowid").fetchall()
        report["db_model_call_ledger_columns"] = lc
        report["db_model_call_ledger_rows"] = len(rows)
        report["db_model_call_ledger_models"] = []
        for row in rows:
            rec = dict(zip(lc, row))
            report["db_model_call_ledger_models"].append({
                "model": rec.get("model"),
                "profile": rec.get("profile"),
                "status": rec.get("status"),
                "input_tokens": rec.get("input_tokens"),
                "output_tokens": rec.get("output_tokens"),
                "created_at": str(rec.get("created_at")),
            })
    except sqlite3.Error as exc:
        report["db_model_call_ledger_error"] = str(exc)

    # ---- chapter source ids ------------------------------------------------
    try:
        cv_cols = [c[1] for c in cur.execute(
            "pragma table_info(lingdoc_chapter_versions)").fetchall()]
        rows = cur.execute(
            "select * from lingdoc_chapter_versions order by rowid").fetchall()
        report["chapter_versions_columns"] = cv_cols
        report["chapter_versions"] = []
        for row in rows:
            rec = dict(zip(cv_cols, row))
            report["chapter_versions"].append({
                "id": rec.get("id"),
                "chapter_id": rec.get("chapter_id"),
                "title": rec.get("title"),
                "source_ids": rec.get("source_ids_json"),
            })
    except sqlite3.Error as exc:
        report["chapter_versions_error"] = str(exc)

    con.close()
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps(report, ensure_ascii=False, indent=2, default=str) + "\n", encoding="utf-8")
    print(json.dumps(report, ensure_ascii=False, indent=2, default=str))
    print("written:", OUT)
    return 0


if __name__ == "__main__":
    sys.exit(main())
