"""Guard tests for the material ingest module (#484).

What this file guards
---------------------
The Beta first release needs a material that is **ready**, and "ready" has to
be an assertable state rather than a sentence somebody wrote.  The module
``scripts/acceptance/beta_material_ingest.py`` owns that claim; these tests
pin the parts of it that must not drift:

* the status words are the ones the evidence producer writes, so a bundle
  that mixes both producers reads one way;
* the parse-status mapping is the **server's** mapping: the module's Python
  table is compared against ``internal/evidence/revision.go`` and
  ``internal/types/knowledge.go`` case by case, so the two cannot drift;
* the three evidence tiers of "processing ready" behave as documented: a
  tier nobody looked at is reported as not observed, never as a pass, and the
  claim rests on the strongest tier that *was* observed;
* the material hash is compared on both sides, and a mismatch is a failure
  rather than a blocked outcome;
* the three failure classes (ingest unreachable / processing incomplete /
  retrieval without a hit) are three different problems, each with its own
  unblock condition;
* the forbidden shortcut -- writing ``knowledges`` or ``chunks`` rows
  directly -- is refused by name;
* the real transport needs **two** switches, and the default mode performs no
  network call at all;
* a binding artifact lets a later round reuse the same material instead of
  uploading it again.

The recorded replay
-------------------
``RECORDED_REPLAY`` is a transcript of a **real** run: the material was
uploaded to WeKnora in an earlier session, parsed, embedded remotely and
bound to a project by the F01-06 step.  Its identifiers and hashes are the
ones that run produced; the provenance is written into the bundle.  Two of
the three evidence tiers were **not** observed in that run, and the bundle
says so -- which is the point: the replay shows what the machinery does with
a tier that has no evidence.
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "scripts" / "acceptance"))

import beta_evidence  # noqa: E402
import beta_material_ingest as ingest  # noqa: E402

MODULE_PATH = REPO_ROOT / "scripts" / "acceptance" / "beta_material_ingest.py"
GO_REVISION = REPO_ROOT / "internal" / "evidence" / "revision.go"
GO_ASSET = REPO_ROOT / "internal" / "evidence" / "asset.go"
GO_KNOWLEDGE = REPO_ROOT / "internal" / "types" / "knowledge.go"

MATERIAL_B_SHA256 = "29e2d8f3c2ae3d75eb278f66be554e4a5622ff8e56d9167d57fcee94f14ef258"

#: The transcript of the run that really happened.  Every identifier in it is
#: the one that run produced; the source of each fact is named.
RECORDED_REPLAY: dict = {
    "schema": "lingdoc.material.replay/v1",
    "source": {
        "kind": "recorded_run",
        "note": (
            "材料 B 在更早的一次会话里真的走过 WeKnora 的上传、解析与远程 embedding"
            "（账本 12 次请求，llm_debug 12 个文件），F01-06 这一轮把它绑到项目上，"
            "拿到 processing_state=ready。本回放只重放记录下来的那一段，不重跑。"),
        "evidence_dir": r"D:\wt\lingdoc-f01-data",
        "recorded_code_head": "a5fb88410e5c8dce40b98c24bf53390dba52395f",
        "database": "weknora_iso @127.0.0.1:5432 (PostgreSQL)",
        "facts": {
            "upload": "f01-evidence.txt / before.txt / after.txt（knowledges=2、"
                      "parse_status=completed、chunks=13、embeddings=13，执行前后未变）",
            "bind": "f01-contract-run-access-log.txt 的 POST /projects/{id}/assets "
                    "响应体（processing_state=ready）",
        },
    },
    "materials": [
        {
            "name": "synth-chapter-b",
            "sha256": MATERIAL_B_SHA256,
            "knowledge_base_id": "ab69e3a1-90f1-4562-8578-fb94bf3361fb",
            "file_name": "f0106-synth-chapter-b-" + MATERIAL_B_SHA256 + ".md",
            "query": "电解质 薄膜 厚度",
            "ingest": {
                "ok": True,
                "route": ingest.ALLOWED_INGEST_ROUTE,
                "knowledge_id": "128b88fd-8389-4800-9851-92e9bf6fe19c",
                "material_sha256": MATERIAL_B_SHA256,
                "http_status": 201,
                "seeded_directly": False,
            },
            "processing": {
                "ok": True,
                "parse_status": "completed",
                "terminal": True,
                "polls": 1,
                "binding": {
                    "asset_id": "1ceba747-79e5-4b6c-a78a-252d1324085e",
                    "asset_revision": 1,
                    "processing_state": "ready",
                    "project_id": "770576b5-c7e4-45f5-9175-daefc8766a96",
                },
                "chunk_count": None,
                "chunk_count_reason": (
                    "那一轮只落了库级计数（chunks=13，两份材料合计），"
                    "没有按资料标识读分块数 ⇒ 本档未观测。"),
            },
            "retrieval": {
                "ok": None,
                "hit_count": None,
                "query": "电解质 薄膜 厚度",
                "reason": "那一轮没有跑检索探针（已落盘证据里没有检索交换行）⇒ 本档未观测。",
            },
        },
    ],
}


def _bundle(**overrides) -> dict:
    """A copy of the recorded bundle with the given material fields replaced."""
    document = json.loads(json.dumps(RECORDED_REPLAY))
    material = document["materials"][0]
    for key, value in overrides.items():
        if key in ("ingest", "processing", "retrieval"):
            material[key].update(value)
        else:
            material[key] = value
    return document


def _transport(bundle: dict) -> ingest.OfflineReplayTransport:
    return ingest.OfflineReplayTransport(bundle=bundle, source="test")


def _spec(**overrides) -> ingest.MaterialSpec:
    values = {"name": "synth-chapter-b", "sha256": MATERIAL_B_SHA256,
              "knowledge_base_id": "ab69e3a1-90f1-4562-8578-fb94bf3361fb",
              "query": "电解质 薄膜 厚度"}
    values.update(overrides)
    return ingest.MaterialSpec(**values)


class VocabularyTests(unittest.TestCase):
    """The words are the evidence producer's words."""

    def test_the_status_words_match_the_evidence_producer(self):
        self.assertEqual(ingest.HELD, beta_evidence.HELD)
        self.assertEqual(ingest.FAILED, beta_evidence.FAILED)
        self.assertEqual(ingest.BLOCKED, beta_evidence.BLOCKED)
        self.assertEqual(ingest.NOT_RUN, beta_evidence.NOT_RUN)

    def test_the_module_quotes_no_ticket_number(self):
        """A gap is named by its identifier; the ticket number lives in the registry.

        The rule binds the **emitted unblock conditions**: those land in
        evidence artifacts and are compared across rounds, so a renumbered gap
        must not change them.  The module's own docstring may name the ticket
        that owns the work; that is documentation, not an unblock condition.
        """
        for identifier in re.findall(r"gap_unblock\(\"(GAP-[A-Z0-9-]+)\"\)",
                                     MODULE_PATH.read_text(encoding="utf-8")):
            with self.subTest(gap=identifier):
                self.assertIsNone(re.search(r"#\d{3}", ingest.gap_unblock(identifier)))

    def test_the_module_emits_no_ticket_number_in_any_unblock_condition(self):
        """Every unblock condition the module can produce is free of one."""
        conditions = []
        for bundle, stage_name in (
            (_bundle(ingest={"ok": False, "route": ingest.ALLOWED_INGEST_ROUTE}),
             "ingest"),
            (_bundle(processing={"ok": True, "parse_status": "processing",
                                 "terminal": False}), "poll"),
            (_bundle(retrieval={"ok": True, "hit_count": 0}), "retrieve"),
        ):
            record = ingest.run_material(_spec(), _transport(bundle))
            conditions.append(record["stages"][stage_name]["unblock"])
        conditions.append(ingest.gap_unblock("GAP-MATERIAL-HASH"))
        for condition in conditions:
            self.assertTrue(condition.strip())
            self.assertIsNone(re.search(r"#\d{3}", condition),
                              f"an unblock condition quotes a ticket number: {condition}")

    def test_every_gap_the_module_uses_is_registered(self):
        source = MODULE_PATH.read_text(encoding="utf-8")
        used = set(re.findall(r"gap_unblock\(\"(GAP-[A-Z0-9-]+)\"\)", source))
        self.assertTrue(used, "the module names no gap at all")
        registry = json.loads(ingest.GAP_PATH.read_text(encoding="utf-8"))["GAPS"]
        for identifier in used:
            self.assertIn(identifier, registry)

    def test_an_unregistered_gap_is_refused_by_name(self):
        with self.assertRaises(ingest.MaterialError) as caught:
            ingest.gap_unblock("GAP-NOT-REGISTERED")
        self.assertIn("GAP-NOT-REGISTERED", str(caught.exception))


