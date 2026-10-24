import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from lingdoc_full_server_restart import (
    compare_readings,
    minimal_environment,
    parse_postgres_dsn,
    postgres_server_environment,
    read_env_file,
    redact_dsn,
    require_project,
    server_environment,
)


class FullServerRestartHelpersTest(unittest.TestCase):
    def test_runtime_environment_does_not_forward_secrets_or_database_fixture(self):
        with patch.dict(os.environ, {
            "PATH": os.environ.get("PATH", ""),
            "DB_PASSWORD": "do-not-forward",
            "JWT_SECRET": "do-not-forward",
            "LINGDOC_TEST_POSTGRES_DSN": "do-not-forward",
            "LINGDOC_FAILURE_TEST_DSN": "do-not-forward",
        }, clear=True):
            env = minimal_environment()
        self.assertEqual(env.get("PATH"), os.environ.get("PATH"))
        for secret_name in ("DB_PASSWORD", "JWT_SECRET", "LINGDOC_TEST_POSTGRES_DSN", "LINGDOC_FAILURE_TEST_DSN"):
            self.assertNotIn(secret_name, env)

    def test_server_environment_uses_only_synthetic_sqlite_and_loopback(self):
        with patch.dict(os.environ, {
            "PATH": os.environ.get("PATH", ""),
            "DB_PASSWORD": "do-not-forward",
            "LINGDOC_FAILURE_TEST_DSN": "do-not-forward",
        }, clear=True):
            env = server_environment(Path("/tmp/isolated"), 43210, "synthetic-jwt")
        self.assertEqual(env["SERVER_HOST"], "127.0.0.1")
        self.assertEqual(env["SERVER_PORT"], "43210")
        self.assertEqual(env["DB_DRIVER"], "sqlite")
        self.assertEqual(env["RETRIEVE_DRIVER"], "sqlite")
        self.assertEqual(env["JWT_SECRET"], "synthetic-jwt")
        self.assertNotIn("DB_PASSWORD", env)
        self.assertNotIn("LINGDOC_FAILURE_TEST_DSN", env)

    def test_project_readback_rejects_mismatched_identity_or_name(self):
        require_project({"data": {"id": "project-1", "name": "synthetic"}}, "project-1", "synthetic", "restart")
        with self.assertRaisesRegex(RuntimeError, "did not match"):
            require_project({"data": {"id": "project-2", "name": "synthetic"}}, "project-1", "synthetic", "restart")


class PostgresModeTest(unittest.TestCase):
    """The protocol's persistence medium (Beta 首版真实验收协议 §1)."""

    DSN = "postgres://appuser:s3cr3t%21%40%23@db.internal:5433/lingdoc_iso"

    def test_the_dsn_is_unpacked_into_the_five_names_the_application_reads(self):
        parts = parse_postgres_dsn(self.DSN)
        self.assertEqual(parts["host"], "db.internal")
        self.assertEqual(parts["port"], "5433")
        self.assertEqual(parts["user"], "appuser")
        self.assertEqual(parts["password"], "s3cr3t!@#")
        self.assertEqual(parts["name"], "lingdoc_iso")

    def test_a_dsn_without_a_host_names_the_cause(self):
        # A password that was not percent-encoded ends the authority early:
        # the error has to say so, or the failure reads like a broken host.
        with self.assertRaisesRegex(RuntimeError, "percent-encoded"):
            parse_postgres_dsn("postgres://appuser:s3cr3t!@#@db.internal:5433/lingdoc_iso")
        with self.assertRaisesRegex(RuntimeError, "postgres://"):
            parse_postgres_dsn("mysql://appuser:s3cr3t@db.internal:5433/lingdoc_iso")

    def test_the_redacted_dsn_carries_no_password(self):
        redacted = redact_dsn(self.DSN)
        self.assertNotIn("s3cr3t", redacted)
        self.assertNotIn("%21", redacted)
        self.assertIn("db.internal:5433/lingdoc_iso", redacted)
        self.assertIn("appuser", redacted)

    def test_the_postgres_environment_names_the_database_and_never_migrates_it(self):
        env = postgres_server_environment(Path("/tmp/isolated"), 43210, "synthetic-jwt", self.DSN)
        self.assertEqual(env["SERVER_HOST"], "127.0.0.1")
        self.assertEqual(env["SERVER_PORT"], "43210")
        self.assertEqual(env["DB_DRIVER"], "postgres")
        self.assertEqual(env["RETRIEVE_DRIVER"], "postgres")
        self.assertEqual(env["DB_HOST"], "db.internal")
        self.assertEqual(env["DB_PORT"], "5433")
        self.assertEqual(env["DB_USER"], "appuser")
        self.assertEqual(env["DB_PASSWORD"], "s3cr3t!@#")
        self.assertEqual(env["DB_NAME"], "lingdoc_iso")
        self.assertEqual(env["AUTO_MIGRATE"], "false")
        self.assertEqual(env["JWT_SECRET"], "synthetic-jwt")

    def test_the_callers_explicit_environment_wins(self):
        env = postgres_server_environment(
            Path("/tmp/isolated"), 43210, "synthetic-jwt", self.DSN,
            {"REDIS_ADDR": "127.0.0.1:6379", "SYSTEM_AES_KEY": "k" * 32})
        self.assertEqual(env["REDIS_ADDR"], "127.0.0.1:6379")
        self.assertEqual(env["SYSTEM_AES_KEY"], "k" * 32)

    def test_the_env_file_is_read_as_key_value_pairs(self):
        with tempfile.TemporaryDirectory(prefix="lingdoc-env-") as temporary:
            path = Path(temporary) / "env.txt"
            path.write_text("# a comment\n\nKEY=value\nOTHER=second\n", encoding="utf-8")
            values = read_env_file(path)
        self.assertEqual(values, {"KEY": "value", "OTHER": "second"})

    def test_an_env_file_line_without_an_equals_is_refused(self):
        with tempfile.TemporaryDirectory(prefix="lingdoc-env-") as temporary:
            path = Path(temporary) / "env.txt"
            path.write_text("KEY=value\nBROKEN\n", encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "without '='"):
                read_env_file(path)


class ReadingComparisonTest(unittest.TestCase):
    def test_equal_readings_are_consistent(self):
        readings = {"project": {"id": "p"}, "chapters": [{"body_sha256": "abc"}]}
        result = compare_readings(readings, json_copy(readings))
        self.assertTrue(result["consistent"])
        self.assertEqual(result["mismatches"], [])

    def test_a_changed_leaf_is_named(self):
        before = {"project": {"id": "p", "project_version": 7}, "chapters": [{"body_sha256": "abc"}]}
        after = {"project": {"id": "p", "project_version": 8}, "chapters": [{"body_sha256": "abc"}]}
        result = compare_readings(before, after)
        self.assertFalse(result["consistent"])
        self.assertEqual([item["key"] for item in result["mismatches"]], ["project.project_version"])

    def test_a_list_that_lost_an_entry_is_named(self):
        before = {"chapters": [{"id": "a"}, {"id": "b"}]}
        after = {"chapters": [{"id": "a"}]}
        result = compare_readings(before, after)
        self.assertFalse(result["consistent"])
        self.assertEqual(result["mismatches"][0]["key"], "chapters")


def json_copy(value):
    import json
    return json.loads(json.dumps(value))


if __name__ == "__main__":
    unittest.main()
