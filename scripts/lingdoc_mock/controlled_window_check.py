"""Declare how a node may retrieve, and check the controlled window against it.

`retrieval` is a step-level field with three states:

  keyword_only    this node does not depend on vector-semantic recall, so it may
                  be judged in a controlled window that forbids embedding
  vector_required this node needs vector retrieval, so in a controlled window
                  it is NOT RUN with an unblock condition
  undeclared      the node declares nothing.  It is not a failure and it is
                  not a silent skip: it is named, so "nobody declared it"
                  never reads as "nobody owns it"

A declared mode nobody knows is refused by name: an unknown declaration is a
broken contract, not a mode that quietly becomes something else.  The runner
refuses an unknown check kind the same way.

The fact that backs the verdict is the acceptance budget ledger: it records
every reserved provider call of a controlled window, chat and embedding alike.
A window whose ledger holds an embedding row did use vector retrieval, and
that window fails as a whole -- it is not "the retrieval passed".  A window
whose ledgers cannot be read is NOT RUN, never PASS.

The window's fact is read from the two ledgers a controlled window writes -- the
reservation ledger and the shared ``.calls.json`` -- and the deployment's
model-call ledger is the cross-check on it: a model the deployment's own
record shows as an embedding call is an embedding model, whatever the model
table says.

``judge_window`` judges the fact of the ledgers it is given.  The release
chain (``scripts/acceptance/beta_evidence.py``) always hands it both ledgers of
the window, so a half-readable window can never be certified.  ``check`` is the
one-ledger form kept for the module's own guard test and for a quick look.
"""
from __future__ import annotations

import argparse
import json
import sqlite3
from pathlib import Path
from typing import Any, Iterable, Sequence

#: The retrieval modes a contract may declare for a step.
RETRIEVAL_MODES = ("keyword_only", "vector_required")

#: The third state: the step declares nothing.  It is named, not judged.
RETRIEVAL_UNDECLARED = "undeclared"

#: A declared mode nobody knows.  It is refused by name.
RETRIEVAL_UNKNOWN = "unknown"

# The unblock condition for a node that cannot run in a controlled window.
# The mode is still part of the closed vocabulary, so the condition has to
# stay true for any future node that declares it: embedding has to come into
# the controlled profile, and the chat/embedding quota has to be split -- the
# two share one allowance today (the profile's own call ceiling).
#
# The wording no longer names a fixed call ceiling: the profiles are
# versioned and their ceilings differ (the first Qwen profile allows 1 call,
# the second 4), so a sentence that pinned "MaxCalls=1" would go stale the
# moment another profile exists.
VECTOR_REQUIRED_UNBLOCK = (
    "把 embedding 纳入受控 profile 并拆分 chat/embedding 额度（今天两者共用同一个 "
    "allowance），届时该节点在受控窗口重新开放。"
)

PASS = "PASS"
FAIL = "FAIL"
NOT_RUN = "NOT_RUN"

#: Why the model types could not be read.  The model type is the authority for
#: what an embedding row is: without it nobody can tell an embedding row from a
#: chat row, so "I could not look" is not "there was none".
NO_MODEL_TYPES = (
    "没有给出模型表（--models-sqlite）：模型类型是判定 embedding 行的唯一依据，"
    "没有它就只能说「读不到」，不能说「没有」。"
)
NO_LEDGER = "没有给出受控窗口的验收预算账本（--ledger）。"


class UnknownRetrievalMode(ValueError):
    """A step declares a retrieval mode nobody knows.

    The refusal is loud on purpose: a misspelled declaration is a broken
    contract, and guessing what it meant is how a mode that forbids embedding
    turns into one that allows it.
    """

    def __init__(self, step_id: str, declared: str) -> None:
        super().__init__(f"{step_id} 声明了没人知道的检索方式「{declared}」")
        self.step_id = step_id
        self.declared = declared


def contract_steps(document: dict[str, Any]) -> list[dict[str, Any]]:
    """The steps of a contract document, in the document's own order."""
    steps = document.get("steps")
    if steps is None:
        steps = [step for scenario in document.get("scenarios", []) for step in scenario.get("steps", [])]
    return steps


