"""The committed T15 evidence becomes one complete, deterministic 22-row matrix."""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from scripts.lingdoc_mock import build_t15_matrix as matrix


ROOT = Path(__file__).resolve().parents[2]


class T15MatrixTest(unittest.TestCase):
    def test_committed_evidence_yields_one_verdict_for_every_contract_scenario(self):
        report, evidence = matrix.build_documents(ROOT)

        rows = report["scenarios"]
        self.assertEqual(len(rows), 22)
        self.assertEqual([row["id"] for row in rows], [f"F{i:02d}" for i in range(1, 23)])
        self.assertEqual(len({row["id"] for row in rows}), 22)
        self.assertEqual(report["summary"], {"passed": 15, "failed": 2, "not_run": 5, "total": 22})

        by_id = {row["id"]: row for row in rows}
        self.assertEqual(by_id["F01"]["verdict"], "not_run")
        self.assertEqual(by_id["F07"]["verdict"], "failed")
        self.assertEqual(by_id["F15"]["verdict"], "failed")
        self.assertIn("has not been re-run after", by_id["F15"]["qualification"])
        self.assertEqual(by_id["F19"]["verdict"], "passed")
        self.assertEqual(by_id["F03"]["verdict"], "passed")
        self.assertIn("T15-10-R1", by_id["F03"]["red_item_ids"])
        self.assertEqual(by_id["F10"]["observed_steps"], 6)
        self.assertEqual(by_id["F16"]["observed_steps"], 11)

        self.assertTrue(report["red_items"])
        for item in report["red_items"]:
            self.assertTrue(item["owner_task"], item["id"])
            self.assertTrue(item["owner"], item["id"])
            self.assertEqual(item["owner_assignment_status"], "unassigned", item["id"])
        self.assertIn("no named assignees", report["ownership_note"])
        self.assertEqual(evidence["report_sha256"], hashlib.sha256(matrix.render_json(report)).hexdigest())
        self.assertTrue(evidence["source_artifacts"])

    def test_repeated_builds_are_byte_identical_and_timestamp_free(self):
        report_one, evidence_one = matrix.build_documents(ROOT)
        report_two, evidence_two = matrix.build_documents(ROOT)

        report_bytes_one = matrix.render_json(report_one)
        evidence_bytes_one = matrix.render_json(evidence_one)
        self.assertEqual(report_bytes_one, matrix.render_json(report_two))
        self.assertEqual(evidence_bytes_one, matrix.render_json(evidence_two))
        for payload in (report_bytes_one, evidence_bytes_one):
            text = payload.decode("utf-8")
            self.assertNotRegex(text, r"(?i)\b(?:timestamp|run_at|created_at|updated_at|duration|elapsed)\b")
            self.assertNotIn("2026-09-", text)

    def test_separate_processes_with_different_hash_seeds_emit_identical_files(self):
        script = ROOT / "scripts/lingdoc_mock/build_t15_matrix.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary_root = Path(temporary_directory)
            outputs = []
            for seed in ("1", "42"):
                report_path = temporary_root / f"report-{seed}.json"
                evidence_path = temporary_root / f"evidence-{seed}.json"
                environment = os.environ.copy()
                environment["PYTHONHASHSEED"] = seed
                subprocess.run(
                    [
                        sys.executable,
                        str(script),
                        "--repo-root",
                        str(ROOT),
                        "--report",
                        str(report_path),
                        "--evidence",
                        str(evidence_path),
                    ],
                    cwd=ROOT,
                    env=environment,
                    capture_output=True,
                    check=True,
                    text=True,
                )
                outputs.append((report_path.read_bytes(), evidence_path.read_bytes()))

        self.assertEqual(outputs[0][0], outputs[1][0])
        self.assertEqual(outputs[0][1], outputs[1][1])

    def test_missing_or_duplicate_scenario_evidence_is_rejected(self):
        document = {"scenarios": [{"id": "F01", "name": "one"}, {"id": "F02", "name": "two"}]}
        first = {"scenario": "F01", "verdict": "passed", "source_artifacts": ["run.json"]}

        with self.assertRaisesRegex(matrix.MatrixError, "missing scenario evidence.*F02"):
            matrix.assemble_matrix(document, [first])
        with self.assertRaisesRegex(matrix.MatrixError, "duplicate scenario evidence.*F01"):
            matrix.assemble_matrix(document, [first, first])


if __name__ == "__main__":
    unittest.main()
