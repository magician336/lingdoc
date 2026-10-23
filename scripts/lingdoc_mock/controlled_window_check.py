"""Declare how a node may retrieve, and check the controlled window against it.

`retrieval` is a step-level field with two values:

  keyword_only    this node does not depend on vector-semantic recall, so it may
                  be judged in a controlled window that forbids embedding
  vector_required this node needs vector retrieval, so in a controlled window
                  it is NOT RUN with an unblock condition

The fact that backs the second value is the acceptance budget ledger: it is
where every reserved provider call of a controlled window is recorded, chat and
embedding alike.  A window whose ledger holds an embedding row did use vector
retrieval, and that window fails as a whole -- it is not "the retrieval passed".

This script reads that ledger, classifies its rows with the models table (the
model type is the authority, never a name convention), and reads the contract
for the declarations.  It never reports PASS for a window it could not read.
"""
from __future__ import annotations

import argparse
import json
import sqlite3
from pathlib import Path
from typing import Any

RETRIEVAL_MODES = ("keyword_only", "vector_required")

# The unblock condition for a node that cannot run in a controlled window.
# It is the condition the ruling fixed: embedding has to come into the
# controlled profile, and the chat/embedding quota has to be split -- the two
# share one MaxCalls=1 allowance today.
VECTOR_REQUIRED_UNBLOCK = (
    "把 embedding 纳入受控 profile 并拆分 chat/embedding 额度（两者今天共用同一个 "
    "MaxCalls=1），届时该节点在受控窗口重新开放。"
)

PASS = "PASS"
FAIL = "FAIL"
NOT_RUN = "NOT_RUN"


def contract_declarations(contract_path: Path) -> list[dict[str, Any]]:
    """Every step that declares how it may retrieve."""
    document = json.loads(contract_path.read_text(encoding="utf-8"))
    steps = document.get("steps")
    if steps is None:
        steps = [step for scenario in document.get("scenarios", []) for step in scenario.get("steps", [])]
    declarations = []
    for step in steps:
        mode = step.get("retrieval")
        if mode is None:
            continue
        declarations.append({
            "step_id": step.get("id"),
            "operation_id": step.get("operation_id"),
            "retrieval": mode,
        })
    return declarations


def model_types(sqlite_path: Path) -> dict[str, str]:
    """The model type of every model the deployment knows, by model name."""
    connection = sqlite3.connect(str(sqlite_path))
    try:
        connection.row_factory = sqlite3.Row
        try:
            rows = connection.execute("SELECT name, type FROM models").fetchall()
        except sqlite3.Error as error:
            raise LookupError(f"读模型表失败：{error}") from error
    finally:
        # The connection has to be closed: on Windows an open handle keeps the
        # file locked, and the reader's own cleanup would fail on it.
        connection.close()
    return {str(row["name"]): str(row["type"]) for row in rows}


def ledger_rows(ledger_path: Path) -> dict[str, Any]:
    """The controlled window's acceptance budget ledger."""
    try:
        document = json.loads(ledger_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise LookupError(f"读受控窗口账本失败：{error}") from error
    if not isinstance(document, dict) or not isinstance(document.get("requests"), list):
        raise LookupError("受控窗口账本不是预期形状（没有 requests 列表）")
    return document


def embedding_rows(ledger: dict[str, Any], types: dict[str, str]) -> list[dict[str, Any]]:
    """The rows of the window that came from an embedding model."""
    found = []
    for request in ledger.get("requests", []):
        model = str(request.get("model") or "")
        kind = types.get(model)
        if kind == "embedding":
            found.append({"number": request.get("number"), "model": model, "status": request.get("status")})
    return found


def check(contract_path: Path, ledger_path: Path | None, sqlite_path: Path | None) -> dict[str, Any]:
    """The window verdict: PASS, FAIL or NOT_RUN -- never PASS for an unread window."""
    declarations = contract_declarations(contract_path)
    result: dict[str, Any] = {
        "scope": "controlled_window_embedding_check",
        "contract": str(contract_path),
        "declarations": declarations,
    }
    if ledger_path is None:
        result.update({
            "status": NOT_RUN,
            "reason": "没有给出受控窗口的验收预算账本（--ledger）。",
            "embedding_rows": [],
        })
        return result
    if sqlite_path is None:
        result.update({
            "status": NOT_RUN,
            "reason": "没有给出模型表（--models-sqlite）：模型类型是判定 embedding 行的唯一依据，"
                      "没有它就只能说「读不到」，不能说「没有」。",
            "embedding_rows": [],
        })
        return result
    try:
        ledger = ledger_rows(ledger_path)
        types = model_types(sqlite_path)
    except LookupError as error:
        result.update({"status": NOT_RUN, "reason": str(error), "embedding_rows": []})
        return result
    found = embedding_rows(ledger, types)
    result["profile"] = ledger.get("profile")
    result["window_requests"] = len(ledger.get("requests", []))
    result["embedding_rows"] = found
    if found:
        # The window used vector retrieval: it fails as a whole, and the
        # retrieval it did is not reported as a pass.
        result.update({
            "status": FAIL,
            "reason": f"受控窗口里出现了 {len(found)} 条 embedding 调用行：这个窗口整体判失败，"
                      "不得把其中任何一次检索记成「已通过检索」。",
        })
        return result
    result.update({
        "status": PASS,
        "reason": f"受控窗口的 {result['window_requests']} 条调用行里没有 embedding 行。",
        "keyword_only_steps": [item["step_id"] for item in declarations
                               if item["retrieval"] == "keyword_only"],
        "vector_required_steps": [
            {"step_id": item["step_id"], "unblock": VECTOR_REQUIRED_UNBLOCK}
            for item in declarations if item["retrieval"] == "vector_required"
        ],
    })
    return result


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--contract", type=Path, required=True,
                        help="声明了 retrieval 的契约文档（workflow.json 或 scenarios.json）")
    parser.add_argument("--ledger", type=Path, default=None,
                        help="受控窗口的验收预算账本（JSON）")
    parser.add_argument("--models-sqlite", type=Path, default=None,
                        help="模型表所在的 SQLite 库（模型类型是 embedding 行的判定依据）")
    arguments = parser.parse_args(argv)
    result = check(arguments.contract, arguments.ledger, arguments.models_sqlite)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return {PASS: 0, FAIL: 1, NOT_RUN: 2}[result["status"]]


if __name__ == "__main__":
    raise SystemExit(main())
