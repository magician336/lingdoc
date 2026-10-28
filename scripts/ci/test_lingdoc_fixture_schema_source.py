"""The fixtures must not carry their own copy of the production schema.

Three shapes of "the fixture knows the schema" exist in this repository, and
each of them breaks the next time a column is added:

1. **A hand-written column list.** The fixture creates the production table
   itself and spells out its columns. The next ``ALTER TABLE`` makes every
   insert on that fixture fail with "no such column", and the HTTP layer turns
   it into an opaque ``500 internal_error`` instead of a schema error (#327).
2. **A hand-written migration subset.** The fixture applies migrations up to
   some number and stops. "It stopped at 000042" then looks like "it ran
   everything", and the fixture cannot tell which step it reached.
3. **A hand-edited version constant.** ``expectedSQLiteMigrationVersion = 53``
   is a number somebody typed; nothing says it is the number the chain
   actually reaches.

This module is the form scan for those shapes (#454). It does not migrate the
fixtures: it reports the shapes, so a new one cannot appear unnoticed. The
audit event's own column set is checked by the Go guard in
``internal/database/audit_event_columns_test.go`` — one invariant, one owner.

Issue #416 closed the audit-table half of this: every fixture that needs
``lingdoc_project_audits`` now declares the table and applies the production
migrations that shape it (``fixturemigrations.ApplySQLite``), so
``test_no_fixture_hand_writes_the_audit_table`` below is a hard rule rather than
a ratchet — that table has no known copies left, and a new one is red on
appearance. The general ratchet stays for the rest of the repository.
"""

from __future__ import annotations

import re
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
SQLITE_MIGRATIONS = REPO_ROOT / "migrations" / "sqlite"
VERSIONED_MIGRATIONS = REPO_ROOT / "migrations" / "versioned"
INTERNAL = REPO_ROOT / "internal"

CREATE_TABLE = re.compile(
    r"CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?\"?([A-Za-z0-9_]+)\"?",
    re.IGNORECASE,
)
MIGRATION_FILE = re.compile(r"(\d{6})_[A-Za-z0-9_]+\.(?:up|down)\.sql")
VERSION_CONSTANT = re.compile(
    r"const\s+expectedSQLiteMigrationVersion\s*=\s*(\d+)"
)

# The audit table has no known copies left (#416). Its fixtures declare the
# table and apply the production migrations that shape it, through
# internal/database/fixturemigrations. A Go test that spells out its columns
# again is the exact shape that broke 29 cases when PF-3 added two columns, so
# this is a hard rule, not a ratchet entry.
AUDIT_TABLE = "lingdoc_project_audits"
AUDIT_TABLE_COPY = re.compile(
    r"CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?\"?" + AUDIT_TABLE + r"\"?(?!\w)",
    re.IGNORECASE,
)


# The known offenders. This is a ratchet, not a wall: the list may only get
# shorter. Every entry is a fixture that hand-writes a production table
# without applying that table's production migration. They are the debt
# #454 exists to clear; the scan fails only when a **new** one appears.
#
# To migrate one: make the fixture apply the production migration for that
# table (the shape `ensureAuditEventTable` in
# internal/lingdoc/workspace/audit_event_table_fixture_test.go uses) and delete
# its line here.
KNOWN_SCHEMA_COPY_FIXTURES = (
    "internal/application/repository/agent_share_source_test.go",
    "internal/application/repository/knowledge_finalize_test.go",
    "internal/application/repository/knowledge_span_repo_test.go",
    "internal/application/repository/knowledge_tag_test.go",
    "internal/application/repository/model_usage_test.go",
    "internal/application/service/agent_web_pages_test.go",
    "internal/application/service/knowledge_housekeeping_test.go",
    "internal/application/service/knowledge_span_tracker_test.go",
    "internal/container/reset_pending_tasks_test.go",
    "internal/container/tenant_skill_reaper_test.go",
    "internal/im/service_lifecycle_test.go",
    "internal/lingdoc/candidateadoption/candidate_basis_test.go",
    "internal/lingdoc/candidateadoption/delivery_input_test.go",
    "internal/lingdoc/rulesetstore/http_test.go",
    "internal/lingdoc/rulesetstore/tenant_authorizer_test.go",
    "internal/lingdoc/workspace/assets_http_test.go",
    "internal/lingdoc/workspace/candidate_oneconn_test.go",
    "internal/lingdoc/workspace/change_set_delivery_test.go",
    "internal/lingdoc/workspace/change_set_http_test.go",
    "internal/lingdoc/workspace/delivery_http_test.go",
    "internal/lingdoc/workspace/delivery_route_node_test.go",
    "internal/lingdoc/workspace/full_workflow_delivery_regression_test.go",
    "internal/lingdoc/workspace/research_adapter_test.go",
    "internal/lingdoc/workspace/research_provider_http_test.go",
    "internal/lingdoc/workspace/roadmap_http_test.go",
    "internal/lingdoc/workspace/source_revocation_integration_test.go",
    "internal/lingdoc/workspacecore/agent_action_service_test.go",
    "internal/lingdoc/workspacecore/candidate_generation_evidence_test.go",
    "internal/lingdoc/workspacecore/impact_g6_extended_test.go",
)

def migration_versions(directory: Path) -> list[int]:
    """The version numbers the migration directory holds."""
    versions = []
    for entry in directory.iterdir():
        if not entry.is_file() or not entry.name.endswith(".up.sql"):
            continue
        match = MIGRATION_FILE.match(entry.name)
        if match:
            versions.append(int(match.group(1)))
    return sorted(versions)


