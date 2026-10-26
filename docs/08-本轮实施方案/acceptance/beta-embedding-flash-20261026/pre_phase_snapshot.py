"""Pre-phase snapshot of the isolated acceptance environment (read-only).

Captured BEFORE any change in this round, so the report's "before" column is a
fact, not a recollection:

  * both ledger files (byte size + sha256 + every request row),
  * the database: model rows, knowledge bases, knowledges, chunks,
    lite_embeddings rows, vector tables, DB-side model call ledger,
  * the Redis databases the asynq queues live in (server registrations and
    pending task keys), so a cross-profile consumption or a leftover task is
    visible BEFORE the new process starts.
"""

import hashlib
import json
import pathlib
import socket
import sqlite3
import sys
import time

ROOT = pathlib.Path(r"D:\wt\lingdoc-484-evidence")
DB = ROOT / "data" / "lingdoc-484-final.db"
LEDGER_DIR = ROOT / "ledger"
OUT = pathlib.Path(
    sys.argv[1] if len(sys.argv) > 1 else
    r"D:\wt\lingdoc-485-evidence\embedding-run\flash-run\pre-phase-snapshot.json")

TARGET_KB = "7c104e1b-eb03-48ff-9d79-8a62b73cf082"
ISO_KB = "895c0a35-e7d5-4c28-b374-16f5d694ddea"
CHAT_MODEL = "lingdoc-chat-qwen3.8-flash-iso"
EMBED_MODEL = "lingdoc-embed-qwen3.7-text-embedding-iso"

REDIS_HOST, REDIS_PORT = "127.0.0.1", 6381
REDIS_DBS = [0, 1, 2, 3, 4, 5, 6]


class Resp:
    def __init__(self, sock):
        self.sock = sock
        self.buf = b""

    def _read_line(self):
        while b"\r\n" not in self.buf:
            chunk = self.sock.recv(65536)
            if not chunk:
                raise RuntimeError("connection closed")
            self.buf += chunk
        line, self.buf = self.buf.split(b"\r\n", 1)
        return line

    def _read_exact(self, n):
        while len(self.buf) < n:
            chunk = self.sock.recv(65536)
            if not chunk:
                raise RuntimeError("connection closed")
            self.buf += chunk
        out, self.buf = self.buf[:n], self.buf[n:]
        return out

    def read(self):
        line = self._read_line()
        kind, rest = line[:1], line[1:]
        if kind == b"+":
            return rest.decode()
        if kind == b"-":
            return "ERR:" + rest.decode()
        if kind == b":":
            return int(rest)
        if kind == b"$":
            n = int(rest)
            if n == -1:
                return None
            return self._read_exact(n + 2)[:-2].decode(errors="replace")
        if kind == b"*":
            n = int(rest)
            if n == -1:
                return None
            return [self.read() for _ in range(n)]
        raise RuntimeError("unknown RESP kind: %r" % line)

    def cmd(self, *args):
        out = ("*%d\r\n" % len(args)).encode()
        for a in args:
            raw = a.encode() if isinstance(a, str) else a
            out += b"$" + str(len(raw)).encode() + b"\r\n" + raw + b"\r\n"
        self.sock.sendall(out)
        return self.read()


def sha256_of(path: pathlib.Path):
    if not path.is_file():
        return None
    return hashlib.sha256(path.read_bytes()).hexdigest()


def snapshot_ledger(path: pathlib.Path):
    if not path.is_file():
        return {"present": False, "path": str(path)}
    raw = path.read_bytes()
    info = {"present": True, "path": str(path), "bytes": len(raw),
            "sha256": hashlib.sha256(raw).hexdigest(), "mtime": path.stat().st_mtime}
    try:
        doc = json.loads(raw.decode("utf-8"))
    except Exception as exc:  # noqa: BLE001
        info["parse_error"] = str(exc)
        return info
    for key in ("profile", "max_calls", "cumulative_token_max", "reserved_tokens",
                "actual_tokens", "reserved_nano_cny", "actual_nano_cny",
                "max_reserve_nano_cny", "blocked", "blocked_reason"):
        if key in doc:
            info[key] = doc[key]
    info["request_count"] = len(doc.get("requests") or [])
    info["requests"] = doc.get("requests") or []
    return info


def snapshot_redis():
    out = {}
    try:
        sock = socket.create_connection((REDIS_HOST, REDIS_PORT), timeout=5)
    except OSError as exc:
        return {"error": str(exc)}
    r = Resp(sock)
    try:
        for db in REDIS_DBS:
            r.cmd("SELECT", str(db))
            servers = sorted(r.cmd("KEYS", "asynq:servers:*") or [])
            tasks = sorted(r.cmd("KEYS", "asynq:*:t:*") or [])
            pending = sorted(r.cmd("KEYS", "asynq:*:pending") or [])
            out["db=%d" % db] = {
                "server_registrations": servers,
                "pending_task_keys": tasks[:50],
                "pending_task_key_count": len(tasks),
                "pending_lists": pending,
            }
    finally:
        sock.close()
    return out


