"""Loopback-only, stateless HTTP examples from LingDoc's shared OpenAPI.

This is a development fixture server, not a provider, auth service or F01 runner.
Run from the repository root; install scripts/ci/requirements-lingdoc.txt first.
"""
from __future__ import annotations

import argparse
import copy
import json
import re
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, unquote, urlsplit

from jsonschema import Draft7Validator, FormatChecker, RefResolver

PREFIX = "/api/v1/lingdoc"
SPEC_PATH = Path(__file__).resolve().parents[2] / "docs/08-本轮实施方案/contracts/openapi.json"
MAX_BODY = 1 << 20
METHODS = {"get", "post", "put", "patch", "delete"}
CONTROL_HEADERS = {"x-lingdoc-mock-status", "x-lingdoc-mock-example"}
ALLOWED_HEADERS = CONTROL_HEADERS | {"content-type", "idempotency-key", "x-request-id"}


class MockError(Exception):
    def __init__(self, code: str, message: str, status: int = 400):
        super().__init__(message)
        self.code, self.status = code, status


def normalize(value):
    """Same OAS 3.0 nullable -> JSON Schema convention as validate_artifacts.py."""
    if isinstance(value, list):
        return [normalize(item) for item in value]
    if not isinstance(value, dict):
        return value
    if "$id" in value or "$schema" in value:
        raise ValueError("Mock schemas may not change resolver scope")
    if "$ref" in value and not value["$ref"].startswith("#/"):
        raise ValueError("Mock only supports local OpenAPI references")
    result = {key: normalize(item) for key, item in value.items() if key != "nullable"}
    if value.get("nullable"):
        if not isinstance(result.get("type"), str):
            raise ValueError("nullable requires an explicit schema type")
        result["type"] = [result["type"], "null"]
    return result


