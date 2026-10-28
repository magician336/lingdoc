"""Tests for the parallel surface consistency detectors.

Two detectors live outside the manifest itself, and these tests pin their
seams:

* ``lingdoc_manifest_baseline`` compares the committed manifest against a
  baseline and reports the ways the manifest goes **backwards** -- a face
  that was claimed becoming available again, a live face losing paths, a path
  that ended up owned by nobody.
* ``lingdoc_ticket_ownership`` compares a face's ticket against the
  registry and reports the paths the ticket claims but the registry does not
  record.

Both detectors must fail loudly.  A detector that silently passes is worse
than no detector, because it is read as a green light.  These tests therefore
pin the failure directions as carefully as the success ones.

The detectors live in ``scripts/`` next to the manifest they read, because
they are about the manifest's meaning; this test lives in ``scripts/ci/``
because that is where CI runs guard tests from.
"""
from __future__ import annotations

from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import lingdoc_manifest_baseline as baseline  # noqa: E402
import lingdoc_parallel_surfaces as manifest  # noqa: E402
import lingdoc_ticket_ownership as ownership  # noqa: E402


def _face(issue=900, name="PF-TEST", status="available", claimed_by="",
          paths=("internal/x.go",), issues=(124,)):
    """A minimal valid face record."""
    return {
        "name": name,
        "issue": issue,
        "issues": issues,
        "status": status,
        "claimed_by": claimed_by,
        "paths": tuple((p, "file") for p in paths),
        "resolves": (),
        "summary": "test face",
    }


def _faces(*records):
    return manifest.validate_records(list(records))


class StatusRegressionTests(unittest.TestCase):
    """A claimed or finished face may not be undone silently."""

    def test_claimed_face_becoming_available_is_a_regression(self):
        before = _faces(_face(status="claimed", claimed_by="someone"))
        after = _faces(_face(status="available"))
        regressions = baseline.compare_faces(before, after)
        self.assertTrue(any(r.kind == "status-regression" for r in regressions))

    def test_done_face_becoming_claimed_is_a_regression(self):
        before = _faces(_face(status="done"))
        after = _faces(_face(status="claimed", claimed_by="someone"))
        regressions = baseline.compare_faces(before, after)
        self.assertTrue(any(r.kind == "status-regression" for r in regressions))

    def test_claiming_a_face_is_forward_progress(self):
        before = _faces(_face(status="available"))
        after = _faces(_face(status="claimed", claimed_by="someone"))
        self.assertEqual(baseline.compare_faces(before, after), ())

    def test_finishing_a_face_is_forward_progress(self):
        before = _faces(_face(status="claimed", claimed_by="someone"))
        after = _faces(_face(status="done"))
        self.assertEqual(baseline.compare_faces(before, after), ())

    def test_a_removed_face_is_a_regression(self):
        before = _faces(_face(status="claimed", claimed_by="someone"))
        after = _faces()
        regressions = baseline.compare_faces(before, after)
        self.assertTrue(any(r.kind == "face-removed" for r in regressions))


