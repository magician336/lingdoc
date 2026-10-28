"""Tests for the parallel surface hand-over ledger (``TRANSFERS``).

The ledger records every path that permanently moved from one face to
another.  ``_check_transfers`` in ``scripts/lingdoc_parallel_surfaces.py``
judges each record against the faces the manifest declares:

* the giving face must exist, must not be ``claimed``, and must no longer
  own the path it gave away;
* the receiving face of the **last** record for a path must own the path
  (a chain ``A -> B -> C`` ends with ``C`` owning it);
* a reserved path is never a transfer object.

These tests pin the failure directions as carefully as the success one: a
ledger check that silently passes is worse than no ledger, because it is
read as proof that the move happened.  The checks hang off
``load_and_validate`` (not ``validate_records``), so the last test also pins
that seam: synthetic face sets are not judged against the committed ledger.
"""
from __future__ import annotations

from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import lingdoc_parallel_surfaces as manifest  # noqa: E402


def _face(issue=900, name="PF-A", status="done", claimed_by="", paths=("internal/x.go",)):
    """A minimal valid face record."""
    return {
        "name": name,
        "issue": issue,
        "issues": (124,),
        "status": status,
        "claimed_by": claimed_by,
        "paths": tuple((path, "file") for path in paths),
        "resolves": (),
        "summary": "test face",
    }


def _record(path="internal/x.go", frm="PF-A", to="PF-B", basis="#1", date="2026-01-01",
            reason="test hand-over"):
    return (path, frm, to, basis, date, reason)


GIVER, TAKER = "PF-A", "PF-B"
TRANSFERRED = "internal/handed_over.go"
KEPT = "internal/kept.go"

RESERVED = tuple(manifest.PathClaim(path=p, kind=k) for p, k in manifest.RESERVED_PATHS)


def _handed_over_faces():
    """A world where the transfer already happened in the path sets."""
    return manifest.validate_records([
        _face(name=GIVER, status="done", paths=(KEPT,)),
        _face(issue=901, name=TAKER, status="done", paths=(TRANSFERRED,)),
    ])


class CommittedLedgerTests(unittest.TestCase):
    """The committed ledger must validate against the committed manifest."""

    def test_the_committed_ledger_validates(self):
        faces = manifest.load_and_validate()
        self.assertGreaterEqual(len(faces), 1)

    def test_every_committed_record_names_two_existing_faces(self):
        faces = manifest.load_and_validate()
        by_name = {face.name: face for face in faces}
        self.assertGreaterEqual(len(manifest.TRANSFERS), 1)
        for record in manifest.TRANSFERS:
            self.assertIn(record[1], by_name, record)
            self.assertIn(record[2], by_name, record)

    def test_load_and_validate_with_synthetic_records_skips_the_ledger(self):
        """The ledger vouches for the committed manifest, not for fixtures.

        ``records=`` builds a face set of the caller's own; judging it
        against the committed TRANSFERS would reject every synthetic
        fixture outright.  The seam is deliberate, so it is pinned.
        """
        faces = manifest.load_and_validate(records=[_face(name="PF-ONLY")])
        self.assertEqual(len(faces), 1)


