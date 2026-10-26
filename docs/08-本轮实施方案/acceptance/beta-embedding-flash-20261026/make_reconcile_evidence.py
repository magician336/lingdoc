"""Build the operator evidence file for the local reconciliation of the frozen
embedding window.

The evidence is an ATTESTATION about one exact ledger snapshot and one exact
failed request. It claims no provider usage: the provider's 403 carried no
usage, and the reconciliation charges the FULL reserved upper bound.
"""

import hashlib
import json
import pathlib

LEDGER = pathlib.Path(r"D:\wt\lingdoc-484-evidence\ledger\qwen-embedding-budget.json.calls.json")
RESPONSE = pathlib.Path(r"D:\wt\lingdoc-485-evidence\embedding-run\provider-403-response.txt")
OUT = pathlib.Path(r"D:\wt\lingdoc-485-evidence\embedding-run\flash-run\reconcile-evidence.json")

REASON = (
    "the provider answered HTTP 403 insufficient_quota on 2026-10-25 (free tier exhausted); "
    "the request produced no usage, so the window is settled at the full reserved upper bound "
    "and the authorized window is reopened for the Flash model"
)
OPERATOR = "lingdoc acceptance agent, acting under the repository owner's authorization of 2026-10-26"

raw = LEDGER.read_bytes()
ledger_sha = hashlib.sha256(raw).hexdigest()
doc = json.loads(raw.decode("utf-8"))

requests = doc.get("requests") or []
if len(requests) != 1:
    raise SystemExit("expected exactly one recorded request, found %d" % len(requests))
request = requests[0]
if request.get("status") != "http_error":
    raise SystemExit("the recorded request is not an http_error")
if not doc.get("blocked") or doc.get("blocked_reason") != "http_error":
    raise SystemExit("the ledger is not frozen with the http_error reason")

response_raw = RESPONSE.read_bytes()
response_sha = hashlib.sha256(response_raw).hexdigest()

evidence = {
    "ledger_sha256": ledger_sha,
    "request_number": request["number"],
    "original_status": request["status"],
    "original_blocked_reason": doc["blocked_reason"],
    "http_status": 403,
    "response_evidence_sha256": response_sha,
    "operator": OPERATOR,
    "reason": REASON,
}
OUT.write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print("ledger sha256      :", ledger_sha)
print("request number     :", request["number"], "status", request["status"])
print("response sha256    :", response_sha)
print("evidence written   :", OUT)
print("reason             :", REASON)
