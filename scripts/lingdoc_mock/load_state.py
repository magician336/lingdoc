#!/usr/bin/env python3
"""Build one declared starting state into a provider and read it back.

A scenario cannot be driven from nothing: it needs a known starting state. The
contract declares those states (`starting_states` in scenarios.json); this tool
performs the prerequisite write requests (project, spec, members, activation,
chapter versions, asset bindings) and then reads the state back through GET
operations, comparing what it asked for with what the provider holds.

It drives the provider through the runner's own transport (`ProviderClient`)
rather than adding a second one, so opener injection, redirect credential
rules, the 16 MiB response ceiling and JSON decoding stay in one place.

Versions are read from the provider between steps (a create answers
spec_revision 0, so the spec save must send 0, and the activation must send what
the save returned), because the contract's request templates carry placeholder
versions. Nothing here invents a version: each one comes from a response this run
saw.
"""
from __future__ import annotations

import argparse
from dataclasses import dataclass
import hashlib
import json
import os
from pathlib import Path
import re
import sys
from typing import Any
from urllib.parse import urlsplit
import uuid

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:  # `python scripts/lingdoc_mock/load_state.py` has no repo root on the path
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.run_f01 import (  # noqa: E402  (path setup must run first)
    OPENAPI_PATH,
    SCENARIOS_PATH,
    ProviderClient,
    WorkflowError,
    build_url,
    header_value,
    non_empty_string,
    operation_index,
    parse_environment_fact,
    read_json_object,
    write_report,
)


# The parts of the contract's fixture vocabulary this tool can create with write
# requests. Any other name a declaration uses is refused together with its reason
# rather than ignored: a starting state that quietly drops a part is exactly the
# silent half-state this tool exists to prevent.
SUPPORTED_PARTS = ("project", "assets", "chapters")
UNBUILDABLE_PARTS: dict[str, str] = {
    "template": "the service ships the demo template; the contract has no request that creates one",
    "source": "a source is a retrieval hit over a usable asset, not a declarable input",
    "candidate": "a candidate is produced by a generation run, which needs a model adapter",
    "confirmation": "a chapter confirmation needs the review items a candidate carries",
    "release": "a release snapshot is frozen by prepareRelease and needs confirmed chapters",
    "export": "an export artifact is produced by startExport once a snapshot passes its checks",
}
SOURCE_MARKER = re.compile(r"\[\[source:([A-Za-z0-9_-]+)\]\]")
# The probe asks the provider for sources of one bound asset. It runs only where the
# provider answers before searching (an asset it will not use), so it never reaches
# a model: an asset that would be searched is listed by listAssets instead.
PROBE_QUERY = "起点状态读回探针（不取用结果，只为读回资料可用性）"
ASSET_STATES = ("pending", "processing", "ready", "failed", "replaced")


@dataclass(frozen=True)
class DeclaredAsset:
    """A 资料 the state asks for, named the way the contract names it (k-demo)."""

    knowledge_id: str
    processing_state: str


@dataclass(frozen=True)
class DeclaredChapter:
    section_id: str
    body_markdown: str
    source_ids: tuple[str, ...]


@dataclass(frozen=True)
class StartingState:
    """One declared starting state, validated to the shape this tool can build."""

    id: str
    project_name: str
    template_id: str
    status: str
    spec: dict[str, str]
    members: tuple[str, ...]
    assets: tuple[DeclaredAsset, ...]
    chapters: tuple[DeclaredChapter, ...]


def _string_list(value: Any, label: str) -> tuple[str, ...]:
    if not isinstance(value, list):
        raise WorkflowError(f"{label} must be a list of strings")
    return tuple(non_empty_string(item, f"{label} entry") for item in value)


