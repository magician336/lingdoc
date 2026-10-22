"""Guard tests for the code naming registry (``scripts/lingdoc_naming_registry.py``).

The registry is the one place that says *what each code file owns*.  It
replaces the prose table "G5 = permission governance, G6 = quality runtime"
with a machine-checkable declaration, and it pins today's remaining debt with
a ratchet: the legacy list may shrink, it may not grow.

These tests are guard tests, not judgement tests.  They read the repository
as it is and say nothing about a particular event:

* the **committed registry** is valid -- unique paths, unique knowledge,
  legal tickets and batches, a baseline that matches the legacy segment;
* the **repository agrees with the registry** -- every naming defect is
  declared, every declared file still exists, and the live debt sits inside
  the ratchet;
* the **rules bite on new code** -- a synthetic tree with a ``g9_`` file, a
  ``G9`` export, a ``seg3_`` file, a ``verify_t11_test.go`` name, a task
  number inside a file name, a numbered body under a clean name, a ticket
  number in a file name or a kebab front-end name is detected, so the guard
  would reject it;
* **bad registry data is rejected loudly** -- the loader raises instead of
  degrading silently, because a silently degraded registry would let the debt
  grow unnoticed.

The registry lives in ``scripts/`` rather than ``scripts/ci/`` on purpose:
``scripts/ci/`` is the gate judgement layer, which must keep exactly one
module answering "what runs".  The test lives here because that is where the
CI runs guard tests from.
"""
from __future__ import annotations

from pathlib import Path
import shutil
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import lingdoc_naming_registry as registry  # noqa: E402

REPO_ROOT = registry.REPO_ROOT
CI_DIR = Path(__file__).resolve().parent
SCRIPTS_DIR = CI_DIR.parent
WORKFLOWS = REPO_ROOT / ".github" / "workflows"
REVIEW_WORKFLOW = WORKFLOWS / "lingdoc-review.yml"

#: The discovery pattern the review workflow already runs.  A guard test that
#: matches it needs no workflow change to enter CI.
CI_DISCOVERY = "python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'"


def _file_record(**overrides):
    """A minimal valid file record, with optional overrides."""
    record = {
        "path": "internal/lingdoc/example.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "一段能唯一指向该文件的知识说明",
    }
    record.update(overrides)
    return record


def undeclared_files(root: Path, declared: set[str]) -> list[str]:
    """The naming defects in a tree that the registry does not declare.

    This is the rule the guard applies.  An empty list means the tree agrees
    with the registry; anything in it is debt the guard rejects.
    """
    return sorted(set(registry.scan_file_defects(root)) - declared)


def undeclared_identifiers(root: Path,
                           declared: set[tuple[str, str, str]]) -> list[tuple[str, str, str]]:
    """The numbered identifiers in a tree that the registry does not declare."""
    return sorted(set(registry.scan_identifier_definitions(root)) - declared)


def _identifier_record(**overrides):
    """A minimal valid identifier record, with optional overrides."""
    record = {
        "name": "G9Example",
        "scope": "exported",
        "file": "internal/lingdoc/example.go",
        "legacy": True,
        "harness": False,
        "random_fixture": False,
    }
    record.update(overrides)
    return record


def _baseline(**overrides):
    """A minimal valid baseline, with optional overrides."""
    baseline = {
        "base_sha": "0" * 40,
        "scope": "test scope",
        "files": 1,
        "exported_identifiers": 1,
        "private_identifiers": 0,
    }
    baseline.update(overrides)
    return baseline