def main() -> int:
    report = {"taken_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"), "db": str(DB)}

    report["ledgers"] = {
        name: snapshot_ledger(LEDGER_DIR / name)
        for name in ("qwen-beta-sqlite.json", "qwen-beta-sqlite.json.calls.json",
                     "qwen-embedding-budget.json", "qwen-embedding-budget.json.calls.json")
    }
    report["provider_403_evidence"] = {
        "path": r"D:\wt\lingdoc-485-evidence\embedding-run\provider-403-response.txt",
        "sha256": sha256_of(pathlib.Path(
            r"D:\wt\lingdoc-485-evidence\embedding-run\provider-403-response.txt")),
    }

    con = sqlite3.connect("file:%s?mode=ro" % DB.as_posix(), uri=True)
    cur = con.cursor()

    cols = [c[1] for c in cur.execute("pragma table_info(models)").fetchall()]
    models = []
    for row in cur.execute("select * from models where deleted_at is null order by id").fetchall():
        rec = dict(zip(cols, row))
        raw = rec.get("parameters")
        if isinstance(raw, (bytes, bytearray)):
            raw = raw.decode("utf-8", "replace")
        params = json.loads(raw or "{}")
        api_key = str(params.get("api_key", ""))
        models.append({
            "id": rec["id"], "name": rec["name"], "display_name": rec["display_name"],
            "type": rec["type"], "source": rec["source"], "status": rec["status"],
            "tenant_id": rec["tenant_id"], "provider": params.get("provider"),
            "base_url": params.get("base_url"),
            "api_key_ciphertext_prefix_ok": api_key.startswith("enc:v1:"),
            "api_key_sha12": hashlib.sha256(api_key.encode()).hexdigest()[:12],
            "embedding_parameters": params.get("embedding_parameters"),
        })
    report["models"] = models
    report["chat_model_is_qwen3_8_flash"] = any(
        m["id"] == CHAT_MODEL and m["name"] == "qwen3.8-flash" for m in models)
    report["embedding_model_row"] = next(
        (m for m in models if m["id"] == EMBED_MODEL), None)

    kb_cols = [c[1] for c in cur.execute("pragma table_info(knowledge_bases)").fetchall()]
    kbs = []
    for row in cur.execute("select * from knowledge_bases where id in (?,?)",
                           (TARGET_KB, ISO_KB)).fetchall():
        rec = dict(zip(kb_cols, row))
        kbs.append({"id": rec["id"], "name": rec["name"], "tenant_id": rec["tenant_id"],
                    "embedding_model_id": rec["embedding_model_id"],
                    "vector_store_id": rec["vector_store_id"],
                    "indexing_strategy": (rec["indexing_strategy"].decode("utf-8", "replace")
                                          if isinstance(rec["indexing_strategy"], (bytes, bytearray))
                                          else rec["indexing_strategy"])})
    report["knowledge_bases"] = kbs

    kn_cols = [c[1] for c in cur.execute("pragma table_info(knowledges)").fetchall()]
    knowledges = []
    for row in cur.execute("select * from knowledges where knowledge_base_id in (?,?)",
                           (TARGET_KB, ISO_KB)).fetchall():
        rec = dict(zip(kn_cols, row))
        knowledges.append({"id": rec["id"], "knowledge_base_id": rec["knowledge_base_id"],
                            "title": rec["title"], "file_name": rec["file_name"],
                            "parse_status": rec["parse_status"],
                            "error_message": rec["error_message"]})
    report["knowledges"] = knowledges

    ch_cols = [c[1] for c in cur.execute("pragma table_info(chunks)").fetchall()]
    chunks = []
    for row in cur.execute(
            "select * from chunks where knowledge_base_id in (?,?) order by created_at, chunk_index",
            (TARGET_KB, ISO_KB)).fetchall():
        rec = dict(zip(ch_cols, row))
        content = rec.get("content") or ""
        chunks.append({
            "id": rec["id"], "knowledge_id": rec["knowledge_id"],
            "knowledge_base_id": rec["knowledge_base_id"], "chunk_index": rec["chunk_index"],
            "index_status": rec["index_status"], "is_enabled": rec["is_enabled"],
            "start_at": rec["start_at"], "end_at": rec["end_at"],
            "content_len_chars": len(content),
            "content_len_bytes": len(content.encode("utf-8")),
            "content_sha256": hashlib.sha256(content.encode("utf-8")).hexdigest(),
            "created_at": str(rec["created_at"]),
        })
    report["chunks"] = chunks

    le_cols = [c[1] for c in cur.execute("pragma table_info(lite_embeddings)").fetchall()]
    emb = []
    for row in cur.execute("select * from lite_embeddings order by id").fetchall():
        rec = dict(zip(le_cols, row))
        emb.append({"id": rec["id"], "chunk_id": rec["chunk_id"],
                    "knowledge_id": rec["knowledge_id"],
                    "knowledge_base_id": rec["knowledge_base_id"],
                    "dimension": rec["dimension"], "source_type": rec["source_type"],
                    "is_enabled": rec["is_enabled"]})
    report["lite_embeddings_rows"] = emb
    report["lite_embeddings_count"] = len(emb)
    report["lite_embeddings_rows_for_iso_kb"] = [
        r for r in emb if r["knowledge_base_id"] == ISO_KB]
    report["lite_embeddings_rows_for_target_kb"] = [
        r for r in emb if r["knowledge_base_id"] == TARGET_KB]

    report["vector_tables"] = [r[0] for r in cur.execute(
        "select name from sqlite_master where type='table' and name like 'vec_%'").fetchall()]
    report["vector_stores_rows"] = cur.execute(
        "select count(*) from vector_stores").fetchone()[0]

    lc_cols = [c[1] for c in cur.execute("pragma table_info(lingdoc_model_call_ledger)").fetchall()]
    ledger_rows = []
    for row in cur.execute("select * from lingdoc_model_call_ledger order by created_at").fetchall():
        rec = dict(zip(lc_cols, row))
        ledger_rows.append({"id": rec["id"], "kind": rec["kind"], "provider": rec["provider"],
                            "model_id": rec["model_id"], "run_mode": rec["run_mode"],
                            "source_kind": rec["source_kind"],
                            "input_tokens": rec["input_tokens"],
                            "output_tokens": rec["output_tokens"], "status": rec["status"]})
    report["db_model_call_ledger_rows"] = ledger_rows
    report["db_model_call_ledger_count"] = len(ledger_rows)

    cv_cols = [c[1] for c in cur.execute(
        "pragma table_info(lingdoc_chapter_versions)").fetchall()]
    source_ids = {}
    for row in cur.execute("select * from lingdoc_chapter_versions").fetchall():
        rec = dict(zip(cv_cols, row))
        source_ids[rec["id"]] = rec.get("source_ids_json")
    report["chapter_source_ids"] = source_ids
    cited = set()
    for value in source_ids.values():
        try:
            cited.update(json.loads(value or "[]"))
        except Exception:  # noqa: BLE001
            pass
    report["chapter_cited_chunk_ids"] = sorted(cited)
    report["chapter_cited_chunks_all_exist"] = sorted(cited) == sorted(
        c["id"] for c in chunks if c["id"] in cited)

    con.close()
    report["redis"] = snapshot_redis()

    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps(report, ensure_ascii=False, indent=2, default=str) + "\n",
                   encoding="utf-8")
    print("written:", OUT)
    print(json.dumps({
        "chat_ledger_sha256": report["ledgers"]["qwen-beta-sqlite.json.calls.json"].get("sha256"),
        "chat_ledger_calls_sha256": report["ledgers"]["qwen-beta-sqlite.json"].get("sha256"),
        "embedding_ledger_sha256": report["ledgers"]["qwen-embedding-budget.json.calls.json"].get("sha256"),
        "embedding_ledger_requests": report["ledgers"]["qwen-embedding-budget.json.calls.json"].get("request_count"),
        "embedding_ledger_blocked": report["ledgers"]["qwen-embedding-budget.json.calls.json"].get("blocked"),
        "embedding_model_name": (report["embedding_model_row"] or {}).get("name"),
        "chat_model_is_qwen3_8_flash": report["chat_model_is_qwen3_8_flash"],
        "lite_embeddings_count": report["lite_embeddings_count"],
        "iso_kb_emb_rows": len(report["lite_embeddings_rows_for_iso_kb"]),
        "target_kb_emb_rows": len(report["lite_embeddings_rows_for_target_kb"]),
        "vector_tables": report["vector_tables"],
        "db_model_call_ledger_count": report["db_model_call_ledger_count"],
        "chapter_cited_chunks_all_exist": report["chapter_cited_chunks_all_exist"],
        "provider_403_sha256": report["provider_403_evidence"]["sha256"],
    }, ensure_ascii=False, indent=2))
    for key, value in report["redis"].items():
        if isinstance(value, dict):
            print("redis %s: servers=%d pending_tasks=%d" % (
                key, len(value.get("server_registrations") or []),
                value.get("pending_task_key_count", -1)))
            for srv in (value.get("server_registrations") or []):
                print("    ", srv)
            for tsk in (value.get("pending_task_keys") or [])[:5]:
                print("     task:", tsk)
    return 0


if __name__ == "__main__":
    sys.exit(main())
