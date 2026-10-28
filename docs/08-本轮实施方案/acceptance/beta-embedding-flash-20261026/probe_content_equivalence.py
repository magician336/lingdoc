import hashlib, json, pathlib, sqlite3

DB = pathlib.Path(r"D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db")
TARGET_KB = "7c104e1b-eb03-48ff-9d79-8a62b73cf082"
ISO_KB = "895c0a35-e7d5-4c28-b374-16f5d694ddea"

con = sqlite3.connect("file:%s?mode=ro" % DB.as_posix(), uri=True)
cur = con.cursor()

for label, kb in (("TARGET", TARGET_KB), ("ISO", ISO_KB)):
    print("===", label, kb)
    rows = cur.execute(
        "select id, chunk_index, index_status, start_at, end_at, length(content), content, "
        "context_header, created_at, deleted_at from chunks where knowledge_base_id = ? "
        "order by chunk_index, created_at", (kb,)).fetchall()
    for r in rows:
        content = r[6] or ""
        print("  id=%s idx=%r status=%s start=%s end=%s len=%s sha=%s ctx=%r created=%s deleted=%s" % (
            r[0], r[1], r[2], r[3], r[4], r[5],
            hashlib.sha256(content.encode()).hexdigest()[:20], r[7], r[8], r[9]))
        print("     head=%r" % content[:70])
        print("     tail=%r" % content[-70:])
con.close()
