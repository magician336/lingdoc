"""Guard tests for the workspace test-name partition (ADR 0016, #736).

The gate splits the workspace family's tests across shard jobs by test
name: the decision module enumerates the package's top-level test names
and turns a deterministic hash slice of them into each shard's ``-run``
filter.  The split is only sound while two properties hold, and both are
pinned here against the **real repository**:

* the enumeration is the run set: ``go test`` runs exactly the top-level
  ``func Test...`` declarations of the family's ``*_test.go`` files, so
  the family must stay free of the constructs that would break that
  equation (build tags, ``TestMain``, testify suites, fuzz/example
  targets) -- any of them appearing turns these guards red and forces a
  re-engagement with ADR 0016, instead of silently moving tests out of
  every shard's filter;
* the published filters are a partition: every enumerated name matches
  exactly one filter, every filter matches at least one name, and the
  module's own enumeration agrees with the independent source parse
  below -- a name matching zero filters would never run, a name
  matching two would run twice.

The parsing here is deliberately a second implementation (line-based,
not the module's multiline regex), so a defect in either parser shows up
as a disagreement instead of cancelling out.
"""
from __future__ import annotations

import re
import unittest
from pathlib import Path

import lingdoc_gate_decision as decision

REPO_ROOT = Path(__file__).resolve().parents[2]
FAMILY_DIR = (REPO_ROOT / decision.LINGDOC_MAIN_ROOT
              / decision.WORKSPACE_FAMILY)

# The constructs that would make a source-level enumeration differ from
# what `go test` actually runs.  The family has none of them today; the
# point of the guard is that nobody adds one without meeting ADR 0015.
BUILD_TAG = re.compile(r"^//go:build\b", re.M)
SUITE_IMPORT = re.compile(r'"github\.com/stretchr/testify/suite"')
OTHER_TARGET = re.compile(r"^func\s+(?:Fuzz|Example|Benchmark)\w+\s*\(", re.M)
TEST_MAIN = re.compile(r"^func\s+TestMain\s*\(", re.M)
# A `func TestX` in a non-test file is not collected by `go test`, but it
# is exactly the kind of confusion this split must not grow.
TEST_FUNC = re.compile(r"^func\s+(Test\w+)\s*\(", re.M)


def _test_sources() -> list[tuple[Path, str]]:
    return [(path, path.read_text(encoding="utf-8", errors="replace"))
            for path in sorted(FAMILY_DIR.glob("*_test.go"))]


def _enumerated_names_independently() -> set[str]:
    """The top-level test names, parsed line by line (second parser)."""
    names: set[str] = set()
    for _path, source in _test_sources():
        for line in source.splitlines():
            stripped = line.rstrip()
            match = TEST_FUNC.match(stripped)
            if match and not line.startswith((" ", "\t")):
                names.add(match.group(1))
    return names


