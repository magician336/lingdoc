"""Run in a real checkout: no hand-maintained copy of LingDoc business fixtures."""
import json
import unittest
from scripts.lingdoc_mock.server import ContractMock, MockError, PREFIX, SPEC_PATH


class RepositoryContractTests(unittest.TestCase):
    def test_all_current_response_examples_and_success_failure(self):
        contract = ContractMock.load(SPEC_PATH)
        self.assertGreater(contract.checked_examples, 0)
        path = PREFIX + "/templates/template-demo"
        status, value = contract.respond("GET", path, {"X-LingDoc-Mock-Status": "200", "X-LingDoc-Mock-Example": "success"})
        self.assertEqual(status, 200)
        self.assertTrue(value["data"]["is_demo"])
        status, value = contract.respond("GET", path, {"X-LingDoc-Mock-Status": "422", "X-LingDoc-Mock-Example": "invalid_state"})
        self.assertEqual((status, value["error"]["code"]), (422, "invalid_state"))

    def test_corrupted_shared_example_is_rejected(self):
        spec = json.loads(SPEC_PATH.read_text(encoding="utf-8"))
        example = spec["paths"]["/templates/{templateId}"]["get"]["responses"]["200"]["content"]["application/json"]["examples"]["success"]["value"]
        example.pop("data")
        with self.assertRaises(MockError) as caught:
            ContractMock(spec)
        self.assertEqual(caught.exception.code, "mock_contract_violation")


if __name__ == "__main__":
    unittest.main()
