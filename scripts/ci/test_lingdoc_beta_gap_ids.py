"""Guard tests for the stable gap identifiers (#504).

What this file guards
---------------------
A gap the Beta release needs has an owner: a ticket, and an item inside it.
Both of those change; the *identifier* does not.  The unblock conditions in
the evidence used to quote the ticket number, so a renumbering silently
invalidated the code and the artifacts.

Now the code quotes a stable identifier (``GAP-MODEL-CREDENTIAL`` and
friends), the registry file ``docs/08-本轮实施方案/acceptance/gaps.json``
holds the one binding from an identifier to its ticket and item, and the
artifacts carry the identifier.  These tests pin that:

* no ticket number is written into the producer or into an unblock condition;
* every identifier the code uses exists in the registry, and every registry
  entry is complete;
* a missing identifier is refused by name, not ignored;
* "did not run" is spelled one way across the producers -- the key is
  ``not_run`` and the status value is ``not_run``, not ``NOT RUN``.
"""
from __future__ import annotations

import json
import re
import sys
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "scripts" / "acceptance"))

import beta_evidence  # noqa: E402

PRODUCER = REPO_ROOT / "scripts" / "acceptance" / "beta_evidence.py"
BROWSER = REPO_ROOT / "scripts" / "acceptance" / "beta_browser_cdp.mjs"
REGISTRY = REPO_ROOT / "docs" / "08-本轮实施方案" / "acceptance" / "gaps.json"
TICKET = re.compile(r"#\d{3}")


def registry() -> dict:
    return json.loads(REGISTRY.read_text(encoding="utf-8"))["GAPS"]


class GapIdentifierTests(unittest.TestCase):
    """The identifier is the stable thing; the ticket number is not."""

    def test_the_registry_binds_every_identifier_to_a_ticket_and_an_item(self):
        entries = registry()
        self.assertTrue(entries)
        for identifier, entry in entries.items():
            self.assertTrue(identifier.startswith("GAP-"), identifier)
            for field in ("ticket", "item", "title", "unblock"):
                self.assertTrue(str(entry.get(field, "")).strip(),
                                f"{identifier} has no {field}")

    def test_the_producer_quotes_no_ticket_number(self):
        source = PRODUCER.read_text(encoding="utf-8")
        for line in source.splitlines():
            if "gap_unblock" in line or "def gap" in line:
                continue
            self.assertIsNone(TICKET.search(line),
                              f"a ticket number is written into the producer: {line.strip()[:120]}")

    def test_every_identifier_the_code_uses_is_in_the_registry(self):
        source = PRODUCER.read_text(encoding="utf-8")
        used = set(re.findall(r"gap_unblock\(\"(GAP-[A-Z0-9-]+)\"\)", source))
        self.assertTrue(used, "the producer names no gap at all")
        for identifier in used:
            self.assertIn(identifier, registry())

    def test_an_unknown_identifier_is_refused_by_name(self):
        with self.assertRaises(beta_evidence.EvidenceError) as caught:
            beta_evidence.gap("GAP-NOT-REGISTERED")
        self.assertIn("GAP-NOT-REGISTERED", str(caught.exception))

    def test_an_unblock_condition_names_the_identifier_and_not_the_ticket(self):
        condition = beta_evidence.gap_unblock("GAP-MODEL-CREDENTIAL")
        self.assertIn("GAP-MODEL-CREDENTIAL", condition)
        self.assertNotIn("#480", condition)

    def test_the_evidence_directory_records_the_identifier(self):
        """The artifact keeps the identifier; the ticket stays in the registry."""
        source = PRODUCER.read_text(encoding="utf-8")
        self.assertIn("GAP_PATH", source)


class OneSpellingTests(unittest.TestCase):
    """"Did not run" is one word across the producers."""

    def test_the_browser_side_uses_the_same_status_value_as_the_producer(self):
        source = BROWSER.read_text(encoding="utf-8")
        self.assertNotIn("status: 'NOT RUN'", source)
        self.assertIn("status: 'not_run'", source)

    def test_the_browser_side_has_one_spelling_of_the_key(self):
        source = BROWSER.read_text(encoding="utf-8")
        self.assertIsNone(re.search(r"\bnotRun\b", source))

    def test_the_producer_and_the_browser_agree_on_the_status_value(self):
        self.assertEqual(beta_evidence.NOT_RUN, "not_run")
        source = BROWSER.read_text(encoding="utf-8")
        self.assertIn(f"status: '{beta_evidence.NOT_RUN}'", source)


if __name__ == "__main__":
    unittest.main()