class RegistryShapeTests(unittest.TestCase):
    """The registry that is actually committed must be well formed."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.files, cls.identifiers, cls.ratchet = registry.load_and_validate()

    def test_the_registry_is_not_empty(self):
        self.assertGreater(len(self.files), 0)
        self.assertGreater(len(self.identifiers), 0)

    def test_the_registry_sits_in_scripts_not_in_the_judgement_layer(self):
        """``scripts/ci/`` answers "what runs"; the registry answers another
        question and must not blur that boundary."""
        self.assertTrue((SCRIPTS_DIR / "lingdoc_naming_registry.py").is_file())
        self.assertFalse((CI_DIR / "lingdoc_naming_registry.py").exists())

    def test_the_guard_test_enters_ci_without_a_workflow_change(self):
        self.assertTrue(REVIEW_WORKFLOW.is_file())
        text = REVIEW_WORKFLOW.read_text(encoding="utf-8")
        self.assertIn(CI_DISCOVERY, text)
        self.assertTrue(Path(__file__).name.startswith("test_lingdoc_"))

    def test_every_file_entry_declares_a_legal_ticket_and_batch(self):
        for entry in self.files:
            with self.subTest(path=entry.path):
                self.assertIn(entry.ticket, registry.RENAME_TICKETS)
                self.assertEqual(entry.batch, registry.BATCH_BY_TICKET[entry.ticket])
                self.assertIn(entry.batch, registry.BATCH_LABELS)

    def test_every_file_entry_breaks_at_least_one_known_rule(self):
        for entry in self.files:
            with self.subTest(path=entry.path):
                self.assertTrue(entry.defects)
                for defect in entry.defects:
                    self.assertIn(defect, registry.FILE_DEFECTS)

    def test_one_piece_of_knowledge_has_one_owner_file(self):
        """The whole point of the registry: a knowledge statement points at
        exactly one file."""
        owners: dict[str, list[str]] = {}
        for entry in self.files:
            owners.setdefault(entry.knowledge, []).append(entry.path)
        for knowledge, paths in owners.items():
            with self.subTest(knowledge=knowledge):
                self.assertEqual(len(paths), 1, f"{knowledge!r} is claimed by {paths}")

    def test_no_knowledge_statement_is_vacuous(self):
        for entry in self.files:
            with self.subTest(path=entry.path):
                self.assertGreaterEqual(len(entry.knowledge), registry.MIN_KNOWLEDGE_LENGTH)
                for vacuous in registry.VACUOUS_KNOWLEDGE:
                    self.assertNotIn(vacuous, entry.knowledge)

    def test_every_identifier_entry_declares_a_legal_scope(self):
        for entry in self.identifiers:
            with self.subTest(name=entry.name, file=entry.file):
                self.assertIn(entry.scope, registry.IDENTIFIER_SCOPES)
                self.assertTrue(registry.is_work_numbered(entry.name))

    def test_the_registry_uses_the_extended_rule_vocabulary(self):
        """A rule that no entry uses is decoration.  The extended rule set has
        to be exercised by the committed registry, otherwise the coverage
        holes it closes would be open again."""
        used = {defect for entry in self.files for defect in entry.defects}
        for defect in ("segment_number", "task_number", "numbered_identifiers"):
            with self.subTest(defect=defect):
                self.assertIn(defect, used)
                self.assertIn(defect, registry.FILE_DEFECTS)

    def test_the_baseline_records_its_sha_and_its_scope(self):
        self.assertEqual(self.ratchet.base_sha, registry.RATCHET_BASELINE["base_sha"])
        self.assertTrue(self.ratchet.scope.strip())
        self.assertGreater(len(self.ratchet.base_sha), 7)

    def test_the_baseline_matches_the_legacy_segment(self):
        legacy_files = sum(1 for entry in self.files if entry.legacy)
        legacy_exported = sum(1 for entry in self.identifiers
                              if entry.legacy and entry.scope == "exported")
        legacy_private = sum(1 for entry in self.identifiers
                             if entry.legacy and entry.scope == "private")
        self.assertEqual(legacy_files, self.ratchet.files)
        self.assertEqual(legacy_exported, self.ratchet.exported_identifiers)
        self.assertEqual(legacy_private, self.ratchet.private_identifiers)

    def test_the_acceptance_rig_vocabulary_is_declared_but_not_counted(self):
        """``G3SnapshotProject`` belongs to the acceptance rig, not to the
        product: it is declared, and it never counts towards the ratchet."""
        harness = [entry for entry in self.identifiers if entry.harness]
        self.assertTrue(harness)
        for entry in harness:
            with self.subTest(name=entry.name, file=entry.file):
                self.assertTrue(registry.is_acceptance_rig(entry.file))
                self.assertFalse(entry.legacy)


class RepositoryAgreementTests(unittest.TestCase):
    """The repository and the registry must agree."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.files, cls.identifiers, cls.ratchet = registry.load_and_validate()
        cls.declared_files = {entry.path for entry in cls.files}
        cls.declared_identifiers = {(e.name, e.scope, e.file) for e in cls.identifiers}
        cls.live_defects = registry.scan_file_defects(REPO_ROOT)
        cls.live_identifiers = set(registry.scan_identifier_definitions(REPO_ROOT))

    def test_the_repository_has_no_complaint_against_the_registry(self):
        self.assertEqual(registry.check_repository(REPO_ROOT, self.files,
                                                  self.identifiers, self.ratchet), [])

    def test_every_naming_defect_in_the_repository_is_declared(self):
        """The registry is a complete list, not an increment: a file that
        breaks a rule and is not declared is new debt the guard rejects."""
        undeclared = sorted(set(self.live_defects) - self.declared_files)
        self.assertEqual(undeclared, [], f"undeclared naming defects: {undeclared}")

    def test_every_declared_file_still_exists(self):
        for entry in self.files:
            with self.subTest(path=entry.path):
                self.assertTrue((REPO_ROOT / entry.path).is_file(),
                                f"{entry.path} is declared but does not exist")

    def test_every_declared_legacy_file_still_breaks_a_rule(self):
        """A legacy flag that no longer applies is an exemption with nothing
        behind it."""
        for entry in self.files:
            if not entry.legacy:
                continue
            with self.subTest(path=entry.path):
                self.assertIn(entry.path, self.live_defects)

    def test_the_live_debt_sits_inside_the_ratchet(self):
        legacy_files = sum(1 for path in self.live_defects
                           if path in self.declared_files)
        self.assertLessEqual(legacy_files, self.ratchet.files)
        # The acceptance rig's own vocabulary is declared but never counted.
        # The unit is the definition site: two packages may export the same
        # name, and they are two identifiers with two definitions.
        legacy_exported = sum(1 for name, scope, path in self.live_identifiers
                              if scope == "exported"
                              and not registry.is_acceptance_rig(path))
        legacy_private = sum(1 for name, scope, path in self.live_identifiers
                             if scope == "private"
                             and not registry.is_acceptance_rig(path))
        self.assertLessEqual(legacy_exported, self.ratchet.exported_identifiers)
        self.assertLessEqual(legacy_private, self.ratchet.private_identifiers)

    def test_every_numbered_identifier_in_the_repository_is_declared(self):
        undeclared = sorted(self.live_identifiers - self.declared_identifiers)
        self.assertEqual(undeclared, [], f"undeclared numbered identifiers: {undeclared}")

    def test_every_product_file_that_owns_a_numbered_identifier_is_declared(self):
        """AC-48-06: a numbered identifier needs an owner file, and an owner
        file needs a rename ticket.

        A file that is not declared has no ticket, so nothing would ever
        rename the identifiers it owns.  This is the hole a clean file name
        used to leave open.
        """
        undeclared = sorted({path for _name, _scope, path in self.live_identifiers
                             if not registry.is_acceptance_rig(path)} - self.declared_files)
        self.assertEqual(
            undeclared, [],
            f"files that own numbered identifiers but are not declared: {undeclared}")

    def test_every_declared_file_still_breaks_the_rule_it_declares(self):
        """The registry says which rules a file breaks.  The scanner says
        which rules it actually breaks; the two must not drift apart.

        Only the entries that still carry debt are judged: an entry whose
        legacy flag has been cleared keeps the rules it used to break as a
        historical record.
        """
        for entry in self.files:
            if not entry.legacy:
                continue
            with self.subTest(path=entry.path):
                live = set(self.live_defects.get(entry.path, ()))
                self.assertTrue(set(entry.defects) <= live,
                                f"{entry.path} declares {entry.defects} but only "
                                f"breaks {sorted(live)}")

    def test_every_declared_identifier_is_still_defined_where_it_is_declared(self):
        stale = sorted(self.declared_identifiers - self.live_identifiers)
        self.assertEqual(stale, [], f"declared identifiers that no longer exist: {stale}")

    def test_the_live_legacy_counts_match_the_declared_legacy_counts(self):
        legacy_files = sum(1 for entry in self.files if entry.legacy)
        self.assertEqual(legacy_files, self.ratchet.files)