class ContractMock:
    def __init__(self, spec: dict):
        self.spec = copy.deepcopy(spec)
        self.normalized = normalize(spec)
        self.routes = []
        for path, item in self.spec["paths"].items():
            # Literal routes win over parameter routes of the same length.
            pieces = re.split(r"(\{[^}]+\})", path)
            pattern = "".join("([^/]+)" if p.startswith("{") else re.escape(p) for p in pieces)
            names = re.findall(r"\{([^}]+)\}", path)
            for method, operation in item.items():
                if method in METHODS:
                    params = item.get("parameters", []) + operation.get("parameters", [])
                    self.routes.append((path, re.compile("^" + pattern + "$"), names, method, operation, params))
        self.routes.sort(key=lambda route: (len(route[2]), -len(route[0])))
        # Do not start a server that can emit malformed tracked fixtures.
        self.checked_examples = self.check_examples()

    @classmethod
    def load(cls, path: Path):
        return cls(json.loads(path.read_text(encoding="utf-8")))

    def validate(self, schema, value, label, *, response=False):
        validator = Draft7Validator(normalize(schema), resolver=RefResolver.from_schema(self.normalized), format_checker=FormatChecker())
        error = next(validator.iter_errors(value), None)
        if error is not None:
            # Avoid echoing request/fixture content (which might contain local secrets).
            location = "/".join(str(part) for part in error.absolute_path) or "<root>"
            raise MockError("mock_contract_violation" if response else "invalid_request",
                            f"{label}: {error.validator} at {location}", 500 if response else 400)

    def check_examples(self):
        count = 0
        for _, _, _, _, op, _ in self.routes:
            for status, response in op["responses"].items():
                media = response.get("content", {}).get("application/json")
                if media is None:
                    continue  # Binary export is explicitly unsupported, never fabricated.
                for name, example in media.get("examples", {}).items():
                    if "value" not in example:
                        raise ValueError("Only inline JSON response examples are supported")
                    self.validate(media["schema"], example["value"],
                                  f'{op["operationId"]}/{status}/{name}', response=True)
                    count += 1
        return count

    def catalog(self):
        return [{"operation": op["operationId"], "method": method.upper(), "path": PREFIX + path,
                 "examples": {status: list(resp.get("content", {}).get("application/json", {}).get("examples", {}))
                              for status, resp in op["responses"].items()}}
                for path, _, _, method, op, _ in self.routes]

    def respond(self, method: str, target: str, headers: dict[str, str], body: bytes = b""):
        headers = {key.lower(): value for key, value in headers.items()}
        if any(key in headers for key in ("authorization", "cookie", "x-tenant-id")):
            raise MockError("mock_credentials_forbidden", "Do not send real credentials to the fixture server")
        url = urlsplit(target)
        if url.scheme or url.netloc or url.fragment or not url.path.startswith(PREFIX + "/"):
            raise MockError("mock_route_not_found", "Not a LingDoc mock route", 404)
        path = url.path[len(PREFIX):]
        for _, pattern, names, verb, op, params in self.routes:
            match = pattern.fullmatch(path)
            if match and method.lower() == verb:
                values = {name: unquote(value) for name, value in zip(names, match.groups())}
                if any("/" in value or value in {".", ".."} for value in values.values()):
                    raise MockError("invalid_request", "Invalid path segment")
                self._check_request(op, params, values, parse_qs(url.query, keep_blank_values=True), headers, body)
                return self._example(op, headers)
        raise MockError("mock_route_not_found", "No matching method/path in contract", 404)

    def _check_request(self, op, params, path, query, headers, body):
        query_names = {p["name"] for p in params if p["in"] == "query"}
        if set(query) - query_names:
            raise MockError("invalid_request", "Unknown query parameter")
        for param in params:
            where, name, schema = param["in"], param["name"], param["schema"]
            bucket, key = (headers, name.lower()) if where == "header" else (path, name) if where == "path" else (query, name)
            if key not in bucket:
                if param.get("required"):
                    raise MockError("invalid_request", f"Missing {where} parameter {name}")
                continue
            value = bucket[key]
            if where == "query":
                if len(value) != 1:
                    raise MockError("invalid_request", "Repeated query parameter is unsupported")
                value = value[0]
            if schema.get("type") == "integer":
                if not re.fullmatch(r"-?\d+", value):
                    raise MockError("invalid_request", f"Expected integer parameter {name}")
                value = int(value)
            elif schema.get("type") == "boolean":
                if value not in {"true", "false"}:
                    raise MockError("invalid_request", f"Expected boolean parameter {name}")
                value = value == "true"
            self.validate(schema, value, f'{op["operationId"]} {where}/{name}')
        request = op.get("requestBody")
        if not body:
            if request and request.get("required"):
                raise MockError("invalid_request", "Missing JSON request body")
            return
        if request is None:
            raise MockError("invalid_request", "This operation has no request body")
        if len(body) > MAX_BODY:
            raise MockError("invalid_request", "Request body too large", 413)
        if headers.get("content-type", "").split(";")[0].strip().lower() != "application/json":
            raise MockError("invalid_request", "Expected application/json", 415)
        media = request.get("content", {}).get("application/json")
        if media is None:
            raise MockError("mock_unsupported", "Only JSON requests are supported", 415)
        try:
            value = json.loads(body, parse_constant=lambda _: (_ for _ in ()).throw(ValueError("non-finite JSON")))
        except (ValueError, UnicodeError):
            raise MockError("invalid_request", "Malformed JSON request") from None
        self.validate(media["schema"], value, f'{op["operationId"]} request')

    def _example(self, op, headers):
        responses = op["responses"]
        successes = sorted(status for status in responses if re.fullmatch(r"2\d\d", status))
        status = headers.get("x-lingdoc-mock-status", successes[0] if successes else "")
        if status not in responses or not re.fullmatch(r"[1-5]\d\d", status):
            raise MockError("mock_example_not_found", "Unknown response status")
        media = responses[status].get("content", {}).get("application/json")
        if media is None:
            raise MockError("mock_unsupported", "No JSON example; binary downloads are not mocked", 501)
        examples = media.get("examples", {})
        name = headers.get("x-lingdoc-mock-example", "success" if "success" in examples else next(iter(examples), ""))
        if name not in examples:
            raise MockError("mock_example_not_found", "Unknown response example")
        value = copy.deepcopy(examples[name]["value"])
        self.validate(media["schema"], value, f'{op["operationId"]}/{status}/{name}', response=True)
        return int(status), value


def allowed_origin(value):
    url = urlsplit(value)
    return (url.scheme == "http" and url.hostname in {"127.0.0.1", "localhost"}
            and url.port is not None and not url.username and not url.password
            and not url.path and not url.query and not url.fragment)


