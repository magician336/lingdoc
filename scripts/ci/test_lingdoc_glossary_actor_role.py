"""Guard tests for the glossary entry of the audit column ``actor_role`` (#560).

What this file guards
---------------------
The audit column ``actor_role`` (tables ``audit_logs`` and
``lingdoc_ruleset_use_audits``) has one meaning, ruled by the management
session of 2026-10-24 on #535 and written into the two glossaries by #560:

* ``actor_role`` = the role the operator held in that tenant at the time;
* its value set = the tenant role vocabulary (single source:
  ``internal/types/tenant_member.go``'s ``TenantRole`` constants) plus the
  two platform-side fixed values (``system_admin`` / ``platform_api_key``)
  plus the empty string, which means *unknown* (pre-2026-10-07 legacy rows
  that the 90-day retention sweep removes);
* it must not be conflated with the project role
  (``lingdoc_members.role``, which contains ``collaborator``), the
  governance role (``governance_role``) or the function roles
  (``function_roles_json``) -- those three vocabularies have never entered
  this column.

The drift this file pins (#560 acceptance): if a role is added to (or
removed from) ``TenantRole``, the glossary value set must follow.  A
glossary that still lists the old set goes red here.  Negative cases at
the bottom prove the checker itself refuses a weakened entry rather than
passing anything.
"""
from __future__ import annotations

import re
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
TENANT_MEMBER_GO = REPO_ROOT / "internal" / "types" / "tenant_member.go"
GLOSSARY_ZH = REPO_ROOT / "docs" / "06-灵档产品开发规划" / "16-术语表.md"
GLOSSARY_ROOT = REPO_ROOT / "GLOSSARY.md"

# The ruled sentences live verbatim in both glossaries.  These fragments are
# the load-bearing ones; weakening any of them must turn a test red.
SENTENCE_MEANING = "操作者在该租户当时的角色"
SENTENCE_EMPTY = "空串 ＝ 未知（2026-10-07 前的历史遗留，将由 90 天保留期自动清除）"
SENTENCE_NEVER_ENTERED = "从未进入本列"
NON_CONFLATION_COLUMNS = ("lingdoc_members.role", "governance_role", "function_roles_json", "collaborator")
PLATFORM_FIXED_VALUES = ("system_admin", "platform_api_key")
AUDIT_TABLES = ("audit_logs", "lingdoc_ruleset_use_audits")

COUNT_WORDS = {"二": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9, "十": 10}

# 租户角色词表四值（`owner` / `admin` / `contributor` / `viewer`）...
VALUE_SET_RE = re.compile(r"租户角色词表(.)值（([^）]*)）")


def tenant_role_values() -> list[str]:
    """The vocabulary, read from its single source: the TenantRole constants."""
    source = TENANT_MEMBER_GO.read_text(encoding="utf-8")
    constants = re.findall(r"TenantRole\w*\s+TenantRole\s*=\s*\"([a-z_]+)\"", source)
    if len(constants) < 2:
        raise AssertionError(
            f"{TENANT_MEMBER_GO.relative_to(REPO_ROOT)}: expected the TenantRole "
            f"constants, found {constants!r} -- the parser drifted from the Go file"
        )
    return constants


def check_glossary_text(label: str, text: str, vocabulary: list[str]) -> None:
    """Assert that ``text`` carries the ruled actor_role entry without weakening.

    Raises AssertionError naming the first missing requirement, so the guard
    fails by refusal, not by silence.  Shared by both glossaries and by the
    negative cases that feed it doctored text.
    """
    if SENTENCE_MEANING not in text:
        raise AssertionError(f"{label}: the ruled meaning sentence is missing")

    match = VALUE_SET_RE.search(text)
    if match is None:
        raise AssertionError(f"{label}: the value-set sentence (租户角色词表N值（...）) is missing")

    count_word, listed = match.group(1), match.group(2)
    if COUNT_WORDS.get(count_word) != len(vocabulary):
        raise AssertionError(
            f"{label}: count word 词表{count_word}值 does not match the {len(vocabulary)} "
            f"TenantRole constants -- update the glossary (and this map if > 10)"
        )
    listed_values = set(re.findall(r"`([a-z_]+)`", listed))
    if listed_values != set(vocabulary):
        raise AssertionError(
            f"{label}: glossary value set {sorted(listed_values)} != TenantRole "
            f"vocabulary {sorted(vocabulary)} -- the glossary drifted from the code"
        )

    for value in PLATFORM_FIXED_VALUES:
        if f"`{value}`" not in text:
            raise AssertionError(f"{label}: platform fixed value `{value}` is missing")
    if SENTENCE_EMPTY not in text:
        raise AssertionError(f"{label}: the ruled empty-string = unknown sentence is missing")
    if SENTENCE_NEVER_ENTERED not in text:
        raise AssertionError(f"{label}: the 'never entered this column' sentence was weakened or removed")
    for column in NON_CONFLATION_COLUMNS:
        if f"`{column}`" not in text:
            raise AssertionError(f"{label}: non-conflation column `{column}` is missing")
    for table in AUDIT_TABLES:
        if table not in text:
            raise AssertionError(f"{label}: audit table {table} is missing")