class PerRecordCheckTests(unittest.TestCase):
    """Each record is judged on its own: the giver must really have given."""

    def test_a_completed_hand_over_passes(self):
        manifest._check_transfers(_handed_over_faces(), [_record(path=TRANSFERRED)],
                                  RESERVED)

    def test_from_face_must_exist(self):
        faces = _handed_over_faces()
        record = _record(path=TRANSFERRED, frm="PF-NOWHERE")
        with self.assertRaises(manifest.ParallelSurfaceManifestError) as caught:
            manifest._check_transfers(faces, [record], RESERVED)
        self.assertIn("from-face", str(caught.exception))

    def test_from_face_must_no_longer_own_the_path(self):
        faces = manifest.validate_records([
            _face(name=GIVER, status="done", paths=(KEPT, TRANSFERRED)),
            _face(issue=901, name=TAKER, status="done", paths=("internal/b.go",)),
        ])
        record = _record(path=TRANSFERRED)
        with self.assertRaises(manifest.ParallelSurfaceManifestError) as caught:
            manifest._check_transfers(faces, [record], RESERVED)
        self.assertIn("still owns", str(caught.exception))

    def test_a_claimed_face_may_not_give_paths_away(self):
        faces = manifest.validate_records([
            _face(name=GIVER, status="claimed", claimed_by="someone", paths=(KEPT,)),
            _face(issue=901, name=TAKER, status="done", paths=(TRANSFERRED,)),
        ])
        record = _record(path=TRANSFERRED)
        with self.assertRaises(manifest.ParallelSurfaceManifestError) as caught:
            manifest._check_transfers(faces, [record], RESERVED)
        self.assertIn("claimed", str(caught.exception))

    def test_record_must_be_a_six_tuple(self):
        faces = _handed_over_faces()
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest._check_transfers(faces, [("internal/x.go", GIVER, TAKER)],
                                      RESERVED)


class ReceiverCheckTests(unittest.TestCase):
    """The last record's receiver must own the path now."""

    def test_receiver_must_own_the_path(self):
        faces = manifest.validate_records([
            _face(name=GIVER, status="done", paths=(KEPT,)),
            _face(issue=901, name=TAKER, status="done", paths=("internal/other.go",)),
        ])
        record = _record(path=TRANSFERRED)
        with self.assertRaises(manifest.ParallelSurfaceManifestError) as caught:
            manifest._check_transfers(faces, [record], RESERVED)
        self.assertIn("does not own", str(caught.exception))

    def test_receiver_must_exist(self):
        faces = manifest.validate_records([
            _face(name=GIVER, status="done", paths=(KEPT,)),
        ])
        record = _record(path="internal/kept_elsewhere.go", to="PF-NOWHERE")
        with self.assertRaises(manifest.ParallelSurfaceManifestError) as caught:
            manifest._check_transfers(faces, [record], RESERVED)
        self.assertIn("to-face", str(caught.exception))

    def test_a_chain_is_judged_on_its_last_record(self):
        """A -> B -> C: C owns the path now; B owned it in between."""
        faces = manifest.validate_records([
            _face(name="PF-A", status="done", paths=(KEPT,)),
            _face(issue=901, name="PF-B", status="done", paths=("internal/b.go",)),
            _face(issue=902, name="PF-C", status="done", paths=(TRANSFERRED,)),
        ])
        chain = [
            _record(path=TRANSFERRED, to="PF-B"),
            _record(path=TRANSFERRED, frm="PF-B", to="PF-C"),
        ]
        manifest._check_transfers(faces, chain, RESERVED)

    def test_a_chain_whose_last_receiver_lost_the_path_fails(self):
        faces = manifest.validate_records([
            _face(name="PF-A", status="done", paths=(KEPT,)),
            _face(issue=901, name="PF-B", status="done", paths=("internal/b.go",)),
            _face(issue=902, name="PF-C", status="done", paths=("internal/c.go",)),
        ])
        chain = [
            _record(path=TRANSFERRED, to="PF-B"),
            _record(path=TRANSFERRED, frm="PF-B", to="PF-C"),
        ]
        with self.assertRaises(manifest.ParallelSurfaceManifestError):
            manifest._check_transfers(faces, chain, RESERVED)


class ReservedPathTests(unittest.TestCase):
    """A reserved path belongs to no face, so it is never a transfer object."""

    def test_a_reserved_path_cannot_be_handed_over(self):
        faces = _handed_over_faces()
        reserved_path = manifest.RESERVED_PATHS[0][0]
        record = _record(path=reserved_path)
        with self.assertRaises(manifest.ParallelSurfaceManifestError) as caught:
            manifest._check_transfers(faces, [record], RESERVED)
        self.assertIn("reserved", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
