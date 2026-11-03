"""The integration gate must rehearse the internal/types PostgreSQL dialect surface.

``internal/types`` carries ``TestPostgres*`` dialect tests (the JSON column
carrier types: jsonb decodes to []byte, text to string).  The backend closeout
PACKAGES do not include ``internal/types`` and no other CI job injects
``LINGDOC_TEST_POSTGRES_DSN``, so a removed rehearsal step would put the
dialect behavior back into a CI coverage vacuum and stay green.  This guard
keeps that from happening silently.  治理票 #756.
"""

import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = REPO_ROOT / ".github" / "workflows" / "lingdoc-interface-integration.yml"

REHEARSAL_STEP = "go test -count=1 -v -run '^TestPostgres' ./internal/types/..."


class InternalTypesDialectRehearsalTests(unittest.TestCase):
    def test_the_integration_job_rehearses_internal_types_test_postgres(self):
        # The step mirrors the internal/database rehearsal: same pattern,
        # same counting, real server required.  Dropping the step or renaming
        # the pattern out of the TestPostgres surface turns this red.
        text = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn(
            REHEARSAL_STEP,
            text,
            "the integration workflow must rehearse ./internal/types TestPostgres for real",
        )

    def test_the_rehearsal_runs_where_the_dsn_is_injected(self):
        # Without LINGDOC_TEST_POSTGRES_DSN the cases skip themselves and say
        # why -- a skip is never counted as a pass.  The rehearsal therefore
        # only counts as one inside the job that injects the DSN; if the env
        # leaves this workflow the step would degrade into a permanent skip.
        text = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn(
            "LINGDOC_TEST_POSTGRES_DSN:",
            text,
            "the integration workflow must inject LINGDOC_TEST_POSTGRES_DSN",
        )


if __name__ == "__main__":
    unittest.main()
