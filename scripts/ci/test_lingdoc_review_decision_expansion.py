"""Guard tests for the contract's per-review-item decision expansion (#618).

What this file guards
---------------------
The F01 contract has to say something about a collection whose size the model
decides at run time: every review item of a chapter needs one decision, and
how many review items there are is not known until the model has answered.  The
contract writes that down as a projection (``{"$expand": ...}``) instead of a
literal list, so the decisions it sends are exactly the review items the
chapter carries.

Four things have to stay true, and each of them is a way the fix could
silently become the defect it fixed:

*One set of rules.*  The F01 runner and the contract's static validator used
to carry their own copies of the ``{{name}}`` rules.  Two copies drift, and a
contract the run and the validator judge differently is a contract nobody can
trust.  The rules now live in ``scripts/lingdoc_mock/workflow_expand.py`` and
both sides read it from there -- including when the contract directory is a
scratch copy, which is how the review workflow runs the validator.

*The validator still refuses a decision set that is not the review item set.*
The server requires one decision per review item; the expansion must not make
the contract able to send fewer.  A negative case points the collection at a
set of a different size and the validator has to refuse.

*The expansion refuses instead of guessing.*  A collection that was never
captured, a collection that is not an array, an element that does not carry
the field: each of them stops the run before a single request is sent.  A
silently empty array would turn "the model produced three review items" into
"the contract decided none of them needed a decision".

*A refusal is never a pass.*  The disposition the contract sends is
``retained_warning``, and nothing in the contract or in the runner may turn
the server's 422 into a success.

Where this file lives
---------------------
It sits in ``scripts/ci/`` because it is a guard test, and the review workflow
collects the guard tests from there (``python -m unittest discover -s
scripts/ci -p 'test_lingdoc_*.py'``).
"""
from __future__ import annotations

import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

from jsonschema import Draft7Validator, RefResolver

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT))

from scripts.lingdoc_mock import run_f01, workflow_expand  # noqa: E402
from scripts.lingdoc_mock.run_f01 import WorkflowError  # noqa: E402

CONTRACTS = REPO_ROOT / "docs" / "08-本轮实施方案" / "contracts"
WORKFLOW = CONTRACTS / "workflow.json"
OPENAPI = CONTRACTS / "openapi.json"
VALIDATOR = CONTRACTS / "validate_artifacts.py"
EXPAND_MODULE = REPO_ROOT / "scripts" / "lingdoc_mock" / "workflow_expand.py"

# The reason the contract sends with every decision, and the disposition it
# sends: a retained warning does not block the release, but it has to be
# listed with the file.
REASON = "仅作内部演示，保留该项并随文件明确列出。"
DISPOSITION = "retained_warning"


class FakeResponse:
    """One provider answer, for the runner to read."""

    def __init__(self, payload: bytes, status: int = 200) -> None:
        self.payload, self.status = payload, status
        self.headers = {"Content-Type": "application/json"}

    def read(self, limit: int = -1) -> bytes:
        return self.payload[:limit]

    def close(self) -> None:
        pass


def contract() -> dict:
    return json.loads(WORKFLOW.read_text(encoding="utf-8"))


def step_of(document: dict, step_id: str) -> dict:
    return next(step for step in document["steps"] if step["id"] == step_id)


def expand_node(collection: str, field: str = "id") -> dict:
    """The projection the contract uses: one decision per element."""
    return {"$expand": {"collection": collection,
                        "item": {"review_item_id": {"$item": field},
                                 "disposition": DISPOSITION,
                                 "reason": REASON}}}


def one_step_spec(body: dict) -> dict:
    return {"id": "EXPAND-GUARD",
            "steps": [{"id": "EXPAND-GUARD-01",
                       "operation_id": "confirmChapter",
                       "actor": "u-owner",
                       "expected_http": 201,
                       "request": {"path_params": {"projectId": "p-1", "chapterId": "c-1"},
                                   "headers": {"Idempotency-Key": "guard-0001"},
                                   "json": {"expected_chapter_version_id": "cv-1",
                                            "expected_spec_revision": 1,
                                            "review_decisions": body}},
                       "assertions": []}]}


def drive(body: dict, variables: dict | None = None, status: int = 201):
    """Run one step that carries the projection; report what was requested."""
    requested: list[object] = []

    def opener(request, timeout):
        requested.append(request)
        return FakeResponse(b'{"data":{}}', status)

    spec = run_f01.spec_from_document(one_step_spec(body), "expansion guard")
    openapi = json.loads(OPENAPI.read_text(encoding="utf-8"))
    runner = run_f01.ScenarioRunner(openapi, spec, opener=opener, variables=variables or {})
    return runner, requested