class ParseStatusMappingTests(unittest.TestCase):
    """The Python mapping is the server's mapping, checked case by case."""

    @staticmethod
    def _parse_status_constants() -> dict[str, str]:
        source = GO_KNOWLEDGE.read_text(encoding="utf-8")
        return {name: value for name, value in
                re.findall(r"ParseStatus(\w+)\s*=\s*\"([^\"]*)\"", source)}

    @staticmethod
    def _asset_states() -> dict[str, str]:
        source = GO_ASSET.read_text(encoding="utf-8")
        return {name: value for name, value in
                re.findall(r"AssetState(\w+)\s+AssetState\s*=\s*\"([^\"]+)\"", source)}

    @staticmethod
    def _switch_cases() -> list[tuple[list[str], str]]:
        source = GO_REVISION.read_text(encoding="utf-8")
        body = source.split("func ProcessingStateOf", 1)[1]
        cases: list[tuple[list[str], str]] = []
        for match in re.finditer(r"case\s+([^:]+):\s*\n\s*return\s+AssetState(\w+)", body):
            terms = [term.strip() for term in match.group(1).split(",")]
            cases.append((terms, match.group(2)))
        return cases

    def test_the_go_switch_is_parsed(self):
        """The cross-language check is worthless if it parses nothing."""
        self.assertGreaterEqual(len(self._switch_cases()), 5)

    def test_every_parse_status_maps_to_the_same_state_in_both_languages(self):
        constants = self._parse_status_constants()
        states = self._asset_states()
        expected: dict[str, str] = {}
        for terms, state in self._switch_cases():
            for term in terms:
                if term.startswith("types.ParseStatus"):
                    name = term.split("types.ParseStatus", 1)[1]
                    self.assertIn(name, constants, term)
                    expected[constants[name]] = states[state]
                elif term.startswith('"') and term.endswith('"'):
                    expected[term.strip('"')] = states[state]
        self.assertTrue(expected)
        for parse_status, state in expected.items():
            with self.subTest(parse_status=parse_status):
                self.assertEqual(ingest.processing_state_of(parse_status), state)

    def test_only_completed_is_ready(self):
        """The server's rule, and this module's: only completed is ready."""
        ready = [status for status in ingest.PARSE_STATUS_TO_ASSET_STATE
                 if ingest.processing_state_of(status) == "ready"]
        self.assertEqual(ready, ["completed"])

    def test_an_unknown_status_is_pending_rather_than_a_pass(self):
        self.assertEqual(ingest.processing_state_of("some-future-status"), "pending")
        self.assertEqual(ingest.processing_state_of(None), "pending")
        self.assertEqual(ingest.processing_state_of(""), "pending")


