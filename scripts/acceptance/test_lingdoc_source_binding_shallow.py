"""Exercise actual local shallow Git objects without network access or Go."""
import json
from pathlib import Path
import shutil
import subprocess
from tempfile import TemporaryDirectory
import unittest

from lingdoc_source_binding import capture_source_binding


@unittest.skipUnless(shutil.which("git"), "Git is required for shallow-checkout fixtures")
class TestShallowCheckoutBinding(unittest.TestCase):
    def git(self, root, *arguments):
        completed = subprocess.run(
            ["git", "-c", "user.name=SyntheticAcceptance", "-c", "user.email=acceptance@example.invalid",
             "-c", "commit.gpgsign=false", "-c", "core.autocrlf=false", "-C", str(root), *arguments],
            capture_output=True, text=True, encoding="utf-8", timeout=30, check=False,
        )
        if completed.returncode:
            raise RuntimeError("Local synthetic Git fixture failed: " + completed.stderr)
        return completed.stdout.strip()

    def exercise(self, base_changes):
        # An explicitly owned temporary directory; nothing is removed outside it.
        temporary_root = Path(__file__).resolve().parents[2] / "python-temp"
        temporary_root.mkdir(exist_ok=True)
        with TemporaryDirectory(prefix="git-shallow-", dir=temporary_root) as directory:
            fixture = Path(directory).resolve()
            self.assertEqual(fixture.parent, temporary_root.resolve())
            origin = fixture / "origin"
            origin.mkdir()
            self.git(origin, "init", "-b", "main")
            (origin / "source.txt").write_text("baseline\n", encoding="utf-8")
            self.git(origin, "add", "source.txt")
            self.git(origin, "commit", "-m", "synthetic baseline")
            self.git(origin, "checkout", "-b", "pr")
            (origin / "source.txt").write_text("PR implementation\n", encoding="utf-8")
            self.git(origin, "add", "source.txt")
            self.git(origin, "commit", "-m", "synthetic PR")
            claimed = self.git(origin, "rev-parse", "HEAD")
            self.git(origin, "checkout", "main")
            if base_changes:
                (origin / "base-only.txt").write_text("integrated baseline\n", encoding="utf-8")
                self.git(origin, "add", "base-only.txt")
                self.git(origin, "commit", "-m", "synthetic base update")
            self.git(origin, "merge", "--no-ff", "pr", "-m", "synthetic CI merge")
            actual = self.git(origin, "rev-parse", "HEAD")
            ci = {"GITHUB_EVENT_NAME": "pull_request", "GITHUB_REF": "refs/pull/122/merge", "GITHUB_SHA": actual}
            observed = []
            for depth in (1, 2):
                clone = fixture / ("checkout-depth-" + str(depth))
                self.git(fixture, "clone", "--no-local", "--depth", str(depth), "--branch", "main", str(origin), str(clone))
                self.assertEqual(self.git(clone, "rev-parse", "--is-shallow-repository"), "true")
                result = capture_source_binding(clone, claimed, environment=ci, runner_path=Path(__file__))
                observed.append({"depth": depth, "status": result["status"], "kind": result["candidate_kind"]})
                if depth == 1:
                    self.assertEqual(result["status"], "BLOCKED")
                    self.assertIn("Git identity lookup failed", result["reason"])
                else:
                    expected = "integrated_candidate" if base_changes else "head_tree_equivalent"
                    self.assertEqual((result["status"], result["candidate_kind"]), ("PASS", expected))
                    self.assertEqual(result["checkout_commit"], actual)
                    self.assertEqual(result["claimed_head"], claimed)
                    self.assertFalse(result["runner"]["inside_tested_checkout"])
                    self.assertEqual(len(result["runner"]["sha256"]), 64)
            print(json.dumps({"base_changes": base_changes, "actual_local_shallow_git": observed}))

    def test_depth_two_binds_equivalent_merge_tree(self):
        self.exercise(base_changes=False)

    def test_depth_two_labels_different_merge_tree_as_integrated_candidate(self):
        self.exercise(base_changes=True)


if __name__ == "__main__":
    unittest.main()