class NewViolationTests(unittest.TestCase):
    """The rules must bite on code that is added tomorrow.

    Each test builds a throw-away tree and asks the scanner what it sees.  A
    rule that only fires on the historical inventory is decoration.
    """

    def setUp(self):
        registry.clear_scan_cache()
        self.root = Path(tempfile.mkdtemp(prefix="lingdoc-naming-"))

    def tearDown(self):
        shutil.rmtree(self.root, ignore_errors=True)
        registry.clear_scan_cache()

    def _write(self, relative: str, content: str = "") -> Path:
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        return path

    def _defects(self, relative: str) -> tuple[str, ...]:
        return registry.file_defects(relative)

    def test_a_new_numbered_file_is_detected(self):
        """AC-48-01: a new ``g9_`` file is new debt."""
        self._write("internal/lingdoc/g9_new.go", "package lingdoc\n")
        found = registry.scan_file_defects(self.root)
        self.assertIn("internal/lingdoc/g9_new.go", found)
        self.assertIn("numbered", found["internal/lingdoc/g9_new.go"])

    def test_a_new_numbered_directory_is_detected(self):
        """AC-48-01: the directory name counts too."""
        self._write("internal/lingdoc/g9package/thing.go", "package g9package\n")
        found = registry.scan_file_defects(self.root)
        self.assertIn("internal/lingdoc/g9package/thing.go", found)
        self.assertIn("numbered", found["internal/lingdoc/g9package/thing.go"])

    def test_a_new_task_numbered_file_is_detected(self):
        self._write("internal/lingdoc/t9_new.go", "package lingdoc\n")
        found = registry.scan_file_defects(self.root)
        self.assertIn("internal/lingdoc/t9_new.go", found)

    def test_a_new_segment_numbered_file_is_detected(self):
        """AC-48-01: ``seg3_`` is a work coordinate like ``G5``."""
        self._write("internal/lingdoc/seg3_new.go", "package lingdoc\n")
        found = registry.scan_file_defects(self.root)
        self.assertIn("internal/lingdoc/seg3_new.go", found)
        self.assertIn("segment_number", found["internal/lingdoc/seg3_new.go"])

    def test_a_new_segment_numbered_directory_is_detected(self):
        """The directory name counts too, exactly as it does for ``g9``."""
        self._write("internal/lingdoc/seg3pkg/thing.go", "package seg3pkg\n")
        found = registry.scan_file_defects(self.root)
        self.assertIn("internal/lingdoc/seg3pkg/thing.go", found)
        self.assertIn("segment_number", found["internal/lingdoc/seg3pkg/thing.go"])

    def test_a_task_number_inside_a_file_name_is_detected(self):
        """AC-48-03: ``T01``-``T16`` are the documentation layer's task pool.

        The number does not have to sit at the front of the name to be a
        defect: ``verify_t09_test.go`` used to slip through.
        """
        self._write("internal/evidence/verify_t11_test.go", "package evidence\n")
        found = registry.scan_file_defects(self.root)
        self.assertIn("internal/evidence/verify_t11_test.go", found)
        self.assertIn("task_number", found["internal/evidence/verify_t11_test.go"])

    def test_a_file_with_a_numbered_identifier_is_detected_even_when_its_name_is_clean(self):
        """AC-48-06: a clean name does not make a numbered body acceptable.

        The file owns those identifiers, so it needs an owner entry -- and an
        owner entry is what gives them a rename ticket.
        """
        self._write("internal/lingdoc/permission.go",
                    "package lingdoc\n\nfunc G9DoThing() {}\n")
        found = registry.scan_file_defects(self.root)
        self.assertIn("internal/lingdoc/permission.go", found)
        self.assertIn("numbered_identifiers", found["internal/lingdoc/permission.go"])

    def test_a_clean_named_file_without_numbered_identifiers_is_not_flagged(self):
        self._write("internal/lingdoc/permission.go",
                    "package lingdoc\n\nfunc DoThing() {}\n")
        self.assertEqual(registry.scan_file_defects(self.root), {})

    def test_a_name_that_only_looks_like_a_number_is_not_flagged(self):
        """``design``/``latest``/``test2``/``segment`` are words, not numbers.

        The number rules look for a number, not for a letter that happens to
        start one.
        """
        for name in ("design.go", "latest.go", "test2.go", "segment.go"):
            with self.subTest(name=name):
                self.assertEqual(self._defects(f"internal/lingdoc/{name}"), ())

    def test_a_segment_number_that_is_not_a_prefix_is_not_a_defect(self):
        """The Spec's rule A' is about the *prefix* ``seg<数字>_``, exactly
        like the project-number rule is about a prefix.  A segment number in
        the middle of a name is not what the Spec states."""
        self.assertEqual(self._defects("internal/lingdoc/user_seg3_profile.go"), ())

    def test_the_acceptance_rig_keeps_its_own_vocabulary(self):
        """The rig's ``T15`` words are the rig's own: they are not product debt."""
        rig = "scripts/lingdoc_mock/build_t15_matrix.py"
        self._write(rig, "T15_SCENARIO_IDS = ['a']\n")
        self.assertNotIn(rig, registry.scan_file_defects(self.root))

    def test_the_acceptance_rig_still_reports_its_grandfathered_names(self):
        """The rig's old ``g6_`` names were recognised before the exemption
        and stay reported."""
        rig = "scripts/lingdoc_mock/g6_thing.py"
        self._write(rig, "G6Thing = 1\n")
        found = registry.scan_file_defects(self.root)
        self.assertIn(rig, found)
        self.assertIn("numbered", found[rig])

    def test_a_compliant_file_is_not_flagged(self):
        self._write("internal/lingdoc/permission.go", "package lingdoc\n")
        found = registry.scan_file_defects(self.root)
        self.assertEqual(found, {})

    def test_a_new_numbered_export_is_detected(self):
        """AC-48-02: a new ``G9`` export is new debt."""
        self._write("internal/lingdoc/thing.go",
                    "package lingdoc\n\n// G9DoThing does a thing.\nfunc G9DoThing() {}\n")
        found = registry.scan_identifier_definitions(self.root)
        self.assertIn(("G9DoThing", "exported", "internal/lingdoc/thing.go"), found)

    def test_a_new_numbered_private_identifier_is_detected(self):
        self._write("internal/lingdoc/thing.go",
                    "package lingdoc\n\nfunc g9Helper() {}\n")
        found = registry.scan_identifier_definitions(self.root)
        self.assertIn(("g9Helper", "private", "internal/lingdoc/thing.go"), found)

    def test_a_new_task_numbered_export_is_detected(self):
        self._write("internal/lingdoc/thing.go", "package lingdoc\n\nfunc T9Thing() {}\n")
        found = registry.scan_identifier_definitions(self.root)
        self.assertIn(("T9Thing", "exported", "internal/lingdoc/thing.go"), found)

    def test_a_new_segment_numbered_export_is_detected(self):
        """AC-48-02: ``Seg3`` names a batch of work, not a piece of knowledge."""
        self._write("internal/lingdoc/thing.go", "package lingdoc\n\nfunc Seg3Thing() {}\n")
        found = registry.scan_identifier_definitions(self.root)
        self.assertIn(("Seg3Thing", "exported", "internal/lingdoc/thing.go"), found)

    def test_a_new_segment_numbered_private_identifier_is_detected(self):
        self._write("internal/lingdoc/thing.go", "package lingdoc\n\nfunc seg3Helper() {}\n")
        found = registry.scan_identifier_definitions(self.root)
        self.assertIn(("seg3Helper", "private", "internal/lingdoc/thing.go"), found)

    def test_a_ticket_number_in_a_file_name_is_detected(self):
        """AC-48-03: ``issue``/``spec``/``ac`` numbers do not belong in code."""
        for name, expected in (("thing_issue196.go", "ticket_number"),
                               ("thing_spec48.go", "ticket_number"),
                               ("thing_ac07.go", "ticket_number")):
            with self.subTest(name=name):
                self.assertIn(expected, self._defects(f"internal/lingdoc/{name}"))

    def test_a_kebab_frontend_file_name_is_detected(self):
        """AC-48-07: a multi-word front-end name is camelCase."""
        defects = self._defects("frontend/src/utils/agent-mode.ts")
        self.assertIn("frontend_kebab", defects)
        self.assertIn("frontend_camel", defects)

    def test_a_camel_frontend_file_name_is_accepted(self):
        self.assertEqual(self._defects("frontend/src/utils/agentMode.ts"), ())

    def test_a_pascal_component_file_name_is_accepted(self):
        self.assertEqual(self._defects("frontend/src/components/AgentMode.vue"), ())

    def test_a_composable_file_name_is_accepted(self):
        self.assertEqual(self._defects("frontend/src/composables/useAgentMode.ts"), ())

    def test_a_multi_word_component_in_camel_case_is_detected(self):
        """A component file is PascalCase; ``sendMsg.vue`` is not."""
        self.assertIn("frontend_vue_case", self._defects("frontend/src/views/chat/sendMsg.vue"))

    def test_the_acceptance_rig_vocabulary_is_not_counted_as_debt(self):
        """``G3SnapshotProject`` describes an object of the acceptance rig.  It
        is declared in the registry and never counted."""
        rig = "frontend/src/views/lingdoc/impactCenterAcceptance/runtimeSnapshot.ts"
        self._write(rig, "export interface G3SnapshotProject {\n  id: string\n}\n")
        found = registry.scan_identifier_definitions(self.root)
        self.assertIn(("G3SnapshotProject", "exported", rig), found)
        self.assertTrue(registry.is_acceptance_rig(rig))

    def test_a_random_fixture_string_is_not_an_identifier(self):
        """A random blob in test data is not a naming defect: the scan works
        on definitions, so a value never becomes an identifier."""
        self._write("internal/handler/thing_test.go",
                    "package handler\n\nconst password = \"G3n3r4t3dP4ssw0rd\"\n")
        found = registry.scan_identifier_definitions(self.root)
        self.assertNotIn("G3n3r4t3dP4ssw0rd", {name for name, _scope, _path in found})
        self.assertIn("G3n3r4t3dP4ssw0rd", registry.RANDOM_FIXTURE_TOKENS)

    def test_the_front_end_style_carve_outs_are_declared_with_a_reason(self):
        self.assertTrue(registry.FRONTEND_STYLE_EXEMPT)
        for prefix, reason in registry.FRONTEND_STYLE_EXEMPT:
            with self.subTest(prefix=prefix):
                self.assertTrue(prefix.startswith("frontend/"))
                self.assertTrue(reason.strip())

    def test_a_locale_file_is_outside_the_front_end_style_convention(self):
        """A locale file is named by its BCP-47 tag; that is not a defect."""
        self.assertEqual(self._defects("frontend/src/i18n/locales/en-US.ts"), ())

    def test_a_new_numbered_file_would_be_rejected_by_the_guard(self):
        """AC-48-06: a new file that breaks a rule and is not declared is
        exactly what the guard must refuse."""
        _files, identifiers, _ratchet = registry.load_and_validate()
        declared_files = {entry.path for entry in _files}
        declared_identifiers = {(e.name, e.scope, e.file) for e in identifiers}
        self._write("internal/lingdoc/g9_new.go", "package lingdoc\n")
        self.assertEqual(undeclared_files(self.root, declared_files),
                         ["internal/lingdoc/g9_new.go"])

    def test_a_new_numbered_export_would_be_rejected_by_the_guard(self):
        """AC-48-02: a new numbered export that is not declared is refused."""
        _files, identifiers, _ratchet = registry.load_and_validate()
        declared_identifiers = {(e.name, e.scope, e.file) for e in identifiers}
        self._write("internal/lingdoc/thing.go",
                    "package lingdoc\n\nfunc G9DoThing() {}\n")
        self.assertEqual(
            undeclared_identifiers(self.root, declared_identifiers),
            [("G9DoThing", "exported", "internal/lingdoc/thing.go")])

    def test_a_new_segment_numbered_file_would_be_rejected_by_the_guard(self):
        """AC-48-06: a new ``seg3_`` file that is not declared is refused."""
        _files, identifiers, _ratchet = registry.load_and_validate()
        declared_files = {entry.path for entry in _files}
        declared_identifiers = {(e.name, e.scope, e.file) for e in identifiers}
        self._write("internal/lingdoc/seg3_new.go", "package lingdoc\n")
        self.assertEqual(undeclared_files(self.root, declared_files),
                         ["internal/lingdoc/seg3_new.go"])
        self.assertEqual(undeclared_identifiers(self.root, declared_identifiers), [])

    def test_a_new_task_numbered_file_would_be_rejected_by_the_guard(self):
        """AC-48-03: a new ``verify_t11_test.go`` that is not declared is
        refused."""
        _files, identifiers, _ratchet = registry.load_and_validate()
        declared_files = {entry.path for entry in _files}
        self._write("internal/evidence/verify_t11_test.go", "package evidence\n")
        self.assertEqual(undeclared_files(self.root, declared_files),
                         ["internal/evidence/verify_t11_test.go"])

    def test_a_clean_named_file_with_a_numbered_body_would_be_rejected(self):
        """AC-48-06: the owner file is what gives the identifier a ticket, so
        an undeclared one is refused as well."""
        _files, identifiers, _ratchet = registry.load_and_validate()
        declared_files = {entry.path for entry in _files}
        self._write("internal/lingdoc/permission.go",
                    "package lingdoc\n\nfunc G9DoThing() {}\n")
        self.assertEqual(undeclared_files(self.root, declared_files),
                         ["internal/lingdoc/permission.go"])


