"""HTTP-header and on-disk evidence regressions; no live provider is used."""
from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
from email.message import Message
import hashlib
import io
import json
from pathlib import Path
import re
import tempfile
import unittest
from unittest.mock import patch

from scripts.lingdoc_mock.run_f01 import F01Runner, WorkflowError, build_url, main, write_report


ROOT = Path(__file__).resolve().parents[2]
DOCX_TYPE = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"


class MessageResponse:
    def __init__(self, payload: bytes, content_type: str, header_name: str = "content-type", status: int = 200):
        self.payload, self.status = payload, status
        self.headers = Message()
        self.headers[header_name] = content_type
        self.closed = False

    def read(self, limit=-1):
        return self.payload[:limit]

    def close(self):
        self.closed = True


def small_runner(opener, token="synthetic-test-token"):
    api = {
        "servers": [{"url": "https://provider.test/api/v1/lingdoc"}],
        "paths": {
            "/projects": {"post": {"operationId": "createProject"}},
            "/projects/{projectId}": {"get": {"operationId": "getProject"}},
        },
    }
    workflow = {"id": "F01", "steps": [
        {"id": "create", "operation_id": "createProject", "expected_http": 201,
         "request": {"json": {"name": "synthetic"}}, "capture": {"project_id": "/data/id"},
         "assertions": ["Project creation still requires provider-side semantic verification."]},
        {"id": "read", "operation_id": "getProject", "expected_http": 200,
         "request": {"path_params": {"projectId": "{{project_id}}"}}, "capture": {}},
    ]}
    return F01Runner(api, workflow, token=token, opener=opener)


