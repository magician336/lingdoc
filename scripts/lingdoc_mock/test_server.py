"""Transport/validator tests use a tiny synthetic contract, never a business DTO fork."""
import copy
import json
import threading
import unittest
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from scripts.lingdoc_mock.server import ContractMock, MockError, PREFIX, make_server


def fixture():
    error_schema = {"type": "object", "required": ["error"], "additionalProperties": False,
                    "properties": {"error": {"type": "string"}}}
    media = {"schema": {"$ref": "#/components/schemas/Result"},
             "examples": {"success": {"value": {"data": {"n": 1, "version": None}}}}}
    responses = {"200": {"content": {"application/json": media}},
                 "422": {"content": {"application/json": {"schema": error_schema,
                          "examples": {"invalid_state": {"value": {"error": "synthetic rejection"}}}}}},
                 "206": {"content": {"application/octet-stream": {"schema": {"type": "string", "format": "binary"}}}}}
    return {"openapi": "3.0.3", "paths": {"/items/{id}": {
        "get": {"operationId": "readItem", "parameters": [{"name": "id", "in": "path", "required": True,
                "schema": {"type": "string", "minLength": 1}}], "responses": responses},
        "post": {"operationId": "writeItem", "parameters": [{"name": "Idempotency-Key", "in": "header", "required": True,
                 "schema": {"type": "string", "minLength": 1}}],
                 "requestBody": {"required": True, "content": {"application/json": {"schema": {
                     "type": "object", "required": ["n"], "additionalProperties": False,
                     "properties": {"n": {"type": "integer"}}}}}}, "responses": copy.deepcopy(responses)}}},
        "components": {"schemas": {"Result": {"type": "object", "required": ["data"], "additionalProperties": False,
            "properties": {"data": {"type": "object", "required": ["n", "version"], "additionalProperties": False,
                "properties": {"n": {"type": "integer"}, "version": {"type": "string", "nullable": True}}}}}}}}


class ContractTests(unittest.TestCase):
    def setUp(self):
        self.contract = ContractMock(fixture())
        self.path = PREFIX + "/items/a"

    def test_success_and_error_selection(self):
        self.assertEqual(self.contract.respond("GET", self.path, {})[0], 200)
        status, body = self.contract.respond("GET", self.path,
                     {"X-LingDoc-Mock-Status": "422", "X-LingDoc-Mock-Example": "invalid_state"})
        self.assertEqual((status, body), (422, {"error": "synthetic rejection"}))

    def test_every_inline_example_validated_at_startup(self):
        spec = fixture()
        spec["paths"]["/items/{id}"]["get"]["responses"]["200"]["content"]["application/json"]["examples"]["success"]["value"]["data"]["n"] = "wrong"
        with self.assertRaises(MockError):
            ContractMock(spec)

    def test_response_revalidated_before_serving(self):
        for route in self.contract.routes:
            if route[3] == "get":
                route[4]["responses"]["200"]["content"]["application/json"]["examples"]["success"]["value"].pop("data")
        with self.assertRaises(MockError) as caught:
            self.contract.respond("GET", self.path, {})
        self.assertEqual(caught.exception.status, 500)
        self.assertEqual(caught.exception.code, "mock_contract_violation")

    def test_fixed_responses_are_copied_and_never_mutated_by_writes(self):
        _, first = self.contract.respond("GET", self.path, {})
        first["data"]["n"] = 999
        self.contract.respond("POST", self.path, {"Content-Type": "application/json", "Idempotency-Key": "test"}, b'{"n":9}')
        self.assertEqual(self.contract.respond("GET", self.path, {})[1]["data"]["n"], 1)

    def test_request_validation(self):
        valid_headers = {"Content-Type": "application/json", "Idempotency-Key": "test"}
        for headers, body in [({}, b'{"n":1}'), (valid_headers, b''), (valid_headers, b'{"n":"wrong"}'),
                              (valid_headers, b'{"n":1,"extra":true}'), (valid_headers, b'not json'),
                              (valid_headers, b'{"n":NaN}')]:
            with self.subTest(body=body, headers=headers), self.assertRaises(MockError):
                self.contract.respond("POST", self.path, headers, body)
        self.assertEqual(self.contract.respond("POST", self.path, valid_headers, b'{"n":1}')[0], 200)

    def test_unknown_path_query_selection_and_binary_fail_explicitly(self):
        cases = [("GET", PREFIX + "/unknown", {}), ("GET", self.path + "?typo=1", {}),
                 ("PATCH", self.path, {}), ("GET", self.path, {"X-LingDoc-Mock-Status": "999"}),
                 ("GET", self.path, {"X-LingDoc-Mock-Example": "typo"}),
                 ("GET", self.path, {"X-LingDoc-Mock-Status": "206"}),
                 ("GET", PREFIX + "/items/%2Fetc", {})]
        for method, path, headers in cases:
            with self.subTest(path=path, headers=headers), self.assertRaises(MockError):
                self.contract.respond(method, path, headers)

    def test_credentials_refused_and_external_refs_never_fetched(self):
        for name in ("Authorization", "Cookie", "X-Tenant-ID"):
            with self.subTest(name=name), self.assertRaises(MockError):
                self.contract.respond("GET", self.path, {name: "must-not-be-logged"})
        spec = fixture()
        spec["components"]["schemas"]["Result"] = {"$ref": "https://example.invalid/schema"}
        with self.assertRaises(ValueError):
            ContractMock(spec)