class PathRegressionTests(unittest.TestCase):
    """A live face may only gain paths; a finished face may release them."""

    def test_live_face_losing_paths_is_a_regression(self):
        before = _faces(_face(status="claimed", claimed_by="someone",
                               paths=("internal/a.go", "internal/b.go")))
        after = _faces(_face(status="claimed", claimed_by="someone",
                             paths=("internal/a.go",)))
        regressions = baseline.compare_faces(before, after)
        self.assertTrue(any(r.kind == "path-set-shrunk" for r in regressions))

    def test_done_face_releasing_paths_needs_a_receiver(self):
        """A finished face may release paths -- but somebody must take them.

        Releasing a path without handing it to another face leaves it owned
        by nobody, and the next executor may take it.  That is the state the
        union check reports.
        """
        before = _faces(_face(status="done",
                               paths=("internal/a.go", "internal/b.go")))
        after = _faces(_face(status="done", paths=("internal/a.go",)))
        regressions = baseline.compare_faces(before, after)
        self.assertTrue(any(r.kind == "path-unowned" for r in regressions))

    def test_done_face_handing_paths_to_another_face_is_allowed(self):
        before = _faces(_face(status="done",
                               paths=("internal/a.go", "internal/b.go")))
        after = _faces(
            _face(status="done", paths=("internal/a.go",)),
            _face(issue=901, name="PF-NEXT", status="claimed",
                  claimed_by="someone", paths=("internal/b.go",)),
        )
        self.assertEqual(baseline.compare_faces(before, after), ())

    def test_live_face_gaining_paths_is_allowed(self):
        before = _faces(_face(status="claimed", claimed_by="someone",
                               paths=("internal/a.go",)))
        after = _faces(_face(status="claimed", claimed_by="someone",
                             paths=("internal/a.go", "internal/b.go")))
        self.assertEqual(baseline.compare_faces(before, after), ())

    def test_a_path_handed_to_another_face_is_allowed(self):
        """The G6 panel split moved paths from a finished face to a new one."""
        before = _faces(
            _face(issue=900, name="PF-OLD", status="done",
                  paths=("frontend/a.vue",)),
        )
        after = _faces(
            _face(issue=900, name="PF-OLD", status="done",
                  paths=("frontend/keep.vue",)),
            _face(issue=901, name="PF-NEW", status="claimed",
                  claimed_by="someone", paths=("frontend/a.vue",)),
        )
        self.assertEqual(baseline.compare_faces(before, after), ())

    def test_a_path_owned_by_nobody_is_a_regression(self):
        """A path that left every face and joined no other is unowned."""
        before = _faces(
            _face(issue=900, name="PF-OLD", status="done",
                  paths=("frontend/a.vue",)),
        )
        after = _faces(
            _face(issue=900, name="PF-OLD", status="done",
                  paths=("frontend/keep.vue",)),
        )
        regressions = baseline.compare_faces(before, after)
        self.assertTrue(any(r.kind == "path-unowned" for r in regressions))

    def test_directory_claim_covers_the_files_inside_it(self):
        """A directory claim and a file list are the same ownership."""
        before = _faces(
            _face(issue=900, name="PF-DIR", status="claimed",
                  claimed_by="someone", paths=("internal/pkg/",)),
        )
        after = _faces(
            _face(issue=900, name="PF-DIR", status="claimed",
                  claimed_by="someone", paths=("internal/pkg/a.go",)),
        )
        # The path is no longer covered, so the union lost it.
        regressions = baseline.compare_faces(before, after)
        self.assertTrue(any(r.kind == "path-unowned" for r in regressions))


class ComparisonOutcomeTests(unittest.TestCase):
    """A comparison that could not be made must not look like a passed one."""

    def test_unreadable_baseline_is_not_compared(self):
        result = baseline.compare_against_git_baseline(
            root=Path("."), ref="refs/heads/does-not-exist-xyz")
        self.assertFalse(result.compared)
        self.assertFalse(result.ok)
        self.assertIn("did not happen", result.reason)

    def test_missing_tracker_makes_the_ticket_comparison_skipped(self):
        original = ownership._gh
        try:
            ownership._gh = lambda *args: (127, "")
            result = ownership.compare_ticket_to_registry(_faces(_face()))
        finally:
            ownership._gh = original
        self.assertEqual(result.compared, 0)
        self.assertTrue(result.skipped)


class TicketParsingTests(unittest.TestCase):
    """Only list items in the ownership section declare paths."""

    BODY = """## 目标

做一件事。

## 文件所有权

本面独占以下路径：

- `internal/a.go`
- `internal/b.go`（新增）

> 说明里提到 `internal/mentioned.go`，但那只是散文。

## 零阻塞理由

理由。
"""

    def test_list_items_are_extracted(self):
        self.assertEqual(
            ownership.extract_declared_paths(self.BODY),
            ("internal/a.go", "internal/b.go"),
        )

    def test_prose_paths_are_not_declarations(self):
        paths = ownership.extract_declared_paths(self.BODY)
        self.assertNotIn("internal/mentioned.go", paths)

    def test_a_body_without_the_section_yields_none(self):
        self.assertIsNone(ownership.extract_declared_paths("## 目标\n\n无路径小节。"))

    def test_the_section_ends_at_the_next_heading(self):
        body = """## 文件所有权

- `internal/a.go`

## 验收

- `internal/not-a-declaration.go`
"""
        self.assertEqual(
            ownership.extract_declared_paths(body), ("internal/a.go",))

    def test_a_shorthand_is_found_and_reported(self):
        """A brace shorthand names several files; it cannot be compared.

        It has to be reported rather than skipped: a shorthand that is
        quietly ignored is a declaration the check cannot see.
        """
        body = """## 文件所有权

- `migrations/sqlite/000052_thing.{up,down}.sql`
- `internal/a.go`
"""
        self.assertEqual(
            ownership.find_shorthand_paths(body),
            ("migrations/sqlite/000052_thing.{up,down}.sql",),
        )

    def test_a_shorthand_is_drift(self):
        original = ownership._gh
        try:
            ownership._gh = lambda *args: (0, _body_json(
                "## 文件所有权\n\n"
                "- `migrations/sqlite/000052_thing.{up,down}.sql`\n"))
            faces = _faces(_face(issue=900, status="claimed",
                                  claimed_by="someone",
                                  paths=("internal/ok.go",)))
            result = ownership.compare_ticket_to_registry(faces)
        finally:
            ownership._gh = original
        self.assertEqual(len(result.drifts), 1)
        self.assertIn("shorthand", result.drifts[0].kind)


