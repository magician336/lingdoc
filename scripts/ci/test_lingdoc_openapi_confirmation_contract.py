"""Guard tests for the acceptance contract's confirmation payload (#681).

The adoption request body is described in three places -- the published
OpenAPI document, the server's request struct, and the front-end client type
-- and all three have to say the same thing about one field: the explicit
adoption confirmation.

The server refuses an adoption without that payload, so a contract that
describes the body as valid without it is not "a bit loose": it publishes a
request the server will reject with a validation error.  The three copies can
drift apart silently, because each of them looks self-consistent on its own.

These tests read the repository as it is and keep no copy of it:

* the **server struct** -- ``acceptCandidateRequest`` in the adoption HTTP
  handler -- is parsed for its JSON field names and for the fields the server
  marks ``binding:"required"``;
* the **published contract** -- the ``AcceptCandidate`` schema in the OpenAPI
  document -- is read for its ``required`` list;
* the **client type** -- the front-end ``AcceptCandidateInput`` interface and
  the rewrite path that assembles a confirmation -- is read for whether the
  confirmation field is optional there.

Two rules bind the server and the contract together:

* **rule A** -- every field the server marks required must be written as
  required in the contract.  A server-required field missing from the
  contract is the drift this test exists to catch;
* **rule B** -- every field the contract writes as required must exist in the
  server struct.  A contract field the server does not know is a request the
  server rejects while decoding, because the decoder refuses unknown fields.

Both directions matter, and each one alone would let a real defect through.
"""
from __future__ import annotations

import json
from pathlib import Path
import re
import unittest

REPO_ROOT = Path(__file__).resolve().parents[2]

#: The server's request struct for the adoption endpoint.
SERVER_REQUEST_FILE = (
    REPO_ROOT / "internal" / "lingdoc" / "candidateadoption" / "candidate_adoption_http.go"
)

#: The published contract.
CONTRACT_FILE = REPO_ROOT / "docs" / "08-本轮实施方案" / "contracts" / "openapi.json"

#: The front-end client type for the same request body.
CLIENT_TYPE_FILE = REPO_ROOT / "frontend" / "src" / "api" / "lingdoc" / "candidateAdoption.ts"

#: The front-end rewrite path, which assembles a confirmation of its own.
REWRITE_PATH_FILE = REPO_ROOT / "frontend" / "src" / "views" / "lingdoc" / "candidateAdoption.ts"

#: The name of the server struct the contract describes.
SERVER_STRUCT = "acceptCandidateRequest"

#: The name of the contract schema the server struct describes.
CONTRACT_SCHEMA = "AcceptCandidate"

#: The confirmation field: the payload the server refuses to adopt without.
CONFIRMATION_FIELD = "confirmation"

#: One struct field: a name, a Go type and a tag string.
FIELD_PATTERN = re.compile(
    r"^\s*(?P<name>[A-Za-z_]\w*)\s+(?P<type>[^`]+?)\s*`(?P<tags>[^`]*)`\s*$"
)

#: The JSON name inside a struct tag.
JSON_TAG_PATTERN = re.compile(r'json:"(?P<json>[^",]+)')

#: The binding tag inside a struct tag.  A binding may list several rules
#: (``binding:"required,max=8"``), so the rule is looked for inside the value
#: rather than compared as a whole.
BINDING_REQUIRED_PATTERN = re.compile(r'binding:"[^"]*\brequired\b')


def server_struct_fields(source: str, struct_name: str) -> dict[str, bool]:
    """Read one struct's JSON field names out of Go source.

    The parse is scoped to the struct's own body: the source is cut at the
    declaration of the struct and at its closing brace, so fields of other
    structs in the same file cannot leak in.  A field is reported as required
    when its tag carries the ``required`` binding rule.

    Returns a mapping from JSON field name to whether the server requires it.
    """
    declaration = f"type {struct_name} struct {{"
    start = source.find(declaration)
    if start < 0:
        raise AssertionError(f"{struct_name} is not declared in the server source")
    body_start = source.index("\n", start) + 1
    depth = 1
    position = body_start
    while depth > 0:
        character = source[position]
        if character == "{":
            depth += 1
        elif character == "}":
            depth -= 1
        position += 1
    body = source[body_start : position - 1]

    fields: dict[str, bool] = {}
    for line in body.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("//"):
            continue
        matched = FIELD_PATTERN.match(line)
        if matched is None:
            continue
        tags = matched.group("tags")
        json_match = JSON_TAG_PATTERN.search(tags)
        if json_match is None:
            continue
        fields[json_match.group("json")] = bool(BINDING_REQUIRED_PATTERN.search(tags))
    if not fields:
        raise AssertionError(f"{struct_name} declares no JSON field; the parse found nothing")
    return fields


def client_interface_body(source: str, interface_name: str) -> str:
    """Read one TypeScript interface's body out of a client source file."""
    declaration = f"export interface {interface_name} {{"
    start = source.find(declaration)
    if start < 0:
        raise AssertionError(f"{interface_name} is not declared in the client source")
    body_start = source.index("\n", start) + 1
    depth = 1
    position = body_start
    while depth > 0:
        character = source[position]
        if character == "{":
            depth += 1
        elif character == "}":
            depth -= 1
        position += 1
    return source[body_start : position - 1]