def _project_of(declaration: dict[str, Any], where: str) -> tuple[str, str, str, dict[str, str], tuple[str, ...]]:
    project = declaration.get("project")
    if not isinstance(project, dict):
        raise WorkflowError(f"{where} must declare a project object")
    name = non_empty_string(project.get("name"), f"{where} project name")
    template_id = non_empty_string(project.get("template_id"), f"{where} project template_id")
    status = project.get("status")
    if status not in {"draft", "active"}:
        raise WorkflowError(f"{where} project status must be draft or active, got {status!r}")
    spec = project.get("spec", {})
    if not isinstance(spec, dict):
        raise WorkflowError(f"{where} project spec must be an object of strings")
    fields = {non_empty_string(key, f"{where} project spec key"): non_empty_string(value, f"{where} project spec value")
              for key, value in spec.items()}
    if not fields:
        # Every scenario that reads a project's conditions needs them saved, so a state
        # declares them; a project with no spec at all is F01's own first two steps.
        raise WorkflowError(f"{where} must declare the project spec its scenarios read")
    members = _string_list(project.get("members", []), f"{where} project members")
    return name, template_id, status, fields, members


def _assets_of(declaration: dict[str, Any], where: str) -> tuple[DeclaredAsset, ...]:
    assets = declaration.get("assets", [])
    if not isinstance(assets, list):
        raise WorkflowError(f"{where} assets must be a list")
    declared: list[DeclaredAsset] = []
    for index, asset in enumerate(assets, start=1):
        if not isinstance(asset, dict):
            raise WorkflowError(f"{where} asset {index} must be an object")
        knowledge_id = non_empty_string(asset.get("knowledge_id"), f"{where} asset {index} knowledge_id")
        state = asset.get("processing_state")
        if state not in ASSET_STATES:
            raise WorkflowError(f"{where} asset {index} processing_state must be one of {list(ASSET_STATES)}")
        declared.append(DeclaredAsset(knowledge_id=knowledge_id, processing_state=state))
    return tuple(declared)


def _chapters_of(declaration: dict[str, Any], where: str) -> tuple[DeclaredChapter, ...]:
    chapters = declaration.get("chapters", [])
    if not isinstance(chapters, list):
        raise WorkflowError(f"{where} chapters must be a list")
    declared: list[DeclaredChapter] = []
    for index, chapter in enumerate(chapters, start=1):
        if not isinstance(chapter, dict):
            raise WorkflowError(f"{where} chapter {index} must be an object")
        section_id = non_empty_string(chapter.get("section_id"), f"{where} chapter {index} section_id")
        body = chapter.get("body_markdown", "")
        if not isinstance(body, str):
            raise WorkflowError(f"{where} chapter {index} body_markdown must be a string; "
                                "an empty one declares a template section with no body yet")
        source_ids = _string_list(chapter.get("source_ids", []), f"{where} chapter {index} source_ids")
        # The provider accepts a body only when its [[source:...]] markers are exactly
        # the declared references; say so here instead of letting it answer 400.
        markers = sorted(set(SOURCE_MARKER.findall(body)))
        if markers != sorted(set(source_ids)):
            raise WorkflowError(
                f"{where} chapter {index} body markers {markers} do not match source_ids {sorted(set(source_ids))}")
        declared.append(DeclaredChapter(section_id=section_id, body_markdown=body, source_ids=source_ids))
    return tuple(declared)


def state_from_declaration(declaration: dict[str, Any], where: str) -> StartingState:
    """Validate one declaration into a state, refusing every part this tool cannot build."""
    if not isinstance(declaration, dict):
        raise WorkflowError(f"{where} must be an object")
    for part in declaration:
        if part in {"id", "name", "note"} or part in SUPPORTED_PARTS:
            continue
        reason = UNBUILDABLE_PARTS.get(part)
        if reason is None:
            raise WorkflowError(f"{where} declares unknown part {part}; this tool builds "
                                f"{'/'.join(SUPPORTED_PARTS)} and refuses what it cannot build")
        raise WorkflowError(f"{where} declares {part}, which cannot be built: {reason}")
    state_id = non_empty_string(declaration.get("id"), f"{where} id")
    name, template_id, status, spec, members = _project_of(declaration, where)
    chapters = _chapters_of(declaration, where)
    if status == "draft" and chapters:
        raise WorkflowError(f"{where} declares draft chapters; template sections only exist once the project is active")
    return StartingState(id=state_id, project_name=name, template_id=template_id, status=status, spec=spec,
                         members=members, assets=_assets_of(declaration, where), chapters=chapters)