class ReadyTierTests(unittest.TestCase):
    """Three tiers; the claim rests on the strongest one that was observed."""

    def test_a_fully_observed_chain_claims_ready_at_the_strongest_tier(self):
        record = ingest.run_material(_spec(), _transport(_bundle(
            processing={"chunk_count": 6},
            retrieval={"ok": True, "hit_count": 2})))
        evidence = record["ready_evidence"]
        self.assertEqual(evidence["claim"], "ready")
        self.assertEqual(evidence["strongest_observed_tier"], "retrieval_hits")
        self.assertTrue(all(tier["observed"] for tier in evidence["tiers"]))

    def test_a_tier_nobody_looked_at_is_not_observed_and_carries_a_reason(self):
        record = ingest.run_material(_spec(), _transport(RECORDED_REPLAY))
        tiers = {tier["tier"]: tier for tier in record["ready_evidence"]["tiers"]}
        self.assertTrue(tiers["processing_state"]["observed"])
        self.assertEqual(tiers["processing_state"]["value"], "ready")
        for name in ("chunk_count", "retrieval_hits"):
            with self.subTest(tier=name):
                self.assertFalse(tiers[name]["observed"])
                self.assertIsNone(tiers[name]["value"])
                self.assertTrue(tiers[name]["reason"].strip(),
                                f"{name} was not observed but carries no reason")

    def test_the_claim_of_the_recorded_run_rests_on_the_first_tier(self):
        record = ingest.run_material(_spec(), _transport(RECORDED_REPLAY))
        self.assertEqual(record["ready_evidence"]["claim"], "ready")
        self.assertEqual(record["ready_evidence"]["strongest_observed_tier"],
                         "processing_state")

    def test_a_not_ready_state_never_claims_ready(self):
        bundle = _bundle(processing={
            "binding": {"asset_id": "a-1", "asset_revision": 1,
                        "processing_state": "processing"}})
        record = ingest.run_material(_spec(), _transport(bundle))
        self.assertEqual(record["ready_evidence"]["claim"], "not_ready")

    def test_no_tier_at_all_is_not_observed(self):
        bundle = _bundle(ingest={"ok": False, "route": ingest.ALLOWED_INGEST_ROUTE,
                                 "reason": "服务不可达"})
        record = ingest.run_material(_spec(), _transport(bundle))
        self.assertEqual(record["ready_evidence"]["claim"], "not_observed")


