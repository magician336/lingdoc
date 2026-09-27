#!/usr/bin/env python3
"""Execute one LingDoc scenario specification against an isolated test provider.

A specification is either a step list document (F01's continuous trace in
workflow.json) or one scenario named out of scenarios.json; the runner drives
whichever it is handed. The runner uses OpenAPI operationIds and the
specification document as its only source of request shapes. It is
intentionally a smoke/integration runner, not a server implementation or a
replacement for the provider's semantic assertions.
"""
from __future__ import annotations

import argparse
from dataclasses import dataclass
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import sys
import tempfile
import time
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode, urljoin, urlsplit, urlunsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


ROOT = Path(__file__).resolve().parents[2]
OPENAPI_PATH = ROOT / "docs/08-本轮实施方案/contracts/openapi.json"
WORKFLOW_PATH = ROOT / "docs/08-本轮实施方案/contracts/workflow.json"
SCENARIOS_PATH = ROOT / "docs/08-本轮实施方案/contracts/scenarios.json"
VARIABLE = re.compile(r"\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}")
FAILURE_STATES = {"failed", "interrupted", "cancelled", "canceled", "error"}


class WorkflowError(RuntimeError):
    """An actionable error in the specification or provider response."""


@dataclass(frozen=True)
class ScenarioSpec:
    """One provider-driving specification: a step list plus how to read its result."""

    id: str
    steps: tuple[dict[str, Any], ...]
    download_sha256_variable: str | None = None


