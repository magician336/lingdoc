import os
from pathlib import Path
import unittest
from unittest.mock import patch

from lingdoc_full_server_restart import minimal_environment, require_project, server_environment


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


if __name__ == "__main__":
    unittest.main()