class NaturalShapeTests(unittest.TestCase):
    """A ticket written the natural way must still be compared.

    The detector used to accept one exact shape: the heading had to be the
    bare word, the path had to follow the bullet immediately, and only one
    path per line was read.  A ticket that added a count to its heading
    (``文件所有权（路径集 9 条）``) or explained a hand-over before the paths
    dropped out of the comparison entirely -- and the skip reason said the
    ticket had *no* ownership section, which was false.  These tests pin the
    natural shapes so the check cannot go quiet that way again.
    """

    def test_a_heading_with_a_trailing_count_is_recognised(self):
        body = """## 文件所有权（路径集 9 条）

- `internal/a.go`
- `internal/b.go`
"""
        self.assertEqual(
            ownership.extract_declared_paths(body),
            ("internal/a.go", "internal/b.go"),
        )

    def test_an_english_heading_with_a_trailing_note_is_recognised(self):
        body = "## File ownership (9 files)\n\n- `internal/a.go`\n"
        self.assertEqual(ownership.extract_declared_paths(body),
                         ("internal/a.go",))

    def test_paths_after_prose_in_a_bullet_are_read(self):
        body = """## 文件所有权

- 从 **PF-3** 移交（3）：`internal/a.go`、`internal/b.go`
"""
        self.assertEqual(ownership.extract_declared_paths(body),
                         ("internal/a.go", "internal/b.go"))

    def test_every_path_on_one_line_is_read(self):
        body = """## 文件所有权

- 新增 `internal/a.go`，修改 `internal/b.go`，以及 `internal/c.go`
"""
        self.assertEqual(
            ownership.extract_declared_paths(body),
            ("internal/a.go", "internal/b.go", "internal/c.go"),
        )

    def test_a_path_with_non_ascii_segments_is_read(self):
        body = """## 文件所有权

- `docs/08-本轮实施方案/Spec-139-模板目录验收矩阵.md`
"""
        self.assertEqual(
            ownership.extract_declared_paths(body),
            ("docs/08-本轮实施方案/Spec-139-模板目录验收矩阵.md",),
        )

    def test_prose_identifiers_are_not_paths(self):
        body = """## 文件所有权

- `Request.Body` 是请求体，`SelectionReasons` 是理由字段
- `internal/a.go`
"""
        self.assertEqual(ownership.extract_declared_paths(body),
                         ("internal/a.go",))

    def test_a_short_shorthand_after_prose_is_still_reported(self):
        body = """## 文件所有权（路径集 2 条）

- 两个方向：`migrations/sqlite/000052_thing.{up,down}.sql`
"""
        self.assertEqual(
            ownership.find_shorthand_paths(body),
            ("migrations/sqlite/000052_thing.{up,down}.sql",),
        )


