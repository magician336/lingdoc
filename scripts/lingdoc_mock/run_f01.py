#!/usr/bin/env python3
"""Execute the repository's F01 workflow against an isolated test provider.

The runner uses OpenAPI operationIds and workflow.json as its only source of
request shapes. It is intentionally a smoke/integration runner, not a server
implementation or a replacement for the provider's semantic assertions.
"""
from __future__ import annotations

import argparse
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import sys
import time
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode, urljoin, urlsplit, urlunsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


ROOT = Path(__file__).resolve().parents[2]
OPENAPI_PATH = ROOT / "docs/08-本轮实施方案/contracts/openapi.json"
WORKFLOW_PATH = ROOT / "docs/08-本轮实施方案/contracts/workflow.json"
VARIABLE = re.compile(r"\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}")
FAILURE_STATES = {"failed", "interrupted", "cancelled", "canceled", "error"}


class WorkflowError(RuntimeError):
    """An actionable error in the workflow definition or provider response."""


def json_pointer(value: Any, pointer: str) -> Any:
    if pointer == "":
        return value
    if not pointer.startswith("/"):
        raise WorkflowError(f"capture pointer must start with '/': {pointer}")
    current = value
    for raw_part in pointer[1:].split("/"):
        part = raw_part.replace("~1", "/").replace("~0", "~")
        try:
            if isinstance(current, list):
                current = current[int(part)]
            elif isinstance(current, dict):
                current = current[part]
            else:
                raise KeyError(part)
        except (KeyError, IndexError, ValueError) as error:
            raise WorkflowError(f"capture pointer not found: {pointer}") from error
    return current


def substitute(value: Any, variables: dict[str, Any]) -> Any:
    if isinstance(value, dict):
        return {key: substitute(child, variables) for key, child in value.items()}
    if isinstance(value, list):
        return [substitute(child, variables) for child in value]
    if not isinstance(value, str):
        return value
    exact = VARIABLE.fullmatch(value)
    if exact:
        name = exact.group(1)
        if name not in variables:
            raise WorkflowError(f"workflow variable is not captured yet: {name}")
        return variables[name]

    def replace(match: re.Match[str]) -> str:
        name = match.group(1)
        if name not in variables:
            raise WorkflowError(f"workflow variable is not captured yet: {name}")
        return str(variables[name])

    return VARIABLE.sub(replace, value)


def operation_index(openapi: dict[str, Any]) -> tuple[str, dict[str, tuple[str, str]]]:
    servers = openapi.get("servers") or []
    if not servers or not isinstance(servers[0].get("url"), str):
        raise WorkflowError("OpenAPI document has no server URL")
    index: dict[str, tuple[str, str]] = {}
    for path, methods in openapi.get("paths", {}).items():
        for method, operation in methods.items():
            operation_id = operation.get("operationId")
            if operation_id:
                if operation_id in index:
                    raise WorkflowError(f"duplicate OpenAPI operationId: {operation_id}")
                index[operation_id] = (method.upper(), path)
    return servers[0]["url"], index


def build_url(base_url: str, path_template: str, params: dict[str, Any], query: dict[str, Any]) -> str:
    def replace_path(match: re.Match[str]) -> str:
        name = match.group(1)
        if name not in params:
            raise WorkflowError(f"missing OpenAPI path parameter: {name}")
        return quote(str(params[name]), safe="")

    path = re.sub(r"\{([^{}]+)\}", replace_path, path_template)
    url = urljoin(base_url.rstrip("/") + "/", path.lstrip("/"))
    if query:
        split = urlsplit(url)
        encoded = urlencode(query, doseq=True)
        url = urlunsplit((split.scheme, split.netloc, split.path, "&".join(part for part in (split.query, encoded) if part), split.fragment))
    return url


def _origin(url: str) -> tuple[str, str, int | None]:
    parsed = urlsplit(url)
    port = parsed.port
    if port is None:
        port = {"http": 80, "https": 443}.get(parsed.scheme.lower())
    return parsed.scheme.lower(), (parsed.hostname or "").lower(), port


