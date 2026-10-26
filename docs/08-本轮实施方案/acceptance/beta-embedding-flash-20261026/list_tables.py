import sqlite3, pathlib
DB = pathlib.Path(r"D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db")
con = sqlite3.connect("file:%s?mode=ro" % DB.as_posix(), uri=True)
cur = con.cursor()
for r in cur.execute("select type, name from sqlite_master order by type, name").fetchall():
    print(r[0], r[1])
con.close()
