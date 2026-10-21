"""Mutation tests consume actual registrars, consumers and the published contract."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import unittest
import warnings

with warnings.catch_warnings():
    warnings.simplefilter("ignore", DeprecationWarning)
    from jsonschema import Draft7Validator, RefResolver

MODULE = Path(__file__).with_name("check_lingdoc_routes.py")
loader = importlib.util.spec_from_file_location("lingdoc_route_parity", MODULE)
parity = importlib.util.module_from_spec(loader)
loader.loader.exec_module(parity)


class LingDocRouteParity(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.root = Path(os.environ.get("LINGDOC_CONTRACT_SOURCE_ROOT", Path(__file__).resolve().parents[2]))
        contract = Path(os.environ.get("LINGDOC_CONTRACT_FILE", cls.root / "docs/08-本轮实施方案/contracts/openapi.json"))
        cls.document = json.loads(contract.read_text(encoding="utf-8-sig"))
        cls.routes, cls.clients, cls.types = parity.load_inputs(cls.root)
        cls.path = "/projects/{projectId}/research-tasks"

    def check(self, document=None, routes=None, clients=None):
        return parity.check_document(self.document if document is None else document,
                                     self.routes if routes is None else routes,
                                     self.clients if clients is None else clients, self.types)

    def test_real_registered_operations_and_api_consumers_match(self):
        report = self.check()
        self.assertEqual(report["result"], "PASS")
        self.assertGreater(report["registered_operations"], 0)
        self.assertGreater(report["frontend_calls_to_scoped_registrars"], 0)

    def test_main_refactored_impact_registrar_stays_in_the_inventory(self):
        self.assertIn("internal/lingdoc/workspace/impact_routes.go", self.routes)
        self.assertNotIn("internal/lingdoc/workspace/g3_g6_full_http.go", self.routes)
        report = self.check()
        reads = [route for route in report["routes"]
                 if route["signature"] == "GET /projects/{}/check-receipts/{}"]
        self.assertEqual(len(reads), 1)
        self.assertEqual(reads[0]["fixed_success"], "200")

    def test_refactored_impact_write_cannot_lose_its_idempotency_header(self):
        bad = copy.deepcopy(self.document)
        operation = bad["paths"]["/projects/{projectId}/change-sets/drafts"]["post"]
        operation["parameters"] = [p for p in operation["parameters"]
                                   if p.get("in") != "header"]
        with self.assertRaisesRegex(parity.ContractParityError, "missing required Idempotency-Key"):
            self.check(document=bad)

    def test_refactored_impact_read_cannot_be_relabelled_as_created(self):
        bad = copy.deepcopy(self.document)
        responses = bad["paths"]["/projects/{projectId}/check-receipts/{receiptId}"]["get"]["responses"]
        responses["201"] = responses.pop("200")
        with self.assertRaisesRegex(parity.ContractParityError, "fixed handler success status"):
            self.check(document=bad)

    def test_deleted_published_operation_is_rejected(self):
        bad = copy.deepcopy(self.document)
        del bad["paths"][self.path]["get"]
        with self.assertRaisesRegex(parity.ContractParityError, "registered operation absent"):
            self.check(document=bad)

    def test_published_method_change_is_rejected(self):
        bad = copy.deepcopy(self.document)
        bad["paths"][self.path]["delete"] = bad["paths"][self.path].pop("get")
        with self.assertRaises(parity.ContractParityError):
            self.check(document=bad)

    def test_generic_request_object_is_not_a_contract(self):
        bad = copy.deepcopy(self.document)
        bad["paths"][self.path]["post"]["requestBody"]["content"]["application/json"]["schema"] = {
            "type": "object", "additionalProperties": True}
        with self.assertRaisesRegex(parity.ContractParityError, "arbitrary/empty object"):
            self.check(document=bad)

    def test_request_decoder_cannot_be_documented_as_optional(self):
        bad = copy.deepcopy(self.document)
        bad["paths"][self.path]["post"]["requestBody"]["required"] = False
        with self.assertRaisesRegex(parity.ContractParityError, "missing required request body"):
            self.check(document=bad)

    def test_real_request_schema_rejects_missing_required_but_allows_omitted_optional_field(self):
        media = self.document["paths"][self.path]["post"]["requestBody"]["content"]["application/json"]
        body = parity.resolve(self.document, media["schema"])
        self.assertIn("expected_context_revision", body["required"])
        self.assertNotIn("task_id", body.get("required", []), "Go task_id is omitempty; creation must allow absence")
        self.assertIn("task_id", body["properties"])
        example = copy.deepcopy(media.get("example") or next(iter(media["examples"].values()))["value"])
        example.pop("task_id", None)
        validator = Draft7Validator(body, resolver=RefResolver.from_schema(self.document))
        self.assertFalse(list(validator.iter_errors(example)))
        del example["expected_context_revision"]
        self.assertTrue(list(validator.iter_errors(example)))

    def test_bodyless_reassessment_cannot_acquire_a_json_body(self):
        bad = copy.deepcopy(self.document)
        operation = bad["paths"]["/projects/{projectId}/change-sets/{changeSetId}/reassess"]["post"]
        self.assertNotIn("requestBody", operation)
        operation["requestBody"] = {"required": False, "content": {"application/json": {"schema": {"type": "object"}}}}
        with self.assertRaisesRegex(parity.ContractParityError, "JSON/bodyless mode"):
            self.check(document=bad)

    def test_empty_success_data_schema_is_rejected(self):
        bad = copy.deepcopy(self.document)
        response = next(v for k, v in bad["paths"][self.path]["get"]["responses"].items() if k.startswith("2"))
        response["content"]["application/json"]["schema"] = {
            "type": "object", "properties": {"data": {}}}
        with self.assertRaisesRegex(parity.ContractParityError, "missing concrete schema"):
            self.check(document=bad)

    def test_fixed_read_success_status_cannot_be_relabelled(self):
        bad = copy.deepcopy(self.document)
        responses = bad["paths"][self.path]["get"]["responses"]
        responses["201"] = responses.pop("200")
        with self.assertRaisesRegex(parity.ContractParityError, "fixed handler success status"):
            self.check(document=bad)

    def test_error_response_cannot_be_an_empty_schema(self):
        bad = copy.deepcopy(self.document)
        bad["paths"][self.path]["get"]["responses"]["403"]["content"]["application/json"]["schema"] = {}
        with self.assertRaisesRegex(parity.ContractParityError, "missing concrete schema"):
            self.check(document=bad)

    def test_success_schema_cannot_drop_a_real_go_response_field(self):
        bad = copy.deepcopy(self.document)
        response = next(v for k, v in bad["paths"][self.path]["get"]["responses"].items() if k.startswith("2"))
        media = response["content"]["application/json"]
        envelope = copy.deepcopy(parity.resolve(bad, media["schema"]))
        data = copy.deepcopy(parity.resolve(bad, envelope["properties"]["data"]))
        item = copy.deepcopy(parity.resolve(bad, data["items"]))
        self.assertIn("goal", item["properties"])
        del item["properties"]["goal"]
        data["items"] = item
        envelope["properties"]["data"] = data
        media["schema"] = envelope
        with self.assertRaisesRegex(parity.ContractParityError, "response schema omits Go JSON fields"):
            self.check(document=bad)

    def test_nullable_ref_allows_null_and_checks_the_actual_target_schema(self):
        media = self.document["paths"][self.path]["post"]["requestBody"]["content"]["application/json"]
        self.assertIn("$ref", media["schema"])
        nullable = {"allOf": [copy.deepcopy(media["schema"])], "nullable": True}
        root = parity.normalize_openapi_schema(self.document)
        normalized = parity.normalize_openapi_schema(nullable)
        validator = Draft7Validator(normalized, resolver=RefResolver.from_schema(root))
        example = copy.deepcopy(media.get("example") or next(iter(media["examples"].values()))["value"])
        self.assertFalse(list(validator.iter_errors(None)))
        self.assertFalse(list(validator.iter_errors(example)))
        wrong = copy.deepcopy(example)
        del wrong["expected_context_revision"]
        self.assertTrue(list(validator.iter_errors(wrong)))
        wrong = copy.deepcopy(example)
        wrong["expected_context_revision"] = "wrong type"
        self.assertTrue(list(validator.iter_errors(wrong)))

    def test_nullable_without_a_real_schema_is_rejected(self):
        for value in ({"nullable": True}, {"nullable": True, "allOf": []},
                      {"nullable": True, "allOf": [{}]}):
            with self.subTest(value=value), self.assertRaises(parity.ContractParityError):
                parity.normalize_openapi_schema(value)

    def test_root_request_schema_cannot_drop_a_real_go_field(self):
        bad = copy.deepcopy(self.document)
        media = bad["paths"][self.path]["post"]["requestBody"]["content"]["application/json"]
        body = copy.deepcopy(parity.resolve(bad, media["schema"]))
        self.assertIn("expected_context_revision", body["properties"])
        del body["properties"]["expected_context_revision"]
        media["schema"] = body
        with self.assertRaisesRegex(parity.ContractParityError, "omits Go JSON fields"):
            self.check(document=bad)

    def test_root_request_schema_cannot_change_real_primitive_type(self):
        bad = copy.deepcopy(self.document)
        media = bad["paths"][self.path]["post"]["requestBody"]["content"]["application/json"]
        body = copy.deepcopy(parity.resolve(bad, media["schema"]))
        body["properties"]["expected_context_revision"] = {"type": "string"}
        media["schema"] = body
        with self.assertRaisesRegex(parity.ContractParityError, "disagrees with Go type"):
            self.check(document=bad)

    def test_frontend_changed_method_is_rejected(self):
        bad = dict(self.clients)
        file = "frontend/src/api/lingdoc/g2.ts"
        before = "=> get<Result<ResearchTask[]>>(project(p) + '/research-tasks')"
        self.assertIn(before, bad[file])
        bad[file] = bad[file].replace(before, before.replace("get<", "put<"), 1)
        with self.assertRaisesRegex(parity.ContractParityError, "uses unpublished operation"):
            self.check(clients=bad)

    def test_frontend_changed_path_is_rejected(self):
        bad = dict(self.clients)
        file = "frontend/src/api/lingdoc/g2.ts"
        before = "project(p) + '/research-tasks')"
        self.assertIn(before, bad[file])
        bad[file] = bad[file].replace(before, "project(p) + '/research-tasks-typo')", 1)
        with self.assertRaisesRegex(parity.ContractParityError, "uses unpublished operation"):
            self.check(clients=bad)

    def test_unrecognized_frontend_url_fails_visibly(self):
        bad = dict(self.clients)
        file = "frontend/src/api/lingdoc/g2.ts"
        bad[file] = bad[file].replace("project(p) + '/research-tasks')", "unknownUrl(p))", 1)
        with self.assertRaisesRegex(parity.ContractParityError, "unknown frontend URL helper"):
            self.check(clients=bad)

    def test_removed_backend_route_leaves_a_detectable_documented_orphan(self):
        bad = dict(self.routes)
        file = "internal/lingdoc/workspace/g2_full_http.go"
        text = bad[file]
        start = text.index('group.GET("/projects/:projectId/research-tasks"')
        opening = text.index("(", start)
        end = parity.close_at(text, opening) + 1
        bad[file] = text[:start] + text[end:]
        with self.assertRaisesRegex(parity.ContractParityError, "no longer registered"):
            self.check(routes=bad)

    def test_dynamic_backend_path_cannot_disappear_from_inventory(self):
        bad = dict(self.routes)
        file = "internal/lingdoc/workspace/g2_full_http.go"
        bad[file] = bad[file].replace('group.GET("/projects/:projectId/research-tasks",', 'group.GET(dynamicPath,', 1)
        with self.assertRaisesRegex(parity.ContractParityError, "nonliteral/unrecognized"):
            self.check(routes=bad)


    def test_read_method_extraction_survives_a_narrow_port_receiver(self):
        """A read route that returns (any, error) has one shape source: the method it calls.

        The G2 read routes are declared as ``g2FullRead(func(...) (any, error))``,
        so the response shape is taken from the method the route calls, not from
        the closure's return type. That extraction used to pin the receiver
        name, and a transport that holds a narrow context port instead of the
        aggregate would have lost the shape check for those routes *silently* -
        the parity gate would still pass, with less coverage.

        This test moves the call onto a bundle field and requires the shape
        drift to still be caught.
        """
        file = "internal/lingdoc/workspace/g2_full_http.go"
        source = self.routes[file]
        original = "return service.ListResearchTasks("
        self.assertIn(original, source, "the G2 research task read route must call the aggregate today")
        moved = dict(self.routes)
        moved[file] = source.replace(original, "return ports.Research.ListResearchTasks(", 1)

        bad = copy.deepcopy(self.document)
        response = next(v for k, v in bad["paths"][self.path]["get"]["responses"].items() if k.startswith("2"))
        media = response["content"]["application/json"]
        envelope = copy.deepcopy(parity.resolve(bad, media["schema"]))
        data = copy.deepcopy(parity.resolve(bad, envelope["properties"]["data"]))
        item = copy.deepcopy(parity.resolve(bad, data["items"]))
        self.assertIn("goal", item["properties"])
        del item["properties"]["goal"]
        data["items"] = item
        envelope["properties"]["data"] = data
        media["schema"] = envelope
        with self.assertRaisesRegex(parity.ContractParityError, "response schema omits Go JSON fields"):
            self.check(document=bad, routes=moved)

    def validator_for_request(self, document, path, method):
        normalized = parity.normalize_openapi_schema(document)
        body = normalized["paths"][path][method]["requestBody"]["content"]["application/json"]["schema"]
        return Draft7Validator(body, resolver=RefResolver.from_schema(normalized))

    def test_full_activation_accepts_current_payload_and_rejects_missing_receipt(self):
        path = "/projects/{projectId}/activate"
        validator = self.validator_for_request(self.document, path, "post")
        current = {"expected_spec_revision": 1, "activation_read_id": "actual-activation-read", "expected_project_version": 2, "reviewed_project_version": 2}
        self.assertFalse(list(validator.iter_errors(current)), "real full-profile request must be expressible")
        for name in ["expected_project_version", "reviewed_project_version"]:
            current.pop(name)
        self.assertFalse(list(validator.iter_errors(current)), "compatibility version omissions remain valid")
        current.pop("activation_read_id")
        self.assertTrue(list(validator.iter_errors(current)), "full assembly rejects a missing receipt")

    def test_removed_activation_receipt_requirement_is_detected(self):
        bad = copy.deepcopy(self.document)
        schema = bad["components"]["schemas"]["ActivateProject"]
        schema["required"].remove("activation_read_id")
        with self.assertRaisesRegex(parity.ContractParityError, "full profile needs required activation_read_id"):
            self.check(document=bad)

    def test_each_focused_working_copy_operation_cannot_disappear(self):
        base = "/projects/{projectId}/chapters/{chapterId}/working-copy"
        for path, method in [(base, "get"), (base, "put"), (base + "/commit", "post"), (base + "/restore", "post")]:
            with self.subTest(path=path, method=method):
                bad = copy.deepcopy(self.document)
                self.assertIn(method, bad["paths"].get(path, {}), "production route must be published")
                del bad["paths"][path][method]
                with self.assertRaisesRegex(parity.ContractParityError, "focused workspace operation absent"):
                    self.check(document=bad)

    def test_working_copy_write_shapes_preserve_nullable_bases_and_nonnull_sources(self):
        base = "/projects/{projectId}/chapters/{chapterId}/working-copy"
        valid = {"expected_spec_revision": 1, "expected_working_copy_revision": 1, "source_ids": [], "base_chapter_version_id": None, "citation_usages": None}
        validator = self.validator_for_request(self.document, base, "put")
        self.assertFalse(list(validator.iter_errors(valid)), "first-version nullable base and omitted empty body are real valid input")
        for name, wrong in [("source_ids", None), ("expected_working_copy_revision", 0), ("expected_working_copy_revision", "1")]:
            self.assertTrue(list(validator.iter_errors({**valid, name: wrong})))
        wrong = copy.deepcopy(valid)
        wrong.pop("source_ids")
        self.assertTrue(list(validator.iter_errors(wrong)))
        for suffix in ["commit", "restore"]:
            body = {"expected_spec_revision": 1, "expected_working_copy_revision": 1, "expected_chapter_version_id": None}
            if suffix == "restore":
                body["chapter_version_id"] = "real-old-version"
            validator = self.validator_for_request(self.document, base + "/" + suffix, "post")
            self.assertFalse(list(validator.iter_errors(body)))
            self.assertTrue(list(validator.iter_errors({**body, "expected_chapter_version_id": 7})))
            if suffix == "restore":
                body.pop("chapter_version_id")
                self.assertTrue(list(validator.iter_errors(body)))

    def test_commit_receipt_cannot_be_replaced_by_a_full_chapter_version(self):
        bad = copy.deepcopy(self.document)
        response = bad["paths"]["/projects/{projectId}/chapters/{chapterId}/working-copy/commit"]["post"]["responses"]["201"]
        envelope = copy.deepcopy(parity.resolve(bad, response["content"]["application/json"]["schema"]))
        envelope["properties"]["data"] = {"$ref": "#/components/schemas/ChapterVersion"}
        response["content"]["application/json"]["schema"] = envelope
        with self.assertRaisesRegex(parity.ContractParityError, "focused success data omits actual DTO fields"):
            self.check(document=bad)

    def test_working_copy_base_cannot_become_nonnullable(self):
        bad = copy.deepcopy(self.document)
        bad["components"]["schemas"]["SaveWorkingCopyRequest"]["properties"]["base_chapter_version_id"].pop("nullable")
        with self.assertRaisesRegex(parity.ContractParityError, "pointer must allow null"):
            self.check(document=bad)


if __name__ == "__main__":
    unittest.main()
