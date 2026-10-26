import json, pathlib, sqlite3

DB = pathlib.Path(r"D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db")
con = sqlite3.connect("file:%s?mode=ro" % DB.as_posix(), uri=True)
cur = con.cursor()

print("=== knowledges columns ===")
cols = [c[1] for c in cur.execute("pragma table_info(knowledges)").fetchall()]
print(cols)
for kn in ("c1197b70-d525-412c-8ef5-42c38cf7fd35", "c04d8954-dcc7-47dc-bc8b-8deb5f312ac2"):
    row = cur.execute("select * from knowledges where id = ?", (kn,)).fetchone()
    rec = dict(zip(cols, row))
    print()
    print("knowledge", kn)
    for k in ("id", "base_id", "title", "name", "parse_status", "source", "file_name",
              "file_type", "created_at", "updated_at", "metadata", "enable_status"):
        if k in rec:
            print("   %-14s %r" % (k, rec[k]))

print()
print("=== chunks columns ===")
ccols = [c[1] for c in cur.execute("pragma table_info(chunks)").fetchall()]
print(ccols)
for cid in ("1e849f5f-83e7-4138-a39f-d1aa1928054a", "4cd2d6a9-b258-47b3-9506-13482a3372f6"):
    row = cur.execute("select * from chunks where id = ?", (cid,)).fetchone()
    rec = dict(zip(ccols, row))
    print()
    print("chunk", cid)
    for k in ("id", "knowledge_id", "chunk_index", "index_status", "is_enabled", "content_revision",
              "context_header", "start_at", "end_at", "created_at"):
        if k in rec:
            v = rec[k]
            print("   %-18s %r" % (k, (str(v)[:160] if v is not None else None)))

print()
print("=== lingdoc_model_call_ledger columns ===")
lcols = [c[1] for c in cur.execute("pragma table_info(lingdoc_model_call_ledger)").fetchall()]
print(lcols)
for row in cur.execute("select * from lingdoc_model_call_ledger order by rowid").fetchall():
    rec = dict(zip(lcols, row))
    print({k: (str(v)[:60] if v is not None else None) for k, v in rec.items()})

print()
print("=== lite_embeddings: dimension distribution + kb breakdown ===")
print("by kb:")
for row in cur.execute(
        "select knowledge_base_id, knowledge_id, count(*), min(dimension), max(dimension) "
        "from lite_embeddings group by knowledge_base_id, knowledge_id order by knowledge_base_id").fetchall():
    print("  ", row)
print("total:", cur.execute("select count(*) from lite_embeddings").fetchone()[0])

print()
print("=== lite_embeddings_fts row count ===")
try:
    print(cur.execute("select count(*) from lite_embeddings_fts").fetchone()[0])
except sqlite3.Error as exc:
    print("err:", exc)

con.close()