def non_empty_string(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise WorkflowError(f"{label} must be a non-empty string")
    return value


def read_json_object(path: Path, label: str) -> dict[str, Any]:
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise WorkflowError(f"cannot load {label} ({path}): {error}") from error
    if not isinstance(document, dict):
        raise WorkflowError(f"{label} must be a JSON object: {path}")
    return document


def _assertion_texts(step: dict[str, Any], where: str) -> list[str]:
    """Keep the intent text of a step, whichever of the two contract shapes it uses."""
    texts: list[str] = []
    single = step.get("assertion")
    if single is not None:
        texts.append(non_empty_string(single, f"{where} assertion"))
    listed = step.get("assertions", [])
    if not isinstance(listed, list):
        raise WorkflowError(f"{where} assertions must be a list")
    texts.extend(non_empty_string(text, f"{where} assertion") for text in listed)
    return texts


def _request_definition(step: dict[str, Any], where: str) -> dict[str, Any]:
    """Read the one thing that makes a step drivable, and say so plainly when it is absent."""
    request_spec = step.get("request")
    if not isinstance(request_spec, dict):
        raise WorkflowError(
            f"{where} has no request definition in the contract; only specifications that "
            "declare path, query, header and body inputs can be driven"
        )
    return request_spec


def _validated_steps(steps: Any, label: str, spec_id: str) -> tuple[dict[str, Any], ...]:
    """Validate the step shape that makes a specification drivable, without naming any scenario."""
    if not isinstance(steps, list) or not steps:
        raise WorkflowError(f"{label} must be a non-empty list of steps")
    validated: list[dict[str, Any]] = []
    for index, step in enumerate(steps, start=1):
        if not isinstance(step, dict):
            raise WorkflowError(f"{label} step {index} must be an object")
        declared = step.get("id")
        step_id = non_empty_string(declared or f"{spec_id}-{index:02d}", f"{label} step {index} id")
        # A report id may be synthesised, but an error has to point at an address the
        # contract file actually resolves: only a declared id does.
        where = step_id if declared else f"{label} step {index}"
        operation_id = non_empty_string(step.get("operation_id"), f"{where} operation_id")
        expected = step.get("expected_http")
        if not isinstance(expected, int) or isinstance(expected, bool):
            raise WorkflowError(f"{where} expected_http must be an HTTP status number")
        validated.append({**step, "id": step_id, "operation_id": operation_id,
                          "request": _request_definition(step, where),
                          "assertions": _assertion_texts(step, where)})
    return tuple(validated)


def _optional_string(value: Any, label: str) -> str | None:
    if value is None:
        return None
    return non_empty_string(value, label)


def spec_from_document(document: dict[str, Any], label: str, spec_id: str | None = None) -> ScenarioSpec:
    """Build the specification one document describes.

    `spec_id` overrides the document's own id when a caller asked for the scenario by
    name and the document it points at is the continuous specification file.
    """
    resolved = non_empty_string(spec_id or document.get("id"), f"{label} id")
    return ScenarioSpec(
        id=resolved,
        steps=_validated_steps(document.get("steps"), label, resolved),
        download_sha256_variable=_optional_string(document.get("download_sha256_variable"),
                                                   f"{resolved} download_sha256_variable"),
    )


def load_spec(spec_path: Path, scenario_id: str | None = None) -> ScenarioSpec:
    """Load the specification a run is driven by: a step list document, or one named scenario."""
    document = read_json_object(spec_path, "specification")
    if scenario_id is None:
        if isinstance(document.get("scenarios"), list):
            raise WorkflowError(f"{spec_path.name} holds scenarios; name one with --scenario")
        return spec_from_document(document, spec_path.name)
    entries = document.get("scenarios")
    if not isinstance(entries, list):
        raise WorkflowError(f"{spec_path.name} is not a scenarios document; --scenario needs one")
    entry = next((item for item in entries if isinstance(item, dict) and item.get("id") == scenario_id), None)
    if entry is None:
        raise WorkflowError(f"{spec_path.name} has no scenario {scenario_id}")
    if entry.get("steps") is not None:
        return spec_from_document(entry, f"scenario {scenario_id}", scenario_id)
    # A scenario without its own steps may point at the continuous specification file.
    pointer = entry.get("specification_file")
    if pointer is None:
        raise WorkflowError(f"{scenario_id} has no executable steps; its contract holds prose assertions only")
    referenced = read_json_object(spec_path.parent / non_empty_string(pointer, f"{scenario_id} specification_file"),
                                   f"{scenario_id} specification")
    return spec_from_document(referenced, f"{scenario_id} specification", scenario_id)


def header_value(headers: Any, name: str) -> str:
    """Read HTTP field names case-insensitively, including plain mappings."""
    for key, value in headers.items():
        if key.lower() == name.lower():
            return str(value)
    return ""


def write_report(path: Path, report: dict[str, Any]) -> None:
    """Create parents and atomically replace only a complete sanitized report."""
    temporary: str | None = None
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=path.parent,
                                         prefix=".f01-", suffix=".tmp", delete=False) as stream:
            temporary = stream.name
            stream.write(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        os.replace(temporary, path)
        temporary = None
    except OSError as error:
        raise WorkflowError(f"cannot write workflow report ({type(error).__name__})") from error
    finally:
        if temporary is not None:
            try:
                Path(temporary).unlink(missing_ok=True)
            except OSError:
                pass  # Do not mask the original write error.


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


class ProviderClient:
    """One provider's HTTP transport: where to send, what to send with, how much to read back.

    Both tools talk to a provider through this, so opener injection, the redirect
    credential rules, the bearer-token rule and the response ceiling stay in one place
    instead of being copied into a second client.
    """

    def __init__(self, base_url: str, token: str | None = None, timeout: float = 20, opener=None) -> None:
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.timeout = timeout
        self.opener = opener if opener is not None else build_opener(CredentialSafeRedirectHandler()).open
        parsed_base = urlsplit(self.base_url)
        if parsed_base.scheme not in {"http", "https"} or not parsed_base.netloc:
            raise WorkflowError("provider base URL must use http or https")
        if token and parsed_base.scheme != "https":
            hostname = (parsed_base.hostname or "").lower()
            try:
                is_loopback = ipaddress.ip_address(hostname).is_loopback
            except ValueError:
                is_loopback = hostname == "localhost"
            if not is_loopback:
                raise WorkflowError("refusing to send a bearer token over non-loopback HTTP")

    def request(self, method: str, url: str, headers: dict[str, str], body: Any) -> tuple[int, dict[str, str], Any]:
        """One provider exchange, saying plainly when the provider itself misbehaves."""
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
            content_type = header_value(response.headers, "Content-Type").lower()
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


class ScenarioRunner:
    def __init__(
        self,
        openapi: dict[str, Any],
        spec: ScenarioSpec,
        base_url: str | None = None,
        token: str | None = None,
        timeout: float = 20,
        poll_interval: float = 1,
        poll_timeout: float = 120,
        opener=None,
        sleep=time.sleep,
    ) -> None:
        server_url, self.operations = operation_index(openapi)
        # The transport is the client's business, not the runner's: the runner drives steps
        # through it, other tools send their own requests through it, and the base URL and
        # token rules are resolved once, inside it.
        self.client = ProviderClient(base_url or server_url, token=token, timeout=timeout, opener=opener)
        self.base_url = self.client.base_url
        self.spec = spec
        self.poll_interval = poll_interval
        self.poll_timeout = poll_timeout
        self.sleep = sleep
        self.variables: dict[str, Any] = {}
        self.step_results: list[dict[str, Any]] = []
        self.active_step: dict[str, str] | None = None

    @classmethod
    def from_files(cls, openapi_path: Path, spec_path: Path, scenario_id: str | None = None,
                   **kwargs: Any) -> "ScenarioRunner":
        spec = load_spec(spec_path, scenario_id)
        return cls(read_json_object(openapi_path, "OpenAPI document"), spec, **kwargs)

    def run(self) -> dict[str, Any]:
        self.variables = {}
        self.step_results = []
        self.active_step = None
        completed = 0
        step_results = self.step_results
        for step in self.spec.steps:
            operation_id = step.get("operation_id")
            self.active_step = {"id": step.get("id", ""), "operation_id": operation_id or ""}
            if operation_id not in self.operations:
                raise WorkflowError(f"{step.get('id')}: OpenAPI operation not found: {operation_id}")
            method, path_template = self.operations[operation_id]
            request_spec = _request_definition(step, step["id"])
            path_params = substitute(request_spec.get("path_params", {}), self.variables)
            query = substitute(request_spec.get("query", {}), self.variables)
            headers = substitute(request_spec.get("headers", {}), self.variables)
            body = substitute(request_spec.get("json"), self.variables)
            url = build_url(self.base_url, path_template, path_params, query)
            status, response_headers, payload = self.client.request(method, url, headers, body)
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
                "content_type": header_value(response_headers, "Content-Type"),
            })
            print(f"{step['id']} PASS {operation_id} HTTP {status}")

        manual_assertions = [
            (step["id"], assertion)
            for step in self.spec.steps
            for assertion in step.get("assertions", [])
        ]
        if manual_assertions:
            print(f"{self.spec.id} completed {completed} steps; {len(manual_assertions)} provider assertions remain evidence items.")
            for step_id, assertion in manual_assertions:
                print(f"MANUAL {step_id}: {assertion}")
        self.active_step = None
        return {**self.report("completed"), "variables": self.variables}

    def report(self, status: str) -> dict[str, Any]:
        # Never include captured IDs, bearer tokens, provider bodies, URLs, or
        # raw exceptions. Assertions are unexpanded text from the local contract.
        # The "workflow" key keeps its name so F01 results stay comparable across
        # the scenario-driven step; the matrix report renames it later.
        result = {
            "workflow": self.spec.id,
            "runner_status": status,
            "completed_steps": len(self.step_results),
            "total_steps": len(self.spec.steps),
            "steps": list(self.step_results),
            "verification_scope": "http_smoke_only",
            "provider_semantics_status": "not_verified",
            "manual_assertions": [
                {"step_id": step["id"], "assertion": assertion, "status": "not_run"}
                for step in self.spec.steps
                for assertion in step.get("assertions", [])
            ],
        }
        if status == "failed" and self.active_step is not None:
            result["failed_step"] = dict(self.active_step)
        return result

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
            next_status, _, next_payload = self.client.request(method, url, headers, None)
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
        actual_type = header_value(headers, "Content-Type").split(";", 1)[0].strip().lower()
        if expected_type and actual_type != expected_type.lower():
            raise WorkflowError(f"{step['id']}: Content-Type {actual_type or '<missing>'}; expected {expected_type}")
        variable_name = self.spec.download_sha256_variable
        if not variable_name:
            raise WorkflowError(f"{step['id']}: the specification must name the captured export digest "
                                "for download verification")
        expected = self.variables.get(variable_name)
        if not isinstance(expected, str) or not re.fullmatch(r"[0-9a-fA-F]{64}", expected):
            raise WorkflowError(f"{step['id']}: captured export SHA-256 is missing or invalid")
        actual = hashlib.sha256(payload).hexdigest()
        if actual.lower() != expected.lower():
            raise WorkflowError(f"{step['id']}: downloaded file SHA-256 does not match getExport")
        print(f"{step['id']} FILE_SHA256 {actual}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run one LingDoc scenario specification against an isolated test provider.")
    parser.add_argument("--base-url", help="OpenAPI server base URL; defaults to the first server in openapi.json")
    parser.add_argument("--token", default=os.environ.get("LINGDOC_TEST_TOKEN"), help="short-lived test bearer token (or LINGDOC_TEST_TOKEN)")
    parser.add_argument("--openapi", type=Path, default=OPENAPI_PATH)
    parser.add_argument("--spec", type=Path, help="specification document: a step list (default workflow.json) or a scenarios document together with --scenario")
    parser.add_argument("--scenario", help="scenario id to drive out of the scenarios document (default scenarios.json)")
    parser.add_argument("--request-timeout", type=float, default=20)
    parser.add_argument("--poll-interval", type=float, default=1)
    parser.add_argument("--poll-timeout", type=float, default=120)
    parser.add_argument("--report", type=Path, help="write a sanitized JSON record of completed steps")
    args = parser.parse_args(argv)
    spec_path = args.spec or (SCENARIOS_PATH if args.scenario else WORKFLOW_PATH)
    runner: ScenarioRunner | None = None
    report_started = False
    try:
        runner = ScenarioRunner.from_files(
            args.openapi,
            spec_path,
            scenario_id=args.scenario,
            base_url=args.base_url,
            token=args.token,
            timeout=args.request_timeout,
            poll_interval=args.poll_interval,
            poll_timeout=args.poll_timeout,
        )
        if args.report:
            # Verify the destination before any provider mutation and replace a
            # previous successful record with an explicit incomplete-run record.
            write_report(args.report, runner.report("not_started"))
            report_started = True
        result = runner.run()
        if args.report:
            write_report(args.report, runner.report("completed"))
            print(f"{runner.spec.id} report written to {args.report}")
    except (WorkflowError, OSError) as error:
        # Name the specification that failed; a load failure has no spec id yet.
        failed = runner.spec.id if runner is not None else args.scenario or spec_path.name
        print(f"{failed} FAILED: {error}", file=sys.stderr)
        if report_started and runner is not None:
            try:
                write_report(args.report, runner.report("failed"))
            except WorkflowError as report_error:
                print(f"{runner.spec.id} REPORT FAILED: {report_error}", file=sys.stderr)
        return 1
    print(json.dumps({key: value for key, value in result.items() if key != "variables"}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
