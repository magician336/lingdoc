"""Focused tests for the F01 black-box workflow runner."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path
import unittest

from scripts.lingdoc_mock.run_f01 import F01Runner, WorkflowError, substitute


ROOT = Path(__file__).resolve().parents[2]


class FakeResponse:
    def __init__(self, payload: bytes, status: int = 200, content_type: str = "application/json") -> None:
        self.payload = payload
        self.status = status
        self.headers = {"Content-Type": content_type}

    def read(self, limit: int = -1) -> bytes:
        return self.payload[:limit]

    def close(self) -> None:
        pass


class WorkflowRunnerTest(unittest.TestCase):
    def test_substitution_preserves_typed_capture(self) -> None:
        self.assertEqual(substitute({"n": "{{number}}", "text": "id={{number}}"}, {"number": 12}),
                         {"n": 12, "text": "id=12"})
        with self.assertRaisesRegex(WorkflowError, "not captured yet"):
            substitute("{{missing}}", {})

    def test_repository_f01_resolves_all_steps(self) -> None:
        runner = F01Runner.from_files(
            ROOT / "docs/08-本轮实施方案/contracts/openapi.json",
            ROOT / "docs/08-本轮实施方案/contracts/workflow.json",
        )
        self.assertEqual(runner.workflow["id"], "F01")
        self.assertEqual(len(runner.workflow["steps"]), 23)
        for step in runner.workflow["steps"]:
            self.assertIn(step["operation_id"], runner.operations, step["id"])

    def test_polls_without_reposting_and_verifies_download_bytes(self) -> None:
        file_bytes = b"test-docx-bytes"
        digest = hashlib.sha256(file_bytes).hexdigest()
        openapi = {
            "servers": [{"url": "http://provider.test/api/v1"}],
            "paths": {
                "/projects/{projectId}/exports": {"post": {"operationId": "startExport"}},
                "/projects/{projectId}/exports/{exportId}": {"get": {"operationId": "getExport"}},
                "/projects/{projectId}/exports/{exportId}/file": {"get": {"operationId": "downloadExport"}},
            },
        }
        workflow = {
            "id": "F01",
            "download_sha256_variable": "expected_sha256",
            "steps": [
                {
                    "id": "start", "operation_id": "startExport", "expected_http": 202,
                    "request": {"path_params": {"projectId": "p1"}, "headers": {"Idempotency-Key": "once"}, "json": {}},
                    "capture": {"export_id": "/data/id"},
                },
                {
                    "id": "poll", "operation_id": "getExport", "expected_http": 200,
                    "request": {"path_params": {"projectId": "p1", "exportId": "{{export_id}}"}, "headers": {}, "json": None},
                    "reference_response": {"data": {"status": "verified"}},
                    "capture": {"expected_sha256": "/data/file_sha256"},
                },
                {
                    "id": "download", "operation_id": "downloadExport", "expected_http": 200,
                    "request": {"path_params": {"projectId": "p1", "exportId": "{{export_id}}"}, "headers": {}, "json": None},
                    "capture": {},
                },
            ],
        }
        responses = [
            FakeResponse(b'{"data":{"id":"e1"}}', 202),
            FakeResponse(b'{"data":{"status":"queued"}}'),
            FakeResponse(json.dumps({"data": {"status": "verified", "file_sha256": digest}}).encode()),
            FakeResponse(file_bytes, 200, "application/octet-stream"),
        ]
        requests = []

        def opener(request, timeout):
            requests.append(request)
            return responses.pop(0)

        runner = F01Runner(openapi, workflow, token="test-token", opener=opener, sleep=lambda _: None, poll_timeout=0.1)
        result = runner.run()
        self.assertEqual(result["completed_steps"], 3)
        self.assertEqual([request.method for request in requests], ["POST", "GET", "GET", "GET"])
        self.assertIn("/exports/e1", requests[1].full_url)
        self.assertEqual(requests[0].get_header("Idempotency-key"), "once")
        self.assertEqual(requests[0].get_header("Authorization"), "Bearer test-token")
        self.assertFalse(responses)


if __name__ == "__main__":
    unittest.main()