class UnparsedOwnershipTests(unittest.TestCase):
    """A section that yields no paths is a shape problem, not a pass.

    "The ticket could not be read", "the ticket has no ownership section" and
    "the ticket has a section but nothing in it parses" are three different
    failures with three different fixes.  Collapsing them into one sentence
    made a silent skip look like a clean comparison.
    """

    def test_a_section_without_paths_yields_an_empty_tuple(self):
        body = "## 文件所有权\n\n本面没有需要声明的路径。\n"
        self.assertEqual(ownership.extract_declared_paths(body), ())

    def test_a_section_without_paths_is_reported_as_unparsed(self):
        original = ownership._gh
        try:
            ownership._gh = lambda *args: (0, _body_json(
                "## 文件所有权\n\n本面没有需要声明的路径。\n"))
            faces = _faces(_face(issue=900, status="claimed",
                                  claimed_by="someone",
                                  paths=("internal/ok.go",)))
            result = ownership.compare_ticket_to_registry(faces)
        finally:
            ownership._gh = original
        self.assertEqual(result.drifts, ())
        self.assertEqual(result.unparsed, (900,))

    def test_an_unreadable_ticket_is_not_a_shape_problem(self):
        original = ownership._gh
        try:
            ownership._gh = lambda *args: (1, "")
            faces = _faces(_face(issue=900, status="claimed",
                                  claimed_by="someone",
                                  paths=("internal/ok.go",)))
            result = ownership.compare_ticket_to_registry(faces)
        finally:
            ownership._gh = original
        self.assertEqual(result.unparsed, ())
        self.assertTrue(any("unavailable" in reason
                            for reason in result.skipped))

    def test_an_unreadable_ticket_exits_not_compared(self):
        self.assertEqual(self._main_exit_for(lambda *args: (1, "")), 2)

    def test_a_section_without_paths_exits_unparsed(self):
        exit_code = self._main_exit_for(lambda *args: (0, _body_json(
            "## 文件所有权\n\n本面没有需要声明的路径。\n")))
        self.assertEqual(exit_code, 3)

    def test_the_unparsed_output_names_the_ticket(self):
        code, err = self._run_main(lambda *args: (0, _body_json(
            "## 文件所有权\n\n本面没有需要声明的路径。\n")))
        self.assertEqual(code, 3)
        self.assertIn("#900", err)
        self.assertIn("no readable path", err)

    def _main_exit_for(self, gh):
        return self._run_main(gh)[0]

    def _run_main(self, gh):
        original_gh = ownership._gh
        original_load = ownership.manifest.load_and_validate
        import contextlib
        import io
        err = io.StringIO()
        try:
            ownership._gh = gh
            ownership.manifest.load_and_validate = lambda: _faces(
                _face(issue=900, status="claimed", claimed_by="someone",
                      paths=("internal/ok.go",)))
            with contextlib.redirect_stderr(err):
                code = ownership.main([])
        finally:
            ownership._gh = original_gh
            ownership.manifest.load_and_validate = original_load
        return code, err.getvalue()


class TicketDriftTests(unittest.TestCase):
    """An ownership collision is drift; an unrecorded path is not."""

    def test_ticket_claiming_another_faces_path_is_drift(self):
        original = ownership._gh
        try:
            ownership._gh = lambda *args: (0, _body_json(
                "## 文件所有权\n\n- `internal/rogue.go`\n"))
            faces = _faces(
                _face(issue=900, name="PF-A", status="claimed",
                      claimed_by="someone", paths=("internal/rogue.go",)),
                _face(issue=901, name="PF-B", status="claimed",
                      claimed_by="someone", paths=("internal/other.go",)),
            )
            result = ownership.compare_ticket_to_registry(faces)
        finally:
            ownership._gh = original
        self.assertEqual(len(result.drifts), 1)
        self.assertEqual(result.drifts[0].paths, ("internal/rogue.go",))

    def test_a_path_no_face_owns_is_not_a_collision(self):
        """A file the face means to create is not a conflict."""
        original = ownership._gh
        try:
            ownership._gh = lambda *args: (0, _body_json(
                "## 文件所有权\n\n- `internal/planned.go`\n"))
            faces = _faces(_face(issue=900, status="claimed",
                                  claimed_by="someone",
                                  paths=("internal/ok.go",)))
            result = ownership.compare_ticket_to_registry(faces)
        finally:
            ownership._gh = original
        self.assertEqual(result.drifts, ())

    def test_a_finished_face_is_not_compared(self):
        """A done face's ticket is a record, not a live claim."""
        original = ownership._gh
        try:
            ownership._gh = lambda *args: (0, _body_json(
                "## 文件所有权\n\n- `internal/rogue.go`\n"))
            faces = _faces(
                _face(issue=900, name="PF-DONE", status="done",
                      paths=("internal/a.go",)),
                _face(issue=901, name="PF-LIVE", status="claimed",
                      claimed_by="someone", paths=("internal/b.go",)),
            )
            result = ownership.compare_ticket_to_registry(faces)
        finally:
            ownership._gh = original
        self.assertEqual(result.drifts, ())
        self.assertTrue(any("done" in reason for reason in result.skipped))

    def test_a_reserved_path_in_a_ticket_is_drift(self):
        original = ownership._gh
        try:
            ownership._gh = lambda *args: (0, _body_json(
                "## 文件所有权\n\n- `frontend/src/views/lingdoc/Workspace.vue`\n"))
            faces = _faces(_face(issue=900, status="claimed",
                                  claimed_by="someone",
                                  paths=("internal/ok.go",)))
            result = ownership.compare_ticket_to_registry(faces)
        finally:
            ownership._gh = original
        self.assertEqual(len(result.drifts), 1)
        self.assertIn("reserved", result.drifts[0].kind)

    def test_a_ticket_listing_fewer_paths_than_the_registry_is_not_drift(self):
        """The registry is the authority; a ticket may list less."""
        original = ownership._gh
        try:
            ownership._gh = lambda *args: (0, _body_json(
                "## 文件所有权\n\n- `internal/ok.go`\n"))
            faces = _faces(_face(issue=900, status="claimed",
                                  claimed_by="someone",
                                  paths=("internal/ok.go", "internal/more.go")))
            result = ownership.compare_ticket_to_registry(faces)
        finally:
            ownership._gh = original
        self.assertEqual(result.drifts, ())

    def test_a_directory_claim_collides_with_a_file_in_that_directory(self):
        original = ownership._gh
        try:
            ownership._gh = lambda *args: (0, _body_json(
                "## 文件所有权\n\n- `internal/pkg/a.go`\n"))
            record = _face(issue=901, name="PF-DIR", status="claimed",
                           claimed_by="someone")
            record["paths"] = (("internal/pkg/", "dir"),)
            faces = _faces(
                _face(issue=900, name="PF-A", status="claimed",
                      claimed_by="someone", paths=("internal/ok.go",)),
                record,
            )
            result = ownership.compare_ticket_to_registry(faces)
        finally:
            ownership._gh = original
        self.assertEqual(len(result.drifts), 1)
        self.assertEqual(result.drifts[0].paths, ("internal/pkg/a.go",))