class CredentialSafeRedirectHandler(HTTPRedirectHandler):
    """Do not replay mutations or leak credentials across redirect origins."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        source = urlsplit(req.full_url)
        target = urlsplit(newurl)
        if target.scheme.lower() not in {"http", "https"}:
            raise WorkflowError("refusing redirect to a non-HTTP provider URL")
        if source.scheme.lower() == "https" and target.scheme.lower() != "https":
            raise WorkflowError("refusing HTTPS downgrade redirect")
        if req.get_method() not in {"GET", "HEAD"}:
            raise WorkflowError("refusing redirect for a mutating provider request")

        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
        if redirected is not None and _origin(req.full_url) != _origin(newurl):
            for name in ("Authorization", "Proxy-Authorization", "Cookie"):
                redirected.remove_header(name)
        return redirected


class F01Runner:
    def __init__(
        self,
        openapi: dict[str, Any],
        workflow: dict[str, Any],
        base_url: str | None = None,
        token: str | None = None,
        timeout: float = 20,
        poll_interval: float = 1,
        poll_timeout: float = 120,
        opener=None,
        sleep=time.sleep,
    ) -> None:
        server_url, self.operations = operation_index(openapi)
        self.base_url = (base_url or server_url).rstrip("/")
        self.workflow = workflow
        self.token = token
        self.timeout = timeout
        self.poll_interval = poll_interval
        self.poll_timeout = poll_timeout
        self.opener = opener if opener is not None else build_opener(CredentialSafeRedirectHandler()).open
        self.sleep = sleep
        self.variables: dict[str, Any] = {}
        parsed_base = urlsplit(self.base_url)
        if parsed_base.scheme not in {"http", "https"} or not parsed_base.netloc:
            raise WorkflowError("provider base URL must use http or https")
        if self.token and parsed_base.scheme != "https":
            hostname = (parsed_base.hostname or "").lower()
            try:
                is_loopback = ipaddress.ip_address(hostname).is_loopback
            except ValueError:
                is_loopback = hostname == "localhost"
            if not is_loopback:
                raise WorkflowError("refusing to send a bearer token over non-loopback HTTP")

    @classmethod
    def from_files(cls, openapi_path: Path, workflow_path: Path, **kwargs: Any) -> "F01Runner":
        try:
            openapi = json.loads(openapi_path.read_text(encoding="utf-8"))
            workflow = json.loads(workflow_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as error:
            raise WorkflowError(f"cannot load workflow contracts: {error}") from error
        if workflow.get("id") != "F01" or not isinstance(workflow.get("steps"), list):
            raise WorkflowError("workflow contract is not a valid F01 step list")
        return cls(openapi, workflow, **kwargs)

    def run(self) -> dict[str, Any]:
        completed = 0
        step_results: list[dict[str, Any]] = []
        for step in self.workflow["steps"]:
            operation_id = step.get("operation_id")
            if operation_id not in self.operations:
                raise WorkflowError(f"{step.get('id')}: OpenAPI operation not found: {operation_id}")
            method, path_template = self.operations[operation_id]
            request_spec = step.get("request", {})
            path_params = substitute(request_spec.get("path_params", {}), self.variables)
            query = substitute(request_spec.get("query", {}), self.variables)
            headers = substitute(request_spec.get("headers", {}), self.variables)
            body = substitute(request_spec.get("json"), self.variables)
            url = build_url(self.base_url, path_template, path_params, query)
            status, response_headers, payload = self._request(method, url, headers, body)
            expected_status = step.get("expected_http")
            if status != expected_status:
                raise WorkflowError(f"{step['id']} {operation_id}: HTTP {status}; expected {expected_status}")
            if operation_id in {"getGeneration", "getExport"}:
                payload = self._poll_terminal(step, method, url, headers, payload)
            self._capture(step, payload)
            if operation_id == "downloadExport":
                self._verify_download(step, payload, response_headers)
            completed += 1
            step_results.append({
                "id": step["id"],
                "operation_id": operation_id,
                "http_status": status,
                "content_type": response_headers.get("Content-Type", ""),
            })
            print(f"{step['id']} PASS {operation_id} HTTP {status}")

        manual_assertions = [
            (step["id"], assertion)
            for step in self.workflow["steps"]
            for assertion in step.get("assertions", [])
        ]
        if manual_assertions:
            print(f"F01 completed {completed} steps; {len(manual_assertions)} provider assertions remain evidence items.")
            for step_id, assertion in manual_assertions:
                print(f"MANUAL {step_id}: {assertion}")
        return {
            "workflow": self.workflow["id"],
            "completed_steps": completed,
            "steps": step_results,
            "variables": self.variables,
        }

    def _request(self, method: str, url: str, headers: dict[str, str], body: Any) -> tuple[int, dict[str, str], Any]:
        request_headers = {str(key): str(value) for key, value in headers.items()}
        request_headers.setdefault("Accept", "application/json, application/octet-stream")
        if self.token:
            request_headers["Authorization"] = "Bearer " + self.token
        data = None
        if body is not None:
            data = json.dumps(body, ensure_ascii=False).encode("utf-8")
            request_headers.setdefault("Content-Type", "application/json")
        request = Request(url, data=data, headers=request_headers, method=method)
        try:
            response = self.opener(request, timeout=self.timeout)
        except HTTPError as error:
            response = error
        except URLError as error:
            raise WorkflowError(f"request failed for {url}: {error.reason}") from error
        try:
            content = response.read(16 * 1024 * 1024 + 1)
            if len(content) > 16 * 1024 * 1024:
                raise WorkflowError("provider response exceeds the 16 MiB safety limit")
            response_headers = dict(response.headers.items())
            content_type = response.headers.get("Content-Type", "").lower()
            if "json" in content_type:
                try:
                    payload: Any = json.loads(content.decode("utf-8"))
                except (UnicodeDecodeError, json.JSONDecodeError) as error:
                    raise WorkflowError("provider returned invalid JSON") from error
            else:
                payload = content
            return response.status, response_headers, payload
        finally:
            response.close()

    def _poll_terminal(self, step: dict[str, Any], method: str, url: str, headers: dict[str, str], payload: Any) -> Any:
        reference = step.get("reference_response") or {}
        target = reference.get("data", {}).get("status")
        if not target:
            raise WorkflowError(f"{step['id']}: async poll step has no reference terminal status")
        deadline = time.monotonic() + self.poll_timeout
        while True:
            try:
                status = json_pointer(payload, "/data/status")
            except WorkflowError:
                raise WorkflowError(f"{step['id']}: async response is missing /data/status")
            if status in FAILURE_STATES:
                raise WorkflowError(f"{step['id']}: async operation ended in {status}")
            if status == target:
                return payload
            if time.monotonic() >= deadline:
                raise WorkflowError(f"{step['id']}: timed out waiting for terminal state {target}")
            self.sleep(self.poll_interval)
            next_status, _, next_payload = self._request(method, url, headers, None)
            if next_status != step.get("expected_http"):
                raise WorkflowError(f"{step['id']}: poll returned HTTP {next_status}")
            payload = next_payload

    def _capture(self, step: dict[str, Any], payload: Any) -> None:
        if not isinstance(payload, (dict, list)) and step.get("capture"):
            raise WorkflowError(f"{step['id']}: cannot capture values from a binary response")
        for name, pointer in step.get("capture", {}).items():
            self.variables[name] = json_pointer(payload, pointer)

    def _verify_download(self, step: dict[str, Any], payload: Any, headers: dict[str, str]) -> None:
        if not isinstance(payload, bytes):
            raise WorkflowError(f"{step['id']}: downloadExport must return file bytes, not JSON")
        expected_type = step.get("expected_binary", {}).get("content_type")
        actual_type = headers.get("Content-Type", "").split(";", 1)[0].strip().lower()
        if expected_type and actual_type != expected_type.lower():
            raise WorkflowError(f"{step['id']}: Content-Type {actual_type or '<missing>'}; expected {expected_type}")
        variable_name = self.workflow.get("download_sha256_variable")
        if not variable_name:
            raise WorkflowError("workflow must name the captured export digest for download verification")
        expected = self.variables.get(variable_name)
        if not isinstance(expected, str) or not re.fullmatch(r"[0-9a-fA-F]{64}", expected):
            raise WorkflowError(f"{step['id']}: captured export SHA-256 is missing or invalid")
        actual = hashlib.sha256(payload).hexdigest()
        if actual.lower() != expected.lower():
            raise WorkflowError(f"{step['id']}: downloaded file SHA-256 does not match getExport")
        print(f"{step['id']} FILE_SHA256 {actual}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run the F01 end-to-end workflow against an isolated LingDoc test provider.")
    parser.add_argument("--base-url", help="OpenAPI server base URL; defaults to the first server in openapi.json")
    parser.add_argument("--token", default=os.environ.get("LINGDOC_TEST_TOKEN"), help="short-lived test bearer token (or LINGDOC_TEST_TOKEN)")
    parser.add_argument("--openapi", type=Path, default=OPENAPI_PATH)
    parser.add_argument("--workflow", type=Path, default=WORKFLOW_PATH)
    parser.add_argument("--request-timeout", type=float, default=20)
    parser.add_argument("--poll-interval", type=float, default=1)
    parser.add_argument("--poll-timeout", type=float, default=120)
    parser.add_argument("--report", type=Path, help="write a sanitized JSON record of completed workflow steps")
    args = parser.parse_args(argv)
    try:
        runner = F01Runner.from_files(
            args.openapi,
            args.workflow,
            base_url=args.base_url,
            token=args.token,
            timeout=args.request_timeout,
            poll_interval=args.poll_interval,
            poll_timeout=args.poll_timeout,
        )
        result = runner.run()
        if args.report:
            report = {key: value for key, value in result.items() if key != "variables"}
            args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
            print(f"F01 report written to {args.report}")
    except WorkflowError as error:
        print(f"F01 FAILED: {error}", file=sys.stderr)
        return 1
    print(json.dumps({key: value for key, value in result.items() if key != "variables"}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

