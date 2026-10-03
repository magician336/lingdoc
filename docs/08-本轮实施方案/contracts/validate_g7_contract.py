"""Validate the proposed G7 contract examples; this does not call a service."""
import copy
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent
contract = json.loads((ROOT / "g7-writing-contract.json").read_text(encoding="utf-8"))
openapi = json.loads((ROOT / "openapi.json").read_text(encoding="utf-8"))
scenarios = json.loads((ROOT / "scenarios.json").read_text(encoding="utf-8"))


def normalize_nullable(value):
    if isinstance(value, list):
        return [normalize_nullable(item) for item in value]
    if not isinstance(value, dict):
        return value
    normalized = {key: normalize_nullable(item) for key, item in value.items() if key != "nullable"}
    if value.get("nullable"):
        normalized["type"] = [normalized["type"], "null"]
    return normalized


schemas = copy.deepcopy(contract["schemas"])
schemas["ExistingSaveChapter"] = normalize_nullable(openapi["components"]["schemas"]["SaveChapter"])


def violations(instance, schema, path="$", depth=0):
    """Check the JSON Schema subset used by this frozen contract, without third-party packages."""
    if depth > 40:
        return [f"{path}: schema reference depth exceeded"]
    if "$ref" in schema:
        target = contract
        for part in schema["$ref"].removeprefix("#/").split("/"):
            target = target[part.replace("~1", "/").replace("~0", "~")]
        return violations(instance, target, path, depth + 1)

    errors = []
    expected = schema.get("type")
    expected_types = expected if isinstance(expected, list) else [expected]
    type_checks = {
        "object": lambda value: isinstance(value, dict),
        "array": lambda value: isinstance(value, list),
        "string": lambda value: isinstance(value, str),
        "integer": lambda value: isinstance(value, int) and not isinstance(value, bool),
        "number": lambda value: isinstance(value, (int, float)) and not isinstance(value, bool),
        "boolean": lambda value: isinstance(value, bool),
        "null": lambda value: value is None,
    }
    if expected and not any(type_checks[kind](instance) for kind in expected_types):
        return [f"{path}: expected {expected}"]
    if "enum" in schema and instance not in schema["enum"]:
        errors.append(f"{path}: value is not in enum")
    if isinstance(instance, dict):
        for key in schema.get("required", []):
            if key not in instance:
                errors.append(f"{path}: missing required property {key}")
        properties = schema.get("properties", {})
        if schema.get("additionalProperties") is False:
            errors.extend(f"{path}: unknown property {key}" for key in instance if key not in properties)
        for key, value in instance.items():
            if key in properties:
                errors.extend(violations(value, properties[key], f"{path}/{key}", depth + 1))
    elif isinstance(instance, list):
        if schema.get("uniqueItems") and len({json.dumps(value, sort_keys=True, ensure_ascii=False) for value in instance}) != len(instance):
            errors.append(f"{path}: duplicate array item")
        if "items" in schema:
            for index, value in enumerate(instance):
                errors.extend(violations(value, schema["items"], f"{path}/{index}", depth + 1))
    elif isinstance(instance, str):
        if len(instance) < schema.get("minLength", 0):
            errors.append(f"{path}: string shorter than minLength")
        if len(instance) > schema.get("maxLength", float("inf")):
            errors.append(f"{path}: string longer than maxLength")
        if schema.get("format") == "date-time":
            from datetime import datetime
            try:
                datetime.fromisoformat(instance.replace("Z", "+00:00"))
            except ValueError:
                errors.append(f"{path}: invalid date-time")
    elif isinstance(instance, (int, float)) and not isinstance(instance, bool):
        if instance < schema.get("minimum", float("-inf")):
            errors.append(f"{path}: below minimum")
    return errors

results = []
for example in contract["examples"]:
    errors = violations(example["value"], schemas[example["schema"]])
    accepted = not errors
    assert accepted == example["accepted"], (
        f"{example['id']}: expected accepted={example['accepted']}, "
        f"got accepted={accepted}; {errors[0] if errors else 'no validation error'}"
    )
    results.append(example["id"])

for case in contract["semantic_cases"]:
    utf16_length = len(case["selected_text"].encode("utf-16-le")) // 2
    accepted = case["start_utf16"] < case["end_utf16"] and utf16_length == case["end_utf16"] - case["start_utf16"]
    assert accepted == case["accepted"], f"{case['id']}: UTF-16 selection boundary expectation mismatch"
    results.append(case["id"])

commit_response = next(example["value"] for example in contract["examples"]
                      if example["id"] == "commit-working-copy-response-valid")
assert commit_response["next_working_copy_revision"] == commit_response["committed_working_copy_revision"] + 1, (
    "a successful explicit commit must advance the work-copy revision exactly once"
)
candidate = next(example["value"] for example in contract["examples"] if example["id"] == "candidate-ready-valid")
authorized_source_ids = [source["source_id"] for source in candidate["authorized_sources"]]
assert len(authorized_source_ids) == len(set(authorized_source_ids)), "candidate repeats an authorized source snapshot"
assert set(candidate["source_ids"]) == set(authorized_source_ids), "candidate source IDs differ from its generation-time authorization snapshot"

smoke = next((item for item in scenarios["scenarios"] if item["id"] == "G7-01-chapter-read-write"), None)
assert smoke is not None, "G7-01 chapter read/write smoke scenario is missing"
assert smoke["starting_state"] == "S1", "G7-01 smoke must use the declared two-chapter fixture"
assert [step["operation_id"] for step in smoke["steps"]] == ["listChapters", "saveChapter", "listChapters"], (
    "G7-01 smoke must read, save one chapter, then read it back"
)
assert smoke["steps"][2]["readback_of"] == smoke["steps"][1]["id"], "G7-01 smoke readback must point to its save"
operation_index = {}
for route, methods in openapi["paths"].items():
    for method, operation in methods.items():
        operation_index[operation["operationId"]] = (method, route, operation)
available = {"project.id"}
for step in smoke["steps"]:
    name = step["operation_id"]
    assert name in operation_index, f"G7-01 smoke uses an undeclared operation: {name}"
    method, route, operation = operation_index[name]
    assert step["expected_http"] in [int(code) for code in operation["responses"]], f"{step['id']}: undeclared HTTP status"
    if "request" not in step:
        continue
    request = step["request"]
    required_path = {part[1:-1] for part in route.split("/") if part.startswith("{") and part.endswith("}")}
    assert set(request["path_params"]) == required_path, f"{step['id']}: request path parameters differ from OpenAPI"
    for value in (request["path_params"], request.get("headers", {}), request.get("json")):
        encoded = json.dumps(value, ensure_ascii=False)
        for variable in re.findall(r"\{\{([A-Za-z_][A-Za-z0-9_.-]*)\}\}", encoded):
            assert variable in available, f"{step['id']}: variable {variable} is not captured yet"
    if method.lower() == "post":
        body = request["json"]
        body_errors = violations(body, schemas["ExistingSaveChapter"])
        assert not body_errors, f"{step['id']}: invalid saveChapter body: {body_errors[0] if body_errors else ''}"
        assert any(key.lower() == "idempotency-key" for key in request["headers"]), f"{step['id']}: missing idempotency key"
    for name, pointer in step.get("capture", {}).items():
        assert pointer.startswith("/"), f"{step['id']}: capture pointer must be a JSON pointer"
        available.add(name)
assert len(smoke["steps"]) == 3, "G7-01 sm