class ServerContractAgreementTests(unittest.TestCase):
    """The server struct and the published contract must agree."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.server_source = SERVER_REQUEST_FILE.read_text(encoding="utf-8")
        cls.server_fields = server_struct_fields(cls.server_source, SERVER_STRUCT)
        contract = json.loads(CONTRACT_FILE.read_text(encoding="utf-8"))
        cls.schema = contract["components"]["schemas"][CONTRACT_SCHEMA]
        cls.contract_required = list(cls.schema.get("required", []))

    def test_the_server_struct_declares_the_confirmation_as_required(self):
        """The premise of this guard: the server refuses the body without it."""
        self.assertIn(
            CONFIRMATION_FIELD,
            self.server_fields,
            f"{SERVER_STRUCT} declares no {CONFIRMATION_FIELD!r} field",
        )
        self.assertTrue(
            self.server_fields[CONFIRMATION_FIELD],
            f"the server does not mark {CONFIRMATION_FIELD!r} as required, so the "
            "contract and the server no longer describe the same request",
        )

    def test_every_server_required_field_is_required_in_the_contract(self):
        """Rule A: what the server demands, the contract has to write down."""
        missing = sorted(
            name
            for name, required in self.server_fields.items()
            if required and name not in self.contract_required
        )
        self.assertEqual(
            missing,
            [],
            f"the server requires {missing} but the contract does not list them as "
            f"required; {CONTRACT_SCHEMA}.required = {self.contract_required}",
        )

    def test_every_contract_required_field_exists_in_the_server_struct(self):
        """Rule B: the contract must not demand a field the server has no."""
        unknown = sorted(name for name in self.contract_required if name not in self.server_fields)
        self.assertEqual(
            unknown,
            [],
            f"the contract requires {unknown} but {SERVER_STRUCT} has no such JSON "
            f"field; the server refuses unknown fields while decoding, so such a "
            f"request is rejected. {SERVER_STRUCT} fields = {sorted(self.server_fields)}",
        )

    def test_the_contract_requires_the_confirmation(self):
        """The drift this ticket is about, stated as a plain assertion."""
        self.assertIn(
            CONFIRMATION_FIELD,
            self.contract_required,
            f"{CONTRACT_SCHEMA}.required does not list {CONFIRMATION_FIELD!r}; the "
            "server rejects such a body, so the contract must not publish it",
        )

    def test_the_contract_still_requires_what_it_required_before(self):
        """Adding a required field must not quietly drop the old ones."""
        for name in (
            "candidate_id",
            "expected_chapter_version_id",
            "expected_spec_revision",
            "replace_existing",
        ):
            with self.subTest(field=name):
                self.assertIn(name, self.contract_required)

    def test_the_contract_does_not_relax_additional_properties(self):
        """The contract stays closed: an unknown field is still a rejection."""
        self.assertIs(self.schema.get("additionalProperties"), False)


class ClientAgreementTests(unittest.TestCase):
    """The client type must ask for the same payload the server demands."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.client_source = CLIENT_TYPE_FILE.read_text(encoding="utf-8")
        cls.rewrite_source = REWRITE_PATH_FILE.read_text(encoding="utf-8")

    def test_the_client_type_requires_the_confirmation(self):
        """``AcceptCandidateInput.confirmation`` is not optional."""
        body = client_interface_body(self.client_source, "AcceptCandidateInput")
        matched = re.search(
            rf"^\s*{CONFIRMATION_FIELD}\s*(\?)?\s*:\s*(?P<type>[^,;]+)",
            body,
            re.MULTILINE,
        )
        self.assertIsNotNone(
            matched,
            f"AcceptCandidateInput declares no {CONFIRMATION_FIELD!r} field",
        )
        self.assertIsNone(
            matched.group(1),
            f"the client type makes {CONFIRMATION_FIELD!r} optional; the server "
            "refuses an adoption without it",
        )
        declared = matched.group("type")
        self.assertNotIn(
            "undefined",
            declared,
            f"the client type allows {CONFIRMATION_FIELD!r} to be undefined; the "
            "server refuses an adoption without it",
        )
        self.assertIn("AdoptionConfirmation", declared)

    def test_the_rewrite_path_assembles_a_confirmation(self):
        """The rewrite path sends a confirmation of its own."""
        matched = re.search(
            r"function\s+rewriteAdoptionConfirmation\s*\([^)]*\)[^{]*\{",
            self.rewrite_source,
        )
        self.assertIsNotNone(
            matched, "the rewrite path no longer assembles a confirmation"
        )
        body = self.rewrite_source[matched.end() :]
        for name in (
            "writing_action",
            "adoption_target",
            "confirmed_at",
            "confirmed_by",
            "origin",
        ):
            with self.subTest(field=name):
                self.assertIn(f"{name}:", body)
        self.assertIn(f"{CONFIRMATION_FIELD}:", body)


if __name__ == "__main__":
    unittest.main()