def load_state(path: Path, state_id: str) -> StartingState:
    """Load one declared starting state out of the contract's states document."""
    document = read_json_object(path, "starting states")
    entries = document.get("starting_states")
    if not isinstance(entries, list) or not entries:
        raise WorkflowError(f"{path.name} declares no starting_states")
    entry = next((item for item in entries if isinstance(item, dict) and item.get("id") == state_id), None)
    if entry is None:
        raise WorkflowError(f"{path.name} has no starting state {state_id}")
    return state_from_declaration(entry, f"starting state {state_id}")


def body_shape(openapi: dict[str, Any], operation_id: str) -> tuple[frozenset[str], frozenset[str]]:
    """The required and allowed request-body property names of one operation."""
    for methods in openapi.get("paths", {}).values():
        for operation in methods.values():
            if operation.get("operationId") != operation_id:
                continue
            schema = operation.get("requestBody", {}).get("content", {}).get("application/json", {}).get("schema")
            if not isinstance(schema, dict):
                raise WorkflowError(f"{operation_id} has no JSON request body in the contract")
            if "$ref" in schema:
                schema = openapi.get("components", {}).get("schemas", {}).get(str(schema["$ref"]).rsplit("/", 1)[-1], {})
            properties = schema.get("properties")
            if not isinstance(properties, dict):
                raise WorkflowError(f"{operation_id} declares no request body properties")
            return frozenset(schema.get("required") or ()), frozenset(properties)
    raise WorkflowError(f"OpenAPI operation not found: {operation_id}")


def _provider_error_code(payload: Any) -> str:
    """Quote the provider's own error code, never its message or body."""
    if isinstance(payload, dict) and isinstance(payload.get("error"), dict):
        code = payload["error"].get("code")
        if isinstance(code, str):
            return code
    return ""


def _digest(value: Any) -> str:
    """A stable digest of one report section: key order and spacing never change it."""
    canonical = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def _denied_reason(payload: Any, asset_id: str) -> str:
    """Read one asset's deny reason out of a retrieval preflight refusal."""
    details = payload.get("error", {}).get("details") if isinstance(payload, dict) else None
    denied = details.get("denied") if isinstance(details, dict) else None
    for entry in denied if isinstance(denied, list) else []:
        if isinstance(entry, dict) and entry.get("asset_id") == asset_id:
            return str(entry.get("reason", ""))
    return ""


class Bindings:
    """One kind of name a declaration uses, and the real ids the environment supplies.

    The flag that supplies it, the word a message uses for it and the map itself are one
    object, so they cannot drift apart; and the names actually asked for are recorded, so a
    supplied name the declaration never uses can be refused instead of ignored.
    """

    def __init__(self, flag: str, label: str) -> None:
        self.flag = flag
        self.label = label
        self.supplied: dict[str, str] = {}
        self.used: set[str] = set()

    def bind(self, name: str, resolved: str) -> None:
        self.supplied[name] = resolved

    def resolve(self, name: str) -> str:
        """The real id for a name the declaration uses, or a refusal naming the flag."""
        resolved = self.supplied.get(name)
        if not resolved:
            raise WorkflowError(f"the environment supplies no {self.label} for {name}; "
                                f"bind the declared name with --{self.flag} {name}=<id>")
        self.used.add(name)
        return resolved

    def refuse_unused(self) -> None:
        """Refuse a supplied name the declaration never asks for: that is how a typo hides."""
        unused = sorted(set(self.supplied) - self.used)
        if unused:
            raise WorkflowError(f"--{self.flag} supplies {', '.join(unused)}, which {self.label} names "
                                "this state never declares; a binding that goes nowhere is how a typo hides")