def make_server(contract: ContractMock, port=4010, origins=()):
    if not all(allowed_origin(origin) for origin in origins):
        raise ValueError("Only explicit loopback HTTP origins with a port are allowed")

    class Handler(BaseHTTPRequestHandler):
        # Close every request; no ambiguous keep-alive body handling.
        protocol_version = "HTTP/1.0"

        def setup(self):
            super().setup()
            self.connection.settimeout(10)

        def log_message(self, *_):
            pass  # No user request headers, bodies or credentials in logs.

        def send_json(self, status, value, checked=False):
            payload = json.dumps(value, ensure_ascii=False, allow_nan=False).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.send_header("Cache-Control", "no-store")
            self.send_header("X-LingDoc-Mode", "mock")
            self.send_header("X-LingDoc-Contract-Checked", str(checked).lower())
            self.send_header("X-Content-Type-Options", "nosniff")
            origin = self.headers.get("Origin")
            if origin in origins:
                self.send_header("Access-Control-Allow-Origin", origin)
                self.send_header("Vary", "Origin")
                self.send_header("Access-Control-Expose-Headers", "X-LingDoc-Mode,X-LingDoc-Contract-Checked")
            self.end_headers()
            self.wfile.write(payload)

        def check_access(self):
            hosts = {f"127.0.0.1:{self.server.server_port}", f"localhost:{self.server.server_port}"}
            if self.headers.get("Host") not in hosts:
                raise MockError("mock_host_forbidden", "Host must be loopback", 403)
            if self.headers.get("Origin") is not None and self.headers["Origin"] not in origins:
                raise MockError("mock_origin_forbidden", "Origin not allowed", 403)

        def do_OPTIONS(self):
            try:
                self.check_access()
                requested = {part.strip().lower() for part in self.headers.get("Access-Control-Request-Headers", "").split(",") if part.strip()}
                if requested - ALLOWED_HEADERS:
                    raise MockError("mock_credentials_forbidden", "Requested headers not allowed", 403)
                self.send_response(204)
                self.send_header("Access-Control-Allow-Origin", self.headers.get("Origin", ""))
                self.send_header("Vary", "Origin")
                self.send_header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
                self.send_header("Access-Control-Allow-Headers", ",".join(sorted(ALLOWED_HEADERS)))
                self.send_header("Content-Length", "0")
                self.end_headers()
            except MockError as error:
                self.failure(error)

        def failure(self, error):
            self.send_json(error.status, {"error": {"code": error.code, "message": str(error), "retryable": False},
                                          "request_id": "t06-mock-error"})

        def handle_json(self):
            try:
                self.check_access()
                lengths = self.headers.get_all("Content-Length", [])
                if "Transfer-Encoding" in self.headers or len(lengths) > 1:
                    raise MockError("invalid_request", "Ambiguous request framing")
                try:
                    length = int(lengths[0]) if lengths else 0
                except ValueError:
                    raise MockError("invalid_request", "Invalid Content-Length") from None
                if not 0 <= length <= MAX_BODY:
                    raise MockError("invalid_request", "Request body too large", 413)
                body = self.rfile.read(length)
                if len(body) != length:
                    raise MockError("invalid_request", "Incomplete request body")
                status, value = contract.respond(self.command, self.path, dict(self.headers), body)
                self.send_json(status, value, checked=True)
            except MockError as error:
                self.failure(error)
            except (TimeoutError, ConnectionError):
                self.close_connection = True
            except Exception:
                self.failure(MockError("mock_internal_error", "Fixture server failed; check the contract locally", 500))

        do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = handle_json

    return ThreadingHTTPServer(("127.0.0.1", port), Handler)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--spec", type=Path, default=SPEC_PATH)
    parser.add_argument("--port", type=int, default=4010)
    parser.add_argument("--check", action="store_true", help="Validate every inline JSON response example; no server")
    parser.add_argument("--list", action="store_true", help="Print operations and selectable example names")
    parser.add_argument("--origin", action="append", default=[], help="Additional loopback Vite origin, e.g. http://127.0.0.1:5174")
    args = parser.parse_args()
    contract = ContractMock.load(args.spec)
    if args.check:
        print(json.dumps({"mode": "mock", "scope": "static_response_examples_only", "operations": len(contract.routes),
                          "examples_checked": contract.checked_examples, "result": "PASS"}))
        return
    if args.list:
        print(json.dumps(contract.catalog(), ensure_ascii=False, indent=2))
        return
    origins = {"http://127.0.0.1:5173", "http://localhost:5173", *args.origin}
    server = make_server(contract, args.port, origins)
    print(f"MOCK ONLY http://127.0.0.1:{server.server_port}{PREFIX} — fixed examples, no writes/auth/model", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