def _body_json(body: str) -> str:
    import json
    return json.dumps({"number": 900, "body": body})


class CommittedManifestTests(unittest.TestCase):
    """The committed manifest must still be valid and consistent with itself."""

    def test_manifest_validates(self):
        faces = manifest.load_and_validate()
        self.assertGreater(len(faces), 0)

    def test_no_two_faces_own_the_same_path(self):
        faces = manifest.load_and_validate()
        for index, face in enumerate(faces):
            for other in faces[index + 1:]:
                for claim in face.paths:
                    for other_claim in other.paths:
                        self.assertFalse(
                            claim.overlaps(other_claim),
                            f"{claim.path!r} is claimed by both "
                            f"{face.name!r} and {other.name!r}",
                        )

    def test_the_committed_manifest_is_not_a_regression_from_itself(self):
        faces = manifest.load_and_validate()
        self.assertEqual(baseline.compare_faces(faces, faces), ())

    def test_the_committed_manifest_does_not_regress_from_main(self):
        """The committed manifest may not undo what ``main`` already records.

        This is the guard that catches a pull request carrying a stale copy
        of the manifest: the copy is internally valid, so the overlap check
        cannot see it, but merging it would hand a claimed face back to the
        pool and strip paths from the faces that own them.

        The comparison needs git and the network, so it is skipped when
        either is unavailable -- and a skipped comparison is reported as
        skipped, never as a pass.
        """
        result = baseline.compare_against_git_baseline(
            root=manifest.REPO_ROOT, ref="origin/main")
        if not result.compared:
            self.skipTest(result.reason)
        self.assertEqual(
            result.regressions, (),
            "the committed manifest regresses from origin/main: "
            + "; ".join(str(r) for r in result.regressions),
        )

    def test_a_branch_that_did_not_touch_the_manifest_is_not_compared(self):
        """A stale branch is not a regression; git keeps the base's copy.

        A branch that forked before the base grew a face has an older
        manifest simply because it has not merged the base yet.  Flagging
        that would make the check cry wolf on every slightly stale branch.
        """
        original = baseline.git_baseline_source
        original_merge_base = baseline._merge_base
        sources = {}

        def fake_source(root, ref, path):
            sources[ref] = ref
            if ref == "HEAD":
                return "CURRENT"
            if ref == "merge-base-sha":
                return "CURRENT"  # the branch did not change the file
            return "BASELINE"

        def fake_merge_base(root, ref):
            return "merge-base-sha"

        try:
            baseline.git_baseline_source = fake_source
            baseline._merge_base = fake_merge_base
            result = baseline.compare_against_git_baseline(
                root=Path("."), ref="origin/main")
        finally:
            baseline.git_baseline_source = original
            baseline._merge_base = original_merge_base
        self.assertTrue(result.compared)
        self.assertEqual(result.regressions, ())
        self.assertIn("did not change", result.reason)

    def test_a_branch_that_did_touch_the_manifest_is_compared(self):
        """A branch that changed the manifest is compared against the base."""
        original = baseline.git_baseline_source
        original_merge_base = baseline._merge_base

        # The real committed manifest as the baseline, with one face's path
        # set replaced by a path the current manifest does not have.  That is
        # the regression the check must find.
        committed = (manifest.REPO_ROOT / "scripts/lingdoc_parallel_surfaces.py")
        baseline_source = committed.read_text(encoding="utf-8")
        baseline_source = baseline_source.replace(
            '("internal/lingdoc/workspacecore/runtime_control.go", "file"),',
            '("internal/base_only_path.go", "file"),',
        )

        def fake_source(root, ref, path):
            if ref == "HEAD":
                return "CHANGED-BY-THE-BRANCH"
            if ref == "merge-base-sha":
                return "ORIGINAL"
            return baseline_source

        def fake_merge_base(root, ref):
            return "merge-base-sha"

        def fake_files_at_ref(root, ref):
            # The baseline path is a real file at the baseline, so withdrawing
            # it is a genuine loss and must still be reported.  The exemption
            # for claims that never had a file behind them must not swallow it.
            return {"internal/base_only_path.go"}

        original_files_at_ref = baseline._files_at_ref
        try:
            baseline.git_baseline_source = fake_source
            baseline._merge_base = fake_merge_base
            baseline._files_at_ref = fake_files_at_ref
            result = baseline.compare_against_git_baseline(
                root=Path("."), ref="origin/main")
        finally:
            baseline.git_baseline_source = original
            baseline._merge_base = original_merge_base
            baseline._files_at_ref = original_files_at_ref
        self.assertTrue(result.compared, result.reason)
        self.assertIn("compared against", result.reason)
        # The baseline's extra path is gone from the current manifest.
        self.assertTrue(result.regressions)
        self.assertTrue(any("base_only_path" in str(r) for r in result.regressions))