def production_tables(directory: Path) -> set[str]:
    """The tables the migration chain creates."""
    tables = set()
    for entry in sorted(directory.iterdir()):
        if not entry.is_file() or not entry.name.endswith(".up.sql"):
            continue
        script = entry.read_text(encoding="utf-8", errors="replace")
        for statement in script.split(";"):
            match = CREATE_TABLE.search(statement)
            if match:
                tables.add(match.group(1))
    return tables


def go_test_files() -> list[Path]:
    return sorted(p for p in INTERNAL.rglob("*_test.go"))


class TestFixtureSchemaSource(unittest.TestCase):
    """The three shapes above, checked over the whole repository."""

    def test_no_fixture_hand_writes_a_production_table(self) -> None:
        """A hand-written production table must be followed by the production
        migration that brings it to the current shape.

        The one sanctioned shape is the helper that creates the base table and
        then applies the production migration for it. A fixture that only
        hand-writes the table is the shape that broke 29 cases last time.
        """
        production = production_tables(SQLITE_MIGRATIONS) | production_tables(
            VERSIONED_MIGRATIONS
        )
        offenders: list[str] = []
        for path in go_test_files():
            source = path.read_text(encoding="utf-8", errors="replace")
            hand_written = {
                match.group(1)
                for match in CREATE_TABLE.finditer(source)
                if match.group(1) in production
            }
            if not hand_written:
                continue
            if "migrations" not in source:
                offenders.append(
                    f"{path.relative_to(REPO_ROOT).as_posix()}: hand-writes {sorted(hand_written)} "
                    "without applying the production migration for it"
                )
        known = set(KNOWN_SCHEMA_COPY_FIXTURES)
        new_offenders = [line for line in offenders if line.split(":")[0] not in known]
        self.assertEqual(
            [],
            new_offenders,
            "new fixtures that carry their own copy of a production schema:\n"
            + "\n".join(new_offenders),
        )

    def test_no_fixture_hand_writes_the_audit_table(self) -> None:
        """No Go test may spell out lingdoc_project_audits' shape (#416).

        The table belongs to the production migration chain. A fixture that
        needs it declares the table and applies the migrations that shape it
        (``fixturemigrations.ApplySQLite``); the resulting shape is compared
        with the row model by ``internal/database/audit_event_columns_test.go``.
        Hand-writing the columns is what made the next migration's column break
        29 cases with an opaque 500 (#327).
        """
        offenders = [
            path.relative_to(REPO_ROOT).as_posix()
            for path in go_test_files()
            if AUDIT_TABLE_COPY.search(path.read_text(encoding="utf-8", errors="replace"))
        ]
        self.assertEqual(
            [],
            offenders,
            "fixtures that hand-write lingdoc_project_audits instead of applying its "
            "production migrations (declare the table and call "
            "fixturemigrations.ApplySQLite):\n" + "\n".join(offenders),
        )

    def test_the_known_offender_list_has_no_stale_entries(self) -> None:
        """The ratchet may only get shorter (#454, #416).

        An entry that no longer offends is stale: leaving it in the list hides
        that the debt was paid and makes the next reader migrate it twice.
        """
        production = production_tables(SQLITE_MIGRATIONS) | production_tables(
            VERSIONED_MIGRATIONS
        )
        offenders = set()
        for path in go_test_files():
            source = path.read_text(encoding="utf-8", errors="replace")
            hand_written = {
                match.group(1)
                for match in CREATE_TABLE.finditer(source)
                if match.group(1) in production
            }
            if hand_written and "migrations" not in source:
                offenders.add(path.relative_to(REPO_ROOT).as_posix())
        stale = sorted(set(KNOWN_SCHEMA_COPY_FIXTURES) - offenders)
        self.assertEqual(
            [],
            stale,
            "KNOWN_SCHEMA_COPY_FIXTURES lists fixtures that no longer offend; delete them",
        )

    def test_the_migration_version_constant_matches_the_chain(self) -> None:
        """The version constant must be the number the chain actually reaches.

        A constant somebody typed by hand is a second copy of the chain's
        state. It is allowed to exist (other faces add migrations), but it has
        to agree with the directory it names.
        """
        versions = migration_versions(SQLITE_MIGRATIONS)
        self.assertTrue(versions, "the SQLite migration directory is empty")
        newest = max(versions)
        holder = INTERNAL / "database" / "migration_sqlite_versioned_schema_test.go"
        source = holder.read_text(encoding="utf-8", errors="replace")
        match = VERSION_CONSTANT.search(source)
        self.assertIsNotNone(match, f"{holder.name} does not declare the expected version")
        self.assertEqual(
            newest,
            int(match.group(1)),
            "the expected SQLite migration version does not match the chain's newest "
            f"migration ({newest}); a migration landed and the constant was not moved, "
            "or the constant was moved past the chain",
        )

    def test_hardcoded_migration_names_exist(self) -> None:
        """A migration file named in a test must exist.

        A fixture that names a migration that was renamed or renumbered fails
        at run time with a file error, which reads like a broken fixture rather
        than a stale name.
        """
        known = {
            entry.name
            for directory in (SQLITE_MIGRATIONS, VERSIONED_MIGRATIONS)
            for entry in directory.iterdir()
            if entry.is_file()
        }
        offenders: list[str] = []
        for path in go_test_files():
            source = path.read_text(encoding="utf-8", errors="replace")
            for match in MIGRATION_FILE.finditer(source):
                name = match.group(0)
                if name not in known:
                    offenders.append(f"{path.relative_to(REPO_ROOT).as_posix()}: names {name}")
        self.assertEqual([], offenders, "tests that name a migration file that does not exist:\n" + "\n".join(offenders))


if __name__ == "__main__":
    unittest.main()