class HTTPTests(unittest.TestCase):
    def setUp(self):
        self.contract = ContractMock(fixture())
        self.server = make_server(self.contract, 0, {"http://127.0.0.1:5173"})
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f"http://127.0.0.1:{self.server.server_port}{PREFIX}/items/a"

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)

    def request(self, **kwargs):
        try:
            return urlopen(Request(self.url, **kwargs), timeout=2)
        except HTTPError as response:
            return response

    def test_real_http_success_error_and_cors(self):
        with self.request(headers={"Origin": "http://127.0.0.1:5173"}) as response:
            self.assertEqual(response.status, 200)
            self.assertEqual(response.headers["X-LingDoc-Mode"], "mock")
            self.assertEqual(response.headers["X-LingDoc-Contract-Checked"], "true")
            self.assertEqual(response.headers["Access-Control-Allow-Origin"], "http://127.0.0.1:5173")
            self.assertEqual(json.load(response)["data"]["n"], 1)
        with self.request(headers={"X-LingDoc-Mock-Status": "422"}) as response:
            self.assertEqual(response.status, 422)
            self.assertEqual(json.load(response)["error"], "synthetic rejection")

    def test_disallowed_origin_and_host(self):
        for headers in ({"Origin": "https://evil.invalid"}, {"Host": "evil.invalid"}):
            with self.request(headers=headers) as response:
                self.assertEqual(response.status, 403)
                self.assertIsNone(response.headers.get("Access-Control-Allow-Origin"))

    def test_preflight_rejects_credentials(self):
        with self.request(method="OPTIONS", headers={"Origin": "http://127.0.0.1:5173",
                          "Access-Control-Request-Headers": "authorization"}) as response:
            self.assertEqual(response.status, 403)
        with self.request(method="OPTIONS", headers={"Origin": "http://127.0.0.1:5173",
                          "Access-Control-Request-Headers": "content-type,x-lingdoc-mock-status"}) as response:
            self.assertEqual(response.status, 204)

    def test_corrupt_fixture_returns_500_not_a_success(self):
        for route in self.contract.routes:
            if route[3] == "get":
                route[4]["responses"]["200"]["content"]["application/json"]["examples"]["success"]["value"].pop("data")
        with self.request() as response:
            self.assertEqual(response.status, 500)
            self.assertEqual(json.load(response)["error"]["code"], "mock_contract_violation")
            self.assertEqual(response.headers["X-LingDoc-Contract-Checked"], "false")

    def test_invalid_http_request_is_not_reported_as_checked_fixture(self):
        with self.request(method="POST", data=b'{"n":1}', headers={"Content-Type": "application/json"}) as response:
            self.assertEqual(response.status, 400)
            self.assertEqual(response.headers["X-LingDoc-Contract-Checked"], "false")


if __name__ == "__main__":
    unittest.main()