class WorkflowReportTest(unittest.TestCase):
    def test_message_header_casing_survives_request_and_download(self):
        for name in ("content-type", "CoNtEnT-TyPe", "Content-Type"):
            with self.subTest(header=name):
                json_response = MessageResponse(b'{"data":{"id":"synthetic-id"}}', "application/json", name)
                runner = small_runner(lambda request, timeout: json_response)
                _, _, payload = runner._request("GET", runner.base_url, {}, None)
                self.assertEqual(payload, {"data": {"id": "synthetic-id"}})
                self.assertTrue(json_response.closed)

                data = b"synthetic transport bytes, not proof of a valid DOCX"
                response = MessageResponse(data, DOCX_TYPE + "; charset=binary", name)
                runner.opener = lambda request, timeout: response
                runner.workflow["download_sha256_variable"] = "file_sha256"
                runner.variables["file_sha256"] = hashlib.sha256(data).hexdigest()
                _, headers, payload = runner._request("GET", runner.base_url, {}, None)
                with redirect_stdout(io.StringIO()):
                    runner._verify_download({"id": "download", "expected_binary": {"content_type": DOCX_TYPE}}, payload, headers)
                self.assertTrue(response.closed)

    def test_report_records_mixed_case_header_and_unverified_semantics(self):
        responses = [MessageResponse(b'{"data":{"id":"private-project-id"}}', "application/json", "cOnTeNt-TyPe", 201),
                     MessageResponse(b'{"data":{}}', "application/json")]
        runner = small_runner(lambda request, timeout: responses.pop(0))
        with redirect_stdout(io.StringIO()):
            result = runner.run()
        report = runner.report("completed")
        self.assertEqual(result["completed_steps"], 2)
        self.assertEqual([step["content_type"] for step in report["steps"]], ["application/json"] * 2)
        self.assertEqual(report["verification_scope"], "http_smoke_only")
        self.assertEqual(report["provider_semantics_status"], "not_verified")
        self.assertEqual(report["manual_assertions"][0]["status"], "not_run")
        encoded = json.dumps(report)
        self.assertNotIn("private-project-id", encoded)
        self.assertNotIn("synthetic-test-token", encoded)
        self.assertNotIn("variables", report)

    def test_main_creates_report_parent_before_provider_mutation(self):
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory) / "new" / "artifacts" / "run.json"
            calls = []

            def opener(request, timeout):
                initial = json.loads(destination.read_text(encoding="utf-8"))
                self.assertEqual(initial["runner_status"], "not_started")
                calls.append(request.method)
                return MessageResponse(b'{"data":{"id":"private-project-id"}}', "application/json", status=201 if len(calls) == 1 else 200)

            runner = small_runner(opener)
            with patch.object(F01Runner, "from_files", return_value=runner), redirect_stdout(io.StringIO()):
                status = main(["--report", str(destination)])
            self.assertEqual(status, 0)
            self.assertEqual(calls, ["POST", "GET"])
            report = json.loads(destination.read_text(encoding="utf-8"))
            self.assertEqual(report["runner_status"], "completed")
            self.assertEqual(report["completed_steps"], 2)
            self.assertEqual(report["provider_semantics_status"], "not_verified")
            self.assertFalse(list(destination.parent.glob(".f01-*.tmp")))

    def test_report_destination_error_prevents_all_provider_requests(self):
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory) / "not-a-directory"
            parent.write_text("occupied", encoding="utf-8")
            calls = []
            runner = small_runner(lambda request, timeout: calls.append(request))
            stderr = io.StringIO()
            with patch.object(F01Runner, "from_files", return_value=runner), redirect_stderr(stderr):
                status = main(["--report", str(parent / "run.json")])
            self.assertEqual(status, 1)
            self.assertEqual(calls, [])
            self.assertIn("cannot write workflow report", stderr.getvalue())
            self.assertNotIn("Traceback", stderr.getvalue())

    def test_failure_replaces_old_success_with_sanitized_partial_report(self):
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory) / "run.json"
            destination.write_text('{"runner_status":"completed","completed_steps":23}', encoding="utf-8")
            responses = [MessageResponse(b'{"data":{"id":"private-project-id"}}', "application/json", status=201),
                         MessageResponse(b'{"error":{"message":"private-provider-error"}}', "application/json", status=503)]
            runner = small_runner(lambda request, timeout: responses.pop(0))
            stderr = io.StringIO()
            with patch.object(F01Runner, "from_files", return_value=runner), redirect_stderr(stderr), redirect_stdout(io.StringIO()):
                status = main(["--report", str(destination)])
            self.assertEqual(status, 1)
            report_text = destination.read_text(encoding="utf-8")
            report = json.loads(report_text)
            self.assertEqual(report["runner_status"], "failed")
            self.assertEqual(report["completed_steps"], 1)
            self.assertEqual(report["failed_step"], {"id": "read", "operation_id": "getProject"})
            for private in ("private-project-id", "private-provider-error", "synthetic-test-token"):
                self.assertNotIn(private, report_text)
            self.assertNotIn("Traceback", stderr.getvalue())

    def test_atomic_report_write_failure_preserves_old_file_and_cleans_temp(self):
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory) / "run.json"
            destination.write_text("old complete file", encoding="utf-8")
            with patch("scripts.lingdoc_mock.run_f01.os.replace", side_effect=OSError("synthetic write fault")):
                with self.assertRaisesRegex(WorkflowError, "cannot write workflow report"):
                    write_report(destination, {"runner_status": "not_started"})
            self.assertEqual(destination.read_text(encoding="utf-8"), "old complete file")
            self.assertFalse(list(destination.parent.glob(".f01-*.tmp")))

    def test_readme_command_includes_lingdoc_prefix(self):
        readme = (ROOT / "docs/08-本轮实施方案/README.md").read_text(encoding="utf-8")
        match = re.search(r"--base-url\s+(\S+)", readme)
        self.assertIsNotNone(match)
        self.assertEqual(build_url(match.group(1), "/templates/{templateId}", {"templateId": "template-demo"}, {}),
                         "http://127.0.0.1:8080/api/v1/lingdoc/templates/template-demo")

    def test_second_run_does_not_reuse_captures_or_step_results(self):
        runner = small_runner(lambda request, timeout: MessageResponse(b'{"data":{"id":"p"}}', "application/json", status=201 if request.method == "POST" else 200))
        with redirect_stdout(io.StringIO()):
            runner.run()
            runner.variables["old_private_capture"] = "must disappear"
            result = runner.run()
        self.assertEqual(result["completed_steps"], 2)
        self.assertNotIn("old_private_capture", result["variables"])


if __name__ == "__main__":
    unittest.main()
