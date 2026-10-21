"""Tests for the parallel work face manifest (``scripts/lingdoc_parallel_surfaces.py``).

The manifest is the one place that says *which paths belong to which parallel
work face*.  It is declarative data: it answers ownership questions and
nothing else.  These tests pin the seams that matter for a maintainer:

* ``load_and_validate()`` loads the committed manifest offline and rejects a
  manifest with a duplicate face name, an illegal status, an empty path set,
  an unknown issue, or a face that claims a reserved integration path.
* **no two faces own the same path** -- this is the invariant that stops two
  executors from silently overwriting each other's work.
* the overlap detector itself is exercised on file/dir combinations, so a
  face that claims a whole directory cannot hide behind an exact path.

The manifest lives in ``scripts/`` rather than ``scripts/ci/`` on purpose:
``scripts/ci/`` is the gate judgement layer, which must keep exactly one
module answering "what runs".  The test lives here because that is where the
CI runs guard tests from.

Bad manifest data must fail loudly: a silently degraded manifest would let two
faces edit the same file, and the collision would only surface at merge time.
"""
from __future__ import annotations

from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import lingdoc_parallel_surfaces as manifest  # noqa: E402


def _record(**overrides):
    """A minimal valid face record, with optional overrides."""
    record = {
        "name": "PF-TEST",
        "issue": 999,
        "issues": (124,),
        "status": "available",
        "claimed_by": "",
        "paths": (("internal/lingdoc/example.go", "file"),),
        "resolves": (),
        "summary": "test face",
    }
    record.update(overrides)
    return record


class PathOverlapTests(unittest.TestCase):
    """The overlap detector, exercised directly."""

    def test_same_file_overlaps(self):
        a = manifest.PathClaim("internal/x.go", "file")
        b = manifest.PathClaim("internal/x.go", "file")
        self.assertTrue(a.overlaps(b))

    def test_different_files_do_not_overlap(self):
        a = manifest.PathClaim("internal/x.go", "file")
        b = manifest.PathClaim("internal/y.go", "file")
        self.assertFalse(a.overlaps(b))

    def test_file_inside_directory_overlaps(self):
        a = manifest.PathClaim("internal/x.go", "file")
        b = manifest.PathClaim("internal", "dir")
        self.assertTrue(a.overlaps(b))
        self.assertTrue(b.overlaps(a))

    def test_file_outside_directory_does_not_overlap(self):
        a = manifest.PathClaim("frontend/x.go", "file")
        b = manifest.PathClaim("internal", "dir")
        self.assertFalse(a.overlaps(b))

    def test_nested_directories_overlap(self):
        a = manifest.PathClaim("frontend/src/api", "dir")
        b = manifest.PathClaim("frontend/src/api/lingdoc", "dir")
        self.assertTrue(a.overlaps(b))

    def test_prefix_is_not_a_directory(self):
        """``internal/lingdoc`` must not swallow ``internal/lingdoccore``."""
        a = manifest.PathClaim("internal/lingdoccore/x.go", "file")
        b = manifest.PathClaim("internal/lingdoc", "dir")
        self.assertFalse(a.overlaps(b))


