import hashlib, json, pathlib, sqlite3

DB = pathlib.Path(r"D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db")
MAT = pathlib.Path(r"D:\wt\lingdoc-484-evidence\material\synth-chapter-c.md")

TARGET_KB = "7c104e1b-eb03-48ff-9d79-8a62b73cf082"
ISO_KB = "895c0a35-e7d5-4c28-b374-16f5d694ddea"

print("material exists:", MAT.exists())
if MAT.exists():
    raw = MAT.read_bytes()
    print("material sha256:", hashlib.sha256(raw).hexdigest())
    print("material bytes:", len(raw))

con = sqlite3.connect("file:%s?mode=ro" % DB.as_posix(), uri=True)
cur = con.cursor()

print()
print("=== knowledge_bases config comparison ===")
for kb_id, label in ((TARGET_KB, "TARGET"), (ISO_KB, "ISO-VECTOR")):
    row = cur.execute(
        "select id, name, tenant_id, chunking_config, embedding_model_id, vector_store_id, "
        "indexing_strategy, type from knowledge_bases where id = ?", (kb_id,)).fetchone()
    if not row:
        print(label, "MISSING")
        continue
    print("%-10s id=%s tenant=%s" % (label, row[0], row[2]))
    print("           chunking_config=%s" % (row[3],))
    print("           embedding_model_id=%r vector_store_id=%r" % (row[4], row[5]))
    print("           strategy=%s type=%s" % (row[6], row[7]))

print()
print("=== chunk content comparison (target 2 vs iso latest 2) ===")
def chunks_of(kb_id):
    rows = cur.execute(
        "select c.id, c.knowledge_id, c.chunk_index, c.content, c.start_at, c.end_at, c.index_status, c.created_at "
        "from chunks c where c.knowledge_base_id = ? order by c.created_at, c.chunk_index", (kb_id,)).fetchall()
    return rows

t = chunks_of(TARGET_KB)
i = chunks_of(ISO_KB)
for r in t:
    print("TARGET id=%s kn=%s idx=%s status=%s len=%s start=%s end=%s sha=%s" % (
        r[0], r[1], r[2], r[6], len(r[3] or ""), r[4], r[5],
        hashlib.sha256((r[3] or "").encode()).hexdigest()[:16]))
print()
for r in i:
    print("ISO    id=%s kn=%s idx=%s status=%s len=%s start=%s end=%s sha=%s" % (
        r[0], r[1], r[2], r[6], len(r[3] or ""), r[4], r[5],
        hashlib.sha256((r[3] or "").encode()).hexdigest()[:16]))

print()
print("=== byte-identical content? (target chunk i vs iso chunk i, latest knowledge) ===")
iso_by_kn = {}
for r in i:
    iso_by_kn.setdefault(r[1], []).append(r)
for kn, rows in iso_by_kn.items():
    print("iso knowledge %s has %d chunk rows" % (kn, len(rows)))

# compare target content sha list vs the newest iso knowledge's content sha list
tgt_shas = [hashlib.sha256((r[3] or "").encode()).hexdigest() for r in t]
if iso_by_kn:
    newest_kn = sorted(iso_by_kn.keys(), key=lambda k: iso_by_kn[k][0][7])[-1]
    iso_shas = [hashlib.sha256((r[3] or "").encode()).hexdigest() for r in iso_by_kn[newest_kn]]
    print("target content shas :", [s[:16] for s in tgt_shas])
    print("iso(%s) content shas:" % newest_kn[:8], [s[:16] for s in iso_shas])
    print("IDENTICAL CONTENT SET:", tgt_shas == iso_shas)

print()
print("=== vector_stores rows ===")
vs_cols = [c[1] for c in cur.execute("pragma table_info(vector_stores)").fetchall()]
print("columns:", vs_cols)
for row in cur.execute("select * from vector_stores").fetchall():
    rec = dict(zip(vs_cols, row))
    print({k: (str(v)[:80]) for k, v in rec.items()})

print()
print("=== lite_embeddings schema + rows ===")
le_cols = [c[1] for c in cur.execute("pragma table_info(lite_embeddings)").fetchall()]
print("columns:", le_cols)
print("row count:", cur.execute("select count(*) from lite_embeddings").fetchone()[0])
for row in cur.execute("select id, source_id, chunk_id, knowledge_id, knowledge_base_id, length(embedding) from lite_embeddings limit 10").fetchall():
    print(" ", row)

con.close()
