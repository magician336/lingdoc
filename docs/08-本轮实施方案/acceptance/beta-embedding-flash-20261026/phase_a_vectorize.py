"""PHASE A: one batch vectorization of the two chunks, through the real
application call chain.

What it does, in order:

  1. reads the embedding ledger (the only ledger the embedding path writes),
  2. records the chunk rows and the vector rows of the vector knowledge base,
  3. asks the application to RE-PROCESS the vector knowledge
     (POST /knowledge/{id}/reparse) -- the same route the previous round used,
     which parses the material and runs the indexing stage, including the
     embedding stage under the acceptance budget,
  4. waits for the pipeline to reach a terminal state and records:
     the chunk rows, their index_status, the vector rows (and their
     dimension), the vector table, and the ledger rows.

It makes NO provider call of its own: the only provider request in this phase
is the one the application makes for the two chunks, and the ledger proves how
many requests were made.
"""

import json
import pathlib
import sqlite3
import sys
import time
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:18096/api/v1"
KB = "895c0a35-e7d5-4c28-b374-16f5d694ddea"
KN = "c04d8954-dcc7-47dc-bc8b-8deb5f312ac2"
CREDS = pathlib.Path(r"D:\wt\lingdoc-484-evidence\owner-484-trackd5-token-credentials.txt")
LEDGER = pathlib.Path(r"D:\wt\lingdoc-484-evidence\ledger\qwen-embedding-budget.json.calls.json")
DB = pathlib.Path(r"D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db")
OUT = pathlib.Path(r"D:\wt\lingdoc-485-evidence\embedding-run\flash-run\phase-a-result.json")


def call(method, path, payload=None, token="", body=None, content_type=""):
    data = body
    headers = {"Accept": "application/json"}
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        headers["Content-Type"] = "application/json"
    elif content_type:
        headers["Content-Type"] = content_type
    if token:
        headers["Authorization"] = "Bearer " + token
    request = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=180) as response:
            raw = response.read()
            status = response.status
    except urllib.error.HTTPError as error:
        raw = error.read()
        status = error.code
    except urllib.error.URLError as error:
        return 0, {"error": str(error)}
    try:
        return status, json.loads(raw.decode("utf-8")) if raw else {}
    except (UnicodeDecodeError, json.JSONDecodeError):
        return status, {"raw_head": raw[:400].decode("utf-8", errors="replace")}


def credentials():
    values = {}
    for line in CREDS.read_text(encoding="utf-8").splitlines():
        if "=" in line and not line.strip().startswith("#"):
            key, _, value = line.partition("=")
            values[key.strip()] = value.strip()
    return values["email"], values["password"]


def ledger_rows():
    if not LEDGER.is_file():
        return []
    doc = json.loads(LEDGER.read_text(encoding="utf-8"))
    return doc.get("requests") or []


def ledger_state():
    doc = json.loads(LEDGER.read_text(encoding="utf-8"))
    return {
        "requests": len(doc.get("requests") or []),
        "blocked": doc.get("blocked"),
        "blocked_reason": doc.get("blocked_reason"),
        "reserved_tokens": doc.get("reserved_tokens"),
        "actual_tokens": doc.get("actual_tokens"),
        "reserved_nano_cny": doc.get("reserved_nano_cny"),
        "actual_nano_cny": doc.get("actual_nano_cny"),
        "max_reserve_nano_cny": doc.get("max_reserve_nano_cny"),
        "cumulative_token_max": doc.get("cumulative_token_max"),
    }


def db_state():
    con = sqlite3.connect("file:%s?mode=ro" % DB.as_posix(), uri=True)
    cur = con.cursor()
    chunks = cur.execute(
        "select id, chunk_index, index_status, is_enabled, start_at, end_at, "
        "length(content), created_at from chunks where knowledge_id = ? order by chunk_index",
        (KN,)).fetchall()
    emb_cols = [c[1] for c in cur.execute("pragma table_info(lite_embeddings)").fetchall()]
    rows = cur.execute(
        "select * from lite_embeddings where knowledge_base_id = ? order by id", (KB,)).fetchall()
    embeddings = []
    for row in rows:
        rec = dict(zip(emb_cols, row))
        embeddings.append({
            "id": rec["id"], "chunk_id": rec["chunk_id"], "knowledge_id": rec["knowledge_id"],
            "source_id": rec["source_id"], "source_type": rec["source_type"],
            "dimension": rec["dimension"], "is_enabled": rec["is_enabled"],
            "content_len": len(rec["content"] or ""),
        })
    vec_tables = [r[0] for r in cur.execute(
        "select name from sqlite_master where type='table' and name like 'vec_%' "
        "and name != 'vector_stores'").fetchall()]
    vec_counts = {}
    for table in vec_tables:
        try:
            vec_counts[table] = cur.execute("select count(*) from %s" % table).fetchone()[0]
        except sqlite3.Error as exc:
            vec_counts[table] = "err: %s" % exc
    fts = cur.execute(
        "select count(*) from lite_embeddings_fts e join lite_embeddings l on l.id = e.rowid "
        "where l.knowledge_base_id = ?", (KB,)).fetchone()[0]
    con.close()
    return {
        "chunk_rows": [list(r) for r in chunks],
        "vector_rows": embeddings,
        "vector_row_count": len(embeddings),
        "vector_tables": vec_tables,
        "vector_table_row_counts": vec_counts,
        "fts_rows_for_kb": fts,
    }