class UnbackedClaimTests(unittest.TestCase):
    """A claim over a file the baseline never had is not a path loss.

    A face may name a file it has not created yet.  Withdrawing such a claim
    later takes nothing away -- nobody owned a real file -- so the comparison
    must not read it as a regression.  The exemption is explicit: it is
    passed in, never guessed, and it never covers a file that really exists.
    """

    def test_a_baseline_claim_without_a_file_behind_it_is_not_a_path_loss(self):
        before = _faces(_face(status="claimed", claimed_by="someone",
                               paths=("internal/never_existed.go",)))
        after = _faces(_face(status="claimed", claimed_by="someone",
                             paths=("internal/kept.go",)))
        regressions = baseline.compare_faces(
            before, after, frozenset({"internal/never_existed.go"}))
        self.assertEqual(regressions, ())

    def test_the_same_loss_is_reported_when_no_exemption_is_given(self):
        """The exemption is passed in, not assumed: without it, the loss stands."""
        before = _faces(_face(status="claimed", claimed_by="someone",
                               paths=("internal/never_existed.go",)))
        after = _faces(_face(status="claimed", claimed_by="someone",
                             paths=("internal/kept.go",)))
        regressions = baseline.compare_faces(before, after)
        self.assertTrue(any(r.kind == "path-set-shrunk" for r in regressions))
        self.assertTrue(any(r.kind == "path-unowned" for r in regressions))

    def test_a_file_that_exists_at_the_baseline_is_never_unbacked(self):
        faces = _faces(_face(status="claimed", claimed_by="someone",
                             paths=("internal/real.go",)))
        tracked = {"internal/real.go", "internal/other.go"}
        self.assertEqual(baseline.unbacked_claims(faces, tracked), frozenset())

    def test_a_directory_claim_is_never_treated_as_unbacked(self):
        """Git does not track an empty directory, so a directory claim stays backed."""
        face = {
            "name": "PF-DIR",
            "issue": 901,
            "issues": (124,),
            "status": "claimed",
            "claimed_by": "someone",
            "paths": (("internal/emptydir", "dir"),),
            "resolves": (),
            "summary": "test face",
        }
        faces = manifest.validate_records([face])
        self.assertEqual(baseline.unbacked_claims(faces, set()), frozenset())


if __name__ == "__main__":
    unittest.main()
