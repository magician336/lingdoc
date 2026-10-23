#!/usr/bin/env python3
"""Material ingest orchestration for the Beta first release.

(The ticket that owns this work is named in the pull request and in the
parallel-surface registry; it is deliberately not written here, so that this
file -- like the evidence producer next to it -- carries no ticket number of
its own.)

What this file owns
-------------------
The material chain, in three stages, and one claim that has to be **assertable**
instead of declared::

    ingest  ->  poll  ->  retrieve
    接入        处理轮询    检索探针

Each stage produces a machine-readable result, and "processing ready" is
carried by three tiers of evidence, weakest first:

1. ``processing_state`` -- the ``processing_state`` in the binding response.
   It is the weakest tier because it is a *derived* state: the server maps a
   parse status onto it (see :func:`processing_state_of`).  It says the
   material finished parsing; it does not say the material is retrievable.
2. ``chunk_count`` -- how many chunks the knowledge id produced.  It proves
   the material was really split, so the identifier is not an empty shell.
3. ``retrieval_hits`` -- how many hits the retrieval probe returned.  It is
   the strongest tier and the closest to the protocol's own wording, which
   asks for a material that can be **retrieved**.

The tier that was actually observed is recorded, and so is every tier that was
*not* observed, with the reason.  A tier that nobody looked at is never
reported as a pass: "not observed" is a first-class result here, the same way
``not_run`` is in the evidence producer.

Why the material hash is compared on both sides
-----------------------------------------------
A material's identity is its SHA-256 (``材料身份＝固定前缀＋文件 SHA-256``, so
the same bytes always mean the same material).  That identity is only worth
something if two independent sides agree on it:

* the **input side** is recorded *before* the run -- the operator's
  ``--material NAME=SHA256`` (or the hash computed from the material file's
  bytes), or the binding artifact a previous round left behind;
* the **assertion side** is what the system reports it actually took, read
  back from the ingest result.

If the two disagree, the material is not the material the run was about.  That
is a ``failed`` outcome, not a blocked one: nothing about the environment
would make the two hashes equal.

Failure classification
----------------------
Three things can go wrong, and they are three different problems:

* ``ingest_unreachable`` -- the material never got in (bad base URL, bad
  token, wrong knowledge base).  **Blocked**: the environment is the reason.
* ``processing_incomplete`` -- it got in, but the parse never reached a
  terminal state.  **Blocked**: the parse is still in flight or failed.
* ``retrieval_no_hit`` -- it is ready, but the retrieval probe found
  nothing.  **Blocked**: the index or the query is the reason.

Each of them carries an unblock condition, so "did not happen" always exists
together with "how to make it happen".

The forbidden shortcut
----------------------
The protocol forbids making the material ready by writing ``knowledges`` or
``chunks`` rows directly.  Two things enforce that here: the transport
interface has no operation that writes those tables, and a transport that
reports it seeded them anyway is a **failed** outcome, not a pass.

Reuse across rounds
-------------------
A run that ends with a usable material leaves a **binding artifact**: the
material's name, its hash, and the identifiers it resolved to.  The next round
reads that artifact and reuses the material instead of ingesting it again --
the ingest stage is then ``reused``, and the artifact says where the claim
came from.

Boundaries of this round
------------------------
* The real transport is **not enabled**.  It needs both ``--real`` and the
  environment variable ``LINGDOC_BETA_MATERIAL_REAL=1``; without both, the
  module refuses to build it.  The default transport is the offline replay.
* The offline replay replays a recorded transcript.  It performs no network
  call, no provider call and no embedding call.
* This module does not wire itself into the F01 contract executor.  That is
  the executioner's job, and the executor's paths belong to another face.

Where the vocabulary comes from
-------------------------------
The four status words are the ones ``scripts/acceptance/beta_evidence.py``
writes (``held`` / ``failed`` / ``blocked`` / ``not_run``), so an evidence
bundle that mixes both producers reads one way.  ``reused`` is added here for
the stage that did not re-run but carries a recorded claim.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request
from dataclasses import dataclass, replace
from typing import Any, Mapping, Protocol, Sequence

ROOT = Path(__file__).resolve().parents[2]
GAP_PATH = ROOT / "docs/08-本轮实施方案/acceptance/gaps.json"

# The four words the evidence producer writes, plus one for a stage that was
# not re-run but carries a recorded claim.
HELD = "held"
FAILED = "failed"
BLOCKED = "blocked"
NOT_RUN = "not_run"
REUSED = "reused"

STAGE_STATUSES = (HELD, FAILED, BLOCKED, NOT_RUN, REUSED)

# The three tiers of "processing ready", weakest first.  The order is the
# strength order: a later tier implies the earlier ones.
TIERS = ("processing_state", "chunk_count", "retrieval_hits")

# The route the protocol allows for putting a material in.  Writing the
# knowledges or chunks tables directly is forbidden (protocol section 1) and
# is refused by name.
ALLOWED_INGEST_ROUTE = "POST /api/v1/knowledge-bases/{knowledge_base_id}/knowledge/file"
FORBIDDEN_ROUTE_MARKERS = (
    "insert into knowledges",
    "insert into chunks",
    "direct_db_seed",
    "direct_sql",
)

# The environment variable that has to agree with the --real flag.
REAL_ENVIRONMENT_VARIABLE = "LINGDOC_BETA_MATERIAL_REAL"

SHA256 = re.compile(r"\A[0-9a-f]{64}\Z")

# The failure classes.  Each one names a different problem and therefore a
# different unblock condition.
INGEST_UNREACHABLE = "ingest_unreachable"
PROCESSING_INCOMPLETE = "processing_incomplete"
RETRIEVAL_NO_HIT = "retrieval_no_hit"
MATERIAL_HASH_MISMATCH = "material_hash_mismatch"
FORBIDDEN_INGEST_ROUTE = "forbidden_ingest_route"
INGEST_NOT_OBSERVED = "ingest_not_observed"

FAILURE_CLASSES = (
    INGEST_UNREACHABLE,
    PROCESSING_INCOMPLETE,
    RETRIEVAL_NO_HIT,
    MATERIAL_HASH_MISMATCH,
    FORBIDDEN_INGEST_ROUTE,
    INGEST_NOT_OBSERVED,
)


class MaterialError(RuntimeError):
    """An error the operator has to see; never a silent pass."""


# --- the parse-status mapping ----------------------------------------------
# This mirrors ``internal/evidence/revision.go``'s ``ProcessingStateOf``.  The
# guard test pins the two against each other: only ``completed`` is ready, and
# an unknown status is pending (the repository's "default to refusal" rule).
PARSE_STATUS_COMPLETED = "completed"
PARSE_STATUS_TO_ASSET_STATE = {
    "completed": "ready",
    "processing": "processing",
    "finalizing": "processing",
    "failed": "failed",
    "cancelled": "failed",
    "deleting": "replaced",
    "pending": "pending",
    "unprocessed": "pending",
    "": "pending",
}


def processing_state_of(parse_status: str | None) -> str:
    """The asset state a parse status maps onto.

    Same rule as the server: only ``completed`` is ``ready``; ``cancelled``
    joins ``failed`` (there is no usable parse product and it will not
    recover on its own); an unknown status is ``pending`` rather than a pass.
    """
    return PARSE_STATUS_TO_ASSET_STATE.get((parse_status or "").strip().lower(), "pending")


# --- the gap registry ------------------------------------------------------

def gap_unblock(identifier: str) -> str:
    """The unblock condition of a gap, named by its stable identifier.

    The identifier is the stable key: the ticket number behind a gap changes,
    the identifier does not.  So the **registry file** is the one place that
    binds them, and the producer quotes the identifier rather than hardcoding
    a number.

    The emitted condition still carries the ticket number, because the
    judgement rules require the release condition and the follow-up ticket to
    sit in the **same cell** (口径 §3.3 规则 6 / §5.3 两个必带件).  It is
    resolved from the registry at emit time, so a renumbering changes
    ``gaps.json`` alone and every artifact follows it automatically.
    """
    try:
        registry = json.loads(GAP_PATH.read_text(encoding="utf-8"))["GAPS"]
    except (OSError, UnicodeDecodeError, json.JSONDecodeError, KeyError) as error:
        raise MaterialError(f"the gap registry could not be read: {error}") from error
    entry = registry.get(identifier)
    if entry is None:
        raise MaterialError(f"the gap registry has no gap named {identifier}")
    ticket = str(entry.get("ticket", "")).strip()
    item = str(entry.get("item", "")).strip()
    if not ticket or not item:
        raise MaterialError(f"the gap registry entry {identifier} has no ticket or no item")
    return (f"（缺口 `{identifier}`（跟进票 {ticket}，{item}）：{entry['unblock']}；"
            f"对应关系见 docs/08-本轮实施方案/acceptance/gaps.json）")


# --- observations ----------------------------------------------------------

@dataclass(frozen=True)
class MaterialSpec:
    """What the operator says about the material before the run.

    ``sha256`` is the **input side** of the identity check: it is recorded
    before anything is ingested.
    """

    name: str
    sha256: str
    knowledge_base_id: str = ""
    file_name: str = ""
    query: str = ""


@dataclass(frozen=True)
class IngestObservation:
    """What the ingest step returned."""

    ok: bool
    route: str
    knowledge_id: str | None = None
    material_sha256: str | None = None
    http_status: int | None = None
    seeded_directly: bool = False
    reason: str = ""


@dataclass(frozen=True)
class BindingObservation:
    """The asset binding that carries the processing state."""

    asset_id: str
    asset_revision: int
    processing_state: str
    project_id: str = ""


@dataclass(frozen=True)
class ProcessingObservation:
    """What the poll step returned."""

    ok: bool
    parse_status: str | None = None
    terminal: bool = False
    polls: int = 0
    binding: BindingObservation | None = None
    chunk_count: int | None = None
    reason: str = ""
    chunk_count_reason: str = ""


@dataclass(frozen=True)
class RetrievalObservation:
    """What the retrieval probe returned."""

    ok: bool
    query: str = ""
    hit_count: int | None = None
    reason: str = ""


class MaterialTransport(Protocol):
    """The three operations the ingest chain needs.

    There is deliberately **no** operation that writes ``knowledges`` or
    ``chunks``: the forbidden shortcut is not reachable through this
    interface, and a transport that claims it did so anyway is caught by the
    ``seeded_directly`` flag.
    """

    name: str

    def ingest(self, spec: MaterialSpec) -> IngestObservation: ...

    def poll(self, spec: MaterialSpec, knowledge_id: str) -> ProcessingObservation: ...

    def retrieve(self, spec: MaterialSpec, knowledge_id: str) -> RetrievalObservation: ...


# --- the offline replay transport ------------------------------------------

@dataclass
class OfflineReplayTransport:
    """Replay a recorded transcript.  It performs no network call.

    A replay bundle is a JSON document::

        {
          "schema": "lingdoc.material.replay/v1",
          "source": {"kind": "recorded_run", "note": "..."},
          "materials": [
            {
              "name": "...",
              "sha256": "<input side>",
              "knowledge_base_id": "...",
              "file_name": "...",
              "query": "...",
              "ingest":    {"ok": true, "route": "...", "knowledge_id": "...",
                            "material_sha256": "<assertion side>", "http_status": 201},
              "processing": {"ok": true, "parse_status": "completed", "terminal": true,
                             "polls": 1,
                             "binding": {"asset_id": "...", "asset_revision": 1,
                                         "processing_state": "ready"},
                             "chunk_count": null,
                             "chunk_count_reason": "..."},
              "retrieval": {"ok": null, "hit_count": null, "reason": "..."}
            }
          ]
        }

    Every stage may be recorded as **not observed** (``ok: null``).  A stage
    that nobody recorded is ``not_run`` with the reason the recording gives --
    it is never reported as a pass.
    """

    bundle: Mapping[str, Any]
    source: str = ""
    name: str = "offline-replay"

    def __post_init__(self) -> None:
        schema = self.bundle.get("schema")
        if schema != "lingdoc.material.replay/v1":
            raise MaterialError(
                f"a replay bundle declares schema {schema!r}, "
                "expected 'lingdoc.material.replay/v1'")
        materials = self.bundle.get("materials")
        if not isinstance(materials, list) or not materials:
            raise MaterialError("a replay bundle carries no materials")

    def _entry(self, spec: MaterialSpec) -> Mapping[str, Any]:
        for entry in self.bundle.get("materials", []):
            if entry.get("name") == spec.name:
                return entry
        raise MaterialError(f"the replay bundle has no material named {spec.name!r}")

    def material_fields(self, name: str) -> dict[str, Any]:
        """The facts the bundle records about a material besides the observations.

        The knowledge base, the file name and the retrieval query belong to the
        material, not to the run, so a replay that recorded them can supply
        them to a caller that did not.
        """
        try:
            entry = self._entry(MaterialSpec(name=name, sha256=""))
        except MaterialError:
            return {}
        return {
            "knowledge_base_id": entry.get("knowledge_base_id", ""),
            "file_name": entry.get("file_name", ""),
            "query": entry.get("query", ""),
        }

    def ingest(self, spec: MaterialSpec) -> IngestObservation:
        raw = self._entry(spec).get("ingest")
        if not isinstance(raw, Mapping):
            raise MaterialError(f"the replay bundle records no ingest for {spec.name!r}")
        return IngestObservation(
            ok=bool(raw.get("ok")),
            route=str(raw.get("route", "")),
            knowledge_id=raw.get("knowledge_id"),
            material_sha256=raw.get("material_sha256"),
            http_status=raw.get("http_status"),
            seeded_directly=bool(raw.get("seeded_directly")),
            reason=str(raw.get("reason", "")),
        )

    def poll(self, spec: MaterialSpec, knowledge_id: str) -> ProcessingObservation:
        raw = self._entry(spec).get("processing")
        if not isinstance(raw, Mapping):
            raise MaterialError(f"the replay bundle records no processing for {spec.name!r}")
        binding_raw = raw.get("binding")
        binding = None
        if isinstance(binding_raw, Mapping):
            binding = BindingObservation(
                asset_id=str(binding_raw.get("asset_id", "")),
                asset_revision=int(binding_raw.get("asset_revision", 0)),
                processing_state=str(binding_raw.get("processing_state", "")),
                project_id=str(binding_raw.get("project_id", "")),
            )
        return ProcessingObservation(
            ok=bool(raw.get("ok")),
            parse_status=raw.get("parse_status"),
            terminal=bool(raw.get("terminal")),
            polls=int(raw.get("polls", 0)),
            binding=binding,
            chunk_count=raw.get("chunk_count"),
            reason=str(raw.get("reason", "")),
            chunk_count_reason=str(raw.get("chunk_count_reason", "")),
        )

    def retrieve(self, spec: MaterialSpec, knowledge_id: str) -> RetrievalObservation:
        raw = self._entry(spec).get("retrieval")
        if not isinstance(raw, Mapping):
            raise MaterialError(f"the replay bundle records no retrieval for {spec.name!r}")
        return RetrievalObservation(
            ok=bool(raw.get("ok")),
            query=str(raw.get("query", spec.query)),
            hit_count=raw.get("hit_count"),
            reason=str(raw.get("reason", "")),
        )


# --- the real transport ----------------------------------------------------

@dataclass
class RealHttpTransport:
    """Talk to a real LingDoc / WeKnora.  **Not enabled in this round.**

    Building this transport takes two independent switches on purpose: the
    ``--real`` flag and the ``LINGDOC_BETA_MATERIAL_REAL=1`` environment
    variable.  One of them alone does nothing.  A tool that can reach a
    provider by accident is a tool that will.

    This implementation is written but **not exercised** in this round: the
    round makes no provider call and no network call, so its correctness is
    unverified.  The wiring that would use it belongs to the executor.
    """

    base_url: str
    token: str
    poll_interval: float = 2.0
    poll_timeout: float = 300.0
    name: str = "real-http"

    def __post_init__(self) -> None:
        if not self.base_url or not self.token:
            raise MaterialError("the real transport needs both a base URL and a token")

    def _request(self, method: str, path: str, *, body: Any = None,
                 raw: bytes | None = None, content_type: str | None = None) -> Any:
        url = self.base_url.rstrip("/") + path
        headers = {"Authorization": f"Bearer {self.token}"}
        data = raw
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        elif content_type is not None:
            headers["Content-Type"] = content_type
        request = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                body_bytes = response.read()
                return response.status, json.loads(body_bytes or b"{}")
        except urllib.error.HTTPError as error:
            return error.code, {}
        except (urllib.error.URLError, TimeoutError, OSError) as error:
            raise MaterialError(f"the request to {path} did not reach the service: {error}") from error

    def ingest(self, spec: MaterialSpec) -> IngestObservation:
        # The multipart upload of the material file is the one operation that
        # needs the file's bytes; the runner supplies them.
        raise MaterialError(
            "the real ingest path needs the material file's bytes and is not "
            "implemented in this round; the offline replay is the default")

    def poll(self, spec: MaterialSpec, knowledge_id: str) -> ProcessingObservation:
        status, body = self._request(
            "GET", f"/api/v1/knowledge/{knowledge_id}")
        data = body.get("data") if isinstance(body, Mapping) else None
        if not isinstance(data, Mapping):
            return ProcessingObservation(
                ok=False, parse_status=None, reason=f"the knowledge read returned HTTP {status}")
        parse_status = str(data.get("parse_status", ""))
        return ProcessingObservation(
            ok=True,
            parse_status=parse_status,
            terminal=parse_status.lower() in ("completed", "failed", "cancelled", "deleting"),
            polls=1,
            binding=None,
            chunk_count=None,
            chunk_count_reason=("the real transport does not read the chunk table; "
                                "the chunk count needs a real read that this round does not make"),
        )

    def retrieve(self, spec: MaterialSpec, knowledge_id: str) -> RetrievalObservation:
        query = spec.query
        if not query:
            return RetrievalObservation(
                ok=False, reason="no retrieval query was given; the probe needs one")
        status, body = self._request(
            "POST", f"/api/v1/knowledge-bases/{spec.knowledge_base_id}/hybrid-search",
            body={"query": query})
        data = body.get("data") if isinstance(body, Mapping) else None
        hits = data if isinstance(data, list) else []
        return RetrievalObservation(
            ok=status == 200, query=query, hit_count=len(hits),
            reason="" if status == 200 else f"the retrieval probe returned HTTP {status}")


# --- the binding artifact --------------------------------------------------

BINDING_SCHEMA = "lingdoc.material.binding/v1"


def load_binding(path: Path) -> dict[str, dict[str, Any]]:
    """Read a binding artifact: material name -> the record of that material."""
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise MaterialError(f"the binding artifact could not be read: {error}") from error
    if document.get("schema") != BINDING_SCHEMA:
        raise MaterialError(
            f"the binding artifact declares schema {document.get('schema')!r}, "
            f"expected {BINDING_SCHEMA!r}")
    entries: dict[str, dict[str, Any]] = {}
    for record in document.get("materials", []):
        name = str(record.get("name", ""))
        if not name:
            continue
        entries[name] = record
    return entries


def write_binding(path: Path, materials: Sequence[Mapping[str, Any]], head: str | None) -> None:
    """Write the binding artifact: the material identity and what it resolved to.

    This is the artifact a later round reuses, so it carries the hash (the
    identity) next to the identifiers (the resolution).  A material without a
    hash is not written: an unbound identity cannot be reused.
    """
    usable = [record for record in materials if str(record.get("sha256", "")).strip()]
    document = {
        "schema": BINDING_SCHEMA,
        "head": head,
        "materials": list(usable),
    }
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(document, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


# --- the three stages ------------------------------------------------------

def _stage(status: str, *, reason: str = "", unblock: str = "",
           failure_class: str | None = None,
           evidence: Mapping[str, Any] | None = None) -> dict[str, Any]:
    if status not in STAGE_STATUSES:
        raise MaterialError(f"unknown stage status {status!r}")
    if status in (BLOCKED, NOT_RUN) and not unblock:
        raise MaterialError(
            f"a {status} stage must carry an unblock condition: "
            "\"did not happen\" is only allowed together with \"how to make it happen\"")
    return {
        "status": status,
        "reason": reason,
        "unblock": unblock,
        "failure_class": failure_class,
        "evidence": dict(evidence or {}),
    }


def _ingest_stage(spec: MaterialSpec, transport: MaterialTransport) -> dict[str, Any]:
    """Stage one: put the material in."""
    observation = transport.ingest(spec)
    if observation.seeded_directly or _route_is_forbidden(observation.route):
        return _stage(
            FAILED,
            reason=("材料是靠直接写 knowledges / chunks 表做成的"
                    f"（走的是 {observation.route!r}）；协议禁止这条捷径，"
                    "所以这一轮什么也没证明"),
            failure_class=FORBIDDEN_INGEST_ROUTE,
            evidence={"route": observation.route},
        )
    if not observation.ok or not observation.knowledge_id:
        return _stage(
            BLOCKED,
            reason=(observation.reason
                    or f"材料没有进来（走的是 {observation.route!r}）"),
            unblock=(gap_unblock("GAP-WEKNORA-INGEST")
                     + "；确认材料真的走了上传接口：知识库标识、访问令牌与服务地址"),
            failure_class=INGEST_UNREACHABLE,
            evidence={"route": observation.route, "http_status": observation.http_status},
        )
    return _stage(
        HELD,
        reason="材料走了上传接口进来了",
        evidence={
            "route": observation.route,
            "http_status": observation.http_status,
            "knowledge_id": observation.knowledge_id,
            "material_sha256": observation.material_sha256,
        },
    )


def _poll_stage(spec: MaterialSpec, transport: MaterialTransport,
                knowledge_id: str) -> dict[str, Any]:
    """Stage two: wait for the processing to reach a terminal state."""
    observation = transport.poll(spec, knowledge_id)
    if not observation.ok:
        return _stage(
            BLOCKED,
            reason=observation.reason or "读不到处理状态",
            unblock=(gap_unblock("GAP-WEKNORA-INGEST")
                     + "；轮询到 parse_status 进入终态（completed / failed / cancelled）后重跑"),
            failure_class=PROCESSING_INCOMPLETE,
            evidence={"parse_status": observation.parse_status},
        )
    if not observation.terminal:
        return _stage(
            BLOCKED,
            reason=(observation.reason
                    or f"解析状态 {observation.parse_status!r} 还没到终态"),
            unblock=(gap_unblock("GAP-WEKNORA-INGEST")
                     + "；等解析进入终态后重跑本脚本"),
            failure_class=PROCESSING_INCOMPLETE,
            evidence={"parse_status": observation.parse_status, "polls": observation.polls},
        )
    return _stage(
        HELD,
        reason=f"解析状态到达终态 {observation.parse_status!r}",
        evidence={
            "parse_status": observation.parse_status,
            "polls": observation.polls,
            "binding": (None if observation.binding is None else {
                "asset_id": observation.binding.asset_id,
                "asset_revision": observation.binding.asset_revision,
                "processing_state": observation.binding.processing_state,
                "project_id": observation.binding.project_id,
            }),
            "chunk_count": observation.chunk_count,
            "chunk_count_reason": observation.chunk_count_reason,
        },
    )


def _retrieve_stage(spec: MaterialSpec, transport: MaterialTransport,
                    knowledge_id: str) -> dict[str, Any]:
    """Stage three: probe whether the material can be retrieved."""
    observation = transport.retrieve(spec, knowledge_id)
    if observation.hit_count is None:
        # Not observed is not "no hit": the probe never ran, so there is no
        # failure class to name.  Saying "no hit" would blame the index for
        # something nobody looked at.
        return _stage(
            NOT_RUN,
            reason=observation.reason or "检索探针没有跑",
            unblock=(gap_unblock("GAP-WEKNORA-INGEST")
                     + "；在真实环境跑一次检索探针后重跑本脚本"),
            failure_class=None,
            evidence={"query": observation.query},
        )
    if observation.hit_count <= 0:
        return _stage(
            BLOCKED,
            reason=(observation.reason
                    or f"检索探针对 {observation.query!r} 没有命中"),
            unblock=(gap_unblock("GAP-WEKNORA-INGEST")
                     + "；核对检索索引与查询词后重跑本脚本"),
            failure_class=RETRIEVAL_NO_HIT,
            evidence={"query": observation.query, "hit_count": observation.hit_count},
        )
    return _stage(
        HELD,
        reason=f"检索探针命中 {observation.hit_count} 条",
        evidence={"query": observation.query, "hit_count": observation.hit_count},
    )


def _route_is_forbidden(route: str) -> bool:
    lowered = (route or "").lower()
    return any(marker in lowered for marker in FORBIDDEN_ROUTE_MARKERS)


def ready_evidence(poll: Mapping[str, Any], retrieve: Mapping[str, Any]) -> dict[str, Any]:
    """The three tiers of "processing ready", and which one was observed.

    A tier nobody looked at is ``observed: false`` with the reason.  The
    strongest observed tier is the one the claim rests on.
    """
    binding = poll.get("evidence", {}).get("binding") or {}
    processing_state = binding.get("processing_state")
    chunk_count = poll.get("evidence", {}).get("chunk_count")
    hit_count = retrieve.get("evidence", {}).get("hit_count")

    tiers: list[dict[str, Any]] = []

    if processing_state:
        tiers.append({
            "tier": "processing_state",
            "observed": True,
            "value": processing_state,
            "source": "绑定动作响应里的 processing_state",
            "strength": 1,
            "note": ("最弱的一档：它是 parse_status 的派生状态，"
                     "说明材料解析完了，不说明材料检索得到"),
        })
    else:
        tiers.append({
            "tier": "processing_state",
            "observed": False,
            "value": None,
            "source": None,
            "strength": 1,
            "reason": poll.get("reason") or "本轮没有绑定动作的 processing_state",
        })

    if chunk_count is not None:
        tiers.append({
            "tier": "chunk_count",
            "observed": True,
            "value": chunk_count,
            "source": "该资料标识下的分块数",
            "strength": 2,
            "note": "证明材料真的被切分了，标识不是空壳",
        })
    else:
        tiers.append({
            "tier": "chunk_count",
            "observed": False,
            "value": None,
            "source": None,
            "strength": 2,
            "reason": poll.get("evidence", {}).get("chunk_count_reason")
            or "本轮没有读到该资料标识下的分块数",        })

    if hit_count is not None:
        tiers.append({
            "tier": "retrieval_hits",
            "observed": True,
            "value": hit_count,
            "source": "检索探针的命中数",
            "strength": 3,
            "note": "最强的一档：最接近协议 §1 对「检索」的要求",
        })
    else:
        tiers.append({
            "tier": "retrieval_hits",
            "observed": False,
            "value": None,
            "source": None,
            "strength": 3,
            "reason": retrieve.get("reason") or "本轮没有跑检索探针",
        })

    observed = [tier for tier in tiers if tier["observed"]]
    strongest = max(observed, key=lambda tier: tier["strength"]) if observed else None
    if strongest is None:
        claim = "not_observed"
    elif strongest["tier"] == "processing_state" and strongest["value"] != "ready":
        claim = "not_ready"
    else:
        claim = "ready"
    return {
        "claim": claim,
        "strongest_observed_tier": None if strongest is None else strongest["tier"],
        "tiers": tiers,
    }


def _hash_stage(spec: MaterialSpec, assertion_sha256: str | None,
                source: str) -> dict[str, Any]:
    """Compare the two sides of the material identity.

    ``source`` says where the assertion side came from.  It matters: when the
    material is reused, the assertion side is the digest the binding artifact
    carries (which a previous round read back from the system), not a fresh
    read.  The report says so instead of implying a fresh observation.
    """
    input_sha = (spec.sha256 or "").strip().lower()
    assertion = (assertion_sha256 or "").strip().lower()
    if not input_sha:
        return {
            "status": NOT_RUN,
            "input_side": None,
            "assertion_side": assertion or None,
            "assertion_side_source": source,
            "match": None,
            "reason": "运行前没有记录材料哈希",
            "unblock": gap_unblock("GAP-MATERIAL-HASH"),
        }
    if not assertion:
        return {
            "status": NOT_RUN,
            "input_side": input_sha,
            "assertion_side": None,
            "assertion_side_source": source,
            "match": None,
            "reason": "运行时没有读到系统侧报告的材料哈希",
            "unblock": gap_unblock("GAP-MATERIAL-HASH"),
        }
    if input_sha != assertion:
        return {
            "status": FAILED,
            "input_side": input_sha,
            "assertion_side": assertion,
            "assertion_side_source": source,
            "match": False,
            "reason": ("材料哈希两侧不一致：运行前记录的是 "
                       f"{input_sha}，运行时系统报告的是 {assertion}；"
                       "这不是环境问题，重跑不会让它一致"),
            "unblock": "",
            "failure_class": MATERIAL_HASH_MISMATCH,
        }
    return {
        "status": HELD,
        "input_side": input_sha,
        "assertion_side": assertion,
        "assertion_side_source": source,
        "match": True,
        "reason": "两侧一致",
        "unblock": "",
        "failure_class": None,
    }


def _overall(statuses: Sequence[str]) -> str:
    """The worst status wins: failed beats blocked beats not_run."""
    for candidate in (FAILED, BLOCKED, NOT_RUN):
        if candidate in statuses:
            return candidate
    return HELD


def run_material(spec: MaterialSpec, transport: MaterialTransport, *,
                 reuse: Mapping[str, Any] | None = None) -> dict[str, Any]:
    """Run the three stages for one material and return its record.

    ``reuse`` is a previously written binding record for the same material.
    When it carries a knowledge id, the ingest stage is ``reused`` instead of
    being run again: the same material is not uploaded twice.
    """
    record: dict[str, Any] = {"name": spec.name, "knowledge_id": None}

    if reuse is not None and reuse.get("knowledge_id"):
        knowledge_id = str(reuse["knowledge_id"])
        record["knowledge_id"] = knowledge_id
        ingest = _stage(
            REUSED,
            reason=("本轮没有重新接入：沿用上一轮绑定产物里的资料标识，"
                    "同一套材料不重复上传"),
            evidence={
                "source": "binding artifact",
                "knowledge_id": knowledge_id,
                "sha256": reuse.get("sha256"),
            },
        )
        assertion_sha256 = str(reuse.get("sha256") or "") or None
        assertion_source = "绑定产物（上一轮从系统侧读回的哈希）"
    else:
        ingest = _ingest_stage(spec, transport)
        knowledge_id = str(ingest.get("evidence", {}).get("knowledge_id") or "")
        record["knowledge_id"] = knowledge_id or None
        assertion_sha256 = ingest.get("evidence", {}).get("material_sha256")
        assertion_source = "接入响应报告的材料哈希"

    if not knowledge_id:
        poll = _stage(
            NOT_RUN,
            reason="没有资料标识，处理轮询与检索探针都没有对象",
            unblock=gap_unblock("GAP-WEKNORA-INGEST"),
            failure_class=INGEST_NOT_OBSERVED,
        )
        retrieve = _stage(
            NOT_RUN,
            reason="没有资料标识，检索探针没有对象",
            unblock=gap_unblock("GAP-WEKNORA-INGEST"),
            failure_class=INGEST_NOT_OBSERVED,
        )
    else:
        poll = _poll_stage(spec, transport, knowledge_id)
        retrieve = _retrieve_stage(spec, transport, knowledge_id)

    hash_stage = _hash_stage(spec, assertion_sha256, assertion_source)
    evidence = ready_evidence(poll, retrieve)

    statuses = [ingest["status"], poll["status"], retrieve["status"], hash_stage["status"]]
    overall = _overall(statuses)

    record.update({
        "status": overall,
        "stages": {"ingest": ingest, "poll": poll, "retrieve": retrieve},
        "material_hash": hash_stage,
        "ready_evidence": evidence,
        "binding": {
            "name": spec.name,
            "sha256": spec.sha256,
            "knowledge_id": knowledge_id or None,
            "knowledge_base_id": spec.knowledge_base_id,
            "file_name": spec.file_name,
            "asset_id": (poll.get("evidence", {}).get("binding") or {}).get("asset_id"),
            "asset_revision": (poll.get("evidence", {}).get("binding") or {}).get("asset_revision"),
            "processing_state": (poll.get("evidence", {}).get("binding") or {}).get("processing_state"),
            "strongest_observed_tier": evidence["strongest_observed_tier"],
            "ingest_route": ingest.get("evidence", {}).get("route"),
        },
    })
    return record


# --- the command line ------------------------------------------------------

def parse_material(values: Sequence[str]) -> list[MaterialSpec]:
    """Parse ``--material NAME=SHA256`` (repeatable)."""
    specs: list[MaterialSpec] = []
    for value in values:
        name, separator, digest = value.partition("=")
        digest = digest.strip().lower()
        if not separator or not name.strip():
            raise MaterialError(f"--material takes NAME=SHA256, got: {value}")
        if not SHA256.fullmatch(digest):
            raise MaterialError(
                f"--material {name.strip()!r} needs a 64-character hexadecimal SHA-256, got: {digest!r}")
        specs.append(MaterialSpec(name=name.strip(), sha256=digest))
    return specs


def sha256_of(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def build_transport(arguments: argparse.Namespace) -> MaterialTransport:
    """The transport, with the real one behind two switches."""
    if arguments.real:
        if os.environ.get(REAL_ENVIRONMENT_VARIABLE) != "1":
            raise MaterialError(
                f"--real needs {REAL_ENVIRONMENT_VARIABLE}=1 in the environment as well; "
                "one switch alone does nothing")
        return RealHttpTransport(base_url=arguments.base_url or "",
                                 token=arguments.token or "")
    if not arguments.replay:
        raise MaterialError(
            "this module runs offline by default: give --replay <bundle.json>; "
            "the real transport needs --real and the environment variable")
    bundle = json.loads(arguments.replay.read_text(encoding="utf-8"))
    return OfflineReplayTransport(bundle=bundle, source=str(arguments.replay))


def git_head(root: Path) -> str | None:
    try:
        completed = subprocess.run(["git", "-C", str(root), "rev-parse", "HEAD"],
                                   capture_output=True, text=True, encoding="utf-8",
                                   timeout=30, check=False)
    except (OSError, subprocess.SubprocessError):
        return None
    if completed.returncode:
        return None
    return completed.stdout.strip() or None


def parse_arguments(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="材料接入编排：接入 → 处理轮询 → 检索探针（默认离线回放）")
    parser.add_argument("--replay", type=Path, default=None,
                        help="离线回放包（JSON）；默认模式")
    parser.add_argument("--real", action="store_true",
                        help="真实接入；还需要环境变量 %s=1" % REAL_ENVIRONMENT_VARIABLE)
    parser.add_argument("--base-url", default=os.environ.get("LINGDOC_BASE_URL", ""),
                        help="真实模式的服务地址")
    parser.add_argument("--token", default=os.environ.get("LINGDOC_TOKEN", ""),
                        help="真实模式的访问令牌（不进产物）")
    parser.add_argument("--material", action="append", default=[], metavar="NAME=SHA256",
                        help="运行前记录的材料哈希（可重复）")
    parser.add_argument("--material-file", action="append", default=[], metavar="NAME=PATH",
                        help="材料文件；哈希由字节现算（运行前记录）")
    parser.add_argument("--knowledge-base-id", default="", help="材料要进的知识库标识")
    parser.add_argument("--query", default="", help="检索探针的查询词")
    parser.add_argument("--binding", type=Path, default=None,
                        help="既有绑定产物；本轮据此复用同一套材料")
    parser.add_argument("--out", type=Path, default=None,
                        help="产物目录；默认是系统临时目录")
    parser.add_argument("--head", default=None, help="本轮绑定的提交；默认取当前 HEAD")
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    for stream in (sys.stdout, sys.stderr):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure is not None:
            reconfigure(encoding="utf-8", errors="replace")
    arguments = parse_arguments(argv)
    head = arguments.head or git_head(ROOT)

    try:
        specs = parse_material(arguments.material)
        for value in arguments.material_file:
            name, separator, path_text = value.partition("=")
            if not separator or not name.strip() or not path_text.strip():
                raise MaterialError(f"--material-file takes NAME=PATH, got: {value}")
            path = Path(path_text.strip())
            digest = sha256_of(path)
            specs.append(MaterialSpec(name=name.strip(), sha256=digest,
                                      file_name=path.name))
        if not specs:
            raise MaterialError(
                "没有给出材料：用 --material NAME=SHA256 或 --material-file NAME=PATH 记录材料身份")
        transport = build_transport(arguments)
    except (MaterialError, OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        print(f"beta_material_ingest FAILED: {error}", file=sys.stderr)
        return 1

    binding_records: dict[str, dict[str, Any]] = {}
    if arguments.binding is not None:
        try:
            binding_records = load_binding(arguments.binding)
        except MaterialError as error:
            print(f"beta_material_ingest FAILED: {error}", file=sys.stderr)
            return 1

    results: list[dict[str, Any]] = []
    for spec in specs:
        facts: Mapping[str, Any] = {}
        if hasattr(transport, "material_fields"):
            facts = transport.material_fields(spec.name)  # type: ignore[attr-defined]
        spec = replace(spec,
                       knowledge_base_id=(spec.knowledge_base_id
                                          or facts.get("knowledge_base_id", "")
                                          or arguments.knowledge_base_id),
                       file_name=spec.file_name or facts.get("file_name", ""),
                       query=spec.query or facts.get("query", "") or arguments.query)
        try:
            results.append(run_material(spec, transport,
                                        reuse=binding_records.get(spec.name)))
        except MaterialError as error:
            print(f"beta_material_ingest FAILED: {error}", file=sys.stderr)
            return 1

    overall = _overall([record["status"] for record in results])
    report = {
        "schema": "lingdoc.material.ingest/v1",
        "head": head,
        "transport": {
            "name": getattr(transport, "name", "unknown"),
            "kind": "real" if arguments.real else "offline",
            "real_enabled": bool(arguments.real),
            "source": getattr(transport, "source", ""),
        },
        "overall_status": overall,
        "materials": results,
        "known_limits": _known_limits(arguments, transport, results),
    }

    out_dir = arguments.out
    if out_dir is None:
        out_dir = Path(tempfile.mkdtemp(prefix="lingdoc-material-ingest-"))
    out_dir.mkdir(parents=True, exist_ok=True)
    report_path = out_dir / "material-ingest-report.json"
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n",
                            encoding="utf-8")
    binding_path = out_dir / "material-binding.json"
    write_binding(binding_path, [record["binding"] for record in results], head)

    print(f"beta_material_ingest: overall={overall} materials={len(results)}")
    for record in results:
        tiers = record["ready_evidence"]
        print(f"  {record['name']}: status={record['status']} "
              f"ready_claim={tiers['claim']} "
              f"strongest_tier={tiers['strongest_observed_tier']} "
              f"hash={record['material_hash']['status']}")
    print(f"  report: {report_path}")
    print(f"  binding: {binding_path}")
    return 0 if overall == HELD else 1


def _known_limits(arguments: argparse.Namespace, transport: MaterialTransport,
                  results: Sequence[Mapping[str, Any]]) -> list[str]:
    """What this run did **not** establish."""
    limits: list[str] = []
    if not arguments.real:
        limits.append(
            "本轮是离线回放：没有发起任何 provider 请求、embedding 调用或上传；"
            "结论只覆盖回放包记录的那一段")
    unobserved: list[str] = []
    for record in results:
        for tier in record["ready_evidence"]["tiers"]:
            if not tier["observed"]:
                unobserved.append(f"{record['name']}/{tier['tier']}")
    if unobserved:
        limits.append("未观测到的证据档：" + "、".join(unobserved)
                      + "（未观测不是通过）")
    limits.append(
        "本模块没有接进 F01 契约执行器：执行器接线属于另一张票，"
        "在它合入之前，本模块的产物不被任何 Beta 主流程消费")
    return limits


if __name__ == "__main__":
    raise SystemExit(main())