def main():
    result = {"phase": "A", "base": BASE, "kb": KB, "knowledge": KN,
              "started_at": time.strftime("%Y-%m-%dT%H:%M:%S%z")}

    result["ledger_before"] = ledger_state()
    result["ledger_rows_before"] = ledger_rows()
    result["db_before"] = db_state()
    print("ledger before:", json.dumps(result["ledger_before"], ensure_ascii=False))
    print("vector rows before:", result["db_before"]["vector_row_count"],
          "chunk rows before:", len(result["db_before"]["chunk_rows"]))

    email, password = credentials()
    status, login = call("POST", "/auth/login", {"email": email, "password": password})
    token = login.get("token") if isinstance(login, dict) else None
    result["login_status"] = status
    print("login:", status, "token acquired:", bool(token))
    if status != 200 or not token:
        result["failed_at"] = "login"
        OUT.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
        return 2

    status, body = call("POST", "/knowledge/%s/reparse" % KN, token=token)
    result["reparse_status"] = status
    result["reparse_body"] = body
    print("reparse:", status, json.dumps(body, ensure_ascii=False)[:300])

    deadline = time.time() + 600
    knowledge = {}
    last_status = None
    while time.time() < deadline:
        time.sleep(5)
        _, fetched = call("GET", "/knowledge/%s" % KN, token=token)
        data = fetched.get("data") if isinstance(fetched, dict) else None
        if isinstance(data, dict):
            knowledge = data
            if data.get("parse_status") != last_status:
                print("   parse_status:", data.get("parse_status"),
                      "error:", (data.get("error_message") or "")[:120])
                last_status = data.get("parse_status")
            if data.get("parse_status") not in ("processing", "pending", ""):
                break
    # The embedding stage runs after the parse reaches its terminal state in
    # some flows; give the stage a bounded grace period to finish.
    time.sleep(20)

    result["knowledge_after"] = {
        "parse_status": knowledge.get("parse_status"),
        "error_message": (knowledge.get("error_message") or "")[:400],
        "processed_at": knowledge.get("processed_at"),
        "embedding_model_id": knowledge.get("embedding_model_id"),
    }
    print("knowledge after:", json.dumps(result["knowledge_after"], ensure_ascii=False))

    status, chunks = call("GET", "/chunks/%s?page=1&page_size=100" % KN, token=token)
    items = chunks.get("data") if isinstance(chunks, dict) else None
    if isinstance(items, dict):
        items = items.get("items")
    if not isinstance(items, list):
        items = []
    chunk_rows = []
    for item in items:
        row = {
            "id": item.get("id"),
            "chunk_index": item.get("chunk_index"),
            "index_status": item.get("index_status"),
            "is_enabled": item.get("is_enabled"),
            "start_at": item.get("start_at"),
            "end_at": item.get("end_at"),
            "content_len": len(item.get("content") or ""),
            "content_head": (item.get("content") or "")[:100],
        }
        chunk_rows.append(row)
        print("  chunk:", row["chunk_index"], row["id"], "index_status=", row["index_status"],
              "start=", row["start_at"], "end=", row["end_at"])
    result["chunks_after"] = chunk_rows

    result["ledger_after"] = ledger_state()
    result["ledger_rows_after"] = ledger_rows()
    print("ledger after:", json.dumps(result["ledger_after"], ensure_ascii=False))
    for row in result["ledger_rows_after"]:
        print("  ledger:", row.get("number"), row.get("model"), row.get("status"),
              "bound=", row.get("input_upper_bound_tokens"), "in=", row.get("input_tokens"),
              "usage=", row.get("usage_status"),
              "reserved_cny=", row.get("reserved_nano_cny"), "actual_cny=", row.get("actual_nano_cny"))

    result["db_after"] = db_state()
    print("vector rows after:", result["db_after"]["vector_row_count"],
          "vector tables:", result["db_after"]["vector_tables"],
          result["db_after"]["vector_table_row_counts"])
    for row in result["db_after"]["vector_rows"]:
        print("  vector row:", row["id"], "chunk=", row["chunk_id"], "dim=", row["dimension"])

    result["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%S%z")
    OUT.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print("written:", OUT)
    return 0


if __name__ == "__main__":
    sys.exit(main())