class StateLoader:
    """Drive the build of one declared state and read it back."""

    def __init__(self, openapi: dict[str, Any], state: StartingState, client: ProviderClient,
                 knowledge: Bindings, member: Bindings) -> None:
        _, self.operations = operation_index(openapi)
        self.openapi = openapi
        self.state = state
        self.client = client
        self.knowledge = knowledge
        self.member = member
        # The client resolved the base URL once (command line over the contract's server
        # entry) and the report reads it back from there: resolving it a second way is how a
        # run ends up on the mock address while the operator believes otherwise.
        self.base_url = client.base_url
        self.build_steps: list[dict[str, Any]] = []
        self.readback_steps: list[dict[str, Any]] = []
        self.identifiers: dict[str, str] = {}
        self.asset_ids: dict[str, str] = {}
        self.active_step: dict[str, str] | None = None

    # ---- one provider exchange -------------------------------------------

    def _exchange(self, step_id: str, operation_id: str, path_params: dict[str, Any], body: Any,
                  headers: dict[str, str]) -> tuple[int, Any, dict[str, Any]]:
        method, template = self.operations[operation_id]
        url = build_url(self.base_url, template, path_params, {})
        self.active_step = {"id": step_id, "operation_id": operation_id}
        status, response_headers, payload = self.client.request(method, url, headers, body)
        return status, payload, {"id": step_id, "operation_id": operation_id, "http_status": status,
                                 "content_type": header_value(response_headers, "Content-Type")}

    @staticmethod
    def _provider_reason(payload: Any) -> str:
        code = _provider_error_code(payload)
        return f" ({code})" if code else ""

    @staticmethod
    def _data(step_id: str, operation_id: str, payload: Any) -> Any:
        if not isinstance(payload, dict) or "data" not in payload:
            raise WorkflowError(f"{step_id} {operation_id}: response carries no data envelope")
        return payload["data"]

    def _read(self, step_id: str, operation_id: str, path_params: dict[str, Any]) -> Any:
        status, payload, record = self._exchange(step_id, operation_id, path_params, None, {})
        if status != 200:
            raise WorkflowError(f"{step_id} {operation_id}: HTTP {status}{self._provider_reason(payload)}; expected 200")
        self.readback_steps.append(record)
        return self._data(step_id, operation_id, payload)

    def _write(self, step_id: str, operation_id: str, path_params: dict[str, Any], body: dict[str, Any],
               expected_http: int) -> Any:
        required, allowed = body_shape(self.openapi, operation_id)
        if not required <= frozenset(body) or not frozenset(body) <= allowed:
            raise WorkflowError(f"{step_id} {operation_id}: this build sends body fields {sorted(body)}; "
                                f"the contract allows {sorted(allowed)} and requires {sorted(required)}")
        headers = {"Idempotency-Key": f"state-{self.state.id}-{step_id}-{uuid.uuid4().hex}"[:128]}
        status, payload, record = self._exchange(step_id, operation_id, path_params, body, headers)
        if status != expected_http:
            raise WorkflowError(f"{step_id} {operation_id}: HTTP {status}{self._provider_reason(payload)}; "
                                f"expected {expected_http}")
        self.build_steps.append(record)
        return self._data(step_id, operation_id, payload)

    # ---- environment facts the declaration names --------------------------

    def asset_id(self, name: str) -> str:
        """The provider's id for a binding this run created (see _bind_assets)."""
        try:
            return self.asset_ids[name]
        except KeyError:
            raise WorkflowError(f"asset {name} was never bound, so it has no provider id") from None

    def preflight(self) -> None:
        """Refuse, before the first write, every name the run cannot honour in full:
        one the environment does not supply, and one it supplies that nothing declares."""
        for asset in self.state.assets:
            self.knowledge.resolve(asset.knowledge_id)
        for name in self.state.members:
            self.member.resolve(name)
        self.knowledge.refuse_unused()
        self.member.refuse_unused()

    # ---- build ------------------------------------------------------------

    def build(self) -> None:
        """Issue the prerequisite writes, taking every version from a response this run saw."""
        state = self.state
        step = f"{state.id}-create"
        project = self._write(step, "createProject", {},
                              {"name": state.project_name, "template_id": state.template_id}, 201)
        project_id = non_empty_string(project.get("id"), f"{step} created project id")
        self.identifiers["project_id"] = project_id
        step = f"{state.id}-spec"
        project = self._write(step, "saveSpec", {"projectId": project_id},
                              {"expected_spec_revision": project.get("spec_revision"), "fields": state.spec}, 200)
        spec_revision = project.get("spec_revision")
        if state.members:
            step = f"{state.id}-members"
            project = self._write(step, "saveMembers", {"projectId": project_id},
                                  {"expected_project_version": project.get("project_version"),
                                   "collaborator_user_ids": sorted(self.member.resolve(name) for name in state.members)}, 200)
        if state.status == "active":
            step = f"{state.id}-activate"
            project = self._write(step, "activateProject", {"projectId": project_id},
                                  {"expected_spec_revision": spec_revision}, 200)
            self._save_chapters(project_id, project.get("spec_revision"))
        # Binding is its own step, not part of activation: the provider accepts a bound asset
        # on a draft project too, and a draft that declares one has to be built and read back
        # like any other state instead of dying on an unbound id.
        self._bind_assets(project_id)
        self.active_step = None

    def _save_chapters(self, project_id: str, spec_revision: Any) -> None:
        state = self.state
        step = f"{state.id}-chapters"
        by_section = {chapter["section_id"]: chapter
                      for chapter in self._read(step, "listChapters", {"projectId": project_id})
                      if isinstance(chapter, dict) and isinstance(chapter.get("section_id"), str)}
        for index, declared in enumerate(state.chapters, start=1):
            chapter = by_section.get(declared.section_id)
            if chapter is None:
                raise WorkflowError(f"{state.id} chapter {index}: the template has no section {declared.section_id}")
            self.identifiers[f"chapter.{declared.section_id}"] = str(chapter.get("id", ""))
            if not declared.body_markdown:
                continue  # A declared empty body means the section exists with no version: nothing to write.
            step = f"{state.id}-chapter-{index}"
            self._write(step, "saveChapter", {"projectId": project_id, "chapterId": chapter.get("id")},
                        {"expected_chapter_version_id": chapter.get("current_version_id"),
                         "expected_spec_revision": spec_revision, "body_markdown": declared.body_markdown,
                         "source_ids": sorted(set(declared.source_ids))}, 201)

    def _bind_assets(self, project_id: str) -> None:
        for index, asset in enumerate(self.state.assets, start=1):
            step = f"{self.state.id}-asset-{index}"
            resolved = self.knowledge.resolve(asset.knowledge_id)
            bound = self._write(step, "bindAsset", {"projectId": project_id}, {"knowledge_id": resolved}, 201)
            self.identifiers[f"asset.{asset.knowledge_id}"] = resolved
            # The retrieval preflight addresses an asset by its own id, and the only place
            # that id is readable is this bind response: listAssets lists usable assets
            # only, so a bound asset that is not usable has no other disclosure.
            self.asset_ids[asset.knowledge_id] = non_empty_string(bound.get("id"), f"{step} bound asset id")

    # ---- read back --------------------------------------------------------

    def probe_asset(self, project_id: str, step_id: str, asset_id: str) -> str:
        """Ask the provider whether a bound asset is usable, and why not.

        `asset_id` is the asset's own id, not its knowledge id: asset_ids names bindings,
        and an id this project has no binding for is answered `not_found` however real the
        knowledge is. The provider refuses an asset it will not use before searching, so
        this never reaches a model; an asset that would be searched is listed by listAssets.
        """
        status, payload, record = self._exchange(step_id, "retrieveSources", {"projectId": project_id},
                                                 {"query": PROBE_QUERY, "asset_ids": [asset_id]}, {})
        if status not in {200, 422}:
            raise WorkflowError(f"{step_id} retrieveSources: HTTP {status}{self._provider_reason(payload)}")
        self.readback_steps.append(record)
        return "" if status == 200 else _denied_reason(payload, asset_id)

    def read_back(self) -> dict[str, Any]:
        """Read the built state through GET operations and compare it with the declaration."""
        state = self.state
        project_id = self.identifiers.get("project_id")
        if not project_id:
            raise WorkflowError(f"{state.id}: nothing was built, so there is nothing to read back")
        # Read-back step ids carry the state id like the build steps do, so one report never
        # mixes two naming schemes and two states' reports stay readable side by side.
        prefix = state.id
        project = self._read(f"{prefix}-read-project", "getProject", {"projectId": project_id})
        chapters = self._read(f"{prefix}-read-chapters", "listChapters", {"projectId": project_id})
        # Read the bound assets even when the declaration names none: "no usable asset"
        # is part of what a state asserts, and an undeclared one has to be visible.
        assets = self._read(f"{prefix}-read-assets", "listAssets", {"projectId": project_id})
        return self.compare(project, chapters, assets)

    def compare(self, project: Any, chapters: Any, assets: Any) -> dict[str, Any]:
        """Compare a read-back state with the declaration and report every difference."""
        state = self.state
        mismatches: list[dict[str, Any]] = []
        # Declared facts no channel can confirm are named here rather than asserted as
        # verified: a declaration that is only echoed back is not evidence about the provider.
        unverified: list[dict[str, Any]] = []
        project = project if isinstance(project, dict) else {}
        chapter_list = chapters if isinstance(chapters, list) else []
        asset_list = assets if isinstance(assets, list) else []

        def check(pointer: str, declared: Any, actual: Any) -> None:
            if declared != actual:
                mismatches.append({"pointer": pointer, "declared": declared, "actual": actual})

        check("/project/name", state.project_name, project.get("name"))
        check("/project/template_id", state.template_id, project.get("template_id"))
        check("/project/status", state.status, project.get("status"))
        check("/project/spec", state.spec, project.get("spec"))

        by_section = {chapter["section_id"]: chapter for chapter in chapter_list
                      if isinstance(chapter, dict) and isinstance(chapter.get("section_id"), str)}
        declared_sections = {chapter.section_id: chapter for chapter in state.chapters}
        snapshot_chapters = []
        for section_id, chapter in sorted(by_section.items()):
            declared = declared_sections.get(section_id)
            check(f"/chapters/{section_id}/body_markdown", declared.body_markdown if declared else "",
                  chapter.get("body_markdown"))
            check(f"/chapters/{section_id}/source_ids", sorted(set(declared.source_ids)) if declared else [],
                  sorted(chapter.get("source_ids") or []))
            check(f"/chapters/{section_id}/confirmation_valid", False, chapter.get("confirmation_valid"))
            if declared is not None and not declared.body_markdown:
                check(f"/chapters/{section_id}/current_version_id", None, chapter.get("current_version_id"))
            # The snapshot records the declared content the read-back confirmed, so two
            # runs of one declaration produce the same snapshot while it is still a
            # statement about what the provider actually holds.
            snapshot_chapters.append({"section_id": section_id,
                                      "body_markdown": declared.body_markdown if declared else "",
                                      "source_ids": sorted(set(declared.source_ids)) if declared else []})
        for section_id in sorted(declared_sections):
            if section_id not in by_section:
                mismatches.append({"pointer": f"/chapters/{section_id}", "declared": "由模板生成",
                                   "actual": "读回时不存在"})
        if state.status == "draft" and by_section:
            mismatches.append({"pointer": "/chapters", "declared": "尚未立项，没有章节",
                               "actual": f"读回 {len(by_section)} 节"})

        members = project.get("members") if isinstance(project.get("members"), list) else []
        roles = sorted(str(member.get("role")) for member in members if isinstance(member, dict))
        check("/project/members/roles", sorted(["owner"] + ["collaborator"] * len(state.members)), roles)
        member_ids = {str(member.get("user_id")) for member in members if isinstance(member, dict)}
        for name in state.members:
            try:
                resolved = self.member.resolve(name)
            except WorkflowError as error:
                mismatches.append({"pointer": f"/project/members/{name}", "declared": "协作者", "actual": str(error)})
            else:
                if resolved not in member_ids:
                    mismatches.append({"pointer": f"/project/members/{name}", "declared": "协作者",
                                       "actual": "读回时不在成员里"})

        project_id = self.identifiers.get("project_id", "")
        usable = {asset.get("knowledge_id"): asset for asset in asset_list if isinstance(asset, dict)}
        snapshot_assets, observed_assets = [], []
        for index, declared_asset in enumerate(state.assets, start=1):
            resolved = self.knowledge.resolve(declared_asset.knowledge_id)
            listed = usable.get(resolved)
            pointer = f"/assets/{declared_asset.knowledge_id}"
            probe_step = f"{state.id}-read-asset-{index}"
            if declared_asset.processing_state == "ready":
                if listed is None:
                    reason = self.probe_asset(project_id, probe_step, self.asset_id(declared_asset.knowledge_id))
                    mismatches.append({"pointer": pointer, "declared": "已绑定且可用",
                                       "actual": f"listAssets 未列出{self._deny_text(reason)}"})
                else:
                    check(f"{pointer}/processing_state", "ready", listed.get("processing_state"))
            elif listed is not None:
                mismatches.append({"pointer": pointer, "declared": f"不可用（{declared_asset.processing_state}）",
                                   "actual": "listAssets 把它列为可用资料"})
            else:
                reason = self.probe_asset(project_id, probe_step, self.asset_id(declared_asset.knowledge_id))
                if reason != "not_ready":
                    mismatches.append({"pointer": pointer, "declared": "已绑定但不可用",
                                       "actual": self._deny_text(reason).lstrip("：") or "检索预检未拒绝这条资料"})
                else:
                    # The provider says "unusable", the declaration says which kind of unusable.
                    # Nothing on this provider separates pending / processing / failed /
                    # replaced, so the finer claim is recorded as unverified, not as confirmed.
                    unverified.append({"pointer": pointer, "declared": f"处理状态 {declared_asset.processing_state}",
                                       "verified": "不可用（not_ready）",
                                       "why": "提供方只答「不可用」；pending / processing / failed / replaced "
                                              "之间没有任何通道能区分"})
            snapshot_assets.append({"knowledge_id": declared_asset.knowledge_id})
            observed_assets.append({"knowledge_id": declared_asset.knowledge_id,
                                    "listed_state": (listed or {}).get("processing_state"),
                                    "deny_reason": "" if listed is not None else reason})
        declared_knowledge = {self.knowledge.resolve(asset.knowledge_id) for asset in state.assets}
        for knowledge_id in sorted(set(usable) - declared_knowledge):
            mismatches.append({"pointer": "/assets", "declared": "没有其他资料",
                               "actual": f"多出一条可用资料 {knowledge_id}"})

        # The snapshot is the declared state, recorded as the read-back confirmed it, and it
        # is built from the declaration rather than from the responses so that repeating a
        # declaration reproduces it exactly. Anything the provider answered differently
        # appears in `mismatches` (which fails the run) or in `unverified` (which names what
        # no channel could confirm), so the snapshot is never a quietly-adjusted record.
        snapshot = {
            "project": {"name": state.project_name, "template_id": state.template_id, "status": state.status,
                        "spec": state.spec,
                        "members": [{"role": "owner"}] + [{"role": "collaborator", "user_id": name}
                                                          for name in state.members]},
            "chapters": snapshot_chapters,
            "assets": snapshot_assets,
        }
        observed = {
            "project_version": project.get("project_version"),
            "spec_revision": project.get("spec_revision"),
            "chapters": [{"section_id": section_id, "has_version": chapter.get("current_version_id") is not None}
                         for section_id, chapter in sorted(by_section.items())],
            "assets": observed_assets,
        }
        return {"snapshot": snapshot, "snapshot_digest": _digest(snapshot),
                # `observed` holds the facts the read-back itself produced, and its digest is
                # what makes "run the same declaration twice, get the same read-back" a
                # statement about the provider: the snapshot digest alone is the declaration
                # compared with itself, which proves nothing about repeatability.
                "observed": observed, "observed_digest": _digest(observed),
                "mismatches": mismatches, "unverified": unverified}

    @staticmethod
    def _deny_text(reason: str) -> str:
        return {"": "", "not_ready": "：资料未就绪（not_ready）", "not_authorized": "：调用者无权读取（not_authorized）",
                "not_found": "：提供方不认这条绑定（not_found）"}.get(reason, f"：{reason}")

    def report(self, status: str, settled: dict[str, Any] | None = None) -> dict[str, Any]:
        # Deliberately free of run identifiers, tokens and provider bodies, like the
        # runner's report: the snapshot holds declared names, not the provider's ids.
        settled = settled or {}
        result: dict[str, Any] = {
            "state": self.state.id,
            "runner_status": status,
            "build_steps": list(self.build_steps),
            "readback_steps": list(self.readback_steps),
            "snapshot": settled.get("snapshot", {}),
            "snapshot_digest": settled.get("snapshot_digest", ""),
            "observed": settled.get("observed", {}),
            "observed_digest": settled.get("observed_digest", ""),
            "mismatches": settled.get("mismatches", []),
            "unverified": settled.get("unverified", []),
            "cannot_build": [{"part": part, "reason": reason} for part, reason in UNBUILDABLE_PARTS.items()],
            "provider_host": urlsplit(self.base_url).netloc,
            "verification_scope": "declared_state_read_back_only",
            "provider_semantics_status": "not_verified",
        }
        if status == "failed" and self.active_step is not None:
            result["failed_step"] = dict(self.active_step)
        return result


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Build one declared starting state into a provider and read it back.")
    parser.add_argument("--state", required=True, help="starting state id declared in the states document")
    parser.add_argument("--states", type=Path, default=SCENARIOS_PATH, help="document holding starting_states")
    parser.add_argument("--openapi", type=Path, default=OPENAPI_PATH)
    parser.add_argument("--base-url", help="OpenAPI server base URL; defaults to the first server in openapi.json")
    parser.add_argument("--token", default=os.environ.get("LINGDOC_TEST_TOKEN"), help="short-lived test bearer token (or LINGDOC_TEST_TOKEN)")
    parser.add_argument("--knowledge", action="append", default=[], metavar="NAME=ID",
                        help="bind a declared asset name to a real knowledge id (repeatable)")
    parser.add_argument("--member", action="append", default=[], metavar="NAME=ID",
                        help="bind a declared member name to a real user id (repeatable)")
    parser.add_argument("--request-timeout", type=float, default=20)
    parser.add_argument("--report", type=Path, help="write a sanitized JSON record of the build and the read-back")
    args = parser.parse_args(argv)
    loader: StateLoader | None = None
    report_started = False
    try:
        knowledge = Bindings("knowledge", "knowledge")
        member = Bindings("member", "user")
        for value in args.knowledge:
            name, resolved = parse_environment_fact(value, "knowledge")
            knowledge.bind(name, resolved)
        for value in args.member:
            name, resolved = parse_environment_fact(value, "member")
            member.bind(name, resolved)
        state = load_state(args.states, args.state)
        openapi = read_json_object(args.openapi, "OpenAPI document")
        server_url, _ = operation_index(openapi)
        client = ProviderClient(args.base_url or server_url, token=args.token, timeout=args.request_timeout)
        loader = StateLoader(openapi, state, client, knowledge, member)
        loader.preflight()
        if args.report:
            # Verify the destination before any provider mutation; a build that dies
            # halfway then leaves an explicit record instead of a stale success.
            write_report(args.report, loader.report("not_started"))
            report_started = True
        loader.build()
        settled = loader.read_back()
        for mismatch in settled["mismatches"]:
            print(f"MISMATCH {mismatch['pointer']}: declared {mismatch['declared']!r}, "
                  f"read back {mismatch['actual']!r}", file=sys.stderr)
        if settled["mismatches"]:
            print(f"{state.id} FAILED: the built state does not match the declaration "
                  f"({len(settled['mismatches'])} difference(s))", file=sys.stderr)
            if args.report:
                write_report(args.report, loader.report("failed", settled))
            return 1
        if args.report:
            write_report(args.report, loader.report("completed", settled))
            print(f"{state.id} report written to {args.report}")
        print(json.dumps({"state": state.id, "identifiers": loader.identifiers,
                          "snapshot_digest": settled["snapshot_digest"]}, ensure_ascii=False))
    except (WorkflowError, OSError) as error:
        failed = loader.state.id if loader is not None else args.state
        print(f"{failed} FAILED: {error}", file=sys.stderr)
        if report_started and loader is not None:
            try:
                write_report(args.report, loader.report("failed"))
            except WorkflowError as report_error:
                print(f"{loader.state.id} REPORT FAILED: {report_error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