def run_validator(mutate=None) -> subprocess.CompletedProcess:
    """Run the contract's static validator over a scratch copy of the contract.

    The scratch copy is what the review workflow does before it runs the
    validator (``cp -R docs/08-本轮实施方案/contracts/. "$scratch/"``), and it
    is the shape that proves the validator finds the expansion module under
    ``--source-root`` instead of next to itself.
    """
    with tempfile.TemporaryDirectory() as directory:
        scratch = Path(directory) / "contracts"
        shutil.copytree(CONTRACTS, scratch)
        document = json.loads((scratch / "workflow.json").read_text(encoding="utf-8"))
        if mutate is not None:
            mutate(document)
        (scratch / "workflow.json").write_text(
            json.dumps(document, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        # Never mistake the committed report for a result from this run.
        (scratch / "validation-result.json").unlink(missing_ok=True)
        assert not (scratch / "workflow_expand.py").exists(), \
            "the scratch copy must not carry the expansion module: the validator has to find it under --source-root"
        return subprocess.run(
            [sys.executable, "-X", "utf8", str(scratch / "validate_artifacts.py"),
             "--source-root", str(REPO_ROOT)],
            capture_output=True, text=True, encoding="utf-8", errors="replace")


class ReviewDecisionExpansionTest(unittest.TestCase):
    """A5: the projection produces one decision per review item."""

    def test_three_review_items_expand_to_three_decisions(self):
        items = [{"id": "r-1", "statement": "一"},
                 {"id": "r-2", "statement": "二"},
                 {"id": "r-3", "statement": "三"}]
        decisions = run_f01.substitute(expand_node("question_review_items"),
                                        {"question_review_items": items})
        self.assertEqual(len(decisions), 3)
        self.assertEqual([decision["review_item_id"] for decision in decisions],
                         ["r-1", "r-2", "r-3"])
        self.assertTrue(all(decision["disposition"] == DISPOSITION for decision in decisions))
        self.assertTrue(all(decision["reason"] == REASON for decision in decisions))

    def test_the_contract_own_projection_expands_to_one_decision_per_review_item(self):
        # The contract's own F01-16 node, not a synthetic one: three review
        # items must become three decisions, one per item.
        node = step_of(contract(), "F01-16")["request"]["json"]["review_decisions"]
        items = [{"id": "r-1"}, {"id": "r-2"}, {"id": "r-3"}]
        decisions = run_f01.substitute(node, {"question_review_items": items})
        self.assertEqual([decision["review_item_id"] for decision in decisions],
                         ["r-1", "r-2", "r-3"])
        self.assertTrue(all(decision["disposition"] == DISPOSITION for decision in decisions))

    def test_the_expanded_body_is_still_the_body_the_wire_schema_accepts(self):
        # The projection is a contract-internal expression: what reaches the
        # server is the array the OpenAPI document already describes.
        openapi = json.loads(OPENAPI.read_text(encoding="utf-8"))
        resolver = RefResolver.from_schema(openapi)
        validator = Draft7Validator(openapi["components"]["schemas"]["ConfirmChapterRequest"],
                                    resolver=resolver)
        body = run_f01.substitute(
            step_of(contract(), "F01-16")["request"]["json"],
            {"question_version": "cv-question-2",
             "spec_revision": 1,
             "question_review_items": [{"id": "r-1"}, {"id": "r-2"}, {"id": "r-3"}]})
        self.assertEqual(len(body["review_decisions"]), 3)
        errors = list(validator.iter_errors(body))
        self.assertEqual(errors, [], "the expanded body must satisfy the wire schema")

    def test_the_runner_and_the_module_agree_on_the_expansion(self):
        items = [{"id": "r-1"}, {"id": "r-2"}]
        node = expand_node("question_review_items")
        self.assertEqual(run_f01.substitute(node, {"question_review_items": items}),
                         workflow_expand.expand_value(node, {"question_review_items": items}))

    def test_an_empty_collection_expands_to_an_empty_array(self):
        self.assertEqual(run_f01.substitute(expand_node("method_review_items"),
                                             {"method_review_items": []}),
                         [])


class ExpansionRefusalTest(unittest.TestCase):
    """A6: a value that cannot be expanded stops the run before any request."""

    def test_a_collection_that_was_never_captured_stops_the_run(self):
        runner, requested = drive(expand_node("question_review_items"))
        with self.assertRaises(WorkflowError) as caught:
            runner.run()
        self.assertIn("not captured yet", str(caught.exception))
        self.assertEqual(requested, [], "no request may be made when the value cannot be expanded")

    def test_a_collection_that_is_not_an_array_stops_the_run(self):
        runner, requested = drive(expand_node("question_review_items"),
                                  {"question_review_items": {"id": "r-1"}})
        with self.assertRaises(WorkflowError) as caught:
            runner.run()
        self.assertIn("must hold an array", str(caught.exception))
        self.assertEqual(requested, [], "no request may be made when the value cannot be expanded")

    def test_an_element_without_the_field_stops_the_run(self):
        runner, requested = drive(expand_node("question_review_items"),
                                  {"question_review_items": [{"id": "r-1"},
                                                            {"statement": "没有 id"}]})
        with self.assertRaises(WorkflowError) as caught:
            runner.run()
        self.assertIn("$item field is missing", str(caught.exception))
        self.assertEqual(requested, [], "no request may be made when the value cannot be expanded")

    def test_a_malformed_expansion_node_stops_the_run(self):
        runner, requested = drive({"$expand": {"collection": "question_review_items"}})
        with self.assertRaises(WorkflowError) as caught:
            runner.run()
        self.assertIn("item template", str(caught.exception))
        self.assertEqual(requested, [], "no request may be made when the value cannot be expanded")


class ValidatorDecisionSetTest(unittest.TestCase):
    """A4: the validator still refuses a decision set that is not the review items."""

    def test_the_validator_accepts_the_contract_as_it_stands(self):
        result = run_validator()
        self.assertEqual(result.returncode, 0, result.stderr[-2000:])

    def test_the_validator_refuses_a_collection_whose_size_is_not_the_review_item_count(self):
        def point_at_the_other_collection(document: dict) -> None:
            node = step_of(document, "F01-16")["request"]["json"]["review_decisions"]
            node["$expand"]["collection"] = "method_review_items"

        result = run_validator(point_at_the_other_collection)
        self.assertNotEqual(result.returncode, 0,
                            "the validator accepted a decision set that is not the review item set")
        self.assertIn("review_decisions", result.stderr)

    def test_the_validator_refuses_decisions_that_name_another_field(self):
        def read_another_field(document: dict) -> None:
            node = step_of(document, "F01-16")["request"]["json"]["review_decisions"]
            node["$expand"]["item"]["review_item_id"] = {"$item": "origin_candidate_id"}

        result = run_validator(read_another_field)
        self.assertNotEqual(result.returncode, 0,
                            "the validator accepted decisions that are not the review items")
        self.assertIn("review_decisions", result.stderr)


class OneSetOfRulesTest(unittest.TestCase):
    """A7: the runner and the validator read the same expansion module."""

    def test_both_sides_delegate_to_the_one_module(self):
        self.assertEqual(Path(run_f01.workflow_expand.__file__).resolve(), EXPAND_MODULE.resolve())
        runner_source = (REPO_ROOT / "scripts" / "lingdoc_mock" / "run_f01.py").read_text(encoding="utf-8")
        self.assertIn("workflow_expand.expand_value", runner_source)
        validator_source = VALIDATOR.read_text(encoding="utf-8")
        self.assertIn("workflow_expand.expand_value", validator_source)
        self.assertIn("scripts/lingdoc_mock/workflow_expand.py", validator_source)

    def test_the_validator_finds_the_module_under_the_source_root(self):
        # The scratch copy carries no expansion module, so a passing run means
        # the validator loaded it from --source-root -- which is how the review
        # workflow runs it.
        result = run_validator()
        self.assertEqual(result.returncode, 0, result.stderr[-2000:])

    def test_both_sides_refuse_the_same_missing_collection_with_the_same_words(self):
        def drop_the_capture(document: dict) -> None:
            step_of(document, "F01-13")["capture"].pop("question_review_items")

        result = run_validator(drop_the_capture)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not captured yet", result.stderr)

        runner, requested = drive(expand_node("question_review_items"))
        with self.assertRaises(WorkflowError) as caught:
            runner.run()
        self.assertIn("not captured yet", str(caught.exception))
        self.assertEqual(requested, [])


class RefusalIsNotAPassTest(unittest.TestCase):
    """A10: a refusal never becomes a success."""

    def test_every_decision_the_contract_makes_is_a_retained_warning(self):
        document = contract()
        for step_id in ("F01-16", "F01-17"):
            node = step_of(document, step_id)["request"]["json"]["review_decisions"]
            self.assertIn("$expand", node, step_id + " does not project over the review items")
            item = node["$expand"]["item"]
            self.assertEqual(item["disposition"], DISPOSITION, step_id)
            self.assertTrue(item["reason"].strip(), step_id)

    def test_the_contract_projects_over_the_review_items_of_both_chapters(self):
        document = contract()
        self.assertEqual(step_of(document, "F01-13")["capture"]["question_review_items"],
                         "/data/review_items")
        self.assertEqual(step_of(document, "F01-15")["capture"]["method_review_items"],
                         "/data/review_items")
        self.assertEqual(
            step_of(document, "F01-16")["request"]["json"]["review_decisions"]["$expand"]["collection"],
            "question_review_items")
        self.assertEqual(
            step_of(document, "F01-17")["request"]["json"]["review_decisions"]["$expand"]["collection"],
            "method_review_items")

    def test_the_contract_does_not_expect_the_refusal_it_is_about(self):
        document = contract()
        statuses = {step.get("expected_http") for step in document["steps"]}
        self.assertNotIn(422, statuses,
                         "the contract must not declare the server's refusal as an expected success")
        for step_id in ("F01-16", "F01-17"):
            self.assertEqual(step_of(document, step_id)["expected_http"], 201, step_id)

    def test_a_refusal_is_never_recorded_as_a_pass(self):
        runner, requested = drive(expand_node("question_review_items"),
                                  {"question_review_items": [{"id": "r-1"}]}, status=422)
        runner.run(stop_on_mismatch=False)
        report = runner.report("failed")
        kinds = [failure.get("kind") for failure in report.get("failures") or []]
        self.assertIn("http_status", kinds,
                      "a refusal must be recorded as a failure, not as a passed step")
        self.assertEqual(report["runner_status"], "failed")


if __name__ == "__main__":
    unittest.main()