class TestVocabularySource(unittest.TestCase):
    def test_the_single_source_parses(self):
        values = tenant_role_values()
        self.assertEqual(len(values), len(set(values)))
        for value in values:
            self.assertRegex(value, r"^[a-z_]+$")

    def test_the_known_four_values_are_still_there(self):
        # A registry sanity anchor: today the vocabulary is exactly these four.
        # If this goes red, TenantRole changed -- update the glossaries, not the code.
        self.assertEqual(set(tenant_role_values()), {"owner", "admin", "contributor", "viewer"})


class TestGlossary16(unittest.TestCase):
    def test_the_actor_role_row_carries_the_ruled_entry(self):
        text = GLOSSARY_ZH.read_text(encoding="utf-8")
        self.assertIn("actor_role", text)
        check_glossary_text("docs/06-灵档产品开发规划/16-术语表.md", text, tenant_role_values())


class TestGlossaryRoot(unittest.TestCase):
    def test_the_actor_role_entry_carries_the_ruled_entry(self):
        text = GLOSSARY_ROOT.read_text(encoding="utf-8")
        self.assertIn("actor_role", text)
        check_glossary_text("GLOSSARY.md", text, tenant_role_values())


class TestTheCheckerRefusesWeakenedEntries(unittest.TestCase):
    """Negative verification: each doctored text must be refused by name."""

    def setUp(self):
        self.vocabulary = ["owner", "admin", "contributor", "viewer"]
        self.good = (
            "审计列 `actor_role` ＝ 操作者在该租户当时的角色。"
            "取值集合 ＝ 租户角色词表四值（`owner` / `admin` / `contributor` / `viewer`）"
            "∪ 平台面固定值（`system_admin` / `platform_api_key`）"
            "∪ 空串 ＝ 未知（2026-10-07 前的历史遗留，将由 90 天保留期自动清除）。"
            "不应与项目角色（`lingdoc_members.role`，含 `collaborator`）、"
            "治理角色（`governance_role`）、职能角色（`function_roles_json`）混同"
            " —— 那三套词从未进入本列。表：`audit_logs` 与 `lingdoc_ruleset_use_audits`。"
        )
        self.check = lambda text: check_glossary_text("doctored", text, self.vocabulary)

    def test_the_healthy_text_passes(self):
        self.check(self.good)  # must not raise

    def test_a_dropped_value_is_refused(self):
        self.assertRaises(AssertionError, self.check, self.good.replace(" / `viewer`）", "）"))

    def test_an_invented_value_is_refused(self):
        self.assertRaises(AssertionError, self.check, self.good.replace("`viewer`）", "`viewer` / `auditor`）"))

    def test_a_stale_vocabulary_is_refused(self):
        # The code moved to five roles, the glossary still lists four.
        self.assertRaises(
            AssertionError,
            lambda: check_glossary_text("doctored", self.good, self.vocabulary + ["auditor"]),
        )

    def test_a_wrong_count_word_is_refused(self):
        self.assertRaises(AssertionError, self.check, self.good.replace("词表四值", "词表五值"))

    def test_a_weakened_non_conflation_sentence_is_refused(self):
        self.assertRaises(AssertionError, self.check, self.good.replace("从未进入本列", "较少进入本列"))

    def test_a_reinterpreted_empty_string_is_refused(self):
        self.assertRaises(
            AssertionError,
            self.check,
            self.good.replace("空串 ＝ 未知（2026-10-07 前的历史遗留，将由 90 天保留期自动清除）", "空串 ＝ 无角色"),
        )

    def test_a_dropped_platform_value_is_refused(self):
        self.assertRaises(AssertionError, self.check, self.good.replace(" / `platform_api_key`", ""))


if __name__ == "__main__":
    unittest.main()
