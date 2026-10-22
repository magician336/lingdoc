"""Tests for the parallel construction status view (``scripts/lingdoc_parallel_state.py``).

The status view joins two sources that live in different places: the naming
registry (what is left to repay) and the tracker (who is building what and
which face may be picked up).  These tests pin the seams that matter:

* the registry view counts each face's files and identifiers, and counts the
  ones still marked as legacy separately from the ones already repaid;
* the tracker view degrades gracefully -- a missing tracker must never raise,
  because the network is not part of the contract;
* the rendered table always carries the ratchet baseline and the scope, so a
  reader can tell which measurement they are looking at.
"""
from __future__ import annotations

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import lingdoc_naming_registry as registry  # noqa: E402
import lingdoc_parallel_state as state  # noqa: E402


class RegistryViewTests(unittest.TestCase):
    """The registry half of the status view."""

    def setUp(self):
        self.view = state.registry_view()

    def test_every_face_ticket_is_present(self):
        for ticket in state.FACE_TICKETS:
            self.assertIn(ticket, self.view)

    def test_counts_are_non_negative_and_bounded(self):
        for ticket, counts in self.view.items():
            self.assertGreaterEqual(counts["files"], 0, ticket)
            self.assertLessEqual(counts["files_remaining"], counts["files"], ticket)
            self.assertLessEqual(counts["ids_remaining"], counts["ids"], ticket)

    def test_totals_match_the_registry(self):
        files = sum(c["files"] for c in self.view.values())
        self.assertEqual(files, len(registry.NAMED_FILES))

    def test_remaining_counts_the_legacy_entries(self):
        expected = {}
        for entry in registry.NAMED_FILES:
            if entry["legacy"]:
                expected[entry["ticket"]] = expected.get(entry["ticket"], 0) + 1
        for ticket, counts in self.view.items():
            self.assertEqual(counts["files_remaining"], expected.get(ticket, 0), ticket)


class TrackerViewTests(unittest.TestCase):
    """The tracker half degrades instead of failing."""

    def test_missing_tracker_yields_no_tickets(self):
        original = state._gh
        try:
            state._gh = lambda *args: (127, "")
            self.assertEqual(state.tracker_view(), {})
        finally:
            state._gh = original

    def test_malformed_json_yields_no_ticket(self):
        original = state._gh
        try:
            state._gh = lambda *args: (0, "not json at all")
            self.assertEqual(state.tracker_view(), {})
        finally:
            state._gh = original

    def test_ticket_is_claimable_only_when_open_and_labelled(self):
        tracker = state.TrackerTicket(
            number=362,
            state="open",
            assignees=("someone",),
            labels=("ready-for-agent",),
            blocked_by=(361,),
        )
        self.assertTrue(tracker.claimable)
        self.assertEqual(tracker.assignees, ("someone",))

        closed = state.TrackerTicket(362, "closed", (), ("ready-for-agent",), ())
        self.assertFalse(closed.claimable)

        unlabelled = state.TrackerTicket(362, "open", (), (), ())
        self.assertFalse(unlabelled.claimable)


class RenderTests(unittest.TestCase):
    """The rendered table always says what it is measuring."""

    def setUp(self):
        # The renderer must never depend on the network: stub the tracker so
        # these tests stay fast and deterministic in CI.
        self._original = state._gh
        state._gh = self._stub_gh

    def tearDown(self):
        state._gh = self._original

    @staticmethod
    def _stub_gh(*args: str) -> tuple[int, str]:
        if "dependencies/blocked_by" in " ".join(args):
            return 0, json.dumps([{"number": 361}])
        return 0, json.dumps({
            "number": 362,
            "state": "open",
            "assignees": [{"login": "someone"}],
            "labels": [{"name": "ready-for-agent"}],
        })

    def test_render_mentions_the_ratchet_baseline(self):
        text = state.render(state.build_status())
        baseline = registry.RATCHET_BASELINE
        self.assertIn(str(baseline["files"]), text)
        self.assertIn(baseline["base_sha"][:8], text)
        self.assertIn(baseline["scope"], text)

    def test_render_lists_every_face(self):
        text = state.render(state.build_status())
        for ticket in state.FACE_TICKETS:
            self.assertIn(f"#{ticket}", text)

    def test_render_shows_the_claimant_and_the_blocker(self):
        text = state.render(state.build_status())
        self.assertIn("someone", text)
        self.assertIn("#361", text)

    def test_render_survives_a_missing_tracker(self):
        state._gh = lambda *args: (127, "")
        text = state.render(state.build_status())
        self.assertIn("?", text)


if __name__ == "__main__":
    unittest.main()
