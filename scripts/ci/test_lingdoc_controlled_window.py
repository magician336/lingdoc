"""The controlled window's verdict: a window that used embedding fails as a whole.

The fact behind the verdict is the acceptance budget ledger: every provider
call of a controlled window is recorded there, chat and embedding alike.  The
model type (from the models table) says which is which -- never a name
convention -- and a window whose ledger cannot be read is NOT RUN, never PASS.
"""
from __future__ import annotations

import json
import sqlite3
import tempfile
import unittest
from pathlib import Path

from scripts.lingdoc_mock.controlled_window_check import (
    FAIL,
    NOT_RUN,
    PASS,
    check,
    main,
)

ROOT = Path(__file__).resolve().parents[2]
CONTRACT = ROOT / "docs/08-本轮实施方案/contracts/workflow.json"


def write_ledger(directory: str, models: list[str]) -> Path:
    path = Path(directory) / "budget.calls.json"
    document = {
        "schema_version": 1,
        "profile": "lingdoc-qwen-token-budget-20261023-v1",
        "max_calls": 1,
        "requests": [
            {"number": index + 1, "model": model, "status": "succeeded",
             "input_upper_bound_tokens": 10, "output_limit_tokens": 0}
            for index, model in enumerate(models)
        ],
    }
    path.write_text(json.dumps(document, ensure_ascii=False), encoding="utf-8")
    return path


def write_models(directory: str) -> Path:
    path = Path(directory) / "models.db"
    connection = sqlite3.connect(str(path))
    try:
        connection.execute("CREATE TABLE models (id TEXT PRIMARY KEY, name TEXT, type TEXT)")
        connection.execute("INSERT INTO models VALUES ('chat-1','qwen3.8-flash','chat')")
        connection.execute("INSERT INTO models VALUES ('embed-1','qwen3.7-text-embedding-flash','embedding')")
        connection.commit()
    finally:
        connection.close()
    return path


class ControlledWindowCheckTest(unittest.TestCase):
    def test_a_window_without_embedding_calls_passes(self):
        with tempfile.TemporaryDirectory() as directory:
            ledger = write_ledger(directory, ["qwen3.8-flash"])
            models = write_models(directory)
            result = check(CONTRACT, ledger, models)
        self.assertEqual(result["status"], PASS)
        self.assertEqual(result["embedding_rows"], [])
        self.assertEqual(result["window_requests"], 1)

    def test_a_window_with_an_embedding_call_fails_as_a_whole(self):
        # The negative control: the window used vector retrieval, so it is not
        # "the retrieval passed" -- the window fails.
        with tempfile.TemporaryDirectory() as directory:
            ledger = write_ledger(directory, ["qwen3.8-flash", "qwen3.7-text-embedding-flash"])
            models = write_models(directory)
            result = check(CONTRACT, ledger, models)
        self.assertEqual(result["status"], FAIL)
        self.assertEqual([row["model"] for row in result["embedding_rows"]],
                         ["qwen3.7-text-embedding-flash"])

    def test_a_window_that_cannot_be_read_is_not_run_and_never_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            models = write_models(directory)
            result = check(CONTRACT, None, models)
        self.assertEqual(result["status"], NOT_RUN)
        self.assertNotIn("embedding_rows", result["embedding_rows"])
        self.assertIn("没有给出受控窗口的验收预算账本", result["reason"])

    def test_a_window_without_the_model_table_is_not_run(self):
        # Without the model type there is no way to tell an embedding row from
        # a chat row, and "I could not look" is not "there was none".
        with tempfile.TemporaryDirectory() as directory:
            ledger = write_ledger(directory, ["qwen3.7-text-embedding-flash"])
            result = check(CONTRACT, ledger, None)
        self.assertEqual(result["status"], NOT_RUN)
        self.assertIn("模型表", result["reason"])

    def test_the_contract_declares_how_the_f01_generation_may_retrieve(self):
        with tempfile.TemporaryDirectory() as directory:
            ledger = write_ledger(directory, ["qwen3.8-flash"])
            models = write_models(directory)
            result = check(CONTRACT, ledger, models)
        declarations = {item["step_id"]: item["retrieval"] for item in result["declarations"]}
        self.assertEqual(declarations.get("F01-07"), "vector_required")
        self.assertEqual([item["step_id"] for item in result["vector_required_steps"]], ["F01-07"])
        self.assertIn("拆分 chat/embedding 额度", result["vector_required_steps"][0]["unblock"])

    def test_the_command_line_reports_the_window_verdict_as_the_exit_code(self):
        with tempfile.TemporaryDirectory() as directory:
            ledger = write_ledger(directory, ["qwen3.7-text-embedding-flash"])
            models = write_models(directory)
            self.assertEqual(main(["--contract", str(CONTRACT), "--ledger", str(ledger),
                                   "--models-sqlite", str(models)]), 1)
            self.assertEqual(main(["--contract", str(CONTRACT)]), 2)


if __name__ == "__main__":
    unittest.main()
