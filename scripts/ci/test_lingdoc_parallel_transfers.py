"""Tests for the path-handover records in the parallel surface manifest.

``scripts/lingdoc_parallel_surfaces.py`` records every path one face handed to
another and checks the record against the two path sets.  These tests pin the
failure directions, because a check that silently passes is worse than no
check: it is read as a green light.

A detector that cannot fail proves nothing, so each test here injects one
violation and asserts that the loader rejects it.
"""
from __future__ import annotations

from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import lingdoc_parallel_surfaces as manifest  # noqa: E402


def _face(name="PF-TEST", issue=900, status="done", claimed_by="", paths=()):
    """A minimal valid face record."""
    return {
        "name": name,
        "issue": issue,
        "issues": (124,),
        "status": status,
        "claimed_by": claimed_by,
        "paths": tuple((p, "file") for p in paths),
        "resolves": (),
        "summary": "test face",
    }


def _transfer(path="internal/a.go", frm="PF-OLD", to="PF-NEW", ticket=412):
    return {"path": path, "from": frm, "to": to, "ticket": ticket, "date": "2026-10-23"}


class TransferRecordTests(unittest.TestCase):
    """The record of a handover has to hold against the two path sets."""

    def _faces(self, *records):
        return manifest.validate_records(list(records))

    def test_a_handover_that_holds_is_accepted(self):
        faces = self._faces(
            _face(name="PF-OLD", issue=901, paths=("internal/old.go",)),
            _face(name="PF-NEW", issue=902, paths=("internal/a.go",)),
        )
        manifest._check_transfers(faces, [_transfer()])

    def test_a_handover_whose_giver_still_claims_the_path_is_rejected(self):
        faces = self._faces(
            _face(name="PF-OLD", issue=901, paths=("internal/a.go",)),
            _face(name="PF-NEW", issue=902, paths=("internal/new.go",)),
        )
        with self.assertRaises(manifest.ParallelSurfaceManifestError) as caught:
            manifest._check_transfers(faces, [_transfer()])
        self.assertIn("still claims", str(caught.exception))

    def test_a_handover_whose_receiver_does_not_own_the_path_is_rejected(self):
        faces = self._faces(
            _face(name="PF-OLD", issue=901, paths=("internal/old.go",)),
            _face(name="PF-NEW", issue=902, paths=("internal/new.go",)),
        )
        with self.assertRaises(manifest.ParallelSurfaceManifestError) as caught:
            manifest._check_transfers(faces, [_transfer()])
        self.assertIn("does not own", str(caught.exception))

    def test_a_face_that_is_being_worked_on_may_not_hand_a_path_over(self):
        faces = self._faces(
            _face(name="PF-OLD", issue=901, status="claimed", claimed_by="someone",
                  paths=("internal/old.go",)),
            _face(name="PF-NEW", issue=902, paths=("internal/a.go",)),
        )
        with self.assertRaises(manifest.ParallelSurfaceManifestError) as caught:
            manifest._check_transfers(faces, [_transfer()])
        self.assertIn("being worked on", str(caught.exception))

    def test_a_handover_naming_an_unknown_face_is_rejected(self):
        faces = self._faces(_face(name="PF-OLD", issue=901, paths=("internal/old.go",)))
        with self.assertRaises(manifest.ParallelSurfaceManifestError) as caught:
            manifest._check_transfers(faces, [_transfer(to="PF-MISSING")])
        self.assertIn("is not a face", str(caught.exception))

    def test_a_handover_missing_a_field_is_rejected(self):
        faces = self._faces(
            _face(name="PF-OLD", issue=901, paths=("internal/old.go",)),
            _face(name="PF-NEW", issue=902, paths=("internal/a.go",)),
        )
        broken = {"path": "internal/a.go", "from": "PF-OLD"}
        with self.assertRaises(manifest.ParallelSurfaceManifestError) as caught:
            manifest._check_transfers(faces, [broken])
        self.assertIn("missing field", str(caught.exception))

    def test_only_the_last_handover_of_a_path_has_to_hold(self):
        """A path may be borrowed and given back; the trail is the record."""
        faces = self._faces(
            _face(name="PF-OLD", issue=901, paths=("internal/old.go",)),
            _face(name="PF-MID", issue=903, paths=("internal/mid.go",)),
            _face(name="PF-NEW", issue=902, paths=("internal/a.go",)),
        )
        manifest._check_transfers(faces, [
            _transfer(frm="PF-OLD", to="PF-MID"),
            _transfer(frm="PF-MID", to="PF-NEW"),
        ])

    def test_the_last_handover_of_a_path_must_hold(self):
        faces = self._faces(
            _face(name="PF-OLD", issue=901, paths=("internal/old.go",)),
            _face(name="PF-MID", issue=903, paths=("internal/mid.go",)),
            _face(name="PF-NEW", issue=902, paths=("internal/new.go",)),
        )
        with self.assertRaises(manifest.ParallelSurfaceManifestError) as caught:
            manifest._check_transfers(faces, [
                _transfer(frm="PF-OLD", to="PF-MID"),
                _transfer(frm="PF-MID", to="PF-NEW"),
            ])
        self.assertIn("does not own", str(caught.exception))


class CommittedTransferTests(unittest.TestCase):
    """The committed records have to hold against the committed faces."""

    def test_the_committed_manifest_loads(self):
        faces = manifest.load_and_validate()
        self.assertTrue(faces)

    def test_every_committed_handover_names_faces_and_a_path(self):
        for index, record in enumerate(manifest.TRANSFERS):
            self.assertIn("path", record, f"TRANSFERS[{index}]")
            self.assertIn("from", record, f"TRANSFERS[{index}]")
            self.assertIn("to", record, f"TRANSFERS[{index}]")

    def test_a_committed_handover_is_never_from_a_face_being_worked_on(self):
        by_name = {face.name: face for face in manifest.load_and_validate()}
        for record in manifest.TRANSFERS:
            giver = by_name[record["from"]]
            self.assertNotEqual(
                giver.status, "claimed",
                f"{record['path']} was handed over by a face that is being worked on",
            )


if __name__ == "__main__":
    unittest.main()