def retrieval_vocabulary(document: dict[str, Any]) -> tuple[list[str], str]:
    """The retrieval modes this contract declares legal, and why there are none.

    A contract that states which modes are legal is the authority on what a
    step may write.  A contract that uses the ``retrieval`` field without
    stating the vocabulary has a broken declaration mechanism, and that is
    refused by name rather than guessed at.
    """
    modes = document.get("retrieval_modes")
    if modes is None:
        return list(RETRIEVAL_MODES), ""
    if not isinstance(modes, dict):
        return list(RETRIEVAL_MODES), "契约的 retrieval_modes 不是对象"
    values = modes.get("values")
    if not isinstance(values, list) or not values:
        return list(RETRIEVAL_MODES), "契约没有声明哪些检索方式是合法的（retrieval_modes.values 是空的）"
    return [str(value) for value in values], ""


def retrieval_declarations(contract_path: Path) -> tuple[list[dict[str, Any]], str]:
    """Every step of the contract, with the retrieval state it declares.

    The state is one of ``keyword_only``, ``vector_required``, ``undeclared``
    or ``unknown``.  ``unknown`` carries the value the contract wrote, and
    the caller refuses it by name instead of guessing what it meant.  The
    second value is why the vocabulary could not be read, if it could not.
    """
    document = json.loads(Path(contract_path).read_text(encoding="utf-8"))
    vocabulary, vocabulary_reason = retrieval_vocabulary(document)
    rows: list[dict[str, Any]] = []
    for step in contract_steps(document):
        mode = step.get("retrieval")
        if mode is None:
            state, declared = RETRIEVAL_UNDECLARED, ""
        elif str(mode) in vocabulary:
            state, declared = str(mode), str(mode)
        else:
            state, declared = RETRIEVAL_UNKNOWN, str(mode)
        rows.append({
            "step_id": step.get("id"),
            "operation_id": step.get("operation_id", ""),
            "retrieval": state,
            "declared": declared,
        })
    return rows, vocabulary_reason


