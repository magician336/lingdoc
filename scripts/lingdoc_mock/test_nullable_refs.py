"""Nullable references go through the actual ContractMock validator, without HTTP."""
import copy
import json
import unittest

from scripts.lingdoc_mock.server import ContractMock, MockError, PREFIX
from scripts.lingdoc_mock.test_server import fixture


def nullable_ref_fixture():
    spec = fixture()
    spec["components"]["schemas"]["Version"] = {
        "type": "object", "properties": {"revision": {"type": "integer", "minimum": 1}},
        "required": ["revision"], "additionalProperties": False}
    ref = {"allOf": [{"$ref": "#/components/schemas/Version"}], "nullable": True}
    spec["components"]["schemas"]["Result"]["properties"]["data"]["properties"]["version"] = copy.deepcopy(ref)
    for operation in spec["paths"]["/items/{id}"].values():
        examples = operation["responses"]["200"]["content"]["application/json"]["examples"]
        examples["object"] = {"value": {"data": {"n": 1, "version": {"revision": 2}}}}
    spec["paths"]["/items/{id}"]["post"]["requestBody"]["content"]["application/json"]["schema"] = ref
    return spec


class NullableReferenceTests(unittest.TestCase):
    def test_nullable_ref_examples_preserve_both_null_and_valid_object(self):
        contract = ContractMock(nullable_ref_fixture())
        path = PREFIX + "/items/a"
        self.assertIsNone(contract.respond("GET", path, {})[1]["data"]["version"])
        status, body = contract.respond("GET", path, {"X-LingDoc-Mock-Example": "object"})
        self.assertEqual(status, 200)
        self.assertEqual(body["data"]["version"], {"revision": 2})

    def test_nullable_ref_request_keeps_target_required_type_and_unknown_field_checks(self):
        contract = ContractMock(nullable_ref_fixture())
        headers = {"Content-Type": "application/json", "Idempotency-Key": "nullable-ref-test"}
        path = PREFIX + "/items/a"
        for body in (b"null", b'{"revision":2}'):
            with self.subTest(valid=body):
                self.assertEqual(contract.respond("POST", path, headers, body)[0], 200)
        for value in ({}, {"revision": "wrong"}, {"revision": 0}, {"revision": 2, "extra": True}):
            with self.subTest(invalid=value), self.assertRaises(MockError) as caught:
                contract.respond("POST", path, headers, json.dumps(value).encode())
            self.assertEqual(caught.exception.status, 400)
            self.assertEqual(caught.exception.code, "invalid_request")

    def test_bad_nullable_ref_response_examples_are_rejected_at_startup(self):
        for value in ({}, {"revision": "wrong"}, {"revision": 2, "extra": True}):
            with self.subTest(value=value):
                spec = nullable_ref_fixture()
                spec["paths"]["/items/{id}"]["get"]["responses"]["200"]["content"]["application/json"]["examples"]["object"]["value"]["data"]["version"] = value
                with self.assertRaises(MockError) as caught:
                    ContractMock(spec)
                self.assertEqual(caught.exception.status, 500)
                self.assertEqual(caught.exception.code, "mock_contract_violation")

    def test_nullable_ref_response_is_revalidated_before_return(self):
        contract = ContractMock(nullable_ref_fixture())
        for route in contract.routes:
            if route[3] == "get":
                route[4]["responses"]["200"]["content"]["application/json"]["examples"]["object"]["value"]["data"]["version"] = {}
        with self.assertRaises(MockError) as caught:
            contract.respond("GET", PREFIX + "/items/a", {"X-LingDoc-Mock-Example": "object"})
        self.assertEqual(caught.exception.status, 500)

    def test_nullable_without_type_or_shaped_reference_is_rejected(self):
        for shape in ({"nullable": True}, {"nullable": True, "allOf": []},
                      {"nullable": True, "allOf": [{}]}, {"nullable": True, "oneOf": [{}]}):
            with self.subTest(shape=shape):
                spec = fixture()
                spec["components"]["schemas"]["Result"]["properties"]["data"]["properties"]["version"] = shape
                with self.assertRaises(ValueError):
                    ContractMock(spec)

    def test_nullable_ref_does_not_allow_external_refs_or_resolver_scope_changes(self):
        for branch in ({"$ref": "https://example.invalid/never-fetch"},
                       {"$ref": "#/components/schemas/Version", "$id": "https://example.invalid/scope"},
                       {"$ref": "#/components/schemas/Version", "$schema": "https://example.invalid/meta"}):
            with self.subTest(branch=branch):
                spec = nullable_ref_fixture()
                spec["components"]["schemas"]["Result"]["properties"]["data"]["properties"]["version"] = {
                    "allOf": [branch], "nullable": True}
                with self.assertRaises(ValueError):
                    ContractMock(spec)


if __name__ == "__main__":
    unittest.main()
