"""List every artifact this round produced, with its sha256, so the report can
point at files a reader can verify."""
import hashlib
import pathlib
import json

DIRS = [
    pathlib.Path(r"D:\wt\lingdoc-485-evidence\embedding-run\flash-run"),
]
EXTRA = [
    pathlib.Path(r"D:\wt\lingdoc-484-evidence\ledger\qwen-embedding-budget.json.calls.json"),
    pathlib.Path(r"D:\wt\lingdoc-484-evidence\ledger\qwen-beta-sqlite.json"),
    pathlib.Path(r"D:\wt\lingdoc-484-evidence\ledger\qwen-beta-sqlite.json.calls.json"),
    pathlib.Path(r"D:\wt\lingdoc-485-evidence\embedding-run\provider-403-response.txt"),
]

rows = []
for directory in DIRS:
    for path in sorted(directory.rglob("*")):
        if path.is_file():
            rows.append(path)
for path in EXTRA:
    if path.is_file():
        rows.append(path)

out = []
for path in rows:
    if path.name == "artifact-manifest.json":
        # the manifest cannot record its own digest
        continue
    raw = path.read_bytes()
    out.append({
        "path": str(path),
        "bytes": len(raw),
        "sha256": hashlib.sha256(raw).hexdigest(),
    })

target = pathlib.Path(r"D:\wt\lingdoc-485-evidence\embedding-run\flash-run\artifact-manifest.json")
target.write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
for row in out:
    print("%10d  %s  %s" % (row["bytes"], row["sha256"][:16], row["path"]))
print("written:", target)