def contract_declarations(contract_path: Path) -> list[dict[str, Any]]:
    """Every step that declares how it may retrieve."""
    rows, _ = retrieval_declarations(contract_path)
    return [{"step_id": row["step_id"], "operation_id": row["operation_id"], "retrieval": row["retrieval"]}
            for row in rows if row["retrieval"] in RETRIEVAL_MODES]


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
        document = json.loads(Path(ledger_path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise LookupError(f"读受控窗口账本失败：{error}") from error
    if not isinstance(document, dict) or not isinstance(document.get("requests"), list):
        raise LookupError("受控窗口账本不是预期形状（没有 requests 列表）")
    return document


def is_embedding(model: str, types: dict[str, str],
                 db_embedding_models: Iterable[str] = ()) -> bool:
    """Whether a model is an embedding model.

    The model table is the authority.  The deployment's own model-call ledger
    is the cross-check: a model the deployment recorded as an embedding call
    is an embedding model, whatever the model table says about it.
    """
    if model in set(db_embedding_models):
        return True
    return types.get(model) == "embedding"


def embedding_rows(ledger: dict[str, Any], types: dict[str, str],
                   db_embedding_models: Iterable[str] = ()) -> list[dict[str, Any]]:
    """The rows of the window that came from an embedding model."""
    found = []
    for request in ledger.get("requests", []):
        model = str(request.get("model") or "")
        if is_embedding(model, types, db_embedding_models):
            found.append({"number": request.get("number"), "model": model, "status": request.get("status")})
    return found


def unclassified_rows(ledger: dict[str, Any], types: dict[str, str],
                      db_embedding_models: Iterable[str] = ()) -> list[str]:
    """The models the window called that nobody can classify.

    A model the model table does not know could be an embedding model.  The
    window's "no embedding row" claim is not established while such a row
    exists, so it cannot be a pass.
    """
    known = set(db_embedding_models)
    found: list[str] = []
    for request in ledger.get("requests", []):
        model = str(request.get("model") or "")
        if not model or model in types or model in known or model in found:
            continue
        found.append(model)
    return found


def _node_verdicts(rows: list[dict[str, Any]], window_status: str, window_reason: str,
                    window_requests: int) -> list[dict[str, Any]]:
    """One verdict per declared step: what the window says about that step."""
    verdicts: list[dict[str, Any]] = []
    for row in rows:
        verdict = dict(row)
        mode = row["retrieval"]
        if mode == "keyword_only":
            if window_status == PASS:
                verdict.update(status=PASS,
                               reason=(f"受控窗口的 {window_requests} 条调用行里没有 embedding 行；"
                                       "该节点声明 keyword_only，可以在受控窗口里被判定。"))
            elif window_status == FAIL:
                verdict.update(status=FAIL,
                               reason=("受控窗口里出现了 embedding 调用行：窗口整体判失败，"
                                       "这个 keyword_only 节点也不能在此窗口被判定。"))
            else:
                verdict.update(status=NOT_RUN,
                               reason="受控窗口的账本读不到：这个节点在窗口里的判定没有发生。")
        elif mode == "vector_required":
            verdict.update(status=NOT_RUN,
                           reason=("该节点声明 vector_required：它确实需要向量检索，"
                                   "在受控窗口里它不能被判定，也不能被记为已判定。"),
                           unblock=VECTOR_REQUIRED_UNBLOCK)
        elif mode == RETRIEVAL_UNKNOWN:
            verdict.update(status=FAIL,
                           reason=(f"契约声明了没人知道的检索方式「{row['declared']}」："
                                   "未知就是拒绝，不能猜成别的意思。"),
                           unblock="把契约里的这个声明改成 keyword_only 或 vector_required 后重跑本脚本。")
        else:
            verdict.update(status=NOT_RUN,
                           reason=("契约没有为这个节点声明检索方式（undeclared）："
                                   "它不参与受控窗口的 embedding 判定，所以本轮没有判定它。"),
                           unblock=("在契约 docs/08-本轮实施方案/contracts/workflow.json 里为这个节点"
                                    "声明 retrieval（keyword_only 或 vector_required）后重跑本脚本。"))
        verdicts.append(verdict)
    return verdicts


def judge_window(contract_path: Path, ledger_paths: Sequence[Path | None], types: dict[str, str] | None,
                 types_reason: str = "", db_embedding_models: Iterable[str] = (),
                 db_reason: str = "") -> dict[str, Any]:
    """The controlled window's verdict, and the verdict for every node it declares.

    ``ledger_paths`` are the window's own ledgers (the reservation ledger and
    the shared ``.calls.json``); every one of them has to be readable, because
    the window's fact is what all of them say together.  ``types`` is the
    model type by model name: without it nobody can tell an embedding row from
    a chat row, and ``types_reason`` says why it is missing.

    ``db_embedding_models`` is the cross-check: the models the deployment's
    own model-call ledger shows as embedding calls.
    """
    db_embedding_models = sorted({str(model) for model in db_embedding_models})
    rows, vocabulary_reason = retrieval_declarations(Path(contract_path))
    result: dict[str, Any] = {
        "scope": "controlled_window_embedding_check",
        "contract": str(contract_path),
        "status": NOT_RUN,
        "reason": "",
        "embedding_rows": [],
        "window_requests": 0,
        "ledgers": [],
        "nodes": [],
        "declarations": [],
        "undeclared_steps": [],
        "vector_required_steps": [],
        "keyword_only_steps": [],
        "cross_check": {
            "available": not db_reason,
            "reason": db_reason or f"部署的模型调用账本里有 {len(db_embedding_models)} 个 embedding 模型的调用。",
            "embedding_models": db_embedding_models,
        },
    }
    result["declarations"] = [{"step_id": row["step_id"], "operation_id": row["operation_id"],
                                "retrieval": row["retrieval"]} for row in rows
                               if row["retrieval"] in RETRIEVAL_MODES]
    result["undeclared_steps"] = [row["step_id"] for row in rows if row["retrieval"] == RETRIEVAL_UNDECLARED]
    result["vector_required_steps"] = [{"step_id": row["step_id"], "unblock": VECTOR_REQUIRED_UNBLOCK}
                                       for row in rows if row["retrieval"] == "vector_required"]
    result["keyword_only_steps"] = [row["step_id"] for row in rows if row["retrieval"] == "keyword_only"]

    def stop(reason: str) -> dict[str, Any]:
        result["reason"] = reason
        result["nodes"] = _node_verdicts(rows, NOT_RUN, reason, result["window_requests"])
        return result

    unknown = [row for row in rows if row["retrieval"] == RETRIEVAL_UNKNOWN]
    if unknown:
        first = unknown[0]
        result["status"] = FAIL
        result["reason"] = (f"契约里 {first['step_id']} 声明了没人知道的检索方式「{first['declared']}」："
                            "未知就是拒绝，不得静默跳过。")
        result["nodes"] = _node_verdicts(rows, FAIL, result["reason"], 0)
        return result
    if vocabulary_reason:
        result["status"] = FAIL
        result["reason"] = vocabulary_reason + "：检索方式的声明机制坏了，不得静默跳过。"
        result["nodes"] = _node_verdicts(rows, FAIL, result["reason"], 0)
        return result

    if not ledger_paths:
        return stop(NO_LEDGER)

    per_ledger: list[list[dict[str, Any]]] = []
    for path in ledger_paths:
        if path is None:
            return stop(NO_LEDGER)
        try:
            document = ledger_rows(Path(path))
        except LookupError as error:
            return stop(str(error))
        result["ledgers"].append(str(path))
        per_ledger.append(document.get("requests", []))
    requests = [request for rows_of_one in per_ledger for request in rows_of_one]
    result["window_requests"] = len(requests)

    if types is None:
        return stop(types_reason or NO_MODEL_TYPES)

    found = embedding_rows({"requests": requests}, types, db_embedding_models)
    result["embedding_rows"] = found

    unknown_models = unclassified_rows({"requests": requests}, types, db_embedding_models)
    if unknown_models:
        return stop("受控窗口调用了模型表里没有的模型 " + "、".join(unknown_models)
                    + "：没有人能说它是不是 embedding 模型，所以窗口事实无法确定。")

    if found:
        result["status"] = FAIL
        result["reason"] = (f"受控窗口里出现了 {len(found)} 条 embedding 调用行：这个窗口整体判失败，"
                            "不得把其中任何一次检索记成「已通过检索」。")
        result["nodes"] = _node_verdicts(rows, result["status"], result["reason"], result["window_requests"])
        return result

    result["status"] = PASS
    result["reason"] = f"受控窗口的 {result['window_requests']} 条调用行里没有 embedding 行。"
    result["nodes"] = _node_verdicts(rows, result["status"], result["reason"], result["window_requests"])
    return result


def check(contract_path: Path, ledger_path: Path | None, sqlite_path: Path | None) -> dict[str, Any]:
    """The window verdict: PASS, FAIL or NOT_RUN -- never PASS for an unread window."""
    types: dict[str, str] | None = None
    reason = ""
    if sqlite_path is None:
        reason = NO_MODEL_TYPES
    else:
        try:
            types = model_types(sqlite_path)
        except LookupError as error:
            reason = str(error)
    verdict = judge_window(Path(contract_path), [ledger_path], types, reason)
    return {
        "scope": verdict["scope"],
        "contract": verdict["contract"],
        "declarations": verdict["declarations"],
        "status": verdict["status"],
        "reason": verdict["reason"],
        "embedding_rows": verdict["embedding_rows"],
        "window_requests": verdict["window_requests"],
        # The per-node verdicts belong to the window's verdict: the one-ledger
        # form is kept for the module's own guard test and for a quick look,
        # and a reader of this form has to see the node verdicts too (the
        # command line already reports them).
        "nodes": verdict["nodes"],
        "keyword_only_steps": verdict["keyword_only_steps"],
        "vector_required_steps": verdict["vector_required_steps"],
        "undeclared_steps": verdict["undeclared_steps"],
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--contract", type=Path, required=True,
                        help="声明了 retrieval 的契约文档（workflow.json 或 scenarios.json）")
    parser.add_argument("--ledger", type=Path, default=None,
                        help="受控窗口的验收预算账本（JSON）")
    parser.add_argument("--calls-ledger", type=Path, default=None,
                        help="受控窗口的共享调用账本（.calls.json）；窗口事实由两本账本一起构成")
    parser.add_argument("--models-sqlite", type=Path, default=None,
                        help="模型表所在的 SQLite 库；模型类型是 embedding 行的判定依据")
    arguments = parser.parse_args(argv)
    types: dict[str, str] | None = None
    reason = ""
    if arguments.models_sqlite is None:
        reason = NO_MODEL_TYPES
    else:
        try:
            types = model_types(arguments.models_sqlite)
        except LookupError as error:
            reason = str(error)
    ledgers: list[Path | None] = [arguments.ledger]
    if arguments.calls_ledger is not None:
        ledgers.append(arguments.calls_ledger)
    verdict = judge_window(arguments.contract, ledgers, types, reason)
    verdict["window_fact_complete"] = len(verdict["ledgers"]) >= 2
    if not verdict["window_fact_complete"]:
        verdict["note"] = ("只读到了一本账本：窗口事实不完整。"
                           "放行判定要预留账本与 .calls.json 两本账本都读得到。")
    print(json.dumps(verdict, ensure_ascii=False, indent=2))
    return {PASS: 0, FAIL: 1, NOT_RUN: 2}[verdict["status"]]


if __name__ == "__main__":
    raise SystemExit(main())
