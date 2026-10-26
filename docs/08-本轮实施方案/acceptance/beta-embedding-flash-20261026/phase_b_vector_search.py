"""PHASE B: one query embedding plus one real vector retrieval.

The query is an ENGLISH paraphrase of the material's subject. Its tokens do
not occur anywhere in the indexed text, so the keyword channel has nothing to
match -- which is the point: whatever order comes back can only have come
from the vector channel, and the scores in the answer are the cosine
similarities the vector store computed.

The query embedding is the application's own (GetQueryEmbedding), so it goes
through the same acceptance budget as the indexing call. The ledger records
how many requests the round really made.
"""

import json
import pathlib
import sys
import time
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:18096/api/v1"
KB = "895c0a35-e7d5-4c28-b374-16f5d694ddea"
CREDS = pathlib.Path(r"D:\wt\lingdoc-484-evidence\owner-484-trackd5-token-credentials.txt")
LEDGER = pathlib.Path(r"D:\wt\lingdoc-484-evidence\ledger\qwen-embedding-budget.json.calls.json")
OUT = pathlib.Path(r"D:\wt\lingdoc-485-evidence\embedding-run\flash-run\phase-b-result.json")

QUERY = "mesoporous oxide thin film porosity compressive strength"


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


def ledger_state():
    doc = json.loads(LEDGER.read_text(encoding="utf-8"))
    return {
        "requests": len(doc.get("requests") or []),
        "blocked": doc.get("blocked"),
        "reserved_tokens": doc.get("reserved_tokens"),
        "actual_tokens": doc.get("actual_tokens"),
        "reserved_nano_cny": doc.get("reserved_nano_cny"),
        "actual_nano_cny": doc.get("actual_nano_cny"),
        "max_reserve_nano_cny": doc.get("max_reserve_nano_cny"),
        "cumulative_token_max": doc.get("cumulative_token_max"),
        "rows": doc.get("requests") or [],
    }


def main():
    result = {"phase": "B", "base": BASE, "kb": KB, "query": QUERY,
              "started_at": time.strftime("%Y-%m-%dT%H:%M:%S%z")}
    result["ledger_before"] = ledger_state()
    print("ledger before:", json.dumps(
        {k: v for k, v in result["ledger_before"].items() if k != "rows"}, ensure_ascii=False))

    email, password = credentials()
    status, login = call("POST", "/auth/login", {"email": email, "password": password})
    token = login.get("token") if isinstance(login, dict) else None
    result["login_status"] = status
    print("login:", status, "token acquired:", bool(token))
    if status != 200 or not token:
        result["failed_at"] = "login"
        OUT.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
        return 2

    status, searched = call("POST", "/knowledge-bases/%s/hybrid-search" % KB, token=token, payload={
        "query_text": QUERY,
        "match_count": 5,
        "vector_threshold": 0.0,
        "keyword_threshold": 0.0,
    })
    result["search_status"] = status
    result["search_response"] = searched
    print("hybrid-search:", status)

    data = searched.get("data") if isinstance(searched, dict) else None
    rows = []
    if isinstance(data, list):
        for index, row in enumerate(data):
            if not isinstance(row, dict):
                continue
            rows.append({
                "position": index + 1,
                "id": row.get("id"),
                "chunk_id": row.get("id") or row.get("chunk_id"),
                "knowledge_id": row.get("knowledge_id"),
                "knowledge_filename": row.get("knowledge_filename"),
                "start_at": row.get("start_at"),
                "end_at": row.get("end_at"),
                "score": row.get("score"),
                "match_type": row.get("match_type"),
                "content_head": (row.get("content") or "")[:120],
            })
    elif isinstance(data, dict):
        for group in (data.get("results") or data.get("items") or []):
            if not isinstance(group, dict):
                continue
            for row in (group.get("results") or []):
                if isinstance(row, dict):
                    rows.append({
                        "position": len(rows) + 1,
                        "retriever_type": group.get("retriever_type"),
                        "chunk_id": row.get("chunk_id") or row.get("id"),
                        "score": row.get("score"),
                        "match_type": row.get("match_type"),
                        "content_head": (row.get("content") or "")[:120],
                    })
    result["hits"] = rows
    print("hits:", len(rows))
    for row in rows:
        print("  #%s chunk=%s score=%s match_type=%s" % (
            row.get("position"), row.get("chunk_id"), row.get("score"), row.get("match_type")))
        print("       %s" % (row.get("content_head") or "").replace("\n", " / ")[:110])

    result["ledger_after"] = ledger_state()
    print("ledger after:", json.dumps(
        {k: v for k, v in result["ledger_after"].items() if k != "rows"}, ensure_ascii=False))
    for row in result["ledger_after"]["rows"]:
        print("  ledger:", row.get("number"), row.get("model"), row.get("status"),
              "bound=", row.get("input_upper_bound_tokens"), "in=", row.get("input_tokens"),
              "usage=", row.get("usage_status"),
              "reserved_cny=", row.get("reserved_nano_cny"), "actual_cny=", row.get("actual_nano_cny"))

    result["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%S%z")
    OUT.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print("written:", OUT)
    return 0


if __name__ == "__main__":
    sys.exit(main())