class MaterialHashTests(unittest.TestCase):
    """The material's identity is checked on both sides."""

    def test_two_sides_that_agree_are_held(self):
        record = ingest.run_material(_spec(), _transport(RECORDED_REPLAY))
        stage = record["material_hash"]
        self.assertEqual(stage["status"], ingest.HELD)
        self.assertTrue(stage["match"])
        self.assertEqual(stage["input_side"], MATERIAL_B_SHA256)
        self.assertEqual(stage["assertion_side"], MATERIAL_B_SHA256)

    def test_two_sides_that_disagree_fail(self):
        bundle = _bundle(ingest={"material_sha256": "0" * 64})
        record = ingest.run_material(_spec(), _transport(bundle))
        stage = record["material_hash"]
        self.assertEqual(stage["status"], ingest.FAILED)
        self.assertFalse(stage["match"])
        self.assertEqual(stage["failure_class"], ingest.MATERIAL_HASH_MISMATCH)
        self.assertEqual(record["status"], ingest.FAILED)

    def test_a_missing_assertion_side_is_not_run_not_a_pass(self):
        bundle = _bundle(ingest={"material_sha256": None})
        record = ingest.run_material(_spec(), _transport(bundle))
        stage = record["material_hash"]
        self.assertEqual(stage["status"], ingest.NOT_RUN)
        self.assertIsNone(stage["match"])
        self.assertTrue(stage["unblock"].strip())

    def test_a_material_without_a_recorded_hash_is_refused(self):
        with self.assertRaises(ingest.MaterialError):
            ingest.parse_material(["synth-chapter-b=not-a-hash"])

    def test_the_hash_is_recorded_from_the_files_bytes_when_a_file_is_given(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "material.md"
            path.write_bytes(b"hello")
            self.assertEqual(ingest.sha256_of(path),
                             "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824")


class FailureClassificationTests(unittest.TestCase):
    """Three problems, three classes, three unblock conditions."""

    def test_an_unreachable_ingest_is_blocked_with_its_own_unblock(self):
        bundle = _bundle(ingest={"ok": False, "route": ingest.ALLOWED_INGEST_ROUTE,
                                 "reason": "连接被拒"})
        record = ingest.run_material(_spec(), _transport(bundle))
        stage = record["stages"]["ingest"]
        self.assertEqual(stage["status"], ingest.BLOCKED)
        self.assertEqual(stage["failure_class"], ingest.INGEST_UNREACHABLE)
        self.assertTrue(stage["unblock"].strip())
        self.assertEqual(record["status"], ingest.BLOCKED)

    def test_an_unfinished_processing_is_blocked_with_its_own_unblock(self):
        bundle = _bundle(processing={"ok": True, "parse_status": "processing",
                                     "terminal": False})
        record = ingest.run_material(_spec(), _transport(bundle))
        stage = record["stages"]["poll"]
        self.assertEqual(stage["status"], ingest.BLOCKED)
        self.assertEqual(stage["failure_class"], ingest.PROCESSING_INCOMPLETE)
        self.assertTrue(stage["unblock"].strip())

    def test_a_retrieval_without_a_hit_is_blocked_with_its_own_unblock(self):
        bundle = _bundle(retrieval={"ok": True, "hit_count": 0})
        record = ingest.run_material(_spec(), _transport(bundle))
        stage = record["stages"]["retrieve"]
        self.assertEqual(stage["status"], ingest.BLOCKED)
        self.assertEqual(stage["failure_class"], ingest.RETRIEVAL_NO_HIT)
        self.assertTrue(stage["unblock"].strip())

    def test_the_three_classes_carry_different_unblock_conditions(self):
        unblocks = set()
        for bundle, stage_name in (
            (_bundle(ingest={"ok": False, "route": ingest.ALLOWED_INGEST_ROUTE}),
             "ingest"),
            (_bundle(processing={"ok": True, "parse_status": "processing",
                                 "terminal": False}), "poll"),
            (_bundle(retrieval={"ok": True, "hit_count": 0}), "retrieve"),
        ):
            record = ingest.run_material(_spec(), _transport(bundle))
            unblocks.add(record["stages"][stage_name]["unblock"])
        self.assertEqual(len(unblocks), 3)

    def test_every_blocked_or_not_run_stage_carries_an_unblock(self):
        """\"Did not happen\" is only allowed together with \"how to make it happen\"."""
        for bundle in (RECORDED_REPLAY,
                       _bundle(ingest={"ok": False, "route": ingest.ALLOWED_INGEST_ROUTE}),
                       _bundle(processing={"ok": True, "parse_status": "processing",
                                           "terminal": False}),
                       _bundle(retrieval={"ok": True, "hit_count": 0})):
            record = ingest.run_material(_spec(), _transport(bundle))
            for name, stage in record["stages"].items():
                if stage["status"] in (ingest.BLOCKED, ingest.NOT_RUN):
                    with self.subTest(stage=name):
                        self.assertTrue(stage["unblock"].strip())

    def test_the_forbidden_shortcut_is_refused_by_name(self):
        bundle = _bundle(ingest={"seeded_directly": True,
                                 "route": "INSERT INTO knowledges ..."})
        record = ingest.run_material(_spec(), _transport(bundle))
        stage = record["stages"]["ingest"]
        self.assertEqual(stage["status"], ingest.FAILED)
        self.assertEqual(stage["failure_class"], ingest.FORBIDDEN_INGEST_ROUTE)
        self.assertEqual(record["status"], ingest.FAILED)

    def test_a_route_that_writes_the_tables_directly_is_refused(self):
        bundle = _bundle(ingest={"route": "insert into chunks (id) values (...)"})
        record = ingest.run_material(_spec(), _transport(bundle))
        self.assertEqual(record["stages"]["ingest"]["failure_class"],
                         ingest.FORBIDDEN_INGEST_ROUTE)

    def test_the_transport_interface_offers_no_way_to_seed_the_tables(self):
        """The forbidden shortcut is not reachable through the interface."""
        for method in dir(ingest.OfflineReplayTransport):
            self.assertNotIn("seed", method.lower())
        for method in dir(ingest.RealHttpTransport):
            self.assertNotIn("seed", method.lower())
        self.assertNotIn("insert", " ".join(
            line.lower() for line in MODULE_PATH.read_text(encoding="utf-8").splitlines()
            if "def " in line and "transport" not in line.lower()))


class ReuseTests(unittest.TestCase):
    """The same material is not uploaded twice."""

    def test_a_bound_material_is_reused_instead_of_ingested(self):
        reuse = {"name": "synth-chapter-b", "sha256": MATERIAL_B_SHA256,
                 "knowledge_id": "128b88fd-8389-4800-9851-92e9bf6fe19c"}
        record = ingest.run_material(_spec(), _transport(RECORDED_REPLAY),
                                     reuse=reuse)
        stage = record["stages"]["ingest"]
        self.assertEqual(stage["status"], ingest.REUSED)
        self.assertEqual(stage["evidence"]["source"], "binding artifact")
        self.assertEqual(record["knowledge_id"], "128b88fd-8389-4800-9851-92e9bf6fe19c")

    def test_the_reused_hash_is_compared_against_the_recorded_one(self):
        reuse = {"name": "synth-chapter-b", "sha256": "1" * 64,
                 "knowledge_id": "128b88fd-8389-4800-9851-92e9bf6fe19c"}
        record = ingest.run_material(_spec(), _transport(RECORDED_REPLAY),
                                     reuse=reuse)
        stage = record["material_hash"]
        self.assertEqual(stage["status"], ingest.FAILED)
        self.assertEqual(stage["failure_class"], ingest.MATERIAL_HASH_MISMATCH)
        self.assertIn("绑定产物", stage["assertion_side_source"])

    def test_the_binding_artifact_round_trips(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "material-binding.json"
            ingest.write_binding(path, [{
                "name": "synth-chapter-b", "sha256": MATERIAL_B_SHA256,
                "knowledge_id": "128b88fd-8389-4800-9851-92e9bf6fe19c",
            }], "abc123")
            records = ingest.load_binding(path)
            self.assertIn("synth-chapter-b", records)
            self.assertEqual(records["synth-chapter-b"]["knowledge_id"],
                             "128b88fd-8389-4800-9851-92e9bf6fe19c")

    def test_a_material_without_a_hash_is_not_written(self):
        """An unbound identity cannot be reused, so it is not written."""
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "material-binding.json"
            ingest.write_binding(path, [{"name": "x", "sha256": ""}], None)
            document = json.loads(path.read_text(encoding="utf-8"))
            self.assertEqual(document["materials"], [])

    def test_a_binding_artifact_with_the_wrong_schema_is_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "material-binding.json"
            path.write_text(json.dumps({"schema": "other"}), encoding="utf-8")
            with self.assertRaises(ingest.MaterialError):
                ingest.load_binding(path)


class TransportSwitchTests(unittest.TestCase):
    """The real transport needs two switches; the default is offline."""

    def test_the_default_mode_refuses_to_run_without_a_replay_bundle(self):
        with self.assertRaises(ingest.MaterialError) as caught:
            ingest.build_transport(ingest.parse_arguments([]))
        self.assertIn("--replay", str(caught.exception))

    def test_the_real_transport_needs_the_environment_variable_too(self):
        arguments = ingest.parse_arguments(["--real", "--base-url", "http://127.0.0.1:1"])
        previous = os.environ.pop(ingest.REAL_ENVIRONMENT_VARIABLE, None)
        try:
            with self.assertRaises(ingest.MaterialError) as caught:
                ingest.build_transport(arguments)
            self.assertIn(ingest.REAL_ENVIRONMENT_VARIABLE, str(caught.exception))
        finally:
            if previous is not None:
                os.environ[ingest.REAL_ENVIRONMENT_VARIABLE] = previous

    def test_the_environment_variable_alone_does_not_enable_the_real_transport(self):
        """One switch is not a switch: the environment variable alone stays offline."""
        with tempfile.TemporaryDirectory() as directory:
            bundle_path = Path(directory) / "replay.json"
            bundle_path.write_text(json.dumps(RECORDED_REPLAY, ensure_ascii=False),
                                   encoding="utf-8")
            arguments = ingest.parse_arguments(["--replay", str(bundle_path)])
            previous = os.environ.get(ingest.REAL_ENVIRONMENT_VARIABLE)
            os.environ[ingest.REAL_ENVIRONMENT_VARIABLE] = "1"
            try:
                transport = ingest.build_transport(arguments)
            finally:
                if previous is None:
                    os.environ.pop(ingest.REAL_ENVIRONMENT_VARIABLE, None)
                else:
                    os.environ[ingest.REAL_ENVIRONMENT_VARIABLE] = previous
            self.assertIsInstance(transport, ingest.OfflineReplayTransport)

    def test_both_switches_build_the_real_transport_without_calling_it(self):
        """Building the real transport makes no request; it only refuses to lie."""
        arguments = ingest.parse_arguments(
            ["--real", "--base-url", "http://127.0.0.1:1", "--token", "not-a-real-token"])
        previous = os.environ.get(ingest.REAL_ENVIRONMENT_VARIABLE)
        os.environ[ingest.REAL_ENVIRONMENT_VARIABLE] = "1"
        try:
            transport = ingest.build_transport(arguments)
        finally:
            if previous is None:
                os.environ.pop(ingest.REAL_ENVIRONMENT_VARIABLE, None)
            else:
                os.environ[ingest.REAL_ENVIRONMENT_VARIABLE] = previous
        self.assertIsInstance(transport, ingest.RealHttpTransport)

    def test_the_offline_transport_performs_no_network_call(self):
        """The replay is offline: it never opens a socket."""
        import urllib.request

        def forbidden(*args, **kwargs):
            raise AssertionError("the offline replay opened a socket")

        original = urllib.request.urlopen
        urllib.request.urlopen = forbidden
        try:
            record = ingest.run_material(_spec(), _transport(RECORDED_REPLAY))
        finally:
            urllib.request.urlopen = original
        self.assertEqual(record["stages"]["ingest"]["status"], ingest.HELD)


class RecordedReplayTests(unittest.TestCase):
    """The recorded run, replayed."""

    def test_the_recorded_run_reaches_the_first_tier_and_stops_there(self):
        record = ingest.run_material(_spec(), _transport(RECORDED_REPLAY))
        self.assertEqual(record["stages"]["ingest"]["status"], ingest.HELD)
        self.assertEqual(record["stages"]["poll"]["status"], ingest.HELD)
        self.assertEqual(record["stages"]["retrieve"]["status"], ingest.NOT_RUN)
        self.assertEqual(record["material_hash"]["status"], ingest.HELD)
        # The overall status is not_run: the retrieval tier was never observed,
        # and "not observed" is not a pass.
        self.assertEqual(record["status"], ingest.NOT_RUN)

    def test_the_recorded_identifiers_are_the_ones_the_run_produced(self):
        record = ingest.run_material(_spec(), _transport(RECORDED_REPLAY))
        self.assertEqual(record["knowledge_id"], "128b88fd-8389-4800-9851-92e9bf6fe19c")
        binding = record["stages"]["poll"]["evidence"]["binding"]
        self.assertEqual(binding["asset_id"], "1ceba747-79e5-4b6c-a78a-252d1324085e")
        self.assertEqual(binding["processing_state"], "ready")
        self.assertEqual(binding["project_id"], "770576b5-c7e4-45f5-9175-daefc8766a96")

    def test_the_ingest_route_is_the_one_the_protocol_allows(self):
        record = ingest.run_material(_spec(), _transport(RECORDED_REPLAY))
        self.assertEqual(record["stages"]["ingest"]["evidence"]["route"],
                         ingest.ALLOWED_INGEST_ROUTE)


class CommandLineTests(unittest.TestCase):
    """The entry point works and writes its two artifacts."""

    def _run(self, bundle: dict, extra: list[str] | None = None) -> tuple[int, Path]:
        directory = tempfile.mkdtemp(prefix="lingdoc-material-test-")
        root = Path(directory)
        bundle_path = root / "replay.json"
        bundle_path.write_text(json.dumps(bundle, ensure_ascii=False), encoding="utf-8")
        out = root / "out"
        completed = subprocess.run(
            [sys.executable, str(MODULE_PATH),
             "--replay", str(bundle_path),
             "--material", f"synth-chapter-b={MATERIAL_B_SHA256}",
             "--out", str(out)] + (extra or []),
            capture_output=True, text=True, encoding="utf-8", check=False, timeout=120)
        return completed.returncode, out

    def test_the_command_writes_a_report_and_a_binding(self):
        code, out = self._run(RECORDED_REPLAY)
        self.assertEqual(code, 1, "a run that did not observe the retrieval tier is not a pass")
        report = json.loads((out / "material-ingest-report.json").read_text(encoding="utf-8"))
        self.assertEqual(report["overall_status"], ingest.NOT_RUN)
        self.assertEqual(report["transport"]["kind"], "offline")
        self.assertFalse(report["transport"]["real_enabled"])
        binding = json.loads((out / "material-binding.json").read_text(encoding="utf-8"))
        self.assertEqual(binding["schema"], ingest.BINDING_SCHEMA)
        self.assertEqual(binding["materials"][0]["knowledge_id"],
                         "128b88fd-8389-4800-9851-92e9bf6fe19c")
        self.assertEqual(binding["materials"][0]["sha256"], MATERIAL_B_SHA256)

    def test_the_report_says_what_it_did_not_establish(self):
        _code, out = self._run(RECORDED_REPLAY)
        report = json.loads((out / "material-ingest-report.json").read_text(encoding="utf-8"))
        limits = " ".join(report["known_limits"])
        self.assertIn("离线回放", limits)
        self.assertIn("未观测", limits)
        self.assertIn("没有接进 F01 契约执行器", limits)

    def test_the_command_refuses_to_run_without_a_material(self):
        directory = tempfile.mkdtemp(prefix="lingdoc-material-test-")
        bundle_path = Path(directory) / "replay.json"
        bundle_path.write_text(json.dumps(RECORDED_REPLAY, ensure_ascii=False),
                               encoding="utf-8")
        completed = subprocess.run(
            [sys.executable, str(MODULE_PATH), "--replay", str(bundle_path)],
            capture_output=True, text=True, encoding="utf-8", check=False, timeout=120)
        self.assertEqual(completed.returncode, 1)
        self.assertIn("没有给出材料", completed.stderr)


if __name__ == "__main__":
    unittest.main()