class NegativeTests(unittest.TestCase):
    """A broken registry must be rejected, never accepted with a warning."""

    def test_a_duplicate_path_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record(), _file_record()],
                                       [_identifier_record()], _baseline())

    def test_two_files_claiming_the_same_knowledge_are_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records(
                [_file_record(path="internal/a.go"),
                 _file_record(path="internal/b.go")],
                [_identifier_record()], _baseline())

    def test_a_grown_legacy_segment_is_rejected(self):
        """AC-48-04: the ratchet only goes down."""
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records(
                [_file_record(path="internal/a.go"),
                 _file_record(path="internal/b.go")],
                [_identifier_record()],
                _baseline(files=1, exported_identifiers=1))

    def test_a_baseline_that_does_not_match_the_segment_is_rejected(self):
        """The baseline is lowered in the same change that clears a segment."""
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records(
                [_file_record(path="internal/a.go")],
                [_identifier_record()],
                _baseline(files=5, exported_identifiers=1))

    def test_an_illegal_ticket_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record(ticket=999)],
                                       [_identifier_record()], _baseline())

    def test_a_ticket_outside_its_batch_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record(ticket=362, batch=3)],
                                       [_identifier_record()], _baseline())

    def test_an_illegal_defect_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record(defects=("mystery",))],
                                       [_identifier_record()], _baseline())

    def test_a_file_entry_with_no_defect_is_rejected(self):
        """A file that breaks no rule has no entry: it is not debt."""
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record(defects=())],
                                       [_identifier_record()], _baseline())

    def test_an_identifier_that_carries_no_work_number_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record()],
                                       [_identifier_record(name="DoThing")], _baseline())

    def test_a_segment_numbered_identifier_is_accepted(self):
        """``seg3`` is a work coordinate like ``G5``: the registry may hold it
        so that a ticket can repay it."""
        _files, identifiers, _ratchet = registry.validate_records(
            [_file_record()], [_identifier_record(name="Seg3Thing")], _baseline())
        self.assertEqual(identifiers[0].name, "Seg3Thing")

    def test_an_illegal_identifier_scope_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record()],
                                       [_identifier_record(scope="internal")],
                                       _baseline())

    def test_an_illegal_legacy_flag_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record(legacy="yes")],
                                       [_identifier_record()], _baseline())

    def test_a_missing_field_is_rejected(self):
        record = _file_record()
        del record["knowledge"]
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([record], [_identifier_record()], _baseline())

    def test_a_missing_identifier_field_is_rejected(self):
        record = _identifier_record()
        del record["scope"]
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record()], [record], _baseline())

    def test_a_vacuous_knowledge_statement_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record(knowledge="相关代码")],
                                       [_identifier_record()], _baseline())

    def test_a_knowledge_statement_that_points_at_nothing_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record(knowledge="短")],
                                       [_identifier_record()], _baseline())

    def test_an_absolute_path_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record(path="/etc/passwd")],
                                       [_identifier_record()], _baseline())

    def test_a_path_that_escapes_the_repository_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record(path="../outside.go")],
                                       [_identifier_record()], _baseline())

    def test_harness_vocabulary_outside_an_acceptance_rig_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records(
                [_file_record()],
                [_identifier_record(harness=True, legacy=False)],
                _baseline(exported_identifiers=0))

    def test_an_unknown_random_fixture_token_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records(
                [_file_record()],
                [_identifier_record(random_fixture=True, legacy=False)],
                _baseline(exported_identifiers=0))

    def test_an_identifier_that_is_neither_legacy_nor_harness_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records(
                [_file_record()],
                [_identifier_record(legacy=False)],
                _baseline(exported_identifiers=0))

    def test_a_duplicate_identifier_entry_is_rejected(self):
        with self.assertRaises(registry.NamingRegistryError):
            registry.validate_records([_file_record()],
                                       [_identifier_record(), _identifier_record()],
                                       _baseline())

    def test_a_registry_that_forgets_a_declaration_is_rejected(self):
        """AC-48-06: the registry is a complete list.  Dropping one declaration
        leaves the tree's numbered vocabulary undeclared, and the guard says so.

        The declaration that is dropped is a numbered identifier, not a file:
        the file side of the rule ("a file that breaks a rule has to be
        declared") is exercised on a throw-away tree by ``ScannerTests``, and
        on the real tree it has nothing left to fire on once the ratchet has
        reached zero.  The identifier side still carries declarations, so the
        completeness rule is still live there.
        """
        files, identifiers, ratchet = registry.load_and_validate()
        dropped = identifiers[0].name
        trimmed = tuple(entry for entry in identifiers if entry.name != dropped)
        complaints = registry.check_repository(REPO_ROOT, files, trimmed, ratchet)
        self.assertTrue(
            any(dropped in complaint for complaint in complaints),
            f"dropping {dropped} from the registry was not reported: {complaints}")

    def test_the_entry_point_exits_non_zero_on_a_broken_registry(self):
        """Bad data must fail loudly: the registry's own command has to exit
        non-zero, never degrade silently."""
        original = registry.NAMED_FILES
        try:
            registry.NAMED_FILES = (_file_record(), _file_record())
            self.assertEqual(registry.main(), 1)
        finally:
            registry.NAMED_FILES = original

    def test_the_entry_point_exits_zero_on_the_committed_registry(self):
        self.assertEqual(registry.main(), 0)


if __name__ == "__main__":
    unittest.main()
