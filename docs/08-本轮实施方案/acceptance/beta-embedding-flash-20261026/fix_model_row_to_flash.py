"""Minimal fix: point the isolated embedding model row at the AUTHORIZED model.

What changes and what does not
-------------------------------
The provider-facing model name is the model row's `name` column (it becomes
Config.ModelName -> the `model` field of the embeddings request body). The
authorized model for this round is `qwen3.7-text-embedding-flash`, so `name`
and `display_name` are changed to it.

NOTHING else is touched:
  * the row id stays, so the knowledge base's embedding_model_id reference and
    every record written in the previous round keep pointing at the same row;
  * `parameters` (the encrypted credential, the endpoint, the provider and the
    embedding parameters) stays byte-identical -- the credential is reused
    under its own ciphertext and never appears in plaintext;
  * the chat model row is not read for writing at all.

The script is idempotent and prints the before/after facts it verified.
"""

import hashlib
import json
import pathlib
import sqlite3
import sys

DB = pathlib.Path(r"D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db")
EMBED_ID = "lingdoc-embed-qwen3.7-text-embedding-iso"
CHAT_ID = "lingdoc-chat-qwen3.8-flash-iso"
OLD_NAME = "qwen3.7-text-embedding"
NEW_NAME = "qwen3.7-text-embedding-flash"
OUT = pathlib.Path(r"D:\wt\lingdoc-485-evidence\embedding-run\flash-run\model-row-fix.json")


def main() -> int:
    con = sqlite3.connect(str(DB))
    cur = con.cursor()

    row = cur.execute("select * from models where id = ?", (EMBED_ID,)).fetchone()
    if row is None:
        con.close()
        raise SystemExit("the embedding model row is not in this database")
    cols = [c[1] for c in cur.execute("pragma table_info(models)").fetchall()]
    before = dict(zip(cols, row))

    chat_row = cur.execute("select * from models where id = ?", (CHAT_ID,)).fetchone()
    chat = dict(zip(cols, chat_row)) if chat_row else None

    params_before = before["parameters"]
    params_sha_before = hashlib.sha256(
        params_before if isinstance(params_before, bytes) else params_before.encode()).hexdigest()

    if before["name"] == NEW_NAME:
        print("already the authorized model name; nothing to write")
        changed = False
    else:
        if before["name"] != OLD_NAME:
            con.close()
            raise SystemExit("the embedding row carries an unexpected model name: %r" % before["name"])
        description = ("track-D #546: the authorized Qwen embedding model "
                       "(%s), same credential and endpoint as the approved chat row" % NEW_NAME)
        cur.execute(
            "update models set name = ?, display_name = ?, description = ?, updated_at = ? where id = ?",
            (NEW_NAME, NEW_NAME, description, "2026-10-26 00:00:00+00:00", EMBED_ID))
        con.commit()
        changed = True
        print("updated the embedding model row:", OLD_NAME, "->", NEW_NAME)

    row = cur.execute("select * from models where id = ?", (EMBED_ID,)).fetchone()
    after = dict(zip(cols, row))
    chat_row2 = cur.execute("select * from models where id = ?", (CHAT_ID,)).fetchone()
    chat_after = dict(zip(cols, chat_row2)) if chat_row2 else None

    params_after = after["parameters"]
    params_sha_after = hashlib.sha256(
        params_after if isinstance(params_after, bytes) else params_after.encode()).hexdigest()

    raw = params_after
    if isinstance(raw, (bytes, bytearray)):
        raw = raw.decode("utf-8")
    parsed = json.loads(raw)

    facts = {
        "row_id_unchanged": after["id"] == EMBED_ID,
        "name_is_flash": after["name"] == NEW_NAME,
        "display_name_is_flash": after["display_name"] == NEW_NAME,
        "type_still_embedding": after["type"] == "Embedding",
        "source_still_remote": after["source"] == "remote",
        "status_still_active": after["status"] == "active",
        "tenant_unchanged": after["tenant_id"] == before["tenant_id"],
        "parameters_byte_identical": params_sha_before == params_sha_after,
        "api_key_still_ciphertext": str(parsed.get("api_key", "")).startswith("enc:v1:"),
        "base_url_unchanged": parsed.get("base_url") == json.loads(
            (before["parameters"].decode("utf-8") if isinstance(before["parameters"], bytes)
             else before["parameters"])).get("base_url"),
        "provider_unchanged": parsed.get("provider") == "aliyun",
        "embedding_parameters_unchanged": parsed.get("embedding_parameters") == json.loads(
            (before["parameters"].decode("utf-8") if isinstance(before["parameters"], bytes)
             else before["parameters"])).get("embedding_parameters"),
        "chat_row_untouched": chat_after == chat,
        "chat_row_still_qwen3_8_flash": (chat_after or {}).get("name") == "qwen3.8-flash",
        "chat_row_type_unchanged": (chat_after or {}).get("type") == (chat or {}).get("type"),
    }
    con.close()

    report = {
        "changed": changed,
        "before": {"name": before["name"], "display_name": before["display_name"],
                   "id": before["id"], "type": before["type"], "source": before["source"],
                   "status": before["status"], "tenant_id": before["tenant_id"]},
        "after": {"name": after["name"], "display_name": after["display_name"],
                  "id": after["id"], "type": after["type"], "source": after["source"],
                  "status": after["status"], "tenant_id": after["tenant_id"]},
        "provider": parsed.get("provider"),
        "base_url": parsed.get("base_url"),
        "embedding_parameters": parsed.get("embedding_parameters"),
        "api_key_ciphertext_prefix_ok": str(parsed.get("api_key", "")).startswith("enc:v1:"),
        "api_key_sha12": hashlib.sha256(str(parsed.get("api_key", "")).encode()).hexdigest()[:12],
        "facts": facts,
    }
    OUT.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    print("=== the embedding model row, after the fix ===")
    for key in ("id", "name", "display_name", "type", "source", "status", "tenant_id"):
        print("  %-12s %s" % (key, report["after"][key]))
    print("  provider     %s" % report["provider"])
    print("  base_url     %s" % report["base_url"])
    print("  embedding_parameters %s" % json.dumps(report["embedding_parameters"]))
    print("  api_key      enc:v1:*=%s sha12=%s (ciphertext only, never printed)"
          % (report["api_key_ciphertext_prefix_ok"], report["api_key_sha12"]))
    print("=== facts ===")
    failed = []
    for key, value in facts.items():
        print("  %-38s %s" % (key, "OK" if value else "FAIL"))
        if not value:
            failed.append(key)
    if failed:
        print("FAILED:", failed)
        return 1
    print("the embedding model row now names the authorized model")
    print("report written:", OUT)
    return 0


if __name__ == "__main__":
    sys.exit(main())