class WorkspacePartitionTests(unittest.TestCase):
    """The published filters partition the family's real test names."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.names = _enumerated_names_independently()
        cls.filters = decision._SHARD_TEST_FILTERS

    def test_the_family_is_what_the_split_assumes(self):
        """The enumeration == run set equation holds (ADR 0015)."""
        self.assertTrue(FAMILY_DIR.is_dir(),
                        "the workspace family disappeared from the tree")
        sources = _test_sources()
        self.assertGreater(len(sources), 100,
                           "the family's test files vanished; the split is "
                           "calibrated on a real, large package")
        for path, source in sources:
            for label, pattern in (("build tag", BUILD_TAG),
                                   ("testify suite", SUITE_IMPORT),
                                   ("fuzz/example/benchmark target",
                                    OTHER_TARGET),
                                   ("TestMain", TEST_MAIN)):
                self.assertIsNone(pattern.search(source),
                                  f"{path.name} carries a {label}; a source "
                                  f"enumeration would drift from what go "
                                  f"test runs -- re-engage ADR 0016")
        for path in sorted(FAMILY_DIR.glob("*.go")):
            if path.name.endswith("_test.go"):
                continue
            source = path.read_text(encoding="utf-8", errors="replace")
            self.assertIsNone(TEST_MAIN.search(source),
                              f"{path.name} declares TestMain")
            self.assertIsNone(TEST_FUNC.search(source),
                              f"{path.name} declares a test function in a "
                              f"non-test file")

    def test_the_family_directory_has_no_subpackages(self):
        """A Go file in a subdirectory would join `.../workspace/...` runs
        while matching none of the top-level `-run` filters -- it would sit
        in zero shards, silently, and every promise above would still look
        green.  The family stays flat (ADR 0016); a subpackage is a new
        partition decision, not a quiet addition."""
        escaped = sorted(path.relative_to(FAMILY_DIR).as_posix()
                         for path in FAMILY_DIR.rglob("*.go")
                         if path.parent != FAMILY_DIR)
        self.assertEqual(escaped, [],
                         "subdirectory Go files would escape every shard: "
                         f"{escaped} -- re-adjudicate ADR 0016 first")

    def test_the_module_sees_the_same_names_as_the_source(self):
        """Two independent parsers must agree on the name set."""
        module_names = set(decision._workspace_test_names(
            decision.WORKSPACE_FAMILY))
        self.assertEqual(module_names, self.names,
                         "the module's enumeration and the independent "
                         "source parse disagree about the family's tests")

    def test_every_name_matches_exactly_one_filter(self):
        """A name in zero filters never runs; in two filters runs twice."""
        self.assertEqual(sorted(self.filters),
                         sorted(decision.WORKSPACE_SHARD_NAMES),
                         "the filter table must carry every workspace slice")
        unmatched: list[str] = []
        doubled: list[str] = []
        for name in sorted(self.names):
            hits = [shard for shard, pattern in self.filters.items()
                    if re.search(pattern, name)]
            if len(hits) == 0:
                unmatched.append(name)
            elif len(hits) > 1:
                doubled.append((name, hits))
        self.assertEqual(unmatched, [],
                         "these tests would silently never run")
        self.assertEqual(doubled, [],
                         "these tests would run in more than one shard")

    def test_every_filter_is_anchored_and_non_empty(self):
        """An unanchored pattern matches by substring, breaking the split."""
        for shard, pattern in self.filters.items():
            self.assertTrue(pattern.startswith("^(")
                            and pattern.endswith(")$"),
                            f"{shard}'s filter is not anchored: {pattern[:40]}"
                            f"...; the partition needs exact name matching")
            matched = [name for name in self.names
                       if re.search(pattern, name)]
            self.assertTrue(
                matched,
                f"{shard}'s filter matches nothing; its shard job would "
                f"fail the not-empty assertion as a false red")

    def test_the_published_plan_carries_the_filters(self):
        """The plan's workspace slices are the filter table's shards."""
        workspace_shards = [name for name, families
                            in decision.LINGDOC_FAMILY_SHARDS
                            if decision.WORKSPACE_FAMILY in families]
        self.assertEqual(sorted(workspace_shards),
                         sorted(self.filters),
                         "the effective plan and the filter table disagree "
                         "about which shards carry a slice")
        # A filter applies to the shard's whole command: a shard carrying a
        # slice must not own any other family, or that family would run
        # filtered without anyone deciding what its partition is.
        for name, families in decision.LINGDOC_FAMILY_SHARDS:
            if name in self.filters:
                self.assertEqual(families, (decision.WORKSPACE_FAMILY,),
                                 f"{name} carries a test filter but owns "
                                 f"families {families}; a filter restricts "
                                 f"the whole shard command")

    def test_the_partition_is_deterministic_across_calls(self):
        """The same tree yields the same slices, call after call."""
        self.assertEqual(decision._workspace_shard_filters(),
                         decision._workspace_shard_filters())


if __name__ == "__main__":
    unittest.main()