class CommittedManifestTests(unittest.TestCase):
    """The manifest that is actually committed must be valid."""

    def setUp(self):
        self.faces = manifest.load_and_validate()

    def test_manifest_is_not_empty(self):
        self.assertGreater(len(self.faces), 0)

    def test_face_names_are_unique(self):
        names = [face.name for face in self.faces]
        self.assertEqual(len(names), len(set(names)))

    def test_face_issue_numbers_are_unique(self):
        """Two faces must not share a tracker ticket."""
        numbers = [face.issue for face in self.faces]
        self.assertEqual(len(numbers), len(set(numbers)))

    def test_face_issue_numbers_are_positive(self):
        for face in self.faces:
            self.assertGreater(face.issue, 0)

    def test_face_issue_is_not_a_spec_issue(self):
        """A face's own ticket must not be one of the G-line Spec issues.

        The face ticket is a *new* issue; the Spec issues are the ones the face
        delivers.  If a face ticket number ever equals a Spec issue number,
        one of the two fields has been confused with the other.
        """
        for face in self.faces:
            self.assertNotIn(
                face.issue, manifest.G_LINE_ISSUES,
                f"{face.name!r} claims ticket #{face.issue}, which is a Spec issue",
            )

    def test_statuses_are_legal(self):
        for face in self.faces:
            self.assertIn(face.status, manifest.FACE_STATUSES)

    def test_path_ownership_is_disjoint(self):
        """The core invariant: no two faces may own the same path."""
        for index, face in enumerate(self.faces):
            for other in self.faces[index + 1:]:
                for claim in face.paths:
                    for other_claim in other.paths:
                        self.assertFalse(
                            claim.overlaps(other_claim),
                            f"{claim.path!r} is claimed by both "
                            f"{face.name!r} and {other.name!r}",
                        )

    def test_reserved_paths_are_not_owned(self):
        reserved = [manifest.PathClaim(p, k) for p, k in manifest.RESERVED_PATHS]
        for face in self.faces:
            for claim in face.paths:
                for reserved_claim in reserved:
                    self.assertFalse(
                        claim.overlaps(reserved_claim),
                        f"face {face.name!r} claims reserved path {claim.path!r}",
                    )

    def test_every_face_declares_known_g_line_issues(self):
        for face in self.faces:
            self.assertGreater(len(face.issues), 0)
            for issue in face.issues:
                self.assertIn(issue, manifest.G_LINE_ISSUES)

    def test_every_face_declares_paths_and_summary(self):
        for face in self.faces:
            self.assertGreater(len(face.paths), 0)
            self.assertTrue(face.summary.strip())

    def test_claimed_faces_name_their_claimant(self):
        for face in self.faces:
            if face.status == "claimed":
                self.assertTrue(face.claimed_by.strip())
            else:
                self.assertFalse(face.claimed_by.strip())

    def test_path_diagram_document_exists(self):
        self.assertTrue(
            (manifest.REPO_ROOT / manifest.PATH_DIAGRAM_DOC).is_file(),
            f"the path diagram {manifest.PATH_DIAGRAM_DOC} must exist",
        )

    def test_declared_file_paths_are_repo_relative(self):
        for face in self.faces:
            for claim in face.paths:
                self.assertFalse(claim.path.startswith("/"), claim.path)
                self.assertNotIn("\\", claim.path, claim.path)
                self.assertNotIn("..", claim.path.split("/"), claim.path)


class NegativeTests(unittest.TestCase):
    """A broken manifest must be rejected, never accepted with a warning."""

    def test_duplicate_face_name_is_rejected(self):
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest.validate_records([_record(), _record()])

    def test_overlapping_path_is_rejected(self):
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest.validate_records([
                _record(name="PF-A", paths=(("internal/x.go", "file"),)),
                _record(name="PF-B", paths=(("internal/x.go", "file"),)),
            ])

    def test_directory_claim_colliding_with_a_file_is_rejected(self):
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest.validate_records([
                _record(name="PF-A", paths=(("internal/x.go", "file"),)),
                _record(name="PF-B", paths=(("internal", "dir"),)),
            ])

    def test_reserved_path_claim_is_rejected(self):
        reserved_path, reserved_kind = manifest.RESERVED_PATHS[0]
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest.validate_records([
                _record(name="PF-A", paths=((reserved_path, reserved_kind),)),
            ])

    def test_illegal_status_is_rejected(self):
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest.validate_records([_record(status="in-progress")])

    def test_claimed_face_without_claimant_is_rejected(self):
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest.validate_records([_record(status="claimed", claimed_by="")])

    def test_unclaimed_face_with_claimant_is_rejected(self):
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest.validate_records([_record(status="available", claimed_by="someone")])

    def test_unknown_issue_is_rejected(self):
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest.validate_records([_record(issues=(999,))])

    def test_non_positive_face_issue_is_rejected(self):
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest.validate_records([_record(issue=0)])

    def test_missing_face_issue_is_rejected(self):
        record = _record()
        del record["issue"]
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest.validate_records([record])

    def test_empty_path_set_is_rejected(self):
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest.validate_records([_record(paths=())])

    def test_illegal_path_kind_is_rejected(self):
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest.validate_records([_record(paths=(("internal/x.go", "glob"),))])


if __name__ == "__main__":
    unittest.main()
